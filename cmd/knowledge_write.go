package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

// parseSources turns repeatable "type=ref" flags into the API's sources array.
func parseSources(raw []string) ([]map[string]string, error) {
	out := []map[string]string{}
	for _, s := range raw {
		kv := strings.SplitN(s, "=", 2)
		if len(kv) != 2 || kv[0] == "" {
			return nil, fmt.Errorf("invalid --source %q (expected type=ref)", s)
		}
		out = append(out, map[string]string{"type": kv[0], "ref": kv[1]})
	}
	return out, nil
}

// addLock copies the optional optimistic-lock + position flags into a write body.
func addLock(cmd *cobra.Command, body map[string]interface{}) {
	if v, _ := cmd.Flags().GetString("version"); v != "" {
		body["expected_version"] = v
	}
	if cmd.Flags().Changed("position") {
		pos, _ := cmd.Flags().GetInt("position")
		body["position"] = pos
	}
}

// NewPatchSectionCmd builds `blenau patch-section`.
func NewPatchSectionCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "patch-section",
		Short: "Append, prepend, or replace a section's body (splices the raw markdown).",
		Long: `Append, prepend, or replace a section's body without rewriting the rest of the
document. Content comes from --content-file, or stdin when omitted or "-".

--op selects the mode: append (default) | prepend | replace. --version is an
OPTIONAL optimistic lock (the section hash from 'blenau docs section'); omit it
for a blind write. --dry-run previews the unified diff.

Examples:
  echo "- new bullet" | blenau patch-section --path docs/x.md --heading "## Log" --op append
  blenau patch-section --path docs/x.md --heading "## Intro" --op replace --content-file intro.md --version <hash>`,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, _ := cmd.Flags().GetString("path")
			heading, _ := cmd.Flags().GetString("heading")
			op, _ := cmd.Flags().GetString("op")
			contentFile, _ := cmd.Flags().GetString("content-file")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if path == "" || heading == "" {
				return fmt.Errorf("--path and --heading are required")
			}
			content, err := readContentArg(contentFile)
			if err != nil {
				return err
			}
			body := map[string]interface{}{
				"path": path, "heading": heading, "content": string(content),
				"op": op, "dry_run": dryRun,
			}
			addLock(cmd, body)
			b, _ := json.Marshal(body)
			raw, status, err := apiCall("POST", "/knowledge/patch-section", b)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
	c.Flags().String("path", "", "Document path.")
	c.Flags().String("heading", "", "Heading of the section to patch.")
	c.Flags().String("op", "append", "append | prepend | replace.")
	c.Flags().String("content-file", "", "File with the content (stdin if omitted or \"-\").")
	c.Flags().String("version", "", "Optional section hash for optimistic locking.")
	c.Flags().Int("position", 0, "Occurrence (1-based) to disambiguate a duplicated heading.")
	c.Flags().Bool("dry-run", false, "Preview the unified diff without committing.")
	c.Flags().Bool("json", false, "Emit JSON instead of human format.")
	return c
}

// NewRenameSectionCmd builds `blenau rename-section`.
func NewRenameSectionCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "rename-section",
		Short: "Rename a section's heading, keeping its level and body.",
		Long: `Rename a section's heading while preserving its level and body. This is the
SAFE path for a rename — edit-section rejects a heading change on purpose.
--version is an optional optimistic lock; --dry-run previews the diff.

Example:
  blenau rename-section --path docs/x.md --heading "## Setup" --new-heading "## Installation"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, _ := cmd.Flags().GetString("path")
			heading, _ := cmd.Flags().GetString("heading")
			newHeading, _ := cmd.Flags().GetString("new-heading")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if path == "" || heading == "" || newHeading == "" {
				return fmt.Errorf("--path, --heading and --new-heading are required")
			}
			body := map[string]interface{}{
				"path": path, "heading": heading, "new_heading": newHeading, "dry_run": dryRun,
			}
			addLock(cmd, body)
			b, _ := json.Marshal(body)
			raw, status, err := apiCall("POST", "/knowledge/rename-section", b)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
	c.Flags().String("path", "", "Document path.")
	c.Flags().String("heading", "", "Current heading.")
	c.Flags().String("new-heading", "", "New heading (keep the same '##' level).")
	c.Flags().String("version", "", "Optional section hash for optimistic locking.")
	c.Flags().Int("position", 0, "Occurrence (1-based) to disambiguate a duplicated heading.")
	c.Flags().Bool("dry-run", false, "Preview the unified diff without committing.")
	c.Flags().Bool("json", false, "Emit JSON instead of human format.")
	return c
}

// NewDeleteSectionCmd builds `blenau delete-section`.
func NewDeleteSectionCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "delete-section",
		Short: "Delete a section (heading + body) by splicing it out of the raw.",
		Long: `Delete an entire section (its heading and body). --version is an optional
optimistic lock; --position disambiguates a duplicated heading; --dry-run
previews the diff. Every write is a git commit, so a mistaken delete is
recoverable with 'blenau revert-write --path <path>'.

Example:
  blenau delete-section --path docs/x.md --heading "## Deprecated"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, _ := cmd.Flags().GetString("path")
			heading, _ := cmd.Flags().GetString("heading")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			if path == "" || heading == "" {
				return fmt.Errorf("--path and --heading are required")
			}
			body := map[string]interface{}{"path": path, "heading": heading, "dry_run": dryRun}
			addLock(cmd, body)
			b, _ := json.Marshal(body)
			raw, status, err := apiCall("POST", "/knowledge/delete-section", b)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
	c.Flags().String("path", "", "Document path.")
	c.Flags().String("heading", "", "Heading of the section to delete.")
	c.Flags().String("version", "", "Optional section hash for optimistic locking.")
	c.Flags().Int("position", 0, "Occurrence (1-based) to disambiguate a duplicated heading.")
	c.Flags().Bool("dry-run", false, "Preview the unified diff without committing.")
	c.Flags().Bool("json", false, "Emit JSON instead of human format.")
	return c
}

// NewRevertWriteCmd builds `blenau revert-write`.
func NewRevertWriteCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "revert-write",
		Short: "Undo a write to a document using git history (every write is reversible).",
		Long: `Undo a write to a document. Restores the file to the state it had BEFORE
--commit (or before the latest commit, if omitted) and commits that as a
forward revert — every edit/patch/ingest is a git commit, so every write is
reversible. Re-derives the search index from the restored content.

Examples:
  blenau revert-write --path docs/x.md                 # undo the latest write
  blenau revert-write --path docs/x.md --commit <sha>  # undo a specific commit`,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, _ := cmd.Flags().GetString("path")
			commit, _ := cmd.Flags().GetString("commit")
			if path == "" {
				return fmt.Errorf("--path is required")
			}
			body := map[string]interface{}{"path": path}
			if commit != "" {
				body["commit_sha"] = commit
			}
			b, _ := json.Marshal(body)
			raw, status, err := apiCall("POST", "/knowledge/revert-write", b)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
	c.Flags().String("path", "", "Document path.")
	c.Flags().String("commit", "", "Commit SHA to undo (default: the latest commit on the file).")
	c.Flags().Bool("json", false, "Emit JSON instead of human format.")
	return c
}

// NewCrystallizeCmd builds `blenau crystallize`.
func NewCrystallizeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "crystallize",
		Short: "Turn raw session text into a NEW knowledge document (opens a PR).",
		Long: `Crystallize a raw agent-session transcript into a new, structured knowledge
document. Session text comes from --content-file or stdin. --title names the
doc; --output-path sets the base folder (default docs/crystallized). Attach
provenance with repeatable --source type=ref.

Prefer 'blenau smart-crystallize' when the material likely belongs across
SEVERAL existing docs — it routes each topic to the best match instead of
creating one new file.

Example:
  blenau crystallize --title "OAuth notes" --output-path docs/auth --content-file session.txt`,
		RunE: func(cmd *cobra.Command, args []string) error {
			title, _ := cmd.Flags().GetString("title")
			outputPath, _ := cmd.Flags().GetString("output-path")
			contentFile, _ := cmd.Flags().GetString("content-file")
			sources, _ := cmd.Flags().GetStringArray("source")
			content, err := readContentArg(contentFile)
			if err != nil {
				return err
			}
			if len(content) == 0 {
				return fmt.Errorf("no session content (provide --content-file or pipe via stdin)")
			}
			body := map[string]interface{}{"session_context": string(content)}
			if title != "" {
				body["title"] = title
			}
			if outputPath != "" {
				body["output_path"] = outputPath
			}
			parsed, err := parseSources(sources)
			if err != nil {
				return err
			}
			if len(parsed) > 0 {
				body["sources"] = parsed
			}
			b, _ := json.Marshal(body)
			raw, status, err := apiCall("POST", "/knowledge/crystallize-enhanced", b)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
	c.Flags().String("title", "", "Title for the new document.")
	c.Flags().String("output-path", "", "Base folder for the generated doc (default docs/crystallized).")
	c.Flags().String("content-file", "", "File with the session text (stdin if omitted or \"-\").")
	c.Flags().StringArray("source", nil, "Provenance as type=ref (repeatable).")
	c.Flags().Bool("json", false, "Emit JSON instead of human format.")
	return c
}

// NewSmartCrystallizeCmd builds `blenau smart-crystallize`.
func NewSmartCrystallizeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "smart-crystallize",
		Short: "Split session text into topics and route each to the best-matching doc.",
		Long: `Split a raw session into topical blocks and route each to the best existing
document (creating one only when nothing fits), batching the GitHub commits into
one PR per affected repo. This is the "merge, don't duplicate" path — the
knowledge base's golden rule. Session text comes from --content-file or stdin.

--repo-hint biases routing toward a repo when a path is ambiguous.

Example:
  blenau smart-crystallize --content-file session.txt`,
		RunE: func(cmd *cobra.Command, args []string) error {
			contentFile, _ := cmd.Flags().GetString("content-file")
			repoHint, _ := cmd.Flags().GetString("repo-hint")
			content, err := readContentArg(contentFile)
			if err != nil {
				return err
			}
			if len(content) == 0 {
				return fmt.Errorf("no session content (provide --content-file or pipe via stdin)")
			}
			body := map[string]interface{}{"session_context": string(content)}
			if repoHint != "" {
				body["repo_hint"] = repoHint
			}
			b, _ := json.Marshal(body)
			raw, status, err := apiCall("POST", "/knowledge/smart-crystallize", b)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
	c.Flags().String("content-file", "", "File with the session text (stdin if omitted or \"-\").")
	c.Flags().String("repo-hint", "", "Bias routing toward this repo (org/name) when ambiguous.")
	c.Flags().Bool("json", false, "Emit JSON instead of human format.")
	return c
}

// NewSuggestCrosslinksCmd builds `blenau suggest-crosslinks`.
func NewSuggestCrosslinksCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "suggest-crosslinks <document-id>",
		Short: "Suggest cross-links to add inside a document (ready-to-paste markdown).",
		Long: `Suggest cross-links to add inside a specific document — ready-to-paste markdown
snippets plus the heading each likely belongs under. Pass the document's id
(from 'blenau docs list --json', the "id" field). Advisory: semantic search
works regardless of links. Requires an admin/member role.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, status, err := apiCall("GET", "/github/suggest-crosslinks/"+url.PathEscape(args[0]), nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
}
