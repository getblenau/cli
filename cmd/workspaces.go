package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"golang.org/x/text/unicode/norm"
)

// NewWorkspacesCmd builds `blenau workspaces` (also serves as whoami).
func NewWorkspacesCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "workspaces",
		Aliases: []string{"whoami"},
		Short:   "List the workspaces your credential can reach (and which is active).",
		Long: `List every workspace your credential can reach, with your role and whether you
can write in each. The "active" one is where reads/writes land right now; pass a
different workspace UUID to any command with --workspace (identity tokens only)
to roam there. A workspace-pinned token (blenau_tk_) sees only its one workspace.

This doubles as "whoami": it shows the identity behind the current token and its
effective role per workspace.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, status, err := apiCall("GET", "/workspaces", nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				var resp struct {
					Active     string `json:"active"`
					Default    string `json:"default"`
					Workspaces []struct {
						ID       string `json:"id"`
						Slug     string `json:"slug"`
						Name     string `json:"name"`
						Role     string `json:"role"`
						CanWrite bool   `json:"can_write"`
						IsActive bool   `json:"is_active"`
					} `json:"workspaces"`
				}
				if err := json.Unmarshal(b, &resp); err != nil {
					cmd.OutOrStdout().Write(norm.NFC.Bytes(b))
					return nil
				}
				w := cmd.OutOrStdout()
				if len(resp.Workspaces) == 0 {
					fmt.Fprintln(w, "No workspaces.")
					return nil
				}
				fmt.Fprintf(w, "%-2s %-38s %-20s %-8s %s\n", "", "ID", "SLUG", "ROLE", "WRITE")
				for _, ws := range resp.Workspaces {
					marker := "  "
					if ws.IsActive || ws.ID == resp.Active {
						marker = "* "
					}
					write := "ro"
					if ws.CanWrite {
						write = "rw"
					}
					fmt.Fprintf(w, "%s%-38s %-20s %-8s %s\n",
						marker, norm.NFC.String(ws.ID), norm.NFC.String(ws.Slug),
						norm.NFC.String(ws.Role), write)
				}
				fmt.Fprintln(w, "\n* = active (where reads/writes land). Roam with --workspace <id>.")
				return nil
			})
		},
	}
	c.Flags().Bool("json", false, "Emit JSON instead of human format.")
	return c
}
