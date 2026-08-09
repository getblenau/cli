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
	"fmt"
	"os"

	"github.com/getblenau/cli/cmd"
)

// version is overridden at build time via -ldflags "-X main.version=..."
// (see .goreleaser.yml). This default is what a plain `go build` produces, and
// it must track the latest tag: the outdated-binary hint PRINTS this string, so
// a stale default makes every locally built binary announce itself as behind.
var version = "0.16.0"

func main() {
	setUTF8Stdout()

	root := cmd.NewRootCmd(version)

	// Handle --agent-manifest before normal Cobra execution so it short-circuits
	// even when no subcommand is given.
	for _, a := range os.Args[1:] {
		if a == "--agent-manifest" {
			if err := cmd.EmitManifest(os.Stdout, root, version); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
	}

	err := root.Execute()
	// After the command has produced its output: a non-intrusive "new version"
	// notice on stderr (only for interactive humans; scripts/agents never see it).
	cmd.NotifyIfUpdateAvailable(version, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		// "unknown command" is the one error an outdated binary reports as a
		// fact about the PRODUCT rather than about itself. A human shrugs and
		// checks their version; an agent writes down "this capability does not
		// exist" and stops. Naming the newer release here is what turns that
		// dead end back into a next step.
		if hint := cmd.OutdatedBinaryHint(version, err); hint != "" {
			fmt.Fprint(os.Stderr, hint)
		}
		os.Exit(1)
	}
}
