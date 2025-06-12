package crd

import (
	"time"

	"github.com/Improwised/kube-oidc-proxy/pkg/cluster"
	"github.com/Improwised/kube-oidc-proxy/pkg/util"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
}

func NewCAPIRbacWatcher(clusters []*cluster.Cluster) (*CAPIRbacWatcher, error) {

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
}

func (w *CAPIRbacWatcher) RegisterEventHandlers() {
	// Register event handlers for CAPIRole
	w.CAPIRoleInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
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
