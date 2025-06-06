package models

import (
	"errors"
	"net/http"
	"net/http/httputil"

	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/context"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/logging"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/subjectaccessreview"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/tokenreview"
	"github.com/Improwised/kube-oidc-proxy/pkg/util"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/kubernetes/plugin/pkg/auth/authorizer/rbac"
)

type Cluster struct {
	Name                  string
	Path                  string
	RestConfig            *rest.Config
	Kubeclient            *kubernetes.Clientset
	TokenReviewer         *tokenreview.TokenReview
	SubjectAccessReviewer *subjectaccessreview.SubjectAccessReview
	Authorizer            *rbac.RBACAuthorizer
	RBACConfig            *util.RBAC
	ProxyHandler          *httputil.ReverseProxy
	ClientTransport       http.RoundTripper
	NoAuthClientTransport http.RoundTripper
	IsStatic              bool
}

var (
	ErrNoImpersonationConfig = errors.New("no impersonation configuration in context")
)

// RoundTrip is called last and is used to manipulate the forwarded request using context.
func (c *Cluster) RoundTrip(req *http.Request) (*http.Response, error) {
	// Here we have successfully authenticated so now need to determine whether
	// we need use impersonation or not.

	// If no impersonation then we return here without setting impersonation
	// header but re-introduce the token we removed.
	if context.NoImpersonation(req) {
		token := context.BearerToken(req)
		req.Header.Add("Authorization", token)
		return c.NoAuthClientTransport.RoundTrip(req)
	}

	// Get the impersonation headers from the context.
	impersonationConf := context.ImpersonationConfig(req)
	if impersonationConf == nil {
		return nil, ErrNoImpersonationConfig
	}

	// Log the request
	logging.LogSuccessfulRequest(req, *impersonationConf.InboundUser, *impersonationConf.ImpersonatedUser)

	// Push request as admin through round trippers to the API server.
	return c.ClientTransport.RoundTrip(req)
}
