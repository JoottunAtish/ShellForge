# Shellforge Project Brief (v0.1, one-week build)

## 1. What it is

A single-binary, offline game that teaches basic→intermediate Linux command line by dropping the learner into a **real, disposable Linux shell** and verifying **real system state** after each task.

Not a quiz. Not a simulated shell. The learner types `grep -ri ERROR logs/ | wc -l` into an actual bash process and the game checks whether the file they produced is correct.

**Tagline:** *Learn the terminal by living in one you can't break.*

## 2. Who it's for

| Audience | Need |
|---|---|
| CS students on Windows laptops | Have never used a terminal; classes assume they have |
| Bootcamp / self-taught devs | Can copy-paste commands, can't compose them |
| Junior devs | Know 10 commands, need the other 40 |

**Primary persona - "Priya, 19, 1st-year CS, Windows 11":** has never opened a terminal, is scared of breaking her laptop, has been told to "just install WSL" with no further help. She must go from `git clone` to playing in under 10 minutes without asking anyone.

That persona is the entire justification for `doctor` and the install docs. If Priya drops off at step 3, nothing else matters.

## 3. The one-week reality check

The full architecture describes a 6-milestone product. Seven days delivers **M0-M2 plus content**. Be ruthless about this now rather than on Day 5.

Budget assumption: **7 days × ~8 h = ~56 h**, with Claude Code doing implementation. Rough split:

```
Engine + runtimes + instrumentation   ~22 h   (Claude Code heavy)
Content authoring (25 levels)         ~16 h   (you heavy - this is the bottleneck)
CI, packaging, cross-platform test     ~8 h
Documentation                          ~7 h
Buffer / integration pain              ~3 h
```

**The bottleneck is content, not code.** Claude Code will write the verification engine faster than you can write 25 good level briefings with hints and fail messages. Plan accordingly: start content on Day 2, not Day 5.

## 4. Scope - IN for v0.1

| Area | Included |
|---|---|
| Language | Go, single static binary, cross-compiled |
| Runtimes | `DockerRuntime` (Linux/macOS), `WslRuntime` (Windows, `wsl --import`) |
| Interface | **Plain-CLI only** - colored, well-formatted, no TUI |
| Shell | Real bash in a PTY, OSC 133 instrumentation, full `vim`/`less` support |
| Content | 25 levels, 6 acts, campaign narrative, embedded in binary |
| Verification | 13 check types, state-based, per-objective results |
| Progression | SQLite, XP, 5 ranks, ~10 achievements, hint ladder with XP cost |
| Commands | `doctor` `init` `play` `run` `check` `hint` `reset` `skip` `map` `stats` `sandbox` `author` |
| Quality | Golden test per level in CI, Linux + Windows matrix |
| Docs | README + install (Win/Linux) + quickstart + troubleshooting + uninstall |

## 5. Scope - OUT for v0.1 (say this out loud, in the README)

Deferred, with the reason:

| Cut | Why |
|---|---|
| **TUI (bubbletea)** | 2 full days on its own; plain-CLI proves the engine and tmux users prefer it |
| **Skill-tree visual map** | `map` prints a text tree instead |
| Dungeon crawl, escape room, forensics, golf, speedrun, rewind, tool cards, daily challenge | All are content/feature layers on a working engine - worthless if the engine isn't done |
| Podman / bwrap / native runtimes | Docker covers Linux; two runtimes is already the schedule risk |
| macOS as a *supported* platform | Will likely work via Docker; untested, so label it "community, unsupported" |
| Networking + systemd levels | Need a mock server / `systemd=true` boot; both are Day-8 problems |
| Remote content packs, self-update | Embedded pack only |
| Spaced repetition, snapshots/rewind | Needs a working mastery + snapshot layer first |
| LLM tutor | Explicitly v2, opt-in, and the game must never need it |

## 6. Success criteria (binary, testable)

1. On a **clean Windows 11 VM**, following only `docs/01-install-windows.md`, a user goes from nothing to playing level 1 in **< 10 minutes**, with no step requiring outside help.
2. `shellforge doctor` correctly diagnoses and prints a fix for: virtualization disabled, WSL absent, WSL1-only, Docker daemon down, insufficient disk.
3. All 25 levels pass the golden test in CI on Linux and Windows: *checks fail before solution, pass after, teardown leaves no residue.*
4. `rm -rf /` inside the sandbox destroys nothing on the host and `shellforge reset` recovers in < 5 s.
5. `vim`, `less`, and `htop` work correctly inside the instrumented shell (resize included).
6. Three people who have never used a terminal complete Act I unaided.
7. Total install footprint < 2.5 GB; uninstall leaves zero orphaned files (no stray 2 GB `.vhdx`).

## 7. Top risks and pre-committed mitigations

| # | Risk | Probability | Mitigation / trigger |
|---|---|---|---|
| R1 | **WSL runtime eats Day 3 and beyond** | High | **Hard gate: if `WslRuntime` isn't passing contract tests by end of Day 3, drop it.** Fall back to "Windows requires Docker Desktop" - which still runs on WSL2, so the original requirement is technically met. Ship, don't polish. |
| R2 | PTY + OSC 133 parsing breaks full-screen apps | Medium | Build the PTY spike on **Day 1**, before anything else. If `vim` doesn't work by end of Day 1, you have a fundamental problem and need to know immediately. |
| R3 | Content authoring overruns | High | Write levels 1-8 on Day 2 alongside the engine. Ship 18 levels rather than 25 if needed - Acts I-IV are the ones that matter. |
| R4 | CRLF breaks every script on Windows | High | `.gitattributes` in commit #1. Strip `\r` when materializing files. Test on Windows Day 3, not Day 6. |
| R5 | Windows CI (WSL on GH runners) is flaky | Medium | Manual VM test as the fallback gate; don't block the release on green Windows CI. |
| R6 | Scope creep into TUI because it's fun | Certain | It's in the cut list above. Re-read it. |

## 8. Definition of Done (project level)

- [ ] `go build` produces working binaries for `windows/amd64`, `linux/amd64`, `linux/arm64`
- [ ] GitHub release with checksums, `install.sh`, `install.ps1`
- [ ] Clean-VM install test passed on Windows 11 and Ubuntu 24.04
- [ ] All levels green in CI golden tests
- [ ] `docs/` complete, every `doctor` error links to a real anchor
- [ ] README has a demo GIF (record it - it triples star rate)
- [ ] LICENSE (MIT), CONTRIBUTING, level-authoring guide

## 9. What "good" looks like on Day 7

Someone clones the repo on a Windows laptop, runs two commands, and 8 minutes later is grepping through fake server logs to find out who deleted the payroll file - and is annoyed when they run out of levels.
