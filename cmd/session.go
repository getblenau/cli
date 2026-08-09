package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

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
	c := &cobra.Command{
		Use: "status",
		// The Short is what an agent reads in `--agent-manifest` and what a
		// human sees in the root listing, so it has to name --server: a
		// capability nobody can see is one nobody uses.
		Short: "Show the auth lane, API URL and workspace; --server adds API health.",
		Long: `Show how this CLI is configured. Local and offline by default: it reads your
config and prints it, making no network call.

--server also asks the API how IT is doing: version, database, and which GitHub
webhook events the Blenau App is subscribed to.

That last one is worth knowing. Blenau heals a repo rename automatically only
if the App is subscribed to the 'repository' event; when it is not, a rename
stops a repo's sync silently and the only fix is
'blenau repos update <id> --repo <org/new-name>'. --server reports it as
rename_autoheal so you never have to guess.

Examples:
  blenau status                 # offline, instant
  blenau status --server        # + API version and webhook subscriptions
  blenau status --server --json # same, machine-readable`,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			withServer, _ := cmd.Flags().GetBool("server")

			// The local half runs first and always: if the API is unreachable
			// you still want to be told which lane and which URL you are on —
			// that is usually what explains the failure.
			fmt.Fprintf(out, "API:  %s\n", resolveAPIURL())
			switch {
			case serviceToken() != "":
				fmt.Fprintln(out, "Auth: service token (workspace pinned by the token)")
			case func() bool { ok, _ := identityConfigured(); return ok }():
				fmt.Fprintln(out, "Auth: browser login (identity lane)")
				if cfg, _ := LoadConfig(); cfg != nil && cfg.ActiveWorkspace != nil {
					fmt.Fprintf(out, "Active workspace: %s (%s)\n", cfg.ActiveWorkspace.Slug, cfg.ActiveWorkspace.Name)
				} else {
					fmt.Fprintln(out, "Active workspace: (none set — `blenau use <slug>` to pick one)")
				}
			default:
				fmt.Fprintln(out, "Auth: not logged in (run `blenau login` or `blenau login --token <tk>`)")
			}

			if !withServer {
				return nil
			}
			// Opt-in on purpose. `status` is what people run when something is
			// already wrong, often offline; making it always hit the network
			// would turn an instant local answer into a timeout.
			raw, code, err := apiCall("GET", "/health", nil)
			if err != nil {
				fmt.Fprintf(out, "\nServer: unreachable (%v)\n", err)
				return nil
			}
			return emitOrFail(cmd, raw, code, func(b []byte) error {
				var h struct {
					Status         string   `json:"status"`
					Version        string   `json:"version"`
					DB             string   `json:"db"`
					GithubApp      string   `json:"github_app"`
					GithubAppEvent []string `json:"github_app_events"`
					RenameAutoheal *bool    `json:"rename_autoheal"`
				}
				if err := json.Unmarshal(b, &h); err != nil {
					out.Write(b)
					return nil
				}
				fmt.Fprintf(out, "\nServer:  %s (%s)\n", h.Status, h.Version)
				fmt.Fprintf(out, "DB:      %s\n", h.DB)
				fmt.Fprintf(out, "GitHub:  %s\n", h.GithubApp)
				if len(h.GithubAppEvent) > 0 {
					fmt.Fprintf(out, "Events:  %s\n", strings.Join(h.GithubAppEvent, ", "))
				}
				switch {
				case h.RenameAutoheal == nil:
					fmt.Fprintln(out, "Rename auto-heal: unknown (could not read the App's subscriptions)")
				case *h.RenameAutoheal:
					fmt.Fprintln(out, "Rename auto-heal: yes — a renamed repo repairs itself")
				default:
					fmt.Fprintln(out, "Rename auto-heal: NO — subscribe the App to the 'repository'\n"+
						"  event, or a rename stops a repo's sync silently. Fix one with:\n"+
						"  blenau repos update <repo-id> --repo <org/new-name>")
				}
				return nil
			})
		},
	}
	c.Flags().Bool("server", false, "Also ask the API for its health and GitHub webhook subscriptions.")
	return c
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
