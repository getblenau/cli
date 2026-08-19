package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"
)

// NewAccessCmd builds `blenau access ...` — the DELEGATE's lane: someone whose
// supplier gave them the right to admit their own people to the supplier's
// workspace.
//
// It lives in the CLI (and not in MCP) because the device-flow login is the
// PERSON, with their real role — admitting someone is a nominal decision, and a
// detached agent token is exactly the credential that must not make it.
func NewAccessCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "access",
		Short: "Admit your own people to a workspace someone shared with you.",
		Long: `When a supplier gives you permission to admit people to their workspace, this
is where you do it. Everyone you admit is READ-ONLY and can only reach the
groups the supplier opened to you — you choose who, never what.

Two separate limits apply and the smaller one wins:

  * the ALLOWANCE the workspace owner gave you, here, and
  * the seats your own Blenau plan allows you, across every workspace.

'blenau access quota' tells you which of the two is currently stopping you, in
the "binding" field. If it says "owner", paying Blenau will NOT unblock you —
only that workspace's admin can raise your allowance, and 'blenau access
request-more' asks them for you. Bare 'blenau access' shows the quota.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return accessQuota(cmd)
		},
	}
	c.PersistentFlags().Bool("json", false, "Emit JSON instead of human format.")
	c.AddCommand(newAccessQuotaCmd())
	c.AddCommand(newAccessListCmd())
	c.AddCommand(newAccessAdmitCmd())
	c.AddCommand(newAccessWithdrawCmd())
	c.AddCommand(newAccessRequestMoreCmd())
	return c
}

type accessQuotaResp struct {
	Used           int    `json:"used"`
	UsedEverywhere int    `json:"used_everywhere"`
	OwnerAllowance int    `json:"owner_allowance"`
	PlanLimit      *int   `json:"plan_limit"`
	Plan           string `json:"plan"`
	// Which of the limits is stopping you: "owner", "plan", "owner_billing",
	// or absent/null when nothing is. Never guessed client-side — a channel
	// that offers an upgrade when the owner is the one blocking is telling the
	// user something false.
	Binding *string `json:"binding"`
	// Qué está pasando y qué hacer, redactado por la API. El CLI lo IMPRIME, no
	// lo escribe: llegó a imprimir "blenau billing upgrade", que no es un
	// comando, y se cazó al comprobarlo antes de cerrar. Con una sola fuente, la
	// pantalla web, el 402 y esta salida no pueden divergir.
	Message         string  `json:"message"`
	SuggestedAction *string `json:"suggested_action"`
	// Relleno SÓLO cuando pagar resuelve. Su ausencia es la señal de no ofrecer
	// el upgrade.
	SuggestedPlan *string `json:"suggested_plan"`
	CanAdmit      bool    `json:"can_admit"`
	ScopeNote     *string `json:"scope_note"`
	Groups        []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"groups"`
}

func accessQuota(cmd *cobra.Command) error {
	raw, status, err := apiCall("GET", "/delegation/quota", nil)
	if err != nil {
		return err
	}
	return emitOrFail(cmd, raw, status, func(b []byte) error {
		var r accessQuotaResp
		if err := json.Unmarshal(b, &r); err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		plan := r.Plan
		if plan == "" {
			plan = "no plan"
		}
		limit := "unlimited"
		if r.PlanLimit != nil {
			limit = strconv.Itoa(*r.PlanLimit)
		}
		fmt.Fprintf(out, "Here:      %d of %d allowed by the workspace owner\n",
			r.Used, r.OwnerAllowance)
		fmt.Fprintf(out, "Everywhere: %d of %s allowed by your plan (%s)\n",
			r.UsedEverywhere, limit, plan)
		fmt.Fprintln(out)

		fmt.Fprintf(out, "%s\n", r.Message)
		if r.SuggestedAction != nil && *r.SuggestedAction != "" {
			fmt.Fprintf(out, "-> %s\n", *r.SuggestedAction)
		}
		// El único remedio que este canal aporta por su cuenta, y sólo porque es
		// un comando SUYO. Se ofrece nada más cuando ata el dueño: en los otros
		// casos mandaría a molestar a alguien que no puede ayudar.
		if r.Binding != nil && *r.Binding == "owner" {
			fmt.Fprintf(out, "   blenau access request-more\n")
		}

		fmt.Fprintln(out)
		if r.ScopeNote != nil && *r.ScopeNote != "" {
			fmt.Fprintf(out, "%s\n", *r.ScopeNote)
			return nil
		}
		fmt.Fprintf(out, "Groups you can share (%d):\n", len(r.Groups))
		for _, g := range r.Groups {
			fmt.Fprintf(out, "  %s  %s\n", g.ID, g.Name)
		}
		return nil
	})
}

func newAccessQuotaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "quota",
		Short: "Show both limits, which one is blocking you, and what you can share.",
		Long: `Shows how many people you have admitted here versus the allowance the owner
gave you, and how many you have admitted everywhere versus your own plan.

The "binding" field names which limit is stopping you, and the message and
suggested action come from the server so this command, the dashboard and the
402 you would get all say the same thing. "plan" means buying more helps;
"owner" means it does not, and 'blenau access request-more' is the way out.`,
		RunE: func(cmd *cobra.Command, args []string) error { return accessQuota(cmd) },
	}
}

func newAccessListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the people you have admitted to this workspace.",
		Long: `Your branch only — the people YOU admitted. You never see who else the
workspace admitted, or who other delegates admitted.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, status, err := apiCall("GET", "/delegation/admissions", nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				var r struct {
					Admissions []struct {
						ID     string `json:"id"`
						Email  string `json:"email"`
						Name   string `json:"name"`
						Status string `json:"status"`
					} `json:"admissions"`
				}
				if err := json.Unmarshal(b, &r); err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				fmt.Fprintf(out, "%d admitted\n", len(r.Admissions))
				for _, a := range r.Admissions {
					fmt.Fprintf(out, "  %s  %-32s %-8s %s\n", a.ID, a.Email, a.Status, a.Name)
				}
				return nil
			})
		},
	}
}

func newAccessAdmitCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "admit <email>",
		Short: "Admit one person, read-only, to groups you already hold.",
		Long: `Admits someone to this workspace as a read-only, identity-bound reader. They
sign in as themselves; no shareable link is ever created, which is the point —
the content is confidential, so there is no forwardable bearer anywhere in the
chain.

--group is required and repeatable, and every group must be one YOU hold and
that the owner opened to customers. Run 'blenau access quota' to see the list.
You choose WHO, never WHAT: the access level is always read-only.

Example:
  blenau access admit ana@cliente.com --name "Ana" --group 2f1c...`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			groups, _ := cmd.Flags().GetStringArray("group")
			if len(groups) == 0 {
				return fmt.Errorf("--group is required (see 'blenau access quota' for the ones you can share)")
			}
			body := map[string]interface{}{"email": args[0], "groups": groups}
			if cmd.Flags().Changed("name") {
				name, _ := cmd.Flags().GetString("name")
				body["name"] = name
			}
			b, _ := json.Marshal(body)
			raw, status, err := apiCall("POST", "/delegation/admissions", b)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
	c.Flags().StringArray("group", nil, "Group id to share (repeatable). REQUIRED.")
	c.Flags().String("name", "", "Display name for the person.")
	_ = c.MarkFlagRequired("group")
	return c
}

func newAccessWithdrawCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "withdraw <admission-id>",
		Short: "Withdraw someone you admitted (frees the seat; keeps the record).",
		Long: `Cuts off access for someone YOU admitted, and frees the seat. You can only
withdraw people from your own branch.

The row is revoked, not deleted: who admitted whom is the first question any
access review asks, and deleting the record would make it unanswerable.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, status, err := apiCall(
				"DELETE", "/delegation/admissions/"+url.PathEscape(args[0]), nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
}

func newAccessRequestMoreCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "request-more",
		Short: "Ask this workspace's admins to raise your allowance.",
		Long: `Emails the workspace admins that you have run out of room, with the number
they need to change and a link to the page where they change it.

This is the remedy when 'blenau access quota' says binding=owner: buying a
Blenau plan would not raise that limit, so the CLI does not offer it there.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]interface{}{}
			if cmd.Flags().Changed("note") {
				note, _ := cmd.Flags().GetString("note")
				body["note"] = note
			}
			b, _ := json.Marshal(body)
			raw, status, err := apiCall("POST", "/delegation/allowance-request", b)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
	c.Flags().String("note", "", "Short message for the admins (why you need more).")
	return c
}

// NewDelegationCmd builds `blenau delegation ...` — the OWNER's side. Admin
// only: this is where the right to admit is handed out, adjusted and taken
// back, and where the owner sees what their delegates did with it.
func NewDelegationCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "delegation",
		Short: "Let a customer admit their own people, and see what they did with it.",
		Long: `Delegation lets a customer of yours admit THEIR people to groups you opened to
customers — read-only, and never beyond what that customer can already read.
They pick who; you decide what, and how many.

Their decisions are your exposure, so 'blenau delegation tree' is the screen
that matters: someone admitting 40 people to your confidential manuals should
not be something you find out from an invoice.

Admin only. Bare 'blenau delegation' shows the tree.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return delegationTree(cmd)
		},
	}
	c.PersistentFlags().Bool("json", false, "Emit JSON instead of human format.")
	c.AddCommand(newDelegationTreeCmd())
	c.AddCommand(newDelegationGrantCmd())
	c.AddCommand(newDelegationAllowanceCmd())
	c.AddCommand(newDelegationRevokeCmd())
	return c
}

func delegationTree(cmd *cobra.Command) error {
	raw, status, err := apiCall("GET", "/delegation/tree", nil)
	if err != nil {
		return err
	}
	return emitOrFail(cmd, raw, status, func(b []byte) error {
		var r struct {
			Branches []struct {
				Delegate struct {
					ID     string `json:"id"`
					Email  string `json:"email"`
					Name   string `json:"name"`
					Status string `json:"status"`
				} `json:"delegate"`
				Allowance  int `json:"allowance"`
				Used       int `json:"used"`
				Admissions []struct {
					Email  string `json:"email"`
					Status string `json:"status"`
				} `json:"admissions"`
			} `json:"branches"`
		}
		if err := json.Unmarshal(b, &r); err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if len(r.Branches) == 0 {
			fmt.Fprintln(out, "Nobody has been given permission to admit people here.")
			fmt.Fprintln(out, "Grant it with: blenau delegation grant <member-id> --group <id>")
			return nil
		}
		for _, br := range r.Branches {
			fmt.Fprintf(out, "%s (%s)  %d/%d admitted  [%s]\n",
				br.Delegate.Email, br.Delegate.ID, br.Used, br.Allowance,
				br.Delegate.Status)
			for _, a := range br.Admissions {
				fmt.Fprintf(out, "    %-32s %s\n", a.Email, a.Status)
			}
		}
		return nil
	})
}

func newDelegationTreeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tree",
		Short: "Show who each delegate has admitted, and their allowance.",
		RunE:  func(cmd *cobra.Command, args []string) error { return delegationTree(cmd) },
	}
}

func newDelegationGrantCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "grant <member-id>",
		Short: "Give a member the right to admit people to specific groups.",
		Long: `Grants (or re-scopes) the right to admit. --group is repeatable and REPLACES
the previous scope, so pass the full list every time.

Only groups open to the 'customers' audience can be delegated: otherwise a
delegate ends up admitting people into a staff group. Open a group to customers
in Dashboard > Groups first.

A group the delegate does not belong to has no effect — the effective scope is
always the intersection with their own access, recomputed on every request, so
narrowing their access narrows what they can hand out without touching this.

--allowance defaults to 3 and can be 0, which is "granted but blocked": they
still see the surface and who to ask instead of a screen that looks broken.

Example:
  blenau delegation grant 8c1f... --allowance 10 --group 2f1c... --group 9ab0...`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			groups, _ := cmd.Flags().GetStringArray("group")
			allowance, _ := cmd.Flags().GetInt("allowance")
			body := map[string]interface{}{"allowance": allowance, "groups": groups}
			b, _ := json.Marshal(body)
			raw, status, err := apiCall(
				"PUT", "/members/"+url.PathEscape(args[0])+"/delegation", b)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
	c.Flags().StringArray("group", nil, "Group id they may share (repeatable; replaces the previous scope).")
	c.Flags().Int("allowance", 3, "How many people they may admit here. 0 = granted but blocked.")
	return c
}

func newDelegationAllowanceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-allowance <member-id> <n>",
		Short: "Change how many people a delegate may admit here. Nothing else.",
		Long: `Moves only the number. It never widens which groups they can share, and it
will not create a delegation that does not exist yet — granting one is a
deliberate act, so use 'blenau delegation grant' for that.

This is the command to run when a delegate tells you they have run out of room.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := strconv.Atoi(args[1])
			if err != nil || n < 0 {
				return fmt.Errorf("allowance must be a number >= 0, got %q", args[1])
			}
			b, _ := json.Marshal(map[string]interface{}{"allowance": n})
			raw, status, err := apiCall(
				"PATCH", "/members/"+url.PathEscape(args[0])+"/delegation", b)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
}

func newDelegationRevokeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "revoke <member-id>",
		Short: "Take back the right to admit, and cut off everyone they admitted.",
		Long: `By default this cascades: the delegate loses the right AND everyone they
admitted loses access. That is the usual meaning of "this customer is gone",
and it is one action rather than removing their people one by one.

--keep-branch instead ADOPTS their people as your own readers: they keep
reading, they stop being that delegate's responsibility, and from then on they
count against your own reader seats. Use it when the customer leaves but the
people they brought should stay.

Neither form deletes anyone. Who admitted whom stays on the record.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			keep, _ := cmd.Flags().GetBool("keep-branch")
			path := "/members/" + url.PathEscape(args[0]) + "/delegation"
			if keep {
				path += "?cascade=false"
			}
			raw, status, err := apiCall("DELETE", path, nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
	c.Flags().Bool("keep-branch", false, "Adopt their people as your own readers instead of cutting them off.")
	return c
}
