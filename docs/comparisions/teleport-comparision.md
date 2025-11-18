# Comparison: kube-oidc-proxy vs Teleport

Both tools provide secure access Kubernetes clusters, but they differ significantly in scope, architecture, and use cases.

## Quick Comparison

| Feature | kube-oidc-proxy | Teleport |
|---------|-----------------|----------|
| **Primary Focus** | Kubernetes API access only | Unified access (K8s, SSH, DBs, Apps) |
| **Architecture** | Single stateless proxy | Multi-component platform |
| **Private Cluster Access** | No | Yes |
| **Database Required** | No | Yes |
| **Access Protocols** | HTTPS only | K8s, SSH, Database, Web Apps |
| **RBAC Granularity** | Full Kubernetes RBAC | Custom RBAC + K8s RBAC |
| **Audit Logging** | Webhook-based | Built-in session recording |
| **Web UI** | Yes (via Headlamp plugin) | Comprehensive web UI |

## kube-oidc-proxy: Kubernetes-Focused

### What It Does
- Stateless reverse proxy for Kubernetes API
- Validates OIDC tokens and forwards to clusters
- Uses native Kubernetes RBAC via CRDs.

### When to Choose kube-oidc-proxy
- You only need Kubernetes API access
- All clusters are publicly accessible
- You want minimal infrastructure overhead
- Your team is comfortable with kubectl and K8s RBAC
- No database dependency preferred
- Cost-sensitive environment

## Teleport: Comprehensive Infrastructure Access Platform

### What It Does
- Unified access to Kubernetes, SSH, databases, and web apps
- Reverse tunneling for private resources
- Session recording and audit logging
- Comprehensive web UI and CLI

### Key Differences from kube-oidc-proxy
- Broader scope beyond just Kubernetes
- Handles private infrastructure access
- Built-in session recording capabilities
- More complex deployment and management
- Includes database for state management
- requires to create role and bindings in target cluster 
- authorizes request multiple times, via teleport k8s service and in target cluster as well. 

### Choose Teleport when
- You need more than just Kubernetes access (SSH, DBs, apps)
- Have private or hybrid infrastructure
- Session auditing and recording are required
- You need a comprehensive access platform

## Summary
- Use kube-oidc-proxy for simple Kubernetes API access to public clusters
- Use Teleport when you need broader infrastructure access, private cluster support, and advanced auditing