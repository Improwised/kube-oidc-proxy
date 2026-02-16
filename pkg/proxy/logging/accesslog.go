package logging

import (
	"net"
	"net/http"
	"strings"

	"github.com/Improwised/kube-oidc-proxy/pkg/logger"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/endpoints/request"
)

var (
	requestInfoFactory = &request.RequestInfoFactory{
		APIPrefixes:          sets.NewString("api", "apis"),
		GrouplessAPIPrefixes: sets.NewString("api"),
	}
)

const (
	UserHeaderClientIPKey = "Remote-Client-IP"
)

// logs the request
func LogSuccessfulRequest(clusterName string, req *http.Request, resp *http.Response, inboundUser user.Info, outboundUser user.Info) {
	remoteAddr, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		remoteAddr = req.RemoteAddr
	}

	xFwdFor := findXForwardedFor(req.Header, remoteAddr)

	fields := []zap.Field{
		zap.String("cluster_name", clusterName),
		zap.String("src_ip", remoteAddr),
		zap.String("x_forwarded_for", xFwdFor),
		zap.String("method", req.Method),
		zap.String("uri", req.RequestURI),
	}

	if req.Header != nil {
		fields = append(fields, zap.String("user_agent", req.UserAgent()))
	} else {
		fields = append(fields, zap.String("user_agent", ""))
	}

	fields = append(fields,
		zap.String("inbound_user", inboundUser.GetName()),
		zap.Strings("inbound_groups", inboundUser.GetGroups()),
		zap.Any("inbound_extra", inboundUser.GetExtra()),
	)

	if resp != nil {
		fields = append(fields, zap.Int("status_code", resp.StatusCode))
	}

	requestInfo, ok := request.RequestInfoFrom(req.Context())
	if !ok {
		// Fallback: try to create RequestInfo from URI
		// We need to trim the cluster name prefix if it exists
		path := req.URL.Path
		if clusterName != "" && strings.HasPrefix(path, "/"+clusterName) {
			path = strings.TrimPrefix(path, "/"+clusterName)
		}
		// Create a fake request with trimmed path for NewRequestInfo
		fakeReq, _ := http.NewRequest(req.Method, path, nil)
		var err error
		requestInfo, err = requestInfoFactory.NewRequestInfo(fakeReq)
		if err == nil {
			ok = true
		}
	}

	if ok {
		fields = append(fields,
			zap.String("Action", strings.ToUpper(requestInfo.Verb)),
			zap.String("apiGroup", requestInfo.APIGroup),
			zap.String("apiVersion", requestInfo.APIVersion),
			zap.String("namespace", requestInfo.Namespace),
			zap.String("resource", requestInfo.Resource),
			zap.String("subresource", requestInfo.Subresource),
			zap.String("name", requestInfo.Name),
			zap.Bool("isResourceRequest", requestInfo.IsResourceRequest),
		)
	}

	if outboundUser != nil {
		fields = append(fields,
			zap.String("outbound_user", outboundUser.GetName()),
			zap.Strings("outbound_groups", outboundUser.GetGroups()),
			zap.String("outbound_uid", outboundUser.GetUID()),
			zap.Any("outbound_extra", outboundUser.GetExtra()),
		)
	}

	logger.Logger.Info("AuSuccess", fields...)
}

// determines if the x-forwarded-for header is present, if so remove
// the remoteaddr since it is repetitive
func findXForwardedFor(headers http.Header, remoteAddr string) string {
	xFwdFor := headers.Get("x-forwarded-for")
	// clean off remoteaddr from x-forwarded-for
	if xFwdFor != "" {

		newXFwdFor := ""
		oneFound := false
		xFwdForIps := strings.Split(xFwdFor, ",")

		for _, ip := range xFwdForIps {
			ip = strings.TrimSpace(ip)

			if ip != remoteAddr {
				newXFwdFor = newXFwdFor + ip + ", "
				oneFound = true
			}

		}

		if oneFound {
			newXFwdFor = newXFwdFor[0 : len(newXFwdFor)-2]
		}

		xFwdFor = newXFwdFor

	}

	return xFwdFor
}

// logs the failed request
func LogFailedRequest(req *http.Request) {
	remoteAddr, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		remoteAddr = req.RemoteAddr
	}

	fields := []zap.Field{
		zap.String("src_ip", remoteAddr),
		zap.String("method", req.Method),
		zap.String("uri", req.RequestURI),
	}

	if req.Header != nil {
		fields = append(fields,
			zap.String("x_forwarded_for", req.Header.Get("x-forwarded-for")),
			zap.String("user_agent", req.UserAgent()),
		)
	} else {
		fields = append(fields,
			zap.String("x_forwarded_for", ""),
			zap.String("user_agent", ""),
		)
	}

	requestInfo, ok := request.RequestInfoFrom(req.Context())
	if !ok {
		// Fallback: try to create RequestInfo from URI
		path := req.URL.Path
		parts := strings.Split(path, "/")
		if len(parts) >= 2 {
			// Assume parts[1] is cluster name
			path = "/" + strings.Join(parts[2:], "/")
		}
		fakeReq, _ := http.NewRequest(req.Method, path, nil)
		var err error
		requestInfo, err = requestInfoFactory.NewRequestInfo(fakeReq)
		if err == nil {
			ok = true
		}
	}

	if ok {
		fields = append(fields,
			zap.String("Action", strings.ToUpper(requestInfo.Verb)),
			zap.String("apiGroup", requestInfo.APIGroup),
			zap.String("apiVersion", requestInfo.APIVersion),
			zap.String("namespace", requestInfo.Namespace),
			zap.String("resource", requestInfo.Resource),
			zap.String("subresource", requestInfo.Subresource),
			zap.String("name", requestInfo.Name),
			zap.Bool("isResourceRequest", requestInfo.IsResourceRequest),
		)
	}

	logger.Logger.Info("AuFail", fields...)
}
