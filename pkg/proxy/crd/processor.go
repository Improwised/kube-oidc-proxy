package crd

import (
	"fmt"

	"github.com/Improwised/kube-oidc-proxy/constants"
	"github.com/Improwised/kube-oidc-proxy/pkg/cluster"
	v1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
)

// convertUnstructured is a generic conversion helper
func ConvertUnstructured[T any](obj interface{}) (*T, error) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("expected unstructured object, got %T", obj)
	}

	var result T
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.UnstructuredContent(), &result); err != nil {
		return nil, fmt.Errorf("conversion failed: %w", err)
	}
	return &result, nil
}

func determineTargetClusters(targetClusters []string, clusters []*cluster.Cluster) []string {
	if len(targetClusters) == 1 && targetClusters[0] == "*" {
		return getAllClusterNames(clusters)
	}
	return targetClusters
}

func getAllClusterNames(clusters []*cluster.Cluster) []string {
	names := make([]string, 0, len(clusters))
	for _, c := range clusters {
		names = append(names, c.Name)
	}
	return names
}

func applyToClusters(targetClusters []string, allClusters []*cluster.Cluster, applyFunc func(*cluster.Cluster)) {
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

func createRoleBinding(roleBinding *CAPIRoleBinding, namespace string, ctrl *CAPIRbacWatcher) []*v1.RoleBinding {
	subjects := convertSubjects(roleBinding.Spec.Subjects)
	roleBindings := make([]*v1.RoleBinding, 0, len(roleBinding.Spec.RoleRef))

	for _, roleRef := range roleBinding.Spec.RoleRef {
		kind := determineRoleRefKindAndAPIGroup(roleRef, ctrl, namespace)

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
				Kind:     kind,
				Name:     roleRef,
			},
		})
	}
	return roleBindings
}

func determineRoleRefKindAndAPIGroup(roleRef string, ctrl *CAPIRbacWatcher, namespace string) string {
	// Check if it's a Role first (namespace-scoped takes precedence)
	for _, cluster := range ctrl.clusters {
		// Check existing Roles in the specific namespace
		for _, role := range cluster.RBACConfig.Roles {
			if role.Name == roleRef && role.Namespace == namespace {
				return "Role"
			}
		}
	}

	// If no Role found, check if it's a ClusterRole
	for _, cluster := range ctrl.clusters {
		// Check existing ClusterRoles in the cluster
		for _, clusterRole := range cluster.RBACConfig.ClusterRoles {
			if clusterRole.Name == roleRef {
				return "ClusterRole"
			}
		}
	}

	// Default to Role if not found (for forward compatibility)
	return "Role"
}

func (ctrl *CAPIRbacWatcher) ProcessCAPIRole(capiRole *CAPIRole) {
	targetClusters := determineTargetClusters(capiRole.Spec.CommonRoleSpec.TargetClusters, ctrl.clusters)
	if len(targetClusters) < 1 && len(ctrl.clusters) > 0 {
		klog.Warning("skipping role ", capiRole.Name, " because it doesn't contain target clusters")
		return
	}

	for _, namespace := range capiRole.Spec.TargetNamespaces {
		role := createRole(capiRole, namespace)

		applyToClusters(targetClusters, ctrl.clusters, func(c *cluster.Cluster) {
			ctrl.addOrUpdateRole(c, role)
		})
	}
}

func (ctrl *CAPIRbacWatcher) ProcessCAPIClusterRole(capiClusterRole *CAPIClusterRole) {
	targetClusters := determineTargetClusters(capiClusterRole.Spec.CommonRoleSpec.TargetClusters, ctrl.clusters)
	if len(targetClusters) < 1 && len(ctrl.clusters) > 0 {
		klog.Warning("skipping cluster role ", capiClusterRole.Name, " because it doesn't contain target clusters")
		return
	}

	clusterRole := createClusterRole(capiClusterRole)
	applyToClusters(targetClusters, ctrl.clusters, func(c *cluster.Cluster) {
		ctrl.addOrUpdateClusterRole(c, clusterRole)
		// Re-evaluate existing RoleBindings that might reference this ClusterRole
		ctrl.reevaluateRoleBindingsForClusterRole(c, clusterRole.Name)
	})
}

func (ctrl *CAPIRbacWatcher) ProcessCAPIClusterRoleBinding(capiClusterRoleBinding *CAPIClusterRoleBinding) {
	targetClusters := determineTargetClusters(capiClusterRoleBinding.Spec.CommonBindingSpec.TargetClusters, ctrl.clusters)
	if len(targetClusters) < 1 && len(ctrl.clusters) > 0 {
		klog.Warning("skipping cluster role binding ", capiClusterRoleBinding.Name, " because it doesn't contain target clusters")
		return
	}

	clusterRoleBindings := createClusterRoleBinding(capiClusterRoleBinding)

	applyToClusters(targetClusters, ctrl.clusters, func(c *cluster.Cluster) {
		for _, crb := range clusterRoleBindings {
			ctrl.addOrUpdateClusterRoleBinding(c, crb)
		}
	})
}

func (ctrl *CAPIRbacWatcher) ProcessCAPIRoleBinding(capiRoleBinding *CAPIRoleBinding) {
	targetClusters := determineTargetClusters(capiRoleBinding.Spec.CommonBindingSpec.TargetClusters, ctrl.clusters)
	if len(targetClusters) < 1 && len(ctrl.clusters) > 0 {
		klog.Warning("skipping role binding ", capiRoleBinding.Name, " because it doesn't contain target clusters")
		return
	}

	for _, namespace := range capiRoleBinding.Spec.TargetNamespaces {
		roleBindings := createRoleBinding(capiRoleBinding, namespace, ctrl)

		applyToClusters(targetClusters, ctrl.clusters, func(c *cluster.Cluster) {
			for _, rb := range roleBindings {
				ctrl.addOrUpdateRoleBinding(c, rb)
			}
		})
	}
}

func (ctrl *CAPIRbacWatcher) DeleteCAPIRole(capiRole *CAPIRole) {
	targetClusters := determineTargetClusters(capiRole.Spec.CommonRoleSpec.TargetClusters, ctrl.clusters)
	for _, namespace := range capiRole.Spec.TargetNamespaces {
		applyToClusters(targetClusters, ctrl.clusters, func(c *cluster.Cluster) {
			ctrl.mu.Lock()
			defer ctrl.mu.Unlock()

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
	applyToClusters(targetClusters, ctrl.clusters, func(c *cluster.Cluster) {
		ctrl.mu.Lock()
		defer ctrl.mu.Unlock()

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
		applyToClusters(targetClusters, ctrl.clusters, func(c *cluster.Cluster) {
			ctrl.mu.Lock()
			defer ctrl.mu.Unlock()

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
	applyToClusters(targetClusters, ctrl.clusters, func(c *cluster.Cluster) {
		ctrl.mu.Lock()
		defer ctrl.mu.Unlock()

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

func (ctrl *CAPIRbacWatcher) ProcessExistingRBACObjects() {

	// Process existing CAPIRoles
	existingCAPIRoles := ctrl.CAPIRoleInformer.GetStore().List()
	for _, obj := range existingCAPIRoles {
		role, err := ConvertUnstructured[CAPIRole](obj)
		if err != nil {
			klog.Errorf("Failed to convert CAPIRole: %v", err)
			continue
		}
		ctrl.ProcessCAPIRole(role)
	}

	// Process existing CAPIRoleBindings
	existingCAPIRoleBindings := ctrl.CAPIRoleBindingInformer.GetStore().List()
	for _, obj := range existingCAPIRoleBindings {
		roleBinding, err := ConvertUnstructured[CAPIRoleBinding](obj)
		if err != nil {
			klog.Errorf("Failed to convert CAPIRoleBinding: %v", err)
			continue
		}
		ctrl.ProcessCAPIRoleBinding(roleBinding)
	}

	// Process existing CAPIClusterRoles
	existingCAPIClusterRoles := ctrl.CAPIClusterRoleInformer.GetStore().List()
	for _, obj := range existingCAPIClusterRoles {
		clusterRole, err := ConvertUnstructured[CAPIClusterRole](obj)
		if err != nil {
			klog.Errorf("Failed to convert CAPIClusterRole: %v", err)
			continue
		}
		ctrl.ProcessCAPIClusterRole(clusterRole)
	}

	// Process existing CAPIClusterRoleBindings
	existingCAPIClusterRoleBindings := ctrl.CAPIClusterRoleBindingInformer.GetStore().List()
	for _, obj := range existingCAPIClusterRoleBindings {
		clusterRoleBinding, err := ConvertUnstructured[CAPIClusterRoleBinding](obj)
		if err != nil {
			klog.Errorf("Failed to convert CAPIClusterRoleBinding: %v", err)
			continue
		}
		ctrl.ProcessCAPIClusterRoleBinding(clusterRoleBinding)
	}

	// Rebuild authorizers for all clusters
	ctrl.RebuildAllAuthorizers()
	ctrl.initialProcessingComplete = true
}

// rebuildAllAuthorizers updates RBAC authorizers for all clusters.
func (ctrl *CAPIRbacWatcher) RebuildAllAuthorizers() {
	for _, c := range ctrl.clusters {
		ctrl.onRBACUpdate(c.RBACConfig, c.Name)

	}
}

// Helper functions to prevent duplication
func (ctrl *CAPIRbacWatcher) addOrUpdateRole(cluster *cluster.Cluster, role *v1.Role) {
	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()

	for i, existingRole := range cluster.RBACConfig.Roles {
		if existingRole.Name == role.Name && existingRole.Namespace == role.Namespace {
			cluster.RBACConfig.Roles[i] = role
			return
		}
	}
	cluster.RBACConfig.Roles = append(cluster.RBACConfig.Roles, role)
}

func (ctrl *CAPIRbacWatcher) addOrUpdateClusterRole(cluster *cluster.Cluster, clusterRole *v1.ClusterRole) {
	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()

	for i, existingCR := range cluster.RBACConfig.ClusterRoles {
		if existingCR.Name == clusterRole.Name {
			cluster.RBACConfig.ClusterRoles[i] = clusterRole
			return
		}
	}
	cluster.RBACConfig.ClusterRoles = append(cluster.RBACConfig.ClusterRoles, clusterRole)
}

func (ctrl *CAPIRbacWatcher) addOrUpdateRoleBinding(cluster *cluster.Cluster, roleBinding *v1.RoleBinding) {
	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()

	for i, existingRB := range cluster.RBACConfig.RoleBindings {
		if existingRB.Name == roleBinding.Name && existingRB.Namespace == roleBinding.Namespace {
			cluster.RBACConfig.RoleBindings[i] = roleBinding
			return
		}
	}
	cluster.RBACConfig.RoleBindings = append(cluster.RBACConfig.RoleBindings, roleBinding)
}

func (ctrl *CAPIRbacWatcher) addOrUpdateClusterRoleBinding(cluster *cluster.Cluster, clusterRoleBinding *v1.ClusterRoleBinding) {
	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()

	for i, existingCRB := range cluster.RBACConfig.ClusterRoleBindings {
		if existingCRB.Name == clusterRoleBinding.Name {
			cluster.RBACConfig.ClusterRoleBindings[i] = clusterRoleBinding
			return
		}
	}
	cluster.RBACConfig.ClusterRoleBindings = append(cluster.RBACConfig.ClusterRoleBindings, clusterRoleBinding)
}

// Re-evaluate RoleBindings when a new ClusterRole is added
func (ctrl *CAPIRbacWatcher) reevaluateRoleBindingsForClusterRole(cluster *cluster.Cluster, clusterRoleName string) {
	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()

	for i, rb := range cluster.RBACConfig.RoleBindings {
		// Check if this RoleBinding references the new ClusterRole but has wrong Kind
		if rb.RoleRef.Name == clusterRoleName && rb.RoleRef.Kind == "Role" {
			// Only update if there's no Role with the same name in the same namespace
			for _, role := range cluster.RBACConfig.Roles {
				if role.Name == clusterRoleName && role.Namespace == rb.Namespace {
					return
				}
			}

			// Only update to ClusterRole if no matching Role exists
			managedBy := rb.Annotations[fmt.Sprintf("%s/managed-by", constants.Group)]
			if managedBy != "" {
				// Update the RoleRef to point to ClusterRole
				cluster.RBACConfig.RoleBindings[i].RoleRef.Kind = "ClusterRole"
				cluster.RBACConfig.RoleBindings[i].RoleRef.APIGroup = v1.GroupName
				klog.V(5).Infof("Updated RoleBinding %s/%s to reference ClusterRole %s",
					rb.Namespace, rb.Name, clusterRoleName)
			}

		}
	}
}
