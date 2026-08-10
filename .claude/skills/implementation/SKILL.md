---
name: implementation
description: The end-to-end workflow for implementing a task in Shellforge, from ticket to merged branch. Load at the start of any coding task in this repository, before writing the first line. Covers where code goes in the layer map, the order of work, which other skills to pull in, when to stop and ask, and the Definition of Done.
---

# Implementing a change

## Before you write anything

1. **Read `PROGRESS.md`.** This repository is young and most of what the design
   documents describe does not exist yet. Knowing what is actually built prevents
   half the wasted work.
2. **Find the ticket, or open one.** Branches are named `<kind>/issue-<number>` and
   there is no branch without a ticket. See the `github-workflow` skill.
3. **Locate the existing code** with the `code-navigation` skill. Do not assume a
   thing is missing until the tools say so.
4. **Decide which layer the change belongs in**, before writing. Putting code in
   the wrong layer is the most expensive mistake available here, because
   `internal/archtest` will reject it and the fix is a redesign, not a move.

```
L5  cmd/shellforge      CLI, output rendering
L4  internal/game       orchestrator, scoring, DAG, achievements, eventbus
L3  internal/content    pack loading and validation
    internal/verify     check registry and engine
L2  internal/pty        PTY multiplexer, OSC parser
    internal/journal    command journal, snapshots
L1  internal/runtime    Runtime interface plus docker and wsl implementations
    internal/sandbox    image spec, provisioning
L0  internal/store      sqlite
    internal/platform   OS probes, console modes, paths
    internal/doctor     preflight
```

Dependencies point downward only. If you find yourself wanting an upward import,
the answer is almost always an interface defined in the consuming package, or a
field added to `Runtime.Capabilities()`.

## Which skills to load

Match the work, do not load everything:

| Doing this | Load |
|---|---|
| Any Go code | `go-style` |
| Anything that deletes, unregisters, or resolves a destructive path | `destructive-safety` |
| Spawning a process, handling a pack path, verifying a download | `security` |
| Writing tests, or deciding what to test | `testing` |
| A level, an asset, or the pack | `level-authoring` |
| Touching the PTY, runtime, verify, store, or Windows specifics | `component-traps` |
| Any prose, error string, comment, or briefing | `writing-style` |
| Branch, issue, PR, commit | `github-workflow` |

## Order of work

**Write the test alongside the code, never "later".** Later does not arrive, and a
test written after the fact tests what the code does rather than what it should do.

1. Write the failing test that describes the behaviour.
2. Write the smallest implementation that satisfies it.
3. Wire it into the layer above only once it works in isolation.
4. Add the error paths, each with a remediation and a doc anchor.
5. Run the full gate before you even think about a commit.

For a runtime implementation specifically: **do not write new tests.** The contract
suite in `internal/runtime/runtimetest` already exists and adding `WslRuntime`
should mean writing zero new tests. If you find yourself writing runtime-specific
tests, the abstraction is wrong.

## Rules that bite most often

- **`context.Context` first parameter**, honoured, on anything touching a runtime,
  the filesystem, or a subprocess. Ctrl-C must leave no orphan containers.
- **Every user-facing error goes through `ux.Fail(op, err, remediation, docAnchor)`.**
  A bare Go error reaching the terminal is a bug. If you add a doc anchor, add the
  matching heading in `docs/05-troubleshooting.md` in the same commit, because CI
  checks it.
- **Subprocesses take an argv vector.** Never a string handed to a shell.
- **Checks never mutate.** If you are adding a check type, it reads and nothing
  else.
- **No new dependency without asking.** The approved list is in the `go-style`
  skill and it is short on purpose.
- **`// TODO(v0.2):`** is the sanctioned way to note a deliberate shortcut. Use it
  rather than quietly doing the simple thing and saying nothing.

## When to stop and ask

Stop and report rather than half-doing it. These are the situations that justify
interrupting:

- The task is materially larger than it looked. Say so early, not on day three.
- You need a dependency that is not on the approved list.
- The change would require an upward layer dependency.
- A level's `solution` does not satisfy its own checks. **Never loosen the check to
  make it pass.** Report it. That is how a level ends up accepting wrong answers.
- The clean fix would break the "you cannot hurt your computer" promise, even
  slightly.
- Two documents disagree and the right answer changes the design.

## Definition of Done

- [ ] Compiles clean; `go vet ./...` clean; `gofmt -s` applied
- [ ] `./scripts/check-punctuation.sh` passes
- [ ] `./scripts/check-links.sh` passes, if docs changed
- [ ] `go test ./...` and `go test -race ./...` pass
- [ ] `govulncheck ./...` and `gosec ./...` clean, or the finding is suppressed with
      a written justification
- [ ] `go test ./internal/archtest/...` passes
- [ ] Unit tests for logic; contract tests pass for runtime implementations
- [ ] Every new user-facing error has a remediation and a doc anchor that exists
- [ ] Every new subprocess call uses argv form and validates its identifiers
- [ ] Every new destructive path goes through the validated helper and has refusal
      tests
- [ ] No new dependency added without asking
- [ ] If it touches level format: `docs/LEVEL-FORMAT.md` updated in the same commit
- [ ] If it adds a check type: registered, tested, documented in the catalogue
- [ ] Manually exercised on Linux; if platform-specific, on Windows too
- [ ] `PROGRESS.md` updated if this changes what works

Run the whole thing with `make ci`, or `.\make.ps1 ci` on Windows.

## Self-review before opening the PR

Read your own diff against `CLAUDE.md` and report, without fixing yet:

- any layer dependency violation
- any new dependency
- any user-facing error lacking a remediation
- any check that could mutate state
- any place a level's checks could accept a wrong answer
- any operation that could reach outside the sandbox

Listing them by severity first, then fixing, catches more than fixing as you go.
