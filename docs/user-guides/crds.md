# Custom Resource Definitions (CRDs) Documentation

This document provides comprehensive guide for the Custom Resource Definitions (CRDs) used by kube-oidc-proxy to manage RBAC across multiple clusters and namespaces.

## Overview

The kube-oidc-proxy project introduces four custom resources that extend Kubernetes RBAC capabilities for multi-cluster environments:

- **CAPIRole**: Namespace-scoped roles with multi-cluster support
- **CAPIClusterRole**: Cluster-scoped roles with multi-cluster support  
- **CAPIRoleBinding**: Namespace-scoped role bindings with multi-cluster support
- **CAPIClusterRoleBinding**: Cluster-scoped role bindings with multi-cluster support

These CRDs solve the challenge of managing consistent RBAC policies across multiple Kubernetes clusters while providing fine-grained control over permissions at both namespace and cluster levels.

## Table of Contents

- [CAPIRole](#capirole)
- [CAPIClusterRole](#capiclusterrole)
- [CAPIRoleBinding](#capirolebinding)
- [CAPIClusterRoleBinding](#capiclusterrolebinding)
- [Best Practices](#best-practices)
- [Troubleshooting](#troubleshooting)

---

## CAPIRole

### Definition

`CAPIRole` is a namespace-scoped custom resource that defines a set of permissions (rules) that can be applied across multiple clusters and specific namespaces. It extends the standard Kubernetes Role concept to work in multi-cluster environments.

### Purpose

CAPIRole solves the problem of managing consistent namespace-level permissions across multiple Kubernetes clusters. Instead of creating and maintaining separate Role objects in each cluster and namespace, administrators can define a single CAPIRole that specifies:

- The permission rules (similar to standard Kubernetes Role)
- Which clusters the role should be applied to
- Which namespaces within those clusters should receive the role

### Schema

```yaml
apiVersion: rbac.platformengineers.io/v1
kind: CAPIRole
metadata:
  name: <role-name>
  namespace: <namespace>
spec:
  rules:                    # Array of PolicyRule objects
    - apiGroups: []         # API groups (e.g., "", "apps", "extensions")
      resources: []         # Resources (e.g., "pods", "deployments")
      verbs: []            # Verbs (e.g., "get", "list", "create")
      resourceNames: []     # Optional: specific resource names
      nonResourceURLs: []   # Optional: non-resource URLs
  targetClusters: []        # Clusters where this role should be applied
  targetNamespaces: []      # Namespaces where this role should be applied
status:
  conditions: []            # Status conditions
```

### Usage Examples

#### Example 1: Developer Role for Pod Management

```yaml
apiVersion: rbac.platformengineers.io/v1
kind: CAPIRole
metadata:
  name: pod-reader
  namespace: development
spec:
  rules:
    - apiGroups: [""]
      resources: ["pods", "pods/log"]
      verbs: ["get", "list", "watch"]
    - apiGroups: [""]
      resources: ["pods/exec"]
      verbs: ["create"]
  targetClusters: ["dev-cluster", "staging-cluster"]
  targetNamespaces: ["frontend", "backend", "api"]
```

#### Example 2: Deployment Manager Role

```yaml
apiVersion: rbac.platformengineers.io/v1
kind: CAPIRole
metadata:
  name: deployment-manager
  namespace: production
spec:
  rules:
    - apiGroups: ["apps"]
      resources: ["deployments", "replicasets"]
      verbs: ["get", "list", "watch", "create", "update", "patch"]
    - apiGroups: [""]
      resources: ["pods"]
      verbs: ["get", "list", "watch"]
  targetClusters: ["prod-cluster-1", "prod-cluster-2"]
  targetNamespaces: ["web-services", "microservices"]
```

### Step-by-Step Creation Guide

1. **Define the Role Requirements**
   - Identify the specific permissions needed
   - Determine target clusters and namespaces
   - Choose appropriate API groups, resources, and verbs

2. **Create the YAML Manifest**
   ```bash
   cat > capirole-example.yaml << EOF
   apiVersion: rbac.platformengineers.io/v1
   kind: CAPIRole
   metadata:
     name: my-custom-role
     namespace: my-namespace
   spec:
     rules:
       - apiGroups: [""]
         resources: ["configmaps", "secrets"]
         verbs: ["get", "list"]
     targetClusters: ["cluster-1", "cluster-2"]
     targetNamespaces: ["app-namespace"]
   EOF
   ```

3. **Apply the Resource**
   ```bash
   kubectl apply -f capirole-example.yaml
   ```

4. **Verify Creation**
   ```bash
   kubectl get capiroles -n my-namespace
   kubectl describe capirole my-custom-role -n my-namespace
   ```

---

## CAPIClusterRole

### Definition

`CAPIClusterRole` is a cluster-scoped custom resource that defines cluster-wide permissions that can be applied across multiple clusters. It extends the standard Kubernetes ClusterRole concept for multi-cluster environments.

### Purpose

CAPIClusterRole addresses the challenge of managing cluster-wide permissions across multiple Kubernetes clusters. It allows administrators to:

- Define cluster-level permissions once
- Apply them consistently across multiple clusters
- Manage non-namespaced resources (nodes, persistent volumes, etc.)
- Control access to cluster-scoped APIs

### Schema

```yaml
apiVersion: rbac.platformengineers.io/v1
kind: CAPIClusterRole
metadata:
  name: <role-name>
spec:
  rules:                    # Array of PolicyRule objects
    - apiGroups: []         # API groups
      resources: []         # Resources
      verbs: []            # Verbs
      resourceNames: []     # Optional: specific resource names
      nonResourceURLs: []   # Optional: non-resource URLs
  targetClusters: []        # Clusters where this role should be applied
status:
  conditions: []            # Status conditions
```

### Usage Examples

#### Example 1: Node Reader Role

```yaml
apiVersion: rbac.platformengineers.io/v1
kind: CAPIClusterRole
metadata:
  name: node-reader
spec:
  rules:
    - apiGroups: [""]
      resources: ["nodes", "nodes/status"]
      verbs: ["get", "list", "watch"]
    - apiGroups: ["metrics.k8s.io"]
      resources: ["nodes", "pods"]
      verbs: ["get", "list"]
  targetClusters: ["*"]  # Apply to all clusters
```

#### Example 2: Cluster Administrator Role

```yaml
apiVersion: rbac.platformengineers.io/v1
kind: CAPIClusterRole
metadata:
  name: cluster-admin
spec:
  rules:
    - apiGroups: ["*"]
      resources: ["*"]
      verbs: ["*"]
    - nonResourceURLs: ["*"]
      verbs: ["*"]
  targetClusters: ["prod-cluster-1", "prod-cluster-2"]
```

#### Example 3: PersistentVolume Manager

```yaml
apiVersion: rbac.platformengineers.io/v1
kind: CAPIClusterRole
metadata:
  name: pv-manager
spec:
  rules:
    - apiGroups: [""]
      resources: ["persistentvolumes", "persistentvolumeclaims"]
      verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
    - apiGroups: ["storage.k8s.io"]
      resources: ["storageclasses"]
      verbs: ["get", "list", "watch"]
  targetClusters: ["storage-cluster-1", "storage-cluster-2"]
```

---

## CAPIRoleBinding

### Definition

`CAPIRoleBinding` is a namespace-scoped custom resource that binds a CAPIRole to subjects (users, groups, or service accounts) across multiple clusters and namespaces.

### Purpose

CAPIRoleBinding solves the complexity of managing role assignments across multiple clusters and namespaces by:

- Binding roles to subjects consistently across clusters
- Supporting multiple target clusters and namespaces
- Providing centralized management of role assignments
- Enabling fine-grained access control in multi-tenant environments

### Schema

```yaml
apiVersion: rbac.platformengineers.io/v1
kind: CAPIRoleBinding
metadata:
  name: <binding-name>
  namespace: <namespace>
spec:
  roleRef: []               # Array of role names to bind
  subjects:                 # Array of subjects
    - user: <username>      # User subject
    - group: <groupname>    # Group subject  
    - serviceAccount: <sa>  # ServiceAccount subject
  targetClusters: []        # Clusters where binding should be applied
  targetNamespaces: []      # Namespaces where binding should be applied
status:
  conditions: []            # Status conditions
```

### Usage Examples

#### Example 1: Developer Team Binding

```yaml
apiVersion: rbac.platformengineers.io/v1
kind: CAPIRoleBinding
metadata:
  name: dev-team-binding
  namespace: development
spec:
  roleRef:
    - pod-reader
    - deployment-manager
  subjects:
    - user: alice@company.com
    - user: bob@company.com
    - group: developers
  targetClusters: ["dev-cluster", "staging-cluster"]
  targetNamespaces: ["frontend", "backend"]
```

#### Example 2: Service Account Binding

```yaml
apiVersion: rbac.platformengineers.io/v1
kind: CAPIRoleBinding
metadata:
  name: ci-cd-binding
  namespace: ci-cd
spec:
  roleRef:
    - deployment-manager
  subjects:
    - serviceAccount: jenkins-sa
    - serviceAccount: gitlab-runner
  targetClusters: ["staging-cluster", "prod-cluster"]
  targetNamespaces: ["applications"]
```

---

## CAPIClusterRoleBinding

### Definition

`CAPIClusterRoleBinding` is a cluster-scoped custom resource that binds a CAPIClusterRole to subjects across multiple clusters at the cluster level.

### Purpose

CAPIClusterRoleBinding enables cluster-wide role assignments across multiple clusters by:

- Providing cluster-level access control across multiple clusters
- Supporting centralized management of cluster administrators
- Enabling consistent cluster-wide permissions
- Simplifying multi-cluster RBAC management

### Schema

```yaml
apiVersion: rbac.platformengineers.io/v1
kind: CAPIClusterRoleBinding
metadata:
  name: <binding-name>
spec:
  roleRef: []               # Array of cluster role names to bind
  subjects:                 # Array of subjects
    - user: <username>      # User subject
    - group: <groupname>    # Group subject
    - serviceAccount: <sa>  # ServiceAccount subject
  targetClusters: []        # Clusters where binding should be applied
status:
  conditions: []            # Status conditions
```

### Usage Examples

#### Example 1: Platform Team Cluster Access

```yaml
apiVersion: rbac.platformengineers.io/v1
kind: CAPIClusterRoleBinding
metadata:
  name: platform-team-cluster-access
spec:
  roleRef:
    - cluster-admin
  subjects:
    - user: admin@company.com
    - group: platform-engineers
  targetClusters: ["*"]  # All clusters
```

#### Example 2: Monitoring Service Binding

```yaml
apiVersion: rbac.platformengineers.io/v1
kind: CAPIClusterRoleBinding
metadata:
  name: monitoring-cluster-binding
spec:
  roleRef:
    - node-reader
    - pv-manager
  subjects:
    - serviceAccount: prometheus
    - serviceAccount: grafana
  targetClusters: ["prod-cluster-1", "prod-cluster-2", "staging-cluster"]
```

---

## Best Practices

### 1. Naming Conventions

- Use descriptive names that indicate the role's purpose
- Include environment or team prefixes when appropriate
- Follow kebab-case naming convention

```yaml
# Good examples
name: frontend-developer
name: prod-deployment-manager
name: monitoring-cluster-reader

# Avoid
name: role1
name: MyRole
name: temp_role
```

### 2. Principle of Least Privilege

Always grant the minimum permissions necessary:

```yaml
# Good: Specific permissions
rules:
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get", "list"]

# Avoid: Overly broad permissions
rules:
  - apiGroups: ["*"]
    resources: ["*"]
    verbs: ["*"]
```

### 3. Target Cluster Management

- Use specific cluster names instead of wildcards when possible
- Document cluster naming conventions
- Regularly audit cluster targets

```yaml
# Preferred: Explicit cluster targeting
targetClusters: ["prod-us-east", "prod-eu-west"]

# Use sparingly: Wildcard targeting
targetClusters: ["*"]
```

### 4. Documentation and Labels

Add meaningful labels and annotations:

```yaml
metadata:
  name: api-developer
  labels:
    team: backend
    environment: development
    managed-by: platform-team
  annotations:
    description: "Allows developers to manage API deployments"
    contact: "backend-team@company.com"
```

### 5. Regular Auditing

- Periodically review role assignments
- Remove unused roles and bindings
- Monitor for privilege escalation
- Use status conditions to track deployment state

---

## Troubleshooting

### Common Issues

#### 1. Role Not Applied to Target Clusters

**Symptoms**: Role exists but permissions not working in target clusters

**Solutions**:
- Verify cluster names in `targetClusters` match actual cluster names
- Check kube-oidc-proxy logs for synchronization errors
- Ensure target clusters are accessible

#### 2. Permission Denied Errors

**Symptoms**: Users getting permission denied despite role binding

**Solutions**:
- Verify role binding subjects match user/group names from OIDC provider
- Check if target namespaces exist in target clusters
- Validate role rules are correctly defined


### Debugging Commands

```bash
# List all CAPIRoles
kubectl get capiroles --all-namespaces

# Describe specific role
kubectl describe capirole <role-name> -n <namespace>

# Check role binding status
kubectl get capirolebindings --all-namespaces

# View controller logs
kubectl logs -n kube-oidc-proxy deployment/kube-oidc-proxy

# Validate CRD installation
kubectl get crd | grep rbac.platformengineers.io
```

### Validation

Before applying CRDs, validate your YAML:

```bash
# Dry run to check syntax
kubectl apply --dry-run=client -f your-crd.yaml

# Validate against schema
kubectl apply --validate=true -f your-crd.yaml
```

---

## Migration Guide

### From Standard Kubernetes RBAC

If migrating from standard Kubernetes Role/RoleBinding:

1. **Identify existing roles and bindings**
   ```bash
   kubectl get roles,rolebindings --all-namespaces
   ```

2. **Convert to CAPIRole format**
   ```yaml
   # Standard Role
   apiVersion: rbac.authorization.k8s.io/v1
   kind: Role
   metadata:
     name: pod-reader
   rules:
     - apiGroups: [""]
       resources: ["pods"]
       verbs: ["get", "list"]
   
   # Converted to CAPIRole
   apiVersion: rbac.platformengineers.io/v1
   kind: CAPIRole
   metadata:
     name: pod-reader
   spec:
     rules:
       - apiGroups: [""]
         resources: ["pods"]
         verbs: ["get", "list"]
     targetClusters: ["current-cluster"]
     targetNamespaces: ["current-namespace"]
   ```

3. **Test in non-production environment first**
4. **Gradually migrate role by role**
5. **Clean up old RBAC objects after validation**

---

## Additional Resources

- [Kubernetes RBAC Documentation](https://kubernetes.io/docs/reference/access-authn-authz/rbac/)
- [kube-oidc-proxy Configuration Guide](../README.md)
- [Multi-cluster Setup Guide](../setup/multi-cluster.md)