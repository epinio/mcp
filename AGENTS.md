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
- **Epinio API client:** custom, in `client/` — REST + WebSocket (logs). Pure Epinio API, no cloud/k8s deps.
- **Kubernetes client:** `k8s.io/client-go` (in `elevated/` only) — dynamic CRD client + typed client for `SelfSubjectAccessReview`, used by the opt-in adoption tier. The core server has no Kubernetes or cloud SDK dependencies.
- **Target Platform:** Epinio on Kubernetes (Rancher Desktop or minikube for local dev; any Epinio-capable cluster for staging)

## Project Structure

The server is split into a pure-Epinio-API **core** and an opt-in **elevated**
package that reaches directly into Kubernetes. The core has no k8s/cloud deps.

```text
epinio-mcp/
├── main.go                  # Server setup; /healthz + /readyz + MCP Streamable HTTP;
│                            #   flag-gated registration of the elevated tiers
├── client/                  # Pure Epinio REST + WS client (no k8s, no cloud SDKs)
│   ├── client.go            # REST + WS client, OIDC refresh, authtoken, GetAppSource
│   └── types.go             # Request/response types mirroring Epinio models
├── tools/                   # Core tools — wire only to the Epinio API
│   ├── register.go          # RegisterCore fan-out
│   ├── guard.go             # AppMutationGuard seam (elevated adoption injects the real guard)
│   ├── info.go              # epinio_info
│   ├── namespaces.go        # namespaces
│   ├── apps.go              # list/show/create/delete/restart/scale/update
│   │                        #   (mutating tools consult appMutationGuard)
│   ├── push.go              # push_app / upload_and_stage / deploy_staged
│   ├── source.go            # get_app_source / list_app_files (via GET .../source API)
│   ├── logs.go              # app_logs
│   ├── env.go               # env vars
│   ├── configurations.go    # configurations + bindings
│   ├── services.go          # services + catalog (list/show)
│   ├── clone.go             # get_app_manifest + clone_app
│   ├── appcharts.go         # list_appcharts / show_appchart
│   ├── builders.go          # list_builders
│   └── guidance.go          # MCP Prompts + get_build_guidance
├── elevated/                # Opt-in tiers (EPINIO_MCP_ELEVATED*) — direct Kubernetes
│   ├── register.go          # RegisterReadOnly + RegisterAdoption (+ guard injection)
│   ├── kube.go              # In-cluster K8s client (dynamic CRD + SSAR + workload patch)
│   ├── capabilities.go      # Capability model + check/enable (log_streaming, self_adoption)
│   ├── connection_info.go   # get_connection_info (broker pattern)
│   └── adoption.go          # adopt_app / reconcile_app / release_app + EnsureNotAdopted
├── Makefile                 # Single tooling entry point: build/test/lint + install targets
├── install/                  # kubectl apply install path (alternative escape hatch)
│   ├── Dockerfile           # Multi-stage Go → distroless-nonroot
│   ├── epinio-mcp.yaml      # One-shot manifest: SA + RBAC + Secrets + Deployment + Service + App CRD
│   └── epinio-mcp-broad-rbac.yaml  # Optional: cluster-wide write verbs for cross-namespace adoption
├── manifests/                # Cluster prereqs for the elevated install (make cluster-prep)
│   ├── chart-server.yaml    # pod serving the custom Helm chart
│   ├── epinio-mcp-rbac.yaml
│   └── standard-elevated-appchart.yaml  # AppChart with elevated RBAC (adoption)
├── appcharts/                # Custom AppChart source (standard-elevated)
│   └── standard-elevated/
├── epinio.yml               # Core push deploy config (standard appchart)
├── epinio-elevated.yml      # Elevated push deploy config (standard-elevated + flags)
├── .epinioignore            # Excludes build artifacts + .git/ from push
├── skills/                  # Portable agent skill (MCP stand-in via the epinio CLI)
│   └── epinio-cli/          # Copy to ~/.cursor/skills/epinio-cli/ (see README)
│       ├── SKILL.md         # MCP tool → epinio CLI map
│       └── guidance.md      # Deploy / staging guidance (get_build_guidance stand-in)
├── LICENSE                  # Apache-2.0
├── AGENTS.md                # This file
└── README.md                # Project overview; links to docs.epinio.io
```

(CI workflows under `.github/workflows/` are still to be added.)

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

The opt-in elevated tier uses a `Capability` / `Requirement` model in
`elevated/capabilities.go` to report and fulfill readiness of features that
depend on cluster infrastructure (K8s RBAC, Dex config, log-stream ingress).
It registers only when `EPINIO_MCP_ELEVATED` is set.

Current capabilities:
- **`log_streaming`** — advertises WebSocket reachability for live log tailing
- **`self_adoption`** — the MCP's own App CRD is complete and adopted

Source retrieval (`get_app_source` / `list_app_files`) is **not** a gated
capability. As of Epinio 1.14.1 it wires to the `GET .../source` API and is a
plain core tool — no S3, no K8s, no elevated RBAC.

- **`check_capabilities`** — pure diagnostic (safe, read-only)
- **`enable_capability`** — invokes each requirement's `Fulfill` path when
  possible; reports `needs_admin` for anything outside the MCP's auth envelope
- **`get_connection_info`** — broker pattern: for capabilities whose backing
  service supports direct connections (currently `log_streaming`), hands back a
  ready-to-dial URL with a short-lived auth token embedded

The capability types and requirement checks live in `elevated/capabilities.go`.

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
