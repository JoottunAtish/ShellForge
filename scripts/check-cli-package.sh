#!/usr/bin/env bash
#
# check-cli-package.sh: fails the build if the CLI entry point package is
# missing, untracked, or unbuildable.
#
# Issue #11: the whole cmd/shellforge package had never been committed.
# .gitignore's unanchored `shellforge` pattern matched the directory at any
# depth, so `git ls-files cmd/shellforge/` returned nothing, and because CI
# builds with `go build ./...` and tests with `go test ./...`, both of which
# only operate on packages present in the checkout, a missing package was
# invisible to them rather than an error. PR #72 fixed the .gitignore cause
# and committed the three missing files. This script is the gate that would
# have caught it: it asserts the package exists, that every .go file under
# cmd/ is tracked by git, that no .go file anywhere in the repository is
# matched by a gitignore rule, and that the main package declares func main.
#
# A missing or broken git or grep must never be read as "nothing found".
# Exit 2 keeps that failure mode distinct from exit 1, a real finding.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

for tool in git grep find mktemp; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "check-cli-package: $tool not found on PATH." >&2
    echo "  Install $tool, then re-run: ./scripts/check-cli-package.sh" >&2
    exit 2
  fi
done

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "FAIL: not inside a git repository." >&2
  echo "      This gate needs git ls-files and git check-ignore to run." >&2
  exit 2
fi

status=0

# ---------------------------------------------------------------------------
# 1. The entry point package directory exists and holds at least one .go file.
# ---------------------------------------------------------------------------
entry_dir="cmd/shellforge"

if [ ! -d "$entry_dir" ]; then
  echo "FAIL: $entry_dir does not exist." >&2
  echo "      The CLI entry point package is missing entirely." >&2
  echo "      Fix: restore cmd/shellforge, then re-run this script." >&2
  status=1
fi

entry_go_files=()
if [ -d "$entry_dir" ]; then
  while IFS= read -r -d '' f; do
    entry_go_files+=("$f")
  done < <(find "$entry_dir" -maxdepth 1 -name '*.go' -print0)
fi

if [ -d "$entry_dir" ] && [ "${#entry_go_files[@]}" -eq 0 ]; then
  echo "FAIL: $entry_dir exists but contains no .go file." >&2
  echo "      An empty package directory builds nothing." >&2
  echo "      Fix: add main.go back under cmd/shellforge." >&2
  status=1
fi

# ---------------------------------------------------------------------------
# 2. Every .go file under cmd/ is tracked by git.
# ---------------------------------------------------------------------------
if [ -d cmd ]; then
  disk_files=()
  while IFS= read -r -d '' f; do
    disk_files+=("$f")
  done < <(find cmd -name '*.go' -print0)

  tmp_tracked="$(mktemp)"
  trap 'rm -f "$tmp_tracked"' EXIT

  # Two pathspecs: 'cmd/**/*.go' alone misses a .go file directly under cmd/
  # with no subdirectory (git's ** requires at least one path component to
  # match), so 'cmd/*.go' covers that case.
  git ls-files -z -- 'cmd/*.go' 'cmd/**/*.go' >"$tmp_tracked" && ls_status=0 || ls_status=$?
  if [ "$ls_status" -ne 0 ]; then
    echo "FAIL: git ls-files could not enumerate cmd/*.go and cmd/**/*.go (exit $ls_status)." >&2
    echo "      This gate reported nothing, not that the tree is clean." >&2
    exit 2
  fi

  declare -A tracked
  while IFS= read -r -d '' f; do
    tracked["$f"]=1
  done <"$tmp_tracked"

  for f in "${disk_files[@]}"; do
    if [ -z "${tracked[$f]:-}" ]; then
      echo "FAIL: $f exists on disk but is not tracked by git." >&2
      echo "      This is the exact shape of the #11 defect: a .go file that" >&2
      echo "      'go build ./...' and 'go test ./...' silently skip because" >&2
      echo "      it was never committed." >&2
      echo "      Fix: git add $f" >&2
      status=1
    fi
  done
fi

# ---------------------------------------------------------------------------
# 3. No .go file anywhere in the repository is matched by a gitignore rule.
# ---------------------------------------------------------------------------
all_go_files=()
while IFS= read -r -d '' f; do
  all_go_files+=("$f")
done < <(find . -name '*.go' -not -path './.git/*' -print0)

if [ "${#all_go_files[@]}" -gt 0 ]; then
  # --no-index: git check-ignore's default mode consults the index and never
  # reports an already-tracked path as ignored, mirroring `git status`. That
  # default would hide the #11 defect the moment the file was first
  # committed: a bad gitignore rule added later would go unnoticed for every
  # file that predates it. --no-index checks the pattern match on its own
  # terms, regardless of whether the path happens to be tracked right now.
  ignore_hits="$(git check-ignore -v --no-index -- "${all_go_files[@]}" 2>/dev/null)" && ignore_status=0 || ignore_status=$?
  case "$ignore_status" in
    0)
      echo "FAIL: these .go files are matched by a gitignore rule:" >&2
      echo "$ignore_hits" >&2
      echo "      A .go file that git would ignore can vanish from the repository" >&2
      echo "      exactly the way cmd/shellforge did in #11." >&2
      echo "      Fix: anchor or remove the offending gitignore rule." >&2
      status=1
      ;;
    1)
      : # no .go file is ignored, nothing to do here
      ;;
    *)
      echo "FAIL: git check-ignore could not complete the scan (exit $ignore_status)." >&2
      echo "      This gate reported nothing, not that the tree is clean." >&2
      exit 2
      ;;
  esac
fi

# ---------------------------------------------------------------------------
# 4. The main package declares func main.
# ---------------------------------------------------------------------------
if [ -d "$entry_dir" ] && [ "${#entry_go_files[@]}" -gt 0 ]; then
  if ! grep -qE '^func main\(\)' "$entry_dir"/*.go 2>/dev/null; then
    echo "FAIL: no file under $entry_dir declares func main()." >&2
    echo "      The entry point package has no entry point." >&2
    echo "      Fix: add a func main() to cmd/shellforge." >&2
    status=1
  fi
fi

if [ "$status" -eq 0 ]; then
  echo "OK: cmd/shellforge is present, tracked, and buildable"
fi

exit "$status"
