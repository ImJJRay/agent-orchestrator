package pocket

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type validationRecordingStore struct {
	items   []domain.PocketValidationWorkItem
	results []domain.PocketValidationResult
}

func (s *validationRecordingStore) ReconcilePocketExecutionLifecycle(context.Context, time.Time) error {
	return nil
}

func (s *validationRecordingStore) PendingPocketDeterministicValidations(
	context.Context,
	int,
) ([]domain.PocketValidationWorkItem, error) {
	if len(s.results) > 0 || len(s.items) == 0 {
		return nil, nil
	}
	return s.items[:1], nil
}

func (s *validationRecordingStore) CreatePocketValidationResult(
	_ context.Context,
	executionID string,
	requirementID string,
	state domain.PocketValidationState,
	sourceKind domain.PocketValidationSourceKind,
	source string,
	detail string,
	observedAt time.Time,
	createdAt time.Time,
) (domain.PocketValidationResult, error) {
	result := domain.PocketValidationResult{
		ID:            "result-1",
		RequirementID: requirementID,
		TaskID:        "task-1",
		ExecutionID:   executionID,
		State:         state,
		SourceKind:    sourceKind,
		Source:        source,
		Detail:        detail,
		ObservedAt:    observedAt,
		CreatedAt:     createdAt,
	}
	s.results = append(s.results, result)
	return result, nil
}

func validationItem(worktree, command string) domain.PocketValidationWorkItem {
	return domain.PocketValidationWorkItem{
		Execution: domain.PocketExecution{
			ID:            "execution-1",
			TaskID:        "task-1",
			SessionID:     "session-1",
			WorkspacePath: worktree,
			State:         domain.PocketExecutionCompleted,
		},
		Requirement: domain.PocketValidationRequirement{
			ID:            "requirement-1",
			TaskID:        "task-1",
			Scope:         domain.PocketValidationTaskScope,
			CheckID:       "focused-check",
			Command:       command,
			Deterministic: true,
			Required:      true,
		},
	}
}

func platformValidationCommands() (pass, fail, unavailable string) {
	if runtime.GOOS == "windows" {
		return "echo ok>validation-marker.txt", "exit /b 7", "pocket-command-that-does-not-exist-9f55"
	}
	return "printf ok > validation-marker.txt", "exit 7", "pocket-command-that-does-not-exist-9f55"
}

func TestDeterministicValidationRunsInOwningWorktreeAndPersistsPass(t *testing.T) {
	worktree := t.TempDir()
	pass, _, _ := platformValidationCommands()
	store := &validationRecordingStore{items: []domain.PocketValidationWorkItem{
		validationItem(worktree, pass),
	}}
	coordinator := New(Options{Store: store, ValidationTimeout: time.Second})

	if err := coordinator.RunPendingValidation(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.results) != 1 || store.results[0].State != domain.PocketValidationPass {
		t.Fatalf("validation results=%#v", store.results)
	}
	if store.results[0].SourceKind != domain.PocketValidationDeterministic ||
		store.results[0].Source != "pocket.validation.command" {
		t.Fatalf("validation provenance=%#v", store.results[0])
	}
	data, err := os.ReadFile(filepath.Join(worktree, "validation-marker.txt"))
	if err != nil {
		t.Fatalf("validation did not execute in owning worktree: %v", err)
	}
	if string(data) != "ok" && string(data) != "ok\r\n" {
		t.Fatalf("marker=%q", string(data))
	}
}

func TestDeterministicValidationPersistsCommandFailure(t *testing.T) {
	worktree := t.TempDir()
	_, fail, _ := platformValidationCommands()
	store := &validationRecordingStore{items: []domain.PocketValidationWorkItem{
		validationItem(worktree, fail),
	}}
	coordinator := New(Options{Store: store, ValidationTimeout: time.Second})

	if err := coordinator.RunPendingValidation(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.results) != 1 || store.results[0].State != domain.PocketValidationFail {
		t.Fatalf("validation results=%#v", store.results)
	}
}

func TestDeterministicValidationUnavailableRemainsUnknown(t *testing.T) {
	worktree := t.TempDir()
	_, _, unavailable := platformValidationCommands()
	store := &validationRecordingStore{items: []domain.PocketValidationWorkItem{
		validationItem(worktree, unavailable),
	}}
	coordinator := New(Options{Store: store, ValidationTimeout: time.Second})

	if err := coordinator.RunPendingValidation(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.results) != 1 || store.results[0].State != domain.PocketValidationUnknown {
		t.Fatalf("unavailable validation results=%#v", store.results)
	}
}

func TestDeterministicValidationMissingWorktreeRemainsUnknown(t *testing.T) {
	pass, _, _ := platformValidationCommands()
	store := &validationRecordingStore{items: []domain.PocketValidationWorkItem{
		validationItem("", pass),
	}}
	coordinator := New(Options{Store: store, ValidationTimeout: time.Second})

	if err := coordinator.RunPendingValidation(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.results) != 1 || store.results[0].State != domain.PocketValidationUnknown {
		t.Fatalf("missing worktree validation results=%#v", store.results)
	}
}
