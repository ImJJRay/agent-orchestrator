# Pocket Agent Orchestrator — Project Status

Updated: 2026-09-29

This file records project-specific state only. Upstream product status remains in `docs/STATUS.md`.

## Baseline

- Upstream-mirror `main`: `39305c3f30b730ba26664134080140a853b24ad7`.
- Legacy custom branch preserved as `quant-main-pre-upstream-2026-09-29` at `a8bbbc06f33b51f0db9d8eb135bda84eccfd1188`.
- `quant-main` reconstructed from current `main`.
- Phase 0 implementation branch: `feature/phase0-baseline-reconstruction`.

## Implemented

- Deterministic `ao session conversation <id>` over AO's existing persisted Chat conversation API.
  - Follows current paginated snapshots to reconstruct the complete active-branch conversation.
  - Fails closed if branch/conversation identity changes while paging.
- Deterministic `ao session result <id>`.
  - Uses persisted AO conversation state only.
  - Returns the exact final assistant message from the most recent relevant non-rolled-back completed/recovered turn.
  - Stable JSON statuses: `completed`, `running`, `failed`, `malformed`.
  - Non-completed states exit non-zero.
- Session CLI JSON/details expose `harness`, `mode`, and resolved persisted `model`.
- Restore uses persisted session model/effort rather than later project defaults for every harness. This closes the OpenCode Chat restore continuity gap while keeping the rule at AO's generic session layer.

## Upstream functionality reused / legacy changes discarded

Current upstream already owns OpenCode model flags for launch and restore, model catalogs, persisted session metadata, Chat/ACP infrastructure, OpenCode configuration precedence, runtime environment handling, and Windows process launch behavior. The September OpenCode patch is therefore not replayed.

The September conversation/result behavior is reimplemented against the current paginated, branch-aware conversation API rather than cherry-picked.

## Tested

Focused deterministic tests are included for:

- OpenCode model/effort persistence across both Chat and TUI restore after project defaults change and SQLite reopen.
- Conversation pagination and consistency checks.
- Result selection, rollback handling, running/failed/malformed classification, and non-zero exit behavior.
- Session CLI harness/mode/model visibility.

Repository-standard CI validation is run through draft PR #3. This section should be updated with the final workflow results before Phase 0 is declared complete.

## Planned

After Phase 0 is green and integrated into `quant-main`:

1. Native Android/Termux ARM64 backend build.
2. SQLite/Git/worktree validation on the AYN Thor Max.
3. OpenCode Chat worker execution and restart/recovery.
4. Headless Connect Mobile and Tailscale iPhone pairing.

JEV, DeepSeek Harness, Layla, Shizuku, and broader routing policy are intentionally deferred.

## Blocked / explicit limitations

- TUI sessions do not yet have a canonical semantic result source. `ao session result` intentionally consumes Chat conversation state only; terminal scraping is not used.
- Android/Termux behavior is not part of Phase 0 and has not been claimed as tested.
