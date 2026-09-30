package cli

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func newPocketCommand(c *commandContext) *cobra.Command {
	cmd := &cobra.Command{Use: "pocket", Short: "Inspect deterministic task policy and decision history"}
	for _, history := range []bool{false, true} {
		name, short := "policy", "Show derived readiness, budgets and reserved actions"
		if history {
			name, short = "decisions", "Show retained decisions explaining permitted and denied actions"
		}
		var asJSON bool
		var before int64
		child := &cobra.Command{Use: name + " <task-id>", Short: short, Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
				return usageError{fmt.Errorf("one task ID is required")}
			}
			return nil
		}, RunE: func(cmd *cobra.Command, args []string) error {
			if history {
				if before < 0 {
					return usageError{fmt.Errorf("before must be nonnegative")}
				}
				var page domain.PocketDecisionPage
				path := "/internal/pocket/tasks/" + url.PathEscape(args[0]) + "/decisions"
				if before > 0 {
					path += fmt.Sprintf("?before=%d", before)
				}
				if err := c.doJSONPath(cmd.Context(), http.MethodGet, path, nil, &page); err != nil {
					return err
				}
				if asJSON {
					return writeJSON(cmd.OutOrStdout(), page)
				}
				for _, d := range page.Decisions {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%d %s %s allowed=%t %s\n", d.Sequence, d.ID, d.Outcome.State, d.Outcome.Allowed, d.Outcome.Reason)
				}
				if page.NextBefore > 0 {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Older decisions: --before %d\n", page.NextBefore)
				}
				return nil
			}
			var view domain.PocketPolicyView
			if err := c.doJSONPath(cmd.Context(), http.MethodGet, "/internal/pocket/tasks/"+url.PathEscape(args[0])+"/policy", nil, &view); err != nil {
				return err
			}
			if asJSON {

				return writeJSON(cmd.OutOrStdout(), view)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (allowed=%t)\nAttempts %d/%d; retries %d/%d; escalations %d/%d\n", view.Policy.State, view.Policy.Reason, view.Policy.Allowed, view.Facts.Attempts, view.Facts.Config.MaxAttempts, view.Facts.Retries, view.Facts.Config.MaxRetries, view.Facts.Escalations, view.Facts.Config.MaxEscalations)
			for _, id := range view.Facts.BlockedBy {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Blocked by %s\n", id)
			}
			return nil
		}}
		if history {
			child.Flags().Int64Var(&before, "before", 0, "Read decisions older than this sequence")
		}
		child.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
		cmd.AddCommand(child)
	}
	return cmd
}
