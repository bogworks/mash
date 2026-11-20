# #955: mash inbox counts urgent mail among only the newest 200 messages; page with --after N.
exec 2>&1
PS4='$ '
note() { { set +x; } 2>/dev/null; echo; echo "## $*"; set -x; }
set -x
mash-local-nats-setup.sh --dir /node/nats --owner acme >/dev/null
mashctl init --config /node/nats/mash-setup.yaml
mashctl endpoint create acme.demo.alice
mashctl endpoint create acme.demo.bob

note "alice sends bob 250 messages; #3 and #7 are urgent, all the rest are not"
{ set +x; } 2>/dev/null
for i in $(seq 1 250); do
  case $i in 3|7) u=--urgent ;; *) u= ;; esac
  mash --endpoint acme.demo.alice send acme.demo.bob $u "message $i" >/dev/null || echo "send $i failed"
done
set -x

note "the default view (this build, main): the newest messages, oldest first; urgent counts are not shown here"
mash --endpoint acme.demo.bob inbox | head -3
note "page through from the start: 50 at a time with --after N (N is the last number already seen)"
mash --endpoint acme.demo.bob inbox --after 0 --limit 50 | cut -c1-90 | sed -n '1p;3p;7p;50p'
note "the urgent messages are among the oldest: read them by number"
mash --endpoint acme.demo.bob read 3 | head -8
mash --endpoint acme.demo.bob read 7 | head -8
note "the next page"
mash --endpoint acme.demo.bob inbox --after 50 --limit 50 | cut -c1-90 | sed -n '1p;50p'
mash --endpoint acme.demo.bob inbox --after 200 --limit 50 | cut -c1-90 | sed -n '1p;50p'
mashctl endpoint show acme.demo.bob
note "NOT RUN: the urgent count in mash inbox's header (counted over the newest 200) is in PR 951 (S4b), not on main yet"
