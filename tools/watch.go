package tools

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/epinio/mcp/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type WatchAppStartupInput struct {
	Namespace  string            `json:"namespace" jsonschema:"the namespace of the app"`
	Name       string            `json:"name" jsonschema:"the app name (must already exist and have been pushed at least once)"`
	Files      map[string]string `json:"files" jsonschema:"map of file paths to file contents for the full application source"`
	ProcessCmd string            `json:"process_cmd,omitempty" jsonschema:"optional command the supervisor runs on first boot for non-CNB images (e.g. /app/bin/start)"`
}
type WatchAppStartupOutput struct {
	Status  string `json:"status"`
	StageID string `json:"stage_id"`
	Image   string `json:"image"`
}

type SyncAppInput struct {
	Namespace  string            `json:"namespace" jsonschema:"the namespace of the app"`
	Name       string            `json:"name" jsonschema:"the app name"`
	Mode       string            `json:"mode,omitempty" jsonschema:"sync mode: files (interpreted languages) or binary (compiled languages); defaults to files"`
	Files      map[string]string `json:"files" jsonschema:"map of file paths to contents; pass changed files in files mode, or exactly one binary in binary mode"`
	Dest       string            `json:"dest,omitempty" jsonschema:"optional destination path inside the pod (overrides files_dest or binary_dest from .epinio-sync.yaml defaults)"`
	BinaryName string            `json:"binary_name,omitempty" jsonschema:"basename of the binary inside the tar for binary mode; defaults to the single file's basename"`
}
type SyncAppOutput struct {
	Status string `json:"status"`
	Mode   string `json:"mode"`
}

func RegisterWatchTools(server *mcp.Server, c *client.Client) {
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "watch_app_startup",
			Annotations: &mcp.ToolAnnotations{Title: "Watch App Startup"},
			Description: "Start an app-watch session: upload the full source, run " +
				"buildpack staging, install the supervisor wrapper, and wait until " +
				"the app is ready. Call before sync_app and again after any " +
				"push_app, deploy_staged, restart_app, or other redeploy that " +
				"recreates the pod — those remove the supervisor. Experimental — " +
				"validated on a limited set of frameworks and builder images. The " +
				"app must already exist (push_app first). For text files pass content " +
				"directly; for binary files base64-encode and prefix with 'base64:'.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input WatchAppStartupInput,
		) (*mcp.CallToolResult, WatchAppStartupOutput, error) {
			if len(input.Files) == 0 {
				return nil, WatchAppStartupOutput{}, fmt.Errorf("at least one file is required")
			}
			if err := appMutationGuard.EnsureMutable(
				ctx,
				input.Namespace,
				input.Name,
				"watch_app_startup",
			); err != nil {
				return nil, WatchAppStartupOutput{}, err
			}

			processed := processFiles(input.Files)
			archive, err := buildTarGz(processed)
			if err != nil {
				return nil, WatchAppStartupOutput{}, fmt.Errorf("build archive: %w", err)
			}

			stageResp, err := c.WatchAppStartup(
				input.Namespace,
				input.Name,
				archive,
				input.ProcessCmd,
			)
			if err != nil {
				return nil, WatchAppStartupOutput{}, err
			}

			return nil, WatchAppStartupOutput{
				Status: fmt.Sprintf(
					"app %q in namespace %q is ready for watch sync (supervisor installed)",
					input.Name,
					input.Namespace,
				),
				StageID: stageResp.Stage.ID,
				Image:   stageResp.ImageURL,
			}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "sync_app",
			Annotations: &mcp.ToolAnnotations{Title: "Sync App"},
			Description: "Sync changed source into a running app under watch. Use " +
				"after watch_app_startup. files mode uploads changed source files; " +
				"binary mode uploads a rebuilt binary (pass exactly one file). The " +
				"supervisor restarts the app in place — no full buildpack pipeline. " +
				"If push_app, deploy_staged, or restart_app ran since the last " +
				"watch_app_startup, call watch_app_startup again first — a plain " +
				"redeploy recreates the pod without the supervisor and sync will fail. " +
				"Experimental. For text files pass content directly; for binary files " +
				"base64-encode and prefix with 'base64:'.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input SyncAppInput,
		) (*mcp.CallToolResult, SyncAppOutput, error) {
			if len(input.Files) == 0 {
				return nil, SyncAppOutput{}, fmt.Errorf("at least one file is required")
			}

			mode := input.Mode
			if mode == "" {
				mode = "files"
			}
			if mode != "files" && mode != "binary" {
				return nil, SyncAppOutput{}, fmt.Errorf(
					"invalid mode %q: must be files or binary",
					mode,
				)
			}
			if mode == "binary" && len(input.Files) != 1 {
				return nil, SyncAppOutput{}, fmt.Errorf(
					"binary mode requires exactly one file in files map",
				)
			}

			if err := appMutationGuard.EnsureMutable(
				ctx,
				input.Namespace,
				input.Name,
				"sync_app",
			); err != nil {
				return nil, SyncAppOutput{}, err
			}

			processed := processFiles(input.Files)
			syncFiles := processed
			binaryName := input.BinaryName

			if mode == "binary" {
				for filePath := range processed {
					if binaryName == "" {
						binaryName = path.Base(strings.Trim(filePath, "/"))
					}
					syncFiles = map[string]string{binaryName: processed[filePath]}
					break
				}
			}

			archive, err := buildTar(syncFiles)
			if err != nil {
				return nil, SyncAppOutput{}, fmt.Errorf("build sync archive: %w", err)
			}

			if err := c.AppSync(
				input.Namespace,
				input.Name,
				archive,
				mode,
				input.Dest,
				binaryName,
			); err != nil {
				return nil, SyncAppOutput{}, wrapSyncError(err)
			}

			return nil, SyncAppOutput{
				Status: fmt.Sprintf(
					"synced app %q in namespace %q",
					input.Name,
					input.Namespace,
				),
				Mode: mode,
			}, nil
		},
	)
}

// wrapSyncError adds actionable guidance when sync fails because the pod no
// longer has the watch supervisor (e.g. after push_app or restart_app).
func wrapSyncError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	noReadyPod := strings.Contains(msg, "no ready pod")
	serviceUnavailable := strings.Contains(msg, "503")
	if noReadyPod || serviceUnavailable {
		return fmt.Errorf(
			"sync: %w — the pod likely lost the watch supervisor after a "+
				"redeploy; call watch_app_startup again before retrying sync_app",
			err,
		)
	}
	return fmt.Errorf("sync: %w", err)
}
