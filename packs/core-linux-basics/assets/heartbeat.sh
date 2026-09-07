#!/bin/bash
# Meridian Logistics liveness heartbeat.
#
# It deliberately writes nothing. A heartbeat that appended to a log would
# change the level's world while the checks are running, and the golden
# contract hashes that world before and after to prove a check never mutates
# anything. Staying quiet is what keeps this level honest.

while true; do
    sleep 5
done
