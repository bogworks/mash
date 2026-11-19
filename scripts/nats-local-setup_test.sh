#!/usr/bin/env bash
# Test harness for scripts/nats-local-setup.sh.  Run it with:
#
#     bash scripts/nats-local-setup_test.sh [main] [osrel] [sec] [reach] [root] [boot]
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
#   sec    path-swap rechecks (device:inode), the server-log allowlist,
#          --retire-keys archive verification (partial, truncated and
#          altered archives), and where each command writes
#   reach  a second OS user (useradd in the container): can reach the
#          listening port, cannot connect with NATS without credentials,
#          cannot read DIR, server.log or mash-setup.json
#   root   every subcommand refuses to run as root (podman exec --user root)
#   boot   --enable-boot / --disable-boot against a real systemd user manager
#          (systemd as PID 1 in the container), including a user-manager
#          restart that stands in for a reboot
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
C2=mash-natssetup-test-sd-$RUNID$SFX
IMG1=localhost/mash-natssetup-test-$RUNID$SFX
IMG2=localhost/mash-natssetup-test-sd-$RUNID$SFX
BUILT_IMG1=0
BUILT_IMG2=0
GROUPS_WANTED=${*:-main osrel sec reach root boot}
TOTAL_FAIL=0
TOTAL_PASS=0

cleanup() {
    # NATSSETUP_TEST_OUT=DIR keeps the transcripts (a new directory)
    if [ -n "${NATSSETUP_TEST_OUT:-}" ] && [ -d "$T/out" ] &&
        [ ! -e "$NATSSETUP_TEST_OUT" ]; then
        cp -r "$T/out" "$NATSSETUP_TEST_OUT"
    fi
    podman rm -f "$C1" >/dev/null 2>&1
    podman rm -f "$C2" >/dev/null 2>&1
    [ "$BUILT_IMG1" = 1 ] && podman rmi -f "$IMG1" >/dev/null 2>&1
    [ "$BUILT_IMG2" = 1 ] && podman rmi -f "$IMG2" >/dev/null 2>&1
    case $T in
    */natssetup-test.*) rm -rf -- "$T" ;;
    esac
}
trap cleanup EXIT

say() { printf '== %s\n' "$*"; }
want() { case " $GROUPS_WANTED " in *" $1 "*) return 0 ;; esac; return 1; }

# ---------------------------------------------------------------- build
say "building mashctl and mash (CGO_ENABLED=0) into $T/bin"
mkdir -p "$T/bin" "$T/gocache" "$T/ctx1" "$T/ctx2" "$T/out"
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
import os, pty, select, sys
typed, cmd = sys.argv[1], sys.argv[2:]
pid, fd = pty.fork()
if pid == 0:
    os.execvp(cmd[0], cmd)
out = b''
sent = False
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
TGTS=("127.0.0.1 $PORT /tmp/reach-ca.pem"
    "127.0.0.1 $PORT2 /tmp/reach-ca2.pem")
[ -z "$EXTIP" ] || TGTS+=("$EXTIP $PORT2 /tmp/reach-ca2.pem")
for tgt in "${TGTS[@]}"; do
    # shellcheck disable=SC2086
    set -- $tgt
    case $1:$2 in
    127.0.0.1:$PORT) lab="loopback-only server, 127.0.0.1:$PORT" ;;
    127.0.0.1:$PORT2) lab="non-loopback server, 127.0.0.1:$PORT2" ;;
    *) lab="non-loopback server, $1:$2" ;;
    esac
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
t "help: names where each command writes, root refusal, residual" \
    'has "--backup DEST writes DEST (mode 0600" && has "and nothing else" &&
     has "writes DEST (0600) and DIR/.keys-retired and removes ca.key,
        nsc/ and export/ from DIR" &&
     has "writes the systemd user unit file at its real path
        \$HOME/.config/systemd/user/<name> and enables it" &&
     has "--disable-boot   removes that unit file" &&
     has "--remove removes DIR (and the unit file, if this script
        made one)" && has "not root" && has "$RES"'
t "help: the log is kept at most 7 days, --remove needs --retire-keys first" \
    'has "the log is kept at most 7 days" &&
     has "refused until --retire-keys has run"'
run "$HELPER" --dir "$W/x1"
t "create without --owner refused" \
    '[ $RC -ne 0 ] && has "needs --owner" && [ ! -e $W/x1 ]'
run "$HELPER" --dir "$W/x1" --owner alice --stop
t "create options with a subcommand refused" '[ $RC -ne 0 ]'
run "$HELPER" --dir "$W/x1" --stop --check
t "two subcommands refused" '[ $RC -ne 0 ] && has "one subcommand"'
run "$HELPER" --dir "$W/x1" --owner alice --yes-expose
t "F1: --yes-expose no longer exists" \
    '[ $RC -ne 0 ] && has "unknown argument: --yes-expose"'

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
# the printed adopt and init commands, run unmodified in a fresh bash -c
note "closing output: printed adopt/init run unmodified"
grep -E '^    (MASH_CONFIG_DIR=[^ ]+ )?mashctl adopt --from ' \
    "$W/happy.out" >"$W/adopt.cmd" || true
oldinit "$W/adopt.cmd" >"$W/init.cmd"
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
t "closing output: exactly one mashctl adopt --from line, right dir" \
    '[ "$(grep -Ec "^ +(MASH_CONFIG_DIR=[^ ]+ )?mashctl adopt --from" \
        $W/happy.out)" = 1 ] &&
     [ "$(grep -c . $W/adopt.cmd)" = 1 ] &&
     [ "$(cat $W/adopt.cmd)" = "    mashctl adopt --from $(printf %q "$D")" ]'
t "closing output: no long adopt flags, no CA env" \
    '! grep -Eq -- "--(sys-creds|system-credentials|admin-creds|admin-credentials|operator-key|account-key)" \
        $W/happy.out &&
     ! grep -Eq "MASH_TLS_CA|MASH_CA" $W/happy.out &&
     ! grep -E "mashctl adopt --from" $W/adopt.cmd | grep -q -- "--ca "'
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
     ! grep -Eq -- "--(backup|upgrade|enable-boot|disable-boot|remove|retire-keys .*tar)" \
        $W/happy.out'
t "closing output: other-commands line, Done and race sentences" \
    'has_n() { tr -s " \n" " " <$W/happy.out | grep -qF -- "$1"; }
     grep -qxF "Other commands: --stop, --start, --check, --help." \
        $W/happy.out &&
     has_n "Done. The setup is in" &&
     has_n "does not limit it to you" &&
     has_n "A process running as you can still swap a path"'
t "closing output: new fingerprint and adopt-user wording, no typing prompt" \
    'has_n() { tr -s " \n" " " <$W/happy.out | grep -qF -- "$1"; }
     has_n "The CA fingerprint is what a person on ANOTHER machine types to" &&
     has_n "trust this server; this machine does not ask for it." &&
     has_n "Install mash and mashctl (part 4 of the guide), then adopt the" &&
     has_n "server into mash as the same user that ran this script (it does" &&
     has_n "not ask for the fingerprint and sets up mash" &&
     has_n "README quick start from its \"launch a session\" step:" &&
     grep -qxF "  https://github.com/bogworks/mash/blob/v0.5.6/README.md#quick-start-one-machine-no-account" \
        $W/happy.out &&
     ! has_n "claude plugin" && ! has_n "docs/install.md" &&
     ! has_n "types the CA fingerprint" && ! has_n "type the CA fingerprint" &&
     ! has_n "compare it with the CA fingerprint" &&
     ! has_n "in their own terminal"'
t "closing output is short (<= 45 lines from Done. on)" \
    '[ "$(sed -n "/^Done\./,\$p" $W/happy.out | wc -l)" -le 45 ]'
t "adopt and init commands found in the output" \
    '[ -s $W/adopt.cmd ] && [ "$(grep -c . $W/init.cmd)" = 1 ]'
run env -i HOME=/home/alice PATH="$BINP:/usr/bin:/bin" \
    MASH_TLS_CA="$D/ca.pem" bash -c "$(oldadopt "$W/adopt.cmd")"
t "printed 'mashctl adopt' succeeds in a fresh bash -c" \
    '[ $RC -eq 0 ] && has "adopt"'
run env -i HOME=/home/alice PATH="$BINP:/usr/bin:/bin" bash -c \
    "$(cat "$W/init.cmd")"
t "printed 'mashctl init' succeeds in a fresh bash -c" '[ $RC -eq 0 ]'
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
t "agents' creds file written where the closing text says" \
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

# ------------------------------------------------- create without --start
note "create without --start prints the start command"
np
D=$W/nostart
run "$HELPER" --dir "$D" --owner bob-1 --port $PORT --mash-config "$W/mcfg"
printf '%s\n' "$OUT" >"$W/nostart.out"
t "create without --start rc 0, offers the start command" \
    '[ $RC -eq 0 ] && has "Start the server first" && has "--start"'
t "--mash-config reaches the closing text and MASH_CONFIG_DIR" \
    'has "MASH_CONFIG_DIR=$W/mcfg mashctl adopt --from $W/nostart" &&
     has "mashctl'"'"'s folder ($W/mcfg; set MASH_CONFIG_DIR"'
t "mash-config dir is not created by the helper" '[ ! -e $W/mcfg ]'
t "closing text names the agents' creds file" \
    'has "bob-1.<group>.<agent>.creds" &&
     ! has "claude plugin marketplace add" &&
     ! has "docs/install.md"'
t "other commands line printed, long command list gone" \
    'has "Other commands: --stop, --start, --check, --help." &&
     for w in --backup --upgrade --enable-boot --disable-boot --remove; do
         ! has "$w" || exit 1; done'
# the adopt --from line is one unbroken line: a long DIR may exceed 80
t "closing output <= 80 columns (but the adopt line)" \
    '[ "$(printf "%s\n" "$OUT" | grep -v "mashctl adopt --from\|README.md#" |
        awk "length > 80" | grep -c .)" = 0 ]'
t "S2: closing output states the residual race" 'has "$RES"'
note "arguments"
for o in Alice 'a--b' '1abc' 'a-' 'abcdefghijklmnopqrstuvwxyz0123456789'; do
    run "$HELPER" --dir "$W/ow" --owner "$o"
    t "owner '$o' refused" '[ $RC -ne 0 ] && has "--owner" && [ ! -e $W/ow ]'
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
t "F4: printed commands quote the space with %q" \
    'grep -qF "mashctl adopt --from /home/alice/m\\ ash" "$LOGF"'
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

# --------------------------------------------------------- F1 exposure
note "F1: non-loopback listen needs a person typing the address"
np
D=$W/expo
run "$HELPER" --dir "$D" --owner alice --listen 0.0.0.0 --port $PORT
t "F1: piped (no terminal): refused, nothing created, prints the command" \
    '[ $RC -ne 0 ] && [ ! -e $D ] && has "Run this yourself" &&
     has "--listen 0.0.0.0" && has "$HELPER"'
run "$HELPER" --dir "$D" --owner alice --listen 0.0.0.0 --port $PORT --start
runpipe "0.0.0.0:$PORT
" "$HELPER" --dir "$D" --owner alice --listen 0.0.0.0 --port $PORT
t "F1: typed text on a pipe does not count" '[ $RC -ne 0 ] && [ ! -e $D ]'
TYPED="0.0.0.0:1"
runpty "$TYPED" "$HELPER" --dir "$D" --owner alice --listen 0.0.0.0 \
    --port $PORT --host box.example
t "F1: terminal but wrong text: refused, nothing created" \
    '[ $RC -ne 0 ] && [ ! -e $D ] && has "not the confirmation"'
t "F1: the exposure text names address, TLS, credentials, cert names" \
    'has "0.0.0.0:$PORT" && has "TLS is required" && has "NATS credentials" &&
     has "DNS:box.example"'
TYPED="__eof__"
runpty "$TYPED" "$HELPER" --dir "$D" --owner alice --listen 0.0.0.0 \
    --port $PORT
t "F1: terminal, no answer (EOF): refused, nothing created" \
    '[ $RC -ne 0 ] && [ ! -e $D ]'
TYPED="0.0.0.0:$PORT"
runpty "$TYPED" "$HELPER" --dir "$D" --owner alice --listen 0.0.0.0 \
    --port $PORT --host box.example --start
printf '%s\n' "$OUT" >"$W/expo.out"
t "F1: terminal and the exact address:port typed: created and started" \
    '[ $RC -eq 0 ] && grep -q "listen: \"0.0.0.0:$PORT\"" $D/server.conf'
t "F1: the typed-confirmation create printed the first host in the URL" \
    'has "tls://box.example:$PORT"'
ssl >"$W/ss-expose.txt"
t "ss: only the typed-confirmed port is open, on 0.0.0.0" \
    '[ "$(nlisten)" -ge 1 ] && grep -qE "(0.0.0.0|\*):$PORT" $W/ss-expose.txt &&
     [ "$(grep -cE "(0.0.0.0|\*):$PORT" $W/ss-expose.txt)" = 1 ]'
run "$HELPER" --dir "$D" --check
t "F1: --check on the exposed setup passes (one socket, client port)" \
    '[ $RC -eq 0 ] && has "listens on *:$PORT (the client port)"'
run "$HELPER" --dir "$D" --stop
note "IPv6 loopback"
np
D=$W/v6
run "$HELPER" --dir "$D" --owner alice --listen ::1 --port $PORT --start
t "--listen ::1 needs no confirmation, starts" \
    '[ $RC -eq 0 ] && grep -q "listen: \"\[::1\]:$PORT\"" $D/server.conf'
ssl >"$W/ss-v6.txt"
t "ss: only [::1]:PORT is open" \
    '[ "$(nlisten)" = 1 ] && grep -q "\[::1\]:$PORT" $W/ss-v6.txt'
run "$HELPER" --dir "$D" --check
t "--check passes on ::1" '[ $RC -eq 0 ] && has "(the client port)"'
run "$HELPER" --dir "$D" --stop

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
run env PATH="$W/evil:$PATH" "$HELPER" --dir "$D" --upgrade
t "F3(c): --upgrade (already current) with the hostile PATH, canary absent" \
    '[ $RC -eq 0 ] && has "already current" && [ ! -e $W/CANARY-path ]'
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
run "$HELPER" --dir "$D" --upgrade
t "F3: --upgrade refuses to overwrite a file it did not create" \
    '[ $RC -ne 0 ] && has "differs from the recorded" && [ ! -e $W/CANARY-bin ]'
cat "$W/ns.good" >"$D/bin/nats-server.tmp"
chmod 700 "$D/bin/nats-server.tmp"
mv -f "$D/bin/nats-server.tmp" "$D/bin/nats-server"
touch "$D/bin/extra-tool"
run "$HELPER" --dir "$D" --check
t "F3: an unexpected file in bin: --check says what it found" \
    '[ $RC -ne 0 ] && has "unexpected extra-tool in bin"'
run "$HELPER" --dir "$D" --upgrade
t "F3: --upgrade stops and says what it found" \
    '[ $RC -ne 0 ] && has "unexpected extra-tool"'
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

# ------------------------------------------------------------- --upgrade
note "--upgrade: older pinned tools to the helper's pins"
OLDNS=v2.14.0
OLDNSC=v2.14.0
mkdir -p "$W/old"
curl -fsSL -o "$W/old/ns.tgz" \
    "https://github.com/nats-io/nats-server/releases/download/$OLDNS/nats-server-$OLDNS-linux-amd64.tar.gz"
curl -fsSL -o "$W/old/nsc.zip" \
    "https://github.com/nats-io/nsc/releases/download/$OLDNSC/nsc-linux-amd64.zip"
OSHA=$(sha256sum <"$W/old/ns.tgz" | cut -d' ' -f1)
OCSHA=$(sha256sum <"$W/old/nsc.zip" | cut -d' ' -f1)
cp "$HELPER" "$W/h-old.sh"
sed -i -e "s#^NS_VER=.*#NS_VER=$OLDNS#" \
    -e "s#^NS_VERLINE=.*#NS_VERLINE='nats-server: $OLDNS'#" \
    -e "s#^NS_BASE=.*#NS_BASE=https://github.com/nats-io/nats-server/releases/download/$OLDNS#" \
    -e "s#^NS_FILE=.*#NS_FILE=nats-server-$OLDNS-linux-amd64.tar.gz#" \
    -e "s#^NS_MEMBER=.*#NS_MEMBER=nats-server-$OLDNS-linux-amd64/nats-server#" \
    -e "s#^NS_SHA=.*#NS_SHA=$OSHA#" \
    -e "s#^NSC_VER=.*#NSC_VER=$OLDNSC#" \
    -e "s#^NSC_VERLINE=.*#NSC_VERLINE='nsc version ${OLDNSC#v}'#" \
    -e "s#^NSC_BASE=.*#NSC_BASE=https://github.com/nats-io/nsc/releases/download/$OLDNSC#" \
    -e "s#^NSC_SHA=.*#NSC_SHA=$OCSHA#" "$W/h-old.sh"
chmod +x "$W/h-old.sh"
np
D=$W/upg
run "$W/h-old.sh" --dir "$D" --owner alice --port $PORT --start
t "upgrade setup: created with the older pinned tools, running" \
    '[ $RC -eq 0 ] && has "nats-server reports '"'"'nats-server: $OLDNS'"'"'"'
run "$HELPER" --dir "$D" --check
t "--check says the tools are older than the pins, still passes" \
    '[ $RC -eq 0 ] && has "older than this script"'
OLDPID=$(cat "$D/server.pid")
run "$HELPER" --dir "$D" --upgrade
t "--upgrade (running): stops, replaces, restarts, checks" \
    '[ $RC -eq 0 ] && has "stopped pid $OLDPID" && has "installed nats-server" &&
     has "listens on 127.0.0.1:$PORT" && has "check passed"'
UPG_OUT=$OUT
t "--upgrade: new versions in DIR/bin, server runs the new binary" \
    '[ "$("$D/bin/nats-server" -v)" = "nats-server: v2.15.1" ] &&
     [ "$(cat $D/server.pid)" != "$OLDPID" ]'
t "--upgrade: bin has only our files, manifest updated" \
    '[ "$(ls -A $D/bin | tr "\n" " ")" = ".manifest nats-server nsc " ]'
run "$HELPER" --dir "$D" --upgrade
t "--upgrade again: already current (idempotent), rc 0" \
    '[ $RC -eq 0 ] && has "already current"'
run "$HELPER" --dir "$D" --stop
note "--upgrade of a stopped setup leaves it stopped"
np
D=$W/upg2
run "$W/h-old.sh" --dir "$D" --owner alice --port $PORT
run "$HELPER" --dir "$D" --upgrade
t "--upgrade (stopped): installs, does not start" \
    '[ $RC -eq 0 ] && has "installed nats-server" && [ ! -f $D/server.pid ]'

# -------------------------------------------------------------- --backup
note "--backup"
np
D=$W/bk
run "$HELPER" --dir "$D" --owner alice --port $PORT --start
run "$HELPER" --dir "$D" --backup "$W/bk.tar.gz"
t "--backup rc 0, mode 0600, holds-secrets notice, server restarted" \
    '[ $RC -eq 0 ] && [ "$(stat -c %a $W/bk.tar.gz)" = 600 ] &&
     has "holds secrets" && has "stopped pid" && has "listens on"'
t "names: --backup says it writes DEST (0600) and nothing else, and the race" \
    'has "--backup writes $W/bk.tar.gz (mode 0600) and nothing else" &&
     has "$RES"'
t "--backup archive lists, has ca.key and nsc, no bin" \
    'tar -tzf $W/bk.tar.gz | grep -q "ca.key" &&
     tar -tzf $W/bk.tar.gz | grep -q "nsc/" &&
     ! tar -tzf $W/bk.tar.gz | grep -q "^./bin"'
t "--backup: the archive leaves no .cmd temp file inside" \
    '! tar -tzf $W/bk.tar.gz | grep -q "\.cmd\."'
run "$HELPER" --dir "$D" --check
t "--check passes after the backup restart" '[ $RC -eq 0 ]'
run "$HELPER" --dir "$D" --backup "$W/bk.tar.gz"
t "--backup: DEST must not exist" '[ $RC -ne 0 ] && has "already exists"'
run "$HELPER" --dir "$D" --backup /tmp/bk.tar.gz
t "--backup: DEST parent /tmp refused (parent-safety)" \
    '[ $RC -ne 0 ] && has "writable by group or others" && [ ! -e /tmp/bk.tar.gz ]'
run "$HELPER" --dir "$D" --backup "$D/inside.tgz"
t "--backup: DEST inside DIR refused" '[ $RC -ne 0 ] && has "outside"'
run "$HELPER" --dir "$D" --backup "$W/linkp/x.tgz"
t "--backup: DEST under a symlinked parent refused" \
    '[ $RC -ne 0 ] && has "symlink" && [ -z "$(ls -A $W/realp)" ]'
run "$HELPER" --dir "$D" --backup 'relative.tgz'
t "--backup: relative DEST refused" '[ $RC -ne 0 ]'
run "$HELPER" --dir "$D" --stop

# ----------------------------------------- restore: --upgrade makes bin/
note "restore: a backup extracted into an empty directory has no bin"
mv "$D" "$D.orig"
mkdir -m 700 "$D"
tar -xzf "$W/bk.tar.gz" -C "$D" --no-same-owner
t "restore: extracted (marker, keys, config), and there is no bin" \
    '[ ! -e $D/bin ] && [ -f $D/.mash-nats-setup ] && [ -f $D/ca.key ] &&
     [ -f $D/server.conf ]'
run "$HELPER" --dir "$D" --start
t "restore: --start refuses a missing bin and says run --upgrade" \
    '[ $RC -ne 0 ] && has "bin is missing" && has "run --upgrade" &&
     [ ! -e $D/bin ] && ! ss -ltnH | grep -q ":$PORT "'
run "$HELPER" --dir "$D" --check
t "restore: --check fails on a missing bin and says run --upgrade" \
    '[ $RC -ne 0 ] && has "bin is missing" && has "run --upgrade" &&
     [ ! -e $D/bin ]'
run "$HELPER" --dir "$D" --upgrade
t "restore: --upgrade makes bin (0700) and installs the two programs" \
    '[ $RC -eq 0 ] && has "made $D/bin (mode 0700" && has "installed nats-server" &&
     [ "$(stat -c %a $D/bin)" = 700 ] &&
     [ "$(ls -A $D/bin | tr "\n" " ")" = ".manifest nats-server nsc " ]'
run "$HELPER" --dir "$D" --start
t "restore: --start then works" \
    '[ $RC -eq 0 ] && has "requires TLS" && [ -f $D/server.pid ]'
run "$HELPER" --dir "$D" --check
t "restore: --check then passes" '[ $RC -eq 0 ] && has "check passed"'
run "$HELPER" --dir "$D" --stop
for v in empty stray mode link; do
    rm -rf "$W/rst-$v"
    mkdir -m 700 "$W/rst-$v"
    tar -xzf "$W/bk.tar.gz" -C "$W/rst-$v" --no-same-owner
    case $v in
    empty) mkdir -m 700 "$W/rst-$v/bin" ;;
    stray) mkdir -m 700 "$W/rst-$v/bin"; echo x >"$W/rst-$v/bin/stray" ;;
    mode) mkdir -m 755 "$W/rst-$v/bin" ;;
    link) ln -s /nonexistent-bin "$W/rst-$v/bin" ;;
    esac
    BEFORE=$(snap "$W/rst-$v")
    run "$HELPER" --dir "$W/rst-$v" --upgrade
    t "restore: --upgrade still refuses a bin that is not as left ($v)" \
        '[ $RC -ne 0 ] && has "is not as this script left it" &&
         [ "$(snap $W/rst-$v)" = "$BEFORE" ]'
    rm -rf "$W/rst-$v"
done
rm -rf "$D.orig"

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

# ---------------------------------------------------------- --retire-keys
note "--retire-keys"
np
D=$W/rk
run "$HELPER" --dir "$D" --owner alice --port $PORT --start
run "$HELPER" --dir "$D" --retire-keys "$W/rk.tar.gz"
t "F1: --retire-keys piped (no terminal): refused, nothing changed" \
    '[ $RC -ne 0 ] && has "Run this yourself" && [ -f $D/ca.key ] &&
     [ ! -e $W/rk.tar.gz ]'
TYPED="wrong"
runpty "$TYPED" "$HELPER" --dir "$D" --retire-keys "$W/rk.tar.gz"
t "F1: --retire-keys wrong typed text: refused, nothing changed" \
    '[ $RC -ne 0 ] && [ -f $D/ca.key ] && [ -d $D/nsc ] && [ -d $D/export ] &&
     [ ! -e $W/rk.tar.gz ]'
t "names: --retire-keys prompt says what it writes and removes, and the race" \
    'has "--retire-keys writes $W/rk.tar.gz (mode 0600) and
        $D/.keys-retired and removes ca.key, nsc/ and export/ from $D" &&
     has "extracts $W/rk.tar.gz into a private temporary directory under $D" &&
     has "$RES"'
TYPED="$D"
runpty "$TYPED" "$HELPER" --dir "$D" --retire-keys "$W/rk.tar.gz"
t "F1: --retire-keys typed correctly: archive written, three paths removed" \
    '[ $RC -eq 0 ] && [ "$(stat -c %a $W/rk.tar.gz)" = 600 ] &&
     [ ! -e $D/ca.key ] && [ ! -e $D/nsc ] && [ ! -e $D/export ] &&
     [ -f $D/.keys-retired ]'
t "--retire-keys archive holds ca.key, nsc/ and export/ and lists back" \
    'tar -tzf $W/rk.tar.gz | grep -q "^ca.key" &&
     tar -tzf $W/rk.tar.gz | grep -q "^nsc/" &&
     tar -tzf $W/rk.tar.gz | grep -q "^export/operator.nk"'
t "--retire-keys left everything else (server files) in place" \
    '[ -f $D/server.pem ] && [ -f $D/server.key ] && [ -f $D/ca.pem ] &&
     [ -f $D/operator.jwt ] && [ -d $D/resolver ] && [ -d $D/jetstream ] &&
     [ -f $D/server.conf ]'
run "$HELPER" --dir "$D" --check
t "--check treats the retired setup as valid ('keys retired')" \
    '[ $RC -eq 0 ] && has "keys retired"'
run "$HELPER" --dir "$D" --stop
t "--stop works after retire" '[ $RC -eq 0 ]'
run "$HELPER" --dir "$D" --start
t "--start works after retire (TLS answers)" \
    '[ $RC -eq 0 ] && has "requires TLS"'
run "$HELPER" --dir "$D" --upgrade
t "--upgrade works after retire" '[ $RC -eq 0 ] && has "already current"'
run "$HELPER" --dir "$D" --backup "$W/rk-backup.tar.gz"
t "--backup works after retire" '[ $RC -eq 0 ]'
run "$HELPER" --dir "$D" --retire-keys "$W/rk2.tar.gz"
t "--retire-keys twice refused" '[ $RC -ne 0 ] && has "already retired"'
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
MSGRK="the CA key and signing keys are still here: run --retire-keys first"
BEFORE=$(snap "$D")
run "$HELPER" --dir "$D" --remove
t "remove before retire: piped, refused, says run --retire-keys, no change" \
    '[ $RC -ne 0 ] && has "$MSGRK" && has "nothing was changed" &&
     [ -f $D/ca.key ] && [ -d $D/nsc ] && [ -d $D/export ] &&
     [ "$(snap $D)" = "$BEFORE" ] &&
     [ "$(nlisten)" -ge 1 ]'
runpty "$D" "$HELPER" --dir "$D" --remove
t "remove before retire: even with the right typed text, refused, no change" \
    '[ $RC -ne 0 ] && has "$MSGRK" && ! has "Type this exactly" &&
     [ -f $D/ca.key ] && [ "$(snap $D)" = "$BEFORE" ] &&
     [ "$(nlisten)" -ge 1 ]'
runpty "$D" "$HELPER" --dir "$D" --retire-keys "$W/rm-keys.tgz"
t "remove: keys retired first (the --retire-keys path)" \
    '[ $RC -eq 0 ] && [ -f $D/.keys-retired ] && [ ! -e $D/ca.key ]'
run "$HELPER" --dir "$D" --remove
t "F1: --remove piped: refused, nothing removed" \
    '[ $RC -ne 0 ] && has "Run this yourself" && [ -d $D ]'
TYPED="nope"
runpty "$TYPED" "$HELPER" --dir "$D" --remove
t "F1: --remove wrong text: refused, nothing removed, server still up" \
    '[ $RC -ne 0 ] && [ -d $D ] && [ "$(nlisten)" -ge 1 ]'
t "names: --remove prompt says it removes DIR and the unit file, and the race" \
    'has "--remove removes $D with everything in it" &&
     has "the unit file $HOME/.config/systemd/user/mash-nats-" && has "$RES"'
TYPED="$D"
runpty "$TYPED" "$HELPER" --dir "$D" --remove --mash-config "$W/cfgkeep"
t "F1: --remove typed correctly: server stopped, DIR gone" \
    '[ $RC -eq 0 ] && [ ! -e $D ] && has "removed $D"'
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
for n in 2 3 4; do
    mkevil
    rm -rf "$S/bpar" "$S/bpar.orig" "$S/db$n"
    mkdir -m 700 "$S/bpar"
    cp -a "$BASE" "$S/db$n"
    run "$S/swapenv" $n "$S/bpar" "$HELPER" --dir "$S/db$n" --backup \
        "$S/bpar/b.tgz"
    note "S2 backup #$n: stat calls on the destination parent: $(cat "$S/cnt")"
    t "S2: --backup, destination parent swapped at recheck #$n: it happened" \
        'swapped $S/bpar'
    t "S2: --backup swap #$n: aborts, names device:inode, nothing written" \
        '[ $RC -ne 0 ] && refused && has "device:inode" &&
         ! has "wrote $S/bpar/b.tgz"'
    t "S2: --backup swap #$n: the replacement is untouched" \
        'untouched $S/bpar'
    t "S2: --backup swap #$n: the unintended destination holds exactly the
        sentinel (listing, sha256): no archive, helper file or key" \
        'evil_exact $S/bpar'
    t "S2: --backup swap #$n: DIR itself is intact" \
        '[ -f $S/db$n/ca.key ] && [ -d $S/db$n/nsc ]'
done
for n in 2 3 4 5; do
    mkevil
    rm -rf "$S/kpar" "$S/kpar.orig" "$S/dk$n"
    mkdir -m 700 "$S/kpar"
    cp -a "$BASE" "$S/dk$n"
    TYPED="$S/dk$n"
    runpty "$TYPED" "$S/swapenv" $n "$S/kpar" "$HELPER" --dir "$S/dk$n" \
        --retire-keys "$S/kpar/k.tgz"
    note "S2 retire-keys #$n: stat calls: $(cat "$S/cnt")"
    t "S2: --retire-keys, dest parent swapped at recheck #$n: it happened" \
        'swapped $S/kpar'
    t "S2: --retire-keys swap #$n: aborts, names device:inode" \
        '[ $RC -ne 0 ] && refused && has "device:inode"'
    t "S2: --retire-keys swap #$n: the unintended destination holds exactly
        the sentinel (listing, sha256): no archive, helper file or key" \
        'evil_exact $S/kpar'
    t "S2: --retire-keys swap #$n: replacement untouched, sources retained" \
        'untouched $S/kpar && [ -f $S/dk$n/ca.key ] && [ -d $S/dk$n/nsc ] &&
         [ -d $S/dk$n/export ] && [ ! -e $S/dk$n/.keys-retired ] &&
         [ -z "$(ls -A $S/dk$n | grep "^.verify")" ]'
done
for n in 2 3; do
    mkevil
    rm -rf "$S/rpar" "$S/rpar.orig"
    mkdir -m 700 "$S/rpar"
    cp -a "$BASE" "$S/rpar/d"
    TYPED="$S/rpar/d"
    rm -f "$S/rk-r$n.tgz"
    runpty "$TYPED" "$HELPER" --dir "$S/rpar/d" --retire-keys "$S/rk-r$n.tgz"
    t "S2: --remove swap #$n: keys retired first (no swap yet)" \
        '[ $RC -eq 0 ] && [ -f $S/rpar/d/.keys-retired ]'
    mkevil
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
         [ -f $S/rpar.orig/d/.keys-retired ] && [ -f $S/rpar.orig/d/server.conf ]'
done
# ------------------- --remove refuses while any key is still here
note "remove refuses until --retire-keys has run"
MSGRK="the CA key and signing keys are still here: run --retire-keys first"
for k in ca.key nsc export; do
    rm -rf "$S/rf-$k"
    cp -a "$BASE" "$S/rf-$k"
    for o in ca.key nsc export; do
        [ "$o" = "$k" ] || rm -rf "$S/rf-$k/$o"
    done
    BEFORE=$(snap "$S/rf-$k")
    runpty "$S/rf-$k" "$HELPER" --dir "$S/rf-$k" --remove
    t "remove refused with only $k left, no .keys-retired: nothing changed" \
        '[ $RC -ne 0 ] && has "$MSGRK" && has "nothing was changed" &&
         ! has "Type this exactly" && [ "$(snap $S/rf-$k)" = "$BEFORE" ] &&
         [ -f $S/rf-$k/.mash-nats-setup ]'
    run "$HELPER" --dir "$S/rf-$k" --remove
    t "remove refused with only $k left, piped: nothing changed" \
        '[ $RC -ne 0 ] && has "$MSGRK" && [ "$(snap $S/rf-$k)" = "$BEFORE" ]'
    rm -rf "$S/rf-$k"
done
# ------------------------------ S9: --retire-keys verifies the archive
note "S9: tar shims make a partial, a truncated and an altered archive"
mkdir -p "$S/sh-tar"
cat >"$S/sh-tar/tar" <<'SH'
#!/bin/bash
# test shim, TARMODE: partial (the archive lacks export/), trunc (the
# archive is cut in half), alter (extracting changes one byte of ca.key)
real=/usr/bin/tar
case "${TARMODE:-}:${1:-}" in
partial:-czf)
    a=()
    for x in "$@"; do [ "$x" = export ] || a+=("$x"); done
    exec $real "${a[@]}" ;;
trunc:-czf)
    $real "$@" || exit
    sz=$(/usr/bin/stat -c %s "$2")
    truncate -s $((sz / 2)) "$2" ;;
alter:-xzf)
    $real "$@" || exit
    c=''
    while [ $# -gt 0 ]; do
        [ "$1" = -C ] && c=$2
        shift
    done
    printf 'x' >>"$c/ca.key" ;;
*) exec $real "$@" ;;
esac
SH
chmod +x "$S/sh-tar/tar"
for m in partial trunc alter; do
    rm -rf "$S/dt-$m" "$S/t-$m.tgz"
    cp -a "$BASE" "$S/dt-$m"
    TYPED="$S/dt-$m"
    runpty "$TYPED" env PATH="$S/sh-tar:$PATH" TARMODE=$m "$HELPER" \
        --dir "$S/dt-$m" --retire-keys "$S/t-$m.tgz"
    case $m in
    partial) MSGS='has "lacks ca.key, nsc/ or export/"' ;;
    trunc) MSGS='has "could not be extracted" || has "did not list back"' ;;
    alter) MSGS='has "differs from the one in"' ;;
    esac
    t "S9: $m archive: aborts with a reason, sources retained, none removed" \
        '[ $RC -ne 0 ] && { '"$MSGS"'; } && [ -f $S/dt-$m/ca.key ] &&
         [ -d $S/dt-$m/nsc ] && [ -d $S/dt-$m/export ] &&
         [ ! -e $S/dt-$m/.keys-retired ] && [ -f $S/dt-$m/server.pem ] &&
         [ -f $S/dt-$m/operator.jwt ]'
    t "S9: $m archive: the temporary extraction directory is gone" \
        '[ -z "$(ls -A $S/dt-$m | grep "^.verify")" ] &&
         [ -z "$(ls -A $S/dt-$m | grep "^.cmd")" ]'
    t "S9: $m archive: no key content in the output" \
        '! has "$(head -c 20 $S/dt-$m/ca.key | tr -d "\n")"'
done
rm -rf "$S/dt-good" "$S/t-good.tgz"
cp -a "$BASE" "$S/dt-good"
runpty "$S/dt-good" "$HELPER" --dir "$S/dt-good" --retire-keys \
    "$S/t-good.tgz"
t "S9: a good archive: verified identical, sources removed, extraction gone" \
    '[ $RC -eq 0 ] && has "identical to the sources" &&
     [ ! -e $S/dt-good/ca.key ] && [ ! -e $S/dt-good/nsc ] &&
     [ ! -e $S/dt-good/export ] && [ -f $S/dt-good/.keys-retired ] &&
     [ -z "$(ls -A $S/dt-good | grep "^.verify")" ] &&
     [ "$(stat -c %a $S/t-good.tgz)" = 600 ]'
}

group_root() {
cd /root || exit 1
note "root: every subcommand refuses, nothing is created or read"
R=/root/rt-h5
MSG="run as your own user, not root; sudo would make root-owned keys"
before=$(find /root -xdev 2>/dev/null | sort | md5sum)
t "root: this really is uid 0" '[ "$(id -u)" = 0 ]'
for a in "--owner alice" "--owner alice --start" --start --stop --check \
    --upgrade "--backup /root/rt-h5-b.tgz" "--retire-keys /root/rt-h5-k.tgz" \
    --enable-boot --disable-boot --remove; do
    # shellcheck disable=SC2086
    run "$HELPER" --dir "$R/d" $a
    t "S7: as root, '$a': refused with the fixed message, exit != 0" \
        '[ $RC -ne 0 ] && has "$MSG"'
    t "S7: as root, '$a': nothing created" \
        '[ ! -e $R ] && [ ! -e /root/rt-h5-b.tgz ] &&
         [ ! -e /root/rt-h5-k.tgz ] &&
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


# ------------------------------------------------- boot (systemd as PID 1)
UNITDIR=/home/alice/.config/systemd/user
unit_of() { printf 'mash-nats-%s.service' \
    "$(printf '%s' "$1" | sha256sum | cut -c1-12)"; }
sc() { systemctl --user "$@"; }
mashenv() { env -i HOME=/home/alice PATH="$BINP:/usr/bin:/bin" "$@"; }
group_boot1() {
cd /home/alice || exit 1
note "boot: systemd user manager in the container"
t "systemd user manager is up (systemctl --user)" \
    '[ "$(sc is-system-running 2>&1)" = running ]'
D=$W/boot
U=$(unit_of "$D")
run "$HELPER" --dir "$D" --owner alice --start
t "boot: create --start (helper-started copy)" '[ $RC -eq 0 ]'
grep -E '^    (MASH_CONFIG_DIR=[^ ]+ )?mashctl adopt --from ' <<<"$OUT" \
    >"$W/b-adopt.cmd" || true
oldinit "$W/b-adopt.cmd" >"$W/b-init.cmd"
run mashenv MASH_TLS_CA="$D/ca.pem" bash -c \
    "$(oldadopt "$W/b-adopt.cmd")"
t "boot: printed adopt command works" '[ $RC -eq 0 ]'
run mashenv bash -c "$(cat "$W/b-init.cmd")"
t "boot: printed init command works" '[ $RC -eq 0 ]'
run mashenv mashctl endpoint create alice/dev/design --make-key
run mashenv mashctl endpoint create alice/dev/review --make-key
run mashenv MASH_OWNER=alice MASH_GROUP=dev MASH_NAME=design mash send \
    alice.dev.review "Mail before reboot"
t "boot: mail sent before enabling boot" '[ $RC -eq 0 ]'
OLDPID=$(cat "$D/server.pid")
run "$HELPER" --dir "$D" --enable-boot
t "F1: --enable-boot piped: refused, nothing written" \
    '[ $RC -ne 0 ] && has "Run this yourself" && [ ! -e $UNITDIR/$U ]'
TYPED=wrong
runpty "$TYPED" "$HELPER" --dir "$D" --enable-boot
t "F1: --enable-boot wrong text: refused, nothing written, copy still up" \
    '[ $RC -ne 0 ] && [ ! -e $UNITDIR/$U ] && kill -0 $OLDPID'
t "names: --enable-boot prompt names the unit file at its real path" \
    'has "--enable-boot writes the unit file $UNITDIR/$U" &&
     has "It writes nothing else" && has "$RES"'
t "boot: the confirmation text says what linger changes only with --linger" \
    'has "Without --linger"'
TYPED=$U
runpty "$TYPED" "$HELPER" --dir "$D" --enable-boot
printf '%s\n' "$OUT" >"$W/enable.out"
t "F1: --enable-boot typed correctly: unit written, enabled, started" \
    '[ $RC -eq 0 ] && has "enabled and started $U" && [ -f $UNITDIR/$U ]'
run sc status "$U" --no-pager
printf '%s\n' "$OUT" >"$W/status1.out"
t "boot: systemctl --user status shows active (running)" \
    'has "Active: active (running)"'
run cat "$UNITDIR/$U"
printf '%s\n' "$OUT" >"$W/unit.out"
t "boot: unit has the absolute ExecStart and Restart=on-failure" \
    'has "ExecStart=\"$D/bin/nats-server\" -c \"$D/server.conf\"" &&
     has "Restart=on-failure"'
t "boot: the helper-started copy was stopped first" '! kill -0 $OLDPID 2>/dev/null'
run "$HELPER" --dir "$D" --check
t "boot: --check sees the systemd server (exact command, one socket)" \
    '[ $RC -eq 0 ] && has "runs exactly nats-server -c server.conf" &&
     has "(the client port)"'
ssl >"$W/ss-boot.txt"
t "ss: only the client port with the systemd-run server" \
    '[ "$(nlisten)" = 1 ]'
run "$HELPER" --dir "$D" --start
t "boot: --start refuses (already running under systemd)" \
    '[ $RC -ne 0 ] && has "already running"'
# restart through systemd (emulates a boot of the user manager's service)
run sc restart "$U"
sleep 4
run mashenv MASH_OWNER=alice MASH_GROUP=dev MASH_NAME=review mash inbox
t "boot: after systemctl --user restart the server is back with the mail" \
    '[ "$(sc is-active $U)" = active ] && has "Mail before reboot"'
# crash: kill -9 the main pid, Restart=on-failure brings it back
MP=$(sc show -p MainPID --value "$U")
kill -9 "$MP"
sleep 5
t "boot: kill -9 of the server: systemd restarted it (Restart=on-failure)" \
    '[ "$(sc is-active $U)" = active ] &&
     [ "$(sc show -p MainPID --value $U)" != "$MP" ]'
# a unit file we did not write is never touched
D2=$W/boot2
run "$HELPER" --dir "$D2" --owner alice --port 4333
U2=$(unit_of "$D2")
printf '%s\n' '[Unit]' 'Description=someone else' >"$UNITDIR/$U2"
TYPED=$U2
runpty "$TYPED" "$HELPER" --dir "$D2" --enable-boot
t "boot: an existing unit file we did not write: refused, untouched" \
    '[ $RC -ne 0 ] && has "did not write it" &&
     [ "$(cat $UNITDIR/$U2 | head -n 1)" = "[Unit]" ]'
mv "$UNITDIR/$U2" "$W/foreign-unit.moved"
# --linger: says exactly what it changes
TYPED=$U2
runpty "$TYPED" "$HELPER" --dir "$D2" --enable-boot --linger
printf '%s\n' "$OUT" >"$W/enable-linger.out"
t "boot: --linger text says what linger changes and how to undo it" \
    'has "loginctl enable-linger alice" && has "keep running when" &&
     has "loginctl" && has "disable-linger alice"'
t "boot: --enable-boot --linger completed (or failed clearly)" \
    '[ $RC -eq 0 ] || has "failed"'
note "boot: linger now: $(loginctl show-user alice -p Linger 2>&1)"
}
group_boot2() {
cd /home/alice || exit 1
D=$W/boot
U=$(unit_of "$D")
D2=$W/boot2
U2=$(unit_of "$D2")
note "boot: after the user manager was restarted (emulated boot)"
i=0
while [ $i -lt 30 ] && [ "$(sc is-active "$U" 2>&1)" != active ]; do
    sleep 1
    i=$((i + 1))
done
t "boot: after restarting the user manager the unit is active again" \
    '[ "$(sc is-active $U)" = active ]'
run sc status "$U" --no-pager
printf '%s\n' "$OUT" >"$W/status2.out"
t "boot: status shows active (running) after the manager restart" \
    'has "Active: active (running)"'
run "$HELPER" --dir "$D" --check
t "boot: --check passes after the restart" '[ $RC -eq 0 ]'
run mashenv MASH_OWNER=alice MASH_GROUP=dev MASH_NAME=review mash inbox
t "boot: the server came back with the mail" 'has "Mail before reboot"'
run "$HELPER" --dir "$D" --stop
t "boot: --stop works through systemd" \
    '[ $RC -eq 0 ] && [ "$(sc is-active $U)" != active ]'
run "$HELPER" --dir "$D" --start
t "boot: --start works through systemd" \
    '[ $RC -eq 0 ] && [ "$(sc is-active $U)" = active ]'
run "$HELPER" --dir "$D" --backup "$W/boot-bk.tar.gz"
t "boot: --backup stops and restarts the systemd server" \
    '[ $RC -eq 0 ] && [ "$(sc is-active $U)" = active ]'
run "$HELPER" --dir "$D" --disable-boot
t "F1: --disable-boot piped: refused, unit still there" \
    '[ $RC -ne 0 ] && has "Run this yourself" && [ -f $UNITDIR/$U ]'
TYPED=wrong
runpty "$TYPED" "$HELPER" --dir "$D" --disable-boot
t "F1: --disable-boot wrong text: refused, unit still enabled" \
    '[ $RC -ne 0 ] && [ -f $UNITDIR/$U ] && [ "$(sc is-enabled $U)" = enabled ]'
t "names: --disable-boot prompt says it removes that unit file" \
    'has "--disable-boot removes the unit file $UNITDIR/$U" && has "$RES"'
TYPED=$U
runpty "$TYPED" "$HELPER" --dir "$D" --disable-boot
printf '%s\n' "$OUT" >"$W/disable.out"
t "F1: --disable-boot typed correctly: disabled, stopped, file removed" \
    '[ $RC -eq 0 ] && [ ! -e $UNITDIR/$U ] && [ "$(sc is-active $U)" != active ]'
t "boot: --disable-boot says linger was not touched and how to turn it off" \
    'has "linger was not touched" && has "loginctl disable-linger alice"'
t "boot: --disable-boot never turned linger off" \
    '[ "$(loginctl show-user alice -p Linger)" = Linger=yes ]'
run "$HELPER" --dir "$D" --start
t "boot: after --disable-boot the helper starts it by hand again" \
    '[ $RC -eq 0 ] && [ -f $D/server.pid ]'
run "$HELPER" --dir "$D" --stop
# --remove with a unit we wrote: the unit goes too
if [ -f "$UNITDIR/$U2" ]; then
    TYPED=$D2
    runpty "$TYPED" "$HELPER" --dir "$D2" --remove
    t "boot: --remove before --retire-keys is refused, unit and DIR stay" \
        '[ $RC -ne 0 ] && has "run --retire-keys first" && [ -d $D2 ] &&
         [ -f $UNITDIR/$U2 ]'
    runpty "$TYPED" "$HELPER" --dir "$D2" --retire-keys "$W/boot2-keys.tgz"
    t "boot: keys retired before the remove" \
        '[ $RC -eq 0 ] && [ -f $D2/.keys-retired ]'
    runpty "$TYPED" "$HELPER" --dir "$D2" --remove
    t "boot: --remove with a boot unit: unit file and DIR both removed" \
        '[ $RC -eq 0 ] && [ ! -e $UNITDIR/$U2 ] && [ ! -e $D2 ] &&
         has "disabled $U2"'
fi
}

group_reach_up() {
cd /home/alice || exit 1
PORT=4700
# bob must be able to walk to DIR so that only DIR's own mode (0700) keeps
# him out, not the home directory's
chmod 755 /home/alice
note "reach: a loopback-only setup and a non-loopback one"
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
PORT2=4701
D2=$W/reach2
runpty "0.0.0.0:$PORT2" "$HELPER" --dir "$D2" --owner alice --listen 0.0.0.0 \
    --port $PORT2 --start
t "reach: non-loopback setup created and running (typed confirmation)" \
    '[ $RC -eq 0 ] && has "Exposed: 0.0.0.0:$PORT2 is reachable from other
        machines" && ! has "Binding 0.0.0.0"'
cp "$D2/ca.pem" /tmp/reach-ca2.pem
chmod 644 /tmp/reach-ca2.pem
EXTIP=$(python3 -I -c 'import socket
a = socket.gethostbyname(socket.gethostname())
print("" if a.startswith("127.") else a)')
note "reach: the container's own non-loopback address: ${EXTIP:-none found}"
{
    printf 'DIR=%q\nPORT=%q\nPORT2=%q\nEXTIP=%q\nALICEUID=%q\n' \
        "$D" "$PORT" "$PORT2" "$EXTIP" "$(id -u)"
} >/tmp/reach-info
chmod 644 /tmp/reach-info
}
group_reach_down() {
cd /home/alice || exit 1
D=$W/reach
D2=$W/reach2
run "$HELPER" --dir "$D" --check
t "reach: after bob, the loopback setup still checks clean" '[ $RC -eq 0 ]'
t "reach: bob left no file in DIR" '[ ! -e $D/bob-was-here ]'
run "$HELPER" --dir "$D" --stop
t "reach: loopback setup stopped" '[ $RC -eq 0 ]'
run "$HELPER" --dir "$D2" --stop
t "reach: non-loopback setup stopped" '[ $RC -eq 0 ]'
}

case ${1:-main} in
reach-up) group_reach_up ;;
reach-down) group_reach_down ;;
main) group_main ;;
sec) group_sec ;;
root) group_root ;;
osrel) group_osrel ;;
boot1) group_boot1 ;;
boot2) group_boot2 ;;
esac
printf '\nSUMMARY: %s passed, %s failed\n' "$PASSN" "$FAILN"
[ "$FAILN" -eq 0 ]
__INCONT__

cat >"$T/ctx1/Containerfile" <<'__CF1__'
FROM docker.io/library/debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends curl tar unzip openssl iproute2 ca-certificates python3 && rm -rf /var/lib/apt/lists/*
RUN useradd -m -u 1000 -s /bin/bash alice
__CF1__
cat >"$T/ctx2/Containerfile" <<'__CF2__'
FROM docker.io/library/debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends systemd systemd-sysv dbus dbus-user-session curl tar unzip openssl iproute2 ca-certificates python3 procps && rm -rf /var/lib/apt/lists/*
RUN useradd -m -u 1000 -s /bin/bash alice
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
__CF2__

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
    say "group sec (path swaps, server-log allowlist, archive verification)"
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

# ----------------------------------------------------------------- boot
if want boot; then
    say "building the systemd image $IMG2"
    podman build -q -t "$IMG2" "$T/ctx2" >/dev/null || exit 2
    BUILT_IMG2=1
    podman run -d --name "$C2" --systemd=always "$IMG2" >/dev/null || exit 2
    i=0
    while [ $i -lt 60 ] && ! podman exec "$C2" systemctl is-system-running \
        2>/dev/null | grep -qE 'running|degraded'; do
        sleep 1
        i=$((i + 1))
    done
    # lets alice have a user manager without logging in, as a booted
    # machine with a login would
    podman exec "$C2" loginctl enable-linger alice
    UX="podman exec --user alice -e XDG_RUNTIME_DIR=/run/user/1000"
    UX="$UX -e DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus $C2"
    i=0
    while [ $i -lt 60 ] && ! $UX systemctl --user is-system-running \
        2>/dev/null | grep -q running; do
        sleep 1
        i=$((i + 1))
    done
    stage "$C2"
    say "group boot, part 1 (enable, crash, restart)"
    report boot1 $UX bash /home/alice/incont.sh boot1
    say "restarting the user manager (stands in for a reboot)"
    podman exec "$C2" systemctl restart user@1000
    sleep 5
    say "group boot, part 2 (after the restart)"
    report boot2 $UX bash /home/alice/incont.sh boot2
    podman cp "$C2:/home/alice/transcript.txt" "$T/out/transcript-boot.txt" \
        2>/dev/null
fi

printf '\nTOTAL: %s passed, %s failed\n' "$TOTAL_PASS" "$TOTAL_FAIL"
[ "$TOTAL_FAIL" -eq 0 ]
