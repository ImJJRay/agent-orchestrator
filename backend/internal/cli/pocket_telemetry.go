package cli

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func newPocketTelemetryCommand(c *commandContext) *cobra.Command {
	var asJSON bool
	var after int64
	cmd := &cobra.Command{Use: "telemetry <task-id>", Short: "Show attempt-scoped tokens and estimated costs", Args: func(_ *cobra.Command, args []string) error {
		if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
			return usageError{fmt.Errorf("one task ID is required")}
		}
		return nil
	}, RunE: func(cmd *cobra.Command, args []string) error {
		if after < 0 {
			return usageError{fmt.Errorf("after must be nonnegative")}
		}
		var page domain.PocketTelemetryPage
		path := "/internal/pocket/tasks/" + url.PathEscape(args[0]) + "/telemetry"
		if after > 0 {
			path += fmt.Sprintf("?after=%d", after)
		}
		if err := c.doJSONPath(cmd.Context(), http.MethodGet, path, nil, &page); err != nil {
			return err
		}
		if asJSON {
			return writeJSON(cmd.OutOrStdout(), page)
		}
		for _, a := range page.Attempts {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Attempt %d %s: %s; validation=%s; usage=%s (%s)\n", a.Execution.AttemptNumber, a.Execution.ID, a.Execution.State, a.ValidationState, a.Coverage, a.Reason)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Input=%s cached=%s output=%s; estimated USD nanos=%s (pricing=%s)\n", pocketMetric(a.InputTokens), pocketMetric(a.CachedInputTokens), pocketMetric(a.OutputTokens), pocketMetric(a.EstimatedCostNanos), a.CostCoverage)
		}
		if page.NextAfter > 0 {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "More attempts: --after %d\n", page.NextAfter)
		}
		return nil
	}}
	cmd.Flags().Int64Var(&after, "after", 0, "Read attempts after this attempt number")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

func pocketMetric(value *int64) string {
	if value == nil {
		return "unknown"
	}
	return fmt.Sprint(*value)
}
