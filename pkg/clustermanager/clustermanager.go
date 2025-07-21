// Package clustermanager provides functionality for managing multiple Kubernetes clusters
// including dynamic cluster discovery, RBAC configuration, and proxy setup.
package clustermanager

import (
	"context"
	"fmt"
	"sync"

	"github.com/Improwised/kube-oidc-proxy/pkg/cluster"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/crd"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/rbac"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/subjectaccessreview"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/tokenreview"
	"github.com/Improwised/kube-oidc-proxy/pkg/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

// ClusterManager manages a collection of Kubernetes clusters, providing functionality
// for adding, updating, removing, and retrieving clusters. It also handles dynamic
// cluster discovery through Kubernetes secrets and configures RBAC for each cluster.
type ClusterManager struct {
	// clusters is a map of cluster name to cluster object
	clusters map[string]*cluster.Cluster

	// lock provides thread-safe access to the clusters map
	lock sync.RWMutex

	// clientset is the Kubernetes client for the management cluster
	clientset kubernetes.Interface

	// tokenPassthroughEnabled determines if token passthrough is enabled for clusters
	tokenPassthroughEnabled bool

	// audiences is a list of valid token audiences for token review
	audiences []string

	// clustersRoleConfigMap maps cluster names to their RBAC configurations
	clustersRoleConfigMap map[string]util.RBAC

	// capiRbacWatcher watches for CAPI RBAC changes and applies them to clusters
	capiRbacWatcher *crd.CAPIRbacWatcher

	// stopCh is a channel used to signal the manager to stop watching for changes
	stopCh <-chan struct{}

	// SetupFunc is an optional function called after a cluster is set up
	// to perform additional configuration
	SetupFunc func(*cluster.Cluster) error
}

// NewClusterManager creates a new ClusterManager instance with the provided configuration.
//
// Parameters:
//   - stopCh: Channel used to signal when to stop watching for cluster changes
//   - tokenPassthroughEnabled: Whether to enable token passthrough for authentication
//   - audiences: List of valid token audiences for token review
//   - clustersRoleConfigMap: Map of cluster names to their RBAC configurations
//   - capiRbacWatcher: Watcher for CAPI RBAC changes
//
// Returns:
//   - A new ClusterManager instance and nil error on success
//   - nil and an error if configuration fails
func NewClusterManager(stopCh <-chan struct{}, tokenPassthroughEnabled bool, audiences []string, clustersRoleConfigMap map[string]util.RBAC, capiRbacWatcher *crd.CAPIRbacWatcher) (*ClusterManager, error) {
	// Build Kubernetes configuration for the management cluster
	config, err := util.BuildConfiguration()
	if err != nil {
		return nil, fmt.Errorf("failed to build Kubernetes configuration: %w", err)
	}

	// Create Kubernetes client for the management cluster
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// Initialize and return the ClusterManager
	return &ClusterManager{
		clusters:                make(map[string]*cluster.Cluster),
		clientset:               client,
		stopCh:                  stopCh,
		tokenPassthroughEnabled: tokenPassthroughEnabled,
		audiences:               audiences,
		clustersRoleConfigMap:   clustersRoleConfigMap,
		capiRbacWatcher:         capiRbacWatcher,
	}, nil
}

// WatchDynamicClusters starts watching a specific secret in the given namespace
// for changes that affect dynamic cluster configurations. It processes events
// to add, update, or remove clusters based on the secret's contents.
//
// Parameters:
//   - namespace: The Kubernetes namespace containing the secret
//   - secretName: The name of the secret containing cluster configurations
func (cm *ClusterManager) WatchDynamicClusters(namespace, secretName string) {
	// Create a watcher for the specified secret
	watcher, err := cm.clientset.CoreV1().Secrets(namespace).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", secretName),
	})
	if err != nil {
		klog.Errorf("Failed to start watcher for secret %s: %v", secretName, err)
		return
	}

	// Process events from the watcher
	for {
		select {
		case event := <-watcher.ResultChan():
			// Type assertion to ensure we have a Secret object
			secret, ok := event.Object.(*corev1.Secret)
			if !ok {
				klog.Errorf("Unexpected object type in watcher: expected Secret, got %T", event.Object)
				continue
			}

			// Handle different event types
			switch event.Type {
			case watch.Added, watch.Modified:
				// Secret was added or modified, update clusters
				klog.V(4).Infof("Secret %s/%s was added or modified, updating clusters", namespace, secretName)
				cm.updateDynamicClusters(secret)
			case watch.Deleted:
				// Secret was deleted, remove associated clusters
				klog.V(4).Infof("Secret %s/%s was deleted, removing associated clusters", namespace, secretName)
				cm.removeDynamicClusters(secret)
			}
		case <-cm.stopCh:
			// Stop channel was closed, stop watching and return
			klog.V(4).Infof("Stopping watcher for secret %s/%s", namespace, secretName)
			watcher.Stop()
			return
		}
	}
}

// updateDynamicClusters processes a secret containing cluster configurations,
// adding new clusters, updating existing ones, and removing clusters that
// are no longer present in the secret. Only dynamic (non-static) clusters
// are affected by this operation.
//
// Parameters:
//   - secret: The Kubernetes secret containing cluster configurations
//
// Returns:
//   - An error if cluster setup fails, nil otherwise
func (cm *ClusterManager) updateDynamicClusters(secret *corev1.Secret) error {
	// Build a set of current clusters defined in the secret
	currentClusters := make(map[string]bool)
	for clusterName := range secret.Data {
		currentClusters[clusterName] = true
	}

	// Remove dynamic clusters that are no longer present in the secret
	// Static clusters are preserved regardless of secret contents
	existingClusters := cm.GetAllClusters()
	for _, cluster := range existingClusters {
		if _, exists := currentClusters[cluster.Name]; !exists && !cluster.IsStatic {
			klog.V(4).Infof("Removing dynamic cluster %s as it's no longer in the secret", cluster.Name)
			cm.RemoveCluster(cluster.Name)
		}
	}

	// Process each cluster configuration in the secret
	for clusterName, kubeconfigData := range secret.Data {
		// Parse the kubeconfig data to create a REST config
		restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigData)
		if err != nil {
			klog.Errorf("Failed to create REST config for cluster %s: %v", clusterName, err)
			continue
		}

		// Create a new cluster object
		newCluster := &cluster.Cluster{
			Name:       clusterName,
			RestConfig: restConfig,
			IsStatic:   false, // Mark as dynamic cluster
		}

		// Set up the cluster with necessary components
		if err = cm.ClusterSetup(newCluster); err != nil {
			klog.Errorf("Failed to set up cluster %s: %v", clusterName, err)
			continue
		}

		// Run additional setup if a setup function is provided
		if cm.SetupFunc != nil {
			if err := cm.SetupFunc(newCluster); err != nil {
				klog.Errorf("Additional setup failed for cluster %s: %v", clusterName, err)
				continue
			}
		}

		// Add or update the cluster in the manager
		cm.AddOrUpdateCluster(newCluster)
		klog.V(4).Infof("Successfully added/updated dynamic cluster %s", clusterName)

		// Update CAPI RBAC watcher if available
		if cm.capiRbacWatcher != nil {
			// Update watcher with the latest set of clusters
			cm.capiRbacWatcher.UpdateClusters(cm.GetAllClusters())

			// Reprocess RBAC objects for the new/updated cluster
			cm.capiRbacWatcher.ProcessExistingRBACObjects()
		}
	}

	return nil
}

// removeDynamicClusters removes all clusters specified in the given secret.
// This is typically called when a secret containing cluster configurations is deleted.
//
// Parameters:
//   - secret: The Kubernetes secret containing cluster configurations to remove
func (cm *ClusterManager) removeDynamicClusters(secret *corev1.Secret) {
	// Remove each cluster specified in the secret
	for clusterName := range secret.Data {
		klog.V(4).Infof("Removing cluster %s due to secret deletion", clusterName)
		cm.RemoveCluster(clusterName)
	}

	// Update CAPI RBAC watcher if available
	if cm.capiRbacWatcher != nil {
		cm.capiRbacWatcher.UpdateClusters(cm.GetAllClusters())
	}
}

// AddOrUpdateCluster adds a new cluster to the manager or updates an existing one.
// This operation is thread-safe.
//
// Parameters:
//   - cluster: The cluster to add or update
func (cm *ClusterManager) AddOrUpdateCluster(cluster *cluster.Cluster) {
	// Lock to ensure thread safety when modifying the clusters map
	cm.lock.Lock()
	defer cm.lock.Unlock()

	// Check if the cluster already exists
	if existing, exists := cm.clusters[cluster.Name]; exists {
		// Update existing cluster
		*existing = *cluster
		klog.Infof("Updated cluster: %s", cluster.Name)
	} else {
		// Add new cluster
		cm.clusters[cluster.Name] = cluster
		klog.Infof("Added cluster: %s", cluster.Name)
	}
}

// RemoveCluster removes a cluster from the manager by name.
// This operation is thread-safe.
//
// Parameters:
//   - name: The name of the cluster to remove
func (cm *ClusterManager) RemoveCluster(name string) {
	// Lock to ensure thread safety when modifying the clusters map
	cm.lock.Lock()
	defer cm.lock.Unlock()

	// Check if the cluster exists before removing
	if _, exists := cm.clusters[name]; exists {
		delete(cm.clusters, name)
		klog.Infof("Removed cluster: %s", name)
	} else {
		klog.V(5).Infof("Attempted to remove non-existent cluster: %s", name)
	}
}

// GetCluster retrieves a cluster by name from the manager.
// This operation is thread-safe. Returns nil if the cluster doesn't exist.
//
// Parameters:
//   - name: The name of the cluster to retrieve
//
// Returns:
//   - A pointer to the cluster if found, nil otherwise
func (cm *ClusterManager) GetCluster(name string) *cluster.Cluster {
	// Use read lock for thread-safe access to the clusters map
	cm.lock.RLock()
	defer cm.lock.RUnlock()

	// Return the cluster if it exists, nil otherwise
	return cm.clusters[name]
}

// GetAllClusters retrieves all clusters from the manager.
// This operation is thread-safe and returns a new slice containing all clusters.
//
// Returns:
//   - A slice of pointers to all clusters managed by this ClusterManager
func (cm *ClusterManager) GetAllClusters() []*cluster.Cluster {
	// Use read lock for thread-safe access to the clusters map
	cm.lock.RLock()
	defer cm.lock.RUnlock()

	// Create a new slice with capacity equal to the number of clusters
	clusters := make([]*cluster.Cluster, 0, len(cm.clusters))

	// Add all clusters to the slice
	for _, c := range cm.clusters {
		clusters = append(clusters, c)
	}

	return clusters
}

// ClusterSetup initializes a cluster with all necessary components:
// - Kubernetes client
// - Subject Access Reviewer
// - Token Reviewer (if token passthrough is enabled)
// - RBAC configuration
//
// Parameters:
//   - cluster: The cluster to set up
//
// Returns:
//   - nil on success, error otherwise
func (cm *ClusterManager) ClusterSetup(cluster *cluster.Cluster) error {
	// Create Kubernetes client for the cluster
	kubeclient, err := kubernetes.NewForConfig(cluster.RestConfig)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}
	cluster.Kubeclient = kubeclient

	// Set up Subject Access Reviewer for authorization checks
	subjectAccessReviewer, err := subjectaccessreview.New(kubeclient.AuthorizationV1().SubjectAccessReviews())
	if err != nil {
		return fmt.Errorf("failed to create Subject Access Reviewer: %w", err)
	}
	cluster.SubjectAccessReviewer = subjectAccessReviewer

	// Initialize Token Reviewer if token passthrough is enabled
	if cm.tokenPassthroughEnabled {
		tokenReviewer, err := tokenreview.New(cluster.RestConfig, cm.audiences)
		if err != nil {
			return fmt.Errorf("failed to create Token Reviewer: %w", err)
		}
		cluster.TokenReviewer = tokenReviewer
	}

	// Get RBAC configuration for this cluster
	roleConfig := cm.clustersRoleConfigMap[cluster.Name]

	// Initialize and apply RBAC configuration
	cluster.RBACConfig = &util.RBAC{}

	// Copy RBAC resources from the configuration
	if roleConfig.Roles != nil {
		cluster.RBACConfig.Roles = append(cluster.RBACConfig.Roles, roleConfig.Roles...)
	}

	if roleConfig.ClusterRoles != nil {
		cluster.RBACConfig.ClusterRoles = append(cluster.RBACConfig.ClusterRoles, roleConfig.ClusterRoles...)
	}

	if roleConfig.ClusterRoleBindings != nil {
		cluster.RBACConfig.ClusterRoleBindings = append(cluster.RBACConfig.ClusterRoleBindings, roleConfig.ClusterRoleBindings...)
	}

	if roleConfig.RoleBindings != nil {
		cluster.RBACConfig.RoleBindings = append(cluster.RBACConfig.RoleBindings, roleConfig.RoleBindings...)
	}

	// Load RBAC configuration into the cluster
	if err = rbac.LoadRBAC(cluster); err != nil {
		return fmt.Errorf("failed to load RBAC configuration: %w", err)
	}

	klog.V(5).Infof("Cluster setup complete for cluster: %s", cluster.Name)
	return nil
}
