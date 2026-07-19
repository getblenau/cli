package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"golang.org/x/text/unicode/norm"
)

// NewWorkspacesCmd builds `blenau workspaces` — discovery of the workspaces the
// current credential can reach (SPEC 3 §2). For a token-pinned CLI this is the
// single membership of the token; it becomes multi-workspace once the identity
// (device-flow) lane lands (Phase 2).
func NewWorkspacesCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "workspaces",
		Short: "List the workspaces your credential can reach.",
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, status, err := apiCall("GET", "/workspaces", nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				var resp struct {
					Active     string `json:"active"`
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
				fmt.Fprintf(w, "%-2s %-20s %-8s %-9s %s\n", "", "SLUG", "ROLE", "CAN_WRITE", "NAME")
				for _, ws := range resp.Workspaces {
					marker := " "
					if ws.IsActive {
						marker = "*"
					}
					fmt.Fprintf(w, "%-2s %-20s %-8s %-9v %s\n",
						marker,
						norm.NFC.String(ws.Slug),
						ws.Role,
						ws.CanWrite,
						norm.NFC.String(ws.Name))
				}
				return nil
			})
		},
	}
	c.Flags().Bool("json", false, "Emit JSON instead of human format.")
	return c
}
