package tools

import (
	"context"
	"fmt"

	"github.com/epinio/mcp/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type InfoInput struct{}
type InfoOutput struct {
	Version             string `json:"version"`
	KubeVersion         string `json:"kube_version"`
	Platform            string `json:"platform"`
	DefaultBuilderImage string `json:"default_builder_image"`
}

func RegisterInfoTools(server *mcp.Server, c *client.Client) {
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "epinio_info",
			Annotations: &mcp.ToolAnnotations{Title: "Epinio Info"},
			Description: "Get Epinio server information including " +
				"version, Kubernetes version, and platform",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input InfoInput,
		) (*mcp.CallToolResult, InfoOutput, error) {
			info, err := c.Info()
			if err != nil {
				return nil, InfoOutput{}, fmt.Errorf("get info: %w", err)
			}
			return nil, InfoOutput{
				Version:             info.Version,
				KubeVersion:         info.KubeVersion,
				Platform:            info.Platform,
				DefaultBuilderImage: info.DefaultBuilderImage,
			}, nil
		},
	)
}
