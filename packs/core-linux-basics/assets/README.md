# Assets

Files materialized into the sandbox during level setup, referenced from a level's
`setup.files[].source`.

## Rules

- **Assets must be internally consistent with their level.** If the level says
  the answer is 147, the log file here must actually contain 147 matching lines.
  Verify by running the level's `solution`, not by adjusting the check.
- **Realistic and in-world.** These are Meridian Logistics files: shipping
  manifests, delivery logs, access logs, config for a service called `atlas`.
  The fiction costs nothing and makes the exercise feel like work rather than
  homework.
- **Deterministic.** A generated asset uses a fixed seed and is committed, or is
  declared with `setup.files[].generate` and produced at setup time from a seed.
  Never both.
- **LF line endings.** `.gitattributes` enforces it and the setup runner strips
  carriage returns again on materialization.
- **No secrets, even fake-looking ones.** Level find-02 deliberately plants a
  string that looks like a leaked API key. Keep it obviously synthetic so no
  scanner ever has a reason to flag this repository.

## What is here

| File | Level | Role |
|---|---|---|
| `app-1.log` | pipe-05 | Rotated log from the `api` service, 420 lines |
| `app-2.log` | pipe-05 | Rotated log from the `worker` service, 380 lines |
| `billing.log` | pipe-05 | Rotated log from the `billing` service, 260 lines |

Together they contain exactly 147 lines matching a case-insensitive search for
`error`, and exactly three distinct error codes: `E401`, `E500`, `E503`. Those
are pipe-05's two answers.

**Editing any of these three files breaks pipe-05, on purpose.**
`internal/content/pipe05_assets_test.go` counts the answers out of the committed
bytes the way the level's own solution counts them, and fails if they no longer
match the checks. It also fails if a log line matches `error` case-insensitively
without being an actual `ERROR` record, because the solution's `grep -i` would
count such a line and the level's answer would quietly become wrong.

If you need to change them, change them and let the test tell you the new
answer, then update the level's checks, its briefing and its hints together. Do
not adjust a check to match an edited asset without rereading the level.

They were produced by the deterministic generator in
`internal/sandbox/demo_level.go`, which has its own consistency tests, rather
than written by hand. That is why the numbers are trustworthy.
