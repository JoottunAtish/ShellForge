# Design Record

These are the documents Shellforge was planned from, written before the code. They
are kept because the reasoning in them is worth more than the conclusions, and
because "why did we not do X" is the most expensive question to re-answer.

**They describe intent, not the current build.** Where they disagree with the code,
the code is right and the document is stale. For what actually exists today, read
[PROGRESS.md](../../PROGRESS.md).

| Document | What it settles |
|---|---|
| [PROJECT-BRIEF.md](PROJECT-BRIEF.md) | What v0.1 is, who it is for, what is explicitly cut and why, the success criteria, and the top risks with pre-committed mitigations. |
| [ARCHITECTURE.md](ARCHITECTURE.md) | The full system design: layers, runtime abstraction, PTY instrumentation, verification engine, persistence schema, and the options that were considered and rejected. |
| [SEVEN-DAY-PLAN.md](SEVEN-DAY-PLAN.md) | Day by day execution with demoable exit criteria, the Day 3 go/no-go gate on WSL, and the cut ladder. |
| [DOCUMENTATION-PLAN.md](DOCUMENTATION-PLAN.md) | What each user-facing document must contain and the standard it is held to. |
| [SESSION-PROMPTS.md](SESSION-PROMPTS.md) | Ready to paste prompts, one per build session, for driving the implementation. |
| [DAY-2-TICKETS.md](DAY-2-TICKETS.md) | How Day 2, the content engine and verification, was broken into eight tickets, with the sizing, the ordering, the consolidation from an earlier fourteen, and the two findings that changed the scope. Superseded by the issues once they are filed. |

## The decisions that shape everything else

If you read nothing else here, read these. They are the ones that would be
expensive to reverse.

| Decision | Choice | Why |
|---|---|---|
| Language | Go | The project lives or dies on distribution and Windows support, and Go is strongest at exactly those. Single static binary, trivial cross-compilation, `embed.FS`. |
| Shell | Real bash in a PTY, instrumented with OSC 133 | A custom REPL cannot run `vim`, has no job control, and teaches a fake shell. Parsing `.bash_history` gives no exit codes and no ordering. |
| Windows runtime | A dedicated WSL2 distribution via `wsl --import` | Fewer prerequisites than requiring Docker Desktop, and fewer prerequisites means fewer people dropping off at step three. |
| Base distro | Debian, not Alpine | Busybox utilities diverge from GNU coreutils and would teach wrong flags. |
| Verification | State, not syntax | Checking what was typed punishes a learner who found a better solution, which is the fastest way to kill a teaching tool. Syntax checks are bonus objectives only. |
| First front end | Plain CLI, no TUI | A TUI is two full days on its own and proves nothing about the engine. It is on the cut list. |
| Reset on WSL | Wipe the scratch directory | Sub-second, versus 10 to 60 seconds for an export and import round trip. |
| LLM tutor | Deferred, opt-in when it lands | The game must be fully playable offline with no account and no key. |

## Things deliberately left out of v0.1

The full list with reasoning is in [PROJECT-BRIEF.md](PROJECT-BRIEF.md) section 5.
The short version: TUI, skill-tree visuals, dungeon crawl, escape room, forensics,
command golf, speedruns, rewind, tool cards, daily challenge, Podman, bubblewrap,
native runtime, supported macOS, networking levels, systemd levels, remote content
packs, self-update, spaced repetition, and telemetry.

Most of those are content or feature layers on top of a working engine. They are
worthless if the engine is not done, which is the whole argument for the scope of
v0.1.
