---
name: security
description: Security rules for Shellforge, covering the threat model, subprocess execution, path handling from untrusted content packs, artifact and supply chain integrity, required scanning tools, privacy, and sandbox configuration. Load before writing any code that spawns a process, handles a path from a level pack, downloads or verifies an artifact, touches the journal, or configures the sandbox. Also load when doing a security review or when govulncheck or gosec reports something.
---

# Security

## Threat model, stated precisely so the rules are applied correctly

The sandbox protects **the learner from themselves**. It is not a security boundary
against a malicious user, because the attacker and the victim are the same person.
Do not spend effort hardening against a learner who is deliberately trying to
escape their own sandbox, and do not treat a container escape from inside a level
as a Shellforge vulnerability. It belongs upstream.

What the rules below actually defend are the three places where the binary handles
input it did not author:

1. **Artifacts downloaded from the network:** the WSL rootfs tarball, release
   binaries.
2. **Content packs**, which may eventually come from third parties.
3. **The host system**, which must never be touched by anything a level declares.

Full public statement of this is in `SECURITY.md`.

## Subprocess execution

Shellforge shells out to `docker`, `wsl.exe`, and `bash` constantly. This is the
highest-risk code in the project.

- **Always use `exec.CommandContext` with separate arguments.** Never build a
  command string and hand it to a shell.

  ```go
  // GOOD: arguments are passed as a vector, never parsed by a shell
  exec.CommandContext(ctx, "wsl.exe", "-d", distro, "-u", user, "--exec", "/bin/bash", "-lc", script)

  // BAD: string interpolation into a shell invocation
  exec.CommandContext(ctx, "sh", "-c", fmt.Sprintf("wsl -d %s -u %s", distro, user))
  ```

- **Never interpolate a level-supplied value into a host shell command.** Level YAML
  controls `setup.script`, `teardown.script`, and `script` checks. Those strings
  execute **inside the sandbox**, passed as a single argv element to bash in the
  container or distro. They must never reach a host shell and never be concatenated
  into a host command line.
- **The command name itself is never variable.** Resolve binaries by fixed name
  (`docker`, `wsl.exe`) and let `exec.LookPath` find them. Do not accept a runtime
  binary path from config or from a level.
- **Validate before you execute.** Distro names, container names, level IDs and
  user names go through a strict allowlist regexp
  (`^[a-zA-Z0-9][a-zA-Z0-9_-]*$` for identifiers) before reaching an argv.
  **Reject, do not sanitize.** The first character must be alphanumeric because a
  value that can start with a hyphen is flag-shaped: once it reaches an argv
  vector as a positional operand, `docker` or `wsl.exe` reads it as an option
  rather than as a value, so a name like `-f` or `--force` becomes an argument
  injection. That is a different failure from shell injection, and passing an
  argv vector instead of a command string does not stop it. Passing `--` as
  the end-of-options terminator before the value is the other half of this
  defence. For a destructive target the allowlist is necessary but not
  sufficient: the value must also be compared against a compile-time
  constant rather than trusted outright, which is what the
  `destructive-safety` skill requires and what the doc comment on
  `ImageSpec.Name` already states.
- **Prefer the standard library over a subprocess.** If `os`, `io/fs`, or
  `archive/tar` can do it, do not spawn a process.
- `Session.Exec` takes `argv []string`, not a command string. That is a security
  boundary. Do not add a convenience overload that takes a string.

## Path handling

- **Every path derived from a content pack is untrusted.**
  - `setup.files[].source` must resolve inside the pack directory. Reject `..`
    segments and absolute paths. Resolve symlinks and re-check.
  - `setup.files[].path` must resolve under `setup.root`, and `setup.root` must be
    under `/home/learner/`. This is what makes reset safe.
- **Use `filepath.Clean` and then check the prefix**, not the other way around. On
  Windows also reject reserved device names and alternate data stream syntax.
- **Never use a host path on the sandbox side, or a sandbox path on the host side.**
  Case sensitivity differs, and this is how you get a level that works on Linux and
  silently corrupts on Windows.
- Strip `\r` from every file materialized into the sandbox.

## Artifact and supply chain integrity

- **Verify sha256 before use, never after.** The WSL rootfs tarball is
  checksum-verified before `wsl --import` touches it. On mismatch, refuse, delete
  the download, and say so in the error.
- **Pin the release URL and the expected digest in the binary.** Do not resolve
  "latest" at runtime.
- **`go.sum` is committed and authoritative.** Builds run with `-mod=readonly`,
  the default since Go 1.16. CI fails if `go.mod` or `go.sum` would change, so a
  dependency bump can only happen through a reviewed commit.
- **Do not set `GOFLAGS=-mod=mod`, `GONOSUMDB`, `GONOSUMCHECK`, or `GOPRIVATE`** in
  CI or the Makefile. The checksum database is a feature.
- **`install.sh` and `install.ps1` verify the checksum of what they download**
  before placing anything on PATH. An installer that does not verify is worse than
  no installer.
- **Dependabot is enabled.** Review the diff; do not merge on green alone.
- **Every GitHub Action is pinned to a 40 character commit SHA**, lowercase, with
  the version in a trailing comment so a reader and Dependabot both know what it
  is:

  ```yaml
  - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
  ```

  `@v7` is a mutable pointer. Whoever controls the tag can repoint it at any
  commit and CI would run that commit with the repository token, so a SHA is the
  only immutable form of Action reference. `scripts/check-ci-gates.py` fails the
  Style job on a tag pin, a branch pin, an abbreviated SHA, or a missing version
  comment, and `scripts/tests/test_check_ci_gates.py` covers that matcher.

  Resolve a tag to its commit rather than copying a value from a search result,
  and remember that an annotated tag points at a tag object, not a commit:

  ```bash
  gh api repos/actions/checkout/git/ref/tags/v7.0.1 --jq '.object | "\(.type) \(.sha)"'
  gh api repos/actions/checkout/git/tags/<that-sha> --jq .object.sha
  ```

  There is no allowlist of trusted publishers. The GitHub-owned actions are the
  likeliest to be fine and also the highest-value target, so exempting them would
  defeat the point.

## Required tooling

These run in CI and must be green:

| Tool | Command | Catches |
|---|---|---|
| `go vet` | `go vet ./...` | Suspicious constructs, printf mistakes, lost cancels |
| `govulncheck` | `govulncheck ./...` | Known CVEs in dependencies and the Go toolchain, filtered to reachable code paths |
| `gosec` | `gosec ./...` | Hardcoded credentials, weak crypto, unhandled errors, subprocess and path traversal patterns |
| race detector | `go test -race ./...` | Data races. The PTY multiplexer is concurrent, so this is not optional. |
| fuzzing | `go test -fuzz` | The OSC parser must have a fuzz target. It consumes an adversarial byte stream by definition. |

`govulncheck` is preferred over generic dependency scanners because it does
reachability analysis and reports only vulnerabilities the code can actually hit,
which keeps the signal usable.

## Data and privacy

- **No telemetry. Ever.** Not opt-in, not anonymous, not "just crash reports". This
  is a product promise printed in the README, and `internal/archtest` fails the
  build if `net/http` appears at or above L3.
- **Never transmit journal contents.** People type passwords and API keys into
  shells. The journal records commands, so treat it as secret material.
- **`bug-report` redacts.** Commands, not command output, and never the environment
  snapshot.
- **Keystroke instrumentation stores low-cardinality counters only** (tab pressed N
  times), never raw keystrokes. A learner may type a password into a `sudo` prompt
  during a permissions level.
- **Nothing is written outside the standard directories** from
  `internal/platform`. Uninstall must be able to find everything. State
  directories are created mode 0700.

## Sandbox configuration

- Never `--privileged`. Drop all capabilities, add back only what a level provably
  needs.
- `--network none` by default. Networking requires an explicit
  `requires: [networking]` in the level.
- The only host mount is `/opt/shellforge`, read-only.
- The sandbox user is `learner`, uid 1000, non-root.
- On WSL, `/etc/wsl.conf` disables automount and interop. Without that, `ls /mnt/c`
  exposes the learner's real files to `rm -rf`, which breaks the central product
  promise.

## References

- [Security Best Practices for Go Developers](https://go.dev/doc/security/best-practices)
- [How Go Mitigates Supply Chain Attacks](https://go.dev/blog/supply-chain)
- [Semgrep: Go command injection cheat sheet](https://semgrep.dev/docs/cheat-sheets/go-command-injection)
- [gosec](https://github.com/securego/gosec)
