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

import os
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
    force_add: list[str] | None = None,
) -> pathlib.Path:
    """A throwaway git repository containing a copy of the real gate script.

    `git init` plus `git add` is all a check that reads `git ls-files` needs:
    the index is populated without ever creating a commit. `add_to_index`
    lets a case leave a file on disk but out of the index, which is the
    literal shape of the #11 defect: present on disk, absent from
    `git ls-files`.

    `force_add` tracks specific paths with `git add -f` after `.gitignore` is
    written, so a path can be both gitignore-matched and present in the
    index. This is what lets a test isolate the gitignore-rule check
    (section 3 of the script) from the untracked-file check (section 2):
    with the file tracked, section 2 cannot fire, so only section 3's
    `git check-ignore --no-index` logic can produce a failure.
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
        for relpath in force_add or []:
            subprocess.run(["git", "add", "-f", relpath], cwd=repo, check=True)

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


def test_tracked_go_file_directly_under_cmd_passes(tmp_path: pathlib.Path) -> None:
    # Regression test: a tracked .go file directly under cmd/, with no
    # intervening subdirectory, must not be misreported as untracked. A
    # pathspec of only 'cmd/**/*.go' does not match this path (git's ** in a
    # pathspec requires at least one path component beneath the prefix), so
    # the gate needs 'cmd/*.go' alongside it to cover this case.
    repo = make_repo(
        tmp_path,
        files={
            "cmd/shellforge/main.go": MAIN_GO_WITH_MAIN,
            "cmd/root.go": "package cmd\n",
        },
    )
    result = run_gate(repo)
    assert result.returncode == 0, (result.stdout, result.stderr)
    assert "OK:" in result.stdout


# ----------------------------------------------------------------- ignored file


def test_ignored_go_file_fails_and_names_file_and_rule(tmp_path: pathlib.Path) -> None:
    # The actual #11 defect, reproduced exactly: an unanchored `shellforge`
    # gitignore line matches the cmd/shellforge directory at any depth. This
    # is the single most important case in this file, so the file is force
    # added to the index (git add -f) despite the gitignore match. Tracked
    # means section 2 (the untracked-file check) cannot fire, so a failure
    # here can only come from section 3 (git check-ignore --no-index). If the
    # file were left untracked instead, section 2 alone would produce a
    # failure that happens to mention both the path and the substring
    # "shellforge", and this test would pass even with section 3 deleted.
    repo = make_repo(
        tmp_path,
        files={"cmd/shellforge/main.go": MAIN_GO_WITH_MAIN},
        gitignore="shellforge\n",
        add_to_index=False,
        force_add=["cmd/shellforge/main.go"],
    )
    result = run_gate(repo)
    assert result.returncode != 0
    combined = result.stdout + result.stderr
    assert "cmd/shellforge/main.go" in combined
    assert "shellforge" in combined
    # Proves section 2 did not fire: that check's failure message is the only
    # place "not tracked by git" appears in the script's output.
    assert "not tracked by git" not in combined


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


# -------------------------------------------------------------- git tool failure


def test_check_ignore_failure_exits_2_not_ok(tmp_path: pathlib.Path) -> None:
    # Acceptance criterion #3 from issue #73: the gate must exit non-zero,
    # not print a success line, if git check-ignore cannot run at all. This
    # is distinct from the up-front "not inside a git repository" guard: the
    # repo-root check passes here, and the failure happens mid-script.
    #
    # A fake `git` shim is placed earlier on PATH. It delegates every
    # subcommand to the real git except `check-ignore`, which it fails on
    # purpose, so only the section 3 error path is exercised.
    real_git = shutil.which("git")
    assert real_git is not None

    repo = make_repo(tmp_path, files={"cmd/shellforge/main.go": MAIN_GO_WITH_MAIN})

    bin_dir = tmp_path / "fakebin"
    bin_dir.mkdir()
    shim = bin_dir / "git"
    shim.write_text(
        "#!/usr/bin/env bash\n"
        'if [ "$1" = "check-ignore" ]; then\n'
        "  echo 'fake git: check-ignore exploded' >&2\n"
        "  exit 3\n"
        "fi\n"
        f'exec "{real_git}" "$@"\n'
    )
    shim.chmod(0o755)

    env = dict(os.environ)
    env["PATH"] = f"{bin_dir}{os.pathsep}{env['PATH']}"
    result = subprocess.run(
        ["bash", "scripts/check-cli-package.sh"],
        cwd=repo,
        capture_output=True,
        text=True,
        env=env,
    )
    assert result.returncode == 2, (result.stdout, result.stderr)
    assert "OK:" not in result.stdout


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
