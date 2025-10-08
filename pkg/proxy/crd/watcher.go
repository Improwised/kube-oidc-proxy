package crd

import (
	"context"
	"sync"
	"time"

	"github.com/Improwised/kube-oidc-proxy/pkg/cluster"
	"github.com/Improwised/kube-oidc-proxy/pkg/util"
	"github.com/Improwised/kube-oidc-proxy/pkg/util/authorizer"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

type CAPIRbacWatcher struct {
	CAPIClusterRoleInformer        cache.SharedIndexInformer
	CAPIRoleInformer               cache.SharedIndexInformer
	CAPIClusterRoleBindingInformer cache.SharedIndexInformer
	CAPIRoleBindingInformer        cache.SharedIndexInformer
	clusters                       []*cluster.Cluster
	initialProcessingComplete      bool
	authorizer                     authorizer.Interface
	mu                             sync.RWMutex
	dynamicClient                  dynamic.Interface
	bindingTimers                  map[string]*time.Timer
	timersMutex                    sync.Mutex
	jitEnabled                     bool
}

func NewCAPIRbacWatcher(clusters []*cluster.Cluster, auth authorizer.Interface, enableJIT bool) (*CAPIRbacWatcher, error) {

	clusterConfig, err := util.BuildConfiguration()
	if err != nil {
		return &CAPIRbacWatcher{}, err
	}

	clusterClient, err := dynamic.NewForConfig(clusterConfig)
	if err != nil {
		return &CAPIRbacWatcher{}, err
	}

	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(clusterClient,
		time.Minute, "", nil)

	capiRoleInformer := factory.ForResource(CAPIRoleGVR).Informer()
	capiRoleBindingInformer := factory.ForResource(CAPIRoleBindingGVR).Informer()
	capiClusterRoleInformer := factory.ForResource(CAPIClusterRoleGVR).Informer()
	capiClusterRoleBindingInformer := factory.ForResource(CAPIClusterRoleBindingGVR).Informer()

	watcher := &CAPIRbacWatcher{
		CAPIRoleInformer:               capiRoleInformer,
		CAPIClusterRoleInformer:        capiClusterRoleInformer,
		CAPIRoleBindingInformer:        capiRoleBindingInformer,
		CAPIClusterRoleBindingInformer: capiClusterRoleBindingInformer,
		clusters:                       clusters,
		authorizer:                     auth,
		dynamicClient:                  clusterClient,
		bindingTimers:                  make(map[string]*time.Timer),
		jitEnabled:                     enableJIT,
	}

	watcher.RegisterEventHandlers()

	return watcher, nil
}

// Start the informers
func (w *CAPIRbacWatcher) Start(stopCh <-chan struct{}) {
	go w.CAPIRoleInformer.Run(stopCh)
	go w.CAPIClusterRoleInformer.Run(stopCh)
	go w.CAPIRoleBindingInformer.Run(stopCh)
	go w.CAPIClusterRoleBindingInformer.Run(stopCh)
	cache.WaitForCacheSync(stopCh,
		w.CAPIRoleInformer.HasSynced,
		w.CAPIClusterRoleInformer.HasSynced,
		w.CAPIRoleBindingInformer.HasSynced,
		w.CAPIClusterRoleBindingInformer.HasSynced,
	)

	// Add a shutdown hook to stop all active timers
	go func() {
		<-stopCh
		klog.Info("Shutting down, stopping all binding timers...")
		w.timersMutex.Lock()
		defer w.timersMutex.Unlock()
		for key, timer := range w.bindingTimers {
			timer.Stop()
			delete(w.bindingTimers, key)
		}
		klog.Info("All binding timers stopped.")
	}()
}

// getBindingKey creates a unique key for a binding object.
// For cluster-scoped resources, it's the name. For namespaced resources, it's "namespace/name".
func getBindingKey(obj metav1.Object) string {
	if obj.GetNamespace() == "" {
		return obj.GetName()
	}
	return obj.GetNamespace() + "/" + obj.GetName()
}

func (w *CAPIRbacWatcher) markBindingAsExpired(gvr schema.GroupVersionResource, namespace, name string) {
	obj, err := w.dynamicClient.Resource(gvr).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			klog.Errorf("Failed to get binding %s/%s to mark as expired: %v", namespace, name, err)
		}
		return
	}

	expiredCondition := metav1.Condition{
		Type:               "Expired",
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             "BindingExpired",
		Message:            "The temporary binding has expired and is no longer effective.",
	}

	unstructuredConditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")

	var conditions []metav1.Condition
	for _, condUnstructured := range unstructuredConditions {
		var cond metav1.Condition
		condMap, _ := condUnstructured.(map[string]interface{})
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(condMap, &cond); err == nil {
			conditions = append(conditions, cond)
		}
	}

	meta.SetStatusCondition(&conditions, expiredCondition)

	var newUnstructuredConditions []interface{}
	for _, cond := range conditions {
		condMap, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&cond)
		newUnstructuredConditions = append(newUnstructuredConditions, condMap)
	}

	unstructured.SetNestedSlice(obj.Object, newUnstructuredConditions, "status", "conditions")

	_, err = w.dynamicClient.Resource(gvr).Namespace(namespace).UpdateStatus(context.Background(), obj, metav1.UpdateOptions{})
	if err != nil {
		klog.Errorf("Failed to update status for binding %s/%s: %v", namespace, name, err)
	} else {
		klog.V(2).Infof("Successfully marked binding %s/%s as expired", namespace, name)
	}
}

// removeTimerForBinding cancels and removes a scheduled deletion timer for a binding.
// This is called when a binding is deleted manually or updated.
func (w *CAPIRbacWatcher) removeTimerForBinding(obj metav1.Object) {
	key := getBindingKey(obj)

	w.timersMutex.Lock()
	defer w.timersMutex.Unlock()

	if timer, exists := w.bindingTimers[key]; exists {
		timer.Stop()
		delete(w.bindingTimers, key)
		klog.V(2).Infof("Cancelled and removed timer for binding %q", key)
	}
}

// addTimerForBinding schedules a binding for deletion if it has an expiration duration.
func (w *CAPIRbacWatcher) addTimerForBinding(obj metav1.Object, durationMinutes *int32, gvr schema.GroupVersionResource) {
	if durationMinutes == nil || *durationMinutes <= 0 {
		return
	}

	key := getBindingKey(obj)
	creationTime := obj.GetCreationTimestamp().Time

	// If the binding is already past its expiration time, delete it immediately.
	if util.IsBindingExpired(nil, creationTime, durationMinutes) {
		klog.V(2).Infof("Binding %q is already expired, marking as expired immediately", key)
		go w.markBindingAsExpired(gvr, obj.GetNamespace(), obj.GetName())
		return
	}

	_, duration := util.CalculateExpirationTime(creationTime, durationMinutes)

	klog.V(2).Infof("Scheduling deletion for binding %q in %v", key, duration)

	w.timersMutex.Lock()
	defer w.timersMutex.Unlock()

	// If a timer already exists (e.g., from a recent update), stop it before creating a new one.
	if timer, exists := w.bindingTimers[key]; exists {
		timer.Stop()
	}

	// Create a timer that will trigger the deletion.
	timer := time.AfterFunc(duration, func() {
		klog.V(2).Infof("Timer fired for binding %q, initiating deletion", key)
		// Safety check: verify binding still exists before updating
		_, err := w.dynamicClient.Resource(gvr).Namespace(obj.GetNamespace()).Get(context.Background(), obj.GetName(), metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			klog.V(3).Infof("Binding %q no longer exists, skipping expiration mark", key)
		} else if err == nil {
			// Binding exists, mark as expired
			w.markBindingAsExpired(gvr, obj.GetNamespace(), obj.GetName())
		} else {
			klog.Errorf("Failed to check binding %q existence: %v", key, err)
		}

		// Clean up the timer from the map.
		w.timersMutex.Lock()
		delete(w.bindingTimers, key)
		w.timersMutex.Unlock()
	})

	w.bindingTimers[key] = timer
}

func (w *CAPIRbacWatcher) RegisterEventHandlers() {
	// Register event handlers for CAPIRole
	w.CAPIRoleInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if !w.initialProcessingComplete {
				klog.V(10).Infof("Skipping CAPIRole add event during initial processing")
				return
			}
			capiRole, err := ConvertUnstructured[CAPIRole](obj)
			if err != nil {
				klog.Errorf("Failed to convert CAPIRole: %v", err)
				return
			}
			w.ProcessCAPIRole(capiRole)
			w.RebuildAllAuthorizers()
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldCapiRole, err := ConvertUnstructured[CAPIRole](oldObj)
			if err != nil {
				klog.Errorf("Failed to convert old CAPIRole: %v", err)
				return
			}
			newCapiRole, err := ConvertUnstructured[CAPIRole](newObj)
			if err != nil {
				klog.Errorf("Failed to convert new CAPIRole: %v", err)
				return
			}
			if oldCapiRole.ResourceVersion == newCapiRole.ResourceVersion {
				klog.V(5).Infof("ResourceVersion is the same, skipping update")
				return
			}
			w.DeleteCAPIRole(oldCapiRole)
			w.ProcessCAPIRole(newCapiRole)
			w.RebuildAllAuthorizers()
		},
		DeleteFunc: func(obj interface{}) {
			u, ok := obj.(*unstructured.Unstructured)
			if !ok {
				klog.Errorf("Unexpected type %T in DeleteFunc for CAPIRole", obj)
				return
			}
			capiRole, err := ConvertUnstructured[CAPIRole](u)
			if err != nil {
				klog.Errorf("Failed to convert CAPIRole during deletion: %v", err)
				return
			}
			w.DeleteCAPIRole(capiRole)
			w.RebuildAllAuthorizers()
		},
	})

	// Register event handlers for CAPIClusterRole
	w.CAPIClusterRoleInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if !w.initialProcessingComplete {
				klog.V(10).Infof("Skipping CAPIClusterRole add event during initial processing")
				return
			}
			capiClusterRole, err := ConvertUnstructured[CAPIClusterRole](obj)
			if err != nil {
				klog.Errorf("Failed to convert CAPIClusterRole: %v", err)
				return
			}
			w.ProcessCAPIClusterRole(capiClusterRole)
			w.RebuildAllAuthorizers()
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldCapiClusterRole, err := ConvertUnstructured[CAPIClusterRole](oldObj)
			if err != nil {
				klog.Errorf("Failed to convert old CAPIClusterRole: %v", err)
				return
			}
			newCapiClusterRole, err := ConvertUnstructured[CAPIClusterRole](newObj)
			if err != nil {
				klog.Errorf("Failed to convert new CAPIClusterRole: %v", err)
				return
			}
			if oldCapiClusterRole.ResourceVersion == newCapiClusterRole.ResourceVersion {
				klog.V(5).Infof("ResourceVersion is the same, skipping update")
				return
			}
			w.DeleteCAPIClusterRole(oldCapiClusterRole)
			w.ProcessCAPIClusterRole(newCapiClusterRole)
			w.RebuildAllAuthorizers()
		},
		DeleteFunc: func(obj interface{}) {
			u, ok := obj.(*unstructured.Unstructured)
			if !ok {
				klog.Errorf("Unexpected type %T in DeleteFunc for CAPIClusterRole", obj)
				return
			}
			capiClusterRole, err := ConvertUnstructured[CAPIClusterRole](u)
			if err != nil {
				klog.Errorf("Failed to convert CAPIClusterRole during deletion: %v", err)
				return
			}
			w.DeleteCAPIClusterRole(capiClusterRole)
			w.RebuildAllAuthorizers()
		},
	})

	// Register event handlers for CAPIRoleBinding
	w.CAPIRoleBindingInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if !w.initialProcessingComplete {
				klog.V(10).Infof("Skipping CAPIRoleBinding add event during initial processing")
				return
			}
			capiRoleBinding, err := ConvertUnstructured[CAPIRoleBinding](obj)
			if err != nil {
				klog.Errorf("Failed to convert CAPIRoleBinding: %v", err)
				return
			}
			w.ProcessCAPIRoleBinding(capiRoleBinding)
			w.RebuildAllAuthorizers()

			if w.jitEnabled && capiRoleBinding.Spec.DurationMinutes != nil && *capiRoleBinding.Spec.DurationMinutes > 0 {
				unstructuredObj, _ := obj.(*unstructured.Unstructured)
				w.addTimerForBinding(unstructuredObj, capiRoleBinding.Spec.DurationMinutes, CAPIRoleBindingGVR)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldCapiRoleBinding, err := ConvertUnstructured[CAPIRoleBinding](oldObj)
			if err != nil {
				klog.Errorf("Failed to convert old CAPIRoleBinding: %v", err)
				return
			}
			newCapiRoleBinding, err := ConvertUnstructured[CAPIRoleBinding](newObj)
			if err != nil {
				klog.Errorf("Failed to convert new CAPIRoleBinding: %v", err)
				return
			}
			if oldCapiRoleBinding.ResourceVersion == newCapiRoleBinding.ResourceVersion {
				klog.V(5).Infof("ResourceVersion is the same, skipping update")
				return
			}

			oldExpired := util.IsBindingExpired(oldCapiRoleBinding.Status.Conditions, oldCapiRoleBinding.CreationTimestamp.Time, oldCapiRoleBinding.Spec.DurationMinutes)
			newExpired := util.IsBindingExpired(newCapiRoleBinding.Status.Conditions, newCapiRoleBinding.CreationTimestamp.Time, newCapiRoleBinding.Spec.DurationMinutes)
			if !oldExpired && newExpired {
				klog.V(4).Infof("RoleBinding %s has expired, removing from authorizer", oldCapiRoleBinding.Name)
				w.DeleteCAPIRoleBinding(oldCapiRoleBinding)
				w.RebuildAllAuthorizers()
				return
			}

			oldUnstructuredObj, _ := oldObj.(*unstructured.Unstructured)
			newUnstructuredObj, _ := newObj.(*unstructured.Unstructured)

			w.DeleteCAPIRoleBinding(oldCapiRoleBinding)
			w.ProcessCAPIRoleBinding(newCapiRoleBinding)
			if w.jitEnabled {
				w.removeTimerForBinding(oldUnstructuredObj)
				w.addTimerForBinding(newUnstructuredObj, newCapiRoleBinding.Spec.DurationMinutes, CAPIRoleBindingGVR)
			}
			w.RebuildAllAuthorizers()
		},
		DeleteFunc: func(obj interface{}) {
			var u *unstructured.Unstructured
			if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				if u, ok = tombstone.Obj.(*unstructured.Unstructured); !ok {
					klog.Errorf("Unexpected type in tombstone %T for CAPIRoleBinding", tombstone.Obj)
					return
				}
			} else if u, ok = obj.(*unstructured.Unstructured); !ok {
				klog.Errorf("Unexpected type %T in DeleteFunc for CAPIRoleBinding", obj)
				return
			}

			if w.jitEnabled {
				w.removeTimerForBinding(u)
			}

			capiRoleBinding, err := ConvertUnstructured[CAPIRoleBinding](u)
			if err != nil {
				klog.Errorf("Failed to convert CAPIRoleBinding during deletion: %v", err)
				return
			}
			w.DeleteCAPIRoleBinding(capiRoleBinding)
			w.RebuildAllAuthorizers()
		},
	})

	// Register event handlers for CAPIClusterRoleBinding
	w.CAPIClusterRoleBindingInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if !w.initialProcessingComplete {
				klog.V(10).Infof("Skipping CAPIClusterRoleBinding add event during initial processing")
				return
			}
			capiClusterRoleBinding, err := ConvertUnstructured[CAPIClusterRoleBinding](obj)
			if err != nil {
				klog.Errorf("Failed to convert CAPIClusterRoleBinding: %v", err)
				return
			}
			w.ProcessCAPIClusterRoleBinding(capiClusterRoleBinding)
			w.RebuildAllAuthorizers()

			if w.jitEnabled && capiClusterRoleBinding.Spec.DurationMinutes != nil && *capiClusterRoleBinding.Spec.DurationMinutes > 0 {
				unstructuredObj, _ := obj.(*unstructured.Unstructured)
				w.addTimerForBinding(unstructuredObj, capiClusterRoleBinding.Spec.DurationMinutes, CAPIClusterRoleBindingGVR)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldCapiClusterRoleBinding, err := ConvertUnstructured[CAPIClusterRoleBinding](oldObj)
			if err != nil {
				klog.Errorf("Failed to convert old CAPIClusterRoleBinding: %v", err)
				return
			}
			newCapiClusterRoleBinding, err := ConvertUnstructured[CAPIClusterRoleBinding](newObj)
			if err != nil {
				klog.Errorf("Failed to convert new CAPIClusterRoleBinding: %v", err)
				return
			}
			if oldCapiClusterRoleBinding.ResourceVersion == newCapiClusterRoleBinding.ResourceVersion {
				klog.V(5).Infof("ResourceVersion is the same, skipping update")
				return
			}

			unstructuredObj, _ := newObj.(*unstructured.Unstructured)
			if unstructuredObj.GetDeletionTimestamp() != nil {
				return
			}

			oldExpired := util.IsBindingExpired(oldCapiClusterRoleBinding.Status.Conditions, oldCapiClusterRoleBinding.CreationTimestamp.Time, oldCapiClusterRoleBinding.Spec.DurationMinutes)
			newExpired := util.IsBindingExpired(newCapiClusterRoleBinding.Status.Conditions, newCapiClusterRoleBinding.CreationTimestamp.Time, newCapiClusterRoleBinding.Spec.DurationMinutes)
			if !oldExpired && newExpired {
				klog.V(4).Infof("ClusterRoleBinding %s has expired, removing from authorizer", oldCapiClusterRoleBinding.Name)
				w.DeleteCAPIClusterRoleBinding(oldCapiClusterRoleBinding)
				w.RebuildAllAuthorizers()
				return
			}

			oldUnstructuredObj, _ := oldObj.(*unstructured.Unstructured)
			newUnstructuredObj, _ := newObj.(*unstructured.Unstructured)

			w.DeleteCAPIClusterRoleBinding(oldCapiClusterRoleBinding)
			w.ProcessCAPIClusterRoleBinding(newCapiClusterRoleBinding)
			if w.jitEnabled {
				w.removeTimerForBinding(oldUnstructuredObj)
				w.addTimerForBinding(newUnstructuredObj, newCapiClusterRoleBinding.Spec.DurationMinutes, CAPIClusterRoleBindingGVR)
			}
			w.RebuildAllAuthorizers()
		},
		DeleteFunc: func(obj interface{}) {
			var u *unstructured.Unstructured
			if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				if u, ok = tombstone.Obj.(*unstructured.Unstructured); !ok {
					klog.Errorf("Unexpected type in tombstone %T for CAPIClusterRoleBinding", tombstone.Obj)
					return
				}
			} else if u, ok = obj.(*unstructured.Unstructured); !ok {
				klog.Errorf("Unexpected type %T in DeleteFunc for CAPIClusterRoleBinding", obj)
				return
			}

			if w.jitEnabled {
				w.removeTimerForBinding(u)
			}

			capiClusterRoleBinding, err := ConvertUnstructured[CAPIClusterRoleBinding](u)
			if err != nil {
				klog.Errorf("Failed to convert CAPIClusterRoleBinding during deletion: %v", err)
				return
			}
			w.DeleteCAPIClusterRoleBinding(capiClusterRoleBinding)
			w.RebuildAllAuthorizers()
		},
	})
}

func (w *CAPIRbacWatcher) UpdateClusters(clusters []*cluster.Cluster) {
	w.clusters = clusters
}
