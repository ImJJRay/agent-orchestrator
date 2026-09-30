package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func validPocketTaskState(state domain.PocketTaskState) bool {
	switch state {
	case domain.PocketTaskPending, domain.PocketTaskActive, domain.PocketTaskCompleted,
		domain.PocketTaskFailed, domain.PocketTaskCancelled, domain.PocketTaskBlocked:
		return true
	default:
		return false
	}
}

func validPocketExecutionState(state domain.PocketExecutionStateKind) bool {
	switch state {
	case domain.PocketExecutionUnknown, domain.PocketExecutionQueued, domain.PocketExecutionRunning,
		domain.PocketExecutionCompleted, domain.PocketExecutionFailed, domain.PocketExecutionInterrupted:
		return true
	default:
		return false
	}
}

func validPocketValidationState(state domain.PocketValidationState) bool {
	switch state {
	case domain.PocketValidationPass, domain.PocketValidationFail, domain.PocketValidationUnknown:
		return true
	default:
		return false
	}
}

func validPocketValidationSource(kind domain.PocketValidationSourceKind) bool {
	switch kind {
	case domain.PocketValidationDeterministic, domain.PocketValidationSemantic, domain.PocketValidationExternal:
		return true
	default:
		return false
	}
}

func (s *Store) CreatePocketTask(ctx context.Context, projectID domain.ProjectID, objective string, now time.Time) (domain.PocketTask, error) {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return domain.PocketTask{}, fmt.Errorf("%w: objective is required", domain.ErrPocketInvalid)
	}
	if projectID != "" {
		var exists int
		if err := s.readDB.QueryRowContext(ctx, "SELECT 1 FROM projects WHERE id = ?", projectID).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.PocketTask{}, fmt.Errorf("%w: project %s", domain.ErrPocketNotFound, projectID)
			}
			return domain.PocketTask{}, fmt.Errorf("lookup project %s: %w", projectID, err)
		}
	}
	now = now.UTC()
	task := domain.PocketTask{
		ID: uuid.NewString(), ProjectID: projectID, Objective: objective,
		State: domain.PocketTaskActive, CreatedAt: now, UpdatedAt: now,
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.writeDB.ExecContext(ctx, `
INSERT INTO pocket_tasks (id, project_id, objective, state, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`, task.ID, task.ProjectID, task.Objective, task.State, task.CreatedAt, task.UpdatedAt); err != nil {
		return domain.PocketTask{}, fmt.Errorf("insert pocket task: %w", err)
	}
	return task, nil
}

func (s *Store) UpdatePocketTaskState(ctx context.Context, id string, state domain.PocketTaskState, now time.Time) (domain.PocketTask, error) {
	if !validPocketTaskState(state) {
		return domain.PocketTask{}, fmt.Errorf("%w: invalid task state %q", domain.ErrPocketInvalid, state)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	res, err := s.writeDB.ExecContext(ctx, "UPDATE pocket_tasks SET state = ?, updated_at = ? WHERE id = ?", state, now.UTC(), id)
	if err != nil {
		return domain.PocketTask{}, fmt.Errorf("update pocket task %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return domain.PocketTask{}, err
	}
	if n == 0 {
		return domain.PocketTask{}, fmt.Errorf("%w: task %s", domain.ErrPocketNotFound, id)
	}
	return scanPocketTask(s.writeDB.QueryRowContext(ctx, `
SELECT id, project_id, objective, state, created_at, updated_at
FROM pocket_tasks WHERE id = ?`, id))
}

func (s *Store) CreatePocketWorker(ctx context.Context, sessionID domain.SessionID, now time.Time) (domain.PocketWorker, error) {
	if sessionID == "" {
		return domain.PocketWorker{}, fmt.Errorf("%w: session id is required", domain.ErrPocketInvalid)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.qw.GetSession(ctx, sessionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.PocketWorker{}, fmt.Errorf("%w: session %s", domain.ErrPocketNotFound, sessionID)
		}
		return domain.PocketWorker{}, fmt.Errorf("lookup session %s: %w", sessionID, err)
	}
	if worker, err := scanPocketWorker(s.writeDB.QueryRowContext(ctx, `
SELECT id, session_id, created_at FROM pocket_workers WHERE session_id = ?`, sessionID)); err == nil {
		return worker, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return domain.PocketWorker{}, fmt.Errorf("lookup pocket worker for %s: %w", sessionID, err)
	}
	worker := domain.PocketWorker{ID: uuid.NewString(), SessionID: sessionID, CreatedAt: now.UTC()}
	if _, err := s.writeDB.ExecContext(ctx, `
INSERT INTO pocket_workers (id, session_id, created_at) VALUES (?, ?, ?)`,
		worker.ID, worker.SessionID, worker.CreatedAt); err != nil {
		return domain.PocketWorker{}, fmt.Errorf("insert pocket worker: %w", err)
	}
	return worker, nil
}

func (s *Store) CreatePocketExecution(
	ctx context.Context,
	taskID, workerID, conversationID, turnID, priorExecutionID string,
	state domain.PocketExecutionStateKind,
	now time.Time,
) (domain.PocketExecution, error) {
	if taskID == "" || workerID == "" {
		return domain.PocketExecution{}, fmt.Errorf("%w: taskId and workerId are required", domain.ErrPocketInvalid)
	}
	if (conversationID == "") != (turnID == "") {
		return domain.PocketExecution{}, fmt.Errorf("%w: conversationId and turnId must be supplied together", domain.ErrPocketInvalid)
	}
	if state == "" {
		state = domain.PocketExecutionUnknown
	}
	if !validPocketExecutionState(state) {
		return domain.PocketExecution{}, fmt.Errorf("%w: invalid execution state %q", domain.ErrPocketInvalid, state)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return domain.PocketExecution{}, fmt.Errorf("begin pocket execution: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var taskProject string
	if err := tx.QueryRowContext(ctx, "SELECT project_id FROM pocket_tasks WHERE id = ?", taskID).Scan(&taskProject); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.PocketExecution{}, fmt.Errorf("%w: task %s", domain.ErrPocketNotFound, taskID)
		}
		return domain.PocketExecution{}, fmt.Errorf("lookup pocket task %s: %w", taskID, err)
	}

	var sessionID string
	if err := tx.QueryRowContext(ctx, "SELECT session_id FROM pocket_workers WHERE id = ?", workerID).Scan(&sessionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.PocketExecution{}, fmt.Errorf("%w: worker %s", domain.ErrPocketNotFound, workerID)
		}
		return domain.PocketExecution{}, fmt.Errorf("lookup pocket worker %s: %w", workerID, err)
	}
	var sessionProject, workspacePath, workspaceRepoPath string
	if err := tx.QueryRowContext(ctx, `
SELECT project_id, workspace_path, workspace_repo_path FROM sessions WHERE id = ?`, sessionID).
		Scan(&sessionProject, &workspacePath, &workspaceRepoPath); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.PocketExecution{}, fmt.Errorf("%w: session %s", domain.ErrPocketNotFound, sessionID)
		}
		return domain.PocketExecution{}, fmt.Errorf("lookup session %s: %w", sessionID, err)
	}
	if taskProject != sessionProject {
		return domain.PocketExecution{}, fmt.Errorf("%w: task project %q does not match worker session project %q", domain.ErrPocketInvalid, taskProject, sessionProject)
	}

	var turnState string
	var retryOfTurn sql.NullString
	if turnID != "" {
		if err := tx.QueryRowContext(ctx, `
SELECT state, retry_of_turn_id
FROM conversation_turns
WHERE id = ? AND conversation_id = ? AND handled_by_session_id = ?`,
			turnID, conversationID, sessionID).Scan(&turnState, &retryOfTurn); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.PocketExecution{}, fmt.Errorf("%w: turn %s is not owned by session %s in conversation %s", domain.ErrPocketInvalid, turnID, sessionID, conversationID)
			}
			return domain.PocketExecution{}, fmt.Errorf("lookup conversation turn %s: %w", turnID, err)
		}
		if state == domain.PocketExecutionUnknown {
			state = domain.PocketExecutionStateKind(turnState)
		}
	}

	attempt := int64(1)
	if priorExecutionID == "" {
		var existing string
		err := tx.QueryRowContext(ctx, `
SELECT id FROM pocket_executions WHERE task_id = ? AND prior_execution_id IS NULL`, taskID).Scan(&existing)
		if err == nil {
			return domain.PocketExecution{}, fmt.Errorf("%w: task %s already has root execution %s", domain.ErrPocketConflict, taskID, existing)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return domain.PocketExecution{}, fmt.Errorf("lookup root pocket execution: %w", err)
		}
		if retryOfTurn.Valid {
			return domain.PocketExecution{}, fmt.Errorf("%w: retry turn %s requires priorExecutionId", domain.ErrPocketInvalid, turnID)
		}
	} else {
		var priorTask, priorTurn string
		var priorAttempt int64
		if err := tx.QueryRowContext(ctx, `
SELECT task_id, turn_id, attempt_number FROM pocket_executions WHERE id = ?`, priorExecutionID).
			Scan(&priorTask, &priorTurn, &priorAttempt); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.PocketExecution{}, fmt.Errorf("%w: prior execution %s", domain.ErrPocketNotFound, priorExecutionID)
			}
			return domain.PocketExecution{}, fmt.Errorf("lookup prior pocket execution %s: %w", priorExecutionID, err)
		}
		if priorTask != taskID {
			return domain.PocketExecution{}, fmt.Errorf("%w: prior execution belongs to task %s", domain.ErrPocketInvalid, priorTask)
		}
		if retryOfTurn.Valid && priorTurn != retryOfTurn.String {
			return domain.PocketExecution{}, fmt.Errorf("%w: AO retry turn %s points to %s, not prior execution turn %s", domain.ErrPocketInvalid, turnID, retryOfTurn.String, priorTurn)
		}
		attempt = priorAttempt + 1
	}

	now = now.UTC()
	exec := domain.PocketExecution{
		ID: uuid.NewString(), TaskID: taskID, WorkerID: workerID, AttemptNumber: attempt,
		PriorExecutionID: priorExecutionID, SessionID: domain.SessionID(sessionID),
		ConversationID: conversationID, TurnID: turnID, ProjectID: domain.ProjectID(sessionProject),
		WorkspacePath: workspacePath, WorkspaceRepoPath: workspaceRepoPath, State: state,
		CreatedAt: now, UpdatedAt: now,
	}
	if state == domain.PocketExecutionRunning {
		exec.StartedAt = &now
	}
	if state == domain.PocketExecutionCompleted || state == domain.PocketExecutionFailed || state == domain.PocketExecutionInterrupted {
		exec.StartedAt = &now
		exec.CompletedAt = &now
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO pocket_executions (
    id, task_id, worker_id, attempt_number, prior_execution_id,
    session_id, conversation_id, turn_id, project_id, workspace_path, workspace_repo_path,
    state, started_at, completed_at, created_at, updated_at
) VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		exec.ID, exec.TaskID, exec.WorkerID, exec.AttemptNumber, exec.PriorExecutionID,
		exec.SessionID, exec.ConversationID, exec.TurnID, exec.ProjectID, exec.WorkspacePath, exec.WorkspaceRepoPath,
		exec.State, exec.StartedAt, exec.CompletedAt, exec.CreatedAt, exec.UpdatedAt); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return domain.PocketExecution{}, fmt.Errorf("%w: execution lineage already exists", domain.ErrPocketConflict)
		}
		return domain.PocketExecution{}, fmt.Errorf("insert pocket execution: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return domain.PocketExecution{}, fmt.Errorf("commit pocket execution: %w", err)
	}
	return exec, nil
}

func (s *Store) UpdatePocketExecutionState(ctx context.Context, id string, state domain.PocketExecutionStateKind, now time.Time) (domain.PocketExecution, error) {
	if !validPocketExecutionState(state) {
		return domain.PocketExecution{}, fmt.Errorf("%w: invalid execution state %q", domain.ErrPocketInvalid, state)
	}
	now = now.UTC()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var query string
	switch state {
	case domain.PocketExecutionRunning:
		query = `UPDATE pocket_executions SET state = ?, started_at = COALESCE(started_at, ?), updated_at = ? WHERE id = ?`
	case domain.PocketExecutionCompleted, domain.PocketExecutionFailed, domain.PocketExecutionInterrupted:
		query = `UPDATE pocket_executions SET state = ?, started_at = COALESCE(started_at, ?), completed_at = ?, updated_at = ? WHERE id = ?`
	default:
		query = `UPDATE pocket_executions SET state = ?, updated_at = ? WHERE id = ?`
	}
	var res sql.Result
	var err error
	switch state {
	case domain.PocketExecutionRunning:
		res, err = s.writeDB.ExecContext(ctx, query, state, now, now, id)
	case domain.PocketExecutionCompleted, domain.PocketExecutionFailed, domain.PocketExecutionInterrupted:
		res, err = s.writeDB.ExecContext(ctx, query, state, now, now, now, id)
	default:
		res, err = s.writeDB.ExecContext(ctx, query, state, now, id)
	}
	if err != nil {
		return domain.PocketExecution{}, fmt.Errorf("update pocket execution %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return domain.PocketExecution{}, err
	}
	if n == 0 {
		return domain.PocketExecution{}, fmt.Errorf("%w: execution %s", domain.ErrPocketNotFound, id)
	}
	return scanPocketExecution(s.writeDB.QueryRowContext(ctx, pocketExecutionSelect+" WHERE id = ?", id))
}

func (s *Store) CreatePocketValidationRequirement(
	ctx context.Context,
	taskID, executionID, checkID, description string,
	scope domain.PocketValidationScope,
	deterministic, required bool,
	now time.Time,
) (domain.PocketValidationRequirement, error) {
	checkID = strings.TrimSpace(checkID)
	if taskID == "" || checkID == "" {
		return domain.PocketValidationRequirement{}, fmt.Errorf("%w: taskId and checkId are required", domain.ErrPocketInvalid)
	}
	switch scope {
	case domain.PocketValidationTaskScope:
		if executionID != "" {
			return domain.PocketValidationRequirement{}, fmt.Errorf("%w: task-scoped requirement cannot name executionId", domain.ErrPocketInvalid)
		}
	case domain.PocketValidationExecutionScope:
		if executionID == "" {
			return domain.PocketValidationRequirement{}, fmt.Errorf("%w: execution-scoped requirement requires executionId", domain.ErrPocketInvalid)
		}
	default:
		return domain.PocketValidationRequirement{}, fmt.Errorf("%w: invalid validation scope %q", domain.ErrPocketInvalid, scope)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var exists int
	if err := s.writeDB.QueryRowContext(ctx, "SELECT 1 FROM pocket_tasks WHERE id = ?", taskID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.PocketValidationRequirement{}, fmt.Errorf("%w: task %s", domain.ErrPocketNotFound, taskID)
		}
		return domain.PocketValidationRequirement{}, err
	}
	if executionID != "" {
		var executionTask string
		if err := s.writeDB.QueryRowContext(ctx, "SELECT task_id FROM pocket_executions WHERE id = ?", executionID).Scan(&executionTask); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.PocketValidationRequirement{}, fmt.Errorf("%w: execution %s", domain.ErrPocketNotFound, executionID)
			}
			return domain.PocketValidationRequirement{}, err
		}
		if executionTask != taskID {
			return domain.PocketValidationRequirement{}, fmt.Errorf("%w: execution belongs to task %s", domain.ErrPocketInvalid, executionTask)
		}
	}
	req := domain.PocketValidationRequirement{
		ID: uuid.NewString(), TaskID: taskID, ExecutionID: executionID, Scope: scope,
		CheckID: checkID, Description: strings.TrimSpace(description), Deterministic: deterministic,
		Required: required, CreatedAt: now.UTC(),
	}
	if _, err := s.writeDB.ExecContext(ctx, `
INSERT INTO pocket_validation_requirements
(id, task_id, execution_id, scope, check_id, description, deterministic, required, created_at)
VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?)`,
		req.ID, req.TaskID, req.ExecutionID, req.Scope, req.CheckID, req.Description,
		req.Deterministic, req.Required, req.CreatedAt); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return domain.PocketValidationRequirement{}, fmt.Errorf("%w: validation check %s already exists in scope", domain.ErrPocketConflict, checkID)
		}
		return domain.PocketValidationRequirement{}, fmt.Errorf("insert pocket validation requirement: %w", err)
	}
	return req, nil
}

func (s *Store) CreatePocketValidationResult(
	ctx context.Context,
	executionID, requirementID string,
	state domain.PocketValidationState,
	sourceKind domain.PocketValidationSourceKind,
	source, detail string,
	observedAt, now time.Time,
) (domain.PocketValidationResult, error) {
	if executionID == "" || requirementID == "" || strings.TrimSpace(source) == "" {
		return domain.PocketValidationResult{}, fmt.Errorf("%w: executionId, requirementId and source are required", domain.ErrPocketInvalid)
	}
	if !validPocketValidationState(state) || !validPocketValidationSource(sourceKind) {
		return domain.PocketValidationResult{}, fmt.Errorf("%w: invalid validation state or source kind", domain.ErrPocketInvalid)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var taskID string
	if err := s.writeDB.QueryRowContext(ctx, "SELECT task_id FROM pocket_executions WHERE id = ?", executionID).Scan(&taskID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.PocketValidationResult{}, fmt.Errorf("%w: execution %s", domain.ErrPocketNotFound, executionID)
		}
		return domain.PocketValidationResult{}, err
	}
	var requirementTask string
	var scopedExecution sql.NullString
	if err := s.writeDB.QueryRowContext(ctx, `
SELECT task_id, execution_id FROM pocket_validation_requirements WHERE id = ?`, requirementID).
		Scan(&requirementTask, &scopedExecution); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.PocketValidationResult{}, fmt.Errorf("%w: requirement %s", domain.ErrPocketNotFound, requirementID)
		}
		return domain.PocketValidationResult{}, err
	}
	if requirementTask != taskID || (scopedExecution.Valid && scopedExecution.String != executionID) {
		return domain.PocketValidationResult{}, fmt.Errorf("%w: validation requirement does not apply to execution", domain.ErrPocketInvalid)
	}
	if observedAt.IsZero() {
		observedAt = now
	}
	result := domain.PocketValidationResult{
		ID: uuid.NewString(), RequirementID: requirementID, TaskID: taskID, ExecutionID: executionID,
		State: state, SourceKind: sourceKind, Source: strings.TrimSpace(source), Detail: strings.TrimSpace(detail),
		ObservedAt: observedAt.UTC(), CreatedAt: now.UTC(),
	}
	if _, err := s.writeDB.ExecContext(ctx, `
INSERT INTO pocket_validation_results
(id, requirement_id, task_id, execution_id, state, source_kind, source, detail, observed_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		result.ID, result.RequirementID, result.TaskID, result.ExecutionID, result.State,
		result.SourceKind, result.Source, result.Detail, result.ObservedAt, result.CreatedAt); err != nil {
		return domain.PocketValidationResult{}, fmt.Errorf("insert pocket validation result: %w", err)
	}
	return result, nil
}

const pocketExecutionSelect = `
SELECT id, task_id, worker_id, attempt_number, prior_execution_id,
       session_id, conversation_id, turn_id, project_id, workspace_path, workspace_repo_path,
       state, started_at, completed_at, created_at, updated_at
FROM pocket_executions`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanPocketTask(row rowScanner) (domain.PocketTask, error) {
	var task domain.PocketTask
	if err := row.Scan(&task.ID, &task.ProjectID, &task.Objective, &task.State, &task.CreatedAt, &task.UpdatedAt); err != nil {
		return domain.PocketTask{}, err
	}
	return task, nil
}

func scanPocketWorker(row rowScanner) (domain.PocketWorker, error) {
	var worker domain.PocketWorker
	if err := row.Scan(&worker.ID, &worker.SessionID, &worker.CreatedAt); err != nil {
		return domain.PocketWorker{}, err
	}
	return worker, nil
}

func scanPocketExecution(row rowScanner) (domain.PocketExecution, error) {
	var execution domain.PocketExecution
	var prior sql.NullString
	var started, completed sql.NullTime
	if err := row.Scan(
		&execution.ID, &execution.TaskID, &execution.WorkerID, &execution.AttemptNumber, &prior,
		&execution.SessionID, &execution.ConversationID, &execution.TurnID, &execution.ProjectID,
		&execution.WorkspacePath, &execution.WorkspaceRepoPath, &execution.State,
		&started, &completed, &execution.CreatedAt, &execution.UpdatedAt,
	); err != nil {
		return domain.PocketExecution{}, err
	}
	if prior.Valid {
		execution.PriorExecutionID = prior.String
	}
	if started.Valid {
		v := started.Time
		execution.StartedAt = &v
	}
	if completed.Valid {
		v := completed.Time
		execution.CompletedAt = &v
	}
	return execution, nil
}

func (s *Store) PocketExecutionForSession(ctx context.Context, sessionID domain.SessionID, turnID string) (domain.PocketExecutionSnapshot, bool, error) {
	query := pocketExecutionSelect + " WHERE session_id = ?"
	args := []any{sessionID}
	if turnID != "" {
		query += " AND turn_id = ?"
		args = append(args, turnID)
	}
	query += " ORDER BY created_at DESC LIMIT 1"
	execution, err := scanPocketExecution(s.readDB.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.PocketExecutionSnapshot{}, false, nil
	}
	if err != nil {
		return domain.PocketExecutionSnapshot{}, false, fmt.Errorf("read pocket execution for session %s: %w", sessionID, err)
	}
	task, err := scanPocketTask(s.readDB.QueryRowContext(ctx, `
SELECT id, project_id, objective, state, created_at, updated_at FROM pocket_tasks WHERE id = ?`, execution.TaskID))
	if err != nil {
		return domain.PocketExecutionSnapshot{}, false, fmt.Errorf("read pocket task %s: %w", execution.TaskID, err)
	}
	worker, err := scanPocketWorker(s.readDB.QueryRowContext(ctx, `
SELECT id, session_id, created_at FROM pocket_workers WHERE id = ?`, execution.WorkerID))
	if err != nil {
		return domain.PocketExecutionSnapshot{}, false, fmt.Errorf("read pocket worker %s: %w", execution.WorkerID, err)
	}
	attemptRows, err := s.readDB.QueryContext(ctx, pocketExecutionSelect+" WHERE task_id = ? ORDER BY attempt_number, created_at", execution.TaskID)
	if err != nil {
		return domain.PocketExecutionSnapshot{}, false, fmt.Errorf("list pocket attempts for task %s: %w", execution.TaskID, err)
	}
	attempts := []domain.PocketExecution{}
	for attemptRows.Next() {
		attempt, scanErr := scanPocketExecution(attemptRows)
		if scanErr != nil {
			_ = attemptRows.Close()
			return domain.PocketExecutionSnapshot{}, false, scanErr
		}
		attempts = append(attempts, attempt)
	}
	if err := attemptRows.Close(); err != nil {
		return domain.PocketExecutionSnapshot{}, false, err
	}
	validation, aggregate, err := s.pocketValidationForExecution(ctx, execution)
	if err != nil {
		return domain.PocketExecutionSnapshot{}, false, err
	}
	return domain.PocketExecutionSnapshot{
		Task: task, Worker: worker, Execution: execution, Attempts: attempts,
		ValidationState: aggregate, Validation: validation,
	}, true, nil
}

func (s *Store) pocketValidationForExecution(ctx context.Context, execution domain.PocketExecution) ([]domain.PocketValidationEvidence, domain.PocketValidationState, error) {
	rows, err := s.readDB.QueryContext(ctx, `
SELECT id, task_id, COALESCE(execution_id, ''), scope, check_id, description, deterministic, required, created_at
FROM pocket_validation_requirements
WHERE task_id = ? AND (scope = 'task' OR execution_id = ?)
ORDER BY created_at, id`, execution.TaskID, execution.ID)
	if err != nil {
		return nil, domain.PocketValidationUnknown, fmt.Errorf("list pocket validation requirements: %w", err)
	}
	defer rows.Close()
	requirements := []domain.PocketValidationRequirement{}
	for rows.Next() {
		var req domain.PocketValidationRequirement
		if err := rows.Scan(&req.ID, &req.TaskID, &req.ExecutionID, &req.Scope, &req.CheckID,
			&req.Description, &req.Deterministic, &req.Required, &req.CreatedAt); err != nil {
			return nil, domain.PocketValidationUnknown, err
		}
		requirements = append(requirements, req)
	}
	if err := rows.Err(); err != nil {
		return nil, domain.PocketValidationUnknown, err
	}

	evidence := make([]domain.PocketValidationEvidence, 0, len(requirements))
	aggregate := domain.PocketValidationUnknown
	requiredSeen := false
	requiredUnknown := false
	for _, req := range requirements {
		resultRows, err := s.readDB.QueryContext(ctx, `
SELECT id, requirement_id, task_id, execution_id, state, source_kind, source, detail, observed_at, created_at
FROM pocket_validation_results
WHERE requirement_id = ? AND execution_id = ?
ORDER BY observed_at DESC, created_at DESC, id DESC`, req.ID, execution.ID)
		if err != nil {
			return nil, domain.PocketValidationUnknown, err
		}
		results := []domain.PocketValidationResult{}
		effective := domain.PocketValidationUnknown
		for resultRows.Next() {
			var result domain.PocketValidationResult
			if err := resultRows.Scan(&result.ID, &result.RequirementID, &result.TaskID, &result.ExecutionID,
				&result.State, &result.SourceKind, &result.Source, &result.Detail, &result.ObservedAt, &result.CreatedAt); err != nil {
				_ = resultRows.Close()
				return nil, domain.PocketValidationUnknown, err
			}
			results = append(results, result)
			if effective == domain.PocketValidationUnknown {
				if !req.Deterministic || result.SourceKind == domain.PocketValidationDeterministic {
					effective = result.State
				}
			}
		}
		if err := resultRows.Close(); err != nil {
			return nil, domain.PocketValidationUnknown, err
		}
		evidence = append(evidence, domain.PocketValidationEvidence{
			Requirement: req, EffectiveState: effective, Results: results,
		})
		if !req.Required {
			continue
		}
		requiredSeen = true
		switch effective {
		case domain.PocketValidationFail:
			aggregate = domain.PocketValidationFail
		case domain.PocketValidationUnknown:
			requiredUnknown = true
		case domain.PocketValidationPass:
			if aggregate != domain.PocketValidationFail {
				aggregate = domain.PocketValidationPass
			}
		}
	}
	if !requiredSeen || (aggregate != domain.PocketValidationFail && requiredUnknown) {
		aggregate = domain.PocketValidationUnknown
	}
	return evidence, aggregate, nil
}
