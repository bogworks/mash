# #866: mash read with no session marks the message read locally; the mailbox count drops when mash serve next starts.
exec 2>&1
PS4='$ '
note() { { set +x; } 2>/dev/null; echo; echo "## $*"; set -x; }
set -x
mash-local-nats-setup.sh --dir /node/nats --owner acme >/dev/null
mashctl init --config /node/nats/mash-setup.yaml
mashctl endpoint create acme.demo.alice
mashctl endpoint create acme.demo.bob
mash --endpoint acme.demo.alice send acme.demo.bob "one"
mash --endpoint acme.demo.alice send acme.demo.bob "two"
note "before: bob has 2 messages waiting"
mashctl endpoint show acme.demo.bob | grep -E 'state|waiting'
note "read #1 from a shell, with no session running"
mash --endpoint acme.demo.bob read 1
mashctl endpoint show acme.demo.bob | grep -E 'state|waiting'
mashctl endpoint inbox acme.demo.bob
note "now a session starts (mash serve, given the two opening lines of a Claude Code session on a pipe; the PIDs are recorded)"
mkfifo /node/in.fifo
mash --endpoint acme.demo.bob serve < /node/in.fifo > /node/serve.out 2> /node/serve.err &
SERVE=$!
{ printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"rehearse","version":"0"}}}' '{"jsonrpc":"2.0","method":"notifications/initialized"}'; sleep 40; } > /node/in.fifo &
SLEEP=$!
for i in $(seq 1 15); do sleep 1; mashctl endpoint show acme.demo.bob | grep -q 'state: *online' && break; done
sleep 3
mashctl endpoint show acme.demo.bob | grep -E 'state|waiting'
mashctl endpoint inbox acme.demo.bob
note "stop the session"
kill $SERVE $SLEEP; wait $SERVE 2>/dev/null
cat /node/serve.err | head -5
