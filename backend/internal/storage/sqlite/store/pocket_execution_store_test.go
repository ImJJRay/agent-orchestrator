package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type pocketFixture struct {
	session domain.SessionRecord
	convID  string
	turn1   string
	turn2   string
}

func seedPocketFixture(t *testing.T, s *sqlite.Store) pocketFixture {
	t.Helper()
	ctx := context.Background()
	seedProject(t, s, "mer")
	rec := sampleRecord("mer")
	rec.Mode = domain.SessionModeChat
	rec.Metadata.WorkspacePath = "/worktrees/mer-1"
	rec.Metadata.WorkspaceRepoPath = "/repos/mer"
	session, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	conv, err := s.CreateConversation(ctx, "conv-1", domain.ConversationScopeSession, session.ProjectID, session.ID, time.Now().UTC())
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	now := time.Now().UTC()
	created, err := s.AppendUserMessage(ctx, conv.ID, session.ID, "gen-1", domain.ConversationMessage{
		ID: "msg-1", Origin: domain.MessageOriginHuman, Text: "first attempt",
	}, "turn-1", now)
	if err != nil || !created {
		t.Fatalf("append first turn: created=%v err=%v", created, err)
	}
	created, err = s.AppendRetryUserMessage(ctx, conv.ID, session.ID, "gen-1", domain.ConversationMessage{
		ID: "msg-2", Origin: domain.MessageOriginHuman, Text: "retry",
	}, "turn-2", "turn-1", now.Add(time.Second))
	if err != nil || !created {
		t.Fatalf("append retry turn: created=%v err=%v", created, err)
	}
	return pocketFixture{session: session, convID: conv.ID, turn1: "turn-1", turn2: "turn-2"}
}

func TestPocketExecutionStateSurvivesReopenWithRetryAndValidationEvidence(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	s, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	fixture := seedPocketFixture(t, s)
	base := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)

	task, err := s.CreatePocketTask(ctx, fixture.session.ProjectID, "persist durable Pocket state", base)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := s.CreatePocketWorker(ctx, fixture.session.ID, base)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.CreatePocketExecution(
		ctx, task.ID, worker.ID, fixture.convID, fixture.turn1, "",
		domain.PocketExecutionUnknown, base,
	)
	if err != nil {
		t.Fatal(err)
	}
	first, err = s.UpdatePocketExecutionState(ctx, first.ID, domain.PocketExecutionFailed, base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreatePocketExecution(
		ctx, task.ID, worker.ID, fixture.convID, fixture.turn2, first.ID,
		domain.PocketExecutionUnknown, base.Add(2*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err = s.UpdatePocketExecutionState(ctx, second.ID, domain.PocketExecutionCompleted, base.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	req, err := s.CreatePocketValidationRequirement(
		ctx, task.ID, "", "go-test", "go test ./...", domain.PocketValidationTaskScope,
		true, true, base.Add(4*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := s.CreatePocketValidationResult(
		ctx, second.ID, req.ID, domain.PocketValidationFail, domain.PocketValidationDeterministic,
		"go test ./...", "exit 1", base.Add(5*time.Minute), base.Add(5*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	semantic, err := s.CreatePocketValidationResult(
		ctx, second.ID, req.ID, domain.PocketValidationPass, domain.PocketValidationSemantic,
		"worker-output", "model claims tests pass", base.Add(6*time.Minute), base.Add(6*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Errorf("close reopened store: %v", err)
		}
	}()

	got, ok, err := s.PocketExecutionForSession(ctx, fixture.session.ID, fixture.turn2)
	if err != nil || !ok {
		t.Fatalf("read after reopen: ok=%v err=%v", ok, err)
	}
	if got.Task.ID != task.ID || got.Worker.ID != worker.ID || got.Execution.ID != second.ID {
		t.Fatalf("durable ids changed after reopen: %#v", got)
	}
	if got.Execution.PriorExecutionID != first.ID || got.Execution.AttemptNumber != 2 {
		t.Fatalf("retry lineage=%#v", got.Execution)
	}
	if got.Execution.SessionID != fixture.session.ID || got.Execution.ConversationID != fixture.convID || got.Execution.TurnID != fixture.turn2 {
		t.Fatalf("AO execution trace=%#v", got.Execution)
	}
	if got.Execution.ProjectID != fixture.session.ProjectID ||
		got.Execution.WorkspacePath != "/worktrees/mer-1" ||
		got.Execution.WorkspaceRepoPath != "/repos/mer" {
		t.Fatalf("workspace/project trace=%#v", got.Execution)
	}
	if len(got.Attempts) != 2 || got.Attempts[0].ID != first.ID || got.Attempts[1].ID != second.ID {
		t.Fatalf("task attempts=%#v", got.Attempts)
	}
	if got.ValidationState != domain.PocketValidationFail || len(got.Validation) != 1 {
		t.Fatalf("validation=%q evidence=%#v", got.ValidationState, got.Validation)
	}
	evidence := got.Validation[0]
	if evidence.Requirement.ID != req.ID || evidence.Requirement.CheckID != "go-test" ||
		evidence.EffectiveState != domain.PocketValidationFail || len(evidence.Results) != 2 {
		t.Fatalf("validation evidence=%#v", evidence)
	}
	if evidence.Results[0].ID != semantic.ID || evidence.Results[1].ID != failed.ID {
		t.Fatalf("validation provenance ordering=%#v", evidence.Results)
	}
}

func TestPocketDeterministicValidationCannotBeOverriddenBySemanticEvidence(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	fixture := seedPocketFixture(t, s)
	base := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)

	task, err := s.CreatePocketTask(ctx, fixture.session.ProjectID, "validate deterministically", base)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := s.CreatePocketWorker(ctx, fixture.session.ID, base)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := s.CreatePocketExecution(
		ctx, task.ID, worker.ID, fixture.convID, fixture.turn1, "",
		domain.PocketExecutionUnknown, base,
	)
	if err != nil {
		t.Fatal(err)
	}
	req, err := s.CreatePocketValidationRequirement(
		ctx, task.ID, execution.ID, "focused-test", "go test ./focused", domain.PocketValidationExecutionScope,
		true, true, base,
	)
	if err != nil {
		t.Fatal(err)
	}

	assertState := func(want domain.PocketValidationState) {
		t.Helper()
		got, ok, err := s.PocketExecutionForSession(ctx, fixture.session.ID, fixture.turn1)
		if err != nil || !ok {
			t.Fatalf("read validation: ok=%v err=%v", ok, err)
		}
		if got.ValidationState != want || len(got.Validation) != 1 || got.Validation[0].EffectiveState != want {
			t.Fatalf("validation state=%q evidence=%#v, want %q", got.ValidationState, got.Validation, want)
		}
	}

	// A declared requirement with no result is unknown, not a pass.
	assertState(domain.PocketValidationUnknown)

	if _, err := s.CreatePocketValidationResult(
		ctx, execution.ID, req.ID, domain.PocketValidationPass, domain.PocketValidationSemantic,
		"model-output", "claims success", base.Add(time.Minute), base.Add(time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	// Semantic/model output is retained as provenance but cannot satisfy a
	// deterministic requirement.
	assertState(domain.PocketValidationUnknown)

	if _, err := s.CreatePocketValidationResult(
		ctx, execution.ID, req.ID, domain.PocketValidationFail, domain.PocketValidationDeterministic,
		"go test ./focused", "exit 1", base.Add(2*time.Minute), base.Add(2*time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	assertState(domain.PocketValidationFail)

	if _, err := s.CreatePocketValidationResult(
		ctx, execution.ID, req.ID, domain.PocketValidationPass, domain.PocketValidationSemantic,
		"model-output", "claims fixed", base.Add(3*time.Minute), base.Add(3*time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	// A later model claim still cannot override the last deterministic failure.
	assertState(domain.PocketValidationFail)

	if _, err := s.CreatePocketValidationResult(
		ctx, execution.ID, req.ID, domain.PocketValidationPass, domain.PocketValidationDeterministic,
		"go test ./focused", "exit 0", base.Add(4*time.Minute), base.Add(4*time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	// Only newer authoritative deterministic evidence changes the outcome.
	assertState(domain.PocketValidationPass)
}

func TestPocketRetryMustMatchDurableAOTurnLineage(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	fixture := seedPocketFixture(t, s)
	now := time.Now().UTC()
	task, err := s.CreatePocketTask(ctx, fixture.session.ProjectID, "retry lineage", now)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := s.CreatePocketWorker(ctx, fixture.session.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.CreatePocketExecution(ctx, task.ID, worker.ID, fixture.convID, fixture.turn1, "", domain.PocketExecutionUnknown, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePocketExecution(ctx, task.ID, worker.ID, fixture.convID, fixture.turn2, "", domain.PocketExecutionUnknown, now); err == nil {
		t.Fatal("retry turn accepted without explicit prior execution")
	}
	second, err := s.CreatePocketExecution(ctx, task.ID, worker.ID, fixture.convID, fixture.turn2, first.ID, domain.PocketExecutionUnknown, now)
	if err != nil {
		t.Fatalf("valid retry lineage rejected: %v", err)
	}
	if second.PriorExecutionID != first.ID || second.AttemptNumber != 2 {
		t.Fatalf("retry=%#v", second)
	}
}

func TestPocketSessionWithoutExecutionRemainsLegacyCompatible(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	fixture := seedPocketFixture(t, s)
	if got, ok, err := s.PocketExecutionForSession(ctx, fixture.session.ID, fixture.turn1); err != nil || ok {
		t.Fatalf("legacy lookup = %#v, ok=%v err=%v; want absent without error", got, ok, err)
	}
}
