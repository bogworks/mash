#!/usr/bin/env bash
# at011.sh COMMIT OUTDIR [VERSION [PREVIOUS]]
#
# AT-011 on linux: the upgrade proof of mash, from the previous release (default 0.5.10) to the candidate COMMIT
# (version default 0.6.0). It starts ONE throwaway container from a clean image (`docker run --rm`), runs the proof
# inside it as a user without privileges, copies the receipt, the log and the evidence to OUTDIR, and the container
# is gone when the proof ends. Nothing is written on this host except OUTDIR (which must not exist yet).
#
#   COMMIT   the candidate's full commit id (40 hex digits)
#   OUTDIR   the receipt at011 writes is OUTDIR/at-011-linux-<arch>.json, ready for `release-check --receipts OUTDIR`
#
# Environment: AT011_REPO (the repository to read the candidate from, default the repository this script is in),
# AT011_GH_REPO (the GitHub repository owner/name the previous release is read from; default the owner/name of AT011_REPO
# when that is a github.com URL, else bogworks/mash), AT011_IMAGE (default golang:1.27), AT011_ARCH (amd64 or arm64,
# default amd64).
#
# The previous release's SHA256SUMS comes from the same place as the files it vouches for, so it is verified here, on
# this host, before the guest sees it: `gh attestation verify` on SHA256SUMS with the release workflow, the tag and the
# commit of the tag pinned. The guest is handed only the digest verified and refuses any other SHA256SUMS.
#
# On a macOS or arm64 VM guest there is no docker: build the runner there (go build -o at011 ./scripts/upgradeproof/at011),
# make a pack of the candidate (see below), and run `at011 run` directly in the guest; see the header of at011/main.go.
#
# Needs on this host: docker (rootless is fine), git, go, curl and gh (GitHub CLI; it needs no login for a public repository
# but a token in its environment is used if present and never printed). It starts no mash, mashctl or server on this host.

set -u
COMMIT=${1:-}
OUT=${2:-}
VERSION=${3:-0.6.0}
PREV=${4:-0.5.10}
IMAGE=${AT011_IMAGE:-golang:1.27}
ARCH=${AT011_ARCH:-amd64}
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=${AT011_REPO:-$HERE/../..}

die() { echo "at011.sh: $*" >&2; exit 2; }
case $COMMIT in [0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]*) ;; *) die "usage: at011.sh COMMIT OUTDIR [VERSION [PREVIOUS]]" ;; esac
[ "${#COMMIT}" = 40 ] || die "COMMIT must be the full 40-digit id"
[ -n "$OUT" ] || die "usage: at011.sh COMMIT OUTDIR [VERSION [PREVIOUS]]"
case $OUT in /*) ;; *) OUT=$PWD/$OUT ;; esac # a bind mount needs an absolute path
OUT=${OUT%/}
[ ! -e "$OUT" ] || die "$OUT exists; the output directory must be new"
git -C "$REPO" cat-file -e "$COMMIT^{commit}" || die "the commit is not in $REPO"

command -v gh >/dev/null 2>&1 || die "gh (the GitHub CLI) is not installed on this host; it is needed to verify the previous release's SHA256SUMS (gh attestation verify). Install it from https://cli.github.com and run again"
command -v curl >/dev/null 2>&1 || die "curl is not installed on this host"
GHREPO=${AT011_GH_REPO:-}
if [ -z "$GHREPO" ]; then
  GHREPO=$(printf '%s' "$REPO" | sed -n 's#^\(https://\|git@\)github\.com[/:]\([A-Za-z0-9_.-]*/[A-Za-z0-9_.-]*\)$#\2#p' | sed 's#\.git$##')
  [ -n "$GHREPO" ] || GHREPO=bogworks/mash
fi
case $GHREPO in */*) ;; *) die "AT011_GH_REPO must be owner/name" ;; esac

mkdir -p "$OUT/stage" "$OUT/home" || exit 2
STAGE=$OUT/stage
# The runner, built here for the guest's platform; and a pack of the candidate commit's objects (a read of the repository).
(cd "$HERE/.." && CGO_ENABLED=0 GOOS=linux GOARCH=$ARCH go build -trimpath -o "$STAGE/at011" ./upgradeproof/at011) || die "could not build the runner"
# The previous release's SHA256SUMS, verified on this host. The tag's commit is read from the GitHub API, then the
# attestation is checked against four facts: the repository, the release workflow, the tag and that commit.
PREVSHA=$(gh api "repos/$GHREPO/commits/v$PREV" --jq .sha 2>/dev/null)
case $PREVSHA in [0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]*) ;; *) die "could not read the commit of tag v$PREV in $GHREPO (gh api repos/$GHREPO/commits/v$PREV)" ;; esac
curl -fsSL -o "$STAGE/prev-SHA256SUMS" "https://github.com/$GHREPO/releases/download/v$PREV/SHA256SUMS" || die "could not download v$PREV SHA256SUMS from $GHREPO"
gh attestation verify "$STAGE/prev-SHA256SUMS" -R "$GHREPO" --signer-workflow "$GHREPO/.github/workflows/release.yml" \
  --source-ref "refs/tags/v$PREV" --source-digest "$PREVSHA" >"$OUT/prev-sums-attestation.txt" 2>&1 \
  || { cat "$OUT/prev-sums-attestation.txt" >&2; die "the attestation of v$PREV SHA256SUMS did not verify; nothing was run"; }
PREVSUMS=$( (sha256sum "$STAGE/prev-SHA256SUMS" 2>/dev/null || shasum -a 256 "$STAGE/prev-SHA256SUMS") | cut -d' ' -f1)
echo "v$PREV SHA256SUMS sha256 $PREVSUMS: attestation verified on this host (repository $GHREPO, tag commit $PREVSHA)"
echo "$COMMIT" | git -C "$REPO" pack-objects --revs --stdout >"$STAGE/candidate.pack" || die "could not pack the candidate"

NAME=at011-$(date -u +%Y%m%dT%H%M%SZ)-$$
echo "starting container $NAME from $IMAGE (linux/$ARCH)"
docker pull -q --platform "linux/$ARCH" "$IMAGE" >/dev/null || die "could not pull $IMAGE"
DIGEST=$(docker image inspect --format '{{index .RepoDigests 0}}' "$IMAGE" 2>/dev/null)
[ -n "$DIGEST" ] || DIGEST=$IMAGE

# Inside, as root: the packages the setup script needs (ss, openssl), a user made for the run, then the proof as that
# user; afterwards, still as root, the results go to /out. Nothing else leaves the container.
INNER='
set -u
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq >/dev/null 2>&1 && apt-get install -y -qq --no-install-recommends iproute2 procps openssl unzip ca-certificates curl git >/dev/null 2>&1 || { echo "at011: apt failed" >&2; exit 3; }
useradd -m -s /bin/bash at011 || exit 3
install -d -o at011 -g at011 /home/at011/out
runuser -u at011 -- env HOME=/home/at011 PATH="$PATH" /in/at011 run \
  -commit "$AT011_COMMIT" -version "$AT011_VERSION" -prev "$AT011_PREV" -pack /in/candidate.pack \
  -out /home/at011/out -work /home/at011/at011-work -venue "$AT011_VENUE" \
  -release-repo "$AT011_GHREPO" -prev-sums-sha256 "$AT011_PREV_SUMS"
rc=$?
cp -R /home/at011/out/. /out/ || rc=4
# The copies belong to whoever owns /out as this container sees it: the host user when the daemon is rootful (or podman
# runs with --userns=keep-id), root when it is rootless (root in the container is the host user). Never a fixed uid.
chown -R "$(stat -c %u:%g /out)" /out || rc=4
exit $rc
'
VENUE="a throwaway docker container $NAME (image $DIGEST, linux/$ARCH, started with docker run --rm), run as an unprivileged user"
docker run --rm --init --platform "linux/$ARCH" --name "$NAME" \
  -v "$STAGE:/in:ro" -v "$OUT/home:/out" \
  -e AT011_COMMIT="$COMMIT" -e AT011_VERSION="$VERSION" -e AT011_PREV="$PREV" -e AT011_VENUE="$VENUE" \
  -e AT011_GHREPO="$GHREPO" -e AT011_PREV_SUMS="$PREVSUMS" \
  "$IMAGE" bash -c "$INNER"
RC=$?
echo "container exit status: $RC"

# The container was started with --rm: show it is gone.
REMOVED=true
if docker ps -a --filter "name=^$NAME\$" --format '{{.Names}}' | grep -q .; then
  REMOVED=false
  STATE="the container $NAME STILL EXISTS after the run"
else
  STATE="the container $NAME was removed when the run ended (docker run --rm; docker ps -a no longer lists it); its exit status was $RC"
fi
echo "$STATE"
# The results sit in OUTDIR/home (what the container copied out); move them up so OUTDIR is what release-check reads.
mv "$OUT/home/"* "$OUT/" 2>/dev/null
rmdir "$OUT/home" 2>/dev/null
"$STAGE/at011" finalize -out "$OUT" -statement "$STATE" -container-removed=$REMOVED || exit 1
[ "$REMOVED" = true ] || exit 1 # a surviving container fails the receipt; the script does not pass either
exit $RC
