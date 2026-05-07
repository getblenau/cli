package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// NewIngestCmd builds `blenau ingest`.
func NewIngestCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "ingest",
		Short: "Ingest a new knowledge document.",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, _ := cmd.Flags().GetString("path")
			title, _ := cmd.Flags().GetString("title")
			contentFile, _ := cmd.Flags().GetString("content-file")
			autoLink, _ := cmd.Flags().GetBool("auto-link")
			sources, _ := cmd.Flags().GetStringArray("source")
			if path == "" || title == "" {
				return fmt.Errorf("--path and --title are required")
			}
			var content []byte
			var err error
			if contentFile != "" {
				content, err = os.ReadFile(contentFile)
				if err != nil {
					return fmt.Errorf("read content file: %w", err)
				}
			} else {
				content, err = io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("read stdin: %w", err)
				}
			}
			parsedSources := []map[string]string{}
			for _, s := range sources {
				kv := strings.SplitN(s, "=", 2)
				if len(kv) != 2 {
					return fmt.Errorf("invalid --source %q (expected type=ref)", s)
				}
				parsedSources = append(parsedSources, map[string]string{
					"type": kv[0], "ref": kv[1],
				})
			}
			body := map[string]interface{}{
				"path":      path,
				"title":     title,
				"content":   string(content),
				"auto_link": autoLink,
			}
			if len(parsedSources) > 0 {
				body["sources"] = parsedSources
			}
			b, _ := json.Marshal(body)
			raw, status, err := apiCall("POST", "/knowledge/ingest-enhanced", b)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
	c.Flags().String("path", "", "Path of the document (e.g. docs/auth/oauth.md)")
	c.Flags().String("title", "", "Document title")
	c.Flags().String("content-file", "", "File to read content from (stdin if empty)")
	c.Flags().Bool("auto-link", false, "Auto-suggest crosslinks")
	c.Flags().StringArray("source", nil, "Source as type=ref (repeatable)")
	c.Flags().Bool("json", false, "Emit JSON instead of human format.")
	return c
}
