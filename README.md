# Epinio MCP Server

> **Beta.** Tool names, capabilities, and install steps may still change. Not yet
> recommended for production.

A [Model Context Protocol](https://modelcontextprotocol.io) server that exposes
the [Epinio](https://epinio.io) API as tools for AI agents such as Claude. It
runs on your cluster and translates MCP tool calls into Epinio REST API requests,
so an agent can deploy and manage applications through conversation.

By default the server wires only to the Epinio API, running as the calling user — app lifecycle, logs, source retrieval, inner-loop app watch sync, and CRUD for app charts, builder images, and catalog services. A single elevated capability set that reaches directly into Kubernetes — workload adoption — is off by default and opt-in via the EPINIO_MCP_ELEVATED flag. See the reference docs.

**Requires Epinio 1.14.1 or later.** The server depends on the builder-image,
catalog-service, and app-chart CRUD API and the source-retrieval endpoint, all
introduced in Epinio 1.14.1; it will not work against earlier releases.

## Documentation

Full documentation lives at **[docs.epinio.io](https://docs.epinio.io)**:

- **[Install the MCP server](https://docs.epinio.io/getting-started/install-mcp)** — prerequisites, configuration, and deployment.
- **[MCP server reference](https://docs.epinio.io/reference/mcp)** — the full tool list, optional capabilities, and health probes.

## Authentication

Every request must carry a credential that Epinio itself accepts, and there is
no anonymous or server-credential fallback: a caller without an Epinio account
(a local user, or an identity from Dex) gets a 401 and no tools. The server
holds no credentials of its own, and each tool call runs as the caller.

For Claude and other OAuth-capable clients, the server is an OAuth 2.0
protected resource. It answers an unauthenticated request with a 401 and a
`WWW-Authenticate` header pointing at its metadata
(`/.well-known/oauth-protected-resource`), which names your Dex as the
authorization server. The client then runs a normal login and sends the
resulting token.

Two settings are required, and the server refuses to start without them:

| Variable | Meaning |
|---|---|
| `EPINIO_MCP_RESOURCE_URL` | The URL users enter in their MCP client, exactly. Clients compare it against the metadata and refuse a mismatch. |
| `EPINIO_MCP_OIDC_ISSUER` | The Dex issuer in front of your Epinio, normally `https://auth.<your-domain>`. |

Your Dex also needs a client for the tool, registered through the Epinio
chart's `dex.extraClients` value. Clients listed there become trusted peers of
`epinio-api`, which is what lets Dex issue a token the Epinio API will accept.
For Claude, register the redirect URI `https://claude.ai/api/mcp/auth_callback`.

Claude Code needs one more step. It is a native client on a loopback port, and
Dex matches registered redirect URIs exactly, so pin the port on both sides:
register `http://localhost:3118/callback` (and the `127.0.0.1` form) in Dex,
then add the server with a matching port. Dex supports neither dynamic client
registration nor CIMD, so pass the client ID explicitly; no secret is needed,
because the client is public and Claude Code uses PKCE:

```bash
claude mcp add --transport http \
  --client-id claude-mcp --callback-port 3118 \
  epinio https://epinio-mcp.example.com
```

## Install

Clone the repo, set your cluster details in `epinio.yml`, then:

```bash
make setup     # push the MCP to Epinio and smoke-test it
```

That is the core install — a pure Epinio-API server. To turn on the opt-in
elevated tier (workload **adoption**, which reaches directly into Kubernetes),
edit `epinio-elevated.yml` and run `make elevated-setup` instead.

See the [install guide](https://docs.epinio.io/getting-started/install-mcp) for
prerequisites and configuration, and `make help` for all targets.

## Development

```bash
make build     # build dist/epinio-mcp with version stamped in
make test      # unit tests with the race detector
make check     # fmt-check + vet + lint + test (the CI gate)
make help      # list all targets
```

To run the server against a cluster locally:

```bash
EPINIO_API_URL=https://epinio.example.com EPINIO_USERNAME=admin EPINIO_PASSWORD=secret make run
```

## License

Apache-2.0 — see [LICENSE](LICENSE).
