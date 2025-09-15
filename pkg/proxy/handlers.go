// Copyright Jetstack Ltd. See LICENSE for details.
package proxy

import (
	"encoding/json"
	"net/http"
	"strings"

	authuser "k8s.io/apiserver/pkg/authentication/user"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/client-go/transport"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/kubeapiserver/admission/exclusion"

	"github.com/Improwised/kube-oidc-proxy/pkg/cluster"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/audit"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/context"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/logging"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/subjectaccessreview"
	"github.com/Improwised/kube-oidc-proxy/pkg/util/authorizer"
)

func (p *Proxy) withHandlers(handler http.Handler) http.Handler {
	// Set up proxy handlers

	handler = p.auditor.WithCustomAuditLog(handler)
	// handler = p.auditor.WithRequest(handler)
	handler = p.WithRBACHandler(handler)
	handler = p.withImpersonateRequest(handler)
	handler = p.withAuthenticateRequest(handler)

	// Add the auditor backend as a shutdown hook
	p.hooks.AddPreShutdownHook("AuditBackend", p.auditor.Shutdown)

	return handler
}

func (p *Proxy) WithRBACHandler(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {

		clusterName := p.GetClusterName(req.URL.Path)
		req.URL.Path = strings.TrimPrefix(req.URL.Path, "/"+clusterName)

		reqInfo, err := p.requestInfo.NewRequestInfo(req)
		if err != nil {
			p.handleError(rw, req, err)
			return
		}

		// skip validation in Excluded resourse
		// Group: "authentication.k8s.io", Resource: "selfsubjectreviews",
		// Group: "authentication.k8s.io", Resource: "tokenreviews",
		// Group: "authorization.k8s.io", Resource: "localsubjectaccessreviews",
		// Group: "authorization.k8s.io", Resource: "selfsubjectaccessreviews",
		// Group: "authorization.k8s.io", Resource: "selfsubjectrulesreviews",
		// Group: "authorization.k8s.io", Resource: "subjectaccessreviews",
		for _, groupResource := range exclusion.Excluded() {
			if groupResource.Group == reqInfo.APIGroup && groupResource.Resource == reqInfo.Resource {
				req.URL.Path = "/" + clusterName + req.URL.Path
				handler.ServeHTTP(rw, req)
				return
			}
		}

		// add request info into context
		req = req.WithContext(context.WithRequestInfo(req.Context(), reqInfo))

		// validate resource request
		if reqInfo.IsResourceRequest {
			user, ok := genericapirequest.UserFrom(req.Context())
			if !ok {
				p.handleError(rw, req, errUnauthorized)
				return
			}

			// Check permission using our custom authorizer
			authorized := p.clusterManager.CheckPermission(authorizer.SubjectTypeUser, user.GetName(), clusterName, reqInfo.Namespace, reqInfo.Resource, reqInfo.Verb)

			if !authorized {
				klog.V(10).Infof("user %s not authorized to %s %s in namespace %s", user.GetName(), reqInfo.Verb, reqInfo.Resource, reqInfo.Namespace)
				p.handleError(rw, req, errUnauthorized)
				return
			}
		}

		// Eg. non resource request
		// 		/api
		//		/version etc..
		req.URL.Path = "/" + clusterName + req.URL.Path
		handler.ServeHTTP(rw, req)

	})
}

// withAuthenticateRequest adds the proxy authentication handler to a chain.
func (p *Proxy) withAuthenticateRequest(handler http.Handler) http.Handler {
	tokenReviewHandler := p.withTokenReview(handler)

	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		// Auth request and handle unauthed
		info, ok, err := p.oidcRequestAuther.AuthenticateRequest(req)
		if err != nil {
			klog.V(5).Infof("Authenticated request failed: %s", err)
			// Since we have failed OIDC auth, we will try a token review, if enabled.
			tokenReviewHandler.ServeHTTP(rw, req)
			return
		}

		// Failed authorization
		if !ok {
			p.handleError(rw, req, errUnauthorized)
			return
		}

		var remoteAddr string
		req, remoteAddr = context.RemoteAddr(req)

		klog.V(4).Infof("authenticated request: %s", remoteAddr)

		// Add the user info to the request context
		req = req.WithContext(genericapirequest.WithUser(req.Context(), info.User))
		handler.ServeHTTP(rw, req)
	})
}

// withTokenReview will attempt a token review on the incoming request, if
// enabled.
func (p *Proxy) withTokenReview(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		// If token review is not enabled then error.
		if !p.config.TokenReview {
			p.handleError(rw, req, errUnauthorized)
			return
		}

		// Attempt to passthrough request if valid token
		if !p.reviewToken(rw, req) {
			// Token review failed so error
			p.handleError(rw, req, errUnauthorized)
			return
		}

		// Set no impersonation headers and re-add removed headers.
		req = context.WithNoImpersonation(req)

		handler.ServeHTTP(rw, req)
	})
}

// withImpersonateRequest adds the impersonation request handler to the chain.
func (p *Proxy) withImpersonateRequest(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		// If no impersonation has already been set, return early
		if context.NoImpersonation(req) {
			handler.ServeHTTP(rw, req)
			return
		}

		var targetForContext authuser.Info
		targetForContext = nil

		var remoteAddr string
		req, remoteAddr = context.RemoteAddr(req)

		// If we have disabled impersonation we can forward the request right away
		if p.config.DisableImpersonation {
			klog.V(2).Infof("passing on request with no impersonation: %s", remoteAddr)
			// Indicate we need to not use impersonation.
			req = context.WithNoImpersonation(req)
			handler.ServeHTTP(rw, req)
			return
		}

		user, ok := genericapirequest.UserFrom(req.Context())
		// No name available so reject request
		if !ok || len(user.GetName()) == 0 {
			p.handleError(rw, req, errNoName)
			return
		}

		userForContext := user

		if p.hasImpersonation(req.Header) {
			// if impersonation headers are present, let's check to see
			// if the user is authorized to perform the impersonation
			target, err := p.clusterManager.GetCluster(p.GetClusterName(req.URL.Path)).SubjectAccessReviewer.CheckAuthorizedForImpersonation(req, user)

			if err != nil {
				p.handleError(rw, req, err)
				return
			}

			if target != nil {
				// TODO - store original context for logging
				user = target
				targetForContext = target
			}
		}

		// Ensure group contains allauthenticated builtin
		allAuthFound := false
		groups := user.GetGroups()
		for _, elem := range groups {
			if elem == authuser.AllAuthenticated {
				allAuthFound = true
				break
			}
		}
		if !allAuthFound {
			groups = append(groups, authuser.AllAuthenticated)
		}

		extra := user.GetExtra()

		if extra == nil {
			extra = make(map[string][]string)
		}

		// If client IP user extra header option set then append the remote client
		// address.
		if p.config.ExtraUserHeadersClientIPEnabled {
			klog.V(6).Infof("adding impersonate extra user header %s: %s (%s)",
				UserHeaderClientIPKey, remoteAddr, remoteAddr)

			extra[UserHeaderClientIPKey] = append(extra[UserHeaderClientIPKey], remoteAddr)
		}

		// Add custom extra user headers to impersonation request.
		for k, vs := range p.config.ExtraUserHeaders {
			for _, v := range vs {
				klog.V(6).Infof("adding impersonate extra user header %s: %s (%s)",
					k, v, remoteAddr)

				extra[k] = append(extra[k], v)
			}
		}

		if targetForContext != nil {
			// add the original user's information as extra headers
			// so they're recorded in the API server's audit log
			extra["originaluser.jetstack.io-user"] = []string{userForContext.GetName()}

			numGroups := len(userForContext.GetGroups())
			if numGroups > 0 {
				groupNames := make([]string, numGroups)
				for i, groupName := range userForContext.GetGroups() {
					groupNames[i] = groupName
				}

				extra["originaluser.jetstack.io-groups"] = groupNames
			}

			if userForContext.GetUID() != "" {
				extra["originaluser.jetstack.io-uid"] = []string{userForContext.GetUID()}
			}

			if userForContext.GetExtra() != nil && len(userForContext.GetExtra()) > 0 {
				jsonExtras, errJsonMarshal := json.Marshal(userForContext.GetExtra())
				if errJsonMarshal != nil {
					p.handleError(rw, req, errJsonMarshal)
					return
				}
				extra["originaluser.jetstack.io-extra"] = []string{string(jsonExtras)}
			}
		}

		conf := &context.ImpersonationRequest{
			ImpersonationConfig: &transport.ImpersonationConfig{
				UserName: user.GetName(),
				Groups:   groups,
				Extra:    extra,
			},
			InboundUser:      &userForContext,
			ImpersonatedUser: &targetForContext,
		}

		// Add the impersonation configuration to the context.
		req = context.WithImpersonationConfig(req, conf)
		handler.ServeHTTP(rw, req)
	})
}

// newErrorHandler returns a handler failed requests.
func (p *Proxy) newErrorHandler() func(rw http.ResponseWriter, r *http.Request, err error) {

	unauthedHandler := audit.NewUnauthenticatedHandler(p.auditor, func(rw http.ResponseWriter, r *http.Request) {
		klog.V(2).Infof("unauthenticated user request %s", r.RemoteAddr)
		http.Error(rw, "Unauthorized", http.StatusUnauthorized)
	})

	return func(rw http.ResponseWriter, r *http.Request, err error) {

		if err == nil {
			klog.Error("error was called with no error")
			http.Error(rw, "", http.StatusInternalServerError)
			return
		}

		// regardless of reason, log failed auth
		logging.LogFailedRequest(r)

		switch err {

		// Failed auth
		case errUnauthorized:
			// If Unauthorized then error and report to audit
			unauthedHandler.ServeHTTP(rw, r)
			return

			// No name given or available in oidc request
		case errNoName:
			klog.V(2).Infof("no name available in oidc info %s", r.RemoteAddr)
			http.Error(rw, "Username claim not available in OIDC Issuer response", http.StatusForbidden)
			return

			// No impersonation configuration found in context
		case cluster.ErrNoImpersonationConfig:
			klog.Errorf("if you are seeing this, there is likely a bug in the proxy (%s): %s", r.RemoteAddr, err)
			http.Error(rw, "", http.StatusInternalServerError)
			return

			// No impersonation user found
		case subjectaccessreview.ErrorNoImpersonationUserFound:
			http.Error(rw, subjectaccessreview.ErrorNoImpersonationUserFound.Error(), http.StatusInternalServerError)
			return

			// Server or unknown error
		default:

			if strings.Contains(err.Error(), "not allowed to impersonate") {
				klog.V(2).Infof(err.Error(), r.RemoteAddr)
				http.Error(rw, err.Error(), http.StatusForbidden)
			} else {
				klog.Errorf("unknown error (%s): %s", r.RemoteAddr, err)
				http.Error(rw, err.Error(), http.StatusInternalServerError)
			}

		}
	}
}

func (p *Proxy) hasImpersonation(header http.Header) bool {
	for h := range header {
		if strings.HasPrefix(strings.ToLower(h), "impersonate-") {
			return true
		}
	}

	return false
}
