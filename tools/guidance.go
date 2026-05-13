package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/krumware/epinio-mcp/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Guidance content is exposed via two parallel mechanisms:
//
//   1. MCP Prompts (prompts/list + prompts/get) — clients that support
//      prompts (Claude Desktop, Claude Code, etc.) surface these as slash
//      commands or let the model request them by name.
//   2. A universal tool — `get_build_guidance(topic, language?)` — for
//      clients that don't implement prompts yet. Same content, same keys.
//
// To add new guidance: add an entry to `guidanceTopics`. The registry picks
// it up automatically — both the prompts and the tool enumerate the same
// map so they stay in lockstep.

type guidanceTopic struct {
	// Machine key (e.g., "deploy_guide"). Used as both the prompt name
	// and as the `topic` argument to get_build_guidance.
	Key string
	// Human-readable title for UIs.
	Title string
	// Short description surfaced in prompts/list.
	Description string
	// Render builds the content body. Some topics take a language
	// argument; others ignore it.
	Render func(language string) string
}

var guidanceTopics = []guidanceTopic{
	{
		Key:         "deploy_guide",
		Title:       "Deploy an app to Epinio",
		Description: "How to structure a source tarball so Epinio's Paketo buildpacks detect and build it cleanly. Supports an optional `language` argument: node, go, python, next, or empty for general guidance.",
		Render:      renderDeployGuide,
	},
	{
		Key:         "pick_appchart",
		Title:       "Choose an Epinio appchart",
		Description: "Guidance on selecting `configuration.appchart` (standard vs. standard-elevated vs. specialized charts). Cross-reference the live options via the list_appcharts tool.",
		Render:      func(string) string { return pickAppchartBody },
	},
	{
		Key:         "pick_builder",
		Title:       "Choose a buildpack builder image",
		Description: "Guidance on selecting `builder_image` for push_app / upload_and_stage. Cross-reference the live list via the list_builders tool.",
		Render:      func(string) string { return pickBuilderBody },
	},
	{
		Key:         "troubleshoot_staging",
		Title:       "Troubleshoot a failed Epinio build",
		Description: "Common staging failure patterns (missing package.json, Turbopack breakage, 504 from ingress, app not fully initialized) and how to recover.",
		Render:      func(string) string { return troubleshootStagingBody },
	},
}

// topicByKey is populated at init for O(1) lookup.
var topicByKey = map[string]guidanceTopic{}

func init() {
	for _, t := range guidanceTopics {
		topicByKey[t.Key] = t
	}
}

// --- Rendering ---

func renderDeployGuide(language string) string {
	language = strings.ToLower(strings.TrimSpace(language))
	var body strings.Builder
	body.WriteString(deployGuideCommon)
	switch language {
	case "node", "nodejs", "node.js":
		body.WriteString("\n\n")
		body.WriteString(deployGuideNode)
	case "next", "nextjs", "next.js":
		body.WriteString("\n\n")
		body.WriteString(deployGuideNode)
		body.WriteString("\n\n")
		body.WriteString(deployGuideNext)
	case "go", "golang":
		body.WriteString("\n\n")
		body.WriteString(deployGuideGo)
	case "python":
		body.WriteString("\n\n")
		body.WriteString(deployGuidePython)
	case "", "all", "general":
		body.WriteString("\n\n")
		body.WriteString(deployGuideNode)
		body.WriteString("\n\n")
		body.WriteString(deployGuideGo)
		body.WriteString("\n\n")
		body.WriteString(deployGuidePython)
	default:
		body.WriteString("\n\n")
		body.WriteString(fmt.Sprintf("(No language-specific section for %q — see list_builders for supported ecosystems.)", language))
	}
	return body.String()
}

// We build the content as raw strings with `@` as a sentinel for Markdown
// inline-code backticks. Embedding literal backticks in Go raw strings
// requires ugly concat patterns that are easy to break; a single
// strings.ReplaceAll at render time is cleaner.
const bt = "`"

var (
	deployGuideCommon     = useBackticks(deployGuideCommonRaw)
	deployGuideNode       = useBackticks(deployGuideNodeRaw)
	deployGuideNext       = useBackticks(deployGuideNextRaw)
	deployGuideGo         = useBackticks(deployGuideGoRaw)
	deployGuidePython     = useBackticks(deployGuidePythonRaw)
	pickAppchartBody      = useBackticks(pickAppchartBodyRaw)
	pickBuilderBody       = useBackticks(pickBuilderBodyRaw)
	troubleshootStagingBody = useBackticks(troubleshootStagingBodyRaw)
)

func useBackticks(s string) string { return strings.ReplaceAll(s, "@", bt) }

const deployGuideCommonRaw = `# Deploying to Epinio

Epinio builds apps via Cloud Native Buildpacks (Paketo by default). Your
source tarball is extracted into @/workspace/source/app/@ inside the builder,
then each buildpack runs a "detect" phase against that directory. Whichever
buildpack matches first gets to build the app.

## Universal rules

- Place language-specific project files (package.json, go.mod, requirements.txt, …)
  **at the root of the tarball**, not nested inside an extra @app/@ directory.
  A common push_app mistake is wrapping everything in an @app/@ subfolder —
  the buildpack then fails with "no 'package.json' found in project path
  /workspace/source/app" because the file is actually at
  @/workspace/source/app/app/package.json@.
- **Listen on $PORT**. Epinio injects @PORT@ into the runtime container;
  bind to @0.0.0.0:$PORT@ (fallback to 8080 if unset).
- **App name** must be lowercase, alphanumeric + hyphens only.
- **Deploy namespace** can be any Epinio namespace the caller has access to;
  default is @workspace@. List namespaces with the list_namespaces tool.
- Use @list_appcharts@ and @list_builders@ to see what's actually available
  on the target cluster — don't hard-code guesses.`

const deployGuideNodeRaw = `## Node.js

- @package.json@ at root. **Must** have a @"start"@ script
  (e.g. @"start": "node server.js"@). The Paketo Node.js buildpack
  uses @npm start@ as the launch process.
- @server.js@ (or whatever the start script invokes) must bind to
  @process.env.PORT || 8080@.
- Keep dependencies minimal — every new dep slows the build.
- Static assets typically live under @public/@ and are served by your
  Express/Fastify/etc. handler.`

const deployGuideNextRaw = `## Next.js 16+

- Use @next build --webpack@ in the @build@ script. Next.js 16 defaults
  to Turbopack for production builds, which breaks in Paketo's buildpack
  environment with "TurbopackInternalError: Symlink node_modules is invalid,
  it points out of the filesystem root".
- For standalone output, copy @.next/static@ and @public/@ into
  @.next/standalone/@ after @next build@, then launch with
  @HOSTNAME=0.0.0.0 node .next/standalone/server.js@.`

const deployGuideGoRaw = `## Go

- @go.mod@ at root. Source typically under @./@ or @./cmd/<name>@.
- Bind to @:$PORT@ (default to 8080 if unset).
- For the Paketo @builder-jammy-tiny@ (statically-linked Go only), make sure
  you don't link against glibc. Other builders accept glibc binaries.`

const deployGuidePythonRaw = `## Python

- @requirements.txt@ at root. For modern projects, @pyproject.toml@ is
  also detected.
- Recommended to include a @Procfile@ (one line, e.g. @web: gunicorn app:app@)
  so the launch command is explicit.
- Bind to @0.0.0.0:$PORT@, not @127.0.0.1@.`

const pickAppchartBodyRaw = `# Choosing an Epinio appchart

The @configuration.appchart@ field on an app selects which Helm chart
Epinio uses to materialize the workload. Valid values come from the cluster's
registered AppCharts — **call @list_appcharts@** for the authoritative
current set.

## Common choices

- **standard** — the default. Use for anything that doesn't need to call the
  Kubernetes API directly. Almost every app belongs here.

- **standard-elevated** — same shape as standard but the pod's ServiceAccount
  has extra RBAC (read access to Epinio application CRDs, access to registry
  credentials). Use when the app genuinely needs K8s API access — e.g. an
  operator, or a service that reads its own Epinio metadata. Don't pick this
  just to "be safe"; elevated RBAC is a larger blast radius if the app is
  compromised.

- **rancher-extension** — purpose-built for shipping Rancher UI extensions.
  Takes two settings: @extName@ and @extVersion@.

## Settings

Each appchart exposes a settings schema. Call @show_appchart(name)@ to see
the settings an appchart accepts and their types. Pass only keys that appear
in that schema via the @settings@ map on create_app/update_app/push_app.`

const pickBuilderBodyRaw = `# Choosing a buildpack builder image

A "builder" is a pre-baked OCI image that bundles a set of Paketo buildpacks.
When you push, the builder detects your source's language/framework and runs
the matching buildpack. The exact image you pass is the @builder_image@
argument to push_app / upload_and_stage.

**Always cross-reference** @list_builders@ for the current authoritative set
usable on-cluster. The known builders at the time of writing:

- **paketobuildpacks/builder-jammy-full:0.3.606** (default) — kitchen-sink
  builder with Node.js, Go, Python, Java, Ruby, and .NET buildpacks. Use
  this unless you have a specific reason not to.

- **krumware/builder-rancher-ext:1.0.26** — specialized for Rancher UI
  Extension packaging. Only use when deploying an extension via the
  @rancher-extension@ appchart.

## If you don't know what to pick

- Check the files first: @package.json@ → Node; @go.mod@ → Go; etc.
- Pick the default (jammy-full) and only override when the cluster has a
  tighter, more appropriate option.
- If @epinio_info@ returns a @default_builder_image@, that's the cluster's
  blessed default and is usually the right first choice.`

const troubleshootStagingBodyRaw = `# Troubleshooting a failed Epinio build

## "no 'package.json' found in project path /workspace/source/app"

Your Node files ended up nested one level too deep. The tarball probably
contains @app/package.json@ instead of just @package.json@. Fix: pass
file paths at the top level (e.g. @package.json@, @server.js@,
@public/index.html@). Same root-cause for equivalent errors on other
buildpacks (@go.mod@, @requirements.txt@, etc.).

## "TurbopackInternalError: Symlink node_modules is invalid..."

Next.js 16+ defaults to Turbopack for production builds and doesn't play
nice with Paketo's layered @node_modules@ directory. Fix: change your
@build@ script to @next build --webpack@.

## Push returns a gateway timeout / HTML error page

Long builds can outlast the ingress's @proxy_read_timeout@ (typically 60s).
Epinio keeps working in the background even after the proxy cuts the HTTP
response. push_app retries transparently; wait and check show_app — the app
may already be deployed. If using a client outside this MCP, re-call
show_app a few minutes later to confirm.

## show_app returns 404 after a push

Epinio listed the app via list_apps but can't find full details. Usually the
previous push failed before the Application resource was fully written. Fix:
delete_app, then push again.

## App is listed but @staging_status: failed@

Pull the failed build's logs with @app_logs(namespace, name, stage_id)@ —
pass the @stage_id@ from show_app. The build log usually makes the failure
obvious: missing file, syntax error, dependency resolution failure.

## Pod never reaches ready

Check @app_logs@ without a stage_id for runtime logs. Common causes:
binding to 127.0.0.1 instead of 0.0.0.0, ignoring $PORT, crashing on a
missing environment variable (set with @set_env@).`

// --- Lookup + render ---

func renderGuidance(topic, language string) (string, string, error) {
	t, ok := topicByKey[topic]
	if !ok {
		available := make([]string, 0, len(guidanceTopics))
		for _, x := range guidanceTopics {
			available = append(available, x.Key)
		}
		sort.Strings(available)
		return "", "", fmt.Errorf("unknown topic %q — available: %s", topic, strings.Join(available, ", "))
	}
	return t.Title, t.Render(language), nil
}

// --- Tool I/O ---

type GetBuildGuidanceInput struct {
	Topic    string `json:"topic" jsonschema:"guidance topic name (e.g. deploy_guide, pick_appchart, pick_builder, troubleshoot_staging). Call with an unknown topic to see the current list."`
	Language string `json:"language,omitempty" jsonschema:"optional language hint for deploy_guide — one of: node, go, python, next. Ignored by other topics."`
}
type GetBuildGuidanceOutput struct {
	Topic   string `json:"topic"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

// --- Registration ---

// RegisterGuidanceTools wires up the guidance content both as MCP Prompts
// (preferred — clients that support the prompts protocol can request them
// by name) and as a `get_build_guidance` tool (universal fallback — works
// with any MCP client since every client supports tool calls).
func RegisterGuidanceTools(server *mcp.Server, _ *client.Client) {
	// Register one prompt per topic. MCP Prompts are the standardized way
	// for servers to offer "here's a block of canned context you can inject."
	for _, t := range guidanceTopics {
		topic := t // capture
		prompt := &mcp.Prompt{
			Name:        topic.Key,
			Title:       topic.Title,
			Description: topic.Description,
		}
		// Only deploy_guide consumes the language argument today; declare
		// it so clients that render prompt forms can prompt for it.
		if topic.Key == "deploy_guide" {
			prompt.Arguments = []*mcp.PromptArgument{
				{
					Name:        "language",
					Description: "Language/framework to tailor the guidance to (node, go, python, next). Omit for general guidance.",
					Required:    false,
				},
			}
		}
		server.AddPrompt(prompt, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			language := ""
			if req.Params != nil {
				language = req.Params.Arguments["language"]
			}
			title, body, err := renderGuidance(topic.Key, language)
			if err != nil {
				return nil, err
			}
			return &mcp.GetPromptResult{
				Description: title,
				Messages: []*mcp.PromptMessage{
					{Role: "user", Content: &mcp.TextContent{Text: body}},
				},
			}, nil
		})
	}

	// Tool version — same content, discoverable via tools/list for clients
	// that don't implement prompts/* yet. Clients should prefer the prompt
	// if they can, but the tool is always here as a fallback.
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "get_build_guidance",
			Annotations: &mcp.ToolAnnotations{Title: "Get Build Guidance"},
			Description: "Return canned guidance on how to build and deploy apps to " +
				"this Epinio cluster in a way that plays nicely with the available " +
				"buildpacks and appcharts. Topics: deploy_guide (optionally tailored " +
				"by language), pick_appchart, pick_builder, troubleshoot_staging. " +
				"Prefer the equivalent MCP prompt when your client supports prompts/get.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input GetBuildGuidanceInput,
		) (*mcp.CallToolResult, GetBuildGuidanceOutput, error) {
			title, body, err := renderGuidance(input.Topic, input.Language)
			if err != nil {
				return nil, GetBuildGuidanceOutput{}, err
			}
			return nil, GetBuildGuidanceOutput{
				Topic:   input.Topic,
				Title:   title,
				Content: body,
			}, nil
		},
	)
}
