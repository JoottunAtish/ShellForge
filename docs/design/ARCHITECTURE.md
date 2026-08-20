# Shellforge System Architecture

> **Working name:** `shellforge` (placeholder - swap freely).
> **One-line:** A standalone, offline-first, cross-platform game that teaches basic→intermediate Linux command line by dropping the learner into a real, disposable Linux shell and verifying real system state.

---

## 0. Scope, Goals, Non-Goals

### 0.1 Goals

| # | Goal | Why it constrains the design |
|---|------|------------------------------|
| G1 | Teach **real** command line, not a simulation | Rules out a JS "fake shell". We must run an actual kernel-backed shell. |
| G2 | Standalone, single-binary install | Rules out "needs Python 3.11 + 40 pip packages". Compiled binary + embedded assets. |
| G3 | Works on Windows via WSL | Requires a WSL provisioning path, plus a Docker-on-WSL2 path. |
| G4 | Offline after first install | Content packs must be embedded or cached; no runtime API calls required. |
| G5 | Destructive-safe | Learner must be able to `rm -rf /` and lose nothing. Requires isolation + reset. |
| G6 | Reproducible tasks | Every learner sees byte-identical starting state for a level. |
| G7 | Content authored by non-core-devs | Levels are declarative data (YAML), not Go/Rust code. |
| G8 | Game-like, not quiz-like | Progression, feedback loops, narrative, scoring - see §8. |

### 0.2 Non-Goals (v1)

- Not a multi-user server / SaaS. No accounts, no backend.
- Not a container security boundary against a malicious *user* - the sandbox protects the user *from themselves*, not the host from an attacker. (The attacker and the victim are the same person.)
- Not teaching kernel internals, C, or distro packaging.
- No cloud sync, no leaderboards requiring a server (v1 leaderboards are local/file-based).

### 0.3 Assumptions

- Learner has admin rights on their machine (needed for WSL/Docker install).
- Learner has ≥4 GB RAM, ≥5 GB disk.
- Target platforms: Windows 10 2004+/11 (x64, arm64), Linux (x64, arm64), macOS (Intel, Apple Silicon).

---

## 1. User Journey (the thing the docs must describe)

```
1. Read README on GitHub
2. Run `install.ps1` (Win) / `install.sh` (Linux/macOS)  →  places `shellforge` binary on PATH
3. `shellforge doctor`        →  diagnoses WSL/Docker/virtualization; prints exact fix commands
4. `shellforge init`          →  provisions the sandbox (imports rootfs / pulls image), ~2-4 min, one time
5. `shellforge play`          →  TUI opens: campaign map, mission briefing, live shell
6. Loop: read briefing → type commands in a real shell → `check` → pass/fail feedback → XP → next
7. `shellforge reset <level>` →  nukes and rebuilds level state
8. `shellforge stats` / `shellforge sync-content` / `shellforge uninstall`
```

**Design rule:** every step above is a first-class CLI verb with its own failure diagnostics. `doctor` is not an afterthought - it is the single largest source of support burden avoided.

---

## 2. Platform Matrix

| Host | Primary runtime | Fallback | Notes |
|------|-----------------|----------|-------|
| Windows 11/10 2004+ | **Dedicated WSL2 distro** (`wsl --import`) | Docker Desktop (WSL2 backend) | No Docker install required in primary path. |
| Windows w/ WSL1 only | Docker Desktop | - | WSL1 lacks real namespaces, `inotify`, and has broken `/proc`. Refuse, or warn hard. |
| Linux | **Rootless Podman / Docker** | `bubblewrap` namespaces | No daemon needed with Podman. |
| macOS (Intel/ARM) | **Docker Desktop / Colima / Lima** | - | No native Linux kernel; a VM is mandatory. |
| Any, no virtualization | **Degraded mode** (see §4.3.E) | - | Chroot-less scratch dir; ~60% of levels usable. |

---

## 3. High-Level Architecture

```
┌──────────────────────────────────────────────────────────────────────────┐
│ L5  PRESENTATION                                                         │
│     TUI (campaign map · briefing pane · embedded terminal · HUD)          │
│     Alt front-ends: plain-CLI mode, optional local web UI                 │
├──────────────────────────────────────────────────────────────────────────┤
│ L4  GAME CORE                                                            │
│     Session Orchestrator · Curriculum DAG · Progression/Scoring ·         │
│     Hint Director · Achievement Engine · Event Bus                        │
├──────────────────────────────────────────────────────────────────────────┤
│ L3  CONTENT + VERIFICATION                                               │
│     Content Pack Loader/Validator · Setup-Teardown Runner ·               │
│     Verification Engine (check catalogue + assertion DSL)                 │
├──────────────────────────────────────────────────────────────────────────┤
│ L2  INSTRUMENTATION                                                      │
│     PTY Multiplexer · OSC-133/OSC-7 parser · Command Journal ·            │
│     Env/CWD Snapshotter · Output Ring Buffer                              │
├──────────────────────────────────────────────────────────────────────────┤
│ L1  RUNTIME ABSTRACTION  (trait/interface: `Runtime`)                     │
│     DockerRuntime · PodmanRuntime · WslRuntime · BwrapRuntime ·           │
│     NativeScratchRuntime · SshRuntime(future)                             │
├──────────────────────────────────────────────────────────────────────────┤
│ L0  PERSISTENCE + PLATFORM                                               │
│     SQLite (progress, journal, achievements) · Config (TOML) ·            │
│     Content cache · Logs · Platform probes (doctor)                       │
└──────────────────────────────────────────────────────────────────────────┘
```

**Dependency rule:** strictly downward. L4 never imports Docker types; it only knows `Runtime`. This is what makes "swap WSL for Docker" a config change rather than a rewrite.

---

## 4. Component Specifications

### 4.1 CLI / Launcher

**Responsibility:** argument parsing, config resolution, subcommand dispatch, global error rendering.

**Commands:**

| Command | Purpose |
|---|---|
| `doctor [--fix]` | Preflight diagnostics; `--fix` runs safe auto-remediation |
| `init [--runtime=auto\|wsl\|docker\|podman\|native]` | Provision sandbox |
| `play [level-id]` | Launch TUI (default: resume) |
| `run <level-id>` | Headless single level (plain CLI mode) |
| `check` | Run verification for current level (also available *inside* the sandbox shell as a shim) |
| `hint [n]` | Reveal next hint tier |
| `reset [level-id\|--all]` | Rebuild level state |
| `skip` | Mark skipped, advance |
| `map` / `stats` / `achievements` | Progress views |
| `content list\|install\|update\|verify` | Content pack management |
| `sandbox shell\|status\|rebuild\|destroy` | Escape hatches / debugging |
| `author validate\|test\|scaffold` | Level authoring toolkit (§4.14) |
| `export --format=json` | Dump progress (portfolio / instructor use) |

**Config resolution order:** CLI flag → env `SHELLFORGE_*` → `$XDG_CONFIG_HOME/shellforge/config.toml` → built-in defaults.

**Paths:**
- Linux/macOS: `~/.config/shellforge/`, `~/.local/share/shellforge/`, `~/.cache/shellforge/`
- Windows: `%APPDATA%\shellforge\`, `%LOCALAPPDATA%\shellforge\`

---

### 4.2 Doctor / Preflight Subsystem

The single highest-ROI component. Each probe returns `{status: ok|warn|fail, detail, remediation, doc_anchor}`.

**Probe list:**

| Probe | Method | Remediation emitted |
|---|---|---|
| OS + build | `GetVersion`/`uname` | "Requires Win10 build 19041+" |
| CPU virtualization | `systeminfo` / `Get-ComputerInfo`, `/proc/cpuinfo` vmx\|svm | "Enable Intel VT-x / AMD-V in BIOS" |
| Hyper-V / VMP feature | `Get-WindowsOptionalFeature VirtualMachinePlatform` | exact `dism` command |
| WSL installed | `wsl --status`, exit code | `wsl --install --no-distribution` |
| WSL version | `wsl -l -v` | `wsl --set-default-version 2` |
| WSL kernel current | `wsl --version` | `wsl --update` |
| Docker present + daemon up | `docker version --format {{.Server.Version}}` | link to Docker Desktop |
| Podman rootless ready | `podman info`, subuid/subgid map | `usermod --add-subuids` |
| Disk free | statfs | "need 5 GB in `<path>`" |
| Terminal ANSI/VT support | `SetConsoleMode` probe / `TERM` | "Use Windows Terminal" |
| Unicode/font | render probe glyphs | "Install a Nerd Font or use `--ascii`" |
| Existing sandbox health | runtime ping | `sandbox rebuild` |

Output: human table by default, `--json` for CI/support. `doctor --fix` only performs *reversible, non-privileged* actions; anything requiring admin prints the command for the user to run.

---

### 4.3 Runtime Abstraction Layer ★ (the key decision)

```go
// L1. internal/runtime. Types only; implementations live in subpackages.
type Runtime interface {
	Provision(ctx context.Context, spec ImageSpec) error // one time, idempotent
	Destroy(ctx context.Context) error
	Status(ctx context.Context) (Status, error)
	StartSession(ctx context.Context, spec SessionSpec) (Session, error)
	Capabilities() Caps // {Networking, Systemd, MultiUser, Snapshotting, Privileged}
}

type Session interface {
	Exec(ctx context.Context, argv []string, opts ExecOpts) (ExecResult, error) // non-interactive probe
	Attach(ctx context.Context, opts AttachOpts) (PTY, error)                   // interactive user shell
	PushFiles(ctx context.Context, m FileManifest) error
	PullFile(ctx context.Context, path string) ([]byte, error)
	Close() error
}
```

The code block above is the interface as implemented in issue #6, not the
original sketch from this design document; that sketch was replaced in place
and survives only in the file history (`git log -p` on this file). The code
is now the authority, and it changed from that superseded sketch as follows:

- `Session` is its own interface. A session has a lifetime and a `Close`; a
  runtime does not.
- `Exec` returns one `ExecResult` rather than four values, so it can also carry
  `Duration` and `TimedOut`. The streams stay separate inside it, because a
  combined output field makes a precise script-check diagnostic impossible.
- `RuntimeStatus` is `Status` and `PtyHandle` is `PTY`, matching the initialism
  rule in the go-style skill. `PTY` is declared at L1 so no runtime has to
  import L2.
- `Snapshot` and `Restore` are gone. v0.1 resets by wiping the scratch
  directory on both backends, so keeping them would only mean two
  implementations of `ErrNotSupported`. Add them when a caller exists.
- `Caps.Cgroups` is gone. No level declares it and no backend reports it.

`Capabilities()` is essential: a level declares `requires: [systemd, networking]` and the loader filters/greys-out levels the current runtime can't support. This is how you degrade gracefully instead of failing mysteriously.

#### A. `WslRuntime` - Windows primary path

**Provisioning:**
```powershell
wsl --import shellforge-sandbox "%LOCALAPPDATA%\shellforge\wsl" rootfs.tar.gz --version 2
```
- `rootfs.tar.gz` is a ~40-120 MB minimal Debian/Alpine rootfs built in CI, shipped as a GitHub release asset (checksummed, verified before import).
- Creates a **dedicated distro** - the learner's existing Ubuntu/other distro is untouched.

**Hardening `/etc/wsl.conf` inside the sandbox** (critical for teaching quality):
```ini
[automount]
enabled = false          # no /mnt/c - learner can't nuke their Windows files, and `ls /` is clean
[interop]
enabled = false          # can't launch notepad.exe from bash
appendWindowsPath = false # PATH isn't polluted with 300 Windows entries
[boot]
systemd = false          # (or true if you teach systemctl; costs boot time)
[user]
default = learner
```

**Reset strategy:** WSL has no native snapshots. Two options:
1. `wsl --export` / `wsl --import` round-trip - slow (10-60 s), full fidelity. Use for "reset everything".
2. **Overlay scratch** - level state lives entirely under `/home/learner/quest/`, and reset = `rm -rf` + re-run setup. Fast (<1 s). **Default.**
3. Optional: back the scratch dir with a loop-mounted ext4 image + `cp` of a pristine image file. Middle ground.

**Gotchas:** listed in §10.

#### B. `DockerRuntime` / `PodmanRuntime`

- Image built in CI, published to GHCR, pinned by digest; also shipped as an OCI tarball for air-gapped `docker load`.
- Container runs `sleep infinity` as PID 1; the shell is `docker exec -it`.
- **Reset = `docker rm -f` + `docker run`** from the pristine image - genuinely instant and perfectly reproducible. This is the strongest reset story.
- Snapshots via `docker commit` (for "checkpoint before boss fight" / undo).
- `--network none` by default; levels that teach `curl`/`ping` declare `requires: [networking]` and get a bridge with an in-sandbox mock server.
- Podman rootless preferred on Linux (no daemon, no root).

#### C. `BwrapRuntime` (Linux, no container engine)

`bubblewrap` + user namespaces: bind a scratch dir as `/`, `--unshare-all`, tmpfs overlays. Zero install beyond `bubblewrap` (in every distro repo). Cheap and fast, but weaker caps (no systemd, awkward package installs).

#### D. `SshRuntime` (future)

Points at a remote VM/Pi. Same interface. Unlocks classroom/lab deployment and "practice on a real server" levels.

#### E. `NativeScratchRuntime` (degraded fallback)

No isolation: runs commands in the user's own shell, confined *by convention* to `~/.local/share/shellforge/scratch/`. Only levels tagged `safe_native: true` are offered. Destructive, permissions, user-management, and systemd levels are hidden. Ship it, warn loudly, and treat it as "better than nothing" for locked-down corporate laptops.

#### Comparison

| | WSL-import | Docker/Podman | bwrap | Native |
|---|---|---|---|---|
| Extra install | WSL only | Container engine | `bubblewrap` | none |
| Reset speed | fast (scratch) | instant (recreate) | fast | n/a |
| Reproducibility | high | **highest** | medium | low |
| Systemd | optional | needs work | no | host's |
| Windows support | **native** | via WSL2 | no | poor |
| Disk | ~1-2 GB | ~1-2 GB | ~300 MB | ~0 |
| Recommended | **Win default** | **Linux/macOS default** | Linux alt | last resort |

---

### 4.4 Sandbox Image Specification

Built by a single `Containerfile`, and the *same* build exports the WSL rootfs tarball (`docker export` → tar). One source of truth, two artifacts.

**Contents:**
- Base: Debian slim (better for `apt` teaching) - Alpine variant optional but `busybox` utils diverge from GNU coreutils and will teach wrong flags. **Use GNU userland.**
- Users: `learner` (uid 1000, sudo NOPASSWD via a group toggled per-level), `root`.
- Toolset: coreutils, findutils, grep/sed/gawk, less, nano, vim-tiny, tmux, tree, jq, curl, git, procps, psmisc, htop, man-db + man pages (**do not strip man pages** - teaching `man` is a level), bash-completion, cron, openssh-client.
- Game mount: `/opt/shellforge/` read-only - contains `instrument.bash`, the `check`/`hint`/`brief` shims, and the level payloads.
- Home skeleton: `/etc/skel` with a pristine `.bashrc` that sources the instrumentation via `--rcfile`.
- Locale pinned `C.UTF-8`, `TZ=UTC`, `HISTCONTROL` unset (we need every command), `HISTFILE` per-level.
- Deterministic: pinned package versions or a snapshot.debian.org source, so the image is byte-stable across rebuilds.

---

### 4.5 Session Orchestrator (L4)

Owns the state machine per level attempt:

```
IDLE → PROVISIONING → SETUP → BRIEFING → ACTIVE ⇄ CHECKING
                                              ↓         ↓
                                          HINTING    PASSED / FAILED
                                              ↓         ↓
                                          ACTIVE     TEARDOWN → IDLE
```

Responsibilities:
- Resolve next level from the Curriculum DAG + learner mastery.
- Drive setup/teardown, guarantee idempotency, hold the reset lock.
- Emit domain events onto the Event Bus (`LevelStarted`, `CommandExecuted`, `CheckRun`, `HintTaken`, `LevelPassed`, `AchievementUnlocked`) - every game mechanic in §8 subscribes here rather than being wired into the core. **This is what makes game features pluggable.**

---

### 4.6 PTY Multiplexer & Shell Instrumentation ★ (the hard part)

The problem: we need to know *what the learner typed, when, its exit code, and its output* - without breaking `vim`, `less`, `htop`, job control, Ctrl-C, tab completion, or resizing.

#### Rejected approaches

| Approach | Why rejected |
|---|---|
| Custom REPL that reads a line and calls `bash -c` | Kills interactivity (no vim/less), no job control, no real readline. Teaches a fake shell. |
| Parse `~/.bash_history` after the fact | No exit codes, no timing, no ordering with output, written on exit only. |
| `script(1)` typescript capture | Raw ANSI soup; command boundaries ambiguous. |
| `trap DEBUG` alone | Fires per-simple-command inside pipelines/functions; extremely noisy; still no clean boundaries. |
| `auditd` / eBPF `execsnoop` | Needs privileges, misses builtins (`cd`, `export`), platform-specific. |

#### Chosen approach: real `bash` in a PTY + **OSC 133 semantic prompts**

The game owns the master side of a PTY. Inside, it launches:
```
bash --rcfile /opt/shellforge/rc/instrument.bash -i
```
The rc file injects terminal escape markers that the game's parser strips out of the byte stream before rendering:

```bash
# /opt/shellforge/rc/instrument.bash   (read-only, bind-mounted)
[ -f /etc/bash.bashrc ] && . /etc/bash.bashrc
[ -f "$HOME/.bashrc" ] && . "$HOME/.bashrc"

SF_JOURNAL="${SF_STATE:-/opt/shellforge/state}/journal.tsv"
SF_ENV="${SF_STATE:-/opt/shellforge/state}/env.snapshot"

# OSC 133 semantic markers:
#   A = prompt start   B = command input start
#   C = pre-execution  D;<exit> = command finished
PS1='\[\e]133;A\a\]'"${PS1:-\u@quest:\w\$ }"'\[\e]133;B\a\]'
PS0='\e]133;C\a'

__sf_after() {
  local ec=$?
  printf '\e]133;D;%s\a' "$ec"
  printf '\e]7;file://quest%s\a' "$PWD"          # OSC 7 : report cwd
  local line; line=$(HISTTIMEFORMAT='' history 1); line=${line#*[0-9]  }
  printf '%s\t%s\t%s\t%s\n' "$EPOCHREALTIME" "$ec" "$PWD" "$line" >> "$SF_JOURNAL"
  env -0 > "$SF_ENV"                              # so checkers can inspect the *interactive* env
  return $ec
}
PROMPT_COMMAND='__sf_after'

export HISTFILE="${SF_STATE}/bash_history" HISTSIZE=100000 HISTFILESIZE=100000
shopt -s histappend
export PATH="/opt/shellforge/bin:$PATH"           # provides `check`, `hint`, `brief`, `reset`
readonly PROMPT_COMMAND PS0 SF_JOURNAL            # mild anti-tamper
```

**The multiplexer** sits between the host terminal and the PTY:

```
host stdin ──► [keystroke tap] ──► PTY master ──► bash
host stdout ◄── [ANSI/OSC parser: strip 133/7, forward rest] ◄── PTY master
                      │
                      ├─► Command Journal (structured events)
                      ├─► Output Ring Buffer (last N KB per command, for output_contains checks)
                      └─► Event Bus (CommandExecuted{cmd, exit, cwd, duration})
```

Also handles `SIGWINCH` → `TIOCSWINSZ` propagation (resizing must work or `vim` corrupts), and raw-mode toggling on the host terminal.

**Why journal *and* stream?** The stream gives ordering + output; the on-disk `journal.tsv` gives the verifier a source of truth that survives a crash and is readable from inside the sandbox by check scripts. Belt and braces.

**Windows note:** enable `ENABLE_VIRTUAL_TERMINAL_PROCESSING` + `ENABLE_VIRTUAL_TERMINAL_INPUT` on the console handles, or require Windows Terminal.

#### Optional: keystroke-level signals

Tapping raw stdin (before the PTY) lets you detect `Tab` (completion), `↑` (history), `Ctrl-R`, `Ctrl-C` - which powers achievements ("Completionist: solve a level using tab-completion for every path") and better hints ("you retyped that path 4 times - try Tab"). Store as low-cardinality counters, never raw keystrokes (avoid capturing anything typed into a password prompt).

---

### 4.7 Command Journal & Snapshotter

**Journal record:**
```json
{ "seq": 42, "ts": 1765432100.123, "level": "pipes-03", "cwd": "/home/learner/quest",
  "raw": "grep -ri 'error' logs/ | wc -l", "exit": 0, "duration_ms": 84,
  "output_sha256": "...", "output_head": "17\n", "output_bytes": 3,
  "used_tab": true, "used_history": false }
```

Stored in SQLite (`events` table) **and** appended to `journal.tsv` inside the sandbox. The journal is the substrate for: command-pattern checks, hint targeting, replay/"watch your solution", speedrun scoring, and analytics.

The shipped `events` table (`internal/store/schema/001_init.sql`) matches this record with two renames, `level_id` for `level` and `exit_code` for `exit`, needed because `exit` reads as a verb next to SQL keywords and `level` alone is ambiguous next to a future foreign key. `output_sha256`, `output_head` and `output_bytes` are not persisted today: command output capture is at least as sensitive as the command text and needs its own privacy review before it is designed, not implemented as a side effect of a schema reconciliation, and no ticket exists yet for that work. Section 4.11 used to describe this same table a second, incompatible way; issue #87 reconciled the two, and 4.11's copy below is now the shipped shape.

**Env/CWD snapshot** solves a subtle problem: verification probes run in a *separate* `exec`, so they cannot see the interactive shell's `export FOO=bar`. `env -0` dumped each prompt makes env-var checks possible. Same trick for `alias` (`alias -p`), functions (`declare -F`), and shell options (`shopt -p`).

---

### 4.8 Content Pack Format ★

A **pack** is a directory (or a `.tar.zst`) with a manifest and levels. Packs are the extension point: community packs, instructor packs, language packs.

```
packs/core-linux-basics/
├── pack.yaml                 # id, version, author, license, engine_semver, level order
├── assets/                   # images/ascii art, sample data files
├── levels/
│   ├── 010-navigation-pwd.yaml
│   ├── 020-navigation-cd.yaml
│   └── ...
└── tests/                    # golden solutions (see §11)
```

**Level schema (annotated, complete):**

```yaml
id: pipes-03
version: 1
title: "The Log Sifter"
pack: core-linux-basics
chapter: "Chapter 4 - Plumbing"
difficulty: 3                      # 1..5
xp: 120
estimated_minutes: 6

concepts: [pipes, grep, wc, redirection]     # → mastery model, spaced repetition
prerequisites: [pipes-01, grep-02]           # → curriculum DAG edges
requires: [networking]                       # → Runtime.Capabilities() gate; omit if none

briefing: |                                  # markdown, rendered in the left pane
  ## Night shift, 02:41
  The billing service is throwing errors and nobody knows how many.
  `/home/learner/quest/logs/` holds today's rotated logs.

  **Objective:** write the *number* of lines containing `ERROR`
  (case-insensitive, across all files) into `~/quest/report.txt`.

objectives:                                   # shown as a live checklist in the HUD
  - id: obj1
    text: "report.txt exists in the quest directory"
  - id: obj2
    text: "It contains only the correct count"
  - id: obj3
    text: "You solved it in one pipeline (bonus)"
    optional: true

setup:
  files:                                      # deterministic, hashed, no network
    - path: /home/learner/quest/logs/app-1.log
      source: assets/app-1.log
      mode: "0644"
    - path: /home/learner/quest/logs/app-2.log
      source: assets/app-2.log
  script: |                                   # runs as root, before handing over
    chown -R learner:learner /home/learner/quest
  seed: 91731                                 # for procedural levels; makes randomness reproducible

checks:
  - id: obj1
    type: file_exists
    path: /home/learner/quest/report.txt
    on_fail: "I don't see report.txt yet. Check the path with `ls ~/quest`."

  - id: obj2
    type: file_content
    path: /home/learner/quest/report.txt
    match: trimmed_equals
    value: "147"
    on_fail: "The file exists but the number is off. Are you searching *all* log files? Is your grep case-insensitive?"

  - id: obj3
    optional: true
    type: command_matched
    scope: level                              # search whole journal for this level
    pattern: '^\s*grep\s+.*\|\s*wc\s+-l'
    on_fail: "You got there - but there's a one-liner. Try `grep ... | wc -l`."

  - id: no_cheating                           # negative / anti-pattern check
    type: command_not_matched
    pattern: 'echo\s+147'
    severity: warn
    on_fail: "Hardcoding the answer works, but you won't learn the pipeline. Try again for full XP."

hints:                                        # progressive ladder, each costs score
  - cost: 5
    text: "`grep` can search many files at once: `grep pattern file1 file2`."
  - cost: 10
    text: "`-r` recurses into a directory; `-i` ignores case."
  - cost: 15
    text: "`wc -l` counts lines fed to it on stdin. What connects two commands?"
  - cost: 40
    reveal_solution: true

solution: |                                   # used by CI golden tests + "show me"
  grep -ri ERROR ~/quest/logs/ | wc -l > ~/quest/report.txt

teardown:
  script: rm -rf /home/learner/quest

tags: [text-processing, must-know]
```

**Loader/validator** enforces: unique IDs, DAG has no cycles, referenced assets exist, all `on_fail` present, `solution` present, semver compatibility with the engine, and that every check `type` exists in the catalogue. `shellforge author validate` runs this in CI.

---

### 4.9 Setup / Teardown / Reset

- **Setup is a transaction.** Materialize files into a staging dir → checksum → atomic move → run setup script → mark `SETUP_OK` sentinel. If any step fails, roll back and surface a clean error.
- **Idempotent by construction:** teardown always runs before setup (`rm -rf` the level root).
- **Reset tiers:**
  - `reset` (soft) - teardown + setup for the current level. Sub-second.
  - `reset --hard` - restore runtime snapshot / recreate container.
  - `sandbox rebuild` - full reprovision from image.
- **Snapshots for checkpointing:** `docker commit` or a tar of the level root, taken at `LevelStarted` and before any check. Enables an in-game "rewind" mechanic (§8.11) and "you broke it, want to undo?" recovery.
- **Determinism:** never fetch from the network during setup. Everything is an embedded asset or a seeded generator.

---

### 4.10 Verification Engine ★

**Execution model:** checks run via `Runtime.Exec()` in a *separate*, non-interactive shell as a chosen user, so they never pollute the learner's history, env, or job table. They read the env/journal snapshots (§4.7) for anything shell-local.

**Check catalogue (v1):**

| Category | Types |
|---|---|
| Filesystem | `file_exists`, `file_absent`, `file_content` (exact / trimmed_equals / contains / regex / sha256 / json_path / line_count), `dir_exists`, `dir_tree` (structural match), `file_mode`, `file_owner`, `symlink_target`, `hardlink_count`, `file_size`, `mtime_within`, `is_empty` |
| Process/system | `process_running`, `process_not_running`, `port_listening`, `service_active` (systemd), `cron_entry_exists`, `user_exists`, `group_membership`, `package_installed`, `disk_usage_under` |
| Shell state | `env_var`, `alias_defined`, `function_defined`, `shopt_set`, `cwd_is`, `last_exit_code` |
| Journal/behavioural | `command_matched`, `command_not_matched`, `command_count_under` (efficiency), `used_tool` (e.g. must use `find`, not `ls -R`), `no_editor_used`, `solved_within_seconds` |
| Output | `output_contains`, `output_matches`, `stdout_equals_for` (re-runs a canonical command and diffs) |
| Escape hatch | `script` - arbitrary bash; **exit 0 = pass**; stdout captured as the failure message |

**Assertion composition:** each check is an object; the level's `checks` array is an implicit AND over non-optional checks. Add `any_of` / `all_of` / `not` wrapper nodes for complex cases:

```yaml
checks:
  - any_of:                     # multiple valid solutions
      - { type: file_content, path: /tmp/out, match: regex, value: '^17$' }
      - { type: file_content, path: /tmp/out, match: regex, value: '^seventeen$' }
```

**Design principles:**
1. **Check state, not syntax, by default.** `command_matched` is for *style/bonus* objectives only. Otherwise you punish learners who found a better solution - the fastest way to kill a teaching tool.
2. **Every failing check must produce a targeted, actionable message.** Generic "❌ Task failed" is a bug. The `on_fail` field is mandatory in the validator.
3. **Partial credit.** Return per-objective results so the HUD checklist updates ✅/❌ live, not a single boolean.
4. **Timeouts + no side effects.** Checks are read-only; a check that mutates state is a validator error (enforced by running checks twice in CI and diffing state).

**Optional live mode:** poll checks every ~2 s (or on `CommandExecuted`) so objectives tick green *as the learner works*. Enormously satisfying - this alone makes it feel like a game rather than homework. Guard with debounce + cheap-checks-only.

---

### 4.11 Progression, Scoring, Persistence

**Curriculum DAG:** nodes = levels, edges = prerequisites. The "map" screen is a rendered DAG. Unlock rule: node available when all prereqs are `passed` (or `skipped`, if `soft_prereq: true`).

**Mastery model (per concept):** track attempts, hints used, time, and retention. Simple and effective: an SM-2-style scheduler per concept for a **Review** queue - "you learned `find -exec` 6 days ago; here's a 90-second drill." This is the single highest-impact learning feature and costs ~200 lines.

**Score formula (tunable in config):**
```
score = base_xp
        × difficulty_multiplier
        × (1 - hint_penalty)                 # Σ hint costs, capped
        × efficiency_bonus                   # f(commands_used / par_commands)
        × time_bonus                         # only above a generous threshold; never punish thinking
        + first_try_bonus + optional_objective_bonus
```
Never let time pressure be mandatory - it's a per-mode toggle (§8.4).

**SQLite schema:**

```sql
CREATE TABLE schema_version (version INTEGER NOT NULL);

CREATE TABLE profile (
  id INTEGER PRIMARY KEY, name TEXT, created_at INTEGER,
  settings_json TEXT
);

CREATE TABLE pack (
  id TEXT PRIMARY KEY, version TEXT, source TEXT, sha256 TEXT, installed_at INTEGER
);

CREATE TABLE level_state (
  profile_id INTEGER, level_id TEXT, pack_id TEXT,
  status TEXT CHECK(status IN ('locked','available','in_progress','passed','skipped')),
  best_score INTEGER, attempts INTEGER DEFAULT 0,
  hints_used INTEGER DEFAULT 0, commands_used INTEGER DEFAULT 0,
  first_passed_at INTEGER, last_attempt_at INTEGER, total_seconds INTEGER,
  PRIMARY KEY (profile_id, level_id)
);

CREATE TABLE attempt (
  id INTEGER PRIMARY KEY, profile_id INTEGER, level_id TEXT,
  started_at INTEGER, ended_at INTEGER, outcome TEXT,
  score INTEGER, hints_used INTEGER, commands_used INTEGER
);

-- events is the command journal, shipped in 001_init.sql and owned by
-- section 4.7. Flat and typed: see the note below for why, not the
-- payload_json shape this section used to show.
CREATE TABLE events (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  seq          INTEGER NOT NULL, ts REAL NOT NULL,
  level_id     TEXT NOT NULL, cwd TEXT NOT NULL, raw TEXT NOT NULL,
  exit_code    INTEGER NOT NULL, duration_ms INTEGER NOT NULL,
  used_tab     INTEGER NOT NULL DEFAULT 0,
  used_history INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE concept_mastery (
  profile_id INTEGER, concept TEXT, ease REAL DEFAULT 2.5,
  interval_days REAL DEFAULT 0, reps INTEGER DEFAULT 0,
  last_seen_at INTEGER, due_at INTEGER,
  PRIMARY KEY (profile_id, concept)
);

CREATE TABLE achievement (
  profile_id INTEGER, key TEXT, unlocked_at INTEGER, progress REAL,
  PRIMARY KEY (profile_id, key)
);

CREATE INDEX idx_events_level_seq ON events(level_id, seq);
CREATE INDEX idx_mastery_due ON concept_mastery(profile_id, due_at);
```

This table used to appear here a second, incompatible way: singular `event`,
keyed by `attempt_id`, carrying a `kind` and an opaque `payload_json` rather
than the flat typed columns above. Issue #87 reconciled the two in favour of
what #51 actually shipped in `internal/store/schema/001_init.sql`, which this
block now matches column for column. `attempt_id` and `kind`, the pair that
would turn this into a heterogeneous log spanning commands, checks, hints,
achievements and resets, arrive with the Day 4 attempt model in a
forward-only `002_*.sql`, once `profile`, `level_state` and `attempt` exist
to give `attempt_id` something to reference and `kind` something to
disambiguate. Until then this table is command-only, which is what
`internal/journal` actually implements and tests.

WAL mode on; migrations versioned and forward-only.

---

### 4.12 Hint Director

Not just "reveal next hint". A ladder plus a **diagnostic** layer:

1. **Tiered hints** (authored) - nudge → concept → near-solution → solution. Each has an XP cost, shown *before* the learner commits.
2. **Reactive hints** (rule-based, derived from the journal):
   - `command_not_found: grep` → "not installed / typo?"
   - Same command failing 3× with exit 1 → surface `man` excerpt for the flag used.
   - `Permission denied` twice → nudge toward `ls -l` and `sudo`.
   - Learner idle >90 s with no commands → "stuck? `hint` costs 5 XP."
   - Path typo detected (Levenshtein ≤2 from an existing path) → "did you mean ...?"
3. **`explain <command>`** - an in-sandbox shim that decomposes a command into its parts (like explainshell) from a local flag database. Offline, no LLM required.
4. **Optional LLM tutor (v2, opt-in):** if the learner supplies an API key, a `hint --ask` mode sends `{briefing, objectives, last N journal entries, current check failures}` and gets a Socratic nudge. **Must be opt-in, off by default, and the game must be fully playable without it** (G4: offline-first).

---

### 4.13 Presentation Layer

**Primary: TUI.** Recommended layout (responsive; collapses to stacked panes under 100 cols):

```
┌ Shellforge ─────────────────────────── Ch.4 · The Log Sifter · ⭐⭐⭐☆☆ ─┐
│ ┌─ BRIEFING ────────────────┐ ┌─ SHELL ───────────────────────────────┐ │
│ │ Night shift, 02:41        │ │ learner@quest:~/quest$ ls logs/       │ │
│ │ The billing service is    │ │ app-1.log  app-2.log                  │ │
│ │ throwing errors...        │ │ learner@quest:~/quest$ █              │ │
│ │                           │ │                                       │ │
│ │ OBJECTIVES                │ │                                       │ │
│ │  ✅ report.txt exists      │ │                                       │ │
│ │  ⬜ correct count          │ │                                       │ │
│ │  ⬜ (bonus) one pipeline   │ │                                       │ │
│ └───────────────────────────┘ └───────────────────────────────────────┘ │
│ XP 1,240  🔥7-day streak   ⏱ 02:14   Hints 0/4                          │
│ [F1 hint] [F2 check] [F3 map] [F5 reset] [F10 quit]                     │
└─────────────────────────────────────────────────────────────────────────┘
```

Notes:
- The shell pane hosts the real PTY. Function keys are intercepted by the multiplexer *before* the PTY so they never reach `vim`. Provide `--no-hotkeys` for learners who need F-keys inside the sandbox; then use the in-sandbox `check`/`hint` shims instead.
- Full-screen apps (`vim`, `htop`) need the shell pane to become fullscreen temporarily, or accept that a 60-col pane is cramped. **Simplest correct answer: a "focus mode" toggle** that hands the whole terminal to the PTY.
- ASCII-only mode + no-color mode (`NO_COLOR`) + colorblind-safe palette (never rely on red/green alone - pair with ✅/❌ glyphs).

**Alternative front-ends** (same core, different renderer):
- **Plain CLI mode** - `shellforge run <level>` prints the briefing, drops you in the shell, you type `check`. Zero TUI complexity, maximum compatibility, great for tmux users. *Ship this first; it de-risks everything.*
- **Local web UI** - Go/Rust server + xterm.js over WebSocket to the PTY. Prettier maps, images, animations; but adds a browser dependency and a second UI to maintain. Good v2 if you want visual polish.
- **VS Code extension** - reuses their integrated terminal. Nice reach, but couples you to VS Code's shell integration.

---

### 4.14 Authoring Toolkit

- `shellforge author scaffold <id>` - generates a level skeleton + assets dir.
- `shellforge author validate <path>` - schema + DAG + asset + check-type validation.
- `shellforge author test <id>` - the golden test: fresh sandbox → setup → **assert all checks FAIL** → run `solution` → **assert all checks PASS** → assert teardown leaves no residue. Run for every level in CI on every PR.
- `shellforge author record <id>` - starts a shell, records your session, and offers to write the journal as the `solution` and suggest `command_matched` patterns. Massively lowers the authoring bar.
- JSON Schema published so editors give autocomplete on level YAML.

---

### 4.15 Content Distribution & Updates

- Core pack **embedded in the binary** (`embed.FS` / `include_dir!`) → works with zero network after install (G4).
- Extra packs: `shellforge content install github:user/repo@v1.2.0` → downloads release tarball, verifies sha256 + optional minisign signature, extracts to `~/.local/share/shellforge/packs/`.
- Engine declares `content_api_version`; packs declare a semver range. Refuse incompatible packs with a clear message.
- Binary self-update: `shellforge update` checks GitHub Releases (opt-out via config).

---

## 5. Key Flows

### 5.1 First run

```
user            CLI            Doctor        Runtime         Store
 │ init          │               │              │               │
 │──────────────►│──probes──────►│              │               │
 │               │◄──ok/fail─────│              │               │
 │               │──select runtime by caps + config────────────►│
 │               │──Provision(ImageSpec)───────►│               │
 │               │            (import rootfs / pull image)      │
 │               │◄──ready──────────────────────│               │
 │               │──migrate schema, seed DAG───────────────────►│
 │◄──"ready, run `shellforge play`"────────────────────────────│
```

### 5.2 A level turn

```
Orchestrator: teardown → materialize files → setup script → sentinel
UI:           render briefing + objectives
Runtime:      Attach() → PTY with instrumented bash
loop:
   keystroke → PTY → bash executes
   OSC 133 D;<exit> → parser → Journal + EventBus
   [live mode] EventBus → Verifier.runCheap() → objectives repaint
user types `check` (or F2)
   → Verifier.runAll() via Runtime.Exec()
   → per-objective results
   → pass? Scoring → concept_mastery update → achievements → next node
   → fail? render targeted on_fail for the *first* failing required objective only
```

*(Render only the first failure, not all of them - dumping five red errors at once is demoralizing and usually they cascade from one cause.)*

---

## 6. Curriculum Map (suggested v1 content, ~60-80 levels)

```
Act I - Orientation          pwd · ls · cd · paths abs/rel · tree · man/--help/apropos · clear/history
Act II - Files               cat/less/head/tail · touch/mkdir · cp/mv/rm · file · stat · wildcards & globs
Act III - Plumbing           stdin/stdout/stderr · > >> 2> &> · | · tee · xargs · sort/uniq/wc/cut/tr
Act IV - Searching           grep (-i -r -n -v -E) · find (-name -type -size -mtime -exec) · locate
Act V - Ownership            users/groups · ls -l anatomy · chmod (symbolic+octal) · chown · umask · sudo
Act VI - Processes           ps · top/htop · jobs/fg/bg/& · kill/killall · signals · nohup · exit codes
Act VII - Environment        env vars · export · PATH · .bashrc vs .profile · alias · which/type
Act VIII - Text mastery      sed basics · awk basics · cut/paste/join · diff/patch · jq
Act IX - Packages & archives apt/dpkg · tar/gzip/zip · checksums
Act X - Networking           ip/ifconfig · ping · curl/wget · ss/netstat · ssh · scp/rsync · /etc/hosts
Act XI - Automation          bash scripting (vars, if, for, while, functions, $?, "$@") · cron · systemd units
Act XII - Boss content       "fix the broken server" · "forensics: who deleted the file?" · "one-liner golf"
```

Each Act ends with a **boss level**: multi-objective, no hand-holding, 10-20 minutes, combining the Act's concepts.

---

## 7. Cross-Cutting Concerns

| Concern | Decision |
|---|---|
| **Security** | Never run the container `--privileged`; no host bind-mounts except the read-only game dir; default `--network none`; drop capabilities; sandbox user is non-root with scoped `sudo`. Verify all downloaded artifacts by sha256 before use. |
| **Privacy/telemetry** | Off by default. If added, local-only aggregate stats with an explicit opt-in prompt and a visible `stats --raw` dump. Never transmit journal contents (people type secrets into shells). |
| **Offline** | Core pack embedded; no runtime network dependency after `init`. |
| **Accessibility** | `--ascii`, `NO_COLOR`, `--plain` (screen-reader-friendly linear output), configurable keybinds, no timing-critical mechanics in the default mode. |
| **i18n** | All user-facing strings via a catalogue; level YAML supports `briefing.en`, `briefing.fr`. Ship en first, structure for more. Mauritius → fr is a realistic second. |
| **Performance** | Level start <1 s (soft reset), check run <500 ms typical, TUI frame budget 16 ms, PTY passthrough latency <5 ms. Cheap checks only in live mode. |
| **Observability** | `--log-level=debug`, logs to `~/.cache/shellforge/logs/`, `shellforge bug-report` bundles doctor JSON + last logs + versions (redacted journal) into a zip for GitHub issues. |
| **Error philosophy** | Every error names *what failed*, *why*, and *the next command to run*. No bare stack traces. |

---

## 8. Game Mechanics - Options ★

Menu of ways to make it feel like a game. Each is annotated with what it needs from the architecture and rough effort. The Event Bus (§4.5) is what lets you add these without touching the core.

### Structural / core loop options

**8.1 Mission Campaign (linear-ish, narrative)**
Chapters with a story arc - you're a new junior sysadmin at a company; each mission is a ticket. Briefings written in-world, terminal is the "company server".
*Needs:* nothing beyond the base. *Effort:* low. *Verdict:* **the recommended default spine.** Narrative costs writing time, not engineering time, and it's the biggest perceived-quality multiplier per hour spent.

**8.2 Skill Tree / Talent Tree**
Render the curriculum DAG as an unlockable tree; nodes cost "skill points" earned from XP; multiple branches (Text Processing / Sysadmin / Networking) let learners choose a path.
*Needs:* DAG renderer + unlock economy. *Effort:* low-medium. *Verdict:* excellent - gives agency and makes the map screen a reward in itself.

**8.3 Filesystem-as-Dungeon (roguelike)**
The directory tree *is* the map. `cd` = move, `ls` = look, `cat` = read scrolls, files are items, `chmod +x` unlocks doors. (Prior art: `bashcrawl`.) Rooms contain monsters solved by commands.
*Needs:* rich setup file manifests + a room generator. *Effort:* medium. *Verdict:* very charming and genuinely teaches navigation muscle memory. Best as **Act I-II** specifically, then hand off to real tasks - it doesn't scale to `awk`.

**8.4 Time Attack / Speedrun mode**
Same levels, timer on, leaderboard of personal bests; par times; ghost replay of your previous run from the journal.
*Needs:* journal replay + timing. *Effort:* low. *Verdict:* great **replay** layer for already-passed levels; never make it the first experience.

**8.5 Command Golf**
Score = fewest keystrokes / fewest commands / shortest pipeline. Par values per level. Global "best known" solution revealed after you pass.
*Needs:* `command_count_under`, keystroke counter. *Effort:* low. *Verdict:* superb for intermediate learners; naturally teaches flags and idioms. Pair with the "here's how a pro did it" reveal.

**8.6 Escape Room / Puzzle Box**
Each level is a locked box: find the clue (`grep`), decode it (`tr`/`base64`), unlock the next (`chmod`, a password file). Prior art: OverTheWire Bandit - hugely successful format.
*Needs:* chained setup where level N's solution seeds level N+1. *Effort:* medium (content-heavy). *Verdict:* strong for Acts IV-VIII. Highly addictive.

**8.7 Break/Fix Simulator ("the server is on fire")**
The sandbox is pre-broken: full disk, permissions wrong, service crashlooping, cron misfiring, `PATH` mangled. Objective = restore green. Multiple valid fixes.
*Needs:* state-based checks (already core), a "health dashboard" panel, runtime `systemd` capability. *Effort:* medium. *Verdict:* **the best "boss fight" format** and the closest to real job skill. Put one at the end of each Act.

**8.8 Detective / Forensics**
"Someone deleted the payroll file at 02:00. Who, and how?" Investigate with `last`, `journalctl`, `find -mtime`, `grep` through fabricated logs. Answer submitted via a file or an in-game `accuse <user>` shim.
*Needs:* rich fabricated log assets + `file_content` answer checks. *Effort:* medium (content-heavy, very reusable engine-wise). *Verdict:* extremely engaging, teaches investigation over memorization.

**8.9 Procedural Daily Challenge**
Seeded generator produces a fresh puzzle every day (random file trees, random target). Everyone with the same date gets the same puzzle → shareable results ("Shellforge #142: 4 commands, 1:32 🟩🟩🟩⬜") without needing a server.
*Needs:* seeded generators in setup + generated checks. *Effort:* medium-high (generators must guarantee solvability). *Verdict:* the single best retention mechanic. Wordle-style sharing gives you free distribution.

### Reward / feedback layers (stack on top of any of the above)

**8.10 XP, levels, ranks, streaks, badges** - `Novice → Apprentice → Operator → Sysadmin → Wizard`. Achievements from journal patterns: *"Tab Master"*, *"Manual Labour"* (used `man` 20×), *"No Hints"*, *"rm -rf survivor"*, *"One-Liner"*. *Effort: low. Do it.*

**8.11 Rewind / Checkpoints** - snapshot before each command batch; `rewind` undoes system state. Turns catastrophic mistakes into a *mechanic* instead of a frustration. *Needs: `Runtime.Snapshot/Restore`. Effort: medium (free with Docker, harder with WSL).*

**8.12 Loot / Collectibles** - each level drops a "tool card" (a man-page summary card for a command you used). Collect all 80 cards. Doubles as a **reference deck** the learner can browse - reward *and* study aid.

**8.13 Pet / Base-building** - you maintain a "server" whose uptime/health depends on completing maintenance tasks; neglect it and it degrades. *Effort: high, and it can feel manipulative. Optional.*

**8.14 Local co-op / classroom** - instructor mode: export/import progress, class leaderboard from JSON files on a shared drive, custom pack per assignment. *Effort: medium. Big for adoption in a university context.*

**8.15 Boss AI / adversary** - a scripted "intruder" process periodically breaks something while you work (creates files, changes perms), and you must both complete the task and counter it. *Effort: medium-high. Great for a finale, exhausting as a default.*

### Recommended layering

```
v0.1  Plain-CLI · Mission Campaign (8.1) · XP/badges (8.10)          ← proves the engine
v0.2  TUI · Skill tree map (8.2) · Live objectives · Hint ladder
v0.3  Dungeon crawl for Acts I-II (8.3) · Command golf (8.5)
v0.4  Break/fix bosses (8.7) · Rewind (8.11) · Tool cards (8.12)
v0.5  Escape-room chapter (8.6) · Forensics chapter (8.8)
v1.0  Daily procedural challenge (8.9) · Speedrun+ghosts (8.4) · Classroom (8.14)
```

---

## 9. Technology Stack Options

| Option | Pros | Cons |
|---|---|---|
| **Go** *(recommended)* | True single static binary, trivial cross-compile inc. Windows arm64, `embed.FS`, best-in-class TUI (`bubbletea`+`lipgloss`), `creack/pty`, official Docker SDK, GoReleaser. | GC pauses irrelevant here; less "systems-y" than Rust. |
| **Rust** | Single binary, `ratatui`, `portable-pty`, `bollard`, `rusqlite`, `include_dir!`. Matches your Tauri/SQLCipher experience. | Windows PTY (ConPTY) plumbing is fiddlier; slower iteration on a content-heavy project. |
| **Node/TS** | Fastest UI iteration, `node-pty`, `xterm.js` if you go web. | `pkg`/SEA binaries are large and flaky; runtime dependency risk breaks G2. |
| **Python** | Fastest to prototype, `ptyprocess`, `textual` (excellent TUI). | Distribution is the weak point on Windows; PyInstaller pain. Violates G2 in spirit. |

**Recommendation:** **Go**, primarily because the distribution story (G2) and the Windows story (G3) are where this project actually lives or dies, and Go is strongest at exactly those. Rust is a fine second if you'd rather stay in a familiar language.

---

## 10. WSL-Specific Design Notes & Gotchas

1. **CRLF is the #1 killer.** A `.sh` with `\r\n` fails with `bad interpreter: /bin/bash^M`. Commit `.gitattributes` with `* text=auto eol=lf` and `*.sh text eol=lf`; additionally strip `\r` when materializing files into the sandbox. Test this on a Windows machine with `core.autocrlf=true`.
2. **Case sensitivity.** Windows filesystems are case-insensitive; the sandbox is case-sensitive. Never source level assets from a Windows path at runtime - always push them *into* the distro.
3. **Never store level state on `/mnt/c`.** 9p/DrvFs is slow (10-50× on many small files) and permissions are fake. Keep everything on the distro's ext4.
4. **Disable interop and automount** (§4.3.A) - otherwise `echo $PATH` prints 40 Windows directories and confuses every PATH lesson, and `ls /mnt/c` exposes the learner's real files to `rm -rf`.
5. **`wsl --import` name collisions** - check `wsl -l -q` before importing; refuse or suffix.
6. **WSL2 memory ballooning** - set limits via `%USERPROFILE%\.wslconfig` (`memory=2GB`, `processors=2`, `swap=0`) but *warn before writing it*, since it's global to all the user's distros. Prefer asking rather than silently editing.
7. **systemd** requires `[boot] systemd=true` and WSL ≥ 0.67.6; adds seconds to first start. Gate `systemctl` levels behind the `systemd` capability.
8. **`wsl --terminate` / `--shutdown`** are your clean-shutdown primitives; the 8-second idle-shutdown behaviour can kill background state - don't rely on long-lived background processes surviving idle.
9. **Antivirus** frequently mangles `wsl --import` of a large tarball. Detect slow/failed imports and suggest an exclusion for the install dir.
10. **Uninstall must be clean:** `wsl --unregister shellforge-sandbox` + delete the vhdx directory. Say so in the docs; orphaned 2 GB vhdx files generate angry issues.
11. **ConPTY quirks:** resizing during a full-screen app can garble output on older Windows builds; require Windows Terminal in docs and check `WT_SESSION`.

---

## 11. Testing & CI Strategy

| Layer | Test |
|---|---|
| Level content | **Golden test** per level (§4.14): fresh sandbox → checks must FAIL → run `solution` → checks must PASS → teardown leaves nothing. Runs on every PR, matrix over runtimes. |
| Check purity | Run every check twice, snapshot filesystem hash before/after - must be identical (checks must not mutate). |
| Check strictness | **Mutation tests**: apply known near-miss solutions (wrong case, wrong dir, hardcoded answer) and assert the level *rejects* them. Catches over-permissive checks. |
| Runtime layer | Contract test suite run against every `Runtime` impl (same assertions, different backend). |
| Instrumentation | PTY golden tests: feed scripted keystrokes, assert journal contents; include `vim`/`less` sessions to prove full-screen apps survive. |
| Platform | GitHub Actions matrix: `ubuntu-latest` (docker+podman+bwrap), `macos-latest` (colima), `windows-latest` (**WSL2 is available on GH windows runners** - use it to test the import path). |
| Install docs | A scripted run of the README's exact commands in a clean container/VM image. If the docs drift, CI fails. |
| Determinism | Build the image twice, compare digests. |

---

## 12. Repository Layout

```
shellforge/
├── cmd/shellforge/            # main, cobra commands
├── internal/
│   ├── doctor/                # probes + remediation
│   ├── runtime/               # Runtime iface + docker/podman/wsl/bwrap/native
│   ├── sandbox/               # image spec, provisioning, snapshots
│   ├── pty/                   # multiplexer, OSC133/OSC7 parser, ring buffer
│   ├── journal/               # command journal, env snapshots
│   ├── content/               # pack loader, schema, validator, DAG
│   ├── verify/                # check catalogue, assertion engine
│   ├── game/                  # orchestrator, scoring, mastery, achievements, eventbus
│   ├── store/                 # sqlite, migrations
│   ├── tui/                   # bubbletea models/views
│   └── platform/              # OS-specific bits, console modes, paths
├── packs/core-linux-basics/   # the v1 curriculum (embedded)
├── images/                    # Containerfile → OCI image + WSL rootfs tarball
├── docs/                      # the user-facing documentation (see §13)
├── scripts/install.sh, install.ps1
├── .github/workflows/         # ci.yml, release.yml (GoReleaser), content-test.yml
└── .gitattributes             # * text=auto eol=lf   ← non-negotiable
```

---

## 13. Documentation Outline (the deliverable that follows)

```
README.md              - what it is, 60-second demo GIF, quickstart, badges
docs/
  01-install-windows.md    WSL enablement, virtualization, Windows Terminal, install.ps1, doctor
  02-install-linux.md      podman/docker/bwrap paths
  03-install-macos.md      Docker Desktop / Colima
  04-quickstart.md         first mission, the check/hint/reset loop, keybinds
  05-how-it-works.md       what the sandbox is, why it's safe, where files live
  06-troubleshooting.md    keyed to every doctor failure code (deep-linkable anchors)
  07-authoring-levels.md   level YAML reference + check catalogue + `author` commands
  08-command-reference.md  every CLI verb and flag
  09-uninstall.md          full cleanup incl. wsl --unregister, vhdx deletion
  CONTRIBUTING.md · CODE_OF_CONDUCT.md · LICENSE
```

**Rule:** every `doctor` failure emits a `docs/06-troubleshooting.md#anchor` link, and CI asserts every emitted anchor exists.

---

## 14. Roadmap

| Milestone | Contents | Exit criterion |
|---|---|---|
| **M0 - Spike** | `Runtime` iface + DockerRuntime, PTY passthrough, one hardcoded level | You can play one level end-to-end on Linux |
| **M1 - Engine** | Content pack loader, verification engine, SQLite, plain-CLI mode, 10 levels | `shellforge run` works; golden tests green |
| **M2 - Windows** | WslRuntime, doctor, install.ps1, rootfs artifact | A clean Win11 VM goes README → playing in <10 min |
| **M3 - Game** | TUI, campaign, XP/badges, hint ladder, skill-tree map, 40 levels | Feels like a game to 5 test users |
| **M4 - Depth** | Break/fix bosses, golf, rewind, tool cards, 70 levels | Full Acts I-IX |
| **M5 - Polish** | Docs complete, macOS, accessibility, packaging (scoop/brew), daily challenge | Public v1.0 release |

---

## 15. Open Decisions (need your call)

| # | Decision | Options | Lean |
|---|---|---|---|
| D1 | Implementation language | Go / Rust | **Go** (distribution + Windows) |
| D2 | Windows primary runtime | WSL-import / require Docker | **WSL-import** (fewer prerequisites = fewer dropoffs) |
| D3 | Base distro in sandbox | Debian slim / Alpine | **Debian** (GNU coreutils = correct flags) |
| D4 | First front-end | TUI first / plain-CLI first | **plain-CLI first**, TUI at M3 |
| D5 | Verification bias | state-only / allow command-pattern gating | **state-only required**, patterns for bonus objectives |
| D6 | Core game format | campaign / dungeon / escape-room | **campaign spine**, dungeon for Act I-II, escape-room for Act IV-VIII |
| D7 | LLM tutor | in v1 / v2 opt-in / never | **v2, opt-in** - must stay fully playable offline |
| D8 | Reset mechanism on WSL | scratch-dir wipe / export-import | **scratch-dir wipe** (sub-second) |
