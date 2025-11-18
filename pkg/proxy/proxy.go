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
	"github.com/Improwised/kube-oidc-proxy/pkg/cluster"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/audit"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/context"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/hooks"

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
)

const (
	UserHeaderClientIPKey = "Remote-Client-IP"
	timestampLayout       = "2006-01-02T15:04:05-0700"
)

var (
	errUnauthorized = errors.New("Unauthorized")
	errNoName       = errors.New("no name in OIDC info")
)

type Config struct {
	DisableImpersonation bool
	TokenReview          bool

	FlushInterval   time.Duration
	ExternalAddress string

	ExtraUserHeaders                map[string][]string
	ExtraUserHeadersClientIPEnabled bool
}

// ClusterManager interface for dependency injection
type ClusterManager interface {
	AddOrUpdateCluster(cluster *cluster.Cluster)
	GetCluster(name string) *cluster.Cluster
	GetAllClusters() []*cluster.Cluster
	RemoveCluster(name string)
}

type errorHandlerFn func(http.ResponseWriter, *http.Request, error)

type Proxy struct {
	oidcRequestAuther *bearertoken.Authenticator
	tokenAuther       authenticator.Token
	secureServingInfo *server.SecureServingInfo
	auditor           *audit.Audit
	clusterManager    ClusterManager
	config            *Config

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

func New(
	oidcOptions *options.OIDCAuthenticationOptions,
	auditOptions *options.AuditOptions,
	ssinfo *server.SecureServingInfo,
	config *Config,
	clusterManager ClusterManager) (*Proxy, error) {

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
		hooks:             hooks.New(),
		secureServingInfo: ssinfo,
		config:            config,
		oidcRequestAuther: bearertoken.New(tokenAuther),
		tokenAuther:       tokenAuther,
		auditor:           auditor,
		requestInfo:       requestInfo,
		clusterManager:    clusterManager,
	}, nil
}

func (p *Proxy) Run(stopCh <-chan struct{}) (<-chan struct{}, <-chan struct{}, error) {
	// standard round tripper for proxy to API Server
	p.handleError = p.newErrorHandler()

	for _, cluster := range p.clusterManager.GetAllClusters() {
		if err := p.SetupClusterProxy(cluster); err != nil {
			return nil, nil, err
		}
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
	cluster := p.clusterManager.GetCluster(clusterName)
	if cluster == nil {
		p.handleError(w, r, errUnauthorized)
		return
	}

	cluster.ProxyHandler.ServeHTTP(w, r)
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

func (p *Proxy) reviewToken(rw http.ResponseWriter, req *http.Request) bool {
	var remoteAddr string
	req, remoteAddr = context.RemoteAddr(req)

	clusterName := p.GetClusterName(req.URL.Path)
	req.URL.Path = strings.TrimPrefix(req.URL.Path, "/"+clusterName)
	config := p.clusterManager.GetCluster(clusterName)

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

func (p *Proxy) GetClusterName(path string) string {
	// validate the length of the path
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

func (p *Proxy) SetupClusterProxy(cluster *cluster.Cluster) error {
	clientRT, err := p.roundTripperForRestConfig(cluster.RestConfig)
	if err != nil {
		return err
	}

	url, err := url.Parse(cluster.RestConfig.Host)
	if err != nil {
		return fmt.Errorf("failed to parse url: %s", err)
	}

	proxyHandler := httputil.NewSingleHostReverseProxy(url)
	cluster.ClientTransport = clientRT
	proxyHandler.Transport = cluster

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
			return err
		}
		cluster.NoAuthClientTransport = noAuthClientRT
	}

	proxyHandler.ErrorHandler = p.handleError
	proxyHandler.FlushInterval = p.config.FlushInterval
	cluster.ProxyHandler = proxyHandler

	return nil
}
