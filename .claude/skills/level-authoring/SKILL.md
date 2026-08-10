---
name: level-authoring
description: Writing and reviewing Shellforge levels, covering the YAML schema, the check catalogue, deterministic setup, writing on_fail and hints, asset consistency, and the safety rules for setup and teardown scripts. Load when creating a level, editing a level, generating level assets, reviewing a content pull request, or when a golden test on a level fails.
---

# Authoring levels

The authoritative schema is `docs/LEVEL-FORMAT.md`. The level designs, with
objectives, checks and teaching notes for all 25, are in `docs/CURRICULUM.md`. This
skill is the judgement that goes around them.

## Workflow

```bash
shellforge author scaffold my-level
$EDITOR packs/core-linux-basics/levels/my-level.yaml
shellforge author validate packs/core-linux-basics
shellforge author test my-level
shellforge run my-level
```

Play it yourself before you file it. Every level in this game should have been
played by its author from a cold start.

## Safety: setup and teardown scripts are the content attack surface

`setup.script` and `teardown.script` run **as root**. `script` checks run as
`learner` by default and may request `root`. These are the only places where
content executes code, so they carry the rules.

- **They run inside the sandbox, never on the host.** A level string reaching a
  host shell is a security bug, not a content bug.
- **`setup.root` must be under `/home/learner/`.** The validator enforces it, and
  the runner enforces it again at execution time. This is what makes `reset` safe:
  reset is `rm -rf` on that path.
- **Never write outside `setup.root`** except where the level explicitly teaches a
  system path, such as `perm-03` using `/opt/atlas` and `/var/log/atlas`. When you
  do, teardown must clean it up precisely, and the golden test must prove it.
- **Never use an absolute path that could exist on a host**, and never a relative
  path that escapes with `..`.
- **Teardown must be idempotent** and safe to run when setup never completed. It
  runs before every setup.
- **No network access in setup. Ever.** Seeded generators or embedded assets only.
- If a level launches a background process, such as `proc-01` and its fake runaway
  indexer, **teardown must kill it**, and the golden test asserts no stray
  processes remain.

## Determinism

A learner in Mauritius on a Tuesday and CI at midnight must see byte-identical
starting state.

- **Explicit `touch -d` for every mtime the level depends on.** `find -mtime` levels
  are otherwise a coin flip depending on when they are played.
- **Seeded generators.** Same seed, same bytes, forever.
- **No `date`, no `$RANDOM`, no hostname, no locale-dependent sorting** in setup or
  in an expected value.
- Locale is pinned to `C.UTF-8` and `TZ=UTC` in the image. Do not rely on anything
  else.

## Checks: state, not syntax

**Verify what the learner achieved, not what they typed.** Checking syntax punishes
someone who found a better solution, and that is the fastest way to make a teaching
tool untrustworthy.

`command_matched` is legitimate only for:

- **bonus objectives**, which never block passing
- the handful of levels where the syntax genuinely **is** the lesson: `tee` in
  `pipe-04`, `find -exec` in `find-04`, `mkdir -p` in `files-01`
- **anti-pattern warnings** with `severity: warn`, such as catching a hardcoded
  answer

Before you tighten a check, ask: **does this reject a valid alternative solution?**
`awk` instead of `grep`, a loop instead of a pipeline, a script instead of a
one-liner. If the state is right, the learner is right.

Use `any_of` when several answers are genuinely correct.

## Writing on_fail

This is the single highest-leverage thing an author does. The learner reads it at
the moment they are most likely to give up.

- **Name what is wrong and what to look at.** Never "incorrect", never "try again",
  never "task failed".
- **Voice: a helpful senior colleague who can see their screen.**
- **Point at the next action**, not the answer.

```yaml
# BAD
on_fail: "report.txt is incorrect."

# GOOD
on_fail: >-
  report.txt is missing or the number is off. Are you searching *all* the files
  in logs/? Is your search case-insensitive?
```

For a `script` check, the failing script's stdout is appended to `on_fail`. Use it
for diagnostics a static check cannot produce.

## Hints

Three or four tiers, escalating, each with a cost shown before the learner commits:

1. **Nudge:** points at the concept without naming the command
2. **Concept:** names the tool and what it does
3. **Near-solution:** the shape, with a blank to fill
4. **Reveal:** `reveal_solution: true`

Two hints minimum. One hint is a cliff.

Do not skip the mechanical hint where one is needed. In `nav-04`, telling the
learner that `/` searches inside `man` and `q` quits is essential, because
beginners get trapped in `less` and panic, and that panic is a real drop-off point.

## Assets

- **Internally consistent with the answer.** If the level says 147, the log must
  contain exactly 147 matching lines. **Verify by running the solution, never by
  adjusting the check to match.**
- **In-world.** Meridian Logistics shipping manifests, delivery logs, `atlas`
  configs. The fiction costs nothing and turns "chmod exercise 4" into "get the
  deploy script running again".
- **LF line endings**, enforced twice: `.gitattributes` and the setup runner
  stripping `\r`.
- **No real secrets, and nothing a scanner would flag.** `find-02` deliberately
  plants a string shaped like a leaked API key. Keep it obviously synthetic.
- **Large fixtures are deliberate.** Sixty files before globs, four thousand lines
  before `head`. Feeling the painful way is the lesson.

## Design principles

1. **Every level teaches one new thing and reuses two old ones.** `script-01` needs
   the stderr knowledge from `pipe-02`. That circularity is what makes a curriculum
   feel designed rather than listed.
2. **Show the painful way before the good way.**
3. **Bosses have no numbered steps.** A broken system and "make it work", with
   multiple valid fixes that all pass.
4. **Beginner levels are winnable in under sixty seconds.** Confidence before
   difficulty.
5. **Turn mistakes into mechanics.** Destroying the level and having to `reset`
   earns an achievement. Punishing it would teach fear of the terminal, which is
   the exact thing this product exists to remove.

## Validator invariants

Enforced by `shellforge author validate`, so know them before you write:

| Rule | Why |
|---|---|
| Every check has a non-empty `on_fail` | Generic failure messages are the number one quality killer |
| Every `objectives[].id` has a matching check id, and the reverse | Keeps the checklist honest |
| `solution` present and non-empty | Golden tests need it |
| At least two hints | One hint is a cliff |
| `setup.root` under `/home/learner/` | Reset safety |
| No `source:` pointing outside the pack | Reproducibility |
| DAG acyclic, all `prerequisites` resolve | Unlock logic |
| At least one non-optional check | A level you cannot fail is not a level |

## When the golden test fails

The golden test asserts checks FAIL before the solution and PASS after.

- **Failing at "checks must FAIL before"** means the level passes before the
  learner does anything. Usually setup is creating the answer, or a check is too
  weak.
- **Failing at "checks must PASS after"** means the solution and the checks
  disagree. **The level is wrong.** Fix the solution or the asset. Do not touch the
  check to make it go green.
