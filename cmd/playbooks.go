package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/text/unicode/norm"
)

// Where `playbooks install` puts a file when nobody says otherwise, in order.
// These are the directories an agent already reads on its own: dropping the
// playbook anywhere else means a human has to remember to point at it, which is
// the step this command exists to remove.
var installDirCandidates = []string{
	filepath.Join(".claude", "commands"),
	filepath.Join(".agents", "workflows"),
}

// NewPlaybooksCmd builds `blenau playbooks ...`. Bare `blenau playbooks` lists,
// matching `blenau repos`.
func NewPlaybooksCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "playbooks",
		Short: "List, print and install Blenau's procedures for common jobs.",
		Long: `Blenau ships procedures for the jobs it expects you to do: migrating an existing
manual or export into the brain, taking an empty workspace to its first
documents, and working a brain-health report down to zero.

A playbook is Markdown meant to be handed to an agent whole. 'get' prints it to
stdout (so you can pipe or redirect it), 'install' writes it where your agent
already looks. Both arrive filled in with THIS workspace's slug and the repos
you may write to, so the paths in them are real — pass --generic for the
placeholder version.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return playbooksList(cmd)
		},
	}
	c.PersistentFlags().Bool("json", false, "Emit JSON instead of human format.")
	c.AddCommand(newPlaybooksListCmd())
	c.AddCommand(newPlaybooksGetCmd())
	c.AddCommand(newPlaybooksInstallCmd())
	return c
}

func newPlaybooksListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the available playbooks and when to use each.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return playbooksList(cmd)
		},
	}
}

func playbooksList(cmd *cobra.Command) error {
	raw, status, err := apiCall("GET", "/playbooks", nil)
	if err != nil {
		return err
	}
	return emitOrFail(cmd, raw, status, func(b []byte) error {
		var resp struct {
			Playbooks []struct {
				ID        string `json:"id"`
				Title     string `json:"title"`
				WhenToUse string `json:"when_to_use"`
			} `json:"playbooks"`
		}
		if err := json.Unmarshal(b, &resp); err != nil {
			cmd.OutOrStdout().Write(norm.NFC.Bytes(b))
			return nil
		}
		w := cmd.OutOrStdout()
		if len(resp.Playbooks) == 0 {
			fmt.Fprintln(w, "No playbooks.")
			return nil
		}
		for _, p := range resp.Playbooks {
			fmt.Fprintf(w, "%s\n    %s\n    %s\n\n",
				norm.NFC.String(p.ID),
				norm.NFC.String(p.Title),
				norm.NFC.String(p.WhenToUse))
		}
		fmt.Fprintln(w, "Print one:   blenau playbooks get <id>")
		fmt.Fprintln(w, "Install one: blenau playbooks install <id>")
		return nil
	})
}

// playbookPath picks the endpoint. The rendered lane is the default because a
// playbook whose destinations are placeholders is a procedure its reader still
// has to finish by hand.
func playbookPath(id string, generic bool, raw bool) string {
	suffix := "/rendered"
	if generic {
		suffix = ""
	}
	p := "/playbooks/" + id + suffix
	if raw {
		p += "?format=raw"
	}
	return p
}

func newPlaybooksGetCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "get <playbook-id>",
		Short: "Print one playbook as Markdown, ready to hand to an agent.",
		Long: `Print a playbook to stdout as Markdown.

Unlike the other commands, the default output here stays Markdown even when
stdout is a pipe — redirecting it to a file is the normal way to use it
('blenau playbooks get migrate-markdown-export > migrate.md'), and a table would
be useless there while JSON would be the wrong artefact. Pass --json explicitly
for the metadata envelope.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			generic, _ := cmd.Flags().GetBool("generic")
			asJSON, _ := cmd.Flags().GetBool("json")
			// Explicit --json only. jsonFlag() would flip to JSON whenever
			// stdout is not a TTY, which is exactly when the caller wants the
			// Markdown.
			wantJSON := asJSON && cmd.Flags().Changed("json")

			raw, status, err := apiCall("GET", playbookPath(args[0], generic, !wantJSON), nil)
			if err != nil {
				return err
			}
			if status >= 400 {
				return failFromResponse(raw)
			}
			out := norm.NFC.Bytes(raw)
			cmd.OutOrStdout().Write(out)
			if len(out) > 0 && out[len(out)-1] != '\n' {
				fmt.Fprintln(cmd.OutOrStdout())
			}
			return nil
		},
	}
	c.Flags().Bool("generic", false, "Placeholder version, without this workspace's repos.")
	return c
}

func newPlaybooksInstallCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "install <playbook-id>",
		Short: "Write a playbook where your agent already looks for instructions.",
		Long: `Save a playbook as a Markdown file.

Without --dir it goes to '.claude/commands' or '.agents/workflows' if either
exists in the current directory, and to the current directory otherwise. The
resolved path is always printed, so the destination is never a guess.

An existing file is NOT overwritten without --force: a playbook you have edited
for your own workspace is worth more than the shipped one, and silently
replacing it is the kind of loss nobody notices until they need the edit.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			generic, _ := cmd.Flags().GetBool("generic")
			force, _ := cmd.Flags().GetBool("force")
			dir, _ := cmd.Flags().GetString("dir")

			if dir == "" {
				dir = resolveInstallDir()
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("could not create %s: %w", dir, err)
			}
			dest := filepath.Join(dir, id+".md")
			if _, err := os.Stat(dest); err == nil && !force {
				return fmt.Errorf("%s already exists; pass --force to overwrite it", dest)
			}

			raw, status, err := apiCall("GET", playbookPath(id, generic, true), nil)
			if err != nil {
				return err
			}
			if status >= 400 {
				return failFromResponse(raw)
			}
			if err := os.WriteFile(dest, raw, 0o644); err != nil {
				return fmt.Errorf("could not write %s: %w", dest, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s (%d bytes).\nHand it to your agent whole.\n",
				dest, len(raw))
			return nil
		},
	}
	c.Flags().String("dir", "", "Directory to write into (default: an agent instructions dir, else '.').")
	c.Flags().Bool("generic", false, "Placeholder version, without this workspace's repos.")
	c.Flags().Bool("force", false, "Overwrite an existing file.")
	return c
}

func resolveInstallDir() string {
	for _, d := range installDirCandidates {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d
		}
	}
	return "."
}

// failFromResponse turns an API error envelope into a CLI error. `emitOrFail`
// cannot be reused here: it exits the process after printing, and these two
// commands need to return so cobra reports the failure through the same path as
// every other error (and so tests can assert on it).
func failFromResponse(raw []byte) error {
	var m map[string]interface{}
	msg := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &m) == nil {
		if d, ok := m["detail"].(string); ok && d != "" {
			msg = d
		} else if e, ok := m["error"].(string); ok && e != "" {
			msg = e
		}
	}
	return fmt.Errorf("%s", norm.NFC.String(msg))
}
