package tools

import (
	"github.com/epinio/mcp/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RegisterCore registers the core Epinio tool groups — everything that wires
// purely to the Epinio REST API, running as the caller's identity. The
// elevated, opt-in capabilities (direct Kubernetes access) live in the
// elevated package and are registered separately by main when explicitly
// enabled via the EPINIO_MCP_ELEVATED* flags.
func RegisterCore(server *mcp.Server, c *client.Client) {
	RegisterInfoTools(server, c)
	RegisterNamespaceTools(server, c)
	RegisterAppTools(server, c)
	RegisterEnvTools(server, c)
	RegisterConfigurationTools(server, c)
	RegisterServiceTools(server, c)
	RegisterLogTools(server, c)
	RegisterPushTools(server, c)
	RegisterCloneTools(server, c)
	RegisterAppSourceTools(server, c)
	RegisterAppChartTools(server, c)
	RegisterBuilderTools(server, c)
	RegisterGuidanceTools(server, c)
}
