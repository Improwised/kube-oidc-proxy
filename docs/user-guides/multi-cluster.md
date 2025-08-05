# Multi-Cluster Support in kube-oidc-proxy

This document provides comprehensive guide for configuring and managing multiple Kubernetes clusters with kube-oidc-proxy.

## Table of Contents

- [Overview](#overview)
- [How Multi-Cluster Support Works](#how-multi-cluster-support-works)
- [Cluster URL Format](#cluster-url-format)
- [Configuration Methods](#configuration-methods)
  - [Static Configuration Method](#static-configuration-method)
  - [Dynamic Configuration Method](#dynamic-configuration-method)
- [Comparison of Methods](#comparison-of-methods)
- [Troubleshooting](#troubleshooting)
- [Advanced Configuration](#advanced-configuration)

## Overview

kube-oidc-proxy supports managing multiple Kubernetes clusters through a single proxy instance. This feature allows organizations to:

- Centralize authentication and authorization for multiple clusters
- Provide a unified entry point for developers and operators
- Simplify OIDC integration across your Kubernetes estate
- Apply consistent access policies across environments

## How Multi-Cluster Support Works

The proxy acts as an intermediary between users and multiple Kubernetes API servers:

1. Users send requests to the proxy with a URL that includes the target cluster name
2. The proxy authenticates the user via OIDC
3. The proxy identifies the target cluster from the URL path
4. The proxy forwards the authenticated request to the appropriate Kubernetes API server
5. The proxy returns the API server's response to the user

## Cluster URL Format

To target a specific cluster, include the cluster name in the request URL:

```
https://<proxy-address>:<proxy-port>/<cluster-name>/api/...
```

For example, to access a cluster named "production":

```
https://k8s-proxy.example.com:8443/production/api/v1/namespaces
```

This cluster name must match one of the configured clusters in the proxy's configuration.

## Configuration Methods

kube-oidc-proxy offers two methods for configuring multiple clusters:

1. **Static Configuration**: Using a YAML file specified via command-line flag
2. **Dynamic Configuration**: Using Kubernetes Secrets for runtime management

### Static Configuration Method

The static configuration method uses a YAML file to define all clusters at startup time.

#### How It Works

1. Create a YAML configuration file listing all clusters and their kubeconfig paths
2. Start kube-oidc-proxy with the `--clusters-config` flag pointing to this file
3. The proxy loads all cluster configurations at startup
4. Cluster configurations remain fixed until the proxy is restarted with a new config

#### Configuration File Format

The configuration file follows this structure:

```yaml
clusters:
  - name: <cluster-name>
    kubeconfig: <path-to-kubeconfig-file>
  - name: <cluster-name>
    kubeconfig: <path-to-kubeconfig-file>
  # Additional clusters...
```

#### Example Configuration File

```yaml
clusters:
  - name: production
    kubeconfig: "/etc/kube-oidc-proxy/kubeconfigs/production.yaml"
  - name: staging
    kubeconfig: "/etc/kube-oidc-proxy/kubeconfigs/staging.yaml"
  - name: development
    kubeconfig: "/etc/kube-oidc-proxy/kubeconfigs/development.yaml"
```

#### Step-by-Step Setup Instructions

1. **Prepare kubeconfig files** for each cluster:
   
   Each kubeconfig file should contain valid credentials for accessing the target cluster. These can be service account tokens, client certificates, or other authentication methods supported by Kubernetes.

2. **Create the clusters configuration file**:

   ```bash
   cat > clusters-config.yaml << EOF
   clusters:
     - name: production
       kubeconfig: "/etc/kube-oidc-proxy/kubeconfigs/production.yaml"
     - name: staging
       kubeconfig: "/etc/kube-oidc-proxy/kubeconfigs/staging.yaml"
     - name: development
       kubeconfig: "/etc/kube-oidc-proxy/kubeconfigs/development.yaml"
   EOF
   ```

3. **Start kube-oidc-proxy with the configuration**:

   ```bash
   kube-oidc-proxy \
     --clusters-config=/path/to/clusters-config.yaml \
     --oidc-client-id=my-client \
     --oidc-issuer-url=https://keycloak.example.com/realms/kubernetes \
     --oidc-username-claim=email \
     --secure-port=8443
   ```

4. **Verify configuration**:

   Test access to each cluster:

   ```bash
   curl -k https://localhost:8443/production/api/v1/namespaces
   curl -k https://localhost:8443/staging/api/v1/namespaces
   curl -k https://localhost:8443/development/api/v1/namespaces
   ```

#### When to Use Static Configuration

Use static configuration when:
- You have a fixed set of clusters that rarely changes
- You're running kube-oidc-proxy outside of Kubernetes
- You want a simple, file-based configuration approach
- You have a robust configuration management system in place

### Dynamic Configuration Method

The dynamic configuration method uses a single Kubernetes Secret to manage multiple cluster configurations at runtime.

#### How It Works

1. Create a Kubernetes Secret containing multiple cluster configurations
2. Start kube-oidc-proxy with flags specifying the secret namespace and name
3. The proxy loads initial configurations from the secret at startup
4. As the secret is updated, the proxy dynamically updates its cluster configurations

#### Secret Structure

The secret must contain multiple keys, where each key represents a cluster name and its value is the base64-encoded kubeconfig for that cluster:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: kube-oidc-proxy-kubeconfigs
  namespace: kube-oidc-proxy
type: Opaque
data:
  production: <base64-encoded-kubeconfig-for-production>
  staging: <base64-encoded-kubeconfig-for-staging>
  development: <base64-encoded-kubeconfig-for-development>
```

#### Step-by-Step Setup Instructions

1. **Prepare kubeconfig files** for each cluster:

   Create a kubeconfig file with appropriate credentials for each target cluster.

2. **Base64 encode each kubeconfig**:

   ```bash
   PRODUCTION_KUBECONFIG_B64=$(cat /path/to/production-kubeconfig.yaml | base64 -w 0)
   STAGING_KUBECONFIG_B64=$(cat /path/to/staging-kubeconfig.yaml | base64 -w 0)
   DEVELOPMENT_KUBECONFIG_B64=$(cat /path/to/development-kubeconfig.yaml | base64 -w 0)
   ```

3. **Create a Secret manifest**:

   ```bash
   cat > multi-cluster-secret.yaml << EOF
   apiVersion: v1
   kind: Secret
   metadata:
     name: kube-oidc-proxy-kubeconfigs
     namespace: kube-oidc-proxy
   type: Opaque
   data:
     production: ${PRODUCTION_KUBECONFIG_B64}
     staging: ${STAGING_KUBECONFIG_B64}
     development: ${DEVELOPMENT_KUBECONFIG_B64}
   EOF
   ```

4. **Apply the Secret**:

   ```bash
   kubectl apply -f multi-cluster-secret.yaml
   ```

5. **Start kube-oidc-proxy with secret watching enabled**:

   ```bash
   kube-oidc-proxy \
     --secret-namespace=kube-oidc-proxy \
     --secret-name=kube-oidc-proxy-kubeconfigs \
     --oidc-client-id=my-client \
     --oidc-issuer-url=https://keycloak.example.com/realms/kubernetes \
     --oidc-username-claim=email
   ```

6. **Verify the configuration**:

   ```bash
   curl -k https://localhost:8443/production/api/v1/namespaces
   curl -k https://localhost:8443/staging/api/v1/namespaces
   curl -k https://localhost:8443/development/api/v1/namespaces
   ```

#### Adding, Updating, or Removing Clusters

One of the key advantages of the dynamic configuration method is the ability to modify cluster configurations without restarting the proxy:

**Adding a new cluster**:
```bash
# Encode the new cluster's kubeconfig
NEW_CLUSTER_KUBECONFIG_B64=$(cat /path/to/new-cluster-kubeconfig.yaml | base64 -w 0)

# Add the new cluster to the existing secret
kubectl patch secret kube-oidc-proxy-kubeconfigs -n kube-oidc-proxy \
  -p "{\"data\":{\"new-cluster\":\"${NEW_CLUSTER_KUBECONFIG_B64}\"}}"
```

**Updating a cluster configuration**:
```bash
# Update an existing cluster with new kubeconfig
UPDATED_KUBECONFIG_B64=$(cat /path/to/updated-production-kubeconfig.yaml | base64 -w 0)

kubectl patch secret kube-oidc-proxy-kubeconfigs -n kube-oidc-proxy \
  -p "{\"data\":{\"production\":\"${UPDATED_KUBECONFIG_B64}\"}}"
```

**Removing a cluster**:
```bash
# Remove a cluster by deleting its key from the secret
kubectl patch secret kube-oidc-proxy-kubeconfigs -n kube-oidc-proxy \
  --type=json -p='[{"op": "remove", "path": "/data/staging"}]'
```

#### Command-Line Flags

The following command-line flags are used for the dynamic configuration method:

- `--secret-namespace`: The namespace where the cluster configuration secret is stored (default: `default`)
- `--secret-name`: The name of the secret containing cluster configurations (default: `kube-oidc-proxy-kubeconfigs`)

#### When to Use Dynamic Configuration

Use dynamic configuration when:
- You need to add or remove clusters frequently
- You're running kube-oidc-proxy inside Kubernetes
- You want to leverage GitOps workflows for cluster management
- You need to automate cluster configuration changes
- You have multiple teams managing different clusters
- You prefer centralized configuration management
## Comparison of Methods

| Feature | Static Configuration | Dynamic Configuration |
|---------|---------------------|----------------------|
| **Configuration Source** | YAML file | Kubernetes Secrets |
| **Runtime Changes** | Requires restart | Dynamic, no restart needed |
| **Environment** | Any (in or outside K8s) | Requires K8s API access |
| **Complexity** | Simple | Moderate |
| **Secret Management** | Manual | Integrated with K8s Secrets |
| **GitOps Compatible** | Yes (file-based) | Yes (K8s resource-based) |
| **Automation** | Limited | Extensive |
| **Use Case** | Static environments | Dynamic environments |

## Troubleshooting

### Common Issues

#### 1. Cluster Not Found

**Symptoms**:
- Error message: `cluster "<name>" not found`
- HTTP 404 response

**Possible Causes**:
- Cluster name in URL doesn't match any configured cluster
- Configuration file or secret has incorrect cluster name
- Secret doesn't have the required label

**Solutions**:
- Verify the cluster name in the URL matches a configured cluster
- Check configuration file or secrets for correct cluster names
- Ensure secrets have the `kube-oidc-proxy.io/cluster-config: "true"` label

#### 2. Authentication Failures

**Symptoms**:
- Error message: `unable to authenticate the request`
- HTTP 401 response

**Possible Causes**:
- OIDC configuration issues
- Token validation problems
- Missing or expired tokens

**Solutions**:
- Verify OIDC configuration (issuer URL, client ID, etc.)
- Check token validity and expiration
- Ensure proper authentication headers in requests

#### 3. Kubeconfig Issues

**Symptoms**:
- Error message: `failed to create client for cluster`
- HTTP 500 response when accessing a specific cluster

**Possible Causes**:
- Invalid kubeconfig format
- Expired or invalid credentials in kubeconfig
- Missing required kubeconfig fields

**Solutions**:
- Validate kubeconfig format and content
- Update credentials in kubeconfig
- Ensure kubeconfig has all required fields (server, auth info, etc.)

#### 4. Secret Watching Problems

**Symptoms**:
- New or updated secrets not reflected in available clusters
- Log messages about secret watch errors

**Possible Causes**:
- Insufficient RBAC permissions for the proxy
- Incorrect namespace or label selector configuration
- Kubernetes API connectivity issues

**Solutions**:
- Verify RBAC permissions for the proxy service account
- Check namespace and label selector configuration
- Ensure connectivity to the Kubernetes API

### Debugging Commands

```bash
# Check available clusters (static configuration)
kube-oidc-proxy --list-clusters --clusters-config=/path/to/config.yaml

# Check available clusters (dynamic configuration)
kubectl get secrets -n kube-oidc-proxy -l kube-oidc-proxy.io/cluster-config=true

# Verify secret content
kubectl get secret cluster-production -n kube-oidc-proxy -o jsonpath='{.data.name}' | base64 -d
kubectl get secret cluster-production -n kube-oidc-proxy -o jsonpath='{.data.kubeconfig}' | base64 -d

# Check proxy logs
kubectl logs -n kube-oidc-proxy deployment/kube-oidc-proxy

# Test connectivity to a cluster
curl -v -k -H "Authorization: Bearer <token>" https://localhost:8443/<cluster-name>/api/v1/namespaces
```