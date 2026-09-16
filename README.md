# Shellforge

**Learn the Linux command line by living in a terminal you can't break.**

[![CI](https://github.com/JoottunAtish/ShellForge/actions/workflows/ci.yml/badge.svg)](https://github.com/JoottunAtish/ShellForge/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go 1.25+](https://img.shields.io/badge/go-1.25%2B-00ADD8.svg)](https://go.dev/dl/)
[![Release: v0.1.0](https://img.shields.io/badge/release-v0.1.0-brightgreen.svg)](https://github.com/JoottunAtish/ShellForge/releases/latest)

[![Shellforge on GitHub](https://githubcard.com/JoottunAtish/ShellForge.svg?d=4S7ENYlgGRMx)](https://github.com/JoottunAtish/ShellForge)

![Solving the pipe-05 level: the briefing opens on a billing service alarm, three rotated logs are searched for ERROR lines and the total written to report.txt, then check turns every objective green and awards the XP.](docs/assets/demo.gif)

Shellforge drops you into a real Linux shell inside a disposable sandbox and gives
you jobs to do. Not multiple choice. Not a simulator. You type real commands into a
real `bash` process, and the game checks whether you actually did the thing.

You are a new junior sysadmin at Meridian Logistics. The last engineer left without
documentation. There are 25 tickets waiting.

---

## What makes it different

Most command line tutorials check what you typed. Shellforge checks what you
**did**.

If a level asks you to count the `ERROR` lines across a directory of logs, it does
not look for the string `grep`. It looks at `report.txt` and checks the number
inside it. Pipe it, loop it, use `awk`, write a script: if the number is right, you
pass. That is the difference between teaching a command and teaching a skill.

| | |
|---|---|
| **Real shell** | Actual `bash` in a PTY. `vim`, `less`, `htop`, job control, tab completion, Ctrl-C all work. |
| **Real verification** | 14 check types that read filesystem state, process state, file modes, ownership, shell environment and the command journal. |
| **Really disposable** | `rm -rf /` inside the sandbox destroys nothing on your machine, and `reset` rebuilds the level in under a second. |
| **Really offline** | No account, no server, no telemetry. Progress is a SQLite file on your disk. |
| **Really yours** | Levels are declarative YAML. Write your own, ship a pack. |

## Install

Both installers verify the release checksum before anything is placed on your
disk, neither edits a shell profile, and neither asks for Administrator or
`sudo`. You can read either script at the URL below before running it.

**Linux**, or from inside WSL on Windows:

```bash
curl -fsSL https://raw.githubusercontent.com/JoottunAtish/ShellForge/main/scripts/install.sh | sh
```

**Windows**, from an ordinary PowerShell window:

```powershell
Invoke-WebRequest -Uri https://raw.githubusercontent.com/JoottunAtish/ShellForge/main/scripts/install.ps1 -OutFile install.ps1
powershell -ExecutionPolicy Bypass -Scope Process -File install.ps1
```

Then:

```
shellforge doctor    # checks your machine and tells you how to fix anything
shellforge init      # sets up the sandbox (one time, a few minutes)
shellforge play      # start
```

**One known rough edge in v0.1.0:** `shellforge init` needs a clone of this
repository, because the installers place the binary and not the sandbox image.
It is tracked as [issue #172](https://github.com/JoottunAtish/ShellForge/issues/172)
and both install guides give the workaround.

If you have never opened a terminal, start with the
[Windows install guide](docs/01-install-windows.md), which assumes nothing and
explains what WSL is before asking you to install it. On Linux the
[Linux install guide](docs/02-install-linux.md) is the short version, and both
cover the manual verify-and-extract alternative if you would rather not pipe a
script from the network.

macOS will probably work through Docker, but it is untested and is labelled
community supported rather than supported.

## What you'll learn

| Act | You'll be able to |
|---|---|
| I. Orientation | Move around a filesystem and read the manual yourself |
| II. Files | Create, copy, move, delete, and batch-handle files with globs |
| III. Plumbing | Redirect output and chain commands into pipelines |
| IV. Search | Find text with `grep` and files with `find` |
| V. Permissions | Read `ls -l`, fix permissions, manage processes |
| VI. Automation | Use environment variables and write your first bash script |

Every act ends in a boss level with no numbered steps, just a broken system and an
instruction to make it work. The campaign ends with a 3 AM incident you have to
diagnose and fix from scratch, then write the post-mortem.

The full ticket list is in [docs/CURRICULUM.md](docs/CURRICULUM.md).

## Can this break my computer?

No, and here is why. Everything happens inside an isolated Linux sandbox that has no
access to your files. On Windows it is a dedicated WSL distribution with Windows
interop and drive automounting switched off, so there is no `/mnt/c` to destroy. On
Linux it is a container with no network and no host mounts.

`rm -rf /` in there destroys the sandbox and nothing else, and typing `reset` at
the prompt rebuilds the level in under a second. It shows you exactly what it
would delete before it does anything, and it only ever touches the level's own
folder. Breaking things is the point.

More detail in [docs/04-how-it-works.md](docs/04-how-it-works.md).

## Is anything sent anywhere?

No. There is no account, no server, and no telemetry, and none of those are planned.
Your progress is a SQLite file on your machine. It works fully offline after install.

## How it works

```
   your terminal
        |
        v
  PTY multiplexer  <-- strips OSC 133 markers, logs every command + exit code
        |
        v
  real bash, inside
        |
   +----+-----------------------------+
   |  Docker container (Linux/macOS)  |
   |  or WSL2 distro (Windows)        |
   +----------------------------------+
        ^
        |
  verification engine  --> reads real state: files, modes, owners, processes, env
```

The shell is instrumented with OSC 133 semantic prompt markers, so the game knows
what you ran, what it exited with, how long it took, and where you were, without
ever intercepting or re-executing your commands. Full design in
[docs/design/ARCHITECTURE.md](docs/design/ARCHITECTURE.md).

## Documentation

| Document | What it covers |
|---|---|
| [docs/](docs/README.md) | Documentation index |
| [docs/03-quickstart.md](docs/03-quickstart.md) | Everything you can type, once you are in |
| [docs/04-how-it-works.md](docs/04-how-it-works.md) | What it installs, what it records, and what it cannot reach |
| [docs/05-troubleshooting.md](docs/05-troubleshooting.md) | Every `doctor` failure, with the fix |
| [CHANGELOG.md](CHANGELOG.md) | What is in each release, and the known issues |
| [docs/CURRICULUM.md](docs/CURRICULUM.md) | All 25 levels: concepts, objectives, checks, hints |
| [docs/LEVEL-FORMAT.md](docs/LEVEL-FORMAT.md) | Level YAML schema and the check catalogue |
| [docs/design/ARCHITECTURE.md](docs/design/ARCHITECTURE.md) | Full system design |
| [CLAUDE.md](CLAUDE.md) | Engineering reference and non-negotiables |
| [PROGRESS.md](PROGRESS.md) | What is built, what is not |

## Contributing

Contributions are welcome, especially levels. A level is a YAML file plus its
assets. Copy the level in `packs/core-linux-basics/levels/` closest to what you
have in mind, then `shellforge author validate` and `shellforge author test`
hold your version to the same contract every shipped level passes.

Read [CONTRIBUTING.md](CONTRIBUTING.md) first, and
[docs/07-authoring-levels.md](docs/07-authoring-levels.md) for how to think about
a level, walked through a real one. The short version: LF line endings, no new
dependencies without asking, every check needs a written failure message, and
every level needs a golden test that passes in CI.

## License

MIT. See [LICENSE](LICENSE).
