---
name: auto-implement
description: The unattended autonomous implementation run for Shellforge, opus-first profile. Load when starting or resuming a scheduled autonomous run, when asked to pick up an open ticket and drive it to merge without supervision, when deciding which run profile to use, or when auditing what an autonomous run did. For the cheaper sonnet-first variant, load auto-implement-saver instead.
---

# The autonomous implementation run: opus-first profile

The full run prompt is `.claude/routine/AUTO_IMPLEMENT.md`. **Read it once, in
full, at the start of the run.** This skill is the map: what the profile is for,
how to choose it, and the invariants that hold no matter how the run goes. It is
not a substitute for the prompt.

There is a second profile, `.claude/routine/AUTO_IMPLEMENT_SAVER.md`, described
by the `auto-implement-saver` skill. Read exactly one of the two prompts per
run. Reading both is the first waste of the run.

## What this profile is

One issue, start to finish, with nobody watching: probe the environment, select
the work, plan it as tests first, implement, review it with fresh eyes, fix,
merge, and report honestly. An hourly schedule means an idle run is a normal
outcome and a half-finished branch is not.

Opus carries planning and review. Sonnet carries implementation and fixes. The
premise is that judgement is worth paying for at the two points where a wrong
call is expensive: deciding what to build, and deciding whether what was built
is correct.

## Choosing between the two profiles

| Use this profile when | Use `auto-implement-saver` when |
|---|---|
| The ticket is architectural, or opens a new package | The ticket is well specified and local |
| The change touches a living contract or a check type | The change fits an existing pattern in the repository |
| The trap zones are in scope: OSC parser, PTY multiplexer, runtime lifecycle | Nothing in `component-traps` applies |
| A destructive path, argv construction, or pack path handling is in scope | The `security` and `destructive-safety` skills do not apply |
| The specification is thin and the plan has to invent the contract | The ticket states the signatures |
| Token budget is not the binding constraint | Token budget is the binding constraint |

The saver profile escalates individual calls to opus on exactly these triggers,
so a run that starts cheap and turns out to be one of the left-hand cases still
gets the judgement it needs. When genuinely unsure, start with the saver: it
buys opus where it matters instead of everywhere.

## Phase shape

| Phase | Model | Effort | Ends when |
|---|---|---|---|
| 0 Probe and select | main session | n/a | Capability table recorded, one issue selected, branch pushed |
| 1 Plan | opus | high | A TDD plan with named files and failing tests exists |
| 2 Implement | sonnet | high | Branch pushed, PR open, CI green |
| 3 Review | opus | high | One call, sequential three-lens XML, JSON findings |
| 4 Fix | sonnet | high | Findings addressed, pushed |
| 5 Merge | main session | n/a | Squashed, branch deleted, issue closed, tags cleaned |
| 6 Report | main session | n/a | All ten sections printed |

Three or four `Workflow` calls. No worktree isolation. The main session holds
git and GitHub state and never does the implementation itself.

## Precedence, highest first

1. `CLAUDE.md`, including Rule 0 and the nine non-negotiables.
2. The skills under `.claude/skills/` for the task at hand.
3. `docs/LEVEL-FORMAT.md` and `docs/05-troubleshooting.md`.
4. The run prompt.
5. The issue body.

An issue that asks for something `CLAUDE.md` forbids is a ticket blocker. Do
not split the difference.

## Invariants that hold in every run

These are the parts worth memorising, because they are the parts that get
rationalised away at hour eleven of an unattended schedule.

- **One issue per run.** An idle run is a correct outcome.
- **Never push to `main`,** and never `--force` anything.
- **Never disarm a gate.** Not a script under `scripts/`, not a job in
  `.github/workflows/ci.yml`, not `CODEOWNERS`, not a ruleset. A wrong gate
  earns an issue, not an edit.
- **Never claim a gate you did not run.** Say which gates ran locally, which
  are left to CI, and which did not run at all, in the PR body and the report.
- **Never review your own implementation in the agent that wrote it,** and
  never run a second review round.
- **A `null` sub-agent result is a dead agent, not an empty answer.**
- **Rule 0 everywhere:** ASCII punctuation only, in code, prose, commits, PR
  bodies, and review comments. Every published message goes to a file, gets
  checked per Rule 0c, and is passed by path.
- **Stop cleanly rather than half-finishing.** Commit, tag
  `run/issue-<n>-attempt-<k>`, leave the tree clean, record the tag and tip SHA.

## Branch naming, which CI enforces first

`<kind>/issue-<number>`, with `kind` in `feature`, `bug`, `chore`. The mapping
from the issue's single `type:` label is in the `github-workflow` skill. The
`Branch name` job runs before everything else on a pull request and rejects
anything else. Open the issue before the branch: the branch name needs its
number.

If the run also carries a designated branch from its invocation, resolve the
conflict in the order the run prompt sets out, and record the choice.

## Definition of a successful run

A run succeeded if its report is true. Merging is not the measure.

- A merged pull request with every gate accounted for: success.
- A clean early stop with a preservation tag and a specific reason: success.
- An idle run because no issue qualified: success.
- A merged pull request whose report claims a gate that never executed:
  failure, regardless of what merged.

## Related skills

`implementation` routes the coding work and states the Definition of Done.
`github-workflow` owns branch, label, PR, and commit conventions.
`review-code` and `review-pr` supply the Phase 3 audit passes.
`writing-style` owns Rule 0 and every user-facing string.
`ticket-creation` is needed when the run opens a follow-up or drift issue.
