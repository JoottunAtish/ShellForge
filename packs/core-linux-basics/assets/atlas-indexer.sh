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

cd "$(dirname "$0")" || exit 1
echo $$ > .indexer.pid

while true; do
    sleep 1
done
