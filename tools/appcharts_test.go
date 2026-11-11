package tools

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/epinio/mcp/client"
)

func TestDecodeChartArchive(t *testing.T) {
	raw := []byte("fake-chart-bytes")
	encoded := base64.StdEncoding.EncodeToString(raw)

	t.Run("plain base64", func(t *testing.T) {
		got, err := decodeChartArchive(encoded)
		if err != nil {
			t.Fatalf("decodeChartArchive() error = %v", err)
		}
		if string(got) != string(raw) {
			t.Errorf("decodeChartArchive() = %q, want %q", got, raw)
		}
	})

	t.Run("base64 prefix", func(t *testing.T) {
		got, err := decodeChartArchive("base64:" + encoded)
		if err != nil {
			t.Fatalf("decodeChartArchive() error = %v", err)
		}
		if string(got) != string(raw) {
			t.Errorf("decodeChartArchive() = %q, want %q", got, raw)
		}
	})

	t.Run("empty", func(t *testing.T) {
		_, err := decodeChartArchive("  ")
		if err == nil || !strings.Contains(err.Error(), "archive is required") {
			t.Fatalf("decodeChartArchive() error = %v, want archive is required", err)
		}
	})

	t.Run("invalid base64", func(t *testing.T) {
		_, err := decodeChartArchive("not-base64!!")
		if err == nil || !strings.Contains(err.Error(), "base64-encoded") {
			t.Fatalf("decodeChartArchive() error = %v, want base64-encoded", err)
		}
	})

	t.Run("too large", func(t *testing.T) {
		big := make([]byte, maxChartArchiveBytes+1)
		encoded := base64.StdEncoding.EncodeToString(big)
		_, err := decodeChartArchive(encoded)
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("decodeChartArchive() error = %v, want exceeds limit", err)
		}
	})
}

func TestPushAppChartTool(t *testing.T) {
	raw := []byte("fake-chart-tgz")
	archive := base64.StdEncoding.EncodeToString(raw)

	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/api/v1/appcharts/push" {
				http.NotFound(w, r)
				return
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("ParseMultipartForm: %v", err)
			}
			if got := r.FormValue("name"); got != "mychart" {
				t.Errorf("name = %q, want mychart", got)
			}
			if got := r.FormValue("short_description"); got != "short" {
				t.Errorf("short_description = %q, want short", got)
			}
			file, _, err := r.FormFile("file")
			if err != nil {
				t.Fatalf("FormFile: %v", err)
			}
			defer file.Close()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(client.AppChartPushResponse{
				Name:      "mychart",
				HelmChart: "epinio_appchart_mychart:0.1.0",
				HelmRepo:  "oci://registry.example.com/apps",
			})
		}))
		defer srv.Close()

		c := client.New(srv.URL, "admin", "pw")
		out, err := pushAppChart(c, PushAppChartInput{
			Name:             "mychart",
			Archive:          archive,
			ShortDescription: "short",
		})
		if err != nil {
			t.Fatalf("pushAppChart() error = %v", err)
		}
		if out.Status != "pushed" ||
			out.Name != "mychart" ||
			out.HelmChart != "epinio_appchart_mychart:0.1.0" ||
			out.HelmRepo != "oci://registry.example.com/apps" {
			t.Errorf("pushAppChart() = %+v", out)
		}
	})

	t.Run("propagates 403", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"errors":[{"status":403,"title":"Forbidden","details":"chart_write required"}]}`, http.StatusForbidden)
		}))
		defer srv.Close()

		c := client.New(srv.URL, "viewer", "pw")
		_, err := pushAppChart(c, PushAppChartInput{
			Name:    "mychart",
			Archive: archive,
		})
		if err == nil {
			t.Fatal("pushAppChart() error = nil, want 403")
		}
		if !strings.Contains(err.Error(), "403") {
			t.Fatalf("pushAppChart() error = %v, want 403", err)
		}
	})

	t.Run("requires name", func(t *testing.T) {
		c := client.New("https://example.com", "admin", "pw")
		_, err := pushAppChart(c, PushAppChartInput{Archive: archive})
		if err == nil || !strings.Contains(err.Error(), "name is required") {
			t.Fatalf("pushAppChart() error = %v, want name is required", err)
		}
	})
}
