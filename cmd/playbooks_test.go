package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The default lane is the RENDERED one. A playbook whose destinations are still
// placeholders is a procedure its reader has to finish by hand, which defeats
// the point of fetching it from the workspace that knows the answer.
func TestPlaybookPathDefaultsToRendered(t *testing.T) {
	if got := playbookPath("migrate-markdown-export", false, false); got != "/playbooks/migrate-markdown-export/rendered" {
		t.Fatalf("default lane = %q, want the rendered one", got)
	}
	if got := playbookPath("x", true, false); got != "/playbooks/x" {
		t.Fatalf("--generic lane = %q", got)
	}
	if got := playbookPath("x", false, true); !strings.HasSuffix(got, "?format=raw") {
		t.Fatalf("raw form = %q, want ?format=raw", got)
	}
	if got := playbookPath("x", true, true); got != "/playbooks/x?format=raw" {
		t.Fatalf("generic+raw = %q", got)
	}
}

// install writes where an agent already reads. The candidates are checked in
// order, and the fallback is the working directory rather than an error: a
// command that refuses to run because a conventional folder is missing is a
// command people stop using.
func TestResolveInstallDirPrefersAgentDirs(t *testing.T) {
	dir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(prev)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	if got := resolveInstallDir(); got != "." {
		t.Fatalf("with no agent dirs, install dir = %q, want \".\"", got)
	}

	if err := os.MkdirAll(filepath.Join(".agents", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := resolveInstallDir(); got != filepath.Join(".agents", "workflows") {
		t.Fatalf("install dir = %q, want the agents workflows dir", got)
	}

	// .claude/commands wins when both exist — it is the one a running agent
	// picks up without being told.
	if err := os.MkdirAll(filepath.Join(".claude", "commands"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := resolveInstallDir(); got != filepath.Join(".claude", "commands") {
		t.Fatalf("install dir = %q, want .claude/commands to win", got)
	}
}

func TestFailFromResponsePrefersTheApiMessage(t *testing.T) {
	if err := failFromResponse([]byte(`{"detail":"playbook not found"}`)); err == nil ||
		err.Error() != "playbook not found" {
		t.Fatalf("detail not surfaced: %v", err)
	}
	if err := failFromResponse([]byte(`{"error":"forbidden"}`)); err == nil ||
		err.Error() != "forbidden" {
		t.Fatalf("error field not surfaced: %v", err)
	}
	// Not JSON at all: the body still has to reach the user rather than being
	// swallowed into a generic failure.
	if err := failFromResponse([]byte("bad gateway")); err == nil ||
		err.Error() != "bad gateway" {
		t.Fatalf("raw body not surfaced: %v", err)
	}
}

// The manifest is what an agent reads to discover the CLI. A command that is
// registered but absent from it does not exist as far as an agent is concerned
// — that has happened before, to `blenau ingest`.
func TestPlaybooksIsInTheAgentManifest(t *testing.T) {
	names := manifestNames(t)
	for _, want := range []string{"playbooks", "playbooks list", "playbooks get", "playbooks install"} {
		if _, ok := names[want]; !ok {
			t.Errorf("%q missing from the agent manifest", want)
		}
	}
	if flags := names["playbooks install"]; !flags["dir"] || !flags["force"] || !flags["generic"] {
		t.Errorf("install flags missing from the manifest: %v", flags)
	}
}
