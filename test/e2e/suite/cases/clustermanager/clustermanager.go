package clustermanager

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/Improwised/kube-oidc-proxy/constants"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/crd"
	"github.com/Improwised/kube-oidc-proxy/test/e2e/framework"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/rbac/v1"

	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var _ = framework.CasesDescribe("Comprehensive Dynamic Cluster Management", func() {
	f := framework.NewDefaultFramework("comprehensive-cluster-mgmt")

	AfterEach(func() {

		By("Cleaning up secrets after test case")
		err := f.Helper().KubeClient.CoreV1().Secrets("default").DeleteCollection(context.TODO(), metav1.DeleteOptions{}, metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred(), "Failed to clean up secrets")

	})

	Describe("Basic Dynamic Cluster Operations", func() {

		AfterEach(func() {
			err := f.Helper().DeleteCRDObject("namespace-reader", crd.CAPIClusterRoleGVR, "")
			Expect(err).NotTo(HaveOccurred())

			err = f.Helper().DeleteCRDObject("namespace-reader-binding", crd.CAPIClusterRoleBindingGVR, "")
			Expect(err).NotTo(HaveOccurred())
		})

		It("should successfully add a new dynamic cluster", func() {

			By("Creating dynamic cluster secret with single cluster")
			secret, err := createSingleClusterSecret("test-cluster-1", f)
			Expect(err).NotTo(HaveOccurred())

			_, err = f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Creating Clusterrole and Clusterrolebinding for list namespace")
			err = createRBACForListNamespace(f)
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for cluster to be added")
			time.Sleep(3 * time.Second)

			By("Verifying access to the new cluster")
			config := f.NewProxyRestConfig()
			config.Host = config.Host + "/test-cluster-1"
			client, err := kubernetes.NewForConfig(config)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() error {
				_, err := client.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
				return err
			}, 10*time.Second).Should(Succeed())
		})

		It("should handle multiple dynamic clusters in a single secret", func() {
			By("Creating secret with multiple cluster configurations")
			secret, err := createMultiClusterSecret(f, "test-cluster-2", "test-cluster-3", "test-cluster-4")
			Expect(err).NotTo(HaveOccurred())
			_, err = f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Creating Clusterrole and Clusterrolebinding for list namespace")
			err = createRBACForListNamespace(f)
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for clusters to be processed")
			time.Sleep(5 * time.Second)

			By("Verifying access to all clusters")
			for _, clusterName := range []string{"test-cluster-2", "test-cluster-3", "test-cluster-4"} {
				config := f.NewProxyRestConfig()
				config.Host = config.Host + "/" + clusterName
				client, err := kubernetes.NewForConfig(config)
				Expect(err).NotTo(HaveOccurred())

				Eventually(func() error {
					_, err := client.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
					return err
				}, 10*time.Second, 1*time.Second).Should(Succeed(), fmt.Sprintf("Cluster %s should be accessible", clusterName))
			}
		})

		It("should update existing dynamic clusters when secret is modified", func() {
			By("Creating initial dynamic cluster")
			secret, err := createSingleClusterSecret("update-cluster-1", f)
			Expect(err).NotTo(HaveOccurred())

			_, err = f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Creating Clusterrole and Clusterrolebinding for list namespace")
			err = createRBACForListNamespace(f)
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for initial cluster to be ready")
			time.Sleep(3 * time.Second)

			By("Updating secret with additional cluster")
			secret.Data["update-cluster-2"] = secret.Data["update-cluster-1"]
			_, err = f.Helper().KubeClient.CoreV1().Secrets("default").Update(context.TODO(), secret, metav1.UpdateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying both clusters are accessible")
			time.Sleep(3 * time.Second)
			for _, clusterName := range []string{"update-cluster-1", "update-cluster-2"} {
				config := f.NewProxyRestConfig()
				config.Host = config.Host + "/" + clusterName
				client, err := kubernetes.NewForConfig(config)
				Expect(err).NotTo(HaveOccurred())

				Eventually(func() error {
					_, err := client.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
					return err
				}, 10*time.Second).Should(Succeed())
			}
		})

		It("should remove dynamic clusters when secret is deleted", func() {
			By("Creating dynamic cluster")
			secret, err := createSingleClusterSecret("delete-test-cluster", f)
			Expect(err).NotTo(HaveOccurred())

			_, err = f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Creating Clusterrole and Clusterrolebinding for list namespace")
			err = createRBACForListNamespace(f)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying cluster is accessible")
			time.Sleep(3 * time.Second)
			config := f.NewProxyRestConfig()
			config.Host = config.Host + "/delete-test-cluster"
			client, err := kubernetes.NewForConfig(config)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() error {
				_, err := client.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
				return err
			}, 10*time.Second).Should(Succeed())

			By("Deleting the secret")
			err = f.Helper().KubeClient.CoreV1().Secrets("default").Delete(context.TODO(), secret.Name, metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying cluster is no longer accessible")
			time.Sleep(3 * time.Second)
			Eventually(func() error {
				_, err := client.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
				return err
			}, 10*time.Second).Should(HaveOccurred())
		})
	})

	Describe("Error Handling and Edge Cases", func() {
		It("should handle invalid kubeconfig gracefully", func() {
			By("Creating secret with invalid kubeconfig")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-oidc-proxy-kubeconfigs",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"invalid-cluster": []byte("invalid yaml content"),
				},
			}
			_, err := f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Waiting and verifying no cluster is created")
			time.Sleep(3 * time.Second)
			config := f.NewProxyRestConfig()
			config.Host = config.Host + "/invalid-cluster"
			client, err := kubernetes.NewForConfig(config)
			Expect(err).NotTo(HaveOccurred())

			_, err = client.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
			Expect(err).To(HaveOccurred())
		})

		It("should handle empty secret data", func() {
			By("Creating secret with empty data")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-oidc-proxy-kubeconfigs",
					Namespace: "default",
				},
				Data: map[string][]byte{},
			}
			_, err := f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Waiting and ensuring no errors occur")
			time.Sleep(3 * time.Second)
			// This should not cause any errors, just no clusters should be created
		})

		It("should handle corrupted kubeconfig data", func() {
			By("Creating secret with corrupted kubeconfig")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-oidc-proxy-kubeconfigs",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"corrupted-cluster": []byte(`
apiVersion: v1
kind: Config
clusters:
- cluster:
	server: https://invalid-server:6443
	certificate-authority-data: invalid-cert-data
  name: corrupted-cluster
contexts:
- context:
	cluster: corrupted-cluster
	user: admin
  name: admin@corrupted-cluster
current-context: admin@corrupted-cluster
users:
- name: admin
  user:
	client-certificate-data: invalid-client-cert
	client-key-data: invalid-client-key
`),
				},
			}
			_, err := f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying cluster creation fails gracefully")
			time.Sleep(3 * time.Second)
			config := f.NewProxyRestConfig()
			config.Host = config.Host + "/corrupted-cluster"
			client, err := kubernetes.NewForConfig(config)
			Expect(err).NotTo(HaveOccurred())

			_, err = client.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
			Expect(err).To(HaveOccurred())
		})

		It("should handle mixed valid and invalid clusters in single secret", func() {
			By("Creating secret with mixed valid and invalid clusters")
			validKubeconfigBytes, err := getValidKubeconfig(f)
			Expect(err).NotTo(HaveOccurred())

			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-oidc-proxy-kubeconfigs",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"valid-cluster":   validKubeconfigBytes,
					"invalid-cluster": []byte("invalid yaml"),
				},
			}
			_, err = f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Creating Clusterrole and Clusterrolebinding for list namespace")
			err = createRBACForListNamespace(f)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying valid cluster is accessible")
			time.Sleep(3 * time.Second)
			config := f.NewProxyRestConfig()
			config.Host = config.Host + "/valid-cluster"
			client, err := kubernetes.NewForConfig(config)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() error {
				_, err := client.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
				return err
			}, 10*time.Second).Should(Succeed())

			By("Verifying invalid cluster is not accessible")
			config.Host = config.Host + "/invalid-cluster"
			invalidClient, err := kubernetes.NewForConfig(config)
			Expect(err).NotTo(HaveOccurred())

			_, err = invalidClient.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
			Expect(err).To(HaveOccurred())

			err = f.Helper().DeleteCRDObject("namespace-reader", crd.CAPIClusterRoleGVR, "")
			Expect(err).NotTo(HaveOccurred())

			err = f.Helper().DeleteCRDObject("namespace-reader-binding", crd.CAPIClusterRoleBindingGVR, "")
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("RBAC Integration with Dynamic Clusters", func() {
		It("should apply global RBAC rules to new dynamic clusters", func() {
			By("Creating global CAPIClusterRole")
			globalRole := &crd.CAPIClusterRole{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "rbac.platformengineers.io/v1",
					Kind:       "CAPIClusterRole",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "global-pod-reader",
				},
				Spec: crd.CAPIClusterRoleSpec{
					CommonRoleSpec: crd.CommonRoleSpec{
						TargetClusters: []string{"*"},
						Rules: []v1.PolicyRule{
							{
								APIGroups: []string{""},
								Resources: []string{"pods"},
								Verbs:     []string{"get", "list"},
							},
						},
					},
				},
			}
			err := f.Helper().CreateCRDObject(globalRole, crd.CAPIClusterRoleGVR, "")
			Expect(err).NotTo(HaveOccurred())

			By("Creating global CAPIClusterRoleBinding")
			globalBinding := &crd.CAPIClusterRoleBinding{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "rbac.platformengineers.io/v1",
					Kind:       "CAPIClusterRoleBinding",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "global-pod-reader-binding",
				},
				Spec: crd.CAPIClusterRoleBindingSpec{
					CommonBindingSpec: crd.CommonBindingSpec{
						RoleRef:        []string{"global-pod-reader"},
						Subjects:       []crd.Subject{{Group: "group-1"}},
						TargetClusters: []string{"*"},
					},
				},
			}
			err = f.Helper().CreateCRDObject(globalBinding, crd.CAPIClusterRoleBindingGVR, "")
			Expect(err).NotTo(HaveOccurred())

			By("Creating dynamic cluster after RBAC rules")

			secret, err := createSingleClusterSecret("rbac-test-cluster", f)
			Expect(err).NotTo(HaveOccurred())

			_, err = f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for RBAC reconciliation")
			time.Sleep(8 * time.Second)

			By("Verifying pod access in dynamic cluster")
			config := f.NewProxyRestConfig()
			config.Host = config.Host + "/rbac-test-cluster"
			client, err := kubernetes.NewForConfig(config)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() error {
				_, err := client.CoreV1().Pods(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
				return err
			}, 15*time.Second).Should(Succeed())

			By("Verifying forbidden access to services")
			_, err = client.CoreV1().Services(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
			Expect(k8sErrors.IsForbidden(err)).To(BeTrue())
		})

		It("should apply cluster-specific RBAC rules", func() {
			By("Creating dynamic cluster first")

			secret, err := createSingleClusterSecret("specific-rbac-cluster", f)
			Expect(err).NotTo(HaveOccurred())

			_, err = f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			time.Sleep(3 * time.Second)

			By("Creating cluster-specific CAPIClusterRole")
			specificRole := &crd.CAPIClusterRole{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "rbac.platformengineers.io/v1",
					Kind:       "CAPIClusterRole",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "specific-configmap-reader",
				},
				Spec: crd.CAPIClusterRoleSpec{
					CommonRoleSpec: crd.CommonRoleSpec{
						TargetClusters: []string{"specific-rbac-cluster"},
						Rules: []v1.PolicyRule{
							{
								APIGroups: []string{""},
								Resources: []string{"configmaps"},
								Verbs:     []string{"get", "list"},
							},
						},
					},
				},
			}
			err = f.Helper().CreateCRDObject(specificRole, crd.CAPIClusterRoleGVR, "")
			Expect(err).NotTo(HaveOccurred())

			By("Creating cluster-specific CAPIClusterRoleBinding")
			specificBinding := &crd.CAPIClusterRoleBinding{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "rbac.platformengineers.io/v1",
					Kind:       "CAPIClusterRoleBinding",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "specific-configmap-reader-binding",
				},
				Spec: crd.CAPIClusterRoleBindingSpec{
					CommonBindingSpec: crd.CommonBindingSpec{
						RoleRef:        []string{"specific-configmap-reader"},
						Subjects:       []crd.Subject{{Group: "group-1"}},
						TargetClusters: []string{"specific-rbac-cluster"},
					},
				},
			}
			err = f.Helper().CreateCRDObject(specificBinding, crd.CAPIClusterRoleBindingGVR, "")
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for RBAC reconciliation")
			time.Sleep(8 * time.Second)

			By("Verifying configmap access in specific cluster")
			config := f.NewProxyRestConfig()
			config.Host = config.Host + "/specific-rbac-cluster"
			client, err := kubernetes.NewForConfig(config)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() error {
				_, err := client.CoreV1().ConfigMaps(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
				return err
			}, 15*time.Second).Should(Succeed())

			By("Verifying forbidden access to pods")
			_, err = client.CoreV1().Pods(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
			Expect(k8sErrors.IsForbidden(err)).To(BeTrue())

			By("Verifying no access in default cluster")
			defaultConfig := f.NewProxyRestConfig()
			defaultConfig.Host = fmt.Sprintf("%s/%s", defaultConfig.Host, constants.ClusterName)
			defaultClient, err := kubernetes.NewForConfig(defaultConfig)
			Expect(err).NotTo(HaveOccurred())

			_, err = defaultClient.CoreV1().ConfigMaps(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
			Expect(k8sErrors.IsForbidden(err)).To(BeTrue())
		})

		It("should handle RBAC updates for existing dynamic clusters", func() {
			By("Creating dynamic cluster")

			secret, err := createSingleClusterSecret("rbac-update-cluster", f)
			Expect(err).NotTo(HaveOccurred())

			_, err = f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			time.Sleep(3 * time.Second)

			By("Verifying initial forbidden access")
			config := f.NewProxyRestConfig()
			config.Host = config.Host + "/rbac-update-cluster"
			client, err := kubernetes.NewForConfig(config)
			Expect(err).NotTo(HaveOccurred())

			_, err = client.CoreV1().Secrets(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
			Expect(k8sErrors.IsForbidden(err)).To(BeTrue())

			By("Creating RBAC rules")
			role := &crd.CAPIClusterRole{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "rbac.platformengineers.io/v1",
					Kind:       "CAPIClusterRole",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "secret-reader",
				},
				Spec: crd.CAPIClusterRoleSpec{
					CommonRoleSpec: crd.CommonRoleSpec{
						TargetClusters: []string{"rbac-update-cluster"},
						Rules: []v1.PolicyRule{
							{
								APIGroups: []string{""},
								Resources: []string{"secrets"},
								Verbs:     []string{"get", "list"},
							},
						},
					},
				},
			}
			err = f.Helper().CreateCRDObject(role, crd.CAPIClusterRoleGVR, "")
			Expect(err).NotTo(HaveOccurred())

			binding := &crd.CAPIClusterRoleBinding{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "rbac.platformengineers.io/v1",
					Kind:       "CAPIClusterRoleBinding",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "secret-reader-binding",
				},
				Spec: crd.CAPIClusterRoleBindingSpec{
					CommonBindingSpec: crd.CommonBindingSpec{
						RoleRef:        []string{"secret-reader"},
						Subjects:       []crd.Subject{{Group: "group-1"}},
						TargetClusters: []string{"rbac-update-cluster"},
					},
				},
			}
			err = f.Helper().CreateCRDObject(binding, crd.CAPIClusterRoleBindingGVR, "")
			Expect(err).NotTo(HaveOccurred())

			By("Verifying access after RBAC creation")
			Eventually(func() error {
				_, err := client.CoreV1().Secrets(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
				return err
			}, 15*time.Second).Should(Succeed())
		})
	})

	Describe("Performance and Scalability", func() {
		It("should handle rapid cluster additions and removals", func() {
			By("Creating and deleting clusters rapidly")
			for i := 0; i < 5; i++ {
				clusterName := fmt.Sprintf("rapid-cluster-%d", i)

				// Create cluster
				secret, err := createSingleClusterSecret(clusterName, f)
				Expect(err).NotTo(HaveOccurred())

				secret.Name = fmt.Sprintf("rapid-secret-%d", i)
				_, err = f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
				Expect(err).NotTo(HaveOccurred())

				// Wait briefly
				time.Sleep(500 * time.Millisecond)

				// Delete cluster
				err = f.Helper().KubeClient.CoreV1().Secrets("default").Delete(context.TODO(), secret.Name, metav1.DeleteOptions{})
				Expect(err).NotTo(HaveOccurred())
			}

			By("Verifying system stability")
			time.Sleep(5 * time.Second)
			// System should remain stable and responsive
		})

		It("should handle large number of clusters in single secret", func() {
			By("Creating secret with many clusters")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-oidc-proxy-kubeconfigs",
					Namespace: "default",
				},
				Data: make(map[string][]byte),
			}

			validKubeconfig, err := getValidKubeconfig(f)
			Expect(err).NotTo(HaveOccurred())

			for i := 0; i < 10; i++ {
				clusterName := fmt.Sprintf("scale-cluster-%d", i)
				secret.Data[clusterName] = validKubeconfig
			}

			_, err = f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Creating Clusterrole and Clusterrolebinding for list namespace")
			err = createRBACForListNamespace(f)
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for all clusters to be processed")
			time.Sleep(10 * time.Second)

			By("Verifying random clusters are accessible")
			for _, i := range []int{0, 4, 9} {
				clusterName := fmt.Sprintf("scale-cluster-%d", i)
				config := f.NewProxyRestConfig()
				config.Host = config.Host + "/" + clusterName
				client, err := kubernetes.NewForConfig(config)
				Expect(err).NotTo(HaveOccurred())

				Eventually(func() error {
					_, err := client.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
					return err
				}, 10*time.Second).Should(Succeed(), fmt.Sprintf("Cluster %s should be accessible", clusterName))
			}

			err = f.Helper().DeleteCRDObject("namespace-reader", crd.CAPIClusterRoleGVR, "")
			Expect(err).NotTo(HaveOccurred())

			err = f.Helper().DeleteCRDObject("namespace-reader-binding", crd.CAPIClusterRoleBindingGVR, "")
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("Concurrent Operations", func() {
		It("should handle concurrent secret modifications", func() {
			By("Creating initial secret")
			secret, err := createSingleClusterSecret("concurrent-cluster-1", f)
			Expect(err).NotTo(HaveOccurred())

			_, err = f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Creating Clusterrole and Clusterrolebinding for list namespace")
			err = createRBACForListNamespace(f)
			Expect(err).NotTo(HaveOccurred())

			By("Performing concurrent updates")
			done := make(chan bool, 3)

			// Goroutine 1: Add cluster
			go func() {
				defer GinkgoRecover()
				time.Sleep(100 * time.Millisecond)
				secret, err := f.Helper().KubeClient.CoreV1().Secrets("default").Get(context.TODO(), "kube-oidc-proxy-kubeconfigs", metav1.GetOptions{})
				if err == nil {
					secret.Data["concurrent-cluster-2"] = secret.Data["concurrent-cluster-1"]
					f.Helper().KubeClient.CoreV1().Secrets("default").Update(context.TODO(), secret, metav1.UpdateOptions{})
				}
				done <- true
			}()

			// Goroutine 2: Add different cluster
			go func() {
				defer GinkgoRecover()
				time.Sleep(200 * time.Millisecond)
				secret, err := f.Helper().KubeClient.CoreV1().Secrets("default").Get(context.TODO(), "kube-oidc-proxy-kubeconfigs", metav1.GetOptions{})
				if err == nil {
					secret.Data["concurrent-cluster-3"] = secret.Data["concurrent-cluster-1"]
					f.Helper().KubeClient.CoreV1().Secrets("default").Update(context.TODO(), secret, metav1.UpdateOptions{})
				}
				done <- true
			}()

			// Goroutine 3: Delete and recreate
			go func() {
				defer GinkgoRecover()
				time.Sleep(300 * time.Millisecond)
				f.Helper().KubeClient.CoreV1().Secrets("default").Delete(context.TODO(), "kube-oidc-proxy-kubeconfigs", metav1.DeleteOptions{})
				time.Sleep(100 * time.Millisecond)
				newSecret, err := createSingleClusterSecret("concurrent-cluster-final", f)
				Expect(err).NotTo(HaveOccurred())

				f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), newSecret, metav1.CreateOptions{})
				done <- true
			}()

			// Wait for all operations
			for i := 0; i < 3; i++ {
				<-done
			}

			By("Verifying system remains stable")
			time.Sleep(5 * time.Second)
			config := f.NewProxyRestConfig()
			config.Host = config.Host + "/concurrent-cluster-final"
			client, err := kubernetes.NewForConfig(config)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() error {
				_, err := client.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
				return err
			}, 10*time.Second).Should(Succeed())

			err = f.Helper().DeleteCRDObject("namespace-reader", crd.CAPIClusterRoleGVR, "")
			Expect(err).NotTo(HaveOccurred())

			err = f.Helper().DeleteCRDObject("namespace-reader-binding", crd.CAPIClusterRoleBindingGVR, "")
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("Static and Dynamic Cluster Coexistence", func() {
		It("should preserve static clusters when dynamic clusters change", func() {
			By("Verifying static cluster is accessible")
			config := f.NewProxyRestConfig()
			config.Host = fmt.Sprintf("%s/%s", config.Host, constants.ClusterName)
			staticClient, err := kubernetes.NewForConfig(config)
			Expect(err).NotTo(HaveOccurred())

			By("Creating Clusterrole and Clusterrolebinding for list namespace")
			err = createRBACForListNamespace(f)
			Expect(err).NotTo(HaveOccurred())

			_, err = staticClient.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Adding dynamic clusters")

			secret, err := createSingleClusterSecret("coexist-dynamic-1", f)
			Expect(err).NotTo(HaveOccurred())

			_, err = f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			time.Sleep(3 * time.Second)

			By("Verifying both static and dynamic clusters work")
			// Test static cluster
			_, err = staticClient.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred())

			// Test dynamic cluster
			dynamicConfig := f.NewProxyRestConfig()
			dynamicConfig.Host = dynamicConfig.Host + "/coexist-dynamic-1"
			dynamicClient, err := kubernetes.NewForConfig(dynamicConfig)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() error {
				_, err := dynamicClient.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
				return err
			}, 10*time.Second).Should(Succeed())

			By("Removing dynamic clusters")
			err = f.Helper().KubeClient.CoreV1().Secrets("default").Delete(context.TODO(), "kube-oidc-proxy-kubeconfigs", metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred())
			time.Sleep(3 * time.Second)

			By("Verifying static cluster still works")
			_, err = staticClient.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred())

			err = f.Helper().DeleteCRDObject("namespace-reader", crd.CAPIClusterRoleGVR, "")
			Expect(err).NotTo(HaveOccurred())

			err = f.Helper().DeleteCRDObject("namespace-reader-binding", crd.CAPIClusterRoleBindingGVR, "")
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("Resource Cleanup and Garbage Collection", func() {
		It("should clean up resources when clusters are removed", func() {
			By("Creating cluster with RBAC")

			secret, err := createSingleClusterSecret("cleanup-test-cluster", f)
			Expect(err).NotTo(HaveOccurred())

			_, err = f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			role := &crd.CAPIClusterRole{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "rbac.platformengineers.io/v1",
					Kind:       "CAPIClusterRole",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "cleanup-test-role",
				},
				Spec: crd.CAPIClusterRoleSpec{
					CommonRoleSpec: crd.CommonRoleSpec{
						TargetClusters: []string{"cleanup-test-cluster"},
						Rules: []v1.PolicyRule{
							{
								APIGroups: []string{""},
								Resources: []string{"pods"},
								Verbs:     []string{"get", "list"},
							},
						},
					},
				},
			}
			err = f.Helper().CreateCRDObject(role, crd.CAPIClusterRoleGVR, "")
			Expect(err).NotTo(HaveOccurred())

			binding := &crd.CAPIClusterRoleBinding{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "rbac.platformengineers.io/v1",
					Kind:       "CAPIClusterRoleBinding",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "cleanup-test-role-binding",
				},
				Spec: crd.CAPIClusterRoleBindingSpec{
					CommonBindingSpec: crd.CommonBindingSpec{
						RoleRef:        []string{"cleanup-test-role"},
						Subjects:       []crd.Subject{{Group: "group-1"}},
						TargetClusters: []string{"cleanup-test-cluster"},
					},
				},
			}
			err = f.Helper().CreateCRDObject(binding, crd.CAPIClusterRoleBindingGVR, "")
			Expect(err).NotTo(HaveOccurred())

			By("Verifying cluster and RBAC work")
			time.Sleep(5 * time.Second)
			config := f.NewProxyRestConfig()
			config.Host = config.Host + "/cleanup-test-cluster"
			client, err := kubernetes.NewForConfig(config)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() error {
				_, err := client.CoreV1().Pods(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
				return err
			}, 10*time.Second).Should(Succeed())

			By("Removing cluster")
			err = f.Helper().KubeClient.CoreV1().Secrets("default").Delete(context.TODO(), "kube-oidc-proxy-kubeconfigs", metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying cluster is no longer accessible")
			time.Sleep(3 * time.Second)
			Eventually(func() error {
				_, err := client.CoreV1().Pods(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
				return err
			}, 10*time.Second).Should(HaveOccurred())
		})
	})

	Describe("Unreachable Cluster Endpoints", func() {
		It("should handle clusters with unreachable server endpoints", func() {
			By("Creating secret with unreachable server")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-oidc-proxy-kubeconfigs",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"unreachable-cluster": []byte(`
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://unreachable-server.nonexistent:6443
    insecure-skip-tls-verify: true
  name: unreachable
contexts:
- context:
    cluster: unreachable
    user: admin
  name: unreachable-context
current-context: unreachable-context
users:
- name: admin
  user:
    token: test-token
`),
				},
			}
			_, err := f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying cluster setup completes but access fails gracefully")
			time.Sleep(3 * time.Second)
			config := f.NewProxyRestConfig()
			config.Host = config.Host + "/unreachable-cluster"
			client, err := kubernetes.NewForConfig(config)
			Expect(err).NotTo(HaveOccurred())

			By("Making request that should fail due to unreachable endpoint")
			_, err = client.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
			Expect(err).To(HaveOccurred())
		})

		It("should handle clusters with DNS resolution failures", func() {
			By("Creating secret with unresolvable DNS")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-oidc-proxy-kubeconfigs",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"dns-failure-cluster": []byte(`
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://this-domain-definitely-does-not-exist.invalid:6443
    insecure-skip-tls-verify: true
  name: dns-failure
contexts:
- context:
    cluster: dns-failure
    user: admin
  name: dns-failure-context
current-context: dns-failure-context
users:
- name: admin
  user:
    token: test-token
`),
				},
			}
			_, err := f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying DNS failure is handled gracefully")
			time.Sleep(3 * time.Second)
			config := f.NewProxyRestConfig()
			config.Host = config.Host + "/dns-failure-cluster"
			client, err := kubernetes.NewForConfig(config)
			Expect(err).NotTo(HaveOccurred())

			_, err = client.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
			Expect(err).To(HaveOccurred())
		})

		It("should handle clusters with wrong ports", func() {
			By("Creating secret with wrong port")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-oidc-proxy-kubeconfigs",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"wrong-port-cluster": []byte(`
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://kube-oidc-proxy-e2e-control-plane:9999
    insecure-skip-tls-verify: true
  name: wrong-port
contexts:
- context:
    cluster: wrong-port
    user: admin
  name: wrong-port-context
current-context: wrong-port-context
users:
- name: admin
  user:
    token: test-token
`),
				},
			}
			_, err := f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying wrong port is handled gracefully")
			time.Sleep(3 * time.Second)
			config := f.NewProxyRestConfig()
			config.Host = config.Host + "/wrong-port-cluster"
			client, err := kubernetes.NewForConfig(config)
			Expect(err).NotTo(HaveOccurred())

			_, err = client.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("Mixed Valid and Invalid Configurations", func() {
		It("should process valid clusters while skipping invalid ones", func() {
			By("Creating secret with mixed valid and invalid configurations")

			validKubeconfigBytes, err := getValidKubeconfig(f)
			Expect(err).NotTo(HaveOccurred())
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-oidc-proxy-kubeconfigs",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"valid-cluster":   validKubeconfigBytes,
					"invalid-yaml":    []byte("invalid: yaml: content: ["),
					"valid-cluster-2": validKubeconfigBytes,
					"missing-server":  []byte("apiVersion: v1\nkind: Config\nclusters: []\n"),
					"valid-cluster-3": validKubeconfigBytes,
				},
			}
			_, err = f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Creating Clusterrole and Clusterrolebinding for list namespace")
			err = createRBACForListNamespace(f)
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for processing")
			time.Sleep(5 * time.Second)

			By("Verifying valid clusters are accessible")
			for _, clusterName := range []string{"valid-cluster", "valid-cluster-2", "valid-cluster-3"} {
				config := f.NewProxyRestConfig()
				config.Host = config.Host + "/" + clusterName
				client, err := kubernetes.NewForConfig(config)
				Expect(err).NotTo(HaveOccurred())

				Eventually(func() error {
					_, err := client.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
					return err
				}, 10*time.Second).Should(Succeed(), "Valid cluster %s should be accessible", clusterName)
			}

			By("Verifying invalid clusters are not accessible")
			for _, clusterName := range []string{"invalid-yaml", "missing-server"} {
				config := f.NewProxyRestConfig()
				config.Host = config.Host + "/" + clusterName
				client, err := kubernetes.NewForConfig(config)
				Expect(err).NotTo(HaveOccurred())

				_, err = client.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
				Expect(err).To(HaveOccurred(), "Invalid cluster %s should not be accessible", clusterName)
			}

			err = f.Helper().DeleteCRDObject("namespace-reader", crd.CAPIClusterRoleGVR, "")
			Expect(err).NotTo(HaveOccurred())

			err = f.Helper().DeleteCRDObject("namespace-reader-binding", crd.CAPIClusterRoleBindingGVR, "")
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("Kubeconfig Format Validation", func() {
		It("should reject secrets with invalid YAML format", func() {
			By("Creating secret with invalid YAML")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-oidc-proxy-kubeconfigs",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"invalid-yaml-cluster": []byte(`
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://example.com:6443
  name: test
contexts:
- context:
    cluster: test
    user: admin
  name: test-context
current-context: test-context
users:
- name: admin
  user: {invalid yaml syntax}
`),
				},
			}
			_, err := f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying cluster is not accessible")
			time.Sleep(3 * time.Second)
			config := f.NewProxyRestConfig()
			config.Host = config.Host + "/invalid-yaml-cluster"
			client, err := kubernetes.NewForConfig(config)
			Expect(err).NotTo(HaveOccurred())

			_, err = client.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
			Expect(err).To(HaveOccurred())
		})

		It("should reject secrets with missing required kubeconfig fields", func() {
			By("Creating secret with missing clusters section")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-oidc-proxy-kubeconfigs",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"missing-clusters": []byte(`
apiVersion: v1
kind: Config
contexts:
- context:
    cluster: test
    user: admin
  name: test-context
current-context: test-context
users:
- name: admin
  user:
    token: test-token
`),
				},
			}
			_, err := f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying cluster is not accessible")
			time.Sleep(3 * time.Second)
			config := f.NewProxyRestConfig()
			config.Host = config.Host + "/missing-clusters"
			client, err := kubernetes.NewForConfig(config)
			Expect(err).NotTo(HaveOccurred())

			_, err = client.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
			Expect(err).To(HaveOccurred())
		})

		It("should reject secrets with missing server URL", func() {
			By("Creating secret with missing server URL")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-oidc-proxy-kubeconfigs",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"missing-server": []byte(`
apiVersion: v1
kind: Config
clusters:
- cluster:
    insecure-skip-tls-verify: true
  name: test
contexts:
- context:
    cluster: test
    user: admin
  name: test-context
current-context: test-context
users:
- name: admin
  user:
    token: test-token
`),
				},
			}
			_, err := f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying cluster is not accessible")
			time.Sleep(3 * time.Second)
			config := f.NewProxyRestConfig()
			config.Host = config.Host + "/missing-server"
			client, err := kubernetes.NewForConfig(config)
			Expect(err).NotTo(HaveOccurred())

			_, err = client.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
			Expect(err).To(HaveOccurred())
		})

		It("should handle kubeconfig with invalid server URL", func() {
			By("Creating secret with invalid server URL")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-oidc-proxy-kubeconfigs",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"invalid-server": []byte(`
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: not-a-valid-url
    insecure-skip-tls-verify: true
  name: test
contexts:
- context:
    cluster: test
    user: admin
  name: test-context
current-context: test-context
users:
- name: admin
  user:
    token: test-token
`),
				},
			}
			_, err := f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying cluster is not accessible")
			time.Sleep(3 * time.Second)
			config := f.NewProxyRestConfig()
			config.Host = config.Host + "/invalid-server"
			client, err := kubernetes.NewForConfig(config)
			Expect(err).NotTo(HaveOccurred())

			_, err = client.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("Secret Filtering", func() {
		It("should only process secrets named 'kube-oidc-proxy-kubeconfigs' in default namespace", func() {
			By("Creating valid secret with correct name/namespace")
			validSecret, err := createSingleClusterSecret("valid-cluster", f)
			Expect(err).NotTo(HaveOccurred())

			_, err = f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), validSecret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Creating secret with wrong name in default namespace")
			wrongNameSecret, err := createSingleClusterSecret("wrong-name-cluster", f)
			Expect(err).NotTo(HaveOccurred())

			wrongNameSecret.Name = "incorrect-secret-name"
			_, err = f.Helper().KubeClient.CoreV1().Secrets("default").Create(context.TODO(), wrongNameSecret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Creating secret with correct name in wrong namespace")
			wrongNsSecret, err := createSingleClusterSecret("wrong-ns-cluster", f)
			Expect(err).NotTo(HaveOccurred())

			wrongNsSecret.Namespace = "kube-system"
			_, err = f.Helper().KubeClient.CoreV1().Secrets("kube-system").Create(context.TODO(), wrongNsSecret, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Creating Clusterrole and Clusterrolebinding for list namespace")
			err = createRBACForListNamespace(f)
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for processing")
			time.Sleep(3 * time.Second)

			By("Verifying only valid cluster is accessible")
			// Valid cluster should work
			validConfig := f.NewProxyRestConfig()
			validConfig.Host = validConfig.Host + "/valid-cluster"
			validClient, err := kubernetes.NewForConfig(validConfig)
			Expect(err).NotTo(HaveOccurred())
			Eventually(func() error {
				_, err := validClient.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
				return err
			}, 10*time.Second).Should(Succeed())

			By("Verifying invalid clusters are ignored")
			// Wrong name cluster
			wrongNameConfig := f.NewProxyRestConfig()
			wrongNameConfig.Host = wrongNameConfig.Host + "/wrong-name-cluster"
			wrongNameClient, err := kubernetes.NewForConfig(wrongNameConfig)
			Expect(err).NotTo(HaveOccurred())
			_, err = wrongNameClient.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
			Expect(err).To(HaveOccurred())

			// Wrong namespace cluster
			wrongNsConfig := f.NewProxyRestConfig()
			wrongNsConfig.Host = wrongNsConfig.Host + "/wrong-ns-cluster"
			wrongNsClient, err := kubernetes.NewForConfig(wrongNsConfig)
			Expect(err).NotTo(HaveOccurred())
			_, err = wrongNsClient.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
			Expect(err).To(HaveOccurred())

			err = f.Helper().DeleteCRDObject("namespace-reader", crd.CAPIClusterRoleGVR, "")
			Expect(err).NotTo(HaveOccurred())

			err = f.Helper().DeleteCRDObject("namespace-reader-binding", crd.CAPIClusterRoleBindingGVR, "")
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

// Helper functions

func createSingleClusterSecret(clusterName string, f *framework.Framework) (*corev1.Secret, error) {
	kubeconfigData, err := getValidKubeconfig(f)
	if err != nil {
		return nil, err
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-oidc-proxy-kubeconfigs",
			Namespace: "default",
		},
		Data: map[string][]byte{
			clusterName: kubeconfigData,
		},
	}, nil
}

func createMultiClusterSecret(f *framework.Framework, clusterNames ...string) (*corev1.Secret, error) {

	kubeconfigData, err := getValidKubeconfig(f)
	if err != nil {
		return nil, err
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-oidc-proxy-kubeconfigs",
			Namespace: "default",
		},
		Data: make(map[string][]byte),
	}

	for _, name := range clusterNames {
		secret.Data[name] = kubeconfigData
	}

	return secret, nil
}

func getValidKubeconfig(f *framework.Framework) ([]byte, error) {
	kindKubeconfigBytes, err := os.ReadFile(f.Helper().Config().KubeConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read KinD kubeconfig: %v", err)
	}

	serverRegex := regexp.MustCompile(`server: https://127\.0\.0\.1:\d+`)
	kindKubeConfig := serverRegex.ReplaceAllString(string(kindKubeconfigBytes),
		"server: https://kube-oidc-proxy-e2e-control-plane:6443")

	return []byte(kindKubeConfig), nil
}

func createRBACForListNamespace(f *framework.Framework) error {
	By("Creating RBAC rules for list namespace")
	role := &crd.CAPIClusterRole{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "rbac.platformengineers.io/v1",
			Kind:       "CAPIClusterRole",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "namespace-reader",
		},
		Spec: crd.CAPIClusterRoleSpec{
			CommonRoleSpec: crd.CommonRoleSpec{
				TargetClusters: []string{"*"},
				Rules: []v1.PolicyRule{
					{
						APIGroups: []string{""},
						Resources: []string{"namespaces"},
						Verbs:     []string{"get", "list"},
					},
				},
			},
		},
	}
	err := f.Helper().CreateCRDObject(role, crd.CAPIClusterRoleGVR, "")
	if err != nil {
		return err
	}

	binding := &crd.CAPIClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "rbac.platformengineers.io/v1",
			Kind:       "CAPIClusterRoleBinding",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "namespace-reader-binding",
		},
		Spec: crd.CAPIClusterRoleBindingSpec{
			CommonBindingSpec: crd.CommonBindingSpec{
				RoleRef:        []string{"namespace-reader"},
				Subjects:       []crd.Subject{{Group: "group-1"}},
				TargetClusters: []string{"*"},
			},
		},
	}
	err = f.Helper().CreateCRDObject(binding, crd.CAPIClusterRoleBindingGVR, "")
	return err
}
