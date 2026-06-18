package tools

import (
	"context"
	"fmt"

	"github.com/epinio/mcp/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ListNamespacesInput struct{}
type ListNamespacesOutput struct {
	Namespaces []NamespaceSummary `json:"namespaces"`
}

type NamespaceSummary struct {
	Name           string   `json:"name"`
	CreatedAt      string   `json:"created_at"`
	Apps           []string `json:"apps"`
	Configurations []string `json:"configurations"`
}

type CreateNamespaceInput struct {
	Name string `json:"name" jsonschema:"the namespace name to create"`
}
type CreateNamespaceOutput struct {
	Status string `json:"status"`
}

type DeleteNamespaceInput struct {
	Name string `json:"name" jsonschema:"the namespace name to delete"`
}
type DeleteNamespaceOutput struct {
	Status string `json:"status"`
}

func RegisterNamespaceTools(server *mcp.Server, c *client.Client) {
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "list_namespaces",
			Annotations: &mcp.ToolAnnotations{Title: "List Namespaces"},
			Description: "List all Epinio namespaces with their apps and configurations",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input ListNamespacesInput,
		) (*mcp.CallToolResult, ListNamespacesOutput, error) {
			namespaces, err := c.ListNamespaces()
			if err != nil {
				return nil, ListNamespacesOutput{}, fmt.Errorf("list namespaces: %w", err)
			}
			var summaries []NamespaceSummary
			for _, ns := range namespaces {
				summaries = append(summaries, NamespaceSummary{
					Name:           ns.Meta.Name,
					CreatedAt:      ns.Meta.CreatedAt,
					Apps:           ns.Apps,
					Configurations: ns.Configurations,
				})
			}
			return nil, ListNamespacesOutput{Namespaces: summaries}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "create_namespace",
			Annotations: &mcp.ToolAnnotations{Title: "Create Namespace"},
			Description: "Create a new Epinio namespace",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input CreateNamespaceInput,
		) (*mcp.CallToolResult, CreateNamespaceOutput, error) {
			if err := c.CreateNamespace(input.Name); err != nil {
				return nil, CreateNamespaceOutput{}, fmt.Errorf("create namespace: %w", err)
			}
			return nil, CreateNamespaceOutput{
				Status: fmt.Sprintf("namespace %q created", input.Name),
			}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "delete_namespace",
			Annotations: &mcp.ToolAnnotations{Title: "Delete Namespace"},
			Description: "Delete an Epinio namespace and all its resources",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input DeleteNamespaceInput,
		) (*mcp.CallToolResult, DeleteNamespaceOutput, error) {
			if err := c.DeleteNamespace(input.Name); err != nil {
				return nil, DeleteNamespaceOutput{}, fmt.Errorf("delete namespace: %w", err)
			}
			return nil, DeleteNamespaceOutput{
				Status: fmt.Sprintf("namespace %q deleted", input.Name),
			}, nil
		},
	)
}
