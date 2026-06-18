package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/oauth2"
)

// Client communicates with the Epinio API server.
type Client struct {
	BaseURL     string
	Username    string
	Password    string
	Token       string
	tokenSource oauth2.TokenSource
	httpClient  *http.Client
}

// tlsConfig returns a TLS config. Verification is skipped by default because
// Epinio clusters commonly use self-signed certificates. Set
// EPINIO_TLS_SKIP_VERIFY=false to enable full verification.
func tlsConfig() *tls.Config {
	skip := os.Getenv("EPINIO_TLS_SKIP_VERIFY") != "false"
	return &tls.Config{InsecureSkipVerify: skip} //nolint:gosec
}

func tlsTransport() *http.Transport {
	return &http.Transport{TLSClientConfig: tlsConfig()}
}

// New creates a new Epinio API client with basic auth.
func New(baseURL, username, password string) *Client {
	baseURL = strings.TrimRight(baseURL, "/")
	return &Client{
		BaseURL:    baseURL,
		Username:   username,
		Password:   password,
		httpClient: &http.Client{Transport: tlsTransport()},
	}
}

// NewWithToken creates a new Epinio API client with a static bearer token (no refresh).
func NewWithToken(baseURL, token string) *Client {
	baseURL = strings.TrimRight(baseURL, "/")
	return &Client{
		BaseURL:    baseURL,
		Token:      token,
		httpClient: &http.Client{Transport: tlsTransport()},
	}
}

// NewWithOIDC creates a new Epinio API client with OIDC token refresh support.
func NewWithOIDC(baseURL, tokenEndpoint, clientID, accessToken, refreshToken string) *Client {
	baseURL = strings.TrimRight(baseURL, "/")

	config := &oauth2.Config{
		ClientID: clientID,
		Endpoint: oauth2.Endpoint{
			TokenURL: tokenEndpoint,
		},
	}

	token := &oauth2.Token{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
	}

	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{
		Transport: tlsTransport(),
	})

	ts := config.TokenSource(ctx, token)

	return &Client{
		BaseURL:     baseURL,
		Token:       accessToken,
		tokenSource: ts,
		httpClient:  &http.Client{Transport: tlsTransport()},
	}
}

func (c *Client) setAuth(req *http.Request) {
	if c.tokenSource != nil {
		tok, err := c.tokenSource.Token()
		if err != nil {
			log.Printf("warning: failed to refresh token: %v", err)
			req.Header.Set("Authorization", "Bearer "+c.Token)
			return
		}
		if tok.AccessToken != c.Token {
			log.Printf("Refreshed expired OIDC token")
			c.Token = tok.AccessToken
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
	} else if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	} else {
		req.SetBasicAuth(c.Username, c.Password)
	}
}

func (c *Client) url(path string) string {
	return c.BaseURL + "/api/v1" + path
}

func (c *Client) do(method, path string, body any, result any) error {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.url(path), bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	c.setAuth(req)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		// Intermediate proxies (nginx ingress, Traefik) return HTML error pages
		// on 5xx — dumping a page full of HTML into a tool response makes for
		// a terrible error. Detect it and surface something concise.
		snippet := string(respBody)
		if isHTMLResponse(resp, respBody) {
			snippet = fmt.Sprintf("proxy returned an HTML error page (%d bytes) — likely an ingress timeout or outage", len(respBody))
		}
		return fmt.Errorf("API error %d: %s", resp.StatusCode, snippet)
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("unmarshal response: %w", err)
		}
	}
	return nil
}

// isHTMLResponse returns true when the response looks like an HTML error page
// from an intermediate proxy (nginx, Traefik, etc.) rather than a structured
// Epinio JSON error. Checks both the Content-Type and a body sniff so we catch
// proxies that set text/plain but ship HTML anyway.
func isHTMLResponse(resp *http.Response, body []byte) bool {
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "text/html") {
		return true
	}
	head := strings.TrimSpace(string(body))
	if len(head) > 256 {
		head = head[:256]
	}
	lower := strings.ToLower(head)
	return strings.HasPrefix(lower, "<!doctype html") || strings.HasPrefix(lower, "<html")
}

// isProxyTimeout returns true when an error from c.do looks like an
// intermediate proxy gave up on a long-running Epinio request. Epinio
// itself keeps working in that case, so callers polling for completion
// can safely retry.
func isProxyTimeout(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// HTTP status codes intermediate proxies return when they stop waiting.
	if strings.Contains(msg, "API error 504") ||
		strings.Contains(msg, "API error 502") ||
		strings.Contains(msg, "API error 408") {
		return true
	}
	// Transport-level: the client closed or the server closed on us before
	// we got a status. Treat these as retryable on the wait endpoints.
	if strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "context deadline exceeded") {
		return true
	}
	return false
}

// Info returns server information.
func (c *Client) Info() (*InfoResponse, error) {
	var resp InfoResponse
	if err := c.do("GET", "/info", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListNamespaces returns all namespaces.
func (c *Client) ListNamespaces() ([]Namespace, error) {
	var resp []Namespace
	if err := c.do("GET", "/namespaces", nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// CreateNamespace creates a new namespace.
func (c *Client) CreateNamespace(name string) error {
	body := map[string]string{"name": name}
	return c.do("POST", "/namespaces", body, nil)
}

// DeleteNamespace deletes a namespace.
func (c *Client) DeleteNamespace(name string) error {
	return c.do("DELETE", "/namespaces/"+name, nil, nil)
}

// ListApps lists applications in a namespace.
func (c *Client) ListApps(namespace string) ([]App, error) {
	var resp []App
	if err := c.do("GET", "/namespaces/"+namespace+"/applications", nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// ListAllApps lists applications across all namespaces.
func (c *Client) ListAllApps() ([]App, error) {
	var resp []App
	if err := c.do("GET", "/applications", nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// ShowApp returns details of a specific application.
func (c *Client) ShowApp(namespace, name string) (*App, error) {
	var resp App
	if err := c.do("GET", "/namespaces/"+namespace+"/applications/"+name, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CreateApp creates a new application.
func (c *Client) CreateApp(namespace string, req AppCreateRequest) error {
	return c.do("POST", "/namespaces/"+namespace+"/applications", req, nil)
}

// DeleteApp deletes an application.
func (c *Client) DeleteApp(namespace, name string) error {
	return c.do("DELETE", "/namespaces/"+namespace+"/applications/"+name, nil, nil)
}

// UpdateApp updates an application.
func (c *Client) UpdateApp(namespace, name string, req AppUpdateRequest) error {
	return c.do("PATCH", "/namespaces/"+namespace+"/applications/"+name, req, nil)
}

// RestartApp restarts an application.
func (c *Client) RestartApp(namespace, name string) error {
	return c.do("POST", "/namespaces/"+namespace+"/applications/"+name+"/restart", nil, nil)
}

// UploadApp uploads application source and returns the blob UID.
func (c *Client) UploadApp(namespace, name string, source io.Reader) (*UploadResponse, error) {
	endpoint := c.url("/namespaces/" + namespace + "/applications/" + name + "/store")

	// Epinio expects multipart/form-data with a "file" field containing the tar.gz
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "source.tar.gz")
	if err != nil {
		return nil, fmt.Errorf("create multipart form: %w", err)
	}
	if _, err := io.Copy(part, source); err != nil {
		return nil, fmt.Errorf("write to multipart form: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart form: %w", err)
	}

	req, err := http.NewRequest("POST", endpoint, &buf)
	if err != nil {
		return nil, fmt.Errorf("create upload request: %w", err)
	}
	c.setAuth(req)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upload: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("upload error %d: %s", resp.StatusCode, string(body))
	}

	var result UploadResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("unmarshal upload response: %w", err)
	}
	return &result, nil
}

// StageApp stages an application build.
func (c *Client) StageApp(namespace, name string, req StageRequest) (*StageResponse, error) {
	var resp StageResponse
	if err := c.do("POST", "/namespaces/"+namespace+"/applications/"+name+"/stage", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// stagingCompleteMaxWait caps how long we'll keep trying before giving up.
// Each underlying Epinio call blocks until the stage Job finishes, which is
// why ingress proxies sometimes 504 us mid-wait.
const stagingCompleteMaxWait = 15 * time.Minute

// StagingComplete waits for the staging Job to finish. Epinio's
// `/staging/:id/complete` endpoint blocks until the Job is done, so a slow
// build can easily outlast an ingress proxy's `proxy_read_timeout` and
// return a 504 HTML page even though Epinio is still working. We retry in
// that case — each subsequent call will either block again or return
// immediately if the Job already finished between attempts.
func (c *Client) StagingComplete(namespace, stageID string) error {
	path := "/namespaces/" + namespace + "/staging/" + stageID + "/complete"
	deadline := time.Now().Add(stagingCompleteMaxWait)
	var lastErr error
	for {
		err := c.do("GET", path, nil, nil)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isProxyTimeout(err) {
			return err // genuine failure — surface it
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("staging did not complete within %s (last error: %w)", stagingCompleteMaxWait, lastErr)
		}
		// Brief backoff before retrying — Epinio is still working upstream.
		time.Sleep(2 * time.Second)
	}
}

// DeployApp deploys a staged application.
func (c *Client) DeployApp(namespace, name string, req DeployRequest) (*DeployResponse, error) {
	var resp DeployResponse
	if err := c.do("POST", "/namespaces/"+namespace+"/applications/"+name+"/deploy", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// appRunningMaxWait caps how long we'll keep trying for the app to roll out
// before giving up. Same proxy-timeout mechanics as StagingComplete.
const appRunningMaxWait = 10 * time.Minute

// AppRunning waits for an app to report ready. Retries across proxy timeouts,
// same reasoning as StagingComplete — Epinio's `/running` endpoint blocks
// until pods are ready, which can outlive an ingress `proxy_read_timeout`.
func (c *Client) AppRunning(namespace, name string) error {
	path := "/namespaces/" + namespace + "/applications/" + name + "/running"
	deadline := time.Now().Add(appRunningMaxWait)
	var lastErr error
	for {
		err := c.do("GET", path, nil, nil)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isProxyTimeout(err) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("app did not reach running state within %s (last error: %w)", appRunningMaxWait, lastErr)
		}
		time.Sleep(2 * time.Second)
	}
}

// ListEnv lists environment variables for an app.
func (c *Client) ListEnv(namespace, app string) ([]EnvVariable, error) {
	var resp []EnvVariable
	if err := c.do("GET", "/namespaces/"+namespace+"/applications/"+app+"/environment", nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// SetEnv sets environment variables on an app.
func (c *Client) SetEnv(namespace, app string, env map[string]string) error {
	return c.do("POST", "/namespaces/"+namespace+"/applications/"+app+"/environment", env, nil)
}

// UnsetEnv deletes an environment variable from an app.
func (c *Client) UnsetEnv(namespace, app, envName string) error {
	return c.do("DELETE", "/namespaces/"+namespace+"/applications/"+app+"/environment/"+envName, nil, nil)
}

// ListConfigurations lists configurations in a namespace.
func (c *Client) ListConfigurations(namespace string) ([]ConfigurationResponse, error) {
	var resp []ConfigurationResponse
	if err := c.do("GET", "/namespaces/"+namespace+"/configurations", nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// CreateConfiguration creates a new configuration.
func (c *Client) CreateConfiguration(namespace string, req ConfigurationCreateRequest) error {
	return c.do("POST", "/namespaces/"+namespace+"/configurations", req, nil)
}

// DeleteConfiguration deletes a configuration.
func (c *Client) DeleteConfiguration(namespace, name string) error {
	return c.do("DELETE", "/namespaces/"+namespace+"/configurations/"+name, nil, nil)
}

// BindConfiguration binds configurations to an app.
func (c *Client) BindConfiguration(namespace, app string, configurations []string) error {
	body := map[string][]string{"names": configurations}
	return c.do("POST", "/namespaces/"+namespace+"/applications/"+app+"/configurationbindings", body, nil)
}

// UnbindConfiguration unbinds a configuration from an app.
func (c *Client) UnbindConfiguration(namespace, app, configuration string) error {
	return c.do("DELETE", "/namespaces/"+namespace+"/applications/"+app+"/configurationbindings/"+configuration, nil, nil)
}

// ListServices lists services in a namespace.
func (c *Client) ListServices(namespace string) ([]Service, error) {
	var resp []Service
	if err := c.do("GET", "/namespaces/"+namespace+"/services", nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// ListCatalogServices lists available catalog services.
func (c *Client) ListCatalogServices() ([]CatalogService, error) {
	var resp []CatalogService
	if err := c.do("GET", "/catalogservices", nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// ListAppCharts lists the AppCharts registered on the Epinio cluster. These
// are the valid values for `configuration.appchart` in an app's epinio.yml
// (e.g. "standard", "standard-elevated").
func (c *Client) ListAppCharts() ([]AppChart, error) {
	var resp []AppChart
	if err := c.do("GET", "/appcharts", nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// ShowAppChart fetches a single AppChart by name, including its full
// description and settings schema.
func (c *Client) ShowAppChart(name string) (*AppChart, error) {
	var resp AppChart
	if err := c.do("GET", "/appcharts/"+name, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ShowCatalogService fetches a single catalog service by name.
func (c *Client) ShowCatalogService(name string) (*CatalogService, error) {
	var resp CatalogService
	if err := c.do("GET", "/catalogservices/"+name, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CreateService creates a new service instance.
func (c *Client) CreateService(namespace string, req ServiceCreateRequest) error {
	return c.do("POST", "/namespaces/"+namespace+"/services", req, nil)
}

// DeleteService deletes a service.
func (c *Client) DeleteService(namespace, name string) error {
	return c.do("DELETE", "/namespaces/"+namespace+"/services/"+name, nil, nil)
}

// BindService binds a service to an app.
func (c *Client) BindService(namespace, service string, req ServiceBindRequest) error {
	return c.do("POST", "/namespaces/"+namespace+"/services/"+service+"/bind", req, nil)
}

// UnbindService unbinds a service from an app.
func (c *Client) UnbindService(namespace, service string, req ServiceUnbindRequest) error {
	return c.do("POST", "/namespaces/"+namespace+"/services/"+service+"/unbind", req, nil)
}

// wsURL converts the base HTTP URL to a WebSocket URL.
func (c *Client) wsURL(path string) string {
	base := c.BaseURL
	base = strings.Replace(base, "https://", "wss://", 1)
	base = strings.Replace(base, "http://", "ws://", 1)
	return base + "/wapi/v1" + path
}

// containerLogLine mirrors the JSON Epinio writes over the log WebSocket.
// Matches `helpers/kubernetes/tailer.ContainerLogLine` in the Epinio repo.
type containerLogLine struct {
	Message       string `json:"Message"`
	ContainerName string `json:"ContainerName"`
	PodName       string `json:"PodName"`
	Namespace     string `json:"Namespace"`
}

// AppLogs fetches application logs via WebSocket. It connects, reads all
// messages arriving before a short read deadline, and returns them as a
// slice of prefixed strings (`[<pod>] <message>`).
//
// When stageID is empty it tails runtime pod logs. When set, it tails the
// staging build's logs — note Epinio uses a different route for staging,
// keyed by (namespace, stage_id) with no app name; BuildLogStreamURL handles
// the path switch. Auth is via Epinio's short-lived ?authtoken= query
// parameter — the OIDC bearer header is not accepted on the WS upgrade.
func (c *Client) AppLogs(namespace, app string, stageID string, follow bool) ([]string, error) {
	wsEndpoint, err := c.BuildLogStreamURL(namespace, app, stageID, follow)
	if err != nil {
		return nil, err
	}

	dialer := websocket.Dialer{
		TLSClientConfig: tlsConfig(),
	}

	conn, wsResp, dialErr := dialer.Dial(wsEndpoint, nil)
	if dialErr != nil {
		statusInfo := ""
		if wsResp != nil {
			body, _ := io.ReadAll(wsResp.Body)
			wsResp.Body.Close()
			statusInfo = fmt.Sprintf(" (HTTP %d: %s)", wsResp.StatusCode, string(body))
		}
		return nil, fmt.Errorf("websocket connect: %w%s", dialErr, statusInfo)
	}
	defer conn.Close()

	timeout := 5 * time.Second
	if !follow {
		timeout = 3 * time.Second
	}
	conn.SetReadDeadline(time.Now().Add(timeout))

	var lines []string
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			// Normal close or deadline exceeded — we're done for this call.
			break
		}
		var entry containerLogLine
		if jerr := json.Unmarshal(message, &entry); jerr == nil && entry.Message != "" {
			pod := entry.PodName
			if entry.ContainerName != "" && entry.ContainerName != entry.PodName {
				pod = pod + "/" + entry.ContainerName
			}
			line := strings.TrimSpace(entry.Message)
			if pod != "" {
				line = "[" + pod + "] " + line
			}
			if line != "" {
				lines = append(lines, line)
			}
			continue
		}
		// Not a JSON log line? Fall back to raw text so we don't lose signal.
		if raw := strings.TrimSpace(string(message)); raw != "" {
			lines = append(lines, raw)
		}
	}
	return lines, nil
}

// authTokenResponse mirrors Epinio's models.AuthTokenResponse.
type authTokenResponse struct {
	Token string `json:"token"`
}

// AuthToken requests a short-lived (30s TTL) token from Epinio, suitable for
// use as a ?authtoken=<X> query parameter when opening WebSocket streams.
// Epinio's WebSocket endpoints (/wapi/v1/...) reject the OIDC bearer header
// directly — they accept only this query-param token. The trade-off is the
// caller must dial the WS promptly after calling AuthToken.
func (c *Client) AuthToken() (string, error) {
	var resp authTokenResponse
	if err := c.do("GET", "/authtoken", nil, &resp); err != nil {
		return "", err
	}
	return resp.Token, nil
}

// BuildLogStreamURL returns the WebSocket URL for tailing either runtime pod
// logs (when stageID == "") or a staging build (when stageID != ""). The URL
// carries the short-lived authtoken in the query string; the caller should
// dial it within ~30 seconds. See AuthToken for the TTL note.
func (c *Client) BuildLogStreamURL(namespace, app, stageID string, follow bool) (string, error) {
	token, err := c.AuthToken()
	if err != nil {
		return "", fmt.Errorf("fetch ws authtoken: %w", err)
	}
	q := url.Values{}
	q.Set("authtoken", token)
	q.Set("follow", fmt.Sprintf("%t", follow))

	var path string
	if stageID != "" {
		path = "/namespaces/" + namespace + "/staging/" + stageID + "/logs"
	} else {
		path = "/namespaces/" + namespace + "/applications/" + app + "/logs"
	}
	return c.wsURL(path) + "?" + q.Encode(), nil
}

// ProbeLogStream reports whether the WebSocket log stream is reachable from
// this client. It performs a short, non-following handshake and closes
// immediately. Returns mode "full" / "degraded" / "unavailable" plus a
// human-readable detail. Uses the short-lived ?authtoken= flow — never sends
// the OIDC bearer header (Epinio's WS handler rejects bearer tokens as
// "malformed token format").
func (c *Client) ProbeLogStream(namespace, app string) (mode, message string) {
	wsEndpoint, err := c.BuildLogStreamURL(namespace, app, "", false)
	if err != nil {
		return "unavailable", fmt.Sprintf("authtoken fetch failed: %v", err)
	}

	dialer := websocket.Dialer{
		TLSClientConfig:  tlsConfig(),
		HandshakeTimeout: 3 * time.Second,
	}

	conn, wsResp, dialErr := dialer.Dial(wsEndpoint, nil)
	if dialErr == nil {
		conn.Close()
		return "full", "websocket upgrade succeeded"
	}
	if wsResp != nil && wsResp.StatusCode != 0 {
		// Server answered the upgrade — Upgrade headers survive the path.
		return "full", fmt.Sprintf("websocket reachable (server responded %d on probe app)", wsResp.StatusCode)
	}
	return "unavailable", fmt.Sprintf("websocket unreachable: %v", dialErr)
}

// PushApp orchestrates the full push workflow: create (if needed) → upload → stage → wait → deploy → wait.
func (c *Client) PushApp(namespace, name string, source io.Reader, builderImage string) (*PushResult, error) {
	// Step 1: Ensure the app exists
	_, err := c.ShowApp(namespace, name)
	if err != nil {
		// App doesn't exist, create it
		if createErr := c.CreateApp(namespace, AppCreateRequest{Name: name}); createErr != nil {
			return nil, fmt.Errorf("create app: %w", createErr)
		}
	}

	// Step 2: Upload source
	upload, err := c.UploadApp(namespace, name, source)
	if err != nil {
		return nil, fmt.Errorf("upload: %w", err)
	}

	// Step 3: Determine builder image
	if builderImage == "" {
		info, err := c.Info()
		if err == nil && info.DefaultBuilderImage != "" {
			builderImage = info.DefaultBuilderImage
		} else {
			builderImage = "paketobuildpacks/builder-jammy-full:0.3.606"
		}
	}

	// Step 4: Stage
	stageResp, err := c.StageApp(namespace, name, StageRequest{
		App:          AppRef{Name: name, Namespace: namespace},
		BlobUID:      upload.BlobUID,
		BuilderImage: builderImage,
	})
	if err != nil {
		return nil, fmt.Errorf("stage: %w", err)
	}

	// Step 5: Wait for staging
	if err := c.StagingComplete(namespace, stageResp.Stage.ID); err != nil {
		return nil, fmt.Errorf("staging wait: %w", err)
	}

	// Step 6: Deploy
	deployResp, err := c.DeployApp(namespace, name, DeployRequest{
		App:      AppRef{Name: name, Namespace: namespace},
		Stage:    stageResp.Stage,
		ImageURL: stageResp.ImageURL,
		Origin:   ApplicationOrigin{Kind: 0, Path: "mcp-push"},
	})
	if err != nil {
		return nil, fmt.Errorf("deploy: %w", err)
	}

	// Step 7: Wait for running
	if err := c.AppRunning(namespace, name); err != nil {
		return nil, fmt.Errorf("app running wait: %w", err)
	}

	return &PushResult{
		Routes:  deployResp.Routes,
		StageID: stageResp.Stage.ID,
		Image:   stageResp.ImageURL,
	}, nil
}
