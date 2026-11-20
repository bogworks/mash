# #879: a join file made with a key holds a seed; one made with --public-key holds no seed.
# Only field names and file modes are printed: never a seed or a JWT.
exec 2>&1
PS4='$ '
note() { { set +x; } 2>/dev/null; echo; echo "## $*"; set -x; }
set -x
mash-local-nats-setup.sh --dir /node/nats --owner acme >/dev/null
mashctl init --config /node/nats/mash-setup.yaml
mkdir -m 700 /node/out

note "the risky route: mashctl makes the key, so the join file carries the seed"
mashctl endpoint create acme.demo.alice --out /node/out/alice.join
stat -c '%a %n' /node/out/alice.join
grep -o '"[a-z_]*":' /node/out/alice.join | tr -d '\n'; echo
grep -c 'SEED' /node/out/alice.join   # lines holding a seed marker
note "the file has no expiry and no single-use: reading it twice works, and it is just a file"
cp /node/out/alice.join /node/out/alice.join.copy && cmp /node/out/alice.join /node/out/alice.join.copy && echo "a copy is identical and usable as well"

note "the safe route: the endpoint's machine makes its own key; only the PUBLIC key travels"
mash key create --out /node/out/bob.seed
ls -l /node/out/bob.seed /node/out/bob.pub
mashctl endpoint create acme.demo.bob --public-key /node/out/bob.pub --out /node/out/bob.join
stat -c '%a %n' /node/out/bob.join
grep -o '"[a-z_]*":' /node/out/bob.join | tr -d '\n'; echo
grep -c 'SEED' /node/out/bob.join
note "the seed stays in bob.seed on bob's machine; mashctl never saw it"
note "NOT RUN: 'mash join FILE [--key SEED]' on the endpoint's machine needs mash join, which is not on main yet"
mash --help | grep -E "^ *mash join" || echo "mash --help lists no join verb in this build"
mashctl endpoint list
