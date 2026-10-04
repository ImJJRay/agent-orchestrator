package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/pockettelemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestPocketTelemetryExactAttributionAndReplay(t *testing.T) {
	dir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dir)
	ctx := context.Background()
	f := seedPocketFixture(t, s)
	now := time.Now().UTC()
	mustNoError(t, s.ReconcilePocketExecutionLifecycle(ctx, now))
	snapshot, ok, err := s.PocketExecutionForSession(ctx, f.session.ID, f.turn1)
	if err != nil || !ok {
		t.Fatalf("snapshot: %v %v", ok, err)
	}
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "ao.db"))
	mustNoError(t, err)
	defer func() { _ = raw.Close() }()
	_, err = raw.Exec("UPDATE conversation_branches SET provider_conversation_id='root-thread' WHERE conversation_id=?", f.convID)
	mustNoError(t, err)
	mustNoError(t, s.BindTurnToProvider(ctx, f.turn1, "scoped-turn-1", now))
	mustNoError(t, s.BindTurnToProvider(ctx, f.turn2, "scoped-turn-2", now))
	for _, payload := range []string{
		`{"providerTurnId":"scoped-turn-1","nativeTurnId":"native-1"}`,
		`{"providerTurnId":"scoped-turn-1","nativeTurnId":"native-1"}`,
		`{"providerTurnId":"scoped-turn-2","nativeTurnId":"native-2"}`,
	} {
		_, err = raw.Exec("INSERT INTO conversation_provider_events(conversation_id,session_id,method,payload_json,received_at) VALUES (?,?,'turn.started',?,?)", f.convID, f.session.ID, payload, now)
		mustNoError(t, err)
	}
	binding, err := s.UpsertUsageBinding(ctx, domain.UsageBindingRecord{SessionID: f.session.ID, Harness: domain.HarnessCodex, NativeRootID: "root-thread", State: domain.UsageBindingActive, UpdatedAt: now})
	mustNoError(t, err)
	source := mustInsertUsageSource(t, s, now, domain.UsageSourceRecord{BindingID: binding.ID, Kind: domain.UsageSourceCodexRollout, NativeSessionID: "root-thread", ArtifactPath: "/root.jsonl", State: domain.UsageSourcePending})
	first := usageEvent("first", canonicalUsageTokens(100, 40, 60, 20))
	first.NativeTurnID = "native-1"
	second := usageEvent("second", canonicalUsageTokens(200, 50, 150, 30))
	second.NativeTurnID = "native-2"
	unknown := usageEvent("unknown", canonicalUsageTokens(900, 0, 900, 90))
	apply := func(offset int64, events []domain.ModelUsageEvent) {
		t.Helper()
		mustNoError(t, s.ApplyUsageChunk(ctx, source.ID, offset, now, domain.SourceCursorState{ByteOffset: offset + 1, State: domain.UsageSourceActive, UpdatedAt: now}, events))
	}
	apply(0, []domain.ModelUsageEvent{first, second, unknown})
	apply(1, []domain.ModelUsageEvent{first, second})
	read := func() domain.PocketTelemetryPage {
		t.Helper()
		page, err := pockettelemetry.Read(ctx, s, snapshot.Task.ID, 0)
		mustNoError(t, err)
		return page
	}
	page := read()
	if len(page.Attempts) != 2 {
		t.Fatalf("attempts: %+v", page)
	}
	for i, want := range []int64{100, 200} {
		a := page.Attempts[i]
		if a.Coverage != "partial" || a.EventCount != 1 || a.InputTokens == nil || *a.InputTokens != want || a.EstimatedCostNanos != nil {
			t.Fatalf("attempt %d: %+v", i, a)
		}
	}
	if page.Attempts[1].Execution.PriorExecutionID != page.Attempts[0].Execution.ID {
		t.Fatal("retry lineage lost")
	}
	// Matching turn labels alone cannot substitute for root/branch ownership.
	for _, root := range []string{"other-root", ""} {
		_, err = raw.Exec("UPDATE conversation_branches SET provider_conversation_id=? WHERE conversation_id=?", root, f.convID)
		mustNoError(t, err)
		page = read()
		if page.Attempts[0].Coverage != "unavailable" || page.Attempts[1].Coverage != "unavailable" {
			t.Fatalf("wrong branch root %q: %+v", root, page)
		}
	}
	_, err = raw.Exec("UPDATE conversation_branches SET provider_conversation_id='root-thread' WHERE conversation_id=?", f.convID)
	mustNoError(t, err)
	// One raw identity mapping to two attempts must never charge either one.
	_, err = raw.Exec("INSERT INTO conversation_provider_events(conversation_id,session_id,method,payload_json,received_at) VALUES (?,?,'turn.started',?,?)", f.convID, f.session.ID, `{"providerTurnId":"scoped-turn-2","nativeTurnId":"native-1"}`, now)
	mustNoError(t, err)
	page = read()
	if page.Attempts[0].Coverage != "unavailable" || page.Attempts[0].InputTokens != nil || page.Attempts[1].EventCount != 1 {
		t.Fatalf("ambiguous attribution: %+v", page)
	}
	// A child rollout with the same turn label is not the root's usage.
	_, err = raw.Exec("UPDATE usage_sources SET native_session_id='child' WHERE id=?", source.ID)
	mustNoError(t, err)
	page = read()
	if page.Attempts[1].Coverage != "unavailable" {
		t.Fatalf("child attribution: %+v", page)
	}
	if _, err = s.PocketTelemetry(ctx, "absent", 0); !errors.Is(err, domain.ErrPocketNotFound) {
		t.Fatalf("missing task: %v", err)
	}
	if _, err = s.PocketTelemetry(ctx, snapshot.Task.ID, -1); !errors.Is(err, domain.ErrPocketInvalid) {
		t.Fatalf("negative cursor: %v", err)
	}
	page, err = s.PocketTelemetry(ctx, snapshot.Task.ID, 1)
	mustNoError(t, err)
	if len(page.Attempts) != 1 || page.Attempts[0].Execution.AttemptNumber != 2 {
		t.Fatalf("cursor: %+v", page)
	}
	prior := page.Attempts[0].Execution.ID
	for number := 3; number <= 101; number++ {
		id := fmt.Sprintf("page-%d", number)
		_, err = raw.Exec(`INSERT INTO pocket_executions(id,task_id,worker_id,attempt_number,prior_execution_id,session_id,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?)`, id, snapshot.Task.ID, snapshot.Worker.ID, number, prior, f.session.ID, now, now)
		mustNoError(t, err)
		prior = id
	}
	page, err = s.PocketTelemetry(ctx, snapshot.Task.ID, 0)
	mustNoError(t, err)
	if len(page.Attempts) != 100 || page.NextAfter != 100 {
		t.Fatalf("first page: count=%d cursor=%d", len(page.Attempts), page.NextAfter)
	}
	page, err = s.PocketTelemetry(ctx, snapshot.Task.ID, page.NextAfter)
	mustNoError(t, err)
	if len(page.Attempts) != 1 || page.Attempts[0].Execution.AttemptNumber != 101 || page.NextAfter != 0 {
		t.Fatalf("last page: %+v", page)
	}
}

func TestPocketUsageNativeTurnReplayEnrichment(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	session := seedUsageSession(t, s, domain.HarnessCodex)
	source := seedUsageSource(t, s, session, now)
	event := usageEvent("same-event", canonicalUsageTokens(10, 0, 10, 2))
	apply := func(offset int64) error {
		return s.ApplyUsageChunk(ctx, source.ID, offset, now, domain.SourceCursorState{ByteOffset: offset + 1, State: domain.UsageSourceActive, UpdatedAt: now}, []domain.ModelUsageEvent{event})
	}
	mustNoError(t, apply(0))
	event.NativeTurnID = "native-turn"
	mustNoError(t, apply(1))
	mustNoError(t, apply(2))
	event.NativeTurnID = "another-turn"
	if err := apply(3); !errors.Is(err, domain.ErrUsageSourceEventConflict) {
		t.Fatalf("conflicting identity: %v", err)
	}
	aggs, err := s.ListUsageModelAggregates(ctx, session.ID)
	mustNoError(t, err)
	if len(aggs) != 1 || aggs[0].Cost.EventCount != 1 {
		t.Fatalf("replay duplicated event: %+v", aggs)
	}
}
