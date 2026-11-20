# #878: one config folder serves one mesh; a second mesh needs --config-dir.
exec 2>&1
PS4='$ '
note() { { set +x; } 2>/dev/null; echo; echo "## $*"; set -x; }
set -x

note "mesh A in the default folder, with alice and bob"
mash-local-nats-setup.sh --dir /node/natsA --owner acme
mashctl init --config /node/natsA/mash-setup.yaml
mashctl endpoint create acme.demo.alice
mashctl endpoint create acme.demo.bob

note "mesh B, a second server, in its own folder"
mash-local-nats-setup.sh --dir /node/natsB --owner globex
mashctl init --config /node/natsB/mash-setup.yaml --config-dir /node/meshB; echo "exit code $?"
mashctl endpoint create globex.ops.carol --config-dir /node/meshB
mashctl endpoint create globex.ops.dave --config-dir /node/meshB

note "mesh A sees only its own endpoints; mesh B only its own"
mash --endpoint acme.demo.alice who
mash --config-dir /node/meshB --endpoint globex.ops.carol who

note "mail works inside each mesh, with --config-dir (or MASH_CONFIG_DIR) naming the folder"
mash --endpoint acme.demo.alice send acme.demo.bob "hello inside A"
mash --endpoint acme.demo.bob inbox
mash --config-dir /node/meshB --endpoint globex.ops.carol send globex.ops.dave "hello inside B"
export MASH_CONFIG_DIR=/node/meshB
mash --endpoint globex.ops.dave inbox
unset MASH_CONFIG_DIR

note "mesh B's address is not known to mesh A's folder"
mash --endpoint acme.demo.alice send globex.ops.dave "across"; echo "exit code $?"

note "NOT RUN: the join-file route ('mash join FILE' on a machine already set up for another mesh) needs mash join, which is not on main yet"
mash --help | grep -E "^ *mash join" || echo "mash --help lists no join verb in this build"
