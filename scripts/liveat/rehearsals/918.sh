# #918: mashctl's online (holds the lease) and mash who's online (heartbeat) can disagree for up to 90 s after a session stops.
exec 2>&1
PS4='$ '
note() { { set +x; } 2>/dev/null; echo; echo "## $*"; set -x; }
set -x
mash-local-nats-setup.sh --dir /node/nats --owner acme >/dev/null
mashctl init --config /node/nats/mash-setup.yaml
mashctl endpoint create acme.demo.alice
mashctl endpoint create acme.demo.bob
mkfifo /node/in.fifo
mash --endpoint acme.demo.bob serve < /node/in.fifo > /node/serve.out 2> /node/serve.err &
SERVE=$!
{ printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"rehearse","version":"0"}}}' '{"jsonrpc":"2.0","method":"notifications/initialized"}'; sleep 400; } > /node/in.fifo &
SLEEP=$!
for i in $(seq 1 20); do sleep 1; mashctl endpoint show acme.demo.bob | grep -q 'state: *online' && break; done
sleep 8
note "session running: both views"
mashctl endpoint show acme.demo.bob | grep state
mash --endpoint acme.demo.alice who
note "crash the session (kill -9 of the recorded PID), then ask both every 5 s, with a clock"
{ set +x; } 2>/dev/null
kill -9 $SERVE; kill $SLEEP; wait $SERVE 2>/dev/null   # a crash: the session is killed, it does not say goodbye
T0=$(date +%s)
for i in $(seq 1 24); do
  now=$(( $(date +%s) - T0 ))
  a=$(mashctl endpoint show acme.demo.bob | awk '/^state:/{print $2}')
  b=$(mash --endpoint acme.demo.alice who | awk '$1=="acme.demo.bob"{print $2}')
  echo "t+${now}s  mashctl endpoint show: $a    mash who: $b"
  [ "$a" = offline ] && [ "$b" = offline ] && break
  sleep 5
done
set -x
mashctl endpoint show acme.demo.bob | grep state
mashctl endpoint show acme.demo.bob
