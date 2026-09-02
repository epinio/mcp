package tools

import (
	"context"
	"fmt"

	"github.com/epinio/mcp/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Gitconfigs are the credentials Epinio uses to pull source from a private
// git repo. This file implements EPINIO-679: gitconfig list/show/match/
// create/delete for the MCP server, mirroring what appcharts.go does for
// AppCharts.
//
// No update_gitconfig tool: EPINIO-343 ("Support updates to existing
// gitconfigs") turned out to mean explicit gitconfig selection at deploy
// time, the `global` flag on create, and GitHub/GitLab Enterprise provider
// fixes — not an edit-in-place API. Epinio's router registers only
// Index/Create/Delete/BatchDelete/Match/Show for gitconfigs (no PATCH), and
// the UI's GitConfigModal.vue has no edit flow either. Add update_gitconfig
// here once Epinio actually ships a PATCH /gitconfigs/:id route.

// GitconfigSummary is the view of a git configuration returned to MCP
// callers. Password and certificate data are write-only on Epinio's API and
// are never included here — mirrors client.Gitconfig, which Epinio itself
// excludes them from on read.
type GitconfigSummary struct {
	Name       string `json:"name"`
	Global     bool   `json:"global,omitempty"`
	URL        string `json:"url,omitempty"`
	Provider   string `json:"provider,omitempty"`
	Username   string `json:"username,omitempty"`
	UserOrg    string `json:"userorg,omitempty"`
	Repository string `json:"repository,omitempty"`
	SkipSSL    bool   `json:"skipssl,omitempty"`
}

type ListGitconfigsInput struct{}
type ListGitconfigsOutput struct {
	Gitconfigs []GitconfigSummary `json:"gitconfigs"`
}

type ShowGitconfigInput struct {
	Name string `json:"name" jsonschema:"the gitconfig id"`
}
type ShowGitconfigOutput struct {
	Gitconfig GitconfigSummary `json:"gitconfig"`
}

type MatchGitconfigsInput struct {
	Prefix string `json:"prefix,omitempty" jsonschema:"name prefix to match; empty matches every gitconfig"`
}
type MatchGitconfigsOutput struct {
	Names []string `json:"names"`
}

type CreateGitconfigInput struct {
	Name        string `json:"name" jsonschema:"resource id for the new gitconfig (lower case alphanumeric/'-', DNS-1123 subdomain, e.g. 'my-github')"`
	URL         string `json:"url" jsonschema:"the git server URL"`
	Provider    string `json:"provider" jsonschema:"git provider: 'git' (generic), 'github', 'github_enterprise_cloud', 'github_enterprise_self_hosted', 'gitlab', or 'gitlab_enterprise'"`
	Username    string `json:"username,omitempty" jsonschema:"git username"`
	Password    string `json:"password,omitempty" jsonschema:"git password or access token (write-only, never returned by list/show)"`
	UserOrg     string `json:"userorg,omitempty" jsonschema:"the GitHub/GitLab user or organization"`
	Repository  string `json:"repository,omitempty" jsonschema:"optional repository this config is scoped to"`
	SkipSSL     bool   `json:"skipssl,omitempty" jsonschema:"skip TLS verification against the git server"`
	Global      bool   `json:"global,omitempty" jsonschema:"make this gitconfig available to all users (admin only — Epinio rejects this for non-admins)"`
	Certificate string `json:"certificate,omitempty" jsonschema:"optional PEM-encoded CA certificate for the git server"`
}

type DeleteGitconfigInput struct {
	Names []string `json:"names" jsonschema:"one or more gitconfig ids to delete"`
}

type GitconfigWriteOutput struct {
	Name   string `json:"name,omitempty"`
	Status string `json:"status"`
}

func summarizeGitconfig(g client.Gitconfig) GitconfigSummary {
	return GitconfigSummary{
		Name:       g.Meta.Name,
		Global:     g.Global,
		URL:        g.URL,
		Provider:   g.Provider,
		Username:   g.Username,
		UserOrg:    g.UserOrg,
		Repository: g.Repository,
		SkipSSL:    g.SkipSSL,
	}
}

func RegisterGitconfigTools(server *mcp.Server, c *client.Client) {
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "list_gitconfigs",
			Annotations: &mcp.ToolAnnotations{Title: "List Git Configs"},
			Description: "List the git configurations (credentials Epinio uses to pull " +
				"from a private git repo) registered on the cluster. Passwords and " +
				"certificates are never included in the response.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			_ ListGitconfigsInput,
		) (*mcp.CallToolResult, ListGitconfigsOutput, error) {
			gitconfigs, err := c.ListGitconfigs()

			if err != nil {
				return nil, ListGitconfigsOutput{}, fmt.Errorf("list gitconfigs: %w", err)
			}

			out := ListGitconfigsOutput{
				Gitconfigs: make([]GitconfigSummary, 0, len(gitconfigs)),
			}
			for _, g := range gitconfigs {
				out.Gitconfigs = append(out.Gitconfigs, summarizeGitconfig(g))
			}

			return nil, out, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "show_gitconfig",
			Annotations: &mcp.ToolAnnotations{Title: "Show Git Config"},
			Description: "Fetch a single git configuration by id. Password and " +
				"certificate data are never returned.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input ShowGitconfigInput,
		) (*mcp.CallToolResult, ShowGitconfigOutput, error) {
			if input.Name == "" {
				return nil, ShowGitconfigOutput{}, fmt.Errorf("name is required")
			}

			g, err := c.ShowGitconfig(input.Name)

			if err != nil {
				return nil, ShowGitconfigOutput{}, fmt.Errorf(
					"show gitconfig %q: %w",
					input.Name,
					err,
				)
			}

			return nil, ShowGitconfigOutput{Gitconfig: summarizeGitconfig(*g)}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "match_gitconfigs",
			Annotations: &mcp.ToolAnnotations{Title: "Match Git Configs"},
			Description: "Return the ids of git configurations whose name starts with " +
				"the given prefix. Pass an empty prefix to match every gitconfig — " +
				"handy before a bulk delete_gitconfig call.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input MatchGitconfigsInput,
		) (*mcp.CallToolResult, MatchGitconfigsOutput, error) {
			names, err := c.GitconfigsMatch(input.Prefix)

			if err != nil {
				return nil, MatchGitconfigsOutput{}, fmt.Errorf("match gitconfigs: %w", err)
			}

			return nil, MatchGitconfigsOutput{Names: names}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "create_gitconfig",
			Annotations: &mcp.ToolAnnotations{Title: "Create Git Config"},
			Description: "Register a new git configuration. Set global=true to make " +
				"it available to all users — Epinio enforces admin-only for that flag " +
				"against your credentials.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input CreateGitconfigInput,
		) (*mcp.CallToolResult, GitconfigWriteOutput, error) {
			if input.Name == "" || input.URL == "" || input.Provider == "" {
				return nil, GitconfigWriteOutput{}, fmt.Errorf("name, url, and provider are required")
			}

			err := c.CreateGitconfig(client.GitconfigCreateRequest{
				ID:           input.Name,
				Global:       input.Global,
				URL:          input.URL,
				Provider:     input.Provider,
				UserOrg:      input.UserOrg,
				Repository:   input.Repository,
				SkipSSL:      input.SkipSSL,
				Username:     input.Username,
				Password:     input.Password,
				Certificates: []byte(input.Certificate),
			})
			if err != nil {
				return nil, GitconfigWriteOutput{}, fmt.Errorf("create gitconfig %q: %w", input.Name, err)
			}

			return nil, GitconfigWriteOutput{Name: input.Name, Status: "created"}, nil
		},
	)

	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "delete_gitconfig",
			Annotations: &mcp.ToolAnnotations{Title: "Delete Git Config"},
			Description: "Delete one or more git configurations in a single batch call.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input DeleteGitconfigInput,
		) (*mcp.CallToolResult, GitconfigWriteOutput, error) {
			if len(input.Names) == 0 {
				return nil, GitconfigWriteOutput{}, fmt.Errorf("names must contain at least one gitconfig id")
			}

			if err := c.DeleteGitconfig(input.Names); err != nil {
				return nil, GitconfigWriteOutput{}, fmt.Errorf("delete gitconfig(s) %v: %w", input.Names, err)
			}

			return nil, GitconfigWriteOutput{Status: "deleted"}, nil
		},
	)
}
