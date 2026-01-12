// Copyright Jetstack Ltd. See LICENSE for details.
package logging

import (
	"net/http"
	"strings"

	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/klog/v2"
)

const (
	UserHeaderClientIPKey = "Remote-Client-IP"
)

// logs the request
func LogSuccessfulRequest(req *http.Request, inboundUser user.Info, outboundUser user.Info) {
	remoteAddr := req.RemoteAddr
	indexOfColon := strings.Index(remoteAddr, ":")
	if indexOfColon > 0 {
		remoteAddr = remoteAddr[0:indexOfColon]
	}

	xFwdFor := findXForwardedFor(req.Header, remoteAddr)

	kvs := []interface{}{
		"src", remoteAddr,
		"x_forwarded_for", xFwdFor,
		"uri", req.RequestURI,
		"inbound_user_name", inboundUser.GetName(),
		"inbound_user_groups", inboundUser.GetGroups(),
		"inbound_user_extras", inboundUser.GetExtra(),
	}

	if outboundUser != nil {
		kvs = append(kvs,
			"outbound_user_name", outboundUser.GetName(),
			"outbound_user_groups", outboundUser.GetGroups(),
			"outbound_user_uid", outboundUser.GetUID(),
			"outbound_user_extras", outboundUser.GetExtra(),
		)
	}

	klog.InfoS("AuSuccess", kvs...)
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
	remoteAddr := req.RemoteAddr
	indexOfColon := strings.Index(remoteAddr, ":")
	if indexOfColon > 0 {
		remoteAddr = remoteAddr[0:indexOfColon]
	}
	xFwdFor := findXForwardedFor(req.Header, remoteAddr)

	klog.InfoS("AuFail",
		"src", remoteAddr,
		"x_forwarded_for", xFwdFor,
		"uri", req.RequestURI,
	)
}
