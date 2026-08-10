#!/usr/bin/env bash
#
# Fail the build on broken relative links in Markdown.
#
# The install guide is the most important file in the repository and it is a web
# of cross-references. A dead link in it strands exactly the person this project
# exists to help, so this is a build gate rather than a nicety.
#
# Only relative links are checked. External URLs are not fetched: CI should not
# fail because somebody else's website is down.
#
# Usage:
#   ./scripts/check-links.sh
#
# Exit 0 clean, exit 1 on any broken link.

set -uo pipefail

cd "$(dirname "$0")/.." || exit 2

broken=0
checked=0

while IFS= read -r file; do
  dir="$(dirname "$file")"

  # Pull the target out of every ](...) link. Skip pure-anchor links such as
  # (#section), and skip external schemes.
  #
  # Fenced code blocks are stripped first. A documentation plan that shows a
  # sample README containing a link to an image nobody has recorded yet is not a
  # broken link, it is an example.
  targets="$(awk '/^[[:space:]]*```/ { infence = !infence; next } !infence' "$file" \
             | grep -oE '\]\([^)]+\)' 2>/dev/null \
             | sed -E 's/^\]\(//; s/\)$//' \
             | grep -vE '^(https?|mailto|ftp):' \
             | grep -vE '^#' || true)"

  [ -z "$targets" ] && continue

  while IFS= read -r link; do
    [ -z "$link" ] && continue

    # Strip any #anchor and any "title" suffix.
    path="${link%%#*}"
    path="${path%% *}"
    [ -z "$path" ] && continue

    checked=$((checked + 1))

    if [ ! -e "$dir/$path" ] && [ ! -e "$path" ]; then
      echo "BROKEN  $file"
      echo "        -> $link"
      broken=$((broken + 1))
    fi
  done <<< "$targets"
done <<< "$(git ls-files '*.md')"

if [ "$broken" -gt 0 ]; then
  echo
  echo "FAIL: $broken broken relative link(s) out of $checked checked."
  exit 1
fi

echo "OK: $checked relative links resolve."
