# Day 2 Ticket Plan: content engine and verification

> **Status: working plan, not a contract.** Once these are filed as GitHub issues,
> the issues are the source of truth and this document is a record of how the day
> was scoped. Where it disagrees with an issue, the issue is right.
>
> Written against [SEVEN-DAY-PLAN.md](SEVEN-DAY-PLAN.md) Day 2,
> [SESSION-PROMPTS.md](SESSION-PROMPTS.md) sessions C and D, and
> [../LEVEL-FORMAT.md](../LEVEL-FORMAT.md), which is the contract every ticket
> below implements against.
>
> **Revision 2.** The first pass produced fourteen tickets. This pass merges them
> into eight. What was merged, what was not, and why, is in section 5.

---

## 1. What Day 2 has to produce

The four exit criteria from the seven day plan, unchanged:

- [ ] `shellforge run nav-01` through `files-04` all playable from YAML with zero Go changes
- [ ] `shellforge author validate packs/core-linux-basics` catches a duplicate id, a
      cycle, a missing asset, a missing `on_fail`, and an unknown check type
- [ ] A failing check prints its authored `on_fail`, not a generic error
- [ ] Only the first failing required objective is shown, and passed objectives show as passed

Every ticket below traces to one of those. A ticket that traces to none of them is
not Day 2 work.

---

## 2. Two findings that change the plan

Both were found while scoping, and both are cheap now and expensive at 17:00.

### 2.1 There is no CLI. `make build` is broken.

`PROGRESS.md` says the CLI dispatcher is a skeleton with every verb registered.
That is stale. `cmd/` does not exist in the tree at all:

```
$ go build ./cmd/shellforge
stat /home/user/ShellForge/cmd/shellforge: directory not found
```

`Makefile` sets `PKG := ./cmd/shellforge`, so `make build` fails. CI has not caught
it because CI runs `go build -v ./...`, which is green across a module with no main
package. Three of the four Day 2 exit criteria are CLI invocations, so nothing on
this day can be demonstrated until the binary exists. This is ticket **A**, and it is
the first thing that lands.

It also means issue #12, "migrate the dispatcher to cobra", is written against a
dispatcher that is not there. Ticket A replaces it, and #12 should be closed as
superseded rather than left to confuse whoever picks it up.

### 2.2 `SF_STATE` defaults inside the read-only mount

`images/rc/instrument.bash` defaults `SF_STATE` to `/opt/shellforge/state` and then
does `mkdir -p` on it. Non-negotiable 2 in `CLAUDE.md` says `/opt/shellforge` is the
one host mount and it is read-only. So either the snapshot writes fail silently
every prompt, which makes `env_var` and `cwd_is` permanently unsatisfiable, or the
mount is not actually read-only, which is worse.

The fix is that the runtime injects `SF_STATE` at a writable in-sandbox path, and
the default in `instrument.bash` becomes a path that is writable rather than one
that happens to sit under the read-only mount. Scoped into **ticket E**, which owns
the state directory.

---

## 3. Day 1 debt that blocks Day 2

The go/no-go in the seven day plan was explicit: if the PTY multiplexer and the OSC
parser are not working, fix them on Day 2 morning and do not proceed to content. The
parser is done. The multiplexer is not, and neither is any runtime backend.

| Issue | Title | Why it blocks |
|---|---|---|
| [#9](https://github.com/JoottunAtish/ShellForge/issues/9) | `DockerRuntime` via the docker CLI | Every check runs through `Session.Exec`. With no backend, nothing in `internal/verify` can be tested against a real sandbox, and `runtimetest` has still never run green. |
| [#10](https://github.com/JoottunAtish/ShellForge/issues/10) | PTY multiplexer with raw mode, resize, and command events | Without it there is no attached shell, so `run <level>` has nothing to attach and the `check` shim has no host listening on the control FIFO. |
| [#11](https://github.com/JoottunAtish/ShellForge/issues/11) | Demo: one hardcoded level playable end to end | The Day 1 gate, and the proof that the control channel round trip works, which ticket H generalises from one hardcoded level to any level. |

Tickets A through F are all testable against a fake `Session`, so they can proceed in
parallel with the debt. Only H hard-blocks on it.

---

## 4. Decisions taken while scoping

| Decision | Choice | Why |
|---|---|---|
| YAML parser | `github.com/goccy/go-yaml` | Already named in SESSION-PROMPTS Day 2 session C. Pure Go, YAML 1.2, and it reports line and column on an unmarshal error. The validator must name the file, the level id and the field, and a parser without positions cannot do that. |
| SQLite in scope | Yes, Day 2 | Pulled forward from Day 4 task 4.1 by decision. Task 2.6 wants journal events in SQLite, and splitting the journal across two days would mean writing a sink twice. |
| SQLite driver | `modernc.org/sqlite` | Pure Go, no cgo. `mattn/go-sqlite3` is faster but forces a C toolchain and breaks trivial cross-compilation, which is the entire reason the project is in Go. **The one dependency choice not yet explicitly approved. Veto it on ticket A if you disagree.** |
| `command_matched` and the journal ban | Validator rejects a journal check that is neither `optional: true` nor `severity: warn` | `internal/journal/doc.go` states the journal is learner-influenced and must never be evidence for scoring, and LEVEL-FORMAT calls command checks style and bonus only. Nothing enforced that. Now something does. |
| How `verify` reads the journal | Through a `JournalReader` interface that `verify` declares, satisfied by `internal/journal` | Lets `internal/verify` keep its promise of dropping the `internal/journal` import while the two command check types still work. `verify/doc.go` says a test will hold that line. |
| `author scaffold` | Cut to Day 5 | No Day 2 exit criterion needs it, and the eight levels in ticket G are transcribed from `docs/CURRICULUM.md` by hand, not scaffolded. It is authoring convenience for the Day 5 content sprint, which is when volume authoring actually happens. |
| Level count | 8, `nav-01` to `files-04` | The exit criterion names exactly this range. `files-05` is the Act II boss and is Day 5 work. |

---

## 5. Consolidation: fourteen tickets down to eight

### What merged

| New | Absorbs | Why it is one ticket |
|---|---|---|
| **A** cli and dependencies | old D2-0 + D2-1 | Cobra is itself a dependency, so the old CLI ticket already touched `go.mod`. Two `go.sum` reviews for one module bootstrap was busywork. |
| **B** content load, validate, `author validate` | old D2-2 + D2-3 + D2-10 | These are one exit criterion end to end. The loader is untestable without something asserting the results are legal, the validator is untestable without a loader, and the CLI verb is a thin wrapper that exists only so the criterion can be demonstrated. Cutting `author scaffold` kept this to a sane size. |
| **C** the check catalogue | old D2-4 + D2-5 | One capability: the thirteen types from LEVEL-FORMAT section 3. The old split was by what each type reads, which is an implementation detail, not a capability. The skill's own rule is to split by capability. |
| **F** store and journal | old D2-8 + D2-9 | One deliverable: commands persist and are readable back. Also the pair most likely to slip to Day 4, and slipping one unit is a cleaner decision than slipping two. |
| **G** the eight levels | old D2-11 + D2-12 | One exit criterion, no Go, and the same golden-test harness proves all eight. |

### What did not merge, and why

- **C and D stay apart.** Merging the catalogue with the engine gives a 6 h ticket
  with roughly twenty acceptance criteria. They are also genuinely different jobs:
  C is thirteen assertions about state, D is execution order, timeouts, composition
  and result assembly. If you want six tickets rather than eight, this is the merge
  to make, and the cost is a pull request nobody can review properly.
- **E stays alone.** The setup runner is the only ticket whose teardown does `rm -rf`
  on a path that came out of a YAML file. It needs its own review with
  `destructive-safety` loaded, and burying that in a larger diff is how the
  catastrophic case gets waved through.
- **G and H stay apart.** H's acceptance criteria depend on G's levels existing, so
  they are adjacent, but writing eight briefings and wiring an integration are
  different work and the review for each looks nothing like the other.

### Net effect

Fourteen tickets, 23.5 h, became eight tickets, 23.0 h. **Merging did not make Day 2
smaller.** It made it fewer, larger units of review. The overrun in section 7 is
unchanged and still needs a decision.

---

## 6. The ticket set

```
A  cli+deps  the binary and the module's first dependencies      [blocks everything]
   |
   +-- C  verify   the check catalogue: all 13 types
   |      |
   |      +-- B  content  load, validate, and `author validate`
   |      |       |
   |      |       +-- G  content  levels nav-01 to files-04
   |      |
   |      +-- D  verify   composition, the engine, result assembly
   |
   +-- E  content  transactional setup and teardown runner
   |
   +-- F  store    SQLite, the journal, and the snapshot readers

D + E + F + G + #9 + #10 + #11  -->  H  cli  wire `run <level-id>` end to end
```

---

### A. cli and dependencies: the binary and the module's first two libraries

**Labels:** `type: feature`, `area: cli`, `dependencies` · **Supersedes:** #12 ·
**Est:** 1.5 h

**Summary.** `make build` produces `bin/shellforge` with every verb registered, and
the module has its first three dependencies pinned, reviewed and fenced.

**Context.** Finding 2.1: `cmd/shellforge` does not exist and `make build` fails. CI
missed it because `go build ./...` is green on a module with no main package. Three
of four exit criteria are CLI invocations, so this lands first. Cobra is itself a
dependency, so the module bootstrap belongs in the same ticket rather than in a
second PR touching the same two files.

**Layer and files.** L5. New: `cmd/shellforge/{main,root,version,commands_test}.go`.
Modified: `go.mod`, `go.sum`, `CLAUDE.md`, `internal/archtest/layers_test.go`,
`.github/workflows/ci.yml`.

**Interfaces.**

```go
func NewRootCommand(v VersionInfo) *cobra.Command
type VersionInfo struct{ Version, Commit, BuildDate string }
```

Verbs from the brief: `doctor`, `init`, `run`, `play`, `check`, `hint`, `reset`,
`map`, `stats`, `author`, `sandbox`, `version`. `author` and `sandbox` are groups
with stubbed subcommands.

**Acceptance criteria.**

- [ ] `make build` and `.\make.ps1 build` both produce a working binary, and CI builds `./cmd/...` explicitly so a missing main package fails the build
- [ ] `version` prints version, commit and build date from the ldflags already in `Makefile`; `help` lists all twelve verbs
- [ ] Every unimplemented verb exits non-zero through `ux.Fail` with a remediation, never a bare cobra error or a panic
- [ ] A table test asserts the registered verb set equals the documented set exactly
- [ ] `cobra`, `goccy/go-yaml` and `modernc.org/sqlite` are required at exact versions and `go mod tidy` is a no-op
- [ ] The stale "no dependencies yet" comment in `go.mod` is replaced by the approved set with a reason each, and `CLAUDE.md` agrees, asserted by a test so the two cannot drift
- [ ] `archtest` fails if `goccy/go-yaml` is imported outside `internal/content` or the sqlite driver outside `internal/store`, verified against a deliberate violation
- [ ] `govulncheck` and `gosec` green

**Approach.** `main.go` stays about ten lines: construct, execute, translate error to
exit status. All wiring in `root.go` so the test builds the tree without running it.
`modernc.org/sqlite` is pure Go, so cross-compilation and the single static binary
promise hold; `mattn/go-sqlite3` would force cgo on every build host including
Windows. The archtest additions are the substance here: a dependency that stays
where it was put can be replaced later, and both of these plausibly will be.

**Out of scope.** Any verb's behaviour. Shell completion. Config loading. `--json`,
which belongs to the Day 3 doctor ticket. Vendoring.

**Safety review.** Supply chain, yes. `SECURITY.md` treats third party code as a
supply chain surface. Required: exact pins with no `+incompatible`, the `go.sum` diff
reviewed line by line in the PR rather than waved through, `govulncheck` green before
merge. No destructive path, so no refusal tests.

**Test plan.** Verb table equality. Per-stub test asserting non-zero exit and a
remediation on stderr. `NewRootCommand` with empty `VersionInfo` does not panic. Two
archtest entries, each verified against a deliberate violation. The `go.mod` and
`CLAUDE.md` agreement test. No golden test on help text: cobra reformats between
minor versions and the test would be noise.

**Docs.** `CLAUDE.md`, `PROGRESS.md` including the stale CLI row.

**Skills.** `implementation`, `go-style`, `security`, `testing`

**References.** SEVEN-DAY-PLAN task 1.1. ARCHITECTURE 4.1. `go.mod` header. `SECURITY.md`. Issue #12.

---

### B. content: load, validate, and `author validate`

**Labels:** `type: feature`, `area: content-engine` · **Blocked by:** A, C ·
**Est:** 4.5 h

**Summary.** A pack becomes typed Go values, every authoring invariant is enforced
with the file, level and field named, and `shellforge author validate` reports it.
Exit criterion 2, end to end.

**Context.** Levels are data; this makes that true. The three merged pieces are one
criterion: a loader with nothing asserting legality is untestable, a validator with
no loader has no input, and the verb is a thin wrapper whose only job is to let the
criterion be demonstrated. Do not invent fields: LEVEL-FORMAT section 2 is the
contract.

Blocked by C only for the final wiring, since `author validate` needs the real check
registry to answer "is this type known". The loader and validator themselves build
against the `TypeChecker` interface and a fake, so most of this ticket can run in
parallel with C.

**Layer and files.** L3 `internal/content`: `pack.go`, `level.go`, `check.go`,
`load.go`, `embed.go`, `validate.go`, tests per file,
`internal/content/testdata/invalid/` with one broken pack per rule. L5
`cmd/shellforge/author.go`. May import L2 and below plus `goccy/go-yaml`.

**Interfaces.**

```go
type Pack struct {
    ID, Name, Version, Author, License, Homepage, Description, Intro string
    ContentAPI int
    Acts  []Act
    Ranks []Rank
    Levels []Level // filled by the loader, absent from pack.yaml
}

type Level struct {
    ID, Title, Act, Briefing, Solution string
    Version, Difficulty, XP, ParCommands, EstimatedMinutes int
    Concepts, Prerequisites, Requires, Tags []string
    Boss       bool
    Objectives []Objective
    Setup      Setup
    Checks     []CheckSpec
    Hints      []Hint
    Teardown   Teardown
    SourceFile string // set by the loader, used in every validation error
}

// CheckSpec is deliberately loose. internal/verify owns per-type parameter
// shapes; content knows only the common fields and the composition nodes.
type CheckSpec struct {
    ID, Type, OnFail, Severity string
    Optional       bool
    TimeoutSeconds int
    AnyOf, AllOf   []CheckSpec
    Not            *CheckSpec
    Params         map[string]any // every key not listed above, verbatim
    Line           int
}

func LoadPack(fsys fs.FS, root string) (*Pack, error)
func Embedded() (*Pack, error)
func (p *Pack) Level(id string) (*Level, bool)
func (p *Pack) Order() ([]string, error) // acts in order, topological within

type Problem struct{ File, LevelID, Field, Message string; Line int }
// "levels/01-nav-01.yaml: nav-01: checks[2].on_fail: must not be empty"
type Report struct{ Problems []Problem }
func (r Report) OK() bool

type TypeChecker interface {
    Known(typeName string) bool
    ValidateParams(typeName string, params map[string]any) error
}

func Validate(p *Pack, tc TypeChecker) Report
```

**Acceptance criteria.**

- [ ] The worked example in LEVEL-FORMAT section 6 round trips with every field intact, and an unknown or mistyped field is a rejection naming file, level and field, never a silent drop
- [ ] `Embedded()` returns the real pack compiled in, with no filesystem access at runtime, and a pack with zero level files loads clean, since that is the repository's state today
- [ ] `Validate` reports **every** problem, not the first, with a fixture in `testdata/invalid/` for each rule below
- [ ] Rules enforced: duplicate or malformed level id; prerequisite cycle or dangling prerequisite; missing asset; `source:` resolving outside the pack after symlink resolution; empty `on_fail` at any composition depth; unknown check type or malformed parameters; objective and check ids not one to one; empty `solution`; fewer than two hints; zero non-optional checks; `setup.root` not strictly under `/home/learner/`
- [ ] A `command_matched` or `command_not_matched` check that is neither `optional: true` nor `severity: warn` is a rejection, and LEVEL-FORMAT is updated in the same commit to state the rule
- [ ] An act listing a level id with no file is a **warning**, not an error, since that is the pack's normal state while being filled in
- [ ] `author validate packs/core-linux-basics` exits 0 today with those warnings, exits non-zero on each of the five failure modes the exit criterion names, prints one problem per line in file order, and supports `--json`
- [ ] Errors reach the user through `ux.Fail`, never a bare Go error

**Approach.** Decode with `yaml.DisallowUnknownField` so a typo is a rejection rather
than a default. Capture `Params` with a two pass decode, typed then `map[string]any`
with the known keys deleted: uglier than a custom unmarshaler and far easier to keep
correct as fields are added. `SourceFile` and `Line` are populated by the loader and
are not YAML fields; they exist so errors can name a place.

Report everything. An author fixing one error per run stops using the validator.
Output is one problem per line so it pastes into a quickfix list.

The `source:` containment check resolves symlinks **before** comparing prefixes.
Comparing strings first is the classic way to let `assets/../../etc` or a symlinked
asset directory through.

**Out of scope.** Running any check. Materialising any file, which is E. `author
scaffold` and `author test`, both Day 5. Validating that the solution actually
solves the level, which is the golden test. `.tar.zst` and remote packs, banned
outright.

**Safety review.** Yes, path resolution. `destructive-safety` applies to the
`source:` and `setup.root` rules. Required refusal tests: `source:` of
`../../../etc/passwd`; `source:` as a symlink pointing outside the pack;
`setup.root` of `/`, of `/home`, of **`/home/learner` exactly**, of
`/home/learner/../../etc`, and of a relative path. The bare `/home/learner` case is
the one that matters most and the one most likely to be missed: it passes a naive
"under /home/learner/" prefix test and it is the learner's entire home, which
teardown would `rm -rf`.

**Test plan.** One fixture pack per rule, each asserting exact `Problem` fields, not
merely that an error occurred. A valid fixture asserting zero problems, which is what
catches an over-eager rule. Table test over malformed YAML: unknown field, wrong
type, duplicate key, truncated file, scalar where a list belongs. The refusal tests
above, each individually named so a failure says which containment case broke. A
`--json` shape test. Fuzz the loader over the pack directory if it costs under an
hour, since a panic reachable from a community pack is a real risk.

**Docs.** `docs/LEVEL-FORMAT.md` for the new journal check rule, `docs/07-authoring-levels.md`, `PROGRESS.md`.

**Skills.** `level-authoring`, `destructive-safety`, `security`, `testing`, `go-style`, `writing-style`

**References.** `docs/LEVEL-FORMAT.md` 1, 2, 6, 7. SESSION-PROMPTS Day 2 session C items 1, 2, 4. ARCHITECTURE 4.8. `internal/content/doc.go`. `internal/journal/doc.go`.

---

### C. verify: the check catalogue, all thirteen types

**Labels:** `type: feature`, `area: verify` · **Blocked by:** A · **Est:** 4.0 h

**Summary.** A check registry exists and every type in LEVEL-FORMAT section 3 is
registered, each reading real sandbox state through `Session.Exec`.

**Context.** Checks are the product; everything else on Day 2 exists so these can
run. The first pass split them by what each type reads, which is an implementation
detail. The capability is the catalogue.

Not blocked by E despite the snapshot dependency: the snapshot readers are unit
tested against fixture files and a fake `Session`, and only the end-to-end path in H
needs the real `SF_STATE` fix.

**Layer and files.** L3 `internal/verify`: `registry.go`, `check.go`, `fs_checks.go`,
`shell_checks.go`, `process_checks.go`, `journal_checks.go`, `script_check.go`,
`snapshot.go`, tests per file, `internal/verify/verifytest/` with a fake `Session`.
May import `internal/runtime` and below. **Must not import `internal/content` or
`internal/journal`.**

**Interfaces.**

```go
type Status string // pass | fail | warn | timeout | error
type Result struct{ ID string; Status Status; Message string; Duration time.Duration }

// Env is everything a check may read. A check needing something not here is a
// check reaching somewhere it should not.
type Env struct {
    Session   runtime.Session
    Root      string
    Snapshots Snapshots
    Journal   JournalReader
}

type Check interface{ ID() string; Run(ctx context.Context, env Env) Result }
type Spec struct {
    ID, Type, OnFail, Severity string
    Optional bool; TimeoutSeconds int
    Params map[string]any
}
type Factory func(s Spec) (Check, error) // errors on a malformed parameter

func Register(typeName string, f Factory) // panics on duplicate, called from init
func Lookup(typeName string) (Factory, bool)
func Types() []string

// Snapshots reads what instrument.bash writes each prompt, under $SF_STATE.
type Snapshots struct{ Dir string }
func (s Snapshots) Env(ctx context.Context, sess runtime.Session) (map[string]string, error)
func (s Snapshots) Cwd(ctx context.Context, sess runtime.Session) (string, error)

// Declared here, not imported. internal/journal satisfies it. This is what lets
// internal/verify keep its promise never to import the journal.
type JournalReader interface{ Commands(scope Scope) []string }
type Scope struct{ Kind ScopeKind; N int } // level | last_n:N | last
```

Types: `file_exists`, `file_absent`, `file_content`, `dir_exists`, `dir_tree`,
`file_mode`, `file_owner`, `symlink_target`, `env_var`, `cwd_is`, `process_running`,
`command_matched`, `command_not_matched`, `script`.

**Acceptance criteria.**

- [ ] All types registered, `Types()` returns exactly them, and registering a duplicate panics
- [ ] `file_content` implements all six match modes plus `ignore_case`; `dir_tree` implements `names_only` and `names_and_hashes`; `file_mode` accepts an exact mode and an `any_of` list
- [ ] `file_absent` distinguishes "does not exist", which passes, from "exists but cannot be stated", which is an error status, since a permissions level produces the second and a naive implementation reports the learner solved it
- [ ] `env_var` and `cwd_is` read the snapshots, so a variable exported in the learner's interactive shell and never in the check's shell passes; `env_var` with no `value` asserts only that it is set, including when set empty
- [ ] A missing or stale snapshot is a **distinct** error status saying the instrumentation did not run, not a fail that reads to the learner as their mistake
- [ ] `process_running` matches `ps -eo pid,args` and honours `negate`; the two journal types read through `JournalReader` and support all three scopes
- [ ] `script` runs as `learner` by default and `root` on request, exit 0 is pass, and a failing script's stdout is appended to the authored `on_fail`
- [ ] A malformed parameter is a construction error caught at pack load, not a runtime failure; every check is read-only; `internal/verify` imports nothing from `internal/journal`, asserted by a source-parsing test

**Approach.** Each check builds a single `argv` and runs it through `Session.Exec`.
Never build a command string: `Exec` takes `argv []string` and the package has no
string form, deliberately, and concatenating a learner-influenced path into a shell
string is exactly the injection that design prevents.

Hash in the sandbox with `sha256sum` for `sha256` and `names_and_hashes`, rather
than pulling files to the host. A level asset can be four thousand lines.

`env -0` is NUL separated and a value can contain a newline. Split on NUL and only on
NUL. Splitting on newline looks correct against every hand-written fixture and breaks
on the first multi-line value.

Snapshots are written by `PROMPT_COMMAND` after each command, so a check run before
the learner has pressed Enter once sees nothing. That is normal, not an error in
their work, and the message must say so.

**Out of scope.** Composition, the engine and result assembly, all D. The purity
test, which needs the engine, so also D. `alias_defined`, `function_defined`,
`shopt_set` and `last_exit_code`: all deferred to v0.2 by LEVEL-FORMAT even though
`instrument.bash` already writes the snapshots they would need. Do not implement them
because the data happens to be there.

**Safety review.** Yes, for `as_user: root`. A pack-supplied `as_user` reaching a
command line is untrusted input, and root inside the sandbox is still root. Validate
it against `^[a-zA-Z0-9][a-zA-Z0-9_-]*$` before use so it cannot start with a hyphen
and be read as a flag. Required refusal tests: `as_user` of `--privileged`, of
`-u root`, of `root learner`, and empty. Otherwise read-only by construction, with no
host-side deletion or path resolution.

**Test plan.** Table test per type against the fake `Session`: pass, fail, missing
target, permission denied. A test driven off `Types()` asserting every registered
type has a table test, so a new type cannot arrive untested. A snapshot fixture with
an embedded newline and an `=` in a value. The empty-snapshot case asserting the
distinct status. The four refusal tests. A test asserting no check's `argv` contains
a metacharacter that was not in its input. A source-parsing test on the journal
import, in the style of `internal/runtime/runtimetest`.

**Docs.** `PROGRESS.md`. `docs/05-troubleshooting.md` needs a heading for the
missing-snapshot doc anchor, which CI enforces. `docs/LEVEL-FORMAT.md` if any
parameter shape differs, fixed in the same commit.

**Skills.** `implementation`, `security`, `component-traps`, `testing`, `go-style`

**References.** `docs/LEVEL-FORMAT.md` 3 and 4. ARCHITECTURE 4.7 and 4.10. SESSION-PROMPTS Day 2 session D items 1, 2. `images/rc/instrument.bash`. `internal/verify/doc.go`.

---

### D. verify: composition, the engine, and result assembly

**Labels:** `type: feature`, `area: verify` · **Blocked by:** C (and #9 for the
purity test) · **Est:** 2.0 h

**Summary.** `any_of`, `all_of` and `not` compose, a level's checks run to
completion under timeouts, and the result matches LEVEL-FORMAT section 5 exactly.

**Context.** Exit criteria 3 and 4 both live here: the authored `on_fail` must be
what the learner sees, and only the first failing required objective may be shown.
The purity test also goes here, since it needs a full engine pass to hash across.

**Layer and files.** L3 `internal/verify`: `compose.go`, `engine.go`, `result.go`,
`purity_test.go`, plus tests.

**Interfaces.**

```go
func NewEngine(opts ...Option) *Engine
func WithCheckTimeout(d time.Duration) Option // default 10s
func WithLevelTimeout(d time.Duration) Option // default 60s

func (e *Engine) Build(specs []Spec) ([]Check, error) // once, at level load
func (e *Engine) Run(ctx context.Context, checks []Check, env Env) LevelResult

type LevelResult struct {
    LevelID        string            `json:"level_id"`
    Passed         bool              `json:"passed"`
    Objectives     []ObjectiveResult `json:"objectives"`
    PrimaryFailure *ObjectiveResult  `json:"primary_failure,omitempty"`
    Notes          []string          `json:"notes"`
    DurationMS     int64             `json:"duration_ms"`
}
```

`score` in LEVEL-FORMAT section 5 is populated by `internal/game` on Day 4. This
package leaves it out; say so in the doc comment so the omission reads as a decision.

**Acceptance criteria.**

- [ ] `any_of`, `all_of` and `not` nest arbitrarily and each composite reports its own authored `on_fail`, never a child's
- [ ] Checks run in declaration order and none short-circuits: a failure in check 1 still runs 2 through N, so the checklist is fully populated
- [ ] `PrimaryFailure` is the first failing **required** check only; optional and warn results appear in `Notes`; `Passed` is true only when every non-optional check passed
- [ ] A check over 10s is a timeout status with a message distinct from a failure, and the level is capped at 60s
- [ ] Cancelling the context stops promptly and reports cleanly, since the learner can Ctrl-C a `check`
- [ ] `LevelResult` marshals to LEVEL-FORMAT section 5 field for field
- [ ] **Purity:** hashing the sandbox filesystem before and after a full run over the `pipe-05` fixture gives an identical hash
- [ ] `pipe-05` from LEVEL-FORMAT section 6 exists as a real level file and is the reference fixture

**Approach.** Build once at load, run many times. A learner types `check` repeatedly
and re-parsing parameters each time is wasted work and a place for a parse error to
surface at the worst moment.

The purity test is the one with teeth. Hash with `find $ROOT -type f -exec sha256sum
{} +` sorted, **plus** a stat pass over mode, owner and size, since a check that
chmods without changing content would pass a content-only hash. It needs a real
sandbox, so it is build-tagged and runs in CI's image job, the same shape as the
runtime contract suite.

`pipe-05` is not one of the eight levels the exit criterion names. It is the engine's
test fixture, per SESSION-PROMPTS session D item 6, and it starts Act III at no cost.

**Out of scope.** Scoring, XP, hint penalties: Day 4. Live polling, optional in
ARCHITECTURE 4.10 and not in the day plan. Rendering to a terminal, which is H.

**Safety review.** No deletion, elevation or path resolution beyond what C does. The
purity test reads the whole level root and must read only under `setup.root`, never
above it, or a bug in the test hashes the learner's home.

**Test plan.** Composition tables including `not` wrapping `any_of` wrapping
`all_of`. Ordering test asserting all checks ran when the first failed. A test
asserting exactly one `PrimaryFailure` when three required checks fail. A test that a
warn failure does not set `Passed` false. Timeout and cancellation tests. A JSON
golden test against LEVEL-FORMAT section 5. The purity test, build-tagged.

**Docs.** `PROGRESS.md`. `docs/LEVEL-FORMAT.md` section 5 if the shape drifts.

**Skills.** `implementation`, `testing`, `go-style`, `component-traps`

**References.** `docs/LEVEL-FORMAT.md` 3 composition, 4, 5, 6. SESSION-PROMPTS Day 2 session D items 3 to 6. ARCHITECTURE 4.10.

---

### E. content: transactional setup and teardown runner

**Labels:** `type: feature`, `area: content-engine` · **Blocked by:** A, #9 ·
**Est:** 2.5 h

**Summary.** A level's `setup` block materialises into the sandbox as a transaction,
teardown always runs first, and running setup twice leaves the same state.

**Context.** ARCHITECTURE 4.9 specifies setup as a transaction with a `SETUP_OK`
sentinel and idempotency by construction. This ticket also owns the state directory
and therefore fixes finding 2.2, without which the snapshot checks in C cannot work
end to end.

Kept as its own ticket deliberately. It is the only place on Day 2 where teardown
does `rm -rf` on a path that came out of a YAML file, and that needs its own review
rather than a paragraph inside a larger diff.

**Layer and files.** L3 `internal/content`: `setup/runner.go`, `setup/generate.go`,
`setup/state.go`, tests. Modified: `images/rc/instrument.bash`.

**Interfaces.**

```go
func NewRunner(sess runtime.Session, packFS fs.FS) *Runner

// Setup always tears down first. Safe on a dirty sandbox and safe to call twice.
func (r *Runner) Setup(ctx context.Context, lvl *content.Level) error
func (r *Runner) Teardown(ctx context.Context, lvl *content.Level) error
func (r *Runner) IsSetUp(ctx context.Context, lvl *content.Level) (bool, error)

type Generator func(seed int64, lines int) ([]byte, error)
func RegisterGenerator(kind string, g Generator) // kind: loglines is all v0.1 needs
```

**Acceptance criteria.**

- [ ] Teardown runs before every setup, so setup on a dirty level root gives the same result as on a clean one
- [ ] Files materialise with `\r` stripped from every text file, and `mode` and `owner` applied
- [ ] `generate:` with a fixed seed produces byte-identical output on every run and both platforms
- [ ] `setup.script` runs as root, inside the sandbox, under `timeout_seconds`, default 30, after the files step
- [ ] A failure at any step rolls back, leaves no `SETUP_OK`, and reports through `ux.Fail` with a remediation
- [ ] `SETUP_OK` is written only after every step succeeded, and `IsSetUp` reads it
- [ ] `SF_STATE` resolves to a writable in-sandbox path and `instrument.bash` no longer defaults it under the read-only `/opt/shellforge` mount
- [ ] No network access is possible during setup, asserted by a test

**Approach.** Stage on the host, push once: materialise into a host staging
directory, strip `\r`, then push the tree with a single `PushFiles`. Per-file pushes
are slow on WSL and turn a rollback into a partial state.

Order is files, then `script`, then the sentinel. The script runs after files because
the worked example uses it to `chown -R` what the files step just wrote.

For 2.2, the state directory goes to a writable in-sandbox path owned by `learner`
and the runtime injects the value. `/opt/shellforge` stays read-only. **Do not solve
this by making the mount writable.**

**Out of scope.** `reset --hard` and `sandbox rebuild`, Day 3 and Day 6. Snapshots
and the rewind mechanic, cut from v0.1. Generators beyond `loglines`.

**Safety review.** **Yes, and this is the most dangerous ticket of the day.**
Teardown is `rm -rf` on a path from a YAML file written by a pack author.
`destructive-safety` applies in full.

Rules: resolve `setup.root` to an absolute real path and refuse anything not strictly
under `/home/learner/`; refuse `/home/learner` itself; refuse a path containing a
symlink component, resolved before the check, not after; the deletion runs inside the
sandbox through `Session.Exec` and never on the host, with a test asserting no host
filesystem removal call exists anywhere in the package.

Required refusal tests, each individually named: `setup.root` of `/`, `/home`,
`/home/learner`, `/home/learner/../..`, `/home/learner/quest/../../../etc`, a symlink
under `/home/learner` pointing at `/`, and a relative path.

**Test plan.** The refusal tests above. Idempotency: setup, mutate, setup again,
assert identical state. Rollback: a `setup.script` exiting non-zero, asserting no
`SETUP_OK` and a clean error. A `\r` test with a CRLF `.sh` asset, asserting the
materialised file runs rather than producing `bad interpreter: /bin/bash^M`.
Generator determinism across two runs of the same seed. A no-network test in the
shape archtest already uses.

**Docs.** `PROGRESS.md`. `docs/LEVEL-FORMAT.md` if `SF_STATE` becomes author-visible.
`docs/05-troubleshooting.md` for every new `DocAnchor`, which CI enforces.

**Skills.** `destructive-safety`, `security`, `implementation`, `testing`, `level-authoring`, `component-traps`

**References.** ARCHITECTURE 4.9. `docs/LEVEL-FORMAT.md` 2 setup. SESSION-PROMPTS Day 2 session C item 3. `images/rc/instrument.bash`. `CLAUDE.md` non-negotiables 2, 5, 7, 8.

---

### F. store and journal: persistence and the command journal

**Labels:** `type: feature`, `area: store` · **Blocked by:** A, #10 · **Est:** 3.0 h

**Summary.** A versioned SQLite database opens at `DatabasePath()` in WAL mode, and
every command the learner runs is recorded and readable back through the
`JournalReader` interface `internal/verify` declares.

**Context.** Pulled forward from Day 4 task 4.1 so the journal has somewhere to write
on Day 2. The PTY multiplexer emits command events; this consumes them.
`instrument.bash` already writes `journal.tsv` inside the sandbox, so there are two
producers to reconcile. Merged because the schema and its only writer are one
deliverable, and because this pair is the most likely thing to slip back to Day 4,
which is a cleaner call to make about one ticket than two.

**Layer and files.** L0 `internal/store`: `store.go`, `migrate.go`,
`schema/001_init.sql`. L2 `internal/journal`: `journal.go`, `tsv.go`. Tests
throughout. `internal/store` may import `internal/platform` and the standard library
plus the driver, and nothing else in the module.

**Interfaces.**

```go
func Open(path string) (*Store, error) // creates parent dirs, migrates, sets WAL
func (s *Store) Migrate(ctx context.Context) error
func (s *Store) SchemaVersion(ctx context.Context) (int, error)

type Entry struct {
    Seq int64; TS time.Time
    LevelID, Cwd, Raw string
    Exit int; DurationMS int64
    UsedTab, UsedHistory bool
}

func New(s *store.Store) *Journal
func (j *Journal) Append(ctx context.Context, e Entry) error
func (j *Journal) Commands(scope verify.Scope) []string // satisfies verify.JournalReader
func (j *Journal) Level(ctx context.Context, levelID string) ([]Entry, error)
func ReadTSV(r io.Reader) ([]Entry, error) // the in-sandbox journal
```

**Acceptance criteria.**

- [ ] `Open` on a nonexistent path creates and migrates; on a current database it is a no-op; migrations apply in order, are recorded, and never re-run
- [ ] WAL mode and a busy timeout are set, so two processes do not deadlock, verified under `-race`
- [ ] A database from a newer schema version is refused with a clear message rather than corrupted
- [ ] The `events` table matches the journal record in ARCHITECTURE 4.7, and an appended `Entry` reads back with every field intact
- [ ] `Commands` satisfies `verify.JournalReader` for all three scopes **without** `internal/verify` importing this package
- [ ] `ReadTSV` parses exactly what `instrument.bash` emits, a command containing a tab does not corrupt the parse, and a truncated final line, which is what a crash mid-write produces, is skipped rather than failing the read
- [ ] Journal contents are never logged, never printed by default, never in a bug report body, asserted by a test
- [ ] `Open` failures go through `ux.Fail` with a remediation and a doc anchor

**Approach.** Embed migration SQL with `embed.FS`, apply in filename order. No
migration framework: a dependency for forty lines is not worth the supply chain
surface. No down migrations; recovery from a bad schema is to delete the database,
acceptable because the only loss is game progress and the alternative is untested
rollback code.

`Append` is on the hot path of every prompt, so no fsync per command. WAL plus a
transaction per batch is enough: losing the last few commands to a crash costs an
efficiency bonus, not correctness, because verification never reads the journal to
decide pass or fail.

The TSV is `EPOCHREALTIME \t exit \t PWD \t command`, so split bounded to four
fields. An unbounded split corrupts on a command containing a tab.

**Out of scope.** Every query above the schema. Progression and scoring tables beyond
ARCHITECTURE 4.11. Replay and watch-your-solution. Achievements, Day 4. `used_tab`
and `used_history` detection, which needs keystroke tapping and is explicitly
optional in ARCHITECTURE 4.6; the fields exist and stay false.

**Safety review.** Path resolution, and privacy. `DatabasePath()` holds the learner's
progress and must never sit under a directory a cache clear would delete, which is
issue #40, already fixed in `paths.go`. Required: a test asserting `DatabasePath()`
is not under `CacheDir()`, so a future change to either function fails here too, and
a test asserting neither package contains a filesystem removal call. Privacy: a
journal entry can contain a password typed in error, so a test asserts no code path
writes `Entry.Raw` to a log, and the field carries a doc comment saying so.

**Test plan.** Fresh open, reopen, migrate-twice idempotency, newer-version refusal,
corrupt-file error path, concurrent open under `-race`, all against `t.TempDir` and
never a real user path. TSV table: a tab in the command, a truncated line, an empty
file, a negative exit code. A scope test for all three kinds. A test that
`internal/verify` compiles against `*Journal` as a `JournalReader`, placed in this
package so the dependency direction stays correct.

**Docs.** `PROGRESS.md`. `docs/05-troubleshooting.md` for the new doc anchors.

**Skills.** `implementation`, `go-style`, `security`, `testing`, `destructive-safety`, `component-traps`

**References.** ARCHITECTURE 4.7 and 4.11. `internal/journal/doc.go`. `internal/platform/paths.go`. `images/rc/instrument.bash`. Issue #40. SEVEN-DAY-PLAN task 2.6.

---

### G. content: levels nav-01 to files-04

**Labels:** `type: content`, `area: content-engine` · **Blocked by:** B · **Est:** 3.5 h

**Summary.** The eight levels the first exit criterion names exist as real YAML,
validate clean, and pass their golden tests. Zero Go changes.

**Context.** `docs/CURRICULUM.md` already specifies all eight in detail: concepts,
XP, difficulty, par, objectives, setup, checks, hints and teaching notes. This is
transcription plus judgement, not design. The largest single ticket of the day, and
the one most likely to want splitting back into Act I and Act II if it drags.

**Layer and files.** New: `packs/core-linux-basics/levels/01-nav-01.yaml` through
`08-files-04.yaml`, plus assets. No Go.

**Acceptance criteria.**

- [ ] All eight validate clean and pass their golden tests: checks all fail on a fresh sandbox, the solution runs, checks all pass, teardown leaves nothing
- [ ] Every check has an `on_fail` naming what to try next, not what went wrong, and every bonus objective is `optional: true` so no command-pattern check can gate passing
- [ ] Setup is deterministic throughout: `nav-04`'s spread mtimes come from `touch -d`, not wall clock; `files-02`'s 4000-line `deliveries.log` comes from a seeded generator and is byte-identical every run
- [ ] `nav-01` is winnable in under 60 seconds by someone who knows `pwd`, and `nav-03`'s decoy tree means a relative path from the wrong directory fails, which is the lesson
- [ ] `nav-04`'s hints include the `/`-to-search-in-`less` hint, since beginners get trapped in the pager
- [ ] `files-03` uses `dir_tree names_and_hashes` for the backup and a `file_exists`/`file_absent` pair for the rename, and keeps the `cp dir dest` without `-r` trap with an `on_fail` that explains it
- [ ] `files-04` asserts `important/` is byte-identical after the deletions, which is the real test of the level, and its briefing states there is no recycle bin **before** anything is deleted
- [ ] No level's setup or teardown script references an absolute path outside its own `setup.root`

**Approach.** Write the checks first and the briefing last. It is far easier to write
a briefing promising exactly what the checks verify than to retrofit checks onto
prose.

Zero Go changes are permitted. If a level cannot be expressed, that is a finding
about the engine and it gets its own issue; it does not get a special case in Go,
because the first exit criterion is that levels load with zero Go changes.

`files-04` is where a learner is most likely to delete the wrong thing, and that is
intentional pedagogy: the curriculum wants the mistake to become an achievement
rather than a punishment. The level's side of that is a briefing that warns and an
`on_fail` on the `important/` check that is kind about it.

**Out of scope.** `files-05`, the Act II boss, and levels 9 to 25: Day 5. ASCII art
and presentation polish.

**Safety review.** Yes, per level, and more so for Act II, which teaches deletion.
Every deletion the learner performs is inside the sandbox and cannot reach the host,
which is the runtime's guarantee, not the level's. What each level owns is that its
own setup and teardown scripts stay inside `setup.root`, every `setup.root` is under
`/home/learner/`, and every `source:` is inside the pack. Required: a review pass
over all eight scripts confirming no outside absolute path appears.

**Test plan.** `author validate`. A golden test per level. A manual playthrough of all
eight with friction noted in the PR, including deliberately deleting `important/` in
`files-04` and confirming `reset` recovers it. A level that validates and is still
confusing is a level that failed.

**Docs.** `PROGRESS.md`. `docs/CURRICULUM.md` on any design change made while
writing, so the curriculum stays the record of what was built.

**Skills.** `level-authoring`, `writing-style`, `destructive-safety`

**References.** `docs/CURRICULUM.md` levels 1 to 8. `docs/LEVEL-FORMAT.md` 2 and 6.

---

### H. cli: wire `run <level-id>` end to end

**Labels:** `type: feature`, `area: cli` · **Blocked by:** D, E, F, G, #9, #10, #11 ·
**Est:** 2.0 h

**Summary.** `shellforge run nav-01` loads the level from YAML, sets up the sandbox,
prints the briefing and objectives, attaches a real shell, and answers `check` with
authored results.

**Context.** The ticket that turns seven libraries into the four exit criteria. It
generalises what #11 proves for one hardcoded level to any level in the pack. Last in
the order, but start it before the others finish so the seams are found early.

**Layer and files.** L5 `cmd/shellforge/run.go`, plus the minimum L4
`internal/game/session.go`. `internal/game` must not import any runtime
implementation; it takes a `runtime.Runtime`.

**Acceptance criteria.**

- [ ] `run nav-01` through `run files-04` all work with **zero Go changes between them**
- [ ] The briefing renders markdown to ANSI and the objective checklist prints before the shell attaches
- [ ] Typing `check` in the sandbox returns results to the learner's terminal through the existing control FIFO
- [ ] A failing check prints its authored `on_fail`, only the first failing required objective is shown, passed objectives print as passed, and warn notes print below
- [ ] `run` on an unknown level id fails through `ux.Fail`, naming the ids that do exist
- [ ] Exiting the shell tears the level down and leaves no stray processes
- [ ] Ctrl-C during a check cancels it and returns to the prompt without killing the session
- [ ] A crash or Ctrl-C leaves no sandbox running, via the **same** cleanup code as the normal path

**Approach.** The control channel already exists in the image: `images/bin/check`
writes to `$SF_STATE/control.req` and reads `control.res`, and #11 implements the host
listener for one level. This ticket replaces the hardcoded level with a loaded one
and the hardcoded check with D's engine. **Do not build a second control mechanism.**

Keep `internal/game` thin. The full orchestrator state machine is Day 4 task 4.2.
What this needs is load, setup, brief, attach, check, teardown, and nothing about
scoring, XP or unlocks.

**Out of scope.** Scoring, XP, hints, `map`, `stats`, achievements, resume, and
`play`: all Day 4. Live objective polling.

**Safety review.** Yes, via teardown, so E's containment rules apply transitively.
Additional rule here: the crash and Ctrl-C cleanup path must be the same code as the
normal path, not a second implementation that was never tested.

**Test plan.** An end-to-end test per level in CI's image job: run headless, assert
checks fail, apply the solution through `Exec`, assert checks pass, assert teardown
left nothing. That is the golden test contract from `docs/LEVEL-FORMAT.md` section 7
and it is what `make golden` should run. A test for the unknown level id message. A
manual pass with `vim` open when `check` is typed, since that is where the control
FIFO and the PTY interact.

**Docs.** `docs/03-quickstart.md`, `docs/04-how-it-works.md`, `PROGRESS.md`,
`docs/05-troubleshooting.md` for any new doc anchor.

**Skills.** `implementation`, `component-traps`, `testing`, `writing-style`, `destructive-safety`

**References.** ARCHITECTURE 4.5 and 5.2. SESSION-PROMPTS Day 2 session C closing paragraph. `docs/LEVEL-FORMAT.md` 7. Issues #9, #10, #11.

---

## 7. Sizing and order

| Ticket | Est |
|---|---|
| A cli and dependencies | 1.5 h |
| B content load, validate, `author validate` | 4.5 h |
| C the check catalogue | 4.0 h |
| D composition, engine, result | 2.0 h |
| E setup and teardown runner | 2.5 h |
| F store and journal | 3.0 h |
| G levels nav-01 to files-04 | 3.5 h |
| H wire `run <level-id>` | 2.0 h |
| **Total** | **23.0 h** |

The seven day plan budgets 12.5 h for Day 2. This is 23.0 h, before the three Day 1
debt tickets. **Day 2 as scoped does not fit in Day 2**, and merging tickets did not
change that: it saved 0.5 h by cutting `author scaffold`, and nothing else.

Two honest options:

1. **Accept the overrun and cut from the ladder.** Nothing in this list is on the cut
   ladder in `PROGRESS.md`, which is itself the signal: this is all engine, and the
   engine is the thing that must not be cut.
2. **Slip ticket F.** At 3.0 h it is the only ticket no exit criterion depends on.
   SQLite was pulled forward by choice, not by need. Slipping it to Day 4 brings the
   day to 20.0 h and costs nothing demoable: the journal checks in C read through
   `JournalReader`, so they can be tested against a fake and wired to the real
   journal on Day 4 with no change to `internal/verify`.

Recommendation: option 2 if the day is running long by lunchtime, and make that call
by then rather than at midnight.

**Order.** A first, alone. Then two tracks: C then D on verification, and B then G on
content, with E and F slotted in whenever a track blocks on the Day 1 debt. B is
blocked by C only for the final wiring, so most of it runs in parallel. H last, then
the four exit criteria demonstrated in one sitting, then a `PROGRESS.md` entry. If
the criteria cannot be demonstrated, the entry says so plainly, which is the rule
from the build plan.

---

## 8. Deliberately not done

- **No `author scaffold` or `author test`.** Neither is needed by an exit criterion,
  and both are authoring convenience for the Day 5 content sprint. The golden test
  contract is implemented as CI tests inside H rather than as a user-facing verb.
- **No live objective polling.** ARCHITECTURE 4.10 calls it enormously satisfying and
  it is, but it is not in the day plan and it is a Day 4 presentation concern.
- **No check types beyond the thirteen.** `CLAUDE.md` bans this outright and
  LEVEL-FORMAT lists fifteen more as v0.2. Anything needing one uses `type: script`.
- **No `files-05`.** It is the Act II boss and the exit criterion stops at `files-04`.
