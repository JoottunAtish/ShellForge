# Levels

One YAML file per level, named `<NN>-<id>.yaml` so that a directory listing
matches campaign order. The `id` field inside the file is what the engine uses;
the numeric prefix is for humans.

Schema and the full check catalogue: [../../../docs/LEVEL-FORMAT.md](../../../docs/LEVEL-FORMAT.md)
Level designs, including objectives, checks and hints: [../../../docs/CURRICULUM.md](../../../docs/CURRICULUM.md)

## Writing one

```bash
shellforge author scaffold pipe-06
shellforge author validate packs/core-linux-basics
shellforge author test pipe-06
shellforge run pipe-06
```

## Non-negotiables for every level

- Every check has a specific, actionable `on_fail`. Never "incorrect".
- Setup is deterministic and offline. Explicit `touch -d` for any mtime the
  level depends on, seeded generators, no network.
- At least two hints, escalating from nudge to concept to near-solution to
  reveal.
- A working `solution` block. The golden test runs it.
- `setup.root` under `/home/learner/`, so reset stays safe.

## Status

Nine levels are written: `nav-01` to `nav-04`, `files-01` to `files-04`, and
`pipe-05`. They validate clean, are embedded in the binary, and are playable with
`shellforge run <id>`.

`pipe-05` is out of curriculum order on purpose. It is the verification engine's
reference fixture and the level `docs/LEVEL-FORMAT.md` section 6 is written
against, so it exercises every reporting shape at once: two required objectives,
one bonus objective, and one `severity: warn` note that produces advice rather
than a checklist line. It is also the first level whose world comes from
committed assets rather than inline content or a generator. Its
`prerequisites: [pipe-04]` does not resolve yet, which `author validate` reports
as a warning, and that is the intended signal rather than a problem.

Levels 9 to 25 are Day 5. `shellforge author validate` reports each of them as
a warning until its file exists, which is the intended signal.

## Two things a level's assets have to satisfy

- **`source:` is relative to the pack root**, so `source: assets/app-1.log` reads
  `packs/core-linux-basics/assets/app-1.log`. The setup runner is handed a
  filesystem already rooted at the pack, which is why the path carries no pack
  directory name.
- **A committed asset and a `generate:` block are mutually exclusive** for the
  same file. One of them would be dead and nobody would know which.

## The golden contract every level must satisfy

`make golden` runs this for each level, and CI runs it in the Sandbox image job:

1. Set the level's world up in a fresh sandbox.
2. Run the checks. **Every required objective must FAIL**, and at least one must.
   A level that passes before the learner has done anything has checks that
   verify nothing. The exception is an objective marked `preserves: true`, which
   must **PASS** here instead: see below.
3. Run the level's own `solution` as the learner, in a login shell, from
   `/home/learner`. That is where a learner's shell starts too, and the two have
   to match or this stops testing the level anybody actually plays.
4. Run the checks. **Every required objective must PASS**, preserving ones
   included. If one does not, the level is wrong; never loosen a check to make
   its own solution pass.
5. Run the checks twice more and hash the world before and after. It must be
   identical: a check reads and nothing else.
6. Tear down. `setup.root` must be gone and no process the level started may
   still be running.

Step 2 is asserted per objective, not on the overall result, and that is the part
that earns its keep: it caught two levels here whose setup script had silently
never created a directory their own solutions needed.

`preserves: true` is for an objective the learner earns by **not breaking**
something. `files-04`'s "important/ is untouched, byte for byte" is the only one
in this pack: the level teaches deletion, and that objective is already true when
the level starts. It is not the same as `optional`, because a learner who deletes
`important/` still fails, and it cannot be used to quiet the gate, because step 2
still requires at least one required non-preserving objective to fail.

## Three conventions every level here follows

- **Everything the learner produces lives inside `setup.root`.** Teardown is
  `rm -rf` on that path and goes no further, so anything written outside it
  survives the level and leaks into the next one.
- **`setup.script` runs as the learner, and must not chown.** The sandbox
  withholds `CAP_DAC_OVERRIDE` from root, so a script running as root cannot even
  write into the learner-owned level root. The runner chowns the level root and
  everything under it to the learner, then re-applies any `owner:` a
  `setup.files` entry declared, so there is nothing left for the script to
  chown. Need a file owned by someone other than the learner? Declare
  `owner:` on that `setup.files` entry; see `docs/LEVEL-FORMAT.md`'s
  "Declaring root-owned state". If your script has more than one command, put
  `set -e` at the top: none is injected, so otherwise only the last command's
  exit status counts and an earlier failure ships silently.
- **A journal check never gates passing.** `command_matched` and
  `command_not_matched` are bonus objectives or `severity: warn` notes. The
  validator rejects a level where one is neither.
