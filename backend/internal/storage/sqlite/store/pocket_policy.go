package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/pocket"
)

func defaultPocketConfig() domain.PocketPolicyConfig {
	return domain.PocketPolicyConfig{MaxAttempts: 3, MaxRetries: 1, MaxEscalations: 1, EscalationSessions: []domain.SessionID{}}
}

// ConfigurePocketPolicy persists explicit authorization and finite budgets.
func (s *Store) ConfigurePocketPolicy(ctx context.Context, taskID string, config domain.PocketPolicyConfig) error {
	if config.MaxAttempts < 1 || config.MaxAttempts > 64 || config.MaxRetries < 0 || config.MaxRetries > 63 || config.MaxEscalations < 0 || config.MaxEscalations > 63 || len(config.EscalationSessions) > 63 {
		return fmt.Errorf("%w: attempts must be 1..64; retry/escalation limits 0..63", domain.ErrPocketInvalid)
	}
	if config.Auto && config.SessionID == "" {
		return fmt.Errorf("%w: auto requires an explicit AO session", domain.ErrPocketInvalid)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var project string
	if err := tx.QueryRowContext(ctx, "SELECT project_id FROM pocket_tasks WHERE id = ?", taskID).Scan(&project); err != nil {
		return pocketLookupError(err, taskID)
	}
	seen := map[domain.SessionID]bool{}
	targets := append([]domain.SessionID{config.SessionID}, config.EscalationSessions...)
	for i, id := range targets {
		if id == "" && i == 0 {
			continue
		}
		if strings.TrimSpace(string(id)) == "" || seen[id] {
			return fmt.Errorf("%w: escalation sessions must be distinct explicit targets", domain.ErrPocketInvalid)
		}
		seen[id] = true
		var p, mode, kind string
		if err := tx.QueryRowContext(ctx, "SELECT project_id, session_mode, kind FROM sessions WHERE id = ?", id).Scan(&p, &mode, &kind); err != nil {
			return pocketLookupError(err, string(id))
		}
		if p != project || mode != "chat" || kind != "worker" {
			return fmt.Errorf("%w: target must be a Chat worker in the task project", domain.ErrPocketInvalid)
		}
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO pocket_policy_configs (task_id, config_json) VALUES (?,?) ON CONFLICT(task_id) DO UPDATE SET config_json=excluded.config_json, revision=revision+1`, taskID, string(encoded))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func pocketLookupError(err error, id string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s", domain.ErrPocketNotFound, id)
	}
	return err
}

// SetPocketDependencies replaces a task's prerequisite edges atomically. Existing
// executions cannot acquire new prerequisites retroactively.
func (s *Store) SetPocketDependencies(ctx context.Context, taskID string, dependencies []string) error {
	if len(dependencies) > 256 {
		return fmt.Errorf("%w: at most 256 dependencies", domain.ErrPocketInvalid)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var project string
	if err := tx.QueryRowContext(ctx, "SELECT project_id FROM pocket_tasks WHERE id=?", taskID).Scan(&project); err != nil {
		return pocketLookupError(err, taskID)
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM pocket_executions WHERE task_id=?", taskID).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("%w: dependencies are fixed once execution begins", domain.ErrPocketConflict)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM pocket_dependencies WHERE task_id=?", taskID); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, dep := range dependencies {
		if dep == taskID || seen[dep] || strings.TrimSpace(dep) == "" {
			return fmt.Errorf("%w: self, blank or duplicate dependency", domain.ErrPocketInvalid)
		}
		seen[dep] = true
		var depProject string
		if err := tx.QueryRowContext(ctx, "SELECT project_id FROM pocket_tasks WHERE id=?", dep).Scan(&depProject); err != nil {
			return pocketLookupError(err, dep)
		}
		if depProject != project {
			return fmt.Errorf("%w: dependency must belong to the same project", domain.ErrPocketInvalid)
		}
		var cycle int
		if err := tx.QueryRowContext(ctx, `WITH RECURSIVE reachable(id) AS (SELECT dependency_id FROM pocket_dependencies WHERE task_id=? UNION SELECT d.dependency_id FROM pocket_dependencies d JOIN reachable r ON d.task_id=r.id) SELECT COUNT(*) FROM reachable WHERE id=?`, dep, taskID).Scan(&cycle); err != nil {
			return err
		}
		if cycle > 0 {
			return fmt.Errorf("%w: dependency cycle", domain.ErrPocketInvalid)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO pocket_dependencies(task_id,dependency_id) VALUES (?,?)", taskID, dep); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func pocketValidationFactsTx(ctx context.Context, tx *sql.Tx, taskID, executionID string) (failed, pending, unknown bool, err error) {
	rows, err := tx.QueryContext(ctx, `SELECT r.deterministic,r.command, COALESCE((SELECT v.state FROM pocket_validation_results v WHERE v.requirement_id=r.id AND v.execution_id=? AND (NOT r.deterministic OR v.source_kind='deterministic') ORDER BY v.observed_at DESC,v.created_at DESC,v.id DESC LIMIT 1),'missing') FROM pocket_validation_requirements r WHERE r.task_id=? AND r.required AND (r.scope='task' OR r.execution_id=?)`, executionID, taskID, executionID)
	if err != nil {
		return false, false, false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var deterministic bool
		var command, state string
		if err := rows.Scan(&deterministic, &command, &state); err != nil {
			return false, false, false, err
		}
		switch state {
		case "fail":
			failed = true
		case "missing":
			if deterministic && command != "" {
				pending = true
			} else {
				unknown = true
			}
		case "unknown":
			unknown = true
		}
	}
	return failed, pending, unknown, rows.Err()
}

func (s *Store) pocketPolicyFactsTx(ctx context.Context, tx *sql.Tx, taskID, exclude string) (domain.PocketPolicyFacts, error) {
	f := domain.PocketPolicyFacts{Config: defaultPocketConfig(), Dependencies: []string{}, BlockedBy: []string{}}
	task, err := scanPocketTask(tx.QueryRowContext(ctx, `SELECT id,project_id,objective,state,created_at,updated_at FROM pocket_tasks WHERE id=?`, taskID))
	if err != nil {
		return f, pocketLookupError(err, taskID)
	}
	f.Task = task
	var encoded string
	err = tx.QueryRowContext(ctx, "SELECT config_json,revision FROM pocket_policy_configs WHERE task_id=?", taskID).Scan(&encoded, &f.Revision)
	if err == nil {
		if err := json.Unmarshal([]byte(encoded), &f.Config); err != nil {
			return f, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return f, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT d.dependency_id,t.state,COALESCE((SELECT id FROM pocket_executions WHERE task_id=t.id ORDER BY attempt_number DESC LIMIT 1),'') FROM pocket_dependencies d JOIN pocket_tasks t ON t.id=d.dependency_id WHERE d.task_id=? ORDER BY d.dependency_id`, taskID)
	if err != nil {
		return f, err
	}
	type depFact struct{ id, state, execution string }
	defer func() { _ = rows.Close() }()
	var deps []depFact
	for rows.Next() {
		var d depFact
		if err := rows.Scan(&d.id, &d.state, &d.execution); err != nil {
			_ = rows.Close()
			return f, err
		}
		deps = append(deps, d)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return f, err
	}
	for _, d := range deps {
		f.Dependencies = append(f.Dependencies, d.id)
		fail, pending, unknown, err := pocketValidationFactsTx(ctx, tx, d.id, d.execution)
		if err != nil {
			return f, err
		}
		if d.state != "completed" || fail || pending || unknown {
			f.BlockedBy = append(f.BlockedBy, d.id)
		}
	}
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM pocket_executions WHERE task_id=? AND id<>?", taskID, exclude).Scan(&f.Attempts); err != nil {
		return f, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(kind='RETRY'),0),COALESCE(SUM(kind='ESCALATE' AND status<>'superseded'),0),COALESCE(SUM(status IN ('prepared','dispatching')),0)>0,COALESCE(SUM(status='uncertain'),0)>0 FROM pocket_actions WHERE task_id=? AND execution_id<>?`, taskID, exclude).Scan(&f.Retries, &f.Escalations, &f.Outstanding, &f.DeliveryUncertain); err != nil {
		return f, err
	}
	// Manual AO retries count too: every non-root attempt not recorded as an escalation consumes retry budget.
	f.Retries = f.Attempts - 1 - f.Escalations
	if f.Retries < 0 {
		f.Retries = 0
	}
	latest, err := scanPocketExecution(tx.QueryRowContext(ctx, pocketExecutionSelect+" WHERE task_id=? AND id<>? ORDER BY attempt_number DESC LIMIT 1", taskID, exclude))
	if err == nil {
		f.Latest = &latest
		f.ValidationFailed, f.ValidationPending, f.ValidationUnknown, err = pocketValidationFactsTx(ctx, tx, taskID, latest.ID)
		if err != nil {
			return f, err
		}
		if latest.TurnID == "" {
			f.SourceUnsafe = true
		} else {
			var provider, state, origin string
			var rolledBack sql.NullTime
			err = tx.QueryRowContext(ctx, `SELECT t.provider_turn_id,t.state,t.rolled_back_at,COALESCE((SELECT origin FROM conversation_messages WHERE turn_id=t.id AND role='user' ORDER BY sequence LIMIT 1),'') FROM conversation_turns t WHERE t.id=? AND t.handled_by_session_id=?`, latest.TurnID, latest.SessionID).Scan(&provider, &state, &rolledBack, &origin)
			if errors.Is(err, sql.ErrNoRows) {
				f.SourceUnsafe = true
			} else if err != nil {
				return f, err
			} else {
				f.SourceUnsafe = rolledBack.Valid || state != string(latest.State) || ((state == "failed" || state == "interrupted") && provider == "")
				f.NativeRetry = state == "failed" && origin == "human" && !f.SourceUnsafe
			}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return f, err
	}
	if f.Latest != nil {
		var pending int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM conversation_activities WHERE conversation_id=? AND kind IN ('approval','user_input') AND status='pending'", f.Latest.ConversationID).Scan(&pending); err != nil {
			return f, err
		}
		if pending > 0 {
			f.SourceUnsafe = true
		}
	}
	var untrackedRetries int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM conversation_turns t JOIN pocket_executions p ON p.turn_id=t.retry_of_turn_id LEFT JOIN pocket_executions child ON child.turn_id=t.id WHERE p.task_id=? AND child.id IS NULL`, taskID).Scan(&untrackedRetries); err != nil {
		return f, err
	}
	if untrackedRetries > 0 {
		f.SourceUnsafe = true
	}
	f.Target = f.Config.SessionID
	if f.Latest != nil {
		f.Target = f.Latest.SessionID
		if f.Retries >= f.Config.MaxRetries && f.Escalations < len(f.Config.EscalationSessions) {
			f.Target = f.Config.EscalationSessions[f.Escalations]
			f.NativeRetry = false
		}
	}
	if f.Latest != nil && !f.NativeRetry {
		var attachments int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM conversation_messages WHERE turn_id=? AND role='user' AND delivery_content_json<>''", f.Latest.TurnID).Scan(&attachments); err != nil {
			return f, err
		}
		if attachments > 0 {
			f.SourceUnsafe = true
		}
	}
	if f.Target != "" {
		var mode, kind, project, provision string
		var terminated bool
		err := tx.QueryRowContext(ctx, "SELECT session_mode,kind,project_id,is_terminated,provision_state FROM sessions WHERE id=?", f.Target).Scan(&mode, &kind, &project, &terminated, &provision)
		if errors.Is(err, sql.ErrNoRows) {
			f.TargetProblem = "target_missing"
		} else if err != nil {
			return f, err
		} else if terminated || mode != "chat" || kind != "worker" || project != string(f.Task.ProjectID) || provision != "ready" {
			f.TargetProblem = "target_unavailable"
		} else {
			var busy, approval int
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM conversation_turns WHERE handled_by_session_id=? AND state IN ('queued','running') AND rolled_back_at IS NULL", f.Target).Scan(&busy); err != nil {
				return f, err
			}
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM conversation_activities a JOIN conversations c ON c.id=a.conversation_id WHERE (c.session_id=? OR c.current_session_id=?) AND a.kind IN ('approval','user_input') AND a.status='pending'`, f.Target, f.Target).Scan(&approval); err != nil {
				return f, err
			}
			if approval > 0 {
				f.TargetProblem = "pending_user_input"
			} else if busy > 0 {
				f.TargetProblem = "target_busy"
			}
			var reserved int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pocket_actions WHERE session_id=? AND execution_id<>? AND status IN ('prepared','dispatching','uncertain') AND task_id<>?`, f.Target, exclude, taskID).Scan(&reserved); err != nil {
				return f, err
			}
			if reserved > 0 {
				f.TargetProblem = "target_busy"
			}
		}
	}
	return f, nil
}

func recordPocketDecisionTx(ctx context.Context, tx *sql.Tx, f domain.PocketPolicyFacts, now time.Time) (domain.PocketDecision, error) {
	out := pocket.Evaluate(f)
	factsJSON, err := json.Marshal(f)
	if err != nil {
		return domain.PocketDecision{}, err
	}
	outJSON, err := json.Marshal(out)
	if err != nil {
		return domain.PocketDecision{}, err
	}
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(factsJSON))
	d := domain.PocketDecision{ID: uuid.NewString(), TaskID: f.Task.ID, Facts: f, Outcome: out, CreatedAt: now.UTC()}
	_, err = tx.ExecContext(ctx, `INSERT INTO pocket_decisions(id,task_id,fingerprint,facts_json,outcome_json,created_at) VALUES (?,?,?,?,?,?) ON CONFLICT(task_id,fingerprint) DO NOTHING`, d.ID, d.TaskID, fingerprint, string(factsJSON), string(outJSON), d.CreatedAt)
	if err != nil {
		return d, err
	}
	err = tx.QueryRowContext(ctx, "SELECT id,created_at FROM pocket_decisions WHERE task_id=? AND fingerprint=?", d.TaskID, fingerprint).Scan(&d.ID, &d.CreatedAt)
	return d, err
}

// ReconcilePocketPolicy records both permitted and denied outcomes and reserves
// at most one child of any prior attempt, atomically with its audit decision.
func (s *Store) ReconcilePocketPolicy(ctx context.Context, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, "SELECT id FROM pocket_tasks ORDER BY created_at,id")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		f, err := s.pocketPolicyFactsTx(ctx, tx, id, "")
		if err != nil {
			return err
		}
		d, err := recordPocketDecisionTx(ctx, tx, f, now)
		if err != nil {
			return err
		}
		if !d.Outcome.Allowed {
			continue
		}
		if err := s.reservePocketActionTx(ctx, tx, d, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) reservePocketActionTx(ctx context.Context, tx *sql.Tx, d domain.PocketDecision, now time.Time) error {
	f := d.Facts
	worker, err := s.ensurePocketWorkerTx(ctx, tx, d.Outcome.Target, now)
	if err != nil {
		return err
	}
	var path, repo string
	if err := tx.QueryRowContext(ctx, "SELECT workspace_path,workspace_repo_path FROM sessions WHERE id=?", d.Outcome.Target).Scan(&path, &repo); err != nil {
		return err
	}
	prior, turn := "", ""
	if f.Latest != nil {
		prior = f.Latest.ID
		turn = f.Latest.TurnID
	}
	executionID := uuid.NewString()
	result, err := tx.ExecContext(ctx, `INSERT INTO pocket_executions(id,task_id,worker_id,attempt_number,prior_execution_id,session_id,project_id,workspace_path,workspace_repo_path,state,created_at,updated_at) VALUES (?,?,?,?,NULLIF(?,''),?,?,?,?, 'queued',?,?) ON CONFLICT DO NOTHING`, executionID, f.Task.ID, worker.ID, f.Attempts+1, prior, worker.SessionID, f.Task.ProjectID, path, repo, now.UTC(), now.UTC())
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	prompt := f.Task.Objective
	if prior != "" {
		prompt = fmt.Sprintf("%s\n\nPocket %s of execution %s. Prior deterministic validation failed: %t. Resolve the failure, preserve scope and report artifacts. This does not authorize task acceptance or Git merge.", prompt, d.Outcome.State, prior, f.ValidationFailed)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO pocket_actions(decision_id,execution_id,task_id,session_id,prior_turn_id,native_retry,prompt,kind,status) VALUES (?,?,?,?,?,?,?,?,'prepared')`, d.ID, executionID, f.Task.ID, worker.SessionID, turn, f.NativeRetry && d.Outcome.State == "RETRY", prompt, d.Outcome.State)
	if err != nil {
		return err
	}
	// Execution-scoped requirements follow remediation attempts; evidence does not.
	if prior != "" {
		_, err = tx.ExecContext(ctx, `INSERT INTO pocket_validation_requirements(id,task_id,execution_id,scope,check_id,description,command,deterministic,required,created_at) SELECT ? || ':' || id,task_id,?,'execution',check_id,description,command,deterministic,required,? FROM pocket_validation_requirements WHERE execution_id=?`, executionID, executionID, now.UTC(), prior)
	}
	return err
}

const pocketActionSelect = `SELECT decision_id,execution_id,task_id,session_id,prior_turn_id,native_retry,prompt,status,error FROM pocket_actions`

func scanPocketAction(row rowScanner) (domain.PocketAction, error) {
	var a domain.PocketAction
	err := row.Scan(&a.DecisionID, &a.ExecutionID, &a.TaskID, &a.SessionID, &a.PriorTurnID, &a.NativeRetry, &a.Prompt, &a.Status, &a.Error)
	return a, err
}

// ClaimPocketAction rechecks current policy before dispatch, including changed
// dependencies, authorization, budgets and AO busy/approval facts.
func (s *Store) ClaimPocketAction(ctx context.Context, now time.Time) (domain.PocketAction, bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return domain.PocketAction{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, pocketActionSelect+" WHERE status='prepared' ORDER BY rowid")
	if err != nil {
		return domain.PocketAction{}, false, err
	}
	defer func() { _ = rows.Close() }()
	var actions []domain.PocketAction
	defer func(r *sql.Rows) { _ = r.Close() }(rows)
	for rows.Next() {
		a, err := scanPocketAction(rows)
		if err != nil {
			_ = rows.Close()
			return a, false, err
		}
		actions = append(actions, a)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return domain.PocketAction{}, false, err
	}
	for _, a := range actions {
		if found, err := s.bindPocketActionTx(ctx, tx, a); err != nil {
			return a, false, err
		} else if found {
			continue
		}
		f, err := s.pocketPolicyFactsTx(ctx, tx, a.TaskID, a.ExecutionID)
		if err != nil {
			return a, false, err
		}
		d, err := recordPocketDecisionTx(ctx, tx, f, now)
		if err != nil {
			return a, false, err
		}
		var originalJSON string
		if err := tx.QueryRowContext(ctx, "SELECT facts_json FROM pocket_decisions WHERE id=?", a.DecisionID).Scan(&originalJSON); err != nil {
			return a, false, err
		}
		var original domain.PocketPolicyFacts
		if err := json.Unmarshal([]byte(originalJSON), &original); err != nil {
			return a, false, err
		}
		if !d.Outcome.Allowed || d.Outcome.Target != a.SessionID || d.Outcome.State != pocket.Evaluate(original).State {
			continue
		}
		if _, err := tx.ExecContext(ctx, "UPDATE pocket_actions SET status='dispatching' WHERE decision_id=?", a.DecisionID); err != nil {
			return a, false, err
		}
		if err := tx.Commit(); err != nil {
			return a, false, err
		}
		a.Status = "dispatching"
		return a, true, nil
	}
	return domain.PocketAction{}, false, tx.Commit()
}

func (s *Store) bindPocketActionTx(ctx context.Context, tx *sql.Tx, a domain.PocketAction) (bool, error) {
	var turn, conv string
	err := tx.QueryRowContext(ctx, `SELECT t.id,t.conversation_id FROM conversation_turns t WHERE t.handled_by_session_id=? AND ((? AND t.retry_of_turn_id=?) OR EXISTS (SELECT 1 FROM conversation_messages m WHERE m.turn_id=t.id AND m.client_message_id=?)) ORDER BY t.requested_at LIMIT 1`, a.SessionID, a.NativeRetry, a.PriorTurnID, "pocket:"+a.DecisionID).Scan(&turn, &conv)
	if errors.Is(err, sql.ErrNoRows) && !a.NativeRetry && a.PriorTurnID != "" {
		// Human/native AO retries take precedence over an undispatched escalation.
		// Adopt that authoritative turn into the existing reservation, never run both.
		var actualSession domain.SessionID
		err = tx.QueryRowContext(ctx, `SELECT t.id,t.conversation_id,t.handled_by_session_id FROM conversation_turns t JOIN pocket_executions p ON p.turn_id=t.retry_of_turn_id WHERE t.retry_of_turn_id=? AND t.handled_by_session_id=p.session_id ORDER BY t.requested_at LIMIT 1`, a.PriorTurnID).Scan(&turn, &conv, &actualSession)
		if err == nil {
			worker, err := s.ensurePocketWorkerTx(ctx, tx, actualSession, time.Now().UTC())
			if err != nil {
				return false, err
			}
			result, err := tx.ExecContext(ctx, `UPDATE pocket_executions SET conversation_id=?,turn_id=?,session_id=?,worker_id=?,workspace_path=(SELECT workspace_path FROM sessions WHERE id=?),workspace_repo_path=(SELECT workspace_repo_path FROM sessions WHERE id=?) WHERE id=? AND turn_id=''`, conv, turn, actualSession, worker.ID, actualSession, actualSession, a.ExecutionID)
			if err != nil {
				return false, err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return false, err
			}
			if n > 0 {
				if _, err := tx.ExecContext(ctx, "UPDATE pocket_actions SET status='superseded',error='adopted authoritative AO retry before policy dispatch' WHERE decision_id=?", a.DecisionID); err != nil {
					return false, err
				}
			}
			return true, nil
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE pocket_executions SET conversation_id=?,turn_id=? WHERE id=? AND turn_id=''", conv, turn, a.ExecutionID); err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, "UPDATE pocket_actions SET status='dispatched',error='' WHERE decision_id=?", a.DecisionID)
	return true, err
}

// SettlePocketAction reconnects to AO even when dispatch returned an error after
// persistence. With no AO fact it fails closed instead of retrying an uncertain call.
func (s *Store) SettlePocketAction(ctx context.Context, a domain.PocketAction, dispatchErr error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	found, err := s.bindPocketActionTx(ctx, tx, a)
	if err != nil {
		return err
	}
	if !found && errors.Is(dispatchErr, domain.ErrPocketAdmissionBlocked) {
		if _, err := tx.ExecContext(ctx, "UPDATE pocket_actions SET status='prepared',error=? WHERE decision_id=?", dispatchErr.Error(), a.DecisionID); err != nil {
			return err
		}
	} else if !found {
		detail := "AO returned no durable turn"
		if dispatchErr != nil {
			detail = dispatchErr.Error()
		}
		if _, err := tx.ExecContext(ctx, "UPDATE pocket_actions SET status='uncertain',error=? WHERE decision_id=?", detail, a.DecisionID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReconcilePocketActions runs before generic lifecycle projection so policy-owned
// AO turns bind to the reserved execution instead of creating a second root task.
func (s *Store) ReconcilePocketActions(ctx context.Context) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, pocketActionSelect+" WHERE status<>'dispatched'")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var actions []domain.PocketAction
	defer func(r *sql.Rows) { _ = r.Close() }(rows)
	for rows.Next() {
		a, err := scanPocketAction(rows)
		if err != nil {
			_ = rows.Close()
			return err
		}
		actions = append(actions, a)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, a := range actions {
		if _, err := s.bindPocketActionTx(ctx, tx, a); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PocketPolicy returns the current derived state plus a bounded audit history.
func (s *Store) PocketPolicy(ctx context.Context, taskID string) (domain.PocketPolicyView, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return domain.PocketPolicyView{}, err
	}
	defer func() { _ = tx.Rollback() }()
	f, err := s.pocketPolicyFactsTx(ctx, tx, taskID, "")
	if err != nil {
		return domain.PocketPolicyView{}, err
	}
	v := domain.PocketPolicyView{Facts: f, Policy: pocket.Evaluate(f), Decisions: []domain.PocketDecision{}, Actions: []domain.PocketAction{}}
	decisionRows, err := tx.QueryContext(ctx, "SELECT rowid,id,facts_json,outcome_json,created_at FROM pocket_decisions WHERE task_id=? ORDER BY rowid DESC LIMIT 100", taskID)
	if err != nil {
		return v, err
	}
	defer func() { _ = decisionRows.Close() }()
	for decisionRows.Next() {
		d := domain.PocketDecision{TaskID: taskID}
		var facts, out string
		if err := decisionRows.Scan(&d.Sequence, &d.ID, &facts, &out, &d.CreatedAt); err != nil {
			_ = decisionRows.Close()
			return v, err
		}
		if err := json.Unmarshal([]byte(facts), &d.Facts); err != nil {
			_ = decisionRows.Close()
			return v, err
		}
		if err := json.Unmarshal([]byte(out), &d.Outcome); err != nil {
			_ = decisionRows.Close()
			return v, err
		}
		v.Decisions = append(v.Decisions, d)
	}
	err = decisionRows.Err()
	_ = decisionRows.Close()
	if err != nil {
		return v, err
	}
	actionRows, err := tx.QueryContext(ctx, pocketActionSelect+" WHERE task_id=? ORDER BY rowid DESC LIMIT 100", taskID)
	if err != nil {
		return v, err
	}
	defer func() { _ = actionRows.Close() }()
	for actionRows.Next() {
		a, err := scanPocketAction(actionRows)
		if err != nil {
			_ = actionRows.Close()
			return v, err
		}
		v.Actions = append(v.Actions, a)
	}
	err = actionRows.Err()
	_ = actionRows.Close()
	if err != nil {
		return v, err
	}
	for i := range v.Actions {
		events, err := pocketActionEventsTx(ctx, tx, v.Actions[i].DecisionID)
		if err != nil {
			return v, err
		}
		v.Actions[i].Events = events
	}
	return v, tx.Commit()
}

func pocketActionEventsTx(ctx context.Context, tx *sql.Tx, decisionID string) ([]domain.PocketActionEvent, error) {
	rows, err := tx.QueryContext(ctx, "SELECT sequence,status,detail,created_at FROM pocket_action_events WHERE decision_id=? ORDER BY sequence DESC LIMIT 100", decisionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	events := []domain.PocketActionEvent{}
	for rows.Next() {
		var e domain.PocketActionEvent
		if err := rows.Scan(&e.Sequence, &e.Status, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// RecoverPocketActions is called once after AO startup recovery. Dispatching
// reservations without an AO turn may be resent with the same idempotency key.
func (s *Store) RecoverPocketActions(ctx context.Context) error {
	if err := s.ReconcilePocketActions(ctx); err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.writeDB.ExecContext(ctx, "UPDATE pocket_actions SET status='prepared' WHERE status='dispatching'")
	return err
}

// PocketDecisionHistory pages the complete immutable audit trail by SQLite sequence.
func (s *Store) PocketDecisionHistory(ctx context.Context, taskID string, before int64) (domain.PocketDecisionPage, error) {
	if before < 0 {
		return domain.PocketDecisionPage{}, fmt.Errorf("%w: before must be nonnegative", domain.ErrPocketInvalid)
	}
	var exists int
	if err := s.readDB.QueryRowContext(ctx, "SELECT 1 FROM pocket_tasks WHERE id=?", taskID).Scan(&exists); err != nil {
		return domain.PocketDecisionPage{}, pocketLookupError(err, taskID)
	}
	page := domain.PocketDecisionPage{Decisions: []domain.PocketDecision{}}
	rows, err := s.readDB.QueryContext(ctx, "SELECT rowid,id,facts_json,outcome_json,created_at FROM pocket_decisions WHERE task_id=? AND (?=0 OR rowid<?) ORDER BY rowid DESC LIMIT 101", taskID, before, before)
	if err != nil {
		return page, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		d := domain.PocketDecision{TaskID: taskID}
		var facts, out string
		if err := rows.Scan(&d.Sequence, &d.ID, &facts, &out, &d.CreatedAt); err != nil {
			return page, err
		}
		if len(page.Decisions) == 100 {
			page.NextBefore = page.Decisions[99].Sequence
			break
		}
		if err := json.Unmarshal([]byte(facts), &d.Facts); err != nil {
			return page, err
		}
		if err := json.Unmarshal([]byte(out), &d.Outcome); err != nil {
			return page, err
		}
		page.Decisions = append(page.Decisions, d)
	}
	return page, rows.Err()
}
