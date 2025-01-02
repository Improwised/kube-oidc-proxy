// Copyright Jetstack Ltd. See LICENSE for details.
package proxy

import (
	ctx "context"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/Improwised/kube-oidc-proxy/cmd/app/options"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/audit"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/context"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/hooks"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/logging"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/subjectaccessreview"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/tokenreview"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apiserver/pkg/apis/apiserver"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authentication/request/bearertoken"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/server"
	"k8s.io/apiserver/plugin/pkg/authenticator/token/oidc"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/plugin/pkg/auth/authorizer/rbac"
)

const (
	UserHeaderClientIPKey = "Remote-Client-IP"
	timestampLayout       = "2006-01-02T15:04:05-0700"
)

var (
	errUnauthorized          = errors.New("Unauthorized")
	errNoName                = errors.New("no name in OIDC info")
	errNoImpersonationConfig = errors.New("no impersonation configuration in context")
)

type Config struct {
	DisableImpersonation bool
	TokenReview          bool

	FlushInterval   time.Duration
	ExternalAddress string

	ExtraUserHeaders                map[string][]string
	ExtraUserHeadersClientIPEnabled bool
}

type ClusterConfig struct {
	Name                  string
	Path                  string
	RestConfig            *rest.Config
	proxyHandler          *httputil.ReverseProxy
	TokenReviewer         *tokenreview.TokenReview
	SubjectAccessReviewer *subjectaccessreview.SubjectAccessReview
	clientTransport       http.RoundTripper
	noAuthClientTransport http.RoundTripper
	Authorizer            *rbac.RBACAuthorizer
}

type errorHandlerFn func(http.ResponseWriter, *http.Request, error)

type Proxy struct {
	oidcRequestAuther *bearertoken.Authenticator
	tokenAuther       authenticator.Token
	secureServingInfo *server.SecureServingInfo
	auditor           *audit.Audit

	ClustersConfig []*ClusterConfig

	config *Config

	hooks       *hooks.Hooks
	handleError errorHandlerFn

	requestInfo genericapirequest.RequestInfoFactory
}

// implement oidc.CAContentProvider to load
// the ca file from the options
type CAFromFile struct {
	CAFile string
}

func (caFromFile CAFromFile) CurrentCABundleContent() []byte {
	res, _ := ioutil.ReadFile(caFromFile.CAFile)
	return res
}

func New(clustersConfig []*ClusterConfig,
	oidcOptions *options.OIDCAuthenticationOptions,
	auditOptions *options.AuditOptions,
	ssinfo *server.SecureServingInfo,
	config *Config) (*Proxy, error) {

	// load the CA from the file listed in the options
	caFromFile := CAFromFile{
		CAFile: oidcOptions.CAFile,
	}

	// setup static JWT Auhenticator
	jwtConfig := apiserver.JWTAuthenticator{
		Issuer: apiserver.Issuer{
			URL:                  oidcOptions.IssuerURL,
			Audiences:            []string{oidcOptions.ClientID},
			CertificateAuthority: string(caFromFile.CurrentCABundleContent()),
		},

		ClaimMappings: apiserver.ClaimMappings{
			Username: apiserver.PrefixedClaimOrExpression{
				Claim:  oidcOptions.UsernameClaim,
				Prefix: &oidcOptions.UsernamePrefix,
			},
			Groups: apiserver.PrefixedClaimOrExpression{
				Claim:  oidcOptions.GroupsClaim,
				Prefix: &oidcOptions.GroupsPrefix,
			},
			UID: apiserver.ClaimOrExpression{
				Claim: "sub",
			},
		},
	}

	// generate tokenAuther from oidc config
	tokenAuther, err := oidc.New(ctx.TODO(), oidc.Options{
		CAContentProvider: caFromFile,
		//RequiredClaims:       oidcOptions.RequiredClaims,
		SupportedSigningAlgs: oidcOptions.SigningAlgs,
		JWTAuthenticator:     jwtConfig,
	})
	if err != nil {
		return nil, err
	}

	auditor, err := audit.New(auditOptions, config.ExternalAddress, ssinfo)
	if err != nil {
		return nil, err
	}

	requestInfo := genericapirequest.RequestInfoFactory{APIPrefixes: sets.NewString("api", "apis"), GrouplessAPIPrefixes: sets.NewString("api")}

	return &Proxy{
		ClustersConfig:    clustersConfig,
		hooks:             hooks.New(),
		secureServingInfo: ssinfo,
		config:            config,
		oidcRequestAuther: bearertoken.New(tokenAuther),
		tokenAuther:       tokenAuther,
		auditor:           auditor,
		requestInfo:       requestInfo,
	}, nil
}

func (p *Proxy) Run(stopCh <-chan struct{}) (<-chan struct{}, <-chan struct{}, error) {
	// standard round tripper for proxy to API Server

	for _, cluster := range p.ClustersConfig {
		clientRT, err := p.roundTripperForRestConfig(cluster.RestConfig)
		if err != nil {
			return nil, nil, err
		}

		url, err := url.Parse(cluster.RestConfig.Host)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse url: %s", err)
		}

		// create proxy
		proxyHandler := httputil.NewSingleHostReverseProxy(url)

		// set proxy transport(use to forward request to API server)
		cluster.clientTransport = clientRT
		proxyHandler.Transport = cluster

		// No auth round tripper for no impersonation
		if p.config.DisableImpersonation || p.config.TokenReview {
			noAuthClientRT, err := p.roundTripperForRestConfig(&rest.Config{
				APIPath: cluster.RestConfig.APIPath,
				Host:    cluster.RestConfig.Host,
				Timeout: cluster.RestConfig.Timeout,
				TLSClientConfig: rest.TLSClientConfig{
					CAFile: cluster.RestConfig.CAFile,
					CAData: cluster.RestConfig.CAData,
				},
			})
			if err != nil {
				return nil, nil, err
			}

			cluster.noAuthClientTransport = noAuthClientRT
		}

		// Set up error handler
		proxyHandler.ErrorHandler = p.handleError
		proxyHandler.FlushInterval = p.config.FlushInterval
		p.handleError = p.newErrorHandler()
		cluster.proxyHandler = proxyHandler
		// proxyHandler.ModifyResponse =
	}

	// Set up proxy handler using proxy
	waitCh, listenerStoppedCh, err := p.serve(http.HandlerFunc(p.httpHandler), stopCh)
	if err != nil {
		return nil, nil, err
	}

	return waitCh, listenerStoppedCh, nil
}

func (p *Proxy) httpHandler(w http.ResponseWriter, r *http.Request) {
	clusterName := p.GetClusterName(r.URL.Path)
	r.URL.Path = strings.TrimPrefix(r.URL.Path, "/"+clusterName)
	proxy := p.getCurrentClusterConfig(clusterName)
	if proxy == nil {
		p.handleError(w, r, errUnauthorized)
		return
	}
	proxy.proxyHandler.ServeHTTP(w, r)
}

func (p *Proxy) serve(handler http.Handler, stopCh <-chan struct{}) (<-chan struct{}, <-chan struct{}, error) {
	// Setup proxy handlers
	handler = p.withHandlers(handler)

	// Run auditor
	if err := p.auditor.Run(stopCh); err != nil {
		return nil, nil, err
	}

	// securely serve using serving config
	waitCh, listenerStoppedCh, err := p.secureServingInfo.Serve(handler, time.Second*60, stopCh)
	if err != nil {
		return nil, nil, err
	}

	return waitCh, listenerStoppedCh, nil
}

// RoundTrip is called last and is used to manipulate the forwarded request using context.
func (p *ClusterConfig) RoundTrip(req *http.Request) (*http.Response, error) {
	// Here we have successfully authenticated so now need to determine whether
	// we need use impersonation or not.

	// If no impersonation then we return here without setting impersonation
	// header but re-introduce the token we removed.
	if context.NoImpersonation(req) {
		token := context.BearerToken(req)
		req.Header.Add("Authorization", token)
		return p.noAuthClientTransport.RoundTrip(req)
	}

	// Get the impersonation headers from the context.
	impersonationConf := context.ImpersonationConfig(req)
	if impersonationConf == nil {
		return nil, errNoImpersonationConfig
	}

	// Log the request
	logging.LogSuccessfulRequest(req, *impersonationConf.InboundUser, *impersonationConf.ImpersonatedUser)

	// Push request as admin through round trippers to the API server.
	return p.clientTransport.RoundTrip(req)
}

func (p *Proxy) reviewToken(rw http.ResponseWriter, req *http.Request) bool {
	var remoteAddr string
	req, remoteAddr = context.RemoteAddr(req)

	clusterName := p.GetClusterName(req.URL.Path)
	req.URL.Path = strings.TrimPrefix(req.URL.Path, "/"+clusterName)
	config := p.getCurrentClusterConfig(clusterName)

	klog.V(4).Infof("attempting to validate a token in request using TokenReview endpoint(%s)",
		remoteAddr)

	ok, err := config.TokenReviewer.Review(req)
	if err != nil {
		klog.Errorf("unable to authenticate the request via TokenReview due to an error (%s): %s",
			remoteAddr, err)
		return false
	}

	if !ok {
		klog.V(4).Infof("passing request with valid token through (%s)",
			remoteAddr)

		return false
	}

	// No error and ok so passthrough the request
	return true
}

func (p *Proxy) roundTripperForRestConfig(config *rest.Config) (http.RoundTripper, error) {
	// get golang tls config to the API server
	tlsConfig, err := rest.TLSConfigFor(config)
	if err != nil {
		return nil, err
	}

	// create tls transport to request
	tlsTransport := &http.Transport{
		Proxy:           http.ProxyFromEnvironment,
		TLSClientConfig: tlsConfig,
	}

	// get kube transport config form rest client config
	restTransportConfig, err := config.TransportConfig()
	if err != nil {
		return nil, err
	}

	// wrap golang tls config with kube transport round tripper
	clientRT, err := transport.HTTPWrappersForConfig(restTransportConfig, tlsTransport)
	if err != nil {
		return nil, err
	}

	return clientRT, nil
}

// Return the proxy OIDC token authenticator
func (p *Proxy) OIDCTokenAuthenticator() authenticator.Token {
	return p.tokenAuther
}

func (p *Proxy) RunPreShutdownHooks() error {
	return p.hooks.RunPreShutdownHooks()
}

func (p *Proxy) getCurrentClusterConfig(clusterName string) *ClusterConfig {
	for _, cluster := range p.ClustersConfig {
		if cluster.Name == clusterName {
			return cluster
		}
	}
	return nil
}

func (p *Proxy) GetClusterName(path string) string {
	// validate the length of the path
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}
