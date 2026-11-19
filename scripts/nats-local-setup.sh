#!/usr/bin/env bash
# nats-local-setup.sh: stand up a local operator-mode NATS server for mash with only nsc, openssl
# and nats-server. It does not use mashctl; it ends by printing the `mashctl adopt` command to run.
#
# Everything goes under --dir (mode 0700): a private CA and server certificate (ca.*, server.*),
# an isolated nsc store (nsc/, nothing in ~/.nsc or ~/.local/share/nats), the server configuration
# (server.conf, resolver/, jetstream/), and the files adopt takes (export/).
#
# Usage: nats-local-setup.sh --dir DIR [--port N] [--host NAME]... [--listen ADDR]
#        [--account NAME] [--operator NAME] [--js-disk SIZE] [--start]
#        | --dir DIR --check | --dir DIR --stop | --help
set -eu
umask 077

usage() {
	cat <<'EOF'
usage: nats-local-setup.sh --dir DIR [options]      create a local operator-mode NATS setup
       nats-local-setup.sh --dir DIR --check        verify an existing setup (creates nothing)
       nats-local-setup.sh --dir DIR --stop         stop the server recorded in DIR/server.pid

  --dir DIR        required; must be absent or empty, at least 3 path components deep
  --port N         client port (default 4222)
  --host NAME      repeatable; extra DNS name or IP for the server certificate
                   (localhost, 127.0.0.1 and ::1 are always included)
  --listen ADDR    listen address (default 127.0.0.1)
  --account NAME   account for mash (default MASH)
  --operator NAME  operator name (default mash-operator)
  --js-disk SIZE   JetStream disk for the account (default 2G; 64 streams, 128 consumers)
  --start          start nats-server detached (pid DIR/server.pid, log DIR/server.log)
  --check          do not create anything: verify files, modes, config, and the TLS port if running
  --stop           stop the server in DIR/server.pid (only if it is
                   `nats-server -c DIR/server.conf`)
  --help           this text
EOF
}

die() { printf 'nats-local-setup: %s\n' "$*" >&2; exit 1; }
say() { printf '%s\n' "$*"; }
step() { N=$((N + 1)); printf '\n[%s] about to: %s\n' "$N" "$*"; }
did() { printf '    did: %s\n' "$*"; }

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

need_val() { [ "$#" -ge 2 ] || die "$1 needs a value"; }
while [ "$#" -gt 0 ]; do
	case $1 in
	--dir) need_val "$@"; DIR=$2; shift 2 ;;
	--port) need_val "$@"; PORT=$2; shift 2 ;;
	--host) need_val "$@"; HOSTS="$HOSTS $2"; shift 2 ;;
	--listen) need_val "$@"; LISTEN=$2; shift 2 ;;
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

[ -n "$DIR" ] || { usage >&2; die "--dir is required (there is no default location)"; }
case $DIR in /*) ;; *) DIR=$PWD/$DIR ;; esac
case "/$DIR/" in */../* | */./*) die "--dir must not contain . or .. components: $DIR" ;; esac
while :; do # squeeze // and trailing /
	case $DIR in *//*) DIR=${DIR%%//*}/${DIR#*//} ;; *) break ;; esac
done
DIR=${DIR%/}
[ -n "$DIR" ] || die "refusing '/' as --dir"
ncomp=$(printf '%s' "$DIR" | tr -s '/' '\n' | grep -c .) || true
[ "$ncomp" -ge 3 ] || die "refusing --dir $DIR: needs at least 3 path components"
case $PORT in '' | *[!0-9]*) die "--port must be a number" ;; esac
[ "$PORT" -ge 1 ] && [ "$PORT" -le 65535 ] || die "--port out of range: $PORT"
case $ACCOUNT$OPERATOR in
*[!A-Za-z0-9_-]*) die "--account and --operator: letters, digits, _ and - only" ;;
esac
[ "$((CHECK + STOP))" -le 1 ] || die "--check and --stop are alternatives"
[ "$((CHECK + STOP + START))" -le 1 ] || die "--start applies to create mode only"

CONF=$DIR/server.conf
PIDF=$DIR/server.pid

# probe_host: an address a client can use for a listen address.
probe_host() {
	case $1 in 0.0.0.0 | '' | '[::]' | '::') printf '127.0.0.1' ;; *) printf '%s' "$1" ;; esac
}
# port_open HOST PORT: succeeds if something accepts a TCP connection there (bash /dev/tcp).
port_open() { (exec 3<>"/dev/tcp/$1/$2") 2>/dev/null; }
# tls_required HOST PORT: the NATS INFO line announces that TLS is required.
tls_required() {
	exec 3<>"/dev/tcp/$1/$2" || return 1
	line=''
	read -r -t 5 line <&3 || true
	exec 3<&- 3>&-
	case $line in INFO*'"tls_required":true'*) return 0 ;; *) return 1 ;; esac
}
# alive PID: running (a zombie that nobody has reaped yet counts as gone).
alive() {
	st=$(ps -p "$1" -o stat= 2>/dev/null) || return 1
	case $st in '' | Z*) return 1 ;; *) return 0 ;; esac
}
conf_listen() { sed -n 's/^listen: *"\(.*\):\([0-9]*\)"$/\1 \2/p' "$CONF" | head -n 1; }

# ---------------------------------------------------------------- --stop
if [ "$STOP" -eq 1 ]; then
	[ -f "$PIDF" ] || die "no $PIDF: nothing recorded to stop"
	pid=$(cat "$PIDF")
	case $pid in '' | *[!0-9]*) die "$PIDF does not hold a pid" ;; esac
	alive "$pid" || { say "process $pid is not running; $PIDF is stale and left alone"; exit 0; }
	args=$(ps -p "$pid" -o args= 2>/dev/null || true)
	[ -n "$args" ] || { say "process $pid is not running; $PIDF is stale and left alone"; exit 0; }
	want="nats-server -c $CONF"
	[ "$args" = "$want" ] || die "pid $pid runs '$args', not '$want': not stopping it"
	say "about to: stop nats-server pid $pid ($args)"
	kill "$pid"
	i=0
	while alive "$pid" && [ "$i" -lt 50 ]; do sleep 0.2; i=$((i + 1)); done
	if alive "$pid"; then die "pid $pid did not exit within 10 s (left running)"; fi
	did "stopped pid $pid"
	exit 0
fi

# ---------------------------------------------------------------- --check
if [ "$CHECK" -eq 1 ]; then
	fails=0
	bad() { printf '  FAIL  %s\n' "$*"; fails=$((fails + 1)); }
	good() { printf '  ok    %s\n' "$*"; }
	mode_is() { # FILE MODE
		[ -e "$1" ] || { bad "missing $1"; return; }
		m=$(stat -c %a "$1" 2>/dev/null || stat -f %Lp "$1")
		if [ "$m" = "$2" ]; then good "$1 mode $m"; else bad "$1 mode $m, want $2"; fi
	}
	say "checking the setup in $DIR (creates nothing)"
	mode_is "$DIR" 700
	for f in ca.key server.key export/operator.nk export/account.nk export/sys.creds \
		export/mashadmin.creds; do mode_is "$DIR/$f" 600; done
	for f in ca.pem server.pem server.conf operator.jwt; do
		if [ -f "$DIR/$f" ]; then good "$DIR/$f exists"; else bad "missing $DIR/$f"; fi
	done
	for d in resolver jetstream nsc; do
		if [ -d "$DIR/$d" ]; then good "$DIR/$d exists"; else bad "missing $DIR/$d"; fi
	done
	if [ -f "$DIR/ca.pem" ] && [ -f "$DIR/server.pem" ]; then
		if openssl verify -CAfile "$DIR/ca.pem" -purpose sslserver "$DIR/server.pem" >/dev/null 2>&1; then
			good "server.pem verifies against ca.pem for TLS server use"
		else bad "server.pem does not verify against ca.pem"; fi
	fi
	if [ -f "$CONF" ]; then
		if out=$(nats-server -t -c "$CONF" 2>&1); then good "nats-server -t -c: $out"
		else bad "nats-server -t -c $CONF: $out"; fi
		lp=$(conf_listen)
		[ -n "$lp" ] || lp="127.0.0.1 4222"
		h=$(probe_host "${lp% *}")
		p=${lp#* }
		if port_open "$h" "$p"; then
			if tls_required "$h" "$p"; then good "$h:$p answers and requires TLS"
			else bad "$h:$p answers but does not announce tls_required"; fi
		else
			say "  skip  nothing listens on $h:$p (server not running: port check skipped)"
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
did "$(nsc --version 2>&1 | tail -n 1); $(openssl version | cut -d" " -f1-2); $(nats-server -v)"
if [ -e "$DIR" ]; then
	[ -d "$DIR" ] || die "$DIR exists and is not a directory"
	[ -z "$(ls -A "$DIR")" ] || die "$DIR is not empty: use an absent or empty directory"
fi
ph=$(probe_host "$LISTEN")
if port_open "$ph" "$PORT"; then
	die "port $ph:$PORT is already taken by something (nothing was killed or changed): use --port"
fi
mkdir -p "$DIR"
chmod 700 "$DIR"
did "tools found; $DIR is usable (mode 0700); $ph:$PORT is free"

step "make a private CA and a server certificate (ECDSA P-256, 10 years)"
SANS='DNS:localhost,IP:127.0.0.1,IP:::1'
FIRSTHOST=localhost
for h in $HOSTS; do
	case $h in
	*[!A-Za-z0-9.:-]*) die "--host $h: use a DNS name or an IP address" ;;
	*:* | [0-9]*.[0-9]*.[0-9]*.[0-9]*) SANS="$SANS,IP:$h" ;;
	*) SANS="$SANS,DNS:$h" ;;
	esac
	[ "$FIRSTHOST" != localhost ] || FIRSTHOST=$h
done
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 3650 \
	-keyout "$DIR/ca.key" -out "$DIR/ca.pem" -subj '/CN=mash local CA' \
	-addext 'basicConstraints=critical,CA:TRUE' \
	-addext 'keyUsage=critical,keyCertSign,cRLSign' 2>/dev/null
openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
	-keyout "$DIR/server.key" -subj '/CN=mash local server' 2>/dev/null |
	openssl x509 -req -in /dev/stdin -CA "$DIR/ca.pem" -CAkey "$DIR/ca.key" -days 3650 \
		-set_serial "0x$(openssl rand -hex 8)" \
		-extfile <(printf 'basicConstraints=CA:FALSE\nkeyUsage=critical,digitalSignature\n%s\n%s\n' \
			'extendedKeyUsage=serverAuth' "subjectAltName=$SANS") \
		-out "$DIR/server.pem" 2>/dev/null
chmod 600 "$DIR/ca.key" "$DIR/server.key"
chmod 644 "$DIR/ca.pem" "$DIR/server.pem"
openssl verify -CAfile "$DIR/ca.pem" -purpose sslserver "$DIR/server.pem" >/dev/null
did "ca.pem, ca.key, server.pem, server.key written; SANs: $SANS"

step "make the operator, accounts and users with nsc, in an isolated store under $DIR/nsc"
NSCD=$DIR/nsc
mkdir -p "$NSCD/data" "$NSCD/config" "$NSCD/keys" "$NSCD/home"
export XDG_DATA_HOME=$NSCD/data XDG_CONFIG_HOME=$NSCD/config NKEYS_PATH=$NSCD/keys
export NSC_HOME=$NSCD/home NATS_CONFIG_HOME=$NSCD/config
NSCLOG=$NSCD/nsc.log
q() { # run nsc quietly; show the tail of its log if it fails
	if ! nsc "$@" >>"$NSCLOG" 2>&1; then
		tail -n 15 "$NSCLOG" >&2
		die "nsc $* failed (log: $NSCLOG)"
	fi
}
f() { nsc "$@" 2>>"$NSCLOG" | tr -d '"' ; } # a --field value without its quotes
q add operator "$OPERATOR" --sys
q add account "$ACCOUNT"
q edit account "$ACCOUNT" --js-disk-storage "$JSDISK" --js-streams 64 --js-consumer 128
q add user -a "$ACCOUNT" mashadmin
q describe operator
OPPUB=$(f describe operator --field sub)
ACCPUB=$(f describe account "$ACCOUNT" --field sub)
ACCISS=$(f describe account "$ACCOUNT" --field iss)
SYSPUB=$(f describe operator --field nats.system_account)
[ "$ACCISS" = "$OPPUB" ] || die "account JWT was issued by $ACCISS, not the operator key $OPPUB"
did "operator $OPERATOR ($OPPUB), system account SYS, account $ACCOUNT ($ACCPUB)"
did "JetStream on $ACCOUNT: disk $JSDISK, 64 streams, 128 consumers (memory storage unset)"
did "users: mashadmin in $ACCOUNT, sys in SYS; all keys are in $NKEYS_PATH"

step "write the server configuration and test it"
mkdir -p "$DIR/resolver" "$DIR/jetstream"
nsc describe operator --raw >"$DIR/operator.jwt" 2>>"$NSCLOG"
OPJWT=$DIR/operator.jwt
ACCJWT=$(nsc describe account "$ACCOUNT" --raw 2>>"$NSCLOG")
SYSJWT=$(nsc describe account SYS --raw 2>>"$NSCLOG")
cat >"$CONF" <<EOF
# written by nats-local-setup.sh: operator mode, full resolver, TLS required, JetStream on.
server_name: "mash-local"
listen: "$LISTEN:$PORT"
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
chmod 600 "$CONF"
if ! nats-server -t -c "$CONF" >>"$NSCLOG" 2>&1; then
	tail -n 10 "$NSCLOG" >&2
	die "nats-server -t -c failed"
fi
did "$CONF written (self-contained; operator JWT in $OPJWT) and 'nats-server -t -c' passes"

step "export the files mashctl adopt takes into $DIR/export (mode 0600)"
EXP=$DIR/export
mkdir -p "$EXP"
seed_of() { # PUBLIC-KEY: print the seed file of a key nsc made
	p=$(find "$NKEYS_PATH/keys" -name "$1.nk" | head -n 1)
	[ -n "$p" ] || die "no seed for $1 under $NKEYS_PATH"
	printf '%s' "$p"
}
cat "$(seed_of "$OPPUB")" >"$EXP/operator.nk"
cat "$(seed_of "$ACCPUB")" >"$EXP/account.nk"
nsc generate creds -a SYS -n sys >"$EXP/sys.creds" 2>>"$NSCLOG"
nsc generate creds -a "$ACCOUNT" -n mashadmin >"$EXP/mashadmin.creds" 2>>"$NSCLOG"
chmod 600 "$EXP"/operator.nk "$EXP"/account.nk "$EXP"/sys.creds "$EXP"/mashadmin.creds
case $(cut -c1-2 "$EXP/operator.nk") in SO) ;; *) die "operator.nk is not an SO seed" ;; esac
case $(cut -c1-2 "$EXP/account.nk") in SA) ;; *) die "account.nk is not an SA seed" ;; esac
did "operator.nk, account.nk, sys.creds, mashadmin.creds written (contents not shown)"

if [ "$START" -eq 1 ]; then
	step "start nats-server detached"
	nohup nats-server -c "$CONF" >"$DIR/server.log" 2>&1 &
	echo $! >"$PIDF"
	i=0
	until port_open "$ph" "$PORT"; do
		i=$((i + 1))
		alive "$(cat "$PIDF")" || { tail -n 10 "$DIR/server.log" >&2; die "server exited"; }
		[ "$i" -lt 75 ] || die "port $ph:$PORT did not answer within 15 s (see $DIR/server.log)"
		sleep 0.2
	done
	did "nats-server pid $(cat "$PIDF") listens on $ph:$PORT (log $DIR/server.log)"
fi

URLHOST=$FIRSTHOST
case $URLHOST in *:*) URLHOST="[$URLHOST]" ;; esac
cat <<EOF

Done. $DIR holds the setup. Nothing above printed a seed or credential.
$([ "$START" -eq 1 ] || printf 'Start the server:  nats-server -c %s\n' "$CONF")
Next, with mashctl (replace <owner> with your owner name):

  export MASH_TLS_CA=$DIR/ca.pem
  mashctl adopt --server tls://$URLHOST:$PORT --owner <owner> --account $ACCOUNT \\
    --operator-key $EXP/operator.nk --account-key $EXP/account.nk \\
    --sys-creds $EXP/sys.creds --admin-creds $EXP/mashadmin.creds
  mashctl init

Check later:  $0 --dir $DIR --check
Stop:         $0 --dir $DIR --stop
EOF
