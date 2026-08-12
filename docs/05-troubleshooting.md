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
