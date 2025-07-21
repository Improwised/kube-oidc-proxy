// Copyright Jetstack Ltd. See LICENSE for details.
package app

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"k8s.io/apiserver/pkg/server"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	"github.com/Improwised/kube-oidc-proxy/cmd/app/options"
	"github.com/Improwised/kube-oidc-proxy/pkg/cluster"
	"github.com/Improwised/kube-oidc-proxy/pkg/clustermanager"
	"github.com/Improwised/kube-oidc-proxy/pkg/probe"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy"
	"github.com/Improwised/kube-oidc-proxy/pkg/proxy/crd"
	"github.com/Improwised/kube-oidc-proxy/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NewRunCommand creates and returns the main cobra command for running the proxy
func NewRunCommand(stopCh <-chan struct{}) *cobra.Command {
	// Initialize configuration options
	opts := options.New()

	// Build the run command with provided options
	cmd := buildRunCommand(stopCh, opts)

	// Add command line flags from options
	opts.AddFlags(cmd)

	return cmd
}

// buildRunCommand constructs the main proxy command with execution logic
func buildRunCommand(stopCh <-chan struct{}, opts *options.Options) *cobra.Command {
	return &cobra.Command{
		Use:  options.AppName,
		Long: "kube-oidc-proxy is a reverse proxy to authenticate users to Kubernetes API servers with Open ID Connect Authentication.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate command line options
			if err := opts.Validate(cmd); err != nil {
				return fmt.Errorf("options validation failed: %w", err)
			}

			// Verify cluster configuration file exists
			if _, err := os.Stat(opts.App.Cluster.Config); err != nil {
				return fmt.Errorf("cluster config file not found: %w", err)
			}

			// Load and parse cluster configuration
			clusterConfigs, err := LoadClusterConfig(opts.App.Cluster.Config)
			if err != nil {
				return fmt.Errorf("failed to load cluster config: %w", err)
			}

			// Validate cluster configuration
			if err := validateClusterConfig(clusterConfigs); err != nil {
				return fmt.Errorf("invalid cluster configuration: %w", err)
			}

			var clusterRBACConfigs map[string]util.RBAC
			if opts.App.Cluster.RoleConfig != "" {
				// Check if RBAC configuration file exists
				if _, err := os.Stat(opts.App.Cluster.RoleConfig); err != nil {
					return fmt.Errorf("RBAC config file not found: %w", err)
				}

				// Load RBAC role configurations
				clusterRBACConfigs, err = util.LoadRBACConfig(opts.App.Cluster.RoleConfig)
				if err != nil {
					return fmt.Errorf("failed to load RBAC config: %w", err)
				}
			}

			// Initialize CAPI RBAC watcher if available
			capiRBACWatcher, err := crd.NewCAPIRbacWatcher(clusterConfigs)
			if err != nil {
				klog.Errorf("Failed to initialize CAPI RBAC watcher: %v", err)
				capiRBACWatcher = nil // Continue without watcher if initialization fails
			}

			// Create cluster manager to handle dynamic clusters
			clusterManager, err := clustermanager.NewClusterManager(
				stopCh,
				opts.App.TokenPassthrough.Enabled,
				opts.App.TokenPassthrough.Audiences,
				clusterRBACConfigs,
				capiRBACWatcher,
			)
			if err != nil {
				return fmt.Errorf("failed to create cluster manager: %w", err)
			}

			// Initialize each static cluster
			for _, cluster := range clusterConfigs {
				configFlags := &genericclioptions.ConfigFlags{
					KubeConfig: &cluster.Path,
				}
				clientOptions := &options.ClientOptions{
					ConfigFlags: configFlags,
				}

				// Create REST config for the cluster
				restConfig, err := clientOptions.ToRESTConfig()
				if err != nil {
					return fmt.Errorf("failed to create REST config for cluster %s: %w", cluster.Name, err)
				}
				cluster.RestConfig = restConfig

				// Set up the cluster in the manager
				if err := clusterManager.ClusterSetup(cluster); err != nil {
					return fmt.Errorf("failed to setup cluster %s: %w", cluster.Name, err)
				}
				cluster.IsStatic = true // Mark as statically configured
				clusterManager.AddOrUpdateCluster(cluster)
			}

			// Start CAPI RBAC watcher if available
			if capiRBACWatcher != nil {
				klog.V(5).Info("Starting CAPI RBAC watcher")
				capiRBACWatcher.Start(stopCh)
				capiRBACWatcher.ProcessExistingRBACObjects()
			}

			// Configure secure serving for the proxy
			secureServingInfo := new(server.SecureServingInfo)
			if err := opts.SecureServing.ApplyTo(&secureServingInfo); err != nil {
				return fmt.Errorf("failed to configure secure serving: %w", err)
			}

			// Create proxy configuration
			proxyConfig := &proxy.Config{
				TokenReview:                     opts.App.TokenPassthrough.Enabled,
				DisableImpersonation:            opts.App.DisableImpersonation,
				FlushInterval:                   opts.App.FlushInterval,
				ExternalAddress:                 opts.SecureServing.BindAddress.String(),
				ExtraUserHeaders:                opts.App.ExtraHeaderOptions.ExtraUserHeaders,
				ExtraUserHeadersClientIPEnabled: opts.App.ExtraHeaderOptions.EnableClientIPExtraUserHeader,
			}

			// Initialize the proxy with OIDC authentication
			proxyInstance, err := proxy.New(
				opts.OIDCAuthentication,
				opts.Audit,
				secureServingInfo,
				proxyConfig,
				clusterManager,
			)
			if err != nil {
				return fmt.Errorf("failed to initialize proxy: %w", err)
			}

			// Configure cluster manager to use proxy for dynamic clusters
			clusterManager.SetupFunc = proxyInstance.SetupClusterProxy

			// Start watching for dynamic clusters
			if opts.SecretNamespace == "" {
				opts.SecretNamespace = getCurrentNamespace()
			}
			go clusterManager.WatchDynamicClusters(opts.SecretNamespace, opts.SecretName)

			// Generate fake JWT for readiness probe
			fakeJWT, err := util.FakeJWT(opts.OIDCAuthentication.IssuerURL)
			if err != nil {
				return fmt.Errorf("failed to generate fake JWT: %w", err)
			}

			// Start readiness probe server
			if err := probe.Run(
				strconv.Itoa(opts.App.ReadinessProbePort),
				fakeJWT,
				proxyInstance.OIDCTokenAuthenticator(),
			); err != nil {
				return fmt.Errorf("failed to start readiness probe: %w", err)
			}

			// Run the proxy and wait for shutdown signals
			waitCh, listenerStoppedCh, err := proxyInstance.Run(stopCh)
			if err != nil {
				return fmt.Errorf("proxy run failed: %w", err)
			}

			// Wait for shutdown signals
			<-waitCh
			<-listenerStoppedCh

			// Execute pre-shutdown hooks
			if err := proxyInstance.RunPreShutdownHooks(); err != nil {
				return fmt.Errorf("pre-shutdown hooks failed: %w", err)
			}

			return nil
		},
	}
}

// LoadClusterConfig loads and parses the cluster configuration from YAML file
func LoadClusterConfig(path string) ([]*cluster.Cluster, error) {
	var clustersList []*cluster.Cluster
	var config struct {
		Clusters []struct {
			Name       string `yaml:"name"`
			Kubeconfig string `yaml:"kubeconfig"`
		} `yaml:"clusters"`
	}

	// Read configuration file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Parse YAML configuration
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse YAML config: %w", err)
	}

	// Convert configuration to cluster models
	for _, clusterConfig := range config.Clusters {
		clustersList = append(clustersList, &cluster.Cluster{
			Name: clusterConfig.Name,
			Path: clusterConfig.Kubeconfig,
		})
	}

	return clustersList, nil
}

// validateClusterConfig checks for basic configuration validity
func validateClusterConfig(clusterConfig []*cluster.Cluster) error {
	clusterNames := make(map[string]bool)

	for _, cluster := range clusterConfig {
		// Check for empty cluster name
		if cluster.Name == "" {
			return fmt.Errorf("cluster name cannot be empty")
		}

		// Check for duplicate cluster names
		if _, exists := clusterNames[cluster.Name]; exists {
			return fmt.Errorf("duplicate cluster name: %s", cluster.Name)
		}
		clusterNames[cluster.Name] = true
	}

	return nil
}

func getCurrentNamespace() string {
	ns := "kube-oidc-proxy" //set namespace to kube-oidc-proxy as conventional assumtion

	config, err := rest.InClusterConfig()
	if err != nil {
		klog.Errorf("failed to get incluster config: %v", err)
		return ns
	}

	// Create Kubernetes client
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Errorf("failed to create clientset: %v", err)
		return ns
	}

	labelSelector := fmt.Sprintf("app.kubernetes.io/component=%s,app.kubernetes.io/instance=%s", options.AppName, options.AppName)

	// List Services across all namespaces
	services, err := clientset.CoreV1().Services("").List(context.TODO(), metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		klog.Errorf("error listing services: %v", err)
		return ns
	}

	// Print matching services
	for _, svc := range services.Items {
		return svc.Namespace
	}

	return ns
}
