#!/usr/bin/env bash
# Tests only. Never shipped, and not a release asset.
#
# install-testcopy.sh SRC DST
#
# Writes DST as a copy of SRC (the shipped install.sh) with the https-only
# guard widened so a file:// MASH_RELEASE_BASE is accepted and curl may use
# --proto =https,file. The tests run this copy against a fake release folder;
# the shipped script itself accepts https:// and nothing else.
#
# Fails (exit 1) unless the anchor line in SRC occurs exactly once, so a
# change to install.sh cannot silently turn the patch off.
set -euo pipefail

if [ "$#" -ne 2 ]; then
    echo "usage: install-testcopy.sh SRC DST" >&2
    exit 2
fi
src=$1
dst=$2

anchor='    https://*) ;;'
patch='    file://*) PROTO="=https,file" ;;'

count=0
out=""
while IFS= read -r line || [ -n "$line" ]; do
    out+="$line"$'\n'
    if [ "$line" = "$anchor" ]; then
        count=$((count + 1))
        out+="$patch"$'\n'
    fi
done <"$src"

if [ "$count" -ne 1 ]; then
    echo "install-testcopy.sh: expected the line '$anchor' exactly once in $src, found $count" >&2
    exit 1
fi

printf '%s' "$out" >"$dst"
chmod 755 "$dst"
