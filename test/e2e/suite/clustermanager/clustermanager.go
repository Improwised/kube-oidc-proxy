package clustermanager

import (
	"context"
	"os"
	"time"

	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/crd"
	"github.com/Improwised/kube-oidc-proxy/test/e2e/framework"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var _ = framework.CasesDescribe("Dynamic Cluster Management", func() {
	f := framework.NewDefaultFramework("dynamic-cluster")
	var newClusterKubeconfig []byte

	It("should dynamically add new clusters and apply global roles", func() {

		By("Creating global role and binding")
		capiClusterRole := &crd.CAPIClusterRole{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "rbac.platformengineers.io/v1",
				Kind:       "CAPIClusterRole",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-node-reader",
			},
			Spec: crd.CAPIClusterRoleSpec{
				CommonRoleSpec: crd.CommonRoleSpec{
					TargetClusters: []string{"*"},
					Rules: []v1.PolicyRule{
						{
							APIGroups: []string{""},
							Resources: []string{"nodes"},
							Verbs:     []string{"get", "list"},
						},
					},
				},
			},
		}
		err := f.Helper().CreateCRDObject(capiClusterRole, crd.CAPIClusterRoleGVR, "")
		Expect(err).NotTo(HaveOccurred())

		capiClusterRoleBinding := &crd.CAPIClusterRoleBinding{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "rbac.platformengineers.io/v1",
				Kind:       "CAPIClusterRoleBinding",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-node-reader-binding",
			},
			Spec: crd.CAPIClusterRoleBindingSpec{
				CommonBindingSpec: crd.CommonBindingSpec{
					RoleRef:        []string{"test-node-reader"},
					Subjects:       []crd.Subject{{Group: "group-1"}},
					TargetClusters: []string{"*"},
				},
			},
		}
		err = f.Helper().CreateCRDObject(capiClusterRoleBinding, crd.CAPIClusterRoleBindingGVR, "")
		Expect(err).NotTo(HaveOccurred())

		By("Waiting for RBAC reconciliation")
		time.Sleep(5 * time.Second)

		By("Listing nodes (should succeed)")
		_, err = f.ProxyClient.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("Creating dynamic cluster secret")
		newClusterKubeconfig, err = os.ReadFile(f.Helper().Config().KubeConfigPath)
		Expect(err).NotTo(HaveOccurred())

		err = f.CreateDynamicClusterSecret("new-cluster", newClusterKubeconfig)
		Expect(err).NotTo(HaveOccurred())

		By("Waiting for cluster to be added")
		time.Sleep(20 * time.Second)

		By("Verifying access in new cluster")
		// Create client for new cluster
		newClusterConfig := f.NewProxyRestConfig()
		newClusterConfig.Host = newClusterConfig.Host + "/new-cluster"
		newClusterClient, err := kubernetes.NewForConfig(newClusterConfig)
		Expect(err).NotTo(HaveOccurred())

		// Verify node access

		_, err = newClusterClient.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
	})
})
