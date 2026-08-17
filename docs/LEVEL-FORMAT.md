# Shellforge Level Format and Check Catalogue (v0.1)

This is the contract between content and engine. Claude Code implements against it; you author against it. `shellforge author validate` enforces it.

---

## 1. Pack structure

```
packs/core-linux-basics/
├── pack.yaml
├── assets/                    # files materialized into the sandbox during setup
│   ├── access.log
│   └── check-nodes.sh
└── levels/
    ├── 01-nav-01.yaml
    └── ...
```

### `pack.yaml`

```yaml
id: core-linux-basics
name: "Meridian Logistics: Atlas"
version: 0.1.0
content_api: 1              # engine refuses packs it can't understand
author: "..."
license: MIT
description: "25 levels, basics to intermediate."
acts:
  - id: act1
    title: "Orientation"
    subtitle: "Your first day."
    levels: [nav-01, nav-02, nav-03, nav-04]
  - id: act2
    title: "Files and Directories"
    levels: [files-01, files-02, files-03, files-04, files-05]
```

---

## 2. Level schema

Field key: **R** required · *O* optional.

```yaml
# ── identity ──────────────────────────────────────────────
id: pipe-05                    # R  unique across pack, [a-z0-9][a-z0-9-]*
version: 1                     # R  bump when checks change (invalidates best scores)
title: "The Log Sifter"        # R
act: act3                      # R  must exist in pack.yaml

# ── metadata ──────────────────────────────────────────────
difficulty: 4                  # R  1..5
xp: 150                        # R
par_commands: 6                # O  for efficiency bonus; omit to disable
estimated_minutes: 15          # O
concepts: [pipes, grep, wc]    # R  ≥1; drives the concept coverage report
prerequisites: [pipe-04]       # O  DAG edges; must be acyclic
requires: []                   # O  runtime caps: networking|systemd|multiuser
boss: true                     # O  changes presentation, no numbered steps shown

# ── presentation ──────────────────────────────────────────
briefing: |                    # R  markdown → ANSI
  ## 02:41 - Billing service
  Errors are pouring in and nobody knows how many.
  Logs are in `~/quest/logs/`.

objectives:                    # R  ≥1; ids must match check ids
  - id: obj1
    text: "report.txt contains the error count"
  - id: obj2
    text: "Solved in one pipeline"
    optional: true             # O  bonus XP, never blocks passing; declared here and nowhere else
  - id: obj3
    text: "important/ is untouched"
    preserves: true            # already true at setup; earned by not breaking it

# ── world setup ───────────────────────────────────────────
setup:                         # R
  root: /home/learner/quest    # R  everything lives here; reset = rm -rf this
  files:                       # O
    - path: logs/app-1.log     # relative to root
      source: assets/app-1.log # from pack; \r\n normalised on materialize
      mode: "0644"             # O  default 0644
      owner: "learner:learner" # O  default learner:learner; see "declaring root-owned state" below
    - path: notes.txt
      content: |               # O  inline alternative to `source`
        Kofi was here.
    - path: data/big.log
      generate:                # O  deterministic generator
        kind: loglines
        seed: 91731
        lines: 4000
  script: |                    # O  runs as the learner AFTER files, in sandbox
    touch -d "2 days ago" logs/app-1.log
  timeout_seconds: 30          # O  default 30, bounds setup.script

# ── verification ──────────────────────────────────────────
checks: [ ... ]                  # R  see §3

# ── help ──────────────────────────────────────────────────
hints:                         # R  ≥2; last should reveal or near-reveal
  - cost: 5
    text: "`grep` searches many files at once."
  - cost: 15
    text: "`-r` recurses, `-i` ignores case."
  - cost: 40
    reveal_solution: true

solution: |                    # R  used by golden tests + `hint --reveal`
  grep -ri ERROR ~/quest/logs/ | wc -l > ~/quest/report.txt

teardown:                      # O  default: rm -rf setup.root
  script: |
    pkill -f atlas-indexer || true

tags: [text-processing, must-know]   # O
```

A `setup.files` entry sets exactly one of `source`, `content`, and `generate`; the
runner refuses an entry that sets none or more than one. An explicit `content:` key
counts as choosing content even when the value is empty, which is not the same
question as whether the key was present at all: `files-01`'s `.gitkeep` is authored as
`content: ""` for exactly this reason, to materialize a real, empty file. An absent
`content:` key is not the same as an empty one.

`mode` is a quoted string such as `"0644"`, not a bare `0644`. An unquoted `0644` is
YAML 1.1 octal, which most YAML parsers, including this one, read as the number 420
rather than the permission bits an author meant. The runner parses the string with
`strconv.ParseUint(s, 8, 32)` and refuses anything above `0o777`, so a malformed value
is a load error naming the file, not a silently wrong permission bit.

`teardown:` may carry a `script:`, as shown above, or may be `{}`, as shown in
section 6's worked example, when a level needs no teardown step beyond the default
`rm -rf setup.root`; `script:` on both `setup` and `teardown` accepts the block
scalar form shown above and the plain scalar form (`script: "true"`) alike.

### Setup and teardown execution

`setup.script` runs as **`learner`**, inside the sandbox, after every file in
`setup.files` is materialized, with the working directory set to the resolved
`setup.root`, under `setup.timeout_seconds` (default 30 seconds). It is passed
to the sandbox as a single argument to `bash -c`, never concatenated into a host
command line. No `set -e` is injected: adding it silently would change the
meaning of every example in this document, so the author decides. A non-zero
exit, or a timeout, rolls the whole setup back, exactly as if `setup.files` had
failed to materialize: nothing is left half-built.

`teardown.script` runs as `learner` too, but with the working directory
`/home/learner`, not `setup.root`: the root is not guaranteed to exist when
teardown runs, since teardown also runs before every setup to keep it
idempotent.

**Do not chown in a setup script, and do not expect root.** Both scripts used to
run as `root`, and the change is worth understanding because it is not a
preference. The sandbox starts with `--cap-drop ALL` and adds back only `CHOWN`
and `FOWNER`, so root inside it does not hold `CAP_DAC_OVERRIDE` and does not
bypass ordinary permission checks. The level root has already been chowned to
the learner by the time a script runs, mode 0755, which makes root *other* on
it: a script doing `mkdir -p reports` as root fails with `EACCES`. The learner
owns the tree, so the learner is the identity that can work in it. The runner
chowns the level root and everything under it to the learner, then re-applies
any `owner:` a `setup.files` entry declared, so a script still has nothing to
chown, and a declared owner survives to when your script runs.

**Declaring root-owned state.** A level that needs a file owned by someone
other than the learner, such as `root`, declares `owner:` on that
`setup.files` entry rather than trying to chown it from a script: `PushFiles`
extracts through the daemon, which is outside the container's own capability
set, so this is the only place in a level that can create root-owned state.
Only a **file** can carry a declared owner. A directory always belongs to the
learner, no matter what its contents declare, so that teardown's `rm -rf` as
the learner can always remove it. `setup.script` cannot create root-owned
state either way: it runs as the learner.

This bit real levels. `files-03` and `files-04` both shipped with a
`mkdir -p` in their setup script and both were silently missing that directory,
because `setup.files` arrive through `docker cp` with daemon privilege and land
regardless, and because both scripts ended with a `chown` that root *can*
perform, so `bash -c` returned the chown's zero exit and discarded the failure
before it. Setup reported success. The golden contract in section 7 is what
found them, which is the argument for running it on every level.

Since no `set -e` is injected, a script whose last command succeeds still
reports success. If your script has more than one command and you want the
first failure to fail the level, write `set -e` yourself at the top.

Setup always tears down first, unconditionally, before staging anything. This
is what makes it safe to run a level's setup twice in a row, or against a level
root a previous run left dirty.

Every level's world lives under `setup.root`, and `setup.root` must resolve
under `/home/learner/`. The engine also refuses a `setup.root` that equals,
contains, or is contained by the state directory (below), because a level whose
root reached that far would let its own reset delete the journal, the command
history, and every other level's progress marker.

### The state directory and the SETUP_OK marker

`SF_STATE`, the directory the shell instrumentation and the level engine share
for the journal, the environment snapshots, and the control channel, defaults to
`/home/learner/.shellforge`. It lives inside the sandbox user's own home, not
under `/opt/shellforge`: `/opt/shellforge` is the one host mount, and it is
read-only, so nothing that needs to be written at play time can live there.

Once a level's setup finishes every step successfully, the engine writes a
marker file at `<SF_STATE>/levels/<level-id>/SETUP_OK`. That marker never lives
inside `setup.root`: a marker inside the level's own world would show up in
`ls -a ~/quest`, could be deleted by the learner, and would be counted by any
level that checks how many files are under its root. Authors do not create,
read, or rely on this file directly; it exists only so the engine can tell
whether a level's world is already in place.

### Authoring invariants (validator-enforced)

| Rule | Reason |
|---|---|
| `id` matches `[a-z0-9][a-z0-9-]*`, first character alphanumeric | A leading hyphen makes the id flag-shaped; see the security skill |
| Every check has a non-empty `on_fail` | Generic failure messages are the #1 quality killer |
| Every `objectives[].id` has a matching check `id` and vice versa | Keeps the HUD checklist honest |
| A `severity: warn` check needs no objective | It produces a note (§5), never a checklist line, so there is nothing to correspond to. A bonus objective is shown to the learner, so the check behind an `optional: true` objective still needs its objective; only a warn note is exempt. |
| `severity` and `id` appear only on the outermost check of an objective, never on a composition branch | They describe a whole objective; the engine evaluates a composite structurally, so on a branch they would be silently ignored |
| `optional` appears on the objective, never on a check or a composition branch | It describes the checklist line the learner reads. One home means a level cannot present a bonus and gate on it at the same time |
| `solution` present and non-empty | Golden tests need it |
| ≥2 hints | One hint is a cliff |
| `setup.root` under `/home/learner/` | Reset safety |
| No `source:` pointing outside the pack | Reproducibility |
| DAG acyclic, all `prerequisites` resolve | Unlock logic |
| Checks whose objective is not `optional` ≥1 | A level you can't fail isn't a level |
| `command_matched` and `command_not_matched` must sit on an `optional: true` objective, or set `severity: warn` on the check | The journal is learner-influenced and may never decide pass or fail |
| A check declares no parameter outside the set its type accepts | A mistyped `sha526` or `compare-to` used to be dropped in silence, leaving a check that asserted something other than what the author wrote. The error names the bad key and lists what the type takes. |

---

## 3. Check catalogue (v0.1, 14 types)

Common fields on every check: `id` (R), `on_fail` (R), `severity` (O: `fail`|`warn`), `timeout_seconds` (O). `optional` is not a check field: it is declared on the objective (section 2), and a check that sets it is refused when the level loads.

Checks are **read-only**. A check that mutates state is a bug and CI catches it (§ purity test).

### Filesystem

```yaml
- id: obj1
  type: file_exists
  path: /home/learner/quest/report.txt
  on_fail: "I don't see report.txt yet - check `ls ~/quest`."
```

```yaml
- type: file_absent
  path: .../scratch.tmp
```

```yaml
- type: file_content
  path: .../report.txt
  match: trimmed_equals      # exact | trimmed_equals | contains | regex | sha256 | line_count
  value: "147"
  ignore_case: false         # O
```

`value` is required on every match mode, but an empty one means different
things and is not always legal:

| Match mode | `value: ""` |
|---|---|
| `exact` | Legal. Asserts the file is empty. |
| `trimmed_equals` | Legal. Asserts the file is empty or only whitespace. |
| `contains` | **Refused when the level loads.** An empty substring is in every file, so the check could never fail. |
| `regex` | **Refused when the level loads.** The empty pattern matches everything, so the check could never fail. |
| `line_count` | Refused when the level loads, since it is not an integer. |
| `sha256` | Accepted, but nothing hashes to the empty string, so the check always fails. |

The three refusals happen when the level loads, not when a learner runs
`check`. A blank placeholder nobody filled in is the way an empty value reaches
a pack, and an objective that passes whatever the learner typed is worse than
one that is missing. `sha256` is left alone because it errs the other way: a
level author sees their own check failing rather than a learner being told they
solved something they did not.

```yaml
- type: dir_exists
  path: .../logs
```

```yaml
- type: dir_tree             # structural + optional content equality
  path: .../config.bak
  compare_to: .../config       # another path in the sandbox
  mode: names_and_hashes     # names_only | names_and_hashes
```

```yaml
- type: file_mode
  path: .../deploy.sh
  mode: "0700"               # exact octal
  # or: modes: ["0700","0750"]
```

Set `mode` for one acceptable mode, or `modes` for several. One of the two is
required, and `modes` wins if both are set.

The list parameter is `modes`, not `any_of`. `any_of` is a composition key
(see Composition below), so a check that writes it is read as a composition
node rather than as a `file_mode` check, and a check cannot be both a type and
a composition node.

```yaml
- type: file_owner
  path: .../data.db
  owner: learner
  group: logistics           # O
```

```yaml
- type: symlink_target
  path: .../current
  target: .../releases/v2
```

### Shell state (reads the env/cwd snapshot from §4.7)

```yaml
- type: env_var
  name: ATLAS_ENV
  value: production          # O; omit to just assert it's set
```

```yaml
- type: cwd_is
  path: /home/learner/quest/logs
```

### Process

```yaml
- type: process_running      # matches against `ps -eo pid,args`
  pattern: heartbeat\.sh
  # negate: true  → asserts NOT running
```

### Journal / behavioural

**A journal check may never gate passing.** Its objective must be `optional:
true`, or the check must set `severity: warn`, and `shellforge author validate`
rejects a level where neither is true. The journal records what the learner
typed, and the shell instrumentation writes it from inside the sandbox, where a
learner can forge an entry with a `printf` of the right escape sequence. A
level that stakes passing on that signal can be beaten without solving it,
and, worse, can fail a learner who solved it a way the pattern did not
anticipate. Use these for bonus objectives, for the handful of levels where
the syntax genuinely is the lesson, and for anti-pattern warnings.

```yaml
- id: obj3
  type: command_matched        # R  its objective must be optional: true (see section 2)
  pattern: 'grep.*\|\s*wc\s+-l'
  scope: level               # level | last_n:5 | last
```

```yaml
- type: command_not_matched
  pattern: '^\s*echo\s+\d+'
  severity: warn             # warn = shows a note, still passes
```

### Escape hatch

```yaml
- type: script
  as_user: learner           # O; default learner. `root` for privileged assertions
  run: |
    cd /opt/atlas && ./run.sh >/dev/null 2>&1 || exit 1
    test -s /var/log/atlas/nightly.log || { echo "job ran but wrote no log"; exit 1; }
  on_fail: "The nightly job still isn't producing output."
```
`exit 0` = pass. **stdout of a failing script is appended to `on_fail`** - use it to give precise diagnostics that static checks can't.

### Composition

```yaml
- id: obj2
  any_of:
    - { type: file_content, path: /tmp/out, match: regex, value: '^17$' }
    - { type: file_content, path: /tmp/out, match: regex, value: '^seventeen$' }
  on_fail: "The count doesn't look right."
```
`all_of` and `not` follow the same shape. Top-level `checks` is an implicit `all_of` over the entries that gate passing, that is, those whose objective is not `optional` and whose `severity` is not `warn`.

### Deferred to v0.2 (do not implement this week)
`port_listening`, `service_active`, `cron_entry_exists`, `user_exists`, `group_membership`, `package_installed`, `disk_usage_under`, `alias_defined`, `function_defined`, `shopt_set`, `mtime_within`, `hardlink_count`, `output_contains`, `command_count_under`, `solved_within_seconds`.

> **Note:** levels 20, 21 and 25 in the curriculum reference some deferred types. Implement them with `type: script` for v0.1 - it covers every case at the cost of a less pretty YAML. Promote the common ones to first-class types in v0.2.

---

## 4. Check execution contract

1. Checks run via `Runtime.Exec()` in a **fresh non-interactive shell**, never in the learner's shell.
2. Default user is `learner`; `as_user: root` for privileged assertions.
3. Shell-local state (env, cwd, aliases) comes from the snapshot files written by `PROMPT_COMMAND`, **not** from the check's own shell.
4. Checks execute in declaration order. Evaluation does **not** short-circuit - all run, so the objective checklist is fully populated.
5. **Only the first failing required check's `on_fail` is displayed.** Optional/warn results are shown as notes below.
6. Default timeout 10 s per check, 60 s per level. Timeout = fail with a distinct message.

`any_of`, `all_of`, and `not` are structural, not registered check types: the catalogue
in section 3 stays at fourteen, and there is no `type: any_of`. A composition node's
branches have no `id`, never appear in `objectives`, and rule 4's "no short-circuit"
applies to objectives, not to a composite's internal branches: once a composite's own
answer is decided, evaluating its remaining branches would cost sandbox round trips and
tell the learner nothing, so it stops. The checklist stays complete either way, because
the composite itself is the objective and is always reported.

When the 60 second level budget is exhausted, the checks that have not yet run are
**marked** as not evaluated rather than silently dropped, for the same reason rule 4
exists: a learner reading the result sees a complete checklist, one that says "we ran
out of time on this one" rather than one with entries missing.

---

## 5. Result shape (engine → UI)

```json
{
  "level_id": "pipe-05",
  "passed": false,
  "objectives": [
    {"id":"obj1","text":"report.txt holds the total ERROR count","status":"pass"},
    {"id":"obj2","text":"codes.txt lists the distinct error codes, sorted","status":"fail",
     "message":"codes.txt should hold the distinct codes, one per line, sorted."},
    {"id":"obj3","text":"Counted with a single pipeline","status":"fail","optional":true,
     "message":"You got there, but there is a one-liner."}
  ],
  "primary_failure": {"id":"obj2","text":"codes.txt lists the distinct error codes, sorted",
                      "status":"fail",
                      "message":"codes.txt should hold the distinct codes, one per line, sorted."},
  "notes": ["You got there, but there is a one-liner.",
            "Hardcoding the answer works, but you won't learn the pipeline."],
  "score": {"base":150,"hint_penalty":20,"efficiency_bonus":15,"first_try":0,"total":145},
  "duration_ms": 412
}
```

`status` is one of `pass`, `fail`, `warn`, `timeout`, `error`. The example shows only
`pass` and `fail` because that run produced only those two. `warn` is what a
`severity: warn` check's failure becomes; `timeout` and `error` are described in §4.6 and
are deliberately distinct from `fail`, because "you have not solved it" and "we could not
tell" need different words in front of a beginner.

`primary_failure` is the first failing check that is **neither a bonus objective
nor `severity: warn`**, per §4.5, and it repeats that objective rather than referring to it by
id. A bonus objective is never the primary failure: presenting one as the headline reason
a level failed teaches the learner that a bonus was mandatory. It is absent, not null, when
nothing blocking failed.

`notes` carries the message of every non-passing result that does not block: bonus
objectives the learner missed, and `severity: warn` anti-pattern checks. A `severity: warn`
check produces a note and **no** entry in `objectives`, which is why §2's invariants say it
needs no objective.

`objectives` and `notes` are always arrays, never null, so a consumer can count them
without a special case.

`score` is populated by `internal/game`, not by the verification engine: it needs the hint
ledger and the command count, which live with the learner's progress rather than with the
sandbox. The engine's own result type omits the field entirely and the game layer adds it,
so a caller reading this document sees one shape either way.

The engine's `Run` returns this result and never an error. A cancelled run still
produces a complete checklist rather than a bare failure: every check that did not get
to run is marked the way the level budget's timeout is (§4), not omitted. A caller that
needs to know whether the learner interrupted the run rather than the level genuinely
failing inspects `ctx.Err()` itself, rather than being handed a second, competing signal
to reconcile against this result.

---

## 6. Worked example - complete level

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

briefing: |
  ## 02:41 - Billing service
  Errors are pouring in. Nobody knows how many.
  Today's rotated logs are in `~/quest/logs/`.

  Find out how bad it is.

objectives:
  - id: obj1
    text: "report.txt holds the total ERROR count"
  - id: obj2
    text: "codes.txt lists the distinct error codes, sorted"
  - id: obj3
    text: "Counted with a single pipeline"
    optional: true

setup:
  root: /home/learner/quest
  files:
    - { path: logs/app-1.log, source: assets/app-1.log }
    - { path: logs/app-2.log, source: assets/app-2.log }
    - { path: logs/billing.log, source: assets/billing.log }

checks:
  - id: obj1
    type: file_content
    path: /home/learner/quest/report.txt
    match: trimmed_equals
    value: "147"
    on_fail: "report.txt is missing or the number is off. Are you searching *all* the files in logs/? Is your search case-insensitive?"

  - id: obj2
    type: file_content
    path: /home/learner/quest/codes.txt
    match: trimmed_equals
    value: "E401\nE500\nE503"
    on_fail: "codes.txt should hold the distinct codes, one per line, sorted. `sort` and `uniq` are friends."

  - id: obj3
    type: command_matched
    pattern: 'grep[^|]*\|\s*wc\s+-l'
    scope: level
    on_fail: "You got there, but there is a one-liner hiding in this. Try connecting grep straight into wc."

  - id: nocheat
    severity: warn
    type: command_not_matched
    pattern: '^\s*echo\s+147'
    on_fail: "Hardcoding the answer works today, but not at 3 AM next time."

hints:
  - cost: 5
    text: "`grep` can take a whole directory if you tell it to look inside."
  - cost: 10
    text: "`-r` searches recursively; `-i` ignores case."
  - cost: 15
    text: "`wc -l` counts the lines it's fed on stdin. What connects two commands together?"
  - cost: 40
    reveal_solution: true

solution: |
  grep -ri ERROR ~/quest/logs/ | wc -l > ~/quest/report.txt
  grep -rhoE 'E[0-9]{3}' ~/quest/logs/ | sort -u > ~/quest/codes.txt

teardown: {}
tags: [text-processing, must-know]
```

---

## 7. Authoring workflow

```bash
shellforge author scaffold pipe-06        # generate skeleton + assets dir
$EDITOR packs/core-linux-basics/levels/pipe-06.yaml
shellforge author validate packs/core-linux-basics
shellforge author test pipe-06            # golden test: fails clean → solution → passes
shellforge run pipe-06                    # play it yourself
```

**Golden test contract** (what `author test` asserts, and what CI runs for every level):
1. Fresh sandbox, run `setup`.
2. Run checks → **every required check must FAIL**, and at least one must. (If a level passes before you have done anything, the checks are wrong.) The exception is an objective marked `preserves: true`, which must **PASS** here instead: see below.
3. Run `solution` as `learner` in a login shell, from `/home/learner`.
4. Run checks → **all required checks must PASS.**
5. Run `teardown` → `setup.root` no longer exists and no stray processes remain.
6. Run checks twice, hash the filesystem before and after → **identical** (purity).

Step 2 is asserted **per objective**, not on the overall pass/fail. That matters: a
level can ship with one required check that passes on a bare setup, testing nothing
the learner has to do, and the level would still report "not passed" because of its
siblings. Checking each one is what catches that, and it caught two real levels the
first time it ran.

`preserves: true` is the one exemption, for an objective the learner satisfies by
**not breaking** something rather than by doing something. `files-04` is the case:
it teaches deletion, and "important/ is untouched, byte for byte" is already true
at setup. Such an objective is inverted at step 2, where it must pass rather than
fail, because one that is already false means the level is asking the learner to
protect something its own setup never created. It is **not** the same as
`optional`: a learner who deletes `important/` still fails the level. And it cannot
be used to quiet the gate, because step 2 also requires that at least one required
non-preserving objective failed.

`author test` **refuses** when it cannot reach a Linux Docker daemon, rather than
skipping. A `make golden` that reports success having tested nothing is worse than one
that reports it could not run: the whole point of the golden contract is that every
level's checks and solution were actually exercised against the sandbox, not merely
that the command exited zero. The Go golden test carrying the same six steps is gated
on the `SHELLFORGE_GOLDEN=1` environment variable as well as a Linux daemon, so
`go test ./...` in the fast CI job never builds the Containerfile or spins up a
container; only the job that sets that variable does.
