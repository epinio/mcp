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
