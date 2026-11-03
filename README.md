# Hookbase Kubernetes Operator

A Kubernetes operator for managing [Hookbase](https://hookbase.app) webhook resources declaratively using Custom Resource Definitions (CRDs).

## Features

- **Declarative webhook management** — Define sources, destinations, routes, and tunnels as Kubernetes resources
- **Automatic reconciliation** — Operator syncs CRD state with the Hookbase API, with drift detection
- **Agent sidecar** — WebSocket-based tunnel agent for forwarding webhooks to in-cluster services
- **Admission webhooks** — Optional validation for CIDR ranges, slug immutability, and filter mutual exclusion
- **Prometheus metrics** — Built-in metrics endpoint with optional ServiceMonitor

## Prerequisites

- Kubernetes 1.28+
- Helm 3.x
- A [Hookbase](https://hookbase.app) account and API key

## Installation

### 1. Create a Hookbase API key

Log in to [hookbase.app](https://hookbase.app), go to **Settings > API Keys**, and create a key with full access. The key will start with `whr_`.

### 2. Install with Helm

```bash
# Create namespace and API key secret
kubectl create namespace hookbase-system
kubectl create secret generic hookbase-api-key \
  -n hookbase-system \
  --from-literal=apiKey="whr_your_api_key_here"

# Install the operator from a release tag (the chart is not published to a Helm repo)
git clone --branch v0.4.1 https://github.com/HookbaseApp/hookbase-operator.git
cd hookbase-operator

helm install hookbase-operator ./chart \
  -n hookbase-system \
  --set hookbase.apiKeySecretRef.name=hookbase-api-key
```

### 3. Verify

```bash
kubectl get pods -n hookbase-system
kubectl get crds | grep hookbase
```

## Usage

### WebhookSource

```yaml
apiVersion: hookbase.io/v1alpha1
kind: WebhookSource
metadata:
  name: github-webhooks
spec:
  name: GitHub Webhooks
  slug: github-webhooks
  provider: github
  rejectInvalidSignatures: true
  signingSecretRef:
    name: github-webhook-secret
    key: secret
```

Signing secrets and verify tokens:

- `spec.signingSecretRef` (default key `signingSecret`) is read and sent to Hookbase on create, and
  again whenever that Secret changes. The operator never writes to a Secret you reference.
- Without `signingSecretRef`, a secret Hookbase generates is stored in the operator-owned Secret
  `<metadata.name>-signing-secret` under key `signingSecret`. Providers with no secret (wise,
  wise-sandbox, aws-sns) or whose secret arrives after registration (asana, notion) store nothing at create
  (`status.verifiesWithoutSecret` / `status.awaitingSigningSecret` report those states); once an
  asana/notion secret is set it is copied into the operator-owned Secret on a later reconcile.
- WhatsApp: `spec.verifyTokenRef` (default key `verifyToken`) supplies the Meta verify token.
  Without it, the generated token is stored in `<metadata.name>-signing-secret` under key
  `verifyToken`.
- `verifySignature` is a deprecated alias for `rejectInvalidSignatures`.

### WebhookDestination

```yaml
apiVersion: hookbase.io/v1alpha1
kind: WebhookDestination
metadata:
  name: my-api
spec:
  name: My API
  slug: my-api
  url: https://api.example.com/webhooks
  method: POST
  timeoutMs: 30000
```

### WebhookRoute

```yaml
apiVersion: hookbase.io/v1alpha1
kind: WebhookRoute
metadata:
  name: github-to-api
spec:
  name: GitHub to API
  sourceRef:
    name: github-webhooks
  destinationRef:
    name: my-api
  priority: 1
  isActive: true
```

### WebhookTunnel

```yaml
apiVersion: hookbase.io/v1alpha1
kind: WebhookTunnel
metadata:
  name: dev-tunnel
spec:
  name: Dev Tunnel
  localPort: 8080
  targetService:
    name: my-service
    port: 8080
```

### HookbaseAPIKey

```yaml
apiVersion: hookbase.io/v1alpha1
kind: HookbaseAPIKey
metadata:
  name: team-ci-key
spec:
  name: CI/CD Pipeline
  scopes:
    - sources:read
    - events:read
    - deliveries:read
  secretRef:
    name: hookbase-ci-key
```

## Configuration

Key Helm values:

| Value | Default | Description |
|-------|---------|-------------|
| `hookbase.apiKey` | `""` | API key (use `apiKeySecretRef` for production) |
| `hookbase.apiKeySecretRef.name` | `""` | K8s Secret containing the API key |
| `hookbase.baseURL` | `https://api.hookbase.app` | Hookbase API URL |
| `agent.image` | `ghcr.io/hookbaseapp/hookbase-agent:latest` | Agent sidecar image |
| `controller.reconcileInterval` | `5m` | Drift detection interval |
| `controller.leaderElect` | `false` | Enable for HA deployments |
| `webhook.enabled` | `false` | Enable admission webhooks |
| `metrics.enabled` | `true` | Expose Prometheus metrics |
| `metrics.serviceMonitor.enabled` | `false` | Create ServiceMonitor CR |

See [`chart/values.yaml`](chart/values.yaml) for all options.

## CRDs

| CRD | Description |
|-----|-------------|
| `WebhookSource` | Webhook ingestion endpoint (e.g., GitHub, Stripe) |
| `WebhookDestination` | Target URL for delivering webhooks |
| `WebhookRoute` | Routes events from source to destination |
| `WebhookTunnel` | Tunnel for forwarding to in-cluster services |
| `WebhookFilter` | Filter conditions for routing |
| `WebhookTransform` | Payload transforms (JSONata, JavaScript, TypeScript, Liquid, XSLT, JMESPath, JSON Logic, jq) |
| `WebhookSchema` | JSON Schema validation for events |
| `HookbaseAPIKey` | Managed API keys stored as K8s Secrets |
| `HookbaseConfig` | Organization-level configuration |
| `HookbaseCronJob` | Scheduled webhook deliveries |

## Development

```bash
# Run tests
go test ./... -v

# Build binaries
make build

# Build Docker images
make docker-build
make docker-build-agent

# Render Helm templates
make helm-template

# Lint
make vet
make helm-lint
```

## License

Proprietary - Copyright Hookbase
