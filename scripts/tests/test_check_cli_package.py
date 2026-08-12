"""Tests for `scripts/check-cli-package.sh`.

Issue #11 found that the whole `cmd/shellforge` package had never been
committed: `.gitignore` line 5 was an unanchored `shellforge` pattern, which
matches any path component named `shellforge` at any depth, including the
directory itself. `git ls-files cmd/shellforge/` returned nothing, and
because CI builds with `go build ./...` and tests with `go test ./...`, both
of which only operate on packages present in the checkout, a missing package
was invisible to them rather than an error. PR #72 fixed the `.gitignore`
cause and committed the three missing files. This test file proves the gate
that would have caught it, reproducing the #11 defect exactly as one of its
cases.

Follows the structure of `scripts/tests/test_check_allowlist_regexp.py`:
each case builds a throwaway git repository under pytest's `tmp_path`,
copies the real script in, `git init` plus `git add` to populate the index
(no commit needed), runs the script as a subprocess, and asserts on the
exit code and stdout or stderr.

Run from the repository root:

    python3 -m pytest scripts/tests -q
"""

from __future__ import annotations

import pathlib
import shutil
import subprocess

import pytest

ROOT = pathlib.Path(__file__).resolve().parents[2]
SCRIPT_SRC = ROOT / "scripts" / "check-cli-package.sh"

MAIN_GO_WITH_MAIN = (
    "package main\n\n"
    'import "fmt"\n\n'
    "func main() {\n"
    '\tfmt.Println("shellforge")\n'
    "}\n"
)

MAIN_GO_WITHOUT_MAIN = (
    "package main\n\n"
    'import "fmt"\n\n'
    "func run() {\n"
    '\tfmt.Println("shellforge")\n'
    "}\n"
)


def make_repo(
    tmp_path: pathlib.Path,
    files: dict[str, str] | None = None,
    gitignore: str | None = None,
    add_to_index: bool = True,
    init_git: bool = True,
) -> pathlib.Path:
    """A throwaway git repository containing a copy of the real gate script.

    `git init` plus `git add` is all a check that reads `git ls-files` needs:
    the index is populated without ever creating a commit. `add_to_index`
    lets a case leave a file on disk but out of the index, which is the
    literal shape of the #11 defect: present on disk, absent from
    `git ls-files`.
    """
    repo = tmp_path / "repo"
    scripts_dir = repo / "scripts"
    scripts_dir.mkdir(parents=True)
    copied = scripts_dir / "check-cli-package.sh"
    shutil.copy(SCRIPT_SRC, copied)
    copied.chmod(0o755)

    for relpath, content in (files or {}).items():
        path = repo / relpath
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")

    if gitignore is not None:
        (repo / ".gitignore").write_text(gitignore, encoding="utf-8")

    if init_git:
        subprocess.run(["git", "init", "-q"], cwd=repo, check=True)
        subprocess.run(
            ["git", "config", "user.email", "test@example.com"], cwd=repo, check=True
        )
        subprocess.run(["git", "config", "user.name", "Test"], cwd=repo, check=True)
        if add_to_index:
            subprocess.run(["git", "add", "-A"], cwd=repo, check=True)

    return repo


def run_gate(repo: pathlib.Path) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["bash", "scripts/check-cli-package.sh"],
        cwd=repo,
        capture_output=True,
        text=True,
    )


# ------------------------------------------------------------------ clean tree


def test_clean_repo_exits_zero(tmp_path: pathlib.Path) -> None:
    repo = make_repo(tmp_path, files={"cmd/shellforge/main.go": MAIN_GO_WITH_MAIN})
    result = run_gate(repo)
    assert result.returncode == 0, (result.stdout, result.stderr)
    assert "OK:" in result.stdout


def test_whole_repository_assertion(tmp_path: pathlib.Path) -> None:
    # Runs the real script against the real repository root, with no
    # fixture at all. This is what makes `make lint` meaningful: the
    # repository as it stands (cmd/shellforge present, tracked, .gitignore
    # anchored since PR #72) must pass.
    result = subprocess.run(
        ["bash", "scripts/check-cli-package.sh"],
        cwd=ROOT,
        capture_output=True,
        text=True,
    )
    assert result.returncode == 0, (result.stdout, result.stderr)


# --------------------------------------------------------------- untracked file


def test_untracked_go_file_under_cmd_fails_and_names_it(tmp_path: pathlib.Path) -> None:
    # The #11 reproduction: a .go file present on disk but never `git add`ed.
    repo = make_repo(
        tmp_path,
        files={"cmd/shellforge/main.go": MAIN_GO_WITH_MAIN},
        add_to_index=False,
    )
    result = run_gate(repo)
    assert result.returncode != 0
    assert "cmd/shellforge/main.go" in (result.stdout + result.stderr)


# ----------------------------------------------------------------- ignored file


def test_ignored_go_file_fails_and_names_file_and_rule(tmp_path: pathlib.Path) -> None:
    # The actual #11 defect, reproduced exactly: an unanchored `shellforge`
    # gitignore line matches the cmd/shellforge directory at any depth, so
    # the file exists on disk and git would ignore it. This is the single
    # most important case in this file.
    repo = make_repo(
        tmp_path,
        files={"cmd/shellforge/main.go": MAIN_GO_WITH_MAIN},
        gitignore="shellforge\n",
        add_to_index=False,
    )
    result = run_gate(repo)
    assert result.returncode != 0
    combined = result.stdout + result.stderr
    assert "cmd/shellforge/main.go" in combined
    assert "shellforge" in combined


def test_anchored_pattern_passes(tmp_path: pathlib.Path) -> None:
    # Proves the gate does not flag the actual PR #72 fix: an anchored
    # pattern only matches a path at the repository root, not
    # cmd/shellforge.
    repo = make_repo(
        tmp_path,
        files={"cmd/shellforge/main.go": MAIN_GO_WITH_MAIN},
        gitignore="/shellforge\n",
    )
    result = run_gate(repo)
    assert result.returncode == 0, (result.stdout, result.stderr)


# ------------------------------------------------------------- missing package


def test_missing_cmd_shellforge_fails(tmp_path: pathlib.Path) -> None:
    repo = make_repo(tmp_path, files={"README.md": "placeholder\n"})
    result = run_gate(repo)
    assert result.returncode != 0


def test_empty_cmd_shellforge_fails(tmp_path: pathlib.Path) -> None:
    repo = make_repo(tmp_path, files={"cmd/shellforge/.gitkeep": ""})
    result = run_gate(repo)
    assert result.returncode != 0


# ------------------------------------------------------------------ missing main


def test_missing_func_main_fails(tmp_path: pathlib.Path) -> None:
    repo = make_repo(
        tmp_path, files={"cmd/shellforge/main.go": MAIN_GO_WITHOUT_MAIN}
    )
    result = run_gate(repo)
    assert result.returncode != 0


# ------------------------------------------------------------------- no git repo


def test_outside_a_git_repo_fails_rather_than_reporting_clean(
    tmp_path: pathlib.Path,
) -> None:
    repo = make_repo(
        tmp_path,
        files={"cmd/shellforge/main.go": MAIN_GO_WITH_MAIN},
        init_git=False,
    )
    result = run_gate(repo)
    assert result.returncode != 0
    assert "OK:" not in result.stdout
