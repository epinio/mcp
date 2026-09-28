package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/epinio/mcp/client"
)

// makeJWT builds an unsigned-but-well-formed JWT with the given payload claims.
// The signature segment is a fixed dummy — decodeJWTClaims never verifies it.
func makeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payloadJSON, err := json.Marshal(claims)

	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	sig := base64.RawURLEncoding.EncodeToString([]byte("signature"))
	return header + "." + payload + "." + sig
}

func TestSummarizeAuthHeader(t *testing.T) {
	jwt := makeJWT(t, map[string]any{
		"iss": "https://dex.example.com",
		"sub": "admin",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	basicCreds := base64.StdEncoding.EncodeToString([]byte("alice:hunter2"))

	tests := []struct {
		name       string
		header     string
		wantSubstr []string
		wantAbsent []string
	}{
		{"empty", "", []string{"none"}, nil},
		{"scheme only", "Bearer", []string{"Bearer (no value)"}, nil},
		{"basic shows user not password", "Basic " + basicCreds,
			[]string{"Basic", "user=alice"}, []string{"hunter2"}},
		{"basic undecodable", "Basic !!!notbase64",
			[]string{"Basic", "undecodable"}, nil},
		{"bearer jwt decodes claims", "Bearer " + jwt,
			[]string{"jwt", "iss=https://dex.example.com", "sub=admin", "exp_in="}, nil},
		{"bearer opaque", "Bearer abcdefghijklmnopqrstuvwxyz",
			[]string{"Bearer", "opaque", "len=26"}, nil},
		{"kubeconfig token", "Bearer kubeconfig-u-abc123:secret456",
			[]string{"rancher-kubeconfig"}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarizeAuthHeader(tt.header)
			for _, want := range tt.wantSubstr {
				if !strings.Contains(got, want) {
					t.Errorf("summarizeAuthHeader(%q) = %q, want substring %q", tt.header, got, want)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("summarizeAuthHeader(%q) = %q, must NOT contain %q (secret leak)", tt.header, got, absent)
				}
			}
		})
	}
}

func TestLooksLikeJWT(t *testing.T) {
	tests := []struct {
		tok  string
		want bool
	}{
		{"a.b.c", true},
		{"header.payload.sig", true},
		{"a.b", false},     // only two segments
		{"a.b.c.d", false}, // four segments
		{"a..c", false},    // empty middle segment
		{"plain-token", false},
		{"", false},
	}
	for _, tt := range tests {
		got := looksLikeJWT(tt.tok)
		if got != tt.want {
			t.Errorf("looksLikeJWT(%q) = %v, want %v", tt.tok, got, tt.want)
		}
	}
}

func TestDecodeJWTClaims(t *testing.T) {
	jwt := makeJWT(t, map[string]any{
		"iss": "https://dex.example.com",
		"sub": "user-12345",
		"aud": "epinio-api",
	})
	got := decodeJWTClaims(jwt)
	for _, want := range []string{"iss=https://dex.example.com", "sub=user-12345", "aud=epinio-api"} {
		if !strings.Contains(got, want) {
			t.Errorf("decodeJWTClaims = %q, want substring %q", got, want)
		}
	}

	// A non-JWT / unparseable payload yields an empty string, never a panic.
	if got := decodeJWTClaims("not.a.jwt"); got != "" {
		t.Errorf("decodeJWTClaims(garbage) = %q, want empty", got)
	}
	if got := decodeJWTClaims("onlyonesegment"); got != "" {
		t.Errorf("decodeJWTClaims(no dots) = %q, want empty", got)
	}
}

func TestFingerprint(t *testing.T) {
	// Short tokens are fully masked so no usable bytes leak.
	short := fingerprint("secret")
	if strings.Contains(short, "secret") {
		t.Errorf("fingerprint(short) = %q leaked the value", short)
	}
	// Long tokens reveal only the first/last 6 chars, never the middle.
	long := fingerprint("abcdefGHIJKLMNOPqrstuv")
	if !strings.HasPrefix(long, "abcdef") || !strings.HasSuffix(long, "qrstuv") {
		t.Errorf("fingerprint(long) = %q, want abcdef…qrstuv shape", long)
	}
	if strings.Contains(long, "GHIJKLMNOP") {
		t.Errorf("fingerprint(long) = %q leaked the middle of the token", long)
	}
}

func TestPerRequestClient(t *testing.T) {
	const apiURL = "https://epinio.example.com"
	basic := base64.StdEncoding.EncodeToString([]byte("bob:pw"))
	noColon := base64.StdEncoding.EncodeToString([]byte("nopassworddelimiter"))
	emptyUser := base64.StdEncoding.EncodeToString([]byte(":pw"))

	tests := []struct {
		name      string
		header    string
		wantNil   bool
		checkFunc func(t *testing.T, c *client.Client)
	}{
		{"bearer", "Bearer tok123", false, func(t *testing.T, c *client.Client) {
			if c.Token != "tok123" {
				t.Errorf("Token = %q, want tok123", c.Token)
			}
		}},
		{"empty bearer", "Bearer ", true, nil},
		{"basic", "Basic " + basic, false, func(t *testing.T, c *client.Client) {
			if c.Username != "bob" || c.Password != "pw" {
				t.Errorf("got user=%q pass set=%v, want bob/pw", c.Username, c.Password != "")
			}
		}},
		{"basic bad base64", "Basic !!!", true, nil},
		{"basic no colon", "Basic " + noColon, true, nil},
		{"basic empty user", "Basic " + emptyUser, true, nil},
		{"unknown scheme", "Negotiate blob", true, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := perRequestClient(apiURL, tt.header)
			if tt.wantNil {
				if c != nil {
					t.Errorf("perRequestClient(%q) = non-nil, want nil", tt.header)
				}
				return
			}
			if c == nil {
				t.Fatalf("perRequestClient(%q) = nil, want client", tt.header)
			}
			if tt.checkFunc != nil {
				tt.checkFunc(t, c)
			}
		})
	}
}

func TestHealthzHandler(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	healthzHandler("v1.2.3")(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ok" || body["version"] != "v1.2.3" {
		t.Errorf("healthz body = %v, want status=ok version=v1.2.3", body)
	}
}

// fakeEpinio returns an httptest server that serves GET /api/v1/info with the
// given status. When status is 200 it returns a minimal InfoResponse.
func fakeEpinio(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/info" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status == http.StatusOK {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"version":      "1.14.0",
				"kube_version": "v1.30.0",
				"platform":     "k3s",
			})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestReadyzHandler(t *testing.T) {
	t.Run("epinio reachable -> 200", func(t *testing.T) {
		srv := fakeEpinio(t, http.StatusOK)
		c := client.New(srv.URL, "admin", "password")

		rec := httptest.NewRecorder()
		readyzHandler("v1", c)(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

		if rec.Code != http.StatusOK {
			t.Fatalf("readyz status = %d, want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
			t.Errorf("readyz body = %s, want status ok", rec.Body.String())
		}
	})

	t.Run("epinio degraded -> 503", func(t *testing.T) {
		srv := fakeEpinio(t, http.StatusInternalServerError)
		c := client.New(srv.URL, "admin", "password")

		rec := httptest.NewRecorder()
		readyzHandler("v1", c)(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("readyz status = %d, want 503", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "degraded") {
			t.Errorf("readyz body = %s, want degraded", rec.Body.String())
		}
	})
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 40); got != "short" {
		t.Errorf("truncate(short) = %q, want unchanged", got)
	}
	got := truncate("abcdefghij", 4)
	if got != "abcd"+"…" {
		t.Errorf("truncate = %q, want abcd…", got)
	}
}

// fakeEpinioMe returns an httptest server standing in for Epinio's
// authenticated /me endpoint. It accepts exactly one credential, so a test can
// prove that anything else is refused.
func fakeEpinioMe(t *testing.T, wantAuth string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/me" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != wantAuth {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"user": "someone@example.com"})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The point of this suite: there is no anonymous path to the tools. Every case
// that is not a credential Epinio accepts must end in 401, never in a session.
func TestRequireEpinioAuth(t *testing.T) {
	const prmURL = "https://mcp.example.com/.well-known/oauth-protected-resource"
	const goodAuth = "Bearer good-token"

	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		if _, ok := callerFrom(r.Context()); !ok {
			t.Error("handler ran without a caller in context")
		}
		w.WriteHeader(http.StatusOK)
	})

	cases := []struct {
		name       string
		authHeader string
		wantStatus int
		wantReach  bool
		wantErr    string
	}{
		{
			name:       "no credential is refused, not served anonymously",
			authHeader: "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "unparseable scheme does not fall back to server creds",
			authHeader: "Token something",
			wantStatus: http.StatusUnauthorized,
			wantErr:    "invalid_request",
		},
		{
			name:       "credential Epinio rejects is refused",
			authHeader: "Bearer wrong-token",
			wantStatus: http.StatusUnauthorized,
			wantErr:    "invalid_token",
		},
		{
			name:       "credential Epinio accepts is let through",
			authHeader: goodAuth,
			wantStatus: http.StatusOK,
			wantReach:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached = false
			srv := fakeEpinioMe(t, goodAuth)

			req := httptest.NewRequest(http.MethodPost, "/", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rec := httptest.NewRecorder()

			requireEpinioAuth(srv.URL, prmURL, next).ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if reached != tc.wantReach {
				t.Errorf("handler reached = %v, want %v", reached, tc.wantReach)
			}
			if tc.wantStatus != http.StatusUnauthorized {
				return
			}
			// A 401 is only useful to a client if it says where to log in.
			challenge := rec.Header().Get("WWW-Authenticate")
			if !strings.Contains(challenge, prmURL) {
				t.Errorf("challenge %q does not point at the metadata", challenge)
			}
			if !strings.Contains(challenge, epinioAudienceScope) {
				t.Errorf("challenge %q does not request the epinio-api audience", challenge)
			}
			if tc.wantErr != "" && !strings.Contains(challenge, tc.wantErr) {
				t.Errorf("challenge %q missing error code %q", challenge, tc.wantErr)
			}
		})
	}
}

// Epinio being broken must not read as "your login is bad", or the client will
// loop through a login that was never the problem.
func TestRequireEpinioAuthUpstreamFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer whatever")
	rec := httptest.NewRecorder()

	requireEpinioAuth(srv.URL, "https://mcp.example.com/prm", http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			t.Error("handler must not run when Epinio cannot be reached")
		},
	)).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
}

func TestPRMHandler(t *testing.T) {
	rec := httptest.NewRecorder()
	prmHandler("https://mcp.example.com", "https://auth.example.com")(
		rec, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil),
	)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var doc struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
		ScopesSupported      []string `json:"scopes_supported"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Clients compare this against the URL the user typed and refuse a mismatch.
	if doc.Resource != "https://mcp.example.com" {
		t.Errorf("resource = %q", doc.Resource)
	}
	if len(doc.AuthorizationServers) != 1 ||
		doc.AuthorizationServers[0] != "https://auth.example.com" {
		t.Errorf("authorization_servers = %v", doc.AuthorizationServers)
	}
	found := false
	for _, s := range doc.ScopesSupported {
		if s == epinioAudienceScope {
			found = true
		}
	}
	if !found {
		t.Errorf("scopes_supported %v lacks the epinio-api audience scope", doc.ScopesSupported)
	}
}
