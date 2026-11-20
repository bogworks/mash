# #912: endpoint delete has no drained-delete guard; look first, suspend, then delete.
exec 2>&1
PS4='$ '
note() { { set +x; } 2>/dev/null; echo; echo "## $*"; set -x; }
set -x
mash-local-nats-setup.sh --dir /node/nats --owner acme >/dev/null
mashctl init --config /node/nats/mash-setup.yaml
mashctl endpoint create acme.demo.alice
mashctl endpoint create acme.demo.bob
mash --endpoint acme.demo.alice send acme.demo.bob "plan for friday"
mash --endpoint acme.demo.alice send acme.demo.bob "second note"

note "step 1: see what bob's inbox holds (no message text; DELIVERED no means bob has not taken it)"
mashctl endpoint inbox acme.demo.bob

note "step 2: suspend bob so nothing new arrives while you decide"
mashctl endpoint suspend acme.demo.bob
mashctl endpoint show acme.demo.bob
note "alice now tries to send to the suspended endpoint"
mash --endpoint acme.demo.alice send acme.demo.bob "too late?"; echo "exit code $?"
mashctl endpoint inbox acme.demo.bob

note "step 3: copy out what you need: bob's own session reads it"
mash --endpoint acme.demo.bob inbox
mash --endpoint acme.demo.bob read 1

note "step 4: delete states the mail it loses"
mashctl endpoint delete acme.demo.bob --dry-run
mashctl endpoint delete acme.demo.bob; echo "exit code $?"
note "with a terminal: the confirmation question (a pseudo-terminal, answered by typing the address)"
printf 'acme.demo.bob\n' | script -qec "mashctl endpoint delete acme.demo.bob" /dev/null; echo "exit code $?"
mashctl endpoint list
ls /node/.config/mash
