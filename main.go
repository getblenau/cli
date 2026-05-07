// Package main is the entrypoint for the Blenau CLI.
//
// Design principles:
//   - Agent-first: every command supports --json, --help is exhaustive,
//     --agent-manifest emits the full contract for discovery.
//   - UTF-8 strict: stdout always UTF-8 NFC, no BOM, on Linux/macOS/Windows.
//   - Single binary distribution (GitHub Releases + Homebrew + Scoop).
//   - HTTP client only — no local logic beyond auth, formatting, validation.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/getblenau/cli/cmd"
)

// version is overridden at build time via -ldflags "-X main.version=..."
var version = "0.0.0-dev"

// setUTF8Stdout forces stdout to UTF-8 on Windows (code page 65001).
//
// TODO: implement properly with golang.org/x/sys/windows.SetConsoleOutputCP(65001)
// behind a build tag (//go:build windows). Currently a no-op stub; will be
// wired in alongside the first real subcommand that emits non-ASCII output.
func setUTF8Stdout() {
	// no-op for now
}

func main() {
	setUTF8Stdout()

	root := cmd.NewRootCmd(version)

	// Handle --agent-manifest before normal Cobra execution so it short-circuits
	// even when no subcommand is given.
	for _, a := range os.Args[1:] {
		if a == "--agent-manifest" {
			emitAgentManifest(root, version)
			return
		}
	}

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// emitAgentManifest writes a JSON contract describing the CLI surface.
func emitAgentManifest(root interface{ Name() string }, ver string) {
	type flagSpec struct {
		Name  string `json:"name"`
		Type  string `json:"type"`
		Usage string `json:"usage"`
	}
	type cmdSpec struct {
		Name  string     `json:"name"`
		Short string     `json:"short"`
		Flags []flagSpec `json:"flags"`
	}
	manifest := struct {
		Name     string    `json:"name"`
		Version  string    `json:"version"`
		Commands []cmdSpec `json:"commands"`
		Flags    []flagSpec `json:"flags"`
	}{
		Name:    "blenau",
		Version: ver,
		Commands: []cmdSpec{},
		Flags: []flagSpec{
			{Name: "json", Type: "bool", Usage: "structured JSON output"},
			{Name: "agent-manifest", Type: "bool", Usage: "emit JSON contract and exit"},
			{Name: "version", Type: "bool", Usage: "print version and exit"},
		},
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(manifest)
}
