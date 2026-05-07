package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// NewEditSectionCmd builds `blenau edit-section`.
func NewEditSectionCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "edit-section",
		Short: "Replace a section of a knowledge document.",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, _ := cmd.Flags().GetString("path")
			heading, _ := cmd.Flags().GetString("heading")
			contentFile, _ := cmd.Flags().GetString("content-file")
			version, _ := cmd.Flags().GetInt("version")
			if path == "" || heading == "" || contentFile == "" {
				return fmt.Errorf("--path, --heading and --content-file are required")
			}
			content, err := os.ReadFile(contentFile)
			if err != nil {
				return fmt.Errorf("read content file: %w", err)
			}
			body := map[string]interface{}{
				"path":             path,
				"heading":          heading,
				"new_content":      string(content),
				"expected_version": version,
			}
			b, _ := json.Marshal(body)
			raw, status, err := apiCall("POST", "/knowledge/edit-section", b)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
	c.Flags().String("path", "", "Document path")
	c.Flags().String("heading", "", "Heading to replace")
	c.Flags().String("content-file", "", "File with new content")
	c.Flags().Int("version", 0, "Expected document version (optimistic locking)")
	c.Flags().Bool("json", false, "Emit JSON instead of human format.")
	return c
}
