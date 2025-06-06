// Copyright Jetstack Ltd. See LICENSE for details.
package app

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"k8s.io/apiserver/pkg/server"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/klog/v2"

	"github.com/Improwised/kube-oidc-proxy/cmd/app/options"
	"github.com/Improwised/kube-oidc-proxy/pkg/clustermanager"
	"github.com/Improwised/kube-oidc-proxy/pkg/models"
	"github.com/Improwised/kube-oidc-proxy/pkg/probe"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/crd"
	"github.com/Improwised/kube-oidc-proxy/pkg/util"
)

func NewRunCommand(stopCh <-chan struct{}) *cobra.Command {
	// Build options
	opts := options.New()

	// Build command
	cmd := buildRunCommand(stopCh, opts)

	// Add option flags to command
	opts.AddFlags(cmd)

	return cmd
}

// Proxy command
func buildRunCommand(stopCh <-chan struct{}, opts *options.Options) *cobra.Command {
	return &cobra.Command{
		Use:  options.AppName,
		Long: "kube-oidc-proxy is a reverse proxy to authenticate users to Kubernetes API servers with Open ID Connect Authentication.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.Validate(cmd); err != nil {
				return err
			}

			// check if the cluster config file exists
			if _, err := os.Stat(opts.App.Cluster.Config); err != nil {
				return err
			}

			// Load cluster config
			clustersConfig, err := LoadClusterConfig(opts.App.Cluster.Config)
			if err != nil {
				return err
			}

			// Validate cluster config
			if err := clusterConfigValidation(clustersConfig); err != nil {
				return err
			}

			var clustersRoleConfigMap map[string]util.RBAC
			if opts.App.Cluster.RoleConfig != "" {
				// check if the cluster role config file exists
				if _, err := os.Stat(opts.App.Cluster.RoleConfig); err != nil {
					return err
				}

				clustersRoleConfigMap, err = util.LoadRBACConfig(opts.App.Cluster.RoleConfig)
				if err != nil {
					return err
				}
			}

			capiRbacWatcher, err := crd.NewCAPIRbacWatcher(clustersConfig)
			if err != nil {
				klog.Errorf("Error starting CAPI RBAC watcher %v", err)
				capiRbacWatcher = nil
			}

			clusterManager, err := clustermanager.NewClusterManager(stopCh, opts.App.TokenPassthrough.Enabled, opts.App.TokenPassthrough.Audiences, clustersRoleConfigMap, capiRbacWatcher)
			if err != nil {
				return err
			}

			for _, cluster := range clustersConfig {

				ConfigFlags := &genericclioptions.ConfigFlags{
					KubeConfig: &cluster.Path,
				}
				tempClient := &options.ClientOptions{
					ConfigFlags: ConfigFlags,
				}

				r, err := tempClient.ToRESTConfig()
				if err != nil {
					return err
				}
				cluster.RestConfig = r

				err = clusterManager.ClusterSetup(cluster)
				if err != nil {
					return err
				}
				cluster.IsStatic = true
				clusterManager.AddOrUpdateCluster(cluster)

			}

			if capiRbacWatcher != nil {
				klog.V(5).Info("Starting CAPI RBAC watcher", capiRbacWatcher)
				capiRbacWatcher.Start(stopCh)
				capiRbacWatcher.ProcessExistingRBACObjects()
			}

			// Initialise Secure Serving Config
			secureServingInfo := new(server.SecureServingInfo)
			if err := opts.SecureServing.ApplyTo(&secureServingInfo); err != nil {
				return err
			}

			proxyConfig := &proxy.Config{
				TokenReview:          opts.App.TokenPassthrough.Enabled,
				DisableImpersonation: opts.App.DisableImpersonation,

				FlushInterval:   opts.App.FlushInterval,
				ExternalAddress: opts.SecureServing.BindAddress.String(),

				ExtraUserHeaders:                opts.App.ExtraHeaderOptions.ExtraUserHeaders,
				ExtraUserHeadersClientIPEnabled: opts.App.ExtraHeaderOptions.EnableClientIPExtraUserHeader,
			}

			// Initialise proxy with OIDC token authenticator
			p, err := proxy.New(opts.OIDCAuthentication, opts.Audit,
				secureServingInfo, proxyConfig, clusterManager)
			if err != nil {
				return err
			}

			// Set setup function for dynamic clusters
			clusterManager.SetupFunc = p.SetupClusterProxy

			go clusterManager.WatchDynamicClusters("default", "kube-oidc-proxy-kubeconfigs")

			// Create a fake JWT to set up readiness probe
			fakeJWT, err := util.FakeJWT(opts.OIDCAuthentication.IssuerURL)
			if err != nil {
				return err
			}

			// Start readiness probe
			if err := probe.Run(strconv.Itoa(opts.App.ReadinessProbePort),
				fakeJWT, p.OIDCTokenAuthenticator()); err != nil {
				return err
			}

			// Run proxy
			waitCh, listenerStoppedCh, err := p.Run(stopCh)
			if err != nil {
				return err
			}

			<-waitCh
			<-listenerStoppedCh

			if err := p.RunPreShutdownHooks(); err != nil {
				return err
			}

			return nil
		},
	}
}

func LoadClusterConfig(path string) ([]*models.Cluster, error) {
	var clusterList []*models.Cluster
	var parsedConfig struct {
		Clusters []struct {
			Name       string `yaml:"name"`
			Kubeconfig string `yaml:"kubeconfig"`
		} `yaml:"clusters"`
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Parse the YAML into the Config struct
	err = yaml.Unmarshal(data, &parsedConfig)
	if err != nil {
		return nil, err
	}

	for _, cluster := range parsedConfig.Clusters {
		clusterList = append(clusterList, &models.Cluster{
			Name: cluster.Name,
			Path: cluster.Kubeconfig,
		})
	}
	return clusterList, nil
}

func clusterConfigValidation(clusterConfig []*models.Cluster) error {
	// check if the cluster name is not empty and unique
	clusterNames := make(map[string]bool)
	for _, cluster := range clusterConfig {
		if cluster.Name == "" {
			return fmt.Errorf("cluster name is empty")
		}
		if _, ok := clusterNames[cluster.Name]; ok {
			return fmt.Errorf("cluster name %s is repeated", cluster.Name)
		}
		clusterNames[cluster.Name] = true
	}
	return nil
}
