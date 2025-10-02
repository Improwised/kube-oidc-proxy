package crd

import (
	"context"
	"sync"
	"time"

	"github.com/Improwised/kube-oidc-proxy/pkg/cluster"
	"github.com/Improwised/kube-oidc-proxy/pkg/util"
	"github.com/Improwised/kube-oidc-proxy/pkg/util/authorizer"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
	cleanupInterval                time.Duration
}

func NewCAPIRbacWatcher(clusters []*cluster.Cluster, auth authorizer.Interface, cleanupInterval time.Duration) (*CAPIRbacWatcher, error) {

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

	if cleanupInterval <= 0 {
		cleanupInterval = time.Minute
	}

	watcher := &CAPIRbacWatcher{
		CAPIRoleInformer:               capiRoleInformer,
		CAPIClusterRoleInformer:        capiClusterRoleInformer,
		CAPIRoleBindingInformer:        capiRoleBindingInformer,
		CAPIClusterRoleBindingInformer: capiClusterRoleBindingInformer,
		clusters:                       clusters,
		authorizer:                     auth,
		dynamicClient:                  clusterClient,
		cleanupInterval:                cleanupInterval,
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
	go w.startExpiredBindingsCleanup(stopCh)
}

func (w *CAPIRbacWatcher) startExpiredBindingsCleanup(stopCh <-chan struct{}) {
	klog.Info("Starting expired bindings cleanup routine")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		<-stopCh
		cancel()
	}()

	ticker := time.NewTicker(w.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.cleanupExpiredBindings(ctx)
		case <-stopCh:
			klog.Info("Stopping expired bindings cleanup routine")
			return
		}
	}
}

func (w *CAPIRbacWatcher) cleanupExpiredBindings(ctx context.Context) {
	klog.V(4).Info("Running cleanup for expired bindings")

	// CAPIClusterRoleBindings
	clusterRoleBindings := w.CAPIClusterRoleBindingInformer.GetStore().List()
	for _, obj := range clusterRoleBindings {
		binding, err := ConvertUnstructured[CAPIClusterRoleBinding](obj)
		if err != nil {
			klog.Errorf("Failed to convert CAPIClusterRoleBinding during cleanup check: %v", err)
			continue
		}

		if util.IsExpired(binding.CreationTimestamp.Time, binding.Spec.DurationMinutes) {
			go w.deleteBinding(ctx, CAPIClusterRoleBindingGVR, "", binding.Name)
		}
	}

	// CAPIRoleBindings
	roleBindings := w.CAPIRoleBindingInformer.GetStore().List()
	for _, obj := range roleBindings {
		binding, err := ConvertUnstructured[CAPIRoleBinding](obj)
		if err != nil {
			klog.Errorf("Failed to convert CAPIRoleBinding during cleanup check: %v", err)
			continue
		}

		if util.IsExpired(binding.CreationTimestamp.Time, binding.Spec.DurationMinutes) {
			go w.deleteBinding(ctx, CAPIRoleBindingGVR, "", binding.Name)
		}
	}
}

func (w *CAPIRbacWatcher) deleteBinding(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string) {
	logName := name
	if namespace != "" {
		logName = namespace + "/" + name
	}
	klog.V(2).Infof("Deleting expired binding %s", logName)

	var err error
	if namespace == "" {
		err = w.dynamicClient.Resource(gvr).Delete(ctx, name, metav1.DeleteOptions{})
	} else {
		err = w.dynamicClient.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	}

	if err != nil {
		// Don't log "not found" errors, as the binding might have been deleted by another process
		// or in a previous reconciliation loop.
		if !apierrors.IsNotFound(err) {
			klog.Errorf("Failed to delete expired binding %s: %v", logName, err)
		}
	} else {
		klog.V(2).Infof("Successfully deleted expired binding %s", logName)
	}
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
			w.DeleteCAPIRoleBinding(oldCapiRoleBinding)
			w.ProcessCAPIRoleBinding(newCapiRoleBinding)
			w.RebuildAllAuthorizers()
		},
		DeleteFunc: func(obj interface{}) {
			u, ok := obj.(*unstructured.Unstructured)
			if !ok {
				klog.Errorf("Unexpected type %T in DeleteFunc for CAPIRoleBinding", obj)
				return
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
			w.DeleteCAPIClusterRoleBinding(oldCapiClusterRoleBinding)
			w.ProcessCAPIClusterRoleBinding(newCapiClusterRoleBinding)
			w.RebuildAllAuthorizers()
		},
		DeleteFunc: func(obj interface{}) {
			u, ok := obj.(*unstructured.Unstructured)
			if !ok {
				klog.Errorf("Unexpected type %T in DeleteFunc for CAPIClusterRoleBinding", obj)
				return
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
