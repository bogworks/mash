#!/usr/bin/env bash
# Stored-mail check: mail written by released mash binaries, classified by sapsdk.Classify, in a throwaway container.
#   scripts/storedmail/run.sh [tag...]      default tags: v0.5.8 v0.5.7 v0.5.6
# The release binaries are downloaded on the host into a fresh temp dir and are never run here; the container mounts that
# dir read-only and runs them, with the NATS server, itself. Exits non-zero on a candidate or an unexpected class.
# Env: MASH_RELEASE_REPO  GitHub repo the releases come from (default bogworks/mash).
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
tags=("$@")
[ ${#tags[@]} -gt 0 ] || tags=(v0.5.8 v0.5.7 v0.5.6)
repo=${MASH_RELEASE_REPO:-bogworks/mash}
if command -v sha256sum >/dev/null 2>&1; then sha256() { sha256sum "$1"; }; else sha256() { shasum -a 256 "$1"; }; fi
bins=$(mktemp -d)
name=mash-storedmail-$$-$RANDOM
# No rm on the host: the download dir is moved to ~/archive (herd rule), the container is removed by name.
archive="${HOME:?}/archive"
trap 'mkdir -p -- "${archive:?}" && mv -- "${bins:?}" "${archive:?}/${name:?}-bins"; docker rm -f "$name" >/dev/null 2>&1 || true' EXIT
echo "download dir: $bins"

for t in "${tags[@]}"; do
	mkdir "$bins/$t"
	curl -fsSL -o "$bins/$t/mash-linux-amd64" "https://github.com/$repo/releases/download/$t/mash-linux-amd64"
	got=$(sha256 "$bins/$t/mash-linux-amd64" | cut -d' ' -f1)
	want=$(gh release view "$t" --repo "$repo" --json assets --jq '.assets[]|select(.name=="mash-linux-amd64")|.digest' 2>/dev/null || true)
	if [ -z "$want" ]; then # no digest, no integrity check: refuse rather than run an unchecked binary
		echo "no release digest for $t mash-linux-amd64; refusing to use it unchecked" >&2
		exit 1
	fi
	if [ "$want" != "sha256:$got" ]; then
		echo "sha256 mismatch for $t: got $got, release says $want" >&2
		exit 1
	fi
	echo "$t sha256 $got (matches the release digest)"
done

docker run --rm --name "$name" -e MASH_STOREDMAIL_IN_CONTAINER=1 -v "$root":/src:ro -v "$bins":/bins:ro golang:1.27 sh -c '
	set -e
	mkdir /work && tar -C /src --exclude=./.git --exclude=./mash -cf - . | tar -C /work -xf -
	cd /work && go run ./scripts/storedmail/classify /bins "$@"
' sh "${tags[@]}"
