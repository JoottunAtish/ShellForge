#!/usr/bin/env python3
"""Assert that no CI job can escape the merge gate.

GitHub only blocks a merge on status checks listed as required in the branch
ruleset. A job that is not listed can fail and still be merged past, and a job
added six months from now is not listed by definition. Two things close that
hole, and this script asserts both of them hold:

1. The `all-green` job depends on every other job in the workflow, so any
   failure fails it.
2. `all-green` is listed as a required status check in the ruleset, so its
   failure blocks the merge.

It also checks that every individual job is required, because defence in depth
is cheap here and a renamed `all-green` would otherwise silently reopen the hole.

Run with no arguments from the repository root.
Exit 0 clean, exit 1 on any gap, exit 2 on a setup problem.
"""

from __future__ import annotations

import json
import pathlib
import re
import sys

WORKFLOW = pathlib.Path(".github/workflows/ci.yml")
RULESET = pathlib.Path(".github/rulesets/main.json")

GATE_JOB = "all-green"
GATE_NAME = "All checks green"

# A matrix job's display name expands per matrix entry, so the workflow name
# alone is not the status check context. Map the job key to the contexts it
# actually produces.
MATRIX_CONTEXTS = {
    "test": ["Test (ubuntu-latest)", "Test (windows-latest)"],
}


def fail(msg: str) -> None:
    print(f"FAIL: {msg}")


def main() -> int:
    for path in (WORKFLOW, RULESET):
        if not path.is_file():
            print(f"check-ci-gates: {path} not found. Run from the repo root.")
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

    # ------------------------------------------------------------------ report
    if problems:
        print()
        print(f"{problems} gap(s) in the merge gate.")
        return 1

    print(f"OK: `{GATE_JOB}` depends on all {len(expected)} other job(s).")
    print(f"OK: {len(required_set)} required status check(s), including the gate.")
    print("OK: no CI job can fail without blocking a merge.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
