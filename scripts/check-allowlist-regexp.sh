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
# first. Three copies quoted the regexp and two had drifted to that form. Issue
# #19 removed the last copy. This script fails if one comes back, and it also
# fails if the security skill stops quoting the strict form, because that skill
# is what a session loads before writing the sandbox destroy path.
#
# What this script actually catches, stated narrowly on purpose: the loose form
# written out in full, anchored across the whole string (a leading caret, one
# bracket expression, a trailing plus, a trailing dollar), in either letter case
# order (a-zA-Z0-9_- or A-Za-z0-9_-). It does not catch every string that means
# the same thing to a regexp engine: a different quantifier such as {1,64}, a
# backslash-escaped hyphen (\-), the hyphen moved to the front of the class, or
# the class split across two bracket expressions all read clean here. A green
# run says "the two known spellings have not come back," not "no loose form of
# any shape exists anywhere in the tree."
#
# The loose form is never spelled out in this file. The needle below is an ERE
# that matches it without containing it, so this script does not become a copy
# it is hunting. scripts/check-punctuation.sh names its banned code points the
# same way.
#
# Exit 0 clean, exit 1 on a content violation, exit 2 if the scan itself could
# not run (a missing tool, or the scan exiting a way that proves nothing).

set -euo pipefail

for tool in git grep xargs; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "check-allowlist-regexp: $tool not found. Install it and retry." >&2
    exit 2
  fi
done

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

skill=".claude/skills/security/SKILL.md"
strict_ere='\^\[a-zA-Z0-9\]\[a-zA-Z0-9_-\]\*\$'
loose_ere='\^\[(a-zA-Z|A-Za-z)0-9_-\]\+\$'

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

# xargs collapses a grep exit of 1 through 125 to 123, so 0 (matched), 1 (no
# match), and 123 (matched or no match, split across batches) are all runs
# that actually happened. Anything else means git or grep failed and this
# scan saw nothing, which must not read as "nothing to report."
set +e
hits="$(git ls-files -z | xargs -0 -r grep -HInE "$loose_ere" --)"
rc=$?
set -e

case "$rc" in
  0 | 1 | 123) ;;
  *)
    echo "FAIL: the allowlist scan could not run (exit $rc)." >&2
    echo '      git or grep failed, so this gate reports nothing rather than' >&2
    echo '      reporting clean. Fix the environment and re-run.' >&2
    exit 2
    ;;
esac

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
