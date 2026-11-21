#!/usr/bin/env bash
# guide-run.sh: a containerised newcomer run of docs/guide/walkthrough.md, and
# the filling of the guide's built-output blocks from what that run recorded.
# See scripts/guide-run/README.md.
#
#   guide-run.sh --make-release-dir DIR [--version V]
#       build a rehearsal release folder from this checkout (static
#       linux/amd64 mash and mashctl, the two scripts, SHA256SUMS)
#   guide-run.sh --release-dir DIR [--version V] [--scratch DIR] [--report FILE]
#       the rehearsal run: one container, the guide's steps in order, asserts
#   guide-run.sh --published --version V [--only-install] [--scratch DIR] [--report FILE]
#       the published run: the container downloads the release from GitHub
#       through a host-side allowlist proxy (egressproxy), nothing else
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
# use the distro mirror and the pinned natscli download. Published mode adds
# one thing: a UNIX socket to a host-side proxy that allows github.com and
# objects.githubusercontent.com and release-assets.githubusercontent.com on port
# 443 and nothing else.
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
STEP_IDS=(install server endpoints status who send inbox read json-send json-read
    json-inbox json-jq delete refused lifecycle)
# Ids the guide has that this harness cannot record (a model is needed).
MANUAL_IDS=(claude-code)
# Steps this harness records that are not in the guide and are never filled.
STRUCT_IDS=(egress claude-code-structure)

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
GH_REPO=bogworks/mash
GH_BASE=https://github.com/$GH_REPO/releases/download
ALLOWLIST='github.com:443 objects.githubusercontent.com:443 release-assets.githubusercontent.com:443'
PROXY_URL=http://127.0.0.1:3128
EGRESS_CTR_DIR=/run/egress

die() {
    echo "error: $*" >&2
    exit 2
}

usage() {
    sed -n '2,18p' "$SELF" | sed 's/^# \{0,1\}//'
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
    dir=$(cd -- "$dir" && pwd) || die "cannot resolve $dir"
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
# and returns 1 on any. It also returns 1 when it scanned no file at all: a
# scan of nothing is not a pass.
scan_secrets() {
    local f hit=0 n=0
    for f in "$@"; do
        [ -f "$f" ] || continue
        n=$((n + 1))
        if grep -Eq 'SU[A-Z2-7]{50}' "$f"; then echo "secret: SU seed shape in $f"; hit=1; fi
        if grep -Eq '(^|[^A-Z0-9])S[AON][A-Z2-7]{50}' "$f"; then echo "secret: SA/SO/SN seed shape in $f"; hit=1; fi
        if grep -Fq -e '-----BEGIN NATS USER JWT' "$f"; then echo "secret: user JWT block in $f"; hit=1; fi
        if grep -Eq 'eyJ[A-Za-z0-9_=-]{8,}' "$f"; then echo "secret: JWT prefix eyJ in $f"; hit=1; fi
        if grep -Fq -e '-----BEGIN NATS USER NKEY SEED' "$f"; then echo "secret: seed block in $f"; hit=1; fi
    done
    if [ "$n" = 0 ]; then echo "secret: no file was scanned"; hit=1; fi
    return "$hit"
}

# run_files RUN: prints every file a run keeps in its run folder that could
# hold container output (the container's own seeds are never copied out): the
# recorded outputs and command lines, the last output, the image build log,
# the release listing, and the proxy's log and stderr (published mode). Only
# files that exist are printed.
run_files() {
    local run=$1 f
    for f in "$run"/out/*.txt "$run"/out/*.cmd "$run/last.txt" "$run/build-image.log" \
        "$run/release-listing.txt" "$run/egress.log" "$run/egressproxy.stderr" "$run/egressproxy-build.log"; do
        [ -f "$f" ] && printf '%s\n' "$f"
    done
}

# scan_run RUN: scan_secrets over run_files
scan_run() {
    local files=() f
    while IFS= read -r f; do files+=("$f"); done < <(run_files "$1")
    scan_secrets "${files[@]+"${files[@]}"}"
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
    if ! scan_run "$run"; then
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

# Steps whose commands are not compared with a command block of the guide,
# each with the reason. A step not named here must have a block in the guide
# and its commands must equal the harness's (after joining line continuations
# and squeezing blanks). A step named here that the guide now shows in a block
# fails the selftest, so the list cannot go stale.
#   install    the guide gives the command inline (`install.sh` from the
#              release); the harness runs it from a file:// release folder
#              with MASH_RELEASE_BASE and MASH_INSTALL_ALLOW_FILE (or, in
#              published mode, curl through the proxy)
#   server     inline in the guide; the harness adds the checksum check and
#              takes the final `mashctl init` line from the script's output
#   delete     inline in the guide; the harness types the address at the
#              prompt through a pty helper
#   refused    prose in the guide ("Then `mash status` as Bob"); same command
#              as step status for bob, run once more
#   lifecycle  inline in the guide (--stop, --start, --remove); --remove is
#              typed at its prompt through the pty helper
NO_BLOCK_STEPS=(install server delete refused lifecycle)
# guide id=harness steps whose commands make up the guide's block (the guide
# shows inbox and read in one block)
CMD_MAP=(endpoints=endpoints status=status who=who send=send inbox=inbox+read
    json-send=json-send json-read=json-read json-inbox=json-inbox json-jq=json-jq)

# selftest_commands GUIDE TMP: record the commands each step function would
# run (step_cmd replaced, no container), then compare with the guide's blocks
selftest_commands() {
    local guide=$1 tmp=$2 d=$2/dry line
    mkdir -p "$d"
    (
        OUT=$d MODE=rehearsal SETUP_DIR=/x
        LAST=$d/last.txt
        echo 'mashctl init --config /x/mash-setup.yaml' >"$LAST"
        step_begin() { STEP=$1; : >"$d/$1"; }
        step_cmd() { printf '%s\n@@END@@\n' "$3" >>"$d/$STEP"; }
        step_end() { :; }
        wait_inbox() { return 0; }
        bad() { :; }
        step_install; step_server; step_endpoints; step_status; step_who; step_send
        step_inbox; step_read; step_json_send; step_json_read; step_json_inbox
        step_json_jq; step_delete; step_refused; step_lifecycle
    ) >/dev/null 2>&1
    python3 -I - "$guide" "$d" "$(IFS=,; echo "${NO_BLOCK_STEPS[*]}")" "${CMD_MAP[@]}" <<'PY' >"$tmp/cmpres.txt"
import os, re, sys
guide, d, allow = sys.argv[1], sys.argv[2], [a for a in sys.argv[3].split(',') if a]
specs = [a.split('=', 1) for a in sys.argv[4:]]
lines = open(guide, encoding='utf-8').read().split('\n')
mark = re.compile(r'^<!-- output: ([a-z0-9-]+) -->$')
built = re.compile(r'\[built output: ([a-z0-9-]+)\]')
blocks, pending, i = {}, [], 0
while i < len(lines):
    l = lines[i]
    if l.startswith('```'):
        j = i + 1
        while j < len(lines) and not lines[j].startswith('```'):
            j += 1
        # the block right after a mark is the step's output, not a command
        if not (i > 0 and mark.match(lines[i - 1])):
            pending.append('\n'.join(lines[i + 1:j]))
        i = j + 1
        continue
    m = mark.match(l)
    if m:
        blocks[m.group(1)] = pending
        pending = []
    elif l.startswith('#'):
        pending = []
    else:
        for b in built.findall(l):
            if b not in blocks:
                blocks[b] = pending
                pending = []
    i += 1

def norm(text):
    text = re.sub(r'\\\n[ \t]*', ' ', text)
    return [re.sub(r'[ \t]+', ' ', c).strip() for c in text.split('\n') if c.strip()]

def harness(step):
    t = open(os.path.join(d, step), encoding='utf-8').read()
    return [c for rec in t.split('@@END@@\n') if rec.strip() for c in norm(rec)]

for gid, steps in specs:
    want = [c for b in blocks.get(gid, []) for c in norm(b)]
    got = [c for st in steps.split('+') for c in harness(st)]
    if want == got:
        print('ok: the commands of step %s equal the guide\'s block (%d)' % (steps, len(got)))
    else:
        print('FAIL: step %s commands differ from the guide block %s' % (steps, gid))
        print('    harness: ' + ' || '.join(got))
        print('    guide:   ' + ' || '.join(want))
for gid in allow:
    if any(norm(b) for b in blocks.get(gid, [])):
        print('FAIL: step %s is on the no-block allow-list but the guide now has a command block for it' % gid)
    else:
        print('ok: step %s is allow-listed (no command block in the guide, see NO_BLOCK_STEPS)' % gid)
PY
    while IFS= read -r line; do
        case $line in
        ok:*) st_ok "${line#ok: }" ;;
        FAIL:*) st_bad "${line#FAIL: }" ;;
        *) echo "$line" ;;
        esac
    done <"$tmp/cmpres.txt"
    [ -s "$tmp/cmpres.txt" ] || st_bad "the command comparison printed nothing"
}

# selftest_asserts TMP: run_content_asserts and scan_run on fixture outputs
# shaped like a real run's (a passing set, then one defect at a time)
selftest_asserts() {
    local d=$1/ca id fails
    mkdir -p "$d"
    cat >"$d/status.txt" <<'EOF'
acme.demo.bob: ready, offline
acme.demo.alice: ready, offline
EOF
    cat >"$d/who.txt" <<'EOF'
ADDRESS          STATE
acme.demo.alice  offline
acme.demo.bob    offline
EOF
    cat >"$d/inbox.txt" <<'EOF'
2 messages for acme.demo.alice, 2 new, 1 urgent.

NUMBER  FROM  WHEN      STATE  URGENT  KIND      MESSAGE
1       bob   just now  new    no      task      lunch at noon?
2       bob   just now  new    yes     question  ready?
EOF
    cat >"$d/read.txt" <<'EOF'
number:   2
from:     acme.demo.bob
trust:    verified
EOF
    echo '{"number":3,"from":"acme.demo.bob","trust":"verified","kind":"task","urgent":false,"reply_to":null,"relay":null,"body":"ping"}' >"$d/json-read.txt"
    echo '{"endpoint":"acme.demo.alice","total":3,"new":3,"urgent":1,"messages":[{"number":1},{"number":2},{"number":3}]}' >"$d/json-inbox.txt"
    echo ping >"$d/json-jq.txt"
    printf 'This deletes acme.demo.bob and its empty inbox. It cannot be undone.\nType the address to confirm: acme.demo.bob\nDeleted endpoint acme.demo.bob\n' >"$d/delete.txt"
    echo 1 >"$d/refused.rc"
    cat >"$d/lifecycle.txt" <<'EOF'
stopped pid 355
started: nats-server pid 709 on 127.0.0.1:4222
type the folder to remove (/home/tester/mash-nats): /home/tester/mash-nats
stopped pid 709
removed /home/tester/mash-nats
note: mashctl's folder /home/tester/.config/mash still points at this server.
EOF
    fails=$(content_fails "$d")
    if [ "$fails" = 0 ]; then st_ok "the content asserts pass on outputs shaped like a real run's"; else st_bad "the content asserts failed $fails times on good fixture outputs"; fi
    # one defect each: every one must be caught
    mutate_check "$d" json-jq.txt 'null' "json-jq with a null body"
    mutate_check "$d" json-read.txt '{"number":3,"trust":"verified","body":null}' "json-read with a null body"
    mutate_check "$d" json-inbox.txt '{"total":2,"messages":[]}' "json-inbox with total 2"
    mutate_check "$d" json-inbox.txt 'not json' "json-inbox that is not JSON"
    mutate_check "$d" status.txt 'acme.demo.bob: ready, offline' "status with alice missing"
    mutate_check "$d" who.txt 'ADDRESS STATE' "who with no rows"
    mutate_check "$d" inbox.txt 'a note from bob about bob' "inbox with no mail rows from bob"
    mutate_check "$d" lifecycle.txt 'stopped pid 355' "lifecycle with only a stop"
    # a scan of nothing is a failure; a seed shape in any kept file is a failure
    mkdir -p "$1/empty/out"
    if scan_run "$1/empty" >/dev/null; then st_bad "the secret scan passed with no file to scan"; else st_ok "the secret scan fails when it scanned no file"; fi
    for id in last.txt build-image.log release-listing.txt out/x.cmd; do
        mkdir -p "$1/sec/out"
        : >"$1/sec/out/y.txt"
        printf 'SU%s\n' "$(printf 'A%.0s' $(seq 1 50))" >"$1/sec/$id"
        if scan_run "$1/sec" >/dev/null; then st_bad "the secret scan missed a seed shape in $id"; else st_ok "the secret scan reads $id"; fi
        rm -f "$1/sec/$id"
    done
}

# content_fails DIR: how many content asserts fail on the outputs in DIR
content_fails() {
    (
        OUT=$1 SETUP_DIR=/home/tester/mash-nats PASS=0 FAIL=0
        run_content_asserts >/dev/null
        echo "$FAIL"
    )
}

# mutate_check DIR FILE TEXT WHAT: replace FILE's content with TEXT, expect the
# content asserts to fail, then restore it
mutate_check() {
    local d=$1 f=$2 text=$3 what=$4 n
    cp -- "$d/$f" "$d/$f.keep"
    printf '%s\n' "$text" >"$d/$f"
    n=$(content_fails "$d")
    mv -- "$d/$f.keep" "$d/$f"
    if [ "$n" -gt 0 ]; then st_ok "the content asserts catch $what"; else st_bad "the content asserts missed $what"; fi
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

    # the structural ids are the harness's own: never a guide block, never filled
    n=0
    for id in "${STRUCT_IDS[@]}"; do
        if printf '%s\n' "${STEP_IDS[@]}" "${MANUAL_IDS[@]}" | grep -Fxq -- "$id"; then n=1; st_bad "structural id $id is also a fill id"; fi
        if grep -Fq "output: $id " "$guide"; then n=1; st_bad "structural id $id has a block in the guide"; fi
    done
    [ "$n" = 0 ] && st_ok "the structural ids (${STRUCT_IDS[*]}) are not fill targets"
    if printf '%s\n' "${STRUCT_IDS[@]}" | grep -Fxq claude-code-structure; then st_ok "the claude-code-structure step exists"; else st_bad "no claude-code-structure step"; fi
    # the delete step types the address at a prompt; no --yes anywhere in the guide
    if grep -Fq -e '--yes' "$guide"; then st_bad "the guide still mentions --yes"; else st_ok "the guide does not mention --yes (the person types the address)"; fi
    if awk '/^step_delete\(\)/,/^}/' "$SELF" | grep -Fq -e '--yes'; then st_bad "the delete step still uses --yes"; else st_ok "the delete step does not use --yes"; fi
    # the egress proxy: its source is there, gofmt-clean, and its own tests are run by scripts/guide_run_test.go
    if [ -f "$HERE/egressproxy/main.go" ] && [ -f "$HERE/egressproxy/main_test.go" ]; then
        st_ok "the egress proxy source and its test are in guide-run/egressproxy"
    else
        st_bad "guide-run/egressproxy/main.go or main_test.go is missing"
    fi
    if command -v gofmt >/dev/null 2>&1; then
        if [ -z "$(gofmt -l "$HERE/egressproxy")" ]; then st_ok "egressproxy is gofmt-clean"; else st_bad "egressproxy is not gofmt-clean"; fi
    fi
    # published mode never gives the install step the file:// escape hatches
    if awk '/^step_install_published\(\)/,/^}/' "$SELF" | grep -Eq 'MASH_RELEASE_BASE|MASH_INSTALL_ALLOW_FILE'; then
        st_bad "the published install step uses MASH_RELEASE_BASE or MASH_INSTALL_ALLOW_FILE"
    else
        st_ok "the published install step uses neither MASH_RELEASE_BASE nor MASH_INSTALL_ALLOW_FILE"
    fi

    # 1b. each step's commands against the guide's command blocks
    selftest_commands "$guide" "$tmp"
    # 1c. the content asserts, on fixture outputs shaped like a real run's
    selftest_asserts "$tmp"

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
MODE=rehearsal
VERSION_SET=0
ONLY_INSTALL=0
PROXY_PID=''
SOCAT_PID=''
EGRESS_DIR=''
EGRESS_SOCK=''
EGRESS_LOG=''
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
IMG_TAG_MADE=0
CONTAINER_MADE=0
CLEANED='not run'
RESULTS=()
STEPLINES=()
IMGLINES=''
NATSHA=''
DISK0=''
STEP=''
STEP_BAD=0
RELEASE_MISSING=0
EG1=''
EG3=''
LAST=''
USE_LOCALBIN=0
START=0
ABORT=0

free_gb() { df -P -k "$1" | awk 'NR==2 {printf "%d", $4 / 1048576}'; }
free_mb() { df -P -k "$1" | awk 'NR==2 {printf "%d", $4 / 1024}'; }

# egress_summary: one line, counts and hosts from the proxy's log
egress_summary() {
    local a r h
    [ -f "$EGRESS_LOG" ] || { echo "no log"; return; }
    a=$(grep -c ' allowed ' "$EGRESS_LOG")
    r=$(grep -c ' refused ' "$EGRESS_LOG")
    h=$(awk '{print $2 ":" $3}' "$EGRESS_LOG" | sort | uniq -c | awk '{printf "%s%s x%s", sep, $2, $1; sep = ", "}')
    echo "$a allowed, $r refused ($h)"
}

write_receipt() {
    local elapsed=$((SECONDS - START)) l
    {
        echo "# guide-run receipt"
        echo
        echo "- run id: $RUN"
        echo "- date (UTC): $(date -u +%Y-%m-%dT%H:%M:%SZ)"
        echo "- harness: scripts/guide-run/guide-run.sh, sha256 $(sha256sum "$SELF" | awk '{print $1}')"
        echo "- guide: docs/guide/walkthrough.md, sha256 $(sha256sum "$GUIDE_DEFAULT" | awk '{print $1}')"
        if [ "$MODE" = published ]; then
            echo "- mode=published"
            [ "$ONLY_INSTALL" = 1 ] && echo "- --only-install: stops after the install step and the egress checks; not a full run"
            echo "- version: $VERSION (downloaded by the container from $GH_BASE/$VERSION/, through the allowlist proxy)"
            echo "- egress allowlist: $ALLOWLIST (exact names, no wildcard; only public unicast addresses, dialed by address)"
            echo "- egress log summary: $(egress_summary)"
            echo "- egress log: $EGRESS_LOG"
            echo "- egress log lines (host and verdict only):"
            if [ -f "$EGRESS_LOG" ]; then awk '{print "    " $2, $3}' "$EGRESS_LOG"; fi
        else
            echo "- mode=rehearsal (a local release folder, file:// base); not the published release"
            echo "- version: $VERSION"
            echo "- release folder files (SHA256SUMS):"
            if [ -f "${RELDIR:-/nonexistent}/SHA256SUMS" ]; then sed 's/^/    /' "$RELDIR/SHA256SUMS"; fi
        fi
        echo "- Claude Code step: structural check only, no model API"
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
        if [ "$MODE" = published ]; then
            echo "- egress: only the proxy's UNIX socket, bind-mounted read-only at $EGRESS_CTR_DIR; socat in the container listens on 127.0.0.1:3128 as tester; https_proxy is set only on the download commands"
        fi
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
        echo "- The Claude Code steps (7): structural check only, no model API. Not filled."
        echo "- A real macOS machine."
        if [ "$MODE" = published ]; then
            echo "- The attestation: the container has no gh, so install.sh says the checksums come from the same download."
            echo "- The plugin tree: not in the release assets, so it is checked in rehearsal mode."
        else
            echo "- The published release: the binaries are built from this checkout and installed from a local folder."
        fi
    } >"$REPORT" 2>/dev/null
}

cleanup() {
    local left=0 imgdesc
    trap - EXIT INT TERM
    # the socat of this run, by its recorded pid inside the container, then the container
    if [ "$CONTAINER_MADE" = 1 ] && [ -n "$SOCAT_PID" ]; then
        podman exec --user tester "$CNAME" kill "$SOCAT_PID" >/dev/null 2>&1
    fi
    if [ "$CONTAINER_MADE" = 1 ]; then podman rm -f "$CNAME" >/dev/null 2>&1; fi
    # the proxy of this run, by its recorded pid, and its socket by exact path
    if [ -n "$PROXY_PID" ]; then
        kill "$PROXY_PID" >/dev/null 2>&1
        wait "$PROXY_PID" >/dev/null 2>&1
        if kill -0 "$PROXY_PID" 2>/dev/null; then left=$((left + 1)); fi
    fi
    if [ -n "$EGRESS_SOCK" ]; then
        rm -f -- "$EGRESS_SOCK"
        if [ -e "$EGRESS_SOCK" ]; then left=$((left + 1)); fi
        rmdir -- "$EGRESS_DIR" 2>/dev/null
    fi
    if [ -n "$IMG_ID" ]; then
        podman rmi -f "$IMG_ID" >/dev/null 2>&1
    elif [ "$IMG_TAG_MADE" = 1 ]; then
        podman rmi -f "$IMG" >/dev/null 2>&1
    fi
    case $CTX in "$SCRATCH"/*/build) rm -rf -- "$CTX" ;; esac
    if [ "$CONTAINER_MADE" = 1 ] && podman container exists "$CNAME" 2>/dev/null; then left=$((left + 1)); fi
    if [ -n "$IMG_ID" ] && podman image exists "$IMG_ID" 2>/dev/null; then left=$((left + 1)); fi
    if [ -z "$IMG_ID" ] && [ "$IMG_TAG_MADE" = 1 ] && podman image exists "$IMG" 2>/dev/null; then left=$((left + 1)); fi
    imgdesc=none
    if [ -n "$IMG_ID" ]; then imgdesc="$IMG_ID by id"; elif [ "$IMG_TAG_MADE" = 1 ]; then imgdesc="$IMG by tag"; fi
    if [ "$left" = 0 ]; then
        CLEANED="done: container $CNAME removed by name, image $imgdesc removed, build dir removed${PROXY_PID:+, proxy pid $PROXY_PID stopped, socket removed}; none left"
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

# The published route, as docs/install.md and the setup skill give it: curl the
# files from the release, check install.sh against SHA256SUMS, run install.sh
# with neither MASH_RELEASE_BASE nor MASH_INSTALL_ALLOW_FILE. https_proxy is set
# on the commands that download, and nowhere else.
step_install_published() {
    local px="https_proxy=$PROXY_URL"
    step_begin install
    if ! step_cmd zero "$REL" "$px curl -fsSLO $GH_BASE/$VERSION/SHA256SUMS"; then
        RELEASE_MISSING=1
        step_end
        return 1
    fi
    step_cmd zero "$REL" "$px curl -fsSLO $GH_BASE/$VERSION/install.sh" || { step_end; return 1; }
    step_cmd zero "$REL" "grep ' install.sh\$' SHA256SUMS | sha256sum -c -" || { step_end; return 1; }
    step_cmd zero "$REL" "MASH_VERSION=$VERSION $px bash install.sh" || { step_end; return 1; }
    USE_LOCALBIN=1
    step_cmd zero "$CHOME" "mash version"
    step_cmd zero "$CHOME" "mashctl version"
    step_end
}

step_install() {
    if [ "$MODE" = published ]; then
        step_install_published
        return
    fi
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
    if [ "$MODE" = published ]; then
        step_cmd zero "$REL" "https_proxy=$PROXY_URL curl -fsSLO $GH_BASE/$VERSION/mash-local-nats-setup.sh" ||
            { step_end; return 1; }
    fi
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
    # as the guide shows: create writes a join file (--out), then mash join
    # installs it. The join file holds credentials; neither command prints it.
    step_cmd zero "$CHOME" "mashctl endpoint create $OWNER.demo.bob --out $OWNER.demo.bob.join"
    step_cmd zero "$CHOME" "mash join $OWNER.demo.bob.join"
    step_cmd zero "$CHOME" "mashctl endpoint create $OWNER.demo.alice --out $OWNER.demo.alice.join"
    step_cmd zero "$CHOME" "mash join $OWNER.demo.alice.join"
    step_end
}

step_status() {
    step_begin status
    step_cmd zero "$CHOME" "MASH_ENDPOINT=$OWNER.demo.bob mash status"
    step_cmd zero "$CHOME" "MASH_ENDPOINT=$OWNER.demo.alice mash status"
    step_end
}

step_who() {
    step_begin who
    step_cmd zero "$CHOME" "MASH_ENDPOINT=$OWNER.demo.bob mash who"
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
    # as the guide shows: a terminal, and the person types the address to
    # confirm. podman exec -t, and the pty helper types it at the prompt.
    EXEC_EXTRA=-t step_cmd zero "$CHOME" "python3 /usr/local/lib/guide-run-ptyhelper.py '$OWNER.demo.bob' mashctl endpoint delete $OWNER.demo.bob"
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

# egress: negative checks inside the container (published mode only)
step_egress() {
    local px="https_proxy=$PROXY_URL"
    step_begin egress
    # 1. a host that is not on the allowlist is refused by the proxy (403)
    step_cmd nonzero "$CHOME" "$px curl -sS --max-time 10 https://example.com"
    EG1=$(cat "$LAST")
    # 2. no proxy: no network at all
    step_cmd nonzero "$CHOME" "curl -sS --noproxy '*' --max-time 5 https://github.com"
    # 3. a release that does not exist: the 404 text, a clean failure
    step_cmd nonzero "$CHOME" "$px curl -fsS --max-time 20 $GH_BASE/v0.0.0-doesnotexist/SHA256SUMS"
    EG3=$(cat "$LAST")
    step_end
}

# claude-code-structure: not a model run. claude --version only if there is a
# claude; the plugin tree checked statically (rehearsal mode: this checkout's).
step_claude_structure() {
    local t=$CHOME/plugin-tree/plugin
    step_begin claude-code-structure
    step_cmd zero "$CHOME" 'if command -v claude >/dev/null 2>&1; then claude --version; else echo "not run: no Claude Code in the container"; fi'
    if [ "$MODE" = published ]; then
        echo "not run: plugin tree is checked in rehearsal mode" >>"$OUT/$STEP.txt"
        step_end
        return
    fi
    step_cmd zero "$CHOME" "set -e
for f in .claude-plugin/plugin.json .mcp.json hooks/hooks.json; do
    test -f $t/\$f
    jq -e . $t/\$f >/dev/null
    echo \"ok: plugin/\$f exists and parses as JSON\"
done
jq -e '.mcpServers | length == 1' $t/.mcp.json >/dev/null
echo \"ok: exactly one MCP server: \$(jq -r '.mcpServers | keys[0]' $t/.mcp.json)\"
jq -e '(.hooks | keys | sort) == [\"PostToolUse\",\"SessionStart\",\"UserPromptSubmit\"]' $t/hooks/hooks.json >/dev/null
echo \"ok: exactly the three hooks: \$(jq -r '.hooks | keys | join(\", \")' $t/hooks/hooks.json)\""
    step_end
}

# jcheck FILE EXPR: FILE parses as one JSON value d, and the python expression
# EXPR (written here, never from the container) is true of it
jcheck() {
    python3 -I -c 'import json, sys
d = json.load(open(sys.argv[1], encoding="utf-8"))
sys.exit(0 if eval(sys.argv[2]) else 1)' "$1" "$2" 2>/dev/null
}

# run_content_asserts: what each step's recorded output must show, from
# $OUT/<id>.txt only (the selftest runs it on a real earlier run's outputs)
run_content_asserts() {
    local f n
    f=$OUT/status.txt
    if [ -f "$f" ] && grep -Eq "^$OWNER\.demo\.bob: ready" "$f" && grep -Eq "^$OWNER\.demo\.alice: ready" "$f"; then
        ok "status shows $OWNER.demo.bob: ready and $OWNER.demo.alice: ready"
    else
        bad "status does not show both endpoints ready"
    fi
    f=$OUT/who.txt
    if [ -f "$f" ] && grep -Eq "^$OWNER\.demo\.alice( |\$)" "$f" && grep -Eq "^$OWNER\.demo\.bob( |\$)" "$f"; then
        ok "who lists $OWNER.demo.alice and $OWNER.demo.bob"
    else
        bad "who does not list both endpoints"
    fi
    f=$OUT/read.txt
    # the guide's form is a "trust: verified" line; the 0.5.x form is "[verified]" in the header
    if [ -f "$f" ] && grep -Eq '^trust: +verified$|^--- #2 from [^ ]+ \[verified\]' "$f"; then ok "mail 2 read as alice: trust verified"; else bad "mail 2 read as alice: not shown as trust verified"; fi
    f=$OUT/json-read.txt
    if [ -f "$f" ] && grep -Eq '"trust": *"verified"' "$f"; then ok "mail 3 read as JSON: trust verified"; else bad "mail 3 read as JSON: no verified trust"; fi
    if [ -f "$f" ] && jcheck "$f" 'd["body"] == "ping" and d["number"] == 3'; then
        ok "mail 3 read as JSON: number 3, body is ping"
    else
        bad "mail 3 read as JSON: not valid JSON with number 3 and body ping"
    fi
    f=$OUT/json-inbox.txt
    if [ -f "$f" ] && jcheck "$f" 'd["total"] == 3 and len(d["messages"]) == 3'; then
        ok "inbox as JSON: valid, total 3, three messages"
    else
        bad "inbox as JSON: not valid JSON with total 3 and three messages"
    fi
    # the command is the guide's (jq -r .body): null would print "null", so the
    # whole output must be the sent text
    f=$OUT/json-jq.txt
    if [ -f "$f" ] && [ "$(cat "$f")" = ping ]; then
        ok "jq -r .body printed the sent text: ping"
    else
        bad "jq -r .body did not print exactly ping"
    fi
    # the inbox table has no trust column; trust is asserted via read and read --json
    f=$OUT/inbox.txt
    n=0
    [ -f "$f" ] && n=$(grep -Ec '^[0-9]+ +bob( |$)' "$f")
    if [ "$n" = 2 ]; then ok "the inbox lists two mail rows from bob"; else bad "the inbox lists $n mail rows from bob, wanted 2"; fi
    f=$OUT/refused.rc
    if [ -f "$f" ] && [ "$(tail -n 1 "$f")" != 0 ] && [ -n "$(cat "$f")" ]; then
        ok "the call after delete exited non-zero ($(tail -n 1 "$f"))"
    else
        bad "the call after delete did not exit non-zero"
    fi
    f=$OUT/delete.txt
    if [ -f "$f" ] && grep -Eq 'Type the address to confirm: *'"$OWNER"'\.demo\.bob' "$f" && grep -Fq "Deleted endpoint $OWNER.demo.bob" "$f"; then
        ok "delete: the address was typed at the prompt on a terminal and the endpoint was deleted"
    else
        bad "delete: no typed confirmation and deletion in the screen output"
    fi
    f=$OUT/lifecycle.txt
    n=0
    [ -f "$f" ] && n=$(grep -Ec '^stopped pid [0-9]+$' "$f")
    if [ -f "$f" ] && [ "$n" = 2 ] && grep -Eq '^started: nats-server pid [0-9]+ on ' "$f" &&
        { if [ -n "${SETUP_DIR:-}" ]; then grep -Fxq "removed $SETUP_DIR" "$f"; else grep -Eq '^removed /' "$f"; fi; }; then
        ok "lifecycle: stopped, started and removed (each said so)"
    else
        bad "lifecycle: the output does not show stop, start and remove"
    fi
}

# asserts: hard facts, from the recorded outputs
run_asserts() {
    local f
    echo "== asserts"
    if [ "$MODE" = published ]; then run_egress_asserts; fi
    if [ "$ONLY_INSTALL" = 1 ]; then run_install_asserts; return; fi
    run_content_asserts
    run_install_asserts
    f=$OUT/claude-code-structure.txt
    if [ -f "$OUT/claude-code-structure.ok" ] && grep -Fq 'not run: no Claude Code in the container' "$f"; then
        ok "claude-code-structure: structural check only, no model API"
    else
        bad "claude-code-structure did not pass"
    fi
    run_secret_assert
}

run_install_asserts() {
    local f=$OUT/install.txt
    if [ -f "$f" ] && grep -Eq "^mash v?${VERSION#v}( |\$)" "$f" && grep -Eq "^mashctl v?${VERSION#v}( |\$)" "$f"; then
        ok "mash version and mashctl version both say ${VERSION#v}"
    else
        bad "mash version and mashctl version do not both say ${VERSION#v}"
    fi
    [ "$ONLY_INSTALL" = 1 ] && run_secret_assert
}

run_egress_asserts() {
    [ -f "$OUT/egress.rc" ] || return 0
    # curl's own line for a refused CONNECT (the proxy answers 403 to it)
    if grep -Fq 'CONNECT tunnel failed, response 403' <<<"$EG1"; then
        ok "egress: https://example.com through the proxy is refused (403)"
    else
        bad "egress: https://example.com was not refused with 403"
    fi
    if [ "$(sed -n 2p "$OUT/egress.rc")" != 0 ] && [ -n "$(sed -n 2p "$OUT/egress.rc")" ]; then
        ok "egress: a direct curl to github.com with no proxy fails (no network)"
    else
        bad "egress: a direct curl to github.com did not fail"
    fi
    if grep -Fq '404' <<<"$EG3"; then
        ok "egress: a release that does not exist fails with the 404 text"
    else
        bad "egress: the nonexistent release did not fail with 404"
    fi
    if grep -Eq ' host=example\.com refused reason="host is not on the allowlist" ' "$EGRESS_LOG" 2>/dev/null; then
        ok "egress.log records the refused example.com request"
    else
        bad "egress.log has no refused example.com line"
    fi
    if grep -Eq ' host=github\.com allowed ' "$EGRESS_LOG" 2>/dev/null; then
        ok "egress.log records allowed github.com"
    else
        bad "egress.log has no allowed github.com line"
    fi
    if grep -Evq ' host=(github\.com|objects\.githubusercontent\.com|release-assets\.githubusercontent\.com|example\.com) ' "$EGRESS_LOG" 2>/dev/null; then
        bad "egress.log has a host other than the allowlist and the example.com probe"
    else
        ok "egress.log has no host outside the allowlist and the example.com probe"
    fi
}

run_secret_assert() {
    # no seed or credential content in any captured output
    if scan_run "$RUN_DIR"; then
        ok "no seed, credential or JWT content in any file the run keeps ($(run_files "$RUN_DIR" | wc -l) files scanned)"
    else
        bad "secret-shaped content in a file the run keeps, or nothing to scan (not filling)"
    fi
}

# start_proxy: build the egress proxy with the host's go into the run folder,
# start it in the background (pid recorded), wait for its socket
start_proxy() {
    local i=0
    # a UNIX socket path is limited to about 107 bytes, which a run folder under
    # a long scratch path exceeds: the socket's own folder is short (0700)
    EGRESS_DIR=${XDG_RUNTIME_DIR:-/tmp}/gr-$RUN
    EGRESS_SOCK=$EGRESS_DIR/egress.sock
    EGRESS_LOG=$RUN_DIR/egress.log
    mkdir -p "$EGRESS_DIR" && chmod 700 "$EGRESS_DIR" || return 1
    echo "== building the egress proxy"
    (cd "$ROOT" && CGO_ENABLED=0 go build -trimpath -o "$RUN_DIR/egressproxy" ./scripts/guide-run/egressproxy) \
        >"$RUN_DIR/egressproxy-build.log" 2>&1 || {
        bad "the egress proxy did not build; see $RUN_DIR/egressproxy-build.log"
        return 1
    }
    : >"$EGRESS_LOG"
    "$RUN_DIR/egressproxy" -sock "$EGRESS_SOCK" -log "$EGRESS_LOG" >"$RUN_DIR/egressproxy.stderr" 2>&1 </dev/null &
    PROXY_PID=$!
    while [ ! -S "$EGRESS_SOCK" ] && [ "$i" -lt 50 ]; do
        sleep 0.1
        i=$((i + 1))
    done
    if [ ! -S "$EGRESS_SOCK" ]; then
        bad "the egress proxy did not start; see $RUN_DIR/egressproxy.stderr"
        return 1
    fi
    echo "egress proxy pid $PROXY_PID on $EGRESS_SOCK (mode $(stat -c %a "$EGRESS_SOCK")), allowing $ALLOWLIST"
}

# start_socat: inside the container, as tester, 127.0.0.1:3128 to the proxy's
# socket; the pid is recorded and killed by pid at the end
start_socat() {
    local i=0 comm
    SOCAT_PID=$(cx "$CHOME" "setsid socat TCP-LISTEN:3128,bind=127.0.0.1,fork,reuseaddr UNIX-CONNECT:$EGRESS_CTR_DIR/egress.sock </dev/null >/dev/null 2>&1 & echo \$!") ||
        { bad "socat did not start in the container"; SOCAT_PID=''; return 1; }
    case $SOCAT_PID in '' | *[!0-9]*) bad "socat gave no pid: $SOCAT_PID"; SOCAT_PID=''; return 1 ;; esac
    while [ "$i" -lt 20 ]; do
        comm=$(cx "$CHOME" "cat /proc/$SOCAT_PID/comm" 2>/dev/null </dev/null)
        if [ "$comm" = socat ] && cx "$CHOME" "exec 3<>/dev/tcp/127.0.0.1/3128" >/dev/null 2>&1 </dev/null; then
            echo "socat pid $SOCAT_PID in $CNAME, 127.0.0.1:3128 to $EGRESS_CTR_DIR/egress.sock"
            return 0
        fi
        sleep 0.25
        i=$((i + 1))
    done
    bad "socat is not listening on 127.0.0.1:3128 in the container"
    return 1
}

do_run() {
    local t g rd n f
    for t in podman curl sha256sum df awk timeout python3; do
        command -v "$t" >/dev/null 2>&1 || die "needs $t"
    done
    [ "$(id -u)" -ne 0 ] || die "run as an ordinary user (rootless podman)"
    [ -n "$NATPIN" ] || die "natscli v$NCV has no pinned sha256; set GUIDE_RUN_NATSCLI_SHA256"
    if [ "$MODE" = published ]; then
        # the latest release is not resolved here: the version is required, so
        # a run names exactly what it follows
        [ "$VERSION_SET" = 1 ] || die "--published needs --version vX.Y.Z"
        [ -z "$RELDIR" ] || die "--published reads the release from GitHub: do not give --release-dir"
        case $VERSION in v[0-9]*) ;; *) die "--version $VERSION is not a version such as v0.5.6" ;; esac
        command -v go >/dev/null 2>&1 || die "--published needs go on the host to build the egress proxy"
    else
        [ "$ONLY_INSTALL" = 0 ] || die "--only-install belongs to --published"
        [ -n "$RELDIR" ] || die "needs --release-dir DIR (or --make-release-dir DIR to build one), or --published --version V"
        RELDIR=$(cd "$RELDIR" 2>/dev/null && pwd -P) || die "release folder does not exist"
        check_release_dir "$RELDIR"
    fi

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

    echo "== run $RUN: ${RELDIR:-$GH_BASE} ($VERSION), mode $MODE; run folder $RUN_DIR"
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
# with a prompt ("type the folder ...: " or "Type the address to confirm: ") send
# RESP and a newline, as a person typing it would. Prints the
# terminal output and exits with the real exit status of CMD.
import os, pty, re, select, signal, sys, time
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
    if not sent and out.endswith(b': ') and re.search(rb'type the (folder|address)', out.lower()):
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
    nats-server openssl iproute2 procps curl unzip ca-certificates util-linux python3 jq socat \
    && rm -rf /var/lib/apt/lists/*
COPY nats.zip /tmp/nats.zip
RUN unzip -j -o /tmp/nats.zip '*/nats' -d /usr/local/bin && chmod 755 /usr/local/bin/nats && rm /tmp/nats.zip
COPY ptyhelper.py /usr/local/lib/guide-run-ptyhelper.py
RUN chmod 644 /usr/local/lib/guide-run-ptyhelper.py && useradd -m -s /bin/bash tester
CF
    echo "== building $IMG from $BASE_IMAGE"
    # recorded before the build, so a failure anywhere after it still removes
    # this run's image, by this exact tag
    IMG_TAG_MADE=1
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

    local runflags=()
    if [ "$MODE" = published ]; then
        start_proxy || exit 1
        # the host user is the container's tester (uid 1000), so the 0600 socket
        # is reachable by socat running as tester
        runflags=("--userns=keep-id:uid=1000,gid=1000" -v "$EGRESS_DIR:$EGRESS_CTR_DIR:ro")
    else
        # stage the release folder world-readable, then copy it in
        mkdir -p "$CTX/release"
        for f in "${RELEASE_FILES[@]}"; do cp -- "$RELDIR/$f" "$CTX/release/$f"; done
        chmod 755 "$CTX/release" "$CTX/release"/*
        # and this checkout's plugin tree, three files, for the structural check
        mkdir -p "$CTX/plugin-tree/plugin/.claude-plugin" "$CTX/plugin-tree/plugin/hooks"
        cp -- "$ROOT/plugin/.claude-plugin/plugin.json" "$CTX/plugin-tree/plugin/.claude-plugin/plugin.json"
        cp -- "$ROOT/plugin/.mcp.json" "$CTX/plugin-tree/plugin/.mcp.json"
        cp -- "$ROOT/plugin/hooks/hooks.json" "$CTX/plugin-tree/plugin/hooks/hooks.json"
        chmod -R a+rX "$CTX/plugin-tree"
    fi

    echo "== starting $CNAME"
    CONTAINER_MADE=1
    podman run -d --name "$CNAME" --user tester --network none \
        --security-opt no-new-privileges --cap-drop=all --pids-limit 2048 \
        "${runflags[@]}" -e "HOME=$CHOME" "$IMG" sleep 7200 >/dev/null || {
        bad "the container did not start"
        exit 1
    }
    if [ "$MODE" = published ]; then
        cx "$CHOME" "mkdir -p release" || { bad "cannot make the release folder in the container"; exit 1; }
        start_socat || exit 1
    else
        podman cp "$CTX/release" "$CNAME:$REL" || {
            bad "podman cp of the release folder failed"
            exit 1
        }
        podman cp "$CTX/plugin-tree" "$CNAME:$CHOME/plugin-tree" || {
            bad "podman cp of the plugin tree failed"
            exit 1
        }
        cx "$CHOME" "ls -l release" >"$RUN_DIR/release-listing.txt" 2>&1
    fi

    SETUP_DIR=''
    step_install
    [ -f "$OUT/install.ok" ] || ABORT=1
    if [ "$MODE" = published ] && [ "$RELEASE_MISSING" = 0 ]; then
        # the allowlist's negative checks, and the release that does not exist
        step_egress
    fi
    if [ "$ABORT" = 0 ] && [ "$ONLY_INSTALL" = 1 ]; then
        echo "== --only-install: stopping after the install step"
        ABORT=2
    fi
    if [ "$ABORT" = 0 ]; then
        step_claude_structure
        step_server
        [ -f "$OUT/server.ok" ] || ABORT=1
    fi
    if [ "$ABORT" = 0 ]; then
        step_endpoints
        [ -f "$OUT/endpoints.ok" ] || ABORT=1
    fi
    if [ "$ABORT" = 0 ]; then
        step_status
        step_who
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
    elif [ "$ABORT" = 1 ]; then
        bad "the run stopped after a step that everything else needs"
    fi
    if [ "$RELEASE_MISSING" = 1 ]; then
        echo "== the release $VERSION was not found: nothing else was run"
    else
        run_asserts
    fi
    echo "== totals: ok=$PASS FAIL=$FAIL ($((SECONDS - START)) s)"
    if [ "$FAIL" = 0 ] && [ "$PASS" -gt 0 ] && [ "$ONLY_INSTALL" = 0 ]; then
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
    --version) VERSION=${2:?--version needs a value}; VERSION_SET=1; shift 2 ;;
    --published) MODE=published; shift ;;
    --only-install) ONLY_INSTALL=1; shift ;;
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
