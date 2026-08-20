package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"
	"golang.org/x/text/unicode/norm"
)

// NewDocsCmd builds `blenau docs ...`.
func NewDocsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "docs",
		Short: "Read documents from the knowledge base.",
	}
	c.PersistentFlags().Bool("json", false, "Emit JSON instead of human format.")
	c.AddCommand(newDocsListCmd())
	c.AddCommand(newDocsGetCmd())
	c.AddCommand(newDocsStructureCmd())
	c.AddCommand(newDocsSectionCmd())
	c.AddCommand(newDocsDeleteCmd())
	c.AddCommand(newDocsSetSourcesCmd())
	return c
}

func newDocsListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "list",
		Short: "List knowledge documents, optionally filtered.",
		Long: `List knowledge documents the caller can read. Filters are AND-composed and
applied server-side inside your scope (they narrow, never widen):

  --prefix       only docs whose path starts with this (e.g. ganemo/infra-odoo/)
  --status       ready | pending | failed
  --source-type  github | manual | crystallize

Each row shows path, status, source and title.

Examples:
  blenau docs list --prefix ganemo/infra-odoo/
  blenau docs list --status failed
  blenau docs list --source-type manual`,
		RunE: func(cmd *cobra.Command, args []string) error {
			q := url.Values{}
			if v, _ := cmd.Flags().GetString("prefix"); v != "" {
				q.Set("prefix", v)
			}
			if v, _ := cmd.Flags().GetString("status"); v != "" {
				q.Set("status", v)
			}
			if v, _ := cmd.Flags().GetString("source-type"); v != "" {
				q.Set("source_type", v)
			}
			path := "/knowledge/documents"
			if enc := q.Encode(); enc != "" {
				path += "?" + enc
			}
			raw, status, err := apiCall("GET", path, nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				var resp struct {
					Documents []struct {
						Path       string `json:"path"`
						Title      string `json:"title"`
						Status     string `json:"status"`
						SourceType string `json:"source_type"`
					} `json:"documents"`
					// Which "nothing" this is. A connected repo with no
					// documents used to print the same "No documents." as a
					// prefix that does not exist, so the reader concluded the
					// path was gone.
					PrefixStatus  string   `json:"prefix_status"`
					PrefixDetail  string   `json:"prefix_detail"`
					KnownPrefixes []string `json:"known_prefixes"`
				}
				if err := json.Unmarshal(b, &resp); err != nil {
					cmd.OutOrStdout().Write(norm.NFC.Bytes(b))
					return nil
				}
				w := cmd.OutOrStdout()
				if len(resp.Documents) == 0 {
					switch resp.PrefixStatus {
					case "connected_but_empty":
						fmt.Fprintln(w, "No documents here yet — but the path exists.")
						fmt.Fprintln(w, norm.NFC.String(resp.PrefixDetail))
					case "unknown_prefix":
						fmt.Fprintln(w, norm.NFC.String(resp.PrefixDetail))
						for _, p := range resp.KnownPrefixes {
							fmt.Fprintln(w, "  "+norm.NFC.String(p))
						}
					default:
						fmt.Fprintln(w, "No documents.")
					}
					return nil
				}
				for _, d := range resp.Documents {
					fmt.Fprintf(w, "%-9s %-10s %s  %s\n",
						norm.NFC.String(d.Status), norm.NFC.String(d.SourceType),
						norm.NFC.String(d.Path), norm.NFC.String(d.Title))
				}
				return nil
			})
		},
	}
	c.Flags().String("prefix", "", "Only docs whose path starts with this prefix.")
	c.Flags().String("status", "", "Filter by status: ready | pending | failed.")
	c.Flags().String("source-type", "", "Filter by source: github | manual | crystallize.")
	return c
}

func newDocsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <path>",
		Short: "Get a knowledge document by path.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := args[0]
			raw, status, err := apiCall("GET", "/knowledge/documents/by-path/"+escapePath(p), nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				cmd.OutOrStdout().Write(norm.NFC.Bytes(b))
				return nil
			})
		},
	}
}

func newDocsStructureCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "structure <path>",
		Short: "Get a document's heading structure.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := args[0]
			raw, status, err := apiCall("GET", "/knowledge/structure/"+escapePath(p), nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				cmd.OutOrStdout().Write(norm.NFC.Bytes(b))
				return nil
			})
		},
	}
}

func newDocsSectionCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "section <path>",
		Short: "Get a single section of a document.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := args[0]
			heading, _ := cmd.Flags().GetString("heading")
			if heading == "" {
				return fmt.Errorf("--heading is required")
			}
			path := "/knowledge/section/" + escapePath(p) + "?heading=" + url.QueryEscape(heading)
			raw, status, err := apiCall("GET", path, nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				cmd.OutOrStdout().Write(norm.NFC.Bytes(b))
				return nil
			})
		},
	}
	c.Flags().String("heading", "", "Heading text to fetch.")
	return c
}

// escapePath escapes path segments while preserving '/'.
func escapePath(p string) string {
	parts := splitPath(p)
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return joinPath(parts)
}

func splitPath(p string) []string {
	out := []string{}
	cur := ""
	for _, r := range p {
		if r == '/' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	out = append(out, cur)
	return out
}

func joinPath(parts []string) string {
	out := ""
	for i, s := range parts {
		if i > 0 {
			out += "/"
		}
		out += s
	}
	return out
}

// newDocsSetSourcesCmd records provenance on an EXISTING document without
// re-sending its body. Re-ingesting a document just to add its sources is what
// put a customer's agent on the path where identical content silently dropped
// the metadata; "say where this came from" is not a content edit.
func newDocsSetSourcesCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "set-sources",
		Short: "Record where an existing document came from, without re-sending it.",
		RunE: func(cmd *cobra.Command, args []string) error {
			doc, _ := cmd.Flags().GetString("path")
			raw, _ := cmd.Flags().GetStringArray("source")
			if doc == "" || len(raw) == 0 {
				return fmt.Errorf("--path and at least one --source are required")
			}
			parsed, err := parseSources(raw)
			if err != nil {
				return err
			}
			body := map[string]any{"path": doc, "sources": parsed}
			if metaOnly, _ := cmd.Flags().GetBool("metadata-only"); metaOnly {
				body["update_body"] = false
			}
			if dry, _ := cmd.Flags().GetBool("dry-run"); dry {
				body["dry_run"] = true
			}
			b, err := json.Marshal(body)
			if err != nil {
				return err
			}
			resp, status, err := apiCall("POST", "/knowledge/documents/sources", b)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, resp, status, nil)
		},
	}
	c.Flags().String("path", "", "Tenant-level doc path. REQUIRED.")
	c.Flags().StringArray("source", nil, "Provenance as type=ref (repeatable). REQUIRED.")
	c.Flags().Bool("metadata-only", false, "Do not touch the document body.")
	c.Flags().Bool("dry-run", false, "Show what would change without writing.")
	c.Flags().Bool("json", false, "Emit JSON instead of human format.")
	_ = c.MarkFlagRequired("path")
	_ = c.MarkFlagRequired("source")
	return c
}
