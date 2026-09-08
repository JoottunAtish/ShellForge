# Install on Windows

> **Status: outline.** This is the most important file in the repository and it is
> written properly on Day 7, with every screenshot below actually taken against a
> real build. `scripts/install.ps1` itself is real today, and step 4 below
> describes exactly what it does; everything else on this page, including
> whether the WSL steps still read this way once install.ps1 has been run on a
> clean machine, is still owed.

**Who this is for:** you are on Windows, you have never opened a terminal, and you
would like to keep your laptop in one piece. That is exactly the right starting
point.

**Time:** about 10 minutes. **Disk:** about 2 GB.

---

## 1. What you are about to install, and why

Shellforge teaches you Linux. To do that it needs a Linux system to teach you in,
and Windows can run one for you. That feature is called WSL, which stands for
Windows Subsystem for Linux. It is made by Microsoft and it is built into Windows.

We will set up our own private Linux system that does not touch anything else on
your PC. It cannot see your files, and deleting everything inside it is the point
rather than a disaster.

## 2. Before you start

- [ ] Windows 10 build 19041 or newer, or any Windows 11.
      To check: press Win+R, type `winver`, press Enter. [SCREENSHOT: winver]
- [ ] About 5 GB of free disk space.
- [ ] Administrator access, meaning you can install software on this machine.

## 3. Step 1: install Windows Terminal

Do this first. The old Windows console mangles colours and makes everything look
broken, and you will think Shellforge is at fault.

[SCREENSHOT: Windows Terminal in the Microsoft Store]

## 4. Step 2: turn on virtualization

Virtualization lets your processor run another operating system inside itself. It
is often switched off from the factory.

To check whether it is already on: press Ctrl+Shift+Esc for Task Manager, then
Performance, then CPU, and look for "Virtualization".
[SCREENSHOT: Task Manager showing Virtualization: Enabled]

If it says Disabled, see
[virtualization-disabled](05-troubleshooting.md#virtualization-disabled). That
section explains what a BIOS is, how to get into yours, and what the setting is
called, with the caveat that every manufacturer names it differently.

## 5. Step 3: install WSL

[SCREENSHOT: Administrator PowerShell]

```powershell
wsl --install --no-distribution
```

`--no-distribution` matters. Shellforge brings its own Linux, and this stops
Windows installing an Ubuntu you did not ask for and will never use.

Then reboot.

## 6. Step 4: install Shellforge

```powershell
powershell -ExecutionPolicy Bypass -Scope Process -File install.ps1
```

That command assumes you already have `install.ps1` on disk. To get it and run
it in one step, in an ordinary (not Administrator) PowerShell window:

```powershell
Invoke-WebRequest -Uri https://raw.githubusercontent.com/JoottunAtish/ShellForge/main/scripts/install.ps1 -OutFile install.ps1
powershell -ExecutionPolicy Bypass -Scope Process -File install.ps1
```

This works once v0.1.0 is tagged. Until then there is no release for it to
resolve, and it fails with `could not resolve the latest release`. Build from
source instead, with Go installed: `.\make.ps1 build`.

Read that URL before you run it: `-ExecutionPolicy Bypass -Scope Process`
affects nothing outside this one window, and it is this repository's own
`scripts/install.ps1`, which you can read at that same address first. What it
does:

- Downloads the Windows release archive and its `SHA256SUMS` file, then
  verifies the archive's checksum before touching your disk. If they do not
  match, it deletes what it downloaded and installs nothing.
- Places `shellforge.exe` in `%LOCALAPPDATA%\Programs\shellforge`, refusing to
  overwrite an existing file there unless you pass `-Force`.
- Adds that directory to your own user `Path` environment variable, so a new
  terminal window can find `shellforge` without you editing anything by hand.
  Pass `-NoPathChange` to skip this and be told what to add yourself instead.
  This never touches the machine-wide `Path`, and it never asks for
  Administrator rights.
- Runs `shellforge doctor` once it is installed, so you know immediately
  whether anything else needs attention. A `doctor` warning does not mean the
  install failed: the binary is on disk either way.

## 7. Step 5: check your machine

```
shellforge doctor
```

[SCREENSHOT: healthy doctor output]

If anything is red, that line tells you exactly what to run. Every error code links
into [Troubleshooting](05-troubleshooting.md).

[SCREENSHOT: failing doctor output, so the reader knows what one looks like]

## 8. Step 6: set up the sandbox

```
shellforge init
```

This downloads a small Linux system and imports it into WSL. It takes a few
minutes and it happens once. [SCREENSHOT: init progress]

## 9. Step 7: play

```
shellforge play
```

[SCREENSHOT: the first briefing]

## 10. Common first problems

A short list, each entry linking into [Troubleshooting](05-troubleshooting.md).
The predicted ones are antivirus blocking the import, PATH not picking up the new
binary until the terminal is reopened, and virtualization being off.

## 11. Removing all of this later

See [Uninstall](06-uninstall.md). It is complete and it is honest about the 2 GB
disk image.
