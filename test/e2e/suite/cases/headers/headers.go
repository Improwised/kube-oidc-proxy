// Copyright Jetstack Ltd. See LICENSE for details.
package headers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/Improwised/kube-oidc-proxy/test/e2e/framework"
)

var _ = framework.CasesDescribe("Headers", func() {
	f := framework.NewDefaultFramework("headers")

	JustAfterEach(func() {
		By("Deleting fake API Server")
		err := f.Helper().DeleteFakeAPIServer(f.Namespace.Name)
		Expect(err).NotTo(HaveOccurred())
	})

	It("should not respond with any extra headers if none are set on the proxy", func() {
		extraOIDCVolumes, fakeAPIServerURL, err := f.Helper().DeployFakeAPIServer(f.Namespace.Name)
		Expect(err).NotTo(HaveOccurred())

		By("Redeploying proxy to send traffic to fake API server")
		f.DeployProxyWith(extraOIDCVolumes, fmt.Sprintf("--server=%s", fakeAPIServerURL), "--certificate-authority=/fake-apiserver/ca.pem")

		resp := sendRequestToProxy(f)

		By("Ensuring no extra headers sent by proxy")
		for k := range resp.Header {
			if strings.HasPrefix(strings.ToLower(k), "impersonate-extra-") {
				Expect(fmt.Errorf("expected no extra user headers, got=%+v", resp.Header)).NotTo(HaveOccurred())
			}
		}
	})

})

func sendRequestToProxy(f *framework.Framework) *http.Response {
	By("Building request to proxy")
	tokenPayload := f.Helper().NewTokenPayload(
		f.IssuerURL(), f.ClientID(), time.Now().Add(time.Minute))

	signedToken, err := f.Helper().SignToken(f.IssuerKeyBundle(), tokenPayload)
	Expect(err).NotTo(HaveOccurred())

	proxyConfig := f.NewProxyRestConfig()
	requester := f.Helper().NewRequester(proxyConfig.Transport, signedToken)

	By("Sending request to proxy")
	reqURL := fmt.Sprintf("%s/foo/bar", proxyConfig.Host)
	_, resp, err := requester.Get(reqURL)
	Expect(err).NotTo(HaveOccurred())

	return resp
}
