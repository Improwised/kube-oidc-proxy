package crd

import (
	"fmt"

	"github.com/Improwised/kube-oidc-proxy/constants"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy"
	"github.com/Improwised/kube-oidc-proxy/pkg/util"
	v1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	rbacvalidation "k8s.io/kubernetes/pkg/registry/rbac/validation"
)

func (ctrl *CAPIRbacWatcher) ConvertUnstructuredToCAPIRole(obj interface{}) (*CAPIRole, error) {
	u := obj.(*unstructured.Unstructured)
	var capiRole CAPIRole
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.UnstructuredContent(), &capiRole); err != nil {
		return nil, fmt.Errorf("conversion error: %w", err)
	}
	return &capiRole, nil
}

func (ctrl *CAPIRbacWatcher) ConvertUnstructuredToCAPIClusterRole(obj interface{}) (*CAPIClusterRole, error) {
	u := obj.(*unstructured.Unstructured)
	var capiClusterRole CAPIClusterRole
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.UnstructuredContent(), &capiClusterRole); err != nil {
		return nil, fmt.Errorf("conversion error: %w", err)
	}
	return &capiClusterRole, nil
}

func (ctrl *CAPIRbacWatcher) ConvertUnstructuredToCAPIClusterRoleBinding(obj interface{}) (*CAPIClusterRoleBinding, error) {
	u := obj.(*unstructured.Unstructured)
	var capiClusterRoleBinding CAPIClusterRoleBinding
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.UnstructuredContent(), &capiClusterRoleBinding); err != nil {
		return nil, fmt.Errorf("conversion error: %w", err)
	}
	return &capiClusterRoleBinding, nil
}

func (ctrl *CAPIRbacWatcher) ConvertUnstructuredToCAPIRoleBinding(obj interface{}) (*CAPIRoleBinding, error) {
	u := obj.(*unstructured.Unstructured)
	var capiRoleBinding CAPIRoleBinding
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.UnstructuredContent(), &capiRoleBinding); err != nil {
		return nil, fmt.Errorf("conversion error: %w", err)
	}
	return &capiRoleBinding, nil
}

func determineTargetClusters(targetClusters []string, clusters []*proxy.ClusterConfig) []string {
	if len(targetClusters) == 0 {
		return getAllClusterNames(clusters)
	}
	return targetClusters
}

func getAllClusterNames(clusters []*proxy.ClusterConfig) []string {
	names := make([]string, 0, len(clusters))
	for _, c := range clusters {
		names = append(names, c.Name)
	}
	return names
}

func applyToClusters(targetClusters []string, allClusters []*proxy.ClusterConfig, applyFunc func(*proxy.ClusterConfig)) {
	for _, clusterName := range targetClusters {
		for _, c := range allClusters {
			if c.Name == clusterName {
				applyFunc(c)
				break
			}
		}
	}
}

func createRole(role *CAPIRole, ns string) *v1.Role {
	return &v1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name: role.Name,
			Annotations: map[string]string{
				fmt.Sprintf("%s/managed-by", constants.Group): role.Name,
			},
			Namespace: ns},
		Rules: role.Spec.Rules,
	}
}

func createClusterRole(clusterRole *CAPIClusterRole) *v1.ClusterRole {
	return &v1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: clusterRole.Name,
			Annotations: map[string]string{
				fmt.Sprintf("%s/managed-by", constants.Group): clusterRole.Name,
			}},
		Rules: clusterRole.Spec.Rules,
	}
}

func determineSubjectKind(subject Subject) string {
	if subject.Group != "" {
		return "Group"
	} else if subject.User != "" {
		return "User"
	} else if subject.ServiceAccount != "" {
		return "ServiceAccount"
	}
	return ""
}

func determineSubjectName(subject Subject) string {
	if subject.Group != "" {
		return subject.Group
	} else if subject.User != "" {
		return subject.User
	} else if subject.ServiceAccount != "" {
		return subject.ServiceAccount
	}
	return ""
}

func convertSubjects(subs []Subject) []v1.Subject {
	subjects := make([]v1.Subject, len(subs))

	for i, subject := range subs {
		subjects[i] = v1.Subject{
			Kind:     determineSubjectKind(subject),
			Name:     determineSubjectName(subject),
			APIGroup: v1.GroupName,
		}
	}
	return subjects
}

func createClusterRoleBinding(clusterRoleBinding *CAPIClusterRoleBinding) []*v1.ClusterRoleBinding {

	subjects := convertSubjects(clusterRoleBinding.Spec.Subjects)
	clusterRoleBindings := make([]*v1.ClusterRoleBinding, 0, len(clusterRoleBinding.Spec.RoleRef))

	for _, roleRef := range clusterRoleBinding.Spec.RoleRef {
		clusterRoleBindings = append(clusterRoleBindings, &v1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("%s-%s", clusterRoleBinding.Name, roleRef),
				Annotations: map[string]string{
					fmt.Sprintf("%s/managed-by", constants.Group): clusterRoleBinding.Name,
				},
			},
			Subjects: subjects,
			RoleRef: v1.RoleRef{
				APIGroup: v1.GroupName,
				Kind:     "ClusterRole",
				Name:     roleRef,
			},
		})
	}
	return clusterRoleBindings
}

func createRoleBinding(roleBinding *CAPIRoleBinding, namespace string) []*v1.RoleBinding {

	subjects := convertSubjects(roleBinding.Spec.Subjects)
	roleBindings := make([]*v1.RoleBinding, 0, len(roleBinding.Spec.RoleRef))

	for _, roleRef := range roleBinding.Spec.RoleRef {
		roleBindings = append(roleBindings, &v1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("%s-%s", roleBinding.Name, roleRef),
				Annotations: map[string]string{
					fmt.Sprintf("%s/managed-by", constants.Group): roleBinding.Name,
				},
				Namespace: namespace,
			},
			Subjects: subjects,
			RoleRef: v1.RoleRef{
				APIGroup: v1.GroupName,
				Kind:     "Role",
				Name:     roleRef,
			},
		})
	}
	return roleBindings
}

func (ctrl *CAPIRbacWatcher) ProcessCAPIRole(capiRole *CAPIRole) {
	targetClusters := determineTargetClusters(capiRole.Spec.CommonRoleSpec.TargetClusters, ctrl.clusters)

	for _, namespace := range capiRole.Spec.TargetNamespaces {
		role := createRole(capiRole, namespace)

		applyToClusters(targetClusters, ctrl.clusters, func(c *proxy.ClusterConfig) {
			c.RBACConfig.Roles = append(c.RBACConfig.Roles, role)
		})
	}

}

func (ctrl *CAPIRbacWatcher) ProcessCAPIClusterRole(capiClusterRole *CAPIClusterRole) {
	targetClusters := determineTargetClusters(capiClusterRole.Spec.CommonRoleSpec.TargetClusters, ctrl.clusters)

	clusterRole := createClusterRole(capiClusterRole)
	applyToClusters(targetClusters, ctrl.clusters, func(c *proxy.ClusterConfig) {
		c.RBACConfig.ClusterRoles = append(c.RBACConfig.ClusterRoles, clusterRole)
	})
}

func (ctrl *CAPIRbacWatcher) ProcessCAPIClusterRoleBinding(capiClusterRoleBinding *CAPIClusterRoleBinding) {

	targetClusters := determineTargetClusters(capiClusterRoleBinding.Spec.CommonBindingSpec.TargetClusters, ctrl.clusters)

	clusterRoleBinding := createClusterRoleBinding(capiClusterRoleBinding)

	applyToClusters(targetClusters, ctrl.clusters, func(c *proxy.ClusterConfig) {
		c.RBACConfig.ClusterRoleBindings = append(c.RBACConfig.ClusterRoleBindings, clusterRoleBinding...)
	})

}

func (ctrl *CAPIRbacWatcher) ProcessCAPIRoleBinding(capiRoleBinding *CAPIRoleBinding) {
	targetClusters := determineTargetClusters(capiRoleBinding.Spec.CommonBindingSpec.TargetClusters, ctrl.clusters)

	for _, namespace := range capiRoleBinding.Spec.TargetNamespaces {
		roleBindings := createRoleBinding(capiRoleBinding, namespace)

		applyToClusters(targetClusters, ctrl.clusters, func(c *proxy.ClusterConfig) {
			c.RBACConfig.RoleBindings = append(c.RBACConfig.RoleBindings, roleBindings...)
		})
	}
}

func (ctrl *CAPIRbacWatcher) DeleteCAPIRole(capiRole *CAPIRole) {
	targetClusters := determineTargetClusters(capiRole.Spec.CommonRoleSpec.TargetClusters, ctrl.clusters)
	for _, namespace := range capiRole.Spec.TargetNamespaces {
		applyToClusters(targetClusters, ctrl.clusters, func(c *proxy.ClusterConfig) {
			var newRoles []*v1.Role
			for _, role := range c.RBACConfig.Roles {
				if role.Annotations[fmt.Sprintf("%s/managed-by", constants.Group)] == capiRole.Name && role.Namespace == namespace {
					continue
				}
				newRoles = append(newRoles, role)
			}
			c.RBACConfig.Roles = newRoles
		})
	}
}

func (ctrl *CAPIRbacWatcher) DeleteCAPIClusterRole(capiClusterRole *CAPIClusterRole) {
	targetClusters := determineTargetClusters(capiClusterRole.Spec.CommonRoleSpec.TargetClusters, ctrl.clusters)
	applyToClusters(targetClusters, ctrl.clusters, func(c *proxy.ClusterConfig) {
		var newClusterRoles []*v1.ClusterRole
		for _, cr := range c.RBACConfig.ClusterRoles {
			if cr.Annotations[fmt.Sprintf("%s/managed-by", constants.Group)] == capiClusterRole.Name {
				continue
			}
			newClusterRoles = append(newClusterRoles, cr)
		}
		c.RBACConfig.ClusterRoles = newClusterRoles
	})
}

func (ctrl *CAPIRbacWatcher) DeleteCAPIRoleBinding(capiRoleBinding *CAPIRoleBinding) {
	targetClusters := determineTargetClusters(capiRoleBinding.Spec.CommonBindingSpec.TargetClusters, ctrl.clusters)
	for _, namespace := range capiRoleBinding.Spec.TargetNamespaces {
		applyToClusters(targetClusters, ctrl.clusters, func(c *proxy.ClusterConfig) {
			var newRoleBindings []*v1.RoleBinding
			for _, rb := range c.RBACConfig.RoleBindings {
				if rb.Annotations[fmt.Sprintf("%s/managed-by", constants.Group)] == capiRoleBinding.Name && rb.Namespace == namespace {
					continue
				}
				newRoleBindings = append(newRoleBindings, rb)
			}
			c.RBACConfig.RoleBindings = newRoleBindings
		})
	}
}

func (ctrl *CAPIRbacWatcher) DeleteCAPIClusterRoleBinding(capiClusterRoleBinding *CAPIClusterRoleBinding) {
	targetClusters := determineTargetClusters(capiClusterRoleBinding.Spec.CommonBindingSpec.TargetClusters, ctrl.clusters)
	applyToClusters(targetClusters, ctrl.clusters, func(c *proxy.ClusterConfig) {
		var newClusterRoleBindings []*v1.ClusterRoleBinding
		for _, crb := range c.RBACConfig.ClusterRoleBindings {
			if crb.Annotations[fmt.Sprintf("%s/managed-by", constants.Group)] == capiClusterRoleBinding.Name {
				continue
			}
			newClusterRoleBindings = append(newClusterRoleBindings, crb)
		}
		c.RBACConfig.ClusterRoleBindings = newClusterRoleBindings
	})
}

// rebuildAllAuthorizers updates RBAC authorizers for all clusters.
func (ctrl *CAPIRbacWatcher) RebuildAllAuthorizers() {
	for _, c := range ctrl.clusters {
		_, staticRoles := rbacvalidation.NewTestRuleResolver(
			c.RBACConfig.Roles,
			c.RBACConfig.RoleBindings,
			c.RBACConfig.ClusterRoles,
			c.RBACConfig.ClusterRoleBindings,
		)
		klog.V(5).Infof("Rebuilding authorizer for cluster: %s", c.Name)
		c.Authorizer = util.NewAuthorizer(staticRoles)
	}
}
