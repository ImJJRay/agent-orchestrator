// Package pockettelemetry derives honest attempt-scoped usage summaries.
package pockettelemetry

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/usage"
)

// Store supplies durable execution and normalized usage facts.
type Store interface {
	PocketTelemetry(context.Context, string, int64) (domain.PocketTelemetryPage, error)
}

// Read derives a bounded attempt page. No timestamp-based attribution is used.
func Read(ctx context.Context, store Store, taskID string, after int64) (domain.PocketTelemetryPage, error) {
	page, err := store.PocketTelemetry(ctx, taskID, after)
	if err != nil {
		return page, err
	}
	for i := range page.Attempts {
		a := &page.Attempts[i]
		a.UsageScope, a.Coverage, a.Reason = "attempt", "unavailable", "no_exact_native_turn_usage"
		a.CostCoverage, a.CostProviderAttribution = "unknown", "unknown"
		a.Models = []string{}
		if a.Execution.StartedAt != nil && a.Execution.CompletedAt != nil && !a.Execution.CompletedAt.Before(*a.Execution.StartedAt) {
			duration := a.Execution.CompletedAt.Sub(*a.Execution.StartedAt).Milliseconds()
			a.DurationMillis = &duration
		}
		for _, model := range a.Aggregates {
			a.EventCount += model.Cost.EventCount
			a.Models = append(a.Models, model.ModelID)
		}
		if a.EventCount == 0 {
			continue
		}
		totals, err := usage.SummarizeModels(a.Aggregates)
		if err != nil {
			return domain.PocketTelemetryPage{}, err
		}
		a.Coverage, a.Reason = "partial", "exact_codex_turn_events_only"
		a.InputTokens, a.CachedInputTokens, a.OutputTokens = totals.InputTokens, totals.CachedInputTokens, totals.OutputTokens
		if totals.EstimatedCost != nil {
			a.EstimatedCostNanos = &totals.EstimatedCost.TotalNanos
			a.CostCoverage = string(totals.EstimatedCost.Coverage)
			a.CostProviderAttribution = string(totals.EstimatedCost.ProviderAttribution)
		}
	}
	return page, nil
}
