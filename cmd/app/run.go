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
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/Improwised/kube-oidc-proxy/cmd/app/options"
	"github.com/Improwised/kube-oidc-proxy/pkg/probe"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/crd"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/rbac"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/subjectaccessreview"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/tokenreview"
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

			isRBACLoded := make(map[string]bool)
			for _, cluster := range clustersConfig {
				isRBACLoded[cluster.Name] = false
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
				if cluster.RBACConfig == nil {
					cluster.RBACConfig = &util.RBAC{}
				}
			}

			// Initialise token reviewer if enabled
			var tokenReviewer *tokenreview.TokenReview
			if opts.App.TokenPassthrough.Enabled {

				for _, cluster := range clustersConfig {
					tokenReviewer, err = tokenreview.New(cluster.RestConfig, opts.App.TokenPassthrough.Audiences)
					if err != nil {
						return err
					}
					cluster.TokenReviewer = tokenReviewer
				}
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

			// Setup Subject Access Review for each cluster
			for _, cluster := range clustersConfig {
				kubeclient, err := kubernetes.NewForConfig(cluster.RestConfig)
				if err != nil {
					return err
				}

				subectAccessReviewer, err := subjectaccessreview.New(kubeclient.AuthorizationV1().SubjectAccessReviews())
				if err != nil {
					return err
				}

				cluster.Kubeclient = kubeclient
				cluster.SubjectAccessReviewer = subectAccessReviewer

			}

			for clusterName, RBACConfig := range clustersRoleConfigMap {
				for _, cluster := range clustersConfig {
					if cluster.Name == clusterName {
						isRBACLoded[cluster.Name] = true
						cluster.RBACConfig.Roles = append(cluster.RBACConfig.Roles, RBACConfig.Roles...)
						cluster.RBACConfig.ClusterRoles = append(cluster.RBACConfig.ClusterRoles, RBACConfig.ClusterRoles...)
						cluster.RBACConfig.ClusterRoleBindings = append(cluster.RBACConfig.ClusterRoleBindings, RBACConfig.ClusterRoleBindings...)
						cluster.RBACConfig.RoleBindings = append(cluster.RBACConfig.RoleBindings, RBACConfig.RoleBindings...)
						err := rbac.LoadRBAC(cluster)
						if err != nil {
							return err
						}
					}
				}
			}

			for _, cluster := range clustersConfig {
				if !isRBACLoded[cluster.Name] {
					err := rbac.LoadRBAC(cluster)
					if err != nil {
						return err
					}
				}
			}

			capiRbacWatcher, err := crd.NewCAPIRbacWatcher(clustersConfig)
			if err != nil {
				fmt.Println("Error starting CAPI RBAC watcher", err)
			} else {
				klog.V(5).Info("Starting CAPI RBAC watcher", capiRbacWatcher)
				capiRbacWatcher.Start(stopCh)

				existingCAPIRoles := capiRbacWatcher.CAPIRoleInformer.GetStore().List()

				for _, obj := range existingCAPIRoles {

					role, err := crd.ConvertUnstructured[crd.CAPIRole](obj)
					if err != nil {
						klog.Errorf("Failed to convert unstructured object to Role: %v", err)
						continue
					}

					capiRbacWatcher.ProcessCAPIRole(role)
				}

				existingCAPIClusterRoles := capiRbacWatcher.CAPIClusterRoleInformer.GetStore().List()

				for _, obj := range existingCAPIClusterRoles {

					clusterRole, err := crd.ConvertUnstructured[crd.CAPIClusterRole](obj)
					if err != nil {
						klog.Errorf("Failed to convert unstructured object to ClusterRole: %v", err)
						continue
					}

					capiRbacWatcher.ProcessCAPIClusterRole(clusterRole)
				}

				existingCAPIClusterRoleBindings := capiRbacWatcher.CAPIClusterRoleBindingInformer.GetStore().List()

				for _, obj := range existingCAPIClusterRoleBindings {

					clusterRoleBinding, err := crd.ConvertUnstructured[crd.CAPIClusterRoleBinding](obj)
					if err != nil {
						klog.Errorf("Failed to convert unstructured object to ClusterRoleBinding: %v", err)
						continue
					}

					capiRbacWatcher.ProcessCAPIClusterRoleBinding(clusterRoleBinding)
				}

				existingCAPIRoleBindings := capiRbacWatcher.CAPIRoleBindingInformer.GetStore().List()

				for _, obj := range existingCAPIRoleBindings {

					roleBinding, err := crd.ConvertUnstructured[crd.CAPIRoleBinding](obj)
					if err != nil {
						klog.Errorf("Failed to convert unstructured object to RoleBinding: %v", err)
						continue
					}

					capiRbacWatcher.ProcessCAPIRoleBinding(roleBinding)
				}

				capiRbacWatcher.RebuildAllAuthorizers()
			}

			// Initialise proxy with OIDC token authenticator
			p, err := proxy.New(clustersConfig, opts.OIDCAuthentication, opts.Audit,
				secureServingInfo, proxyConfig)
			if err != nil {
				return err
			}

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

func LoadClusterConfig(path string) ([]*proxy.ClusterConfig, error) {
	var clusterList []*proxy.ClusterConfig
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
		clusterList = append(clusterList, &proxy.ClusterConfig{
			Name: cluster.Name,
			Path: cluster.Kubeconfig,
		})
	}
	return clusterList, nil
}

func clusterConfigValidation(clusterConfig []*proxy.ClusterConfig) error {
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
