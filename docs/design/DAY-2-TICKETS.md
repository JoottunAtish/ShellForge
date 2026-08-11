# Day 2 Ticket Plan: content engine and verification

> **Status: working plan, not a contract.** Once these are filed as GitHub issues,
> the issues are the source of truth and this document is a record of how the day
> was scoped. Where it disagrees with an issue, the issue is right.
>
> Written against [SEVEN-DAY-PLAN.md](SEVEN-DAY-PLAN.md) Day 2,
> [SESSION-PROMPTS.md](SESSION-PROMPTS.md) sessions C and D, and
> [../LEVEL-FORMAT.md](../LEVEL-FORMAT.md), which is the contract every ticket
> below implements against.

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
this day can be demonstrated until the binary exists. This is ticket **D2-0**, and
it is the first thing that lands.

It also means issue #12, "migrate the dispatcher to cobra", is written against a
dispatcher that is not there. D2-0 replaces it, and #12 should be closed as
superseded rather than left to confuse whoever picks it up.

### 2.2 `SF_STATE` defaults inside the read-only mount

`images/rc/instrument.bash` defaults `SF_STATE` to `/opt/shellforge/state` and then
does `mkdir -p` on it. Non-negotiable 2 in `CLAUDE.md` says `/opt/shellforge` is the
one host mount and it is read-only. So either the snapshot writes fail silently
every prompt, which makes `env_var` and `cwd_is` permanently unsatisfiable, or the
mount is not actually read-only, which is worse.

The fix is that the runtime injects `SF_STATE` at a writable in-sandbox path, and
the default in `instrument.bash` becomes a path that is writable rather than one
that happens to sit under the read-only mount. This is scoped into **D2-7**, which
owns the state directory, and **D2-5** depends on it: a snapshot check cannot pass
against a snapshot that was never written.

---

## 3. Day 1 debt that blocks Day 2

The go/no-go in the seven day plan was explicit: if the PTY multiplexer and the OSC
parser are not working, fix them on Day 2 morning and do not proceed to content.
The parser is done. The multiplexer is not, and neither is any runtime backend.

| Issue | Title | Why it blocks |
|---|---|---|
| [#9](https://github.com/JoottunAtish/ShellForge/issues/9) | `DockerRuntime` via the docker CLI | Every check runs through `Session.Exec`. With no backend, nothing in `internal/verify` can be tested against a real sandbox, and `runtimetest` has still never run green. |
| [#10](https://github.com/JoottunAtish/ShellForge/issues/10) | PTY multiplexer with raw mode, resize, and command events | Without it there is no attached shell, so `run <level>` has nothing to attach and the `check` shim has no host listening on the control FIFO. |
| [#11](https://github.com/JoottunAtish/ShellForge/issues/11) | Demo: one hardcoded level playable end to end | The Day 1 gate. It is also the proof that the control channel round trip works, which D2-13 generalises from one hardcoded level to any level. |

**These three land before any new Day 2 ticket starts.** D2-1 through D2-6 and D2-8
are the exception: they are pure library work with no runtime dependency at compile
time, so they can proceed in parallel with the debt as long as their tests use a
fake `Session`.

---

## 4. Decisions taken while scoping

| Decision | Choice | Why |
|---|---|---|
| YAML parser | `github.com/goccy/go-yaml` | Already named in SESSION-PROMPTS Day 2 session C. Pure Go, YAML 1.2, and it reports line and column on an unmarshal error. The validator is required to name the file, the level id and the field, and a parser without positions cannot do that. |
| SQLite in scope | Yes, Day 2 | Pulled forward from Day 4 task 4.1 by decision. Task 2.6 wants journal events in SQLite, and splitting the journal across two days would mean writing a sink twice. |
| SQLite driver | `modernc.org/sqlite` | Pure Go, no cgo. `mattn/go-sqlite3` is faster but forces a C toolchain and breaks trivial cross-compilation, which is the entire reason the project is in Go. **This is the one dependency choice not yet explicitly approved. Veto it on D2-1 if you disagree.** |
| `command_matched` and the journal ban | Validator rejects a journal check that is neither `optional: true` nor `severity: warn` | `internal/journal/doc.go` states the journal is learner-influenced and must never be evidence for scoring, and LEVEL-FORMAT calls command checks style and bonus only. Nothing enforced that. Now something does. |
| How `verify` reads the journal | Through a `JournalReader` interface that `verify` declares, satisfied by `internal/journal` | Lets `internal/verify` keep its promise of dropping the `internal/journal` import while the two command check types still work. `verify/doc.go` says a test will hold that line, so the test goes in D2-6. |
| Level count | 8, `nav-01` to `files-04` | The exit criterion names exactly this range. `files-05` is the Act II boss and is Day 5 work. |

---

## 5. The ticket set

Fourteen tickets. Each is one branch and one pull request.

```
D2-0  cli      create cmd/shellforge with cobra                          [blocks everything]
D2-1  chore    add goccy/go-yaml and modernc.org/sqlite
   |
   +-- D2-2  content  Pack and Level structs plus YAML loading
   |      |
   |      +-- D2-3  content  the pack validator
   |      +-- D2-7  content  transactional setup and teardown runner
   |
   +-- D2-4  verify   registry plus the eight filesystem check types
   |      |
   |      +-- D2-5  verify   shell state, process, journal and script check types
   |             |
   |             +-- D2-6  verify   composition, the engine, and result assembly
   |
   +-- D2-8  store    SQLite schema and migrations
          |
          +-- D2-9  journal  writer, journal.tsv reader, snapshot readers

D2-3 + D2-0            --> D2-10  cli      author validate and author scaffold
D2-10                  --> D2-11  content  Act I levels, nav-01 to nav-04
D2-10                  --> D2-12  content  Act II levels, files-01 to files-04
D2-6 + D2-7 + D2-9 +   --> D2-13  cli      wire run <level-id> end to end
      #9 + #10 + #11
```

### Sizing, honestly

| Ticket | Estimate |
|---|---|
| D2-0 | 1.0 h |
| D2-1 | 0.5 h |
| D2-2 | 1.5 h |
| D2-3 | 2.0 h |
| D2-4 | 2.0 h |
| D2-5 | 2.0 h |
| D2-6 | 2.0 h |
| D2-7 | 2.5 h |
| D2-8 | 1.5 h |
| D2-9 | 1.5 h |
| D2-10 | 1.5 h |
| D2-11 | 1.5 h |
| D2-12 | 2.0 h |
| D2-13 | 2.0 h |
| **Total** | **23.5 h** |

The seven day plan budgets 12.5 h for Day 2. This is 23.5 h, before the three Day 1
debt tickets. **Day 2 as scoped does not fit in Day 2.** That is worth saying now
rather than discovering it at 22:00.

Two honest options:

1. **Accept the overrun and cut from the ladder.** The cut ladder in `PROGRESS.md`
   already ranks what goes first. Nothing in this list is on it, which is itself a
   signal: this is all engine, and the engine is the thing that must not be cut.
2. **Slip the parts that Day 2's exit criteria do not name.** D2-8 and D2-9, at
   3.0 h, are the only tickets here that no exit criterion depends on. SQLite was
   pulled forward by choice, not by need. Slipping both back to Day 4 brings the day
   to 20.5 h and costs nothing demoable. The `command_matched` checks in D2-5 read
   through the `JournalReader` interface, so they can be tested against a fake and
   wired to the real journal on Day 4 with no change to `verify`.

Recommendation: option 2 if the day is running long by lunchtime, and make the call
by then rather than at midnight.

---

## 6. The tickets

Each section below is the body of one issue, in the field order of
`.github/ISSUE_TEMPLATE/04-implementation-ticket.yml`. Sections are compressed
where a full paragraph adds nothing; the self-containment test in the
`ticket-creation` skill still has to pass on every one before it is filed.

---

### D2-0 cli: create cmd/shellforge with cobra

**Labels:** `type: feature`, `area: cli` · **Branch:** `feature/issue-<n>` ·
**Supersedes:** #12

**Summary.** `make build` produces `bin/shellforge`, and every verb in the brief is
registered and reachable.

**Context.** `cmd/shellforge` does not exist. `Makefile` points `PKG` at it and CI
never noticed because `go build ./...` is green on a module with no main package.
Three of the four Day 2 exit criteria are CLI invocations, so nothing this day can
be demonstrated until this lands. Issue #12 assumes a hand-rolled dispatcher to
migrate away from; there is none, so this ticket goes straight to cobra and #12 is
closed as superseded.

**Layer and files.** L5. New: `cmd/shellforge/main.go`, `root.go`, `version.go`,
`commands_test.go`. Modified: `.github/workflows/ci.yml`. May import anything.
Nothing may import it.

**Interfaces.**

```go
// main.go
func main()  // calls root.Execute, maps error to exit status via ux.Render

// root.go
func NewRootCommand(v VersionInfo) *cobra.Command

type VersionInfo struct{ Version, Commit, BuildDate string }
```

Verbs, matching the brief: `doctor`, `init`, `run`, `play`, `check`, `hint`,
`reset`, `map`, `stats`, `author`, `sandbox`, `version`. `author` and `sandbox`
carry subcommands (`author validate|scaffold|test|record`, `sandbox
build|rebuild|destroy|status`) and are registered here as groups with their
subcommands stubbed.

**Acceptance criteria.**

- [ ] `make build` and `.\make.ps1 build` both produce a working binary
- [ ] `shellforge version` prints version, commit and build date from the ldflags already in `Makefile`
- [ ] `shellforge help` lists all twelve verbs
- [ ] Every unimplemented verb exits non-zero through `ux.Fail` with a remediation line, never a bare cobra error or a panic
- [ ] A table test asserts the registered verb set equals the documented set exactly, so a verb cannot be dropped or added silently
- [ ] CI builds `./cmd/...` explicitly, so a missing main package fails the build rather than passing it
- [ ] `gofmt`, `go vet`, the layer test, and the punctuation gate all pass

**Approach.** Cobra is on the approved list in `go.mod`'s comment already. Keep
`main.go` to about ten lines: construct, execute, translate error to exit status.
All flag and verb wiring lives in `root.go` so the test can construct the tree
without running it. Do not add `cobra-cli` scaffolding or a `docs` generator.

**Out of scope.** Any verb's actual behaviour. Shell completion. Config file
loading. The `--json` flag, which belongs to the doctor ticket on Day 3.

**Safety review.** No. This registers commands and implements none of them. No
deletion, no path resolution, no elevation.

**Test plan.** Verb table equality test. A test asserting each stub exits non-zero
and that its stderr contains a remediation. A test that `NewRootCommand` builds
with empty `VersionInfo` without panicking. No golden output tests on help text:
cobra changes its formatting between minor versions and the test would be noise.

**Docs to update.** `PROGRESS.md`, including correcting the stale CLI row.
`docs/03-quickstart.md` if any verb name changes.

**Skills.** `implementation`, `go-style`, `testing`, `writing-style`

**References.** SEVEN-DAY-PLAN task 1.1. ARCHITECTURE section 4.1. Issue #12.

---

### D2-1 chore: add goccy/go-yaml and modernc.org/sqlite

**Labels:** `type: chore`, `dependencies`, `area: content-engine`

**Summary.** The module has its first two dependencies, pinned, reviewed and
documented, and a layer test that stops either one leaking upward.

**Context.** `go.mod` currently carries a comment saying there are no dependencies
and that is deliberate, plus a note to ask before adding anything off the approved
list. This is the ask, and it was approved: `goccy/go-yaml` for the level format and
`modernc.org/sqlite` for the journal and progress store. Doing both in one ticket
means one `go.sum` review rather than two.

**Layer and files.** No layer. Modified: `go.mod`, `go.sum`, `CLAUDE.md`,
`internal/archtest/layers_test.go`.

**Acceptance criteria.**

- [ ] Both modules are required at an exact version, and `go mod tidy` leaves `go.mod` and `go.sum` unchanged
- [ ] The stale "no dependencies yet" comment in `go.mod` is replaced by the approved set with a one-line reason for each
- [ ] `CLAUDE.md` lists the approved dependency set, and it agrees with `go.mod`
- [ ] `internal/archtest` fails if `goccy/go-yaml` is imported anywhere but `internal/content`, or if the sqlite driver is imported anywhere but `internal/store`
- [ ] `govulncheck` and `gosec` are green
- [ ] `go build ./...` needs no network, given a populated module cache

**Approach.** `modernc.org/sqlite` is pure Go, so cross-compilation and the single
static binary promise hold. `mattn/go-sqlite3` is faster and would force cgo and a C
toolchain on every build host including Windows, which is the persona this project
exists for. The archtest additions are the real content of this ticket: a dependency
that stays where it was put is a dependency that can be replaced later, and both of
these are plausible replacements within a year.

**Out of scope.** Using either library. D2-2 uses the YAML parser and D2-8 uses the
driver. Vendoring. A `tools.go`.

**Safety review.** Yes, supply chain. `SECURITY.md` treats third party code as a
supply chain surface. Required: exact version pins with no `+incompatible`, the
`go.sum` diff reviewed line by line in the pull request rather than waved through,
and `govulncheck` green before merge. Dependabot already covers `gomod`. No
destructive path, so no refusal tests.

**Test plan.** Two new archtest table entries with a deliberate violation verified
locally before committing, the same way the layer rule itself was verified on Day 0.
A test asserting `go.mod`'s approved list and `CLAUDE.md`'s agree, so the two cannot
drift the way the allowlist regexp did.

**Docs to update.** `CLAUDE.md`, `PROGRESS.md`.

**Skills.** `security`, `go-style`, `implementation`

**References.** SESSION-PROMPTS Day 2 session C item 1. `SECURITY.md`. `go.mod`
header comment. The Go supply chain articles linked from `CLAUDE.md`.

---

### D2-2 content: Pack and Level structs plus YAML loading

**Labels:** `type: feature`, `area: content-engine` · **Blocked by:** D2-1

**Summary.** A pack on disk or embedded in the binary becomes typed Go values, with
every field in LEVEL-FORMAT section 2 represented and unknown fields rejected.

**Context.** Levels are data. This is the ticket that makes that true. Everything
downstream on Day 2 consumes these types, so getting the shape right matters more
than getting it quickly. Do not invent fields: LEVEL-FORMAT section 2 is the
contract, and a field that is not there does not go in.

**Layer and files.** L3, `internal/content`. New: `pack.go`, `level.go`, `check.go`,
`load.go`, `embed.go`, and a test file per source file, plus
`internal/content/testdata/`. May import L2 and below plus `goccy/go-yaml`. Must not
import `internal/game` or any runtime implementation.

**Interfaces.**

```go
type Pack struct {
    ID, Name, Version               string
    ContentAPI                      int
    Author, License, Homepage       string
    Description, Intro              string
    Acts                            []Act
    Ranks                           []Rank
    Levels                          []Level // filled by the loader, absent from pack.yaml
}

type Act struct{ ID, Title, Subtitle string; Levels []string }
type Rank struct{ ID, Title string; MinXP int }

type Level struct {
    ID, Title, Act    string
    Version           int
    Difficulty, XP    int
    ParCommands       int      // 0 means the efficiency bonus is off
    EstimatedMinutes  int
    Concepts          []string
    Prerequisites     []string
    Requires          []string
    Boss              bool
    Briefing          string
    Objectives        []Objective
    Setup             Setup
    Checks            []CheckSpec
    Hints             []Hint
    Solution          string
    Teardown          Teardown
    Tags              []string
    SourceFile        string   // set by the loader, used in every validation error
}

// CheckSpec is deliberately loose. internal/verify owns the per-type parameter
// shapes; content only knows the fields common to every check, plus the
// composition nodes.
type CheckSpec struct {
    ID, Type, OnFail string
    Optional         bool
    Severity         string   // "" means fail
    TimeoutSeconds   int
    AnyOf, AllOf     []CheckSpec
    Not              *CheckSpec
    Params           map[string]any // every key not listed above, verbatim
    Line             int            // for error messages
}

func LoadPack(fsys fs.FS, root string) (*Pack, error)
func Embedded() (*Pack, error)          // embed.FS over packs/core-linux-basics
func (p *Pack) Level(id string) (*Level, bool)
func (p *Pack) Order() ([]string, error) // acts in order, levels topologically within
```

**Acceptance criteria.**

- [ ] Every field in LEVEL-FORMAT section 2 round trips, and the worked example in section 6 loads without error
- [ ] An unknown top-level or nested field is a rejection naming the file, the level id and the field, not a silent drop
- [ ] `Embedded()` returns the real `packs/core-linux-basics` pack compiled into the binary, with no filesystem access at runtime
- [ ] A malformed YAML file produces an error carrying the file name and the line number
- [ ] `CheckSpec.Params` captures every type-specific key verbatim, and a check with `any_of` nests to at least three levels without loss
- [ ] `Order()` returns acts in `pack.yaml` order and errors on a prerequisite cycle rather than looping
- [ ] Loading a pack with zero level files succeeds, since that is the state of the repository today

**Approach.** Decode with `yaml.DisallowUnknownField` so a typo in a level is a
rejection rather than a default. Capture `Params` with a two pass decode: once into
the typed struct, once into a `map[string]any`, then delete the known keys. That is
uglier than a custom unmarshaler and much easier to keep correct as fields are
added.

`SourceFile` and `Line` exist only so that validation errors can name a place. They
are populated by the loader and are not YAML fields.

Deliberately not done here: no validation beyond what the parser itself refuses.
Every authoring invariant belongs to D2-3, so that "the file parsed" and "the level
is legal" stay separable and separately testable.

**Out of scope.** Validation. Setup execution. Check construction. `.tar.zst` packs.
Remote packs, which are banned outright in `CLAUDE.md`.

**Safety review.** Partly. Pack-supplied paths are untrusted input per
`internal/content/doc.go`, but this ticket only reads them into strings and resolves
nothing. Path containment for `source:` and `path:` is enforced in D2-3 and D2-7,
where the paths are actually resolved. Nothing here deletes, mounts, or elevates.
Noted here explicitly so the boundary is not assumed to be someone else's problem.

**Test plan.** Golden test: LEVEL-FORMAT section 6 verbatim as a fixture, asserting
every field. Table test over malformed inputs, each asserting the error names the
file and the field: unknown field, wrong type for `difficulty`, `xp` as a string,
a duplicate YAML key, a truncated file, a `checks` entry that is a scalar. A test
that a level with all optional fields omitted loads with correct zero values. A
test that `Embedded()` works from a binary with no working directory. Fuzz the
loader over the pack directory as a corpus if it costs under an hour, since a panic
in a parser reachable from a community pack is a real risk.

**Docs to update.** `PROGRESS.md`. `docs/LEVEL-FORMAT.md` only if a discrepancy is
found, in which case fix the document in the same commit, since it is a living
contract.

**Skills.** `implementation`, `go-style`, `level-authoring`, `testing`, `security`

**References.** `docs/LEVEL-FORMAT.md` sections 1, 2 and 6. SESSION-PROMPTS Day 2
session C item 1. ARCHITECTURE section 4.8. `internal/content/doc.go`.

---

### D2-3 content: the pack validator

**Labels:** `type: feature`, `area: content-engine` · **Blocked by:** D2-2, D2-4

**Summary.** Every authoring invariant in LEVEL-FORMAT is enforced, and each
violation is reported with the file, the level id and the field.

**Context.** This is the ticket the second exit criterion names by hand: duplicate
id, cycle, missing asset, missing `on_fail`, unknown check type. It is also the only
thing standing between an author and a level that fails at 03:00 in a golden test
run rather than at authoring time.

**Layer and files.** L3, `internal/content`. New: `validate.go`, `validate_test.go`,
`internal/content/testdata/invalid/` with one deliberately broken pack per rule.

**Interfaces.**

```go
type Problem struct {
    File    string // pack-relative, always set
    LevelID string // "" for pack-level problems
    Field   string // dotted path, e.g. "checks[2].on_fail"
    Message string
    Line    int    // 0 when unknown
}

func (p Problem) Error() string // "levels/01-nav-01.yaml: nav-01: checks[2].on_fail: must not be empty"

type Report struct{ Problems []Problem }

func (r Report) OK() bool

// Validate needs the check registry to answer "is this type known" and "are these
// parameters well formed", so it takes a lookup rather than importing verify's
// concrete registry. That keeps content testable with a fake registry.
type TypeChecker interface {
    Known(typeName string) bool
    ValidateParams(typeName string, params map[string]any) error
}

func Validate(p *Pack, tc TypeChecker) Report
```

**Acceptance criteria.**

Each of these is a rule with a fixture in `testdata/invalid/`:

- [ ] Duplicate level id across the pack, and a level id not matching `[a-z0-9-]+`
- [ ] A prerequisite cycle, and a prerequisite naming a level that does not exist
- [ ] A `source:` naming an asset that is not in the pack, and a `source:` resolving outside the pack directory after symlink resolution
- [ ] An empty or missing `on_fail` on any check, at any composition depth
- [ ] An unknown check type, and a known type with malformed parameters
- [ ] Objective ids and check ids that do not correspond one to one, in either direction
- [ ] Empty `solution`, fewer than two hints, zero non-optional checks, `setup.root` not under `/home/learner/`
- [ ] A `command_matched` or `command_not_matched` check that is neither `optional: true` nor `severity: warn`
- [ ] An act in `pack.yaml` listing a level id with no file, reported as a warning rather than an error, since that is the repository's normal state while the pack is being filled in
- [ ] `Validate` reports every problem it finds, not just the first

**Approach.** Report everything. An author fixing one error per run is an author who
stops using the validator. `Problem` is a value, not a wrapped error chain, because
the output is a list for a human, not something a caller branches on.

The `command_matched` rule is new and is not currently written in LEVEL-FORMAT.
`internal/journal/doc.go` and `internal/verify/doc.go` both state that the journal is
learner-influenced and never evidence for scoring, and LEVEL-FORMAT already says
command checks are for style and bonus objectives only, but nothing enforced it. A
level could quietly gate passing on a forgeable signal. **LEVEL-FORMAT must be
updated in the same commit** to state the rule, since it is a living contract.

The `source:` containment check resolves symlinks before comparing prefixes.
Comparing strings before resolution is the classic way to let `assets/../../etc` or
a symlinked asset directory through.

**Out of scope.** Running any check. Materialising any file. Validating that the
solution actually solves the level, which is the golden test and belongs to `author
test`. Style opinions about briefing prose.

**Safety review.** Yes, path resolution. `destructive-safety` applies to the
`source:` and `setup.root` rules. Required refusal tests: a `source:` of
`../../../etc/passwd`; a `source:` that is a symlink pointing outside the pack; a
`setup.root` of `/home/learner/../../etc`; a `setup.root` of `/` and of
`/home/learner` exactly, which is the learner's whole home and must be refused
because teardown does `rm -rf` on it. That last one is the case that would be
catastrophic and the one most likely to be missed, since it satisfies a naive
"under /home/learner/" prefix test.

**Test plan.** One fixture pack per rule under `testdata/invalid/`, each asserting
the exact `Problem` fields and not just that an error occurred. A valid fixture pack
asserting zero problems, which is the test that catches an over-eager rule. A test
that `Validate` on the real `packs/core-linux-basics` returns only the
missing-level-file warnings. Refusal tests as above, each with its own name so a
failure says which containment case broke.

**Docs to update.** `docs/LEVEL-FORMAT.md`, adding the journal check rule to the
authoring invariants table. `docs/07-authoring-levels.md`. `PROGRESS.md`.

**Skills.** `level-authoring`, `destructive-safety`, `security`, `testing`, `go-style`

**References.** `docs/LEVEL-FORMAT.md` section 2 authoring invariants.
SESSION-PROMPTS Day 2 session C item 2. `internal/journal/doc.go`.

---

### D2-4 verify: registry plus the eight filesystem check types

**Labels:** `type: feature`, `area: verify` · **Blocked by:** D2-1

**Summary.** A check registry exists, and the eight filesystem check types from
LEVEL-FORMAT section 3 are registered, each reading real sandbox state through
`Session.Exec`.

**Context.** Checks are the product. Everything else on Day 2 exists so that these
can run. Splitting the thirteen types across D2-4 and D2-5 keeps each ticket to one
sitting; the split is by what they read, not by file.

**Layer and files.** L3, `internal/verify`. New: `registry.go`, `check.go`,
`fs_checks.go`, and tests per file, plus `internal/verify/verifytest/` holding a
fake `Session`. May import `internal/runtime` and below. Must not import
`internal/content` or `internal/journal`.

**Interfaces.**

```go
type Status string
const (StatusPass Status = "pass"; StatusFail Status = "fail"; StatusWarn Status = "warn"; StatusTimeout Status = "timeout"; StatusError Status = "error")

type Result struct {
    ID       string
    Status   Status
    Message  string // on_fail for a failure, plus script stdout where applicable
    Duration time.Duration
}

// Env is everything a check may read. A check that needs something not on Env is
// a check that is reaching somewhere it should not.
type Env struct {
    Session   runtime.Session
    Root      string     // the level's setup.root
    Snapshots Snapshots  // shell-local state, see D2-5
    Journal   JournalReader
}

type Check interface {
    ID() string
    Run(ctx context.Context, env Env) Result
}

type Spec struct {
    ID, Type, OnFail string
    Optional         bool
    Severity         string
    TimeoutSeconds   int
    Params           map[string]any
}

type Factory func(s Spec) (Check, error) // errors on a malformed or unknown parameter

func Register(typeName string, f Factory) // panics on a duplicate, called from init
func Lookup(typeName string) (Factory, bool)
func Types() []string                      // sorted, for the validator and for tests
```

Types in this ticket: `file_exists`, `file_absent`, `file_content`, `dir_exists`,
`dir_tree`, `file_mode`, `file_owner`, `symlink_target`.

**Acceptance criteria.**

- [ ] All eight types are registered, and `Types()` returns exactly them
- [ ] `file_content` implements all six match modes: `exact`, `trimmed_equals`, `contains`, `regex`, `sha256`, `line_count`, plus `ignore_case`
- [ ] `dir_tree` implements `names_only` and `names_and_hashes` and compares against another path in the sandbox
- [ ] `file_mode` accepts both an exact `mode` and an `any_of` list
- [ ] A malformed parameter is a construction error, not a runtime failure, so an unknown match mode is caught by the validator at pack load
- [ ] A missing file makes a check fail cleanly with the authored `on_fail`, never an error status
- [ ] Every check runs as `learner` by default, in a non-interactive shell, and none writes anything
- [ ] Registering a duplicate type name panics, and the panic is covered by a test

**Approach.** Each check builds a single `argv` and runs it through `Session.Exec`.
Never build a command string. `Exec` takes `argv []string` and the package has no
string form, deliberately, and a check that concatenates a learner-influenced path
into a shell string is exactly the injection this design was set up to prevent.

For `file_content` with `sha256` and for `dir_tree` with `names_and_hashes`, hash
in the sandbox with `sha256sum` rather than pulling the file to the host. A level
asset can be four thousand lines and pulling it per check is slow and pointless.

`file_absent` must distinguish "does not exist", which passes, from "exists but
cannot be stated", which is an error status, not a pass. A permissions level will
produce the second case and a naive implementation reports the learner solved it.

**Out of scope.** Shell state, process, journal and script types, which are D2-5.
Composition and the engine, which are D2-6. The purity test, which is D2-6 because
it needs the engine to run a full pass.

**Safety review.** No deletion and no elevation, and every check is read-only by
construction. The relevant rule is the read-only one rather than a destructive one:
a check that mutates is a bug that CI catches by hashing the filesystem across a
double run, and that test lands in D2-6. Path handling is in scope for `security`:
paths come from a pack and are untrusted, and they go into an `argv` element and
never into a shell string.

**Test plan.** Table test per type against the fake `Session` in `verifytest`,
covering pass, fail, missing target, and permission denied. A test asserting no
check's `argv` contains a shell metacharacter that was not in its input. A test that
every registered type has at least one table test, driven off `Types()`, so a new
type cannot arrive untested. Contract tests against a real Docker sandbox are
deferred to D2-13, when a backend exists.

**Docs to update.** `PROGRESS.md`. `docs/LEVEL-FORMAT.md` if any parameter shape
differs from what is written there, fixed in the same commit.

**Skills.** `implementation`, `go-style`, `security`, `testing`, `component-traps`

**References.** `docs/LEVEL-FORMAT.md` section 3 filesystem, and section 4.
ARCHITECTURE section 4.10. SESSION-PROMPTS Day 2 session D items 1 and 2.
`internal/verify/doc.go`.

---

### D2-5 verify: shell state, process, journal and script check types

**Labels:** `type: feature`, `area: verify` · **Blocked by:** D2-4, D2-7

**Summary.** The remaining six check types work, and `env_var` and `cwd_is` read the
`PROMPT_COMMAND` snapshots rather than the check shell's own environment.

**Context.** The snapshot reading is the part that is easy to get wrong and
expensive to discover. `internal/verify/doc.go` states it plainly: get it wrong and
level `env-01` is unsolvable, because a check runs in a separate `exec` and cannot
see an `export` typed in the learner's shell. Blocked by D2-7 because the snapshot
files have to exist and be writable first, see finding 2.2.

**Layer and files.** L3, `internal/verify`. New: `shell_checks.go`,
`process_checks.go`, `journal_checks.go`, `script_check.go`, `snapshot.go`, plus
tests. Still must not import `internal/journal`.

**Interfaces.**

```go
// Snapshots reads the files instrument.bash writes each prompt, under $SF_STATE.
type Snapshots struct{ Dir string } // env.snapshot, alias.snapshot, func.snapshot, shopt.snapshot

func (s Snapshots) Env(ctx context.Context, sess runtime.Session) (map[string]string, error) // env -0, NUL separated
func (s Snapshots) Cwd(ctx context.Context, sess runtime.Session) (string, error)

// JournalReader is declared here, not imported. internal/journal satisfies it.
// This is what lets internal/verify keep its promise never to import the journal.
type JournalReader interface {
    Commands(scope Scope) []string
}

type Scope struct{ Kind ScopeKind; N int } // level | last_n:N | last
```

Types in this ticket: `env_var`, `cwd_is`, `process_running` with `negate`,
`command_matched`, `command_not_matched`, `script` with `as_user`.

**Acceptance criteria.**

- [ ] `env_var` reads `env.snapshot` and passes for a variable exported in the learner's interactive shell and never exported in the check's shell
- [ ] `env_var` with no `value` asserts only that the variable is set, including when set to the empty string
- [ ] `cwd_is` reads the snapshot, not `pwd` in the check shell
- [ ] A missing or stale snapshot file is a distinct error status with a message saying the shell instrumentation did not run, not a silent fail that reads to the learner as their mistake
- [ ] `process_running` matches against `ps -eo pid,args` and honours `negate`
- [ ] `command_matched` and `command_not_matched` read through `JournalReader` and support `scope: level`, `last_n:N` and `last`
- [ ] `script` runs as `learner` by default and `root` on request, treats exit 0 as pass, and appends the failing script's stdout to the authored `on_fail`
- [ ] `internal/verify` imports nothing from `internal/journal`, asserted by a test in the package

**Approach.** `env -0` is NUL separated and a value can contain a newline, so split
on NUL and only on NUL. Splitting on newline is the bug that will look correct
against every test fixture written by hand and break on the first multi-line value.

The snapshot files are written by `PROMPT_COMMAND` after each command. That means a
check run before the learner has pressed Enter even once sees no snapshot at all.
That is the missing-snapshot case above and it is a normal state, not an error in
the learner's work, so its message must say so.

`script` is the escape hatch and takes arbitrary bash from the pack. It runs inside
the sandbox where arbitrary bash is the point, so there is no injection boundary to
defend inside the sandbox. There is one on the way in: the script body goes to
`Exec` as an argv element passed to `bash -c`, and `as_user` is validated against the
argv allowlist regexp before it can reach a `-u` flag.

**Out of scope.** `alias_defined`, `function_defined`, `shopt_set` and
`last_exit_code`. All four are deferred to v0.2 by LEVEL-FORMAT section 3, even
though `instrument.bash` already writes the snapshots they would need. Do not
implement them because the data happens to be there.

**Safety review.** Yes, for `as_user: root`. A pack-supplied `as_user` reaching a
command line is untrusted input, and `root` inside the sandbox is still root. Rules
from `security` apply: validate `as_user` against `^[a-zA-Z0-9][a-zA-Z0-9_-]*$`
before use, so a value cannot start with a hyphen and be read as a flag. Required
refusal tests: `as_user: --privileged`, `as_user: -u root`, `as_user: root learner`,
and an empty `as_user`. No host-side deletion or path resolution.

**Test plan.** Table tests per type against the fake `Session`. A snapshot fixture
containing a value with an embedded newline and one with an `=` in the value. A test
for the empty-snapshot case asserting the distinct status and the message. The four
refusal tests above. A source-parsing test asserting no file in `internal/verify`
imports `internal/journal`, in the style of the existing import test in
`internal/runtime/runtimetest`.

**Docs to update.** `PROGRESS.md`. `docs/05-troubleshooting.md` needs a heading for
the missing-snapshot doc anchor, since CI fails the build without one.

**Skills.** `implementation`, `security`, `component-traps`, `testing`, `go-style`

**References.** `docs/LEVEL-FORMAT.md` section 3 shell state, process, journal, and
escape hatch, and section 4 item 3. ARCHITECTURE section 4.7.
`images/rc/instrument.bash`. `internal/verify/doc.go`. `internal/journal/doc.go`.

---

### D2-6 verify: composition, the engine, and result assembly

**Labels:** `type: feature`, `area: verify` · **Blocked by:** D2-5

**Summary.** `any_of`, `all_of` and `not` compose, a level's checks run to
completion under timeouts, and the result matches LEVEL-FORMAT section 5 exactly.

**Context.** Two of the four Day 2 exit criteria live here: the authored `on_fail`
must be what the learner sees, and only the first failing required objective may be
shown. This is also where the purity test goes, since it needs a full engine pass to
hash across.

**Layer and files.** L3, `internal/verify`. New: `compose.go`, `engine.go`,
`result.go`, `purity_test.go`, plus tests.

**Interfaces.**

```go
type Engine struct{ /* registry, timeouts */ }

func NewEngine(opts ...Option) *Engine
func WithCheckTimeout(d time.Duration) Option  // default 10s
func WithLevelTimeout(d time.Duration) Option  // default 60s

// Build turns validated specs into runnable checks once, at level load.
func (e *Engine) Build(specs []Spec) ([]Check, error)

func (e *Engine) Run(ctx context.Context, checks []Check, env Env) LevelResult

type LevelResult struct {
    LevelID        string             `json:"level_id"`
    Passed         bool               `json:"passed"`
    Objectives     []ObjectiveResult  `json:"objectives"`
    PrimaryFailure *ObjectiveResult   `json:"primary_failure,omitempty"`
    Notes          []string           `json:"notes"`
    DurationMS     int64              `json:"duration_ms"`
}
```

`Score` in LEVEL-FORMAT section 5 is populated by `internal/game` on Day 4, not here.
The field exists in the JSON shape and this package leaves it out; the game layer
adds it. Note this in the doc comment so the omission reads as a decision.

**Acceptance criteria.**

- [ ] `any_of`, `all_of` and `not` nest arbitrarily and each composite reports its own authored `on_fail`, not a child's
- [ ] Checks run in declaration order and none short-circuits: a failure in check 1 still runs checks 2 through N, so the checklist is fully populated
- [ ] `PrimaryFailure` is the first failing required check only, and `optional` and `severity: warn` results appear in `Notes`
- [ ] `Passed` is true only when every non-optional check passed
- [ ] A check exceeding 10s is a timeout status with a message distinct from a failure, and the level as a whole is capped at 60s
- [ ] Cancelling the context stops the run promptly and reports cleanly, since the learner can Ctrl-C a `check`
- [ ] The JSON marshalling of `LevelResult` matches LEVEL-FORMAT section 5 field for field
- [ ] **Purity:** hashing the sandbox filesystem before and after a full check run over the `pipe-05` fixture gives an identical hash

**Approach.** Build once at load, run many times. A learner types `check` repeatedly
and re-parsing parameters each time is wasted work and a place for a parse error to
surface at the worst moment.

The purity test is the one with teeth. Hash with `find $ROOT -type f -exec sha256sum
{} +` sorted, plus a stat pass covering mode, owner and size, since a check that
chmods without changing content would otherwise pass a content-only hash. It needs a
real sandbox, so it is build-tagged and runs in CI's image job, not in the unit test
run, the same shape as the runtime contract suite.

Implement `pipe-05` from LEVEL-FORMAT section 6 as a real level file here and use it
as the reference fixture, exactly as SESSION-PROMPTS session D item 6 asks. It is not
one of the eight levels the exit criterion names; it is the engine's test fixture and
it also gets Act III started early at no cost.

**Out of scope.** Scoring, XP, hint penalties, all Day 4. Live polling mode, which
ARCHITECTURE section 4.10 lists as optional and which is not in the day plan.
Rendering the result to a terminal, which is D2-13.

**Safety review.** No deletion, no elevation, no path resolution beyond what D2-4
and D2-5 already do. The purity test does read the whole level root, and it must
read only under `setup.root` and never above it, or a bug in the test could hash the
learner's home.

**Test plan.** Composition table tests including a `not` wrapping an `any_of`
wrapping an `all_of`. Ordering test asserting all checks ran when the first failed.
A test asserting exactly one `PrimaryFailure` when three required checks fail. A
test that a warn-severity failure does not set `Passed` to false. A timeout test
with a check that sleeps. A context cancellation test. A JSON golden test against
LEVEL-FORMAT section 5. The purity test, build-tagged.

**Docs to update.** `PROGRESS.md`. `docs/LEVEL-FORMAT.md` section 5 if the shape
drifts, in the same commit.

**Skills.** `implementation`, `testing`, `go-style`, `component-traps`

**References.** `docs/LEVEL-FORMAT.md` sections 3 composition, 4, 5 and 6.
SESSION-PROMPTS Day 2 session D items 3 to 6. ARCHITECTURE section 4.10.

---

### D2-7 content: transactional setup and teardown runner

**Labels:** `type: feature`, `area: content-engine` · **Blocked by:** D2-2, #9

**Summary.** A level's `setup` block materialises into the sandbox as a
transaction, teardown always runs first, and re-running setup twice leaves the same
state.

**Context.** ARCHITECTURE section 4.9 specifies setup as a transaction with a
`SETUP_OK` sentinel and idempotency by construction. This is also the ticket that
owns the state directory, and therefore the ticket that fixes finding 2.2, because
`SF_STATE` currently defaults inside the read-only mount and the snapshot writes
that D2-5 depends on cannot succeed as things stand.

**Layer and files.** L3, `internal/content`. New: `setup/runner.go`,
`setup/generate.go`, `setup/state.go`, plus tests. Modified:
`images/rc/instrument.bash` for the `SF_STATE` default.

**Interfaces.**

```go
type Runner struct{ /* session, pack fs, state dir */ }

func NewRunner(sess runtime.Session, packFS fs.FS) *Runner

// Setup always tears down first. It is safe to call on a dirty sandbox and safe
// to call twice.
func (r *Runner) Setup(ctx context.Context, lvl *content.Level) error
func (r *Runner) Teardown(ctx context.Context, lvl *content.Level) error
func (r *Runner) IsSetUp(ctx context.Context, lvl *content.Level) (bool, error) // SETUP_OK sentinel

// Generators are seeded and offline. kind: loglines is the only one v0.1 needs.
type Generator func(seed int64, lines int) ([]byte, error)
func RegisterGenerator(kind string, g Generator)
```

**Acceptance criteria.**

- [ ] Teardown runs before every setup, so setup on a dirty level root produces the same result as setup on a clean one
- [ ] Files materialise with `\r` stripped from every text file, and `mode` and `owner` applied
- [ ] `generate:` with `kind: loglines` and a fixed seed produces byte-identical output on every run and on both platforms
- [ ] `setup.script` runs as root, inside the sandbox, under `timeout_seconds`, defaulting to 30
- [ ] A failure at any step rolls back, leaves no `SETUP_OK`, and reports through `ux.Fail` with a remediation
- [ ] `SETUP_OK` is written only after every step succeeded, and `IsSetUp` reads it
- [ ] `SF_STATE` resolves to a writable in-sandbox path, and `instrument.bash` no longer defaults it under the read-only `/opt/shellforge` mount
- [ ] No network access is possible during setup, asserted by a test

**Approach.** Stage on the host, push once. Materialise into a host staging
directory, strip `\r`, then push the whole tree with a single `PushFiles`. Per-file
pushes are slow on WSL and turn a rollback into a partial state.

Order matters and is specified: files, then `script`, then the sentinel. The script
runs after files because the worked example in LEVEL-FORMAT uses it to `chown -R`
what the files step just wrote.

For the `SF_STATE` fix, the state directory goes to a writable path inside the
sandbox owned by `learner`, and the runtime injects the value. `/opt/shellforge`
stays read-only. Do not solve this by making the mount writable.

**Out of scope.** `reset --hard` and `sandbox rebuild`, which are Day 3 and Day 6.
Snapshots and the rewind mechanic, listed as optional in ARCHITECTURE 4.9 and cut
from v0.1. Generators beyond `loglines`.

**Safety review.** **Yes, and this is the most dangerous ticket of the day.**
Teardown is `rm -rf` on a path that came out of a YAML file written by a pack
author. `destructive-safety` applies in full.

Rules: resolve `setup.root` to an absolute real path and refuse anything that is not
strictly under `/home/learner/`; refuse `/home/learner` itself, which passes a naive
prefix test and is the learner's entire home; refuse a path containing a symlink
component, resolved before the check, not after; the deletion runs inside the
sandbox through `Session.Exec` and never on the host, and a test asserts no host
filesystem removal call exists anywhere in the package.

Required refusal tests: `setup.root` of `/`, of `/home`, of `/home/learner`, of
`/home/learner/../..`, of `/home/learner/quest/../../../etc`, of a symlink under
`/home/learner` pointing at `/`, and of a relative path. Each named individually so
a failure says which containment case broke.

**Test plan.** The refusal tests above. Idempotency test: setup, mutate the root,
setup again, assert identical state. Rollback test: a `setup.script` that exits
non-zero, asserting no `SETUP_OK` and a clean error. A `\r` test with a `.sh` asset
containing CRLF, asserting the materialised file runs rather than producing `bad
interpreter: /bin/bash^M`. A generator determinism test running the same seed twice
and comparing hashes. A test asserting the package makes no network call, in the
shape archtest already uses.

**Docs to update.** `PROGRESS.md`. `docs/LEVEL-FORMAT.md` if `SF_STATE`'s location
becomes author-visible. `docs/05-troubleshooting.md` for every new `DocAnchor`, which
CI enforces.

**Skills.** `destructive-safety`, `security`, `implementation`, `testing`,
`level-authoring`, `component-traps`

**References.** ARCHITECTURE section 4.9. `docs/LEVEL-FORMAT.md` section 2 setup.
SESSION-PROMPTS Day 2 session C item 3. `images/rc/instrument.bash`. `CLAUDE.md`
non-negotiables 2, 5, 7 and 8.

---

### D2-8 store: SQLite schema and migrations

**Labels:** `type: feature`, `area: store` · **Blocked by:** D2-1

**Summary.** A versioned SQLite database opens at `DatabasePath()`, in WAL mode,
with the schema from ARCHITECTURE section 4.11 and a forward migration runner.

**Context.** Pulled forward from Day 4 task 4.1 so that the journal has somewhere to
write on Day 2. It is the first ticket to use `internal/platform.DatabasePath`, which
Day 1 already fixed so it cannot land inside the cache directory on Windows.

**Layer and files.** L0, `internal/store`. New: `store.go`, `migrate.go`,
`schema/001_init.sql`, plus tests. May import `internal/platform` and the standard
library plus the driver. Must import nothing else in the module.

**Interfaces.**

```go
func Open(path string) (*Store, error) // creates parent dirs, applies migrations, sets WAL
func (s *Store) Close() error
func (s *Store) Migrate(ctx context.Context) error
func (s *Store) SchemaVersion(ctx context.Context) (int, error)
```

**Acceptance criteria.**

- [ ] `Open` on a nonexistent path creates the database and applies every migration
- [ ] `Open` on a current database is a no-op and does not rewrite the schema
- [ ] Migrations apply in order, are recorded, and never re-run
- [ ] WAL mode and a busy timeout are set, so two processes do not deadlock
- [ ] A database from a newer schema version is refused with a clear message rather than corrupted
- [ ] The `events` table matches the journal record in ARCHITECTURE section 4.7
- [ ] `Open` failures go through `ux.Fail` with a remediation and a doc anchor
- [ ] The driver is imported only in this package, asserted by archtest from D2-1

**Approach.** Embed the migration SQL with `embed.FS` and apply in filename order.
No migration framework: a dependency for something that is forty lines is not worth
the supply chain surface. Down migrations are not implemented; the recovery path for
a bad schema is to delete the database, which is acceptable because the only thing
lost is game progress and the alternative is untested rollback code.

**Out of scope.** Every query above the schema. Scoring and progression tables
beyond what section 4.11 specifies. The journal writer, which is D2-9.

**Safety review.** Yes, path resolution. `DatabasePath()` is the learner's progress
and must never be created under a directory that a cache clear would delete, which
is precisely issue #40 and already fixed in `paths.go`. Required: a test asserting
`DatabasePath()` is not under `CacheDir()`, mirroring the one Day 1 added, so a
future change to either function fails here too. No deletion in this package: a test
asserts it contains no removal call.

**Test plan.** Fresh open, reopen, migrate-twice idempotency, a newer-version
refusal, a corrupt-file error path, and a concurrent-open test under `-race`. All
against `t.TempDir`, never a real user path.

**Docs to update.** `PROGRESS.md`. `docs/05-troubleshooting.md` for the new doc
anchors.

**Skills.** `implementation`, `go-style`, `security`, `testing`, `destructive-safety`

**References.** ARCHITECTURE section 4.11. `internal/platform/paths.go`. Issue #40.

---

### D2-9 journal: writer, journal.tsv reader, and snapshot readers

**Labels:** `type: feature`, `area: store` · **Blocked by:** D2-8, #10

**Summary.** Every command the learner runs is recorded to SQLite and readable back
through the `JournalReader` interface that `internal/verify` declares.

**Context.** The PTY multiplexer emits command events; this is what consumes them.
`instrument.bash` already writes `journal.tsv` inside the sandbox, so there are two
producers to reconcile: the host-side OSC 133 events and the in-sandbox TSV.

**Layer and files.** L2, `internal/journal`. New: `journal.go`, `tsv.go`,
`snapshot.go`, plus tests. May import `internal/store` and below.

**Interfaces.**

```go
type Entry struct {
    Seq        int64
    TS         time.Time
    LevelID    string
    Cwd        string
    Raw        string
    Exit       int
    DurationMS int64
    UsedTab    bool
    UsedHistory bool
}

func New(s *store.Store) *Journal
func (j *Journal) Append(ctx context.Context, e Entry) error
func (j *Journal) Commands(scope verify.Scope) []string // satisfies verify.JournalReader
func (j *Journal) Level(ctx context.Context, levelID string) ([]Entry, error)

// ReadTSV parses the in-sandbox journal written by instrument.bash.
func ReadTSV(r io.Reader) ([]Entry, error)
```

**Acceptance criteria.**

- [ ] An `Entry` appended is readable back with every field intact
- [ ] `Commands` satisfies `verify.JournalReader` for all three scopes without `internal/verify` importing this package
- [ ] `ReadTSV` parses the exact format `instrument.bash` emits: `EPOCHREALTIME \t exit \t PWD \t command`
- [ ] A command containing a tab does not corrupt the parse, because the split is bounded to four fields
- [ ] A truncated final line, which is what a crash mid-write produces, is skipped rather than failing the whole read
- [ ] Journal contents are never logged, never printed by default, and never included in a bug report body
- [ ] Writes are batched or transactional enough that a command per second does not thrash the disk

**Approach.** `Append` is on the hot path of every prompt, so it must not fsync per
command. WAL plus a transaction per batch is enough; losing the last few commands to
a crash costs an efficiency bonus, not correctness, because verification never reads
the journal to decide pass or fail.

Treat journal contents as secret material, as `internal/journal/doc.go` requires.
People type passwords into shells.

**Out of scope.** Replay and the watch-your-solution feature. Achievements derived
from journal events, which is Day 4. `used_tab` and `used_history` detection, which
needs keystroke tapping and is explicitly optional in ARCHITECTURE 4.6; the fields
exist and stay false.

**Safety review.** No deletion or elevation. Privacy is the live concern: a journal
entry can contain a password typed in error. Required: a test asserting no code path
writes an entry's `Raw` to a log, and a doc comment on `Entry.Raw` saying so.

**Test plan.** Round trip through a temp store. TSV parse table including a tab in
the command, a truncated line, an empty file, and a line with a negative exit code.
A scope test for all three kinds. A test that `internal/verify` compiles against
`*Journal` as a `JournalReader`, placed in this package so the dependency direction
stays correct.

**Docs to update.** `PROGRESS.md`.

**Skills.** `implementation`, `security`, `testing`, `component-traps`

**References.** ARCHITECTURE section 4.7. `internal/journal/doc.go`.
`images/rc/instrument.bash`. SEVEN-DAY-PLAN task 2.6.

---

### D2-10 cli: author validate and author scaffold

**Labels:** `type: feature`, `area: cli` · **Blocked by:** D2-0, D2-3

**Summary.** `shellforge author validate <pack>` reports every authoring problem
with file, level and field, and `author scaffold <id>` writes a level skeleton that
validates.

**Context.** This is the second exit criterion, verbatim. It is also what makes
D2-11 and D2-12 tractable: writing eight levels against a validator is a different
job from writing eight levels and hoping.

**Layer and files.** L5, `cmd/shellforge`. New: `author.go`, `author_test.go`.

**Acceptance criteria.**

- [ ] `author validate packs/core-linux-basics` exits 0 on the pack as it stands, with the missing-level-file warnings printed as warnings
- [ ] It exits non-zero and prints a located message for each of: duplicate id, prerequisite cycle, missing asset, missing `on_fail`, unknown check type
- [ ] Output is one problem per line, in file order, so it pastes into an editor's quickfix list
- [ ] `--json` emits the `Report` for tooling
- [ ] `author scaffold <id>` writes a level file and an assets directory, and the result passes `author validate` unmodified
- [ ] `scaffold` refuses to overwrite an existing level file
- [ ] Both go through `ux.Fail` on error, never a bare Go error

**Approach.** The scaffold's output is the first thing a new author sees, so it is
worth writing well: a real briefing with the timestamp convention, two hints, a
non-optional check with an authored `on_fail`, and a solution. A skeleton full of
`TODO` teaches nothing.

**Out of scope.** `author test`, the golden test runner, which needs a sandbox and
belongs with D2-13 or Day 5. `author record`, which is on the cut ladder.

**Safety review.** Yes, mildly. `scaffold` writes to the host filesystem at a path
derived from a user-supplied id. Validate the id against
`^[a-z0-9][a-z0-9-]*$` and join it to the pack levels directory, then confirm the
result is still inside that directory before writing. Refusal tests: an id of
`../../etc/passwd`, an absolute id, an id with a null byte, an empty id. No deletion.

**Test plan.** A test per exit-criterion failure mode using the `testdata/invalid/`
packs from D2-3. A `--json` shape test. A scaffold round trip: scaffold, then
validate, asserting zero problems. The four refusal tests. An overwrite-refusal test.

**Docs to update.** `docs/07-authoring-levels.md`, which is currently an outline and
should describe this workflow. `docs/LEVEL-FORMAT.md` section 7 if the commands
differ. `PROGRESS.md`.

**Skills.** `implementation`, `level-authoring`, `security`, `writing-style`

**References.** `docs/LEVEL-FORMAT.md` section 7. SESSION-PROMPTS Day 2 session C
item 4. SEVEN-DAY-PLAN Day 2 exit criteria.

---

### D2-11 content: Act I levels, nav-01 to nav-04

**Labels:** `type: content`, `area: content-engine` · **Blocked by:** D2-10

**Summary.** The four Act I levels exist as real YAML, validate clean, and pass
their golden tests.

**Context.** `docs/CURRICULUM.md` already specifies all four in detail: concepts, XP,
difficulty, par, objectives, setup, checks, hints and teaching notes. This ticket is
transcription plus judgement, not design. `nav-04` is the Act I boss and the
curriculum calls it the single most valuable level in the game.

**Layer and files.** New: `packs/core-linux-basics/levels/01-nav-01.yaml` through
`04-nav-04.yaml`, plus assets under `packs/core-linux-basics/assets/`. No Go.

**Acceptance criteria.**

- [ ] All four validate clean
- [ ] Each passes its golden test: checks all fail on a fresh sandbox, the solution runs, checks all pass, teardown leaves nothing
- [ ] `nav-01` is winnable in under 60 seconds by someone who knows `pwd`, per the curriculum's teaching note
- [ ] Every check has an `on_fail` that names what to try next, not what went wrong
- [ ] `nav-03`'s decoy tree means a relative path from the wrong directory fails, which is the lesson
- [ ] `nav-04`'s hints include the `/`-to-search-in-`less` hint, since beginners get trapped in the pager
- [ ] Each level's bonus objective is `optional: true`, so no command-pattern check can gate passing
- [ ] Setup is deterministic: `nav-04`'s spread mtimes come from `touch -d`, not from wall clock time

**Approach.** Write the checks first and the briefing last. It is much easier to
write a briefing that promises exactly what the checks verify than to retrofit
checks onto prose.

Zero Go changes are permitted in this ticket. If a level cannot be expressed, that is
a finding about the engine and it gets its own issue; it does not get a special case
in Go, because the first exit criterion is that levels load with zero Go changes.

**Out of scope.** Act II, which is D2-12. Levels 9 to 25, which are Day 5. ASCII art
and any presentation polish.

**Safety review.** Yes, per level. Every `setup.root` under `/home/learner/`, every
`source:` inside the pack, no setup or teardown script that deletes outside the level
root. `level-authoring` carries the setup and teardown script rules and they apply
to all four.

**Test plan.** `author validate`. The golden test per level. A manual playthrough of
each, with friction noted in the pull request, because a level that validates and is
still confusing is a level that failed.

**Docs to update.** `PROGRESS.md`. `docs/CURRICULUM.md` if a level's design changes
while writing it, so the curriculum stays the record of what was built.

**Skills.** `level-authoring`, `writing-style`, `destructive-safety`

**References.** `docs/CURRICULUM.md` Act I, levels 1 to 4. `docs/LEVEL-FORMAT.md`
sections 2 and 6.

---

### D2-12 content: Act II levels, files-01 to files-04

**Labels:** `type: content`, `area: content-engine` · **Blocked by:** D2-10

**Summary.** The first four Act II levels exist as real YAML, validate clean, and
pass their golden tests, completing the range the exit criterion names.

**Context.** Same shape as D2-11 and specified in `docs/CURRICULUM.md` levels 5 to 8.
Separated because `files-03` and `files-04` need `dir_tree` and the untouched-set
integrity check, which is genuinely harder than anything in Act I, and because eight
levels is two sittings.

**Layer and files.** New: `packs/core-linux-basics/levels/05-files-01.yaml` through
`08-files-04.yaml`, plus assets. No Go.

**Acceptance criteria.**

- [ ] All four validate clean and pass their golden tests
- [ ] `files-02`'s `deliveries.log` is generated with a seeded generator, is at least 4000 lines, and is byte-identical on every setup
- [ ] `files-02`'s bonus is `command_not_matched ^\s*cat\s` and is `optional: true`, so solving it with `cat` still passes the level
- [ ] `files-03` uses `dir_tree` with `names_and_hashes` for the backup and a `file_exists` and `file_absent` pair for the rename
- [ ] `files-04` asserts `important/` is byte-identical after the deletions, which is the actual test of the level
- [ ] `files-04`'s briefing states there is no recycle bin, before the learner deletes anything
- [ ] No level's teardown or setup script can touch anything outside its own `setup.root`

**Approach.** `files-04` is the level where a learner is most likely to delete the
wrong thing, and that is intentional pedagogy: the curriculum wants the mistake to
become an achievement rather than a punishment. The engine side of that is that
`reset` must be fast and obvious, and the level side is that the briefing warns and
the `on_fail` on the `important/` check is kind about it.

The `cp dir dest` without `-r` trap in `files-03` is deliberate. Let them hit it. The
`on_fail` explains it, which is the teaching moment.

**Out of scope.** `files-05`, the Act II boss with the 60 mixed files, which is Day 5.

**Safety review.** Yes, and more so than D2-11: these levels teach deletion. Every
deletion the learner performs is inside the sandbox and cannot reach the host, which
is `CLAUDE.md` non-negotiable 8 and is the runtime's guarantee, not the level's. What
the level owns is that its own setup and teardown scripts stay inside `setup.root`.
Required: a review pass over each script confirming no absolute path outside the
level root appears in any of them.

**Test plan.** `author validate`, golden tests, and a manual playthrough including
deliberately deleting `important/` in `files-04` and confirming `reset` recovers it.

**Docs to update.** `PROGRESS.md`. `docs/CURRICULUM.md` on any design change.

**Skills.** `level-authoring`, `destructive-safety`, `writing-style`

**References.** `docs/CURRICULUM.md` Act II, levels 5 to 8.

---

### D2-13 cli: wire run <level-id> end to end

**Labels:** `type: feature`, `area: cli` · **Blocked by:** D2-6, D2-7, D2-9, #9, #10, #11

**Summary.** `shellforge run nav-01` loads the level from YAML, sets up the sandbox,
prints the briefing and objectives, attaches a real shell, and answers `check` with
authored results.

**Context.** This is the ticket that turns thirteen libraries into the four exit
criteria. It generalises what #11 proves for one hardcoded level to any level in the
pack. It is last because it is the integration, and it should be started before the
others are finished so that the seams are found early.

**Layer and files.** L5 `cmd/shellforge/run.go`, plus L4 `internal/game` for the
minimum orchestration this needs. New: `cmd/shellforge/run.go`,
`internal/game/session.go`. `internal/game` must not import any runtime
implementation; it takes a `runtime.Runtime`.

**Acceptance criteria.**

- [ ] `shellforge run nav-01` through `run files-04` all work with zero Go changes between them
- [ ] The briefing renders from markdown to ANSI, and the objective checklist prints before the shell attaches
- [ ] Typing `check` in the sandbox returns results to the learner's terminal through the existing control FIFO
- [ ] A failing check prints its authored `on_fail`, and only the first failing required objective is shown
- [ ] Passed objectives print as passed and warn-severity notes print below
- [ ] `run` on an unknown level id fails through `ux.Fail`, naming the ids that do exist
- [ ] Exiting the shell tears the level down and leaves no stray processes
- [ ] Ctrl-C during a check cancels it and returns to the prompt without killing the session

**Approach.** The control channel already exists in the image: `images/bin/check`
writes to `$SF_STATE/control.req` and reads `$SF_STATE/control.res`. #11 implements
the host listener for one level. This ticket replaces the hardcoded level with a
loaded one and the hardcoded check with the D2-6 engine. Do not build a second
control mechanism.

Keep `internal/game` thin here. The full orchestrator state machine is Day 4 task
4.2. What this needs is load, setup, brief, attach, check, teardown, and nothing
about scoring, XP or unlocks.

**Out of scope.** Scoring, XP, hints, `map`, `stats`, achievements, resume: all Day
4. `play`, which auto-resumes, is Day 4. Live objective polling.

**Safety review.** Yes, via teardown. `run` triggers D2-7's teardown on exit, so the
containment rules there apply transitively. Additional rule here: a crash or a
Ctrl-C must not leave a sandbox running, and the cleanup path must be the same code
as the normal path, not a second implementation that was never tested.

**Test plan.** An end to end test per level in CI's image job: run headless, assert
checks fail, apply the solution through `Exec`, assert checks pass, assert teardown
left nothing. This is the golden test contract from `docs/LEVEL-FORMAT.md` section 7
and it is what `make golden` should run. A test for the unknown level id message. A
manual pass with `vim` open when `check` is typed, since that is the case where the
control FIFO and the PTY interact.

**Docs to update.** `docs/03-quickstart.md`, `docs/04-how-it-works.md`,
`PROGRESS.md`, and `docs/05-troubleshooting.md` for any new doc anchor.

**Skills.** `implementation`, `component-traps`, `testing`, `writing-style`,
`destructive-safety`

**References.** ARCHITECTURE sections 4.5 and 5.2. SESSION-PROMPTS Day 2 session C
closing paragraph. `docs/LEVEL-FORMAT.md` section 7. Issues #9, #10, #11.

---

## 7. Suggested order of work

Two tracks, joining at D2-13.

**Morning, sequential and blocking:** D2-0, then D2-1. Nothing else can start
cleanly until the binary builds and the dependencies are in.

**Track A, content:** D2-2, D2-3, D2-7, D2-10, then D2-11 and D2-12 in parallel.

**Track B, verification:** D2-4, D2-5, D2-6, with D2-8 and D2-9 slotted in whenever
Track B is blocked on the Day 1 debt.

**Day 1 debt** (#9, #10, #11) runs alongside and must land before D2-13 starts.

**Evening:** D2-13, then the four exit criteria demonstrated in one sitting, then a
`PROGRESS.md` entry. If the exit criteria cannot be demonstrated, the entry says so
plainly, which is the rule from the build plan.

---

## 8. Things this plan deliberately does not do

- **No `author test` golden runner as its own ticket.** The golden contract is
  implemented as CI tests inside D2-13 rather than as a user-facing verb, because
  the verb is authoring convenience and the tests are the actual gate. `author test`
  can land on Day 5 when levels are being written in volume.
- **No live objective polling.** ARCHITECTURE 4.10 calls it enormously satisfying
  and it is, but it is not in the day plan and it is a Day 4 presentation concern.
- **No check types beyond the thirteen.** `CLAUDE.md` bans this outright and
  LEVEL-FORMAT lists fifteen more as v0.2. Anything that needs one uses `type:
  script`.
- **No `files-05`.** It is the Act II boss and the exit criterion stops at
  `files-04`.
