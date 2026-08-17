#!/usr/bin/env python3
r"""Tests for the rootfs artifact publishing steps in `.github/workflows/ci.yml`.

Before this ticket, the WSL rootfs tarball was produced three different,
disagreeing ways: CI built it with plain `gzip -9` and threw it away when the
job ended, `make rootfs` wrote the right filename with the right sidecar
shape, and `.\make.ps1 rootfs` wrote the wrong filename, uncompressed, with a
sidecar in the wrong shape. Nothing published a tarball anywhere, so nothing
downstream (the WSL runtime import path, a release page, a learner's own
`wsl --import`) could ever depend on one existing.

This file pins the shape of the fix in `ci.yml`, not the shape of the tarball
itself: the tag trigger that makes the `image` job run on a version tag, the
`contents: write` permission that job needs to attach a release asset, the
upload step's name, path, and `uses:` pin, and the release-attach step's
condition and command. A step that moves, gets renamed, or loses its pin
would otherwise fail silently: the job could still go green while publishing
nothing, which is exactly the failure this ticket exists to close.

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

CI_YML = ROOT / ".github" / "workflows" / "ci.yml"
UPLOAD_STEP_NAME = "Upload rootfs artifacts"


def _workflow() -> dict:
    return yaml.safe_load(CI_YML.read_text(encoding="utf-8"))


def _image_job_steps() -> list[dict]:
    return _workflow()["jobs"]["image"]["steps"]


def test_workflow_runs_on_a_version_tag_push() -> None:
    # A release asset can only attach on a tag push, so the workflow has to
    # trigger on one. Without this, the release-attach step below is dead
    # code: its own `if:` would never see `refs/tags/` in `github.ref`.
    #
    # PyYAML's default (YAML 1.1) resolver reads the bare word `on` as the
    # boolean True, not the string "on", so the trigger block's key in the
    # parsed dict is the Python value True. This is a known YAML workflow
    # file trap, not a typo: looking it up by the string key would raise a
    # KeyError even though the file itself is exactly right.
    data = _workflow()
    on_block = data[True]
    tags = on_block["push"]["tags"]
    assert "v*" in tags, tags


def test_image_job_can_write_release_assets() -> None:
    # The workflow level permission is `contents: read`. Attaching a release
    # asset needs `contents: write`, and only this one job should have it:
    # scoping the elevated permission to the job that needs it, rather than
    # raising it workflow wide, is what keeps every other job's token at
    # read-only.
    data = _workflow()
    assert data["jobs"]["image"]["permissions"]["contents"] == "write"


def test_upload_rootfs_artifacts_step_exists_with_both_files() -> None:
    steps = _image_job_steps()
    upload_step = next(
        (s for s in steps if s.get("name") == UPLOAD_STEP_NAME), None
    )
    assert upload_step is not None, f"no step named {UPLOAD_STEP_NAME!r} found"

    # Whole lines, not substrings: "rootfs.tar.gz" is a substring of
    # "rootfs.tar.gz.sha256", so removing the raw tarball line and leaving
    # only the sidecar would still satisfy an `in` check against the string
    # this way, and the regression this test exists to catch is exactly
    # that: the raw tarball silently stops being published.
    path = upload_step["with"]["path"]
    lines = {line.strip() for line in path.splitlines() if line.strip()}
    assert "rootfs.tar.gz" in lines, lines
    assert "rootfs.tar.gz.sha256" in lines, lines


def test_upload_rootfs_artifacts_uses_the_pinned_upload_artifact_action() -> None:
    # Reuses the exact pin already established for the fuzz-crasher upload
    # step, rather than a second independent one: one Action, one SHA, one
    # place to bump it, and check-ci-gates.py already fails a bare tag pin.
    text = CI_YML.read_text(encoding="utf-8")
    tuples = gates.parse_uses_lines(text)
    matches = [t for t in tuples if t[1].startswith("actions/upload-artifact@")]
    assert matches, "no actions/upload-artifact reference found in ci.yml"

    for _, ref, comment in matches:
        assert gates.check_uses_ref(ref, comment) is None

    refs = {ref for _, ref, _ in matches}
    assert len(refs) == 1, f"actions/upload-artifact is pinned to more than one ref: {refs}"


def test_release_attach_step_runs_after_upload_on_tag_push_only() -> None:
    steps = _image_job_steps()
    upload_index = next(
        i for i, s in enumerate(steps) if s.get("name") == UPLOAD_STEP_NAME
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

    # Not just that both commands appear, but that upload is tried first and
    # create is the fallback: a reordering, an unconditional pair, or `&&`
    # instead of `||` are all broken in a real way (create errors against an
    # existing release; upload can never reach an unconditional create) and
    # two independent `in` checks would not catch any of them.
    run = release_step["run"]
    assert re.search(r"gh release upload.*\|\|\s*\n?\s*gh release create", run, re.DOTALL), run
