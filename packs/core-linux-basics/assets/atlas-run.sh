#!/bin/bash
# Meridian Logistics nightly consolidation.
#
# Runs as the learner. Reads the config, writes one report line. It is
# deliberately boring: the level is about why it will not run, not about what
# it does when it does.

set -e

here="$(cd "$(dirname "$0")/.." && pwd)"
conf="$here/etc/atlas.conf"
log="$here/var/log/atlas/nightly.log"

depot=$(grep '^depot=' "$conf" | cut -d= -f2)
retention=$(grep '^retention_days=' "$conf" | cut -d= -f2)

# Written with > rather than >>. This file is the job's report, not its
# history, so a rerun replaces it and two runs in a row leave the same bytes
# behind.
printf 'nightly consolidation complete: depot %s, retention %s days\n' \
    "$depot" "$retention" > "$log"
