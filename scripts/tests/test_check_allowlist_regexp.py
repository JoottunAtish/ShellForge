#!/usr/bin/env python3
"""Tests for `scripts/check-allowlist-regexp.sh`.

The gate is a shell script, not a Python module, so it is exercised the only way
a shell script can be: as a subprocess, against a throwaway git repository built
fresh in a tmp dir for each test. Nothing here touches the real Shellforge tree
except to read the gate's own source so it can be copied into each fixture.

A gate nobody tests is a gate that can quietly stop scanning, which is exactly
what happened here: the version this file guards against reported "consistent"
and exited 0 after a `git ls-files` failure that meant it never scanned a single
file. See `test_a_non_git_directory_does_not_report_clean` below, which is the
regression test for that finding.

Run from the repository root:

    python3 -m pytest scripts/tests -q
"""

from __future__ import annotations

import pathlib
import shutil
import subprocess

import pytest

ROOT = pathlib.Path(__file__).resolve().parents[2]
GATE_SRC = ROOT / "scripts" / "check-allowlist-regexp.sh"

# A minimal but real quoting of the strict form, in the shape the gate expects to
# find inside the security skill's "Validate before you execute" bullet.
STRICT_SKILL_TEXT = (
    "- **Validate before you execute.** Distro names, container names, level IDs\n"
    "  and user names go through a strict allowlist regexp\n"
    "  (`^[a-zA-Z0-9][a-zA-Z0-9_-]*$` for identifiers) before reaching an argv.\n"
    "  **Reject, do not sanitize.**\n"
)

SKILL_PATH = ".claude/skills/security/SKILL.md"

# The gate scans every tracked file, and this file is one of them, so the loose
# forms are assembled here rather than written out. A literal copy in this
# source would fail the very gate these tests exercise, and the gate would be
# right to fail it: it cannot tell a fixture from a rule somebody is about to
# trust. The bytes written into each fixture on disk are still the real thing,
# so detection is tested against the genuine article.
#
# Do not "simplify" these back into literals. The first version of this file did
# exactly that, passed locally because an untracked file is invisible to
# `git ls-files`, and turned the gate red the moment it was committed.
LOOSE = "^[" + "a-zA-Z" + "0-9_-" + "]" + "+$"
LOOSE_CASE_ORDER_VARIANT = "^[" + "A-Za-z" + "0-9_-" + "]" + "+$"

# The exact three copies of the loose form that issue #19 removed, recovered
# with `git show main:<path> | sed -n <lines>p`. If the anchored needle misses
# any one of these, the anchoring is wrong.
REMOVED_COPY_SKILL_MD = (
    "  user names go through a strict allowlist regexp\n"
    f"  (`{LOOSE}` for identifiers) before reaching an argv. **Reject, do\n"
    "  not sanitize.**\n"
)
REMOVED_COPY_APISHAPE_1 = (
    "// injection, a different failure from shell injection, and the plain\n"
    f"// {LOOSE} form does not close it.\n"
)
REMOVED_COPY_APISHAPE_2 = (
    "// This diverges from the string currently quoted in the ticket and in the\n"
    f"// security skill, both of which still show {LOOSE}.\n"
)

REMOVED_COPIES = [
    pytest.param(REMOVED_COPY_SKILL_MD, id="skill-md-copy"),
    pytest.param(REMOVED_COPY_APISHAPE_1, id="apishape-copy-1"),
    pytest.param(REMOVED_COPY_APISHAPE_2, id="apishape-copy-2"),
]

# A legitimate use of the same character class, with no anchors, teaching a
# learner how to grep for identifier-looking text. This is not the loose
# allowlist regexp and must not be flagged.
CHARACTER_CLASS_TRAP = (
    'a level briefing: try grep -E "[a-zA-Z0-9_-]+" logs/*.txt\n'
)


def make_repo(
    tmp_path: pathlib.Path,
    files: dict[str, str] | None = None,
    *,
    skill_text: str | None = STRICT_SKILL_TEXT,
    git_init: bool = True,
) -> pathlib.Path:
    """Build a throwaway repo with the real gate script copied in.

    `skill_text` is written to `.claude/skills/security/SKILL.md` unless it is
    `None`, in which case the file is simply not created. `files` adds any other
    tracked files, keyed by path relative to the repo root. `git_init=False`
    builds the same directory tree but skips `git init`, for the one test that
    needs a directory the gate cannot treat as a repository.
    """
    repo = tmp_path / "repo"
    repo.mkdir()

    if git_init:
        subprocess.run(["git", "init", "-q"], cwd=repo, check=True)

    script_dst = repo / "scripts" / "check-allowlist-regexp.sh"
    script_dst.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(GATE_SRC, script_dst)
    script_dst.chmod(0o755)

    if skill_text is not None:
        skill_dst = repo / SKILL_PATH
        skill_dst.parent.mkdir(parents=True, exist_ok=True)
        skill_dst.write_text(skill_text, encoding="utf-8")

    for relpath, content in (files or {}).items():
        p = repo / relpath
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(content, encoding="utf-8")

    if git_init:
        subprocess.run(["git", "add", "-A"], cwd=repo, check=True)

    return repo


def run_gate(repo: pathlib.Path) -> subprocess.CompletedProcess[str]:
    script = repo / "scripts" / "check-allowlist-regexp.sh"
    return subprocess.run(
        ["bash", str(script)],
        cwd=repo,
        capture_output=True,
        text=True,
    )


# ------------------------------------------------------------------ content hits


def test_a_tracked_loose_form_fails_and_names_the_file(tmp_path: pathlib.Path) -> None:
    repo = make_repo(
        tmp_path,
        files={"docs/note.md": "old pattern: " + REMOVED_COPY_SKILL_MD},
    )
    result = run_gate(repo)
    assert result.returncode == 1, result.stderr
    assert "docs/note.md" in result.stderr


def test_a_clean_tree_passes(tmp_path: pathlib.Path) -> None:
    repo = make_repo(tmp_path)
    result = run_gate(repo)
    assert result.returncode == 0, result.stderr
    assert "consistent" in result.stdout


def test_skill_missing_the_strict_form_fails(tmp_path: pathlib.Path) -> None:
    repo = make_repo(tmp_path, skill_text="nothing about an allowlist here.\n")
    result = run_gate(repo)
    assert result.returncode == 1, result.stderr
    assert "no longer quotes" in result.stderr


def test_a_missing_skill_file_fails(tmp_path: pathlib.Path) -> None:
    repo = make_repo(tmp_path, skill_text=None)
    result = run_gate(repo)
    assert result.returncode == 1, result.stderr
    assert "is missing" in result.stderr


def test_the_gate_does_not_flag_its_own_text(tmp_path: pathlib.Path) -> None:
    # The needle is built so it cannot contain the string it hunts. Prove that
    # by scanning the real script's own text, copied into the fixture repo
    # exactly as it ships, alongside a valid skill file.
    repo = make_repo(tmp_path, files={"scripts/copy-under-test.sh": GATE_SRC.read_text(encoding="utf-8")})
    result = run_gate(repo)
    assert result.returncode == 0, result.stderr
    assert "consistent" in result.stdout


# ------------------------------------------------------------- the B1 regression


def test_a_non_git_directory_does_not_report_clean(tmp_path: pathlib.Path) -> None:
    # Before the fix, `git ls-files` failing here (exit 128, no such repository)
    # was swallowed by `|| true`, so the scan silently covered zero files and the
    # gate printed "allowlist regexp: consistent" and exited 0. That is a false
    # green on a security gate: it must fail loudly instead, and it must not
    # claim to be clean.
    repo = make_repo(tmp_path, git_init=False)
    result = run_gate(repo)
    assert result.returncode != 0, result.stdout
    assert "consistent" not in result.stdout
    assert "could not run" in result.stderr


# --------------------------------------------------------------- anchoring proof


@pytest.mark.parametrize("loose_text", REMOVED_COPIES)
def test_anchoring_still_catches_the_removed_copies(
    tmp_path: pathlib.Path, loose_text: str
) -> None:
    repo = make_repo(tmp_path, files={"fixture.txt": loose_text})
    result = run_gate(repo)
    assert result.returncode == 1, result.stderr
    assert "fixture.txt" in result.stderr


def test_the_character_class_trap_is_not_flagged(tmp_path: pathlib.Path) -> None:
    # `[a-zA-Z0-9_-]+` with no anchors is an ordinary regexp lesson, not the
    # allowlist pattern. Before anchoring, the bare-substring needle flagged
    # this line, which would have sent a level author to fix a correct file.
    repo = make_repo(tmp_path, files={"packs/core-linux-basics/levels/grep-01.md": CHARACTER_CLASS_TRAP})
    result = run_gate(repo)
    assert result.returncode == 0, result.stderr
    assert "consistent" in result.stdout


def test_the_case_order_variant_is_caught(tmp_path: pathlib.Path) -> None:
    # A reviewer planted the same class with the letter ranges reordered, and
    # the original needle missed it. The widened needle must catch that exact
    # spelling too. See LOOSE_CASE_ORDER_VARIANT above for why it is assembled
    # rather than written out.
    repo = make_repo(tmp_path, files={"fixture.txt": LOOSE_CASE_ORDER_VARIANT + "\n"})
    result = run_gate(repo)
    assert result.returncode == 1, result.stderr
    assert "fixture.txt" in result.stderr
