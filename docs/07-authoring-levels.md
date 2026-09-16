# Authoring levels

A level is a YAML file plus its assets. You do not need to write any Go.

This page is the guide: how to think about a level, and the mistakes worth
avoiding. [LEVEL-FORMAT.md](LEVEL-FORMAT.md) is the schema, and it is the
authoritative one. Where the two disagree, that one is right. This page points
into it by section rather than restating it, because two copies of a contract
become two different contracts.

## The one-minute version

```bash
# There is no scaffold generator in v0.1.0. Start from a level that already
# passes the golden contract: an empty skeleton does not.
cp packs/core-linux-basics/levels/10-pipe-01.yaml \
   packs/core-linux-basics/levels/26-my-level.yaml
$EDITOR packs/core-linux-basics/levels/26-my-level.yaml
shellforge author validate packs/core-linux-basics
shellforge author test my-level
shellforge run my-level
```

`author validate` checks the schema and the authoring invariants in
[LEVEL-FORMAT.md section 2](LEVEL-FORMAT.md#authoring-invariants-validator-enforced).
It is fast and needs no sandbox.

`author test` runs the golden contract against a real sandbox, and needs a Linux
Docker daemon. It refuses rather than skips when it cannot reach one, because a
test run that reports success having tested nothing is worse than one that says
it could not run.

## 1. The anatomy of a level

Everything below is `packs/core-linux-basics/levels/14-pipe-05.yaml`, the boss
level of Act III. It is worth reading whole before you write your own, because
it uses nearly every feature there is.

**Identity and placement.** The file name carries a two-digit prefix for
ordering on disk; the `id` inside is what everything else refers to.

```yaml
id: pipe-05
version: 1
title: "The Log Sifter"
act: act3
boss: true

difficulty: 4
xp: 150
par_commands: 6
estimated_minutes: 15
concepts: [pipes, grep, wc, redirection]
prerequisites: [pipe-04]
tags: [text-processing, must-know]
```

`prerequisites` is what builds the curriculum DAG. A level stays locked until
every prerequisite is passed or skipped, and `shellforge map` draws the result.
`par_commands` is the count a competent person needs; beating it earns an
efficiency bonus, so guess it by solving the level yourself and adding one.

**The briefing** is the situation, not the instructions. It is rendered as
Markdown.

```yaml
briefing: |
  ## 02:41. Billing service.

  Your phone woke you up. Errors are pouring in and nobody knows how many.

  Today's rotated logs are in `~/quest/logs/`. There are three of them and they
  are long enough that scrolling is not a plan.

  Find out how bad it is, then find out which failures are involved. Leave the
  answers behind in `~/quest/`: the total in `report.txt`, and the codes in
  `codes.txt`.
```

Note what it does not do. It never names `grep`, `wc` or `|`. It says what has
to be true when the learner is done and leaves the route to them, which is the
whole reason verification looks at state rather than syntax.

**Objectives** are the checklist the learner sees. Each one maps to exactly one
check `id`, and the validator refuses a level where an objective has no check or
a check has no objective.

```yaml
objectives:
  - id: obj1
    text: "report.txt holds the total ERROR count"
  - id: obj2
    text: "codes.txt lists the distinct error codes, sorted"
  - id: obj3
    text: "Counted with a single pipeline"
    optional: true
```

`optional` lives on the objective and nowhere else. That is deliberate: it
describes the checklist line the learner reads, and one home means a level
cannot present something as a bonus and quietly gate on it at the same time.

**Setup** builds the world. `root` must be under `/home/learner/`, and that is
what makes `reset` safe: it is a delete and a rebuild of exactly that directory.

```yaml
setup:
  root: /home/learner/quest
  files:
    - path: logs/app-1.log
      source: assets/app-1.log
```

**Checks** are covered in sections 2 and 3 below. **Hints** and **solution** are
sections 5 and 6.

## 2. Choosing a check type

Fourteen types, in four families plus an escape hatch.
[LEVEL-FORMAT.md section 3](LEVEL-FORMAT.md#3-check-catalogue-v01-14-types) has
the exact YAML for each. This is how to pick one.

| You want to assert | Use |
|---|---|
| A file is there | `file_exists` |
| A file is gone, or was never made | `file_absent` |
| What is inside a file | `file_content`, with `match:` exact, trimmed_equals, contains, regex, sha256 or line_count |
| A directory is there | `dir_exists` |
| A directory matches another, by names or by content | `dir_tree` |
| Permissions | `file_mode`, with `mode:` for one answer or `modes:` for several |
| Ownership | `file_owner` |
| A symlink points somewhere | `symlink_target` |
| An environment variable is set, or set to something | `env_var` |
| Where the learner is standing | `cwd_is` |
| A process is running, or is not | `process_running`, with `negate:` for the second |
| They used a particular construct | `command_matched`, bonus only |
| They did not take a shortcut | `command_not_matched`, warn only |
| Something none of the above can express | `script` |

Two rules about the list decide most design questions.

**Prefer the most specific type that expresses the assertion.** `file_mode` and
a `script` running `stat -c %a` assert the same thing, and only one of them
produces a failure message the engine can explain. `script` is the escape hatch,
not the default: reach for it when nothing else fits, and when you do, remember
that the stdout of a failing script is appended to its `on_fail`, so use it to
say precisely what was wrong.

**`any_of` is how you accept more than one right answer.** If a level can be
solved two legitimate ways that leave different state, compose rather than
picking a winner:

```yaml
- id: obj2
  any_of:
    - { type: file_content, path: /tmp/out, match: regex, value: '^17$' }
    - { type: file_content, path: /tmp/out, match: regex, value: '^seventeen$' }
  on_fail: "The count doesn't look right."
```

`all_of` and `not` follow the same shape, and `id` and `severity` belong on the
outermost node only.

## 3. State checks and journal checks

This is the most important distinction on the page.

**A state check reads the system.** Did the file end up with the right number in
it? It does not care how. The learner can pipe, loop, use `awk`, or write a
script, and all of them pass, which is what makes the level teach a skill rather
than a command.

**A journal check reads what the learner typed.** `command_matched` and
`command_not_matched` are the only two, and they may never decide whether a
level is passed. The validator enforces it: such a check must sit on an
`optional: true` objective, or set `severity: warn`.

The reason is not stylistic. The journal is written by the shell instrumentation
from inside the sandbox, where a learner can forge an entry with a `printf` of
the right escape sequence, so a level that gates on it can be beaten without
being solved. The worse failure is the other direction: a pattern that did not
anticipate a valid solution fails somebody who did the work.

pipe-05 uses both, correctly:

```yaml
  - id: obj3
    type: command_matched
    pattern: 'grep[^|]*\|\s*wc\s+-l'
    scope: level
    on_fail: >-
      You got the count, and there is a shorter way. Try connecting grep
      straight into wc with a pipe, so nothing lands in a file in between.

  - id: nocheat
    severity: warn
    type: command_not_matched
    pattern: '^\s*echo\s+147'
    on_fail: >-
      Writing the number in by hand works tonight. It will not work at 03:00
      next time, when the count is different and you are the one who has to
      find it again.
```

The first is a bonus objective: the learner already passed by getting the
number, and this rewards the shorter route. The second is a warn, so it shows a
note and changes nothing. Neither can fail the level.

The legitimate cases for a journal check are small: a bonus for the elegant
route, the handful of levels where the syntax genuinely is the lesson (`tee`,
`find -exec`, `mkdir -p`), and an anti-pattern note like the one above.

## 4. Writing `on_fail`

**This is the single highest-leverage thing an author does.** It is the
difference between a level that teaches and a level that frustrates, and the
validator refuses an empty one for exactly that reason.

Write it in the voice of a helpful senior colleague who can see the learner's
screen. Never "incorrect". Never "try again". Name what is wrong and what to
look at.

```yaml
    on_fail: >-
      report.txt is missing, or the number in it is not the total. It has to be
      `~/quest/report.txt`: your shell starts in your home directory, not in
      `quest`. Are you searching every file in logs/, rather than one of them?
      `grep` can take a whole directory if you tell it to look inside.
```

Three things that message does, and all three are worth copying:

1. **It distinguishes the failure modes.** Missing, or there with the wrong
   number. Those are different problems and the learner knows which one they
   have.
2. **It names the most likely cause.** Writing to the wrong directory, because
   the shell starts in `~` and the briefing says `~/quest/`. That one mistake
   accounts for most failures on this level.
3. **It asks a question rather than giving the answer.** "Are you searching
   every file in logs/" points without solving. The answer is what hints are
   for, and they cost something.

## 5. The hint ladder

At least two hints. One hint is a cliff.

The ladder goes nudge, then concept, then mechanism, then solution, and the cost
rises with how much it gives away. pipe-05's is a good shape:

```yaml
hints:
  - cost: 5
    text: "`grep` searches inside files, and it can take a whole directory ..."
  - cost: 10
    text: "`-r` searches every file underneath a directory. `-i` ignores case ..."
  - cost: 15
    text: "`wc -l` counts the lines it is fed. Two commands can be joined ..."
  - cost: 25
    text: "For the codes, `grep -o` prints only the part that matched ..."
  - cost: 40
    reveal_solution: true
```

Note that no hint below the last one gives a runnable command. They give the
flag and what it does, and leave the assembly to the learner. A hint that can be
pasted is a solution with a smaller price tag.

The price is always shown before it is charged, and the total is subtracted from
the level's award. The award never goes below zero, so a learner who takes every
hint earns nothing rather than losing points they already had.

## 6. Deterministic setup, and the solution block

**Setup never touches the network.** Not once, not for a small file, not behind
a cache. Levels ship their assets, or generate them from a seed. The same level
must produce byte-identical starting state on every machine, every time, or a
check that passes for you fails for somebody else and neither of you can
reproduce it.

`touch -d` matters more than it looks like it should. If a level depends on a
modification time, an ordering, or anything `ls -t` would show, set it
explicitly. Files materialized in a fresh sandbox all have roughly the same
timestamp, and "roughly" is where the flake lives.

`setup.script` runs as `learner`, not root, with the working directory set to
`setup.root`. Do not chown in it and do not expect root: the sandbox drops all
capabilities except `CHOWN` and `FOWNER`, so root inside it does not bypass
ordinary permission checks the way you expect.
[LEVEL-FORMAT.md section 2](LEVEL-FORMAT.md#setup-and-teardown-execution) has
the whole execution contract, including rollback.

**The `solution` block is not documentation. It is executed.**

```yaml
solution: |
  grep -ri ERROR ~/quest/logs/ | wc -l > ~/quest/report.txt
  grep -rhoE 'E[0-9]{3}' ~/quest/logs/ | sort -u > ~/quest/codes.txt
```

The golden test runs it as `learner` in a login shell from `/home/learner`, and
every required check must pass afterwards. It also builds a journal from it, one
command per non-empty non-comment line, which is what a `command_matched` check
is tested against. A pattern written to match a multi-line construct on one line
will not match there, because the solution is split into fragment lines rather
than run as one command.

## 7. Assets that agree with the answer

If a check asserts `147`, then the three log files must actually contain 147
matching lines, and `codes.txt` must actually be `E401`, `E500`, `E503` and
nothing else. Generate the assets and derive the expected value from them.
Never the other way around.

The failure mode here is quiet. A hand-edited log file and a hand-written
expected count drift by one, the golden test catches it, and you spend an hour
deciding which number is right. Compute the answer from the asset and the
question never comes up.

## 8. The golden test, and what to do when it fails

`shellforge author test <id>` runs six steps against a real sandbox:

1. Fresh sandbox, run `setup`.
2. Run the checks. **Every required check must fail**, and at least one must. A
   level that passes before the learner has done anything is a level whose
   checks are wrong.
3. Run `solution` as `learner`.
4. Run the checks. **All required checks must pass.**
5. Run `teardown`. `setup.root` is gone and no stray processes remain.
6. Run the checks twice and hash the filesystem either side. **Identical.**

Step 2 is asserted per objective rather than on the overall verdict. A level can
ship with one check that passes on a bare setup, testing nothing, and still
report "not passed" because of its siblings. Checking each one is what catches
that, and it caught two real levels the first time it ran.

When it fails, the rule is the one that matters most in this repository:

**Never loosen a check to make your solution pass.** If the `solution` block
does not satisfy the check, the level is wrong, not the check. Quietly relaxing
a check until it goes green is how a level ends up accepting a wrong answer,
which teaches the wrong thing to everyone who plays it afterwards.

The inverse has a rule too. **Never tighten a check without confirming it still
accepts valid alternative solutions.** Solve the level a second way, deliberately
not the way you first thought of, and run the checks against that.

Step 6 is the purity contract: checks read and never write. If it fails, one of
your checks has a side effect, and a `script` check is almost always the
culprit.

`author test` needs a Linux Docker daemon and refuses without one.

## 9. Design principles worth internalising

From [CURRICULUM.md](CURRICULUM.md), which is the best available example of
these being applied to 25 levels:

- **Every level teaches one new thing and reuses two old ones.** New in
  isolation does not stick.
- **Make the painful way visible before the good way.** Sixty files before
  globs. A four thousand line `cat` before `head`. The learner has to feel the
  problem before the tool is interesting.
- **Bosses have no numbered steps.** Give them a broken system and "make it
  work", with several valid fixes that all pass.
- **Beginner levels must be winnable in under sixty seconds.** Confidence
  first, difficulty later.
- **Check the learner never has to guess what "done" means.** If the briefing
  and the objectives disagree, the briefing is wrong.

## 10. Publishing

There is no remote pack downloading in v0.1.0. The pack in
`packs/core-linux-basics/` is embedded in the binary, so a new level reaches
people by being merged.

See [CONTRIBUTING.md](../CONTRIBUTING.md) for the pull request process. A level
contribution needs `author validate` clean, `author test` passing, and both
stated in the pull request body.
