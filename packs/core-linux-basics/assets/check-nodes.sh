#!/bin/bash
# Meridian Logistics node health sweep.
#
# Kofi wrote this. It prints the healthy nodes on stdout and the problems on
# stderr, which is why running it and reading the screen tells you nothing
# useful: the two streams arrive interleaved and look like one list.
#
# It exits non-zero when any node failed, which is correct behaviour for a
# check script and worth knowing about before you wire it into anything.

echo "node atlas-01  OK      latency 12ms"
echo "node atlas-02  OK      latency 15ms"
echo "ERROR node atlas-03 unreachable after 3 attempts" >&2
echo "node atlas-04  OK      latency 11ms"
echo "ERROR node atlas-05 returned HTTP 503" >&2
echo "node atlas-06  OK      latency 18ms"
echo "node atlas-07  OK      latency 14ms"
echo "ERROR node atlas-08 certificate expired 2026-01-11" >&2
echo "node atlas-09  OK      latency 13ms"
echo "ERROR node atlas-10 disk usage 97 percent" >&2
echo "node atlas-11  OK      latency 16ms"
echo "node atlas-12  OK      latency 12ms"

exit 2
