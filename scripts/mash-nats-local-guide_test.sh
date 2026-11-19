#!/usr/bin/env bash
# Test for docs/skills/mash-nats-local/SKILL.md (the guide, DRAFT 6).  Run with:
#
#     bash scripts/mash-nats-local-guide_test.sh [--update-closing]
#
# It extracts every fenced block whose info string is `sh` or `sh person`,
# in order, and runs each one in ONE fresh rootless podman container (debian
# bookworm-slim) as user alice: each block as its own `bash -c`, with
# HOME=/home/alice and PATH=/usr/local/bin:/usr/bin:/bin only; the `sh
# person` blocks that need a typed confirmation (retire-keys, remove) on a
# pseudo-terminal with it fed in.  Then four tamper tests of the part-4
# block, each in its own fresh container.  Nothing runs on the host except
# the Go build (in a scratch copy of the repo), git (read-only) and podman.
# The SUBSTITUTIONS section of the report says what differs from the guide.
# Containers and images are removed by exact (suffixed) name.
#
# --update-closing writes the helper's closing output (from the create block)
# into SKILL.md between the closing-begin/closing-end markers; without it the
# fresh output is compared with what is embedded.
# GUIDETEST_REPORT=FILE sets where the report goes (default: none kept);
# GUIDETEST_OUT=DIR (must not exist) keeps every block's raw output.
# Needs: podman (rootless), go, python3, git, network (apt, GitHub).
set -u

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P) || exit 2
REPO=$(dirname "$HERE")
SKILL=$REPO/docs/skills/mash-nats-local/SKILL.md
[ -f "$SKILL" ] || { echo "no $SKILL" >&2; exit 2; }
for c in podman go python3 awk git; do
    command -v "$c" >/dev/null || { echo "$c is required" >&2; exit 2; }
done
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
mkdir -p "$T/out" "$T/blk" "$T/bin" "$T/gocache" "$T/ctx" "$T/assets"

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
NBLK=$(find "$T/blk" -name '*.sh' | grep -c .)
WIDE=0
[ -f "$T/blk/wide.txt" ] && WIDE=$(grep -c . "$T/blk/wide.txt")
WWIDE=0
[ -f "$T/wblk/wide.txt" ] && WWIDE=$(grep -c . "$T/wblk/wide.txt")
LONGEST=$(cat "$T"/blk/*.sh | awk '{ if (length > m) m = length } END { print m + 0 }')
LONGEST_ASCII=$(grep -c '[^ -~]' "$SKILL" || true)


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
        mash-linux-amd64 | mashctl-linux-amd64 | mash.intoto.jsonl)
            a=file:///opt/guide-assets/$f
            [ "$mode" = badmash ] && [ "$f" = mash-linux-amd64 ] &&
                a=file:///opt/guide-assets/mash-evil ;;
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

putassets() { # CONTAINER MODE: the shared asset directory
    local c=$1 f
    podman exec --user root "$c" mkdir -p /opt/guide-assets
    for f in mash-linux-amd64 mashctl-linux-amd64 mashctl-real \
        mash.intoto.jsonl mash-evil expected.sha256 gh-wrapper; do
        podman cp "$T/assets/$f" "$c:/opt/guide-assets/$f"
    done
    printf '%s\n' "$2" >"$T/mode"
    podman cp "$T/mode" "$c:/opt/guide-assets/mode"
    podman exec --user root "$c" chmod -R a+rX /opt/guide-assets
}
putassets "$C" normal

# ------------------------------------------------- runners
PASSN=0
FAILN=0
BPASS=0
BFAIL=0
BWHY=""
OUT=""
RC=0
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
sig() { grep -qF -- "$2" "$T/blk/$(printf %02d "$1").sh"; }
rc0() { [ "$RC" -eq 0 ]; }
rcn0() { [ "$RC" -ne 0 ]; }
nothas() { ! has "$1"; }
row() { # N METHOD RC
    local st=PASS
    [ -z "$BWHY" ] || st="FAIL$BWHY"
    case $st in PASS) BPASS=$((BPASS + 1)) ;; *) BFAIL=$((BFAIL + 1)) ;; esac
    local first
    first=$(head -c 60 "$T/blk/$(printf %02d "$1").sh" | tr '\n' ' ')
    printf '| %s | `%s` | %s | %s | %s |\n' "$1" \
        "$(printf '%s' "$first" | sed 's/|/\\|/g')" "$2" "$3" "$st" \
        >>"$T/rows.md"
    printf '%s  block %s (%s rc=%s)%s\n' "${st%%;*}" "$1" "$2" "$3" \
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
    if [ "$(cat "$T/blk/$(printf %02d "$1").info")" = "sh person" ]; then
        echo "BUG: runblk $1 is a person block" >&2
        exit 2
    fi
    f=$T/blk/$(printf %02d "$1").sh
    cin "$(cat "$f")"
    save "blk$(printf %02d "$1")"
}
runpty() { # N TYPED: on a pty (script -qec), the confirmation fed in
    local nn f
    nn=$(printf %02d "$1")
    f=$T/blk/$nn.sh
    podman cp "$f" "$C:$CIP/blk$nn.sh"
    podman exec --user root "$C" chown alice:alice "$CIP/blk$nn.sh"
    OUT=$({ sleep 5; printf '%s\n' "$2"; } |
        podman exec -i --user alice -w "$CIP" "$C" env -i HOME="$CIP" \
            PATH=/usr/local/bin:/usr/bin:/bin \
            script -qec "bash -c \"\$(cat $CIP/blk$nn.sh)\"" /dev/null 2>&1)
    RC=$?
    OUT=$(printf '%s\n' "$OUT" | tr -d '\r')
    save "blk$nn"
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

# ------------------------------------------------- the guide's own checks
say "checking the guide itself"
BWHY=""
RC=0
ck "block count is 33 (got $NBLK)" test "$NBLK" -eq 33
ck "$WIDE SKILL.md prose lines over 80 columns (fences, tables, URL-only lines exempt)" \
    test "$WIDE" -eq 0
ck "$WWIDE WHAT-MASH-NEEDS.md prose lines over 80 columns" test "$WWIDE" -eq 0
ck "non-ASCII characters in SKILL.md" test "$LONGEST_ASCII" -eq 0
ck "non-ASCII characters in WHAT-MASH-NEEDS.md" \
    test "$(grep -c '[^ -~]' "$WHAT" || true)" -eq 0
extra "guide: 33 sh blocks, prose <= 80 columns in both documents, ASCII"

# expected signature per block (so a reordered guide fails loudly)
declare -A SIG=(
    [1]='command -v "$c"' [2]='sudo apt-get install' [3]='exists: $p'
    [4]='sha256sum -c -' [5]='--owner acme --start' [6]='--check'
    [7]='" version;' [8]='installed and verified in'
    [9]='bin/mashctl" version' [10]='adopt --from "$HOME/mash-nats" --dry-run'
    [11]='adopt --from "$HOME/mash-nats"' [12]='stat -c'
    [13]='create acme.demo.alice' [14]='create acme.demo.bob'
    [15]='endpoint list' [16]='MASH_ENDPOINT=acme.demo.alice'
    [17]='MASH_ENDPOINT=acme.demo.bob' [18]='echo "same $p"'
    [19]='--retire-keys' [20]='mv -i' [21]='--check' [22]='--stop'
    [23]='--start' [24]='--backup' [25]='not restored:' [26]='--upgrade'
    [27]='--enable-boot' [28]='--disable-boot' [29]='--remove'
    [30]='rm -r "$HOME/.config/mash" "$HOME/mash-setup"'
    [31]='rm "$HOME/.local/bin/mash"' [32]='rm -f'
    [33]='create acme.demo.design'
)
declare -A KIND=([19]=person [27]=person [28]=person [29]=person)
for n in $(seq 1 33); do
    want="sh"
    [ -n "${KIND[$n]:-}" ] && want="sh person"
    got=$(cat "$T/blk/$(printf %02d "$n").info" 2>/dev/null)
    BWHY=""
    ck "info string is '$got', expected '$want'" test "$got" = "$want"
    ck "block $n does not contain '${SIG[$n]}'" sig "$n" "${SIG[$n]}"
    if [ -n "$BWHY" ]; then
        extra "block $n identity"
    fi
done
BWHY=""

# static checks over the block text
blkcount() { # N PATTERN COUNT
    [ "$(grep -o -- "$2" "$T/blk/$(printf %02d "$1").sh" | grep -c .)" -eq "$3" ]
}
ck "no fenced block in SKILL.md or WHAT-MASH-NEEDS.md contains the word exit" \
    bash -c "! grep -qw exit \"$T\"/sall/*.sh \"$T\"/wblk/*.sh"
ck "part-4 block (8) has exactly two 'attestation verify' calls" \
    blkcount 8 'attestation verify' 2
ck "part-4 block (8) tail is '); [ \$? -eq 0 ] || {' (set -e in force)" \
    sig 8 '); [ $? -eq 0 ] || { echo "NOT installed'
ck "part-4 block (8) is a multi-line subshell with set -eu" bash -c \
    "[ \"\$(head -n1 \"$T/blk/08.sh\")\" = '(' ] && sed -n 2p \"$T/blk/08.sh\" | grep -qx 'set -eu'"
ck "the guide says the person runs the sh person blocks (and the agent never)" \
    grep -q 'never run' "$SKILL"
ck "every block that retires, removes, or sets boot is marked sh person" bash -c \
    "for f in \"$T\"/blk/[0-9][0-9].sh; do b=\${f%.sh}; if grep -qE -- '--retire-keys|--remove|--enable-boot|--disable-boot' \$f && [ \"\$(cat \$b.info)\" != 'sh person' ]; then echo \"unmarked: \$f\"; exit 1; fi; done"
ck "block 16 and 17 use MASH_ENDPOINT" bash -c \
    "grep -qF MASH_ENDPOINT \"$T/blk/16.sh\" && grep -qF MASH_ENDPOINT \"$T/blk/17.sh\""
ck "no block has MASH_TLS_CA, MASH_OWNER, MASH_GROUP or MASH_NAME" \
    bash -c "! grep -qE 'MASH_TLS_CA|MASH_OWNER|MASH_GROUP|MASH_NAME' \"$T\"/blk/[0-9][0-9].sh"
ck "adopt blocks use --from and no long flags" bash -c \
    "grep -qF 'adopt --from' \"$T/blk/10.sh\" && grep -qF 'adopt --from' \"$T/blk/11.sh\" && ! grep -qE -- '--server|--operator-key|--ca ' \"$T\"/blk/1[01].sh"
ck "endpoint create blocks have --creds-out" bash -c \
    "grep -qF -- '--creds-out' \"$T/blk/13.sh\" && grep -qF -- '--creds-out' \"$T/blk/14.sh\""
extra "guide text: no 'exit', two verify calls, MASH_ENDPOINT only, --from, --creds-out"

# the script URL commit and sha in the guide equal git's
GCOMMIT=$(grep -o 'raw.githubusercontent.com/bogworks/mash/[0-9a-f]\{40\}/scripts/nats-local-setup.sh' \
    "$T/blk/04.sh" | cut -d/ -f4)
GSHA=$(grep -o '[0-9a-f]\{64\}  \$HOME/mash-setup/nats-local-setup.sh' \
    "$T/blk/04.sh" | cut -c1-64)
LASTC=$(git -C "$REPO" log -1 --format=%H -- scripts/nats-local-setup.sh)
FULLC=$(git -C "$REPO" rev-parse "${GCOMMIT:-none}^{commit}" 2>/dev/null)
HELPERSHA=$(sha256sum "$REPO/scripts/nats-local-setup.sh" | cut -d' ' -f1)
SKILLSHA=$(sha256sum "$SKILL" | cut -d' ' -f1)
COMMITSHA=$(git -C "$REPO" show "${GCOMMIT:-none}:scripts/nats-local-setup.sh" 2>/dev/null | sha256sum | cut -d' ' -f1)
ck "guide URL commit is 40 hex" test "${#GCOMMIT}" -eq 40
ck "git rev-parse of the guide's commit equals it" test "$FULLC" = "$GCOMMIT"
ck "it is the last commit that changed the script ($LASTC)" test "$LASTC" = "$GCOMMIT"
ck "the script at that commit has the guide's sha256" test "$COMMITSHA" = "$GSHA"
ck "the script in this tree has the guide's sha256" test "$HELPERSHA" = "$GSHA"
ck "that commit is on a pushed (remote) branch" bash -c \
    "[ -n \"\$(git -C '$REPO' branch -r --contains '$GCOMMIT' 2>/dev/null)\" ]"
PIN_COMMIT=8ac4d0b0ee09240fe72c1ac5637444b44d25570f
PIN_SHA=900f4fcb5b327cc0204954e27536d51b5aecd674817e306b895d11ef989692b0
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

# ------------------------------------------------- run the blocks
D=$CIP/mash-nats
CA=$D/ca.pem
say "running the blocks"

# 1: missing check, before apt
runblk 1
ck "rc 0" rc0
for m in curl unzip openssl ss; do ck "missing: $m is reported" has "missing: $m"; done
ck "tar is not reported missing" nothas "missing: tar"
ck "sha256sum is not reported missing" nothas "missing: sha256sum"
ck "prints checked" has checked
row 1 "bash -c" "$RC"

# 2: apt (the block now runs apt-get update itself)
runblk 2
ck "rc 0" rc0
row 2 "bash -c" "$RC"
podman cp "$T/assets/curl" "$C:/usr/local/bin/curl"
podman exec --user root "$C" chmod 755 /usr/local/bin/curl
RC=0
extra "install curl shim at /usr/local/bin/curl (after apt)"
runblk 1
save blk01-after-apt
ck "rc 0" rc0
ck "nothing missing after apt" nothas "missing:"
ck "prints checked" has checked
extra "block 1 again, after apt: nothing missing"

# 3: nothing exists
runblk 3
ck "rc 0" rc0
ck "no 'exists:' line" nothas "exists:"
ck "no port line" nothas "LISTEN"
ck "prints only checked" test "$OUT" = checked
row 3 "bash -c" "$RC"

# 4: fetch the script
runblk 4
ck "rc 0" rc0
ck "sha256sum says OK" has ": OK"
row 4 "bash -c" "$RC"

# 5: create --start
runblk 5
ck "rc 0" rc0
ck "prints Done." has "Done."
row 5 "bash -c" "$RC"
awk '/^Done\./ { p = 1 } p' "$T/out/blk05.txt" >"$T/out/closing.txt"

# 6: check
runblk 6
ck "rc 0" rc0
ck "no FAIL line" nothas "FAIL"
ck "check passed" has "check passed"
row 6 "bash -c" "$RC"

# 7: mash/mashctl are absent
runblk 7
ck "rc 0" rc0
ck "two No such file" nlines "No such file" 2
ck "prints checked" has checked
row 7 "bash -c" "$RC"

# the pinned gh: the real tarball must have the sha256 the guide pins; then a
# repacked copy (bin/gh = the verify stand-in) is made for the served download
GHSHA=$(grep -o '[0-9a-f]\{64\}  \$G.tar.gz' "$T/blk/08.sh" | cut -c1-64)
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

# the part-4 block with the pinned gh hash replaced by the repacked tarball's
sed "s/$GHSHA/$FAKESHA/" "$T/blk/08.sh" >"$T/blk/08.sub.sh"
ck "the substitution changed exactly the gh hash" bash -c \
    "[ \"\$(diff '$T/blk/08.sh' '$T/blk/08.sub.sh' | grep -c '^>')\" -eq 1 ]"
ck "the original block still has the real pinned hash" sig 8 "$GHSHA"
RC=0
extra "substituted copy of block 8 (gh hash -> repacked tarball's), made"

# 8: the part-4 block (the substituted copy)
cin "$(cat "$T/blk/08.sub.sh")"
save blk08
ck "rc 0" rc0
ck "gh tarball sha256 OK" has "gh_2.105.0_linux_amd64.tar.gz: OK"
ck "prints installed and verified in" has "installed and verified in /home/alice/.local/bin"
ck "no NOT installed" nothas "NOT installed"
row 8 "bash -c (hash-substituted copy)" "$RC"
cin 'cat "$HOME/gh-wrapper.log"; ls -l "$HOME/.local/bin" | tail -n +2 | wc -l; ls "$HOME/mash-setup"'
save blk08-probe
ck "stand-in saw 4 verify calls" test "$(printf '%s\n' "$OUT" | grep -c 'attestation verify')" -eq 4
ck "three programs in .local/bin" has "3"
ck "the work folder is under mash-setup" has install-
extra "block 8: four verify calls (two before, two after install), three files"
cin 'stat -c "%a %n" "$HOME/mash-setup/"install-*'
save blk08-mode
ck "work folder is mode 700" has "700 /home/alice/mash-setup/install-"
extra "block 8: the work folder is mode 700"

# 9: mashctl version
runblk 9
ck "rc 0" rc0
ck "mashctl version printed" has mashctl
row 9 "bash -c" "$RC"

# 10: dry run (through the harness shim)
runblk 10
ck "rc 0" rc0
ck "All checks passed" has "All checks passed"
ck "dry-run says nothing is written" has "nothing is written"
ck "no FAIL or error line" nothas "FAIL"
row 10 "bash -c (adopt --from via shim)" "$RC"

# 11: adopt (an ordinary block now: this machine types no fingerprint)
runblk 11
ck "rc 0" rc0
ck "All checks passed" has "All checks passed"
ck "mashadmin.creds copied, not recorded" nothas "not copied"
ck "no fingerprint prompt" nothas "ingerprint"
row 11 "bash -c, no pty (adopt --from via shim)" "$RC"
cin 'ls "$HOME/.config/mash" "$HOME/.config/mash/server" "$HOME/.config/mash/keys"'
save blk11-ls
ck "config.json written (init ran)" has config.json
ck "mashadmin.creds in server/" has mashadmin.creds
extra "after adopt+init: config.json, keys and creds are in mashctl's folder"

# 12: stat
runblk 12
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
row 12 "bash -c" "$RC"
# model folder: the five entries the guide expects, run through the block text
# with HOME pointed at it (the test makes and keeps it inside the container)
cin 'M=$HOME/stat-model; mkdir -p -m 700 "$M/.config/mash/keys" "$M/.config/mash/server" && chmod 700 "$M/.config/mash" && : >"$M/.config/mash/ca.pem" && chmod 644 "$M/.config/mash/ca.pem" && : >"$M/.config/mash/config.json" && chmod 600 "$M/.config/mash/config.json"'
ck "model folder made" rc0
OUTM=$(cexec env HOME=$CIP/stat-model bash -c "$(cat "$T/blk/12.sh")" 2>&1 </dev/null)
ORDERM=$(printf '%s\n' "$OUTM" | awk '{ printf "%s ", $1 }')
ORDERMN=$(printf '%s\n' "$OUTM" | sed 's|.*/||' | tr '\n' ' ')
ck "MODEL folder order is 700 644 600 700 700 (got: $ORDERM)" test "$ORDERM" = "700 644 600 700 700 "
ck "MODEL names: mash ca.pem config.json keys server (got: $ORDERMN)" \
    test "$ORDERMN" = "mash ca.pem config.json keys server "
extra "block 12 on a model folder: the guide's five-entry order holds"

# 13-15: endpoints (--creds-out through the shim)
runblk 13
ck "rc 0" rc0
ck "address shown with dots" has acme.demo.alice
row 13 "bash -c (--creds-out dropped by shim)" "$RC"
runblk 14
ck "rc 0" rc0
row 14 "bash -c (--creds-out dropped by shim)" "$RC"
runblk 15
ck "rc 0" rc0
ck "alice listed" has acme.demo.alice
ck "bob listed" has acme.demo.bob
row 15 "bash -c" "$RC"
cin 'stat -c "%a %n" "$HOME/.config/mash/acme.demo.alice.creds" "$HOME/.config/mash/acme.demo.bob.creds"'
save creds-modes
ck "alice creds 600" has "600 /home/alice/.config/mash/acme.demo.alice.creds"
ck "bob creds 600" has "600 /home/alice/.config/mash/acme.demo.bob.creds"
extra "the agents' creds files are where the guide says, mode 600"

# 16-17: alice -> bob.  A COPY of each block maps MASH_ENDPOINT=o.g.n to
# MASH_OWNER=o MASH_GROUP=g MASH_NAME=n (the in-tree mash does not read
# MASH_ENDPOINT yet).
mapep() { sed -E 's/MASH_ENDPOINT=([a-z0-9-]+)\.([a-z0-9-]+)\.([a-z0-9-]+)/MASH_OWNER=\1 MASH_GROUP=\2 MASH_NAME=\3/' "$1"; }
mapep "$T/blk/16.sh" >"$T/blk/16.sub.sh"
mapep "$T/blk/17.sh" >"$T/blk/17.sub.sh"
cin "$(cat "$T/blk/16.sub.sh")"
save blk16
ck "rc 0" rc0
ck "sent" has "sent"
row 16 "bash -c (MASH_ENDPOINT mapped in a copy)" "$RC"
cin "$(cat "$T/blk/17.sub.sh")"
save blk17
ck "rc 0" rc0
ck "[verified]" has "[verified]"
ck "Hello Bob" has "Hello Bob"
ck "from alice" has acme.demo.alice
row 17 "bash -c (MASH_ENDPOINT mapped in a copy)" "$RC"

# 18: cmp
runblk 18
ck "rc 0" rc0
ck "same four times" nlines "same " 4
ck "no differ" nothas differ
row 18 "bash -c" "$RC"

# 29 (negative): --remove must refuse before --retire-keys has archived the keys
runpty 29 "$D"
save remove-before-retire
ck "remove before retire: rc non-zero" rcn0
cin '[ -d "$HOME/mash-nats" ] && echo dir-kept; [ -e "$HOME/mash-nats/ca.key" ] && echo cakey-kept; [ -d "$HOME/mash-nats/nsc" ] && echo nsc-kept; ss -ltn | grep -c ":4222 "'
ck "remove before retire: server dir, ca.key and nsc/ all still there" bash -c \
    "printf '%s\n' \"$OUT\" | grep -qx dir-kept && printf '%s\n' \"$OUT\" | grep -qx cakey-kept && printf '%s\n' \"$OUT\" | grep -qx nsc-kept"
ck "remove before retire: server still listens on 4222" \
    test "$(printf '%s\n' "$OUT" | tail -n1)" = 1
RC=0
extra "negative: --remove before --retire-keys refuses and deletes nothing"

# 19: retire-keys on a pty
runpty 19 "$D"
ck "rc 0" rc0
row 19 "pty (script -qec)" "$RC"
cin 'for p in export ca.key nsc; do [ -e "$HOME/mash-nats/$p" ] && echo "STILL: $p"; done; stat -c "%a %n" "$HOME/mash-nats-keys.tar.gz"; tar tzf "$HOME/mash-nats-keys.tar.gz" >"$HOME/keys.lst"; echo "tar rc=$?"; head -n 8 "$HOME/keys.lst"'
save retire-probe
ck "export/, ca.key, nsc/ are gone" nothas "STILL:"
ck "archive mode is 600" has "600 /home/alice/mash-nats-keys.tar.gz"
ck "archive lists back" has "tar rc=0"
extra "after retire-keys: files gone, archive 600 and lists back"

# 20: move the archive
runblk 20
ck "rc 0" rc0
cin 'stat -c "%a %n" "$HOME/archive/mash-nats-keys.tar.gz"; [ ! -e "$HOME/mash-nats-keys.tar.gz" ] && echo moved'
ck "archive in \$HOME/archive, mode 600" has "600 /home/alice/archive/mash-nats-keys.tar.gz"
ck "gone from home" has moved
row 20 "bash -c" "$RC"

# 21: check after
runblk 21
ck "rc 0" rc0
ck "no FAIL line" nothas "FAIL"
ck "check passed" has "check passed"
row 21 "bash -c" "$RC"

answers() { # the server answers through mashctl, with no MASH_TLS_CA
    cin '$HOME/.local/bin/mashctl endpoint list'
    ck "mashctl endpoint list rc 0" rc0
    ck "alice still listed" has alice
}

# 22-24: lifecycle
runblk 22
ck "rc 0" rc0
ck "stopped pid" has "stopped pid"
row 22 "bash -c" "$RC"
cin 'ss -ltn | grep ":4222 "; true'
ck "port 4222 no longer listening" test -z "$OUT"
extra "after stop: nothing listens on 4222"

runblk 23
ck "rc 0" rc0
row 23 "bash -c" "$RC"
BWHY=""
answers
extra "after start: the server answers (mashctl endpoint list)"

runblk 24
ck "rc 0" rc0
row 24 "bash -c" "$RC"
cin 'stat -c "%a %n" "$HOME/mash-nats-backup.tar.gz"; tar tzf "$HOME/mash-nats-backup.tar.gz" | head -n 5'
save backup-probe
ck "backup archive mode 600" has "600 /home/alice/mash-nats-backup.tar.gz"
answers
extra "after backup: archive 600, the server answers again"

# 25: restore.  First as written (the directory exists: must refuse), then
# the guide's own prose: stop, move the directory away, run it, upgrade,
# start, check.
runblk 25
ck "rc 0" rc0
ck "refuses: already exists" has "not restored: /home/alice/mash-nats already exists"
row 25 "bash -c (directory exists: refuses)" "$RC"
cin 'bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --stop && mv "$HOME/mash-nats" "$HOME/mash-nats.moved"'
ck "stop and move aside" rc0
runblk 25
save blk25-restore
ck "rc 0" rc0
ck "prints restored" has restored
row 25 "bash -c (second run: directory moved aside)" "$RC"
cin 'ls "$HOME/mash-nats"; stat -c "%a %n" "$HOME/mash-nats"; [ ! -e "$HOME/mash-nats/bin" ] && echo "no bin"'
save restore-probe
ck "server.conf restored" has server.conf
ck "directory mode 700" has "700 /home/alice/mash-nats"
ck "bin/ not in the backup" has "no bin"
extra "after restore: files back, mode 700, no bin/"
runblk 26
save blk26
ck "rc 0 (--upgrade makes bin/ in a restored directory)" rc0
row 26 "bash -c" "$RC"
cin 'ls "$HOME/mash-nats/bin"; stat -c "%a %n" "$HOME/mash-nats/bin"'
ck "bin/ has nats-server and nsc" has nats-server
ck "bin/ mode 700" has "700 /home/alice/mash-nats/bin"
extra "after --upgrade: bin/ made, mode 700"
runblk 26
save blk26-again
ck "second --upgrade rc 0 (nothing to do)" rc0
extra "--upgrade again is a no-op"
runblk 23
save blk23-after-restore
ck "start after restore rc 0" rc0
extra "start after restore"
runblk 21
save blk21-after-restore
ck "check after restore rc 0" rc0
ck "check passed" has "check passed"
extra "check after restore"
answers
extra "after restore, upgrade and start: the server answers"

# 33: What next, the design endpoint (run here, while the server is up)
runblk 33
ck "rc 0" rc0
ck "address shown" has acme.demo.design
row 33 "bash -c (run before remove; --creds-out dropped by shim)" "$RC"
cin '$HOME/.local/bin/mashctl endpoint list'
ck "design listed" has acme.demo.design
extra "after block 33: acme.demo.design is listed"

# 27, 28: boot service needs systemd; not run here
BWHY=""
for n in 27 28; do
    printf '| %s | `%s` | skipped | - | NOT RUN (no systemd here; covered by nats-local-setup_test.sh boot group) |\n' \
        "$n" "$(head -c 60 "$T/blk/$n.sh" | sed 's/|/\\|/g')" >>"$T/rows.md"
done

# 29: remove
runpty 29 "$D"
ck "rc 0" rc0
row 29 "pty (script -qec)" "$RC"
cin '[ ! -e "$HOME/mash-nats" ] && echo gone; [ -d "$HOME/.config/mash" ] && echo "mashctl folder kept"'
ck "server directory gone" has gone
ck "mashctl folder kept" has "mashctl folder kept"
extra "after remove: server dir gone, mashctl folder kept"
cin 'ss -ltn | grep ":4222 "; true'
ck "port 4222 free after remove" test -z "$OUT"
extra "after remove: nothing listens on 4222"
# 30-32: the rest of "Remove it completely" (blocks 19, 29, 30, 31, 32 ran in
# the guide's order: retire, remove, config+downloads, programs, archives)
for n in 30 31 32; do
    runblk $n
    ck "rc 0" rc0
    row $n "bash -c" "$RC"
done
cin '[ ! -e "$HOME/.config/mash" ] && echo a; [ ! -e "$HOME/mash-setup" ] && echo b; [ ! -e "$HOME/archive/mash-nats-keys.tar.gz" ] && [ ! -e "$HOME/mash-nats-backup.tar.gz" ] && echo c; [ -z "$(ls -A "$HOME/.local/bin")" ] && echo d'
ck "mashctl folder, mash-setup, archives gone, .local/bin empty"     test "$OUT" = "$(printf 'a
b
c
d')"
extra "after blocks 30-32: everything the guide made is gone"

# ------------------------------------------------- S5: tamper tests of block 8
# each in its own fresh container (image with the prerequisites in), running a
# hash-substituted copy of the part-4 block against the shim curl and the
# gh stand-in (which compares file hashes with the genuine assets).
C_MAIN=$C
tb_new() { # SUFFIX MODE
    TB=mash-guidetest-$RUNID-$1
    BOXES+=("$TB")
    podman run -d --name "$TB" --user alice "$IMG-pre" sleep 3600 >/dev/null || return 1
    C=$TB
    putassets "$TB" "$2"
    podman cp "$T/assets/curl" "$TB:/usr/local/bin/curl"
    podman cp "$T/assets/gh-fake.tar.gz" "$TB:/opt/guide-assets/gh-fake.tar.gz"
    podman cp "$T/assets/gh-evil.tar.gz" "$TB:/opt/guide-assets/gh-evil.tar.gz"
    podman exec --user root "$TB" chmod 755 /usr/local/bin/curl
    podman exec --user root "$TB" chmod a+r /opt/guide-assets/gh-fake.tar.gz \
        /opt/guide-assets/gh-evil.tar.gz
}
tb_end() {
    podman rm -f "$TB" >/dev/null 2>&1
    C=$C_MAIN
}

putmode() { printf '%s\n' "$1" >"$T/mode"; podman cp "$T/mode" "$TB:/opt/guide-assets/mode"; podman exec --user root "$TB" chmod a+r /opt/guide-assets/mode; }

tamper_all() { # LABEL BLOCKFILE(substituted) BLOCKFILE(original) SUFFIX
    local L=$1 BF=$2 OF=$3 X=$4 x
    # (a) mash-linux-amd64 substituted before verify
    tb_new tamper-a-$X badmash
    cin "$(cat "$BF")"
    save tamper-a-$X
    ck "$L (a) block fails (rc != 0)" rcn0
    ck "$L (a) verification failed is shown" has "Verification failed"
    ck "$L (a) prints NOT installed, or NOT verified" has "NOT installed, or NOT verified"
    ck "$L (a) does not print installed and verified" nothas "installed and verified"
    cin 'ls -A "$HOME/.local/bin" | wc -l'
    ck "$L (a) nothing in \$HOME/.local/bin" test "$OUT" = 0
    extra "S5(a) $L: substituted mash before verify: refused, nothing installed"
    tb_end
    # (b) mash swapped after the first verification, before the re-verify
    tb_new tamper-b-$X swap3
    cin "$(cat "$BF")"
    save tamper-b-$X
    ck "$L (b) block fails (rc != 0)" rcn0
    ck "$L (b) re-verify failure shown" has "Verification failed"
    ck "$L (b) prints NOT installed" has "NOT installed"
    ck "$L (b) does not print installed and verified" nothas "installed and verified"
    cin 'echo "bin=$(ls -A "$HOME/.local/bin" | wc -l)"; echo "rejected=$(ls "$HOME/mash-setup/rejected" 2>&1 | tr "\n" " ")"; echo "calls=$(wc -l <"$HOME/gh-wrapper.log")"'
    save tamper-b-$X-probe
    ck "$L (b) .local/bin is empty" has "bin=0"
    ck "$L (b) rejected has gh mash mashctl" has "rejected=gh mash mashctl "
    ck "$L (b) three verify calls (two before, one failing re-verify)" has "calls=3"
    extra "S5(b) $L: mash swapped between verifications: moved to rejected"
    tb_end
    # (c) a wrong gh tarball hash
    tb_new tamper-c-$X normal
    cin "$(cat "$OF")"
    save tamper-c1-$X
    ck "$L (c1) original block (real pinned hash, stand-in tarball) fails" rcn0
    ck "$L (c1) sha256sum says FAILED" has "FAILED"
    ck "$L (c1) prints NOT installed, or NOT verified" has "NOT installed, or NOT verified"
    cin 'ls -A "$HOME/.local/bin" | wc -l'
    ck "$L (c1) nothing in .local/bin" test "$OUT" = 0
    extra "S5(c1) $L: real pinned hash vs another tarball: refused"
    tb_end
    tb_new tamper-c2-$X badgh
    cin "$(cat "$BF")"
    save tamper-c2-$X
    ck "$L (c2) substituted block, a different tarball served, fails" rcn0
    ck "$L (c2) sha256sum says FAILED" has "FAILED"
    ck "$L (c2) prints NOT installed, or NOT verified" has "NOT installed, or NOT verified"
    cin 'ls -A "$HOME/.local/bin" | wc -l'
    ck "$L (c2) nothing in .local/bin" test "$OUT" = 0
    extra "S5(c2) $L: wrong gh tarball: refused, nothing installed"
    tb_end
    # (d) one of gh, mash, mashctl already there
    for x in gh mash mashctl; do
        tb_new tamper-d-$x-$X normal
        cin "mkdir -p \"\$HOME/.local/bin\" && echo mine >\"\$HOME/.local/bin/$x\""
        cin "$(cat "$BF")"
        save tamper-d-$x-$X
        ck "$L (d $x) block fails (rc != 0)" rcn0
        ck "$L (d $x) prints already there: .../$x" has "already there: /home/alice/.local/bin/$x"
        ck "$L (d $x) prints NOT installed" has "NOT installed"
        cin "ls -A \"\$HOME/.local/bin\"; cat \"\$HOME/.local/bin/$x\"; ls -A \"\$HOME/mash-setup\" | wc -l"
        ck "$L (d $x) only that file is there, unchanged, nothing downloaded" \
            test "$OUT" = "$(printf '%s\nmine\n0' "$x")"
        extra "S5(d) $L: $x already present: stops, nothing overwritten"
        tb_end
    done
}
say "S5 tamper tests, the block AS WRITTEN (each in a fresh container)"
tamper_all "as written" "$T/blk/08.sub.sh" "$T/blk/08.sh" w

# ------------------------------------------------- WHAT-MASH-NEEDS.md
say "WHAT-MASH-NEEDS.md: every fenced block run, or listed as not run"
NW=$(find "$T/wblk" -name '*.sh' | grep -c .)
BWHY=""
RC=0
ck "WHAT-MASH-NEEDS.md has 7 fenced blocks (got $NW)" test "$NW" -eq 7
extra "WHAT-MASH-NEEDS.md has 7 fenced blocks"
# index: info string | disposition | pattern the block must contain | reason
wb() { # N INFO DISPOSITION PATTERN REASON
    local nn
    nn=$(printf %02d "$1")
    BWHY=""
    ck "WHAT block $1 info string is '$2'" \
        test "$(cat "$T/wblk/$nn.info" 2>/dev/null)" = "$2"
    ck "WHAT block $1 contains '$4'" grep -qF -- "$4" "$T/wblk/$nn.sh"
    printf '| W%s | `%s` | %s | %s |\n' "$1" "$4" "$3" "$5" >>"$T/wrows.md"
    extra "WHAT block $1 ($3): $4"
}
: >"$T/wrows.md"
wb 1 "" "documented, not run" 'listen: "127.0.0.1:4222"' \
    "sample server.conf text, not a command"
wb 2 sh "RUN (fresh container)" '--port 4333 --start' "create on another port"
wb 3 sh "documented, not run" '--dir "$HOME/srv/mash-nats"' \
    "needs $HOME/srv made first; --dir is the same code path the default run used"
wb 4 sh "documented, not run" '--mash-config "$HOME/mash-conf"' \
    "only changes the closing text; the folder itself is mashctl's (shimmed here)"
wb 5 sh "documented, not run" 'MASH_CONFIG_DIR="$HOME/mash-conf"' \
    "a real adopt writes mashctl state; shimmed mashctl in this harness"
wb 6 "sh person" "person block, never run by the agent path" '--listen 0.0.0.0' \
    "typed confirmation; opens every interface: not run"
wb 7 sh "documented, not run (read-only --dry-run)" 'adopt --dry-run --server' \
    "uses long flags (--system-credentials) the in-tree mashctl does not have yet"
ck "WHAT block 7 is a --dry-run" grep -qF -- '--dry-run' "$T/wblk/07.sh"
RC=0
extra "WHAT-MASH-NEEDS.md: all 7 blocks classified"

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
ck "closing text <= 80 columns (the adopt line cannot wrap)" test \
    "$(grep -v 'adopt --from' "$CL" | awk 'length > 80' | grep -c .)" -eq 0
ck "closing text has no \$HOME" test "$(grep -cF '$HOME' "$CL")" -eq 0
ck "closing text uses /home/alice paths" grep -q '/home/alice/' "$CL"
ck "closing text has one 'mashctl adopt --from' line" test \
    "$(grep -c 'mashctl adopt --from' "$CL")" -eq 1
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

SKILLSHA_AFTER=$(sha256sum "$SKILL" | cut -d' ' -f1)
say "SKILL.md sha256 used for the blocks: $SKILLSHA"
say "SKILL.md sha256 now:                 $SKILLSHA_AFTER"
say "helper sha256 (scripts/nats-local-setup.sh): $HELPERSHA"
say "guide URL commit: $GCOMMIT"

# ------------------------------------------------- report
say "writing the report"
{
    printf '# nats-local guide test report\n\n'
    printf 'RESULT: %s blocks/steps passed, %s failed; %s assertions passed, %s failed.\n\n' \
        "$BPASS" "$BFAIL" "$PASSN" "$FAILN"
    printf 'SKILL.md sha256 (when the blocks ran): %s\n' "$SKILLSHA"
    printf 'SKILL.md sha256 (at the end, after any --update-closing): %s\n' "$SKILLSHA_AFTER"
    printf 'helper scripts/nats-local-setup.sh sha256: %s\n' "$HELPERSHA"
    printf 'guide script URL commit: %s\n\n' "$GCOMMIT"
    printf 'Guide checks: %s sh blocks; %s prose lines over 80 columns; longest sh block line %s columns (not a failure).\n\n' \
        "$NBLK" "$WIDE" "$LONGEST"
    printf '## Per-block results\n\n| n | first 60 chars | method | rc | result |\n|---|---|---|---|---|\n'
    cat "$T/rows.md"
    cat <<'__SUBS__'

## What was not the real thing (SUBSTITUTIONS)

(a) Part-4 block (block 8), gh: the pinned gh v2.105.0 tarball was
    downloaded for real and its sha256 was checked against the hash pinned in
    the guide (a test step). The real v0.5.6 mash assets do not exist, so the
    block cannot verify against real ones. The block that ran is a COPY in
    which the pinned gh sha256 is replaced by the sha256 of a repacked copy
    of that tarball (same layout, bin/gh replaced by a stand-in). The curl
    shim at /usr/local/bin/curl serves that repacked tarball for the gh URL;
    the original, unsubstituted block was also run against it and refuses on
    the hash (tamper test c1).
(b) The gh stand-in is NOT gh. It records argv, checks the flags the guide
    promises (-R, --bundle, --signer-workflow, --source-ref v0.5.6), and
    compares the file's sha256 with the genuine asset's (Verification failed
    on a mismatch). It cannot check a signature. The real gh was run for real
    only on the real v0.5.5 mash-linux-amd64 and its bundle (a test step).
    After block 8 the installed $HOME/.local/bin/gh is the stand-in.
(c) mash/mashctl/mash.intoto.jsonl: curl shim serves local files for
    https://github.com/bogworks/mash/releases/download/v0.5.6/<f> (mash and
    the real mashctl built from this tree; the bundle is a placeholder).
(d) mashctl: the file served as mashctl-linux-amd64 is a HARNESS-ONLY bash
    shim around the real in-tree mashctl, which has no `adopt --from`,
    fingerprint prompt, adopt-then-init or `endpoint create --creds-out`.
    The shim rewrites `adopt --from DIR` into the old long-flag form (reads
    DIR/mash-setup.json; MASH_TLS_CA=DIR/ca.pem; --sys-creds, --admin-creds),
    runs `mashctl init` after a real adopt, and drops `--creds-out` after
    checking it names the default path. Blocks 10 and 11 are therefore NOT
    tested against ctl's real `adopt --from`; neither prompts for a
    fingerprint (the guide says this machine is not asked), so none is fed. In the build copy, adopt.go is patched so `--admin-creds`
    copies the file (0.5.6 behaviour); not committed.
(e) mash: the in-tree mash does not read MASH_ENDPOINT. Blocks 16 and 17 ran
    as COPIES with MASH_ENDPOINT=o.g.n rewritten to MASH_OWNER=o
    MASH_GROUP=g MASH_NAME=n. The guide text itself was checked to contain
    MASH_ENDPOINT and none of MASH_TLS_CA, MASH_OWNER, MASH_GROUP, MASH_NAME.
(f) Block 33 (What next, `endpoint create acme.demo.design`) ran before the
    remove blocks (it needs the server). Block 25 (restore) ran first as
    written (refuses, the directory exists), then after the test stopped
    the server and renamed the directory to mash-nats.moved; block 26
    (--upgrade) then made bin/ itself. The person blocks (19, 29) ran on a
    pty; 29 also ran once BEFORE 19 to check it refuses. Blocks 27 and
    28 (boot) were not run (no systemd).
(g) The image is debian:bookworm-slim plus sudo; user alice (uid 1000,
    NOPASSWD sudo); each block ran as `podman exec --user alice ... env -i
    HOME=/home/alice PATH=/usr/local/bin:/usr/bin:/bin bash -c '<block>'`;
    blocks 19 and 29 ran under `script -qec` (a pty) with the directory
    typed after a 5 second wait. The tamper tests used a second image with
    the prerequisites already installed.
(h) The CA fingerprint in the embedded closing text is masked in the
    comparison with the fresh output (it differs every run).
__SUBS__
    printf '\n## WHAT-MASH-NEEDS.md blocks\n\n| block | contains | status | reason |\n|---|---|---|---|\n'
    cat "$T/wrows.md"
    printf '\n## NOT VERIFIED\n\n'
    cat <<'__NV__'
- Real attestation verify on v0.5.6 (assets do not exist; the release
  workflow does not yet publish mashctl).
- ctl's real `mashctl adopt --from`, the typed fingerprint, adopt running
  init itself, `endpoint create --creds-out`, and mash reading MASH_ENDPOINT:
  all only through the shims above.
- adopt copying ca.pem (0644) into mashctl's folder: the in-tree mashctl
  does not; block 12's five-entry order was checked on a model folder.
- --enable-boot and --disable-boot, a real reboot.
- Any distribution other than Debian 12 (bookworm), and Ubuntu.
__NV__
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
