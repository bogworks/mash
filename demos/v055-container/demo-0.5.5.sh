#!/bin/bash
# mash 0.5.5 demo: two endpoints exchange signed mail over TLS inside one FROM-scratch podman container.
# Usage: sh demos/v055-container/demo-0.5.5.sh up | send | status | down        (DEMO_PORT=24222 by default)
set -eu

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
HERE=$SCRIPT_DIR
VER=v0.5.5
REL=https://github.com/bogworks/mash/releases/download/$VER
IMAGE=mash-demo-0.5.5:1
CNAME=mash-demo-055
LABEL=mash-demo=0.5.5
DL=$HERE/downloads
BUILD=$HERE/build
CTX=$BUILD/ctx
SRC=$HERE/src-v0.5.5
STATE=$HERE/state
PORTFILE=$STATE/port

say() { printf '\n== %s\n' "$*"; }
die() { printf 'demo: %s\n' "$*" >&2; exit 1; }
show() { printf '   $ %s\n' "$*"; }

exists() { [ -n "$(podman ps -a --filter "name=^${CNAME}\$" --format '{{.Names}}')" ]; }
image_exists() { podman image exists "$IMAGE"; }

# in_box [VAR=VALUE ...] -- COMMAND ARGS...   run inside the container with the demo's home and directories.
in_box() {
	envargs="-e HOME=/demo/home -e MASH_CONFIG_DIR=/demo/config -e MASH_STATE_DIR=/demo/state -e MASH_KEYS_DIR=/demo/keys -e PATH=/bin"
	while [ "$1" != "--" ]; do envargs="$envargs -e $1"; shift; done
	shift
	# shellcheck disable=SC2086
	podman exec $envargs "$CNAME" "$@"
}
# step HEADING [VAR=VALUE ...] -- COMMAND ARGS...   print the heading and the exact command, run it
step() {
	say "$1"; shift
	shown=""; past=0
	for a in "$@"; do
		if [ "$a" = "--" ]; then past=1; continue; fi
		if [ "$past" = 1 ]; then shown="$shown $(printf '%q' "$a")"; else shown="$shown $a"; fi
	done
	printf '   $%s\n' "$shown"
	in_box "$@"
}

port_free() { ! ss -ltn 2>/dev/null | awk '{print $4}' | grep -q "[:.]$1\$"; }

cmd_up() {
	command -v podman >/dev/null || die "podman is required"
	if exists; then
		echo "demo: container $CNAME already exists; not touching it. Run '$0 down' first." >&2
		exit 1
	fi
	mkdir -p "$DL" "$BUILD" "$STATE"

	say "1. Download the mash $VER release binary and verify its checksum"
	if [ ! -f "$DL/mash-linux-amd64" ] || [ ! -f "$DL/SHA256SUMS" ]; then
		show "curl -fsSL $REL/mash-linux-amd64 ; curl -fsSL $REL/SHA256SUMS"
		curl -fsSL -o "$DL/mash-linux-amd64.part" "$REL/mash-linux-amd64"
		curl -fsSL -o "$DL/SHA256SUMS" "$REL/SHA256SUMS"
		mv "$DL/mash-linux-amd64.part" "$DL/mash-linux-amd64"
	else
		echo "   (already downloaded in $DL; checking again)"
	fi
	show "grep ' mash-linux-amd64\$' SHA256SUMS | sha256sum -c -"
	[ "$(grep -c ' mash-linux-amd64$' "$DL/SHA256SUMS")" = 1 ] || die "SHA256SUMS has no single entry for mash-linux-amd64; refusing to continue"
	( cd "$DL" && grep ' mash-linux-amd64$' SHA256SUMS | sha256sum -c - ) || die "CHECKSUM MISMATCH for mash-linux-amd64; refusing to continue"
	chmod +x "$DL/mash-linux-amd64"
	if command -v gh >/dev/null; then
		show "gh attestation verify mash-linux-amd64 -R bogworks/mash --signer-workflow bogworks/mash/.github/workflows/release.yml --source-ref refs/tags/$VER"
		if ( cd "$DL" && gh attestation verify mash-linux-amd64 -R bogworks/mash \
			--signer-workflow bogworks/mash/.github/workflows/release.yml --source-ref "refs/tags/$VER" ) 2>&1 | sed 's/^/   /'; then
			echo "   attestation: verified"
		else
			echo "   WARNING: attestation check did not pass (optional step; the checksum above did)"
		fi
	else
		echo "   (gh not installed: skipping the optional attestation check)"
	fi
	[ "$("$DL/mash-linux-amd64" version | cut -d' ' -f2)" = "0.5.5" ] || die "downloaded binary is not mash 0.5.5"

	say "2. Build the helper binaries (static, from source)"
	command -v go >/dev/null || die "go is required to build mashctl, nats-server and nodeinit"
	if [ ! -x "$BUILD/mashctl" ]; then
		show "CGO_ENABLED=0 GOBIN=$BUILD go install github.com/bogworks/mash/cmd/mashctl@$VER"
		CGO_ENABLED=0 GOBIN="$BUILD" go install github.com/bogworks/mash/cmd/mashctl@$VER
	fi
	if [ ! -x "$BUILD/nats-server" ]; then
		show "CGO_ENABLED=0 GOBIN=$BUILD go install github.com/nats-io/nats-server/v2@v2.15.1"
		CGO_ENABLED=0 GOBIN="$BUILD" go install github.com/nats-io/nats-server/v2@v2.15.1
	fi
	if [ ! -x "$BUILD/nodeinit" ]; then
		if [ ! -d "$SRC" ]; then
			show "git clone --depth 1 --branch $VER https://github.com/bogworks/mash.git $SRC"
			git clone -q --depth 1 --branch "$VER" https://github.com/bogworks/mash.git "$SRC"
		fi
		show "CGO_ENABLED=0 go -C $SRC build -o $BUILD/nodeinit ./scripts/liveat/nodeinit"
		CGO_ENABLED=0 go -C "$SRC" build -o "$BUILD/nodeinit" ./scripts/liveat/nodeinit
	fi
	for b in mashctl nats-server nodeinit; do
		if ldd "$BUILD/$b" >/dev/null 2>&1; then die "$b is not static"; fi
	done
	echo "   mashctl, nats-server, nodeinit: static binaries"

	say "3. Build the image (FROM scratch: four binaries, no shell, nothing pulled)"
	mkdir -p "$CTX"
	cp "$DL/mash-linux-amd64" "$CTX/mash"
	cp "$BUILD/mashctl" "$BUILD/nats-server" "$BUILD/nodeinit" "$CTX/"
	printf 'FROM scratch\nCOPY mash mashctl nats-server nodeinit /bin/\nENTRYPOINT []\n' >"$CTX/Containerfile"
	show "podman build --pull=never --label $LABEL -t $IMAGE $CTX"
	podman build -q --pull=never --label "$LABEL" -t "$IMAGE" "$CTX" >/dev/null

	say "4. Start the container"
	show "podman run -d --name $CNAME --label $LABEL $IMAGE /bin/nodeinit serve"
	podman run -d --name "$CNAME" --label "$LABEL" "$IMAGE" /bin/nodeinit serve >/dev/null
	for d in /demo/home /demo/config /demo/state /demo/keys; do in_box -- /bin/nodeinit mkdir "$d"; done

	P=${DEMO_PORT:-24222}
	if ! port_free "$P"; then
		old=$P
		while ! port_free "$P"; do P=$((P + 1)); done
		echo "   port $old is busy on this host; using $P"
	fi
	echo "$P" >"$PORTFILE"

	step "5. Start a local TLS-only NATS server with its own CA, and make the owner 'randy' (server port $P, inside the container)" \
		-- /bin/mashctl init --local --owner randy --port "$P" --start
	for e in alpha beta; do
		step "6. Create endpoint randy/demo/$e (the key is made inside the container)" \
			-- /bin/mashctl endpoint create "randy/demo/$e" --make-key
	done
	step "7. List the endpoints" -- /bin/mashctl endpoint list
	echo
	echo "Up. Now run: $0 send"
}

cmd_send() {
	exists || die "container $CNAME is not running; run '$0 up' first"
	P=$(cat "$PORTFILE")
	A="MASH_OWNER=randy MASH_GROUP=demo MASH_NAME=alpha"
	B="MASH_OWNER=randy MASH_GROUP=demo MASH_NAME=beta"
	# shellcheck disable=SC2086
	set -- $A; A1=$1 A2=$2 A3=$3
	# shellcheck disable=SC2086
	set -- $B; B1=$1 B2=$2 B3=$3

	step "alpha: status (config, credentials, TLS)" "$A1" "$A2" "$A3" -- /bin/mash status
	step "alpha sends mail to beta" "$A1" "$A2" "$A3" -- /bin/mash send randy.demo.beta "hello from alpha"
	step "beta reads its inbox (the label [verified] means alpha's own key signed it)" "$B1" "$B2" "$B3" -- /bin/mash inbox
	step "beta replies to alpha" "$B1" "$B2" "$B3" -- /bin/mash send randy.demo.alpha "hello back from beta"
	step "alpha reads its inbox" "$A1" "$A2" "$A3" -- /bin/mash inbox

	REFUSED="MASH_URL=nats://127.0.0.1:$P"
	say "REFUSED CASE: a plain-text nats:// URL, with no override. mash must refuse before it connects (expect exit 2)"
	echo "   \$ $REFUSED $A1 $A2 $A3 /bin/mash send randy.demo.beta \"this must not be sent\""
	set +e
	in_box "$REFUSED" "$A1" "$A2" "$A3" -- /bin/mash send randy.demo.beta "this must not be sent"
	rc=$?
	set -e
	echo "   exit code: $rc"
	[ "$rc" = 2 ] || die "expected exit code 2 from the refused send, got $rc"
	echo "   -> refused as expected: nothing was sent over plain text."
	step "beta's inbox again: only the one message from alpha, the refused one is not there" "$B1" "$B2" "$B3" -- /bin/mash inbox
	echo
	echo "Send demo finished. Clean up with: $0 down"
}

cmd_status() {
	podman ps -a --filter "name=^${CNAME}\$" --format 'container {{.Names}}: {{.Status}}'
	exists || echo "container $CNAME: none"
	if image_exists; then echo "image $IMAGE: present"; else echo "image $IMAGE: none"; fi
}

cmd_down() {
	if exists; then
		say "Remove the container (this also stops the NATS server inside it)"
		show "podman rm -f $CNAME"
		podman rm -f "$CNAME" >/dev/null
	else
		echo "container $CNAME: none"
	fi
	if image_exists; then
		show "podman rmi $IMAGE"
		podman rmi "$IMAGE" >/dev/null
	else
		echo "image $IMAGE: none"
	fi
	rm -f "$PORTFILE"
	echo "Removed. (Downloads and builds stay in $HERE for next time; delete that directory to remove them.)"
}

case "${1:-}" in
up) cmd_up ;;
send) cmd_send ;;
status) cmd_status ;;
down) cmd_down ;;
*) echo "usage: $0 up | send | status | down" >&2; exit 2 ;;
esac
