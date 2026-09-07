#!/bin/bash
# Meridian Logistics liveness heartbeat.
#
# It deliberately writes nothing. A heartbeat that appended to a log would
# change the level's world while the checks are running, and the golden
# contract hashes that world before and after to prove a check never mutates
# anything. Staying quiet is what keeps this level honest.
#
# The sleep is a backgrounded child that the trap kills and then reaps, rather
# than a plain `sleep 5` in the loop body. A plain sleep is a separate process
# and `pkill -f heartbeat.sh` does not match it, so killing this script would
# orphan the sleep to PID 1 and leave it in the process table for up to five
# more seconds. The golden contract looks for strays the moment teardown
# returns, and would find that one. Waiting on the child before exiting is
# what makes it deterministic: a kill is a request, and this script does not
# leave until its child has actually gone.

child=""
trap 'kill "$child" 2>/dev/null; wait "$child" 2>/dev/null; exit 0' TERM INT

while true; do
    sleep 5 &
    child=$!
    wait "$child"
done
