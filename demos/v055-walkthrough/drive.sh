#!/bin/bash
# mash v0.5.5 internal CLI walk-through driver (demo-video@0.1.0, vhs-demo skill).
# Usage: bash drive.sh [--narrate] [--pause SECONDS]      (no --narrate: plain report mode, no pauses, no typing)
# Everything runs live inside one FROM-scratch podman container (a host with rootless podman); the container's own network namespace holds the servers.
set -u

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
HERE=$SCRIPT_DIR
V=$SCRIPT_DIR/build
mkdir -p "$V"
# Downloads and helper builds are reused from the sibling demo (run its `up` once to fetch and build them).
D=${DEMO_SRC:-$SCRIPT_DIR/../v055-container}
VER=v0.5.5
COMMIT=e389eaf807279b58e63b05578a779ef672d93a36
IMAGE=mash-video-0.5.5:1
CNAME=mash-video-055
LABEL=mash-video=0.5.5
CTX=$V/ctx
PORT_RANDY=24222
PORT_BOB=24223
STEPS=$HERE/steps.md
NARRATE=0
PAUSE_SCALE=1
while [ $# -gt 0 ]; do
	case "$1" in
	--narrate) NARRATE=1 ;;
	--pause) PAUSE_SCALE=$2; shift ;;
	*) echo "usage: drive.sh [--narrate] [--pause SECONDS-SCALE]" >&2; exit 2 ;;
	esac
	shift
done
[ "$NARRATE" = 1 ] || PAUSE_SCALE=0
[ "$PORT_RANDY" != 4222 ] && [ "$PORT_BOB" != 4222 ] || { echo "refusing the default port 4222" >&2; exit 2; }

B=$'\033[1m'; C=$'\033[36m'; G=$'\033[32m'; R=$'\033[31m'; Y=$'\033[33m'; Z=$'\033[0m'
[ "$NARRATE" = 1 ] || { B=; C=; G=; R=; Y=; Z=; }

pause() { # pause SECONDS: scaled; 0 in report mode
	awk -v s="$1" -v k="$PAUSE_SCALE" 'BEGIN{ if (s*k > 0) system("sleep " s*k) }'
}
cleanup_done=0
cleanup() {
	[ "$cleanup_done" = 1 ] && return
	cleanup_done=1
	echo "${C}\$ podman rm -f $CNAME${Z}"
	podman rm -f "$CNAME" 2>&1 | sed 's/^/  removed container /'
	echo "${C}\$ podman rmi $IMAGE${Z}"
	podman rmi "$IMAGE" 2>&1 | sed 's/^/  /'
	echo "${C}\$ podman ps -a --filter label=$LABEL ; podman images --filter label=$LABEL${Z}"
	n1=$(podman ps -a --filter "label=$LABEL" --format '{{.Names}}' | wc -l)
	n2=$(podman images --filter "label=$LABEL" --format '{{.ID}}' | wc -l)
	echo "  containers left: $n1   images left: $n2"
}
trap 'cleanup; exit 130' INT TERM
trap 'cleanup' EXIT

redact() {
	sed -E \
		-e 's/S[UAO][A-Z2-7]{56}/[seed redacted]/g' \
		-e 's/eyJ[A-Za-z0-9._=-]+/[jwt redacted]/g' \
		-e '/-----BEGIN/,/-----END/c\[pem block redacted]'
}
fold_text() { fold -s -w 132; }

type_cmd() { # type_cmd TEXT : show as typed
	printf '%s$ %s' "$C" "$Z"
	if [ "$NARRATE" = 1 ]; then
		printf '%s' "$1" | while IFS= read -r -n1 ch; do printf '%s' "$ch"; sleep 0.018; done
		sleep 0.4
	else
		printf '%s' "$1"
	fi
	printf '\n'
}

SETUP_ENV=(-e HOME=/demo/home -e MASH_CONFIG_DIR=/demo/config -e MASH_STATE_DIR=/demo/state -e MASH_KEYS_DIR=/demo/keys -e PATH=/bin)
LAST_RC=0
LAST_OUT=""
STEP_ID=""; STEP_TITLE=""; STEP_N=0; STEP_CMDS=0; STEP_K=0
TMPOUT=$(mktemp "${TMPDIR:-/tmp}/mash-video-out.XXXXXX")
trap 'rm -f "$TMPOUT"; cleanup' EXIT

# narrate_block ID: print the narration for a step from steps.md (everything under "## ID" up to "Commands:")
narrate_block() {
	[ -f "$STEPS" ] || { echo "(no steps.md)"; return; }
	awk -v id="$1" '
		$0 ~ "^## " id " " { on=1; t=$0; sub(/^## [^ ]+ /,"",t); print "\001" t; next }
		/^## / { on=0 }
		on && /^Commands:/ { on=0 }
		on { print }' "$STEPS"
}

begin_step() { # begin_step ID NCMDS
	STEP_ID=$1; STEP_CMDS=$2; STEP_K=0; STEP_N=$((STEP_N + 1))
	local block title
	block=$(narrate_block "$1")
	title=$(printf '%s\n' "$block" | sed -n 's/^\x01//p' | head -1)
	STEP_TITLE=${title:-$1}
	if [ "$NARRATE" = 1 ]; then
		clear
		printf '%s%s%s\n\n' "$B" "$STEP_TITLE" "$Z"
		printf '%s\n' "$block" | sed '/^\x01/d' | sed -E "s/^(What you see:|Why:|Analogy:|Expect:)/${Y}\1${Z}/" | fold_text
		pause "${NARR_PAUSE:-12}"
	else
		printf '\n===== %s =====\n' "$STEP_TITLE"
	fi
}

# run_cmd SHOWN_TEXT ENVARGS... -- ARGV...   (ENVARGS are podman -e pairs; ARGV runs in the container)
run_cmd() {
	local shown=$1; shift
	local envs=()
	while [ "$1" != "--" ]; do envs+=("$1"); shift; done
	shift
	STEP_K=$((STEP_K + 1))
	if [ "$NARRATE" = 1 ]; then
		clear
		printf '%s%s%s  (command %d of %d)\n\n' "$B" "$STEP_TITLE" "$Z" "$STEP_K" "$STEP_CMDS"
	fi
	type_cmd "$shown"
	podman exec -w /demo/home "${SETUP_ENV[@]}" "${envs[@]}" "$CNAME" "$@" >"$TMPOUT" 2>&1
	LAST_RC=$?
	emit_output
	pause "${CMD_PAUSE:-5}"
}
emit_output() {
	local n
	redact <"$TMPOUT" >"$TMPOUT.r"
	n=$(wc -l <"$TMPOUT.r")
	LAST_OUT=$(cat "$TMPOUT.r")
	if [ "$n" -gt 25 ]; then
		head -25 "$TMPOUT.r"
		echo "${Y}... $((n - 25)) more lines cut${Z}"
	else
		cat "$TMPOUT.r"
	fi
	rm -f "$TMPOUT.r"
	if [ "$LAST_RC" = 0 ]; then echo "${G}(exit $LAST_RC)${Z}"; else echo "${R}(exit $LAST_RC)${Z}"; fi
}
refused() { # refused CODE: report whether the exit code is the expected refusal
	if [ "$LAST_RC" = 0 ]; then echo "${R}UNEXPECTED: this should have been refused but exited 0${Z}"; UNEXPECTED=$((UNEXPECTED + 1)); fi
}
UNEXPECTED=0
end_step() { :; }

ME="MASH_OWNER=randy MASH_GROUP=demo MASH_NAME"   # display prefix for acting as an endpoint
as() { # as NAME -> podman -e args for endpoint NAME under randy/demo
	printf '%s\n' -e MASH_OWNER=randy -e MASH_GROUP=demo -e "MASH_NAME=$1"
}

########## A. Setup (hidden in narrate mode before READY)
setup() {
	echo "== setup: reuse the verified downloads and builds of the sibling demo; image, container, directories"
	command -v podman >/dev/null || return 1
	[ -f "$D/downloads/SHA256SUMS" ] && [ -x "$D/build/mashctl" ] || { echo "no downloads/builds in $D: run 'sh demos/v055-container/demo-0.5.5.sh up' once (then 'down'), or set DEMO_SRC"; return 1; }
	[ -z "$(podman ps -a --filter "name=^${CNAME}\$" --format '{{.Names}}')" ] || { echo "container $CNAME exists; not touching it"; return 1; }
	! podman image exists "$IMAGE" || { echo "image $IMAGE exists; not touching it"; return 1; }
	[ "$(grep -c ' mash-linux-amd64$' "$D/downloads/SHA256SUMS")" = 1 ] || { echo "SHA256SUMS lacks one entry"; return 1; }
	( cd "$D/downloads" && grep ' mash-linux-amd64$' SHA256SUMS | sha256sum -c - ) || { echo "CHECKSUM MISMATCH"; return 1; }
	"$D/downloads/mash-linux-amd64" version | grep -q "0.5.5 (commit $COMMIT)" || { echo "release binary is not 0.5.5/$COMMIT"; return 1; }
	go version -m "$D/build/mashctl" | grep -q "mod.*github.com/bogworks/mash.*v0.5.5" || { echo "mashctl is not module v0.5.5"; return 1; }
	"$D/build/nats-server" --version | grep -q "2.15.1" || { echo "nats-server is not 2.15.1"; return 1; }
	for b in mashctl nats-server nodeinit; do ! ldd "$D/build/$b" >/dev/null 2>&1 || { echo "$b not static"; return 1; }; done
	mkdir -p "$CTX" || return 1
	cp "$D/downloads/mash-linux-amd64" "$CTX/mash" && cp "$D/build/mashctl" "$D/build/nats-server" "$D/build/nodeinit" "$CTX/" || return 1
	printf 'FROM scratch\nCOPY mash mashctl nats-server nodeinit /bin/\nENTRYPOINT []\n' >"$CTX/Containerfile"
	podman build -q --pull=never --label "$LABEL" -t "$IMAGE" "$CTX" >/dev/null || return 1
	podman run -d --name "$CNAME" --label "$LABEL" "$IMAGE" /bin/nodeinit serve >/dev/null || return 1
	for d in /demo/home /demo/config /demo/state /demo/keys; do podman exec "$CNAME" /bin/nodeinit mkdir "$d" || return 1; done
	echo "== setup done"
}
if [ "$NARRATE" = 1 ]; then
	clear
	setup >"$V/setup.log" 2>&1 || { cat "$V/setup.log"; exit 1; }
else
	setup || exit 1
fi
echo READY
sleep 0.2

########## B. Walk
# 1 title
if [ "$NARRATE" = 1 ]; then
	clear
	narrate_block s01 | sed -E 's/^\x01(.*)$/'"${B}"'\1'"${Z}"'\n/' | fold_text
	pause "${TITLE_PAUSE:-24}"
else
	echo; echo "===== title screen ====="; narrate_block s01 | sed 's/^\x01//'
fi

begin_step s02 2
run_cmd "mash version" -- /bin/mash version
run_cmd "mashctl version" -- /bin/mashctl version

begin_step s03 2
run_cmd "mashctl init --local --owner randy --port $PORT_RANDY --start" -- /bin/mashctl init --local --owner randy --port "$PORT_RANDY" --start
# harmless view of the config's url line (the image has no cat; read it from outside, only three keys)
STEP_K=$((STEP_K + 1))
if [ "$NARRATE" = 1 ]; then clear; printf '%s%s%s  (command %d of %d)\n\n' "$B" "$STEP_TITLE" "$Z" "$STEP_K" "$STEP_CMDS"; fi
type_cmd "grep -E '\"(url|owner|tls_ca)\"' /demo/config/config.json     # read with podman cp: the image has no grep"
podman cp "$CNAME:/demo/config/config.json" - 2>/dev/null | tar -xO 2>/dev/null | grep -E '"(url|owner|tls_ca)"' >"$TMPOUT"
LAST_RC=${PIPESTATUS[0]}; [ -s "$TMPOUT" ] && LAST_RC=0 || LAST_RC=1
emit_output; pause "${CMD_PAUSE:-5}"

begin_step s04 3
run_cmd "mash key create --out alpha.seed --pub-out alpha.pub" -- /bin/mash key create --out alpha.seed --pub-out alpha.pub
run_cmd "mashctl endpoint create randy/demo/alpha --public-key alpha.pub --jwt-out randy.demo.alpha.jwt" -- /bin/mashctl endpoint create randy/demo/alpha --public-key alpha.pub --jwt-out randy.demo.alpha.jwt
run_cmd "mash credential create --seed alpha.seed --jwt randy.demo.alpha.jwt" -- /bin/mash credential create --seed alpha.seed --jwt randy.demo.alpha.jwt

begin_step s05 1
run_cmd "mashctl endpoint create randy/demo/beta --make-key" -- /bin/mashctl endpoint create randy/demo/beta --make-key

begin_step s06 2
run_cmd "mashctl endpoint list" -- /bin/mashctl endpoint list
run_cmd "mashctl endpoint show randy/demo/alpha" -- /bin/mashctl endpoint show randy/demo/alpha

begin_step s07 1
run_cmd "$ME=alpha mash status" -e MASH_OWNER=randy -e MASH_GROUP=demo -e MASH_NAME=alpha -- /bin/mash status

begin_step s08 4
run_cmd "$ME=alpha mash send randy.demo.beta \"hello from alpha\"" -e MASH_OWNER=randy -e MASH_GROUP=demo -e MASH_NAME=alpha -- /bin/mash send randy.demo.beta "hello from alpha"
run_cmd "$ME=beta mash inbox" -e MASH_OWNER=randy -e MASH_GROUP=demo -e MASH_NAME=beta -- /bin/mash inbox
run_cmd "$ME=beta mash send randy.demo.alpha \"hello back from beta\"" -e MASH_OWNER=randy -e MASH_GROUP=demo -e MASH_NAME=beta -- /bin/mash send randy.demo.alpha "hello back from beta"
run_cmd "$ME=alpha mash inbox" -e MASH_OWNER=randy -e MASH_GROUP=demo -e MASH_NAME=alpha -- /bin/mash inbox

begin_step s09 1
run_cmd "mashctl endpoint reissue randy/demo/beta --make-key" -- /bin/mashctl endpoint reissue randy/demo/beta --make-key

begin_step s10 5
run_cmd "mashctl endpoint delete randy/demo/beta" -- /bin/mashctl endpoint delete randy/demo/beta
RETIRED=$(printf '%s\n' "$LAST_OUT" | sed -n 's/^creds file renamed to \([^ ]*\) .*/\1/p')
run_cmd "$ME=beta mash inbox" -e MASH_OWNER=randy -e MASH_GROUP=demo -e MASH_NAME=beta -- /bin/mash inbox
if [ -n "$RETIRED" ]; then
	run_cmd "$ME=beta MASH_CREDS=${RETIRED} mash inbox" -e MASH_OWNER=randy -e MASH_GROUP=demo -e MASH_NAME=beta -e "MASH_CREDS=$RETIRED" -- /bin/mash inbox
	run_cmd "$ME=beta MASH_CREDS=${RETIRED} mash status" -e MASH_OWNER=randy -e MASH_GROUP=demo -e MASH_NAME=beta -e "MASH_CREDS=$RETIRED" -- /bin/mash status
	run_cmd "mashctl issued list" -- /bin/mashctl issued list
else
	echo "NOTE: could not find the retired creds file name in the delete output"
fi

########## C. Negative examples
begin_step n1 1
run_cmd "mashctl endpoint create randy/design --make-key" -- /bin/mashctl endpoint create randy/design --make-key
refused

begin_step n2 1
run_cmd "mashctl endpoint create design --make-key" -- /bin/mashctl endpoint create design --make-key
refused

begin_step n3 1
run_cmd "MASH_URL=nats://127.0.0.1:$PORT_RANDY $ME=alpha mash send randy.demo.beta hi" -e "MASH_URL=nats://127.0.0.1:$PORT_RANDY" -e MASH_OWNER=randy -e MASH_GROUP=demo -e MASH_NAME=alpha -- /bin/mash send randy.demo.beta hi
refused

BOBENV=(-e HOME=/demo/bob/home -e MASH_CONFIG_DIR=/demo/bob/config -e MASH_STATE_DIR=/demo/bob/state -e MASH_KEYS_DIR=/demo/bob/keys)
BOBSHOWN="MASH_CONFIG_DIR=/demo/bob/config MASH_KEYS_DIR=/demo/bob/keys"
for d in /demo/bob /demo/bob/home /demo/bob/config /demo/bob/state /demo/bob/keys; do podman exec "$CNAME" /bin/nodeinit mkdir "$d" >/dev/null 2>&1; done
begin_step n4 5
run_cmd "$BOBSHOWN mashctl init --local --owner bob --dir /demo/server-bob --port $PORT_BOB --start" "${BOBENV[@]}" -- /bin/mashctl init --local --owner bob --dir /demo/server-bob --port "$PORT_BOB" --start
run_cmd "$BOBSHOWN mashctl endpoint create bob/demo/eve --make-key" "${BOBENV[@]}" -- /bin/mashctl endpoint create bob/demo/eve --make-key
run_cmd "$BOBSHOWN mashctl endpoint list" "${BOBENV[@]}" -- /bin/mashctl endpoint list
run_cmd "$ME=alpha MASH_CREDS=/demo/bob/config/bob.demo.eve.creds mash inbox" -e MASH_OWNER=randy -e MASH_GROUP=demo -e MASH_NAME=alpha -e MASH_CREDS=/demo/bob/config/bob.demo.eve.creds -- /bin/mash inbox
refused
run_cmd "MASH_OWNER=bob MASH_GROUP=demo MASH_NAME=eve MASH_CREDS=/demo/bob/config/bob.demo.eve.creds mash inbox" -e MASH_OWNER=bob -e MASH_GROUP=demo -e MASH_NAME=eve -e MASH_CREDS=/demo/bob/config/bob.demo.eve.creds -- /bin/mash inbox
refused

begin_step n5 2
run_cmd "mashctl add alpha2" -- /bin/mashctl add alpha2
refused
run_cmd "mashctl retire alpha" -- /bin/mashctl retire alpha
refused

########## D. Close, cleanup
if [ "$NARRATE" = 1 ]; then
	clear
	narrate_block s99 | sed -E 's/^\x01(.*)$/'"${B}"'\1'"${Z}"'\n/' | fold_text
	pause "${CLOSE_PAUSE:-14}"
	clear
	printf '%sCleanup: removing this recording'"'"'s container and image by exact name%s\n\n' "$B" "$Z"
else
	echo; echo "===== close ====="; narrate_block s99 | sed 's/^\x01//'; echo; echo "===== cleanup ====="
fi
cleanup
echo "unexpected successes: $UNEXPECTED"
pause 1
echo DONE
pause "${DONE_PAUSE:-6}"
