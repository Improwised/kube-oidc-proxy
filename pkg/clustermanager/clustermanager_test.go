// Copyright Improwised Ltd. See LICENSE for details.
package clustermanager

import (
	"testing"

	"github.com/Improwised/kube-oidc-proxy/pkg/cluster"
	"github.com/Improwised/kube-oidc-proxy/pkg/util"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// TestAddAndRetrieveCluster tests adding a cluster and retrieving it
func TestAddAndRetrieveCluster(t *testing.T) {
	// Create a ClusterManager
	cm := &ClusterManager{
		clusters: make(map[string]*cluster.Cluster),
	}

	// Create a test cluster
	testCluster := &cluster.Cluster{
		Name:     "test-cluster",
		IsStatic: true,
	}

	// Add the cluster
	cm.AddOrUpdateCluster(testCluster)

	// Retrieve the cluster
	retrievedCluster := cm.GetCluster("test-cluster")

	// Assert the result
	assert.NotNil(t, retrievedCluster)
	assert.Equal(t, testCluster, retrievedCluster)
}

// TestUpdateExistingCluster tests updating an existing cluster
func TestUpdateExistingCluster(t *testing.T) {
	// Create a ClusterManager
	cm := &ClusterManager{
		clusters: make(map[string]*cluster.Cluster),
	}

	// Create a test cluster
	originalCluster := &cluster.Cluster{
		Name:     "test-cluster",
		IsStatic: true,
		Path:     "/original/path",
	}

	// Add the original cluster
	cm.AddOrUpdateCluster(originalCluster)

	// Create an updated cluster
	updatedCluster := &cluster.Cluster{
		Name:     "test-cluster",
		IsStatic: true,
		Path:     "/updated/path",
	}

	// Update the cluster
	cm.AddOrUpdateCluster(updatedCluster)

	// Retrieve the updated cluster
	retrievedCluster := cm.GetCluster("test-cluster")

	// Assert the result
	assert.NotNil(t, retrievedCluster)
	assert.Equal(t, updatedCluster.Path, retrievedCluster.Path)
}

// TestRemoveCluster tests removing a cluster
func TestRemoveCluster(t *testing.T) {
	// Create a ClusterManager
	cm := &ClusterManager{
		clusters: make(map[string]*cluster.Cluster),
	}

	// Create a test cluster
	testCluster := &cluster.Cluster{
		Name:     "test-cluster",
		IsStatic: true,
	}

	// Add the cluster
	cm.AddOrUpdateCluster(testCluster)

	// Verify the cluster was added
	assert.NotNil(t, cm.GetCluster("test-cluster"))

	// Remove the cluster
	cm.RemoveCluster("test-cluster")

	// Verify the cluster was removed
	assert.Nil(t, cm.GetCluster("test-cluster"))
}

// TestGetAllClusters tests retrieving all clusters
func TestGetAllClusters(t *testing.T) {
	// Create a ClusterManager
	cm := &ClusterManager{
		clusters: make(map[string]*cluster.Cluster),
	}

	// Create test clusters
	cluster1 := &cluster.Cluster{
		Name:     "cluster1",
		IsStatic: true,
	}
	cluster2 := &cluster.Cluster{
		Name:     "cluster2",
		IsStatic: false,
	}

	// Add the clusters
	cm.AddOrUpdateCluster(cluster1)
	cm.AddOrUpdateCluster(cluster2)

	// Get all clusters
	allClusters := cm.GetAllClusters()

	// Assert the result
	assert.Equal(t, 2, len(allClusters))

	// Check if both clusters are in the result
	var foundCluster1, foundCluster2 bool
	for _, cluster := range allClusters {
		if cluster.Name == "cluster1" {
			foundCluster1 = true
		}
		if cluster.Name == "cluster2" {
			foundCluster2 = true
		}
	}
	assert.True(t, foundCluster1)
	assert.True(t, foundCluster2)
}

// TestHandleInvalidKubeconfig tests handling invalid kubeconfig
func TestHandleInvalidKubeconfig(t *testing.T) {
	// Create a stop channel for the ClusterManager
	stopCh := make(chan struct{})
	defer close(stopCh)

	// Create a fake Kubernetes clientset
	fakeClient := fake.NewSimpleClientset()

	// Create a ClusterManager with the fake client
	cm := &ClusterManager{
		clusters:                make(map[string]*cluster.Cluster),
		clientset:               fakeClient,
		stopCh:                  stopCh,
		tokenPassthroughEnabled: false,
		clustersRoleConfigMap:   make(map[string]util.RBAC),
	}

	// Create a test secret with invalid kubeconfig data
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"cluster1": []byte(`invalid kubeconfig data`),
		},
	}

	// Update dynamic clusters
	err := cm.updateDynamicClusters(secret)

	// Assert the result
	assert.NoError(t, err)               // The function handles errors internally
	assert.Equal(t, 0, len(cm.clusters)) // No clusters should be added
}

// TestStaticClusterPersistence tests that static clusters persist when dynamic clusters are updated
func TestStaticClusterPersistence(t *testing.T) {
	// Create a stop channel for the ClusterManager
	stopCh := make(chan struct{})
	defer close(stopCh)

	// Create a fake Kubernetes clientset
	fakeClient := fake.NewSimpleClientset()

	// Create a ClusterManager with the fake client
	cm := &ClusterManager{
		clusters:                make(map[string]*cluster.Cluster),
		clientset:               fakeClient,
		stopCh:                  stopCh,
		tokenPassthroughEnabled: false,
		clustersRoleConfigMap:   make(map[string]util.RBAC),
	}

	// Add a static cluster
	staticCluster := &cluster.Cluster{
		Name:     "static-cluster",
		IsStatic: true,
	}
	cm.AddOrUpdateCluster(staticCluster)

	// Create a test secret with dynamic cluster data
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"dynamic-cluster": []byte(`invalid kubeconfig data`), // Invalid data, but it doesn't matter for this test
		},
	}

	// Update dynamic clusters
	err := cm.updateDynamicClusters(secret)

	// Assert the result
	assert.NoError(t, err)

	// Verify that the static cluster still exists
	assert.NotNil(t, cm.GetCluster("static-cluster"))

	// Create a new secret with different dynamic clusters
	newSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"new-dynamic-cluster": []byte(`invalid kubeconfig data`), // Invalid data, but it doesn't matter for this test
		},
	}

	// Update dynamic clusters again
	err = cm.updateDynamicClusters(newSecret)

	// Assert the result
	assert.NoError(t, err)

	// Verify that the static cluster still exists
	assert.NotNil(t, cm.GetCluster("static-cluster"))

	// Verify that the old dynamic cluster is removed
	assert.Nil(t, cm.GetCluster("dynamic-cluster"))
}

// TestRemoveDynamicClusters tests removing dynamic clusters
func TestRemoveDynamicClusters(t *testing.T) {
	// Create a ClusterManager
	cm := &ClusterManager{
		clusters: make(map[string]*cluster.Cluster),
	}

	// Add some clusters
	cluster1 := &cluster.Cluster{
		Name:     "cluster1",
		IsStatic: false,
	}
	cluster2 := &cluster.Cluster{
		Name:     "cluster2",
		IsStatic: false,
	}
	cm.AddOrUpdateCluster(cluster1)
	cm.AddOrUpdateCluster(cluster2)

	// Create a secret with clusters to remove
	secret := &corev1.Secret{
		Data: map[string][]byte{
			"cluster1": []byte("data"),
			"cluster2": []byte("data"),
		},
	}

	// Remove the dynamic clusters
	cm.removeDynamicClusters(secret)

	// Verify that the clusters were removed
	assert.Nil(t, cm.GetCluster("cluster1"))
	assert.Nil(t, cm.GetCluster("cluster2"))
}

// TestNewSecretController tests creating a new SecretController
func TestNewSecretController(t *testing.T) {
	// Create a fake Kubernetes client
	fakeClient := fake.NewSimpleClientset()

	// Create a ClusterManager with the fake client
	cm := &ClusterManager{
		clusters:                make(map[string]*cluster.Cluster),
		clientset:               fakeClient,
		tokenPassthroughEnabled: false,
		audiences:               []string{},
		clustersRoleConfigMap:   make(map[string]util.RBAC),
	}

	// Test creating a SecretController
	controller, err := NewSecretController(cm, "test-namespace", "test-secret")

	// Verify that the controller was created successfully
	assert.NoError(t, err)
	assert.NotNil(t, controller)
	assert.Equal(t, cm, controller.clusterManager)
	assert.Equal(t, "test-namespace", controller.namespace)
	assert.Equal(t, "test-secret", controller.secretName)
	assert.NotNil(t, controller.secretsInformer)
	assert.NotNil(t, controller.queue)
}
