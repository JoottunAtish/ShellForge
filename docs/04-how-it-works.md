# How it works

> **Status: outline.** Written on Day 7. This is the trust document, and it is
> worth the hour it costs.

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

Each level's world lives in one directory. `reset` deletes it and rebuilds it in
under a second. `sandbox rebuild` recreates the whole sandbox from the pristine
image. `rm -rf /` inside the sandbox destroys the sandbox and nothing else.

## 4. What is stored, and where?

A SQLite database and a small configuration file, in `%LOCALAPPDATA%\shellforge` on
Windows or `~/.local/share/shellforge` on Linux.

It records which levels you passed, which commands you ran, and how long you took.
Command *output* is not stored beyond the current session. This page will list the
exact tables and show you how to delete all of it.

**Nothing leaves your machine.** There is no account, no server, and no telemetry,
and none are planned.

One honest caveat, stated plainly: the command journal records what you type, and
people sometimes type passwords into shells. It is stored with restrictive file
permissions and it is never transmitted. `shellforge bug-report` includes the
commands you ran but not their output, and never the environment snapshot.

## 5. What does it install on my system?

One binary. One WSL distribution, or one container image. One configuration
directory. Nothing else: no background services, no startup entries, no drivers.

See [Uninstall](06-uninstall.md) for removing every one of those.

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

You can read exactly what that configuration file does:
[`images/rc/instrument.bash`](../images/rc/instrument.bash).

Verification is separate, and it looks at the real system. If a level asks for a
count in `report.txt`, it reads `report.txt` and checks the number. It does not
care whether you used `grep`, `awk`, a loop, or a script you wrote yourself. That
is deliberate: checking what you typed would punish you for finding a better
solution than the one the author had in mind.

## Why "you cannot break it" is a real promise and not marketing

Three independent things have to hold, and all three are checked in CI:

1. **Isolation.** No host mounts except one read-only directory, no network by
   default, never `--privileged`, and a non-root user.
2. **Reset.** Every level's world lives under one directory, and reset is a delete
   and a rebuild. No snapshot restore that might half-work.
3. **Determinism.** Setup never touches the network. The same level produces
   byte-identical starting state on every machine, every time.
