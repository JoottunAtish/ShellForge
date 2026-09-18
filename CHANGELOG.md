# Changelog

Notable changes, newest first. Dates are the release date.

This file records what changed for someone using Shellforge. The commit history
is the record for someone working on it, and `PROGRESS.md` is the day-by-day
build log.

## v0.1.0

The first release. Everything below is new, so this entry describes what the
release is rather than what changed in it.

### The game

- **25 levels across six acts**, from moving around a filesystem to writing a
  bash script, ending in a 3 AM incident you diagnose and fix from scratch.
  Every level passes the golden contract against a real Docker daemon.
- **A real bash shell in a real PTY.** `vim`, `less`, `htop`, job control, tab
  completion and Ctrl-C all work, because nothing intercepts or re-executes what
  you type.
- **Verification reads system state, not your keystrokes.** 14 check types
  covering files, contents, directory trees, modes, ownership, symlinks,
  environment, working directory, processes, and an escape hatch for the rest.
  Pipe it, loop it, use `awk`, write a script: if the result is right, you pass.
- **A curriculum DAG.** Levels unlock from what you have passed. `shellforge map`
  draws it, `shellforge play` resolves the next one and says which it chose and
  why before provisioning anything, and `shellforge skip` moves you past one
  without awarding XP.
- **Scoring with the arithmetic shown.** XP scaled by difficulty, a bonus for
  beating par and for passing first try, hints priced before you commit to them,
  and an award that never drops below zero. Passing prints the whole sum line by
  line.
- **14 achievements**, and `shellforge stats` shows the locked ones too.
- **Live objective ticking.** Objectives tick as you work, without you asking.
  `check` stays the only complete answer and the only one that explains a
  failure. `--live-check=off` turns the ticking off.
- **`reset` that tells you what it would delete first**, and only ever touches
  the level's own directory.

### The sandbox

- **Docker on Linux, WSL2 on Windows**, behind one runtime interface, and the
  game plays natively from PowerShell or Windows Terminal. The pseudo terminal
  is allocated inside the sandbox rather than on the host, so Windows needs no
  ConPTY and there is one attach path rather than two.
- **No host access.** No bind mounts except a read-only `/opt/shellforge`, no
  network unless a level asks for it, never `--privileged`, and a non-root user.
  On Windows, automount and interop are off, so there is no `/mnt/c`.
- `rm -rf /` inside the sandbox destroys the sandbox and nothing else.

### Getting it running

- **`shellforge doctor`**, twelve preflight checks with a status table, `--json`,
  and `--fix` for the reversible ones. Every failure names the command that fixes
  it and links to a heading in the troubleshooting guide.
- **`install.sh` and `install.ps1`**, both verifying the release checksum before
  anything is placed on disk, and neither editing a shell profile or elevating.
- **`shellforge bug-report`**, which bundles diagnostics into a zip on your own
  machine and uploads nothing. Your commands are excluded by default, and
  `--journal` includes them redacted.

### Authoring

- **Levels are YAML.** `shellforge author validate` checks a pack against the
  format, `shellforge author test` runs the six-step golden contract against a
  real sandbox and refuses rather than skips without a Docker daemon.

### Privacy

- No account, no server, no telemetry, opt-in or otherwise. Progress is one
  SQLite file on your machine and it works fully offline after install.

### Known issues

- **`shellforge init` needs a clone of this repository.** The installers place
  the binary and nothing else, so neither backend can find a sandbox image on a
  machine that has only the release. Both install guides give the workaround.
  [#172](https://github.com/JoottunAtish/ShellForge/issues/172)
- **On Windows, play from inside WSL, not from PowerShell.** `play`, `run` and
  `sandbox shell` are the three verbs that open an interactive shell, and
  opening one allocates a pseudo terminal on the host, which the library
  Shellforge uses for that does not implement on Windows at all. Both backends
  are affected, so switching from Docker to WSL does not help. They refuse up
  front with the fix rather than failing after provisioning. Every other verb,
  `doctor` and `init` included, works natively. WSL2 is a real Linux host and
  Docker Desktop shares one daemon with it, so the sandbox is not built twice.
  [#138](https://github.com/JoottunAtish/ShellForge/issues/138)
- **`shellforge author scaffold` and `shellforge author record` are not built.**
  They are hidden from `shellforge help`, so nothing offers them, and they stay
  registered, so typing one answers with a refusal naming where it went rather
  than an unknown command error. Copy an existing level instead; the authoring
  guide says so.
- **There is no `uninstall` or `export` verb.** [Uninstall](docs/06-uninstall.md)
  names every file and directory by hand, and your progress is one SQLite file
  you can copy.
- **macOS is untested.** It will probably work through Docker Desktop or Colima.
  Community supported rather than supported.
