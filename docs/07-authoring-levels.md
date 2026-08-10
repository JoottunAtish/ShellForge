# Authoring levels

> **Status: outline.** Written on Day 7, derived from
> [LEVEL-FORMAT.md](LEVEL-FORMAT.md), which is the authoritative schema. Read that
> one until this page exists properly.

A level is a YAML file plus its assets. You do not need to write any Go.

## Quick start

```bash
shellforge author scaffold my-level
$EDITOR packs/core-linux-basics/levels/my-level.yaml
shellforge author validate packs/core-linux-basics
shellforge author test my-level
shellforge run my-level
```

## What this page will cover

1. The anatomy of a level, walked through against a real one.
2. The check catalogue, with a worked example of each type.
3. Choosing between state checks and journal checks, and why the answer is almost
   always state.
4. Writing a good `on_fail`. This is the single highest-leverage thing an author
   does, and it is the difference between a level that teaches and a level that
   frustrates.
5. Writing the hint ladder: nudge, then concept, then near-solution, then reveal.
6. Deterministic setup, and why `touch -d` matters more than it looks like it
   should.
7. Generating realistic assets that are internally consistent with the answer.
8. The golden test, and what to do when it fails.
9. Mutation testing your own level: deliberately try to pass it the wrong way.
10. Publishing a pack.

## The rules that will not change

- **Every check needs a specific, actionable `on_fail`.** Never "incorrect", never
  "try again". Write it in the voice of a helpful senior colleague who can see the
  learner's screen.
- **Verify state, not syntax**, except where the syntax genuinely is the lesson.
  `tee`, `find -exec` and `mkdir -p` are the legitimate cases; there are not many
  others.
- **Setup is deterministic and offline.** No network calls, ever. Explicit
  `touch -d` for any modification time the level depends on. Seeded generators.
- **At least two hints.** One hint is a cliff.
- **`setup.root` under `/home/learner/`.** This is what keeps reset safe.
- **Never loosen a check to make your solution pass.** If the `solution` block does
  not satisfy the check, the level is wrong, not the check. Quietly relaxing checks
  until they go green is how a level ends up accepting wrong answers.

## Design principles worth internalising

From [CURRICULUM.md](CURRICULUM.md), which is the best available example of these
being applied:

- Every level teaches one new thing and reuses two old ones.
- Make the painful way visible before the good way. Sixty files before globs. A
  four thousand line `cat` before `head`.
- Bosses have no numbered steps. Give them a broken system and "make it work",
  with several valid fixes that all pass.
- Beginner levels must be winnable in under sixty seconds. Confidence first,
  difficulty later.

See [CONTRIBUTING.md](../CONTRIBUTING.md) for the pull request process.
