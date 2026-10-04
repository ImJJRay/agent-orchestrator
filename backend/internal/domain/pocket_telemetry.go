package domain

// PocketTelemetryPage is a bounded read of attempts, in attempt-number order.
type PocketTelemetryPage struct {
	TaskID    string                   `json:"taskId"`
	Attempts  []PocketAttemptTelemetry `json:"attempts"`
	NextAfter int64                    `json:"nextAfter,omitempty"`
}

// PocketAttemptTelemetry reports observed usage only. Partial means exact
// attribution exists, not that collection (including subagents) is complete.
type PocketAttemptTelemetry struct {
	Execution               PocketExecution       `json:"execution"`
	ValidationState         PocketValidationState `json:"validationState"`
	DurationMillis          *int64                `json:"durationMillis"`
	UsageScope              string                `json:"usageScope"`
	Coverage                string                `json:"coverage"`
	Reason                  string                `json:"reason"`
	EventCount              int64                 `json:"eventCount"`
	Models                  []string              `json:"models"`
	InputTokens             *int64                `json:"inputTokens"`
	CachedInputTokens       *int64                `json:"cachedInputTokens"`
	OutputTokens            *int64                `json:"outputTokens"`
	EstimatedCostNanos      *int64                `json:"estimatedCostNanos"`
	CostCoverage            string                `json:"costCoverage"`
	CostProviderAttribution string                `json:"costProviderAttribution"`
	Aggregates              []UsageModelAggregate `json:"-"`
}
