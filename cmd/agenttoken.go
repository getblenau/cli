package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"golang.org/x/text/unicode/norm"
)

// NewAgentTokenCmd builds `blenau agent-token ...` — the lane for an EXTERNAL
// account to give its own AI agent read access to what that person can already
// read.
//
// It lives in the CLI rather than in MCP for the same reason `access` does: the
// device-flow login is the PERSON, with their real role. A token minted by a
// detached agent token would be a credential creating credentials, which is the
// one shape this lane exists to avoid.
//
// The tokens do not expire. That is deliberate and it is safe only because they
// are cut the moment the access behind them is withdrawn — see
// `revoke` below and the workspace owner's inventory.
func NewAgentTokenCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "agent-token",
		Short: "Create and manage tokens for your own AI agent.",
		Long: `A token lets your AI agent read exactly what you can read in this workspace —
no more. It is shown once, at creation.

Tokens do not expire. They stop working the moment your access does: if your
account is paused, or the person who admitted you loses their delegation, or the
workspace owner turns agent access off, every token you hold dies with it.

  blenau agent-token list
  blenau agent-token create "Claude Desktop"
  blenau agent-token revoke <id>`,
	}
	c.AddCommand(newAgentTokenListCmd())
	c.AddCommand(newAgentTokenCreateCmd())
	c.AddCommand(newAgentTokenRevokeCmd())
	return c
}

type selfTokenList struct {
	Tokens []struct {
		ID         string  `json:"id"`
		Name       string  `json:"name"`
		CreatedAt  string  `json:"created_at"`
		LastUsedAt *string `json:"last_used_at"`
	} `json:"tokens"`
	Limit int `json:"limit"`
	// Por qué no puede, cuando no puede. Una lista vacía sin esto se lee como
	// "no tienes ninguno" cuando lo cierto es "no puedes tener ninguno" — el
	// mismo fallo que un prefijo vacío indistinguible de uno inexistente.
	CanCreate     bool   `json:"can_create"`
	BlockedReason string `json:"blocked_reason"`
	BlockedDetail string `json:"blocked_detail"`
}

func newAgentTokenListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List your agent tokens, and say why you cannot create one if you cannot.",
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, status, err := apiCall("GET", "/auth/mcp-tokens/self", nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				var resp selfTokenList
				if err := json.Unmarshal(b, &resp); err != nil {
					cmd.OutOrStdout().Write(norm.NFC.Bytes(b))
					return nil
				}
				w := cmd.OutOrStdout()
				if !resp.CanCreate {
					fmt.Fprintln(w, "You cannot create an agent token here.")
					fmt.Fprintln(w, norm.NFC.String(resp.BlockedDetail))
					if len(resp.Tokens) == 0 {
						return nil
					}
					fmt.Fprintln(w)
				}
				if len(resp.Tokens) == 0 {
					fmt.Fprintf(w, "No agent tokens. You may hold up to %d.\n", resp.Limit)
					return nil
				}
				for _, t := range resp.Tokens {
					used := "never used"
					if t.LastUsedAt != nil {
						used = "last used " + *t.LastUsedAt
					}
					fmt.Fprintf(w, "%s  %-28s %s\n",
						norm.NFC.String(t.ID), norm.NFC.String(t.Name), used)
				}
				fmt.Fprintf(w, "\n%d of %d.\n", len(resp.Tokens), resp.Limit)
				return nil
			})
		},
	}
}

func newAgentTokenCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create <name>",
		Short: "Create a token for your agent. Shown once — copy it now.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, _ := json.Marshal(map[string]string{"name": args[0]})
			raw, status, err := apiCall("POST", "/auth/mcp-tokens/self", body)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				var resp struct {
					ID    string `json:"id"`
					Name  string `json:"name"`
					Token string `json:"token"`
				}
				if err := json.Unmarshal(b, &resp); err != nil {
					cmd.OutOrStdout().Write(norm.NFC.Bytes(b))
					return nil
				}
				w := cmd.OutOrStdout()
				fmt.Fprintln(w, norm.NFC.String(resp.Token))
				fmt.Fprintf(w, "\nSaved as %q (id %s). This is the only time it is shown.\n",
					norm.NFC.String(resp.Name), norm.NFC.String(resp.ID))
				fmt.Fprintln(w, "It does not expire — revoke it with `blenau agent-token revoke` when you are done.")
				return nil
			})
		},
	}
}

func newAgentTokenRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoke one of your agent tokens.",
		Long: `Revoking is immediate and cannot be undone: any agent using that token stops
working. Create a new one if you need it again.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, status, err := apiCall("DELETE", "/auth/mcp-tokens/self/"+args[0], nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				fmt.Fprintf(cmd.OutOrStdout(), "Revoked %s.\n", norm.NFC.String(args[0]))
				return nil
			})
		},
	}
}

// NewAgentAccessCmd builds `blenau agent-access ...` — the OWNER's side: the
// switch, and the inventory of who holds a token because of it.
//
// The two live in one command on purpose. "Is it on" is the less useful half;
// with tokens that never expire, "who is holding one" is the whole of the
// security story, and a switch you can flip without seeing that list is a
// decision made blind.
func NewAgentAccessCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "agent-access",
		Short: "Owner: allow external accounts to create agent tokens, and see who holds one.",
		Long: `Agent access for external accounts is OFF until you turn it on. When it is on,
the people you and your delegates admitted can create a token for their own AI
agent, scoped to exactly what they can already read.

Turning it off revokes every token it authorised, immediately. Turning it back
on does not restore them.

  blenau agent-access status
  blenau agent-access who
  blenau agent-access on
  blenau agent-access off`,
	}
	c.AddCommand(newAgentAccessStatusCmd())
	c.AddCommand(newAgentAccessSetCmd("on", true))
	c.AddCommand(newAgentAccessSetCmd("off", false))
	c.AddCommand(newAgentAccessWhoCmd())
	return c
}

func newAgentAccessStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Is agent access on, and what is riding on it.",
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, status, err := apiCall("GET", "/workspace/customer-agents", nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				var resp struct {
					Enabled            bool `json:"enabled"`
					ExternalAccounts   int  `json:"external_accounts"`
					LiveCustomerTokens int  `json:"live_customer_tokens"`
				}
				if err := json.Unmarshal(b, &resp); err != nil {
					cmd.OutOrStdout().Write(norm.NFC.Bytes(b))
					return nil
				}
				state := "off"
				if resp.Enabled {
					state = "on"
				}
				fmt.Fprintf(cmd.OutOrStdout(),
					"Agent access for external accounts: %s\n%d external account(s), %d live token(s).\n",
					state, resp.ExternalAccounts, resp.LiveCustomerTokens)
				return nil
			})
		},
	}
}

func newAgentAccessSetCmd(verb string, enabled bool) *cobra.Command {
	short := "Allow external accounts to create agent tokens."
	if !enabled {
		short = "Stop it, and revoke every token it authorised."
	}
	return &cobra.Command{
		Use:   verb,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			body, _ := json.Marshal(map[string]bool{"enabled": enabled})
			raw, status, err := apiCall("PUT", "/workspace/customer-agents", body)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				var resp struct {
					Enabled       bool `json:"enabled"`
					TokensRevoked int  `json:"tokens_revoked"`
				}
				if err := json.Unmarshal(b, &resp); err != nil {
					cmd.OutOrStdout().Write(norm.NFC.Bytes(b))
					return nil
				}
				w := cmd.OutOrStdout()
				if resp.Enabled {
					fmt.Fprintln(w, "Agent access is on.")
				} else {
					fmt.Fprintf(w, "Agent access is off. %d token(s) revoked.\n", resp.TokensRevoked)
				}
				return nil
			})
		},
	}
}

func newAgentAccessWhoCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "who",
		Short: "Who holds an agent token, and who admitted them.",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/auth/mcp-tokens/inventory"
			if all, _ := cmd.Flags().GetBool("include-revoked"); all {
				path += "?include_revoked=true"
			}
			raw, status, err := apiCall("GET", path, nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				var resp struct {
					Tokens []struct {
						HolderEmail  string  `json:"holder_email"`
						Audience     string  `json:"holder_audience"`
						SponsorEmail *string `json:"sponsor_email"`
						Name         string  `json:"name"`
						Kind         string  `json:"kind"`
						NeverExpires bool    `json:"never_expires"`
						LastUsedAt   *string `json:"last_used_at"`
						Revoked      bool    `json:"revoked"`
						Reason       *string `json:"revoked_reason"`
					} `json:"tokens"`
				}
				if err := json.Unmarshal(b, &resp); err != nil {
					cmd.OutOrStdout().Write(norm.NFC.Bytes(b))
					return nil
				}
				w := cmd.OutOrStdout()
				if len(resp.Tokens) == 0 {
					fmt.Fprintln(w, "Nobody holds a token.")
					return nil
				}
				for _, t := range resp.Tokens {
					sponsor := "you"
					if t.SponsorEmail != nil {
						sponsor = *t.SponsorEmail
					}
					used := "never used"
					if t.LastUsedAt != nil {
						used = "last used " + *t.LastUsedAt
					}
					line := fmt.Sprintf("%-32s %-9s admitted by %-28s %-20s %s",
						norm.NFC.String(t.HolderEmail), norm.NFC.String(t.Audience),
						norm.NFC.String(sponsor), norm.NFC.String(t.Name), used)
					if t.Revoked && t.Reason != nil {
						line += "  [revoked: " + norm.NFC.String(*t.Reason) + "]"
					}
					fmt.Fprintln(w, line)
				}
				return nil
			})
		},
	}
	c.Flags().Bool("include-revoked", false, "Also show tokens that have already been cut, and why.")
	return c
}
