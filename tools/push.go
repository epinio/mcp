package tools

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/epinio/mcp/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type PushAppInput struct {
	Namespace    string            `json:"namespace" jsonschema:"the namespace to deploy into"`
	Name         string            `json:"name" jsonschema:"the application name"`
	Files        map[string]string `json:"files" jsonschema:"map of file paths to file contents (text files only)"`
	BuilderImage string            `json:"builder_image,omitempty" jsonschema:"optional builder image override (default: server default or paketobuildpacks/builder-jammy-full:0.3.606)"`
}
type PushAppOutput struct {
	Status  string   `json:"status"`
	Routes  []string `json:"routes"`
	StageID string   `json:"stage_id"`
	Image   string   `json:"image"`
}

type UploadAndStageInput struct {
	Namespace    string            `json:"namespace" jsonschema:"the namespace of the app"`
	Name         string            `json:"name" jsonschema:"the app name (must already exist)"`
	Files        map[string]string `json:"files" jsonschema:"map of file paths to file contents (text files only)"`
	BuilderImage string            `json:"builder_image,omitempty" jsonschema:"optional builder image override"`
}
type UploadAndStageOutput struct {
	Status  string `json:"status"`
	StageID string `json:"stage_id"`
	BlobUID string `json:"blob_uid"`
	Image   string `json:"image"`
}

type DeployStagedInput struct {
	Namespace string `json:"namespace" jsonschema:"the namespace of the app"`
	Name      string `json:"name" jsonschema:"the app name"`
	StageID   string `json:"stage_id" jsonschema:"the stage ID from upload_and_stage"`
	Image     string `json:"image" jsonschema:"the image URL from upload_and_stage"`
}
type DeployStagedOutput struct {
	Status string   `json:"status"`
	Routes []string `json:"routes"`
}

// buildTarGz creates an in-memory tar.gz archive from a map of filepath->content.
func buildTarGz(files map[string]string) (io.Reader, error) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	for filePath, content := range files {
		filePath = strings.TrimPrefix(filePath, "/")
		filePath = strings.TrimPrefix(filePath, "./")
		// Strip literal surrounding quotes — callers that accidentally
		// double-serialize keys (e.g. `{json.dumps(k): v ...}`) produce
		// paths like `"package.json"`, which the unpacker would write as a
		// literally-quoted filename that no buildpack can find. Safe to
		// strip universally; nothing valid has a leading or trailing `"`.
		filePath = strings.Trim(filePath, "\"'")
		if filePath == "" {
			continue
		}

		dir := path.Dir(filePath)
		if dir != "." && dir != "" {
			parts := strings.Split(dir, "/")
			for i := range parts {
				dirPath := strings.Join(parts[:i+1], "/") + "/"
				if err := tw.WriteHeader(&tar.Header{
					Name:     dirPath,
					Typeflag: tar.TypeDir,
					Mode:     0755,
				}); err != nil {
					return nil, fmt.Errorf("write tar dir header for %s: %w", dirPath, err)
				}
			}
		}

		data := []byte(content)
		if err := tw.WriteHeader(&tar.Header{
			Name: filePath,
			Mode: 0644,
			Size: int64(len(data)),
		}); err != nil {
			return nil, fmt.Errorf("write tar header for %s: %w", filePath, err)
		}
		if _, err := tw.Write(data); err != nil {
			return nil, fmt.Errorf("write tar content for %s: %w", filePath, err)
		}
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close tar: %w", err)
	}
	if err := gw.Close(); err != nil {
		return nil, fmt.Errorf("close gzip: %w", err)
	}
	return &buf, nil
}

// processFiles checks if any file values are base64-encoded (prefixed with
// "base64:") and decodes them, returning the raw bytes map.
func processFiles(files map[string]string) map[string]string {
	result := make(map[string]string, len(files))
	for k, v := range files {
		if strings.HasPrefix(v, "base64:") {
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(v, "base64:"))
			if err == nil {
				result[k] = string(decoded)
				continue
			}
		}
		result[k] = v
	}
	return result
}

func RegisterPushTools(server *mcp.Server, c *client.Client) {
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "push_app",
			Annotations: &mcp.ToolAnnotations{Title: "Push App"},
			Description: "Push (deploy) an application to Epinio from source files. " +
				"Creates the app if it doesn't exist, uploads source as tar.gz, " +
				"builds via buildpacks, and deploys. For text files, pass content " +
				"directly. For binary files, base64-encode the content and prefix " +
				"with 'base64:'.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input PushAppInput,
		) (*mcp.CallToolResult, PushAppOutput, error) {
			if len(input.Files) == 0 {
				return nil, PushAppOutput{}, fmt.Errorf("at least one file is required")
			}
			processed := processFiles(input.Files)
			archive, err := buildTarGz(processed)
			if err != nil {
				return nil, PushAppOutput{}, fmt.Errorf("build archive: %w", err)
			}
			result, err := c.PushApp(input.Namespace, input.Name, archive, input.BuilderImage)
			if err != nil {
				return nil, PushAppOutput{}, fmt.Errorf("push: %w", err)
			}
			return nil, PushAppOutput{
				Status: fmt.Sprintf(
					"app %q deployed successfully in namespace %q",
					input.Name,
					input.Namespace,
				),
				Routes:  result.Routes,
				StageID: result.StageID,
				Image:   result.Image,
			}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "upload_and_stage",
			Annotations: &mcp.ToolAnnotations{Title: "Upload and Stage"},
			Description: "Upload source and stage (build) an Epinio application " +
				"without deploying it. Use deploy_staged to deploy after verifying " +
				"the build. The app must already exist (use create_app first).",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input UploadAndStageInput,
		) (*mcp.CallToolResult, UploadAndStageOutput, error) {
			if len(input.Files) == 0 {
				return nil, UploadAndStageOutput{}, fmt.Errorf("at least one file is required")
			}
			processed := processFiles(input.Files)
			archive, err := buildTarGz(processed)
			if err != nil {
				return nil, UploadAndStageOutput{}, fmt.Errorf("build archive: %w", err)
			}
			upload, err := c.UploadApp(input.Namespace, input.Name, archive)
			if err != nil {
				return nil, UploadAndStageOutput{}, fmt.Errorf("upload: %w", err)
			}
			builderImage := input.BuilderImage
			if builderImage == "" {
				info, infoErr := c.Info()
				if infoErr == nil && info.DefaultBuilderImage != "" {
					builderImage = info.DefaultBuilderImage
				} else {
					builderImage = "paketobuildpacks/builder-jammy-full:0.3.606"
				}
			}
			stageResp, err := c.StageApp(input.Namespace, input.Name, client.StageRequest{
				App:          client.AppRef{Name: input.Name, Namespace: input.Namespace},
				BlobUID:      upload.BlobUID,
				BuilderImage: builderImage,
			})
			if err != nil {
				return nil, UploadAndStageOutput{}, fmt.Errorf("stage: %w", err)
			}
			if err := c.StagingComplete(input.Namespace, stageResp.Stage.ID); err != nil {
				return nil, UploadAndStageOutput{}, fmt.Errorf("staging wait: %w", err)
			}
			return nil, UploadAndStageOutput{
				Status:  fmt.Sprintf("app %q staged successfully — use deploy_staged to deploy", input.Name),
				StageID: stageResp.Stage.ID,
				BlobUID: upload.BlobUID,
				Image:   stageResp.ImageURL,
			}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "deploy_staged",
			Annotations: &mcp.ToolAnnotations{Title: "Deploy Staged Build"},
			Description: "Deploy a previously staged Epinio application. Use after " +
				"upload_and_stage to complete the deployment.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input DeployStagedInput,
		) (*mcp.CallToolResult, DeployStagedOutput, error) {
			deployResp, err := c.DeployApp(input.Namespace, input.Name, client.DeployRequest{
				App:      client.AppRef{Name: input.Name, Namespace: input.Namespace},
				Stage:    client.StageRef{ID: input.StageID},
				ImageURL: input.Image,
				Origin:   client.ApplicationOrigin{Kind: 0, Path: "mcp-push"},
			})
			if err != nil {
				return nil, DeployStagedOutput{}, fmt.Errorf("deploy: %w", err)
			}
			if err := c.AppRunning(input.Namespace, input.Name); err != nil {
				return nil, DeployStagedOutput{}, fmt.Errorf("wait for running: %w", err)
			}
			return nil, DeployStagedOutput{
				Status: fmt.Sprintf(
					"app %q deployed and running in namespace %q",
					input.Name,
					input.Namespace,
				),
				Routes: deployResp.Routes,
			}, nil
		},
	)
}
