# Shellforge Claude Code Session Prompts

Ready to paste. One session per block; start a fresh session per block so context stays tight.

**Before every session:** confirm `CLAUDE.md`, `docs/LEVEL-FORMAT.md`, and `docs/CURRICULUM.md` are in the repo and referenced. Claude Code reads `CLAUDE.md` automatically; point at the others explicitly.

**General rules to enforce in every session:**
- Ask before adding any dependency not in the approved list.
- Write the test before or alongside the code, never "later".
- Stop and report if a task is larger than it looked, rather than half-doing it.

---

## Day 1 · Session A - Scaffold + Runtime + Docker

```
Read CLAUDE.md first.

Build the foundation for Shellforge:

1. Repo scaffold matching the layout in CLAUDE.md. cobra CLI with commands
   doctor, init, run, check, sandbox (subcommands: status, shell, destroy).
   All stubs except `run`.

2. internal/runtime: define the Runtime and Session interfaces exactly as
   specified in CLAUDE.md, plus ImageSpec, SessionSpec, ExecOpts, ExecResult,
   FileManifest, Caps, Status.

3. internal/runtime/docker: implement Runtime against the Docker CLI
   (shell out to `docker`, do NOT add the Docker SDK dependency - the CLI is
   simpler, has no version-skew issues, and we only need six operations).
   - Provision: build/pull image, idempotent
   - StartSession: `docker run -d --network none --cap-drop ALL sleep infinity`
   - Exec: `docker exec -u <user>` capturing stdout/stderr/exit separately
   - Attach: `docker exec -it` wired to a PTY (leave the PTY plumbing as a
     TODO returning the *exec.Cmd - session B wires it)
   - PushFiles / PullFile: `docker cp` via a staging dir, stripping \r from
     text files
   - Destroy: rm -f container

4. A contract test suite in internal/runtime/runtimetest that any Runtime
   implementation can be run against. Run it for Docker.

5. images/Containerfile: Debian slim, non-root user `learner` (uid 1000),
   packages: coreutils findutils grep sed gawk less nano vim-tiny tree jq
   curl git procps psmisc htop man-db manpages bash-completion cron
   openssh-client file. Do NOT strip man pages - reading `man` is a level.
   Locale C.UTF-8, TZ UTC. Create /opt/shellforge (read-only mount point)
   and /home/learner.

Constraints: no TUI, no dependencies beyond cobra + testify, context on every
runtime operation, no orphan containers on Ctrl-C.
```

---

## Day 1 · Session B - PTY multiplexer + OSC 133  ⚠️ *the critical one*

```
Read CLAUDE.md, especially the "internal/pty - the OSC parser" section.

Implement the terminal instrumentation layer.

1. internal/pty/mux.go - PTY multiplexer:
   - Put the host terminal in raw mode; GUARANTEE restore on every exit path
     (defer + signal handler for SIGINT/SIGTERM, and on panic).
   - Use creack/pty to allocate a PTY for the runtime's attach command.
   - Bidirectional copy: host stdin → PTY master, PTY master → parser → host
     stdout.
   - Propagate SIGWINCH to TIOCSWINSZ on the PTY.
   - Expose a channel of CommandEvent{Raw, ExitCode, Cwd, StartedAt, Duration}.

2. internal/pty/osc.go - streaming escape sequence parser:
   - A byte-level state machine: Normal → ESC → OSC → PAYLOAD → terminator
     (BEL or ESC backslash). It MUST handle sequences split arbitrarily
     across Read() boundaries.
   - Recognize: 133;A (prompt start), 133;B (command start), 133;C (pre-exec),
     133;D;<exit> (command done), 7;file://<host><path> (cwd report).
   - Strip ONLY recognized sequences from the forwarded stream. Forward every
     other byte unmodified - vim depends on this.
   - Unrecognized OSC payloads pass through untouched and must not corrupt
     parser state.

3. images/rc/instrument.bash - the in-sandbox rc file:
   - Source /etc/bash.bashrc and ~/.bashrc first.
   - PS1 wrapped with \[\e]133;A\a\] ... \[\e]133;B\a\]  (readline \[ \]
     markers are REQUIRED here or line wrapping breaks).
   - PS0='\e]133;C\a'
   - PROMPT_COMMAND runs a function that: captures $? FIRST, emits
     133;D;<exit> and OSC 7 with $PWD, appends a TSV row
     {epoch, exit, cwd, command} to $SF_JOURNAL using `history 1`,
     dumps `env -0` to $SF_ENV, and returns the original exit code.
   - HISTFILE per level, HISTSIZE 100000, histappend on, HISTCONTROL unset.
   - Prepend /opt/shellforge/bin to PATH.
   - Mark PROMPT_COMMAND/PS0 readonly (mild anti-tamper).

4. Tests:
   - Parser: markers split across reads at every byte offset; a recorded vim
     session's byte stream passes through byte-identical apart from our
     markers; user-typed fake 133 sequences don't break state.
   - Integration: run bash in a container with the rc file, send scripted
     input, assert the emitted CommandEvents match expectations including
     exit codes.

Success criterion for the day: `shellforge run demo` gives a real bash prompt
where vim, less and htop all work correctly, resizing works mid-session,
Ctrl-C interrupts the command not the game, and no escape codes are visible
to the user - while the game logs every command with its exit code.
```

---

## Day 2 · Session C - Content engine

```
Read CLAUDE.md and docs/LEVEL-FORMAT.md. Implement the level format exactly
as specified there - do not invent fields.

1. internal/content: Pack and Level structs matching docs/LEVEL-FORMAT.md section 2,
   YAML loading (goccy/go-yaml), embedded via embed.FS from packs/.

2. Validator implementing every rule in docs/LEVEL-FORMAT.md "Authoring
   invariants": unique ids, acyclic prerequisite DAG, referenced assets
   exist, every check has non-empty on_fail, objectives ↔ check ids match
   1:1, solution non-empty, ≥2 hints, setup.root under /home/learner,
   all check types registered, content_api compatible.
   Errors must name the file, the level id and the field.

3. internal/content/setup: setup runner -
   - teardown first (always), then materialize files into a staging dir,
     strip \r from text files, apply mode/owner, docker-cp/wsl-copy in,
     run setup.script as root with timeout, write a SETUP_OK sentinel.
   - Transactional: any failure rolls back and reports cleanly.
   - Deterministic generators for setup.files[].generate (kind: loglines,
     seeded).

4. `shellforge author validate <pack>` and `author scaffold <id>`.

Then wire `shellforge run <level-id>` to: load level → setup → print briefing
and objectives → attach shell → on `check`, report results.
```

---

## Day 2 · Session D - Verification engine

```
Read docs/LEVEL-FORMAT.md section 3 and section 4.

Implement internal/verify:

1. A registry: Register(typeName, factory). Unknown type at pack-load time is
   a validation error, never a runtime surprise.

2. These 13 check types and no others:
   file_exists, file_absent, file_content (match: exact|trimmed_equals|
   contains|regex|sha256|line_count), dir_exists, dir_tree (names_only|
   names_and_hashes), file_mode, file_owner, symlink_target, env_var,
   cwd_is, process_running (with negate), command_matched,
   command_not_matched, script.
   (That's 14 counting script - correct, script is the escape hatch.)

3. Composition nodes: any_of, all_of, not.

4. Execution per docs/LEVEL-FORMAT.md section 4:
   - Run via Session.Exec in a fresh non-interactive shell, never the
     learner's shell.
   - Default user learner; script checks may set as_user: root.
   - env_var and cwd_is read the snapshot files written by PROMPT_COMMAND,
     NOT the check shell's own environment. This is essential - get it wrong
     and level 23 is unsolvable.
   - No short-circuiting: run all checks, return all results.
   - Per-check timeout 10s, per-level 60s; timeout is a distinct failure.
   - CHECKS MUST NOT MUTATE STATE. Add a test that hashes the sandbox
     filesystem before and after a full check run and asserts equality.

5. Result assembly matching docs/LEVEL-FORMAT.md section 5: per-objective statuses,
   a single primary_failure (first failing REQUIRED check), warnings as notes.

6. Implement level pipe-05 from docs/LEVEL-FORMAT.md §6 as a real level and
   use it as the reference test fixture.
```

---

## Day 3 · Session E - WSL runtime

```
Read CLAUDE.md, especially the Windows specifics.

Implement internal/runtime/wsl satisfying the same Runtime interface, and
make it pass the existing runtimetest contract suite unchanged.

1. Detection: parse `wsl -l -v` - NOTE it outputs UTF-16LE, decode it
   properly. Also `wsl --status` and `wsl --version`.

2. Provision:
   - Download rootfs.tar.gz from the pinned release URL to the cache dir.
   - Verify sha256 BEFORE import. Refuse on mismatch.
   - Check `wsl -l -q` for an existing `shellforge-sandbox`; if present and
     healthy, no-op (idempotent); if present and broken, offer rebuild.
   - `wsl --import shellforge-sandbox <installDir> <tarball> --version 2`
   - Write /etc/wsl.conf inside the distro:
       [automount] enabled=false
       [interop] enabled=false, appendWindowsPath=false
       [boot] systemd=false
       [user] default=learner
     then `wsl --terminate` so it takes effect.

3. Exec: `wsl -d shellforge-sandbox -u <user> --exec /bin/bash -lc <cmd>`,
   capturing stdout/stderr/exit separately.
   Attach: same but through the PTY multiplexer.
   PushFiles: write via `wsl -d ... -- tee` or a staged tar piped to stdin -
   do NOT write through /mnt/c (it's slow and permissions are fake).

4. Reset: wipe and recreate setup.root only (sub-second). No export/import.

5. Destroy: `wsl --unregister shellforge-sandbox` AND delete the install
   directory including the .vhdx. Verify the directory is gone.

6. Capabilities: systemd=false, networking=true, multiuser=true,
   snapshotting=false.

Test on real Windows 11. Report anything that doesn't work rather than
working around it silently.
```

---

## Day 3 · Session F - Doctor

```
Implement internal/doctor.

Each probe returns {ID, Status(ok|warn|fail), Detail, Remediation, DocAnchor}.
Probes: os_version, cpu_virtualization, vm_platform_feature, wsl_installed,
wsl_version_2, wsl_kernel_current, docker_present, docker_daemon_running,
disk_free (need 5GB), terminal_vt_support, windows_terminal, sandbox_health.

- `shellforge doctor` prints a readable table with ✅/⚠️/❌, and for each
  failure the exact command to run and a docs/ link.
- `--json` for bug reports.
- `--fix` performs ONLY reversible non-privileged remediation; anything
  needing admin is printed for the user to run, never executed.
- Every DocAnchor must correspond to a heading in docs/05-troubleshooting.md.
  Add a CI test asserting this.

Also add `shellforge bug-report`: zips doctor --json + logs + versions +
a REDACTED journal (commands only, no output) for GitHub issues.
```

---

## Day 4 · Session G - Game core

```
Implement the game layer. Read CLAUDE.md for the layer rule: internal/game
must not import any runtime implementation package.

1. internal/store: SQLite (modernc.org/sqlite, pure Go - no cgo), WAL mode,
   embedded forward-only migrations. Schema: profile, pack, level_state,
   attempt, event, concept_mastery, achievement. (Take the DDL from the
   architecture doc section 4.11; concept_mastery can be written but unused this
   week.)

2. internal/game/orchestrator: state machine
   IDLE→SETUP→BRIEFING→ACTIVE⇄CHECKING→PASSED/FAILED→TEARDOWN→IDLE.
   Owns setup/teardown, holds the reset lock, guarantees teardown runs even
   on abnormal exit.

3. internal/game/bus: synchronous event bus. Events: LevelStarted,
   CommandExecuted, CheckRun, HintTaken, LevelPassed, LevelReset,
   AchievementUnlocked. Every feature subscribes; nothing reaches into the
   orchestrator.

4. Curriculum DAG: unlock resolution (available when all prerequisites are
   passed or skipped), plus `shellforge map` printing a text tree grouped by
   act with ✅ passed / 🔓 available / 🔒 locked and XP per level.

5. Scoring:
   total = xp * difficulty_mult
           - sum(hint costs taken)
           + efficiency_bonus (if par_commands set and commands_used <= par)
           + first_try_bonus
           + optional_objective_bonus
   Persist best_score per level; re-plays can improve it.

6. Hints: `hint` shows the NEXT tier's cost and asks to confirm before
   deducting. `hint --reveal` jumps to the solution tier.

7. Achievements (journal-derived, all as bus subscribers):
   first_blood, tab_master (tab completion used 25x), manual_labour (man 20x),
   no_hints (clear an act hint-free), one_liner (5 levels at or under par),
   rm_survivor (triggered a reset after destroying level state), night_owl,
   act_clear_1..6, campaign_complete.

8. Output rendering (internal/ux): markdown briefing → ANSI, objective
   checklist with live ✅/⬜, pass/fail banners, XP and rank-up display,
   `shellforge stats`. Respect NO_COLOR and add --ascii.
```

---

## Day 5 · Session H - Content generation assist

Day 5 is mostly you writing. Use Claude Code for the mechanical parts:

```
Read docs/LEVEL-FORMAT.md and docs/CURRICULUM.md.

Generate the level YAML for levels <N> through <M> from docs/CURRICULUM.md,
plus the asset files they need under packs/core-linux-basics/assets/.

Requirements per level:
- Follow docs/LEVEL-FORMAT.md exactly. Every check needs a specific, actionable
  on_fail written in the voice of a helpful senior colleague - never
  "incorrect" or "try again".
- Setup must be fully deterministic: explicit `touch -d` for any mtime the
  level depends on, seeded generators, no network.
- 3-4 hint tiers escalating from nudge → concept → near-solution → reveal.
- Include the working `solution` block.
- Generate realistic asset files (logs, CSVs, configs) with the fictional
  Meridian Logistics framing. Assets must be internally consistent - if the
  level says the answer is 147, the generated log must actually contain 147
  matching lines. Verify by running the solution.

After generating, run `shellforge author validate` and `author test` on each
and fix anything that fails. Report any level where the solution doesn't
produce the expected check result rather than adjusting the check to match.
```

*(That last sentence matters - otherwise checks get quietly loosened until they pass, which is how you ship a level that accepts wrong answers.)*

---

## Day 6 · Session I - Golden tests, CI, packaging

```
1. `shellforge author test <level|--all>`: the golden test from
   docs/LEVEL-FORMAT.md section 7 - fresh sandbox, setup, assert required checks FAIL,
   run solution as learner in a login shell, assert required checks PASS,
   teardown, assert setup.root gone and no stray processes. Plus the purity
   check (filesystem hash identical after a double check run).

2. Mutation tests: for levels pipe-05, find-04, perm-01, files-04 and
   script-01, define known near-miss solutions (right answer wrong path,
   hardcoded literal, wrong case, correct file left in the wrong directory)
   and assert the level REJECTS each. This catches over-permissive checks.

3. .github/workflows/ci.yml: matrix ubuntu-latest + windows-latest.
   Ubuntu runs build, vet, unit tests, and golden tests via Docker.
   Windows runs build, unit tests, and golden tests via WSL2 (available on
   GH windows runners - enable it and import the rootfs artifact).
   Add the doc-anchor test from Session F.

4. .github/workflows/release.yml: build the container image, export
   rootfs.tar.gz, run GoReleaser for windows/amd64, linux/amd64,
   linux/arm64, darwin/amd64, darwin/arm64. Attach binaries, rootfs, and
   checksums to the GitHub Release. Pure-Go sqlite means no cgo, so
   cross-compilation stays trivial - keep it that way.

5. scripts/install.sh and scripts/install.ps1: detect OS/arch, fetch the
   latest release, VERIFY THE CHECKSUM, install to a sane location
   (~/.local/bin or %LOCALAPPDATA%\Programs\shellforge), add to PATH,
   then run `shellforge doctor` and print next steps.
   install.ps1 must not require an execution-policy bypass beyond
   `-Scope Process`.
```

---

## Day 7 · Session J - Documentation

```
Write the user-facing documentation. Audience: a first-year CS student on
Windows 11 who has never opened a terminal and is worried about breaking
their laptop. Warm, plain, zero jargon without explanation.

README.md - hook, demo GIF placeholder, what it teaches (the six acts),
60-second quickstart, the safety explanation (why it can't hurt your
computer), install links per OS, contributing, license.

docs/01-install-windows.md - the most important file in the repo.
Prerequisites explained (what WSL even is, in two sentences), enabling
virtualization, installing WSL, installing Windows Terminal, running
install.ps1, running doctor, first run. Screenshot placeholders marked
[SCREENSHOT: ...]. Every doctor failure code gets a heading in
05-troubleshooting.md that this file links to.

docs/02-install-linux.md, 03-quickstart.md (the check/hint/reset loop and
every keybind/command), 04-how-it-works.md (what the sandbox is, where files
live, why rm -rf is safe, what data is stored locally and that nothing is
sent anywhere), 05-troubleshooting.md (one anchored heading per doctor code
plus the top 10 predictable problems), 06-uninstall.md (complete cleanup
including wsl --unregister and deleting the vhdx - be explicit, orphaned
2GB files generate angry issues), 07-authoring-levels.md (derived from
docs/LEVEL-FORMAT.md, for contributors).

Then CONTRIBUTING.md and a CI check that every doc anchor referenced in Go
code actually exists.
```

---

## Prompt patterns worth reusing

**When something is subtly broken:**
```
The OSC parser drops markers when output is heavy. Before changing anything:
reproduce it with a failing test, explain the root cause, and propose two
fixes with tradeoffs. Don't implement until I pick one.
```

**Before merging a day's work:**
```
Self-review the diff against CLAUDE.md. Report: any layer-dependency
violations, any new dependency, any user-facing error lacking a remediation,
any check that could mutate state, any place a level's checks could accept a
wrong answer. List issues by severity; don't fix yet.
```

**When behind schedule:**
```
Given the remaining tasks for Day N and roughly X hours left, propose a cut
list ordered by (user-visible impact) / (hours saved), and identify anything
that would become significantly more expensive if deferred past this week.
```
