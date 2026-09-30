package controllers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// PocketStateService is the narrow durable policy-state contract used by Pocket.
// AO sessions, conversations, worktrees, lifecycle and usage remain owned by
// their existing services; this surface only associates durable policy identity
// and validation evidence with those facts.
type PocketStateService interface {
	CreatePocketTask(context.Context, domain.ProjectID, string, time.Time) (domain.PocketTask, error)
	UpdatePocketTaskState(context.Context, string, domain.PocketTaskState, time.Time) (domain.PocketTask, error)
	CreatePocketWorker(context.Context, domain.SessionID, time.Time) (domain.PocketWorker, error)
	CreatePocketExecution(context.Context, string, string, string, string, string, domain.PocketExecutionStateKind, time.Time) (domain.PocketExecution, error)
	BindPocketExecutionTurn(context.Context, string, string, string, time.Time) (domain.PocketExecution, error)
	UpdatePocketExecutionState(context.Context, string, domain.PocketExecutionStateKind, time.Time) (domain.PocketExecution, error)
	CreatePocketValidationRequirement(context.Context, string, string, string, string, domain.PocketValidationScope, bool, bool, time.Time) (domain.PocketValidationRequirement, error)
	CreatePocketValidationResult(context.Context, string, string, domain.PocketValidationState, domain.PocketValidationSourceKind, string, string, time.Time, time.Time) (domain.PocketValidationResult, error)
	PocketExecutionForSession(context.Context, domain.SessionID, string) (domain.PocketExecutionSnapshot, bool, error)
}

// PocketStateController exposes daemon-loopback-only Pocket state operations.
// The router owns the local-control boundary; these handlers intentionally do
// not become part of the public/mobile /api/v1 contract.
type PocketStateController struct {
	Svc PocketStateService
	Now func() time.Time
}

func (c *PocketStateController) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

// Register mounts internal Pocket routes on the supplied root router.
func (c *PocketStateController) Register(r chi.Router) {
	r.Post("/internal/pocket/tasks", c.createTask)
	r.Get("/internal/pocket/tasks/{taskId}/policy", c.getPolicy)
	r.Get("/internal/pocket/tasks/{taskId}/decisions", c.getDecisions)
	r.Put("/internal/pocket/tasks/{taskId}/policy", c.configurePolicy)
	r.Put("/internal/pocket/tasks/{taskId}/dependencies", c.setDependencies)
	r.Patch("/internal/pocket/tasks/{taskId}", c.updateTask)
	r.Post("/internal/pocket/workers", c.createWorker)
	r.Post("/internal/pocket/tasks/{taskId}/executions", c.createExecution)
	r.Post("/internal/pocket/executions/{executionId}/turn", c.bindExecutionTurn)
	r.Patch("/internal/pocket/executions/{executionId}", c.updateExecution)
	r.Post("/internal/pocket/tasks/{taskId}/validation-requirements", c.createValidationRequirement)
	r.Post("/internal/pocket/executions/{executionId}/validation-results", c.createValidationResult)
	r.Get("/internal/pocket/sessions/{sessionId}/execution", c.getSessionExecution)
}

type createPocketTaskRequest struct {
	ProjectID string `json:"projectId"`
	Objective string `json:"objective"`
}

type updatePocketTaskRequest struct {
	State domain.PocketTaskState `json:"state"`
}

type createPocketWorkerRequest struct {
	SessionID string `json:"sessionId"`
}

type createPocketExecutionRequest struct {
	WorkerID         string                          `json:"workerId"`
	ConversationID   string                          `json:"conversationId"`
	TurnID           string                          `json:"turnId"`
	PriorExecutionID string                          `json:"priorExecutionId"`
	State            domain.PocketExecutionStateKind `json:"state"`
}

type bindPocketExecutionTurnRequest struct {
	ConversationID string `json:"conversationId"`
	TurnID         string `json:"turnId"`
}

type updatePocketExecutionRequest struct {
	State domain.PocketExecutionStateKind `json:"state"`
}

type createPocketValidationRequirementRequest struct {
	ExecutionID   string                       `json:"executionId"`
	Scope         domain.PocketValidationScope `json:"scope"`
	CheckID       string                       `json:"checkId"`
	Description   string                       `json:"description"`
	Command       string                       `json:"command"`
	Deterministic *bool                        `json:"deterministic"`
	Required      *bool                        `json:"required"`
}

type createPocketValidationResultRequest struct {
	RequirementID string                            `json:"requirementId"`
	State         domain.PocketValidationState      `json:"state"`
	SourceKind    domain.PocketValidationSourceKind `json:"sourceKind"`
	Source        string                            `json:"source"`
	Detail        string                            `json:"detail"`
	ObservedAt    *time.Time                        `json:"observedAt"`
}

func (c *PocketStateController) createTask(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		writePocketError(w, r, errors.New("pocket state service unavailable"))
		return
	}
	var req createPocketTaskRequest
	if err := decodeJSONStrict(r, &req); err != nil {
		writePocketBadJSON(w, r)
		return
	}
	task, err := c.Svc.CreatePocketTask(r.Context(), domain.ProjectID(req.ProjectID), req.Objective, c.now())
	if err != nil {
		writePocketError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, task)
}

func (c *PocketStateController) updateTask(w http.ResponseWriter, r *http.Request) {
	var req updatePocketTaskRequest
	if err := decodeJSONStrict(r, &req); err != nil {
		writePocketBadJSON(w, r)
		return
	}
	task, err := c.Svc.UpdatePocketTaskState(r.Context(), chi.URLParam(r, "taskId"), req.State, c.now())
	if err != nil {
		writePocketError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, task)
}

func (c *PocketStateController) createWorker(w http.ResponseWriter, r *http.Request) {
	var req createPocketWorkerRequest
	if err := decodeJSONStrict(r, &req); err != nil {
		writePocketBadJSON(w, r)
		return
	}
	worker, err := c.Svc.CreatePocketWorker(r.Context(), domain.SessionID(req.SessionID), c.now())
	if err != nil {
		writePocketError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, worker)
}

func (c *PocketStateController) createExecution(w http.ResponseWriter, r *http.Request) {
	var req createPocketExecutionRequest
	if err := decodeJSONStrict(r, &req); err != nil {
		writePocketBadJSON(w, r)
		return
	}
	execution, err := c.Svc.CreatePocketExecution(
		r.Context(), chi.URLParam(r, "taskId"), req.WorkerID, req.ConversationID,
		req.TurnID, req.PriorExecutionID, req.State, c.now(),
	)
	if err != nil {
		writePocketError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, execution)
}

func (c *PocketStateController) bindExecutionTurn(w http.ResponseWriter, r *http.Request) {
	var req bindPocketExecutionTurnRequest
	if err := decodeJSONStrict(r, &req); err != nil {
		writePocketBadJSON(w, r)
		return
	}
	execution, err := c.Svc.BindPocketExecutionTurn(
		r.Context(), chi.URLParam(r, "executionId"), req.ConversationID, req.TurnID, c.now(),
	)
	if err != nil {
		writePocketError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, execution)
}

func (c *PocketStateController) updateExecution(w http.ResponseWriter, r *http.Request) {
	var req updatePocketExecutionRequest
	if err := decodeJSONStrict(r, &req); err != nil {
		writePocketBadJSON(w, r)
		return
	}
	execution, err := c.Svc.UpdatePocketExecutionState(r.Context(), chi.URLParam(r, "executionId"), req.State, c.now())
	if err != nil {
		writePocketError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, execution)
}

func (c *PocketStateController) createValidationRequirement(w http.ResponseWriter, r *http.Request) {
	var req createPocketValidationRequirementRequest
	if err := decodeJSONStrict(r, &req); err != nil {
		writePocketBadJSON(w, r)
		return
	}
	deterministic := true
	if req.Deterministic != nil {
		deterministic = *req.Deterministic
	}
	required := true
	if req.Required != nil {
		required = *req.Required
	}
	var requirement domain.PocketValidationRequirement
	var err error
	if strings.TrimSpace(req.Command) != "" {
		commandSvc, ok := c.Svc.(interface {
			CreatePocketValidationCommandRequirement(
				context.Context, string, string, string, string, string,
				domain.PocketValidationScope, bool, bool, time.Time,
			) (domain.PocketValidationRequirement, error)
		})
		if !ok {
			writePocketError(w, r, errors.New("pocket validation command service unavailable"))
			return
		}
		requirement, err = commandSvc.CreatePocketValidationCommandRequirement(
			r.Context(), chi.URLParam(r, "taskId"), req.ExecutionID, req.CheckID, req.Description,
			req.Command, req.Scope, deterministic, required, c.now(),
		)
	} else {
		requirement, err = c.Svc.CreatePocketValidationRequirement(
			r.Context(), chi.URLParam(r, "taskId"), req.ExecutionID, req.CheckID, req.Description,
			req.Scope, deterministic, required, c.now(),
		)
	}
	if err != nil {
		writePocketError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, requirement)
}

func (c *PocketStateController) createValidationResult(w http.ResponseWriter, r *http.Request) {
	var req createPocketValidationResultRequest
	if err := decodeJSONStrict(r, &req); err != nil {
		writePocketBadJSON(w, r)
		return
	}
	observedAt := time.Time{}
	if req.ObservedAt != nil {
		observedAt = req.ObservedAt.UTC()
	}
	result, err := c.Svc.CreatePocketValidationResult(
		r.Context(), chi.URLParam(r, "executionId"), req.RequirementID,
		req.State, req.SourceKind, req.Source, req.Detail, observedAt, c.now(),
	)
	if err != nil {
		writePocketError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, result)
}

func (c *PocketStateController) getSessionExecution(w http.ResponseWriter, r *http.Request) {
	sessionID := domain.SessionID(strings.TrimSpace(chi.URLParam(r, "sessionId")))
	snapshot, ok, err := c.Svc.PocketExecutionForSession(r.Context(), sessionID, strings.TrimSpace(r.URL.Query().Get("turnId")))
	if err != nil {
		writePocketError(w, r, err)
		return
	}
	if !ok {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "POCKET_EXECUTION_NOT_FOUND", "no durable Pocket execution is associated with this session/turn", nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, snapshot)
}

func writePocketBadJSON(w http.ResponseWriter, r *http.Request) {
	envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "request body must be valid JSON", nil)
}

func writePocketError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrPocketInvalid):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "POCKET_INVALID", err.Error(), nil)
	case errors.Is(err, domain.ErrPocketNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "POCKET_NOT_FOUND", err.Error(), nil)
	case errors.Is(err, domain.ErrPocketConflict):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "POCKET_CONFLICT", err.Error(), nil)
	default:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal_error", "POCKET_INTERNAL", "Pocket state operation failed", nil)
	}
}
