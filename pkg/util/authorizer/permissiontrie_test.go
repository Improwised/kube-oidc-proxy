package authorizer

import (
	"testing"
)

func TestPermissionTrie_AddAndCheckResourcePermission(t *testing.T) {
	trie := NewPermissionTrie()
	subjectType := SubjectTypeUser
	subjectName := "test-user"
	cluster := "test-cluster"
	namespace := "test-ns"
	apiGroup := "apps"
	resource := "deployments"
	verb := "get"
	resourceName := "test-deployment"

	trie.AddResourcePermission(subjectType, subjectName, cluster, namespace, apiGroup, resource, verb, []string{resourceName})

	testCases := []struct {
		name         string
		subjectType  SubjectType
		subjectName  string
		cluster      string
		namespace    string
		apiGroup     string
		resource     string
		resourceName string
		verb         string
		expected     bool
	}{
		{"ExactMatch", subjectType, subjectName, cluster, namespace, apiGroup, resource, resourceName, verb, true},
		{"WrongVerb", subjectType, subjectName, cluster, namespace, apiGroup, resource, resourceName, "list", false},
		{"WrongResourceName", subjectType, subjectName, cluster, namespace, apiGroup, resource, "wrong-deployment", "get", false},
		{"WrongResource", subjectType, subjectName, cluster, namespace, apiGroup, "pods", resourceName, verb, false},
		{"WrongAPIGroup", subjectType, subjectName, cluster, namespace, "batch", resource, resourceName, verb, false},
		{"WrongNamespace", subjectType, subjectName, cluster, "wrong-ns", apiGroup, resource, resourceName, verb, false},
		{"WrongCluster", subjectType, subjectName, "wrong-cluster", namespace, apiGroup, resource, resourceName, verb, false},
		{"WrongSubjectName", subjectType, "wrong-user", cluster, namespace, apiGroup, resource, resourceName, verb, false},
		{"WrongSubjectType", SubjectTypeGroup, subjectName, cluster, namespace, apiGroup, resource, resourceName, verb, false},
		{"InvalidVerb", subjectType, subjectName, cluster, namespace, apiGroup, resource, resourceName, "invalid", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := trie.CheckResourcePermission(tc.subjectType, tc.subjectName, tc.cluster, tc.namespace, tc.apiGroup, tc.resource, tc.resourceName, tc.verb)
			if got != tc.expected {
				t.Errorf("Expected permission to be %v, but got %v", tc.expected, got)
			}
		})
	}
}

func TestPermissionTrie_Wildcards(t *testing.T) {
	trie := NewPermissionTrie()
	subject := "wildcard-user"
	cluster := "test-cluster"

	// Verb wildcard
	trie.AddResourcePermission(SubjectTypeUser, subject, cluster, "ns1", "apps", "pods", "*", nil)
	// Resource wildcard
	trie.AddResourcePermission(SubjectTypeUser, subject, cluster, "ns1", "apps", "*", "get", nil)
	// APIGroup wildcard
	trie.AddResourcePermission(SubjectTypeUser, subject, cluster, "ns1", "*", "deployments", "list", nil)
	// Namespace wildcard (cluster-level)
	trie.AddResourcePermission(SubjectTypeUser, subject, cluster, "", "batch", "jobs", "watch", nil)
	// ResourceName wildcard
	trie.AddResourcePermission(SubjectTypeUser, subject, cluster, "ns2", "core", "secrets", "get", nil)
	// URL wildcard
	trie.AddURLPermission(SubjectTypeUser, subject, cluster, "*", "get")

	testCases := []struct {
		name     string
		check    func() bool
		expected bool
	}{
		{"VerbWildcard_AllowsGet", func() bool {
			return trie.CheckResourcePermission(SubjectTypeUser, subject, cluster, "ns1", "apps", "pods", "pod1", "get")
		}, true},
		{"VerbWildcard_AllowsDelete", func() bool {
			return trie.CheckResourcePermission(SubjectTypeUser, subject, cluster, "ns1", "apps", "pods", "pod1", "delete")
		}, true},
		{"ResourceWildcard_AllowsPods", func() bool {
			return trie.CheckResourcePermission(SubjectTypeUser, subject, cluster, "ns1", "apps", "pods", "pod1", "get")
		}, true},
		{"ResourceWildcard_AllowsServices", func() bool {
			return trie.CheckResourcePermission(SubjectTypeUser, subject, cluster, "ns1", "apps", "services", "svc1", "get")
		}, true},
		{"APIGroupWildcard_AllowsApps", func() bool {
			return trie.CheckResourcePermission(SubjectTypeUser, subject, cluster, "ns1", "apps", "deployments", "dep1", "list")
		}, true},
		{"APIGroupWildcard_AllowsBatch", func() bool {
			return trie.CheckResourcePermission(SubjectTypeUser, subject, cluster, "ns1", "batch", "deployments", "dep1", "list")
		}, true},
		{"NamespaceWildcard_AllowsInNamespace", func() bool {
			return trie.CheckResourcePermission(SubjectTypeUser, subject, cluster, "some-ns", "batch", "jobs", "job1", "watch")
		}, true},
		{"NamespaceWildcard_AllowsInEmptyNamespace", func() bool {
			return trie.CheckResourcePermission(SubjectTypeUser, subject, cluster, "", "batch", "jobs", "job1", "watch")
		}, true},
		{"ResourceNameWildcard_AllowsAnyName", func() bool {
			return trie.CheckResourcePermission(SubjectTypeUser, subject, cluster, "ns2", "core", "secrets", "secret1", "get")
		}, true},
		{"ResourceNameWildcard_AllowsOtherName", func() bool {
			return trie.CheckResourcePermission(SubjectTypeUser, subject, cluster, "ns2", "core", "secrets", "secret2", "get")
		}, true},
		{"URLWildcard_AllowsSpecificURL", func() bool { return trie.CheckURLPermission(SubjectTypeUser, subject, cluster, "/api/v1/pods", "get") }, true},
		{"URLWildcard_AllowsOtherURL", func() bool { return trie.CheckURLPermission(SubjectTypeUser, subject, cluster, "/metrics", "get") }, true},
		{"URLWildcard_DeniesWrongVerb", func() bool { return trie.CheckURLPermission(SubjectTypeUser, subject, cluster, "/api/v1/pods", "post") }, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.check(); got != tc.expected {
				t.Errorf("Expected permission to be %v, but got %v", tc.expected, got)
			}
		})
	}
}

func TestPermissionTrie_AddAndCheckURLPermission(t *testing.T) {
	trie := NewPermissionTrie()
	subject := "url-user"
	cluster := "c1"
	url := "/api/v1/nodes"
	verb := "get"

	trie.AddURLPermission(SubjectTypeUser, subject, cluster, url, verb)
	trie.AddURLPermission(SubjectTypeUser, subject, cluster, "/metrics", "*")

	testCases := []struct {
		name     string
		url      string
		verb     string
		expected bool
	}{
		{"ExactMatch", url, verb, true},
		{"WrongVerb", url, "post", false},
		{"WrongURL", "/api/v1/pods", verb, false},
		{"VerbWildcard_AllowsGet", "/metrics", "get", true},
		{"VerbWildcard_AllowsPost", "/metrics", "post", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := trie.CheckURLPermission(SubjectTypeUser, subject, cluster, tc.url, tc.verb)
			if got != tc.expected {
				t.Errorf("Expected permission for URL %s with verb %s to be %v, but got %v", tc.url, tc.verb, tc.expected, got)
			}
		})
	}
}

func TestPermissionTrie_RemoveClusterPermissions(t *testing.T) {
	trie := NewPermissionTrie()
	subject := "multi-cluster-user"

	// Add permissions for two clusters
	trie.AddResourcePermission(SubjectTypeUser, subject, "cluster-a", "default", "core", "pods", "get", nil)
	trie.AddURLPermission(SubjectTypeUser, subject, "cluster-a", "/logs", "get")
	trie.AddResourcePermission(SubjectTypeUser, subject, "cluster-b", "default", "core", "pods", "get", nil)

	// Verify permissions exist
	if !trie.CheckResourcePermission(SubjectTypeUser, subject, "cluster-a", "default", "core", "pods", "pod1", "get") {
		t.Fatal("Expected permission to exist in cluster-a before removal")
	}
	if !trie.CheckResourcePermission(SubjectTypeUser, subject, "cluster-b", "default", "core", "pods", "pod1", "get") {
		t.Fatal("Expected permission to exist in cluster-b before removal")
	}

	// Remove cluster-a
	auth := &RBACAuthorizer{trie: trie}
	auth.RemoveClusterPermissions("cluster-a")

	// Verify permissions for cluster-a are gone
	if trie.CheckResourcePermission(SubjectTypeUser, subject, "cluster-a", "default", "core", "pods", "pod1", "get") {
		t.Error("Expected resource permission to be removed from cluster-a, but it still exists")
	}
	if trie.CheckURLPermission(SubjectTypeUser, subject, "cluster-a", "/logs", "get") {
		t.Error("Expected URL permission to be removed from cluster-a, but it still exists")
	}

	// Verify permissions for cluster-b remain
	if !trie.CheckResourcePermission(SubjectTypeUser, subject, "cluster-b", "default", "core", "pods", "pod1", "get") {
		t.Error("Expected permission to remain for cluster-b, but it was removed")
	}

	// Verify subject node was not deleted because it still has cluster-b
	subjectKey := getSubjectKey(SubjectTypeUser, subject)
	if _, exists := trie.subjectNodes[subjectKey]; !exists {
		t.Error("SubjectNode was deleted but should not have been")
	}

	// Now remove cluster-b and check that the subject node is also removed
	auth.RemoveClusterPermissions("cluster-b")
	if _, exists := trie.subjectNodes[subjectKey]; exists {
		t.Error("SubjectNode was not deleted but should have been")
	}
}

func TestPermissionTrie_EmptyTrie(t *testing.T) {
	trie := NewPermissionTrie()
	if trie.CheckResourcePermission(SubjectTypeUser, "any-user", "any-cluster", "any-ns", "any-group", "any-res", "any-name", "get") {
		t.Error("Expected empty trie to always deny resource permission")
	}
	if trie.CheckURLPermission(SubjectTypeUser, "any-user", "any-cluster", "/any/url", "get") {
		t.Error("Expected empty trie to always deny URL permission")
	}
}
