package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func notesServer(t *testing.T, captured *map[string]interface{}, lastPath *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*lastPath = r.Method + " " + r.URL.Path
		if r.Body != nil {
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			*captured = body
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"n-1","body":"x","list":"inbox","status":"open","visibility":"personal"}`))
	}))
}

func runNotes(t *testing.T, srv *httptest.Server, args ...string) error {
	t.Helper()
	t.Setenv("BLENAU_API_URL", srv.URL)
	t.Setenv("BLENAU_API_TOKEN", "blenau_tk_test")
	var buf bytes.Buffer
	root := NewRootCmd("test")
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{"notes"}, args...))
	return root.Execute()
}

func TestNotesRememberPrivateFlag(t *testing.T) {
	var captured map[string]interface{}
	var path string
	srv := notesServer(t, &captured, &path)
	defer srv.Close()

	if err := runNotes(t, srv, "remember", "gift idea", "--list", "personal-stuff", "--private"); err != nil {
		t.Fatalf("remember failed: %v", err)
	}
	if path != "POST /notes" {
		t.Fatalf("wrong endpoint: %s", path)
	}
	if captured["private"] != true || captured["list"] != "personal-stuff" {
		t.Fatalf("private/list did not travel: %v", captured)
	}

	// default: the flag stays OFF the wire (server default owns the semantics)
	if err := runNotes(t, srv, "remember", "team task"); err != nil {
		t.Fatalf("remember failed: %v", err)
	}
	if _, present := captured["private"]; present {
		t.Fatalf("private must be absent by default: %v", captured)
	}
}

func TestNotesShareListWireShape(t *testing.T) {
	var captured map[string]interface{}
	var path string
	srv := notesServer(t, &captured, &path)
	defer srv.Close()

	if err := runNotes(t, srv, "share-list", "video-ideas", "--group", "gid"); err != nil {
		t.Fatalf("share-list failed: %v", err)
	}
	if path != "PUT /notes/shares/gid" {
		t.Fatalf("wrong endpoint: %s", path)
	}
	if err := runNotes(t, srv, "unshare-list", "video-ideas", "--group", "gid"); err != nil {
		t.Fatalf("unshare-list failed: %v", err)
	}
	if path != "DELETE /notes/shares/gid" {
		t.Fatalf("wrong endpoint: %s", path)
	}
	if !classifyWrite("PUT", "/notes/shares/gid") || !classifyWrite("DELETE", "/notes/shares/gid") {
		t.Fatal("note-list shares must classify as writes")
	}
	if classifyWrite("GET", "/notes/shares") {
		t.Fatal("listing shares is a read")
	}
}

func TestNotesSetDefaultWireShape(t *testing.T) {
	var captured map[string]interface{}
	var path string
	srv := notesServer(t, &captured, &path)
	defer srv.Close()

	if err := runNotes(t, srv, "set-default", "video-ideas", "--personal"); err != nil {
		t.Fatalf("set-default failed: %v", err)
	}
	if path != "PUT /notes/list-prefs" {
		t.Fatalf("wrong endpoint: %s", path)
	}
	if captured["personal_by_default"] != true || captured["list"] != "video-ideas" {
		t.Fatalf("body did not travel: %v", captured)
	}
	if err := runNotes(t, srv, "set-default", "video-ideas", "--workspace"); err != nil {
		t.Fatalf("set-default --workspace failed: %v", err)
	}
	if captured["personal_by_default"] != false {
		t.Fatalf("--workspace must send false: %v", captured)
	}
	if err := runNotes(t, srv, "set-default", "video-ideas"); err == nil {
		t.Fatal("must require exactly one of --personal/--workspace")
	}
	if !classifyWrite("PUT", "/notes/list-prefs") {
		t.Fatal("list-prefs must classify as a write")
	}
}

func TestNotesUpdateVisibilityFlags(t *testing.T) {
	var captured map[string]interface{}
	var path string
	srv := notesServer(t, &captured, &path)
	defer srv.Close()

	if err := runNotes(t, srv, "update", "n-1", "--private"); err != nil {
		t.Fatalf("update --private failed: %v", err)
	}
	if captured["private"] != true {
		t.Fatalf("--private did not travel: %v", captured)
	}
	if err := runNotes(t, srv, "update", "n-1", "--shared"); err != nil {
		t.Fatalf("update --shared failed: %v", err)
	}
	if captured["private"] != false {
		t.Fatalf("--shared must send private=false: %v", captured)
	}
	if err := runNotes(t, srv, "update", "n-1", "--private", "--shared"); err == nil {
		t.Fatal("mutually exclusive flags must error before the network")
	}
}
