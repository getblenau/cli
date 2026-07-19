package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewLogoutCmd builds `blenau logout`.
func NewLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Log out of the browser session (revokes and clears the keychain).",
		RunE: func(cmd *cobra.Command, args []string) error {
			if serviceToken() != "" {
				fmt.Fprintln(cmd.ErrOrStderr(),
					"You're using a service token (env/config), not a browser login — nothing to log out.")
				return nil
			}
			return identityLogout(cmd.ErrOrStderr())
		},
	}
}

// NewWhoamiCmd builds `blenau whoami` — the identity + reachable workspaces,
// resolved via GET /workspaces (not from token claims).
func NewWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show who you are and which workspaces you can reach.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := fetchWorkspaces()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			lane := "browser login"
			if serviceToken() != "" {
				lane = "service token"
			}
			active := ""
			if cfg, _ := LoadConfig(); cfg != nil && cfg.ActiveWorkspace != nil {
				active = cfg.ActiveWorkspace.Slug
			}
			fmt.Fprintf(out, "Authenticated via %s. %d workspace(s) reachable.\n", lane, len(ws))
			for _, w := range ws {
				marker := " "
				if w.Slug == active || (active == "" && w.IsActive) {
					marker = "*"
				}
				fmt.Fprintf(out, "  %s %s (%s)\n", marker, w.Slug, w.Role)
			}
			return nil
		},
	}
}

// NewStatusCmd builds `blenau status`.
func NewStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the current auth lane, API URL and active workspace.",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "API:  %s\n", resolveAPIURL())
			if serviceToken() != "" {
				fmt.Fprintln(out, "Auth: service token (workspace pinned by the token)")
				return nil
			}
			if ok, _ := identityConfigured(); ok {
				fmt.Fprintln(out, "Auth: browser login (identity lane)")
				if cfg, _ := LoadConfig(); cfg != nil && cfg.ActiveWorkspace != nil {
					fmt.Fprintf(out, "Active workspace: %s (%s)\n", cfg.ActiveWorkspace.Slug, cfg.ActiveWorkspace.Name)
				} else {
					fmt.Fprintln(out, "Active workspace: (none set — `blenau use <slug>` to pick one)")
				}
				return nil
			}
			fmt.Fprintln(out, "Auth: not logged in (run `blenau login` or `blenau login --token <tk>`)")
			return nil
		},
	}
}

// NewUseCmd builds `blenau use <slug|id>` — set the active workspace (writes go
// here; reads default here). Identity lane only.
func NewUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <slug|id>",
		Short: "Set the active workspace (browser login only).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if serviceToken() != "" {
				return fmt.Errorf("workspace is pinned by your service token; `use` applies to browser login only")
			}
			ref, err := resolveWorkspaceRef(args[0])
			if err != nil {
				return err
			}
			cfg, err := LoadConfig()
			if err != nil || cfg == nil {
				cfg = &Config{APIURL: DefaultAPIURL}
			}
			if cfg.APIURL == "" {
				cfg.APIURL = DefaultAPIURL
			}
			cfg.ActiveWorkspace = ref
			if _, err := SaveConfig(cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Active workspace: %s (%s)\n", ref.Slug, ref.Name)
			return nil
		},
	}
}
