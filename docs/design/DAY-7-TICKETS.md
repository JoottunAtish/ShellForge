# Day 7 Ticket Plan: documentation and release

Status: plan of record for Day 7. Filed as issue #171, a single issue, deliberately,
for the same reason #152 and #158 were.

This document is the reasoning. The issue is the specification. Where the two
disagree, the issue is right, because that is what an implementer reads.

---

## 1. What Day 7 has to produce

`SEVEN-DAY-PLAN.md` gives Day 7 seven tasks under one goal: "the docs are the
product for anyone who has not installed it yet."

| # | Task | Est |
|---|---|---|
| 7.1 | `README.md`: hook, demo GIF, 60 second quickstart, what it teaches, safety note | 1.5 h |
| 7.2 | `docs/01-install-windows.md`, the most important file in the repo. Screenshots. Every `doctor` failure has an anchor here. | 2 h |
| 7.3 | `docs/02-install-linux.md`, `03-quickstart.md`, `04-how-it-works.md`, `05-troubleshooting.md`, `06-uninstall.md` | 2 h |
| 7.4 | `docs/07-authoring-levels.md` plus `CONTRIBUTING.md` | 1 h |
| 7.5 | Record the demo GIF (VHS, scripted and re-recordable) | 1 h |
| 7.6 | CI check: every doc anchor emitted by `doctor` exists in the docs | 0.5 h |
| 7.7 | Tag v0.1.0, publish the release, write the announcement post | 1 h |

Exit criteria: a person who has never seen the project installs and plays using
only the README, and `v0.1.0` is released with binaries and the rootfs artifact
attached.

One of the seven is not work here, and one is only half work here.

**7.6 is already in flight and is not this ticket.** The gate exists in the
`Docs` job of `ci.yml`, and issue #86 established that it has been reporting
green while checking essentially nothing: it greps for the struct literal form
`DocAnchor: "..."`, and every real call site passes the anchor as the fourth
positional argument to `ux.Fail`. `cmd/shellforge/docanchor_test.go` closed the
blind spot for one package by parsing the AST. Issue #132 is the collapse of the
three surviving implementations into one, and PR #160 is open against it.
Day 7 must not touch that gate. Changing a gate inside a documentation pull
request is the move the workflow forbids, whether the change tightens it or
loosens it.

**7.7 is half work here.** `release.yml` is complete and self-verifying: it
builds three archives, exports the rootfs, writes one `SHA256SUMS`, verifies it
against what was built, proves `shellforge version` prints the tag, and uploads.
Nothing in it needs writing. What Day 7 owes is the part a workflow cannot do:
prose that is true after a tag exists, a changelog entry, and a written runbook
so the tag push is a checklist rather than a memory. The tag push itself happens
after the merge, by hand, and is recorded in the issue rather than in the diff.

That leaves 7.1, 7.2, 7.3, 7.4, 7.5 and the mergeable half of 7.7.

---

## 2. Why one ticket and not six

The `ticket-creation` skill says one ticket is one branch is one pull request,
and that a ticket with more than about eight acceptance criteria is two tickets.
This one has more than that and is still one ticket.

The six parts are one claim: **a stranger can read this repository and end up
playing the game.** They only prove out together.

- A README that promises a 60 second quickstart, pointing at an install guide
  that still carries a "do not follow this yet" banner, is worse than either
  document alone. The reader trusts the first page and gets stopped by the
  second.
- The demo GIF is the README's first paragraph in visual form. Recording it
  against a build whose prose has not been reconciled means recording the wrong
  interface.
- The command surface fix (section 3.2) has to land with the docs it corrects,
  because the gate that keeps it correct is the same commit.
- Splitting the seven guides across six pull requests means six reviews of the
  same question ("is this true today?") with five of them unable to answer it,
  because the answer depends on the other five.

The counter argument, recorded honestly: this diff touches Markdown, Go, a VHS
tape, a binary GIF and PNG assets, and a changelog, in one sitting. That is a
real review cost. It is accepted because the alternative is six pull requests
where the exit criterion cannot be evaluated until the last one merges.

---

## 3. Findings that change the plan

Nine things turned up while planning. Some are decisions, most are defects
sitting in the tree today.

### 3.1 The repository still tells every reader that it does not work

`README.md` line 19 says, in a block quote at the top of the page:

> **Status: pre-alpha, under active construction.** The design is complete and
> the repository is scaffolded. The engine is not built yet.

Twenty-five levels pass the golden contract against a real Docker daemon. The
game core, the curriculum DAG, fourteen achievements, two installers and a
release workflow are all in. That sentence has been false for weeks.

It is not alone. Seven of the nine user-facing documents carry a status banner
saying they are outlines to be written on Day 7. `docs/README.md` closes with
"Do not follow them yet; there is nothing to install." `CLAUDE.md` line 21 tells
every Claude Code session that "this repository is a Day 0 scaffold and most of
what the design documents describe is not built yet", and
`.claude/skills/code-navigation/SKILL.md` line 68 repeats it.

Day 7 is the day those sentences stop being true in the file as well as in
reality. The banner sweep is listed first in the ticket because it is the
cheapest thing on the list and the single largest gap between what the project
is and what a reader believes it is.

### 3.2 Five commands the documentation tells people to run do not exist

An inventory of every `shellforge <verb>` string in `README.md`,
`CONTRIBUTING.md` and `docs/*.md`, checked against the fourteen verbs
`NewRootCommand` actually registers:

| Documented | Reality |
|---|---|
| `shellforge uninstall` | Not a registered verb. Cobra prints its unknown command error. |
| `shellforge export --format=json` | Not a registered verb. Same. |
| `shellforge author scaffold` | Registered, but a stub whose error says it "lands on Day 2". |
| `shellforge --ascii` | `--ascii` is a flag on `map`, not on the root command. |
| `shellforge reset --hard` | `reset` is an in-level command and takes `--yes`, not `--hard`. |

The first two are issue #139, already filed and honestly flagged in
`06-uninstall.md` itself. The other three are not filed anywhere.

`docs/design/DOCUMENTATION-PLAN.md` lists "every command shown in the docs is
copy-pasteable and correct, run them" as a Day 7 checklist item. A checklist
item is a person remembering. Five of them got through anyway, in a repository
that gates punctuation, layer dependencies, link targets and CLI package
existence. So Day 7 turns that checklist item into a gate:
`cmd/shellforge/docs_commands_test.go` parses the command strings out of the
documentation, resolves each against the real cobra tree, and fails on a verb
that does not exist, a flag the resolved command does not accept, or a verb that
resolves to a stub.

This is the one piece of Go in an otherwise prose ticket and it is what keeps
the prose true after Day 7 ends.

### 3.3 `author scaffold` gets removed from the docs, not built

Four documents tell a contributor to run `shellforge author scaffold <id>`:
`README.md`, `docs/07-authoring-levels.md`, `docs/LEVEL-FORMAT.md` section 8,
and `packs/core-linux-basics/levels/README.md`. The `level-authoring` skill
opens with it too.

Two options. Build it, which is real work in a documentation pull request, or
stop documenting a verb that refuses. Building it is the tempting answer and it
is the wrong one for today: `author scaffold` is a P2 contributor convenience,
Day 7 has a release to cut, and the cut ladder puts `author record` on it
explicitly. So the authoring documentation shows the real path, which is copying
an existing level YAML, and the stub's own message stops naming a day that
passed a month ago.

The same applies to `author record`, which the Day 6 plan already recorded as
still a stub.

### 3.4 The Linux install page requires a runtime the project refuses to implement

`docs/02-install-linux.md` lists under Requirements:

> A container runtime: Docker or Podman.

`CLAUDE.md` says, under What NOT to do: "Don't implement Podman, bwrap, native,
or SSH runtimes." `internal/sandbox.Resolve` knows about Docker and WSL. A
reader who installs Podman on the strength of that line gets `no-runtime-available`
from `doctor` and no explanation of why.

Docker only, and the page says so plainly, with a one line note that Podman is a
v0.2 question rather than an oversight.

### 3.5 The quickstart undersells the game by sixteen levels

`docs/03-quickstart.md` says:

> The levels you can play today: Nine, in this order: `nav-01` to `nav-04`,
> `files-01` to `files-04`, and `pipe-05`. Levels 9 to 25 arrive later.

Levels 9 to 25 landed in #152 and all twenty-five pass the golden contract. The
page is otherwise accurate and well written, which is what makes this
particular stale paragraph expensive: everything around it reads current, so a
reader has no reason to doubt it.

### 3.6 The trust document names the wrong directory on Linux

`docs/04-how-it-works.md` section 4 says the database and the configuration file
both live in `~/.local/share/shellforge` on Linux. `internal/platform/paths.go`
puts the progress database there and `config.toml` in `~/.config/shellforge`,
with the cache in `~/.cache/shellforge` and logs beneath it.

On the page whose entire job is "here is exactly what this program writes to
your disk and here is how to delete it", a missing directory is the defect that
matters most. Section 5 has to enumerate all four, and `06-uninstall.md` has to
remove all four.

### 3.7 Uninstall is documented against two verbs that will not exist at v0.1.0

Issue #139 is open and `06-uninstall.md` flags the gap in its own banner, which
is the right thing to have done and is not a place to leave a release.

The decision is to close #139 by making the page describe only what exists.
Day 6's exit criterion is "uninstall verified: no orphaned vhdx, no stray
config", and a page that names the four absolute directories and the binary
satisfies it. Building an `uninstall` verb satisfies it more elegantly and is
`cut: v0.2` work: it needs the marker check, the refusal tests, and the
"verify afterwards and name the path to check in Explorer" behaviour that the
`destructive-safety` skill requires of anything that deletes an install
directory. That is not a paragraph, it is a ticket.

`export` goes the same way. The progress database is one SQLite file at a known
path, so "copy this file" is the honest instruction, and it is one a beginner
can follow.

### 3.8 The Windows screenshots need a machine this ticket may not have

`docs/01-install-windows.md` carries eight `[SCREENSHOT: ...]` placeholders, and
`docs/assets/README.md` lists ten shots to capture "on Day 7, against a clean
Windows 11 VM".

Screenshots are the right call for that page and the placeholders are not. A
released v0.1.0 whose most important document contains the literal text
`[SCREENSHOT: winver]` reads as abandoned. So the rule is: no placeholder
survives the merge. Either the image is committed and referenced, or the
placeholder is deleted and the surrounding prose carries the step on its own.
Both outcomes are acceptable and the second is not a failure.

`.gitattributes` already declares `*.png` and `*.gif` binary and marks `docs/**`
as documentation for language statistics, so committing the assets was
anticipated.

### 3.9 The demo recording has to be reproducible and must not show a real save file

`docs/assets/README.md` already chose VHS over a screen recorder, for the right
reason: a hand recorded GIF goes stale silently and a scripted one does not. Two
things the storyboard does not say, and the tape has to:

1. The recording runs against a throwaway progress database, by pointing
   `XDG_DATA_HOME` at a temporary directory. Otherwise the XP totals, the rank
   and the achievement list in the GIF are whoever recorded it, and re-recording
   after the next level pass produces a different GIF for no reason.
2. It uses `shellforge run pipe-05` rather than `shellforge play`. `play`
   resolves the next unlocked level from the learner's own progress, which is
   the opposite of reproducible. `pipe-05` is the level the storyboard was
   written against: its briefing opens "02:41. Billing service." and it is the
   one the ERROR count sequence belongs to.

---

## 4. What Day 7 does not do

- **Touch the doc anchor gate.** 7.6, covered by #86, #132 and open PR #160.
- **Build `uninstall`, `export`, `author scaffold` or `author record`.** All
  `cut: v0.2`. Day 7 makes the documentation stop naming them.
- **Push the v0.1.0 tag inside the pull request.** The tag is pushed after the
  merge, against the merged `main`, following the runbook the pull request adds.
- **Write the announcement post into the repository.** It is a post for a forum,
  not a repository artifact. The changelog entry is the part that belongs here.
- **Play the campaign start to finish in one sitting.** That is the Day 5 exit
  criterion carried through Day 6, it needs a person and a few hours, and it is
  the acceptance test for the release rather than part of this diff.
- **Run the clean VM install (6.8).** Same shape: a person, two virtual
  machines, and only the written documentation. It gates the tag, not the merge.

---

## 5. Ordering, and the chicken and egg in 7.7

The install sections have to be written for the world in which `v0.1.0` exists,
because that is the world every reader of the merged `main` is in five minutes
later. Today they say, correctly, that the installers fail with
`could not resolve the latest release` because there is no tag.

So the order is fixed and the runbook states it:

1. Merge this pull request, with the install prose written as though v0.1.0 is
   tagged.
2. Push the tag immediately. `main` is briefly ahead of reality and the window
   is minutes, not days.
3. Confirm `release.yml` attached all six assets, then run both installers from
   the published release on a clean machine.

Inverting steps 1 and 2 does not help: tagging first publishes a release whose
README still says there is no release.

---

## 6. References

- `docs/design/SEVEN-DAY-PLAN.md`, Day 7 and the exit criteria
- `docs/design/DOCUMENTATION-PLAN.md`, the file map, the `01-install-windows.md`
  outline, the trust document's five questions, the Day 7 quality gates, and the
  demo storyboard
- `docs/design/DAY-6-TICKETS.md`, for the one-ticket precedent and the
  `bug-report` journal decision that `04-how-it-works.md` section 4 describes
- `.github/workflows/release.yml`, the asset names and the tag push trigger
- `internal/platform/paths.go`, the four directories the uninstall page enumerates
- Issues #86, #132, #139, and PR #160
- Issue #171, the ticket this document is the reasoning for
