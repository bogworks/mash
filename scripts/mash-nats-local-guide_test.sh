#!/usr/bin/env bash
# Test for docs/skills/mash-nats-local/SKILL.md (the guide, DRAFT 8: parts 1
# to 4, frozen at part 3 plus stop/start/remove) and WHAT-MASH-NEEDS.md.  Run:
#
#     bash scripts/mash-nats-local-guide_test.sh [--update-closing]
#
# It extracts every fenced block whose info string is `sh` or `sh person`,
# in order, and runs each one in ONE fresh rootless podman container (debian
# bookworm-slim) as user alice: each block as its own `bash -c`, with
# HOME=/home/alice and PATH=/usr/local/bin:/usr/bin:/bin only; the `sh
# person` block (--remove) once with no terminal (exit 4) and once on a
# pseudo-terminal with the typed path fed in.  The one stand-in is `claude`
# (the container has no Claude Code): a script printing a version line; the
# first block is also run without it to see the guide's `command not found`
# stop case.  Nothing runs on the host except git (read-only), the raw-URL
# fetch of the pinned helper and podman.  The SUBSTITUTIONS section of the
# report says what differs from the guide.
# Containers and images are removed by exact (suffixed) name.
#
# GUIDETEST_PENDING=1 (off by default) also runs the parked parts (install
# mash and mashctl, adopt, endpoints, Alice and Bob, tamper tests) read from
# docs/plans/2026-11-19-nats-skill-parts-4-5-parked.md instead of SKILL.md.
# That code needs go and builds mash and mashctl from this tree; it is kept
# for when the parts come back and is NOT run by the default run.
#
# --update-closing writes the helper's closing output (from the create block)
# into SKILL.md between the closing-begin/closing-end markers; without it the
# fresh output is compared with what is embedded.
# GUIDETEST_REPORT=FILE sets where the report goes (default: none kept);
# GUIDETEST_OUT=DIR (must not exist) keeps every block's raw output.
# Needs: podman (rootless), python3, git, network (apt, GitHub); go if pending.
set -u

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P) || exit 2
REPO=$(dirname "$HERE")
SKILL=$REPO/docs/skills/mash-nats-local/SKILL.md
PARK=$REPO/docs/plans/2026-11-19-nats-skill-parts-4-5-parked.md
PENDING=${GUIDETEST_PENDING:-0}
[ -f "$SKILL" ] || { echo "no $SKILL" >&2; exit 2; }
for c in podman python3 awk git; do
    command -v "$c" >/dev/null || { echo "$c is required" >&2; exit 2; }
done
if [ "$PENDING" = 1 ]; then
    command -v go >/dev/null || { echo "go is required (GUIDETEST_PENDING=1)" >&2; exit 2; }
    [ -f "$PARK" ] || { echo "no $PARK" >&2; exit 2; }
fi
UPDATE=0
case "${1:-}" in
--update-closing) UPDATE=1 ;;
"") ;;
*) echo "usage: $0 [--update-closing]" >&2; exit 2 ;;
esac

T=$(mktemp -d "${TMPDIR:-/tmp}/guidetest.XXXXXX") || exit 2
RUNID=$(printf '%s' "${T##*.}" | tr 'A-Z' 'a-z')
C=mash-guidetest-$RUNID
IMG=localhost/mash-guidetest-$RUNID
BUILT_IMG=0
BUILT_IMG2=0
BOXES=()
CIP=/home/alice

cleanup() {
    if [ -n "${GUIDETEST_REPORT:-}" ] && [ -f "$T/report.md" ]; then
        cp "$T/report.md" "$GUIDETEST_REPORT"
    fi
    if [ -n "${GUIDETEST_OUT:-}" ] && [ -d "$T/out" ] &&
        [ ! -e "$GUIDETEST_OUT" ]; then
        cp -r "$T/out" "$GUIDETEST_OUT"
    fi
    podman rm -f "$C" >/dev/null 2>&1
    for b in "${BOXES[@]}"; do podman rm -f "$b" >/dev/null 2>&1; done
    [ "$BUILT_IMG2" = 1 ] && podman rmi -f "$IMG-pre" >/dev/null 2>&1
    [ "$BUILT_IMG" = 1 ] && podman rmi -f "$IMG" >/dev/null 2>&1
    case $T in
    */guidetest.*) rm -rf -- "$T" ;;
    esac
}
trap cleanup EXIT

say() { printf '== %s\n' "$*"; }
mkdir -p "$T/out" "$T/blk" "$T/pblk" "$T/bin" "$T/gocache" "$T/ctx" "$T/assets"

# ------------------------------------------------- the guide itself
say "reading the guide"
AWKPROG='
/^```/ {
    if (!infence) {
        infence = 1
        info = substr($0, 4)
        cap = (all == 1 || info == "sh" || info == "sh person")
        if (cap) {
            n++
            f = sprintf("%s/%02d.sh", d, n)
            printf "%s\n", info > sprintf("%s/%02d.info", d, n)
            close(sprintf("%s/%02d.info", d, n))
            printf "" > f
        }
    } else {
        infence = 0
        if (cap) close(f)
        cap = 0
    }
    next
}
infence { if (cap) print > f; next }
/^\|/ { next }
/^ *[^ ]+$/ { next }
length > 80 { printf "%d: %s\n", NR, $0 > (d "/wide.txt") }
'
mkdir -p "$T/sall" "$T/wblk"
awk -v d="$T/blk" -v all=0 "$AWKPROG" "$SKILL"
awk -v d="$T/sall" -v all=1 "$AWKPROG" "$SKILL"
WHAT=$REPO/docs/skills/mash-nats-local/WHAT-MASH-NEEDS.md
[ -f "$WHAT" ] && awk -v d="$T/wblk" -v all=1 "$AWKPROG" "$WHAT"
# the parked parts: blocks numbered from 07 (the old part-4 numbering)
if [ "$PENDING" = 1 ]; then
    awk -v d="$T/pblk" -v all=0 -v n=6 "$AWKPROG" "$PARK"
fi
NBLK=$(find "$T/blk" -name '*.sh' | grep -c .)
WIDE=0
[ -f "$T/blk/wide.txt" ] && WIDE=$(grep -c . "$T/blk/wide.txt")
WWIDE=0
[ -f "$T/wblk/wide.txt" ] && WWIDE=$(grep -c . "$T/wblk/wide.txt")
LONGEST=$(cat "$T"/blk/*.sh | awk '{ if (length > m) m = length } END { print m + 0 }')
LONGEST_ASCII=$(grep -c '[^ -~]' "$SKILL" || true)


if [ "$PENDING" = 1 ]; then
# ------------------------------------------------- build
say "building mash and mashctl in a scratch copy of the repo"
cp -a "$REPO" "$T/src" || exit 2
python3 -I - "$T/src/cmd/mashctl/adopt.go" <<'__PATCH__' || exit 2
import sys
p = sys.argv[1]
s = open(p).read()
old = '''		adminPath = st.adminCredsAbs
		step("%srecord the admin creds path %s (the file is not copied)", verb, adminPath)
'''
new = '''		adminPath = st.adminDst
		if o.dryRun {
			step("would copy the admin creds to %s (0600)", st.adminDst)
			break
		}
		if _, err := ctlkeys.InstallFile(st.adminDst, st.adminRaw); err != nil {
			return fail("%v", err)
		}
		step("admin creds copied to %s", st.adminDst)
'''
if s.count(old) != 1:
    sys.exit("adopt.go patch: the expected text is not there exactly once")
open(p, "w").write(s.replace(old, new))
__PATCH__
for b in mashctl mash; do
    (cd "$T/src" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
        GOCACHE="$T/gocache" go build -o "$T/assets/$b-linux-amd64" \
        "./cmd/$b") || { echo "FAIL  go build $b" >&2; exit 2; }
done
# the real mashctl binary is served as mashctl-real; the file the guide's
# part-4 block downloads as mashctl-linux-amd64 is the HARNESS-ONLY shim
mv "$T/assets/mashctl-linux-amd64" "$T/assets/mashctl-real"
printf '{}\n' >"$T/assets/mash.intoto.jsonl"
printf 'a different mash\n' >"$T/assets/mash-evil"

cat >"$T/assets/mashctl-linux-amd64" <<'__CTL__'
#!/bin/bash
# HARNESS-ONLY SHIM, served as the guide's "mashctl-linux-amd64".  The
# mashctl built from this tree does not yet have `adopt --from DIR`, the
# fingerprint prompt, adopt-then-init, or `endpoint create --creds-out`.
#  - `adopt --from DIR [--dry-run]` is rewritten into the old long-flag form:
#    DIR/mash-setup.json gives server, owner and account; the CA goes in
#    MASH_TLS_CA=DIR/ca.pem; keys and creds come from DIR/export.  After a
#    real adopt that succeeded, `mashctl init` runs (the real adopt will do
#    it itself).  Nothing is fed for the fingerprint prompt: it is not
#    exercised.
#  - `endpoint create ... --creds-out P` drops the flag (this build writes
#    <config dir>/<address>.creds itself) after checking P is that path.
# Everything else goes to the real binary untouched.
REAL=/opt/guide-assets/mashctl-real
cfg=${MASH_CONFIG_DIR:-$HOME/.config/mash}
if [ "${1:-}" = adopt ] && [ "${2:-}" = --from ]; then
    d=${3:-}
    shift 3
    [ -f "$d/mash-setup.json" ] || {
        echo "shim: no $d/mash-setup.json" >&2
        exit 1
    }
    j=$(cat "$d/mash-setup.json")
    f() { printf '%s' "$j" | sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p"; }
    dry=0
    for a in "$@"; do [ "$a" = --dry-run ] && dry=1; done
    MASH_TLS_CA=$d/ca.pem "$REAL" adopt --server "$(f server)" \
        --owner "$(f owner)" --account "$(f account)" \
        --operator-key "$d/export/operator.nk" \
        --account-key "$d/export/account.nk" \
        --sys-creds "$d/export/sys.creds" \
        --admin-creds "$d/export/mashadmin.creds" "$@"
    rc=$?
    if [ $rc -eq 0 ] && [ $dry -eq 0 ]; then
        "$REAL" init
        rc=$?
    fi
    exit $rc
fi
if [ "${1:-}" = endpoint ] && [ "${2:-}" = create ]; then
    out=()
    skip=0
    for a in "$@"; do
        if [ $skip -eq 1 ]; then
            skip=0
            case $a in
            "$cfg"/*.creds) ;;
            *) echo "shim: --creds-out $a is not in $cfg" >&2; exit 1 ;;
            esac
            continue
        fi
        if [ "$a" = --creds-out ]; then
            skip=1
            continue
        fi
        out+=("$a")
    done
    exec "$REAL" "${out[@]}"
fi
exec "$REAL" "$@"
__CTL__

cat >"$T/assets/curl" <<'__CURL__'
#!/bin/bash
# HARNESS-ONLY SHIM: the v0.5.6 release assets of bogworks/mash and the
# pinned gh tarball come from local files; every other URL goes to the real
# curl untouched.  /opt/guide-assets/mode: normal | badmash (a different
# mash-linux-amd64) | badgh (a different gh tarball) | swap3 (normal files).
mode=$(cat /opt/guide-assets/mode 2>/dev/null)
args=()
for a in "$@"; do
    case $a in
    https://github.com/bogworks/mash/releases/download/v0.5.6/*)
        f=${a##*/}
        case $f in
        mash-linux-amd64 | mashctl-linux-amd64 | mash.intoto.jsonl | SHA256SUMS)
            # the guide's curl -O names the file after the URL
            src=/opt/guide-assets/$f
            [ "$mode" = badmash ] && [ "$f" = mash-linux-amd64 ] &&
                src=/opt/guide-assets/mash-evil
            exec cp "$src" "./$f" ;;
        esac ;;
    https://github.com/cli/cli/releases/download/v2.105.0/gh_2.105.0_linux_amd64.tar.gz)
        # the guide's curl -O names the file after the URL
        src=/opt/guide-assets/gh-fake.tar.gz
        [ "$mode" = badgh ] && src=/opt/guide-assets/gh-evil.tar.gz
        exec cp "$src" "./${a##*/}" ;;
    esac
    args+=("$a")
done
exec /usr/bin/curl "${args[@]}"
__CURL__

cat >"$T/assets/gh-wrapper" <<'__GH__'
#!/bin/bash
# HARNESS-ONLY STAND-IN for `gh attestation verify` (the real v0.5.6 assets
# do not exist, so the real gh cannot verify them).  It is put at bin/gh of
# a repacked copy of the pinned gh tarball.  It records argv, checks the
# flags the guide promises, and "verifies" by comparing the sha256 of the
# file with the sha256 of the genuine asset (expected.sha256); a mismatch
# is "Verification failed".  In mode swap3 the 3rd call first appends a byte
# to $HOME/.local/bin/mash (a swap between the two verifications).
printf '%s\n' "$*" >>"$HOME/gh-wrapper.log"
n=$(( $(cat "$HOME/gh-count" 2>/dev/null || echo 0) + 1 ))
echo "$n" >"$HOME/gh-count"
mode=$(cat /opt/guide-assets/mode 2>/dev/null)
if [ "$mode" = swap3 ] && [ "$n" -eq 3 ]; then
    printf x >>"$HOME/.local/bin/mash"
fi
all=" $* "
for w in "attestation verify" " -R bogworks/mash " \
    " --bundle mash.intoto.jsonl " \
    " --signer-workflow bogworks/mash/.github/workflows/release.yml " \
    " --source-ref refs/tags/v0.5.6 "; do
    case $all in
    *"$w"*) ;;
    *) echo "gh stand-in: missing '$w'" >&2; exit 1 ;;
    esac
done
[ -s mash.intoto.jsonl ] || { echo "gh stand-in: no bundle here" >&2; exit 1; }
file=$3
b=${file##*/}
name=${b%-linux-amd64}
want=$(awk -v n="$name" '$2 == n { print $1 }' /opt/guide-assets/expected.sha256)
got=$(sha256sum "$file" | cut -d' ' -f1)
if [ -z "$want" ] || [ "$got" != "$want" ]; then
    echo "Error: verifying $file: Verification failed (digest mismatch)" >&2
    exit 1
fi
exit 0
__GH__
chmod 755 "$T/assets/gh-wrapper" "$T/assets/curl" "$T/assets/mashctl-linux-amd64"
{
    printf '%s  mash\n' "$(sha256sum "$T/assets/mash-linux-amd64" | cut -d' ' -f1)"
    printf '%s  mashctl\n' "$(sha256sum "$T/assets/mashctl-linux-amd64" | cut -d' ' -f1)"
} >"$T/assets/expected.sha256"
# the release's SHA256SUMS (the real one lists every asset; two extra lines
# here so the guide's grep has something to skip)
{
    printf '%s  mash-linux-amd64\n' "$(sha256sum "$T/assets/mash-linux-amd64" | cut -d' ' -f1)"
    printf '%s  mash-linux-arm64\n' "$(printf 'arm64' | sha256sum | cut -d' ' -f1)"
    printf '%s  mashctl-linux-amd64\n' "$(sha256sum "$T/assets/mashctl-linux-amd64" | cut -d' ' -f1)"
    printf '%s  mashctl-linux-arm64\n' "$(printf 'ctlarm64' | sha256sum | cut -d' ' -f1)"
} >"$T/assets/SHA256SUMS"
fi

cat >"$T/ctx/Containerfile" <<'__CF__'
FROM docker.io/library/debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends sudo
RUN useradd -m -u 1000 -s /bin/bash alice && echo 'alice ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/alice && chmod 440 /etc/sudoers.d/alice
__CF__
say "building the image $IMG"
podman build -q -t "$IMG" "$T/ctx" >/dev/null || exit 2
BUILT_IMG=1
# a second image with the prerequisites already in, for the tamper boxes
mkdir -p "$T/ctx2"
cat >"$T/ctx2/Containerfile" <<__CF2__
FROM $IMG
RUN apt-get update && apt-get install -y curl tar unzip openssl iproute2 ca-certificates coreutils
__CF2__
podman build -q -t "$IMG-pre" "$T/ctx2" >/dev/null || exit 2
BUILT_IMG2=1
podman run -d --name "$C" --user alice "$IMG" sleep 7200 >/dev/null || exit 2

putassets() { # CONTAINER MODE: the shared asset directory (pending only)
    local c=$1 f
    podman exec --user root "$c" mkdir -p /opt/guide-assets
    for f in mash-linux-amd64 mashctl-linux-amd64 mashctl-real \
        mash.intoto.jsonl mash-evil expected.sha256 gh-wrapper SHA256SUMS; do
        podman cp "$T/assets/$f" "$c:/opt/guide-assets/$f"
    done
    printf '%s\n' "$2" >"$T/mode"
    podman cp "$T/mode" "$c:/opt/guide-assets/mode"
    podman exec --user root "$c" chmod -R a+rX /opt/guide-assets
}
[ "$PENDING" = 1 ] && putassets "$C" normal

# ------------------------------------------------- runners
PASSN=0
FAILN=0
BPASS=0
BFAIL=0
BWHY=""
OUT=""
RC=0
BK=blk
PF=
cexec() {
    podman exec --user alice -w "$CIP" "$C" env -i HOME="$CIP" \
        PATH=/usr/local/bin:/usr/bin:/bin "$@"
}
cin() { OUT=$(cexec bash -c "$1" 2>&1 </dev/null); RC=$?; }
cinroot() { OUT=$(podman exec --user root "$C" bash -c "$1" 2>&1 </dev/null); RC=$?; }
has() { printf '%s\n' "$OUT" | grep -qF -- "$1"; }
ck() { # DESCRIPTION CMD...: one assertion
    local d=$1
    shift
    if "$@"; then PASSN=$((PASSN + 1)); else
        FAILN=$((FAILN + 1))
        BWHY="$BWHY; $d"
    fi
}
nlines() { [ "$(printf '%s\n' "$OUT" | grep -cF -- "$1")" -eq "$2" ]; }
firstline() { [ "$(printf '%s\n' "$OUT" | head -n1 | cut -c1-3)" = "$1" ]; }
sig() { grep -qF -- "$2" "$T/$BK/$(printf %02d "$1").sh"; }
rc0() { [ "$RC" -eq 0 ]; }
rcn0() { [ "$RC" -ne 0 ]; }
nothas() { ! has "$1"; }
row() { # N METHOD RC
    local st=PASS
    [ -z "$BWHY" ] || st="FAIL$BWHY"
    case $st in PASS) BPASS=$((BPASS + 1)) ;; *) BFAIL=$((BFAIL + 1)) ;; esac
    local first
    first=$(head -c 60 "$T/$BK/$(printf %02d "$1").sh" | tr '\n' ' ')
    printf '| %s | `%s` | %s | %s | %s |\n' "$PF$1" \
        "$(printf '%s' "$first" | sed 's/|/\\|/g')" "$2" "$3" "$st" \
        >>"$T/rows.md"
    printf '%s  block %s (%s rc=%s)%s\n' "${st%%;*}" "$PF$1" "$2" "$3" \
        "${BWHY:+  $BWHY}"
    BWHY=""
}
save() { # NAME: keep the current output, secrets scrubbed
    printf '%s\n' "$OUT" | tr -d '\r' |
        sed -E 's/S[OANUCX][A-Z2-7]{40,}/<SEED>/g;s/eyJ[A-Za-z0-9_.-]{20,}/<JWT>/g' \
            >"$T/out/$1.txt"
}
runblk() { # N: as written, its own bash -c
    local f
    # the agent path never runs a person block (runpty is the person's side)
    if [ "$(cat "$T/$BK/$(printf %02d "$1").info")" = "sh person" ]; then
        echo "BUG: runblk $PF$1 is a person block" >&2
        exit 2
    fi
    f=$T/$BK/$(printf %02d "$1").sh
    cin "$(cat "$f")"
    save "$BK$(printf %02d "$1")"
}
runpty() { # N TYPED: on a pty (script -qec), the confirmation fed in
    local nn f
    nn=$(printf %02d "$1")
    f=$T/$BK/$nn.sh
    [ "$(cat "$T/$BK/$nn.info")" = "sh person" ] || {
        echo "BUG: runpty $PF$1 is not a person block" >&2
        exit 2
    }
    podman cp "$f" "$C:$CIP/$BK$nn.sh"
    podman exec --user root "$C" chown alice:alice "$CIP/$BK$nn.sh"
    OUT=$({ sleep 5; printf '%s\n' "$2"; } |
        podman exec -i --user alice -w "$CIP" "$C" env -i HOME="$CIP" \
            PATH=/usr/local/bin:/usr/bin:/bin \
            script -qec "bash -c \"\$(cat $CIP/$BK$nn.sh)\"" /dev/null 2>&1)
    RC=$?
    OUT=$(printf '%s\n' "$OUT" | tr -d '\r')
    save "$BK$nn"
}
extra() { # NAME: an extra row (not a guide block)
    local st=PASS
    [ -z "$BWHY" ] || st="FAIL$BWHY"
    case $st in PASS) BPASS=$((BPASS + 1)) ;; *) BFAIL=$((BFAIL + 1)) ;; esac
    printf '| %s | (test step) | bash -c | %s | %s |\n' "$1" "$RC" "$st" \
        >>"$T/rows.md"
    printf '%s  step: %s%s\n' "${st%%;*}" "$1" "${BWHY:+  $BWHY}"
    BWHY=""
}

# ------------------------------------------------- extra containers
C_MAIN=$C
tb_new() { # SUFFIX MODE: a fresh container from the image with prerequisites
    TB=mash-guidetest-$RUNID-$1
    BOXES+=("$TB")
    podman run -d --name "$TB" --user alice "$IMG-pre" sleep 3600 >/dev/null || return 1
    C=$TB
    if [ "$PENDING" = 1 ]; then
        putassets "$TB" "$2"
        podman cp "$T/assets/curl" "$TB:/usr/local/bin/curl"
        podman cp "$T/assets/gh-fake.tar.gz" "$TB:/opt/guide-assets/gh-fake.tar.gz"
        podman cp "$T/assets/gh-evil.tar.gz" "$TB:/opt/guide-assets/gh-evil.tar.gz"
        podman exec --user root "$TB" chmod 755 /usr/local/bin/curl
        podman exec --user root "$TB" chmod a+r /opt/guide-assets/gh-fake.tar.gz \
            /opt/guide-assets/gh-evil.tar.gz
    fi
}
tb_end() {
    podman rm -f "$TB" >/dev/null 2>&1
    C=$C_MAIN
}
tblk() { # FILE NAME: run a block file in the current box, keep the output
    cin "$(cat "$1")"
    save "$2"
}
BN() { printf '%s/pblk/%02d.sh' "$T" "$1"; }

# ------------------------------------------------- the guide's own checks
say "checking the guide itself"
BWHY=""
RC=0
ck "SKILL.md sh block count is 11 (got $NBLK)" test "$NBLK" -eq 11
NWB=$(find "$T/wblk" -name '*.sh' | grep -c .)
ck "WHAT-MASH-NEEDS.md fenced block count is 6 (got $NWB)" test "$NWB" -eq 6
ck "$WIDE SKILL.md prose lines over 80 columns (fences, tables, URL-only lines exempt)" \
    test "$WIDE" -eq 0
ck "$WWIDE WHAT-MASH-NEEDS.md prose lines over 80 columns" test "$WWIDE" -eq 0
ck "non-ASCII characters in SKILL.md" test "$LONGEST_ASCII" -eq 0
ck "non-ASCII characters in WHAT-MASH-NEEDS.md" \
    test "$(grep -c '[^ -~]' "$WHAT" || true)" -eq 0
extra "guide: 11 sh blocks in SKILL.md, 6 fences in WHAT-MASH-NEEDS.md, prose <= 80 columns in both documents, ASCII"

# expected signature per block (so a reordered guide fails loudly)
declare -A SIG=(
    [1]='command -v "$c"' [2]='sudo apt-get install'
    [3]='exists: $p' [4]='sha256sum -c -' [5]='--owner acme --start'
    [6]='--check' [7]='--stop' [8]='--dir "$HOME/mash-nats" --start'
    [9]='--remove' [10]='--remove' [11]='rm -r "$HOME/mash-setup"'
)
declare -A INFO=([9]="sh person" [10]="sh person" [11]="sh person")
for n in $(seq 1 11); do
    want=sh
    [ "${INFO[$n]+set}" = set ] && want=${INFO[$n]}
    got=$(cat "$T/blk/$(printf %02d "$n").info" 2>/dev/null)
    BWHY=""
    ck "info string is '$got', expected '$want'" test "$got" = "$want"
    ck "block $n does not contain '${SIG[$n]}'" sig "$n" "${SIG[$n]}"
    if [ -n "$BWHY" ]; then
        extra "block $n identity"
    fi
done
# WHAT-MASH-NEEDS.md: info string and a pattern for every fence
declare -A WINFO=([1]="")
declare -A WSIG=(
    [1]='listen: "127.0.0.1:4222"' [2]='--port 4333 --start'
    [3]='--dir "$HOME/srv/mash-nats"' [4]='--mash-config "$HOME/mash-conf"'
    [5]='MASH_CONFIG_DIR="$HOME/mash-conf"' [6]='adopt --dry-run --server'
)
for n in $(seq 1 6); do
    want=sh
    [ "${WINFO[$n]+set}" = set ] && want=${WINFO[$n]}
    got=$(cat "$T/wblk/$(printf %02d "$n").info" 2>/dev/null)
    BWHY=""
    ck "WHAT block $n info string is '$got', expected '$want'" test "$got" = "$want"
    ck "WHAT block $n does not contain '${WSIG[$n]}'" grep -qF -- "${WSIG[$n]}" "$T/wblk/$(printf %02d "$n").sh"
    if [ -n "$BWHY" ]; then
        extra "WHAT block $n identity"
    fi
done
BWHY=""

# static checks over the block text
ck "no fenced block in SKILL.md or WHAT-MASH-NEEDS.md contains the word exit" \
    bash -c "! grep -qw exit \"$T\"/sall/*.sh \"$T\"/wblk/*.sh"
ck "the guide says the person runs the sh person blocks (and the agent never)" \
    grep -q 'never run' "$SKILL"
ck "every sh block (both documents) that uses --remove is marked sh person" bash -c \
    "for f in \"$T\"/blk/[0-9][0-9].sh \"$T\"/wblk/[0-9][0-9].sh; do b=\${f%.sh}; case \$(cat \$b.info) in sh*) ;; *) continue ;; esac; if grep -qE -- '--remove|rm -r.*\.config/mash|^rm .*\.local/bin' \$f && [ \"\$(cat \$b.info)\" != 'sh person' ]; then echo \"unmarked: \$f\"; exit 1; fi; done"
ck "the create block passes --owner acme explicitly" sig 5 "--owner acme"
extra "guide text: no 'exit', person block marked, create passes --owner"

# the script URL commit and sha in the guide equal git's
GCOMMIT=$(grep -o 'raw.githubusercontent.com/bogworks/mash/[0-9a-f]\{40\}/scripts/nats-local-setup.sh' \
    "$T/blk/04.sh" | cut -d/ -f4)
GSHA=$(grep -o '[0-9a-f]\{64\}  \$HOME/mash-setup/nats-local-setup.sh' \
    "$T/blk/04.sh" | cut -c1-64)
LASTC=$(git -C "$REPO" log -1 --format=%H -- scripts/nats-local-setup.sh)
FULLC=$(git -C "$REPO" rev-parse "${GCOMMIT:-none}^{commit}" 2>/dev/null)
HELPERSHA=$(sha256sum "$REPO/scripts/nats-local-setup.sh" | cut -d' ' -f1)
SKILLSHA=$(sha256sum "$SKILL" | cut -d' ' -f1)
WHATSHA=$(sha256sum "$WHAT" | cut -d' ' -f1)
COMMITSHA=$(git -C "$REPO" show "${GCOMMIT:-none}:scripts/nats-local-setup.sh" 2>/dev/null | sha256sum | cut -d' ' -f1)
ck "guide URL commit is 40 hex" test "${#GCOMMIT}" -eq 40
ck "git rev-parse of the guide's commit equals it" test "$FULLC" = "$GCOMMIT"
ck "it is the last commit that changed the script ($LASTC)" test "$LASTC" = "$GCOMMIT"
ck "the script at that commit has the guide's sha256" test "$COMMITSHA" = "$GSHA"
ck "the script in this tree has the guide's sha256" test "$HELPERSHA" = "$GSHA"
ck "that commit is on a pushed (remote) branch" bash -c \
    "[ -n \"\$(git -C '$REPO' branch -r --contains '$GCOMMIT' 2>/dev/null)\" ]"
PIN_COMMIT=f713821435cbb23dd8c0a94c3760cd54f3541aab
PIN_SHA=be7a07c94474d86ccffabe0ed30892ef5fcc25350cb2b4f8edc22e9772b364f5
ck "the guide's script commit equals the pinned $PIN_COMMIT" \
    test "$GCOMMIT" = "$PIN_COMMIT"
ck "the guide's script sha256 equals the pinned $PIN_SHA" \
    test "$GSHA" = "$PIN_SHA"
ck "the helper in this tree has the pinned sha256" \
    test "$HELPERSHA" = "$PIN_SHA"
RAWURL=https://raw.githubusercontent.com/bogworks/mash/$GCOMMIT/scripts/nats-local-setup.sh
if /usr/bin/curl -fsSL --max-time 60 -o "$T/raw.sh" "$RAWURL" 2>/dev/null; then
    ck "the raw URL content (curl, outside the container) hashes to the guide's sha256" \
        test "$(sha256sum "$T/raw.sh" | cut -d' ' -f1)" = "$GSHA"
    RAWMSG="fetched and hashed"
else
    RAWMSG="SKIPPED: could not fetch $RAWURL (offline?)"
    echo "SKIP  raw URL hash: $RAWMSG"
fi
RC=0
extra "guide script URL: commit and sha256 equal git's, the pin and the file's; raw URL $RAWMSG"
BWHY=""

if [ "$PENDING" = 1 ]; then
say "PENDING: static checks of the parked blocks"
NPB=$(find "$T/pblk" -name '*.sh' | grep -c .)
ck "parked sh block count is 16 (got $NPB)" test "$NPB" -eq 16
declare -A PSIG=(
    [7]='" version;' [8]='gh_2.105.0_linux_amd64.tar.gz'
    [9]='mash.intoto.jsonl SHA256SUMS'
    [10]='attestation verify mash-linux-amd64'
    [11]='attestation verify mashctl-linux-amd64'
    [12]='SHA256SUMS | sha256sum -c -' [13]='chmod +x'
    [14]='mashctl" version' [15]='adopt --from "$HOME/mash-nats" --dry-run'
    [16]='adopt --from "$HOME/mash-nats"' [17]='stat -c'
    [18]='create acme.demo.alice' [19]='create acme.demo.bob'
    [20]='endpoint list' [21]='MASH_ENDPOINT=acme.demo.alice'
    [22]='MASH_ENDPOINT=acme.demo.bob'
)
for n in $(seq 7 22); do
    BWHY=""
    ck "parked block $n does not contain '${PSIG[$n]}'" \
        grep -qF -- "${PSIG[$n]}" "$T/pblk/$(printf %02d "$n").sh"
    [ -z "$BWHY" ] || extra "parked block $n identity"
done
BWHY=""
ck "blocks 10 and 11 each have one attestation verify with -R, --bundle, --signer-workflow, --source-ref refs/tags/v0.5.6" bash -c \    "for n in 10 11; do f=\"$T/pblk/\$n.sh\"; [ \"\$(grep -o 'attestation verify' \$f | grep -c .)\" -eq 1 ] && grep -qF -- '-R bogworks/mash --bundle mash.intoto.jsonl --signer-workflow bogworks/mash/.github/workflows/release.yml --source-ref refs/tags/v0.5.6 &&' \$f || exit 1; done"ck "no part-4 block is a subshell (no set -e, no leading paren)" bash -c \    "for n in 07 08 09 10 11 12 13 14; do ! head -n1 \"$T/pblk/\$n.sh\" | grep -q '^(' && ! grep -qE '^set -|\\bset -e' \"$T/pblk/\$n.sh\" || exit 1; done"ck "block 21 and 22 use MASH_ENDPOINT" bash -c \    "grep -qF MASH_ENDPOINT \"$T/pblk/21.sh\" && grep -qF MASH_ENDPOINT \"$T/pblk/22.sh\""ck "no block has MASH_TLS_CA, MASH_OWNER, MASH_GROUP or MASH_NAME" \    bash -c "! grep -qE 'MASH_TLS_CA|MASH_OWNER|MASH_GROUP|MASH_NAME' \"$T\"/blk/[0-9][0-9].sh"ck "adopt blocks use --from and no long flags" bash -c \    "grep -qF 'adopt --from' \"$T/pblk/15.sh\" && grep -qF 'adopt --from' \"$T/pblk/16.sh\" && ! grep -qE -- '--server|--operator-key|--ca ' \"$T\"/blk/1[56].sh"ck "endpoint create blocks have --creds-out" bash -c \    "grep -qF -- '--creds-out' \"$T/pblk/18.sh\" && grep -qF -- '--creds-out' \"$T/pblk/19.sh\""extra "parked guide text checks"
fi

# ------------------------------------------------- run the blocks
D=$CIP/mash-nats
say "running the blocks"

# 2: missing check, before apt
runblk 1
ck "rc 0" rc0
for m in curl unzip openssl ss; do ck "missing: $m is reported" has "missing: $m"; done
ck "tar is not reported missing" nothas "missing: tar"
ck "sha256sum is not reported missing" nothas "missing: sha256sum"
ck "prints checked" has checked
row 1 "bash -c" "$RC"

# 3: apt (the block runs apt-get update itself)
runblk 2
ck "rc 0" rc0
row 2 "bash -c" "$RC"
runblk 1
save blk02-after-apt
ck "rc 0" rc0
ck "nothing missing after apt" nothas "missing:"
ck "prints checked" has checked
extra "block 2 again, after apt: nothing missing"

# 4: nothing exists
runblk 3
ck "rc 0" rc0
ck "no 'exists:' line" nothas "exists:"
ck "no port line" nothas "LISTEN"
ck "prints only checked" test "$OUT" = checked
row 3 "bash -c" "$RC"

# 5: fetch the script (raw URL at the pinned commit, real network)
runblk 4
ck "rc 0" rc0
ck "sha256sum says OK" has "nats-local-setup.sh: OK"
row 4 "bash -c" "$RC"

# 6: create --owner acme --start
runblk 5
ck "rc 0" rc0
ck "prints Done." has "Done."
ck "owner acme printed" has "Owner              acme"
for i in 1 2 3 4 5 6 7; do ck "step [$i] about to:" has "[$i] about to:"; done
row 5 "bash -c" "$RC"
awk '/^Done\./ { p = 1 } p' "$T/out/blk05.txt" >"$T/out/closing.txt"

# 7: check
runblk 6
ck "rc 0" rc0
ck "no FAIL line" nothas "FAIL"
ck "check passed" has "check passed"
row 6 "bash -c" "$RC"

if [ "$PENDING" = 1 ]; then
say "PENDING: running the parked parts"
BK=pblk
podman cp "$T/assets/curl" "$C:/usr/local/bin/curl"
podman exec --user root "$C" chmod 755 /usr/local/bin/curl
# 7: mash/mashctl are absent
runblk 7
ck "rc 0" rc0
ck "two No such file" nlines "No such file" 2
ck "prints checked" has checked
row 7 "bash -c" "$RC"

# the pinned gh: the real tarball must have the sha256 the guide pins; then a
# repacked copy (bin/gh = the verify stand-in) is made for the served download
GHSHA=$(grep -o '[0-9a-f]\{64\}  gh_2.105.0_linux_amd64.tar.gz' "$T/pblk/08.sh" | cut -c1-64)
cinroot "set -e
d=\$(mktemp -d); cd \$d
/usr/bin/curl -fsSLO https://github.com/cli/cli/releases/download/v2.105.0/gh_2.105.0_linux_amd64.tar.gz
echo '$GHSHA  gh_2.105.0_linux_amd64.tar.gz' | sha256sum -c -
tar -xzf gh_2.105.0_linux_amd64.tar.gz
mkdir -p /opt/real-gh; cp -a gh_2.105.0_linux_amd64 /opt/real-gh/; chmod -R a+rX /opt/real-gh
cp /opt/guide-assets/gh-wrapper gh_2.105.0_linux_amd64/bin/gh
tar -czf /opt/guide-assets/gh-fake.tar.gz gh_2.105.0_linux_amd64
echo evil > gh_2.105.0_linux_amd64/bin/extra
tar -czf /opt/guide-assets/gh-evil.tar.gz gh_2.105.0_linux_amd64
chmod a+r /opt/guide-assets/*.tar.gz
sha256sum /opt/guide-assets/gh-fake.tar.gz | cut -d' ' -f1 >/opt/guide-assets/gh-fake.sha
chmod a+r /opt/guide-assets/gh-fake.sha"
save real-gh-tarball
ck "the REAL gh tarball has the sha256 pinned in the guide" rc0
ck "sha256sum says OK" has ": OK"
extra "REAL gh v2.105.0 tarball sha256 equals the guide's pinned hash"
cinroot 'cat /opt/guide-assets/gh-fake.sha'
FAKESHA=$OUT
for f in gh-fake.tar.gz gh-evil.tar.gz; do
    podman cp "$C:/opt/guide-assets/$f" "$T/assets/$f"
done
# the REAL gh on the real v0.5.5 assets (these exist)
cin 'mkdir -p "$HOME/v055" && cd "$HOME/v055" && for f in mash-linux-amd64 mash.intoto.jsonl; do curl -fsSLO "https://github.com/bogworks/mash/releases/download/v0.5.5/$f" || exit 90; done && /opt/real-gh/gh_2.105.0_linux_amd64/bin/gh attestation verify mash-linux-amd64 -R bogworks/mash --bundle mash.intoto.jsonl --signer-workflow bogworks/mash/.github/workflows/release.yml --source-ref refs/tags/v0.5.5; echo "gh exit=$?"'
save real-gh-v055
ck "real gh verify of v0.5.5 exits 0" has "gh exit=0"
extra "REAL gh attestation verify on real v0.5.5 mash-linux-amd64"

# the gh block (8) with the pinned gh hash replaced by the repacked tarball's
sed "s/$GHSHA/$FAKESHA/" "$T/pblk/08.sh" >"$T/pblk/08.sub.sh"
ck "the substitution changed exactly the gh hash" bash -c \
    "[ \"\$(diff '$T/pblk/08.sh' '$T/pblk/08.sub.sh' | grep -c '^>')\" -eq 1 ]"
ck "the original block still has the real pinned hash" sig 8 "$GHSHA"
RC=0
extra "substituted copy of block 8 (gh hash -> repacked tarball's), made"

# 8: fetch the pinned gh and install it (the substituted copy)
cin "$(cat "$T/pblk/08.sub.sh")"
save blk08
ck "rc 0" rc0
ck "gh tarball sha256 OK" has "gh_2.105.0_linux_amd64.tar.gz: OK"
row 8 "bash -c (hash-substituted copy)" "$RC"
cin 'ls -A "$HOME/.local/bin"; stat -c "%a %n" "$HOME/.local/bin/gh"'
save blk08-probe
ck "only gh in .local/bin" bash -c "[ \"\$(printf '%s\n' \"$OUT\" | head -n1)\" = gh ]"
ck "gh mode 755" has "755 /home/alice/.local/bin/gh"
extra "after block 8: gh (the stand-in) is the only file in .local/bin, mode 755"

# 9: download the four release files (shim)
runblk 9
ck "rc 0" rc0
cin 'ls "$HOME/mash-setup/dl" | tr "\n" " "'
save blk09-probe
for f in mash-linux-amd64 mashctl-linux-amd64 mash.intoto.jsonl SHA256SUMS; do
    ck "dl has $f" has "$f"
done
extra "after block 9: the four release files are in mash-setup/dl"

# 10, 11: one verify per file (the stand-in; success prints only our echo)
runblk 10
ck "rc 0" rc0
ck "prints exactly: verified mash" test "$OUT" = "verified mash"
row 10 "bash -c (gh stand-in)" "$RC"
runblk 11
ck "rc 0" rc0
ck "prints exactly: verified mashctl" test "$OUT" = "verified mashctl"
row 11 "bash -c (gh stand-in)" "$RC"
cin 'cat "$HOME/gh-wrapper.log"'
save blk11-ghlog
ck "stand-in saw 2 verify calls" test "$(printf '%s\n' "$OUT" | grep -c 'attestation verify')" -eq 2
ck "calls name each file once" bash -c \
    "printf '%s\n' \"$OUT\" | grep -q 'verify mash-linux-amd64 -R bogworks/mash --bundle mash.intoto.jsonl' && printf '%s\n' \"$OUT\" | grep -q 'verify mashctl-linux-amd64 -R bogworks/mash --bundle mash.intoto.jsonl'"
extra "blocks 10 and 11: two verify calls, one per file, with -R, --bundle, --signer-workflow, --source-ref"

# 12: SHA256SUMS
runblk 12
ck "rc 0" rc0
ck "mash-linux-amd64: OK" has "mash-linux-amd64: OK"
ck "mashctl-linux-amd64: OK" has "mashctl-linux-amd64: OK"
ck "two OK lines" nlines ": OK" 2
row 12 "bash -c" "$RC"

# 13: install (after all four checks passed)
runblk 13
ck "rc 0" rc0
row 13 "bash -c" "$RC"
cin 'ls -A "$HOME/.local/bin" | tr "\n" " "; stat -c "%a %n" "$HOME/.local/bin/mash" "$HOME/.local/bin/mashctl"; ls "$HOME/mash-setup/dl" | tr "\n" " "'
save blk13-probe
ck "gh mash mashctl in .local/bin" has "gh mash mashctl"
ck "mash is 755" has "755 /home/alice/.local/bin/mash"
ck "mashctl is 755" has "755 /home/alice/.local/bin/mashctl"
ck "the two binaries left dl" nothas "mash-linux-amd64 mash.intoto"
extra "after block 13: three programs in .local/bin, binaries moved out of dl"

# 14: mashctl version
runblk 14
ck "rc 0" rc0
ck "mashctl version printed" has mashctl
row 14 "bash -c" "$RC"

# 15: dry run (through the harness shim)
runblk 15
ck "rc 0" rc0
ck "All checks passed" has "All checks passed"
ck "dry-run says nothing is written" has "nothing is written"
ck "no FAIL or error line" nothas "FAIL"
row 15 "bash -c (adopt --from via shim)" "$RC"

# 16: adopt (an ordinary block: this machine types no fingerprint)
runblk 16
ck "rc 0" rc0
ck "All checks passed" has "All checks passed"
ck "mashadmin.creds copied, not recorded" nothas "not copied"
ck "no fingerprint prompt" nothas "ingerprint"
row 16 "bash -c, no pty (adopt --from via shim)" "$RC"
cin 'ls "$HOME/.config/mash" "$HOME/.config/mash/server" "$HOME/.config/mash/keys"'
save blk16-ls
ck "config.json written (init ran)" has config.json
ck "mashadmin.creds in server/" has mashadmin.creds
extra "after adopt+init: config.json, keys and creds are in mashctl's folder"

# 17: stat
runblk 17
ck "rc 0" rc0
ck "folder 700" has "700 /home/alice/.config/mash"
ck "keys 700" has "700 /home/alice/.config/mash/keys"
ck "server 700" has "700 /home/alice/.config/mash/server"
ck "config.json 600" has "600 /home/alice/.config/mash/config.json"
# FINDING: the in-tree mashctl does not copy ca.pem into its folder (it keeps
# "tls_ca" pointing at the server directory), so the real output has four
# entries, not five.  The guide (0.5.6) says five, ca.pem 0644 second.  The
# observed four must be in the guide's order; the five-entry order is checked
# on a model folder with the same names and modes (below).
ORDER=$(printf '%s\n' "$OUT" | awk '{ printf "%s ", $1 }')
ck "OBSERVED stat order is 700 600 700 700 (got: $ORDER)" test "$ORDER" = "700 600 700 700 "
ORDERN=$(printf '%s\n' "$OUT" | sed 's|.*/||' | tr '\n' ' ')
ck "OBSERVED names in order: mash config.json keys server (got: $ORDERN)" \
    test "$ORDERN" = "mash config.json keys server "
row 17 "bash -c" "$RC"
cin 'M=$HOME/stat-model; mkdir -p -m 700 "$M/.config/mash/keys" "$M/.config/mash/server" && chmod 700 "$M/.config/mash" && : >"$M/.config/mash/ca.pem" && chmod 644 "$M/.config/mash/ca.pem" && : >"$M/.config/mash/config.json" && chmod 600 "$M/.config/mash/config.json"'
ck "model folder made" rc0
OUTM=$(cexec env HOME=$CIP/stat-model bash -c "$(cat "$T/pblk/17.sh")" 2>&1 </dev/null)
ORDERM=$(printf '%s\n' "$OUTM" | awk '{ printf "%s ", $1 }')
ORDERMN=$(printf '%s\n' "$OUTM" | sed 's|.*/||' | tr '\n' ' ')
ck "MODEL folder order is 700 644 600 700 700 (got: $ORDERM)" test "$ORDERM" = "700 644 600 700 700 "
ck "MODEL names: mash ca.pem config.json keys server (got: $ORDERMN)" \
    test "$ORDERMN" = "mash ca.pem config.json keys server "
extra "block 17 on a model folder: the guide's five-entry order holds"

# 18-20: endpoints (--creds-out through the shim)
runblk 18
ck "rc 0" rc0
ck "address shown with dots" has acme.demo.alice
row 18 "bash -c (--creds-out dropped by shim)" "$RC"
runblk 19
ck "rc 0" rc0
row 19 "bash -c (--creds-out dropped by shim)" "$RC"
runblk 20
ck "rc 0" rc0
ck "alice listed" has acme.demo.alice
ck "bob listed" has acme.demo.bob
row 20 "bash -c" "$RC"
cin 'stat -c "%a %n" "$HOME/.config/mash/acme.demo.alice.creds" "$HOME/.config/mash/acme.demo.bob.creds"'
save creds-modes
ck "alice creds 600" has "600 /home/alice/.config/mash/acme.demo.alice.creds"
ck "bob creds 600" has "600 /home/alice/.config/mash/acme.demo.bob.creds"
extra "the agents' creds files are where the guide says, mode 600"

# 21-22: alice -> bob.  A COPY of each block maps MASH_ENDPOINT=o.g.n to
# MASH_OWNER=o MASH_GROUP=g MASH_NAME=n (the in-tree mash does not read
# MASH_ENDPOINT yet).
mapep() { sed -E 's/MASH_ENDPOINT=([a-z0-9-]+)\.([a-z0-9-]+)\.([a-z0-9-]+)/MASH_OWNER=\1 MASH_GROUP=\2 MASH_NAME=\3/' "$1"; }
mapep "$T/pblk/21.sh" >"$T/pblk/21.sub.sh"
mapep "$T/pblk/22.sh" >"$T/pblk/22.sub.sh"
cin "$(cat "$T/pblk/21.sub.sh")"
save blk21
ck "rc 0" rc0
ck "sent" has "sent"
row 21 "bash -c (MASH_ENDPOINT mapped in a copy)" "$RC"
cin "$(cat "$T/pblk/22.sub.sh")"
save blk22
ck "rc 0" rc0
ck "[verified]" has "[verified]"
ck "Hello Bob" has "Hello Bob"
ck "from alice" has acme.demo.alice
row 22 "bash -c (MASH_ENDPOINT mapped in a copy)" "$RC"

# ---- FACT 1: owner mismatch (the server's owner is acme)
cin '"$HOME/.local/bin/mashctl" endpoint create other.demo.carol --make-key --creds-out "$HOME/.config/mash/other.demo.carol.creds"; echo "exit=$?"; "$HOME/.local/bin/mashctl" endpoint list; ls "$HOME/.config/mash"'
save owner-mismatch
RC=0
extra "FACT 1 (observed, not asserted): endpoint create other.demo.carol after adopt with owner acme"
BK=blk
fi

# ---- stop, stop again, start, check
runblk 7
save blk08-first
ck "rc 0" rc0
ck "stop: about to: stop nats-server pid N" has "about to: stop nats-server pid"
ck "stop: did: stopped pid N" has "did: stopped pid"
row 7 "bash -c" "$RC"
cin 'ss -ltn | grep ":4222 "; true'
ck "port 4222 no longer listening" test -z "$OUT"
extra "after stop: nothing listens on 4222"
runblk 7
save blk08-again
ck "stop again: rc 0" rc0
ck "stop again: says the pid file is stale" has "stale"
extra "--stop again: stale pid file, exit 0"
runblk 8
ck "rc 0" rc0
ck "start: exactly one did: line" nlines "did:" 1
ck "start: listens on 127.0.0.1:4222 and requires TLS" has "listens on 127.0.0.1:4222 and requires TLS"
row 8 "bash -c" "$RC"
runblk 6
save blk07-after-start
ck "rc 0" rc0
ck "check passed after start" has "check passed"
ck "no FAIL line" nothas "FAIL"
extra "--check after start (guide part 4: must pass)"
BWHY=""
cin 'ss -ltn | grep -c ":4222 "'
ck "port 4222 listens after start" test "$OUT" = 1
extra "after start: port 4222 listens"

# ---- remove (a person block): first with no terminal (must refuse, exit 4,
# delete nothing), then on a pty with the directory typed
runnotty() { # N: a person block with NO terminal (stdin /dev/null)
    local nn
    nn=$(printf %02d "$1")
    [ "$(cat "$T/$BK/$nn.info")" = "sh person" ] || {
        echo "BUG: runnotty $PF$1 is not a person block" >&2
        exit 2
    }
    cin "$(cat "$T/$BK/$nn.sh")"
    save "$BK$nn-notty"
}
runnotty 9
ck "remove with no terminal: exit 4" test "$RC" -eq 4
cin '[ -d "$HOME/mash-nats" ] && echo dir-kept; [ -e "$HOME/mash-nats/ca.key" ] && echo cakey-kept; ss -ltn | grep -c ":4222 "'
save remove-notty-probe
ck "no terminal: server dir and ca.key still there, server still listens" bash -c \
    "printf '%s\n' \"$OUT\" | grep -qx dir-kept && printf '%s\n' \"$OUT\" | grep -qx cakey-kept && [ \"\$(printf '%s\n' \"$OUT\" | tail -n1)\" = 1 ]"
RC=0
extra "--remove with no terminal: refuses, deletes nothing (rc 4 asserted above)"
runpty 9 "$D"
ck "remove on a pty: rc 0" rc0
ck "remove: about to: stop nats-server pid" has "about to: stop nats-server pid"
ck "remove: did: stopped pid" has "did: stopped pid"
ck "remove: did: removed" has "did: removed"
row 9 "pty (script -qec), directory typed" "$RC"
cin '[ ! -e "$HOME/mash-nats" ] && echo gone; [ -f "$HOME/mash-setup/nats-local-setup.sh" ] && echo "script kept"'
ck "server directory gone" has gone
ck "mash-setup script kept (the guide says so)" has "script kept"
extra "after remove: server dir gone, mash-setup kept"
cin 'ss -ltn | grep ":4222 "; true'
ck "port 4222 free after remove" test -z "$OUT"
extra "after remove: nothing listens on 4222"

# ---- "Doing it again": the second --remove (same text as block 9), then rm -r
runpty 10 "$D"
save blk10-second-remove
ck "second remove after a remove: refuses, rc 1" test "$RC" -eq 1
ck "second remove: says the directory does not exist" has "does not exist or is not a directory"
row 10 "pty (script -qec), directory typed (second remove)" "$RC"
cin '[ -e "$HOME/mash-nats" ] && echo dir-back; [ -f "$HOME/mash-setup/nats-local-setup.sh" ] && echo "script kept"; true'
ck "after the second remove: no server directory, script still kept" bash -c \
    "! printf '%s\n' \"$OUT\" | grep -qx dir-back && printf '%s\n' \"$OUT\" | grep -qx 'script kept'"
extra "after the second remove: still no server dir, mash-setup kept"
runpty 11 ""
ck "rm -r mash-setup on a pty: rc 0" rc0
row 11 "pty (script -qec)" "$RC"
cin '[ ! -e "$HOME/mash-setup" ] && echo setup-gone'
ck "mash-setup is gone" has setup-gone
extra "after rm -r: $HOME/mash-setup is gone"
runblk 3
ck "part 2 last check after doing it again: rc 0" rc0
ck "part 2 last check prints only checked" test "$OUT" = checked
extra "block 3 again after doing it again: prints only checked"

if [ "$PENDING" = 1 ]; then
BK=pblk
say "S5 tamper tests of part 4 (each in a fresh container)"
SUB=$T/pblk/08.sub.sh
BWHY=""
# (a) a file swapped after download (a1), or served wrong (a2)
for v in a1 a2; do
    mode=normal
    [ $v = a2 ] && mode=badmash
    tb_new tamper-$v $mode
    tblk "$SUB" tamper-$v-b08
    ck "S5($v) gh block rc 0" rc0
    tblk "$(BN 9)" tamper-$v-b09
    ck "S5($v) download rc 0" rc0
    if [ $v = a1 ]; then
        cin 'cd "$HOME/mash-setup/dl" && printf x >>mash-linux-amd64'
        ck "S5(a1) the file is swapped after download" rc0
    fi
    tblk "$(BN 10)" tamper-$v-b10
    ck "S5($v) verify mash block fails (rc != 0)" rcn0
    ck "S5($v) Verification failed is shown" has "Verification failed"
    ck "S5($v) does not print verified mash" nothas "verified mash"
    tblk "$(BN 11)" tamper-$v-b11
    ck "S5($v) verify mashctl block still passes" rc0
    ck "S5($v) prints verified mashctl" has "verified mashctl"
    tblk "$(BN 12)" tamper-$v-b12
    ck "S5($v) SHA256SUMS check fails" rcn0
    ck "S5($v) sha256sum says FAILED" has "mash-linux-amd64: FAILED"
    # the guide says stop: the install block (13) is NOT run
    cin 'ls -A "$HOME/.local/bin"; ls -A "$HOME/mash-setup/dl" | tr "\n" " "'
    save tamper-$v-probe
    ck "S5($v) only gh is in .local/bin (no mash, no mashctl)" bash -c \
        "[ \"\$(printf '%s\n' \"$OUT\" | head -n1)\" = gh ] && ! printf '%s\n' \"$OUT\" | grep -qx 'mash\|mashctl'"
    ck "S5($v) the files stay in mash-setup/dl" has "mash-linux-amd64"
    extra "S5($v): $([ $v = a1 ] && echo 'file swapped after download' || echo 'tampered file served'): verify block fails, install block not run, nothing in .local/bin but gh"
    tb_end
done
# (b) a wrong gh tarball: the original block (real pinned hash, the stand-in
# tarball served) b1; the substituted block with a different tarball served b2
tb_new tamper-b1 normal
tblk "$T/pblk/08.sh" tamper-b1
ck "S5(b1) original gh block (real pinned hash) fails" rcn0
ck "S5(b1) sha256sum says FAILED" has "FAILED"
cin 'ls -A "$HOME/.local/bin" | wc -l; ls "$HOME/.local/bin/gh" 2>&1 | head -n1'
ck "S5(b1) gh is not installed" bash -c \
    "printf '%s\n' \"$OUT\" | grep -q '^0\$' && printf '%s\n' \"$OUT\" | grep -q 'No such file'"
extra "S5(b1): real pinned hash vs another tarball: block fails, gh not installed"
tb_end
tb_new tamper-b2 badgh
tblk "$SUB" tamper-b2
ck "S5(b2) substituted block, a different tarball served, fails" rcn0
ck "S5(b2) sha256sum says FAILED" has "FAILED"
cin 'ls -A "$HOME/.local/bin" | wc -l'
ck "S5(b2) nothing in .local/bin" test "$OUT" = 0
extra "S5(b2): wrong gh tarball: block fails, gh not installed"
tb_end
BK=blk
fi

# ------------------------------------------------- WHAT-MASH-NEEDS.md
say "WHAT-MASH-NEEDS.md: every fenced block run, or listed as not run"
NW=$(find "$T/wblk" -name '*.sh' | grep -c .)
BWHY=""
RC=0
ck "WHAT-MASH-NEEDS.md has 6 fenced blocks (got $NW)" test "$NW" -eq 6
extra "WHAT-MASH-NEEDS.md has 6 fenced blocks"
# info string and pattern were checked above (WINFO, WSIG); this lists them
wb() { # N DISPOSITION REASON
    printf '| W%s | `%s` | %s | %s |\n' "$1" "${WSIG[$1]}" "$2" "$3" >>"$T/wrows.md"
}
: >"$T/wrows.md"
wb 1 "documented, not run" "sample server.conf text, not a command"
wb 2 "RUN (fresh container)" "create on another port"
wb 3 "documented, not run" "needs a srv folder made first; --dir is the same code path the default run used"
wb 4 "documented, not run" "only changes the closing text; the folder itself is mashctl's (shimmed here)"
wb 5 "documented, not run" "a real adopt writes mashctl state; shimmed mashctl in this harness"
wb 6 "documented, not run (read-only --dry-run)" "uses long flags (--system-credentials) the in-tree mashctl does not have yet"
ck "WHAT block 6 is a --dry-run" grep -qF -- '--dry-run' "$T/wblk/06.sh"
RC=0
extra "WHAT-MASH-NEEDS.md: all 6 blocks classified"

# W2 run: the --port 4333 create, in a fresh container (helper copied in from
# this tree; its sha256 is checked against the guide's pin first)
BWHY=""
ck "helper in tree equals the guide's pinned sha256" test "$HELPERSHA" = "$GSHA"
if tb_new whatport normal; then
    podman exec --user root "$TB" mkdir -p /home/alice/mash-setup
    podman cp "$REPO/scripts/nats-local-setup.sh" "$TB:/home/alice/mash-setup/nats-local-setup.sh"
    podman exec --user root "$TB" chown -R alice:alice /home/alice/mash-setup
    cin "$(cat "$T/wblk/02.sh")"
    save what-port4333
    ck "W2 create on 4333 rc 0" rc0
    ck "W2 prints Done." has "Done."
    cin 'bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --check; echo "rc=$?"; ss -ltn | grep -c ":4333 "; ss -ltn | grep -c ":4222 "'
    save what-port4333-check
    ck "W2 check passed" has "check passed"
    ck "W2 check mentions 4333" has "4333"
    ck "W2 4333 listens, 4222 does not" bash -c \
        "[ \"\$(printf '%s\n' \"$OUT\" | tail -n2 | tr '\n' ' ')\" = '1 0 ' ]"
    extra "WHAT W2: --port 4333 create, then --check, in a fresh container"
    tb_end
fi

# ------------------------------------------------- the closing output
say "closing output"
CL=$T/out/closing.txt
BWHY=""
RC=0
norm() { sed -E 's/([0-9A-Fa-f]{2}:){31}[0-9A-Fa-f]{2}/<FINGERPRINT>/g;s/\b[0-9a-f]{8}( [0-9a-f]{8})+\b/<FINGERPRINT>/g'; }
ck "closing text captured" test -s "$CL"
ck "closing text <= 80 columns (the adopt line and a lone URL cannot wrap)" test \
    "$(grep -v 'adopt --from' "$CL" | awk 'length > 80 && !/^ *[^ ]+$/' | grep -c .)" -eq 0
ck "closing text has no \$HOME" test "$(grep -cF '$HOME' "$CL")" -eq 0
ck "closing text uses /home/alice paths" grep -q '/home/alice/' "$CL"
ck "closing Next is the README quick start only (no mashctl adopt, no part pointer)" test \
    "$(grep -cE 'mashctl adopt|part [0-9]|quick-start-one-machine-no-account' "$CL")" -eq 1
ck "closing text has no mashctl adopt" test \
    "$(grep -c 'mashctl adopt' "$CL")" -eq 0
ck "closing text has no long adopt flags" test \
    "$(grep -cE -- '--operator-key|--sys-creds|--system-credentials' "$CL")" -eq 0
ck "closing text has no pid or key id" test \
    "$(grep -Eic 'pid [0-9]+|[A-Z2-7]{50,}' "$CL")" -eq 0
extra "closing text: captured, <= 80 columns, real paths, no pid or key id"

if [ "$UPDATE" = 1 ] && [ -s "$CL" ]; then
    awk -v f="$CL" '
    /<!-- closing-begin -->/ {
        print; print "```"
        while ((getline l < f) > 0) print l
        print "```"; skip = 1; next
    }
    /<!-- closing-end -->/ { skip = 0 }
    !skip { print }' "$SKILL" >"$T/SKILL.new" && cp "$T/SKILL.new" "$SKILL"
    say "SKILL.md closing text updated"
fi
awk '/<!-- closing-begin -->/ { on = 1; next }
    /<!-- closing-end -->/ { on = 0 }
    on && $0 != "```"' "$SKILL" >"$T/out/embedded.txt"
norm <"$T/out/embedded.txt" >"$T/out/embedded.norm"
norm <"$CL" >"$T/out/closing.norm"
BWHY=""
ck "embedded closing text is not empty" test -s "$T/out/embedded.txt"
ck "embedded closing text equals the fresh output (CA fingerprint masked)" \
    diff -q "$T/out/embedded.norm" "$T/out/closing.norm"
extra "embedded closing text equals the fresh create output"

# neither document may carry the removed features (after any --update-closing)
: >"$T/skill-moved.txt"
BWHY=""
RC=0
for w in --retire-keys --backup --upgrade --enable-boot --disable-boot \
    --linger --listen --host MASH_INSECURE_PLAINTEXT; do
    grep -nF -- "$w" "$SKILL" "$WHAT" >>"$T/skill-moved.txt"
    ck "neither document contains $w" bash -c \
        "! grep -qF -- '$w' '$SKILL' '$WHAT'"
done
extra "neither document contains --retire-keys, --backup, --upgrade, --enable-boot, --disable-boot, --linger, --listen, --host, MASH_INSECURE_PLAINTEXT"


# ---- findings in SKILL.md that the planner asked to be told about (not failures)
: >"$T/skill-findings.txt"
for w in 'mashctl adopt' 'endpoint create' 'gh attestation'; do
    grep -nF -- "$w" "$SKILL" | sed "s/^/[$w] /" >>"$T/skill-findings.txt"
done
for w in Alice Bob; do
    grep -nw -- "$w" "$SKILL" | sed "s/^/[$w] /" >>"$T/skill-findings.txt"
done

# ---- section pointers ("part N", "section N") must point at headings that exist
BWHY=""
RC=0
python3 -I - "$SKILL" "$WHAT" >"$T/pointers.txt" <<'__PTR__'
import re, sys
skill = open(sys.argv[1]).read()
what = open(sys.argv[2]).read()
def heads(t):
    return {int(m.group(1)) for m in re.finditer(r'(?m)^#{1,4} (\d+)\. ', t)}
H = {"skill": heads(skill), "what": heads(what)}
bad = []
def scan(name, text, own, other):
    # a pointer is "part N" or "section N"; "part" means the guide (SKILL.md),
    # "section" means WHAT-MASH-NEEDS.md, whichever document it appears in
    for i, line in enumerate(text.split("\n"), 1):
        for m in re.finditer(r'(?i)\b(part|section)s? (\d+)\b', line):
            kind, n = m.group(1).lower(), int(m.group(2))
            target = "skill" if kind == "part" else "what"
            if n not in H[target]:
                bad.append("%s:%d: %s %d has no heading in %s" % (name, i, kind, n, target))
scan("SKILL.md", skill, "skill", "what")
scan("WHAT-MASH-NEEDS.md", what, "what", "skill")
for phrase in ("If something is wrong", "Certificates last ten years"):
    if phrase not in skill:
        bad.append('SKILL.md: quoted pointer "%s" has no target' % phrase)
for b in bad:
    print(b)
print("pointers-checked")
__PTR__
ck "every part/section pointer in both documents points at an existing heading" \
    bash -c "[ \"\$(grep -vc '^pointers-checked\$' '$T/pointers.txt')\" -eq 0 ]"
extra "section pointers (part N, section N) point at headings that exist"

SKILLSHA_AFTER=$(sha256sum "$SKILL" | cut -d' ' -f1)
WHATSHA_AFTER=$(sha256sum "$WHAT" | cut -d' ' -f1)
say "SKILL.md sha256 used for the blocks: $SKILLSHA"
say "SKILL.md sha256 now:                 $SKILLSHA_AFTER"
say "WHAT-MASH-NEEDS.md sha256:           $WHATSHA_AFTER"
say "helper sha256 (scripts/nats-local-setup.sh): $HELPERSHA"
say "guide URL commit: $GCOMMIT"

# ------------------------------------------------- report
say "writing the report"
{
    printf '# nats-local guide test report\n\n'
    printf 'RESULT: %s blocks/steps passed, %s failed; %s assertions passed, %s failed.\n\n' \
        "$BPASS" "$BFAIL" "$PASSN" "$FAILN"
    printf 'Mode: GUIDETEST_PENDING=%s (the parked parts %s).\n\n' "$PENDING" \
        "$([ "$PENDING" = 1 ] && echo 'were run' || echo 'were NOT run')"
    printf 'SKILL.md sha256 (when the blocks ran): %s\n' "$SKILLSHA"
    printf 'SKILL.md sha256 (at the end, after any --update-closing): %s\n' "$SKILLSHA_AFTER"
    printf 'WHAT-MASH-NEEDS.md sha256: %s\n' "$WHATSHA_AFTER"
    printf 'helper scripts/nats-local-setup.sh sha256: %s\n' "$HELPERSHA"
    printf 'guide script URL commit: %s\n\n' "$GCOMMIT"
    printf 'Guide checks: %s sh blocks; %s prose lines over 80 columns; longest sh block line %s columns (not a failure).\n\n' \
        "$NBLK" "$WIDE" "$LONGEST"
    printf '## Per-block results\n\n| n | first 60 chars | method | rc | result |\n|---|---|---|---|---|\n'
    cat "$T/rows.md"
    cat <<'__SUBS__'

## What was not the real thing (SUBSTITUTIONS)

@@
    ran twice: first with NO claude on the PATH (it prints `command not
    found`, rc 127: the guide's stop case, asserted), then with a stand-in
    script at /usr/local/bin/claude that prints one version line
    ("2.1.0 (Claude Code) [guide-test stand-in]"). It proves only that the
    block runs and prints a line, not that Claude Code is installed.
(b) Everything else in doc 1 is real: apt-get in the container, the pinned
    helper fetched from the real raw.githubusercontent.com URL at the pinned
    commit and checked against its pinned sha256, the real create (nats-server
    and nsc downloaded from GitHub, hash-checked by the script), real
    --check, --stop (twice), --start, and --remove.
(c) The image is debian:bookworm-slim plus sudo; user alice (uid 1000,
    NOPASSWD sudo); each block ran as `podman exec --user alice ... env -i
    HOME=/home/alice PATH=/usr/local/bin:/usr/bin:/bin bash -c '<block>'`,
    with no terminal. The `sh person` block (--remove) ran once with stdin
    /dev/null (refuses, exit 4, nothing deleted) and once under `script -qec`
    (a pty) with the directory typed after a 5 second wait. The helper's
    "ask the owner on a terminal" prompt is not exercised: the guide passes
    --owner acme, and the blocks have no terminal.
(d) Doc 1 ran on ONE container, in order. The WHAT-MASH-NEEDS.md --port
    example (W2) ran in its own fresh container (image with the prerequisites
    already installed); its other blocks are documented, not run (table).
(e) The CA fingerprint in the embedded closing text is masked in the
    comparison with the fresh output (it differs every run).
__SUBS__
    if [ "$PENDING" = 1 ]; then
    cat <<'__SUBSP__'

(P) PENDING mode ran the parked parts too, with the DRAFT7 substitutions:
    the gh block ran as a copy with the repacked tarball's hash and a gh
    stand-in; mash/mashctl come from the build of this tree through a curl
    shim (mashctl is a harness-only shim around the in-tree one, which lacks
    `adopt --from`, the fingerprint prompt and `endpoint create
    --creds-out`); MASH_ENDPOINT is mapped to MASH_OWNER/GROUP/NAME in
    copies. See the test script for the code. The tamper test (c)
    (program already present) is skipped: the parked text has no
    program-exists check.
__SUBSP__
    fi
    printf '\n## WHAT-MASH-NEEDS.md blocks\n\n| block | contains | status | reason |\n|---|---|---|---|\n'
    cat "$T/wrows.md"
    printf '\n## NOT VERIFIED\n\n'
    cat <<'__NV__'
- Claude Code itself (a stand-in prints the version line).
- The helper's terminal prompt for the owner (no terminal in the blocks).
- A real reboot (the guide says there is no start at boot).
- Any distribution other than Debian 12 (bookworm), and Ubuntu.
- The parked parts (install mash and mashctl, adopt, endpoints, Alice and
  Bob, tamper tests): not in SKILL.md now; run only with GUIDETEST_PENDING=1.
- The closing box's `mashctl adopt --from` and "part 4 of the guide" are the
  helper's text, embedded as printed; they are not run here.
__NV__
    printf '\n## Removed-feature scan (lines of either document naming a removed flag; empty is good)\n\n```\n'
    cat "$T/skill-moved.txt"
    printf '```\n'
    printf '\n## SKILL.md findings (mashctl adopt, endpoint create, gh attestation, Alice, Bob; reported, not failures)\n\n```\n'
    cat "$T/skill-findings.txt"
    printf '```\n'
    printf '\n## Pointer check output\n\n```\n'
    cat "$T/pointers.txt"
    printf '```\n'
    printf '\n## Real output (trimmed)\n'
    sec() { # TITLE FILE [LINES]
        [ -f "$2" ] || return 0
        printf '\n### %s\n\n```\n' "$1"
        head -n "${3:-40}" "$2"
        printf '```\n'
    }
    sec "create output, from Done. to the end" "$CL" 60
    for f in "$T"/out/*.txt; do
        b=${f##*/}
        [ "$b" = closing.txt ] && continue
        [ "$b" = embedded.txt ] && continue
        sec "${b%.txt}" "$f" 40
    done
} >"$T/report.md"

printf '\nRESULT: %s blocks/steps passed, %s failed; %s assertions passed, %s failed\n' \
    "$BPASS" "$BFAIL" "$PASSN" "$FAILN"
[ "$BFAIL" -eq 0 ]
