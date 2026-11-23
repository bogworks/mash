#!/usr/bin/env bash
# Container test for scripts/mash-local-nats-setup.sh.  Run it with:
#
#     bash scripts/mash-local-nats-setup_test.sh
#
# Venue rules: rootless podman on this host only; one image per distro, built
# from an official tag (debian:stable, ubuntu:24.04, ubuntu:26.04); one
# container per case and mode, named after the run id, no ports published,
# network none (the loopback inside the container is all the script needs), a
# non-root user (tester) except for the root-refusal case; nothing is installed
# on the host.  On exit (also on failure) it removes only this run's containers,
# by name, this run's images, by recorded id, and its build directory.
#
# Two modes per image: "linux" (the script as it runs on Linux) and "darwin"
# (MLNT_UNAME=Darwin, so the script takes its macOS branches, with shims on PATH
# that behave like the BSD tools: a stat that rejects -c and takes -f, no ss and
# a lsof on top of it, a ps and a readlink that reject GNU-only flags, shasum
# instead of sha256sum).  That proves the BSD branches run and use no GNU-only
# flag; it does not prove they work with the real macOS tools.
#
# Environment:
#   MLNT_IMAGES   space-separated image tags (default: debian:stable ubuntu:24.04 ubuntu:26.04)
#   MLNT_CASES    space-separated case numbers (default: 1 2 ... 27)
#   MLNT_MODES    space-separated modes (default: linux darwin)
#   MLNT_REPORT   receipt file (markdown; default: receipt-<run id>.md in the scratch
#                 directory, or in ${TMPDIR:-/tmp} when the scratch directory is made here)
#   MLNT_SCRATCH  scratch directory (default: a new mktemp -d under ${TMPDIR:-/tmp},
#                 removed at the end only when the test made it and nothing failed)
#   MLNT_NATSCLI  natscli version (default 0.5.2; any other version needs MLNT_NATSCLI_SHA256)
#   MLNT_SCRIPT   test another copy of the script (default: scripts/mash-local-nats-setup.sh)
#   MLNT_MASHCTL  path to a LINUX amd64 static mashctl binary that has `init --config`; case 27
#                 copies it into the container and runs the script's printed `mashctl init` line
#                 twice (default unset: that part prints "skipped: MLNT_MASHCTL not set")
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
W=$(mktemp -d "${TMPDIR:-/tmp}/w.XXXXXX")
ROUTE=pipe
PRE=()
RC=0
SIM=${MLNT_SIM:-}          # "darwin": the shimmed, BSD-like PATH is set up below
REALSS=$(command -v ss)
REALSTAT=$(command -v stat)
REALPS=$(command -v ps)
REALREADLINK=$(command -v readlink)
REALSHA=$(command -v sha256sum || true)
export REALSS REALSTAT
SHIMS=''
SHIMLOG=$W/shim.log

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
err_is() { [ "$RC" = 1 ] && [ "$(cat "$W/err")" = "$1" ]; }
quiet_ok() { [ "$RC" = 0 ] && [ ! -s "$W/out" ] && [ ! -s "$W/err" ]; }
abs_exists() { case $1 in /*) [ -e "$1" ] ;; *) return 1 ;; esac; }
alive() {
    [ -d "/proc/$1" ] && ! grep -q '^State:[[:space:]]*Z' "/proc/$1/status" 2>/dev/null
}
differs() { [ -n "$1" ] && [ "$1" != "$2" ]; }
gone() { ! alive "$1"; }
pid_of() { head -n 1 "$1/server.pid" 2>/dev/null; }
port_of() { sed -n 's/^ *"port": *\([0-9]*\),*$/\1/p' "$1/mash-setup.json"; }
listening() { "$REALSS" -ltn | awk 'NR>1 {print $4}' | grep -Fxq "127.0.0.1:$1"; }
# only_listener PID PORT: every listening socket of PID is 127.0.0.1:PORT, and there is one
only_listener() {
    local l
    if [ "$SIM" = darwin ]; then # through the BSD-style lsof, as on a Mac
        l=$(lsof -a -p "$1" -iTCP -sTCP:LISTEN -nP -Fn | sed -n 's/^n//p' | sort -u)
    else
        l=$("$REALSS" -ltnp | awk -v pat="pid=$1," 'index($0, pat) {print $4}' | sort -u)
    fi
    [ "$l" = "127.0.0.1:$2" ]
}
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
        cat "$SCRIPT" | "${PRE[@]+"${PRE[@]}"}" bash -s -- "$@" >"$W/out" 2>"$W/err"
    else
        "${PRE[@]+"${PRE[@]}"}" bash "$SCRIPT" "$@" >"$W/out" 2>"$W/err" </dev/null
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
# (in the darwin mode: the BSD-like shims first, and never ss or sha256sum)
mkpath() {
    local d=$1 src f b x skip hide=''
    shift
    [ -z "$SHIMS" ] || hide='ss sha256sum'
    mkdir -p "$d"
    for src in ${SHIMS:+"$SHIMS"} /usr/local/bin /usr/bin /usr/sbin /bin /sbin; do
        for f in "$src"/*; do
            b=${f##*/}; skip=0
            for x in "$@" $hide; do [ "$b" = "$x" ] && skip=1; done
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


# sim_setup: the darwin mode.  Shims that behave like the macOS tools, on top of
# the real GNU ones by absolute path; every call is logged to $SHIMLOG ("OK" or
# "REJECT"), so a case can show which flags the script used.
sim_setup() {
    SHIMS=$W/sims
    mkdir -p "$SHIMS"
    : >"$SHIMLOG"
    cat >"$SHIMS/stat" <<SH
#!/bin/sh
# BSD stat: -L, -f FORMAT (%u %Lp %p); -c is an error, as on macOS
fol=''; fmt=''
while [ \$# -gt 0 ]; do
    case \$1 in
    -L) fol=-L; shift ;;
    -f) [ \$# -ge 2 ] || break; fmt=\$2; shift 2 ;;
    *) break ;;
    esac
done
if [ -z "\$fmt" ] || [ \$# -ne 1 ]; then
    echo "stat REJECT \$*" >>"$SHIMLOG"
    echo "stat: illegal option or usage: \$*" >&2
    exit 1
fi
echo "stat OK \$fol -f \$fmt \$1" >>"$SHIMLOG"
case \$fmt in
%u) exec $REALSTAT \$fol -c %u "\$1" ;;
%Lp) exec $REALSTAT \$fol -c %a "\$1" ;;
%p) h=\$($REALSTAT \$fol -c %f "\$1") || exit 1; printf '%o\n' "\$((0x\$h))" ;;
*) echo "stat REJECT format \$fmt" >>"$SHIMLOG"; echo "stat: bad format" >&2; exit 1 ;;
esac
SH
    cat >"$SHIMS/ps" <<SH
#!/bin/sh
# BSD ps: only the forms a Mac accepts and the script uses
case "\$*" in
"-p "[0-9]*" -o stat="|"-p "[0-9]*" -o comm="|"-ww -p "[0-9]*" -o args="|"-p "[0-9]*" -o comm=,args=")
    echo "ps OK \$*" >>"$SHIMLOG"; exec $REALPS "\$@" ;;
*) echo "ps REJECT \$*" >>"$SHIMLOG"; echo "ps: illegal option" >&2; exit 1 ;;
esac
SH
    cat >"$SHIMS/readlink" <<SH
#!/bin/sh
# BSD readlink: no -f (macOS before 12.3 has none)
case \${1:-} in -*) echo "readlink REJECT \$*" >>"$SHIMLOG"; echo "readlink: illegal option" >&2; exit 1 ;; esac
echo "readlink OK \$*" >>"$SHIMLOG"
exec $REALREADLINK "\$@"
SH
    cat >"$SHIMS/shasum" <<SH
#!/bin/sh
[ "\${1:-}" = -a ] && [ "\${2:-}" = 256 ] || { echo "shasum REJECT \$*" >>"$SHIMLOG"; exit 1; }
shift 2
exec ${REALSHA:-/usr/bin/sha256sum} "\$@"
SH
    cat >"$SHIMS/lsof" <<SH
#!/bin/sh
# BSD-ish lsof on top of ss: -n -P -iTCP[:PORT] -sTCP:LISTEN [-a -p PID] [-Fn]
port=''; pid=''; fn=0; and=0; tcp=0; lis=0
while [ \$# -gt 0 ]; do
    case \$1 in
    -n | -P | -nP | -Pn) ;;
    -a) and=1 ;;
    -iTCP) tcp=1 ;;
    -iTCP:*) tcp=1; port=\${1#-iTCP:} ;;
    -sTCP:LISTEN) lis=1 ;;
    -Fn) fn=1 ;;
    -p) [ \$# -ge 2 ] || exit 1; pid=\$2; shift ;;
    *) echo "lsof REJECT \$*" >>"$SHIMLOG"; echo "lsof: unknown option \$1" >&2; exit 1 ;;
    esac
    shift
done
if [ \$tcp = 0 ] || [ \$lis = 0 ] || { [ -n "\$pid" ] && [ \$and = 0 ]; }; then
    echo "lsof REJECT (needs -iTCP -sTCP:LISTEN, and -a with -p)" >>"$SHIMLOG"; exit 1
fi
echo "lsof OK port=\$port pid=\$pid F=\$fn" >>"$SHIMLOG"
$REALSS -ltnpH 2>/dev/null | awk -v port="\$port" -v pid="\$pid" -v fn="\$fn" '
{
    addr = \$4; n = split(addr, a, ":"); p = a[n]; host = substr(addr, 1, length(addr) - length(p) - 1)
    if (port != "" && p != port) next
    pv = 0; if (match(\$0, /pid=[0-9]+/)) pv = substr(\$0, RSTART + 4, RLENGTH - 4)
    if (pid != "" && pv != pid) next
    if (host == "0.0.0.0" || host == "[::]") host = "*"
    found = 1
    if (fn) printf "p%s\nf0\nn%s:%s\n", pv, host, p
    else printf "x %s u 0u IPv4 0 0t0 TCP %s:%s (LISTEN)\n", pv, host, p
}
END { exit(found ? 0 : 1) }'
SH
    chmod 755 "$SHIMS"/*
    mkpath "$W/dbin"
    export PATH="$W/dbin"
    hash -r
}

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
    ROUTE="file"; run --owner acme
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
    ROUTE="file"
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
    ROUTE="file"; run --owner acme --dir "$d2"
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
    local d=$H/mash-nats p pid
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
    XDG_CONFIG_HOME="$W/xdgc" nats --server "tls://127.0.0.1:$p" \
        --creds "$d/creds/mashadmin.creds" --timeout 3s pub x y >"$W/noca" 2>&1
    RC=$?
    chk "tls without the CA: refused" [ "$RC" != 0 ]
    chk "tls without the CA: the message names certificate verification (unknown authority)" \
        hasre "$W/noca" 'certificate signed by unknown authority|failed to verify certificate'
    pid=$(pid_of "$d")
    chk "the server pid's only listener is 127.0.0.1:$p" only_listener "$pid" "$p"
    # what openssl generated: SANs, key types, the CA's limits
    openssl x509 -in "$d/server.pem" -noout -ext subjectAltName >"$W/san" 2>&1 </dev/null
    chk "server cert SAN: DNS:localhost" has "$W/san" 'DNS:localhost'
    chk "server cert SAN: IP 127.0.0.1" has "$W/san" 'IP Address:127.0.0.1'
    chk "server cert SAN: IP ::1" has "$W/san" 'IP Address:0:0:0:0:0:0:0:1'
    chk "server cert SAN: exactly these three" \
        [ "$(tr ',' '\n' <"$W/san" | grep -Ec 'DNS:|IP Address:')" = 3 ]
    openssl x509 -in "$d/server.pem" -noout -text >"$W/stext" 2>&1 </dev/null
    openssl x509 -in "$d/ca.pem" -noout -text >"$W/catext" 2>&1 </dev/null
    chk "server key is EC (id-ecPublicKey)" hasre "$W/stext" 'Public Key Algorithm: id-ecPublicKey'
    chk "server key curve is P-256 (prime256v1)" hasre "$W/stext" 'ASN1 OID: prime256v1'
    chk "CA key is EC P-256" hasre "$W/catext" 'ASN1 OID: prime256v1'
    chk "CA: basic constraints are critical" hasre "$W/catext" 'Basic Constraints: critical'
    chk "CA: CA:TRUE, pathlen:0" hasre "$W/catext" 'CA:TRUE, pathlen:0'
    chk "CA: name constraints present and critical" hasre "$W/catext" 'Name Constraints: critical'
    chk "CA: permitted DNS:localhost" hasre "$W/catext" 'DNS:localhost'
    chk "CA: permitted IP 127.0.0.1/255.255.255.255" has "$W/catext" 'IP:127.0.0.1/255.255.255.255'
    chk "CA: permitted IP ::1/ffff...ffff" \
        hasre "$W/catext" 'IP:0:0:0:0:0:0:0:1/FFFF:FFFF:FFFF:FFFF:FFFF:FFFF:FFFF:FFFF'
    chk "CA: nothing excluded" lacks "$W/catext" 'Excluded'
    chk "CA cert is signed with SHA-256" hasre "$W/catext" 'Signature Algorithm: ecdsa-with-SHA256'
    chk "server cert is signed with SHA-256 (not SHA-1, which Go refuses)" \
        hasre "$W/stext" 'Signature Algorithm: ecdsa-with-SHA256'
    chk "ca.key mode is 600" [ "$("$REALSTAT" -c %a "$d/ca.key")" = 600 ]
    chk "openssl verifies the server cert against the CA" openssl verify -CAfile "$d/ca.pem" "$d/server.pem"
    # the CA must not vouch for anything else: sign a cert for another name with its key
    openssl req -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -subj "/CN=evil.example.com" \
        -keyout "$W/evil.key" -out "$W/evil.csr" 2>/dev/null </dev/null
    printf 'subjectAltName=DNS:evil.example.com\n' >"$W/evil.ext"
    openssl x509 -req -in "$W/evil.csr" -CA "$d/ca.pem" -CAkey "$d/ca.key" -CAserial "$W/evil.srl" \
        -CAcreateserial -days 30 -extfile "$W/evil.ext" -out "$W/evil.pem" 2>/dev/null </dev/null
    openssl verify -CAfile "$d/ca.pem" "$W/evil.pem" >"$W/evilv" 2>&1 </dev/null
    RC=$?
    chk "a cert for evil.example.com signed by the CA does not verify" [ "$RC" != 0 ]
    chk "... because of the name constraint" hasre "$W/evilv" 'permitted subtree violation'
    python3 "$W/rawplain.py" "$p" >"$W/raw" 2>&1
    chk "plain-text (non-TLS) client gets no PONG" has "$W/raw" 'no-pong'
    stopdir "$d"
}

case7() { # --stop / --start
    local d=$H/mash-nats p0 p1 p2
    ROUTE="file"
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
    ROUTE="file"
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
    run --dir / --owner acme
    chk "--dir /: exit 1" rc_is 1
    chk "--dir /: says absolute path, not /" has "$W/err" 'absolute path, and not /'
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
    ROUTE="file"
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
    ROUTE="file"
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
    chk "--remove: says mashctl's folder still points at the server" has "$W/out" "/.config/mash still points at this server."
    chk "--remove: says how to set up again" has "$W/out" "mashctl init --config <file> --config-dir <new folder>"
    chk "--remove: the note lines fit 80 columns" bash -c '! grep -E "^(note|To set up|or move).{81,}" "$1"' _ "$W/out"
    chk "--remove: the dir is gone" absent "$d"
    chk "--remove: the server is gone" gone "$pid"
    # file route, second dir
    run --owner acme --dir "$H/d10"; pid=$(pid_of "$H/d10")
    ptyrun "$H/d10" env XDG_CONFIG_HOME=/xdg/cfg bash "$SCRIPT" --remove --dir "$H/d10"
    chk "--remove --dir, right path, file route: exit 0" rc_is 0
    chk "--remove: with an absolute XDG_CONFIG_HOME it names that folder" has "$W/out" "folder /xdg/cfg/mash still points"
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
    chk "dummy on 4222: our server is stopped" bash -c '! "$REALSS" -ltn | grep -q 127.0.0.1:4223'
    python3 -m http.server 4223 --bind 127.0.0.1 >"$W/dummy2.log" 2>&1 &
    NP=$!
    chk "control: a dummy on 4223 is up" waitlisten 4223
    ROUTE="file"
    run --start
    chk "--start with the port taken: exit 1" rc_is 1
    chk "--start with the port taken: says port 4223 is used" has "$W/err" 'port 4223 is now used by another program'
    chk "--start with the port taken: no server.pid left" absent "$d/server.pid"
    chk "--start with the port taken: dummy untouched" alive "$NP"
    kill "$DP" "$NP"; wait "$DP" "$NP" 2>/dev/null
    chk "dummies stopped by recorded pid" bash -c '! "$REALSS" -ltn | grep -Eq "127.0.0.1:422[23]"'
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
    chk "both listen" bash -c "\"\$REALSS\" -ltn | grep -q 127.0.0.1:$pa && \"\$REALSS\" -ltn | grep -q 127.0.0.1:$pb"
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

case15() { # macOS support: no refusal, the BSD branches (darwin mode), neither ss nor lsof
    local P=$W/p d=$H/dw n p
    ROUTE="file"
    # (a) neither ss nor lsof: a clear error, nothing made
    mkpath "$P" ss lsof
    PRE=(env "PATH=$P"); run --owner acme --dir "$H/nop"
    chk "no ss and no lsof: exit 1" rc_is 1
    chk "no ss and no lsof: the error line" hasx "$W/err" 'error: need ss (Linux) or lsof (macOS)'
    chk "no ss and no lsof: nothing created" absent "$H/nop"
    chk "no ss and no lsof: no Linux-only refusal text" bash -c '! grep -Eq "Linux only|Linux machine" "$1"' _ "$W/err"
    PRE=(); run --owner acme --dir "$d"
    chk "a made dir to try the other modes on: exit 0" rc_is 0
    p=$(port_of "$d")
    stopdir "$d"
    PRE=(env "PATH=$P"); run --start --dir "$d"
    chk "--start without ss and lsof: exit 1" rc_is 1
    chk "--start without ss and lsof: the error line" hasx "$W/err" 'error: need ss (Linux) or lsof (macOS)'
    chk "--start without ss and lsof: no server.pid" absent "$d/server.pid"
    ptyrun "$d" env "PATH=$P" bash "$SCRIPT" --remove --dir "$d"
    chk "--remove without ss and lsof: exit 1" rc_is 1
    chk "--remove without ss and lsof: the error line" has "$W/out" 'error: need ss (Linux) or lsof (macOS)'
    chk "--remove without ss and lsof: the dir is kept" [ -d "$d" ]
    PRE=(); ptyrun "$d" bash "$SCRIPT" --remove --dir "$d"
    chk "cleanup: --remove with the tools: exit 0" rc_is 0
    # (b) MLNT_UNAME does not make the script refuse anything
    PRE=(env MLNT_UNAME=Linux)
    run --owner acme --dir "$H/lx"
    chk "MLNT_UNAME=Linux: works as usual" rc_is 0
    stopdir "$H/lx"
    if [ "$SIM" != darwin ]; then
        chk "linux mode: no shims on PATH" [ -z "$SHIMS" ]
        return 0
    fi
    # (c) darwin mode: the same life cycle through the BSD branches
    chk "darwin mode: MLNT_UNAME is Darwin" [ "${MLNT_UNAME:-}" = Darwin ]
    chk "darwin mode: ss is not on PATH" bash -c '! command -v ss'
    chk "darwin mode: sha256sum is not on PATH" bash -c '! command -v sha256sum'
    chk "darwin mode: the stat shim rejects -c" bash -c '! stat -c %u / 2>/dev/null'
    chk "darwin mode: the stat shim takes -f %u" stat -f %u /
    chk "darwin mode: the readlink shim rejects -f" bash -c '! readlink -f / 2>/dev/null'
    chk "darwin mode: shasum -a 256 works" bash -c 'echo x | shasum -a 256 >/dev/null'
    : >"$SHIMLOG"
    PRE=(); run --owner acme --dir "$d"
    check_success "$d" acme
    chk "darwin: the create line says the port" has "$W/out" 'port: 4222'
    chk "darwin: the script asked stat -L -f %u" has "$SHIMLOG" 'stat OK -L -f %u /'
    chk "darwin: the script asked stat -L -f %Lp" has "$SHIMLOG" 'stat OK -L -f %Lp /'
    chk "darwin: the script asked lsof -iTCP:PORT" hasre "$SHIMLOG" '^lsof OK port=4222 pid= F=1$'
    chk "darwin: the script asked ps for the state (the start loop)" hasre "$SHIMLOG" '^ps OK -p [0-9]+ -o stat=$'
    run --stop --dir "$d"
    chk "darwin --stop: exit 0" rc_is 0
    chk "darwin --stop: the script asked stat -f %u (no -L)" has "$SHIMLOG" 'stat OK  -f %u /'
    chk "darwin --stop: says stopped" hasre "$W/out" '^stopped pid [0-9]+$'
    chk "darwin --stop: ps was asked for comm and args" \
        bash -c 'grep -Eq "^ps OK -p [0-9]+ -o comm=$" "$1" && grep -Eq "^ps OK -ww -p [0-9]+ -o args=$" "$1"' _ "$SHIMLOG"
    run --start --dir "$d"
    chk "darwin --start: exit 0" rc_is 0
    run --start --dir "$d"
    chk "darwin --start when running: already running (the ps comm/args check)" has "$W/out" 'already running'
    ptyrun "$d" bash "$SCRIPT" --remove --dir "$d"
    chk "darwin --remove on a terminal: exit 0" rc_is 0
    chk "darwin --remove: dir gone" absent "$d"
    chk "darwin: no stat call was rejected" bash -c '! grep -q "^stat REJECT" "$1"' _ "$SHIMLOG"
    chk "darwin: no ps call was rejected" bash -c '! grep -q "^ps REJECT" "$1"' _ "$SHIMLOG"
    chk "darwin: no lsof call was rejected" bash -c '! grep -q "^lsof REJECT" "$1"' _ "$SHIMLOG"
    chk "darwin: no readlink call was rejected" bash -c '! grep -q "^readlink REJECT" "$1"' _ "$SHIMLOG"
    chk "darwin: no shasum call was rejected" bash -c '! grep -q "^shasum REJECT" "$1"' _ "$SHIMLOG"
    chk "darwin: readlink was never used by the script" bash -c '! grep -q "^readlink" "$1"' _ "$SHIMLOG"
    n=$(grep -c '^ps OK' "$SHIMLOG")
    chk "darwin: ps was used ($n calls)" [ "$n" -gt 0 ]
}

case16() { # a truncated download (curl | bash) runs nothing
    local n cut pct before after pid d=$H/mash-nats
    n=$(wc -c <"$SCRIPT")
    chk "the script's last line is main \"\$@\"" [ "$(tail -n 1 "$SCRIPT")" = 'main "$@"' ]
    before=$(find "$H" -mindepth 0 2>/dev/null | sort | cksum)
    for pct in 10 30 60 80 95 99; do
        cut=$((n * pct / 100))
        head -c "$cut" "$SCRIPT" | cat | bash -s -- --owner acme >"$W/out" 2>"$W/err"
        RC=$?
        after=$(find "$H" -mindepth 0 2>/dev/null | sort | cksum)
        chk "first $pct% ($cut of $n bytes): nothing created in \$HOME" [ "$before" = "$after" ]
        chk "first $pct%: no mash-nats dir" absent "$d"
        chk "first $pct%: no mode ran (stdout empty)" [ ! -s "$W/out" ]
        chk "first $pct%: nothing listens on 4222" bash -c '! "$REALSS" -ltn | grep -q ":4222"'
    done
    head -n -1 "$SCRIPT" | cat | bash -s -- --owner acme >"$W/out" 2>"$W/err"
    RC=$?
    chk "everything but the last line: runs nothing (exit 0, no output)" quiet_ok
    chk "...: no mash-nats dir" absent "$d"
    # the same on a made server: a truncated --stop and --remove touch nothing
    ROUTE="file"; run --owner acme
    chk "setup ran" rc_is 0
    pid=$(pid_of "$d")
    cut=$((n * 60 / 100))
    head -c "$cut" "$SCRIPT" | cat | bash -s -- --stop >"$W/out" 2>"$W/err"
    chk "truncated --stop: the server is still alive" alive "$pid"
    head -c "$cut" "$SCRIPT" | cat | bash -s -- --remove >"$W/out" 2>"$W/err"
    chk "truncated --remove: the dir and the server are still there" bash -c '[ -d "$1" ]' _ "$d"
    chk "truncated --remove: the server is still alive" alive "$pid"
    stopdir "$d"
}

case17() { # a parent folder others can write to is refused unless sticky
    local par=$H/par m h0
    ROUTE="file"
    mkdir "$par"
    for m in 775 770 777 757 2775; do
        chmod "$m" "$par"
        run --owner acme --dir "$par/x$m"
        chk "parent mode $m (writable by group or others, no sticky): exit 1" rc_is 1
        chk "parent mode $m: error says writable by others" hasre "$W/err" '^error: .*writable by others: '"$par"
        chk "parent mode $m: hint says chmod go-w" hasre "$W/err" "^hint: .*chmod go-w $par"
        chk "parent mode $m: nothing made" absent "$par/x$m"
    done
    for m in 1777 1775 755; do
        chmod "$m" "$par"
        run --owner acme --dir "$par/y$m"
        chk "parent mode $m: accepted (exit 0)" rc_is 0
        chk "parent mode $m: the dir is made" [ -d "$par/y$m" ]
        stopdir "$par/y$m"
    done
    mkdir -p "$H/gp/q"; chmod 775 "$H/gp"
    run --owner acme --dir "$H/gp/q/z"
    chk "group-writable grandparent under \$HOME: exit 1" rc_is 1
    chk "grandparent: the error names it" has "$W/err" "writable by others: $H/gp"
    chk "grandparent: nothing made" absent "$H/gp/q/z"
    h0=$("$REALSTAT" -c %a "$H")
    chmod 775 "$H"
    run --owner acme
    chk "group-writable \$HOME (the immediate parent of the default dir): exit 1" rc_is 1
    chk "\$HOME: the error names it" has "$W/err" "writable by others: $H"
    chk "\$HOME: nothing made" absent "$H/mash-nats"
    run --start --dir "$par/y755"
    chk "--start under a group-writable \$HOME: refused too" rc_is 1
    chmod "$h0" "$H"
    run --owner acme --dir "$H/after"
    chk "\$HOME back to mode $h0: accepted" rc_is 0
    stopdir "$H/after"
}

case22() { # --dir anywhere: outside $HOME, ancestors owned by you or root, symlinks
    local t d pid
    ROUTE="file"
    # (1) a parent you own, outside $HOME: the full life cycle
    t=$(mktemp -d "${TMPDIR:-/tmp}/mlnt.XXXXXX"); d=$t/mash-nats
    chk "control: the parent is outside \$HOME, mode 700" [ "$("$REALSTAT" -c %a "$t")" = 700 ]
    run --owner acme --dir "$d"
    check_success "$d" acme
    pid=$(pid_of "$d")
    chk "outside \$HOME: the dir is mode 700" [ "$("$REALSTAT" -c %a "$d")" = 700 ]
    run --stop --dir "$d"
    chk "outside \$HOME: --stop: exit 0" rc_is 0
    chk "outside \$HOME: --stop: server gone" gone "$pid"
    run --start --dir "$d"
    chk "outside \$HOME: --start: exit 0" rc_is 0
    chk "outside \$HOME: --start: a live pid" alive "$(pid_of "$d")"
    pid=$(pid_of "$d")
    ptyrun "$d" bash "$SCRIPT" --remove --dir "$d"
    chk "outside \$HOME: --remove on a terminal: exit 0" rc_is 0
    chk "outside \$HOME: --remove: dir gone" absent "$d"
    chk "outside \$HOME: --remove: server gone" gone "$pid"
    rmdir "$t"
    # (2) a parent owned by root that you cannot write to
    for d in /opt/mlnt-x /usr/local/share/mlnt-x; do
        run --owner acme --dir "$d"
        chk "root-owned parent $d: exit 1" rc_is 1
        chk "root-owned parent $d: says cannot write to ${d%/*}" hasx "$W/err" "error: cannot write to ${d%/*}"
        chk "root-owned parent $d: hint says pick a folder you own" has "$W/err" 'hint: pick a folder you own'
        chk "root-owned parent $d: nothing made" absent "$d"
    done
    # (3) an ancestor owned by another user
    run --owner acme --dir /home/other/sub/n
    chk "ancestor owned by another user: exit 1" rc_is 1
    chk "other user's ancestor: says not owned by you or root" has "$W/err" 'not owned by you or root: /home/other'
    chk "other user's ancestor: nothing made" absent /home/other/sub/n
    # (4) symlink ancestors
    t=$(mktemp -d "${TMPDIR:-/tmp}/mlnt.XXXXXX"); mkdir "$t/real"; ln -s "$t/real" "$t/mine"
    run --owner acme --dir "$t/mine/n"
    chk "symlink ancestor owned by you: exit 1" rc_is 1
    chk "symlink ancestor: says symlink" has "$W/err" "refusing a symlink in the path: $t/mine"
    chk "symlink ancestor: target untouched" [ -z "$(ls -A "$t/real")" ]
    rm "$t/mine"; rmdir "$t/real" "$t"
    run --owner acme --dir /mlnt-rootlink/n
    chk "root-owned symlink ancestor: exit 0" rc_is 0
    chk "root-owned symlink ancestor: the dir is made behind the link" [ -d /home/tester/linkdst/n ]
    stopdir /mlnt-rootlink/n
    # (5) / and a top-level folder in a root-owned /
    run --owner acme --dir /
    chk "--dir /: exit 1" rc_is 1
    run --owner acme --dir /mlnt-top
    chk "--dir /mlnt-top: cannot write to /" hasx "$W/err" 'error: cannot write to /'
}

case18() { # the owner name "system" is reserved
    local e
    ROUTE="file"
    run --owner system --dir "$H/s1"
    chk "owner system: exit 1" rc_is 1
    chk "owner system: says invalid owner name" has "$W/err" 'error: invalid owner name: system'
    chk "owner system: says it is reserved" has "$W/err" 'reserved'
    chk "owner system: nothing made" absent "$H/s1"
    ROUTE=pipe
    run --owner system --dir "$H/s1"
    chk "owner system, piped route: exit 1" rc_is 1
    ptyrun system bash "$SCRIPT" --dir "$H/s2"
    chk "owner system typed at the prompt: exit 1" rc_is 1
    chk "owner system typed: invalid owner name, reserved" hasre "$W/out" 'invalid owner name: system is reserved'
    chk "owner system typed: nothing made" absent "$H/s2"
    run --owner SYSTEM --dir "$H/s3"
    chk "owner SYSTEM (upper case): exit 1" rc_is 1
    e=systems
    run --owner "$e" --dir "$H/s4"
    chk "owner $e (only the exact name is reserved): exit 0" rc_is 0
    stopdir "$H/s4"
}

case19() { # the port is probed again right before the server starts
    local d=$H/mash-nats real p
    real=$REALSS
    mkdir -p "$W/shim"
    # real ss on its first -ltn listing, then it claims MLNT_FAKEPORT is listening
    cat >"$W/shim/ss" <<SH
#!/bin/sh
case "\$*" in *-ltn*) ;; *) exec $real "\$@" ;; esac
n=\$(cat "$W/ss.count" 2>/dev/null || echo 0)
n=\$((n + 1))
echo "\$n" >"$W/ss.count"
$real "\$@"
[ "\$n" -le 1 ] || echo "LISTEN 0 128 127.0.0.1:\${MLNT_FAKEPORT:-4222} 0.0.0.0:*"
SH
    chmod 755 "$W/shim/ss"
    if [ "$SIM" = darwin ]; then # the script asks lsof here: the same trick on the lsof shim
        cat >"$W/shim/lsof" <<SH
#!/bin/sh
n=\$(cat "$W/ss.count" 2>/dev/null || echo 0)
n=\$((n + 1))
echo "\$n" >"$W/ss.count"
$SHIMS/lsof "\$@"
[ "\$n" -le 1 ] || printf 'p1\nf0\nn127.0.0.1:%s\n' "\${MLNT_FAKEPORT:-4222}"
exit 0
SH
        chmod 755 "$W/shim/lsof"
    fi
    rm -f "$W/ss.count"
    ROUTE=pipe
    PRE=(env "PATH=$W/shim:$PATH" MLNT_FAKEPORT=4222)
    run --owner acme
    chk "port taken between the first look and the start: exit 1" rc_is 1
    chk "...: the message names the port" hasx "$W/err" 'error: port 4222 was taken just now by another program'
    chk "...: the hint says to run again" hasre "$W/err" '^hint: .*run mash-local-nats-setup.sh again'
    chk "...: not the generic 'a step failed'" lacks "$W/err" 'a step failed'
    chk "...: nothing left behind" absent "$d"
    chk "...: no server was started" bash -c '! "$REALSS" -ltn | grep -q ":4222"'
    chk "...: the port tool was asked twice or more" [ "$(cat "$W/ss.count")" -ge 2 ]
    # --start: same probe
    PRE=()
    ROUTE="file"; run --owner acme
    chk "setup ran" rc_is 0
    p=$(port_of "$d")
    stopdir "$d"
    rm -f "$W/ss.count"
    PRE=(env "PATH=$W/shim:$PATH" "MLNT_FAKEPORT=$p")
    run --start
    chk "--start, port taken just now: exit 1" rc_is 1
    chk "--start: the message names the port" hasx "$W/err" "error: port $p was taken just now by another program"
    chk "--start: the hint says --start again" hasre "$W/err" '^hint: .*--start again'
    chk "--start: the recorded server is not running" nopid "$d/server.pid"
    chk "--start: no server was started" bash -c '! "$REALSS" -ltn | grep -q ":'"$p"'"'
    PRE=()
    run --start
    chk "--start again without the clash: exit 0" rc_is 0
    stopdir "$d"
}

case20() { # --remove refuses while something still listens on the recorded port
    local d=$H/mash-nats p DP s0 s1
    ROUTE="file"
    run --owner acme
    chk "setup ran" rc_is 0
    p=$(port_of "$d")
    stopdir "$d"
    chk "the server is stopped" bash -c '! "$REALSS" -ltn | grep -q "127.0.0.1:'"$p"'"'
    python3 -m http.server "$p" --bind 127.0.0.1 >"$W/dummy.log" 2>&1 &
    DP=$!
    chk "control: a dummy listener (a wrapper's server) is up on $p" waitlisten "$p"
    s0=$(snap "$d")
    ptyrun "$d" bash "$SCRIPT" --remove
    chk "--remove with a listener on the port: exit 1" rc_is 1
    chk "--remove: says something still listens, nothing removed" \
        has "$W/out" "error: something still listens on 127.0.0.1:$p; nothing was removed"
    chk "--remove: hint says to stop it" hasre "$W/out" 'hint: stop the program'
    chk "--remove: not reported as removed" lacks "$W/out" "removed $d"
    chk "--remove: the dir is still there" [ -d "$d" ]
    s1=$(snap "$d")
    chk "--remove: every file unchanged" [ "$s0" = "$s1" ]
    chk "--remove: the other listener is untouched" alive "$DP"
    kill "$DP"; wait "$DP" 2>/dev/null
    chk "the dummy is stopped by its recorded pid" gone "$DP"
    ptyrun "$d" bash "$SCRIPT" --remove
    chk "--remove with the port free: exit 0" rc_is 0
    chk "--remove with the port free: the dir is gone" absent "$d"
}

# guard_check LABEL PID NAME: a made dir whose server.pid names a process that is not
# our nats-server must leave that process alone on --stop and on --remove
guard_check() {
    local label=$1 victim=$2 d=$H/g-$3
    ROUTE="file"
    run --owner acme --dir "$d"
    chk "[$label] setup ran" rc_is 0
    stopdir "$d"
    printf '%s\n' "$victim" >"$d/server.pid"
    run --stop --dir "$d"
    chk "[$label] --stop: exit 0" rc_is 0
    chk "[$label] --stop: says the pid was left alone" hasre "$W/out" "pid $victim is not our nats-server; left alone"
    chk "[$label] --stop: the process is still alive" alive "$victim"
    chk "[$label] --stop: the stale server.pid is removed" absent "$d/server.pid"
    printf '%s\n' "$victim" >"$d/server.pid"
    ptyrun "$d" bash "$SCRIPT" --remove --dir "$d"
    chk "[$label] --remove: exit 0" rc_is 0
    chk "[$label] --remove: says the pid was left alone" has "$W/out" 'left alone'
    chk "[$label] --remove: the process is still alive" alive "$victim"
    chk "[$label] --remove: the dir is gone" absent "$d"
}

case21() { # the pid-reuse guard
    local SP NS
    sleep 300 &
    SP=$!
    guard_check "sleep 300" "$SP" sleep
    printf 'listen: 127.0.0.1:4290\n' >"$W/other.conf"
    nats-server -c "$W/other.conf" >"$W/other.log" 2>&1 &
    NS=$!
    chk "control: a nats-server with another config is up on 4290" waitlisten 4290
    guard_check "nats-server from another config" "$NS" other
    chk "the other nats-server still listens on 4290" listening 4290
    kill "$SP" "$NS"; wait "$SP" "$NS" 2>/dev/null
    chk "the test's sleep is stopped by its recorded pid" gone "$SP"
    chk "the test's nats-server is stopped by its recorded pid" gone "$NS"
}

case23() { # HOME is needed only for the default folder; --dir works without it
    local d=$W/nohome pid pt h
    ROUTE="file"
    PRE=(env -u HOME)
    # (1) no --dir: HOME unset, empty or relative is refused
    run --owner acme
    chk "no --dir, HOME unset: exit 1" rc_is 1
    chk "no --dir, HOME unset: says HOME is not an absolute path" hasx "$W/err" 'error: HOME is not set to an absolute path'
    chk "no --dir, HOME unset: hint says set HOME or pass --dir" has "$W/err" 'hint: set HOME and retry, or pass --dir'
    for h in '' relative/path; do
        PRE=(env "HOME=$h")
        run --owner acme
        chk "no --dir, HOME='$h': exit 1" rc_is 1
        chk "no --dir, HOME='$h': says HOME is not an absolute path" hasx "$W/err" 'error: HOME is not set to an absolute path'
        chk "no --dir, HOME='$h': hint says set HOME or pass --dir" has "$W/err" 'hint: set HOME and retry, or pass --dir'
    done
    PRE=(env -u HOME)
    # (2) an absolute --dir with HOME unset: the whole life cycle
    run --owner acme --dir "$d"
    check_success "$d" acme
    chk "create, HOME unset: no mention of HOME" bash -c '! cat "$0" "$1" | grep -q HOME' "$W/out" "$W/err"
    pid=$(pid_of "$d"); pt=$(port_of "$d")
    chk "create, HOME unset: the server answers TLS with the admin creds" nn "$d" "$pt" account info
    run --stop --dir "$d"
    chk "--stop, HOME unset: exit 0" rc_is 0
    chk "--stop, HOME unset: no mention of HOME" bash -c '! cat "$0" "$1" | grep -q HOME' "$W/out" "$W/err"
    chk "--stop, HOME unset: server gone" gone "$pid"
    run --start --dir "$d"
    chk "--start, HOME unset: exit 0" rc_is 0
    chk "--start, HOME unset: no mention of HOME" bash -c '! cat "$0" "$1" | grep -q HOME' "$W/out" "$W/err"
    chk "--start, HOME unset: a live pid" alive "$(pid_of "$d")"
    pid=$(pid_of "$d"); pt=$(port_of "$d")
    chk "--start, HOME unset: the server answers TLS with the admin creds" nn "$d" "$pt" account info
    ptyrun "$d" env -u HOME bash "$SCRIPT" --remove --dir "$d"
    chk "--remove on a terminal, HOME unset: exit 0" rc_is 0
    chk "--remove, HOME unset: no mention of HOME" bash -c '! grep -q HOME "$0"' "$W/out"
    chk "--remove, HOME unset: dir gone" absent "$d"
    chk "--remove, HOME unset: server gone" gone "$pid"
    # (3) a relative --dir with HOME unset: refused cleanly
    run --owner acme --dir rel/dir
    chk "relative --dir, HOME unset: exit 1" rc_is 1
    chk "relative --dir, HOME unset: says --dir must be absolute" has "$W/err" 'error: --dir must be an absolute path'
    chk "relative --dir, HOME unset: no unbound variable" lacks "$W/err" 'unbound'
    chk "relative --dir, HOME unset: nothing made" absent rel/dir
    PRE=()
}

case24() { # the nats store layout is checked before any file is read
    local d=$H/mash-nats real n
    real=$(command -v nats)
    ROUTE=pipe
    mkdir -p "$W/shim"
    cat >"$W/shim/nats" <<SH
#!/bin/sh
$real "\$@" || exit \$?
case "\$*" in
*"auth user add sys"*) rm -f "\$XDG_DATA_HOME/nats/nsc/stores/mash/accounts/SYSTEM/SYSTEM.jwt" ;;
esac
SH
    chmod 755 "$W/shim/nats"
    PRE=(env "PATH=$W/shim:$PATH"); run --owner acme
    chk "moved store file: exit 1" rc_is 1
    chk "moved store file: the exact message, then the hint" \
        bash -c '[ "$(sed -n 1p "$0")" = "error: the nats command stored its keys somewhere unexpected (tested with nats v0.5.2)" ] && [ "$(sed -n 2p "$0")" = "hint: use nats v0.5.2, or report this; nothing was left behind" ]' "$W/err"
    n=$(grep -c 'stored its keys' "$W/err")
    chk "moved store file: the message is printed once (got $n)" [ "$n" = 1 ]
    chk "moved store file: nothing but those two lines" [ "$(wc -l <"$W/err")" -eq 2 ]
    chk "moved store file: the dir is gone" absent "$d"
    chk "moved store file: no server on 4222" bash -c '! "$REALSS" -ltn | grep -q 127.0.0.1:4222'
    rm -f "$W/shim/nats"
    PRE=()
    run --owner acme
    check_success "$d" acme
    chk "a normal run afterwards: no lock left" absent "$d/.lock"
    stopdir "$d"
}

case25() { # one lifecycle lock: create, --start, --stop, --remove do not overlap
    local d=$H/lk pid pt SP DP i rca rcb rc1 rc2 e2 w
    ROUTE="file"
    # (a) two creates at once on the same --dir: exactly one wins
    bash "$SCRIPT" --owner acme --dir "$d" >"$W/o1" 2>"$W/e1" </dev/null & rca=$!
    bash "$SCRIPT" --owner acme --dir "$d" >"$W/o2" 2>"$W/e2" </dev/null & rcb=$!
    wait "$rca"; rc1=$?
    wait "$rcb"; rc2=$?
    chk "two creates: exactly one succeeds (rc $rc1, $rc2)" \
        bash -c '{ [ "$1" = 0 ] && [ "$2" != 0 ]; } || { [ "$1" != 0 ] && [ "$2" = 0 ]; }' _ "$rc1" "$rc2"
    if [ "$rc1" = 0 ]; then cp "$W/o1" "$W/out"; cp "$W/e1" "$W/err"; e2=$W/e2
    else cp "$W/o2" "$W/out"; cp "$W/e2" "$W/err"; e2=$W/e1; fi
    RC=0
    chk "two creates: the loser says an error" hasre "$e2" '^error: '
    check_success "$d" acme
    pid=$(pid_of "$d"); pt=$(port_of "$d")
    chk "two creates: the first one's server answers" nn "$d" "$pt" account info
    chk "two creates: no lock left" absent "$d/.lock"
    # (b) a lock held by a live pid: nothing runs, nothing changes
    sleep 300 & SP=$!
    echo "$SP" >"$d/.lock"
    run --stop --dir "$d"
    chk "live lock, --stop: exit 1" rc_is 1
    chk "live lock, --stop: says another run is using it" has "$W/err" "error: another run is using $d (pid $SP)"
    chk "live lock, --stop: the server is untouched" alive "$pid"
    chk "live lock, --stop: the lock is still the sleeper's" [ "$(cat "$d/.lock")" = "$SP" ]
    rm -f "$d/.lock"
    run --stop --dir "$d"
    chk "no lock, --stop: exit 0" rc_is 0
    chk "no lock, --stop: server gone" gone "$pid"
    chk "no lock, --stop: no lock left" absent "$d/.lock"
    echo "$SP" >"$d/.lock"
    run --start --dir "$d"
    chk "live lock, --start: exit 1" rc_is 1
    chk "live lock, --start: says another run is using it" has "$W/err" "error: another run is using $d (pid $SP)"
    chk "live lock, --start: no server started" bash -c '! "$REALSS" -ltn | grep -q 127.0.0.1:'"$pt"
    ptyrun "$d" bash "$SCRIPT" --remove --dir "$d"
    chk "live lock, --remove: exit 1" rc_is 1
    chk "live lock, --remove: says another run is using it" has "$W/out" "error: another run is using $d (pid $SP)"
    chk "live lock, --remove: the dir is intact" [ -f "$d/mash-setup.json" ]
    chk "live lock, --remove: the sleeper still holds the lock" [ "$(cat "$d/.lock")" = "$SP" ]
    kill "$SP"; wait "$SP" 2>/dev/null
    chk "the test's sleep is stopped by its recorded pid" gone "$SP"
    # (c) a lock holding a dead pid is taken over
    true & DP=$!
    wait "$DP"
    chk "control: the pid in the stale lock is dead" gone "$DP"
    rm -f "$d/.lock"; echo "$DP" >"$d/.lock"
    run --start --dir "$d"
    chk "stale lock, --start: exit 0" rc_is 0
    chk "stale lock, --start: no lock left" absent "$d/.lock"
    chk "stale lock, --start: the server answers" nn "$d" "$pt" account info
    echo "$DP" >"$d/.lock"
    run --stop --dir "$d"
    chk "stale lock, --stop: exit 0" rc_is 0
    chk "stale lock, --stop: no lock left" absent "$d/.lock"
    chk "stale lock, --stop: server gone" bash -c '! "$REALSS" -ltn | grep -q 127.0.0.1:'"$pt"
    mkdir "$d/.lock"
    run --start --dir "$d"
    chk "empty lock, --start: exit 0 (taken over)" rc_is 0
    chk "empty lock, --start: no lock left" absent "$d/.lock"
    stopdir "$d"
    # (d) --start and --remove at once: never a server on a deleted store
    for i in 1 2 3; do
        w=$H/race$i
        run --owner acme --dir "$w"
        chk "race $i: control, create ok" rc_is 0
        pt=$(port_of "$w")
        stopdir "$w"
        bash "$SCRIPT" --start --dir "$w" >"$W/o1" 2>"$W/e1" </dev/null & rca=$!
        ptyrun "$w" bash "$SCRIPT" --remove --dir "$w"
        rc2=$RC
        wait "$rca"; rc1=$?
        sleep 1
        if [ -d "$w" ]; then
            chk "race $i: dir kept, so --remove failed (rc $rc2)" [ "$rc2" != 0 ]
            chk "race $i: dir kept, so --start succeeded (rc $rc1)" [ "$rc1" = 0 ]
            chk "race $i: dir kept, the server answers" nn "$w" "$pt" account info
            chk "race $i: dir kept, no lock left" absent "$w/.lock"
            stopdir "$w"
            ptyrun "$w" bash "$SCRIPT" --remove --dir "$w"
        else
            chk "race $i: dir gone, so --remove succeeded (rc $rc2)" [ "$rc2" = 0 ]
            chk "race $i: dir gone, no server listens" bash -c '! "$REALSS" -ltn | grep -q 127.0.0.1:'"$pt"
        fi
        chk "race $i: finally the dir is gone" absent "$w"
    done
}

case26() { # a port held by another user is invisible to this one: the failure says so
    local d=$W/mash-nats26 pt
    mkdir -p "$W/blind"
    # the port tools see nothing, as on a Mac when another user holds the port
    printf '#!/bin/sh\nexit 0\n' >"$W/blind/ss"
    printf '#!/bin/sh\nexit 1\n' >"$W/blind/lsof"
    chmod 755 "$W/blind/ss" "$W/blind/lsof"
    foreign() { # PORT: a listener the script cannot see; its pid in FP
        python3 -c 'import socket,sys,time
s=socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", int(sys.argv[1]))); s.listen(5); time.sleep(600)' "$1" &
        FP=$!
        waitlisten "$1"
    }
    ROUTE="file"
    # (a) create
    foreign 4222
    chk "control: the foreign listener holds 4222" listening 4222
    PRE=(env "PATH=$W/blind:$PATH")
    run --owner acme --dir "$d"
    chk "create: exit 1" rc_is 1
    chk "create: did not come up" has "$W/err" 'error: the server did not come up'
    chk "create: says port 4222 may be in use by another user" has "$W/err" 'port 4222 may be in use by another user'
    chk "create: the exact line" hasx "$W/err" 'error: port 4222 may be in use by another user (this system shows only your own listeners)'
    chk "create: the hint says free the port and run --start again" hasre "$W/err" '^hint: free port 4222 or ask who holds it, then run .*mash-local-nats-setup.sh --start again'
    chk "create: the folder is kept, as for any later failure (it has its marker)" [ -f "$d/mash-setup.json" ]
    chk "create: the recorded server is not running" nopid "$d/server.pid"
    chk "create: no nats-server of ours on 4222" bash -c '! "$REALSS" -ltnp | grep ":4222" | grep -q nats-server'
    chk "create: the foreign listener is still alive" alive "$FP"
    kill "$FP"; wait "$FP" 2>/dev/null
    chk "create: the test's listener is stopped by its recorded pid" gone "$FP"
    # (b) --start on a dir made on a free port
    d=$W/mash-nats26b
    PRE=()
    run --owner acme --dir "$d"
    chk "setup ran" rc_is 0
    pt=$(port_of "$d")
    stopdir "$d"
    foreign "$pt"
    chk "--start: control: the foreign listener holds $pt" listening "$pt"
    PRE=(env "PATH=$W/blind:$PATH")
    run --start --dir "$d"
    chk "--start: exit 1" rc_is 1
    chk "--start: the exact line" hasx "$W/err" "error: port $pt may be in use by another user (this system shows only your own listeners)"
    chk "--start: the hint says --start again" hasre "$W/err" "^hint: free port $pt or ask who holds it, then run .*mash-local-nats-setup.sh --start again"
    chk "--start: the dir is intact" [ -f "$d/mash-setup.json" ]
    chk "--start: the recorded server is not running" nopid "$d/server.pid"
    chk "--start: no nats-server of ours" bash -c '! "$REALSS" -ltnp | grep ":'"$pt"'" | grep -q nats-server'
    chk "--start: the foreign listener is still alive" alive "$FP"
    chk "--start: no seed or cred text in the output" bash -c '! cat "$1" "$2" | grep -Eq "S[OANUCX][A-Z2-7]{50,}|eyJ[A-Za-z0-9_-]{20,}|-----"' _ "$W/out" "$W/err"
    kill "$FP"; wait "$FP" 2>/dev/null
    chk "--start: the test's listener is stopped by its recorded pid" gone "$FP"
    PRE=()
    run --start --dir "$d"
    chk "control: with the port free, --start works" rc_is 0
    stopdir "$d"
}

case27() { # the keys mashctl init checks: operator.nk signed the accounts; mashctl init accepts the output
    local d=$H/c27 KS a pub iss opsub who mout
    ROUTE="file"
    run --owner acme --dir "$d"
    chk "setup ran" rc_is 0
    KS=$d/nats-store/nats/nsc/keys/keys
    nsc() { XDG_DATA_HOME=$d/nats-store XDG_CONFIG_HOME=$d/nats-store/config nats "$@" </dev/null; }
    # the key file the nats store keeps for the public key $1
    kfile() { printf '%s/%s/%s/%s.nk' "$KS" "$(printf %s "$1" | cut -c1)" "$(printf %s "$1" | cut -c2-3)" "$1"; }
    opsub=$(python3 -c 'import base64,json,sys
p=open(sys.argv[1]).read().strip().split(".")[1]
print(json.loads(base64.urlsafe_b64decode(p + "=" * (-len(p) % 4)))["sub"])' "$d/operator.jwt")
    chk "operator.jwt subject is an operator public key (O...)" bash -c '[[ "$1" =~ ^O[A-Z2-7]{55}$ ]]' _ "$opsub"
    chk "operator.nk is the operator's own seed: the nats store's key file for the operator jwt subject" \
        cmp -s "$d/keys/operator.nk" "$(kfile "$opsub")"
    for a in "acme" SYSTEM; do
        iss=$(nsc auth account info "$a" --operator mash 2>&1 | sed -n 's/^ *Issuer: *\(O[A-Z2-7]*\).*$/\1/p' | head -n 1)
        chk "$a: the account jwt names an issuer (O...)" [ -n "$iss" ]
        chk "$a: the account jwt issuer is the operator jwt subject" [ "$iss" = "$opsub" ]
        chk "$a: keys/operator.nk is byte-identical to the key file of the issuer $iss" \
            cmp -s "$d/keys/operator.nk" "$(kfile "$iss")"
    done
    pub=$(nsc auth account info acme --operator mash 2>&1 | sed -n '1s/^Account .* (\(A[A-Z2-7]*\))$/\1/p')
    chk "acme: the account public key is known (A...)" [ -n "$pub" ]
    chk "acme: keys/account.nk is the seed of that account key" cmp -s "$d/keys/account.nk" "$(kfile "$pub")"
    # end to end with the real mashctl, when the harness copied one in (MLNT_MASHCTL)
    if [ ! -x /t/mashctl ]; then
        ok "skipped: MLNT_MASHCTL not set (no mashctl init check)"
    else
        mkdir -p "$W/mbin" "$W/mh"
        ln -sf /t/mashctl "$W/mbin/mashctl"
        who=$(tail -n 1 "$W/out")
        chk "the script's last line is the mashctl init command" \
            [ "$who" = "mashctl init --config $d/mash-setup.yaml" ]
        for a in first second; do
            mout=$W/mashctl.$a
            env "PATH=$W/mbin:$PATH" "HOME=$W/mh" "XDG_CONFIG_HOME=$W/mh/c" "XDG_DATA_HOME=$W/mh/d" \
                "XDG_STATE_HOME=$W/mh/s" "XDG_CACHE_HOME=$W/mh/x" bash -c "$who" >"$mout" 2>&1 </dev/null
            RC=$?
            cp "$mout" "$W/out"; : >"$W/err"
            chk "mashctl init, $a run: exit 0" rc_is 0
            chk "mashctl init, $a run: output has no 'refusing'" bash -c '! grep -qi refusing "$1"' _ "$mout"
        done
    fi
    stopdir "$d"
}

run_case() {
    case $CASE in
    15) case15 ;; 16) case16 ;; 17) case17 ;; 18) case18 ;; 19) case19 ;; 20) case20 ;; 21) case21 ;;
    1) case1 ;; 2) case2 ;; 3) case3 ;; 4) case4 ;; 5) case5 ;; 6) case6 ;; 7) case7 ;;
    8) case8 ;; 9) case9 ;; 10) case10 ;; 11) case11 ;; 12) case12 ;; 13) case13 ;; 14) case14 ;; 22) case22 ;; 23) case23 ;; 24) case24 ;; 25) case25 ;; 26) case26 ;; 27) case27 ;;
    *) echo "FAIL: c$CASE: no such case" ;;
    esac
}
[ "$SIM" != darwin ] || sim_setup
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

MADE_SCRATCH=0
if [ -n "${MLNT_SCRATCH:-}" ]; then
    SCRATCH=$MLNT_SCRATCH
    mkdir -p "$SCRATCH" || exit 2
else
    SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/mlnt.XXXXXX") || exit 2
    MADE_SCRATCH=1
fi
IMAGES=${MLNT_IMAGES:-"debian:stable ubuntu:24.04 ubuntu:26.04"}
CASES=${MLNT_CASES:-"1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27"}
MODES=${MLNT_MODES:-"linux darwin"}
NCV=${MLNT_NATSCLI:-0.5.2}
# sha256 of https://github.com/nats-io/natscli/releases/download/v0.5.2/nats-0.5.2-linux-amd64.zip,
# computed once on 2026-11-20; the test fails if the download differs
NATPIN=9dddf92072bba154c11a08165718fb6ee42af1f25ce4f1bb284d2fc141abcff2
[ "$NCV" = 0.5.2 ] || NATPIN=${MLNT_NATSCLI_SHA256:-}
[ -n "$NATPIN" ] || { echo "natscli v$NCV has no pinned sha256; set MLNT_NATSCLI_SHA256" >&2; exit 2; }
RUN=$(date +%m%d%H%M%S)-$$
if [ "$MADE_SCRATCH" = 1 ]; then
    REPORT=${MLNT_REPORT:-${TMPDIR:-/tmp}/mlnt-receipt-$RUN.md}
else
    REPORT=${MLNT_REPORT:-$SCRATCH/receipt-$RUN.md}
fi
CTX=$SCRATCH/build-$RUN
LOGS=$SCRATCH/logs-$RUN
MY_CONTAINERS=()
MY_IMAGE_IDS=()
CLEANED='not run'
SCRATCH_NOTE=''

free_gb() { df -P -k "$1" | awk 'NR==2 {printf "%d", $4 / 1048576}'; }
free_mb() { df -P -k "$1" | awk 'NR==2 {printf "%d", $4 / 1024}'; }

cleanup() {
    local n i rm_ok=0 left
    trap - EXIT INT TERM
    for n in "${MY_CONTAINERS[@]+"${MY_CONTAINERS[@]}"}"; do podman rm -f "$n" >/dev/null 2>&1; done
    for i in "${MY_IMAGE_IDS[@]+"${MY_IMAGE_IDS[@]}"}"; do podman rmi -f "$i" >/dev/null 2>&1; done
    case $CTX in "$SCRATCH"/build-*) rm -rf -- "$CTX" ;; esac
    # the scratch directory goes only when this run made it, and only if all passed
    SCRATCH_NOTE='scratch dir given by MLNT_SCRATCH: kept'
    if [ "$MADE_SCRATCH" = 1 ]; then
        if [ "$FAIL" = 0 ] && [ "$PASS" -gt 0 ]; then
            rm -rf -- "$LOGS"
            rmdir -- "$SCRATCH" 2>/dev/null && SCRATCH_NOTE="made by this run ($SCRATCH): removed" ||
                SCRATCH_NOTE="made by this run ($SCRATCH): NOT empty, kept"
        else
            SCRATCH_NOTE="made by this run ($SCRATCH): kept for inspection (logs in $LOGS)"
        fi
    fi
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
        echo "- modes: $MODES"
        echo "- date (UTC): $(date -u +%Y-%m-%dT%H:%M:%SZ)"
        echo "- test: scripts/mash-local-nats-setup_test.sh, sha256 $(sha256sum "$SELF_TEST" | awk '{print $1}')"
        echo "- script under test: scripts/mash-local-nats-setup.sh, sha256 $SCRIPTSHA (run as a test copy with the MLNT_UNAME hook added)"
        echo "- natscli v$NCV linux-amd64 zip, sha256 $NATSHA (pinned: $NATPIN)"
        echo "- podman: $(podman --version 2>/dev/null)"
        echo
        echo "## What the two modes prove"
        echo
        echo "- linux: the script as it runs on Linux (GNU stat, /proc, ss), the whole case list, unshimmed."
        echo "- darwin: MLNT_UNAME=Darwin, so the script takes its macOS branches (BSD stat -f, ps and"
        echo "  lsof instead of /proc and ss), the same whole case list, on a PATH with shims that behave"
        echo "  like the BSD tools (stat rejects -c; no ss or sha256sum; lsof built on ss; ps and readlink"
        echo "  reject GNU-only flags; each shim call is logged and case 15 checks no call was rejected)."
        echo "  This shows the BSD branches run end to end and use no GNU-only flag. It does NOT show they"
        echo "  work with the real macOS tools (real BSD stat, ps, lsof, sed, date, bash 3.2 as /bin/bash,"
        echo "  LibreSSL, Homebrew paths, launchd or terminal behaviour)."
        echo
        echo "## Images"
        echo
        printf '%s\n' "$IMGLINES"
        echo
        echo "## Totals"
        echo
        echo "- ok: $PASS"
        echo "- FAIL: $FAIL"
        echo "- host free disk before: ${DISK0:-?} MB, after: $(free_mb "$(dirname -- "$SCRATCH")") MB"
        echo "- cleanup: $CLEANED"
        echo "- scratch: $SCRATCH_NOTE"
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
# The script the containers run is a test copy with the MLNT_UNAME hook put back; the shipped script has none (#1120).
bash "$HERE/mash-local-nats-setup-testcopy.sh" "$SCRIPT_SRC" "$CTX/mash-local-nats-setup.sh" || exit 2
SCRIPT_SRC=$CTX/mash-local-nats-setup.sh
echo "== run $RUN: images: $IMAGES; cases: $CASES"
if [ -n "${MLNT_MASHCTL:-}" ]; then
    [ -f "$MLNT_MASHCTL" ] || { echo "no $MLNT_MASHCTL (MLNT_MASHCTL)" >&2; exit 2; }
    cp -- "$MLNT_MASHCTL" "$CTX/mashctl" && chmod 755 "$CTX/mashctl" || exit 2
fi
echo "== downloading natscli v$NCV"
curl -fsSL -o "$CTX/nats.zip" \
    "https://github.com/nats-io/natscli/releases/download/v$NCV/nats-$NCV-linux-amd64.zip" || {
    echo "cannot download natscli v$NCV" >&2; exit 2; }
NATSHA=$(sha256sum "$CTX/nats.zip" | awk '{print $1}')
echo "natscli sha256 $NATSHA"
if [ "$NATSHA" != "$NATPIN" ]; then
    echo "FAIL: natscli v$NCV zip sha256 is $NATSHA, pinned $NATPIN" >&2
    FAIL=$((FAIL + 1)); RESULTS="$RESULTS
- FAIL natscli zip sha256 differs from the pin"
    exit 2
fi
echo "natscli sha256 matches the pin"
cat >"$CTX/Containerfile" <<'CF'
ARG BASE
FROM ${BASE}
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    nats-server openssl iproute2 procps curl unzip ca-certificates util-linux python3 \
    && rm -rf /var/lib/apt/lists/*
COPY nats.zip /tmp/nats.zip
RUN unzip -j -o /tmp/nats.zip '*/nats' -d /usr/local/bin && chmod 755 /usr/local/bin/nats && rm /tmp/nats.zip
RUN useradd -m -s /bin/bash tester && useradd -m -s /bin/bash other \
    && chmod 755 /home/other && mkdir /home/other/sub && chown other /home/other/sub \
    && mkdir /home/tester/linkdst && chown tester /home/tester/linkdst \
    && ln -s /home/tester/linkdst /mlnt-rootlink
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
    # shellcheck disable=SC2034
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
    for mode in $MODES; do
    RESULTS="$RESULTS

#### $tag, mode $mode"
    envs=()
    [ "$mode" = darwin ] && envs=(-e MLNT_UNAME=Darwin -e MLNT_SIM=darwin)
    for n in $CASES; do
        name=mlnt-$RUN-$short-$mode-c$n
        lg=$LOGS/$short-$mode-c$n.log
        user=tester
        [ "$n" = 1 ] && user=root
        MY_CONTAINERS+=("$name")
        echo "== [$tag, $mode] case $n"
        timeout 600 podman run --rm --name "$name" --user "$user" --network none \
            --security-opt no-new-privileges --cap-drop=all --pids-limit 2048 \
            -e MLNT_INSIDE=1 "${envs[@]+"${envs[@]}"}" \
            -v "$SELF_TEST:/t/test.sh:ro" -v "$SCRIPT_SRC:/t/mash-local-nats-setup.sh:ro" \
            ${MLNT_MASHCTL:+-v "$CTX/mashctl:/t/mashctl:ro"} \
            "$img" bash /t/test.sh "$n" >"$lg" 2>&1
        rc=$?
        podman rm -f "$name" >/dev/null 2>&1
        po=$(grep -c '^ok: ' "$lg")
        pf=$(grep -c '^FAIL: ' "$lg")
        if ! grep -q "^done: c$n\$" "$lg"; then
            pf=$((pf + 1))
            echo "FAIL: [$tag, $mode] c$n did not finish (podman rc $rc)"
            RESULTS="$RESULTS
- FAIL [$tag, $mode] c$n did not finish (podman rc $rc); log $lg"
        fi
        PASS=$((PASS + po)); FAIL=$((FAIL + pf))
        grep -E '^(FAIL|  )' "$lg" | sed "s/^/[$tag, $mode] /"
        RESULTS="$RESULTS
$(grep -E '^(ok|FAIL): ' "$lg" | sed "s/^/- [$tag, $mode] /")"
    done
    done
done

echo "== totals: ok=$PASS FAIL=$FAIL"
[ "$FAIL" = 0 ] && [ "$PASS" -gt 0 ]
