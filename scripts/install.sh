#!/bin/sh
# Shellforge installer.
#
#   curl -fsSL <url>/install.sh | sh
#
# POSIX sh only: no bash arrays, no bash test brackets, no locals declared
# outside a function, no echo with a dash e flag, no here strings, and no
# bash-only pipe status option, which is not POSIX. set -eu at the top.
#
# Verification happens before placement. verify() runs before place() ever
# does, and place() cannot run at all unless verify() succeeded. An
# installer that verifies after placing something is not verifying.
#
# The release published by .github/workflows/release.yml carries three
# archives: shellforge_VERSION_linux_amd64.tar.gz, shellforge_VERSION_linux_arm64.tar.gz,
# and shellforge_VERSION_windows_amd64.zip. This script only ever fetches one
# of the first two, chosen by detect_platform; the Windows archive belongs to
# scripts/install.ps1.
#
# Environment overrides, all optional:
#   SHELLFORGE_VERSION    a tag such as v0.1.0. Default: the latest release.
#   SHELLFORGE_BIN_DIR    where the binary goes. Default: $HOME/.local/bin.
#   SHELLFORGE_BASE_URL   where to fetch from. Default:
#                         https://github.com/JoottunAtish/ShellForge/releases/download
#
# Flags:
#   --force               overwrite an existing file at the target path.
#
# This script edits no shell profile. Not .bashrc, not .profile, not
# .zshrc. It prints the export line and stops: silently editing a person's
# shell configuration reaches outside the program's own business, and it is
# also what makes an installer impossible to undo.

set -eu

DEFAULT_BASE_URL="https://github.com/JoottunAtish/ShellForge/releases/download"

# Set by detect_platform.
OS=""
ARCH=""
# Set by resolve_version.
VERSION=""
# Set by main, read by cleanup via the trap.
WORKDIR=""
# Set by main from the --force flag, read by validate_bin_dir.
FORCE=0

die() {
  # $1 what failed, $2 the next command to run or what to check. Every
  # refusal and every failure goes through here, so every message names
  # what failed, why, and the next step, per non-negotiable 6 in CLAUDE.md.
  msg="$1"
  remediation="$2"
  echo "install.sh: $msg" >&2
  echo "  $remediation" >&2
  exit 1
}

detect_platform() {
  uname_s="$(uname -s)"
  uname_m="$(uname -m)"

  case "$uname_s" in
    Linux)
      OS=linux
      ;;
    Darwin)
      die "shellforge v0.1 ships Linux and Windows. macOS is not built because nothing in this project has ever run on it." \
          "Run this installer on Linux, or use scripts/install.ps1 from inside WSL on Windows."
      ;;
    *)
      die "unsupported operating system: $uname_s" \
          "shellforge v0.1 ships Linux and Windows only. Run this installer on one of those."
      ;;
  esac

  case "$uname_m" in
    x86_64 | amd64)
      ARCH=amd64
      ;;
    aarch64 | arm64)
      ARCH=arm64
      ;;
    *)
      die "unsupported CPU architecture: $uname_m" \
          "shellforge v0.1 ships linux_amd64 and linux_arm64 binaries only. Build from source instead: go build -o bin/shellforge ./cmd/shellforge"
      ;;
  esac
}

resolve_version() {
  if [ -n "${SHELLFORGE_VERSION:-}" ]; then
    VERSION="$SHELLFORGE_VERSION"
    return 0
  fi

  if [ "$SHELLFORGE_BASE_URL" != "$DEFAULT_BASE_URL" ]; then
    die "SHELLFORGE_VERSION is not set, and SHELLFORGE_BASE_URL is set to a custom value" \
        "Set SHELLFORGE_VERSION to a tag such as v0.1.0. A custom base URL cannot be resolved to a latest version, because the asset names carry the version."
  fi

  latest_url="https://github.com/JoottunAtish/ShellForge/releases/latest"
  if command -v curl >/dev/null 2>&1; then
    effective="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$latest_url")" || \
      die "could not resolve the latest release" \
          "Check your network connection, or set SHELLFORGE_VERSION to a specific tag such as v0.1.0."
  elif command -v wget >/dev/null 2>&1; then
    effective="$(wget --max-redirect=5 -S --spider "$latest_url" 2>&1 \
      | awk '/^ *Location: /{loc=$2} END{print loc}')" || true
    if [ -z "$effective" ]; then
      die "could not resolve the latest release" \
          "Check your network connection, or set SHELLFORGE_VERSION to a specific tag such as v0.1.0."
    fi
  else
    die "neither curl nor wget is installed" \
        "Install curl or wget, then run the installer again."
  fi

  VERSION="${effective##*/}"
  if [ -z "$VERSION" ]; then
    die "could not determine the latest release tag from $effective" \
        "Set SHELLFORGE_VERSION to a specific tag such as v0.1.0."
  fi
}

fetch() {
  # $1 url, $2 destination path.
  url="$1"
  dest="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" -o "$dest" || \
      die "download failed: $url" \
          "Check the URL and your network connection, then run the installer again."
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$dest" "$url" || \
      die "download failed: $url" \
          "Check the URL and your network connection, then run the installer again."
  else
    die "neither curl nor wget is installed" \
        "Install curl or wget, then run the installer again."
  fi
}

verify() {
  # $1 archive path, $2 SHA256SUMS path, $3 asset name. Refuses on
  # mismatch. This is the security core: the order in main() is what makes
  # it real, and this is the only place a digest is compared.
  archive="$1"
  sums="$2"
  asset="$3"

  if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$archive" | awk '{print $1}')"
  elif command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "$archive" | awk '{print $1}')"
  else
    die "neither sha256sum nor shasum is installed" \
        "Install coreutils (sha256sum) or perl (shasum), then run the installer again."
  fi

  # Exact filename field match, not a substring: shellforge_VERSION_linux_amd64.tar.gz
  # and shellforge_VERSION_linux_arm64.tar.gz share a long prefix.
  line="$(awk -v want="$asset" '$2 == want { print; found = 1 } END { exit !found }' "$sums")" || \
    die "SHA256SUMS has no entry for $asset" \
        "The release may be incomplete. Try a different SHELLFORGE_VERSION, or report this."
  expected="$(printf '%s\n' "$line" | awk '{print $1}')"

  if [ "$actual" != "$expected" ]; then
    die "checksum mismatch for $asset: expected $expected, got $actual" \
        "Nothing was installed. The download may be corrupted or tampered with. Run the installer again, and if this repeats, report it."
  fi
}

validate_bin_dir() {
  # Every refusal here leaves the filesystem unchanged and exits non-zero.
  bd="$1"

  if [ -z "$bd" ]; then
    die "SHELLFORGE_BIN_DIR is empty" \
        "Set SHELLFORGE_BIN_DIR to an absolute path such as \$HOME/.local/bin, or unset it to use that default."
  fi

  if [ "$bd" = "/" ]; then
    die "SHELLFORGE_BIN_DIR is the filesystem root" \
        "Set SHELLFORGE_BIN_DIR to a real directory such as \$HOME/.local/bin."
  fi

  case "$bd" in
    "~" | "~"/*)
      die "SHELLFORGE_BIN_DIR is '$bd': a literal ~ is not expanded inside a shell variable" \
          "Use \$HOME instead of ~, for example SHELLFORGE_BIN_DIR=\$HOME/.local/bin"
      ;;
  esac

  if [ "$bd" = "$HOME" ]; then
    die "SHELLFORGE_BIN_DIR is your home directory" \
        "shellforge will not scatter a binary directly into \$HOME. Use \$HOME/.local/bin, the documented default."
  fi

  case "/$bd/" in
    */../*)
      die "SHELLFORGE_BIN_DIR contains a '..' segment: $bd" \
          "Pass a plain absolute path with no '..' segments."
      ;;
  esac

  case "$bd" in
    /*) : ;;
    *)
      die "SHELLFORGE_BIN_DIR is not an absolute path: $bd" \
          "Pass an absolute path, for example \$HOME/.local/bin."
      ;;
  esac

  target="$bd/shellforge"
  if [ -L "$target" ]; then
    die "$target is a symlink" \
        "Remove it by hand and run the installer again. Writing through a symlink can land the binary somewhere you did not name, so this is refused even with --force."
  fi

  if [ -e "$target" ] && [ "$FORCE" -ne 1 ]; then
    die "$target already exists" \
        "Re-run with --force to overwrite it, or remove it yourself first."
  fi
}

place() {
  # Extract the archive into the temp dir this script created, then move
  # the binary into BIN_DIR. Nothing here runs unless verify() already
  # succeeded.
  archive="$1"
  bin_dir="$2"

  extract_dir="$WORKDIR/extract"
  mkdir -p "$extract_dir"
  tar -xzf "$archive" -C "$extract_dir" shellforge || \
    die "could not extract $archive" \
        "The archive may be corrupted. Delete it and run the installer again."

  mkdir -p "$bin_dir" || \
    die "could not create $bin_dir" \
        "Check that you have permission to create directories under $(dirname "$bin_dir")."

  chmod 0755 "$extract_dir/shellforge"
  mv -f "$extract_dir/shellforge" "$bin_dir/shellforge" || \
    die "could not move the binary into $bin_dir" \
        "Check that you have write permission there."
}

path_advice() {
  # Print the export line when BIN_DIR is not on PATH. Edits nothing.
  bin_dir="$1"
  case ":$PATH:" in
    *":$bin_dir:"*)
      return 0
      ;;
  esac
  echo
  echo "$bin_dir is not on your PATH. Add this line to your shell profile, then open a new terminal:"
  echo
  echo "    export PATH=\"$bin_dir:\$PATH\""
}

run_doctor() {
  # Runs the installed binary's doctor, never fails the install. A failing
  # doctor is information (a missing Docker, say), not an install error.
  bin_dir="$1"
  echo
  echo "shellforge is installed at $bin_dir/shellforge"
  echo "Next: run shellforge init"
  echo
  if "$bin_dir/shellforge" doctor; then
    echo "doctor: no problems found"
  else
    echo "doctor reported a problem. This does not mean the install failed: the binary is installed."
    echo "Run shellforge doctor for details once you have addressed it."
  fi
  return 0
}

cleanup() {
  # The only rm -rf in this script, and it only ever operates on the
  # mktemp -d directory this script itself created, guarded to match its
  # own template. It never takes a value derived from user input.
  case "${WORKDIR:-}" in
    "")
      : ;;
    */shellforge-install.*)
      rm -rf "$WORKDIR"
      ;;
    *)
      echo "install.sh: refusing to remove unexpected directory: $WORKDIR" >&2
      ;;
  esac
}

main() {
  for arg in "$@"; do
    case "$arg" in
      --force)
        FORCE=1
        ;;
      *)
        die "unknown argument: $arg" \
            "Supported flags: --force."
        ;;
    esac
  done

  if [ -z "${SHELLFORGE_BIN_DIR+set}" ]; then
    SHELLFORGE_BIN_DIR="$HOME/.local/bin"
  fi
  if [ -z "${SHELLFORGE_BASE_URL+set}" ]; then
    SHELLFORGE_BASE_URL="$DEFAULT_BASE_URL"
  fi

  detect_platform
  validate_bin_dir "$SHELLFORGE_BIN_DIR"
  resolve_version

  trap cleanup EXIT
  WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/shellforge-install.XXXXXX")" || \
    die "could not create a temporary directory" \
        "Check that \${TMPDIR:-/tmp} exists and is writable."

  asset="shellforge_${VERSION}_${OS}_${ARCH}.tar.gz"
  archive="$WORKDIR/$asset"
  sums="$WORKDIR/SHA256SUMS"

  fetch "$SHELLFORGE_BASE_URL/$VERSION/$asset" "$archive"
  fetch "$SHELLFORGE_BASE_URL/$VERSION/SHA256SUMS" "$sums"
  verify "$archive" "$sums" "$asset"
  place "$archive" "$SHELLFORGE_BIN_DIR"

  path_advice "$SHELLFORGE_BIN_DIR"
  run_doctor "$SHELLFORGE_BIN_DIR"
}

main "$@"
