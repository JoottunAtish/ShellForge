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

Empty. Levels 1 to 8 are written on Day 2, levels 9 to 25 on Day 5.
