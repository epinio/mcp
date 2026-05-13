package tools

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"github.com/krumware/epinio-mcp/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetAppSourceInput struct {
	Namespace string `json:"namespace" jsonschema:"the namespace of the app"`
	Name      string `json:"name" jsonschema:"the app name"`
	// Extract: when true, the tarball is expanded in-memory and returned as a
	// map of file paths to UTF-8 string contents. Convenient for editing
	// flows. Binary files round-trip through UTF-8 with replacement chars —
	// use false + Tarball for byte-faithful copies.
	Extract bool `json:"extract,omitempty" jsonschema:"if true, return extracted text files; otherwise return the raw tarball as base64"`
}

type GetAppSourceOutput struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	BlobUID   string `json:"blobuid"`
	// Tarball is the raw staging tarball as base64 when Extract=false.
	Tarball string `json:"tarball,omitempty"`
	// Files is the extracted file tree when Extract=true.
	Files map[string]string `json:"files,omitempty"`
}

type ListAppFilesInput struct {
	Namespace string `json:"namespace" jsonschema:"the namespace of the app"`
	Name      string `json:"name" jsonschema:"the app name"`
}

type AppFileEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type ListAppFilesOutput struct {
	Namespace string         `json:"namespace"`
	Name      string         `json:"name"`
	BlobUID   string         `json:"blobuid"`
	Files     []AppFileEntry `json:"files"`
	TotalSize int64          `json:"total_size"`
	FileCount int            `json:"file_count"`
}

func RegisterAppSourceTools(server *mcp.Server, c *client.Client) {
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "get_app_source",
			Annotations: &mcp.ToolAnnotations{Title: "Get App Source"},
			Description: "Retrieve the source of a deployed Epinio application. " +
				"Resolves the app's blobuid from its Kubernetes CRD, then fetches " +
				"the staging tarball from the Epinio S3 gateway. Set extract=true " +
				"to get a flat file map instead of the raw tarball. Gated by the " +
				"app_editing capability.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input GetAppSourceInput,
		) (*mcp.CallToolResult, GetAppSourceOutput, error) {
			if err := requireCapability(ctx, c, "app_editing", ProbeContext{}); err != nil {
				return nil, GetAppSourceOutput{}, err
			}
			k, err := client.GetKubeClient()
			if err != nil {
				return nil, GetAppSourceOutput{}, fmt.Errorf("kubernetes client: %w", err)
			}
			s, err := client.GetS3Client(ctx)
			if err != nil {
				return nil, GetAppSourceOutput{}, fmt.Errorf("s3 client: %w", err)
			}
			blobuid, err := k.AppBlobUID(ctx, input.Namespace, input.Name)
			if err != nil {
				return nil, GetAppSourceOutput{}, err
			}
			body, _, err := s.GetObject(ctx, blobuid)
			if err != nil {
				return nil, GetAppSourceOutput{}, fmt.Errorf("fetch blob %q: %w", blobuid, err)
			}
			out := GetAppSourceOutput{
				Namespace: input.Namespace,
				Name:      input.Name,
				BlobUID:   blobuid,
			}
			if input.Extract {
				files, err := extractTar(body)
				if err != nil {
					return nil, GetAppSourceOutput{}, fmt.Errorf("extract tar: %w", err)
				}
				out.Files = files
				return nil, out, nil
			}
			out.Tarball = base64.StdEncoding.EncodeToString(body)
			return nil, out, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "list_app_files",
			Annotations: &mcp.ToolAnnotations{Title: "List App Files"},
			Description: "List the files in a deployed app's source tarball with " +
				"their sizes. Much cheaper (tokens-wise) than get_app_source for " +
				"'tell me about this app' style questions — returns paths + sizes " +
				"only, no file contents. Gated by the app_editing capability since " +
				"it needs the same blobuid + S3 lookup.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input ListAppFilesInput,
		) (*mcp.CallToolResult, ListAppFilesOutput, error) {
			if err := requireCapability(ctx, c, "app_editing", ProbeContext{}); err != nil {
				return nil, ListAppFilesOutput{}, err
			}
			k, err := client.GetKubeClient()
			if err != nil {
				return nil, ListAppFilesOutput{}, fmt.Errorf("kubernetes client: %w", err)
			}
			s, err := client.GetS3Client(ctx)
			if err != nil {
				return nil, ListAppFilesOutput{}, fmt.Errorf("s3 client: %w", err)
			}
			blobuid, err := k.AppBlobUID(ctx, input.Namespace, input.Name)
			if err != nil {
				return nil, ListAppFilesOutput{}, err
			}
			body, _, err := s.GetObject(ctx, blobuid)
			if err != nil {
				return nil, ListAppFilesOutput{}, fmt.Errorf("fetch blob %q: %w", blobuid, err)
			}
			entries, total, err := listTarEntries(body)
			if err != nil {
				return nil, ListAppFilesOutput{}, fmt.Errorf("list tar: %w", err)
			}
			return nil, ListAppFilesOutput{
				Namespace: input.Namespace,
				Name:      input.Name,
				BlobUID:   blobuid,
				Files:     entries,
				TotalSize: total,
				FileCount: len(entries),
			}, nil
		},
	)
}

// listTarEntries walks a (possibly gzip-compressed) tar in memory and returns
// one AppFileEntry per regular file. Unlike extractTar it reads only the
// header for each entry and seeks past the file data — the returned payload
// stays tiny regardless of tarball size.
func listTarEntries(raw []byte) ([]AppFileEntry, int64, error) {
	var r io.Reader = bytes.NewReader(raw)
	if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		gz, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, 0, fmt.Errorf("gzip header: %w", err)
		}
		defer gz.Close()
		r = gz
	}
	var total int64
	var out []AppFileEntry
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, 0, fmt.Errorf("tar header: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA { //nolint:staticcheck
			continue
		}
		out = append(out, AppFileEntry{Path: hdr.Name, Size: hdr.Size})
		total += hdr.Size
	}
	return out, total, nil
}

// extractTar walks a (possibly gzip-compressed) tar archive in memory and
// returns a map of file path to contents. Skips directories, symlinks, and
// other non-regular entries.
func extractTar(raw []byte) (map[string]string, error) {
	var r io.Reader = bytes.NewReader(raw)
	if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		gz, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("gzip header: %w", err)
		}
		defer gz.Close()
		r = gz
	}

	files := make(map[string]string)
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar header: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA { //nolint:staticcheck // TypeRegA is older archives
			continue
		}
		buf, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", hdr.Name, err)
		}
		files[hdr.Name] = string(buf)
	}
	return files, nil
}
