package cmd

import (
	"fmt"
	"net/url"

	"github.com/spf13/cobra"
	"golang.org/x/text/unicode/norm"
)

// NewAuditCmd builds `blenau audit ...`.
func NewAuditCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "audit",
		Short: "Audit links and knowledge events.",
	}
	c.PersistentFlags().Bool("json", false, "Emit JSON instead of human format.")
	c.AddCommand(newAuditLinksCmd())
	c.AddCommand(newAuditLogCmd())
	return c
}

func newAuditLinksCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "links",
		Short: "Audit GitHub links across the knowledge base.",
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, status, err := apiCall("GET", "/github/audit-links", nil)
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

func newAuditLogCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "log",
		Short: "Show the knowledge audit log.",
		RunE: func(cmd *cobra.Command, args []string) error {
			limit, _ := cmd.Flags().GetInt("limit")
			eventType, _ := cmd.Flags().GetString("event-type")
			q := url.Values{}
			if limit > 0 {
				q.Set("limit", fmt.Sprintf("%d", limit))
			}
			if eventType != "" {
				q.Set("event_type", eventType)
			}
			path := "/knowledge/audit-log"
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
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
	c.Flags().Int("limit", 0, "Max events to return")
	c.Flags().String("event-type", "", "Filter by event type")
	return c
}
