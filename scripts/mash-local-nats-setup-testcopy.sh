#!/usr/bin/env bash
# Tests only. Never shipped, and not a release asset.
#
# mash-local-nats-setup-testcopy.sh SRC DST
#
# Writes DST as a copy of SRC (the shipped mash-local-nats-setup.sh) with a
# test hook added: MLNT_UNAME, when set, replaces the output of `uname -s`, so
# "Darwin" makes the copy take the macOS branches (BSD stat flags, ps and lsof
# instead of /proc and ss) on a Linux host. The shipped script reads only
# `uname -s` and has no such hook.
#
# Fails (exit 1) unless the anchor line in SRC occurs exactly once, so a
# change to the script cannot silently turn the patch off.
set -euo pipefail

if [ "$#" -ne 2 ]; then
    echo "usage: mash-local-nats-setup-testcopy.sh SRC DST" >&2
    exit 2
fi
src=$1
dst=$2

# shellcheck disable=SC2016 # the anchor is literal text
anchor='    if [ "$(uname -s 2>/dev/null || true)" = Darwin ]; then'
# shellcheck disable=SC2016
patch='    if [ "${MLNT_UNAME:-$(uname -s 2>/dev/null || true)}" = Darwin ]; then'

count=0
out=""
while IFS= read -r line || [ -n "$line" ]; do
    if [ "$line" = "$anchor" ]; then
        count=$((count + 1))
        line=$patch
    fi
    out+="$line"$'\n'
done <"$src"

if [ "$count" -ne 1 ]; then
    echo "mash-local-nats-setup-testcopy.sh: expected the line '$anchor' exactly once in $src, found $count" >&2
    exit 1
fi

printf '%s' "$out" >"$dst"
chmod 755 "$dst"
