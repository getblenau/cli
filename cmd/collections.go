package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/text/unicode/norm"
)

// NewCollectionsCmd builds `blenau collections ...` — the ERP-context layer.
func NewCollectionsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "collections",
		Short: "Structured record collections synced from external systems (ERP context).",
		Long: `Collections — the structured-data / ERP-context layer of Blenau.

A collection is a table of records synced from an external system (an Odoo ERP,
a script, Make): products, partners, orders. Records arrive as flat JSON via a
one-time ingest URL (create/rotate-secret) or the authenticated 'import' path.
An agent then resolves a natural-language query to records with semantic
similarity + exact identifier match, returning each record's source id
(external_id), metadata, and a confidence verdict.

Volatile facts (live stock, live price, order status) are NOT stored here —
query gives you the external_id, then you fetch the live value from the source
system with that id. What Blenau returns is as-fresh-as-the-last-sync context.

Typical flow:
  1. create <name>              -> mint the ingest URL, wire your source to it
  2. import <name> --file …     -> backfill the existing catalogue (a webhook
                                    only fires on FUTURE changes)
  3. fields / update <name>     -> declare field roles (semantic/identifier/
                                    filterable) so search and filters behave
  4. describe / query <name>    -> what agents call at run time

Reads (list/describe/fields/query/get-record) are open to any member; writes
require an admin/member role.`,
	}
	c.PersistentFlags().Bool("json", false, "Emit JSON instead of human format.")
	c.AddCommand(newCollectionsListCmd())
	c.AddCommand(newCollectionsCreateCmd())
	c.AddCommand(newCollectionsDescribeCmd())
	c.AddCommand(newCollectionsFieldsCmd())
	c.AddCommand(newCollectionsUpdateCmd())
	c.AddCommand(newCollectionsQueryCmd())
	c.AddCommand(newCollectionsReindexCmd())
	c.AddCommand(newCollectionsImportCmd())
	c.AddCommand(newCollectionsEmbedPendingCmd())
	c.AddCommand(newCollectionsGetRecordCmd())
	c.AddCommand(newCollectionsDeleteRecordCmd())
	c.AddCommand(newCollectionsReconcileCmd())
	c.AddCommand(newCollectionsRotateSecretCmd())
	c.AddCommand(newCollectionsDeleteCmd())
	return c
}

func newCollectionsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the collections in this workspace.",
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, status, err := apiCall("GET", "/collections", nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				var resp struct {
					Collections []struct {
						Name        string `json:"name"`
						Description string `json:"description"`
					} `json:"collections"`
				}
				if err := json.Unmarshal(b, &resp); err != nil {
					cmd.OutOrStdout().Write(norm.NFC.Bytes(b))
					return nil
				}
				w := cmd.OutOrStdout()
				if len(resp.Collections) == 0 {
					fmt.Fprintln(w, "No collections.")
					return nil
				}
				fmt.Fprintf(w, "%-24s %s\n", "NAME", "DESCRIPTION")
				for _, c := range resp.Collections {
					fmt.Fprintf(w, "%-24s %s\n",
						norm.NFC.String(c.Name), norm.NFC.String(c.Description))
				}
				return nil
			})
		},
	}
}

func newCollectionsCreateCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a collection and mint its ONE-TIME ingest URL.",
		Long: `Create a collection and mint its ingest secret. The ingest URL + secret are
returned ONCE (Blenau stores only a hash) — point your source's webhook (Odoo
automation, Make, a script) at that URL and POST each record as flat JSON.

Lost the URL? 'blenau collections rotate-secret <name>' issues a new one; your
records are never affected. Requires an admin/member role.

Example:
  blenau collections create productos --description "Odoo product catalogue"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]interface{}{"name": args[0]}
			if cmd.Flags().Changed("description") {
				v, _ := cmd.Flags().GetString("description")
				body["description"] = v
			}
			b, _ := json.Marshal(body)
			raw, status, err := apiCall("POST", "/collections", b)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, humanIngestURL(cmd))
		},
	}
	c.Flags().String("description", "", "Human description of what the collection holds.")
	return c
}

func newCollectionsDescribeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "describe <name>",
		Short: "Discover a collection's shape BEFORE querying (fields, ids, counts).",
		Long: `Discovery for agents: returns id_field, id_match_fields (identifiers matched
exactly), filterable_fields (a {field: type} map — string/number/date/enum),
exposed_fields, and record counts (records / pending_embed / failed_embed).
last_ingest_at is null when data has NEVER arrived — that distinguishes "nothing
changed yet" from "the webhook is pointed at the wrong place". Always call this
before 'query' so filters use real field names and types.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, status, err := apiCall("GET", "/collections/"+url.PathEscape(args[0])+"/describe", nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
}

func newCollectionsFieldsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "fields <name>",
		Short: "Inspect what the source is ACTUALLY sending vs what is configured.",
		Long: `For each field observed in a sample of records: detected_type, suggested_type,
suggested_role (semantic | filterable | identifier | control), coverage (how
often it carries a real value — 0.0 means the source always sends it empty,
which silently breaks type inference), distinct_count and example values.
unknown_fields lists configured names never observed (probable typos). This is
the input you edit, then feed back with 'blenau collections update'.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, status, err := apiCall("GET", "/collections/"+url.PathEscape(args[0])+"/fields", nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
}

func newCollectionsUpdateCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "update <name>",
		Short: "Declare a collection's field ROLES (semantic / identifier / filterable).",
		Long: `Declare a collection's field roles, overriding the draft Blenau infers on first
ingest. Only the flags you pass are changed (unset flags are left untouched).

  --embed-fields       fields that feed the semantic embedding (the meaning).
  --id-match-fields    exact-match identifiers, e.g. default_code. Kept OUT of
                       the embedding on purpose — a field cannot be both this and
                       --embed-fields (a code adds noise to a meaning search).
  --field-types        JSON {field: "string|number|date|enum"} so filters
                       compare correctly (without a type, price < 80 compares
                       TEXT). date/enum are also excluded from the embedding.
  --exposed-fields     allowlist of attributes returned by queries (hide the rest).
  --id-field           JSON key carrying the record id (default: id).
  --write-date-field   JSON key carrying the update clock (default: write_date).
  --description         human description.

Saving does NOT re-embed. If the response has reindex_required: true, the vectors
no longer match the config — run 'blenau collections reindex <name>'. Requires an
admin/member role.

Examples:
  blenau collections update productos --embed-fields name,description
  blenau collections update productos --id-match-fields default_code \
      --field-types '{"list_price":"number","create_date":"date"}'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]interface{}{}
			if cmd.Flags().Changed("description") {
				v, _ := cmd.Flags().GetString("description")
				body["description"] = v
			}
			for flag, key := range map[string]string{
				"embed-fields":    "embed_fields",
				"id-match-fields": "id_match_fields",
				"exposed-fields":  "exposed_fields",
			} {
				if cmd.Flags().Changed(flag) {
					v, _ := cmd.Flags().GetStringSlice(flag)
					body[key] = v
				}
			}
			for flag, key := range map[string]string{
				"id-field":         "id_field",
				"write-date-field": "write_date_field",
			} {
				if cmd.Flags().Changed(flag) {
					v, _ := cmd.Flags().GetString(flag)
					body[key] = v
				}
			}
			if cmd.Flags().Changed("field-types") {
				v, _ := cmd.Flags().GetString("field-types")
				var parsed map[string]interface{}
				if err := json.Unmarshal([]byte(v), &parsed); err != nil {
					return fmt.Errorf("--field-types must be a JSON object: %w", err)
				}
				body["field_types"] = parsed
			}
			if len(body) == 0 {
				return fmt.Errorf("nothing to update: pass at least one role flag (see --help)")
			}
			b, _ := json.Marshal(body)
			raw, status, err := apiCall("PATCH", "/collections/"+url.PathEscape(args[0]), b)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
	c.Flags().String("description", "", "Human description of the collection.")
	c.Flags().StringSlice("embed-fields", nil, "Comma-separated fields that feed the semantic embedding.")
	c.Flags().StringSlice("id-match-fields", nil, "Comma-separated exact-match identifier fields.")
	c.Flags().StringSlice("exposed-fields", nil, "Comma-separated allowlist of attributes returned by queries.")
	c.Flags().String("field-types", "", `JSON map of {field: "string|number|date|enum"}.`)
	c.Flags().String("id-field", "", "JSON key carrying the record id (default: id).")
	c.Flags().String("write-date-field", "", "JSON key carrying the update clock (default: write_date).")
	return c
}

func newCollectionsQueryCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "query <name>",
		Short: "Resolve a natural-language query (+optional filters) to records.",
		Long: `Resolve a natural-language query to records. Blenau embeds the query, matches by
semantic similarity + exact identifier (SKU/code), and returns a stable envelope:
  {"results":[{external_id, relevance, match_type, rendered, attributes}],
   "match_type", "confidence"}

Use external_id for any downstream LIVE lookup (stock, live price, order status
are not stored here). confidence is Blenau's verdict: high = trust the top
result; low = ambiguous, disambiguate first; none = no usable match.

--filters is a JSON object over the collection's fields (see 'describe'):
equality {"category":"sheets"} or ranges {"list_price":{"lte":80}}. Pass an empty
query with --filters for a pure filter query. Read-only.

Examples:
  blenau collections query productos -q "queen size cotton sheets"
  blenau collections query productos -q "" --filters '{"list_price":{"lte":80}}'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]interface{}{}
			if q, _ := cmd.Flags().GetString("query"); q != "" {
				body["query"] = q
			}
			if cmd.Flags().Changed("top-k") {
				k, _ := cmd.Flags().GetInt("top-k")
				body["top_k"] = k
			}
			if cmd.Flags().Changed("filters") {
				v, _ := cmd.Flags().GetString("filters")
				var parsed map[string]interface{}
				if err := json.Unmarshal([]byte(v), &parsed); err != nil {
					return fmt.Errorf("--filters must be a JSON object: %w", err)
				}
				body["filters"] = parsed
			}
			b, _ := json.Marshal(body)
			raw, status, err := apiCall("POST", "/collections/"+url.PathEscape(args[0])+"/query", b)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				var resp struct {
					Confidence string `json:"confidence"`
					MatchType  string `json:"match_type"`
					Results    []struct {
						ExternalID string  `json:"external_id"`
						Relevance  float64 `json:"relevance"`
						MatchType  string  `json:"match_type"`
						Rendered   string  `json:"rendered"`
					} `json:"results"`
				}
				if err := json.Unmarshal(b, &resp); err != nil {
					cmd.OutOrStdout().Write(norm.NFC.Bytes(b))
					return nil
				}
				w := cmd.OutOrStdout()
				fmt.Fprintf(w, "confidence: %s\n", norm.NFC.String(resp.Confidence))
				if len(resp.Results) == 0 {
					fmt.Fprintln(w, "No matches.")
					return nil
				}
				for _, r := range resp.Results {
					fmt.Fprintf(w, "%-20s %.3f  %-8s %s\n",
						norm.NFC.String(r.ExternalID), r.Relevance,
						norm.NFC.String(r.MatchType), norm.NFC.String(r.Rendered))
				}
				return nil
			})
		},
	}
	c.Flags().StringP("query", "q", "", "Natural-language query. Empty + --filters = pure filter query.")
	c.Flags().String("filters", "", "JSON filter over collection fields (equality or ranges).")
	c.Flags().Int("top-k", 10, "Max results to return (server caps at 50).")
	return c
}

func newCollectionsReindexCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "reindex <name>",
		Short: "Recompute every record's embedding under the current field config.",
		Long: `Recompute every record's embedding text after changing which fields are
semantic. Defaults to a PREVIEW: returns how many records would be re-embedded
plus estimated_cost_usd, and changes nothing. Re-embedding is a real bill, so it
is never a side effect of saving config.

Pass --apply to actually run it: the affected rows are marked pending and drained
via 'blenau collections embed-pending <name>' (or the dashboard drains them
automatically). Requires an admin/member role.

Examples:
  blenau collections reindex productos           # preview + cost
  blenau collections reindex productos --apply    # execute`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			apply, _ := cmd.Flags().GetBool("apply")
			dryRun := "true"
			if apply {
				dryRun = "false"
			}
			p := "/collections/" + url.PathEscape(args[0]) + "/reindex?dry_run=" + dryRun
			raw, status, err := apiCall("POST", p, nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
	c.Flags().Bool("apply", false, "Actually re-embed (default is a cost preview that changes nothing).")
	return c
}

func newCollectionsImportCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "import <name>",
		Short: "Bulk-import records from a JSON file using your own auth (backfill).",
		Long: `Bulk-import records with the caller's own auth — no ingest secret needed. This
is the backfill path: a new collection is EMPTY and a webhook only fires on
FUTURE changes, so wiring up Odoo and stopping there never brings in the existing
catalogue. Feed it that catalogue once.

--file points at a JSON array of flat record objects: [{"id":1,"name":"…"}, …].
Embeds inline up to a bound; anything left is reported as pending_embed — drain
it with 'blenau collections embed-pending <name>'. Requires an admin/member role.

Example:
  blenau collections import productos --file catalogue.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			file, _ := cmd.Flags().GetString("file")
			drain, _ := cmd.Flags().GetBool("drain")
			if file == "" {
				return fmt.Errorf("--file is required (JSON array of record objects)")
			}
			records, err := readJSONArray(file)
			if err != nil {
				return err
			}
			b, _ := json.Marshal(map[string]interface{}{"records": records})
			raw, status, err := apiCall("POST", "/collections/"+url.PathEscape(name)+"/import", b)
			if err != nil {
				return err
			}
			if status >= 400 || !drain {
				return emitOrFail(cmd, raw, status, nil)
			}
			// --drain: finish embedding everything the import left pending, so the
			// collection is fully queryable when this returns. The API embeds
			// inline up to a bound and parks the rest; loop embed-pending to zero.
			var imp map[string]interface{}
			_ = json.Unmarshal(raw, &imp)
			embedded, failed, err := drainEmbedPending(name)
			if err != nil {
				return err
			}
			if imp == nil {
				imp = map[string]interface{}{}
			}
			imp["drained_embedded"] = embedded
			imp["drained_failed"] = failed
			out, _ := json.Marshal(imp)
			return emitOrFail(cmd, out, 200, func(_ []byte) error {
				w := cmd.OutOrStdout()
				fmt.Fprintf(w, "imported %v record(s); embedded %d more while draining (%d failed)\n",
					imp["imported"], embedded, failed)
				return nil
			})
		},
	}
	c.Flags().String("file", "", "Path to a JSON array of flat record objects. REQUIRED.")
	c.Flags().Bool("drain", false, "After import, loop embed-pending until the whole collection is embedded.")
	_ = c.MarkFlagRequired("file")
	return c
}

// drainEmbedPending repeatedly calls a collection's embed-pending endpoint until
// no rows remain to process, returning cumulative embedded/failed counts. Bounded
// by maxDrainRounds so a persistently-poisoned queue can never loop forever.
func drainEmbedPending(name string) (int, int, error) {
	const maxDrainRounds = 1000
	totalEmbedded, totalFailed := 0, 0
	for round := 0; round < maxDrainRounds; round++ {
		raw, status, err := apiCall("POST", "/collections/"+url.PathEscape(name)+"/embed-pending", nil)
		if err != nil {
			return totalEmbedded, totalFailed, err
		}
		if status >= 400 {
			return totalEmbedded, totalFailed, fmt.Errorf("embed-pending failed (%d): %s", status, string(raw))
		}
		var r struct {
			Embedded int  `json:"embedded"`
			Failed   int  `json:"failed"`
			More     bool `json:"more"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return totalEmbedded, totalFailed, err
		}
		totalEmbedded += r.Embedded
		totalFailed += r.Failed
		if !r.More {
			break
		}
	}
	return totalEmbedded, totalFailed, nil
}

func newCollectionsEmbedPendingCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "embed-pending <name>",
		Short: "Embed records persisted without a vector. Re-run until more=false.",
		Long: `Embed records that were persisted without a vector (after an import or a
reindex --apply). The response reports embedded / failed / more; re-invoke until
more is false. Requires an admin/member role.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := "/collections/" + url.PathEscape(args[0]) + "/embed-pending"
			if cmd.Flags().Changed("limit") {
				limit, _ := cmd.Flags().GetInt("limit")
				p += "?limit=" + strconv.Itoa(limit)
			}
			raw, status, err := apiCall("POST", p, nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
	c.Flags().Int("limit", 0, "Max rows to embed this call (server default/cap applies).")
	return c
}

func newCollectionsGetRecordCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get-record <name> <external-id>",
		Short: "Fetch one record by its source id (external_id).",
		Long: `Fetch a single record by its source id (external_id) and return its exposed
attributes. Read-only.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := "/collections/" + url.PathEscape(args[0]) + "/records/" + url.PathEscape(args[1])
			raw, status, err := apiCall("GET", p, nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
}

func newCollectionsDeleteRecordCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete-record <name> <external-id>",
		Short: "Tombstone a single record so it stops appearing in queries.",
		Long: `Tombstone a single record by its source id (external_id) so it stops appearing
in queries. Bearer-only (a mere ingest-URL holder must not be able to delete).
Requires an admin/member role.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := "/collections/" + url.PathEscape(args[0]) + "/records/" + url.PathEscape(args[1])
			raw, status, err := apiCall("DELETE", p, nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
}

func newCollectionsReconcileCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "reconcile <name>",
		Short: "Tombstone records absent from the producer's live id set (catch missed deletes).",
		Long: `Tombstone every record whose id is NOT in the producer's live set — this catches
deletes the webhook missed. Supply the live ids as a comma-separated --live-ids
or, for large sets, a JSON array file via --live-ids-file. An EMPTY live set is
refused (it would wipe the collection). Requires an admin/member role.

Examples:
  blenau collections reconcile productos --live-ids 12,15,20
  blenau collections reconcile productos --live-ids-file live_ids.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file, _ := cmd.Flags().GetString("live-ids-file")
			inline, _ := cmd.Flags().GetStringSlice("live-ids")
			var liveIDs []interface{}
			switch {
			case file != "":
				arr, err := readJSONArray(file)
				if err != nil {
					return err
				}
				liveIDs = arr
			case len(inline) > 0:
				for _, s := range inline {
					if s = strings.TrimSpace(s); s != "" {
						liveIDs = append(liveIDs, s)
					}
				}
			default:
				return fmt.Errorf("provide --live-ids or --live-ids-file (an empty live set is refused)")
			}
			b, _ := json.Marshal(map[string]interface{}{"live_ids": liveIDs})
			raw, status, err := apiCall("POST", "/collections/"+url.PathEscape(args[0])+"/reconcile", b)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
	c.Flags().StringSlice("live-ids", nil, "Comma-separated live record ids.")
	c.Flags().String("live-ids-file", "", "Path to a JSON array of live record ids (for large sets).")
	return c
}

func newCollectionsRotateSecretCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rotate-secret <name>",
		Short: "Regenerate the ONE-TIME ingest URL (recovery + security rotation).",
		Long: `Regenerate the collection's ingest URL — both the recovery path (the URL is only
ever shown once, so this is how you get a working one back if it was lost) and
the security-rotation path. The previous URL stops working IMMEDIATELY, so update
it wherever it is configured (Odoo, Make…). Records are NOT affected — this
changes the door, never the data behind it. Requires an admin/member role.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, status, err := apiCall("POST", "/collections/"+url.PathEscape(args[0])+"/rotate-secret", nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, humanIngestURL(cmd))
		},
	}
}

func newCollectionsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a collection and ALL its records (irreversible).",
		Long: `Delete a collection and every record it holds. Irreversible. Requires an
admin/member role.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, status, err := apiCall("DELETE", "/collections/"+url.PathEscape(args[0]), nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, nil)
		},
	}
}

// humanIngestURL prints the one-time ingest URL block shared by create and
// rotate-secret. The URL is shown ONCE, so surface it prominently.
func humanIngestURL(cmd *cobra.Command) func([]byte) error {
	return func(b []byte) error {
		var m map[string]interface{}
		if err := json.Unmarshal(b, &m); err != nil {
			cmd.OutOrStdout().Write(norm.NFC.Bytes(b))
			return nil
		}
		w := cmd.OutOrStdout()
		if name, ok := m["name"].(string); ok && name != "" {
			fmt.Fprintf(w, "collection: %s\n", norm.NFC.String(name))
		}
		if u, ok := m["ingest_url"].(string); ok {
			fmt.Fprintf(w, "ingest_url: %s\n", norm.NFC.String(u))
		}
		fmt.Fprintln(w, "\n⚠  Shown ONCE — Blenau stores only a hash. Save it now.")
		if note, ok := m["note"].(string); ok && note != "" {
			fmt.Fprintf(w, "%s\n", norm.NFC.String(note))
		}
		return nil
	}
}

// readJSONArray reads a file and asserts it decodes to a JSON array.
func readJSONArray(path string) ([]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var arr []interface{}
	if err := json.Unmarshal(data, &arr); err != nil {
		return nil, fmt.Errorf("%s must contain a JSON array: %w", path, err)
	}
	return arr, nil
}
