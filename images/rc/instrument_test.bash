#!/usr/bin/env bash
# Unit test for __sf_urlencode in instrument.bash. Runs standalone, no
# Docker and no sandbox required: bash images/rc/instrument_test.bash.
#
# Extracts only __sf_urlencode rather than sourcing the whole of
# instrument.bash, because sourcing it also pulls in /etc/bash.bashrc and
# the user's own .bashrc, sets PROMPT_COMMAND and PS0, and makes several
# variables readonly: side effects a unit test for one function has no
# business triggering.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
src="$here/instrument.bash"

eval "$(sed -n '/^# BEGIN __sf_urlencode$/,/^# END __sf_urlencode$/p' "$src")"

fail=0

check() {
  local name="$1" input="$2" want="$3" got
  __sf_urlencode "$input"
  got="$__sf_encoded"
  if [ "$got" != "$want" ]; then
    echo "FAIL: $name: input $(printf '%q' "$input"), want $(printf '%q' "$want"), got $(printf '%q' "$got")"
    fail=1
  else
    echo "OK: $name"
  fi
}

check "plain path" "/home/learner" "/home/learner"
check "literal percent at end" "/home/learner/100%" "/home/learner/100%25"
check "literal percent name stays distinct from a space" "/home/learner/a%20b" "/home/learner/a%2520b"
check "space becomes percent twenty" "/home/learner/a b" "/home/learner/a%20b"
check "multibyte utf8 path" $'/home/learner/caf\xc3\xa9' "/home/learner/caf%C3%A9"
check "root" "/" "/"
check "unreserved characters pass through" "/home/learner/a-b_c.d~e" "/home/learner/a-b_c.d~e"

# ---------------------------------------------------------------------------
# __sf_after: the missing-SF_STATE path.
#
# Extracted the same way and for the same reason as __sf_urlencode above.
# Each case runs __sf_after in its own subshell, with `set +e` inside it, so
# a case that captures nothing (the bug this test exists to catch) reports as
# a FAIL line rather than aborting the whole suite under -e: a command
# substitution assigned to a variable is a simple command in bash, and its
# failure is fatal under `set -e` like any other.
# ---------------------------------------------------------------------------
eval "$(sed -n '/^# BEGIN __sf_after$/,/^# END __sf_after$/p' "$src")"

check_state_gone() {
  local name="$1" want_contains="$2" level_id="${3:-}" got
  got="$(
    set +e
    unset __sf_state_gone_warned
    export SF_STATE="/nonexistent/sf-state-$$"
    if [ -n "$level_id" ]; then
      export SF_LEVEL_ID="$level_id"
    else
      unset SF_LEVEL_ID
    fi
    __sf_after
    true
  )"
  if [[ "$got" != *"$want_contains"* ]]; then
    echo "FAIL: $name: output did not contain $(printf '%q' "$want_contains"), got $(printf '%q' "$got")"
    fail=1
  elif [[ "$got" == *"No such file or directory"* ]]; then
    echo "FAIL: $name: a bash error leaked into the reply: $(printf '%q' "$got")"
    fail=1
  else
    echo "OK: $name"
  fi
}

check_state_gone "names the level to rerun" "shellforge run nav-01" "nav-01"
check_state_gone "falls back to a placeholder with no SF_LEVEL_ID" "shellforge run <level-id>"
check_state_gone "tells the learner to exit" "Type \`exit\`"

# Warned once per shell, not once per prompt: two calls in the same subshell
# (same __sf_state_gone_warned) must print the message only once.
twice="$(
  set +e
  unset __sf_state_gone_warned
  export SF_STATE="/nonexistent/sf-state-$$"
  __sf_after
  __sf_after
  true
)"
count=$(printf '%s\n' "$twice" | grep -c "cannot be repaired" || true)
if [ "$count" != "1" ]; then
  echo "FAIL: warns once per shell: expected the message exactly once across two prompts, got $count"
  fail=1
else
  echo "OK: warns once per shell"
fi

# ---------------------------------------------------------------------------
# `next`
#
# Extracted the same way, with the absolute path to the control-channel shim
# swapped for a stub, because the point of the test is the decision the
# function makes about the sentinel and not the request it sends.
#
# Every case runs in a subshell. The whole behaviour under test is that one
# branch calls `exit` and the other does not, so a test that ran it in this
# shell would end here on its first passing case.
# ---------------------------------------------------------------------------
next_src="$(sed -n '/^# BEGIN __sf_next$/,/^# END __sf_next$/p' "$src"   | sed 's#/opt/shellforge/bin/_sf-request#__sf_request_stub#')"

sf_state_dir="$(mktemp -d)"
trap 'rm -rf "$sf_state_dir"' EXIT

# check_next runs `next` with the sentinel either present or absent and
# asserts on whether the shell ended, what was printed, and whether the
# sentinel survived.
#
# want_exit is the exit status the subshell is expected to end with: 0 when
# `next` ended the shell, 99 when it returned and the line after it ran.
check_next() {
  local name="$1" sentinel_present="$2" want_exit="$3" want_gone="$4" out status

  rm -f "$sf_state_dir/advance"
  if [ "$sentinel_present" = "yes" ]; then
    : > "$sf_state_dir/advance"
  fi

  # `|| status=$?` rather than a bare assignment: this file runs under
  # `set -e`, and the whole point of one of the two cases is that the
  # subshell ends non-zero.
  status=0
  out="$(
    set +e
    export SF_STATE="$sf_state_dir"
    __sf_request_stub() { printf 'reply for %s
' "$*"; }
    eval "$next_src"
    next
    exit 99
  )" || status=$?

  if [ "$status" != "$want_exit" ]; then
    echo "FAIL: $name: exit status $status, want $want_exit"
    fail=1
    return
  fi
  if [ "${out#reply for}" = "$out" ]; then
    echo "FAIL: $name: the host reply was not printed, got $(printf '%q' "$out")"
    fail=1
    return
  fi
  if [ "$want_gone" = "yes" ] && [ -e "$sf_state_dir/advance" ]; then
    echo "FAIL: $name: the sentinel was left behind, so the next level would end on its own"
    fail=1
    return
  fi
  echo "OK: $name"
}

check_next "ends the shell when the host accepted" yes 0 yes
check_next "stays put when the host did not" no 99 yes

exit $fail
