// Package cmd hosts the Cobra command tree for the Blenau CLI.
package cmd

import (
	"github.com/spf13/cobra"
)

// NewRootCmd builds the root `blenau` command.
func NewRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:     "blenau",
		Short:   "Blenau command-line interface",
		Long:    "Blenau CLI — agent-first command-line interface for the Blenau platform.\n\nAll commands support --json for structured output. Use --agent-manifest to emit a JSON contract of the CLI surface for tooling discovery.",
		Version: version,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
		SilenceUsage: true,
	}

	root.PersistentFlags().Bool("json", false, "Emit JSON output (default for non-TTY).")
	// --agent-manifest is intercepted in main() before Cobra parsing;
	// declared here so it appears in --help.
	root.PersistentFlags().Bool("agent-manifest", false, "emit a JSON contract describing the CLI surface and exit")

	root.AddCommand(NewLoginCmd())
	root.AddCommand(NewSearchCmd())
	root.AddCommand(NewWorkspacesCmd())
	root.AddCommand(NewReposCmd())
	root.AddCommand(NewDocsCmd())
	root.AddCommand(NewIngestCmd())
	root.AddCommand(NewEditSectionCmd())
	root.AddCommand(NewAuditCmd())
	root.AddCommand(NewAssetsCmd())

	return root
}
