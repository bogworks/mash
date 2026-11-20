# What a person sees for items accepted by design (#865, #867, #870, #875, #877, #882): read-only looks.
exec 2>&1
PS4='$ '
note() { { set +x; } 2>/dev/null; echo; echo "## $*"; set -x; }
set -x
mash-local-nats-setup.sh --dir /node/nats --owner acme >/dev/null
mashctl init --config /node/nats/mash-setup.yaml
mashctl endpoint create acme.demo.alice
mashctl endpoint create acme.demo.bob

note "#865: the NATS helper's whole option list; nothing for boot start, upgrade or backup"
mash-local-nats-setup.sh --help | sed -n 1,12p
note "#870: who is online now; there is no 'last seen' column or option"
mash --endpoint acme.demo.alice who
mash who --help | head -30
note "#875: mash doctor's rows (no channel or plugin row)"
mash --endpoint acme.demo.alice doctor; echo "exit code $?"
note "#877: who as JSON in the shell (the MCP tool returns an object, {\"endpoints\":[...]})"
mash --endpoint acme.demo.alice who --json
mashctl endpoint list --json
note "#882: a success prints no next-step hint"
mashctl endpoint set acme.demo.alice role=reviewer
mashctl endpoint show acme.demo.alice
note "#867: the unsafe plaintext variable, when inherited: what status and who say (the server here is TLS-only)"
MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1 mash --endpoint acme.demo.alice status; echo "exit code $?"
MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1 mash --endpoint acme.demo.alice who; echo "exit code $?"
