package tools

import (
	"archive/tar"
	"fmt"
	"io"
	"path"
	"strings"
	"testing"
)

func TestBuildTar(t *testing.T) {
	reader, err := buildTar(map[string]string{
		"main.go":     "package main",
		"pkg/util.go": "package pkg",
	})
	if err != nil {
		t.Fatalf("buildTar() error = %v", err)
	}

	tr := tar.NewReader(reader)
	var names []string
	for {
		hdr, readErr := tr.Next()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			t.Fatalf("tar.Next() error = %v", readErr)
		}
		if hdr.Typeflag == tar.TypeReg {
			names = append(names, hdr.Name)
		}
	}

	want := []string{"main.go", "pkg/util.go"}
	for _, name := range want {
		found := false
		for _, got := range names {
			if got == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("tar missing %q, got %v", name, names)
		}
	}
}

func TestBuildTarStripsQuotes(t *testing.T) {
	reader, err := buildTar(map[string]string{`"main.go"`: "package main"})
	if err != nil {
		t.Fatalf("buildTar() error = %v", err)
	}

	tr := tar.NewReader(reader)
	hdr, err := tr.Next()
	if err != nil {
		t.Fatalf("tar.Next() error = %v", err)
	}
	if hdr.Name != "main.go" {
		t.Errorf("entry name = %q, want main.go", hdr.Name)
	}
	data, err := io.ReadAll(tr)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(data) != "package main" {
		t.Errorf("content = %q, want package main", string(data))
	}
}

func TestSyncAppBinaryNameDefault(t *testing.T) {
	processed := map[string]string{"bin/my-app": "binary-bytes"}
	binaryName := ""
	syncFiles := processed

	for filePath := range processed {
		if binaryName == "" {
			binaryName = path.Base(strings.Trim(filePath, "/"))
		}
		syncFiles = map[string]string{binaryName: processed[filePath]}
		break
	}

	if binaryName != "my-app" {
		t.Errorf("binaryName = %q, want my-app", binaryName)
	}
	if syncFiles["my-app"] != "binary-bytes" {
		t.Errorf("syncFiles = %v, want my-app key", syncFiles)
	}
}

func TestWrapSyncError(t *testing.T) {
	t.Run("no ready pod hints startup", func(t *testing.T) {
		err := wrapSyncError(fmt.Errorf("upload error 422: no ready pod found"))
		if err == nil {
			t.Fatal("wrapSyncError() = nil, want error")
		}
		msg := err.Error()
		for _, want := range []string{
			"watch_app_startup",
			"sync_app",
			"no ready pod",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("error = %q, want substring %q", msg, want)
			}
		}
	})

	t.Run("503 hints startup", func(t *testing.T) {
		err := wrapSyncError(fmt.Errorf("upload error 503: service unavailable"))
		if err == nil {
			t.Fatal("wrapSyncError() = nil, want error")
		}
		if !strings.Contains(err.Error(), "watch_app_startup") {
			t.Errorf("error = %q, want watch_app_startup hint", err.Error())
		}
	})

	t.Run("other errors pass through", func(t *testing.T) {
		inner := fmt.Errorf("permission denied")
		err := wrapSyncError(inner)
		if !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("error = %q, want original message preserved", err.Error())
		}
		if strings.Contains(err.Error(), "watch_app_startup") {
			t.Errorf("error = %q, must not add startup hint for unrelated errors", err.Error())
		}
	})
}
