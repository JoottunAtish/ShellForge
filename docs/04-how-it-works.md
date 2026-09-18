# How it works

Five questions a reasonably suspicious person should ask before running an
unfamiliar program that says it wants to teach them Linux.

## 1. Where does the Linux come from?

A minimal Debian system, built from a
[Containerfile in this repository](../images/Containerfile) that you can read
right now. On Windows it becomes its own WSL distribution. On Linux it is a
container.

It is not a shared distribution and it is not one you already had. Your existing
Ubuntu, if you have one, is untouched.

## 2. Can it see my files?

No.

On Windows, [`/etc/wsl.conf`](../images/wsl.conf) disables automount and interop.
There is no `/mnt/c`, so your real files are not reachable, and Windows programs
cannot be launched from inside the sandbox. On Linux, the container runs with no
host mounts and no network.

The single exception is `/opt/shellforge`, which is mounted read-only and contains
the game's own files.

## 3. What if I break something?

That is the point.

Each level's world lives in one directory. Typing `reset` at the prompt deletes it
and rebuilds it in under a second: `reset` on its own tells you what it would
delete, and `reset --yes` does it. It only ever touches the level's own directory,
so a file you saved elsewhere in your sandbox home survives.

`shellforge sandbox rebuild` recreates the whole sandbox from the pristine image.
`rm -rf /` inside the sandbox destroys the sandbox and nothing else.

## 4. What is stored, and where?

One SQLite database, `progress.db`. That is the whole of what Shellforge knows
about you, and copying that one file is a complete backup.

Shellforge resolves four directories and writes inside them and nowhere else:

| Directory | Linux | Windows | What is in it at v0.1.0 |
|---|---|---|---|
| Data | `~/.local/share/shellforge/` | `%LocalAppData%\shellforge\` | `progress.db`, and on Windows the sandbox's WSL backing store under `wsl\shellforge-sandbox\`, which is where the roughly 2 GB `.vhdx` lives |
| Cache | `~/.cache/shellforge/` | `%LocalAppData%\shellforge\cache\` | `rootfs/rootfs.tar.gz`, if the sandbox image was fetched rather than built locally. Otherwise the directory may not exist. |
| Config | `~/.config/shellforge/` | `%AppData%\shellforge\` | Nothing. v0.1.0 has no configuration file, so this directory may never be created. |
| Logs | `~/.cache/shellforge/logs/` | `%LocalAppData%\shellforge\cache\logs\` | Nothing. v0.1.0 writes no log files, so this directory may never be created. |

The last two rows are listed because they are resolved and reserved, and because
[Uninstall](06-uninstall.md) tells you to remove them. Finding them empty or
absent is the expected outcome, not a sign that something went wrong.

On Windows the cache sits one level inside the data directory, which means
removing the data directory removes the cache with it. Linux keeps them apart,
following the XDG convention, and honours `XDG_DATA_HOME`, `XDG_CONFIG_HOME` and
`XDG_CACHE_HOME` if you have set them.

The database records which levels you passed, your score and rank, which
achievements you have earned, and, per level, the commands you ran with their
exit codes and timings. Command *output* is not stored at all: not in the
database, not on disk, not beyond the moment it reaches your screen.

**Nothing leaves your machine.** There is no account, no server, and no telemetry,
and none are planned.

One honest caveat, stated plainly: the command journal records what you type, and
people sometimes type passwords into shells. It is stored with restrictive file
permissions and it is never transmitted. `shellforge bug-report` never includes
your commands by default: only `shellforge bug-report --journal` does, and even
then every command is run through a redaction step first, a closed list that
strips out things like `PASSWORD=...` assignments, `--token` and similar flags,
`Authorization` headers, and PEM key blocks. The bundle never includes command
output, and never the environment snapshot.

## 5. What does it install on my system?

Four things on Windows and three on Linux. Here is all of it:

1. **One binary.** `shellforge.exe` in `%LocalAppData%\Programs\shellforge\` on
   Windows, or `shellforge` in `~/.local/bin/` on Linux. It is a single static
   executable with no runtime to install alongside it.
2. **One sandbox.** On Windows, a WSL distribution named `shellforge-sandbox`,
   backed by a `.vhdx` of roughly 2 GB under your data directory. On Linux, a
   Docker image and container, both named `shellforge-sandbox`.
3. **The directories in section 4**, which amount to one SQLite file and possibly
   one cached tarball.
4. **On Windows only, one `PATH` entry**, adding
   `%LocalAppData%\Programs\shellforge` to your user `Path` so that typing
   `shellforge` works. `install.ps1` skips this if you pass `-NoPathChange`.
   `install.sh` never edits a shell profile on Linux: it prints the `export` line
   and leaves the decision to you.

Nothing else. No background service, no scheduled task, no startup entry, no
driver, no browser extension, no registry key beyond the one `PATH` entry, and
nothing written outside your own user account. Shellforge never asks for
Administrator or `sudo`, and neither installer elevates.

[Uninstall](06-uninstall.md) removes every one of those and tells you how to
confirm each is gone.

---

## How the game knows what you did

Worth explaining, because it sounds either like magic or like spying, and it is
neither.

The shell is real bash. Shellforge does not read your commands and re-run them
somewhere else, and it does not implement its own shell. Instead, bash is started
with a configuration file that makes the prompt emit invisible markers, using a
terminal standard called OSC 133. Shellforge strips those markers out of the output
before you ever see them. They tell it when a command started, when it finished,
and what exit code it produced.

The same configuration file appends one line per command to a plain text file
inside the sandbox, `journal.tsv`: the time, the exit code, the directory you were
in, and the command line itself. Shellforge reads that file to fill in your command
log and to work out whether you beat the level's par. That is the only place your
command text comes from, and it is why the count is right even for a command you
ran inside a script.

You can read exactly what that configuration file does:
[`images/rc/instrument.bash`](../images/rc/instrument.bash).

**Where that text goes, and where it does not.** It is copied out of the sandbox
into the SQLite database described in section 4, on your own machine, and that is
the end of its journey. It is never uploaded, never included in a crash report, and
never printed by an error message. There is no server to send it to. If the file
goes missing or is unreadable, Shellforge records no commands for that level and
carries on: a lost journal costs you an efficiency bonus, never a level.

Verification is separate, and it looks at the real system. If a level asks for a
count in `report.txt`, it reads `report.txt` and checks the number. It does not
care whether you used `grep`, `awk`, a loop, or a script you wrote yourself. That
is deliberate: checking what you typed would punish you for finding a better
solution than the one the author had in mind.

## What happens when you type `check`

The whole round trip, because it explains why verification cannot be fooled from
inside the sandbox, and why a `check` sometimes pauses.

```
inside the sandbox                          on your machine
------------------                          ---------------
you type `check`
  |
  v
/opt/shellforge/bin/check
writes one line to
$SF_STATE/control.req  ------------------>  the host is blocked reading that
                                            named pipe, and wakes up
                                              |
                                              v
                                            it runs the level's checks itself,
                                            reading the sandbox through a
                                            separate non-interactive shell
                                              |
                                              v
$SF_STATE/control.res  <------------------  it writes the rendered result back
  |
  v
`check` prints it
```

**The checks run on your machine, not in the sandbox.** That is the important
part. Nothing inside the sandbox decides whether you passed, so no amount of
editing, deleting or breaking things in there can change the verdict. It also
means a level about file permissions cannot accidentally lock the game out of its
own verification.

The shim inside the sandbox is deliberately stupid: it writes a line and prints
what comes back. You can read it:
[`images/bin/_sf-request`](../images/bin/_sf-request).

**Why a `check` occasionally pauses for a few seconds.** If you press Ctrl-C
while a check is running, the shim dies immediately and you get your prompt back,
but the host has already started work and is holding a reply nobody is reading
any more. It gives up after ten seconds. Type `check` again straight away and it
may wait out the rest of that, once. Nothing is lost and nothing is stale: the
reply you eventually see is always the result of the check you asked for.

## What a live pass does between checks

You do not have to type `check` to see an objective tick. After each command
finishes, the game quietly re-runs a subset of the level's checks on your
machine, the same way `check` does, and prints a line for any objective that
just started passing, or that just stopped, with no other output. This is a
live pass.

A live pass only ever runs the checks cheap enough to fire on every command
without you noticing: a single read of the sandbox, or a read of what you
already typed. A check that has to walk a whole directory tree, or run a
script you authored, is not cheap by that definition, so it never ticks on
its own; `check` still covers it, exactly as before. A live pass also never
overlaps a `check` you typed yourself, or a `reset`: they share a lock, so
one always waits for the other to finish rather than reading the sandbox at
the same moment.

A live pass never decides pass or fail, never shows why something is wrong,
and never runs on a timer while you are simply reading the briefing: only a
command finishing triggers one. `check` is still the only complete answer,
and the only one that explains a failure. Run
`shellforge run nav-01 --live-check=off` to turn the ticking off and hear
only from `check` itself.

## Why "you cannot break it" is a real promise and not marketing

Three independent things have to hold. CI checks all three against the Docker
backend on every pull request. The WSL backend's own contract suite needs a
Windows machine with WSL2, which no runner this project can reach has, so there
it is checked by hand instead: see the exception under isolation below, which is
what checking it by hand found.

1. **Isolation.** No host mounts except one read-only directory, never
   `--privileged`, and a non-root user. On Docker, no network by default either.

   Not on WSL, and this is the one place the promise is thinner than the
   sentence above. WSL2 gives every distribution the same virtual adapter and
   offers no switch to take it away, so a sandbox there can reach the network
   and can see your machine on it. What keeps your files safe on Windows is the
   mount side rather than the network side: automount and interop are off, so
   there is no `/mnt/c` and no way to launch a Windows program. `CHANGELOG.md`
   carries this as a known issue for v0.1.
2. **Reset.** Every level's world lives under one directory, and reset is a delete
   and a rebuild. No snapshot restore that might half-work. The deletion runs
   inside the sandbox, as the learner, through one validated helper that refuses
   any path outside that directory, and there is a refusal test for every way a
   path could go wrong.
3. **Determinism.** Setup never touches the network. The same level produces
   byte-identical starting state on every machine, every time.
