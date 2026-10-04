package store

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// PocketTelemetry reads retained execution facts and exactly attributed usage.
// The cursor is an exclusive attempt number; one page contains at most 100 attempts.
func (s *Store) PocketTelemetry(ctx context.Context, taskID string, after int64) (domain.PocketTelemetryPage, error) {
	page := domain.PocketTelemetryPage{TaskID: taskID, Attempts: []domain.PocketAttemptTelemetry{}}
	if after < 0 {
		return page, domain.ErrPocketInvalid
	}
	var exists string
	if err := s.readDB.QueryRowContext(ctx, "SELECT id FROM pocket_tasks WHERE id=?", taskID).Scan(&exists); err != nil {
		return page, pocketLookupError(err, taskID)
	}
	rows, err := s.readDB.QueryContext(ctx, pocketExecutionSelect+" WHERE task_id=? AND attempt_number>? ORDER BY attempt_number LIMIT 101", taskID, after)
	if err != nil {
		return page, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		execution, err := scanPocketExecution(rows)
		if err != nil {
			return page, err
		}
		if len(page.Attempts) == 100 {
			page.NextAfter = page.Attempts[99].Execution.AttemptNumber
			break
		}
		page.Attempts = append(page.Attempts, domain.PocketAttemptTelemetry{Execution: execution})
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if err := rows.Close(); err != nil {
		return page, err
	}
	for i := range page.Attempts {
		attempt := &page.Attempts[i]
		_, attempt.ValidationState, err = s.pocketValidationForExecution(ctx, attempt.Execution)
		if err != nil {
			return page, err
		}
		aggregates, err := s.qr.AggregateUsageByPocketExecution(ctx, attempt.Execution.ID)
		if err != nil {
			return page, fmt.Errorf("read pocket attempt usage: %w", err)
		}
		for _, aggregate := range aggregates {
			attempt.Aggregates = append(attempt.Aggregates, usageAggregateFromGen(gen.AggregateUsageBySessionHarnessModelRow(aggregate)))
		}
	}
	return page, nil
}
