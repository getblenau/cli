package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// maxBatchDocs mirrors the server cap on POST /knowledge/ingest-batch.
const maxBatchDocs = 200

// NewIngestCmd builds `blenau ingest`.
func NewIngestCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "ingest",
		Short: "Ingest a knowledge document (single via --path, or a whole folder via --dir).",
		Long: `Ingest markdown into the knowledge base.

Single doc: pass --path and --title; content comes from --content-file or stdin.
Optionally --auto-link crosslinks and attach provenance with --source type=ref.

Bulk (CLI-only): pass --dir to walk a local folder and ingest every .md file in
one shot (batched, server cap 200 per call). Each file's brain path mirrors its
path relative to --dir, optionally under --prefix; the title is derived from the
filename. This is the fast path for seeding a workspace from an existing docs
tree — something an in-browser agent cannot do because it has no local files.

Examples:
  blenau ingest --path docs/auth/oauth.md --title "OAuth" --content-file oauth.md
  cat notes.md | blenau ingest --path docs/notes.md --title "Notes"
  blenau ingest --dir ./docs --prefix handbook/`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dir, _ := cmd.Flags().GetString("dir"); dir != "" {
				return runIngestDir(cmd, dir)
			}
			return runIngestSingle(cmd)
		},
	}
	c.Flags().String("path", "", "Path of the document (e.g. docs/auth/oauth.md).")
	c.Flags().String("title", "", "Document title.")
	c.Flags().String("content-file", "", "File to read content from (stdin if omitted).")
	c.Flags().Bool("auto-link", false, "Auto-suggest crosslinks (single-doc mode).")
	c.Flags().StringArray("source", nil, "Source as type=ref (repeatable, single-doc mode).")
	c.Flags().String("dir", "", "Bulk mode: ingest every .md under this folder.")
	c.Flags().String("prefix", "", "Bulk mode: prepend this to each file's brain path (e.g. handbook/).")
	c.Flags().Bool("json", false, "Emit JSON instead of human format.")
	return c
}

func runIngestSingle(cmd *cobra.Command) error {
	path, _ := cmd.Flags().GetString("path")
	title, _ := cmd.Flags().GetString("title")
	contentFile, _ := cmd.Flags().GetString("content-file")
	autoLink, _ := cmd.Flags().GetBool("auto-link")
	sources, _ := cmd.Flags().GetStringArray("source")
	if path == "" || title == "" {
		return fmt.Errorf("--path and --title are required (or use --dir for bulk)")
	}
	content, err := readContentArg(contentFile)
	if err != nil {
		return err
	}
	body := map[string]interface{}{
		"path":      path,
		"title":     title,
		"content":   string(content),
		"auto_link": autoLink,
	}
	parsed, err := parseSources(sources)
	if err != nil {
		return err
	}
	if len(parsed) > 0 {
		body["sources"] = parsed
	}
	b, _ := json.Marshal(body)
	raw, status, err := apiCall("POST", "/knowledge/ingest-enhanced", b)
	if err != nil {
		return err
	}
	return emitOrFail(cmd, raw, status, nil)
}

// runIngestDir walks dir for .md files and ingests them via /knowledge/ingest-batch,
// chunked to the server's per-call cap.
func runIngestDir(cmd *cobra.Command, dir string) error {
	prefix, _ := cmd.Flags().GetString("prefix")
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("stat --dir: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}

	var docs []map[string]interface{}
	err = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() || !strings.EqualFold(filepath.Ext(p), ".md") {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		brainPath := prefix + filepath.ToSlash(rel)
		content, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}
		docs = append(docs, map[string]interface{}{
			"path":    brainPath,
			"title":   deriveTitle(rel),
			"content": string(content),
		})
		return nil
	})
	if err != nil {
		return err
	}
	if len(docs) == 0 {
		return fmt.Errorf("no .md files found under %s", dir)
	}

	// Chunk to the server cap and aggregate the per-file results.
	agg := map[string]interface{}{}
	var allResults []interface{}
	total := 0
	for start := 0; start < len(docs); start += maxBatchDocs {
		end := start + maxBatchDocs
		if end > len(docs) {
			end = len(docs)
		}
		b, _ := json.Marshal(map[string]interface{}{"documents": docs[start:end]})
		raw, status, err := apiCall("POST", "/knowledge/ingest-batch", b)
		if err != nil {
			return err
		}
		if status >= 400 {
			// Surface the first failing chunk verbatim via the standard path.
			return emitOrFail(cmd, raw, status, nil)
		}
		var chunk struct {
			Results []interface{} `json:"results"`
			Total   int           `json:"total"`
		}
		if json.Unmarshal(raw, &chunk) == nil {
			allResults = append(allResults, chunk.Results...)
			total += chunk.Total
		}
	}
	agg["results"] = allResults
	agg["total"] = total
	out, _ := json.Marshal(agg)
	return emitOrFail(cmd, out, 200, func(b []byte) error {
		w := cmd.OutOrStdout()
		failed := 0
		for _, r := range allResults {
			if m, ok := r.(map[string]interface{}); ok {
				if s, _ := m["status"].(string); s == "failed" {
					failed++
				}
			}
		}
		fmt.Fprintf(w, "ingested %d document(s), %d failed\n", total-failed, failed)
		return nil
	})
}

// deriveTitle turns a relative file path into a human title: the base filename
// without extension, with separators spaced out.
func deriveTitle(rel string) string {
	base := filepath.Base(rel)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.ReplaceAll(base, "-", " ")
	base = strings.ReplaceAll(base, "_", " ")
	base = strings.TrimSpace(base)
	if base == "" {
		return rel
	}
	return base
}
