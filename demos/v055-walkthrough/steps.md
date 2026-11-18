# mash v0.5.5 internal CLI walk-through: narration

Written from the run's own output (report mode, 2026-11-18). Each block is read by drive.sh by its id.
Every command runs live inside one throwaway FROM-scratch podman container (`mash-video-055`). The container's own network
namespace holds the servers (ports 24222 and 24223), so nothing is published on the host and 4222 is never used. Hidden
environment on every command: HOME and MASH_STATE_DIR under /demo, MASH_CONFIG_DIR=/demo/config and MASH_KEYS_DIR=/demo/keys
(the bob steps show theirs). Commands run in /demo/home, so alpha.seed means /demo/home/alpha.seed.

## s01 mash v0.5.5: internal CLI walk-through
What this is: a recording of the v0.5.5 command line, for internal review. Every command runs live in a throwaway container;
nothing is replayed. It shows how endpoints are made and removed, how mail is signed, and what must be refused.

What was installed (the container has no shell, only four static binaries):
  mash         the v0.5.5 release binary, checked against the release's SHA256SUMS
  mashctl      go install github.com/bogworks/mash/cmd/mashctl@v0.5.5
  nats-server  2.15.1 (go install)
  nodeinit     helper that makes directories and keeps the container alive
Release commit: e389eaf807279b58e63b05578a779ef672d93a36

Walk: versions, a local TLS server, two ways to make an endpoint, list and show, status, signed mail, reissue and delete.
Then five things that must be refused. Home is /demo; the container and image are removed at the end.
Commands:
- (none; title screen)

## s02 Step 1: the two binaries and their versions
What you see: `mash version` prints the release number and the full commit, e389eaf. `mashctl version` prints `mashctl 0.0.0-dev`: the version is not stamped into a binary built by `go install ...@v0.5.5`, so it cannot tell you its own release. This is a real finding of this recording, not a typo; the module version v0.5.5 is in the binary's build info, and setup checked it there.
Why: the release binary names its commit, so a bug report can be matched to source. mashctl built this way does not.
Analogy: one tool answers `--version` fully; the other answers like an unlabeled build.
Commands:
- mash version
- mashctl version

## s03 Step 2: a local server, TLS only, with its own CA
What you see: `mashctl init --local` writes a NATS server config, a private CA and a server certificate (valid 10 years), the operator, system and account keys, an admin creds file and config.json, then starts the server (loopback only, TLS). It ends with `Ready: the local server is up at tls://127.0.0.1:24222`. Then three lines of config.json: url is a tls:// URL, owner is randy, tls_ca points at the CA clients trust. Only those three keys are shown; no credential is printed.
Why: mash refuses plain text everywhere, so even a loopback test server needs a certificate. Creating the server also creates the owner (randy) whose account signs every endpoint credential.
Analogy: like generating a CA and a server cert for a VPN gateway before you hand out client configs.
Commands:
- mashctl init --local --owner randy --port 24222 --start
- grep config.json (read from outside: the image has no grep)

## s04 Step 3: an endpoint on another machine (alpha)
What you see: three commands, in the order they would run on two machines. 1) `mash key create` on the endpoint's machine writes a seed (alpha.seed) and prints only the public key, also written to alpha.pub. 2) `mashctl endpoint create randy/demo/alpha --public-key alpha.pub` on the mashctl machine signs that public key into a JWT (randy.demo.alpha.jwt) and records the endpoint. 3) Back on the endpoint's machine, `mash credential create` joins the seed and the JWT into a creds file under /demo/config. Seed and JWT contents are never shown.
Why: the private key never leaves the endpoint's machine; only the public half goes to mashctl, and only the signed JWT comes back.
Analogy: like making a CSR: you keep the private key, the CA sees only the request.
Commands:
- mash key create --out alpha.seed --pub-out alpha.pub
- mashctl endpoint create randy/demo/alpha --public-key alpha.pub --jwt-out randy.demo.alpha.jwt
- mash credential create --seed alpha.seed --jwt randy.demo.alpha.jwt

## s05 Step 4: an endpoint on the same host (beta)
What you see: one command, `mashctl endpoint create randy/demo/beta --make-key`. mashctl first prints the target server, owner and admin creds file it found in config.json, then `created endpoint randy/demo/beta` with its address (randy.demo.beta) and the creds file path. The key was made on this host, so no transfer step is needed.
Why: when mashctl and the endpoint share a machine, one command does what step 3 did in three.
Analogy: ssh-keygen and ssh-copy-id in one go, on a single box.
Commands:
- mashctl endpoint create randy/demo/beta --make-key

## s06 Step 5: list and show
What you see: `endpoint list` shows two endpoints, alpha and beta, each with address, inbox name and creation time. `endpoint show randy/demo/alpha` shows the directory record: owner, group, instance, the account, alpha's public user key, the mail subject agents.mail.randy.demo.alpha, the creds file path, and the issue record `purpose issue ... not revoked`. Empty fields (note, role, host, labels) are simply unset.
Why: the record is the server-side truth of who may send as whom; the issue record is how a revoked key is later shown.
Analogy: like `ip addr` for identities: what exists, and which key is behind each name.
Commands:
- mashctl endpoint list
- mashctl endpoint show randy/demo/alpha

## s07 Step 6: mash status as alpha
What you see: `mash status` checks locally and prints one line per item. name, config (url tls://127.0.0.1:24222, owner randy), creds (exists and readable) and `tls: OK - tls:// connection` are OK. `channel allowlist: MISSING` is a Claude Code plugin setting, irrelevant in this container; `connection` and `delivery` are not checked by the CLI, only a running session reports them. The last line is the version and commit.
Why: status tells you what is configured before you chase a connection error. The MISSING line is expected here and is not a failure.
Analogy: like checking a config file with a linter, not watching the traffic.
Commands:
- MASH_OWNER=randy MASH_GROUP=demo MASH_NAME=alpha mash status

## s08 Step 7: signed mail, both ways
What you see: alpha sends `hello from alpha` to randy.demo.beta and prints `sent #1`. As beta, `mash inbox` lists one message `from randy.demo.alpha [verified]`: the label means alpha's own key signed it. Beta replies; alpha's inbox shows `from randy.demo.beta [verified]`. Each inbox numbers its own mail from #1.
Why: the server stores mail until read, and the signature lets the reader trust the sender without trusting the network.
Analogy: like a mailbox where the sender's wax seal is checked on pickup.
Commands:
- MASH_OWNER=randy MASH_GROUP=demo MASH_NAME=alpha mash send randy.demo.beta "hello from alpha"
- MASH_OWNER=randy MASH_GROUP=demo MASH_NAME=beta mash inbox
- MASH_OWNER=randy MASH_GROUP=demo MASH_NAME=beta mash send randy.demo.alpha "hello back from beta"
- MASH_OWNER=randy MASH_GROUP=demo MASH_NAME=alpha mash inbox

## s09 Step 8: reissue beta's key
What you see: `mashctl endpoint reissue randy/demo/beta --make-key` makes a new key for the same address, writes new creds to the same file and revokes the old key: `revoked user key UC...: 1 server(s) acknowledged`, then `reissued endpoint`. It reminds you to restart the endpoint's sessions so they read the new creds. The address does not change.
Why: rotation without changing who beta is; the old key is cut off on the server, not just replaced.
Analogy: like rotating an SSH key and removing the old one from authorized_keys in the same step.
Commands:
- mashctl endpoint reissue randy/demo/beta --make-key

## s10 Step 9: delete beta; its old credentials stop working
What you see: `endpoint delete` revokes beta's current key, removes its record and inbox, and renames the creds file to `randy.demo.beta.creds.retired-<time>` instead of deleting it. So a plain `mash inbox` as beta fails locally: `credentials: open ... no such file or directory` (exit 1). Pointing MASH_CREDS at the retired file reaches the server, which refuses: `nats: Authorization Violation` (exit 1). `mash status` does NOT say why: it checks local files only, and still reports creds OK and tls OK. `mashctl issued list` shows the reason: both of beta's keys carry a `revoked` time.
Why: a revoked key is refused by the server even though the creds file is still readable on disk.
Analogy: like a revoked client cert that still sits in your home directory: readable, useless.
Commands:
- mashctl endpoint delete randy/demo/beta
- MASH_OWNER=randy MASH_GROUP=demo MASH_NAME=beta mash inbox
- MASH_OWNER=randy MASH_GROUP=demo MASH_NAME=beta MASH_CREDS=<retired file> mash inbox
- MASH_OWNER=randy MASH_GROUP=demo MASH_NAME=beta MASH_CREDS=<retired file> mash status
- mashctl issued list

## n1 This must be refused (1 of 5): a two-part name
Expect: `mashctl endpoint create randy/design --make-key` exits 2 with `error (address_ambiguous): two tokens are ambiguous; write alice/dev/design or --group dev design`. The old owner/name form is not accepted; an endpoint needs owner, group and name.
Why: a two-part name could mean owner/group or group/name; v0.5.5 will not guess.
Commands:
- mashctl endpoint create randy/design --make-key

## n2 This must be refused (2 of 5): no group
Expect: `mashctl endpoint create design --make-key` exits 2 with `error (group_missing): an endpoint needs a group; write --group dev or alice/dev/design`.
Why: every endpoint lives in a group, which is part of its address and of its permissions.
Commands:
- mashctl endpoint create design --make-key

## n3 This must be refused (3 of 5): plain text without the override
Expect: sending with MASH_URL=nats://127.0.0.1:24222 exits 2 before any connection is made, saying every mash connection requires TLS, loopback included, and naming the override `insecure_plaintext (MASH_INSECURE_PLAINTEXT=1)`. The override exists and the message is its warning; we do not use it here, and nothing is sent.
Why: loopback is not trusted either; plain text is an explicit, deliberate opt-in.
Commands:
- MASH_URL=nats://127.0.0.1:24222 MASH_OWNER=randy MASH_GROUP=demo MASH_NAME=alpha mash send randy.demo.beta hi

## n4 This must be refused (4 of 5): another owner's credentials
Expect: a second server for owner bob starts on 24223 with its own config, keys and CA (MASH_CONFIG_DIR and MASH_KEYS_DIR under /demo/bob), and bob/demo/eve gets credentials there. Then eve's creds file is used against randy's server. Two refusals, both exit 1. First, as randy.demo.alpha with eve's creds: refused locally, `the credentials ... do not match endpoint randy.demo.alpha`. Second, as bob.demo.eve with eve's creds, which passes the local check and reaches randy's server: `nats: Authorization Violation`.
Why: the credential is signed by bob's account; randy's server does not trust it. The local check just fails faster.
Analogy: a badge for another company's building: the reader at the door refuses it.
Commands:
- (bob) mashctl init --local --owner bob --dir /demo/server-bob --port 24223 --start
- (bob) mashctl endpoint create bob/demo/eve --make-key
- (bob) mashctl endpoint list
- MASH_OWNER=randy MASH_GROUP=demo MASH_NAME=alpha MASH_CREDS=<eve's creds> mash inbox
- MASH_OWNER=bob MASH_GROUP=demo MASH_NAME=eve MASH_CREDS=<eve's creds> mash inbox

## n5 This must be refused (5 of 5): the removed commands
Expect: `mashctl add alpha2` and `mashctl retire alpha` each exit 2 and name their replacements: `endpoint create ... --public-key` and `endpoint delete`. They do nothing else.
Why: the old verbs are gone, not aliased; a script that still uses them fails loudly and points to the new form.
Commands:
- mashctl add alpha2
- mashctl retire alpha

## s99 That is v0.5.5
What was shown: both binaries' versions (mashctl reports 0.0.0-dev when built with go install), a TLS-only local server, endpoints made by the other-machine path and the same-host path, signed mail marked [verified], reissue and delete with the old key refused, and five cases refused as they should be.
Finding to take away: `mash status` does not report a revoked key; `mashctl issued list` does.
Next: the container and image are removed by name, and the run ends.
Commands:
- (none; close screen)
