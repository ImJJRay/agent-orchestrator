# Pocket Agent Orchestrator — Project Status

Updated: 2026-09-30

This file records Pocket Agent project state only. Upstream product status remains in `docs/STATUS.md`.

## Baseline

- Upstream mirror `main`: `b188dd3bf03c331e01cb1c43f93163811cd33a15`.
- Legacy custom branch preserved as `quant-main-pre-upstream-2026-09-29` at `a8bbbc06f33b51f0db9d8eb135bda84eccfd1188`.
- `quant-main` currently matches upstream `main`.
- Phase 0 final integration branch: `feature/phase0-baseline-final`.

## Phase 0 custom delta

- `ao session conversation <id>`
  - Reads AO's persisted Chat conversation through the existing daemon API.
  - Walks the current paginated active-branch snapshot rather than truncating at one page.
  - Fails closed if conversation/branch identity changes while paging.
- `ao session result <id>`
  - Reads persisted AO conversation state only; no LLM summarization or transcript scraping.
  - Returns the exact final assistant output from the most recent relevant non-rolled-back completed/recovered turn.
  - Stable JSON statuses: `completed`, `running`, `failed`, `malformed`.
  - Non-completed states return a non-success exit status.
- Session CLI output exposes the execution profile needed by a future orchestrator: `harness`, `mode`, and resolved persisted `model`.
- Restore pins the persisted session model for every harness so later project-default changes do not change a restored worker's model. Existing harness-specific effort semantics are unchanged.

## September code deliberately not replayed

Current upstream already owns OpenCode launch/restore `--model` handling, model catalogs, persisted session metadata, Chat/ACP infrastructure, OpenCode configuration precedence, Windows/runtime environment behavior, and restore/session APIs. The old OpenCode/model commit was therefore not cherry-picked.

The old result-retrieval commit was reimplemented against the current paginated, branch-aware conversation architecture rather than replayed.

## Validation

Focused regression coverage exists for:

- OpenCode per-session model continuity across TUI and Chat restore after project defaults change and SQLite reopen.
- Conversation pagination and branch-consistency handling.
- Deterministic result selection, rollback handling, running/failed/malformed states, and exit behavior.
- CLI harness/mode/model visibility.

The repository-standard CI gates on the final integration branch are the completion gate for Phase 0.

## Deferred

Android/Termux ARM64, SQLite/Git/worktree validation on the AYN Thor Max, OpenCode runtime validation on-device, Connect Mobile/Tailscale pairing, JEV, DSH, Layla, Shizuku, and broader routing policy are outside Phase 0.
