#!/usr/bin/env python3
r"""Tests for the hand rolled release pipeline in `.github/workflows/release.yml`.

The release pipeline is not GoReleaser: it is a small, readable set of shell
steps that build three archives, export the WSL rootfs the same way `make
rootfs` already does, checksum everything once, verify that checksum before
publishing, and only then attach the result to a GitHub Release. This file
pins the shape of that pipeline so a rename, a reorder, or a silently
restated command does not ship assets nobody can verify.

Two tests here (`test_asset_names_match_what_install_sh_constructs` and
`test_asset_names_match_what_install_ps1_constructs`) are a deliberate drift
detector: they cross check the asset name fragments against
`scripts/install.sh` and `scripts/install.ps1`, which do not exist yet in
this part of the ticket. They are expected to be RED until that part lands,
and turning them green is part of that part's own completion criterion. Do
not skip them and do not guard them behind an existence check that hides the
red.

Run from the repository root:

    python3 -m pytest scripts/tests -q
"""

from __future__ import annotations

import importlib.util
import pathlib
import re

import yaml

ROOT = pathlib.Path(__file__).resolve().parents[2]

# The script's filename contains a hyphen, so it is not importable by name. Load
# it by path instead, the same way test_check_ci_gates.py does.
_SCRIPT = ROOT / "scripts" / "check-ci-gates.py"
_spec = importlib.util.spec_from_file_location("check_ci_gates", _SCRIPT)
assert _spec is not None and _spec.loader is not None
gates = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(gates)

RELEASE_YML = ROOT / ".github" / "workflows" / "release.yml"
MAKEFILE = ROOT / "Makefile"
INSTALL_SH = ROOT / "scripts" / "install.sh"
INSTALL_PS1 = ROOT / "scripts" / "install.ps1"

ASSET_TEMPLATES = (
    "shellforge_{v}_linux_amd64.tar.gz",
    "shellforge_{v}_linux_arm64.tar.gz",
    "shellforge_{v}_windows_amd64.zip",
)

# Fragments release.yml, install.sh, and install.ps1 must all agree on. A
# rename in any one of the three fails a test here rather than shipping
# assets an installer cannot find, which is the whole point of this file.
NAME_FRAGMENTS = (
    "shellforge_",
    "_linux_amd64.tar.gz",
    "_linux_arm64.tar.gz",
    "_windows_amd64.zip",
    "SHA256SUMS",
)


def _text() -> str:
    return RELEASE_YML.read_text(encoding="utf-8")


def _strip_comments(text: str, *, block: bool = False) -> str:
    """Strip comment text, so a fragment surviving only in prose does not
    count as the installer still constructing that asset name.

    `block` also strips PowerShell's `<# ... #>` form, used for the whole
    .SYNOPSIS and .DESCRIPTION header at the top of install.ps1, which is
    exactly where the old asset name lived after the real construction line
    stopped spelling it out.
    """
    if block:
        text = re.sub(r"(?s)<#.*?#>", "", text)
    return re.sub(r"(?m)^\s*#.*$", "", text)


def _workflow() -> dict:
    return yaml.safe_load(_text())


def _release_job() -> dict:
    return _workflow()["jobs"]["release"]


def _release_steps() -> list[dict]:
    return _release_job()["steps"]


def _step(name: str) -> dict:
    for step in _release_steps():
        if step.get("name") == name:
            return step
    raise AssertionError(f"no step named {name!r} found in release.yml")


def test_release_workflow_triggers_on_a_version_tag_and_on_dispatch() -> None:
    # PyYAML's default (YAML 1.1) resolver reads the bare word `on` as the
    # boolean True, not the string "on". Same trap documented in
    # test_ci_yml_rootfs_artifact.py.
    data = _workflow()
    on_block = data[True]
    assert "v*" in on_block["push"]["tags"], on_block["push"]["tags"]
    assert "workflow_dispatch" in on_block, on_block


def test_release_job_is_the_only_job_and_holds_contents_write() -> None:
    data = _workflow()
    jobs = data["jobs"]
    assert set(jobs) == {"release"}, jobs
    assert jobs["release"]["permissions"]["contents"] == "write"
    # The workflow level permission stays read-only: only the one job that
    # needs to publish escalates, and it does so explicitly, in its own
    # permissions block rather than by raising the workflow default.
    assert data["permissions"]["contents"] == "read"


def test_every_uses_in_release_yml_is_sha_pinned() -> None:
    text = _text()
    tuples = gates.parse_uses_lines(text)
    assert tuples, "no uses: lines found in release.yml"
    for _, ref, comment in tuples:
        assert gates.check_uses_ref(ref, comment) is None


def test_release_yml_builds_all_three_targets() -> None:
    text = _text()
    for target in ("linux/amd64", "linux/arm64", "windows/amd64"):
        assert target in text, f"{target} not found in release.yml"
    assert "darwin/" not in text.lower()
    assert "goos=darwin" not in text.lower()


def test_release_yml_builds_with_cgo_disabled_and_trimpath() -> None:
    text = _text()
    assert "CGO_ENABLED=0" in text
    assert "-trimpath" in text


def test_release_yml_stamps_the_same_ldflag_variables_as_the_makefile() -> None:
    makefile_text = MAKEFILE.read_text(encoding="utf-8")
    makefile_vars = set(re.findall(r"-X main\.(\w+)=", makefile_text))
    assert makefile_vars, "no -X main.<var> ldflags found in the Makefile"

    release_vars = set(re.findall(r"-X main\.(\w+)=", _text()))
    assert release_vars == makefile_vars, (release_vars, makefile_vars)


def test_release_yml_runs_the_linux_amd64_binary_and_checks_its_tag() -> None:
    # The ldflag test above only proves the same -X main.<var> names are
    # passed here as in the Makefile: a string comparison, not a run. This is
    # the other half the acceptance criterion asks for: that TAG really
    # reaches main.version and the binary really prints it. The runner
    # release.yml runs on is linux/amd64, so the archive it just built can
    # execute right there.
    step = _step("shellforge version on the linux/amd64 artifact names the tag")
    run = step["run"]
    assert "linux_amd64.tar.gz" in run, run
    assert re.search(r"\bversion\b", run), run
    assert "$TAG" in run, run


def test_release_yml_reuses_make_rootfs_rather_than_restating_it() -> None:
    text = _text()
    assert re.search(r"\bmake rootfs\b", text), "release.yml does not call make rootfs"
    assert "docker export" not in text, (
        "release.yml restates the docker export instead of reusing make rootfs"
    )


def test_sha256sums_lists_every_archive_and_not_itself() -> None:
    step = _step("One SHA256SUMS for every archive")
    run = step["run"]
    assert "SHA256SUMS" in run
    for fragment in (
        "linux_amd64.tar.gz",
        "linux_arm64.tar.gz",
        "windows_amd64.zip",
        "rootfs.tar.gz",
    ):
        assert fragment in run, (fragment, run)

    # SHA256SUMS must not be one of the things summed, or the sums file would
    # try to list a digest of itself. Everything before the redirect is what
    # gets summed.
    before_redirect = run.split(">")[0]
    assert "SHA256SUMS" not in before_redirect, run


def test_release_yml_verifies_sha256sums_before_publishing() -> None:
    steps = _release_steps()
    names = [s.get("name", "") for s in steps]

    verify_index = next(
        i
        for i, n in enumerate(names)
        if "verify" in n.lower() and "sha256sums" in n.lower()
    )
    attach_index = next(
        i
        for i, n in enumerate(names)
        if "attach" in n.lower() and "release" in n.lower()
    )
    assert verify_index < attach_index, names

    verify_step = steps[verify_index]
    assert "sha256sum -c SHA256SUMS" in verify_step["run"], verify_step["run"]


def test_release_attach_step_runs_after_upload_on_tag_push_only() -> None:
    steps = _release_steps()
    upload_index = next(
        i
        for i, s in enumerate(steps)
        if s.get("name") == "Upload the release assets as a CI artifact"
    )

    release_step = None
    for step in steps[upload_index + 1 :]:
        if "release" in step.get("name", "").lower():
            release_step = step
            break
    assert release_step is not None, "no release-attach step found after the upload step"

    condition = str(release_step["if"])
    assert "github.event_name == 'push'" in condition
    assert "refs/tags/" in condition

    run = release_step["run"]
    assert re.search(r"gh release upload.*\|\|\s*\n?\s*gh release create", run, re.DOTALL), run


def test_release_yml_refuses_an_empty_tag() -> None:
    # A guard on an empty TAG naming the workflow_dispatch input, rather than
    # building assets named shellforge__linux_amd64.tar.gz.
    step = _step("Resolve the version")
    run = step["run"]
    assert re.search(r'-z\s+"?\$TAG"?', run), run
    assert "exit 1" in run
    assert "tag" in run.lower()


def test_asset_names_match_what_install_sh_constructs() -> None:
    assert INSTALL_SH.is_file(), (
        f"{INSTALL_SH} does not exist yet. This test is the drift detector "
        "part 3 (the installers) must turn green; it is expected RED until "
        "part 3 lands."
    )
    release_text = _text()
    install_code = _strip_comments(INSTALL_SH.read_text(encoding="utf-8"))

    # install.sh builds the Linux asset name from ${OS} and ${ARCH} rather
    # than spelling out "linux_amd64" or "linux_arm64" anywhere in code, so
    # the literal fragments below can never appear there: they only make
    # sense checked against release.yml, which builds concrete archives for
    # concrete platforms. What install.sh must still contain, comments
    # stripped, is the construction line itself. Assert on that directly, or
    # a rename of the archive suffix that leaves the header comment
    # unedited would pass silently, which is the bug this test exists to
    # catch.
    assert "shellforge_" in install_code, "shellforge_ missing from install.sh"
    assert "_${OS}_${ARCH}.tar.gz" in install_code, (
        "install.sh's asset construction line no longer builds "
        "shellforge_${VERSION}_${OS}_${ARCH}.tar.gz. If the archive suffix "
        "or field order changed, release.yml must build a matching name."
    )
    for fragment in NAME_FRAGMENTS:
        assert fragment in release_text, f"{fragment} missing from release.yml"


def test_asset_names_match_what_install_ps1_constructs() -> None:
    assert INSTALL_PS1.is_file(), (
        f"{INSTALL_PS1} does not exist yet. This test is the drift detector "
        "part 3 (the installers) must turn green; it is expected RED until "
        "part 3 lands."
    )
    release_text = _text()
    install_code = _strip_comments(INSTALL_PS1.read_text(encoding="utf-8"), block=True)

    # Unlike install.sh, install.ps1's Windows asset name is not
    # parameterized: "_windows_amd64.zip" is a literal in the real
    # construction line. It is also a literal in the .DESCRIPTION header
    # above it, which is why comments must be stripped first: without that,
    # a rename of the construction line alone would still find the fragment
    # in the stale header and report no drift.
    assert "shellforge_" in install_code, "shellforge_ missing from install.ps1"
    assert "_windows_amd64.zip" in install_code, (
        "install.ps1's asset construction line no longer builds "
        "shellforge_${resolvedVersion}_windows_amd64.zip. If the archive "
        "suffix changed, release.yml must build a matching name."
    )
    for fragment in NAME_FRAGMENTS:
        assert fragment in release_text, f"{fragment} missing from release.yml"


def test_release_yml_builds_no_darwin_target() -> None:
    # A comment explaining why macOS is not built is expected and may
    # legitimately name "darwin" in prose; what must never appear is a
    # darwin build target itself.
    text = _text().lower()
    assert "darwin/" not in text
    assert "goos=darwin" not in text
