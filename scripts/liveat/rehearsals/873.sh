# #873: mashctl init needs 5 shared storage buckets; streams = endpoints + 5 (a paid Synadia account is needed).
exec 2>&1
PS4='$ '
note() { { set +x; } 2>/dev/null; echo; echo "## $*"; set -x; }
set -x
mash-local-nats-setup.sh --dir /node/nats --owner acme >/dev/null
N="nats --server tls://127.0.0.1:4222 --tlsca /node/nats/ca.pem --creds /node/nats/creds/mashadmin.creds"
note "before init: the account holds no streams"
$N stream ls -n
mashctl init --config /node/nats/mash-setup.yaml
note "after init, no endpoint yet: the five shared buckets"
$N stream ls -n
$N stream ls -n | wc -l
mashctl status
mashctl doctor
note "three endpoints"
for n in alice bob carol; do mashctl endpoint create acme.demo.$n; done
$N stream ls -n
echo "streams: $($N stream ls -n | wc -l) (3 endpoints + 5)"
mashctl status
mashctl doctor
