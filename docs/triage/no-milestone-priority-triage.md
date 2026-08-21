# No-milestone ticket triage, by priority

Snapshot date: 2026-08-21. Scope: every open issue with no milestone assigned.
Query: `repo:joottunatish/shellforge is:issue is:open no:milestone` (16 issues).

## How priority is recorded

There is no `priority:` label in `.github/labels.yml`. Per the triage decision,
the existing `cluster:` tags are reused as priority tiers rather than minting a new
label family:

| Tier label | Priority | Meaning |
|---|---|---|
| `cluster: 1` | P0, critical | Blocks merges or ships wrong behaviour. Do first. |
| `cluster: 2` | P1, important | Real bug, safety gate, or security or tooling breakage. |
| `cluster: 3` | P2, lower | Deferred (`cut: v0.2`), blocked, refactor, docs, or a decision. |

Caveat worth recording: the `cluster:` tags were originally thematic work
groupings, not severity. Commit `3207f1b` ("store and journal: close out cluster 2")
shows a cluster shipping as one batch of related tickets. Reusing them as priority
tiers overloads that meaning. If cluster-as-theme is ever needed again, split the two
axes into a real `priority:` family and free the `cluster:` tags back up.

## Tier 1 (cluster: 1): critical, do first

One ticket. It is the only open no-milestone issue that keeps `main` red.

| # | Title | Labels | Why it is P0 |
|---|---|---|---|
| 141 | pty: the Windows resize watcher races its own stop, so main is intermittently red | type: bug, area: pty, platform: windows | `Test (windows-latest)` fails intermittently under `-race`. The failure blocks every merge, because `All checks green` gates on it. |

## Tier 2 (cluster: 2): important

Real bugs, a silently disabled CI safety gate, a least-privilege gap, and Windows
tooling that fails outright. None block `main` today, but each is a correctness or
safety problem, not a nicety.

| # | Title | Labels | Why it is P1 |
|---|---|---|---|
| 74 | runtime: report the real Docker failure instead of a bare URL under a vague error | type: bug, area: runtime, area: cli | Error quality is non-negotiable #6: an error must say what failed and the next step. A bare URL denies the user the real cause. Moved up from cluster: 3. |
| 86 | ci: the doc anchor gate matches one comment and misses every real ux.Fail call site | type: ci | The gate greps the struct-literal form and every real call site uses the positional form, so the safety gate passes while enforcing nothing. False confidence. |
| 105 | ci: scope the image job's contents:write to the release-attach step only | type: ci, area: sandbox-image | Job-level `contents: write` grants every step of the image job write scope. Least privilege on a job that runs a docker build. |
| 116 | content: the validator does not enforce the state directory containment rule the runner does | type: feature, area: content-engine | `author validate` accepts a pack the setup runner then refuses. The two must agree, or validation lies to a pack author. |
| 118 | ci: make.ps1 hands the shell gates to WSL bash, which has no git | type: ci, platform: windows | On a Windows box without Git Bash on PATH, every shell gate fails with "git not found". Windows contributors cannot run the gates at all. |

## Tier 3 (cluster: 3): lower

Deferred by decision (`cut: v0.2`), externally blocked, refactors, a docs fix, an
uncertain investigation, and two open decisions. Real, but none are urgent.

| # | Title | Labels | Why it is P2 |
|---|---|---|---|
| 60 | ci: no real Windows coverage for the docker runtime contract suite | type: ci, area: runtime, blocked | Labelled `blocked`, waiting on something outside the repo. Cannot act now. |
| 77 | runtime: report interactive shell support as a capability, not a GOOS test | type: refactor, area: runtime, platform: windows | Internal refactor, no behaviour change until the WSL runtime lands. Kept at cluster: 3. |
| 115 | content: perm-03 needs root-owned state the level format cannot express | type: feature, area: content-engine, cut: v0.2 | Deliberately deferred to v0.2. |
| 117 | content: graduate the script body leak scan from a test helper into a validator rule | type: feature, area: content-engine, cut: v0.2 | Deliberately deferred to v0.2. |
| 119 | content: the carriage return strip is one pass, so a doubled CR still ships a CRLF | type: bug, area: content-engine, cut: v0.2 | A real CRLF bug, but already marked `cut: v0.2`, so deferred by decision. |
| 132 | docs: three implementations of the doc anchor rule should collapse into one | (none) | Consolidation of duplicated rule checks. No user impact. Pairs with #86. |
| 133 | runtime: check whether modern buildx appends a "View build details" trailer that summarizeFailure mishandles | (none) | Explicitly uncertain and not to be acted on without a real failing build to check against. Investigation, not a fix. |
| 135 | cli: shell completion has no owning ticket, decide ship or cut | type: feature, area: cli | A ship-or-cut decision, not implementation work. |
| 138 | runtime: no interactive sandbox shell on a native Windows console, on either backend | type: feature, area: runtime, platform: windows, needs-triage | Genuine Windows capability gap, but a feature that depends on the WSL runtime landing. |
| 139 | docs: the uninstall page names shellforge uninstall and export, neither of which is a verb | type: docs, area: cli, needs-triage | User-facing docs error, but minor and easily worked around. Small fix. |

## Counts

- Tier 1 (cluster: 1): 1
- Tier 2 (cluster: 2): 5
- Tier 3 (cluster: 3): 10
- Total: 16

## Notes for the next pass

- Four tickets still carry `needs-triage` (#138, #139, #141) plus the two untyped
  tickets (#132, #133 have no `type:` label at all). Assign a `type:` label to #132
  and #133 and clear `needs-triage` once each is genuinely triaged.
- #86 and #132 are the same underlying concern (the doc anchor rule). Consider one
  milestone for both so the fix and the consolidation ship together.
- Everything in Tier 3 marked `cut: v0.2` should move under a v0.2 milestone when one
  exists, which would also remove it from this no-milestone list.
