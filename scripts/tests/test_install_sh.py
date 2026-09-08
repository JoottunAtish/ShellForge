#!/usr/bin/env python3
r"""Tests for `scripts/install.sh`.

`install.sh` is a POSIX `/bin/sh` script, meant to be piped into `sh` from a
`curl` one liner. It downloads a release archive, verifies its sha256
against `SHA256SUMS` BEFORE placing anything, then moves the binary into
place. Verification happens before placement, never after: an installer
that verifies after placing is not verifying.

Nothing here reaches the network. `SHELLFORGE_BASE_URL` is pointed at a
`file://` fixture root built at test run time by `_build_fixture_release`,
so `curl -fsSL` and `wget -qO-` exercise the real fetch path against a
fixture instead. `CLAUDE.md` forbids committing a binary or an archive, so
the fixture on disk (`scripts/tests/fixtures/install/`) holds only plain
text; the archives, and both `SHA256SUMS` files, are built here, at run
time, so they can never go stale against the real release layout.

Follows the structure of `scripts/tests/test_check_cli_package.py`: a
`tmp_path` fixture, the real script run as a subprocess, assertions on exit
code and captured output, and a post-condition that nothing outside the
target directory changed.

Every refusal test snapshots `$HOME` before and after with
`_snapshot_home` and asserts it is byte for byte identical. That is the
half of each refusal that actually protects the learner: a message alone
proves nothing if the filesystem changed anyway.

Run from the repository root:

    python3 -m pytest scripts/tests -q
"""

from __future__ import annotations

import hashlib
import io
import os
import pathlib
import subprocess
import tarfile
import zipfile

ROOT = pathlib.Path(__file__).resolve().parents[2]
INSTALL_SH = ROOT / "scripts" / "install.sh"
FIXTURE_DIR = ROOT / "scripts" / "tests" / "fixtures" / "install"
FAKE_BIN = FIXTURE_DIR / "fake-shellforge"

DEFAULT_VERSION = "v0.1.0-test"

# The same fragments release.yml, install.sh, and install.ps1 must agree on.
# Kept in one place so a rename anywhere shows up here rather than as a
# mysterious download-not-found failure.
ASSET_NAMES = {
    "linux_amd64": "shellforge_{v}_linux_amd64.tar.gz",
    "linux_arm64": "shellforge_{v}_linux_arm64.tar.gz",
    "windows_amd64": "shellforge_{v}_windows_amd64.zip",
}


def _build_fixture_release(
    tmp_path: pathlib.Path, version: str = DEFAULT_VERSION
) -> pathlib.Path:
    """Build a fake release, plain archives and all, under tmp_path.

    Returns the download root: the directory a real GitHub release's
    "download" URL segment would point at, one level above the version
    directory. `install.sh` composes `$SHELLFORGE_BASE_URL/$VERSION/$ASSET`,
    so the caller is expected to point `SHELLFORGE_BASE_URL` at exactly what
    this function returns.
    """
    download_root = tmp_path / "release" / "download"
    version_dir = download_root / version
    version_dir.mkdir(parents=True, exist_ok=True)

    fake_bin_bytes = FAKE_BIN.read_bytes()

    def _make_tar(name: str, arcname: str) -> pathlib.Path:
        path = version_dir / name
        with tarfile.open(path, "w:gz") as tar:
            info = tarfile.TarInfo(arcname)
            info.size = len(fake_bin_bytes)
            info.mode = 0o755
            tar.addfile(info, io.BytesIO(fake_bin_bytes))
        return path

    def _make_zip(name: str, arcname: str) -> pathlib.Path:
        path = version_dir / name
        with zipfile.ZipFile(path, "w") as zf:
            zi = zipfile.ZipInfo(arcname)
            zi.external_attr = (0o755 & 0xFFFF) << 16
            zf.writestr(zi, fake_bin_bytes)
        return path

    linux_amd64 = _make_tar(ASSET_NAMES["linux_amd64"].format(v=version), "shellforge")
    linux_arm64 = _make_tar(ASSET_NAMES["linux_arm64"].format(v=version), "shellforge")
    windows_amd64 = _make_zip(
        ASSET_NAMES["windows_amd64"].format(v=version), "shellforge.exe"
    )

    rootfs = version_dir / "rootfs.tar.gz"
    rootfs_text = b"fixture rootfs placeholder, one line, not a real image\n"
    with tarfile.open(rootfs, "w:gz") as tar:
        info = tarfile.TarInfo("rootfs-marker.txt")
        info.size = len(rootfs_text)
        tar.addfile(info, io.BytesIO(rootfs_text))
    rootfs_digest = hashlib.sha256(rootfs.read_bytes()).hexdigest()
    (version_dir / "rootfs.tar.gz.sha256").write_text(
        f"{rootfs_digest}  rootfs.tar.gz\n", encoding="utf-8"
    )

    # Order matters: the linux_amd64 entry must come first, because the
    # checksum mismatch tests flip that line's digest and this test machine
    # is x86_64.
    entries = [
        (linux_amd64.name, linux_amd64),
        (linux_arm64.name, linux_arm64),
        (windows_amd64.name, windows_amd64),
        ("rootfs.tar.gz", rootfs),
    ]
    lines = []
    for asset_name, path in entries:
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        lines.append(f"{digest}  {asset_name}")
    (version_dir / "SHA256SUMS").write_text("\n".join(lines) + "\n", encoding="utf-8")

    # SHA256SUMS.wrong: the same lines, with the first (linux_amd64) digest
    # flipped by one hex character, so the mismatch is a genuinely wrong
    # digest rather than one that merely happens not to match.
    wrong_lines = list(lines)
    digest, _, rest = wrong_lines[0].partition("  ")
    flipped = "0" if digest[-1] != "0" else "1"
    wrong_lines[0] = f"{digest[:-1]}{flipped}  {rest}"
    (version_dir / "SHA256SUMS.wrong").write_text(
        "\n".join(wrong_lines) + "\n", encoding="utf-8"
    )

    return download_root


def _run_install(
    tmp_path: pathlib.Path,
    env: dict[str, str | None] | None = None,
    args: tuple[str, ...] = (),
    version: str = DEFAULT_VERSION,
) -> subprocess.CompletedProcess[str]:
    """Run the real install.sh under `sh`, never `bash`.

    Running it under `sh` is what proves the script is POSIX. `HOME` is a
    fresh directory under `tmp_path`, `SHELLFORGE_BASE_URL` points at the
    `file://` fixture root under `tmp_path`, and `TMPDIR` is a fresh
    directory under `tmp_path` too, so every `mktemp -d` the script makes is
    inspectable afterward instead of landing in the real /tmp.
    """
    home = tmp_path / "home"
    home.mkdir(parents=True, exist_ok=True)
    tmp_root = tmp_path / "tmp"
    tmp_root.mkdir(parents=True, exist_ok=True)
    download_root = tmp_path / "release" / "download"

    full_env: dict[str, str] = {
        "HOME": str(home),
        "PATH": os.environ.get("PATH", "/usr/bin:/bin"),
        "TMPDIR": str(tmp_root),
        "SHELLFORGE_VERSION": version,
        "SHELLFORGE_BASE_URL": download_root.resolve().as_uri(),
    }
    if env:
        for key, value in env.items():
            if value is None:
                full_env.pop(key, None)
            else:
                full_env[key] = value

    return subprocess.run(
        ["sh", str(INSTALL_SH), *args],
        env=full_env,
        cwd=str(tmp_path),
        capture_output=True,
        text=True,
        timeout=60,
    )


def _snapshot_home(home: pathlib.Path) -> dict[str, tuple[int, int, str]]:
    """path (relative to home) -> (size, mtime_ns, digest) for every file.

    A symlink is recorded by its target rather than followed, so a refusal
    that is supposed to leave a planted symlink untouched is caught even
    though the symlink itself has no meaningful "size".
    """
    snapshot: dict[str, tuple[int, int, str]] = {}
    if not home.exists():
        return snapshot
    for path in sorted(home.rglob("*")):
        try:
            lstat = path.lstat()
        except OSError:
            continue
        rel = str(path.relative_to(home))
        if path.is_symlink():
            snapshot[rel] = (0, lstat.st_mtime_ns, f"symlink:{os.readlink(path)}")
        elif path.is_file():
            digest = hashlib.sha256(path.read_bytes()).hexdigest()
            snapshot[rel] = (lstat.st_size, lstat.st_mtime_ns, digest)
    return snapshot


def _fake_uname_dir(
    tmp_path: pathlib.Path, os_name: str, arch_name: str, label: str
) -> pathlib.Path:
    """A directory holding a fake `uname` that reports a chosen OS and CPU.

    Prepending this to PATH lets a test simulate an unsupported platform
    (an arch `uname -m` never reports on the real test runner, or a
    Darwin `uname -s`) without needing to run on that platform at all.
    """
    fake_dir = tmp_path / f"fakebin-{label}"
    fake_dir.mkdir(parents=True, exist_ok=True)
    script = fake_dir / "uname"
    script.write_text(
        "#!/bin/sh\n"
        'case "$1" in\n'
        f"  -s) echo '{os_name}' ;;\n"
        f"  -m) echo '{arch_name}' ;;\n"
        "  *) echo unknown ;;\n"
        "esac\n",
        encoding="utf-8",
    )
    script.chmod(0o755)
    return fake_dir


def _bin_path(tmp_path: pathlib.Path) -> pathlib.Path:
    return tmp_path / "home" / ".local" / "bin" / "shellforge"


# ---------------------------------------------------------------------------
# Happy path
# ---------------------------------------------------------------------------


def test_happy_path_installs_and_reports_the_version(tmp_path: pathlib.Path) -> None:
    _build_fixture_release(tmp_path)

    result = _run_install(tmp_path)

    assert result.returncode == 0, result.stderr
    bin_path = _bin_path(tmp_path)
    assert bin_path.is_file()
    assert os.access(bin_path, os.X_OK)

    version_result = subprocess.run(
        [str(bin_path), "version"], capture_output=True, text=True, timeout=10
    )
    assert version_result.returncode == 0
    assert "shellforge v0.1.0-test" in version_result.stdout


def test_happy_path_never_touches_a_shell_profile(tmp_path: pathlib.Path) -> None:
    _build_fixture_release(tmp_path)
    home = tmp_path / "home"
    home.mkdir(parents=True, exist_ok=True)
    profiles = {
        name: f"# original {name}, not to be touched\n"
        for name in (".bashrc", ".profile", ".zshrc")
    }
    for name, content in profiles.items():
        (home / name).write_text(content, encoding="utf-8")

    result = _run_install(tmp_path)

    assert result.returncode == 0, result.stderr
    for name, content in profiles.items():
        assert (home / name).read_text(encoding="utf-8") == content, name

    combined = result.stdout + result.stderr
    assert "export PATH=" in combined


def test_nothing_outside_the_target_directory_was_written(
    tmp_path: pathlib.Path,
) -> None:
    _build_fixture_release(tmp_path)
    home = tmp_path / "home"
    before = _snapshot_home(home)

    result = _run_install(tmp_path)

    assert result.returncode == 0, result.stderr
    after = _snapshot_home(home)

    removed = set(before) - set(after)
    assert removed == set(), removed

    changed_or_new = {rel for rel in after if after.get(rel) != before.get(rel)}
    assert changed_or_new, "expected the installer to have written something"
    for rel in changed_or_new:
        assert rel.startswith(os.path.join(".local", "bin")), rel


def test_a_failing_doctor_does_not_fail_the_install(tmp_path: pathlib.Path) -> None:
    _build_fixture_release(tmp_path)

    result = _run_install(tmp_path, env={"FAKE_SHELLFORGE_DOCTOR_EXIT": "1"})

    assert result.returncode == 0, result.stderr
    assert _bin_path(tmp_path).is_file()
    combined = (result.stdout + result.stderr).lower()
    assert "doctor" in combined


def test_existing_file_at_the_target_is_not_overwritten_without_force(
    tmp_path: pathlib.Path,
) -> None:
    _build_fixture_release(tmp_path)
    bin_dir = tmp_path / "home" / ".local" / "bin"
    bin_dir.mkdir(parents=True)
    target = bin_dir / "shellforge"
    original = b"not a real shellforge binary\n"
    target.write_bytes(original)

    result = _run_install(tmp_path)

    assert result.returncode != 0
    assert target.read_bytes() == original
    combined = result.stdout + result.stderr
    assert "--force" in combined


def test_force_overwrites_the_existing_file(tmp_path: pathlib.Path) -> None:
    _build_fixture_release(tmp_path)
    bin_dir = tmp_path / "home" / ".local" / "bin"
    bin_dir.mkdir(parents=True)
    target = bin_dir / "shellforge"
    target.write_bytes(b"not a real shellforge binary\n")

    result = _run_install(tmp_path, args=("--force",))

    assert result.returncode == 0, result.stderr
    assert target.read_bytes() != b"not a real shellforge binary\n"
    version_result = subprocess.run(
        [str(target), "version"], capture_output=True, text=True, timeout=10
    )
    assert "shellforge v0.1.0-test" in version_result.stdout


# ---------------------------------------------------------------------------
# Checksum verification
# ---------------------------------------------------------------------------


def test_checksum_mismatch_installs_nothing_and_prints_both_digests(
    tmp_path: pathlib.Path,
) -> None:
    download_root = _build_fixture_release(tmp_path)
    version_dir = download_root / DEFAULT_VERSION
    correct = (version_dir / "SHA256SUMS").read_text(encoding="utf-8")
    wrong = (version_dir / "SHA256SUMS.wrong").read_text(encoding="utf-8")
    assert correct != wrong
    # Serve the wrong sums file under the real SHA256SUMS name: this is what
    # "point at SHA256SUMS.wrong" means for a script that always asks for a
    # file named exactly SHA256SUMS.
    (version_dir / "SHA256SUMS").write_text(wrong, encoding="utf-8")

    home = tmp_path / "home"
    before = _snapshot_home(home)
    result = _run_install(tmp_path)
    after = _snapshot_home(home)

    assert result.returncode != 0
    assert not _bin_path(tmp_path).exists()
    assert before == after

    combined = result.stdout + result.stderr
    expected_digest = next(
        line for line in wrong.splitlines() if "linux_amd64" in line
    ).split()[0]
    actual_digest = next(
        line for line in correct.splitlines() if "linux_amd64" in line
    ).split()[0]
    assert expected_digest in combined
    assert actual_digest in combined


def test_checksum_mismatch_removes_its_temporary_directory(
    tmp_path: pathlib.Path,
) -> None:
    download_root = _build_fixture_release(tmp_path)
    version_dir = download_root / DEFAULT_VERSION
    wrong = (version_dir / "SHA256SUMS.wrong").read_text(encoding="utf-8")
    (version_dir / "SHA256SUMS").write_text(wrong, encoding="utf-8")

    tmp_root = tmp_path / "tmp"
    tmp_root.mkdir(parents=True, exist_ok=True)

    result = _run_install(tmp_path)

    assert result.returncode != 0
    combined = (result.stdout + result.stderr).lower()
    assert "checksum" in combined
    leftover = list(tmp_root.glob("shellforge-install.*"))
    assert leftover == [], leftover


# ---------------------------------------------------------------------------
# Platform detection
# ---------------------------------------------------------------------------


def test_unsupported_arch_refuses_by_name(tmp_path: pathlib.Path) -> None:
    fake_dir = _fake_uname_dir(tmp_path, "Linux", "mips64", "arch")
    home = tmp_path / "home"
    before = _snapshot_home(home)

    result = _run_install(
        tmp_path,
        env={"PATH": f"{fake_dir}:{os.environ.get('PATH', '/usr/bin:/bin')}"},
    )
    after = _snapshot_home(home)

    assert result.returncode != 0
    assert before == after
    combined = (result.stdout + result.stderr).lower()
    assert "mips64" in combined


def test_macos_refuses_and_says_v01_ships_linux_and_windows(
    tmp_path: pathlib.Path,
) -> None:
    fake_dir = _fake_uname_dir(tmp_path, "Darwin", "arm64", "darwin")
    home = tmp_path / "home"
    before = _snapshot_home(home)

    result = _run_install(
        tmp_path,
        env={"PATH": f"{fake_dir}:{os.environ.get('PATH', '/usr/bin:/bin')}"},
    )
    after = _snapshot_home(home)

    assert result.returncode != 0
    assert before == after
    combined = (result.stdout + result.stderr).lower()
    assert "v0.1" in combined
    assert "linux" in combined
    assert "windows" in combined


# ---------------------------------------------------------------------------
# Version resolution
# ---------------------------------------------------------------------------


def test_a_custom_base_url_without_a_version_refuses(tmp_path: pathlib.Path) -> None:
    home = tmp_path / "home"
    before = _snapshot_home(home)

    # _run_install always points SHELLFORGE_BASE_URL at the fixture root,
    # which is already a custom value; leaving SHELLFORGE_VERSION empty is
    # what must trigger the refusal.
    result = _run_install(tmp_path, env={"SHELLFORGE_VERSION": ""})
    after = _snapshot_home(home)

    assert result.returncode != 0
    assert before == after
    combined = result.stdout + result.stderr
    assert "SHELLFORGE_VERSION" in combined


# ---------------------------------------------------------------------------
# validate_bin_dir refusals
# ---------------------------------------------------------------------------


def test_refuses_an_empty_bin_dir(tmp_path: pathlib.Path) -> None:
    home = tmp_path / "home"
    before = _snapshot_home(home)

    result = _run_install(tmp_path, env={"SHELLFORGE_BIN_DIR": ""})
    after = _snapshot_home(home)

    assert result.returncode != 0
    assert before == after
    assert "empty" in (result.stdout + result.stderr).lower()


def test_refuses_the_filesystem_root_as_bin_dir(tmp_path: pathlib.Path) -> None:
    home = tmp_path / "home"
    before = _snapshot_home(home)

    result = _run_install(tmp_path, env={"SHELLFORGE_BIN_DIR": "/"})
    after = _snapshot_home(home)

    assert result.returncode != 0
    assert before == after
    assert "root" in (result.stdout + result.stderr).lower()


def test_refuses_a_literal_tilde_as_bin_dir(tmp_path: pathlib.Path) -> None:
    home = tmp_path / "home"
    before = _snapshot_home(home)

    result = _run_install(tmp_path, env={"SHELLFORGE_BIN_DIR": "~"})
    after = _snapshot_home(home)

    assert result.returncode != 0
    assert before == after
    assert "~" in (result.stdout + result.stderr)


def test_refuses_home_itself_as_bin_dir(tmp_path: pathlib.Path) -> None:
    home = tmp_path / "home"
    home.mkdir(parents=True, exist_ok=True)
    before = _snapshot_home(home)

    result = _run_install(tmp_path, env={"SHELLFORGE_BIN_DIR": str(home)})
    after = _snapshot_home(home)

    assert result.returncode != 0
    assert before == after
    assert "home directory" in (result.stdout + result.stderr).lower()


def test_refuses_a_bin_dir_containing_a_dot_dot_segment(
    tmp_path: pathlib.Path,
) -> None:
    home = tmp_path / "home"
    home.mkdir(parents=True, exist_ok=True)
    before = _snapshot_home(home)
    bad = str(home / "foo" / ".." / "bar")

    result = _run_install(tmp_path, env={"SHELLFORGE_BIN_DIR": bad})
    after = _snapshot_home(home)

    assert result.returncode != 0
    assert before == after
    assert ".." in (result.stdout + result.stderr)


def test_refuses_a_relative_bin_dir(tmp_path: pathlib.Path) -> None:
    home = tmp_path / "home"
    before = _snapshot_home(home)

    result = _run_install(tmp_path, env={"SHELLFORGE_BIN_DIR": "relative/bin"})
    after = _snapshot_home(home)

    assert result.returncode != 0
    assert before == after
    assert "absolute" in (result.stdout + result.stderr).lower()


def test_refuses_a_symlinked_target_path(tmp_path: pathlib.Path) -> None:
    _build_fixture_release(tmp_path)
    home = tmp_path / "home"
    bin_dir = home / ".local" / "bin"
    bin_dir.mkdir(parents=True)
    elsewhere = tmp_path / "elsewhere"
    elsewhere.mkdir()
    victim = elsewhere / "not-shellforge"
    victim.write_text("bogus content that must survive\n", encoding="utf-8")
    target = bin_dir / "shellforge"
    target.symlink_to(victim)
    before_victim = victim.read_bytes()

    # Refused even with --force: writing through a symlink can land the
    # binary somewhere the caller did not name.
    result = _run_install(tmp_path, args=("--force",))

    assert result.returncode != 0
    assert target.is_symlink()
    assert victim.read_bytes() == before_victim
    assert "symlink" in (result.stdout + result.stderr).lower()


def test_refuses_an_archive_whose_shellforge_member_is_a_symlink(
    tmp_path: pathlib.Path,
) -> None:
    """A release archive whose `shellforge` member is a symlink to some
    other file must be refused before `chmod` ever runs: `chmod 0755` on a
    symlink follows it and changes the permission bits of whatever it
    points to, and `mv` would then place a symlink to that target on PATH.
    `verify()`'s sha256 check covers the archive as a whole, not what kind
    of filesystem entry the named member inside it is, so a tampered
    release whose SHA256SUMS was generated from the same tampered archive
    still passes verification. This builds exactly that archive: no other
    fixture helper in this file can, because `_build_fixture_release`
    always writes a regular file member.
    """
    version = DEFAULT_VERSION
    download_root = tmp_path / "release" / "download"
    version_dir = download_root / version
    version_dir.mkdir(parents=True, exist_ok=True)

    home = tmp_path / "home"
    home.mkdir(parents=True, exist_ok=True)
    victim = home / "victim.txt"
    victim.write_text("do not touch\n", encoding="utf-8")
    victim.chmod(0o600)
    before_mode = victim.stat().st_mode

    asset_name = ASSET_NAMES["linux_amd64"].format(v=version)
    archive_path = version_dir / asset_name
    with tarfile.open(archive_path, "w:gz") as tar:
        info = tarfile.TarInfo("shellforge")
        info.type = tarfile.SYMTYPE
        info.linkname = str(victim)
        tar.addfile(info)

    digest = hashlib.sha256(archive_path.read_bytes()).hexdigest()
    (version_dir / "SHA256SUMS").write_text(
        f"{digest}  {asset_name}\n", encoding="utf-8"
    )

    result = _run_install(tmp_path)

    assert result.returncode != 0
    combined = (result.stdout + result.stderr).lower()
    assert "not a plain file" in combined
    assert victim.stat().st_mode == before_mode, (
        "chmod followed the symlink and changed the victim's permissions"
    )
    assert not _bin_path(tmp_path).exists()


# ---------------------------------------------------------------------------
# The script itself
# ---------------------------------------------------------------------------


def test_the_script_is_posix_sh_and_names_no_bashism() -> None:
    text = INSTALL_SH.read_text(encoding="utf-8")
    lines = text.splitlines()
    assert lines[0] == "#!/bin/sh", lines[0]
    for bashism in (
        "[[",
        "local ",
        "declare ",
        "echo -e",
        "pipefail",
        "function ",
        "${var,,}",
        "${var^^}",
        "<<<",
    ):
        assert bashism not in text, bashism
