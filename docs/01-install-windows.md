# Install on Windows

> **Status: outline.** This is the most important file in the repository and it is
> written properly on Day 7, with screenshots, against a build that actually
> installs. There is nothing to install yet.

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

The one-line installer, with the full URL written out so you can read it before
running it, plus four bullets saying exactly what it does. Nobody should run a
script they have not been told the contents of.

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
