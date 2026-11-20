# A join file for an address of another account (owner) is installed into a folder that already holds an endpoint.
# The other account's join file is made by changing the owner in a join file of the same server (the setup script
# makes one account per server), so it has the same servers and certificate authority.
exec 2>&1
PS4='$ '
note() { { set +x; } 2>/dev/null; echo; echo "## $*"; set -x; }
set -x
mash-local-nats-setup.sh --dir /node/nats --owner acme >/dev/null
mashctl init --config /node/nats/mash-setup.yaml
mkdir -m 700 /node/out
mash key create --out /node/out/alice.seed
mashctl endpoint create acme.demo.alice --public-key /node/out/alice.seed.pub --out /node/out/alice.join
mash key create --out /node/out/carol.seed
mashctl endpoint create acme.demo.carol --public-key /node/out/carol.seed.pub --out /node/out/carol.join

note "the folder holds one endpoint of owner acme"
mash join /node/out/alice.join --key /node/out/alice.seed
ls ~/.config/mash

note "a join file of the same server for an address of another owner (globex): installed beside it"
sed 's/acme\.demo\.carol/globex.ops.carol/g' /node/out/carol.join > /node/out/globex.join
grep -o '"address": *"[^"]*"' /node/out/globex.join
mash join /node/out/globex.join --key /node/out/carol.seed; echo "exit code $?"
ls ~/.config/mash

note "the workaround: one folder per account"
export MASH_CONFIG_DIR=/node/globex-folder
mash join /node/out/globex.join --key /node/out/carol.seed; echo "exit code $?"
ls /node/globex-folder
unset MASH_CONFIG_DIR
