# Install on Linux

**Who this is for:** you are on Linux and comfortable with a package manager. This
page is deliberately short.

## Requirements

- **Docker.** Not Podman: see the note at the end of this page.
- About 2 GB of disk.
- x86-64 or arm64.
- A clone of this repository, for now. See ["Provisioning the sandbox"](#provisioning-the-sandbox)
  below, which is the one step of this page that v0.1.0 does not do for you.

## 1. Install Docker

Docker is what runs the disposable Linux sandbox you play in. If you already have
a working `docker` command, skip this section.

**Debian and Ubuntu:**

```bash
sudo apt-get update
sudo apt-get install -y docker.io
sudo systemctl enable --now docker
```

**Fedora:**

```bash
sudo dnf install -y docker
sudo systemctl enable --now docker
```

**Arch:**

```bash
sudo pacman -S --needed docker
sudo systemctl enable --now docker
```

Then add yourself to the `docker` group, so that Shellforge can talk to the
Docker daemon without `sudo`:

```bash
sudo usermod -aG docker "$USER"
```

**That does not apply to terminals you already have open.** Group membership is
read when you log in, so log out and back in, or run `newgrp docker` in the
terminal you are using. Skipping this is what produces
`permission denied while trying to connect to the Docker daemon socket`, which
is [docker-permission-denied](05-troubleshooting.md#docker-permission-denied) in
the troubleshooting guide.

Check it worked:

```bash
docker run --rm hello-world
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

There is no distribution package, no PPA, no AUR entry and no Homebrew formula.
The binary above is the whole program.

## 3. Provisioning the sandbox

```bash
shellforge doctor
```

`doctor` checks your machine and every line it prints that is not `ok` tells you
what to run next, with a link into [Troubleshooting](05-troubleshooting.md).
Then:

```bash
shellforge init
```

**This step needs a clone of this repository in v0.1.0, and that is a known
bug.** `init` builds the sandbox image from `images/Containerfile`, and it looks
for that file by walking up from wherever you are standing to the nearest
`go.mod`. Run it anywhere else and it stops with `could not find
images/Containerfile`. It is tracked as
[issue #172](https://github.com/JoottunAtish/ShellForge/issues/172), and until it
is fixed:

```bash
git clone https://github.com/JoottunAtish/ShellForge.git
cd ShellForge
shellforge init
```

You only need to be inside the clone for `init`. Once the image exists, `init`
finds it and never rebuilds, so `shellforge play` works from any directory
afterwards and the clone is only in the way if you delete the image.

## 4. Play

```bash
shellforge play
```

## Notes for arm64

The sandbox image is built on your own machine from `debian:bookworm-slim`,
which Debian publishes for both amd64 and arm64, so the build picks up your
architecture and there is nothing to configure. `install.sh` detects the
architecture itself and fetches the matching binary.

## Why not Podman

Podman would suit this project: no daemon and no root. It is not supported in
v0.1.0 because Shellforge implements exactly two runtime backends, Docker and
WSL, and shipping a third that had never been run on a clean machine would be a
worse outcome than saying no. `doctor` reports
[no-runtime-available](05-troubleshooting.md#no-runtime-available) if Podman is
all you have, rather than trying and failing in a way you would have to debug.

It is a v0.2 question and a reasonable one to open an issue about.

## Notes for macOS

macOS is not a supported platform for v0.1. It will probably work through Docker
Desktop or Colima, and we would like to hear from you if it does, but it is
untested by the maintainers and it is labelled community supported for a reason.
