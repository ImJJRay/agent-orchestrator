# Pocket Agent Orchestrator — Project Status

Updated: 2026-09-30

This file records Pocket Agent project state only. Upstream product status remains in `docs/STATUS.md`.

## Baseline

- Upstream mirror `main`: `b188dd3bf03c331e01cb1c43f93163811cd33a15`.
- Legacy custom branch preserved as `quant-main-pre-upstream-2026-09-29` at `a8bbbc06f33b51f0db9d8eb135bda84eccfd1188`.
- Active Pocket Agent integration branch: `pocket-main`.
- Legacy branch name `quant-main` is superseded by `pocket-main` and should not receive new work.
- Phase 0 custom baseline is integrated into `pocket-main`.

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
- Session CLI output exposes `harness`, `mode`, and resolved persisted `model`.
- Restore pins the persisted session model for every harness so later project-default changes do not change a restored worker's model. Existing harness-specific effort semantics are unchanged.

## September code deliberately not replayed

Current upstream already owns OpenCode launch/restore `--model` handling, model catalogs, persisted session metadata, Chat/ACP infrastructure, OpenCode configuration precedence, Windows/runtime environment behavior, and restore/session APIs. The old OpenCode/model commit was therefore not cherry-picked.

The old result-retrieval commit was reimplemented against the current paginated, branch-aware conversation architecture rather than replayed.

## Validation performed

Passed:

- Focused Linux race regression for the existing Claude restore matrix and the new OpenCode TUI/Chat model-persistence invariant.
- Focused restore regression covering legacy sessions without persisted model metadata, persisted-session model precedence, and OpenCode TUI/Chat restore behavior.
- Go formatting.
- `go build ./...`
- `go vet ./...`
- golangci-lint.
- API drift check.
- sqlc drift check.
- Cloud Go build/vet/test job.
- gitleaks.
- CLI E2E fresh-install container and Linux native jobs on the clean final branch.
- The immediately preceding reconstruction of the same Pocket Agent delta also passed CLI E2E on Linux, macOS, Windows and the Windows workspace/session job.

The repository-wide `go test -race -count=1 -timeout=20m ./...` suite is intentionally not a Phase 0 completion gate for this cycle. It was explicitly stopped after repeated long-running CI executions. No pass is claimed for that full suite.

## Deferred

Android/Termux ARM64, SQLite/Git/worktree validation on the AYN Thor Max, OpenCode runtime validation on-device, Connect Mobile/Tailscale pairing, JEV, DSH, Layla, Shizuku, and broader routing policy are outside Phase 0.

## Thor Runtime + iPhone Remote Control milestone

Implemented as one cohesive feature from `pocket-main`, based on current upstream
`b188dd3bf03c331e01cb1c43f93163811cd33a15`. No upstream commits were missing at
inspection. The Phase 0 functionality was retained, not reimplemented.

### Exact custom delta

- Android-safe AO hook-shim interpreter: resolve native `sh` instead of writing
  a `/bin/sh` shebang. Android tmux fallback likewise resolves a native shell.
- Runtime prerequisite errors identify the actual OS. Android continues using
  upstream tmux; desktop Linux keeps its existing detached PTY path.
- Thin `ao mobile` CLI for existing loopback status/enable/disable/regenerate and
  secure-pairing controls, plus mobile-app-compatible v2 pairing links. Repeated
  CLI enable preserves pairing credentials. Default pairing requires verified
  private HTTPS; trusted-LAN pairing is explicit. No mobile control route was
  exposed through the authenticated listener.
- Secure Tailscale endpoints appear in mobile status and endpoint refresh even
  when userspace networking has no tunnel NIC.
- Explicit `AO_TAILSCALE_BINARY` / `AO_TAILSCALE_SOCKET` configuration applies to
  discovery and the entire upstream Serve lifecycle.
- `AO_MOBILE_TAILNET_ONLY=1` prevents automatic public Cloudflare connector
  selection. No Funnel, public listener, custom proxy, or new transport was added.
- Native Termux bootstrap, pinned native OpenCode installer, headless daemon/node
  start commands, and isolated deterministic validation script. See
  [THOR.md](THOR.md) for the complete sequence and evidence boundaries.
- Mobile command telemetry classification and focused CLI/platform/mobile tests.

### Upstream work avoided

The unmodified backend already cross-builds for Android ARM64, including modernc
SQLite and existing Unix process/PTY packages. Connect Mobile already operates
without Electron and restores credentials/listeners on boot. OpenCode Chat/ACP,
worktrees, model selection and restore are upstream capabilities. No dependency
fork, new adapter, replacement mobile client, proot, generated-file edits, JEV,
DSH, Pi evaluation, Layla, Shizuku, or broad policy work was introduced.

### Validation actually performed

- Unmodified baseline and modified AO Android ARM64 cross-builds with CGO off.
- Official Tailscale v1.102.5 CLI and daemon Android ARM64 cross-builds with
  `ts_omit_systray,ts_omit_ssh`; verified Android ELF interpreter and PIE output.
  Native bootstrap uses CGO on for Android/bionic DNS; native execution is pending.
- Downloaded native OpenCode v2.0.12 community package: SHA256 and Termux control
  metadata verified. No Android execution or ACP/provider turn is claimed.
- Isolated Linux diagnostic: executable, daemon readiness, migrated SQLite,
  loopback health/readiness/identity, API project registration, native Git
  worktree isolation, mobile bearer auth, mobile control-route exclusion and
  password/identity-preserving daemon restart passed.
- Complete CLI, telemetrymeta, agentlaunch, tmux, OpenCode adapter and ACP package
  suites passed. Complete mobilebridge and controller suites passed under an
  unprivileged user (their permission-denial fixtures fail when run as root).
- Focused race checks passed for mobile controls/secure pairing and the existing
  OpenCode session-model/project-default restore regressions.
- `go build ./...`, `go vet ./...`, and pinned golangci-lint v2.13.2 passed
  with zero lint findings. Final CLI/agentlaunch/telemetry recheck and API
  schema drift/parity checks passed. No API schema changes were required.
- Shell syntax and Python compilation passed for deployment/validation scripts.

The repository-wide `go test -race ./...` was not run, consistent with the user's
explicit development-loop constraint. Remote cross-platform CI and physical
Android/iPhone checks are not implied by these local passes.

### Physical Thor/iPhone tests still required

1. Run native bootstrap: CGO/clang link, native DNS, Android process/sockets,
   SQLite, Git/worktrees and daemon initialization.
2. Install/authenticate native OpenCode and run `validate-thor.py --chat
   'provider/model'`: real AO-owned worktree edit, exact result, model persistence,
   daemon/session restore and follow-up turn.
3. Optionally run `--tui`: native tmux/PTY/prompt/file-edit and restore acceptance.
4. Start/authenticate the separate userspace Tailscale node, enable HTTPS/Serve,
   and run `--secure` for production Serve/login configuration validation.
5. Pair the existing AO iPhone client away from LAN, create a Chat worker, inspect
   changed files/result and reconnect/restore after AO restart.

### Known limitations and next batch

The Android VPN app has no desktop CLI, so it alone cannot drive secure pairing.
The supplied userspace node is separately authenticated and must be validated on
Thor. It does not install OS-level tailnet routes/MagicDNS inside Termux; peer
TLS/reachability must be checked from another tailnet device. The OpenCode package
is a community native prerelease; compatibility is an acceptance test, not an
assumption. Android background-process survival and boot supervision are not
claimed. TUI has no canonical semantic result source; `ao session result` remains
Chat-only. There is no physical-device validation in this status update.

Physical Thor/OpenCode/iPhone acceptance remains pending and must still be run when the
device is available. Per the latest project decision, device acceptance no longer blocks
independent backend/control-plane work. New implementation work starts from current
`pocket-main` on a feature branch and returns through an unmerged PR; `main` and
`quant-main` remain untouched.


## Execution evidence and deterministic completion pre-gates — pending PR

Feature branch base: `pocket-main` @
`18347f4d549c1767dbabe5fb2e51182a5c21bb56`.

### Verified before implementation

- Phase 6 Chat result semantics already exist through `ao session result <id>`: exact
  durable assistant output, explicit `completed/running/failed/malformed` states,
  no generated summary, and no terminal scraping. TUI remains explicitly unsupported
  until a harness-native semantic result source exists.
- Current AO already persists canonical nullable session usage totals for input,
  cached input, uncached input, output and estimated cost where a supported source
  reports them. Missing counters remain null; incomplete collection remains explicit.
- Chat conversation usage is latest cumulative conversation state, not a per-turn
  ledger. It must not be relabeled as execution-scoped usage.

### Implemented on this feature branch

- New `ao session execution <id> [--json]` observation command combines existing
  durable AO facts into schema `pocket.execution.v1`.
- Latest non-rolled-back Chat turn reports timing, retry lineage and changed-file
  evidence when the daemon has them.
- Exact `ao session result` semantics are embedded rather than duplicated or
  summarized.
- Canonical usage/cache/cost is exposed with `scope: "session"` and
  `executionScoped: false`. Cache hit ratio is derived only when both inclusive
  input and cached-input counters are known.
- Unknown execution-level usage/cost, provider/account, historical effort, validation,
  dependency and final-human-approval facts are explicitly named as unknown.
- Deterministic completion pre-gates block on an incomplete/malformed/failed result
  or any pending approval/structured input. Validation, dependency and final approval
  gates remain unknown until durable task policy supplies them.
- `automaticAcceptanceAllowed` and `mergeAuthorized` are both false. Semantic task
  acceptance never grants Git merge authority.

### Not yet claimed

This increment is a policy/telemetry foundation, not Phase 7/8 completion. It does not
yet add durable task/worker/execution identities, per-execution token accounting,
task-scoped validation profiles/results, dependency graphs, autonomy budgets, route
candidate filtering, retry/escalation budgets, worker reuse, cache-aware switching,
Jev decisions, or multi-task planning.

Jev live integration is intentionally not present. The official TypeSafe OpenAPI
currently exposes `POST https://api.typesafe.ai/v1/systemone` and
`GET https://api.typesafe.ai/v1/models` with bearer authentication. Dynamic model
discovery is therefore possible, but no credential or live availability has been
assumed or tested.
