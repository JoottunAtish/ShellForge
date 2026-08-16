# Shellforge

**Learn the Linux command line by living in a terminal you can't break.**

[![CI](https://github.com/JoottunAtish/ShellForge/actions/workflows/ci.yml/badge.svg)](https://github.com/JoottunAtish/ShellForge/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go 1.25+](https://img.shields.io/badge/go-1.25%2B-00ADD8.svg)](https://go.dev/dl/)
[![Status: pre-alpha](https://img.shields.io/badge/status-pre--alpha-orange.svg)](PROGRESS.md)

[![Shellforge on GitHub](https://githubcard.com/JoottunAtish/ShellForge.svg?d=4S7ENYlgGRMx)](https://github.com/JoottunAtish/ShellForge)

Shellforge drops you into a real Linux shell inside a disposable sandbox and gives
you jobs to do. Not multiple choice. Not a simulator. You type real commands into a
real `bash` process, and the game checks whether you actually did the thing.

You are a new junior sysadmin at Meridian Logistics. The last engineer left without
documentation. There are 25 tickets waiting.

> **Status: pre-alpha, under active construction.** The design is complete and the
> repository is scaffolded. The engine is not built yet. See [PROGRESS.md](PROGRESS.md)
> for exactly what works today and [docs/design/SEVEN-DAY-PLAN.md](docs/design/SEVEN-DAY-PLAN.md)
> for what lands when. There is no installable release yet.

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
| **Real verification** | 13 check types that read filesystem state, process state, file modes, ownership and shell environment. |
| **Really disposable** | `rm -rf /` inside the sandbox destroys nothing on your machine, and `reset` rebuilds in under a second. |
| **Really offline** | No account, no server, no telemetry. Progress is a SQLite file on your disk. |
| **Really yours** | Levels are declarative YAML. Write your own, ship a pack. |

## Install

Not yet. There is no release to install.

When v0.1 ships, this section becomes:

**Windows** [Windows install guide](docs/01-install-windows.md)
**Linux** [Linux install guide](docs/02-install-linux.md)

```
shellforge doctor    # checks your machine and tells you how to fix anything
shellforge init      # sets up the sandbox (one time, a few minutes)
shellforge play      # start
```

macOS will probably work through Docker, but it is untested and will be labelled
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

`rm -rf /` in there destroys the sandbox and nothing else, and `shellforge reset`
rebuilds it in under a second. Breaking things is the point.

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
| [docs/CURRICULUM.md](docs/CURRICULUM.md) | All 25 levels: concepts, objectives, checks, hints |
| [docs/LEVEL-FORMAT.md](docs/LEVEL-FORMAT.md) | Level YAML schema and the check catalogue |
| [docs/design/ARCHITECTURE.md](docs/design/ARCHITECTURE.md) | Full system design |
| [CLAUDE.md](CLAUDE.md) | Engineering reference and non-negotiables |
| [PROGRESS.md](PROGRESS.md) | What is built, what is not |

## Contributing

Contributions are welcome, especially levels. A level is a YAML file plus its
assets, and `shellforge author scaffold` generates the skeleton for you.

Read [CONTRIBUTING.md](CONTRIBUTING.md) first. The short version: LF line endings,
no new dependencies without asking, every check needs a written failure message, and
every level needs a golden test that passes in CI.

## License

MIT. See [LICENSE](LICENSE).
