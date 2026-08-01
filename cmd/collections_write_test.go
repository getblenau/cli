package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// collectionsServer routes the endpoints the write commands hit and captures
// the last body + path (same shape as docsDeleteServer).
func collectionsServer(t *testing.T, captured *map[string]interface{}, lastPath *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*lastPath = r.Method + " " + r.URL.Path
		if r.Body != nil {
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			*captured = body
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"written":1,"failed":0,"results":[],"warnings":[]}`))
	}))
}

func runCollections(t *testing.T, srv *httptest.Server, args ...string) (string, error) {
	t.Helper()
	t.Setenv("BLENAU_API_URL", srv.URL)
	t.Setenv("BLENAU_API_TOKEN", "blenau_tk_test")
	var buf bytes.Buffer
	root := NewRootCmd("test")
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{"collections"}, args...))
	err := root.Execute()
	return buf.String(), err
}

func writeRecordsFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "records.json")
	if err := os.WriteFile(p, []byte(`[{"id":"s-1","student_id":"ana"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCollectionsUpsertSendsRecordsAndDryRun(t *testing.T) {
	var captured map[string]interface{}
	var path string
	srv := collectionsServer(t, &captured, &path)
	defer srv.Close()

	if _, err := runCollections(t, srv, "upsert", "ledger", "--file", writeRecordsFile(t), "--dry-run"); err != nil {
		t.Fatalf("upsert failed: %v", err)
	}
	if path != "POST /collections/ledger/records" {
		t.Fatalf("wrong endpoint: %s", path)
	}
	if captured["dry_run"] != true {
		t.Fatalf("dry_run flag did not travel: %v", captured)
	}
	recs, ok := captured["records"].([]interface{})
	if !ok || len(recs) != 1 {
		t.Fatalf("records did not travel: %v", captured)
	}
}

func TestCollectionsUpsertRequiresFile(t *testing.T) {
	var captured map[string]interface{}
	var path string
	srv := collectionsServer(t, &captured, &path)
	defer srv.Close()

	if _, err := runCollections(t, srv, "upsert", "ledger"); err == nil {
		t.Fatal("expected an error without --file")
	}
	if path != "" {
		t.Fatalf("must not reach the API without --file, hit %s", path)
	}
}

func TestCollectionsShareSendsGrantBody(t *testing.T) {
	var captured map[string]interface{}
	var path string
	srv := collectionsServer(t, &captured, &path)
	defer srv.Close()

	_, err := runCollections(t, srv, "share", "ledger",
		"--group", "11111111-2222-3333-4444-555555555555",
		"--permission", "write", "--filter", `{"student_id":"s-001"}`)
	if err != nil {
		t.Fatalf("share failed: %v", err)
	}
	if path != "PUT /collections/ledger/grants/11111111-2222-3333-4444-555555555555" {
		t.Fatalf("wrong endpoint: %s", path)
	}
	if captured["permission"] != "write" {
		t.Fatalf("permission did not travel: %v", captured)
	}
	flt, ok := captured["record_filter"].(map[string]interface{})
	if !ok || flt["student_id"] != "s-001" {
		t.Fatalf("record_filter did not travel: %v", captured)
	}
}

func TestCollectionsShareRejectsBadFilterJSON(t *testing.T) {
	var captured map[string]interface{}
	var path string
	srv := collectionsServer(t, &captured, &path)
	defer srv.Close()

	if _, err := runCollections(t, srv, "share", "ledger",
		"--group", "g", "--filter", "{not-json"); err == nil {
		t.Fatal("expected an error for invalid --filter JSON")
	}
	if path != "" {
		t.Fatalf("must not reach the API with bad filter, hit %s", path)
	}
}

func TestCollectionsUnshareHitsDelete(t *testing.T) {
	var captured map[string]interface{}
	var path string
	srv := collectionsServer(t, &captured, &path)
	defer srv.Close()

	if _, err := runCollections(t, srv, "unshare", "ledger", "--group", "gid"); err != nil {
		t.Fatalf("unshare failed: %v", err)
	}
	if path != "DELETE /collections/ledger/grants/gid" {
		t.Fatalf("wrong endpoint: %s", path)
	}
}

func TestCollectionsUpdateContractFlags(t *testing.T) {
	var captured map[string]interface{}
	var path string
	srv := collectionsServer(t, &captured, &path)
	defer srv.Close()

	_, err := runCollections(t, srv, "update", "ledger",
		"--required-fields", "student_id,session_id",
		"--unknown-policy", "reject", "--append-only",
		"--guidance", "One record per session.")
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if captured["unknown_policy"] != "reject" || captured["append_only"] != true {
		t.Fatalf("contract knobs did not travel: %v", captured)
	}
	req, _ := captured["required_fields"].([]interface{})
	if len(req) != 2 {
		t.Fatalf("required_fields did not travel: %v", captured)
	}
	if captured["guidance"] != "One record per session." {
		t.Fatalf("guidance did not travel: %v", captured)
	}
}

func TestClassifyWriteCoversCollections(t *testing.T) {
	writes := [][2]string{
		{"POST", "/collections/ledger/records"},
		{"POST", "/collections/ledger/import"},
		{"POST", "/collections/ledger/reconcile"},
		{"PUT", "/collections/ledger/grants/gid"},
		{"DELETE", "/collections/ledger/grants/gid"},
		{"DELETE", "/collections/ledger"},
	}
	for _, w := range writes {
		if !classifyWrite(w[0], w[1]) {
			t.Errorf("%s %s must classify as a write", w[0], w[1])
		}
	}
	reads := [][2]string{
		{"GET", "/collections"},
		{"POST", "/collections/ledger/query"},
		{"GET", "/collections/ledger/describe"},
		{"GET", "/collections/ledger/grants"},
	}
	for _, r := range reads {
		if classifyWrite(r[0], r[1]) {
			t.Errorf("%s %s must NOT classify as a write", r[0], r[1])
		}
	}
}
