package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// withPipedStdin points os.Stdin at a pipe (NOT a char device) for the duration
// of fn, so isTerminal(os.Stdin) is false — the deterministic "non-interactive
// shell" case, regardless of how `go test` was launched.
func withPipedStdin(t *testing.T, fn func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	orig := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = orig }()
	fn()
}

// docsDeleteServer stands up a fake API for `docs delete`: an exact by-path
// resolver and the delete-document endpoint. `deletePost` captures the last
// POST body so tests can assert what was actually sent.
func docsDeleteServer(t *testing.T, byPathStatus int, byPathBody string, deleteStatus int, deleteBody string, captured *map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/knowledge/documents/by-path/"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(byPathStatus)
			_, _ = w.Write([]byte(byPathBody))
		case r.Method == "POST" && r.URL.Path == "/knowledge/delete-document":
			if captured != nil {
				b, _ := io.ReadAll(r.Body)
				var m map[string]interface{}
				_ = json.Unmarshal(b, &m)
				*captured = m
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(deleteStatus)
			_, _ = w.Write([]byte(deleteBody))
		default:
			http.Error(w, "not found", 404)
		}
	}))
}

func runDocsDelete(t *testing.T, srv *httptest.Server, args ...string) (string, error) {
	t.Helper()
	t.Setenv("BLENAU_API_URL", srv.URL)
	t.Setenv("BLENAU_API_TOKEN", "blenau_tk_test")
	root := NewRootCmd("test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"docs", "delete"}, args...))
	err := root.Execute()
	return out.String(), err
}

func TestDocsDeleteRegisteredInHelp(t *testing.T) {
	root := NewRootCmd("test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"docs", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("docs --help: %v", err)
	}
	if !strings.Contains(out.String(), "delete") {
		t.Fatalf("docs --help does not list 'delete':\n%s", out.String())
	}
}

func TestDocsDeleteRejectsWildcard(t *testing.T) {
	srv := docsDeleteServer(t, 200, "{}", 200, "{}", nil)
	defer srv.Close()
	_, err := runDocsDelete(t, srv, "--path", "eng/*")
	if err == nil || !strings.Contains(err.Error(), "pattern") {
		t.Fatalf("expected wildcard rejection, got: %v", err)
	}
}

func TestDocsDeleteMissingDocIs404(t *testing.T) {
	srv := docsDeleteServer(t, 404, `{"detail":"Document not found"}`, 200, "{}", nil)
	defer srv.Close()
	_, err := runDocsDelete(t, srv, "--path", "eng/nope.md", "--yes")
	if err == nil || !strings.Contains(err.Error(), "no document at path") {
		t.Fatalf("expected 'no document at path' error, got: %v", err)
	}
}

func TestDocsDeleteNonTTYWithoutYesRefuses(t *testing.T) {
	var captured map[string]interface{}
	byPath := `{"path":"eng/x.md","title":"X","source_type":"github"}`
	srv := docsDeleteServer(t, 200, byPath, 200, `{"status":"deleted"}`, &captured)
	defer srv.Close()
	// With a piped (non-TTY) stdin, a delete without --yes must fail closed and
	// NEVER reach the delete endpoint.
	var err error
	withPipedStdin(t, func() {
		_, err = runDocsDelete(t, srv, "--path", "eng/x.md")
	})
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected non-TTY refusal mentioning --yes, got: %v", err)
	}
	if captured != nil {
		t.Fatalf("delete endpoint must NOT be hit without confirmation")
	}
}

func TestDocsDeleteYesSendsResolvedPathAndShowsRevert(t *testing.T) {
	var captured map[string]interface{}
	byPath := `{"path":"eng/legacy.md","title":"Legacy","source_type":"github"}`
	del := `{"status":"deleted","commit_sha":"abc123","db_only":false,"repo":"acme/x","revert_hint":"Recover from git history: ` + "`git revert abc123`" + ` in acme/x"}`
	srv := docsDeleteServer(t, 200, byPath, 200, del, &captured)
	defer srv.Close()

	out, err := runDocsDelete(t, srv, "--path", "eng/legacy.md", "--yes")
	if err != nil {
		t.Fatalf("delete --yes: %v", err)
	}
	if captured["path"] != "eng/legacy.md" {
		t.Fatalf("delete body path = %v (want resolved canonical path)", captured["path"])
	}
	if av, _ := captured["allow_db_only"].(bool); av {
		t.Fatalf("allow_db_only should default false")
	}
	// The resolved identity is always echoed to stderr.
	if !strings.Contains(out, "path:        eng/legacy.md") {
		t.Fatalf("missing resolved-identity echo:\n%s", out)
	}
	// The commit SHA + revert command reach the caller (human formatting when the
	// TTY is present; here — buffered, non-TTY — via the JSON result, which
	// carries both fields).
	if !strings.Contains(out, "abc123") || !strings.Contains(out, "git revert abc123") {
		t.Fatalf("missing commit sha / revert hint:\n%s", out)
	}
}

func TestDocsDeleteDryRunReportsPlanAndDoesNotConfirm(t *testing.T) {
	var captured map[string]interface{}
	byPath := `{"path":"eng/x.md","title":"X","source_type":"manual"}`
	plan := `{"status":"dry_run","file_on_default_branch":true,"would_delete_github":true,"db_only":false}`
	srv := docsDeleteServer(t, 200, byPath, 200, plan, &captured)
	defer srv.Close()

	out, err := runDocsDelete(t, srv, "--path", "eng/x.md", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if dr, _ := captured["dry_run"].(bool); !dr {
		t.Fatalf("dry_run flag not sent to server: %v", captured)
	}
	// The identity is echoed; the plan reaches the caller (human sentence on a
	// TTY, JSON here). Nothing was deleted — the server captured only the dry_run.
	if !strings.Contains(out, "path:        eng/x.md") {
		t.Fatalf("missing resolved-identity echo:\n%s", out)
	}
	if !strings.Contains(out, "would_delete_github") {
		t.Fatalf("dry-run plan not shown:\n%s", out)
	}
}

func TestDocsDeleteDryRunFormatsFailClosedError(t *testing.T) {
	// A dry-run that hits a fail-closed 409 (index-only, open PR) must render the
	// actionable message, not raw JSON — same formatting as the real delete.
	byPath := `{"path":"eng/pending.md","title":"P","source_type":"crystallize"}`
	del := `{"detail":{"error":"open_pr_would_recreate","message":"'eng/pending.md' is referenced by 1 open PR(s); merge or close them before deleting.","suggested_action":"merge or close the PR(s), then retry"}}`
	srv := docsDeleteServer(t, 200, byPath, 409, del, nil)
	defer srv.Close()

	_, err := runDocsDelete(t, srv, "--path", "eng/pending.md", "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "merge or close") {
		t.Fatalf("expected formatted fail-closed error on dry-run, got: %v", err)
	}
	if strings.Contains(err.Error(), `{"detail"`) {
		t.Fatalf("dry-run should not dump raw JSON, got: %v", err)
	}
}

func TestDocsDeleteOpenPRErrorIsFormatted(t *testing.T) {
	byPath := `{"path":"eng/pending.md","title":"P","source_type":"crystallize"}`
	del := `{"detail":{"error":"open_pr_would_recreate","message":"'eng/pending.md' has no file on the default branch but is referenced by 1 open PR(s); merge or close them before deleting.","suggested_action":"merge or close the PR(s), then retry"}}`
	srv := docsDeleteServer(t, 200, byPath, 409, del, nil)
	defer srv.Close()

	_, err := runDocsDelete(t, srv, "--path", "eng/pending.md", "--yes")
	if err == nil || !strings.Contains(err.Error(), "open PR") {
		t.Fatalf("expected formatted open-PR error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "merge or close") {
		t.Fatalf("expected suggested_action in error, got: %v", err)
	}
}

func TestDocsDeleteDBOnlyNeedsOverrideErrorIsFormatted(t *testing.T) {
	byPath := `{"path":"eng/orphan.md","title":"O","source_type":"manual"}`
	del := `{"detail":{"error":"db_only_delete_requires_confirmation","message":"'eng/orphan.md' has no backing file on the default branch (index-only). Deleting it is irrecoverable; re-run with allow_db_only to confirm.","irrecoverable":true,"suggested_action":"re-run with allow_db_only=true to confirm the irrecoverable delete"}}`
	srv := docsDeleteServer(t, 200, byPath, 409, del, nil)
	defer srv.Close()

	_, err := runDocsDelete(t, srv, "--path", "eng/orphan.md", "--yes")
	if err == nil || !strings.Contains(err.Error(), "irrecoverable") {
		t.Fatalf("expected irrecoverable override error, got: %v", err)
	}
}

// --json is a machine contract: the ONLY thing on either stream is the JSON
// document. It used to emit the resolved-identity echo to stderr anyway, so a
// caller that merged the streams got four lines of prose in front of its JSON
// and a parse error. The harness points both streams at one buffer, which is
// exactly that caller.
func TestDocsDeleteJSONEmitsNothingButJSON(t *testing.T) {
	byPath := `{"path":"eng/legacy.md","title":"Legacy","source_type":"github"}`
	del := `{"status":"deleted","commit_sha":"abc123","db_only":false,"repo":"acme/x"}`
	srv := docsDeleteServer(t, 200, byPath, 200, del, nil)
	defer srv.Close()

	out, err := runDocsDelete(t, srv, "--path", "eng/legacy.md", "--yes", "--json")
	if err != nil {
		t.Fatalf("delete --json: %v", err)
	}
	var parsed map[string]interface{}
	if e := json.Unmarshal([]byte(strings.TrimSpace(out)), &parsed); e != nil {
		t.Fatalf("combined output is not parseable JSON (%v):\n%s", e, out)
	}
	if parsed["status"] != "deleted" {
		t.Fatalf("wrong payload: %v", parsed)
	}
	if strings.Contains(out, "Document to delete") {
		t.Fatalf("human echo leaked into --json output:\n%s", out)
	}
}

// Same for the preview: --dry-run --json is how a caller asks "what would this
// do" programmatically.
func TestDocsDeleteDryRunJSONEmitsNothingButJSON(t *testing.T) {
	byPath := `{"path":"eng/x.md","title":"X","source_type":"manual"}`
	plan := `{"status":"dry_run","file_on_default_branch":true,"would_delete_github":true,"db_only":false}`
	srv := docsDeleteServer(t, 200, byPath, 200, plan, nil)
	defer srv.Close()

	out, err := runDocsDelete(t, srv, "--path", "eng/x.md", "--dry-run", "--json")
	if err != nil {
		t.Fatalf("dry-run --json: %v", err)
	}
	var parsed map[string]interface{}
	if e := json.Unmarshal([]byte(strings.TrimSpace(out)), &parsed); e != nil {
		t.Fatalf("combined output is not parseable JSON (%v):\n%s", e, out)
	}
	if parsed["status"] != "dry_run" {
		t.Fatalf("wrong payload: %v", parsed)
	}
}

// Without --json the echo stays, even though `go test` gives us a non-TTY
// stdout: the human piping stdout to a file is still reading stderr, and the
// point of the echo is that they see WHAT is about to be deleted.
func TestDocsDeleteEchoSurvivesInferredNonTTY(t *testing.T) {
	byPath := `{"path":"eng/legacy.md","title":"Legacy","source_type":"github"}`
	del := `{"status":"deleted","commit_sha":"abc123","db_only":false,"repo":"acme/x"}`
	srv := docsDeleteServer(t, 200, byPath, 200, del, nil)
	defer srv.Close()

	out, err := runDocsDelete(t, srv, "--path", "eng/legacy.md", "--yes")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !strings.Contains(out, "Document to delete") {
		t.Fatalf("echo suppressed without an explicit --json:\n%s", out)
	}
}
