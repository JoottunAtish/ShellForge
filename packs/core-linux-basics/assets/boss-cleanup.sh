#!/bin/bash
# Truncates the debug log so it cannot fill the disk. This is the job that has
# not run since Kofi edited the schedule.

here="$(cd "$(dirname "$0")/.." && pwd)"
: > "$here/var/log/atlas/debug.log"
