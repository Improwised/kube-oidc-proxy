package webhook

import (
	ctrl "sigs.k8s.io/controller-runtime"
)

// SetupWebhooksWithManager registers all webhooks with the manager.
func SetupWebhooksWithManager(mgr ctrl.Manager) error {
	if err := SetupCAPIClusterRoleWebhookWithManager(mgr); err != nil {
		return err
	}
	if err := SetupCAPIRoleWebhookWithManager(mgr); err != nil {
		return err
	}
	return nil
}
