#!/usr/bin/env bash
# guide-run.sh: a containerised newcomer run of docs/guide/walkthrough.md, and
# the filling of the guide's built-output blocks from what that run recorded.
# See scripts/guide-run/README.md.
#
#   guide-run.sh --make-release-dir DIR [--version V]
#       build a rehearsal release folder from this checkout (static
#       linux/amd64 mash and mashctl, the two scripts, SHA256SUMS)
#   guide-run.sh --release-dir DIR [--version V] [--scratch DIR] [--report FILE]
#       the run: one container, the guide's steps in order, asserts
#   guide-run.sh fill RUN_DIR [--guide FILE]
#       put the run's recorded outputs into the guide's blocks
#   guide-run.sh selftest
#       static checks, no podman
#
# Venue rules (ruled by x-lead): this host only; rootless podman; the official
# debian image only, its digest recorded in the receipt; one container and one
# image per run id, both removed by recorded name and id on exit; a non-root
# user (tester) inside; no ports published; --network none; nothing aimed at
# the LAN or the tailnet; more than 20 GB free before the run. Build time may
# use the distro mirror and the pinned natscli download.
set -u

if [ "${BASH_VERSINFO[0]}" -lt 4 ]; then
    echo "guide-run.sh needs bash 4 or newer on the host" >&2
    exit 2
fi

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P) || exit 2
ROOT=$(cd "$HERE/../.." && pwd -P) || exit 2
SELF=$HERE/$(basename "${BASH_SOURCE[0]}")
GUIDE_DEFAULT=$ROOT/docs/guide/walkthrough.md

# The step ids, in run order; each is a block id in the guide.
STEP_IDS=(install server endpoints status send inbox read json-send json-read
    json-inbox json-jq delete refused lifecycle)
# Ids the guide has that this harness cannot record (a model is needed).
MANUAL_IDS=(claude-code)

BASE_IMAGE=${GUIDE_RUN_BASE:-debian:stable}
NCV=${GUIDE_RUN_NATSCLI:-0.5.2}
# sha256 of the natscli v0.5.2 linux-amd64 zip, the same pin as
# scripts/mash-local-nats-setup_test.sh
NATPIN=9dddf92072bba154c11a08165718fb6ee42af1f25ce4f1bb284d2fc141abcff2
[ "$NCV" = 0.5.2 ] || NATPIN=${GUIDE_RUN_NATSCLI_SHA256:-}

RELEASE_FILES=(SHA256SUMS install.sh mash-local-nats-setup.sh mash-linux-amd64
    mashctl-linux-amd64)
DEFAULT_VERSION=v0.0.0-rehearsal
OWNER=acme
CHOME=/home/tester

die() {
    echo "error: $*" >&2
    exit 2
}

usage() {
    sed -n '2,15p' "$SELF" | sed 's/^# \{0,1\}//'
}

# ----------------------------------------------------------------------
# make-release-dir
# ----------------------------------------------------------------------
make_release_dir() {
    local dir=$1 ver=$2 commit
    command -v go >/dev/null 2>&1 || die "--make-release-dir needs go"
    [ -n "$dir" ] || die "--make-release-dir needs a folder"
    case $ver in v[0-9]*) ;; *) die "--version $ver is not a version such as v0.5.6" ;; esac
    if [ -e "$dir" ] && [ -n "$(ls -A -- "$dir" 2>/dev/null)" ]; then
        die "$dir exists and is not empty"
    fi
    mkdir -p -- "$dir" || die "cannot make $dir"
    commit=$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || echo 0000000000000000000000000000000000000000)
    local ld="-X github.com/bogworks/mash/internal/endpoint.Version=${ver#v} -X main.commit=$commit"
    echo "== building mash and mashctl $ver (linux/amd64, static)"
    (cd "$ROOT" &&
        CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$ld" -o "$dir/mash-linux-amd64" ./cmd/mash &&
        CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$ld" -o "$dir/mashctl-linux-amd64" ./cmd/mashctl) ||
        die "go build failed"
    cp -- "$ROOT/scripts/install.sh" "$ROOT/scripts/mash-local-nats-setup.sh" "$dir/" || die "cannot copy the scripts"
    chmod 755 "$dir/install.sh" "$dir/mash-local-nats-setup.sh" "$dir/mash-linux-amd64" "$dir/mashctl-linux-amd64"
    (cd "$dir" && sha256sum install.sh mash-local-nats-setup.sh mash-linux-amd64 mashctl-linux-amd64 >SHA256SUMS) ||
        die "cannot write SHA256SUMS"
    echo "release folder: $dir ($ver)"
    cat "$dir/SHA256SUMS"
}

# check_release_dir DIR: refuse, naming what is missing or wrong
check_release_dir() {
    local dir=$1 f missing=() want got bad=()
    [ -d "$dir" ] || die "release folder $dir does not exist"
    for f in "${RELEASE_FILES[@]}"; do
        [ -f "$dir/$f" ] || missing+=("$f")
    done
    if [ "${#missing[@]}" -gt 0 ]; then
        echo "error: release folder $dir lacks: ${missing[*]}" >&2
        echo "       it needs: ${RELEASE_FILES[*]}" >&2
        echo "       (guide-run.sh --make-release-dir DIR builds one)" >&2
        exit 2
    fi
    for f in "${RELEASE_FILES[@]}"; do
        [ "$f" = SHA256SUMS ] && continue
        want=$(awk -v n="$f" '$2 == n || $2 == "*" n {print $1; exit}' "$dir/SHA256SUMS")
        got=$(sha256sum -- "$dir/$f" | awk '{print $1}')
        [ -n "$want" ] && [ "$want" = "$got" ] || bad+=("$f")
    done
    if [ "${#bad[@]}" -gt 0 ]; then
        echo "error: $dir/SHA256SUMS has no matching entry for: ${bad[*]}" >&2
        exit 2
    fi
}

# ----------------------------------------------------------------------
# secrets scan: seeds, creds and JWTs must never be in a recorded output
# ----------------------------------------------------------------------
# scan_secrets FILE...: prints each hit (file and kind, never the content)
# and returns 1 on any
scan_secrets() {
    local f hit=0
    for f in "$@"; do
        [ -f "$f" ] || continue
        if grep -Eq 'SU[A-Z2-7]{50}' "$f"; then echo "secret: SU seed shape in $f"; hit=1; fi
        if grep -Eq '(^|[^A-Z0-9])S[AON][A-Z2-7]{50}' "$f"; then echo "secret: SA/SO/SN seed shape in $f"; hit=1; fi
        if grep -Fq -e '-----BEGIN NATS USER JWT' "$f"; then echo "secret: user JWT block in $f"; hit=1; fi
        if grep -Eq 'eyJ[A-Za-z0-9_=-]{8,}' "$f"; then echo "secret: JWT prefix eyJ in $f"; hit=1; fi
        if grep -Fq -e '-----BEGIN NATS USER NKEY SEED' "$f"; then echo "secret: seed block in $f"; hit=1; fi
    done
    return "$hit"
}

# ----------------------------------------------------------------------
# fill
# ----------------------------------------------------------------------
do_fill() {
    local run=$1 guide=$2 id ok=() f
    [ -d "$run/out" ] || die "$run has no out/ folder: not a run folder"
    [ -f "$guide" ] || die "no guide at $guide"
    if [ ! -f "$run/assert.ok" ]; then
        echo "refusing to fill: $run has no assert.ok (the run's asserts did not all pass)" >&2
        return 1
    fi
    # shellcheck disable=SC2046
    if ! scan_secrets "$run"/out/*.txt; then
        echo "refusing to fill: a recorded output holds secret-shaped content" >&2
        return 1
    fi
    for id in "${STEP_IDS[@]}"; do
        f=$run/out/$id.ok
        [ -f "$f" ] && ok+=("$id")
    done
    python3 -I - "$run/out" "$guide" "$CHOME" "${ok[@]+"${ok[@]}"}" <<'PY'
import os, re, sys
out, guide, chome = sys.argv[1:4]
ids = set(sys.argv[4:])
fence = re.compile(r'^```')
mark = re.compile(r'^<!-- output: ([a-z0-9-]+) -->$')
lines = open(guide, encoding='utf-8').read().split('\n')

def body(i):
    t = open(os.path.join(out, i + '.txt'), encoding='utf-8', errors='replace').read()
    t = t.replace('\r', '').replace(chome, '/home/alice')
    ls = [l.rstrip() for l in t.split('\n')]
    while ls and ls[-1] == '':
        ls.pop()
    return ls

res, i, filled, long_lines = [], 0, [], []
while i < len(lines):
    m = mark.match(lines[i])
    if not m or m.group(1) not in ids:
        res.append(lines[i]); i += 1; continue
    bid = m.group(1)
    if i + 1 >= len(lines):
        sys.exit('guide: the mark for %s ends the file' % bid)
    b = body(bid)
    for l in b:
        if len(l) > 80:
            long_lines.append((bid, len(l)))
    nxt = lines[i + 1]
    if fence.match(nxt):
        j = i + 2
        while j < len(lines) and not fence.match(lines[j]):
            j += 1
        if j >= len(lines):
            sys.exit('guide: the block after the mark for %s is not closed' % bid)
        res += [lines[i], nxt] + b + ['```']
        i = j + 1
    elif nxt.startswith('Output:'):
        res += ['Output:', '', lines[i], '```'] + b + ['```']
        i += 2
    else:
        sys.exit('guide: the mark for %s is followed by neither a block nor an Output: line' % bid)
    filled.append(bid)
open(guide, 'w', encoding='utf-8').write('\n'.join(res))
print('filled: ' + (' '.join(filled) or '(none)'))
left = [i for i in ids if i not in filled]
if left:
    print('captured but not in the guide: ' + ' '.join(sorted(left)))
for bid, n in long_lines:
    print('over 80 columns (left as captured): %s has a line of %d' % (bid, n))
PY
}

# ----------------------------------------------------------------------
# selftest: no podman
# ----------------------------------------------------------------------
ST_PASS=0
ST_FAIL=0
st_ok() { ST_PASS=$((ST_PASS + 1)); echo "ok: $*"; }
st_bad() { ST_FAIL=$((ST_FAIL + 1)); echo "FAIL: $*"; }

guide_ids() { # prints the ids in the guide, one per line
    {
        grep -Eo '^<!-- output: [a-z0-9-]+ -->$' "$1" | sed 's/^<!-- output: //; s/ -->$//'
        grep -Eo '\[built output: [a-z0-9-]+\]' "$1" | sed 's/^\[built output: //; s/\]$//'
    } | sort -u
}

selftest() {
    local guide=${1:-$GUIDE_DEFAULT} tmp id want got n a b
    [ -f "$guide" ] || die "no guide at $guide"
    tmp=$(mktemp -d "${TMPDIR:-/tmp}/guide-run-selftest.XXXXXX") || exit 2

    # 1. ids: the guide's and the steps'
    want=$(printf '%s\n' "${STEP_IDS[@]}" "${MANUAL_IDS[@]}" | sort -u)
    got=$(guide_ids "$guide")
    if [ "$want" = "$got" ]; then
        st_ok "the guide's block ids equal the step ids plus the manual ones"
    else
        st_bad "guide ids and step ids differ"
        diff <(echo "$want") <(echo "$got") | sed 's/^/    /'
    fi
    # shellcheck disable=SC2016
    if grep -Eq '^Output:.*`\[built output\]`' "$guide"; then
        st_bad "a placeholder has no id (still [built output])"
    else
        st_ok "every placeholder has an id"
    fi
    # each mark is followed by a block or an Output: line
    n=$(awk '/^<!-- output: [a-z0-9-]+ -->$/ {m=1; next} m==1 {if ($0 !~ /^```/ && $0 !~ /^Output:/) bad++; m=0} END {print bad+0}' "$guide")
    if [ "$n" = 0 ]; then st_ok "every mark is followed by a block or an Output: line"; else st_bad "$n marks are followed by something else"; fi
    n=$(guide_ids "$guide" | wc -l)
    [ "$n" -gt 0 ] || st_bad "no ids found in the guide"

    # 2. fill on a fixture run folder
    local run=$tmp/run guidecopy=$tmp/guide.md
    mkdir -p "$run/out"
    cp -- "$guide" "$guidecopy"
    for id in "${STEP_IDS[@]}"; do
        printf 'fixture output of %s\n  under %s/.config/mash\nsecond line\n' "$id" "$CHOME" >"$run/out/$id.txt"
        echo "\$ fixture $id" >"$run/out/$id.cmd"
        echo 0 >"$run/out/$id.rc"
        : >"$run/out/$id.ok"
    done
    a=$(awk 'length($0) > 80' "$guidecopy" | wc -l)
    if do_fill "$run" "$guidecopy" >"$tmp/fill1.log" 2>&1; then
        st_bad "fill ran without assert.ok"
    else
        st_ok "fill refuses a run without assert.ok"
    fi
    : >"$run/assert.ok"
    if do_fill "$run" "$guidecopy" >"$tmp/fill1.log" 2>&1; then
        st_ok "fill ran on the fixture run folder"
    else
        st_bad "fill failed on the fixture"; sed 's/^/    /' "$tmp/fill1.log"
    fi
    b=$(awk 'length($0) > 80' "$guidecopy" | wc -l)
    if [ "$a" = "$b" ]; then st_ok "fill made no line over 80 columns"; else st_bad "fill made $((b - a)) lines over 80 columns"; fi
    n=0
    for id in "${STEP_IDS[@]}"; do
        grep -Fxq "fixture output of $id" "$guidecopy" || { st_bad "block $id was not filled"; n=1; }
    done
    [ "$n" = 0 ] && st_ok "every step id's block holds its recorded output"
    if grep -Fq "$CHOME" "$guidecopy"; then st_bad "the temp home path was not normalised"; else st_ok "the temp home path became /home/alice"; fi
    # shellcheck disable=SC2016
    if grep -Fq '`[built output: ' "$guidecopy"; then
        # only the manual ones may stay
        n=$(grep -Eo '\[built output: [a-z0-9-]+\]' "$guidecopy" | grep -Evc "\[built output: ($(IFS='|'; echo "${MANUAL_IDS[*]}"))\]")
        if [ "$n" = 0 ]; then st_ok "only the manual placeholders are left"; else st_bad "$n placeholders were not replaced"; fi
    else
        st_ok "no placeholder is left"
    fi
    cp -- "$guidecopy" "$tmp/after1.md"
    do_fill "$run" "$guidecopy" >/dev/null 2>&1
    if cmp -s "$tmp/after1.md" "$guidecopy"; then st_ok "a second fill changes nothing"; else st_bad "a second fill changed the guide"; fi

    # an uncaptured step leaves its block unchanged
    cp -- "$guide" "$tmp/g2.md"
    rm -f "$run/out/status.ok"
    do_fill "$run" "$tmp/g2.md" >/dev/null 2>&1
    if grep -Fxq "fixture output of status" "$tmp/g2.md"; then st_bad "a step without .ok was filled"; else st_ok "a step without .ok is left unfilled"; fi

    # a secret-shaped output stops the fill
    printf 'x %s\n' "eyJhbGciOiJlZDI1NTE5LW5rZXkifQ" >"$run/out/send.txt"
    if do_fill "$run" "$tmp/g2.md" >/dev/null 2>&1; then st_bad "fill ran with a JWT-shaped output"; else st_ok "fill refuses a JWT-shaped output"; fi
    printf 'SU%s\n' "$(printf 'A%.0s' $(seq 1 50))" >"$run/out/send.txt"
    if do_fill "$run" "$tmp/g2.md" >/dev/null 2>&1; then st_bad "fill ran with a seed-shaped output"; else st_ok "fill refuses a seed-shaped output"; fi

    case $tmp in "${TMPDIR:-/tmp}"/guide-run-selftest.*) rm -rf -- "$tmp" ;; esac
    echo "selftest: ok=$ST_PASS FAIL=$ST_FAIL"
    [ "$ST_FAIL" = 0 ]
}

# ----------------------------------------------------------------------
# the run
# ----------------------------------------------------------------------
PASS=0
FAIL=0
ok() { PASS=$((PASS + 1)); echo "ok: $*"; RESULTS+=("- ok: $*"); }
bad() { FAIL=$((FAIL + 1)); echo "FAIL: $*"; RESULTS+=("- FAIL: $*"); }

RELDIR=''
VERSION=$DEFAULT_VERSION
SCRATCH=''
REPORT=''
RUN=''
RUN_DIR=''
OUT=''
CTX=''
CNAME=''
IMG=''
IMG_ID=''
CONTAINER_MADE=0
CLEANED='not run'
RESULTS=()
STEPLINES=()
IMGLINES=''
NATSHA=''
DISK0=''
STEP=''
STEP_BAD=0
LAST=''
USE_LOCALBIN=0
START=0
ABORT=0

free_gb() { df -P -k "$1" | awk 'NR==2 {printf "%d", $4 / 1048576}'; }
free_mb() { df -P -k "$1" | awk 'NR==2 {printf "%d", $4 / 1024}'; }

write_receipt() {
    local elapsed=$((SECONDS - START)) l
    {
        echo "# guide-run receipt"
        echo
        echo "- run id: $RUN"
        echo "- date (UTC): $(date -u +%Y-%m-%dT%H:%M:%SZ)"
        echo "- harness: scripts/guide-run/guide-run.sh, sha256 $(sha256sum "$SELF" | awk '{print $1}')"
        echo "- guide: docs/guide/walkthrough.md, sha256 $(sha256sum "$GUIDE_DEFAULT" | awk '{print $1}')"
        echo "- mode: rehearsal (a local release folder, file:// base); not the published release"
        echo "- version: $VERSION"
        echo "- release folder files (SHA256SUMS):"
        if [ -f "${RELDIR:-/nonexistent}/SHA256SUMS" ]; then sed 's/^/    /' "$RELDIR/SHA256SUMS"; fi
        echo "- natscli v$NCV linux-amd64 zip, sha256 $NATSHA (pinned: $NATPIN)"
        echo "- podman: $(podman --version 2>/dev/null)"
        echo "- wall time: ${elapsed} s"
        echo "- run folder: $RUN_DIR"
        echo
        echo "## Image"
        echo
        printf '%s\n' "$IMGLINES"
        echo
        echo "## Container"
        echo
        echo "- name $CNAME; user tester; --network none; no ports published; no-new-privileges; cap-drop all"
        echo
        echo "## Steps"
        echo
        printf '%s\n' "${STEPLINES[@]+"${STEPLINES[@]}"}"
        echo
        echo "## Totals"
        echo
        echo "- ok: $PASS"
        echo "- FAIL: $FAIL"
        echo "- host free disk before: ${DISK0:-?} MB, after: $(free_mb "${SCRATCH:-/tmp}") MB"
        echo "- cleanup: $CLEANED"
        echo
        echo "## Asserts and steps"
        echo
        for l in "${RESULTS[@]+"${RESULTS[@]}"}"; do printf '%s\n' "$l"; done
        echo
        echo "## What this does not prove"
        echo
        echo "- The Claude Code steps (6): no model here. Not filled."
        echo "- A real macOS machine."
        echo "- The published release: the binaries are built from this checkout and installed from a local folder."
    } >"$REPORT" 2>/dev/null
}

cleanup() {
    local left=0
    trap - EXIT INT TERM
    if [ "$CONTAINER_MADE" = 1 ]; then podman rm -f "$CNAME" >/dev/null 2>&1; fi
    if [ -n "$IMG_ID" ]; then podman rmi -f "$IMG_ID" >/dev/null 2>&1; fi
    case $CTX in "$SCRATCH"/*/build) rm -rf -- "$CTX" ;; esac
    if [ "$CONTAINER_MADE" = 1 ] && podman container exists "$CNAME" 2>/dev/null; then left=$((left + 1)); fi
    if [ -n "$IMG_ID" ] && podman image exists "$IMG_ID" 2>/dev/null; then left=$((left + 1)); fi
    if [ "$left" = 0 ]; then
        CLEANED="done: container $CNAME removed by name, image ${IMG_ID:-none} by id, build dir removed; none left"
    else
        CLEANED="INCOMPLETE: $left of this run's objects still exist"
    fi
    [ -n "$REPORT" ] && write_receipt
    echo "cleanup: $CLEANED"
    [ -n "$REPORT" ] && echo "receipt: $REPORT"
    echo "run folder: $RUN_DIR"
}

# cx CWD CMD: a shell line in the container as tester (bash -c)
cx() {
    local path=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin
    [ "$USE_LOCALBIN" = 1 ] && path=$CHOME/.local/bin:$path
    # shellcheck disable=SC2086
    timeout 300 podman exec ${EXEC_EXTRA:-} --user tester -w "$1" -e "HOME=$CHOME" -e "PATH=$path" "$CNAME" bash -o pipefail -c "$2"
}

step_begin() {
    STEP=$1
    STEP_BAD=0
    : >"$OUT/$1.txt"
    : >"$OUT/$1.cmd"
    : >"$OUT/$1.rc"
    rm -f "$OUT/$1.ok"
    echo "== step $1"
}

# step_cmd EXPECT CWD CMD: EXPECT is zero or nonzero
step_cmd() {
    local expect=$1 cwd=$2 cmd=$3 rc
    printf '$ %s\n' "$cmd" >>"$OUT/$STEP.cmd"
    LAST=$RUN_DIR/last.txt
    cx "$cwd" "$cmd" >"$LAST" 2>&1 </dev/null
    rc=$?
    tr -d '\r' <"$LAST" >>"$OUT/$STEP.txt"
    echo "$rc" >>"$OUT/$STEP.rc"
    if [ "$expect" = zero ] && [ "$rc" != 0 ]; then
        STEP_BAD=1
    elif [ "$expect" = nonzero ] && [ "$rc" = 0 ]; then
        STEP_BAD=1
    fi
    return "$rc"
}

step_end() {
    local rcs
    rcs=$(tr '\n' ' ' <"$OUT/$STEP.rc")
    if [ "$STEP_BAD" = 0 ]; then
        : >"$OUT/$STEP.ok"
        ok "step $STEP (exit status: ${rcs% })"
        STEPLINES+=("- $STEP: ok; exit status ${rcs% }; out/$STEP.txt")
    else
        bad "step $STEP (exit status: ${rcs% }); out/$STEP.txt"
        STEPLINES+=("- $STEP: FAILED; exit status ${rcs% }; out/$STEP.txt")
        echo "  --- first 40 lines of the output of $STEP"
        head -n 40 "$OUT/$STEP.txt" | sed 's/^/  | /'
    fi
}

# wait_inbox TEXT: at most 30 tries, one second apart, until alice's inbox
# lists a message with TEXT (a word of what was sent)
wait_inbox() {
    local i=0
    while [ "$i" -lt 30 ]; do
        if cx "$CHOME" "MASH_ENDPOINT=$OWNER.demo.alice mash inbox | grep -Fq -- '$1'" >/dev/null 2>&1 </dev/null; then
            return 0
        fi
        sleep 1
        i=$((i + 1))
    done
    return 1
}

REL=$CHOME/release

step_install() {
    step_begin install
    step_cmd zero "$REL" "grep ' install.sh\$' SHA256SUMS | sha256sum -c -"
    step_cmd zero "$REL" "MASH_VERSION=$VERSION MASH_RELEASE_BASE=file://$REL MASH_INSTALL_ALLOW_FILE=1 bash install.sh"
    USE_LOCALBIN=1
    step_cmd zero "$CHOME" "mash version"
    step_cmd zero "$CHOME" "mashctl version"
    step_end
}

step_server() {
    local line
    step_begin server
    step_cmd zero "$REL" "grep ' mash-local-nats-setup.sh\$' SHA256SUMS | sha256sum -c -"
    step_cmd zero "$REL" "bash mash-local-nats-setup.sh --owner $OWNER"
    # the line the script printed last, taken from its output, never typed
    line=$(grep -v '^[[:space:]]*$' "$LAST" | tail -n 1)
    case $line in
    "mashctl init --config /"*mash-setup.yaml) ;;
    *)
        STEP_BAD=1
        echo "the setup script's last line is not a mashctl init line: $line" >>"$OUT/$STEP.txt"
        step_end
        return 1
        ;;
    esac
    SETUP_DIR=$(dirname "${line#mashctl init --config }")
    step_cmd zero "$CHOME" "$line"
    step_end
}

step_endpoints() {
    step_begin endpoints
    step_cmd zero "$CHOME" "mashctl endpoint create $OWNER.demo.bob"
    step_cmd zero "$CHOME" "mashctl endpoint create $OWNER.demo.alice"
    step_end
}

step_status() {
    step_begin status
    step_cmd zero "$CHOME" "MASH_ENDPOINT=$OWNER.demo.bob mash status"
    step_cmd zero "$CHOME" "MASH_ENDPOINT=$OWNER.demo.alice mash status"
    step_end
}

step_send() {
    step_begin send
    step_cmd zero "$CHOME" "MASH_ENDPOINT=$OWNER.demo.bob mash send $OWNER.demo.alice \"lunch at noon?\""
    step_cmd zero "$CHOME" "MASH_ENDPOINT=$OWNER.demo.bob mash send $OWNER.demo.alice --urgent \\
  --kind question \"ready?\""
    step_end
    wait_inbox "ready?" || bad "the two messages did not arrive in alice's inbox within 30 s"
}

step_inbox() {
    step_begin inbox
    step_cmd zero "$CHOME" "MASH_ENDPOINT=$OWNER.demo.alice mash inbox"
    step_end
}

step_read() {
    step_begin read
    step_cmd zero "$CHOME" "MASH_ENDPOINT=$OWNER.demo.alice mash read 2"
    step_end
}

step_json_send() {
    step_begin json-send
    step_cmd zero "$CHOME" "MASH_ENDPOINT=$OWNER.demo.bob mash send $OWNER.demo.alice --json \"ping\""
    step_end
    wait_inbox "ping" || bad "the third message did not arrive in alice's inbox within 30 s"
}

step_json_read() {
    step_begin json-read
    step_cmd zero "$CHOME" "MASH_ENDPOINT=$OWNER.demo.alice mash read 3 --json"
    step_end
}

step_json_inbox() {
    step_begin json-inbox
    step_cmd zero "$CHOME" "MASH_ENDPOINT=$OWNER.demo.alice mash inbox --json"
    step_end
}

step_json_jq() {
    step_begin json-jq
    step_cmd zero "$CHOME" "MASH_ENDPOINT=$OWNER.demo.alice mash read 3 --json | jq -r .body"
    step_end
}

step_delete() {
    step_begin delete
    # the spec (spec/mashctl.json, endpoint delete) offers --yes in place of
    # typing the address on a terminal
    step_cmd zero "$CHOME" "mashctl endpoint delete $OWNER.demo.bob --yes"
    step_end
}

step_refused() {
    step_begin refused
    step_cmd nonzero "$CHOME" "MASH_ENDPOINT=$OWNER.demo.bob mash status"
    step_end
}

step_lifecycle() {
    step_begin lifecycle
    step_cmd zero "$REL" "bash mash-local-nats-setup.sh --stop"
    step_cmd zero "$REL" "bash mash-local-nats-setup.sh --start"
    # --remove needs a terminal: podman exec -t, and the folder typed at the
    # prompt through a pty helper
    EXEC_EXTRA=-t step_cmd zero "$REL" "python3 /usr/local/lib/guide-run-ptyhelper.py '$SETUP_DIR' bash mash-local-nats-setup.sh --remove"
    step_end
}

# asserts: hard facts, from the recorded outputs
run_asserts() {
    local f n
    echo "== asserts"
    f=$OUT/read.txt
    # the guide's form is a "trust: verified" line; the 0.5.x form is "[verified]" in the header
    if [ -f "$f" ] && grep -Eq '^trust: +verified$|^--- #2 from [^ ]+ \[verified\]' "$f"; then ok "mail 2 read as alice: trust verified"; else bad "mail 2 read as alice: not shown as trust verified"; fi
    f=$OUT/json-read.txt
    if [ -f "$f" ] && grep -Eq '"trust": "verified"' "$f"; then ok "mail 3 read as JSON: trust verified"; else bad "mail 3 read as JSON: no verified trust"; fi
    f=$OUT/inbox.txt
    n=0
    [ -f "$f" ] && n=$(grep -c 'verified' "$f")
    if [ "$n" -ge 2 ]; then ok "the inbox lists both mails as verified ($n lines)"; else bad "the inbox lists $n verified lines, wanted 2"; fi
    n=0
    [ -f "$f" ] && n=$(grep -c 'bob' "$f")
    if [ "$n" -ge 2 ]; then ok "the inbox lists two mails from bob"; else bad "the inbox lists $n mails from bob, wanted 2"; fi
    f=$OUT/refused.rc
    if [ -f "$f" ] && [ "$(tail -n 1 "$f")" != 0 ] && [ -n "$(cat "$f")" ]; then
        ok "the call after delete exited non-zero ($(tail -n 1 "$f"))"
    else
        bad "the call after delete did not exit non-zero"
    fi
    f=$OUT/install.txt
    if [ -f "$f" ] && grep -Eq "^mash v?${VERSION#v}( |\$)" "$f" && grep -Eq "^mashctl v?${VERSION#v}( |\$)" "$f"; then
        ok "mash version and mashctl version both say ${VERSION#v}"
    else
        bad "mash version and mashctl version do not both say ${VERSION#v}"
    fi
    # no seed or credential content in any captured output
    if scan_secrets "$OUT"/*.txt; then ok "no seed, credential or JWT content in any captured output"; else bad "secret-shaped content in a captured output (not filling)"; fi
}

do_run() {
    local t g rd n f
    for t in podman curl sha256sum df awk timeout python3; do
        command -v "$t" >/dev/null 2>&1 || die "needs $t"
    done
    [ "$(id -u)" -ne 0 ] || die "run as an ordinary user (rootless podman)"
    [ -n "$RELDIR" ] || die "needs --release-dir DIR (or --make-release-dir DIR to build one)"
    [ -n "$NATPIN" ] || die "natscli v$NCV has no pinned sha256; set GUIDE_RUN_NATSCLI_SHA256"
    RELDIR=$(cd "$RELDIR" 2>/dev/null && pwd -P) || die "release folder does not exist"
    check_release_dir "$RELDIR"

    RUN=$(date +%m%d%H%M%S)-$$
    if [ -z "$SCRATCH" ]; then
        SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/guide-run.XXXXXX") || exit 2
    fi
    mkdir -p "$SCRATCH" || exit 2
    SCRATCH=$(cd "$SCRATCH" && pwd -P)
    RUN_DIR=$SCRATCH/run-$RUN
    OUT=$RUN_DIR/out
    CTX=$RUN_DIR/build
    CNAME=guide-run-$RUN
    IMG=localhost/guide-run-$RUN
    [ -n "$REPORT" ] || REPORT=$RUN_DIR/receipt.md
    START=$SECONDS
    mkdir -p "$OUT" "$CTX" || exit 2
    trap cleanup EXIT
    trap 'exit 130' INT TERM

    # venue checks
    g=$(podman info --format '{{.Store.GraphRoot}}' 2>/dev/null)
    [ -n "$g" ] || die "cannot read the podman graph root"
    for rd in "$g" "$SCRATCH"; do
        n=$(free_gb "$rd")
        if [ "${n:-0}" -le 20 ]; then
            echo "refusing: only ${n:-?} GB free under $rd (need more than 20 GB)" >&2
            exit 2
        fi
    done
    DISK0=$(free_mb "$SCRATCH")

    echo "== run $RUN: release $RELDIR ($VERSION); run folder $RUN_DIR"
    echo "== downloading natscli v$NCV"
    curl -fsSL -o "$CTX/nats.zip" \
        "https://github.com/nats-io/natscli/releases/download/v$NCV/nats-$NCV-linux-amd64.zip" ||
        die "cannot download natscli v$NCV"
    NATSHA=$(sha256sum "$CTX/nats.zip" | awk '{print $1}')
    if [ "$NATSHA" != "$NATPIN" ]; then
        echo "FAIL: natscli v$NCV zip sha256 is $NATSHA, pinned $NATPIN" >&2
        exit 2
    fi
    echo "natscli sha256 matches the pin"

    cat >"$CTX/ptyhelper.py" <<'PYEOF'
# ptyhelper.py RESP CMD...: run CMD on a pseudo-terminal. When the output ends
# with a prompt ("type the folder ...: ") send RESP and a newline. Prints the
# terminal output and exits with the real exit status of CMD.
import os, pty, select, signal, sys, time
resp, cmd = sys.argv[1], sys.argv[2:]
pid, fd = pty.fork()
if pid == 0:
    os.execvp(cmd[0], cmd)
out = b''
sent = False
end = time.time() + 120
timed_out = True
while time.time() < end:
    r, _, _ = select.select([fd], [], [], 5)
    if not r:
        continue
    try:
        d = os.read(fd, 4096)
    except OSError:
        timed_out = False
        break
    if not d:
        timed_out = False
        break
    out += d
    if not sent and out.endswith(b': ') and b'type the folder' in out.lower():
        sent = True
        os.write(fd, resp.encode() + b'\n')
if timed_out:
    os.kill(pid, signal.SIGKILL)
    out += b'\n[ptyhelper.py: timeout]\n'
_, st = os.waitpid(pid, 0)
sys.stdout.write(out.decode(errors='replace').replace('\r', ''))
sys.exit(124 if timed_out else os.waitstatus_to_exitcode(st))
PYEOF
    cat >"$CTX/Containerfile" <<'CF'
ARG BASE
FROM ${BASE}
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    nats-server openssl iproute2 procps curl unzip ca-certificates util-linux python3 jq \
    && rm -rf /var/lib/apt/lists/*
COPY nats.zip /tmp/nats.zip
RUN unzip -j -o /tmp/nats.zip '*/nats' -d /usr/local/bin && chmod 755 /usr/local/bin/nats && rm /tmp/nats.zip
COPY ptyhelper.py /usr/local/lib/guide-run-ptyhelper.py
RUN chmod 644 /usr/local/lib/guide-run-ptyhelper.py && useradd -m -s /bin/bash tester
CF
    echo "== building $IMG from $BASE_IMAGE"
    if ! podman build -q --pull=missing --build-arg "BASE=$BASE_IMAGE" -t "$IMG" -f "$CTX/Containerfile" "$CTX" \
        >"$RUN_DIR/build-image.log" 2>&1; then
        echo "FAIL: the image build failed; see $RUN_DIR/build-image.log"
        FAIL=$((FAIL + 1))
        RESULTS+=("- FAIL: the image build failed")
        exit 1
    fi
    IMG_ID=$(podman image inspect --format '{{.Id}}' "$IMG")
    local bdig brd
    bdig=$(podman image inspect --format '{{.Digest}}' "$BASE_IMAGE" 2>/dev/null)
    brd=$(podman image inspect --format '{{index .RepoDigests 0}}' "$BASE_IMAGE" 2>/dev/null)
    IMGLINES="- $BASE_IMAGE: digest ${bdig:-?} (${brd:-no repo digest}); built image $IMG id $IMG_ID"
    echo "$IMGLINES"

    # stage the release folder world-readable, then copy it in
    mkdir -p "$CTX/release"
    for f in "${RELEASE_FILES[@]}"; do cp -- "$RELDIR/$f" "$CTX/release/$f"; done
    chmod 755 "$CTX/release" "$CTX/release"/*

    echo "== starting $CNAME"
    CONTAINER_MADE=1
    podman run -d --name "$CNAME" --user tester --network none \
        --security-opt no-new-privileges --cap-drop=all --pids-limit 2048 \
        -e "HOME=$CHOME" "$IMG" sleep 7200 >/dev/null || {
        bad "the container did not start"
        exit 1
    }
    podman cp "$CTX/release" "$CNAME:$REL" || {
        bad "podman cp of the release folder failed"
        exit 1
    }
    cx "$CHOME" "ls -l release" >"$RUN_DIR/release-listing.txt" 2>&1

    SETUP_DIR=''
    step_install
    [ -f "$OUT/install.ok" ] || ABORT=1
    if [ "$ABORT" = 0 ]; then
        step_server
        [ -f "$OUT/server.ok" ] || ABORT=1
    fi
    if [ "$ABORT" = 0 ]; then
        step_endpoints
        [ -f "$OUT/endpoints.ok" ] || ABORT=1
    fi
    if [ "$ABORT" = 0 ]; then
        step_status
        step_send
        step_inbox
        step_read
        step_json_send
        step_json_read
        step_json_inbox
        step_json_jq
        step_delete
        step_refused
        step_lifecycle
    else
        bad "the run stopped after a step that everything else needs"
    fi
    run_asserts
    echo "== totals: ok=$PASS FAIL=$FAIL ($((SECONDS - START)) s)"
    if [ "$FAIL" = 0 ] && [ "$PASS" -gt 0 ]; then
        : >"$RUN_DIR/assert.ok"
        return 0
    fi
    return 1
}

# ----------------------------------------------------------------------
# main
# ----------------------------------------------------------------------
MAKE_DIR=''
SUB=''
FILL_RUN=''
FILL_GUIDE=$GUIDE_DEFAULT
while [ $# -gt 0 ]; do
    case $1 in
    --release-dir) RELDIR=${2:?--release-dir needs a folder}; shift 2 ;;
    --make-release-dir) MAKE_DIR=${2:?--make-release-dir needs a folder}; shift 2 ;;
    --version) VERSION=${2:?--version needs a value}; shift 2 ;;
    --scratch) SCRATCH=${2:?--scratch needs a folder}; shift 2 ;;
    --report) REPORT=${2:?--report needs a file}; shift 2 ;;
    --guide) FILL_GUIDE=${2:?--guide needs a file}; shift 2 ;;
    fill) SUB=fill; FILL_RUN=${2:?fill needs RUN_DIR}; shift 2 ;;
    selftest) SUB=selftest; shift ;;
    -h | --help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
    esac
done

case $SUB in
fill) do_fill "$FILL_RUN" "$FILL_GUIDE"; exit $? ;;
selftest) selftest "$FILL_GUIDE"; exit $? ;;
esac
if [ -n "$MAKE_DIR" ]; then
    make_release_dir "$MAKE_DIR" "$VERSION"
    exit $?
fi
do_run
