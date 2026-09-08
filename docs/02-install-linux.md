# Install on Linux

> **Status: outline.** Written properly on Day 7, with the per distribution
> container runtime commands, rootless Podman, and the arm64 and macOS notes
> below filled in. `scripts/install.sh` itself is real today, and section 2
> below describes exactly what it does.

**Who this is for:** you are on Linux and comfortable with a package manager. This
page is deliberately short.

## Requirements

- A container runtime: Docker or Podman.
- About 2 GB of disk.
- x86-64 or arm64.

## 1. Install a container runtime

Per-distribution commands for Debian and Ubuntu, Fedora, and Arch.

Include the group step, and the warning that it does not apply to shells that are
already open:

```bash
sudo usermod -aG docker "$USER"
```

## 2. Install Shellforge

```bash
curl -fsSL https://raw.githubusercontent.com/JoottunAtish/ShellForge/main/scripts/install.sh | sh
```

Read that URL before you run it. It is this repository's own `scripts/install.sh`,
nothing else, and you can read the whole file at that same address in a browser
first. What it does:

- Detects your CPU architecture (amd64 or arm64) and downloads the matching
  archive from the release, plus the release's `SHA256SUMS` file.
- Verifies the archive's checksum against `SHA256SUMS` before it touches your
  disk. If they do not match, it deletes what it downloaded and stops: nothing
  is installed.
- Extracts the `shellforge` binary into `$HOME/.local/bin` (override with
  `SHELLFORGE_BIN_DIR`), refusing to overwrite an existing file there unless
  you pass `--force`.
- Prints the `export PATH=...` line to add if that directory is not already on
  your `PATH`, then runs `shellforge doctor` so you know immediately whether
  anything else needs attention. A `doctor` warning does not mean the install
  failed: the binary is on disk either way.

It edits no shell profile. You add the `PATH` line yourself, to the file your
shell actually reads (`~/.bashrc`, `~/.zshrc`, or similar), then open a new
terminal.

**Verifying it yourself, without running anything piped from the network:**

```bash
curl -fsSLO https://github.com/JoottunAtish/ShellForge/releases/download/vX.Y.Z/shellforge_vX.Y.Z_linux_amd64.tar.gz
curl -fsSLO https://github.com/JoottunAtish/ShellForge/releases/download/vX.Y.Z/SHA256SUMS
sha256sum -c --ignore-missing SHA256SUMS
tar -xzf shellforge_vX.Y.Z_linux_amd64.tar.gz
```

Replace `vX.Y.Z` with the release tag you want and `linux_amd64` with
`linux_arm64` on an arm64 machine. `sha256sum -c` prints `OK` next to the file
it checked; anything else means do not run the binary.

Package manager options go here once they exist.

## 3. Verify

```
shellforge doctor
shellforge init
shellforge play
```

## Rootless Podman

Preferred where available: no daemon and no root. Note the subuid and subgid
mapping requirement and how to check that it is set up.

## Notes for arm64

The sandbox image is multi-arch. Call out anything that differs.

## Notes for macOS

macOS is not a supported platform for v0.1. It will probably work through Docker
Desktop or Colima, and we would like to hear from you if it does, but it is
untested by the maintainers and it is labelled community supported for a reason.
