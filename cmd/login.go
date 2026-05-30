package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewLoginCmd builds `blenau login`.
func NewLoginCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "login",
		Short: "Save an API token to local config.",
		Long:  "Save a Blenau API token to the local CLI config file (mode 0600).",
		RunE: func(cmd *cobra.Command, args []string) error {
			token, _ := cmd.Flags().GetString("token")
			if token == "" {
				return fmt.Errorf("--token is required.\n" +
					"Browser-based login coming in a later release. Use --token <tk> for now.\n" +
					"See: https://docs.blenau.com/cli/")
			}
			cfg := &Config{APIURL: DefaultAPIURL, Token: token}
			path, err := SaveConfig(cfg)
			if err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Saved token to %s\n", path)
			return nil
		},
	}
	c.Flags().String("token", "", "Blenau API token (blenau_tk_...)")
	return c
}
