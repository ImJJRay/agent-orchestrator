package pocket

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestPocketPolicy(t *testing.T) {
	base := domain.PocketPolicyFacts{Task: domain.PocketTask{State: domain.PocketTaskActive}, Config: domain.PocketPolicyConfig{Auto: true, MaxAttempts: 3, MaxRetries: 1, MaxEscalations: 1, EscalationSessions: []domain.SessionID{"escalation"}}, Target: "worker"}
	failed := func(f *domain.PocketPolicyFacts) {
		f.Latest = &domain.PocketExecution{State: domain.PocketExecutionFailed}
		f.Attempts = 1
	}
	tests := []struct {
		name          string
		change        func(*domain.PocketPolicyFacts)
		state, reason string
		allowed       bool
	}{
		{"ready", func(*domain.PocketPolicyFacts) {}, "READY", "bounded_action_permitted", true},
		{"dependencies", func(f *domain.PocketPolicyFacts) { f.BlockedBy = []string{"prerequisite"} }, "BLOCKED", "dependencies_not_satisfied", false},
		{"auto disabled", func(f *domain.PocketPolicyFacts) { f.Config.Auto = false }, "READY", "automation_disabled", false},
		{"missing target", func(f *domain.PocketPolicyFacts) { f.Target = "" }, "NEEDS_USER", "explicit_target_required", false},
		{"busy", func(f *domain.PocketPolicyFacts) { f.TargetProblem = "target_busy" }, "BLOCKED", "target_busy", false},
		{"approval", func(f *domain.PocketPolicyFacts) { f.TargetProblem = "pending_user_input" }, "NEEDS_USER", "pending_user_input", false},
		{"retry", failed, "RETRY", "bounded_action_permitted", true},
		{"failed execution validation not run", func(f *domain.PocketPolicyFacts) { failed(f); f.ValidationPending = true }, "RETRY", "bounded_action_permitted", true},
		{"escalate", func(f *domain.PocketPolicyFacts) { failed(f); f.Retries = 1; f.Target = "escalation" }, "ESCALATE", "bounded_action_permitted", true},
		{"attempt budget", func(f *domain.PocketPolicyFacts) { failed(f); f.Attempts = 3 }, "NEEDS_USER", "attempt_budget_exhausted", false},
		{"all budgets", func(f *domain.PocketPolicyFacts) { failed(f); f.Retries = 1; f.Escalations = 1 }, "NEEDS_USER", "retry_and_escalation_budgets_exhausted", false},
		{"unconfigured escalation", func(f *domain.PocketPolicyFacts) { failed(f); f.Retries = 1; f.Config.EscalationSessions = nil }, "NEEDS_USER", "escalation_target_required", false},
		{"in progress", func(f *domain.PocketPolicyFacts) {
			f.Latest = &domain.PocketExecution{State: domain.PocketExecutionRunning}
		}, "BLOCKED", "execution_in_progress", false},
		{"validation pending", func(f *domain.PocketPolicyFacts) {
			f.Latest = &domain.PocketExecution{State: domain.PocketExecutionCompleted}
			f.ValidationPending = true
		}, "BLOCKED", "validation_pending", false},
		{"validation unknown", func(f *domain.PocketPolicyFacts) {
			f.Latest = &domain.PocketExecution{State: domain.PocketExecutionCompleted}
			f.ValidationUnknown = true
		}, "NEEDS_USER", "validation_unknown", false},
		{"deterministic fail", func(f *domain.PocketPolicyFacts) {
			f.Latest = &domain.PocketExecution{State: domain.PocketExecutionCompleted}
			f.ValidationFailed = true
			f.Attempts = 1
		}, "RETRY", "bounded_action_permitted", true},
		{"human acceptance", func(f *domain.PocketPolicyFacts) {
			f.Latest = &domain.PocketExecution{State: domain.PocketExecutionCompleted}
		}, "NEEDS_USER", "task_acceptance_required", false},
		{"explicit complete with fail", func(f *domain.PocketPolicyFacts) {
			f.Task.State = domain.PocketTaskCompleted
			f.ValidationFailed = true
		}, "BLOCKED", "deterministic_validation_failed", false},
		{"explicit completion", func(f *domain.PocketPolicyFacts) { f.Task.State = domain.PocketTaskCompleted }, "COMPLETED", "explicit_task_completion", false},
		{"cancel", func(f *domain.PocketPolicyFacts) { f.Task.State = domain.PocketTaskCancelled }, "NEEDS_USER", "task_terminal", false},
		{"uncertain AO", func(f *domain.PocketPolicyFacts) { failed(f); f.SourceUnsafe = true }, "NEEDS_USER", "ao_delivery_or_lineage_uncertain", false},
		{"uncertain action", func(f *domain.PocketPolicyFacts) { f.DeliveryUncertain = true }, "NEEDS_USER", "delivery_uncertain", false},
		{"pending action", func(f *domain.PocketPolicyFacts) { f.Outstanding = true }, "BLOCKED", "action_pending", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := base
			tt.change(&f)
			got := Evaluate(f)
			if got.State != tt.state || got.Reason != tt.reason || got.Allowed != tt.allowed {
				t.Fatalf("got %#v", got)
			}
			if Evaluate(f) != got {
				t.Fatal("policy is not deterministic")
			}
		})
	}
}
