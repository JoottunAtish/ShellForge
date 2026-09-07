# Day 5 Ticket Plan: the content sprint, levels 9 to 25

Status: plan of record for Day 5. Filed as issue #152, a single issue, deliberately.

This document is the reasoning. The issue is the specification. Where the two
disagree, the issue is right, because that is what an implementer reads.

---

## 1. What Day 5 has to produce

`SEVEN-DAY-PLAN.md` gives Day 5 five blocks and calls it a writing day, not a
coding day:

| Block | Content | Est |
|---|---|---|
| 5.1 | Act II remainder (level 9) plus Act III plumbing (10 to 14) | 3 h |
| 5.2 | Act IV search (15 to 18) | 2 h |
| 5.3 | Act V permissions and processes (19 to 22) | 2.5 h |
| 5.4 | Act VI environment and scripting (23 to 25), including the final boss | 2 h |
| 5.5 | `author record` helper, only if time | 1 h |

Exit criteria: 25 levels validate clean, the whole campaign has been played in one
sitting, and every act ends in a boss level that takes ten minutes or more.

`packs/core-linux-basics` holds nine level files today: `nav-01` to `nav-04`,
`files-01` to `files-04`, and `pipe-05`. `pack.yaml` lists all 25 ids across six
acts, so `shellforge author validate` already prints one warning per missing file.
Sixteen levels are outstanding: `files-05`, `pipe-01` to `pipe-04`, `find-01` to
`find-04`, `perm-01`, `perm-02`, `proc-01`, `perm-03`, `env-01`, `script-01` and
`boss-final`.

---

## 2. Why one ticket and not sixteen

The `ticket-creation` skill says one ticket is one branch is one pull request, and
that a ticket with more than about eight acceptance criteria is two tickets. Day 5
is the case where following that rule produces a worse outcome, and the reason is
worth writing down rather than leaving as a preference.

Sixteen level tickets would share one pack file, one asset directory, one asset
README table, one curriculum document and one prerequisite chain. Every one of
them would edit `docs/CURRICULUM.md` and `packs/core-linux-basics/assets/README.md`,
so fifteen of them would land in conflict. The prerequisite chain is linear across
the whole campaign, so a level cannot be reviewed in isolation from the one before
it. And the campaign playthrough that Day 5's own exit criterion demands is a
single act over all 25 levels, not sixteen separate ones.

The unit of work here is the pack, not the level. So the ticket is the pack.

What that costs is a long acceptance list, and it is paid for by making each level
its own line in that list rather than its own issue. What it must not cost is
self-containment: the issue carries the full per-level specification, so nobody has
to read this document to implement it.

---

## 3. Findings that change the plan

Seven things turned up while scoping that the task table does not reflect. Five of
them change level designs, one changes the sandbox image, and one is arithmetic.

### 3.1 sudo does not work in the sandbox, and Act V assumes it

This is the finding that reshapes the day.

`images/Containerfile` grants the learner passwordless sudo, with a comment saying
Act V teaches it. `internal/runtime/docker/docker.go:221` then creates the
container with `--security-opt no-new-privileges`, which stops sudo from gaining
privilege at all. `cmd/shellforge/isolation_test.go`'s
`TestSudoIsRefusedByNoNewPrivileges` pins that behaviour and says, in its own
`TODO(v0.2)`:

> decide whether Act V drops no-new-privileges for levels that declare they need
> sudo, or whether the curriculum teaches sudo without running it. Do NOT resolve
> it by removing the flag to make a level pass.

`docs/CURRICULUM.md` level 20 says "First forced `sudo`", level 22 needs a
root-owned log directory, and level 25 needs a root-owned service tree. All three
are unbuildable as written.

**Decision: the curriculum teaches sudo without running it, and the flag stays.**

Dropping `no-new-privileges` per level is technically available: the level format
already carries `requires: [networking|systemd|multiuser]`, and `Runtime` already
reports `Capabilities()`, so a `requires: [multiuser]` level could be given a
laxer container. It is still the wrong call for v0.1. It is Go work at L1 plus
plumbing at L4 on a day budgeted for writing, it weakens the one guarantee the
README makes to a beginner, and it would be done under time pressure at the end of
the week. Non-negotiable 2 in `CLAUDE.md` is not something to trade for a nicer
level 20.

Every Act V and Act VI objective is therefore reachable by an unprivileged
learner. The lesson survives the change in every case, because the thing that
actually blocks a beginner is reading `ls -l` and understanding that a directory
needs `+x` to be traversed, not typing the word sudo:

- **`perm-02`** keeps ownership, groups and `id`. The learner regroups and
  regrants a file they own, and reads a root-owned file they cannot write. What
  they lose is `sudo chown`.
- **`perm-03`** keeps all four faults, including the directory traversal trap that
  is the point of the level. The log directory is unwritable by mode rather than
  by owner, which fails the job in exactly the same way and is fixed with `chmod`.
- **`boss-final`** keeps all five faults, re-sited under the learner's own tree.

The Containerfile comment and the test comment both claim Act V will use sudo.
Both are updated in the same commit, which is what the test asks for in its own
failure message.

### 3.2 A level cannot own state outside `/home/learner/`, which blocks two bosses as written

Issue #115 already records this for `perm-03`: `setup.root` must resolve under
`/home/learner/` (`internal/platform/sandboxpath.go`), and only a regular file may
carry a non-learner `owner:`, because `buildManifest` refuses an entry that is an
ancestor of another entry. `/opt/atlas` and `/var/log/atlas` are expressible by
neither rule. `boss-final` has the same problem and no issue of its own.

There is a second enforcement layer that the issue does not mention.
`TestShippedLevelsKeepLearnerOutputInsideTheirRoot` fails any check whose `path` or
`compare_to` names anything outside the level's own root, so even if setup could
build `/opt/atlas`, no filesystem check could point at it. Only a `script` check's
`run` body escapes that scan, and only because a level may legitimately teach a
system path.

**Decision: re-site both levels inside their own root.** `perm-03` gets
`/home/learner/atlas` with `bin/`, `etc/` and `var/log/atlas/` under it;
`boss-final` gets `/home/learner/atlas` too. The briefing frames it as Kofi's
staging copy of the service, which is a plausible thing to find on a real box and
costs the level nothing.

This does not close #115. #115 asks whether a level may ever declare state outside
its own root, and the answer here is that Day 5 does not need one, which is a
smaller claim. #115 stays open as the v0.2 design question, and gains a comment
pointing at the level that stopped needing it.

### 3.3 Five check types the curriculum names do not exist

`docs/LEVEL-FORMAT.md` section 3 registers 14 types and defers fifteen more. The
curriculum's level designs reference five of the deferred ones, and the format
document's own note says to implement them with `type: script` for v0.1. Recording
the exact mapping so it is decided once rather than five times:

| Curriculum wants | Level | v0.1 shape |
|---|---|---|
| `process_not_running` | 21 | `process_running` with `negate: true` |
| `group_membership` | 20 | `script`: `id -nG learner` piped through `grep -qx` |
| `cron_entry_exists` | 25 | `script` validating the five fields of a cron line in a file the level owns |
| `disk_usage_under` | 25 | `script` comparing `stat -c %s` against a byte threshold |
| `command_count_under` | 12 | Dropped. `par_commands` already drives the efficiency bonus, which is the same intent through the scoring system rather than through a check. |

`cron_entry_exists` deserves a second sentence. No cron daemon runs in the sandbox:
PID 1 is `sleep infinity` and nothing starts `cron`. A level that waited for a cron
job to fire would hang. So level 25's fifth fault is a malformed crontab line in a
file under the level's own root, checked as text. The learner still has to read a
crontab line and see that it has four fields where it needs five, which is the
teachable part.

### 3.4 `-mtime` levels need relative timestamps, and the determinism rule reads as if they are banned

The `level-authoring` skill says no `date` in setup and explicit `touch -d` for
every mtime a level depends on. Level 17 asks for `.conf` files modified in the
last two days, which is `find -mtime -2`, which is relative to now by definition. A
fixed absolute timestamp makes that level pass on the day it was written and fail
forever after.

The rule's intent is that the answer is fixed, not that the clock is unused.
`touch -d "36 hours ago"` satisfies both: the offset is fixed, so the set of files
`find -mtime -2` returns is fixed, on any machine on any day. Two constraints go
with it, and both are in the issue: never place a file near the boundary the level
tests, and never use `date` to compute the offset, since GNU `touch` parses the
relative form itself.

### 3.5 Level 25's "2 GB log" cannot be built and does not need to be

The curriculum's first fault for `boss-final` is a runaway 2 GB log. The generator
registry bounds `loglines` at 200,000 lines and stages generated content in host
memory, so `generate:` cannot produce it. `truncate -s 2G` would produce a sparse
file that `du` reports as zero, which teaches the learner the wrong thing when they
go looking for the space.

A real file of roughly 12 MB, written by `setup.script` with `yes | head -n`,
behaves identically for the lesson: it is visibly the biggest thing in the tree,
`du -sh` reports it honestly, and truncating it is the same command it would be at
2 GB. The threshold check reads `stat -c %s` and wants under 1 MB.

### 3.6 The learner is not in the `logistics` group, so no group lesson is reachable

`images/Containerfile` creates `groupadd --gid 1100 logistics` and never adds
anybody to it. A learner may `chgrp` a file they own only to a group they belong
to, so with the group empty, level 20's group objective cannot be satisfied by any
command an unprivileged learner can run.

One line in the image fixes it: `usermod -aG logistics learner`. The CI image
content job gains an assertion for it, in the same style as the `man ls` assertion
that already guards level 4.

### 3.7 A flawless campaign does not reach the top rank

Base XP over all 25 levels, using the curriculum's own numbers, is 2580.
`pack.yaml` sets `wizard` at `min_xp: 2600`. A learner who passes every level, on
the first try, without a single hint, finishes 20 XP short of the last rank in the
game.

Scoring is issue #125 and is not built, so bonuses and multipliers may cover the
gap later. Tuning to exactly 2600 would still be wrong: hint costs subtract, and a
top rank that only a flawless run reaches is a top rank nobody reaches. Lower
`wizard` to 2400 and leave the level XP alone.

---

## 4. The sixteen levels

Full per-level specifications, with objectives, check types, assets and traps, are
in the issue. This section records only the decisions that are not obvious from the
curriculum entry.

### Act II

**9. `files-05`, Act II boss.** Sixty files, built by a loop in `setup.script`
rather than sixty `setup.files` entries: the content is irrelevant, only the names
matter, and a sixty-entry manifest is sixty chances to make a typo. There is no
pristine copy to point `dir_tree` at without putting it inside the learner's own
world where they can see and delete it, which is the same argument `files-04`
already made for hashing instead. So the untouched set is asserted by a `script`
check that lists the remaining names and compares them to a sorted expected list.

### Act III

**10. `pipe-01`.** The curriculum appends the current date, which no check can
assert exactly. Split it: `file_content contains` on a known listing entry proves
the listing survived, so `>` did not clobber it, and a `script` check proves the
last line parses as a date and sits after the listing. The lesson is truncate
versus append, and the ordering assertion is exactly that lesson.

**12. `pipe-03`.** `uniq -c` pads its counts with leading spaces, and the width of
that padding is not something to make a learner match. The top-three check
normalizes whitespace inside a `script` check before comparing, so a hand-written
`awk` answer and a `sort | uniq -c | sort -rn | head -3` answer both pass. Counts
in `access.log` are built so the third and fourth places are strictly separated,
because a tie would make the answer ambiguous.

**13. `pipe-04`.** The curriculum says the `tee` check is legitimate because the
syntax is the skill. It is still a journal check, and a journal check may never
gate passing. So `tee` is a bonus objective and the exact content of `emails.txt`
is what decides the level. This is a real divergence from the curriculum text and
is recorded there.

### Act IV

**16. `find-02`.** Committing a 16 character token to the repository is what the
asset rules exist to prevent, even a synthetic one. `setup.script` assembles the
key from two halves at setup time, so no literal that matches `[A-Z0-9]{16}` ever
appears in a committed file. The answer accepts an absolute path, a `src/` relative
path and a `./src/` relative path through `any_of`, because all three are what
`grep -rl` prints depending on where the learner ran it.

**17. `find-03`.** The oversized file is `truncate -s 11M`, which is instant and
which `find -size +10M` sees, since `-size` reads the apparent size. This is the
one place a sparse file is correct, because `find` is the tool being taught.

### Act V

**19. `perm-01`.** Unchanged from the curriculum. Three `file_mode` checks.

**20. `perm-02`.** Rebuilt around 3.1 and 3.6. The learner regroups a file they
own to `logistics` and makes it group writable, records their own group list, and
reads a root-owned file they cannot write into a copy they own. The root-owned file
is a `setup.files` entry with `owner: "root:root"`, which is expressible and which
teardown can still remove, because unlinking a file needs write and execute on the
parent directory rather than ownership of the file.

**21. `proc-01`.** The check needs the runaway process's PID after the learner has
killed it, so the PID has to be recorded somewhere while the process is alive. It
cannot go outside `setup.root`. So the fake indexer writes `.atlas-indexer.pid`
inside the level root, hidden by the leading dot. A learner who thinks to run
`ls -a` finds the answer without `ps`, which is a skill `nav-02` taught and a thing
real daemons do. That is an accepted trade rather than an oversight: the
alternative is a check that cannot verify the PID at all.

`setup.script` must start the indexer with `setsid` and a redirect so it outlives
the setup shell, and `teardown.script` must kill both it and the heartbeat the
solution starts. The golden runner already asserts that no process outlives
teardown, so a forgotten `pkill` fails CI rather than leaking into the next level.

**22. `perm-03`, Act V boss.** Re-sited per 3.2 and de-privileged per 3.1. Four
faults: `run.sh` is not executable, its parent directory has no `+x` so nothing
inside can be traversed, the log directory is mode `0555` so the job cannot write,
and a config file is `000`. All four are fixed with `chmod` by the owner. The
single `script` check runs the job as the learner and asserts exit 0 plus a
non-empty log, so any order of fixes and any mix of symbolic and octal passes,
which is the point of the level.

`teardown.script` has to `chmod -R u+rwX` the root before the runner's `rm -rf`,
because a directory the level deliberately left unwritable is a directory teardown
cannot empty. Teardown runs before setup as well, so the chmod is guarded by a
directory test.

### Act VI

**23. `env-01`.** `.bashrc` lives outside any level root, so an edit to it survives
teardown and leaks into every later level. `setup.script` copies it to a backup
inside the level root and `teardown.script` restores it, guarded, before the
`rm -rf` that removes the backup. The runner's order makes that safe: sentinel,
then `teardown.script`, then `rm -rf` the root.

The persistence check is a `script` check running `bash -ic`, not `bash -lc`: a
login shell reads `.bash_profile` and a non-interactive one reads neither, and
`.bashrc` is the file the level teaches. `bash -i` without a tty writes a job
control warning to stderr, so the check redirects it.

**24. `script-01`.** Executability is a `script` check running `test -x`, not
`file_mode`. `file_mode` wants an exact mode or an explicit list, and there is no
good reason to fail a learner whose `chmod +x` produced `0775` instead of `0755`.

**25. `boss-final`.** Five faults, all under `/home/learner/atlas`, all fixable
unprivileged: an oversized log (3.5), a service script with no `+x`, a `prot=8080`
typo discoverable by grepping the error log, a stale PID file, and a malformed cron
line (3.3). The post-mortem is one objective composed of five `contains` checks
under `all_of`, rather than five objectives, so the checklist reads as one task.

---

## 5. What Day 5 does not do

- **`author record`.** Block 5.5 is marked "only if time" in the plan and it is the
  sixth rung of the cut ladder. It is cut, not deferred quietly.
- **New check types.** Everything maps onto the 14 that exist, per 3.3.
- **Relaxing `no-new-privileges`,** per 3.1.
- **Relaxing the containment rule,** per 3.2. #115 stays open.
- **Day 4's remainder.** #125 through #130 are scoring, hints, achievements, the
  pass banner, `play` and `reset`. None of them is a prerequisite for writing a
  level, and none of them is in this ticket.

Two consequences of that last point are worth stating plainly, because they look
like content bugs and are not:

Journal checks verify nothing under `shellforge run` today. `game.Session` accepts
a journal and `journal.Journal` implements `Commands`, but `cmd_run.go` does not
wire one, so every bonus objective backed by `command_matched` reports a fail
against an empty command list. `author validate` prints a warning saying so on
every such check. Those bonuses are written now and start working when #129 lands
`play`.

And a level's XP is authored but not yet awarded, because the score formula is
#125.

---

## 6. Definition of done for the day

- 25 level files, `author validate` reporting no warnings other than the journal
  ones described above.
- `author test --all` green against a real container, which is the golden contract:
  set up, every required check fails, run the level's own solution, every required
  check passes, checks changed nothing, teardown leaves nothing behind and no stray
  process.
- The campaign played start to finish in one sitting, with friction noted in
  `PROGRESS.md`.
- `docs/CURRICULUM.md`, `docs/LEVEL-FORMAT.md`, the asset README and `PROGRESS.md`
  updated in the same commit as the content they describe.
