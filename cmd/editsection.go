package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// NewEditSectionCmd builds `blenau edit-section`.
func NewEditSectionCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "edit-section",
		Short: "Replace a section's BODY with optimistic locking (splices the raw markdown).",
		Long: `Replace a section's body, splicing only that section of the canonical raw
markdown — bytes outside it are never rewritten. Optimistic locking is
mandatory: pass --version with the section's current hash from
'blenau docs section <path> --heading "…"' (its "version" field). On a
version_mismatch the response returns the current content so you can reconcile
and retry.

New content comes from --content-file, or from stdin when it is omitted or "-".
edit-section replaces a BODY only; to rename a heading use 'rename-section', to
append/prepend use 'patch-section'.

Examples:
  blenau docs section docs/x.md --heading "## Setup"      # read version + body
  blenau edit-section --path docs/x.md --heading "## Setup" --version <hash> --content-file new.md
  echo "new body" | blenau edit-section --path docs/x.md --heading "## Setup" --version <hash>
  blenau edit-section --path docs/x.md --heading "## Setup" --version <hash> --content-file new.md --dry-run`,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, _ := cmd.Flags().GetString("path")
			heading, _ := cmd.Flags().GetString("heading")
			contentFile, _ := cmd.Flags().GetString("content-file")
			version, _ := cmd.Flags().GetString("version")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if path == "" || heading == "" {
				return fmt.Errorf("--path and --heading are required")
			}
			if version == "" {
				return fmt.Errorf("--version is required (the section hash from 'blenau docs section')")
			}
			content, err := readContentArg(contentFile)
			if err != nil {
				return err
			}
			body := map[string]interface{}{
				"path":             path,
				"heading":          heading,
				"new_content":      string(content),
				"expected_version": version,
				"dry_run":          dryRun,
			}
			if cmd.Flags().Changed("position") {
				pos, _ := cmd.Flags().GetInt("position")
				body["position"] = pos
			}
			b, _ := json.Marshal(body)
			raw, status, err := apiCall("POST", "/knowledge/edit-section", b)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
	c.Flags().String("path", "", "Document path.")
	c.Flags().String("heading", "", "Heading of the section to replace.")
	c.Flags().String("content-file", "", "File with the new body (stdin if omitted or \"-\").")
	c.Flags().String("version", "", "Expected section hash for optimistic locking (from 'docs section').")
	c.Flags().Int("position", 0, "Occurrence (1-based) to disambiguate a duplicated heading.")
	c.Flags().Bool("dry-run", false, "Preview the unified diff without committing.")
	c.Flags().Bool("json", false, "Emit JSON instead of human format.")
	return c
}
