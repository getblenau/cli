package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"golang.org/x/text/unicode/norm"
)

// NewReposCmd builds `blenau repos`.
func NewReposCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "repos",
		Short: "List GitHub repos connected to the workspace.",
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, status, err := apiCall("GET", "/github/repos", nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				var resp struct {
					Repos []struct {
						Name       string `json:"name"`
						FullName   string `json:"full_name"`
						PathPrefix string `json:"path_prefix"`
						Private    bool   `json:"private"`
					} `json:"repos"`
				}
				if err := json.Unmarshal(b, &resp); err != nil {
					// Fallback: dump raw.
					cmd.OutOrStdout().Write(norm.NFC.Bytes(b))
					return nil
				}
				w := cmd.OutOrStdout()
				if len(resp.Repos) == 0 {
					fmt.Fprintln(w, "No repos.")
					return nil
				}
				fmt.Fprintf(w, "%-40s %-30s %s\n", "FULL_NAME", "PATH_PREFIX", "PRIVATE")
				for _, r := range resp.Repos {
					fmt.Fprintf(w, "%-40s %-30s %v\n",
						norm.NFC.String(r.FullName),
						norm.NFC.String(r.PathPrefix),
						r.Private)
				}
				return nil
			})
		},
	}
	c.Flags().Bool("json", false, "Emit JSON instead of human format.")
	return c
}
