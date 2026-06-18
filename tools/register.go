package tools

import (
	"github.com/epinio/mcp/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RegisterAll registers all Epinio tool groups on the given server.
func RegisterAll(server *mcp.Server, c *client.Client) {
	RegisterInfoTools(server, c)
	RegisterNamespaceTools(server, c)
	RegisterAppTools(server, c)
	RegisterEnvTools(server, c)
	RegisterConfigurationTools(server, c)
	RegisterServiceTools(server, c)
	RegisterLogTools(server, c)
	RegisterPushTools(server, c)
	RegisterCloneTools(server, c)
	RegisterCapabilityTools(server, c)
	RegisterAppSourceTools(server, c)
	RegisterConnectionInfoTools(server, c)
	RegisterAppChartTools(server, c)
	RegisterBuilderTools(server, c)
	RegisterGuidanceTools(server, c)
	RegisterAdoptionTools(server, c)
}
