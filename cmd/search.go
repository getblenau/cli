package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/text/unicode/norm"
)

// SearchResult is a single result row from /knowledge/search.
type SearchResult struct {
	Path      string  `json:"path"`
	Title     string  `json:"title"`
	Heading   string  `json:"heading"`
	Content   string  `json:"content"`
	Relevance float64 `json:"relevance"`
}

// SearchResponse mirrors the API response.
type SearchResponse struct {
	Results []SearchResult `json:"results"`
	Query   string         `json:"query"`
	Count   int            `json:"count"`
}

// NewSearchCmd builds `blenau search`.
func NewSearchCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "search <query>",
		Short: "Semantic search over the knowledge base.",
		Long: `Semantic (meaning-based) search over the workspace's knowledge documents.
Returns the most relevant sections with their path, heading and a content
snippet. This searches Knowledge only — it never returns Notes or Collections
records (use 'blenau notes recall' / 'blenau collections query' for those).`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := strings.Join(args, " ")
			topK, _ := cmd.Flags().GetInt("top-k")
			body, _ := json.Marshal(map[string]interface{}{"query": query, "top_k": topK})
			raw, status, err := apiCall("POST", "/knowledge/search", body)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				var sr SearchResponse
				if err := json.Unmarshal(b, &sr); err != nil {
					cmd.OutOrStdout().Write(norm.NFC.Bytes(b))
					return nil
				}
				w := cmd.OutOrStdout()
				if sr.Count == 0 {
					fmt.Fprintf(w, "No results for %q.\n", query)
					return nil
				}
				fmt.Fprintf(w, "%d result(s) for %q:\n\n", sr.Count, query)
				for i, r := range sr.Results {
					fmt.Fprintf(w, "%d. [%.3f] %s\n", i+1, r.Relevance, norm.NFC.String(r.Path))
					if r.Heading != "" {
						fmt.Fprintf(w, "   %s\n", norm.NFC.String(r.Heading))
					}
					if snippet := snippetOf(r.Content); snippet != "" {
						fmt.Fprintf(w, "   %s\n", norm.NFC.String(snippet))
					}
					fmt.Fprintln(w)
				}
				return nil
			})
		},
	}
	c.Flags().Int("top-k", 10, "Max results to return.")
	c.Flags().Bool("json", false, "Emit JSON instead of human format.")
	return c
}

// snippetOf returns a single-line, length-bounded preview of a result body.
func snippetOf(content string) string {
	s := strings.TrimSpace(strings.ReplaceAll(content, "\n", " "))
	const max = 160
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}
