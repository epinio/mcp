package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/epinio/mcp/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type AppLogsInput struct {
	Namespace string `json:"namespace" jsonschema:"the namespace of the app"`
	Name      string `json:"name" jsonschema:"the app name"`
	StageID   string `json:"stage_id,omitempty" jsonschema:"optional stage ID to get staging/build logs instead of runtime logs"`
}
type AppLogsOutput struct {
	Lines []string `json:"lines"`
	Count int      `json:"count"`
}

func RegisterLogTools(server *mcp.Server, c *client.Client) {
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "app_logs",
			Annotations: &mcp.ToolAnnotations{Title: "App Logs"},
			Description: "Fetch recent logs from an Epinio application. " +
				"Returns runtime logs by default, or staging/build logs " +
				"if a stage_id is provided.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input AppLogsInput,
		) (*mcp.CallToolResult, AppLogsOutput, error) {
			lines, err := c.AppLogs(
				input.Namespace,
				input.Name,
				input.StageID,
				false,
			)

			if err != nil {
				return nil, AppLogsOutput{}, fmt.Errorf("fetch logs: %w", err)
			}

			if len(lines) > 100 {
				lines = lines[len(lines)-100:]
			}

			var cleaned []string
			for _, l := range lines {
				l = strings.TrimSpace(l)
				if l != "" {
					cleaned = append(cleaned, l)
				}
			}

			return nil, AppLogsOutput{Lines: cleaned, Count: len(cleaned)}, nil
		},
	)
}
