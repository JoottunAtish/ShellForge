#!/usr/bin/env python3
"""Assert two things about CI: nothing escapes the merge gate, nothing runs from a
mutable tag.

**The merge gate.** GitHub only blocks a merge on status checks listed as required
in the branch ruleset. A job that is not listed can fail and still be merged past,
and a job added six months from now is not listed by definition. Two things close
that hole, and this script asserts both of them hold:

1. The `all-green` job depends on every other job in the workflow, so any
   failure fails it.
2. `all-green` is listed as a required status check in the ruleset, so its
   failure blocks the merge.

It also checks that every individual job is required, because defence in depth
is cheap here and a renamed `all-green` would otherwise silently reopen the hole.

**Action pinning.** Every `uses:` in `.github/workflows/` must name a 40 character
commit SHA. A tag, including a major-version tag like `@v7`, is a mutable pointer:
whoever controls it can repoint it at any commit, and CI would execute that commit
with the repository token. A SHA is the only immutable form of Action reference.
Each pin carries a trailing `# vX.Y.Z` comment, which is both how a human reads
the line and how Dependabot knows which version it is updating from.

Run with no arguments from the repository root.
Exit 0 clean, exit 1 on any gap, exit 2 on a setup problem.

The pinning matcher is covered by `scripts/tests/test_check_ci_gates.py`.
"""

from __future__ import annotations

import json
import pathlib
import re
import sys

WORKFLOW = pathlib.Path(".github/workflows/ci.yml")
WORKFLOW_DIR = pathlib.Path(".github/workflows")
RULESET = pathlib.Path(".github/rulesets/main.json")

GATE_JOB = "all-green"
GATE_NAME = "All checks green"

# A `uses:` line, with its optional trailing comment captured separately. The
# comment is invisible to a YAML parse, so the pinning pass reads raw lines: it
# needs the comment, and it needs a line number to put in the failure message.
USES_LINE_RE = re.compile(
    r"^\s*(?:-\s+)?uses:\s*(?P<ref>\S+)(?:\s+#\s*(?P<comment>.*?))?\s*$"
)

# Lowercase only. Two spellings of one commit make a diff harder to read and give
# Dependabot two forms to rewrite, for no gain.
SHA_RE = re.compile(r"^[0-9a-f]{40}$")

# Accepts v1, v1.2, and v1.2.3, with an optional prerelease or build suffix. The
# ticket asked for vX.Y.Z exactly, but not every publisher tags all three
# components and Dependabot writes back whatever the upstream tag says, so a
# strict three-part rule would turn a routine dependency PR red. The major
# component is mandatory, because a comment that does not name a version is not
# doing the job the comment exists for.
VERSION_COMMENT_RE = re.compile(r"^v\d+(?:\.\d+){0,2}(?:[-+][0-9A-Za-z.-]+)?$")

# A matrix job's display name expands per matrix entry, so the workflow name
# alone is not the status check context. Map the job key to the contexts it
# actually produces.
MATRIX_CONTEXTS = {
    "test": ["Test (ubuntu-latest)", "Test (windows-latest)"],
}


def fail(msg: str) -> None:
    print(f"FAIL: {msg}")


def _pin_remediation(action: str, tag: str = "<tag>") -> str:
    """The two commands that turn a tag into a SHA, spelled out for the reader."""
    # An action may name a subdirectory, as in `owner/repo/sub@ref`. The API call
    # wants the repository only.
    repo = "/".join(action.split("/")[:2])
    return (
        f"    gh api repos/{repo}/git/ref/tags/{tag} "
        f"--jq '.object | \"\\(.type) \\(.sha)\"'\n"
        f"    # If that prints `tag`, it is an annotated tag pointing at a tag\n"
        f"    # object rather than a commit. Dereference it:\n"
        f"    gh api repos/{repo}/git/tags/<that-sha> --jq .object.sha\n"
        f"  Then write the commit SHA, with the version as a comment:\n"
        f"    uses: {action}@<commit-sha> # {tag}"
    )


def check_uses_ref(ref: str, comment: str | None) -> str | None:
    """Return what is wrong with a `uses:` reference, or None if it is acceptable.

    Pure, so the test suite can drive it with fixture strings.
    """
    if ref.startswith("./") or ref.startswith("../"):
        # A local action lives in this repository and arrives through the same
        # review as every other file. There is no third party to pin.
        return None

    if ref.startswith("docker://"):
        image = ref[len("docker://") :]
        if "@sha256:" in image:
            return None
        name = image.split(":")[0]
        return (
            f"{ref} names a container image by tag.\n"
            f"  A tag can be repushed to point at different bytes. Pin the digest:\n"
            f"    uses: docker://{name}@sha256:<digest>\n"
            f"  Read the digest with: docker buildx imagetools inspect {image}"
        )

    action, sep, rev = ref.partition("@")
    if not sep or not rev:
        return (
            f"{ref} names no version at all, so CI runs whatever the default\n"
            f"  branch of that action holds today. Pin the commit SHA of a release:\n"
            f"{_pin_remediation(action)}"
        )

    if not SHA_RE.match(rev):
        if len(rev) == 40 and re.fullmatch(r"[0-9A-Fa-f]{40}", rev):
            return (
                f"{ref} is pinned to a SHA written in uppercase.\n"
                f"  Use lowercase, so one commit has one spelling and Dependabot\n"
                f"  rewrites it the way it writes every other pin:\n"
                f"    uses: {action}@{rev.lower()}"
            )
        if re.fullmatch(r"[0-9A-Fa-f]{7,39}", rev):
            return (
                f"{ref} is pinned to an abbreviated SHA.\n"
                f"  An abbreviation can become ambiguous as the upstream repository\n"
                f"  grows, and GitHub does not accept it here. Use all 40 characters:\n"
                f"{_pin_remediation(action)}"
            )
        return (
            f"{ref} is pinned to a mutable tag or branch.\n"
            f"  Whoever controls '{rev}' can repoint it at any commit, and this\n"
            f"  workflow would then run that commit with the repository token. A\n"
            f"  commit SHA is the only immutable form of Action reference.\n"
            f"{_pin_remediation(action, rev)}"
        )

    if comment is None:
        return (
            f"{ref} is pinned to a SHA but carries no version comment, so a reader\n"
            f"  cannot tell what version it is and Dependabot has no version to\n"
            f"  update from. Add one:\n"
            f"    uses: {ref} # v1.2.3"
        )

    words = comment.split()
    if not words or not VERSION_COMMENT_RE.match(words[0]):
        return (
            f"{ref} has the comment '{comment}', which does not start with a\n"
            f"  version. The comment must name the version this SHA is, written the\n"
            f"  way Dependabot writes it:\n"
            f"    uses: {ref} # v1.2.3"
        )

    return None


def parse_uses_lines(text: str) -> list[tuple[int, str, str | None]]:
    """Every `uses:` line in a workflow, as (line number, reference, comment)."""
    found = []
    for lineno, line in enumerate(text.splitlines(), start=1):
        match = USES_LINE_RE.match(line)
        if match:
            found.append((lineno, match.group("ref"), match.group("comment")))
    return found


def scan_workflow_text(path: str, text: str) -> list[str]:
    """Pinning problems in one workflow file, each naming the file and line."""
    problems = []
    for lineno, ref, comment in parse_uses_lines(text):
        problem = check_uses_ref(ref, comment)
        if problem:
            problems.append(f"{path}:{lineno}: {problem}")
    return problems


def uses_refs_in_workflow(workflow: dict) -> list[str]:
    """Every `uses:` value a YAML parse can see.

    The line scanner is the thing that reports problems, because it knows line
    numbers and can see comments. This exists to cross-check it: a `uses:` written
    in a flow-style mapping, or split across lines, would otherwise be silently
    exempt from the pinning rule, which is the failure mode the rule exists to
    prevent.
    """
    refs = []
    for job in (workflow.get("jobs") or {}).values():
        if not isinstance(job, dict):
            continue
        # A job may call a reusable workflow instead of listing steps.
        if isinstance(job.get("uses"), str):
            refs.append(job["uses"])
        for step in job.get("steps") or []:
            if isinstance(step, dict) and isinstance(step.get("uses"), str):
                refs.append(step["uses"])
    return refs


def main() -> int:
    for path in (WORKFLOW, RULESET):
        if not path.is_file():
            print(f"check-ci-gates: {path} not found. Run from the repo root.")
            return 2

    if not WORKFLOW_DIR.is_dir():
        print(f"check-ci-gates: {WORKFLOW_DIR} not found. Run from the repo root.")
        return 2

    try:
        import yaml
    except ImportError:
        print("check-ci-gates: PyYAML is not installed.")
        print("  pip install pyyaml")
        return 2

    workflow = yaml.safe_load(WORKFLOW.read_text(encoding="utf-8"))
    jobs = workflow.get("jobs") or {}
    if not jobs:
        fail("no jobs found in the workflow")
        return 1

    problems = 0

    # ---------------------------------------------------------------- gate job
    if GATE_JOB not in jobs:
        fail(
            f"the workflow has no `{GATE_JOB}` job.\n"
            f"  Without it, a failing job that is not individually required can be\n"
            f"  merged past. Add it back."
        )
        return 1

    gate = jobs[GATE_JOB]
    needs = gate.get("needs") or []
    if isinstance(needs, str):
        needs = [needs]
    needs_set = set(needs)

    expected = {k for k in jobs if k != GATE_JOB}
    missing_from_needs = sorted(expected - needs_set)
    if missing_from_needs:
        problems += 1
        fail(
            f"these jobs are not in `{GATE_JOB}.needs`, so they cannot block a merge:\n"
            + "".join(f"    {j}\n" for j in missing_from_needs)
            + f"  Add them to the needs list of the `{GATE_JOB}` job."
        )

    stale = sorted(needs_set - expected)
    if stale:
        problems += 1
        fail(
            f"`{GATE_JOB}.needs` references jobs that no longer exist:\n"
            + "".join(f"    {j}\n" for j in stale)
        )

    # `if: always()` is what makes the gate run when a dependency fails. Without
    # it the gate is skipped, and a skipped required check blocks forever or is
    # treated as passing depending on configuration. Either way it is wrong.
    if str(gate.get("if", "")).strip() not in ("always()", "${{ always() }}"):
        problems += 1
        fail(
            f"the `{GATE_JOB}` job must be `if: always()`.\n"
            f"  Otherwise it is skipped when a dependency fails, which is exactly\n"
            f"  the case it exists to catch."
        )

    # ----------------------------------------------------------------- ruleset
    raw = RULESET.read_text(encoding="utf-8")
    ruleset = json.loads(raw)

    required: list[str] = []
    for rule in ruleset.get("rules", []):
        if rule.get("type") == "required_status_checks":
            params = rule.get("parameters", {})
            required = [c["context"] for c in params.get("required_status_checks", [])]
            if not params.get("strict_required_status_checks_policy"):
                problems += 1
                fail(
                    "strict_required_status_checks_policy is off, so a branch can be\n"
                    "  merged while behind main with stale passing checks."
                )
            break
    else:
        problems += 1
        fail("the ruleset has no required_status_checks rule at all")

    required_set = set(required)

    if GATE_NAME not in required_set:
        problems += 1
        fail(
            f"the gate check {GATE_NAME!r} is not a required status check.\n"
            f"  This is the one that must be required. Add it to {RULESET}."
        )

    # Every job's produced context should also be required, individually.
    expected_contexts: list[str] = []
    for key, job in jobs.items():
        if key == GATE_JOB:
            continue
        if key in MATRIX_CONTEXTS:
            expected_contexts.extend(MATRIX_CONTEXTS[key])
            continue
        name = job.get("name")
        if not name:
            problems += 1
            fail(
                f"job {key!r} has no `name:`, so its status check context is the job\n"
                f"  key and the ruleset cannot reliably reference it. Give it a name."
            )
            continue
        if re.search(r"\$\{\{", str(name)):
            problems += 1
            fail(
                f"job {key!r} has a templated name {name!r} but no entry in\n"
                f"  MATRIX_CONTEXTS in this script. Add one so its contexts are known."
            )
            continue
        expected_contexts.append(name)

    not_required = sorted(set(expected_contexts) - required_set)
    if not_required:
        problems += 1
        fail(
            "these checks exist but are not required, so they cannot block a merge\n"
            "  on their own:\n"
            + "".join(f"    {c}\n" for c in not_required)
            + f"  Add them to required_status_checks in {RULESET}."
        )

    unknown = sorted(required_set - set(expected_contexts) - {GATE_NAME})
    if unknown:
        problems += 1
        fail(
            "the ruleset requires checks that the workflow never produces, which\n"
            "  blocks every merge forever because they can never report:\n"
            + "".join(f"    {c}\n" for c in unknown)
        )

    # ----------------------------------------------------------- action pinning
    workflow_files = sorted(
        p
        for p in WORKFLOW_DIR.iterdir()
        if p.is_file() and p.suffix in (".yml", ".yaml")
    )
    if not workflow_files:
        problems += 1
        fail(f"no workflow files found in {WORKFLOW_DIR}")

    pinned = 0
    for path in workflow_files:
        text = path.read_text(encoding="utf-8")
        posix = path.as_posix()

        for problem in scan_workflow_text(posix, text):
            problems += 1
            fail(problem)

        lines = parse_uses_lines(text)
        pinned += len(lines)

        seen = {ref for _, ref, _ in lines}
        parsed = yaml.safe_load(text) or {}
        for ref in uses_refs_in_workflow(parsed):
            if ref not in seen:
                problems += 1
                fail(
                    f"{posix}: the `uses:` value '{ref}' is in this file but the\n"
                    f"  line scanner never saw it, so it was not checked for a SHA\n"
                    f"  pin. Write it as a plain `uses: <ref>` on its own line."
                )

    # ------------------------------------------------------------------ report
    if problems:
        print()
        print(f"{problems} problem(s) found.")
        return 1

    print(f"OK: `{GATE_JOB}` depends on all {len(expected)} other job(s).")
    print(f"OK: {len(required_set)} required status check(s), including the gate.")
    print("OK: no CI job can fail without blocking a merge.")
    print(
        f"OK: {pinned} action reference(s) across {len(workflow_files)} workflow "
        f"file(s), all pinned to a commit SHA."
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
