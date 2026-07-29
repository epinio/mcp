package tools

import (
	"context"
	"fmt"

	"github.com/epinio/mcp/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Builder images are the Epinio BuilderImage CRD — the cluster's registry of
// builder images an app may stage with. This is the authoritative source for
// the `builder_image` parameter on push_app / upload_and_stage; the entry with
// default=true is used when none is given. All write ops go through the Epinio
// REST API as the calling user (Epinio enforces RBAC).

type ListBuilderImagesInput struct{}
type ListBuilderImagesOutput struct {
	BuilderImages []client.BuilderImage `json:"builder_images"`
}

type ShowBuilderImageInput struct {
	Name string `json:"name" jsonschema:"the builder image name"`
}
type ShowBuilderImageOutput struct {
	BuilderImage client.BuilderImage `json:"builder_image"`
}

type CreateBuilderImageInput struct {
	Name             string `json:"name" jsonschema:"resource name for the new builder image"`
	Image            string `json:"image" jsonschema:"the builder image reference the buildpack lifecycle runs (e.g. paketobuildpacks/builder-jammy-full:0.3.606)"`
	Description      string `json:"description,omitempty" jsonschema:"optional longer description"`
	ShortDescription string `json:"short_description,omitempty" jsonschema:"optional one-line description"`
}

type UpdateBuilderImageInput struct {
	Name             string `json:"name" jsonschema:"the builder image to update"`
	Image            string `json:"image,omitempty" jsonschema:"new image reference (unchanged if empty)"`
	Description      string `json:"description,omitempty" jsonschema:"new description (unchanged if empty)"`
	ShortDescription string `json:"short_description,omitempty" jsonschema:"new short description (unchanged if empty)"`
}

type DeleteBuilderImageInput struct {
	Name string `json:"name" jsonschema:"the builder image to delete"`
}

type BuilderImageWriteOutput struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// RegisterBuilderImageTools registers the BuilderImage CRD tools. Read tools
// are always safe; the write tools require builder-image admin permission,
// which Epinio enforces against the caller's credentials.
func RegisterBuilderImageTools(server *mcp.Server, c *client.Client) {
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "list_builder_images",
			Annotations: &mcp.ToolAnnotations{Title: "List Builder Images"},
			Description: "List the builder images registered on the Epinio cluster — " +
				"the valid values for the `builder_image` parameter on push_app / " +
				"upload_and_stage. The entry with `default: true` is what staging uses " +
				"when no builder image is specified; `bound_apps` marks images already " +
				"in use.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			_ ListBuilderImagesInput,
		) (*mcp.CallToolResult, ListBuilderImagesOutput, error) {
			imgs, err := c.ListBuilderImages()

			if err != nil {
				return nil, ListBuilderImagesOutput{}, fmt.Errorf("list builder images: %w", err)
			}

			if imgs == nil {
				imgs = []client.BuilderImage{}
			}

			return nil, ListBuilderImagesOutput{BuilderImages: imgs}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "show_builder_image",
			Annotations: &mcp.ToolAnnotations{Title: "Show Builder Image"},
			Description: "Fetch a single registered builder image by name.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input ShowBuilderImageInput,
		) (*mcp.CallToolResult, ShowBuilderImageOutput, error) {
			if input.Name == "" {
				return nil, ShowBuilderImageOutput{}, fmt.Errorf("name is required")
			}

			bi, err := c.ShowBuilderImage(input.Name)

			if err != nil {
				return nil, ShowBuilderImageOutput{}, fmt.Errorf(
					"show builder image %q: %w",
					input.Name,
					err,
				)
			}

			return nil, ShowBuilderImageOutput{BuilderImage: *bi}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "create_builder_image",
			Annotations: &mcp.ToolAnnotations{Title: "Create Builder Image"},
			Description: "Register a new builder image on the cluster. Requires " +
				"builder-image admin permission (enforced by Epinio against your " +
				"credentials). The `default` flag is operator policy and cannot be set here.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input CreateBuilderImageInput,
		) (*mcp.CallToolResult, BuilderImageWriteOutput, error) {
			if input.Name == "" || input.Image == "" {
				return nil, BuilderImageWriteOutput{}, fmt.Errorf("name and image are required")
			}

			err := c.CreateBuilderImage(client.BuilderImageCreateRequest{
				Name:             input.Name,
				Image:            input.Image,
				Description:      input.Description,
				ShortDescription: input.ShortDescription,
			})
			if err != nil {
				return nil, BuilderImageWriteOutput{}, fmt.Errorf(
					"create builder image %q: %w",
					input.Name,
					err,
				)
			}

			return nil, BuilderImageWriteOutput{Name: input.Name, Status: "created"}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "update_builder_image",
			Annotations: &mcp.ToolAnnotations{Title: "Update Builder Image"},
			Description: "Update a registered builder image's image reference or " +
				"descriptions. Any omitted field is left unchanged.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input UpdateBuilderImageInput,
		) (*mcp.CallToolResult, BuilderImageWriteOutput, error) {
			if input.Name == "" {
				return nil, BuilderImageWriteOutput{}, fmt.Errorf("name is required")
			}
			err := c.UpdateBuilderImage(
				input.Name,
				client.BuilderImageUpdateRequest{
					Image:            input.Image,
					Description:      input.Description,
					ShortDescription: input.ShortDescription,
				},
			)
			if err != nil {
				return nil, BuilderImageWriteOutput{}, fmt.Errorf(
					"update builder image %q: %w",
					input.Name,
					err,
				)
			}
			return nil, BuilderImageWriteOutput{Name: input.Name, Status: "updated"}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "delete_builder_image",
			Annotations: &mcp.ToolAnnotations{Title: "Delete Builder Image"},
			Description: "Delete a registered builder image from the cluster.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input DeleteBuilderImageInput,
		) (*mcp.CallToolResult, BuilderImageWriteOutput, error) {
			if input.Name == "" {
				return nil, BuilderImageWriteOutput{}, fmt.Errorf("name is required")
			}
			if err := c.DeleteBuilderImage(input.Name); err != nil {
				return nil, BuilderImageWriteOutput{}, fmt.Errorf(
					"delete builder image %q: %w",
					input.Name,
					err,
				)
			}
			return nil, BuilderImageWriteOutput{Name: input.Name, Status: "deleted"}, nil
		},
	)
}
