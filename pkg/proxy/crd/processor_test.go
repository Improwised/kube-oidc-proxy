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
	tests := []struct {
		name                string
		capiRoleBinding     *CAPIRoleBinding
		namespace           string
		clusters            []*cluster.Cluster
		expectedBindings    int
		expectedRoleRefKind string
		description         string
	}{
		{
			name: "Create RoleBinding referencing Role",
			capiRoleBinding: &CAPIRoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-binding",
				},
				Spec: CAPIRoleBindingSpec{
					CommonBindingSpec: CommonBindingSpec{
						RoleRef: []string{"test-role"},
						Subjects: []Subject{
							{
								User: "test-user",
							},
						},
					},
				},
			},
			namespace: "test-ns",
			clusters: []*cluster.Cluster{
				{
					Name: "cluster1",
					RBACConfig: &util.RBAC{
						Roles: []*v1.Role{
							{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "test-role",
									Namespace: "test-ns",
								},
							},
						},
					},
				}},
			expectedBindings:    1,
			expectedRoleRefKind: "Role",
			description:         "Should create RoleBinding referencing Role when Role exists",
		},
		{
			name: "Create RoleBinding referencing ClusterRole",
			capiRoleBinding: &CAPIRoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-binding",
				},
				Spec: CAPIRoleBindingSpec{
					CommonBindingSpec: CommonBindingSpec{
						RoleRef: []string{"test-cluster-role"},
						Subjects: []Subject{
							{User: "test-user"},
						},
					},
				},
			},
			namespace: "test-ns",
			clusters: []*cluster.Cluster{{
				Name: "cluster-1",
				RBACConfig: &util.RBAC{
					ClusterRoles: []*v1.ClusterRole{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-cluster-role",
							},
						},
					},
				},
			}},
			expectedBindings:    1,
			expectedRoleRefKind: "ClusterRole",
			description:         "Should create RoleBinding referencing ClusterRole when only ClusterRole exists",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := &CAPIRbacWatcher{
				clusters: tt.clusters,
			}

			roleBindings := createRoleBinding(tt.capiRoleBinding, tt.namespace, ctrl)

			require.Equal(t, tt.expectedBindings, len(roleBindings), tt.description)
			assert.Equal(t, tt.expectedRoleRefKind, roleBindings[0].RoleRef.Kind, tt.description)
			assert.Equal(t, v1.GroupName, roleBindings[0].RoleRef.APIGroup, "APIGroup should be set correctly")
			assert.Equal(t, tt.namespace, roleBindings[0].Namespace, "Namespace should be set correctly")

			// Verify managed-by annotation
			expectedAnnotation := constants.Group + "/managed-by"
			assert.Equal(t, tt.capiRoleBinding.Name, roleBindings[0].Annotations[expectedAnnotation], "Should have correct managed-by annotation")
		})
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

func TestDetermineRoleRefKindAndAPIGroup(t *testing.T) {
	tests := []struct {
		name             string
		roleRef          string
		namespace        string
		clusters         []*cluster.Cluster
		expectedKind     string
		expectedAPIGroup string
		description      string
	}{
		{
			name:      "Role exists in namespace - should prefer Role over ClusterRole",
			roleRef:   "test-role",
			namespace: "test-ns",
			clusters: []*cluster.Cluster{{

				Name: "cluster1",
				RBACConfig: &util.RBAC{
					Roles: []*v1.Role{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test-role",
								Namespace: "test-ns",
							},
						},
					},
					ClusterRoles: []*v1.ClusterRole{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-role",
							},
						},
					},
				}},
			},
			expectedKind:     "Role",
			expectedAPIGroup: v1.GroupName,
			description:      "When both Role and ClusterRole exist with same name, Role should take precedence",
		},
		{
			name:      "Only ClusterRole exists - should use ClusterRole",
			roleRef:   "test-cluster-role",
			namespace: "test-ns",
			clusters: []*cluster.Cluster{{
				Name: "cluster1",
				RBACConfig: &util.RBAC{
					Roles: []*v1.Role{},
					ClusterRoles: []*v1.ClusterRole{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-cluster-role",
							},
						},
					},
				},
			}},
			expectedKind:     "ClusterRole",
			expectedAPIGroup: v1.GroupName,
			description:      "When only ClusterRole exists, should use ClusterRole",
		},
		{
			name:      "Role exists in different namespace - should use ClusterRole",
			roleRef:   "test-role",
			namespace: "test-ns",
			clusters: []*cluster.Cluster{{
				Name: "cluster1",
				RBACConfig: &util.RBAC{
					Roles: []*v1.Role{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test-role",
								Namespace: "different-ns",
							},
						},
					},
					ClusterRoles: []*v1.ClusterRole{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-role",
							},
						},
					}},
			},
			},
			expectedKind:     "ClusterRole",
			expectedAPIGroup: v1.GroupName,
			description:      "When Role exists in different namespace, should use ClusterRole",
		},
		{
			name:      "Neither Role nor ClusterRole exists - should default to Role",
			roleRef:   "non-existent-role",
			namespace: "test-ns",
			clusters: []*cluster.Cluster{{
				Name: "cluster1",
				RBACConfig: &util.RBAC{
					Roles:        []*v1.Role{},
					ClusterRoles: []*v1.ClusterRole{},
				}},
			},
			expectedKind:     "Role",
			expectedAPIGroup: v1.GroupName,
			description:      "When neither exists, should default to Role for forward compatibility",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := &CAPIRbacWatcher{
				clusters: tt.clusters,
			}

			kind := determineRoleRefKindAndAPIGroup(tt.roleRef, ctrl, tt.namespace)

			assert.Equal(t, tt.expectedKind, kind, tt.description)
			assert.Equal(t, tt.expectedAPIGroup, v1.GroupName, tt.description)
		})
	}
}

func TestAddOrUpdateRole(t *testing.T) {
	tests := []struct {
		name            string
		existingRoles   []*v1.Role
		newRole         *v1.Role
		expectedCount   int
		expectedUpdated bool
		description     string
	}{
		{
			name:          "Add new role to empty list",
			existingRoles: []*v1.Role{},
			newRole: &v1.Role{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "new-role",
					Namespace: "test-ns",
				},
			},
			expectedCount:   1,
			expectedUpdated: false,
			description:     "Should add new role when list is empty",
		},
		{
			name: "Update existing role",
			existingRoles: []*v1.Role{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-role",
						Namespace: "test-ns",
						Labels:    map[string]string{"version": "v1"},
					},
				},
			},
			newRole: &v1.Role{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "existing-role",
					Namespace: "test-ns",
					Labels:    map[string]string{"version": "v2"},
				},
			},
			expectedCount:   1,
			expectedUpdated: true,
			description:     "Should update existing role in place",
		},
		{
			name: "Add role with same name but different namespace",
			existingRoles: []*v1.Role{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "same-name",
						Namespace: "ns1",
					},
				},
			},
			newRole: &v1.Role{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "same-name",
					Namespace: "ns2",
				},
			},
			expectedCount:   2,
			expectedUpdated: false,
			description:     "Should add role with same name but different namespace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &cluster.Cluster{
				RBACConfig: &util.RBAC{
					Roles: tt.existingRoles,
				},
			}
			ctrl := &CAPIRbacWatcher{}

			ctrl.addOrUpdateRole(cluster, tt.newRole)

			assert.Equal(t, tt.expectedCount, len(cluster.RBACConfig.Roles), tt.description)

			if tt.expectedUpdated {
				found := false
				for _, role := range cluster.RBACConfig.Roles {
					if role.Name == tt.newRole.Name && role.Namespace == tt.newRole.Namespace {
						assert.Equal(t, tt.newRole.Labels, role.Labels, "Role should be updated with new labels")
						found = true
						break
					}
				}
				assert.True(t, found, "Updated role should be found")
			}
		})
	}
}

func TestAddOrUpdateClusterRole(t *testing.T) {
	tests := []struct {
		name                 string
		existingClusterRoles []*v1.ClusterRole
		newClusterRole       *v1.ClusterRole
		expectedCount        int
		expectedUpdated      bool
		description          string
	}{
		{
			name:                 "Add new cluster role",
			existingClusterRoles: []*v1.ClusterRole{},
			newClusterRole: &v1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{
					Name: "new-cluster-role",
				},
			},
			expectedCount:   1,
			expectedUpdated: false,
			description:     "Should add new cluster role",
		},
		{
			name: "Update existing cluster role",
			existingClusterRoles: []*v1.ClusterRole{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "existing-cluster-role",
						Labels: map[string]string{"version": "v1"},
					},
				},
			},
			newClusterRole: &v1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "existing-cluster-role",
					Labels: map[string]string{"version": "v2"},
				},
			},
			expectedCount:   1,
			expectedUpdated: true,
			description:     "Should update existing cluster role in place",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &cluster.Cluster{
				RBACConfig: &util.RBAC{
					ClusterRoles: tt.existingClusterRoles,
				},
			}
			ctrl := &CAPIRbacWatcher{}

			ctrl.addOrUpdateClusterRole(cluster, tt.newClusterRole)

			assert.Equal(t, tt.expectedCount, len(cluster.RBACConfig.ClusterRoles), tt.description)

			if tt.expectedUpdated {
				found := false
				for _, clusterRole := range cluster.RBACConfig.ClusterRoles {
					if clusterRole.Name == tt.newClusterRole.Name {
						assert.Equal(t, tt.newClusterRole.Labels, clusterRole.Labels, "ClusterRole should be updated")
						found = true
						break
					}
				}
				assert.True(t, found, "Updated cluster role should be found")
			}
		})
	}
}

func TestReevaluateRoleBindingsForClusterRole(t *testing.T) {
	tests := []struct {
		name                 string
		existingRoleBindings []*v1.RoleBinding
		existingRoles        []*v1.Role
		clusterRoleName      string
		expectedUpdates      int
		description          string
	}{
		{
			name: "Update RoleBinding when no conflicting Role exists",
			existingRoleBindings: []*v1.RoleBinding{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-binding",
						Namespace: "test-ns",
						Annotations: map[string]string{
							constants.Group + "/managed-by": "test-capi-binding",
						},
					},
					RoleRef: v1.RoleRef{
						Kind:     "Role",
						Name:     "test-role",
						APIGroup: v1.GroupName,
					},
				},
			},
			existingRoles:   []*v1.Role{},
			clusterRoleName: "test-role",
			expectedUpdates: 1,
			description:     "Should update RoleBinding to reference ClusterRole when no conflicting Role exists",
		},
		{
			name: "Do not update RoleBinding when conflicting Role exists",
			existingRoleBindings: []*v1.RoleBinding{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-binding",
						Namespace: "test-ns",
						Annotations: map[string]string{
							constants.Group + "/managed-by": "test-capi-binding",
						},
					},
					RoleRef: v1.RoleRef{
						Kind:     "Role",
						Name:     "test-role",
						APIGroup: v1.GroupName,
					},
				},
			},
			existingRoles: []*v1.Role{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-role",
						Namespace: "test-ns",
					},
				},
			},
			clusterRoleName: "test-role",
			expectedUpdates: 0,
			description:     "Should not update RoleBinding when conflicting Role exists in same namespace",
		},
		{
			name: "Do not update unmanaged RoleBinding",
			existingRoleBindings: []*v1.RoleBinding{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "unmanaged-binding",
						Namespace: "test-ns",
					},
					RoleRef: v1.RoleRef{
						Kind:     "Role",
						Name:     "test-role",
						APIGroup: v1.GroupName,
					},
				},
			},
			existingRoles:   []*v1.Role{},
			clusterRoleName: "test-role",
			expectedUpdates: 0,
			description:     "Should not update RoleBinding that is not managed by CAPI controller",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &cluster.Cluster{
				RBACConfig: &util.RBAC{
					RoleBindings: tt.existingRoleBindings,
					Roles:        tt.existingRoles,
				},
			}
			ctrl := &CAPIRbacWatcher{}

			ctrl.reevaluateRoleBindingsForClusterRole(cluster, tt.clusterRoleName)

			updatedCount := 0
			for _, rb := range cluster.RBACConfig.RoleBindings {
				if rb.RoleRef.Name == tt.clusterRoleName && rb.RoleRef.Kind == "ClusterRole" {
					updatedCount++
				}
			}

			assert.Equal(t, tt.expectedUpdates, updatedCount, tt.description)
		})
	}
}

func TestIntegrationScenario_RaceCondition(t *testing.T) {
	// Test the race condition scenario where RoleBinding is processed before ClusterRole
	ctrl := &CAPIRbacWatcher{
		clusters: []*cluster.Cluster{{
			Name: "cluster1",
			RBACConfig: &util.RBAC{
				Roles:               []*v1.Role{},
				ClusterRoles:        []*v1.ClusterRole{},
				RoleBindings:        []*v1.RoleBinding{},
				ClusterRoleBindings: []*v1.ClusterRoleBinding{},
			}},
		},
	}

	// Step 1: Process CAPIRoleBinding before ClusterRole exists
	capiRoleBinding := &CAPIRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-binding",
		},
		Spec: CAPIRoleBindingSpec{
			CommonBindingSpec: CommonBindingSpec{
				TargetClusters: []string{"cluster1"},
				RoleRef:        []string{"future-cluster-role"},
				Subjects: []Subject{
					{User: "test-user"},
				},
			},
			TargetNamespaces: []string{"test-ns"},
		},
	}

	ctrl.ProcessCAPIRoleBinding(capiRoleBinding)

	// Verify RoleBinding was created with Kind="Role" (incorrect due to race condition)
	cluster := ctrl.clusters[0]
	require.Equal(t, 1, len(cluster.RBACConfig.RoleBindings), "RoleBinding should be created")
	assert.Equal(t, "Role", cluster.RBACConfig.RoleBindings[0].RoleRef.Kind, "Should initially default to Role")

	// Step 2: Process CAPIClusterRole
	capiClusterRole := &CAPIClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "future-cluster-role",
		},
		Spec: CAPIClusterRoleSpec{
			CommonRoleSpec: CommonRoleSpec{
				TargetClusters: []string{"cluster1"},
			},
		},
	}

	ctrl.ProcessCAPIClusterRole(capiClusterRole)

	// Verify ClusterRole was created and RoleBinding was updated
	require.Equal(t, 1, len(cluster.RBACConfig.ClusterRoles), "ClusterRole should be created")
	require.Equal(t, 1, len(cluster.RBACConfig.RoleBindings), "RoleBinding should still exist")
	assert.Equal(t, "ClusterRole", cluster.RBACConfig.RoleBindings[0].RoleRef.Kind, "RoleBinding should now reference ClusterRole")
	assert.Equal(t, v1.GroupName, cluster.RBACConfig.RoleBindings[0].RoleRef.APIGroup, "APIGroup should be correct")
}
