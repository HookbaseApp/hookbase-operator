# Hookbase Kubernetes Operator — Architecture Guide

## What Is This?

The Hookbase Kubernetes Operator is a Go-based Kubernetes controller that manages Hookbase webhook infrastructure as native Kubernetes resources. Instead of configuring webhook sources, destinations, routes, and tunnels through the Hookbase dashboard or CLI, platform teams define them as YAML manifests — the same way they manage Deployments, Services, and ConfigMaps.

The operator watches these custom resources, reconciles them against the Hookbase API, and keeps the two in sync. If someone modifies a resource through the dashboard, the operator corrects the drift on its next reconciliation cycle. If someone deletes a CRD, the operator cleans up the corresponding API resource via finalizers.

## Why It Exists

Webhook infrastructure is a dependency for applications. When an engineering team deploys a new service that needs to receive GitHub webhooks, they currently have to:

1. Deploy the service
2. Log into Hookbase
3. Manually create a source, destination, route, and possibly a tunnel
4. Copy secrets and URLs back into their Kubernetes config

This is error-prone, not reproducible, and breaks GitOps workflows. The operator collapses all of that into a single `kubectl apply` alongside the application deployment.

## Architecture

```
                                              +-----------------+
                                              | Hookbase API    |
                                              | (Cloudflare     |
                                              |  Workers)       |
                                              +--------^--------+
                                                       |
                                              SDK calls (HTTPS)
                                                       |
+------------------------------------------------------+--------+
| Kubernetes Cluster                                             |
|                                                                |
|  +------------------+     watches     +-------------------+    |
|  | hookbase-operator| <-------------- | CRDs (10 types)   |    |
|  | (controller-     |                 | - WebhookSource   |    |
|  |  runtime)        |                 | - WebhookDest     |    |
|  |                  |   reconciles    | - WebhookRoute    |    |
|  | 11 controllers   | --------------> | - WebhookTunnel   |    |
|  | Prometheus metrics|                | - WebhookTransform|    |
|  | Health probes    |                 | - WebhookFilter   |    |
|  +--------+---------+                 | - WebhookSchema   |    |
|           |                           | - HookbaseCronJob |    |
|           | injects sidecar           | - HookbaseAPIKey  |    |
|           v                           | - HookbaseConfig  |    |
|  +------------------+                 +-------------------+    |
|  | Application Pod  |                                          |
|  | +------+ +-----+ |                                          |
|  | | app  | |agent| | <-- hookbase-agent sidecar               |
|  | +------+ +--+--+ |     (Go, ~5MB, WebSocket tunnel)        |
|  +---------------+---+                                          |
|                  |                                              |
|           WebSocket to Hookbase tunnel server                  |
+----------------------------------------------------------------+
```

## Components

### 1. Operator Binary (`cmd/operator/`)

The main controller manager. Built on `controller-runtime` v0.19. Runs as a single Deployment in the cluster (typically in `hookbase-system` namespace). Manages 11 controllers — one per CRD type plus one for Kubernetes Ingress resources.

**Key design decisions:**
- **One controller per CRD type** — isolation, independent error handling, separate requeue timers
- **SDK client manager** — per-namespace client caching, resolves API keys from K8s Secrets with priority chain: explicit `apiKeyRef` > default HookbaseAPIKey (labeled `hookbase.io/default=true`) > bootstrap key
- **Finalizers on all resources** — ensures API cleanup even if `kubectl delete` is called

### 2. Tunnel Agent (`cmd/agent/`)

A lightweight Go binary (~5MB) that acts as a WebSocket tunnel client. Runs as a sidecar container injected into application Deployments by the `WebhookTunnel` controller.

**Protocol:**
- Connects to Hookbase tunnel server via WebSocket
- Receives HTTP requests as JSON messages (`{id, method, url, headers, body}`)
- Forwards them to the local service (`localhost:PORT` or a K8s Service)
- Sends responses back over the WebSocket
- Maintains connection with ping/pong heartbeats (client: 30s, server: 10s)
- Auto-reconnects with exponential backoff (1s base, 60s cap, jitter)
- Limits concurrency to 50 simultaneous forwarded requests

### 3. Helm Chart (`chart/`)

Production-ready Helm chart with:
- All 10 CRDs in `templates/crds/`
- RBAC (ClusterRole with fine-grained permissions)
- Bootstrap API key Secret
- Optional ServiceMonitor for Prometheus
- Configurable security context, resource limits, node affinity

### 4. Admission Webhooks (`internal/admission/`)

Optional validating and defaulting webhooks:
- **Slug immutability** — prevents changing `.spec.slug` after creation
- **CIDR validation** — validates IP allowlist/denylist entries
- **Mutual exclusion** — prevents setting both `filterRef` and `filterConditions` on routes
- **Defaults** — sets `provider=generic`, `method=POST`, `filterLogic=AND`

## CRD Reference

### Core Resources

| CRD | API Group | Purpose |
|-----|-----------|---------|
| `WebhookSource` | `hookbase.io/v1alpha1` | Defines a webhook ingestion endpoint (e.g., "GitHub Webhooks") |
| `WebhookDestination` | `hookbase.io/v1alpha1` | Defines where webhooks are delivered (URL + auth) |
| `WebhookRoute` | `hookbase.io/v1alpha1` | Connects a source to a destination with optional filter/transform |
| `WebhookTunnel` | `hookbase.io/v1alpha1` | Creates a public URL that tunnels to a local K8s service |

### Pipeline Resources

| CRD | Purpose |
|-----|---------|
| `WebhookTransform` | JSONata/JavaScript transformations applied to webhook payloads |
| `WebhookFilter` | Conditions that determine which webhooks are routed |
| `WebhookSchema` | JSON Schema validation for incoming webhooks |

### Infrastructure Resources

| CRD | Purpose |
|-----|---------|
| `HookbaseCronJob` | Scheduled HTTP requests (cron jobs managed by Hookbase) |
| `HookbaseAPIKey` | Provisions scoped API keys, stores raw key in K8s Secret |
| `HookbaseConfig` | Cluster-scoped config (bootstrap key, default agent image) |

## Reconciliation Loop

Every controller follows the same pattern:

```
1. Fetch CRD from cluster
   └─ NotFound? → done (resource was deleted before we got to it)

2. Resolve API key
   └─ explicit apiKeyRef → K8s Secret
   └─ default HookbaseAPIKey in namespace → its Secret
   └─ bootstrap key from HookbaseConfig/Helm
   └─ FAIL? → set Ready=False, requeue in 30s

3. Handle deletion (DeletionTimestamp set?)
   └─ Call SDK Delete (ignore NotFound — already gone)
   └─ Remove finalizer
   └─ Return

4. Add finalizer if missing

5. Create or Update
   └─ status.resourceId == "" → SDK Create, store ID in status
   └─ status.resourceId != "" → SDK Update
   └─ FAIL? → set Ready=False, requeue in 30s

6. Set conditions
   └─ Ready=True, Synced=True
   └─ Update observedGeneration, lastSyncTime

7. Requeue after reconcileInterval (drift detection)
```

**Special behaviors:**
- **WebhookRoute**: Resolves `sourceRef` and `destinationRef` to their API IDs by reading sibling CRDs. If any ref isn't Ready, requeues instead of failing.
- **WebhookTunnel**: Creates a K8s Secret with the auth token (only available at creation time). Injects a sidecar container into the target Deployment.
- **HookbaseAPIKey**: Uses the bootstrap key (not its own key) for API calls. Stores the raw API key in a K8s Secret with owner reference.

## Cross-Resource References

CRDs reference each other by name within the same namespace:

```yaml
apiVersion: hookbase.io/v1alpha1
kind: WebhookRoute
spec:
  sourceRef: github-webhooks       # References WebhookSource by metadata.name
  destinationRef: internal-api     # References WebhookDestination by metadata.name
  filterRef: production-only       # References WebhookFilter by metadata.name
  transformRef: strip-headers      # References WebhookTransform by metadata.name
```

The route controller resolves these names to API IDs at reconciliation time. This is GitOps-friendly — no raw API IDs in manifests.

## API Key Resolution

```
┌─────────────────────────────────────────────────────┐
│                  GetClient()                         │
│                                                      │
│  1. spec.apiKeyRef set?                              │
│     └─ YES → read K8s Secret → return client         │
│                                                      │
│  2. Default HookbaseAPIKey in namespace?             │
│     (label: hookbase.io/default=true)                │
│     └─ YES + Secret exists → return client           │
│     └─ YES + Secret missing → fall through           │
│                                                      │
│  3. Bootstrap key configured?                        │
│     └─ YES → return client                           │
│     └─ NO → error: no API key available              │
└─────────────────────────────────────────────────────┘
```

This allows teams to:
- Use a shared bootstrap key for quick setup
- Create per-namespace scoped keys for production isolation
- Override per-resource for fine-grained access control

## Sidecar Injection

When a `WebhookTunnel` has `spec.sidecarInjection` set:

1. Operator creates the tunnel in the Hookbase API
2. Stores the auth token in a K8s Secret (`{tunnel-name}-tunnel-auth`)
3. Patches the target Deployment to add a `hookbase-agent` container
4. Agent connects via WebSocket and forwards requests to `localhost:{port}` or `{service}.{ns}.svc.cluster.local:{port}`
5. On tunnel CRD deletion: removes the sidecar container from the Deployment, K8s garbage-collects the auth Secret (owner reference)

## Ingress Controller

The operator watches Kubernetes `Ingress` resources with `ingressClassName: hookbase`. For each ingress rule, it creates the underlying Hookbase resources (source + tunnel + route) automatically.

Annotations control behavior:
```yaml
metadata:
  annotations:
    hookbase.io/source-provider: "stripe"
    hookbase.io/source-verify-signature: "true"
    hookbase.io/source-signing-secret-ref: "stripe-secret:key"
    hookbase.io/route-transform-ref: "my-transform"
```

## Observability

### Prometheus Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `hookbase_operator_reconcile_total` | Counter | `kind`, `result` | Total reconciliations by resource type and outcome |
| `hookbase_operator_reconcile_duration_seconds` | Histogram | `kind` | Reconciliation latency |
| `hookbase_operator_api_calls_total` | Counter | `resource`, `method`, `status_code` | SDK API call counts |
| `hookbase_operator_managed_resources` | Gauge | `kind`, `namespace` | Number of managed resources |
| `hookbase_operator_tunnel_connected` | Gauge | `name`, `namespace` | Tunnel connection status (0/1) |

### Health Probes

- `/healthz` — liveness probe
- `/readyz` — readiness probe

### Conditions

Every CRD has standard Kubernetes conditions:
- `Ready` — overall resource health
- `Synced` — last successful sync with Hookbase API

```bash
kubectl get webhooksources
# NAME              SOURCE ID   PROVIDER   READY   AGE
# github-webhooks   src_2       github     True    5m

kubectl describe webhooksource github-webhooks
# Status:
#   Conditions:
#     Type:    Ready
#     Status:  True
#     Reason:  Synced
```

## Directory Structure

```
operator/                          # 7,400 lines of Go
├── cmd/
│   ├── operator/main.go           # Controller manager entrypoint
│   └── agent/main.go              # Tunnel agent binary
├── api/v1alpha1/                  # CRD type definitions
│   ├── webhooksource_types.go     # Source spec/status/markers
│   ├── webhookdestination_types.go
│   ├── webhookroute_types.go      # Cross-resource refs
│   ├── webhooktunnel_types.go     # Sidecar injection config
│   ├── webhook{transform,filter,schema}_types.go
│   ├── hookbase{cronjob,apikey,config}_types.go
│   ├── common_types.go            # Shared types (SecretKeyRef, conditions)
│   ├── groupversion_info.go       # API group registration
│   └── zz_generated.deepcopy.go   # Generated DeepCopy methods
├── internal/
│   ├── controller/                # 11 reconcilers
│   │   ├── webhooksource_controller.go
│   │   ├── webhookroute_controller.go   # Cross-ref resolution
│   │   ├── webhooktunnel_controller.go  # Sidecar injection
│   │   ├── ingress_controller.go        # Ingress-to-CRD translation
│   │   └── ...
│   ├── hookbase/client.go         # SDK client manager (per-namespace caching)
│   ├── agent/                     # Go tunnel agent
│   │   ├── agent.go               # Orchestrator + concurrency limiter
│   │   ├── websocket.go           # WebSocket client with reconnect
│   │   └── forwarder.go           # HTTP request forwarding
│   ├── admission/webhooks.go      # Validating + defaulting webhooks
│   └── metrics/metrics.go         # Prometheus metric definitions
├── chart/                         # Helm chart
│   ├── Chart.yaml
│   ├── values.yaml
│   └── templates/
│       ├── crds/                  # 10 CRD definitions
│       ├── deployment.yaml
│       ├── clusterrole.yaml
│       └── servicemonitor.yaml
├── config/samples/                # Example CRDs
├── test/                          # 30 tests
│   ├── websocket_test.go          # Agent WebSocket protocol tests
│   ├── admission_test.go          # Webhook validation tests
│   ├── controller_test.go         # SDK integration + CRD type tests
│   └── mock-api.go                # Standalone mock API for e2e
├── Dockerfile                     # Production operator image
├── Dockerfile.agent               # Production agent image (<10MB)
├── Makefile
└── go.mod                         # Uses hookbase-go SDK
```

## Installation

```bash
# Add the Helm repo (when published)
helm repo add hookbase https://charts.hookbase.app

# Install with bootstrap API key
helm install hookbase-operator hookbase/hookbase-operator \
  --namespace hookbase-system --create-namespace \
  --set hookbase.apiKey=whr_your_api_key

# Or reference an existing Secret
helm install hookbase-operator hookbase/hookbase-operator \
  --namespace hookbase-system --create-namespace \
  --set hookbase.apiKeySecretRef.name=my-hookbase-key \
  --set hookbase.apiKeySecretRef.key=apiKey
```

## Quick Start

After installation, create webhook infrastructure as Kubernetes resources:

```yaml
# 1. Source — where webhooks come from
apiVersion: hookbase.io/v1alpha1
kind: WebhookSource
metadata:
  name: github-webhooks
spec:
  name: GitHub Webhooks
  slug: github-webhooks
  provider: github
  verifySignature: true
  signingSecretRef:
    name: github-webhook-secret
    key: secret
---
# 2. Destination — where webhooks go
apiVersion: hookbase.io/v1alpha1
kind: WebhookDestination
metadata:
  name: my-api
spec:
  name: My API
  url: "https://api.example.com/webhooks"
  method: POST
  authType: bearer
  authSecretRef:
    name: api-token
    key: token
---
# 3. Route — connects source to destination
apiVersion: hookbase.io/v1alpha1
kind: WebhookRoute
metadata:
  name: github-to-api
spec:
  name: GitHub to API
  sourceRef: github-webhooks
  destinationRef: my-api
```

```bash
kubectl apply -f webhooks.yaml
kubectl get webhooksources,webhookdestinations,webhookroutes
```

## Dependencies

| Dependency | Version | Purpose |
|-----------|---------|---------|
| `controller-runtime` | v0.19.0 | Kubernetes controller framework |
| `hookbase-go` SDK | local | Hookbase API client |
| `nhooyr.io/websocket` | v1.8.17 | WebSocket for tunnel agent |
| `prometheus/client_golang` | v1.20.5 | Metrics instrumentation |
| Go | 1.23+ | Language runtime |
