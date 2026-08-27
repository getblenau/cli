package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/text/unicode/norm"
)

// newDocsDeleteCmd builds `blenau docs delete` — a whole-document delete.
//
// This is deliberately narrow and loud. Unlike delete-section (which splices one
// heading out of a surviving file), this removes the ENTIRE document: its file on
// GitHub and its search index. So it:
//   - resolves an EXACT path to exactly one document (never a prefix/glob) and
//     refuses if nothing matches — never a silent no-op;
//   - echoes the RESOLVED title/path/source_type (not what you typed) before
//     doing anything;
//   - requires confirmation: re-type the path interactively, or pass --yes;
//     fail-closed (refuse) in a non-interactive shell without --yes;
//   - on success prints the commit SHA and the exact git command to recover it.
func newDocsDeleteCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "delete",
		Short: "Delete a WHOLE document (its file on GitHub + its search index).",
		Long: `Delete an entire document — its source .md on GitHub AND its search index.
This is NOT delete-section: delete-section removes one heading from a file that
survives; this removes the whole file.

The delete is GitHub-first and fail-closed. The file removed on GitHub is always
the resolved document's canonical file — never the raw path you type — so it can
never reach outside the document (no '.github/workflows', no '../' traversal).
When a real file backs the doc it is deleted from Git first (recoverable from git
history); an index-only doc with no backing file is removed only with
--allow-db-only, and never when an open PR would recreate it.

--path takes an EXACT document path (no globs or wildcards). Confirm by re-typing
the path when prompted, or pass --yes (required in a non-interactive shell).
--dry-run shows exactly what would happen without changing anything.

Examples:
  blenau docs delete --path ganemo/infra-odoo/old-runbook.md
  blenau docs delete --path eng/legacy.md --dry-run
  blenau docs delete --path eng/legacy.md --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, _ := cmd.Flags().GetString("path")
			yes, _ := cmd.Flags().GetBool("yes")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			allowDBOnly, _ := cmd.Flags().GetBool("allow-db-only")
			if path == "" {
				return fmt.Errorf("--path is required")
			}
			// Client-side guard: this command NEVER matches a pattern. The server
			// resolves an exact row; a '*' or '?' here is a mistake, not a selector.
			if strings.ContainsAny(path, "*?") {
				return fmt.Errorf("--path must be an exact document path, not a pattern (no '*' or '?')")
			}

			// 1. Resolve to EXACTLY one document (exact by-path lookup). 404 => 0
			//    matches => refuse loudly, never a silent no-op.
			rawGet, st, err := apiCall("GET", "/knowledge/documents/by-path/"+escapePath(path), nil)
			if err != nil {
				return err
			}
			if st == 404 {
				return fmt.Errorf("no document at path %q — delete resolves an EXACT path (no globs); run `blenau docs list --prefix ...` to find it", path)
			}
			if st >= 400 {
				return apiDetailError(rawGet)
			}
			var doc struct {
				Path       string `json:"path"`
				Title      string `json:"title"`
				SourceType string `json:"source_type"`
			}
			if err := json.Unmarshal(rawGet, &doc); err != nil || doc.Path == "" {
				return fmt.Errorf("could not parse the resolved document (unexpected response)")
			}

			// 2. Echo the RESOLVED identity (what will actually be deleted).
			//    This is prose for the human about to confirm; in machine mode it
			//    is suppressed (the caller named the path, and --json promises
			//    JSON and nothing else) — never the confirmation itself, which
			//    still fails closed below.
			errw := cmd.ErrOrStderr()
			if !machineOutput {
				fmt.Fprintf(errw, "Document to delete:\n")
				fmt.Fprintf(errw, "  path:        %s\n", norm.NFC.String(doc.Path))
				fmt.Fprintf(errw, "  title:       %s\n", norm.NFC.String(doc.Title))
				fmt.Fprintf(errw, "  source_type: %s\n", norm.NFC.String(doc.SourceType))
			}

			// 3. Dry-run: report the plan, change nothing.
			if dryRun {
				body, _ := json.Marshal(map[string]interface{}{
					"path": doc.Path, "allow_db_only": allowDBOnly, "dry_run": true,
				})
				raw, status, err := apiCall("POST", "/knowledge/delete-document", body)
				if err != nil {
					return err
				}
				// A dry-run can still hit a fail-closed 409/502 (open PR, index-only
				// without --allow-db-only, GitHub unreachable) — the exact thing the
				// preview exists to surface. Format its object-shaped detail the same
				// way the real delete does, instead of dumping raw JSON.
				if status >= 400 {
					return apiDetailError(raw)
				}
				return emitOrFail(cmd, raw, status, func(b []byte) error {
					var p struct {
						FileOnDefaultBranch bool `json:"file_on_default_branch"`
						WouldDeleteGitHub   bool `json:"would_delete_github"`
						DBOnly              bool `json:"db_only"`
					}
					_ = json.Unmarshal(b, &p)
					if p.WouldDeleteGitHub {
						fmt.Fprintf(errw, "\nDry run: would delete the file on GitHub, then remove the index (recoverable via git).\n")
					} else {
						fmt.Fprintf(errw, "\nDry run: no file on the default branch — this is an index-only (irrecoverable) delete; needs --allow-db-only.\n")
					}
					return nil
				})
			}

			// 4. Confirmation. Re-type the exact path, or --yes. Non-TTY without
			//    --yes fails closed.
			if !yes {
				if !isTerminal(os.Stdin) {
					return fmt.Errorf("refusing to delete in a non-interactive shell without --yes (pass --yes to confirm %q)", doc.Path)
				}
				fmt.Fprintf(errw, "\nRe-type the path to confirm deletion (or Ctrl-C to abort):\n> ")
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				// Compare against the SAME NFC-normalized value the operator was
				// shown (the echo above uses norm.NFC), or a non-NFC stored path
				// (e.g. an NFD filename synced from macOS) could never be confirmed.
				if strings.TrimSpace(line) != norm.NFC.String(doc.Path) {
					return fmt.Errorf("confirmation did not match %q — aborted", norm.NFC.String(doc.Path))
				}
			}

			// 5. Delete — send the RESOLVED canonical path.
			body, _ := json.Marshal(map[string]interface{}{
				"path": doc.Path, "allow_db_only": allowDBOnly,
			})
			raw, status, err := apiCall("POST", "/knowledge/delete-document", body)
			if err != nil {
				return err
			}
			if status >= 400 {
				return apiDetailError(raw)
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				var r struct {
					CommitSha  string `json:"commit_sha"`
					DBOnly     bool   `json:"db_only"`
					RevertHint string `json:"revert_hint"`
					Repo       string `json:"repo"`
				}
				_ = json.Unmarshal(b, &r)
				w := cmd.OutOrStdout()
				fmt.Fprintf(w, "Deleted %s\n", norm.NFC.String(doc.Path))
				if r.DBOnly {
					fmt.Fprintln(w, "  (index-only delete — irrecoverable; no backing file existed)")
				} else if r.CommitSha != "" {
					fmt.Fprintf(w, "  commit: %s\n", norm.NFC.String(r.CommitSha))
					if r.RevertHint != "" {
						fmt.Fprintf(w, "  recover: %s\n", norm.NFC.String(r.RevertHint))
					}
				}
				return nil
			})
		},
	}
	c.Flags().String("path", "", "Exact document path to delete (no globs).")
	c.Flags().Bool("yes", false, "Skip the interactive confirmation (required in non-interactive shells).")
	c.Flags().Bool("dry-run", false, "Show what would happen without deleting anything.")
	c.Flags().Bool("allow-db-only", false, "Confirm an irrecoverable index-only delete (doc with no backing file).")
	c.Flags().Bool("json", false, "Emit JSON instead of human format.")
	return c
}

// apiDetailError formats a non-2xx API body whose "detail" may be a plain string
// OR an object carrying {error, message, ...} (the delete endpoint's 409/502
// shapes), so the user gets the actionable message rather than raw JSON.
func apiDetailError(raw []byte) error {
	var m map[string]interface{}
	if json.Unmarshal(raw, &m) == nil {
		switch d := m["detail"].(type) {
		case string:
			if d != "" {
				return fmt.Errorf("%s", norm.NFC.String(d))
			}
		case map[string]interface{}:
			msg, _ := d["message"].(string)
			if msg == "" {
				msg, _ = d["error"].(string)
			}
			if sa, ok := d["suggested_action"].(string); ok && sa != "" {
				return fmt.Errorf("%s (%s)", norm.NFC.String(msg), norm.NFC.String(sa))
			}
			if msg != "" {
				return fmt.Errorf("%s", norm.NFC.String(msg))
			}
		}
	}
	return fmt.Errorf("%s", strings.TrimSpace(string(norm.NFC.Bytes(raw))))
}
