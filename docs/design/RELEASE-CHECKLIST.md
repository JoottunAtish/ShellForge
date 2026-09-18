# Release checklist

How a Shellforge release is cut. Written down because the tag push is otherwise
an act of memory, and the one thing memory reliably loses is the ordering
constraint in section 1.

Everything here is done by a person against merged `main`.
`.github/workflows/release.yml` does the building and publishing, and it needs no
input beyond the tag.

---

## 1. The ordering constraint, and why it is this way round

**The tag is pushed immediately after the documentation merge, not before it.**

The install sections have to be written for a world in which the release exists,
because that is the world every reader of merged `main` is in five minutes later.
So between the merge and the tag there is a window where `main` describes a
release that does not exist yet. Keep it to minutes.

Inverting the two does not help. Tagging first publishes a release whose README
still says there is no release.

If the window cannot be minutes, say so in the pull request rather than shipping
prose you know to be briefly wrong without anyone else knowing.

---

## 2. Before the tag

Work top to bottom. Each item is checkable and none of them takes long.

- [ ] `make ci` is clean on `main`
- [ ] CI is green on `main` for the merge commit you are about to tag
- [ ] `CHANGELOG.md` has an entry for this version, and its "Known issues"
      section matches the open issues that actually affect a new user
- [ ] `docs/assets/demo.gif` exists, is under 5 MB, and `README.md` references
      it above the fold. Re-record with `make demo` if the interface has changed
      since the last release.
- [ ] `shellforge version` on a local build prints the version you are about to
      tag, or `dev`. It must not print a stale tag.
- [ ] No document promises a verb that does not exist.
      `go test ./cmd/shellforge/ -run TestEveryCommandInTheDocsResolves` is the
      gate and it runs in CI, so this is a re-check rather than a discovery.
- [ ] `PROGRESS.md` reflects what is shipping

---

## 3. Push the tag

```bash
git switch main && git pull
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```

The tag push triggers `release.yml`. Nothing else is needed: the workflow builds,
verifies, and publishes on its own.

---

## 4. Confirm the release

`release.yml` attaches **six assets**. Check all six are on the release page,
because a partial upload looks like a successful one from the workflow log:

| Asset | What it is |
|---|---|
| `shellforge_v0.1.0_linux_amd64.tar.gz` | The binary, x86-64 Linux |
| `shellforge_v0.1.0_linux_arm64.tar.gz` | The binary, arm64 Linux |
| `shellforge_v0.1.0_windows_amd64.zip` | The binary, x86-64 Windows |
| `rootfs.tar.gz` | The WSL rootfs |
| `rootfs.tar.gz.sha256` | Its digest |
| `SHA256SUMS` | One file listing the other five, which both installers verify against |

There is no darwin target, deliberately: nothing in this repository has ever run
on darwin and `internal/doctor` has no darwin probes, so an untested binary would
generate install reports nobody here can reproduce.

- [ ] All six assets are attached
- [ ] The workflow's own `sha256sum -c SHA256SUMS` step passed
- [ ] The workflow's `shellforge version` step printed the tag

---

## 5. The acceptance test

**This is the release. The rest is preparation.**

Two clean machines, no clone of this repository, following only the written
documentation. This is task 6.8 of `docs/design/SEVEN-DAY-PLAN.md` and it is the
only thing that can tell you whether the install path works, because everyone who
has ever run this code has run it from inside the repository, where every
fallback resolves.

**Linux:**

- [ ] `curl ... | sh` from `docs/02-install-linux.md`, exactly as written
- [ ] `shellforge doctor` reports no `fail`
- [ ] `shellforge init` provisions a sandbox
- [ ] `shellforge play` reaches a briefing, and a level can be passed

**Windows 11:**

- [ ] `install.ps1` from `docs/01-install-windows.md`, exactly as written
- [ ] `shellforge doctor` reports no `fail`
- [ ] `shellforge init` imports the distribution
- [ ] `shellforge play` reaches a briefing, and a level can be passed

**Uninstall, on both:**

- [ ] `shellforge sandbox destroy` removes the sandbox
- [ ] Every directory in `docs/06-uninstall.md` is gone after following it
- [ ] On Windows, `wsl -l -v` no longer lists `shellforge-sandbox`, and the
      install directory is gone in File Explorer. **Check this with your eyes.**
      An orphaned 2 GB `.vhdx` we claimed to have removed is a broken promise
      even though it destroyed nothing.

Where something fails, file it rather than fixing it in place. A release with a
known, filed, documented defect is honest. A release quietly patched after the
tag is not reproducible.

**Run this before the tag, not after it.** Every box above is now expected to
pass as written. #172 and #138 both landed: a release install provisions a
sandbox with no clone, and `shellforge play` opens a level from a native
Windows console. Nothing in this list is known to fail.

Nothing in it has been confirmed on real Windows hardware either. No runner this
project can reach has WSL2 or Linux containers, so the Windows column is proven
by construction and by a Linux CI leg exercising the same bytes, which is not
the same as proven. Cut a pre-release (a tag with a hyphen in it, such as
`v0.1.0-rc.1`, which release.yml publishes as a pre-release so it never becomes
`/releases/latest`), install from it on a clean Windows 11 machine, and work
this list. Three things are worth naming because only a human can see them:

- `vim` renders correctly inside the shell.
- Resizing the window mid-session does not corrupt the display.
- After `exit`, the host console still behaves, arrow keys included.

Where something fails, file it rather than fixing it in place.

---

## 6. Afterwards

- [ ] Play the campaign start to finish in one sitting. This is a Day 5 exit
      criterion that has been carried since, and it is the only thing that finds
      what 25 levels feel like in a row rather than one at a time.
- [ ] Write the announcement post. It is a post for a forum, not a repository
      artifact, which is why it is not in this repository. `CHANGELOG.md` is the
      part that belongs here.
- [ ] Open the next milestone's issues, starting with anything the acceptance
      test found.
