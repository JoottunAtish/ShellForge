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
| `check-nodes.sh` | pipe-02 | Health sweep writing eight healthy lines to stdout and four failures to stderr, exiting 2 |
| `access.log` | pipe-03 | Tracking API log, 160 requests from 12 distinct addresses |
| `contacts.csv` | pipe-04 | Depot contacts, 15 records, email in column 3, capitalisation deliberately inconsistent |
| `support-tickets.txt` | find-01 | 26 tickets, 7 mentioning a refund in mixed case, 12 resolved |
| `ledger.csv` | perm-02 | Shipment ledger the learner regroups to `logistics` |
| `kofi-handover.txt` | perm-02 | Declared `owner: "root:root"`, so the learner can read it and cannot write it |
| `atlas-indexer.sh` | proc-01 | The runaway process. Sleeps rather than spinning, and writes its own pid to `.indexer.pid` |
| `heartbeat.sh` | proc-01 | Started by the learner in the background. Writes nothing, on purpose |
| `atlas-run.sh` | perm-03 | The nightly job. Rewrites its report with `>` so two runs leave the same bytes |
| `atlas.conf` | perm-03 | What the nightly job reads, staged at mode `0000` |
| `atlas-status` | env-01 | One line of status, reachable only once `~/toolbox/bin` is on `PATH` |
| `atlas-service` | boss-final | The order service. `--check` validates the config and writes nothing |
| `atlas-health` | boss-final | Reports the first fault it finds and stops. Read only |
| `boss-cleanup.sh` | boss-final | The cleanup job the broken cron entry never ran |
| `boss-atlas.conf` | boss-final | Staged with the `prot=8080` typo |
| `boss-error.log` | boss-final | Where the typo is discoverable, three nights of it |
| `boss-crontab` | boss-final | Staged with four time fields where cron needs five |

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

## Assets whose bytes decide an answer

Four levels assert an exact answer computed from committed bytes, and each one
is guarded by a test that recomputes the answer the way the level's own solution
computes it:

| Asset | Level | Guarded by |
|---|---|---|
| the three pipe-05 logs | pipe-05 | `internal/content/pipe05_assets_test.go` |
| `access.log` | pipe-03 | `internal/content/answers_test.go` |
| `contacts.csv` | pipe-04 | `internal/content/answers_test.go` |
| `support-tickets.txt` | find-01 | `internal/content/answers_test.go` |

Edit one of those and the test tells you the new answer. Update the level's
checks, briefing and hints together, and bump the level's `version` so recorded
best scores are invalidated. Do not adjust a check to match an edited asset
without rereading the level.

`answers_test.go` also asserts two things that are about ambiguity rather than
drift: that no two of pipe-03's addresses tie for third place, and that
`support-tickets.txt` spells `resolved` in one case only. Either would give the
level two defensible answers and make it reject one of them.

Every asset in this directory is checked for carriage returns by
`TestEveryAssetIsLFOnly`, not only the pipe-05 logs.

They were produced by the deterministic generator in
`internal/sandbox/demo_level.go` rather than written by hand. That generator is
slated for deletion under #96; the durable guarantee is
`internal/content/pipe05_assets_test.go`, which recomputes the answers from the
committed bytes on every run. That is why the numbers are trustworthy.
