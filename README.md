# epinio-mcp

> **Experimental:** This project is under active development and not yet stable.
> APIs, tool names, and install steps may change without notice. Not recommended
> for production use.

An MCP (Model Context Protocol) server that exposes the Epinio API as tools for AI agents. Built in Go using the official MCP Go SDK.

## Overview

This server acts as a bridge between AI agents (Claude, etc.) and an Epinio application platform instance. It translates MCP tool calls into Epinio REST API requests, enabling AI-driven application deployment and management on Kubernetes.

## Architecture

```text
AI Agent (Claude, etc.)
    ↕ MCP Protocol (Streamable HTTP)
Epinio MCP Server (this project)
    ↕ REST API (Basic Auth + TLS)
Epinio API Server
    ↕ Kubernetes API
Kubernetes Cluster
```

## Tools (44 total)

| Tool | Description |
| ---- | ----------- |
| `epinio_info` | Get server version, k8s version, platform info |
| `list_namespaces` | List all namespaces with their apps and configurations |
| `create_namespace` | Create a new namespace |
| `delete_namespace` | Delete a namespace and all its resources |
| `list_apps` | List applications, optionally filtered by namespace |
| `show_app` | Get detailed app info (status, routes, instances, config) |
| `create_app` | Create an application (without deploying) |
| `delete_app` | Delete an application |
| `restart_app` | Restart an application |
| `scale_app` | Scale an application to a desired instance count |
| `update_app` | Update app configuration (instances, routes, env, configurations, appchart, settings) |
| `push_app` | Full push workflow: create + upload + stage + deploy from source files |
| `upload_and_stage` | Upload source and build without deploying (inspect logs first) |
| `deploy_staged` | Deploy a previously staged build |
| `app_logs` | Fetch runtime or staging/build logs from an application |
| `list_env` | List environment variables for an app |
| `set_env` | Set environment variables on an app |
| `unset_env` | Remove an environment variable from an app |
| `list_configurations` | List configurations in a namespace |
| `create_configuration` | Create a key-value configuration |
| `delete_configuration` | Delete a configuration |
| `bind_configuration` | Bind configurations to an app |
| `unbind_configuration` | Unbind a configuration from an app |
| `list_services` | List service instances in a namespace |
| `list_catalog_services` | List available catalog services with their settings schemas |
| `show_catalog_service` | Fetch a single catalog service's full details and settings schema |
| `list_appcharts` | List AppCharts registered on the cluster (valid values for `appchart`), with per-chart settings schemas |
| `show_appchart` | Fetch a single AppChart's description + settings schema |
| `list_builders` | List Cloud Native Buildpacks builder images usable with this cluster and the ecosystems each supports |
| `get_build_guidance` | Return canned guidance on deploying / appchart selection / builder selection / build troubleshooting (universal fallback for the MCP Prompts with the same names) |
| `check_capabilities` | Report readiness of optional MCP capabilities (e.g. `app_editing`) and what's missing |
| `enable_capability` | Fulfill a capability's satisfiable requirements (create service instances, bind configurations) |
| `get_app_source` | Retrieve a deployed app's staging tarball via S3 gateway; gated by the `app_editing` capability |
| `list_app_files` | List file paths + sizes in a deployed app's source (cheap, no bytes returned); gated by `app_editing` |
| `get_connection_info` | Return the URL + forwarded OIDC token a caller needs to connect directly to a capability's backing service (e.g. Epinio's log WebSocket) |
| `create_service` | Create a service from the catalog |
| `delete_service` | Delete a service instance |
| `bind_service` | Bind a service to an app |
| `unbind_service` | Unbind a service from an app |
| `get_app_manifest` | Inspect full app configuration (image, routes, env, settings) |
| `clone_app` | Clone an existing app to a new name using its built image |
| `adopt_app` | Bring an existing kubectl-managed Deployment into Epinio's view: label it, create an App CRD, make it visible to `epinio app list/show/logs/exec` |
| `reconcile_app` | Sync an adopted app's CRD to observed reality (image URL, routes from Ingresses). `dry_run` supported |
| `release_app` | Remove Epinio labels + App CRD for an adopted app. Underlying Deployment keeps running |

## Capabilities (optional, gated)

Some tools require extra cluster infrastructure. They're gated behind
named capabilities so the MCP reports readiness explicitly rather than
failing silently. Call `check_capabilities` to see what's ready.

| Capability       | Gates                                       | Requires                                                                  |
| ---------------- | ------------------------------------------- | ------------------------------------------------------------------------- |
| `app_editing`    | `get_app_source`, `list_app_files`          | `s3-gateway` catalog + S3 credentials available to the MCP (either Epinio-bound configuration or env-var-backed Secret populated by `enable_capability`) + `get apps.application.epinio.io` RBAC on the MCP pod SA |
| `log_streaming`  | (environmental) advertises WS reachability  | Ingress preserves `Upgrade`; Epinio's `/authtoken` endpoint reachable     |
| `self_adoption`  | N/A (internal-housekeeping capability)      | MCP's own App CRD exists, is annotated `epinio.io/adopted=true`, and has spec matching the running Deployment. Fulfill reconciles from the pod |

`enable_capability` can fulfill the user-scope pieces (service instance,
configuration binding, self-adoption metadata, and — for adopted MCP
installs — writing S3 credentials into the MCP's own Secret with a
rolling restart); cluster-admin items (catalog install, cross-namespace
RBAC) are reported as `needs_admin`.

## Prerequisites

- Go 1.24+
- A running [Epinio](https://epinio.io) instance

## Health Probes

Besides the MCP protocol endpoints, the server exposes two plain-HTTP probes:

| Path       | Type        | Behavior                                                  |
| ---------- | ----------- | --------------------------------------------------------- |
| `/healthz` | Liveness    | Always returns 200 if the process is up                   |
| `/readyz`  | Readiness   | Calls Epinio `/info`; 200 on success, 503 on upstream failure with the error body |

Both respond with JSON (`status`, `version`, and `epinio` info on `/readyz` success).

## Configuration

Environment variables:

| Variable                   | Default                                   | Description                                                                   |
| -------------------------- | ----------------------------------------- | ----------------------------------------------------------------------------- |
| `EPINIO_API_URL`           | `https://epinio.192.168.127.2.sslip.io`   | Epinio API endpoint                                                           |
| `EPINIO_USERNAME`          | `admin`                                   | API username (basic auth)                                                     |
| `EPINIO_PASSWORD`          | `password`                                | API password (basic auth)                                                     |
| `EPINIO_TOKEN`             |                                           | OIDC access token (takes precedence over basic auth)                          |
| `EPINIO_REFRESH_TOKEN`     |                                           | OIDC refresh token (enables auto-refresh)                                     |
| `EPINIO_TOKEN_ENDPOINT`    |                                           | OIDC token endpoint URL (required for refresh)                                |
| `EPINIO_OIDC_CLIENT_ID`    | `epinio-cli`                              | OIDC client ID                                                                |
| `EPINIO_MCP_APP_NAME`      | `epinio-mcp`                              | The MCP's own Epinio app name — used by `ConfigurationBindingReq` for self-checks |
| `EPINIO_MCP_APP_NAMESPACE` | `epinio`                                  | The MCP's own Epinio namespace                                                |
| `S3_CONFIG_PATH`           | `/configurations/epinio-s3-gateway`       | Dir where the bound s3-gateway configuration is mounted (set automatically when the config is bound) |
| `S3_ENDPOINT`              |                                           | Override for local dev — bypasses the mounted configuration                    |
| `S3_BUCKET` / `S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` / `S3_USE_SSL` | | Set together with `S3_ENDPOINT` for local dev |
| `PORT`                     | `8080`                                    | HTTP listen port                                                              |

Per-request auth: when a Bearer token is sent on the `Authorization`
header, the MCP creates a dedicated server + Epinio client with that
token for the session. The env-var auth above is the fallback when no
per-request header is present.

## Running Locally

```bash
go run .
```

Or with custom configuration:

```bash
EPINIO_API_URL=https://epinio.example.com EPINIO_USERNAME=admin EPINIO_PASSWORD=secret go run .
```

## Deploying

Two install paths:

### `epinio push` (Epinio-managed)

Epinio handles the lifecycle (push, logs, restart, scale). A `Taskfile.yml`
at the repo root automates the full setup:

```bash
# Edit epinio.yml with your cluster credentials first
task setup          # cluster-prep → s3-service → configure-s3 → push → verify
```

Individual steps for partial runs or re-runs:

```bash
task cluster-prep   # (cluster-admin, once) manifests + namespace label
task s3-service     # create + wait for epinio-s3-gateway service
task configure-s3   # create the epinio-s3-gateway Epinio configuration
task push           # epinio push from repo root
task verify         # smoke-test /healthz and /readyz
```

See [`INSTALL.md`](INSTALL.md) for the full step-by-step walkthrough.

### `kubectl apply` (adopted)

One manifest drops the MCP into the `epinio` namespace without an
`epinio push`. The MCP finishes its own setup via conversation:

```bash
# 1. Edit install/epinio-mcp.yaml — set image tag + auth credentials
$EDITOR install/epinio-mcp.yaml

# 2. Apply
kubectl apply -f install/epinio-mcp.yaml
kubectl -n epinio rollout status deployment/epinio-mcp

# 3. Chat with the MCP:
#    "Run enable_capability for self_adoption, then for app_editing."
```

See [`install/README.md`](install/README.md) for RBAC details, upgrade,
and uninstall.

> **Image availability:** `ghcr.io/epinio/mcp` is published on
> release tags. Until the first tag lands, build locally:
> `docker build -f install/Dockerfile -t <tag> .`
