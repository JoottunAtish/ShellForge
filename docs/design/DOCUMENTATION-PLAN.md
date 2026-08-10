# Shellforge Documentation Plan

The docs are the product for everyone who hasn't installed it yet. For your persona (Priya, Windows 11, never used a terminal, scared of breaking her laptop), `docs/01-install-windows.md` matters more than any feature you'll build this week.

**Governing principle:** the reader is anxious and has no context. Explain *what a thing is* before telling them to install it. Never write "just" or "simply".

---

## File map

| File | Audience | Length | Priority |
|---|---|---|---|
| `README.md` | Someone deciding whether to try it | 1 screen + detail | P0 |
| `docs/01-install-windows.md` | Priya | Long, screenshot-heavy | **P0 - most important file** |
| `docs/02-install-linux.md` | Comfortable users | Short | P0 |
| `docs/03-quickstart.md` | Just installed | Medium | P0 |
| `docs/04-how-it-works.md` | Curious / suspicious | Medium | P1 |
| `docs/05-troubleshooting.md` | Stuck | Long, anchored | **P0 - linked from every error** |
| `docs/06-uninstall.md` | Leaving | Short | P0 |
| `docs/07-authoring-levels.md` | Contributors | Long | P2 |
| `CONTRIBUTING.md` | Contributors | Short | P2 |

---

## README skeleton

```markdown
# Shellforge

Learn the Linux command line by living in a terminal you can't break.

![demo](docs/assets/demo.gif)

Shellforge drops you into a real Linux shell inside a disposable sandbox and
gives you jobs to do. Not multiple choice. Not a simulator. You type real
commands and it checks whether you actually did the thing.

You are a new junior sysadmin at Meridian Logistics. The last engineer left
without documentation. There are 25 tickets waiting.

## Install

**Windows** → [Windows install guide](docs/01-install-windows.md)
**Linux**   → [Linux install guide](docs/02-install-linux.md)

Then:
    shellforge doctor    # checks your machine and tells you how to fix anything
    shellforge init      # sets up the sandbox (one time, a few minutes)
    shellforge play      # start

## What you'll learn

| Act | You'll be able to |
|-----|-------------------|
| I - Orientation | Move around a filesystem and read the manual yourself |
| II - Files | Create, copy, move, delete, and batch-handle files with globs |
| III - Plumbing | Redirect output and chain commands into pipelines |
| IV - Search | Find text with grep and files with find |
| V - Permissions | Read `ls -l`, fix permissions, manage processes |
| VI - Automation | Use environment variables and write your first bash script |

Ends with a 3 AM incident you have to diagnose and fix from scratch.

## Can this break my computer?

No, and here's why: everything happens inside an isolated Linux sandbox that
has no access to your files. `rm -rf /` in there destroys the sandbox and
nothing else - and `shellforge reset` rebuilds it in under a second.
[More detail](docs/04-how-it-works.md)

## Is anything sent anywhere?

No. There is no account, no server, and no telemetry. Progress is a SQLite
file on your machine. It works fully offline after install.

## Contributing · License
```

---

## `docs/01-install-windows.md` outline

Structure that matches how a nervous person actually reads:

```
1. What you're about to install, and why
   - "Shellforge needs a Linux system to teach you Linux. Windows can run one
     for you - it's a built-in feature called WSL. We'll set up our own private
     one that doesn't touch anything else on your PC."
   - Time: ~10 minutes, ~2 GB disk.

2. Before you start - the checklist
   - Windows 10 build 19041+ or Windows 11 (how to check: Win+R → winver)
   - ~5 GB free disk
   - Administrator access

3. Step 1 - Install Windows Terminal
   (Do this FIRST. The old console mangles colours and the whole thing looks
   broken. Microsoft Store link. [SCREENSHOT])

4. Step 2 - Turn on virtualization
   - How to check whether it's already on (Task Manager → Performance → CPU →
     "Virtualization: Enabled") [SCREENSHOT]
   - If disabled: what a BIOS is, how to get into it, what setting to look for
     (Intel VT-x / AMD-V / SVM), with the caveat that every manufacturer names
     it differently. Link to a search that finds their specific model.

5. Step 3 - Install WSL
   - `wsl --install --no-distribution` in an admin PowerShell [SCREENSHOT]
   - Why --no-distribution: we bring our own.
   - Reboot.

6. Step 4 - Install Shellforge
   - The install.ps1 one-liner, with the full URL shown so they can read it.
   - What it does, in four bullets, so they're not running a mystery script.

7. Step 5 - Run the check
   - `shellforge doctor` [SCREENSHOT of a healthy output]
   - "If anything is red, each line tells you exactly what to run. If you're
     stuck, every code links into the troubleshooting guide."

8. Step 6 - Set up the sandbox
   - `shellforge init`, what's happening, why it takes a few minutes.

9. Step 7 - Play
   - `shellforge play` [SCREENSHOT of the first briefing]

10. Common first problems  (short list, links into 05-troubleshooting.md)

11. How to remove all of this later → 06-uninstall.md
```

**Screenshot list to capture on Day 7** (10 minutes with a VM):
`winver` · Task Manager virtualization · admin PowerShell · `wsl --install` output · `install.ps1` output · healthy `doctor` · failing `doctor` · `init` progress · first level briefing · a passed level with XP.

---

## `docs/04-how-it-works.md` - the trust document

Answer the five questions a suspicious person has:

1. **Where does the Linux come from?** A minimal Debian system in its own WSL distro (Windows) or container (Linux). Built from a public Containerfile in this repo - link it.
2. **Can it see my files?** No. Automount and Windows interop are disabled; there is no `/mnt/c`. The only shared path is a read-only game directory.
3. **What if I break something?** That's the point. Each level's world lives in one directory; `reset` deletes and rebuilds it. `sandbox rebuild` recreates the whole sandbox.
4. **What's stored, and where?** A SQLite file plus config, in `%LOCALAPPDATA%\shellforge` / `~/.local/share/shellforge`. Lists exactly what's recorded: which levels you passed, which commands you ran, timings. Says plainly that command *output* is not stored beyond the current session, that nothing leaves the machine, and how to delete it all.
5. **What does it install on my system?** Enumerate: one binary, one WSL distro, one config dir. Nothing else. Link to uninstall.

This page costs an hour and prevents the "is this spyware" issue thread.

---

## `docs/05-troubleshooting.md` - anchored, machine-linked

Every `doctor` probe emits a `DocAnchor`, and CI fails if the anchor doesn't exist here. Format per entry:

```markdown
## virtualization-disabled
**You'll see:** `doctor` reports "CPU virtualization: not available"

**What it means:** Your CPU can run virtual machines, but the feature is
switched off in your PC's firmware. WSL needs it.

**Fix:**
1. Restart and press <F2 / F10 / Del> during boot (varies by manufacturer)
2. Look for "Intel VT-x", "AMD-V", "SVM Mode", or "Virtualization Technology"
3. Enable it, save, exit
4. Run `shellforge doctor` again

**Still stuck?** Search "<your laptop model> enable virtualization BIOS".
```

Also cover the predictable ten: WSL1-only distro, `wsl --import` blocked by antivirus, Docker daemon not running, "command not found: shellforge" (PATH), garbled output in old console, terminal too small, disk full during init, level state corrupted (→ `reset --hard`), sandbox missing after Windows update, permission denied inside the sandbox.

---

## `docs/06-uninstall.md`

Be explicit and complete - this is where orphaned 2 GB `.vhdx` files come from.

```
shellforge sandbox destroy      # removes the WSL distro / container
shellforge uninstall            # removes config, progress, cache
```
Then the manual verification: `wsl -l -v` shows no `shellforge-sandbox`, and the install directory is gone. Show the exact paths per OS. Note that this removes progress permanently and how to `shellforge export` first if they want to keep it.

---

## Documentation quality gates (Day 7 checklist)

- [ ] Someone who has never seen the project installs it using only the docs, unaided, while you watch and say nothing
- [ ] Every `doctor` failure code has a matching anchor (CI-enforced)
- [ ] Every command shown in the docs is copy-pasteable and correct - run them
- [ ] No unexplained jargon: WSL, distro, shell, sandbox, PATH each defined at first use
- [ ] Screenshots are current, not from an early build
- [ ] The demo GIF is under 5 MB and shows a real level being solved
- [ ] README renders correctly on GitHub (check the table and GIF)
- [ ] Uninstall verified on a real machine - check with Explorer that the vhdx is gone

---

## Demo GIF

Use **VHS** (`charmbracelet/vhs`) rather than screen recording - it's a scripted `.tape` file, so the demo is reproducible and re-recordable when the UI changes.

Storyboard, ~25 seconds:
```
1. `shellforge play`            → briefing appears: "02:41 - Billing service"
2. `ls logs/`                   → three log files
3. `grep -ri ERROR logs/ | wc -l > report.txt`
4. `check`                      → ✅ objectives turn green, +150 XP, rank up
```
The rank-up moment at the end is what makes people click install.
