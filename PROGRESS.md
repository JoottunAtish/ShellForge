# Progress

One line per working session. The rule from the build plan: every evening, commit,
push, get CI green, and add a line here. No silent carry-over. If an exit criterion
is unchecked the next morning, it either gets done before new work or it gets
formally cut.

**Current state: Day 0 complete. Day 1 in progress: the runtime interface, the streaming OSC parser, and the Docker backend exist. No PTY multiplexer, and `WslRuntime` is not started.**

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
| Runtimes | `Runtime` and `Session` interfaces plus their value types and sentinel errors are defined in `internal/runtime`, the reusable contract suite is in `internal/runtime/runtimetest`, and `internal/runtime/docker` implements both by shelling out to the `docker` CLI. The contract suite is green against it on Windows with Docker Desktop's Linux engine, except one subtest documented below. `WslRuntime` is not started. |
| PTY multiplexer and OSC parser | Parser done: streaming OSC 133 and OSC 7 state machine, fuzzed, with a recorded vim session passing through byte-identical. Multiplexer not started. |
| Verification engine | Not started |
| Progress database | Not started |
| Documentation | Design record complete. User docs are outlines. |
| Engineering rules | `CLAUDE.md` index plus 13 on-demand skills under `.claude/skills/` |
| Link checker | Done, and verified to catch a broken relative link |
| Merge gate check | Done. `scripts/check-ci-gates.py` asserts no CI job can fail without blocking a merge |
| Action pinning gate | Done, and verified to fail on a deliberate tag pin. Covered by `scripts/tests/test_check_ci_gates.py` |
| Allowlist regexp gate | Done. After the #42 harvest it catches the anchored single-class form with a leading, trailing, or backslash-escaped hyphen under the `+`, `*`, and `{n,m}` quantifiers, in either case order, and reads git and grep exit status directly so a scan that could not run fails loudly rather than reporting clean. Covered by `scripts/tests/test_check_allowlist_regexp.py`. It still matches spellings, not every string a regexp engine would treat as equivalent. |
| Label taxonomy | Defined in `.github/labels.yml`, applied by `./scripts/sync-labels.sh` |
| Issue templates | Four forms, including a self-contained implementation ticket |
| MCP servers | `.mcp.json` auto-connects jCodemunch and jDocmunch |
| `internal/platform` (paths) | Tested. Every function in `paths.go` has at least one test. `DataDir` rejects a relative `XDG_DATA_HOME` instead of silently returning a relative path. The Windows `CacheDir`/`DataDir` collision (#40) is fixed: `CacheDir` nests a `cache` element below `DataDir`, so a cache clear cannot delete progress or the WSL disk image. |

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
  The security skill quoted a looser regexp when this landed; issue #19
  reconciled the two, and `scripts/check-allowlist-regexp.sh` now fails the
  build if the loose form reappears. The destroy path that will trust
  `ImageSpec.Name` has not been written yet, and `Destroy` is now documented as
  idempotent, matching `Provision`, so that path can call it without checking
  `Status` first.
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

### Day 1, 2026-08-10: the streaming OSC parser

Issue #7. First working code in `internal/pty`.

- `internal/pty` declares `Parser`, `NewParser`, `Write`, `Flush`, `Event`,
  `EventKind`, and `MaxOSCPayload`. It recognizes `133;A`, `133;B`, `133;C`,
  `133;D;<exit>`, and `7;file://<host><path>`, with both terminators, BEL and
  ESC backslash.
- A byte-level state machine over four states, not a regex over a buffer. Only
  the payload is buffered, capped at 4096 bytes, and a sequence that reaches the
  cap is abandoned with every held byte forwarded and nothing stripped.
- Only a fully parsed and recognized sequence is stripped. Everything else is
  forwarded byte for byte: window title sequences, CSI traffic, and any payload
  that starts with `133;` but names no subcommand we know.
- `Event.At` is stamped by the parser when the terminator byte is consumed. The
  clock is an unexported field so tests can pin it.
- `testdata/vim-session.bin` is 4601 bytes of real PTY master output from vim.
  It passes through with zero bytes added and zero removed. A test asserts its
  length, its 19 CRLF pairs, and its two OSC title sequences, so a line ending
  rewrite cannot change the fixture unnoticed. `.gitattributes` now marks
  `*.bin` binary for the same reason: git was only leaving those CRs alone
  because seven lone CRs made `text=auto` guess binary, which is an accident.
- `FuzzParser` runs for 30 s in CI as a step inside the Test job. Its
  invariants: the output is always a subsequence of the input, and feeding a
  stream one byte per `Write` produces the same bytes and the same events as
  feeding it whole.
- A learner can type a byte-identical forged marker and the parser cannot tell
  the difference, by construction. Nothing about scoring may trust a journal
  exit code; verification checks state. That needs a follow-up on the journal
  side before Act VI.
- No user-facing error path here, so no doc anchor. The multiplexer owns the
  user-facing wrapping.
- Still zero dependencies.

### Day 1 follow-ups, 2026-08-10: OSC parser review fixes

Three reviewers audited the parser branch. This pass applies the adjudicated
findings. No behavior change to decoding; the fixes are new rejections, a
tightened exit code bound, and test coverage.

- The OSC 7 wire-format mismatch between the parser and
  `images/rc/instrument.bash` is now documented in `recognize`'s comment and
  pinned with hostile test rows, not fixed. The parser percent-decodes per
  `url.PathUnescape`, which is correct: an OSC 7 payload is a file:// URI and a
  URI is percent-encoded by definition. `instrument.bash:68` still emits `$PWD`
  raw. Today, a path containing a bare `%` fails to decode and is forwarded as
  an unrecognized OSC instead of reported. **Cwd reporting does not work end to
  end for a path containing a literal `%`.** Fixing the producer is issue #25,
  not done here.
- `overflowWithHeldEsc` had zero test coverage: nothing in the suite called
  the exact path that reaches it, a payload at `MaxOSCPayload` with an ESC
  held mid-decision. Added, and verified against a deliberate regression
  before committing.
- The fuzz invariants only checked subsequence, which a byte-dropping parser
  could still satisfy. Added an identity check for ESC-free input and a
  byte-identical check whenever zero events fire.
- `CommandDone.ExitCode` accepted anything up to `MaxInt64`. A wait status is
  0-255, so anything larger is now rejected as unrecognized rather than
  reported.
- `Event.Cwd` reached the caller with no content validation: a percent escape
  or a raw smuggled ESC could put a C0 control byte or DEL into a path string
  handed to L3 and L4. Both routes are now rejected outright rather than
  sanitized.
- Pinned the `PathUnescape` over `QueryUnescape` decision with a test: a
  literal `+` in a path must stay a `+`, not become a space.
- Fixed a test that looked like it exercised the nil-writer substitution in
  `NewParser` but did not: it wrote only a fully recognized marker, which is
  stripped before ever reaching `p.out.Write`.
- Opened issue #26 (upload fuzz crashers as a CI artifact, needs a pinned
  Action SHA outside this session's reach) and issue #27 (re-capture the vim
  fixture inside the sandbox image, needs Docker). Neither is done here.
  Issue #22 tracks ratifying the ten Parser contract decisions from this
  branch; also not done here, adjudication only.

### Day 1, 2026-08-10: the runtime contract suite

Issue #8, PR #31. Test infrastructure only. There is still no backend, so the
suite does not yet run against a real sandbox; the Docker ticket is where it
first goes green.

- `internal/runtime/runtimetest` now defines `Factory`, `RunContract`, and the
  twelve exported contract assertions. A backend proves itself by calling
  `runtimetest.RunContract` and writing zero tests of its own. `WslRuntime` on
  Day 3 is expected to pass it unchanged.
- The suite only ever provisions and destroys `runtimetest.SandboxName`, which
  is `shellforge-contracttest`. This is the suite's only real guard against
  destroying the wrong sandbox: a `Factory` must return a test-only
  constructor whose compile-time destroy target is `SandboxName`, never a
  backend's production constructor. An earlier draft wrote a marker file
  before `Destroy` and read it back, and called that an identity check. It
  was not one: pushing a file and reading it back through the same `Session`
  proves round-tripping, already covered by `TestPullFileRoundTrips`, and
  nothing about which sandbox is on the other end. That mechanism is gone.
  Every assertion that provisions now calls `ensureNoSandbox` first
  (`Destroy`, then assert `Status().Provisioned == false`), which proves the
  assertion's own precondition instead of assuming it, but proves nothing
  about a sandbox this suite did not itself create. If a `Factory` is wired
  wrong, the sandbox is lost the moment `Destroy` is first called, same as
  before.
- Review also settled two open contract questions, recorded for issue #32:
  `Session` (`Exec`, `Attach`, `PushFiles`, `PullFile`) is safe for
  concurrent use, `Close` is not concurrent-safe with another in-flight call
  but is idempotent; and a successful `Provision` leaves the sandbox
  running, not merely created, so `StartSession` needs no separate start
  step.
- The package's own tests cover its plumbing rather than any backend: a
  skipping `Factory` must produce twelve skipped subtests and no failures,
  the dispatch table must list every exported assertion declared anywhere
  in the package's non-test files (not just `contract.go`), and no file in
  the package may import anything but the standard library and
  `internal/runtime` itself, `net/http` and `os/exec` included even though
  both are standard library.
- Four gaps in the interface surfaced and are recorded for issue #32 rather
  than worked around: no way to enumerate or count sandboxes, so "leaves
  exactly one sandbox" is only assertable as "the same sandbox still works"
  (F1); `Status` carrying no sandbox identity, the reason the destroy
  assertion can only prove its own precondition rather than verify identity
  (F2); `FileEntry` having no way to mark content binary, so the CR-stripping
  rule cannot be fully stated without risking asset corruption (F3); and
  `ImageSpec` having no way to say "use the backend's own default reference"
  (F4).

---

### Day 1, 2026-08-10: the argv allowlist regexp, reconciled

Issue #19. Documentation plus one new gate. No behaviour change and no new Go
code outside a test.

- `.claude/skills/security/SKILL.md` now quotes `^[a-zA-Z0-9][a-zA-Z0-9_-]*$`,
  the same regexp the doc comments in `internal/runtime` already required, and
  says why the first character must be alphanumeric: a value that can start
  with a hyphen is flag-shaped, and `docker` or `wsl.exe` would read it as an
  option once it reaches an argv vector.
- `scripts/check-allowlist-regexp.sh` is a new Style gate. It fails if the
  strict form goes missing from the security skill, and it fails if the loose
  one-class form comes back, anchored and spelled out with either letter case
  order (`a-zA-Z0-9_-` or `A-Za-z0-9_-`). Three places quoted the regexp and
  two had drifted; a gate costs less than noticing again in a month. Wired
  into `make lint`, `.\make.ps1 lint`, and the Style job. It does not catch
  every rewrite that a regexp engine would treat as equivalent: a different
  quantifier, an escaped hyphen, or the hyphen moved to a different position
  in the class all pass this gate. It catches the two spellings this repo
  actually had, not every spelling that could exist.
- Review found three problems with the first version of that gate, fixed in
  the same branch. It could report clean without having scanned anything at
  all: a `git ls-files` failure was swallowed by `|| true`, so a broken
  environment printed "allowlist regexp: consistent" and exited 0, having
  scanned zero files. That is a false green on a security gate. Its needle
  also matched the loose form as a bare substring anywhere, which flagged a
  level briefing teaching the same character class with no anchors; it is now
  anchored to the full caret-bracket-plus-dollar form. And its own claim of
  coverage was wider than the truth it now states above. All three are fixed
  and covered by `scripts/tests/test_check_allowlist_regexp.py`.
- The drift note in `internal/runtime/apishape_test.go` is gone. It described
  the disagreement as outstanding, and it no longer is. The constant it guards
  is unchanged, and a new table-driven test asserts the pattern refuses `-f`,
  `--force`, and a bare hyphen while still accepting `learner` and
  `core-linux-basics`.
- `SECURITY.md` and `.claude/skills/review-code/SKILL.md` still describe
  allowlist validation without quoting a regexp, on purpose. Two more copies
  would be two more things to drift.
- No shared validation helper yet. There is still no caller: the sandbox
  destroy path that will trust `ImageSpec.Name` is unwritten, and the helper
  plus its refusal tests belong to that ticket.

---

### Day 1, 2026-08-11: contract debt and the Windows path collision fixed

Issue #42, the consolidation of six tracking issues (#22, #23, #32, #38, #40,
#41). Decisions settled, one code fix, no new behaviour beyond `paths.go`.

- **Windows `CacheDir` and `DataDir` no longer collide (#40 fixed).** PR #39
  pinned this collision with `TestWindowsDataDirCollidesWithCacheDir`; this
  change removes that test and fixes the defect. `CacheDir` now nests a `cache`
  element one level below `DataDir`, so a cache clear can no longer reach the
  progress database or the WSL `.vhdx`. The resolution is factored into
  `windowsCacheDir` and `windowsDataDir` so it is guarded by a test that runs on
  Linux CI, not only behind a Windows build; `TestConfigCacheDataDirsAreDistinct`
  no longer skips on Windows, and a new test asserts `DatabasePath` never sits
  under `CacheDir`. The relative `XDG_DATA_HOME` rejection PR #39 added is
  preserved through the merge, still routed through `ux.Fail`.
- **The interface-binding runtime decisions moved into doc comments**:
  `ImageSpec.Reference` empty means the backend default (F4), `Session.Exec`
  states the cancellation and missing-binary contract (decisions 8 and 12), and
  `Status` records why it carries no identity field (F2).
- **The journal trust rule is now written down** in `internal/journal/doc.go`,
  `internal/verify/doc.go`, and the `component-traps` skill: journal contents
  are learner-influenced and never evidence for scoring, a learner can forge a
  byte-identical OSC 133 marker, checks decide from real state via
  `Session.Exec`, and the efficiency bonus is deliberately gameable. The
  `PROMPT_COMMAND` snapshot files stay legitimate check inputs.
- **The `git ls-files` blind spot is recorded** in the `testing` skill: a gate
  that scans tracked files cannot see an un-added file, so a pre-commit check
  passes and the post-commit run goes red.
- **The allowlist gate was harvested from the orphaned `chore/issue-19`
  (`9edea39`).** Its broader needle and its direct git/grep exit-status handling
  replaced the narrower merged version. Deleting that remote branch is blocked
  by this environment (a 403 on any non-designated push), so it awaits a
  maintainer; the tip SHA is recorded here so it stays recoverable.
- The four PR ratifications (#21, #31, #37, #39) are recorded on issue #42.

### Day 1, 2026-08-11: test internal/platform/paths.go

Issue #13. Test only, plus a small real fix the tests found. No new dependency.

- `internal/platform/paths_test.go` covers all six exported functions:
  `ConfigDir`, `CacheDir`, `DataDir`, `LogDir`, `DatabasePath`, `EnsureDir`.
  Same package as `paths.go`, matching the style of `internal/platform/ux`.
  Every test points `HOME` and both sets of Windows and XDG variables at a
  fresh `t.TempDir` root via `t.Setenv`, so no test can write to a real user
  directory and the suite runs the same on a CI box with no `HOME` set.
- Seven of the nine tests were green on arrival and are regression pins, not
  red-first drivers, one skips off Windows, and one is genuinely red. The one
  genuine red test found a real bug: `DataDir`
  hand-rolled its `XDG_DATA_HOME` branch and never checked
  `filepath.IsAbs`, so a relative value such as `XDG_DATA_HOME=relative/data`
  produced a relative `DataDir`, silently, with no error. `ConfigDir` and
  `CacheDir` already refuse the equivalent case, because they delegate to
  `os.UserConfigDir` and `os.UserCacheDir`, which both error on a relative
  XDG value. **`DataDir` now does the same: a relative `XDG_DATA_HOME` is a
  reported error, not a fallback and not a silently accepted relative path.**
  This is a behaviour change worth naming here explicitly, so it reads as an
  intentional fix rather than a surprise the next time someone sets
  `XDG_DATA_HOME` by hand and hits an error that did not exist before.
- The ticket also predicted a bug in the empty-string case
  (`XDG_DATA_HOME=""`). There is not one: `os.Getenv` already returns `""`
  for an unset variable, and the existing guard is `xdg != ""`, so unset and
  empty already took the same fallback path before this change. Kept as a
  regression pin against a future refactor to `os.LookupEnv`, which would
  reintroduce exactly that bug.
- Resolved one contract ambiguity in the ticket's own acceptance criteria,
  flagged by an outside contributor's comment on the issue and never
  answered there: `LogDir` keeps `logs` as its final path element rather
  than `shellforge`, and is tested by containment under `CacheDir`
  (`CacheDir()/logs`), not by the literal "ends in shellforge" wording that
  applies to `ConfigDir`, `CacheDir`, and `DataDir`.
- Corrected a stale doc comment while in the file: `CacheDir`'s comment
  claimed Windows resolves to `%LocalAppData%\shellforge\cache`; the code
  has always returned `%LocalAppData%\shellforge`. The comment now matches
  the code.
- Found and pinned, not fixed, because fixing it relocates a directory and
  is out of this ticket's scope: on Windows, `DataDir` and `CacheDir`
  resolve to the identical path, so the progress database currently lives
  inside what the rest of the codebase calls the cache directory. A future
  "clear the cache" operation written against `CacheDir` would delete the
  learner's progress along with it. Pinned by
  `TestWindowsDataDirCollidesWithCacheDir`, skipped on non-Windows. Tracked as
  issue #40, to be resolved before the Day 6 uninstall and cache-clearing
  work lands. Issue #41 tracks ratifying this ticket's four contract
  decisions (the relative `XDG_DATA_HOME` error, the `LogDir` containment
  reading, the empty-string non-bug, and this Windows collision).
- The relative `XDG_DATA_HOME` error now goes through
  `internal/platform/ux.Fail`, with an empty `DocAnchor` so
  `docs/05-troubleshooting.md` needs no new heading. `paths.go` had no
  callers yet and none of its six pre-existing error paths went through
  `ux.Fail` either, but a bare `fmt.Errorf` here meant `ux.Render` would tell
  a learner with a one-line misconfiguration that this was our bug rather
  than theirs, which non-negotiable number 6 in `CLAUDE.md` rules out.
- `govulncheck`, `gosec`, and `python3 -m pytest scripts/tests -q` are not
  installed in this environment and were not run locally. CI runs all
  three and is authoritative.

### Day 1, 2026-08-11: DockerRuntime, via the docker CLI

Issue #9. First backend behind `internal/runtime`. Adds `github.com/creack/pty`
as a dependency, the first one this module has taken; `golang.org/x/term` and
`spf13/cobra` also landed in `go.mod` for #10 and #12 to pick up, but neither
is imported yet.

- `internal/runtime/docker` implements `Runtime` and `Session` entirely by
  shelling out to `docker` with `exec.CommandContext` and an argv vector.
  There is no `sh -c` anywhere in the package; a table test asserts the exact
  argv for `Provision`, `Destroy`, `Exec`, and `PullFile`.
- `New(name, image string)` allowlist-validates both before either can reach
  an argv, rejecting rather than sanitizing. `name` (the container identity)
  uses the strict identifier regexp already established for this codebase,
  `^[a-zA-Z0-9][a-zA-Z0-9_-]*$`. `image` needs its own, wider pattern,
  `^[a-zA-Z0-9][a-zA-Z0-9/_.:@-]*$`, because a real image reference needs
  `/`, `.`, `:`, and `@` for a registry host, a tag, and a digest; the first
  character stays restricted to alphanumeric, which is the property that
  actually defeats argument injection. **Contract decision, not ratified
  yet:** the ticket's own acceptance criterion named
  `^[a-zA-Z0-9_.:-]+$` for both name and image, which is a single character
  class ending in a literal hyphen. That shape is exactly what
  `scripts/check-allowlist-regexp.sh` exists to catch for identifiers: it
  admits a leading hyphen and reproduces the argument-injection flaw the
  strict two-class form was written to close. Using it verbatim would have
  shipped that flaw. Needs a follow-up issue to reconcile the ticket text.
- **Provision creates and starts the container, not just the image.**
  The ticket's own mapping table shows `docker run` under `StartSession`, but
  `runtime.Runtime.Provision`'s doc comment and
  `TestStatusReportsProvisioned` both require `Status` to report `Running`
  true immediately after `Provision`, with no separate start step. Followed
  the interface and the contract suite, per CLAUDE.md's rule that the code is
  authoritative over ticket prose when the two disagree. `StartSession` now
  only checks that promise held; it runs nothing.
- `ImageSpec.Name` is the authority for which image `Provision` builds,
  falling back to the image `New` was constructed with when empty. This is
  what lets the contract suite provision `shellforge-contracttest` as both
  the container name and the image tag without colliding with a production
  image.
- **`--cap-drop ALL` also drops `CAP_CHOWN` from root**, discovered by
  running the contract suite live: `PushFiles`' owner-application step
  failed with "Operation not permitted" on every case. Fixed by adding back
  `CAP_CHOWN` and `CAP_FOWNER` specifically, which is the security skill's
  own rule for this ("drop all capabilities, add back only what a level
  provably needs") applied to a need every level has, not a specific one.
- **A cancelled `Exec` left the sandbox-side process running**, also found
  by running the contract suite live, not by unit tests. `exec.CommandContext`
  cancels by sending the local `docker` client SIGKILL, which it cannot
  catch, so `docker exec`'s own `--sig-proxy` (default true, non-tty) never
  gets a chance to forward anything into the container. Sending SIGTERM
  instead, via `cmd.Cancel`, lets the client proxy it in on Linux and macOS.
  Confirmed why this cannot be verified on this Windows dev box: a two-line
  repro shows `os.Process.Signal(syscall.SIGTERM)` returns "not supported by
  windows" for a plain child process, so `TestDockerContract/ExecHonoursContextCancellation/cancel_aborts`
  fails here specifically and is expected to pass on CI's `ubuntu-latest`
  job, where `docker exec` runs natively rather than through Docker
  Desktop's Windows-to-Linux-VM bridge.
- `PushFiles` stages every file into one host temp directory under
  `os.MkdirTemp`, mirroring each entry's absolute sandbox path, so one
  `docker cp` of the staging root onto the container's `/` places everything
  correctly, then applies mode and owner with one follow-up `Exec` running
  as root. `FileEntry.Owner` is not independently allowlisted by the
  interface's own doc comments but reaches an in-sandbox shell script this
  code assembles, so it is validated here (`user` or `user:group`) before
  that happens, and every value is also single-quoted as a second,
  independent layer.
- `PullFile` reads the tar stream `docker cp <name>:<path> -` writes to
  stdout with `archive/tar`. Nothing here shells out to `tar`.
- **Found, not fixed, out of this ticket's scope:** the shared contract
  suite's `TestPullFileRoundTrips/utf-8_multibyte` subtest fails against
  this backend, and would fail against any backend: its own table case name
  contains a hyphen, and `runtimetest`'s `sanitizeName` helper refuses a
  hyphen. This is a bug in `internal/runtime/runtimetest/contract.go`, which
  this ticket's file list does not include. Needs a follow-up issue.
- `docker-not-found`, `docker-daemon-down`, and `docker-permission-denied`
  already had headings in `docs/05-troubleshooting.md` from Day 0; no new
  anchor needed. `sandbox-missing` also already existed and is not
  re-wrapped here: `StartSession` returns a plain wrapped
  `runtime.ErrSandboxMissing`, matching `internal/runtime/errors.go`'s own
  documented pattern of leaving the `ux.Fail` conversion to the caller that
  knows which command the user ran; only the Docker-specific environment
  failures (binary missing, daemon down, permission denied) are wrapped
  here, because nothing above this package could discover those.
- Gates run locally on Windows: `gofmt -s -w .`, `go vet ./...`,
  `go test ./...` (green except the one documented Windows-only subtest),
  `go test -race ./...` (green, run with that subtest excluded),
  `go test ./internal/archtest/...`, `./scripts/check-punctuation.sh`,
  `./scripts/check-allowlist-regexp.sh`, `./scripts/check-links.sh`. Not run:
  `python3 scripts/check-ci-gates.py` and `python3 -m pytest scripts/tests -q`
  (no `python3` in this environment), `govulncheck ./...` and `gosec ./...`
  (neither installed). CI is authoritative for all four.

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
