# Shellforge Documentation

## If you are here to use Shellforge

Read these in order. They are numbered because that is the order a new user needs
them in.

| # | Document | Read it when |
|---|---|---|
| 01 | [Install on Windows](01-install-windows.md) | You are on Windows and have never used a terminal. **Start here.** |
| 02 | [Install on Linux](02-install-linux.md) | You are on Linux and comfortable with a package manager. |
| 03 | [Quickstart](03-quickstart.md) | It is installed and you want to know how to play. |
| 04 | [How it works](04-how-it-works.md) | You want to know what it installed, whether it can see your files, and what it records. |
| 05 | [Troubleshooting](05-troubleshooting.md) | Something is broken. Every `doctor` error links here. |
| 06 | [Uninstall](06-uninstall.md) | You want it gone, completely, including the 2 GB disk image. |
| 07 | [Authoring levels](07-authoring-levels.md) | You want to write a level. |

## If you are here to work on Shellforge

| Document | What it is |
|---|---|
| [CLAUDE.md](../CLAUDE.md) | **The engineering reference.** Non-negotiables, layer rules, Go style, security rules. Read this first. |
| [LEVEL-FORMAT.md](LEVEL-FORMAT.md) | The contract between content and engine: level schema and the check catalogue. |
| [CURRICULUM.md](CURRICULUM.md) | All 25 levels: concepts, objectives, checks, hints, teaching notes. |
| [CONTRIBUTING.md](../CONTRIBUTING.md) | How to set up, what a good level looks like, how to open a pull request. |
| [SECURITY.md](../SECURITY.md) | Threat model and how to report a vulnerability. |
| [PROGRESS.md](../PROGRESS.md) | What is built and what is not. |

### The design record

[docs/design/](design/README.md) holds the documents the project was planned from.
They are a record of intent, not a description of what exists today. When they
disagree with the code, the code is right and the document is stale.

## Two documents are living contracts

Most of the design record is frozen. Two files are not, and changing them has
consequences:

- **[LEVEL-FORMAT.md](LEVEL-FORMAT.md)** must be updated in the same commit as any
  change to the level schema or the check catalogue. The validator and the docs
  drifting apart is how content authors lose trust in both.
- **[05-troubleshooting.md](05-troubleshooting.md)** must contain a heading for
  every `DocAnchor` any `doctor` probe emits. CI fails the build otherwise, because
  a diagnostic that links to a page nobody wrote is worse than no link at all.

## Documentation standards

The reader is anxious and has no context. From
[the documentation plan](design/DOCUMENTATION-PLAN.md):

- Explain what a thing **is** before telling anyone to install it.
- Never write "just" or "simply".
- Define jargon at first use: WSL, distro, shell, sandbox, PATH.
- Every command shown must be copy-pasteable and correct. Run it first.
- No em dashes, no en dashes, no smart quotes. A smart quote in a shell command is
  a broken shell command. CI enforces this.

## Status

The design record is complete. The user-facing guides numbered 01 to 07 are
outlines, and they are written properly on Day 7 against a build that actually
installs. Do not follow them yet; there is nothing to install.
