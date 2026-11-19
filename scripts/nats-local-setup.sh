#!/usr/bin/env bash
# nats-local-setup.sh: stand up a local operator-mode NATS server for mash
# with only nsc, openssl and nats-server. It does not use mashctl; it ends
# by printing the `mashctl adopt` command to run.
#
# Layout rule for this file: indent with 4 spaces (no tabs) and keep every
# source line and every printed line within 80 columns. Printed text goes
# through wrap(), which breaks at spaces; only an unbreakable word (a very
# long --dir path) can exceed 80.
#
# Everything goes under --dir (mode 0700), which this script creates itself
# with mkdir (never an existing directory): a private CA and server
# certificate (ca.*, server.*), an isolated nsc store (nsc/, nothing in
# ~/.nsc or ~/.local/share/nats), the server configuration (server.conf,
# resolver/, jetstream/), and the files adopt takes (export/). Files are
# created new (noclobber, umask 077), never over an existing path.
#
# Usage: nats-local-setup.sh --dir DIR [--port N] [--host NAME]...
#        [--listen ADDR [--yes-expose]] [--account NAME] [--operator NAME]
#        [--js-disk SIZE] [--start]
#        | --dir DIR --check | --dir DIR --stop | --help
set -euC
umask 077

usage() {
    cat <<'EOF'
usage: nats-local-setup.sh --dir DIR [options]
           create a local operator-mode NATS setup
       nats-local-setup.sh --dir DIR --check
           verify an existing setup (creates nothing)
       nats-local-setup.sh --dir DIR --stop
           stop the server recorded in DIR/server.pid

  --dir DIR        required; must NOT exist (not even empty): the script
                   creates it with mode 0700. At least 3 path components
                   deep; the parent must exist, be a real directory (no
                   symlinks in the path), be owned by you or root, and not
                   be group/world-writable unless sticky (like /tmp)
  --port N         client port (default 4222)
  --host NAME      repeatable; extra DNS name or IP (v4 or v6) for the
                   server certificate (localhost, 127.0.0.1 and ::1 are
                   always included); the first one is used in the URL
  --listen ADDR    listen address (default 127.0.0.1); anything but
                   127.0.0.1, ::1 or localhost is refused without
                   --yes-expose
  --yes-expose     allow a non-loopback --listen; says what is exposed.
                   The script never touches a firewall or any service
  --account NAME   account for mash (default MASH)
  --operator NAME  operator name (default mash-operator)
  --js-disk SIZE   JetStream disk for the account, like 500M or 2G (default
                   2G; 64 streams, 128 consumers)
  --start          start nats-server detached (pid DIR/server.pid, log
                   DIR/server.log)
  --check          do not create anything: verify owner, type, modes,
                   files, config, and the TLS port if the server is up
  --stop           stop the server in DIR/server.pid (only if it is
                   `nats-server -c DIR/server.conf`)
  --help           this text
EOF
}

# wrap P1 P2 TEXT...: print TEXT word-wrapped to 80 columns, the first line
# after prefix P1 and the others after prefix P2.
wrap() {
    _p1=$1 _p2=$2
    shift 2
    printf '%s\n' "$*" | awk -v p1="$_p1" -v p2="$_p2" '
        BEGIN { pre = p1 }
        {
            cur = ""
            n = split($0, w, " ")
            for (i = 1; i <= n; i++) {
                if (cur == "") cur = w[i]
                else if (length(pre cur w[i]) + 1 <= 80) cur = cur " " w[i]
                else { print pre cur; pre = p2; cur = w[i] }
            }
            print (cur == "" ? "" : pre cur); pre = p2
        }'
}
# safe: stdin minus lines that look like secrets (nkey seeds, whole PEM
# blocks, JWTs, "seed/creds/secret: value"), then a note of how many lines
# were withheld.
SECRE='S[OAUNCX][A-Z2-7]{40,}|-----BEGIN|-----END|eyJ[A-Za-z0-9_-]{10,}'
SECRE="$SECRE|([Ss]eed|[Cc]reds|[Ss]ecret)[^ ]* *[:=] *[^ ]"
safe() {
    _in=$(cat)
    [ -n "$_in" ] || return 0
    _k=$(printf '%s\n' "$_in" | sed '/-----BEGIN/,/-----END/d' |
        grep -Ev "$SECRE" || true)
    _t=$(printf '%s\n' "$_in" | grep -c '')
    _c=0
    [ -z "$_k" ] || { _c=$(printf '%s\n' "$_k" | grep -c ''); }
    [ -z "$_k" ] || printf '%s\n' "$_k"
    [ "$_t" -eq "$_c" ] ||
        printf '(%s line(s) withheld: they looked like secrets)\n' \
            "$((_t - _c))"
}
# cut: show external text (logs) folded to 80 columns.
fold80() { fold -w 80; }

die() { wrap 'nats-local-setup: ' '    ' "$*" >&2; exit 1; }
say() { wrap '' '    ' "$*"; }
step() { N=$((N + 1)); printf '\n'; wrap "[$N] about to: " '    ' "$*"; }
did() { wrap '    did: ' '         ' "$*"; }

N=0
DIR=''
PORT=4222
LISTEN=127.0.0.1
ACCOUNT=MASH
OPERATOR=mash-operator
JSDISK=2G
START=0
CHECK=0
STOP=0
HOSTS=''
EXPOSE=0
LOG=''

# valid_host H: a DNS name ([A-Za-z0-9.-]) or an IPv4 or IPv6 literal.
valid_host() {
    case $1 in
    '' | -* | .* | *.) return 1 ;;
    *:*:*) case $1 in *[!0-9A-Fa-f:.]*) return 1 ;; esac; return 0 ;;
    *:*) return 1 ;;
    *[!A-Za-z0-9.-]*) return 1 ;;
    *[!0-9.]*) return 0 ;; # a name
    esac
    IFS=. read -r _a _b _c _d _e <<EOF
$1
EOF
    [ -z "$_e" ] && [ -n "$_d" ] || return 1
    for _x in "$_a" "$_b" "$_c" "$_d"; do
        case $_x in '' | ????* | *[!0-9]*) return 1 ;; esac
        [ "$_x" -le 255 ] || return 1
    done
}

need_val() { [ "$#" -ge 2 ] || die "$1 needs a value"; }
while [ "$#" -gt 0 ]; do
    case $1 in
    --dir) need_val "$@"; DIR=$2; shift 2 ;;
    --port) need_val "$@"; PORT=$2; shift 2 ;;
    --host)
        need_val "$@"
        valid_host "$2" ||
            die "--host: use a DNS name or an IPv4/IPv6 address"
        HOSTS="$HOSTS $2"; shift 2 ;;
    --listen) need_val "$@"; LISTEN=$2; shift 2 ;;
    --yes-expose) EXPOSE=1; shift ;;
    --account) need_val "$@"; ACCOUNT=$2; shift 2 ;;
    --operator) need_val "$@"; OPERATOR=$2; shift 2 ;;
    --js-disk) need_val "$@"; JSDISK=$2; shift 2 ;;
    --start) START=1; shift ;;
    --check) CHECK=1; shift ;;
    --stop) STOP=1; shift ;;
    --help | -h) usage; exit 0 ;;
    *) usage >&2; die "unknown argument: $1" ;;
    esac
done

[ -n "$DIR" ] || {
    usage >&2
    die "--dir is required (there is no default location)"
}
case $DIR in /*) ;; *) DIR=$PWD/$DIR ;; esac
case "/$DIR/" in
*/../* | */./*) die "--dir must not contain . or .. components: $DIR" ;;
esac
while :; do # squeeze // and trailing /
    case $DIR in *//*) DIR=${DIR%%//*}/${DIR#*//} ;; *) break ;; esac
done
DIR=${DIR%/}
[ -n "$DIR" ] || die "refusing '/' as --dir"
ncomp=$(printf '%s' "$DIR" | tr -s '/' '\n' | grep -c .) || true
[ "$ncomp" -ge 3 ] ||
    die "refusing --dir $DIR: needs at least 3 path components"
case $PORT in '' | *[!0-9]*) die "--port must be a number" ;; esac
[ "$PORT" -ge 1 ] && [ "$PORT" -le 65535 ] || die "--port out of range: $PORT"
case $ACCOUNT in '' | *[!A-Za-z0-9_-]*)
    die "--account: letters, digits, _ and - only" ;;
esac
case $OPERATOR in '' | *[!A-Za-z0-9_-]*)
    die "--operator: letters, digits, _ and - only" ;;
esac
_d=${JSDISK%[KMGT]}
case $_d in '' | *[!0-9]*) die "--js-disk: like 500M or 2G" ;; esac
case $LISTEN in '' | *[!A-Za-z0-9.:\[\]-]*)
    die "--listen: an IP address or a host name" ;;
esac
case $DIR in *[[:cntrl:]]*) die "--dir must not contain control characters" ;;
esac
LOOPBACK=0
case $LISTEN in 127.0.0.1 | ::1 | '[::1]' | localhost) LOOPBACK=1 ;; esac
if [ "$LOOPBACK" -eq 0 ] && [ "$EXPOSE" -eq 0 ] && [ "$((CHECK + STOP))" -eq 0 ]
then
    die "--listen $LISTEN is not loopback: other machines could reach the" \
        "server. Add --yes-expose if you mean that (this script never" \
        "changes a firewall or a service)."
fi
[ "$((CHECK + STOP))" -le 1 ] || die "--check and --stop are alternatives"
[ "$((CHECK + STOP + START))" -le 1 ] ||
    die "--start applies to create mode only"

# uid_of / rawmode PATH: owner uid and octal mode (Linux, then BSD stat).
uid_of() { stat -c %u "$1" 2>/dev/null || stat -f %u "$1"; }
rawmode() { stat -c %a "$1" 2>/dev/null || stat -f %Lp "$1"; }
# Q VALUE: shell-quoted for pasting.
Q() { printf '%q' "$1"; }

CONF=$DIR/server.conf
PIDF=$DIR/server.pid

# probe_host: an address a client can use for a listen address.
probe_host() {
    case $1 in
    0.0.0.0 | '' | '[::]' | '::') printf '127.0.0.1' ;;
    \[*\]) _h=${1#\[}; printf '%s' "${_h%\]}" ;;
    *) printf '%s' "$1" ;;
    esac
}
# port_open HOST PORT: succeeds if something accepts a TCP connection there
# (bash /dev/tcp).
port_open() { (exec 3<>"/dev/tcp/$1/$2") 2>/dev/null; }
# tls_required HOST PORT: the NATS INFO line announces that TLS is required.
tls_required() {
    exec 3<>"/dev/tcp/$1/$2" || return 1
    line=''
    read -r -t 5 line <&3 || true
    exec 3<&- 3>&-
    case $line in INFO*'"tls_required":true'*) return 0 ;; *) return 1 ;; esac
}
# alive PID: running (a zombie nobody has reaped yet counts as gone).
alive() {
    st=$(ps -p "$1" -o stat= 2>/dev/null) || return 1
    case $st in '' | Z*) return 1 ;; *) return 0 ;; esac
}
conf_listen() {
    sed -n 's/^listen: *"\(.*\):\([0-9]*\)"$/\1 \2/p' "$CONF" | head -n 1
}

# ---------------------------------------------------------------- --stop
if [ "$STOP" -eq 1 ]; then
    [ -f "$PIDF" ] || die "no $PIDF: nothing recorded to stop"
    pid=$(cat "$PIDF")
    case $pid in '' | *[!0-9]*) die "$PIDF does not hold a pid" ;; esac
    stale="process $pid is not running; $PIDF is stale and left alone"
    alive "$pid" || { say "$stale"; exit 0; }
    # "command=" is the same as "args=" on Linux procps and also exists on
    # macOS, where "args=" is only an alias.
    args=$(ps -p "$pid" -o command= 2>/dev/null || true)
    [ -n "$args" ] || { say "$stale"; exit 0; }
    want="nats-server -c $CONF"
    [ "$args" = "$want" ] ||
        die "pid $pid runs '$args', not '$want': not stopping it"
    say "about to: stop nats-server pid $pid ($args)"
    kill "$pid"
    i=0
    while alive "$pid" && [ "$i" -lt 50 ]; do sleep 0.2; i=$((i + 1)); done
    if alive "$pid"; then die "pid $pid did not exit in 10 s (left running)"; fi
    did "stopped pid $pid"
    exit 0
fi

# ---------------------------------------------------------------- --check
if [ "$CHECK" -eq 1 ]; then
    fails=0
    bad() { wrap '  FAIL  ' '        ' "$*"; fails=$((fails + 1)); }
    good() { wrap '  ok    ' '        ' "$*"; }
    mode_is() { # PATH MODE d|f: exists, right type, not a link, mine, mode
        [ -e "$1" ] || { bad "missing $1"; return; }
        if [ -L "$1" ]; then bad "$1 is a symlink"; return; fi
        if [ "$3" = d ] && [ ! -d "$1" ]; then bad "$1 is not a directory"
            return; fi
        if [ "$3" = f ] && [ ! -f "$1" ]; then bad "$1 is not a regular file"
            return; fi
        u=$(uid_of "$1")
        [ "$u" = "$(id -u)" ] || { bad "$1 is owned by uid $u, not you"
            return; }
        m=$(rawmode "$1")
        [ "${#m}" -le 3 ] || m=${m#?}   # ignore setgid/sticky bits
        if [ "$m" = "$2" ]; then good "$1 is yours, mode $m"
        else bad "$1 mode $m, want $2"; fi
    }
    say "checking the setup in $DIR (creates nothing)"
    mode_is "$DIR" 700 d
    mode_is "$DIR/export" 700 d
    for f in ca.key server.key export/operator.nk export/account.nk \
        export/sys.creds export/mashadmin.creds; do
        mode_is "$DIR/$f" 600 f
    done
    for f in ca.pem server.pem server.conf operator.jwt; do
        if [ -f "$DIR/$f" ]; then good "$DIR/$f exists"
        else bad "missing $DIR/$f"; fi
    done
    for d in resolver jetstream nsc; do
        if [ -d "$DIR/$d" ]; then good "$DIR/$d exists"
        else bad "missing $DIR/$d"; fi
    done
    if [ -f "$DIR/ca.pem" ] && [ -f "$DIR/server.pem" ]; then
        if openssl verify -CAfile "$DIR/ca.pem" -purpose sslserver \
            "$DIR/server.pem" >/dev/null 2>&1; then
            good "server.pem verifies against ca.pem for TLS server use"
        else bad "server.pem does not verify against ca.pem"; fi
    fi
    if [ -f "$CONF" ]; then
        if out=$(nats-server -t -c "$CONF" 2>&1); then
            out=$(printf '%s\n' "$out" | sed 's/ *(sha256:[0-9a-f]*)//')
            good "nats-server -t -c: $out"
        else
            out=$(printf '%s\n' "$out" | safe)
            bad "nats-server -t -c $CONF: $out"
        fi
        lp=$(conf_listen)
        [ -n "$lp" ] || lp="127.0.0.1 4222"
        h=$(probe_host "${lp% *}")
        p=${lp#* }
        if port_open "$h" "$p"; then
            if tls_required "$h" "$p"; then
                good "$h:$p answers and requires TLS"
            else bad "$h:$p answers but does not announce tls_required"; fi
        else
            wrap '  skip  ' '        ' "nothing listens on $h:$p" \
                "(server not running: port check skipped)"
        fi
    fi
    [ "$fails" -eq 0 ] || die "check: $fails problem(s)"
    say "check passed"
    exit 0
fi

# ---------------------------------------------------------------- create
step "preflight (tools, directory, port)"
for t in nsc openssl nats-server; do
    command -v "$t" >/dev/null 2>&1 || die "$t is not on PATH"
done
if [ "$LOOPBACK" -eq 0 ]; then
    say "EXPOSED (--yes-expose): the server will listen on $LISTEN:$PORT," \
        "which other machines may reach. TLS is required and clients need" \
        "credentials. This script changes no firewall and no service."
fi
did "$(nsc --version 2>&1 | tail -n 1); $(openssl version | cut -d" " -f1-2);" \
    "$(nats-server -v)"
if [ -e "$DIR" ] || [ -L "$DIR" ]; then
    die "$DIR already exists: this script creates --dir itself and" \
        "refuses any existing path (even an empty directory)"
fi
PARENT=${DIR%/*}
[ -d "$PARENT" ] || die "parent $PARENT does not exist or is not a directory"
[ ! -L "$PARENT" ] || die "parent $PARENT is a symlink: refusing"
REAL=$(cd -P -- "$PARENT" 2>/dev/null && pwd -P) ||
    die "cannot enter parent $PARENT"
[ "$REAL" = "$PARENT" ] ||
    die "parent $PARENT is not a plain path (resolves to $REAL; a symlink" \
        "in the path): use the real path"
ME=$(id -u)
puid=$(uid_of "$PARENT")
[ "$puid" = "$ME" ] || [ "$puid" = 0 ] ||
    die "parent $PARENT is owned by uid $puid: need you ($ME) or root"
pm=$(rawmode "$PARENT")
if [ $((8#$pm & 8#022)) -ne 0 ] && [ $((8#$pm & 8#1000)) -eq 0 ]; then
    die "parent $PARENT (mode $pm) is group/world-writable and not sticky:" \
        "others could swap things in it. Use a private parent directory."
fi
ph=$(probe_host "$LISTEN")
if port_open "$ph" "$PORT"; then
    die "port $ph:$PORT is already taken by something (nothing was killed" \
        "or changed): use --port"
fi
mkdir -m 0700 "$DIR" || die "could not create $DIR (it must not exist)"
[ -d "$DIR" ] && [ ! -L "$DIR" ] || die "$DIR is not a plain directory"
[ "$(uid_of "$DIR")" = "$ME" ] || die "$DIR is not owned by you"
[ "$(rawmode "$DIR")" = 700 ] || die "$DIR is not mode 700"
LOG=$(mktemp "$DIR/.cmd.XXXXXX") || die "cannot make a temp file in $DIR"
trap 'rm -f "$LOG"' EXIT
trap 'exit 1' INT TERM HUP
did "tools found; $DIR created by this script (mode 0700, yours);" \
    "$ph:$PORT is free"

step "make a private CA and a server certificate (ECDSA P-256, 10 years)"
SANS='DNS:localhost,IP:127.0.0.1,IP:::1'
FIRSTHOST=localhost
for h in $HOSTS; do
    case $h in
    *[!A-Za-z0-9.:-]*) die "--host $h: use a DNS name or an IP address" ;;
    localhost | 127.0.0.1 | ::1) ;; # already in the list
    *:* | [0-9]*.[0-9]*.[0-9]*.[0-9]*) SANS="$SANS,IP:$h" ;;
    *) SANS="$SANS,DNS:$h" ;;
    esac
    [ "$FIRSTHOST" != localhost ] || FIRSTHOST=$h
done
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
    -days 3650 -keyout "$DIR/ca.key" -out "$DIR/ca.pem" \
    -subj '/CN=mash local CA' \
    -addext 'basicConstraints=critical,CA:TRUE' \
    -addext 'keyUsage=critical,keyCertSign,cRLSign' 2>/dev/null ||
    die "openssl could not make the CA"
openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
    -keyout "$DIR/server.key" -subj '/CN=mash local server' 2>/dev/null |
    openssl x509 -req -in /dev/stdin -CA "$DIR/ca.pem" -CAkey "$DIR/ca.key" \
        -days 3650 -set_serial "0x$(openssl rand -hex 8)" \
        -extfile <(printf '%s\n' 'basicConstraints=CA:FALSE' \
            'keyUsage=critical,digitalSignature' \
            'extendedKeyUsage=serverAuth' "subjectAltName=$SANS") \
        -out "$DIR/server.pem" 2>/dev/null ||
    die "openssl could not make the server certificate"
openssl verify -CAfile "$DIR/ca.pem" -purpose sslserver "$DIR/server.pem" \
    >/dev/null
did "ca.pem, ca.key, server.pem, server.key written; SANs: $SANS"

step "make the operator, accounts and users with nsc, in an isolated store" \
    "under $DIR/nsc"
NSCD=$DIR/nsc
mkdir -m 0700 "$NSCD" "$NSCD/data" "$NSCD/config" "$NSCD/keys" "$NSCD/home"
export XDG_DATA_HOME=$NSCD/data XDG_CONFIG_HOME=$NSCD/config
export NKEYS_PATH=$NSCD/keys NSC_HOME=$NSCD/home NATS_CONFIG_HOME=$NSCD/config
# Tool output goes to the temp file $LOG (mode 0600, removed on every exit),
# never to a persistent log. On failure only a redacted tail is shown.
capfail() { # WHAT
    tail -n 15 "$LOG" | safe | fold80 >&2
    die "$1 failed (its output is not kept: it may hold secrets)"
}
q() { # run nsc quietly
    : >|"$LOG"
    nsc "$@" >>"$LOG" 2>&1 || capfail "nsc $1 $2"
}
f() { # a --field value without quotes
    : >|"$LOG"
    _v=$(nsc "$@" 2>>"$LOG") || capfail "nsc $1 $2"
    printf '%s\n' "$_v" | tr -d '"'
}
q add operator "$OPERATOR" --sys
q add account "$ACCOUNT"
q edit account "$ACCOUNT" --js-disk-storage "$JSDISK" --js-streams 64 \
    --js-consumer 128
q add user -a "$ACCOUNT" mashadmin
q describe operator
OPPUB=$(f describe operator --field sub)
ACCPUB=$(f describe account "$ACCOUNT" --field sub)
ACCISS=$(f describe account "$ACCOUNT" --field iss)
SYSPUB=$(f describe operator --field nats.system_account)
[ "$ACCISS" = "$OPPUB" ] ||
    die "account JWT was issued by $ACCISS, not the operator key $OPPUB"
did "operator $OPERATOR ($OPPUB), system account SYS, account $ACCOUNT" \
    "($ACCPUB)"
did "JetStream on $ACCOUNT: disk $JSDISK, 64 streams, 128 consumers (memory" \
    "storage unset)"
did "users: mashadmin in $ACCOUNT, sys in SYS; all keys are in $NKEYS_PATH"

step "write the server configuration and test it"
mkdir -m 0700 "$DIR/resolver" "$DIR/jetstream"
OPJWT=$DIR/operator.jwt
: >|"$LOG"
nsc describe operator --raw >"$OPJWT" 2>>"$LOG" ||
    capfail "nsc describe operator"
ACCJWT=$(nsc describe account "$ACCOUNT" --raw 2>>"$LOG") ||
    capfail "nsc describe account"
SYSJWT=$(nsc describe account SYS --raw 2>>"$LOG") ||
    capfail "nsc describe account"
CONFLISTEN=$LISTEN
case $LISTEN in \[*) ;; *:*) CONFLISTEN="[$LISTEN]" ;; esac
cat >"$CONF" <<EOF
# written by nats-local-setup.sh: operator mode, full resolver, TLS required,
# JetStream on. (The resolver_preload lines hold the account JWTs: long.)
server_name: "mash-local"
listen: "$CONFLISTEN:$PORT"
tls {
  cert_file: "$DIR/server.pem"
  key_file: "$DIR/server.key"
}
jetstream {
  store_dir: "$DIR/jetstream"
}
operator: "$OPJWT"
system_account: "$SYSPUB"
resolver {
  type: full
  dir: "$DIR/resolver"
  allow_delete: true
  interval: "2m"
  timeout: "1.9s"
}
resolver_preload: {
  $SYSPUB: "$SYSJWT"
  $ACCPUB: "$ACCJWT"
}
EOF
: >|"$LOG"
nats-server -t -c "$CONF" >>"$LOG" 2>&1 || capfail "nats-server -t -c"
did "$CONF written (self-contained; operator JWT in $OPJWT) and" \
    "'nats-server -t -c' passes"

step "export the files mashctl adopt takes into $DIR/export (mode 0600)"
EXP=$DIR/export
mkdir -m 0700 "$EXP"
seed_of() { # PUBLIC-KEY: print the seed file of a key nsc made
    p=$(find "$NKEYS_PATH/keys" -name "$1.nk" | head -n 1)
    [ -n "$p" ] || die "no seed for $1 under $NKEYS_PATH"
    printf '%s' "$p"
}
cat "$(seed_of "$OPPUB")" >"$EXP/operator.nk"
cat "$(seed_of "$ACCPUB")" >"$EXP/account.nk"
: >|"$LOG"
nsc generate creds -a SYS -n sys >"$EXP/sys.creds" 2>>"$LOG" ||
    capfail "nsc generate creds"
nsc generate creds -a "$ACCOUNT" -n mashadmin >"$EXP/mashadmin.creds" \
    2>>"$LOG" || capfail "nsc generate creds"
case $(cut -c1-2 "$EXP/operator.nk") in
SO) ;; *) die "operator.nk is not an SO seed" ;;
esac
case $(cut -c1-2 "$EXP/account.nk") in
SA) ;; *) die "account.nk is not an SA seed" ;;
esac
did "operator.nk, account.nk, sys.creds, mashadmin.creds written (contents" \
    "not shown)"

if [ "$START" -eq 1 ]; then
    step "start nats-server detached"
    nohup nats-server -c "$CONF" >"$DIR/server.log" 2>&1 &
    echo $! >"$PIDF"
    i=0
    until port_open "$ph" "$PORT"; do
        i=$((i + 1))
        alive "$(cat "$PIDF")" || {
            tail -n 10 "$DIR/server.log" | safe | fold80 >&2
            die "server exited"
        }
        [ "$i" -lt 75 ] ||
            die "port $ph:$PORT did not answer in 15 s (see $DIR/server.log)"
        sleep 0.2
    done
    did "nats-server pid $(cat "$PIDF") listens on $ph:$PORT (log" \
        "$DIR/server.log)"
fi

URLHOST=$FIRSTHOST
case $URLHOST in *:*) URLHOST="[$URLHOST]" ;; esac
SELF=$(basename "$0")
file() { printf '  %-24s %s\n' "$1" "$2"; }
# Everything printed below inside a command is shell-quoted with %q.
QDIR=$(Q "$DIR")

printf '\n'
wrap '' '' "Done. The setup is in $QDIR. Nothing above printed a seed or" \
    "a credential, and nothing below does."
if [ "$LOOPBACK" -eq 1 ]; then
    printf '\n'
    wrap '' '' "Other users on this machine can still reach $LISTEN:$PORT;" \
        "they cannot connect without credentials."
else
    printf '\n'
    wrap '' '' "Exposed: $LISTEN:$PORT is reachable from other machines" \
        "(TLS required, credentials needed). No firewall or service was" \
        "touched."
fi
printf '\nFiles left for you (all under %s):\n' "$QDIR"
file ca.pem 'CA certificate: give this to clients'
file server.conf 'nats-server configuration (no seeds)'
file export/mashadmin.creds 'admin credentials (secret)'
file export/sys.creds 'system account credentials (secret)'
file export/operator.nk 'operator seed (secret)'
file export/account.nk 'account seed (secret)'
if [ "$START" -eq 1 ]; then
    file server.log 'server log'
    file server.pid 'server process id (--stop reads it)'
fi

printf '\nNext:\n'
if [ "$START" -ne 1 ]; then
    printf '  # start the server\n  nats-server \\\n      -c %s\n\n' \
        "$(Q "$CONF")"
fi
printf '  # adopt it into mash (replace <owner> with your owner name)\n'
printf '  cd %s\n' "$(Q "$EXP")"
printf '  export MASH_TLS_CA=%s\n' "$(Q "$DIR/ca.pem")"
printf '  mashctl adopt \\\n'
printf '      --server %s \\\n' "$(Q "tls://$URLHOST:$PORT")"
printf '      --owner <owner> \\\n'
printf '      --account %s \\\n' "$(Q "$ACCOUNT")"
printf '      --operator-key operator.nk \\\n'
printf '      --account-key account.nk \\\n'
printf '      --sys-creds sys.creds \\\n'
printf '      --admin-creds mashadmin.creds\n'
printf '  mashctl init\n'
printf '\nLater:\n'
printf '  %s --check \\\n      --dir %s\n' "$(Q "$SELF")" "$QDIR"
printf '  %s --stop \\\n      --dir %s\n' "$(Q "$SELF")" "$QDIR"
