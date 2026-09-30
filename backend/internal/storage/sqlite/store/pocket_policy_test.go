package store_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/pocket"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func policyTask(t *testing.T, s *sqlite.Store) domain.PocketTask {
	t.Helper()
	task, err := s.CreatePocketTask(context.Background(), "mer", "implement bounded work", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return task
}
func policyConfig(session domain.SessionID) domain.PocketPolicyConfig {
	return domain.PocketPolicyConfig{Auto: true, SessionID: session, MaxAttempts: 3, MaxRetries: 1, MaxEscalations: 1}
}
func policyView(t *testing.T, s *sqlite.Store, task string) domain.PocketPolicyView {
	t.Helper()
	v, err := s.PocketPolicy(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func policyReconcile(t *testing.T, s *sqlite.Store) {
	t.Helper()
	if err := s.ReconcilePocketActions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcilePocketExecutionLifecycle(context.Background(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcilePocketPolicy(context.Background(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}
func idlePolicyFixture(t *testing.T, s *sqlite.Store) pocketFixture {
	t.Helper()
	f := seedPocketFixture(t, s)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, turn := range []string{f.turn1, f.turn2} {
		if err := s.SettleTurnByID(ctx, turn, domain.TurnStateCancelled, "fixture settled", now); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func TestPocketPolicyDAG(t *testing.T) {
	s := newTestStore(t)
	idlePolicyFixture(t, s)
	ctx := context.Background()
	a, b, c := policyTask(t, s), policyTask(t, s), policyTask(t, s)
	if err := s.SetPocketDependencies(ctx, a.ID, []string{b.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPocketDependencies(ctx, b.ID, []string{c.ID}); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		id   string
		deps []string
	}{{c.ID, []string{a.ID}}, {a.ID, []string{a.ID}}, {a.ID, []string{b.ID, b.ID}}, {a.ID, []string{"missing"}}} {
		if err := s.SetPocketDependencies(ctx, tt.id, tt.deps); err == nil {
			t.Fatal("invalid DAG accepted")
		}
	}
	if got := policyView(t, s, a.ID); len(got.Facts.Dependencies) != 1 || got.Facts.Dependencies[0] != b.ID {
		t.Fatal("rejected update did not roll back")
	}
	if v := policyView(t, s, b.ID); v.Policy.State != "BLOCKED" {
		t.Fatalf("unmet dependency: %#v", v.Policy)
	}
	if _, err := s.UpdatePocketTaskState(ctx, c.ID, domain.PocketTaskCompleted, time.Now()); err != nil {
		t.Fatal(err)
	}
	if v := policyView(t, s, b.ID); len(v.Facts.BlockedBy) != 0 {
		t.Fatal("completed prerequisite did not unblock")
	}
	if err := s.ReconcilePocketPolicy(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	n := len(policyView(t, s, b.ID).Decisions)
	if err := s.ReconcilePocketPolicy(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(policyView(t, s, b.ID).Decisions) != n {
		t.Fatal("duplicate audit snapshot")
	}
}

type policyExecutor struct {
	t                *testing.T
	s                *sqlite.Store
	conv             map[domain.SessionID]string
	calls            int
	failAfterPersist bool
}

func (e *policyExecutor) SendPolicy(ctx context.Context, id domain.SessionID, msg ports.ChatUserMessage) (domain.ConversationTurn, error) {
	e.calls++
	turnID := fmt.Sprintf("policy-turn-%d", e.calls)
	created, err := e.s.AppendUserMessage(ctx, e.conv[id], id, "gen-1", domain.ConversationMessage{ID: "message-" + turnID, Text: msg.Text, Origin: msg.Origin, ClientMessageID: msg.ClientMessageID}, turnID, time.Now().UTC())
	if err != nil {
		return domain.ConversationTurn{}, err
	}
	if !created {
		return domain.ConversationTurn{}, nil
	}
	if e.failAfterPersist {
		return domain.ConversationTurn{}, errors.New("lost dispatch response")
	}
	return domain.ConversationTurn{ID: turnID, ConversationID: e.conv[id], HandledBySessionID: id}, nil
}
func (e *policyExecutor) RetryPolicy(ctx context.Context, id domain.SessionID, source string) (domain.ConversationTurn, error) {
	e.calls++
	turnID := fmt.Sprintf("policy-retry-%d", e.calls)
	created, err := e.s.AppendRetryUserMessage(ctx, e.conv[id], id, "gen-1", domain.ConversationMessage{ID: "message-" + turnID, Text: "native retry", Origin: domain.MessageOriginHuman}, turnID, source, time.Now().UTC())
	if err != nil {
		return domain.ConversationTurn{}, err
	}
	if !created {
		return domain.ConversationTurn{}, nil
	}
	return domain.ConversationTurn{ID: turnID}, nil
}
func settlePolicy(t *testing.T, s *sqlite.Store, task string, state domain.TurnState) {
	t.Helper()
	v := policyView(t, s, task)
	if v.Facts.Latest == nil {
		t.Fatal("no latest attempt")
	}
	turn := v.Facts.Latest.TurnID
	ctx := context.Background()
	if err := s.BindTurnToProvider(ctx, turn, "provider-"+turn, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleTurnByID(ctx, turn, state, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	policyReconcile(t, s)
}

func TestPocketPolicyAutomaticRetryEscalationAndBudgets(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	f := idlePolicyFixture(t, s)
	rec := sampleRecord("mer")
	rec.Mode = domain.SessionModeChat
	target, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := s.CreateConversation(ctx, "escalation-conv", domain.ConversationScopeSession, "mer", target.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	task := policyTask(t, s)
	config := policyConfig(f.session.ID)
	config.EscalationSessions = []domain.SessionID{target.ID}
	if err := s.ConfigurePocketPolicy(ctx, task.ID, config); err != nil {
		t.Fatal(err)
	}
	e := &policyExecutor{t: t, s: s, conv: map[domain.SessionID]string{f.session.ID: f.convID, target.ID: conv.ID}}
	c := pocket.New(pocket.Options{Store: s, Executor: e})
	if err := c.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.RunPendingAction(ctx); err != nil {
		t.Fatal(err)
	}
	policyReconcile(t, s)
	root := policyView(t, s, task.ID).Facts.Latest.ID
	settlePolicy(t, s, task.ID, domain.TurnStateFailed)
	if err := c.RunPendingAction(ctx); err != nil {
		t.Fatal(err)
	}
	policyReconcile(t, s)
	retry := policyView(t, s, task.ID).Facts.Latest
	if retry.AttemptNumber != 2 || retry.PriorExecutionID != root {
		t.Fatalf("retry lineage: %#v", retry)
	}
	settlePolicy(t, s, task.ID, domain.TurnStateFailed)
	if err := c.RunPendingAction(ctx); err != nil {
		t.Fatal(err)
	}
	policyReconcile(t, s)
	escalation := policyView(t, s, task.ID).Facts.Latest
	if escalation.SessionID != target.ID || escalation.PriorExecutionID != retry.ID || escalation.AttemptNumber != 3 {
		t.Fatalf("escalation: %#v", escalation)
	}
	settlePolicy(t, s, task.ID, domain.TurnStateFailed)
	v := policyView(t, s, task.ID)
	if v.Policy.State != "NEEDS_USER" || v.Policy.Reason != "attempt_budget_exhausted" || v.Facts.Retries != 1 || v.Facts.Escalations != 1 {
		t.Fatalf("budgets: %#v", v)
	}
	for range 3 {
		if err := c.RunPendingAction(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if e.calls != 3 {
		t.Fatalf("dispatches=%d", e.calls)
	}
}

func TestPocketPolicyNativeRetryAndValidationFailure(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	f := seedPocketFixture(t, s)
	now := time.Now()
	if err := s.SettleTurnByID(ctx, f.turn2, domain.TurnStateCancelled, "", now); err != nil {
		t.Fatal(err)
	}
	if err := s.BindTurnToProvider(ctx, f.turn1, "acknowledged", now); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleTurnByID(ctx, f.turn1, domain.TurnStateFailed, "", now); err != nil {
		t.Fatal(err)
	}
	// Explicit root association excludes the fixture's pre-existing retry child.
	task := policyTask(t, s)
	w, err := s.CreatePocketWorker(ctx, f.session.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := s.CreatePocketExecution(ctx, task.ID, w.ID, f.convID, f.turn1, "", domain.PocketExecutionFailed, now)
	if err != nil {
		t.Fatal(err)
	}
	// AO has an existing retry already: native recovery must attach it, not create one.
	config := policyConfig(f.session.ID)
	if err := s.ConfigurePocketPolicy(ctx, task.ID, config); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcilePocketPolicy(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcilePocketActions(ctx); err != nil {
		t.Fatal(err)
	}
	policyReconcile(t, s)
	v := policyView(t, s, task.ID)
	if v.Facts.Attempts != 2 || v.Facts.Latest.PriorExecutionID != execution.ID {
		t.Fatalf("native AO retry lineage: %#v", v)
	}
	// Separate completed root with a deterministic fail must remediate despite semantic pass.
	task2 := policyTask(t, s)
	root, err := s.CreatePocketExecution(ctx, task2.ID, w.ID, "", "", "", domain.PocketExecutionCompleted, now)
	if err != nil {
		t.Fatal(err)
	}
	req, err := s.CreatePocketValidationCommandRequirement(ctx, task2.ID, root.ID, "check", "check", "false", domain.PocketValidationExecutionScope, true, true, now)
	if err != nil {
		t.Fatal(err)
	}
	for i, kind := range []domain.PocketValidationSourceKind{domain.PocketValidationDeterministic, domain.PocketValidationSemantic} {
		state := domain.PocketValidationFail
		if i == 1 {
			state = domain.PocketValidationPass
		}
		if _, err := s.CreatePocketValidationResult(ctx, root.ID, req.ID, state, kind, "test", "", now.Add(time.Duration(i)*time.Second), now); err != nil {
			t.Fatal(err)
		}
	}
	v = policyView(t, s, task2.ID)
	if !v.Facts.ValidationFailed {
		t.Fatal("semantic pass overrode deterministic failure")
	}
	if _, err := s.UpdatePocketTaskState(ctx, task2.ID, domain.PocketTaskCompleted, now); err != nil {
		t.Fatal(err)
	}
	if policyView(t, s, task2.ID).Policy.State != "BLOCKED" {
		t.Fatal("explicit completion bypassed validation fail")
	}
	dependent := policyTask(t, s)
	if err := s.SetPocketDependencies(ctx, dependent.ID, []string{task2.ID}); err != nil {
		t.Fatal(err)
	}
	if len(policyView(t, s, dependent.ID).Facts.BlockedBy) != 1 {
		t.Fatal("failed prerequisite satisfied dependency")
	}
}

func TestPocketPolicyRestartAndDuplicatePrevention(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	f := idlePolicyFixture(t, s)
	task := policyTask(t, s)
	if err := s.ConfigurePocketPolicy(ctx, task.ID, policyConfig(f.session.ID)); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcilePocketPolicy(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.ReconcilePocketPolicy(ctx, time.Now()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	v := policyView(t, s, task.ID)
	if v.Facts.Attempts != 1 || len(v.Actions) != 1 {
		t.Fatalf("duplicate reservation: %#v", v)
	}
	a, ok, err := s.ClaimPocketAction(ctx, time.Now())
	if err != nil || !ok {
		t.Fatalf("claim: %t %v", ok, err)
	}
	if _, ok, err := s.ClaimPocketAction(ctx, time.Now()); err != nil || ok {
		t.Fatalf("double claim: %t %v", ok, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.RecoverPocketActions(ctx); err != nil {
		t.Fatal(err)
	}
	b, ok, err := s.ClaimPocketAction(ctx, time.Now())
	if err != nil || !ok || b.DecisionID != a.DecisionID || b.ExecutionID != a.ExecutionID {
		t.Fatalf("restart changed reservation: %#v %t %v", b, ok, err)
	}
	// Simulate process death after AO commits but before Pocket receives a response.
	e := &policyExecutor{t: t, s: s, conv: map[domain.SessionID]string{f.session.ID: f.convID}, failAfterPersist: true}
	_, _ = e.SendPolicy(ctx, b.SessionID, ports.ChatUserMessage{Text: b.Prompt, Origin: domain.MessageOriginAutomation, ClientMessageID: "pocket:" + b.DecisionID})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.RecoverPocketActions(ctx); err != nil {
		t.Fatal(err)
	}
	policyReconcile(t, s)
	v = policyView(t, s, task.ID)
	if v.Facts.Attempts != 1 || v.Facts.Latest.TurnID == "" || v.Actions[0].Status != "dispatched" {
		t.Fatalf("recovery duplicate: %#v", v)
	}
	if _, ok, err := s.ClaimPocketAction(ctx, time.Now()); err != nil || ok {
		t.Fatalf("recovery redispatched: %t %v", ok, err)
	}
	if err := s.SetPocketDependencies(ctx, task.ID, nil); !errors.Is(err, domain.ErrPocketConflict) {
		t.Fatalf("retroactive dependencies: %v", err)
	}
}

func TestPocketPolicyDispatchRevalidationAndUncertainty(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	f := idlePolicyFixture(t, s)
	task := policyTask(t, s)
	config := policyConfig(f.session.ID)
	for _, bad := range []domain.PocketPolicyConfig{{MaxAttempts: 0}, {MaxAttempts: 65}, {MaxAttempts: 1, MaxRetries: -1}, {MaxAttempts: 1, MaxEscalations: 64}, {Auto: true, MaxAttempts: 1}} {
		if err := s.ConfigurePocketPolicy(ctx, task.ID, bad); !errors.Is(err, domain.ErrPocketInvalid) {
			t.Fatalf("invalid budget accepted: %v", err)
		}
	}
	if err := s.ConfigurePocketPolicy(ctx, task.ID, config); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcilePocketPolicy(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	config.Auto = false
	if err := s.ConfigurePocketPolicy(ctx, task.ID, config); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ClaimPocketAction(ctx, time.Now()); err != nil || ok {
		t.Fatalf("disabled action executed: %t %v", ok, err)
	}
	config.Auto = true
	if err := s.ConfigurePocketPolicy(ctx, task.ID, config); err != nil {
		t.Fatal(err)
	}
	a, ok, err := s.ClaimPocketAction(ctx, time.Now())
	if err != nil || !ok {
		t.Fatalf("reenabled claim: %t %v", ok, err)
	}
	if err := s.SettlePocketAction(ctx, a, errors.New("uncertain delivery")); err != nil {
		t.Fatal(err)
	}
	policyReconcile(t, s)
	v := policyView(t, s, task.ID)
	if len(v.Actions[0].Events) < 3 || v.Actions[0].Events[0].Status != "uncertain" {
		t.Fatal("dispatch failure was not durably audited")
	}
	if v.Policy.State != "NEEDS_USER" || v.Policy.Reason != "delivery_uncertain" || v.Actions[0].Error != "uncertain delivery" {
		t.Fatalf("uncertainty: %#v", v)
	}
	if _, ok, err := s.ClaimPocketAction(ctx, time.Now()); err != nil || ok {
		t.Fatalf("uncertain delivery retried: %t %v", ok, err)
	}
}

func TestPocketPolicyValidationRemediationAndInheritedChecks(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	f := idlePolicyFixture(t, s)
	task := policyTask(t, s)
	if err := s.ConfigurePocketPolicy(ctx, task.ID, policyConfig(f.session.ID)); err != nil {
		t.Fatal(err)
	}
	e := &policyExecutor{t: t, s: s, conv: map[domain.SessionID]string{f.session.ID: f.convID}}
	c := pocket.New(pocket.Options{Store: s, Executor: e})
	if err := c.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.RunPendingAction(ctx); err != nil {
		t.Fatal(err)
	}
	policyReconcile(t, s)
	root := policyView(t, s, task.ID).Facts.Latest
	req, err := s.CreatePocketValidationCommandRequirement(ctx, task.ID, root.ID, "unit-test", "unit test", "false", domain.PocketValidationExecutionScope, true, true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	settlePolicy(t, s, task.ID, domain.TurnStateCompleted)
	if v := policyView(t, s, task.ID); v.Policy.Reason != "validation_pending" {
		t.Fatalf("validation did not gate dispatch: %#v", v.Policy)
	}
	if _, err := s.CreatePocketValidationResult(ctx, root.ID, req.ID, domain.PocketValidationFail, domain.PocketValidationDeterministic, "test", "exit 1", time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePocketValidationResult(ctx, root.ID, req.ID, domain.PocketValidationPass, domain.PocketValidationSemantic, "model", "claim pass", time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	policyReconcile(t, s)
	if err := c.RunPendingAction(ctx); err != nil {
		t.Fatal(err)
	}
	policyReconcile(t, s)
	child := policyView(t, s, task.ID).Facts.Latest
	if child.PriorExecutionID != root.ID || child.AttemptNumber != 2 {
		t.Fatalf("remediation lineage %#v", child)
	}
	snap, ok, err := s.PocketExecutionForSession(ctx, f.session.ID, child.TurnID)
	if err != nil || !ok {
		t.Fatalf("snapshot: %t %v", ok, err)
	}
	if len(snap.Validation) != 1 || snap.Validation[0].Requirement.Command != "false" || len(snap.Validation[0].Results) != 0 || snap.ValidationState != domain.PocketValidationUnknown {
		t.Fatalf("checks/evidence leaked: %#v", snap.Validation)
	}
	settlePolicy(t, s, task.ID, domain.TurnStateCompleted)
	if _, err := s.CreatePocketValidationResult(ctx, child.ID, snap.Validation[0].Requirement.ID, domain.PocketValidationUnknown, domain.PocketValidationDeterministic, "test", "unavailable", time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	policyReconcile(t, s)
	if policyView(t, s, task.ID).Policy.Reason != "validation_unknown" {
		t.Fatal("unknown validation allowed progress")
	}
	if _, err := s.CreatePocketValidationResult(ctx, child.ID, snap.Validation[0].Requirement.ID, domain.PocketValidationPass, domain.PocketValidationDeterministic, "test", "verified", time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	policyReconcile(t, s)
	if policyView(t, s, task.ID).Policy.Reason != "task_acceptance_required" {
		t.Fatal("deterministic pass automatically accepted task")
	}
}

func TestPocketPolicyAdmissionAndTargetValidation(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	f := idlePolicyFixture(t, s)
	task := policyTask(t, s)
	config := policyConfig(f.session.ID)
	config.EscalationSessions = []domain.SessionID{f.session.ID}
	if err := s.ConfigurePocketPolicy(ctx, task.ID, config); !errors.Is(err, domain.ErrPocketInvalid) {
		t.Fatalf("duplicate target accepted: %v", err)
	}
	config.EscalationSessions = nil
	if err := s.ConfigurePocketPolicy(ctx, task.ID, config); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := s.UpsertActivity(ctx, f.convID, "", domain.ConversationActivity{ID: "pending", Kind: domain.ActivityKindUserInput, Status: domain.ActivityStatusPending, RequestID: "input"}, now); err != nil {
		t.Fatal(err)
	}
	if v := policyView(t, s, task.ID); v.Policy.Reason != "pending_user_input" || v.Policy.Allowed {
		t.Fatalf("pending input bypassed %#v", v.Policy)
	}
	if err := s.FailPendingInputs(ctx, f.convID, now); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcilePocketPolicy(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertActivity(ctx, f.convID, "", domain.ConversationActivity{ID: "approval", Kind: domain.ActivityKindApproval, Status: domain.ActivityStatusPending, RequestID: "approval"}, now); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ClaimPocketAction(ctx, now); err != nil || ok {
		t.Fatalf("late approval bypassed: %t %v", ok, err)
	}
	if err := s.FailPendingApprovals(ctx, f.convID, now); err != nil {
		t.Fatal(err)
	}
	created, err := s.AppendUserMessage(ctx, f.convID, f.session.ID, "gen-1", domain.ConversationMessage{ID: "human-busy", Text: "unrelated", Origin: domain.MessageOriginHuman}, "human-busy-turn", now)
	if err != nil || !created {
		t.Fatalf("busy fixture %t %v", created, err)
	}
	if _, ok, err := s.ClaimPocketAction(ctx, now); err != nil || ok {
		t.Fatalf("busy target bypassed: %t %v", ok, err)
	}
	rec, ok, err := s.GetSession(ctx, f.session.ID)
	if err != nil || !ok {
		t.Fatal(err)
	}
	rec.IsTerminated = true
	if err := s.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if v := policyView(t, s, task.ID); v.Facts.TargetProblem != "target_unavailable" {
		t.Fatalf("terminated target %#v", v.Facts)
	}
}

func TestPocketPolicyNativeRetryActuation(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	f := idlePolicyFixture(t, s)
	now := time.Now()
	created, err := s.AppendUserMessage(ctx, f.convID, f.session.ID, "gen-1", domain.ConversationMessage{ID: "native-message", Text: "native retry work", Origin: domain.MessageOriginHuman}, "native-source", now)
	if err != nil || !created {
		t.Fatalf("source %t %v", created, err)
	}
	if err := s.BindTurnToProvider(ctx, "native-source", "native-provider", now); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleTurnByID(ctx, "native-source", domain.TurnStateFailed, "failure", now); err != nil {
		t.Fatal(err)
	}
	policyReconcile(t, s)
	snapshot, ok, err := s.PocketExecutionForSession(ctx, f.session.ID, "native-source")
	if err != nil || !ok {
		t.Fatal(err)
	}
	if err := s.ConfigurePocketPolicy(ctx, snapshot.Task.ID, policyConfig(f.session.ID)); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcilePocketPolicy(ctx, now); err != nil {
		t.Fatal(err)
	}
	e := &policyExecutor{t: t, s: s, conv: map[domain.SessionID]string{f.session.ID: f.convID}}
	c := pocket.New(pocket.Options{Store: s, Executor: e})
	if err := c.RunPendingAction(ctx); err != nil {
		t.Fatal(err)
	}
	policyReconcile(t, s)
	v := policyView(t, s, snapshot.Task.ID)
	if v.Facts.Attempts != 2 || !v.Actions[0].NativeRetry || v.Facts.Latest.PriorExecutionID != snapshot.Execution.ID {
		t.Fatalf("native actuation %#v", v)
	}
	turn, err := s.TurnByID(ctx, v.Facts.Latest.TurnID)
	if err != nil {
		t.Fatal(err)
	}
	if turn.RetryOfTurnID != "native-source" {
		t.Fatal("native AO lineage lost")
	}
}

func TestPocketPolicyHistoryPagingAndDAGPersistence(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	idlePolicyFixture(t, s)
	a, b := policyTask(t, s), policyTask(t, s)
	if err := s.SetPocketDependencies(ctx, a.ID, []string{b.ID}); err != nil {
		t.Fatal(err)
	}
	for i := range 105 {
		state := domain.PocketTaskActive
		if i%2 == 0 {
			state = domain.PocketTaskBlocked
		}
		if _, err := s.UpdatePocketTaskState(ctx, a.ID, state, time.Now().Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
		if err := s.ReconcilePocketPolicy(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.PocketDecisionHistory(ctx, a.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Decisions) != 100 || first.NextBefore == 0 {
		t.Fatalf("first history page %#v", first)
	}
	second, err := s.PocketDecisionHistory(ctx, a.ID, first.NextBefore)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Decisions) != 5 || second.NextBefore != 0 || second.Decisions[0].Sequence >= first.NextBefore {
		t.Fatalf("second page %#v", second)
	}
	firstID := first.Decisions[0].ID
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if v := policyView(t, s, a.ID); len(v.Facts.Dependencies) != 1 || v.Facts.Dependencies[0] != b.ID || v.Decisions[0].ID != firstID {
		t.Fatal("DAG/audit changed after reopen")
	}
	if err := s.SetPocketDependencies(ctx, b.ID, []string{a.ID}); !errors.Is(err, domain.ErrPocketInvalid) {
		t.Fatalf("reopened graph accepted cycle: %v", err)
	}
}

func TestPocketPolicyAdmissionRefusalRetainsOneReservation(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	f := idlePolicyFixture(t, s)
	task := policyTask(t, s)
	if err := s.ConfigurePocketPolicy(ctx, task.ID, policyConfig(f.session.ID)); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcilePocketPolicy(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	a, ok, err := s.ClaimPocketAction(ctx, time.Now())
	if err != nil || !ok {
		t.Fatal(err)
	}
	if err := s.SettlePocketAction(ctx, a, domain.ErrPocketAdmissionBlocked); err != nil {
		t.Fatal(err)
	}
	b, ok, err := s.ClaimPocketAction(ctx, time.Now())
	v := policyView(t, s, task.ID)
	found := false
	for _, event := range v.Actions[0].Events {
		if event.Detail == domain.ErrPocketAdmissionBlocked.Error() {
			found = true
		}
	}
	if !found {
		t.Fatal("admission refusal was lost from audit")
	}
	if err != nil || !ok || b.ExecutionID != a.ExecutionID || b.DecisionID != a.DecisionID {
		t.Fatalf("admission refusal consumed a new attempt: %#v %t %v", b, ok, err)
	}
}

func TestPocketPolicyHumanRetrySupersedesPreparedEscalation(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	f := idlePolicyFixture(t, s)
	now := time.Now()
	created, err := s.AppendUserMessage(ctx, f.convID, f.session.ID, "gen-1", domain.ConversationMessage{ID: "manual-source-message", Text: "work", Origin: domain.MessageOriginHuman}, "manual-source", now)
	if err != nil || !created {
		t.Fatal(err)
	}
	if err := s.BindTurnToProvider(ctx, "manual-source", "manual-provider", now); err != nil {
		t.Fatal(err)
	}
	if err := s.SettleTurnByID(ctx, "manual-source", domain.TurnStateFailed, "", now); err != nil {
		t.Fatal(err)
	}
	policyReconcile(t, s)
	snapshot, ok, err := s.PocketExecutionForSession(ctx, f.session.ID, "manual-source")
	if err != nil || !ok {
		t.Fatal(err)
	}
	rec := sampleRecord("mer")
	rec.Mode = domain.SessionModeChat
	other, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	config := policyConfig(f.session.ID)
	config.MaxRetries = 0
	config.EscalationSessions = []domain.SessionID{other.ID}
	if err := s.ConfigurePocketPolicy(ctx, snapshot.Task.ID, config); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcilePocketPolicy(ctx, now); err != nil {
		t.Fatal(err)
	}
	reserved := policyView(t, s, snapshot.Task.ID).Actions[0]
	created, err = s.AppendRetryUserMessage(ctx, f.convID, f.session.ID, "gen-1", domain.ConversationMessage{ID: "manual-retry-message", Text: "work", Origin: domain.MessageOriginHuman}, "manual-retry", "manual-source", now)
	if err != nil || !created {
		t.Fatal(err)
	}
	// Exercise the projector directly, without a prior action-recovery pass.
	if err := s.ReconcilePocketExecutionLifecycle(ctx, now); err != nil {
		t.Fatal(err)
	}
	v := policyView(t, s, snapshot.Task.ID)
	if v.Actions[0].Status != "superseded" || v.Facts.Latest.ID != reserved.ExecutionID || v.Facts.Latest.SessionID != f.session.ID || v.Facts.Latest.TurnID != "manual-retry" || v.Facts.Attempts != 2 || v.Facts.Escalations != 0 || v.Facts.Retries != 1 {
		t.Fatalf("human retry not adopted: %#v", v)
	}
	if _, ok, err := s.ClaimPocketAction(ctx, now); err != nil || ok {
		t.Fatalf("superseded escalation dispatched: %t %v", ok, err)
	}
}
