package tools

import (
	"context"
	"fmt"

	"github.com/epinio/mcp/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetAppManifestInput struct {
	Namespace string `json:"namespace" jsonschema:"the namespace of the app"`
	Name      string `json:"name" jsonschema:"the app name"`
}

type GetAppManifestOutput struct {
	Name          string                          `json:"name"`
	Namespace     string                          `json:"namespace"`
	Status        string                          `json:"status"`
	ImageURL      string                          `json:"image_url"`
	StageID       string                          `json:"stage_id"`
	Routes        []string                        `json:"routes"`
	Configuration client.ApplicationConfiguration `json:"configuration"`
	Origin        string                          `json:"origin"`
}

type CloneAppInput struct {
	SourceNamespace string `json:"source_namespace" jsonschema:"namespace of the source app"`
	SourceName      string `json:"source_name" jsonschema:"name of the source app to clone"`
	TargetNamespace string `json:"target_namespace,omitempty" jsonschema:"namespace for the clone (defaults to source namespace)"`
	TargetName      string `json:"target_name" jsonschema:"name for the cloned app"`
}

type CloneAppOutput struct {
	Status string   `json:"status"`
	Routes []string `json:"routes"`
	Image  string   `json:"image"`
}

func RegisterCloneTools(server *mcp.Server, c *client.Client) {
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "get_app_manifest",
			Annotations: &mcp.ToolAnnotations{Title: "Get App Manifest"},
			Description: "Get the full manifest/configuration of an application " +
				"including image URL, stage ID, environment, routes, and settings. " +
				"Useful for inspecting app details before cloning or modifying.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input GetAppManifestInput,
		) (*mcp.CallToolResult, GetAppManifestOutput, error) {
			ns := input.Namespace
			if ns == "" {
				ns = "workspace"
			}
			app, err := c.ShowApp(ns, input.Name)

			if err != nil {
				return nil, GetAppManifestOutput{}, fmt.Errorf("get app: %w", err)
			}
			out := GetAppManifestOutput{
				Name:          app.Meta.Name,
				Namespace:     app.Meta.Namespace,
				Status:        app.Status,
				ImageURL:      app.ImageURL,
				StageID:       app.StageID,
				Routes:        app.Configuration.Routes,
				Configuration: app.Configuration,
				Origin:        originString(app.Origin),
			}
			return nil, out, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "clone_app",
			Annotations: &mcp.ToolAnnotations{Title: "Clone App"},
			Description: "Clone an existing application to a new name. Deploys " +
				"using the source app's built image — no rebuild needed. Copies " +
				"environment variables but generates new routes for the target app.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input CloneAppInput,
		) (*mcp.CallToolResult, CloneAppOutput, error) {
			sourceNs := input.SourceNamespace
			if sourceNs == "" {
				sourceNs = "workspace"
			}
			targetNs := input.TargetNamespace
			if targetNs == "" {
				targetNs = sourceNs
			}

			source, err := c.ShowApp(sourceNs, input.SourceName)

			if err != nil {
				return nil, CloneAppOutput{}, fmt.Errorf(
					"get source app %q: %w",
					input.SourceName,
					err,
				)
			}
			if source.ImageURL == "" {
				return nil, CloneAppOutput{}, fmt.Errorf(
					"source app %q has no image — it must be deployed first",
					input.SourceName,
				)
			}

			createReq := client.AppCreateRequest{
				Name: input.TargetName,
				Configuration: client.AppUpdateRequest{
					Instances: source.Configuration.Instances,
					AppChart:  source.Configuration.AppChart,
					Settings:  source.Configuration.Settings,
				},
			}
			if len(source.Configuration.Configurations) > 0 {
				createReq.Configuration.Configurations = source.Configuration.Configurations
			}

			if err := c.CreateApp(targetNs, createReq); err != nil {
				return nil, CloneAppOutput{}, fmt.Errorf(
					"create target app %q: %w",
					input.TargetName,
					err,
				)
			}

			if len(source.Configuration.Environment) > 0 {
				err := c.SetEnv(
					targetNs,
					input.TargetName,
					source.Configuration.Environment,
				)

				if err != nil {
					return nil, CloneAppOutput{}, fmt.Errorf(
						"created app but failed to copy env vars: %w",
						err,
					)
				}
			}

			deployReq := client.DeployRequest{
				App: client.AppRef{
					Name:      input.TargetName,
					Namespace: targetNs,
				},
				Stage: client.StageRef{
					ID: source.StageID,
				},
				ImageURL: source.ImageURL,
				Origin: client.ApplicationOrigin{
					Kind: 0,
					Path: fmt.Sprintf(
						"cloned from %s/%s",
						sourceNs,
						input.SourceName,
					),
				},
			}
			deployResp, err := c.DeployApp(
				targetNs,
				input.TargetName,
				deployReq,
			)

			if err != nil {
				return nil, CloneAppOutput{}, fmt.Errorf("deploy failed: %w", err)
			}

			if err := c.AppRunning(targetNs, input.TargetName); err != nil {
				return nil, CloneAppOutput{}, fmt.Errorf("deployed but app not yet running: %w", err)
			}

			return nil, CloneAppOutput{
				Status: "running",
				Routes: deployResp.Routes,
				Image:  source.ImageURL,
			}, nil
		},
	)
}
