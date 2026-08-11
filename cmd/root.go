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
	// Multi-workspace selector (browser-login/identity lane only; ignored with a
	// pinned service token). Bound to package globals used by apiCall.
	root.PersistentFlags().StringVar(&flagWorkspace, "workspace", "",
		"Workspace (slug or id) to act in. Reads roam; writes require confirmation when it differs from the active workspace.")
	root.PersistentFlags().StringVar(&flagConfirmWorkspace, "confirm-workspace", "",
		"Confirm a write to this workspace (slug or id) in non-interactive use.")

	root.AddCommand(NewLoginCmd())
	root.AddCommand(NewLogoutCmd())
	root.AddCommand(NewWhoamiCmd())
	root.AddCommand(NewStatusCmd())
	root.AddCommand(NewUseCmd())
	root.AddCommand(NewSearchCmd())
	root.AddCommand(NewWorkspacesCmd())
	root.AddCommand(NewReposCmd())
	root.AddCommand(NewDocsCmd())
	root.AddCommand(NewIngestCmd())
	root.AddCommand(NewEditSectionCmd())
	root.AddCommand(NewAuditCmd())
	root.AddCommand(NewHealthCmd())
	root.AddCommand(NewAssetsCmd())
	root.AddCommand(NewNotesCmd())
	root.AddCommand(NewCollectionsCmd())
	root.AddCommand(NewPatchSectionCmd())
	root.AddCommand(NewRenameSectionCmd())
	root.AddCommand(NewDeleteSectionCmd())
	root.AddCommand(NewRevertWriteCmd())
	root.AddCommand(NewCrystallizeCmd())
	root.AddCommand(NewSmartCrystallizeCmd())
	root.AddCommand(NewSuggestCrosslinksCmd())
	root.AddCommand(NewUpdateCmd())
	root.AddCommand(NewConceptCmd())
	root.AddCommand(NewPublishCmd())

	return root
}
