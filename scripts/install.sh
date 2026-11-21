#!/usr/bin/env bash
# install.sh: install mash and mashctl from one release.
#
#     curl -fsSL https://github.com/bogworks/mash/releases/latest/download/install.sh | bash
#
# It finds this machine's type (linux or darwin, amd64 or arm64), downloads
# mash and mashctl and the release's SHA256SUMS, checks each binary against
# them, and puts the two binaries in ~/.local/bin (MASH_INSTALL_DIR names
# another folder). It asks nothing and needs no sudo. If gh is installed it
# first verifies SHA256SUMS against the release's attestation.
#
# Environment:
#   MASH_INSTALL_DIR   the folder to install into (default ~/.local/bin)
#   MASH_VERSION       pin a release, for example v0.5.6 (default: the latest,
#                      which is resolved to its tag first, so the attestation
#                      is always checked against that tag)
#   MASH_REPO          the GitHub repository (default bogworks/mash): used for
#                      the download address, the tag lookup and the attestation
#                      check (-R and the release workflow's signer)
#   MASH_RELEASE_BASE  where the release files are (an https:// address, a
#                      mirror); needs MASH_VERSION, which names the tag the
#                      attestation is checked against
#
# Everything is inside main, called on the last line: a download cut short
# runs nothing.
set -euo pipefail

REPO="${MASH_REPO:-bogworks/mash}"
PROTO="=https"
WORKDIR=""
BASE=""
STAGED=()  # temporary files made in the install folder
FETCHED=() # files downloaded into WORKDIR

cleanup() {
    local f
    for f in "${STAGED[@]+"${STAGED[@]}"}"; do
        [ -n "$f" ] && rm -f -- "$f"
    done
    if [ -n "$WORKDIR" ] && [ -d "$WORKDIR" ]; then
        for f in "${FETCHED[@]+"${FETCHED[@]}"}"; do
            rm -f -- "$f"
        done
        rmdir -- "$WORKDIR" 2>/dev/null || true
    fi
}

die() {
    echo "error: $*" >&2
    exit 1
}

# sha256_of FILE: the hex SHA-256 of FILE, with sha256sum or shasum.
sha256_of() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum -- "$1" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 -- "$1" | awk '{print $1}'
    else
        die "neither sha256sum nor shasum was found; cannot check the download"
    fi
}

# fetch NAME: download NAME from the release into WORKDIR.
fetch() {
    FETCHED+=("$WORKDIR/$1")
    curl -fsSL --proto "$PROTO" --proto-redir '=https' --tlsv1.2 -o "$WORKDIR/$1" "$BASE/$1" ||
        die "could not download $BASE/$1"
}

# tilde PATH: PATH with a leading $HOME shown as ~.
tilde() {
    case "$1" in
    "$HOME") echo "~" ;;
    "$HOME"/*) echo "~${1#"$HOME"}" ;;
    *) echo "$1" ;;
    esac
}

# unsafe_path DIR: DIR is a real path (no symlinks). Say why it may not be
# installed into, or print nothing: DIR owned by someone other than this user
# or root, or DIR or any folder above it writable by group or others (except a
# sticky folder owned by root above DIR, such as /tmp).
unsafe_path() {
    local target=$1 c perms owner me
    me=$(id -u)
    c=$target
    while :; do
        # shellcheck disable=SC2012 # ls -ld is the portable way to read a mode
        perms=$(ls -ld -- "$c" | awk '{print $1}')
        # shellcheck disable=SC2012
        owner=$(ls -ldn -- "$c" | awk '{print $3}')
        if [ "$c" = "$target" ] && [ "$owner" != "$me" ] && [ "$owner" != 0 ]; then
            echo "$c is owned by another user"
            return
        fi
        case "${perms:5:1}${perms:8:1}" in
        *w*)
            if [ "$c" = "$target" ] || [ "${perms:9:1}" != t ] || [ "$owner" != 0 ]; then
                echo "$c is writable by its group or other users"
                return
            fi
            ;;
        esac
        [ "$c" = / ] && return
        c=$(dirname -- "$c")
    done
}

main() {
    local os arch uname_s uname_m dir real tag sumsname name want got tmp line shown resolved
    local -a names staged_new

    [[ $REPO =~ ^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$ ]] || die "MASH_REPO $REPO is not owner/name"

    # The machine's type.
    uname_s=$(uname -s | tr '[:upper:]' '[:lower:]')
    uname_m=$(uname -m)
    case "$uname_m" in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) arch=$uname_m ;;
    esac
    os=$uname_s
    case "$os/$arch" in
    linux/amd64 | linux/arm64 | darwin/amd64 | darwin/arm64) ;;
    *) die "$os/$arch is not a platform mash is built for" ;;
    esac

    # Which release: always a tag, so the attestation can be tied to it.
    local semver='^v[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?$'
    tag=""
    if [ -n "${MASH_VERSION:-}" ]; then
        tag=$MASH_VERSION
        case "$tag" in v*) ;; *) tag="v$tag" ;; esac
        [[ $tag =~ $semver ]] ||
            die "MASH_VERSION $MASH_VERSION is not a version such as v0.5.6"
    fi
    if [ -n "${MASH_RELEASE_BASE:-}" ]; then
        [ -n "$tag" ] || die "set MASH_VERSION with MASH_RELEASE_BASE"
        BASE=${MASH_RELEASE_BASE%/}
    else
        if [ -z "$tag" ]; then
            resolved=$(curl -fsSLI --proto '=https' --proto-redir '=https' --tlsv1.2 -o /dev/null -w '%{url_effective}' \
                "https://github.com/$REPO/releases/latest") ||
                die "could not find the latest release of $REPO"
            case "$resolved" in
            */releases/tag/*) ;;
            *) die "the latest release of $REPO did not resolve to a release tag: $resolved" ;;
            esac
            tag=${resolved##*/}
            [[ $tag =~ $semver ]] || die "the latest release's tag $tag is not a version such as v0.5.6"
        fi
        BASE="https://github.com/$REPO/releases/download/$tag"
    fi
    case "$BASE" in
    https://*) ;;
    *) die "the release location must be an https:// address: $BASE" ;;
    esac
    echo "Fetching mash and mashctl from the $tag release: $BASE"

    # The folder, and whether it is safe to write into. A symbolic link to it
    # is followed; the real folder and every folder above it are checked.
    [ -n "${HOME:-}" ] || die "HOME is not set"
    dir=${MASH_INSTALL_DIR:-$HOME/.local/bin}
    dir=${dir%/}
    [ -n "$dir" ] || die "MASH_INSTALL_DIR names the root folder"
    case "$dir" in /*) ;; *) die "MASH_INSTALL_DIR must be an absolute path: $dir" ;; esac
    if [ ! -e "$dir" ]; then
        (umask 022 && mkdir -p -- "$dir") || die "could not make $dir"
    fi
    [ -d "$dir" ] || die "$dir is not a folder"
    real=$(cd -P -- "$dir" && pwd -P) || die "could not enter $dir"
    tmp=$(unsafe_path "$real")
    [ -z "$tmp" ] || die "$tmp: not installing there"
    for name in mash mashctl; do
        [ ! -d "$real/$name" ] || die "$real/$name is a folder: not installing there"
    done

    # A private place to download to, removed on exit.
    WORKDIR=$(mktemp -d "${TMPDIR:-/tmp}/mash-install.XXXXXX") || die "could not make a temporary folder"
    chmod 0700 "$WORKDIR"
    trap cleanup EXIT

    sumsname=SHA256SUMS
    fetch "$sumsname"

    # The attestation of SHA256SUMS, when gh is there and new enough to check it.
    if ! command -v gh >/dev/null 2>&1; then
        echo "gh not found: the checksums come from the same download, so this trusts github.com's TLS; see docs/install.md to verify the attestation"
    elif ! gh attestation verify --help >/dev/null 2>&1; then
        echo "gh is too old to verify attestations: the checksums come from the same download, so this trusts github.com's TLS; see docs/install.md to verify the attestation"
    else
        fetch mash.intoto.jsonl
        (cd "$WORKDIR" && gh attestation verify "$sumsname" -R "$REPO" --bundle mash.intoto.jsonl \
            --signer-workflow "$REPO/.github/workflows/release.yml" --source-ref "refs/tags/$tag" >/dev/null) ||
            die "the attestation of the release's SHA256SUMS did not verify: nothing was installed"
    fi

    # Download each binary and hold it to the release's checksums.
    names=("mash-$os-$arch" "mashctl-$os-$arch")
    for name in "${names[@]}"; do
        want=$(awk -v n="$name" '$2 == n || $2 == "*" n { print $1; exit }' "$WORKDIR/$sumsname")
        [ -n "$want" ] || die "SHA256SUMS has no entry for $name: nothing was installed"
        fetch "$name"
        got=$(sha256_of "$WORKDIR/$name")
        [ "$got" = "$want" ] || die "$name does not match SHA256SUMS: nothing was installed"
    done

    # Stage both binaries (and a copy of what they replace) in the folder, then
    # rename them back to back; if the second rename fails the first is undone.
    staged_new=()
    for name in "${names[@]}"; do
        tmp=$(mktemp "$real/.${name%%-*}.XXXXXX") || die "could not write in $real"
        STAGED+=("$tmp")
        cp -- "$WORKDIR/$name" "$tmp"
        chmod 0755 "$tmp"
        staged_new+=("$tmp")
    done
    local backup=""
    if [ -f "$real/mash" ]; then
        backup=$(mktemp "$real/.mash-old.XXXXXX") || die "could not write in $real"
        STAGED+=("$backup")
        cp -p -- "$real/mash" "$backup"
    fi
    mv -f -- "${staged_new[0]}" "$real/mash" || die "could not install mash: nothing was installed"
    if ! mv -f -- "${staged_new[1]}" "$real/mashctl"; then
        if [ -n "$backup" ]; then mv -f -- "$backup" "$real/mash"; else rm -f -- "$real/mash"; fi
        die "could not install mashctl: nothing was installed"
    fi
    STAGED=()
    [ -z "$backup" ] || rm -f -- "$backup"

    shown=$(tilde "$dir")
    for name in mash mashctl; do
        line=$("$real/$name" version 2>/dev/null | head -n 1) || true
        [ -n "$line" ] || die "the installed $name did not report its version"
        echo "Installed $line in $shown"
    done

    case ":$PATH:" in
    *":$dir:"*) ;;
    *) echo "$shown is not on your PATH; add it to use mash" ;;
    esac
}

main "$@"
