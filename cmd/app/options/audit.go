package options

import (
	"github.com/spf13/pflag"
	apiserveroptions "k8s.io/apiserver/pkg/server/options"
	cliflag "k8s.io/component-base/cli/flag"
)

type AuditOptions struct {
	*apiserveroptions.AuditOptions
	AuditWebhookServer string
}

func NewAuditOptions(nfs *cliflag.NamedFlagSets) *AuditOptions {
	a := &AuditOptions{
		AuditOptions: apiserveroptions.NewAuditOptions(),
	}

	return a.AddFlags(nfs.FlagSet("Audit"))
}

func (a *AuditOptions) AddFlags(fs *pflag.FlagSet) *AuditOptions {
	a.AuditOptions.AddFlags(fs)

	fs.StringVar(&a.AuditWebhookServer, "audit-webhook-server", a.AuditWebhookServer,
	 `Specify the server URL for the webhook audit backend (e.g., http://localhost:8080).
The backend will receive POST requests with a JSON-formatted audit log in the request body.
The endpoint to be called is <server-url>/api/v1/k8s-audit-log/webhook.`)
	return a
}
