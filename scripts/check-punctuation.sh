#!/usr/bin/env bash
#
# Fail the build on typographic punctuation.
#
# Shellforge is a command line teaching tool. A smart quote or an em dash in a
# copy-pasteable command is a broken command, and an em dash in a level briefing
# renders as a mojibake box in a terminal that is not UTF-8 clean. So the rule is
# absolute: ASCII punctuation only. See Rule 0 in CLAUDE.md.
#
# This file is deliberately pure ASCII. The banned characters are expressed as
# \x{...} escapes so that the checker never flags itself.
#
# Usage:
#   ./scripts/check-punctuation.sh            # check tracked files
#   ./scripts/check-punctuation.sh path ...   # check specific paths
#
# Exit 0 clean, exit 1 on any hit.

set -uo pipefail

# grep -P needs a UTF-8 or unibyte locale. Git Bash on Windows often has neither.
export LC_ALL="${LC_ALL:-C.UTF-8}"

# U+2010 HYPHEN                     U+2011 NON-BREAKING HYPHEN
# U+2012 FIGURE DASH                U+2013 EN DASH
# U+2014 EM DASH                    U+2015 HORIZONTAL BAR
# U+2018 LEFT SINGLE QUOTE          U+2019 RIGHT SINGLE QUOTE
# U+201A SINGLE LOW QUOTE           U+201C LEFT DOUBLE QUOTE
# U+201D RIGHT DOUBLE QUOTE         U+201E DOUBLE LOW QUOTE
# U+2026 HORIZONTAL ELLIPSIS        U+2032 PRIME
# U+2033 DOUBLE PRIME               U+2212 MINUS SIGN
# U+00A0 NO-BREAK SPACE             U+2009 THIN SPACE
# U+202F NARROW NO-BREAK SPACE
PATTERN='[\x{2010}-\x{2015}\x{2018}-\x{201E}\x{2026}\x{2032}\x{2033}\x{2212}\x{00A0}\x{2009}\x{202F}]'

# Binary and vendored content is exempt. Everything a human writes is not.
EXCLUDES=(
  ':(exclude)*.png' ':(exclude)*.jpg' ':(exclude)*.jpeg' ':(exclude)*.gif'
  ':(exclude)*.ico' ':(exclude)*.woff' ':(exclude)*.woff2'
  ':(exclude)*.tar' ':(exclude)*.gz' ':(exclude)*.zst' ':(exclude)*.zip'
  ':(exclude)*.db' ':(exclude)*.sqlite' ':(exclude)*.cast'
  ':(exclude)go.sum'
)

if ! grep -qP 'x' <<<'x' 2>/dev/null; then
  echo "check-punctuation: this grep does not support -P (PCRE)." >&2
  echo "  Install GNU grep, or run the equivalent Python check:" >&2
  echo "  python3 -c \"import re,sys,pathlib;..." >&2
  exit 2
fi

if [ "$#" -gt 0 ]; then
  FILES=("$@")
else
  if ! command -v git >/dev/null 2>&1; then
    echo "check-punctuation: git not found and no paths given." >&2
    exit 2
  fi
  mapfile -t FILES < <(git ls-files -- . "${EXCLUDES[@]}")
fi

if [ "${#FILES[@]}" -eq 0 ]; then
  echo "check-punctuation: no files to check."
  exit 0
fi

# -I skips binary files. -n gives line numbers. -H forces the filename prefix.
hits="$(grep -HInP "$PATTERN" -- "${FILES[@]}" 2>/dev/null || true)"

if [ -n "$hits" ]; then
  echo "FAIL: typographic punctuation found. ASCII only. See Rule 0 in CLAUDE.md."
  echo
  echo "$hits"
  echo
  echo "Replacements:"
  echo "  em dash    -> a colon, a comma, parentheses, or a full stop"
  echo "  en dash    -> an ASCII hyphen, for ranges like 10-60"
  echo "  curly quote-> an ASCII ' or \""
  echo "  ellipsis   -> three ASCII periods"
  echo "  nbsp       -> an ASCII space"
  count="$(printf '%s\n' "$hits" | wc -l | tr -d ' ')"
  echo
  echo "$count line(s) to fix."
  exit 1
fi

echo "OK: no typographic punctuation found in ${#FILES[@]} files."
