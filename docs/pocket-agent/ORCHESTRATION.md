# Deterministic orchestration

Pocket derives policy from AO's SQLite task, execution, conversation and validation
facts. AO continues to own the Chat provider, runtime, conversation, worktree and
restart lifecycle. Pocket reserves decisions and asks AO to perform admitted work.
No model-generated decision is part of this core.

## Configure a bounded task

Use the existing loopback `POST /internal/pocket/tasks` to create a task with
`projectId` and `objective`. Use the existing validation-requirements endpoint to
configure task-scoped checks before enabling automation. Those checks run in the
AO-owned worktree after execution. Existing automatically observed tasks can also
be configured; their already-recorded attempts count against the new limits.

Before the first attempt, replace its prerequisites with
`PUT /internal/pocket/tasks/{taskId}/dependencies`:

```json
{"dependencies": ["prerequisite-task-id"]}
```

All prerequisites must be tasks in the same project. Dependency updates reject
cycles, self edges, duplicate edges and missing tasks atomically. Once any attempt
is reserved or observed, its dependency graph is fixed. Explicit task completion
through the existing task-state endpoint satisfies a dependency only when its
required validation is not failed, pending or unknown. Finishing an AO turn does
not mark the task accepted.

Configure authorization using `PUT /internal/pocket/tasks/{taskId}/policy`:

```json
{
  "auto": true,
  "sessionId": "project-worker-1",
  "escalationSessions": ["project-worker-2"],
  "maxAttempts": 3,
  "maxRetries": 1,
  "maxEscalations": 1
}
```

Targets must be explicit existing Chat worker sessions in the task's project.
Configure their harness/model/settings through AO's existing controls. An escalation
means sending the task to the next declared session; Pocket neither compares nor
switches models. After an escalation, retries stay with that session if any global
retry budget remains. The configured root session applies to a task with no attempt;
retries of an already observed task stay with its actual latest session.

Defaults for unconfigured tasks: automation off, 3 attempts, 1 retry, 1 escalation,
no execution target. Limits: attempts 1–64; retries/escalations 0–63. Setting a
zero retry/escalation budget is valid. A budget is consumed by an attempt reservation,
not just successful provider execution; manual AO retry attempts consume retry
budget too. A human AO retry arriving before a reserved escalation dispatches
supersedes that reservation and counts as a retry instead. Failed/cancelled attempts are never removed from the ledger. Reconfiguring
a task does not reset counters. Escalation also requires an available explicitly
listed target, even if escalation budget remains.

## Inspect policy and history

```sh
ao pocket policy <task-id> --json
ao pocket decisions <task-id> --json
ao pocket decisions <task-id> --before <sequence> --json
```

The same surfaces are `GET /internal/pocket/tasks/{taskId}/policy` and
`GET /internal/pocket/tasks/{taskId}/decisions?before=<sequence>`. These reuse the
existing loopback-only boundary; they are not added to mobile/public OpenAPI.
The policy view includes the latest 100 decisions and all bounded task actions,
with recent action events. Decision pages contain up to 100 entries and a
`nextBefore` cursor. The full immutable decision and action-event history remains
in AO SQLite. Task state, latest execution, consumed budgets, prerequisites,
blocked prerequisites, target safety facts and reasons are inspectable.

| State | Meaning |
| --- | --- |
| `READY` | The initial task can start on its explicit target if automation is enabled. |
| `BLOCKED` | Prerequisite, execution, validation, busy target or pending action prevents progress. |
| `RETRY` | The failed attempt or authoritative validation failure can receive a bounded remediation attempt. |
| `ESCALATE` | Retry budget is exhausted; a configured escalation session and both budgets remain. |
| `NEEDS_USER` | Budget, unresolved input, unknown validation, uncertain delivery/lineage, unavailable target or task acceptance requires intervention. |
| `COMPLETED` | Task completion was explicitly recorded; policy does not automatically accept tasks. |

Every outcome has a reason and `allowed` flag. `READY`/`RETRY`/`ESCALATE` with
`allowed: false` and `automation_disabled` describes a possible action without
authorizing it. After reservation, the current state is normally blocked while
its outbox action waits for dispatch; the action's authorizing decision remains
in history. Claiming re-evaluates current budgets, dependencies, explicit task
state and authorization before dispatch. Disabling automation prevents prepared
actions from dispatching; it does not cancel an AO turn already sent.

Required checks are authoritative. Missing runnable validation blocks until its
runner records a result; unavailable validation remains unknown and requires user
attention. Failed execution can retry before validation because the validator
only runs on completed/recovered attempts. A semantic/model pass cannot satisfy
a deterministic requirement or override its failure. Execution-scoped requirements
are copied to remediation/escalation attempts, but old results never count as
new evidence. Even a deterministic pass does not imply semantic task acceptance
or Git merge permission.

## Delivery and restart safety

The action decision and execution reservation are committed in one transaction.
Root/child uniqueness constrains the attempt chain, and claiming a prepared action
is serialized. Sends use `pocket:<decision-id>` as AO's durable client message
identity. Eligible failed human turns use AO's native unique retry relation instead.
Native retry eligibility and provider/branch checks remain AO's responsibility.
A human native retry that races an undispatched escalation is adopted into its
reservation, and the escalation is suppressed. A conflicting manual retry after
policy dispatch is preserved in AO and requires human reconciliation before any
further automatic progression.

The lifecycle projector binds a policy delivery to its reserved attempt before
ordinary automatic turn projection, so a race cannot produce a second root task.
On restart, AO settles/adopts its runtime first; Pocket then reconnects actions
to durable AO messages/retry turns and projects AO state. A dispatch interrupted
before AO persistence may resume the same reservation/key. A durable turn is
never resent just because Pocket missed the dispatch response. A call with no
provable durable AO turn fails closed as uncertain. AO provider delivery uncertainty,
rolled-back sources and lost attachment context on non-native retries also require
user attention. Proven busy/input admission refusal consumes no additional attempt
and preserves its event in the action audit trail.

This core does not execute terminal/TUI tasks, create new sessions, choose models,
reuse workers opportunistically, plan tasks, automatically accept results, or merge
Git changes. Supply task decomposition, sessions and acceptance through existing
user/AO controls. Device/provider validation on Thor remains a separate check.
