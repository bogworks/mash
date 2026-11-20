# #1008: a first `mash join` killed after the creds are linked and before config.json is written.
# Only file names, modes and the tools' own messages are printed: never a seed or a JWT.
exec 2>&1
PS4='$ '
note() { { set +x; } 2>/dev/null; echo; echo "## $*"; set -x; }
set -x
mash-local-nats-setup.sh --dir /node/nats --owner acme >/dev/null
mashctl init --config /node/nats/mash-setup.yaml
mkdir -m 700 /node/out
mash key create --out /node/out/alice.seed
mashctl endpoint create acme.demo.alice --public-key /node/out/alice.seed.pub --out /node/out/alice.join
ls /node/out

note "a machine with no mash folder yet (this is the first join); kill join the moment the creds file shows up"
set +x
half=""
for try in $(seq 1 400); do
  h=/node/h$try
  mkdir -m 700 "$h"
  d=$h/.config/mash
  HOME=$h mash join /node/out/alice.join --key /node/out/alice.seed >/dev/null 2>&1 &
  pid=$!
  while [ ! -e "$d/acme.demo.alice.creds" ] && kill -0 "$pid" 2>/dev/null; do :; done
  kill -9 "$pid" 2>/dev/null
  wait "$pid" 2>/dev/null
  if [ -e "$d/acme.demo.alice.creds" ] && [ ! -e "$d/config.json" ]; then half=$h; echo "attempt $try: killed with the creds linked and no config.json"; break; fi
done
set -x
if [ -z "$half" ]; then echo "NO half-joined state in 400 attempts"; exit 3; fi
export HOME=$half

note "what the half-joined folder holds (names and modes only)"
ls -la ~/.config/mash

note "a retry is refused"
mash join /node/out/alice.join --key /node/out/alice.seed; echo "exit code $?"

note "--replace does not finish it either"
mash join /node/out/alice.join --key /node/out/alice.seed --replace; echo "exit code $?"
ls -la ~/.config/mash
mash --endpoint acme.demo.alice who; echo "exit code $?"

note "the workaround: move the folder aside and join again"
mv ~/.config/mash ~/.config/mash.half-joined
mash join /node/out/alice.join --key /node/out/alice.seed; echo "exit code $?"
ls -la ~/.config/mash
mash --endpoint acme.demo.alice who; echo "exit code $?"
