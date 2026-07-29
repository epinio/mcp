package client

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewTrimsTrailingSlash(t *testing.T) {
	c := New("https://epinio.example.com/", "admin", "pw")
	if c.BaseURL != "https://epinio.example.com" {
		t.Errorf("BaseURL = %q, want trailing slash trimmed", c.BaseURL)
	}
}

func TestURL(t *testing.T) {
	c := New("https://epinio.example.com", "admin", "pw")
	got := c.url("/info")
	want := "https://epinio.example.com/api/v1/info"
	if got != want {
		t.Errorf("url() = %q, want %q", got, want)
	}
}

func TestSetAuth(t *testing.T) {
	t.Run("token takes precedence over basic", func(t *testing.T) {
		c := NewWithToken("https://x", "tok123")
		req, _ := http.NewRequest(http.MethodGet, "https://x", nil)
		c.setAuth(req)
		if got := req.Header.Get("Authorization"); got != "Bearer tok123" {
			t.Errorf("Authorization = %q, want Bearer tok123", got)
		}
	})

	t.Run("basic auth when no token", func(t *testing.T) {
		c := New("https://x", "alice", "secret")
		req, _ := http.NewRequest(http.MethodGet, "https://x", nil)
		c.setAuth(req)
		user, pass, ok := req.BasicAuth()
		if !ok || user != "alice" || pass != "secret" {
			t.Errorf("BasicAuth = (%q,%q,%v), want alice/secret/true", user, pass, ok)
		}
	})
}

func TestDoSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/info" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"1.14.0","platform":"k3s"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "admin", "pw")
	info, err := c.Info()

	if err != nil {
		t.Fatalf("Info() error = %v", err)
	}
	if info.Version != "1.14.0" || info.Platform != "k3s" {
		t.Errorf("Info() = %+v, want version 1.14.0 platform k3s", info)
	}
}

func TestDoAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"namespace not found"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "admin", "pw")
	_, err := c.Info()
	if err == nil {
		t.Fatal("Info() error = nil, want API error")
	}
	if !strings.Contains(err.Error(), "API error 404") {
		t.Errorf("error = %q, want it to mention API error 404", err.Error())
	}
	if !strings.Contains(err.Error(), "namespace not found") {
		t.Errorf("error = %q, want it to surface the Epinio error body", err.Error())
	}
}

func TestDoHTMLProxyError(t *testing.T) {
	// An ingress/proxy 502 returns an HTML page; do() must collapse it to a
	// concise message rather than dumping the page into the tool response.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<!DOCTYPE html><html><body>502 Bad Gateway</body></html>"))
	}))
	defer srv.Close()

	c := New(srv.URL, "admin", "pw")
	_, err := c.Info()
	if err == nil {
		t.Fatal("Info() error = nil, want proxy error")
	}
	if !strings.Contains(err.Error(), "HTML error page") {
		t.Errorf("error = %q, want collapsed HTML-page message", err.Error())
	}
	if strings.Contains(err.Error(), "<html>") {
		t.Errorf("error = %q, must not dump raw HTML", err.Error())
	}
}

func TestIsHTMLResponse(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		want        bool
	}{
		{"html content-type", "text/html; charset=utf-8", "anything", true},
		{"doctype sniff despite text/plain", "text/plain", "<!DOCTYPE html><html>", true},
		{"html tag sniff", "", "<html><body>err</body></html>", true},
		{"json is not html", "application/json", `{"error":"x"}`, false},
		{"plain text is not html", "text/plain", "boom", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{Header: http.Header{}}
			if tt.contentType != "" {
				resp.Header.Set("Content-Type", tt.contentType)
			}
			got := isHTMLResponse(resp, []byte(tt.body))
			if got != tt.want {
				t.Errorf("isHTMLResponse(%q, %q) = %v, want %v", tt.contentType, tt.body, got, tt.want)
			}
		})
	}
}

func TestIsProxyTimeout(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"504", errors.New("API error 504: gateway timeout"), true},
		{"502", errors.New("API error 502: bad gateway"), true},
		{"408", errors.New("API error 408: request timeout"), true},
		{"eof", errors.New("request GET /x: EOF"), true},
		{"reset", errors.New("read: connection reset by peer"), true},
		{"deadline", errors.New("context deadline exceeded"), true},
		{"404 is not retryable", errors.New("API error 404: not found"), false},
		{"500 is not retryable", errors.New("API error 500: boom"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isProxyTimeout(tt.err); got != tt.want {
				t.Errorf("isProxyTimeout(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
