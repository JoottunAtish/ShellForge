#!/usr/bin/env python3
"""Tests for `scripts/sync-labels.sh --prune`.

`--prune` is a reporting-only mode: it names GitHub's five auto-created default
labels so a human can confirm each is unused and delete it by hand. It must
never delete anything itself, and this file exists to pin that down as a test
rather than as a promise in a comment. The one assertion that matters most is
that the fake `gh` on PATH never sees the substring "delete" in any argv it is
called with, whether or not `--prune` happens to shell out to `gh` for a
read-only reason.

Run from the repository root:

    python3 -m pytest scripts/tests -q
"""

from __future__ import annotations

import os
import pathlib
import subprocess

import yaml

ROOT = pathlib.Path(__file__).resolve().parents[2]

DEFAULT_LABEL_NAMES = ("bug", "documentation", "enhancement", "invalid", "question")

FAKE_GH = """#!/usr/bin/env bash
# Fake gh for testing scripts/sync-labels.sh --prune. Records every call and
# prints harmless canned output so the real script does not choke if it
# happens to call gh for anything.
echo "$@" >> "$FAKE_GH_LOG"
case "$1 $2" in
  "label list")
    echo '[{"name":"bug"},{"name":"documentation"}]'
    ;;
  *)
    echo "fake-gh: ok"
    ;;
esac
exit 0
"""


def make_fake_gh(tmp_path: pathlib.Path) -> tuple[pathlib.Path, pathlib.Path]:
    """A fake `gh` executable plus the log file it appends its argv to."""
    bin_dir = tmp_path / "fakebin"
    bin_dir.mkdir()
    gh = bin_dir / "gh"
    gh.write_text(FAKE_GH, encoding="utf-8")
    gh.chmod(0o755)
    log = tmp_path / "gh-calls.log"
    return bin_dir, log


def all_label_names_from_taxonomy() -> set[str]:
    labels_yml = ROOT / ".github" / "labels.yml"
    data = yaml.safe_load(labels_yml.read_text(encoding="utf-8"))
    return {entry["name"] for entry in data}


def test_prune_reports_five_labels_without_deleting(tmp_path: pathlib.Path) -> None:
    bin_dir, log = make_fake_gh(tmp_path)

    # Carry the rest of the real environment through, only PATH and the log
    # file location are test-specific.
    full_env = dict(os.environ)
    full_env["PATH"] = f"{bin_dir}:{os.environ['PATH']}"
    full_env["FAKE_GH_LOG"] = str(log)

    result = subprocess.run(
        ["bash", "scripts/sync-labels.sh", "--prune"],
        cwd=ROOT,
        env=full_env,
        capture_output=True,
        text=True,
    )

    assert result.returncode == 0, (result.stdout, result.stderr)

    for name in DEFAULT_LABEL_NAMES:
        assert name in result.stdout, result.stdout

    other_names = all_label_names_from_taxonomy() - set(DEFAULT_LABEL_NAMES)
    for name in other_names:
        assert name not in result.stdout, (name, result.stdout)

    if log.exists():
        log_text = log.read_text(encoding="utf-8")
        assert "delete" not in log_text.lower(), log_text
