# What this changes

<!-- One or two sentences. What and why, not how. -->

## Type

- [ ] Bug fix
- [ ] New level or content change
- [ ] Engine feature
- [ ] Documentation
- [ ] Build, CI, or tooling

## Checklist

<!-- The full list is in CLAUDE.md under "Definition of Done". -->

- [ ] `gofmt -s -w .` applied
- [ ] `go vet ./...` clean
- [ ] `go test ./...` and `go test -race ./...` pass
- [ ] `./scripts/check-punctuation.sh` passes (no em dash, no en dash, no smart quotes)
- [ ] Layer test still passes (`go test ./internal/archtest/...`)
- [ ] No new dependency, or I asked first and the answer was yes
- [ ] Any new user-facing error has a remediation and a doc anchor that exists
- [ ] Any new subprocess call uses argv form and validates interpolated identifiers

## If this touches the level format

- [ ] `docs/LEVEL-FORMAT.md` updated in this same commit
- [ ] If a check type was added: registered, tested, and documented in the catalogue

## If this adds or changes a level

- [ ] `shellforge author validate` passes
- [ ] `shellforge author test <id>` passes: checks fail before the solution, pass after, teardown leaves nothing
- [ ] Every check has a specific, actionable `on_fail`
- [ ] Setup is deterministic and makes no network calls
- [ ] At least two hints, escalating
- [ ] I tried to pass it the wrong way and it rejected me
- [ ] I did not loosen a check to make the solution pass

## Platforms exercised

<!-- "Linux only" is a fine answer. We just need to know. -->

- [ ] Linux
- [ ] Windows
- [ ] macOS (community supported)

## Notes for the reviewer

<!-- Anything you are unsure about, or deliberately left out. -->
