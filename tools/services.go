package tools

import (
	"context"
	"fmt"

	"github.com/krumware/epinio-mcp/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ListServicesInput struct {
	Namespace string `json:"namespace" jsonschema:"the namespace to list services in"`
}
type ListServicesOutput struct {
	Services []ServiceSummary `json:"services"`
}
type ServiceSummary struct {
	Name           string   `json:"name"`
	Namespace      string   `json:"namespace"`
	CatalogService string   `json:"catalog_service"`
	Status         string   `json:"status"`
	BoundApps      []string `json:"bound_apps,omitempty"`
	CreatedAt      string   `json:"created_at"`
}

type ListCatalogInput struct{}
type ListCatalogOutput struct {
	CatalogServices []CatalogSummary `json:"catalog_services"`
}

// CatalogSummary describes a catalog entry. Settings is the schema of
// configurable parameters the entry accepts — each value is a JSON Schema-ish
// object with at minimum a "type" field (string/bool/integer), optionally
// minimum/maximum/enum. Clients should consult this before calling
// create_service so they pass valid settings.
type CatalogSummary struct {
	Name             string         `json:"name"`
	Description      string         `json:"description"`
	ShortDescription string         `json:"short_description"`
	HelmChart        string         `json:"helm_chart,omitempty"`
	ChartVersion     string         `json:"chart_version"`
	AppVersion       string         `json:"app_version"`
	Settings         map[string]any `json:"settings,omitempty"`
}

type ShowCatalogServiceInput struct {
	Name string `json:"name" jsonschema:"the catalog service name to describe"`
}
type ShowCatalogServiceOutput struct {
	CatalogService CatalogSummary `json:"catalog_service"`
}

type CreateServiceInput struct {
	Namespace      string            `json:"namespace" jsonschema:"the namespace to create the service in"`
	Name           string            `json:"name" jsonschema:"the service instance name"`
	CatalogService string            `json:"catalog_service" jsonschema:"the catalog service to instantiate"`
	Settings       map[string]string `json:"settings,omitempty" jsonschema:"optional service settings"`
}
type CreateServiceOutput struct {
	Status string `json:"status"`
}

type DeleteServiceInput struct {
	Namespace string `json:"namespace" jsonschema:"the namespace of the service"`
	Name      string `json:"name" jsonschema:"the service name to delete"`
}
type DeleteServiceOutput struct {
	Status string `json:"status"`
}

type BindServiceInput struct {
	Namespace string `json:"namespace" jsonschema:"the namespace containing the service and app"`
	Service   string `json:"service" jsonschema:"the service name to bind"`
	App       string `json:"app" jsonschema:"the app name to bind the service to"`
}
type BindServiceOutput struct {
	Status string `json:"status"`
}

type UnbindServiceInput struct {
	Namespace string `json:"namespace" jsonschema:"the namespace containing the service and app"`
	Service   string `json:"service" jsonschema:"the service name to unbind"`
	App       string `json:"app" jsonschema:"the app name to unbind the service from"`
}
type UnbindServiceOutput struct {
	Status string `json:"status"`
}

func summarizeCatalog(cs client.CatalogService) CatalogSummary {
	return CatalogSummary{
		Name:             cs.Meta.Name,
		Description:      cs.Description,
		ShortDescription: cs.ShortDescription,
		HelmChart:        cs.HelmChart,
		ChartVersion:     cs.ChartVersion,
		AppVersion:       cs.AppVersion,
		Settings:         cs.Settings,
	}
}

func RegisterServiceTools(server *mcp.Server, c *client.Client) {
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "list_services",
			Annotations: &mcp.ToolAnnotations{Title: "List Services"},
			Description: "List service instances in an Epinio namespace",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input ListServicesInput,
		) (*mcp.CallToolResult, ListServicesOutput, error) {
			services, err := c.ListServices(input.Namespace)
			if err != nil {
				return nil, ListServicesOutput{}, fmt.Errorf("list services: %w", err)
			}
			var summaries []ServiceSummary
			for _, svc := range services {
				summaries = append(summaries, ServiceSummary{
					Name:           svc.Meta.Name,
					Namespace:      svc.Meta.Namespace,
					CatalogService: svc.CatalogService,
					Status:         svc.Status,
					BoundApps:      svc.BoundApps,
					CreatedAt:      svc.Meta.CreatedAt,
				})
			}
			return nil, ListServicesOutput{Services: summaries}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "list_catalog_services",
			Annotations: &mcp.ToolAnnotations{Title: "List Catalog Services"},
			Description: "List available service catalog entries that can be " +
				"instantiated. Each entry includes its settings schema — consult " +
				"this before calling create_service to pass valid settings.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input ListCatalogInput,
		) (*mcp.CallToolResult, ListCatalogOutput, error) {
			catalog, err := c.ListCatalogServices()
			if err != nil {
				return nil, ListCatalogOutput{}, fmt.Errorf("list catalog: %w", err)
			}
			summaries := make([]CatalogSummary, 0, len(catalog))
			for _, cs := range catalog {
				summaries = append(summaries, summarizeCatalog(cs))
			}
			return nil, ListCatalogOutput{CatalogServices: summaries}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "show_catalog_service",
			Annotations: &mcp.ToolAnnotations{Title: "Show Catalog Service"},
			Description: "Fetch detailed information about a single catalog " +
				"service, including the full settings schema describing what " +
				"parameters create_service accepts.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input ShowCatalogServiceInput,
		) (*mcp.CallToolResult, ShowCatalogServiceOutput, error) {
			cs, err := c.ShowCatalogService(input.Name)
			if err != nil {
				return nil, ShowCatalogServiceOutput{}, fmt.Errorf("show catalog service: %w", err)
			}
			return nil, ShowCatalogServiceOutput{CatalogService: summarizeCatalog(*cs)}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "create_service",
			Annotations: &mcp.ToolAnnotations{Title: "Create Service"},
			Description: "Create a new service instance from the catalog in an Epinio namespace",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input CreateServiceInput,
		) (*mcp.CallToolResult, CreateServiceOutput, error) {
			if err := c.CreateService(input.Namespace, client.ServiceCreateRequest{
				CatalogService: input.CatalogService,
				Name:           input.Name,
				Settings:       input.Settings,
			}); err != nil {
				return nil, CreateServiceOutput{}, fmt.Errorf("create service: %w", err)
			}
			return nil, CreateServiceOutput{
				Status: fmt.Sprintf(
					"service %q created in namespace %q",
					input.Name,
					input.Namespace,
				),
			}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "delete_service",
			Annotations: &mcp.ToolAnnotations{Title: "Delete Service"},
			Description: "Delete a service instance from an Epinio namespace",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input DeleteServiceInput,
		) (*mcp.CallToolResult, DeleteServiceOutput, error) {
			if err := c.DeleteService(input.Namespace, input.Name); err != nil {
				return nil, DeleteServiceOutput{}, fmt.Errorf("delete service: %w", err)
			}
			return nil, DeleteServiceOutput{
				Status: fmt.Sprintf(
					"service %q deleted from namespace %q",
					input.Name,
					input.Namespace,
				),
			}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "bind_service",
			Annotations: &mcp.ToolAnnotations{Title: "Bind Service"},
			Description: "Bind a service to an Epinio application, making its " +
				"connection details available as environment variables",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input BindServiceInput,
		) (*mcp.CallToolResult, BindServiceOutput, error) {
			if err := c.BindService(
				input.Namespace,
				input.Service,
				client.ServiceBindRequest{AppName: input.App},
			); err != nil {
				return nil, BindServiceOutput{}, fmt.Errorf("bind service: %w", err)
			}
			return nil, BindServiceOutput{
				Status: fmt.Sprintf(
					"bound service %q to app %q in namespace %q",
					input.Service,
					input.App,
					input.Namespace,
				),
			}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "unbind_service",
			Annotations: &mcp.ToolAnnotations{Title: "Unbind Service"},
			Description: "Unbind a service from an Epinio application",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input UnbindServiceInput,
		) (*mcp.CallToolResult, UnbindServiceOutput, error) {
			if err := c.UnbindService(
				input.Namespace,
				input.Service,
				client.ServiceUnbindRequest{AppName: input.App},
			); err != nil {
				return nil, UnbindServiceOutput{}, fmt.Errorf("unbind service: %w", err)
			}
			return nil, UnbindServiceOutput{
				Status: fmt.Sprintf(
					"unbound service %q from app %q in namespace %q",
					input.Service,
					input.App,
					input.Namespace,
				),
			}, nil
		},
	)
}
