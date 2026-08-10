#!/usr/bin/env python3
"""Tests for the Action pinning check in `scripts/check-ci-gates.py`.

The check exists so that a `uses:` pinned to a mutable tag cannot reach main. A
check nobody tests is a check that quietly stops checking, so the matcher is
covered here against known-good and known-bad references, and the real workflow
directory is asserted clean as a regression guard.

Run from the repository root:

    python3 -m pytest scripts/tests -q
"""

from __future__ import annotations

import importlib.util
import pathlib

import pytest

ROOT = pathlib.Path(__file__).resolve().parents[2]

# The script's filename contains a hyphen, so it is not importable by name. Load
# it by path instead. This is side-effect free: `main()` is guarded by
# `if __name__ == "__main__"`.
_SCRIPT = ROOT / "scripts" / "check-ci-gates.py"
_spec = importlib.util.spec_from_file_location("check_ci_gates", _SCRIPT)
assert _spec is not None and _spec.loader is not None
gates = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(gates)

GOOD_SHA = "3d3c42e5aac5ba805825da76410c181273ba90b1"


def workflow(*uses_lines: str) -> str:
    """A minimal but real workflow whose steps are the given `uses:` lines."""
    steps = "\n".join(f"      - uses: {line}" for line in uses_lines)
    return f"name: Fixture\n\njobs:\n  build:\n    steps:\n{steps}\n"


# --------------------------------------------------------------- accepted refs

ACCEPTED = [
    pytest.param(f"actions/checkout@{GOOD_SHA} # v7.0.1", id="sha-plus-version"),
    pytest.param(f"actions/checkout@{GOOD_SHA} # v7", id="major-only-comment"),
    pytest.param(f"actions/checkout@{GOOD_SHA} # v7.0.1-beta.1", id="prerelease"),
    pytest.param(
        f"actions/cache/restore@{GOOD_SHA} # v4.2.0", id="subdirectory-action"
    ),
    pytest.param("./.github/actions/setup # local", id="local-action"),
    pytest.param("./.github/actions/setup", id="local-action-no-comment"),
    pytest.param(
        "docker://alpine@sha256:"
        "beefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdead",
        id="image-by-digest",
    ),
    # YAML allows a quoted scalar, and a correctly pinned action is still
    # correctly pinned when quoted. An earlier version of the line regex
    # captured the quote characters as part of the ref, so these failed as
    # "not a 40 character SHA" while being perfectly valid.
    pytest.param(
        f'"actions/checkout@{GOOD_SHA}" # v7.0.1', id="double-quoted-ref"
    ),
    pytest.param(
        f"'actions/checkout@{GOOD_SHA}' # v7.0.1", id="single-quoted-ref"
    ),
    pytest.param('"./.github/actions/setup" # local', id="quoted-local-action"),
]


@pytest.mark.parametrize("line", ACCEPTED)
def test_accepted_references_report_nothing(line: str) -> None:
    assert gates.scan_workflow_text("fixture.yml", workflow(line)) == []


# --------------------------------------------------------------- rejected refs

REJECTED = [
    pytest.param("actions/checkout@v7", "mutable tag", id="major-version-tag"),
    pytest.param("actions/checkout@v7.0.1", "mutable tag", id="full-version-tag"),
    pytest.param("actions/checkout@main", "mutable tag", id="branch"),
    pytest.param("actions/checkout", "no version at all", id="no-ref"),
    pytest.param("actions/checkout@", "no version at all", id="empty-ref"),
    pytest.param(f"actions/checkout@{GOOD_SHA}", "no version comment", id="no-comment"),
    pytest.param(
        f"actions/checkout@{GOOD_SHA} # pinned by hand",
        "does not start with a",
        id="comment-without-version",
    ),
    pytest.param(
        f"actions/checkout@{GOOD_SHA.upper()} # v7.0.1", "uppercase", id="uppercase-sha"
    ),
    pytest.param(
        f"actions/checkout@{GOOD_SHA[:39]} # v7.0.1",
        "abbreviated SHA",
        id="thirty-nine-characters",
    ),
    pytest.param(
        f"actions/checkout@{GOOD_SHA[:10]} # v7.0.1",
        "abbreviated SHA",
        id="short-sha",
    ),
    pytest.param("docker://alpine:3.20", "by tag", id="image-by-tag"),
    # Quoting must not become a way to smuggle a tag pin past the gate.
    pytest.param('"actions/checkout@v7"', "mutable tag", id="double-quoted-tag"),
    pytest.param("'actions/checkout@v7'", "mutable tag", id="single-quoted-tag"),
    pytest.param(
        f'"actions/checkout@{GOOD_SHA}"', "no version comment", id="quoted-no-comment"
    ),
]


@pytest.mark.parametrize("line,expected_reason", REJECTED)
def test_rejected_references_are_reported(line: str, expected_reason: str) -> None:
    problems = gates.scan_workflow_text("fixture.yml", workflow(line))
    assert len(problems) == 1, problems
    assert expected_reason in problems[0]


def test_the_failure_names_the_file_and_the_line() -> None:
    # The message is read by a person who has to go and fix the line, so it has to
    # say which line. The fixture puts the offending `uses:` sixth.
    text = workflow(f"actions/checkout@{GOOD_SHA} # v7.0.1", "actions/setup-go@v7")
    problems = gates.scan_workflow_text(".github/workflows/ci.yml", text)
    assert len(problems) == 1, problems
    assert problems[0].startswith(".github/workflows/ci.yml:7: actions/setup-go@v7")


def test_the_remediation_names_the_action_being_fixed() -> None:
    problem = gates.check_uses_ref("actions/setup-go@v7", None)
    assert problem is not None
    assert "gh api repos/actions/setup-go/git/ref/tags/v7" in problem
    # A subdirectory action still resolves against its repository, not the path.
    problem = gates.check_uses_ref("actions/cache/restore@v4", None)
    assert problem is not None
    assert "gh api repos/actions/cache/git/ref/tags/v4" in problem


def test_every_uses_gets_one_problem_not_just_the_first() -> None:
    text = workflow("actions/checkout@v7", "actions/setup-go@v7")
    assert len(gates.scan_workflow_text("fixture.yml", text)) == 2


# ------------------------------------------------------- line scanner coverage


def test_the_line_scanner_sees_every_uses_the_yaml_parse_does() -> None:
    yaml = pytest.importorskip("yaml")
    text = workflow(f"actions/checkout@{GOOD_SHA} # v7.0.1", "actions/setup-go@v7")
    seen = {ref for _, ref, _ in gates.parse_uses_lines(text)}
    assert set(gates.uses_refs_in_workflow(yaml.safe_load(text))) == seen


def test_a_flow_style_uses_escapes_the_line_scanner() -> None:
    # This is the case the cross-check in main() exists for. A `uses:` written
    # inside a flow mapping is invisible to the line scanner, so if the scanner
    # were the only pass, this reference would be exempt from the pinning rule.
    # The assertion documents that gap and proves the cross-check has real work.
    yaml = pytest.importorskip("yaml")
    text = (
        "name: Fixture\n\njobs:\n  build:\n    steps:\n"
        "      - {uses: actions/checkout@v7}\n"
    )
    assert gates.scan_workflow_text("fixture.yml", text) == []
    assert gates.uses_refs_in_workflow(yaml.safe_load(text)) == [
        "actions/checkout@v7"
    ]


def test_a_reusable_workflow_call_is_collected() -> None:
    yaml = pytest.importorskip("yaml")
    text = (
        "name: Fixture\n\njobs:\n  build:\n"
        f"    uses: ./.github/workflows/reusable.yml\n"
    )
    assert gates.uses_refs_in_workflow(yaml.safe_load(text)) == [
        "./.github/workflows/reusable.yml"
    ]


def test_uses_inside_a_run_block_is_not_mistaken_for_a_step() -> None:
    # A shell line that happens to contain the word is not a step reference. The
    # regex anchors on the start of the line, so indentation alone is not enough
    # to fool it, but `echo "uses: x"` would be if it were not for the anchor.
    text = (
        "name: Fixture\n\njobs:\n  build:\n    steps:\n"
        "      - run: |\n"
        "          echo 'this step uses: actions/checkout@v7'\n"
    )
    assert gates.parse_uses_lines(text) == []


# ------------------------------------------------------------ the real thing


def test_the_real_workflows_are_all_pinned() -> None:
    """The regression guard. This is what turns red if somebody adds a tag pin."""
    files = sorted(
        p
        for p in (ROOT / ".github" / "workflows").iterdir()
        if p.is_file() and p.suffix in (".yml", ".yaml")
    )
    assert files, "no workflow files found"

    problems: list[str] = []
    total = 0
    for path in files:
        text = path.read_text(encoding="utf-8")
        rel = path.relative_to(ROOT).as_posix()
        problems.extend(gates.scan_workflow_text(rel, text))
        total += len(gates.parse_uses_lines(text))

    assert problems == [], "\n".join(problems)
    assert total > 0, "no `uses:` lines found, so this test proved nothing"
