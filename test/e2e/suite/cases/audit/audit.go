// Copyright Jetstack Ltd. See LICENSE for details.

package audit

import (
	"fmt"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/Improwised/kube-oidc-proxy/constants"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/audit"
	"github.com/Improwised/kube-oidc-proxy/test/e2e/framework"
)

var _ = framework.CasesDescribe("Custom Audit Logging", func() {
	f := framework.NewDefaultFramework("custom-audit")

	Context("when making authenticated requests", func() {

		It("should handle unauthorized requests", func() {
			By("Making unauthorized request")
			// Setup proxy client with invalid token
			proxyConfig := f.NewProxyRestConfig()
			proxyConfig.Host = fmt.Sprintf("%s/%s", proxyConfig.Host, constants.ClusterName)
			requester := f.Helper().NewRequester(proxyConfig.Transport, "invalid-token")

			// Make request
			target := fmt.Sprintf("%s/api/v1/namespaces/%s/pods", proxyConfig.Host, f.Namespace.Name)
			_, resp, err := requester.Get(target)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))

			By("Verifying unauthorized audit log")
			// Allow time for audit logs to be written
			time.Sleep(2 * time.Second)

			// Get audit logs and verify unauthorized entry exists
			var auditLogs []audit.Log
			// Logic to retrieve and verify audit logs for unauthorized request
			// Implementation depends on how audit logs are stored/retrieved in your setup
			for _, log := range auditLogs {
				if log.ClusterName == constants.ClusterName &&
					log.Resource == "pods" &&
					log.Namespace == f.Namespace.Name {
					Expect(log.Email).To(BeEmpty())
					Expect(log.Groups).To(BeEmpty())
					break
				}
			}
		})
	})
})
