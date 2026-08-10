# Progress

One line per working session. The rule from the build plan: every evening, commit,
push, get CI green, and add a line here. No silent carry-over. If an exit criterion
is unchecked the next morning, it either gets done before new work or it gets
formally cut.

**Current state: Day 0 complete. Day 1 started: the runtime interface exists, no backend behind it yet.**

---

## What actually works today

| Area | State |
|---|---|
| Repository scaffold | Done. Builds, vets, and tests clean. |
| `go build ./...` | Green |
| `go vet ./...` | Green |
| `go test ./...` | Green |
| Layer dependency enforcement | Done, and verified to fail on a deliberate violation |
| Punctuation gate | Done, and verified to fail on a deliberate violation |
| CLI dispatcher | Skeleton. Every verb is registered; `version` and `help` work, the rest report that they are not built yet. |
| Sandbox image | `Containerfile` written, not yet built |
| Shell instrumentation | `instrument.bash` written, not yet exercised |
| Content pack | `pack.yaml` with six acts declared. Zero levels written. |
| Runtimes | `Runtime` and `Session` interfaces plus their value types and sentinel errors are defined in `internal/runtime`. No backend implemented. |
| PTY multiplexer and OSC parser | Not started |
| Verification engine | Not started |
| Progress database | Not started |
| Documentation | Design record complete. User docs are outlines. |
| Engineering rules | `CLAUDE.md` index plus 13 on-demand skills under `.claude/skills/` |
| Link checker | Done, and verified to catch a broken relative link |
| Merge gate check | Done. `scripts/check-ci-gates.py` asserts no CI job can fail without blocking a merge |
| Action pinning gate | Done, and verified to fail on a deliberate tag pin. Covered by `scripts/tests/test_check_ci_gates.py` |
| Label taxonomy | Defined in `.github/labels.yml`, applied by `./scripts/sync-labels.sh` |
| Issue templates | Four forms, including a self-contained implementation ticket |
| MCP servers | `.mcp.json` auto-connects jCodemunch and jDocmunch |

**There is no release and nothing to install.**

---

## Log

### Day 0, 2026-08-10: setup

Repository initialised and structured. Nothing is running yet, which is correct for
Day 0.

Done:

- `.gitattributes` committed first, before any other file, per the plan mandate.
- Eight design documents restructured from a flat pile of numbered files into
  `CLAUDE.md` at the root, the two living contracts under `docs/`, and the design
  record under `docs/design/`.
- All typographic punctuation removed repository-wide: 269 substitutions across the
  eight documents. Em dashes, en dashes, curly quotes, and non-breaking spaces are
  now banned by Rule 0 in `CLAUDE.md` and enforced by
  `scripts/check-punctuation.sh` in CI.
- `CLAUDE.md` extended with the typography rule, a Go style section grounded in the
  Google Go Style Guide, and a security section covering the subprocess, path
  handling, and supply chain rules that this codebase actually needs.
- Go module `github.com/JoottunAtish/ShellForge`, Go 1.23 floor, zero dependencies.
  The scaffold builds against the standard library alone so CI is green from the
  first commit with no module download.
- Layer packages created with `doc.go` files stating each package's layer and what
  it must not import.
- `internal/archtest` enforces the layer rule by parsing every import in the module.
  Verified against a deliberate violation: it caught both the runtime
  implementation leak and a `net/http` import at the game layer.
- CLI skeleton with all twelve verbs from the brief registered, plus tests
  asserting the table stays honest.
- `Makefile` plus `make.ps1`, because `make` is not installed on a default Windows
  box and Windows is the primary persona.
- CI workflow: branch name gate, punctuation gate, CRLF gate, executable bit check,
  gofmt, module tidiness, build, vet, layer test, test, race, govulncheck, gosec,
  doc anchor check, link check, and a sandbox image build that asserts `man ls`
  works. Matrixed over Linux and Windows.
- Repository furniture: README, LICENSE, CONTRIBUTING, CODE_OF_CONDUCT, SECURITY,
  four issue forms, a pull request template, and Dependabot across Go modules,
  Actions, and the sandbox base image.
- **Engineering rules split for progressive disclosure.** `CLAUDE.md` is now a slim
  always-loaded index; the detail lives in 13 skills under `.claude/skills/` that
  load only when the task needs them: `implementation`, `ticket-creation`,
  `code-navigation`, `writing-style`, `go-style`, `destructive-safety`, `security`,
  `testing`, `component-traps`, `level-authoring`, `github-workflow`,
  `review-code`, `review-pr`.
- **`destructive-safety` is the one to read first.** It enumerates the five things
  that would be catastrophic on a learner's machine, with `wsl --unregister`
  targeting the wrong distribution at the top, and it makes refusal tests mandatory
  on every destructive path.
- Branch naming is `<kind>/issue-<number>` across features, bugs, and chores, and
  CI rejects anything else on a pull request.
- The issue label taxonomy is defined in `.github/labels.yml` and applied by
  `./scripts/sync-labels.sh`, so it is version controlled rather than invented in
  the web interface. No count is quoted here on purpose: the file is the source of
  truth and a number in prose goes stale the first time a label is added.
- `.mcp.json` auto-connects jCodemunch and jDocmunch for code and documentation
  navigation.

Environment notes for whoever picks this up:

- **Go is not installed on the development machine.** The scaffold was verified
  with a Go 1.26.5 toolchain fetched to a temporary directory. Install Go properly
  before Day 1.
- Docker is installed but the daemon was not running.
- WSL2 is present with a `Debian` distribution. That distribution is the
  developer's own and must stay untouched; the sandbox gets its own
  `shellforge-sandbox` distribution.

Not done, deliberately deferred to Day 1:

- No runtime implementation. No PTY. No container image built.
- `cobra` not yet added. The hand-rolled dispatcher exists only so that the
  repository builds and CI is green from commit one, and Session A replaces it.

### Day 0 follow-ups, 2026-08-10: CI supply chain

Landed after the scaffold, before Day 1 work started. No engine code touched.

- CI was red on the initial scaffold. Fixed by installing `vim` rather than
  `vim-tiny` in the sandbox image, and by giving the security job a current
  toolchain instead of the `go.mod` floor, because `govulncheck` v1.6.0 needs
  Go 1.25 and `setup-go` sets `GOTOOLCHAIN=local`. Issue #3.
- Every GitHub Action is now pinned to its commit SHA rather than a `@v7` major
  tag, with the version in a trailing comment. `SECURITY.md` already committed
  this project to treating the Actions in CI as a supply chain surface, and a
  mutable tag was the one place that line was not held. Issue #5.
- `scripts/check-ci-gates.py` grew a second job: it now also fails on any `uses:`
  that is not a 40 character lowercase SHA with a version comment, so the pinning
  does not decay the first time somebody adds a step. Verified against a
  deliberate tag pin. It has its own pytest suite under `scripts/tests/`, which is
  the first Python test in the repository and runs in the Style job.

### Day 1, 2026-08-10: the runtime interface

Issue #6. Types only, no behaviour, so nothing new runs yet.

- `internal/runtime` now declares `Runtime` and `Session`, plus `ImageSpec`,
  `SessionSpec`, `ExecOpts`, `ExecResult`, `AttachOpts`, `PTY`, `FileManifest`,
  `FileEntry`, `Caps`, and `Status`, and the three sentinel errors.
- `PTY` lives at L1 rather than in `internal/pty`, so that a runtime never has
  to import L2. `internal/pty` consumes the interface, it does not own it.
- `Exec` takes `argv []string` and there is no string form anywhere in the
  package. A test parses the package source and fails on an exported function
  or method regardless of receiver, or on a bare string parameter named like a
  command on any interface the package declares (not a hardcoded list of
  three), so a convenience overload cannot arrive quietly later.
- `Snapshot` and `Restore` were left off deliberately. Reset wipes the scratch
  directory on both backends and no caller needs them, so adding them now would
  only buy two implementations of `ErrNotSupported`.
- The allowlist requirement on `ImageSpec.Name`, on every `User` field, and on
  `FileEntry.Path` is in the doc comments, and a test walks every exported
  struct field by name so a new field called `Name`, `User`, or `Path` on a
  future type is covered automatically rather than by a hardcoded table. The
  allowlist regexp is `^[a-zA-Z0-9][a-zA-Z0-9_-]*$`, which additionally
  refuses a leading hyphen so a validated value cannot be mistaken for a flag.
  This is stricter than the regexp still quoted in the ticket and in the
  security skill; those need a follow-up so the three do not drift apart. The
  destroy path that will trust `ImageSpec.Name` has not been written yet, and
  `Destroy` is now documented as idempotent, matching `Provision`, so that
  path can call it without checking `Status` first.
- Sentinel error identity is now covered two ways: by position rather than by
  value in the wrapping test, so an accidental alias of one sentinel to
  another cannot hide by being skipped as `this test's own sentinel`, and by a
  direct pairwise comparison that fails on its own line if two sentinels are
  ever the same value.
- A reflect-based table pins the field count and the field types of every
  value type in the ticket, so a deleted field, a renamed field, or a retyped
  field (`fs.FileMode` silently becoming `uint32`, for example) fails even
  though nothing else in the package changed.
- Still zero dependencies. The package builds against the standard library
  alone.

---

## Day 1: the spike

> Goal: one hardcoded level, playable end to end, on Linux, with a real bash.

Exit criteria, from the build plan:

- [ ] `shellforge run demo` opens a real bash prompt inside a container
- [ ] `vim`, `less`, `htop` all render correctly; terminal resize works mid-session
- [ ] Ctrl-C interrupts a command without killing the game
- [ ] Every command is logged with `{cmd, exit_code, cwd, duration}`, and no OSC
      escape codes are visible to the user
- [ ] Typing `check` inside the shell reports pass or fail correctly
- [ ] `rm -rf /` inside the sandbox does nothing to the host

Go or no-go: if the PTY multiplexer and the OSC parser are not working by end of
day, stop and fix them on Day 2 morning. Do not proceed to content. There is no
fallback for that component.

## Day 2: content engine and verification

- [ ] Levels load from YAML with zero Go changes
- [ ] `author validate` catches duplicate id, cycle, missing asset, missing
      `on_fail`, unknown check type
- [ ] A failing check prints its authored `on_fail`, not a generic error
- [ ] Only the first failing required objective is shown

## Day 3: Windows, the highest-risk day

- [ ] Clean Windows 11 VM: `doctor`, `init`, `run nav-01` works
- [ ] `wsl -l -v` shows `shellforge-sandbox` and the user's own distros are untouched
- [ ] Inside the sandbox: `echo $PATH` is clean and `/mnt/c` does not exist
- [ ] `sandbox destroy` unregisters the distro and deletes the vhdx directory

**Gate at 18:00.** If `WslRuntime` is not working, cut it, switch Windows to
"requires Docker Desktop", and spend Day 4 on content. Do not let this bleed.

## Day 4: game core

- [ ] `shellforge play` auto-resumes at the correct next level
- [ ] XP persists across restarts and `map` shows locks correctly
- [ ] Passing a level prints XP earned, objectives hit, achievements unlocked
- [ ] Hints deduct XP and show the cost before confirming

## Day 5: content sprint

- [ ] 25 levels validate clean
- [ ] Whole campaign played start to finish in one sitting, friction noted
- [ ] Every act ends in a boss level that takes 10 minutes or more

## Day 6: hardening, CI, packaging

- [ ] CI green on both platforms
- [ ] A pre-release exists and `install.ps1` from it works on a clean VM
- [ ] Uninstall verified: no orphaned vhdx, no stray config

## Day 7: documentation and release

- [ ] Someone who has never seen the project installs and plays using only the README
- [ ] `v0.1.0` released with binaries and the rootfs artifact attached

---

## The cut ladder

When behind, cut in this order:

1. Levels 19 to 25 (Acts V and VI)
2. Achievements beyond three
3. Efficiency and first-try scoring bonuses
4. `WslRuntime`, falling back to Docker Desktop on Windows
5. macOS builds
6. The `author record` helper
7. Mutation tests

**Never cut:** doctor, install docs, golden tests, reset, sandbox isolation.

A game with 12 levels that installs cleanly and cannot hurt anyone beats a game with
25 levels that strands a beginner at step three.
