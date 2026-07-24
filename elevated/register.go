package elevated

import (
	"context"

	"github.com/epinio/mcp/client"
	"github.com/epinio/mcp/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RegisterReadOnly registers the elevated read-only tier: capability reporting
// (check_capabilities / enable_capability) and connection info for log
// streaming. Opt-in via EPINIO_MCP_ELEVATED.
//
// Source retrieval used to live here (blobuid lookup + direct S3 fetch), but as
// of Epinio 1.14.1 it wires to the GET .../applications/:app/source endpoint and
// is a plain core tool — no Kubernetes, no S3, no elevated RBAC.
func RegisterReadOnly(server *mcp.Server, c *client.Client) {
	RegisterCapabilityTools(server, c)
	RegisterConnectionInfoTools(server, c)
}

// RegisterAdoption registers the elevated read-write adoption tier
// (adopt_app / reconcile_app / release_app). These patch arbitrary
// Deployments/Services and write Secrets directly — the broad-RBAC profile,
// opt-in via EPINIO_MCP_ELEVATED_ADOPTION. It also installs the adopted-app
// mutation guard into the core tools so destructive Epinio operations are
// refused on adopted (kubectl-managed) apps.
func RegisterAdoption(server *mcp.Server, c *client.Client) {
	RegisterAdoptionTools(server, c)
	tools.SetAppMutationGuard(adoptionGuard{})
}

// adoptionGuard implements tools.AppMutationGuard by deferring to
// EnsureNotAdopted. Installed only when the adoption tier is active, so a
// pure-core or read-only deployment keeps the default permissive guard.
type adoptionGuard struct{}

func (adoptionGuard) EnsureMutable(ctx context.Context, namespace, name, operation string) error {
	return EnsureNotAdopted(ctx, namespace, name, operation)
}
