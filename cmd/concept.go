package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// conceptText is Blenau's mental model, explained from inside the CLI so a new
// user never has to leave the terminal to understand what the primitives are.
const conceptText = `Blenau — the mental model

Blenau has THREE tiers, each with its own CLI surface. Pick by intent:

1. KNOWLEDGE  (blenau ingest / search / docs / edit-section …)
   The durable tier. The atomic unit is a DOCUMENT with a hierarchical PATH
   (e.g. ganemo/infra-odoo/backups.md). Documents are versioned, provenance-
   tracked and git-backed (each write is a commit in a connected GitHub repo).
   Grouping is by PATH CONVENTION — a shared prefix IS the "folder"; there are
   no folder entities. Use Knowledge for things you want to keep and retrieve by
   meaning. 'blenau search' searches ONLY this tier.
     • list a subtree:   blenau docs list --prefix ganemo/infra-odoo/
     • read a doc:        blenau docs get <path>
     • write a doc:       blenau ingest --path <path> --title <t> < file.md

2. NOTES  (blenau notes …)
   The working-memory tier: quick lists, reminders, to-dos, fleeting ideas.
   Disposable — meant to be completed and cleared. A note has a body + an
   optional list; NO path. Notes are NEVER returned by 'blenau search' (asking
   "what do I know about X?" can't surface a grocery item).
     • blenau notes remember "renew the domain Monday" --list reminders
     • blenau notes recall --list reminders

3. COLLECTIONS  (blenau collections …)
   The structured-records tier: tables of records synced from an external system
   (an Odoo ERP, a script): products, partners, orders. An agent resolves a
   natural-language query to a record's source id + metadata + confidence.
     • blenau collections query productos -q "queen cotton sheets"

Why no free-form tags/labels? Because the PATH is the grouping primitive — it's
provenance-tracked and git-backed. A parallel tag axis would fragment that. Need
flexible grouping? Choose a path prefix (they're cheap and nestable).

Run 'blenau <tier> --help' for the full surface of any tier.`

// NewConceptCmd builds `blenau concept`.
func NewConceptCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "concept",
		Aliases: []string{"model"},
		Short:   "Explain Blenau's mental model (Knowledge vs Notes vs Collections).",
		Long:    "Print Blenau's mental model — the three tiers (Knowledge / Notes / Collections), what the atomic primitives are, and how grouping works — without leaving the terminal.",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), conceptText)
			return nil
		},
	}
}
