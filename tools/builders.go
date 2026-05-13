package tools

import (
	"context"

	"github.com/krumware/epinio-mcp/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// BuilderInfo describes a known Cloud Native Buildpacks builder image that
// agents can pass to push_app / upload_and_stage via `builder_image`. This
// list is curated — it does not introspect OCI image metadata. Keep it
// aligned with the images actually usable on the target cluster.
type BuilderInfo struct {
	Image    string   `json:"image"`
	Supports []string `json:"supports"`
	Default  bool     `json:"default,omitempty"`
}

type ListBuildersInput struct{}
type ListBuildersOutput struct {
	Builders []BuilderInfo `json:"builders"`
}

// curatedBuilders is the static list returned by list_builders. Add entries
// here when a new builder image becomes usable on-cluster.
var curatedBuilders = []BuilderInfo{
	{
		Image:    "paketobuildpacks/builder-jammy-full:0.3.606",
		Supports: []string{"node", "go", "python", "java", "ruby", ".net"},
		Default:  true,
	},
	{
		Image:    "krumware/builder-rancher-ext:1.0.26",
		Supports: []string{"node", "rancher-extension"},
	},
}

func RegisterBuilderTools(server *mcp.Server, _ *client.Client) {
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "list_builders",
			Annotations: &mcp.ToolAnnotations{Title: "List Builders"},
			Description: "List Cloud Native Buildpacks builder images known to " +
				"be usable with this Epinio cluster, including the " +
				"language/framework ecosystems each supports. Pass the chosen " +
				"`image` to push_app / upload_and_stage via the `builder_image` " +
				"parameter. The entry marked `default: true` matches what " +
				"epinio_info reports — use it when no specific builder is required.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			_ ListBuildersInput,
		) (*mcp.CallToolResult, ListBuildersOutput, error) {
			out := make([]BuilderInfo, len(curatedBuilders))
			copy(out, curatedBuilders)
			return nil, ListBuildersOutput{Builders: out}, nil
		},
	)
}
