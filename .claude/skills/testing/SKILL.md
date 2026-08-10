---
name: testing
description: Testing rules and required test types for Shellforge, including the runtime contract suite, golden tests for levels, check purity tests, mutation tests, refusal tests for destructive paths, and fuzzing the OSC parser. Load when writing tests, deciding what to test, reviewing test coverage, or when a test is failing and you are tempted to change the assertion.
---

# Testing

## The rule that matters most

**Never change an assertion to make a test pass.** Work out why it fails first. In
this codebase that mistake has a specific catastrophic form: a level's `solution`
does not satisfy its checks, so somebody loosens the check. Now the level accepts
wrong answers and teaches the wrong thing, silently, forever.

If the solution does not satisfy the check, the level is wrong. Report it and stop.

## Baseline

- **Table-driven by default.** Name each case; the name appears in the failure.
- **`testify/require`** for anything that invalidates the rest of the test,
  `assert` only where the test can meaningfully continue. The scaffold currently
  uses the standard library because testify is not a dependency yet.
- **Write the test alongside the code, never "later".** Later does not arrive, and
  a test written afterwards tests what the code does rather than what it should do.
- **A test that passes before and after your change tests nothing.** Verify it
  fails without the change. This is the single highest-value habit here.
- **No network access in any test.** Not for a module, not for an image, not for a
  fixture.
- **Tests clean up their containers and distributions even on failure.** Use
  `t.Cleanup`, not a deferred call that a `t.Fatal` skips.
- **`go test -race ./...` is part of the gate.** The PTY layer is concurrent by
  nature.

## The test types this project requires

### Runtime contract suite

Written **once**, in `internal/runtime/runtimetest`, and run against every
implementation. Adding `WslRuntime` should mean writing zero new tests.

If you find yourself writing runtime-specific tests, the abstraction is leaking.
Fix the abstraction rather than the test.

### Golden test, one per level

Runs in CI on every pull request. The contract:

1. Fresh sandbox, run `setup`.
2. Run checks. **All required checks must FAIL.** If a level passes before the
   learner has done anything, the checks are wrong. This step catches more real
   bugs than step 4.
3. Run `solution` as `learner` in a login shell.
4. Run checks. **All required checks must PASS.**
5. Run `teardown`. `setup.root` is gone and no stray processes remain.
6. Run checks twice, hash the filesystem before and after. **Identical.**

### Check purity

Step 6 above, as a standalone test across the whole check catalogue. A check that
mutates sandbox state is a bug. Hash the filesystem, run every check twice, diff.

### Mutation tests

For the strict levels, feed known near-misses and assert the level **rejects**
each:

- the right answer in the wrong directory
- a hardcoded literal instead of a computed result
- wrong case
- the correct file left in the wrong place
- an empty file that happens to satisfy a weak `contains` check

This is what catches an over-permissive check, and over-permissive checks are the
quiet failure mode of a teaching tool. Required for `pipe-05`, `find-04`,
`perm-01`, `files-04`, and `script-01` at minimum.

### Refusal tests for destructive paths

**Mandatory. A destructive code path is not done without them.** See the
`destructive-safety` skill.

Test that the operation **refuses** for: an empty path, `/`, `~`,
`/home/learner` itself, a `..` traversal, a symlink escaping the root, a host
filesystem path, and a distribution name that is not ours.

The success path for deletion is the easy half. The refusals are what protect the
student.

### Fuzzing

The OSC parser consumes an adversarial byte stream by definition and must have a
fuzz target seeded with real captures.

### PTY golden tests

Feed scripted keystrokes, assert the resulting journal entries. Include a recorded
`vim` session and a `less` session to prove full-screen applications survive
byte-identical apart from our markers. Include markers split across `Read`
boundaries **at every byte offset**, because that is the real failure mode and it
shows up around one percent of the time, which is exactly often enough to reach
users and exactly rare enough to escape a manual test.

### Layer test

`internal/archtest` parses every import in the module and fails on an upward edge,
a runtime implementation leaking above L1, or a network client at or above L3. Do
not delete it. To add a legitimate edge, edit its tables and justify it in the
commit message.

### Doc anchor test

Every `DocAnchor` emitted by Go code must have a matching heading in
`docs/05-troubleshooting.md`. A diagnostic that links to a page nobody wrote is
worse than no link, because the learner follows it and lands nowhere.

## What to test, and what not to

**Test:**

- Error and refusal paths, which are the ones nobody exercises by hand
- Boundaries: empty input, one element, the split-across-reads case
- Anything a comment describes as tricky
- Anything that was once a bug, as a regression test

**Do not test:**

- The standard library
- `gofmt` output
- Trivial getters
- Exact wording of a message, unless the wording is the contract. Assert that the
  remediation is non-empty, not that it reads a particular way, or every copy edit
  breaks the suite.

## Running

```bash
make test        # go test ./...
make race        # go test -race ./...
make golden      # author test across every level
make ci          # everything
```

On Windows, `.\make.ps1 <target>`.
