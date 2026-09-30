package domain

import (
	"errors"
	"time"
)

var (
	ErrPocketNotFound = errors.New("pocket state not found")
	ErrPocketConflict = errors.New("pocket state conflict")
	ErrPocketInvalid  = errors.New("invalid pocket state")
)

type PocketTaskState string

const (
	PocketTaskPending   PocketTaskState = "pending"
	PocketTaskActive    PocketTaskState = "active"
	PocketTaskCompleted PocketTaskState = "completed"
	PocketTaskFailed    PocketTaskState = "failed"
	PocketTaskCancelled PocketTaskState = "cancelled"
	PocketTaskBlocked   PocketTaskState = "blocked"
)

type PocketExecutionStateKind string

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

type PocketValidationScope string

const (
	PocketValidationTaskScope      PocketValidationScope = "task"
	PocketValidationExecutionScope PocketValidationScope = "execution"
)

type PocketValidationState string

const (
	PocketValidationPass    PocketValidationState = "pass"
	PocketValidationFail    PocketValidationState = "fail"
	PocketValidationUnknown PocketValidationState = "unknown"
)

type PocketValidationSourceKind string

const (
	PocketValidationDeterministic PocketValidationSourceKind = "deterministic"
	PocketValidationSemantic      PocketValidationSourceKind = "semantic"
	PocketValidationExternal      PocketValidationSourceKind = "external"
)

type PocketTask struct {
	ID        string          `json:"id"`
	ProjectID ProjectID       `json:"projectId,omitempty"`
	Objective string          `json:"objective,omitempty"`
	State     PocketTaskState `json:"state"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

type PocketWorker struct {
	ID        string    `json:"id"`
	SessionID SessionID `json:"sessionId"`
	CreatedAt time.Time `json:"createdAt"`
}

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

type PocketValidationRequirement struct {
	ID            string                `json:"id"`
	TaskID        string                `json:"taskId"`
	ExecutionID   string                `json:"executionId,omitempty"`
	Scope         PocketValidationScope `json:"scope"`
	CheckID       string                `json:"checkId"`
	Description   string                `json:"description,omitempty"`
	Deterministic bool                  `json:"deterministic"`
	Required      bool                  `json:"required"`
	CreatedAt     time.Time             `json:"createdAt"`
}

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

type PocketValidationEvidence struct {
	Requirement    PocketValidationRequirement `json:"requirement"`
	EffectiveState PocketValidationState       `json:"effectiveState"`
	Results        []PocketValidationResult     `json:"results"`
}

type PocketExecutionSnapshot struct {
	Task            PocketTask                 `json:"task"`
	Worker          PocketWorker               `json:"worker"`
	Execution       PocketExecution            `json:"execution"`
	Attempts        []PocketExecution           `json:"attempts"`
	ValidationState PocketValidationState      `json:"validationState"`
	Validation      []PocketValidationEvidence `json:"validation"`
}
