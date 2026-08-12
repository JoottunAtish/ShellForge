---
name: auto-implement-saver
description: The unattended autonomous implementation run for Shellforge, token saver profile: sonnet carries every phase and opus is bought only on named escalation triggers. Load when starting or resuming a scheduled autonomous run under a token budget, when asked to run the cheaper profile, or when deciding whether a ticket needs the opus-first run. For the opus-first variant, load auto-implement instead.
---

# The autonomous implementation run: token saver profile

The full run prompt is `.claude/routine/AUTO_IMPLEMENT_SAVER.md`. **Read it
once, in full, at the start of the run.** This skill is the map: what the
profile changes, where the savings actually come from, and the lines that budget
never justifies crossing. It is not a substitute for the prompt.

Do not also read `.claude/routine/AUTO_IMPLEMENT.md`. The two profiles are
alternatives and reading both is the first waste of the run.

## The one thing this profile changes

Where the tokens go. Sonnet carries every phase. Opus is bought per call, on a
named trigger, and the trigger is recorded in the report.

Nothing about the output contract moves: same gates, same three review lenses,
same Definition of Done, same honesty rules, same preservation discipline. If a
choice looks like correctness traded for budget, it is a misreading of the
profile. Spend the tokens.

## Where the savings come from

In order of how much they are worth. The first two are most of it.

1. **Specificity, not less thinking.** Sonnet at high effort on a narrow,
   fully specified task beats opus at high effort on a vague one, at a fraction
   of the cost. Every sub-agent prompt therefore carries the exact files it may
   touch, the exact acceptance criteria, the skills to load, the Phase 0
   reconnaissance, and a restatement of Rule 0. A sub-agent that has to go
   discover the repository is the expensive failure this profile exists to
   prevent.
2. **Reconnaissance gathered once.** Phase 0b collects everything the four
   phases need about the repository and passes it down by value. No sub-agent
   repeats it. Symbol names and signatures via jCodemunch, not file bodies.
3. **Narrow review contexts.** The three lenses run as three parallel sonnet
   agents, one lens each, deduplicated in the main session. Three small contexts
   cost less than one large one, and narrow scope is what makes sonnet reliable
   as a reviewer.
4. **Two `Workflow` calls, not four.** Plan and implement are one two-stage
   pipeline, so the plan never round-trips through the main session.
5. **Budgeted reads and logs.** Three supplementary reads per sub-agent. Sixty
   lines of failing log, from the failing job only. Diffs, not transcripts.
   Widening CI poll intervals. Gates chained in one Bash call.
6. **Cheap exits taken early.** Restoring beats adopting beats starting. A clear
   blocker stops the run on the spot rather than grinding.

## Escalate one call to opus, high, when any of these fires

Per call, not for the rest of the run. Record every escalation and its trigger.

1. A destructive path, argv construction, or pack path handling is in scope, or
   anything the `security` or `destructive-safety` skills govern.
2. The OSC parser, the PTY multiplexer, or `internal/runtime` process lifecycle
   is in scope. These are the `component-traps` zones.
3. The Phase 1 plan is internally inconsistent, names a file that does not
   exist, or proposes an upward import. One replan.
4. Phase 3 returns two or more `blocking` findings, or any `blocking` finding
   in the safety lens. Phase 4 escalates.
5. Repair round 3 on the same CI job. Two failed sonnet rounds on one job means
   the diagnosis is wrong, not the patch.
6. A living contract changes: `docs/LEVEL-FORMAT.md`, a check type, or the
   archtest tables.

If none fires, sonnet finishes the run. For a routine ticket that is the
expected outcome and it is not a lower standard.

## Phase shape

| Phase | Model | Effort |
|---|---|---|
| 0 Probe, recon, select | main session | n/a |
| 1 Plan | sonnet | high |
| 2 Implement | sonnet | high |
| 3 Review, one agent per lens, parallel | sonnet | high |
| 4 Fix | sonnet | medium, opus high on trigger |
| Repair rounds 1 and 2 | sonnet | medium |
| Repair rounds 3 and 4 | opus | high |
| 5 Merge, 6 Report | main session | n/a |

## Budget is never a reason for any of these

- Skipping the review phase, dropping a lens, or shortening a lens because the
  diff looks small. Cheaper review means narrower context, never fewer lenses.
- Skipping a local gate. `gofmt -s -w . && go vet ./... && go test ./... &&
  ./scripts/check-punctuation.sh` runs before every commit, output inspected.
- Claiming a gate that did not execute.
- Pushing to `main`, force-pushing anything, or disarming a gate.
- Reviewing your own implementation in the agent that wrote it, or running a
  second review round.
- Skipping the refusal tests the `destructive-safety` skill mandates.
- Treating a `null` sub-agent result as an empty answer.
- Working on more than one issue per run.
- Leaving a branch, a tree, or a pull request half-changed instead of
  committing, tagging `run/issue-<n>-attempt-<k>`, and reporting the tag.

The full NEVER list is in the run prompt and in `CLAUDE.md`. A cheap run that
breaks one of these is a failed run.

## The cost ledger

Section 11 of the report, specific to this profile: every phase with the model
that actually ran it, every opus escalation with the trigger that bought it, the
`Workflow` call count, the repair round count, and any context budget that was
exceeded and why.

Without it there is no way to tell a genuinely cheap run from a run that
quietly escalated everything, so the ledger is not optional.

## When to use the other profile instead

Prefer `auto-implement` when the ticket is architectural or opens a new package,
when the specification is thin enough that the plan has to invent the contract,
or when several escalation triggers are obviously in scope from the outset. In
that last case, paying for opus once up front costs less than escalating three
times.

When genuinely unsure, start here. This profile buys opus where it matters
rather than everywhere.

## Definition of a successful run

A run succeeded if its report is true, cost ledger included. Merging is not the
measure, and neither is coming in cheap.

- A merged pull request with every gate accounted for: success.
- A clean early stop with a preservation tag and a specific reason: success.
- An idle run because no issue qualified: success.
- A run that came in cheap by skipping a lens, a gate, or an honest report: the
  worst outcome available, because it looks like the best one.

## Related skills

`auto-implement` is the opus-first profile and the comparison table for choosing
between them. `implementation` routes the coding work and states the Definition
of Done. `github-workflow` owns branch, label, PR, and commit conventions.
`review-code` and `review-pr` supply the Phase 3 audit passes.
`code-navigation` is what keeps the context budget affordable.
`writing-style` owns Rule 0.
