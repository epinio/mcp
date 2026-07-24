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
