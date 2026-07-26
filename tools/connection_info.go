package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/epinio/mcp/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// get_connection_info hands a caller a ready-to-dial WebSocket URL for
// streaming an app's logs directly over Epinio's log stream, as an alternative
// to proxying through app_logs. It's a plain Epinio-API tool: Epinio's
// /authtoken endpoint mints a short-lived token that BuildLogStreamURL embeds
// in the URL. No Kubernetes access — hence core, not elevated.

type GetConnectionInfoInput struct {
	Namespace string `json:"namespace" jsonschema:"app namespace"`
	Name      string `json:"name" jsonschema:"app name"`
	Follow    *bool  `json:"follow,omitempty" jsonschema:"tail new lines (default true)"`
	StageID   string `json:"stage_id,omitempty" jsonschema:"tail the staging build's logs instead of runtime pods"`
}

type GetConnectionInfoOutput struct {
	Target    string `json:"target"` // "namespace/name"
	Protocol  string `json:"protocol"`
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at,omitempty"` // RFC3339 UTC
	Note      string `json:"note,omitempty"`
}

func RegisterConnectionInfoTools(server *mcp.Server, c *client.Client) {
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "get_connection_info",
			Annotations: &mcp.ToolAnnotations{Title: "Get Connection Info"},
			Description: "Return a ready-to-dial WebSocket URL for streaming an " +
				"application's logs directly (Epinio's log stream), as an alternative " +
				"to proxying through app_logs. The URL embeds a short-lived authtoken, " +
				"so dial it promptly. Pass stage_id to stream a staging build instead " +
				"of runtime pods.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input GetConnectionInfoInput,
		) (*mcp.CallToolResult, GetConnectionInfoOutput, error) {
			if input.Namespace == "" || input.Name == "" {
				return nil, GetConnectionInfoOutput{}, fmt.Errorf("namespace and name are required")
			}
			follow := true
			if input.Follow != nil {
				follow = *input.Follow
			}
			// Epinio's WS accepts only its own short-lived (~30s) ?authtoken=,
			// never the OIDC bearer. BuildLogStreamURL fetches it and picks the
			// right route (runtime pods vs staging build).
			wsURL, err := c.BuildLogStreamURL(
				input.Namespace,
				input.Name,
				input.StageID,
				follow,
			)

			if err != nil {
				return nil, GetConnectionInfoOutput{}, fmt.Errorf("build log stream URL: %w", err)
			}
			out := GetConnectionInfoOutput{
				Target:    input.Namespace + "/" + input.Name,
				Protocol:  "websocket",
				URL:       wsURL,
				ExpiresAt: time.Now().UTC().Add(30 * time.Second).Format(time.RFC3339),
				Note: "The authtoken embedded in the URL expires in ~30s — dial " +
					"immediately. After the handshake the WS stays open; on failure " +
					"call get_connection_info again for a fresh URL, or fall back to app_logs.",
			}
			if input.StageID != "" {
				out.Note = "Staging log stream for stage " + input.StageID + ". " + out.Note
			}
			return nil, out, nil
		},
	)
}
