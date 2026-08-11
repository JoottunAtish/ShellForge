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
# removed the last copy. This script fails if the security skill stops quoting the
# strict form, and it fails if an anchored single-class identifier pattern that
# admits a leading hyphen comes back in the common spellings below.
#
# What this actually catches (read this before trusting a clean run):
#   - the shape ^[CLASS]QUANTIFIER$ where CLASS is a letter/digit/underscore
#     range plus a literal hyphen (leading, trailing, or backslash-escaped
#     trailing), in either case order (a-zA-Z or A-Za-z), and QUANTIFIER is
#     +, *, or a brace repetition such as {1,64}.
#   - it does NOT flag an unanchored class in prose, for example a level
#     briefing that shows `grep -E "[a-zA-Z0-9_-]+"` as a teaching example,
#     because that text has no leading ^ to anchor on.
#   - it does NOT flag the strict two-class form itself,
#     ^[a-zA-Z0-9][a-zA-Z0-9_-]*$, because there the first class is followed
#     by another [ rather than by a quantifier and $.
#
# What it cannot catch, by construction, because this is a heuristic over
# regexp literals in text, not a regexp equivalence checker:
#   - the same loose class written with a shorthand such as [\w-] or [[:alnum:]-]
#   - a pattern assembled at runtime from string parts, so no single literal
#     line ever spells out the shape
#   - a loose class using a character this script's alternation does not
#     enumerate (for example a class built without an underscore at all)
# A reviewer who wants a stronger guarantee than "the common spellings did not
# reappear" still has to read the diff. This gate narrows that job, it does not
# replace it.
#
# The loose form is never spelled out in this file. The needle below is an ERE
# that matches it without containing it, so this script does not become the copy
# it is hunting. scripts/tests/test_check_allowlist_regexp.py checks the needle
# against the spellings above, built by concatenation for the same reason.
# scripts/check-punctuation.sh names its banned code points the same way.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

# A missing or broken tool must never be read as "nothing found". Without this
# probe, an absent git or grep makes the strict-form check below report a
# content violation that does not exist (it cannot read the skill file's
# content correctly) and makes the scan silently cover zero files, both of
# which look exactly like a clean run. Exit 2 keeps that failure mode distinct
# from exit 1, a real hit.
for tool in git grep; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "check-allowlist-regexp: $tool not found on PATH." >&2
    echo "  Install $tool, then re-run: ./scripts/check-allowlist-regexp.sh" >&2
    exit 2
  fi
done

skill=".claude/skills/security/SKILL.md"
strict_ere='\^\[a-zA-Z0-9\]\[a-zA-Z0-9_-\]\*\$'
# Matches ^[CLASS]QUANTIFIER$ where CLASS is a letter/digit/underscore range
# plus a hyphen (leading, trailing, or backslash-escaped trailing), letters in
# either case order. See the header for exactly what this does and does not
# cover, and scripts/tests/test_check_allowlist_regexp.py for the fixtures
# this was built against.
loose_ere='\^\[(-(a-zA-Z|A-Za-z)0-9_|(a-zA-Z|A-Za-z)0-9_(-|\\-))\](\+|\*|\{[0-9]+(,[0-9]*)?\})\$'

# Binary and vendored content is exempt, same list as check-punctuation.sh.
EXCLUDES=(
  ':(exclude)*.png' ':(exclude)*.jpg' ':(exclude)*.jpeg' ':(exclude)*.gif'
  ':(exclude)*.ico' ':(exclude)*.woff' ':(exclude)*.woff2'
  ':(exclude)*.tar' ':(exclude)*.gz' ':(exclude)*.zst' ':(exclude)*.zip'
  ':(exclude)*.db' ':(exclude)*.sqlite' ':(exclude)*.cast'
  ':(exclude)go.sum'
)

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

# git ls-files writes to a real file rather than a pipe or process substitution
# so its own exit status is checked directly. A pipe into xargs or a process
# substitution into mapfile both discard that status: the read side sees an
# empty or partial stream and cannot tell a real empty tree from
# "fatal: detected dubious ownership in repository", the routine failure mode
# inside a containerized runner. Reporting clean in that case would be a false
# green on a security gate.
tmp_files="$(mktemp)"
trap 'rm -f "$tmp_files"' EXIT

git ls-files -z -- . "${EXCLUDES[@]}" >"$tmp_files" && git_status=0 || git_status=$?

if [ "$git_status" -ne 0 ]; then
  echo "FAIL: git ls-files could not enumerate the tracked tree (exit $git_status)." >&2
  echo "      This gate reported nothing, not that the tree is clean." >&2
  echo "      Fix: run 'git ls-files' by hand and resolve the error. Inside a" >&2
  echo "      container this is often: git config --global --add safe.directory $repo_root" >&2
  exit 2
fi

mapfile -d '' FILES <"$tmp_files"

if [ "${#FILES[@]}" -eq 0 ]; then
  echo "check-allowlist-regexp: no tracked files to scan." >&2
  echo "      This gate reported nothing, not that the tree is clean." >&2
  exit 2
fi

# -H forces the filename prefix, -I skips binary files, -n gives line numbers.
# grep's own exit status is read directly: 0 means it found a hit, 1 means it
# ran clean, 2 or more means grep itself failed (a bad pattern, an unreadable
# file). Piping through xargs, as the previous version did, collapses that
# into a single 123 and loses the distinction this script needs.
hits="$(grep -HInE -- "$loose_ere" "${FILES[@]}" 2>/dev/null)" && grep_status=0 || grep_status=$?

case "$grep_status" in
  0)
    echo 'FAIL: the loose identifier allowlist regexp is back in these files:' >&2
    printf '%s\n' "$hits" >&2
    echo '      It allows a leading hyphen, so a validated value can be flag-shaped' >&2
    echo '      and read as an option once it reaches an argv vector.' >&2
    echo '      Fix: use ^[a-zA-Z0-9][a-zA-Z0-9_-]*$ instead.' >&2
    echo '      See .claude/skills/security/SKILL.md.' >&2
    status=1
    ;;
  1)
    : # clean, nothing to do here; status may still be 1 from the strict check above
    ;;
  *)
    echo "FAIL: grep could not complete the scan (exit $grep_status)." >&2
    echo "      This gate reported nothing, not that the tree is clean." >&2
    exit 2
    ;;
esac

if [ "$status" -eq 0 ]; then
  echo 'allowlist regexp: consistent'
fi

exit "$status"
