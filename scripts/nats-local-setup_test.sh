#!/usr/bin/env bash
# Test harness for scripts/nats-local-setup.sh.  Run it with:
#
#     bash scripts/nats-local-setup_test.sh [main] [osrel] [sec] [reach] [root]
#
# Every test runs inside throwaway rootless podman containers made from
# debian:bookworm-slim (the helper's target OS); nothing is installed, started
# or tested on the host.  On the host the harness only writes into a fresh
# directory under $TMPDIR (Go build cache, mashctl and mash binaries built
# with CGO_ENABLED=0, build contexts, transcripts).  It removes the containers
# and images it made, by exact name, and then that directory, when it ends.
#
# Groups (default: all):
#   main   the whole helper as an ordinary user, with fake downloads, shims
#          and canary secrets
#   osrel  a non-Debian /etc/os-release is refused
#   sec    path-swap rechecks (device:inode), the server-log allowlist, and
#          where each command writes
#   reach  a second OS user (useradd in the container): can reach the
#          listening port, cannot connect with NATS without credentials,
#          cannot read DIR, server.log or mash-setup.json
#   root   every subcommand refuses to run as root (podman exec --user root)
#
# Environment: NATSSETUP_TEST_SUFFIX=-xx is appended to the container and
# image names (so concurrent users of podman never collide);
# NATSSETUP_TEST_HELPER=FILE tests another copy of the helper (used to show
# that the new cases fail against the old helper); NATSSETUP_TEST_BREAK=1
# makes the OpenSSL-version case expect an impossible exit code, to show the
# assertion can fail.
# Needs: podman (rootless), go, network access for the first image build
# (apt-get) and for the pinned downloads in the main group.
# NATSSETUP_TEST_OUT=DIR (must not exist) keeps the transcripts there.
# Prints PASS or FAIL for every case and exits non-zero if any case failed.
set -u

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P) || exit 2
REPO=$(dirname "$HERE")
HELPER_SRC=${NATSSETUP_TEST_HELPER:-$HERE/nats-local-setup.sh}
[ -f "$HELPER_SRC" ] || { echo "no $HELPER_SRC" >&2; exit 2; }
command -v podman >/dev/null || { echo "podman is required" >&2; exit 2; }
command -v go >/dev/null || { echo "go is required" >&2; exit 2; }

T=$(mktemp -d "${TMPDIR:-/tmp}/natssetup-test.XXXXXX") || exit 2
RUNID=${T##*.}
RUNID=$(printf '%s' "$RUNID" | tr 'A-Z' 'a-z')
SFX=${NATSSETUP_TEST_SUFFIX:-}
C1=mash-natssetup-test-$RUNID$SFX
IMG1=localhost/mash-natssetup-test-$RUNID$SFX
BUILT_IMG1=0
GROUPS_WANTED=${*:-main osrel sec reach root}
TOTAL_FAIL=0
TOTAL_PASS=0

cleanup() {
    # NATSSETUP_TEST_OUT=DIR keeps the transcripts (a new directory)
    if [ -n "${NATSSETUP_TEST_OUT:-}" ] && [ -d "$T/out" ] &&
        [ ! -e "$NATSSETUP_TEST_OUT" ]; then
        cp -r "$T/out" "$NATSSETUP_TEST_OUT"
    fi
    podman rm -f "$C1" >/dev/null 2>&1
    [ "$BUILT_IMG1" = 1 ] && podman rmi -f "$IMG1" >/dev/null 2>&1
    case $T in
    */natssetup-test.*) rm -rf -- "$T" ;;
    esac
}
trap cleanup EXIT

say() { printf '== %s\n' "$*"; }
want() { case " $GROUPS_WANTED " in *" $1 "*) return 0 ;; esac; return 1; }

# ---------------------------------------------------------------- build
say "building mashctl and mash (CGO_ENABLED=0) into $T/bin"
mkdir -p "$T/bin" "$T/gocache" "$T/ctx1" "$T/out"
for b in mashctl mash; do
    (cd "$REPO" && CGO_ENABLED=0 GOCACHE="$T/gocache" \
        go build -o "$T/bin/$b" "./cmd/$b") || {
        echo "FAIL  go build $b" >&2
        exit 2
    }
done

cat >"$T/pty.py" <<'__PTY_PY__'
# pty.py TYPED CMD...: run CMD on a pseudo-terminal; when it asks for a
# typed confirmation, send TYPED (or ^D for __eof__, nothing for __none__).
# When it asks "Owner name (...): " or "Owner name (...) [x]: ", send the
# next answer of TYPED split on "|" (an empty field is an empty answer);
# a TYPED that starts with "owner:" is only for those prompts.
import os, pty, select, sys
typed, cmd = sys.argv[1], sys.argv[2:]
owners = []
if typed.startswith('owner:'):
    owners = typed[6:].split('|')
    typed = '__none__'
pid, fd = pty.fork()
if pid == 0:
    os.execvp(cmd[0], cmd)
out = b''
sent = False
oseen = 0
while True:
    r, _, _ = select.select([fd], [], [], 30)
    if not r:
        break
    try:
        d = os.read(fd, 4096)
    except OSError:
        break
    if not d:
        break
    out += d
    if owners and out.count(b'Owner name (') > oseen and (
            out.endswith(b'): ') or out.endswith(b']: ')):
        oseen += 1
        os.write(fd, owners.pop(0).encode() + b'\n')
    if not sent and out.endswith(b'\n> ') and b'Type this exactly' in out:
        sent = True
        if typed == '__eof__':
            os.write(fd, b'\x04')
        elif typed != '__none__':
            os.write(fd, typed.encode() + b'\n')
try:
    _, st = os.waitpid(pid, 0)
except ChildProcessError:
    st = 0
sys.stdout.write(out.decode(errors='replace').replace('\r', ''))
sys.exit(os.waitstatus_to_exitcode(st))
__PTY_PY__

cat >"$T/probe.py" <<'__PROBE_PY__'
# probe.py MODE HOST PORT CA: what a local user's client sees on a NATS port.
#   tcp    open a TCP connection, print "connected"
#   tls    read INFO, TLS handshake (CA from the file), send CONNECT without
#          credentials and a PING, print what comes back
#   plain  read INFO, send CONNECT and a PING in plain text, print what
#          comes back
import json, socket, ssl, sys
mode, host, port, ca = sys.argv[1], sys.argv[2], int(sys.argv[3]), sys.argv[4]
try:
    s = socket.create_connection((host, port), timeout=5)
except OSError as e:
    print('connect-failed', e.__class__.__name__)
    sys.exit(0)
if mode == 'tcp':
    print('connected')
    sys.exit(0)
s.settimeout(5)
info = b''
while not info.endswith(b'\n'):
    d = s.recv(4096)
    if not d:
        break
    info += d
print('info-tls-required=%s' % ('"tls_required":true' in info.decode(
    errors='replace')))
if mode == 'tls':
    ctx = ssl.create_default_context(cafile=ca)
    try:
        s = ctx.wrap_socket(s, server_hostname='localhost')
    except (ssl.SSLError, OSError) as e:
        print('handshake-failed', e.__class__.__name__)
        sys.exit(0)
    print('handshake-ok')
    msg = b'CONNECT {"verbose":false,"pedantic":false,"tls_required":true,' \
        b'"protocol":1}\r\nPING\r\n'
else:
    msg = b'CONNECT {"verbose":false,"pedantic":false}\r\nPING\r\n'
try:
    s.sendall(msg)
except OSError:
    print('closed-on-send')
s.settimeout(3)
got = b''
try:
    while True:
        d = s.recv(4096)
        if not d:
            print('closed-by-server')
            break
        got += d
except (socket.timeout, OSError):
    pass
t = got.decode(errors='replace')
print('got-pong=%s' % ('PONG' in t))
print('got-ok=%s' % ('+OK' in t))
print('got-err=%s' % ('-ERR' in t))
print('got-authz=%s' % ('uthori' in t or 'uthentic' in t))
print('got-info=%s' % ('INFO' in t))
__PROBE_PY__

cat >"$T/bob.sh" <<'__BOB__'
#!/usr/bin/env bash
# Runs INSIDE the container as the SECOND OS USER (bob), who is not the
# owner of the setup. Reads /tmp/reach-info (written by alice's reach-up).
set -u
. /tmp/reach-info
PASSN=0
FAILN=0
pass() { printf 'PASS  %s\n' "$1"; PASSN=$((PASSN + 1)); }
fail() { printf 'FAIL  %s%s\n' "$1" "${2:+  ($2)}"; FAILN=$((FAILN + 1)); }
t() { if eval "$2"; then pass "$1"; else fail "$1" "$2"; fi; }
P() { python3 -I /tmp/reach-probe.py "$@" 2>&1; }
t "reach: this really is a second user, not alice" \
    '[ "$(id -un)" = bob ] && [ "$(id -u)" != "$ALICEUID" ]'
TGTS=("127.0.0.1 $PORT /tmp/reach-ca.pem")
for tgt in "${TGTS[@]}"; do
    # shellcheck disable=SC2086
    set -- $tgt
    lab="loopback-only server, 127.0.0.1:$PORT"
    r=$(P tcp "$1" "$2" "$3")
    t "reach: every local user can reach a loopback port: bob opens TCP to the $lab" \
        '[ "$r" = connected ]'
    r=$(P tls "$1" "$2" "$3")
    t "reach: bob's TLS handshake works ($lab) with the CA from a world-readable copy" \
        'printf "%s\n" "$r" | grep -q "^handshake-ok"'
    t "reach: bob cannot connect with NATS without credentials ($lab): error, no PONG" \
        'printf "%s\n" "$r" | grep -q "^got-err=True" &&
         printf "%s\n" "$r" | grep -q "^got-authz=True" &&
         printf "%s\n" "$r" | grep -q "^got-pong=False" &&
         printf "%s\n" "$r" | grep -q "^got-ok=False"'
    # The helper never enables plain text; no plain-text override exists, so
    # none is tested. nats-server announces tls_required in its plain INFO
    # line (the helper reads that line); a plain-text client gets no +OK and
    # no PONG, i.e. no session.
    r=$(P plain "$1" "$2" "$3")
    t "reach: plain text is refused ($lab): INFO says tls_required, no +OK, no PONG" \
        'printf "%s\n" "$r" | grep -q "^info-tls-required=True" &&
         printf "%s\n" "$r" | grep -q "^got-pong=False" &&
         printf "%s\n" "$r" | grep -q "^got-ok=False"'
done
r=$(ls "$DIR" 2>&1); rc=$?
t "reach: bob cannot list DIR (mode 0700)" \
    '[ $rc -ne 0 ] && printf "%s" "$r" | grep -q "Permission denied"'
for f in server.log mash-setup.json server.conf ca.key; do
    r=$(cat "$DIR/$f" 2>&1); rc=$?
    t "reach: bob cannot read DIR/$f" \
        '[ $rc -ne 0 ] && printf "%s" "$r" | grep -q "Permission denied" &&
         [ -z "$(printf "%s" "$r" | grep -v "Permission denied")" ]'
done
r=$(cat "$DIR/ca.pem" 2>&1); rc=$?
t "reach: bob cannot read DIR/ca.pem either (the world-readable copy is alice's)" \
    '[ $rc -ne 0 ]'
t "reach: bob cannot create a file in DIR" \
    '! touch "$DIR/bob-was-here" 2>/dev/null'
printf '\nSUMMARY: %s passed, %s failed\n' "$PASSN" "$FAILN"
[ "$FAILN" -eq 0 ]
__BOB__

cat >"$T/incont.sh" <<'__INCONT__'
#!/usr/bin/env bash
# Runs INSIDE the throwaway container as the normal user (alice). Usage:
#   incont.sh GROUP     GROUP is main | osrel
# Prints PASS/FAIL per case; the full transcript goes to $LOGF.
set -u
HELPER=/home/alice/h.sh
W=/home/alice/w
LOGF=/home/alice/transcript.txt
BINP=/home/alice/bin
PTY="python3 -I /home/alice/pty.py"
export PATH=$BINP:$PATH
mkdir -p "$W"
PASSN=0
FAILN=0
pass() { printf 'PASS  %s\n' "$1"; PASSN=$((PASSN + 1)); }
fail() { printf 'FAIL  %s%s\n' "$1" "${2:+  ($2)}"; FAILN=$((FAILN + 1)); }
note() { printf '\n### %s\n' "$*" >>"$LOGF"; }
run() {
    { printf '$'; printf ' %q' "$@"; printf '\n'; } >>"$LOGF"
    OUT=$("$@" 2>&1 </dev/null)
    RC=$?
    printf '%s\n[rc=%s]\n' "$OUT" "$RC" >>"$LOGF"
}
runpty() { # TYPED CMD...
    _ty=$1
    shift
    { printf '$ (on a pty, typing %q)' "$_ty"; printf ' %q' "$@"
      printf '\n'; } >>"$LOGF"
    OUT=$($PTY "$_ty" "$@" 2>&1)
    RC=$?
    printf '%s\n[rc=%s]\n' "$OUT" "$RC" >>"$LOGF"
}
# oldadopt FILE, oldinit FILE: SHIM for harness runs only.  The printed
# closing command is `mashctl adopt --from DIR`; the mashctl built from this
# tree does not have --from yet (nor --ca, nor a fingerprint prompt, and its
# adopt does not do init).  oldadopt rewrites `--from DIR` into the old
# long-flag form (reading DIR/mash-setup.json, as adopt --from will); the CA
# goes in MASH_TLS_CA, set by the caller.  oldinit prints the matching
# `mashctl init`.  Neither is used to check what the helper prints.
oldparse() {
    _l=$(cat "$1")
    _l=${_l#"${_l%%[![:space:]]*}"}
    _p=''
    case $_l in MASH_CONFIG_DIR=*) _p="${_l%% mashctl adopt*} " ;; esac
    eval "set -- ${_l##*mashctl adopt --from }"
    _d=$1
    _j=$(cat "$_d/mash-setup.json")
}
oldfield() { printf '%s' "$_j" | sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p"; }
oldadopt() {
    oldparse "$1"
    printf '%smashctl adopt --server %q --owner %q --account %q' "$_p" \
        "$(oldfield server)" "$(oldfield owner)" "$(oldfield account)"
    printf ' --operator-key %q --account-key %q' \
        "$_d/export/operator.nk" "$_d/export/account.nk"
    printf ' --sys-creds %q --admin-creds %q\n' \
        "$_d/export/sys.creds" "$_d/export/mashadmin.creds"
}
oldinit() { oldparse "$1"; printf '%smashctl init\n' "$_p"; }
runpipe() { # INPUT CMD...: stdin is a pipe, stdout captured
    _in=$1
    shift
    { printf '$ printf %q |' "$_in"; printf ' %q' "$@"; printf '\n'; } \
        >>"$LOGF"
    OUT=$(printf '%s' "$_in" | "$@" 2>&1)
    RC=$?
    printf '%s\n[rc=%s]\n' "$OUT" "$RC" >>"$LOGF"
}
has() { # wrap-insensitive: whitespace squeezed on both sides
    local _ho _hn
    _ho=$(printf "%s" "$OUT" | tr -s " \n" "  ")
    _hn=$(printf "%s" "$1" | tr -s " \n" "  ")
    [[ $_ho == *"$_hn"* ]]
}
hasany() { # succeeds if $OUT holds any of the arguments
    local _w
    for _w in "$@"; do has "$_w" && return 0; done
    return 1
}
t() { # NAME CONDITION(eval)
    if eval "$2"; then pass "$1"; else fail "$1" "$2"; fi
}
RES="A process running as you can still swap a path between the recheck"
RES="$RES and the write; this script does not defend against that."
# the README URL line is one unbroken line and may exceed 80
wide() {
    printf '%s\n' "$OUT" | grep -v 'README.md#quick-start' |
        awk 'length > 80' | grep -c . || true
}
GONE="--(backup|upgrade|enable-boot|disable-boot|retire-keys|linger|host"
GONE="$GONE|listen)|retire|systemd"
PORT=4300
np() { PORT=$((PORT + 1)); }
SEED=SUAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAB
JWTC=eyJhbGciOiJlZDI1NTE5LW5rZXkifQ
PEMC=MIIFAKEPEMBODYMARKERQQ
canary_text() {
    printf '%s\n' "seed line $SEED" "token $JWTC.eyJqdGkiOiJYWFhYWA.SIG" \
        '-----BEGIN PRIVATE KEY-----' "$PEMC" '-----END PRIVATE KEY-----' \
        'password=hunter2'
}
# leaks DIR: how many canary markers are in $OUT or in any file under DIR
leaks() {
    _n=0
    for m in "$SEED" "$JWTC" "$PEMC" hunter2; do
        has "$m" && _n=$((_n + 1))
        if [ -d "$1" ] && grep -rlaF --exclude-dir=bin ${LEAKSKIP:+--exclude=$LEAKSKIP} -- "$m" "$1" >/dev/null 2>&1; then
            _n=$((_n + 1))
        fi
    done
    echo "$_n"
}
ssl() { # ss -ltn to stdout, and into the transcript
    { printf '\n$ ss -ltn\n'; ss -ltn; } | tee -a "$LOGF"
}
nlisten() { ss -ltnH | grep -c LISTEN || true; }
H() { "$HELPER" "$@"; }
# snap DIR: a digest of every path, size and mode under DIR (not jetstream/)
snap() { (cd "$1" && find . ! -path './jetstream*' -printf '%p %s %m\n' |
    sort | md5sum); }

group_main() {
cd /home/alice || exit 1
# ----------------------------------------------------------------- help
note "help and usage"
run "$HELPER" --help
t "help: rc 0, <= 80 columns, no emoji" '[ $RC -eq 0 ] && [ "$(wide)" = 0 ]'
t "help: the modes, remove's terminal rule and exit 4, root refusal, residual" \
    'has "nats-local-setup.sh --dir DIR --start | --stop | --check |
        --remove" &&
     has "Alone: start an existing server; it never creates one." &&
     has "--remove         stops the server recorded in DIR/server.pid and
        removes DIR; you type DIR at a terminal to confirm, and with no
        terminal it refuses and exits 4" &&
     has "The server listens on 127.0.0.1 only" && has "not root" &&
     has "the log is kept at most 7 days" && has "$RES"'
OPTS="--dir --owner --mash-config --port --account --operator --js-disk"
OPTS="$OPTS --start --stop --check --remove --help "
t "help: lists exactly the kept options, no others" \
    '[ "$(printf "%s\n" "$OUT" | grep -oE "^  --[a-z-]+" | tr -d " " |
        tr "\n" " ")" = "$OPTS" ]'
t "help: names no removed option" \
    '! hasany --backup --upgrade --retire-keys --keys-retired --enable-boot \
        --disable-boot --linger --host --listen systemd retire'
t "help: --owner is optional, defaults to the login name, checked by later modes" \
    'has "nats-local-setup.sh [create] --dir DIR [--owner NAME] [options]" &&
     has "(default: your login name; create asks on a terminal)" &&
     has "it must equal the owner in DIR/mash-setup.json"'
mkdir -p "$W/idshim"
printf '%s\n' '#!/bin/bash' 'case "$1" in -un) echo Bad.Login;;' \
    '*) exec /usr/bin/id "$@";; esac' >"$W/idshim/id"
chmod +x "$W/idshim/id"
OWNALLOW="lower-case letters, digits and single hyphens, starting with a letter, at most 32 characters"
run env PATH="$W/idshim:$PATH" "$HELPER" --dir "$W/x1"
t "owner: no --owner, no terminal, invalid login: exit 2, hint, nothing made" \
    '[ $RC -eq 2 ] && has "no --owner given and your login name '"'"'Bad.Login'"'"'
        is not a valid owner name (allowed: $OWNALLOW). Pass --owner NAME." &&
     [ ! -e $W/x1 ]'
runpty "owner:|" env PATH="$W/idshim:$PATH" "$HELPER" --dir "$W/x1"
t "owner: invalid login on a terminal, empty answers: says why, asks twice, refuses" \
    '[ $RC -eq 2 ] && has "Your login name '"'"'Bad.Login'"'"' cannot be used as an
        owner name (allowed: $OWNALLOW)." &&
     has "Owner name (the first part of every address): " &&
     has "Try once more." && has "Pass --owner NAME." && [ ! -e $W/x1 ]'
run "$HELPER" --dir "$W/x1" --port 4999 --stop
t "create options with a subcommand refused" \
    '[ $RC -ne 0 ] && has "apply to create only" && [ ! -e $W/x1 ]'
run "$HELPER" --dir "$W/x1" --stop --check
t "two subcommands refused" '[ $RC -ne 0 ] && has "one subcommand"'
run "$HELPER" --dir "$W/x1" --owner alice --yes-expose
t "F1: --yes-expose no longer exists" \
    '[ $RC -ne 0 ] && has "unknown argument: --yes-expose"'
for o in "--backup $W/x.tgz" --upgrade "--retire-keys $W/x.tgz" \
    --enable-boot --disable-boot --linger "--host box.example" \
    "--listen 0.0.0.0"; do
    # shellcheck disable=SC2086
    run "$HELPER" --dir "$W/x1" $o
    t "removed option '$o': unknown argument, usage, exit 2, nothing made" \
        '[ $RC -eq 2 ] && has "usage: nats-local-setup.sh" &&
         has "unknown argument: ${o%% *}" && [ ! -e $W/x1 ]'
done
run "$HELPER" --dir "$W/x1" --owner alice --listen ::1 --start
t "removed option --listen with create options: exit 2, nothing made" \
    '[ $RC -eq 2 ] && has "unknown argument: --listen" && [ ! -e $W/x1 ]'

# ---------------------------------------------- platform and prerequisites
note "platform and prerequisites"
mkdir -p "$W/sh-uname" "$W/sh-darwin" "$W/nocurl"
printf '%s\n' '#!/bin/bash' 'case "$1" in -m) echo aarch64;;' \
    '-s) echo Linux;; *) exec /usr/bin/uname "$@";; esac' \
    >"$W/sh-uname/uname"
printf '%s\n' '#!/bin/bash' 'case "$1" in -s) echo Darwin;;' \
    '*) exec /usr/bin/uname "$@";; esac' >"$W/sh-darwin/uname"
chmod +x "$W/sh-uname/uname" "$W/sh-darwin/uname"
run env PATH="$W/sh-uname:$PATH" "$HELPER" --dir "$W/pl1" --owner alice
t "arm64 refused with a clear message, nothing created" \
    '[ $RC -ne 0 ] && has "x86-64" && has "aarch64" && [ ! -e $W/pl1 ]'
run env PATH="$W/sh-darwin:$PATH" "$HELPER" --dir "$W/pl1" --owner alice
t "non-Linux refused" '[ $RC -ne 0 ] && has "Darwin" && [ ! -e $W/pl1 ]'
for f in /usr/bin/*; do
    case ${f##*/} in curl | unzip) ;; *) ln -sf "$f" "$W/nocurl/${f##*/}" ;;
    esac
done
run env PATH="$W/nocurl" /usr/bin/bash "$HELPER" --dir "$W/pl2" --owner alice
t "missing prerequisites listed with the apt line, nothing created" \
    '[ $RC -ne 0 ] && has "curl unzip" && has "apt-get install -y curl unzip" \
     && [ ! -e $W/pl2 ]'

# -------------------------------------------------- happy path (real tools)
note "create --start, default listen (real download of pinned tools)"
D=$W/happy
run "$HELPER" --dir "$D" --owner alice --start
HAPPY_OUT=$OUT
printf '%s\n' "$OUT" >"$W/happy.out"
{ printf '\n$ cat %s/server.conf (JWTs replaced)\n' "$D"
  sed -E 's/eyJ[A-Za-z0-9_.-]+/<JWT>/g' "$D/server.conf"; } >>"$LOGF"
t "create --start rc 0" '[ $RC -eq 0 ]'
t "(d) digest of each download printed and matched" \
    'has "downloaded nats-server-v2.15.1-linux-amd64.tar.gz sha256" &&
     has bf5edc7b1ce98e885c368cb43a886eb148dc5d0551d99f3cf6141fb5e1954c70 &&
     has 7d55eda757dc9f233675a3038fcf8779bcda99753b1603f7009fc8537e126b7e'
t "tools report the pinned versions" \
    'has "nats-server reports '"'"'nats-server: v2.15.1'"'"'" &&
     has "nsc reports '"'"'nsc version 2.15.0'"'"'"'
t "create output <= 80 columns" '[ "$(wide)" = 0 ]'
t "no seed, JWT or PEM in the output" \
    '! grep -Eq "(^|[^A-Z2-7])S[OANUCX][A-Z2-7]{54,}|eyJ[A-Za-z0-9_-]{10,}|BEGIN" \
        <<<"$OUT"'
t "no temp files left, DIR 700" \
    '[ -z "$(find $D -name ".cmd.*")" ] && [ "$(stat -c %a $D)" = 700 ]'
t "every file 0600 (tools 0700, dirs 0700)" \
    '[ -z "$(find $D -type f ! -perm 600 ! -path "$D/bin/nsc" \
        ! -path "$D/bin/nats-server")" ] &&
     [ -z "$(find $D -type d ! -perm 700)" ]'
t "marker file present" '[ -f $D/.mash-nats-setup ]'
t "no 'not copied' wording" '! grep -qi "not copied" <<<"$OUT"'
ssl >"$W/ss-default.txt"
t "ss: only the client port is open (default listen)" \
    '[ "$(nlisten)" = 1 ] && grep -q "127.0.0.1:4222" $W/ss-default.txt'
run "$HELPER" --dir "$D" --check
t "--check passes, lists the one listening socket" \
    '[ $RC -eq 0 ] && has "listens on 127.0.0.1:4222 (the client port)" &&
     has "match their recorded sha256" && has "requires TLS"'
CHECK_OUT=$OUT
sed -E 's/eyJ[A-Za-z0-9_.-]+/<JWT>/g' "$D/server.conf" >"$W/server.conf.redacted"
t "server.conf has no monitoring port and quotes the paths" \
    '! grep -q "http" $D/server.conf && grep -q "cert_file: \"$D/server.pem\"" \
     $D/server.conf'
# the closing no longer prints adopt/init; the end-to-end check below builds
# the adopt command itself (harness shim) so the exports are still proven
note "closing output, then adopt of the exported files by the harness"
printf '    mashctl adopt --from %s\n' "$(printf %q "$D")" >"$W/adopt.cmd"
oldinit "$W/adopt.cmd" >"$W/init.cmd"
sed -n '/^Done\./,$p' "$W/happy.out" >"$W/happy.closing"
FPHEX=$(openssl x509 -in "$D/ca.pem" -noout -fingerprint -sha256 |
    sed 's/^[^=]*=//' | tr -d ':' | tr 'A-F' 'a-f')
FPGOT=$(awk '/^  CA fingerprint /{on=1; sub(/^  CA fingerprint +/,"")}
    /^  Server directory/{on=0} on{print}' "$W/happy.out" | tr -d ' \n')
t "closing output: CA fingerprint line equals the independent sha256" \
    '[ ${#FPHEX} -eq 64 ] && [ "$FPGOT" = "sha256:$FPHEX" ] &&
     [ "$(grep -c "sha256:" <<<"$FPGOT")" = 1 ]'
t "closing output: fingerprint is eight groups of eight hex digits" \
    'grep -Eq "sha256:[0-9a-f]{8}( [0-9a-f]{8}){7}" <<<"$(awk \
     "/^  CA fingerprint /{on=1} /^  Server directory/{on=0} on{print}" \
     $W/happy.out | sed -E "s/^ +//; s/^CA fingerprint +//" | tr "\n" " " |
     sed -E "s/ +/ /g")"'
t "closing output: no MASH_TLS_CA anywhere" \
    '! grep -q MASH_TLS_CA $W/happy.out'
t "closing output: no long adopt flags, no CA env" \
    '! grep -Eq -- "--(sys-creds|system-credentials|admin-creds|admin-credentials|operator-key|account-key)" \
        $W/happy.out &&
     ! grep -Eq "MASH_TLS_CA|MASH_CA" $W/happy.out'
t "closing output: hand-managed paragraph is gone" \
    '! tr -s " \n" " " <$W/happy.out | grep -qF "Hand-managed server"'
t "closing output: no separate init step" '! grep -q "mashctl init" $W/happy.out'
t "mash-setup.json: exists, mode 600, one exact line" \
    '[ -f $D/mash-setup.json ] && [ "$(stat -c %a $D/mash-setup.json)" = 600 ] &&
     [ "$(cat $D/mash-setup.json)" = \
      "{\"server\":\"tls://localhost:$(sed -n "s/^listen: .*:\([0-9]*\)\"\$/\1/p" \
        $D/server.conf)\",\"owner\":\"alice\",\"account\":\"MASH\"}" ]'
t "mash-setup.json: no secrets, no fingerprint" \
    '! grep -Eq "S[OANUCX][A-Z2-7]{40,}|eyJ|BEGIN|sha256|$FPHEX" $D/mash-setup.json'
t "closing output: per-file list replaced by one line" \
    'grep -qxF "Everything is in $D; run --check to list it." $W/happy.out &&
     ! grep -q "^Files (all under" $W/happy.out &&
     ! grep -q "export/operator.nk" $W/happy.out'
t "step 1 and the box print the owner back" \
    'tr -s " \n" " " <$W/happy.out | grep -qF \
        "owner alice (the first part of every address)" &&
     grep -Eq "^  Owner +alice$" $W/happy.out'
t "closing output: no seed or credential content printed" \
    '! grep -Eq "(^|[^A-Z2-7])S[OANUCX][A-Z2-7]{54,}|eyJ[A-Za-z0-9_-]{10,}|BEGIN" \
        $W/happy.out'
t "closing output: removed paragraphs are gone" \
    '! grep -q "Or, after adopt succeeds" $W/happy.out &&
     ! tr -s " \n" " " <$W/happy.out | grep -q "a credential carries no" &&
     ! grep -q "Looking after the server later" $W/happy.out &&
     ! grep -Eq -- "$GONE" $W/happy.out'
t "closing output: other-commands line, Done and race sentences" \
    'has_n() { tr -s " \n" " " <$W/happy.out | grep -qF -- "$1"; }
     grep -qxF "Other commands: --stop, --start, --check, --remove, --help." \
        $W/happy.out &&
     has_n "Done. The setup is in" &&
     has_n "does not limit it to you" &&
     has_n "A process running as you can still swap a path"'
t "closing output: the box says ca.key and the nsc store stay in DIR" \
    'has_n() { tr -s " \n" " " <$W/happy.out | grep -qF -- "$1"; }
     has_n "(mode 0700. ca.key and the nsc store stay in it: a process" &&
     has_n "running as you can read them, and no step here moves them)" &&
     has_n "Binding 127.0.0.1 keeps the server off the network but does not" &&
     ! has_n "EXPOSED" && ! has_n "Exposed"'
t "closing output: fingerprint wording kept, Next is the README step 1 only" \
    'has_n() { tr -s " \n" " " <$W/happy.out | grep -qF -- "$1"; }
     has_n "The CA fingerprint is what a person on ANOTHER machine types to" &&
     has_n "trust this server; this machine does not ask for it." &&
     [ "$(sed -n "/^Next/,\$p" $W/happy.closing | head -n 4)" = "Next: follow the README quick start, step 1:
https://github.com/bogworks/mash/blob/v0.5.6/README.md#quick-start-one-machine-no-account

Other commands: --stop, --start, --check, --remove, --help." ] &&
     [ "$(grep -c "^Next" $W/happy.closing)" = 1 ] &&
     ! grep -q "^Next$" $W/happy.closing'
t "closing output: no mashctl, adopt, plugin, config-dir or placeholder text" \
    '! grep -Eqi "mashctl|adopt|plugin|MASH_CONFIG_DIR|creds|install mash|<[a-z]+>" \
        $W/happy.closing &&
     ! grep -q "Start the server first" $W/happy.closing'
t "closing output is short (<= 45 lines from Done. on)" \
    '[ "$(sed -n "/^Done\./,\$p" $W/happy.out | wc -l)" -le 45 ]'
t "adopt and init commands built for the harness" \
    '[ -s $W/adopt.cmd ] && [ "$(grep -c . $W/init.cmd)" = 1 ]'
run env -i HOME=/home/alice PATH="$BINP:/usr/bin:/bin" \
    MASH_TLS_CA="$D/ca.pem" bash -c "$(oldadopt "$W/adopt.cmd")"
t "harness 'mashctl adopt' of the exports succeeds in a fresh bash -c" \
    '[ $RC -eq 0 ] && has "adopt"'
run env -i HOME=/home/alice PATH="$BINP:/usr/bin:/bin" bash -c \
    "$(cat "$W/init.cmd")"
t "harness 'mashctl init' succeeds in a fresh bash -c" '[ $RC -eq 0 ]'
run env -i HOME=/home/alice PATH="$BINP:/usr/bin:/bin" \
    mashctl endpoint create alice/dev/design --make-key
run env -i HOME=/home/alice PATH="$BINP:/usr/bin:/bin" \
    mashctl endpoint create alice/dev/review --make-key
run env -i HOME=/home/alice PATH="$BINP:/usr/bin:/bin" MASH_OWNER=alice \
    MASH_GROUP=dev MASH_NAME=design mash send alice.dev.review "Hello Review"
t "mail sent through the helper-made server" '[ $RC -eq 0 ]'
run env -i HOME=/home/alice PATH="$BINP:/usr/bin:/bin" MASH_OWNER=alice \
    MASH_GROUP=dev MASH_NAME=review mash inbox
t "mail arrives (Hello Review)" 'has "Hello Review"'
t "agents' creds file written in mashctl's folder" \
    '[ -f /home/alice/.config/mash/alice.dev.design.creds ]'
note "--stop"
run "$HELPER" --dir "$D" --stop
t "--stop stops the recorded pid" '[ $RC -eq 0 ] && has "stopped pid"'
run "$HELPER" --dir "$D" --stop
t "second --stop: stale pid left alone, rc 0" '[ $RC -eq 0 ] && has "stale"'
note "--start on an existing setup"
run "$HELPER" --dir "$D" --start
t "--start brings it back, TLS answers" \
    '[ $RC -eq 0 ] && has "requires TLS"'
run "$HELPER" --dir "$D" --start
t "--start refuses when this dir already has a server" \
    '[ $RC -ne 0 ] && has "already running"'
run "$HELPER" --dir "$D" --stop
note "--start never creates"
run "$HELPER" --dir "$W/nocreate" --start
t "bare --start on a missing dir: exit 1, refusal text, nothing created" \
    '[ $RC -eq 1 ] && has "$W/nocreate not found; --start never creates a
        server. Create it with:" && has "--dir $W/nocreate --owner NAME
        --start" && [ ! -e $W/nocreate ] && [ "$(wide)" = 0 ]'
run "$HELPER" --dir "$W/nocreate" --owner alice --port 4390 --start
t "--owner (a create option) with --start on a missing dir still creates" \
    '[ $RC -eq 0 ] && [ -f $W/nocreate/server.conf ] &&
     [ -f $W/nocreate/.mash-nats-setup ]'
run "$HELPER" --dir "$W/nocreate" --stop
run "$HELPER" --dir "$W/nocreate" --start
t "--start on the now existing dir starts it" \
    '[ $RC -eq 0 ] && has "requires TLS"'
run "$HELPER" --dir "$W/nocreate" --stop

# ------------------------------------------------- create without --start
note "create without --owner and without a terminal uses the login name"
np
D=$W/nostart
run "$HELPER" --dir "$D" --port $PORT --mash-config "$W/mcfg"
printf '%s\n' "$OUT" >"$W/nostart.out"
sed -n '/^Done\./,$p' "$W/nostart.out" >"$W/nostart.closing"
t "default owner, no terminal: rc 0, no prompt, login name alice used" \
    '[ $RC -eq 0 ] && ! has "Owner name (" &&
     has "owner alice (the first part of every address)" &&
     grep -Eq "^  Owner +alice$" $W/nostart.out &&
     [ "$(cat $D/mash-setup.json)" = "{\"server\":\"tls://localhost:$PORT\",\"owner\":\"alice\",\"account\":\"MASH\"}" ]'
t "create without --start rc 0, offers the start command" \
    '[ $RC -eq 0 ] && has "Start the server first" && has "--start"'
t "the printed start command has no placeholder and runs as printed" \
    '! grep -q "<[a-z]*>" $W/nostart.out &&
     grep -qxF "  $(printf %q "$HELPER") --dir $D --start" $W/nostart.closing'
t "closing: Next after the start block; no mashctl, adopt, MASH_CONFIG_DIR" \
    '[ "$(grep -c "^Next: follow the README quick start, step 1:$" \
        $W/nostart.closing)" = 1 ] &&
     ! grep -Eqi "mashctl|adopt|plugin|MASH_CONFIG_DIR|creds" \
        $W/nostart.closing &&
     [ "$(sed -n "/^Start the server first/,\$p" $W/nostart.closing | wc -l)" \
        -le 12 ]'
t "mash-config dir is not created by the helper" '[ ! -e $W/mcfg ]'
note "create asks for the owner once on a terminal"
np
D=$W/askowner
APORT=$PORT
runpty "owner:carol-2" "$HELPER" --dir "$D" --port $PORT
printf '%s\n' "$OUT" >"$W/askowner.out"
t "owner: pty prompt shows the login default; a typed answer is used" \
    '[ $RC -eq 0 ] &&
     has "Owner name (the first part of every address) [alice]: " &&
     has "owner carol-2 (the first part of every address)" &&
     grep -Eq "^  Owner +carol-2$" $W/askowner.out &&
     grep -q "\"owner\":\"carol-2\"" $D/mash-setup.json'
np
D=$W/askowner2
runpty "owner:" "$HELPER" --dir "$D" --port $PORT
t "owner: an empty answer on a terminal takes the login name" \
    '[ $RC -eq 0 ] && has "[alice]: " &&
     has "owner alice (the first part of every address)" &&
     grep -q "\"owner\":\"alice\"" $D/mash-setup.json'
D=$W/askbad
runpty "owner:Bad_Name|ALICE" "$HELPER" --dir "$D"
t "owner: two bad typed answers refused (exit 2), what is allowed, nothing made" \
    '[ $RC -eq 2 ] && has "is not a valid owner name. Allowed: $OWNALLOW." &&
     has "Try once more." && has "Pass --owner NAME." &&
     [ "$(printf "%s" "$OUT" | grep -c "Owner name (")" = 2 ] && [ ! -e $D ]'
runpty "owner:Bad_Name|dave" "$HELPER" --dir "$D" --port $PORT
t "owner: a bad answer then a good one is accepted (asked once more)" \
    '[ $RC -eq 0 ] && has "Try once more." &&
     has "owner dave (the first part of every address)"'
runpty "owner:" "$HELPER" --dir "$W/askbad2" --owner zed --port $PORT
t "owner: with --owner given the person is not asked" \
    '! has "Owner name (" && [ $RC -eq 0 ] && grep -q "\"owner\":\"zed\"" \
        $W/askbad2/mash-setup.json'
note "later modes: --owner must equal the recorded owner"
D=$W/askowner
SNAP=$(snap "$D")
for m in --check --stop --start; do
    run "$HELPER" --dir "$D" --owner alice $m
    t "owner mismatch $m: refused first, exit 1, nothing changed" \
        '[ $RC -eq 1 ] && has "owner alice differs from the owner this
            directory was made with (carol-2); nothing was changed" &&
         [ "$(snap "$D")" = "$SNAP" ]'
done
runpty "$D" "$HELPER" --dir "$D" --owner alice --remove
t "owner mismatch --remove: refused before any prompt, nothing removed" \
    '[ $RC -eq 1 ] && has "owner alice differs from the owner this directory
        was made with (carol-2); nothing was changed" &&
     ! has "Type this exactly" && [ "$(snap "$D")" = "$SNAP" ]'
run "$HELPER" --dir "$D" --owner carol-2 --check
t "owner match --check: proceeds as normal" \
    '[ $RC -eq 0 ] && ! has "differs"'
run "$HELPER" --dir "$D" --owner carol-2 --start
t "owner match --start (existing dir): starts the server" \
    '[ $RC -eq 0 ] && has "requires TLS" && ! has "differs"'
run "$HELPER" --dir "$D" --owner alice --stop
t "owner mismatch --stop with a running server: refused, server still up" \
    '[ $RC -eq 1 ] && has "differs from the owner" &&
     ss -ltnH | grep -q "127.0.0.1:$APORT"'
run "$HELPER" --dir "$D" --owner carol-2 --stop
t "owner match --stop with a running server: stops it" \
    '[ $RC -eq 0 ] && has "stopped pid"'
run "$HELPER" --dir "$D" --owner Bad_Name --check
t "owner given to a later mode is only compared, never validated as new" \
    '[ $RC -eq 1 ] && has "differs from the owner"'
run "$HELPER" --dir "$D" --owner carol-2 --port 4999 --check
t "--port stays create-only for later modes" \
    '[ $RC -ne 0 ] && has "apply to create only"'
mkdir -p "$W/noowner/d"
run "$HELPER" --dir "$W/noowner/d" --owner alice --check
t "owner given but the directory records none: refused, not a made setup" \
    '[ $RC -ne 0 ] && has "has no owner"'
OUT=$(cat "$W/nostart.out")
t "other commands line printed, no removed option anywhere" \
    'grep -qxF "Other commands: --stop, --start, --check, --remove, --help." \
        $W/nostart.out &&
     ! hasany --backup --upgrade --retire-keys --enable-boot --disable-boot \
        --linger --host --listen retire systemd'
# the adopt --from line is one unbroken line: a long DIR may exceed 80
t "closing output <= 80 columns (but the adopt line)" \
    '[ "$(printf "%s\n" "$OUT" | grep -v "mashctl adopt --from\|README.md#" |
        awk "length > 80" | grep -c .)" = 0 ]'
t "S2: closing output states the residual race" 'has "$RES"'
note "arguments"
for o in Alice 'a--b' '1abc' 'a-' 'abcdefghijklmnopqrstuvwxyz0123456789'; do
    run "$HELPER" --dir "$W/ow" --owner "$o"
    t "owner '$o' refused" '[ $RC -eq 2 ] && has "--owner" && [ ! -e $W/ow ]'
done
run "$HELPER" --dir relative/dir/x --owner alice
t "relative --dir refused" '[ $RC -ne 0 ] && has "absolute"'
run "$HELPER" --dir "$W/x/../y/z" --owner alice
t "--dir with .. refused" '[ $RC -ne 0 ]'
run "$HELPER" --dir "$W/mc" --owner alice --mash-config '/home/alice/a"b'
t "--mash-config validated like --dir" '[ $RC -ne 0 ] && has "--mash-config"'

# ------------------------------------------------------------- F4 chars
note "F4: conf-significant characters in --dir"
before=$(ls -A /home/alice | md5sum)
for spec in 'dq|/home/alice/a"b' "sq|/home/alice/a'b" 'bs|/home/alice/a\b' \
    'dollar|/home/alice/a$b' 'bt|/home/alice/a`b' 'nl|/home/alice/a
b' 'lb|/home/alice/a{b' 'rb|/home/alice/a}b' 'hash|/home/alice/a#b' \
    'tab|/home/alice/a	b' 'esc|/home/alice/a'$'\033''b'; do
    nm=${spec%%|*}
    d=${spec#*|}
    run "$HELPER" --dir "$d" --owner alice
    t "F4: --dir with $nm refused before anything is written" \
        '[ $RC -ne 0 ] && { has "must not contain" ; } &&
         [ "$(ls -A /home/alice | md5sum)" = "$before" ]'
done
note "F4: a directory with a space works end to end"
np
D='/home/alice/m ash'
run "$HELPER" --dir "$D" --owner alice --port $PORT --start
t "F4: --dir with a space: create --start rc 0" '[ $RC -eq 0 ]'
t "F4: server.conf quotes the spaced path and nats-server -t parses it" \
    'grep -q "cert_file: \"/home/alice/m ash/server.pem\"" "$D/server.conf" &&
     "$D/bin/nats-server" -t -c "$D/server.conf" 2>&1 | grep -q valid'
run "$HELPER" --dir "$D" --check
t "F4: --check passes for the spaced dir" '[ $RC -eq 0 ]'
t "F4: printed paths quote the space with %q" \
    'grep -qF "Done. The setup is in /home/alice/m\\ ash." "$LOGF"'
run "$HELPER" --dir "$D" --stop

# ------------------------------------------------------------- F2 paths
note "F2: path safety"
run "$HELPER" --dir /tmp/mashx/m1 --owner alice
t "F2: /tmp as a parent refused, tells you to use your home" \
    '[ $RC -ne 0 ] && has "writable by group or others" && has "under your" &&
     [ ! -e /tmp/mashx ]'
mkdir -m 775 "$W/p775"
run "$HELPER" --dir "$W/p775/m1" --owner alice
t "F2: group-writable parent refused" \
    '[ $RC -ne 0 ] && has "writable by group or others" && [ ! -e $W/p775/m1 ]'
mkdir "$W/p1777"
chmod 1777 "$W/p1777"
run "$HELPER" --dir "$W/p1777/m1" --owner alice
t "F2: sticky world-writable parent refused too" \
    '[ $RC -ne 0 ] && has "mode 1777" && [ ! -e $W/p1777/m1 ]'
mkdir -p "$W/realp"
ln -s "$W/realp" "$W/linkp"
run "$HELPER" --dir "$W/linkp/m1" --owner alice
t "F2: symlinked component refused, nothing written to the target" \
    '[ $RC -ne 0 ] && has "symlink" && [ -z "$(ls -A $W/realp)" ]'
mkdir "$W/emptyd"
run "$HELPER" --dir "$W/emptyd" --owner alice
t "F2: existing (empty) directory refused" \
    '[ $RC -ne 0 ] && has "already exists"'
run "$HELPER" --dir "$W/nonexist/m1" --owner alice
t "F2: missing parent refused" '[ $RC -ne 0 ]'
# swapped path: a mkdir shim that sleeps, swaps the parent, then mkdirs
mkdir -p "$W/sh-mkdir"
cat >"$W/sh-mkdir/mkdir" <<'SH'
#!/bin/bash
# test shim: on the Nth mkdir, swap the parent for a symlink to $EVIL
n=$(cat /home/alice/w/swapcount 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" >/home/alice/w/swapcount
if [ "$n" = "${SWAPN:-1}" ]; then
    sleep 1
    mv "$SWAPP" "$SWAPP.orig"
    ln -s "$EVIL" "$SWAPP"
fi
exec /usr/bin/mkdir "$@"
SH
chmod +x "$W/sh-mkdir/mkdir"
for n in 1 2; do
    rm -rf "$W/swp"
    mkdir -p "$W/swp/parent" "$W/swp/evil"
    echo 0 >"$W/swapcount"
    run env PATH="$W/sh-mkdir:$PATH" SWAPN=$n SWAPP="$W/swp/parent" \
        EVIL="$W/swp/evil" "$HELPER" --dir "$W/swp/parent/m1" --owner alice \
        --port 4399
    t "F2: parent swapped for a symlink during mkdir #$n: nothing written" \
        '[ -z "$(ls -A $W/swp/evil)" ]'
    t "F2: swap #$n: helper refused (rc!=0) and said why" \
        '[ $RC -ne 0 ] && { has "replaced" || has "no longer leads"; }'
    t "F2: swap #$n: original directory holds no setup files" \
        '[ -z "$(find $W/swp/parent.orig -type f 2>/dev/null | grep -v \
            .mash-nats-setup | grep -v ^$ | head -n 1 | grep -E "ca.key|server.key|creds|\.nk")" ]'
    note "swap #$n leftover: $(find "$W/swp" | head -n 20 | tr '\n' ' ')"
done
rm -rf "$W/swp"

# ----------------------------------------------------------- F3 downloads
note "F3: pinned digests, tampered download, hostile PATH"
np
mkdir -p "$W/tamper/nats-server-v2.15.1-linux-amd64"
printf '%s\n' '#!/bin/bash' 'echo ran >/home/alice/w/CANARY-tamper' \
    >"$W/tamper/nats-server-v2.15.1-linux-amd64/nats-server"
chmod +x "$W/tamper/nats-server-v2.15.1-linux-amd64/nats-server"
tar -C "$W/tamper" -czf "$W/tamper.tgz" nats-server-v2.15.1-linux-amd64
mkdir -p "$W/sh-curl"
printf '%s\n' '#!/bin/bash' 'case "$*" in' \
    '*nats-server*) cat /home/alice/w/tamper.tgz ;;' \
    '*) cat /home/alice/w/tamper.tgz ;; esac' >"$W/sh-curl/curl"
chmod +x "$W/sh-curl/curl"
D=$W/tampered
run env PATH="$W/sh-curl:$PATH" "$HELPER" --dir "$D" --owner alice \
    --port $PORT
t "F3(b): tampered tarball refused before extraction" \
    '[ $RC -ne 0 ] && has "pins bf5edc7b" && has "Nothing" &&
     has "was extracted or executed"'
t "F3(b): nothing extracted, nothing executed, no tool installed" \
    '[ ! -e $W/CANARY-tamper ] && [ ! -e $D/bin/nats-server ] &&
     [ ! -e $D/bin/.dl/x ] && [ -z "$(find $D/bin -type f)" ]'
t "F3(b): the digest of what was downloaded is printed" \
    'has "downloaded nats-server-v2.15.1-linux-amd64.tar.gz sha256 "'
mkdir -p "$W/evil"
for tool in nsc nats-server; do
    printf '%s\n' '#!/bin/bash' "echo \"$tool ran: \$*\" >>/home/alice/w/CANARY-path" \
        'exit 1' >"$W/evil/$tool"
    chmod +x "$W/evil/$tool"
done
np
D=$W/hostile
run env PATH="$W/evil:$PATH" "$HELPER" --dir "$D" --owner alice \
    --port $PORT --start
t "F3(c): malicious nsc and nats-server first on PATH: create still works" \
    '[ $RC -eq 0 ]'
t "F3(c): the canary file never appears (nothing on PATH was run)" \
    '[ ! -e $W/CANARY-path ]'
t "F3(c): output mentions the PATH copies only as never run" \
    'has "never run"'
run env PATH="$W/evil:$PATH" "$HELPER" --dir "$D" --check
t "F3(c): --check with the hostile PATH: ok, canary absent" \
    '[ $RC -eq 0 ] && [ ! -e $W/CANARY-path ]'
run env PATH="$W/evil:$PATH" "$HELPER" --dir "$D" --stop
run env PATH="$W/evil:$PATH" "$HELPER" --dir "$D" --start
t "F3(c): --stop/--start with the hostile PATH, canary absent" \
    '[ $RC -eq 0 ] && [ ! -e $W/CANARY-path ]'
run "$HELPER" --dir "$D" --stop
note "F3: DIR/bin tampering is detected and never executed"
cp "$D/bin/nats-server" "$W/ns.good"
printf '%s\n' '#!/bin/bash' 'echo ran >>/home/alice/w/CANARY-bin' \
    >"$D/bin/nats-server.tmp"
mv -f "$D/bin/nats-server.tmp" "$D/bin/nats-server"
chmod 700 "$D/bin/nats-server"
run "$HELPER" --dir "$D" --check
t "F3: replaced nats-server: --check FAIL on its digest, not executed" \
    '[ $RC -ne 0 ] && has "nats-server sha256" && [ ! -e $W/CANARY-bin ]'
run "$HELPER" --dir "$D" --start
t "F3: --start refuses a replaced nats-server, not executed" \
    '[ $RC -ne 0 ] && has "Nothing was run" && [ ! -e $W/CANARY-bin ]'
cat "$W/ns.good" >"$D/bin/nats-server.tmp"
chmod 700 "$D/bin/nats-server.tmp"
mv -f "$D/bin/nats-server.tmp" "$D/bin/nats-server"
touch "$D/bin/extra-tool"
run "$HELPER" --dir "$D" --check
t "F3: an unexpected file in bin: --check says what it found" \
    '[ $RC -ne 0 ] && has "unexpected extra-tool in bin"'
mv "$D/bin/extra-tool" "$W/extra-tool.moved"
run "$HELPER" --dir "$D" --check
t "F3: after removing the stray file --check passes again" '[ $RC -eq 0 ]'

# ------------------------------------------------------------ F5 canaries
note "F5: canary secrets from every subprocess"
FK=$W/fk
mkdir -p "$FK/nsroot/nats-server-v2.15.1-linux-amd64" "$FK/sh"
# fake nats-server and fake nsc: behaviour chosen by a mode file
cat >"$FK/nsroot/nats-server-v2.15.1-linux-amd64/nats-server" <<SH
#!/bin/bash
canary() {
    printf '%s\n' "seed line $SEED" "token $JWTC.eyJqdGkiOiJYWFhYWA.SIG" \\
        '-----BEGIN PRIVATE KEY-----' "$PEMC" '-----END PRIVATE KEY-----' \\
        'password=hunter2'
}
m=\$(cat /home/alice/w/fk/mode-ns 2>/dev/null)
case "\$1" in
-v) echo "nats-server: v2.15.1"; [ "\$m" = ver ] && canary; exit 0 ;;
-t) [ "\$m" = ft ] && { canary; canary >&2; exit 1; }
    echo "configuration is valid"; exit 0 ;;
-c) [ "\$m" = frun ] && { canary; canary >&2; exit 1; }; exit 0 ;;
esac
exit 0
SH
chmod +x "$FK/nsroot/nats-server-v2.15.1-linux-amd64/nats-server"
tar -C "$FK/nsroot" -czf "$FK/ns.tgz" nats-server-v2.15.1-linux-amd64
mkdir -p "$FK/nscroot"
cat >"$FK/nscroot/nsc" <<SH
#!/bin/bash
canary() {
    printf '%s\n' "seed line $SEED" "token $JWTC.eyJqdGkiOiJYWFhYWA.SIG" \\
        '-----BEGIN PRIVATE KEY-----' "$PEMC" '-----END PRIVATE KEY-----' \\
        'password=hunter2'
}
m=\$(cat /home/alice/w/fk/mode-nsc 2>/dev/null)
case "\$1" in
--version) echo "nsc version 2.15.0"; [ "\$m" = ver ] && canary; exit 0 ;;
esac
canary; canary >&2
exit 1
SH
chmod +x "$FK/nscroot/nsc"
(cd "$FK/nscroot" && python3 -I -c "
import zipfile
z = zipfile.ZipFile('$FK/nsc.zip', 'w')
z.write('nsc')
z.close()")
NSSHA=$(sha256sum <"$FK/ns.tgz" | cut -d' ' -f1)
NSCSHA=$(sha256sum <"$FK/nsc.zip" | cut -d' ' -f1)
# the helper under test, with ONLY the pins and URLs changed
mkfk() { # NAME FAKE_NS(0|1) FAKE_NSC(0|1)
    cp "$HELPER" "$FK/$1.sh"
    if [ "$2" = 1 ]; then
        sed -i -e "s#^NS_BASE=.*#NS_BASE=https://fake.invalid/ns#" \
            -e "s#^NS_SHA=.*#NS_SHA=$NSSHA#" "$FK/$1.sh"
    fi
    if [ "$3" = 1 ]; then
        sed -i -e "s#^NSC_BASE=.*#NSC_BASE=https://fake.invalid/nsc#" \
            -e "s#^NSC_SHA=.*#NSC_SHA=$NSCSHA#" "$FK/$1.sh"
    fi
    chmod +x "$FK/$1.sh"
}
# shims for the system tools; each fails with canaries when its mode file
# says so, otherwise it runs the real tool
for tool in curl tar unzip openssl; do
    real=$(command -v "$tool")
    cat >"$FK/sh/$tool" <<SH
#!/bin/bash
canary() {
    printf '%s\n' "seed line $SEED" "token $JWTC.eyJqdGkiOiJYWFhYWA.SIG" \\
        '-----BEGIN PRIVATE KEY-----' "$PEMC" '-----END PRIVATE KEY-----' \\
        'password=hunter2'
}
m=\$(cat /home/alice/w/fk/mode-$tool 2>/dev/null)
if [ "$tool" = curl ]; then
    case "\$*" in
    *fake.invalid/ns/*) cat /home/alice/w/fk/ns.tgz; exit 0 ;;
    *fake.invalid/nsc/*) cat /home/alice/w/fk/nsc.zip; exit 0 ;;
    esac
fi
if [ "\$m" = fail ] && [ "\$1" != version ]; then
    canary; canary >&2; exit 2
fi
if [ "\$m" = ver ] && [ "\$1" = version ]; then
    echo "OpenSSL 3.0.0 1 Jan 2000"; canary; exit 0
fi
exec $real "\$@"
SH
    chmod +x "$FK/sh/$tool"
done
setmodes() { # tool=mode ...
    rm -f "$FK"/mode-*
    for kv in "$@"; do printf '%s' "${kv#*=}" >"$FK/mode-${kv%%=*}"; done
}
fcase() { # LABEL FAKE_NS FAKE_NSC EXTRA_ARGS... (modes set before)
    _l=$1; _a=$2; _b=$3
    shift 3
    np
    mkfk "h-$_l" "$_a" "$_b"
    D=$W/f5-$_l
    run env PATH="$FK/sh:$PATH" "$FK/h-$_l.sh" --dir "$D" --owner alice \
        --port $PORT "$@"
}
fclean() { # leaks and a kept-file scan
    _x=$(leaks "$D")
    t "F5 $1: no canary in stdout/stderr or any file under DIR" '[ "$_x" = 0 ]'
}
setmodes nsc=f
fcase nsc-fail 0 1
t "F5 nsc (add operator) fails, rc!=0, says its output is not kept" \
    '[ $RC -ne 0 ] && has "withheld" && has "is not kept"'
fclean "nsc subcommand failure"
setmodes nsc=ver
fcase nsc-version 0 1
t "F5 nsc --version output with canaries: refused" '[ $RC -ne 0 ]'
fclean "nsc version output"
setmodes ns=ft
fcase ns-t 1 0
t "F5 nats-server -t fails at create, rc!=0, withheld note" \
    '[ $RC -ne 0 ] && has "withheld"'
fclean "nats-server -t failure"
setmodes ns=ver
fcase ns-version 1 0
t "F5 nats-server -v with canaries: refused" '[ $RC -ne 0 ]'
fclean "nats-server version output"
setmodes ns=frun
fcase ns-run 1 0
D=$W/f5-ns-run
t "F5 create without --start ok with the fake nats-server" '[ $RC -eq 0 ]'
canary_text >>"$D/server.log"
SEEDED=$(wc -l <"$D/server.log")
run env PATH="$FK/sh:$PATH" "$FK/h-ns-run.sh" --dir "$D" --start
t "F5 --start failure tail: rc!=0, server exited" \
    '[ $RC -ne 0 ] && has "server exited"'
# the start rotates the seeded server.log to server.log.1 (skipped here, as
# the seeded file was); the failing server's own server.log is now checked
LEAKSKIP=server.log.1 fclean "--start failure with canaries in server.log (seeded)"
SEEDED=0
t "F5 server.log: nothing written after the seed holds a canary" \
    '[ "$(tail -n +$((SEEDED + 1)) $D/server.log | grep -c -E "$SEED|$JWTC|$PEMC|hunter2")" = 0 ]'
t "S3: what the failing server wrote to server.log is only withheld lines" \
    '[ "$(tail -n +$((SEEDED + 1)) $D/server.log | grep -c .)" -ge 1 ] &&
     [ "$(tail -n +$((SEEDED + 1)) $D/server.log |
        grep -vc "^<line withheld>$")" = 0 ] && has "<line withheld>"'
run env PATH="$FK/sh:$PATH" "$FK/h-ns-run.sh" --dir "$D" --check
t "F5 --check on that setup (fake binary has its own digest): no canary" \
    '[ "$(LEAKSKIP=server.log.1 leaks "$D")" = 0 ]'
setmodes tar=fail
fcase tar 0 0
t "F5 tar failing with canaries: rc!=0" '[ $RC -ne 0 ] && has "tar extract"'
fclean "tar"
setmodes unzip=fail
fcase unzip 0 0
t "F5 unzip failing with canaries: rc!=0" '[ $RC -ne 0 ] && has "unzip"'
fclean "unzip"
setmodes curl=fail
fcase curl 0 0
t "F5 curl failing with canaries: rc!=0" '[ $RC -ne 0 ] && has "download"'
fclean "curl"
setmodes openssl=fail
fcase openssl 0 0
t "F5 openssl failing with canaries (CA step): rc!=0" '[ $RC -ne 0 ]'
fclean "openssl"
setmodes openssl=ver
fcase openssl-ver 0 0
fclean "openssl version output"
BREAKRC=0
[ -z "${NATSSETUP_TEST_BREAK:-}" ] || BREAKRC=99
t "F5 openssl version output with canaries does not break create" \
    '[ $RC -eq $BREAKRC ] && [ -f $D/server.pem ] && [ -f $D/ca.key ]'
run "$HELPER" --dir "$W/f5-openssl-ver" --stop
rm -f "$FK"/mode-*

# --------------------------------------------------- --check negatives
note "--check negatives, extra listening port"
np
D=$W/neg
run "$HELPER" --dir "$D" --owner alice --port $PORT --start
chmod 755 "$D/export"
chmod 644 "$D/export/sys.creds"
mv "$D/export/operator.nk" "$D/export/operator.nk.real"
ln -s "$D/export/operator.nk.real" "$D/export/operator.nk"
run "$HELPER" --dir "$D" --check
t "--check FAILs on modes and a symlinked seed" \
    '[ $RC -ne 0 ] && has "export mode 755" && has "sys.creds mode 644" &&
     has "operator.nk is a symlink"'
rm "$D/export/operator.nk"
mv "$D/export/operator.nk.real" "$D/export/operator.nk"
chmod 700 "$D/export"
chmod 600 "$D/export/sys.creds"
J=$D/mash-setup.json
cp "$J" "$W/neg-setup.json"
chmod 644 "$J"
run "$HELPER" --dir "$D" --check
t "--check FAILs on mash-setup.json mode 644" \
    '[ $RC -ne 0 ] && has "mash-setup.json mode 644"'
chmod 600 "$J"
printf '{"server":"tls://localhost:1","owner":"alice"\n' >|"$J"
run "$HELPER" --dir "$D" --check
t "--check FAILs on malformed mash-setup.json" \
    '[ $RC -ne 0 ] && has "mash-setup.json is not the one-line JSON"'
printf '{"server":"tls://localhost:1","owner":"alice","account":"MASH"}\n' \
    >|"$J"
run "$HELPER" --dir "$D" --check
t "--check FAILs when mash-setup.json names another port" \
    '[ $RC -ne 0 ] && has "mash-setup.json server port"'
rm "$J"
run "$HELPER" --dir "$D" --check
t "--check FAILs when mash-setup.json is missing" \
    '[ $RC -ne 0 ] && has "missing $D/mash-setup.json"'
cp "$W/neg-setup.json" "$J"
chmod 600 "$J"
run "$HELPER" --dir "$D" --stop
printf 'http: 127.0.0.1:%s\n' $((PORT + 1)) >>"$D/server.conf"
run "$HELPER" --dir "$D" --start
run "$HELPER" --dir "$D" --check
t "--check FAILs when anything besides the client port is open (monitoring)" \
    '[ $RC -ne 0 ] && has "also listens on 127.0.0.1:$((PORT + 1))"'
ssl >"$W/ss-extra.txt"
run "$HELPER" --dir "$D" --stop
run "$HELPER" --dir "$W/x/not-ours" --check
t "--check on a missing dir refused" '[ $RC -ne 0 ]'
mkdir "$W/nomark"
touch "$W/nomark/a-file" "$W/nomark/b-file"
run "$HELPER" --dir "$W/nomark" --check
t "--check on a dir without the marker FAILs" '[ $RC -ne 0 ] && has "missing $W/nomark/.mash-nats-setup"'

# ----------------------------------------------- server.log rotation (7d)
note "server.log: rotated on every start, nothing older than 7 days kept"
np
D=$W/lg
mkdir -p "$W/lgout"
echo "outside one" >"$W/lgout/server.log.1"
echo "outside two" >"$W/lgout/server.log.old"
touch -d '30 days ago' "$W/lgout/server.log.1" "$W/lgout/server.log.old"
OUTSIDE=$(cd "$W/lgout" && find . -printf '%p %s %m %T@\n' | sort |
    md5sum)
OUTSHA=$(sha256sum "$W/lgout"/* | md5sum)
run "$HELPER" --dir "$D" --owner alice --port $PORT --start
t "log: create --start makes server.log (0600), no .1 yet" \
    '[ $RC -eq 0 ] && [ "$(stat -c %a $D/server.log)" = 600 ] &&
     [ ! -e $D/server.log.1 ] &&
     ! has "Files (all under"'
run "$HELPER" --dir "$D" --stop
echo "first-run marker" >>"$D/server.log"
LOG1=$(sha256sum <"$D/server.log")
echo "old three" >"$D/server.log.3"
chmod 600 "$D/server.log.3"
touch -d '8 days ago' "$D/server.log.3"
ln -s "$W/lgout/server.log.1" "$D/server.log.9"
echo "old file, name starts with server.log" >"$D/server.logfile"
touch -d '8 days ago' "$D/server.logfile"
echo "not a log" >"$D/notes.txt"
touch -d '30 days ago' "$D/notes.txt"
run "$HELPER" --dir "$D" --start
t "log: start renames server.log to server.log.1 (same content, 0600)" \
    '[ $RC -eq 0 ] && [ "$(sha256sum <$D/server.log.1)" = "$LOG1" ] &&
     [ "$(stat -c %a $D/server.log.1)" = 600 ] &&
     ! grep -q "first-run marker" $D/server.log'
t "log: the new server.log is 0600 and fresh" \
    '[ "$(stat -c %a $D/server.log)" = 600 ] &&
     grep -q "Server is ready" $D/server.log'
t "log: a server.log* file older than 7 days is gone" \
    '[ ! -e $D/server.log.3 ] && [ ! -e $D/server.logfile ]'
t "log: a symlink named server.log* is not followed or removed, nor is a file that is not server.log*" \
    '[ -L $D/server.log.9 ] && [ -f $D/notes.txt ] &&
     [ "$(cat $W/lgout/server.log.1)" = "outside one" ]'
t "log: nothing outside DIR was touched (list, size, mode, mtime, sha256)" \
    '[ "$(cd $W/lgout && find . -printf "%p %s %m %T@\n" | sort | md5sum)" = "$OUTSIDE" ] &&
     [ "$(sha256sum $W/lgout/* | md5sum)" = "$OUTSHA" ]'
run "$HELPER" --dir "$D" --stop
echo "second-run marker" >>"$D/server.log"
touch -d '8 days ago' "$D/server.log"
run "$HELPER" --dir "$D" --start
t "log: a server.log older than 7 days is rotated and then deleted" \
    '[ $RC -eq 0 ] && ! grep -rq "second-run marker" $D/server.log* &&
     [ "$(stat -c %a $D/server.log)" = 600 ]'
t "log: the aged log is gone, no .1 left, only the new server.log" \
    '[ ! -e $D/server.log.1 ] &&
     [ "$(cd $D && ls -A | grep "^server.log" | tr "\n" " ")" = "server.log server.log.9 " ]'
run "$HELPER" --dir "$D" --stop

# --------------------------------------------------------------- --remove
note "--remove"
mkdir -p "$W/cfgkeep"
echo keep >"$W/cfgkeep/keep.txt"
np
D=$W/rm
run "$HELPER" --dir "$D" --owner alice --port $PORT --start --mash-config \
    "$W/cfgkeep"
run "$HELPER" --dir "$W/nomark" --remove
t "--remove refuses a dir without the marker and says what it found" \
    '[ $RC -ne 0 ] && has "no .mash-nats-setup marker" && has "a-file" &&
     [ -f $W/nomark/a-file ]'
BEFORE=$(snap "$D")
run "$HELPER" --dir "$D" --remove
t "--remove with no terminal: exit 4, message, command, no change" \
    '[ $RC -eq 4 ] && has "--remove needs a person at a terminal" &&
     has "nothing was changed" && has "Run this yourself" &&
     has "$HELPER --dir $D --remove" && ! has "Type this exactly" &&
     [ -f $D/ca.key ] && [ -d $D/nsc ] && [ "$(snap $D)" = "$BEFORE" ] &&
     [ "$(nlisten)" -ge 1 ]'
runpipe "$D
" "$HELPER" --dir "$D" --remove
t "--remove: the path typed on a pipe does not count: exit 4, no change" \
    '[ $RC -eq 4 ] && [ -d $D ] && [ "$(snap $D)" = "$BEFORE" ] &&
     [ "$(nlisten)" -ge 1 ]'
TYPED="nope"
runpty "$TYPED" "$HELPER" --dir "$D" --remove
t "--remove wrong text on a terminal: refused, nothing removed" \
    '[ $RC -ne 0 ] && has "not the confirmation" && [ -d $D ] &&
     [ -f $D/ca.key ] && [ "$(snap $D)" = "$BEFORE" ] && [ "$(nlisten)" -ge 1 ]'
t "names: --remove prompt says it removes DIR, stops the server, and the race" \
    'has "--remove removes $D with everything in it" &&
     has "it stops the server first" && has "$RES" &&
     ! has "unit file" && ! has "retire"'
TYPED="$D"
runpty "$TYPED" "$HELPER" --dir "$D" --remove --mash-config "$W/cfgkeep"
t "--remove typed path on a terminal: server stopped, DIR gone" \
    '[ $RC -eq 0 ] && [ ! -e $D ] && has "removed $D" && has "stopped pid" &&
     ! has "retire"'
t "--remove prints that mashctl's folder is untouched and its command" \
    'has "mashctl'"'"'s folder was not touched" && has "rm -r -- $W/cfgkeep"'
t "--remove did not touch the mashctl folder" '[ -f $W/cfgkeep/keep.txt ]'
t "--remove: nothing listens any more" '[ "$(nlisten)" = 0 ]'
}

group_sec() {
cd /home/alice || exit 1
S=$W/sec
mkdir -p "$S"
note "sec: a base setup to copy (real pinned downloads)"
np
BASE=$S/base
run "$HELPER" --dir "$BASE" --owner alice --port $PORT
t "sec: base setup created" '[ $RC -eq 0 ] && [ -f $BASE/ca.key ]'
# ------------------------------------------------ S3: the server log
note "S3: a real server start gives a readable log"
run "$HELPER" --dir "$BASE" --start
t "S3: real start rc 0" '[ $RC -eq 0 ]'
t "S3: server.log is readable: the usual lines are all there" \
    'grep -q "Starting nats-server" $BASE/server.log &&
     grep -q "Version:  2.15.1" $BASE/server.log &&
     grep -q "ID:       N" $BASE/server.log &&
     grep -q "Trusted Operators" $BASE/server.log &&
     grep -q "JETSTREAM" $BASE/server.log &&
     grep -q "Store Directory" $BASE/server.log &&
     grep -q "Listening for client connections" $BASE/server.log &&
     grep -q "Server is ready" $BASE/server.log'
t "S3: a normal start withholds no line" \
    '[ "$(grep -c "<line withheld>" $BASE/server.log)" = 0 ] &&
     [ "$(grep -c . $BASE/server.log)" -ge 15 ]'
run "$HELPER" --dir "$BASE" --stop
t "S3: --stop rc 0" '[ $RC -eq 0 ]'
awk '/^srvlog_ok\(\) \{/,/^}/' "$HELPER" >"$S/flt.sh"
awk '/^srvlog_filter\(\) \{/,/^}/' "$HELPER" >>"$S/flt.sh"
filt() { ( CONF=$BASE/server.conf; DIR=$BASE; . "$S/flt.sh"; srvlog_filter ); }
t "S3: the filter passes every line of a real server.log unchanged" \
    'filt <"$BASE/server.log" | cmp -s - "$BASE/server.log"'
{
    canary_text
    printf '%s\n' "[1234] 2026/11/19 10:00:00.123456 [INF] seed $SEED" \
        "[1234] 2026/11/19 10:00:00.123456 [INF] Leafnode token $JWTC" \
        "[1234] 2026/11/19 10:00:00.123456 [ERR] password=hunter2" \
        "[1234] 2026/11/19 10:00:00.123456 [INF] a line of an unknown shape" \
        "$PEMC" "[1234] 2026/11/19 10:00:00.123456 [DBG] anything"
    while IFS= read -r l; do
        printf '%s\n' "$l $SEED" "$l $JWTC.x.y" "$l password=hunter2" \
            "$l$PEMC" "$l"$'\x01'
    done <"$BASE/server.log"
} >"$S/can.txt"
filt <"$S/can.txt" >"$S/can.out"
t "S3: canaries of unknown shape and appended to allowed shapes: all withheld" \
    '[ "$(grep -c . $S/can.txt)" = "$(grep -c . $S/can.out)" ] &&
     [ "$(grep -vc "^<line withheld>$" $S/can.out)" = 0 ] &&
     [ "$(grep -c . $S/can.out)" -ge 80 ]'
t "S3: no canary marker survives the filter" \
    '[ -s $S/can.out ] && ! grep -qaE "$SEED|$JWTC|$PEMC|hunter2" $S/can.out'
t "S3: the allowlist is documented in a comment" \
    'grep -q "^# srvlog_ok LINE: succeeds only if LINE is exactly one" \
        "$HELPER"'
# ----------------------------------- S2: swapped paths (stat shim)
note "S2: swapped paths, recheck by device:inode"
mkdir -p "$S/sh-stat"
cat >"$S/sh-stat/stat" <<'SH'
#!/bin/bash
# test shim: on the SWAPN-th "stat -c %d:%i -- SWAPP" (the helper records
# the device:inode once and compares it again before each write), replace
# SWAPP with the directory EVIL, as another process running as you could.
if [ "${1:-}" = -c ] && [ "${2:-}" = %d:%i ] &&
    [ "${4:-}" = "${SWAPP:-/nonexistent}" ]; then
    n=$(cat "$SWAPCNT" 2>/dev/null || echo 0)
    n=$((n + 1))
    echo "$n" >"$SWAPCNT"
    if [ "$n" = "$SWAPN" ]; then
        mv "$SWAPP" "$SWAPP.orig"
        mv "$EVIL" "$SWAPP"
    fi
fi
exec /usr/bin/stat "$@"
SH
chmod +x "$S/sh-stat/stat"
mkevil() { # fresh EVIL directory holding one canary file
    rm -rf "$S/evil"
    mkdir -m 700 "$S/evil"
    echo "do not touch" >"$S/evil/canary"
    EVILSHA=$(sha256sum <"$S/evil/canary")
    EVILLIST=$(cd "$S/evil" && find . -mindepth 1 | sort)
}
cat >"$S/swapenv" <<SH
#!/bin/bash
# swapenv N SWAPP CMD...: run CMD with the stat shim that swaps SWAPP
echo 0 >"$S/cnt"
n=\$1; p=\$2
shift 2
exec env PATH="$S/sh-stat:\$PATH" SWAPCNT="$S/cnt" SWAPN=\$n SWAPP="\$p" \\
    EVIL="$S/evil" "\$@"
SH
chmod +x "$S/swapenv"
untouched() { # SWAPP now is EVIL: only the canary, mode 0700
    [ "$(ls -A "$1")" = canary ] && [ "$(stat -c %a "$1")" = 700 ] &&
        [ "$(cat "$1/canary")" = "do not touch" ]
}
# evil_exact DIR: the unintended destination (the directory swapped in) holds
# exactly what the test put there (the full listing, hidden files included,
# and the sentinel's sha256) and nothing else: no helper file, key, archive,
# temp file or directory landed in it
evil_exact() {
    [ "$(cd "$1" && find . -mindepth 1 | sort)" = "$EVILLIST" ] &&
        [ "$(sha256sum <"$1/canary")" = "$EVILSHA" ] &&
        [ -z "$(find "$1" -mindepth 1 ! -path "$1/canary")" ]
}
swapped() { [ -d "$1.orig" ]; }
refused() { has "replaced" || has "no longer leads"; }
for n in 2 3; do
    mkevil
    rm -rf "$S/cpar" "$S/cpar.orig"
    mkdir -m 700 "$S/cpar"
    np
    echo 0 >"$S/cnt"
    run "$S/swapenv" $n "$S/cpar" "$HELPER" --dir "$S/cpar/m1" --owner alice \
        --port $PORT
    note "S2 creation #$n: stat calls on the parent: $(cat "$S/cnt")"
    t "S2: creation, parent swapped at recheck #$n: the swap really happened" \
        'swapped $S/cpar'
    # #3 lands on the record taken after mkdir (not a recheck): the helper
    # still stops, because the new parent has no m1, but without the message
    t "S2: creation, swap #$n: aborts (names device:inode at #2)" \
        '[ $RC -ne 0 ] && { [ $n = 3 ] || { refused && has "device:inode"; }; }'
    t "S2: creation, swap #$n: the replacement is untouched" \
        'untouched $S/cpar'
    t "S2: creation, swap #$n: the unintended destination holds exactly the
        sentinel (listing, sha256): no helper file, key or archive" \
        'evil_exact $S/cpar'
done
for n in 2 3; do
    mkevil
    rm -rf "$S/rpar" "$S/rpar.orig"
    mkdir -m 700 "$S/rpar"
    cp -a "$BASE" "$S/rpar/d"
    TYPED="$S/rpar/d"
    runpty "$TYPED" "$S/swapenv" $n "$S/rpar" "$HELPER" --dir "$S/rpar/d" \
        --remove
    note "S2 remove #$n: stat calls: $(cat "$S/cnt")"
    t "S2: --remove, a parent swapped at recheck #$n: the swap happened" \
        'swapped $S/rpar'
    t "S2: --remove swap #$n: aborts, names device:inode" \
        '[ $RC -ne 0 ] && refused && has "device:inode"'
    t "S2: --remove swap #$n: the unintended destination holds exactly the
        sentinel (listing, sha256): nothing was removed into or from it" \
        'evil_exact $S/rpar'
    t "S2: --remove swap #$n: replacement untouched, original DIR not removed" \
        'untouched $S/rpar && [ -f $S/rpar.orig/d/.mash-nats-setup ] &&
         [ -f $S/rpar.orig/d/ca.key ] && [ -f $S/rpar.orig/d/server.conf ]'
done

}

group_root() {
cd /root || exit 1
note "root: every subcommand refuses, nothing is created or read"
R=/root/rt-h5
MSG="run as your own user, not root; sudo would make root-owned keys"
before=$(find /root -xdev 2>/dev/null | sort | md5sum)
t "root: this really is uid 0" '[ "$(id -u)" = 0 ]'
for a in "--owner alice" "--owner alice --start" --start --stop --check \
    --remove; do
    # shellcheck disable=SC2086
    run "$HELPER" --dir "$R/d" $a
    t "S7: as root, '$a': refused with the fixed message, exit != 0" \
        '[ $RC -ne 0 ] && has "$MSG"'
    t "S7: as root, '$a': nothing created" \
        '[ ! -e $R ] &&
         [ "$(find /root -xdev 2>/dev/null | sort | md5sum)" = "$before" ]'
done
runpty "$R/d" "$HELPER" --dir "$R/d" --remove
t "S7: as root on a terminal: still refused before any prompt" \
    '[ $RC -ne 0 ] && has "$MSG" && ! has "Type this exactly"'
}

group_osrel() {
cd /home/alice || exit 1
note "not Debian/Ubuntu"
run "$HELPER" --dir "$W/os1" --owner alice
t "non-Debian /etc/os-release (fedora): refused, nothing created" \
    '[ $RC -ne 0 ] && has "Debian or Ubuntu only" && [ ! -e $W/os1 ]'
}


group_reach_up() {
cd /home/alice || exit 1
PORT=4700
# bob must be able to walk to DIR so that only DIR's own mode (0700) keeps
# him out, not the home directory's
chmod 755 /home/alice
note "reach: a loopback-only setup"
D=$W/reach
run "$HELPER" --dir "$D" --owner alice --port $PORT --start
t "reach: loopback setup created and running" \
    '[ $RC -eq 0 ] && has "requires TLS"'
t "reach: DIR is 0700, server.log and mash-setup.json are 0600" \
    '[ "$(stat -c %a $D)" = 700 ] && [ "$(stat -c %a $D/server.log)" = 600 ] &&
     [ "$(stat -c %a $D/mash-setup.json)" = 600 ]'
t "reach: the closing text says every user on this machine can reach the port" \
    'has "Binding 127.0.0.1 keeps the server off the network but does not
        limit it to you: every user on this machine can reach port $PORT.
        They cannot connect without credentials, read mail or make agents."'
t "reach: only 127.0.0.1:$PORT listens for it" \
    'ss -ltnH | grep -q "127.0.0.1:$PORT" &&
     ! ss -ltnH | grep -qE "(0.0.0.0|\*):$PORT"'
cp "$D/ca.pem" /tmp/reach-ca.pem
chmod 644 /tmp/reach-ca.pem
{
    printf 'DIR=%q\nPORT=%q\nALICEUID=%q\n' "$D" "$PORT" "$(id -u)"
} >/tmp/reach-info
chmod 644 /tmp/reach-info
}
group_reach_down() {
cd /home/alice || exit 1
D=$W/reach
run "$HELPER" --dir "$D" --check
t "reach: after bob, the loopback setup still checks clean" '[ $RC -eq 0 ]'
t "reach: bob left no file in DIR" '[ ! -e $D/bob-was-here ]'
run "$HELPER" --dir "$D" --stop
t "reach: loopback setup stopped" '[ $RC -eq 0 ]'
}

case ${1:-main} in
reach-up) group_reach_up ;;
reach-down) group_reach_down ;;
main) group_main ;;
sec) group_sec ;;
root) group_root ;;
osrel) group_osrel ;;
esac
printf '\nSUMMARY: %s passed, %s failed\n' "$PASSN" "$FAILN"
[ "$FAILN" -eq 0 ]
__INCONT__

cat >"$T/ctx1/Containerfile" <<'__CF1__'
FROM docker.io/library/debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends curl tar unzip openssl iproute2 ca-certificates python3 && rm -rf /var/lib/apt/lists/*
RUN useradd -m -u 1000 -s /bin/bash alice
__CF1__

# $1 container, $2 user: put the helper, binaries and test files in place
stage() {
    podman exec "$1" mkdir -p /home/alice/bin
    podman cp "$HELPER_SRC" "$1:/home/alice/h.sh"
    podman cp "$T/bin/mashctl" "$1:/home/alice/bin/mashctl"
    podman cp "$T/bin/mash" "$1:/home/alice/bin/mash"
    podman cp "$T/pty.py" "$1:/home/alice/pty.py"
    podman cp "$T/incont.sh" "$1:/home/alice/incont.sh"
    podman exec --user root "$1" chown -R alice:alice /home/alice
}

# $1 label, then the command: run, keep PASS/FAIL lines, count them
report() {
    local label=$1 f=$T/out/$1.out
    shift
    "$@" >"$f" 2>&1
    grep -E '^(PASS|FAIL)' "$f"
    grep -v -E '^(PASS|FAIL|SUMMARY|     )' "$f" | grep -v '^$' |
        sed "s/^/    [$label] /"
    TOTAL_PASS=$((TOTAL_PASS + $(grep -c '^PASS' "$f")))
    TOTAL_FAIL=$((TOTAL_FAIL + $(grep -c '^FAIL' "$f")))
}

# ----------------------------------------------------------------- main
if want main || want osrel || want sec || want root || want reach; then
    say "building the plain image $IMG1"
    podman build -q -t "$IMG1" "$T/ctx1" >/dev/null || exit 2
    BUILT_IMG1=1
    podman run -d --name "$C1" --user alice "$IMG1" sleep 7200 >/dev/null ||
        exit 2
    stage "$C1"
fi
if want main; then
    say "group main"
    report main podman exec ${NATSSETUP_TEST_BREAK:+-e NATSSETUP_TEST_BREAK=1} \
        "$C1" bash /home/alice/incont.sh main
    podman cp "$C1:/home/alice/transcript.txt" "$T/out/transcript-main.txt" \
        2>/dev/null
fi
if want sec; then
    say "group sec (path swaps, server-log allowlist)"
    report sec podman exec "$C1" bash /home/alice/incont.sh sec
    podman cp "$C1:/home/alice/transcript.txt" "$T/out/transcript-sec.txt" \
        2>/dev/null
fi
if want reach; then
    say "group reach (a second OS user, bob, made with useradd in the container)"
    podman exec --user root "$C1" useradd -m -s /bin/bash bob
    podman cp "$T/bob.sh" "$C1:/tmp/reach-bob.sh"
    podman cp "$T/probe.py" "$C1:/tmp/reach-probe.py"
    podman exec --user root "$C1" chmod 755 /tmp/reach-bob.sh
    podman exec --user root "$C1" chmod 644 /tmp/reach-probe.py
    report reach-up podman exec "$C1" bash /home/alice/incont.sh reach-up
    report reach-bob podman exec --user bob "$C1" bash /tmp/reach-bob.sh
    report reach-down podman exec "$C1" bash /home/alice/incont.sh reach-down
fi
if want root; then
    say "group root (the helper as uid 0 in the container)"
    report root podman exec --user root -e HOME=/root "$C1" \
        bash /home/alice/incont.sh root
fi
if want osrel; then
    say "group osrel (/etc/os-release says fedora, as root, then restored)"
    podman exec --user root "$C1" cp /etc/os-release /etc/os-release.orig
    podman exec --user root "$C1" sed -i 's/^ID=.*/ID=fedora/;/^ID_LIKE=/d' \
        /etc/os-release
    report osrel podman exec "$C1" bash /home/alice/incont.sh osrel
    podman exec --user root "$C1" mv /etc/os-release.orig /etc/os-release
fi


printf '\nTOTAL: %s passed, %s failed\n' "$TOTAL_PASS" "$TOTAL_FAIL"
[ "$TOTAL_FAIL" -eq 0 ]
