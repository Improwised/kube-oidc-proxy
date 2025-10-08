package webhook

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	typesv1 "github.com/Improwised/kube-oidc-proxy/pkg/proxy/crd/types/v1"
)

// checkCAPIRoleBindings validates if a role is referenced in any CAPIRoleBinding.
func checkCAPIRoleBindings(ctx context.Context, c client.Client, roleName string, resource schema.GroupResource) error {
	bindingList := &typesv1.CAPIRoleBindingList{}
	if err := c.List(ctx, bindingList); err != nil {
		return apierrors.NewInternalError(fmt.Errorf("failed to list CAPIRoleBindings: %w", err))
	}

	for _, binding := range bindingList.Items {
		for _, roleRef := range binding.Spec.RoleRef {
			if roleRef == roleName {
				msg := fmt.Sprintf("cannot delete %q - still in use by CAPIRoleBinding %q", roleName, binding.Name)
				return apierrors.NewForbidden(resource, roleName, fmt.Errorf(msg))
			}
		}
	}

	return nil
}

// checkCAPIClusterRoleBindings validates if a role is referenced in any CAPIClusterRoleBinding.
func checkCAPIClusterRoleBindings(ctx context.Context, c client.Client, roleName string, resource schema.GroupResource) error {
	bindingList := &typesv1.CAPIClusterRoleBindingList{}
	if err := c.List(ctx, bindingList); err != nil {
		return apierrors.NewInternalError(fmt.Errorf("failed to list CAPIClusterRoleBindings: %w", err))
	}

	for _, binding := range bindingList.Items {
		for _, roleRef := range binding.Spec.RoleRef {
			if roleRef == roleName {
				msg := fmt.Sprintf("cannot delete %q - still in use by CAPIClusterRoleBinding %q", roleName, binding.Name)
				return apierrors.NewForbidden(resource, roleName, fmt.Errorf(msg))
			}
		}
	}

	return nil
}
