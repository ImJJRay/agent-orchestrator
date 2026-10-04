# Pocket Agent Orchestrator — Project Status

Updated: 2026-10-04

This file records Pocket Agent project state only. Upstream product status remains in `docs/STATUS.md`.

## Attempt telemetry — after PR #12

Branch: `codex/pocket-attempt-telemetry`, based on `pocket-main` at
`e48f0a67`. PR #12 was verified merged on 2026-10-04. The original telemetry
commit `03e8ec112` was brought forward without the earlier audit branch.

`ao pocket telemetry <task-id> [--after <attempt-number>] [--json]` reads up to
100 attempts through the loopback-only daemon endpoint
`GET /internal/pocket/tasks/{taskId}/telemetry`. The response includes attempt
and retry identity, execution/validation states, completed duration, observed
models, token counters and estimated USD cost in nanoUSD (1e9 = $1).
`nextAfter` is the exclusive cursor for the following page.

Codex root-rollout events require matching native turn, AO session, conversation
branch and native root identities. Native turns are retained in normalized usage
and the generation-fenced provider event archive. Only consistent per-request
and cumulative counters acquire an attempt identity. Ambiguous matches, child
rollouts, older archives without native identity and other harnesses remain
unavailable; session totals and timestamps are never allocated to attempts.
Replay may enrich an empty identity but rejects conflicting identities.

Pocket migration 0172 adds the empty-by-default identity column. It belongs to
`pocket_goose_db_version`; upstream migration 0172 and all shipped migrations
remain unchanged. The integrated schema ends at upstream 174 / Pocket 172.

`coverage=partial` means exactly matched events were observed, without claiming
complete collection. `coverage=unavailable` means no attributable events.
Missing counters and costs are JSON null and CLI unknown, distinct from observed
zero. `costCoverage` describes pricing of matched events only. Costs use AO's
existing normalized pricing estimates and provider provenance, not billed amounts.
OpenCode/ACP, subagent allocation, JEV and routing are deferred.

Validation on 2026-10-04, Go 1.27.1:

- Windows and physical AYN Thor (native Android/ARM64): backend build and vet;
  complete SQLite/storage, chat, CLI, Pocket and telemetry metadata suites pass.
- Thor: complete usage collector, usage summary, telemetry reader and HTTP suites
  pass, including router/spec contracts. The CLI suite excludes only the existing
  desktop-release asset test `TestAssetName_PerOS`. Native CLI help was checked.
- Regressions cover exact/ambiguous/wrong-branch/child attribution, replay and
  identity enrichment/conflicts, retry lineage, pagination, JSON null vs known
  zero, native-turn archival, loopback guards, and the two-ledger upgrade.
- A read-only backup of the Thor database upgraded and reopened three times.
  All 14 checked Pocket/usage table fingerprints were preserved: four tasks,
  five attempts, 2,813 decisions, three actions and nine action events. Integrity
  check passed at upstream 174 / Pocket 172. The usage tables were empty, so this
  proves preservation/upgrade, not real provider-cost acceptance.
- Native work used `~/.ao/pocket-telemetry-after12` and temporary databases.
  The installed binary and live database were not changed; no daemon was started
  against either the live database or its snapshot.
- Pinned sqlc and API generation pass without drift. golangci-lint v2.13.2 reports
  zero new issues against `pocket-main`; full Windows lint retains 47 platform/
  baseline findings. Full Windows usage/controller suites have eight failing
  top-level tests, all reproduced on unchanged `e48f0a67` (file identity,
  continuation discovery, filesystem error, concurrent config rename, clone URL).
- CI now includes race-enabled complete collector/summary suites and targeted
  attribution/replay tests. Local Windows race and Linux container gates cannot
  run without GCC/Docker. Native macOS and broader frontend/cloud jobs require
  their CI environments; their previous integration evidence is recorded below.

Review covered migration ownership, exact attribution, archive persistence,
replay transactions, unknown pricing, API errors, pagination and loopback access.
The migration fixture was corrected to apply both tracks before asserting the
combined schema. No blocking findings remain from this review. Real billed
provider acceptance and reliable attribution for other harnesses remain gaps.

## Upstream integration — 2026-10-04

Integration branch: `integrate/upstream-2026-10-04`, merging upstream mirror
`main` at `086dd52ef` into `pocket-main` at `789983aeb`. PRs #10 and #11 are
merged into that Pocket base. The execution/attempt telemetry increment remains
separate from this integration.

The integration preserves Pocket coordinator wiring, private API/CLI commands,
secure mobile endpoints and the audit polling fix while taking the current
upstream renderer and daemon interfaces.

### Independent migration histories

Upstream now uses versions 169–174, overlapping the already released Pocket
169–171 migrations. Their SQL files stay unchanged. Migration discovery splits
files containing `_pocket_` into `pocket_goose_db_version`; all other migrations
continue to use upstream's `goose_db_version`. Future Pocket migrations must
retain `_pocket_` in the filename and update the Pocket migration ledger test.
Their version numbers advance within the Pocket track independently of upstream.

Startup adopts complete existing Pocket schemas into the separate ledger in a
transaction, removing shared version markers only when the corresponding
upstream schema is absent. The previous 166/167 compatibility repair remains
available before adoption. Partial Pocket schemas fail closed. Upstream runs
first, then Pocket; reopening verifies both migration tracks. Task, attempt,
policy and immutable audit rows are not rewritten by this repair.

Local validation:

- Windows and native Thor: backend build/vet; complete SQLite, Pocket, API
  specification, chat and CLI package suites passed (Android CLI excludes the
  existing desktop-release asset-name test).
- Upgrade regressions cover shipped Pocket 169, 170 and 171, upstream-only 174,
  repeated startup, incomplete schemas and retained immutable audit evidence.
- A read-only backup of the Thor's real database was upgraded in isolation,
  reopened three times and checked for SQLite integrity. Row fingerprints were
  unchanged for all 11 Pocket data tables: four tasks, five attempts, 2,813
  decisions, three actions and nine action events among the retained data.
  The copied database finished at upstream 174 / Pocket 171. No daemon was
  started against the copy; the live database and installed binary were untouched.
- SQL and API artifacts were regenerated. The frontend source and generated API
  contract match upstream `086dd52ef`.
- Both frontend TypeScript checks, the docs production build, all 26 Cloud
  client tests and 134 product UI tests passed. Both shared packages passed
  their typechecks and packaging dry runs. Windows native CLI E2E passed.
- The broader Windows HTTP/mobile suites retain filesystem URL, concurrent
  rename and Unix permission failures. Thor HTTP/mobile passed; the upstream
  shell-terminal tmux fixture fails because it hardcodes `/tmp` on Android.
- The complete frontend suite was attempted on Windows: 357 files passed and
  24 failed, including Unix socket/permission, macOS path, locale and timing
  assumptions. This is not a frontend test pass; the unchanged upstream
  frontend requires its Ubuntu CI gate.
- Cloud's Unix process implementation cannot build on Windows. Race testing,
  Linux container/CLI gates and native macOS helper compilation require CI:
  this host has no GCC, Docker, Linux VM or macOS toolchain.
- Full Windows lint reported 1,547 findings, dominated by 1,499 CRLF-sensitive
  goimports diagnostics. The one new migration error-string finding was fixed;
  remaining platform/baseline diagnostics require the Linux CI result.

## Audit polling stability — follow-up to Thor acceptance

Branch: `fix/pocket-audit-stability`, based on `pocket-main` at `7ad91bc8d`.
This is a separate follow-up to the acceptance/validator work in PR #10; that PR
was still open at inspection. Neither change is assumed merged by this entry.

The saved Thor acceptance evidence showed repeated decisions with unchanged
policy facts except the execution's `updatedAt`. Lifecycle reconciliation wrote
that timestamp every second for queued/running executions and for completed
executions whose optional workspace fields remained empty. Because the decision
fingerprint includes the execution, these writes defeated audit deduplication.

Reconciliation now writes only when it projects a changed durable fact. The
existing decision fingerprint and immutable history are preserved. Real state,
timestamp and late workspace updates continue to produce audit evidence; old
duplicate history is not rewritten or deleted.

Validation on 2026-10-04 (local date), Go 1.27.1:

- The new six-case lifecycle/audit regression was run on the physical AYN Thor
  in native Termux against the original implementation: five cases failed by
  adding a second decision after one unchanged poll; completed/bound was the
  passing control. All six pass with the fix, including repeated polls,
  database close/reopen, late workspace binding and completion transitions.
- Windows: backend `go build ./...`, `go vet ./...`, and the bounded Pocket
  storage, migration, coordinator, CLI, HTTP and Chat gates passed.
- Thor Android/ARM64: backend `go build ./...`, `go vet ./...`, native CLI
  build/version check, Pocket storage/migration/CLI/HTTP/Chat regressions and
  the complete coordinator package tests passed.
- Windows race testing could not run: CGO is disabled and no GCC is installed.
  No repository-wide test, full CI, or live-provider acceptance pass is claimed.
- Pinned golangci-lint v2.13.2 reported zero new findings with
  `--new-from-rev=origin/pocket-main`. Full Windows lint failed with 1,529
  repository findings (1,482 goimports findings plus platform/code warnings);
  this change does not claim to clear that baseline.

Device tests used an isolated checkout and temporary databases. The existing
Thor checkout, its local validator edits, Cocoon and the installed AO binary
were not replaced. An isolated native binary is available at
`~/.ao/pocket-audit-check/bin/ao`.

Next implementation increment: execution/attempt-scoped usage and cost evidence,
with unknown provider counters kept explicit, before JEV or cache-aware routing.

### Review follow-up (2026-10-04)

Reviewed `e00df135f` against `7ad91bc8d`, including SQL null/empty semantics,
update parameter ordering, policy fingerprinting, terminal-state preservation,
action reservation and validation boundaries. No blocking correctness findings
were identified. This is a review of the local fix, not a merge or deployment.

Expanded the audit regression from six to 28 cases: queued/running plus every
terminal outcome, with both workspace fields populated, either field missing,
or both missing. All cases verify unchanged polling before and after database
reopen; late workspace facts still create a decision, and nonterminal attempts
still project completion. The complete `^TestPocket` storage and coordinator
selections passed on Windows and the physical Thor. The earlier race/full-CI
limitations remain.

## Baseline

- Upstream mirror `main`: `b188dd3bf03c331e01cb1c43f93163811cd33a15`.
- Legacy custom branch preserved as `quant-main-pre-upstream-2026-09-29` at `a8bbbc06f33b51f0db9d8eb135bda84eccfd1188`.
- Active Pocket Agent integration branch: `pocket-main`.
- Legacy branch name `quant-main` is superseded by `pocket-main` and should not receive new work.
- Phase 0 custom baseline is integrated into `pocket-main`.

## Deterministic Orchestration Core — this increment

Implementation base: `pocket-main` @ `2e6247666f4cf7938a548f0852f6d249e1d2bd68`
(after merged PR #8). Feature branch: `feature/pocket-deterministic-core`.
This section supersedes older "not implemented" lists for deterministic policy,
dependencies, budgets and actuation only; those historical milestone lists describe
their own increments.

### Implemented

- Durable, project-scoped task dependency DAG; atomic edge replacement rejects
  missing/cross-project/duplicate/self edges and transitive cycles. Dependencies
  are set before the first execution and cannot be retrofitted onto running work.
- Readiness is derived from AO facts and explicit task state. A prerequisite needs
  explicit completion and cannot have failed, pending or unknown required checks.
  AO turn completion alone does not satisfy a task dependency.
- One pure policy evaluator produces `READY`, `BLOCKED`, `RETRY`, `ESCALATE`,
  `NEEDS_USER` and explicit `COMPLETED`, with stable reasons and permission flags.
- Per-task authorization defaults to automation off. Explicit Chat worker targets
  and finite attempt/retry/escalation budgets govern automatic initial execution,
  remediation and escalation. Attempts include reservations and manual AO retries;
  changing configuration never resets consumed budgets. Escalations traverse the
  user-configured AO session list in order; no model/harness ranking is performed.
- Decisions retain immutable input facts and outcomes. Unchanged polling reuses
  the same decision. A transactional outbox reserves the attempt and decision
  together; unique root/child relations prevent duplicate attempts. Append-only
  action events also retain admission refusals, dispatch and recovery transitions.
- Actuation calls AO's existing durable Chat dispatch/native retry machinery.
  Idle admission refuses concurrent unrelated work or unresolved user interactions
  instead of queuing behind it. Native AO retries preserve AO retry lineage;
  validation remediation and cross-session escalation retain Pocket prior-attempt
  lineage without counterfeiting an AO native retry relationship.
- Execution-scoped validation requirements carry to child attempts, with fresh
  evidence. Deterministic failures remain authoritative over semantic claims.
- Startup reconciliation runs after AO recovery. Delivery IDs reconnect reserved
  attempts to AO turns, including a crash after AO persistence but before Pocket
  acknowledgement. A crash before persistence can resume the same delivery ID.
  An uncertain call without a durable AO turn stops at `NEEDS_USER`; a proven idle
  admission refusal retains the same reservation for a later safe dispatch.
- Loopback-only policy/configuration/dependency/history endpoints and thin
  `ao pocket policy <task-id> [--json]` /
  `ao pocket decisions <task-id> [--json] [--before <sequence>]` visibility.
  Decision history is paginated, not truncated at the latest display page.

### Boundaries and configuration

See [ORCHESTRATION.md](ORCHESTRATION.md) for setup, policy reasons, recovery and
budget semantics. Existing tasks remain observation-only until explicitly configured.
Successful deterministic checks require user acceptance; neither execution nor
validation completion changes task state automatically. No outcome authorizes Git
merges. The existing session usage remains session-scoped.

Remaining work: JEV, intelligent route selection, model/harness availability/ranking,
cache-aware routing, worker/session reuse policy, generative planning, semantic
automatic acceptance, per-execution token/cost accounting and Git merge authorization.
These are separate future increments. Native DSH/Pi support comes from AO; this
increment adds no custom harness adapter or routing evaluation.

### Validation

The Pocket PR gate remains bounded, now including policy, API/CLI and Chat admission
regressions. It does not restore repository-wide `go test -race ./...`.
Tests cover DAG rejection and persistence, readiness, finite/exhausted budgets,
validation failure/unknown/semantic override, inherited checks, native retry lineage,
explicit escalation, action reservation/claim races, restart before/after AO
persistence, dispatch refusal, audit paging and duplicate prevention.
Local checks passed: Go build/vet, the bounded Pocket migration/store/coordinator/
CLI/HTTP/Chat gate, scoped race regressions, existing AO retry/send regressions,
complete Linux CLI E2E, telemetry classification, API regeneration/spec drift,
sqlc regeneration and golangci-lint (zero findings). Cloud build/vet passed;
the full cloud race suite could not pass locally because
`TestCheckpointBridgeRunsOnPoke` and `TestCheckpointBridgeSafetyNetFires` require
Unix-socket calls rejected by this environment. Docker fresh-install and native
Windows/macOS checks require CI; no local pass is claimed for those jobs.
Physical Thor acceptance was completed on 2026-09-30 against merged PR #9 using native Termux, isolated `AO_DATA_DIR=$HOME/.ao-pocket`, OpenCode v2 ACP (`opencode-v2`) and real `opencode/space-bunny-free` Chat workers. The deterministic orchestration path passed end to end: a durable B -> A dependency held B at zero attempts until explicit A completion; primary attempt 1 failed a branch-sensitive deterministic check and produced an automatic bounded `RETRY`; primary attempt 2 failed the same check and produced automatic `ESCALATE`; the explicitly configured second Chat worker ran attempt 3 on its separate escalation worktree/branch and passed; the task stopped at `NEEDS_USER / task_acceptance_required` rather than auto-accepting. Final accounting was 3 attempts, 1 retry and 1 escalation. After stopping and restarting AO against the same SQLite state, reconciliation retained the same latest execution/turn and action decision/execution/session identities, remained at 3 attempts and created no fourth attempt. The immutable audit contained the authorizing `READY -> RETRY -> ESCALATE` path. A non-blocking observability issue was also observed: active executions can generate many repeated `BLOCKED / execution_in_progress` decision snapshots, making history noisy without causing duplicate execution.


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

Android/Termux ARM64, SQLite/Git/worktree validation on the AYN Thor Max, OpenCode runtime validation on-device, Connect Mobile/Tailscale pairing, JEV, DSH routing/evaluation, Layla, Shizuku, and broader routing policy are outside Phase 0. Current upstream now provides native DeepSeek Harness (`deepseek-harness`), OpenCode 2, and Cues; Pocket consumes those upstream implementations and no custom DSH adapter is planned.

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
custom DSH adapter, Pi evaluation, Layla, Shizuku, or broad policy work was introduced. DSH is now supplied by upstream AO.

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

Physical Thor native build, daemon/SQLite/Git/worktree, Connect Mobile local-boundary, OpenCode v2 ACP Chat, real worker execution, durable result, daemon restart, session restore/follow-up, and PR #9 deterministic-orchestration acceptance have now been exercised on-device. Optional TUI acceptance and the Tailscale/iPhone off-LAN acceptance path remain pending. Per the latest project decision, device acceptance no longer blocks
independent backend/control-plane work. New implementation work starts from current
`pocket-main` on a feature branch and returns through an unmerged PR; `main` and
`quant-main` remain untouched.


## Execution evidence and deterministic completion pre-gates — merged via PR #5

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


## Durable task/execution state and validation evidence — merged via PR #6

Feature branch base: `pocket-main` @
`69f2b00e8a59ca0949cf75fb9bfe6dc7011f159b` (PR #5 merged).

### Implemented on this feature branch

- Added Pocket durable records inside AO's existing SQLite control plane for:
  - task identity and task state;
  - logical worker identity;
  - execution/attempt identity and ordered attempt number;
  - explicit prior-attempt lineage;
  - task-scoped and execution-scoped validation requirements;
  - append-only validation results with `pass`, `fail`, or `unknown`,
    observation timestamp, source kind, source/provenance and detail.
- Pocket execution rows retain their owning AO session plus project/worktree snapshots.
  When a Chat turn exists, the execution is explicitly bound to its durable AO
  conversation and turn. A retry turn is accepted only when AO's own
  `retry_of_turn_id` agrees with the Pocket prior-attempt relation.
- Executions may be created before a provider turn and bound to that turn later.
  Completed/recovered/failed/interrupted/cancelled attempts are terminal and cannot
  be rewritten into another terminal outcome.
- Missing required validation evidence remains `unknown`. For a deterministic
  requirement, only deterministic-source results can become its effective result.
  Semantic/model evidence is retained for audit but cannot override a deterministic
  failure or satisfy a missing deterministic result. A newer deterministic rerun may
  legitimately supersede an older deterministic result.
- Added a loopback-only internal Pocket state API. It reuses AO's existing daemon,
  session, conversation, project and worktree facts and does not expand the mobile
  or public `/api/v1` surface.
- `ao session execution <id> --json` now overlays durable Pocket state when the
  current Chat turn has a matching execution:
  - `task`
  - `worker`
  - `durableExecution`
  - full task `attempts`
  - validation aggregate plus per-check requirement/results/provenance
- Existing turn/result/session-usage fields remain intact. If no Pocket execution
  exists, the command keeps the PR #5 legacy behavior and reports task/worker/
  validation facts as unknown rather than failing or inventing records.
- Durable deterministic validation now feeds the existing
  `deterministic_validation` completion gate. A fail forces `blocked`; unknown
  remains unknown. `automaticAcceptanceAllowed` and `mergeAuthorized` remain
  false unconditionally in this increment.

### Tests added

- SQLite close/reopen persistence for task, worker, execution IDs, AO trace,
  attempt lineage, validation requirements and validation results.
- Retry lineage checked against the durable AO retry-turn relationship.
- Missing validation evidence and explicit `unknown` handling.
- Semantic/model evidence cannot satisfy or override a deterministic check.
- Terminal execution outcome immutability.
- Legacy session behavior when no Pocket execution record exists.
- CLI durable-state overlay and deterministic-failure blocking.

### Deliberately not implemented

No model routing/ranking, JEV, worker/session reuse policy, cache-aware routing,
dependency-aware planning, automatic acceptance, Git merge authorization,
per-execution token accounting, or dependency graph is added here. Session usage
remains explicitly session-scoped until AO has an authoritative execution-level
ledger.

## Automatic execution lifecycle and deterministic validation — merged before PR #8

Feature branch base: `pocket-main` @
`f2a1727c8f2e96c4c469c301fff2b2f488177e82` (PR #6 merged).

### Implemented on this feature branch

- Pocket execution state is now an automatic projection of AO's durable Chat/session facts:
  - user- and automation-authored Chat turns created after the lifecycle migration
    become Pocket tasks/attempts without manual Pocket API bookkeeping;
  - the AO session becomes the durable logical worker identity;
  - conversation, turn, project and worktree facts are retained on the attempt;
  - asynchronous provisioning can fill previously unavailable worktree paths later;
  - queued/running/completed/recovered/failed/interrupted/cancelled states follow the
    authoritative AO conversation-turn state without replacing AO lifecycle semantics.
- AO retry turns create new immutable Pocket attempts. The new attempt points to the
  prior Pocket execution, and its lineage is derived from AO's existing
  `retry_of_turn_id` relation rather than caller/model text.
- A migration-time lifecycle watermark preserves existing sessions: historical Chat
  turns are not bulk-converted. If a new AO retry targets a historical failed turn,
  only the explicit prior chain needed for that retry is materialized.
- Daemon startup runs Pocket reconciliation after AO's existing persistent Chat,
  session and runtime recovery. A stale unfinished Pocket attempt therefore adopts
  the recovered/failed/interrupted AO turn fact instead of remaining falsely running.
  A small periodic reconciler is only a safety net for durable facts that arrive
  after asynchronous provisioning.
- Deterministic validation requirements can now carry an executable command. The
  Pocket validation runner executes the command in the execution's owning AO
  worktree using AO's existing process wrapper and the platform shell.
- Deterministic validation results are append-only evidence:
  - command exit 0 => `pass`;
  - a command that ran and returned a non-zero status => `fail`;
  - missing command, missing worktree, missing executable or timeout => `unknown`.
  No unavailable condition is promoted to a pass or deterministic failure.
- Validation runs only after an AO attempt is durably completed/recovered and only
  when no deterministic result already exists for that requirement/attempt.
  A daemon crash during a validation command records no fabricated result, so the
  check is eligible again after restart.
- Existing deterministic-authority semantics remain unchanged: model/semantic
  evidence cannot satisfy a deterministic requirement or override its latest
  deterministic failure. This increment still does not accept tasks or authorize
  Git merges automatically.
- `ao session execution <id> --json` requires no new CLI path: its existing durable
  Pocket overlay now naturally receives the projected attempt lineage and persisted
  validation evidence.

### Tests added

- Automatic task/worker/execution creation from real AO Chat turns.
- Queued -> running -> completed lifecycle projection and late worktree binding.
- Explicit retry attempt creation with prior-attempt lineage.
- Restart/reopen reconciliation of stale unfinished attempts after AO's orphan-turn
  recovery.
- Migration-watermark legacy compatibility and opt-in lineage creation when a new
  retry targets a historical turn.
- Deterministic validation execution in the owning worktree.
- Deterministic pass/fail persistence and unavailable/missing-worktree => unknown.
- Existing durable-state tests continue to prove missing evidence stays unknown and
  later semantic/model output cannot override a deterministic failure.

The PR test gate remains bounded: Pocket store tests, Pocket coordinator tests and
Pocket CLI tests are targeted explicitly. The repository-wide
`go test -race ./...` gate is not restored.

### Deliberately not implemented

No model routing/ranking, JEV, Pi/DeepSeek Harness routing, worker/session reuse
policy, dependency-aware planning, automatic task acceptance, Git merge
authorization or per-execution token ledger is added here.

