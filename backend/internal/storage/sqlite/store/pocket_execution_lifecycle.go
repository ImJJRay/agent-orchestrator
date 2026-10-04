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

type pocketTurnFact struct {
	ID             string
	ConversationID string
	SessionID      domain.SessionID
	RetryOfTurnID  string
	State          domain.TurnState
	Objective      string
	Origin         domain.MessageOrigin
	RequestedAt    time.Time
	StartedAt      *time.Time
	CompletedAt    *time.Time
}

func scanPocketTurnFact(row rowScanner) (pocketTurnFact, error) {
	var fact pocketTurnFact
	var retry sql.NullString
	var started, completed sql.NullTime
	if err := row.Scan(
		&fact.ID,
		&fact.ConversationID,
		&fact.SessionID,
		&retry,
		&fact.State,
		&fact.Objective,
		&fact.Origin,
		&fact.RequestedAt,
		&started,
		&completed,
	); err != nil {
		return pocketTurnFact{}, err
	}
	if retry.Valid {
		fact.RetryOfTurnID = retry.String
	}
	if started.Valid {
		value := started.Time.UTC()
		fact.StartedAt = &value
	}
	if completed.Valid {
		value := completed.Time.UTC()
		fact.CompletedAt = &value
	}
	fact.RequestedAt = fact.RequestedAt.UTC()
	return fact, nil
}

func pocketTrackableOrigin(origin domain.MessageOrigin) bool {
	return origin == domain.MessageOriginHuman || origin == domain.MessageOriginAutomation
}

func pocketExecutionStateFromTurn(state domain.TurnState) domain.PocketExecutionStateKind {
	switch state {
	case domain.TurnStateQueued:
		return domain.PocketExecutionQueued
	case domain.TurnStateRunning:
		return domain.PocketExecutionRunning
	case domain.TurnStateCompleted:
		return domain.PocketExecutionCompleted
	case domain.TurnStateRecovered:
		return domain.PocketExecutionRecovered
	case domain.TurnStateFailed:
		return domain.PocketExecutionFailed
	case domain.TurnStateInterrupted:
		return domain.PocketExecutionInterrupted
	case domain.TurnStateCancelled:
		return domain.PocketExecutionCancelled
	default:
		return domain.PocketExecutionUnknown
	}
}

func pocketExecutionMayAdvance(current, observed domain.PocketExecutionStateKind) bool {
	if pocketExecutionTerminal(current) || observed == domain.PocketExecutionUnknown {
		return false
	}
	switch current {
	case domain.PocketExecutionUnknown:
		return true
	case domain.PocketExecutionQueued:
		return observed != domain.PocketExecutionUnknown
	case domain.PocketExecutionRunning:
		return pocketExecutionTerminal(observed) || observed == domain.PocketExecutionRunning
	default:
		return false
	}
}

func pocketExecutionTimes(fact pocketTurnFact) (*time.Time, *time.Time) {
	return fact.StartedAt, fact.CompletedAt
}

func (s *Store) pocketTurnFactTx(ctx context.Context, tx *sql.Tx, turnID string) (pocketTurnFact, error) {
	row := tx.QueryRowContext(ctx, `
SELECT
    t.id,
    t.conversation_id,
    t.handled_by_session_id,
    t.retry_of_turn_id,
    t.state,
    COALESCE((
        SELECT m.text
        FROM conversation_messages m
        WHERE m.conversation_id = t.conversation_id
          AND m.turn_id = t.id
          AND m.role = 'user'
        ORDER BY m.sequence
        LIMIT 1
    ), ''),
    COALESCE((
        SELECT m.origin
        FROM conversation_messages m
        WHERE m.conversation_id = t.conversation_id
          AND m.turn_id = t.id
          AND m.role = 'user'
        ORDER BY m.sequence
        LIMIT 1
    ), ''),
    t.requested_at,
    t.started_at,
    t.completed_at
FROM conversation_turns t
WHERE t.id = ?
  AND COALESCE(t.handled_by_review_id, '') = ''
LIMIT 1`, turnID)
	fact, err := scanPocketTurnFact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return pocketTurnFact{}, fmt.Errorf("%w: conversation turn %s", domain.ErrPocketNotFound, turnID)
	}
	if err != nil {
		return pocketTurnFact{}, fmt.Errorf("read conversation turn %s for Pocket lifecycle: %w", turnID, err)
	}
	if !pocketTrackableOrigin(fact.Origin) {
		return pocketTurnFact{}, fmt.Errorf("%w: conversation turn %s origin %q is not Pocket-managed work", domain.ErrPocketInvalid, turnID, fact.Origin)
	}
	return fact, nil
}

func (s *Store) ensurePocketWorkerTx(ctx context.Context, tx *sql.Tx, sessionID domain.SessionID, now time.Time) (domain.PocketWorker, error) {
	var worker domain.PocketWorker
	err := tx.QueryRowContext(ctx,
		`SELECT id, session_id, created_at FROM pocket_workers WHERE session_id = ?`,
		sessionID,
	).Scan(&worker.ID, &worker.SessionID, &worker.CreatedAt)
	if err == nil {
		return worker, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.PocketWorker{}, fmt.Errorf("read Pocket worker for session %s: %w", sessionID, err)
	}
	worker = domain.PocketWorker{
		ID:        uuid.NewString(),
		SessionID: sessionID,
		CreatedAt: now.UTC(),
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO pocket_workers (id, session_id, created_at) VALUES (?, ?, ?)`,
		worker.ID, worker.SessionID, worker.CreatedAt,
	); err != nil {
		return domain.PocketWorker{}, fmt.Errorf("insert Pocket worker for session %s: %w", sessionID, err)
	}
	return worker, nil
}

func (s *Store) ensurePocketExecutionForTurnTx(
	ctx context.Context,
	tx *sql.Tx,
	turnID string,
	now time.Time,
	visiting map[string]bool,
) (domain.PocketExecution, bool, error) {
	// Bind reservations before generic projection, closing the dispatch race.
	action, actionErr := scanPocketAction(tx.QueryRowContext(ctx, pocketActionSelect+` WHERE (session_id=(SELECT handled_by_session_id FROM conversation_turns WHERE id=?) OR prior_turn_id IN (SELECT retry_of_turn_id FROM conversation_turns WHERE id=?)) AND ((prior_turn_id=(SELECT retry_of_turn_id FROM conversation_turns WHERE id=?)) OR EXISTS (SELECT 1 FROM conversation_messages m WHERE m.turn_id=? AND m.client_message_id='pocket:' || pocket_actions.decision_id)) LIMIT 1`, turnID, turnID, turnID, turnID))
	if actionErr == nil {
		if _, err := s.bindPocketActionTx(ctx, tx, action); err != nil {
			return domain.PocketExecution{}, false, err
		}
	} else if !errors.Is(actionErr, sql.ErrNoRows) {
		return domain.PocketExecution{}, false, actionErr
	}
	var existingID string
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM pocket_executions WHERE turn_id = ? LIMIT 1`,
		turnID,
	).Scan(&existingID)
	if err == nil {
		execution, scanErr := scanPocketExecution(tx.QueryRowContext(ctx, pocketExecutionSelect+" WHERE id = ?", existingID))
		return execution, false, scanErr
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return domain.PocketExecution{}, false, fmt.Errorf("lookup Pocket execution for turn %s: %w", turnID, err)
	}
	if visiting[turnID] {
		return domain.PocketExecution{}, false, fmt.Errorf("%w: retry lineage cycle at turn %s", domain.ErrPocketInvalid, turnID)
	}
	if len(visiting) >= 64 {
		return domain.PocketExecution{}, false, fmt.Errorf("%w: retry lineage exceeds 64 attempts", domain.ErrPocketInvalid)
	}
	visiting[turnID] = true
	defer delete(visiting, turnID)

	fact, err := s.pocketTurnFactTx(ctx, tx, turnID)
	if err != nil {
		return domain.PocketExecution{}, false, err
	}
	var projectID, workspacePath, workspaceRepoPath string
	var sessionKind domain.SessionKind
	if err := tx.QueryRowContext(ctx, `
SELECT project_id, workspace_path, workspace_repo_path, kind
FROM sessions
WHERE id = ?`, fact.SessionID).Scan(&projectID, &workspacePath, &workspaceRepoPath, &sessionKind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.PocketExecution{}, false, fmt.Errorf("%w: session %s", domain.ErrPocketNotFound, fact.SessionID)
		}
		return domain.PocketExecution{}, false, fmt.Errorf("read session %s for Pocket lifecycle: %w", fact.SessionID, err)
	}
	if sessionKind != domain.KindWorker {
		return domain.PocketExecution{}, false, fmt.Errorf("%w: session %s kind %q is not Pocket worker execution", domain.ErrPocketInvalid, fact.SessionID, sessionKind)
	}

	worker, err := s.ensurePocketWorkerTx(ctx, tx, fact.SessionID, fact.RequestedAt)
	if err != nil {
		return domain.PocketExecution{}, false, err
	}

	var taskID string
	priorExecutionID := ""
	attemptNumber := int64(1)
	if fact.RetryOfTurnID != "" {
		prior, _, err := s.ensurePocketExecutionForTurnTx(ctx, tx, fact.RetryOfTurnID, now, visiting)
		if err != nil {
			return domain.PocketExecution{}, false, fmt.Errorf("ensure prior Pocket attempt for retry %s: %w", fact.ID, err)
		}
		var occupied string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM pocket_executions WHERE prior_execution_id=? AND turn_id<>?", prior.ID, fact.ID).Scan(&occupied); err == nil {
			return domain.PocketExecution{}, false, fmt.Errorf("%w: concurrent AO retry requires human reconciliation", domain.ErrPocketInvalid)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return domain.PocketExecution{}, false, err
		}
		taskID = prior.TaskID
		priorExecutionID = prior.ID
		attemptNumber = prior.AttemptNumber + 1
	} else {
		taskID = uuid.NewString()
		createdAt := fact.RequestedAt
		if createdAt.IsZero() {
			createdAt = now.UTC()
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO pocket_tasks (id, project_id, objective, state, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`,
			taskID,
			projectID,
			strings.TrimSpace(fact.Objective),
			domain.PocketTaskActive,
			createdAt,
			createdAt,
		); err != nil {
			return domain.PocketExecution{}, false, fmt.Errorf("insert automatic Pocket task for turn %s: %w", fact.ID, err)
		}
	}

	state := pocketExecutionStateFromTurn(fact.State)
	createdAt := fact.RequestedAt
	if createdAt.IsZero() {
		createdAt = now.UTC()
	}
	startedAt, completedAt := pocketExecutionTimes(fact)
	execution := domain.PocketExecution{
		ID:                uuid.NewString(),
		TaskID:            taskID,
		WorkerID:          worker.ID,
		AttemptNumber:     attemptNumber,
		PriorExecutionID:  priorExecutionID,
		SessionID:         fact.SessionID,
		ConversationID:    fact.ConversationID,
		TurnID:            fact.ID,
		ProjectID:         domain.ProjectID(projectID),
		WorkspacePath:     workspacePath,
		WorkspaceRepoPath: workspaceRepoPath,
		State:             state,
		StartedAt:         startedAt,
		CompletedAt:       completedAt,
		CreatedAt:         createdAt,
		UpdatedAt:         now.UTC(),
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO pocket_executions (
    id, task_id, worker_id, attempt_number, prior_execution_id,
    session_id, conversation_id, turn_id, project_id, workspace_path, workspace_repo_path,
    state, started_at, completed_at, created_at, updated_at
) VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		execution.ID,
		execution.TaskID,
		execution.WorkerID,
		execution.AttemptNumber,
		execution.PriorExecutionID,
		execution.SessionID,
		execution.ConversationID,
		execution.TurnID,
		execution.ProjectID,
		execution.WorkspacePath,
		execution.WorkspaceRepoPath,
		execution.State,
		execution.StartedAt,
		execution.CompletedAt,
		execution.CreatedAt,
		execution.UpdatedAt,
	); err != nil {
		return domain.PocketExecution{}, false, fmt.Errorf("insert automatic Pocket execution for turn %s: %w", fact.ID, err)
	}
	return execution, true, nil
}

// EnsurePocketExecutionForTurn makes one durable AO user/automation turn visible
// to Pocket. Retry turns recursively bind their explicit AO retry source first,
// so a retry of a pre-Pocket legacy turn creates exactly the lineage needed for
// the new attempt without bulk-converting older conversation history.
func (s *Store) EnsurePocketExecutionForTurn(
	ctx context.Context,
	turnID string,
	now time.Time,
) (domain.PocketExecution, bool, error) {
	if strings.TrimSpace(turnID) == "" {
		return domain.PocketExecution{}, false, fmt.Errorf("%w: turnId is required", domain.ErrPocketInvalid)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return domain.PocketExecution{}, false, fmt.Errorf("begin automatic Pocket turn projection: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	execution, created, err := s.ensurePocketExecutionForTurnTx(ctx, tx, turnID, now.UTC(), map[string]bool{})
	if err != nil {
		return domain.PocketExecution{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return domain.PocketExecution{}, false, fmt.Errorf("commit automatic Pocket turn projection: %w", err)
	}
	return execution, created, nil
}

func (s *Store) pocketUntrackedTurnIDs(ctx context.Context) ([]string, error) {
	rows, err := s.readDB.QueryContext(ctx, `
SELECT DISTINCT t.id
FROM conversation_turns t
JOIN conversation_messages m
  ON m.conversation_id = t.conversation_id
 AND m.turn_id = t.id
 AND m.role = 'user'
JOIN pocket_lifecycle_state lifecycle
  ON lifecycle.singleton = 1
JOIN sessions session
  ON session.id = t.handled_by_session_id
 AND session.kind = 'worker'
LEFT JOIN pocket_executions execution
  ON execution.turn_id = t.id
WHERE execution.id IS NULL
  AND t.requested_at >= lifecycle.automation_started_at
  AND COALESCE(t.handled_by_review_id, '') = ''
  AND m.origin IN ('human', 'automation')
ORDER BY t.requested_at, t.id`)
	if err != nil {
		return nil, fmt.Errorf("list untracked Pocket turns: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan untracked Pocket turn: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate untracked Pocket turns: %w", err)
	}
	return ids, nil
}

func (s *Store) reconcilePocketExecutionRows(ctx context.Context, now time.Time) error {
	type row struct {
		id                string
		currentState      domain.PocketExecutionStateKind
		turnState         domain.TurnState
		startedAt         sql.NullTime
		completedAt       sql.NullTime
		projectID         string
		workspacePath     string
		workspaceRepoPath string
	}
	rows, err := s.readDB.QueryContext(ctx, `
SELECT
    execution.id,
    execution.state,
    turn.state,
    turn.started_at,
    turn.completed_at,
    session.project_id,
    session.workspace_path,
    session.workspace_repo_path
FROM pocket_executions execution
JOIN conversation_turns turn
  ON turn.id = execution.turn_id
 AND turn.conversation_id = execution.conversation_id
 AND turn.handled_by_session_id = execution.session_id
JOIN sessions session
  ON session.id = execution.session_id
WHERE execution.turn_id <> ''
  AND (
      execution.state IN ('unknown', 'queued', 'running')
      OR execution.project_id = ''
      OR execution.workspace_path = ''
      OR execution.workspace_repo_path = ''
  )`)
	if err != nil {
		return fmt.Errorf("list Pocket executions for reconciliation: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var pending []row
	for rows.Next() {
		var item row
		if err := rows.Scan(
			&item.id,
			&item.currentState,
			&item.turnState,
			&item.startedAt,
			&item.completedAt,
			&item.projectID,
			&item.workspacePath,
			&item.workspaceRepoPath,
		); err != nil {
			return fmt.Errorf("scan Pocket execution reconciliation row: %w", err)
		}
		pending = append(pending, item)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate Pocket execution reconciliation rows: %w", err)
	}
	if len(pending) == 0 {
		return nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Pocket execution reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, item := range pending {
		observed := pocketExecutionStateFromTurn(item.turnState)
		next := item.currentState
		if pocketExecutionMayAdvance(item.currentState, observed) {
			next = observed
		}
		var startedAt any
		if item.startedAt.Valid {
			startedAt = item.startedAt.Time.UTC()
		}
		var completedAt any
		if item.completedAt.Valid {
			completedAt = item.completedAt.Time.UTC()
		}
		// The policy fingerprint includes UpdatedAt. Unchanged polls, including
		// incomplete workspaces, must not advance it.
		if _, err := tx.ExecContext(ctx, `
UPDATE pocket_executions
SET
    project_id = CASE WHEN project_id = '' THEN ? ELSE project_id END,
    workspace_path = CASE WHEN workspace_path = '' THEN ? ELSE workspace_path END,
    workspace_repo_path = CASE WHEN workspace_repo_path = '' THEN ? ELSE workspace_repo_path END,
    state = ?,
    started_at = COALESCE(started_at, ?),
    completed_at = CASE
        WHEN ? IN ('completed', 'recovered', 'failed', 'interrupted', 'cancelled')
        THEN COALESCE(completed_at, ?)
        ELSE completed_at
    END,
    updated_at = ?
WHERE id = ?
  AND (
      (project_id = '' AND ? <> '')
      OR (workspace_path = '' AND ? <> '')
      OR (workspace_repo_path = '' AND ? <> '')
      OR state <> ?
      OR (started_at IS NULL AND ? IS NOT NULL)
      OR (completed_at IS NULL AND ? IS NOT NULL
          AND ? IN ('completed', 'recovered', 'failed', 'interrupted', 'cancelled'))
  )`,
			item.projectID,
			item.workspacePath,
			item.workspaceRepoPath,
			next,
			startedAt,
			next,
			completedAt,
			now.UTC(),
			item.id,
			item.projectID,
			item.workspacePath,
			item.workspaceRepoPath,
			next,
			startedAt,
			completedAt,
			next,
		); err != nil {
			return fmt.Errorf("reconcile Pocket execution %s: %w", item.id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Pocket execution reconciliation: %w", err)
	}
	return nil
}

// ReconcilePocketExecutionLifecycle projects new AO Chat work created after the
// lifecycle watermark and then refreshes existing attempts from AO's durable
// turn/session facts. It is idempotent and safe to call on a timer and after
// daemon recovery.
func (s *Store) ReconcilePocketExecutionLifecycle(ctx context.Context, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	turnIDs, err := s.pocketUntrackedTurnIDs(ctx)
	if err != nil {
		return err
	}
	for _, turnID := range turnIDs {
		if _, _, err := s.EnsurePocketExecutionForTurn(ctx, turnID, now); err != nil {
			if errors.Is(err, domain.ErrPocketInvalid) || errors.Is(err, domain.ErrPocketNotFound) {
				continue
			}
			return err
		}
	}
	return s.reconcilePocketExecutionRows(ctx, now)
}

// PendingPocketDeterministicValidations returns deterministic checks that apply
// to completed/recovered attempts and have no deterministic observation yet.
// A semantic/external result never suppresses a deterministic check.
func (s *Store) PendingPocketDeterministicValidations(
	ctx context.Context,
	limit int,
) ([]domain.PocketValidationWorkItem, error) {
	if limit <= 0 {
		limit = 16
	}
	rows, err := s.readDB.QueryContext(ctx, `
SELECT
    execution.id,
    execution.task_id,
    execution.worker_id,
    execution.attempt_number,
    execution.prior_execution_id,
    execution.session_id,
    execution.conversation_id,
    execution.turn_id,
    execution.project_id,
    execution.workspace_path,
    execution.workspace_repo_path,
    execution.state,
    execution.started_at,
    execution.completed_at,
    execution.created_at,
    execution.updated_at,
    requirement.id,
    requirement.task_id,
    requirement.execution_id,
    requirement.scope,
    requirement.check_id,
    requirement.description,
    requirement.command,
    requirement.deterministic,
    requirement.required,
    requirement.created_at
FROM pocket_executions execution
JOIN pocket_validation_requirements requirement
  ON requirement.task_id = execution.task_id
 AND (
      requirement.scope = 'task'
      OR (requirement.scope = 'execution' AND requirement.execution_id = execution.id)
 )
WHERE execution.state IN ('completed', 'recovered')
  AND requirement.deterministic = TRUE
  AND NOT EXISTS (
      SELECT 1
      FROM pocket_validation_results result
      WHERE result.requirement_id = requirement.id
        AND result.execution_id = execution.id
        AND result.source_kind = 'deterministic'
  )
ORDER BY COALESCE(execution.completed_at, execution.updated_at), execution.attempt_number, requirement.created_at
LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending deterministic Pocket validation: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := make([]domain.PocketValidationWorkItem, 0)
	for rows.Next() {
		var item domain.PocketValidationWorkItem
		var prior, reqExecution sql.NullString
		var started, completed sql.NullTime
		if err := rows.Scan(
			&item.Execution.ID,
			&item.Execution.TaskID,
			&item.Execution.WorkerID,
			&item.Execution.AttemptNumber,
			&prior,
			&item.Execution.SessionID,
			&item.Execution.ConversationID,
			&item.Execution.TurnID,
			&item.Execution.ProjectID,
			&item.Execution.WorkspacePath,
			&item.Execution.WorkspaceRepoPath,
			&item.Execution.State,
			&started,
			&completed,
			&item.Execution.CreatedAt,
			&item.Execution.UpdatedAt,
			&item.Requirement.ID,
			&item.Requirement.TaskID,
			&reqExecution,
			&item.Requirement.Scope,
			&item.Requirement.CheckID,
			&item.Requirement.Description,
			&item.Requirement.Command,
			&item.Requirement.Deterministic,
			&item.Requirement.Required,
			&item.Requirement.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan pending deterministic Pocket validation: %w", err)
		}
		if prior.Valid {
			item.Execution.PriorExecutionID = prior.String
		}
		if started.Valid {
			value := started.Time.UTC()
			item.Execution.StartedAt = &value
		}
		if completed.Valid {
			value := completed.Time.UTC()
			item.Execution.CompletedAt = &value
		}
		if reqExecution.Valid {
			item.Requirement.ExecutionID = reqExecution.String
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending deterministic Pocket validation: %w", err)
	}
	return items, nil
}
