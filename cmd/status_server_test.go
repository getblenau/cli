package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// `blenau status --server` is the only place in this CLI that answers "does a
// repo rename repair itself here?". The fact lives on GET /health, which was
// reachable over raw HTTP and nowhere else — so the party that most needs to
// audit itself, an agent, could not ask.
//
// These tests guard the DISCOVERY surface as much as the behaviour. A flag
// nobody can find is a flag nobody uses, and this project has already shipped
// one capability that was invisible from every channel.

func TestStatusServerFlagIsDiscoverable(t *testing.T) {
	root := NewRootCmd("test")
	var status *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "status" {
			status = c
		}
	}
	if status == nil {
		t.Fatal("status command missing from the root")
	}
	if status.Flags().Lookup("server") == nil {
		t.Fatal("--server not declared, so it cannot reach the manifest")
	}
	// The Short is what shows in the root listing AND in the agent manifest.
	// Leaving it unmentioned there is how a capability stays invisible.
	if !strings.Contains(status.Short, "--server") {
		t.Errorf("Short does not name --server: %q", status.Short)
	}
}

func TestStatusHelpCarriesTheRemedyNotJustTheFact(t *testing.T) {
	root := NewRootCmd("test")
	var status *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "status" {
			status = c
		}
	}
	if status == nil {
		t.Fatal("status command missing")
	}
	for _, want := range []string{
		"rename_autoheal",        // the field it reports
		"repos update",           // what to DO when it is false
		"silently",               // why it matters: the failure is silent
		"blenau status --server", // a runnable example, not prose
	} {
		if !strings.Contains(status.Long, want) {
			t.Errorf("long help is missing %q", want)
		}
	}
}

func TestStatusStaysOfflineByDefault(t *testing.T) {
	// The default path must not call the API: `status` is what people run when
	// something is already broken, often with no network. Asserted on the flag
	// default rather than by intercepting HTTP, because the guarantee IS the
	// default.
	root := NewRootCmd("test")
	for _, c := range root.Commands() {
		if c.Name() != "status" {
			continue
		}
		f := c.Flags().Lookup("server")
		if f == nil {
			t.Fatal("--server missing")
		}
		if f.DefValue != "false" {
			t.Errorf("--server defaults to %q; status must stay offline", f.DefValue)
		}
	}
}
