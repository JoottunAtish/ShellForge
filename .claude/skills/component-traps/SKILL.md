---
name: component-traps
description: The known traps in each Shellforge subsystem, gathered from the design record so nobody rediscovers them the expensive way. Load before working on the OSC parser or PTY multiplexer, a runtime implementation, the verification engine, the store, the sandbox image, or anything Windows or WSL specific.
---

# Component traps

Each of these cost real time to discover, either in this project's design phase or
in every other project that has tried the same thing. Read the relevant section
before you start, not after you are debugging.

## internal/pty: the OSC parser

**The parser is a streaming byte-level state machine, not a regex over a buffer.**
Escape sequences arrive split across `Read` calls at arbitrary offsets. A buffered
implementation drops markers roughly one percent of the time, which is frequent
enough to reach users and rare enough to cost a day.

States: `Normal -> ESC -> OSC -> OSC_PAYLOAD -> (BEL | ESC backslash terminator)`.
Buffer only the payload.

Recognize `133;A`, `133;B`, `133;C`, `133;D;<exit>`, `7;file://...`.

**Strip recognized sequences from the forwarded stream. Forward every other byte
unmodified.** `vim` depends on that being literally true, not approximately true.

Other requirements, each of which breaks something visible if missed:

- **`SIGWINCH` to `TIOCSWINSZ` propagation.** Without it, resizing corrupts `vim`.
- **Host terminal raw mode with guaranteed restore on every exit path**: `defer`,
  plus a signal handler, plus panic recovery. A crash that leaves the user's
  terminal in raw mode means they cannot type, and they will have to close the
  window. Unforgivable and easy to get wrong.
- **Unrecognized OSC payloads must not corrupt parser state.** A learner typing
  `printf '\e]133;fake\a'` is a real scenario, especially in Act VI.
- Fuzz target required.

## The instrumentation rc file

`images/rc/instrument.bash`. Three traps, all silent:

- **The `\[` and `\]` readline markers around escape sequences in `PS1` are
  required.** They tell readline the bytes occupy zero display columns. Without
  them bash miscounts the prompt width and line wrapping corrupts the moment the
  learner types past the terminal edge. This is the most common way to get OSC 133
  subtly wrong.
- **Capture `$?` on the very first line of the `PROMPT_COMMAND` function.** Any
  statement before it, including a `local` declaration with an assignment,
  overwrites the exit status you are trying to record.
- **`HISTCONTROL` must be unset.** The defaults drop duplicate and space-prefixed
  commands, and the game needs every command, including the repeated ones. "You
  retyped that path four times, try Tab" depends on it.

## internal/runtime

Write the **contract test suite once** in `internal/runtime/runtimetest` and run it
against every implementation. Adding `WslRuntime` should mean writing zero new
tests. If you are writing runtime-specific tests, the abstraction is leaking.

- **`Provision` is idempotent.** Safe to call on an existing sandbox.
- **`Session.Exec` takes `argv []string`, not a command string.** That is a security
  boundary. Do not add a string overload.
- **Never let a runtime implementation type escape above L1.** If the game layer
  needs backend information, add it to `Runtime.Capabilities()`.
- **Ctrl-C leaves no orphan containers and no half-imported distribution.** Every
  operation takes a context and honours it.

## Windows and WSL

This is where the schedule risk lives. Every item here is a known,
already-diagnosed trap.

- **`wsl.exe` outputs UTF-16LE.** Parsing `wsl -l -v` as UTF-8 gives garbage with
  null bytes between every character. This catches everyone exactly once. Decode it.
- **Check `wsl -l -q` for a name collision before `wsl --import`.** Refuse or
  suffix; never overwrite.
- **Verify the rootfs sha256 before import, not after.** After is not verification.
- **`wsl --unregister` is instant and irreversible.** See the `destructive-safety`
  skill. The developer machine for this project has a personal `Debian` sitting
  next to the sandbox.
- **`/etc/wsl.conf` needs `wsl --terminate` before it takes effect.** Write it,
  terminate, then verify it applied. Do not assume.
- **`.wslconfig` in the user profile is global to every distribution.** Never write
  it without asking; it changes the behaviour of the learner's own environment.
- **Never store level state on `/mnt/c`.** 9p is 10 to 50 times slower on many
  small files and the permissions are fake, which breaks every permissions level.
- **Antivirus frequently mangles `wsl --import` of a large tarball.** Detect a slow
  or failed import and suggest an exclusion for the install directory.
- **WSL shuts down after about eight seconds idle.** Do not rely on background
  processes surviving.
- **Enable `ENABLE_VIRTUAL_TERMINAL_PROCESSING` and `ENABLE_VIRTUAL_TERMINAL_INPUT`,
  and restore the original console mode on exit.**
- **ConPTY can garble on resize during a full-screen application** on older builds.
  Check `WT_SESSION` and warn when not in Windows Terminal.
- **Paths via `os.UserConfigDir` and `os.UserCacheDir`.** Never hardcoded.

## internal/verify

- **Checks run in a fresh non-interactive shell via `Session.Exec`, never in the
  learner's shell.** They must not pollute history, environment, or the job table.
- **Shell-local state comes from the snapshot files** written by `PROMPT_COMMAND`,
  not from the check's own process. A check that reads its own `os.Getenv` for
  `env_var` will fail on level `env-01`, which is exactly the level that tests it.
  This is the subtle one; get it wrong and the level is unsolvable.
- **Never short-circuit.** Every check runs so the objective checklist is complete,
  even though only the first failing required check has its `on_fail` displayed.
- **Checks never mutate.** The purity test hashes the filesystem across a double
  run.
- **Unknown check type is a load-time validation error**, never a runtime surprise.
- Per-check timeout 10 s, per-level 60 s, and a timeout is a distinct failure with
  its own message.

## internal/content

- **Strip `\r` from every file materialized into the sandbox.** `.gitattributes` is
  the first defence and this is the second. Both are deliberate.
- **Setup is transactional:** stage, checksum, move, run script, write the
  `SETUP_OK` sentinel. Any failure rolls back and reports cleanly.
- **Teardown always runs before setup**, so setup is idempotent by construction.
- **Pack paths are untrusted.** See the `security` skill.

## internal/store

- WAL mode. Migrations embedded, forward-only, applied on open.
- **Never hand-edit a migration that has shipped.**
- Parameterized queries only. No `fmt.Sprintf` into SQL, even for internal values.
- The database records every command the learner typed. Create it mode 0600 in a
  directory created mode 0700.

## The sandbox image

- **Do not strip man pages.** Debian slim excludes them by default via
  `/etc/dpkg/dpkg.cfg.d`, and `man ls` failing makes `nav-04` unplayable. That
  level is the most valuable one in the game, because everything after it assumes
  the learner can read a manual. The `Containerfile` removes the exclusion rules
  and reinstalls coreutils to get them back, and CI asserts `man ls` works.
- **GNU userland, not busybox.** Alpine's utilities diverge from GNU coreutils and
  would teach wrong flags.
- **Locale `C.UTF-8`, `TZ=UTC`, pinned.**
- One build produces two artifacts: the OCI image and the WSL rootfs tarball. **If
  they diverge, a level passes on Linux and fails on Windows for reasons nobody can
  reproduce.**
