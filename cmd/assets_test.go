package cmd

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAssetsRegisteredInHelp asserts the `assets` command is registered and
// listed in the root --help output, with an `upload` subcommand.
func TestAssetsRegisteredInHelp(t *testing.T) {
	root := NewRootCmd("test")
	found := false
	for _, c := range root.Commands() {
		if c.Name() == "assets" {
			found = true
			// verify upload subcommand present
			hasUpload := false
			for _, sub := range c.Commands() {
				if sub.Name() == "upload" {
					hasUpload = true
				}
			}
			if !hasUpload {
				t.Fatalf("assets has no 'upload' subcommand")
			}
		}
	}
	if !found {
		t.Fatalf("assets command not registered on root")
	}

	// --help should list "assets".
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute --help: %v", err)
	}
	if !strings.Contains(out.String(), "assets") {
		t.Fatalf("--help does not list 'assets':\n%s", out.String())
	}
}

// TestAssetsUploadMissingDoc asserts that omitting --doc errors.
func TestAssetsUploadMissingDoc(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "img.png")
	if err := os.WriteFile(f, []byte("tiny"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRootCmd("test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"assets", "upload", f})
	err := root.Execute()
	if err == nil {
		t.Fatalf("expected error when --doc is missing")
	}
	if !strings.Contains(err.Error(), "doc") {
		t.Fatalf("expected error to mention 'doc', got: %v", err)
	}
}

// TestAssetsUploadOversizedNoCompress asserts that a file over the cap without
// --compress fails with the guidance error and does not upload.
func TestAssetsUploadOversizedNoCompress(t *testing.T) {
	tmp := t.TempDir()
	big := filepath.Join(tmp, "big.png")
	if err := os.WriteFile(big, bytes.Repeat([]byte("x"), maxAssetBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}

	// Server that would fail the test if hit.
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(200)
	}))
	defer srv.Close()
	t.Setenv("BLENAU_API_URL", srv.URL)
	t.Setenv("BLENAU_API_TOKEN", "test")

	root := NewRootCmd("test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"assets", "upload", big, "--doc", "docs/x.md"})
	err := root.Execute()
	if err == nil {
		t.Fatalf("expected error for oversized file")
	}
	if !strings.Contains(err.Error(), "Images must be 1 MB or smaller") {
		t.Fatalf("expected guidance in error, got: %v", err)
	}
	if hit {
		t.Fatalf("oversized file must NOT be uploaded")
	}
}

// TestAssetsUploadSuccess stands up a fake API and asserts the multipart
// upload reaches it with the expected fields and the JSON body is returned.
func TestAssetsUploadSuccess(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "shot.png")
	if err := os.WriteFile(f, []byte("pretend-png-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	var gotDoc, gotFilename, gotAlt, gotHeading, gotPosition, gotAuth, gotFileContent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/assets/upload-binary" {
			http.Error(w, "not found", 404)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		gotDoc = r.FormValue("doc_path")
		gotFilename = r.FormValue("filename")
		gotAlt = r.FormValue("alt_text")
		gotHeading = r.FormValue("insert_heading")
		gotPosition = r.FormValue("insert_position")
		file, _, ferr := r.FormFile("file")
		if ferr == nil {
			b, _ := io.ReadAll(file)
			gotFileContent = string(b)
			_ = file.Close()
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"asset_path":"assets/shot.png","commit_sha":"abc123","markdown":"![alt](assets/shot.png)","insert":{"embedded":true,"heading":"## Setup"}}`))
	}))
	defer srv.Close()
	t.Setenv("BLENAU_API_URL", srv.URL)
	t.Setenv("BLENAU_API_TOKEN", "secret-token")

	root := NewRootCmd("test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{
		"assets", "upload", f,
		"--doc", "docs/setup.md",
		"--alt", "a screenshot",
		"--insert-at", "## Setup",
		"--position", "after",
		"--json",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute upload: %v", err)
	}

	if gotAuth != "Bearer secret-token" {
		t.Fatalf("auth header = %q", gotAuth)
	}
	if gotDoc != "docs/setup.md" {
		t.Fatalf("doc_path = %q", gotDoc)
	}
	if gotFilename != "shot.png" {
		t.Fatalf("filename = %q", gotFilename)
	}
	if gotAlt != "a screenshot" {
		t.Fatalf("alt_text = %q", gotAlt)
	}
	if gotHeading != "## Setup" {
		t.Fatalf("insert_heading = %q", gotHeading)
	}
	if gotPosition != "after" {
		t.Fatalf("insert_position = %q", gotPosition)
	}
	if gotFileContent != "pretend-png-bytes" {
		t.Fatalf("file content = %q", gotFileContent)
	}
	if !strings.Contains(out.String(), "asset_path") {
		t.Fatalf("expected JSON body in output, got: %s", out.String())
	}
}
