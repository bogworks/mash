#!/usr/bin/env bash
# Stored-mail check: mail written by released mash binaries, classified by sapsdk.Classify, in a throwaway container.
#   scripts/storedmail/run.sh [tag...]      default tags: v0.5.8 v0.5.7 v0.5.6
# The release binaries are downloaded on the host into a fresh temp dir and are never run here; the container mounts that
# dir read-only and runs them, with the NATS server, itself. Exits non-zero on a candidate or an unexpected class.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
tags=("$@")
[ ${#tags[@]} -gt 0 ] || tags=(v0.5.8 v0.5.7 v0.5.6)
repo=bogworks/mash
bins=$(mktemp -d)
name=mash-storedmail-$$-$RANDOM
trap 'rm -rf "$bins"; docker rm -f "$name" >/dev/null 2>&1 || true' EXIT
echo "download dir: $bins"

for t in "${tags[@]}"; do
	mkdir "$bins/$t"
	curl -fsSL -o "$bins/$t/mash-linux-amd64" "https://github.com/$repo/releases/download/$t/mash-linux-amd64"
	got=$(sha256sum "$bins/$t/mash-linux-amd64" | cut -d' ' -f1)
	want=$(gh release view "$t" --repo "$repo" --json assets --jq '.assets[]|select(.name=="mash-linux-amd64")|.digest' 2>/dev/null || true)
	if [ -n "$want" ] && [ "$want" != "sha256:$got" ]; then
		echo "sha256 mismatch for $t: got $got, release says $want" >&2
		exit 1
	fi
	if [ -n "$want" ]; then echo "$t sha256 $got (matches the release digest)"; else echo "$t sha256 $got (no release digest shown)"; fi
done

docker run --rm --name "$name" -v "$root":/src:ro -v "$bins":/bins:ro golang:1.27 sh -c '
	set -e
	mkdir /work && tar -C /src --exclude=./.git --exclude=./mash -cf - . | tar -C /work -xf -
	cd /work && go run ./scripts/storedmail/classify /bins "$@"
' sh "${tags[@]}"
