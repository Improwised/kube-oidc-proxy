// Package clustermanager provides functionality for managing multiple Kubernetes clusters
// including dynamic cluster discovery, RBAC configuration, and proxy setup.
package clustermanager

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Improwised/kube-oidc-proxy/pkg/cluster"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/crd"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/rbac"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/subjectaccessreview"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/tokenreview"
	"github.com/Improwised/kube-oidc-proxy/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/workqueue"
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

	// secretController is the controller that watches for secret changes
	secretController *SecretController
}

// SecretController is a Kubernetes controller that watches for changes to secrets
// containing cluster configurations and updates the ClusterManager accordingly.
// It follows the standard Kubernetes controller pattern using SharedInformers and workqueues.
type SecretController struct {
	// secretsInformer provides cached access to secrets
	secretsInformer cache.SharedIndexInformer

	// secretsSynced indicates when the secret cache has been synced
	secretsSynced cache.InformerSynced

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[*corev1.Secret]

	// clusterManager is the cluster manager that this controller updates
	clusterManager *ClusterManager

	// namespace is the namespace to watch for secrets
	namespace string

	// secretName is the name of the secret to watch
	secretName string
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

// NewSecretController creates a new SecretController instance that watches for changes
// to a specific secret and updates the provided ClusterManager accordingly.
//
// Parameters:
//   - clusterManager: The ClusterManager to update when secret changes occur
//   - namespace: The Kubernetes namespace containing the secret to watch
//   - secretName: The name of the secret containing cluster configurations
//
// Returns:
//   - A new SecretController instance and nil error on success
//   - nil and an error if initialization fails
func NewSecretController(clusterManager *ClusterManager, namespace, secretName string) (*SecretController, error) {
	// Create informer factory for the specific namespace
	informerFactory := informers.NewSharedInformerFactoryWithOptions(
		clusterManager.clientset,
		time.Minute*10, // Resync period
		informers.WithNamespace(namespace),
	)

	// Get the secret informer from the factory
	secretInformer := informerFactory.Core().V1().Secrets().Informer()

	// Create the controller instance
	controller := &SecretController{
		secretsInformer: secretInformer,
		secretsSynced:   secretInformer.HasSynced,
		queue: workqueue.NewTypedRateLimitingQueue(
			workqueue.DefaultTypedControllerRateLimiter[*corev1.Secret](),
		),
		clusterManager: clusterManager,
		namespace:      namespace,
		secretName:     secretName,
	}

	// Register event handlers to fill the queue with secret changes
	secretInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			secret, ok := obj.(*corev1.Secret)
			if !ok {
				klog.Errorf("Unexpected object type in AddFunc: expected Secret, got %T", obj)
				return
			}
			// Only process the specific secret we're interested in
			if secret.Name == secretName {
				controller.queue.Add(secret)
			}
		},
		UpdateFunc: func(old interface{}, new interface{}) {
			newSecret, ok := new.(*corev1.Secret)
			if !ok {
				klog.Errorf("Unexpected object type in UpdateFunc: expected Secret, got %T", new)
				return
			}
			// Only process the specific secret we're interested in
			if newSecret.Name == secretName {
				controller.queue.Add(newSecret)
			}
		},
		DeleteFunc: func(obj interface{}) {
			// IndexerInformer uses a delta queue, therefore for deletes we have to use this
			// key function.
			secret, ok := obj.(*corev1.Secret)
			if !ok {
				// Handle tombstone case
				if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
					secret, ok = tombstone.Obj.(*corev1.Secret)
					if !ok {
						klog.Errorf("Unexpected object type in DeleteFunc tombstone: expected Secret, got %T", tombstone.Obj)
						return
					}
				} else {
					klog.Errorf("Unexpected object type in DeleteFunc: expected Secret, got %T", obj)
					return
				}
			}
			// Only process the specific secret we're interested in
			if secret.Name == secretName {
				controller.queue.Add(secret)
			}
		},
	})

	return controller, nil
}

// Run starts the SecretController and blocks until the context is cancelled.
// It starts the informer, waits for caches to sync, and then starts worker goroutines
// to process items from the work queue.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - threadiness: Number of worker goroutines to start
//
// Returns:
//   - An error if cache sync fails, nil otherwise
func (sc *SecretController) Run(ctx context.Context, threadiness int) error {
	// Don't let panics crash the process
	defer runtime.HandleCrash()
	// Make sure the work queue is shutdown which will trigger workers to end
	defer sc.queue.ShutDown()

	logger := klog.FromContext(ctx)
	logger.Info("Starting secret controller")

	// Start the informer
	go sc.secretsInformer.Run(ctx.Done())

	// Wait for the secret cache to sync before starting workers
	if !cache.WaitForCacheSync(ctx.Done(), sc.secretsSynced) {
		return fmt.Errorf("failed to wait for secret caches to sync")
	}

	// Start up worker threads based on threadiness
	for i := 0; i < threadiness; i++ {
		// runWorker will loop until "something bad" happens. The wait.UntilWithContext will
		// then restart the worker after one second
		go wait.UntilWithContext(ctx, sc.runWorker, time.Second)
	}
	logger.Info("Started secret controller workers")

	// Wait until we're told to stop
	<-ctx.Done()
	logger.Info("Shutting down secret controller")

	return nil
}

// runWorker is a long-running function that will continually call the
// processNextWorkItem function in order to read and process a message on the workqueue.
func (sc *SecretController) runWorker(ctx context.Context) {
	// Hot loop until we're told to stop. processNextWorkItem will
	// automatically wait until there's work available, so we don't worry
	// about secondary waits
	for sc.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem deals with one item off the queue. It returns false
// when it's time to quit.
func (sc *SecretController) processNextWorkItem(ctx context.Context) bool {
	// Pull the next work item from queue. It will be an object reference that we use to lookup
	// something in a cache
	ref, shutdown := sc.queue.Get()
	if shutdown {
		return false
	}
	// You always have to indicate to the queue that you've completed a piece of work
	defer sc.queue.Done(ref)

	// Process the object reference. This method contains the "do stuff" logic
	err := sc.syncHandler(ctx, ref)
	if err == nil {
		// If you had no error, tell the queue to stop tracking history for your
		// item. This will reset things like failure counts for per-item rate limiting
		sc.queue.Forget(ref)
		return true
	}

	// There was a failure so be sure to report it. This method allows for
	// pluggable error handling which can be used for things like cluster-monitoring
	runtime.HandleErrorWithContext(ctx, err, "Error syncing secret; requeuing for later retry", "objectReference", ref)

	// Since we failed, we should requeue the item to work on later. This
	// method will add a backoff to avoid hotlooping on particular items
	// (they're probably still not going to work right away) and overall
	// controller protection (everything I've done is broken, this controller
	// needs to calm down or it can starve other useful work) cases.
	sc.queue.AddRateLimited(ref)

	return true
}

// syncHandler contains the business logic of the controller. It processes a single
// secret object reference and updates the cluster manager accordingly.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - key: Object key containing namespace/name of the secret to process
//
// Returns:
//   - nil on success, error if processing fails
//
// syncHandler contains the business logic of the controller [...]
func (sc *SecretController) syncHandler(ctx context.Context, key interface{}) error {
	logger := klog.FromContext(ctx)

	// Handle both string keys and direct Secret objects
	var secret *corev1.Secret
	switch v := key.(type) {
	case string:
		// Get the secret from the informer cache
		obj, exists, err := sc.secretsInformer.GetStore().GetByKey(v)
		if err != nil {
			return fmt.Errorf("failed to get secret from cache: %w", err)
		}
		if !exists {
			// Secret was deleted
			logger.V(4).Info("Secret was deleted, removing associated clusters", "key", v)
			sc.handleSecretDeletion(v)
			return nil
		}
		secret = obj.(*corev1.Secret)
	case *corev1.Secret:
		secret = v
	default:
		return fmt.Errorf("unexpected key type: expected string or *v1.Secret, got %T", key)
	}

	// Verify secret name matches our target
	if secret.Name != sc.secretName {
		logger.V(5).Info("Ignoring secret that doesn't match target name",
			"secretName", secret.Name, "targetName", sc.secretName)
		return nil
	}

	// Process the secret
	logger.V(4).Info("Processing secret", "name", secret.Name)
	return sc.clusterManager.updateDynamicClusters(secret)
}

// handleSecretDeletion handles the case when the watched secret is deleted.
// It removes all dynamic clusters that were created from that secret.
//
// Parameters:
//   - key: Object key of the deleted secret
func (sc *SecretController) handleSecretDeletion(key string) {
	// Since the secret is deleted, we need to remove all dynamic clusters
	// We can't get the secret data anymore, so we remove all non-static clusters
	existingClusters := sc.clusterManager.GetAllClusters()
	for _, cluster := range existingClusters {
		if !cluster.IsStatic {
			klog.V(4).Infof("Removing dynamic cluster %s due to secret deletion", cluster.Name)
			sc.clusterManager.RemoveCluster(cluster.Name)
		}
	}

	// Update CAPI RBAC watcher if available
	if sc.clusterManager.capiRbacWatcher != nil {
		sc.clusterManager.capiRbacWatcher.UpdateClusters(sc.clusterManager.GetAllClusters())
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

// StartSecretController creates and starts a SecretController to watch for changes
// to a specific secret containing cluster configurations. This replaces the old
// WatchDynamicClusters method with a proper Kubernetes controller pattern.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - namespace: The Kubernetes namespace containing the secret
//   - secretName: The name of the secret containing cluster configurations
//   - threadiness: Number of worker goroutines for the controller (default: 1)
//
// Returns:
//   - An error if controller creation or startup fails, nil otherwise
func (cm *ClusterManager) StartSecretController(ctx context.Context, namespace, secretName string, threadiness int) error {
	// Create the secret controller
	controller, err := NewSecretController(cm, namespace, secretName)
	if err != nil {
		return fmt.Errorf("failed to create secret controller: %w", err)
	}

	// Store the controller reference
	cm.secretController = controller

	// Start the controller in a goroutine
	go func() {
		if err := controller.Run(ctx, threadiness); err != nil {
			klog.Errorf("Secret controller failed: %v", err)
		}
	}()

	klog.V(4).Infof("Started secret controller for secret %s/%s", namespace, secretName)
	return nil
}

// StopSecretController stops the secret controller if it's running.
// This is useful for graceful shutdown.
func (cm *ClusterManager) StopSecretController() {
	if cm.secretController != nil {
		// The controller will stop when its context is cancelled
		// The queue shutdown is handled in the Run method
		klog.V(4).Info("Secret controller will stop when context is cancelled")
	}
}
