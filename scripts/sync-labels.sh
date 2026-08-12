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
#   ./scripts/sync-labels.sh --prune      # report GitHub's default labels
#                                         # (does not delete anything, see below)

set -uo pipefail

cd "$(dirname "$0")/.." || exit 2

LABELS_FILE=".github/labels.yml"
DRY_RUN="${DRY_RUN:-}"

# --prune is a flag, not a repo argument, so it is checked before the
# positional $1 handling below claims it.
#
# GitHub auto-creates five labels (bug, documentation, enhancement, invalid,
# question) on every new repository, before this script ever runs. They
# shadow the `type:` taxonomy in .github/labels.yml (compare "bug" here with
# "type: bug" there) and are not part of that taxonomy, so this script never
# creates, updates, or deletes them through the sync loop above.
#
# This mode only reports them. It never calls a mutating gh command, because
# deleting a label silently removes it from every issue and pull request that
# carried it, and that decision belongs to a human who has confirmed each
# label is actually unused, not to a script run unattended.
if [ "${1:-}" = "--prune" ]; then
  cat <<'EOF'
GitHub's auto-created default labels, not part of .github/labels.yml:

  bug
  documentation
  enhancement
  invalid
  question

Each one shadows a same-topic `type:` label in .github/labels.yml. This script
does not delete them for you. For each one, confirm it has zero open or closed
issues and zero pull requests, then remove it by hand:

  gh label delete <name>

Run this only after that confirmation. This script never runs gh label delete
itself.
EOF
  exit 0
fi

REPO="${1:-}"

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
