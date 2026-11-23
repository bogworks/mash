#!/usr/bin/env bash
# mash-local-nats-setup.sh: make, start, stop or remove a local NATS server
# for mash (operator mode, TLS, JetStream, 127.0.0.1 only) using nats-server,
# the nats command and openssl. It does not need nsc and installs nothing.
# It runs on Linux and on macOS (bash 3.2 and the BSD tools that ship with
# macOS are enough; Homebrew's nats-server and nats are found on PATH).
#
# Safety, in short: refuses root; DIR (default ~/mash-nats, or any absolute
# --dir) must be a new folder in a parent you can write to, with every folder
# above it owned by you or root and not writable by others, and no symlink
# unless root owns it; DIR is made exclusively (mode 0700);
# the failure trap removes only the DIR this run made, and only before the
# marker file (mash-setup.json) exists; --remove needs the path typed on a
# terminal; stop signals only the pid in server.pid after /proc confirms it
# is a nats-server started with this DIR's server.conf (read from /proc on
# Linux, from ps on macOS). Seeds and creds are
# never printed. No globs are used for any delete. Everything that does
# anything runs from main(), the last line of the file, so a truncated download
# piped into bash defines functions and runs nothing.
#
# One exclusive lifecycle lock (the file DIR/.lock, holding the pid) is held
# across the whole of create, --start, --stop and --remove, so they cannot
# overlap; a lock whose pid is gone is taken over, under a second exclusive
# file (DIR/.lock.recover), so only one run can take it over. If a run is
# killed in that tiny window, the next run stops and names the file to remove.
#
# Portability: file owner and mode come from GNU stat (-c) or BSD stat (-f),
# found by trying `stat -c %u /` once; process checks use /proc on Linux and
# kill -0 plus ps on macOS; listeners are read with ss (Linux) or lsof (macOS).
# The openssl commands avoid -addext and -noenc so that LibreSSL (macOS) and
# OpenSSL 3 both work.
#
# Test hook (not in --help): MLNT_UNAME, when set, replaces the output of
# `uname -s`; "Darwin" makes the script take the macOS branches (BSD stat
# flags, ps and lsof instead of /proc and ss), so a test can exercise them.
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

Works on Linux and macOS. Needs nats-server (2.10.7 or newer), nats and
openssl on PATH, and ss (Linux) or lsof (macOS) to look at ports.
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
    HOME_DIR=${HOME_DIR%/}
    DEFAULT_DIR=0
    if [ -z "$DIR" ]; then
        # HOME is needed only to choose the default folder
        case $HOME_DIR in
        /?*) ;;
        *) die 1 "HOME is not set to an absolute path" "set HOME and retry, or pass --dir" ;;
        esac
        DIR=$HOME_DIR/mash-nats
        DEFAULT_DIR=1
    fi
    DIR=${DIR%/}
    UID_NOW=$(id -u)
}

# Platform layer. BSD=1 means BSD userland (macOS): BSD stat flags, ps and lsof.
# MLNT_UNAME=Darwin forces it (test hook); otherwise it is chosen by whether
# GNU `stat -c` works here.
init_platform() {
    BSD=0
    if [ "${MLNT_UNAME:-$(uname -s 2>/dev/null || true)}" = Darwin ]; then
        BSD=1
    elif ! stat -c %u / >/dev/null 2>&1; then
        BSD=1
    fi
}

# fileowner PATH: numeric owner (of the link itself for a symlink);
# fileowner_l, filemode_l: the same but following a symlink; mode in octal
fileowner() {
    if [ "$BSD" = 1 ]; then stat -f %u "$1" 2>/dev/null; else stat -c %u "$1" 2>/dev/null; fi
}
fileowner_l() {
    if [ "$BSD" = 1 ]; then stat -L -f %u "$1" 2>/dev/null; else stat -L -c %u "$1" 2>/dev/null; fi
}
filemode_l() {
    if [ "$BSD" = 1 ]; then stat -L -f %Lp "$1" 2>/dev/null; else stat -L -c %a "$1" 2>/dev/null; fi
}

# check_parent_mode DIR: refuse a folder that group or others can write to,
# unless it has the sticky bit (like /tmp), since they could swap what is under it
check_parent_mode() {
    _m=$(filemode_l "$1") || die 1 "cannot read the mode of $1" "check the folder and retry"
    case $_m in '' | *[!0-7]*) die 1 "cannot read the mode of $1" "check the folder and retry" ;; esac
    _s=0
    [ "${#_m}" -lt 4 ] || _s=${_m%???}
    _o=${_m#"${_m%?}"} # last digit (others)
    _g=${_m%?}
    _g=${_g#"${_g%?}"} # the digit before it (group)
    if [ $(((${_g:-0} | ${_o:-0}) & 2)) -ne 0 ] && [ $((_s & 1)) -eq 0 ]; then
        die 1 "a parent folder is writable by others: $1 (mode $_m, no sticky bit)" \
            "run: chmod go-w $1 (or pick another --dir)"
    fi
}

check_dir_path() {
    case $DIR in
    /?*) ;;
    *) die 1 "--dir must be an absolute path, and not /: ${DIR:-/}" \
        "for example ${HOME_DIR:-/home/you}/mash-nats" ;;
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
    _o=$(fileowner_l "$1") || die 1 "cannot read $1" "check the folder and retry"
    if [ "$_o" != "$UID_NOW" ] && [ "$_o" != 0 ]; then
        die 1 "not owned by you or root: $1" "pick a folder you own with --dir"
    fi
    check_parent_mode "$1"
}

# ---------------------------------------------------------------- helpers
tty_ok() { ( exec </dev/tty ) 2>/dev/null; }

marker_get() { # KEY: string value from mash-setup.json (one key per line)
    sed -n 's/^ *"'"$1"'": *\(.*\)$/\1/p' "$DIR/mash-setup.json" | head -n 1 |
        sed 's/[ ,]*$//; s/^"//; s/"$//'
}

# proc_alive PID: the process exists and is not a zombie
proc_alive() {
    if [ "$BSD" = 1 ]; then
        kill -0 "$1" 2>/dev/null || return 1
        _st=$(ps -p "$1" -o stat= 2>/dev/null | head -n 1 | tr -d ' ')
        case $_st in '' | Z* | X*) return 1 ;; esac
        return 0
    fi
    _st=$(sed -n 's/^State:[[:space:]]*\(.\).*/\1/p' "/proc/$1/status" 2>/dev/null)
    [ -n "$_st" ] && [ "$_st" != Z ] && [ "$_st" != X ]
}
# proc_ours PID: running nats-server whose arguments name this DIR's server.conf
proc_ours() {
    proc_alive "$1" || return 1
    if [ "$BSD" = 1 ]; then
        _exe=$(ps -p "$1" -o comm= 2>/dev/null | head -n 1 | sed 's/^ *//; s/ *$//')
        [ "${_exe##*/}" = nats-server ] || return 1
        ps -ww -p "$1" -o args= 2>/dev/null | head -n 1 | tr ' ' '\n' | grep -Fxq -- "$DIR/server.conf"
        return
    fi
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

# need_port_tool: ss (Linux) or lsof (macOS), whichever this platform prefers
need_port_tool() {
    PORT_TOOL=''
    if [ "$BSD" = 1 ]; then
        if command -v lsof >/dev/null 2>&1; then PORT_TOOL=lsof
        elif command -v ss >/dev/null 2>&1; then PORT_TOOL=ss; fi
    else
        if command -v ss >/dev/null 2>&1; then PORT_TOOL=ss
        elif command -v lsof >/dev/null 2>&1; then PORT_TOOL=lsof; fi
    fi
    [ -n "$PORT_TOOL" ] ||
        die 1 "need ss (Linux) or lsof (macOS)" "install it (Linux: apt install iproute2); nothing was changed"
}
# listen_addrs [PORT]: the local addresses (ADDR:PORT, one per line) of TCP listeners
listen_addrs() {
    if [ "$PORT_TOOL" = lsof ]; then
        lsof -nP "-iTCP${1:+:$1}" -sTCP:LISTEN -Fn 2>/dev/null </dev/null | sed -n 's/^n//p'
    else
        ss -ltn 2>/dev/null | awk 'NR>1 {print $4}'
    fi
}

listening() { # is 127.0.0.1:$PORT accepting
    listen_addrs "$PORT" | grep -Fxq "127.0.0.1:$PORT"
}

port_taken() { # PORT
    listen_addrs "$1" | grep -Eq "[:.]$1\$"
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
    _tl=$(tail -n 5 "$DIR/server.log" 2>/dev/null | tr -d '\000' || true)
    printf '%s\n' "$_tl" |
        grep -Eva 'S[OANUCX][A-Z2-7]{50,}|eyJ[A-Za-z0-9_-]{20,}|-----' |
        cut -c1-200 | sed 's/^/  /' >&2 || true
    _foreign=0
    if printf '%s\n' "$_tl" | grep -Fq 'address already in use' && ! { proc_alive "$SPID" && listening; }; then
        _foreign=1
    fi
    if proc_ours "$SPID"; then kill -TERM "$SPID" 2>/dev/null || true; rm -f "$DIR/server.pid"; fi
    SAID=1
    if [ "$_foreign" = 1 ]; then
        echo "error: port $PORT may be in use by another user (this system shows only your own listeners)" >&2
        # the folder is kept (it has its marker), so the next step is --start in both modes
        printf 'hint: free port %s or ask who holds it, then run %s --start again (or %s --remove to start over)\n' \
            "$PORT" "$SELF" "$SELF" >&2
        exit 1
    fi
    printf 'hint: fix the cause and run %s --start, or %s --remove to start over\n' \
        "$SELF" "$SELF" >&2
    exit 1
}

# ---------------------------------------------------------------- lock
LOCKED=0
RLOCKED=0
release_lock() {
    if [ "$RLOCKED" = 1 ]; then
        RLOCKED=0
        [ "$(cat "$DIR/.lock.recover" 2>/dev/null || true)" != "$$" ] || rm -f "$DIR/.lock.recover" 2>/dev/null || true
    fi
    if [ "$LOCKED" = 1 ]; then
        LOCKED=0
        [ "$(cat "$DIR/.lock" 2>/dev/null || true)" != "$$" ] || rm -f "$DIR/.lock" 2>/dev/null || true
    fi
    return 0
}
busy() { die 1 "another run is using $DIR (pid ${1:-?})" "wait for it to finish, then run $SELF again"; }
# the lock is the file DIR/.lock holding the pid, made with noclobber (O_EXCL),
# which is atomic and exclusive on every system (mkdir is not, in uutils
# coreutils on Ubuntu 26.04)
lock_make() { ( set -C; printf '%s\n' "$$" >"$DIR/.lock" ) 2>/dev/null; }
# a pid read from a lock file: digits only, else empty
lock_pid() {
    _v=$(cat "$1" 2>/dev/null || true)
    case $_v in *[!0-9]*) _v='' ;; esac
    printf '%s' "$_v"
}
# take over a stale DIR/.lock (its pid, $1, is gone) under a second lock,
# DIR/.lock.recover, so that only one run can do it. Returns 0 with the lock
# taken, or 1 when another run's recovery finished meanwhile (the caller looks
# at .lock again).
lock_recover() {
    _stale=$1
    if ! ( set -C; printf '%s\n' "$$" >"$DIR/.lock.recover" ) 2>/dev/null; then
        _rp=$(lock_pid "$DIR/.lock.recover")
        if [ -n "$_rp" ] && kill -0 "$_rp" 2>/dev/null; then busy "$_rp"; fi
        sleep 1
        # a file that still holds the same dead pid after the wait was left by
        # a stopped run; anything else is a finished recovery by another run
        if [ -n "$_rp" ] && [ "$(lock_pid "$DIR/.lock.recover")" = "$_rp" ]; then
            die 1 "$DIR/.lock.recover is left over from a run that was stopped while it took over a stale lock" \
                "remove that one file ($DIR/.lock.recover), then run $SELF again"
        fi
        return 1
    fi
    RLOCKED=1
    # re-read under the recover lock: remove .lock only if it is still the stale one
    _cur=$(lock_pid "$DIR/.lock")
    if [ -n "$_cur" ] && [ "$_cur" != "$_stale" ]; then busy "$_cur"; fi
    if [ -d "$DIR/.lock" ] && [ ! -L "$DIR/.lock" ]; then
        rmdir "$DIR/.lock" 2>/dev/null || busy "$_stale"
    else
        rm -f "$DIR/.lock" 2>/dev/null || busy "$_stale"
    fi
    lock_make || busy "$_stale"
    LOCKED=1
    RLOCKED=0
    [ "$(cat "$DIR/.lock.recover" 2>/dev/null || true)" != "$$" ] || rm -f "$DIR/.lock.recover" 2>/dev/null || true
}
lock_dir() {
    _try=0
    while [ "$_try" -lt 3 ]; do
        _try=$((_try + 1))
        lock_make && { LOCKED=1; return 0; }
        _lp=$(cat "$DIR/.lock" 2>/dev/null || true)
        if [ -z "$_lp" ]; then
            sleep 1
            _lp=$(cat "$DIR/.lock" 2>/dev/null || true)
        fi
        case $_lp in *[!0-9]*) _lp='' ;; esac
        if [ -n "$_lp" ] && kill -0 "$_lp" 2>/dev/null; then busy "$_lp"; fi
        # stale: its run is gone; take over under .lock.recover
        lock_recover "$_lp" && return 0
    done
    busy "$_lp"
}
lock_mode() { # for --start, --stop, --remove
    trap release_lock EXIT
    trap 'exit 1' INT TERM HUP
    lock_dir
}

# ---------------------------------------------------------------- stop/start/remove
mode_stop() {
    require_made_dir
    lock_mode
    do_stop
}

mode_start() {
    require_made_dir
    lock_mode
    if _p=$(recorded_pid) && proc_ours "$_p"; then
        echo "already running: pid $_p on 127.0.0.1:$PORT"; return 0
    fi
    miss=''
    for t in nats-server nats; do command -v "$t" >/dev/null 2>&1 || miss="$miss $t"; done
    [ -z "$miss" ] || die 1 "missing:$miss" "install it: brew install nats-server nats (macOS), or see the docs"
    need_port_tool
    if port_taken "$PORT"; then
        die 1 "port $PORT is now used by another program" \
            "stop that program, then run $SELF --start again"
    fi
    start_server
    echo "started: nats-server pid $SPID on 127.0.0.1:$PORT"
}

mode_remove() {
    require_made_dir
    need_port_tool
    tty_ok || die 4 "--remove needs a terminal to confirm" \
        "run it from a terminal; nothing was changed"
    lock_mode
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
    # mashctl keeps its own folder; this script never touches it. Name the one
    # mashctl uses: $MASH_CONFIG_DIR, else an absolute $XDG_CONFIG_HOME/mash,
    # else $HOME/.config/mash
    case "${MASH_CONFIG_DIR:-}" in
        /*) mc=$MASH_CONFIG_DIR ;;
        *) case "${XDG_CONFIG_HOME:-}" in
            /*) mc=$XDG_CONFIG_HOME/mash ;;
            *) mc=${HOME:-~}/.config/mash ;;
           esac ;;
    esac
    echo "note: mashctl's folder $mc still points at this server."
    echo "To set up again, run mashctl init --config <file> --config-dir <new folder>"
    echo "or move that folder aside."
}

# ---------------------------------------------------------------- create
add_miss() { miss="${miss:+$miss, }$1"; hints="${hints:+$hints; }$2"; }
cleanup() {
    _rc=$?
    trap - EXIT INT TERM
    release_lock
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
    [ -z "$miss" ] || die 1 "missing: $miss" "$hints"
    need_port_tool

    NSV=$(nats-server --version 2>/dev/null </dev/null |
        sed -n 's/^[^0-9]*\([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\).*/\1/p' | head -n 1)
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
    # mkdir is not exclusive everywhere (uutils), so the lock decides who made DIR;
    # CREATED is set only by the winner, and a loser never removes anything
    lock_dir
    for _f in "$DIR"/* "$DIR"/.[!.]*; do
        [ ! -e "$_f" ] && [ ! -L "$_f" ] || [ "$_f" = "$DIR/.lock" ] ||
            die 1 "$DIR already exists" "pick another --dir, or use --start, --stop or --remove on it"
    done
    CREATED=1
    cd "$DIR"
    mkdir -m 700 keys creds jetstream resolver nats-store nats-store/config

    # --- TLS: a private CA that may only vouch for localhost, 127.0.0.1 and ::1
    # (config files, not -addext or -noenc, so LibreSSL on macOS works too;
    # -sha256 because LibreSSL's x509 -req signs with SHA-1 otherwise, which Go refuses)
    cat >ca.cnf <<'CNF'
[req]
distinguished_name = dn
prompt = no
x509_extensions = v3ca
[dn]
CN = mash-local-ca
[v3ca]
basicConstraints = critical,CA:TRUE,pathlen:0
keyUsage = keyCertSign,cRLSign
nameConstraints = critical,permitted;DNS:localhost,permitted;IP:127.0.0.1/255.255.255.255,permitted;IP:::1/ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff
CNF
    cat >server.cnf <<'CNF'
[req]
distinguished_name = dn
prompt = no
[dn]
CN = localhost
[v3srv]
subjectAltName = DNS:localhost,IP:127.0.0.1,IP:::1
extendedKeyUsage = serverAuth
CNF
    openssl ecparam -genkey -name prime256v1 -noout -out ca.key 2>/dev/null </dev/null
    openssl req -new -x509 -sha256 -key ca.key -days 3650 -config ca.cnf -extensions v3ca \
        -out ca.pem 2>/dev/null </dev/null
    openssl ecparam -genkey -name prime256v1 -noout -out server.key 2>/dev/null </dev/null
    openssl req -new -sha256 -key server.key -config server.cnf -out server.csr 2>/dev/null </dev/null
    openssl x509 -req -sha256 -in server.csr -CA ca.pem -CAkey ca.key -CAserial ca.srl -CAcreateserial \
        -days 3650 -extfile server.cnf -extensions v3srv -out server.pem 2>/dev/null </dev/null
    rm -f server.csr ca.cnf server.cnf ca.srl
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
    # the nats command signs the accounts with the operator's own (identity) key, not
    # its signing key, and mashctl init needs operator.nk to be the key that signed them
    OPSIGN=$(nats auth operator info "$OP" 2>/dev/null </dev/null |
        sed -n '1s/^Operator .* (\(O[A-Z2-7]*\))$/\1/p')
    # every file the next steps read must be where this nats version puts it
    _ok=1
    [ -n "$SYSPUB" ] && [ -n "$ACCPUB" ] && [ -n "$OPSIGN" ] || _ok=0
    if [ "$_ok" = 1 ]; then
        for _f in "$DIR/creds/mashadmin.creds" "$DIR/creds/sys.creds" "$ST/$OP.jwt" \
            "$ST/accounts/SYSTEM/SYSTEM.jwt" "$ST/accounts/$OWNER/$OWNER.jwt" \
            "$(keyfile "$OPSIGN")" "$(keyfile "$ACCPUB")"; do
            [ -f "$_f" ] || _ok=0
        done
    fi
    [ "$_ok" = 1 ] ||
        die 1 "the nats command stored its keys somewhere unexpected (tested with nats v0.5.2)" \
            "use nats v0.5.2, or report this; nothing was left behind"
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
    release_lock
    echo "mashctl init --config $DIR/mash-setup.yaml"
}

# ---------------------------------------------------------------- main
main() {
    parse_args "$@"
    init_platform
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
