# Pocket Agent Harness Evaluation

Updated: 2026-09-30

This document defines comparable evaluation cases. It does not promote a harness or
record measurements that have not been run.

## Current evidence

### OpenCode

OpenCode remains the Pocket production baseline because AO already supports its Chat/ACP
and TUI paths, model selection, restore and worktree ownership. Native execution on the
AYN Thor remains a pending physical acceptance test.

### Pi

AO already includes Pi TUI support and Pi Chat through the separately installed Pi ACP
adapter. The existing Pi Chat boundary is intentionally conservative: the current
adapter does not enforce AO permission modes, so Chat requires the user to opt into the
bypass-permissions fallback. Pocket must reuse this upstream support rather than add a
second Pi adapter.

No Pocket Pi performance, cache, recovery or Thor measurements have been run.

### DeepSeek Harness (DSH)

Current Agent Orchestrator upstream now ships native `deepseek-harness` support, including the agent adapter and ACP Chat driver. Pocket consumes that upstream implementation directly; no Pocket-specific DSH adapter is planned.

DSH evaluation is still useful for Thor compatibility, cache behavior, continuity, cost, latency, permissions, recovery and result semantics. It is now an evaluation of upstream-native AO behavior rather than a decision about whether Pocket should design an adapter.

## Comparable cases

Each case starts from the same repository commit and an isolated AO worktree. The task
brief, acceptance criteria and validation commands must be identical across harnesses.
Use the same model/provider only when both routes actually expose it; otherwise record
the resolved route rather than fabricating parity.

| Case | Purpose | Required deterministic acceptance |
| --- | --- | --- |
| E1 targeted edit | Small change in 1-2 files | specified test passes; forbidden files untouched |
| E2 bounded bug fix | Existing failing regression in <=5 files | regression fails before and passes after; focused suite passes |
| E3 continuation | Follow-up on E1/E2 in the same logical session | native resume succeeds; prior context remains relevant; no workspace ownership conflict |
| E4 long conversation | Repeated related turns with stable project context | all requested changes pass; continuity/cache counters captured only when reported |
| E5 restart recovery | Interrupt AO/harness between durable steps | restart reconciles truthfully; no false completion; workspace and session ownership preserved |
| E6 permission boundary | Task that requests a controlled action | route follows its real permission semantics; no silent privilege widening |
| E7 parallel isolation | Two non-overlapping tasks from one base | separate worktrees; no write collision; both validations independently reproducible |

## Harness matrix

Run E1-E7 first through OpenCode. Run the same cases through upstream Pi and AO's upstream-native DSH integration. DSH remains an experimental Pocket routing candidate until measured on the Thor, but adapter design is no longer Pocket work.

For every run capture, where actually available:

- AO session and logical test-case identity;
- harness, interface mode, resolved model/provider and reasoning/effort;
- start/end/duration;
- exact durable result and final execution state;
- input, cached input, uncached input and output usage;
- cost plus whether it was provider-observed or inferred;
- retry/escalation count;
- deterministic validation results;
- changed files;
- permission/approval behavior;
- resume/recovery outcome;
- cache continuity before and after route changes;
- Thor/Termux outcome when physical testing becomes available.

An unavailable metric is `unknown`, never zero.

## Promotion rule

A secondary harness is not promoted because it is new, faster in a single anecdote, or
has a public cache claim. Promotion requires repeatable Pocket measurements for the task
class in question and no regression in permissions, recovery, result semantics or
worktree isolation. Upstream-native DSH is promoted only where its measured Pocket results justify routing to it; no custom adapter or forked DSH integration is planned.
