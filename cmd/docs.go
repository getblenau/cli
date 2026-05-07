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
	return c
}

func newDocsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all knowledge documents.",
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, status, err := apiCall("GET", "/knowledge/documents", nil)
			if err != nil {
				return err
			}
			return emitOrFail(cmd, raw, status, func(b []byte) error {
				var resp struct {
					Documents []struct {
						Path    string `json:"path"`
						Title   string `json:"title"`
						Version int    `json:"version"`
					} `json:"documents"`
				}
				if err := json.Unmarshal(b, &resp); err != nil {
					cmd.OutOrStdout().Write(norm.NFC.Bytes(b))
					return nil
				}
				w := cmd.OutOrStdout()
				if len(resp.Documents) == 0 {
					fmt.Fprintln(w, "No documents.")
					return nil
				}
				for _, d := range resp.Documents {
					fmt.Fprintf(w, "%s  (v%d) %s\n",
						norm.NFC.String(d.Path), d.Version, norm.NFC.String(d.Title))
				}
				return nil
			})
		},
	}
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
