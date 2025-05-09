// Copyright Jetstack Ltd. See LICENSE for details.
package audit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
	genericapifilters "k8s.io/apiserver/pkg/endpoints/filters"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/server"
	genericfilters "k8s.io/apiserver/pkg/server/filters"
	"k8s.io/component-base/version"
	"k8s.io/klog/v2"

	"github.com/Improwised/kube-oidc-proxy/cmd/app/options"
	"github.com/go-resty/resty/v2"
)

type Audit struct {
	opts         *options.AuditOptions
	serverConfig *server.CompletedConfig
	client       *resty.Client
}

type Log struct {
	ClusterName string `json:"cluster_name"`
	// user info
	Email  string              `json:"email"`
	UID    string              `json:"uid"`
	Groups []string            `json:"groups"`
	Extra  map[string][]string `json:"extra"`
	// request info
	IsResourceRequest bool     `json:"is_resource_request"`
	RequestPath       string   `json:"request_path"`
	Verb              string   `json:"verb"`
	APIPrefix         string   `json:"api_prefix"`
	APIGroup          string   `json:"api_group"`
	APIVersion        string   `json:"api_version"`
	Namespace         string   `json:"namespace"`
	Resource          string   `json:"resource"`
	SubResource       string   `json:"sub_resource"`
	Name              string   `json:"name"`
	Parts             []string `json:"parts"`
	FieldSelector     string   `json:"field_selector"`
	LabelSelector     string   `json:"label_selector"`
	// body
	RequestBody json.RawMessage `json:"request_body"`
}

// New creates a new Audit struct to handle auditing for proxy requests. This
// is mostly a wrapper for the apiserver auditing handlers to combine them with
// the proxy.
func New(opts *options.AuditOptions, externalAddress string, secureServingInfo *server.SecureServingInfo) (*Audit, error) {
	serverConfig := &server.Config{
		ExternalAddress: externalAddress,
		SecureServing:   secureServingInfo,

		// Default to treating watch as a long-running operation.
		// Generic API servers have no inherent long-running subresources.
		// This is so watch requests are handled correctly in the audit log.
		LongRunningFunc: genericfilters.BasicLongRunningRequestCheck(
			sets.NewString("watch"), sets.NewString()),
	}

	// We do not support dynamic auditing, so leave nil
	if err := opts.ApplyTo(serverConfig); err != nil {
		return nil, err
	}

	serverConfig.EffectiveVersion = version.NewEffectiveVersion("1.0.31")
	completed := serverConfig.Complete(nil)

	if opts.AuditWebhookServer == "" {
		return nil, fmt.Errorf("audit webhook server is required")
	}

	client := resty.New().SetBaseURL(opts.AuditWebhookServer)
	return &Audit{
		opts:         opts,
		serverConfig: &completed,
		client:       client,
	}, nil
}

// Run will run the audit backend if configured.
func (a *Audit) Run(stopCh <-chan struct{}) error {
	if a.serverConfig.AuditBackend != nil {
		if err := a.serverConfig.AuditBackend.Run(stopCh); err != nil {
			return fmt.Errorf("failed to run the audit backend: %s", err)
		}
	}

	return nil
}

// Shutdown will shutdown the audit backend if configured.
func (a *Audit) Shutdown() error {
	if a.serverConfig.AuditBackend != nil {
		a.serverConfig.AuditBackend.Shutdown()
	}

	return nil
}

// WithRequest will wrap the given handler to inject the request information
// into the context which is then used by the wrapped audit handler.
func (a *Audit) WithRequest(handler http.Handler) http.Handler {
	klog.V(4).Infof("Enabling audit for proxy requests")
	handler = genericapifilters.WithAudit(handler, a.serverConfig.AuditBackend, a.serverConfig.AuditPolicyRuleEvaluator, a.serverConfig.LongRunningFunc)
	handler = genericapifilters.WithAuditInit(handler)
	return genericapifilters.WithRequestInfo(handler, a.serverConfig.RequestInfoResolver)
}

// WithUnauthorized will wrap the given handler to inject the request
// information into the context which is then used by the wrapped audit
// handler.
func (a *Audit) WithUnauthorized(handler http.Handler) http.Handler {
	handler = genericapifilters.WithFailedAuthenticationAudit(handler, a.serverConfig.AuditBackend, a.serverConfig.AuditPolicyRuleEvaluator)
	return genericapifilters.WithRequestInfo(handler, a.serverConfig.RequestInfoResolver)
}

// custrom audit handler
func (a *Audit) WithCustomAuditLog(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		parts := strings.Split(r.URL.Path, "/")
		if len(parts) < 2 {
			klog.V(4).Info("Invalid request: No cluster name in the request")
			handler.ServeHTTP(w, r)
			return
		}
		clusterName := parts[1]

		requestInfo, found := request.RequestInfoFrom(r.Context())
		if !found || !requestInfo.IsResourceRequest {
			klog.V(4).Info("Invalid request: No RequestInfo or not a resource request")
			handler.ServeHTTP(w, r)
			return
		}

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			klog.Errorf("Error reading request body: %v", err)
		}
		if len(bodyBytes) == 0 {
			klog.V(4).Info("Empty request body")
			bodyBytes = []byte("{}")
		}

		// get user info from request
		userInfo, ok := request.UserFrom(r.Context())
		if !ok {
			klog.V(4).Info("No user info found in the request")
		}

		a.SendAuditLog(Log{
			ClusterName: clusterName,
			// user info
			Email:  userInfo.GetName(),
			UID:    userInfo.GetUID(),
			Groups: userInfo.GetGroups(),
			// Extra:  userInfo.GetExtra(),
			// request info
			IsResourceRequest: requestInfo.IsResourceRequest,
			RequestPath:       requestInfo.Path,
			Verb:              requestInfo.Verb,
			APIPrefix:         requestInfo.APIPrefix,
			APIGroup:          requestInfo.APIGroup,
			APIVersion:        requestInfo.APIVersion,
			Namespace:         requestInfo.Namespace,
			Resource:          requestInfo.Resource,
			SubResource:       requestInfo.Subresource,
			Name:              requestInfo.Name,
			Parts:             requestInfo.Parts,
			FieldSelector:     requestInfo.FieldSelector,
			LabelSelector:     requestInfo.LabelSelector,
			// body
			RequestBody: bodyBytes,
		})

		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		handler.ServeHTTP(w, r)
	})

}

func (a *Audit) SendAuditLog(log Log) {
	r, err := a.client.R().SetBody(log).Post("/api/v1/k8s-audit-log/webhook")
	if err != nil {
		klog.Errorf("Error sending audit log to webhook: %v", err)
		return

	}
	if r == nil {
		klog.Errorf("Error sending audit log to webhook: response is nil")
		return
	}
	if r.IsError() || r.StatusCode() != http.StatusOK {
		klog.Errorf("Error sending audit log to webhook: %v", r.String())
	}
}
