---
name: ticket-creation
description: Writing a self-contained Shellforge implementation ticket that Claude Code can execute without asking a follow-up question. Load when asked to create a ticket, write an issue for work to be done, break a feature into tickets, plan a piece of work, or convert a design document section into actionable issues. Supplies the required template, the self-containment test, and a worked example.
---

# Writing an implementation ticket

## The standard

**An implementation ticket is done when someone who has never seen this project
can pick it up and finish it without asking a single question.**

That is a high bar and it is the point. A ticket that says "implement the OSC
parser" is not a ticket, it is a reminder. The person who writes the ticket has the
context loaded; the person who implements it does not. Every question the
implementer has to ask is a context switch for two people and a day of latency.

Write the ticket for a competent stranger, not for yourself next Tuesday.

## Which template

Use `.github/ISSUE_TEMPLATE/04-implementation-ticket.yml` for planned work. The
other three templates are for reports coming in from outside: install problems,
bugs, and level problems. Those describe a symptom. This one describes work.

## The template

Every section is required. "Not applicable" is an acceptable answer and is
different from leaving a section out, because it tells the implementer you thought
about it.

````markdown
### Summary

One sentence. What will be true when this is done that is not true now.

### Context

Why this exists and why now. Link the design document section, the failing
behaviour, or the ticket that blocked on it. Two or three sentences. The
implementer needs to know enough to make good judgement calls when the
specification runs out, which it always does.

### Layer and files

Where this code goes, per the layer map in CLAUDE.md.

- Layer: L2
- Package: `internal/pty`
- New files: `internal/pty/osc.go`, `internal/pty/osc_test.go`
- Modified: `internal/pty/mux.go`
- May import: L1 and below
- Must not import: anything above L2

### Interfaces

The exact signatures to implement, or the exact YAML shape to support. If the
implementer has to invent an API, two people will invent two different ones.

```go
type Parser struct{ ... }

// Write consumes a chunk of PTY output, forwards non-marker bytes to w, and
// emits any recognised markers on the returned channel.
func (p *Parser) Write(b []byte) (n int, err error)
func (p *Parser) Events() <-chan Event
```

### Acceptance criteria

Binary and checkable. Not "works well". Someone else must be able to verify each
one without asking what you meant.

- [ ] Markers split across two `Read` calls at any byte offset are recognised
- [ ] A recorded vim session passes through byte-identical apart from our markers
- [ ] `printf '\e]133;fake\a'` typed by the user does not corrupt parser state
- [ ] Unrecognised OSC payloads are forwarded unmodified

### Approach

The intended design, and the traps. Include what has already been ruled out and
why, so the implementer does not rediscover a dead end.

Streaming byte-level state machine, not a regex over a buffer. A buffered
implementation drops markers roughly one percent of the time under heavy output,
which is frequent enough to matter and rare enough to cost a day to diagnose.

### Out of scope

Explicit. This is the section that prevents scope creep and the section that
prevents a reviewer asking "why didn't you also".

- Keystroke tapping for tab-completion detection. Separate ticket.
- Windows console VT mode. Covered by issue #12.

### Safety review

**Required on every ticket. Answer it even when the answer is no.**

- Does this touch deletion, path resolution, WSL, mounts, elevation, or uninstall?
- If yes: which destructive-safety rules apply, and which refusal tests are
  required?

No. This code only reads a byte stream and never touches the filesystem.

### Test plan

What proves it works, including the failure paths. A ticket whose test plan is
"add unit tests" has not been thought through.

- Table test: markers split at every byte offset from 0 to len(marker)
- Golden test: recorded vim byte stream in, byte-identical stream out
- Fuzz target: `FuzzParser`, seeded with real captures
- Race: the parser runs on the PTY read goroutine

### Docs to update

- `docs/design/ARCHITECTURE.md` section 4.6 if the marker set changes
- `PROGRESS.md`

### Skills to load

`implementation`, `go-style`, `component-traps`, `testing`

### References

- `docs/design/ARCHITECTURE.md` section 4.6
- `docs/design/SESSION-PROMPTS.md`, Day 1 Session B
- OSC 133 semantic prompts specification

### Definition of done

The standard checklist from the `implementation` skill, plus anything specific to
this ticket.
````

## The self-containment test

Before you file it, read the ticket as though you know nothing about Shellforge
and answer these. If any answer is no, the ticket is not ready:

1. Do I know **which files** to create or change?
2. Do I know **what layer** this is, and what it may import?
3. Do I know the **exact signatures**, or the exact data shape?
4. Can I tell, **without asking**, whether I am finished?
5. Do I know what is **deliberately excluded**?
6. Do I know whether this can **damage the user's machine**, and what guards are
   required if so?
7. Do I know **what to test**, including the failure paths?
8. Do I know **which documents to update**?
9. Are the **references** specific, naming a section rather than a whole document?

Question 4 is the one that fails most often. "Implement the verification engine"
has no finish line. "All thirteen check types registered, each with a table test,
and `pipe-05` passing the golden test" does.

## Sizing

- **One ticket is one branch is one pull request.** If the acceptance criteria
  cannot be satisfied in one focused sitting, split it.
- **Split by capability, not by file.** "Add `file_content` and `file_exists`
  checks" is a good ticket. "Edit `verify.go`" is not.
- **A ticket with more than about eight acceptance criteria is two tickets.**
- If work is blocked, say so in the Context section and link the blocker. Do not
  file a ticket that cannot be started and leave the implementer to discover it.

## Labels and branch

Set these when filing, so the implementer does not have to guess:

- exactly one `type:` label
- an `area:` label matching the layer
- a `platform:` label only if genuinely platform specific
- `beginner-blocker` or `accepts-wrong-answer` if either applies, because they
  outrank everything else

The branch follows from the type: `feature/issue-<n>`, `bug/issue-<n>`, or
`chore/issue-<n>`. See the `github-workflow` skill.

## Filing from the command line

The gh CLI cannot render YAML issue forms non-interactively, so build the body to
mirror the template's fields exactly, in order, using the field labels as headings:

```bash
gh issue create \
  --title "[ticket] pty: streaming OSC 133 parser" \
  --label "type: feature" --label "area: pty" \
  --body-file ticket.md
```

Writing the body to a file first is worth it. These are long, and a heredoc that
contains code fences and backticks is easy to get subtly wrong.

## Anti-patterns

| Do not write | Write instead |
|---|---|
| "Implement the verification engine" | The thirteen types, the registry signature, and the acceptance criteria that prove each works |
| "Make the parser robust" | The specific inputs it must survive and the test that proves it |
| "Fix the WSL bug" | The reproduction, the expected behaviour, and the file it lives in |
| "See the architecture doc" | The section number, and the three paragraphs that actually apply |
| "Add tests" | Which tests, covering which paths, including the refusals |
| A ticket with no Safety review section | Answer it, even if the answer is one word |
