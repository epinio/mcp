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

	"github.com/epinio/mcp/client"
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
	Files     []AppFileEntry `json:"files"`
	TotalSize int64          `json:"total_size"`
	FileCount int            `json:"file_count"`
}

// RegisterAppSourceTools registers the source-retrieval tools. These wire
// directly to Epinio's GET .../applications/:app/source endpoint — the server
// streams the staging tarball from S3 on the caller's behalf, so the MCP needs
// no Kubernetes or S3 access of its own.
func RegisterAppSourceTools(server *mcp.Server, c *client.Client) {
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "get_app_source",
			Annotations: &mcp.ToolAnnotations{Title: "Get App Source"},
			Description: "Retrieve the source of a deployed Epinio application via " +
				"the Epinio API. Set extract=true to get a flat file map instead of " +
				"the raw tarball. The app must have been staged at least once; an " +
				"app with no stored source returns an error.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input GetAppSourceInput,
		) (*mcp.CallToolResult, GetAppSourceOutput, error) {
			body, err := c.GetAppSource(input.Namespace, input.Name)
			if err != nil {
				return nil, GetAppSourceOutput{}, err
			}
			out := GetAppSourceOutput{
				Namespace: input.Namespace,
				Name:      input.Name,
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
				"only, no file contents.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input ListAppFilesInput,
		) (*mcp.CallToolResult, ListAppFilesOutput, error) {
			body, err := c.GetAppSource(input.Namespace, input.Name)
			if err != nil {
				return nil, ListAppFilesOutput{}, err
			}
			entries, total, err := listTarEntries(body)
			if err != nil {
				return nil, ListAppFilesOutput{}, fmt.Errorf("list tar: %w", err)
			}
			return nil, ListAppFilesOutput{
				Namespace: input.Namespace,
				Name:      input.Name,
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
	tr, err := tarReader(raw)
	if err != nil {
		return nil, 0, err
	}
	var total int64
	var out []AppFileEntry
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, 0, fmt.Errorf("tar header: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
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
	tr, err := tarReader(raw)
	if err != nil {
		return nil, err
	}
	files := make(map[string]string)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar header: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
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

// tarReader returns a *tar.Reader over raw, transparently handling a gzip
// wrapper (the staging blob may be stored gzip-compressed or plain).
func tarReader(raw []byte) (*tar.Reader, error) {
	var r io.Reader = bytes.NewReader(raw)
	if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		gz, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("gzip header: %w", err)
		}
		r = gz
	}
	return tar.NewReader(r), nil
}
