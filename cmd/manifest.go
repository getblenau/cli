package cmd

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// FlagSpec describes a single flag in the agent manifest.
type FlagSpec struct {
	Name        string      `json:"name"`
	Type        string      `json:"type"`
	Required    bool        `json:"required,omitempty"`
	Default     interface{} `json:"default,omitempty"`
	Description string      `json:"description,omitempty"`
}

// ArgSpec describes a positional argument.
type ArgSpec struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
}

// CommandSpec describes one subcommand.
type CommandSpec struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Args        []ArgSpec  `json:"args,omitempty"`
	Flags       []FlagSpec `json:"flags,omitempty"`
}

// UpdateInfo answers, for an agent introspecting this binary, the question the
// command list alone cannot: "is this contract the CURRENT one?". Without it a
// missing command is indistinguishable from a command that never existed.
// Omitted entirely when the check is opted out of or GitHub is unreachable —
// absent means "unknown", never "up to date".
type UpdateInfo struct {
	LatestVersion string `json:"latest_version"`
	Stale         bool   `json:"stale"`
}

// Manifest is the top-level agent contract.
type Manifest struct {
	Name        string        `json:"name"`
	Version     string        `json:"version"`
	Update      *UpdateInfo   `json:"update,omitempty"`
	Commands    []CommandSpec `json:"commands"`
	GlobalFlags []FlagSpec    `json:"global_flags,omitempty"`
}

// requiredFlags lists flag names that are required per command. Top-level
// commands are keyed by their own name; subcommands by "group leaf".
// SUPPLEMENT, not the source of truth. Anything declared with
// `MarkFlagRequired` is picked up from cobra automatically (see collectFlags);
// this covers only flags a command enforces by hand inside RunE.
var requiredFlags = map[string][]string{
	"login":               {"token"},
	"health repair":       {"type"},
	"ingest":              {"path", "title"},
	"edit-section":        {"path", "heading", "version"},
	"patch-section":       {"path", "heading"},
	"rename-section":      {"path", "heading", "new-heading"},
	"delete-section":      {"path", "heading"},
	"revert-write":        {"path"},
	"docs section":        {"heading"},
	"collections import":  {"file"},
	"collections upsert":  {"file"},
	"collections share":   {"group"},
	"collections unshare": {"group"},
	"notes share-list":    {"group"},
	"notes unshare-list":  {"group"},
	"repos connect":       {"repo", "installation-id"},
}

// commandArgs lists positional arg names per command (cobra doesn't carry
// names). Keyed the same way as requiredFlags.
var commandArgs = map[string][]ArgSpec{
	"search":                    {{Name: "query", Type: "string", Required: true}},
	"docs get":                  {{Name: "path", Type: "string", Required: true}},
	"docs structure":            {{Name: "path", Type: "string", Required: true}},
	"docs section":              {{Name: "path", Type: "string", Required: true}},
	"suggest-crosslinks":        {{Name: "document-id", Type: "string", Required: true}},
	"repos disconnect":          {{Name: "repo-id", Type: "string", Required: true}},
	"repos update":              {{Name: "repo-id", Type: "string", Required: true}},
	"repos available":           {{Name: "installation-id", Type: "string", Required: true}},
	"repos sync":                {{Name: "repo-id", Type: "string", Required: false}},
	"notes remember":            {{Name: "body", Type: "string", Required: true}},
	"notes done":                {{Name: "note-id", Type: "string", Required: true}},
	"notes reopen":              {{Name: "note-id", Type: "string", Required: true}},
	"notes update":              {{Name: "note-id", Type: "string", Required: true}},
	"notes forget":              {{Name: "note-id", Type: "string", Required: true}},
	"collections create":        {{Name: "name", Type: "string", Required: true}},
	"collections describe":      {{Name: "name", Type: "string", Required: true}},
	"collections fields":        {{Name: "name", Type: "string", Required: true}},
	"collections update":        {{Name: "name", Type: "string", Required: true}},
	"collections query":         {{Name: "name", Type: "string", Required: true}},
	"collections reindex":       {{Name: "name", Type: "string", Required: true}},
	"collections import":        {{Name: "name", Type: "string", Required: true}},
	"collections embed-pending": {{Name: "name", Type: "string", Required: true}},
	"collections get-record":    {{Name: "name", Type: "string", Required: true}, {Name: "external-id", Type: "string", Required: true}},
	"collections delete-record": {{Name: "name", Type: "string", Required: true}, {Name: "external-id", Type: "string", Required: true}},
	"collections reconcile":     {{Name: "name", Type: "string", Required: true}},
	"collections rotate-secret": {{Name: "name", Type: "string", Required: true}},
	"collections delete":        {{Name: "name", Type: "string", Required: true}},
	"collections upsert":        {{Name: "name", Type: "string", Required: true}},
	"collections shares":        {{Name: "name", Type: "string", Required: true}},
	"collections share":         {{Name: "name", Type: "string", Required: true}},
	"collections unshare":       {{Name: "name", Type: "string", Required: true}},
	"notes share-list":          {{Name: "list", Type: "string", Required: true}},
	"notes unshare-list":        {{Name: "list", Type: "string", Required: true}},
	"notes set-default":         {{Name: "list", Type: "string", Required: true}},
}

func flagType(f *pflag.Flag) string {
	switch f.Value.Type() {
	case "bool":
		return "bool"
	case "int", "int8", "int16", "int32", "int64":
		return "int"
	case "float32", "float64":
		return "float"
	case "stringSlice", "stringArray":
		return "string[]"
	default:
		return "string"
	}
}

func flagDefault(f *pflag.Flag) interface{} {
	switch f.Value.Type() {
	case "bool":
		if f.DefValue == "true" {
			return true
		}
		return nil // omit false default
	case "int", "int8", "int16", "int32", "int64":
		if f.DefValue == "0" {
			return nil
		}
		var n int
		_, err := jsonNumber(f.DefValue, &n)
		if err == nil {
			return n
		}
		return f.DefValue
	default:
		if f.DefValue == "" {
			return nil
		}
		return f.DefValue
	}
}

func jsonNumber(s string, out *int) (int, error) {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, io.EOF
		}
		n = n*10 + int(c-'0')
	}
	*out = n
	return n, nil
}

func collectFlags(set *pflag.FlagSet, requiredNames []string) []FlagSpec {
	required := map[string]bool{}
	for _, n := range requiredNames {
		required[n] = true
	}
	var out []FlagSpec
	set.VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		// Cobra already knows which flags are required — `MarkFlagRequired`
		// records it on the flag. Ask IT, and treat `requiredNames` as a
		// supplement for the handful enforced by hand inside RunE.
		//
		// The map used to be the only source, and it drifted exactly the way a
		// hand-maintained mirror of the code always does: eight commands
		// (every `assets` subcommand, and one added the same afternoon) told
		// agents a flag was optional while the command refused without it. An
		// agent cannot see why that call failed.
		if a, ok := f.Annotations[cobra.BashCompOneRequiredFlag]; ok &&
			len(a) > 0 && a[0] == "true" {
			required[f.Name] = true
		}
		out = append(out, FlagSpec{
			Name:        f.Name,
			Type:        flagType(f),
			Required:    required[f.Name],
			Default:     flagDefault(f),
			Description: strings.TrimSpace(f.Usage),
		})
	})
	return out
}

// BuildManifest introspects the cobra tree and builds the manifest.
func BuildManifest(root *cobra.Command, version string) Manifest {
	m := Manifest{
		Name:    root.Name(),
		Version: version,
	}
	// Global flags = root's persistent flags.
	m.GlobalFlags = collectFlags(root.PersistentFlags(), nil)
	for _, sub := range root.Commands() {
		if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
			continue
		}
		if sub.HasSubCommands() {
			for _, child := range sub.Commands() {
				if child.Hidden || child.Name() == "help" {
					continue
				}
				name := sub.Name() + " " + child.Name()
				spec := CommandSpec{
					Name:        name,
					Description: strings.TrimSpace(child.Short),
					Args:        commandArgs[name],
					Flags:       collectFlags(child.Flags(), requiredFlags[name]),
				}
				m.Commands = append(m.Commands, spec)
			}
			// A parent that also DOES something must still appear. This used to
			// `continue` here, so the day `ingest status` was added, `blenau
			// ingest` — the product's main write command, and the only place
			// `--source` exists — silently vanished from the contract agents
			// read to discover the CLI. Nothing failed; the command simply
			// stopped being discoverable, and a customer's agent went looking
			// for a way to record provenance and could not find one.
			if !sub.Runnable() {
				continue
			}
		}
		spec := CommandSpec{
			Name:        sub.Name(),
			Description: strings.TrimSpace(sub.Short),
			Args:        commandArgs[sub.Name()],
			Flags:       collectFlags(sub.Flags(), requiredFlags[sub.Name()]),
		}
		m.Commands = append(m.Commands, spec)
	}
	return m
}

// EmitManifest writes the manifest as indented JSON to w.
//
// The staleness lookup lives HERE and not in BuildManifest so the manifest
// builder stays pure: the coverage tests call BuildManifest directly and must
// never reach the network to do it.
func EmitManifest(w io.Writer, root *cobra.Command, version string) error {
	m := BuildManifest(root, version)
	if latest, stale := UpdateAvailable(version); latest != "" {
		m.Update = &UpdateInfo{LatestVersion: latest, Stale: stale}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(m)
}
