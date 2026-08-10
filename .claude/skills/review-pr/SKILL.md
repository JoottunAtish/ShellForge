---
name: review-pr
description: Reviewing a Shellforge pull request on GitHub, including fetching the diff, verifying the template checklist, running the gates, and posting review comments in the house format. Load when asked to review a PR, review a branch, or leave feedback on someone's changes. Use together with review-code, which supplies the actual audit passes.
---

# Reviewing a pull request

`review-code` is the audit. This skill is the process around it: what to check
about the PR itself, and how to leave the feedback.

## Gather

```bash
gh pr view <n>                                   # description and checklist
gh pr diff <n>                                   # the change
gh pr checks <n>                                 # CI state
gh pr view <n> --json files,additions,deletions,headRefName,labels
```

Then check out and run the gates yourself. **Do not trust green CI alone**, and do
not trust the author's ticked checkboxes:

```bash
gh pr checkout <n>
make ci          # or: .\make.ps1 ci
```

If CI is red, read why before reviewing content. A failing punctuation or layer
check is a thirty-second fix and reviewing around it wastes everyone's time.

## Check the PR itself, before the code

| Check | Blocking if wrong |
|---|---|
| Branch is `<kind>/issue-<number>` | Yes, CI enforces it |
| Links an issue with `Closes #n` | Should fix |
| Uses the PR template, not free prose | Should fix |
| Title is `<area>: <imperative summary>` | Nit |
| One logical change | Should fix. A refactor bundled with a fix cannot be reverted cleanly. |
| Platforms exercised are stated | Should fix |

**Verify the checklist rather than believing it.** The template asks the author to
confirm they ran `gofmt`, the tests, the race detector, and the punctuation gate. A
ticked box that is a lie wastes your whole pass, so run them. An unticked box with
a reason beside it is fine and honest.

If the PR touches a level, the checklist claims the golden test passes and that the
author tried to pass it the wrong way. Run `author test <id>` yourself.

## Audit the diff

Load `review-code` and work its passes in order. Consequence first:

1. Could this damage the user's machine
2. Security
3. Correctness
4. Architecture
5. The teaching product
6. Tests

Load `destructive-safety` for pass 1 whenever the diff touches deletion, path
resolution, WSL, containers, uninstall, reset, or `doctor --fix`.

## Comment format

Every substantive comment is three parts:

```
<severity>: <what is wrong>

<why it matters, one or two sentences>

<the concrete fix, as a suggestion block where possible>
```

| Prefix | Meaning | Blocks |
|---|---|---|
| `blocking:` | Correctness, security, or a violated non-negotiable | Yes |
| `question:` | I do not understand this and might be wrong | Until answered |
| `suggestion:` | I think this is better, you may disagree | No |
| `nit:` | Cosmetic | No |
| `praise:` | Good, and I want it to survive future refactors | No |

Worked example:

````
blocking: this passes the distro name through as a parameter

`wsl --unregister` is instant and irreversible, and the developer machine for
this project has a personal `Debian` sitting next to the sandbox. A caller
passing the wrong name, or an empty string, permanently destroys it.

```suggestion
	if name != sandboxDistro {
		return fmt.Errorf("refusing to unregister %q: shellforge only ever removes %q", name, sandboxDistro)
	}
```

Please add a refusal test covering the empty string and a foreign distro name.
````

## Posting

Inline comments on specific lines, a summary at the top:

```bash
gh pr review <n> --comment --body "..."          # feedback, no verdict
gh pr review <n> --request-changes --body "..."  # something blocking
gh pr review <n> --approve --body "..."          # good to merge
```

The summary should open with the verdict and the count, so the author knows the
shape before reading:

```
Two blocking issues, both in the WSL destroy path, plus three suggestions.
The content changes look good and the new level's on_fail messages are
genuinely helpful.
```

## Rules for the reviewer

- **Comment on the code, never the person.** "This drops the exit code", not "you
  forgot the exit code".
- **Always say why.** A comment without a reason is an instruction, and the author
  learns nothing and does it again next month.
- **Do not review formatting.** `gofmt` owns it and CI enforces it. If you are
  commenting on whitespace, the tooling failed, not the author.
- **Approve with unresolved nits.** Blocking on a `nit:` is a review smell.
- **Use suggestion blocks for anything mechanical.** A one-click fix beats five
  sentences.
- **Prefix everything.** A long unprefixed review is impossible to triage.
- **Praise deliberately.** Marking why something is good is how it survives the
  next refactor by someone who did not understand it.
- **This is a project about teaching beginners.** A review that would make a
  first-time contributor never come back has failed, regardless of how correct it
  was.

## The two questions specific to this codebase

Ask them explicitly on every relevant PR, and write the answer in the review rather
than assuming:

1. **Could this reach the learner's real machine?** Anything touching paths,
   deletion, WSL, mounts, or elevation.
2. **Could this level's checks reject a valid alternative solution?** Verifying
   state rather than syntax is the pedagogical stance of the whole product, and a
   well-intentioned tightening is how you punish someone for solving it a better
   way than the author imagined.
