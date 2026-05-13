# Installing Epinio MCP via `epinio push`

This is the recommended install path for most users. Epinio manages the MCP
lifecycle (push, logs, restart, scale) just like any other application.

For the alternative `kubectl apply` (adopted) install — useful when you want the
MCP managed outside Epinio's REST path — see [`install/README.md`](install/README.md).

---

## Quick start

The repo ships a `Taskfile.yml` that automates the full setup. If you have
[`task`](https://taskfile.dev) installed:

```bash
# Edit epinio.yml with your cluster credentials first (see Step 6)
task setup          # cluster-prep → s3-service → configure-s3 → push → verify
```

Individual tasks for partial runs or re-runs:

```bash
task cluster-prep   # (cluster-admin, once per cluster) manifests + namespace label
task s3-service     # create + wait for epinio-s3-gateway service
task configure-s3   # create the epinio-s3-gateway Epinio configuration (idempotent)
task push           # epinio push from repo root
task verify         # smoke-test /healthz and /readyz
```

The sections below document each step in detail — useful when things go wrong
or when you need to run steps manually.

---

## Prerequisites

- Kubernetes cluster with [Epinio](https://github.com/epinio/epinio) installed
- `kubectl` and the `epinio` CLI pointed at your cluster
- [`task`](https://taskfile.dev) (optional — for `task setup` / individual tasks)
- Epinio namespace to deploy into (default: `epinio`)

---

## Step 1 — Deploy the chart server (one-time per cluster)

> **Task equivalent:** `task cluster-prep` runs Steps 1–3 plus the namespace
> label (Step 4 prerequisite) in one shot.

The `standard-elevated` AppChart references a custom Helm chart served by a small
nginx pod in the `epinio` namespace. Apply once:

```bash
kubectl apply -f manifests/chart-server.yaml
```

Verify it's running:

```bash
kubectl -n epinio get pod chart-server
```

---

## Step 2 — Register the `standard-elevated` AppChart (one-time per cluster)

This chart extends Epinio's standard chart with per-app RBAC that lets the MCP
pod read `apps.application.epinio.io` CRDs (needed for source retrieval) and pull
from the internal registry.

```bash
kubectl apply -f manifests/standard-elevated-appchart.yaml
```

Verify it appears in Epinio:

```bash
epinio app chart list
# should show: standard-elevated
```

---

## Step 3 — Register the S3 gateway catalog entry (one-time per cluster)

The MCP uses an S3 gateway to read app source tarballs from Epinio's internal
SeaweedFS store. Register the catalog entry that lets Epinio provision one:

```bash
kubectl apply -f manifests/s3-gateway-catalog-entry.yaml
```

Verify it appears in the catalog:

```bash
epinio service catalog
# should show: s3-gateway
```

> **Note:** The S3 gateway proxies to the SeaweedFS filer that Epinio deploys as
> part of its own install (`seaweedfs-filer-client.epinio.svc.cluster.local:8888`).
> If your Epinio uses a different internal filer address, update the `extraArgs`
> in `manifests/s3-gateway-catalog-entry.yaml` before applying.

---

## Step 4 — Label the epinio namespace (one-time per cluster)

By default, the `epinio` system namespace is not managed by Epinio, which
prevents service instances (like the S3 gateway) from being deployed into it.
Label it so Epinio can manage workloads there:

```bash
kubectl label namespace epinio app.kubernetes.io/component=epinio-namespace --overwrite
```

> **`task cluster-prep` does this automatically** as its final step.

---

## Step 5 — Create the S3 gateway service instance (in epinio namespace)

> **Task equivalent:** `task s3-service`

```bash
epinio service create s3-gateway epinio-s3-gateway
```

Wait for it to become ready:

```bash
epinio service show epinio-s3-gateway --namespace epinio
# Status should reach "deployed"
```

Once deployed, the Helm chart creates a Kubernetes Secret in the `epinio` namespace
containing the gateway's connection details. The MCP reads these values via an Epinio
**configuration** also named `epinio-s3-gateway` that gets created and bound to the
MCP pod later by `enable_capability` (Step 7).

### What the `epinio-s3-gateway` configuration contains

The configuration is a set of key-value files mounted into the MCP pod at
`/configurations/epinio-s3-gateway/`. The MCP reads four keys:

| Key | Required | Example | Notes |
|-----|----------|---------|-------|
| `endpoint` | yes | `seaweedfs-s3-epinio-s3-gateway.epinio.svc.cluster.local:8333` | ClusterIP host:port of the gateway pod |
| `bucket` | no | `epinio` | Defaults to `epinio` if omitted |
| `useSSL` | no | `false` | `true` or `false`; omit for in-cluster plaintext |
| `credentials` | no | _(see below)_ | Omit if the gateway has auth disabled (`s3.enableAuth=false`) |

When auth is enabled, `credentials` uses AWS CLI INI format:

```ini
[default]
aws_access_key_id = <key>
aws_secret_access_key = <secret>
```

> **You do not create this configuration manually.** `enable_capability app_editing`
> (Step 8) reads the values from the gateway's Kubernetes Secret automatically and
> creates + binds the configuration for you. The table above is for reference when
> debugging — e.g. inspecting a misconfigured endpoint or verifying credentials were
> picked up correctly.

### Finding the values yourself

When the service instance is deployed, the Helm chart writes a Kubernetes Secret with
the same name (`epinio-s3-gateway`) into the `epinio` namespace. This is where
`enable_capability` reads from, and where you can look if you need to debug or set
things up manually.

List all keys in the Secret:

```bash
kubectl -n epinio get secret epinio-s3-gateway -o jsonpath='{.data}' | tr ',' '\n'
```

Decode individual values:

```bash
# Endpoint (host:port the MCP dials)
kubectl -n epinio get secret epinio-s3-gateway \
  -o jsonpath='{.data.endpoint}' | base64 -d

# Bucket name
kubectl -n epinio get secret epinio-s3-gateway \
  -o jsonpath='{.data.bucket}' | base64 -d

# Credentials (AWS CLI INI format — only present if s3.enableAuth=true)
kubectl -n epinio get secret epinio-s3-gateway \
  -o jsonpath='{.data.credentials}' | base64 -d
```

Or dump the whole thing at once:

```bash
kubectl -n epinio get secret epinio-s3-gateway -o go-template='
{{- range $k,$v := .data }}{{ $k }}: {{ $v | base64decode }}
{{ end }}'
```

---

## Step 6 — Create the `epinio-s3-gateway` Epinio configuration

> **Task equivalent:** `task configure-s3` (idempotent — safe to re-run)

Epinio does **not** automatically create a configuration from a deployed service.
The `epinio.yml` binds `epinio-s3-gateway` as a configuration, so it must exist
before `epinio push` will succeed. The S3 credentials live in the
`epinio-s3-connection-details` Secret that Epinio's own Helm install writes into
the `epinio` namespace:

```bash
ENDPOINT=$(kubectl -n epinio get secret epinio-s3-connection-details \
  -o jsonpath='{.data.endpoint}' | base64 -d)
BUCKET=$(kubectl -n epinio get secret epinio-s3-connection-details \
  -o jsonpath='{.data.bucket}' | base64 -d)
USESSL=$(kubectl -n epinio get secret epinio-s3-connection-details \
  -o jsonpath='{.data.useSSL}' | base64 -d)
CREDS=$(kubectl -n epinio get secret epinio-s3-connection-details \
  -o jsonpath='{.data.credentials}' | base64 -d)
epinio target epinio
epinio configuration create epinio-s3-gateway \
  endpoint "$ENDPOINT" \
  bucket   "$BUCKET" \
  useSSL   "$USESSL" \
  credentials "$CREDS"
```

This only needs to be done once. If the configuration already exists
(`epinio configuration show epinio-s3-gateway` succeeds), skip this step.

---

## Step 7 — Configure `epinio.yml`

Open `epinio.yml` at the repo root and fill in your cluster-specific values in
the `environment` section:

```yaml
environment:
  EPINIO_API_URL: "https://epinio.your-cluster.example.com"   # your Epinio ingress URL
  EPINIO_USERNAME: "admin"                                      # Epinio admin user
  EPINIO_PASSWORD: "your-password"                              # Epinio admin password
```

> **OIDC alternative:** If your cluster uses OIDC auth, leave `EPINIO_USERNAME` /
> `EPINIO_PASSWORD` empty and set `EPINIO_TOKEN`, `EPINIO_REFRESH_TOKEN`, and
> `EPINIO_TOKEN_ENDPOINT` instead.

The `EPINIO_MCP_APP_NAME` and `EPINIO_MCP_APP_NAMESPACE` fields below those can
stay as-is unless you're deploying under a different name or namespace.

---

## Step 8 — Push

> **Task equivalent:** `task push`

From the repo root (Epinio picks up `epinio.yml` automatically):

```bash
epinio target epinio          # select the epinio namespace
epinio push
```

The push runs the full Paketo build cycle: upload source → stage (compile) →
deploy → wait for ready. Build logs stream to your terminal.

When the push completes:

```bash
epinio app show epinio-mcp    # check status + route
epinio app logs epinio-mcp    # tail runtime logs
```

The MCP is now available at the route Epinio assigned, e.g.:
`https://epinio-mcp.192.168.X.X.sslip.io/mcp`

---

## Verify the MCP is up

> **Task equivalent:** `task verify`

```bash
# Liveness probe
curl https://epinio-mcp.<your-route>/healthz

# Readiness probe (confirms MCP can reach Epinio)
curl https://epinio-mcp.<your-route>/readyz
```

Expected output for `/readyz`:
```json
{"epinio":{"kube_version":"...","platform":"...","version":"..."},"status":"ok","version":"0.6.0"}
```

---

## Step 9 — Bootstrap optional capabilities (via conversation)

After the MCP is running, chat with it to finish capability setup. The main
thing to enable is `app_editing`, which allows `get_app_source` and
`list_app_files`. Under the hood, `enable_capability` reads the S3 gateway's
Kubernetes Secret, creates the `epinio-s3-gateway` Epinio configuration with the
correct endpoint/bucket/credentials (see Step 5 for the key reference), binds it
to the MCP app, and triggers a rolling restart so the pod picks up the mount.

```
You: What capabilities are you missing?

MCP: (calls check_capabilities)
     app_editing: needs fulfillment — s3-gateway configuration not bound
     log_streaming: ready

You: Run enable_capability for app_editing.

MCP: (reads service Secret, creates + binds epinio-s3-gateway configuration,
      triggers rolling restart)
     app_editing: ready after pod rollout
```

---

## Updating

To push a new version:

```bash
git pull
epinio push
```

Epinio performs a rolling update — the old pod stays running until the new one is ready.

---

## Connecting to an AI agent

The MCP exposes a standard Streamable HTTP endpoint at `/mcp`. Any MCP-compatible
AI agent or client can connect by pointing it at the route Epinio assigned:

```
https://epinio-mcp.<your-route>/mcp
```

Authentication: pass an `Authorization: Bearer <token>` or `Authorization: Basic <base64(user:pass)>` header — the MCP forwards the credential to Epinio for each request. If no header is sent, the server falls back to the env-var credentials configured in `epinio.yml`.
