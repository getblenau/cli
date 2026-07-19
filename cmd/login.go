package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewLoginCmd builds `blenau login`.
func NewLoginCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "login",
		Short: "Log in with your browser, or save an API token (--token).",
		Long: "Log in to Blenau.\n\n" +
			"  blenau login            Browser login (most secure; identity stored in the OS keychain).\n" +
			"  blenau login --token X  Save a service token blenau_tk_ (best for CI/automation).\n\n" +
			"See: https://docs.blenau.com/cli/",
		RunE: func(cmd *cobra.Command, args []string) error {
			token, _ := cmd.Flags().GetString("token")
			if token == "" {
				// Browser device flow — identity lane. Progress on stderr so
				// stdout stays clean for scripting.
				return loginDeviceFlow(cmd.ErrOrStderr())
			}
			// Merge, don't clobber: preserve a previously-set api_url (and any
			// future fields) instead of resetting the whole config on login.
			cfg, err := LoadConfig()
			if err != nil || cfg == nil {
				cfg = &Config{APIURL: DefaultAPIURL}
			}
			if cfg.APIURL == "" {
				cfg.APIURL = DefaultAPIURL
			}
			cfg.Token = token
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
