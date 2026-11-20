# #965: mashctl init --config refuses a folder set up for a different server; two ways on.
exec 2>&1
PS4='$ '
note() { { set +x; } 2>/dev/null; echo; echo "## $*"; set -x; }
set -x

note "mesh 1: a local server, set up with init (this is the folder ~/.config/mash)"
mash-local-nats-setup.sh --dir /node/nats --owner acme
mashctl init --config /node/nats/mash-setup.yaml
mashctl endpoint create acme.demo.alice

note "a second, different server (what you get after --remove and a fresh setup, or a second mesh)"
mash-local-nats-setup.sh --dir /node/nats2 --owner acme
note "the refusal: the same config folder, a different server"
mashctl init --config /node/nats2/mash-setup.yaml; echo "exit code $?"
ls /node/.config/mash
mashctl status

note "way 1: a new folder with --config-dir"
mashctl init --config /node/nats2/mash-setup.yaml --config-dir /node/mesh2; echo "exit code $?"
mashctl status --config-dir /node/mesh2
mashctl endpoint create acme.demo.carol --config-dir /node/mesh2
mashctl endpoint list --config-dir /node/mesh2
note "the first mesh is untouched"
mashctl status
mashctl endpoint list

note "way 2: move the old folder aside, then run the printed init line again"
mv /node/.config/mash /node/.config/mash.old
mashctl init --config /node/nats2/mash-setup.yaml; echo "exit code $?"
mashctl status
mashctl endpoint list
ls /node/.config /node/.config/mash.old
