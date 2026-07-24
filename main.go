package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/epinio/mcp/client"
	"github.com/epinio/mcp/elevated"
	"github.com/epinio/mcp/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// version is the server version reported over MCP and on the health probes.
// It defaults to "dev" and is overridden at build time via
// -ldflags "-X main.version=<tag>" (see the Makefile and install/Dockerfile).
var version = "dev"

func main() {
	apiURL := envOrDefault("EPINIO_API_URL", "https://epinio.example.com")
	token := os.Getenv("EPINIO_TOKEN")
	refreshToken := os.Getenv("EPINIO_REFRESH_TOKEN")
	tokenEndpoint := os.Getenv("EPINIO_TOKEN_ENDPOINT")
	clientID := envOrDefault("EPINIO_OIDC_CLIENT_ID", "epinio-cli")
	username := envOrDefault("EPINIO_USERNAME", "admin")
	password := envOrDefault("EPINIO_PASSWORD", "password")
	port := envOrDefault("PORT", "8080")

	var c *client.Client
	if token != "" && refreshToken != "" && tokenEndpoint != "" {
		c = client.NewWithOIDC(apiURL, tokenEndpoint, clientID, token, refreshToken)
		log.Printf("Using OIDC auth with token refresh")
	} else if token != "" {
		c = client.NewWithToken(apiURL, token)
		log.Printf("Using static bearer token auth")
	} else {
		c = client.New(apiURL, username, password)
		log.Printf("Using basic auth (user: %s)", username)
	}

	info, err := c.Info()
	if err != nil {
		log.Fatalf("failed to connect to Epinio API at %s: %v", apiURL, err)
	}
	log.Printf("Connected to Epinio %s (k8s %s, platform %s)", info.Version, info.KubeVersion, info.Platform)

	impl := &mcp.Implementation{
		Name:    "epinio-mcp",
		Version: version,
	}

	// Opt-in elevated tiers. Default off — the beta is a pure Epinio-API MCP.
	// EPINIO_MCP_ELEVATED enables the read-only tier (capability reporting +
	// log-stream connection info), EPINIO_MCP_ELEVATED_ADOPTION the read-write
	// adoption tier. Both reach past the Epinio API into Kubernetes and require
	// the standard-elevated appchart's RBAC — see the install manifests.
	enableElevated := envBool("EPINIO_MCP_ELEVATED", false)
	enableAdoption := envBool("EPINIO_MCP_ELEVATED_ADOPTION", false)
	log.Printf("tool tiers: core=on elevated=%t adoption=%t", enableElevated, enableAdoption)

	// registerTools wires the core Epinio-API tools plus whichever elevated
	// tiers are enabled. Used for both the server-level client and each
	// per-request client so the exposed surface is identical either way.
	registerTools := func(s *mcp.Server, cl *client.Client) {
		tools.RegisterCore(s, cl)
		if enableElevated {
			elevated.RegisterReadOnly(s, cl)
		}
		if enableAdoption {
			elevated.RegisterAdoption(s, cl)
		}
	}

	server := mcp.NewServer(impl, nil)
	registerTools(server, c)

	mcpHandler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		// Per-request auth: forward whichever credential the caller sent to
		// a dedicated Epinio client for this session, so all tool calls
		// inherit the caller's identity. Falls back to server-level env-var
		// auth when no Authorization header is present.
		if authHeader := r.Header.Get("Authorization"); authHeader != "" {
			if perReqClient := perRequestClient(apiURL, authHeader); perReqClient != nil {
				perReqServer := mcp.NewServer(impl, nil)
				registerTools(perReqServer, perReqClient)
				return perReqServer
			}
		}
		return server
	}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthzHandler(impl.Version))
	mux.HandleFunc("/readyz", readyzHandler(impl.Version, c))
	// OAuth/OIDC discovery. This server authenticates by forwarding the caller's
	// Authorization header (or its own env credentials) to Epinio — it is not an
	// OAuth authorization server. MCP clients probe /.well-known/oauth-* before
	// connecting; answer those with a clean 404 so a well-behaved client
	// concludes "no OAuth required" and connects directly. Without this the
	// probes fall through to the MCP handler below, which rejects them as
	// malformed JSON-RPC and breaks the client's OAuth error handling.
	mux.HandleFunc("/.well-known/", http.NotFound)
	// Everything else (including the MCP Streamable HTTP paths) falls through
	// to the MCP handler, optionally wrapped with request logging.
	mux.Handle("/", withRequestLogging(mcpHandler))

	addr := fmt.Sprintf(":%s", port)
	log.Printf("Epinio MCP server listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// perRequestClient builds an Epinio client scoped to a single MCP session
// using whichever credential flavor the caller presented. Returns nil if
// the Authorization header is present but unrecognized — the caller will
// fall back to the server-level client in that case.
//
// Recognized headers:
//
//   - `Authorization: Bearer <token>` — forwarded to Epinio verbatim. Epinio
//     validates JWTs against Dex. Works for any Dex-issued OIDC token.
//   - `Authorization: Basic <base64(user:pass)>` — decoded and forwarded as
//     Basic auth to Epinio. Epinio validates against its local password
//     store (the same credentials `epinio login -u admin --password ...`
//     uses). Useful for agent managers that only support Basic auth.
func perRequestClient(apiURL, authHeader string) *client.Client {
	switch {
	case strings.HasPrefix(authHeader, "Bearer "):
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token == "" {
			return nil
		}
		return client.NewWithToken(apiURL, token)
	case strings.HasPrefix(authHeader, "Basic "):
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(authHeader, "Basic "))
		if err != nil {
			return nil
		}
		parts := strings.SplitN(string(raw), ":", 2)
		if len(parts) != 2 || parts[0] == "" {
			return nil
		}
		return client.New(apiURL, parts[0], parts[1])
	}
	return nil
}

// withRequestLogging adds a one-line access log per MCP request with a
// sanitized auth-header summary and response status. Enable/disable via the
// MCP_LOG_REQUESTS env var (default: on). Helpful when wiring the MCP into
// a new client and the bearer-token format isn't known — you can see the
// scheme, length, fingerprint, and (for JWTs) decoded claims without the
// full token hitting the log.
func withRequestLogging(next http.Handler) http.Handler {
	if v := strings.ToLower(os.Getenv("MCP_LOG_REQUESTS")); v == "false" || v == "0" || v == "off" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		authHeader := r.Header.Get("Authorization")
		summary := summarizeAuthHeader(authHeader)

		// Capture the response status so we can log it.
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		log.Printf(
			"mcp %s %s auth=[%s] status=%d dur=%s ua=%q",
			r.Method, r.URL.Path, summary, rec.status, time.Since(start).Round(time.Millisecond),
			truncate(r.Header.Get("User-Agent"), 40),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}

// Flush is required so streaming (SSE) responses aren't buffered through the
// recorder. The MCP uses Streamable HTTP which depends on prompt flushing.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// kubeconfigTokenRE matches Rancher/kubeconfig tokens of the form
// "kubeconfig-u-<id>:<secret>". We log a hit on this shape so it's obvious
// when a client is forwarding its own K8s API token instead of a Dex JWT.
var kubeconfigTokenRE = regexp.MustCompile(`^kubeconfig-[a-zA-Z]+-[a-zA-Z0-9]+:[a-zA-Z0-9]+$`)

// summarizeAuthHeader produces a non-secret, log-friendly summary of an
// Authorization header. Never logs the full token value or any password.
func summarizeAuthHeader(h string) string {
	h = strings.TrimSpace(h)
	if h == "" {
		return "none"
	}
	parts := strings.SplitN(h, " ", 2)
	scheme := parts[0]
	if len(parts) < 2 || parts[1] == "" {
		return scheme + " (no value)"
	}
	tok := parts[1]

	// Special-case Basic so we log the username (handy for multi-tenant
	// debugging) but never the password.
	if strings.EqualFold(scheme, "Basic") {
		raw, err := base64.StdEncoding.DecodeString(tok)
		if err != nil {
			return fmt.Sprintf("Basic (undecodable base64 len=%d)", len(tok))
		}
		up := strings.SplitN(string(raw), ":", 2)
		user := up[0]
		return fmt.Sprintf("Basic user=%s", user)
	}

	// For Bearer and anything else, fingerprint the token + identify shape.
	fp := fingerprint(tok)
	var kind string
	switch {
	case looksLikeJWT(tok):
		kind = "jwt"
	case kubeconfigTokenRE.MatchString(tok):
		kind = "rancher-kubeconfig"
	default:
		kind = "opaque"
	}
	base := fmt.Sprintf("%s %s len=%d %s", scheme, kind, len(tok), fp)
	if kind == "jwt" {
		if claims := decodeJWTClaims(tok); claims != "" {
			base += " " + claims
		}
	}
	return base
}

// looksLikeJWT is a cheap heuristic — three base64url-ish segments separated
// by dots. Avoids allocating a decoder just to identify shape.
func looksLikeJWT(tok string) bool {
	if strings.Count(tok, ".") != 2 {
		return false
	}
	for _, seg := range strings.Split(tok, ".") {
		if seg == "" {
			return false
		}
	}
	return true
}

// decodeJWTClaims returns a short string with the JWT's `iss`, `aud`, `sub`,
// and `exp` (when set) — without verifying the signature. Display only.
func decodeJWTClaims(tok string) string {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Some JWTs are emitted with padding — try std url encoding too.
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return ""
		}
	}
	var claims struct {
		Iss string      `json:"iss,omitempty"`
		Sub string      `json:"sub,omitempty"`
		Aud interface{} `json:"aud,omitempty"`
		Exp int64       `json:"exp,omitempty"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	var pieces []string
	if claims.Iss != "" {
		pieces = append(pieces, fmt.Sprintf("iss=%s", claims.Iss))
	}
	if claims.Sub != "" {
		pieces = append(pieces, fmt.Sprintf("sub=%s", truncate(claims.Sub, 24)))
	}
	if claims.Aud != nil {
		pieces = append(pieces, fmt.Sprintf("aud=%v", claims.Aud))
	}
	if claims.Exp > 0 {
		remaining := time.Until(time.Unix(claims.Exp, 0)).Round(time.Minute)
		pieces = append(pieces, fmt.Sprintf("exp_in=%s", remaining))
	}
	if len(pieces) == 0 {
		return ""
	}
	return "{" + strings.Join(pieces, " ") + "}"
}

// fingerprint returns a short first-and-last-chars preview that's enough to
// distinguish two tokens but not enough to reuse one.
func fingerprint(s string) string {
	switch {
	case len(s) <= 8:
		return fmt.Sprintf("%q", strings.Repeat("*", len(s)))
	case len(s) <= 16:
		return fmt.Sprintf("%s…%s", s[:2], s[len(s)-2:])
	default:
		return fmt.Sprintf("%s…%s", s[:6], s[len(s)-6:])
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// healthzHandler is a liveness probe: the process is up. It does not check
// any upstream dependencies — use /readyz for that. K8s treats a failing
// liveness probe as "restart the pod," so this endpoint intentionally stays
// simple and local.
func healthzHandler(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"version": version,
		})
	}
}

// readyzHandler is a readiness probe: the MCP can reach Epinio. When Epinio
// is unreachable (upstream outage, token expiry), the probe returns 503 so
// a fronting load balancer stops sending traffic until recovery.
//
// The upstream check uses the server-level Client (env-var auth). Per-request
// tokens are not validated here — they're only seen inside a session.
func readyzHandler(version string, c *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{
			"status":  "ok",
			"version": version,
		}
		status := http.StatusOK

		info, err := c.Info()
		if err != nil {
			body["status"] = "degraded"
			body["epinio_error"] = err.Error()
			status = http.StatusServiceUnavailable
		} else {
			body["epinio"] = map[string]string{
				"version":      info.Version,
				"kube_version": info.KubeVersion,
				"platform":     info.Platform,
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envBool reads a boolean-ish env var. Truthy: 1/true/yes/on (any case).
// Anything else (including unset) returns the fallback for unset and false
// for an explicit non-truthy value.
func envBool(key string, fallback bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if v == "" {
		return fallback
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
