package webhook

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	typesv1 "github.com/Improwised/kube-oidc-proxy/pkg/proxy/crd/types/v1"
)

// SetupCAPIClusterRoleWebhookWithManager registers the CAPIClusterRole webhook with the manager.
func SetupCAPIClusterRoleWebhookWithManager(mgr ctrl.Manager) error {
	validator := &CAPIClusterRoleCustomValidator{
		Client: mgr.GetClient(),
	}

	return ctrl.NewWebhookManagedBy(mgr).
		For(&typesv1.CAPIClusterRole{}).
		WithValidator(validator).
		Complete()
}

//+kubebuilder:webhook:path=/validate-rbac-platformengineers-io-v1-capiclusterrole,mutating=false,failurePolicy=fail,sideEffects=None,groups=rbac.platformengineers.io,resources=capiclusterroles,verbs=delete,versions=v1,name=vcapiclusterrole-v1.kb.io,admissionReviewVersions=v1

// CAPIClusterRoleCustomValidator validates CAPIClusterRole resources.
type CAPIClusterRoleCustomValidator struct {
	Client client.Client
}

// ValidateCreate validates CAPIClusterRole creation requests.
// No validation is performed on creation, this is a no-op.
// This method is required to satisfy the admission.CustomValidator interface.
func (v *CAPIClusterRoleCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// ValidateUpdate validates CAPIClusterRole update requests.
// No validation is performed on update, this is a no-op.
// This method is required to satisfy the admission.CustomValidator interface.
func (v *CAPIClusterRoleCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// ValidateDelete validates CAPIClusterRole deletion requests.
func (v *CAPIClusterRoleCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	capiclusterrole, ok := obj.(*typesv1.CAPIClusterRole)
	if !ok {
		return nil, fmt.Errorf("expected CAPIClusterRole but got %T", obj)
	}

	klog.Info("validating CAPIClusterRole deletion", "name", capiclusterrole.Name)

	resource := typesv1.GroupVersion.WithResource("capiclusterroles").GroupResource()

	// Check if role is referenced in any bindings before allowing deletion
	if err := checkCAPIRoleBindings(ctx, v.Client, capiclusterrole.Name, resource); err != nil {
		return nil, err
	}
	if err := checkCAPIClusterRoleBindings(ctx, v.Client, capiclusterrole.Name, resource); err != nil {
		return nil, err
	}

	return nil, nil
}
