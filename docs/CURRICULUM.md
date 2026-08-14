# Shellforge Curriculum: The 25 Tasks

**Narrative frame:** The learner is a new junior sysadmin at **Meridian Logistics**, a shipping company whose one Linux server ("`atlas`") is held together by a departed engineer named Kofi who left behind cryptic notes and a mess. Each level is a ticket. Acts escalate from "find your desk" to "the server is on fire at 3 AM".

The story is thin on purpose - it costs writing time, not engineering time, and it turns "chmod exercise 4" into "get the deploy script running again". Every briefing opens with a timestamp and a situation.

**Reading this table:** *Par* = number of commands a competent user needs; used for the efficiency bonus. *Time* = expected minutes for a beginner.

**Two conventions were settled while levels 1 to 8 were written, and they apply
to every level from here on.**

*Everything the learner produces lives inside the level's `setup.root`.* An
earlier draft of Act I had the learner writing to `~/answer.txt` and `~/found.txt`
directly in their home directory. Teardown is `rm -rf` on `setup.root` and may go
no further, so a file written outside it survives the level that created it and
leaks into the next one. Each level now owns a folder, usually `~/quest`, and both
the setup and the learner's answers live in it.

*A journal check may never gate passing.* `command_matched` and
`command_not_matched` must be `optional: true` or `severity: warn`, and
`shellforge author validate` rejects a level where one is neither. See
[LEVEL-FORMAT.md](LEVEL-FORMAT.md) section 3 for why. Where a level below
describes a required journal check, it is a bonus objective in the built level.

---

## Act I - Orientation
*"Your first day. Nobody has shown you anything."*

### 1. `nav-01` - First Contact
| | |
|---|---|
| Concepts | `pwd`, the prompt, filesystem root |
| XP / Difficulty / Par / Time | 40 / 1 / 2 / 3 min |

**Briefing.** 08:15. You've been handed a terminal and no instructions. Before you can go anywhere, you need to know where you are.
**Objectives.** (1) Print your current directory. (2) Write the path you're in into `~/quest/answer.txt`.
**Setup.** `~/quest` with a `welcome.txt` from Kofi.
**Checks.** `file_content ~/quest/answer.txt trimmed_equals /home/learner` · `command_matched ^\s*pwd\s*$` (bonus).
**Hints.** (1) The prompt shows an abbreviation, not the full path. (2) Three letters: "print working directory". (3) `pwd`. (4) Solution.
**Teaching note.** First level must be winnable in under 60 seconds. Confidence before difficulty.

### 2. `nav-02` - Taking Inventory
| | |
|---|---|
| Concepts | `ls`, `-l`, `-a`, `-h`, hidden files |
| XP / Diff / Par / Time | 50 / 1 / 3 / 5 min |

**Briefing.** Kofi left files everywhere, including some he didn't want you to see immediately.
**Objectives.** (1) Find the hidden file in `~/quest`. (2) Copy its *name* into `~/quest/found.txt`. (3) *(bonus)* Discover the largest file in `~/quest/inbox/` using a human-readable listing.
**Setup.** `~/quest/.kofi-notes`, `~/quest/inbox/` with 6 files of varied sizes.
**Checks.** `file_content ~/quest/found.txt trimmed_equals .kofi-notes` · bonus `command_matched ls\s+-[a-z]*l[a-z]*h|ls\s+-[a-z]*h[a-z]*l`.
**Hints.** (1) Files starting with `.` are hidden from a plain `ls`. (2) `ls` takes flags; one of them means "all". (3) `ls -a`. (4) Solution.

### 3. `nav-03` - Getting Around
| | |
|---|---|
| Concepts | `cd`, absolute vs relative, `..`, `~`, `-`, `tree` |
| XP / Diff / Par / Time | 60 / 2 / 5 / 6 min |

**Briefing.** The warehouse records live four directories deep. Kofi's note says "third bay, second aisle". Go find it.
**Objectives.** (1) Navigate to `~/warehouse/bay-3/aisle-2/shelf-c`. (2) Create `here.txt` containing the absolute path. (3) *(bonus)* Return to your home directory using a single character argument.
**Setup.** A 4-level tree with decoy bays/aisles.
**Checks.** `file_exists .../shelf-c/here.txt` · `file_content` equals the absolute path · bonus `command_matched ^\s*cd\s+~?\s*$|^\s*cd\s+-\s*$`.
**Hints.** (1) `cd` changes directory; paths can be chained with `/`. (2) You can go multiple levels at once: `cd a/b/c`. (3) `cd ~/warehouse/bay-3/aisle-2/shelf-c`. (4) Solution.
**Teaching note.** This is where absolute vs relative clicks. Make the decoys mean that a relative path from the wrong place fails.

### 4. `nav-04` - The Manual  🔶 *Act I boss*
| | |
|---|---|
| Concepts | `man`, `--help`, `apropos`, self-sufficiency |
| XP / Diff / Par / Time | 90 / 2 / 4 / 8 min |

**Briefing.** Kofi's last note: *"I'm not going to teach you every command. Learn to read the manual and you'll never need me."*
**Objectives.** (1) Find the `ls` flag that sorts by modification time and use it. (2) Find the `ls` flag that reverses sort order. (3) Write the two flags (without dashes, alphabetical, space-separated) into `~/flags.txt`. (4) *(bonus)* Use `apropos` to search for a keyword.
**Setup.** `~/logs/` with files whose mtimes are deliberately spread.
**Checks.** `file_content ~/quest/flags.txt trimmed_equals "r t"` · bonus `command_matched ^\s*man\s+ls` · bonus `command_matched ^\s*apropos\s+`.

> The `man ls` check reads the journal, so it is a bonus objective and cannot gate passing. The flags file is what decides the level. A learner who already knew `-t` and `-r` has still solved it, which is the correct outcome: the check verifies what they achieved, not what they typed.
**Hints.** (1) `man ls` opens the manual. Press `/` to search inside it, `q` to quit. (2) Search for "time" and "reverse". (3) The flags are `-t` and `-r`. (4) Solution.
**Teaching note.** This is the single most valuable level in the game. Everything after assumes they can read `man`. Do not skip the `/`-to-search hint - beginners get trapped in `less` and panic.

---

## Act II - Files and Directories
*"Now make yourself useful."*

### 5. `files-01` - Laying Foundations
Concepts: `mkdir`, `-p`, `touch`. **50 XP / D2 / par 3 / 5 min.**
**Objectives.** Build `~/shipments/2026/q1/` in one command; create empty `manifest.csv` and `.gitkeep` inside it.
**Checks.** `dir_exists` · `file_exists` ×2 · `file_content manifest.csv line_count 0` · bonus `command_matched mkdir\s+-p`.

> There is no `is_empty` check type. Emptiness is `file_content` with `match: line_count` and `value: "0"`.
**Teaching note.** Make the nested path deep enough that doing it without `-p` is visibly annoying.

### 6. `files-02` - Reading the Records
Concepts: `cat`, `less`, `head`, `tail`, `wc -l`. **60 XP / D2 / par 4 / 6 min.**
**Objectives.** From a 4,000-line `~/quest/deliveries.log`: put the **first** line in `~/quest/first.txt`, the **last 3** lines in `~/quest/last.txt`, and the total line count in `~/quest/count.txt`.
**Checks.** three `file_content` matches · `command_not_matched ^\s*cat\s+\S*deliveries\.log` with `severity: warn` (i.e. solve it without dumping the whole file).

> `deliveries.log` comes from the seeded `loglines` generator, so the expected first and last lines are fixed constants and identical on every machine. Changing the seed or the line count changes the answers and needs a `version:` bump.
**Teaching note.** The negative check teaches *why* `head`/`tail` exist. Make the file big enough that `cat` floods the screen.

### 7. `files-03` - Moving Day
Concepts: `cp`, `mv`, `cp -r`, rename-vs-move. **70 XP / D2 / par 5 / 7 min.**
**Objectives.** Back up `~/quest/config/` to `config.bak/` (recursively), rename `oldname.conf` to `atlas.conf`, move all `.csv` files from `~/quest/downloads/` into `~/quest/filed/`.
**Checks.** `dir_tree` `names_and_hashes` on the backup · `file_exists`/`file_absent` pair for the rename · per-file `file_exists`/`file_absent` for the move, plus two `file_exists` on the non-`.csv` files that must not move.

> The destination is `~/quest/filed/`, not `~/shipments/2026/q1/` as an earlier draft had it. Every level's teardown removes only its own `setup.root`, so a level must never write into another level's world.
**Teaching note.** Include the classic trap: `cp dir dest` without `-r` fails. Let them hit it; the `on_fail` explains it.

### 8. `files-04` - Controlled Demolition
Concepts: `rm`, `rmdir`, `rm -r`, `-i`, why there is no undo. **70 XP / D3 / par 4 / 6 min.**
**Objectives.** Delete the three `*.tmp` files, remove the empty `scratch/` directory, delete the non-empty `cache/` directory - while leaving `important/` completely untouched.
**Checks.** `file_absent` ×3 · `not: dir_exists` ×2 · **`important/` byte-identical**: `file_content` `match: sha256` on each of the three files, plus a `script` check asserting it holds exactly three files.

> There is no `dir_absent` check type; a missing directory is `not:` wrapping `dir_exists`. There is also no way to point `dir_tree` at a pristine copy without keeping that copy inside the level's own world, where the learner can see and delete it, so byte-identity is asserted with content hashes instead. The hashes are of the level's own inline `content:` blocks: editing that content without recomputing them breaks the level, and `TestLevelAssetHashesMatchTheirContent` is what catches it.
**Teaching note.** The briefing explicitly warns: there is no recycle bin. Award the "rm -rf Survivor" achievement if they nuke `important/` and have to `reset`. Turning the mistake into a badge is much better pedagogy than punishing it.

### 9. `files-05` - Pattern Matching  🔶 *Act II boss*
Concepts: globs `*`, `?`, `[]`, `{}`, brace expansion. **120 XP / D3 / par 6 / 12 min.**
**Objectives.** From a directory of 60 mixed files: move all `.log` files to `logs/`; move files matching `report-2025-??.pdf` to `archive/`; delete all files starting with `tmp_`; leave everything else alone.
**Checks.** file counts + `dir_tree` per destination + untouched-set integrity.
**Teaching note.** 60 files makes doing it one-by-one painful enough that globs feel like a superpower. That feeling is the lesson.

---

## Act III - Plumbing
*"Commands are Lego. Here's how they connect."*

### 10. `pipe-01` - Capturing Output
Concepts: `>`, `>>`, truncate vs append. **60 XP / D2 / par 4 / 6 min.**
**Objectives.** Save a directory listing to `inventory.txt`; then append the current date to the same file without losing the listing.
**Checks.** `line_count` ≥ N · `file_content contains` the date · ordering check (listing before date).
**Teaching note.** Make them experience `>` clobbering the file. The `on_fail` for "listing missing" says: *"`>` replaces, `>>` adds."*

### 11. `pipe-02` - Separating Streams
Concepts: stdout vs stderr, `2>`, `&>`, `/dev/null`. **80 XP / D3 / par 4 / 8 min.**
**Objectives.** Run a provided flaky script `check-nodes.sh` that writes to both streams. Capture *only* its errors into `errors.txt` and *only* its normal output into `output.txt`.
**Checks.** `file_content errors.txt` contains only error lines · `output.txt` contains no error lines · neither is empty.
**Teaching note.** The provided script is the teaching asset - it prints interleaved good/bad lines so the split is visible and satisfying.

### 12. `pipe-03` - Connecting Pipes
Concepts: `|`, `sort`, `uniq -c`, `wc`. **90 XP / D3 / par 5 / 9 min.**
**Objectives.** From `access.log`: produce a count of unique IP addresses into `unique_ips.txt`, and the top 3 most frequent IPs (with counts) into `top3.txt`.
**Checks.** exact `file_content` for both · bonus `command_count_under 8`.
**Teaching note.** `sort | uniq -c | sort -rn | head -3` is the canonical idiom - one of the highest-value one-liners in the whole curriculum.

### 13. `pipe-04` - The Toolbelt
Concepts: `cut`, `tr`, `tee`, `xargs`. **90 XP / D3 / par 5 / 9 min.**
**Objectives.** From a CSV: extract column 3 into `emails.txt`, lowercase it, and simultaneously write the result to both `emails.txt` and the screen using a single pipeline.
**Checks.** `file_content` exact · `command_matched tee` (this one *is* the skill).
**Teaching note.** One of the few places a syntax check is legitimate - `tee` is the objective, not the output.

### 14. `pipe-05` - The Log Sifter  🔶 *Act III boss*
Concepts: full pipeline composition. **150 XP / D4 / par 6 / 15 min.**
**Briefing.** 02:41. The billing service is throwing errors and nobody knows how many.
**Objectives.** (1) Count `ERROR` lines (case-insensitive) across all files in `logs/` → `report.txt`. (2) List the distinct error codes, sorted, → `codes.txt`. (3) *(bonus)* Solve objective 1 in a single pipeline. (4) *(anti-pattern)* Do not hardcode the number.
**Checks.** `file_content` exact ×2 · bonus `command_matched grep.*\|\s*wc\s+-l` · `command_not_matched echo\s+\d+\s*>` (severity: warn).
**Teaching note.** This is the level from the architecture doc - build it first as the reference implementation for every check type.

---

## Act IV - Search
*"The server has 400,000 files. Find the one that matters."*

### 15. `find-01` - Finding Words
Concepts: `grep`, `-i`, `-n`, `-v`, `-c`. **80 XP / D3 / par 4 / 7 min.**
**Objectives.** In `support-tickets.txt`: write all lines mentioning "refund" (any case) with line numbers to `refunds.txt`; write the count of lines *not* mentioning "resolved" to `open_count.txt`.
**Checks.** exact `file_content` ×2.

### 16. `find-02` - Deep Search
Concepts: `grep -r`, `-l`, `-E`, basic regex. **100 XP / D4 / par 5 / 10 min.**
**Objectives.** Search the whole `src/` tree for a hardcoded API key matching `[A-Z0-9]{16}`; write the *filename* (not the line) to `leak.txt`.
**Checks.** `file_content` equals the one correct path · bonus `command_matched grep.*-[a-zA-Z]*l`.
**Teaching note.** `-l` (files-with-matches) is the insight - the answer is *which file*, not *what line*.

### 17. `find-03` - Finding Files
Concepts: `find`, `-name`, `-type`, `-size`, `-mtime`. **100 XP / D4 / par 5 / 10 min.**
**Objectives.** List all `.conf` files modified in the last 2 days → `recent.txt`; find the single file larger than 10 MB → `hog.txt`.
**Checks.** exact content · bonus `command_not_matched ls\s+-R` (i.e. actually use `find`).
**Teaching note.** Setup must set mtimes explicitly with `touch -d` so the level is deterministic regardless of when it's played.

### 18. `find-04` - Search and Destroy  🔶 *Act IV boss*
Concepts: `find -exec`, `find | xargs`, combining search with action. **160 XP / D4 / par 5 / 15 min.**
**Objectives.** Delete every `.tmp` file anywhere under `data/` (nested up to 5 deep); make every `.sh` file under `scripts/` executable; count how many files you touched → `touched.txt`.
**Checks.** no `.tmp` remains anywhere · `file_mode` on 8 scripts · non-`.sh` files unchanged · count correct.
**Teaching note.** The `-exec {} \;` syntax is genuinely hard. Hint tier 3 should show the shape: `find ... -exec cmd {} \;`.

---

## Act V - Permissions and Processes
*"Why can't I run this?"*

### 19. `perm-01` - The Gatekeeper
Concepts: `ls -l` anatomy, `chmod` symbolic and octal. **100 XP / D3 / par 5 / 10 min.**
**Objectives.** Make `deploy.sh` executable by its owner only; make `notes.txt` readable by everyone but writable by no one; set `secrets.env` to `600`.
**Checks.** `file_mode` ×3 exact · bonus: used symbolic notation at least once.
**Teaching note.** Include an `ls -l` decoder in the briefing - the 10-character string is a wall for beginners until someone breaks it into `d rwx rwx rwx`.

### 20. `perm-02` - Chain of Command
Concepts: users, groups, `chown`, `sudo`, `id`. **110 XP / D4 / par 5 / 10 min.**
**Objectives.** A file owned by `root` must be transferred to `learner`; add `learner` to the `logistics` group; verify with `id`.
**Checks.** `file_owner` · `group_membership` · bonus `command_matched ^\s*id`.
**Teaching note.** First forced `sudo`. Briefing explains *why* it fails without sudo before they try - permission errors are demoralizing when unexplained.

### 21. `proc-01` - Process Control
Concepts: `ps aux`, `kill`, signals, `&`, `jobs`, `fg`, `nohup`. **120 XP / D4 / par 6 / 12 min.**
**Objectives.** A runaway process `atlas-indexer` is eating CPU - find its PID, write it to `pid.txt`, and terminate it. Then start `heartbeat.sh` in the background so it survives your shell.
**Checks.** `process_not_running atlas-indexer` · `file_content pid.txt` matches actual PID · `process_running heartbeat.sh`.
**Teaching note.** Setup launches the fake runaway process; its PID differs per attempt, so the check must resolve it dynamically - a good stress test for the `script` check type.

### 22. `perm-03` - Lockout  🔶 *Act V boss (break/fix)*
Concepts: diagnosing permission failures under pressure. **180 XP / D5 / par 8 / 18 min.**
**Briefing.** 03:12. The nightly job hasn't run in three days. Kofi "fixed" something before he left.
**The break.** `/opt/atlas/run.sh` is not executable; its parent dir lacks `+x` (so nothing inside can be traversed); the log dir is owned by root so the job can't write; and a config file is `000`.
**Objectives.** Get `/opt/atlas/run.sh` to execute successfully as `learner` and produce output in `/var/log/atlas/nightly.log`.
**Checks.** `script` check that literally runs the job as `learner` and asserts exit 0 + log written. **Multiple valid fixes all pass** - this is the point.
**Teaching note.** The best level in the game. No step-by-step objectives; just "make it work". The directory-`+x`-for-traversal trap is the thing nobody teaches and everybody needs.

---

## Act VI - Environment and Scripting
*"Stop typing the same thing twice."*

### 23. `env-01` - The Environment
Concepts: env vars, `export`, `$PATH`, `.bashrc`, `which`/`type`, `alias`. **110 XP / D4 / par 6 / 12 min.**
**Objectives.** Export `ATLAS_ENV=production` in the current shell; add `~/bin` to `PATH` so a provided script runs by bare name; make both survive a new shell by editing `.bashrc`.
**Checks.** `env_var` (from the env snapshot) · `file_content ~/.bashrc contains` · `script` check that opens a *fresh* shell and verifies persistence.
**Teaching note.** The fresh-shell check is what teaches the difference between "set now" and "set always". Worth the extra check-engine work.

### 24. `script-01` - Your First Script
Concepts: shebang, `chmod +x`, `$1`, `$@`, `if`, `for`, exit codes. **140 XP / D4 / par 6 / 15 min.**
**Objectives.** Write `~/bin/count-logs.sh` that takes a directory as `$1`, prints the number of `.log` files in it, exits `0` on success and `1` with a message to **stderr** if the directory doesn't exist.
**Checks.** `script` check invoking it with a valid dir (assert stdout + exit 0), an invalid dir (assert exit 1 + stderr non-empty, stdout empty), and no args · `file_content` starts with a shebang · `file_mode` executable.
**Teaching note.** The stderr-vs-stdout requirement pays back level 11. Circularity like this is what makes a curriculum feel designed rather than listed.

### 25. `boss-final` - The 3 AM Page  🔶🔶 *Final boss*
Concepts: everything. **300 XP / D5 / par 15 / 30 min.**
**Briefing.** 03:04. Your phone is ringing. `atlas` is not serving orders. You have no runbook. Kofi is unreachable. Fix it.

**The break (five faults, discoverable in any order):**
1. Disk "full" - a runaway 2 GB log in `/var/log/atlas/` must be found and truncated.
2. The service script lost its `+x` bit.
3. A config file has a typo (`prot=8080` instead of `port=8080`) discoverable only by grepping the error log.
4. A stale PID file blocks startup.
5. A cron entry is malformed so the cleanup job never ran (root cause of #1).

**Objectives.** (1) `atlas-health` reports `OK`. (2) Disk usage under threshold. (3) Cron entry valid. (4) Write a post-mortem to `~/postmortem.txt` naming all five root causes (checked with `file_content contains` × 5 keyword sets).

**Checks.** One `script` check running `atlas-health` · `disk_usage_under` · `cron_entry_exists` · 5 keyword checks on the post-mortem.

**Teaching note.** The post-mortem objective is deliberate: it forces the learner to *articulate* what they did, which is the actual skill the job requires. It's also the emotional payoff - they write the document that proves they can do this now.

---

## Curriculum design principles (for anyone authoring more levels)

1. **Every level teaches one new thing and reuses two old ones.** Level 24 needs stderr from level 11.
2. **Verify state, not syntax** - except where the syntax *is* the lesson (`tee`, `find -exec`, `mkdir -p`).
3. **Make the painful way visible before the good way.** 60 files before globs. 4,000-line `cat` before `head`.
4. **Every `on_fail` names what's wrong and what to look at.** Never "incorrect".
5. **Bosses have no numbered steps.** "Make it work" with multiple valid solutions.
6. **Deterministic setup, always.** Explicit `touch -d` for mtimes, seeded generators, no network.
7. **Beginner levels must be winnable in 60 seconds.** Fun first, difficulty later.

## Concept coverage matrix

| Concept | Levels |
|---|---|
| Navigation | 1, 2, 3 |
| Documentation | 4 |
| File manipulation | 5, 6, 7, 8, 9 |
| Globs | 9, 17 |
| Redirection | 10, 11, 14 |
| Pipes | 12, 13, 14 |
| grep | 15, 16, 14, 25 |
| find | 17, 18, 25 |
| Permissions | 19, 20, 22, 25 |
| Processes | 21, 25 |
| Environment | 23, 24 |
| Scripting | 24, 25 |
| Debugging under pressure | 22, 25 |
