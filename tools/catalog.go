package tools

import (
	"context"
	"fmt"

	"github.com/epinio/mcp/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Catalog service CRUD manages the catalog *entries* (service templates users
// instantiate via create_service) — distinct from service instances. The
// per-entry `settings` schema is intentionally not exposed (nested type);
// everything else the API accepts is. Writes require catalog admin permission,
// enforced by Epinio against the caller's credentials.

type CreateCatalogServiceInput struct {
	Name             string   `json:"name" jsonschema:"resource name for the new catalog service entry"`
	Chart            string   `json:"chart" jsonschema:"the Helm chart this catalog service deploys"`
	ChartVersion     string   `json:"chart_version,omitempty" jsonschema:"chart version"`
	AppVersion       string   `json:"app_version,omitempty" jsonschema:"app version"`
	ShortDescription string   `json:"short_description,omitempty" jsonschema:"one-line description"`
	Description      string   `json:"description,omitempty" jsonschema:"longer description"`
	ServiceIcon      string   `json:"service_icon,omitempty" jsonschema:"icon URL"`
	Values           string   `json:"values,omitempty" jsonschema:"default Helm values (YAML string)"`
	SecretTypes      []string `json:"secret_types,omitempty" jsonschema:"credential secret types the service exposes"`
	HelmRepoName     string   `json:"helm_repo_name,omitempty" jsonschema:"Helm repo name"`
	HelmRepoURL      string   `json:"helm_repo_url,omitempty" jsonschema:"Helm repo URL"`
	HelmRepoSecret   string   `json:"helm_repo_secret,omitempty" jsonschema:"name of a Secret holding Helm repo credentials"`
}

type UpdateCatalogServiceInput struct {
	Name             string   `json:"name" jsonschema:"the catalog service entry to update"`
	Chart            string   `json:"chart,omitempty" jsonschema:"new chart reference (unchanged if empty)"`
	ChartVersion     string   `json:"chart_version,omitempty"`
	AppVersion       string   `json:"app_version,omitempty"`
	ShortDescription string   `json:"short_description,omitempty"`
	Description      string   `json:"description,omitempty"`
	ServiceIcon      string   `json:"service_icon,omitempty"`
	Values           string   `json:"values,omitempty"`
	SecretTypes      []string `json:"secret_types,omitempty"`
	HelmRepoName     string   `json:"helm_repo_name,omitempty"`
	HelmRepoURL      string   `json:"helm_repo_url,omitempty"`
	HelmRepoSecret   string   `json:"helm_repo_secret,omitempty"`
}

type DeleteCatalogServiceInput struct {
	Name string `json:"name" jsonschema:"the catalog service entry to delete"`
}

type CatalogServiceWriteOutput struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

func RegisterCatalogTools(server *mcp.Server, c *client.Client) {
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "create_catalog_service",
			Annotations: &mcp.ToolAnnotations{Title: "Create Catalog Service"},
			Description: "Register a new catalog service entry — a service template " +
				"users instantiate with create_service. Requires catalog admin " +
				"permission, enforced by Epinio against your credentials.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input CreateCatalogServiceInput,
		) (*mcp.CallToolResult, CatalogServiceWriteOutput, error) {
			if input.Name == "" || input.Chart == "" {
				return nil, CatalogServiceWriteOutput{}, fmt.Errorf("name and chart are required")
			}

			err := c.CreateCatalogService(client.CatalogServiceCreateRequest{
				Name:             input.Name,
				ShortDescription: input.ShortDescription,
				Description:      input.Description,
				HelmChart:        input.Chart,
				ChartVersion:     input.ChartVersion,
				AppVersion:       input.AppVersion,
				ServiceIcon:      input.ServiceIcon,
				Values:           input.Values,
				HelmRepo: client.HelmRepoRequest{
					Name:   input.HelmRepoName,
					URL:    input.HelmRepoURL,
					Secret: input.HelmRepoSecret,
				},
				SecretTypes: input.SecretTypes,
			})
			if err != nil {
				return nil, CatalogServiceWriteOutput{}, fmt.Errorf(
					"create catalog service %q: %w",
					input.Name,
					err,
				)
			}

			return nil, CatalogServiceWriteOutput{Name: input.Name, Status: "created"}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "update_catalog_service",
			Annotations: &mcp.ToolAnnotations{Title: "Update Catalog Service"},
			Description: "Update a catalog service entry. Any omitted field is left unchanged.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input UpdateCatalogServiceInput,
		) (*mcp.CallToolResult, CatalogServiceWriteOutput, error) {
			if input.Name == "" {
				return nil, CatalogServiceWriteOutput{}, fmt.Errorf("name is required")
			}

			upd := client.CatalogServiceUpdateRequest{
				ShortDescription: input.ShortDescription,
				Description:      input.Description,
				HelmChart:        input.Chart,
				ChartVersion:     input.ChartVersion,
				AppVersion:       input.AppVersion,
				ServiceIcon:      input.ServiceIcon,
				Values:           input.Values,
				SecretTypes:      input.SecretTypes,
			}

			if input.HelmRepoName != "" ||
				input.HelmRepoURL != "" ||
				input.HelmRepoSecret != "" {
				upd.HelmRepo = &client.HelmRepoRequest{
					Name:   input.HelmRepoName,
					URL:    input.HelmRepoURL,
					Secret: input.HelmRepoSecret,
				}
			}

			if err := c.UpdateCatalogService(input.Name, upd); err != nil {
				return nil, CatalogServiceWriteOutput{}, fmt.Errorf(
					"update catalog service %q: %w",
					input.Name,
					err,
				)
			}

			return nil, CatalogServiceWriteOutput{Name: input.Name, Status: "updated"}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "delete_catalog_service",
			Annotations: &mcp.ToolAnnotations{Title: "Delete Catalog Service"},
			Description: "Delete a catalog service entry from the cluster.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input DeleteCatalogServiceInput,
		) (*mcp.CallToolResult, CatalogServiceWriteOutput, error) {
			if input.Name == "" {
				return nil, CatalogServiceWriteOutput{}, fmt.Errorf("name is required")
			}

			if err := c.DeleteCatalogService(input.Name); err != nil {
				return nil, CatalogServiceWriteOutput{}, fmt.Errorf(
					"delete catalog service %q: %w",
					input.Name,
					err,
				)
			}

			return nil, CatalogServiceWriteOutput{Name: input.Name, Status: "deleted"}, nil
		},
	)
}
