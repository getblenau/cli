package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The agent manifest is the contract agents read to discover what this CLI can
// do — the root help says so in as many words. So a command missing from it is
// a command that does not exist, as far as an agent is concerned.
//
// That happened. The walker emitted a command's CHILDREN and then `continue`d
// past the command itself, so any parent that also does work of its own was
// dropped. On the day `ingest status` was added, `blenau ingest` — the main
// write command, and the only place `--source` lives — disappeared from the
// contract. Nothing failed and no test noticed; a customer's agent went looking
// for a way to record provenance and reported that it did not exist.
//
// This walks the real command tree instead of listing names by hand, so a
// command added tomorrow is covered without anyone remembering to come here.

func manifestNames(t *testing.T) map[string]map[string]bool {
	t.Helper()
	root := NewRootCmd("test")
	var sb strings.Builder
	if err := EmitManifest(&sb, root, "test"); err != nil {
		t.Fatalf("EmitManifest: %v", err)
	}
	var m Manifest
	if err := json.Unmarshal([]byte(sb.String()), &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	out := map[string]map[string]bool{}
	for _, c := range m.Commands {
		flags := map[string]bool{}
		for _, f := range c.Flags {
			flags[f.Name] = true
		}
		out[c.Name] = flags
	}
	return out
}

func TestEveryRunnableCommandIsInTheAgentManifest(t *testing.T) {
	got := manifestNames(t)

	var missing []string
	var walk func(c *cobra.Command, prefix string)
	walk = func(c *cobra.Command, prefix string) {
		for _, sub := range c.Commands() {
			if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
				continue
			}
			name := strings.TrimSpace(prefix + " " + sub.Name())
			// A group that only holds subcommands is not itself an operation;
			// only things an agent can actually RUN have to be listed.
			if sub.Runnable() {
				if _, ok := got[name]; !ok {
					missing = append(missing, name)
				}
			}
			walk(sub, name)
		}
	}
	walk(NewRootCmd("test"), "")

	if len(missing) > 0 {
		t.Fatalf("runnable commands absent from the agent manifest: %v", missing)
	}
}

func TestIngestKeepsItsSourceFlagInTheManifest(t *testing.T) {
	// The specific loss, pinned: an agent asked to record a document's
	// provenance has to be able to find this flag.
	got := manifestNames(t)
	flags, ok := got["ingest"]
	if !ok {
		t.Fatal("`ingest` is missing from the manifest")
	}
	for _, f := range []string{"source", "path", "title", "content-file"} {
		if !flags[f] {
			t.Errorf("`ingest` lost its --%s flag in the manifest", f)
		}
	}
}

func TestASubcommandDoesNotDisplaceItsParent(t *testing.T) {
	got := manifestNames(t)
	for _, pair := range [][2]string{
		{"ingest", "ingest status"},
	} {
		if _, ok := got[pair[0]]; !ok {
			t.Errorf("%q vanished once %q existed", pair[0], pair[1])
		}
		if _, ok := got[pair[1]]; !ok {
			t.Errorf("%q is missing", pair[1])
		}
	}
}
