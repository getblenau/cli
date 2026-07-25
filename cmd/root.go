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
		// PersistentPreRun captures the global --workspace selector before any
		// subcommand runs, so every API request can forward it as the
		// X-Blenau-Workspace header (multi-workspace roaming for identity tokens).
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if ws, _ := cmd.Flags().GetString("workspace"); ws != "" {
				workspaceOverride = ws
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
		SilenceUsage: true,
	}

	root.PersistentFlags().Bool("json", false, "Emit JSON output (default for non-TTY).")
	root.PersistentFlags().String("workspace", "", "Target workspace UUID (identity tokens only; roams reads/writes there). Env: BLENAU_WORKSPACE. Discover with 'blenau workspaces'.")
	// --agent-manifest is intercepted in main() before Cobra parsing;
	// declared here so it appears in --help.
	root.PersistentFlags().Bool("agent-manifest", false, "emit a JSON contract describing the CLI surface and exit")

	root.AddCommand(NewLoginCmd())
	root.AddCommand(NewSearchCmd())
	root.AddCommand(NewReposCmd())
	root.AddCommand(NewDocsCmd())
	root.AddCommand(NewIngestCmd())
	root.AddCommand(NewEditSectionCmd())
	root.AddCommand(NewPatchSectionCmd())
	root.AddCommand(NewRenameSectionCmd())
	root.AddCommand(NewDeleteSectionCmd())
	root.AddCommand(NewRevertWriteCmd())
	root.AddCommand(NewCrystallizeCmd())
	root.AddCommand(NewSmartCrystallizeCmd())
	root.AddCommand(NewSuggestCrosslinksCmd())
	root.AddCommand(NewAuditCmd())
	root.AddCommand(NewAssetsCmd())
	root.AddCommand(NewNotesCmd())
	root.AddCommand(NewCollectionsCmd())
	root.AddCommand(NewWorkspacesCmd())

	return root
}
