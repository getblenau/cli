package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"
	"golang.org/x/text/unicode/norm"
)

// NewNotesCmd builds `blenau notes ...` — the working-memory tier.
func NewNotesCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "notes",
		Short: "Quick notes, lists and reminders (working memory, NOT knowledge).",
		Long: `Notes — the working-memory tier of Blenau.

Deliberately NOT Knowledge. Capture a note for free from an impulse ("add
toothpaste to my list", "idea for the next video") and it lives only until it
is acted on. Two invariants make it useful to an agent:

  * recall is EXHAUSTIVE and STRUCTURED — "this is the list, complete and
    exact", the one thing a native-agent memory blob cannot promise.
  * notes NEVER touch the Knowledge retrieval path, so "what do I know about X?"
    can never return a grocery item. Notes are never returned by 'blenau search'.

Use notes for disposable, act-on-it-then-clear things (shopping lists,
reminders, to-dos, fleeting ideas). Use 'blenau ingest' / 'blenau docs' for
durable knowledge you want to keep. Notes are meant to empty; Knowledge accrues.

Honest limit: Blenau does not send scheduled alerts. It stores a reminder for
review via 'recall' (keep the date in the note text); it does not ping you at a
time. A workspace member (admin/member role) may read and write; readers cannot.`,
	}
	c.PersistentFlags().Bool("json", false, "Emit JSON instead of human format.")
	c.AddCommand(newNotesRememberCmd())
	c.AddCommand(newNotesRecallCmd())
	c.AddCommand(newNotesListsCmd())
	c.AddCommand(newNotesDoneCmd())
	c.AddCommand(newNotesReopenCmd())
	c.AddCommand(newNotesUpdateCmd())
	c.AddCommand(newNotesForgetCmd())
	c.AddCommand(newNotesSharesCmd())
	c.AddCommand(newNotesShareListCmd())
	c.AddCommand(newNotesUnshareListCmd())
	return c
}

func newNotesSharesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "shares",
		Short: "Which of YOUR personal lists are shared with which groups.",
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, status, err := apiCall("GET", "/notes/shares", nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
}

func newNotesShareListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "share-list <list>",
		Short: "Lend one of YOUR personal lists to a group (additive, revocable).",
		Long: `Lend one of your personal note lists to a group — "share my video-ideas list
with the content team". The group's members and agents can then read and act on
the personal notes in that list (mark done, edit), like a shared fridge list.
Your other personal lists stay yours; workspace notes are unaffected. Only
affects lists YOU own. Idempotent.

Example:
  blenau notes share-list video-ideas --group <group-id>`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			group, _ := cmd.Flags().GetString("group")
			if group == "" {
				return fmt.Errorf("--group is required (the group's id)")
			}
			raw, status, err := apiCall(
				"PUT",
				"/notes/shares/"+url.PathEscape(group)+"?list="+url.QueryEscape(args[0]),
				nil,
			)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
	c.Flags().String("group", "", "Group id to share with. REQUIRED.")
	_ = c.MarkFlagRequired("group")
	return c
}

func newNotesUnshareListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "unshare-list <list>",
		Short: "Stop lending a personal list to a group (access ends immediately).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			group, _ := cmd.Flags().GetString("group")
			if group == "" {
				return fmt.Errorf("--group is required")
			}
			raw, status, err := apiCall(
				"DELETE",
				"/notes/shares/"+url.PathEscape(group)+"?list="+url.QueryEscape(args[0]),
				nil,
			)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
	c.Flags().String("group", "", "Group id to unshare. REQUIRED.")
	_ = c.MarkFlagRequired("group")
	return c
}

func newNotesRememberCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "remember <body>",
		Short: "Capture a note. Body + an optional --list; nothing else.",
		Long: `Capture a note. Body plus an optional --list — no title, no path. The list is
created on first use; capture is free, which is the whole point.

Infer --list from what the user said ("my shopping list" -> "groceries";
"remind me..." -> "reminders"). Run 'blenau notes lists' first if you want to
land in an existing bucket instead of coining a near-duplicate. Omit --list for
the "inbox" bucket.

--private makes the note PERSONAL: visible only to you (and credentials acting
as you), never to other workspace members or standalone agents. Default is the
shared workspace pool.

Examples:
  blenau notes remember "buy oat milk" --list groceries
  blenau notes remember "gift idea for mom" --list personal-stuff --private`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]interface{}{"body": args[0]}
			if list, _ := cmd.Flags().GetString("list"); list != "" {
				body["list"] = list
			}
			if private, _ := cmd.Flags().GetBool("private"); private {
				body["private"] = true
			}
			b, _ := json.Marshal(body)
			raw, status, err := apiCall("POST", "/notes", b)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, humanNote(cmd, "noted"))
		},
	}
	c.Flags().String("list", "", "Bucket the note belongs to (e.g. groceries, reminders). Default: inbox.")
	c.Flags().Bool("private", false, "Personal note: visible only to you, never to other members or standalone agents.")
	return c
}

func newNotesRecallCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "recall",
		Short: "Return a list EXHAUSTIVELY (the complete, exact set — not a top-k).",
		Long: `Return notes, complete and exact — every note in the list, in the order it was
built, so the caller can trust it and reshape it (group by category, add prices,
send it on). This is NOT a semantic top-k.

  --list    the bucket to read; omit to get everything across lists.
  --status  open (default — a list should read as remaining work), done,
            archived, or all.
  --query   OPTIONAL text filter over the note body ("did I jot something about
            milk?") — a word filter, never a similarity ranking.

Examples:
  blenau notes recall --list groceries
  blenau notes recall --status all
  blenau notes recall --list reminders -q domain`,
		RunE: func(cmd *cobra.Command, args []string) error {
			q := url.Values{}
			if list, _ := cmd.Flags().GetString("list"); list != "" {
				q.Set("list", list)
			}
			if status, _ := cmd.Flags().GetString("status"); status != "" {
				q.Set("status", status)
			}
			if query, _ := cmd.Flags().GetString("query"); query != "" {
				q.Set("q", query)
			}
			if cmd.Flags().Changed("limit") {
				limit, _ := cmd.Flags().GetInt("limit")
				q.Set("limit", strconv.Itoa(limit))
			}
			path := "/notes"
			if enc := q.Encode(); enc != "" {
				path += "?" + enc
			}
			raw, status, err := apiCall("GET", path, nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				var resp struct {
					List      *string `json:"list"`
					Status    string  `json:"status"`
					Count     int     `json:"count"`
					Truncated bool    `json:"truncated"`
					Notes     []struct {
						ID     string `json:"id"`
						Body   string `json:"body"`
						List   string `json:"list"`
						Status string `json:"status"`
					} `json:"notes"`
				}
				if err := json.Unmarshal(b, &resp); err != nil {
					cmd.OutOrStdout().Write(norm.NFC.Bytes(b))
					return nil
				}
				w := cmd.OutOrStdout()
				if len(resp.Notes) == 0 {
					fmt.Fprintln(w, "No notes.")
					return nil
				}
				for _, n := range resp.Notes {
					mark := "[ ]"
					if n.Status == "done" {
						mark = "[x]"
					} else if n.Status == "archived" {
						mark = "[~]"
					}
					fmt.Fprintf(w, "%s %s  (%s) %s\n",
						mark, norm.NFC.String(n.Body), norm.NFC.String(n.List), norm.NFC.String(n.ID))
				}
				fmt.Fprintf(w, "\n%d note(s)", resp.Count)
				if resp.Truncated {
					fmt.Fprint(w, " (truncated — raise --limit)")
				}
				fmt.Fprintln(w)
				return nil
			})
		},
	}
	c.Flags().String("list", "", "Bucket to read; omit for all lists.")
	c.Flags().String("status", "open", "open | done | archived | all.")
	c.Flags().StringP("query", "q", "", "Optional text filter over the note body.")
	c.Flags().Int("limit", 0, "Max notes to return (default: exhaustive, up to 1000).")
	return c
}

func newNotesListsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "lists",
		Short: "Show every list with open/done counts and last activity.",
		Long: `Show every note list with its open/done counts and last activity. Read this
before adding to a list so "add milk to my list" lands in the existing
"groceries" bucket rather than coining a near-duplicate — or when the user asks
"what lists do I have?".`,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, status, err := apiCall("GET", "/notes/lists", nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				var resp struct {
					Lists []struct {
						Name         string `json:"name"`
						Open         int    `json:"open"`
						Done         int    `json:"done"`
						LastActivity string `json:"last_activity"`
					} `json:"lists"`
				}
				if err := json.Unmarshal(b, &resp); err != nil {
					cmd.OutOrStdout().Write(norm.NFC.Bytes(b))
					return nil
				}
				w := cmd.OutOrStdout()
				if len(resp.Lists) == 0 {
					fmt.Fprintln(w, "No lists.")
					return nil
				}
				fmt.Fprintf(w, "%-24s %6s %6s  %s\n", "LIST", "OPEN", "DONE", "LAST_ACTIVITY")
				for _, l := range resp.Lists {
					fmt.Fprintf(w, "%-24s %6d %6d  %s\n",
						norm.NFC.String(l.Name), l.Open, l.Done, l.LastActivity)
				}
				return nil
			})
		},
	}
}

func newNotesDoneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "done <note-id>",
		Short: "Mark a note done — the verb that makes the store tend to empty.",
		Long: `Mark a note complete: the user did the thing (bought the item, sent the email).
This is what shrinks a list toward empty; the note stays but drops out of the
default 'open' view. Pass the note id from 'blenau notes recall'.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, status, err := apiCall("POST", "/notes/"+url.PathEscape(args[0])+"/done", nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, humanNote(cmd, "done"))
		},
	}
}

func newNotesReopenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reopen <note-id>",
		Short: "Undo a completion (a note marked done too soon).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, status, err := apiCall("POST", "/notes/"+url.PathEscape(args[0])+"/reopen", nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, humanNote(cmd, "reopened"))
		},
	}
}

func newNotesUpdateCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "update <note-id>",
		Short: "Edit a note's text or move it to another list.",
		Long: `Edit a note's text (--body) and/or move it to another list (--list) — "change
milk to oat milk", "move this to my work list". Only the fields you pass change.
Pass the note id from 'blenau notes recall'.

--private / --shared flip the note's visibility (only the creator may make a
note personal; --shared puts it back in the workspace pool).

Examples:
  blenau notes update <id> --body "oat milk"
  blenau notes update <id> --list work
  blenau notes update <id> --private`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]interface{}{}
			if cmd.Flags().Changed("body") {
				v, _ := cmd.Flags().GetString("body")
				body["body"] = v
			}
			if cmd.Flags().Changed("list") {
				v, _ := cmd.Flags().GetString("list")
				body["list"] = v
			}
			privateSet, _ := cmd.Flags().GetBool("private")
			sharedSet, _ := cmd.Flags().GetBool("shared")
			if privateSet && sharedSet {
				return fmt.Errorf("--private and --shared are mutually exclusive")
			}
			if privateSet {
				body["private"] = true
			}
			if sharedSet {
				body["private"] = false
			}
			if len(body) == 0 {
				return fmt.Errorf("nothing to update: pass --body, --list, --private or --shared")
			}
			b, _ := json.Marshal(body)
			raw, status, err := apiCall("PATCH", "/notes/"+url.PathEscape(args[0]), b)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, humanNote(cmd, "updated"))
		},
	}
	c.Flags().String("body", "", "New note text.")
	c.Flags().String("list", "", "Move the note to this list.")
	c.Flags().Bool("private", false, "Make the note personal (creator-only).")
	c.Flags().Bool("shared", false, "Put the note back in the shared workspace pool.")
	return c
}

func newNotesForgetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "forget <note-id>",
		Short: "Delete a note outright (real deletion — use 'done' to keep it).",
		Long: `Delete a note outright — the user no longer wants it at all. To keep a completed
note but out of the way, use 'blenau notes done' instead; this is real deletion.
Pass the note id from 'blenau notes recall'.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, status, err := apiCall("DELETE", "/notes/"+url.PathEscape(args[0]), nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
}

// humanNote returns a humanFn that prints "<verb> <id>  <body> [list]" for the
// single-note response shape shared by remember/done/reopen/update.
func humanNote(cmd *cobra.Command, verb string) func([]byte) error {
	return func(b []byte) error {
		var n struct {
			ID     string `json:"id"`
			Body   string `json:"body"`
			List   string `json:"list"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(b, &n); err != nil || n.ID == "" {
			cmd.OutOrStdout().Write(norm.NFC.Bytes(b))
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s  %s (%s)\n",
			verb, norm.NFC.String(n.ID), norm.NFC.String(n.Body), norm.NFC.String(n.List))
		return nil
	}
}
