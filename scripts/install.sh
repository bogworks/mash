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
#   MASH_VERSION       pin a release, for example v0.5.6 (default: the latest)
#   MASH_RELEASE_BASE  where the release files are (https:// or, for tests,
#                      file://); overrides MASH_VERSION for the download
#
# Everything is inside main, called on the last line: a download cut short
# runs nothing.
set -euo pipefail

REPO="bogworks/mash"
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
    curl -fsSL --proto '=https,file' --tlsv1.2 -o "$WORKDIR/$1" "$BASE/$1" ||
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

# unsafe_dir DIR: say why DIR may not be installed into (a symlink, or
# writable by group or others), or print nothing.
unsafe_dir() {
    local d=$1 perms
    if [ -L "$d" ]; then
        echo "$d is a symbolic link"
        return
    fi
    # shellcheck disable=SC2012 # ls -ld is the portable way to read a mode
    perms=$(ls -ld -- "$d" | awk '{print $1}')
    case "${perms:5:1}${perms:8:1}" in
    *w*) echo "$d is writable by its group or other users" ;;
    esac
}

main() {
    local os arch uname_s uname_m dir tag sumsname name want got tmp line shown
    local -a names

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

    # Where the release is, and which tag it is (for the attestation check).
    tag=""
    if [ -n "${MASH_VERSION:-}" ]; then
        tag=$MASH_VERSION
        case "$tag" in v*) ;; *) tag="v$tag" ;; esac
        [[ $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?$ ]] ||
            die "MASH_VERSION $MASH_VERSION is not a version such as v0.5.6"
    fi
    if [ -n "${MASH_RELEASE_BASE:-}" ]; then
        BASE=${MASH_RELEASE_BASE%/}
    elif [ -n "$tag" ]; then
        BASE="https://github.com/$REPO/releases/download/$tag"
    else
        BASE="https://github.com/$REPO/releases/latest/download"
    fi
    case "$BASE" in
    https://* | file://*) ;;
    *) die "the release location must be an https:// address: $BASE" ;;
    esac
    if [ -z "$tag" ] && [[ ${BASE##*/} =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?$ ]]; then
        tag=${BASE##*/}
    fi

    # The folder, and whether it is safe to write into.
    [ -n "${HOME:-}" ] || die "HOME is not set"
    dir=${MASH_INSTALL_DIR:-$HOME/.local/bin}
    dir=${dir%/}
    [ -n "$dir" ] || die "MASH_INSTALL_DIR names the root folder"
    case "$dir" in /*) ;; *) die "MASH_INSTALL_DIR must be an absolute path: $dir" ;; esac
    if [ ! -e "$dir" ]; then
        (umask 022 && mkdir -p -- "$dir") || die "could not make $dir"
    fi
    [ -d "$dir" ] || die "$dir is not a folder"
    for line in "$dir" "$(dirname -- "$dir")"; do
        tmp=$(unsafe_dir "$line")
        [ -z "$tmp" ] || die "$tmp: not installing there"
    done

    # A private place to download to, removed on exit.
    WORKDIR=$(mktemp -d "${TMPDIR:-/tmp}/mash-install.XXXXXX") || die "could not make a temporary folder"
    chmod 0700 "$WORKDIR"
    trap cleanup EXIT

    sumsname=SHA256SUMS
    fetch "$sumsname"

    # The attestation of SHA256SUMS, when gh is there to check it.
    if command -v gh >/dev/null 2>&1; then
        fetch mash.intoto.jsonl
        local -a flags=(-R "$REPO" --bundle mash.intoto.jsonl --signer-workflow "$REPO/.github/workflows/release.yml")
        [ -z "$tag" ] || flags+=(--source-ref "refs/tags/$tag")
        (cd "$WORKDIR" && gh attestation verify "$sumsname" "${flags[@]}" >/dev/null) ||
            die "the attestation of the release's SHA256SUMS did not verify: nothing was installed"
    else
        echo "gh not found: checked the checksums only (see docs/install.md to verify the attestation)"
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

    # Install: a temporary file in the folder, then an atomic rename.
    for name in "${names[@]}"; do
        tmp=$(mktemp "$dir/.${name%%-*}.XXXXXX") || die "could not write in $dir"
        STAGED+=("$tmp")
        cp -- "$WORKDIR/$name" "$tmp"
        chmod 0755 "$tmp"
        mv -f -- "$tmp" "$dir/${name%%-*}"
    done
    STAGED=()

    shown=$(tilde "$dir")
    for name in mash mashctl; do
        line=$("$dir/$name" version 2>/dev/null | head -n 1) || true
        [ -n "$line" ] || die "the installed $name did not report its version"
        echo "Installed $line in $shown"
    done

    case ":$PATH:" in
    *":$dir:"*) ;;
    *) echo "$shown is not on your PATH; add it to use mash" ;;
    esac
}

main "$@"
