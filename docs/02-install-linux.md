# Install on Linux

> **Status: outline.** Written on Day 7. There is nothing to install yet.

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

The `install.sh` one-liner with its full URL, what it does in four bullets, and the
manual alternative: download the binary from Releases and verify the checksum
yourself. Show the verification command, because a page that tells people to verify
without showing how is not really telling them to verify.

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
