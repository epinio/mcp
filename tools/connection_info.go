package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/krumware/epinio-mcp/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// GetConnectionInfoInput selects which capability's broker info the caller
// wants, and the target app. The target follows the "namespace/name" convention
// consistent with ProbeContext.ProbeApp so the same value can be passed to
// check_capabilities.
type GetConnectionInfoInput struct {
	Capability string `json:"capability" jsonschema:"capability name (e.g. log_streaming)"`
	Namespace  string `json:"namespace" jsonschema:"app namespace"`
	Name       string `json:"name" jsonschema:"app name"`
	// Follow applies to log_streaming — when true, the returned WebSocket URL
	// will tail logs indefinitely. Defaults to true since that's the whole
	// point of using the WS path.
	Follow *bool `json:"follow,omitempty" jsonschema:"for log_streaming: tail new lines (default true)"`
	// StageID for log_streaming switches the URL to the staging build's log
	// stream (a different Epinio route). Leave empty to tail runtime pods.
	StageID string `json:"stage_id,omitempty" jsonschema:"for log_streaming: tail the staging build instead of runtime pods"`
}

// GetConnectionInfoOutput hands the caller everything it needs to open a
// direct connection to the backing service, using the caller's own OIDC
// credential. The MCP does not mint new tokens — it forwards what it already
// has from the current request's Authorization header.
type GetConnectionInfoOutput struct {
	Capability string `json:"capability"`
	Target     string `json:"target"` // "namespace/name"
	Protocol   string `json:"protocol"`
	URL        string `json:"url"`
	// Token is the caller's bearer token — present only when the MCP is
	// running under per-request auth (i.e. the request carried an
	// Authorization header). Empty for env-var / basic auth deployments.
	Token     string `json:"token,omitempty"`
	TokenType string `json:"token_type,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"` // RFC3339 UTC, from JWT `exp`
	Note      string `json:"note,omitempty"`
}

// RegisterConnectionInfoTools adds get_connection_info. The tool is gated by
// the target capability's readiness — if the backing infrastructure isn't up,
// there's no useful URL to hand back.
func RegisterConnectionInfoTools(server *mcp.Server, c *client.Client) {
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name:        "get_connection_info",
			Annotations: &mcp.ToolAnnotations{Title: "Get Connection Info"},
			Description: "Return the URL and credentials a caller needs to connect " +
				"directly to a capability's backing service (e.g. Epinio's log " +
				"WebSocket). Forwards the caller's OIDC bearer token — the MCP does " +
				"not mint new credentials. Gated by the capability's readiness; only " +
				"call this after check_capabilities reports ready.",
		},
		func(
			ctx context.Context,
			req *mcp.CallToolRequest,
			input GetConnectionInfoInput,
		) (*mcp.CallToolResult, GetConnectionInfoOutput, error) {
			if input.Namespace == "" || input.Name == "" {
				return nil, GetConnectionInfoOutput{}, fmt.Errorf("namespace and name are required")
			}
			target := input.Namespace + "/" + input.Name

			// log_streaming (and other environmental capabilities) are cluster-wide
			// properties of Epinio, not per-app. We pass an empty ProbeContext so
			// any probe falls back to its default target — the gate should reflect
			// "can Epinio serve this capability" not "can we reach this specific
			// app's endpoint," which the caller will discover when they actually
			// dial the returned URL.
			if err := requireCapability(ctx, c, input.Capability, ProbeContext{}); err != nil {
				return nil, GetConnectionInfoOutput{}, err
			}

			switch input.Capability {
			case "log_streaming":
				out, err := logStreamConnectionInfo(c, input, target)
				if err != nil {
					return nil, GetConnectionInfoOutput{}, err
				}
				return nil, out, nil
			}
			return nil, GetConnectionInfoOutput{}, fmt.Errorf(
				"capability %q has no broker path — get_connection_info currently supports: log_streaming",
				input.Capability,
			)
		},
	)
}

func logStreamConnectionInfo(c *client.Client, input GetConnectionInfoInput, target string) (GetConnectionInfoOutput, error) {
	follow := true
	if input.Follow != nil {
		follow = *input.Follow
	}

	// Epinio's WS accepts only its own short-lived (~30s TTL) token via
	// ?authtoken=<X>, never the OIDC bearer header. BuildLogStreamURL handles
	// fetching that token and assembling the correct URL (runtime pod logs
	// vs staging build logs use different routes).
	wsURL, err := c.BuildLogStreamURL(input.Namespace, input.Name, input.StageID, follow)
	if err != nil {
		return GetConnectionInfoOutput{}, err
	}

	out := GetConnectionInfoOutput{
		Capability: input.Capability,
		Target:     target,
		Protocol:   "websocket",
		URL:        wsURL,
		// Epinio's WS authtoken expires in ~30 seconds — the browser must
		// dial promptly. After the handshake the connection stays open.
		ExpiresAt: time.Now().UTC().Add(25 * time.Second).Format(time.RFC3339),
		Note: "Authtoken embedded in the URL expires in 30s — dial immediately. " +
			"After the handshake the WS stays open. If the connection fails, " +
			"call get_connection_info again for a fresh URL, or fall back to " +
			"the polling /api/app-logs path.",
	}
	if input.StageID != "" {
		out.Note = "Staging log stream for stage " + input.StageID + ". " + out.Note
	}
	return out, nil
}

// jwtExpiry pulls the `exp` claim out of a JWT without verifying the
// signature. For display/TTL hints only — the upstream still enforces
// whatever validation it wants.
func jwtExpiry(token string) *time.Time {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Some encoders leave padding; try the permissive form.
		payload, err = base64.RawStdEncoding.DecodeString(parts[1])
		if err != nil {
			return nil
		}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil
	}
	if claims.Exp == 0 {
		return nil
	}
	t := time.Unix(claims.Exp, 0).UTC()
	return &t
}
