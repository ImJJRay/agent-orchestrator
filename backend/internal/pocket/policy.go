package pocket

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// Evaluate is the authoritative deterministic orchestration policy. It is pure:
// readiness is derived, never stored as a mutable task/display state.
func Evaluate(f domain.PocketPolicyFacts) domain.PocketPolicyOutcome {
	out := domain.PocketPolicyOutcome{}
	deny := func(state, reason string) domain.PocketPolicyOutcome {
		out.State, out.Reason = state, reason
		return out
	}
	if len(f.BlockedBy) > 0 {
		return deny("BLOCKED", "dependencies_not_satisfied")
	}
	if f.ValidationFailed && f.Task.State == domain.PocketTaskCompleted {
		return deny("BLOCKED", "deterministic_validation_failed")
	}
	switch f.Task.State {
	case domain.PocketTaskCompleted:
		return deny("COMPLETED", "explicit_task_completion")
	case domain.PocketTaskFailed, domain.PocketTaskCancelled:
		return deny("NEEDS_USER", "task_terminal")
	case domain.PocketTaskBlocked:
		return deny("BLOCKED", "explicit_task_hold")
	}
	if f.DeliveryUncertain {
		return deny("NEEDS_USER", "delivery_uncertain")
	}
	if f.Outstanding {
		return deny("BLOCKED", "action_pending")
	}
	if f.Latest != nil {
		switch f.Latest.State {
		case domain.PocketExecutionUnknown, domain.PocketExecutionQueued, domain.PocketExecutionRunning:
			return deny("BLOCKED", "execution_in_progress")
		case domain.PocketExecutionCancelled:
			return deny("NEEDS_USER", "execution_cancelled")
		}
		if f.SourceUnsafe {
			return deny("NEEDS_USER", "ao_delivery_or_lineage_uncertain")
		}
		if f.ValidationPending && !f.ValidationFailed && (f.Latest.State == domain.PocketExecutionCompleted || f.Latest.State == domain.PocketExecutionRecovered) {
			return deny("BLOCKED", "validation_pending")
		}
		if (f.Latest.State == domain.PocketExecutionCompleted || f.Latest.State == domain.PocketExecutionRecovered) && !f.ValidationFailed {
			if f.ValidationUnknown {
				return deny("NEEDS_USER", "validation_unknown")
			}
			return deny("NEEDS_USER", "task_acceptance_required")
		}
	}
	if f.Attempts >= f.Config.MaxAttempts {
		return deny("NEEDS_USER", "attempt_budget_exhausted")
	}
	out.State = "READY"
	if f.Latest != nil {
		if f.Retries < f.Config.MaxRetries {
			out.State = "RETRY"
		} else if f.Escalations < f.Config.MaxEscalations && f.Escalations < len(f.Config.EscalationSessions) {
			out.State = "ESCALATE"
		} else {
			if f.Escalations < f.Config.MaxEscalations {
				return deny("NEEDS_USER", "escalation_target_required")
			}
			return deny("NEEDS_USER", "retry_and_escalation_budgets_exhausted")
		}
	}
	out.Target = f.Target
	if f.TargetProblem != "" {
		state := "BLOCKED"
		if f.TargetProblem != "target_busy" {
			state = "NEEDS_USER"
		}
		return deny(state, f.TargetProblem)
	}
	if f.Target == "" {
		return deny("NEEDS_USER", "explicit_target_required")
	}
	out.Allowed = f.Config.Auto
	out.Reason = "bounded_action_permitted"
	if !f.Config.Auto {
		out.Reason = "automation_disabled"
	}
	return out
}
