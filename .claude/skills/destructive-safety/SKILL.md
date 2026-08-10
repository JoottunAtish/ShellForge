---
name: destructive-safety
description: The absolute rules protecting the learner's own machine from Shellforge. Load before writing or reviewing ANY code that deletes, unregisters, unmounts, overwrites, chmods, or chowns anything; anything that resolves a path used in a destructive operation; anything touching wsl --unregister, .wslconfig, docker rm, reset, teardown, uninstall, sandbox destroy, or doctor --fix; and before writing a level setup or teardown script. Also load for any review of a change that could reach outside the sandbox.
---

# Destructive safety

## The promise

The README tells a nineteen year old who has never opened a terminal that she
cannot break her laptop with this. Everything in this file exists to make that
sentence literally true.

**Shellforge deletes things constantly.** Every level reset is an `rm -rf`. Every
uninstall unregisters a WSL distribution and deletes a 2 GB disk image. That is the
product working correctly, and it is also one path resolution bug away from
deleting a student's coursework.

Treat every destructive operation as a loaded weapon that is currently pointed at
a beginner's home directory.

## The five things that would be catastrophic

Ranked by how bad, not by how likely. All five are reachable from code that looks
reasonable.

### 1. Unregistering the learner's own WSL distribution

`wsl --unregister <name>` is **instant and irreversible**. There is no
confirmation, no recycle bin, and no undo. If `<name>` resolves to `Ubuntu` or
`Debian` instead of `shellforge-sandbox`, you have permanently destroyed
everything that person had in Linux.

This is the single worst thing this codebase can do. The developer machine for
this project has a `Debian` distribution sitting right next to the sandbox.

**Mandatory guards, all of them, every time:**

```go
// The name is a constant. It is never a parameter, never from config, never
// from a level, and never from parsing `wsl -l -v` output.
const sandboxDistro = "shellforge-sandbox"

func destroy(ctx context.Context, name string) error {
    if name != sandboxDistro {
        return fmt.Errorf("refusing to unregister %q: shellforge only ever removes %q", name, sandboxDistro)
    }
    // ... then verify it exists and looks like ours before acting
}
```

- The distribution name is a **compile-time constant**, never a variable that
  could be empty. `wsl --unregister ""` and `wsl --unregister $UNSET_VAR` are the
  classic ways this goes wrong.
- **Verify before destroying:** confirm the distribution exists, and confirm it
  contains a Shellforge marker file such as `/opt/shellforge/.sandbox-id`. If the
  marker is missing, refuse and tell the user, because a name collision means
  something else is using that name.
- **Enumerate and diff.** Before and after, `wsl -l -q` should differ by exactly
  one entry. If more than one distribution disappeared, that is a bug worth
  crashing over.
- Remember `wsl.exe` outputs **UTF-16LE**. A decoding bug produces garbled names,
  and a garbled comparison against a constant fails safe, but a garbled name
  passed *into* a command does not.

### 2. Reaching the learner's real files through /mnt/c

If WSL automount is enabled, `/mnt/c` exists inside the sandbox, and the level that
teaches `rm -rf` now has a path to the student's Documents folder.

- `[automount] enabled = false` in `/etc/wsl.conf` is **not a preference**. It is
  the mechanism behind the safety promise on Windows.
- Verify it took effect after import, do not assume. `wsl --terminate` is required
  before the config applies.
- There must be a test that asserts `/mnt/c` does not exist inside a provisioned
  sandbox.
- The same applies to `[interop] enabled = false`. Interop lets bash launch
  Windows executables, which is a direct route to the host.

### 3. Editing .wslconfig, which is global to every distribution

`%USERPROFILE%\.wslconfig` configures **all** of the user's WSL distributions, not
just ours. Silently writing memory or processor limits there changes the behaviour
of the learner's own development environment.

- **Never write it without asking.** Print the proposed change and require explicit
  confirmation.
- If you do write it, back up the original first and say where the backup is.
- Prefer per-distribution settings in `/etc/wsl.conf` inside our own sandbox, which
  affects nothing else.

### 4. A destructive path escaping setup.root

`reset` is `rm -rf` on a path built from level YAML. Level YAML may eventually come
from a third party.

**Every destructive path goes through one validated helper. No exceptions, no
inline `os.RemoveAll`.**

The helper must reject, in this order, before deleting anything:

- an empty path, or one that cleans to `.`, `/`, or a drive root
- any path that is not absolute after cleaning
- any path not under `/home/learner/`
- any path containing a `..` segment before cleaning
- any path whose resolved symlink target escapes the prefix, checked **after**
  resolving, because a symlink placed by the learner during the level is a real
  scenario
- the level root itself being a symlink

```go
// Refuse first, delete second. The check is cheap and the mistake is permanent.
func removeLevelRoot(root string) error {
    clean := filepath.Clean(root)
    if clean == "" || clean == "." || clean == "/" || !strings.HasPrefix(clean, "/home/learner/") {
        return fmt.Errorf("refusing to remove %q: level roots must live under /home/learner/", root)
    }
    // resolve symlinks, re-check the prefix, then remove
}
```

- **The deletion runs inside the sandbox, never on the host.** A host-side
  `os.RemoveAll` on a path that came from a level is never correct.
- `setup.root` under `/home/learner/` is a validator-enforced authoring invariant.
  Enforce it again at execution time anyway. Validation and execution are different
  moments and packs can be loaded by paths the validator did not see.

### 5. Deleting the install directory during uninstall

Uninstall deletes a directory containing a 2 GB `.vhdx`. If that path resolves to
the wrong place, it takes the user's data with it.

- Derive it only from `internal/platform`, never from user input or a stored value
  that could be stale or corrupted.
- Refuse if the directory does not contain the expected sandbox marker.
- Refuse to delete a home directory, a drive root, or anything with an unexpected
  number of entries.
- Verify afterwards and **tell the user the exact path to check in Explorer**. An
  orphaned 2 GB file that we claimed to remove is a broken promise even though it
  destroyed nothing.

## Hard rules

These are not guidance. A change that violates one does not merge.

1. **No `os.RemoveAll` outside the validated helper.** Grep for it in review.
2. **No destructive host-side operation on a path derived from content.** Ever.
3. **Destructive identifiers are constants, not parameters.** Distribution name,
   container name, image name.
4. **Refuse rather than sanitize.** A path that looks wrong is a bug to report, not
   a string to clean up and proceed with.
5. **Never `--privileged`, never a host bind mount other than read-only
   `/opt/shellforge`, never `--network host`.**
6. **`doctor --fix` performs only reversible, non-privileged actions.** Anything
   needing administrator rights is printed for the user to run themselves. We do
   not silently elevate on someone's machine.
7. **No operation deletes something the user created outside the level root.** If a
   learner saves a file in their sandbox home outside `~/quest`, reset must not
   take it.
8. **Fail closed.** If a safety check cannot determine whether an operation is
   safe, it is unsafe. Error out with a clear message.

## Before you write a destructive operation, answer these

- What is the worst path this could resolve to if every input is wrong or empty?
- Is the identifier a constant, or could it be empty or attacker-influenced?
- Does this run on the host or inside the sandbox? Should it?
- Is there a marker file confirming this is ours before we destroy it?
- What does the user see if it refuses? Does that message tell them what to do?
- Is there a test that asserts the refusal, not just the success path?

## Required tests

A destructive code path is not done until these exist:

- **Refusal tests are mandatory and come first.** Empty string, `/`, `~`,
  `/home/learner` itself, `../../etc`, a symlink escaping the root, a path on the
  host filesystem, a distribution name that is not ours.
- **A marker check test:** destroying something without the Shellforge marker must
  fail.
- **An isolation test:** `rm -rf /` inside a provisioned sandbox leaves the host
  untouched, and `reset` recovers.
- **A `/mnt/c` absence test** on Windows.
- **An uninstall completeness test:** nothing orphaned, verified by listing the
  parent directory afterwards.

Success-path tests for deletion are the easy half. The refusal tests are the ones
that protect the student.

## Reviewing someone else's destructive change

Ask for the blast radius in writing. If the author cannot state what the worst
resolved path is, the change is not ready, regardless of how clean the code looks.

Red flags worth blocking on:

- `os.RemoveAll` with a variable that is not obviously validated three lines above
- a distribution or container name that arrived as a function parameter
- string concatenation building a path instead of `filepath.Join`
- a prefix check performed before `filepath.Clean` rather than after
- symlinks resolved after the safety check instead of before
- a new `sudo`, `runas`, or elevation prompt
- a `defer` cleanup that could run on a partially initialised struct with zero
  valued path fields
