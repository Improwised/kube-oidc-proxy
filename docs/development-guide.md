# kube-oidc-proxy Development Guide

This guide provides comprehensive instructions for setting up a development environment for kube-oidc-proxy, running tests, and contributing to the project.

## Table of Contents
- [Prerequisites](#prerequisites)
- [Setting Up the Development Environment](#setting-up-the-development-environment)
- [Starting an OIDC Provider](#starting-an-oidc-provider)
- [Generating Certificates](#generating-certificates)
- [Installing CRDs](#installing-crds)
- [Running kube-oidc-proxy](#running-kube-oidc-proxy)
- [Running Tests](#running-tests)
- [Running E2E Tests](#running-e2e-tests)
- [Development Workflow](#development-workflow)

## Prerequisites

### Required Software
- Go 1.23.0 or later
- Docker
- kubectl
- kind (Kubernetes in Docker) for local testing
- make

## Setting Up the Development Environment

you need oidc provider running locally to test kube-oidc-proxy. You can use a simple OIDC provider like Keycloak or Dex.

### Generating Certificates

For secure communication, kube-oidc-proxy requires TLS certificates. Here's how to generate them for development:

```bash
# Create a directory for certificates
mkdir kube-oidc-proxy-certs

# Generate a CA certificate
openssl req -x509 -newkey rsa:4096 -nodes -keyout ./kube-oidc-proxy-certs/ca.key -out ./kube-oidc-proxy-certs/ca.crt -subj "/CN=kube-oidc-proxy-ca" -days 365

# Generate a server certificate signing request
openssl req -newkey rsa:4096 -nodes -keyout ./kube-oidc-proxy-certs/server.key -out ./kube-oidc-proxy-certs/server.csr -subj "/CN=kube-oidc-proxy" -days 365

# Create a config file for the certificate
cat > ./kube-oidc-proxy-certs/server.ext << EOF
authorityKeyIdentifier=keyid,issuer
basicConstraints=CA:FALSE
keyUsage = digitalSignature, nonRepudiation, keyEncipherment, dataEncipherment
subjectAltName = @alt_names

[alt_names]
DNS.1 = localhost
IP.1 = 127.0.0.1
EOF

# Sign the server certificate with the CA
openssl x509 -req -in ./kube-oidc-proxy-certs/server.csr -CA ./kube-oidc-proxy-certs/ca.crt -CAkey ./kube-oidc-proxy-certs/ca.key -CAcreateserial -out ./kube-oidc-proxy-certs/server.crt -days 365 -extfile ./kube-oidc-proxy-certs/server.ext
```

### Installing CRDs

kube-oidc-proxy uses Custom Resource Definitions (CRDs) for RBAC management. To install them:

```bash
# Export KUBECONFIG to point to your target cluster
export KUBECONFIG=/path/to/your/kubeconfig

# Apply the CRDs
kubectl apply -f deploy/crds/rbac.platformengineers.io_capiclusterrolebindings.yaml
kubectl apply -f deploy/crds/rbac.platformengineers.io_capiclusterroles.yaml
kubectl apply -f deploy/crds/rbac.platformengineers.io_capirolebindings.yaml
kubectl apply -f deploy/crds/rbac.platformengineers.io_capiroles.yaml
```



### Cluster Configuration

```yaml
# clusters-config.yaml
clusters:
  - name: local
    kubeconfig: "/path/to/your/kubeconfig"
```
### Running kube-oidc-proxy
After setting up the OIDC provider and generating certificates and cluster configuration, you can run kube-oidc-proxy:

```bash
./bin/kube-oidc-proxy \
  --clusters-config=clusters-config.yaml \
  --oidc-issuer-url=<oidc-provider-url(ex. local.keycloak.com)> \
  --oidc-client-id=<client-id-of-oidc-issuer>\
  --oidc-ca-file=<certificate-file-path-used-for-oidc-issuer> \
  --tls-cert-file=<tls-cert-file-path> \
  --tls-private-key-file=<tls-private-key-file-path> \
  --oidc-groups-claim=groups
```

for ```tls-cert-file``` and ```tls-private-key-file```, you can use the certificates generated in the previous step.

## Running Tests

### Unit Tests

To run unit tests:

```bash
make test
```

This will:
1. Generate necessary mocks
2. Verify code formatting and linting
3. Run all unit tests
4. Generate test reports in the `artifacts` directory

### Running E2E Tests

End-to-end tests verify the complete functionality of kube-oidc-proxy. To run them:

```bash
make e2e
```

This will:
1. Create a kind cluster
2. Deploy the necessary components (OIDC issuer, kube-oidc-proxy)
3. Run the E2E test suite
4. Generate test reports

### Running Specific Tests

To run specific tests:

```bash
# Run a specific unit test
go test -v ./pkg/proxy/...

# Run a specific E2E test
KUBE_OIDC_PROXY_ROOT_PATH="$(pwd)" go test -v -run TestRBAC ./test/e2e/suite/...
```

## Development Workflow

1. **Create a Development Cluster**:
   ```bash
   make dev_cluster_create
   ```

2. **Deploy kube-oidc-proxy**:
   ```bash
   make dev_cluster_deploy
   ```
   This will output a valid OIDC token for testing.

3. **Test the Deployment**:
   ```bash
   # Use the token from the previous step
   curl -k https://172.17.0.2:30226 -H 'Authorization: bearer <token>'
   ```

4. **Make Changes to the Code**:
   After making changes, rebuild and redeploy:
   ```bash
   make build
   make dev_cluster_deploy
   ```

5. **Run Tests**:
   ```bash
   make test
   make e2e
   ```

6. **Clean Up**:
   ```bash
   make dev_cluster_destroy
   ```

## Troubleshooting

### Common Issues

1. **Certificate Issues**:
   - Ensure the OIDC provider's certificate is trusted by kube-oidc-proxy
   - Check that the TLS certificates for kube-oidc-proxy are valid

2. **OIDC Configuration**:
   - Verify the OIDC issuer URL is correct and accessible
   - Ensure the client ID matches what's configured in the OIDC provider

3. **Kubernetes Access**:
   - Make sure the kubeconfig has sufficient permissions
   - Check that the CRDs are properly installed

### Debugging

1. **Enable Debug Logging**:
   ```bash
   ./bin/kube-oidc-proxy \
     --v=4 \
     ... (other flags)
   ```

2. **Check OIDC Discovery**:
   ```bash
   curl -k https://127.0.0.1:8080/.well-known/openid-configuration
   ```

3. **Verify Token**:
   ```bash
   # Get a token from the OIDC provider
   TOKEN=$(curl -k -X POST https://127.0.0.1:8080/token)
   
   # Use the token with kube-oidc-proxy
   curl -k https://localhost:8443 -H "Authorization: Bearer $TOKEN"
   ```

## Additional Resources

- [Complete Setup Guide](./user-guides/complete-setup-guide.md)
- [Development Testing](./tasks/development-testing.md)
- [Auditing](./tasks/auditing.md)
- [Token Passthrough](./tasks/token-passthrough.md)