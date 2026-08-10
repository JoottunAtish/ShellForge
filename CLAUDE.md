# CLAUDE.md: Shellforge Engineering Reference

> Claude Code reads this file on every session. It is deliberately short. The
> detailed rules live in `.claude/skills/` and load on demand, so that a session
> about the OSC parser does not carry the level authoring rules and vice versa.
>
> Keep this current. A stale CLAUDE.md is worse than none.

---

## What this project is

A single-binary Go game that teaches the Linux command line by running **real bash
in a sandboxed Linux environment** and verifying **real system state**. Not a
simulator. Levels are declarative YAML data.

Timeline: **7 days.** Scope is deliberately narrow. When in doubt, do the simpler
thing and note the shortcut with `// TODO(v0.2):`.

**Read `PROGRESS.md` before assuming anything exists.** This repository is a Day 0
scaffold and most of what the design documents describe is not built yet.

---

## Rule 0: no em dashes, no en dashes, no smart punctuation

**Applies to everything: code, comments, commit messages, documentation, level
briefings, `on_fail` messages, hint text, CLI output, and replies in chat.**

Banned: U+2014 em dash, U+2013 en dash, U+2010 to U+2015, U+2212 minus sign,
U+2018 and U+2019 curly single quotes, U+201C and U+201D curly double quotes,
U+2026 ellipsis, U+00A0 and U+2009 and U+202F non-breaking and thin spaces.

Rewrite an em dash as a colon, a comma, parentheses, or a full stop. A hyphen joins
words; it does not separate clauses. Use a plain ASCII hyphen for ranges.

```bash
./scripts/check-punctuation.sh    # hard CI gate, not a warning
```

Full guidance, with worked rewrites and the prose rules for user-facing text, is in
the **`writing-style`** skill.

---

## Non-negotiables

Violating any of these breaks the product, not just the code.

1. **The learner's shell is real bash in a real PTY.** Never implement a command
   interpreter, never intercept and re-execute commands. `vim`, `less`, `htop`, job
   control, and tab completion must all work.
2. **Nothing on the host is reachable from inside the sandbox.** No host bind mounts
   except the read-only `/opt/shellforge`. `--network none` unless a level declares
   `requires: [networking]`. Never `--privileged`.
3. **Verification checks state, not syntax**, except where a level explicitly marks
   a syntax check as a bonus objective.
4. **Checks never mutate.** Read-only. CI enforces via a before and after filesystem
   hash.
5. **Setup is deterministic and offline.** No network calls during setup, ever.
   Seeded generators or embedded assets only.
6. **Every user-facing error says what failed, why, and the next command to run.**
   No bare Go errors reach the terminal. Use
   `internal/platform/ux.Fail(op, err, remediation, docAnchor)`.
7. **`.gitattributes` enforces LF.** Strip `\r` from every file materialized into
   the sandbox. CRLF in a `.sh` produces `bad interpreter: /bin/bash^M`.
8. **Nothing destructive ever escapes the sandbox.** The README promises a beginner
   they cannot break their laptop. See the **`destructive-safety`** skill before
   writing anything that deletes.
9. **No telemetry.** Not opt-in, not anonymous. There is nothing to enable.

---

## Skills: load what the task needs

Do not load everything. Match the work.

| Skill | Load it before |
|---|---|
| **`implementation`** | Any coding task. Start here; it routes to the rest. |
| **`ticket-creation`** | Writing an issue for work to be done |
| **`code-navigation`** | Finding or understanding existing code or docs |
| **`writing-style`** | Any prose, error string, comment, briefing, or commit message |
| **`go-style`** | Any Go code |
| **`destructive-safety`** | Anything that deletes, unregisters, unmounts, or resolves a destructive path |
| **`security`** | Spawning a process, handling a pack path, verifying a download |
| **`testing`** | Writing tests, or deciding what to test |
| **`component-traps`** | The PTY, a runtime, verify, store, the image, or Windows |
| **`level-authoring`** | A level, an asset, or the content pack |
| **`github-workflow`** | Branch, issue, label, PR, review comment, commit |
| **`review-code`** | Auditing a diff for bugs and safety flaws |
| **`review-pr`** | Reviewing a pull request on GitHub |

---

## Architecture: layers and the dependency rule

```
L5 cmd/shellforge      CLI, output rendering
L4 internal/game       orchestrator, scoring, DAG, achievements, eventbus
L3 internal/content    pack loading and validation
   internal/verify     check registry and engine
L2 internal/pty        PTY multiplexer, OSC parser
   internal/journal    command journal, snapshots
L1 internal/runtime    Runtime interface plus docker and wsl implementations
   internal/sandbox    image spec, provisioning
L0 internal/store      sqlite
   internal/platform   OS probes, console modes, paths
   internal/doctor     preflight
```

**Dependencies point downward only.** `internal/game` must never import a Docker or
WSL type. If you need runtime information at L4, add it to
`Runtime.Capabilities()`. This rule is what makes "drop WSL support" a one-day
decision instead of a rewrite, and that is a scheduled decision point on Day 3.

Enforced by `internal/archtest`, which parses every import in the module and also
fails on a network client appearing at or above L3. To add a legitimate edge, edit
its tables and justify it in the commit message. Do not delete the test.

---

## Repo layout

```
cmd/shellforge/                     CLI entry point and command table
internal/{doctor,runtime,sandbox,pty,journal,content,verify,game,store,platform}/
internal/archtest/                  layer dependency enforcement
packs/core-linux-basics/            pack.yaml, levels/, assets/
images/                             Containerfile, wsl.conf, rc/, bin/
docs/                               user docs; docs/design/ holds the design record
scripts/                            check-punctuation.sh, check-links.sh, sync-labels.sh
.claude/skills/                     the detailed rules, loaded on demand
.github/workflows/                  ci.yml
```

Two documents are living contracts and must be updated in the same commit as the
code they describe:

- **`docs/LEVEL-FORMAT.md`** for any level schema or check catalogue change.
- **`docs/05-troubleshooting.md`** must contain a heading for every `DocAnchor` any
  probe emits. CI fails the build otherwise.

`docs/design/` describes intent, not the current build. When it disagrees with the
code, the code is right.

---

## Commands

```bash
make build           # go build -o bin/shellforge ./cmd/shellforge
make test            # go test ./...
make race            # go test -race ./...
make fuzz            # fuzz the OSC parser for 60s
make lint            # gofmt + vet + punctuation + links + layer test
make sec             # govulncheck + gosec
make image           # build the sandbox image
make rootfs          # export the WSL rootfs tarball
make run LEVEL=nav-01
make golden          # author test across every level
make ci              # everything
```

On Windows use `.\make.ps1 <target>`. `make` is not installed on a default Windows
box, which is why both exist.

`make fuzz` is deliberately not part of `make ci`: CI already runs a 30 second fuzz
tripwire on every pull request, and a full 60 second local run on top of that would
only slow the everything-target down without adding coverage worth the wait.

Run before every commit:

```bash
gofmt -s -w . && go vet ./... && go test ./... && ./scripts/check-punctuation.sh
```

---

## What NOT to do

- Don't use an em dash or an en dash. See Rule 0.
- Don't build the TUI. Plain CLI only this week. It is on the cut list for a reason.
- Don't add check types beyond the 13 in `docs/LEVEL-FORMAT.md`. Use `type: script`.
- Don't implement Podman, bwrap, native, or SSH runtimes.
- Don't add remote content pack downloading. Embedded pack only.
- Don't build a command string and pass it to a host shell.
- Don't loosen a level's checks to make its solution pass. The level is wrong.
- Don't tighten a level's checks without confirming they still accept valid
  alternative solutions.
- Don't parse `~/.bash_history`. The journal is the source of truth.
- Don't let a check write to the sandbox.
- Don't add a dependency without asking.
- Don't commit a binary, a rootfs tarball, or a `.vhdx`.

---

## Glossary

| Term | Meaning |
|---|---|
| **Pack** | A directory of levels plus assets plus `pack.yaml` |
| **Level** | One task: briefing, setup, checks, hints, solution |
| **Objective** | A named sub-goal shown as a checklist item; maps 1:1 to a check id |
| **Check** | A single verifiable assertion about sandbox state |
| **Journal** | Ordered record of every command with exit code, cwd, timing |
| **Runtime** | A backend that can host the sandbox (Docker, WSL) |
| **Sandbox** | The disposable Linux environment the learner plays in |
| **Golden test** | Setup, checks fail, solution, checks pass, teardown clean |
| **Par** | Command count a competent user needs; drives the efficiency bonus |

---

## References

External standards this project is built on. When something here is ambiguous, go
to the source.

- [Effective Go](https://go.dev/doc/effective_go)
- [Google Go Style Guide](https://google.github.io/styleguide/go/)
- [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments)
- [Security Best Practices for Go Developers](https://go.dev/doc/security/best-practices)
- [How Go Mitigates Supply Chain Attacks](https://go.dev/blog/supply-chain)
- [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck)
- [gosec](https://github.com/securego/gosec)
