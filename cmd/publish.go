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

// publication is the admin-lane view of one exposed document.
type publication struct {
	ID                  string `json:"id"`
	PublishedPath       string `json:"published_path"`
	CurrentPath         string `json:"current_path"`
	Title               string `json:"title"`
	Visibility          string `json:"visibility"`
	URL                 string `json:"url"`
	PublishedAt         string `json:"published_at"`
	RevokedAt           string `json:"revoked_at"`
	ChangedSincePublish bool   `json:"changed_since_published"`
	MovedSincePublish   bool   `json:"moved_since_published"`
}

// resolveDocID turns an exact document path into its id. Publishing addresses a
// document, never a pattern: exposing the wrong file to the internet is not the
// kind of mistake a glob should be able to make.
func resolveDocID(path string) (string, string, error) {
	if strings.ContainsAny(path, "*?") {
		return "", "", fmt.Errorf("--path must be an exact document path, not a pattern (no '*' or '?')")
	}
	raw, st, err := apiCall("GET", "/knowledge/documents/by-path/"+escapePath(path), nil)
	if err != nil {
		return "", "", err
	}
	if st == 404 {
		return "", "", fmt.Errorf("no document at path %q — run `blenau docs list --prefix ...` to find it", path)
	}
	if st >= 400 {
		return "", "", apiDetailError(raw)
	}
	var doc struct {
		ID    string `json:"id"`
		Path  string `json:"path"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || doc.ID == "" {
		return "", "", fmt.Errorf("could not parse the resolved document (unexpected response)")
	}
	return doc.ID, doc.Title, nil
}

// findLivePublication locates the live publication for a path, so revoke and
// rotate-key can be addressed the same way as everything else in this CLI —
// by the document you know, not by an id you would have to go and look up.
func findLivePublication(path string) (*publication, error) {
	raw, st, err := apiCall("GET", "/publications", nil)
	if err != nil {
		return nil, err
	}
	if st >= 400 {
		return nil, apiDetailError(raw)
	}
	var body struct {
		Publications []publication `json:"publications"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("could not parse the publication list (unexpected response)")
	}
	for i := range body.Publications {
		p := &body.Publications[i]
		if p.RevokedAt != "" {
			continue
		}
		if p.CurrentPath == path || p.PublishedPath == path {
			return p, nil
		}
	}
	return nil, fmt.Errorf("no live publication for %q — run `blenau publish list` to see what is exposed", path)
}

func printPublication(w *os.File, p publication) {
	fmt.Fprintf(w, "  %s\n", norm.NFC.String(p.URL))
}

// NewPublishCmd builds `blenau publish` and its subcommands.
//
// Publishing puts a document in front of people outside the workspace, so this
// command is written to make that impossible to do by accident: it resolves an
// exact path, echoes the document and the resulting URL before acting, defaults
// to the narrower (unlisted) exposure, and refuses to make something indexable
// in a non-interactive shell without --yes.
func NewPublishCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "publish",
		Short: "Publish a document to a public URL, or manage what is already published.",
		Long: `Expose one document to readers outside the workspace.

Two kinds of exposure, both readable with no Blenau account:

  --link    (default) reachable only by someone holding the URL, which carries a
            secret key. Search engines are told not to index it.
  --public  reachable by anyone and indexable. Use this for material you WANT
            people to find, like a user manual.

Promoting an unlisted document to public never changes its address, so links you
have already sent keep working. The address is the document's path at the moment
you publish it: reorganising your repo afterwards does not break it.

Publishing is an admin decision and cannot be done by an agent.

Examples:
  blenau publish --path manuals/getting-started.md
  blenau publish --path manuals/getting-started.md --public
  blenau publish list
  blenau publish status --path manuals/getting-started.md
  blenau publish revoke --path manuals/getting-started.md
  blenau publish rotate-key --path manuals/getting-started.md`,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, _ := cmd.Flags().GetString("path")
			public, _ := cmd.Flags().GetBool("public")
			yes, _ := cmd.Flags().GetBool("yes")
			if path == "" {
				return fmt.Errorf("--path is required")
			}
			docID, title, err := resolveDocID(path)
			if err != nil {
				return err
			}

			visibility := "link"
			if public {
				visibility = "public"
			}

			errw := cmd.ErrOrStderr()
			fmt.Fprintf(errw, "Document to publish:\n")
			fmt.Fprintf(errw, "  path:  %s\n", norm.NFC.String(path))
			fmt.Fprintf(errw, "  title: %s\n", norm.NFC.String(title))

			// Only `--public` needs confirming. An unlisted link is reversible in
			// practice (revoke, or rotate the key) and reaches only whoever you
			// send it to; an indexed page is copied by crawlers within hours and
			// no amount of revoking un-copies it.
			if public && !yes {
				if !isTerminal(os.Stdin) {
					return fmt.Errorf("refusing to publish %q as indexable in a non-interactive shell without --yes", path)
				}
				fmt.Fprintf(errw, "\nThis makes the document readable by anyone and indexable by search engines.\n")
				fmt.Fprintf(errw, "Type 'public' to confirm (or Ctrl-C to abort):\n> ")
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				if strings.TrimSpace(line) != "public" {
					return fmt.Errorf("confirmation did not match — aborted")
				}
			}

			body, _ := json.Marshal(map[string]interface{}{"visibility": visibility})
			raw, status, err := apiCall("POST", "/documents/"+docID+"/publish", body)
			if err != nil {
				return err
			}
			if status >= 400 {
				return apiDetailError(raw)
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				var p publication
				_ = json.Unmarshal(b, &p)
				w := cmd.OutOrStdout()
				if p.Visibility == "public" {
					fmt.Fprintf(w, "Published (public, indexable):\n")
				} else {
					fmt.Fprintf(w, "Published (anyone with the link):\n")
				}
				fmt.Fprintf(w, "  %s\n", norm.NFC.String(p.URL))
				if p.Visibility == "link" {
					fmt.Fprintf(w, "\nThe key in that URL is what makes it work — share the whole address.\n")
					fmt.Fprintf(w, "Run `blenau publish list` to read it again later.\n")
				}
				return nil
			})
		},
	}
	c.Flags().String("path", "", "Exact document path to publish (no globs).")
	c.Flags().Bool("public", false, "Make it readable by anyone AND indexable, instead of link-only.")
	c.Flags().Bool("yes", false, "Skip the confirmation required by --public.")
	c.Flags().Bool("json", false, "Emit JSON instead of human format.")

	c.AddCommand(newPublishListCmd())
	c.AddCommand(newPublishStatusCmd())
	c.AddCommand(newPublishRevokeCmd())
	c.AddCommand(newPublishRotateKeyCmd())
	return c
}

func newPublishListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "list",
		Short: "List every document this workspace has exposed to the internet.",
		Long: `Show what of yours is readable from outside, with the exact URL for each.

This is also where you re-read the key of an unlisted link when someone asks for
it again — rotating the key would break every copy already sent, so it is a
separate, deliberate command.

Two flags worth reading in the output:
  changed  the document was edited after it was published; nobody re-approved
           what is now being served.
  moved    the document was moved in the repo; the published address still
           works and now differs from where the document lives.

Examples:
  blenau publish list
  blenau publish list --include-revoked`,
		RunE: func(cmd *cobra.Command, args []string) error {
			includeRevoked, _ := cmd.Flags().GetBool("include-revoked")
			p := "/publications"
			if includeRevoked {
				p += "?include_revoked=true"
			}
			raw, status, err := apiCall("GET", p, nil)
			if err != nil {
				return err
			}
			if status >= 400 {
				return apiDetailError(raw)
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				var body struct {
					Publications []publication `json:"publications"`
				}
				_ = json.Unmarshal(b, &body)
				w := cmd.OutOrStdout()
				if len(body.Publications) == 0 {
					fmt.Fprintln(w, "Nothing is published.")
					return nil
				}
				for _, p := range body.Publications {
					state := p.Visibility
					if p.RevokedAt != "" {
						state = "revoked"
					}
					fmt.Fprintf(w, "%-8s %s\n", state, norm.NFC.String(p.Title))
					fmt.Fprintf(w, "         %s\n", norm.NFC.String(p.URL))
					var notes []string
					if p.ChangedSincePublish {
						notes = append(notes, "changed since published")
					}
					if p.MovedSincePublish {
						notes = append(notes, "moved to "+norm.NFC.String(p.CurrentPath))
					}
					if len(notes) > 0 {
						fmt.Fprintf(w, "         (%s)\n", strings.Join(notes, "; "))
					}
				}
				return nil
			})
		},
	}
	c.Flags().Bool("include-revoked", false, "Also show publications that have been taken down.")
	c.Flags().Bool("json", false, "Emit JSON instead of human format.")
	return c
}

func newPublishStatusCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "status",
		Short: "Check whether one document is readable from the internet.",
		Long: `Answer 'is this exposed?' for a single document.

Unlike ` + "`blenau publish list`" + `, this never returns the secret key of an
unlisted link, so it is safe to run anywhere and is the same view an agent gets.

Examples:
  blenau publish status --path manuals/getting-started.md`,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, _ := cmd.Flags().GetString("path")
			if path == "" {
				return fmt.Errorf("--path is required")
			}
			raw, status, err := apiCall("GET", "/publication-status?path="+escapePath(path), nil)
			if err != nil {
				return err
			}
			if status >= 400 {
				return apiDetailError(raw)
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				var s struct {
					Published   bool   `json:"published"`
					Visibility  string `json:"visibility"`
					Address     string `json:"address"`
					RequiresKey bool   `json:"requires_key"`
				}
				_ = json.Unmarshal(b, &s)
				w := cmd.OutOrStdout()
				if !s.Published {
					fmt.Fprintf(w, "Not published.\n")
					return nil
				}
				if s.Visibility == "public" {
					fmt.Fprintf(w, "Public and indexable:\n  %s\n", norm.NFC.String(s.Address))
				} else {
					fmt.Fprintf(w, "Shared by link:\n  %s\n", norm.NFC.String(s.Address))
					fmt.Fprintf(w, "  (needs its key — `blenau publish list` prints the full URL)\n")
				}
				return nil
			})
		},
	}
	c.Flags().String("path", "", "Exact document path to check.")
	c.Flags().Bool("json", false, "Emit JSON instead of human format.")
	return c
}

func newPublishRevokeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "revoke",
		Short: "Take a published document off the internet.",
		Long: `Stop serving a published document. The URL starts answering 404.

The record of what was exposed, by whom and for how long is kept — revoking is a
state change, not a delete. The address stays reserved, so re-publishing later
gets a fresh one and the old link stays dead rather than resurrecting under
different content.

A document that was indexed may still appear in search results until the engine
re-crawls it; revoking stops Blenau serving it, not Google remembering it.

Examples:
  blenau publish revoke --path manuals/getting-started.md
  blenau publish revoke --path manuals/getting-started.md --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, _ := cmd.Flags().GetString("path")
			yes, _ := cmd.Flags().GetBool("yes")
			if path == "" {
				return fmt.Errorf("--path is required")
			}
			p, err := findLivePublication(path)
			if err != nil {
				return err
			}
			errw := cmd.ErrOrStderr()
			fmt.Fprintf(errw, "Publication to revoke:\n")
			fmt.Fprintf(errw, "  title: %s\n", norm.NFC.String(p.Title))
			fmt.Fprintf(errw, "  url:   %s\n", norm.NFC.String(p.URL))
			if !yes {
				if !isTerminal(os.Stdin) {
					return fmt.Errorf("refusing to revoke in a non-interactive shell without --yes")
				}
				fmt.Fprintf(errw, "\nAnyone holding this link will get a 404. Type 'revoke' to confirm:\n> ")
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				if strings.TrimSpace(line) != "revoke" {
					return fmt.Errorf("confirmation did not match — aborted")
				}
			}
			raw, status, err := apiCall("POST", "/publications/"+p.ID+"/revoke", nil)
			if err != nil {
				return err
			}
			if status >= 400 {
				return apiDetailError(raw)
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				fmt.Fprintf(cmd.OutOrStdout(), "Revoked. %s now returns 404.\n",
					norm.NFC.String(strings.Split(p.URL, "?")[0]))
				return nil
			})
		},
	}
	c.Flags().String("path", "", "Exact document path whose publication to revoke.")
	c.Flags().Bool("yes", false, "Skip the interactive confirmation.")
	c.Flags().Bool("json", false, "Emit JSON instead of human format.")
	return c
}

func newPublishRotateKeyCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "rotate-key",
		Short: "Invalidate every unlisted link shared so far, keeping the same address.",
		Long: `Replace the secret key in an unlisted document's URL.

This is the one operation here that BREAKS links you have already sent — that is
its purpose, for when a link reached somebody it should not have. The address
does not change, so re-sharing means sending the new URL to the people who
should still have it.

Nothing else breaks a distributed link: publishing, promoting to public and
editing all leave it working.

Examples:
  blenau publish rotate-key --path manuals/getting-started.md`,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, _ := cmd.Flags().GetString("path")
			yes, _ := cmd.Flags().GetBool("yes")
			if path == "" {
				return fmt.Errorf("--path is required")
			}
			p, err := findLivePublication(path)
			if err != nil {
				return err
			}
			errw := cmd.ErrOrStderr()
			fmt.Fprintf(errw, "Rotating the key for: %s\n", norm.NFC.String(p.Title))
			if !yes {
				if !isTerminal(os.Stdin) {
					return fmt.Errorf("refusing to rotate in a non-interactive shell without --yes")
				}
				fmt.Fprintf(errw, "\nEvery link shared so far stops working. Type 'rotate' to confirm:\n> ")
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				if strings.TrimSpace(line) != "rotate" {
					return fmt.Errorf("confirmation did not match — aborted")
				}
			}
			raw, status, err := apiCall("POST", "/publications/"+p.ID+"/rotate-key", nil)
			if err != nil {
				return err
			}
			if status >= 400 {
				return apiDetailError(raw)
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				var np publication
				_ = json.Unmarshal(b, &np)
				w := cmd.OutOrStdout()
				fmt.Fprintf(w, "Key rotated. The new link is:\n  %s\n", norm.NFC.String(np.URL))
				fmt.Fprintf(w, "\nEvery previously shared link now returns 404.\n")
				return nil
			})
		},
	}
	c.Flags().String("path", "", "Exact document path whose key to rotate.")
	c.Flags().Bool("yes", false, "Skip the interactive confirmation.")
	c.Flags().Bool("json", false, "Emit JSON instead of human format.")
	return c
}
