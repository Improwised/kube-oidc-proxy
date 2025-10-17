package authorizer

import (
	"sync"
)

// Verb constants for bitmask
const (
	VerbGet uint32 = 1 << iota
	VerbList
	VerbWatch
	VerbCreate
	VerbUpdate
	VerbPatch
	VerbDelete
	VerbDeleteCollection
	VerbImpersonate
	VerbPost
	VerbPut
	VerbAll // Wildcard
)

// CollectionVerbs represents verbs that act on a collection of resources.
const CollectionVerbs = VerbList | VerbWatch | VerbCreate | VerbDeleteCollection

// verbMap translates verb strings to their bitmask representation
var verbMap = map[string]uint32{
	"get":              VerbGet,
	"list":             VerbList,
	"watch":            VerbWatch,
	"create":           VerbCreate,
	"update":           VerbUpdate,
	"patch":            VerbPatch,
	"delete":           VerbDelete,
	"deletecollection": VerbDeleteCollection,
	"impersonate":      VerbImpersonate,
	"post":             VerbPost,
	"put":              VerbPut,
	"*":                VerbAll,
}

// PermissionTrie holds all permissions in a tree-like structure for fast lookups.
type PermissionTrie struct {
	subjectNodes map[string]*SubjectNode
	mu           sync.RWMutex
}

// SubjectType is the type of entity (user, group, etc.).
type SubjectType string

const (
	SubjectTypeUser           SubjectType = "User"
	SubjectTypeGroup          SubjectType = "Group"
	SubjectTypeServiceAccount SubjectType = "ServiceAccount"
)

// SubjectNode is a branch in the trie for a specific user or group.
type SubjectNode struct {
	subjectType  SubjectType
	clusterNodes map[string]*ClusterNode
}

// ClusterNode is a branch for a specific Kubernetes cluster.
type ClusterNode struct {
	namespaceNodes  map[string]*NamespaceNode
	nonResourceURLs map[string]*URLNode
}

// URLNode holds permissions for a non-resource URL path.
type URLNode struct {
	verbs uint32
}

// NamespaceNode is a branch for a specific namespace.
type NamespaceNode struct {
	apiGroupNodes map[string]*APIGroupNode
}

// APIGroupNode is a branch for a specific API group (e.g., "apps").
type APIGroupNode struct {
	resourceNodes map[string]*ResourceNode
}

// ResourceNode holds the final permissions for a resource (e.g., "pods").
type ResourceNode struct {
	verbs         uint32
	resourceNames map[string]struct{} // nil means all names are allowed
}

// NewPermissionTrie creates a new, empty permission trie.
func NewPermissionTrie() *PermissionTrie {
	return &PermissionTrie{
		subjectNodes: make(map[string]*SubjectNode),
	}
}

// AddResourcePermission adds a permission for a specific API resource.
func (t *PermissionTrie) AddResourcePermission(subjectType SubjectType, subjectName, cluster, namespace, apiGroup, resource, verb string, resourceNames []string) {
	verbBit, ok := verbMap[verb]
	if !ok {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	subjectNode := t.getOrCreateSubjectNode(subjectType, subjectName)
	clusterNode := subjectNode.getOrCreateClusterNode(cluster)
	namespaceNode := clusterNode.getOrCreateNamespaceNode(namespace)
	apiGroupNode := namespaceNode.getOrCreateAPIGroupNode(apiGroup)
	resourceNode := apiGroupNode.getOrCreateResourceNode(resource)

	resourceNode.verbs |= verbBit

	if len(resourceNames) > 0 {
		if resourceNode.resourceNames == nil {
			resourceNode.resourceNames = make(map[string]struct{})
		}
		for _, name := range resourceNames {
			resourceNode.resourceNames[name] = struct{}{}
		}
	}
}

// AddURLPermission adds a permission for a non-resource URL path.
func (t *PermissionTrie) AddURLPermission(subjectType SubjectType, subjectName, cluster, url, verb string) {
	verbBit, ok := verbMap[verb]
	if !ok {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	subjectNode := t.getOrCreateSubjectNode(subjectType, subjectName)
	clusterNode := subjectNode.getOrCreateClusterNode(cluster)
	urlNode := clusterNode.getOrCreateURLNode(url)
	urlNode.verbs |= verbBit
}

// CheckResourcePermission checks if a subject has permission for a resource.
func (t *PermissionTrie) CheckResourcePermission(subjectType SubjectType, subjectName, cluster, namespace, apiGroup, resource, resourceName, verb string) bool {
	verbBit, ok := verbMap[verb]
	if !ok {
		return false
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	subjectKey := getSubjectKey(subjectType, subjectName)
	subjectNode, exists := t.subjectNodes[subjectKey]
	if !exists {
		return false
	}

	clusterNode, exists := subjectNode.clusterNodes[cluster]
	if !exists {
		return false
	}

	// 1. Check for rules in the specific namespace.
	if namespaceNode, exists := clusterNode.namespaceNodes[namespace]; exists {
		if namespaceNode.check(apiGroup, resource, resourceName, verbBit) {
			return true
		}
	}

	// 2. If no permission, check for cluster-wide rules (in namespace "").
	if namespace != "" {
		if namespaceNode, exists := clusterNode.namespaceNodes[""]; exists {
			if namespaceNode.check(apiGroup, resource, resourceName, verbBit) {
				return true
			}
		}
	}

	return false
}

// CheckURLPermission checks if a subject has permission for a URL path.
func (t *PermissionTrie) CheckURLPermission(subjectType SubjectType, subjectName, cluster, url, verb string) bool {
	verbBit, ok := verbMap[verb]
	if !ok {
		return false
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	subjectKey := getSubjectKey(subjectType, subjectName)
	subjectNode, exists := t.subjectNodes[subjectKey]
	if !exists {
		return false
	}

	clusterNode, exists := subjectNode.clusterNodes[cluster]
	if !exists {
		return false
	}

	// Check the specific URL, then the wildcard "*" URL.
	if urlNode, exists := clusterNode.nonResourceURLs[url]; exists {
		if (urlNode.verbs&VerbAll != 0) || (urlNode.verbs&verbBit != 0) {
			return true
		}
	}
	if urlNode, exists := clusterNode.nonResourceURLs["*"]; exists {
		if (urlNode.verbs&VerbAll != 0) || (urlNode.verbs&verbBit != 0) {
			return true
		}
	}

	return false
}

// check is an internal helper to search for permissions down the trie.
func (n *NamespaceNode) check(apiGroup, resource, resourceName string, verbBit uint32) bool {

	if apiGroupNode, exists := n.apiGroupNodes[apiGroup]; exists {
		if apiGroupNode.check(resource, resourceName, verbBit) {
			return true
		}
	}
	if apiGroupNode, exists := n.apiGroupNodes["*"]; exists {
		if apiGroupNode.check(resource, resourceName, verbBit) {
			return true
		}
	}

	return false
}

// check is an internal helper to search for permissions down the trie.
func (a *APIGroupNode) check(resource, resourceName string, verbBit uint32) bool {
	if resourceNode, exists := a.resourceNodes[resource]; exists {
		if resourceNode.check(resourceName, verbBit) {
			return true
		}
	}
	if resourceNode, exists := a.resourceNodes["*"]; exists {
		if resourceNode.check(resourceName, verbBit) {
			return true
		}
	}
	return false
}

// check is an internal helper to search for permissions down the trie.
func (r *ResourceNode) check(resourceName string, verbBit uint32) bool {
	verbAllowed := (r.verbs&VerbAll != 0) || (r.verbs&verbBit != 0)
	if !verbAllowed {
		return false
	}

	// Collection-level verbs are not restricted by resourceNames.
	if (verbBit & CollectionVerbs) != 0 {
		return true
	}

	// For item-level verbs, if resourceNames is nil, it's a wildcard.
	if r.resourceNames == nil {
		return true
	}
	// Otherwise, check if the specific name is in our list.
	_, exists := r.resourceNames[resourceName]
	return exists
}

// getOrCreateSubjectNode finds or creates a new node for a subject.
func (t *PermissionTrie) getOrCreateSubjectNode(subjectType SubjectType, subjectName string) *SubjectNode {
	subjectKey := getSubjectKey(subjectType, subjectName)
	if _, exists := t.subjectNodes[subjectKey]; !exists {
		t.subjectNodes[subjectKey] = &SubjectNode{
			subjectType:  subjectType,
			clusterNodes: make(map[string]*ClusterNode),
		}
	}
	return t.subjectNodes[subjectKey]
}

// getOrCreateClusterNode finds or creates a new node for a cluster.
func (s *SubjectNode) getOrCreateClusterNode(cluster string) *ClusterNode {
	if _, exists := s.clusterNodes[cluster]; !exists {
		s.clusterNodes[cluster] = &ClusterNode{
			namespaceNodes:  make(map[string]*NamespaceNode),
			nonResourceURLs: make(map[string]*URLNode),
		}
	}
	return s.clusterNodes[cluster]
}

// getOrCreateURLNode finds or creates a new node for a URL.
func (c *ClusterNode) getOrCreateURLNode(url string) *URLNode {
	if _, exists := c.nonResourceURLs[url]; !exists {
		c.nonResourceURLs[url] = &URLNode{verbs: 0}
	}
	return c.nonResourceURLs[url]
}

// getOrCreateNamespaceNode finds or creates a new node for a namespace.
func (c *ClusterNode) getOrCreateNamespaceNode(namespace string) *NamespaceNode {
	if _, exists := c.namespaceNodes[namespace]; !exists {
		c.namespaceNodes[namespace] = &NamespaceNode{
			apiGroupNodes: make(map[string]*APIGroupNode),
		}
	}
	return c.namespaceNodes[namespace]
}

// getOrCreateAPIGroupNode finds or creates a new node for an API group.
func (n *NamespaceNode) getOrCreateAPIGroupNode(apiGroup string) *APIGroupNode {
	if _, exists := n.apiGroupNodes[apiGroup]; !exists {
		n.apiGroupNodes[apiGroup] = &APIGroupNode{
			resourceNodes: make(map[string]*ResourceNode),
		}
	}
	return n.apiGroupNodes[apiGroup]
}

// getOrCreateResourceNode finds or creates a new node for a resource.
func (a *APIGroupNode) getOrCreateResourceNode(resource string) *ResourceNode {
	if _, exists := a.resourceNodes[resource]; !exists {
		a.resourceNodes[resource] = &ResourceNode{
			verbs:         0,
			resourceNames: nil,
		}
	}
	return a.resourceNodes[resource]
}

// getSubjectKey creates a unique ID string for a subject.
func getSubjectKey(subjectType SubjectType, subjectName string) string {
	return string(subjectType) + ":" + subjectName
}
