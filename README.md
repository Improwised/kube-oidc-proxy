# 🚀 kube-oidc-proxy

## 📖 Table of Contents

- [✨ Introduction](#-introduction)
- [📦 Handling kubectl Requests with Multi-Cluster Support](#-handling-kubectl-requests-with-multi-cluster-support)
- [🔧 Setting Up Multiple Clusters](#-setting-up-multiple-clusters)
- [🗂️ Configuring kubeconfig with kubelogin](#️-configuring-kubeconfig-with-kubelogin)
- [🔑 Roles and Permissions](#-roles-and-permissions)
  - [🛠 Default Roles and Permissions](#-default-roles-and-permissions)
  - [📂 Namespace-Specific Access](#-namespace-specific-access)
  - [🌐 Cluster-Wide Access](#-cluster-wide-access)
  - [⚙️ Custom Roles and Permissions](#️-custom-roles-and-permissions)
- [📜 Logging](#-logging)
- [🔍 Custom Webhook Auditing](#-custom-webhook-auditing)
- [🖥 Development](#-development)
  - [📝 Step 1: Keycloak Configuration](#-step-1-keycloak-configuration)
  - [⚙️ Step 2: Build the Binary](#️-step-2-build-the-binary)
  - [🚀 Step 3: Run the Proxy](#-step-3-run-the-proxy)
  - [🛡 Flag Descriptions](#-flag-descriptions)

---

## ✨ Introduction

`kube-oidc-proxy` is a reverse proxy server designed to authenticate and authorize users for Kubernetes API servers using **Keycloak (OIDC)**. It is ideal for managed Kubernetes platforms like GKE and EKS, where native OIDC support is unavailable. 🌐

### 🎯 Key Features

1. **Intercept Requests:** Receives `kubectl` requests from users.
2. **Authentication & Authorization:**
   - Authenticates requests using OIDC providers (e.g., Keycloak).
   - Verifies user permissions based on predefined roles.
3. **Forward Requests:** Sends authorized requests to the Kubernetes API server.
4. **Respond Back:** Routes Kubernetes API server responses back to users.

Check out this [DFD Diagram](https://github.com/Improwised/kube-oidc-proxy/issues/28#issuecomment-2581895267) to visualize the flow! 🔗

---

## 📦 Handling kubectl Requests with Multi-Cluster Support

To manage requests for multiple clusters, users specify the target cluster in the request URL.

### 🛠 Configuring the Cluster Name in the Request URL

Include the cluster name in the kubeconfig file's server URL:

```yaml
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://<proxy-ip>:<proxy-port>/<cluster-name>
```

Replace `<proxy-ip>`, `<proxy-port>`, and `<cluster-name>` with the proxy server's IP address, port, and cluster name.

### 🧩 Example Configuration

If the proxy runs at `192.168.1.100` on port `8080` and the cluster name is `k8s`, the server URL becomes:

```text
https://192.168.1.100:8080/k8s
```

This ensures all `kubectl` requests are routed through the proxy to the appropriate cluster. 🔄

---

## 🔧 Setting Up Multiple Clusters

`kube-oidc-proxy` supports multiple clusters, ideal for organizations managing diverse environments. 🌍

### 📝 Configuration Steps

1. **Create a Configuration File:** Define clusters in `config.yaml`:

   ```yaml
   clusters:
     - name: k8s
       kubeconfig: "<path-to-k8s-kubeconfig>"
     - name: kind
       kubeconfig: "<path-to-kind-kubeconfig>"
   ```

2. **Provide Configuration to Proxy:** Use the `--clusters-config` flag:

   ```bash
   go run cmd/main.go --clusters-config <path-to-config.yaml>
   ```

The proxy now authenticates and authorizes requests for all configured clusters. ✅

---

## 🗂️ Configuring kubeconfig with kubelogin

To enhance security, we use **kubelogin** for dynamic token generation and authentication with a proxy server. Follow these steps to set up **kubelogin** on your system.

### Step 1: Install Krew
Krew is a package manager for `kubectl` plugins. Run the following command to download and install Krew:

```bash
(
  set -x; cd "$(mktemp -d)" &&
  OS="$(uname | tr '[:upper:]' '[:lower:]')" &&
  ARCH="$(uname -m | sed -e 's/x86_64/amd64/' -e 's/\(arm\)\(64\)\?.*/\1\2/' -e 's/aarch64$/arm64/')" &&
  KREW="krew-${OS}_${ARCH}" &&
  curl -fsSLO "https://github.com/kubernetes-sigs/krew/releases/latest/download/${KREW}.tar.gz" &&
  tar zxvf "${KREW}.tar.gz" &&
  ./${KREW} install krew
)
```

### Step 2: Add Krew to PATH
After installing Krew, add its binary directory to your `PATH` environment variable. Edit your `.bashrc` or `.zshrc` file and append the following line:

```bash
export PATH="${KREW_ROOT:-$HOME/.krew}/bin:$PATH"
```

Then, reload your shell configuration:

```bash
source ~/.bashrc  # or source ~/.zshrc
```

### Step 3: Install kubelogin Plugin
Install the **kubelogin** plugin using Krew:

```bash
kubectl krew install oidc-login
```

### Step 4: kubeconfig File Format
Here is an example `kubeconfig` file configured to use **kubelogin**:

```yaml
apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: <base64-ca-cert-of-proxy-server>
    server: https://<proxy-ip>:<proxy-port>/<cluster-name>
  name: <cluster-name>
contexts:
- context:
    cluster: <cluster-name>
    user: <user>
  name: <context-name>
current-context: <context-name>
kind: Config
preferences: {}
users:
- name: <user>
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      args:
      - oidc-login
      - get-token
      - --oidc-issuer-url=<oidc-issuer-url>
      - --oidc-client-id=<oidc-client-id>
      - --oidc-client-secret=<oidc-client-secret>
      command: kubectl
```

## 🔑 Roles and Permissions

The proxy uses roles to define user permissions for each cluster. Roles can be tailored to organizational needs. 🏗️

### 🛠 Default Roles and Permissions

1. **DevOps**
   ```yaml
   rules:
     - apiGroups: ["*"]
       resources: ["*"]
       verbs: ["*"]
   ```

2. **Developer**
   ```yaml
   rules:
     - apiGroups: ["*"]
       resources: ["pods", "pods/log"]
       verbs: ["list", "watch", "get"]
   ```

3. **Watcher**
   ```yaml
   rules:
     - apiGroups: ["*"]
       resources: ["*"]
       verbs: ["list", "watch", "get"]
   ```

4. **Developer with Port-Forward**
   ```yaml
   rules:
     - apiGroups: [""]
       resources: ["pods", "pods/log", "pods/portforward"]
       verbs: ["list", "watch", "get","create"]
   ```
5. **Developer with Port-Exec and Port-Forward**
   ```yaml
   rules:
     - apiGroups: [""]
       resources: ["pods", "pods/log", "pods/portforward", "pods/exec"]
       verbs: ["list", "watch", "get","create"]
   ```
### 📂 Namespace-Specific Access

Use the format `<cluster-name>:<role>:<namespace>` for namespace-specific roles. Assign this role to users in Keycloak.

#### Resource Count
- Roles & RoleBindings: `4 (roles) × n (namespaces)`
- These are created dynamically within the proxy.

### 🌐 Cluster-Wide Access

Use `<cluster-name>:<role>` to grant cluster-wide roles in Keycloak.

#### Resource Count
- ClusterRoles & ClusterRoleBindings: 4 (roles)
- These are created dynamically within the proxy.

### ⚙️ Custom Roles and Permissions

Administrators can define custom roles in `role-config.yaml`:

```bash
go run cmd/main.go --role-config <path-to-role-config.yaml>
```

Refer to the role confing file [example](./roleConfig.yaml.example).

---

## 📜 Logging

Logs provide insights for debugging and integration with SIEM systems (e.g., Fluentd). 📊

### Example Logs

- **Successful Request:**

  ```text
  [2021-11-25T01:05:17+0000] AuSuccess src:[10.42.0.5 / 10.42.1.3] URI:/api/v1/namespaces/openunison/pods?limit=500 inbound:[mlbadmin1 / system:masters|system:authenticated /]
  ```

- **Failed Request:**

  ```text
  [2021-11-25T01:05:24+0000] AuFail src:[10.42.0.5 / 10.42.1.3] URI:/api/v1/nodes
  ```

---

## 🔍 Custom Webhook Auditing (Optional)

Send audit logs as JSON payloads to a webhook for custom auditing. 🔎

### Example Audit Payload

```go
type Log struct {
	ClusterName string `json:"cluster_name"`
	// user info
	Email  string              `json:"email"`
	UID    string              `json:"uid"`
	Groups []string            `json:"groups"`
	Extra  map[string][]string `json:"extra"`
	// request info
	IsResourceRequest bool     `json:"is_resource_request"`
	RequestPath       string   `json:"request_path"`
	Verb              string   `json:"verb"`
	APIPrefix         string   `json:"api_prefix"`
	APIGroup          string   `json:"api_group"`
	APIVersion        string   `json:"api_version"`
	Namespace         string   `json:"namespace"`
	Resource          string   `json:"resource"`
	SubResource       string   `json:"sub_resource"`
	Name              string   `json:"name"`
	Parts             []string `json:"parts"`
	FieldSelector     string   `json:"field_selector"`
	LabelSelector     string   `json:"label_selector"`
	// body
	RequestBody json.RawMessage `json:"request_body"`
}
```

### Configuring the Webhook

To enable custom webhook auditing, use the `--audit-webhook-server` flag:

```bash
go run cmd/main.go --audit-webhook-server <webhook-url>
```

Audit logs are sent to:

```
<webhook-url>/api/v1/k8s-audit-log/webhook
```

---

## 🖥 Development

> **Note:** Requires Go version 1.17 or higher. 🛠️

### 📝 Step 1: Keycloak Configuration

1. Create a new [client](https://github.com/Improwised/kube-oidc-proxy/issues/13#issuecomment-2576744735) in Keycloak.
2. Assign client [scopes and mappers](https://github.com/Improwised/kube-oidc-proxy/issues/13#issuecomment-2579232821) to client.
3. Create and assign [roles](https://github.com/Improwised/kube-oidc-proxy/issues/13#issuecomment-2601407225) to users.

### ⚙️ Step 2: Build the Binary

```bash
go build -o ./proxy ./cmd/.
```

### 🚀 Step 3: Run the Proxy

```bash
./proxy \
  --clusters-config=<path to>/clusterConfig.yaml \
  --oidc-issuer-url=https://<server-url>/realms/<realm-name> \
  --oidc-client-id=<client-id> \
  --oidc-ca-file=<path to oidc provider CA file> \
  --oidc-signing-algs=<alg-name> \
  --tls-cert-file=<path to TLS cert file> \
  --tls-private-key-file=<path to TLS private key file> \
  --oidc-groups-claim=groups \
  --role-config=<path to role-config file>
```

### 🛡 Flag Descriptions

- **`--clusters-config`**: Path to the clusters configuration file.
- **`--oidc-issuer-url`**: OIDC provider URL.
- **`--oidc-client-id`**: Client ID for authentication.
- **`--oidc-ca-file`**: CA file path for verifying the OIDC server.
- **`--oidc-signing-algs`**: Allowed signing algorithms (default: `RS256`).
- **`--tls-cert-file`**: TLS certificate file path.
- **`--tls-private-key-file`**: TLS private key file path.
- **`--oidc-groups-claim`**: Claim to retrieve user groups (default: `groups`).
- **`--role-config`**: Role configuration file path.
- **`--audit-webhook-server`**: Server URL for the webhook audit backend (optional).

---

