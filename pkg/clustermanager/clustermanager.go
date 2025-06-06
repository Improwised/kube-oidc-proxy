package clustermanager

import (
	"context"
	"fmt"
	"sync"

	"github.com/Improwised/kube-oidc-proxy/pkg/models"
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

type ClusterManager struct {
	clusters                map[string]*models.Cluster
	lock                    sync.RWMutex
	clientset               kubernetes.Interface
	tokenPassthroughEnables bool
	audiences               []string
	clustersRoleConfigMap   map[string]util.RBAC
	capiRbacWatcher         *crd.CAPIRbacWatcher
	stopCh                  <-chan struct{}
	SetupFunc               func(*models.Cluster) error
}

func NewClusterManager(stopCh <-chan struct{}, tokenPassthroughEnables bool, audiences []string, clustersRoleConfigMap map[string]util.RBAC, capiRbacWatcher *crd.CAPIRbacWatcher) (*ClusterManager, error) {
	config, err := util.BuildConfiguration()
	if err != nil {
		return nil, err
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &ClusterManager{
		clusters:                make(map[string]*models.Cluster),
		clientset:               client,
		stopCh:                  stopCh,
		tokenPassthroughEnables: tokenPassthroughEnables,
		audiences:               audiences,
		clustersRoleConfigMap:   clustersRoleConfigMap,
		capiRbacWatcher:         capiRbacWatcher,
	}, nil
}

func (cm *ClusterManager) WatchDynamicClusters(namespace, secretName string) {

	watcher, err := cm.clientset.CoreV1().Secrets(namespace).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", secretName),
	})
	if err != nil {
		klog.Errorf("Failed to start watcher for secret %s: %v", secretName, err)
		return
	}

	for {
		select {
		case event := <-watcher.ResultChan():
			secret, ok := event.Object.(*corev1.Secret)
			if !ok {
				klog.Errorf("Unexpected object type in watcher")
				continue
			}

			switch event.Type {
			case watch.Added, watch.Modified:
				cm.updateDynamicClusters(secret)
			case watch.Deleted:
				cm.removeDynamicClusters(secret)
			}
		case <-cm.stopCh:
			watcher.Stop()
			return
		}
	}
}

func (cm *ClusterManager) updateDynamicClusters(secret *corev1.Secret) error {

	// Build set of current clusters in the secret
	currentClusters := make(map[string]bool)
	for clusterName := range secret.Data {
		currentClusters[clusterName] = true
	}

	// Remove clusters not present in the current secret
	existingClusters := cm.GetAllClusters()
	for _, cluster := range existingClusters {
		if _, exists := currentClusters[cluster.Name]; !exists && !cluster.IsStatic {
			cm.RemoveCluster(cluster.Name)
		}
	}

	for clusterName, kubeconfig := range secret.Data {
		restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
		if err != nil {
			klog.Errorf("Failed to create rest config for %s: %v", clusterName, err)
			continue
		}

		cluster := &models.Cluster{
			Name:       clusterName,
			RestConfig: restConfig,
			IsStatic:   false,
		}
		err = cm.ClusterSetup(cluster)
		if err != nil {
			return err
		}

		// Add this after cluster setup
		if cm.SetupFunc != nil {
			if err := cm.SetupFunc(cluster); err != nil {
				klog.Errorf("Proxy setup failed for %s: %v", clusterName, err)
				continue
			}
		}
		cm.AddOrUpdateCluster(cluster)

		if cm.capiRbacWatcher != nil {
			// Update watcher with latest clusters
			cm.capiRbacWatcher.UpdateClusters(cm.GetAllClusters())

			// Reprocess RBAC objects for new cluster
			cm.capiRbacWatcher.ProcessExistingRBACObjects()
		}
	}
	return nil
}

func (cm *ClusterManager) removeDynamicClusters(secret *corev1.Secret) {
	for clusterName := range secret.Data {
		cm.RemoveCluster(clusterName)
	}
}

func (cm *ClusterManager) AddOrUpdateCluster(cluster *models.Cluster) {
	cm.lock.Lock()
	defer cm.lock.Unlock()

	if existing, exists := cm.clusters[cluster.Name]; exists {
		*existing = *cluster
		klog.Infof("Updated cluster: %s", cluster.Name)
	} else {
		cm.clusters[cluster.Name] = cluster
		klog.Infof("Added cluster: %s", cluster.Name)
	}
}

func (cm *ClusterManager) RemoveCluster(name string) {
	cm.lock.Lock()
	defer cm.lock.Unlock()

	if _, exists := cm.clusters[name]; exists {
		delete(cm.clusters, name)
		klog.Infof("Removed cluster: %s", name)
	}
}

func (cm *ClusterManager) GetCluster(name string) *models.Cluster {
	cm.lock.RLock()
	defer cm.lock.RUnlock()
	return cm.clusters[name]
}

func (cm *ClusterManager) GetAllClusters() []*models.Cluster {
	cm.lock.RLock()
	defer cm.lock.RUnlock()

	clusters := make([]*models.Cluster, 0, len(cm.clusters))
	for _, c := range cm.clusters {
		clusters = append(clusters, c)
	}
	return clusters
}

func (cm *ClusterManager) ClusterSetup(cluster *models.Cluster) error {
	// Create kubeclient
	kubeclient, err := kubernetes.NewForConfig(cluster.RestConfig)
	if err != nil {
		return err
	}
	cluster.Kubeclient = kubeclient

	// Setup Subject Access Reviewer
	subjectAccessReviewer, err := subjectaccessreview.New(kubeclient.AuthorizationV1().SubjectAccessReviews())
	if err != nil {
		return err
	}
	cluster.SubjectAccessReviewer = subjectAccessReviewer

	// Initialize Token Reviewer if enabled
	if cm.tokenPassthroughEnables {
		tokenReviewer, err := tokenreview.New(cluster.RestConfig, cm.audiences)
		if err != nil {
			return err
		}
		cluster.TokenReviewer = tokenReviewer
	}

	roleConfig := cm.clustersRoleConfigMap[cluster.Name]

	// Apply RBAC configuration
	cluster.RBACConfig = &util.RBAC{}
	cluster.RBACConfig.Roles = append(cluster.RBACConfig.Roles, roleConfig.Roles...)
	cluster.RBACConfig.ClusterRoles = append(cluster.RBACConfig.ClusterRoles, roleConfig.ClusterRoles...)
	cluster.RBACConfig.ClusterRoleBindings = append(cluster.RBACConfig.ClusterRoleBindings, roleConfig.ClusterRoleBindings...)
	cluster.RBACConfig.RoleBindings = append(cluster.RBACConfig.RoleBindings, roleConfig.RoleBindings...)

	// Load RBAC
	err = rbac.LoadRBAC(cluster)
	if err != nil {
		return err
	}
	klog.V(5).Info("cluster setup complete for cluster: ", cluster.Name)

	return nil
}
