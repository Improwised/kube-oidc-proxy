package crd

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/tools/cache"
)

// mockInformer for testing
type mockInformer struct {
	cache.SharedIndexInformer
	store cache.Store
}

func (m *mockInformer) GetStore() cache.Store {
	return m.store
}

func TestCleanupExpiredBindings_DeletesExpired(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	now := metav1.Now()
	expiredTimestamp := metav1.Time{Time: now.Add(-60 * time.Minute)}
	duration := int64(30)

	// Create an expired CAPIClusterRoleBinding
	expiredCRB := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "rbac.platformengineers.io/v1",
			"kind":       "CAPIClusterRoleBinding",
			"metadata": map[string]interface{}{
				"name":              "expired-crb",
				"creationTimestamp": expiredTimestamp.Format(time.RFC3339),
			},
			"spec": map[string]interface{}{
				"durationMinutes": duration,
			},
		},
	}

	// Create an active CAPIClusterRoleBinding
	activeCRB := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "rbac.platformengineers.io/v1",
			"kind":       "CAPIClusterRoleBinding",
			"metadata": map[string]interface{}{
				"name":              "active-crb",
			"creationTimestamp": now.Format(time.RFC3339),
			},
			"spec": map[string]interface{}{
				"durationMinutes": duration,
			},
		},
	}

	// Create an expired CAPIRoleBinding
	expiredRB := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "rbac.platformengineers.io/v1",
			"kind":       "CAPIRoleBinding",
			"metadata": map[string]interface{}{
				"name":              "expired-rb",
				"creationTimestamp": expiredTimestamp.Format(time.RFC3339),
			},
			"spec": map[string]interface{}{
				"durationMinutes": duration,
			},
		},
	}

	// Setup fake client and watcher
	fakeClient := fake.NewSimpleDynamicClient(runtime.NewScheme(), expiredCRB, activeCRB, expiredRB)
	clusterBindingStore := cache.NewStore(cache.MetaNamespaceKeyFunc)
	require.NoError(t, clusterBindingStore.Add(expiredCRB))
	require.NoError(t, clusterBindingStore.Add(activeCRB))
	roleBindingStore := cache.NewStore(cache.MetaNamespaceKeyFunc)
	require.NoError(t, roleBindingStore.Add(expiredRB))

	watcher := &CAPIRbacWatcher{
		dynamicClient:                  fakeClient,
		CAPIClusterRoleBindingInformer: &mockInformer{store: clusterBindingStore},
		CAPIRoleBindingInformer:        &mockInformer{store: roleBindingStore},
	}

	// Run cleanup
	watcher.cleanupExpiredBindings(ctx)

	// Verify asynchronous deletion
	err := wait.PollImmediate(100*time.Millisecond, 5*time.Second, func() (bool, error) {
		// Check that expiredCRB is gone
		_, errGetCRB := fakeClient.Resource(CAPIClusterRoleBindingGVR).Get(ctx, "expired-crb", metav1.GetOptions{})
		if !apierrors.IsNotFound(errGetCRB) {
			return false, nil // Not deleted yet or other error
		}

		// Check that expiredRB is gone
		_, errGetRB := fakeClient.Resource(CAPIRoleBindingGVR).Get(ctx, "expired-rb", metav1.GetOptions{})
		if !apierrors.IsNotFound(errGetRB) {
			return false, nil // Not deleted yet or other error
		}

		return true, nil // Both are deleted
	})
	require.NoError(t, err, "Expired bindings were not deleted within the time limit")

	// Verify non-expired binding still exists
	_, err = fakeClient.Resource(CAPIClusterRoleBindingGVR).Get(ctx, "active-crb", metav1.GetOptions{})
	assert.NoError(t, err, "Active binding should not have been deleted")
}
