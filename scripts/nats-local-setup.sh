#!/usr/bin/env bash
# nats-local-setup.sh: stand up and look after a local operator-mode NATS
# server for mash on Debian or Ubuntu (Linux x86-64). It does not use
# mashctl and never writes to mashctl's folder; create ends by printing the
# `mashctl adopt` command to run.
#
# Layout rule for this file: indent with 4 spaces (no tabs) and keep every
# source line and every printed line within 80 columns. Printed text goes
# through wrap(), which breaks at spaces; only an unbreakable word (a very
# long path) can exceed 80.
#
# Safety model, in short:
#  - Paths: absolute; every component from / down must be a real directory
#    owned by root or you and not writable by group or others. The script
#    enters the verified parent with cd -P, makes DIR with mkdir relative to
#    that directory, re-checks device:inode, cd's into DIR and writes only
#    through relative paths from there.
#  - Tools: nats-server and nsc are downloaded from pinned URLs, compared
#    with a literal sha256 in this file BEFORE extraction or execution, and
#    installed into DIR/bin. Only DIR/bin/nats-server and DIR/bin/nsc are
#    ever run; a copy on PATH is never executed.
#  - Output: everything a subprocess prints goes to a private temp file and
#    through safe(), which withholds secret-looking lines.
#  - Decisions that matter (non-loopback listen, retire-keys, remove, boot)
#    need a person typing a confirmation at a terminal.
set -euC
umask 077
MAINPID=$$

# ------------------------------------------------------- pinned downloads
NS_VER=v2.15.1
NS_VERLINE='nats-server: v2.15.1'
NS_BASE=https://github.com/nats-io/nats-server/releases/download/v2.15.1
NS_FILE=nats-server-v2.15.1-linux-amd64.tar.gz
NS_MEMBER=nats-server-v2.15.1-linux-amd64/nats-server
NS_SHA=bf5edc7b1ce98e885c368cb43a886eb148dc5d0551d99f3cf6141fb5e1954c70
NSC_VER=v2.15.0
NSC_VERLINE='nsc version 2.15.0'
NSC_BASE=https://github.com/nats-io/nsc/releases/download/v2.15.0
NSC_FILE=nsc-linux-amd64.zip
NSC_SHA=7d55eda757dc9f233675a3038fcf8779bcda99753b1603f7009fc8537e126b7e

usage() {
    cat <<'EOF'
usage: nats-local-setup.sh [create] --dir DIR --owner NAME [options]
       nats-local-setup.sh --dir DIR --start | --stop | --check | --upgrade
       nats-local-setup.sh --dir DIR --backup DEST | --retire-keys DEST
       nats-local-setup.sh --dir DIR --enable-boot [--linger]
       nats-local-setup.sh --dir DIR --disable-boot | --remove

Linux x86-64 (amd64), Debian or Ubuntu only. Needs: curl tar unzip openssl
sha256sum ss. Exactly one subcommand per run; create is the default.

  --dir DIR        required, absolute. Every component must be a real
                   directory (no symlinks) owned by you or root and not
                   writable by group or others (so not under /tmp: pick a
                   directory under your home). create makes DIR itself
                   (mode 0700) and refuses an existing path
  --owner NAME     create: your mash owner name (lower-case letters,
                   digits, single hyphens, starts with a letter, max 32)
  --mash-config D  where mashctl keeps its files (default
                   $HOME/.config/mash); used only in the closing text
  --port N         client port (default 4222)
  --host NAME      repeatable; extra DNS name or IP for the server
                   certificate (localhost, 127.0.0.1 and ::1 always
                   included); the first one is used in the URL
  --listen ADDR    listen address (default 127.0.0.1). Anything but
                   127.0.0.1, ::1 or localhost needs you to type the
                   address:port at a terminal
  --account NAME   account for mash (default MASH)
  --operator NAME  operator name (default mash-operator)
  --js-disk SIZE   JetStream disk, like 500M or 2G (default 2G)
  --start          with create: start the server afterwards. Alone: start
                   the server of an existing setup
  --stop           stop the server recorded in DIR/server.pid
  --check          verify files, modes, tools, config, TLS and what listens
  --upgrade        install this script's pinned nats-server and nsc
  --backup DEST    one tar.gz (0600) of DIR except DIR/bin; it holds secrets
  --retire-keys DEST  move ca.key, nsc/ and export/ into one tar.gz, then
                   remove them from DIR (typed confirmation)
  --enable-boot    systemd user unit that keeps the server up (typed
                   confirmation); --linger also runs loginctl enable-linger
  --disable-boot   remove that unit (typed confirmation)
  --remove         delete DIR (typed confirmation; only a directory this
                   script made, found by DIR/.mash-nats-setup)
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
# safe: stdin minus lines that look like secrets (nkey seeds, JWTs, PEM
# blocks, "seed/secret/password/creds: value"), then a count of withheld
# lines. Control characters are dropped. EVERY subprocess output that
# reaches the terminal or a kept file goes through this.
SECRE='S[OANUCX][A-Z2-7]{40,}|-----BEGIN|-----END|eyJ[A-Za-z0-9_-]{10,}'
SECRE="$SECRE|([Ss]eed|[Cc]reds|[Ss]ecret|[Pp]assword|[Tt]oken)[^ ]* *[:=]"
safe() {
    _in=$(cat | tr -d '\000-\010\013-\037\177')
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
fold80() { fold -w 80; }

die() { wrap 'nats-local-setup: ' '    ' "$*" >&2; exit 1; }
say() { wrap '' '    ' "$*"; }
step() { N=$((N + 1)); printf '\n'; wrap "[$N] about to: " '    ' "$*"; }
did() { wrap '    did: ' '         ' "$*"; }

N=0
DIR=''
OWNER=''
MASHCFG=''
MASHCFG_GIVEN=0
PORT=4222
LISTEN=127.0.0.1
ACCOUNT=MASH
OPERATOR=mash-operator
JSDISK=2G
HOSTS=''
MODE=''
START=0
LINGER=0
DEST=''
LOG=''
CREATE_OPTS=0
ORIG=("$@")

# SELFABS: this script's absolute path ($0 resolved), taken before any cd.
SELFABS=$(cd -P -- "$(dirname -- "$0")" 2>/dev/null && pwd -P)
SELFABS=$SELFABS/$(basename -- "$0")

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

# pathchars WHAT VALUE: refuse characters that could change the meaning of
# a value written into server.conf, a unit file or a shell word.
pathchars() {
    case $2 in
    *[[:cntrl:]]*) die "$1 must not contain control characters or newlines" ;;
    esac
    case $2 in
    *'"'* | *"'"* | *\\* | *'$'* | *'`'* | *'{'* | *'}'* | *'#'*)
        die "$1 must not contain any of: double quote, single quote," \
            "backslash, dollar sign, backtick, braces, hash (they would" \
            "change how server.conf is read). Offending value: $2" ;;
    esac
}
# normpath WHAT VALUE: absolute, no . or .. components, // squeezed.
normpath() {
    _n=$2
    case $_n in /*) ;; *) die "$1 must be an absolute path: $_n" ;; esac
    case "/$_n/" in
    */../* | */./*) die "$1 must not contain . or .. components: $_n" ;;
    esac
    while :; do
        case $_n in *//*) _n=${_n%%//*}/${_n#*//} ;; *) break ;; esac
    done
    _n=${_n%/}
    [ -n "$_n" ] || die "refusing '/' as $1"
    printf '%s' "$_n"
}

need_val() { [ "$#" -ge 2 ] || die "$1 needs a value"; }
setmode() {
    [ -z "$MODE" ] || [ "$MODE" = "$1" ] ||
        die "one subcommand per run: both $MODE and $1 were given"
    MODE=$1
}
while [ "$#" -gt 0 ]; do
    case $1 in
    --dir) need_val "$@"; DIR=$2; shift 2 ;;
    --owner) need_val "$@"; OWNER=$2; CREATE_OPTS=1; shift 2 ;;
    --mash-config) need_val "$@"; MASHCFG=$2; MASHCFG_GIVEN=1; shift 2 ;;
    --port) need_val "$@"; PORT=$2; CREATE_OPTS=1; shift 2 ;;
    --host)
        need_val "$@"
        valid_host "$2" ||
            die "--host: use a DNS name or an IPv4/IPv6 address"
        HOSTS="$HOSTS $2"; CREATE_OPTS=1; shift 2 ;;
    --listen) need_val "$@"; LISTEN=$2; CREATE_OPTS=1; shift 2 ;;
    --account) need_val "$@"; ACCOUNT=$2; CREATE_OPTS=1; shift 2 ;;
    --operator) need_val "$@"; OPERATOR=$2; CREATE_OPTS=1; shift 2 ;;
    --js-disk) need_val "$@"; JSDISK=$2; CREATE_OPTS=1; shift 2 ;;
    --start) START=1; shift ;;
    --linger) LINGER=1; shift ;;
    --stop) setmode stop; shift ;;
    --check) setmode check; shift ;;
    --upgrade) setmode upgrade; shift ;;
    --enable-boot) setmode enable-boot; shift ;;
    --disable-boot) setmode disable-boot; shift ;;
    --remove) setmode remove; shift ;;
    --backup) need_val "$@"; setmode backup; DEST=$2; shift 2 ;;
    --retire-keys) need_val "$@"; setmode retire-keys; DEST=$2; shift 2 ;;
    --help | -h) usage; exit 0 ;;
    *) usage >&2; die "unknown argument: $1" ;;
    esac
done

# --start with --owner (or other create options) is create-and-start; alone
# it starts an existing setup.
if [ "$START" -eq 1 ]; then
    if [ -z "$MODE" ] && [ "$CREATE_OPTS" -eq 0 ]; then MODE=start
    elif [ -z "$MODE" ]; then MODE=create
    elif [ "$MODE" != create ]; then
        die "--start cannot be combined with --$MODE"
    fi
fi
[ -n "$MODE" ] || MODE=create
if [ "$MODE" != create ] && [ "$CREATE_OPTS" -eq 1 ]; then
    die "--owner, --port, --host, --listen, --account, --operator and" \
        "--js-disk apply to create only"
fi
[ "$LINGER" -eq 0 ] || [ "$MODE" = enable-boot ] ||
    die "--linger applies to --enable-boot only"
[ "$MASHCFG_GIVEN" -eq 0 ] || [ "$MODE" = create ] || [ "$MODE" = remove ] ||
    die "--mash-config applies to create and --remove only"

[ -n "$DIR" ] || {
    usage >&2
    die "--dir is required (there is no default location)"
}

# ------------------------------------------------- platform, prerequisites
case $(uname -s) in Linux) ;; *)
    die "this helper supports Linux x86-64 (amd64) on Debian or Ubuntu" \
        "only; this is $(uname -s)" ;;
esac
case $(uname -m) in x86_64 | amd64) ;; *)
    die "this helper supports Linux x86-64 (amd64) on Debian or Ubuntu" \
        "only; this machine is $(uname -m)" ;;
esac
# shellcheck disable=SC1091
OSID=$( (. /etc/os-release 2>/dev/null && echo " ${ID:-} ${ID_LIKE:-} ") ||
    true)
case $OSID in *" debian "* | *" ubuntu "*) ;; *)
    die "this helper supports Debian or Ubuntu only (/etc/os-release says" \
        "${OSID:-nothing})" ;;
esac
case $MODE in
create | upgrade) NEED='curl tar unzip openssl sha256sum ss' ;;
check) NEED='openssl sha256sum ss' ;;
backup | retire-keys) NEED='tar sha256sum' ;;
start | stop | enable-boot | disable-boot | remove) NEED='sha256sum' ;;
esac
miss=''
pk=''
for t in $NEED; do
    command -v "$t" >/dev/null 2>&1 && continue
    miss="$miss $t"
    case $t in sha256sum) p=coreutils ;; ss) p=iproute2 ;; *) p=$t ;; esac
    pk="$pk $p"
done
[ -z "$miss" ] ||
    die "missing prerequisite(s):$miss. Install them with:" \
        "sudo apt-get install -y$pk"
case " $NEED " in *" openssl "*)
    ov=$(openssl version 2>&1 | safe)
    case $ov in
    'OpenSSL '[3-9].* | 'OpenSSL '[1-9][0-9].*) ;;
    *) die "need OpenSSL 3 or newer; 'openssl version' says: $ov" ;;
    esac ;;
esac

# ----------------------------------------------------------- validation
DIR=$(normpath --dir "$DIR")
pathchars --dir "$DIR"
ncomp=$(printf '%s' "$DIR" | tr -s '/' '\n' | grep -c .) || true
[ "$ncomp" -ge 3 ] ||
    die "refusing --dir $DIR: needs at least 3 path components"
if [ -z "$MASHCFG" ]; then
    [ -n "${HOME:-}" ] || die "HOME is not set: give --mash-config"
    MASHCFG=$HOME/.config/mash
fi
MASHCFG=$(normpath --mash-config "$MASHCFG")
pathchars --mash-config "$MASHCFG"
case $MODE in
backup | retire-keys)
    DEST=$(normpath "--$MODE DEST" "$DEST")
    pathchars "--$MODE DEST" "$DEST"
    case "$DEST/" in "$DIR"/*) die "--$MODE DEST must be outside $DIR" ;;
    esac ;;
enable-boot | disable-boot)
    case $DIR in *%*) die "--dir must not contain % (systemd would read it" \
        "as a specifier)" ;; esac
    [ -n "${HOME:-}" ] || die "HOME is not set" ;;
esac
if [ "$MODE" = create ]; then
    [ -n "$OWNER" ] || die "create needs --owner NAME (your mash owner)"
    [[ $OWNER =~ ^[a-z]([a-z0-9]|-[a-z0-9])*$ ]] && [ "${#OWNER}" -le 32 ] ||
        die "--owner: lower-case letters, digits and single hyphens," \
            "starting with a letter, at most 32 characters"
    case $PORT in '' | *[!0-9]*) die "--port must be a number" ;; esac
    [ "$PORT" -ge 1 ] && [ "$PORT" -le 65535 ] ||
        die "--port out of range: $PORT"
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
fi
LOOPBACK=0
case $LISTEN in 127.0.0.1 | ::1 | '[::1]' | localhost) LOOPBACK=1 ;; esac

ME=$(id -u)
# Q VALUE: shell-quoted for pasting.
Q() { printf '%q' "$1"; }
uid_of() { stat -c %u -- "$1"; }
rawmode() { stat -c %a -- "$1"; }
inode_of() { stat -c %d:%i -- "$1"; }

# emit_cmd INDENT WORD...: print a command, breaking with " \" before 80.
emit_cmd() {
    _ind=$1
    shift
    _line=''
    for _t in "$@"; do
        if [ -z "$_line" ]; then _line=$_ind$_t
        elif [ $((${#_line} + ${#_t} + 3)) -le 80 ]; then _line="$_line $_t"
        else printf '%s \\\n' "$_line"; _line="$_ind    $_t"; fi
    done
    printf '%s\n' "$_line"
}
# own_cmd ARGS...: this script's command line with ARGS, quoted.
own_cmd() {
    _w=("$(Q "$SELFABS")")
    for _a in "$@"; do _w+=("$(Q "$_a")"); done
    emit_cmd "${OWNIND:-  }" "${_w[@]}"
}

# ------------------------------------------------- typed confirmation (F1)
# need_terminal: stdin and stdout must be a terminal. Otherwise print the
# exact command for the person to run in their own terminal and stop.
need_terminal() { # WHAT
    [ -t 0 ] && [ -t 1 ] && return 0
    printf '\n' >&2
    wrap 'nats-local-setup: ' '    ' "$1 needs a person at a terminal (stdin" \
        "and stdout must be a terminal) and nothing was changed. Run this" \
        "yourself in a terminal; it will show what it does and ask you" \
        "to type a confirmation:" >&2
    printf '\n' >&2
    _w=("$(Q "$SELFABS")")
    for _a in "${ORIG[@]}"; do _w+=("$(Q "$_a")"); done
    emit_cmd '  ' "${_w[@]}" >&2
    exit 1
}
# confirm TEXT: ask the person to type TEXT exactly.
confirm() {
    printf '\nType this exactly to go ahead, or anything else to stop:\n'
    printf '    %s\n> ' "$1"
    _ans=''
    IFS= read -r _ans || true
    [ "$_ans" = "$1" ] ||
        die "that was not the confirmation text: nothing was changed"
}

# ---------------------------------------------------- path safety (F2)
# chain_one P: a real directory (not a symlink), owned by root or you, not
# writable by group or others (sticky or not).
chain_one() {
    [ ! -L "$1" ] || die "$1 is a symlink: refusing. Use the real path."
    [ -d "$1" ] || die "$1 does not exist or is not a directory"
    _u=$(uid_of "$1")
    [ "$_u" = "$ME" ] || [ "$_u" = 0 ] ||
        die "$1 is owned by uid $_u: it must be owned by you ($ME) or root"
    _m=$(rawmode "$1")
    if [ $((8#$_m & 8#022)) -ne 0 ]; then
        die "$1 (mode $_m) is writable by group or others, so another" \
            "user could swap things inside it. Pick a directory under your" \
            "home (not /tmp)."
    fi
}
# chain_ok PATH: chain_one for / and every component down to PATH.
chain_ok() {
    chain_one /
    _cur=''
    IFS=/ read -ra _parts <<<"${1#/}"
    for _c in "${_parts[@]}"; do
        _cur=$_cur/$_c
        chain_one "$_cur"
    done
}
# enter PATH: verify the chain, cd -P into it, confirm where we landed.
enter() {
    chain_ok "$1"
    cd -P -- "$1" || die "cannot enter $1"
    [ "$(pwd -P)" = "$1" ] || die "$1 is not a plain path (it resolves to" \
        "$(pwd -P))"
}
# chkdir: DIR still names the directory we are in (device:inode).
chkdir() {
    [ "$(pwd -P)" = "$DIR" ] && [ "$(inode_of "$DIR" 2>/dev/null)" = \
        "$CWDID" ] || die "$DIR no longer leads to the directory this run" \
        "is working in (something replaced a path component): stopping"
}
# split_dest: DESTPARENT and DESTNAME from DEST; DEST must not exist and its
# parent must pass the same checks as --dir's parent.
split_dest() {
    DESTPARENT=${DEST%/*}
    DESTNAME=${DEST##*/}
    [ -n "$DESTPARENT" ] || DESTPARENT=/
    chain_ok "$DESTPARENT"
    [ ! -e "$DEST" ] && [ ! -L "$DEST" ] ||
        die "$DEST already exists: choose a path that does not exist"
}

# ---------------------------------------------------------- run helpers
# Tool output goes to the temp file $LOG (mode 0600, removed on every exit),
# never to a persistent log. On failure only a redacted tail is shown.
cleanup() { [ -z "$LOG" ] || rm -f -- "$LOG"; }
trap cleanup EXIT
trap 'exit 1' INT TERM HUP
mklog() {
    LOG=$(mktemp ./.cmd.XXXXXX) || die "cannot make a temp file in $DIR"
}
capfail() { # WHAT
    tail -n 15 "$LOG" | safe | fold80 >&2
    die "$1 failed (its output is not kept: it may hold secrets)"
}
runlog() { # WHAT CMD...: run, output to $LOG; failure shows a redacted tail
    _wh=$1
    shift
    : >|"$LOG"
    "$@" >>"$LOG" 2>&1 || capfail "$_wh"
}
shown() { # print the first line of $LOG, redacted
    sed -n 1p "$LOG" | safe
}

# ------------------------------------------------------------ server state
CONF=$DIR/server.conf
PIDF=server.pid
UNITNAME=mash-nats-$(printf '%s' "$DIR" | sha256sum | cut -c1-12).service
UNITDIR=${HOME:-/nonexistent}/.config/systemd/user
UNITPATH=$UNITDIR/$UNITNAME
UNITMARK="# written by nats-local-setup.sh for $DIR"

uidline() { id -un 2>/dev/null || echo "$ME"; }
# alive PID: running (a zombie nobody has reaped yet counts as gone).
alive() {
    _st=$(sed -n 's/^.*) \(.\).*$/\1/p' "/proc/$1/stat" 2>/dev/null) ||
        return 1
    case $_st in '' | Z*) return 1 ;; *) return 0 ;; esac
}
# ours PID: its command line is exactly DIR/bin/nats-server -c DIR/server.conf
ours() {
    alive "$1" || return 1
    cmp -s <(printf '%s\0%s\0%s\0' "$DIR/bin/nats-server" -c "$CONF") \
        "/proc/$1/cmdline"
}
boot_ours() {
    [ -f "$UNITPATH" ] && [ ! -L "$UNITPATH" ] &&
        [ "$(sed -n 1p "$UNITPATH")" = "$UNITMARK" ]
}
# srv_pid: print the pid of this directory's running server, if any.
srv_pid() {
    _p=''
    if boot_ours; then
        _p=$(systemctl --user show -p MainPID --value "$UNITNAME" \
            2>/dev/null || true)
        [ "$_p" != 0 ] || _p=''
    elif [ -f "$PIDF" ]; then
        _p=$(cat "$PIDF")
    fi
    case $_p in '' | *[!0-9]*) return 0 ;; esac
    ! ours "$_p" || printf '%s' "$_p"
}
conf_listen() {
    sed -n 's/^listen: *"\(.*\):\([0-9]*\)"$/\1 \2/p' server.conf |
        sed -n 1p
}
# probe_host: an address a client can use for a listen address.
probe_host() {
    case $1 in
    0.0.0.0 | '' | '[::]' | '::') printf '127.0.0.1' ;;
    \[*\]) _h=${1#\[}; printf '%s' "${_h%\]}" ;;
    *) printf '%s' "$1" ;;
    esac
}
# port_open HOST PORT: succeeds if something accepts a TCP connection there.
port_open() { (exec 3<>"/dev/tcp/$1/$2") 2>/dev/null; }
# tls_required HOST PORT: the NATS INFO line announces that TLS is required.
tls_required() {
    exec 3<>"/dev/tcp/$1/$2" || return 1
    line=''
    read -r -t 5 line <&3 || true
    exec 3<&- 3>&-
    case $line in INFO*'"tls_required":true'*) return 0 ;; *) return 1 ;; esac
}
sysd() { # systemctl --user ARGS...: output to $LOG, redacted tail on failure
    runlog "systemctl --user $1" systemctl --user "$@"
}

# srv_stop: stop this directory's server (pid file or boot unit).
srv_stop() {
    if boot_ours; then
        _p=$(srv_pid)
        [ -n "$_p" ] || { say "the boot unit is not running"; return 0; }
        sysd stop "$UNITNAME"
        did "stopped $UNITNAME (pid $_p)"
        return 0
    fi
    [ -f "$PIDF" ] || die "no $DIR/$PIDF: nothing recorded to stop"
    _p=$(cat "$PIDF")
    case $_p in '' | *[!0-9]*) die "$DIR/$PIDF does not hold a pid" ;; esac
    if ! alive "$_p"; then
        say "process $_p is not running; $DIR/$PIDF is stale and left alone"
        return 0
    fi
    _args=$(tr '\0' ' ' <"/proc/$_p/cmdline" | tr -d '\n' | safe)
    ours "$_p" || die "pid $_p runs '$_args', not 'nats-server -c $CONF':" \
        "not stopping it"
    say "about to: stop nats-server pid $_p"
    kill "$_p"
    _i=0
    while alive "$_p" && [ "$_i" -lt 50 ]; do sleep 0.2; _i=$((_i + 1)); done
    ! alive "$_p" || die "pid $_p did not exit in 10 s (left running)"
    did "stopped pid $_p"
}

# srv_start: start the server (boot unit or detached) and wait for TLS.
srv_start() {
    _lp=$(conf_listen)
    [ -n "$_lp" ] || die "cannot read the listen address from $CONF"
    _h=$(probe_host "${_lp% *}")
    _pt=${_lp#* }
    if port_open "$_h" "$_pt"; then
        die "port $_h:$_pt is already taken by something else (nothing was" \
            "killed or changed): free it or recreate with another --port"
    fi
    if boot_ours; then
        sysd start "$UNITNAME"
    else
        chkdir
        nohup "$DIR/bin/nats-server" -c "$CONF" </dev/null \
            > >(nohup sed -u -E "/-----BEGIN/,/-----END/d;/$SECRE/d" \
                >>server.log 2>&1) 2>&1 &
        echo $! >|"$PIDF"
    fi
    _i=0
    until port_open "$_h" "$_pt" && tls_required "$_h" "$_pt"; do
        _i=$((_i + 1))
        _p=$(srv_pid)
        if [ -z "$_p" ]; then
            sleep 0.5
            [ ! -f server.log ] || tail -n 10 server.log | safe | fold80 >&2
            die "the server exited (log: $DIR/server.log, filtered)"
        fi
        [ "$_i" -lt 75 ] ||
            die "port $_h:$_pt did not answer with TLS in 15 s" \
                "(log: $DIR/server.log, filtered)"
        sleep 0.2
    done
    did "nats-server pid $(srv_pid) listens on $_h:$_pt and requires TLS" \
        "(log $DIR/server.log, filtered)"
}

# ---------------------------------------------------------- tool handling
sha_of() { _s=$(sha256sum <"$1") && printf '%s' "${_s%% *}"; }
# manifest line: name version archive-sha binary-sha
man_get() { # NAME FIELD(3|4) -> value
    [ -f bin/.manifest ] || return 0
    awk -v n="$1" -v f="$2" '$1 == n { print $f }' bin/.manifest
}
# bin_problems: one line per problem with DIR/bin; returns 1 if any.
bin_problems() {
    _bad=0
    if [ ! -d bin ] || [ -L bin ]; then
        echo "bin is missing or not a directory"
        return 1
    fi
    [ "$(uid_of bin)" = "$ME" ] || { echo "bin is not yours"; _bad=1; }
    [ "$(rawmode bin)" = 700 ] || { echo "bin mode is not 700"; _bad=1; }
    for _e in bin/* bin/.[!.]*; do
        [ -e "$_e" ] || [ -L "$_e" ] || continue
        case ${_e#bin/} in nats-server | nsc | .manifest | .dl) ;;
        *) echo "unexpected ${_e#bin/} in bin (not made by this script)"
            _bad=1 ;;
        esac
    done
    for _t in nats-server nsc; do
        _f=bin/$_t
        if [ ! -e "$_f" ]; then echo "$_t is missing"; _bad=1; continue; fi
        if [ -L "$_f" ] || [ ! -f "$_f" ]; then
            echo "$_t is not a regular file"; _bad=1; continue
        fi
        [ "$(uid_of "$_f")" = "$ME" ] || { echo "$_t is not yours"; _bad=1; }
        [ "$(rawmode "$_f")" = 700 ] || { echo "$_t mode is not 700"; _bad=1; }
        _want=$(man_get "$_t" 4)
        _got=$(sha_of "$_f")
        if [ -z "$_want" ]; then echo "$_t has no manifest entry"; _bad=1
        elif [ "$_got" != "$_want" ]; then
            echo "$_t sha256 $_got differs from the recorded $_want"
            _bad=1
        fi
    done
    return "$_bad"
}
need_bin() {
    _pr=$(bin_problems) || die "DIR/bin is not as this script left it:" \
        "$(printf '%s' "$_pr" | tr '\n' ';'). Nothing was run."
}
# fetch NAME FILE BASE SHA: download to bin/.dl, check the digest in code.
fetch() {
    : >|"$LOG"
    curl -fsSL --proto '=https' --tlsv1.2 "$3/$2" >"bin/.dl/$2" 2>>"$LOG" ||
        capfail "download of $1 from $3/$2"
    _g=$(sha_of "bin/.dl/$2")
    did "downloaded $2 sha256 $_g"
    if [ "$_g" != "$4" ]; then
        rm -f -- "bin/.dl/$2"
        die "$2 has sha256 $_g but this script pins $4: refusing. Nothing" \
            "was extracted or executed."
    fi
    did "$2 matches the pinned sha256"
}
# install_tools create|upgrade
install_tools() {
    [ ! -e bin/.dl ] || die "bin/.dl exists (an earlier run was cut short):" \
        "check it, move it away and run again"
    mkdir -m 0700 bin/.dl
    fetch nats-server "$NS_FILE" "$NS_BASE" "$NS_SHA"
    fetch nsc "$NSC_FILE" "$NSC_BASE" "$NSC_SHA"
    mkdir -m 0700 bin/.dl/x bin/.dl/y
    runlog "tar extract of $NS_FILE" tar -xzf "bin/.dl/$NS_FILE" \
        -C bin/.dl/x --no-same-owner "$NS_MEMBER"
    runlog "unzip of $NSC_FILE" unzip -q -n "bin/.dl/$NSC_FILE" nsc \
        -d bin/.dl/y
    [ -f "bin/.dl/x/$NS_MEMBER" ] || die "$NS_FILE had no $NS_MEMBER"
    [ -f bin/.dl/y/nsc ] || die "$NSC_FILE had no nsc"
    cat "bin/.dl/x/$NS_MEMBER" >bin/.new-nats-server
    cat bin/.dl/y/nsc >bin/.new-nsc
    chmod 0700 bin/.new-nats-server bin/.new-nsc
    chkdir
    for _t in nats-server nsc; do
        case $_t in
        nats-server) _flag=-v; _line=$NS_VERLINE ;;
        *) _flag=--version; _line=$NSC_VERLINE ;;
        esac
        : >|"$LOG"
        _o=$("$DIR/bin/.new-$_t" "$_flag" 2>>"$LOG" | safe) || true
        [ "$_o" = "$_line" ] ||
            die "the extracted $_t reports '$_o', expected '$_line':" \
                "not installing it"
        did "$_t reports '$_o'"
    done
    for _t in nats-server nsc; do
        if [ -e "bin/$_t" ] || [ -L "bin/$_t" ]; then
            [ "$1" = upgrade ] || die "bin/$_t exists: not overwriting"
        fi
        mv -f -- "bin/.new-$_t" "bin/$_t"
    done
    {
        printf 'nats-server %s %s %s\n' "$NS_VER" "$NS_SHA" \
            "$(sha_of bin/nats-server)"
        printf 'nsc %s %s %s\n' "$NSC_VER" "$NSC_SHA" "$(sha_of bin/nsc)"
    } >|bin/.manifest
    rm -f -- "bin/.dl/$NS_FILE" "bin/.dl/$NSC_FILE" bin/.dl/y/nsc \
        "bin/.dl/x/$NS_MEMBER"
    rmdir "bin/.dl/x/${NS_MEMBER%/*}" bin/.dl/x bin/.dl/y bin/.dl
    did "installed nats-server $NS_VER and nsc $NSC_VER in $DIR/bin (mode" \
        "0700; only these two paths are ever run)"
}

# ------------------------------------------------------------------ check
check_run() {
    fails=0
    bad() { wrap '  FAIL  ' '        ' "$*"; fails=$((fails + 1)); }
    good() { wrap '  ok    ' '        ' "$*"; }
    mode_is() { # PATH MODE d|f: exists, right type, not a link, mine, mode
        l=$DIR/$1
        [ "$1" != . ] || l=$DIR
        [ -e "$1" ] || { bad "missing $l"; return; }
        if [ -L "$1" ]; then bad "$l is a symlink"; return; fi
        if [ "$3" = d ] && [ ! -d "$1" ]; then
            bad "$l is not a directory"; return; fi
        if [ "$3" = f ] && [ ! -f "$1" ]; then
            bad "$l is not a regular file"; return; fi
        u=$(uid_of "$1")
        [ "$u" = "$ME" ] || { bad "$l is owned by uid $u, not you"; return; }
        m=$(rawmode "$1")
        [ "${#m}" -le 3 ] || m=${m#?}   # ignore setgid/sticky bits
        if [ "$m" = "$2" ]; then good "$l is yours, mode $m"
        else bad "$l mode $m, want $2"; fi
    }
    say "checking the setup in $DIR (creates nothing)"
    mode_is . 700 d
    mode_is .mash-nats-setup 600 f
    if [ -f .keys-retired ]; then
        good "keys retired: ca.key, nsc/ and export/ are off this machine"
        for f in ca.key nsc export; do
            if [ -e "$f" ] || [ -L "$f" ]; then
                bad "$f is marked retired but is still in $DIR"
            fi
        done
    else
        mode_is export 700 d
        for f in ca.key export/operator.nk export/account.nk \
            export/sys.creds export/mashadmin.creds; do
            mode_is "$f" 600 f
        done
        if [ -d nsc ]; then good "$DIR/nsc exists"
        else bad "missing $DIR/nsc"; fi
    fi
    mode_is server.key 600 f
    for f in ca.pem server.pem server.conf operator.jwt; do
        if [ -f "$f" ]; then good "$DIR/$f exists"
        else bad "missing $DIR/$f"; fi
    done
    for d in resolver jetstream; do
        if [ -d "$d" ]; then good "$DIR/$d exists"
        else bad "missing $DIR/$d"; fi
    done
    if [ -f ca.pem ] && [ -f server.pem ]; then
        if openssl verify -CAfile ca.pem -purpose sslserver server.pem \
            >/dev/null 2>&1; then
            good "server.pem verifies against ca.pem for TLS server use"
        else bad "server.pem does not verify against ca.pem"; fi
    fi
    binok=0
    if pr=$(bin_problems); then
        binok=1
        good "$DIR/bin: nats-server and nsc match their recorded sha256," \
            "yours, mode 700, nothing else there"
        if [ "$(man_get nats-server 3)" = "$NS_SHA" ] &&
            [ "$(man_get nsc 3)" = "$NSC_SHA" ]; then
            good "tool versions are current ($NS_VER, $NSC_VER)"
        else
            say "  note  the tools are older than this script's pins" \
                "($NS_VER, $NSC_VER): --upgrade replaces them"
        fi
    else
        while IFS= read -r l; do bad "$DIR/bin: $l"; done <<<"$pr"
    fi
    if [ -f server.conf ] && [ "$binok" -eq 1 ]; then
        : >|"$LOG"
        if "$DIR/bin/nats-server" -t -c "$CONF" >>"$LOG" 2>&1; then
            good "nats-server -t -c: $(sed -n 1p "$LOG" |
                sed 's/ *(sha256:[0-9a-f]*)//' | safe)"
        else
            bad "nats-server -t -c $CONF: $(safe <"$LOG" | fold80)"
        fi
    fi
    if [ -f server.conf ]; then
        lp=$(conf_listen)
        [ -n "$lp" ] || lp="127.0.0.1 4222"
        h=$(probe_host "${lp% *}")
        p=${lp#* }
        pid=$(srv_pid)
        if [ -n "$pid" ]; then
            good "server pid $pid runs exactly nats-server -c server.conf"
            if port_open "$h" "$p" && tls_required "$h" "$p"; then
                good "$h:$p answers and requires TLS"
            else bad "$h:$p does not answer with tls_required"; fi
            inos=''
            for l in /proc/"$pid"/fd/*; do
                t=$(readlink "$l" 2>/dev/null) || continue
                case $t in 'socket:['*) t=${t#socket:[}; inos="$inos ${t%]}" ;;
                esac
            done
            socks=$(ss -ltneH 2>/dev/null | awk -v want="$inos" '
                BEGIN { n = split(want, a, " ")
                    for (i = 1; i <= n; i++) w[a[i]] = 1 }
                { for (i = 1; i <= NF; i++) if ($i ~ /^ino:/ &&
                    (substr($i, 5) in w)) print $4 }')
            nsock=0
            while IFS= read -r s; do
                [ -n "$s" ] || continue
                nsock=$((nsock + 1))
                case $s in
                *:"$p") good "pid $pid listens on $s (the client port)" ;;
                *) bad "pid $pid also listens on $s: only the client port" \
                    "$p should be open" ;;
                esac
            done <<<"$socks"
            [ "$nsock" -ge 1 ] || bad "pid $pid listens on no TCP socket"
        elif port_open "$h" "$p"; then
            bad "$h:$p answers but the server recorded for this directory" \
                "is not running: something else holds the port"
        else
            wrap '  skip  ' '        ' "nothing listens on $h:$p" \
                "(server not running: port checks skipped)"
        fi
    fi
    [ "$fails" -eq 0 ] || return 1
    say "check passed"
}

# ------------------------------------------------------------------ modes
need_marker() {
    [ -f .mash-nats-setup ] && [ ! -L .mash-nats-setup ] &&
        [ "$(uid_of .mash-nats-setup)" = "$ME" ] && return 0
    _found=$(find . -mindepth 1 -maxdepth 1 -printf '%f ' | cut -c1-200)
    die "$DIR has no .mash-nats-setup marker owned by you, so it is not a" \
        "directory this script made. It holds: ${_found:-nothing}." \
        "Refusing."
}

case $MODE in
# -------------------------------------------------------------- --stop
stop)
    enter "$DIR"; CWDID=$(inode_of .); mklog; need_marker
    srv_stop
    exit 0 ;;
# ------------------------------------------------------------- --check
check)
    enter "$DIR"; CWDID=$(inode_of .); mklog
    check_run || die "check: $fails problem(s)"
    exit 0 ;;
# ------------------------------------------------------------- --start
start)
    enter "$DIR"; CWDID=$(inode_of .); mklog; need_marker; need_bin
    [ -f server.conf ] || die "no $CONF: nothing to start"
    pid=$(srv_pid)
    [ -z "$pid" ] || die "a server for this directory is already running" \
        "(pid $pid)"
    srv_start
    exit 0 ;;
# ----------------------------------------------------------- --upgrade
upgrade)
    enter "$DIR"; CWDID=$(inode_of .); mklog; need_marker; need_bin
    if [ "$(man_get nats-server 3)" = "$NS_SHA" ] &&
        [ "$(man_get nsc 3)" = "$NSC_SHA" ]; then
        say "already current: nats-server $NS_VER and nsc $NSC_VER"
        check_run || die "check: $fails problem(s)"
        exit 0
    fi
    step "upgrade nats-server and nsc in $DIR/bin to $NS_VER and $NSC_VER"
    was=$(srv_pid)
    if [ -n "$was" ]; then srv_stop; fi
    install_tools upgrade
    if [ -n "$was" ]; then srv_start; fi
    check_run || die "check: $fails problem(s)"
    exit 0 ;;
# ------------------------------------------------------------- --backup
backup | retire-keys)
    split_dest
    enter "$DIR"; CWDID=$(inode_of .); mklog; need_marker
    if [ "$MODE" = retire-keys ]; then
        [ ! -f .keys-retired ] || die "keys were already retired"
        for f in ca.key nsc export; do
            [ -e "$f" ] || die "$DIR/$f is missing: nothing consistent to" \
                "retire (run --check)"
        done
        need_terminal "--retire-keys"
        printf '\n'
        wrap '' '' "--retire-keys writes ca.key, nsc/ and export/ from $DIR" \
            "into $DEST (one tar.gz, mode 0600), checks it, then REMOVES" \
            "those three from $DIR. After that the server still runs, but" \
            "this machine can no longer sign new certificates, accounts or" \
            "users. Do this only after mashctl adopt succeeded. The" \
            "archive holds secrets: move it off this machine."
        confirm "$DIR"
    fi
    was=$(srv_pid)
    if [ "$MODE" = backup ] && [ -n "$was" ]; then srv_stop; fi
    created=0
    ( cd -P -- "$DESTPARENT" && [ "$(pwd -P)" = "$DESTPARENT" ] &&
        : >"$DESTNAME" ) 2>/dev/null ||
        die "could not create $DEST exclusively"
    created=1
    case $MODE in
    backup) members=(--exclude=./bin '--exclude=./.cmd.*' .) ;;
    *) members=(ca.key nsc export) ;;
    esac
    : >|"$LOG"
    if ! ( cd -P -- "$DESTPARENT" && [ "$(pwd -P)" = "$DESTPARENT" ] &&
        tar -czf "$DESTNAME" -C "/proc/$MAINPID/cwd" "${members[@]}"
        ) >>"$LOG" 2>&1; then
        [ "$created" -eq 0 ] || rm -f -- "$DEST"
        capfail "tar"
    fi
    n=$( (cd -P -- "$DESTPARENT" && tar -tzf "$DESTNAME" 2>>"$LOG") |
        grep -c '') || n=0
    if [ "$n" -lt 2 ]; then
        rm -f -- "$DEST"
        die "the archive $DEST did not list back: removed it"
    fi
    if [ "$MODE" = backup ] &&
        (cd -P -- "$DESTPARENT" && tar -tzf "$DESTNAME") 2>/dev/null |
        grep -q '^\./bin/'; then
        rm -f -- "$DEST"
        die "the archive contained bin/: removed it"
    fi
    did "wrote $DEST (mode $(rawmode "$DEST"), $n entries, lists back)"
    if [ "$MODE" = backup ]; then
        if [ -n "$was" ]; then srv_start; fi
        say "this archive holds secrets (CA key, nsc keys, creds): keep it" \
            "private and off this machine"
    else
        chkdir
        rm -r -- ca.key nsc export
        printf 'retired to %s\n' "$DEST" >.keys-retired
        did "removed $DIR/ca.key, $DIR/nsc and $DIR/export; wrote" \
            "$DIR/.keys-retired"
        say "the server needs only server.pem, server.key, ca.pem," \
            "operator.jwt, resolver/, jetstream/ and server.conf, so it" \
            "keeps running. The archive holds secrets: move it off this" \
            "machine and keep it safe."
    fi
    exit 0 ;;
# ---------------------------------------------------------- boot units
enable-boot | disable-boot)
    enter "$DIR"; CWDID=$(inode_of .); mklog; need_marker
    need_terminal "--$MODE"
    command -v systemctl >/dev/null 2>&1 ||
        die "systemctl is not available: no systemd here"
    runlog "reaching your systemd user manager (systemctl --user)" \
        systemctl --user show-environment
    U=$(uidline)
    if [ "$MODE" = enable-boot ]; then
        need_bin
        if [ -e "$UNITPATH" ] || [ -L "$UNITPATH" ]; then
            boot_ours || die "$UNITPATH exists and this script did not" \
                "write it: not touching it"
        fi
        printf '\n'
        wrap '' '' "--enable-boot will: write $UNITPATH (a systemd user" \
            "unit that runs $DIR/bin/nats-server -c $CONF, restarting it" \
            "on failure); run systemctl --user daemon-reload and" \
            "systemctl --user enable --now $UNITNAME; stop the server this" \
            "script started, if any, because systemd takes over."
        if [ "$LINGER" -eq 1 ]; then
            wrap '' '' "--linger will ALSO run loginctl enable-linger $U." \
                "That changes your account on this machine: your user" \
                "services start when the machine boots and keep running when" \
                "you log out. It stays on until you run loginctl" \
                "disable-linger $U (this script never turns it off)."
        else
            wrap '' '' "Without --linger the unit starts when you log in" \
                "and stops when your last session ends."
        fi
        confirm "$UNITNAME"
        was=$(srv_pid)
        if [ -n "$was" ] && ! boot_ours; then srv_stop; fi
        if ! boot_ours; then
            [ -d "$UNITDIR" ] || { chain_ok "${HOME}"
                mkdir -p -- "$UNITDIR"; }
            {
                printf '%s\n' "$UNITMARK" '[Unit]' \
                    "Description=mash NATS server for $DIR" \
                    'After=network-online.target' '' '[Service]' \
                    "ExecStart=\"$DIR/bin/nats-server\" -c \"$CONF\"" \
                    'Restart=on-failure' 'RestartSec=2' \
                    'NoNewPrivileges=yes' '' '[Install]' \
                    'WantedBy=default.target'
            } >"$UNITPATH"
            did "wrote $UNITPATH"
        fi
        sysd daemon-reload
        sysd enable --now "$UNITNAME"
        did "enabled and started $UNITNAME"
        if [ "$LINGER" -eq 1 ]; then
            runlog "loginctl enable-linger" loginctl enable-linger "$U"
            did "ran loginctl enable-linger $U (undo: loginctl" \
                "disable-linger $U)"
        fi
        _lp=$(conf_listen)
        _i=0
        until port_open "$(probe_host "${_lp% *}")" "${_lp#* }"; do
            _i=$((_i + 1))
            [ "$_i" -lt 75 ] || die "the server did not answer in 15 s: see" \
                "systemctl --user status $UNITNAME"
            sleep 0.2
        done
        did "the server answers on ${_lp% *}:${_lp#* }"
    else
        if [ ! -e "$UNITPATH" ] && [ ! -L "$UNITPATH" ]; then
            say "no boot unit $UNITNAME for this directory: nothing to do"
            exit 0
        fi
        boot_ours || die "$UNITPATH exists and this script did not write" \
            "it: not touching it"
        printf '\n'
        wrap '' '' "--disable-boot will run systemctl --user disable --now" \
            "$UNITNAME (this STOPS the server) and remove $UNITPATH. It does" \
            "not change linger."
        confirm "$UNITNAME"
        sysd disable --now "$UNITNAME"
        rm -f -- "$UNITPATH"
        sysd daemon-reload
        did "disabled $UNITNAME and removed $UNITPATH"
        say "linger was not touched. If you turned it on and want it off:" \
            "loginctl disable-linger $U"
        say "start the server again by hand with --start"
    fi
    exit 0 ;;
# ------------------------------------------------------------- --remove
remove)
    chain_ok "$DIR"
    cd -P -- "$DIR" || die "cannot enter $DIR"
    CWDID=$(inode_of .)
    need_marker
    need_terminal "--remove"
    printf '\n'
    wrap '' '' "--remove will stop the server, remove the boot unit this" \
        "script wrote (if any) and DELETE $DIR with everything in it (CA" \
        "key, nsc keys, creds, mail stored in JetStream). Back up first if" \
        "you need any of it."
    confirm "$DIR"
    mklog
    if [ -n "$(srv_pid)" ]; then srv_stop; fi
    if boot_ours; then
        sysd disable --now "$UNITNAME"
        rm -f -- "$UNITPATH"
        sysd daemon-reload
        did "disabled $UNITNAME and removed $UNITPATH"
    fi
    rm -f -- "$LOG"
    LOG=''
    chkdir
    cd -P -- .. || die "cannot leave $DIR"
    [ "$(pwd -P)" = "${DIR%/*}" ] && [ "$(inode_of "${DIR##*/}")" = \
        "$CWDID" ] || die "$DIR is not where this run found it: not removing"
    rm -r -- "${DIR##*/}"
    did "removed $DIR"
    say "mashctl's folder was not touched. If you are done with mash," \
        "this removes it (it holds your keys and agents' creds):"
    emit_cmd '  ' rm -r -- "$(Q "$MASHCFG")"
    exit 0 ;;
esac

# ---------------------------------------------------------------- create
CONFLISTEN=$LISTEN
case $LISTEN in \[*) ;; *:*) CONFLISTEN="[$LISTEN]" ;; esac
SANS='DNS:localhost,IP:127.0.0.1,IP:::1'
FIRSTHOST=localhost
for h in $HOSTS; do
    case $h in
    localhost | 127.0.0.1 | ::1) ;; # already in the list
    *:* | [0-9]*.[0-9]*.[0-9]*.[0-9]*) SANS="$SANS,IP:$h" ;;
    *) SANS="$SANS,DNS:$h" ;;
    esac
    [ "$FIRSTHOST" != localhost ] || FIRSTHOST=$h
done
if [ "$LOOPBACK" -eq 0 ]; then
    need_terminal "a non-loopback --listen"
    printf '\n'
    wrap '' '' "EXPOSED: the server will listen on $LISTEN:$PORT, which" \
        "other machines may be able to reach. TLS is required and clients" \
        "need NATS credentials. The certificate will name: ${SANS//,/ }." \
        "This script changes no firewall and no service."
    confirm "$LISTEN:$PORT"
fi

step "preflight (directory, port)"
PARENT=${DIR%/*}
NAME=${DIR##*/}
chain_ok "$PARENT"
[ ! -e "$DIR" ] && [ ! -L "$DIR" ] ||
    die "$DIR already exists: this script creates --dir itself and" \
        "refuses any existing path (even an empty directory)"
ph=$(probe_host "$LISTEN")
if port_open "$ph" "$PORT"; then
    die "port $ph:$PORT is already taken by something (nothing was killed" \
        "or changed): use --port"
fi
cd -P -- "$PARENT" || die "cannot enter $PARENT"
[ "$(pwd -P)" = "$PARENT" ] || die "$PARENT is not a plain path"
mkdir -m 0700 -- "$NAME" || die "could not create $DIR (it must not exist)"
NID=$(inode_of "$NAME")
fail_new() {
    cd -P -- .. 2>/dev/null && rmdir -- "$NAME" 2>/dev/null || true
    die "$1"
}
{ [ -d "$NAME" ] && [ ! -L "$NAME" ]; } ||
    fail_new "$NAME is not a plain directory"
[ "$(uid_of "$NAME")" = "$ME" ] || fail_new "$DIR is not owned by you"
[ "$(rawmode "$NAME")" = 700 ] || fail_new "$DIR is not mode 700"
cd -- "$NAME" || fail_new "cannot enter $DIR"
CWDID=$(inode_of .)
[ "$CWDID" = "$NID" ] || fail_new "$DIR changed while it was made"
{ [ "$(pwd -P)" = "$DIR" ] && [ "$(inode_of "$DIR")" = "$CWDID" ]; } ||
    fail_new "$PARENT was replaced while $DIR was made: it was not used" \
        "and the new directory was removed"
printf 'mash-nats-setup 1 created by uid %s\n' "$ME" >.mash-nats-setup
mklog
did "$DIR created by this script (mode 0700, yours); $ph:$PORT is free"
[ "$LOOPBACK" -eq 1 ] || did "EXPOSED: $LISTEN:$PORT, typed confirmation" \
    "given"

step "install nats-server and nsc into $DIR/bin (pinned, checked)"
mkdir -m 0700 bin
if cmd=$(command -v nsc 2>/dev/null); then
    say "(a copy of nsc on PATH at $cmd is never run)"
fi
if cmd=$(command -v nats-server 2>/dev/null); then
    say "(a copy of nats-server on PATH at $cmd is never run)"
fi
install_tools create
NSC=$DIR/bin/nsc
NATSD=$DIR/bin/nats-server

step "make a private CA and a server certificate (ECDSA P-256, 10 years)"
runlog "openssl (CA)" openssl req -x509 -newkey ec \
    -pkeyopt ec_paramgen_curve:P-256 -nodes -days 3650 \
    -keyout ca.key -out ca.pem -subj '/CN=mash local CA' \
    -addext 'basicConstraints=critical,CA:TRUE' \
    -addext 'keyUsage=critical,keyCertSign,cRLSign'
: >|"$LOG"
openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
    -keyout server.key -subj '/CN=mash local server' 2>>"$LOG" |
    openssl x509 -req -in /dev/stdin -CA ca.pem -CAkey ca.key \
        -days 3650 -set_serial "0x$(openssl rand -hex 8)" \
        -extfile <(printf '%s\n' 'basicConstraints=CA:FALSE' \
            'keyUsage=critical,digitalSignature' \
            'extendedKeyUsage=serverAuth' "subjectAltName=$SANS") \
        -out server.pem >>"$LOG" 2>&1 ||
    capfail "openssl (server certificate)"
runlog "openssl verify" openssl verify -CAfile ca.pem -purpose sslserver \
    server.pem
did "ca.pem, ca.key, server.pem, server.key written; SANs: $SANS"

step "make the operator, accounts and users with nsc, in an isolated store" \
    "under $DIR/nsc"
mkdir -m 0700 nsc nsc/data nsc/config nsc/keys nsc/home
# Relative on purpose: everything nsc writes lands under the directory this
# script is working in, not wherever an absolute string might lead later.
export XDG_DATA_HOME=nsc/data XDG_CONFIG_HOME=nsc/config
export NKEYS_PATH=nsc/keys NSC_HOME=nsc/home NATS_CONFIG_HOME=nsc/config
q() { # run nsc quietly
    chkdir
    : >|"$LOG"
    "$NSC" "$@" >>"$LOG" 2>&1 || capfail "nsc $1 $2"
}
f() { # a --field value without quotes
    chkdir
    : >|"$LOG"
    _v=$("$NSC" "$@" 2>>"$LOG") || capfail "nsc $1 $2"
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
did "users: mashadmin in $ACCOUNT, sys in SYS; all keys are in $DIR/nsc/keys"

step "write the server configuration and test it"
mkdir -m 0700 resolver jetstream
: >|"$LOG"
"$NSC" describe operator --raw >operator.jwt 2>>"$LOG" ||
    capfail "nsc describe operator"
ACCJWT=$("$NSC" describe account "$ACCOUNT" --raw 2>>"$LOG") ||
    capfail "nsc describe account"
SYSJWT=$("$NSC" describe account SYS --raw 2>>"$LOG") ||
    capfail "nsc describe account"
cat >server.conf <<EOF
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
operator: "$DIR/operator.jwt"
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
chkdir
runlog "nats-server -t -c" "$NATSD" -t -c "$CONF"
did "$CONF written (self-contained; operator JWT in $DIR/operator.jwt) and" \
    "'nats-server -t -c' passes"

step "export the files mashctl adopt takes into $DIR/export (mode 0600)"
mkdir -m 0700 export
seed_of() { # PUBLIC-KEY: print the seed file of a key nsc made
    p=$(find nsc/keys/keys -name "$1.nk" | sed -n 1p)
    [ -n "$p" ] || die "no seed for $1 under $DIR/nsc/keys"
    printf '%s' "$p"
}
cat "$(seed_of "$OPPUB")" >export/operator.nk
cat "$(seed_of "$ACCPUB")" >export/account.nk
chkdir
: >|"$LOG"
"$NSC" generate creds -a SYS -n sys >export/sys.creds 2>>"$LOG" ||
    capfail "nsc generate creds"
"$NSC" generate creds -a "$ACCOUNT" -n mashadmin >export/mashadmin.creds \
    2>>"$LOG" || capfail "nsc generate creds"
case $(cut -c1-2 export/operator.nk) in
SO) ;; *) die "operator.nk is not an SO seed" ;;
esac
case $(cut -c1-2 export/account.nk) in
SA) ;; *) die "account.nk is not an SA seed" ;;
esac
did "operator.nk, account.nk, sys.creds, mashadmin.creds written (contents" \
    "not shown)"

if [ "$START" -eq 1 ]; then
    step "start nats-server detached"
    srv_start
fi

URLHOST=$FIRSTHOST
case $URLHOST in *:*) URLHOST="[$URLHOST]" ;; esac
file() { printf '  %-24s %s\n' "$1" "$2"; }
QDIR=$(Q "$DIR")
SERVERURL=tls://$URLHOST:$PORT

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
printf '\nFiles (all under %s):\n' "$QDIR"
file bin/nats-server "server, $NS_VER (sha256 checked)"
file bin/nsc "nsc, $NSC_VER (sha256 checked)"
file ca.pem 'CA certificate: give this to clients'
file server.conf 'nats-server configuration (no seeds)'
file export/mashadmin.creds 'admin credentials (secret)'
file export/sys.creds 'system account credentials (secret)'
file export/operator.nk 'operator seed (secret)'
file export/account.nk 'account seed (secret)'
file ca.key 'CA private key (secret)'
file nsc/ 'nsc store with all signing keys (secret)'
if [ "$START" -eq 1 ]; then
    file server.log 'server log (secret-looking lines withheld)'
    file server.pid 'server process id (--stop reads it)'
fi

printf '\nWhat you hold\n'
wrap '  Server address     ' '                     ' "$SERVERURL"
wrap '  CA file            ' '                     ' "$DIR/ca.pem"
wrap '  Server directory   ' '                     ' "$DIR"
wrap '                     ' '                     ' "(mode 0700; holds the" \
    "CA key and the nsc keys until you run --retire-keys)"

printf '\nNext\n'
if [ "$START" -ne 1 ]; then
    printf '  Start the server first (you did not pass --start):\n\n'
    OWNIND='    ' own_cmd --dir "$DIR" --start
    printf '\n'
fi
CFGENV=()
[ "$MASHCFG_GIVEN" -eq 0 ] || CFGENV=("MASH_CONFIG_DIR=$(Q "$MASHCFG")")
# lines GROUP...: each group on its own line, joined with " \".
lines() {
    _n=$#
    for _g in "$@"; do
        _n=$((_n - 1))
        if [ "$_n" -gt 0 ]; then printf '    %s \\\n' "$_g"
        else printf '    %s\n' "$_g"; fi
    done
}
printf '  1. Adopt the server into mash. One command; run it as it is:\n\n'
lines "${CFGENV[@]}" "MASH_TLS_CA=$(Q "$DIR/ca.pem") mashctl adopt" \
    "--server $(Q "$SERVERURL")" "--owner $(Q "$OWNER")" \
    "--account $(Q "$ACCOUNT")" \
    "--operator-key $(Q "$DIR/export/operator.nk")" \
    "--account-key $(Q "$DIR/export/account.nk")" \
    "--sys-creds $(Q "$DIR/export/sys.creds")" \
    "--admin-creds $(Q "$DIR/export/mashadmin.creds")"
printf '\n  2. Create the directory and lease buckets:\n\n'
if [ "${#CFGENV[@]}" -gt 0 ]; then
    printf '    %s mashctl init\n' "${CFGENV[0]}"
else
    printf '    mashctl init\n'
fi
printf '\n'
wrap '  ' '  ' "mashctl keeps its files in $MASHCFG (set MASH_CONFIG_DIR to" \
    "change that). Agents' credentials are written there as" \
    "$OWNER.<group>.<agent>.creds."
printf '\n'
wrap '  ' '  ' "To use mash in Claude Code, run these two lines in Claude" \
    "Code, then see the guide:"
printf '\n    claude plugin marketplace add bogworks/mash\n'
printf '    claude plugin install mash@mash\n\n'
wrap '  ' '  ' "https://github.com/bogworks/mash/blob/v0.5.6/docs/install.md"
printf '\n  3. Looking after the server later (each takes --dir):\n'
BK=$HOME/mash-nats-backup.tar.gz
RK=$HOME/mash-nats-keys.tar.gz
OWNIND='    '
lc() { printf '\n  %s\n' "$1"; shift; own_cmd "$@"; }
lc 'start the server' --dir "$DIR" --start
lc 'stop it' --dir "$DIR" --stop
lc 'check it' --dir "$DIR" --check
lc 'back it up (the archive holds secrets)' --dir "$DIR" --backup "$BK"
lc 'after adopt, move the offline-only secrets off this machine' \
    --dir "$DIR" --retire-keys "$RK"
lc 'upgrade nats-server and nsc to the pinned versions' --dir "$DIR" --upgrade
lc 'start it at boot (systemd user unit)' --dir "$DIR" --enable-boot
lc 'stop starting it at boot' --dir "$DIR" --disable-boot
lc 'remove it all' --dir "$DIR" --remove
