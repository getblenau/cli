package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/text/unicode/norm"
)

// SearchResult is a single result row from /knowledge/search.
type SearchResult struct {
	Path      string  `json:"path"`
	Snippet   string  `json:"snippet"`
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
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := args[0]
			for _, a := range args[1:] {
				query += " " + a
			}
			topK, _ := cmd.Flags().GetInt("top-k")
			asJSON, _ := cmd.Flags().GetBool("json")
			if !cmd.Flags().Changed("json") {
				if pj, _ := cmd.Root().PersistentFlags().GetBool("json"); pj {
					asJSON = true
				}
			}

			cfg, err := LoadConfig()
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("not logged in: run 'blenau login --token <tk>' first")
			}
			if err != nil {
				return err
			}
			if cfg.Token == "" {
				return fmt.Errorf("config has no token: run 'blenau login --token <tk>' first")
			}
			if cfg.APIURL == "" {
				cfg.APIURL = DefaultAPIURL
			}

			body, _ := json.Marshal(map[string]interface{}{
				"query": query,
				"top_k": topK,
			})
			req, err := http.NewRequest("POST", cfg.APIURL+"/knowledge/search", bytes.NewReader(body))
			if err != nil {
				return err
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+cfg.Token)

			client := &http.Client{Timeout: 30 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				return fmt.Errorf("call %s: %w", req.URL, err)
			}
			defer resp.Body.Close()
			raw, err := io.ReadAll(resp.Body)
			if err != nil {
				return err
			}
			if resp.StatusCode >= 400 {
				return fmt.Errorf("api error %d: %s", resp.StatusCode, string(raw))
			}

			if asJSON {
				// Verbatim, NFC-normalized, no escape of non-ASCII.
				out := norm.NFC.Bytes(raw)
				cmd.OutOrStdout().Write(out)
				if len(out) == 0 || out[len(out)-1] != '\n' {
					fmt.Fprintln(cmd.OutOrStdout())
				}
				return nil
			}

			var sr SearchResponse
			if err := json.Unmarshal(raw, &sr); err != nil {
				return fmt.Errorf("parse response: %w", err)
			}
			w := cmd.OutOrStdout()
			if sr.Count == 0 {
				fmt.Fprintf(w, "No results for %q.\n", sr.Query)
				return nil
			}
			fmt.Fprintf(w, "%d result(s) for %q:\n\n", sr.Count, sr.Query)
			for i, r := range sr.Results {
				fmt.Fprintf(w, "%d. [%.3f] %s\n", i+1, r.Relevance, norm.NFC.String(r.Path))
				if r.Snippet != "" {
					fmt.Fprintf(w, "   %s\n", norm.NFC.String(r.Snippet))
				}
				fmt.Fprintln(w)
			}
			return nil
		},
	}
	c.Flags().Int("top-k", 10, "Max results to return.")
	c.Flags().Bool("json", false, "Emit JSON instead of human format.")
	return c
}
