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

// SetupCAPIRoleWebhookWithManager registers the CAPIRole webhook with the manager.
func SetupCAPIRoleWebhookWithManager(mgr ctrl.Manager) error {
	validator := &CAPIRoleCustomValidator{
		Client: mgr.GetClient(),
	}

	return ctrl.NewWebhookManagedBy(mgr).
		For(&typesv1.CAPIRole{}).
		WithValidator(validator).
		Complete()
}

//+kubebuilder:webhook:path=/validate-rbac-platformengineers-io-v1-capirole,mutating=false,failurePolicy=fail,sideEffects=None,groups=rbac.platformengineers.io,resources=capiroles,verbs=delete,versions=v1,name=vcapirole-v1.kb.io,admissionReviewVersions=v1

// CAPIRoleCustomValidator validates CAPIRole resources.
type CAPIRoleCustomValidator struct {
	Client client.Client
}

// ValidateCreate validates CAPIRole creation requests.
// No validation is performed on creation, this is a no-op.
// This method is required to satisfy the admission.CustomValidator interface.
func (v *CAPIRoleCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// ValidateUpdate validates CAPIRole update requests.
// No validation is performed on update, this is a no-op.
// This method is required to satisfy the admission.CustomValidator interface.
func (v *CAPIRoleCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// ValidateDelete validates CAPIRole deletion requests.
func (v *CAPIRoleCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	capirole, ok := obj.(*typesv1.CAPIRole)
	if !ok {
		return nil, fmt.Errorf("expected CAPIRole but got %T", obj)
	}

	klog.Info("validating CAPIRole deletion", "name", capirole.Name)

	// Check if role is referenced in any bindings before allowing deletion
	resource := typesv1.GroupVersion.WithResource("capiroles").GroupResource()
	return nil, checkCAPIRoleBindings(ctx, v.Client, capirole.Name, resource)
}
