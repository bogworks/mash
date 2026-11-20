#!/usr/bin/env bash
# Container test for scripts/mash-local-nats-setup.sh.  Run it with:
#
#     bash scripts/mash-local-nats-setup_test.sh
#
# Venue rules: rootless podman on this host only; one image per distro, built
# from an official tag (debian:stable, ubuntu:24.04, ubuntu:26.04); one
# container per case, named after the run id, no ports published, network
# none (the loopback inside the container is all the script needs), a non-root
# user (tester) except for the root-refusal case; nothing is installed on the
# host.  On exit (also on failure) it removes only this run's containers, by
# name, this run's images, by recorded id, and its build directory.
#
# Environment:
#   MLNT_IMAGES   space-separated image tags (default: debian:stable ubuntu:24.04 ubuntu:26.04)
#   MLNT_CASES    space-separated case numbers (default: 1 2 ... 14)
#   MLNT_REPORT   receipt file (markdown; default: <scratch>/receipt-<run id>.md)
#   MLNT_SCRATCH  scratch directory (default: the p1t folder of the session scratchpad)
#   MLNT_NATSCLI  natscli version (default 0.5.2)
#   MLNT_SCRIPT   test another copy of the script (default: scripts/mash-local-nats-setup.sh)
#
# Every assertion prints "ok: ..." or "FAIL: ..."; the totals come last and the
# exit status is non-zero when any assertion failed.

# ======================================================================
# Part 1: runs INSIDE the container (as `bash test.sh N` with MLNT_INSIDE=1)
# ======================================================================
inside_main() {
set -u
SCRIPT=/t/mash-local-nats-setup.sh
CASE=$1
H=${HOME:?}
W=$(mktemp -d /tmp/w.XXXXXX)
ROUTE=pipe
PRE=()
RC=0

ok() { printf 'ok: c%s: %s\n' "$CASE" "$1"; }
dump() {
    printf '  --- rc=%s, stdout then stderr of the last run\n' "$RC"
    { head -n 12 "$W/out"; echo '  ---'; head -n 12 "$W/err"; } 2>/dev/null | sed 's/^/  | /'
}
bad() { printf 'FAIL: c%s: %s\n' "$CASE" "$1"; dump; }
chk() {
    local d=$1
    shift
    if "$@" >"$W/chk" 2>&1; then ok "$d"; else bad "$d"; sed 's/^/  > /' "$W/chk" | head -n 8; fi
}
rc_is() { [ "$RC" = "$1" ]; }
has() { grep -Fq -- "$2" "$1"; }
hasx() { grep -Fxq -- "$2" "$1"; }
hasre() { grep -Eq -- "$2" "$1"; }
lacks() { ! grep -Fq -- "$2" "$1"; }
abs_exists() { case $1 in /*) [ -e "$1" ] ;; *) return 1 ;; esac; }
alive() {
    [ -d "/proc/$1" ] && ! grep -q '^State:[[:space:]]*Z' "/proc/$1/status" 2>/dev/null
}
differs() { [ -n "$1" ] && [ "$1" != "$2" ]; }
gone() { ! alive "$1"; }
pid_of() { head -n 1 "$1/server.pid" 2>/dev/null; }
port_of() { sed -n 's/^ *"port": *\([0-9]*\),*$/\1/p' "$1/mash-setup.json"; }
listening() { ss -ltn | awk 'NR>1 {print $4}' | grep -Fxq "127.0.0.1:$1"; }
waitlisten() {
    local i=0
    while ! listening "$1" && [ "$i" -lt 40 ]; do sleep 0.25; i=$((i + 1)); done
    listening "$1"
}
plain_pub() { XDG_CONFIG_HOME="$W/xdgc" nats --server nats://127.0.0.1:4222 --timeout 3s pub x y; }
nopid() { [ ! -s "$1" ] || ! alive "$(cat "$1")"; }
absent() { [ ! -e "$1" ] && [ ! -L "$1" ]; }

run() { # ARGS...: the script, by the current ROUTE and PRE, with no terminal
    if [ "$ROUTE" = pipe ]; then
        cat "$SCRIPT" | "${PRE[@]}" bash -s -- "$@" >"$W/out" 2>"$W/err"
    else
        "${PRE[@]}" bash "$SCRIPT" "$@" >"$W/out" 2>"$W/err" </dev/null
    fi
    RC=$?
}

cat >"$W/ptyhelper.py" <<'PYEOF'
# ptyhelper.py RESP CMD...: run CMD on a pseudo-terminal.  When the output ends with
# a prompt ("owner name ...: " or "type the folder ...: ") send RESP and a
# newline (RESP "__none__": send nothing).  Prints the terminal output and
# exits with the real exit status of CMD.
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
    low = out.lower()
    if (not sent and resp != '__none__' and out.endswith(b': ')
            and (b'owner name' in low or b'type the folder' in low)):
        sent = True
        os.write(fd, resp.encode() + b'\n')
if timed_out:
    os.kill(pid, signal.SIGKILL)
    out += b'\n[ptyhelper.py: timeout]\n'
_, st = os.waitpid(pid, 0)
sys.stdout.write(out.decode(errors='replace').replace('\r', ''))
sys.exit(124 if timed_out else os.waitstatus_to_exitcode(st))
PYEOF

cat >"$W/rawplain.py" <<'PYEOF'
# rawplain.py PORT: a plain-text NATS client: read INFO, send CONNECT and PING,
# print PONG when the server answered it, else what happened.
import socket, sys
s = socket.create_connection(('127.0.0.1', int(sys.argv[1])), timeout=5)
buf = b''
try:
    buf = s.recv(4096)
    s.sendall(b'CONNECT {"verbose":false}\r\nPING\r\n')
    buf += s.recv(4096)
except Exception as e:
    print('closed', e.__class__.__name__)
print('PONG' if b'PONG' in buf else 'no-pong')
PYEOF

ptyrun() { # RESP cmd...: with a terminal; RC is the command's exit status
    local resp=$1; shift
    : >"$W/err"
    python3 "$W/ptyhelper.py" "$resp" "$@" >"$W/out" 2>&1
    RC=$?
}

# mkpath DIR EXCLUDED...: a PATH directory with symlinks to everything but EXCLUDED
mkpath() {
    local d=$1 src f b x skip
    shift
    mkdir -p "$d"
    for src in /usr/local/bin /usr/bin /usr/sbin; do
        for f in "$src"/*; do
            b=${f##*/}; skip=0
            for x in "$@"; do [ "$b" = "$x" ] && skip=1; done
            { [ "$skip" = 1 ] || [ -e "$d/$b" ]; } || ln -s "$f" "$d/$b"
        done
    done
}

NN_CRED=mashadmin.creds
nn() { # D PORT NATS-ARGS...: the nats command against a made server, with TLS and creds
    local d=$1 p=$2
    shift 2
    XDG_CONFIG_HOME="$W/xdgc" XDG_DATA_HOME="$W/xdgd" nats --server "tls://127.0.0.1:$p" \
        --tlsca "$d/ca.pem" --creds "$d/creds/$NN_CRED" --timeout 5s "$@"
}
stopdir() { bash "$SCRIPT" --stop --dir "$1" >/dev/null 2>&1 </dev/null; }

modes_ok() { # DIR: dirs 700, ca.pem/server.pem 644, everything else 600
    local t m p want r=0
    while read -r t m p; do
        case $t in
        d) want=700 ;;
        *) case ${p##*/} in ca.pem | server.pem) want=644 ;; *) want=600 ;; esac ;;
        esac
        [ "$m" = "$want" ] || { printf '  mode %s want %s: %s\n' "$m" "$want" "$p"; r=1; }
    done < <(find "$1" -printf '%y %m %p\n')
    return $r
}

check_success() { # DIR OWNER: assertions for a successful create (last run)
    local d=$1 o=$2 k v y n
    chk "exit 0" rc_is 0
    n=$(wc -l <"$W/out")
    chk "output is <= 10 lines (got $n)" [ "$n" -le 10 ]
    chk "last line is exactly the mashctl init command" \
        [ "$(tail -n 1 "$W/out")" = "mashctl init --config $d/mash-setup.yaml" ]
    chk "stderr is empty" [ ! -s "$W/err" ]
    y=$d/mash-setup.yaml
    for k in servers ca owner admin_creds system_creds operator_key account_key; do
        chk "yaml has $k" grep -q "^$k: " "$y"
    done
    for k in ca admin_creds system_creds operator_key account_key; do
        v=$(sed -n "s/^$k: //p" "$y")
        chk "yaml $k is absolute and exists" abs_exists "$v"
    done
    chk "yaml owner is $o" [ "$(sed -n 's/^owner: //p' "$y")" = "$o" ]
    chk "yaml servers matches the port in the marker" \
        [ "$(sed -n 's/^servers: //p' "$y")" = "[tls://127.0.0.1:$(port_of "$d")]" ]
    chk "modes: dirs 700, ca.pem/server.pem 644, the rest 600" modes_ok "$d"
    chk "server.pid names a live process" alive "$(pid_of "$d")"
}

homelist() { find "$H" -mindepth 1 -not -path "$1" -not -path "$1/*" | sort; }

SEEDRE='(^|[^A-Z2-7])S[OANUCX][A-Z2-7]{54,}'
JWTRE='-----BEGIN NATS USER JWT'

# ---------------------------------------------------------------- cases
case1() { # root refused
    local before after
    before=$(find "$H" -mindepth 0 2>/dev/null | sort | cksum)
    run --owner acme
    chk "root: exit 1" rc_is 1
    chk "root: error: line on stderr" hasre "$W/err" '^error: '
    chk "root: says do not run as root" has "$W/err" 'do not run as root'
    run --owner acme --dir "$H/mash-r"
    chk "root with --dir: exit 1" rc_is 1
    run --stop
    chk "root --stop: exit 1" rc_is 1
    ROUTE=file; run --owner acme
    chk "root, file route: exit 1" rc_is 1
    after=$(find "$H" -mindepth 0 2>/dev/null | sort | cksum)
    chk "root: nothing created ($H unchanged)" [ "$before" = "$after" ]
    chk "root: no mash-nats dir" absent "$H/mash-nats"
}

case2() { # missing tools, hidden by PATH
    local P=$W/p
    ROUTE=pipe
    mkpath "$P" nats
    PRE=(env "PATH=$P"); run --owner acme
    chk "no nats: exit 1" rc_is 1
    chk "no nats: error: missing: nats" hasx "$W/err" 'error: missing: nats'
    chk "no nats: hint mentions brew" hasre "$W/err" '^hint: .*brew'
    chk "no nats: hint names the natscli GitHub releases URL" \
        has "$W/err" 'https://github.com/nats-io/natscli/releases'
    chk "no nats: nothing created" absent "$H/mash-nats"
    rm -rf "$P"; mkpath "$P" openssl
    PRE=(env "PATH=$P"); run --owner acme
    chk "no openssl: exit 1" rc_is 1
    chk "no openssl: error: missing: openssl" hasx "$W/err" 'error: missing: openssl'
    chk "no openssl: hint mentions brew" hasre "$W/err" '^hint: .*brew'
    chk "no openssl: nothing created" absent "$H/mash-nats"
    rm -rf "$P"; mkpath "$P" nats-server
    PRE=(env "PATH=$P"); run --owner acme
    chk "no nats-server: exit 1" rc_is 1
    chk "no nats-server: error: missing: nats-server" hasx "$W/err" 'error: missing: nats-server'
    chk "no nats-server: hint mentions brew" hasre "$W/err" '^hint: .*brew'
    chk "no nats-server: nothing created" absent "$H/mash-nats"
    rm -rf "$P"; mkpath "$P" nats openssl nats-server
    PRE=(env "PATH=$P"); run --owner acme
    chk "none of the three: exit 1" rc_is 1
    chk "none: one line names each" hasx "$W/err" 'error: missing: nats-server, nats, openssl'
    chk "none: exactly one error: line" [ "$(grep -c '^error:' "$W/err")" = 1 ]
    chk "none: hints mention brew" hasre "$W/err" '^hint: .*brew.*brew.*brew'
    chk "none: hint names the natscli GitHub releases URL" \
        has "$W/err" 'https://github.com/nats-io/natscli/releases'
    ROUTE=file
    PRE=(env "PATH=$P"); run --owner acme
    chk "none, file route: exit 1" rc_is 1
    chk "none, file route: same error line" hasx "$W/err" 'error: missing: nats-server, nats, openssl'
    chk "nothing created" absent "$H/mash-nats"
}

case3() { # nats-server too old
    mkdir -p "$W/shim"
    printf '#!/bin/sh\necho "nats-server: v2.9.0"\n' >"$W/shim/nats-server"
    chmod 755 "$W/shim/nats-server"
    PRE=(env "PATH=$W/shim:$PATH"); run --owner acme
    chk "v2.9.0: exit 1" rc_is 1
    chk "v2.9.0: says too old" has "$W/err" 'too old'
    chk "v2.9.0: nothing created" absent "$H/mash-nats"
    printf '#!/bin/sh\necho "nats-server: v2.10.6"\n' >"$W/shim/nats-server"
    run --owner acme
    chk "v2.10.6: exit 1" rc_is 1
    chk "v2.10.6: says too old" has "$W/err" 'too old'
    printf '#!/bin/sh\necho "nats-server: banana"\n' >"$W/shim/nats-server"
    run --owner acme
    chk "unreadable version: exit 1" rc_is 1
    chk "unreadable version: error: line" hasre "$W/err" '^error: cannot read the nats-server version'
    chk "nothing created" absent "$H/mash-nats"
}

case4() { # success, both routes
    local d=$H/mash-nats d2=$H/mash-f
    homelist "$d" >"$W/home.before"
    ROUTE=pipe; run --owner acme
    check_success "$d" acme
    homelist "$d" >"$W/home.after"
    chk "nothing created in \$HOME beyond the dir" cmp -s "$W/home.before" "$W/home.after"
    chk "the default dir is \$HOME/mash-nats and made" [ -d "$d" ]
    ROUTE=file; run --owner acme --dir "$d2"
    check_success "$d2" acme
    chk "the second server took another port" [ "$(port_of "$d")" != "$(port_of "$d2")" ]
    stopdir "$d"; stopdir "$d2"
}

case5() { # no seed or creds content leaks
    local d=$H/mash-nats f
    run --owner acme
    chk "setup ran" rc_is 0
    for f in "$W/out" "$W/err" "$d/server.log" "$d/server.conf" "$d/mash-setup.yaml" "$d/mash-setup.json"; do
        chk "no seed in ${f#"$H"/}" bash -c '! grep -Eq "$1" "$2"' _ "$SEEDRE" "$f"
        chk "no user JWT header in ${f#"$H"/}" bash -c '! grep -q -- "$1" "$2"' _ "$JWTRE" "$f"
    done
    chk "control: the regex does match a real seed (operator.nk)" grep -Eq "$SEEDRE" "$d/keys/operator.nk"
    chk "control: the regex does match a real seed (account.nk)" grep -Eq "$SEEDRE" "$d/keys/account.nk"
    chk "control: the JWT header is in mashadmin.creds" grep -q -- "$JWTRE" "$d/creds/mashadmin.creds"
    run --stop; cat "$W/out" "$W/err" >"$W/stop.txt"
    chk "--stop output has no seed" bash -c '! grep -Eq "$1" "$2"' _ "$SEEDRE" "$W/stop.txt"
    run --start; cat "$W/out" "$W/err" >"$W/start.txt"
    chk "--start output has no seed" bash -c '! grep -Eq "$1" "$2"' _ "$SEEDRE" "$W/start.txt"
    chk "server.log after --start has no seed" bash -c '! grep -Eq "$1" "$2"' _ "$SEEDRE" "$d/server.log"
    chk "server.log.1 has no seed" bash -c '! grep -Eq "$1" "$2"' _ "$SEEDRE" "$d/server.log.1"
    stopdir "$d"
}

case6() { # the server works
    local d=$H/mash-nats p
    run --owner acme
    chk "setup ran" rc_is 0
    p=$(port_of "$d")
    NN_CRED=mashadmin.creds
    chk "stream add S1 (file storage)" nn "$d" "$p" stream add S1 --subjects 's.>' --defaults --storage file
    chk "publish to the stream" nn "$d" "$p" pub s.a hello-mash
    nn "$d" "$p" stream get S1 1 >"$W/sget" 2>&1
    chk "read the message back from the stream" has "$W/sget" hello-mash
    chk "kv add B1" nn "$d" "$p" kv add B1
    chk "kv put B1 k" nn "$d" "$p" kv put B1 k v-mash
    nn "$d" "$p" kv get B1 k --raw >"$W/kvget" 2>&1
    chk "kv get B1 k returns the value" has "$W/kvget" v-mash
    nn "$d" "$p" account info >"$W/ainfo" 2>&1
    chk "wire: JetStream max streams 64" hasre "$W/ainfo" 'Streams: +[0-9]+ of 64$'
    chk "wire: JetStream max consumers 128" hasre "$W/ainfo" 'Consumers: Maximum 128 per stream$'
    chk "wire: JetStream max file storage 2.0 GiB" hasre "$W/ainfo" 'Storage: +[0-9.]+ [A-Za-z]+ of 2\.0 GiB'
    chk "wire: JetStream memory 0" hasre "$W/ainfo" 'Memory: +0 B of 0 B'
    XDG_DATA_HOME="$d/nats-store" XDG_CONFIG_HOME="$d/nats-store/config" \
        nats auth account info acme --operator mash >"$W/jwt" 2>&1
    chk "JWT: account is named acme" hasre "$W/jwt" '^Account acme \(A[A-Z0-9]+\)$'
    chk "JWT: JetStream on" hasre "$W/jwt" 'JetStream: +true'
    chk "JWT: max streams 64" hasre "$W/jwt" 'Maximum Streams: +64$'
    chk "JWT: max consumers 128" hasre "$W/jwt" 'Max Consumers Per Stream: +128$'
    chk "JWT: max file storage 2.0 GiB" hasre "$W/jwt" 'Max File Storage: +2\.0 GiB$'
    chk "the admin user is in account acme (wire account = JWT account)" \
        [ "$(sed -n 's/^ *Account: *//p' "$W/ainfo")" = "$(sed -n '1s/^Account acme (\(.*\))$/\1/p' "$W/jwt")" ]
    XDG_DATA_HOME="$d/nats-store" XDG_CONFIG_HOME="$d/nats-store/config" \
        nats auth account info SYSTEM --operator mash >"$W/sysjwt" 2>&1
    chk "JWT: the system account is named SYSTEM" hasre "$W/sysjwt" '^Account SYSTEM \(A[A-Z0-9]+\)$'
    chk "JWT: system account has the system flag" hasre "$W/sysjwt" 'System Account: +true'
    NN_CRED=sys.creds
    nn "$d" "$p" server ping >"$W/ping" 2>&1
    chk "sys.creds: nats server ping works" has "$W/ping" 'mash-local'
    NN_CRED=mashadmin.creds
    XDG_CONFIG_HOME="$W/xdgc" nats --server "tls://127.0.0.1:$p" --tlsca "$d/ca.pem" pub x y >"$W/noauth" 2>&1
    RC=$?
    chk "no creds: refused" [ "$RC" != 0 ]
    chk "no creds: Authorization Violation" has "$W/noauth" 'Authorization Violation'
    XDG_CONFIG_HOME="$W/xdgc" nats --server "nats://127.0.0.1:$p" \
        --creds "$d/creds/mashadmin.creds" pub x y >"$W/notls" 2>&1
    RC=$?
    chk "no CA given: refused" [ "$RC" != 0 ]
    python3 "$W/rawplain.py" "$p" >"$W/raw" 2>&1
    chk "plain-text (non-TLS) client gets no PONG" has "$W/raw" 'no-pong'
    stopdir "$d"
}

case7() { # --stop / --start
    local d=$H/mash-nats p0 p1 p2
    ROUTE=file
    run --owner acme
    chk "setup ran" rc_is 0
    p=$(port_of "$d"); p0=$(pid_of "$d")
    nn "$d" "$p" stream add S1 --subjects 's.>' --defaults --storage file >/dev/null 2>&1
    nn "$d" "$p" pub s.a persisted >/dev/null 2>&1
    run --stop --dir "$d"
    chk "--stop: exit 0" rc_is 0
    chk "--stop: says stopped pid $p0" hasx "$W/out" "stopped pid $p0"
    chk "--stop: the server is gone" gone "$p0"
    chk "--stop: server.pid removed" absent "$d/server.pid"
    chk "--stop: the log is kept" [ -s "$d/server.log" ]
    run --stop --dir "$d"
    chk "--stop again: exit 0" rc_is 0
    chk "--stop again: says not running" hasx "$W/out" 'not running'
    run --start --dir "$d"
    chk "--start: exit 0" rc_is 0
    p1=$(pid_of "$d")
    chk "--start: a new pid ($p1 vs $p0)" differs "$p1" "$p0"
    chk "--start: says started with that pid" has "$W/out" "started: nats-server pid $p1 on 127.0.0.1:"
    chk "--start: the server is alive" alive "$p1"
    chk "--start: the old log is kept as server.log.1" [ -s "$d/server.log.1" ]
    chk "--start: the stream survived the restart" nn "$d" "$p" stream info S1
    nn "$d" "$p" stream get S1 1 >"$W/sget" 2>&1
    chk "--start: the message survived the restart" has "$W/sget" persisted
    run --start --dir "$d"
    chk "--start when running: exit 0" rc_is 0
    chk "--start when running: says already running" has "$W/out" 'already running'
    chk "--start when running: same pid" [ "$(pid_of "$d")" = "$p1" ]
    ROUTE=pipe
    run --stop --dir "$d"
    chk "--stop, piped route: exit 0" rc_is 0
    chk "--stop, piped route: server gone" gone "$p1"
    run --start --dir "$d"; p2=$(pid_of "$d")
    chk "--start, piped route: exit 0" rc_is 0
    chk "--start, piped route: a live pid" alive "$p2"
    stopdir "$d"
}

snap() { # DIR: names and checksums of the files a failed command must not touch
    find "$1" -path '*/jetstream' -prune -o -path '*/resolver' -prune -o -path '*/server.log*' -prune \
        -o -type f -exec cksum {} + | sort -k3
}

case8() { # argument and path checking
    local d=$H/mash-nats o n s0 s1 pid
    ROUTE=file
    run --start
    chk "--start on a missing dir: exit 1" rc_is 1
    chk "--start missing: error: no setup found" has "$W/err" "error: no setup found at $d"
    chk "--start missing: hint has the create command" has "$W/err" 'to make one, run: mash-local-nats-setup.sh --owner'
    run --start --dir "$H/zz"
    chk "--start missing, custom dir: hint repeats --dir" has "$W/err" "--dir $H/zz"
    run --stop
    chk "--stop on a missing dir: exit 1" rc_is 1
    run --remove
    chk "--remove on a missing dir: exit 1" rc_is 1
    run --owner acme
    chk "setup ran" rc_is 0
    pid=$(pid_of "$d"); s0=$(snap "$d")
    run --start --owner other
    chk "--start --owner other: exit 1" rc_is 1
    chk "--start --owner other: says it differs" has "$W/err" 'differs from the recorded owner acme'
    run --stop --owner other
    chk "--stop --owner other: exit 1" rc_is 1
    s1=$(snap "$d")
    chk "owner mismatch: nothing changed" [ "$s0" = "$s1" ]
    chk "owner mismatch: server still running" alive "$pid"
    run --owner acme
    chk "dir exists: exit 1" rc_is 1
    chk "dir exists: says already exists" has "$W/err" 'already exists'
    chk "dir exists: left alone" [ "$(snap "$d")" = "$s0" ]
    chk "dir exists: server still running" alive "$pid"
    run --bogus
    chk "unknown flag: exit 2" rc_is 2
    chk "unknown flag: error: names it" has "$W/err" 'error: unknown option: --bogus'
    run --dir
    chk "--dir without a value: exit 2" rc_is 2
    run --owner
    chk "--owner without a value: exit 2" rc_is 2
    run --stop --start
    chk "--stop --start: exit 2" rc_is 2
    run --help
    chk "--help: exit 0" rc_is 0
    chk "--help: usage text" has "$W/out" 'usage: mash-local-nats-setup.sh'
    run --dir /tmp/mlnt-out --owner acme
    chk "--dir outside \$HOME: exit 1" rc_is 1
    chk "--dir outside \$HOME: nothing made" absent /tmp/mlnt-out
    run --dir "$H" --owner acme
    chk "--dir \$HOME itself: exit 1" rc_is 1
    run --dir relative/dir --owner acme
    chk "relative --dir: exit 1" rc_is 1
    run --dir "$H/a/../b" --owner acme
    chk "--dir with ..: exit 1" rc_is 1
    chk "--dir with ..: nothing made" absent "$H/b"
    run --dir "$H/no/parent" --owner acme
    chk "--dir with a missing parent: exit 1" rc_is 1
    mkdir "$H/real"; ln -s "$H/real" "$H/lnk"
    run --dir "$H/lnk" --owner acme
    chk "symlinked --dir: exit 1" rc_is 1
    chk "symlinked --dir: says symlink" has "$W/err" 'symlink'
    chk "symlinked --dir: target untouched" [ -z "$(ls -A "$H/real")" ]
    run --dir "$H/lnk/sub" --owner acme
    chk "--dir under a symlink: exit 1" rc_is 1
    run --start --dir "$H/lnk"
    chk "--start on a symlinked dir: exit 1" rc_is 1
    run --stop --dir "$H/lnk"
    chk "--stop on a symlinked dir: exit 1" rc_is 1
    mkdir "$H/notours"
    run --start --dir "$H/notours"
    chk "--start on a dir we did not make: exit 1" rc_is 1
    chk "--start on a dir we did not make: says so" has "$W/err" 'was not made by this script'
    for o in Acme 1abc a--b abc- ACME 'a_b' "$(printf 'a%.0s' $(seq 33))"; do
        n=${o:0:12}
        run --owner "$o" --dir "$H/inv"
        chk "invalid owner '$n' (${#o} chars): exit 1" rc_is 1
        chk "invalid owner '$n': error: line and hint" has "$W/err" 'invalid owner name'
        chk "invalid owner '$n': nothing made" absent "$H/inv"
    done
    stopdir "$d"
}

case9() { # owner prompt, tty and no tty
    local a=$H/d9a b=$H/d9b c=$H/d9c e=$H/d9e
    ROUTE=pipe
    run --dir "$a"
    chk "no --owner, no tty (pipe route): exit 4" rc_is 4
    chk "no tty: error says no terminal" has "$W/err" 'no terminal to ask for the owner name'
    chk "no tty: hint says pass --owner" has "$W/err" 'pass --owner NAME'
    chk "no tty: nothing made" absent "$a"
    ROUTE=file
    run --dir "$a"
    chk "no --owner, no tty (file route): exit 4" rc_is 4
    chk "no tty: nothing made" absent "$a"
    ptyrun "" bash "$SCRIPT" --dir "$a"
    chk "pty, Enter: exit 0" rc_is 0
    chk "pty, Enter: the prompt offers [tester]" has "$W/out" 'owner name [tester]: '
    chk "pty, Enter: owner is the login name tester" hasx "$a/mash-setup.yaml" 'owner: tester'
    chk "pty, Enter: last output line is the mashctl command" \
        [ "$(grep . "$W/out" | tail -n 1)" = "mashctl init --config $a/mash-setup.yaml" ]
    stopdir "$a"
    ptyrun acme bash "$SCRIPT" --dir "$b"
    chk "pty, typed acme: exit 0" rc_is 0
    chk "pty, typed acme: owner acme" hasx "$b/mash-setup.yaml" 'owner: acme'
    stopdir "$b"
    ptyrun acme bash -c 'cat "$0" | bash -s -- --dir "$1"' "$SCRIPT" "$c"
    chk "piped route with a pty on /dev/tty, typed acme: exit 0" rc_is 0
    chk "piped route with a pty: owner acme" hasx "$c/mash-setup.yaml" 'owner: acme'
    stopdir "$c"
    ptyrun Acme bash "$SCRIPT" --dir "$e"
    chk "pty, typed Acme: exit 1" rc_is 1
    chk "pty, typed Acme: invalid owner name" has "$W/out" 'invalid owner name'
    chk "pty, typed Acme: nothing made" absent "$e"
}

case10() { # --remove
    local d=$H/mash-nats pid
    ROUTE=file
    run --owner acme
    chk "setup ran" rc_is 0
    pid=$(pid_of "$d")
    run --remove
    chk "--remove, no tty: exit 4" rc_is 4
    chk "--remove, no tty: says it needs a terminal" has "$W/err" '--remove needs a terminal'
    chk "--remove, no tty: dir still there" [ -d "$d" ]
    chk "--remove, no tty: server still running" alive "$pid"
    ptyrun "$H/wrong" bash "$SCRIPT" --remove
    chk "--remove, wrong path typed: exit 1" rc_is 1
    chk "--remove, wrong path typed: says it does not match" has "$W/out" 'the path typed does not match'
    chk "--remove, wrong path typed: dir still there" [ -d "$d" ]
    chk "--remove, wrong path typed: server still running" alive "$pid"
    ptyrun "" bash "$SCRIPT" --remove
    chk "--remove, Enter only: exit 1" rc_is 1
    chk "--remove, Enter only: dir still there" [ -d "$d" ]
    ptyrun "$d/" bash "$SCRIPT" --remove
    chk "--remove, path with a trailing slash: refused (exit 1)" rc_is 1
    chk "--remove, path with a trailing slash: dir still there" [ -d "$d" ]
    ptyrun "$d" bash -c 'cat "$0" | bash -s -- --remove' "$SCRIPT"
    chk "--remove, right path, piped route: exit 0" rc_is 0
    chk "--remove: says removed" has "$W/out" "removed $d"
    chk "--remove: the dir is gone" absent "$d"
    chk "--remove: the server is gone" gone "$pid"
    # file route, second dir
    run --owner acme --dir "$H/d10"; pid=$(pid_of "$H/d10")
    ptyrun "$H/d10" bash "$SCRIPT" --remove --dir "$H/d10"
    chk "--remove --dir, right path, file route: exit 0" rc_is 0
    chk "--remove --dir: dir gone and server gone" absent "$H/d10"
    chk "--remove --dir: server gone" gone "$pid"
    chk "--remove did not touch \$HOME itself" [ -d "$H" ]
}

case11() { # port already taken
    local d=$H/mash-nats d2=$H/mash-b DP NP pid
    ROUTE=pipe
    python3 -m http.server 4222 --bind 127.0.0.1 >"$W/dummy.log" 2>&1 &
    DP=$!
    chk "control: the dummy listener is up on 4222" waitlisten 4222
    run --owner acme
    chk "dummy on 4222: setup exit 0" rc_is 0
    chk "dummy on 4222: says port 4223" hasx "$W/out" 'port: 4223 (4222 is used by another server, left alone)'
    chk "dummy on 4222: says it left 4222 alone" has "$W/out" 'left alone'
    chk "dummy on 4222: yaml says 4223" hasx "$d/mash-setup.yaml" 'servers: [tls://127.0.0.1:4223]'
    chk "dummy on 4222: our server listens on 4223" listening 4223
    chk "dummy on 4222: the dummy is still alive" alive "$DP"
    chk "dummy on 4222: the dummy still answers HTTP" \
        [ "$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:4222/)" = 200 ]
    run --stop
    chk "dummy on 4222: our --stop leaves the dummy alone" alive "$DP"
    chk "dummy on 4222: our server is stopped" bash -c '! ss -ltn | grep -q 127.0.0.1:4223'
    python3 -m http.server 4223 --bind 127.0.0.1 >"$W/dummy2.log" 2>&1 &
    NP=$!
    chk "control: a dummy on 4223 is up" waitlisten 4223
    ROUTE=file
    run --start
    chk "--start with the port taken: exit 1" rc_is 1
    chk "--start with the port taken: says port 4223 is used" has "$W/err" 'port 4223 is now used by another program'
    chk "--start with the port taken: no server.pid left" absent "$d/server.pid"
    chk "--start with the port taken: dummy untouched" alive "$NP"
    kill "$DP" "$NP"; wait "$DP" "$NP" 2>/dev/null
    chk "dummies stopped by recorded pid" bash -c '! ss -ltn | grep -Eq "127.0.0.1:422[23]"'
    ROUTE=pipe
    nats-server -a 127.0.0.1 -p 4222 >"$W/ns.log" 2>&1 &
    NP=$!
    chk "control: a packaged nats-server is up on 4222" waitlisten 4222
    run --owner beta --dir "$d2"
    chk "nats-server on 4222: setup exit 0" rc_is 0
    chk "nats-server on 4222: says port 4223, 4222 left alone" \
        hasx "$W/out" 'port: 4223 (4222 is used by another server, left alone)'
    chk "nats-server on 4222: it is still alive" alive "$NP"
    chk "nats-server on 4222: it still serves" \
        plain_pub
    chk "nats-server on 4222: ours has its own pid" [ "$(pid_of "$d2")" != "$NP" ]
    pid=$(pid_of "$d2")
    run --stop --dir "$d2"
    chk "nats-server on 4222: --stop stops only ours" gone "$pid"
    chk "nats-server on 4222: still alive after our --stop" alive "$NP"
    kill "$NP"; wait "$NP" 2>/dev/null
    chk "packaged server stopped by recorded pid" gone "$NP"
}

case12() { # failure paths
    local d=$H/mash-nats pat real seed jwt
    real=$(command -v nats-server)
    ROUTE=pipe
    mkdir -p "$W/shim"
    for pat in 'auth operator add' 'auth account add' 'auth account edit' 'auth user add' 'auth account info'; do
        printf '#!/bin/sh\ncase "$*" in *"%s"*) exit 1 ;; esac\nexec %s "$@"\n' "$pat" "$(command -v nats)" \
            >"$W/shim/nats"
        chmod 755 "$W/shim/nats"
        PRE=(env "PATH=$W/shim:$PATH"); run --owner acme
        chk "nats '$pat' fails: exit 1" rc_is 1
        chk "nats '$pat' fails: an error: line is shown" hasre "$W/err" '^error: '
        chk "nats '$pat' fails: the dir is removed" absent "$d"
    done
    rm -f "$W/shim/nats"
    # bad config at start: a nats-server shim that breaks server.conf and logs fake secrets
    seed=SU$(printf 'A%.0s' $(seq 56)); jwt=eyJ$(printf 'a%.0s' $(seq 60))
    cat >"$W/shim/nats-server" <<SH
#!/bin/sh
case "\$1" in -v|--version) exec $real "\$@" ;; esac
echo "$seed" >&2
echo "token $jwt" >&2
echo "-----BEGIN USER NKEY SEED-----" >&2
printf 'bogus_directive: [\n' >>"\$2"
exec $real "\$@"
SH
    chmod 755 "$W/shim/nats-server"
    PRE=(env "PATH=$W/shim:$PATH"); run --owner acme
    chk "bad server.conf: exit 1" rc_is 1
    chk "bad server.conf: says the server did not come up" has "$W/err" 'error: the server did not come up'
    chk "bad server.conf: log lines are shown" hasre "$W/err" '^  [^ ]'
    chk "bad server.conf: the hint mentions --remove" hasre "$W/err" '^hint: .*--remove'
    chk "bad server.conf: the dir is kept" [ -f "$d/mash-setup.json" ]
    chk "bad server.conf: the log has the fake seed (control)" has "$d/server.log" "$seed"
    chk "bad server.conf: no seed shown" lacks "$W/err" "$seed"
    chk "bad server.conf: no JWT shown" lacks "$W/err" "$jwt"
    chk "bad server.conf: no NKEY marker shown" lacks "$W/err" 'BEGIN USER NKEY SEED'
    chk "bad server.conf: no seed regex match in output" bash -c '! grep -Eq "$1" "$2"' _ "$SEEDRE" "$W/err"
    chk "bad server.conf: no stale server.pid" nopid "$d/server.pid"
    PRE=()
    ptyrun "$d" bash "$SCRIPT" --remove
    chk "bad server.conf: --remove (as the hint says) removes it" absent "$d"
    chk "bad server.conf: --remove exit 0" rc_is 0
}

case13() { # the server survives a hangup of the shell that started it
    local d=$H/mash-nats pid p
    ptyrun __none__ bash -c 'bash "$0" --owner acme >"$2" 2>&1; exit' "$SCRIPT" x "$W/o13"
    chk "script from a pty shell that then exits: shell exit 0" rc_is 0
    chk "the script itself succeeded" has "$W/o13" 'started: nats-server pid'
    pid=$(pid_of "$d")
    sleep 2
    chk "server alive after its terminal's shell exited (SIGHUP to the group)" alive "$pid"
    p=$(port_of "$d")
    chk "server still serves after the hangup" nn "$d" "$p" account info
    kill -HUP "$pid"; sleep 1
    chk "server survives a direct SIGHUP" alive "$pid"
    chk "server still serves after SIGHUP" nn "$d" "$p" account info
    stopdir "$d"
    chk "stopped" gone "$pid"
    ptyrun __none__ bash -c 'bash "$0" --start --dir "$1" >"$2" 2>&1; exit' "$SCRIPT" "$d" "$W/o13b"
    pid=$(pid_of "$d")
    sleep 2
    chk "--start from a pty shell that exits: server alive" alive "$pid"
    # piped route, plain shell that exits (no pty)
    stopdir "$d"
    bash -c 'cat "$0" | bash -s -- --start --dir "$1" >"$2" 2>&1; exit' "$SCRIPT" "$d" "$W/o13c" </dev/null
    pid=$(pid_of "$d")
    sleep 2
    chk "--start, piped, from a shell that exits: server alive" alive "$pid"
    stopdir "$d"
}

case14() { # two dirs at once, different ports
    local a=$H/mash-nats b=$H/d2 pa pb
    ROUTE=pipe
    run --owner acme; check_success "$a" acme
    run --owner beta --dir "$b"; check_success "$b" beta
    pa=$(port_of "$a"); pb=$(port_of "$b")
    chk "different ports ($pa, $pb)" [ "$pa" != "$pb" ]
    chk "both listen" bash -c "ss -ltn | grep -q 127.0.0.1:$pa && ss -ltn | grep -q 127.0.0.1:$pb"
    chk "both pids differ" differs "$(pid_of "$a")" "$(pid_of "$b")"
    chk "both pids alive" alive "$(pid_of "$a")"
    chk "both pids alive (B)" alive "$(pid_of "$b")"
    chk "A's admin creds work on A" nn "$a" "$pa" account info
    chk "B's admin creds work on B" nn "$b" "$pb" account info
    chk "A's creds are refused by B (separate operators)" bash -c '! "$@"' _ \
        nats --server "tls://127.0.0.1:$pb" --tlsca "$b/ca.pem" --creds "$a/creds/mashadmin.creds" \
        --timeout 3s account info
    nn "$b" "$pb" stream add SB --subjects 'b.>' --defaults --storage file >/dev/null 2>&1
    chk "B's stream is B's own (A has none)" bash -c '! "$@" 2>/dev/null' _ \
        nats --server "tls://127.0.0.1:$pa" --tlsca "$a/ca.pem" --creds "$a/creds/mashadmin.creds" stream info SB
    pid=$(pid_of "$a")
    run --stop --dir "$a"
    chk "--stop on A: exit 0" rc_is 0
    chk "--stop on A: A gone" gone "$(printf '%s' "${pid:-0}")"
    chk "--stop on A: B still alive and serving" nn "$b" "$pb" account info
    run --start --dir "$a"
    chk "--start on A again: exit 0" rc_is 0
    chk "--start on A again: the same port" [ "$(port_of "$a")" = "$pa" ]
    chk "A serves again" nn "$a" "$pa" account info
    stopdir "$a"; stopdir "$b"
}

run_case() {
    case $CASE in
    1) case1 ;; 2) case2 ;; 3) case3 ;; 4) case4 ;; 5) case5 ;; 6) case6 ;; 7) case7 ;;
    8) case8 ;; 9) case9 ;; 10) case10 ;; 11) case11 ;; 12) case12 ;; 13) case13 ;; 14) case14 ;;
    *) echo "FAIL: c$CASE: no such case" ;;
    esac
}
run_case
printf 'done: c%s\n' "$CASE"
}

if [ "${MLNT_INSIDE:-}" = 1 ]; then
    inside_main "$@"
    exit 0
fi

# ======================================================================
# Part 2: the host side (podman only)
# ======================================================================
set -u
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P) || exit 2
SELF_TEST=$HERE/$(basename "${BASH_SOURCE[0]}")
SCRIPT_SRC=${MLNT_SCRIPT:-$HERE/mash-local-nats-setup.sh}  # MLNT_SCRIPT: test another copy
[ -f "$SCRIPT_SRC" ] || { echo "no $SCRIPT_SRC" >&2; exit 2; }
for t in podman curl sha256sum df awk timeout; do
    command -v "$t" >/dev/null 2>&1 || { echo "needs $t" >&2; exit 2; }
done

SCR_DEFAULT=/tmp/claude-1000/-home-randyb-code-mash-wt-policy-mash
SCR_DEFAULT=$SCR_DEFAULT/a96abb56-309c-4e6b-bf1f-73e29107fede/scratchpad/p1t
SCRATCH=${MLNT_SCRATCH:-$SCR_DEFAULT}
IMAGES=${MLNT_IMAGES:-"debian:stable ubuntu:24.04 ubuntu:26.04"}
CASES=${MLNT_CASES:-"1 2 3 4 5 6 7 8 9 10 11 12 13 14"}
NCV=${MLNT_NATSCLI:-0.5.2}
RUN=$(date +%m%d%H%M%S)-$$
mkdir -p "$SCRATCH" || exit 2
REPORT=${MLNT_REPORT:-$SCRATCH/receipt-$RUN.md}
CTX=$SCRATCH/build-$RUN
LOGS=$SCRATCH/logs-$RUN
MY_CONTAINERS=()
MY_IMAGE_IDS=()
CLEANED='not run'

free_gb() { df -P -k "$1" | awk 'NR==2 {printf "%d", $4 / 1048576}'; }
free_mb() { df -P -k "$1" | awk 'NR==2 {printf "%d", $4 / 1024}'; }

cleanup() {
    local n i rm_ok=0 left
    trap - EXIT INT TERM
    for n in "${MY_CONTAINERS[@]+"${MY_CONTAINERS[@]}"}"; do podman rm -f "$n" >/dev/null 2>&1; done
    for i in "${MY_IMAGE_IDS[@]+"${MY_IMAGE_IDS[@]}"}"; do podman rmi -f "$i" >/dev/null 2>&1; done
    case $CTX in "$SCRATCH"/build-*) rm -rf -- "$CTX" ;; esac
    left=0
    for n in "${MY_CONTAINERS[@]+"${MY_CONTAINERS[@]}"}"; do
        podman container exists "$n" 2>/dev/null && left=$((left + 1))
    done
    for i in "${MY_IMAGE_IDS[@]+"${MY_IMAGE_IDS[@]}"}"; do
        podman image exists "$i" 2>/dev/null && left=$((left + 1))
    done
    [ "$left" = 0 ] && rm_ok=1
    if [ "$rm_ok" = 1 ]; then
        CLEANED="done: ${#MY_CONTAINERS[@]} containers removed by name, ${#MY_IMAGE_IDS[@]} images by id,"
        CLEANED="$CLEANED build dir removed; none left"
    else
        CLEANED="INCOMPLETE: $left of this run's objects still exist"
    fi
    write_receipt
    echo "cleanup: $CLEANED"
    echo "receipt: $REPORT"
}

PASS=0; FAIL=0
declare -A DIG
RESULTS=''
IMGLINES=''
DISK0=''
NATSHA=''
SCRIPTSHA=$(sha256sum "$SCRIPT_SRC" | awk '{print $1}')

write_receipt() {
    {
        echo "# mash-local-nats-setup.sh container test receipt"
        echo
        echo "- run id: $RUN"
        echo "- date (UTC): $(date -u +%Y-%m-%dT%H:%M:%SZ)"
        echo "- test: scripts/mash-local-nats-setup_test.sh, sha256 $(sha256sum "$SELF_TEST" | awk '{print $1}')"
        echo "- script under test: scripts/mash-local-nats-setup.sh, sha256 $SCRIPTSHA"
        echo "- natscli v$NCV linux-amd64 zip, sha256 $NATSHA"
        echo "- podman: $(podman --version 2>/dev/null)"
        echo
        echo "## Images"
        echo
        printf '%s\n' "$IMGLINES"
        echo
        echo "## Totals"
        echo
        echo "- ok: $PASS"
        echo "- FAIL: $FAIL"
        echo "- host free disk before: ${DISK0:-?} MB, after: $(free_mb "$SCRATCH") MB"
        echo "- cleanup: $CLEANED"
        echo
        echo "## Cases"
        echo
        printf '%s\n' "$RESULTS"
    } >"$REPORT" 2>/dev/null
}
trap cleanup EXIT
trap 'exit 130' INT TERM

# ---- venue checks
GRAPH=$(podman info --format '{{.Store.GraphRoot}}' 2>/dev/null)
[ -n "$GRAPH" ] || { echo "cannot read the podman graph root" >&2; exit 2; }
for p in "$GRAPH" "$SCRATCH"; do
    g=$(free_gb "$p")
    if [ "${g:-0}" -le 20 ]; then
        echo "refusing: only ${g:-?} GB free under $p (need more than 20 GB)" >&2
        exit 2
    fi
done
DISK0=$(free_mb "$SCRATCH")
[ "$(id -u)" -ne 0 ] || { echo "run as an ordinary user (rootless podman)" >&2; exit 2; }

mkdir -p "$CTX" "$LOGS" || exit 2
echo "== run $RUN: images: $IMAGES; cases: $CASES"
echo "== downloading natscli v$NCV"
curl -fsSL -o "$CTX/nats.zip" \
    "https://github.com/nats-io/natscli/releases/download/v$NCV/nats-$NCV-linux-amd64.zip" || {
    echo "cannot download natscli v$NCV" >&2; exit 2; }
NATSHA=$(sha256sum "$CTX/nats.zip" | awk '{print $1}')
echo "natscli sha256 $NATSHA"
cat >"$CTX/Containerfile" <<'CF'
ARG BASE
FROM ${BASE}
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    nats-server openssl iproute2 curl unzip ca-certificates util-linux python3 \
    && rm -rf /var/lib/apt/lists/*
COPY nats.zip /tmp/nats.zip
RUN unzip -j -o /tmp/nats.zip '*/nats' -d /usr/local/bin && chmod 755 /usr/local/bin/nats && rm /tmp/nats.zip
RUN useradd -m -s /bin/bash tester
CF

for tag in $IMAGES; do
    short=$(printf '%s' "$tag" | tr ':.' '--')
    img=localhost/mlnt-$RUN-$short
    echo "== building $img from $tag"
    if ! podman build -q --pull=missing --build-arg "BASE=$tag" -t "$img" -f "$CTX/Containerfile" "$CTX" \
        >"$LOGS/build-$short.log" 2>&1; then
        echo "FAIL: build of $tag failed; see $LOGS/build-$short.log"
        FAIL=$((FAIL + 1)); RESULTS="$RESULTS
- FAIL [$tag] image build failed"
        continue
    fi
    id=$(podman image inspect --format '{{.Id}}' "$img")
    MY_IMAGE_IDS+=("$id")
    bdig=$(podman image inspect --format '{{.Digest}}' "$tag" 2>/dev/null)
    brd=$(podman image inspect --format '{{index .RepoDigests 0}}' "$tag" 2>/dev/null)
    DIG[$tag]=$bdig
    IMGLINES="$IMGLINES
- $tag: digest ${bdig:-?} (${brd:-no repo digest}); built image $img id $id"
done

for tag in $IMAGES; do
    short=$(printf '%s' "$tag" | tr ':.' '--')
    img=localhost/mlnt-$RUN-$short
    podman image exists "$img" 2>/dev/null || continue
    RESULTS="$RESULTS

### $tag"
    for n in $CASES; do
        name=mlnt-$RUN-$short-c$n
        user=tester
        [ "$n" = 1 ] && user=root
        MY_CONTAINERS+=("$name")
        echo "== [$tag] case $n"
        timeout 600 podman run --rm --name "$name" --user "$user" --network none \
            --security-opt no-new-privileges --cap-drop=all --pids-limit 2048 \
            -e MLNT_INSIDE=1 \
            -v "$SELF_TEST:/t/test.sh:ro" -v "$SCRIPT_SRC:/t/mash-local-nats-setup.sh:ro" \
            "$img" bash /t/test.sh "$n" >"$LOGS/$short-c$n.log" 2>&1
        rc=$?
        podman rm -f "$name" >/dev/null 2>&1
        po=$(grep -c '^ok: ' "$LOGS/$short-c$n.log")
        pf=$(grep -c '^FAIL: ' "$LOGS/$short-c$n.log")
        if ! grep -q "^done: c$n\$" "$LOGS/$short-c$n.log"; then
            pf=$((pf + 1))
            echo "FAIL: [$tag] c$n did not finish (podman rc $rc)"
            RESULTS="$RESULTS
- FAIL [$tag] c$n did not finish (podman rc $rc); log $LOGS/$short-c$n.log"
        fi
        PASS=$((PASS + po)); FAIL=$((FAIL + pf))
        grep -E '^(FAIL|  )' "$LOGS/$short-c$n.log" | sed "s/^/[$tag] /"
        RESULTS="$RESULTS
$(grep -E '^(ok|FAIL): ' "$LOGS/$short-c$n.log" | sed "s/^/- [$tag] /")"
    done
done

echo "== totals: ok=$PASS FAIL=$FAIL"
[ "$FAIL" = 0 ] && [ "$PASS" -gt 0 ]
