package tools

import (
	"context"
	"fmt"

	"github.com/epinio/mcp/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ListEnvInput struct {
	Namespace string `json:"namespace" jsonschema:"the namespace of the app"`
	App       string `json:"app" jsonschema:"the app name"`
}
type ListEnvOutput struct {
	Variables []EnvVar `json:"variables"`
}
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type SetEnvInput struct {
	Namespace string            `json:"namespace" jsonschema:"the namespace of the app"`
	App       string            `json:"app" jsonschema:"the app name"`
	Env       map[string]string `json:"env" jsonschema:"key-value pairs of environment variables to set"`
}
type SetEnvOutput struct {
	Status string `json:"status"`
}

type UnsetEnvInput struct {
	Namespace string `json:"namespace" jsonschema:"the namespace of the app"`
	App       string `json:"app" jsonschema:"the app name"`
	Name      string `json:"name" jsonschema:"the environment variable name to remove"`
}
type UnsetEnvOutput struct {
	Status string `json:"status"`
}

func RegisterEnvTools(server *mcp.Server, c *client.Client) {
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "list_env",
			Annotations: &mcp.ToolAnnotations{Title: "List Environment Variables"},
			Description: "List environment variables for an Epinio application",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input ListEnvInput,
		) (*mcp.CallToolResult, ListEnvOutput, error) {
			vars, err := c.ListEnv(input.Namespace, input.App)
			if err != nil {
				return nil, ListEnvOutput{}, fmt.Errorf("list env: %w", err)
			}
			var out []EnvVar
			for _, v := range vars {
				out = append(out, EnvVar{Name: v.Name, Value: v.Value})
			}
			return nil, ListEnvOutput{Variables: out}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "set_env",
			Annotations: &mcp.ToolAnnotations{Title: "Set Environment Variables"},
			Description: "Set environment variables on an Epinio application",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input SetEnvInput,
		) (*mcp.CallToolResult, SetEnvOutput, error) {
			if err := c.SetEnv(input.Namespace, input.App, input.Env); err != nil {
				return nil, SetEnvOutput{}, fmt.Errorf("set env: %w", err)
			}
			return nil, SetEnvOutput{
				Status: fmt.Sprintf(
					"set %d env vars on %s/%s",
					len(input.Env),
					input.Namespace,
					input.App,
				),
			}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "unset_env",
			Annotations: &mcp.ToolAnnotations{Title: "Remove Environment Variable"},
			Description: "Remove an environment variable from an Epinio application",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input UnsetEnvInput,
		) (*mcp.CallToolResult, UnsetEnvOutput, error) {
			if err := c.UnsetEnv(input.Namespace, input.App, input.Name); err != nil {
				return nil, UnsetEnvOutput{}, fmt.Errorf("unset env: %w", err)
			}
			return nil, UnsetEnvOutput{
				Status: fmt.Sprintf(
					"removed %q from %s/%s",
					input.Name,
					input.Namespace,
					input.App,
				),
			}, nil
		},
	)
}
