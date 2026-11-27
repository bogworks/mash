#!/usr/bin/env bash
# AT-023 (REQ-022): the restart-replay proof, in one throwaway container created for the run and destroyed at the end.
#   scripts/replayproof/at023.sh [--commit REF] [--version 0.6.0] [--oracle-version 0.5.10] [--out DIR] [--known-issue URL] [--release-repo OWNER/REPO] [--container-tmp DIR]
# --known-issue is the link to the known-0.6.0 entry; without it the run fails (REQ-022: the entry is linked).
# With --known-issue, the launcher first checks on the host, read-only with gh (never printing a token), that the issue exists in
# the release repository and carries the label known-<version>; if gh is missing or the check fails, it exits 2 before the
# container starts. The result goes into the container as KNOWN_VERIFIED and into the record (known_issue.verified_by).
# --release-repo is the GitHub repository whose releases hold the oracle (default bogworks/mash).
# --container-tmp is the container's scratch directory, passed as its TMPDIR (default: the image's TMPDIR, or /tmp). Everything the
# container writes (HOME, caches, source, tree, work, output, the built runner) goes in a directory under it.
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
image=golang:1.27@sha256:5f1b98fa02fe396ebce2a0227a0e08309e11792915b9dd2e1f71053d7e7e6666
name=mash-at023-$$-$RANDOM
version=0.6.0
oracle=0.5.10
ref=HEAD
known=
repo=bogworks/mash
ctmp=
out=
while [ $# -gt 0 ]; do
	case $1 in
	--commit) ref=${2:?--commit needs a value}; shift 2 ;;
	--version) version=${2:?}; shift 2 ;;
	--oracle-version) oracle=${2:?}; shift 2 ;;
	--out) out=${2:?}; shift 2 ;;
	--known-issue) known=${2:?}; shift 2 ;;
	--release-repo) repo=${2:?}; shift 2 ;;
	--container-tmp) ctmp=${2:?}; shift 2 ;;
	*) echo "usage: at023.sh [--commit REF] [--version X.Y.Z] [--oracle-version X.Y.Z] [--out DIR] [--known-issue URL] [--release-repo OWNER/REPO] [--container-tmp DIR]" >&2; exit 2 ;;
	esac
done
[ -n "$out" ] || { echo "--out DIR is required (the record is written there)" >&2; exit 2; }
commit=$(git -C "$root" rev-parse --verify "${ref}^{commit}") || { echo "$ref does not resolve to a commit" >&2; exit 1; }
[ -e "$out/record.json" ] && { echo "$out already holds a record.json from an earlier run: give a new --out directory" >&2; exit 2; }

# The known-issue check: on the host, where gh and its login are. The container has no token.
knownverified=
if [ -n "$known" ]; then
	command -v gh >/dev/null 2>&1 || { echo "--known-issue given but gh is not installed: cannot confirm the issue and its known-$version label (exit 2)" >&2; exit 2; }
	[[ $known =~ ^https://github\.com/$repo/issues/([1-9][0-9]*)$ ]] || { echo "--known-issue must be exactly https://github.com/$repo/issues/<number>, got: $known" >&2; exit 2; }
	n=${BASH_REMATCH[1]}
	info=$(gh issue view "$n" -R "$repo" --json state,labels,number --jq '[.number,.state,([.labels[].name]|join(","))]|@tsv' 2>/dev/null) \
		|| { echo "gh could not read issue $n in $repo (missing, no access, or not logged in): the known-issue check failed (exit 2)" >&2; exit 2; }
	IFS=$'\t' read -r inum istate ilabels <<<"$info"
	[ "$inum" = "$n" ] || { echo "gh returned issue ${inum:-none}, expected $n (exit 2)" >&2; exit 2; }
	case ",$ilabels," in
	*",known-$version,"*) ;;
	*) echo "issue $n in $repo does not carry the label known-$version (labels: ${ilabels:-none}) (exit 2)" >&2; exit 2 ;;
	esac
	knownverified="$(printf '%s' "$istate" | tr '[:upper:]' '[:lower:]'), label known-$version, checked by the launcher with gh at $(date -u +%Y-%m-%dT%H:%M:%SZ)"
fi
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
echo "known issue:         ${known:-none}${knownverified:+ ($knownverified)}"
	echo
} | tee "$out/launcher.log"

# The git objects of the commit (read-only on the host) and the runner's files go in as one tar archive on stdin. The pack
# is staged in the output directory and stays there. The checkout itself (.git, untracked files) is never mounted.
git -C "$root" rev-list "$commit" >/dev/null || exit 1
echo "$commit" | git -C "$root" pack-objects --revs --stdout -q >"$out/commit.pack" 2>>"$out/launcher.log" || { echo "git pack-objects failed" >&2; exit 1; }
inputs=(go.mod go.sum cmd internal scripts/replayproof scripts/storedmail scripts/buildrelease)
tar -C "$out" -cf - commit.pack -C "$root" "${inputs[@]}" |
	docker run -i --rm --init --name "$name" --user 1000:1000 ${ctmp:+-e TMPDIR="$ctmp"} -e GOFLAGS=-buildvcs=false \
		-e COMMIT="$commit" -e VERSION="$version" -e ORACLE="$oracle" -e KNOWN="$known" -e KNOWN_VERIFIED="$knownverified" -e REPO="$repo" -e VENUE="$venue" \
		"$image" bash -c '
	set -euo pipefail
	S=${TMPDIR:-/tmp}/at023
	export HOME=$S/h GOCACHE=$S/h/gocache GOPATH=$S/h/gopath
	mkdir -p "$HOME" "$S/src" "$S/tree" "$S/out" "$S/work"
	tar -C $S/tree -xf -
	(cd $S/src && git init -q && git unpack-objects -q < $S/tree/commit.pack && git checkout -q --detach "$COMMIT" \
		&& test "$(git rev-parse HEAD)" = "$COMMIT" && test -z "$(git status --porcelain --untracked-files=all --ignored=matching)") >&2 \
		|| { echo "FAIL: a clean clone at $COMMIT" >&2; exit 1; }
	cd $S/tree
	echo "== unit tests ==" >&2
	go test -count=1 ./scripts/replayproof/... >&2 || { echo "FAIL: unit tests" >&2; exit 1; }
	go build -o $S/at023 ./scripts/replayproof/at023 >&2
	rc=0
	$S/at023 -src $S/src -commit "$COMMIT" -version "$VERSION" -oracle-version "$ORACLE" \
		-out $S/out -work $S/work -known-issue "$KNOWN" -known-issue-verified "$KNOWN_VERIFIED" -release-repo "$REPO" -venue "$VENUE" >&2 || rc=$?
	cp $S/work/oracle/capture.json $S/out/capture-oracle.json 2>/dev/null || true
	cp $S/work/candidate/capture.json $S/out/capture-candidate.json 2>/dev/null || true
	for role in oracle candidate; do # the stderr of the serve processes, for a failed run (no secrets: mash prints none)
		for f in $S/work/$role/serve-*.stderr; do [ -f "$f" ] && cp "$f" "$S/out/$role-$(basename "$f")"; done
	done
	echo "$rc" > $S/out/exit-code
	tar -C $S/out -cf - .
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
