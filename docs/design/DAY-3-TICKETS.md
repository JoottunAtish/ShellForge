# Day 3 Ticket Plan: Windows, WSL runtime and doctor

Status: plan of record for Day 3. Filed as issues #67, #68, #69, #70 and #71.

This document is the reasoning. The issues are the specifications. Where the two
disagree, the issue is right, because that is what an implementer reads.

---

## 1. What Day 3 has to produce

`SEVEN-DAY-PLAN.md` lists seven tasks for Day 3 and calls it the highest-risk day
of the week:

| # | Task | Est |
|---|---|---|
| 3.1 | CI: build the image, `docker create` plus `docker export`, publish `rootfs.tar.gz` as a release artifact | 1 h |
| 3.2 | `WslRuntime`: detect, `wsl --import` with checksum verify, name-collision handling, `Exec`, `Attach` | 3 h |
| 3.3 | Inject `/etc/wsl.conf`: interop off, automount off, appendWindowsPath off, default user `learner` | 0.5 h |
| 3.4 | Reset strategy: scratch-dir wipe under `/home/learner/quest` | 0.5 h |
| 3.5 | `internal/doctor`: every probe from ARCHITECTURE 4.2, plus `--json` | 2.5 h |
| 3.6 | Windows console: VT processing and VT input, detect `WT_SESSION` | 1 h |
| 3.7 | Test on real Windows 11, the full loop | 1.5 h |

The day exists to answer one question: **can Windows be a first-class platform
without requiring Docker Desktop?** Everything else on the list serves that.

---

## 2. Findings that change the plan

Five things turned up while scoping that the task table does not reflect.

### 2.1 Day 2 has not started, so one exit criterion is unreachable

The Day 3 exit criteria include:

> Clean Windows 11 VM: `doctor` then `init` then `run nav-01` works

`run nav-01` needs the content engine, the check registry, and a written level.
All of that is Day 2, and Day 2 is entirely open: #49 through #55 are filed and
every one of them carries `blocked`. There are zero levels written.

**This is recorded as a deliberate deviation rather than restated as a goal.** The
tickets are written against the loop that is actually reachable on Day 3:

```
doctor  ->  init  ->  sandbox shell  ->  vim  ->  sandbox destroy
```

That still exercises provisioning, the hardened `wsl.conf`, the PTY through
`wsl.exe`, and the destroy path, which is everything the gate needs to judge. The
`run nav-01` criterion carries over to whenever Day 2 lands, and it belongs to
#55 rather than to any ticket here.

### 2.2 Three rootfs producers disagree with each other

Task 3.1 is half built already, and the half that exists is internally
inconsistent:

- `.github/workflows/ci.yml`, in the `image` job, builds `rootfs.tar.gz` with
  `gzip -9`, prints `sha256sum`, and then **throws both away** when the job ends.
- `Makefile` target `rootfs` writes `images/out/rootfs.tar.gz` plus
  `rootfs.tar.gz.sha256`, using `sha256sum`.
- `make.ps1` case `rootfs` writes `images/out/rootfs.tar`, **uncompressed**, plus
  `rootfs.tar.sha256`, using `Get-FileHash`.

So a developer on Windows running `.\make.ps1 rootfs` gets a different filename,
different bytes, and a differently formatted sidecar than CI and the Makefile
produce. The WSL runtime verifies a digest before importing, and that verification
means nothing while there are three answers to what it is verifying.

### 2.3 `wsl.conf` and the troubleshooting anchors already exist

Two tasks are much smaller than their estimates suggest:

- **Task 3.3 is nearly done.** `images/wsl.conf` exists with the exact hardened
  content ARCHITECTURE 4.3.A specifies, and `images/Containerfile` already does
  `COPY wsl.conf /etc/wsl.conf`. What remains is rewriting and then verifying it
  after import, which matters because the config only takes effect after
  `wsl --terminate`.
- **Every doc anchor the doctor needs is already written.** `05-troubleshooting.md`
  carries a heading for all twelve probes and then some. Task 3.5 needs correct
  references, not new prose.

### 2.4 The destroy path needs a marker file the image does not carry

The `destructive-safety` skill requires that nothing is unregistered or deleted
until a Shellforge marker file has been confirmed present, so that a WSL
distribution name collision cannot lead to destroying something the learner owns.
It names the marker `/opt/shellforge/.sandbox-id`.

The image does not create it. `Containerfile` makes `/opt/shellforge/bin`, `rc`
and `state`, and nothing else.

The marker has to be baked into the rootfs for `Destroy` to check it, which makes
it an image concern rather than a runtime one. So #67 adds it and asserts it in the
tarball content check, and **#69 depends on #67** as a result. That dependency did
not exist in the first draft of this plan and was added once the safety rule was
read properly.

### 2.5 #11 hardcodes Docker, so there is no resolver to extend

The first draft of this plan assumed #11 had built runtime resolution for
`run demo` and that Day 3 should extend it. Checking the `feature/issue-11` branch
rather than the ticket text: it has not.

`cmd/shellforge/cmd_run.go` calls `docker.New(sandboxName, sandboxImage)`
directly. There is one hardcoded backend and no selection logic anywhere.

The same file also holds `checkInteractiveShellSupported`, which refuses an
interactive shell on Windows and emits the `windows-needs-wsl` anchor, with a
comment saying that until `WslRuntime` exists, running from inside WSL is the only
way. **That gate becomes wrong the moment #69 lands.** Relaxing it is scoped to
#71, and #69 carries a comment saying so, specifically so that two tickets do not
both edit the same call site.

---

## 3. Day 2 debt that does not block Day 3

Worth stating plainly, because it is the good news of this plan: **the Day 2
backlog does not block any of this.**

| Day 3 needs | State |
|---|---|
| `internal/runtime` interfaces, specs, sentinel errors | Done |
| `internal/runtime/runtimetest`, twelve contract assertions | Done |
| `internal/runtime/docker`, a working sibling to copy | Done |
| `internal/pty` multiplexer and OSC parser | Done |
| `internal/platform/ux`, the error and remediation shape | Done |
| `internal/platform` paths, `ValidIdentifier` | Done |
| `images/Containerfile`, `wsl.conf`, the in-sandbox shims | Written |
| `05-troubleshooting.md` anchors | Written |
| A CLI dispatcher to hang verbs on | Exists, hand-rolled |

Nothing on that list is waiting on the content engine, the verification engine, or
the store. Day 3 can run in parallel with the Day 2 backlog, which is the reason
this plan was written now rather than after Day 2 closes.

The one real coupling is the reverse direction: #50 lists `reset --hard` and
`sandbox rebuild` as Day 3 work, so `sandbox rebuild` lands in #71 while per-level
teardown stays with #50.

---

## 4. Decisions taken while scoping

**The reset strategy is the scratch wipe, and that is not revisited.** ARCHITECTURE
4.3.A offers three options. Export and import round-trips cost 10 to 60 seconds and
the entire point is sub-second reset. #69 implements only the primitive; the
transactional wrapper is #50's.

**The WSL contract suite will skip in CI, and that is correct.** GitHub's hosted
`windows-latest` runners have no WSL2, so `RunContract` will skip there exactly as
the Docker suite already skips for Windows-container mode. This is not a gap to
paper over with a mock: real Windows coverage is #60's problem, and a mocked
`wsl.exe` would assert that our code calls the commands we think it calls, which is
the one thing that was never in doubt.

**The `Debian` distribution on the development machine stays.** `wsl -l -v` on the
machine this was scoped on reports:

```
* Debian            Stopped   2
  docker-desktop    Running   2
```

`Debian` is the default distribution, and the `destructive-safety` skill cites its
adjacency as the reason the guards in item 1 exist. It was offered up for deletion
during scoping and deliberately kept, because it is the test fixture that makes two
of #69's acceptance criteria meaningful: `wsl -l -q` differing by exactly one entry
across a destroy, and the learner's own distributions being untouched. Being the
default distribution is precisely what makes it valuable, since a `wsl` invocation
missing its `-d` flag targets the default, and that bug should surface on a
maintainer's machine rather than on a user's.

**No new dependency.** `wsl.exe` writes UTF-16LE and the obvious reach is for
`golang.org/x/text`. It is not on the approved set, and a BOM check plus a manual
UTF-16 decode is about twenty lines. `golang.org/x/sys/windows` already provides
every console call #68 needs and is already in `go.mod`.

---

## 5. Consolidation: seven tasks into five tickets

### What merged

**Tasks 3.2, 3.3 and 3.4 became one ticket, #69.** Provisioning, the `wsl.conf`
injection and the reset primitive are three methods on one type implementing one
interface, and none of them can be verified alone: the only acceptance criterion
that matters is `RunContract` passing, and that needs the whole surface. Splitting
at the `Runtime` and `Session` seam was considered and rejected for that reason.

**Task 3.6 absorbed a Day 1 stub.** `internal/pty/raw_windows.go` has carried
`startResizeWatcher` as a documented no-op since Day 1, while the unix build arms a
real SIGWINCH watcher. Console VT mode and the resize watcher are both Windows
terminal host integration, they are both small, and they land together in #68.

**Task 3.7 was distributed rather than filed.** Manual validation is not a ticket,
it is the evidence a ticket closes with. Its checks became acceptance criteria on
#69, #68 and #71, each recorded in the pull request that claims them.

### What did not merge, and why

**#68 stayed separate from #70** despite being only 1.5 hours. Both #70 and #71
consume its console primitives. Folding it into #70 would make #71 depend on the
doctor ticket to get a console function, which is a dependency with no conceptual
justification.

**#67 stayed separate from #69.** It is CI and build tooling against a Go
implementation, different labels and different skills, and bundling a YAML change
into a large safety-critical Go review muddies both.

### What was added

**#71 is not in the task table.** The Day 3 exit criteria require
`sandbox destroy` to unregister the distribution and delete the vhdx, verified in
Explorer. That needs a verb that calls `Destroy`. Without #71, #69 is a library
with no entry point and the gate cannot be judged by a person. Folding it into #69
was the alternative and would have produced a roughly six hour ticket spanning L1
to L5.

### Net effect

Seven tasks, one absorbed stub and one added integration ticket, into five issues.

---

## 6. The ticket set

### A. #67, ci: publish the WSL rootfs tarball as a verified release artifact

`type: ci`, `area: sandbox-image`. Not blocked. Roughly 1 hour.

One rootfs, built one way, with one checksum format, actually downloadable.
Reconciles the three producers from finding 2.2 onto `rootfs.tar.gz` plus a
`sha256sum`-format sidecar, uploads both from the `image` job SHA-pinned, and
attaches them to a release on a tag. Adds `/opt/shellforge/.sandbox-id` per
finding 2.4, and asserts the tarball actually contains `/etc/wsl.conf`,
`/home/learner`, the shims, the marker and the man pages, because a tarball
missing `wsl.conf` produces a sandbox with automount ON, which is a silent breach
of the safety promise rather than a build failure.

Ruled out in the ticket: `docker save`, which produces an OCI layer archive that
`wsl --import` cannot read at all.

### B. #69, runtime: WslRuntime, the Windows sandbox backend

`type: feature`, `area: runtime`, `platform: windows`, `blocked` by #67. Roughly
4 hours. **The gate ticket, and the most dangerous ticket in the project so far.**

`internal/runtime/wsl` implements `Runtime` and `Session` and passes
`RunContract` unchanged. If the contract suite needs changing to accommodate the
backend, that is a design conversation, not an edit to the suite.

The provisioning order is specified and each step earns its place: resolve the
rootfs, verify the digest **before** import, check for a name collision, import at
version 2, write and re-read `wsl.conf`, then `wsl --terminate`. Skipping the
terminate leaves interop and automount ON for the first session, which means
`/mnt/c` exists, which is the mechanism behind the safety promise on Windows.

Ten named refusal tests, a marker check, and an enumerate-and-diff assertion that
`wsl -l -q` loses exactly one entry. The ticket also resolves an apparent
contradiction in the `implementation` skill: "write zero new tests" is correct
about the contract and does not exempt `parseList` or the path validator.

### C. #70, doctor: twelve probes, the table, and `--json`

`type: feature`, `area: doctor`, `blocked` by #68. Roughly 2.5 hours.

Twelve probes, each returning `{ID, Status, Detail, Remediation, DocAnchor}`, as a
table or as JSON, with `--fix` restricted to reversible non-privileged actions.

Two design points worth recording. `sandbox_health` has to ping a runtime, but
doctor is L0 and runtime is L1, so the import would point upward and `archtest`
will reject it; resolved with a `SandboxProber` interface defined in doctor and
satisfied at L5. And the CI anchor gate greps for a literal `DocAnchor: "..."`
field shape, so anchors carried in any other spelling make it print "No doc anchors
emitted yet" and exit 0, passing while every link is broken. A Go test walking the
real probe list is the actual gate.

### D. #68, platform: Windows console VT mode and the resize watcher

`type: feature`, `area: pty`, `platform: windows`. Not blocked. Roughly 1.5 hours.
**Lands first**, because #70 and #71 both consume it.

VT processing on stdout, VT input on stdin, restore on every exit path including a
panic, `WT_SESSION` detection, and a real resize watcher replacing the Day 1 no-op.

Windows has no SIGWINCH, and the ticket picks polling `GetConsoleScreenBufferInfo`
over reading `WINDOW_BUFFER_SIZE_EVENT`, with the reason recorded: the second
option requires draining the console input queue, and `Mux.Run` is already
forwarding stdin verbatim, so consuming input events in a second place would
swallow the learner's keystrokes.

### E. #71, cli: init, sandbox verbs, and runtime selection

`type: feature`, `area: cli`, `blocked` by #69 and #68. Roughly 2 hours.

`shellforge init [--runtime=auto|wsl|docker]` and
`shellforge sandbox status|shell|rebuild|destroy`. Replaces the hardcoded
`docker.New` from finding 2.5 so there is one resolution path, and relaxes
`checkInteractiveShellSupported`.

Selection goes through `Capabilities` and never branches on `Status.Backend`,
which `status.go` documents as display only, asserted by a test. That rule is what
keeps "drop WSL support" a one-day decision rather than a rewrite.

`destroy` requires the user to type the sandbox name rather than `y`, prints the
absolute directory first, and verifies afterwards. This layer passes no path and no
name of its own into `Destroy`, and offers no `--name` flag, ever, because doing so
would defeat every guard in the layer below.

---

## 7. Sizing and order

```
#67 ci: rootfs artifact          (none)          ~1.0 h
#68 platform: console + resize   (none)          ~1.5 h
        |                            |
        v                            v
#69 runtime: WslRuntime        #70 doctor        ~4.0 h  /  ~2.5 h
        |                            |
        +------------+---------------+
                     v
              #71 cli: init + sandbox            ~2.0 h
```

`#67` and `#68` are the only two without `blocked`, so they are the startable
work. Total is roughly 11 hours against the plan's 10, with the difference being
#71, which the plan did not budget.

Order matters more than the estimates. `#68` first unblocks two tickets for the
price of the smallest one.

---

## 8. The go or no-go gate

`SEVEN-DAY-PLAN.md` is unambiguous:

> **If `WslRuntime` is not working: cut it.** Switch Windows to "requires Docker
> Desktop", spend Day 4 on content instead, and note it in the README as v0.2
> work. Make this call by 18:00 Day 3.

What the gate judges, given finding 2.1:

- [ ] `doctor` reports honestly on a clean Windows 11 machine
- [ ] `init` provisions the WSL sandbox without Docker Desktop
- [ ] `wsl -l -v` shows the sandbox and the user's own distributions untouched
- [ ] Inside the sandbox, `/mnt/c` does not exist and `echo $PATH` is clean
- [ ] `sandbox shell` opens a real instrumented bash, and `vim` works in it
- [ ] `sandbox destroy` unregisters the distribution and the vhdx directory is
      gone, confirmed in Explorer

If `#69` is not green by the deadline, the cut is: close it `cut: v0.2`, keep
`#67`, `#68` and `#70`, which all stand on their own, and reduce `#71` to Docker
only. **None of the other four tickets depends on WSL working.** That is by design,
so the cut costs one ticket rather than the day.

---

## 9. Deliberately not done

- `shellforge bug-report`. SESSION-PROMPTS pairs it with the doctor session, but
  `commands.go` schedules it for Day 6 and it needs the journal from #51.
- Unicode and font render probing, in the ARCHITECTURE 4.2 table. It cannot be done
  reliably without drawing glyphs and reading the cursor back, and a wrong answer is
  worse than none.
- The Podman rootless probe, also in that table. `CLAUDE.md` forbids a Podman
  runtime, so the probe would report on something unreachable.
- `%USERPROFILE%\.wslconfig` memory and processor limits. Global to every
  distribution the learner owns, and ARCHITECTURE 10.6 says ask rather than
  silently edit.
- systemd. `Capabilities` reports false and the content layer gates out levels
  declaring it.
- Antivirus detection for slow imports, ARCHITECTURE 10.9. Noted as a
  `TODO(v0.2)` where the import timeout is handled.
- Multi-architecture rootfs. v0.1 is amd64.
- Signing and provenance for the release artifact. Day 6 hardening.
- The cobra migration. #48 owns it, and Day 3 adds verbs to the existing dispatcher
  rather than pre-empting it.
