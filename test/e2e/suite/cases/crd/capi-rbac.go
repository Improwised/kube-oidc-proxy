package crd

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/Improwised/kube-oidc-proxy/constants"
	typesv1 "github.com/Improwised/kube-oidc-proxy/pkg/proxy/crd/types/v1"
	"github.com/Improwised/kube-oidc-proxy/test/e2e/framework"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/rbac/v1"
	"k8s.io/client-go/kubernetes"
)

var _ = framework.CasesDescribe("CRD CAPI-RBAC", func() {
	f := framework.NewDefaultFramework("capi-rbac")

	It("should enforce RBAC rules from CAPIRole and CAPIRoleBinding", func() {
		By("Creating CAPIRole allowing GET pods")
		capiRole := &typesv1.CAPIRole{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "rbac.platformengineers.io/v1",
				Kind:       "CAPIRole",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-reader",
				Namespace: f.Namespace.Name,
			},
			Spec: typesv1.CAPIRoleSpec{
				CommonRoleSpec: typesv1.CommonRoleSpec{
					TargetClusters: []string{constants.ClusterName},
					Rules: []v1.PolicyRule{
						{
							APIGroups: []string{""},
							Resources: []string{"pods"},
							Verbs:     []string{"get", "list"},
						},
					},
				},
				TargetNamespaces: []string{f.Namespace.Name},
			},
		}
		err := f.Helper().CreateCRDObject(capiRole, constants.CAPIRoleGVR, f.Namespace.Name)
		Expect(err).NotTo(HaveOccurred())

		By("Creating CAPIRoleBinding for group-1")
		capiRoleBinding := &typesv1.CAPIRoleBinding{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "rbac.platformengineers.io/v1",
				Kind:       "CAPIRoleBinding",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-reader-binding",
				Namespace: f.Namespace.Name,
			},
			Spec: typesv1.CAPIRoleBindingSpec{
				CommonBindingSpec: typesv1.CommonBindingSpec{
					RoleRefs: []v1.RoleRef{
						{APIGroup: v1.GroupName, Kind: "Role", Name: "test-pod-reader"},
					},
					Subjects:       []typesv1.Subject{{Group: "group-1"}},
					TargetClusters: []string{constants.ClusterName},
				},
				TargetNamespaces: []string{f.Namespace.Name},
			},
		}
		err = f.Helper().CreateCRDObject(capiRoleBinding, constants.CAPIRoleBindingGVR, f.Namespace.Name)
		Expect(err).NotTo(HaveOccurred())

		By("Waiting for RBAC reconciliation")
		// Wait for proxy to process CRDs (adjust timeout as needed)
		time.Sleep(5 * time.Second)

		By("Listing pods (should succeed)")
		_, err = f.ProxyClient.CoreV1().Pods(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("Listing services (should be forbidden)")
		_, err = f.ProxyClient.CoreV1().Services(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
		Expect(k8sErrors.IsForbidden(err)).To(BeTrue())
	})

	It("should enforce cluster-wide RBAC from CAPIClusterRole", func() {
		By("Creating CAPIClusterRole allowing node access")
		capiClusterRole := &typesv1.CAPIClusterRole{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "rbac.platformengineers.io/v1",
				Kind:       "CAPIClusterRole",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-node-reader",
			},
			Spec: typesv1.CAPIClusterRoleSpec{
				CommonRoleSpec: typesv1.CommonRoleSpec{
					TargetClusters: []string{constants.ClusterName},
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
		err := f.Helper().CreateCRDObject(capiClusterRole, constants.CAPIClusterRoleGVR, "")
		Expect(err).NotTo(HaveOccurred())

		By("Creating CAPIClusterRoleBinding for group-1")
		capiClusterRoleBinding := &typesv1.CAPIClusterRoleBinding{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "rbac.platformengineers.io/v1",
				Kind:       "CAPIClusterRoleBinding",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-node-reader-binding",
			},
			Spec: typesv1.CAPIClusterRoleBindingSpec{
				CommonBindingSpec: typesv1.CommonBindingSpec{
					RoleRefs: []v1.RoleRef{
						{APIGroup: v1.GroupName, Kind: "ClusterRole", Name: "test-node-reader"},
					},
					Subjects:       []typesv1.Subject{{Group: "group-1"}},
					TargetClusters: []string{constants.ClusterName},
				},
			},
		}
		err = f.Helper().CreateCRDObject(capiClusterRoleBinding, constants.CAPIClusterRoleBindingGVR, "")
		Expect(err).NotTo(HaveOccurred())

		By("Waiting for RBAC reconciliation")
		time.Sleep(5 * time.Second)

		By("Listing nodes (should succeed)")
		_, err = f.ProxyClient.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
	})

	It("should handle updates to CAPIRole dynamically", func() {
		By("Creating initial CAPIRole")
		capiRole := &typesv1.CAPIRole{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "rbac.platformengineers.io/v1",
				Kind:       "CAPIRole",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-dynamic-role",
				Namespace: f.Namespace.Name,
			},
			Spec: typesv1.CAPIRoleSpec{
				CommonRoleSpec: typesv1.CommonRoleSpec{
					TargetClusters: []string{constants.ClusterName},
					Rules:          []v1.PolicyRule{}, // No rules initially
				},
				TargetNamespaces: []string{f.Namespace.Name},
			},
		}
		err := f.Helper().CreateCRDObject(capiRole, constants.CAPIRoleGVR, f.Namespace.Name)
		Expect(err).NotTo(HaveOccurred())

		capiRoleBinding := &typesv1.CAPIRoleBinding{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "rbac.platformengineers.io/v1",
				Kind:       "CAPIRoleBinding",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-dynamic-role-binding",
				Namespace: f.Namespace.Name,
			},
			Spec: typesv1.CAPIRoleBindingSpec{
				CommonBindingSpec: typesv1.CommonBindingSpec{
					RoleRefs: []v1.RoleRef{
						{APIGroup: v1.GroupName, Kind: "Role", Name: "test-dynamic-role"},
					},
					Subjects:       []typesv1.Subject{{Group: "group-1"}},
					TargetClusters: []string{constants.ClusterName},
				},
				TargetNamespaces: []string{f.Namespace.Name},
			},
		}
		err = f.Helper().CreateCRDObject(capiRoleBinding, constants.CAPIRoleBindingGVR, f.Namespace.Name)
		Expect(err).NotTo(HaveOccurred())

		By("Verifying initial forbidden access")
		_, err = f.ProxyClient.CoreV1().Pods(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
		Expect(k8sErrors.IsForbidden(err)).To(BeTrue())

		By("Updating CAPIRole to allow pods")
		capiRole.Spec.CommonRoleSpec.Rules = []v1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"pods"},
				Verbs:     []string{"get", "list"},
			},
		}
		err = f.Helper().UpdateCRDObject(capiRole, constants.CAPIRoleGVR, f.Namespace.Name)
		Expect(err).NotTo(HaveOccurred())

		By("Waiting for update to propagate")
		time.Sleep(5 * time.Second)

		By("Listing pods (should succeed after update)")
		_, err = f.ProxyClient.CoreV1().Pods(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
	})

	It("should bind existing cluster roles using CAPIClusterRoleBinding", func() {
		// Create native ClusterRole
		_, err := f.Helper().KubeClient.RbacV1().ClusterRoles().Create(context.TODO(), &v1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{Name: "existing-cluster-role"},
			Rules: []v1.PolicyRule{
				{
					APIGroups: []string{""},
					Resources: []string{"nodes"},
					Verbs:     []string{"get", "list"},
				},
			},
		}, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		// Create CRD binding
		capiBinding := &typesv1.CAPIClusterRoleBinding{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "rbac.platformengineers.io/v1",
				Kind:       "CAPIClusterRoleBinding",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "existing-role-binding",
			},
			Spec: typesv1.CAPIClusterRoleBindingSpec{
				CommonBindingSpec: typesv1.CommonBindingSpec{
					TargetClusters: []string{constants.ClusterName},
					RoleRefs: []v1.RoleRef{
						{APIGroup: v1.GroupName, Kind: "ClusterRole", Name: "existing-cluster-role"},
					},
					Subjects: []typesv1.Subject{{Group: "group-1"}},
				},
			},
		}
		Expect(f.Helper().CreateCRDObject(capiBinding, constants.CAPIClusterRoleBindingGVR, "")).To(Succeed())

		By("Verifying node access")
		Eventually(func() error {
			_, err := f.ProxyClient.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
			return err
		}, 10*time.Second).Should(Succeed())
	})

	It("should apply roles to multiple target namespaces", func() {
		// Create 2 test namespaces
		ns1, _ := f.CreateKubeNamespace("target-ns-1")
		ns2, _ := f.CreateKubeNamespace("target-ns-2")

		// Create CRD role targeting multiple namespaces
		capiRole := &typesv1.CAPIRole{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "rbac.platformengineers.io/v1",
				Kind:       "CAPIRole",
			},
			ObjectMeta: metav1.ObjectMeta{Name: "cross-ns-role",
				Namespace: f.Namespace.Name,
			},
			Spec: typesv1.CAPIRoleSpec{
				CommonRoleSpec: typesv1.CommonRoleSpec{
					Rules: []v1.PolicyRule{
						{
							APIGroups: []string{""},
							Resources: []string{"configmaps"},
							Verbs:     []string{"get", "list"},
						},
					},
					TargetClusters: []string{constants.ClusterName},
				},
				TargetNamespaces: []string{ns1.Name, ns2.Name},
			},
		}
		Expect(f.Helper().CreateCRDObject(capiRole, constants.CAPIRoleGVR, f.Namespace.Name)).To(Succeed())

		capiRoleBinding := &typesv1.CAPIRoleBinding{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "rbac.platformengineers.io/v1",
				Kind:       "CAPIRoleBinding",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-reader-binding",
				Namespace: f.Namespace.Name,
			},
			Spec: typesv1.CAPIRoleBindingSpec{
				CommonBindingSpec: typesv1.CommonBindingSpec{
					RoleRefs: []v1.RoleRef{
						{APIGroup: v1.GroupName, Kind: "Role", Name: "cross-ns-role"},
					},
					Subjects:       []typesv1.Subject{{Group: "group-1"}},
					TargetClusters: []string{constants.ClusterName},
				},
				TargetNamespaces: []string{ns1.Name, ns2.Name},
			},
		}
		err := f.Helper().CreateCRDObject(capiRoleBinding, constants.CAPIRoleBindingGVR, f.Namespace.Name)
		Expect(err).NotTo(HaveOccurred())

		// Test access in both namespaces
		By("Accessing ns1")
		Eventually(func() error {
			_, err := f.ProxyClient.CoreV1().ConfigMaps(ns1.Name).List(context.TODO(), metav1.ListOptions{})
			return err
		}, 10*time.Second).Should(Succeed())

		By("Accessing ns2")
		Eventually(func() error {
			_, err := f.ProxyClient.CoreV1().ConfigMaps(ns2.Name).List(context.TODO(), metav1.ListOptions{})
			return err
		}, 10*time.Second).Should(Succeed())
	})

	It("should deny access to unauthenticated users", func() {
		// Create invalid rest config without token
		invalidConfig := f.NewProxyRestConfig()
		invalidConfig.BearerToken = ""
		invalidClient, _ := kubernetes.NewForConfig(invalidConfig)

		_, err := invalidClient.CoreV1().Pods(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
		Expect(k8sErrors.IsUnauthorized(err)).To(BeTrue())
	})

	It("should deny access to unauthorized users", func() {
		// Create token for user without permissions
		token := f.Helper().NewTokenPayload(f.IssuerURL(), "unauthorized-client", time.Now().Add(time.Hour))
		signedToken, _ := f.Helper().SignToken(f.IssuerKeyBundle(), token)

		invalidConfig := f.NewProxyRestConfig()
		invalidConfig.BearerToken = signedToken
		invalidClient, _ := kubernetes.NewForConfig(invalidConfig)

		_, err := invalidClient.CoreV1().Secrets(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
		Expect(k8sErrors.IsUnauthorized(err)).To(BeTrue())

	})

	It("should combine permissions from multiple roles", func() {
		// Create Pod role
		podRole := &typesv1.CAPIRole{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "rbac.platformengineers.io/v1",
				Kind:       "CAPIRole",
			},
			ObjectMeta: metav1.ObjectMeta{Name: "multi-role-pods",
				Namespace: f.Namespace.Name,
			},
			Spec: typesv1.CAPIRoleSpec{
				CommonRoleSpec: typesv1.CommonRoleSpec{
					Rules: []v1.PolicyRule{
						{
							APIGroups: []string{""},
							Resources: []string{"pods"},
							Verbs:     []string{"get", "list"},
						},
					},
					TargetClusters: []string{constants.ClusterName},
				},
				TargetNamespaces: []string{f.Namespace.Name},
			},
		}
		Expect(f.Helper().CreateCRDObject(podRole, constants.CAPIRoleGVR, f.Namespace.Name)).To(Succeed())

		// Create Service role
		svcRole := &typesv1.CAPIRole{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "rbac.platformengineers.io/v1",
				Kind:       "CAPIRole",
			},
			ObjectMeta: metav1.ObjectMeta{Name: "multi-role-services",
				Namespace: f.Namespace.Name,
			},
			Spec: typesv1.CAPIRoleSpec{
				CommonRoleSpec: typesv1.CommonRoleSpec{
					Rules: []v1.PolicyRule{
						{
							APIGroups: []string{""},
							Resources: []string{"services"},
							Verbs:     []string{"list"},
						},
					},
					TargetClusters: []string{constants.ClusterName},
				},
				TargetNamespaces: []string{f.Namespace.Name},
			},
		}
		Expect(f.Helper().CreateCRDObject(svcRole, constants.CAPIRoleGVR, f.Namespace.Name)).To(Succeed())

		capiRoleBinding := &typesv1.CAPIRoleBinding{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "rbac.platformengineers.io/v1",
				Kind:       "CAPIRoleBinding",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "multiple-role-ref",
				Namespace: f.Namespace.Name,
			},
			Spec: typesv1.CAPIRoleBindingSpec{
				CommonBindingSpec: typesv1.CommonBindingSpec{
					RoleRefs: []v1.RoleRef{
						{APIGroup: v1.GroupName, Kind: "Role", Name: "multi-role-pods"},
						{APIGroup: v1.GroupName, Kind: "Role", Name: "multi-role-services"},
					},
					Subjects:       []typesv1.Subject{{Group: "group-1"}},
					TargetClusters: []string{constants.ClusterName},
				},
				TargetNamespaces: []string{f.Namespace.Name},
			},
		}

		err := f.Helper().CreateCRDObject(capiRoleBinding, constants.CAPIRoleBindingGVR, f.Namespace.Name)
		Expect(err).NotTo(HaveOccurred())

		By("Verifying combined access")
		Eventually(func() error {
			_, err := f.ProxyClient.CoreV1().Pods(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
			return err
		}, 10*time.Second).Should(Succeed())

		Eventually(func() error {
			_, err := f.ProxyClient.CoreV1().Services(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
			return err
		}, 10*time.Second).Should(Succeed())
	})

	It("should handle wildcard resource permissions", func() {
		// Create wildcard role
		wildcardRole := &typesv1.CAPIClusterRole{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "rbac.platformengineers.io/v1",
				Kind:       "CAPIClusterRole",
			},
			ObjectMeta: metav1.ObjectMeta{Name: "wildcard-role"},
			Spec: typesv1.CAPIClusterRoleSpec{
				CommonRoleSpec: typesv1.CommonRoleSpec{
					Rules: []v1.PolicyRule{
						{
							APIGroups: []string{""},
							Resources: []string{"*"},
							Verbs:     []string{"get", "list"},
						},
					},
					TargetClusters: []string{constants.ClusterName},
				},
			},
		}
		Expect(f.Helper().CreateCRDObject(wildcardRole, constants.CAPIClusterRoleGVR, "")).To(Succeed())
		By("creating binding")

		capiClusterRoleBinding := &typesv1.CAPIClusterRoleBinding{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "rbac.platformengineers.io/v1",
				Kind:       "CAPIClusterRoleBinding",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-binding",
			},
			Spec: typesv1.CAPIClusterRoleBindingSpec{
				CommonBindingSpec: typesv1.CommonBindingSpec{
					RoleRefs: []v1.RoleRef{
						{APIGroup: v1.GroupName, Kind: "Role", Name: "wildcard-role"},
					},
					Subjects:       []typesv1.Subject{{Group: "group-1"}},
					TargetClusters: []string{constants.ClusterName},
				},
			},
		}

		err := f.Helper().CreateCRDObject(capiClusterRoleBinding, constants.CAPIClusterRoleBindingGVR, "")
		Expect(err).NotTo(HaveOccurred())

		// Test access to multiple resources
		By("Accessing pods")
		Eventually(func() error {
			_, err := f.ProxyClient.CoreV1().Pods(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
			return err
		}, 10*time.Second).Should(Succeed())

		By("Accessing nodes")
		Eventually(func() error {
			_, err := f.ProxyClient.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
			return err
		}, 10*time.Second).Should(Succeed())

		By("Denying write operations")
		_, err = f.ProxyClient.CoreV1().Pods(f.Namespace.Name).Create(
			context.TODO(),
			&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test"}},
			metav1.CreateOptions{},
		)
		Expect(k8sErrors.IsForbidden(err)).To(BeTrue())
	})

	It("should apply roles to all clusters using wildcard target", func() {
		By("Creating CAPIRole with wildcard cluster target")
		capiRole := &typesv1.CAPIRole{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "rbac.platformengineers.io/v1",
				Kind:       "CAPIRole",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "wildcard-cluster-role",
				Namespace: f.Namespace.Name,
			},
			Spec: typesv1.CAPIRoleSpec{
				CommonRoleSpec: typesv1.CommonRoleSpec{
					TargetClusters: []string{"*"},
					Rules: []v1.PolicyRule{
						{
							APIGroups: []string{""},
							Resources: []string{"configmaps"},
							Verbs:     []string{"get", "list"},
						},
					},
				},
				TargetNamespaces: []string{f.Namespace.Name},
			},
		}
		err := f.Helper().CreateCRDObject(capiRole, constants.CAPIRoleGVR, f.Namespace.Name)
		Expect(err).NotTo(HaveOccurred())

		By("Creating CAPIRoleBinding with wildcard cluster target")
		capiBinding := &typesv1.CAPIRoleBinding{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "rbac.platformengineers.io/v1",
				Kind:       "CAPIRoleBinding",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "wildcard-cluster-binding",
				Namespace: f.Namespace.Name,
			},
			Spec: typesv1.CAPIRoleBindingSpec{
				CommonBindingSpec: typesv1.CommonBindingSpec{
					RoleRefs: []v1.RoleRef{
						{APIGroup: v1.GroupName, Kind: "ClusterRole", Name: "wildcard-cluster-role"},
					},
					Subjects:       []typesv1.Subject{{Group: "group-1"}},
					TargetClusters: []string{"*"},
				},
				TargetNamespaces: []string{f.Namespace.Name},
			},
		}
		err = f.Helper().CreateCRDObject(capiBinding, constants.CAPIRoleBindingGVR, f.Namespace.Name)
		Expect(err).NotTo(HaveOccurred())

		By("Verifying access with wildcard clusters")
		Eventually(func() error {
			_, err := f.ProxyClient.CoreV1().ConfigMaps(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
			return err
		}, 10*time.Second).Should(Succeed())
	})

	It("should not grant access with empty targetClusters", func() {
		By("Creating role with empty targetClusters")
		capiRole := &typesv1.CAPIRole{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "rbac.platformengineers.io/v1",
				Kind:       "CAPIRole",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "empty-target-role",
				Namespace: f.Namespace.Name,
			},
			Spec: typesv1.CAPIRoleSpec{
				CommonRoleSpec: typesv1.CommonRoleSpec{
					TargetClusters: []string{},
					Rules: []v1.PolicyRule{
						{
							APIGroups: []string{""},
							Resources: []string{"secrets"},
							Verbs:     []string{"get"},
						},
					},
				},
				TargetNamespaces: []string{f.Namespace.Name},
			},
		}
		err := f.Helper().CreateCRDObject(capiRole, constants.CAPIRoleGVR, f.Namespace.Name)
		Expect(err).NotTo(HaveOccurred())

		By("Creating valid binding")
		capiBinding := &typesv1.CAPIRoleBinding{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "rbac.platformengineers.io/v1",
				Kind:       "CAPIRoleBinding",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "empty-target-binding",
				Namespace: f.Namespace.Name,
			},
			Spec: typesv1.CAPIRoleBindingSpec{
				CommonBindingSpec: typesv1.CommonBindingSpec{
					RoleRefs: []v1.RoleRef{
						{APIGroup: v1.GroupName, Kind: "Role", Name: "empty-target-role"},
					},
					Subjects:       []typesv1.Subject{{Group: "group-1"}},
					TargetClusters: []string{constants.ClusterName},
				},
				TargetNamespaces: []string{f.Namespace.Name},
			},
		}
		err = f.Helper().CreateCRDObject(capiBinding, constants.CAPIRoleBindingGVR, f.Namespace.Name)
		Expect(err).NotTo(HaveOccurred())

		By("Verifying no access granted")
		_, err = f.ProxyClient.CoreV1().Secrets(f.Namespace.Name).List(context.TODO(), metav1.ListOptions{})
		Expect(k8sErrors.IsForbidden(err)).To(BeTrue())
	})
})
