---
name: go-style
description: Go formatting, naming, error handling, structure, and dependency rules for Shellforge. Load before writing or reviewing any Go code in this repository, when adding a package, when deciding how to name or wrap something, when a gofmt or go vet check fails, or when considering adding a dependency.
---

# Go style

Baseline is the standard Go toolchain plus the Google Go Style Guide. Where this
file is silent, follow the Google guide. Where they conflict, this file wins.

## Mechanical

- **Go 1.23 or newer.** Standard layout, no framework.
- **`gofmt -s` is not negotiable and not configurable.** CI fails on any
  unformatted file. Do not argue with the formatter.
- **No hard column limit.** Break lines for readability. Keep function signatures
  on one line; if the argument list is too long, that is a signal to introduce a
  parameter struct, not to wrap.
- **Import grouping:** standard library, then third party, then this module, with a
  blank line between groups. Never use the dot import form.
- **File names** are lowercase, with underscores only where the toolchain requires
  them: `_test.go`, `_windows.go`, `_linux.go`.

## Naming

- **Packages** are lowercase, single word, no underscores, no plurals. Never
  `util`, `common`, `helpers`, `base`, or `misc`. A package name that does not
  narrow the meaning of its contents is a design smell.
- **Initialisms keep consistent case:** `ID`, `URL`, `API`, `PTY`, `OSC`, `WSL`,
  `XP`. Write `levelID` not `levelId`, `PTYHandle` not `PtyHandle`.
- **Receivers** are one or two letters, consistent across every method on the type.
  Omit the name when unused.
- **No `Get` prefix on getters.** `Capabilities()`, not `GetCapabilities()`. Use
  `Fetch` or `Compute` when the call is expensive or can block.
- **Variable name length tracks scope.** `i` in a three-line loop is right; `i` as
  a package-level variable is not. Omit type words: `levels` not `levelSlice`.
- **Constants use MixedCaps**, never `SCREAMING_SNAKE`. Name them for their role,
  not their value.

## Errors

- **Wrap with context using `%w`:**
  `fmt.Errorf("provision wsl distro %q: %w", name, err)`. The message says what the
  code was trying to do, lowercase, no trailing punctuation.
- **Error strings are lowercase and unpunctuated** unless they begin with a proper
  noun or an exported identifier.
- **`error` is the last return value.** Return the `error` interface, not a
  concrete error type.
- **Handle the error first and return early.** No `else` after an error branch.
- **Never discard an error with `_`** without a same-line comment saying why it is
  safe.
- **Sentinel errors for control flow:** `ErrSandboxMissing`, `ErrLevelLocked`.
  Compare with `errors.Is`, type-assert with `errors.As`. Never string-match an
  error message.
- **User-facing errors go through `internal/platform/ux.Fail(op, err, remediation,
  docAnchor)`** so every failure carries a fix and a documentation link. This is
  non-negotiable number 6, and there is a test asserting it.
- **Never `panic` outside `main` and package initialization.** Panic is for
  genuinely impossible states. A malformed level YAML is not impossible.

## Structure

- **`context.Context` is the first parameter** of every function that touches a
  runtime, the filesystem, or a subprocess. Honour cancellation. Ctrl-C must always
  exit cleanly and leave no orphan containers or half-imported distros.
- **Never store a `context.Context` in a struct field.**
- **No global mutable state** except the logger. Configuration is passed in.
- **Accept interfaces, return concrete types.** Define the interface in the
  consuming package, not alongside the implementation.
- **Doc comments on every exported identifier**, as full sentences beginning with
  the name. Every package has a `doc.go` stating which layer it sits in and what it
  must not import.
- **Named result parameters** only when there are two or more results of the same
  type, or when the name documents required caller action. Never just to enable a
  naked return.

## The layer rule

Dependencies point downward only. The layer map is in `CLAUDE.md`. Enforced by
`internal/archtest`, which parses every import in the module.

`internal/game` must never import a Docker or WSL type. If you need backend
information at L4, add it to `Runtime.Capabilities()`. This is what keeps "drop WSL
support" a one-day decision rather than a rewrite.

To add a legitimate edge, edit the tables in `internal/archtest/layers_test.go` and
justify it in the commit message. Do not delete the test.

## Dependencies

Every dependency is a cross-compilation risk, a supply chain surface, and a thing
that can break the Windows build.

Approved set, and nothing else:

| Module | Purpose | Note |
|---|---|---|
| `spf13/cobra` | CLI structure | |
| `creack/pty` | PTY allocation | |
| `modernc.org/sqlite` | SQLite | **Prefer over `mattn/go-sqlite3`.** Pure Go, removes cgo from the release matrix, keeps cross-compilation trivial. |
| `goccy/go-yaml` | Level YAML | |
| `fatih/color` | Output styling | Must honour `NO_COLOR`. |
| `stretchr/testify` | Assertions | `require`, not `assert`, for anything that invalidates the rest of the test. |
| `golang.org/x/term` | Host raw mode and window size | Go team maintained golang.org/x module; approved for #10, the standard answer for raw mode since Go has no stdlib termios API. |
| `charmbracelet/glamour` | Level briefings, markdown to ANSI | Approved for #55. **Pin to v0.10.0.** v1.0.0 declares `go 1.24.0` and would raise this module's floor; v0.10.0 and its whole direct tree stay at or below `go 1.23.0`. Imported only by `cmd/shellforge`, never below L5. Use the project's own `briefingStyle`, not `WithStandardStyle`: every standard style leaves the `##` heading marker in the output, and `dark` inflates a 352 byte briefing to 5,698 bytes of per-space padding escapes. Honours `NO_COLOR` through `ux.ColorEnabled`, and emits no attributes at all, bold included, when colour is off. |

**Ask before adding anything else.** The answer is usually the standard library.

The scaffold currently has zero dependencies so that CI is green with no module
download. Day 1 Session A introduces cobra and creack/pty.

## Before every commit

```bash
gofmt -s -w . && go vet ./... && go test ./... && ./scripts/check-punctuation.sh
```
