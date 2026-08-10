#!/usr/bin/env bash
#
# Apply the branch ruleset in .github/rulesets/main.json to GitHub.
#
# GitHub rulesets are not version controlled by GitHub, so the JSON in this
# repository is the source of truth and this script pushes it. That way a
# protection rule cannot be quietly weakened in the web interface without a
# reviewable diff.
#
# The JSON carries `_comment` keys for the humans reading it. They are stripped
# before the API call, because the API rejects unknown fields.
#
# Requires the gh CLI, authenticated with admin rights on the repository.
#
# Usage:
#   ./scripts/sync-ruleset.sh                  # create or update on the current repo
#   ./scripts/sync-ruleset.sh owner/repo
#   DRY_RUN=1 ./scripts/sync-ruleset.sh        # print the payload, change nothing

set -uo pipefail

cd "$(dirname "$0")/.." || exit 2

RULESET_FILE=".github/rulesets/main.json"
REPO="${1:-}"
DRY_RUN="${DRY_RUN:-}"

for tool in gh jq; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "sync-ruleset: $tool is required but not installed." >&2
    exit 2
  fi
done

if [ ! -f "$RULESET_FILE" ]; then
  echo "sync-ruleset: $RULESET_FILE not found." >&2
  exit 2
fi

if [ -z "$REPO" ]; then
  REPO="$(gh repo view --json nameWithOwner -q .nameWithOwner)" || exit 2
fi

NAME="$(jq -r '.name' "$RULESET_FILE")"

# Strip every _comment and _comment_* key at any depth.
PAYLOAD="$(jq 'walk(if type == "object" then with_entries(select(.key | startswith("_comment") | not)) else . end)' "$RULESET_FILE")"

if [ -n "$DRY_RUN" ]; then
  echo "Would apply to $REPO:"
  printf '%s\n' "$PAYLOAD"
  echo
  echo "Dry run. Nothing was changed."
  exit 0
fi

# Update in place when a ruleset of this name already exists, so re-running does
# not accumulate duplicates.
EXISTING_ID="$(gh api "repos/$REPO/rulesets" --jq ".[] | select(.name == \"$NAME\") | .id" 2>/dev/null | head -1)"

if [ -n "$EXISTING_ID" ]; then
  echo "Updating ruleset '$NAME' (id $EXISTING_ID) on $REPO"
  if printf '%s' "$PAYLOAD" | gh api --method PUT "repos/$REPO/rulesets/$EXISTING_ID" --input - >/dev/null; then
    echo "OK: updated"
  else
    echo "FAILED to update ruleset" >&2
    exit 1
  fi
else
  echo "Creating ruleset '$NAME' on $REPO"
  if printf '%s' "$PAYLOAD" | gh api --method POST "repos/$REPO/rulesets" --input - >/dev/null; then
    echo "OK: created"
  else
    echo "FAILED to create ruleset" >&2
    exit 1
  fi
fi

echo
echo "Active rulesets on $REPO:"
gh api "repos/$REPO/rulesets" --jq '.[] | "  \(.name)  [\(.enforcement)]  target=\(.target)"'

echo
echo "Reminder: the bypass entry for RepositoryAdmin exists only because nobody"
echo "can approve their own pull request. Remove it when a second maintainer joins."
