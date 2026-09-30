// Package pocket projects AO lifecycle facts into Pocket execution telemetry and
// runs deterministic task validation without owning AO session execution.
package pocket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	defaultReconcileInterval = time.Second
	defaultValidationTimeout = 10 * time.Minute
	maxValidationOutputBytes = 64 << 10
)

// Store is the durable Pocket surface required by the lifecycle projector and
// deterministic validator. AO remains the source of truth for session/turn/worktree
// state; the store methods only project and annotate those facts.
type Store interface {
	ReconcilePocketExecutionLifecycle(context.Context, time.Time) error
	PendingPocketDeterministicValidations(context.Context, int) ([]domain.PocketValidationWorkItem, error)
	CreatePocketValidationResult(
		context.Context,
		string,
		string,
		domain.PocketValidationState,
		domain.PocketValidationSourceKind,
		string,
		string,
		time.Time,
		time.Time,
	) (domain.PocketValidationResult, error)
}

// Options configures the Pocket coordinator.
type Options struct {
	Executor           Executor
	Store              Store
	Logger             *slog.Logger
	Clock              func() time.Time
	ReconcileInterval  time.Duration
	ValidationInterval time.Duration
	ValidationTimeout  time.Duration
}

// Coordinator keeps Pocket projection eventually consistent with AO durable
// facts, executes deterministic checks and actuates authorized Chat work. It
// never accepts tasks or authorizes Git operations.
type Coordinator struct {
	executor           Executor
	actionMu           sync.Mutex
	recoveryMu         sync.Mutex
	recovered          bool
	store              Store
	log                *slog.Logger
	now                func() time.Time
	reconcileInterval  time.Duration
	validationInterval time.Duration
	validationTimeout  time.Duration
}

// New constructs a Pocket lifecycle coordinator.
func New(opts Options) *Coordinator {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	now := opts.Clock
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	reconcileInterval := opts.ReconcileInterval
	if reconcileInterval <= 0 {
		reconcileInterval = defaultReconcileInterval
	}
	validationInterval := opts.ValidationInterval
	if validationInterval <= 0 {
		validationInterval = defaultReconcileInterval
	}
	validationTimeout := opts.ValidationTimeout
	if validationTimeout <= 0 {
		validationTimeout = defaultValidationTimeout
	}
	return &Coordinator{
		store:              opts.Store,
		executor:           opts.Executor,
		log:                log,
		now:                now,
		reconcileInterval:  reconcileInterval,
		validationInterval: validationInterval,
		validationTimeout:  validationTimeout,
	}
}

// Reconcile projects current AO durable state into Pocket. It is explicitly
// callable after AO's own restart reconciliation so stale running attempts are
// corrected immediately rather than waiting for the safety-net ticker.
func (c *Coordinator) Reconcile(ctx context.Context) error {
	if c == nil || c.store == nil {
		return nil
	}
	if orchestration, ok := c.store.(OrchestrationStore); ok {
		c.recoveryMu.Lock()
		if !c.recovered {
			if err := orchestration.RecoverPocketActions(ctx); err != nil {
				c.recoveryMu.Unlock()
				return err
			}
			c.recovered = true
		}
		c.recoveryMu.Unlock()
		if err := orchestration.ReconcilePocketActions(ctx); err != nil {
			return err
		}
	}
	if err := c.store.ReconcilePocketExecutionLifecycle(ctx, c.now().UTC()); err != nil {
		return err
	}
	if orchestration, ok := c.store.(OrchestrationStore); ok {
		return orchestration.ReconcilePocketPolicy(ctx, c.now().UTC())
	}
	return nil
}

// Start launches independent state and validation loops. Validation command
// latency therefore cannot delay execution-state reconciliation.
func (c *Coordinator) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	if c == nil || c.store == nil {
		close(done)
		return done
	}

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		c.reconcileLoop(ctx)
	}()
	go func() {
		defer wg.Done()
		c.validationLoop(ctx)
	}()
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(c.reconcileInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := c.RunPendingAction(ctx); err != nil && ctx.Err() == nil {
					c.log.Warn("Pocket action failed", "err", err)
				}
			}
		}
	}()
	go func() {
		wg.Wait()
		close(done)
	}()
	return done
}

func (c *Coordinator) reconcileLoop(ctx context.Context) {
	ticker := time.NewTicker(c.reconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.Reconcile(ctx); err != nil && ctx.Err() == nil {
				c.log.Warn("Pocket execution lifecycle reconciliation failed", "err", err)
			}
		}
	}
}

func (c *Coordinator) validationLoop(ctx context.Context) {
	ticker := time.NewTicker(c.validationInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.RunPendingValidation(ctx); err != nil && ctx.Err() == nil {
				c.log.Warn("Pocket deterministic validation failed to run", "err", err)
			}
		}
	}
}

type validationDetail struct {
	Command     string `json:"command,omitempty"`
	Worktree    string `json:"worktree,omitempty"`
	ExitCode    *int   `json:"exitCode,omitempty"`
	Output      string `json:"output,omitempty"`
	Unavailable string `json:"unavailable,omitempty"`
	DurationMS  int64  `json:"durationMs"`
}

func boundedValidationOutput(output []byte) string {
	if len(output) <= maxValidationOutputBytes {
		return strings.TrimSpace(string(output))
	}
	suffix := "\n...[truncated by Pocket validation runner]"
	keep := maxValidationOutputBytes - len(suffix)
	if keep < 0 {
		keep = 0
	}
	return strings.TrimSpace(string(output[:keep])) + suffix
}

func validationUnavailable(err error, output string, exitCode int) bool {
	if err == nil {
		return false
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return true
	}
	if runtime.GOOS != "windows" && exitCode == 127 {
		return true
	}
	if runtime.GOOS == "windows" {
		lower := strings.ToLower(output)
		if exitCode == 9009 || strings.Contains(lower, "is not recognized as an internal or external command") {
			return true
		}
	}
	return false
}

func (c *Coordinator) executeValidation(
	ctx context.Context,
	item domain.PocketValidationWorkItem,
) (domain.PocketValidationState, string, error) {
	command := strings.TrimSpace(item.Requirement.Command)
	detail := validationDetail{Command: command, Worktree: item.Execution.WorkspacePath}
	started := c.now()
	if command == "" {
		detail.Unavailable = "no deterministic command configured"
		detail.DurationMS = c.now().Sub(started).Milliseconds()
		encoded, _ := json.Marshal(detail)
		return domain.PocketValidationUnknown, string(encoded), nil
	}
	worktree := strings.TrimSpace(item.Execution.WorkspacePath)
	if worktree == "" {
		detail.Unavailable = "AO worktree path is not available"
		detail.DurationMS = c.now().Sub(started).Milliseconds()
		encoded, _ := json.Marshal(detail)
		return domain.PocketValidationUnknown, string(encoded), nil
	}
	info, err := os.Stat(worktree)
	if err != nil || !info.IsDir() {
		if err != nil {
			detail.Unavailable = fmt.Sprintf("AO worktree is unavailable: %v", err)
		} else {
			detail.Unavailable = "AO worktree path is not a directory"
		}
		detail.DurationMS = c.now().Sub(started).Milliseconds()
		encoded, _ := json.Marshal(detail)
		return domain.PocketValidationUnknown, string(encoded), nil
	}

	runCtx, cancel := context.WithTimeout(ctx, c.validationTimeout)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = aoprocess.CommandContext(runCtx, "cmd", "/c", command)
	} else {
		cmd = aoprocess.CommandContext(runCtx, "sh", "-c", command)
	}
	cmd.Dir = worktree
	output, runErr := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return domain.PocketValidationUnknown, "", ctx.Err()
	}

	outputText := boundedValidationOutput(output)
	detail.Output = outputText
	detail.DurationMS = c.now().Sub(started).Milliseconds()
	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
		detail.ExitCode = &exitCode
	}
	switch {
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		detail.Unavailable = "validation command timed out"
		encoded, _ := json.Marshal(detail)
		return domain.PocketValidationUnknown, string(encoded), nil
	case validationUnavailable(runErr, outputText, exitCode):
		detail.Unavailable = "validation command is unavailable"
		encoded, _ := json.Marshal(detail)
		return domain.PocketValidationUnknown, string(encoded), nil
	case runErr == nil:
		encoded, _ := json.Marshal(detail)
		return domain.PocketValidationPass, string(encoded), nil
	default:
		encoded, _ := json.Marshal(detail)
		return domain.PocketValidationFail, string(encoded), nil
	}
}

// RunPendingValidation executes at most one deterministic check. The store only
// returns checks with no prior deterministic observation, making the operation
// retry-safe across daemon restarts: a command interrupted by process death has
// no fabricated result and is eligible again after recovery.
func (c *Coordinator) RunPendingValidation(ctx context.Context) error {
	if c == nil || c.store == nil {
		return nil
	}
	items, err := c.store.PendingPocketDeterministicValidations(ctx, 1)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}
	item := items[0]
	state, detail, err := c.executeValidation(ctx, item)
	if err != nil {
		return err
	}
	now := c.now().UTC()
	_, err = c.store.CreatePocketValidationResult(
		ctx,
		item.Execution.ID,
		item.Requirement.ID,
		state,
		domain.PocketValidationDeterministic,
		"pocket.validation.command",
		detail,
		now,
		now,
	)
	return err
}

// Executor is AO's existing durable Chat control plane, not a Pocket worker runtime.
type Executor interface {
	SendPolicy(context.Context, domain.SessionID, ports.ChatUserMessage) (domain.ConversationTurn, error)
	RetryPolicy(context.Context, domain.SessionID, string) (domain.ConversationTurn, error)
}

// OrchestrationStore persists policy decisions and outbox reservations in AO SQLite.
type OrchestrationStore interface {
	RecoverPocketActions(context.Context) error
	ReconcilePocketActions(context.Context) error
	ReconcilePocketPolicy(context.Context, time.Time) error
	ClaimPocketAction(context.Context, time.Time) (domain.PocketAction, bool, error)
	SettlePocketAction(context.Context, domain.PocketAction, error) error
}

// RunPendingAction executes one reserved decision. Stable message identity and
// AO's native retry uniqueness protect both uncertain round trips and restarts.
func (c *Coordinator) RunPendingAction(ctx context.Context) error {
	if c == nil || c.executor == nil {
		return nil
	}
	s, ok := c.store.(OrchestrationStore)
	if !ok {
		return nil
	}
	c.actionMu.Lock()
	defer c.actionMu.Unlock()
	c.recoveryMu.Lock()
	if !c.recovered {
		if err := s.RecoverPocketActions(ctx); err != nil {
			c.recoveryMu.Unlock()
			return err
		}
		c.recovered = true
	}
	c.recoveryMu.Unlock()
	a, found, err := s.ClaimPocketAction(ctx, c.now().UTC())
	if err != nil || !found {
		return err
	}
	if a.NativeRetry {
		_, err = c.executor.RetryPolicy(ctx, a.SessionID, a.PriorTurnID)
	} else {
		_, err = c.executor.SendPolicy(ctx, a.SessionID, ports.ChatUserMessage{Text: a.Prompt, Origin: domain.MessageOriginAutomation, ClientMessageID: "pocket:" + a.DecisionID})
	}
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return errors.Join(err, s.SettlePocketAction(settleCtx, a, err))
}
