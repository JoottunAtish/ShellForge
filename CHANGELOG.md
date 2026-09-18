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
- **No host access.** No bind mounts except a read-only `/opt/shellforge`, never
  `--privileged`, and a non-root user. On Windows, automount and interop are off,
  so there is no `/mnt/c`. On Docker there is also no network unless a level asks
  for it; on WSL there is, and the known issue below says why and what it costs.
- `rm -rf /` inside the sandbox destroys the sandbox and nothing else.

### Getting it running

- **`shellforge doctor`**, twelve preflight checks with a status table, `--json`,
  and `--fix` for the reversible ones. Every failure names the command that fixes
  it and links to a heading in the troubleshooting guide.
- **`install.sh` and `install.ps1`**, both verifying the release checksum before
  anything is placed on disk, and neither editing a shell profile or elevating.
  On amd64 they fetch the sandbox image as well as the binary, so
  `shellforge init` works from a release install with no clone of this
  repository.
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

- **On arm64 Linux, `shellforge init` needs a clone of this repository.** The
  published sandbox image is amd64, so the installer deliberately does not place
  it on another architecture rather than hand you one whose every command fails
  with `exec format error`. It says so at the time. `init` then builds the image
  itself, which needs the repository and Docker, and
  [the Linux install guide](docs/02-install-linux.md) has the steps. amd64, which
  is almost everyone, needs none of this.
- **On Windows, the sandbox can still reach the network.** Every level is set up
  and verified offline, and no level needs a connection, but a learner who types
  `curl` inside one on the WSL backend will find it works. The Docker backend
  runs with `--network none` and does not. WSL2 has no per-distribution network
  switch to turn off: every distribution shares one virtual adapter, and that
  adapter can also see your own machine. Nothing reaches your files from in
  there, because no directory of yours is mounted and automount is off, but it
  is weaker isolation than the Docker backend gives.

  There is no way to ask for Docker instead for a level in v0.1. `shellforge
  play` and `shellforge run` always pick a backend themselves, and on Windows
  they pick WSL whenever WSL2 is present. Only `init` and the `sandbox`
  subcommands take `--runtime`.
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
