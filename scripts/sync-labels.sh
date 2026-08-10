#!/usr/bin/env bash
#
# Sync GitHub issue labels from .github/labels.yml.
#
# The YAML file is the source of truth. Running this creates missing labels and
# updates the colour and description of existing ones. It does NOT delete labels
# that are absent from the file, because deleting a label silently removes it
# from every issue that carried it. Prune by hand, deliberately.
#
# Requires the gh CLI, authenticated with repo scope.
#
# Usage:
#   ./scripts/sync-labels.sh              # sync to the current repo
#   ./scripts/sync-labels.sh owner/repo   # sync to a specific repo
#   DRY_RUN=1 ./scripts/sync-labels.sh    # print what would change

set -uo pipefail

cd "$(dirname "$0")/.." || exit 2

LABELS_FILE=".github/labels.yml"
REPO="${1:-}"
DRY_RUN="${DRY_RUN:-}"

if ! command -v gh >/dev/null 2>&1; then
  echo "sync-labels: the gh CLI is not installed." >&2
  echo "  https://cli.github.com" >&2
  exit 2
fi

if [ ! -f "$LABELS_FILE" ]; then
  echo "sync-labels: $LABELS_FILE not found." >&2
  exit 2
fi

repo_args=()
[ -n "$REPO" ] && repo_args=(--repo "$REPO")

# Minimal parser for the specific shape of labels.yml: a flat list of maps with
# name, color and description. Deliberately not a general YAML parser, so that
# this script has no dependency beyond gh and awk.
parse() {
  awk '
    /^- name:/ {
      if (name != "") print name "\t" color "\t" desc
      name = substr($0, index($0, ":") + 1)
      color = ""; desc = ""
      gsub(/^[ \t]+|[ \t]+$/, "", name)
      gsub(/^"|"$/, "", name)
      next
    }
    /^[ \t]+color:/ {
      color = substr($0, index($0, ":") + 1)
      gsub(/^[ \t]+|[ \t]+$/, "", color)
      gsub(/^"|"$/, "", color)
      next
    }
    /^[ \t]+description:/ {
      desc = substr($0, index($0, ":") + 1)
      gsub(/^[ \t]+|[ \t]+$/, "", desc)
      gsub(/^"|"$/, "", desc)
      next
    }
    END { if (name != "") print name "\t" color "\t" desc }
  ' "$LABELS_FILE"
}

created=0
updated=0
failed=0

while IFS=$'\t' read -r name color desc; do
  [ -z "$name" ] && continue

  if [ -n "$DRY_RUN" ]; then
    printf 'would sync  %-28s #%s  %s\n' "$name" "$color" "$desc"
    continue
  fi

  # --force updates an existing label rather than erroring on conflict.
  if out=$(gh label create "$name" \
             --color "$color" \
             --description "$desc" \
             --force \
             "${repo_args[@]}" 2>&1); then
    case "$out" in
      *updated*) updated=$((updated + 1)); printf 'updated  %s\n' "$name" ;;
      *)         created=$((created + 1)); printf 'created  %s\n' "$name" ;;
    esac
  else
    failed=$((failed + 1))
    printf 'FAILED   %-28s %s\n' "$name" "$out" >&2
  fi
done < <(parse)

if [ -n "$DRY_RUN" ]; then
  echo
  echo "Dry run. Nothing was changed."
  exit 0
fi

echo
echo "created $created, updated $updated, failed $failed"
[ "$failed" -eq 0 ] || exit 1
