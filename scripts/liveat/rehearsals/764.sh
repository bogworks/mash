# #764: --changed is gone in this build; mashctl endpoint reissue ADDRESS is the way.
exec 2>&1
PS4='$ '
note() { { set +x; } 2>/dev/null; echo; echo "## $*"; set -x; }
set -x
mash-local-nats-setup.sh --dir /node/nats --owner acme >/dev/null
mashctl init --config /node/nats/mash-setup.yaml
mashctl endpoint create acme.demo.alice
mash --endpoint acme.demo.alice status

note "the help of reissue: one address, no --changed"
mashctl endpoint reissue --help
note "--changed is refused"
mashctl endpoint reissue --changed; echo "exit code $?"
mashctl endpoint reissue acme.demo.alice --changed; echo "exit code $?"

note "the case behind the issue: someone reissues alice elsewhere (here: a join file), so this machine's creds file holds a cancelled key"
mkdir -m 700 /node/out
mashctl endpoint reissue acme.demo.alice --out /node/out/alice.join
mash --endpoint acme.demo.alice status; echo "exit code $?"
mash --endpoint acme.demo.alice send acme.demo.alice "to myself"; echo "exit code $?"
note "the way on: reissue ADDRESS on this machine; it checks the old file first and says what to do"
mashctl endpoint reissue acme.demo.alice; echo "exit code $?"
note "do what its hint says: move the old file aside, then run the same command again"
mv /node/.config/mash/acme.demo.alice.creds /node/.config/mash/acme.demo.alice.creds.old
mashctl endpoint reissue acme.demo.alice; echo "exit code $?"
mash --endpoint acme.demo.alice status; echo "exit code $?"
mash --endpoint acme.demo.alice send acme.demo.alice "to myself"; echo "exit code $?"
mash --endpoint acme.demo.alice inbox
