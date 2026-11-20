#!/usr/bin/env bash
# mash-local-nats-setup.sh: make, start, stop or remove a local NATS server
# for mash (operator mode, TLS, JetStream, 127.0.0.1 only) using nats-server,
# the nats command and openssl. It does not need nsc and installs nothing.
#
# Safety, in short: refuses root; DIR (default ~/mash-nats, or any absolute
# --dir) must be a new folder in a parent you can write to, with every folder
# above it owned by you or root and not writable by others, and no symlink
# unless root owns it; DIR is made exclusively (mode 0700);
# the failure trap removes only the DIR this run made, and only before the
# marker file (mash-setup.json) exists; --remove needs the path typed on a
# terminal; stop signals only the pid in server.pid after /proc confirms it
# is a nats-server started with this DIR's server.conf. Seeds and creds are
# never printed. No globs are used for any delete. Everything that does
# anything runs from main(), the last line of the file, so a truncated download
# piped into bash defines functions and runs nothing.
#
# Test hook (not in --help): MLNT_UNAME, when set, replaces the output of
# `uname -s`, so a test can pretend to be macOS.
set -eu
umask 077

usage() {
    cat <<'EOF'
usage: mash-local-nats-setup.sh [--dir DIR] [--owner NAME] [--stop | --start | --remove]

  (default)    make a local NATS server and start it
  --dir DIR    a new folder to use, absolute path (default $HOME/mash-nats)
  --owner NAME owner name; asked once on the terminal if not given
  --stop       stop the server
  --start      start an existing server again (after a reboot)
  --remove     stop the server and delete DIR (you type the path)
  --help       this text
EOF
}

# die CODE MESSAGE [HINT]
SAID=0
die() {
    _c=$1
    SAID=1
    printf 'error: %s\n' "$2" >&2
    [ -z "${3:-}" ] || printf 'hint: %s\n' "$3" >&2
    exit "$_c"
}

MODE=create
DIR=''
OWNER=''
MARKED=0
CREATED=0
SPID=''
SELF=${0##*/}
[ "$SELF" != bash ] && [ "$SELF" != sh ] || SELF=mash-local-nats-setup.sh

setmode() {
    [ "$MODE" = create ] || [ "$MODE" = "$1" ] ||
        die 2 "only one of --stop, --start, --remove at a time" "see --help"
    MODE=$1
}
parse_args() {
    while [ "$#" -gt 0 ]; do
        case $1 in
        --help | -h) usage; exit 0 ;;
        --dir)
            [ "$#" -ge 2 ] || die 2 "--dir needs a path" "see --help"
            DIR=$2; shift 2 ;;
        --owner)
            [ "$#" -ge 2 ] || die 2 "--owner needs a name" "see --help"
            OWNER=$2; shift 2 ;;
        --stop) setmode stop; shift ;;
        --start) setmode start; shift ;;
        --remove) setmode remove; shift ;;
        *) die 2 "unknown option: $1" "see --help" ;;
        esac
    done
}

valid_owner() {
    case $1 in
    '' | [!a-z]* | *[!a-z0-9-]* | *--* | *-) return 1 ;;
    esac
    [ "${#1}" -le 32 ]
}
OWNER_HINT="use lower-case letters, digits and single hyphens, starting with a letter (32 max)"
check_owner() { # NAME; "system" is the name of the operator's system account
    [ "$1" != system ] ||
        die 1 "invalid owner name: system is reserved" "pick another name; $OWNER_HINT"
    valid_owner "$1" || die 1 "invalid owner name: $1" "$OWNER_HINT"
}

# ---------------------------------------------------------------- paths
set_paths() {
    HOME_DIR=${HOME:-}
    case $HOME_DIR in
    /?*) ;;
    *) die 1 "HOME is not set to an absolute path" "set HOME and retry" ;;
    esac
    HOME_DIR=${HOME_DIR%/}
    DEFAULT_DIR=0
    if [ -z "$DIR" ]; then DIR=$HOME_DIR/mash-nats; DEFAULT_DIR=1; fi
    DIR=${DIR%/}
    UID_NOW=$(id -u)
}

fileowner() { stat -c %u "$1" 2>/dev/null || stat -f %u "$1"; }

# check_parent_mode DIR: refuse a folder that group or others can write to,
# unless it has the sticky bit (like /tmp), since they could swap what is under it
check_parent_mode() {
    _m=$(stat -L -c %a "$1" 2>/dev/null) || die 1 "cannot read the mode of $1" "check the folder and retry"
    case $_m in '' | *[!0-7]*) die 1 "cannot read the mode of $1" "check the folder and retry" ;; esac
    _s=0
    [ "${#_m}" -lt 4 ] || _s=${_m%???}
    _g=${_m: -2:1}
    _o=${_m: -1}
    if [ $(((${_g:-0} | ${_o:-0}) & 2)) -ne 0 ] && [ $((_s & 1)) -eq 0 ]; then
        die 1 "a parent folder is writable by others: $1 (mode $_m, no sticky bit)" \
            "run: chmod go-w $1 (or pick another --dir)"
    fi
}

check_dir_path() {
    case $DIR in
    /?*) ;;
    *) die 1 "--dir must be an absolute path, and not /: ${DIR:-/}" \
        "for example $HOME_DIR/mash-nats" ;;
    esac
    case $DIR in
    *[!A-Za-z0-9._/-]*)
        die 1 "--dir may only use letters, digits, . _ - and /" "choose a simpler path" ;;
    esac
    case "$DIR/" in
    *//* | */./* | */../*)
        die 1 "--dir must not contain //, . or .. parts: $DIR" "give a plain path" ;;
    esac
    if [ "$DEFAULT_DIR" = 1 ]; then
        [ -d "$HOME_DIR" ] || die 1 "HOME is not a directory: $HOME_DIR" "set HOME and retry"
        [ "$(fileowner "$HOME_DIR")" = "$UID_NOW" ] ||
            die 1 "HOME is not owned by you: $HOME_DIR" "run as the owner of your home folder, or pass --dir"
    fi
    # walk from / over every ancestor of DIR (DIR itself is not one)
    _p=''
    _rest=${DIR#/}
    check_ancestor / ''
    while :; do
        _comp=${_rest%%/*}
        case $_rest in */*) _rest=${_rest#*/} ;; *) break ;; esac
        _p=$_p/$_comp
        if [ ! -e "$_p" ]; then
            die 1 "parent folder does not exist: $_p" "make it first, or pick another --dir"
        fi
        check_ancestor "$_p" "$_p"
    done
    if [ -L "$DIR" ]; then
        die 1 "refusing a symlink in the path: $DIR" "pass a real folder with --dir"
    fi
    _par=${DIR%/*}
    [ -n "$_par" ] || _par=/
    # a DIR that exists is reported by the create step ("already exists")
    if [ "$MODE" = create ] && [ ! -e "$DIR" ] && [ ! -w "$_par" ]; then
        die 1 "cannot write to $_par" "pick a folder you own with --dir"
    fi
}

# check_ancestor PATH [LINKCHECK]: an ancestor of DIR must not be a symlink
# unless root owns the link (system links such as /home), must be owned by you
# or root, and must not be writable by others (see check_parent_mode)
check_ancestor() {
    if [ -n "$2" ] && [ -L "$1" ] && [ "$(fileowner "$1")" != 0 ]; then
        die 1 "refusing a symlink in the path: $1" "pass a real folder with --dir"
    fi
    _o=$(stat -L -c %u "$1" 2>/dev/null) || die 1 "cannot read $1" "check the folder and retry"
    if [ "$_o" != "$UID_NOW" ] && [ "$_o" != 0 ]; then
        die 1 "not owned by you or root: $1" "pick a folder you own with --dir"
    fi
    check_parent_mode "$1"
}

# ---------------------------------------------------------------- helpers
tty_ok() { ( exec </dev/tty ) 2>/dev/null; }

marker_get() { # KEY: string value from mash-setup.json (one key per line)
    sed -n 's/^ *"'"$1"'": *"\{0,1\}\([^",]*\)"\{0,1\},\{0,1\} *$/\1/p' \
        "$DIR/mash-setup.json" | head -n 1
}

proc_alive() {
    _st=$(sed -n 's/^State:[[:space:]]*\(.\).*/\1/p' "/proc/$1/status" 2>/dev/null)
    [ -n "$_st" ] && [ "$_st" != Z ] && [ "$_st" != X ]
}
# proc_ours PID: running nats-server whose arguments name this DIR's server.conf
proc_ours() {
    proc_alive "$1" || return 1
    _exe=$(readlink "/proc/$1/exe" 2>/dev/null || true)
    if [ -z "$_exe" ]; then _exe=$(cat "/proc/$1/comm" 2>/dev/null || true); fi
    _exe=${_exe% (deleted)}
    [ "${_exe##*/}" = nats-server ] || return 1
    tr '\0' '\n' <"/proc/$1/cmdline" 2>/dev/null | grep -Fxq -- "$DIR/server.conf"
}
recorded_pid() {
    [ -f "$DIR/server.pid" ] || return 1
    _pid=$(head -n 1 "$DIR/server.pid" 2>/dev/null || true)
    case $_pid in '' | *[!0-9]*) return 1 ;; esac
    printf '%s' "$_pid"
}

listening() { # is 127.0.0.1:$PORT accepting
    ss -ltn 2>/dev/null | awk 'NR>1 {print $4}' | grep -Fxq "127.0.0.1:$PORT"
}

port_taken() { # PORT
    ss -ltn 2>/dev/null | awk 'NR>1 {print $4}' | grep -Eq "[:.]$1\$"
}

# existing-dir checks shared by --start, --stop, --remove
require_made_dir() {
    _hint="to make one, run: $SELF --owner ${OWNER:-NAME}"
    [ "$DIR" = "$HOME_DIR/mash-nats" ] || _hint="$_hint --dir $DIR"
    [ -d "$DIR" ] || die 1 "no setup found at $DIR" "$_hint"
    [ ! -L "$DIR" ] || die 1 "refusing a symlinked folder: $DIR" "pass the real folder with --dir"
    [ "$(fileowner "$DIR")" = "$UID_NOW" ] ||
        die 1 "not owned by you: $DIR" "pass a folder you own with --dir"
    [ -f "$DIR/mash-setup.json" ] && [ ! -L "$DIR/mash-setup.json" ] ||
        die 1 "$DIR was not made by this script (no mash-setup.json)" "nothing was changed"
    [ "$(marker_get schema)" = 1 ] && [ "$(marker_get dir)" = "$DIR" ] ||
        die 1 "$DIR/mash-setup.json is not one of ours" "nothing was changed"
    REC_OWNER=$(marker_get owner)
    PORT=$(marker_get port)
    case $PORT in '' | *[!0-9]*) die 1 "$DIR/mash-setup.json has no usable port" "nothing was changed" ;; esac
    if [ -n "$OWNER" ] && [ "$OWNER" != "$REC_OWNER" ]; then
        die 1 "owner $OWNER differs from the recorded owner $REC_OWNER" \
            "drop --owner, or use --owner $REC_OWNER; nothing was changed"
    fi
}

do_stop() { # prints one line; returns 0 when not running at the end
    _p=$(recorded_pid) || { echo "not running"; return 0; }
    if ! proc_alive "$_p"; then
        rm -f "$DIR/server.pid"; echo "not running"; return 0
    fi
    if ! proc_ours "$_p"; then
        rm -f "$DIR/server.pid"
        echo "not running (pid $_p is not our nats-server; left alone)"; return 0
    fi
    kill -TERM "$_p"
    _i=0
    while proc_alive "$_p" && [ "$_i" -lt 50 ]; do sleep 0.2; _i=$((_i + 1)); done
    if proc_alive "$_p"; then
        die 1 "pid $_p did not stop within 10 s" "check $DIR/server.log"
    fi
    rm -f "$DIR/server.pid"
    echo "stopped pid $_p"
}

nats_env() {
    export XDG_DATA_HOME="$DIR/nats-store" XDG_CONFIG_HOME="$DIR/nats-store/config"
}

# verify PORT: sign-in with the admin creds and a system ping, over TLS
verify() {
    nats_env
    _i=0; _ok=0
    while [ "$_i" -lt 6 ]; do
        if nats --server "tls://127.0.0.1:$PORT" --tlsca "$DIR/ca.pem" \
            --creds "$DIR/creds/mashadmin.creds" --timeout 3s account info >/dev/null 2>&1 </dev/null &&
            nats --server "tls://127.0.0.1:$PORT" --tlsca "$DIR/ca.pem" \
                --creds "$DIR/creds/sys.creds" --timeout 3s server ping >/dev/null 2>&1 </dev/null; then
            _ok=1; break
        fi
        _i=$((_i + 1)); sleep 1
    done
    [ "$_ok" = 1 ]
}

start_server() { # needs PORT
    # look at the port once more right before starting: it may have been taken meanwhile
    if port_taken "$PORT"; then
        if [ "$MODE" = create ]; then
            MARKED=0 # no server was started: the failure trap may remove DIR
            die 1 "port $PORT was taken just now by another program" \
                "nothing was left behind; run $SELF again"
        fi
        die 1 "port $PORT was taken just now by another program" \
            "stop that program, then run $SELF --start again"
    fi
    if [ -f "$DIR/server.log" ]; then mv -f "$DIR/server.log" "$DIR/server.log.1"; fi
    nohup nats-server -c "$DIR/server.conf" >"$DIR/server.log" 2>&1 </dev/null &
    SPID=$!
    echo "$SPID" >"$DIR/server.pid"
    _i=0
    while [ "$_i" -lt 20 ]; do
        proc_alive "$SPID" || break
        ! listening || break
        sleep 0.5; _i=$((_i + 1))
    done
    if proc_alive "$SPID" && listening && verify; then return 0; fi
    echo "error: the server did not come up; last log lines:" >&2
    tail -n 5 "$DIR/server.log" 2>/dev/null |
        tr -d '\000' | grep -Eva 'S[OANUCX][A-Z2-7]{50,}|eyJ[A-Za-z0-9_-]{20,}|-----' |
        cut -c1-200 | sed 's/^/  /' >&2 || true
    if proc_ours "$SPID"; then kill -TERM "$SPID" 2>/dev/null || true; rm -f "$DIR/server.pid"; fi
    SAID=1
    printf 'hint: fix the cause and run %s --start, or %s --remove to start over\n' \
        "$SELF" "$SELF" >&2
    exit 1
}

# ---------------------------------------------------------------- stop/start/remove
mode_stop() {
    require_made_dir
    do_stop
}

mode_start() {
    require_made_dir
    if _p=$(recorded_pid) && proc_ours "$_p"; then
        echo "already running: pid $_p on 127.0.0.1:$PORT"; return 0
    fi
    miss=''
    for t in nats-server nats ss; do command -v "$t" >/dev/null 2>&1 || miss="$miss $t"; done
    if [ -n "$miss" ]; then
        _h="install it; see --help in the docs for where to get it"
        case " $miss " in *" ss "*) _h="ss is needed (Linux only in 0.5.6); $_h" ;; esac
        die 1 "missing:$miss" "$_h"
    fi
    if port_taken "$PORT"; then
        die 1 "port $PORT is now used by another program" \
            "stop that program, then run $SELF --start again"
    fi
    start_server
    echo "started: nats-server pid $SPID on 127.0.0.1:$PORT"
}

mode_remove() {
    require_made_dir
    command -v ss >/dev/null 2>&1 ||
        die 1 "ss is needed (Linux only in 0.5.6)" "install it: apt install iproute2; nothing was removed"
    tty_ok || die 4 "--remove needs a terminal to confirm" \
        "run it from a terminal; nothing was changed"
    printf 'type the folder to remove (%s): ' "$DIR" >/dev/tty
    IFS= read -r ans </dev/tty || ans=''
    [ "$ans" = "$DIR" ] || die 1 "the path typed does not match" "nothing was changed"
    do_stop
    # a wrapper script around nats-server keeps the port after the recorded pid
    # is gone; deleting DIR then would pull its store out from under it
    if listening; then
        die 1 "something still listens on 127.0.0.1:$PORT; nothing was removed" \
            "stop the program that holds the port (a wrapper script that runs nats-server?), then run $SELF --remove again"
    fi
    rm -rf "$DIR"
    echo "removed $DIR"
}

# ---------------------------------------------------------------- create
add_miss() { miss="${miss:+$miss, }$1"; hints="${hints:+$hints; }$2"; }
cleanup() {
    _rc=$?
    trap - EXIT INT TERM
    if [ "$_rc" -ne 0 ] && [ "$SAID" = 0 ]; then
        printf 'error: a step failed\n' >&2
        printf 'hint: nothing was left behind; check the tools and retry\n' >&2
    fi
    # only a DIR this run made, and only before the marker: no server was ever
    # started from it, so there is no store to lose (unlike --remove)
    if [ "$_rc" -ne 0 ] && [ "$CREATED" = 1 ] && [ "$MARKED" = 0 ] &&
        [ -d "$DIR" ] && [ ! -L "$DIR" ]; then
        rm -rf "$DIR"
    fi
    exit "$_rc"
}

acct_pub() {
    nats auth account info "$1" --operator "$OP" 2>/dev/null </dev/null |
        sed -n '1s/^Account .* (\([A-Z0-9]*\))$/\1/p'
}
keyfile() { # PUBKEY: path of its seed file in the CLI key store
    _t=$(printf '%s' "$1" | cut -c1)
    _d=$(printf '%s' "$1" | cut -c2-3)
    printf '%s/%s/%s/%s.nk' "$KS" "$_t" "$_d" "$1"
}

do_create() {
    miss=''; hints=''
    command -v nats-server >/dev/null 2>&1 ||
        add_miss nats-server "install nats-server 2.10.7+: apt install nats-server (Debian/Ubuntu) \
or brew install nats-server"
    command -v nats >/dev/null 2>&1 ||
        add_miss nats "install nats: brew install nats, or on Debian/Ubuntu download the linux zip \
from https://github.com/nats-io/natscli/releases"
    command -v openssl >/dev/null 2>&1 ||
        add_miss openssl "install openssl: apt install openssl or brew install openssl"
    command -v ss >/dev/null 2>&1 ||
        add_miss ss "ss is needed (Linux only in 0.5.6); install it: apt install iproute2"
    [ -z "$miss" ] || die 1 "missing: $miss" "$hints"

    NSV=$(nats-server --version 2>/dev/null </dev/null |
        sed -n 's/.*v\{0,1\}\([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\).*/\1/p' | head -n 1)
    [ -n "$NSV" ] || die 1 "cannot read the nats-server version" "need nats-server 2.10.7 or newer"
    IFS=. read -r v1 v2 v3 <<EOF
$NSV
EOF
    if [ $((v1 * 1000000 + v2 * 1000 + v3)) -lt 2010007 ]; then
        die 1 "nats-server $NSV is too old (need 2.10.7 or newer)" \
            "apt install nats-server (Debian/Ubuntu, 2.10.7+) or brew install nats-server"
    fi
    # unwritable XDG paths keep the nats command from making folders in your home
    NOXDG='XDG_DATA_HOME=/dev/null/x XDG_CONFIG_HOME=/dev/null/y'
    # shellcheck disable=SC2086
    NCV=$(env $NOXDG nats --version 2>/dev/null </dev/null | head -n 1 | sed 's/^[^0-9]*//')
    # shellcheck disable=SC2086
    env $NOXDG nats auth operator --help >/dev/null 2>&1 </dev/null ||
        die 1 "nats ${NCV:-?} has no 'nats auth operator' command" \
            "upgrade nats: brew upgrade nats, or download the latest from \
https://github.com/nats-io/natscli/releases"

    if [ -z "$OWNER" ]; then
        _def=$(id -un 2>/dev/null || true)
        { valid_owner "$_def" && [ "$_def" != system ]; } || _def=''
        tty_ok || die 4 "no terminal to ask for the owner name" "pass --owner NAME"
        printf 'owner name%s: ' "${_def:+ [$_def]}" >/dev/tty
        IFS= read -r OWNER </dev/tty || OWNER=''
        [ -n "$OWNER" ] || OWNER=$_def
        check_owner "$OWNER"
    fi

    [ ! -e "$DIR" ] && [ ! -L "$DIR" ] ||
        die 1 "$DIR already exists" \
            "use --start or --stop on it, --remove to delete it, or pick another --dir"

    echo "checked: nats-server $NSV, nats ${NCV:-?}, openssl"

    PORT=4222
    if port_taken 4222; then
        _n=4223
        while port_taken "$_n"; do
            _n=$((_n + 1))
            [ "$_n" -le 4272 ] || die 1 "no free port from 4222 to 4272" "free a port and retry"
        done
        PORT=$_n
        echo "port: $PORT (4222 is used by another server, left alone)"
    else
        echo "port: $PORT"
    fi

    trap cleanup EXIT
    trap 'exit 1' INT TERM HUP

    mkdir -m 700 "$DIR" || die 1 "cannot make $DIR" "check the folder and retry"
    CREATED=1
    cd "$DIR"
    mkdir -m 700 keys creds jetstream resolver nats-store nats-store/config

    # --- TLS: a private CA that may only vouch for localhost, 127.0.0.1 and ::1
    openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 3650 \
        -subj "/CN=mash-local-ca" -keyout ca.key -out ca.pem \
        -addext "basicConstraints=critical,CA:TRUE,pathlen:0" \
        -addext "keyUsage=keyCertSign,cRLSign" \
        -addext "nameConstraints=critical,permitted;DNS:localhost,permitted;IP:127.0.0.1/255.255.255.255,permitted;IP:::1/ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff" \
        2>/dev/null </dev/null
    openssl req -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -subj "/CN=localhost" \
        -keyout server.key -out server.csr 2>/dev/null </dev/null
    printf 'subjectAltName=DNS:localhost,IP:127.0.0.1,IP:::1\nextendedKeyUsage=serverAuth\n' >server.ext
    openssl x509 -req -in server.csr -CA ca.pem -CAkey ca.key -CAserial ca.srl -CAcreateserial \
        -days 3650 -extfile server.ext -out server.pem 2>/dev/null </dev/null
    rm -f server.csr server.ext ca.srl
    chmod 600 ca.key server.key
    chmod 644 ca.pem server.pem

    # --- operator, accounts, users (the nats key store stays inside DIR)
    nats_env
    OP=mash
    nats auth operator add "$OP" >/dev/null </dev/null
    nats auth account add "$OWNER" --operator "$OP" --defaults >/dev/null </dev/null
    nats auth account edit "$OWNER" --operator "$OP" --jetstream --js-disk 2147483648 \
        --js-memory 0 --js-disk-stream 0 --js-memory-stream 0 --js-streams 64 \
        --js-consumers 128 --js-max-pending 0 >/dev/null </dev/null
    nats auth user add mashadmin "$OWNER" --operator "$OP" --defaults \
        --credential "$DIR/creds/mashadmin.creds" >/dev/null </dev/null
    nats auth user add sys SYSTEM --operator "$OP" --defaults \
        --credential "$DIR/creds/sys.creds" >/dev/null </dev/null
    chmod 600 creds/mashadmin.creds creds/sys.creds

    ST=$XDG_DATA_HOME/nats/nsc/stores/$OP
    KS=$XDG_DATA_HOME/nats/nsc/keys/keys
    SYSPUB=$(acct_pub SYSTEM)
    ACCPUB=$(acct_pub "$OWNER")
    OPSIGN=$(nats auth operator info "$OP" 2>/dev/null </dev/null |
        sed -n 's/^ *Signing Keys: *\(O[A-Z2-7]*\).*$/\1/p' | head -n 1)
    [ -n "$SYSPUB" ] && [ -n "$ACCPUB" ] && [ -n "$OPSIGN" ] ||
        die 1 "could not read the new operator or accounts" "run $SELF --remove, then retry"
    [ -f "$(keyfile "$OPSIGN")" ] && [ -f "$(keyfile "$ACCPUB")" ] ||
        die 1 "could not find the new keys" "run $SELF --remove, then retry"
    cp "$(keyfile "$OPSIGN")" keys/operator.nk
    cp "$(keyfile "$ACCPUB")" keys/account.nk
    chmod 600 keys/operator.nk keys/account.nk
    cp "$ST/$OP.jwt" operator.jwt
    chmod 600 operator.jwt
    SYSJWT=$(tr -d '\n\r ' <"$ST/accounts/SYSTEM/SYSTEM.jwt")
    ACCJWT=$(tr -d '\n\r ' <"$ST/accounts/$OWNER/$OWNER.jwt")

    cat >server.conf <<CONF
listen: 127.0.0.1:$PORT
server_name: mash-local
tls {
  cert_file: "$DIR/server.pem"
  key_file: "$DIR/server.key"
}
jetstream {
  store_dir: "$DIR/jetstream"
}
operator: "$DIR/operator.jwt"
system_account: $SYSPUB
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
CONF
    chmod 600 server.conf

    cat >mash-setup.yaml <<YAML
servers: [tls://127.0.0.1:$PORT]
ca: $DIR/ca.pem
owner: $OWNER
admin_creds: $DIR/creds/mashadmin.creds
system_creds: $DIR/creds/sys.creds
operator_key: $DIR/keys/operator.nk
account_key: $DIR/keys/account.nk
YAML
    chmod 600 mash-setup.yaml

    # the marker: from here on a failure leaves DIR in place for --remove
    cat >mash-setup.json <<JSON
{
  "schema": 1,
  "owner": "$OWNER",
  "port": $PORT,
  "dir": "$DIR",
  "created": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
JSON
    chmod 600 mash-setup.json
    MARKED=1
    echo "made: CA, certificate, operator, accounts, users"

    start_server
    echo "started: nats-server pid $SPID on 127.0.0.1:$PORT"
    echo "checked: sign-in and system ping ok"
    echo "mashctl init --config $DIR/mash-setup.yaml"
}

# ---------------------------------------------------------------- main
main() {
    # --help is harmless anywhere; everything else stops first on macOS
    case ${1:-} in --help | -h) usage; exit 0 ;; esac
    _os=${MLNT_UNAME:-$(uname -s 2>/dev/null || true)}
    [ "$_os" != Darwin ] ||
        die 1 "this script sets up NATS on Linux only; on macOS, run it on a Linux machine or use a NATS server you already have"
    parse_args "$@"
    [ "$(id -u)" -ne 0 ] ||
        die 1 "do not run as root" "run as your own user; nothing was changed"
    if [ -n "$OWNER" ]; then check_owner "$OWNER"; fi
    set_paths
    check_dir_path
    case $MODE in
    stop) mode_stop ;;
    start) mode_start ;;
    remove) mode_remove ;;
    *) do_create ;;
    esac
}

main "$@"
