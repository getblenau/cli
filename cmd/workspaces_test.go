package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWorkspacesRegistered asserts `workspaces` is on the root command.
func TestWorkspacesRegistered(t *testing.T) {
	root := NewRootCmd("test")
	found := false
	for _, c := range root.Commands() {
		if c.Name() == "workspaces" {
			found = true
		}
	}
	if !found {
		t.Fatalf("workspaces command not registered on root")
	}
}

// TestWorkspacesRendersList hits GET /workspaces on a mock server and renders
// the human table, marking the active workspace.
func TestWorkspacesRendersList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/workspaces" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"active":"id-a","default":null,"workspaces":[
			{"id":"id-a","slug":"acme","name":"Acme","role":"admin","can_write":true,"is_active":true},
			{"id":"id-b","slug":"globex","name":"Globex","role":"reader","can_write":false,"is_active":false}
		]}`))
	}))
	defer srv.Close()
	t.Setenv("BLENAU_API_URL", srv.URL)
	t.Setenv("BLENAU_API_TOKEN", "test")

	root := NewRootCmd("test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"workspaces"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Non-TTY (test) defaults to JSON output (agent-first). Assert the payload
	// round-trips both workspaces and the active id.
	s := out.String()
	if !strings.Contains(s, "acme") || !strings.Contains(s, "globex") {
		t.Fatalf("expected both workspaces in output, got:\n%s", s)
	}
	if !strings.Contains(s, `"active":"id-a"`) {
		t.Fatalf("expected active id in output, got:\n%s", s)
	}
}

// TestWorkspacesHumanTable exercises the human renderer via --json=false.
func TestWorkspacesHumanTable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"active":"id-a","workspaces":[{"id":"id-a","slug":"acme","name":"Acme","role":"admin","can_write":true,"is_active":true}]}`))
	}))
	defer srv.Close()
	t.Setenv("BLENAU_API_URL", srv.URL)
	t.Setenv("BLENAU_API_TOKEN", "test")

	root := NewRootCmd("test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"workspaces", "--json=false"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "SLUG") || !strings.Contains(s, "acme") || !strings.Contains(s, "*") {
		t.Fatalf("expected human table with active marker, got:\n%s", s)
	}
}
