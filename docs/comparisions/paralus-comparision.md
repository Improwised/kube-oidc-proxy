# Comparison: kube-oidc-proxy vs Paralus

Both tools(kube-oidc-proxy and paralus) provide secure, centralized access to multiple Kubernetes clusters using your existing identity provider (OIDC/SAML).


## Quick Decision Guide

| Feature | kube-oidc-proxy | Paralus |
|---------|-----------------|---------|
| **Best For** | Public cloud clusters | Private/hybrid clusters |
| **Architecture** | Stateless proxy | Proxy + relay agents |
| **Private Cluster Access** | No | Yes |
| **Database Required** | No | Yes |
| **RBAC Granularity** | Resource-level | Namespace/Cluster-level |
| **Audit Logging** | Webhook-based | Built-in |
| **Web UI** | Yes(through headlamp plugin) | Yes(It's own web UI) |
| **Setup Complexity** | Low | Medium |

## kube-oidc-proxy: Simple & Lightweight

### How It Works
- Stateless reverse proxy sitting between users and Kubernetes API
- Validates OIDC tokens and forwards requests to clusters
- Uses native Kubernetes RBAC for authorization

### Key Features
- No database or persistent storage
- Full Kubernetes RBAC support
- Horizontal scaling
- Works with any OIDC provider
- Custom audit via webhooks
- UI provided as Headlamp Plugin

### Ideal For
- Public cloud clusters (GKE, EKS, AKS)
- Teams comfortable with Kubernetes RBAC
- Environments wanting minimal components
- Cost-sensitive deployments

## Paralus

### How It Works
- Central server with in-cluster relay agents
- Agents create secure tunnels to private clusters
- Built-in user management and audit logging
- authorization is done by target cluster for request based on roles and bindngs created in target cluster

### Key Features
- Access to private/on-premises clusters
- Built-in audit logging
- Web UI for user management
- Automatic user provisioning from IDP

### Ideal For
- Hybrid or private cloud environments
- Organizations needing Web UI for non-technical users

## When to Choose Which?

### Choose kube-oidc-proxy if:
- All your clusters are in public cloud
- You need fine-grained, resource-level permissions
- You want minimal operational overhead
- Your team understands Kubernetes RBAC
- You prefer no database dependencies
- don't want to create any role and bindings in your target cluster.

### Choose Paralus if:
- You have private/on-premises clusters
- You want a Web UI for user management and don't want to use any external IDP
- You have non-technical users needing cluster access
- You need automatic user provisioning

## Key Differences

### Architecture
- **kube-oidc-proxy**: Single stateless component
- **Paralus**: Multiple components (server, agents, database)

### Access Scope
- **kube-oidc-proxy**: Public clusters only
- **Paralus**: Public + private clusters

### Permission Management
- **kube-oidc-proxy**: Full Kubernetes RBAC (resource-level)
- **Paralus**: Simplified roles (namespace/cluster-level)

### Operational Overhead
- **kube-oidc-proxy**: Low (no database, stateless)
- **Paralus**: Medium (database, agents, server)

## Final Recommendation
- Start with kube-oidc-proxy for public cloud simplicity

- Choose Paralus for private clusters

Both are open-source and actively maintained. Your choice depends on infrastructure and team requirements.