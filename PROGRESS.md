# Progress

One line per working session. The rule from the build plan: every evening, commit,
push, get CI green, and add a line here. No silent carry-over. If an exit criterion
is unchecked the next morning, it either gets done before new work or it gets
formally cut.

**Current state: Day 2 complete on Linux, and now exercised against a real container in CI. `shellforge run <level-id>` plays any of the nine written levels from the embedded YAML pack with zero Go changes between them: it renders the briefing, prints the objective checklist, hands over a real instrumented bash, and answers `check` with authored results through the existing control FIFO. The verification engine, composition nodes, result assembly and the golden test contract are all built and unit tested. The golden contract has run against all nine levels in CI's Sandbox image job, which is the first thing to actually play a level, and finding real bugs is exactly what it did: two levels whose setup script silently never created a directory, one level whose expected answer disagreed with the directory the shell started in, and one whose five timestamp commands could each have failed unnoticed. All four are fixed. Still NOT verified from a developer machine: every Docker-gated test, because the environment that built this had no Docker socket, so CI rather than a person is the witness for anything end to end. Scoring, XP, hints, `reset`, `map`, `stats`, `play` and journal wiring are all Day 4 and deliberately absent. Windows still plays from inside WSL until Day 3.**

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
| CLI dispatcher | `cmd/shellforge` runs on `spf13/cobra` as of #48, not the hand-rolled dispatcher. Fourteen verbs registered (`doctor` `init` `play` `run` `check` `hint` `reset` `skip` `map` `stats` `sandbox` `bug-report` `author` `version`), plus `sandbox` and `author` as command groups with four stub subcommands each. `version`, `help`, `run demo`, `author validate`, `author test`, and `doctor` (issue #70, see its own row below) work; every other verb fails through `ux.Fail` with a remediation. **The whole `cmd/shellforge` package was untracked by git until #11**: `.gitignore`'s unanchored `shellforge` pattern matched the directory, so it was never committed and CI never compiled or tested it. |
| `shellforge run <level-id>` | Works on Linux for any level in the pack, with zero Go changes between levels. Loads from the embedded YAML, renders the briefing through glamour, prints the objective checklist, provisions, materializes the level, hands over a real instrumented bash, serves `check` over the existing FIFO control channel, and tears the level world down on every exit path through one cleanup function. An unknown id fails through `ux.Fail` naming the ids that exist. Refuses on a Windows host with the `windows-needs-wsl` anchor. **Never run against a real container from a developer machine**: no Docker socket here, so every end-to-end claim rests on unit tests plus CI's golden run. The learner's shell starts in `/home/learner`, not the level root, which is what a login shell does and what nav-01 teaches. |
| `shellforge run demo` | Still works, deliberately. The Day 1 hardcoded level is kept until a YAML isolation test and the golden harness have replaced the safety coverage `internal/sandbox/demo_golden_test.go` carries, which is the repository's only live host-isolation test. Tracked as a follow-up. |
| `shellforge author test` | Done. Runs the `docs/LEVEL-FORMAT.md` section 7 golden contract per level and is what `make golden` calls, a target that had been calling a command that did not exist since Day 0. Refuses rather than skips without a Docker daemon, because a `make golden` reporting success having tested nothing is worse than no gate. |
| Sandbox image | `Containerfile` written, and built by CI's Sandbox image job on every run. Not built on a developer machine here: no Docker socket in this environment. |
| Shell instrumentation | `instrument.bash` written, and exercised by every learner shell CI's golden run provisions, including the missing-SF_STATE recovery path added in this PR. Not exercised on a developer machine here, for the same reason. |
| Content pack | `pack.yaml` with six acts declared, and nine levels written: `nav-01` to `nav-04`, `files-01` to `files-04` (issue #54), and `pipe-05`. They validate clean, are embedded via `packs/packs.go`, and are all reachable from `shellforge run`. `pipe-05` is out of curriculum order on purpose: it is the engine's reference fixture, the level `docs/LEVEL-FORMAT.md` section 6 is written against, and the first whose world comes from committed assets rather than inline content. The golden contract has run all nine against a real container in CI's Sandbox image job, which found and fixed four real bugs (see the current-state line above); no developer machine here has a Docker socket, so CI rather than a person is the witness. Levels 9 to 25 are Day 5. |
| Level assets | First three committed: `assets/app-1.log`, `app-2.log`, `billing.log`, for pipe-05. Produced by the deterministic generator in `internal/sandbox/demo_level.go` rather than hand written, so their numbers came from code that already had consistency tests. The durable guarantee is `internal/content/pipe05_assets_test.go`, not the generator, which is slated for deletion under #96: five tests count the answers out of the committed bytes the way the level's solution counts them, including one that catches a noise line matching `error` case-insensitively without being an ERROR record. |
| `internal/game` | A thin `Session`: load, setup, brief, check, teardown, and nothing else. It declares its own `Verifier` interface so it is testable with a two-method fake, borrows the `runtime.Session` it is given and never closes it, and holds the only `content.CheckSpec` to `verify.Spec` conversion, which has to sit above both peers. No event bus, no scoring, no unlock state, no store writes: all Day 4. |
| Pack loading and validation | Done in `internal/content` (issue #53). `LoadPack`, `Embedded`, `Pack.Level`, `Pack.Order`, and `Validate` with a `TypeChecker` the caller supplies, so `internal/content` and `internal/verify` stay peers rather than one importing the other. `shellforge author validate <pack>` reports every problem one per line and supports `--json`. A legal `command_matched` or `command_not_matched` check now gets a warning naming issue #88: no runtime session wires a real journal yet, so the check verifies nothing until then, and the validator says so rather than staying quiet. |
| Level setup and teardown runner | Done in `internal/content/setup` (issue #50). `Runner.Setup`, `Teardown`, and `IsSetUp` materialize and remove a level's world inside the sandbox: teardown-first idempotency, a `loglines` content generator behind a registered kind, CRLF stripping on the host side before a `runtime.FileEntry` is built, rollback on any failure via `context.WithoutCancel`, and a `SETUP_OK` sentinel written under the state directory rather than the level root. Not wired into the game orchestrator or the CLI: no caller constructs a `Runner` yet outside its own tests. That wiring, plus the pack loader and validator that produce a real `content.Level`, is #52, #53, and #54. |
| Runtimes | `Runtime` and `Session` interfaces plus their value types and sentinel errors are defined in `internal/runtime`, the reusable contract suite is in `internal/runtime/runtimetest`, and `internal/runtime/docker` implements both by shelling out to the `docker` CLI. The contract suite is green against it on Windows with Docker Desktop's Linux engine, except one subtest documented below. `internal/runtime/wsl` (issue #69) now implements both by shelling out to `wsl.exe`: `New`, `Provision`, `Destroy`, `Status`, `StartSession`, `Capabilities`, and a `Session` with `Exec`, `Attach`, `PushFiles`, `PullFile`. The UTF-16LE decoder, the seven install directory refusals, both Destroy name refusals, the marker check, the enumerate-and-diff guard, the digest and name-collision refusals, and every argv construction are asserted and green on Linux CI. The contract suite wired against it (`TestWslContract`) skips everywhere this run and CI can reach: no `wsl.exe`, no Windows, and no WSL2 on either CI leg. A human on real Windows 11 with WSL2 still owes the thirteen contract assertions passing for real, the hardening probes seeing a genuinely imported distribution, and the install directory (`.vhdx` included) actually gone after `Destroy`, confirmed in Explorer. See the Day 3 entry below for the full list of what is asserted in code versus what still needs that human. |
| PTY multiplexer and OSC parser | Both done. Parser: streaming OSC 133 and OSC 7 state machine, fuzzed, with a recorded vim session passing through byte-identical. Multiplexer (`internal/pty/mux.go`): host stdin forwarded to the sandbox verbatim including Ctrl-C, host terminal raw mode restored across every exit path including a panic, initial resize plus SIGWINCH on unix, and CommandEvent assembly from the marker stream. `CommandEvent.Raw` is always empty pending #51. Windows resize watching (issue #68) polls `GetConsoleScreenBufferInfo` through the same injectable `getSize`/`resize` fields the unix watcher uses, every 250ms by default, and forwards a change the same way SIGWINCH does on unix. |
| Verification engine | Done in `internal/verify` (issue #52). `Engine`, `NewEngine`, `WithCheckTimeout`, `WithLevelTimeout`, `Build` and `Run`, the `any_of`/`all_of`/`not` composition nodes, and `LevelResult` matching `docs/LEVEL-FORMAT.md` section 5 field for field. Checks are built once at level load and run on every `check`. 262 tests and subtests. The hermetic half of the purity guarantee is `internal/verify/purity_test.go`, which asserts every check type runs only read-only commands; the filesystem-hash half is in the golden harness and needs Docker. |
| Progress database | `internal/store` (schema, migrations) and `internal/journal` (the command journal) are both built and unit tested, per #51. Nothing calls `store.Open` outside their own tests: not wired into `cmd/shellforge`, `internal/game`, or `internal/pty`. `CommandEvent.Raw` on the host-side event stream is still always empty. Issues #92 and #90 closed two `Open` classification bugs: a missing progress database file, or one whose parent directory does not exist yet, no longer reads as corrupt, and a SQLite database Shellforge did not create is refused rather than silently adopted. See the Day 3 follow-up entry below for the byte-identity measurement this forced and the fixture change it required. |
| Documentation | Design record complete. User docs are outlines. |
| Engineering rules | `CLAUDE.md` index plus 13 on-demand skills under `.claude/skills/` |
| Link checker | Done, and verified to catch a broken relative link |
| Merge gate check | Done. `scripts/check-ci-gates.py` asserts no CI job can fail without blocking a merge |
| Action pinning gate | Done, and verified to fail on a deliberate tag pin. Covered by `scripts/tests/test_check_ci_gates.py` |
| Allowlist regexp gate | Done. After the #42 harvest it catches the anchored single-class form with a leading, trailing, or backslash-escaped hyphen under the `+`, `*`, and `{n,m}` quantifiers, in either case order, and reads git and grep exit status directly so a scan that could not run fails loudly rather than reporting clean. Covered by `scripts/tests/test_check_allowlist_regexp.py`. It still matches spellings, not every string a regexp engine would treat as equivalent. |
| CLI entry point gate | Done. `scripts/check-cli-package.sh` fails if `cmd/shellforge` is missing or empty, if a `.go` file under `cmd/` is untracked, if any `.go` file anywhere in the repository is matched by a gitignore rule, or if no file under `cmd/shellforge` declares `func main()`. Wired into `make lint`, `.\make.ps1 lint`, and the style job. The `test` job also runs `make build` on Linux and `.\make.ps1 build` on Windows and checks the resulting binary runs `version`. Covered by `scripts/tests/test_check_cli_package.py`, including the exact #11 reproduction: an unanchored `shellforge` gitignore line plus a file on disk. |
| Label taxonomy | Defined in `.github/labels.yml`, applied by `./scripts/sync-labels.sh` |
| Issue templates | Four forms, including a self-contained implementation ticket |
| MCP servers | `.mcp.json` auto-connects jCodemunch and jDocmunch |
| `internal/platform` (paths) | Tested. Every function in `paths.go` has at least one test. `DataDir` rejects a relative `XDG_DATA_HOME` instead of silently returning a relative path. The Windows `CacheDir`/`DataDir` collision (#40) is fixed: `CacheDir` nests a `cache` element below `DataDir`, so a cache clear cannot delete progress or the WSL disk image. |
| `internal/platform` (console mode, issue #68) | Done. `EnableVirtualTerminal`, `SupportsVirtualTerminal`, and `IsWindowsTerminal` in `console.go`, `console_windows.go` (`golang.org/x/sys/windows`), and `console_other.go`. On Windows, `EnableVirtualTerminal` sets `ENABLE_VIRTUAL_TERMINAL_PROCESSING`/`ENABLE_VIRTUAL_TERMINAL_INPUT` and its restore reverts both modes exactly, idempotently; on every other platform both are no-ops. Not yet wired into `main`: that is the CLI ticket's job, out of scope here. Manual confirmation on real Windows 11 hardware, that resizing mid-`vim` no longer corrupts the display, is still outstanding and belongs in the pull request per the ticket's own acceptance criteria; this environment has no Windows console to verify against, only the Linux and cross-compiled Windows builds and the automated suite. |
| Rootfs release artifact | CI, `make rootfs`, and `.\make.ps1 rootfs` now all produce `rootfs.tar.gz` plus a `sha256sum`-format sidecar with reproducible `gzip -9 -n`, CI uploads both as a build artifact on every run and attaches them to the GitHub release on a tag push, and CI asserts the tarball contains `/etc/wsl.conf`, `/home/learner`, `/opt/shellforge/bin/check`, `/opt/shellforge/.sandbox-id`, and a man page. Not verified here: this environment has no Docker daemon, so the whole `image` job and the two scripts' actual output rest on CI, and the cross-platform digest match between a Linux runner's gzip and Git for Windows' gzip is expected but unproven pending a manual check on both platforms. |
| `shellforge doctor` (issue #70) | Done. `internal/doctor` (L0) implements the twelve probes from the ticket's table (`os_version`, `cpu_virtualization`, `vm_platform_feature`, `wsl_installed`, `wsl_version_2`, `wsl_kernel_current`, `docker_present`, `docker_daemon_running`, `disk_free`, `terminal_vt_support`, `windows_terminal`, `sandbox_health`), a table and `--json` renderer, and `--fix`, wired in at `cmd/shellforge/cmd_doctor.go` in place of the old stub. Windows-only probes are omitted, not failed, on Linux and macOS. Every result carries a non-empty id, status, detail, remediation, and doc anchor, including passing rows. Exit status is 0 unless a probe is `Fail`; a `Warn`-only report exits 0. `--fix`'s entire allowlist is one call, `platform.EnsureDir(platform.DataDir())`: confirmed by hand on this host, it creates exactly one directory at mode 0700 and nothing else, and refuses the three remediations it does not own. Verified by hand: `shellforge doctor`, `--json`, and `--fix` all ran against this container's real, broken machine (no CPU virtualization flags exposed, Docker daemon down) and reported exactly that, with the right exit codes. `internal/doctor/no_removal_test.go` and `internal/doctor/purity_test.go` were each confirmed to go red against a deliberate one-line violation, then reverted, before being trusted; that confirmation is recorded below. `sandbox_health` is wired with a `nil` `SandboxProber`, so it always reports `Warn` today: choosing and provisioning a runtime is the Day 3 CLI ticket, and `newDoctorCommand` takes the prober as a parameter for exactly that reason. Not done: `Level.UnmarshalJSON`, result caching, a `--output` flag, and `--fix --dry-run`, each marked `// TODO(v0.2):` at its call site, because none of them is this ticket's job. Never run against a real WSL host or a real Docker daemon in Windows-container mode: every WSL and Docker-daemon-state test goes through the fake `runner`, and the two `x/sys` syscall wrappers (`RtlGetVersion`, `GetDiskFreeSpaceEx`, `unix.Statfs`) are covered only by `GOOS=windows go build`/`go vet` cross-compilation, not by execution, on this Linux-only host. `govulncheck`, `gosec`, and `pytest` over `scripts/tests` are not installed here and are left to CI, as with every prior entry that says so. |

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
- **`PushFiles` staged a mirrored tree on the host and copied it, and that
  design was wrong.** It cost three red CI rounds and three wrong guesses
  before the mechanism was measured rather than theorised, which is the
  real lesson worth recording here. `docker cp` does not only add the
  files in the archive: it also **reassigns the ownership and mode of
  directories that already exist**. Staging `/home/learner/<path>` under
  `os.MkdirTemp` produced an archive containing `home/` and
  `home/learner/`, so copying it silently rewrote `/home` and
  `/home/learner` to the host uid and the staging mode. Measured directly
  against a live container: `/home` went from `learner:learner 0755` to
  `1001:1001 0750`, after which the `learner` user could not traverse into
  its own home directory. Everything downstream followed from that:
  `stat` returned empty, and a pushed script reported exit 126, which
  reads as a CRLF shebang failure and was nothing of the kind. The
  intermediate guesses (add `CAP_DAC_OVERRIDE`; then chmod the created
  directories) each fixed a real symptom and left the cause in place.
  **`PushFiles` now builds the tar itself with `archive/tar` and streams
  it to `docker cp - <name>:/`.** It emits entries only at and below the
  level it actually creates, never at or above `sandboxRoot`, and writes
  every entry as uid 0. Nothing is staged on the host, so there is no
  temp directory to leak on an error path, and no host identity can reach
  the sandbox at all. `CAP_DAC_OVERRIDE` was removed again as unnecessary
  once root owns what it chmods. The security skill's "prefer the
  standard library over a subprocess" points the same way, and `PullFile`
  already read a tar with the same package.
- **Why none of this reproduced locally, which is the part to remember.**
  Docker Desktop for Windows has no POSIX ownership to preserve, so its
  `docker cp` substitutes root and `0755` for everything. That is
  permissive in exactly the way that hid the bug: root-owned `0755`
  directories stay traversable, so the clobbering was invisible. A real
  Linux host sends its own uid and the real staging mode instead. Worse,
  the `0755` to `0750` tightening made for `gosec` G301 is what turned a
  latent clobber into a hard lockout, so a security fix and a
  platform-specific blind spot combined into a failure neither would have
  caused alone. Building the archive in-process removes the divergence:
  the bytes the daemon receives are now identical on both platforms.
- **A cancelled `Exec` left the sandbox-side process running**, found by
  running the contract suite live, not by any unit test, and it turned out
  to need two attempts. `exec.CommandContext` cancels by sending the local
  `docker` client SIGKILL, which only ever stops the local client. The first
  attempt sent SIGTERM instead, on the theory that `docker exec`'s
  `--sig-proxy` would forward it into the container; that theory was wrong,
  proven wrong by CI rather than caught locally: `docker exec` has no
  `--sig-proxy` flag at all (only `docker run` and `docker attach` do), and
  the orphan reproduced identically on CI's `ubuntu-latest` job, a real
  Linux daemon, ruling out "this is just a Windows Docker Desktop artifact."
  The actual fix: every `Exec` now wraps argv in a tiny sandbox-side shell
  that announces its own PID on the first line of stderr before `exec`-ing
  into the real command, and a cancelled or timed-out call sends an
  explicit follow-up `docker exec <name> kill -9 <pid>` using that PID. The
  marker line is stripped back out of `ExecResult.Stderr` before it reaches
  a caller. `cmd.Cancel` still sends SIGTERM to the local client, but only
  to end it promptly; that alone was never what killed the sandbox-side
  process. `TestDockerContract/ExecHonoursContextCancellation/cancel_aborts`
  is green locally and, per the CI run this depends on, on `ubuntu-latest`.
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
- **One fix outside this ticket's declared file list**, called out because
  the ticket did not authorise it: `sanitizeName` in
  `internal/runtime/runtimetest/contract.go` refused a hyphen while
  rewriting spaces *into* hyphens, so the `utf-8 multibyte` case of
  `TestPullFileRoundTrips` failed on its own table case name before it
  could assert anything about any backend. It was unrunnable for every
  possible implementation, not just this one. CLAUDE.md allows changing an
  assertion that is provably wrong, and this one is; the alternative was
  leaving CI permanently red on a defect in the fixture rather than in the
  code under test. Needs ratifying with the contract suite's author.
- Gates run locally on Windows, all green with no exclusions:
  `gofmt -s -w .`, `go vet ./...`, `go test ./...`,
  `go test -race ./...`, `go test ./internal/archtest/...`,
  `./scripts/check-punctuation.sh`, `./scripts/check-allowlist-regexp.sh`,
  `./scripts/check-links.sh`, and `go mod tidy` leaving `go.mod`
  unchanged. The full contract suite ran live against Docker Desktop's
  Linux engine, not skipped. Not run locally:
  `python3 scripts/check-ci-gates.py` and
  `python3 -m pytest scripts/tests -q` (no `python3` in this environment),
  `govulncheck ./...` (not installed). CI is authoritative for those three.
- CI's `gosec` job found four real issues on the first push, all fixed:
  two `G204` subprocess findings, justified with `#nosec` comments naming
  exactly why the argv is safe (the binary path is fixed by
  `exec.LookPath` in `New`, the vector is built from allowlisted
  identifiers), and two file-permission findings on the host-side staging
  `PushFiles` no longer does at all, since the in-memory tar removed that
  code path entirely.

### Day 1, 2026-08-11: the PTY multiplexer

Issue #10. `internal/pty` now has a working host-to-sandbox multiplexer, not
just the parser Day 1 already had. First real dependency added on top of
`creack/pty`: `golang.org/x/term`, for host raw mode and window size, since
Go has no stdlib termios API.

- `internal/pty/mux.go` declares `Mux`, `New`, `Run`, `Events`, and
  `CommandEvent`, exactly the shape the ticket fixed in advance. `Mux` wraps
  a `runtime.PTY`, forwards a host `io.Reader` to it byte for byte with no
  byte, including Ctrl-C's 0x03, special-cased, and forwards the sandbox's
  output through the existing OSC parser to a host `io.Writer`, stripping
  only what the parser recognizes.
- **Raw mode restoration is guarded by one `sync.Once`, reached from two
  places**: a top level `defer` in `Run`, which also recovers and
  re-panics after restoring so a panic never skips it, and a SIGINT and
  SIGTERM safety net that can fire independently while `Run` is still
  blocked. A raw terminal disables the line discipline that would
  otherwise turn the learner's own Ctrl-C into a delivered SIGINT, so a
  real SIGINT or SIGTERM reaching this process while raw mode is active
  never came from the learner typing at the sandbox; the handler exists
  only so that outside signal does not strand the terminal in raw mode.
  `doc.go` now names this as a second risk category for this package,
  alongside the parser's own streaming state machine risk.
- **A panic inside the goroutine that calls `runtime.PTY.Wait` is
  recoverable from `Run`, correctly, only because it is forwarded as data
  first.** `recover` only ever catches a panic raised in the same
  goroutine as the deferred call to it, and `Wait` runs in its own
  goroutine so `Run` can select on it alongside `ctx.Done` and the two copy
  goroutines. The goroutine's own recover captures the value and sends it
  across a channel; `Run`'s own goroutine re-panics with that value once it
  receives it, which is what lets `Run`'s top level defer restore the
  terminal before the panic reaches `Run`'s caller. `TestRun_RestoresTerminal/panic_inside_run`
  pins this.
- **`Run` closes the sandbox PTY handle on every exit path, but never waits
  for the stdin-forwarding goroutine, only the output copy goroutine.** The
  stdin side blocks on a `Read` of the host's own stdin, which nothing in
  `Run` can unblock without closing the host's stdin out from under
  whatever else might use the terminal after this call returns, and that is
  not this function's decision to make. The output side blocks on a `Read`
  of `m.pty`, which `Run` owns and closes unconditionally before returning,
  so that side is always waited for. Documented in `Run`'s own comment
  rather than left implicit, since it is the one place this multiplexer
  deliberately does not wait for a goroutine it started.
- Event assembly (`PreExec` starts a pending `CommandEvent`, `CommandDone`
  fills in the exit code and duration, `CwdReport` fills in the cwd and
  emits) matches `instrument.bash`'s own emission order. A `CommandDone`
  with no matching `PreExec`, or a `CwdReport` with no pending event, is
  ignored rather than synthesized; a stale pending event, from a dropped
  `CwdReport`, is flushed with an empty `Cwd` the moment the next `PreExec`
  arrives rather than held forever.
- `Events()` is a buffered channel of 64. Large enough that a normal play
  session's burst of quick commands does not trip the drop path, small
  enough that a consumer that has stopped draining it entirely is caught
  within a session. A full buffer never blocks the read side copy
  goroutine: `emit` is a non-blocking send with a `default` branch that logs
  only the fact of a drop, never the event itself, since nothing in this
  package logs raw PTY content at any level on the chance it holds a
  password the learner typed into a sudo prompt.
- `CommandEvent.Raw` is always empty. Populating it needs a
  `runtime.Session` correlated with the journal, which `Mux` has no
  reference to today; tracked against issue #51, not attempted here.
- `startResizeWatcher` is real on unix (`raw_unix.go`, SIGWINCH to
  `Resize`) and a documented no-op on Windows (`raw_windows.go`), a Day 3
  stub: Windows has no SIGWINCH equivalent and needs ConPTY specific
  handling, tracked as a `TODO(v0.2)`.
- **`golang.org/x/term`'s newest tag reproduces this repository's own known
  `go.mod` trap, one level removed.** `go get golang.org/x/term` bumped the
  `go` directive from 1.23 to 1.25.0 again, exactly the failure mode
  `internal/runtime/docker`'s own history warns about, except this time the
  cause was not the tool itself: `golang.org/x/term@v0.45.0`, and the
  `golang.org/x/sys` it requires at that version, both declare `go 1.25.0`
  in their own `go.mod`, which forces every module that depends on them to
  match. Pinned to `golang.org/x/term@v0.33.0` and `golang.org/x/sys@v0.34.0`
  instead, the newest pair whose own `go.mod` still says `go 1.23.0`, which
  keeps this module's floor where it was. `go.mod`'s own comment now records
  which pair and why, so the next dependency bump that trips this either
  gets the same treatment or a deliberate, reasoned floor raise, not a
  silent one. The `go` directive ended up written as `go 1.23.0` rather
  than the prior bare `go 1.23`; that is the toolchain's own normalization
  of the identical floor, not a version change, confirmed by a clean
  `go build ./...` with no `-mod=mod` override needed.
- Added to `.claude/skills/go-style/SKILL.md`'s approved dependency table:
  `golang.org/x/term`, for host raw mode and window size.
- Tests: a `fakePTY` (`runtime.PTY` over an `io.Pipe` for the read side,
  a plain recorded byte slice for the write side) and the four injectable
  terminal func fields overridden directly, this is a white box test in
  the same package, cover terminal restoration across a clean exit, a copy
  error, a cancelled context, and a panic; the terminating signal handler
  called directly; the initial resize proven ordered ahead of any forwarded
  output; the resize method's own argument order with two easily
  transposed values; a non-fatal resize failure; stdin forwarded verbatim
  including Ctrl-C; markers stripped from a stream with one deliberately
  split mid-payload; event assembly for a clean command, a non-zero exit, a
  cwd change across commands, and a dropped `CwdReport`; `Events()` closing
  when `Run` returns; and a full buffer proven non-blocking. One test,
  proving the drop-capacity assertion exactly, was flaky under `-race`
  until it stopped racing the read side copy goroutine's own processing of
  its last written chunk against the drain loop: an `io.Pipe` `Write`
  returning only proves the matching `Read` happened, not that the
  goroutine has gone on to process what it read, so the fix was one more
  round trip through the pipe after the real payload, which does prove it,
  rather than a sleep. A second, real timing sensitivity surfaced in the
  single command assembly test: two back-to-back `time.Now` calls landed on
  the same tick often enough to make `Duration` flakily zero, fixed with a
  five millisecond gap between the two markers being fed, not by loosening
  the assertion.
- Gates run locally, all green: `gofmt -s -w .`, `go vet ./...`,
  `go build ./...`, cross-compiling `internal/pty` for both `windows/amd64`
  and `linux/amd64`, `go test ./...`, `go test -race ./...` (run six times
  in a row for `internal/pty` specifically, all green, after the flake
  above was fixed), `go test ./internal/archtest/...`,
  `./scripts/check-punctuation.sh`, and `go mod tidy` leaving only the
  `golang.org/x/term` and `golang.org/x/sys` additions in `go.mod` and
  `go.sum`. Not run locally: `govulncheck ./...` and `gosec ./...` (neither
  installed in this environment), `./scripts/check-allowlist-regexp.sh` and
  `./scripts/check-links.sh` (not re-run this session, nothing this ticket
  touched should affect either). CI is authoritative for all of those.

### Day 1 follow-ups, 2026-08-11: PTY multiplexer review fixes

An independent review of PR #59 found two real defects before merge and
one real CI-only failure unrelated to either. All three fixed here, plus
two smaller findings from the same pass.

- **Only the goroutine waiting on `runtime.PTY.Wait` had panic recovery.
  A panic in either copy goroutine, stdin to sandbox or sandbox output
  through the parser, was unrecoverable and would have crashed the whole
  process with the host terminal still in raw mode**, exactly what
  `doc.go` and this ticket's acceptance criteria say must never happen.
  All three of `Run`'s worker goroutines now go through one `runRecovered`
  helper, which captures a panic as data and hands it back on a channel;
  `Run`'s own select re-panics with it from its own goroutine, so the top
  level deferred restore still runs first. `TestRun_RestoresTerminal` grew
  `panic_in_stdin_copy_goroutine` and `panic_in_output_copy_goroutine` to
  pin this.
- **The SIGINT/SIGTERM safety net called `os.Exit(1)` directly, from a
  goroutine independent of `Run`'s own select.** That skipped `Run`'s
  entire exit path: no `drain`, no `Events()` close, no return to the
  caller, which never got a chance to close the `runtime.Session` or
  container the PTY was attached to before the process died. `Mux` is L2
  and does not own the process lifetime; ending it was never this
  package's decision to make. The watcher now forwards the caught signal
  to `m.signalled`, a channel `Run`'s select already watches alongside
  `ctx.Done` and the three worker outcomes, so a caught signal unwinds
  through the ordinary exit path and `Run` returns an error wrapping the
  new `ErrSignalled`. `reraise` and `defaultReraise` are gone.
  `TestMux_HandlesTerminatingSignal` now drives this through `m.signalled`
  directly rather than a removed `handleTerminatingSignal` method, for the
  same reason it never raised a real signal before: unsafe against the
  test process, and `syscall.SIGTERM` has nothing to raise on Windows
  regardless.
- **A bug introduced while fixing the first finding above, caught by the
  suite itself, not by review**: `runRecovered`'s channel was single-send
  and never closed, but `drain` deliberately reads from the output copy
  goroutine's channel a second time on every branch except its own, to
  wait for that goroutine to actually finish. That second read blocked
  forever once the one buffered value was already gone, hanging
  `error_from_copy_goroutine` and the new `panic_in_output_copy_goroutine`
  test for the whole 5 second timeout. Fixed by closing the channel
  immediately after the single send; a receive after that returns at once
  instead of blocking, which is all `drain` needs.
- Two smaller findings from the same review, both fixed: `emit`'s drop
  warning now ends `\r\n`, not a bare `\n`, since the latter does not
  carriage-return on a host terminal `Run` has put into raw mode and would
  render as a corrupted staircase line if the buffer ever actually filled;
  and `New` now prefers whichever of `in` or `out` is an actual terminal,
  via `term.IsTerminal`, before falling back to whichever merely exposes a
  file descriptor, so a redirected non-terminal `in` alongside a real
  terminal `out` no longer fails outright over the side that was never
  going anywhere anyway. `TestNew_NeitherInNorOutExposesFd` closes the one
  path in `New` that had no test at all.
- **Real CI failure, unrelated to this ticket's own code**:
  `Test (windows-latest)` failed three times in a row, on fresh runners,
  all twelve `TestDockerContract` subtests reporting the identical
  `docker build exited 1: ... no matching manifest for
  windows(10.0.26100)/amd64`. `main`'s last green run had passed the same
  suite on `windows-latest` about an hour earlier at 3.2 seconds, which is
  this suite's own skip time, not a real run; the hosted runner's Docker
  daemon evidently started answering `docker version` in
  Windows-container mode sometime in between, which can pull nothing
  Linux-only. `internal/runtime/docker/contract_test.go`'s daemon probe
  now asks `docker version --format {{.Server.Os}}` and skips unless the
  answer is `linux`, rather than only checking that the daemon answers at
  all. Verified this changes nothing about coverage that mattered:
  `Test (ubuntu-latest)` still runs the full suite for real, in about 30
  seconds. `Test (windows-latest)` now skips cleanly in about 3 seconds,
  matching its behaviour before the runner image changed, instead of
  failing loudly for a reason no change to this package could fix.
  **This leaves hosted Windows CI with no real coverage of
  `internal/runtime/docker` at all**, which is not a regression from this
  fix but was not visible before it either; tracked as issue #60 rather
  than left implicit behind a quiet green check.
- Gates re-run locally after all of the above, all green:
  `gofmt -s -w .`, `go vet ./...`, `go build ./...`, `go test ./...`,
  `go test -race ./...` (the `internal/pty` suite specifically run five
  times in a row with no flakes), `go test ./internal/archtest/...`,
  `./scripts/check-punctuation.sh`, and a local `gosec -quiet
  -exclude-dir=docs ./...` (installed fresh this session), clean.

### Day 1, 2026-08-12: documentation drift reconciled, the argv allowlist consolidated

Issue #43. Documentation and one new helper. No behaviour change to what the
allowlist accepts or refuses; it is now enforced from one place instead of seven.

- `docs/LEVEL-FORMAT.md` documented a level id as `[a-z0-9-]+`, which admits a
  leading hyphen. Corrected to `[a-z0-9][a-z0-9-]*`, with a row in the
  authoring invariants table saying why: a leading hyphen makes the id
  flag-shaped, same reasoning as the argv allowlist.
- Issue bodies #6 and #19, edited via the API since the punctuation and
  allowlist gates cannot see either: #6's safety review quoted the older,
  hyphen-anywhere form even though the implementation that closed it already
  shipped the strict, first-character-alphanumeric form; corrected in place
  with an edit note. #19 is legitimately about that older form, so its body
  is left as written with a leading note marking it as historical subject,
  not current guidance.
- The approved dependency set had two stale pointers, both saying "listed in
  CLAUDE.md" when the table has lived in `.claude/skills/go-style/SKILL.md`
  since the Day 0 skill split. Fixed in `go.mod`'s header comment and in
  `CONTRIBUTING.md`. `CLAUDE.md` itself now says explicitly, in "What NOT to
  do", that the table lives in the `go-style` skill, so a reader does not
  have to infer it from the skill table above.
- **`internal/platform.ValidIdentifier`** (`internal/platform/identifier.go`)
  is now the one Go source of truth for the argv allowlist regexp, alongside
  an exported `IdentifierPattern` string constant for callers that want to
  quote it in an error message. It replaces the pattern's separate copies in
  `internal/runtime/spec.go`, `internal/runtime/session.go`,
  `internal/runtime/docker/docker.go`, `internal/runtime/docker/session.go`,
  and `internal/runtime/apishape_test.go`; each now names the function
  instead of restating the regexp. `internal/platform/identifier_test.go`
  carries the refusal table (empty, `-f`, `--force`, a bare hyphen, a leading
  underscore, an embedded space, a shell metacharacter, a path separator,
  `..`, a trailing newline, a non ASCII byte) and an exact-value assertion on
  `IdentifierPattern`, so a future consolidation cannot be the loosening.
  `scripts/check-allowlist-regexp.sh` and the security skill still quote the
  pattern literally, because a bash gate scanning prose and a skill file the
  gate greps have no compiler to lean on, but both now point at
  `internal/platform.ValidIdentifier` as the canonical implementation.
- **Contract decision, made while implementing, not merely following the
  ticket:** the ticket named `internal/runtime/identifier.go` as the new
  file's home, but `internal/runtime`'s own `doc.go` and
  `apishape_test.go`'s `TestExecTakesArgvNotACommandString` require that
  package to declare interfaces and value types only, no exported functions.
  Adding `ValidIdentifier` there fails that test on contact. Asked; the
  answer was `internal/platform`, which `internal/runtime` (and its `docker`
  subpackage) is already permitted to import, so the type-only invariant
  holds and the layer rule is untouched.
- A grep of the tracked tree for the loose form,
  `grep -rn 'a-zA-Z0-9_-]+' .claude/ docs/ internal/ scripts/`, returns only
  the allowlist gate's own fixtures: the briefing snippet
  `scripts/tests/test_check_allowlist_regexp.py` writes to prove the gate
  does not flag unanchored prose, and the gate's own header comment
  describing that same fixture.
- Gates re-run locally: `gofmt -s -w .`, `go vet ./...`, `go build ./...`,
  `go test ./...`, `go test -race ./...`, `go test ./internal/archtest/...`,
  `./scripts/check-punctuation.sh`, `./scripts/check-links.sh`, and
  `python3 -m pytest scripts/tests -q` (43 passed, pytest installed fresh
  this session), all clean. `govulncheck` and `gosec` are not installed in
  this environment and were not run locally; CI runs both and is
  authoritative.

### Day 1, 2026-08-12: percent-encode the OSC 7 cwd payload, partial pass on issue #44

Issue #44, three of five acceptance criteria. Stays open: AC3 and the sandbox
end-to-end verification are not done here.

- **AC1, done.** `images/rc/instrument.bash` gained `__sf_urlencode`, called
  from `__sf_after` before the OSC 7 marker is written, so `$PWD` reaches the
  payload percent-encoded instead of raw. A directory literally named `100%`
  now encodes to `100%25`, which `url.PathUnescape` decodes back to `100%`. A
  directory literally named `a%20b` (five literal characters) encodes to
  `a%2520b`, which decodes back to `a%20b` exactly, staying distinguishable
  from a directory actually named `a b`, which separately encodes to `a%20b`
  and decodes back to `a b`. That distinguishability is the entire point of
  AC1, and it now holds.
- **AC2, done.** `__sf_urlencode` is bash builtins only: `printf`, parameter
  expansion, and a C-style `for` loop, no subprocess and no `$(...)`. It
  writes its result to the global `__sf_encoded` specifically to avoid
  forking a subshell. `LC_ALL=C` makes bash index the string one byte at a
  time rather than one multibyte character at a time, which is what makes a
  path containing a multi-byte UTF-8 character round-trip correctly through
  a function with no notion of what a UTF-8 code point is; that reasoning is
  written into the comment above the function, per AC2.
- **`images/rc/instrument_test.bash`** is a new standalone unit test for
  `__sf_urlencode`, runnable with no Docker and no sandbox
  (`bash images/rc/instrument_test.bash`). It extracts only the function with
  `sed` and `eval` rather than sourcing the whole of `instrument.bash`, which
  also pulls in `/etc/bash.bashrc`, the user's own `.bashrc`, and sets
  `PROMPT_COMMAND` and several `readonly` variables: side effects a unit test
  for one function has no business triggering. Seven cases, including the
  `100%` and `a%20b` round-trips above, a multi-byte UTF-8 case, and the root
  path. All seven pass. Set mode 755. Wired into the `Style and punctuation`
  CI job as a new step, after the CRLF gate and before the merge gate check.
- **`internal/pty/osc_test.go`**: the two `hostileCases()` rows that pinned
  the now-fixed producer mismatch (`cwd unencoded bare percent at end` and
  `cwd unencoded percent mid path`) are removed. **Not because the payloads
  become unreachable**: this file's own documented threat model says a
  byte-identical forged marker, written by any process with access to the
  PTY and not just `instrument.bash`, is indistinguishable from a genuine
  one, so a bare-percent OSC 7 payload stays fully reachable regardless of
  what `instrument.bash` emits. The real reason is that equivalent branch
  coverage over `url.PathUnescape`'s failure modes already exists,
  independent of any specific producer: the still-present `cwd bad percent`
  row (invalid hex digit) and `cwd truncated percent` row (short string)
  already exercise the identical two failure branches those rows exercised.
  No replacement rows were added.
- **AC5, done.** `TestEventKindString` is new: a table test covering all
  five `EventKind` constants (`promptstart`, `commandstart`, `preexec`,
  `commanddone`, `cwdreport`) plus a value outside all five, asserting the
  `eventkind(99)` fallback form. `EventKind.String()` itself was not
  changed: it already existed with exactly this behaviour, written in an
  earlier review pass but never covered by a test until now. This closes the
  last 0-percent-coverage gap the OSC parser had.
- **`internal/pty/osc.go`**: comment-only change inside `recognize`'s OSC 7
  branch. The paragraph that said the producer does not yet encode the path
  and blamed issue #25 now says the producer meets the contract as of issue
  #44, points at `__sf_urlencode` and its RFC 3986 unreserved-plus-slash rule,
  and points at `images/rc/instrument_test.bash` for the round-trip cases.
  The paragraph explaining why a raw-path fallback was rejected (the `a%20b`
  versus `a b` ambiguity) is kept, because that reasoning is exactly why the
  fix works. No logic in this file changed: `recognize`, `Write`, and `step`
  are untouched.
- **AC3 and the sandbox end-to-end verification are NOT done here.** AC3
  needs `vim-session.bin` re-captured inside the built sandbox image via
  `make image` then `script -q`, and this environment has no running Docker
  daemon: `docker version` succeeds against the client but the daemon socket
  is unreachable. `internal/pty/testdata/vim-session.bin` was not touched.
  **AC4 needed no change**: `.gitattributes` already marks `*.bin binary`,
  verified, not edited.
- **Issue #44 stays open.** The remaining piece, the vim fixture recapture
  plus running the golden path against a real sandbox, needs Docker and is
  still tracked there.
- Gates run locally, all green: `gofmt -s -w .`, `go vet ./...`,
  `go test ./...`, `go test -race ./...`,
  `./scripts/check-punctuation.sh`, `go test ./internal/archtest/...`,
  `./scripts/check-allowlist-regexp.sh`, `./scripts/check-links.sh`,
  `python3 scripts/check-ci-gates.py`, `python3 -m pytest scripts/tests -q`,
  and `bash images/rc/instrument_test.bash`. `govulncheck` and `gosec` are
  not installed in this environment and were not run locally; CI runs both
  and is authoritative.

### Day 1 follow-ups, 2026-08-12: a token saver autonomous run profile

Agent tooling only. No Go code, no test, no gate, and no level changed here
(#63).

- **`.claude/routine/AUTO_IMPLEMENT_SAVER.md` is a second unattended run
  profile**, alongside the existing opus-first `AUTO_IMPLEMENT.md`. It routes
  phases 1 through 4 to sonnet and buys opus per call on six named triggers:
  a destructive path or argv construction or pack path handling in scope; the
  OSC parser, PTY multiplexer, or runtime process lifecycle in scope; an
  internally inconsistent plan, once; two or more `blocking` review findings, or
  any `blocking` finding in the safety lens; repair round 3 on the same CI job;
  a change to a living contract. If none fires, sonnet finishes the run.
- **The saving is specificity, not less thinking.** Reconnaissance is gathered
  once in Phase 0b in the main session, as symbol names and signatures rather
  than file bodies, and embedded verbatim in every sub-agent prompt, so no
  sub-agent rediscovers the repository. On top of that: three supplementary
  reads per sub-agent, sixty lines of failing log from the failing job only,
  diffs rather than transcripts, widening CI poll intervals, gates chained in
  one Bash call, and two `Workflow` calls rather than four with plan and
  implement as one pipeline.
- **The three review lenses survive intact**, run as three parallel
  narrow-context sonnet agents, one lens each, deduplicated in the main session
  by file, line, and substance, keeping the highest severity per group. Narrow
  scope is what makes a cheaper reviewer reliable; dropping a lens would not
  have been. The local gate chain, the NEVER list, the preservation discipline,
  and the honest reporting rules are unchanged from the opus profile.
- **The report gains a cost ledger** as section 11: the model that actually ran
  each phase, every escalation with the trigger that bought it, the `Workflow`
  call count, the repair round count, and any context budget exceeded and why.
  Without it there is no way to tell a genuinely cheap run from one that quietly
  escalated everything.
- **Both profiles are now reachable from the skills table**: new
  `auto-implement` and `auto-implement-saver` skills, each stating when to load
  it and which sibling to load instead, plus a comparison table for choosing.
  Each run prompt points at the other and says to read exactly one per run.
  `CLAUDE.md` gained the two skill rows and a `.claude/routine/` line in the
  repo layout.
- **Not done here, deliberately**: no scheduler or cron wiring, so choosing a
  profile stays a decision made at invocation; no tooling that computes the cost
  ledger, which the run reports rather than measures. Neither profile has been
  exercised end to end against a real ticket yet, so the claimed saving is a
  design argument, not a measurement.
- Gates run locally: `./scripts/check-punctuation.sh` and
  `./scripts/check-links.sh`, both clean. The Go gates were not run and did not
  apply: this change touches no Go file. CI is authoritative for the rest.

### Day 1, 2026-08-12: fuzz crasher upload and a reporting-only label prune mode, partial pass on issue #45

Issue #45, two of three pieces. Stays open: the five default labels themselves
are not deleted.

- **`.github/workflows/ci.yml`** gained a new step, "Upload fuzz crashers",
  immediately after "Fuzz the OSC parser" in the `test` job. It runs only
  `if: failure() && matrix.os == 'ubuntu-latest'` and uploads
  `internal/pty/testdata/fuzz/FuzzParser/` with `if-no-files-found: ignore`,
  using `actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a #
  v7.0.1`. That SHA was verified by reading the compare view between the
  `v7.0.0` and `v7.0.1` tags on github.com/actions/upload-artifact
  (`.../compare/v7.0.0...v7.0.1`), which lists every commit between the two
  tags; the last commit in that list, a merge commit, is the actual tip of
  `v7.0.1`, and that commit's SHA is the one pinned here. This is stronger
  evidence than trusting a tag reference or a search result alone, since the
  compare view shows the real commit graph. A red fuzz run destroys its own
  reproducer the moment the runner workspace is torn down, so without this
  step a crashing input found in CI has to be reproduced by hand instead of
  downloaded and added to the seed corpus. The fuzz duration and the seed
  corpus itself were not touched.
- **`scripts/sync-labels.sh` gained a `--prune` mode**, checked before the
  existing positional `$1` repo handling since `--prune` is a flag, not a
  repo argument. It prints the five names GitHub auto-creates on every new
  repository (`bug`, `documentation`, `enhancement`, `invalid`, `question`),
  states that they shadow the `type:` taxonomy in `.github/labels.yml`, and
  names the exact manual command (`gh label delete <name>`) to run only
  after confirming each still has zero issues and zero pull requests. It
  never calls `gh label delete` or any other mutating `gh` command, and
  exits 0 without requiring `gh` to be installed at all. The default sync
  behaviour (no `--prune`) is unchanged.
- **`scripts/tests/test_sync_labels_prune.py`** is new. It runs
  `bash scripts/sync-labels.sh --prune` as a subprocess with a fake `gh` on
  `PATH` that logs every call it receives, and asserts: exit code 0; the
  five default label names appear in stdout; no other name from
  `.github/labels.yml` appears in stdout, so a future accidental widening of
  the prune list would be caught; and the fake `gh`'s call log, if it has
  any content at all, never contains the substring "delete" in any case.
  Confirmed failing before `--prune` existed (the script fell through to the
  normal sync path and printed `created ...` for every real label instead),
  passing after.
- **`scripts/tests/test_ci_yml_fuzz_upload_step.py`** is new. It parses the
  real `ci.yml`, finds the step named "Fuzz the OSC parser", and asserts the
  very next step in the `test` job is the new upload step with the right
  `path`, `if-no-files-found`, and `if:` condition. A second test reuses
  `gates.parse_uses_lines` and `gates.check_uses_ref` from
  `scripts/check-ci-gates.py` to confirm the new `uses:` line is pinned to a
  40 character lowercase commit SHA with a version comment, the same
  standard every other action in this repository already meets. Confirmed
  failing before the step existed (no step follows the fuzz step; no
  `actions/upload-artifact@` reference found), passing after.
- **The five default labels are NOT deleted, and `.github/labels.yml` was
  deliberately left untouched.** No tool available in this environment can
  perform that write: no GitHub MCP label-delete tool, no `gh` CLI
  installed, and no direct GitHub API call permitted by this environment's
  own operating rules, even though a token happens to be present in the
  environment. All five were already confirmed this session to carry zero
  issues and zero pull requests as of 2026-08-12, using
  `mcp__github__list_issues` (one call per label, filtered by that label,
  across both open and closed state) for issue usage, and
  `mcp__github__search_pull_requests` (one query per label, `label:<name>`)
  for pull request usage, both returning zero results for all five labels.
  So the delete itself is a five-command manual step (`gh label delete
  <name>` per name) for whoever next has `gh` access, not a research task.
  `.github/labels.yml` gained no
  header comment claiming the prune happened, because it has not happened
  yet.
- **Issue #45 stays open.** The reporting mode and its tests, and the CI
  upload step and its tests, are done. The five labels themselves are not.
- Gates run locally, all green: `./scripts/check-punctuation.sh`,
  `python3 scripts/check-ci-gates.py`, `python3 -m pytest scripts/tests -q`,
  and `./scripts/check-links.sh`. `gofmt`, `go vet`, `go test`,
  `go test -race`, and `go test ./internal/archtest/...` were not run and do
  not apply: this change touches no Go file.

### Day 1, 2026-08-12: the demo level, playable end to end. The gate is GO.

Issue #11, the last Day 1 ticket. Wiring, plus four defects the wiring exposed,
three of which no existing test could have caught.

- **`internal/sandbox/demo_level.go`** declares `DemoLevel`, `Demo`, `Setup`,
  `Check` and `Teardown`, shaped like the YAML that replaces it on Day 2 so the
  migration is mechanical. It mirrors `pipe-05`: three generated log files, 147
  lines matching ERROR, that count written into `report.txt`.
- **The answer is a property of the generator, not an observation about it.**
  Each file gets a fixed ERROR budget (58, 41, 48) laid down as a fixed number
  of true values and then shuffled, so the count survives any change to how the
  shuffle picks positions. The seed only decides where the ERROR lines land and
  which message each carries. Noise messages are paired to their severity so the
  file reads like a real log, and no noise line may contain "error" in any case:
  the solution greps case-insensitively, so one stray "Error" would inflate the
  answer with nothing noticing. `TestNoiseNeverMatchesTheAnswer` pins that.
- **`cmd/shellforge/cmd_run.go`** implements `run demo`: provision, session,
  teardown-first setup, briefing, FIFO control channel, `Mux`, an event drain,
  and teardown on every exit path. The teardown `defer` is registered BEFORE
  `Setup` so a setup that fails halfway still cleans up, and it runs on
  `context.WithoutCancel` so a Ctrl-C at the process level cannot skip it.
- **The session's default `WorkDir` is `/home/learner`, not the level root.**
  `docker exec -w` fails on a directory that does not exist, and the level root
  does not exist until `Setup` creates it, so a session defaulting to the level
  root could not run the calls that create it. The interactive shell gets the
  level root through `AttachOpts.WorkDir` instead.
- **Defect 1, `ExecOpts.Stdin` was silently discarded by the Docker backend,
  and it is why `check` printed nothing at all.** `execArgv` never passed `-i`,
  and `docker exec` without `-i` connects the container process's standard input
  to nothing. So the reply `tee` opened the response FIFO, which correctly
  unblocked the shim's `cat`, then wrote zero bytes and closed: the shim got an
  immediate EOF and printed nothing. Everything looked right and nothing worked.
  Measured, not inferred: `echo x | docker exec c tee /tmp/probe` leaves the
  probe empty and the same command with `-i` writes `x`. `-i` is added only when
  there is stdin to send, because there is nothing for it to do otherwise:
  `execRunner.run` leaves `cmd.Stdin` nil when the caller passed no bytes, and
  `os/exec` then connects the child to `os.DevNull`. Review caught the first
  version of this note claiming the conditional was a safety control, on the
  grounds that an unconditional `-i` would attach this process's own standard
  input and let a background check eat the learner's keystrokes. That is wrong:
  `os/exec` never substitutes `os.Stdin` for a nil `cmd.Stdin`, so the keystrokes
  were never reachable. The conditional is tidiness, and nobody should rebuild a
  security argument on it. **`runtimetest` had no assertion
  covering the field at all**, which is why this shipped; `PushFiles` uses stdin
  too but through `docker cp -`, which reads the local CLI's stdin and was
  unaffected, so the gap stayed invisible. New contract assertion
  `ExecWritesStdin` closes it, using a bare `cat` so delivery and closure are
  asserted at once: a backend that delivers the bytes but never closes stdin
  hangs rather than comparing. Suite size pins moved 12 to 13. WslRuntime
  inherits the assertion on Day 3.
- **Defect 2, root cannot delete the level root.** `--cap-drop ALL` removes
  `CAP_DAC_OVERRIDE`, so root inside the sandbox is subject to ordinary
  permission checks; unlinking needs write permission on the directory, and the
  level root is learner-owned, so a root `rm -rf` failed with Permission denied
  on every entry. Teardown now runs as the learner, who owns their own world,
  with a best-effort root `chown` first to repair the one broken state a setup
  that died between `PushFiles` and its chown can leave behind. Found by the
  golden test against a live container, not by any unit test.
- **Defect 3, `PushFiles` leaves ancestor directories root-owned.** It creates
  them as uid 0 and only chmods them, never chowns them, so after the push
  `/home/learner/quest` was root-owned 0755 and the learner could not create
  `report.txt` in their own level root: the level was unsolvable and the
  solution failed with Permission denied. `Setup` now chowns the root, which at
  the time was the hardcoded equivalent of the trailing `chown -R
  learner:learner .` every level's `setup.script` ended with. That trailing
  chown is gone as of #52: it was the mechanism masking a later, unrelated bug,
  and it never belonged to `pipe-05`, which has no `setup.script` at all.
  `TestTheLearnerOwnsTheLevelRoot` asserts the property directly by writing to
  the root as the learner rather than asserting the chown call.
- **Defect 4, and the one with the widest blast radius: the entire
  `cmd/shellforge` package was never committed to the repository.**
  `.gitignore` line 5 was `shellforge`, unanchored, which matches any path
  component with that name at any depth, including the directory. `git ls-tree
  origin/main cmd/` returned nothing. Because CI builds with `go build ./...`
  and tests with `go test ./...`, both skipped a package that was not there
  and reported green, so the CLI entry point and its tests had never once
  been compiled or run in CI since Day 0. The patterns are now anchored to
  `/shellforge` and `/shellforge.exe`, with a comment saying why they must stay
  that way, and `main.go`, `commands.go` and `commands_test.go` are committed
  for the first time. A CI gate that builds the actual binary rather than
  `./...` would have caught this and does not exist yet.
- **The control channel is the FIFO pair the shims already expected**, host side
  implemented with one blocking `Exec` per direction: `cat` to read the request,
  `tee` to write the reply, no shell anywhere in the path. The reply is bounded
  at 10 seconds so a learner who kills the shim mid-request cannot wedge the
  loop for the rest of the session; the read side is deliberately unbounded,
  because waiting is the normal state. `TODO(v0.2)` records that it costs two
  sandbox-side processes per request and cannot serve two at once.
- **`run` refuses up front on a Windows host**, with the new
  `windows-needs-wsl` doc anchor. `creack/pty`'s Windows `StartWithSize` returns
  `ErrUnsupported` unconditionally, so `Attach` cannot work there at all, and
  without the guard `run demo` spent minutes building an image before failing at
  the last step with "docker exec -it: unsupported", which reads like a Docker
  problem and is not one. The contract suite has no `Attach` assertion, which is
  why it passes on Windows and does not catch this.
- **Tests.** No Docker: fixture consistency counted the way the solution counts
  it, the per-file budgets, the noise trap, determinism, an 18 row refusal table
  for `validateLevelRoot`, teardown running as the learner and chowning as root,
  setup ordering, `Check` including eleven near-misses a real learner produces,
  check purity, the control channel wire format, and the debug log's carriage
  returns and its refusal to log raw command text. Docker required, skipped
  cleanly without a Linux daemon: the golden sequence, setup idempotence, the
  learner owning the root, check purity against a real filesystem hash,
  instrumentation markers from a real bash through the real parser, the control
  channel answering the real shim, no host mounts, and the isolation test.
- **The isolation test is the one that matters most.** It runs `rm -rf` inside
  the sandbox as the learner, for real, then asserts a host canary is
  byte-identical, the host working directory kept every entry, the level really
  was destroyed (otherwise the first two assertions prove nothing), and `Setup`
  recovers the level to a passing state.
- **Every new test was verified to fail without its fix.** The control channel
  test specifically: `-i` was disabled, the test failed with the exact
  diagnostic it prints about a discarded stdin, and the fix was restored.
- Also in this branch, at the maintainer's request and unrelated to the ticket:
  the Code of Conduct contact address changed to `ajoottun24@gmail.com`, and
  this repository's local `git config user.email` was pointing at the old
  address and is corrected. Past commits were authored as
  `164630017+JoottunAtish@users.noreply.github.com`, so no history rewrite was
  needed.
- Gates run locally, all green: `gofmt -s -w .`, `go vet ./...`,
  `go build ./...`, `go test ./...`, `go test -race ./...` on
  `cmd/shellforge`, `internal/sandbox` and `internal/pty`,
  `go test ./internal/archtest/...`, `./scripts/check-punctuation.sh` (132
  files, up from 124 now that `cmd/` is visible to it),
  `./scripts/check-allowlist-regexp.sh`, `./scripts/check-links.sh`, and the doc
  anchor check. Not run locally: `govulncheck` and `gosec` (not installed), and
  `scripts/check-ci-gates.py` plus `pytest` (no PyYAML, no pytest). This change
  touches no CI YAML and no Python. CI is authoritative for those.

#### Review pass on #72, same branch

A full review of the branch before merge. No blocking defect, one wrong comment
and four things worth fixing.

- **The stated reason for making `docker exec -i` conditional was wrong, and the
  wrong version was the alarming one.** It claimed an unconditional `-i` would
  attach this process's own standard input and let a background check eat the
  learner's keystrokes. `os/exec` connects a nil `cmd.Stdin` to `os.DevNull` and
  never substitutes `os.Stdin`, and `execRunner.run` leaves it nil when the
  caller passed no bytes, so the keystrokes were never reachable. The behaviour
  is unchanged and still correct; the comment in `session.go` and the note above
  now say why it is a tidiness choice and record that it is not a safety
  control, so nobody rebuilds a security argument on it.
- **`DemoLevel.Check` read `report.txt` with no timeout**, which made its own
  `res.TimedOut` branch unreachable code: the docker backend only reports
  `TimedOut` when `ExecOpts.Timeout` is set. The path being read is one the
  learner owns, so `cat` was not guaranteed to return. A `report.txt` that is a
  named pipe with no writer wedged the control channel for the rest of the
  session, and a link to `/dev/zero` grew the host process without bound.
  Bounded at ten seconds, with a message naming the file to remove.
- **The failure message asked whether the search was case-insensitive, which
  cannot ever be the cause.** Nothing in the fixture matches ERROR in any case
  except the literal level label, so `grep -r ERROR` and `grep -ri ERROR` both
  return 147: measured both ways, not reasoned about. There are now three
  messages instead of one, telling missing, empty, and wrong apart, and the
  wrong one quotes what is in the file, because a learner who ran `wc -l` on a
  file rather than a pipe has the right count in the wrong shape and recounting
  is the one thing that will not help.
  `TestCaseSensitivityDoesNotChangeTheAnswer` pins the fact the wording rests
  on, so a fixture that later grows lowercase error text fails a test instead of
  quietly making the old advice true again.
- **Nothing waited for the control loop on the way out**, so the claim that no
  `cat` is left holding the FIFO open was likely rather than true: the kill runs
  inside that goroutine, and returning without waiting for it raced process
  exit. The exit path now waits, bounded at five seconds, because a control loop
  that will not come back must not be able to hold the learner's prompt hostage.
- **`TestTeardownRefusesBeforeItDeletes` asserted no `rm` but not no `chown`.**
  The chown is recursive and runs as root, so it is a state-changing call and
  belongs behind the same guard. The guard does already cover it; the test now
  says so, which is what stops a future reordering from passing unnoticed.
- Deferred deliberately, filed as #77: `run` still decides whether an
  interactive shell is possible by testing `runtime.GOOS` rather than by asking
  `Runtime.Capabilities()`. The refusal is correct today, and the fix touches
  `runtime.Caps`, which is a code-owner path, on a branch already carrying more
  than one logical change.
- Every fix above was verified to fail without itself: the timeout assertion and
  the failure-message regression test were each run against the old code and
  seen to fail with their own diagnostic before the fix went back in.

### Day 2, 2026-08-12: cmd/shellforge migrates to cobra, the first three dependencies

Issue #48. `cmd/shellforge` no longer hand-rolls its own dispatcher.
`spf13/cobra`, `goccy/go-yaml`, and `modernc.org/sqlite` are the module's
first three real dependencies beyond `creack/pty` and `golang.org/x/term`.

- **`root.go` and `version.go` are new; `commands.go` is gone.**
  `NewRootCommand(VersionInfo) *cobra.Command` builds the whole tree.
  `main.go` is back down to constructing the root command, executing it with
  the Ctrl-C-aware context, and rendering any error through `ux.Render`,
  matching the ticket's own `main.go` sketch. `run` keeps its existing,
  already-tested `parseRunArgs` by setting `DisableFlagParsing` on its
  `cobra.Command` rather than rewriting argument handling this ticket was not
  asked to touch; `cmd_run.go`, `cmd_run_test.go`, and
  `control_channel_test.go` are otherwise untouched.
- **Fourteen verbs, not the ticket's own twelve.** The ticket's Interfaces
  section names `doctor init run play check hint reset map stats author
  sandbox version`, dropping `skip` and `bug-report` from what was already
  registered and tested. Both are kept: `skip` is in
  `docs/design/PROJECT-BRIEF.md`'s own scope table, which the ticket says it
  is matching, and `internal/platform/ux.Render`'s fallback message for an
  unexpected error already tells the learner to run `shellforge bug-report`,
  so dropping it would point that message at a command that does not exist.
  `TestRegisteredVerbSetMatchesTheDocumentedSet` pins the full fourteen.
- **`sandbox` and `author` are `cobra.Command` groups**, each with the four
  stub subcommands the ticket's Interfaces section names verbatim:
  `sandbox build|rebuild|destroy|status`, `author validate|scaffold|test|record`.
  This does change `sandbox`'s subcommand set from the single stub's own
  `usage` string on Day 0 (`status|shell|rebuild|destroy`); the ticket's
  explicit interface for #48 is the newer, authoritative source here.
  `TestSandboxIsAGroupWithItsFourSubcommands` and
  `TestAuthorIsAGroupWithItsFourSubcommands` pin both sets.
- **Shell completion is explicitly off.** cobra registers a `completion`
  subcommand by default; `root.CompletionOptions.DisableDefaultCmd = true`
  turns it back off, because the ticket's own "Out of scope" section names
  shell completion as the Day 3 doctor ticket's job, not this one's.
- **CLAUDE.md is not touched, contrary to the ticket's file list.** The
  ticket predates #43 (Day 1), which already moved the approved dependency
  table to `.claude/skills/go-style/SKILL.md` and made CLAUDE.md say so
  explicitly. Re-adding a table to CLAUDE.md now would recreate the exact
  two-copies-drift problem #43 closed.
- **`goccy/go-yaml` and `modernc.org/sqlite` needed a real importer, or CI's
  own tidiness gate would strip them again.** `.github/workflows/ci.yml`
  (lines 176 to 193) runs `go mod tidy` and fails on any diff, and Go's
  module graph pruning drops a required module nothing imports. The ticket's
  own file list scopes this ticket to `cmd/shellforge` alone for behaviour,
  which cannot satisfy that: `cmd/shellforge` importing either package
  directly would itself violate the very confinement the ticket's own
  acceptance criteria ask for. Asked; the answer was to add a minimal, real
  primitive in each package rather than defer the two dependencies to their
  own tickets. **`internal/content/yaml.go`** adds `Unmarshal(filename
  string, data []byte, v any) error`, strict-mode `goccy/go-yaml` naming the
  source file in its error, exactly the decoding primitive `internal/content`'s
  own `doc.go` already promised; it knows nothing about the level schema,
  naming a level id and a field is the validator's job, tracked at #53.
  **`internal/store/store.go`** adds `Open(ctx, path) (*sql.DB, error)`,
  which opens through the `modernc.org/sqlite` driver and enables WAL mode,
  matching `internal/store`'s own `doc.go`; schema and migrations are #51's
  job. Neither package gained anything beyond that one function and its
  test.
- **`internal/archtest` gained two tests, each verified against a deliberate
  violation before being committed**, per the ticket's own acceptance
  criteria: `TestConfinedDependenciesStayConfined` fails if `goccy/go-yaml`
  is imported anywhere but `internal/content`, or `modernc.org/sqlite`
  anywhere but `internal/store` (proven by temporarily blank-importing the
  sqlite driver into `cmd/shellforge/main.go`, watching the test name the
  exact violation, then reverting). `TestGoModDirectDependenciesAreApproved`
  cross-checks go.mod's own direct `require` block against the Dependencies
  table in the go-style skill (proven by temporarily deleting `spf13/cobra`'s
  row from that table). The check is one-directional on purpose: the table
  may list an approved module go.mod does not use yet (`fatih/color`,
  `stretchr/testify` both still do), but nothing may reach go.mod without
  first being in the table.
- **Dependency versions were chosen to avoid the floor-bump trap go.mod's own
  header comment already documents for `golang.org/x/term`.** `spf13/cobra`
  pinned at `v1.10.2` and `goccy/go-yaml` at `v1.19.2` are both plain; the
  newest `modernc.org/sqlite` (`v1.56.0`, and `v1.44.0` and above generally)
  requires `go >= 1.24` or `1.25` in its own go.mod, which would have forced
  this module's floor up from `1.23.0` again. Checked each version's
  declared `go` directive against the proxy before choosing:
  `modernc.org/sqlite@v1.38.0` is the newest release whose own go.mod, and
  its `modernc.org/libc@v1.65.10` dependency's go.mod, both still say
  `go 1.23.0`. `go.mod`'s floor is unchanged at `1.23.0`, and `go mod tidy`
  is a no-op across two consecutive runs.
- **`.github/workflows/ci.yml`** gained a `Build cmd/shellforge` step,
  `go build -o bin/shellforge ./cmd/shellforge`, ahead of the existing
  `go build -v ./...`, so a missing main package fails loudly again rather
  than the way `-v ./...` alone let it pass silently until #11.
  `scripts/check-ci-gates.py` and its pytest suite both still pass against
  the edited workflow.
- **gosec found one real issue in the new code, fixed here**: `store.go`'s
  `Open` discarded `db.Close()`'s own error on the path where enabling WAL
  mode fails (G104). Fixed by folding a non-nil close error into the
  returned message rather than dropping it.
- **Found, not fixed, out of this ticket's scope**: the Docs job's own doc
  anchor check in `ci.yml` greps for the literal text `DocAnchor:\s*"..."`,
  which only matches a struct literal. Every `ux.Fail` call in this codebase,
  including every one this ticket added, is positional
  (`ux.Fail(op, err, remediation, "anchor")`), so that grep has apparently
  matched nothing since #6 first wired doc anchors up. Confirmed locally:
  `grep -rhoE 'DocAnchor:\s*"[a-z0-9-]+"' --include='*.go' .` returns zero
  matches against the whole tree, `ux_test.go` and `cmd_run_test.go`
  included, even though both files reference real anchors. Every anchor this
  ticket uses is the empty string, so nothing here depends on the check
  actually running, but the gate itself needs a follow-up issue.
- Gates run locally, all green: `gofmt -s -w .`, `go vet ./...`,
  `go build -o bin/shellforge ./cmd/shellforge`, `go build -v ./...`,
  `go test ./...`, `go test -race ./...`, `go test ./internal/archtest/...`
  (four tests, two new), `./scripts/check-punctuation.sh`,
  `./scripts/check-allowlist-regexp.sh`, `./scripts/check-links.sh`,
  `python3 scripts/check-ci-gates.py`, `python3 -m pytest scripts/tests -q`,
  `go mod tidy` (stable across two runs), and a local
  `gosec -quiet -exclude-dir=docs ./...`, clean after the fix above.
  `govulncheck` could not run: this environment's proxy returned 403 on the
  vulnerability database fetch. CI is authoritative for that one. Manually
  exercised the built binary on Linux: grouped `help` output, `version`,
  several stub verbs including a subcommand (`sandbox build`,
  `author validate`), an unknown command, and `run` with no level, each
  producing the expected user-facing error or output.

### Day 2 follow-up, 2026-08-13: review fix on #80, cobra's own errors and -v/--version

PR #80 code owner review caught one blocking issue in the cobra migration
above, missed locally because nothing exercised cobra's own error path or
the version flag alias, both real on `main` before this PR. Fixed here.

- **Two real regressions, both from `root.go` setting `SilenceErrors:
  true` with nothing to translate cobra's own errors first.** `shellforge
  -v` / `--version` used to work (the old dispatcher special-cased them to
  the `version` verb) and instead produced "unknown flag" wrapped in
  `ux.Render`'s "this is unexpected, it is our bug" fallback. A command
  typo, the single most common beginner mistake, got the same fallback
  instead of a usage message. `TestRunRejectsUnknownCommandWithUsageError`,
  which pinned the old dispatcher's version of this, was deleted with the
  rest of `commands.go` and nothing cobra-shaped replaced it, which is
  exactly how both shipped.
- **`main.go` gained `renderableError(err) error`.** Every verb's own
  `RunE` in this codebase already returns either `nil` or a `*ux.Error`, by
  convention; `renderableError` uses that as the signal: an error that is
  not already a `*ux.Error` can only have come from cobra's own argument
  parsing, so it gets wrapped in one, `err.Error()` as the message and
  "Run `shellforge help` to see the available commands." as the
  remediation, before ever reaching `ux.Render`. `ux.Render`'s "unexpected,
  our bug" fallback now only fires for a genuine programming error, which
  is the case it was written for.
- **`root.go` restores the `-v`/`--version` alias**, not via cobra's own
  `Command.Version` field, which prints a different, single-line format,
  but a local `-v`/`--version` bool flag on the root command plus a `RunE`
  that checks it and calls the same `printVersion` the `version` verb uses,
  falling back to `cmd.Help()` for a bare `shellforge`. Local, not
  persistent: `-v` on a subcommand still means whatever that subcommand
  defines, matching the old dispatcher, which only ever recognized the
  alias as the first argument.
- Five new tests in `commands_test.go`: the two flag spellings
  (`TestVersionFlagAliasesStillWork`), a bare invocation still printing
  usage and exiting clean (`TestBareInvocationShowsHelp`), an unknown
  command and an unknown flag each rendering without the words
  "unexpected" or "bug-report" and pointing at `shellforge help`
  (`TestUnknownCommandAndFlagAreUserFacing`), and `renderableError` leaving
  an already-wrapped `*ux.Error` alone rather than double-wrapping it
  (`TestRenderableErrorPassesThroughAnExistingUxError`).
- Verified against the built binary, matching exactly what review reported
  reproducing against `main`: `shellforge -v`, `shellforge --version`,
  `shellforge nope`, and `shellforge --bogus` all now behave correctly, and
  `shellforge version` and a bare `shellforge` are unchanged.
- Gates rerun locally, all green: `gofmt -s -w .`, `go vet ./...`,
  `go build -o bin/shellforge ./cmd/shellforge`, `go build -v ./...`,
  `go test ./...`, `go test -race ./...`, `go test ./internal/archtest/...`,
  `./scripts/check-punctuation.sh`, `./scripts/check-allowlist-regexp.sh`,
  `./scripts/check-links.sh`, `./scripts/check-cli-package.sh`,
  `python3 scripts/check-ci-gates.py`, `python3 -m pytest scripts/tests -q`,
  `go mod tidy` (stable), and a local `gosec -quiet -exclude-dir=docs
  ./...`, clean.

---

### Day 1, 2026-08-12: fail the build if the CLI entry point vanishes again

Issue #73. CI and scripts only, zero layer, no Go file touched.

- **`scripts/check-cli-package.sh`** is a new style gate asserting four things,
  each with its own message: `cmd/shellforge/` exists and holds at least one
  `.go` file, every `.go` file under `cmd/` is tracked by git, no `.go` file
  anywhere in the repository is matched by a gitignore rule, and the main
  package declares `func main()`. A missing git, grep, or find, or a call made
  outside a git repository, exits 2 rather than reporting clean, matching
  `check-allowlist-regexp.sh`'s convention. Wired into `make lint`,
  `.\make.ps1 lint`, and the style job, plus its own step there so a failure
  reads as its own line in the checks list.
- **`git check-ignore` needed `--no-index` to catch the actual #11 shape.**
  Its default mode consults the index and never reports an already-tracked
  path as ignored, mirroring `git status`. That default would hide the defect
  the moment the file was first committed: a bad gitignore rule added later
  would go unnoticed for every file that predates it. Found by hand, not by
  the test suite: the pytest fixture for the ignored-file case happens to
  leave the file untracked, so it passed either way, but the real repository
  did not until this was fixed. See the verification below.
- **The `test` job now runs `make build` on `ubuntu-latest` and
  `.\make.ps1 build` on `windows-latest`**, then asserts the binary exists and
  its `version` subcommand exits 0. Neither the documented build command nor
  its `-ldflags` version stamping was exercised by CI before this: `go build
  ./...` proves the packages compile, nothing proved `-o bin/shellforge`
  actually produces a binary a learner could run.
- **Verification performed for real, not merely described**: `.gitignore`'s
  `/shellforge` line was changed to unanchored `shellforge` locally, and
  `bash scripts/check-cli-package.sh` was run from the repository root. It
  failed, naming all six tracked files under `cmd/shellforge` and the
  `.gitignore:13:shellforge` rule that matched each one. `.gitignore` was then
  restored with `git checkout -- .gitignore`; `git diff --stat .gitignore`
  showed nothing, and the gate passed clean again immediately afterward. This
  is also the reproduction that surfaced the `--no-index` gap above: the first
  version of the script reported clean against the reverted `.gitignore`
  because the files were already tracked, which is precisely the false green
  this ticket exists to close.
- `scripts/tests/test_check_cli_package.py` covers a clean repository, the
  #11 reproduction (untracked file under `cmd/`, and separately an ignored
  file naming both the file and the rule), the anchored pattern passing, a
  missing package directory, an empty package directory, a missing
  `func main`, running outside a git repository, and a whole-repository
  assertion against the real checkout with no fixture, which is what makes
  `make lint` meaningful. Structured like
  `scripts/tests/test_check_allowlist_regexp.py`: a throwaway git repository
  per case under `tmp_path`, the real script copied in, `git init` plus
  `git add`, run as a subprocess.
- `docs/05-troubleshooting.md` is untouched. No new `DocAnchor` is emitted by
  this change; the gate's failures print their own remediation directly to
  stderr, the same convention `check-allowlist-regexp.sh` already uses.
- Gates run locally, all green: `bash scripts/check-cli-package.sh`,
  `python3 -m pytest scripts/tests -q` (55 passed, including the 9 new cases),
  `gofmt -s -l .` (no output, no `.go` file was touched by this ticket),
  `go vet ./...`, `go test ./...`, `./scripts/check-punctuation.sh`,
  `go test ./internal/archtest/...`, `./scripts/check-allowlist-regexp.sh`,
  `./scripts/check-links.sh`, and `python3 scripts/check-ci-gates.py` (needed
  `pip install pyyaml pytest` first in this environment; confirms `ci.yml`
  stays well-formed and every `uses:` stays pinned, unaffected since no new
  `uses:` line was added). `make build` **does** work in this environment,
  Go is installed here unlike some earlier sessions, so it was run for real:
  `go build -trimpath -ldflags "..." -o bin/shellforge ./cmd/shellforge`
  succeeded, and `./bin/shellforge version` printed the version, commit,
  build date, Go version, and platform, exiting 0. `govulncheck` and `gosec`
  are not installed in this environment and were not run locally; neither
  applies here anyway, since no Go code changed. The Windows leg
  (`.\make.ps1 build` on `windows-latest`) is asserted by the new CI step and
  was not run locally: this environment has no Windows host.

### Day 2, 2026-08-13: the verification check registry

Issue #49. First code in `internal/verify`: a check registry and all 14
check types from the docs/LEVEL-FORMAT.md catalogue (the section's own "13
types" heading undercounts by one against its own list, see below), each
reading real sandbox state through `Session.Exec`. Not wired to a real
sandbox or to `internal/journal`: this ticket is explicitly scoped to the
snapshot readers and the checks, unit tested against fixtures and a fake
`Session`, and stops short of the engine that assembles results, the
composition nodes (`any_of`/`all_of`/`not`), and the `run` wiring that needs
the real `SF_STATE` fix from a different ticket.

- `check.go` declares `Status`, `Result`, `Env`, `Check`, `Spec`, `Factory`,
  `JournalReader`, `Scope`, and `ScopeKind`, plus the shared param-decoding
  helpers every `*_checks.go` file uses to turn a `Spec.Params` map into a
  typed, validated check. `registry.go` is `Register`/`Lookup`/`Types`, a
  mutex-guarded map; `Register` panics on a duplicate type name, which is a
  programming error rather than a runtime condition.
- `snapshot.go` implements `Snapshots.Env` and `Snapshots.Cwd`, both reading
  through `Session.Exec` (`cat` against a file under `Snapshots.Dir`), never
  a local filesystem call. `env -0` output is split on NUL only, and each
  record on the first `=` only, so a value containing a literal newline or
  `=` survives intact; both are pinned by fixtures in `snapshot_test.go`.
- **`Cwd` has no dedicated snapshot file to read, and the ticket's own
  interface names it as if it did.** `instrument.bash` writes exactly four
  files (`env.snapshot`, `alias.snapshot`, `func.snapshot`,
  `shopt.snapshot`), none of them a cwd file. `Cwd` calls `Env` internally
  and reads `PWD` out of the result, since bash auto-exports it. Recorded as
  a contract decision rather than silently worked around.
- **Any `Snapshots` read failure, whether the file is genuinely absent or
  `Session.Exec` itself errored, collapses to one sentinel,
  `ErrSnapshotMissing`.** `env_var` and `cwd_is` translate it into a
  `Status: error` `Result` with a message that says there is nothing to
  check yet, never into `Status: fail`, so a learner who has not pressed
  Enter at the sandbox prompt is not told they got the objective wrong.
  `docs/05-troubleshooting.md` gained one heading, `missing-snapshot`
  (placed immediately after `vm-platform-missing` per the ticket's explicit
  instruction), and `snapshot.go` carries a bare `// DocAnchor:
  "missing-snapshot"` comment next to the sentinel rather than routing
  through `internal/platform/ux.Fail`: a check `Result` is shown through the
  level UI, not rendered as a CLI-level `ux.Error`, and the CI anchor gate
  greps `.go` source for the `DocAnchor: "..."` string literally, so a bare
  comment satisfies it without forcing an unnatural `ux.Fail` call. Verified
  locally against the exact grep and heading-match commands
  `.github/workflows/ci.yml`'s Docs job runs.
- All 14 check types are registered from `init()` in the `*_checks.go` file
  that owns them: `fs_checks.go` (`file_exists`, `file_absent`,
  `file_content`, `dir_exists`, `dir_tree`, `file_mode`, `file_owner`,
  `symlink_target`), `shell_checks.go` (`env_var`, `cwd_is`),
  `process_checks.go` (`process_running`), `journal_checks.go`
  (`command_matched`, `command_not_matched`), `script_check.go` (`script`).
  Filesystem checks share one `statPath` helper (`stat -c '%F|%a|%U|%G'`)
  that distinguishes "does not exist" from "exists but the probe itself
  failed" by matching stat's own "No such file or directory" text; every
  other non-zero exit or Go error from `Exec` is `Status: error`, never a
  guessed pass or fail. `file_absent` is the check this protects most
  directly: a permission failure while probing must not read as "the
  learner deleted it".
- `file_content` implements all six match modes plus `ignore_case`;
  `dir_tree` lists both sides with `find -printf '%P\n'` for the structural
  set `names_only` checks, and additionally hashes every regular file with
  `find -type f -exec sha256sum {} +` for `names_and_hashes`; `file_mode`
  accepts an exact `mode` or an `any_of` list, both parsed as octal with
  `strconv.ParseUint(v, 8, 32)`, which tolerates the leading zero a level
  author writes without special-casing it.
- `env_var` with no `value` param passes on presence alone, including a
  variable exported to the empty string: the parsed snapshot map holds the
  key either way, so presence is `_, ok := snapshot[name]`, not a
  non-empty-string check.
- `process_running` runs `ps -eo pid,args` and matches `pattern` against
  every line after the header row, honouring `negate`. `command_matched` and
  `command_not_matched` share one implementation reading
  `Env.Journal.Commands(scope)`; `scope` is parsed from the single YAML
  string the level format documents (`level`, `last`, `last_n:N`) into
  `Scope{Kind, N}`, with an invalid string or a non-positive `last_n` value
  rejected in the `Factory`.
- `script` runs `["bash", "-c", body]` with `ExecOpts.User` set to `"learner"`
  by default or the validated `as_user`, exit 0 as pass, and a failing
  script's trimmed stdout appended to the authored `on_fail`. **`as_user` is
  validated with `platform.ValidIdentifier` before it can reach
  `ExecOpts.User`, rejecting rather than sanitizing**, per the security
  skill's rule for anything reaching an argv. `ValidIdentifier` already
  rejects the empty string, since the pattern requires a first character, so
  no separate branch was needed to refuse an explicitly empty `as_user`
  distinctly from `--privileged`, `-u root`, or `root learner`: all four are
  the same rejection path, each pinned by its own individually named test.
  An `as_user` key entirely absent from `Params` is the only path that
  reaches the `"learner"` default without validation, and that distinction
  (absent vs. explicitly empty) is called out in the test itself, not just
  in this note.
- `internal/verify/verifytest` holds a fake `runtime.Session`: `Exec`
  answers from a FIFO queue of scripted `Step` functions, one per call, and
  panics on the next call past the end of the queue rather than returning a
  zero value, so an under-scripted test fails loudly instead of asserting
  against garbage. `Attach`, `PushFiles`, `PullFile`, and `Close` all panic
  unconditionally: no check in this package calls any of the four, and the
  fake is built to prove that rather than quietly tolerate it.
- **`internal/verify` never imports `internal/journal`, by design, not just
  by the layer rule.** `internal/journal` is L2 and `internal/verify` is L3,
  so importing it would be a legal downward edge that `internal/archtest`
  would wave through; the package declares its own `JournalReader`
  interface instead, satisfied by `internal/journal` from outside this
  package. `check_test.go` adds a source-parsing test, mirroring
  `internal/runtime/runtimetest`'s own import-scoping test, that parses
  every non-test `.go` file in this package's directory and fails if any of
  them imports `internal/journal`. `doc.go`'s stale sentence claiming this
  package "may import internal/journal, internal/runtime, and below" is
  fixed to say it does not import `internal/journal` at all and names the
  `JournalReader` indirection; the four documented invariants are otherwise
  unchanged. Also added `internal/verify/verifytest` to
  `internal/archtest/layers_test.go`'s layer table at L3, alongside
  `internal/verify` itself, since the archtest walk fails any package with
  no assigned layer.
- `docs/LEVEL-FORMAT.md` was read against every implemented param shape
  (match modes, `dir_tree` modes, `file_mode`'s `mode`/`any_of`, the `scope`
  string grammar, `script`'s `as_user`/`run`) and not touched: nothing here
  diverges from what section 3 already documents. The 13-vs-14 count noted
  above is a heading miscount, not a param-shape difference, so it is
  recorded here rather than used as grounds to edit that file under this
  ticket's own stated scope.
- A test asserts no check's constructed `argv` contains a shell
  metacharacter that was not present in its own input: a deliberately
  shell-shaped path (`$(rm -rf /)`, `;`, a redirection) is round-tripped
  through `file_exists`, `file_absent`, `dir_exists`, `file_content`,
  `file_mode`, and `file_owner`, and the fake `Session` proves it arrived as
  one untouched argv element rather than a fragment of something evaluated.
  `script` is confirmed to be the sole exception by design: its body reaches
  `bash -c` as exactly one argv element, never concatenated with anything
  else, pinned separately in `script_check_test.go`.
- Gates run locally, all green: `gofmt -s -w .`, `go vet ./...`,
  `go build ./...`, `go test ./...`, `go test -race ./...`,
  `go test ./internal/archtest/...`, `./scripts/check-punctuation.sh`
  (staged first, since it reads `git ls-files` and the new package was
  otherwise invisible to it), `./scripts/check-allowlist-regexp.sh`,
  `./scripts/check-links.sh`, `python3 scripts/check-ci-gates.py`, and
  `python3 -m pytest scripts/tests -q` (57 passed; `pytest` needed
  installing first in this environment). `govulncheck ./...` and
  `gosec ./...` are not installed here and were not run locally; nothing in
  this ticket needed the Docker daemon, which is confirmed not running
  here regardless. CI is authoritative for all three.

### Day 2 follow-up, 2026-08-13: review fixes on #49

Ten review findings on PR #81, all fixed on the same branch. Four were
argument injection: a level-supplied `path` reached `stat`, `cat`, and
`readlink` with no `--`, so a path beginning with a hyphen would have been
read as an option rather than as an operand. Passing an argv vector stops a
shell from interpreting the value; only `--` stops the option parser. GNU
find has no such terminator and fails on `--` as an unknown predicate, so
`dir_tree` rejects a non-absolute `path` or `compare_to` in its `Factory`
instead, which closes the same hole for the one command that cannot be
fixed the same way. `argv_injection_test.go` gained the `dir_tree` and
`symlink_target` cases it had claimed to cover and did not, plus a second
test asserting the terminator is present before every path operand and
absent from find's.

Also: `file_content` reads `value` through `paramStringPresence`, so
`match: exact` with an empty value, the natural way to assert that a file is
empty, is authorable again; `dir_tree` reports a root that does not exist as
`Status: fail` with the authored `on_fail` rather than `Status: error`,
matching what `statPath` already does for every other filesystem check, so a
learner who has not created the directory yet is not told the check broke;
`dir_tree` normalizes a trailing slash on either root, which previously made
every hash comparison fail; `equalStringMaps` checks membership rather than
reading a missing key back as the empty string; and `verifytest/doc.go` no
longer claims a compiler-enforced guardrail that did not exist. That claim
is now true instead: `check_test.go` walks every `.go` file in the module and
fails if a non-test file imports the fake.

`docs/LEVEL-FORMAT.md` is again untouched. Requiring an absolute `dir_tree`
path is implementation validation of what the catalogue already implies, not
a new documented shape.

### Day 2, 2026-08-13: transactional setup and teardown runner

Issue #50. New package `internal/content/setup`, layer 3, plus the minimum
`content.Level` typed surface it needs.

Works:

- `internal/content/setupspec.go` declares `Level`, `Setup`, `FileSpec`,
  `GenerateSpec`, and `Teardown`, the four fields issue #50's runner reads.
  This is a deliberate minimum, not the full level type: issue #53 owns
  `pack.go`, `level.go`, `check.go`, `load.go`, `embed.go`, and `validate.go`,
  and is expected to extend this `Level` declaration in place rather than
  redeclare it. A follow-up ratification issue against #53 is needed for this
  contract; not opened from this session, since this run does not touch
  GitHub state.
- `Runner.Setup`, `Runner.Teardown`, and `Runner.IsSetUp` in
  `internal/content/setup/runner.go`. Setup always tears down first, so it is
  idempotent on a dirty root or a repeat call. Teardown validates and
  resolves `setup.root` inside the sandbox with `readlink -m` before removing
  anything, refuses a root outside `/home/learner/`, refuses one that a
  symlink resolves elsewhere, and refuses one that equals, contains, or is
  contained by the state directory, a case the ticket's own seven required
  refusals do not cover and that this package adds a test for regardless.
  The final `rm -rf` runs as the learner, not root: the container drops
  `CAP_DAC_OVERRIDE`, so a root `rm -rf` on a learner-owned tree fails with
  Permission denied, exactly the behavior already measured and reproduced
  from `internal/sandbox/demo_level.go`, the reviewed prior art for this
  containment problem.
- A failure anywhere after the root is resolved rolls the whole setup back
  through `Runner.rollback`, which reruns `Teardown` on a
  `context.WithoutCancel` copy of the caller's context with its own bounded
  timeout, so a Ctrl-C at the moment of failure still cleans up. A rollback
  that itself fails is attached to the original error with `errors.Join`
  rather than swallowed.
- The `SETUP_OK` sentinel lives at `<stateDir>/levels/<level-id>/SETUP_OK`,
  written and removed by `mkdir -p` and `touch`/`rm -f` as the learner, never
  through `PushFiles`. That matters: the docker backend's `buildPushTar`
  writes every ancestor directory as uid 0, so pushing the sentinel would
  silently reassign the state directory to root and every later journal
  write would fail. The level id is validated against
  `platform.ValidIdentifier` before it becomes a path element.
- A `loglines` generator behind a package-level registry
  (`RegisterGenerator`, `Generate`), seeded with `rand.New(rand.NewSource(seed))`
  for cross-platform determinism, refusing `lines` outside 1 to 200000 rather
  than truncating, with a golden sha256 digest pinned in
  `generate_test.go` so a future change to the generator, or a
  platform-dependent one, fails a test instead of quietly shipping a
  different asset on Linux and Windows.
- The host-side staging step (`Runner.buildManifest`, `buildFileEntry`)
  validates a `FileSpec`'s `path`, exactly-one-of `source`/`content`/`generate`,
  `mode` (parsed as octal, so an unquoted `0644` is a load error rather than
  a silently wrong permission bit), and `owner`, reads `source` through
  `fs.ValidPath` against the pack's own `fs.FS`, strips `\r\n` to `\n` before
  the content ever reaches a `runtime.FileEntry` (a backstop, not a
  replacement: `Session.PushFiles` keeps its own strip), and refuses a
  manifest whose total staged size exceeds 64 MiB rather than truncating it.
  Everything here is host memory only; nothing is staged to a host path on
  disk, which is what `TestNoHostFilesystemRemovalInPackage` pins by parsing
  the package's own source for `os.Remove`, `os.RemoveAll`, `os.Mkdir`, and
  similar calls, alongside a second parser test banning the whole `net`
  family of imports from this package and from `internal/content`.
- `FileSpec`'s YAML decoder (`internal/content/setupspec.go`,
  `FileSpec.UnmarshalYAML`) is hand-rolled for one reason: `ContentSet`.
  Struct tag decoding cannot tell an absent `content` key from an empty one,
  and files-01 materializes a `.gitkeep` that exists only to be empty, so
  that distinction has to be real. Everything else the decoder does is what
  strict struct decoding would have done anyway, including refusing an
  unknown key.
- **Fixed (#100): the blanket `chown -R learner:learner` that follows
  `PushFiles` was reverting a declared `owner:` on a `setup.files` entry a
  moment after `PushFiles` applied it, which made it impossible for a level
  to set up root-owned state at all.** `Runner.Setup` now re-applies every
  declared owner, grouped into one `chown` per distinct owner, immediately
  after the blanket chown, and only ever names the declared file's own
  absolute path, never a directory, so teardown's `rm -rf` as the learner
  stays safe. Proven by unit test at the runner; perm-02 is the first shipped
  level that will exercise it end to end.
- Every user-facing error is a `*ux.Error` from `ux.Fail`, with three new
  doc anchors, `unsafe-level-root`, `level-pack-invalid`, and
  `setup-script-failed`, each with a heading in
  `docs/05-troubleshooting.md` added in this same commit, and one existing
  anchor's body, `level-state-corrupted`, extended with a `reset` is not
  yet built fallback. `docs/LEVEL-FORMAT.md` gained a "Setup and teardown
  execution" section and a "The state directory and the SETUP_OK marker"
  section in the same commit, covering the script semantics (root's working
  directory, root's user, the missing `set -e`, the 30 second default
  timeout) and the sentinel's location.
- `internal/archtest/layers_test.go` gained
  `"internal/content/setup": 3`: it is layer 3 because it is exactly the
  same kind of thing `internal/content` and `internal/verify` already are,
  pack-supplied data turned into sandbox actions, and it imports
  `internal/content` (same layer, one-way: `internal/content` must never
  import `internal/content/setup`, a rule the layer numbers cannot catch
  since both sit at 3, so it is stated in prose in both packages'
  `doc.go` files instead), `internal/runtime` (L1), and
  `internal/platform`/`internal/platform/ux` (L0).
- **`SF_STATE`'s default moved from `/opt/shellforge/state` to
  `/home/learner/.shellforge`.** `/opt/shellforge` is not a real bind mount
  on the Docker backend today, which is why writing to
  `/opt/shellforge/state` has worked so far, but `CLAUDE.md`'s non-negotiable
  number 2 says it is the one host mount and it is read-only, and the WSL
  backend plus the Day 3 decision are expected to make that literally true.
  This move fixes a latent defect before it can bite rather than after:
  `internal/runtime/runtimetest/contract.go` has asserted against
  `/home/learner/.shellforge` as `contractStateDir` since Day 1, so the
  runtime contract suite was already living with this default while the
  image and the demo level used the old one. Everything that named the old
  path moved with it in this same commit: `images/rc/instrument.bash`,
  `images/bin/_sf-request`, the `Containerfile` (which now creates
  `/home/learner/.shellforge` as `learner:learner` 0755 instead of creating
  and chowning `/opt/shellforge/state`), `internal/sandbox/demo_level.go`'s
  `demoStateDir`, and the CI image smoke assertion, which now also checks
  the new directory exists, is learner-owned, and is writable by learner.
  Two test-data updates are not a loosened assertion, and are recorded here
  as such: the literal path in `cmd/shellforge/cmd_run_test.go`'s
  `TestPrepareControlChannelRecreatesTheFifos` and in
  `internal/verify/shell_checks_test.go`'s fixture data are arbitrary
  strings that only needed to be internally consistent with themselves, and
  now read as the new default instead of the old one, matching the
  deliberate contract change rather than working around a test that was
  actually right.
- **`docs/design/ARCHITECTURE.md` was not touched**, on purpose, per
  `CLAUDE.md`'s rule that the design record describes intent and the code is
  right when the two disagree. Its lines describing `/opt/shellforge/state`
  are now stale prose, not a bug; recorded here so it is discoverable rather
  than silently drifting.

Not done, deliberately:

- No wiring into `internal/game`, `cmd/shellforge`, or the pack loader.
  Nothing outside this package's own tests constructs a `Runner` yet. That
  is #52 (orchestrator), #53 (the full `Level` type and the pack loader),
  and #54 (check wiring against a real level).
- No `reset --hard`, no sandbox rebuild, no snapshot or rewind, and no
  generator kind beyond `loglines`.
- No fix for `Session.PushFiles` corrupting a binary asset containing the
  literal bytes `0d 0a`; recorded as `// TODO(v0.2)` in `runner.go` with a
  `binary` flag on `FileSpec` as the intended shape, matching what
  `internal/runtime/runtimetest` already flags as gap F3.
- `TestCRLFScriptRunsInsideTheSandbox` is a `t.Skip` placeholder rather than
  a real Docker-backed test: this package has no `runtime.Session` fixture
  wired to a live sandbox yet, so proving a CRLF-authored `.sh` executes
  inside a real container end to end is left for whichever ticket first
  gives this package (or its caller) that fixture, most likely #52.
- The follow-up ratification issue against #53 for the `Level` type
  contract is not opened: this session implements code on an existing
  branch and does not touch GitHub issues, labels, or pull requests.

Gates run locally, all green: `gofmt -s -w .`, `go vet ./...`,
`go test ./...`, `go test -race ./...`,
`go test ./internal/archtest/...`, `./scripts/check-punctuation.sh`,
`./scripts/check-links.sh`, `./scripts/check-allowlist-regexp.sh` (run after
staging the new files, since it enumerates with `git ls-files` and cannot
see an unstaged file), and `python3 scripts/check-ci-gates.py`. Docker is not
running in this environment, so `TestCRLFScriptRunsInsideTheSandbox` skips
cleanly rather than running for real, and the `Containerfile` and CI image
smoke assertion changes are unverified locally; CI is authoritative for
both. `govulncheck` and `gosec` are not installed here and were not run;
`pytest` is not installed either, so `scripts/tests/` did not run locally,
though `check-ci-gates.py` itself, a plain script rather than a pytest
suite, did.

### Day 2 follow-up, 2026-08-13: review fixes on #83

An independent review of PR #83 found three blocking gaps and a set of
smaller ones. Fixed on the same branch, each guard mutation-tested the same
way the review found the originals: delete it in a scratch copy, rerun the
package suite, confirm it fails and names the right test.

Blocking:

- **`Setup` never created `setup.root`.** It relied on `PushFiles` creating
  the root as a side effect of writing files into it, but `setup.files` is
  optional, so a level with only a `setup.script` got a `chown` and a
  `WorkDir` against a directory nothing had created, and both failed. Five
  existing tests used exactly that shape and passed only because the fake
  answers exit 0 to an unscripted call. Fixed with `mkdir -p -- <root>` as
  the learner, after `Teardown` and before `PushFiles`, its exit code
  checked like every other step. `TestSetupCreatesLevelRootWithNoFiles`
  pins it against a new `dirAwareFakeSession`, which tracks which
  directories `mkdir -p` actually created and answers `chown` and `bash -c`
  non-zero against one that was not, the way the real docker backend would.
  Deleting the `mkdir` step in a scratch copy fails exactly that test.
- **Every validation branch in `buildFileEntry` was untested.** The review
  deleted five guards at once (exactly-one-of source/content/generate, the
  empty-or-absolute path check, the path-escapes-root prefix check,
  `fs.ValidPath` on `source`, the `maxManifestBytes` cap) plus the
  `ownerPattern` check on its own, and the suite stayed green throughout.
  `TestBuildFileEntryRefusesInvalidSpecs` is a table test covering all of
  those plus the path's own pre-Clean `..` segment loop, mutation-tested one
  guard at a time. Two of the review's own listed inputs turned out not to
  pin what they were meant to: `../../etc/passwd` escapes far enough that
  the abs/prefix check catches it independently of the segment loop, so a
  second case, `logs/../notes.txt` (which cleans to a path that legitimately
  sits under root), was added to actually pin the segment loop; and
  `source: ../outside.txt` against `fstest.MapFS` is refused by `MapFS.Open`
  itself before `buildFileEntry`'s own `fs.ValidPath` check ever runs, so
  that one case runs against a new `permissiveFS` test double that does not
  validate at all, which is what makes the case prove buildFileEntry's own
  check is load-bearing. `TestBuildManifestRefusesAnOversizedManifest`
  covers the byte cap separately, since it lives in `buildManifest` rather
  than `buildFileEntry`.
- **`TestRefusesLevelRootParentTraversal` and
  `TestRefusesLevelRootTraversalToEtc` did not pin the guard they are named
  after.** Both inputs clean to something a later branch (the `/` result or
  the prefix check) catches on its own, so deleting `validateLevelRoot`'s
  pre-Clean `..` segment loop left the suite green. Both tests now also
  assert the error names the segment ("it contains a .. segment") rather
  than a later branch's message, which is what makes them fail when the
  loop is removed instead of merely observing that something eventually
  refused.

Suggestions applied, each verified not to regress the suite:

- `resolveLevelRoot` re-validates the resolved root against both
  `validateLevelRoot` and `refuseIfInsideStateDir` after the `readlink`
  round trip, matching `internal/sandbox/demo_level.go`'s prior art. Neither
  call can fail today because resolution is refused unless it is an exact
  identity; both stay for the same reason the prior art keeps them, in case
  a future change relaxes that equality check.
- The state directory guard moved out of `resolveLevelRoot` and into
  `sentinelPath`, the one place `Setup`, `Teardown`, and `IsSetUp` all go
  through. Before this, `IsSetUp` built an argv from `r.stateDir` with no
  validation at all; `TestIsSetUpRefusesAnUnsafeStateDir` pins the new
  coverage, and deleting the check in `sentinelPath` fails it along with
  `TestSentinelPathRefusesAnUnsafeStateDir` and `TestRefusesAnUnsafeStateDir`.
- `TestNoHostFilesystemRemovalInPackage`'s doc comment claimed it fails on
  any import of `"os"` outside its own allowlist, but it only ever walked
  call expressions for a literal `os.` identifier, which an aliased import
  (`import osx "os"`) would defeat. The test now also fails if any non-test
  `.go` file in the package imports `"os"` at all, checked by import path
  rather than identifier, which is both stronger (also catches `os.Create`
  and `os.OpenFile`, which the old call-name list did not name) and
  actually matches what the comment already said. No non-test file in the
  package imports `os` today, so this passes without any other change.
  Verified: an aliased `osx "os"` import added to a scratch copy is caught.
  PROGRESS.md's own claim in the Day 2 entry above, that nothing is staged
  to a host path on disk, is now literally backed by this check rather than
  by the narrower call-name list alone.
- The three stale `/opt/shellforge/state` literals in
  `internal/verify/snapshot_test.go` now read `/home/learner/.shellforge`,
  matching what the Day 2 entry above already claimed was done everywhere.
  They are arbitrary fixture strings with no behavior riding on the exact
  value, so this is a wording fix, not a loosened assertion.
- A `mode` whose parsed value carries a bit above `0o777` (for example
  `"7777"`) is now refused rather than silently masked down to `0777` by
  the docker backend's `e.mode.Perm()`. `TestBuildFileEntryRefusesInvalidSpecs`
  covers it.

Declined:

- The other half of the same suggestion, letting `content: ""` express an
  intentional empty file, is not applied. It needs a schema change to
  `FileSpec` (a `*string` or an explicit `empty: true` field), which touches
  the same `content.Level` surface issue #53 is expected to extend, and
  making that call now would preempt the ratification issue this session
  cannot open (see below).
- `TestSetupOnDirtyRootMatchesSetupOnCleanRoot` claimed to pin criterion 1's
  idempotency but simulated nothing: `Setup` never calls `IsSetUp`, so the
  scripted `test -f` answer was never consulted, and the test compared two
  manifests built by the same pure function from the same input, which
  cannot differ. Building a real in-memory sandbox-state simulation strong
  enough to make this test mean something is a bigger, riskier piece of
  work than a review-response commit should carry, so the test is deleted
  instead. This half of criterion 1 (setup on a dirty root behaves like
  setup on a clean root) has no test today and needs a live-session fixture
  to get one properly; recorded here rather than left as a test that reads
  as coverage while asserting nothing.

Answers to the review's two questions:

- **`stripCRLF` versus `CLAUDE.md` non-negotiable 7: resolved by #84.**
  `stripCRLF` and its docker-side twin `stripCR` both rewrite only the `\r`
  in a `\r\n` pair, and always did; rule 7 read as an unconditional strip of
  `\r`, which was the specification drift, not the code. #84 resolved as
  option 1, narrowing rule 7's wording (and six other copies of the same
  claim) to match what every implementation already does, rather than
  option 2, widening every strip and adding a `FileSpec.binary` field no
  shipped asset needs. The evidence for narrowing: no packed asset is
  anything but `yaml`, `log`, `md`, or `go`; a lone-CR line ending has not
  been produced by a mainstream tool since Mac OS 9; and
  `internal/pty/testdata/vim-session.bin`, already marked binary in
  `.gitattributes`, proves a byte sequence an unconditional strip would
  corrupt already exists in this repository.
  `TestStripCRLFLeavesALoneCarriageReturn` is the test that keeps the
  narrowed rule enforced, since `internal/content/setup` had no direct
  `stripCRLF` test before it.
- **The `content.Level` contract with #53: ratified by #85.** The contract
  held exactly as this session predicted: `Level` moved to
  `internal/content/level.go` as #53 was permitted to do, nothing was
  redeclared, and `SourceFile` carries `yaml:"-"`. `Mode` stayed a string,
  `Teardown` stayed a one-field struct, and `content: ""` was answered by a
  third route neither this session nor #85 anticipated: `FileSpec.ContentSet`,
  set by a hand-rolled `UnmarshalYAML`, distinguishes an absent `content:`
  key from a present, empty one, which is what lets `files-01`'s `.gitkeep`
  exist. #85 is closed by the PR that carries this paragraph.

Not touched, reported instead: the review's `ci.yml` doc-anchor gate
finding is correct (`grep`ing only the struct-literal `DocAnchor: "..."`
form matches one anchor that lives in a comment, and misses all 52 real,
positional `ux.Fail` call sites) but changing a CI gate is out of scope for
a review-response commit. All five doc anchors this package emits do have
headings in `docs/05-troubleshooting.md`, checked by hand as the review
itself did.

Gates run locally, all green: `gofmt -s -l .` (clean), `go vet ./...`,
`go build ./...`, `go test ./...`, `go test -race ./...`,
`./scripts/check-punctuation.sh`, `./scripts/check-allowlist-regexp.sh`
(run after staging the new `dirawaresession_test.go`, since it enumerates
with `git ls-files`), `./scripts/check-links.sh`, and
`python3 scripts/check-ci-gates.py`. `./scripts/check-cli-package.sh` also
run and green, unaffected by this change. Docker is not running in this
environment, `govulncheck` and `gosec` are not installed, and `pytest` is
not installed, so none of the three ran; same gaps as the Day 2 entry
above.

### Day 2, 2026-08-13: the store schema, migrations, and the command journal

Issue #51. Two packages: `internal/store` (layer 0) reshaped from its Day 0
stub into a real schema, embedded forward-only migrations, and a pragma set
tuned for a hot-path writer; `internal/journal` (layer 2), empty except for
`doc.go` before this, now holds the command journal itself and the TSV
reader. Nothing outside their own tests calls either package: no wiring into
`cmd/shellforge`, `internal/game`, or `internal/pty`, exactly as the ticket's
own out-of-scope list requires. No new dependency; `modernc.org/sqlite` was
already required and already approved, and `go.mod`/`go.sum` are unchanged,
confirmed by their absence from `git status` after this branch's own work.

- **`store.Open` is reshaped to `Open(ctx, path) (*Store, error)`**, not the
  ticket's `Open(path string)`. `Open` creates parent directories, creates a
  file, chmods it, and runs migrations, and CLAUDE.md requires `context.Context`
  first on anything touching the filesystem; `Open` had zero callers, so
  nothing broke by adding it. `Store` wraps a `*sql.DB` and the path; `DB()`
  is the documented escape hatch for a layer 2 package that owns its own
  tables, parameterized queries only, never closed by the caller.
- **A single-connection pool is load bearing, not an optimization.**
  `busy_timeout` and `synchronous` are per-connection pragmas and
  `database/sql` pools connections, so without `SetMaxOpenConns(1)` a pragma
  set through `db.ExecContext` would silently apply to one pooled connection
  and not to the next one the pool opens under concurrent load. WAL mode, a
  5 second `busy_timeout`, and `synchronous=NORMAL` are set as named
  constant strings (a `PRAGMA` cannot take a bound parameter, so this is the
  only way to keep parameterized-queries-only while still setting one).
  NORMAL is what keeps `Append` off `fsync` on every command; the accepted
  cost, a power loss can lose the last few journal entries, is fine because
  the journal is never evidence for scoring.
- **Migrations are embedded under `internal/store/schema/*.sql`**, named
  `NNN_name.sql`, parsed, sorted, and checked for gaps and duplicates by an
  unexported `migrations()`. Each file runs as one argument-free
  `ExecContext` inside its own transaction (the modernc driver only executes
  every statement in a multi-statement string when no arguments are bound),
  followed by a parameterized `DELETE FROM schema_version` then `INSERT`,
  which keeps exactly one row in that table whatever state it was in before.
  A database recording a version newer than this build understands is
  refused with `ErrSchemaTooNew`, wrapped in a `*ux.Error`: nothing is
  written, no migration runs, and the file is left exactly as it was. There
  are no down migrations.
- **Only `schema_version` and `events` ship in `001_init.sql`, and this
  conforms to `docs/design/ARCHITECTURE.md` section 4.7, the section issue
  #51's own acceptance criterion names.** 4.7 already specifies "Stored in
  SQLite (`events` table)" with a flat journal record carrying `seq`, `ts`,
  `level`, `cwd`, `raw`, `exit`, `duration_ms`, `used_tab`, `used_history`,
  which is exactly the shape `Entry` and the `events` table both have,
  modulo two column renames (`level_id` for 4.7's `level`, `exit_code` for
  `exit`) and three fields 4.7 lists but this ticket defers with output
  capture (`output_sha256`, `output_head`, `output_bytes`, absent from
  `Entry`). What actually conflicts is section 4.11 elsewhere in the same
  design record, which describes an unrelated, later-stage persistence
  layer: a singular `event` table with `attempt_id`, `kind`, and an opaque
  `payload_json`, plus five more tables (`profile`, `pack`, `level_state`,
  `attempt`, `concept_mastery`, `achievement`) this ticket does not need.
  `Entry` has flat typed fields and no `attempt_id` and no `kind`, and the
  acceptance criterion demands an appended `Entry` read back with every
  field intact, which an opaque JSON payload cannot satisfy without
  inventing an encoding nobody specified. An earlier version of this entry
  described this as the code drifting from the design record; that framing
  was backwards; the drift is between 4.7 and 4.11 inside
  `docs/design/ARCHITECTURE.md` itself, the code matches the section this
  ticket cites, and reconciling 4.7 against 4.11 is its own follow-up,
  tracked in issue #87, not a reason to move the schema toward 4.11's
  `payload_json` later. `docs/design/` describes intent, and the code is
  right per CLAUDE.md when parts of the design record disagree with each
  other, not only when the record disagrees with the code.
- **Four new doc anchors, four new headings in `docs/05-troubleshooting.md`
  in this same commit:** `progress-db-unwritable`, `progress-db-corrupt`,
  `progress-db-too-new`, `progress-db-migration-failed`. Every remediation
  tells the learner to rename their progress file, never to delete it,
  matching the destructive-safety skill's stance that this package destroys
  nothing belonging to a learner; the only `DELETE` anywhere is the one
  inside a migration's own transaction, on the single-column
  `schema_version` table this package owns. `classifyOpenError` picks
  between the unwritable and corrupt anchors by attempting `os.OpenFile`
  with `O_RDWR` and no `O_CREATE` and checking `errors.Is(err,
  fs.ErrPermission)`, never by string-matching a driver message. A fifth
  anchor, `progress-db-in-use`, followed in a later review-fix commit: see
  below.
- **`TestOpenRejectsAnUnwritableDirectory` is replaced, not merely edited.**
  The ticket requires `Open` to create parent directories, which makes that
  test's premise (a path inside a nonexistent directory must fail) provably
  wrong under the new contract. It is replaced by two tests, net coverage
  up: `TestOpenCreatesMissingParentDirectories` pins the new creation
  behavior and its 0700/0600 modes, and `TestOpenRejectsAnUnwritableParent`
  keeps a genuine unwritable-parent failure path and strengthens it with
  `*ux.Error` field assertions. This is the one assertion change the run
  prompt authorized in advance.
- **`internal/journal` declares `Entry`, `Scope`, `ScopeKind`, `Journal`,
  `New`, `Append`, `Level`, `Commands`, `Err`, and `ReadTSV`,** all reaching
  SQLite only through the `*sql.DB` `store.Store.DB()` returns, never by
  importing `modernc.org/sqlite` directly:
  `internal/archtest/dependencies_test.go` confines that driver to
  `internal/store` alone, and `TestConfinedDependenciesStayConfined` still
  passes. `ts` is stored as `REAL` unix seconds with microsecond precision
  (`float64(e.TS.UnixMicro())/1e6` on the way in,
  `time.UnixMicro(int64(math.Round(v*1e6))).UTC()` on the way out); a
  nanosecond component finer than a microsecond does not survive the round
  trip, which is a documented contract, not an accident, and both the exact
  round trip and the deliberate truncation have their own test.
- **A real bug caught by `TestCommandsForEveryScopeKind`, not written in
  from the start: `ScopeLastN` and `ScopeLast` first ordered by `seq DESC,
  id DESC`, and `seq` is per level, not global.** Two levels playing
  interleaved commands sorted by `seq` first put every level's first command
  before either level's second command, not in the order the commands
  actually ran. Fixed by ordering `ScopeLastN`, `ScopeLast`, and the
  `ScopeLevel` newest-level lookup by `id` alone, the auto-increment row
  identity, which is the only column here with a global, monotonic meaning;
  `ScopeLevel`'s own per-level replay still orders by `seq ASC, id ASC`,
  where `seq` is exactly the right key. Caught because the test fixture
  interleaved two levels on purpose, per the plan; a fixture using only one
  level would not have caught this.
- **`Commands` cannot return an error, since `verify.JournalReader` fixes
  its signature, so a query failure is recorded rather than swallowed.** It
  returns an empty slice and stores the error under a mutex, retrievable
  through `Err()`. Verification never reads the journal to decide pass or
  fail (see `doc.go`), so a degraded command log costs only the efficiency
  bonus, never correctness. `Commands` also builds its own 2 second bounded
  context internally, since its fixed signature carries no `ctx` parameter,
  so a wedged read cannot stall verification.
- **`*journal.Journal` does not literally satisfy `verify.JournalReader`,
  and this is stated plainly rather than glossed over.** `internal/journal`
  is layer 2 and `internal/verify` is layer 3; CLAUDE.md's dependency rule
  and `internal/archtest`'s layer table both forbid an upward import in
  production code, and this outranks the ticket's own fixed signature
  `Commands(scope verify.Scope) []string`, which would force exactly that
  import. `internal/journal` instead declares its own `Scope` and
  `ScopeKind`, field-for-field and value-for-value identical to `verify`'s,
  and `verifycontract_test.go` (package `journal_test`, so it is a _test.go
  file `internal/archtest`'s own `collectImports` never parses, confirmed by
  reading `layers_test.go` rather than assumed) holds a three-line adapter
  plus `var _ verify.JournalReader = reader{}`. A `reflect`-based drift test
  in the same file, `TestJournalScopeMirrorsVerifyScope`, fails and names
  the drift if `verify.Scope`'s field names, field types, or `ScopeKind`
  constant values ever change without a matching update here. So the
  acceptance criterion "`Commands` satisfies `verify.JournalReader` ...
  without `internal/verify` importing this package" is met in substance
  (the method set is pinned at compile time, drift is caught, `verify`
  still imports nothing from this package) and not in letter (the parameter
  type `Commands` actually takes is `journal.Scope`, not `verify.Scope`).
  The durable fix, moving `Scope` and `ScopeKind` into a small layer 0
  package both sides alias, needs its own ticket: it edits an exported type
  in `internal/verify`, which this ticket does not authorize.
- **`ReadTSV` parses `images/rc/instrument.bash`'s four-field TSV format**
  with `bufio.Reader.ReadString('\n')`, not a `Scanner`, because a `Scanner`
  cannot tell whether the final line had its terminator, and that
  distinction is the truncation rule. Two different forgiveness rules, both
  tested with their own named table rows: a truncated final line (no
  trailing newline) is dropped even if it would otherwise parse, because a
  `printf` writes a line and its newline in one write, so a missing
  terminator means the write was cut short; a malformed line anywhere else
  (fewer than four fields, an unparsable timestamp, an unparsable exit code)
  is skipped wherever it sits, and the lines around it still come back.
  `EPOCHREALTIME`'s radix character is accepted as either `.` or `,`, since
  a comma-decimal locale emits a comma and rejecting it would silently empty
  the journal for that learner; the fraction is zero-padded or truncated to
  six digits and parsed as an integer, so there is no float rounding on the
  way in. `Seq` numbers only the accepted lines, not the file's own line
  count, and `LevelID` is left empty for the caller to stamp.
- **Journal contents are never logged or printed, enforced by an AST scan,
  not just a comment.** `internal/journal`'s production source cannot import
  `log` or `log/slog`, cannot call any `fmt` function except `fmt.Errorf`,
  and cannot reference `os.Stdout` or `os.Stderr`; `Entry.String` and
  `Entry.GoString` are built with `strings.Builder` and `strconv` rather
  than `fmt.Sprintf` specifically so that this holds even in the one place
  this package formats an `Entry`. A second scan asserts no call anywhere in
  the package takes `.Raw` as a direct argument except
  `ExecContext`/`QueryContext`/`QueryRowContext`/`Scan` (the parameterized-
  query and read-back paths) and `len` (which reveals only a byte count,
  never content, exactly what the redacted `String`/`GoString` output
  needs). `TestEntryStringRedactsRaw` and `TestEntryGoStringRedactsRaw` are
  the behavioral half: `%v`, `%s`, and `%#v` all contain `[redacted N
  bytes]` and never the command text or any substring of it. The
  `bug-report` clause of the acceptance criterion is not asserted here,
  since no `bug-report` command exists in the tree yet.
- **Two package-local AST guards, `TestStorePackageCallsNoFilesystemRemoval`
  and `TestJournalPackageCallsNoFilesystemRemoval`, replace refusal tests
  for a destructive path that does not exist in this ticket.** Each asserts
  its own package's production source never calls `os.Remove`,
  `os.RemoveAll`, `os.Truncate`, `os.Rename`, `os.WriteFile`, or
  `os.Create`, and never imports `os/exec`; `internal/store`'s version
  additionally allows `os.Chmod`, since `Open` calls it once to restrict a
  file's own permissions, which only ever narrows access. Both are
  source-level tripwires, not proofs: stated limits (package-local, cannot
  see an indirect call through a function value or a `database/sql`
  statement that drops a table) are in each test's own doc comment.
- **`TestDocAnchorsHaveTroubleshootingHeadings`** walks up to the module
  root, reads `docs/05-troubleshooting.md`, and asserts a heading exists for
  each of `internal/store`'s four anchor constants. This is the honest local
  guard for this package: `ux.Fail` here passes the anchor positionally,
  which the CI Docs job's `DocAnchor: "..."` grep cannot see at all, so this
  test is stronger than that gate for this package specifically, not a
  workaround for it.
- **`TestDatabasePathIsNotUnderCacheDir`** duplicates an existing assertion
  in `internal/platform` on purpose: it guards this package's own assumption
  that a cache wipe never removes the progress database, stated here rather
  than only where `platform.CacheDir` and `platform.DatabasePath` are
  defined.
- **`TestConcurrentStoresOnOneFileDoNotDeadlock`** opens two independent
  `*Store` values on the same path (two connections, which is what makes
  this a real lock contest) and drives 50 inserts plus 50 reads from each
  through raw parameterized SQL against `s.DB()`, not through
  `internal/journal`: `internal/store` must never import `internal/journal`,
  since that would be an upward layer 0 to layer 2 edge. Runs clean under
  `-race`. Stated limit: two handles in one process approximates two
  processes, since SQLite locking is per connection, but does not exercise
  cross-process file locking on a network filesystem or a Windows share.
- Order of work followed the plan: failing tests were written first for
  both packages. For `internal/store`, whose `Open` already existed as a
  stub, the new tests and the reshaped implementation were written together
  rather than strictly test-then-code, and the one test confirmed red before
  its fix was `TestDocAnchorsHaveTroubleshootingHeadings`, which failed
  correctly (four missing headings named exactly) before the docs section
  was added. For `internal/journal`, which had no non-`doc.go` source at
  all, every test file was written first and `go vet ./internal/journal/...`
  failed to compile, naming every missing type (`Journal`, `Entry`, `Scope`,
  `ScopeLevel`, and more) before a line of `journal.go` or `tsv.go` existed;
  that compile failure is the honest red phase for that package, confirmed
  by running it, not assumed. The scope-ordering bug above was the one real
  assertion failure this branch hit after both packages compiled, caught by
  `TestCommandsForEveryScopeKind` and fixed before moving on.

Not done, deliberately, per the ticket's own out-of-scope list:

- No wiring into `cmd/shellforge`, `internal/game`, or `internal/pty`.
  Nothing calls `store.Open` after this ticket either, exactly as before it.
  `CommandEvent.Raw` on the host-side event stream is still always empty.
- No `profile`, `pack`, `level_state`, `attempt`, `concept_mastery`, or
  `achievement` tables, and no progression, scoring, streak, mastery, or
  achievement reads or writes. Day 4, in `002_*.sql`.
- No replay, no `used_tab`/`used_history` detection (both fields exist and
  are persisted, and stay `false`), no command output capture
  (`output_sha256`, `output_head`, `output_bytes` do not exist on `Entry`),
  no `bug-report` command, no down migrations, no writer for `journal.tsv`
  (this ticket only reads what `instrument.bash` already writes), and no
  per-line size cap in `ReadTSV` (`// TODO(v0.2)`).
- No change to `internal/verify`, `internal/archtest`'s tables, any script
  under `scripts/`, `.github/workflows/ci.yml`, `go.mod`, or `go.sum`.

Gates run locally, all green: `gofmt -s -w .`, `go vet ./...`,
`go build ./...`, `go test ./...`, `go test -race ./...`,
`go test ./internal/archtest/...`, `./scripts/check-punctuation.sh`,
`./scripts/check-links.sh`, `python3 scripts/check-ci-gates.py`, and
`./scripts/check-allowlist-regexp.sh` (run after `git add`, since it
enumerates with `git ls-files` and cannot see an unstaged file). Docker is
not running in this environment and this ticket needed nothing from it.
`govulncheck` and `gosec` are not installed here, and `pytest` is not
installed either, so `scripts/tests/` did not run; CI is authoritative for
all three, same gap as every session above that noted it.

### Day 2 follow-up, 2026-08-13: close a descriptor leak in classifyOpenError, caught by Windows CI

Second commit on issue #51, `09a80ce`, not recorded here at the time; added
now rather than left silent. `classifyOpenError`'s permission probe opened
the progress database with `os.OpenFile` and discarded the returned
`*os.File` without closing it, leaking a descriptor on every failed `Open`
that classified as corrupt. Unix tolerates this: an unlinked file with an
open handle still goes away, so the whole suite passed locally on Linux.
`windows-latest` does not tolerate it, and its Test job failed `t.TempDir`
cleanup on both `TestOpenOnACorruptFileFails` and
`TestOpenOnACorruptFileLeavesItUntouched` with "The process cannot access
the file because it is being used by another process". A Linux-only local
gate run cannot substitute for the Windows job for exactly this reason: the
platform difference hid the leak, it did not cause it, and the fix belongs
in the code rather than in a skip.

The probe now captures the handle and closes it immediately; nothing is
read from it, since the errno alone answers permission versus corrupt.
`TestOpenOnACorruptFileLeaksNoDescriptor` is the new regression guard,
counting `/proc/self/fd` across twenty failed `Open` calls so this fails on
Linux too rather than only on a Windows runner; it was mutation-verified
before that commit landed (descriptors grew 8 to 28 with the close removed,
flat with it in place), and it skips off Linux deliberately, stated in its
own doc comment. The same line was the Security job's only `gosec` finding,
`G304` (potential file inclusion via variable), and now carries a `#nosec`
with a written justification in the house style: the path is the progress
database path this package was handed, already opened by the sqlite driver
by the time this runs, and the open carries neither `os.O_CREATE` nor
`os.O_TRUNC`, so it cannot create or destroy anything.

### Day 2 follow-up, 2026-08-13: review fixes on #51, a wrong-lock diagnosis and a wrong-level answer

Third commit on issue #51, addressing an independent review's 15 findings:
two blocking, both real defects with a learner-visible failure mode, plus
suggestions triaged one by one.

- **Blocking: `classifyOpenError` misclassified a locked database as a
  corrupt one.** Two Shellforge processes starting together race to convert
  a fresh database from delete mode to WAL, which needs a brief exclusive
  lock that `pragmaBusyTimeout`'s own busy handler does not cover, and the
  loser saw `SQLITE_BUSY` land in the corrupt bucket, whose remediation
  told the learner to rename their progress file and lose every level they
  had finished, even though nothing was wrong with the file. Fixed on the
  driver's own result code, not a string match:
  `errors.As(cause, &*sqlite.Error)` and `Code() & 0xff` compared against
  SQLite's own numeric `SQLITE_BUSY` (5) and `SQLITE_LOCKED` (6), verified
  against a real contended conversion in a scratch program before relying
  on it in the fix. `internal/store` now imports `modernc.org/sqlite` by
  name, not only as a blank driver import; `TestConfinedDependenciesStayConfined`
  still passes, since the confinement table restricts the import path to
  this package, not the import form, and this adds no new dependency, since
  the module was already required. A new anchor, `progress-db-in-use`, with
  a matching `docs/05-troubleshooting.md` heading, tells the learner to
  close the other copy of Shellforge rather than rename or delete anything.
  `execPragmaWithRetry` retries a busy or locked pragma up to 8 times with a
  25 ms pause between attempts (a 200 ms bound, well under anything a
  learner would notice) honouring `ctx` cancellation, which is what makes
  the simultaneous-start race resolve instead of fail.
  `TestOpenOnAContendedConversionReportsInUseAndRetries` holds a write
  transaction on a plain, non-WAL database, asserts `Open` reports
  `progress-db-in-use` (never mentioning rename or delete) rather than
  `progress-db-corrupt`, then releases the transaction from a goroutine and
  asserts a second `Open` succeeds, which is what proves the retry loop
  works rather than merely compiling.
  `TestConcurrentStoresOnOneFileDoNotDeadlock` is unchanged and stays as the
  steady-state case: its two `Open` calls both land after the database is
  already in WAL, so it never contended for the conversion in the first
  place, stated now in its own doc comment; the new test is what exercises
  the contended moment.
- **Blocking: `journal.Journal.ScopeLevel` answered with the wrong level's
  history.** It inferred the level from the newest row in the whole
  `events` table, so a learner who finishes one level, starts the next, and
  runs `check` before typing anything got the previous level's commands
  back, and a replay saw every earlier attempt's commands forever, since
  the table is persistent. `internal/verify/journal_checks.go` already
  defaults an empty `scope` to `ScopeLevel`, so `command_matched` could pass
  wrongly and `command_not_matched` could fail a learner for a command they
  ran on a different level: exactly the "level accepts a wrong answer"
  failure mode CLAUDE.md singles out. Fixed with a new `SetLevel(levelID
  string, sinceID int64)` method, mutex-guarded like the existing `err`
  field, that layer 4 calls when a level starts or resets; `New`'s
  signature is untouched. `ScopeLevel` now queries `WHERE level_id = ? AND
  id > ?` against the stored level and boundary, ordered `seq ASC, id ASC`
  as before. Before `SetLevel` is ever called, `Commands` returns an empty
  slice and records an error retrievable through `Err`, rather than
  guessing from the newest row; the guess (`newestLevelID`) is deleted
  outright, not kept as a fallback. `ScopeLevel`'s doc comment now states
  the exact boundary it implements.
  `TestCommandsForEveryScopeKind` now appends `nav-01` rows after the
  `pipes-03` rows it verifies, so a fixture where the newer level's rows
  have higher `events.id` than the level under verification is exactly what
  proves the newest-row guess is gone; a new
  `TestCommandsScopeLevelWithNoLevelSetReturnsEmptyAndErr` covers the
  no-level-set state. `verifycontract_test.go`'s behavioural test now calls
  `SetLevel` before exercising `ScopeLevel` through the adapter, since it no
  longer works without it. **Decision on `ScopeLastN` and `ScopeLast`:
  left with their existing global, level-agnostic semantics, deliberately,
  not silently.** Their exposure to the same wrong-level window is far
  smaller and self-correcting: a learner sees at most one stale command
  from the level they just left, and it ages out the moment they type their
  own first command in the new level, rather than persisting for the whole
  attempt the way the `ScopeLevel` bug did. Scoping them to the level
  boundary too is reasonable future work, stated in `journal.go` and
  `doc.go`, but it is not this ticket's fix and needs its own pass over
  every level using `last` or `last_n:N` scope today.
- **The 4.7 versus 4.11 framing, corrected.** `docs/design/ARCHITECTURE.md`
  section 4.7, the section issue #51's own acceptance criterion names,
  already specifies "Stored in SQLite (`events` table)" with the same flat
  journal record this package ships, modulo `level_id`/`level` and
  `exit_code`/`exit` naming and three deferred output-capture fields. The
  earlier entry above framed the shipped schema as drifting from the
  design record; that was backwards. The real drift is that section 4.11
  elsewhere in the same document describes an unrelated, later-stage
  persistence layer with a singular `event` table and an opaque
  `payload_json`, which contradicts 4.7 and was never implemented, is not
  implementable against this ticket's "reads back with every field intact"
  criterion, and is not evidence of this package being off-spec. Corrected
  in `internal/journal/doc.go` and in the Day 2 entry above; reconciling
  4.7 against 4.11 is tracked in issue #87, as a change to the design
  record, not to this package.
- **`internal/journal/tsv.go` per-line bound.** `bufio.Reader.ReadString`
  grows without limit, so a `journal.tsv` a learner filled with `yes >>
  journal.tsv`, or whose last line never terminates, could pull the whole
  thing into one Go string and exhaust host memory. A `TODO(v0.2)` marker
  was claimed in the Day 2 entry above but never existed in the code; since
  the bound is contained and cheap, it is implemented rather than the
  marker added. `readBoundedLine` reads with `br.ReadSlice('\n')`, not
  `ReadString`, specifically because `ReadString` would already have pulled
  an unbounded line fully into memory before this function ever saw its
  length; `ReadSlice` returns `bufio.ErrBufferFull` a few KiB at a time
  instead, so an over-long line is detected and its fragments discarded
  without ever holding more than one buffer's worth in memory. An over-long
  line is skipped the same way a malformed line already was. Two new table
  cases: an over-long record in the middle of a file (surrounding records
  still returned) and a file consisting of one unterminated over-long line
  (nothing returned).
- **A tab inside `PWD`, documented rather than fixed.** `instrument.bash`
  emits `PWD` unescaped as field three, so a working directory containing a
  literal tab truncates `Cwd` and leaves a garbage prefix on `Raw`, unlike a
  tab inside the command, which the four-field `SplitN` bound already
  handles safely. Fixing the producer is out of scope here: it would need
  the same percent-encoding `instrument.bash` already does for the OSC 7
  marker, kept consistent with issue #25's OSC 7 work rather than done
  twice differently. `parseTSVLine` and `ReadTSV`'s doc comments now name
  the limitation precisely, and a new table case pins the current,
  undesirable-but-documented behaviour.
- **`internal/store/migrate.go`'s empty `schema_version` table.**
  `schemaVersion` used to collapse "table does not exist" (a fresh
  database) and "table exists but holds no row" (not reachable from this
  package's own writes, but reachable from an external tool or a future
  migration touching the table) into the same "version 0", which made
  `Migrate` re-run `001_init.sql`'s `CREATE TABLE schema_version` against a
  database where that table already exists, failing with a "table already
  exists" error naming the wrong problem and leaving the database
  permanently unopenable. `schemaVersion` now counts rows explicitly and
  returns a distinct error naming the empty table, which `Migrate` already
  wraps through the existing `progress-db-migration-failed` anchor, no new
  anchor needed. `TestSchemaVersionOnAnEmptySchemaVersionTableIsAnError`
  deletes the row, reopens, and asserts the error names the empty table
  rather than "already exists", and that nothing was written.
- **`verifycontract_test.go` now pins the scope-kind set size, not only its
  known members.** `TestJournalScopeMirrorsVerifyScope` previously could not
  see a new `verify.ScopeKind` constant being added: it would stay green
  while `journal.commands`' `default` arm silently answered with no
  commands for the new kind. A `wantScopeKinds = 3` assertion with a
  comment pointing at `journal.commands`' switch now fails loudly instead.
- **Both packages' filesystem-removal guards closed three evasions.**
  `bannedFilesystemRemoval`/`journalBannedFilesystemRemoval` matched only
  the literal identifier `"os"`, only when a banned function was a
  `CallExpr`'s `Fun`, and had no entry for `os.OpenFile` or
  `syscall.Unlink`/`syscall.Ftruncate`. `import osx "os"; osx.Remove(p)`
  and `var rm = os.Remove` followed by `rm(path)` both passed the guard
  green, and a future `os.OpenFile(path, os.O_RDWR|os.O_TRUNC, 0o600)`
  would have truncated the learner's progress database undetected. Fixed by
  resolving each file's own import alias for `"os"`, `"os/exec"`, and
  `"syscall"` from its own `f.Imports` rather than matching the string
  `"os"`; by matching on bare `*ast.SelectorExpr` nodes rather than only a
  `CallExpr`'s `Fun`, so a banned function taken as a value is caught the
  same walk catches a direct call; by giving `os.OpenFile` a flag-aware
  check (`openFileRequestsTruncateOrCreate`) that permits the existing
  read-only probe and blocks a flag list mentioning `O_TRUNC` or
  `O_CREATE`; and by adding `syscall.Unlink` and `syscall.Ftruncate` to the
  banned set. Applied identically to both `internal/store/guards_test.go`
  and `internal/journal/guards_test.go`. Mutation-verified for all four new
  arms in both packages before committing: a scratch file exercising each
  evasion (`os.OpenFile` with `O_TRUNC`, the import-alias form, the
  function-value form, and `syscall.Unlink`) was added, confirmed to fail
  the guard, then removed, in each package.
- **`internal/journal/privacy_test.go`'s redaction matrix, expanded, and
  the one hole it cannot close, documented.** The existing tests covered
  only `%v`, `%s`, and `%#v` on a bare `Entry`. `TestEntryRedactionMatrix`
  now crosses `{Entry, *Entry, []Entry, []*Entry, map[string]Entry, an
  exported-field wrapper, fmt.Errorf("%v", e)}` against `{%v, %s, %q, %#v,
  %+v}` and asserts every combination redacts. The one shape that cannot
  redact, an `Entry` reached only through an unexported field of another
  type, is pinned as a known, asserted leak by
  `TestEntryRedactionHoleIsExactlyTheUnexportedFieldCase` rather than
  pretended closed, because reflect marks a value read through an
  unexported field as read-only, so `fmt` never calls `String` or
  `GoString` on it; no change to `Entry` closes this, including giving
  `Raw` its own named type. The rule this leaves is written on `Entry`'s
  doc comment, on `String`'s doc comment, and in `doc.go`: never embed a
  `journal.Entry` or `*journal.Entry` as an unexported field of anything
  formatted with `fmt`.
- **Two nits fixed:** `internal/store/doc.go`,
  `internal/store/guards_test.go`, and the Day 2 entry above all called
  `schema_version` a two-column table; `001_init.sql` declares it with one
  column, `version INTEGER NOT NULL`, matching ARCHITECTURE 4.11's own
  version of that table. All three corrected to "single-column".
- **Declined or deferred, with reasons:**
  - `internal/store/store.go:99`, `Open` writing into a foreign SQLite
    file it was not handed by `platform.DatabasePath`: deferred, not
    implemented. It has no caller today, the intended path is the fixed
    `platform.DatabasePath()`, and a refusal with its own anchor and
    refusal tests is a self-contained piece of work that deserves its own
    ticket rather than growing this branch further; flagged for the main
    session to file as a follow-up.
  - `internal/store/store_test.go:374`'s concurrency test not reaching the
    contended moment: subsumed by
    `TestOpenOnAContendedConversionReportsInUseAndRetries` above; the
    existing test is kept as the steady-state case, stated in its own doc
    comment now, rather than rewritten to do something it was never
    designed to do.

Gates re-run locally, all green: `gofmt -s -w .`, `go vet ./...`, `go build
./...`, `go test ./...`, `go test -race ./...`, `go test
./internal/archtest/...`, `./scripts/check-punctuation.sh`,
`./scripts/check-links.sh`, `python3 scripts/check-ci-gates.py`, and
`./scripts/check-allowlist-regexp.sh` (run after `git add`). Docker is
still not running here, `govulncheck` and `gosec` are still not installed,
and `pytest` is still not installed, so `scripts/tests/` still did not run;
CI remains authoritative for all three. `go.mod` and `go.sum` are
byte-identical to before this commit, confirmed by an empty `git diff --
go.mod go.sum`.

### Day 2, 2026-08-14: the verification engine, and `run` for any level

Issues #52 and #55, folded into one branch as the note on #52 planned. Both of
Day 2's outstanding exit criteria are implemented. Neither has been seen working
in a real container, which is the first thing to say about it.

**`internal/verify` is finished.** `compose.go`, `engine.go` and `result.go`, on
top of the fourteen check types #49 landed. `Build` resolves specs to checks once
at level load, so a malformed parameter surfaces before the learner sees a prompt
rather than on whichever `check` first hits it. `Run` evaluates in declaration
order with no short-circuit, so the objective checklist is always complete.
262 tests and subtests, green under `-race`.

Six contract decisions the ticket left open, all recorded in the PR body for
ratification:

- Composition mirrors `content.CheckSpec` on `verify.Spec`, and `Run` keeps the
  signature the ticket fixed by putting `LevelID` on `Env` and objective text on
  `Spec`. `Build` wraps each top-level check in an unexported `objective` carrying
  the metadata, so the two-method `Check` interface and all fourteen existing
  types are untouched.
- `objectives[].status` is the five `Status` values, not the two section 5's
  example happened to show.
- Section 4 rule 5 beats section 5's example, which set `primary_failure` to an
  `optional: true` objective and reported `passed: false` for a run whose only
  failure was that bonus. The example was corrected.
- Composites are structural, outside the registry, so `Types()` stays at fourteen.
- Composites short-circuit internally, and that is not a violation of the
  no-short-circuit invariant: a branch has no id and never appears in
  `Objectives`, so the checklist is still complete.
- The 60 second level budget marks the remaining objectives rather than dropping
  them, because rule 4's stated purpose for no-short-circuit is a fully populated
  checklist.

**`not` never launders an inconclusive result into a pass.** Child pass gives
fail, child fail gives pass, and a child timeout or error propagates unchanged.
If it inverted them, a sandbox that stopped answering would satisfy every `not`
in the pack, so a level would start accepting wrong answers at exactly the moment
something was already broken. This is the subtlest bug available in the engine and
it has its own test, verified to fail against the wrong implementation.

**`runGuarded` is what makes the 10 second per-check budget true.** Each check
runs on its own goroutine and `Run` gives up when the context is done, so a check
that ignores cancellation is still bounded. That is not hypothetical: the `script`
escape hatch runs author-supplied bash through one `Exec`. A panic is recovered
into `StatusError` there, because `recover` only catches a panic in its own
goroutine and an unrecovered one would kill the process, losing the whole
checklist with the host terminal still in raw mode. A check that never returns
leaks one goroutine, accepted deliberately and marked `TODO(v0.2)`: the
alternative is blocking the learner's prompt on a check that will not come back.

**The purity invariant is enforced for the first time.** `doc.go` has promised a
CI purity test since #49 and there was not one. `internal/verify/purity_test.go`
is the hermetic half: it drives all eleven Exec-calling check types through the
real registry and asserts every command they run is on a read-only allowlist,
that no argv names a mutating command or flag, that `find` is never handed
`-delete` or an `-exec` outside the allowlist, that two runs ask the sandbox
identical things, and that `PushFiles`, `PullFile` and `Attach` are never called.
Verified to fail without its fix: `dir_tree`'s `find -exec sha256sum` was changed
to `find -exec rm` and two assertions fired with two different explanations.

`find` gets its own test because it is the one allowlisted command that can be
made to mutate without changing `argv[0]`, and `dir_tree` already uses `-exec`, so
that door is open and what keeps it safe is which command goes through it.

Two exemptions, both bounded rather than assumed. `script` is exempt because its
body is one argv element to `bash -c` and therefore opaque to the package;
`TestScriptCheckIsTheOnlyExemption` asserts no other check type runs a shell, so a
second one adopting `bash -c` cannot quietly widen the hole while the allowlist
still passes. The two journal checks are exempt because they never call `Exec`,
which is asserted rather than commented.

**`internal/game` is a thin `Session`** and nothing more: load, setup, brief,
check, teardown. It declares its own `Verifier` interface, following the pattern
`content` uses for `TypeChecker`, so the whole thing is tested against a
two-method fake with no registry, sandbox or real level. It borrows the
`runtime.Session` it is given and never closes it, because one sandbox session
outlives one level. `doc.go` used to describe an event bus and pluggable scoring
as though they existed; it now says what is here.

It also holds the only `content.CheckSpec` to `verify.Spec` conversion, which has
to sit above both peers. Two tests hold the mirroring rather than trusting it: one
checks every field arrives, one pins both structs' field counts so adding a field
fails a test and forces a decision. That is a deliberate maintenance burden; the
alternative is a level silently verifying something other than what its author
wrote.

**One real schema ambiguity resolved, in the safe direction.** `optional` lives on
both `content.Objective` and `content.CheckSpec` and they can disagree. Either
saying optional now wins, because treating a disagreement as required would let a
level fail a learner on something its own checklist showed as a bonus. Marked
`TODO(v0.2)` and left for a content follow-up, since removing one changes the
level format.

**`run` no longer knows a level id.** It loads the embedded pack, looks the id up,
and plays what it finds. An unknown id fails through `ux.Fail` with the new
`level-not-found` anchor, naming every real id in campaign order and never
offering the demo. `parseRunArgs` takes the first campaign level as an argument,
which removed the three hardcoded references to `demo` in its messages.

Two adapters behind one `playable` interface keep the flow single: one for a pack
level, one for the Day 1 demo. That is what stops the FIFO plumbing, the teardown
ordering and the raw-mode rules existing twice, and those are exactly the parts
that were hard to get right.

**An anchor-losing bug fixed.** `runDemo` re-wrapped every `Setup` failure as
`level-state-corrupted`, discarding the specific anchor `setup.Runner` had already
attached: `unsafe-level-root` for a bad path, `setup-script-failed` for a failing
script. A learner following that link landed on a page about a corrupt level when
the real problem was something else. A wrong link is worse than no link.
`internal/game` has tests that catch the regression, verified against the old
behaviour.

**The demo level is kept, not deleted.** `internal/sandbox/demo_golden_test.go`
holds this repository's only live host-isolation test, the one that runs `rm -rf`
inside the sandbox and asserts a host canary is byte-identical, and its only live
filesystem purity check. Deleting them in the same change that introduces a
generic level runner would drop safety coverage while enlarging the surface, with
no moment where both old and new coverage are green. A follow-up issue carries the
deletion, gated on a YAML isolation test and the golden harness being green first.

**Rendering.** `render_check.go` is pure functions over a `LevelResult`, so the
learner-facing strings are testable with golden strings and no sandbox. A timeout
and an error are never dressed as wrong answers: each gets its own mark, its own
objective-line suffix, and a summary saying in words that this is not a wrong
answer, because the remediation is completely different. A missed bonus is dim and
labelled, never red. `crlf` is the single place the raw-mode rule is applied, is
idempotent, and leaves an authored CRLF alone so a pack written on Windows cannot
produce the doubled carriage return that renders as a stray blank column.

Replies are capped at 16 KiB with a truncation notice, because the `script` check
appends a failing script's whole stdout to its `on_fail`, so a flood is reachable
from a level rather than only from a bug.

**glamour, pinned to v0.10.0, and it does less than expected.** The pin is load
bearing: v1.0.0 declares `go 1.24.0` and would raise this module's floor, the same
trap `go.mod` already documents for `golang.org/x/term`.
`TestGoDirectiveStaysAtTheSupportedFloor` now fails if a future bump raises it.

The briefing uses this project's own style config rather than a standard one, and
the measurements are in the code because reaching for `WithStandardStyle` is the
obvious thing to do. Against nav-01's real 352 byte briefing: every standard
style, `ascii` and `notty` included, leaves the `##` heading marker in the output,
so a learner reads markdown that failed to render; and `dark` produces 5,698 bytes
with 669 escape sequences, because it paints each padding space individually. The
second matters beyond appearance, because `brief` sends its reply through the
capped FIFO. The custom style gives 695 bytes with 10 escape sequences and no
heading marker. Two tests pin both defects, verified to fail when the style is
switched back.

With colour off, nothing is styled at all, bold included. Bold is not colour, but
`ux` emits no escape whatsoever when colour is suppressed, and the reasons apply
equally: `TERM=dumb` cannot render attributes and a redirected stream is a file
where an escape is noise. The first version emitted bold under `NO_COLOR` and a
test caught it.

A rendering failure is never fatal: the raw markdown is printed between the
existing rules. Refusing to start a level because a formatting library choked
would be indefensible when the markdown is readable.

**#93 item 7, `files-04`'s golden test contract, was already closed** by #94
and #99: `Objective.Preserves`, the `preserves: true` inversion at golden
step 2, and the guarding tests
`TestPreservesObjectivesAreNotAlsoOptional` and
`TestEveryPreservingObjectiveIsADestructiveLevel` all shipped before this
PR. See `docs/LEVEL-FORMAT.md`'s "Golden test contract" section, the
paragraphs on `preserves: true`, for the written contract. Nothing to do
here beyond recording that item 7 needed no further work.

**#98's decisions 11, 12, and 13 are ratified as shipped**, PR
`chore/issue-93`. 12 is this project-specific glamour style and the
measurement behind it: the 352 byte briefing becoming 5,698 bytes with 669
escape sequences under every standard style, and every standard style,
`ascii` and `notty` included, leaving the `##` heading marker in the output,
which is the evidence that a hand-rolled renderer stays a live alternative
worth keeping rather than a detour back to `WithStandardStyle`. 13 is
already the behavior above: with colour off, nothing is styled, bold
included, because `TERM=dumb` cannot render attributes and a redirected
stream is a file where an escape is noise. 11, that `hint` and `reset`
answer honestly rather than partially, is ratified against the shipped CLI
dispatcher's current state: both verbs are still stubs that fail through
`ux.Fail` (see the CLI dispatcher row above), so "answer honestly" today
means refusing cleanly rather than pretending to work; the decision binds
whichever session wires either verb to `internal/game`.

**pipe-05 and its three committed assets.** The logs were produced by the
deterministic generator in `internal/sandbox/demo_level.go`, which already has
consistency tests including one asserting no noise line matches "error" in any
case, rather than written by hand. Measured on the committed bytes: 147
case-insensitive matches for `error`, and exactly `E401`, `E500`, `E503`.

Five tests in `internal/content` hold the assets to those answers with no Docker,
so they run on the Windows leg too. Verified against three real drift scenarios:
one extra ERROR line, a code changed to `E404`, and a noise line reading
"connection errors retried" added as INFO. The third is the subtle one: the
solution greps case-insensitively, so any line containing "error" counts whatever
its severity says, and a plausible-looking noise line would make the level's answer
quietly wrong.

**The golden contract is code, shared by `author test` and the Go test**, because
two definitions of "is this level correct" would drift and then disagree. It lives
in `cmd/shellforge` rather than `internal/game` because it needs a real sandbox and
`internal/archtest`'s `runtimeImplAllowed` deliberately excludes `internal/game`;
putting it there would have been the leak that set exists to prevent.

Step 2 asserts per objective rather than on `Passed` alone, which is the
strengthening worth naming: `Passed` being false only proves SOME required check
failed, so a level can ship with a required check that passes on a bare setup,
verifying nothing, and `Passed` would still be false because of its siblings.

Step 6 compares the sandbox user's processes against a snapshot taken before the
level ran. A level whose `setup.script` backgrounds something and whose teardown
forgets to kill it is otherwise invisible: the level passes and the next level
inherits a process it did not start.

The hash walker runs inside the sandbox, takes its root as an argument to a fixed
script rather than interpolated into it, and invokes `find` without `-L` so a
planted symlink cannot walk it out of the root. Its root is validated first with a
thirteen case refusal table, in the shape `destructive-safety` asks for even though
this path only reads: the risk is a bug here hashing the whole sandbox, which would
look like a hang and make the purity comparison meaningless.

`author test` refuses rather than skips without Docker, exiting through `ux.Fail`
with `docker-daemon-down`. The Go test skips, so something has to be honest, and a
`make golden` reporting success having tested nothing is worse than no gate. It
provisions its own container, never the production `shellforge-sandbox`, so an
author cannot tear down a level a learner has open in another terminal.

The Go golden test is gated on a Linux Docker daemon AND `SHELLFORGE_GOLDEN=1`.
The env var is not belt and braces: `ubuntu-latest` HAS a working daemon, so a
daemon-only gate would make `go test ./...` in the fast Test job start building the
Containerfile and turn a seconds-long job into a minutes-long one. The new CI step
is in the existing Sandbox image job, so no job was added and neither
`all-green`'s needs nor the ruleset changed.

**Three test updates worth recording, because each asserted something that had
become untrue.** `TestCmdRunRefusesAnUnknownLevel` used `nav-01` as its example of
a level that does not exist, and `nav-01` is real now, so it asserted the opposite
of what it said; it uses `nav-99`. `author test` came off `commands_test.go`'s stub
list, the same way `author validate` did when #53 landed. The demo's two briefing
tests moved to the demo adapter that now owns its presentation.

**Two bugs of my own, both caught by tests rather than by review.** `plural()`
documents that every noun it is given takes a plain `-s`, and it had been called
with "process" and "bonus", which would have rendered "2 processs" and "2 bonuss"
in front of a learner; both rephrased rather than the shared helper changed. And
the first field-count test carried arithmetic that was simply wrong.

Gates run locally, all green: `gofmt -s`, `go vet ./...`, `go test ./...`,
`go test -race` on `internal/verify`, `internal/game`, `cmd/shellforge` and
`internal/content`, `go test ./internal/archtest/...`,
`./scripts/check-punctuation.sh`, `./scripts/check-links.sh`,
`./scripts/check-cli-package.sh`, `./scripts/check-allowlist-regexp.sh`,
`python3 scripts/check-ci-gates.py`, `go mod tidy` leaving the `go` directive at
1.23.0, and `shellforge author validate packs/core-linux-basics`.

Not run locally: `govulncheck` and `gosec` (not installed, and the dependency
graph grew here so the Security job is the first real read on it), `pytest` over
`scripts/tests` (not installed), and every Docker-gated test in the branch, which
is the entire golden contract, the filesystem purity check and every end-to-end
claim about playing a level, because this environment has no Docker socket. That
last one is the largest untested surface in this branch and CI is the first thing
to exercise it.

---

## Day 1: the spike

> Goal: one hardcoded level, playable end to end, on Linux, with a real bash.

Exit criteria, from the build plan. Ticked against a manual run inside WSL2 on
2026-08-12, issue #11:

- [x] `shellforge run demo` opens a real bash prompt inside a container.
      Verified: `learner@79bb25ed7ab8:~/quest$`, a real instrumented bash.
- [x] `vim`, `less`, `htop` all render correctly; terminal resize works
      mid-session. `vi report.txt` and `htop` both entered and exited cleanly.
- [x] Ctrl-C interrupts a command without killing the game. Verified against a
      running `sleep`.
- [x] Every command is logged with `{cmd, exit_code, cwd, duration}`, and no OSC
      escape codes are visible to the user. The journal TSV inside the sandbox
      carries all four fields for every command, including the exit code 1 from
      a failed `sudo` and a cwd that follows `cd`. Nothing rendered a marker.
      **`CommandEvent.Raw` is still empty** on the host-side event stream,
      pending #51; the in-sandbox journal is where the command text lives today.
- [x] Typing `check` inside the shell reports pass or fail correctly. `FAIL`
      before the solution with the authored `on_fail`, `PASS: report.txt holds
      the total ERROR count (147)` after.
- [x] `rm -rf /` inside the sandbox does nothing to the host. Proven by
      `TestDestructiveRemovalInsideTheSandboxCannotReachTheHost`, not by the
      manual attempt: see the note below, because the manual attempt did not
      establish this.

**Go.** The hard part works: real bash, real markers, real state verification,
real isolation. Day 2 can proceed to the content engine.

**The manual `rm -rf` did not prove isolation, and the automated test is what
does.** `sudo rm -rf /*` at the prompt fails with "the no new privileges flag is
set" and the removal never runs at all, so it demonstrates that sudo is blocked
and says nothing about the filesystem. The real test runs the removal as the
learner, with no sudo, for real: it deletes the level and the learner's home,
then asserts a host canary file is byte-identical, the host working directory
still has every entry it started with, the level really was destroyed inside the
sandbox (otherwise the first two assertions prove nothing), and `Setup` recovers
the level to a passing state. Root-owned paths such as `/bin` survive because
the container drops `CAP_DAC_OVERRIDE`, which is also why the session stays
usable afterwards.

**Sudo and Act V are in conflict, unresolved.** The `Containerfile` grants the
learner passwordless sudo and its comment says Act V teaches it. The container
is then created with `--security-opt no-new-privileges`, which prevents sudo
from gaining privileges at all, so the grant has no effect.
`TestSudoIsRefusedByNoNewPrivileges` pins the current behaviour with a
`TODO(v0.2)`. Either Act V drops the flag for levels that declare they need
sudo, or the curriculum teaches sudo without running it. **Do not resolve it by
removing the flag to make a level pass.**

## Day 2: content engine and verification

- [x] Levels load from YAML with zero Go changes. Nine levels now: `nav-01` to
      `files-04` plus `pipe-05`. `shellforge run <id>` plays any of them and the
      Go code knows no level id, so adding a tenth is a YAML file and nothing
      else. Same caveat as the two below about what "plays" rests on.
- [x] `author validate` catches duplicate id, cycle, missing asset, missing
      `on_fail`, unknown check type. All five, plus setup.root containment,
      objective correspondence in both directions, and the new journal check
      rule. `shellforge author validate packs/core-linux-basics` exits 0 with
      17 warnings: sixteen for levels not yet written, and one for pipe-05's
      `prerequisites: [pipe-04]`, which names a level that has no file yet. The
      count stayed at 17 rather than dropping to 16 when pipe-05 landed, because
      it traded its own missing-file warning for that prerequisite one.
- [x] A failing check prints its authored `on_fail`, not a generic error.
      `internal/verify` carries the authored text through to
      `ObjectiveResult.Message`, and `renderCheckReply` prints it under the
      checklist. A timeout or an error deliberately does NOT carry the authored
      text, because nothing was established about whether the learner was right.
      **Demonstrated by unit test, not by playing a level**: see the caveat below.
- [x] Only the first failing required objective is shown.
      `LevelResult.PrimaryFailure` is the first non-passing objective that is
      neither optional nor `severity: warn`, and the renderer prints exactly that
      one body. A second failing required objective still gets its checklist line.
      Verified to fail without its fix, against the naive implementation that
      prints every failing message. Same caveat.

**The caveat, stated plainly because it is the honest limit of this day's work.**
Both criteria above, and the two ticked before them, are demonstrated by unit
tests over a fake sandbox. Not one of them has been demonstrated by playing a
level in a real container, because the environment this was built in has no
Docker socket. The golden contract that would demonstrate all four end to end is
written, is what `make golden` runs, and executes in CI's Sandbox image job; that
CI run is the first time any of this touches a real sandbox. Until it is green,
read every claim on this page as "the tests say so" rather than "somebody saw it
work".

Also not run locally, for the same reason or because the tool is absent:
`govulncheck`, `gosec`, and `pytest` over `scripts/tests`. The dependency graph
grew in this branch, so the Security job is the first real read on it.

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

### Day 2 follow-up, 2026-08-14: what CI found when the golden test first ran for real

The golden contract ran nine levels in a real container for the first time and
failed three. None of the three was a golden-harness bug in the way it first
looked, and one of them was not a level bug at all. Writing this down because the
correction matters more than the fixes: "fix the level" would have been the wrong
move on two of them.

**files-03 and files-04 were one engine bug.** Both create a directory in
setup.script, and in both cases the directory was never created. setup.script ran
as root, and the sandbox drops CAP_DAC_OVERRIDE deliberately, so root cannot
create an entry in the learner-owned 0755 level root. `mkdir -p filed` as root
gets EACCES.

Two things hid it, and either alone would have kept it hidden for the rest of the
project. setup.files land through PushFiles, which is `docker cp` and runs in the
daemon with full host privilege, so a level's files appear normally whatever the
container's capability set says. And every level's script ended with
`chown -R learner:learner .`, which root CAN do because CAP_CHOWN is kept, so
`bash -c` returned that command's zero exit and discarded the failed mkdir before
it. Setup reported success on a world it had not finished building.

Fixed by running setup.script and teardown.script as the learner, which is the
identity that owns the tree and the smaller privilege. The redundant chown came
out of all nine levels.

**That found a fourth problem nobody had reported.** nav-04's script is five
`touch -d` calls establishing the mtime ordering the level is entirely about, and
its checks only read flags.txt, so any of them failing would have taught nothing
and reported nothing. It gained `set -e`. Worth knowing why nav-04 passed while
the other two failed: `touch -d` as root works, because CAP_FOWNER covers utimes.
The same capability set allows the timestamps and forbids the mkdir. Coincidence,
not design.

**nav-01 was a separate bug and it was mine.** It asks the learner which
directory they started in and expects /home/learner, and both the interactive
Attach and the harness's solution runner used the level root. The level was right:
a login shell starts you in your home directory, nav-01 teaches exactly that, and
starting anywhere else makes the campaign's first lesson false. Both call sites
now use sandboxHome.

The instructive part is that because BOTH copies were wrong in the same direction
they agreed with each other, and the harness happily verified solutions from a
directory no learner is ever in. So the guard asserts both are specifically
sandboxHome rather than merely equal to each other. Two wrong copies of one value
agree, which is why "do these match" was never going to be enough.

**One more round: the harness itself was wrong about files-04.** With the engine
fixed, eight of nine passed and files-04 failed on `important-intact`, "important/
is untouched, byte for byte". That objective is already true at setup and the
learner earns it by keeping it true while deleting everything around it. Step 2's
per-objective assertion had assumed every objective describes work. So
content.Objective gained `preserves: true`, and the harness inverts the assertion
for one: it must PASS at step 2, because one already false means the level asks
the learner to protect something its own setup never created. Step 4 still
requires it to pass, which is where it earns its keep, and it is not `optional`:
deleting important/ still fails the level.

Six new Docker-free tests so none of this needs a container to stay fixed, and
all six run on the Windows leg: the WorkDir guard, the chown guard, the
needs-root guard, the errexit guard that found nav-04, and two holding
`preserves` honest (never together with `optional`, and only on a level tagged
`destructive`). All six verified to fail without their fix.

Two living contracts changed in the same commits as their code, per the rule:
docs/LEVEL-FORMAT.md's setup and teardown execution section, which said both
scripts run as root, and section 7's golden contract list. Both changes plus the
go floor move are on #98 for ratification.

The script-user change takes something away, and #100 records it rather than
letting it be rediscovered: a level can no longer set up state that genuinely
needs root, such as a root-owned file a permissions level wants the learner to
chmod. Act 5 is perm-01, perm-02, perm-03 and proc-01, so that is about three
levels away. The likely answer is that setup.files with an explicit owner already
covers it, since that path goes through docker cp and the daemon's own privilege
rather than the container's capability set, which is exactly why a level's files
were landing while its script was not. #100 asks for that to be checked against
the real briefings before anything is designed.

Separately, the Security job found three reachable vulnerabilities, all arriving
with glamour. x/text's fix declares `go 1.25.0` and Go will not build a module
whose dependency asks for a newer directive than it declares, so the fix and the
1.23.0 floor were mutually exclusive. The maintainer chose to keep the library, so
the floor is 1.25.0, stated in five places and moved in all five. A newer minimum
Go is not a security cost: the directive is a minimum, so it ships a standard
library with more fixes rather than fewer. The cost is compatibility, and building
from source now needs Go 1.25.

Still not run from a developer machine: every Docker-gated test, govulncheck,
gosec and pytest. The difference from yesterday is that CI has now run the first
two, and both found real things.

### Day 2 follow-up, 2026-08-16: two check parameters that could not do what they said

Issues #82 and #95, taken on one branch because they are the same defect wearing
two costumes: a check parameter whose documented behaviour and real behaviour
disagreed, and in both cases the documentation was the honest half.

**`file_content` with `match: contains` or `match: regex` and an empty `value`
is now refused when the level loads.** `strings.Contains(s, "")` is true for
every `s` and `regexp.Compile("")` matches every string at every position, so
either one produced an objective that passed whatever the learner did. That is
the `accepts-wrong-answer` class inverted: not a learner finding a hole, but a
level author leaving a placeholder blank and shipping a checklist line that
cannot fail. Nothing downstream could have caught it, because an unfalsifiable
check and a satisfied one look identical from outside. `exact` and
`trimmed_equals` keep accepting an empty value, where it means "the file is
empty" and is the natural way to write that; `sha256` and `line_count` already
failed closed and were left alone, with a regression test pinning that
`line_count` still reports its own message rather than one of the new ones.

**`file_mode`'s list parameter is now `modes`, and the documented `any_of`
spelling never worked.** `internal/content` reserves `any_of` for a composition
node, so it was decoded into `CheckSpec.AnyOf` and never reached `Params`: an
author following section 3 of docs/LEVEL-FORMAT.md got an error about declaring
both a type and a composition node, which is true and unhelpful. The parameter
had been dead since it was registered. No shipped level used it, so nothing was
broken for a learner.

The round trip is what was missing, and it is the reason this survived review
twice. `internal/content` owns the decoder that decides whether a key becomes a
field or a parameter, `internal/verify` owns the registry that decides whether a
parameter is accepted, they are both layer 3, and neither may import the other.
A test in either one runs against a fake and proves only its own half. The new
tests live in `cmd/shellforge`, where the two legitimately meet, next to
TestEmbeddedPackValidatesAgainstTheRealRegistry: one loads a level using `modes`
through the real decoder and builds it against the real registry, and two more
pin that both old spellings are still refused with an error naming the file.

Every new guard was mutation-checked rather than assumed: each was reverted in
turn and the tests that should have caught it did.

One thing found and deliberately not fixed here: setting both `mode` and `modes`
is accepted, and `modes` silently wins. The documentation now says so rather
than pretending otherwise. Silent precedence between two spellings of one
setting is the same shape as the `optional` disagreement #97 describes, so it
belongs in a ticket with that one, not smuggled into this branch.

### Day 3, 2026-08-17: the rootfs tarball, made to agree with itself

Issue #67, Day 3 ticket A. CI, no Go layer. Fixes the three-different-tarballs
problem the issue named, and bakes in the marker file the WSL destroy path
(issue #69) needs before it can refuse to unregister the wrong distribution.

- **Three producers, one contract now.** `rootfs.tar.gz` is `gzip -9` of
  `docker export` of the sandbox image, and `rootfs.tar.gz.sha256` is one
  `sha256sum`-format line. CI's `image` job, `make rootfs`, and
  `.\make.ps1 rootfs` all write exactly those two files now. Before this,
  CI built a tarball and discarded it at the end of the job, the Makefile
  already had the right filename and sidecar shape, and `make.ps1` wrote
  `rootfs.tar` uncompressed with a `Get-FileHash`-shaped sidecar: wrong
  filename, wrong bytes, wrong sidecar.
- **`gzip -9 -n` everywhere, not `gzip -9`.** GNU gzip stores a timestamp in
  its header by default, so the exact same input compresses to a different
  file, and therefore a different sha256, on every run. `-n` (`--no-name`)
  suppresses that, and without it the "digest recorded by the two scripts is
  identical" acceptance criterion cannot hold even on one machine, let alone
  two. Added to the CI step, the Makefile, and `make.ps1`.
- **`make.ps1` shells out to a real `gzip.exe`, not `System.IO.Compression.GZipStream`.**
  The .NET deflate implementation is not guaranteed to produce byte-identical
  output to GNU gzip at the same nominal level, which would defeat the point
  of a shared checksum. `Get-GzipCommand` looks for `gzip` on `PATH` first,
  then for `gzip.exe` under Git for Windows' own `usr\bin` next to `git.exe`,
  and the `rootfs` target now fails loudly with a one-line remediation if
  neither is found, rather than falling back to a different compressor
  silently. The export still goes to a file via docker's own `-o` flag first,
  then gzip runs on that file in place: piping `docker export`'s stdout
  through the PowerShell pipeline risks mangling binary data, particularly
  under Windows PowerShell 5.1.
- **CI now publishes what it builds instead of throwing it away.** The
  rootfs step gained content assertions (the exported tarball must contain
  `/etc/wsl.conf`, `/home/learner`, `/opt/shellforge/bin/check`,
  `/opt/shellforge/.sandbox-id`, and a man page, or the job fails) so a
  passing build actually proves the tarball is importable, not just that
  gzip exited zero. A missing `/etc/wsl.conf` would mean interop and
  automount default ON in a real `wsl --import`, which is a safety
  regression, not a build nicety. Both files then go up via
  `actions/upload-artifact`, reusing the exact SHA-pinned reference already
  in this file for the fuzz-crasher upload, as a build artifact on every
  run. On a tag push (`refs/tags/v*`, added to the workflow's own triggers),
  a new step in the same job attaches both files to the GitHub release using
  `gh release upload ... --clobber`, falling back to `gh release create` if
  no release exists yet for that tag. `gh` is already on GitHub-hosted
  runners, so no new third party Action entered the supply chain for this.
  The `image` job gained `permissions: contents: write` for this, scoped to
  that one job; the workflow level stays `contents: read`.
- **`/opt/shellforge/.sandbox-id` now exists in the image**: a fixed string,
  owned root, mode 0444, written before the directory's own `chown -R` so it
  is not a separate ownership pass. Mode 0444 means a learner cannot rewrite
  its content; the directory it lives in stays root-owned mode 0755 as it
  already was, so a learner cannot delete or recreate it either, and neither
  protection comes from the file's own bits alone. The contract is presence,
  not an exact string match: the `destructive-safety` skill asks for
  confirmation that a marker file exists, not that it holds a specific
  value, so the WSL destroy path (issue #69, which depends on this one) only
  needs to check the file is there before `wsl --unregister`, and refuse
  when it is absent, rather than trusting that whatever it is about to
  unregister is really one of ours. The `v1` in the string is future proofing
  in case the marker format itself ever needs to change in a way that check
  would need to tell apart, not a value anything reads today.
- `scripts/tests/test_ci_yml_rootfs_artifact.py` is new, modeled on
  `test_ci_yml_fuzz_upload_step.py`: it asserts the tag trigger, the job's
  `contents: write` permission, the upload step's name and both file paths
  and its pin (via `check-ci-gates.py`'s own `parse_uses_lines` and
  `check_uses_ref`), and the release-attach step's position, condition, and
  command. `python3 -m pytest scripts/tests -q` is green, including every
  pre-existing test in that directory.
- `.gitignore` already covered `images/out/`, `*.tar`, and `*.tar.gz` before
  this ticket. Verified, not changed: no tarball can land in the repository.
- **What is honestly not verified here.** This environment has no Docker
  daemon at all, so nothing that needs one, the image build, the export, the
  content assertions, either script's actual byte output, could be run from
  a developer machine this time either. CI's `image` job is the only witness
  for all of that, same as every prior Docker-gated claim on this page. The
  cross-platform digest comparison the issue's own test plan calls for, a
  Linux runner's `gzip -9 -n` against Git for Windows' `gzip -9 -n` on the
  same image, needs one machine of each kind and was not run: it is a
  reasonably founded expectation (both are the same GNU gzip implementation)
  rather than a proven one, and stays that way until someone with both
  platforms records the two digests by hand, per the issue's own test plan.

### Day 3 follow-up, 2026-08-17: optional has one home, the objective

Issue #97. `internal/content`, `internal/game`, six level files, and the docs
that describe them. No new Go dependency, no `internal/archtest` edge, and
`internal/verify` untouched throughout.

- **The decision.** `optional` used to live on both `content.Objective` and
  `content.CheckSpec`, and the two could disagree; `internal/game.verifySpecs`
  resolved a disagreement by letting either side's `true` win, marked
  `TODO(v0.2)` at the time (see the Day 2, 2026-08-14 entry above). This
  ticket deletes `CheckSpec.Optional` outright rather than picking a winner at
  runtime: every one of the 15 `optional` hits in the shipped pack resolved to
  8 objective-side declarations and 7 check-side ones, and every check-side
  hit already had an identical objective-side twin in place, so the objective
  was a pure deletion away from being the only declaration anywhere.
  `optional` is also meaningless without an objective, since it describes a
  checklist line the learner reads, while `severity` is not: the validator
  already exempted a `severity: warn` check from needing an objective and
  refused that same exemption to an `optional` one. One home means the
  objective's checklist and the engine's pass or fail verdict can no longer
  disagree about the same check.
- **`optionalDeclared`, not a decode error.** The YAML key `optional` stays
  reserved on a check's mapping, so it never falls into `Params` and never
  reaches the check registry as an unrecognized parameter. `decodeField`
  records only that the key was present, in an unexported
  `CheckSpec.optionalDeclared`, and does not decode its value: `optional:
  "yes"` on a check now gets the validator's "belongs on the objective"
  message, not a bare type complaint. The validator rejects the key at every
  composition depth, not only on a branch as the old rule did, because a
  check can no longer declare it correctly anywhere now that the field exists
  only on `Objective`.
- **`gating` recomputed from the check's own objective, and this is a
  security fix, not a refactor.** The rule that a `command_matched` or
  `command_not_matched` check may never decide pass or fail depends on
  knowing whether a check is gating. Before this ticket, `validateChecks`
  read that off `CheckSpec.Optional`, the field this ticket deletes. After
  it, `gating` is read from a `map[string]bool` built from `lvl.Objectives`,
  keyed by objective id: an objective declared `optional: true` makes every
  check with a matching id non-gating, and a check whose id matches no
  objective defaults to gating, the safe direction, since the missing
  correspondence is refused on its own by
  `validateObjectiveCorrespondence`. An objective with an empty id is
  skipped when the map is built, so an unnamed optional objective can never
  make an unnamed check look non-gating by both reading as key `""`.
  `TestValidateGatingReadsEachObjectivesOwnOptionalFlag` pins the keyed-by-id
  part specifically: an implementation that collapsed every objective's flag
  into one shared boolean, or that ORed all of them together, would still
  pass the simpler tests and fail that one.
- **The latent defect this fixes.** Before this ticket, the validator's
  `required` counter for "a level you cannot fail is not a level" also read
  `CheckSpec.Optional`, while `internal/game.verifySpecs` computed
  `verify.Spec.Optional` by ORing in the objective's own flag. A level whose
  only check sat under an objective-only `optional: true`, with the check
  itself declaring nothing, counted as `required == 1` at validation time
  while the engine already treated the same check as optional at runtime:
  the validator certified as failable a level that could not be failed. No
  shipped level hit this, because every optional objective in the pack was
  already backed by a journal-type check, which `hasStateLeaf` already
  excludes from the required count for an unrelated reason.
  `TestValidateEveryObjectiveOptionalIsALevelYouCannotFail` is red against the
  pre-ticket code for exactly this reason and is the test that proves the
  fix.
- **Level pack.** Seven check-side `optional: true` lines deleted across six
  files (`01-nav-01.yaml`, `02-nav-02.yaml`, `03-nav-03.yaml`,
  `04-nav-04.yaml` twice, `05-files-01.yaml`, `14-pipe-05.yaml`); every one
  had an identical objective-side declaration already in place, so no
  level's behavior changes. `06-files-02.yaml`'s `no-flooding` check was not
  touched at all: it already gated through `severity: warn` alone and never
  carried `optional`. `TestEmbeddedPackValidatesAgainstTheRealRegistry` is
  the acceptance test for these seven deletions: it goes red the moment the
  validator rule lands and green only once every one of them is gone.
- **Docs.** `docs/LEVEL-FORMAT.md` section 2's invariant table, the check
  catalogue's common-fields line, the journal section's requirement sentence
  and its `command_matched` example, the composition section's implicit
  `all_of` description, and the `primary_failure` sentence in section 5 all
  described the two-homes schema and now describe the one-home version.
  Section 6's worked example dropped its own check-side `optional: true`,
  which `TestWorkedExampleValidates` now enforces by going red until it is
  gone. `docs/CURRICULUM.md`'s journal-check paragraph got the same
  rewording. `docs/design/ARCHITECTURE.md` was left alone on purpose: it
  describes intent, and the intent did not change, only which field
  expresses it.
- **Mutation check.** Reverting `gating := !bonus[c.ID] && c.Severity !=
  SeverityWarn` to `!c.optionalDeclared && c.Severity != SeverityWarn`, the
  old check-side reading, turned `TestValidateJournalChecksCannotGatePassing`
  (both its `optional is accepted` subtests),
  `TestValidateGatingReadsEachObjectivesOwnOptionalFlag`, and
  `TestEmbeddedPackValidatesAgainstTheRealRegistry` (on all seven levels
  whose check-side line was deleted) red, which is what proves the new tests
  are load-bearing rather than incidentally passing. Restored immediately
  after confirming it, and the full suite was green again afterward.
- **The golden contract ran locally, which is a first for this page.** The
  docker daemon came up part way through this session, so unlike every
  recent entry above, the nine shipped levels were played for real against a
  live Linux container from a developer machine rather than left to CI. Both
  entry points ran and both passed all nine: the Go test
  `TestEveryLevelGoldenPath`, and the CLI path `shellforge author test --all`,
  which also exercises the purity and teardown phases per level. So the claim
  that no level's behaviour changes with the seven deleted lines gone is
  proved end to end here, not merely reasoned from the fact that no level's
  `verify.Spec.Optional` or `gating` value changes.
- **What genuinely could not run locally.** `govulncheck`, `python3`, and
  `pytest` are absent from this environment, so `scripts/check-ci-gates.py`,
  `scripts/tests`, and `govulncheck ./...` stayed CI's to run alone. Nothing
  else was skipped. `gofmt -s -w .`, `go vet ./...`, `go build ./...`,
  `go test ./...`, `go test -race ./...`, `go test ./internal/archtest/...`,
  `bash scripts/check-punctuation.sh`, `bash scripts/check-links.sh`,
  `bash scripts/check-cli-package.sh`, `bash scripts/check-allowlist-regexp.sh`,
  and `gosec -quiet -exclude-dir=docs ./...` all ran locally and are green.
- **A trap for the next person who runs the sandbox tests locally.** A stale
  `shellforge-controltest` image built before the Containerfile gained its
  `/home/learner/.shellforge` step makes `TestControlChannelAnswersTheShim`
  fail with `mkfifo: cannot create fifo ... No such file or directory`, which
  reads as a code defect in the control channel and is not one. Confirmed
  identical on `main`, so it is not from this branch. Untagging the stale
  image and letting the test rebuild it fixes it. CI never sees this because
  it always builds the image fresh.

One thing found and deliberately not fixed under this same ticket: this
session found an eighth place carrying the old wording, not counted among
the fifteen `optional` hits because it names no such key: `cmd/shellforge`'s
`TestValidateFailureModes`'s `journal check gating a pass` case asserted the
literal string `optional: true or severity: warn` through the `author
validate` CLI path, one layer above where the ticket's own plan looked.
Updated to the same new wording as `TestValidateJournalChecksCannotGatePassing`,
since it exercises the identical validator message through a different
caller rather than a different rule, and the wording change was already this
ticket's own decision to make.

### Day 3 follow-up, 2026-08-17: the progress database refuses a file it did not create, and a missing one stops reading as corrupt

Issues #92 and #90, folded into one branch because the two decision paths
through `Open` turned out to be disjoint: a foreign database never reaches
`classifyOpenError` at all, so widening its arm for #92 cannot swallow #90's
case, and a non-SQLite file cannot reach the foreign check either, since it
fails its very first pragma before `Migrate` is ever called. All of
`internal/store`, test files only apart from `store.go` and `migrate.go`.

- **#92, one line plus a doc comment sentence.** `classifyOpenError`'s
  permission probe matched only `fs.ErrPermission`, so a progress database
  path that does not exist yet, or whose parent directory does not exist
  yet, fell through to the corrupt anchor: its remediation told the learner
  to rename a file that was never written and warned them their finished
  levels would be forgotten. The arm now also matches `fs.ErrNotExist`.
  Covered by `TestClassifyOpenErrorOnAMissingFileIsUnwritableNotCorrupt`
  (both cases run and fail correctly on this Windows host) and
  `TestOpenOnAnExistingUnwritableParentReportsUnwritable`, the real bug
  reproduction, which needs POSIX mode bits and **skips on Windows**; CI's
  Linux job is its only witness.
- **#90: `ErrForeignDatabase`, `userObjectCount`, `looksLikeOurs`,
  `refuseIfForeign`.** `Open` now refuses, rather than silently migrating, a
  SQLite database that is not provably a Shellforge progress file and already
  holds objects Shellforge did not create. It fails closed: a query that
  cannot complete is refused with the same anchor as a confirmed foreign
  database, never treated as evidence the file is ours. New anchor
  `progress-db-not-ours`, this package's sixth, with its heading in
  `docs/05-troubleshooting.md` naming `DataDir`'s per-OS path rather than
  staying abstract. The remediation never suggests deleting, renaming,
  overwriting, or moving anything, which is the point when the file may
  belong to another program.
- **The identity test is provenance first, and it took two rounds to get
  right.** The first version asked "does this record no version and hold a
  table?", ran inside `Migrate`'s `current == 0` branch, and filtered
  `sqlite_master` on `type = 'table'`. The independent review found two ways
  a foreign file routed around it, both reproduced by measurement rather than
  argued:
  - **A foreign database carrying its own `schema_version` table reached the
    version arithmetic and was told to rename itself.** `schema_version` is a
    common name, older Flyway and much hand rolled migration code included.
    Because the gate only ran when the recorded version was zero, a file with
    someone else's `schema_version` fell through to `anchorMigrationFailed`
    or `anchorTooNew`, and both of those remediations tell the learner to
    rename the file. Telling a learner to rename another program's database
    is the exact outcome this gate exists to make impossible.
  - **A database whose only objects were views was adopted**, because a view
    is not a `table`. Shellforge wrote its own tables into it and narrowed
    its mode, which is both harms #90 names, on a file it did not create.
    `internal/store/doc.go` and the troubleshooting entry both promise that
    never happens, so the promise was false rather than merely incomplete.

  Both are fixed by changing what the question is. Provenance is now decided
  first, before any version arithmetic, by `looksLikeOurs`: a file is ours
  when `sqlite_master` holds both objects `001_init.sql` creates,
  `schema_version` and `events`. `events` is the load bearing half, since
  `schema_version` alone is not evidence of anything. A file that is provably
  ours is left alone, extra tables the learner added included. A file that is
  not provably ours is refused if it holds any non-`sqlite_` object at all,
  views and indexes included, and adopted only when it holds nothing.
  `TestAFreshlyMigratedDatabaseIsProvablyOurs` is what stops the two named
  tables going stale: if a later migration renames either, that test fails
  rather than the gate silently starting to refuse real progress files.
- **The chmod moved from before `Migrate` to after it, not made
  conditional.** A file `Migrate` is about to refuse now has nothing at all
  done to it. A conditional chmod would need either a second
  `sqlite_master` read in `Open` to decide, which duplicates the identity
  logic and can disagree with the first read, or a value returned from
  `Migrate` saying "this one was ours," which is a signature change the
  ticket rules out. The cost, stated plainly: a fresh database now sits at
  the process umask default for the duration of the migrations rather than
  one pragma, inside the 0700 directory `EnsureDir` created throughout, and
  `001_init.sql` creates only empty tables, so no learner data exists in the
  file during the widened window. Two secondary effects are improvements: a
  database refused for `ErrSchemaTooNew`, and one refused through
  `anchorMigrationFailed`, are no longer chmodded before the refusal either.
  A third is a small regression and is recorded rather than buried: a fresh
  database whose migration fails is now left at the umask default
  permanently, because `Open` returns before reaching the chmod. It sits
  inside the 0700 directory, a failed migration commits nothing, and the next
  successful `Open` tightens it, so the exposure is a file with no learner
  data in a directory only the learner can enter.
- **The byte-identity criterion is not literally satisfiable, and the test
  says so precisely instead of claiming otherwise.** Measured directly,
  using this package's own already-shipped `execPragmaWithRetry` against a
  one-table delete-mode fixture, before any #90 code existed: length
  identical at 8192 bytes before and after; the only differing bytes at
  offsets 18, 19, 27, and 95; nothing at or above offset 100 differed. That
  matches SQLite's own 100 byte file header ending exactly at offset 100:
  `PRAGMA journal_mode=WAL` runs in `Open` before `Migrate` ever looks at
  the file, and converting a delete-mode database to WAL rewrites the
  file-format version bytes (18-19), the change counter (24-27), and the
  version-valid-for number (92-95). `TestOpenLeavesAForeignDatabaseUntouched`
  asserts exactly that: length unchanged, everything from offset 100 onward
  byte-identical (hashed with sha256 for a readable failure), and the
  foreign table's own schema text and rows unchanged. Making the header
  identical too would need the WAL pragma to run after the identity gate,
  which changes the lock contention `execPragmaWithRetry` is built around;
  filed as a follow-up rather than done here (see below).
- **One existing fixture changed, forced by the new contract, no assertion
  did.** `TestOpenOnAContendedConversionReportsInUseAndRetries` built its
  blocking-transaction fixture with `CREATE TABLE t (x INTEGER)` on a plain
  `sql.Open`, which is exactly a database #90 now refuses: the test's own
  second half asserted a second `Open` succeeds once the blocking
  transaction releases. The fixture is now a genuine current-version
  Shellforge database, built by looping `applyMigration` over `migrations()`
  directly (this package's own `Open` would convert it to WAL immediately,
  before the test ever gets to hold its blocking transaction against a
  delete-mode file), and the blocking write is an `INSERT INTO events`
  inside the same uncommitted transaction. Both original assertions,
  `anchorInUse` first and a successful `Open` after release, are unchanged.
- **The anchor guard is now AST-derived, with a floor, as a logically
  separate change within the same branch.** `TestDocAnchorsHaveTroubleshootingHeadings`
  used to check a hardcoded slice of five anchor constants against
  `docs/05-troubleshooting.md`; adding a sixth by hand risked the exact
  failure this package already avoids elsewhere: a witness that silently
  shrinks. `declaredDocAnchors` now walks this package's own production
  `.go` files for every `const` identifier prefixed `anchor`, unquotes its
  string value, and the test checks the doc heading against that list. The
  six named constants stay as an explicit floor, checked with
  `slices.Contains`, so a walk that finds nothing still fails loudly instead
  of passing over zero anchors. Verified by mutation: renaming `anchorInUse`
  to `docAnchorInUse` everywhere it is used made the floor fire with
  "the walk has gone blind," then reverted. `strconv` and `slices` added to
  `guards_test.go`, both standard library.
- **Mutation battery, two findings beyond confirming the rest load bearing.**
  `count > 0 -> count > 1` breaks not only `one_unrelated_table` as
  predicted but also `a_table_and_an_index`: an index never counts under
  `type = 'table'`, so that fixture has exactly one real table too, the
  same shape as the single-table case, for the same reason. Hoisting the
  `current == 0` guard so the foreign check runs on every `Migrate` breaks
  `TestOpenOnAShellforgeDatabaseAtARecordedVersionStillOpens`,
  `TestMigrateTwiceIsANoOp`, and `TestOpenOnACurrentDatabaseIsANoOp` as
  predicted, plus two more it did not name
  (`TestConcurrentStoresOnOneFileDoNotDeadlock`,
  `TestOpenOnAContendedConversionReportsInUseAndRetries`), because both
  reopen an already-migrated database and the hoisted check would wrongly
  refuse it; `TestSchemaVersionOnAnEmptySchemaVersionTableIsAnError` was
  predicted to break here too but does not, because its fixture makes
  `schemaVersion` itself return an error, which `Migrate` returns on
  unconditionally before either the gated or the hoisted foreign check ever
  runs, so that test is not actually a witness for this scoping.
- **Skips on this Windows host, CI's Linux job the only witness**:
  `TestOpenOnAnExistingUnwritableParentReportsUnwritable` and
  `TestOpenLeavesAForeignDatabaseModeUnchanged`, both needing POSIX mode
  bits, alongside the pre-existing `TestOpenCreatesMissingParentDirectories`,
  `TestOpenRejectsAnUnwritableParent`, `TestOpenClassifiesAnUnreadableFileAsUnwritable`,
  and `TestOpenOnACorruptFileLeaksNoDescriptor` (Linux `/proc/self/fd` only).
  Mutation M7 (chmod back above `Migrate`) is consequently local-inert here:
  its only witness is the mode test above, and nothing else broke either.
- **Follow-up filed, not done here**: run the WAL pragma after the identity
  gate rather than before it, so a refused foreign database is byte-identical
  including its header, not merely unchanged from offset 100 onward. Needs
  its own look at the lock contention `execPragmaWithRetry` and
  `TestOpenOnAContendedConversionReportsInUseAndRetries` are built around,
  and at whether the `-wal`/`-shm` siblings this fix currently tolerates as
  a side effect can be avoided for a database being refused.
- Gates run locally, all green: `gofmt -s -w .`, `go vet ./...`,
  `go build ./...`, `go test ./...`, `go test -race ./...` (both the
  package pair and the full module), `go test ./internal/archtest/...`,
  `bash scripts/check-punctuation.sh`, `bash scripts/check-links.sh`,
  `bash scripts/check-cli-package.sh`, and `gosec -exclude-dir=docs ./...`
  (70 files, 0 issues). Not run locally: `govulncheck ./...`, `python3
  scripts/check-ci-gates.py`, and `python3 -m pytest scripts/tests -q`
  (none installed in this environment, no dependency or workflow change on
  this branch regardless). CI is authoritative for those three.

### Day 3, 2026-08-17: Windows console VT mode and the resize watcher

Issue #68, Day 3 ticket D. Replaces the Day 1 no-op stub and adds the console
mode handling the doctor ticket and the CLI ticket both consume next.

- **`internal/platform`** gained `EnableVirtualTerminal`, `SupportsVirtualTerminal`,
  and `IsWindowsTerminal`, split across `console.go` (portable, no build tag:
  `IsWindowsTerminal` is a `WT_SESSION` check plus a `runtime.GOOS` branch,
  nothing Windows-specific to call), `console_windows.go`
  (`golang.org/x/sys/windows`'s `GetConsoleMode`/`SetConsoleMode`, real
  `ENABLE_VIRTUAL_TERMINAL_PROCESSING`/`ENABLE_VIRTUAL_TERMINAL_INPUT` handling),
  and `console_other.go` (`!windows`, both functions are no-ops that always
  succeed). `EnableVirtualTerminal`'s restore is idempotent via `sync.Once`,
  matching the discipline `Mux.restoreOnce` already uses for the host terminal.
  A failure enabling VT input rolls stdout's mode back before returning, so a
  caller that gives up after the error is not left with the two handles in
  different states.
- **`golang.org/x/sys` moves from indirect to direct** in `go.mod`, same
  v0.34.0 pin, no version change, and gained a row in the go-style skill's
  Dependencies table: `TestGoModDirectDependenciesAreApproved` in
  `internal/archtest` enforces that every direct dependency is listed there,
  and it was right to fail until the row existed.
- **`internal/pty/raw_windows.go`** replaces the stub. Windows has no
  SIGWINCH, so the watcher polls `m.getSize`, the same injectable field
  `raw_unix.go`'s SIGWINCH handler and `Mux.Run`'s own initial resize both
  use, every 250ms by default, and calls `m.resize` only when the reported
  size actually changed. The alternative, reading `WINDOW_BUFFER_SIZE_EVENT`
  off the console input handle, was rejected for the reason the ticket gave:
  `Mux.Run` already forwards the host's stdin to the sandbox verbatim, and
  draining console input events in a second place here would swallow the
  learner's keystrokes. `Mux` gained one new field, `resizePollInterval`,
  zero by default (meaning "use `raw_windows.go`'s own 250ms constant"), so
  a test can shrink it rather than waiting on the real interval; unix's
  watcher never reads it.
- **Tests.** `internal/pty/raw_windows_test.go` (`go:build windows`) drives
  `startResizeWatcher` directly through the injectable `getSize` field and
  the existing `fakePTY`/`newTestMux` harness from `mux_test.go`: a changed
  size resizes with the clamped rows-then-cols order, an unchanged size
  never calls resize, and calling stop provably ends the goroutine rather
  than merely coinciding with a quiet tick. `internal/platform/console_test.go`
  runs on every platform, branching on `runtime.GOOS`: the portable
  `IsWindowsTerminal` answers are pinned on both sides of that branch, the
  Windows-only tests cover the real `GetConsoleMode`/`SetConsoleMode` round
  trip when a real console is attached and fall back to asserting a clean
  error, never a panic, with a nil restore, when it is not, since a CI
  runner's stdout is not reliably a console either way. A third Windows-only
  test forces that failure path deterministically by swapping `os.Stdout`
  for a pipe. `go test -race ./internal/pty/... ./internal/platform/...` is
  green on Linux, and green again natively on a real Windows 11 Pro host
  (build 26200) when this branch was rebased onto main for merge: all three
  `startResizeWatcher` tests and both Windows console tests pass there under
  `-race`, alongside `go test -race ./...` for the whole module.
- **The real console round trip now has a test that actually reaches it.**
  `TestEnableVirtualTerminal_Windows`'s console-attached branch cannot run
  under `go test` anywhere, on CI or on a developer machine, because the test
  binary's stdout is always a pipe. GetConsoleMode on it therefore always
  fails, that test always takes its no-console fallback, and the round trip
  it documents had never executed. `console_realconsole_windows_test.go` is
  new and closes that hole: it opens the console's own `CONOUT$` and `CONIN$`
  devices, which exist regardless of where stdout is redirected, points
  `os.Stdout` and `os.Stdin` at them, and asserts the modes exactly. Not
  merely that the two virtual terminal bits are set, but that the resulting
  modes equal the prior modes *unioned with exactly those bits* and nothing
  else, that restore returns both to the precise values read beforehand, and
  that three restore calls leave them there rather than writing something
  different the second time.
- **That test never calls `AllocConsole`, deliberately.** Manufacturing a
  console requires `FreeConsole` first, which detaches the console from the
  whole test process rather than from one test, and a suite CI runs must not
  do that. So the test skips when no console is attached, which is every CI
  runner, and the assertion is only ever made on a developer's Windows
  machine. It is not theoretical there: it ran and passed on the Windows 11
  host this branch was prepared on, where `CONOUT$` opened with no
  `AllocConsole` needed at all. The modes observed were stdin `0x1f7` to
  `0x3f7`, exactly the `ENABLE_VIRTUAL_TERMINAL_INPUT` bit, and back to
  `0x1f7`; stdout read `0x7` throughout, which already includes
  `ENABLE_VIRTUAL_TERMINAL_PROCESSING`, because a current Windows 11 console
  enables it by default and setting it there is a no-op rather than a
  missing change. A `defer` restores both modes even if an assertion fails
  partway through, so a broken restore under test cannot leave the
  developer's own arrow keys emitting escape sequences.
- **What is honestly not verified here.** The one remaining manual
  acceptance criterion, resizing the host terminal during a full screen
  program inside the sandbox and confirming the host terminal's arrow keys
  still behave after exit, is still unverified. `cmd_run.go` does already
  build the multiplexer, so the watcher half of that criterion is reachable
  today via `shellforge run`; what blocks the check is that it needs a human
  dragging a real terminal's edge, and the session this was merged from is
  non-interactive with a piped stdout. The arrow keys half is not reachable
  at all yet: nothing calls `EnableVirtualTerminal`, which this ticket
  deliberately leaves to the CLI ticket, so no console mode is being changed
  for a restore to get wrong. Both stay open for a hand check on an
  interactive Windows terminal.

### Day 3, 2026-08-19: the WSL runtime, argv by argv

Issue #69. `internal/runtime/wsl` now implements `runtime.Runtime` and
`runtime.Session` by shelling out to `wsl.exe`, the way `internal/runtime/docker`
already implements them by shelling out to `docker`. This entry is deliberately
plain about what a Linux run with no Windows and no `wsl.exe` on PATH can and
cannot prove.

- **New package, ten files.** `doc.go` names the layer and the import list.
  `wsl.go` carries `New`, `wslRuntime`, `Provision`, `Destroy`, `Status`,
  `StartSession`, `Capabilities`, the two closed distribution-name constants, the
  marker check, and `classifyFailure`. `session.go` carries `wslSession`, `Exec`,
  `Attach`, `PushFiles`, `PullFile`, the sandbox-path validator, `stripCR`, and the
  tar builder. `runner.go` carries the `runner` and `fetcher` injection points,
  `execRunner`, `httpFetcher`, and `verifyDigest`. `list.go` carries the hand
  written UTF-16LE decoder and `parseList`/`parseQuietList`. `paths.go` carries
  `installDir`, `validateInstallDir`, `validateInstallDirUnder`, and
  `resolveRootfs`. `resize_windows.go` and `resize_other.go` are three lines each,
  carrying only `wslPTY.Resize`; every other symbol in the package, including
  every refusal test, compiles and runs on Linux.
- **The destroy target is a closed set of two compile-time constants.**
  `sandboxDistro = "shellforge-sandbox"` and
  `contractDistro = "shellforge-contracttest"`, the latter pinned equal to
  `runtimetest.SandboxName` by `TestContractDistroMatchesRuntimetestSandboxName` so
  a rename of the suite constant is a test failure rather than a contract run that
  silently cannot destroy what it created. `New` refuses any other name before it
  ever resolves `wsl.exe`; `Destroy` re-checks against the same set immediately
  before acting and is asserted to make zero `wsl.exe` invocations when refused.
  `internal/runtime/runtimetest/contract.go` was not touched.
- **UTF-16LE has its own decoder and eleven tests**, because `wsl.exe` writes
  every list command in it and a naive UTF-8 read produces NUL-interleaved text
  that almost parses. `decodeUTF16LE` strips the little-endian BOM, refuses a
  big-endian one outright, refuses an odd-length body, and decodes through
  `encoding/binary` plus `unicode/utf16`, so a surrogate pair round trips. A
  parsed distribution name is only ever compared, counted, and displayed: every
  argv element naming a distribution is one of the two package constants, never a
  value read back from `wsl -l -v` or `wsl -l -q`.
- **The install directory has seven refusal tests plus the derivation test.**
  `validateInstallDirUnder(dataDir, dir)` is the pure form the table drives
  against a `t.TempDir()`, refusing in order: empty, a `..` segment checked before
  cleaning, not absolute, the data directory root itself, not strictly under that
  root, then a symlink whose resolved target escapes it. `installDirFor` gives the
  production and contract-test distributions distinct directories under
  `platform.DataDir()/wsl/<distro>`, so a contract run can never reach the
  production `.vhdx`.
- **Provision, in order: resolve and digest-verify the rootfs only when a fresh
  import is needed, import, write `/etc/wsl.conf`, read it back to confirm,
  terminate, start, then verify by observation.** The digest is checked before
  `wsl --import` touches anything, never after; an empty `SHA256` against an https
  `Reference` is a refusal, not a skipped check. A digest mismatch on a file this
  package downloaded deletes the download and refuses; a mismatch on a
  caller-supplied local path refuses and leaves the file alone, because deleting a
  developer's own `make rootfs` output is itself a destructive act on a file
  Shellforge did not create. An existing, marked, WSL2 distribution is reused with
  no `--import`: `/etc/wsl.conf` is rewritten and the distribution terminated only
  when the readback differs from what Shellforge expects, and the three hardening
  probes (`/mnt/c` absent, `$PATH` free of `/mnt/`, `id -un` is `learner`) run
  either way. This is a deliberate deviation from the Phase 1 plan's literal
  step order, which read as resolving and digest-checking the rootfs
  unconditionally before the collision check: doing that unconditionally would
  make every idempotent `Provision` call on an already-healthy sandbox depend on a
  rootfs tarball being present on disk, which defeats the point of idempotency and
  is not what `TestProvisionIsIdempotentAgainstAnAlreadyMarkedDistribution` (which
  supplies no rootfs at all) is asserting. The safety property the plan actually
  cares about, that an unverified artifact never reaches `wsl --import`, holds
  either way and is what the tests assert.
- **`TestWslConfConstantMatchesImagesWslConf`** reads `images/wsl.conf` from the
  repository and compares it byte for byte, after CRLF normalisation, against the
  `wslConf` package constant, so the two cannot drift silently. `go:embed` cannot
  reach outside a package's own directory, which is why this is a constant and a
  test rather than an embed.
- **`rootfs-not-found`** is a new heading in `docs/05-troubleshooting.md`, for the
  case where `ImageSpec.Reference` is empty and neither `images/out/rootfs.tar.gz`
  nor a cached download exists. Placed after `rootfs-checksum-mismatch` and before
  `command-not-found`, in the same house shape as its neighbors.
- **Review pass on #134 fixed three defects, two of them blocking.** `Attach`
  now recognizes `pty.ErrUnsupported` (the same unconditional Windows failure
  `checkInteractiveShellSupported` in `cmd/shellforge` already refuses in front
  of) and reports it through `ux.Fail` with the `windows-needs-wsl` anchor
  instead of leaking `wsl.exe --exec: unsupported` as a bare Go error; this is
  defense in depth; the CLI guard is what a learner actually sees today.
  `Destroy` no longer reports success while orphaning the `.vhdx`: the
  not-present branch now resolves the install directory and runs it through
  `removeInstallDir` before returning nil, so a `Destroy` retried after a
  directory removal that failed the first time (an open handle on `ext4.vhdx`,
  say) finishes the job the second time instead of quietly forgetting the
  leftover 2 GB file. `Status` no longer treats a non-zero `wsl -l -v` exit as
  an error when the output does not carry a recognized failure signature: a
  host with WSL present and zero distributions registered now answers
  `Provisioned: false` rather than surfacing `wsl -l -v exited <code>` as an
  opaque failure, which is what a GitHub `windows-latest` runner does and what
  broke `Test (windows-latest)` on this PR's first CI run. `findRow`, which
  `Provision` depends on for the exact same reason on a learner's first-ever
  run, shares the fix through one `listRows` helper rather than repeating the
  bug in a second place.
- **`TestWslContract`'s factory now skips honestly on a host that cannot
  actually run the suite, not just one where `wsl.exe` fails to answer.** The
  `--version` probe now carries a timeout, so a wedged WSL service is a skip
  rather than a hang. Beyond that, `wsl.exe --version` succeeding proves only
  that the client binary runs, the same gap the docker sibling's factory closes
  by asking its daemon which OS its containers run rather than merely whether
  it answers: GitHub's hosted `windows-latest` runners answer `--version` with
  no distributions and no usable WSL2 underneath. The suite needs a rootfs to
  import, so the factory now also skips when `defaultRootfs()` cannot resolve
  one, which is always true in a plain `go test` job that never ran `make
  rootfs`, and is the same honest signal a developer's own Windows 11 machine
  gives once a rootfs is actually built.
- **What is honestly not verified here, because this run has no Windows, no
  `wsl.exe`, and no WSL2 on either CI leg.** `TestWslContract` (thirteen
  assertions, the same suite `internal/runtime/docker` runs) skips cleanly on this
  host and skips cleanly by design on both `ubuntu-latest` and `windows-latest`
  CI runners, the latter because GitHub's hosted Windows runners have no WSL2
  installed. Nothing here has proven: a real `wsl --import` actually completing
  against a real rootfs tarball; the three hardening probes observing a genuinely
  hardened distribution rather than a scripted fake answer; `wsl --unregister`
  actually leaving a developer's own `Debian` or `Ubuntu` untouched; the install
  directory, `.vhdx` included, actually gone from disk after `Destroy`, confirmed
  by eye in Explorer; or ConPTY resize, which `resize_windows.go` stubs as a
  documented no-op pending a real tracking issue (the TODO used to point at #68,
  which landed on main in 3a6ff31 without touching this). That is a gap in
  manual verification, not the same thing as the interactive shell itself:
  `vim`, `less`, and `htop` inside a real distribution are not merely
  unverified, they are provably unreachable today on any Windows host,
  because `creack/pty`'s Windows `Start` returns `ErrUnsupported`
  unconditionally and both `Attach` and the CLI guard in front of it now say
  so through `ux.Fail` rather than a bare error. All of the remaining,
  genuinely hardware-gated items need a human on a real Windows 11 machine
  with WSL2 installed. The Day 3 go/no-go gate in
  `docs/design/SEVEN-DAY-PLAN.md` names exactly this risk, and this entry is
  the honest record that the gate is not yet cleared by this run alone.
- **Gates run on this host:** `gofmt -s -w .`, `go vet ./...`, `go build ./...`,
  `go test ./...`, `go test -race ./...`,
  `go test ./internal/archtest/...`, `./scripts/check-punctuation.sh`,
  `./scripts/check-allowlist-regexp.sh`, `./scripts/check-links.sh`,
  `python3 scripts/check-ci-gates.py`, all green. `govulncheck`, `gosec`, and
  `pytest` over `scripts/tests` are not installed on this host and are left to CI,
  as they were for the Day 3 console mode entry above.

### Day 3, 2026-08-19: doctor, twelve probes, the table, and --json

Issue #70, Day 3 ticket C. `internal/doctor` moves from a `doc.go`-only stub to a
full package, and `shellforge doctor` moves from a stub to the first fully
diagnostic verb in the CLI.

- **The package.** `doctor.go` carries `Level` (with a lowercase-word
  `MarshalJSON`, never a number, so a shell script can compare against `"fail"`),
  `Probe`, `Result`, `Report`, `Report.Failed`, the `SandboxProber` interface,
  `Run`/`run`, `Fix`/`fix`, `fixAction`/`fixActions`, `baseProbe`, and the twelve
  anchor constants. `runner.go` copies the shape of
  `internal/runtime/docker/runner.go`, with one difference forced by this
  package's own job: docker's `execRunner` binds to one fixed binary name at
  construction, but doctor's probes shell out to `uname`, `docker`, `wsl.exe`,
  and `powershell.exe`, so `execRunner` here resolves `argv[0]` with
  `exec.LookPath` fresh on every call instead. The twelve probes live in
  `probes_os.go` (`os_version`, `cpu_virtualization`, `vm_platform_feature`),
  `probes_wsl.go` (`wsl_installed`, `wsl_version_2`, `wsl_kernel_current`),
  `probes_docker.go` (`docker_present`, `docker_daemon_running`),
  `probes_terminal.go` (`terminal_vt_support`, `windows_terminal`),
  `probes_sandbox.go` (`sandbox_health`), and `probes_disk.go`
  (`disk_free`), each embedding `baseProbe` and each guarded by its own
  `ctx.Err()` check both before and after any subprocess call, so a hung
  `docker version` costs exactly its own five-second `probeTimeout` and not the
  rest of the report. Four files sit behind build tags because they touch a
  real OS API: `probes_os_windows.go` (`RtlGetVersion`), `probes_os_other.go`
  (`uname -r` through the runner), `probes_disk_windows.go`
  (`GetDiskFreeSpaceEx`), and `probes_disk_unix.go` (`unix.Statfs`, reading
  `Bavail * Bsize`, the available-to-unprivileged-users count). Every pure
  classifier (`classifyKernelRelease`, `classifyWindowsBuild`,
  `classifyCPUFlags`, `decodeWSLText`, `classifyWSLList`, `classifyWSLVersion`,
  `classifyDockerOutput`, `classifyDiskFree`) lives in the untagged file next to
  its probe, so the Windows classifier is unit tested on the ubuntu leg and the
  Linux one on the windows leg, and only the two syscall wrappers actually need
  the tag.
- **No new dependency, no `go.mod`/`go.sum` edit.** `golang.org/x/sys` was
  already a direct requirement at v0.34.0 as of #68; this adds imports of its
  `unix` and `windows` subpackages, verified against the pinned version before
  writing a line of the build-tagged files: `RtlGetVersion` and
  `GetDiskFreeSpaceEx` exist with the signatures assumed, and `unix.Statfs_t`
  has `Bavail` and `Bsize` on both `linux/amd64` and `darwin/arm64`, though
  `Bsize` is `int64` on Linux and `uint32` on Darwin, which is why the
  multiplication casts both operands to `uint64` explicitly rather than relying
  on one side's type. `golang.org/x/sys` is not in `confinedImports` in
  `internal/archtest/dependencies_test.go`, so a second importer needed no
  table edit.
- **The layer.** `internal/doctor` already carried `"internal/doctor": 0` in
  `internal/archtest/layers_test.go`'s table before this ticket, so no archtest
  edit was made or needed. `sandbox_health` reaches the sandbox through the
  `SandboxProber` interface declared in this package and satisfied by the
  caller at L5; `internal/doctor` imports nothing from this module except
  `internal/platform` and `internal/platform/ux`, confirmed by reading every
  import in the package rather than by inspection alone.
- **`internal/platform/ux` gained `DocURL(anchor string) string`.** `Render`
  now calls it instead of building the `docBaseURL + "#" + anchor` string
  itself, so the two cannot drift. `TestDocURLOmitsTheFragmentForAnEmptyAnchor`
  is new; every existing `ux` test still passes with no assertion changed.
- **The exit status contract (AC3), the case a reviewer should check first.**
  `cmd/shellforge/cmd_doctor.go`'s `doctorExitError` returns a `*ux.Error` when
  and only when at least one `Result.Status` is `Fail`; a report with only
  `Warn` rows returns `nil`, and `main.go`'s existing `os.Exit(1)` on a non-nil
  `RunE` error is the only place doctor's exit status is decided. No
  `os.Exit` call was added anywhere in this package or in `cmd_doctor.go`.
- **`--fix`'s entire allowlist is one action.** `fixActions()` maps exactly
  `"disk_free"` to `platform.EnsureDir(platform.DataDir())`. `disk_free`
  reports `Fixable: true` with status `Warn`, not `OK`, in the one case where
  free space is sufficient and the data directory does not exist yet: `Fix`
  only considers a result whose status is not `OK`, so an `OK` "fixable" row
  would silently never be reached, which is the one place this run's own first
  draft got the design wrong and a test caught it (see the refusal-test note
  below). Every other remediation this package can report needs elevation
  (`wsl --install`, `dism.exe /online /enable-feature`, `wsl --update`,
  starting the Docker daemon) and is only ever printed, never executed. Every
  remediation string that also appears in `docs/01-install-windows.md` or
  `docs/05-troubleshooting.md` was checked against those files by hand and
  matches verbatim; no doc was edited, because none needed to be: all twelve
  anchors already existed as headings.
- **The two temporary-violation confirmations the plan required, both done
  and both reverted before this entry was written.** `TestRunMutatesNothing`
  and `TestProbesNeverInvokeAFixAction` were confirmed to fail with a one-line
  `platform.EnsureDir(dir)` call inserted unconditionally into
  `diskFreeProbe.Run`: both went red, one on the filesystem-hash comparison
  and one on `platform.DataDir()` existing where it should not, then the line
  was removed and both went green again. Separately,
  `TestPackageMakesNoRemovalCall` was confirmed to fail with a temporary
  `os.RemoveAll` call added to `probes_disk.go`, reporting the exact file,
  line, and selector, then the call was removed and the test passed again.
- **Manually exercised on this Linux host**, against the real, broken machine
  RECON already described (no `vmx`/`svm` flag visible in `/proc/cpuinfo`
  inside this container, Docker daemon down): `shellforge doctor` printed
  three `ok`, two `warn`, two `fail` rows with a `Fix:` and `More detail:` line
  under every non-`ok` row and exited 1; `shellforge doctor --json` produced
  the same report as parseable JSON on stdout with the `*ux.Error` on stderr,
  stdout untouched; `shellforge doctor --fix` created exactly
  `~/.local/share/shellforge` at mode `0700`, printed one `Fixed:` line and
  three `Not fixed, run it yourself:` lines naming the remediations it does
  not own, and created nothing else. `shellforge help` and `shellforge
  version` still work unchanged.
- **What is honestly not verified here.** No WSL probe has ever seen a real
  `wsl.exe`: `wsl_installed`, `wsl_version_2`, and `wsl_kernel_current` are
  covered only through the fake `runner`, including the UTF-16LE decoding and
  the localized-`wsl --status`-body table the ticket's test plan named. No
  Docker probe has seen a real daemon in Windows-container mode; that state is
  covered the same way, through `classifyDockerOutput`'s table test. The two
  Windows-only syscall wrappers, `RtlGetVersion` and `GetDiskFreeSpaceEx`, are
  covered by `GOOS=windows go build ./...` and `GOOS=windows go vet ./...`
  compiling clean on this host, and by the pure classifiers they feed being
  unit tested on Linux, but neither wrapper has executed on a real Windows
  machine in this run. `GOOS=darwin GOARCH=arm64 go build ./...` also compiles
  clean, unexercised beyond that. `sandbox_health` has only ever seen a `nil`
  prober and a scripted fake one; no real runtime has ever answered its
  `Ping`. The one remaining acceptance criterion this entry cannot close by
  itself is a human on real Windows 11 confirming the same table renders
  sensibly in the legacy console and in Windows Terminal, which needs the
  hardware `docs/01-install-windows.md` keeps asking for.
- **Deviations from the plan, recorded rather than hidden.** (1) The plan
  described the `--json`/`--fix` output seam as an unexported `renderJSON`
  call made directly by "the CLI"; that cannot compile, since `cmd/shellforge`
  and `internal/doctor` are different packages, so `render.go` gained one
  exported wrapper, `RenderJSONWithFixOutcome(w, r, fixed, refused)`, that
  `RenderJSON` itself now calls with `nil, nil`. (2) `cmd_doctor.go` reads the
  build version from this package's own `version` package-level variable
  (already set by the linker for every other verb) rather than threading a
  `VersionInfo` parameter through `newDoctorCommand`, so that the call site
  the plan specifies literally, `newDoctorCommand(nil)`, compiles exactly as
  written. (3) `disk_free`'s "fixable, space is fine, directory missing" case
  reports `Warn`, not `OK`: seven paragraphs earlier in the plan's own design
  notes it says `Fix` only ever considers a non-`OK` result, so an `OK`
  "fixable" row cannot exist without contradicting that sentence, and Warn is
  also the more honest word for "not yet initialized" than "passing".
- **Gates run on this host:** `gofmt -s -w .`, `go vet ./...`, `go build ./...`,
  `go test ./...`, `go test -race ./...`, `go test ./internal/archtest/...`,
  `./scripts/check-punctuation.sh`, `./scripts/check-allowlist-regexp.sh`,
  `./scripts/check-links.sh`, `./scripts/check-cli-package.sh`,
  `python3 scripts/check-ci-gates.py`, `GOOS=windows GOARCH=amd64 go build
  ./...`, `GOOS=windows GOARCH=amd64 go vet ./...`, and `GOOS=darwin
  GOARCH=arm64 go build ./...`, all green. `govulncheck`, `gosec`, and
  `pytest` over `scripts/tests` are not installed on this host and are left to
  CI; the Docker daemon is down on this host, which #70 needs for none of its
  tests, all of which go through the fake runner.

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
