# #872 and #767: the reissue lock covers one machine only: reissue an endpoint from one machine at a time.
exec 2>&1
PS4='$ '
note() { { set +x; } 2>/dev/null; echo; echo "## $*"; set -x; }
set -x
mash-local-nats-setup.sh --dir /node/nats --owner acme >/dev/null
mashctl init --config /node/nats/mash-setup.yaml
mashctl endpoint create acme.demo.alice

note "two reissues of one endpoint at once, on one machine: the second waits for the first"
mashctl endpoint reissue acme.demo.alice > /node/r1.txt 2>&1 &
p1=$!
mashctl endpoint reissue acme.demo.alice > /node/r2.txt 2>&1 &
p2=$!
wait $p1; echo "first exit code $?"
wait $p2; echo "second exit code $?"
cat /node/r1.txt /node/r2.txt

note "a reissue that cannot get the lock in 10 s is refused: hold the lock (as a running reissue would) and try"
ls /node/.config/mash/
( flock -x 9; sleep 14 ) 9>/node/.config/mash/acme.demo.alice.creds.lock &
lp=$!
sleep 1
time mashctl endpoint reissue acme.demo.alice; echo "exit code $?"
wait $lp
note "the lock is gone with the holder: the next reissue works"
mashctl endpoint reissue acme.demo.alice; echo "exit code $?"
mashctl endpoint show acme.demo.alice

note "NOT RUN: two different machines reissuing one endpoint at once (the case the lock does not cover) needs two hosts sharing one server; the guidance is to reissue from one machine at a time"
