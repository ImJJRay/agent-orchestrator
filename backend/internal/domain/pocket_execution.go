package domain

import (
	"errors"
	"time"
)

var (
	// ErrPocketNotFound reports a missing Pocket durable-state record.
	ErrPocketNotFound = errors.New("pocket state not found")
	// ErrPocketConflict reports an incompatible or duplicate Pocket state transition.
	ErrPocketConflict = errors.New("pocket state conflict")
	// ErrPocketInvalid reports invalid Pocket state input or an invalid AO association.
	ErrPocketInvalid = errors.New("invalid pocket state")
)

// PocketTaskState is the durable lifecycle state of a Pocket task.
type PocketTaskState string

// Pocket task states.
const (
	PocketTaskPending   PocketTaskState = "pending"
	PocketTaskActive    PocketTaskState = "active"
	PocketTaskCompleted PocketTaskState = "completed"
	PocketTaskFailed    PocketTaskState = "failed"
	PocketTaskCancelled PocketTaskState = "cancelled"
	PocketTaskBlocked   PocketTaskState = "blocked"
)

// PocketExecutionStateKind is the durable lifecycle state of one execution attempt.
type PocketExecutionStateKind string

// Pocket execution states.
const (
	PocketExecutionUnknown     PocketExecutionStateKind = "unknown"
	PocketExecutionQueued      PocketExecutionStateKind = "queued"
	PocketExecutionRunning     PocketExecutionStateKind = "running"
	PocketExecutionCompleted   PocketExecutionStateKind = "completed"
	PocketExecutionRecovered   PocketExecutionStateKind = "recovered"
	PocketExecutionFailed      PocketExecutionStateKind = "failed"
	PocketExecutionInterrupted PocketExecutionStateKind = "interrupted"
	PocketExecutionCancelled   PocketExecutionStateKind = "cancelled"
)

// PocketValidationScope says whether a validation requirement applies to a task or one attempt.
type PocketValidationScope string

// Pocket validation requirement scopes.
const (
	PocketValidationTaskScope      PocketValidationScope = "task"
	PocketValidationExecutionScope PocketValidationScope = "execution"
)

// PocketValidationState is the explicit outcome of validation evidence.
type PocketValidationState string

// Pocket validation result states.
const (
	PocketValidationPass    PocketValidationState = "pass"
	PocketValidationFail    PocketValidationState = "fail"
	PocketValidationUnknown PocketValidationState = "unknown"
)

// PocketValidationSourceKind identifies the provenance class of validation evidence.
type PocketValidationSourceKind string

// Pocket validation evidence source kinds.
const (
	PocketValidationDeterministic PocketValidationSourceKind = "deterministic"
	PocketValidationSemantic      PocketValidationSourceKind = "semantic"
	PocketValidationExternal      PocketValidationSourceKind = "external"
)

// PocketTask is the durable policy identity for one desired outcome.
type PocketTask struct {
	ID        string          `json:"id"`
	ProjectID ProjectID       `json:"projectId,omitempty"`
	Objective string          `json:"objective,omitempty"`
	State     PocketTaskState `json:"state"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

// PocketWorker is a logical worker identity anchored to an AO session.
type PocketWorker struct {
	ID        string    `json:"id"`
	SessionID SessionID `json:"sessionId"`
	CreatedAt time.Time `json:"createdAt"`
}

// PocketExecution is one durable attempt to progress a Pocket task.
type PocketExecution struct {
	ID                string                   `json:"id"`
	TaskID            string                   `json:"taskId"`
	WorkerID          string                   `json:"workerId"`
	AttemptNumber     int64                    `json:"attemptNumber"`
	PriorExecutionID  string                   `json:"priorExecutionId,omitempty"`
	SessionID         SessionID                `json:"sessionId"`
	ConversationID    string                   `json:"conversationId,omitempty"`
	TurnID            string                   `json:"turnId,omitempty"`
	ProjectID         ProjectID                `json:"projectId,omitempty"`
	WorkspacePath     string                   `json:"workspacePath,omitempty"`
	WorkspaceRepoPath string                   `json:"workspaceRepoPath,omitempty"`
	State             PocketExecutionStateKind `json:"state"`
	StartedAt         *time.Time               `json:"startedAt,omitempty"`
	CompletedAt       *time.Time               `json:"completedAt,omitempty"`
	CreatedAt         time.Time                `json:"createdAt"`
	UpdatedAt         time.Time                `json:"updatedAt"`
}

// PocketValidationRequirement declares one durable validation check.
type PocketValidationRequirement struct {
	ID            string                `json:"id"`
	TaskID        string                `json:"taskId"`
	ExecutionID   string                `json:"executionId,omitempty"`
	Scope         PocketValidationScope `json:"scope"`
	CheckID       string                `json:"checkId"`
	Description   string                `json:"description,omitempty"`
	Command       string                `json:"command,omitempty"`
	Deterministic bool                  `json:"deterministic"`
	Required      bool                  `json:"required"`
	CreatedAt     time.Time             `json:"createdAt"`
}

// PocketValidationResult is one append-only observation for a validation requirement.
type PocketValidationResult struct {
	ID            string                     `json:"id"`
	RequirementID string                     `json:"requirementId"`
	TaskID        string                     `json:"taskId"`
	ExecutionID   string                     `json:"executionId"`
	State         PocketValidationState      `json:"state"`
	SourceKind    PocketValidationSourceKind `json:"sourceKind"`
	Source        string                     `json:"source"`
	Detail        string                     `json:"detail,omitempty"`
	ObservedAt    time.Time                  `json:"observedAt"`
	CreatedAt     time.Time                  `json:"createdAt"`
}

// PocketValidationEvidence combines a requirement with its retained observations and effective state.
type PocketValidationEvidence struct {
	Requirement    PocketValidationRequirement `json:"requirement"`
	EffectiveState PocketValidationState       `json:"effectiveState"`
	Results        []PocketValidationResult    `json:"results"`
}

// PocketValidationWorkItem is one deterministic requirement ready to execute
// against the AO worktree snapshotted by its execution attempt.
type PocketValidationWorkItem struct {
	Execution   PocketExecution             `json:"execution"`
	Requirement PocketValidationRequirement `json:"requirement"`
}

// PocketExecutionSnapshot is the durable task/worker/attempt/validation view for a session execution.
type PocketExecutionSnapshot struct {
	Task            PocketTask                 `json:"task"`
	Worker          PocketWorker               `json:"worker"`
	Execution       PocketExecution            `json:"execution"`
	Attempts        []PocketExecution          `json:"attempts"`
	ValidationState PocketValidationState      `json:"validationState"`
	Validation      []PocketValidationEvidence `json:"validation"`
}
