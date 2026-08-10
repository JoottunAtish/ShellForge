---
name: github-workflow
description: Shellforge conventions for GitHub work. Load before creating a branch, opening or triaging an issue, applying labels, opening a pull request, writing PR review comments, or writing a commit message. Covers the branch naming format, the three issue templates, the label taxonomy, the PR template and checklist, review comment severity prefixes, and commit message format.
---

# GitHub conventions

The writing-style rules apply to everything here. No em dashes, no en dashes, no
smart quotes in an issue title, an issue body, a PR description, a review comment,
or a commit message.

## Branch naming: every branch traces to a ticket

**Format: `<kind>/issue-<number>`.**

```
feature/issue-24
bug/issue-31
chore/issue-7
```

Three kinds and no others:

| Kind | Use it for | Issue types that map to it |
|---|---|---|
| `feature` | New capability, and new levels or content | `type: feature`, `type: content` |
| `bug` | Something behaves incorrectly | `type: bug`, `type: install`, `type: security` |
| `chore` | Everything that is not user-visible behaviour | `type: docs`, `type: ci`, `type: refactor`, `dependencies` |

Rules:

- **Open the issue first.** The branch name needs its number, so there is no such
  thing as a branch without a ticket. If the work is worth a branch, it is worth a
  ticket, and the ticket is where the reasoning lives when someone asks in March
  why this happened.
- **The number is the GitHub issue number, with the `issue-` prefix.**
  `feature/issue-24`, never `feature/24` and never `feature/add-hint-ladder`.
- **One branch per ticket.** If a branch grows a second concern, split the ticket.
- **Never commit directly to `main`.**
- **Dependabot is exempt.** It has its own naming and we do not fight it.

```bash
git switch main && git pull
git switch -c feature/issue-24
```

CI validates the branch name on every pull request, before anything else runs. It
is cheaper to rename a branch than to discover at merge time that the work is not
traceable to a decision.

## Opening an issue: always use a template

Three issue forms live in `.github/ISSUE_TEMPLATE/`. Use one. Do not open a blank
issue with a paragraph of prose.

| Template | Use it for |
|---|---|
| `01-install-problem.yml` | Installation failed, or `doctor` reported something the user could not fix |
| `02-bug.yml` | The game behaves incorrectly |
| `03-level-problem.yml` | A level is unclear, unfair, wrong, or accepts a wrong answer |

**The gh CLI does not render YAML issue forms non-interactively.** `gh issue create
--template` only works with the browser flow. When filing from the command line,
build the body by hand so it mirrors the template's fields exactly, in the same
order, using the field labels as headings:

````bash
gh issue create \
  --title "[level] pipe-03 accepts a hardcoded answer" \
  --label "type: content" --label "accepts-wrong-answer" --label "needs-triage" \
  --body "$(cat <<'BODY'
### Level id

pipe-03

### What kind of problem?

It accepted something that should not have passed

### What did you type?

```shell
echo "42" > unique_ips.txt
check
```

### What did you expect, and what happened instead?

I expected the level to reject a hardcoded answer. It passed both objectives.
The anti-pattern guard is missing from this level.

### How would you fix it?

Add the command_not_matched check that pipe-05 already has.
BODY
)"
````

Rules:

- **Fill every field the template marks required.** A skipped required field means
  the issue bounces back and the reporter does the work twice.
- **Title format is `[area] what is wrong`**, lowercase after the tag. Use the
  prefix the template sets: `[install]`, `[bug]`, `[level]`.
- **Never paste a screenshot in place of text** that could be pasted as text. Text
  is searchable, and the next person with this problem is searching.
- **Never open a public issue for a security report.** See `SECURITY.md` and use
  private vulnerability reporting.

## Labelling

Labels are defined in `.github/labels.yml` and applied with
`./scripts/sync-labels.sh`. That file is the source of truth. Do not invent labels
ad hoc in the web interface.

- **Exactly one `type:` label per issue.** Not optional.
- **Add an `area:` label** when you know the subsystem. Areas map to the layer
  architecture in `CLAUDE.md`.
- **Add a `platform:` label** only when the problem is genuinely platform specific.
  A bug that happens everywhere gets no platform label.
- **`needs-triage`** is applied automatically by the templates. Remove it when you
  have actually triaged it, not before.
- **Two labels outrank everything else** and should be applied aggressively:
  - `beginner-blocker`: it stops a first-time user cold. This is the product
    thesis. Fix these first.
  - `accepts-wrong-answer`: a level passes something it should reject. Treat it as
    a correctness bug, not a content nicety, because it teaches the wrong thing.
- **`cut: v0.2` is a decision, not a rejection.** Use it rather than `wontfix` when
  the idea is good and simply out of scope, and say so in a comment.

## Pull requests

Use `.github/PULL_REQUEST_TEMPLATE.md`. It is applied automatically on the web. From
the CLI, pass it as the body and fill it in.

- **Title:** `<area>: <what changed>`, imperative, lowercase after the colon.
  Examples: `pty: handle OSC markers split across reads`,
  `content: add pipe-03 and its access.log asset`,
  `docs: rewrite the Windows install guide`.
- **Link the issue** with `Closes #24` so it closes on merge.
- **Tick the checklist honestly.** Do not tick a box for a command you did not run.
  An unticked box with a one-line reason beside it is a perfectly acceptable PR. A
  ticked box that is a lie wastes the reviewer's entire pass.
- **State which platforms you exercised.** "Linux only" is a fine answer.
- **One logical change per PR.** A refactor bundled with a bug fix makes both harder
  to review and impossible to revert cleanly.

## Merging: only code owners

`main` is protected by a ruleset. The rules, and the reason each exists:

| Rule | Effect |
|---|---|
| Pull request required | Nothing reaches `main` directly |
| 1 approving review, **from a code owner** | Only a code owner can authorise a merge |
| Stale reviews dismissed on push | An approval covers the code that was approved, not whatever landed afterwards |
| Last push requires approval | You cannot approve, then push, then merge |
| Review threads resolved | A blocking comment cannot be merged past silently |
| All status checks green, up to date with `main` | No merging past a red build, and no merging stale |
| No deletion, no force push | `main` history is shared |

Code owners are declared in `.github/CODEOWNERS`. The safety-critical paths are
owned explicitly: `images/`, `internal/runtime/`, `internal/sandbox/`,
`internal/platform/`, `scripts/`, `.github/`, `CLAUDE.md`, `.claude/`, and the two
living contracts. A change to any of those cannot merge without an owner's
approval.

The ruleset itself lives in `.github/rulesets/main.json` and is applied with
`./scripts/sync-ruleset.sh`. GitHub does not version control rulesets, so keeping
the JSON in the repository means a protection rule cannot be quietly weakened in
the web interface without a reviewable diff.

**The admin bypass, and why it is there.** GitHub does not let anyone approve their
own pull request. On a single-maintainer repository, a code-owner-review requirement
with no bypass locks the maintainer out of their own project completely. So
`RepositoryAdmin` is on the bypass list. **Remove that entry as soon as a second
maintainer exists**, because until then it is the difference between a rule and a
wall. An admin merging past a red build is then a deliberate and visible act rather
than an accident.

**Never merge your own PR by bypassing when someone else could review it.** The
bypass is a lock pick for when you are alone in the building, not a normal door.

## Review comments

Reviewing is a teaching act in a project about teaching. Match the tone.

Format every substantive comment as three parts:

```
<severity>: <what is wrong>

<why it matters, in one or two sentences>

<the concrete fix, as a suggestion block where possible>
```

Severity prefixes, used consistently so the author can triage a long review:

| Prefix | Meaning | Blocks merge |
|---|---|---|
| `blocking:` | Correctness, security, or a violated non-negotiable | Yes |
| `question:` | I do not understand this and might be wrong | Until answered |
| `suggestion:` | I think this is better, you may disagree | No |
| `nit:` | Cosmetic. Take it or leave it. | No |
| `praise:` | This is good and I want it to survive future refactors | No |

Rules:

- **Use GitHub suggestion blocks for anything mechanical.** A suggestion the author
  can click is worth five sentences describing the change.
- **Comment on the code, never the person.** "This drops the exit code", not "you
  forgot the exit code".
- **Say why.** A review comment without a reason is an instruction, and the author
  learns nothing and will do it again.
- **Do not review formatting.** `gofmt` owns that and CI enforces it. If you are
  commenting on whitespace, the tooling has failed, not the author.
- **Approve with unresolved nits.** Blocking a PR on a `nit:` is a review smell.
- **When requesting a change to a level's checks, say explicitly whether the change
  could reject a valid alternative solution.** That is the review question that
  matters most in this codebase.

## Commit messages

```
<area>: <imperative summary under 72 characters>

Why this change exists, and anything a reader six months from now would
need to know. Wrap at 72. Skip this paragraph only for genuinely trivial
commits.

Closes #24
```

- Imperative mood: "add", not "added" or "adds".
- The subject says what changed. The body says why.
- Never `--no-verify`. If a hook fails, fix the cause.
- Do not commit or push unless you were asked to.
