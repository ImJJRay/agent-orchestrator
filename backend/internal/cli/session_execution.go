package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const pocketExecutionSchemaVersion = "pocket.execution.v1"

type estimatedCostDTO struct {
	TotalNanos          int64  `json:"totalNanos"`
	InputNanos          *int64 `json:"inputNanos"`
	CachedInputNanos    *int64 `json:"cachedInputNanos"`
	OutputNanos         *int64 `json:"outputNanos"`
	Coverage            string `json:"coverage"`
	ProviderAttribution string `json:"providerAttribution"`
}

type usageTotalsDTO struct {
	InputTokens         *int64            `json:"inputTokens"`
	CachedInputTokens   *int64            `json:"cachedInputTokens"`
	UncachedInputTokens *int64            `json:"uncachedInputTokens"`
	OutputTokens        *int64            `json:"outputTokens"`
	ProcessedTokens     *int64            `json:"processedTokens"`
	EstimatedCost       *estimatedCostDTO `json:"estimatedCost"`
}

type usageModelDTO struct {
	ModelID string         `json:"modelId"`
	Totals  usageTotalsDTO `json:"totals"`
}

type usageHarnessDTO struct {
	Harness string          `json:"harness"`
	Totals  usageTotalsDTO  `json:"totals"`
	Models  []usageModelDTO `json:"models"`
}

type sessionUsageDTO struct {
	SessionID  string            `json:"sessionId"`
	Incomplete bool              `json:"incomplete"`
	Totals     usageTotalsDTO    `json:"totals"`
	Harnesses  []usageHarnessDTO `json:"harnesses"`
}

type executionProfileOutput struct {
	Harness       string  `json:"harness,omitempty"`
	InterfaceMode string  `json:"interfaceMode,omitempty"`
	Model         *string `json:"model"`
	Effort        *string `json:"effort"`
	Provider      *string `json:"provider"`
}

type executionChangedFileOutput struct {
	Path      string `json:"path"`
	OldPath   string `json:"oldPath,omitempty"`
	Status    string `json:"status,omitempty"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

type executionTurnOutput struct {
	ID                    string                       `json:"id"`
	State                 string                       `json:"state"`
	RequestedAt           string                       `json:"requestedAt,omitempty"`
	StartedAt             *string                      `json:"startedAt"`
	CompletedAt           *string                      `json:"completedAt"`
	DurationMillis        *int64                       `json:"durationMillis"`
	RetryOfExecutionID    string                       `json:"retryOfExecutionId,omitempty"`
	HasRetryAttempt       bool                         `json:"hasRetryAttempt"`
	ChangedFilesKnown     bool                         `json:"changedFilesKnown"`
	ChangedFilesTruncated bool                         `json:"changedFilesTruncated"`
	ChangedFiles          []executionChangedFileOutput `json:"changedFiles,omitempty"`
}

type executionUsageOutput struct {
	Scope           string            `json:"scope"`
	ExecutionScoped bool              `json:"executionScoped"`
	Incomplete      bool              `json:"incomplete"`
	CacheHitRatio   *float64          `json:"cacheHitRatio"`
	Totals          usageTotalsDTO    `json:"totals"`
	Harnesses       []usageHarnessDTO `json:"harnesses,omitempty"`
}

type completionGateOutput struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Reason string `json:"reason"`
}

type completionPolicyOutput struct {
	State                      string                 `json:"state"`
	SemanticReviewEligible     bool                   `json:"semanticReviewEligible"`
	AutomaticAcceptanceAllowed bool                   `json:"automaticAcceptanceAllowed"`
	MergeAuthorized            bool                   `json:"mergeAuthorized"`
	Gates                      []completionGateOutput `json:"gates"`
}

type executionValidationOutput struct {
	State    domain.PocketValidationState      `json:"state"`
	Evidence []domain.PocketValidationEvidence `json:"evidence"`
}

type sessionExecutionOutput struct {
	SchemaVersion    string                     `json:"schemaVersion"`
	SessionID        string                     `json:"sessionId"`
	ProjectID        string                     `json:"projectId,omitempty"`
	Task             *domain.PocketTask         `json:"task,omitempty"`
	Worker           *domain.PocketWorker       `json:"worker,omitempty"`
	DurableExecution *domain.PocketExecution    `json:"durableExecution,omitempty"`
	Validation       *executionValidationOutput `json:"validation,omitempty"`
	Execution        *executionTurnOutput       `json:"execution"`
	Profile          executionProfileOutput     `json:"profile"`
	Result           sessionResultOutput        `json:"result"`
	Usage            executionUsageOutput       `json:"usage"`
	Policy           completionPolicyOutput     `json:"policy"`
	Unknown          []string                   `json:"unknown"`
}

func newSessionExecutionCommand(ctx *commandContext) *cobra.Command {
	var opts sessionOptions
	cmd := &cobra.Command{
		Use:   "execution <id>",
		Short: "Show normalized execution evidence and deterministic completion gates",
		Long: "Show the latest durable Chat execution result, timing, changed-file evidence, session-scoped usage, " +
			"and deterministic completion pre-gates. Usage is never relabeled as per-turn data when AO only knows it at session scope. " +
			"This command does not authorize task acceptance or a Git merge.",
		Example: "  ao session execution mer-3 --json",
		Args:    oneSessionIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := normalizeSessionID(args[0])
			if err != nil {
				return err
			}
			return ctx.sessionExecution(cmd.Context(), cmd, id, opts)
		},
	}
	f := cmd.Flags()
	addSessionProjectFlag(f, &opts.project, "Project id to scope the lookup")
	f.BoolVar(&opts.json, "json", false, "Output as JSON")
	return cmd
}

func (c *commandContext) sessionExecution(ctx context.Context, cmd *cobra.Command, id string, opts sessionOptions) error {
	sess, err := c.fetchScopedSession(ctx, id, opts.project)
	if err != nil {
		return err
	}
	if sess.Mode != "chat" {
		return fmt.Errorf("session %s uses %s mode; normalized execution evidence currently requires durable Chat semantics", id, sess.Mode)
	}

	snapshot, err := c.fetchConversationSnapshot(ctx, id)
	if err != nil {
		return err
	}
	var usage sessionUsageDTO
	if err := c.getJSON(ctx, "usage/sessions/"+url.PathEscape(id), &usage); err != nil {
		return err
	}

	out := buildSessionExecutionOutput(sess, snapshot, usage)
	turnID := ""
	if out.Execution != nil {
		turnID = out.Execution.ID
	}
	durable, found, err := c.fetchPocketExecutionState(ctx, id, turnID)
	if err != nil {
		return err
	}
	if found {
		applyDurablePocketState(&out, durable)
	}
	if opts.json {
		return writeJSON(cmd.OutOrStdout(), out)
	}
	return writeSessionExecutionText(cmd, out)
}

func (c *commandContext) fetchPocketExecutionState(ctx context.Context, sessionID, turnID string) (domain.PocketExecutionSnapshot, bool, error) {
	path := "/internal/pocket/sessions/" + url.PathEscape(sessionID) + "/execution"
	if turnID != "" {
		path += "?turnId=" + url.QueryEscape(turnID)
	}
	var snapshot domain.PocketExecutionSnapshot
	if err := c.doJSONPath(ctx, http.MethodGet, path, nil, &snapshot); err != nil {
		var responseErr apiResponseError
		if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusNotFound {
			return domain.PocketExecutionSnapshot{}, false, nil
		}
		return domain.PocketExecutionSnapshot{}, false, err
	}
	return snapshot, true, nil
}

func applyDurablePocketState(out *sessionExecutionOutput, snapshot domain.PocketExecutionSnapshot) {
	task := snapshot.Task
	worker := snapshot.Worker
	execution := snapshot.Execution
	out.Task = &task
	out.Worker = &worker
	out.DurableExecution = &execution
	out.Validation = &executionValidationOutput{
		State:    snapshot.ValidationState,
		Evidence: snapshot.Validation,
	}
	out.Unknown = removeUnknowns(out.Unknown, "taskId", "workerId", "executionRetryCount", "validationResults")

	state := deterministicValidationState(snapshot.Validation)
	for i := range out.Policy.Gates {
		if out.Policy.Gates[i].Name != "deterministic_validation" {
			continue
		}
		out.Policy.Gates[i].State = string(state)
		switch state {
		case domain.PocketValidationPass:
			out.Policy.Gates[i].Reason = "all required deterministic validation checks have authoritative passing evidence"
		case domain.PocketValidationFail:
			out.Policy.Gates[i].Reason = "at least one required deterministic validation check has authoritative failing evidence"
		default:
			out.Policy.Gates[i].Reason = "required deterministic validation evidence is missing or unknown"
		}
		break
	}
	if state == domain.PocketValidationFail {
		out.Policy.State = "blocked"
	}
	// This increment supplies evidence only. Durable validation never authorizes
	// acceptance or Git operations on its own.
	out.Policy.AutomaticAcceptanceAllowed = false
	out.Policy.MergeAuthorized = false
}

func deterministicValidationState(evidence []domain.PocketValidationEvidence) domain.PocketValidationState {
	seen := false
	unknown := false
	for _, item := range evidence {
		if !item.Requirement.Required || !item.Requirement.Deterministic {
			continue
		}
		seen = true
		switch item.EffectiveState {
		case domain.PocketValidationFail:
			return domain.PocketValidationFail
		case domain.PocketValidationUnknown:
			unknown = true
		}
	}
	if !seen || unknown {
		return domain.PocketValidationUnknown
	}
	return domain.PocketValidationPass
}

func removeUnknowns(values []string, remove ...string) []string {
	set := make(map[string]struct{}, len(remove))
	for _, value := range remove {
		set[value] = struct{}{}
	}
	out := values[:0]
	for _, value := range values {
		if _, ok := set[value]; !ok {
			out = append(out, value)
		}
	}
	return out
}

func buildSessionExecutionOutput(sess sessionDTO, snapshot conversationSnapshotDTO, usage sessionUsageDTO) sessionExecutionOutput {
	result := deriveSessionResult(sess.ID, snapshot)
	turn, hasTurn := lastActiveTurn(snapshot.Turns)

	profile := executionProfileOutput{
		Harness:       sess.Harness,
		InterfaceMode: sess.Mode,
	}
	if sess.Model != "" {
		model := sess.Model
		profile.Model = &model
	}

	var execution *executionTurnOutput
	if hasTurn {
		execution = executionTurn(turn)
	}

	cacheRatio := cacheHitRatio(usage.Totals)
	policy := assessCompletionPolicy(result, snapshot)
	unknown := executionUnknowns(profile, usage, execution)

	return sessionExecutionOutput{
		SchemaVersion: pocketExecutionSchemaVersion,
		SessionID:     sess.ID,
		ProjectID:     sess.ProjectID,
		Execution:     execution,
		Profile:       profile,
		Result:        result,
		Usage: executionUsageOutput{
			Scope:           "session",
			ExecutionScoped: false,
			Incomplete:      usage.Incomplete,
			CacheHitRatio:   cacheRatio,
			Totals:          usage.Totals,
			Harnesses:       usage.Harnesses,
		},
		Policy:  policy,
		Unknown: unknown,
	}
}

func executionTurn(turn conversationTurnDTO) *executionTurnOutput {
	out := &executionTurnOutput{
		ID:                 turn.ID,
		State:              turn.State,
		RequestedAt:        turn.RequestedAt,
		StartedAt:          turn.StartedAt,
		CompletedAt:        turn.CompletedAt,
		RetryOfExecutionID: turn.RetryOfTurnID,
		HasRetryAttempt:    turn.HasRetryAttempt,
	}
	if turn.StartedAt != nil && turn.CompletedAt != nil {
		started, startErr := time.Parse(time.RFC3339Nano, *turn.StartedAt)
		completed, completeErr := time.Parse(time.RFC3339Nano, *turn.CompletedAt)
		if startErr == nil && completeErr == nil && !completed.Before(started) {
			duration := completed.Sub(started).Milliseconds()
			out.DurationMillis = &duration
		}
	}
	if turn.Diff != nil {
		out.ChangedFilesKnown = true
		out.ChangedFilesTruncated = turn.Diff.Truncated
		out.ChangedFiles = make([]executionChangedFileOutput, 0, len(turn.Diff.Files))
		for _, file := range turn.Diff.Files {
			out.ChangedFiles = append(out.ChangedFiles, executionChangedFileOutput(file))
		}
	}
	return out
}

func cacheHitRatio(totals usageTotalsDTO) *float64 {
	if totals.InputTokens == nil || totals.CachedInputTokens == nil || *totals.InputTokens <= 0 {
		return nil
	}
	ratio := float64(*totals.CachedInputTokens) / float64(*totals.InputTokens)
	return &ratio
}

func assessCompletionPolicy(result sessionResultOutput, snapshot conversationSnapshotDTO) completionPolicyOutput {
	gates := []completionGateOutput{}
	if result.Status == string(sessionResultStatusCompleted) {
		gates = append(gates, completionGateOutput{
			Name: "semantic_result", State: "pass",
			Reason: "the latest active Chat turn has a completed durable assistant result",
		})
	} else {
		gates = append(gates, completionGateOutput{
			Name: "semantic_result", State: "fail",
			Reason: "the latest active Chat turn does not have a completed durable result",
		})
	}

	pending := pendingInteractionCount(snapshot.Activities)
	if pending == 0 {
		gates = append(gates, completionGateOutput{
			Name: "blocking_interaction", State: "pass",
			Reason: "no pending approval or structured user-input request is present on the active branch",
		})
	} else {
		gates = append(gates, completionGateOutput{
			Name: "blocking_interaction", State: "fail",
			Reason: fmt.Sprintf("%d pending approval or structured user-input request(s) remain", pending),
		})
	}

	gates = append(gates,
		completionGateOutput{
			Name: "deterministic_validation", State: "unknown",
			Reason: "no task-scoped required-validation result is associated with this execution yet",
		},
		completionGateOutput{
			Name: "dependencies", State: "unknown",
			Reason: "no durable task dependency graph is associated with this execution yet",
		},
		completionGateOutput{
			Name: "required_human_approval", State: "unknown",
			Reason: "the Pocket policy layer does not yet have a durable final-approval requirement for this execution",
		},
	)

	state := "awaiting_evidence"
	for _, gate := range gates {
		if gate.State == "fail" {
			state = "blocked"
			break
		}
	}
	return completionPolicyOutput{
		State:                      state,
		SemanticReviewEligible:     false,
		AutomaticAcceptanceAllowed: false,
		MergeAuthorized:            false,
		Gates:                      gates,
	}
}

func pendingInteractionCount(activities []conversationActivityDTO) int {
	count := 0
	for _, activity := range activities {
		if activity.Status != "pending" {
			continue
		}
		if activity.ActivityKind == "approval" || activity.ActivityKind == "user_input" {
			count++
		}
	}
	return count
}

func executionUnknowns(profile executionProfileOutput, usage sessionUsageDTO, execution *executionTurnOutput) []string {
	unknown := []string{
		"taskId",
		"workerId",
		"executionUsage",
		"executionCost",
		"executionRetryCount",
		"executionModelAttribution",
		"profile.effort",
		"profile.provider",
		"validationResults",
		"dependencyState",
		"requiredHumanApproval",
	}
	if profile.Model == nil {
		unknown = append(unknown, "profile.model")
	}
	if usage.Totals.InputTokens == nil {
		unknown = append(unknown, "usage.inputTokens")
	}
	if usage.Totals.CachedInputTokens == nil {
		unknown = append(unknown, "usage.cachedInputTokens")
	}
	if usage.Totals.UncachedInputTokens == nil {
		unknown = append(unknown, "usage.uncachedInputTokens")
	}
	if usage.Totals.OutputTokens == nil {
		unknown = append(unknown, "usage.outputTokens")
	}
	if usage.Totals.EstimatedCost == nil {
		unknown = append(unknown, "usage.estimatedCost")
	}
	if execution == nil {
		unknown = append(unknown, "execution.timing", "execution.changedFiles")
	} else {
		if execution.DurationMillis == nil {
			unknown = append(unknown, "execution.durationMillis")
		}
		if !execution.ChangedFilesKnown {
			unknown = append(unknown, "execution.changedFiles")
		}
	}
	return unknown
}

func writeSessionExecutionText(cmd *cobra.Command, out sessionExecutionOutput) error {
	w := cmd.OutOrStdout()
	if _, err := fmt.Fprintf(w, "session: %s\n", out.SessionID); err != nil {
		return err
	}
	if out.DurableExecution != nil {
		if _, err := fmt.Fprintf(w, "task: %s; worker: %s; attempt: %s (#%d)\n",
			out.DurableExecution.TaskID, out.DurableExecution.WorkerID, out.DurableExecution.ID, out.DurableExecution.AttemptNumber); err != nil {
			return err
		}
	}
	if out.Execution != nil {
		if _, err := fmt.Fprintf(w, "execution: %s (%s)\n", out.Execution.ID, out.Execution.State); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintln(w, "execution: none"); err != nil {
		return err
	}
	if out.Validation != nil {
		if _, err := fmt.Fprintf(w, "validation: %s\n", out.Validation.State); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "result: %s\n", out.Result.Status); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "usage: %s scoped; execution scoped: %t\n", out.Usage.Scope, out.Usage.ExecutionScoped); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "policy: %s; automatic acceptance: %t; merge authorized: %t\n",
		out.Policy.State, out.Policy.AutomaticAcceptanceAllowed, out.Policy.MergeAuthorized); err != nil {
		return err
	}
	return nil
}
