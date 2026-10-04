package pockettelemetry

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type telemetryStore struct{ page domain.PocketTelemetryPage }

func (s telemetryStore) PocketTelemetry(context.Context, string, int64) (domain.PocketTelemetryPage, error) {
	return s.page, nil
}

func TestPocketTelemetryUnknownAndKnownZero(t *testing.T) {
	for _, priced := range []bool{false, true} {
		cost := domain.UsageCostAggregate{EventCount: 1}
		if priced {
			cost.PricedEventCount, cost.ObservedCostEventCount = 1, 1
		}
		zero := int64(0)
		store := telemetryStore{domain.PocketTelemetryPage{Attempts: []domain.PocketAttemptTelemetry{
			{},
			{Aggregates: []domain.UsageModelAggregate{{ModelID: "model", Cost: cost, Tokens: domain.UsageTokenMetrics{InputTokens: &zero}}}},
		}}}
		page, err := Read(context.Background(), store, "task", 0)
		if err != nil {
			t.Fatal(err)
		}
		unknown, observed := page.Attempts[0], page.Attempts[1]
		if unknown.Coverage != "unavailable" || unknown.EstimatedCostNanos != nil || unknown.InputTokens != nil || unknown.DurationMillis != nil {
			t.Fatalf("unavailable: %+v", unknown)
		}
		if observed.Coverage != "partial" || observed.InputTokens == nil || *observed.InputTokens != 0 || observed.OutputTokens != nil {
			t.Fatalf("observed zero: %+v", observed)
		}
		if priced {
			if observed.EstimatedCostNanos == nil || *observed.EstimatedCostNanos != 0 || observed.CostCoverage != "complete" {
				t.Fatalf("known zero cost: %+v", observed)
			}
		} else if observed.EstimatedCostNanos != nil || observed.CostCoverage != "unknown" {
			t.Fatalf("unknown cost: %+v", observed)
		}
		encoded, err := json.Marshal(unknown)
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"inputTokens", "cachedInputTokens", "outputTokens", "estimatedCostNanos", "durationMillis"} {
			if !strings.Contains(string(encoded), `"`+field+`":null`) {
				t.Fatalf("missing explicit null for %s: %s", field, encoded)
			}
		}
	}
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
