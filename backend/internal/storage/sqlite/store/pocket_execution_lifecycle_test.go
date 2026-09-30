package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func TestPocketLifecycleProjectsAOTurnsAndRetryLineage(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	fixture := seedPocketFixture(t, s)

	if err := s.ReconcilePocketExecutionLifecycle(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	got, ok, err := s.PocketExecutionForSession(ctx, fixture.session.ID, fixture.turn2)
	if err != nil || !ok {
		t.Fatalf("automatic Pocket snapshot: ok=%v err=%v", ok, err)
	}
	if got.Task.Objective != "first attempt" {
		t.Fatalf("task objective=%q, want first attempt", got.Task.Objective)
	}
	if got.Worker.SessionID != fixture.session.ID {
		t.Fatalf("worker session=%q, want %q", got.Worker.SessionID, fixture.session.ID)
	}
	if len(got.Attempts) != 2 {
		t.Fatalf("attempts=%d, want 2: %#v", len(got.Attempts), got.Attempts)
	}
	first, second := got.Attempts[0], got.Attempts[1]
	if first.AttemptNumber != 1 || first.PriorExecutionID != "" || first.TurnID != fixture.turn1 {
		t.Fatalf("root attempt=%#v", first)
	}
	if second.AttemptNumber != 2 || second.PriorExecutionID != first.ID || second.TurnID != fixture.turn2 {
		t.Fatalf("retry attempt=%#v", second)
	}
	if first.TaskID != second.TaskID || first.WorkerID != second.WorkerID {
		t.Fatalf("retry changed task/worker: first=%#v second=%#v", first, second)
	}
	if first.State != domain.PocketExecutionQueued || second.State != domain.PocketExecutionQueued {
		t.Fatalf("initial states=%q/%q, want queued/queued", first.State, second.State)
	}
	if second.ProjectID != fixture.session.ProjectID ||
		second.WorkspacePath != "/worktrees/mer-1" ||
		second.WorkspaceRepoPath != "/repos/mer" {
		t.Fatalf("AO trace binding=%#v", second)
	}
}

func TestPocketLifecycleFollowsAOTurnStateAndLateWorkspaceBinding(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	fixture := seedPocketFixture(t, s)

	rec, ok, err := s.GetSession(ctx, fixture.session.ID)
	if err != nil || !ok {
		t.Fatalf("get session: ok=%v err=%v", ok, err)
	}
	rec.Metadata.WorkspacePath = ""
	rec.Metadata.WorkspaceRepoPath = ""
	if err := s.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	if err := s.ReconcilePocketExecutionLifecycle(ctx, now); err != nil {
		t.Fatal(err)
	}
	initial, ok, err := s.PocketExecutionForSession(ctx, fixture.session.ID, fixture.turn1)
	if err != nil || !ok {
		t.Fatalf("initial snapshot: ok=%v err=%v", ok, err)
	}
	if initial.Execution.State != domain.PocketExecutionQueued {
		t.Fatalf("state=%q, want queued", initial.Execution.State)
	}
	if initial.Execution.WorkspacePath != "" || initial.Execution.WorkspaceRepoPath != "" {
		t.Fatalf("workspace should not be invented before AO persists it: %#v", initial.Execution)
	}

	rec, ok, err = s.GetSession(ctx, fixture.session.ID)
	if err != nil || !ok {
		t.Fatalf("get session for workspace update: ok=%v err=%v", ok, err)
	}
	rec.Metadata.WorkspacePath = "/late/worktree"
	rec.Metadata.WorkspaceRepoPath = "/late/repo"
	if err := s.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if err := s.BindTurnToProvider(ctx, fixture.turn1, "provider-turn-1", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcilePocketExecutionLifecycle(ctx, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	running, ok, err := s.PocketExecutionForSession(ctx, fixture.session.ID, fixture.turn1)
	if err != nil || !ok {
		t.Fatalf("running snapshot: ok=%v err=%v", ok, err)
	}
	if running.Execution.State != domain.PocketExecutionRunning || running.Execution.StartedAt == nil {
		t.Fatalf("running execution=%#v", running.Execution)
	}
	if running.Execution.WorkspacePath != "/late/worktree" || running.Execution.WorkspaceRepoPath != "/late/repo" {
		t.Fatalf("late workspace binding=%#v", running.Execution)
	}

	if err := s.SettleTurn(
		ctx,
		fixture.convID,
		"provider-turn-1",
		domain.TurnStateCompleted,
		"",
		now.Add(3*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcilePocketExecutionLifecycle(ctx, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	completed, ok, err := s.PocketExecutionForSession(ctx, fixture.session.ID, fixture.turn1)
	if err != nil || !ok {
		t.Fatalf("completed snapshot: ok=%v err=%v", ok, err)
	}
	if completed.Execution.State != domain.PocketExecutionCompleted || completed.Execution.CompletedAt == nil {
		t.Fatalf("completed execution=%#v", completed.Execution)
	}
}

func TestPocketLifecycleRecoveryReconcilesUnfinishedAttemptAfterReopen(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	s, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	fixture := seedPocketFixture(t, s)
	now := time.Now().UTC()
	if err := s.BindTurnToProvider(ctx, fixture.turn1, "provider-turn-restart", now); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcilePocketExecutionLifecycle(ctx, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	before, ok, err := s.PocketExecutionForSession(ctx, fixture.session.ID, fixture.turn1)
	if err != nil || !ok {
		t.Fatalf("before restart: ok=%v err=%v", ok, err)
	}
	if before.Execution.State != domain.PocketExecutionRunning {
		t.Fatalf("before restart state=%q, want running", before.Execution.State)
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

	// This is the durable Chat repair AO already performs when a dead controller
	// cannot be adopted. Pocket reconciliation must consume that fact rather than
	// preserving its stale pre-restart running state.
	if err := s.SettleOrphanedTurns(ctx, fixture.session.ID, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcilePocketExecutionLifecycle(ctx, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	after, ok, err := s.PocketExecutionForSession(ctx, fixture.session.ID, fixture.turn1)
	if err != nil || !ok {
		t.Fatalf("after restart: ok=%v err=%v", ok, err)
	}
	if after.Execution.State != domain.PocketExecutionFailed || after.Execution.CompletedAt == nil {
		t.Fatalf("recovered execution=%#v", after.Execution)
	}
}

func TestPocketLifecyclePreservesLegacyFallbackUntilNewRetry(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "legacy-pocket")
	rec := sampleRecord("legacy-pocket")
	rec.Mode = domain.SessionModeChat
	session, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	legacyAt := time.Date(2020, 1, 1, 12, 0, 0, 0, time.UTC)
	conv, err := s.CreateConversation(
		ctx,
		"legacy-pocket-conv",
		domain.ConversationScopeSession,
		session.ProjectID,
		session.ID,
		legacyAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.AppendUserMessage(ctx, conv.ID, session.ID, "legacy-gen", domain.ConversationMessage{
		ID: "legacy-pocket-message", Origin: domain.MessageOriginHuman, Text: "legacy objective",
	}, "legacy-pocket-turn", legacyAt)
	if err != nil || !created {
		t.Fatalf("append legacy turn: created=%v err=%v", created, err)
	}

	if err := s.ReconcilePocketExecutionLifecycle(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.PocketExecutionForSession(ctx, session.ID, "legacy-pocket-turn"); err != nil || ok {
		t.Fatalf("legacy turn should retain fallback semantics before new work: ok=%v err=%v", ok, err)
	}

	retryAt := time.Now().UTC().Add(time.Minute)
	created, err = s.AppendRetryUserMessage(ctx, conv.ID, session.ID, "legacy-gen", domain.ConversationMessage{
		ID: "legacy-pocket-retry-message", Origin: domain.MessageOriginHuman, Text: "legacy objective",
	}, "legacy-pocket-retry", "legacy-pocket-turn", retryAt)
	if err != nil || !created {
		t.Fatalf("append new retry: created=%v err=%v", created, err)
	}
	if err := s.ReconcilePocketExecutionLifecycle(ctx, retryAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.PocketExecutionForSession(ctx, session.ID, "legacy-pocket-retry")
	if err != nil || !ok {
		t.Fatalf("retry should enter Pocket lifecycle: ok=%v err=%v", ok, err)
	}
	if len(got.Attempts) != 2 ||
		got.Attempts[0].TurnID != "legacy-pocket-turn" ||
		got.Attempts[1].TurnID != "legacy-pocket-retry" ||
		got.Attempts[1].PriorExecutionID != got.Attempts[0].ID {
		t.Fatalf("legacy retry lineage=%#v", got.Attempts)
	}
}

func TestPocketAutomaticExecutionFeedsDeterministicValidationQueue(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	fixture := seedPocketFixture(t, s)
	now := time.Now().UTC()

	if err := s.BindTurnToProvider(ctx, fixture.turn1, "provider-validation-turn", now); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleTurn(
		ctx,
		fixture.convID,
		"provider-validation-turn",
		domain.TurnStateCompleted,
		"",
		now.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcilePocketExecutionLifecycle(ctx, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	snapshot, ok, err := s.PocketExecutionForSession(ctx, fixture.session.ID, fixture.turn1)
	if err != nil || !ok {
		t.Fatalf("automatic execution: ok=%v err=%v", ok, err)
	}
	if snapshot.Execution.State != domain.PocketExecutionCompleted {
		t.Fatalf("execution state=%q, want completed", snapshot.Execution.State)
	}

	requirement, err := s.CreatePocketValidationCommandRequirement(
		ctx,
		snapshot.Task.ID,
		"",
		"focused-go-test",
		"focused deterministic check",
		"go test ./internal/pocket",
		domain.PocketValidationTaskScope,
		true,
		true,
		now.Add(3*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if requirement.Command != "go test ./internal/pocket" {
		t.Fatalf("stored command=%q", requirement.Command)
	}

	pending, err := s.PendingPocketDeterministicValidations(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending validation=%#v", pending)
	}
	if pending[0].Execution.ID != snapshot.Execution.ID ||
		pending[0].Execution.WorkspacePath != "/worktrees/mer-1" ||
		pending[0].Requirement.ID != requirement.ID ||
		pending[0].Requirement.Command != "go test ./internal/pocket" {
		t.Fatalf("pending validation work item=%#v", pending[0])
	}

	if _, err := s.CreatePocketValidationResult(
		ctx,
		snapshot.Execution.ID,
		requirement.ID,
		domain.PocketValidationPass,
		domain.PocketValidationSemantic,
		"worker-output",
		"model claims the check passed",
		now.Add(4*time.Second),
		now.Add(4*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	pending, err = s.PendingPocketDeterministicValidations(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("semantic evidence must not suppress deterministic validation: %#v", pending)
	}

	if _, err := s.CreatePocketValidationResult(
		ctx,
		snapshot.Execution.ID,
		requirement.ID,
		domain.PocketValidationFail,
		domain.PocketValidationDeterministic,
		"pocket.validation.command",
		"exit 1",
		now.Add(5*time.Second),
		now.Add(5*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	pending, err = s.PendingPocketDeterministicValidations(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("deterministic observation should settle validation queue: %#v", pending)
	}
	got, ok, err := s.PocketExecutionForSession(ctx, fixture.session.ID, fixture.turn1)
	if err != nil || !ok {
		t.Fatalf("read deterministic evidence: ok=%v err=%v", ok, err)
	}
	if got.ValidationState != domain.PocketValidationFail {
		t.Fatalf("validation state=%q, want fail", got.ValidationState)
	}
}

func TestPocketLifecycleDoesNotProjectOrchestratorConversation(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "orchestrator-pocket")
	rec := sampleRecord("orchestrator-pocket")
	rec.Kind = domain.KindOrchestrator
	rec.Mode = domain.SessionModeChat
	session, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Minute)
	conv, err := s.CreateConversation(
		ctx,
		"orchestrator-pocket-conv",
		domain.ConversationScopeSession,
		session.ProjectID,
		session.ID,
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.AppendUserMessage(ctx, conv.ID, session.ID, "orchestrator-gen", domain.ConversationMessage{
		ID: "orchestrator-pocket-message", Origin: domain.MessageOriginHuman, Text: "plan this project",
	}, "orchestrator-pocket-turn", now)
	if err != nil || !created {
		t.Fatalf("append orchestrator turn: created=%v err=%v", created, err)
	}
	if err := s.ReconcilePocketExecutionLifecycle(ctx, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.PocketExecutionForSession(ctx, session.ID, "orchestrator-pocket-turn"); err != nil || ok {
		t.Fatalf("orchestrator turn must not become worker execution: ok=%v err=%v", ok, err)
	}
}

