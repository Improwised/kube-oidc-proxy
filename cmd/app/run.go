// Copyright Jetstack Ltd. See LICENSE for details.
package app

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"

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

			// Load and parse cluster configuration
			clusterConfigs, err := LoadClusterConfig(opts.App.Cluster.Config)
			if err != nil {
				klog.Warningf("failed to load cluster config: %v", err.Error())
			}

			var clusterRBACConfigs map[string]util.RBAC
			if opts.App.Cluster.RoleConfig != "" {
				// Check if RBAC configuration file exists
				if _, err := os.Stat(opts.App.Cluster.RoleConfig); err == nil {
					// Load RBAC role configurations
					clusterRBACConfigs, err = util.LoadRBACConfig(opts.App.Cluster.RoleConfig)
					if err != nil {
						klog.Errorf("failed to load RBAC config: %v", err.Error())
					}
				} else {
					klog.Errorf("RBAC config file not found: %v", err.Error())
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
				opts.App.MaxGoroutines,
			)
			if err != nil {
				return fmt.Errorf("failed to create cluster manager: %w", err)
			}

			// Initialize each static cluster
			initStaticClusters(clusterConfigs, clusterManager, opts.App.MaxGoroutines)

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

			// Start watching for dynamic clusters using the new controller pattern
			if opts.SecretNamespace == "" {
				opts.SecretNamespace = getCurrentNamespace()
			}

			// Create context from stopCh for the secret controller
			ctx, cancel := context.WithCancel(context.Background())
			go func() {
				<-stopCh
				cancel()
			}()

			// Start the secret controller with proper controller pattern
			if err := clusterManager.StartSecretController(ctx, opts.SecretNamespace, opts.SecretName, 1); err != nil {
				klog.Errorf("failed to start secret controller: %v", err.Error())
			}

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
	// Verify cluster configuration file exists
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("cluster config file not found: %w", err)
	}

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
	clusterNames := make(map[string]bool)

	for _, clusterConfig := range config.Clusters {
		if clusterConfig.Name == "" {
			klog.Warningf("found empty cluster name, skipping that cluster")
			continue
		}
		if _, exists := clusterNames[clusterConfig.Name]; exists {
			klog.Warningf("duplicate cluster name: %s, skipping this cluster", clusterConfig.Name)
			continue
		}

		clustersList = append(clustersList, &cluster.Cluster{
			Name: clusterConfig.Name,
			Path: clusterConfig.Kubeconfig,
		})
		clusterNames[clusterConfig.Name] = true
	}

	return clustersList, nil
}

// getCurrentNamespace determines the current Kubernetes namespace by looking for
// services with the kube-oidc-proxy label selector, defaults to "kube-oidc-proxy"
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

// initStaticClusters initializes all static clusters concurrently with goroutine
// limiting to prevent overwhelming the system with parallel cluster setups
func initStaticClusters(clusterConfigs []*cluster.Cluster, clusterManager *clustermanager.ClusterManager, maxGoroutines int) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxGoroutines)

	for _, c := range clusterConfigs {
		wg.Add(1)
		go func(c *cluster.Cluster) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			configFlags := &genericclioptions.ConfigFlags{
				KubeConfig: &c.Path,
			}
			clientOptions := &options.ClientOptions{
				ConfigFlags: configFlags,
			}

			// Create REST config for the cluster
			restConfig, err := clientOptions.ToRESTConfig()
			if err != nil {
				klog.Warningf("failed to create REST config for cluster %s: %v", c.Name, err)
				return
			}
			c.RestConfig = restConfig

			// Set up the cluster in the manager
			if err := clusterManager.ClusterSetup(c); err != nil {
				klog.Warningf("failed to setup cluster %s: %v", c.Name, err)
				return
			}
			c.IsStatic = true // Mark as statically configured
			clusterManager.AddOrUpdateCluster(c)
		}(c)
	}
	wg.Wait()

}
