# Shellforge Seven-Day Execution Plan

**Rule for every day:** the exit criterion is a *demoable behaviour*, not "code written". If you can't demo it, the day isn't done.

**Rule for the whole week:** integrate daily. Never have more than one day of unmerged work.

---

## Day 0 (evening before, ~1 h) - Setup

Do this before Day 1 so Day 1 is pure building.

- [ ] Create GitHub repo, MIT license, Go 1.23+ module `github.com/<you>/shellforge`
- [ ] **Commit `.gitattributes` first**, before any other file:
      ```
      * text=auto eol=lf
      *.sh   text eol=lf
      *.bash text eol=lf
      *.ps1  text eol=crlf
      *.tar  binary
      *.gz   binary
      ```
- [ ] Place `CLAUDE.md` at the repo root and `LEVEL-FORMAT.md` plus `CURRICULUM.md` under `docs/`
- [ ] `Makefile` with `build test lint run fmt`
- [ ] Basic `.github/workflows/ci.yml` (build + `go vet` + `go test ./...`)
- [ ] Enable Docker on your dev machine; have a Windows 11 VM or machine ready

---

## Day 1 - The Spike: prove the hard part works

> **Goal: one hardcoded level, playable end-to-end, on Linux, with a real bash.**

The riskiest thing in the project is the PTY + instrumentation. Do it first. If it doesn't work, everything downstream is worthless and you need to know on Day 1.

### Tasks

| # | Task | Est |
|---|---|---|
| 1.1 | Repo scaffold per `CLAUDE.md` layout; cobra CLI skeleton with `doctor`/`init`/`run` stubs | 1 h |
| 1.2 | `internal/runtime`: define the `Runtime` interface + `Caps` + `Session` | 0.5 h |
| 1.3 | `DockerRuntime`: `Provision` (pull/build image), `StartSession`, `Exec`, `Attach`, `PushFiles`, `Destroy` | 2.5 h |
| 1.4 | `images/Containerfile`: Debian slim + toolset + `learner` user + `/opt/shellforge` mount point | 1 h |
| 1.5 | `internal/pty`: multiplexer - raw-mode host terminal, `creack/pty`, bidirectional copy, `SIGWINCH` propagation | 2 h |
| 1.6 | `internal/pty/osc.go`: streaming ANSI/OSC parser that extracts `133;A/B/C/D;<exit>` and `7;file://...` and **strips them from the forwarded stream** | 2 h |
| 1.7 | `images/rc/instrument.bash`: PS1/PS0/PROMPT_COMMAND markers + journal append + `env -0` snapshot | 1 h |
| 1.8 | Hardcoded level: setup writes `~/quest/logs/*.log`, one `file_content` check, `check` shim in `/opt/shellforge/bin` | 1 h |

### Exit criteria (demo these)

- [ ] `shellforge run demo` opens a real bash prompt inside a container
- [ ] `vim`, `less`, `htop` all render correctly; terminal resize works mid-session
- [ ] Ctrl-C interrupts a command without killing the game
- [ ] After each command, the game's log shows `{cmd, exit_code, cwd, duration}` - **and none of the OSC escape codes are visible to the user**
- [ ] Typing `check` inside the shell reports pass/fail correctly
- [ ] `rm -rf /` inside the sandbox does nothing to the host

### Go/No-Go
If 1.5/1.6 are not working by end of day, **stop and fix them on Day 2 morning.** Do not proceed to content. There is no fallback for this component.

---

## Day 2 - Content engine + verification

> **Goal: levels are data, not code. Ten real levels loading from YAML.**

### Tasks

| # | Task | Est |
|---|---|---|
| 2.1 | `internal/content`: level + pack structs, YAML unmarshal, `embed.FS` loading | 1.5 h |
| 2.2 | Pack validator: unique IDs, DAG acyclic, assets exist, `on_fail` present on every check, check types registered, `solution` present | 1.5 h |
| 2.3 | `internal/verify`: check registry + 13 check types (see `docs/LEVEL-FORMAT.md` section 3) | 3 h |
| 2.4 | `any_of` / `all_of` / `not` composition nodes | 0.5 h |
| 2.5 | Setup/teardown runner: file materialization (**strip `\r`**), mode/owner, setup script, `SETUP_OK` sentinel, idempotent teardown-then-setup | 1.5 h |
| 2.6 | `internal/journal`: SQLite `event` writes + `journal.tsv` in-sandbox + env/cwd snapshot readers | 1.5 h |
| 2.7 | **Write levels 1-8** (Act I + start of Act II) as real YAML | 3 h |

### Exit criteria
- [ ] `shellforge run nav-01` ... `files-04` all playable from YAML with zero Go changes
- [ ] `shellforge author validate packs/core-linux-basics` catches: duplicate ID, cycle, missing asset, missing `on_fail`, unknown check type
- [ ] A failing check prints its authored `on_fail` message, not a generic error
- [ ] Only the **first** failing required objective is shown; passed objectives show ✅

---

## Day 3 - Windows: WSL runtime + doctor  ⚠️ *highest-risk day*

> **Goal: it works on a real Windows machine.**

### Tasks

| # | Task | Est |
|---|---|---|
| 3.1 | CI job: build container image → `docker create` + `docker export` → `rootfs.tar.gz` as a release artifact (single source of truth with the OCI image) | 1 h |
| 3.2 | `WslRuntime`: detect (`wsl -l -v`), `wsl --import` with checksum verify, name-collision handling, `Exec` via `wsl -d <name> -u learner --`, `Attach` via PTY over `wsl.exe` | 3 h |
| 3.3 | Inject `/etc/wsl.conf` (interop off, automount off, appendWindowsPath off, default user `learner`) | 0.5 h |
| 3.4 | Reset strategy: scratch-dir wipe under `/home/learner/quest` (sub-second) | 0.5 h |
| 3.5 | `internal/doctor`: all probes from architecture §4.2, each with `{status, detail, remediation, doc_anchor}`; `--json` output | 2.5 h |
| 3.6 | Windows console: enable `ENABLE_VIRTUAL_TERMINAL_PROCESSING` + `_INPUT`; detect `WT_SESSION` and warn if absent | 1 h |
| 3.7 | **Test on real Windows 11.** Full loop: init → play → vim → check → reset → destroy | 1.5 h |

### Exit criteria
- [ ] Clean Windows 11 VM: `doctor` → `init` → `run nav-01` works
- [ ] `wsl -l -v` shows `shellforge-sandbox` and the user's own distros are untouched
- [ ] Inside the sandbox: `echo $PATH` is clean, `/mnt/c` does not exist
- [ ] `sandbox destroy` unregisters the distro and deletes the vhdx directory - verified with Explorer

### 🚦 GO/NO-GO GATE (end of Day 3)
**If `WslRuntime` is not working: cut it.** Switch Windows to "requires Docker Desktop" (which uses the WSL2 backend, so the WSL requirement still holds), spend Day 4 on content instead, and note it in the README as v0.2 work. Make this call by 18:00 Day 3 - do not let it bleed into Day 4.

---

## Day 4 - Game core

> **Goal: it stops feeling like a test harness and starts feeling like a game.**

### Tasks

| # | Task | Est |
|---|---|---|
| 4.1 | `internal/store`: SQLite schema + migrations (architecture §4.11), WAL mode | 1.5 h |
| 4.2 | `internal/game`: Session Orchestrator state machine (`IDLE→SETUP→BRIEFING→ACTIVE⇄CHECKING→PASSED`) | 2 h |
| 4.3 | Event Bus + domain events (`LevelStarted`, `CommandExecuted`, `CheckRun`, `HintTaken`, `LevelPassed`) | 1 h |
| 4.4 | Curriculum DAG: unlock resolution, `map` command (text tree with ✅/🔓/🔒) | 1 h |
| 4.5 | Scoring: XP, difficulty multiplier, hint penalty, efficiency bonus, first-try bonus | 1 h |
| 4.6 | Hint ladder: `hint` reveals next tier, shows cost **before** committing, `hint --reveal` for solution | 1 h |
| 4.7 | Achievements: ~10, all derived from journal events (Tab Master, No Hints, Manual Labour, One-Liner, Streak, rm -rf Survivor, Act clears) | 1 h |
| 4.8 | Output polish: briefing renderer (markdown → ANSI), live objective checklist, pass/fail banners, rank-up celebration, `stats` | 2 h |

### Exit criteria
- [ ] `shellforge play` auto-resumes at the correct next level
- [ ] XP persists across restarts; `map` shows locks correctly
- [ ] Passing a level prints XP earned, objectives hit, and any achievement unlocked
- [ ] Hints deduct XP and the cost was shown before confirming

---

## Day 5 - Content sprint

> **Goal: 25 levels. This is a writing day, not a coding day.**

Levels 9-25 from `docs/CURRICULUM.md`. Work Act by Act. For each level produce: briefing, objectives, setup assets, checks with **authored `on_fail` for every one**, 3-4 hints, solution, teardown.

| Block | Content | Est |
|---|---|---|
| 5.1 | Act II remainder (levels 9) + Act III plumbing (10-14) | 3 h |
| 5.2 | Act IV search (15-18) | 2 h |
| 5.3 | Act V permissions & processes (19-22) | 2.5 h |
| 5.4 | Act VI environment & scripting (23-25), incl. final boss | 2 h |
| 5.5 | `author record` helper (record a session, emit solution + suggested patterns) - *only if time* | 1 h |

### Exit criteria
- [ ] 25 levels validate clean
- [ ] You personally played the whole campaign start to finish in one sitting and noted the friction points
- [ ] Every act ends in a boss level that takes 10+ minutes

### If behind
Ship Acts I-IV (18 levels) and move Acts V-VI to v0.2. **Do not ship half-written levels** - one level with a vague `on_fail` does more damage than a missing level.

---

## Day 6 - Hardening, CI, packaging

> **Goal: someone other than you can install it.**

### Tasks

| # | Task | Est |
|---|---|---|
| 6.1 | `author test <id>` golden runner: fresh sandbox → **assert checks FAIL** → run `solution` → **assert checks PASS** → assert teardown clean | 1.5 h |
| 6.2 | Wire golden tests into CI for all levels; matrix `ubuntu-latest` + `windows-latest` | 1.5 h |
| 6.3 | Check purity test: run all checks twice, diff filesystem hash - must be identical | 0.5 h |
| 6.4 | Mutation tests: for 5 representative levels, feed known near-misses (wrong case, hardcoded answer, right file wrong dir) and assert rejection | 1 h |
| 6.5 | GoReleaser: `windows/amd64`, `linux/amd64`, `linux/arm64`, `darwin/*`; checksums; GitHub Release | 1 h |
| 6.6 | `install.sh` + `install.ps1`: detect arch, download, verify checksum, place on PATH, run `doctor` | 1.5 h |
| 6.7 | `bug-report` command: bundles doctor JSON + logs + versions + redacted journal | 0.5 h |
| 6.8 | Full clean-VM test on Windows 11 **and** Ubuntu, following only the written docs | 1.5 h |

### Exit criteria
- [ ] CI green on both platforms
- [ ] A GitHub pre-release exists and `install.ps1` from it works on a clean VM
- [ ] Uninstall verified: no orphaned vhdx, no stray config

---

## Day 7 - Documentation + release

> **Goal: the docs are the product for anyone who hasn't installed it yet.**

| # | Task | Est |
|---|---|---|
| 7.1 | `README.md`: hook, demo GIF, 60-second quickstart, what it teaches, safety note | 1.5 h |
| 7.2 | `docs/01-install-windows.md` - the most important file in the repo. Screenshots. Every `doctor` failure has an anchor here. | 2 h |
| 7.3 | `docs/02-install-linux.md`, `03-quickstart.md`, `04-how-it-works.md`, `05-troubleshooting.md`, `06-uninstall.md` | 2 h |
| 7.4 | `docs/07-authoring-levels.md` (derived from `docs/LEVEL-FORMAT.md`) + `CONTRIBUTING.md` | 1 h |
| 7.5 | Record demo GIF (`asciinema` → `agg`, or `vhs` by Charm - scriptable and reproducible) | 1 h |
| 7.6 | CI check: every `doc_anchor` emitted by `doctor` exists in the docs | 0.5 h |
| 7.7 | Tag v0.1.0, publish release, write the announcement post | 1 h |

### Exit criteria
- [ ] A person who has never seen the project installs and plays using only the README
- [ ] `v0.1.0` released with binaries and rootfs artifact attached

---

## Daily discipline

**Every morning (10 min):** re-read yesterday's exit criteria. Anything unchecked either gets done before new work or gets formally cut. No silent carry-over.

**Every evening (15 min):** commit, push, CI green, update a one-line status in `PROGRESS.md`. If CI is red overnight, Day N+1 starts broken.

**The cut ladder** - when behind, cut in this order:
```
1. Levels 19-25 (Acts V-VI)
2. Achievements beyond 3
3. Efficiency/first-try scoring bonuses
4. WslRuntime → Docker Desktop on Windows
5. macOS builds
6. `author record` helper
7. Mutation tests
NEVER CUT: doctor · install docs · golden tests · reset · sandbox isolation
```
That last line is the whole point. A game with 12 levels that installs cleanly and can't hurt anyone beats a game with 25 levels that strands Priya at step 3.
