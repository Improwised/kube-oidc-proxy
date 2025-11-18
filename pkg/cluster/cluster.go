package cluster

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

// Cluster represents a Kubernetes cluster configuration and its associated resources.
type Cluster struct {
	Name                  string                                   // Name of the cluster
	Path                  string                                   // Path to the kubeconfig file
	RestConfig            *rest.Config                             // REST configuration for Kubernetes API access
	Kubeclient            *kubernetes.Clientset                    // Kubernetes client for interacting with the cluster
	TokenReviewer         *tokenreview.TokenReview                 // Token reviewer for validating tokens
	SubjectAccessReviewer *subjectaccessreview.SubjectAccessReview // Reviewer for subject access requests
	Authorizer            *rbac.RBACAuthorizer                     // RBAC authorizer for access control
	RBACConfig            *util.RBAC                               // RBAC configuration for the cluster
	ProxyHandler          *httputil.ReverseProxy                   // Reverse proxy handler for forwarding requests
	ClientTransport       http.RoundTripper                        // Transport for authenticated requests
	NoAuthClientTransport http.RoundTripper                        // Transport for unauthenticated requests
	IsStatic              bool                                     // Indicates if the cluster is statically configured
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
