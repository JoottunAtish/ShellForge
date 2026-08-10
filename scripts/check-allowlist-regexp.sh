#!/usr/bin/env bash
#
# check-allowlist-regexp.sh: keeps the identifier allowlist regexp from drifting.
#
# Every value that reaches a docker or wsl.exe argv (an image name, a user name,
# a manifest path) must match ^[a-zA-Z0-9][a-zA-Z0-9_-]*$. The first character is
# restricted on purpose: a value that can start with a hyphen is flag-shaped, and
# once it lands in an argv vector as a positional operand the command reads it as
# an option instead. That is argument injection, and an argv vector on its own
# does not stop it.
#
# An older, looser one-class form allowed a hyphen in any position including the
# first. Three files quoted the regexp and two had drifted to that form. Issue #19
# removed the last copy. This script fails if one comes back, and it also fails if
# the security skill stops quoting the strict form, because that skill is what a
# session loads before writing the sandbox destroy path.
#
# The loose form is never spelled out in this file. The needle below is an ERE
# that matches it without containing it, so this script does not become the copy
# it is hunting. scripts/check-punctuation.sh names its banned code points the
# same way.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

skill=".claude/skills/security/SKILL.md"
strict_ere='\^\[a-zA-Z0-9\]\[a-zA-Z0-9_-\]\*\$'
loose_ere='a-zA-Z0-9_-[]][+]'

status=0

if [ ! -f "$skill" ]; then
  echo "FAIL: $skill is missing. That file is what this check exists to protect." >&2
  exit 1
fi

if ! grep -qE "$strict_ere" "$skill"; then
  echo "FAIL: $skill no longer quotes the identifier allowlist regexp." >&2
  echo '      Expected: ^[a-zA-Z0-9][a-zA-Z0-9_-]*$' >&2
  echo "      Fix: restore it in the 'Validate before you execute' bullet." >&2
  status=1
fi

hits="$(git ls-files -z | xargs -0 grep -InE "$loose_ere" -- || true)"
if [ -n "$hits" ]; then
  echo 'FAIL: the loose identifier allowlist regexp is back in these files:' >&2
  printf '%s\n' "$hits" >&2
  echo '      It allows a leading hyphen, so a validated value can be flag-shaped' >&2
  echo '      and read as an option once it reaches an argv vector.' >&2
  echo '      Fix: use ^[a-zA-Z0-9][a-zA-Z0-9_-]*$ instead.' >&2
  echo '      See .claude/skills/security/SKILL.md.' >&2
  status=1
fi

if [ "$status" -eq 0 ]; then
  echo 'allowlist regexp: consistent'
fi

exit "$status"
