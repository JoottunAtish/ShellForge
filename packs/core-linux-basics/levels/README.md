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

Levels 1 to 8 are written: `nav-01` to `nav-04` and `files-01` to `files-04`.
They validate clean and are embedded in the binary. None has been played or
golden-tested yet, because the check engine and the `run` wiring are not built.

Levels 9 to 25 are Day 5. `shellforge author validate` reports each of them as
a warning until its file exists, which is the intended signal.

## Two conventions every level here follows

- **Everything the learner produces lives inside `setup.root`.** Teardown is
  `rm -rf` on that path and goes no further, so anything written outside it
  survives the level and leaks into the next one.
- **A journal check never gates passing.** `command_matched` and
  `command_not_matched` are bonus objectives or `severity: warn` notes. The
  validator rejects a level where one is neither.
