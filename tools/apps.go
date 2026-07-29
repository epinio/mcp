package tools

import (
	"context"
	"fmt"

	"github.com/epinio/mcp/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ListAppsInput struct {
	// Optional: omit to list apps across every namespace the caller can
	// access. The `omitempty` here is load-bearing — without it, the SDK's
	// jsonschema reflection marks the field as required and callers get
	// "missing properties: [namespace]" when they don't send one.
	Namespace string `json:"namespace,omitempty" jsonschema:"optional: the namespace to list apps in; leave empty for all namespaces"`
}
type ListAppsOutput struct {
	Apps []AppSummary `json:"apps"`
}

type AppSummary struct {
	Name      string   `json:"name"`
	Namespace string   `json:"namespace"`
	Status    string   `json:"status"`
	Routes    []string `json:"routes,omitempty"`
	Instances int      `json:"instances"`
	Ready     int      `json:"ready"`
	// StageID is the ID of the most recent staging attempt.
	StageID string `json:"stage_id,omitempty"`
	// StagingStatus is Epinio's authoritative job-level status for the most
	// recent stage: "active" (build running now), "done" (succeeded), or
	// "failed". Reliable even on first load — unlike a client-side
	// "stage_id changed recently" heuristic.
	StagingStatus string `json:"staging_status,omitempty"`
	CreatedAt     string `json:"created_at"`
}

type ShowAppInput struct {
	Namespace string `json:"namespace" jsonschema:"the namespace of the app"`
	Name      string `json:"name" jsonschema:"the app name"`
}
type ShowAppOutput struct {
	Name          string            `json:"name"`
	Namespace     string            `json:"namespace"`
	Status        string            `json:"status"`
	StatusMessage string            `json:"status_message,omitempty"`
	StageID       string            `json:"stage_id,omitempty"`
	StagingStatus string            `json:"staging_status,omitempty"`
	Routes        []string          `json:"routes,omitempty"`
	Instances     int               `json:"desired_instances"`
	Ready         int               `json:"ready_instances"`
	ImageURL      string            `json:"image_url,omitempty"`
	Environment   map[string]string `json:"environment,omitempty"`
	Configs       []string          `json:"configurations,omitempty"`
	Origin        string            `json:"origin"`
	CreatedAt     string            `json:"created_at"`
}

type CreateAppInput struct {
	Namespace string `json:"namespace" jsonschema:"the namespace to create the app in"`
	Name      string `json:"name" jsonschema:"the application name"`
	Instances *int32 `json:"instances,omitempty" jsonschema:"number of instances to run"`
}
type CreateAppOutput struct {
	Status string `json:"status"`
}

type DeleteAppInput struct {
	Namespace string `json:"namespace" jsonschema:"the namespace of the app"`
	Name      string `json:"name" jsonschema:"the app name to delete"`
}
type DeleteAppOutput struct {
	Status string `json:"status"`
}

type RestartAppInput struct {
	Namespace string `json:"namespace" jsonschema:"the namespace of the app"`
	Name      string `json:"name" jsonschema:"the app name to restart"`
}
type RestartAppOutput struct {
	Status string `json:"status"`
}

type ScaleAppInput struct {
	Namespace string `json:"namespace" jsonschema:"the namespace of the app"`
	Name      string `json:"name" jsonschema:"the app name to scale"`
	Instances int32  `json:"instances" jsonschema:"desired instance count (>= 0)"`
}
type ScaleAppOutput struct {
	Status string `json:"status"`
}

// UpdateAppInput mirrors Epinio's AppUpdateRequest. Any omitted field is left
// unchanged — slices and maps sent as empty values will replace the existing
// content, so supply only the fields you intend to modify.
type UpdateAppInput struct {
	Namespace      string            `json:"namespace" jsonschema:"the namespace of the app"`
	Name           string            `json:"name" jsonschema:"the app name to update"`
	Instances      *int32            `json:"instances,omitempty" jsonschema:"optional new instance count"`
	Restart        *bool             `json:"restart,omitempty" jsonschema:"if true, restart the app after applying the update"`
	Configurations []string          `json:"configurations,omitempty" jsonschema:"replaces the full list of bound configurations"`
	Environment    map[string]string `json:"environment,omitempty" jsonschema:"environment variables to set on the app"`
	Routes         []string          `json:"routes,omitempty" jsonschema:"replaces the full list of routes"`
	AppChart       string            `json:"appchart,omitempty" jsonschema:"appchart name — note: Epinio rejects chart changes on active apps"`
	Settings       map[string]string `json:"settings,omitempty" jsonschema:"appchart-specific settings to apply"`
}
type UpdateAppOutput struct {
	Status string `json:"status"`
}

func originString(o client.ApplicationOrigin) string {
	switch {
	case o.Container != "":
		return "container: " + o.Container
	case o.Git != nil:
		return "git: " + o.Git.URL
	case o.Path != "":
		return "path: " + o.Path
	default:
		return "unknown"
	}
}

func RegisterAppTools(server *mcp.Server, c *client.Client) {
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "list_apps",
			Annotations: &mcp.ToolAnnotations{Title: "List Apps"},
			Description: "List Epinio applications, optionally filtered by namespace",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input ListAppsInput,
		) (*mcp.CallToolResult, ListAppsOutput, error) {
			var apps []client.App
			var err error
			if input.Namespace == "" {
				apps, err = c.ListAllApps()
			} else {
				apps, err = c.ListApps(input.Namespace)
			}
			if err != nil {
				return nil, ListAppsOutput{}, fmt.Errorf("list apps: %w", err)
			}
			var summaries []AppSummary
			for _, app := range apps {
				s := AppSummary{
					Name:          app.Meta.Name,
					Namespace:     app.Meta.Namespace,
					Status:        app.Status,
					StageID:       app.StageID,
					StagingStatus: app.StagingStatus,
					CreatedAt:     app.Meta.CreatedAt,
				}
				if app.Workload != nil {
					s.Routes = app.Workload.Routes
					s.Instances = app.Workload.DesiredReplicas
					s.Ready = app.Workload.ReadyReplicas
				}
				summaries = append(summaries, s)
			}
			return nil, ListAppsOutput{Apps: summaries}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "show_app",
			Annotations: &mcp.ToolAnnotations{Title: "Show App"},
			Description: "Get detailed information about an Epinio application " +
				"including status, routes, instances, and configuration",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input ShowAppInput,
		) (*mcp.CallToolResult, ShowAppOutput, error) {
			app, err := c.ShowApp(input.Namespace, input.Name)

			if err != nil {
				return nil, ShowAppOutput{}, fmt.Errorf("show app: %w", err)
			}
			out := ShowAppOutput{
				Name:          app.Meta.Name,
				Namespace:     app.Meta.Namespace,
				Status:        app.Status,
				StatusMessage: app.StatusMessage,
				StageID:       app.StageID,
				StagingStatus: app.StagingStatus,
				ImageURL:      app.ImageURL,
				Environment:   app.Configuration.Environment,
				Configs:       app.Configuration.Configurations,
				Origin:        originString(app.Origin),
				CreatedAt:     app.Meta.CreatedAt,
			}
			if app.Workload != nil {
				out.Routes = app.Workload.Routes
				out.Instances = app.Workload.DesiredReplicas
				out.Ready = app.Workload.ReadyReplicas
			}
			if app.Configuration.Instances != nil {
				out.Instances = int(*app.Configuration.Instances)
			}
			return nil, out, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "create_app",
			Annotations: &mcp.ToolAnnotations{Title: "Create App"},
			Description: "Create a new Epinio application",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input CreateAppInput,
		) (*mcp.CallToolResult, CreateAppOutput, error) {
			createReq := client.AppCreateRequest{
				Name: input.Name,
				Configuration: client.AppUpdateRequest{
					Instances: input.Instances,
				},
			}
			if err := c.CreateApp(input.Namespace, createReq); err != nil {
				return nil, CreateAppOutput{}, fmt.Errorf("create app: %w", err)
			}
			return nil, CreateAppOutput{
				Status: fmt.Sprintf(
					"app %q created in namespace %q",
					input.Name,
					input.Namespace,
				),
			}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "delete_app",
			Annotations: &mcp.ToolAnnotations{Title: "Delete App"},
			Description: "Delete an Epinio application.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input DeleteAppInput,
		) (*mcp.CallToolResult, DeleteAppOutput, error) {
			if err := appMutationGuard.EnsureMutable(
				ctx,
				input.Namespace,
				input.Name,
				"delete_app",
			); err != nil {
				return nil, DeleteAppOutput{}, err
			}

			if err := c.DeleteApp(input.Namespace, input.Name); err != nil {
				return nil, DeleteAppOutput{}, fmt.Errorf("delete app: %w", err)
			}

			return nil, DeleteAppOutput{
				Status: fmt.Sprintf(
					"app %q deleted from namespace %q",
					input.Name,
					input.Namespace,
				),
			}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "restart_app",
			Annotations: &mcp.ToolAnnotations{Title: "Restart App"},
			Description: "Restart an Epinio application.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input RestartAppInput,
		) (*mcp.CallToolResult, RestartAppOutput, error) {
			if err := appMutationGuard.EnsureMutable(
				ctx,
				input.Namespace,
				input.Name,
				"restart_app",
			); err != nil {
				return nil, RestartAppOutput{}, err
			}
			if err := c.RestartApp(input.Namespace, input.Name); err != nil {
				return nil, RestartAppOutput{}, fmt.Errorf("restart app: %w", err)
			}
			return nil, RestartAppOutput{
				Status: fmt.Sprintf(
					"app %q restarted in namespace %q",
					input.Name,
					input.Namespace,
				),
			}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "scale_app",
			Annotations: &mcp.ToolAnnotations{Title: "Scale App"},
			Description: "Scale an Epinio application to a desired instance count. " +
				"A thin wrapper over update_app for the common case.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input ScaleAppInput,
		) (*mcp.CallToolResult, ScaleAppOutput, error) {
			if input.Instances < 0 {
				return nil, ScaleAppOutput{}, fmt.Errorf(
					"scale app: instances must be >= 0, got %d",
					input.Instances,
				)
			}
			if err := appMutationGuard.EnsureMutable(ctx, input.Namespace, input.Name, "scale_app"); err != nil {
				return nil, ScaleAppOutput{}, err
			}
			n := input.Instances
			if err := c.UpdateApp(input.Namespace, input.Name, client.AppUpdateRequest{
				Instances: &n,
			}); err != nil {
				return nil, ScaleAppOutput{}, fmt.Errorf("scale app: %w", err)
			}
			return nil, ScaleAppOutput{
				Status: fmt.Sprintf(
					"scaled app %q in %q to %d instance(s)",
					input.Name,
					input.Namespace,
					input.Instances,
				),
			}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "update_app",
			Annotations: &mcp.ToolAnnotations{Title: "Update App"},
			Description: "Update an Epinio application's configuration (instances, " +
				"routes, environment, configurations, appchart, settings). Any " +
				"omitted field is left unchanged. Pass restart=true to roll pods " +
				"after the update.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input UpdateAppInput,
		) (*mcp.CallToolResult, UpdateAppOutput, error) {
			if err := appMutationGuard.EnsureMutable(ctx, input.Namespace, input.Name, "update_app"); err != nil {
				return nil, UpdateAppOutput{}, err
			}
			upd := client.AppUpdateRequest{
				Restart:        input.Restart,
				Instances:      input.Instances,
				Configurations: input.Configurations,
				Environment:    input.Environment,
				Routes:         input.Routes,
				AppChart:       input.AppChart,
				Settings:       input.Settings,
			}
			if err := c.UpdateApp(input.Namespace, input.Name, upd); err != nil {
				return nil, UpdateAppOutput{}, fmt.Errorf("update app: %w", err)
			}
			return nil, UpdateAppOutput{
				Status: fmt.Sprintf(
					"updated app %q in namespace %q",
					input.Name,
					input.Namespace,
				),
			}, nil
		},
	)
}
