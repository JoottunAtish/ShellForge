# Troubleshooting

> **Status: outline.** Written properly on Day 7 against a build that actually
> installs. The headings below are the contract: every `doctor` probe emits a doc
> anchor, and CI fails the build if the anchor has no heading here.

Every entry follows the same shape, because someone reading this page is already
frustrated and needs the answer, not an essay.

```
## <anchor-id>
**You'll see:** the exact text doctor prints

**What it means:** two sentences, no jargon

**Fix:**
1. numbered steps
2. with the exact command

**Still stuck?** what to search for, or where to open an issue
```

---

## virtualization-disabled

**You'll see:** `doctor` reports "CPU virtualization: not available"

**What it means:** Your processor can run virtual machines, but the feature is
switched off in your PC's firmware settings. Windows needs it to run Linux.

**Fix:**

1. Restart your computer and press the setup key during boot. It is usually F2,
   F10, or Delete, and the boot screen normally tells you which.
2. Look for a setting called "Intel VT-x", "AMD-V", "SVM Mode", or "Virtualization
   Technology". Every manufacturer names it differently.
3. Enable it, save, and exit.
4. Run `shellforge doctor` again.

**Still stuck?** Search for your exact laptop model followed by "enable
virtualization BIOS".

---

## os-too-old

**You'll see:** `doctor` reports your Windows build is below 19041.

**What it means:** WSL2 needs Windows 10 version 2004 or newer, or any Windows 11.

**Fix:** Press Win+R, type `winver`, and press Enter to see your build number. If
it is below 19041, run Windows Update until it is not.

---

## vm-platform-missing

**You'll see:** `doctor` reports the Virtual Machine Platform feature is off.

**What it means:** A Windows optional feature that WSL2 depends on is not enabled.

**Fix:** Run this in an Administrator PowerShell, then reboot.

```powershell
dism.exe /online /enable-feature /featurename:VirtualMachinePlatform /all /norestart
```

---

## missing-snapshot

**You'll see:** A check reports it could not tell yet, instead of pass or fail,
right after a level starts or right after `reset`.

**What it means:** Verification reads shell state, such as an environment
variable or the current directory, from files the sandbox shell writes after
every command you run. Nothing has run yet, so those files do not exist yet
either. This is not a wrong answer: there is nothing to check yet.

**Fix:** Run a command at the sandbox prompt, then run `check` again.

---

## wsl-not-installed

**You'll see:** `doctor` reports WSL is not installed.

**What it means:** WSL is the Windows feature that runs Linux. Shellforge needs a
Linux system to teach you Linux, and this is how Windows provides one.

**Fix:** Run this in an Administrator PowerShell, then reboot.

```powershell
wsl --install --no-distribution
```

The `--no-distribution` part matters: Shellforge brings its own, and this stops
Windows installing an Ubuntu you did not ask for.

---

## wsl-version-1

**You'll see:** `doctor` reports your WSL default version is 1.

**What it means:** WSL1 does not have a real Linux kernel. Process isolation,
file watching, and `/proc` all behave differently, and levels would fail in
confusing ways.

**Fix:**

```powershell
wsl --set-default-version 2
```

---

## wsl-kernel-outdated

**You'll see:** `doctor` reports the WSL kernel is out of date.

**Fix:**

```powershell
wsl --update
```

---

## wsl-import-blocked

**You'll see:** `init` fails partway through importing, or hangs for several
minutes and then errors.

**What it means:** Antivirus software frequently interferes with `wsl --import` of
a large file. This is the most common cause of a failed first run on Windows.

**Fix:** Add an exclusion for the Shellforge install directory in your antivirus
settings, then run `shellforge init` again. `shellforge doctor` prints the exact
path.

---

## wsl-name-collision

**You'll see:** `doctor` or `init` reports a distribution named
`shellforge-sandbox` already exists.

**What it means:** A previous install left one behind, or an install was
interrupted.

**Fix:** `shellforge sandbox rebuild` replaces it. To remove it by hand:

```powershell
wsl --unregister shellforge-sandbox
```

---

## docker-not-found

**You'll see:** `doctor` reports Docker is not installed.

**What it means:** On Linux, Shellforge uses a container to hold the sandbox.

**Fix:** Install Docker or Podman with your distribution's package manager. See
[Install on Linux](02-install-linux.md).

---

## docker-daemon-down

**You'll see:** `doctor` reports Docker is installed but not running.

**Fix on Linux:**

```bash
sudo systemctl start docker
```

**Fix on Windows or macOS:** Start Docker Desktop and wait for it to report
running.

---

## docker-permission-denied

**You'll see:** "permission denied while trying to connect to the Docker daemon
socket".

**What it means:** Your user is not in the `docker` group.

**Fix:**

```bash
sudo usermod -aG docker "$USER"
```

Then log out and back in. The group change does not apply to shells that were
already open.

---

## disk-space-low

**You'll see:** `doctor` reports insufficient free disk space.

**What it means:** The sandbox needs roughly 2 GB, and Shellforge asks for 5 GB
free so that the import has room to work.

**Fix:** Free up space on the drive `doctor` names, then run `shellforge init`
again.

---

## terminal-no-vt

**You'll see:** Garbled output full of characters like `[0m` and `[1;32m`.

**What it means:** Your terminal does not understand colour codes. The old Windows
console does not.

**Fix:** Install Windows Terminal from the Microsoft Store and run Shellforge in
it. If you cannot, run `shellforge --ascii` or set `NO_COLOR=1`.

---

## terminal-not-windows-terminal

**You'll see:** A warning, not an error, that you are not in Windows Terminal.

**What it means:** Shellforge will probably work, but full-screen programs like
`vim` can garble on resize in the old console.

**Fix:** Install Windows Terminal from the Microsoft Store.

---

## terminal-too-small

**You'll see:** A warning that your terminal window is narrower than 80 columns.

**Fix:** Make the window wider. Briefings and the objective checklist assume 80.

---

## windows-needs-wsl

**You'll see:** "Could not open an interactive sandbox shell on Windows", when
you run `shellforge run` from PowerShell or the Windows command prompt.

**What it means:** The Docker backend gives you a real bash prompt by allocating
a pseudo terminal on the host, and the library that does that has no Windows
implementation yet. Windows gets its own sandbox backend on Day 3 of the build
plan. Until then the game runs from inside WSL, which is a real Linux host.

**Fix:**

1. Open your WSL distribution.
2. Change to the repository directory.
3. Build and run there:

```bash
go build -o bin/shellforge ./cmd/shellforge
./bin/shellforge run demo
```

Docker Desktop's WSL integration shares one daemon between Windows and WSL, so
the sandbox image is not rebuilt and nothing is downloaded twice.

**Still stuck?** Check that `docker version` works inside WSL. If it does not,
turn on WSL integration for your distribution in Docker Desktop, under Settings,
Resources, WSL integration.

---

## sandbox-missing

**You'll see:** `doctor` reports the sandbox is not provisioned.

**Fix:**

```
shellforge init
```

---

## sandbox-unhealthy

**You'll see:** `doctor` reports the sandbox exists but does not respond.

**What it means:** This sometimes happens after a Windows update, or if the
container was removed by hand.

**Fix:**

```
shellforge sandbox rebuild
```

---

## rootfs-checksum-mismatch

**You'll see:** `init` refuses to continue, reporting a checksum mismatch.

**What it means:** The downloaded Linux system does not match what we published.
Usually this is a truncated download. Shellforge refuses to import it rather than
verifying after the fact, which would be too late.

**Fix:** Run `shellforge init` again to re-download. If it happens twice, please
open an issue with the output of `shellforge doctor --json`.

---

## command-not-found

**You'll see:** `shellforge: command not found`, or on Windows, "The term
'shellforge' is not recognized".

**What it means:** The program installed, but the folder it went into is not on
your PATH. PATH is the list of folders your shell searches for programs.

**Fix:** Close and reopen your terminal first, since PATH changes do not apply to
already-open windows. If that does not work, see the install guide for your
platform.

---

## level-state-corrupted

**You'll see:** Checks failing on a level you have not started, or setup errors.

**Fix:**

```
shellforge reset --hard
```

If that does not help, or if `reset` is not available yet, remove the sandbox and
let the next run build a clean one:

```
docker rm -f shellforge-sandbox
```

---

## progress-db-unwritable

**You'll see:** an error while Shellforge tries to open the file where it
records your progress, telling you the directory is not writable.

**What it means:** Shellforge keeps a small file on your own computer that
remembers which levels you have finished. That file lives in a folder your
user account normally owns, and this error means Shellforge could not create
or write to that folder, most often because of a permissions problem or a
full disk.

**Fix:**

Check that you can create files in the folder the error names, then run:

```
shellforge doctor
```

If the folder belongs to a different user, or your disk is full, fix that
first. Nothing about your progress is lost by this error: no file is deleted
by it.

---

## progress-db-corrupt

**You'll see:** an error while Shellforge tries to read the file where it
records your progress, telling you the file is corrupt.

**What it means:** the small file that remembers which levels you have
finished is unreadable, most often because it was cut off mid-write by a
crash or a power loss. Shellforge never repairs a file automatically and
never deletes it either.

**Fix:**

Rename the file the error names to `progress.db.broken`, then run:

```
shellforge doctor
```

Shellforge will start a new progress file. The levels you have finished will
be forgotten, and nothing else is lost. If you want help recovering the old
one, keep `progress.db.broken` and mention it in a bug report.

---

## progress-db-in-use

**You'll see:** an error while Shellforge tries to open the file where it
records your progress, telling you the file is in use.

**What it means:** another copy of Shellforge, most often one you already
have open in a different window, has this file open right now. SQLite only
lets one process finish a certain quick internal setup step on the file at a
time, and this one lost that race. Nothing is wrong with the file itself,
and Shellforge does not touch it when this happens.

**Fix:**

Close the other copy of Shellforge, or the other window running it, then run:

```
shellforge doctor
```

If nothing else is open, run `shellforge doctor` again anyway: an antivirus
scanner or a backup tool can briefly hold the same kind of lock. If it keeps
happening, check that only one antivirus or backup process watches your
Shellforge data folder.

---

## progress-db-too-new

**You'll see:** an error while Shellforge tries to open the file where it
records your progress, telling you the file's format is newer than this copy
of Shellforge understands.

**What it means:** the small file that remembers which levels you have
finished was last written by a newer version of Shellforge than the one you
are running now. Opening it with an older version could scramble it, so
Shellforge refuses instead.

**Fix:**

Update Shellforge to the latest version and start it again. If you are going
back to an older version of Shellforge on purpose, rename the file the error
names first; Shellforge will start a new progress file rather than touch the
newer one.

---

## progress-db-migration-failed

**You'll see:** an error while Shellforge tries to update the layout of the
file where it records your progress.

**What it means:** the small file that remembers which levels you have
finished needs its internal layout updated for this version of Shellforge,
and that update did not complete. This is our bug, not something you did
wrong.

**Fix:**

Run:

```
shellforge doctor
```

and include its output if you report this. To carry on playing right away,
rename the file the error names to `progress.db.broken`; Shellforge will
start a new progress file, and the levels you have finished will be
forgotten.

---

## unsafe-level-root

**You'll see:** "refusing to use ... as a level root", when starting or resetting
a level.

**What it means:** Every level declares a `root` in its YAML, and that folder is
what the game deletes when it sets the level up or tears it down. Shellforge only
ever deletes inside your sandbox home, `/home/learner/`, and it refuses anything
else rather than guessing. Two things cause this: a level whose `root` is written
wrongly, and a shortcut (a symlink) somewhere on that path pointing at a different
folder.

**Fix:** If you are playing a level from a pack you wrote or edited, check its
`setup.root`. It must be a folder strictly inside `/home/learner/`, and
`/home/learner` on its own is not allowed. Run:

```
shellforge author validate packs/core-linux-basics
```

If you created a symlink inside your sandbox home during a level, remove it and
start the level again. If you are stuck, remove the sandbox and let the next run
build a clean one:

```
docker rm -f shellforge-sandbox
```

---

## level-not-found

**You'll see:** "play level" and the name you typed, followed by a list of the
levels that do exist.

**What it means:** There is no level with that id in the content pack. Almost
always a typo, or a level from the curriculum that is not written yet. The pack
that ships with Shellforge fills in over time, and `pack.yaml` lists ids ahead of
the files that implement them, so a name you read in the campaign plan may not be
playable yet.

**Fix:** The error lists every level in the pack, in campaign order. Pick one
from that list. To see the list on its own, without triggering the error:

```
shellforge author validate packs/core-linux-basics
```

If you meant to start at the beginning:

```
shellforge run nav-01
```

---

## level-pack-invalid

**You'll see:** "read the level definition", "read the level assets", or
"generate the level assets", followed by the name of a file or a field.

**What it means:** The level pack declares something Shellforge cannot build. The
usual causes are a `source:` naming a file that is not in the pack, a `mode:`
written without quotes (it must be `"0644"`, not `0644`), an `owner:` that is not
a user or a user and group pair, a file that sets more than one of `source`,
`content`, and `generate`, and a `generate.kind` that does not exist. In v0.1 the
only generator kind is `loglines`.

**Fix:** The error names the file and the field. Correct it, then check the whole
pack before playing again:

```
shellforge author validate packs/core-linux-basics
```

---

## setup-script-failed

**You'll see:** "run the level setup script" or "run the level teardown script",
with an exit code or a timeout.

**What it means:** A level can run a short script to finish setting its world up,
or to clean up after itself. That script failed, so Shellforge removed everything
it had built rather than leaving you a half-made level. Nothing on your computer
was touched: the script runs inside the sandbox.

**Fix:** This is a bug in the level, not in what you typed. If the level is one of
yours, look at `setup.script` or `teardown.script` in its YAML and run the level's
own test:

```
shellforge author test <level-id>
```

A timeout means the script ran longer than the level allows. The limit is
`setup.timeout_seconds`, which defaults to 30. If the level is not yours, please
report it with `shellforge bug-report`.

---

## permission-denied-in-sandbox

**You'll see:** "Permission denied" while working inside a level.

**What it means:** This is often the level working as intended. Act V is about
permissions, and being denied is the puzzle.

**Fix:** Look at the file with `ls -l`. If you are genuinely stuck, `hint` costs
a small amount of XP and is there for exactly this.

---

## Common first problems

To be written on Day 7, once there is real evidence of what people actually hit.
The predicted list is above. The real list will differ, and the real one wins.
