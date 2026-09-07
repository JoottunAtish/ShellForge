#!/bin/bash
# Meridian Logistics depot indexer, Kofi's version.
#
# It was supposed to build an index and exit. It builds nothing and never
# exits, which is why it is still running three days after anyone noticed.
#
# It sleeps rather than spinning. A genuinely CPU hungry loop would be more
# faithful to the ticket and would also peg a core on the machine of every
# learner and every CI runner that plays this level, which is a poor trade
# for realism nobody can see.
#
# The sleep is a backgrounded child that the trap kills and then reaps. See
# heartbeat.sh for why: a plain `sleep 1` in the loop body is a separate
# process that neither the learner's `kill <pid>` nor teardown's `pkill -f`
# matches, so it would outlive this script as an orphan on PID 1. The window
# is one second rather than five, which makes it a race this level would
# sometimes win and sometimes lose. That is worse than failing every time.

cd "$(dirname "$0")" || exit 1
echo $$ > .indexer.pid

child=""
trap 'kill "$child" 2>/dev/null; wait "$child" 2>/dev/null; exit 0' TERM INT

while true; do
    sleep 1 &
    child=$!
    wait "$child"
done
