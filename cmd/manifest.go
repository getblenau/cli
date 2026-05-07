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

// Manifest is the top-level agent contract.
type Manifest struct {
	Name        string        `json:"name"`
	Version     string        `json:"version"`
	Commands    []CommandSpec `json:"commands"`
	GlobalFlags []FlagSpec    `json:"global_flags,omitempty"`
}

// requiredFlags lists flag names that are required per command.
var requiredFlags = map[string][]string{
	"login": {"token"},
}

// requiredArgs lists arg names per command (cobra doesn't carry names).
var commandArgs = map[string][]ArgSpec{
	"search": {{Name: "query", Type: "string", Required: true}},
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
func EmitManifest(w io.Writer, root *cobra.Command, version string) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(BuildManifest(root, version))
}
