#!/usr/bin/env python3
"""Tests for `scripts/check-allowlist-regexp.sh`.

The gate's needle is deliberately unreadable: it is an ERE built so that it
matches the loose, flag-shape-admitting identifier regexp without ever
spelling that regexp out contiguously in the gate script's own tracked text.
See the header of check-allowlist-regexp.sh for why: a copy sitting in a
comment that explains why it is wrong is indistinguishable from a copy
somebody is about to trust, and a gate that contained the exact text it hunts
for would flag itself.

This test file inherits that same constraint, for the same reason, and it
cannot dodge it the way prose can. `internal/runtime/apishape_test.go` keeps
the banned form out of its comment because a comment has a choice: it can
describe the shape in English instead of quoting it, and it loses nothing by
doing so. A test that verifies the gate detects a given string has no such
choice. Proving detection requires a tracked file that actually contains the
banned byte sequence somewhere on disk, or there is nothing for the gate to
find. The constraint this file honors instead is narrower but load-bearing:
that exact sequence must never appear contiguously in *this* file's own
source text, only in the fixture file this test writes into a throwaway git
repository at run time and never commits to ShellForge itself. Every loose
spelling below is therefore assembled with `"".join([...])`, so the source
line is broken up by the commas and quotes of a Python list literal and the
banned run of characters exists only in the joined, in-memory string that
gets written to a fixture file inside a temporary directory.

The gate script derives its own repository root from its own file location
(`BASH_SOURCE[0]`), so it cannot be pointed at a fixture directory through an
environment variable or a flag, and none was added to it for this test's
convenience (that would be a backdoor in a security gate). Instead, each test
builds a real throwaway git repository under pytest's `tmp_path`, copies the
real script into `<tmp>/scripts/check-allowlist-regexp.sh`, writes a fixture
`.claude/skills/security/SKILL.md`, runs `git init` and `git add`, and then
runs the copied script against that repository exactly as CI runs the real
one against ShellForge. `git ls-files` reads the index, and `git add` is
enough to populate it: no commit is needed, and none is made.

Run from the repository root:

    python3 -m pytest scripts/tests -q
"""

from __future__ import annotations

import pathlib
import shutil
import subprocess

import pytest

ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT_SRC = ROOT / "scripts" / "check-allowlist-regexp.sh"

STRICT_FORM = "^[a-zA-Z0-9][a-zA-Z0-9_-]*$"

DEFAULT_SKILL_MD = (
    "# Security\n\n"
    "## Validate before you execute\n\n"
    "Distro names, container names, level IDs and user names go through a "
    f"strict allowlist regexp (`{STRICT_FORM}` for identifiers) before "
    "reaching an argv. Reject, do not sanitize.\n"
)

SKILL_MD_WITHOUT_STRICT_FORM = (
    "# Security\n\n"
    "## Validate before you execute\n\n"
    "Distro names, container names, level IDs and user names are checked "
    "before reaching an argv. Reject, do not sanitize.\n"
)

# Every entry here is a loose, flag-shape-admitting single-class identifier
# pattern: a hyphen sits somewhere in the class (leading, trailing, or
# trailing and backslash-escaped), and the letter ranges may appear in either
# case order. Each is assembled with "".join([...]) so the banned contiguous
# run of characters never appears as a single literal in this source file;
# see the module docstring for why that matters.
LOOSE_SPELLINGS = [
    pytest.param(
        "".join(["^[", "a-zA-Z0-9_-", "]+$"]),
        id="original-trailing-hyphen",
    ),
    pytest.param(
        "".join(["^[", "-a-zA-Z0-9_", "]+$"]),
        id="leading-hyphen",
    ),
    pytest.param(
        "".join(["^[", "a-zA-Z0-9_-", "]", "{1,64}$"]),
        id="brace-quantifier",
    ),
    pytest.param(
        "".join(["^[", "a-zA-Z0-9_", "\\-", "]+$"]),
        id="escaped-trailing-hyphen",
    ),
    pytest.param(
        "".join(["^[", "A-Za-z0-9_-", "]+$"]),
        id="case-order-swapped",
    ),
]


def make_repo(
    tmp_path: pathlib.Path,
    skill_md: str = DEFAULT_SKILL_MD,
    extra_files: dict[str, str] | None = None,
    write_skill_md: bool = True,
) -> pathlib.Path:
    """A throwaway git repository containing a copy of the real gate script.

    `git init` plus `git add` is all a check that reads `git ls-files` needs:
    the index is populated without ever creating a commit.
    """
    repo = tmp_path / "repo"
    scripts_dir = repo / "scripts"
    scripts_dir.mkdir(parents=True)
    copied = scripts_dir / "check-allowlist-regexp.sh"
    shutil.copy(SCRIPT_SRC, copied)
    copied.chmod(0o755)

    if write_skill_md:
        skill_dir = repo / ".claude" / "skills" / "security"
        skill_dir.mkdir(parents=True)
        (skill_dir / "SKILL.md").write_text(skill_md, encoding="utf-8")

    for relpath, content in (extra_files or {}).items():
        path = repo / relpath
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")

    subprocess.run(["git", "init", "-q"], cwd=repo, check=True)
    subprocess.run(["git", "add", "-A"], cwd=repo, check=True)
    return repo


def run_gate(repo: pathlib.Path) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["bash", "scripts/check-allowlist-regexp.sh"],
        cwd=repo,
        capture_output=True,
        text=True,
    )


# ------------------------------------------------------------------ clean tree


def test_clean_tree_exits_zero(tmp_path: pathlib.Path) -> None:
    repo = make_repo(tmp_path)
    result = run_gate(repo)
    assert result.returncode == 0, result.stderr
    assert "consistent" in result.stdout


def test_the_gate_does_not_flag_its_own_copied_text(tmp_path: pathlib.Path) -> None:
    # The fixture repository tracks the gate script itself (it was copied and
    # `git add -A` picked it up), so a clean exit here is also proof that the
    # needle does not match the file it lives in. This is exactly the
    # property the header comment claims: the needle matches the loose form
    # without containing it.
    repo = make_repo(tmp_path)
    result = run_gate(repo)
    assert result.returncode == 0, result.stderr
    combined = result.stdout + result.stderr
    assert "scripts/check-allowlist-regexp.sh" not in combined


# --------------------------------------------------------------- missing skill


def test_missing_skill_md_fails(tmp_path: pathlib.Path) -> None:
    repo = make_repo(tmp_path, write_skill_md=False)
    result = run_gate(repo)
    assert result.returncode != 0


def test_skill_md_without_strict_form_fails(tmp_path: pathlib.Path) -> None:
    repo = make_repo(tmp_path, skill_md=SKILL_MD_WITHOUT_STRICT_FORM)
    result = run_gate(repo)
    assert result.returncode == 1
    assert "no longer quotes" in result.stderr


# --------------------------------------------------------- loose form detected


@pytest.mark.parametrize("spelling", LOOSE_SPELLINGS)
def test_a_loose_spelling_in_a_tracked_file_fails_and_names_it(
    tmp_path: pathlib.Path, spelling: str
) -> None:
    repo = make_repo(
        tmp_path,
        extra_files={"docs/example.md": f"The allowlist regexp is `{spelling}`.\n"},
    )
    result = run_gate(repo)
    assert result.returncode == 1, (result.stdout, result.stderr)
    assert "docs/example.md" in result.stderr


# --------------------------------------------------------------- true negatives


def test_the_strict_form_itself_is_not_flagged(tmp_path: pathlib.Path) -> None:
    repo = make_repo(
        tmp_path,
        extra_files={
            "docs/example.md": f"Identifiers must match `{STRICT_FORM}`.\n"
        },
    )
    result = run_gate(repo)
    assert result.returncode == 0, (result.stdout, result.stderr)


def test_an_unanchored_class_in_prose_is_not_flagged(tmp_path: pathlib.Path) -> None:
    # This is the false positive BLOCKING 3 exists to close: a level briefing
    # teaching character classes shows this exact snippet with no leading `^`
    # anchoring it to the start of a pattern, and it must not trip the gate.
    briefing = 'Try `grep -E "[a-zA-Z0-9_-]+" notes.txt` and see what matches.\n'
    repo = make_repo(tmp_path, extra_files={"packs/example/briefing.md": briefing})
    result = run_gate(repo)
    assert result.returncode == 0, (result.stdout, result.stderr)
