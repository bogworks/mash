#!/usr/bin/env bash
# Tests only. Never shipped, and not a release asset.
#
# mash-local-nats-setup-testcopy.sh SRC DST
#
# Writes DST as a copy of SRC (the shipped mash-local-nats-setup.sh) with the platform test hook put back:
# MLNT_UNAME, when set, replaces the output of `uname -s`, so a test can make the script take its macOS branches. The
# shipped script reads `uname -s` and nothing else (#1120).
#
# Fails (exit 1) unless the anchor text occurs exactly once in SRC, so a change to the script cannot silently turn the
# patch off.
set -euo pipefail

if [ "$#" -ne 2 ]; then
    echo "usage: mash-local-nats-setup-testcopy.sh SRC DST" >&2
    exit 2
fi
src=$1
dst=$2

anchor='if [ "$(uname -s 2>/dev/null || true)" = Darwin ]; then'
patch='if [ "${MLNT_UNAME:-$(uname -s 2>/dev/null || true)}" = Darwin ]; then'

content=$(cat -- "$src"; printf x)
content=${content%x}
rest=$content
count=0
while [[ $rest == *"$anchor"* ]]; do
    count=$((count + 1))
    rest=${rest#*"$anchor"}
done
if [ "$count" -ne 1 ]; then
    echo "mash-local-nats-setup-testcopy.sh: expected '$anchor' exactly once in $src, found $count" >&2
    exit 1
fi
printf '%s' "${content/"$anchor"/$patch}" >"$dst"
chmod 0755 "$dst"
