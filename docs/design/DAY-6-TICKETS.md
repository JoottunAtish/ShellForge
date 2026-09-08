# Day 6 Ticket Plan: hardening, CI, and packaging

Status: plan of record for Day 6. Filed as issue #158, a single issue, deliberately.

This document is the reasoning. The issue is the specification. Where the two
disagree, the issue is right, because that is what an implementer reads.

---

## 1. What Day 6 has to produce

`SEVEN-DAY-PLAN.md` gives Day 6 eight tasks under one goal: "someone other than
you can install it."

| # | Task | Est |
|---|---|---|
| 6.1 | `author test <id>` golden runner | 1.5 h |
| 6.2 | Golden tests in CI for all levels, matrix ubuntu plus windows | 1.5 h |
| 6.3 | Check purity test: run all checks twice, diff the filesystem hash | 0.5 h |
| 6.4 | Mutation tests for five representative levels | 1 h |
| 6.5 | GoReleaser: binaries, checksums, GitHub Release | 1 h |
| 6.6 | `install.sh` plus `install.ps1` | 1.5 h |
| 6.7 | `bug-report` command | 0.5 h |
| 6.8 | Full clean-VM test on Windows 11 and Ubuntu | 1.5 h |

Exit criteria: CI green on both platforms, a GitHub pre-release exists and
`install.ps1` from it works on a clean VM, and uninstall is verified with no
orphaned vhdx and no stray config.

Three of the eight are not work.

**6.1 is done.** `shellforge author test` landed with #99, implements the
`docs/LEVEL-FORMAT.md` section 7 contract, and is what `make golden` calls. It
refuses rather than skips without a Docker daemon, which was a deliberate choice
recorded in `PROGRESS.md`: a `make golden` that reports success having tested
nothing is worse than no gate at all.

**6.3 is done, in two halves.** `internal/verify/purity_test.go` is the hermetic
half: an assertion that every check type runs only read-only commands.
`assertCheckPurity` in `cmd/shellforge/golden.go` is the real half: hash the
level world, run every check twice, hash again, compare. The second needs Docker
and runs in CI's Sandbox image job.

**6.8 cannot be a code ticket.** It is a person sitting in front of two clean
virtual machines following only the written documentation. It is the acceptance
test for this ticket rather than part of it.

That leaves 6.4, 6.5, 6.6 and 6.7. Issue #158 is those four.

---

## 2. Why one ticket and not four

The `ticket-creation` skill says one ticket is one branch is one pull request,
and that a ticket with more than about eight acceptance criteria is two tickets.
Issue #158 has more than that and is still one ticket, for the same reason #152
was one ticket for sixteen levels: the rule optimises for a reviewer being able
to hold the change in their head, and here splitting makes that harder rather
than easier.

The four parts are one claim: **a stranger can install this and tell us when it
breaks.** They only prove out together.

- An installer with no release to fetch from cannot be tested end to end. It can
  be unit tested against a fixture, which is worth doing and is not the same
  thing as knowing it works.
- A release with no installer is an artifact nobody consumes. The asset naming
  is a contract between the workflow and the two installers, and a contract
  split across two pull requests is a contract that drifts between them.
- `bug-report` is the thing that carries a failed install back to us. Shipping
  installers without it means the first install failure arrives as a screenshot.
- The mutation tests are the odd one out and go in anyway, because they are the
  last piece of the "does the teaching actually work" gate that 6.1 and 6.3
  started, and because they are 1 hour of work that would otherwise sit alone in
  a fifth pull request nobody prioritises.

The counter-argument, recorded honestly: a reviewer reading this diff has to
hold Go, YAML, GitHub Actions YAML, POSIX shell and PowerShell in one sitting.
That is a real cost. It is accepted because the alternative is four pull
requests where three of them cannot demonstrate that they work.

---

## 3. Findings that change the plan

Six things turned up while planning that the ticket had to absorb. Four are
decisions, two are defects in the tree today.

### 3.1 The journal contradiction is a real disagreement, and the docs win

`internal/journal/journal.go` says of `Entry.Raw`:

> secret material: never logged, never in a bug report

`docs/04-how-it-works.md` section 4 tells the learner:

> `shellforge bug-report` includes the commands you ran but not their output,
> and never the environment snapshot.

`ARCHITECTURE.md` line 773 specifies the bundle as "doctor JSON plus last logs
plus versions (redacted journal)".

Two of the three agree that the journal ships, redacted. One says it never
does. That is not a wording nit: it decides whether the command is useful.

**The documentation wins.** A bug report without the commands the learner ran is
a bug report about a shell that reports nothing about the shell. So the commands
ship, with three constraints that keep the field comment's intent intact:

1. Off by default. `--journal` is opt-in, so the accident case is the safe case.
2. Redacted through a named, closed-list function that lives in
   `internal/journal`, because that package owns the secret-material contract
   and a redaction rule kept anywhere else will drift from it.
3. Never the output, never the environment snapshot, never the database.

The field comment gets corrected in the same commit. Leaving a comment in the
tree that contradicts what the code does is how the next person makes the
opposite decision in good faith.

### 3.2 Redaction is a closed list, not an entropy heuristic

The obvious implementation of "redact secrets from a command line" is to look
for long high-entropy runs. It is the wrong one here.

`files-04` asserts three sha256 sums. A learner comparing a hash by hand types a
64 character hex string, and an entropy rule eats it. So the one input a
maintainer needs in order to understand why `important-intact` failed is the one
input the redactor removes.

The rule is therefore a list of five named patterns (secret-shaped `KEY=VALUE`,
secret-shaped long flags, the `-p` forms of the database clients, the
`Authorization` header, and PEM blocks), documented in `doc.go` and enumerated
in the issue. The honest limitation, that a secret typed in a shape not on the
list survives, is stated in the `README.txt` inside the bundle: read it before
you attach it.

A closed list that misses something is a known gap. A heuristic that eats the
evidence is a silent one.

### 3.3 `script-01` accepts a wrong answer today

`script-01`'s `counts-correctly` check has this in its `on_fail`:

> `find "$1" -maxdepth 1 -type f -name '*.log' | wc -l` counts them; without
> `-maxdepth 1` you would also count anything nested

The level's setup script creates five `.log` files directly in `logs/`, two
files that are not logs, and `empty/.keep`. There is nothing nested anywhere. So
a submitted script with no `-maxdepth 1` returns 5 for `logs/` and 0 for
`empty/`, and passes.

The level's own `on_fail` promises a distinction the level's world cannot make.
That is the `accepts-wrong-answer` failure mode, and it is exactly what a
mutation test exists to find: the check was written correctly against a world
that was never built to exercise it.

The fix is one asset, `logs/archive/2025-11-30.log`. With it, the maxdepth-1
answer is still 5, the level's own solution is unchanged and still correct, and
an implementation without `-maxdepth 1` returns 6 and is rejected. The briefing
already says "directly inside it", so no prose moves.

This is the pattern to expect from the other three levels too, and the rule
`CLAUDE.md` states applies: if a near miss passes, the level is wrong. Fix the
level. Never loosen the case to match.

### 3.4 Two of the five levels need a journal, and two cases must assert passing

The existing `TestPipe05RejectsNearMisses` builds its `game.Session` with no
`Journal`, which is fine for `pipe-05` because none of its checks reads one. Two
of the four new levels are not so lucky:

- `perm-01`'s `used-symbolic` is `command_matched` with `scope: level`.
- `find-04`'s `no-hardcoded-count` is `command_not_matched`, also level scoped.

Without a journal those objectives evaluate against an empty command list.
`golden.go` already solved this for `author test`: `solutionJournal` derives a
command list from the level's own `solution` text, because applying a solution
non-interactively never triggers the shell instrumentation that would write a
real one. The mutation harness passes the same thing, and overrides it per case
where the case is about what the learner typed.

The second half of this is more interesting. Neither of those two checks can
fail a level:

- `used-symbolic` is `optional: true`. It is a bonus.
- `no-hardcoded-count` is `severity: warn`. It is advice.

`ObjectiveResult.blocking()` returns false for both, so `echo 22 > touched.txt`
leaves `find-04` **passing**, with the warning in `LevelResult.Notes`. A
mutation case that asserted rejection there would be asserting a bug into
existence, and it would be the kind of bug that gets "fixed" a month later by
making a bonus objective blocking, which changes the game.

So the near-miss table carries a `wantNote` field: the case asserts the level
passed and that the named objective landed in `Notes`. Two of the twenty-odd
cases are that shape, and they are the two most likely to be got wrong.

### 3.5 Hand-rolled release, not GoReleaser

6.5 names GoReleaser. This ticket does not use it.

The repository has an explicit supply-chain posture. Every Action is pinned to a
40 character SHA with a version comment, `scripts/check-ci-gates.py` fails the
build on a tag pin, and the Sandbox image job carries this comment about using
the preinstalled `gh`:

> so this needs no new third party Action pinned into the supply chain

`modernc.org/sqlite` was chosen over `mattn/go-sqlite3` specifically to keep cgo
out of the release matrix, which means cross-compilation is a `GOOS`/`GOARCH`
loop around `go build`. Three targets, `-trimpath`, the same ldflags the
`Makefile` already defines, `tar.gz` for Linux and `zip` for Windows, one
`SHA256SUMS`. Roughly fifteen lines of workflow.

What GoReleaser would buy: changelog generation, archive layout conventions,
and a release-notes template. What it would cost: a third-party Action holding
the release token, plus a `.goreleaser.yaml` whose behaviour is a second thing
to understand when a release goes wrong. For three targets and no cgo, the trade
is not worth it. If the matrix ever grows a platform that needs a real
toolchain, revisit it then.

### 3.6 Publishing moves into one workflow, which closes #105

`ci.yml`'s Sandbox image job currently ends with a step that attaches the rootfs
tarball to a release on a tag push. To do that the whole job carries
`permissions: contents: write`, on a job that also runs on every pull request.
That is #105: "scope the image job's contents:write to the release-attach step
only."

GitHub has no per-step permission scope, so #105 as literally worded is not
implementable. It is implementable as stated intent, by moving the publishing
somewhere that only ever runs on a tag.

So `release.yml` owns everything that touches a Release: the binaries, the
rootfs, the checksums. The Sandbox image job keeps everything that is a gate
(build the image, assert its contents, run the golden contract, assert the
tarball has what `wsl --import` needs, upload it as a CI artifact) and goes back
to `contents: read`.

That is a net reduction in what a pull request build is allowed to do, and it
resolves #105 by removing the permission rather than narrowing it. The rootfs
export itself is not duplicated: `release.yml` calls `make rootfs`, which
already produces `images/out/rootfs.tar.gz` and its sidecar with the same
`gzip -9 -n` that keeps the digest reproducible.

### 3.7 The installers are deliberately asymmetric about PATH

`install.sh` will not edit a shell profile. `install.ps1` will edit the user
PATH. That looks inconsistent and is not.

On Unix, the person running a `curl | sh` installer can act on "add this line to
your shell profile", and the cost of getting it wrong is a broken login shell
that the installer cannot undo. The `destructive-safety` skill exists to stop
exactly this class of reach outside a program's own business. So the script
prints the `export` line and stops.

On Windows, `docs/01-install-windows.md` is written for somebody who has never
opened a terminal, and "add this directory to your PATH environment variable" is
not an instruction that person can follow. The PATH edit is therefore part of
the install, constrained to the smallest form that works:

- `'User'` scope only, never `'Machine'`. No elevation, ever.
- Read the current value first and skip when the directory is already there, so
  running the installer twice does not append twice.
- Print exactly what changed.
- `-NoPathChange` for anyone who would rather do it themselves.

Both installers verify the sha256 from `SHA256SUMS` **before** anything is
placed. An installer that verifies afterwards is not verifying, and the security
skill states it as a rule rather than a preference.

---

## 4. What the four parts are

### Part 1: mutation tests (6.4)

`TestLevelsRejectNearMisses` replaces `TestPipe05RejectsNearMisses` and covers
the five levels the `testing` skill names: `pipe-05`, `files-04`, `find-04`,
`perm-01`, `script-01`. One table, one subtest per level, one sub-subtest per
case. Each case applies the level's own solution clean, then writes exactly one
wrong thing on top, so the case tests the check and not the setup.

The cases are the mistakes a real learner makes: a file truncated instead of
deleted, a glob that caught two of three, a directory emptied but not removed,
a recursive `chmod` that took a README with it, a count typed rather than
computed, a mode with the right nine bits and a setuid bit on top, an error
message on stdout instead of stderr, a script in the wrong directory.

Folding pipe-05 in means one name instead of two, and that name appears in three
`-run` regexps: `.github/workflows/ci.yml`, `Makefile`, and `make.ps1`. All
three move in the same commit.

`TestMutationTableCoversTheRequiredLevels` is hermetic and fails if an id is
dropped from the table, so coverage cannot quietly shrink.

### Part 2: the release pipeline (6.5)

`.github/workflows/release.yml`, on a `v*` tag push and `workflow_dispatch`.
Three binaries (`linux/amd64`, `linux/arm64`, `windows/amd64`), the rootfs and
its sidecar, and one `SHA256SUMS`. Asset names are fixed because three things
agree on them: the workflow, both installers, and a pytest that asserts they
match.

macOS is not built. Nothing in the repository has ever run on darwin, `doctor`
has no darwin probes, and macOS is rung 5 on the cut ladder. Cross-compiling is
free and support is not, so the installers name darwin explicitly when they
refuse rather than shipping a binary nobody has run.

### Part 3: the installers (6.6)

`scripts/install.sh` is POSIX `sh`, because it is piped into `sh`. Small
functions, `curl` or `wget`, download to a `mktemp -d`, verify, and only then
place. On mismatch, delete the temporary directory and nothing else, print both
digests, exit non-zero with nothing installed.

`scripts/install.ps1` does the same with `Get-FileHash`, needs no execution
policy change beyond `-Scope Process`, and edits the user PATH under the
constraints in 3.7.

The test seam is a base URL override on both, which lets the whole thing be
exercised offline against a fixture release tree. `install.sh` gets a pytest
file in the existing `scripts/tests` suite; `install.ps1` gets a pwsh step on
the existing Windows CI leg. Neither adds a CI job, so neither needs a ruleset
change, which `scripts/check-ci-gates.py` would otherwise demand.

### Part 4: `bug-report` (6.7)

A new `internal/bugreport` package at L4, plus the command at L5. It writes a
zip holding `report.json`, `doctor.txt`, a `README.txt` that says what is and is
not in there, and any log files that exist.

The design rule is that `Collect` records rather than fails. Every source is
optional and a missing one produces a note. The reason is concrete: the most
likely bug report in this project is "Docker will not start", and resolving a
runtime is precisely what does not work in that case. A `bug-report` that exits
with an error there is useless at the only moment it matters.

`Prober` is declared in `internal/bugreport` and satisfied by `cmd/shellforge`,
the same seam `doctor.SandboxProber` uses, so the bundle never learns what a
backend is and `internal/runtime` stays below it.

---

## 5. What Day 6 does not do

- **6.2's Windows half.** The Ubuntu half of "golden tests in CI for all levels"
  has been running in the Sandbox image job since Day 2. The Windows half needs
  a runner with a Linux-capable Docker engine or working nested virtualization,
  which is #60, which is labelled `blocked`. Day 6's exit criterion "CI green on
  both platforms" is already met by the existing matrix: `Test (windows-latest)`
  builds, vets, and runs the hermetic suite. What it does not do is run a
  container, and nothing in this ticket changes that.
- **6.8, the clean-VM install.** A person, two virtual machines, and only the
  written documentation. It is the acceptance test for this work.
- **The campaign played start to finish in one sitting.** Still carried from Day
  5, still a human task, still recorded as not done in `PROGRESS.md` rather than
  quietly dropped.
- **macOS binaries,** per part 2.
- **GoReleaser,** per 3.5.
- **Package managers.** Scoop and Homebrew are M5 in `ARCHITECTURE.md`.
- **`shellforge update` and remote content packs.** Both are v0.2. Nothing about
  installing needs either.
- **`author scaffold` and `author record`.** Still stubs. `record` was marked
  "only if time" in block 5.5 and cut in the Day 5 plan; nothing has changed.
- **Writing the Day 7 install pages.** `docs/01-install-windows.md` and
  `docs/02-install-linux.md` are outlines by design and get written on Day 7,
  with screenshots, against a build that installs. This ticket corrects only the
  status claims those pages carry that the work makes false.
- **Any form of upload.** The bundle is a file on disk. There is no telemetry,
  not opt-in, not anonymous, and there is nothing to enable.

---

## 6. Definition of done for the day

- `TestLevelsRejectNearMisses` green against a real Docker daemon for all five
  levels, with the two `wantNote` cases asserting a pass rather than a rejection.
- `script-01` fixed, and `shellforge author test script-01` still green with the
  level's own unchanged solution.
- No level's checks loosened. Any level tightened is named in the pull request
  along with the alternative solutions re-confirmed against it.
- A `workflow_dispatch` run of `release.yml` has produced all six assets, and
  `sha256sum -c SHA256SUMS` passes against them.
- `install.sh` run end to end against those assets on Linux, and the installed
  binary answers `shellforge version` with the right tag.
- `install.ps1` exercised against the fixture on the Windows CI leg, including
  the refusal cases.
- `shellforge bug-report` produces a bundle with no sandbox, with Docker
  stopped, and with no progress database, and the default invocation contains no
  command text at all.
- `ci.yml`'s Sandbox image job no longer holds `contents: write`, and #105 is
  closed.
- `internal/journal` and `docs/04-how-it-works.md` say the same thing about what
  a bug report carries.
- `PROGRESS.md` updated in the same commit, naming both defects from section 3,
  and stating plainly which gates ran locally and which are left to CI.
