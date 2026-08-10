# Security Policy

## Threat model, stated plainly

Shellforge runs a real Linux shell and invites you to break it. Understanding what
the sandbox does and does not protect matters, so here it is without hedging.

**What the sandbox protects.** It protects *you* from *yourself*. You can run
`rm -rf /` inside it and lose nothing that matters. Your host filesystem is not
mounted, Windows interop is disabled, there is no `/mnt/c`, and there is no network
unless a level explicitly asks for one. `shellforge reset` rebuilds the level world
in under a second.

**What the sandbox is not.** It is not a hardened security boundary against a
determined attacker who already controls the machine. In Shellforge the attacker
and the victim are the same person: the learner. We do not attempt to stop someone
who is deliberately trying to escape their own sandbox, and you should not treat a
container escape from inside a Shellforge level as a vulnerability in Shellforge.
It is a vulnerability in your container runtime, and it belongs upstream.

## What we do consider a vulnerability

Report any of these:

- **Host compromise from content.** A level pack, level YAML, or asset file that
  causes code execution on the **host** rather than inside the sandbox. This
  includes path traversal out of the pack directory, a `setup.files[].path` that
  escapes `setup.root`, or any level-supplied string reaching a host shell.
- **Failure to verify a downloaded artifact.** The WSL rootfs tarball, or anything
  fetched by `install.sh` or `install.ps1`, being used without its sha256 being
  checked first.
- **Data leaving the machine.** Shellforge has no telemetry and no network calls
  after install. Any outbound connection is a bug and we want to know immediately.
- **Journal or credential leakage.** The command journal records what you typed, and
  people type passwords into shells. Anything that writes the journal, the
  environment snapshot, or keystroke data somewhere it should not go, including into
  a `bug-report` bundle, is a serious issue.
- **Uninstall leaving privileged residue.** A WSL distribution that survives
  `sandbox destroy`, or an orphaned `.vhdx`, is a bug and we treat the disk-space
  class of it as a real problem.
- **Privilege escalation on the host** through any Shellforge code path, including
  the installers and the `doctor --fix` remediations.

## What we do not consider a vulnerability

- Escaping the sandbox from inside a level, as explained above.
- `sudo` working inside the sandbox. That is a curriculum requirement; Act V teaches
  it.
- The learner destroying their own progress database.
- Levels that are solvable in an unintended way. That is a content bug. Open a
  normal issue.

## Reporting

**Do not open a public issue for a security report.**

Use GitHub's private vulnerability reporting on this repository:
<https://github.com/JoottunAtish/ShellForge/security/advisories/new>

Include, where you can:

- What you observed and what you expected
- The exact steps, including the level ID if it is content-related
- Your platform, and the output of `shellforge doctor --json`
- Whether the impact lands on the host or stays inside the sandbox

You will get an acknowledgement within **five working days**. This is a small
project with one maintainer, so please be patient beyond that. We will tell you our
assessment, whether we agree on severity, and a rough timeline.

We do not run a bug bounty and cannot offer payment. We will credit you in the
release notes and the advisory unless you would rather stay anonymous.

## Supported versions

Pre-alpha. Nothing is released yet, so nothing is supported yet. Once v0.1 ships,
the latest minor release receives security fixes.

| Version | Supported |
|---|---|
| pre-release `main` | Best effort |

## How we try to prevent this class of problem

These run on every pull request and are documented in [CLAUDE.md](CLAUDE.md):

| Control | What it does |
|---|---|
| `govulncheck` | Reachability-aware scan for known CVEs in dependencies and the Go toolchain |
| `gosec` | Static analysis for subprocess, path traversal, credential, and crypto issues |
| `go vet` | Suspicious constructs in the standard toolchain |
| `go test -race` | Data races, which matter because the PTY multiplexer is concurrent |
| Fuzzing | The OSC parser has a fuzz target; it consumes an adversarial byte stream by design |
| `go.sum` plus `-mod=readonly` | Dependency changes can only land through a reviewed commit |
| Dependabot | Dependency updates, reviewed rather than auto-merged |
| Layer test | `internal/archtest` prevents the game layer reaching into a runtime implementation |

Design rules we hold ourselves to:

- Subprocesses are invoked with `exec.CommandContext` and an argv vector. Never a
  command string handed to a shell.
- Identifiers interpolated into an argv are allowlist-validated and rejected, not
  sanitized.
- Downloaded artifacts are sha256-verified **before** use, never after.
- The container never runs `--privileged`, defaults to `--network none`, and mounts
  exactly one read-only host path.
- No telemetry exists in the codebase, so there is nothing to accidentally enable.
