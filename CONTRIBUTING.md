# Contributing to Shellforge

Thanks for looking. The most valuable contribution right now is **levels**, and the
second most valuable is **telling us where the install docs failed you**.

Before you start on code, read [CLAUDE.md](CLAUDE.md). It holds the non-negotiables,
the layer rules, and the Go conventions. It is written for an AI coding assistant
but it is the human engineering reference too.

---

## Ground rules

These are short because they are the ones that actually get violated.

1. **LF line endings, always.** `.gitattributes` enforces it and CI checks it. A
   `.sh` file with CRLF fails inside the sandbox with `bad interpreter: /bin/bash^M`.
2. **No em dashes or en dashes** anywhere in the repository. Use a plain ASCII
   hyphen, a colon, or a comma. CI fails the build on typographic punctuation. Run
   `./scripts/check-punctuation.sh` locally.
3. **Ask before adding a dependency.** The approved set is listed in
   `.claude/skills/go-style/SKILL.md`. Every dependency is a cross-compilation
   risk and this project ships a single static binary.
4. **Checks never mutate state.** A verification check that writes to the sandbox is
   a bug. CI hashes the filesystem before and after a double check run.
5. **Every user-facing error names what failed, why, and the next command to run.**
   Bare Go errors must not reach the terminal.
6. **Never loosen a check to make a solution pass.** If the level's `solution` does
   not satisfy the check, the level is wrong. Report it, do not adjust the check
   until it goes green. That is how you ship a level that accepts wrong answers.

---

## Development setup

You need **Go 1.25 or newer** and **one container runtime**.

```bash
git clone https://github.com/JoottunAtish/ShellForge.git
cd ShellForge
make build
make test
```

On Windows, `make` is usually not installed. Use the PowerShell equivalent, which
runs the same commands:

```powershell
.\make.ps1 build
.\make.ps1 test
```

Or run the underlying commands directly:

```bash
go build -o bin/shellforge ./cmd/shellforge
go test ./...
gofmt -s -l .
go vet ./...
```

### Runtime prerequisites

| Platform | What you need |
|---|---|
| Linux | Docker or Podman, running |
| Windows | WSL2 (`wsl --install --no-distribution`), or Docker Desktop |
| macOS | Docker Desktop or Colima. Community supported, untested by maintainers. |

Run `shellforge doctor` once it builds. It will tell you what is missing and the
exact command to fix it.

---

## Before every commit

```bash
gofmt -s -w . && go vet ./... && go test ./...
```

`make ci` runs the whole set including the punctuation and layering checks.

---

## Contributing a level

This is the path we most want people to take.

```bash
shellforge author scaffold my-level-id     # generates the skeleton and assets dir
$EDITOR packs/core-linux-basics/levels/my-level-id.yaml
shellforge author validate packs/core-linux-basics
shellforge author test my-level-id         # golden test
shellforge run my-level-id                 # play it yourself
```

The schema and the full check catalogue are in
[docs/LEVEL-FORMAT.md](docs/LEVEL-FORMAT.md). The authoring invariants there are
enforced by the validator, so read them before you write.

### What a good level looks like

- **It teaches one new thing and reuses two old ones.** Level 24 needs the stderr
  knowledge from level 11. That circularity is what makes a curriculum feel designed
  rather than listed.
- **It verifies state, not syntax.** Use `command_matched` only for bonus objectives
  where the syntax genuinely *is* the lesson, such as `tee` or `find -exec`.
- **Every `on_fail` names what is wrong and what to look at.** Never "incorrect",
  never "try again". Write it in the voice of a helpful senior colleague.
- **Setup is deterministic and offline.** Explicit `touch -d` for any mtime the
  level depends on, seeded generators, no network calls. Ever.
- **It makes the painful way visible before the good way.** Sixty files before
  globs. A four thousand line `cat` before `head`.
- **Bosses have no numbered steps.** Give them a broken system and "make it work",
  with multiple valid fixes that all pass.

### The golden test contract

Every level must satisfy this, and CI runs it on every pull request:

1. Fresh sandbox, run `setup`.
2. Run checks. **All required checks must FAIL.** If a level passes before the
   learner has done anything, the checks are wrong.
3. Run `solution` as `learner` in a login shell.
4. Run checks. **All required checks must PASS.**
5. Run `teardown`. `setup.root` no longer exists and no stray processes remain.
6. Run checks twice, hash the filesystem before and after. **Identical.**

---

## Contributing documentation

The audience is a first-year CS student on Windows who has never opened a terminal
and is worried about breaking their laptop.

- Explain what a thing **is** before telling anyone to install it.
- Never write "just" or "simply".
- Define jargon at first use: WSL, distro, shell, sandbox, PATH.
- Every command shown must be copy-pasteable and correct. Run it first.
- Every `doctor` failure code needs a matching anchor in
  [docs/05-troubleshooting.md](docs/05-troubleshooting.md). CI enforces this.

---

## Pull requests

- **Branch naming is `<kind>/issue-<number>`**, where kind is `feature`, `bug`, or
  `chore`. For example `feature/issue-24`. CI rejects anything else, so open the
  issue first. Every branch traces to a ticket, because the ticket is where the
  reasoning lives when someone asks in six months why this happened.
- Branch from `main`. One logical change per PR.
- Include tests. Write them alongside the code, not "later".
- If your change touches the level format, update
  [docs/LEVEL-FORMAT.md](docs/LEVEL-FORMAT.md) in the same commit.
- If your change adds a check type, register it, test it, and document it in the
  catalogue in the same commit.
- Say which platforms you exercised it on. "Linux only" is a fine answer, we just
  need to know.

CI must be green. If Windows CI is flaky rather than genuinely broken, say so in the
PR and a maintainer will make the call.

### Merging

`main` is protected. A pull request needs **an approving review from a code owner**
before it can merge, all status checks green, and the branch up to date with `main`.
Code owners are listed in [.github/CODEOWNERS](.github/CODEOWNERS), and the
safety-critical paths (the sandbox image, the runtimes, the platform layer, the
scripts, and the rules themselves) are owned explicitly.

This means you cannot merge your own contribution, and neither can we without a
second pair of eyes once there is a second maintainer. That is deliberate: this
project runs a real shell on a beginner's laptop and promises them nothing can go
wrong.

---

## Reporting bugs

Once the binary builds, `shellforge bug-report` bundles the doctor output, logs,
versions, and a redacted command journal into a zip. Attach it. It contains no
command output, only the commands themselves.

For install failures, tell us the exact step number in the install guide where you
got stuck. That is the single most useful bug report this project can receive.

---

## Code of conduct

By participating you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).
