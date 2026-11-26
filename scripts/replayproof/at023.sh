#!/usr/bin/env bash
# AT-023 (REQ-022): the restart-replay proof, in one throwaway container created for the run and destroyed at the end.
#   scripts/replayproof/at023.sh [--commit REF] [--version 0.6.0] [--oracle-version 0.5.10] [--out DIR] [--known-issue URL] [--release-repo OWNER/REPO]
# --known-issue is the link to the known-0.6.0 entry; without it the run fails (REQ-022: the entry is linked).
# --release-repo is the GitHub repository whose releases hold the oracle (default bogworks/mash).
# Inside the container (image golang:1.27, a non-root user, no bind mount of the checkout): everything it gets arrives as
# one tar archive on stdin: the git objects of the commit (git pack-objects, staged as commit.pack in DIR) and the runner's
# tree (go.mod, go.sum, cmd, internal and the scripts the runner imports). The runner's unit tests run; a clean clone of the candidate
# commit is rebuilt from the git objects; the runner (from this working tree) downloads the oracle
# release and checks it against that release's SHA256SUMS, builds the candidate with scripts/buildrelease, and runs the
# replay on each build against its own throwaway NATS server. The record (record.json), the log (run.log), the captured
# messages and the launcher's venue lines are written to DIR on the host; nothing else is created on the host.
# The same runner runs directly in a throwaway VM guest: see scripts/replayproof/at023/main.go for its flags.
# Exit 0 only when every assertion holds.
set -uo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
image=golang:1.27
name=mash-at023-$$-$RANDOM
version=0.6.0
oracle=0.5.10
ref=HEAD
known=
repo=bogworks/mash
out=
while [ $# -gt 0 ]; do
	case $1 in
	--commit) ref=${2:?--commit needs a value}; shift 2 ;;
	--version) version=${2:?}; shift 2 ;;
	--oracle-version) oracle=${2:?}; shift 2 ;;
	--out) out=${2:?}; shift 2 ;;
	--known-issue) known=${2:?}; shift 2 ;;
	--release-repo) repo=${2:?}; shift 2 ;;
	*) echo "usage: at023.sh [--commit REF] [--version X.Y.Z] [--oracle-version X.Y.Z] [--out DIR] [--known-issue URL] [--release-repo OWNER/REPO]" >&2; exit 2 ;;
	esac
done
[ -n "$out" ] || { echo "--out DIR is required (the record is written there)" >&2; exit 2; }
commit=$(git -C "$root" rev-parse --verify "${ref}^{commit}") || { echo "$ref does not resolve to a commit" >&2; exit 1; }
[ -e "$out/record.json" ] && { echo "$out already holds a record.json from an earlier run: give a new --out directory" >&2; exit 2; }
mkdir -p "$out" || exit 1

cleanup() { docker rm -f "$name" >/dev/null 2>&1 || true; }
trap cleanup EXIT

docker image inspect "$image" >/dev/null 2>&1 || docker pull -q "$image" >/dev/null
imageid=$(docker image inspect "$image" --format '{{.Id}} {{join .RepoDigests ","}}')
status=$(git -C "$root" status --porcelain | grep -v '^?? mashctl$' | tr '\n' ';')
created=$(date -u +%Y-%m-%dT%H:%M:%SZ)
venue="container from $image ($imageid), name $name, docker run --rm --init, user 1000:1000, host $(uname -sm), created $created"

{
	echo "== AT-023 run =="
	echo "candidate commit:    $commit"
	echo "version:             $version   oracle: v$oracle from github.com/$repo"
	echo "runner tree:         this working tree, changes against the commit: ${status:-none}"
	echo "venue:               $venue"
	echo
} | tee "$out/launcher.log"

# The git objects of the commit (read-only on the host) and the runner's files go in as one tar archive on stdin. The pack
# is staged in the output directory and stays there. The checkout itself (.git, untracked files) is never mounted.
git -C "$root" rev-list "$commit" >/dev/null || exit 1
echo "$commit" | git -C "$root" pack-objects --revs --stdout -q >"$out/commit.pack" 2>>"$out/launcher.log" || { echo "git pack-objects failed" >&2; exit 1; }
inputs=(go.mod go.sum cmd internal scripts/replayproof scripts/storedmail scripts/buildrelease)
tar -C "$out" -cf - commit.pack -C "$root" "${inputs[@]}" |
	docker run -i --rm --init --name "$name" --user 1000:1000 \
		-e HOME=/tmp/h -e GOCACHE=/tmp/h/gocache -e GOPATH=/tmp/h/gopath -e GOFLAGS=-buildvcs=false \
		-e COMMIT="$commit" -e VERSION="$version" -e ORACLE="$oracle" -e KNOWN="$known" -e REPO="$repo" -e VENUE="$venue" \
		"$image" bash -c '
	set -euo pipefail
	mkdir -p /tmp/h /tmp/at023/src /tmp/at023/tree /tmp/at023/out /tmp/at023/work
	tar -C /tmp/at023/tree -xf -
	(cd /tmp/at023/src && git init -q && git unpack-objects -q < /tmp/at023/tree/commit.pack && git checkout -q --detach "$COMMIT" \
		&& test "$(git rev-parse HEAD)" = "$COMMIT" && test -z "$(git status --porcelain --untracked-files=all --ignored=matching)") >&2 \
		|| { echo "FAIL: a clean clone at $COMMIT" >&2; exit 1; }
	cd /tmp/at023/tree
	echo "== unit tests ==" >&2
	go test -count=1 ./scripts/replayproof/... >&2 || { echo "FAIL: unit tests" >&2; exit 1; }
	go build -o /tmp/at023/at023 ./scripts/replayproof/at023 >&2
	rc=0
	/tmp/at023/at023 -src /tmp/at023/src -commit "$COMMIT" -version "$VERSION" -oracle-version "$ORACLE" \
		-out /tmp/at023/out -work /tmp/at023/work -known-issue "$KNOWN" -release-repo "$REPO" -venue "$VENUE" >&2 || rc=$?
	cp /tmp/at023/work/oracle/capture.json /tmp/at023/out/capture-oracle.json 2>/dev/null || true
	cp /tmp/at023/work/candidate/capture.json /tmp/at023/out/capture-candidate.json 2>/dev/null || true
	for role in oracle candidate; do # the stderr of the serve processes, for a failed run (no secrets: mash prints none)
		for f in /tmp/at023/work/$role/serve-*.stderr; do [ -f "$f" ] && cp "$f" "/tmp/at023/out/$role-$(basename "$f")"; done
	done
	echo "$rc" > /tmp/at023/out/exit-code
	tar -C /tmp/at023/out -cf - .
	exit "$rc"
' | tar -x -C "$out"
ps=("${PIPESTATUS[@]}")
rc=0
[ "${ps[0]}" = 0 ] || { echo "FAIL: building the input archive (tar exit ${ps[0]})" | tee -a "$out/launcher.log" >&2; rc=1; }
if [ "${ps[2]}" != 0 ]; then echo "FAIL: extracting the container's output (tar exit ${ps[2]})" | tee -a "$out/launcher.log" >&2; rc=1; fi
if [ "${ps[1]}" != 0 ]; then rc=${ps[1]}; fi
if [ ! -s "$out/record.json" ]; then echo "FAIL: $out/record.json is missing after extraction" | tee -a "$out/launcher.log" >&2; rc=1; fi

if docker ps -a --format '{{.Names}}' | grep -qx "$name"; then
	echo "container NOT destroyed: $name" | tee -a "$out/launcher.log" >&2
	docker rm -f "$name" >/dev/null 2>&1 || true
	rc=1
else
	echo "container destroyed: $name (docker run --rm), $(date -u +%Y-%m-%dT%H:%M:%SZ)" | tee -a "$out/launcher.log"
fi
echo "venue: $venue" >"$out/venue.txt"
echo "destroyed: $(date -u +%Y-%m-%dT%H:%M:%SZ), container $name" >>"$out/venue.txt"
if [ "$rc" = 0 ]; then echo "AT-023 RESULT: PASS" | tee -a "$out/launcher.log"; else echo "AT-023 RESULT: FAIL (exit $rc)" | tee -a "$out/launcher.log"; fi
exit "$rc"
