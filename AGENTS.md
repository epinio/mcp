# Agents Guide — epinio-mcp

## Project Conventions

- **AGENTS.md** is used instead of CLAUDE.md for agent instructions.
- **README.md** is kept up-to-date with every meaningful change.
- Root-level doc filenames are CAPITALIZED (e.g., `AGENTS.md`, not `agents.md`).
- No `Co-Authored-By` lines in git commit messages.

## Agent Behavior

- Be inquisitive — ask questions when intent is ambiguous.
- Move fast — take action within reason inside this project folder.
- Keep docs current — README and AGENTS must reflect reality at all times.
- Prefer simplicity — don't over-engineer; build incrementally.

## Tech Stack

- **Language:** Go 1.25
- **MCP SDK:** `github.com/modelcontextprotocol/go-sdk` (official, v1.4.1)
- **Transport:** Streamable HTTP; `/healthz` + `/readyz` sidecar via `http.ServeMux`
- **Epinio API client:** custom, in `client/` — REST + WebSocket (logs)
- **AWS S3 SDK:** `github.com/aws/aws-sdk-go-v2/...` (for `app_editing` capability — reads blobs from the SeaweedFS gateway)
- **Kubernetes client:** `k8s.io/client-go` — dynamic CRD client for Epinio Application reads, typed client for `SelfSubjectAccessReview`
- **Target Platform:** Epinio on Kubernetes (Rancher Desktop or minikube for local dev; any Epinio-capable cluster for staging)

## Project Structure

```text
epinio-mcp/
├── main.go                  # Server setup; /healthz + /readyz + MCP Streamable HTTP
├── client/
│   ├── client.go            # Epinio REST + WS client, OIDC refresh, authtoken helper
│   ├── types.go             # Request/response types mirroring Epinio models
│   ├── k8s.go               # In-cluster K8s client (dynamic CRD reads + SSAR)
│   └── s3.go                # aws-sdk-go-v2 wrapper for the Epinio s3-gateway
├── tools/
│   ├── register.go          # RegisterAll fan-out
│   ├── info.go              # epinio_info
│   ├── namespaces.go        # namespaces
│   ├── apps.go              # list/show/create/delete/restart/scale/update
│   │                        #   (show_app carries adopted/advisory decoration;
│   │                        #    write tools gated by EnsureNotAdopted)
│   ├── push.go              # push_app / upload_and_stage / deploy_staged
│   ├── logs.go              # app_logs
│   ├── env.go               # env vars
│   ├── configurations.go    # configurations + bindings (adopted-mode guarded)
│   ├── services.go          # services + catalog (list/show)
│   ├── clone.go             # manifest + clone
│   ├── capabilities.go      # Capability model + check/enable + requireCapability gate
│   │                        #   (S3AccessReq, SelfAdoptionReq, self_adoption)
│   ├── app_source.go        # get_app_source / list_app_files (gated by app_editing)
│   ├── adoption.go          # adopt_app / reconcile_app / release_app + EnsureNotAdopted
│   ├── appcharts.go         # list_appcharts / show_appchart
│   ├── builders.go          # list_builders
│   ├── guidance.go          # MCP Prompts + get_build_guidance
│   └── connection_info.go   # get_connection_info (broker pattern)
├── install/                  # kubectl apply install path (alternative)
│   ├── Dockerfile           # Multi-stage Go → distroless-nonroot
│   ├── epinio-mcp.yaml      # One-shot manifest: SA + RBAC + Secrets + Deployment + Service + App CRD
│   ├── epinio-mcp-broad-rbac.yaml  # Optional: cluster-wide write verbs for cross-namespace adoption
│   └── README.md            # Install/bootstrap/upgrade/uninstall flow
├── .github/workflows/
│   └── docker-publish.yml   # Build + publish to ghcr.io on tag/main
├── manifests/                # Pre-requisites for `epinio push` install
│   ├── chart-server.yaml    # nginx pod serving the custom Helm chart
│   ├── epinio-mcp-rbac.yaml
│   ├── standard-elevated-appchart.yaml  # AppChart with elevated RBAC for source retrieval
│   └── s3-gateway-catalog-entry.yaml    # Epinio catalog entry for the S3 gateway
├── appcharts/                # Custom AppChart source (standard-elevated)
│   └── standard-elevated/
├── epinio.yml               # Epinio push deploy config (primary install path)
├── .epinioignore            # Excludes build artifacts + .git/ from push
├── AGENTS.md                # This file
└── README.md                # Project overview and docs
```

## Epinio API Patterns

- Base path: `/api/v1`
- Auth: Bearer token (OIDC) or Basic Auth (username/password)
- Namespaced resources use path pattern: `/namespaces/:namespace/resources/:resource`
- The client in `client/client.go` wraps all API calls with auth and error handling.
- Types in `client/types.go` mirror Epinio's Go model structs from `pkg/api/core/v1/models/`.
- Per-request auth: MCP server creates per-session clients from `Authorization` headers (see `main.go`).

## Dex OIDC Authentication

Epinio uses Dex as its IDP. Browser apps authenticate via OIDC Authorization Code + PKCE flow.

**Adding a new client for a sample app:**
1. Backup `dex-config` secret: `kubectl get secret dex-config -n epinio -o yaml > backup.yaml`
2. Decode config: `kubectl get secret dex-config -n epinio -o jsonpath='{.data.config\.yaml}' | base64 -d`
3. Add a `staticClients` entry (public, with `redirectURIs`) and add as `trustedPeers` of `epinio-api`
4. Patch the secret and restart Dex: `kubectl rollout restart deployment/dex -n epinio`

**Key details:**
- Dex URL: `epinio.{domain}` → `auth.{domain}`
- Token endpoint: `https://auth.{domain}/token`
- Scopes: `openid offline_access profile email groups federated:id audience:server:client_id:epinio-api`
- Code challenge method: S256
- Dex v2.37 does not support CRD-based client registration — all clients live in the `dex-config` secret
- Existing standard client IDs: `epinio-api`, `epinio-cli`, `epinio-ui`, `rancher-dashboard`

## MCP Tool Patterns

- Each tool group lives in its own file under `tools/`.
- Input/output types are Go structs with `json` and `jsonschema` struct tags.
- The SDK auto-generates JSON Schema from the Go types.
- Tool handlers return `(nil, output, nil)` for success — SDK wraps the output.
- Errors returned from handlers are surfaced as tool errors (`IsError: true`).
- Registration functions follow the pattern `RegisterXxxTools(server, client)`.
- Per-request auth: `main.go` checks the `Authorization: Bearer ...` header on
  each MCP request and spins a dedicated server + client for that session with
  the user's token. Server-level env-var auth is the fallback when no header.

## Capability Model

Optional features that depend on external infrastructure (S3 gateway, K8s
RBAC, Dex config, etc.) are gated via the `Capability` / `Requirement` model
in `tools/capabilities.go`. Tools that need extras (e.g. `get_app_source`)
call `requireCapability(ctx, c, "app_editing", ...)` at the top of their
handler — returns a structured error if the prereqs aren't satisfied.

- **`check_capabilities`** — pure diagnostic (safe, read-only)
- **`enable_capability`** — invokes each requirement's `Fulfill` path (creates
  service instances, binds configurations) when possible; reports
  `needs_admin` for anything outside the MCP's auth envelope
- **`get_connection_info`** — broker pattern: for capabilities whose backing
  service supports direct-browser connections (currently `log_streaming`),
  hands back a ready-to-dial URL with a short-lived auth token embedded

The capability types and requirement checks are implemented in `tools/capabilities.go`.

## Staging Status Signal

`list_apps` and `show_app` expose Epinio's `staging_status` field (values:
`"active"`, `"done"`, `"failed"`, or empty). It comes straight from Epinio's
view of the Kubernetes staging Job — appsmith's Apps tab pulses "Staging"
when it sees `"active"`, which is accurate even on first load (no heuristic
windows). Empty-string means no recent staging activity.

## Log Streaming

Epinio's WS log endpoint rejects OIDC bearer tokens as "malformed token
format". Instead, clients must:

1. `GET /api/v1/authtoken` with the bearer header → returns a short-lived
   token (~30s TTL, per `helpers/authtoken/token.go` in the Epinio repo)
2. Pass that token as `?authtoken=<X>` on the WS URL

The `client.BuildLogStreamURL()` helper and `get_connection_info` tool both
use this flow. Staging logs use a different URL path entirely:
`/namespaces/:ns/staging/:stage_id/logs` (no app name).

## Related Epinio Source

The Epinio repo is cloned at `~/code/epinio-new/epinio` on dev machines.
Reach for it when you need to see how the upstream CLI/server handles a
flow (auth, WS, stage jobs). `~/code/epinio-new/helm-charts/chart/epinio`
has the Helm chart for cluster install/upgrade shape.
