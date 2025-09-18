package authorizer

import (
	"github.com/Improwised/kube-oidc-proxy/pkg/util"
	v1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/klog/v2"
)

// Attributes holds all the details about a request to be checked.
type Attributes struct {
	User    user.Info
	Verb    string
	Cluster string

	IsResourceRequest bool
	Path              string
	Namespace         string
	APIGroup          string
	Resource          string
	ResourceName      string
}

// Interface describes what an authorizer can do.
type Interface interface {
	// CheckPermission checks if a user can perform an action.
	CheckPermission(attrs Attributes) bool

	// UpdatePermissionTrie loads all RBAC rules from a cluster.
	UpdatePermissionTrie(rbacConfig *util.RBAC, clusterName string)

	// RemoveClusterPermissions deletes all rules for a specific cluster.
	RemoveClusterPermissions(cluster string)
}

// RBACAuthorizer checks permissions using a PermissionTrie.
type RBACAuthorizer struct {
	trie *PermissionTrie
}

// NewRBACAuthorizer creates a new, empty authorizer.
func NewRBACAuthorizer() Interface {
	trie := NewPermissionTrie()
	return &RBACAuthorizer{
		trie: trie,
	}
}

// UpdatePermissionTrie loads a fresh set of RBAC rules for a cluster.
func (a *RBACAuthorizer) UpdatePermissionTrie(rbacConfig *util.RBAC, clusterName string) {
	// First, delete all old rules for this cluster.
	a.RemoveClusterPermissions(clusterName)

	// Combine rules from ClusterRoles that include other roles.
	resolvedClusterRoles := resolveAggregatedRoles(rbacConfig.ClusterRoles)

	// Make RoleBindings easy to look up.
	roleBindingSubjects := make(map[string][]v1.Subject)
	for _, binding := range rbacConfig.RoleBindings {
		if binding.RoleRef.Kind == "Role" {
			key := "Role/" + binding.Namespace + "/" + binding.RoleRef.Name
			roleBindingSubjects[key] = append(roleBindingSubjects[key], binding.Subjects...)
		}
	}

	// Make ClusterRoleBindings easy to look up.
	clusterRoleBindingSubjects := make(map[string][]v1.Subject)
	for _, binding := range rbacConfig.ClusterRoleBindings {
		if binding.RoleRef.Kind == "ClusterRole" {
			key := "ClusterRole/" + binding.RoleRef.Name
			clusterRoleBindingSubjects[key] = append(clusterRoleBindingSubjects[key], binding.Subjects...)
		}
	}

	// Add rules from each Role.
	for _, role := range rbacConfig.Roles {
		key := "Role/" + role.Namespace + "/" + role.Name
		if subjects, found := roleBindingSubjects[key]; found {
			for _, subject := range subjects {
				a.addRulesForSubject(subject, clusterName, role.Namespace, role.Rules)
			}
		}
	}

	// Add rules from each ClusterRole.
	for _, clusterRole := range resolvedClusterRoles {
		key := "ClusterRole/" + clusterRole.Name
		if subjects, found := clusterRoleBindingSubjects[key]; found {
			for _, subject := range subjects {
				a.addRulesForSubject(subject, clusterName, "", clusterRole.Rules)
			}
		}
	}
}

// addRulesForSubject is a helper that adds all permissions from a rule set for one subject.
func (a *RBACAuthorizer) addRulesForSubject(subject v1.Subject, clusterName, namespace string, rules []v1.PolicyRule) {
	subjectType := SubjectType(subject.Kind)
	if subjectType != SubjectTypeUser && subjectType != SubjectTypeGroup && subjectType != SubjectTypeServiceAccount {
		return
	}

	for _, rule := range rules {
		for _, verb := range rule.Verbs {
			// Add permissions for URL paths.
			for _, url := range rule.NonResourceURLs {
				a.trie.AddURLPermission(subjectType, subject.Name, clusterName, url, verb)
			}

			// Add permissions for API resources (like "pods").
			apiGroups := rule.APIGroups
			if len(apiGroups) == 0 {
				apiGroups = []string{""}
			}
			for _, apiGroup := range apiGroups {
				for _, resource := range rule.Resources {
					a.trie.AddResourcePermission(subjectType, subject.Name, clusterName, namespace, apiGroup, resource, verb, rule.ResourceNames)
				}
			}
		}
	}
}

// RemoveClusterPermissions deletes all rules associated with a single cluster.
func (a *RBACAuthorizer) RemoveClusterPermissions(cluster string) {
	a.trie.mu.Lock()
	defer a.trie.mu.Unlock()
	for subjectKey, subjectNode := range a.trie.subjectNodes {
		if _, exists := subjectNode.clusterNodes[cluster]; exists {
			delete(subjectNode.clusterNodes, cluster)

			if len(subjectNode.clusterNodes) == 0 {
				delete(a.trie.subjectNodes, subjectKey)
			}
		}
	}
}

// CheckPermission checks if a user or any of their groups has permission.
func (a *RBACAuthorizer) CheckPermission(attrs Attributes) bool {
	// 1. Check permissions for the user directly.
	if a.checkSubjectPermission(attrs.User.GetName(), SubjectTypeUser, attrs) {
		return true
	}

	// 2. Check permissions for each of the user's groups.
	for _, group := range attrs.User.GetGroups() {
		if a.checkSubjectPermission(group, SubjectTypeGroup, attrs) {
			return true
		}
	}

	return false
}

// checkSubjectPermission is a helper that checks permissions for a single subject.
func (a *RBACAuthorizer) checkSubjectPermission(subjectName string, subjectType SubjectType, attrs Attributes) bool {
	if attrs.IsResourceRequest {
		return a.trie.CheckResourcePermission(subjectType, subjectName, attrs.Cluster, attrs.Namespace, attrs.APIGroup, attrs.Resource, attrs.ResourceName, attrs.Verb)
	}
	return a.trie.CheckURLPermission(subjectType, subjectName, attrs.Cluster, attrs.Path, attrs.Verb)
}

// resolveAggregatedRoles finds ClusterRoles that include other roles and combines their rules.
func resolveAggregatedRoles(clusterRoles []*v1.ClusterRole) []*v1.ClusterRole {
	rolesByName := make(map[string]*v1.ClusterRole, len(clusterRoles))
	for _, role := range clusterRoles {
		rolesByName[role.Name] = role.DeepCopy()
	}

	for _, role := range rolesByName {
		resolveStack := make(map[string]bool)
		resolveAggregation(role, rolesByName, resolveStack)
	}

	resolvedRoles := make([]*v1.ClusterRole, 0, len(rolesByName))
	for _, role := range rolesByName {
		resolvedRoles = append(resolvedRoles, role)
	}
	return resolvedRoles
}

// resolveAggregation is a helper that recursively combines rules from aggregated roles.
func resolveAggregation(role *v1.ClusterRole, rolesByName map[string]*v1.ClusterRole, resolveStack map[string]bool) {
	// Avoid infinite loops if roles include each other.
	if resolveStack[role.Name] {
		klog.Warningf("Warning: cycle detected in ClusterRole aggregation involving %s\n", role.Name)
		return
	}
	resolveStack[role.Name] = true
	defer func() { resolveStack[role.Name] = false }()

	if role.AggregationRule == nil {
		return
	}

	for _, selector := range role.AggregationRule.ClusterRoleSelectors {
		parsedSelector, err := metav1.LabelSelectorAsSelector(&selector)
		if err != nil {
			klog.Warningf("Warning: could not parse label selector in ClusterRole %s: %v\n", role.Name, err)
			continue
		}

		// Find other roles that match the selector.
		for _, otherRole := range rolesByName {
			if otherRole.Name == role.Name {
				continue
			}
			if parsedSelector.Matches(labels.Set(otherRole.Labels)) {
				// First, resolve the other role's aggregations.
				resolveAggregation(otherRole, rolesByName, resolveStack)
				// Then, append its rules to the current role.
				role.Rules = append(role.Rules, otherRole.Rules...)
			}
		}
	}
}
