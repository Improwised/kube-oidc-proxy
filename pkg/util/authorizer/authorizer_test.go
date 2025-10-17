package authorizer

import (
	"testing"

	"github.com/Improwised/kube-oidc-proxy/pkg/util"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/authentication/user"
)

func TestNewRBACAuthorizer(t *testing.T) {
	auth := NewRBACAuthorizer()
	assert.NotNil(t, auth, "NewRBACAuthorizer should not return nil")

	rbacAuth, ok := auth.(*RBACAuthorizer)
	assert.True(t, ok, "auth should be of type *RBACAuthorizer")
	assert.NotNil(t, rbacAuth.trie, "Authorizer trie should not be nil")
}

func TestUpdateAndCheckPermissions(t *testing.T) {
	auth := NewRBACAuthorizer()
	clusterName := "test-cluster"

	rbacConfig := &util.RBAC{
		Roles: []*v1.Role{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-reader", Namespace: "ns1"},
				Rules: []v1.PolicyRule{{
					APIGroups: []string{"core"},
					Resources: []string{"pods"},
					Verbs:     []string{"get", "list"},
				}},
			},
		},
		ClusterRoles: []*v1.ClusterRole{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "node-reader"},
				Rules: []v1.PolicyRule{{
					APIGroups: []string{"core"},
					Resources: []string{"nodes"},
					Verbs:     []string{"get"},
				}},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "metrics-viewer"},
				Rules: []v1.PolicyRule{{
					NonResourceURLs: []string{"/metrics"},
					Verbs:           []string{"get"},
				}},
			},
		},
		RoleBindings: []*v1.RoleBinding{
			{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns1"},
				Subjects:   []v1.Subject{{Kind: "User", Name: "test-user"}},
				RoleRef:    v1.RoleRef{Kind: "Role", Name: "pod-reader"},
			},
		},
		ClusterRoleBindings: []*v1.ClusterRoleBinding{
			{
				Subjects: []v1.Subject{{Kind: "Group", Name: "test-group"}},
				RoleRef:  v1.RoleRef{Kind: "ClusterRole", Name: "node-reader"},
			},
			{
				Subjects: []v1.Subject{{Kind: "User", Name: "metrics-user"}},
				RoleRef:  v1.RoleRef{Kind: "ClusterRole", Name: "metrics-viewer"},
			},
		},
	}

	auth.UpdatePermissionTrie(rbacConfig, clusterName)

	testCases := []struct {
		name       string
		attributes Attributes
		expected   bool
	}{
		{
			name: "User with direct RoleBinding permission",
			attributes: Attributes{
				User:              &user.DefaultInfo{Name: "test-user"},
				Cluster:           clusterName,
				IsResourceRequest: true,
				Namespace:         "ns1",
				APIGroup:          "core",
				Resource:          "pods",
				Verb:              "get",
			},
			expected: true,
		},
		{
			name: "User with direct RoleBinding, wrong verb",
			attributes: Attributes{
				User:              &user.DefaultInfo{Name: "test-user"},
				Cluster:           clusterName,
				IsResourceRequest: true,
				Namespace:         "ns1",
				APIGroup:          "core",
				Resource:          "pods",
				Verb:              "delete",
			},
			expected: false,
		},
		{
			name: "User in group with ClusterRoleBinding permission",
			attributes: Attributes{
				User:              &user.DefaultInfo{Name: "some-user", Groups: []string{"test-group"}},
				Cluster:           clusterName,
				IsResourceRequest: true,
				APIGroup:          "core",
				Resource:          "nodes",
				Verb:              "get",
			},
			expected: true,
		},
		{
			name: "User in group, wrong resource",
			attributes: Attributes{
				User:              &user.DefaultInfo{Name: "some-user", Groups: []string{"test-group"}},
				Cluster:           clusterName,
				IsResourceRequest: true,
				APIGroup:          "core",
				Resource:          "services",
				Verb:              "get",
			},
			expected: false,
		},
		{
			name: "User with NonResourceURL permission",
			attributes: Attributes{
				User:              &user.DefaultInfo{Name: "metrics-user"},
				Cluster:           clusterName,
				IsResourceRequest: false,
				Path:              "/metrics",
				Verb:              "get",
			},
			expected: true,
		},
		{
			name: "User with NonResourceURL, wrong path",
			attributes: Attributes{
				User:              &user.DefaultInfo{Name: "metrics-user"},
				Cluster:           clusterName,
				IsResourceRequest: false,
				Path:              "/logs",
				Verb:              "get",
			},
			expected: false,
		},
		{
			name: "User with no permissions",
			attributes: Attributes{
				User:              &user.DefaultInfo{Name: "unauthorized-user"},
				Cluster:           clusterName,
				IsResourceRequest: true,
				Namespace:         "ns1",
				APIGroup:          "core",
				Resource:          "pods",
				Verb:              "get",
			},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := auth.CheckPermission(tc.attributes)
			assert.Equal(t, tc.expected, got)
		})
	}
}

func TestRemoveClusterPermissions(t *testing.T) {
	auth := NewRBACAuthorizer()
	clusterA := "cluster-a"
	clusterB := "cluster-b"
	user := &user.DefaultInfo{Name: "test-user"}

	rbacConfigA := &util.RBAC{
		ClusterRoles: []*v1.ClusterRole{{
			ObjectMeta: metav1.ObjectMeta{Name: "reader"},
			Rules:      []v1.PolicyRule{{Resources: []string{"pods"}, Verbs: []string{"get"}}},
		}},
		ClusterRoleBindings: []*v1.ClusterRoleBinding{{
			Subjects: []v1.Subject{{Kind: "User", Name: user.GetName()}},
			RoleRef:  v1.RoleRef{Kind: "ClusterRole", Name: "reader"},
		}},
	}

	auth.UpdatePermissionTrie(rbacConfigA, clusterA)
	auth.UpdatePermissionTrie(rbacConfigA, clusterB)

	attrs := Attributes{User: user, IsResourceRequest: true, Resource: "pods", Verb: "get"}

	attrs.Cluster = clusterA
	assert.True(t, auth.CheckPermission(attrs), "Permission should exist on cluster-a before removal")

	attrs.Cluster = clusterB
	assert.True(t, auth.CheckPermission(attrs), "Permission should exist on cluster-b")

	auth.RemoveClusterPermissions(clusterA)

	attrs.Cluster = clusterA
	assert.False(t, auth.CheckPermission(attrs), "Permission should be gone from cluster-a after removal")

	attrs.Cluster = clusterB
	assert.True(t, auth.CheckPermission(attrs), "Permission should still exist on cluster-b")
}

func TestResolveAggregatedRoles(t *testing.T) {
	roleA := &v1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "role-a", Labels: map[string]string{"tier": "1"}},
		Rules:      []v1.PolicyRule{{Verbs: []string{"get"}}},
	}
	roleB := &v1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "role-b", Labels: map[string]string{"tier": "2"}},
		Rules:      []v1.PolicyRule{{Verbs: []string{"list"}}},
		AggregationRule: &v1.AggregationRule{
			ClusterRoleSelectors: []metav1.LabelSelector{
				{MatchLabels: map[string]string{"tier": "1"}},
			},
		},
	}
	roleCWithCycle := &v1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "role-c", Labels: map[string]string{"cycle": "true"}},
		AggregationRule: &v1.AggregationRule{
			ClusterRoleSelectors: []metav1.LabelSelector{
				{MatchLabels: map[string]string{"cycle": "true"}},
			},
		},
	}

	roles := []*v1.ClusterRole{roleA, roleB, roleCWithCycle}
	resolvedRoles := resolveAggregatedRoles(roles)

	var resolvedB *v1.ClusterRole
	for _, r := range resolvedRoles {
		if r.Name == "role-b" {
			resolvedB = r
		}
	}

	assert.NotNil(t, resolvedB, "Resolved role B should exist")
	// role-b should have its own "list" rule and role-a's "get" rule.
	assert.Len(t, resolvedB.Rules, 2, "Resolved role B should have 2 rules")

	hasGet := false
	hasList := false
	for _, rule := range resolvedB.Rules {
		if rule.Verbs[0] == "get" {
			hasGet = true
		}
		if rule.Verbs[0] == "list" {
			hasList = true
		}
	}
	assert.True(t, hasGet, "Resolved role B should have 'get' verb")
	assert.True(t, hasList, "Resolved role B should have 'list' verb")

	// Test for cycle detection, just ensure it terminates without error.
	// The function prints a warning, which we can't easily check here,
	// but termination is the most important part.
	assert.NotPanics(t, func() {
		resolveAggregatedRoles([]*v1.ClusterRole{roleCWithCycle})
	})
}
