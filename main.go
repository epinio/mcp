package main

import (
	"context"
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
	port := envOrDefault("PORT", "8080")

	// OAuth 2.0 resource-server configuration. Both values are required, and
	// the server refuses to start without them: it has no credentials of its
	// own, so if it cannot tell a client where to log in, it cannot serve
	// anyone at all.
	//
	//   - resourceURL must equal the URL users enter in their MCP client,
	//     exactly. RFC 9728 requires the metadata's "resource" to match, and
	//     clients reject a mismatch.
	//   - issuer is the Dex issuer that fronts this Epinio (https://auth.<domain>).
	resourceURL := strings.TrimRight(os.Getenv("EPINIO_MCP_RESOURCE_URL"), "/")
	issuer := strings.TrimRight(os.Getenv("EPINIO_MCP_OIDC_ISSUER"), "/")
	if resourceURL == "" || issuer == "" {
		log.Fatalf(
			"EPINIO_MCP_RESOURCE_URL and EPINIO_MCP_OIDC_ISSUER are both required; " +
				"this server authenticates every caller against Epinio and has no " +
				"anonymous mode",
		)
	}
	prmURL := resourceURL + wellKnownPRM

	// The only client this process owns. It carries no credential and can
	// therefore only reach Epinio's unauthenticated endpoints. Every tool call
	// runs on a per-request client built from the caller's own token.
	anon := client.NewAnonymous(apiURL)

	info, err := anon.Info()

	if err != nil {
		log.Fatalf("failed to connect to Epinio API at %s: %v", apiURL, err)
	}
	log.Printf(
		"Connected to Epinio %s (k8s %s, platform %s)",
		info.Version,
		info.KubeVersion,
		info.Platform,
	)

	impl := &mcp.Implementation{
		Name:    "epinio-mcp",
		Version: version,
	}

	// Opt-in elevated tier.
	// EPINIO_MCP_ELEVATED enables the direct-Kubernetes tier: workload adoption
	// plus the capability framework (check/enable, self-adoption). It requires
	// the standard-elevated appchart's RBAC, see the install manifests.
	enableElevated := envBool("EPINIO_MCP_ELEVATED", false)
	log.Printf("tool tiers: core=on elevated=%t", enableElevated)

	// Install the adopted-app mutation guard once, here at startup, before any
	// request handler runs.
	if enableElevated {
		tools.SetAppMutationGuard(elevated.AdoptionGuard())
	}

	// registerTools wires the core Epinio-API tools plus the elevated tier when
	// enabled. Used for both the server-level client and each per-request client
	// so the exposed surface is identical either way.
	registerTools := func(s *mcp.Server, cl *client.Client) {
		tools.RegisterCore(s, cl)
		if enableElevated {
			elevated.Register(s, cl)
		}
	}

	mcpHandler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		// requireEpinioAuth has already validated the caller and put their
		// client in the request context, so every tool call runs as them.
		// Returning nil makes the SDK answer 400; it should be unreachable.
		c, ok := callerFrom(r.Context())
		if !ok {
			return nil
		}
		log.Printf("new MCP session for Epinio user %q", c.user)
		perReqServer := mcp.NewServer(impl, nil)
		registerTools(perReqServer, c.client)
		return perReqServer
	}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthzHandler(impl.Version))
	mux.HandleFunc("/readyz", readyzHandler(impl.Version, anon))

	// RFC 9728 protected resource metadata. This is how a client discovers
	// which authorization server to send the user to. Clients probe the
	// path-suffixed form first and the bare form second, so serve the subtree.
	prm := prmHandler(resourceURL, issuer)
	mux.HandleFunc(wellKnownPRM, prm)
	mux.HandleFunc(wellKnownPRM+"/", prm)
	// This server is a resource server, not an authorization server: anything
	// else under /.well-known/ is not ours to answer.
	mux.HandleFunc("/.well-known/", http.NotFound)

	// Everything else (including the MCP Streamable HTTP paths) is protected.
	// No credential, no tools — there is deliberately no anonymous fallback.
	mux.Handle("/", withRequestLogging(
		requireEpinioAuth(apiURL, prmURL, mcpHandler),
	))

	addr := fmt.Sprintf(":%s", port)
	log.Printf("Epinio MCP server listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// wellKnownPRM is the RFC 9728 protected-resource-metadata path.
const wellKnownPRM = "/.well-known/oauth-protected-resource"

// epinioAudienceScope is Dex's cross-client trust scope. Epinio verifies every
// token with client ID "epinio-api", so a token minted for any other client is
// rejected no matter how valid it is. Asking for this scope makes Dex issue a
// token whose audience is epinio-api, which is the same trick the Epinio CLI
// uses. A client for this server must be listed in epinio-api's trustedPeers.
const epinioAudienceScope = "audience:server:client_id:epinio-api"

// requestedScopes is what a client should ask the authorization server for.
// Advertised in the metadata document and repeated in the 401 challenge.
var requestedScopes = []string{
	"openid",
	"profile",
	"email",
	"groups",
	"federated:id",
	epinioAudienceScope,
}

// caller is an authenticated MCP client: the Epinio client built from their
// credential, plus who Epinio says they are.
type caller struct {
	client *client.Client
	user   string
}

type callerKey struct{}

func callerFrom(ctx context.Context) (*caller, bool) {
	c, ok := ctx.Value(callerKey{}).(*caller)
	return c, ok
}

// prmHandler serves RFC 9728 protected resource metadata, pointing clients at
// the Dex instance in front of this Epinio.
func prmHandler(resourceURL, issuer string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Discovery is pre-authentication by definition, and browsers preflight
		// it from the client origin.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":                 resourceURL,
			"authorization_servers":    []string{issuer},
			"bearer_methods_supported": []string{"header"},
			"scopes_supported":         requestedScopes,
			"resource_documentation":   "https://docs.epinio.io/reference/mcp",
		})
	}
}

// unauthorized answers with the challenge that starts an OAuth login. The 401
// status is what makes a client begin discovery; the resource_metadata pointer
// is what tells it where to look.
func unauthorized(w http.ResponseWriter, prmURL, errCode, description string) {
	challenge := fmt.Sprintf(
		`Bearer resource_metadata=%q, scope=%q`,
		prmURL,
		strings.Join(requestedScopes, " "),
	)
	if errCode == "" {
		// No credential at all is not an error condition, it is the start of
		// the login handshake, so the challenge carries no error code. The
		// body still explains itself for anyone reading it by hand.
		description = "authentication required: log in to Epinio via " +
			"the authorization server in the linked resource metadata"
	} else {
		challenge += fmt.Sprintf(
			`, error=%q, error_description=%q`,
			errCode,
			description,
		)
	}
	w.Header().Set("WWW-Authenticate", challenge)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":             errCode,
		"error_description": description,
	})
}

// requireEpinioAuth gates every MCP request on a credential that Epinio itself
// accepts. There is no fallback: a caller without an Epinio account (local user
// or Dex identity) gets a 401 and no tools.
//
// Epinio is the only authority here on purpose. This server does not keep a
// user list, does not verify JWTs itself, and cannot be configured to trust
// anyone Epinio would not. Asking Epinio's authenticated /me endpoint is the
// whole check: if Epinio answers, the caller is real, and the same credential
// is what every subsequent tool call will run under.
func requireEpinioAuth(apiURL, prmURL string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			unauthorized(w, prmURL, "", "")
			return
		}

		cl := perRequestClient(apiURL, authHeader)
		if cl == nil {
			unauthorized(
				w,
				prmURL,
				"invalid_request",
				"unsupported Authorization scheme: expected Bearer or Basic",
			)
			return
		}

		me, err := cl.Me()
		if err != nil {
			if client.IsUnauthorized(err) {
				unauthorized(
					w,
					prmURL,
					"invalid_token",
					"Epinio rejected the credential",
				)
				return
			}
			// Epinio is reachable but unhappy for some other reason. Saying
			// 401 here would send the client into a pointless re-login loop.
			log.Printf("auth check failed against Epinio: %v", err)
			http.Error(
				w,
				"cannot verify credential with Epinio",
				http.StatusBadGateway,
			)
			return
		}

		ctx := context.WithValue(
			r.Context(),
			callerKey{},
			&caller{client: cl, user: me.User},
		)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// perRequestClient builds an Epinio client scoped to a single MCP session
// using whichever credential flavor the caller presented. Returns nil if the
// Authorization header is unrecognized, which the caller turns into a 401.
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
		raw, err := base64.StdEncoding.DecodeString(
			strings.TrimPrefix(authHeader, "Basic "),
		)

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
			r.Method,
			r.URL.Path,
			summary,
			rec.status,
			time.Since(start).Round(time.Millisecond),
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
// The check runs without credentials against Epinio's unauthenticated /info,
// so readiness reflects reachability only. Caller credentials are validated
// per request, not here.
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
