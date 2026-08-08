package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/text/unicode/norm"
)

// NewHealthCmd builds `blenau health ...`.
//
// Brain health had no CLI and no MCP tool at all, while the feature's own
// design note says customers fix their docs by driving an agent. So the product
// manufactured remediation prompts for agents and the only way to deliver one
// was a human copying it out of the dashboard. This is that missing door.
func NewHealthCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "health",
		Short: "What is wrong with this workspace's documentation, and how to fix it.",
	}
	c.PersistentFlags().Bool("json", false, "Emit JSON instead of human format.")
	c.AddCommand(newHealthListCmd())
	c.AddCommand(newHealthRepairCmd())
	return c
}

func healthQuery(cmd *cobra.Command, group bool) string {
	q := url.Values{}
	if v, _ := cmd.Flags().GetString("path"); v != "" {
		q.Set("path", v)
	}
	if v, _ := cmd.Flags().GetString("type"); v != "" {
		q.Set("type", v)
	}
	if v, _ := cmd.Flags().GetString("severity"); v != "" {
		q.Set("severity", v)
	}
	if refresh, _ := cmd.Flags().GetBool("refresh"); refresh {
		q.Set("refresh", "true")
	}
	if group {
		q.Set("group", "true")
	}
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}

type healthGroup struct {
	Type       string   `json:"type"`
	Severity   string   `json:"severity"`
	Title      string   `json:"title"`
	Count      int      `json:"count"`
	Documents  int      `json:"documents"`
	Repairable bool     `json:"repairable"`
	Ambiguous  bool     `json:"ambiguous"`
	Paths      []string `json:"paths"`
	Prompt     string   `json:"prompt"`
}

type healthReport struct {
	Score     int           `json:"score"`
	TotalDocs int           `json:"total_documents"`
	Groups    []healthGroup `json:"groups"`
	Summary   struct {
		Critical   int `json:"critical"`
		Warning    int `json:"warning"`
		Suggestion int `json:"suggestion"`
	} `json:"summary"`
}

func newHealthListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "list",
		Short: "List findings grouped by casuistry, with one prompt per group.",
		Long: "Grouped by default: the shared procedure is printed once and every\n" +
			"affected document listed beside it. Reading 177 findings one by one,\n" +
			"each carrying its own copy of the same instruction, costs more context\n" +
			"than doing the work.\n\n" +
			"Use --prompt <type> to print JUST that group's instruction, ready to\n" +
			"hand to an agent.",
		RunE: func(cmd *cobra.Command, args []string) error {
			want, _ := cmd.Flags().GetString("prompt")
			flat, _ := cmd.Flags().GetBool("flat")
			raw, status, err := apiCall("GET", "/knowledge/health"+healthQuery(cmd, !flat), nil)
			if err != nil {
				return err
			}
			if want != "" {
				var r healthReport
				if e := json.Unmarshal(raw, &r); e == nil {
					for _, g := range r.Groups {
						if g.Type == want {
							fmt.Fprintln(cmd.OutOrStdout(), g.Prompt)
							return nil
						}
					}
					return fmt.Errorf("no findings of type %q", want)
				}
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				var r healthReport
				if e := json.Unmarshal(b, &r); e != nil || flat {
					cmd.OutOrStdout().Write(norm.NFC.Bytes(b))
					return nil
				}
				out := cmd.OutOrStdout()
				fmt.Fprintf(out, "Salud %d/100  ·  %d documentos  ·  %d crítico, %d atención, %d sugerencia\n\n",
					r.Score, r.TotalDocs, r.Summary.Critical, r.Summary.Warning, r.Summary.Suggestion)
				if len(r.Groups) == 0 {
					fmt.Fprintln(out, "Sin hallazgos.")
					return nil
				}
				for _, g := range r.Groups {
					how := "necesita criterio — usá el prompt"
					if g.Repairable {
						how = "se arregla solo: blenau health repair --type " + g.Type
					}
					fmt.Fprintf(out, "%-22s %3d hallazgos en %3d documentos  [%s]\n",
						g.Type, g.Count, g.Documents, g.Severity)
					fmt.Fprintf(out, "  %s\n", g.Title)
					fmt.Fprintf(out, "  → %s\n", how)
					if len(g.Paths) > 0 {
						shown := g.Paths
						if len(shown) > 3 {
							shown = shown[:3]
						}
						fmt.Fprintf(out, "  %s", strings.Join(shown, "\n  "))
						if len(g.Paths) > 3 {
							fmt.Fprintf(out, "\n  … y %d más", len(g.Paths)-3)
						}
						fmt.Fprintln(out)
					}
					fmt.Fprintln(out)
				}
				fmt.Fprintln(out, "Prompt completo de un grupo:  blenau health list --prompt <type>")
				return nil
			})
		},
	}
	c.Flags().String("path", "", "Only findings under this path prefix.")
	c.Flags().String("type", "", "Comma-separated finding types.")
	c.Flags().String("severity", "", "Comma-separated: critical,warning,suggestion.")
	c.Flags().Bool("refresh", false, "Re-check GitHub (costs API calls).")
	c.Flags().Bool("flat", false, "One entry per finding instead of per casuistry.")
	c.Flags().String("prompt", "", "Print only this group's agent prompt.")
	return c
}

func newHealthRepairCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "repair",
		Short: "Close an entire class of findings that needs no judgement.",
		Long: "Only the classes with nothing to decide are repairable\n" +
			"(sources_unrecorded, drift_uningested). Anything else is refused and\n" +
			"the refusal prints the prompt to use instead — because for a damaged\n" +
			"document the two plausible remedies are opposite ones, and guessing\n" +
			"wrong destroys content.",
		RunE: func(cmd *cobra.Command, args []string) error {
			t, _ := cmd.Flags().GetString("type")
			if t == "" {
				return fmt.Errorf("--type is required (e.g. sources_unrecorded)")
			}
			body := map[string]any{"type": t}
			if dry, _ := cmd.Flags().GetBool("dry-run"); dry {
				body["dry_run"] = true
			}
			if p, _ := cmd.Flags().GetString("path-prefix"); p != "" {
				body["path_prefix"] = p
			}
			if n, _ := cmd.Flags().GetInt("limit"); n > 0 {
				body["limit"] = n
			}
			b, err := json.Marshal(body)
			if err != nil {
				return err
			}
			raw, status, err := apiCall("POST", "/knowledge/health/repair-batch", b)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				var r struct {
					Type      string `json:"type"`
					DryRun    bool   `json:"dry_run"`
					Matched   int    `json:"matched"`
					Repaired  int    `json:"repaired"`
					Truncated bool   `json:"truncated"`
					Failed    []struct {
						Path    string `json:"path"`
						Outcome string `json:"outcome"`
					} `json:"failed"`
				}
				if e := json.Unmarshal(b, &r); e != nil {
					cmd.OutOrStdout().Write(norm.NFC.Bytes(b))
					return nil
				}
				out := cmd.OutOrStdout()
				verb := "reparados"
				if r.DryRun {
					verb = "se repararían (dry-run, nada escrito)"
				}
				fmt.Fprintf(out, "%s: %d de %d %s\n", r.Type, r.Repaired, r.Matched, verb)
				if r.Truncated {
					fmt.Fprintln(out, "⚠ tope alcanzado — quedan más. Volvé a correrlo.")
				}
				for _, f := range r.Failed {
					fmt.Fprintf(out, "  sin resolver: %s (%s)\n", f.Path, f.Outcome)
				}
				return nil
			})
		},
	}
	c.Flags().String("type", "", "Finding type to repair. REQUIRED.")
	c.Flags().String("path-prefix", "", "Only documents under this prefix.")
	c.Flags().Bool("dry-run", false, "Report what would change without writing.")
	c.Flags().Int("limit", 0, "Cap the batch (default 500, max 1000).")
	_ = c.MarkFlagRequired("type")
	return c
}
