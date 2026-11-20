# #874, #935, #958: unverified mail, as this build (main) shows it. The refusal itself (S7, PR 952) is not on main.
exec 2>&1
PS4='$ '
note() { { set +x; } 2>/dev/null; echo; echo "## $*"; set -x; }
set -x
mash-local-nats-setup.sh --dir /node/nats --owner acme >/dev/null
mashctl init --config /node/nats/mash-setup.yaml
mashctl endpoint create acme.demo.alice
mashctl endpoint create acme.demo.bob
mash --endpoint acme.demo.alice send acme.demo.bob "a normal, signed message"

note "unsigned mail: published straight to bob's mail subject with the nats command (no mash signature), using alice's creds"
nats --server tls://127.0.0.1:4222 --tlsca /node/nats/ca.pem --creds /node/.config/mash/acme.demo.alice.creds pub agents.mail.acme.demo.bob "an unsigned message"; echo "exit code $?"

note "what the administrator sees: TRUST is the only signal; DELIVERED is yes/no"
mashctl endpoint inbox acme.demo.bob
note "what bob sees from a shell: every message with its trust label"
mash --endpoint acme.demo.bob inbox
mash --endpoint acme.demo.bob read 2
mashctl endpoint show acme.demo.bob | grep -E 'state|waiting'
mash --endpoint acme.demo.bob doctor; echo "exit code $?"

note "now a session starts for bob (mash serve, given the two opening lines of a Claude Code session on a pipe; PIDs recorded)"
mkfifo /node/in.fifo
mash --endpoint acme.demo.bob serve < /node/in.fifo > /node/serve.out 2> /node/serve.err &
SERVE=$!
{ printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"rehearse","version":"0"}}}' '{"jsonrpc":"2.0","method":"notifications/initialized"}'; sleep 60; } > /node/in.fifo &
SLEEP=$!
for i in $(seq 1 15); do sleep 1; mashctl endpoint show acme.demo.bob | grep -q 'state: *online' && break; done
sleep 3
note "after serve: what the administrator sees (DELIVERED yes for both; TRUST is the signal)"
mashctl endpoint inbox acme.demo.bob
note "what bob sees, and doctor's refused row"
mash --endpoint acme.demo.bob inbox
mash --endpoint acme.demo.bob doctor; echo "exit code $?"
note "what the session was told (the mail text of the first line only; the rest is quoted by mash)"
cut -c1-400 /node/serve.out | tail -2
kill $SERVE $SLEEP
note "NOT RUN beyond what main does: PR 952 (S7) makes serve refuse unverified mail by default; main's serve above is the build under test"
