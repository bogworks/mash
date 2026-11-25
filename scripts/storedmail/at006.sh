#!/usr/bin/env bash
# AT-006 and AT-018: the stored-mail proof, in one throwaway container created for the run and destroyed at the end.
#   scripts/storedmail/at006.sh
# Inside the container (image golang:1.27): the release list is derived from the published releases, each release's
# mash-linux-amd64 is downloaded and checked against its digest, each release's own `mash` writes stored mail, and this
# tree's endpoint code and `mash inbox` read it (zero refusals, every message delivered once, X1 to X3 count 0); then the
# duplicate cases of AT-018 run on a copy of the oracle base ($AT006_BASE, default origin/main) and on this tree, and their outcomes must be identical.
# Nothing is created on the host, no directory and no file: `git archive $AT006_BASE` is streamed into the container on
# stdin and extracted there, the container is started and the receipt is printed. The released binaries are downloaded
# and run only inside the container.
# Env: AT006_BASE       git ref of the AT-018 oracle, printed in the receipt. Proof runs use origin/main (the default); any
#                       other value is a debugging run that exits non-zero; a base equal to the tested commit is refused.
#      MASH_RELEASE_REPO  GitHub repo the releases come from (default bogworks/mash).
#      AT006_TAGS      space-separated tags replacing the derived list; debugging only, the run exits non-zero.
#      AT006_DIRTY=1   run on a tree with uncommitted changes (the receipt says so; not a proof run).
set -uo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
image=golang:1.27
name=mash-at006-$$-$RANDOM
rc=0

cleanup() {
	# the run creates nothing on the host; remove only the container, by its own recorded name
	docker rm -f "$name" >/dev/null 2>&1 || true
}
trap cleanup EXIT

commit=$(git -C "$root" rev-parse HEAD)
base=${AT006_BASE:-origin/main}
main=$(git -C "$root" rev-parse --verify "${base}^{commit}") || { echo "AT006_BASE $base does not resolve to a commit in this repository" >&2; exit 1; }
if [ "$main" = "$commit" ]; then
	echo "the oracle base $base is the tested commit $commit; AT-018 needs a base distinct from the tree" >&2
	exit 1
fi
if [ "$base" != origin/main ]; then
	echo "AT006_BASE override ($base): not a proof run; the run exits non-zero"
	rc=1
fi
tree=clean
if [ -n "$(git -C "$root" status --porcelain)" ]; then
	tree="DIRTY (uncommitted changes: not a proof run)"
	if [ "${AT006_DIRTY:-}" != 1 ]; then echo "the tree has uncommitted changes; commit them or set AT006_DIRTY=1 for a trial run" >&2; exit 1; fi
	rc=1
fi
docker image inspect "$image" >/dev/null 2>&1 || docker pull -q "$image" >/dev/null
digest=$(docker image inspect "$image" --format '{{.Id}} {{join .RepoDigests ","}}')

echo "== AT-006 / AT-018 run =="
echo "commit tested:       $commit ($tree)"
echo "oracle ($base): $main"
echo "release repo:        ${MASH_RELEASE_REPO:-bogworks/mash}"
echo "venue:               container from $image, image $digest, name $name, docker run --rm --init"
echo

git -C "$root" archive "$main" | docker run -i --rm --init --name "$name" -e AT006_TAGS="${AT006_TAGS:-}" \
	-e MASH_RELEASE_REPO="${MASH_RELEASE_REPO:-bogworks/mash}" -e AT006_IN_CONTAINER=1 \
	-v "$root":/src:ro "$image" bash -c '
	set -uo pipefail
	fail=0
	mkdir /oracle && tar -x -C /oracle || { echo "FAIL: extracting git archive of the oracle base from stdin"; exit 1; }
	mkdir /work /out
	oracle_test() { # dir out: run the AT-018 test in dir, write its outcomes to out, and return the test exit code
		(cd "$1" && MASH_AT018_OUT="$2" go test ./internal/endpoint -run "^TestStoredDuplicateOracle\$" -count=1 -v >/out/test.log 2>&1)
		local c=$?
		grep -E "^\s*(---|ok|FAIL|PASS|panic)|_test.go:" /out/test.log
		return $c
	}
	tar -C /src --exclude=./.git --exclude=./mash -cf - . | tar -C /work -xf -
	cp /work/internal/endpoint/stored_dup_oracle_test.go /oracle/internal/endpoint/
	echo "== AT-018 on the oracle base =="
	oracle_test /oracle /out/main.json || { echo "FAIL: the oracle test did not pass on the oracle base"; fail=1; }
	echo "== AT-018 on this tree =="
	oracle_test /work /out/tree.json || { echo "FAIL: the oracle test did not pass on this tree"; fail=1; }
	if [ -s /out/main.json ] && [ -s /out/tree.json ]; then
		if diff -u /out/main.json /out/tree.json; then
			echo "AT-018 diff: IDENTICAL ($(grep -c "\"delivered\"" /out/tree.json) cases; sha256 $(sha256sum < /out/tree.json | cut -d" " -f1))"
		else
			echo "AT-018 diff: DIFFERENT"; fail=1
		fi
	else
		echo "AT-018 diff: NOT COMPARED"; fail=1
	fi
	echo
	echo "== AT-006 =="
	(cd /work && go build -o /out/mash ./cmd/mash) || { echo "FAIL: go build ./cmd/mash"; exit 1; }
	(cd /work && go run ./scripts/storedmail/at006 -mash /out/mash -skipped docs/skipped-releases.txt -work /out/w) || fail=1
	echo
	if [ "$fail" = 0 ]; then echo "container result: PASS"; else echo "container result: FAIL"; fi
	exit $fail
'
crc=$?
[ "$crc" = 0 ] || rc=1

if docker ps -a --format '{{.Names}}' | grep -qx "$name"; then
	echo "container NOT destroyed: $name" >&2
	docker rm -f "$name" >/dev/null 2>&1 || true
	rc=1
else
	echo "container destroyed: $name (docker run --rm)"
fi
if [ "$rc" = 0 ]; then echo "AT-006/AT-018 RESULT: PASS"; else echo "AT-006/AT-018 RESULT: FAIL"; fi
exit $rc
