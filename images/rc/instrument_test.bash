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

exit $fail
