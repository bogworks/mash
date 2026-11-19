# mashctl target outputs (0.5.6) - DRAFT2

Status: design mockups, not code. Nothing here was run. Written to the maintainer's
feedback on the v0.5.5 video and the CLI standard's rules; BEFORE blocks are
excerpts of docs/plans/2026-11-19-cli-output-catalog-mashctl.md (v0.5.5 tag).
DRAFT2 applies the rules added during review: a command a person must run stands
alone on its own line (R-paste), a printed fix is true for the broken session
(R-true-fix), a health report gives its verdict first (R-verdict), one name per
thing (R-names). Both parts use the table columns OWNER, GROUP, ENDPOINT (the
name), PATH (the dotted address).

Part 1 is the first-run journey, in the order a new user meets it. Part 2 is every
other mashctl output. Each part ends with its own "Choices to confirm" and "Facts
to confirm".

---

# Part 1: the first-run journey (target output for mashctl 0.5.6), DRAFT2

Design document. Nothing here was run. BEFORE blocks are excerpts of
`docs/plans/2026-11-19-cli-output-catalog-mashctl.md` (v0.5.5), trimmed to the
lines that matter, each marked with the point it breaks (P1..P9, defined in
the catalog). AFTER blocks are the target, exactly as the terminal shows them,
every line 80 columns or fewer (checked by `check.sh` and `check3.sh` in this
directory).

Conventions in the AFTER blocks:

- `NEXT:` (after a success) and `fix:` (after a failure) are the two labels for
  "what to type now". `NEXT:` is coloured on a terminal (a colour readable on
  dark and light backgrounds). With `NO_COLOR` set, or when output is not a
  terminal, it is plain text and the word stays `NEXT:`. A blank line always
  comes before it.
- A command a person must run stands alone on its own line, indented two
  spaces, complete, never wrapped, with no punctuation after it. The sentence
  before it ends with a colon and fits one line. Anything mash cannot know is a
  `<placeholder>`. A document is not a NEXT: it is written `See <path>`.
- A fix that sets configuration names the file with its resolved path first
  (`~/.config/mash/config.json`, or the directory in `MASH_CONFIG_DIR`). An
  environment variable is offered only as the test for one shell.
- The walkthrough is one terminal that you scroll back through. Nothing clears
  the screen between steps.
- Paths are shown with `~` for the user's home directory. The config
  directory is `~/.config/mash` (docs/synadia.md and docs/tls-and-clusters.md).
- Flags the rules rename are used in their new form: `--token-out`,
  `--system-credentials`, `--admin-credentials`, `--make-admin`,
  `mash credential install --key --token`, `mash key create --out bob.key`.
  Labels are called tags (`--tag k=v`) and capabilities `--capability`.
  Suspend and resume are `mashctl endpoint suspend` and `mashctl endpoint
  resume`. `mashctl list` is removed (use `mashctl endpoint list`). Flags the
  rules do not name keep today's spelling unless listed under "Choices to
  confirm".
- Names: endpoint (the thing), name (`bob`), address (the dotted path
  `acme.demo.bob`, header `PATH` in tables; the name's header is `ENDPOINT`),
  group, owner. Part 2 uses the same headers: `OWNER GROUP ENDPOINT PATH`.

Section order: 0 setup, 1 connect mashctl to the server, 2 endpoint bob and
endpoint alice, 3 list and show, 4 run a session and prove it works.

---

## 0. The setup block

Printed at the top of the guide the walkthrough follows (not by a command).
It comes right before the first command.

**BEFORE.** The walkthrough opened with the first command and named no
machine. The maintainer's point 6: "name the machines, setup the environment
off the bat". Today the catalog's own flow names the machine only as "the
host that runs mashctl" and "the endpoint's machine" (P4 rows 3 and 4).

**AFTER**

```text
We are going to walk you through setting up mash between two endpoints on one
machine.

  Host       myserver
  Owner      acme
  Group      demo
  Endpoints  alice and bob

  +--------------------------- myserver ---------------------------+
  |                                                                |
  |    +-------------+            +-----------+                    |
  |    | NATS server |<-----------|  mashctl  |  you run this to   |
  |    +-------------+            +-----------+  create endpoints  |
  |        ^      ^                                                |
  |        |      |                                                |
  |    +-------+ +-------+                                         |
  |    | alice | |  bob  |    each endpoint sends and reads mail   |
  |    +-------+ +-------+                                         |
  |                                                                |
  +----------------------------------------------------------------+

An endpoint is an agent or tool on the mail mesh. Its address is written with
dots, acme.demo.bob: owner acme, group demo, name bob. That is what mash send
takes. mashctl also accepts it as acme/demo/bob.
```

**What changed and why**

- P6 (maintainer point 6): host, group, endpoints and the diagram come before
  the first command, so every later "this machine" has a name.
- Point 2 and rule 3: alice and bob, owner acme, group demo, machine myserver.
- Point 8 (last paragraph asked for the `x/y/z` path explained): one
  paragraph, states the address once as the dotted form and says the slash
  form is accepted as input (see Choices to confirm, item 2).
- Point 4: wrapped by hand at 80 columns or fewer.
- R-names: "Agents" became "Endpoints"; the path is called the address, written
  with dots.

---

## 1. Connect mashctl to the server

mashctl does not install or start NATS (rule 14; maintainer point 1). The
operator who runs the NATS server gives you four things: the server address,
the operator key file, the account key file and the system credentials file,
and the file of the server's certificate authority if the certificate is
private.

### 1a. If there is no server yet: `mashctl init --local`

**BEFORE** (catalog `init`, case 2; the first lines, the rest is 9 more
paths):

```text
$ mashctl init --local --owner acme --port 20001 --start
config dir: /tmp/mc/cfg (set MASH_CONFIG_DIR to change)         <- P8
mashctl: recorded the operator JWT in /tmp/mc/cfg/keys/operator.jwt (mashctl accepts account JWTs issued by the keys it declares)   <- P5 P6
  server          started, pid 63205 (log /tmp/mc/cfg/server/server.log)   <- P9
Ready: the local server is up at tls://127.0.0.1:20001. Create an endpoint with: mashctl endpoint create acme/dev/design --make-key   (it writes the creds file; ...)   <- P4 P5 (248 columns, group dev)
```

**AFTER**

```text
$ mashctl init --local
error: mashctl does not install or start a NATS server.
fix: set one up first, then connect mashctl to it:
  mashctl adopt --help

See docs/guides/nats-local.md
```

Exit code 2 (usage) [confirm: exit 2 or 1]. `--start`, `--dir` and `--port`
get the same message. The guide, the setup script and the skill are named
here: [confirm: the file name of the setup script and the skill].

`mashctl list` is removed in the same way (it showed what `mashctl endpoint
list` shows):

```text
$ mashctl list
error: mashctl list was removed; it showed the same as mashctl endpoint list.
fix: run:
  mashctl endpoint list
```

**What changed and why**

- Maintainer point 1 and rule 14: mashctl no longer makes a server, a
  certificate, keys under `/demo`, or a process. The message is the refusal
  form (rule 10) and names the guide as `See <path>` (R-doc), with the command
  to run next on its own line (R-paste).
- P4, P5, P8, P9: gone with the command.
- Consistency decision: `mashctl list` is removed; the message names
  `mashctl endpoint list`.

### 1b. `mashctl adopt`

**BEFORE** (catalog `adopt`, case 8, real run; the check lines are 80 to 190
columns, the closing paragraph is 330):

```text
$ mashctl adopt --server tls://127.0.0.1:20001 --operator-key /tmp/mc/adopt-src/operator.nk --account-key /tmp/mc/adopt-src/account.nk --sys-creds /tmp/mc/adopt-src/sys.creds --owner acme --account MASH --mint-admin   <- P5
mashctl adopt: checking tls://127.0.0.1:20001 as owner "acme", account "MASH" (read-only: nothing is written until every check passes)   <- P5
  1. files          ok: operator key OAQCFPM5BOL22AGI6O76QGKIQHRLYN7DZZEATDRWD6QTEC5462SVCBJU, account key AD7BK6VI6EGIDZ2XXCLTKUBGR5NOIJYVZ77DTRETXNW2XXPNQTK2UGO6, system creds (modes 0600, seeds not printed)   <- P5 P6 P8
  2. account        ok: "MASH" found on the resolver, subject = the account key, not expired. Not visible from here: whether the operator has revoked it   <- P6
  3. jetstream      ok: the account JWT gives it: disk 2GiB, streams 64, consumers 128   <- P6 P9
  4. system user    ok: the ping was answered (standalone, 1 server(s) replied) and the claims lookup worked. The claims update (push) permission is not exercised here: `mashctl endpoint delete` is the first to use it   <- P5 P6 P9
  5. operator key   ok: it issued the account JWT (issuer OAQCFPM5BOL22AGI6O76QGKIQHRLYN7DZZEATDRWD6QTEC5462SVCBJU), whose signature is valid   <- P8
  6. admin creds    ok: connected (minted)
  6. jetstream      ok: the server answered for the account: 9 stream(s), 4 consumer(s), 22530B of disk in use   <- P9
All checks passed.
  operator key OAQC...CBJU imported in /tmp/mc/cfg2/keys   <- P8 (key id repeated from step 1)
  recorded the admin user in the issue record (bucket mash-issued)   <- P6
  write /tmp/mc/cfg2/config.json with "url": "tls://127.0.0.1:20001", "owner": "acme", "admin_creds": "/tmp/mc/cfg2/server/mashadmin.creds", "tls_ca": "/tmp/mc/cfg/server/ca.pem"   <- P5 P9
Next:   <- P4 (after a blank line, two items; item 2 names provision, with a placeholder)
  1. mashctl init                                  the directory and lease buckets (needs the admin creds)   <- P5 P6
  2. mashctl provision owner.group.instance ...    declare an endpoint (see mashctl help)   <- P4
What adopt does not do: it changes nothing on the server, except that --mint-admin records the minted mashadmin user in the issue record ... (330 columns)   <- P5 P6 P7
```

**AFTER**

```text
$ MASH_TLS_CA=ca.pem mashctl adopt --server tls://myserver:4222 \
    --owner acme --account MASH --make-admin \
    --operator-key operator.nk --account-key account.nk \
    --system-credentials system.creds

Checking tls://myserver:4222 as owner acme. Nothing is written until all the
checks pass.

  1 files     ok  The operator key, account key and system credentials files
                  are there, and only you can read them.
  2 server    ok  The server turns away a connection with no credentials and
                  accepts the system credentials. Only operator-signed
                  accounts can use it.
  3 account   ok  Account MASH exists on the server and has not expired.
  4 storage   ok  The account may store messages (2 GiB). mash needs this to
                  keep mail until it is read.
  5 system    ok  The system credentials can list the server and look up
                  accounts.
  6 operator  ok  The operator key signed this account, so you hold the
                  authority to manage it.
  7 admin     ok  The admin credentials that mashctl will use connect, and
                  the server confirms storage is on for the account.

Adopted tls://myserver:4222 as owner acme.

  Saved the keys, the admin credentials and the settings in ~/.config/mash.
  On the server, the only change is a record of the new admin credentials.

NEXT: on myserver, prepare the server for mash:
  mashctl init
```

When a check fails, the run stops at that line:

```text
  3 account   FAILED  Account MASH is not on the server.

error: account MASH was not found on tls://myserver:4222.
fix: ask your operator for the account name and pass it as --account <name>
```

When the address is `nats://` (plain text):

```text
$ mashctl adopt --server nats://myserver:4222 --owner acme ...
error: nats://myserver:4222 is not encrypted, and mash only connects over TLS.
fix: use a tls:// address. Your operator can give you the certificate file.
```

Exit code 2 for the plain-text refusal (rule 10, R-exit). `--detail` adds the
key ids and the file paths of each check; `--dry-run` prints "Would ..." lines
and ends "Nothing was changed." (rule 11).

adopt reports its checks one line at a time as it runs, so its verdict is the
last line (`Adopted ...`, or `error:` after a `FAILED` line), not the first.
The verdict-first rule (R-verdict) holds for `mashctl status`, which has all
its answers before it prints.

**What changed and why**

- P5 (maintainer point 4): the command is wrapped with `\`, every output line
  is 80 columns or fewer, no line carries a path longer than `~/.config/mash`.
- P6 (point 8, point 16): each check is one line of meaning in plain words. No
  JWT, seed, resolver, JetStream, bucket, lease, issue record.
- P8 and P9: the three 56-character key ids, the file modes and the resource
  counts (streams, consumers) are gone from the default output; they are in
  `--detail`.
- P7: the closing paragraph "What adopt does not do" is replaced by the one
  true sentence for this run: the server change is the admin record.
- P4 (point 11): the `Next:` list of two items, with the `provision`
  placeholder, becomes one `NEXT:` with the command on its own line. The
  command is the next step in the guide and runs on myserver, the host mashctl
  read from the address. `provision` is not next in the guide.
- P1 (point 14): adopt printed its own header already; it now says only what
  it is checking and why nothing is written yet.
- Rule 10: both refusals use `error:` and `fix:`. The account fix names the
  flag with a `<name>` placeholder (mash does not know the account).
- Renamed flags (see Choices to confirm, item 3): `--system-credentials`,
  `--make-admin`.

### 1c. `mashctl init`

**BEFORE** (catalog `init`, case 3):

```text
$ mashctl init
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)   <- P1 P5 P6
mashctl: directory and lease buckets ready   <- P6 (the user is not told what these are)
```

**AFTER**

```text
$ mashctl init

Prepared tls://myserver:4222 for mash.

  mash now has room on the server for:
    - the list of endpoints (what `mashctl endpoint list` shows)
    - the record of every token mash signs
    - the sessions that are running

NEXT: on the machine where the endpoint will run, create its key:
  mash key create --out <name>.key
```

Run again on a server that is ready:

```text
$ mashctl init
Already prepared: tls://myserver:4222. Nothing was changed.
```

[confirm: init is safe to run twice and says nothing changed; the catalog shows
"ready" both times].

**What changed and why**

- P1 (point 14): the three-part preamble is gone; the server is named in the
  result line.
- P6: "directory and lease buckets" becomes the three things they hold, in plain
  words. [confirm: what the lease storage holds; written here as "the sessions
  that are running"].
- P4 (point 11): the old output had no NEXT. The new one names the next step
  in the guide, which is Step 1 of the endpoint flow. mashctl does not know
  the endpoint's name, so the command carries `<name>`.
- Rule 8: what happened, then what it means.
- R-paste: the "Add --explain" sentence is not in the NEXT; `--explain` is in
  `--help` (see Choices to confirm, item 17).

### 1d. `mashctl status` (new)

Shows what the preamble showed on every command, on request, in plain words.
It describes the administrator side: which server mashctl talks to and as
whom. It is not `mash status`: that command is about one endpoint (bob), its
credentials and whether it can reach the server. `mashctl status` knows no
endpoint.

The first line is the verdict, in words that read without colour: `OK`,
`PROBLEM` or `NOT REACHABLE`. Then only the rows that were asked for (a plain
run asks for the facts the old preamble gave) or that are wrong. After a
problem, `fix:` and the command. Exit code 0 on `OK`, 1 on `PROBLEM` or `NOT
REACHABLE`. On a terminal the verdict word is coloured (green, red, yellow);
the capitals carry the meaning without it.

**BEFORE.** No such command. Today the same facts arrive as the line before
every result (catalog P1):

```text
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)   <- P1 P5 P6
```

**AFTER, short form**

```text
$ mashctl status
OK

server:     tls://myserver:4222 (reachable, encrypted)
owner:      acme
signed in:  as admin for owner acme
prepared:   yes
config:     ~/.config/mash/config.json
```

The server was adopted and `mashctl init` was not run yet:

```text
$ mashctl status
PROBLEM: the server is not prepared for mash.

server:     tls://myserver:4222 (reachable, encrypted)
prepared:   NO

fix: prepare the server, on myserver:
  mashctl init
```

The server cannot be reached; the command says so and names the address:

```text
$ mashctl status
NOT REACHABLE: tls://myserver:4222 refused the connection.

fix: start the NATS server on myserver, then check again:
  mashctl status
```

**AFTER, `--detail`**

```text
$ mashctl status --detail
OK

mashctl:             0.5.6
server:              tls://myserver:4222 (reachable, encrypted)
certificate:         checked against ~/.config/mash/ca.pem
owner:               acme
account:             MASH
signed in:           as admin for owner acme
prepared:            yes

config:              ~/.config/mash/config.json
key directory:       ~/.config/mash/keys
admin credentials:   ~/.config/mash/server/mashadmin.creds
system credentials:  ~/.config/mash/server/sys.creds

operator key:        OAQCFPM5BOL22AGI6O76QGKIQHRLYN7DZZEATDRWD6QTEC5462SVCBJU
account key:         AD7BK6VI6EGIDZ2XXCLTKUBGR5NOIJYVZ77DTRETXNW2XXPNQTK2UGO6
```

**What changed and why**

- P1 (point 14): the preamble becomes a command you run when you want it. The
  facts are the same: server (from `url`), owner, the credentials in use, the
  config location. The credentials file path moves to `--detail`; the short
  form says "signed in: as admin for owner acme".
- R-verdict: line 1 is `OK`, `PROBLEM: ...` or `NOT REACHABLE: ...`. A problem
  prints only the rows that are wrong, and `fix:` with the command on its own
  line. The version moved to `--detail` (a version is not a check).
- Point 17 (the model of the status screen): short form lists only what a
  person needs; `--detail` is for the person who wants the paths and key ids.
- R-nextcase: `status` after a failure prints `fix:`; it prints no NEXT on
  success.
- [confirm: whether `mashctl status` connects to the server to test it and
  to find out whether init ran; the catalog shows only the config values].
- Difference from `mash status` (another author): that one is run as an
  endpoint, names it (acme.demo.bob) and checks the endpoint's own
  credentials, in the same verdict-first shape. `mashctl status` shows what
  `config.json` and the admin credentials say about the server mashctl
  administers, and never names an endpoint.
- Width: `system credentials:` is the longest label, 19 columns, so
  `--detail` pads to 19 plus two spaces and the values start in column 22.

---

## 2. Endpoint bob (three steps, other-machine path) and endpoint alice

The three steps are shown in long form first (what `--explain` prints), then
once in short form (what a plain run prints). In the walkthrough everything
runs on myserver, so the copy steps in the NEXT lines are skipped; on two
machines they apply [Choices to confirm, item 4].

Short vocabulary used by all three steps (rule 7): private key, public key,
token (the statement your mash server signs: "this public key is endpoint
acme.demo.bob"), credentials (the one file mash runs with: private key plus
token).

mash and mashctl print `<placeholder>` where they do not know the value. After
`mash key create` the owner and group are known only if the config names them;
the mockups show the case where it does not.

### Step 1 of 3 (long form): `mash key create --out bob.key --explain`

**BEFORE** (catalog Appendix A, `mash key create`, case 2):

```text
$ mash key create --out /tmp/mc/work/bob.seed
created key
  public key  UAO35ZE32HBKAMOGS4CYOJCJWGWGGEGGDEWOLR5Q2L47QFBJMQJRU3CC   <- P8 P9
  seed        /tmp/mc/work/bob.seed   <- P6
  public file /tmp/mc/work/bob.pub
next: send /tmp/mc/work/bob.pub to the host that runs mashctl, then: mashctl endpoint create ADDRESS --public-key /tmp/mc/work/bob.pub   <- P4 P5 (placeholder ADDRESS, no step number, no why)
```

**AFTER**

```text
Step 1 of 3: create a key for your agent (on the agent's machine)

This makes a private key (NATS calls it a seed) for bob. Its public half is
used in the next step to create bob's endpoint on the mail mesh. The private
key stays on this machine. If someone else runs your mash server, send them
bob.pub; if mashctl is on another machine, copy bob.pub there.

$ mash key create --out bob.key

Created a key pair.

  private key  bob.key  (keep it secret; only you can read it)
  public key   bob.pub  (safe to share)

NEXT: on the machine that runs mashctl (copy bob.pub there first):
  mashctl endpoint create <owner>/<group>/bob --public-key bob.pub
```

**What changed and why**

- P4 (point 8, the maintainer's own wording): header "Step 1 of 3", why
  paragraph, blank line, `$ command`, output, blank line, coloured NEXT. The
  NEXT names the machine that runs the next command and the copy.
- P6 (point 3): "private key (NATS calls it a seed)" appears once, here.
  Everything after says private key.
- P8 and P9: the 56-character public key is not printed (it is in
  `bob.pub`); `--detail` prints it.
- Point 8 first bullet: it says what is happening and why, without keys,
  NATS or JWT knowledge.
- Rule 9: the `$ command` line is printed by `--explain` without the
  `--explain` flag so a reader can copy it.
- R-paste: the NEXT sentence ends with a colon on one line; the command is
  on its own line. `<owner>/<group>` are placeholders, because mash key create
  was not told them. The managed-setup sentence moved into the why paragraph.
- R-names: "Created a key pair" (the verb for things created; no owner is
  claimed from a file name).

### Step 2 of 3 (long form): `mashctl endpoint create ... --explain`

**BEFORE** (catalog `endpoint create`, case 6):

```text
$ mashctl endpoint create acme/team/bob --public-key /tmp/mc/work/bob.pub --jwt-out /tmp/mc/work/bob.jwt
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)   <- P1 P5 P6
created endpoint acme/team/bob
  address  acme.team.bob   <- P2 P8
  jwt      /tmp/mc/work/bob.jwt   <- P6
next: on the endpoint's machine, run: mash credential create --seed bob.seed --jwt bob.jwt   <- P4 P5 P6
```

**AFTER**

```text
Step 2 of 3: create the endpoint (on the machine that runs mashctl)

This creates the endpoint acme.demo.bob on the mail mesh, so other endpoints
can send to it. The address is owner.group.name: acme owns the server, demo
is the group bob mails within, and bob is the name. You typed it as
acme/demo/bob, which mashctl also accepts.

mashctl signs bob's public key. The result is bob's token: the statement your
mash server signs that "this public key is endpoint acme.demo.bob". The
server accepts bob only with a key and a token that go together.

$ mashctl endpoint create acme/demo/bob --public-key bob.pub

Created endpoint acme.demo.bob on tls://myserver:4222.

  Signed bob's public key.
  Created the token acme.demo.bob.token (not secret).

NEXT: on bob's machine (copy acme.demo.bob.token there first):
  mash credential install --key <key file> --token acme.demo.bob.token
```

**What changed and why**

- P1 (point 14): the preamble is gone; the server is named in the result
  line.
- P2 and P8 (points 13, 16): the endpoint is printed once, as
  `acme.demo.bob`. The slash form was typed; it is not echoed back.
- P6 (points 3, 8): `jwt` becomes `token`, with its one-sentence meaning in
  the why paragraph. The flag is `--token-out` (default `acme.demo.bob.token`
  in the current directory).
- P4 (point 8, point 11): the old NEXT named a command with `bob.seed` and
  `bob.jwt`, which the user has to translate. The new NEXT uses the token file
  name mashctl wrote. The key file is `<key file>`: mashctl cannot see where
  bob's private key is.
- Point 8 (the request to explain `randy/demo/beta`): the first paragraph
  does it, in one sentence of meaning and one of use.
- R-names: "Created the token" (was "Wrote").

### Step 3 of 3 (long form): `mash credential install ... --explain`

**BEFORE** (catalog Appendix A, `mash credential create`, case 8):

```text
$ mash credential create --seed /tmp/mc/work/bob.seed --jwt /tmp/mc/work/bob.jwt --out /tmp/mc/work/bob.creds
created credentials
  public key  UAO35ZE32HBKAMOGS4CYOJCJWGWGGEGGDEWOLR5Q2L47QFBJMQJRU3CC   <- P8 P9
  creds       /tmp/mc/work/bob.creds   <- P6
next: mash status   <- P4 (mash status prints no next line, and does not start a session)
```

**AFTER**

```text
Step 3 of 3: install the credentials (on the agent's machine)

This joins bob's private key and the token from Step 2 into credentials: the
one file mash runs with. It checks that the token was made for this key, and
writes acme.demo.bob.creds to mash's config directory. The private key does
not leave this machine.

$ mash credential install --key bob.key --token acme.demo.bob.token

Installed credentials for acme.demo.bob.

  File: ~/.config/mash/acme.demo.bob.creds (keep it secret)

NEXT: check that bob can sign in, on this machine:
  MASH_OWNER=acme MASH_GROUP=demo MASH_NAME=bob mash status

Or add another endpoint, starting on its machine:
  mash key create --out <name>.key
```

**What changed and why**

- P6 and the question in point 8 ("one credential a key and the other a
  credential"): `mash credential create --seed --jwt` becomes
  `mash credential install --key --token`. The why paragraph says what the
  file is made of. "Install" says the file goes into the config directory.
- P4 (point 11): `next: mash status` is replaced by `mash status` with the
  identity filled in (mash knows acme.demo.bob from the token), so the pasted
  line works in a shell that has not exported anything. The second item is
  the other thing a person may do next (the walkthrough makes alice before it
  starts a session). Rule 6 allows a short list when there is a real choice.
- R-true-fix: the variables on that line are a test for one command, not the
  fix; the way to run a session is in section 4.
- P8 and P9: no public key, no repeated path.
- Point 10 ("why don't I need a beta.seed"): see alice below, where the same
  three lines show one command doing all three steps.

### The three steps in short form (what a plain run prints)

Each command prints this when run without `--explain`.

**AFTER**

```text
$ mash key create --out bob.key

Created a key pair.

  private key  bob.key  (keep it secret; only you can read it)
  public key   bob.pub  (safe to share)

NEXT: on the machine that runs mashctl (copy bob.pub there first):
  mashctl endpoint create <owner>/<group>/bob --public-key bob.pub
```

```text
$ mashctl endpoint create acme/demo/bob --public-key bob.pub

Created endpoint acme.demo.bob on tls://myserver:4222.

  Signed bob's public key.
  Created the token acme.demo.bob.token (not secret).

NEXT: on bob's machine (copy acme.demo.bob.token there first):
  mash credential install --key <key file> --token acme.demo.bob.token
```

```text
$ mash credential install --key bob.key --token acme.demo.bob.token

Installed credentials for acme.demo.bob.

  File: ~/.config/mash/acme.demo.bob.creds (keep it secret)

NEXT: check that bob can sign in, on this machine:
  MASH_OWNER=acme MASH_GROUP=demo MASH_NAME=bob mash status

Or add another endpoint, starting on its machine:
  mash key create --out <name>.key
```

**What changed and why**

- Rule 9: the short form keeps the what-happened lines and the NEXT; it drops
  the step header and the why paragraph, which `--explain` brings back.
- Rule 8: result, then up to three lines.

### Alice on the same machine: `mashctl endpoint create acme/demo/alice --make-key`

**BEFORE** (catalog `endpoint create`, case 4):

```text
$ mashctl endpoint create acme/team/alice --make-key
created endpoint acme/team/alice
  address  acme.team.alice   <- P2 P8
  creds    /tmp/mc/cfg/acme.team.alice.creds   <- P6
next: start a session as this endpoint: MASH_OWNER=acme MASH_GROUP=team MASH_NAME=alice   <- P4 P5 (directly under the creds line; names variables, no command)
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)   <- P1 P5 P6
```

**AFTER, short form**

```text
$ mashctl endpoint create acme/demo/alice --make-key

Created endpoint acme.demo.alice on tls://myserver:4222.

  1. Created a private key for alice on this machine.
  2. Created her token, signed by your server.
  3. Installed credentials in ~/.config/mash/acme.demo.alice.creds.

NEXT: check that alice can sign in, on this machine:
  MASH_OWNER=acme MASH_GROUP=demo MASH_NAME=alice mash status

Or add another endpoint on this machine:
  mashctl endpoint create acme/demo/<name> --make-key
```

**AFTER, long form (`--explain`)**

```text
This does all three steps in one command, on one machine. Use it when the
endpoint runs on the same machine as mashctl. alice's private key is made here
and never written anywhere except her credentials file.

Step 1 of 3: create a key for your agent (on the agent's machine)

  This makes a private key (NATS calls it a seed) for alice. Its public half
  is signed in Step 2.
  Done: created a private key for alice.

Step 2 of 3: create the endpoint (on the machine that runs mashctl)

  This creates the endpoint acme.demo.alice, which you typed as
  acme/demo/alice. mashctl signs alice's public key, which makes her token:
  the statement your mash server signs that "this public key is endpoint
  acme.demo.alice".
  Done: created the token, endpoint acme.demo.alice exists on
  tls://myserver:4222.

Step 3 of 3: install the credentials (on the agent's machine)

  This joins the private key and the token into credentials: the one file
  mash runs with.
  Done: installed ~/.config/mash/acme.demo.alice.creds (keep it secret).

NEXT: check that alice can sign in, on this machine:
  MASH_OWNER=acme MASH_GROUP=demo MASH_NAME=alice mash status

Or add another endpoint on this machine:
  mashctl endpoint create acme/demo/<name> --make-key
```

**What changed and why**

- Point 10 ("why don't I need a beta.seed", "is the alpha.seed also the
  operator creds"): the three numbered lines show the three steps happening
  one after the other, with the private key made here. No other file is
  mentioned. The admin credentials are never named.
- P1, P2, P4, P6, P8: as for bob. The old NEXT sat under the output, named
  only variables and no command; the new one is separated, coloured and
  names who and where, with the command on its own line.
- Rule 9: in the short form each line is one step, in the order of the
  three-step flow. The long form wraps those lines in the same step headers
  as bob's.
- The long form has no `$ command` echo per step, because there is one
  command; it is typed once above the output.
- R-names: each step line starts with "Created" or "Installed".

---

## 3. `mashctl endpoint list` and `mashctl endpoint show`

### `mashctl endpoint list`

**BEFORE** (catalog `endpoint list`, case 4; the maintainer's screenshot
SCR-20261119-brtu shows the same shape):

```text
$ mashctl endpoint list
acme/team/alice  acme.team.alice  acme_team_alice  created 2026-11-19T00:21:38Z   <- P2 P3 P8
acme/team/alice2  acme.team.alice2  acme_team_alice2  created 2026-11-19T00:21:38Z   <- P3 (columns start in different places)
acme/team/carol  acme.team.carol  acme_team_carol  created 2026-11-19T00:21:50Z, note "demo tool", labels env=demo,kind=tool   <- P5 P6 P9
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)   <- P1 P5 P6
4 endpoints
```

The preamble and the count are on stderr, the rows on stdout (catalog P1 note).

**AFTER**

```text
$ mashctl endpoint list

OWNER  GROUP  ENDPOINT  PATH             CREATED (UTC)
acme   demo   alice     acme.demo.alice  2026-11-19 00:21
              bob       acme.demo.bob    2026-11-19 00:22

2 endpoints
```

```text
$ mashctl endpoint list --wide

OWNER  GROUP  ENDPOINT  PATH             TYPE   HOST      CREATED (UTC)     NOTE
acme   demo   alice     acme.demo.alice  agent  myserver  2026-11-19 00:21  -
              bob       acme.demo.bob    agent  myserver  2026-11-19 00:22  -

2 endpoints
```

When another owner or group is present, the owner and the group are written on
their first row only, as kubectl does:

```text
$ mashctl endpoint list

OWNER  GROUP  ENDPOINT  PATH              CREATED (UTC)
acme   demo   alice     acme.demo.alice   2026-11-19 00:21
              bob       acme.demo.bob     2026-11-19 00:22
       prod   deploy    acme.prod.deploy  2026-11-19 00:30

3 endpoints
```

With no endpoints:

```text
$ mashctl endpoint list

No endpoints yet.

NEXT: create one for an endpoint on this machine:
  mashctl endpoint create acme/<group>/<name> --make-key

Or for an endpoint on another machine, start there:
  mash key create --out <name>.key
```

Removed command, see 1a: `mashctl list` prints the refusal that names
`mashctl endpoint list`.

**What changed and why**

- P1 (point 14): no preamble. The count (`2 endpoints`) is on stdout, after
  the table, so a pipe sees it too.
- P2 and P8 (points 13, 16): one address form per row, `acme.demo.alice`,
  the one `mash send` takes. The slash form and the storage name are gone;
  the storage name is in `show --detail`.
- P3 (point 12, point 14): header row, aligned columns, grouped like
  kubectl (owner and group once, blank for the rows under them). The table
  looks like the maintainer's sketch, in the same column order.
- P9 and P6: note, host and type go behind `--wide`; the words `labels` and
  `kind` are replaced by `TYPE` (agent or tool). Tags are in `show`.
- Rule 5: dates as `2026-11-19 00:21`, the header says `CREATED (UTC)`.
- P4: the empty case named `--help` only; it now names the command that
  makes an endpoint, in the same one-command-per-line form as every NEXT.
- R-names: the table has the columns `OWNER GROUP ENDPOINT PATH`. `ENDPOINT`
  is the endpoint's name (`bob`) and `PATH` is the dotted address
  (`acme.demo.bob`), the one `mash send` takes. Part 2's grouped tables use
  the same `ENDPOINT` header for the name, so the two parts now agree. The
  earlier `NAME` header and the old `ENDPOINT` header for the dotted address
  are gone.
- `--wide` width: the header and rows with `HOST` and `NOTE` are 77 columns
  for two rows; a longer note is cut to the screen width with `...`.

### `mashctl endpoint show acme.demo.bob`

**BEFORE** (catalog `endpoint show`, case 3):

```text
$ mashctl endpoint show acme/team/alice
acme/team/alice  acme.team.alice   <- P2
  owner:        acme   <- P8
  group:        team   <- P8
  instance:     alice   <- P8
  agent:        claude-code   <- P9
  account:      AD7BK6VI6EGIDZ2XXCLTKUBGR5NOIJYVZ77DTRETXNW2XXPNQTK2UGO6   <- P9
  user key:     UAGT4ZQXALANXPHIAET7WXOZQRCNHQWDUU2UMRFXF23KU6FJQVUH7KOQ   <- P9
  mail subject: agents.mail.acme.team.alice   <- P9
  created:      2026-11-19T00:21:38Z
  note:         
  role:         
  host:         
  description:  
  caps:         
  labels:       
  creds file:   /tmp/mc/cfg/acme.team.alice.creds   <- P6
  issue record: purpose issue, issued 2026-11-19T00:21:38Z, not revoked   <- P6 P8
mashctl: target server ... (the same preamble)   <- P1 P5 P6
```

Eight of the sixteen lines are empty fields or repeat what the first line
says (P8, P9).

**AFTER, short form**

```text
$ mashctl endpoint show acme.demo.bob

Endpoint acme.demo.bob

created:      2026-11-19 00:22 UTC
type:         agent
token:        valid, not revoked
credentials:  ~/.config/mash/acme.demo.bob.creds
```

When bob's credentials are not on this machine, the last line reads
`credentials:  not on this machine`. A note, host, description or tags
(`tags:  env=demo`) appear as one more line only when set. A suspended
endpoint adds `status:  SUSPENDED` (set by `mashctl endpoint suspend`, undone
by `mashctl endpoint resume`).

**AFTER, `--detail`**

```text
$ mashctl endpoint show acme.demo.bob --detail

Endpoint acme.demo.bob

owner:         acme
group:         demo
name:          bob
slash form:    acme/demo/bob
storage name:  acme_demo_bob
mail subject:  agents.mail.acme.demo.bob
agent:         claude-code
created:       2026-11-19 00:22 UTC
note:          -
role:          -
host:          -
description:   -
tags:          -
capabilities:  -

account key:   AD7BK6VI6EGIDZ2XXCLTKUBGR5NOIJYVZ77DTRETXNW2XXPNQTK2UGO6
public key:    UAO35ZE32HBKAMOGS4CYOJCJWGWGGEGGDEWOLR5Q2L47QFBJMQJRU3CC
token:         signed 2026-11-19 00:22 UTC, purpose issue, not revoked
credentials:   ~/.config/mash/acme.demo.bob.creds
```

**What changed and why**

- P1 (point 14): no preamble.
- P2 and P8 (points 13, 16): the title says the endpoint once; owner, group
  and name are not repeated on the default screen. In `--detail` the other
  forms appear under labels (`slash form`, `storage name`) so they are not
  an unexplained triple.
- P9 (point 16, "most of this detail could be shoved into --detail"): the
  default form has four lines: created, type, token, credentials. Empty
  fields are not printed in the default form. Account key, public key, mail
  subject and agent move to `--detail`.
- P6: `issue record: purpose issue ... not revoked` becomes `token: valid,
  not revoked`; `creds file` becomes `credentials`; capabilities and tags are
  printed only when set (and as `-` in `--detail`).
- `--detail` fills empty fields with `-`.
- Rule 5: `created` as `2026-11-19 00:22 UTC`.
- R-names: the directory key is called the storage name; no flat form is in
  the default output.

---

## 4. Run a session as bob, and prove it works

### The start-of-session steps, as plain steps

This is where the walkthrough goes after Step 3 and after alice. It is guide
text, not output of a mashctl command.

**BEFORE** (the line from `endpoint create`, catalog flow table row 2,
marked P4): `next: start a session as this endpoint: MASH_OWNER=acme
MASH_GROUP=team MASH_NAME=alice`. It named variables, no command, and the
next screen in the video was another command (the maintainer's point 11).

**AFTER**

```text
Run a session as bob (on myserver)

Step 1. Open a terminal on myserver, in the folder bob works in.

Step 2. Tell mash who this session is. mash reads it from
~/.config/mash/config.json ("owner", "group" and "name"). That file names one
endpoint, so alice and bob on one machine cannot both use it. Instead, set
the variables in the terminal each session starts from:
  export MASH_OWNER=acme MASH_GROUP=demo MASH_NAME=bob

Step 3. Start Claude Code in that same terminal, with the mash plugin
installed [confirm: the exact start command and the plugin install step].

Step 4. mash signs in with bob's credentials, the file
~/.config/mash/acme.demo.bob.creds.

For alice, open a second terminal and set MASH_NAME=alice instead.
```

**What changed and why**

- Point 11: the step that follows Step 3 is this one, in the order a user
  does it, with the exact variables for bob.
- R-true-fix: the file that holds the identity is named first, with the
  resolved path. The `export` line is offered because the walkthrough has two
  endpoints on one machine; it is true there because Claude Code starts from
  the same terminal. On a machine with one endpoint, the file is the fix.
- The three names (`MASH_OWNER`, `MASH_GROUP`, `MASH_NAME`) are set once;
  they match the address (acme.demo.bob).
- Point 18 and rule 15: the session uses the mash tools (MCP). The CLI
  commands in the next part are for the person.

### `mash send`, `mash inbox`, `mash read`: the proof

These belong to the `mash` commands another author is designing. They are
shown only as the commands the user types and the effect they have.
`[mash output: see the mash targets file]`

```text
# in alice's session
$ mash send acme.demo.bob "hello bob"
```
Effect: one line saying the message was sent to acme.demo.bob.
`[mash output: see the mash targets file]`

```text
# in bob's session
$ mash inbox
```
Effect: lists one message, #1, from acme.demo.alice, marked as signed by
alice's own key (catalog: `[verified]`).
`[mash output: see the mash targets file]`

```text
# in bob's session
$ mash read 1
```
Effect: prints the message "hello bob", quoted, with its sender.
`[mash output: see the mash targets file]`

Facts from the catalog (Appendix A, `mash send`, `mash inbox`, `mash read`):
`mash send ADDRESS "text"` takes the dotted address; `mash inbox` lists
messages by number; `mash read N` reads one by number; an unknown endpoint
exits 1 with `mash: no endpoint named "..."`.

---

## Choices to confirm

1. The setup block and the long-form text are guide text. The setup block is
   printed by the guide (or the video script), not by a command, so no
   command prints "We are going to walk you through ...". Reasoning: the
   rules give no command for it, and `mashctl status` is not the place.
2. The path explanation sits in the setup block (one paragraph) and again in
   Step 2's why paragraph. The address is the dotted form; the slash form is
   input only and is named as such. Reasoning: point 8 asks for it at Step 2;
   point 6 asks for it at the start. Review 2 (problem 5) asked that mash not
   print two spellings on one screen; mashctl commands in NEXT lines still
   use the slash form because that is the form the rules decided mashctl takes.
   If mashctl is made to take the dotted form too, every `<owner>/<group>/bob`
   in a NEXT becomes `<owner>.<group>.bob`.
3. Flag renames beyond the rules: `--sys-creds` becomes
   `--system-credentials`, `--admin-creds` becomes `--admin-credentials`
   (not shown), `--mint-admin` becomes `--make-admin`. These are decided in
   the sibling document and adopted here. Labels are tags (`--tag k=v`) and
   capabilities `--capability`; suspend and resume are `mashctl endpoint
   suspend|resume`.
4. The other-machine flow shows the machine name only in the NEXT lines
   ("on bob's machine", "on the machine that runs mashctl"), because the
   setup block says one machine. A reader on two machines sees the copy
   sentences; on one machine they skip them. Reasoning: the maintainer's
   point 6 wants one machine; the task asks for the other-machine path in full.
5. After Step 3 and after alice the NEXT is one command (check the endpoint
   signs in) and a second, "Or add another endpoint", separated by a blank
   line. Reasoning: the walkthrough makes alice before a session starts, so a
   single NEXT to the session would be untrue on the screen after bob
   (point 11). The check line puts the identity on the command so it works in
   a shell that has exported nothing.
6. `--explain` on `mashctl endpoint create ... --make-key` prints one opening
   paragraph and three step blocks, each ending `Done: ...`, with no `$`
   echo per step. Reasoning: there is one command, so one `$`; the rules
   describe the three-command case only.
7. In the grouped list, a long address is not truncated; every row is padded
   to the longest address (third example). Reasoning: a cut address cannot be
   used in `mash send`.
8. `mashctl endpoint list` counts on stdout (`2 endpoints`), after a blank
   line. Reasoning: the catalog puts it on stderr, which a pipe loses.
9. The default `show` prints four lines and `--detail` prints the rest;
   `show` omits empty fields by default and `--detail` prints them as `-`.
10. `mashctl status` prints the verdict first (`OK`, `PROBLEM`, `NOT
    REACHABLE`), then `label: value` lines with a colon like the maintainer's
    `mash status` sketch (point 17). Exit 0 on `OK`, 1 otherwise. A plain-run
    `OK` still prints the facts the old preamble gave, because running the
    command asks for them.
11. `mashctl status` prints a `PROBLEM` and a `fix:` only when init has not
    run; with nothing wrong it prints no NEXT and no fix. Reasoning: rule 6.
12. `mashctl init` run twice prints "Already prepared ... Nothing was
    changed."
13. The `.creds` file extension and the `.token` extension are kept (rules
    3 and the flow); `.key` and `.pub` for bob's key pair. The word
    "credentials" is used for the file everywhere in prose.
14. The init message names its three storage items as "the list of
    endpoints, the record of every token mash signs, the sessions that are
    running". The catalog names a directory, an issue record bucket and
    lease buckets; the third mapping is a guess (see Facts to confirm).
15. adopt prints its seven checks by default (one line of meaning each) and
    puts key ids behind `--detail`. Reasoning: the maintainer wants to know
    what each check means; the ids tell a person nothing. Its verdict is the
    last line, because the checks run in order; `mashctl status` is
    verdict-first.
16. `mash key create` prints the file names and does not print the public
    key. Reasoning: points 16 and 17 (nothing the user did not ask for).
17. The NEXT lines no longer say "Add --explain to see each step explained".
    Reasoning: R-paste allows one sentence and one command per NEXT; `--explain`
    is listed in `--help` and in the walkthrough.
18. Exit code of the plain-URL refusal is 2 in `adopt` (contract REQ-014).
    Review 2 asked for 1, so that it matches `mash status`; 2 is kept because
    the contract fixes a refused plain URL as a usage error and a change needs
    an ADR. The outputs are the same either way.
19. The NEXT after `mash key create` and `mashctl endpoint create` carries
    `<owner>/<group>` and `<key file>` placeholders. Reasoning: R-paste; mash
    does not know them unless the config names them.
20. The word `seed` stays once, in Step 1 ("NATS calls it a seed"). Review 2
    asked to move it to `--detail`; rule 7 decided it stays in the key step.
21. Table headers: `ENDPOINT` is the name (`bob`) and `PATH` the dotted
    address (`acme.demo.bob`), in every grouped table (columns `OWNER GROUP
    ENDPOINT PATH`), as in the maintainer's mockup and part 2; the old
    `ADDRESS` and `NAME` headers are retired. `ENDPOINT` is 8 columns wide,
    so the `--wide` list table header now fills exactly 80 columns; if the
    NOTE column ever grows its header, drop `HOST` from `--wide` first.

## Facts to confirm

- [confirm: exit code of `mashctl init --local` and `mashctl list` when they
  print the removal message (2 as written, or 1)]
- [confirm: the file name of the setup script and the skill that the removal
  message names (the guide is `docs/guides/nats-local.md`)]
- [confirm: `mashctl init` is safe to run twice and may say nothing changed]
- [confirm: what the lease storage holds, written as "the sessions that are
  running"]
- [confirm: `mashctl status` connects to the server to test it and to see
  whether init ran; the catalog shows only values read from `config.json`]
- [confirm: `mashctl status` exits 1 on `PROBLEM` and `NOT REACHABLE`]
- [confirm: `mash key create --out bob.key` writes `bob.pub` (the catalog
  says a trailing `.seed` becomes `.pub`, and any other name gets `.pub`
  appended, so `bob.key` would give `bob.key.pub` today; the rules decide
  `bob.pub`)]
- [confirm: the default config directory is `~/.config/mash` on this
  machine; the docs show it for synadia and TLS examples; `MASH_CONFIG_DIR`
  changes it, and every printed path then shows the resolved directory]
- [confirm: `config.json` accepts `owner`, `group` and `name` for the
  endpoint a session runs as]
- [confirm: `--detail` of `show` prints capabilities and tags when set, under
  the words `capabilities` and `tags`]
- [confirm: `endpoint show` can tell a suspended endpoint, and prints it as a
  `status:` line when it is]
- [confirm: the exact command that starts a Claude Code session as an
  endpoint, and the plugin install step]
- [confirm: `mashctl adopt` accepts the server certificate file as
  `MASH_TLS_CA` on the command line, as the catalog's capture does]
- [confirm: adopt's only server change with `--make-admin` is recording the
  admin credentials in the record of signed tokens (catalog, "What adopt does
  not do")]
- [confirm: the address `tls://myserver:4222` is an example; the TLS
  certificate must name myserver]
- [confirm: whether mash needs `bob.key` and the token file after
  `mash credential install`; no line in the output says to keep or delete
  them]

## Changes in DRAFT2

By rule (counts are of changed or added AFTER blocks and lines; each is a
place, not a word).

- R-paste (every NEXT and fix): 17 NEXT lines rewritten as
  `NEXT: <sentence>:` plus the command on its own indented line (adopt, init,
  step 1 long and short, step 2 long and short, step 3 long and short, alice
  short and long, the empty list, status fix x3, `mashctl init --local`
  fix, `mashctl list` fix, the account-not-found fix). The `--explain`
  advice left the NEXT. Placeholders added: `<name>`, `<owner>/<group>`,
  `<key file>`, `--account <name>`. The `export` line in section 4 stands on
  its own line.
- R-true-fix: 2 places. Section 4 names `~/.config/mash/config.json` before
  the `export`. The `Run a session` NEXT (old option 2 of Step 3 and alice)
  is replaced by `mash status` with the identity on the command, as a test
  for one command.
- R-verdict: `mashctl status` redone in 4 mockups (short OK, not prepared,
  not reachable, `--detail`), verdict as line 1 (`OK`, `PROBLEM: ...`,
  `NOT REACHABLE: ...`); version row moved to `--detail`. adopt is noted as
  last-line verdict (it prints as it checks).
- R-names: 12 places. "Agents" to "Endpoints" in the setup block and its
  diagram; the setup paragraph calls the dotted path the address; table
  headers `ENDPOINT` (name) and `PATH` (dotted address) in 3 list tables and
  the wide table (since changed from `NAME` and `ENDPOINT`); "directory key"
  to "storage name" in `show --detail`; "Wrote"
  to "Created" or "Installed" in the step outputs; "made" to "Created";
  `Created a key pair`; no flat form in default output; `2 s` units kept
  (no `12 min` forms in this file).
- R-nextcase: `fix:` after a failure in 5 places (status x2, init --local,
  list, adopt account); `NEXT:` after a success everywhere else; the document
  for the NATS guide is `See docs/guides/nats-local.md`.
- Consistency (sibling): flags `--system-credentials`, `--admin-credentials`,
  `--make-admin`; tags and capabilities; `endpoint suspend|resume`;
  `mashctl list` removed with a refusal; `init --local` and `--start` removed
  with the refusal plus the document line.
- R-exit: exit 2 kept for the plain-URL refusal; Choices item 18 says why.
- Checks: `check.sh` (width and banned words) and `check3.sh` (NEXT and fix
  form), both clean at the end.

---

# mashctl 0.5.6 target output, part 2: every command except the first-run journey

Status: design proposal, no code. Written against the catalog
`docs/plans/2026-11-19-cli-output-catalog-mashctl.md` (v0.5.5) and the rules in
`rules.md`. The first-run journey (adopt, init, status, endpoint create / list /
show, key and credentials steps) is in part 1 and is not repeated here.

How to read the mockups.
- In every AFTER block the word NEXT is coloured on a terminal. When output is
  not a terminal, or `NO_COLOR` is set, the word stays `NEXT:` with no colour.
- `NEXT:` follows a success and `fix:` follows a failure. Both are one sentence
  ending in a colon; a command a person must run is on its own line beneath it,
  indented two spaces, complete, not wrapped, with no full stop or comma after
  it, and `<placeholder>` for any value mash cannot know. A document is not a
  NEXT: it is written `See <path>`.
- A command that reports a state (`audit`, `issued check`, `endpoint suspended`)
  prints the verdict first, in words that read without colour (`OK`, `PROBLEM`,
  `INCOMPLETE`, `SUSPENDED`), then only what is wrong or asked for.
- The server named in a result line (`tls://127.0.0.1:24222`) is the server the
  command changed. It replaces the old `mashctl: target server ...` line, which
  is gone from every command (P1).
- `$ command` and `[exit N]` lines are annotations for the reader. The program
  prints only the lines under them.
- Names: owner `acme`, group `demo`, endpoints `alice` and `bob`, machine
  `myserver`. One word per thing: endpoint (the thing), name (`bob`), address
  (`acme.demo.bob`), group, owner. People see the dotted address in results;
  `acme/demo/bob` appears only inside a command a person types. In a grouped
  table the column that holds the name is headed `ENDPOINT`; the dotted address
  column, where a table has one, is headed `PATH` (part 1's endpoint list has
  both: `OWNER GROUP ENDPOINT PATH`); a column that holds an address in a
  message table is headed `ADDRESS` or `FROM`.
- Points P1..P9 are the nine points of the catalog (P1 preamble, P2 address three
  ways, P3 unaligned tables, P4 NEXT lines, P5 over 80 columns, P6 undefined
  words, P7 agent output shown to a person, P8 repeated fields, P9 unasked
  detail).
- `--json` is unchanged everywhere. Nothing here redesigns any JSON.
- Dry-runs end `Nothing was changed.` and print no NEXT (see Choices).

---

## A. The command tree, `mashctl --help`, and removed commands

### A1. The tree

```text
mashctl
  Connect      adopt            connect mashctl to a server that exists
               init             prepare that server for mash (once)
               status           show the server mashctl uses, and if it works
  Endpoints    endpoint create | list | show | set
               endpoint reissue | delete
               endpoint suspend | resume | suspended
  Inspect      audit
               issued list | check
               inbox inspect
               permissions
  Advanced     limits
               provision | deprovision
  Other        version
```

Proposals, each marked as a proposal:

- `endpoint set` replaces `label`, `unlabel` and `set-meta`. One verb changes
  what an endpoint is described as: role, host, description, tags, capabilities.
- `endpoint suspend`, `endpoint resume` and `endpoint suspended` replace the
  top-level `suspend`, `resume`, `suspended`. Exit codes stay (0, 1, 2, and 3 for
  "not suspended").
- `mashctl list` is removed. It is a second, different `endpoint list` (tab
  separated, public key, no header).
- `provision` and `deprovision` stay, under Advanced, with the same names in
  0.5.6. They are the commands for a server where someone else makes the NATS
  users (section G). `endpoint create`, `reissue` and `delete` are the commands
  for everyone else. Their help says so in one line.
- `init --local` and `init --start` are removed (rule 14).
- `mashctl status` is new (part 1).

### A2. BEFORE: `mashctl --help` (catalog, usage_help, trimmed)

Marks P3 P5 P6 P7; 20 lines over 80 columns.

```text
usage: mashctl <verb> [args]

  mashctl init                                 create the directory and lease buckets (or raise their caps to the configured limits)
  mashctl init --local [--dir D] [--owner O] [--port P] [--start] write the configuration of a local operator-mode NATS server with JetStream (loopback, TLS only), keys and credentials, for one developer; --start also starts it and creates the buckets
  mashctl limits [address...]                  raise existing inboxes to the configured limits (config.json "limits"; never lowers)
  mashctl set-meta <owner.group.instance> [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]... [--uncap C]... (a value of - clears the field; --label k=- clears that label; --cap C adds a capability, --uncap C removes one, --cap - clears them all)
  mashctl list
  mashctl audit [--json] [--strict] [--creds-dir DIR] read only: list every directory record, inbox stream, lease and creds file with their state, the orphans; --strict exits 1 on an orphan, ...
  mashctl label <owner.group.instance> k=v... [--dry-run] set labels on the endpoint's directory record
```

### A3. AFTER: `mashctl --help`

```text
$ mashctl --help
[exit 0]
mashctl - administer a mash server

Usage: mashctl <command> [options]

Connect
  adopt       connect mashctl to a NATS server you already run
  init        prepare that server for mash (once, after adopt)
  status      show the server mashctl uses, and whether it works

Endpoints   (an endpoint is an agent or tool with an address on the mail mesh)
  endpoint create     make an endpoint and its credentials
  endpoint list       list endpoints            endpoint show   show one
  endpoint set        change an endpoint's role, host, description, tags
  endpoint reissue    give an endpoint a new key (the old key stops working)
  endpoint delete     delete an endpoint and its inbox for good
  endpoint suspend    stop delivering mail to an endpoint (resume undoes it)

Inspect
  audit               what the server holds, and what is left over
  issued              list the keys mash issued; check who is connected
  inbox inspect       show the mail stored for one endpoint
  permissions         show what an endpoint is allowed to do

Advanced
  limits              raise inbox size limits to the configured values
  provision           register an endpoint whose NATS user someone else made
  deprovision         remove such an endpoint

Moved or removed
  list                       removed; see endpoint list
  label, unlabel, set-meta   moved; see endpoint set
  suspend, resume            moved; see endpoint suspend and endpoint resume
  init --local, --start      removed; mashctl does not install NATS
                             (See docs/guides/nats-local.md)

An address is owner.group.name, for example acme.demo.bob. The form
acme/demo/bob is accepted too. Add --json for scripts, --help for a command.
```

What changed and why:

- P5: every line is 80 columns or less; today 20 lines are over, up to 330.
- P6, P7: the text names no lease, bucket, cap, creds, kind or JWT. The words
  that remain (endpoint, tags, key) are explained where they appear.
- P3: one aligned list, grouped by task (connect, endpoints, inspect, advanced),
  instead of two layouts in one table.
- Feedback 14 and rule 1: nothing about the server is printed here; `status`
  shows it on request.
- Rule 14 (feedback 1): `init --local` is listed only in the removed block.
- Rule 12: one-line summary, usage, grouped list; the example is in each
  command's own `--help`.

### A4. AFTER: `mashctl endpoint --help`

```text
$ mashctl endpoint --help
[exit 0]
mashctl endpoint - make, change and remove endpoints

Usage: mashctl endpoint <command> [options]

  create      make an endpoint and its credentials
  list        list endpoints (--wide for more columns)
  show        show one endpoint
  set         change an endpoint's role, host, description or tags
  reissue     give an endpoint a new key; the old key stops working
  delete      delete an endpoint, its inbox and its address for good
  suspend     stop delivering mail to an endpoint
  resume      deliver mail to an endpoint again
  suspended   say whether an endpoint is suspended (exit 3 if it is not)

Example:
  mashctl endpoint create acme/demo/bob --make-key
```

BEFORE (catalog, endpoint cases 2 and 3), marks P5 P6:

```text
usage: mashctl endpoint <verb> [args]
  verbs: create, reissue, delete, list, show
  try: mashctl endpoint create --help
mashctl: endpoint: unknown verb "frob" (the verbs are create, reissue, delete, list, show; try: mashctl endpoint --help)
```

What changed and why:

- The bare `mashctl endpoint` and `mashctl endpoint frob` print the one-line
  error and the list above (see E, usage errors), not a different short list.

### A5. AFTER: removed commands and flags

All exit 2, nothing is changed. Two lines, `error:` then `fix:` (rule 10).

```text
$ mashctl add
[exit 2]
error: mashctl add was removed.
fix: create an endpoint with the new command:
  mashctl endpoint create <owner>/<group>/<name> --public-key <file>

$ mashctl reissue
[exit 2]
error: mashctl reissue was removed.
fix: reissue an endpoint with the new command:
  mashctl endpoint reissue <owner>/<group>/<name> --make-key

$ mashctl retire
[exit 2]
error: mashctl retire was removed.
fix: delete an endpoint with the new command:
  mashctl endpoint delete <owner>/<group>/<name>

$ mashctl list
[exit 2]
error: mashctl list was removed.
fix: list endpoints with the new command:
  mashctl endpoint list

$ mashctl label acme.demo.alice env=demo
[exit 2]
error: mashctl label was replaced by mashctl endpoint set.
fix: use the new command:
  mashctl endpoint set acme/demo/alice --tag env=demo

$ mashctl unlabel acme.demo.alice env
[exit 2]
error: mashctl unlabel was replaced by mashctl endpoint set.
fix: use the new command:
  mashctl endpoint set acme/demo/alice --untag env

$ mashctl set-meta acme.demo.alice --role designer
[exit 2]
error: mashctl set-meta was replaced by mashctl endpoint set.
fix: use the new command:
  mashctl endpoint set acme/demo/alice --role designer

$ mashctl suspend acme.demo.bob
[exit 2]
error: mashctl suspend was replaced by mashctl endpoint suspend.
fix: use the new command:
  mashctl endpoint suspend acme/demo/bob
```

`resume` and `suspended` get the same two lines with their own names.

```text
$ mashctl init --local
[exit 2]
error: mashctl no longer installs or starts a NATS server.
fix: set one up (See docs/guides/nats-local.md), then connect mashctl:
  mashctl adopt --help

$ mashctl init --start
[exit 2]
error: mashctl no longer installs or starts a NATS server.
fix: set one up (See docs/guides/nats-local.md), then connect mashctl:
  mashctl adopt --help

$ mashctl endpoint create acme/demo/bob --public-key b.pub --jwt-out b.jwt
[exit 2]
error: --jwt-out was renamed --token-out.
fix: run it again with the new flag name:
  mashctl endpoint create acme/demo/bob --public-key b.pub --token-out b.jwt

$ mash credential create --seed bob.seed --jwt acme.demo.bob.jwt
[exit 2]
error: mash credential create was replaced by mash credential install.
fix: use the new command:
  mash credential install --key bob.key --token acme.demo.bob.token

$ mash credential install --seed bob.seed --jwt acme.demo.bob.jwt
[exit 2]
error: --seed was renamed --key, and --jwt was renamed --token.
fix: run it again with the new flag names:
  mash credential install --key bob.key --token acme.demo.bob.token
```

Unknown commands that were never real (for example `unzone`, `frobnicate`) get
the unknown-command message in section E.

BEFORE (catalog, removed_add and removed_unzone), marks P5, and for unzone
P3 P6 P9:

```text
mashctl: add was removed. Use: mashctl endpoint create alice/dev/design --public-key design.pub   (see: mashctl endpoint create --help)
mashctl: unknown command "unzone"
usage: mashctl <verb> [args]   (then the whole 20 line usage block)
```

What changed and why:

- P5: each message is two lines, each under 80 columns.
- P9: a removed command no longer prints the full usage block under its error.
- Feedback 1 and 14 (rule 14): the `init --local` message names the guide;
  getting a server is done outside mashctl.
- The old `--seed` and `--jwt` names appear only in these messages, so a person
  who has them in a script learns the new names at once. They are not in help.
- Names follow the decided three-step flow (`--token-out`, `credential install`).

---

## B. endpoint reissue and endpoint delete

### B1. reissue, one endpoint, `--make-key` (same machine)

BEFORE (catalog, er_make), marks P1 P2 P4 P6 P8, and the preamble line:

```text
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from ...), admin creds ...
reissued endpoint acme/team/alice
  address  acme.team.alice
  creds    /tmp/mc/cfg/acme.team.alice.creds
next: restart the sessions of this endpoint so they read the new creds
servers acknowledged the account update: 1
revoked user key UAGT4ZQX...: 1 server(s) acknowledged
```

AFTER

```text
$ mashctl endpoint reissue acme/demo/bob --make-key
[exit 0]
Reissued endpoint acme.demo.bob on tls://127.0.0.1:24222.
  Created a new key. Credentials: ~/.config/mash/acme.demo.bob.creds
  The old key no longer works: all 1 servers confirmed it.
  Unread mail in bob's inbox is kept.

NEXT: on myserver, restart the session that runs as bob, so it uses the
new credentials. Until then bob is signed out.
```

AFTER, with `--detail`

```text
$ mashctl endpoint reissue acme/demo/bob --make-key --detail
[exit 0]
Reissued endpoint acme.demo.bob on tls://127.0.0.1:24222.
  Created a new key. Credentials: ~/.config/mash/acme.demo.bob.creds
  The old key no longer works: all 1 servers confirmed it.
  Unread mail in bob's inbox is kept.

Detail
  directory key     acme_demo_bob
  old public key    UAO35ZE32HBKAMOGS4CYOJCJWGWGGEGGDEWOLR5Q2L47QFBJMQJRU3CC
  new public key    UA2VGOPQ6HQ2U4DAHV5XFS6NHSQCYBKB5RMLXN4Y52OWXWSXTU3FJTQQ
  old key revoked   2026-11-19 00:30 UTC
  new token issued  2026-11-19 00:30 UTC
  servers           1 of 1 answered
  inbox             kept (3 messages)

NEXT: on myserver, restart the session that runs as bob, so it uses the
new credentials. Until then bob is signed out.
```

AFTER, `--dry-run`

```text
$ mashctl endpoint reissue acme/demo/bob --make-key --dry-run
[exit 0]
Would reissue endpoint acme.demo.bob on tls://127.0.0.1:24222:
  - make a new key on this machine
  - stop the old key working, on every server
  - replace ~/.config/mash/acme.demo.bob.creds with the new credentials
  - keep bob's inbox and its unread mail
Nothing was changed.
```

What changed and why:

- P1: no preamble. The server is named once, in the result line.
- P2, P8: the address is `acme.demo.bob` once. The old output had it as
  `acme/team/alice` and `acme.team.alice` on the next line, plus the creds path.
- P6: "creds", "user key" and "account update" are gone. The two "servers
  acknowledged" lines (one said "account update", one "revoked user key") are
  one sentence: the old key no longer works, and how many servers confirmed it.
  This is what the old lines meant: mashctl sends the change to every server and
  waits for each one to confirm before it reports success.
- P4: NEXT names the action, who does it (you) and where (myserver). Before, it
  sat above the confirmation lines, so the last thing on screen was not it.
- P9: public keys, the directory key and times are behind `--detail`.
- `--dry-run` uses "Would" sentences and ends `Nothing was changed.` (rule 11).

### B2. reissue with `--public-key` (key made on another machine)

BEFORE (catalog, er_pub), marks P1 P2 P4 P6 P8:

```text
reissued endpoint acme/team/bob
  address  acme.team.bob
  jwt      /tmp/mc/work/bob2.jwt
next: on the endpoint's machine, run: mash credential create --seed bob.seed --jwt bob2.jwt
servers acknowledged the account update: 1
revoked user key UBH6YD4H...: 1 server(s) acknowledged
```

AFTER

```text
$ mashctl endpoint reissue acme/demo/bob --public-key bob2.pub
[exit 0]
Reissued endpoint acme.demo.bob on tls://127.0.0.1:24222.
  Created a new token, ./acme.demo.bob.token (it names the key in bob2.pub).
  The old key no longer works: all 1 servers confirmed it.
  Unread mail in bob's inbox is kept.
  Bob is signed out until the new credentials are installed.

NEXT: copy the token to bob's machine, and install it there with the key:
  mash credential install --key bob2.key --token acme.demo.bob.token
```

AFTER, `--dry-run`

```text
$ mashctl endpoint reissue acme/demo/bob --public-key bob2.pub --dry-run
[exit 0]
Would reissue endpoint acme.demo.bob on tls://127.0.0.1:24222:
  - sign a new token for the key in bob2.pub
  - write it to ./acme.demo.bob.token
  - stop the old key working, on every server
  - keep bob's inbox and its unread mail
Nothing was changed.
```

What changed and why:

- P6: "jwt" and "seed" become token and key (rule 7). The command is the decided
  `mash credential install`, with the file names the person used.
- P4: NEXT is the exact command on its own line, names the machine where it
  runs, and says the token file must travel there.
- The flag `--jwt-out` is now `--token-out`; the default file is
  `acme.demo.bob.token` in the current directory.

### B3. reissue, `--all` and `--changed`

Proposal: `--all` and `--changed` always make the new keys on this machine, so
`--make-key` is not needed with them. `--public-key` with them is a usage error
(section E). They act only on endpoints whose credentials file is on this
machine. `--all` means every such endpoint. `--changed` means those whose
permissions differ from the current permissions, whose last reissue was left
half done, or whose key is revoked.

BEFORE (catalog, er_all_dry2 and er_all_yes), marks P2 P4 P5 P6:

```text
would reissue acme/team/alice
would reissue acme/team/alice2
would reissue acme/team/bob
would reissue acme/team/carol
skipped acme/team/ned (no_local_creds): its creds are not on this host; reissue it on its own machine with: mash key create, then mashctl endpoint reissue acme/team/ned --public-key FILE
reissued 0, skipped 1, refused 0, error 0
would reissue 4; nothing was changed; to do it run: mashctl endpoint reissue --all --make-key --yes
```

```text
reissued 3, skipped 0, refused 1, error 0
partial: some endpoints did not finish; rerun with --changed, not --all (--all rotates the ones that succeeded again): mashctl endpoint reissue --changed --make-key --yes
next: restart the sessions of the reissued endpoints so they read the new creds
```

AFTER, `--dry-run` (a run without `--yes` prints the same)

```text
$ mashctl endpoint reissue --all --dry-run
[exit 0]
Would give new keys to 2 endpoints on tls://127.0.0.1:24222. The old keys
would stop working.

OWNER  GROUP  ENDPOINT  CREDENTIALS FILE (THIS MACHINE)
acme   demo   alice     ~/.config/mash/acme.demo.alice.creds
              bob       ~/.config/mash/acme.demo.bob.creds

Skipped, no credentials file on this machine (1):
  acme.demo.carol
To reissue it, make a key on carol's machine with mash key create, then run:
  mashctl endpoint reissue acme/demo/carol --public-key <file>

Nothing was changed. Add --yes to do it.
```

AFTER, `--all --yes`, all done

```text
$ mashctl endpoint reissue --all --yes
[exit 0]
Gave new keys to 2 endpoints on tls://127.0.0.1:24222.

OWNER  GROUP  ENDPOINT  RESULT
acme   demo   alice     done
              bob       done

The old keys no longer work: all 1 servers confirmed it.

NEXT: on myserver, restart the sessions that run as alice and bob, so
they use the new credentials.
```

AFTER, `--all --yes`, partial outcome (exit 5)

```text
$ mashctl endpoint reissue --all --yes
[exit 5]
Gave a new key to 1 of 2 endpoints on tls://127.0.0.1:24222. 1 was not done.

OWNER  GROUP  ENDPOINT  RESULT
acme   demo   alice     done
              bob       not done: its credentials file here is not the one
                        the server expects (nothing was changed for bob)

Alice's old key no longer works: all 1 servers confirmed it.
Bob's key is unchanged. Do not run --all again: it would give alice a
second new key. When bob is done, restart the sessions of alice and bob.

NEXT: move bob's file aside, then reissue bob alone:
  mv ~/.config/mash/acme.demo.bob.creds ~/.config/mash/acme.demo.bob.old
  mashctl endpoint reissue acme/demo/bob --make-key
```

AFTER, `--changed`, nothing to do

```text
$ mashctl endpoint reissue --changed --yes
[exit 0]
Nothing to reissue: no endpoint on this machine needs a new key.
```

What changed and why:

- P3, P2: a grouped table (owner and group once), the dotted address only.
- P6: no `reissued 0, skipped 0, refused 0, error 0` counter line. The result is
  a sentence plus a RESULT column; the reason is in plain words.
- P4: the old partial text mixed an instruction, a warning and a long command in
  one 190 column line. Now: what happened, what not to do, one NEXT.
- The old advice ("rerun with --changed") is not used for bob: once the file is
  moved aside, bob has no credentials file here, so `--changed` would skip him.
  [confirm: that `--changed` skips an endpoint with no local file]
- The dry run no longer prints a counter line of zeros under the "would" lines.

### B4. delete, dry-run and real

BEFORE (catalog, ed_dry and ed), marks P1 P2 P6 P8 P9:

```text
would delete acme/team/dave (acme.team.dave) (nothing was changed)
would revoke user key UCSZM7WQ...
would remove the directory record and the inbox, and burn the address
would rename creds file /tmp/mc/cfg/acme.team.dave.creds to /tmp/mc/cfg/acme.team.dave.creds.retired-<time>
```

```text
deleted acme/team/dave (acme.team.dave)
creds file renamed to /tmp/mc/cfg/acme.team.dave.creds.retired-20261119T002404Z (--delete-creds removes it instead)
revoked user key UCSZM7WQ...: 1 server(s) acknowledged
user key UCSZM7WQ... revoked in the account JWT: its creds stop working when the server takes the update; the creds file is separate
servers acknowledged the revocation: 1
```

AFTER, `--dry-run`

```text
$ mashctl endpoint delete acme/demo/bob --dry-run
[exit 0]
Would delete endpoint acme.demo.bob on tls://127.0.0.1:24222:
  - stop bob's key working, on every server
  - delete bob's inbox; unread mail is lost
  - retire the address: acme.demo.bob can never be used again
  - rename ~/.config/mash/acme.demo.bob.creds to
    acme.demo.bob.creds.retired-<UTC date and time>
Nothing was changed.
```

AFTER, real

```text
$ mashctl endpoint delete acme/demo/bob
[exit 0]
Deleted endpoint acme.demo.bob on tls://127.0.0.1:24222.
  Its key no longer works: all 1 servers confirmed it.
  Its inbox is deleted, with any unread mail. The address is retired for good.
  Its credentials file here was renamed to
  ~/.config/mash/acme.demo.bob.creds.retired-20261119-0024
  (add --delete-creds to remove the file instead).

NEXT: stop the session that runs as bob, and delete any copy of its
credentials file on other machines. The key in it no longer works.
```

AFTER, `--detail`

```text
$ mashctl endpoint delete acme/demo/bob --detail
[exit 0]
Deleted endpoint acme.demo.bob on tls://127.0.0.1:24222.
  Its key no longer works: all 1 servers confirmed it.
  Its inbox is deleted, with any unread mail. The address is retired for good.
  Its credentials file here was renamed to
  ~/.config/mash/acme.demo.bob.creds.retired-20261119-0024
  (add --delete-creds to remove the file instead).

Detail
  directory key   acme_demo_bob
  public key      UAO35ZE32HBKAMOGS4CYOJCJWGWGGEGGDEWOLR5Q2L47QFBJMQJRU3CC
  key revoked     2026-11-19 00:24 UTC
  servers         1 of 1 answered
  mail lost       3 unread messages

NEXT: stop the session that runs as bob, and delete any copy of its
credentials file on other machines. The key in it no longer works.
```

What changed and why:

- P2: the address appears once, dotted. The old output printed it as
  `acme/team/dave (acme.team.dave)` on the result line.
- P8, P9: "revoked" was said three times (twice with the key, once as a count).
  It is one line now. The key text and the count are in `--detail`.
- P6: "directory record", "burn the address", "creds", "account JWT" are gone;
  "retire the address ... never be used again" says what burning does.
- The dry run shows the file name pattern in words instead of the unexpanded
  `.retired-<time>`, and shows no key.
- NEXT is a real step: a retired key still sits in files on other machines.

### B5. delete, repeat, `--force`, `--no-revoke`

BEFORE (catalog, ed_again, ed_force, ed_norevoke), marks P1 P2 P5 P6 P8:

```text
deleted acme/team/dave (acme.team.dave)
the address was already retired and nothing was left on the server
mashctl: endpoint delete acme/team/dave: no directory record on the server, so no user key is known and nothing was revoked; only the creds file is handled
```

```text
deleted acme/team/fay (acme.team.fay)
acme/team/fay: its user key is not revoked (--no-revoke): its creds still authenticate until the key is revoked in the account JWT (nsc, or your operator tooling) or the signing key is rotated
servers acknowledged the revocation: 0
WARNING: --no-revoke: acme.team.fay's NATS user key UCOWLNCN... is NOT revoked and can still connect, ...   (this WARNING line is printed twice)
```

AFTER, repeat (already deleted, nothing left here either)

```text
$ mashctl endpoint delete acme/demo/bob
[exit 0]
Endpoint acme.demo.bob was already deleted. Nothing was changed.
```

AFTER, repeat, a credentials file is still here, no server record

```text
$ mashctl endpoint delete acme/demo/bob
[exit 0]
Endpoint acme.demo.bob is already deleted on tls://127.0.0.1:24222.
  mash holds no key for it, so there is nothing to revoke. Its key was
  revoked when it was deleted.
  Its credentials file here was renamed to
  ~/.config/mash/acme.demo.bob.creds.retired-20261119-0031
```

AFTER, `--force` when the server has no record (finishes a half-done delete)

```text
$ mashctl endpoint delete acme/demo/bob --force
[exit 0]
No record of acme.demo.bob on tls://127.0.0.1:24222.
  Only the credentials file here was handled: it was renamed to
  ~/.config/mash/acme.demo.bob.creds.retired-20261119-0031
  No key was revoked, because mash does not know which key it was.
```

AFTER, `--force --dry-run`

```text
$ mashctl endpoint delete acme/demo/bob --force --dry-run
[exit 0]
Would handle acme.demo.bob on tls://127.0.0.1:24222:
  - the server has no record of it, so no key would be revoked
  - rename ~/.config/mash/acme.demo.bob.creds to
    acme.demo.bob.creds.retired-<UTC date and time>
Nothing was changed.
```

AFTER, `--no-revoke` (the warning is printed once, after the results)

```text
$ mashctl endpoint delete acme/demo/bob --no-revoke
[exit 0]
Deleted endpoint acme.demo.bob on tls://127.0.0.1:24222. KEY NOT REVOKED.
  Its inbox is deleted, with any unread mail. The address is retired for good.
  Its credentials file here was renamed to
  ~/.config/mash/acme.demo.bob.creds.retired-20261119-0024
  Its key was not revoked.

warning: bob's key still works. Until it is revoked, a copy of bob's
credentials can still sign in, send and read mail. Revoke it with the
tools of whoever runs the server (in NATS, add it to the account's
revocation list).

NEXT: ask whoever runs the server to revoke bob's key, whose public key is:
  UAO35ZE32HBKAMOGS4CYOJCJWGWGGEGGDEWOLR5Q2L47QFBJMQJRU3CC
```

What changed and why:

- P1, P2, P8: no preamble, one address form, and the "already retired" case is
  one sentence. The old output said `deleted ...` when nothing was deleted.
- P5: the longest old line (250 columns) is wrapped under 80.
- `--no-revoke`: the old output printed the warning twice (both on stderr) plus
  a third long line on stdout. It now prints the warning once, after the
  results, in plain words, with what to do.
- NEXT after `--no-revoke` names the key on its own line, because the person
  must hand it to whoever can revoke it. The first line says `KEY NOT REVOKED`
  so the state reads before anything else.
- Delete of an address that never existed is a refusal (section E), exit 1.

---

## C. endpoint set, suspend and resume

### C1. `mashctl endpoint set --help`

Proposal. `endpoint set` changes any of the five properties in one command and
prints only what changed. "Tag" replaces "label" and "capability" replaces
"cap". If the maintainer wants the old flag names kept, help must carry the two
one-line explanations shown in the last two options (see C1 notes).

```text
$ mashctl endpoint set --help
[exit 0]
mashctl endpoint set - change how an endpoint is described

Usage: mashctl endpoint set ADDRESS [options]

Options (use at least one; the value - clears a field)
  --role R               what the endpoint does, for example designer
  --host H               the machine it runs on
  --describe TEXT        one sentence about it
  --tag K=V              add or change a tag (a note shown by mash who --detail)
  --untag K              remove a tag
  --capability WORD      add a capability (a word for something it can do)
  --remove-capability W  remove a capability; --capability - removes all
  --dry-run              say what would change and change nothing
  --json                 print the result as JSON

Changing these does not change the endpoint's key or what it may do.

Example:
  mashctl endpoint set acme/demo/alice --role designer --tag team=ui
```

BEFORE (catalog, help_set-meta), marks P5 P6:

```text
usage:
  mashctl set-meta <owner.group.instance> [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]... [--uncap C]... (a value of - clears the field; --label k=- clears that label; --cap C adds a capability, --uncap C removes one, --cap - clears them all)
```

What changed and why:

- P5, P6: the 230 column usage is a short table. Tags and capabilities each get
  one explaining clause.
- C1 notes: if the old names must stay, the flags are `--label K=V` ("add or
  change a label, a note shown by mash who --detail"), `--cap WORD` ("add a
  capability, a word for something it can do") and `--uncap WORD`.

### C2. set: result forms

BEFORE (catalog, sm_show and sm_clear), marks P1 P6 P9:

```text
updated acme.team.alice: role "designer", host "laptop", labels tier=1, description "makes designs", caps read
updated acme.team.alice: role "", host "laptop", labels none, description "makes designs", caps read
```

AFTER, several changes

```text
$ mashctl endpoint set acme/demo/alice --role designer --host myserver \
>     --describe "makes designs" --tag team=ui --capability read
[exit 0]
Updated endpoint acme.demo.alice on tls://127.0.0.1:24222.
  role         designer
  host         myserver
  description  makes designs
  tag added    team=ui
  capability   read added
```

AFTER, clearing

```text
$ mashctl endpoint set acme/demo/alice --role - --untag team
[exit 0]
Updated endpoint acme.demo.alice on tls://127.0.0.1:24222.
  role         cleared
  tag removed  team
```

AFTER, nothing differs

```text
$ mashctl endpoint set acme/demo/alice --host myserver
[exit 0]
Endpoint acme.demo.alice already has these values. Nothing was changed.
```

AFTER, removing a tag that is not set (warns, still exit 0)

```text
$ mashctl endpoint set acme/demo/alice --untag colour
[exit 0]
warning: acme.demo.alice has no tag "colour". Nothing was changed.
```

AFTER, `--dry-run`

```text
$ mashctl endpoint set acme/demo/alice --role designer --tag team=ui --dry-run
[exit 0]
Would update endpoint acme.demo.alice on tls://127.0.0.1:24222:
  - set role to designer
  - add tag team=ui
Nothing was changed.
```

What changed and why:

- P9: the old line printed all five fields every time, so clearing one printed
  four unchanged values. Only changed fields are shown.
- P6: role, host, description, tag and capability are defined in `--help`.
- The old dry-run said "would have labeled ... (nothing written)" without saying
  what; the new one lists the changes.
- No NEXT: nothing is required afterwards, and the key does not change.

### C3. suspend, resume, suspended

Behaviour kept (docs/mashctl.md): `suspend` refuses all inbound mail to the
endpoint's inbox and keeps stored mail readable; the endpoint's key still
works; suspending twice returns the same suspension. `deprovision` needs the
suspension id and the last message number from here. `suspended` exits 0 when
suspended, 3 when not, 1 on error.

BEFORE (catalog, suspend, suspended_yes, suspended_not), marks P1 P5 P6 P9:

```text
suspended acme.team.bob: id 63afd15649fb5dee9904fbba687fbe7c, last_seq 0, msgs 0 (new mail parked; the endpoint's credentials are still valid)
suspended acme.team.bob since 2026-11-19T00:22:17Z: id 63afd15649fb5dee9904fbba687fbe7c, last_seq 0, msgs 0
acme.team.bob is not suspended
```

AFTER

```text
$ mashctl endpoint suspend acme/demo/bob
[exit 0]
Suspended mail to endpoint acme.demo.bob on tls://127.0.0.1:24222.
  New mail to bob is refused. The 3 messages already stored stay readable.
  Bob's key still works: it can sign in and read what is stored.
  Suspension id   63afd15649fb5dee9904fbba687fbe7c
  Last message    number 3
```

AFTER, resume

```text
$ mashctl endpoint resume acme/demo/bob
[exit 0]
Resumed mail to endpoint acme.demo.bob on tls://127.0.0.1:24222.
  New mail to bob is delivered again.
```

AFTER, asking

```text
$ mashctl endpoint suspended acme/demo/bob
[exit 0]
SUSPENDED: acme.demo.bob, since 2026-11-19 00:22 UTC.
  Suspension id   63afd15649fb5dee9904fbba687fbe7c
  Last message    number 3 (3 messages stored)

$ mashctl endpoint suspended acme/demo/alice
[exit 3]
NOT SUSPENDED: acme.demo.alice.
```

AFTER, `--dry-run`

```text
$ mashctl endpoint suspend acme/demo/bob --dry-run
[exit 0]
Would suspend mail to endpoint acme.demo.bob on tls://127.0.0.1:24222:
  - refuse new mail to bob; the 3 stored messages stay readable
  - leave bob's key working
Nothing was changed.
```

What changed and why:

- P1, P5, P6: no preamble; no `id`, `last_seq`, `msgs`, `parked`. "Suspension
  id" and "Last message number" stay because `deprovision` needs both, and the
  text says what they are.
- Exit codes are kept: suspend 0 (also the second time), resume 0, suspended 0 or
  3, errors 1, usage 2.
- No NEXT on suspend, resume or suspended: the next screen is whatever the
  person decides, not a step of a guide.
- `--dry-run` for suspend and resume is added for the same reason as for the
  other changing commands; the 0.5.5 docs list none. [confirm: add it]
- Known limit, unchanged: `mash send` to a suspended endpoint fails with "no
  response from stream" and does not say the endpoint is suspended.

---

## D. Inspect and advanced commands

Each command prints a short human form. `--detail` prints the rest. Tables
use a header row, grouped owner and group (written once), dates as
`2026-11-19 00:22` in UTC with `(UTC)` in the header, and `--wide` where a table
has more columns to show.

### D1. audit

What it does (docs/audit.md): it reads the server and the credentials
directories, changes nothing, and reports state, not use. It lists every
endpoint and its inbox, and what is left over: inboxes with no endpoint,
credentials files that match no endpoint, and mail lost unread because an inbox
was full. `--strict` exits 1 when there is a finding; `--compare FILE` adds what
moved since an earlier `--json` report; exit 3 when the audit could not finish.

BEFORE (catalog, audit case 2 and case 5), marks P2 P3 P5 P6 P7 P8 P9:

```text
mashctl audit version 2 generated 2026-11-19T00:22:46Z owner acme
creds directories: /tmp/mc/cfg
records 4, running 0
record acme_team_alice	stopped	address acme.team.alice	user UAGT4ZQXALANXPHIAET7WXOZQRCNHQWDUU2UMRFXF23KU6FJQVUH7KOQ	stream inbox-acme_team_alice	msgs 0	first 0	last 0	suspended false	pending 0	ack_pending 0	deliver true	policy all	start_seq 0	filter none	ack_floor 0	delivered 0	num_redelivered 0	subjects agents.mail.acme.team.alice
orphans: 0 stream(s), 0 lease(s), 1 creds file(s)
orphan creds /tmp/mc/cfg/acme.team.bob.creds	user UBH6YD4HP7LRXHHW7ADO5AKELNC3Z7JKW24V6DRNDKW7QVZVSAIDSKFT
fenced lease acme_team_dave (a deprovision tombstone; it expires)
retired markers 8 (final 8, pending 0)	bucket mash-retired	bytes 2263 of 67108864	used 0.00%
retired marker acme_team_dave	final	retired_at 2026-11-19T00:24:03Z	user UCSZM7WQVG46DF4NUFL37WGSTF7TXKOHOFHVPN7PL4CV6FEWDKOKXFEK
mashctl: audit --strict: 1 finding(s): 0 orphan stream(s), 0 orphan lease(s), 1 orphan creds file(s), 0 item(s) not readable or retired-marker finding(s), 0 inbox(es) vanished and 0 recreated since the earlier audit, 0 final retired marker(s) lost since the earlier audit
```

AFTER, clean

```text
$ mashctl audit
[exit 0]
OK: nothing is wrong and nothing is left over.

Audit of owner acme on tls://127.0.0.1:24222, 2026-11-19 00:22 UTC
(changes nothing; shows what the server holds, not who used it)

OWNER  GROUP  ENDPOINT  ONLINE  MAIL STORED  UNREAD  DELIVERY
acme   demo   alice     no      0            0       on
              bob       no      3            3       on
```

AFTER, `--wide` (adds columns), `--detail` (adds the rest)

```text
$ mashctl audit --wide
[exit 0]
OK: nothing is wrong and nothing is left over.

Audit of owner acme on tls://127.0.0.1:24222, 2026-11-19 00:22 UTC
(changes nothing; shows what the server holds, not who used it)

OWNER  GROUP  ENDPOINT  ONLINE  MAIL STORED  UNREAD  DELIVERY  FIRST  LAST
acme   demo   alice     no      0            0       on        0      0
              bob       no      3            3       on        1      3
```

```text
$ mashctl audit --detail
[exit 0]
OK: nothing is wrong and nothing is left over.

Audit of owner acme on tls://127.0.0.1:24222, 2026-11-19 00:22 UTC
(changes nothing; shows what the server holds, not who used it)

OWNER  GROUP  ENDPOINT  ONLINE  MAIL STORED  UNREAD  DELIVERY
acme   demo   alice     no      0            0       on
              bob       no      3            3       on

Detail
  Credentials folders searched: ~/.config/mash
  acme.demo.bob
    directory key   acme_demo_bob
    public key      UAO35ZE32HBKAMOGS4CYOJCJWGWGGEGGDEWOLR5Q2L47QFBJMQJRU3CC
    inbox           inbox-acme_demo_bob, messages 1 to 3
    read position   0 confirmed, 0 delivered, 3 waiting, 0 redelivered
    address for mail  agents.mail.acme.demo.bob
  Deleted endpoints whose addresses are retired: 3
    acme.demo.dave   retired 2026-11-19 00:24 UTC
    acme.demo.erin   retired 2026-11-19 00:24 UTC
    acme.demo.fay    retired 2026-11-19 00:24 UTC
```

AFTER, with a finding (`--strict`, exit 1)

```text
$ mashctl audit --strict
[exit 1]
PROBLEM: 1 credentials file belongs to no endpoint.
  ~/.config/mash/acme.demo.dave.creds
  It may be left over from a delete or a reissue that was cut short.

Audit of owner acme on tls://127.0.0.1:24222, 2026-11-19 00:24 UTC
(changes nothing; shows what the server holds, not who used it)

OWNER  GROUP  ENDPOINT  ONLINE  MAIL STORED  UNREAD  DELIVERY
acme   demo   alice     no      0            0       on
              bob       no      3            3       on

error: the audit found 1 problem.
fix: if the file is not needed, delete it; if it is, list the endpoints:
  mashctl endpoint list
```

AFTER, `--compare`

```text
$ mashctl audit --compare audit-earlier.json
[exit 0]
OK: nothing is wrong and nothing is left over.

Audit of owner acme on tls://127.0.0.1:24222, 2026-11-19 00:25 UTC
(changes nothing; shows what the server holds, not who used it)

OWNER  GROUP  ENDPOINT  ONLINE  MAIL STORED  UNREAD  DELIVERY
acme   demo   alice     no      0            0       on
              bob       no      3            3       on

Since the audit of 2026-11-19 00:22 UTC: 2 inboxes, 0 changed, 0 gone,
0 new.
```

What changed and why:

- P7, P9: the default is one line per endpoint. The 330 column line (public
  key, stream name, six consumer fields, subject) is behind `--detail`. The
  eight lines of retired markers and tombstones, which only describe deleted
  endpoints, are behind `--detail` too.
- P2, P8: one address form; the stream name and directory key are shown only in
  `--detail`. The old line had `acme_team_alice`, `acme.team.alice` and
  `inbox-acme_team_alice` in one row.
- P3: a table with a header, replacing tab separated `name value` pairs. The
  column of names is headed ENDPOINT, as in part 1.
- P6: lease, bucket, orphan, fenced and creds are replaced by "leftovers",
  "problem" and "credentials file". Online means a session is signed in now.
- The verdict is the first line: `OK` or `PROBLEM`, then what is wrong. The old
  330 character `--strict` line becomes `error:` and `fix:`; exit codes are
  unchanged: 0 clean, 1 finding under `--strict`, 3 audit could not finish.
- The text form is the same with and without `--strict`; only the exit code and
  the last `error:` and `fix:` lines differ (without `--strict` a finding exits
  0 and the two lines are not printed).

### D2. issued list

What it does: lists every key mash has issued (docs/mashctl.md, "The issue
record"). Today it prints one tab separated row per key, with `generation 0`,
`tags` and `purpose issue`.

BEFORE (catalog, il), marks P2 P3 P5 P6 P7 P9:

```text
UAGT4ZQXALANXPHIAET7WXOZQRCNHQWDUU2UMRFXF23KU6FJQVUH7KOQ	endpoint	acme.team.alice	2026-11-19T00:21:38Z	generation 0	tags 	purpose issue
UBFOC5KRY5JRFS5QWS3K5GHKTEOVAXFWOQL35A2PYIIO3THDCMG7IQFX	admin	mashadmin	2026-11-19T00:21:30Z	generation 0	tags 	purpose issue
5 issued
```

AFTER

```text
$ mashctl issued list
[exit 0]
Keys that mash has issued, on tls://127.0.0.1:24222

OWNER  GROUP  ENDPOINT   PUBLIC KEY   ISSUED (UTC)      STATUS
acme   demo   alice      UAGT4ZQX...  2026-11-19 00:21  in use
              bob        UAO35ZE3...  2026-11-19 00:21  in use
-      -      mashadmin  UBFOC5KR...  2026-11-19 00:21  in use  (administrator)

3 keys issued, none revoked.
```

AFTER, `--wide`

```text
$ mashctl issued list --wide
[exit 0]
Keys that mash has issued, on tls://127.0.0.1:24222

OWNER  GROUP  ENDPOINT   ISSUED (UTC)      STATUS  PUBLIC KEY
acme   demo   alice      2026-11-19 00:21  in use  UAGT4ZQXALANXPHIAET7WXOZQ...
              bob        2026-11-19 00:21  in use  UAO35ZE32HBKAMOGS4CYOJCJW...
-      -      mashadmin  2026-11-19 00:21  in use  UBFOC5KRY5JRFS5QWS3K5GHKT...

3 keys issued, none revoked.
```

What changed and why:

- P3: header row and aligned columns. The administrator key (the one mashctl
  connects with) belongs to no owner and group, so it shows `-` in both.
- P2, P6, P9: `generation 0`, `tags` and `purpose issue` are not shown. The
  purpose (issue, rotation) and the full public key appear in `--detail`
  [confirm: the exact `--detail` fields are decided with the key store].
- STATUS is "in use" or "revoked 2026-11-19 00:30" (UTC). "revoked-at" is gone.
- The administrator row is marked `(administrator)` in the default table only.
  [confirm: whether to show the administrator key at all without `--detail`]

### D3. issued check

What it does: asks every server which keys are connected now and compares them
with the keys mash issued. It reports a connected key mash did not issue (key,
server, connection name, who signed it), a revoked key still connected, and
keys that are issued but not connected (normal). Exit 0 clean and complete, 1 a
finding, 2 nothing found but the check was incomplete or failed.

BEFORE (catalog, ic), marks P3 P5 P6 P7 P9:

```text
recorded but not connected (informational, 4):
  UAEXLQVHY4P37VJ5YUSI5PF3K3ZVMAXVW5BXYKAQA5NPCDRPHDTPN6ZN	endpoint	acme.team.carol
  UAGT4ZQXALANXPHIAET7WXOZQRCNHQWDUU2UMRFXF23KU6FJQVUH7KOQ	endpoint	acme.team.alice
summary: 1 connected, 0 unrecorded, 0 revoked but connected, 5 recorded (4 not connected), 1 of 1 servers answered
ok: every connected key was issued by mash and none is revoked
```

AFTER, clean

```text
$ mashctl issued check
[exit 0]
OK: every connected key was issued by mash, and none is revoked.

Checked 1 of 1 servers on tls://127.0.0.1:24222. 1 key is connected.
```

AFTER, a finding (exit 1)

```text
$ mashctl issued check
[exit 1]
PROBLEM: 1 of the 2 connected keys was not issued by mash.

Checked 1 of 1 servers on tls://127.0.0.1:24222.

Connected, not issued by mash
  public key       UBH6YD4HP7LRXHHW7ADO5AKELNC3Z7JKW24V6DRNDKW7QVZVSAIDSKFT
  server           mash-local
  connection name  build-agent
  signed by        the account's signing key

error: a key that mash did not issue can sign in to your mash account.
fix: find who made it. If it is not yours, revoke it with the tools of
     whoever runs the server, and check the account's signing keys.
```

AFTER, a revoked key still connected (exit 1)

```text
$ mashctl issued check
[exit 1]
PROBLEM: a revoked key is still connected.

Checked 1 of 1 servers on tls://127.0.0.1:24222. 2 keys are connected.

Revoked, still connected
  endpoint         acme.demo.bob
  public key       UAO35ZE32HBKAMOGS4CYOJCJWGWGGEGGDEWOLR5Q2L47QFBJMQJRU3CC
  server           mash-local

error: the revocation of bob's key did not take effect on this server.
fix: check that every server has the latest account update, then check again:
  mashctl issued check
```

AFTER, incomplete (exit 2)

```text
$ mashctl issued check
[exit 2]
INCOMPLETE: 1 of 2 servers did not answer.

Checked 1 of 2 servers on tls://127.0.0.1:24222. Nothing wrong was found on
the server that answered. If 2 is the wrong number of servers, pass
--servers <number>.

error: 1 of 2 servers did not answer, so the check cannot be called clean.
fix: bring the server back, then run the check again:
  mashctl issued check
```

What changed and why:

- P3, P7, P9: the list of issued-but-not-connected keys (a 56 character key per
  row, no header) is one sentence with a count. The keys are in `--detail`.
- P6: "unrecorded", "recorded", "revoked but connected" are gone; "issued by
  mash" and "revoked" are used instead.
- Exit codes are unchanged. The result is the first line (`OK`, `PROBLEM` or
  `INCOMPLETE`), and `error:` and `fix:` lines follow a finding. The keys that
  are issued but not connected are not printed by default; they are normal.
- No NEXT: the next step depends on who made the key, which mash cannot tell.

### D4. inbox inspect

What it does: shows one endpoint's stored mail and where its delivery stands.
It reads only. Exit 0, 1 refusal (no such endpoint, no inbox), 2 usage, 3 could
not complete.

BEFORE (catalog, ii), marks P2 P5 P6 P7 P8 P9:

```text
inbox acme.team.carol: stream inbox-acme_team_carol, first_seq 1, last_seq 3, messages 3, bytes 1992
consumer deliver: ack_floor stream 0 consumer 0, delivered stream 0 consumer 0, num_pending 3, num_redelivered 0, num_ack_pending 0
SEQ  MSG-ID                            SENDER           TRUST     STORED                SIZE  KIND      STATE
1    4f83ac040b5043183dd2285b26e0ba98  acme_team_alice  verified  2026-11-19T00:22:41Z  11    task      undelivered
note: per-message delivery count and ack time are not kept by JetStream or mash; the consumer's num_redelivered is the aggregate
note: a message above the consumer's ack floor and at or below its last delivered sequence is delivered and either not acknowledged yet or acknowledged out of order: JetStream does not report acknowledgements above the a
```

AFTER

```text
$ mashctl inbox inspect acme/demo/bob
[exit 0]
Inbox of acme.demo.bob: 3 messages stored, 3 waiting to be delivered.

NO.  FROM             TRUST     STORED (UTC)      SIZE   TYPE      STATE
1    acme.demo.alice  verified  2026-11-19 00:22  11 B   task      waiting
2    acme.demo.alice  verified  2026-11-19 00:22  10 B   question  waiting
3    acme.demo.alice  verified  2026-11-19 00:22  2 B    task      waiting
```

AFTER, a part of the inbox (`--from 2 --limit 1`)

```text
$ mashctl inbox inspect acme/demo/bob --from 2 --limit 1
[exit 0]
Inbox of acme.demo.bob: 3 messages stored, 3 waiting to be delivered.
Showing message 2 only (--from 2 --limit 1).

NO.  FROM             TRUST     STORED (UTC)      SIZE   TYPE      STATE
2    acme.demo.alice  verified  2026-11-19 00:22  10 B   question  waiting
```

AFTER, `--detail`

```text
$ mashctl inbox inspect acme/demo/bob --detail
[exit 0]
Inbox of acme.demo.bob: 3 messages stored, 3 waiting to be delivered.

NO.  FROM             TRUST     STORED (UTC)      SIZE   TYPE      STATE
1    acme.demo.alice  verified  2026-11-19 00:22  11 B   task      waiting
2    acme.demo.alice  verified  2026-11-19 00:22  10 B   question  waiting
3    acme.demo.alice  verified  2026-11-19 00:22  2 B    task      waiting

Detail
  stored: messages 1 to 3, 1992 B (inbox-acme_demo_bob)
  delivery position: 0 confirmed, 0 delivered, 3 waiting, 0 redelivered
  message ids: 4f83ac040b5043183dd2285b26e0ba98 (no. 1)
               2250e9385e15acec2c342f4b904e2799 (no. 2)
               a078cb9ea3375b81b880e2197da4851e (no. 3)

STATE words
  waiting    stored, not delivered to bob yet
  delivered  sent to bob; the server cannot say if bob confirmed it
  confirmed  bob confirmed it
Delivery is tracked for the inbox as a whole, not for each message.
```

What changed and why:

- P2, P8: the sender is shown dotted (`acme.demo.alice`), not as the flat
  `acme_team_alice` on every row. The stream name moves to `--detail`.
- P5, P6, P7, P9: the consumer line and the two `note:` lines (128 and 228
  columns, JetStream terms) are gone from the default. The state words are
  explained in `--detail`, which is where the 0.5.5 notes now live.
- TRUST keeps `verified`, the word the mail itself shows. The other words
  (claimed, unknown-signer, stale, forged, malformed, absent) are unchanged.
  [confirm: whether to explain them in `--detail`]
- Column KIND becomes TYPE (what the message is: task, question).
- STATE: undelivered becomes waiting; acked becomes confirmed; the third state
  keeps its meaning, in short words.

### D5. limits, `limits --show`

What it does: raises existing inboxes up to the configured size limits, never
lowers one. `--show` prints the limits in force and does not connect.

BEFORE (catalog, limits_raise and limits_show), marks P1 P2 P3 P6:

```text
acme_team_alice: max_bytes 16777216 -> 33554432
inbox_max_bytes 16MiB
inbox_max_age 30d
directory_max_bytes 16MiB
lease_max_bytes 4MiB
inbox_max_consumers -1
```

AFTER, `limits --show`

```text
$ mashctl limits --show
[exit 0]
Limits in force

  Inbox size            16 MiB per endpoint (the oldest mail is dropped first)
  Inbox age             30 days  (older mail is dropped)
  Directory size        16 MiB
  Online-status size    4 MiB
  Readers per inbox     no limit

Change them under "limits" in ~/.config/mash/config.json, then raise the
inboxes to the new values:
  mashctl limits
```

AFTER, raising

```text
$ mashctl limits
[exit 0]
Raised the inbox size limit of 2 endpoints on tls://127.0.0.1:24222.

OWNER  GROUP  ENDPOINT  WAS     NOW
acme   demo   alice     16 MiB  32 MiB
              bob       16 MiB  32 MiB
```

AFTER, nothing to raise

```text
$ mashctl limits
[exit 0]
All 2 inboxes are at or above the configured limits. Nothing was changed.
```

AFTER, one endpoint

```text
$ mashctl limits acme/demo/bob
[exit 0]
The inbox of acme.demo.bob is already at the configured limit (32 MiB).
Nothing was changed.
```

What changed and why:

- P3, P6: config key names (`inbox_max_consumers -1`) are replaced by plain
  labels with units. `-1` becomes "no limit". Directory and online-status size
  stay because the person sets them. [confirm: what a "reader" means in the
  server: the number of consumers of an inbox]
- P2: the flat key `acme_team_alice` and raw bytes (`16777216`) are gone.
- The old default printed one `unchanged` line per endpoint; it is one sentence.
- No NEXT after `limits --show`: it states the next step in a sentence, which
  is a hint, not the next screen.
- `--show` connects to nothing, so it names no server.

### D6. permissions (text form)

What it does: shows what an endpoint's NATS user is allowed to publish and
subscribe to. Every endpoint has the same kind of set, for its address (docs).
Offline; it never connects. Today the default prints JSON (agents) and a
`note:` line; `--nsc` prints flags for the `nsc` tool.

Proposal: the default becomes the words below; `--json` prints the JSON; `--nsc`
is unchanged. `--detail` prints the exact subject lists.

BEFORE (catalog, perm), marks P5 P6 P7 P9:

```text
      "agents.mail.acme.*.*",
      "agents.prompt.acme.*.*",
      "agents.hb.acme.team.alice",
      "$JS.API.STREAM.INFO.inbox-acme_team_alice",
      "$JS.ACK.inbox-acme_team_alice.deliver.>",
      "$KV.mash-leases.acme_team_alice",
note: these are the permissions of the endpoint's NATS user: the owner-line set for its address.
```

AFTER

```text
$ mashctl permissions acme/demo/alice
[exit 0]
What acme.demo.alice is allowed to do

It may:
  - send mail, prompts and status messages to any endpoint of owner acme
  - receive prompts and status messages sent to acme.demo.alice
  - read its own inbox, and no other inbox
  - show that it is online, and see which acme endpoints are online
  - read the directory of endpoints

It may not send to, or read from, another owner's endpoints.

These are the same for every endpoint of owner acme. Use --detail for the
exact NATS subjects, or --nsc for the flags the nsc tool takes.
```

AFTER, `--detail` (first lines)

```text
$ mashctl permissions acme/demo/alice --detail
[exit 0]
What acme.demo.alice is allowed to do

(the list above, then:)

Subjects it may publish to
  agents.mail.acme.*.*
  agents.prompt.acme.*.*
  agents.status.acme.*.*
  agents.hb.acme.demo.alice
  ...
Subjects it may subscribe to
  agents.prompt.acme.demo.alice
  agents.status.acme.demo.alice
  agents.hb.acme.*.*
  ...
```

What changed and why:

- P7, P9: the JSON default was for agents. A person gets the meaning; the
  subject lists are in `--detail`.
- P6: JetStream, KV, ack and consumer subjects are not named in the default.
- "no other inbox" and "may not send to another owner" follow from the subject
  lists in the catalog (own inbox subjects only; owner in the mail subject).
  [confirm: that these two sentences are exactly right]
- `--nsc` output stays as it is today, because `nsc` reads it.

### D7. provision

What it does: writes only the directory record and the inbox for an endpoint
whose NATS user was made by someone else, from that user's public key. It does
not make a key or credentials. (See G.)

BEFORE (catalog, prov and prov_again), marks P1 P2 P6:

```text
provisioned acme.team.ivy: inbox inbox-acme_team_ivy, mail agents.mail.acme.team.ivy
mash: endpoint acme.team.ivy is already provisioned
usage: mashctl provision <owner.group.instance> --user U --account A [--note N] [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]...
--user is required
```

AFTER, `--help`

```text
$ mashctl provision --help
[exit 0]
mashctl provision - register an endpoint whose NATS user someone else made

Usage: mashctl provision ADDRESS --user KEY --account KEY [options]

Use this on a server where mashctl cannot make NATS users. For a server
that mashctl set up with adopt, use endpoint create instead.

Options
  --user KEY       the endpoint's NATS user public key (starts with U)
  --account KEY    the account's public key (starts with A)
  --role R         what the endpoint does
  --host H         the machine it runs on
  --describe TEXT  one sentence about it
  --tag K=V        add a tag (repeat for more)
  --note TEXT      a note kept with the endpoint

Example:
  mashctl provision acme/demo/alice --user UD7CJ5RS... --account AD7BK6VI...
```

AFTER, real

```text
$ mashctl provision acme/demo/alice --user UD7CJ5RS... --account AD7BK6VI...
[exit 0]
Registered endpoint acme.demo.alice on tls://127.0.0.1:24222.
  Its inbox is ready. mash made no key or credentials: they come from
  whoever made its NATS user.

NEXT: on alice's machine, see how to install credentials made elsewhere:
  mash credential install --help
```

What changed and why:

- P2, P6: the inbox name and the mail subject are not printed. "Inbox is
  ready" is the whole fact.
- NEXT names the machine and a command that exists; the line above it says who
  must act: mash cannot make the credentials on such a server.
  [confirm: how credentials from another maker are installed]
- `provision` has no `--dry-run` today (see Facts to confirm).

### D8. deprovision

What it does: removes an endpoint's directory record and inbox and revokes its
NATS user, after checks. With `--suspension ID --drained-to N` (both, from
`endpoint suspend`) it removes only if the inbox is still suspended at message N,
so no unread mail is lost. `--no-revoke` skips the revocation.

BEFORE (catalog, depro_plain, depro_susp, depro_norevoke), marks P1 P5 P6 P8:

```text
revoked user key UD7CJ5RS...: 1 server(s) acknowledged
deprovisioned acme.team.ivy (user key UD7CJ5RS... revoked in the account JWT)
warning: user key UD7CJ5RS... has no issue record, so a future-dated credential cannot be ruled out; revoking anyway
```

```text
deprovisioned acme.team.lee (--no-revoke: its NATS user is not revoked; revoke it with your operator tooling)
WARNING: --no-revoke: acme.team.lee's NATS user key UCODJ3XFUJKE6T4JPJXRSY6EYROK3J6RILGMAO5JRKCIBGQFIG2IJNBH is NOT revoked and can still connect, publish and receive unt...   (printed twice)
```

AFTER, `--help`

```text
$ mashctl deprovision --help
[exit 0]
mashctl deprovision - remove an endpoint made with provision

Usage: mashctl deprovision ADDRESS [options]

Revokes the endpoint's NATS user (it stops working), then removes the
endpoint and its inbox. Unread mail is lost unless you drain it first.

Options
  --suspension ID    the suspension id printed by endpoint suspend
  --drained-to N     the last message number you read (needs --suspension)
  --user KEY         revoke this NATS user instead of the one on record
  --no-revoke        remove the endpoint but do not revoke its key
  --servers N        how many servers must confirm (else MASH_SERVERS)
  --sys-creds FILE   system account credentials (else MASH_SYS_CREDS)

Example, without losing mail:
  mashctl endpoint suspend acme/demo/alice
  (read the stored mail, then)
  mashctl deprovision acme.demo.alice --suspension ID --drained-to 41
```

AFTER, plain

```text
$ mashctl deprovision acme.demo.alice
[exit 0]
Removed endpoint acme.demo.alice on tls://127.0.0.1:24222.
  Its NATS user no longer works: all 1 servers confirmed it.
  Its inbox is deleted, with any unread mail.
warning: mash has no record of issuing this key, so it cannot check that
no credential for it is dated in the future. The key was revoked anyway.

NEXT: delete any copy of alice's credentials file on its machine.
```

AFTER, after a drain

```text
$ mashctl deprovision acme.demo.alice --suspension 63afd156... --drained-to 41
[exit 0]
Removed endpoint acme.demo.alice on tls://127.0.0.1:24222.
  Mail was read up to message 41 before it was removed; none was lost.
  Its NATS user no longer works: all 1 servers confirmed it.

NEXT: delete any copy of alice's credentials file on its machine.
```

AFTER, `--no-revoke` (the warning once)

```text
$ mashctl deprovision acme.demo.alice --no-revoke
[exit 0]
Removed endpoint acme.demo.alice on tls://127.0.0.1:24222. KEY NOT REVOKED.
  Its inbox is deleted, with any unread mail. Its key was not revoked.

warning: alice's key still works. Until it is revoked, a copy of alice's
credentials can still sign in, send and read mail. Revoke it with the
tools of whoever runs the server.

NEXT: ask whoever runs the server to revoke alice's key, whose public key is:
  UD7CJ5RSKIOORP6QZS6RB5I3JD2LX4IOHRXLC6FBZW6MVYGYU62MBPNS
```

What changed and why:

- P1, P5, P6, P8: one "no longer works" line instead of two; JWT, issue record
  and "revoked in the account JWT" removed; the warning is printed once.
- P4: NEXT is true: deprovision of an endpoint made by `endpoint create` leaves
  its credentials file in the config folder (catalog, depro_hal).
- `deprovision` has no `--dry-run` today (see Facts to confirm).

---

## E. Refusals and errors

Form (rule 10): the first line is `error:` and says what is wrong; the second
line is `fix:` and says what to do, as one sentence ending in a colon with the
command on its own indented line beneath it (a fix that needs no command is one
sentence ending in a full stop); nothing else by default. Every line is 80
columns or less, and every command line is 78 or less. The machine code
(`file_exists`, `key_unchanged`, ...) is printed only with `--json` and
`--detail`. Exit codes are unchanged (0 done, 1 refused, 2 usage, 5 partial,
6 error, and for `audit` 3 could not finish) except that a plain URL refused
for TLS now exits 2 in every command (contract REQ-014). A value that failed a check is named but
never printed back, so a secret pasted into the wrong place is not echoed.
A fix that sets configuration names the file first, with its resolved path
(`~/.config/mash/config.json`, or the folder `MASH_CONFIG_DIR` names); a
variable set with `export` is offered only as a test in the current shell,
because it does not reach a session started anywhere else.

With `--detail`, the machine code is added as a third line, for example:

```text
$ mashctl endpoint reissue acme/demo/bob --public-key bob.pub --detail
[exit 1]
error: bob.pub is already bob's public key, so nothing would change.
fix: make a new key on bob's machine:
  mash key create --out bob2.key
code: key_unchanged
```

### E1. Usage errors (exit 2, every command)

A usage error prints three parts: the one-line `error:`; a `fix:` sentence
with a complete command beneath it (the person's own address and values when
they typed them, `<placeholder>` when mash cannot know them); and the usage in
80 columns or less. It never prints the whole command list.

BEFORE (catalog, er_nokey, label_noargs, ii_bad), marks P1 P5 P6:

```text
mashctl: endpoint reissue: error (key_missing): a reissue needs a new key: write --public-key FILE (the endpoint's new public key, from mash key create) or --make-key (make the key on this host)
mashctl: label: want an address (owner.group.instance) and at least one label
usage: mashctl label <owner.group.instance> k=v [k=v]... [--dry-run]
       mashctl unlabel <owner.group.instance> k [k]... [--dry-run]
```

AFTER

```text
$ mashctl endpoint reissue acme/demo/bob
[exit 2]
error: a reissue needs a new key: give --make-key or --public-key FILE.
fix: make the new key here:
  mashctl endpoint reissue acme/demo/bob --make-key
usage: mashctl endpoint reissue ADDRESS (--make-key | --public-key FILE)

$ mashctl endpoint reissue
[exit 2]
error: no endpoint address given.
fix: name the endpoint to reissue:
  mashctl endpoint reissue <owner>/<group>/<name> --make-key
usage: mashctl endpoint reissue ADDRESS (--make-key | --public-key FILE)

$ mashctl endpoint set acme/demo/alice
[exit 2]
error: nothing to change: give at least one of --role, --host, --describe,
       --tag, --untag, --capability.
fix: for example, set a role:
  mashctl endpoint set acme/demo/alice --role <role>
usage: mashctl endpoint set ADDRESS [options]

$ mashctl endpoint set acme/demo/alice --policy x
[exit 2]
error: unknown option --policy.
fix: see the options:
  mashctl endpoint set --help
usage: mashctl endpoint set ADDRESS [options]

$ mashctl endpoint frob
[exit 2]
error: unknown command "endpoint frob".
fix: see the commands:
  mashctl endpoint --help
usage: mashctl endpoint <create|list|show|set|reissue|delete|suspend|resume>

$ mashctl frobnicate
[exit 2]
error: unknown command "frobnicate".
fix: see the commands:
  mashctl --help
usage: mashctl <command> [options]

$ mashctl issued
[exit 2]
error: issued needs a command: list or check.
fix: for example, check what is connected:
  mashctl issued check
usage: mashctl issued <list|check> [options]

$ mashctl inbox
[exit 2]
error: inbox needs a command: inspect.
fix: show the mail stored for one endpoint:
  mashctl inbox inspect <owner>/<group>/<name> --limit 5
usage: mashctl inbox inspect ADDRESS [--from N] [--to N] [--limit N]

$ mashctl endpoint delete bob
[exit 2]
error: "bob" is not an endpoint address.
fix: give the whole address, owner, group and name:
  mashctl endpoint delete <owner>/<group>/<name>
usage: mashctl endpoint delete ADDRESS [--force] [--no-revoke] [--dry-run]
```

Note: the address check names the argument and does not print its value in the
real command. The example above shows `bob` because the test typed it; the real
message is `error: the address is not an endpoint address.` [confirm: the rule
that values are never printed back applies to addresses, or only to keys].

Other usage errors, same form:

```text
$ mashctl endpoint reissue acme/demo/bob --make-key --public-key bob.pub
[exit 2]
error: give --make-key or --public-key, not both.
fix: choose one; to make the key here:
  mashctl endpoint reissue acme/demo/bob --make-key
usage: mashctl endpoint reissue ADDRESS (--make-key | --public-key FILE)

$ mashctl endpoint reissue --all --public-key bob.pub
[exit 2]
error: --all and --changed make the new keys on this machine, so they cannot
       take --public-key.
fix: leave out --public-key; to see what would change:
  mashctl endpoint reissue --all --dry-run
usage: mashctl endpoint reissue (--all | --changed) [--yes] [--dry-run]

$ mashctl endpoint create bob
[exit 2]
error: an endpoint address has three parts: owner, group and name.
fix: give all three:
  mashctl endpoint create <owner>/<group>/bob --make-key
usage: mashctl endpoint create OWNER/GROUP/NAME (--make-key | --public-key F)

$ mashctl endpoint create demo/bob --make-key
[exit 2]
error: "demo/bob" has two parts; an address has three, owner/group/name.
fix: add the owner:
  mashctl endpoint create <owner>/demo/bob --make-key
usage: mashctl endpoint create OWNER/GROUP/NAME (--make-key | --public-key F)

$ mashctl endpoint create bob --owner acme --make-key
[exit 2]
error: no group given. Every endpoint belongs to a group.
fix: add the group:
  mashctl endpoint create acme/<group>/bob --make-key
usage: mashctl endpoint create OWNER/GROUP/NAME (--make-key | --public-key F)

$ mashctl endpoint create acme/demo/bob --group ops --make-key
[exit 2]
error: the address says group demo, but --group says ops.
fix: make them agree; to use the group in the address:
  mashctl endpoint create acme/demo/bob --make-key
usage: mashctl endpoint create OWNER/GROUP/NAME (--make-key | --public-key F)

$ mashctl endpoint create demo/bob --group demo --make-key
[exit 2]
error: no owner given.
fix: add the owner:
  mashctl endpoint create <owner>/demo/bob --make-key
usage: mashctl endpoint create OWNER/GROUP/NAME (--make-key | --public-key F)

$ mashctl endpoint create acme/demo --make-key
[exit 2]
error: no endpoint name given.
fix: add the name:
  mashctl endpoint create acme/demo/<name> --make-key
usage: mashctl endpoint create OWNER/GROUP/NAME (--make-key | --public-key F)

$ mashctl endpoint create acme/demo/bob --make-key --tag "bad key=x"
[exit 2]
error: a tag name must start with a letter, then up to 31 letters, digits,
       or _ . -  (the value is not shown).
fix: write a tag like this:
  mashctl endpoint create acme/demo/bob --make-key --tag team=ui
usage: mashctl endpoint create OWNER/GROUP/NAME (--make-key | --public-key F)

$ mashctl issued check --servers 0
[exit 2]
error: --servers must be a whole number, 1 or more.
fix: give the number of servers:
  mashctl issued check --servers <number>
usage: mashctl issued check [--sys-creds FILE] [--servers N]

$ mashctl deprovision acme.demo.alice --drained-to 3
[exit 2]
error: --drained-to and --suspension go together.
fix: add the suspension id that endpoint suspend printed:
  mashctl deprovision acme.demo.alice --suspension <id> --drained-to 3
usage: mashctl deprovision ADDRESS [--suspension ID --drained-to N]

$ mashctl deprovision acme.demo.alice --user U... --drained-to 3 --suspension x
[exit 2]
error: --user cannot be combined with --drained-to.
fix: leave out --user:
  mashctl deprovision acme.demo.alice --suspension x --drained-to 3
usage: mashctl deprovision ADDRESS [--suspension ID --drained-to N]

$ mashctl provision acme.demo.alice
[exit 2]
error: --user is required.
fix: add the user's and the account's public keys:
  mashctl provision acme.demo.alice --user <user-key> --account <account-key>
usage: mashctl provision ADDRESS --user KEY --account KEY

$ mashctl provision acme.demo.alice --user notakey --account A...
[exit 2]
error: --user is not a NATS user public key (it starts with U; the value
       is not shown).
fix: give the user's public key:
  mashctl provision acme.demo.alice --user <user-key> --account <account-key>
usage: mashctl provision ADDRESS --user KEY --account KEY

$ mashctl limits nobody
[exit 2]
error: the address is not an endpoint address.
fix: give the whole address, owner, group and name:
  mashctl limits <owner>/<group>/<name>
usage: mashctl limits [ADDRESS...] | mashctl limits --show
```

What changed and why:

- P5, P6: the error is one line under 80 columns, no code in parentheses (it
  was `error (key_missing)`), and the usage fits 80 columns.
- P9: no full command list under an unknown command, as in 0.5.5.
- P4: the `fix:` command replaces the long advice that was inside the error
  line; it is complete and pasteable, with placeholders where mash cannot know.
- Old codes covered: `usage`, `unknown_flag`, `address_missing`,
  `address_invalid`, `address_ambiguous`, `naming_disagrees`, `group_missing`,
  `owner_missing`, `name_missing`, `key_both`, `key_missing`,
  `bulk_needs_make_key`, `labels_invalid`, `servers_invalid`. `labels_invalid`
  is called "tag" in the text.

### E2. Every command that connects to a server (target and connection)

BEFORE (catalog, env_plain_create, noinit_*, creds_*, down_*), marks P1 P5 P6:

```text
mashctl: endpoint create: error (server): mashctl: mash: refusing a plain nats:// connection to "127.0.0.1": every mash connection requires TLS, to any host (loopback included); set insecure_plaintext (MASH_INSECURE_PLAINTEXT=1) to allow plain text; the fix is to use a tls:// URL with the server's certificate (tls_ca for a private CA)
mashctl: no owner (config.json "owner" or MASH_OWNER)
mashctl: admin commands need MASH_URL and MASH_ADMIN_CREDS
mashctl: endpoint list: mashctl: connect tls://127.0.0.1:20001: nats: no servers available for connection (check MASH_URL and that at least one listed server is up)
```

AFTER

```text
$ MASH_URL=nats://127.0.0.1:24222 mashctl endpoint list
[exit 2]
error: refusing to connect to 127.0.0.1 without encryption (the address
       starts with nats://).
fix: use a tls:// address: change "url" in ~/.config/mash/config.json (or
     MASH_URL, which overrides it). For a server with its own certificate
     authority, also set "tls_ca" there to its file.

$ mashctl endpoint list
[exit 1]
error: mashctl is not connected to a server yet.
fix: connect mashctl to a server first; for how, run:
  mashctl adopt --help

$ mashctl endpoint create acme/demo/bob --make-key
[exit 1]
error: mashctl does not have this server's signing key, so it cannot make
       endpoints.
fix: connect mashctl to the server first; for how, run:
  mashctl adopt --help

$ mashctl endpoint list
[exit 6]
error: mashctl has no administrator credentials for this server.
fix: connect mashctl to the server; for how, run:
  mashctl adopt --help

$ mashctl endpoint list
[exit 6]
error: the administrator credentials file is missing: it cannot be opened.
fix: restore the file, or connect again; for how, run:
  mashctl adopt --help

$ mashctl endpoint list
[exit 6]
error: the administrator credentials file can be read by other users
       (mode 0644).
fix: make the file private to you:
  chmod 600 ~/.config/mash/server/mashadmin.creds

$ mashctl endpoint list
[exit 6]
error: cannot reach the server tls://127.0.0.1:24222.
fix: check that the server is running, then see where mashctl stands:
  mashctl status

$ mashctl endpoint create acme/demo/bob --make-key
[exit 6]
error: cannot reach the server tls://127.0.0.1:24222. Nothing was changed.
fix: start the server, then run the same command again.

$ mashctl audit
[exit 3]
error: cannot reach the server tls://127.0.0.1:24222.
fix: check that the server is running, then see where mashctl stands:
  mashctl status

$ mashctl inbox inspect acme/demo/bob
[exit 3]
error: cannot reach the server tls://127.0.0.1:24222.
fix: check that the server is running, then see where mashctl stands:
  mashctl status

$ mashctl issued check
[exit 2]
error: cannot reach the server tls://127.0.0.1:24222.
fix: check that the server is running, then see where mashctl stands:
  mashctl status

$ mashctl endpoint delete acme/demo/bob
[exit 6]
error: cannot reach the server tls://127.0.0.1:24222. Nothing was changed.
fix: start the server, then run the same command again.
```

Warnings (not refusals; the command goes on). They replace the two long
`warning:` lines of 0.5.5 and are printed only when they matter.

```text
$ MASH_OWNER=other mashctl endpoint list
warning: MASH_OWNER=other overrides the owner in your configuration (acme).
         mashctl is acting as owner other. Unset MASH_OWNER to use acme.

$ MASH_URL=tls://localhost:24222 mashctl endpoint list     (inside a session)
warning: MASH_URL overrides your configuration and this shell looks like it
         is inside a mash session, so mashctl is acting on that session's own
         server, tls://localhost:24222. To act on another server, run outside
         the session, or unset the MASH_* variables first.
```

What changed and why:

- P1: the preamble is gone. A server problem names the server in the error.
- P5, P6: the TLS refusal (310 columns, with `insecure_plaintext` and `tls_ca`
  explained nowhere) is two lines. The test-server escape hatch,
  `MASH_INSECURE_PLAINTEXT=1`, is documented in `--detail` and in
  docs/operations.md, not offered in the default text.
- The wrapper text `mashctl: endpoint list: mashctl: mash:` (the same program
  named three times) is gone.
- "not initialised" now means "not connected to a server yet": the fix points
  to `mashctl adopt --help`, because `init --local` is removed.
- Exit codes are as in the catalog (create 6 on a server problem, `list` 6,
  `audit` and `inbox inspect` 3, `issued check` 2, `init` 1). The TLS refusal
  is 2 in every command (contract REQ-014).
- `status` (part 1) shows the warnings above on request in plain words.

### E3. endpoint create (refusals)

BEFORE (docs/mashctl.md table, catalog cases 20 to 34), marks P5 P6 P7:

```text
mashctl: endpoint create: error (group_missing): an endpoint needs a group; write --group dev or alice/dev/design
mashctl: endpoint create: refused (public_key_issued): the public key UASKC43D... was already issued to another address; each key is used at one address: make a new key for this endpoint with: mash key create --out alice2.seed
```

AFTER

```text
$ mashctl endpoint create acme/demo/bob --public-key bob.pub
[exit 1]
# when: a part of the address is empty or has a bad character
error: the name is empty or has a character mash does not allow.
fix: use letters, digits, - and _ in each part of the address.

# when: the server's naming rule refuses the name
error: this server's naming rule does not accept that name.
fix: ask whoever runs the server what the rule is, or pick another name.

# when: the endpoint already exists
error: the endpoint acme.demo.bob already exists.
fix: to give it a new key, reissue it:
  mashctl endpoint reissue acme/demo/bob --make-key

# when: the address was deleted earlier
error: the address acme.demo.bob was deleted, and a deleted address is
       never used again.
fix: choose another name, for example bob2.

# when: the --public-key file cannot be read
error: bob.pub cannot be read.
fix: check the file name and that you can read it.

# when: the file is not a NATS user public key
error: bob.pub does not hold a NATS user public key.
fix: make a key pair on bob's machine:
  mash key create --out bob.key

# when: the public key was issued to another endpoint
error: this public key already belongs to another endpoint.
fix: make a new key on bob's machine:
  mash key create --out bob.key

# when: the public key was revoked
error: this key was revoked, so it can never be used again.
fix: make a new key on bob's machine:
  mash key create --out bob.key

# when: the key kept from an unfinished run was revoked
error: the key waiting from an earlier, unfinished run was revoked.
fix: delete the key left by the unfinished run:
  rm ~/.config/mash/acme.demo.bob.creds.pending
Then run the same command again.

# when: the token file already exists
error: ./acme.demo.bob.token already exists and is never overwritten.
fix: name another file with --token-out, or delete this one.

# when: another key's credentials file is in the way
error: a credentials file for a different key is already at
       ~/.config/mash/acme.demo.bob.creds.
fix: move that file aside:
  mv ~/.config/mash/acme.demo.bob.creds ~/.config/mash/acme.demo.bob.old
Then run the command again.

# when: no config folder is set
error: mashctl has no folder to write bob's credentials to.
fix: in this shell, point mashctl at a folder you own, then run it again:
  export MASH_CONFIG_DIR=<folder>

# when: the config folder is writable by other users
error: mashctl cannot write to ~/.config/mash: other users can write there.
       Nothing was changed.
fix: make the folder private to you:
  chmod 700 ~/.config/mash

# when: a server did not answer the revocation check
error: mashctl could not check whether this key was revoked: a server did
       not answer. Nothing was changed.
fix: bring the server back, then run the same command again.
```

Exit codes in this block: invalid name 1, naming rule 1, exists 1, deleted
address 1, unreadable key file 1, not a public key 1, key already used 1,
revoked 1, waiting key revoked 1, token file exists 1, credentials file
exists 1, no folder 1, folder not safe 6, could not check revocation 6. The
`error:` text does not repeat the exit code.

Codes covered: `name_invalid`, `name_rule`, `address_taken`, `key_retired`,
`public_key_unreadable`, `public_key_invalid`, `public_key_issued`,
`key_revoked`, `staged_key_revoked`, `file_exists`, `creds_exists`,
`no_config_dir`, `write`, `revocation_unknown`, `no_account_key` (E2).

### E4. endpoint reissue (refusals)

```text
$ mashctl endpoint reissue acme/demo/zed --make-key
[exit 1]
error: there is no endpoint acme.demo.zed.
fix: see which endpoints exist:
  mashctl endpoint list

$ mashctl endpoint reissue acme/demo/bob --public-key bob.pub
[exit 1]
error: bob.pub is already bob's public key, so nothing would change.
fix: make a new key on bob's machine:
  mash key create --out bob2.key

$ mashctl endpoint reissue acme/demo/bob --public-key bob2.pub
[exit 1]
error: ./acme.demo.bob.token already exists and is never overwritten.
fix: name another file with --token-out, or delete this one.

$ mashctl endpoint reissue acme/demo/alice --public-key bob2.pub
[exit 1]
error: this public key already belongs to another endpoint.
fix: make a new key on alice's machine:
  mash key create --out alice2.key

$ mashctl endpoint reissue acme/demo/bob --make-key
[exit 1]
error: the credentials file ~/.config/mash/acme.demo.bob.creds is not
       bob's current one. Nothing was changed.
fix: move that file aside:
  mv ~/.config/mash/acme.demo.bob.creds ~/.config/mash/acme.demo.bob.old
Then run again, or use --public-key with a key made on bob's machine.

$ mashctl endpoint reissue acme/demo/bob --make-key
[exit 1]
error: bob's credentials file cannot be read or understood. Nothing was
       changed.
fix: restore the file, or move it aside and run again.

$ mashctl endpoint reissue acme/demo/bob --make-key
[exit 1]
error: bob's old key has a token dated in the future, so revoking the key
       would not cover it. Nothing was changed.
fix: check this machine's clock, then run the command again.

$ mashctl endpoint reissue acme/demo/bob --make-key
[exit 1]
error: another reissue of acme.demo.bob is running (process 85044 on bf2).
fix: wait for it to finish, then run again. A killed run leaves nothing to
     clean up.

$ mashctl endpoint reissue acme/demo/bob --make-key
[exit 1]
error: an old lock folder blocks the reissue of acme.demo.bob:
       ~/.config/mash/acme.demo.bob.creds.lock
fix: if no reissue is running, delete that folder once:
  rm -r ~/.config/mash/acme.demo.bob.creds.lock
Then run the command again.

$ mashctl endpoint reissue acme/demo/bob --make-key
[exit 6]
error: another reissue changed bob's key while this one ran, so the file
       this run wrote was removed. The old key stays revoked.
fix: run the same command again.

$ mashctl endpoint reissue acme/demo/bob --make-key
[exit 6]
error: only 1 of 2 servers answered. Nothing was revoked.
fix: bring the missing server back, or pass --servers N if the count is
     wrong. Then run the same command again.

$ mashctl endpoint reissue acme/demo/bob --make-key
[exit 6]
error: the server did not answer, or answered with an error. Nothing was
       changed.
fix: check that the server is running:
  mashctl status
Then run the command again.
```

In a bulk run (`--all`, `--changed`) a refusal is shown in the RESULT column
(see B3) with the same words as the `error:` line above, and a skipped endpoint
that has no credentials file here is shown under "Skipped" (exit 0). The
unreadable file is exit 6 in a bulk run and exit 1 for one endpoint.

Codes covered: `endpoint_missing`, `key_unchanged`, `file_exists`,
`public_key_issued`, `creds_mismatch`, `creds_unreadable`, `future_issued`,
`reissue_busy` (both forms), `superseded`, `servers`, `server`,
`no_local_creds`, plus (from E3) `key_revoked`, `staged_key_revoked`,
`key_retired`, `no_account_key`, `no_config_dir`, `write`,
`revocation_unknown`, `key_both`, `key_missing`, `bulk_needs_make_key`.

### E5. endpoint delete (refusals)

```text
$ mashctl endpoint delete acme/demo/zed
[exit 1]
error: there is no endpoint acme.demo.zed, and it was never created.
fix: see which endpoints exist:
  mashctl endpoint list
If it was removed only in part, finish the removal:
  mashctl deprovision acme.demo.zed

$ mashctl endpoint delete acme/demo/bob
[exit 1]
error: only 1 of 2 servers answered, so the key cannot be shown to be
       revoked. Nothing was changed.
fix: bring the missing server back and run the command again. If the
     count is wrong, pass --servers N.

$ mashctl endpoint delete acme/demo/bob
[exit 1]
error: bob's key has a token dated in the future, so revoking the key would
       not cover it. Nothing was changed.
fix: check this machine's clock, then run the command again.

$ mashctl endpoint delete acme/demo/bob
[exit 1]
error: the server could not confirm that bob's key was revoked. Nothing was
       removed.
fix: run the same command again; revoking twice is safe.

$ mashctl endpoint delete acme/demo/bob
[exit 1]
error: the record for acme.demo.bob belongs to a different key than the one
       mash issued. Nothing was changed.
fix: list what the server holds:
  mashctl audit
Then remove the endpoint with the key you mean:
  mashctl deprovision acme.demo.bob --user <public-key>

$ mashctl endpoint delete acme/demo/bob
[exit 1]
error: mash cannot show that its record of bob's key is complete (it has
       been recorded too many times). Nothing was changed.
fix: revoke the key with the tools of whoever runs the server, then:
  mashctl endpoint delete acme/demo/bob --no-revoke
```

Codes covered: `not_found`, too few servers, future issue time, revocation not
confirmed, record belongs to another key, history not complete; plus
`servers_invalid` and `address_invalid` (E1).

### E6. Other commands

```text
$ mashctl endpoint show acme/demo/zed
[exit 1]
error: there is no endpoint acme.demo.zed.
fix: see which endpoints exist:
  mashctl endpoint list

$ mashctl endpoint set acme/demo/zed --role x
[exit 1]
error: there is no endpoint acme.demo.zed.
fix: see which endpoints exist:
  mashctl endpoint list

$ mashctl endpoint suspend acme/demo/zed
[exit 1]
error: acme.demo.zed has no inbox, so there is nothing to suspend.
fix: see which endpoints exist:
  mashctl endpoint list

$ mashctl inbox inspect acme/demo/zed
[exit 1]
error: there is no endpoint acme.demo.zed.
fix: see which endpoints exist:
  mashctl endpoint list

$ mashctl audit --compare nope.json
[exit 3]
error: the file nope.json does not exist.
fix: save an earlier audit first:
  mashctl audit --json > audit-earlier.json

$ mashctl provision acme/demo/alice --user UD7CJ5RS... --account AD7B...
[exit 1]
error: acme.demo.alice is already registered.
fix: to change how it is described, use set:
  mashctl endpoint set acme/demo/alice --role <role>

$ mashctl deprovision acme.demo.zed
[exit 1]
error: there is no endpoint acme.demo.zed.
fix: see which endpoints exist:
  mashctl endpoint list

$ mashctl deprovision acme.demo.bob --suspension deadbeef --drained-to 0
[exit 1]
error: that is not the suspension you drained. Nothing was revoked.
fix: read the current suspension:
  mashctl endpoint suspended acme/demo/bob

$ mashctl deprovision acme.demo.bob
[exit 1]
error: a credential for bob's key is dated after the revocation time, so a
       revocation would not cover it. Nothing was revoked.
fix: check this machine's clock, then run the command again.

$ mashctl deprovision acme.demo.bob
[exit 1]
error: mash cannot read its record of bob's key, so it cannot check it.
       Nothing was revoked.
fix: check the server's records:
  mashctl audit
Then run the command again.

$ mashctl deprovision acme.demo.bob
[exit 1]
error: the record for bob names a different key than --user. Nothing was
       revoked.
fix: run without --user to use the key on record.
```

Codes covered by E1 to E6: every code in the docs/mashctl.md table
(`name_invalid` to `no_local_creds`) and the plain refusals listed under it.

---

## F. `--dry-run`: every command that has it

Commands with `--dry-run` today: `endpoint create`, `endpoint reissue` (one and
bulk), `endpoint delete`, `label` and `unlabel` (now `endpoint set`), `adopt`,
`mash key create`, `mash credential create` (now `install`). Added in this
proposal: `endpoint suspend` and `endpoint resume`. Shapes already shown above:

- reissue, one: B1 (`--make-key`) and B2 (`--public-key`).
- reissue, bulk: B3, which also prints the table and the skipped list.
- delete: B4; `--force`: B5.
- set: C2. suspend: C3.

The two not shown above (`endpoint create` and `adopt`, `mash key create`,
`mash credential install`) belong to part 1.

The form every dry-run follows:

```text
Would <verb> <thing> on tls://127.0.0.1:24222:
  - <one step per line, plain words>
Nothing was changed.
```

Rules for the form: a dry-run connects when the real command would, so it
reports the real refusals; it prints `Would` sentences in the same order as the
real output; it prints no NEXT; it exits 0 when the real command would run.

---

## G. A server where someone else makes the NATS users

Why this matters: mashctl makes the endpoint's NATS user itself, and it can do
that only on a server it can sign for: an operator-mode server whose keys
`mashctl adopt` imported (ADR 0052). A hosted NATS service (Synadia, which now
needs a paid account) or any server run by another team keeps the operator key
and decides who may have a NATS user. There mashctl cannot sign users and
cannot revoke them.

Which commands apply:

- `mashctl provision` registers the endpoint's directory record and inbox for
  a NATS user someone else made (you give `--user` and `--account`).
- `mashctl deprovision` removes such an endpoint. It revokes the user when
  mashctl holds the operator key; on a server where it does not, use
  `--no-revoke` and ask the owner of the server to revoke the key.
- `mashctl endpoint delete --no-revoke` is the same for an endpoint made with
  `endpoint create` whose key must be revoked by someone else.
- `mashctl endpoint create` and `endpoint reissue` need the signing key and are
  not available. Credentials come from whoever makes the NATS user.
- Commands that only read or change mash's own records work with the
  administrator credentials alone: `endpoint list`, `show`, `set`, `suspend`,
  `resume`, `inbox inspect`, `audit`, `limits`. `issued check` also needs the
  system account credentials. [confirm: that all of these run without `adopt`'s
  keys, and how mashctl is configured on such a server]

Proposed note, as printed at the end of `mashctl provision --help` and
`mashctl deprovision --help` (replaces the one line used above):

```text
$ mashctl provision --help
...
Not for servers that mashctl set up. Use this when the NATS users are made by
someone else, such as a hosted NATS service. mash does not make a key or
credentials here, and mashctl cannot revoke the user unless it holds the
server's operator key. Then use deprovision --no-revoke and ask the server's
owner to revoke the key.
```

What changed and why:

- Feedback 14 (P9): the old text said "revoke it with your operator tooling".
  The new note says who can revoke, and when.
- Nothing is promised for Synadia beyond the commands above; no unreleased
  integration is named.

---

## Choices to confirm

Decisions the rules left open. Each is one line.

1. `endpoint set` flags are `--tag`, `--untag`, `--capability` and
   `--remove-capability`; reason: "label" and "cap" are banned in default output
   and "tag" is the word people use. If the old names stay, the two one-line
   explanations in C1 notes are needed in `--help`.
2. `--tag` replaces `--label` on `endpoint create` and `provision` too, for the
   same reason (part 1 must agree).
3. `suspend`, `resume`, `suspended` move under `endpoint`; reason: one place for
   everything about an endpoint. They keep exit codes 0, 1, 2, 3.
4. `mashctl list` is removed, not aliased; reason: hard cut (no old-state
   tolerance), and `endpoint list` shows everything it showed.
5. `provision` and `deprovision` keep their names and move to "Advanced";
   reason: a rename adds nothing and the list stays short.
6. Dry-runs print no NEXT; reason: rule 11 says they end `Nothing was changed.`,
   and the next screen is not a step of a guide.
7. Bulk reissue (`--all`, `--changed`) no longer needs `--make-key`; reason:
   `--make-key` was the only form it accepted, so the flag carried no choice.
8. The partial-reissue NEXT names a single-endpoint reissue, not `--changed`;
   reason: after the file is moved aside, `--changed` skips that endpoint.
9. `permissions` default prints words; `--json` prints what it printed before;
   `--nsc` is unchanged; reason: rule 15 (the default is for a person).
10. `mashctl list`, `label`, `unlabel`, `set-meta`, `suspend`, `resume` get
    removed-command messages (exit 2), not silent aliases; reason: same as 4.
11. `audit`, `issued check` and `endpoint suspended` print the verdict first
    (`OK`, `PROBLEM`, `INCOMPLETE`, `SUSPENDED`, `NOT SUSPENDED`); reason: the
    exit code alone is not visible to a person, and a person reads the first
    line first. A `--no-revoke` delete or deprovision ends its first line with
    `KEY NOT REVOKED` for the same reason.
12. Usage errors print `error:`, a `fix:` sentence with a complete command
    beneath it, and the usage line; reason: one shape for every refusal
    (R-nextcase), the command is pasteable, and a placeholder stands for what
    mash cannot know.
13. `--detail` never names a JWT; it says "token". Reason: the rules allow JWT
    in `--detail` and docs, but nothing in this document needs it.
14. A repeat `endpoint delete` of a retired address exits 0 and says so; of an
    address that never existed exits 1; reason: the catalog behaves this way and
    a killed delete must finish when run again.
15. Config folder shown as `~/.config/mash`; reason: a person needs a real
    path; the actual folder follows `MASH_CONFIG_DIR`.
16. The administrator key is listed in `issued list` with `-` for owner and
    group; reason: it is a key mash issued, and hiding it would hide a key that
    can sign in.
17. Review 2 asked for exit 1, not 2, for a refused plain URL. Exit 2 is kept in
    every command; reason: contract REQ-014 fixes it as 2, and mashctl and mash
    must agree. The `error:` text names a refusal, not a usage mistake.
18. `mashctl status` is the new command that part 1 defines (the server mashctl
    uses and whether it works, in plain words, replacing the old preamble). Part
    2 only points to it, in the fix for a server that cannot be reached.
19. In a grouped table the name column is headed `ENDPOINT` (`bob`) and the
    dotted address, where shown, `PATH` (`acme.demo.bob`), following the
    maintainer's mockup. Part 1's endpoint list uses the same headers, so the
    two parts now agree. No table here gained a `PATH` column: each already
    shows owner and group, so the name alone identifies the row.
20. A usage error's `fix:` command uses the person's own values when they typed
    them and `<owner>/<group>/<name>`-style placeholders when they did not.
21. A fix that needs a file removed or moved prints the exact `rm`, `rmdir`
    or `mv` command on its own line, with the full path. [confirm: that a person
    should be handed these commands; the alternative is a sentence only]
22. `Removed`/`Registered`/`Reissued`/`Deleted`/`Suspended` are kept as the verbs
    of those actions; `Created` is used for new keys and tokens made inside
    them.
23. A dry-run that ends "Add --yes to do it." is not a NEXT (rule 11); the
    flag is named, not a command line, because the command the person typed is
    already on their screen.

## Facts to confirm

1. [confirm: names of the setup script and skill that `docs/guides/nats-local.md`
   will name; A5 says "the setup script or skill it names".]
2. [confirm: what `mashctl unzone` did in 0.5.0; the catalog treats it as an
   unknown verb, so it gets the unknown-command message, not a removal message.]
3. [confirm: characters allowed in an address part; E3 says letters, digits,
   `-` and `_`.]
4. [confirm: what "name rule" is and whether it will stay; the feedback
   (point 17) hopes it is not the legacy one. E3 keeps one generic message.]
5. [confirm: `endpoint delete` of an address with no server record and no
   credentials file; docs say exit 1 ("refuses"), the catalog says exit 0 when
   the address was retired. B5 uses the catalog for retired and exit 1 for
   never created.]
6. [confirm: `--changed` skips an endpoint that has no credentials file here
   (B3 relies on it).]
7. [confirm: `deprovision` and `provision` have no `--dry-run`; the docs list
   none. Proposal: add one to each.]
8. [confirm: deprovision removes the inbox and the record; the docs say the
   user is revoked and the "record and inbox" go, but the exact steps are in
   operations.md.]
9. [confirm: what the "readers per inbox" limit counts (`inbox_max_consumers`).]
10. [confirm: the `--detail` fields for `issued list` and `permissions`.]
11. [confirm: whether values given as addresses are "never printed back"; E1
    note.]
12. [confirm: how mashctl is configured without `adopt` on a server where mash
    cannot sign users (section G).]
13. [confirm: the config folder path and the file name for credentials
    (`~/.config/mash/acme.demo.bob.creds`) match part 1.]
14. [confirm: `endpoint suspend` prints the number of stored messages
    (`msgs` today) and `Last message number` is the old `last_seq`.]
15. [confirm: mashctl's configuration file is `~/.config/mash/config.json`, or
    the folder named by `MASH_CONFIG_DIR`, and holds `url`, `tls_ca` and
    `limits` (E2, D5); part 1 shows the same path.]
16. [confirm: `mashctl adopt --help` is the pointer to print when mashctl has no
    server (a full `adopt` command needs the operator's files, which mash cannot
    name).]
17. [confirm: `mash who --detail` shows tags (C1 help text).]
18. [confirm: `rm -r` of the lock folder named in E4 is safe when no reissue is
    running.]

## Changes in DRAFT2

By rule (counts are blocks or lines changed in AFTER text):

- R-paste (about 70 lines): every `NEXT:` and `fix:` that glued a command into
  prose now ends its sentence with a colon and puts the command on its own
  indented line (A5 removed-command messages, B2, B3, B4, D1 `--strict`, D3,
  D5, D7, D8, all of E). Commands are complete; the 24 usage errors of E1 each
  gained a command with the person's values or a `<placeholder>`. Removed trailing
  full stops and commas after commands. The shortest forms keep every command
  line within 78 columns (the `--jwt-out` mockup now uses `b.pub`).
- R-true-fix (7): E2 plain-URL fix names `~/.config/mash/config.json` before
  `MASH_URL`; D5 `limits --show` names the file; E3 "no config folder" says the
  `export` is for this shell only; E intro states the rule. Audit of section E:
  no fix tells the person to restart a session or to export a variable for a
  session started elsewhere; the one `export` left (E3, `MASH_CONFIG_DIR`)
  applies to the shell in which mashctl itself failed.
- R-verdict (13 outputs): `audit` (default, `--wide`, `--detail`, `--strict`,
  `--compare`), `issued check` (clean, unknown key, revoked key, incomplete),
  `endpoint suspended` (both states), and the two `--no-revoke` results.
- R-names (about 40 lines): the name column of grouped tables is headed
  `ENDPOINT` (was `NAME`);
  `Created` for new keys and tokens; no `--long` flag (the help says `--detail`);
  `1992 B`; no slash form printed as a result (`bob2`, not `acme/demo/bob2`);
  `mashctl list`-style removal text says `see endpoint list`.
- R-nextcase (all): `fix:` after a failure, `NEXT:` after a success, command
  beneath, in every block.
- R-doc (3): "See docs/guides/nats-local.md" in A3 and A5.
- R-exit (2): E intro and E2 cite REQ-014; choice 17 records review 2's request.
- Section E gained the usage-error shape (`error:`, `fix:` with command,
  `usage:`) in place of `error:`, `usage:`, `example:`.

