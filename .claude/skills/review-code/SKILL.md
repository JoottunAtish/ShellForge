---
name: review-code
description: A systematic audit pass over Shellforge code or a diff, hunting correctness bugs, security flaws, layer violations, and anything that could damage the learner's machine. Load when asked to review code, audit a change, self-review a diff before opening a pull request, or check whether something is safe to merge. For reviewing a pull request on GitHub specifically, load review-pr instead or as well.
---

# Reviewing Shellforge code

## What this review is actually for

This project runs a real shell on a beginner's laptop and tells them they cannot
break it. A review here is not a style pass. It is the last thing standing between
a path resolution bug and a student's coursework.

Review in the order below. It is ordered by consequence, so that if you run out of
attention the important passes have already happened.

## Pass 1: could this damage the user's machine

Load the `destructive-safety` skill and work through it properly. Do not skim this
pass because the diff "looks unrelated to deletion". Path helpers and configuration
changes reach deletion code from a distance.

Grep the diff for these and justify every hit:

```
os.RemoveAll        os.Remove          os.Rename        os.Truncate
os.Chmod            os.Chown           os.WriteFile
--unregister        --shutdown         .wslconfig
docker rm           docker volume rm   --privileged     --network host
-v /                --mount            sudo             runas
rm -rf
```

For each, answer:

- What is the worst path this resolves to if every input is empty or wrong?
- Is the destructive identifier a compile-time constant?
- Does a marker file confirm the target is ours before we destroy it?
- Is the prefix check performed **after** `filepath.Clean` and **after** resolving
  symlinks?
- Is there a refusal test, not just a success test?

**A destructive change with no refusal test is blocking, always.**

## Pass 2: security

Load the `security` skill. Concentrate on the three places this codebase handles
input it did not author: downloaded artifacts, content packs, and anything crossing
the host boundary.

- **Subprocesses:** `exec.CommandContext` with an argv vector. Any `sh -c` or
  `bash -c` with an interpolated value is blocking. The command name must be fixed.
- **Identifiers reaching an argv** are allowlist-validated and rejected, not
  sanitized.
- **Pack paths:** `source` inside the pack directory, `path` under `setup.root`,
  `setup.root` under `/home/learner/`. Traversal rejected before use.
- **Downloads:** sha256 verified **before** use. A verify-after-import is the same
  as no verification.
- **Secrets:** nothing writes the journal, the environment snapshot, or raw
  keystrokes anywhere they could leave the machine. No new `net/http` above L0.
- **SQL:** parameterized only. No `fmt.Sprintf` into a query, even for values that
  look internal.

## Pass 3: correctness

- **Error handling:** wrapped with `%w` and context. No error discarded with `_`
  without a same-line reason. No `panic` outside `main`.
- **Context:** first parameter, actually honoured, cancellation propagates. Ask
  specifically: on Ctrl-C, does this leave an orphan container or a half-imported
  distribution?
- **Concurrency:** the PTY layer is concurrent. Look for shared state without a
  mutex, a goroutine with no way to exit, and a channel that can block forever.
  `go test -race` catches some of this and not all of it.
- **Resource cleanup:** every `Open` has a `Close`, every raw terminal mode has a
  guaranteed restore on **every** exit path including panic. A `defer` that only
  runs on the happy path is not cleanup.
- **Partial failure:** if setup fails halfway, does teardown still leave a clean
  state? Setup is supposed to be transactional.
- **Off-by-one and boundary conditions** in the OSC parser specifically. Markers
  split across reads at every byte offset is the failure mode, and it shows up
  roughly one percent of the time, which means it will not show up in your manual
  test.

## Pass 4: architecture

- **Layer direction.** `internal/archtest` catches import edges, but it cannot
  catch a concept leaking. A Docker-shaped idea in `internal/game` passes the test
  and still violates the rule.
- **Is this in the right package?** A `util`-shaped addition is a design smell.
- **Interfaces defined in the consuming package**, not next to the implementation.
- **Did this add a dependency?** Check `go.mod` in the diff regardless of what the
  description says.

## Pass 5: the teaching product

Unique to this codebase, and easy to forget in a technical review.

- **Does every new user-facing error name what failed, why, and the next command?**
  A bare error reaching the terminal is blocking.
- **Does every new doc anchor have a heading in `docs/05-troubleshooting.md`?**
- **If a level's checks changed: could the new version reject a valid alternative
  solution?** This is the review question that matters most here. Checking state
  rather than syntax is the whole pedagogical stance, and a well-meaning tightening
  is how you punish a learner for being clever.
- **If a level's checks were loosened, why?** If it was to make the solution pass,
  that is backwards and it is blocking. The level is wrong, not the check.
- **Prose:** no "just", no "simply", no em dashes, no smart quotes.

## Pass 6: tests

- Does a test exist that **fails without this change**? A test that passes both
  before and after tests nothing.
- Are the refusal and error paths tested, or only the happy path?
- Do tests clean up their containers and distributions even on failure?
- Do tests require network access? They must not.
- For a check type: is there a purity test proving it does not mutate?

## Severity, and what blocks

| Severity | Examples |
|---|---|
| **Blocking** | Anything that could reach the host. Missing refusal test on a destructive path. Command string handed to a shell. Unverified download. A check that mutates. A level that accepts a wrong answer. A bare error reaching the terminal. |
| **Should fix** | Layer smell, missing test for an error path, a doc anchor with no heading, an unwrapped error. |
| **Suggestion** | Naming, structure, a simpler approach. |
| **Nit** | Cosmetic. Never block on one. |

## Reporting

List findings by severity, most severe first. For each: what is wrong, why it
matters, and the concrete fix. **Do not fix while reviewing.** Report first, then
fix in a separate pass, because a reviewer who is editing stops reading.

If you find nothing blocking, say so plainly rather than inventing a nit to look
thorough.

A finding you are unsure about is still worth reporting, labelled as uncertain. In
this codebase a false positive costs five minutes and a false negative can cost
somebody their files.
