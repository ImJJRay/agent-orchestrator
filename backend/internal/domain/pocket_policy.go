package domain

import "time"

// PocketPolicyConfig authorizes a bounded task on explicit AO sessions. Targets
// are supplied by the user; policy never selects a model or harness.
type PocketPolicyConfig struct {
	Auto               bool        `json:"auto"`
	SessionID          SessionID   `json:"sessionId"`
	EscalationSessions []SessionID `json:"escalationSessions"`
	MaxAttempts        int         `json:"maxAttempts"`
	MaxRetries         int         `json:"maxRetries"`
	MaxEscalations     int         `json:"maxEscalations"`
}

// PocketPolicyFacts is a durable snapshot consumed by the single policy evaluator.
type PocketPolicyFacts struct {
	Task              PocketTask         `json:"task"`
	Config            PocketPolicyConfig `json:"config"`
	Revision          int64              `json:"revision"`
	Dependencies      []string           `json:"dependencies"`
	BlockedBy         []string           `json:"blockedBy"`
	Attempts          int                `json:"attempts"`
	Retries           int                `json:"retries"`
	Escalations       int                `json:"escalations"`
	Latest            *PocketExecution   `json:"latest,omitempty"`
	ValidationFailed  bool               `json:"validationFailed"`
	ValidationPending bool               `json:"validationPending"`
	ValidationUnknown bool               `json:"validationUnknown"`
	SourceUnsafe      bool               `json:"sourceUnsafe"`
	Target            SessionID          `json:"target"`
	TargetProblem     string             `json:"targetProblem,omitempty"`
	NativeRetry       bool               `json:"nativeRetry"`
	Outstanding       bool               `json:"outstanding"`
	DeliveryUncertain bool               `json:"deliveryUncertain"`
}

// PocketPolicyOutcome explains the next action, without granting acceptance or merge authority.
type PocketPolicyOutcome struct {
	State   string    `json:"state"`
	Reason  string    `json:"reason"`
	Allowed bool      `json:"allowed"`
	Target  SessionID `json:"target,omitempty"`
}

// PocketDecision is an immutable audit entry. Identical fact snapshots reuse its ID.
type PocketDecision struct {
	Sequence  int64               `json:"sequence"`
	ID        string              `json:"id"`
	TaskID    string              `json:"taskId"`
	Facts     PocketPolicyFacts   `json:"facts"`
	Outcome   PocketPolicyOutcome `json:"outcome"`
	CreatedAt time.Time           `json:"createdAt"`
}

// PocketAction is the durable outbox reservation for one policy decision.
type PocketAction struct {
	Events      []PocketActionEvent `json:"events,omitempty"`
	DecisionID  string              `json:"decisionId"`
	ExecutionID string              `json:"executionId"`
	TaskID      string              `json:"taskId"`
	SessionID   SessionID           `json:"sessionId"`
	PriorTurnID string              `json:"priorTurnId,omitempty"`
	NativeRetry bool                `json:"nativeRetry"`
	Prompt      string              `json:"prompt"`
	Status      string              `json:"status"`
	Error       string              `json:"error,omitempty"`
}

// PocketPolicyView exposes derived readiness and retained policy/action history.
type PocketPolicyView struct {
	Facts     PocketPolicyFacts   `json:"facts"`
	Policy    PocketPolicyOutcome `json:"policy"`
	Decisions []PocketDecision    `json:"decisions"`
	Actions   []PocketAction      `json:"actions"`
}

// PocketDecisionPage is a bounded history page; NextBefore retrieves older entries.
type PocketDecisionPage struct {
	Decisions  []PocketDecision `json:"decisions"`
	NextBefore int64            `json:"nextBefore,omitempty"`
}

// PocketActionEvent retains dispatch, admission refusal and recovery transitions.
type PocketActionEvent struct {
	Sequence  int64     `json:"sequence"`
	Status    string    `json:"status"`
	Detail    string    `json:"detail,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}
