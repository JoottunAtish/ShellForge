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
id: pipe-05                    # R  unique across pack, [a-z0-9-]+
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
    optional: true             # optional = bonus XP, never blocks passing

# ── world setup ───────────────────────────────────────────
setup:                         # R
  root: /home/learner/quest    # R  everything lives here; reset = rm -rf this
  files:                       # O
    - path: logs/app-1.log     # relative to root
      source: assets/app-1.log # from pack; \r stripped on materialize
      mode: "0644"             # O  default 0644
      owner: "learner:learner" # O  default learner:learner
    - path: notes.txt
      content: |               # O  inline alternative to `source`
        Kofi was here.
    - path: data/big.log
      generate:                # O  deterministic generator
        kind: loglines
        seed: 91731
        lines: 4000
  script: |                    # O  runs as root AFTER files, inside sandbox
    touch -d "2 days ago" logs/app-1.log
    chown -R learner:learner .
  timeout_seconds: 30          # O  default 30

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

### Authoring invariants (validator-enforced)

| Rule | Reason |
|---|---|
| Every check has a non-empty `on_fail` | Generic failure messages are the #1 quality killer |
| Every `objectives[].id` has a matching check `id` and vice versa | Keeps the HUD checklist honest |
| `solution` present and non-empty | Golden tests need it |
| ≥2 hints | One hint is a cliff |
| `setup.root` under `/home/learner/` | Reset safety |
| No `source:` pointing outside the pack | Reproducibility |
| DAG acyclic, all `prerequisites` resolve | Unlock logic |
| Non-optional checks ≥1 | A level you can't fail isn't a level |

---

## 3. Check catalogue (v0.1 - 13 types)

Common fields on every check: `id` (R), `on_fail` (R), `optional` (O, default false), `severity` (O: `fail`|`warn`), `timeout_seconds` (O).

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
  # or: any_of: ["0700","0750"]
```

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

```yaml
- type: command_matched
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
`all_of` and `not` follow the same shape. Top-level `checks` is an implicit `all_of` over non-optional entries.

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

---

## 5. Result shape (engine → UI)

```json
{
  "level_id": "pipe-05",
  "passed": false,
  "objectives": [
    {"id":"obj1","text":"report.txt contains the error count","status":"pass"},
    {"id":"obj2","text":"Solved in one pipeline","status":"fail","optional":true,
     "message":"You got there - but there's a one-liner."}
  ],
  "primary_failure": {"id":"obj2","message":"..."},
  "notes": ["Hardcoding the answer works, but you won't learn the pipeline."],
  "score": {"base":150,"hint_penalty":20,"efficiency_bonus":15,"first_try":0,"total":145},
  "duration_ms": 412
}
```

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
  script: chown -R learner:learner .

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
    optional: true
    type: command_matched
    pattern: 'grep[^|]*\|\s*wc\s+-l'
    scope: level
    on_fail: "You got there - but there's a one-liner hiding in this. Try connecting grep straight into wc."

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
2. Run checks → **all required checks must FAIL.** (If a level passes before you've done anything, the checks are wrong.)
3. Run `solution` as `learner` in a login shell.
4. Run checks → **all required checks must PASS.**
5. Run `teardown` → `setup.root` no longer exists and no stray processes remain.
6. Run checks twice, hash the filesystem before and after → **identical** (purity).
