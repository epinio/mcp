package elevated

import (
	"context"

	"github.com/epinio/mcp/client"
	"github.com/epinio/mcp/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Register wires the elevated tier — the single opt-in set of capabilities that
// reach directly into Kubernetes: workload adoption (adopt_app / reconcile_app
// / release_app) and the capability framework (check_capabilities /
// enable_capability, including the MCP's own self-adoption). Enabled via the
// EPINIO_MCP_ELEVATED flag, and requires the standard-elevated appchart's RBAC.
//
// The adopted-app mutation guard is installed separately and once, at startup,
// via AdoptionGuard (see main) — never here, because Register runs per request
// and the guard is a process-global.
//
// NOTE: source retrieval and log-stream connection info are NOT elevated — they
// wire to the Epinio API and live in the core tools package.
func Register(server *mcp.Server, c *client.Client) {
	RegisterCapabilityTools(server, c)
	RegisterAdoptionTools(server, c)
}

// AdoptionGuard returns the guard that refuses destructive Epinio operations on
// adopted (kubectl-managed) apps. main installs it once at startup when the
// elevated tier is enabled, so a pure-core deployment keeps the default
// permissive guard.
func AdoptionGuard() tools.AppMutationGuard { return adoptionGuard{} }

// adoptionGuard implements tools.AppMutationGuard by deferring to
// EnsureNotAdopted.
type adoptionGuard struct{}

func (adoptionGuard) EnsureMutable(
	ctx context.Context,
	namespace, name, operation string,
) error {
	return EnsureNotAdopted(ctx, namespace, name, operation)
}
