package tools

import (
	"context"
	"fmt"

	"github.com/epinio/mcp/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ListConfigsInput struct {
	Namespace string `json:"namespace" jsonschema:"the namespace to list configurations in"`
}
type ListConfigsOutput struct {
	Configurations []ConfigSummary `json:"configurations"`
}
type ConfigSummary struct {
	Name      string   `json:"name"`
	Namespace string   `json:"namespace"`
	Type      string   `json:"type"`
	Origin    string   `json:"origin"`
	BoundApps []string `json:"bound_apps,omitempty"`
	CreatedAt string   `json:"created_at"`
}

type CreateConfigInput struct {
	Namespace string            `json:"namespace" jsonschema:"the namespace to create the configuration in"`
	Name      string            `json:"name" jsonschema:"the configuration name"`
	Data      map[string]string `json:"data" jsonschema:"key-value pairs of configuration data"`
}
type CreateConfigOutput struct {
	Status string `json:"status"`
}

type DeleteConfigInput struct {
	Namespace string `json:"namespace" jsonschema:"the namespace of the configuration"`
	Name      string `json:"name" jsonschema:"the configuration name to delete"`
}
type DeleteConfigOutput struct {
	Status string `json:"status"`
}

type BindConfigInput struct {
	Namespace      string   `json:"namespace" jsonschema:"the namespace of the app"`
	App            string   `json:"app" jsonschema:"the app to bind configurations to"`
	Configurations []string `json:"configurations" jsonschema:"configuration names to bind"`
}
type BindConfigOutput struct {
	Status string `json:"status"`
}

type UnbindConfigInput struct {
	Namespace     string `json:"namespace" jsonschema:"the namespace of the app"`
	App           string `json:"app" jsonschema:"the app to unbind the configuration from"`
	Configuration string `json:"configuration" jsonschema:"the configuration name to unbind"`
}
type UnbindConfigOutput struct {
	Status string `json:"status"`
}

func RegisterConfigurationTools(server *mcp.Server, c *client.Client) {
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "list_configurations",
			Annotations: &mcp.ToolAnnotations{Title: "List Configurations"},
			Description: "List configurations in an Epinio namespace",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input ListConfigsInput,
		) (*mcp.CallToolResult, ListConfigsOutput, error) {
			configs, err := c.ListConfigurations(input.Namespace)
			if err != nil {
				return nil, ListConfigsOutput{}, fmt.Errorf("list configurations: %w", err)
			}
			var summaries []ConfigSummary
			for _, cfg := range configs {
				summaries = append(summaries, ConfigSummary{
					Name:      cfg.Meta.Name,
					Namespace: cfg.Meta.Namespace,
					Type:      cfg.Configuration.Type,
					Origin:    cfg.Configuration.Origin,
					BoundApps: cfg.Configuration.BoundApps,
					CreatedAt: cfg.Meta.CreatedAt,
				})
			}
			return nil, ListConfigsOutput{Configurations: summaries}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "create_configuration",
			Annotations: &mcp.ToolAnnotations{Title: "Create Configuration"},
			Description: "Create a new configuration (key-value data) in an Epinio namespace",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input CreateConfigInput,
		) (*mcp.CallToolResult, CreateConfigOutput, error) {
			if err := c.CreateConfiguration(input.Namespace, client.ConfigurationCreateRequest{
				Name: input.Name,
				Data: input.Data,
			}); err != nil {
				return nil, CreateConfigOutput{}, fmt.Errorf("create configuration: %w", err)
			}
			return nil, CreateConfigOutput{
				Status: fmt.Sprintf(
					"configuration %q created in namespace %q",
					input.Name,
					input.Namespace,
				),
			}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "delete_configuration",
			Annotations: &mcp.ToolAnnotations{Title: "Delete Configuration"},
			Description: "Delete a configuration from an Epinio namespace",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input DeleteConfigInput,
		) (*mcp.CallToolResult, DeleteConfigOutput, error) {
			if err := c.DeleteConfiguration(input.Namespace, input.Name); err != nil {
				return nil, DeleteConfigOutput{}, fmt.Errorf("delete configuration: %w", err)
			}
			return nil, DeleteConfigOutput{
				Status: fmt.Sprintf(
					"configuration %q deleted from namespace %q",
					input.Name,
					input.Namespace,
				),
			}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "bind_configuration",
			Annotations: &mcp.ToolAnnotations{Title: "Bind Configuration"},
			Description: "Bind one or more configurations to an Epinio application. " +
				"Refused on adopted apps — adopted apps don't have an Epinio-owned " +
				"Helm release to re-render, so binding has no effect. Write " +
				"credentials into the app's backing Secret directly via kubectl instead.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input BindConfigInput,
		) (*mcp.CallToolResult, BindConfigOutput, error) {
			if err := appMutationGuard.EnsureMutable(ctx, input.Namespace, input.App, "bind_configuration"); err != nil {
				return nil, BindConfigOutput{}, err
			}
			if err := c.BindConfiguration(input.Namespace, input.App, input.Configurations); err != nil {
				return nil, BindConfigOutput{}, fmt.Errorf("bind configuration: %w", err)
			}
			return nil, BindConfigOutput{
				Status: fmt.Sprintf(
					"bound %v to %s/%s",
					input.Configurations,
					input.Namespace,
					input.App,
				),
			}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "unbind_configuration",
			Annotations: &mcp.ToolAnnotations{Title: "Unbind Configuration"},
			Description: "Unbind a configuration from an Epinio application. " +
				"Refused on adopted apps.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input UnbindConfigInput,
		) (*mcp.CallToolResult, UnbindConfigOutput, error) {
			if err := appMutationGuard.EnsureMutable(ctx, input.Namespace, input.App, "unbind_configuration"); err != nil {
				return nil, UnbindConfigOutput{}, err
			}
			if err := c.UnbindConfiguration(input.Namespace, input.App, input.Configuration); err != nil {
				return nil, UnbindConfigOutput{}, fmt.Errorf("unbind configuration: %w", err)
			}
			return nil, UnbindConfigOutput{
				Status: fmt.Sprintf(
					"unbound %q from %s/%s",
					input.Configuration,
					input.Namespace,
					input.App,
				),
			}, nil
		},
	)
}
