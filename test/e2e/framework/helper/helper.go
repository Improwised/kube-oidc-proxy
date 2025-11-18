// Copyright Jetstack Ltd. See LICENSE for details.
package helper

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/Improwised/kube-oidc-proxy/test/e2e/framework/config"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Helper provides methods for common operations needed during tests.
type Helper struct {
	cfg *config.Config

	KubeClient          kubernetes.Interface
	ApiExtensionsClient apiextensionsclientset.Interface
	restConfig          *rest.Config
}

func NewHelper(cfg *config.Config, restConfig *rest.Config) *Helper {
	return &Helper{
		cfg:        cfg,
		restConfig: restConfig,
	}
}

// NewDynamicClient creates a new dynamic Kubernetes client.
func (h *Helper) NewDynamicClient() (dynamic.Interface, error) {

	if h.restConfig == nil {
		return nil, fmt.Errorf("restConfig is not initialized")
	}

	// Create a dynamic client using the provided REST config
	return dynamic.NewForConfig(h.restConfig)
}

func (h *Helper) Config() *config.Config {
	return h.cfg
}

func (h *Helper) CreateCRDObject(obj interface{}, gvr schema.GroupVersionResource, namespace string) error {
	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return fmt.Errorf("failed to convert object to unstructured: %v", err)
	}

	dynamicClient, err := h.NewDynamicClient()
	if err != nil {
		return err
	}

	_, err = dynamicClient.Resource(gvr).Namespace(namespace).Create(
		context.TODO(),
		&unstructured.Unstructured{Object: unstructuredObj},
		metav1.CreateOptions{},
	)
	return err
}

func (h *Helper) UpdateCRDObject(obj interface{}, gvr schema.GroupVersionResource, namespace string) error {
	// Convert the object to an unstructured map
	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return fmt.Errorf("failed to convert object to unstructured: %v", err)
	}

	dynamicClient, err := h.NewDynamicClient()
	if err != nil {
		return err
	}

	// Fetch the existing resource to get its resourceVersion
	existingObj, err := dynamicClient.Resource(gvr).Namespace(namespace).Get(context.TODO(), unstructuredObj["metadata"].(map[string]interface{})["name"].(string), metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to fetch existing object: %v", err)
	}

	// Copy the resourceVersion from the existing object
	unstructuredObj["metadata"].(map[string]interface{})["resourceVersion"] = existingObj.GetResourceVersion()

	// Perform the update with the resourceVersion
	_, err = dynamicClient.Resource(gvr).Namespace(namespace).Update(
		context.TODO(),
		&unstructured.Unstructured{Object: unstructuredObj},
		metav1.UpdateOptions{},
	)
	return err
}

func (h *Helper) DeleteCRDObject(name string, gvr schema.GroupVersionResource, namespace string) error {
	dynamicClient, err := h.NewDynamicClient()
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %v", err)
	}

	// Delete the resource by name
	err = dynamicClient.Resource(gvr).Namespace(namespace).Delete(
		context.TODO(),
		name,
		metav1.DeleteOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to delete object: %v", err)
	}

	return nil
}
