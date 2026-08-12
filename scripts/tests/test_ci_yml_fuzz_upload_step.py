#!/usr/bin/env python3
"""Tests for the fuzz-crasher upload step added to `.github/workflows/ci.yml`.

A red fuzz run destroys its own reproducer the moment the runner workspace is
torn down. The step immediately after "Fuzz the OSC parser" uploads the fuzz
corpus directory so a crashing input survives long enough to be added to the
seed corpus. This file pins the step's shape (position, scoping, path) and
its `uses:` pin (SHA plus version comment) so neither drifts unnoticed.

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
FUZZ_STEP_NAME = "Fuzz the OSC parser"


def _test_job_steps() -> list[dict]:
    data = yaml.safe_load(CI_YML.read_text(encoding="utf-8"))
    return data["jobs"]["test"]["steps"]


def _fuzz_step_index(steps: list[dict]) -> int:
    for i, step in enumerate(steps):
        if step.get("name") == FUZZ_STEP_NAME:
            return i
    raise AssertionError(f"no step named {FUZZ_STEP_NAME!r} found")


def test_fuzz_upload_step_exists_and_is_scoped() -> None:
    steps = _test_job_steps()
    fuzz_index = _fuzz_step_index(steps)

    upload_step = None
    for step in steps[fuzz_index + 1 :]:
        name = step.get("name", "")
        if all(word in name.lower() for word in ("upload", "fuzz", "crash")):
            upload_step = step
            break
    assert upload_step is not None, "no upload step found after the fuzz step"

    assert (
        upload_step["with"]["path"] == "internal/pty/testdata/fuzz/FuzzParser/"
    )
    assert upload_step["with"]["if-no-files-found"] == "ignore"

    condition = str(upload_step["if"])
    assert "failure()" in condition
    assert "matrix.os == 'ubuntu-latest'" in condition


def test_upload_artifact_uses_is_pinned_sha_with_version_comment() -> None:
    text = CI_YML.read_text(encoding="utf-8")
    tuples = gates.parse_uses_lines(text)

    matches = [t for t in tuples if t[1].startswith("actions/upload-artifact@")]
    assert len(matches) == 1, matches
    _, ref, comment = matches[0]

    assert gates.check_uses_ref(ref, comment) is None

    _, sha = ref.split("@", 1)
    assert re.fullmatch(r"[0-9a-f]{40}", sha), sha
