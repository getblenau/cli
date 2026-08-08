package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"
	"golang.org/x/text/unicode/norm"
)

// NewReposCmd builds `blenau repos ...`. Bare `blenau repos` lists (backward
// compatible); subcommands manage the connections.
func NewReposCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "repos",
		Short: "List and manage the GitHub repos connected to the workspace.",
		Long: `Connect GitHub repositories to a workspace so their markdown becomes searchable
knowledge, and route writes back to them. Each repo has a path_prefix that
namespaces its docs inside the brain (e.g. "api/"), so a write to "api/x.md"
lands in that repo. Bare 'blenau repos' lists them.

Non-admins see and manage only repos whose path_prefix falls inside their
allowed paths — enough for an agent to discover where it may write and close the
create -> connect -> ingest loop autonomously.`,
		// Backward-compatible: `blenau repos` still lists.
		RunE: func(cmd *cobra.Command, args []string) error {
			return reposList(cmd)
		},
	}
	c.PersistentFlags().Bool("json", false, "Emit JSON instead of human format.")
	c.AddCommand(newReposListCmd())
	c.AddCommand(newReposConnectCmd())
	c.AddCommand(newReposDisconnectCmd())
	c.AddCommand(newReposUpdateCmd())
	c.AddCommand(newReposAvailableCmd())
	c.AddCommand(newReposSyncCmd())
	return c
}

func reposList(cmd *cobra.Command) error {
	raw, status, err := apiCall("GET", "/github/repos", nil)
	if err != nil {
		return err
	}
	return emitOrFail(cmd, raw, status, func(b []byte) error {
		var resp struct {
			Repos []struct {
				ID         string `json:"id"`
				Repo       string `json:"repo"`
				FullName   string `json:"full_name"`
				PathPrefix string `json:"path_prefix"`
				Label      string `json:"label"`
				DocCount   int    `json:"doc_count"`
			} `json:"repos"`
		}
		if err := json.Unmarshal(b, &resp); err != nil {
			cmd.OutOrStdout().Write(norm.NFC.Bytes(b))
			return nil
		}
		w := cmd.OutOrStdout()
		if len(resp.Repos) == 0 {
			fmt.Fprintln(w, "No repos.")
			return nil
		}
		fmt.Fprintf(w, "%-38s %-30s %-24s %s\n", "ID", "REPO", "PATH_PREFIX", "DOCS")
		for _, r := range resp.Repos {
			name := r.Repo
			if name == "" {
				name = r.FullName
			}
			fmt.Fprintf(w, "%-38s %-30s %-24s %d\n",
				norm.NFC.String(r.ID), norm.NFC.String(name),
				norm.NFC.String(r.PathPrefix), r.DocCount)
		}
		return nil
	})
}

func newReposListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List connected repos with their path_prefix and doc counts.",
		RunE:  func(cmd *cobra.Command, args []string) error { return reposList(cmd) },
	}
}

func newReposConnectCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "connect",
		Short: "Connect a GitHub repo (needs the Blenau GitHub App installation id).",
		Long: `Connect a GitHub repo to this workspace and queue an initial sync of its
markdown. Requires the installation id of the Blenau GitHub App on that repo —
discover it (and the repos it can reach) with 'blenau repos available <id>'
after installing the app.

--path-prefix namespaces the repo's docs. Omit it to auto-derive (empty for the
first repo, "<repo-name>/" otherwise); pass --path-prefix "" only to force the
root namespace. A non-admin must pass an explicit prefix inside its allowed paths.

Example:
  blenau repos connect --repo acme/handbook --installation-id 12345678 --path-prefix handbook/`,
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, _ := cmd.Flags().GetString("repo")
			installID, _ := cmd.Flags().GetInt("installation-id")
			if repo == "" {
				return fmt.Errorf("--repo is required (org/name)")
			}
			if installID == 0 {
				return fmt.Errorf("--installation-id is required (see 'blenau repos available')")
			}
			body := map[string]interface{}{
				"repo_full_name":  repo,
				"installation_id": installID,
			}
			// Only send path_prefix when explicitly set, so omission means
			// server-side auto-derive while --path-prefix "" forces root.
			if cmd.Flags().Changed("path-prefix") {
				pp, _ := cmd.Flags().GetString("path-prefix")
				body["path_prefix"] = pp
			}
			if cmd.Flags().Changed("label") {
				label, _ := cmd.Flags().GetString("label")
				body["label"] = label
			}
			b, _ := json.Marshal(body)
			raw, status, err := apiCall("POST", "/github/repos", b)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
	c.Flags().String("repo", "", "Repo full name (org/name). REQUIRED.")
	c.Flags().Int("installation-id", 0, "Blenau GitHub App installation id. REQUIRED.")
	c.Flags().String("path-prefix", "", "Namespace for the repo's docs. Omit to auto-derive; \"\" = root.")
	c.Flags().String("label", "", "Human label for the connection.")
	_ = c.MarkFlagRequired("repo")
	_ = c.MarkFlagRequired("installation-id")
	return c
}

func newReposDisconnectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disconnect <repo-id>",
		Short: "Disconnect a repo (removes Blenau's record; the GitHub repo is untouched).",
		Long: `Disconnect a repo by its Blenau repo id (from 'blenau repos list'). This removes
Blenau's binding and stops syncing; the GitHub repository itself is never
modified.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, status, err := apiCall("DELETE", "/github/repos/"+url.PathEscape(args[0]), nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
}

func newReposUpdateCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "update <repo-id>",
		Short: "Change a connected repo's name, path_prefix and/or label.",
		Long: `Edit a connected repo (repo id from 'blenau repos list').

--repo is how a workspace survives a GitHub RENAME. If pushes stopped arriving
after the repo was renamed on GitHub, this is the fix — not disconnect and
reconnect, which is refused while the repo still owns documents. It updates the
connection and restamps every document's provenance in one transaction, after
checking the new name is readable by this workspace's installation.

Changing --path-prefix rewrites the path of every document that came from this
repo so history follows the new prefix; the whole change is one transaction and
fails (409) if any doc would collide with an existing path. Pass --path-prefix
"" to move the repo to the root namespace.

Examples:
  blenau repos update <repo-id> --repo acme/app-odoo
  blenau repos update <repo-id> --path-prefix docs/ --label "Main handbook"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]interface{}{}
			if cmd.Flags().Changed("path-prefix") {
				pp, _ := cmd.Flags().GetString("path-prefix")
				body["path_prefix"] = pp
			}
			if cmd.Flags().Changed("label") {
				label, _ := cmd.Flags().GetString("label")
				body["label"] = label
			}
			if cmd.Flags().Changed("repo") {
				r, _ := cmd.Flags().GetString("repo")
				body["repo"] = r
			}
			if len(body) == 0 {
				return fmt.Errorf("nothing to update: pass --repo, --path-prefix and/or --label")
			}
			b, _ := json.Marshal(body)
			raw, status, err := apiCall("PATCH", "/github/repos/"+url.PathEscape(args[0]), b)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
	c.Flags().String("path-prefix", "", "New namespace for the repo's docs (\"\" = root).")
	c.Flags().String("label", "", "New human label.")
	c.Flags().String("repo", "", "New full name on GitHub (org/name) after a rename.")
	return c
}

func newReposAvailableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "available <installation-id>",
		Short: "List repos reachable via a GitHub App installation (with connected flag).",
		Long: `List the GitHub repos reachable through a Blenau GitHub App installation, each
flagged whether it is already connected. This is the discovery step before
'blenau repos connect': install the app, then run this with the installation id
to see connectable repos. Admin only.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := "/github/installations/" + url.PathEscape(args[0]) + "/available-repos"
			raw, status, err := apiCall("GET", p, nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
}

func newReposSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync [repo-id]",
		Short: "Re-pull markdown from GitHub and re-index it (all repos, or one).",
		Long: `Re-pull every markdown file from the connected repos and re-ingest it, so the
brain matches GitHub after out-of-band changes. Pass a repo id to sync just that
one; omit it to sync all connected repos. Admin only.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/github/sync"
			if len(args) == 1 {
				path += "/" + url.PathEscape(args[0])
			}
			raw, status, err := apiCall("POST", path, nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
}
