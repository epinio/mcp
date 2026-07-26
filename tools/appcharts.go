package tools

import (
	"context"
	"fmt"

	"github.com/epinio/mcp/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// AppChartSummary is the flattened view we return to MCP callers. Mirrors
// client.AppChart but uses `map[string]any` for settings so the SDK's
// JSON-schema reflection doesn't need to chase a nested struct type.
type AppChartSummary struct {
	Name             string `json:"name"`
	Description      string `json:"description,omitempty"`
	ShortDescription string `json:"short_description,omitempty"`
	HelmChart        string `json:"helm_chart,omitempty"`
	HelmRepo         string `json:"helm_repo,omitempty"`
	// Settings maps each setting name to its schema (type + constraints).
	// Callers should consult this before setting `appchart` + `settings`
	// on create_app / update_app / push_app to pass valid values.
	Settings map[string]any `json:"settings,omitempty"`
}

type ListAppChartsInput struct{}
type ListAppChartsOutput struct {
	AppCharts []AppChartSummary `json:"appcharts"`
}

type ShowAppChartInput struct {
	Name string `json:"name" jsonschema:"the appchart name (e.g. 'standard', 'standard-elevated')"`
}
type ShowAppChartOutput struct {
	AppChart AppChartSummary `json:"appchart"`
}

// CRUD inputs. The per-chart `settings` schema is intentionally not exposed —
// it's a nested type that complicates the tool schema and is rarely authored
// via the MCP; register charts needing a settings schema with a chart tarball
// that already declares it.
type CreateAppChartInput struct {
	Name             string            `json:"name" jsonschema:"resource name for the new appchart (e.g. 'standard-elevated')"`
	HelmChart        string            `json:"helm_chart" jsonschema:"URL/reference to the Helm chart this appchart deploys"`
	HelmRepo         string            `json:"helm_repo,omitempty" jsonschema:"optional Helm repo the chart resolves through"`
	Description      string            `json:"description,omitempty" jsonschema:"optional longer description"`
	ShortDescription string            `json:"short_description,omitempty" jsonschema:"optional one-line description"`
	Values           map[string]string `json:"values,omitempty" jsonschema:"optional Helm values to pin"`
}

type UpdateAppChartInput struct {
	Name             string            `json:"name" jsonschema:"the appchart to update"`
	HelmChart        string            `json:"helm_chart,omitempty" jsonschema:"new chart reference (unchanged if empty)"`
	HelmRepo         string            `json:"helm_repo,omitempty" jsonschema:"new helm repo (unchanged if empty)"`
	Description      string            `json:"description,omitempty" jsonschema:"new description (unchanged if empty)"`
	ShortDescription string            `json:"short_description,omitempty" jsonschema:"new short description (unchanged if empty)"`
	Values           map[string]string `json:"values,omitempty" jsonschema:"replacement Helm values"`
}

type DeleteAppChartInput struct {
	Name string `json:"name" jsonschema:"the appchart to delete"`
}

type AppChartWriteOutput struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

func summarizeAppChart(a client.AppChart) AppChartSummary {
	var settings map[string]any
	if len(a.Settings) > 0 {
		settings = make(map[string]any, len(a.Settings))
		for k, v := range a.Settings {
			settings[k] = v
		}
	}
	return AppChartSummary{
		Name:             a.Meta.Name,
		Description:      a.Description,
		ShortDescription: a.ShortDescription,
		HelmChart:        a.HelmChart,
		HelmRepo:         a.HelmRepo,
		Settings:         settings,
	}
}

func RegisterAppChartTools(server *mcp.Server, c *client.Client) {
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "list_appcharts",
			Annotations: &mcp.ToolAnnotations{Title: "List App Charts"},
			Description: "List the AppCharts registered on the Epinio cluster. " +
				"These are the valid values for `configuration.appchart` on an app " +
				"(e.g. 'standard', 'standard-elevated'). Each entry includes its " +
				"settings schema so agents can pick the right chart and construct a " +
				"valid `settings` map for create_app / update_app / push_app " +
				"without guessing.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			_ ListAppChartsInput,
		) (*mcp.CallToolResult, ListAppChartsOutput, error) {
			charts, err := c.ListAppCharts()

			if err != nil {
				return nil, ListAppChartsOutput{}, fmt.Errorf("list appcharts: %w", err)
			}

			out := ListAppChartsOutput{
				AppCharts: make([]AppChartSummary, 0, len(charts)),
			}

			for _, ch := range charts {
				out.AppCharts = append(out.AppCharts, summarizeAppChart(ch))
			}

			return nil, out, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "show_appchart",
			Annotations: &mcp.ToolAnnotations{Title: "Show App Chart"},
			Description: "Fetch a single AppChart by name with its full description " +
				"and settings schema. Use this to inspect what an appchart expects " +
				"before selecting it for an app.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input ShowAppChartInput,
		) (*mcp.CallToolResult, ShowAppChartOutput, error) {
			if input.Name == "" {
				return nil, ShowAppChartOutput{}, fmt.Errorf("name is required")
			}

			ch, err := c.ShowAppChart(input.Name)

			if err != nil {
				return nil, ShowAppChartOutput{}, fmt.Errorf(
					"show appchart %q: %w",
					input.Name,
					err,
				)
			}

			return nil, ShowAppChartOutput{AppChart: summarizeAppChart(*ch)}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "create_appchart",
			Annotations: &mcp.ToolAnnotations{Title: "Create App Chart"},
			Description: "Register a new AppChart on the cluster. Requires appchart " +
				"admin permission (enforced by Epinio against your credentials).",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input CreateAppChartInput,
		) (*mcp.CallToolResult, AppChartWriteOutput, error) {
			if input.Name == "" || input.HelmChart == "" {
				return nil, AppChartWriteOutput{}, fmt.Errorf("name and helm_chart are required")
			}

			err := c.CreateAppChart(client.AppChartCreateRequest{
				Name:             input.Name,
				HelmChart:        input.HelmChart,
				HelmRepo:         input.HelmRepo,
				Description:      input.Description,
				ShortDescription: input.ShortDescription,
				Values:           input.Values,
			})
			if err != nil {
				return nil, AppChartWriteOutput{}, fmt.Errorf("create appchart %q: %w", input.Name, err)
			}

			return nil, AppChartWriteOutput{Name: input.Name, Status: "created"}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "update_appchart",
			Annotations: &mcp.ToolAnnotations{Title: "Update App Chart"},
			Description: "Update an existing AppChart. Omitted fields are unchanged.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input UpdateAppChartInput,
		) (*mcp.CallToolResult, AppChartWriteOutput, error) {
			if input.Name == "" {
				return nil, AppChartWriteOutput{}, fmt.Errorf("name is required")
			}

			err := c.UpdateAppChart(input.Name, client.AppChartUpdateRequest{
				HelmChart:        input.HelmChart,
				HelmRepo:         input.HelmRepo,
				Description:      input.Description,
				ShortDescription: input.ShortDescription,
				Values:           input.Values,
			})
			if err != nil {
				return nil, AppChartWriteOutput{}, fmt.Errorf(
					"update appchart %q: %w",
					input.Name,
					err,
				)
			}

			return nil, AppChartWriteOutput{Name: input.Name, Status: "updated"}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "delete_appchart",
			Annotations: &mcp.ToolAnnotations{Title: "Delete App Chart"},
			Description: "Delete an AppChart from the cluster.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input DeleteAppChartInput,
		) (*mcp.CallToolResult, AppChartWriteOutput, error) {
			if input.Name == "" {
				return nil, AppChartWriteOutput{}, fmt.Errorf("name is required")
			}

			if err := c.DeleteAppChart(input.Name); err != nil {
				return nil, AppChartWriteOutput{}, fmt.Errorf(
					"delete appchart %q: %w",
					input.Name,
					err,
				)
			}

			return nil, AppChartWriteOutput{Name: input.Name, Status: "deleted"}, nil
		},
	)
}
