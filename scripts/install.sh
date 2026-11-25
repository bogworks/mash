#!/usr/bin/env bash
# install.sh: install mash and mashctl from one release.
#
#     curl -fsSL https://github.com/bogworks/mash/releases/latest/download/install.sh | bash
#
# It finds this machine's type (linux or darwin, amd64 or arm64), downloads
# mash and mashctl and the release's SHA256SUMS, checks each binary against
# them, and puts the two binaries in ~/.local/bin (MASH_INSTALL_DIR names
# another folder). It asks nothing and needs no sudo. If gh is installed it
# first verifies SHA256SUMS against the release's attestation, checked against
# the commit the release tag points to.
#
# Environment:
#   MASH_INSTALL_DIR   the folder to install into (default ~/.local/bin)
#   MASH_VERSION       pin a release, for example v0.5.6 (default: the latest,
#                      which is resolved to its tag first, so the attestation
#                      is always checked against that tag)
#   MASH_REPO          the GitHub repository (default bogworks/mash): used for
#                      the download address, the tag lookup and the attestation
#                      check (-R and the release workflow's signer)
#   MASH_API_BASE      where the tag's commit is looked up when gh cannot
#                      (default https://api.github.com)
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
VHOME=""   # the empty home the staged version checks run in, inside WORKDIR

cleanup() {
    local f
    for f in "${STAGED[@]+"${STAGED[@]}"}"; do
        [ -n "$f" ] && rm -f -- "$f"
    done
    if [ -n "$WORKDIR" ] && [ -d "$WORKDIR" ]; then
        for f in "${FETCHED[@]+"${FETCHED[@]}"}"; do
            rm -f -- "$f"
        done
        [ -n "$VHOME" ] && rm -rf -- "$VHOME"
        rmdir -- "$WORKDIR" 2>/dev/null || true
    fi
}

die() {
    echo "error: $*" >&2
    exit 1
}

# version_of PROGRAM NAME: the version PROGRAM reports, without its name or a leading v
# ("mash v0.5.6 (3f9a41c)" and "mashctl 0.5.6" both give 0.5.6); empty if it does not run,
# exits non-zero, or reports a first word other than NAME. PROGRAM runs in an isolated environment (an empty temporary home, no MASH_*
# variables, PATH only), so the user's own configuration cannot make it refuse to report.
version_of() {
    local out
    out=$(version_line "$1") || return 0
    printf '%s\n' "$out" | awk -v want="$2" 'NR == 1 && $1 == want { sub(/^v/, "", $2); print $2 }'
}

# check_staged PROGRAM NAME: die unless the staged PROGRAM reports itself as NAME and the requested version.
check_staged() {
    local ver line other
    ver=$(version_of "$1" "$2")
    if [ -z "$ver" ]; then
        line=$(version_line "$1")
        other=$(printf '%s\n' "$line" | awk '{print $1}' | tr -cd '[:print:]')
        other=${other:0:40}
        if [ -n "$other" ] && [ "$other" != "$2" ]; then
            die "the downloaded $2 reports itself as $other, expected $2: nothing was installed"
        fi
        die "the downloaded $2 did not report its version: nothing was installed"
    fi
    [ "$ver" = "${tag#v}" ] || die "the downloaded $2 reports $ver, expected ${tag#v}: nothing was installed"
}

# version_line PROGRAM: the first line PROGRAM's `version` prints, run as version_of runs it
# (isolated); empty if it does not run or exits non-zero.
version_line() {
    local out
    mkdir -p -- "$VHOME" || return 0 # VHOME is set in main: an assignment here would die with the command substitution
    out=$(env -i HOME="$VHOME" PATH="$PATH" XDG_CONFIG_HOME="$VHOME/config" \
        XDG_STATE_HOME="$VHOME/state" XDG_DATA_HOME="$VHOME/data" "$1" version 2>/dev/null) || return 0
    printf '%s\n' "$out" | head -n 1
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

# unsafe_path DIR: DIR is a real path (no symlinks). Say, as the whole error
# line, why it may not be installed into, or print nothing. DIR owned by
# someone other than this user or root is refused first and alone. Otherwise
# every folder on the walk up from DIR that is writable by group or others
# (except a sticky folder owned by root above DIR, such as /tmp) is collected,
# nearest first, and ONE line names them all with one fix: chmod go-w with each
# folder in single quotes. With one folder the line reads "<dir> is writable by
# its group or other users: not installing there. Fix: ..."; with several it
# adds "(so is <second>, <third>)" after the first folder's name.
unsafe_path() {
    local target=$1 c perms owner me sq q first="" others="" fix="" n=0
    me=$(id -u)
    sq="'\\''"
    c=$target
    while :; do
        # shellcheck disable=SC2012 # ls -ld is the portable way to read a mode
        perms=$(ls -ld -- "$c" | awk '{print $1}')
        # shellcheck disable=SC2012
        owner=$(ls -ldn -- "$c" | awk '{print $3}')
        if [ "$c" = "$target" ] && [ "$owner" != "$me" ] && [ "$owner" != 0 ]; then
            echo "$c is owned by another user: not installing there"
            return
        fi
        case "${perms:5:1}${perms:8:1}" in
        *w*)
            if [ "$c" = "$target" ] || [ "${perms:9:1}" != t ] || [ "$owner" != 0 ]; then
                q=${c//\'/$sq}
                fix="$fix '$q'"
                if [ "$n" = 0 ]; then
                    first=$c
                elif [ "$n" = 1 ]; then
                    others=$c
                else
                    others="$others, $c"
                fi
                n=$((n + 1))
            fi
            ;;
        esac
        [ "$c" = / ] && break
        c=$(dirname -- "$c")
    done
    [ "$n" = 0 ] && return
    if [ "$n" = 1 ]; then
        echo "$first is writable by its group or other users: not installing there. Fix: chmod go-w$fix, then run the installer again"
    else
        echo "$first is writable by its group or other users (so is $others): not installing there. Fix: chmod go-w$fix, then run the installer again"
    fi
}

main() {
    local new_a new_b os arch uname_s uname_m dir real tag sumsname name want got tmp line shown resolved ghout ghline ghver ghmajor ghminor ghpatch ghpre commit
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
    [ -z "$tmp" ] || die "$tmp"
    for name in mash mashctl; do
        [ ! -d "$real/$name" ] || die "$real/$name is a folder: not installing there"
    done

    # A private place to download to, removed on exit.
    WORKDIR=$(mktemp -d "${TMPDIR:-/tmp}/mash-install.XXXXXX") || die "could not make a temporary folder"
    chmod 0700 "$WORKDIR"
    VHOME="$WORKDIR/home"
    trap cleanup EXIT

    sumsname=SHA256SUMS
    fetch "$sumsname"

    # The attestation of SHA256SUMS, when gh is there and 2.102.0 or newer: older
    # versions check --signer-workflow only as a prefix (GHSA-wjmr-j3rp-mh2g).
    if ! command -v gh >/dev/null 2>&1; then
        echo "warning: gh not found: checked checksums only, not verified" >&2
    else
        ghout=$(gh --version 2>/dev/null) || ghout=
        ghline=${ghout%%$'\n'*}
        ghver=$(printf '%s\n' "$ghline" | awk '$1 == "gh" && $2 == "version" && $3 ~ /^[0-9]+\.[0-9]+\.[0-9]+/ { split($3, v, /[^0-9]+/); print v[1], v[2], v[3], ($3 ~ /^[0-9.]+-/ ? "pre" : "") }')
        if [ -z "$ghout" ]; then
            echo "warning: gh did not run: checked checksums only, not verified" >&2
        elif [ -z "$ghver" ]; then
            echo "warning: gh version unreadable: checked checksums only, not verified" >&2
        else
            read -r ghmajor ghminor ghpatch ghpre <<<"$ghver"
            # A prerelease of 2.102.0 itself is older than 2.102.0.
            if [ "$ghmajor" -lt 2 ] || { [ "$ghmajor" -eq 2 ] && [ "$ghminor" -lt 102 ]; } ||
                { [ "$ghmajor" -eq 2 ] && [ "$ghminor" -eq 102 ] && [ "$ghpatch" -eq 0 ] && [ -n "$ghpre" ]; }; then
                echo "warning: gh is below 2.102.0: checked checksums only, not verified" >&2
            else
                fetch mash.intoto.jsonl
                # The commit the tag points to (gh resolves an annotated tag too), so
                # a release attested from another commit is refused. gh may not be
                # logged in: then ask the public API with curl.
                commit=$(gh api "repos/$REPO/commits/$tag" --jq .sha 2>/dev/null) || commit=
                if ! [[ $commit =~ ^[0-9a-f]{40}$ ]]; then
                    commit=$(curl -fsSL --proto "$PROTO" --proto-redir '=https' --tlsv1.2 \
                        -H 'Accept: application/vnd.github.sha' \
                        "${MASH_API_BASE:-https://api.github.com}/repos/$REPO/commits/$tag" 2>/dev/null) || commit=
                fi
                if ! [[ $commit =~ ^[0-9a-f]{40}$ ]]; then
                    # A gh that is not logged in falls back to the public API (60 an hour per address).
                    echo "error: could not read the commit of $REPO $tag; nothing was installed" >&2
                    if ! gh auth status >/dev/null 2>&1; then
                        echo "hint: gh auth login, then run the install again" >&2
                    fi
                    exit 1
                fi
                (cd "$WORKDIR" && gh attestation verify "$sumsname" -R "$REPO" --bundle mash.intoto.jsonl \
                    --signer-workflow "$REPO/.github/workflows/release.yml" --source-ref "refs/tags/$tag" \
                    --source-digest "$commit" >/dev/null) ||
                    die "the release failed verification; nothing was installed"
                echo "Verified the release attestation for $REPO $tag"
            fi
        fi
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

    # A split pair left by a hard kill between the two renames (one program
    # missing) is not "installed": the install below replaces both, which
    # repairs it. The old programs are never run.
    if [ -f "$real/mash" ] || [ -f "$real/mashctl" ]; then
        if [ ! -f "$real/mash" ] || [ ! -f "$real/mashctl" ]; then
            echo "only one of mash and mashctl is in $real: installing $tag of both"
        fi
    fi

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

    # The two new programs, still staged and already checked against SHA256SUMS,
    # must each report the requested release's version; if not, refuse before
    # either rename.
    check_staged "${staged_new[0]}" mash
    check_staged "${staged_new[1]}" mashctl

    local backup=""
    if [ -f "$real/mash" ]; then
        backup=$(mktemp "$real/.mash-old.XXXXXX") || die "could not write in $real"
        STAGED+=("$backup")
        cp -p -- "$real/mash" "$backup"
    fi
    # Ignore SIGINT, SIGTERM and SIGHUP across the two renames and the undo, so
    # an interruption leaves both old or both new. SIGKILL cannot be caught; that
    # leaves a tiny window.
    local old_traps
    old_traps=$(trap -p INT TERM HUP)
    trap '' INT TERM HUP
    mv -f -- "${staged_new[0]}" "$real/mash" || die "could not install mash: nothing was installed"
    if ! mv -f -- "${staged_new[1]}" "$real/mashctl"; then
        if [ -n "$backup" ]; then mv -f -- "$backup" "$real/mash"; else rm -f -- "$real/mash"; fi
        die "could not install mashctl: nothing was installed"
    fi
    trap - INT TERM HUP
    eval "$old_traps"
    STAGED=()
    [ -z "$backup" ] || rm -f -- "$backup"

    shown=$(tilde "$dir")
    for name in mash mashctl; do
        line=$(version_line "$real/$name")
        [ -n "$line" ] || die "the installed $name did not report its version"
        echo "Installed $line in $shown"
    done

    case ":$PATH:" in
    *":$dir:"*) ;;
    *) echo "$shown is not on your PATH; add it to use mash" ;;
    esac
}

main "$@"
