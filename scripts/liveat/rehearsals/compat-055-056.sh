# 0.5.5 -> 0.5.6 compatibility: a server set up the 0.5.5 way, then 0.5.6's `mashctl init --config` over that same server,
# then mail both ways between a 0.5.5 endpoint and a 0.5.6 endpoint, then 0.5.6's endpoint list/show/doctor over the
# 0.5.5-made endpoint.
#
# Run with the 0.5.5 binaries copied in (liveat rehearse ... --put):
#   /node/v055/mash     the v0.5.5 release asset mash-linux-amd64 (sha256 checked against SHA256SUMS)
#   /node/v055/mashctl  built from the v0.5.5 tag source (mashctl is not a release asset at 0.5.5)
# 0.5.6 is main's build: mash and mashctl in /usr/local/bin, which the node image builds from this checkout.
#
# ONE shared HOME (/node) and ONE shared config directory (~/.config/mash, MASH_CONFIG_DIR unset) are used by BOTH
# versions, as one real user on one machine would have it: that is the compatibility question. A different config
# directory is used only if 0.5.6 refuses the shared one, and the script says so when it does.
#
# Every command is printed, then its output, then `exit code: N` (set +e style: a refusal does not stop the script).
# No key file is ever printed; the verb also removes any seed or JWT from what it shows.
exec 2>&1
set +e
M55=/node/v055/mash
C55=/node/v055/mashctl
M56=/usr/local/bin/mash
C56=/usr/local/bin/mashctl
CFG=/node/.config/mash
SRV=$CFG/server
URL=tls://127.0.0.1:4222

step() { echo; echo "=== step $*"; }
run() { echo; echo "\$ $*"; "$@"; local rc=$?; echo "exit code: $rc"; return $rc; }
# as ENDPOINT-GROUP-NAME TOOL args: run a 0.5.5 tool as one endpoint (0.5.5 takes group and name from the environment)
as55() { local g=$1 n=$2; shift 2; run env MASH_GROUP="$g" MASH_NAME="$n" "$@"; }
# the NATS buckets, as the admin sees them (names and key names only; the nats CLI reads the admin creds file itself)
buckets() {
  echo; echo "\$ nats kv ls (admin creds), then the key names of each bucket"
  for b in $(nats --server "$URL" --tlsca "$SRV/ca.pem" --creds "$SRV/mashadmin.creds" kv ls -n 2>&1); do
    echo "-- bucket $b:"
    nats --server "$URL" --tlsca "$SRV/ca.pem" --creds "$SRV/mashadmin.creds" kv ls "$b" 2>&1 | sed 's/^/   /'
  done
}

step "0: versions and what is on PATH"
run $M55 version
run $C55 version
run $C55 --version
run $M56 version
run $C56 version
run $C56 --version
run sha256sum $M55 $C55 $M56 $C56
run nats-server --version

step "1: the 0.5.5 way (README quick start at v0.5.5): init --local --start, then endpoints with --make-key"
run $C55 init --local --owner acme --start
run ls -la $CFG
run ls -la $SRV $CFG/keys
echo; echo "\$ cat $CFG/config.json   (urls, owner and file paths only)"; cat $CFG/config.json; echo "exit code: $?"
buckets
run $C55 endpoint create acme/dev/alice --make-key
run $C55 endpoint create acme/dev/bob --make-key
run ls -la $CFG
as55 dev alice $M55 status
as55 dev alice $M55 who
echo "--- 0.5.5 mail between two 0.5.5 endpoints"
as55 dev alice $M55 send acme.dev.bob "hello bob, from alice on 0.5.5"
as55 dev bob $M55 inbox
as55 dev bob $M55 read 1
as55 dev bob $M55 send acme.dev.alice "hello alice, from bob on 0.5.5"
as55 dev alice $M55 inbox
$C55 endpoint list; echo "(0.5.5 endpoint list, exit code: $?)"
echo; echo "\$ $C55 status   (0.5.5 mashctl has no status or doctor verb; first line of what it prints)"; $C55 status 2>&1 | head -1

step "2: 0.5.6 mashctl init --config over THE SAME 0.5.5 server (setup file built from what init --local left)"
cat > /node/setup-055.yaml <<EOF
servers: ["$URL"]
ca: "$SRV/ca.pem"
owner: acme
admin_creds: "$SRV/mashadmin.creds"
system_creds: "$SRV/sys.creds"
operator_key: "$CFG/keys/operator.nk"
account_key: "$CFG/keys/account.nk"
EOF
echo; echo "\$ cat /node/setup-055.yaml   (paths only)"; cat /node/setup-055.yaml
run $C56 init --config /node/setup-055.yaml --dry-run
run $C56 init --config /node/setup-055.yaml
INIT_RC=$?
E56=""
if [ "$INIT_RC" != 0 ]; then
  echo; echo "NOTE: 0.5.6 refused the shared config folder (exit $INIT_RC). A real user's way on: a new config folder with --config-dir."
  E56="--config-dir /node/mesh056"
  run $C56 init --config /node/setup-055.yaml $E56
fi
echo; echo "\$ cat $CFG/config.json   (after 0.5.6 init)"; cat $CFG/config.json; echo "exit code: $?"
run ls -la $CFG
buckets
run $C56 init --config /node/setup-055.yaml $E56
run $C56 status $E56
run $C56 limits show $E56
echo; echo "--- a 0.5.6 endpoint (main has no 'mash join'; the 0.5.6 flow that exists is mashctl endpoint create, creds into the config folder)"
run sh -c "$M56 --help | grep -ci join"
run $C56 endpoint create acme.demo.carol $E56
run ls -la $CFG
run $C56 endpoint create acme.demo.dave --out /node/dave.join $E56
run ls -la /node/dave.join
run $M56 --endpoint acme.demo.carol $E56 status
run $M56 --endpoint acme.demo.carol $E56 who

step "3: mail both ways, each side with its own binary"
echo "--- 0.5.5 alice -> 0.5.6 carol"
as55 dev alice $M55 send acme.demo.carol "hello carol, from alice on 0.5.5"
run $M56 --endpoint acme.demo.carol $E56 inbox
run $M56 --endpoint acme.demo.carol $E56 read 1
echo "--- 0.5.6 carol -> 0.5.5 alice"
run $M56 --endpoint acme.demo.carol $E56 send acme.dev.alice "hello alice, from carol on 0.5.6"
as55 dev alice $M55 inbox
as55 dev alice $M55 read 2
as55 dev alice $M55 read 3
as55 dev alice $M55 status
echo "--- 0.5.6 carol -> 0.5.5 bob, and 0.5.5 bob -> 0.5.6 carol"
run $M56 --endpoint acme.demo.carol $E56 send acme.dev.bob "second one, to bob, from carol on 0.5.6"
as55 dev bob $M55 inbox
as55 dev bob $M55 send acme.demo.carol "second one, to carol, from bob on 0.5.5"
run $M56 --endpoint acme.demo.carol $E56 inbox
run $M56 --endpoint acme.demo.carol $E56 read 2
echo "--- the 0.5.5 endpoint's creds used by the 0.5.6 mash"
A55CREDS=$CFG/acme.dev.alice.creds   # 0.5.5 puts creds in the shared folder; a --config-dir run needs --creds
run $M56 --endpoint acme.dev.alice --creds $A55CREDS $E56 status
run $M56 --endpoint acme.dev.alice --creds $A55CREDS $E56 inbox
run $M56 --endpoint acme.dev.alice --creds $A55CREDS $E56 who

step "4: 0.5.6 mashctl over the 0.5.5-made endpoints"
run $C56 endpoint list $E56
run $C56 endpoint list --wide $E56
run $C56 endpoint list acme.dev $E56
run $C56 endpoint show acme.dev.alice $E56
run $C56 endpoint show acme/dev/alice $E56
run $C56 endpoint show acme.dev.bob $E56
run $C56 endpoint show acme.demo.carol $E56
run $C56 endpoint inbox acme.dev.alice $E56
run $C56 endpoint permissions acme.dev.alice $E56
run $C56 doctor $E56
run $C56 doctor --strict $E56
run $C56 doctor --json $E56
run $M56 --endpoint acme.dev.alice --creds $A55CREDS $E56 doctor
run $M56 --endpoint acme.demo.carol $E56 doctor
buckets
step "5: the other real route on: keep ONE shared folder; move the 0.5.5 config.json aside so 0.5.6 can write its own"
if [ -n "$E56" ]; then
  run mv $CFG/config.json $CFG/config.json.055
  run $C56 init --config /node/setup-055.yaml
  echo; echo "\$ cat $CFG/config.json   (0.5.6's)"; cat $CFG/config.json; echo "exit code: $?"
  run $C56 endpoint create acme.demo.erin
  run $C56 endpoint list
  as55 dev alice $M55 who
  as55 dev alice $M55 status
  as55 dev alice $M55 inbox
  run $M56 --endpoint acme.demo.erin who
  run $M56 --endpoint acme.dev.alice who
  run $M56 --endpoint acme.demo.erin send acme.dev.alice "from erin on 0.5.6, shared folder"
  as55 dev alice $M55 inbox
  run $M56 --endpoint acme.dev.alice inbox
  run $C56 doctor
  buckets
fi
echo; echo "=== done"
