# Install on Windows

**Who this is for:** you are on Windows, you have never opened a terminal, and you
would like to keep your laptop in one piece. That is exactly the right starting
point.

**Time:** about 20 minutes, most of it waiting. **Disk:** about 5 GB free, of
which Shellforge keeps about 2 GB.

Seven steps. Do them in order: each one checks that the last one worked, so if
something is wrong you find out on the step that caused it rather than three
steps later.

---

## 1. What you are about to install, and why

Shellforge teaches you Linux. To do that it needs a Linux system to teach you in,
and Windows can run one for you. That feature is called **WSL**, which stands for
Windows Subsystem for Linux. It is made by Microsoft and it is built into Windows.

A **distribution**, or distro, is one installed Linux system. You can have
several side by side and they do not see each other. We will set up our own
private one, called `shellforge-sandbox`, that does not touch anything else on
your PC. It cannot see your files, and deleting everything inside it is the point
rather than a disaster.

A **terminal** is a window you type commands into. A **shell** is the program
inside it that reads what you type and runs it. You will be typing into a real
Linux shell called bash, which is the thing this game is teaching you.

## 2. Before you start

Three things to check. None of them takes more than a minute.

**Your Windows version.** You need Windows 10 build 19041 or newer, or any
Windows 11. To check: press Win+R, type `winver`, press Enter. A small window
opens and names your version and build number.

If your build is older than 19041, Windows Update is the fix, and
[os-too-old](05-troubleshooting.md#os-too-old) has the detail.

**Disk space.** About 5 GB free on your C: drive. Shellforge settles at roughly
2 GB; the rest is working room while the sandbox is built.

**Administrator access.** You need to be able to install software on this
machine. Step 4 is the only step that needs it, and Shellforge itself never asks
for it.

## 3. Step 1: install Windows Terminal

Do this first, before anything else.

The console window Windows has shipped since the 1980s cannot draw colours
properly. If you use it, Shellforge's output arrives full of stray characters
like `[0m` and `[1;32m`, everything looks broken, and you will reasonably assume
Shellforge is at fault.

Windows 11 has Windows Terminal already, so you can skip this. On Windows 10,
open the Microsoft Store, search for **Windows Terminal**, and install it. It is
free and published by Microsoft.

From here on, "open a terminal" means: press the Start button, type
`Windows Terminal`, and press Enter.

## 4. Step 2: turn on virtualization

Virtualization lets your processor run another operating system inside itself.
WSL needs it. It is often switched off from the factory, and it is switched on in
your PC's firmware rather than in Windows.

**Check whether it is already on.** Press Ctrl+Shift+Esc to open Task Manager,
click **Performance**, click **CPU**, and look at the block of text on the right
for a line reading **Virtualization**. It says either Enabled or Disabled.

If it says Enabled, skip to step 3.

If it says Disabled, you need to turn it on in your PC's firmware settings.
[virtualization-disabled](05-troubleshooting.md#virtualization-disabled) walks
through what that means, how to get into yours, and what the setting is called,
with the caveat that every manufacturer names it something different.

## 5. Step 3: install WSL

This step needs an **Administrator** terminal, which is a terminal allowed to
change the system. To open one: press Start, type `Windows Terminal`, then
right-click the result and choose **Run as administrator**. Windows asks you to
confirm. The window that opens says "Administrator" in its title bar, and that is
how you know you have the right one.

In that window:

```powershell
wsl --install --no-distribution
```

`--no-distribution` matters. Shellforge brings its own Linux, and this flag stops
Windows from also installing an Ubuntu you did not ask for and will never use.

**Then reboot.** WSL is not finished installing until you have. Skipping the
reboot is the most common reason step 5 fails.

After rebooting, open an ordinary terminal (no Administrator needed from here on)
and check:

```powershell
wsl --version
```

It should print a WSL version, a kernel version, and a few others. If it says WSL
is not recognised, see
[wsl-not-installed](05-troubleshooting.md#wsl-not-installed).

## 6. Step 4: install Shellforge

In an ordinary terminal, not an Administrator one:

```powershell
Invoke-WebRequest -Uri https://raw.githubusercontent.com/JoottunAtish/ShellForge/main/scripts/install.ps1 -OutFile install.ps1
powershell -ExecutionPolicy Bypass -Scope Process -File install.ps1
```

The first line downloads the installer. The second runs it.

**Read that URL before you run it.** It is this repository's own
`scripts/install.ps1` and nothing else, and you can open that same address in a
browser and read the whole file first. `-ExecutionPolicy Bypass -Scope Process`
allows an unsigned script to run in this one window only: it changes nothing
about your machine or your user account, and it stops applying the moment you
close the window.

What the installer does, in full:

- Downloads the Windows release archive and the release's `SHA256SUMS` file, then
  verifies the archive's checksum **before** it touches your disk. If they do not
  match it deletes what it downloaded and stops, and nothing is installed.
- Places `shellforge.exe` in `%LOCALAPPDATA%\Programs\shellforge`, refusing to
  overwrite an existing file there unless you pass `-Force`.
- Adds that one directory to your own user `Path`, so that a new terminal can
  find `shellforge` without you editing anything by hand. Pass `-NoPathChange` to
  skip this and be told what to add yourself. It never touches the machine-wide
  `Path` and it never asks for Administrator rights.
- Runs `shellforge doctor` so you know immediately whether anything else needs
  attention.

**Close the terminal and open a new one.** A terminal reads `Path` when it
starts, so the one you installed from cannot see the new entry. This is what
produces `shellforge: The term 'shellforge' is not recognized`, and it is
[command-not-found](05-troubleshooting.md#command-not-found).

## 7. Step 5: check your machine

```
shellforge doctor
```

`doctor` runs twelve checks and prints a table: a name, a status of `ok`, `warn`
or `fail`, and a detail line. A `warn` is worth reading and does not stop you. A
`fail` does.

Every `fail` line names what to do next, and carries a code such as
`virtualization-disabled` or `wsl-version-1` that is a heading in
[Troubleshooting](05-troubleshooting.md). Search that page for the code and you
get the same fix written out at length.

You do not need everything green. You need no `fail`.

## 8. Step 6: set up the sandbox

```
shellforge init
```

This builds your private Linux distribution and imports it into WSL. It takes a
few minutes, it happens once, and it prints which backend it chose and why before
it starts.

**In v0.1.0 this step needs a clone of this repository, and that is a known
bug.** `init` looks for a Linux system image that v0.1.0's installer does not
fetch, and stops with a message about a missing rootfs. It is tracked as
[issue #172](https://github.com/JoottunAtish/ShellForge/issues/172). Until it is
fixed, you need Git, Go and Docker Desktop installed, and then:

```powershell
git clone https://github.com/JoottunAtish/ShellForge.git
cd ShellForge
.\make.ps1 rootfs
shellforge init
```

`.\make.ps1 rootfs` builds the image and writes `images\out\rootfs.tar.gz`, which
is what `init` then imports. You only need the clone for this one step: once the
distribution is imported, `shellforge play` works from any directory and you can
delete the clone.

[rootfs-not-found](05-troubleshooting.md#rootfs-not-found) has the same
instructions if you meet the error before reading this far.

## 9. Step 7: play

**Play from inside WSL, not from this PowerShell window.**

Everything up to here works natively on Windows. Opening a level does not.
`play`, `run` and `sandbox shell` allocate a pseudo terminal on the host, and
Windows consoles have no implementation of that yet, on either backend. The
commands say so up front rather than failing halfway through setting a level up,
and it is tracked as
[issue #138](https://github.com/JoottunAtish/ShellForge/issues/138).

WSL is a real Linux machine, so the game runs there normally, and Docker Desktop
shares one daemon between Windows and WSL, so nothing is built or downloaded a
second time. You need a general purpose distribution to play from, which is not
the `shellforge-sandbox` one step 6 imported:

```powershell
wsl --install -d Ubuntu
```

It asks you to choose a username and a password for that distribution. Then, in
the Ubuntu window it opens, install Go if you do not have it there
(`sudo apt update && sudo apt install -y golang-go`), and build from the clone
you already made in step 6:

```bash
cd /mnt/c/Users/you/ShellForge
go build -o bin/shellforge ./cmd/shellforge
./bin/shellforge play
```

[windows-needs-wsl](05-troubleshooting.md#windows-needs-wsl) has the same
instructions, and says what to do if `docker version` does not work inside
Ubuntu.

Your progress is a file in the home directory of whichever side you are on, so
play from the same place each time or you will start again from `nav-01`.

`play` picks your next level, says which one and why, provisions it, and prints
the briefing. Read it, then type Linux commands at the prompt. `check` tells you
how you are doing, `hint` costs you points and says so first, and `exit` leaves.
If you forget any of that, type `help`.

[Quickstart](03-quickstart.md) is the two-page version of everything you can type
once you are in.

## 10. Common first problems

In roughly the order people hit them:

| If | Go to |
|---|---|
| `shellforge` is not recognised after installing | [command-not-found](05-troubleshooting.md#command-not-found), and reopen your terminal first |
| Task Manager says virtualization is disabled | [virtualization-disabled](05-troubleshooting.md#virtualization-disabled) |
| `wsl --install` says WSL is not recognised | [wsl-not-installed](05-troubleshooting.md#wsl-not-installed) |
| You rebooted and WSL still reports version 1 | [wsl-version-1](05-troubleshooting.md#wsl-version-1) |
| Your antivirus blocked the import | [wsl-import-blocked](05-troubleshooting.md#wsl-import-blocked) |
| `init` cannot find a rootfs | [rootfs-not-found](05-troubleshooting.md#rootfs-not-found), and see step 6 above |
| The output is full of `[0m` and `[1;32m` | [terminal-no-vt](05-troubleshooting.md#terminal-no-vt), and see step 1 above |

If none of those is it, `shellforge bug-report` writes a diagnostic zip to your
own machine and uploads nothing. Attach it to an issue. The bottom of
[Troubleshooting](05-troubleshooting.md) says exactly what is in it.

## 11. Removing all of this later

See [Uninstall](06-uninstall.md). It names every file and directory, including
the roughly 2 GB `.vhdx` that is the single most complained-about leftover of
tools in this category, and it tells you how to confirm each one is gone.
