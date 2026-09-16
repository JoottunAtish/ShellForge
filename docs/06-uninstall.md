# Uninstall

Removing Shellforge is two steps: the sandbox, then everything else. There is no
single uninstall command in v0.1.0, so this page names every file and directory
by hand, and the last section tells you how to confirm each one is gone.

Be complete and be explicit. Orphaned 2 GB disk images are where the angry issues
come from, and this page existing properly is the difference.

## `shellforge sandbox destroy` removes the sandbox only

```
shellforge sandbox destroy
```

This removes the sandbox and nothing else:

- On Windows, the `shellforge-sandbox` WSL distribution, and its install
  directory, including the backing `.vhdx` file of roughly 2 GB.
- On Linux and macOS, the `shellforge-sandbox` Docker container. The
  `shellforge-sandbox` image is deliberately left in place, so a rebuild does
  not re-download or rebuild it; see "Manual cleanup" below if you want the
  disk space back.

It asks you to type the sandbox name to confirm, unless you pass `--yes`. Before
it asks, it prints exactly what it is about to remove, including the absolute
directory it will delete on Windows. After it removes the sandbox, it checks
again rather than trusting a successful removal call, and prints the same
absolute path a second time so you always have it to check by hand.

**Your progress, config, and cache are a separate step.** `sandbox destroy`
does not touch the progress database, and it does not touch anything under
your Shellforge data directory other than the sandbox files named above.
Keeping or removing that data is not this command's job.

## Keep your progress first, if you want it

Everything Shellforge knows about you is one SQLite file. Copy it somewhere the
uninstall will not reach and you have kept your progress:

**Linux:**

```bash
cp ~/.local/share/shellforge/progress.db ~/shellforge-progress-backup.db
```

**Windows:**

```powershell
Copy-Item "$env:LOCALAPPDATA\shellforge\progress.db" "$env:USERPROFILE\shellforge-progress-backup.db"
```

Uninstalling removes your progress permanently, and there is no cloud copy to
fall back on, because there is no cloud. Putting the file back where it came
from restores it.

There is no export verb in v0.1.0. A human-readable export is a v0.2 question;
copying the file is the thing that works today, and it is the complete backup
rather than a summary of one.

## Remove everything else

`sandbox destroy` deliberately leaves your progress, configuration and cache
alone. Removing those is this section, and it is three directories on Linux and
two on Windows.

Look at each one before you delete it. If a directory holds something you did
not expect, stop and read it rather than removing it: nothing else on your
machine writes to these paths, so a surprise is worth understanding.

**Linux:**

```bash
ls -la ~/.local/share/shellforge    # progress database
ls -la ~/.cache/shellforge          # the cached rootfs tarball, if one was fetched
ls -la ~/.config/shellforge         # v0.1.0 writes nothing here, so it may not exist
```

```bash
rm -rf ~/.local/share/shellforge
rm -rf ~/.config/shellforge
rm -rf ~/.cache/shellforge
```

**Windows:**

```powershell
Get-ChildItem "$env:LOCALAPPDATA\shellforge"   # progress database, sandbox files, and cache
Get-ChildItem "$env:APPDATA\shellforge"        # v0.1.0 writes nothing here, so it may not exist
```

```powershell
Remove-Item -Recurse -Force "$env:LOCALAPPDATA\shellforge"
Remove-Item -Recurse -Force "$env:APPDATA\shellforge"
```

Two directories rather than three on Windows, because the cache lives at
`%LocalAppData%\shellforge\cache`, one level inside the first path. Removing
that path takes the cache with it.

A "path not found" on the config directory is the expected result rather than a
problem: v0.1.0 has no configuration file, so the directory is resolved and
reserved but never written to. The same goes for `logs` inside the cache.
[How it works](04-how-it-works.md) section 4 has the full table.

If you set `XDG_DATA_HOME`, `XDG_CONFIG_HOME` or `XDG_CACHE_HOME`, Shellforge
used `$XDG_DATA_HOME/shellforge` and so on instead of the defaults above. Check
with `echo $XDG_DATA_HOME` before you delete anything.

Then remove the binary itself. `scripts/install.sh` and `scripts/install.ps1`
each place it in one fixed default directory and, on Windows, add one entry to
your user `Path`:

**Linux**, if you installed with `install.sh` and its default `SHELLFORGE_BIN_DIR`:

```bash
rm -f "$HOME/.local/bin/shellforge"
```

If your shell profile has an `export PATH=...` line adding
`$HOME/.local/bin` that you added yourself when the installer printed it,
and you have nothing else there, remove that line too.

**Windows**, if you installed with `install.ps1` and its default `-BinDir`:

```powershell
Remove-Item -Recurse -Force "$env:LOCALAPPDATA\Programs\shellforge"
```

`install.ps1` also added `%LOCALAPPDATA%\Programs\shellforge` to your user
`Path` environment variable, unless you passed `-NoPathChange`. Remove it from
Settings > System > About > Advanced system settings > Environment Variables,
under the user `Path` entry, or from PowerShell:

```powershell
$dir = "$env:LOCALAPPDATA\Programs\shellforge"
$key = Get-Item 'HKCU:\Environment'
$raw = $key.GetValue('Path', '', 'DoNotExpandEnvironmentNames')
$kind = $key.GetValueKind('Path')
$kept = ($raw -split ';' | Where-Object { $_.TrimEnd('\') -ne $dir }) -join ';'
Set-ItemProperty 'HKCU:\Environment' -Name Path -Value $kept -Type $kind
```

This reads and writes the registry value directly rather than going through
`[Environment]::GetEnvironmentVariable` and `SetEnvironmentVariable`, because
those two always read the expanded value and always write it back as a plain
string. If anything else on your user `Path`, such as
`%LOCALAPPDATA%\Microsoft\WindowsApps`, is stored with a `%...%` token, going
through those two would permanently replace that token with its expanded
form. The five lines above leave every other entry exactly as it was.

If you passed a custom `SHELLFORGE_BIN_DIR` (Linux) or `-BinDir` (Windows) when
you installed, remove that directory instead of the default named above.

## Verify it is actually gone

This section is the entire point of the page. Do not skip it.

**Windows:**

```powershell
wsl -l -v
```

`shellforge-sandbox` must not be listed. `shellforge sandbox destroy` itself
runs this same check for you and fails loudly, naming the absolute path to
check in Explorer, if the distribution is still there afterward: it never
reports success on a removal it has not confirmed.

Then open File Explorer and confirm the install directory `sandbox destroy`
printed is gone. It held a `.vhdx` file of roughly 2 GB, and if the underlying
`wsl --unregister` did not complete, that file is still on your disk taking up
space with nothing pointing at it.

**Linux:**

```bash
docker images | grep shellforge
docker ps -a | grep shellforge
```

`docker ps -a | grep shellforge` should print nothing. `shellforge sandbox
destroy` runs that same check itself before it reports success. `docker
images | grep shellforge` is expected to still print the `shellforge-sandbox`
image: destroy never removes it, on purpose, so that line is not a sign
anything went wrong.

## Manual cleanup, if something went wrong

**Linux: the image `sandbox destroy` leaves behind.** Removing it reclaims
roughly 400 MB, at the cost of a rebuild next time you run `shellforge init`:

```bash
docker rmi shellforge-sandbox
```

**Windows: a distribution `sandbox destroy` could not remove.** This is the
case that produces an orphaned `.vhdx`, so do it in this order.

First look at what you have. This lists every WSL distribution on the machine,
including ones that have nothing to do with Shellforge:

```powershell
wsl -l -v
```

If, and only if, `shellforge-sandbox` is in that list, unregister it by that
exact name:

```powershell
wsl --unregister shellforge-sandbox
```

**Read that command before you run it.** `wsl --unregister` is instant. There
is no confirmation, no recycle bin and no undo, and it permanently destroys
everything inside whichever distribution you name. Type `shellforge-sandbox`
and nothing else. If you have a `Ubuntu` or a `Debian` in that list, those are
yours and unregistering one would take everything in it with no way back.
`shellforge sandbox destroy` exists precisely so you do not have to type this
command: it holds the name as a constant and checks the distribution is ours
before touching it.

Then remove the install directory, which is where the `.vhdx` lives and what
an interrupted unregister leaves behind:

```powershell
Get-ChildItem "$env:LOCALAPPDATA\shellforge\wsl\shellforge-sandbox"
Remove-Item -Recurse -Force "$env:LOCALAPPDATA\shellforge\wsl\shellforge-sandbox"
```

**Never edit `%USERPROFILE%\.wslconfig`.** That file configures every WSL
distribution you have, not just this one, and nothing about installing or
removing Shellforge requires touching it.

## What is left behind after all of this

Nothing, and here is the full list so you can check rather than take our word
for it. Shellforge writes to these paths and no others:

| What | Linux | Windows |
|---|---|---|
| Progress database | `~/.local/share/shellforge/progress.db` | `%LocalAppData%\shellforge\progress.db` |
| Configuration | `~/.config/shellforge/` | `%AppData%\shellforge\` |
| Cache | `~/.cache/shellforge/` | `%LocalAppData%\shellforge\cache\` |
| Sandbox | a Docker container and image named `shellforge-sandbox` | a WSL distribution named `shellforge-sandbox`, and its install directory under `%LocalAppData%\shellforge\` |
| The binary | `~/.local/bin/shellforge` | `%LocalAppData%\Programs\shellforge\shellforge.exe` |
| One `PATH` entry | none: `install.sh` never edits a shell profile | `%LocalAppData%\Programs\shellforge`, in the user `Path` |

Nothing is written outside this list. No registry keys beyond the one `Path`
entry above, no scheduled tasks, no services, no system-wide directories, and
nothing in your home directory other than the paths named here.
