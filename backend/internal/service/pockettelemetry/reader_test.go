package pockettelemetry

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type telemetryStore struct{ page domain.PocketTelemetryPage }

func (s telemetryStore) PocketTelemetry(context.Context, string, int64) (domain.PocketTelemetryPage, error) {
	return s.page, nil
}

func TestReadPartialCostsAndUnknownCounters(t *testing.T) {
	start := time.Unix(1000, 0)
	end := start.Add(2 * time.Second)
	zero := int64(0)
	input := int64(12)
	store := telemetryStore{domain.PocketTelemetryPage{Attempts: []domain.PocketAttemptTelemetry{{
		Execution:  domain.PocketExecution{StartedAt: &start, CompletedAt: &end},
		Aggregates: []domain.UsageModelAggregate{{Harness: domain.HarnessCodex, ModelID: "model", Tokens: domain.UsageTokenMetrics{InputTokens: &input, CachedInputTokens: &zero}, Cost: domain.UsageCostAggregate{EventCount: 2, PricedEventCount: 1, PricedTotalNanos: 10, ObservedCostEventCount: 1}}},
	}}}}
	page, err := Read(context.Background(), store, "task", 0)
	if err != nil {
		t.Fatal(err)
	}
	a := page.Attempts[0]
	if a.Coverage != "partial" || a.CostCoverage != "partial" || a.EstimatedCostNanos == nil || *a.EstimatedCostNanos != 10 || a.OutputTokens != nil || *a.CachedInputTokens != 0 || *a.DurationMillis != 2000 {
		t.Fatalf("summary: %+v", a)
	}
}
