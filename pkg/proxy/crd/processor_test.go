package crd

import (
	"fmt"
	"testing"

	"github.com/Improwised/kube-oidc-proxy/constants"
	"github.com/Improwised/kube-oidc-proxy/pkg/cluster"
	"github.com/Improwised/kube-oidc-proxy/pkg/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestConvertUnstructured_Success(t *testing.T) {
	// Setup test CAPIRole
	capiRole := &CAPIRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-role",
		},
		Spec: CAPIRoleSpec{
			CommonRoleSpec: CommonRoleSpec{
				Name:  "test-role",
				Rules: []v1.PolicyRule{{Verbs: []string{"get"}}},
			},
			TargetNamespaces: []string{"default"},
		},
	}

	// Convert to unstructured
	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(capiRole)
	require.NoError(t, err)
	u := &unstructured.Unstructured{Object: unstructuredObj}

	// Test conversion
	result, err := ConvertUnstructured[CAPIRole](u)
	require.NoError(t, err)
	assert.Equal(t, capiRole.Name, result.Name)
	assert.Len(t, result.Spec.Rules, 1)
}

func TestConvertUnstructured_Failure(t *testing.T) {
	invalidObj := "not-an-unstructured-object"
	_, err := ConvertUnstructured[CAPIRole](invalidObj)
	assert.ErrorContains(t, err, "expected unstructured object")
}

func TestProcessCAPIRole(t *testing.T) {
	// Setup test cluster
	testCluster := &cluster.Cluster{
		Name: "cluster1",
		RBACConfig: &util.RBAC{
			Roles: make([]*v1.Role, 0),
		},
	}
	watcher := &CAPIRbacWatcher{clusters: []*cluster.Cluster{testCluster}}

	// Test CAPIRole
	capiRole := &CAPIRole{
		Spec: CAPIRoleSpec{
			CommonRoleSpec: CommonRoleSpec{
				TargetClusters: []string{"cluster1"},
			},
			TargetNamespaces: []string{"default"},
		},
	}
	capiRole.Name = "test-role"
	capiRole.Spec.Rules = []v1.PolicyRule{{Verbs: []string{"get"}}}

	// Process the role
	watcher.ProcessCAPIRole(capiRole)

	// Verify results
	assert.Len(t, testCluster.RBACConfig.Roles, 1)
	role := testCluster.RBACConfig.Roles[0]
	assert.Equal(t, "test-role", role.Name)
	assert.Equal(t, "default", role.Namespace)
	assert.Len(t, role.Rules, 1)
}

func TestDeleteCAPIRole(t *testing.T) {
	// Setup test cluster with existing role
	testRole := &v1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-role",
			Namespace: "default",
			Annotations: map[string]string{
				fmt.Sprintf("%s/managed-by", constants.Group): "test-role",
			},
		},
	}
	testCluster := &cluster.Cluster{
		Name: "cluster1",
		RBACConfig: &util.RBAC{
			Roles: []*v1.Role{testRole},
		},
	}
	watcher := &CAPIRbacWatcher{clusters: []*cluster.Cluster{testCluster}}

	// Create CAPIRole for deletion
	capiRole := &CAPIRole{
		Spec: CAPIRoleSpec{
			CommonRoleSpec: CommonRoleSpec{
				TargetClusters: []string{"cluster1"},
			},
			TargetNamespaces: []string{"default"},
		},
	}
	capiRole.Name = "test-role"

	// Delete the role
	watcher.DeleteCAPIRole(capiRole)

	// Verify deletion
	assert.Empty(t, testCluster.RBACConfig.Roles)
}

func TestDetermineTargetClusters(t *testing.T) {
	testClusters := []*cluster.Cluster{
		{Name: "cluster1"},
		{Name: "cluster2"},
	}

	t.Run("specified clusters", func(t *testing.T) {
		targets := determineTargetClusters([]string{"cluster1"}, testClusters)
		assert.Equal(t, []string{"cluster1"}, targets)
	})

	t.Run("empty clusters", func(t *testing.T) {
		targets := determineTargetClusters(nil, testClusters)
		assert.Empty(t, targets)
	})

	t.Run("wildcard expansion", func(t *testing.T) {
		targets := determineTargetClusters([]string{"*"}, testClusters)
		assert.ElementsMatch(t, []string{"cluster1", "cluster2"}, targets)
	})

	t.Run("wildcard with duplicates", func(t *testing.T) {
		targets := determineTargetClusters([]string{"*", "cluster1"}, testClusters)
		assert.ElementsMatch(t, []string{"*", "cluster1"}, targets)
	})
}

func TestCreateClusterRoleBinding(t *testing.T) {
	capiBinding := &CAPIClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "test-binding"},
		Spec: CAPIClusterRoleBindingSpec{
			CommonBindingSpec: CommonBindingSpec{
				RoleRef:  []string{"role1", "role2"},
				Subjects: []Subject{{User: "test-user"}},
			},
		},
	}

	bindings := createClusterRoleBinding(capiBinding)
	require.Len(t, bindings, 2)

	for i, binding := range bindings {
		assert.Equal(t, fmt.Sprintf("test-binding-role%d", i+1), binding.Name)
		assert.Equal(t, "test-user", binding.Subjects[0].Name)
		assert.Equal(t, "User", binding.Subjects[0].Kind)
	}
}

func TestRebuildAllAuthorizers(t *testing.T) {
	testCluster := &cluster.Cluster{
		Name: "cluster1",
		RBACConfig: &util.RBAC{
			Roles: []*v1.Role{{
				ObjectMeta: metav1.ObjectMeta{Name: "test-role"},
				Rules:      []v1.PolicyRule{{Verbs: []string{"get"}}},
			}},
		},
	}
	watcher := &CAPIRbacWatcher{clusters: []*cluster.Cluster{testCluster}}

	watcher.RebuildAllAuthorizers()

	// Verify authorizer is created
	assert.NotNil(t, testCluster.Authorizer)
}

func TestApplyToClusters(t *testing.T) {
	testClusters := []*cluster.Cluster{
		{Name: "cluster1", RBACConfig: &util.RBAC{}},
		{Name: "cluster2", RBACConfig: &util.RBAC{}},
	}

	counter := 0
	applyToClusters([]string{"cluster1"}, testClusters, func(c *cluster.Cluster) {
		counter++
	})

	assert.Equal(t, 1, counter)
}

func TestGetAllClusterNames(t *testing.T) {
	t.Run("empty clusters", func(t *testing.T) {
		clusters := []*cluster.Cluster{}
		assert.Empty(t, getAllClusterNames(clusters))
	})

	t.Run("multiple clusters", func(t *testing.T) {
		clusters := []*cluster.Cluster{
			{Name: "cluster1"},
			{Name: "cluster2"},
		}
		assert.ElementsMatch(t, []string{"cluster1", "cluster2"}, getAllClusterNames(clusters))
	})
}

func TestCreateRole(t *testing.T) {
	capiRole := &CAPIRole{
		ObjectMeta: metav1.ObjectMeta{Name: "test-role"},
		Spec: CAPIRoleSpec{
			CommonRoleSpec: CommonRoleSpec{
				Rules: []v1.PolicyRule{{Verbs: []string{"get"}}},
			},
		},
	}

	role := createRole(capiRole, "test-ns")
	assert.Equal(t, "test-role", role.Name)
	assert.Equal(t, "test-ns", role.Namespace)
	assert.Equal(t, fmt.Sprintf("%s/managed-by", constants.Group), getAnnotationKey(role.Annotations))
	assert.Equal(t, "test-role", role.Annotations[fmt.Sprintf("%s/managed-by", constants.Group)])
	assert.Len(t, role.Rules, 1)
}

func TestCreateClusterRole(t *testing.T) {
	capiClusterRole := &CAPIClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "test-clusterrole"},
		Spec: CAPIClusterRoleSpec{
			CommonRoleSpec: CommonRoleSpec{
				Rules: []v1.PolicyRule{{Verbs: []string{"list"}}},
			},
		},
	}

	clusterRole := createClusterRole(capiClusterRole)
	assert.Equal(t, "test-clusterrole", clusterRole.Name)
	assert.Equal(t, fmt.Sprintf("%s/managed-by", constants.Group), getAnnotationKey(clusterRole.Annotations))
	assert.Len(t, clusterRole.Rules, 1)
}

func TestDetermineSubjectKind(t *testing.T) {
	tests := []struct {
		name     string
		subject  Subject
		expected string
	}{
		{"group", Subject{Group: "admins"}, "Group"},
		{"user", Subject{User: "john"}, "User"},
		{"serviceaccount", Subject{ServiceAccount: "system"}, "ServiceAccount"},
		{"empty", Subject{}, ""},
		{"multiple fields", Subject{Group: "admins", User: "john"}, "Group"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, determineSubjectKind(tt.subject))
		})
	}
}

func TestDetermineSubjectName(t *testing.T) {
	tests := []struct {
		name     string
		subject  Subject
		expected string
	}{
		{"group", Subject{Group: "admins"}, "admins"},
		{"user", Subject{User: "john"}, "john"},
		{"serviceaccount", Subject{ServiceAccount: "system"}, "system"},
		{"empty", Subject{}, ""},
		{"multiple fields", Subject{Group: "admins", User: "john"}, "admins"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, determineSubjectName(tt.subject))
		})
	}
}

func TestConvertSubjects(t *testing.T) {
	subjects := []Subject{
		{Group: "devs"},
		{User: "admin"},
		{ServiceAccount: "system"},
		{}, // Should be skipped
	}

	converted := convertSubjects(subjects)
	require.Len(t, converted, 4)

	// Valid subjects
	assert.Equal(t, "Group", converted[0].Kind)
	assert.Equal(t, "devs", converted[0].Name)
	assert.Equal(t, "User", converted[1].Kind)
	assert.Equal(t, "admin", converted[1].Name)
	assert.Equal(t, "ServiceAccount", converted[2].Kind)
	assert.Equal(t, "system", converted[2].Name)

	// Empty subject
	assert.Equal(t, "", converted[3].Kind)
	assert.Equal(t, "", converted[3].Name)
}

func TestCreateRoleBinding(t *testing.T) {
	capiBinding := &CAPIRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "test-rolebinding"},
		Spec: CAPIRoleBindingSpec{
			CommonBindingSpec: CommonBindingSpec{
				RoleRef:  []string{"role1", "role2"},
				Subjects: []Subject{{User: "test-user"}},
			},
		},
	}

	bindings := createRoleBinding(capiBinding, "test-ns")
	require.Len(t, bindings, 2)

	for i, binding := range bindings {
		assert.Equal(t, fmt.Sprintf("test-rolebinding-role%d", i+1), binding.Name)
		assert.Equal(t, "test-ns", binding.Namespace)
		assert.Equal(t, "test-user", binding.Subjects[0].Name)
		assert.Equal(t, "User", binding.Subjects[0].Kind)
	}
}

func TestProcessCAPIClusterRole(t *testing.T) {
	testCluster := &cluster.Cluster{
		Name: "cluster1",
		RBACConfig: &util.RBAC{
			ClusterRoles: make([]*v1.ClusterRole, 0),
		},
	}
	watcher := &CAPIRbacWatcher{clusters: []*cluster.Cluster{testCluster}}

	capiClusterRole := &CAPIClusterRole{
		Spec: CAPIClusterRoleSpec{
			CommonRoleSpec: CommonRoleSpec{
				TargetClusters: []string{"cluster1"},
			},
		},
	}
	capiClusterRole.Name = "test-clusterrole"
	capiClusterRole.Spec.Rules = []v1.PolicyRule{{Verbs: []string{"list"}}}

	watcher.ProcessCAPIClusterRole(capiClusterRole)

	assert.Len(t, testCluster.RBACConfig.ClusterRoles, 1)
	cr := testCluster.RBACConfig.ClusterRoles[0]
	assert.Equal(t, "test-clusterrole", cr.Name)
	assert.Len(t, cr.Rules, 1)
}

func TestDeleteCAPIClusterRole(t *testing.T) {
	testCluster := &cluster.Cluster{
		Name: "cluster1",
		RBACConfig: &util.RBAC{
			ClusterRoles: []*v1.ClusterRole{{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-clusterrole",
					Annotations: map[string]string{
						fmt.Sprintf("%s/managed-by", constants.Group): "test-clusterrole",
					},
				},
			}},
		},
	}
	watcher := &CAPIRbacWatcher{clusters: []*cluster.Cluster{testCluster}}

	capiClusterRole := &CAPIClusterRole{
		Spec: CAPIClusterRoleSpec{
			CommonRoleSpec: CommonRoleSpec{
				TargetClusters: []string{"cluster1"},
			},
		},
	}
	capiClusterRole.Name = "test-clusterrole"

	watcher.DeleteCAPIClusterRole(capiClusterRole)
	assert.Empty(t, testCluster.RBACConfig.ClusterRoles)
}

func TestProcessCAPIRoleBinding(t *testing.T) {
	testCluster := &cluster.Cluster{
		Name: "cluster1",
		RBACConfig: &util.RBAC{
			RoleBindings: make([]*v1.RoleBinding, 0),
		},
	}
	watcher := &CAPIRbacWatcher{clusters: []*cluster.Cluster{testCluster}}

	capiRoleBinding := &CAPIRoleBinding{
		Spec: CAPIRoleBindingSpec{
			CommonBindingSpec: CommonBindingSpec{
				TargetClusters: []string{"cluster1"},
				RoleRef:        []string{"role1"},
				Subjects:       []Subject{{User: "test-user"}},
			},
			TargetNamespaces: []string{"default"},
		},
	}
	capiRoleBinding.Name = "test-rolebinding"

	watcher.ProcessCAPIRoleBinding(capiRoleBinding)

	assert.Len(t, testCluster.RBACConfig.RoleBindings, 1)
	rb := testCluster.RBACConfig.RoleBindings[0]
	assert.Equal(t, "test-rolebinding-role1", rb.Name)
	assert.Equal(t, "default", rb.Namespace)
}

func TestDeleteCAPIRoleBinding(t *testing.T) {
	testRoleBinding := &v1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-rolebinding-role1",
			Namespace: "default",
			Annotations: map[string]string{
				fmt.Sprintf("%s/managed-by", constants.Group): "test-rolebinding",
			},
		},
	}
	testCluster := &cluster.Cluster{
		Name: "cluster1",
		RBACConfig: &util.RBAC{
			RoleBindings: []*v1.RoleBinding{testRoleBinding},
		},
	}
	watcher := &CAPIRbacWatcher{clusters: []*cluster.Cluster{testCluster}}

	capiRoleBinding := &CAPIRoleBinding{
		Spec: CAPIRoleBindingSpec{
			CommonBindingSpec: CommonBindingSpec{
				TargetClusters: []string{"cluster1"},
			},
			TargetNamespaces: []string{"default"},
		},
	}
	capiRoleBinding.Name = "test-rolebinding"

	watcher.DeleteCAPIRoleBinding(capiRoleBinding)
	assert.Empty(t, testCluster.RBACConfig.RoleBindings)
}

func TestProcessCAPIClusterRoleBinding(t *testing.T) {
	testCluster := &cluster.Cluster{
		Name: "cluster1",
		RBACConfig: &util.RBAC{
			ClusterRoleBindings: make([]*v1.ClusterRoleBinding, 0),
		},
	}
	watcher := &CAPIRbacWatcher{clusters: []*cluster.Cluster{testCluster}}

	capiCRB := &CAPIClusterRoleBinding{
		Spec: CAPIClusterRoleBindingSpec{
			CommonBindingSpec: CommonBindingSpec{
				TargetClusters: []string{"cluster1"},
				RoleRef:        []string{"cluster-role1"},
				Subjects:       []Subject{{Group: "admins"}},
			},
		},
	}
	capiCRB.Name = "test-clusterrolebinding"

	watcher.ProcessCAPIClusterRoleBinding(capiCRB)

	assert.Len(t, testCluster.RBACConfig.ClusterRoleBindings, 1)
	crb := testCluster.RBACConfig.ClusterRoleBindings[0]
	assert.Equal(t, "test-clusterrolebinding-cluster-role1", crb.Name)
	assert.Equal(t, "admins", crb.Subjects[0].Name)
}

func TestDeleteCAPIClusterRoleBinding(t *testing.T) {
	testCRB := &v1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-clusterrolebinding-cluster-role1",
			Annotations: map[string]string{
				fmt.Sprintf("%s/managed-by", constants.Group): "test-clusterrolebinding",
			},
		},
	}
	testCluster := &cluster.Cluster{
		Name: "cluster1",
		RBACConfig: &util.RBAC{
			ClusterRoleBindings: []*v1.ClusterRoleBinding{testCRB},
		},
	}
	watcher := &CAPIRbacWatcher{clusters: []*cluster.Cluster{testCluster}}

	capiCRB := &CAPIClusterRoleBinding{
		Spec: CAPIClusterRoleBindingSpec{
			CommonBindingSpec: CommonBindingSpec{
				TargetClusters: []string{"cluster1"},
			},
		},
	}
	capiCRB.Name = "test-clusterrolebinding"

	watcher.DeleteCAPIClusterRoleBinding(capiCRB)
	assert.Empty(t, testCluster.RBACConfig.ClusterRoleBindings)
}

func TestApplyToClusters_NonExistentCluster(t *testing.T) {
	testClusters := []*cluster.Cluster{
		{Name: "cluster1", RBACConfig: &util.RBAC{}},
		{Name: "cluster2", RBACConfig: &util.RBAC{}},
	}

	counter := 0
	applyToClusters([]string{"cluster3"}, testClusters, func(c *cluster.Cluster) {
		counter++
	})

	assert.Equal(t, 0, counter)
}

func TestConvertUnstructured_ConversionFailure(t *testing.T) {
	u := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"spec": map[string]interface{}{
				"rules": "invalid-type", // Should be []interface{}
			},
		},
	}
	u.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "kube-oidc.proxy",
		Version: "v1alpha1",
		Kind:    "CAPIRole",
	})

	_, err := ConvertUnstructured[CAPIRole](u)
	assert.ErrorContains(t, err, "conversion failed")
}

// Helper function to get annotation key
func getAnnotationKey(annotations map[string]string) string {
	for k := range annotations {
		return k
	}
	return ""
}
