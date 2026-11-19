# mashctl target outputs (0.5.6) - DRAFT10

Status: design mockups, not code. Nothing here was run. Written to the maintainer's
feedback on the v0.5.5 video and the CLI standard's rules; BEFORE blocks are
excerpts of docs/plans/2026-11-19-cli-output-catalog-mashctl.md (v0.5.5 tag).
DRAFT10 applies six directions given for this draft on the critic's second round and a
real two-machine walk of v0.5.5 (see "Changes in DRAFT10" and, at the very end,
"Dispositions of the critic's round 2"):

1. The server and the config path are in no success output. They appear on
   failure, under `--detail` and in `mashctl status`. One warning remains, for
   a server taken from MASH_URL that differs from the config file.
2. Destruction needs a typed confirmation, with no flag that skips it: `endpoint delete`,
   every single `endpoint reissue`, and a bulk reissue (the person types the
   count). Each says what will be lost first and proves afterwards that the old
   key is refused.
3. One exit-code table, the CLI standard's, for every command.
4. The two-machine flow uses the real values: the hand-off names the files to
   copy, the install writes the server and the CA, a reissue writes a numbered
   token, and nothing names an old key.
5. Printed fixes are true for what is broken (removed-command messages echo the
   person's own files).
6. Names: `ADDRESS` replaces `PATH`; `Adopted`; one server form.

Earlier drafts are described under "Changes in DRAFT2" to "Changes in DRAFT9".

Part 1 is the first-run journey, in the order a new user meets it. Part 2 is every
other mashctl output. Each part ends with its own "Choices to confirm" and "Facts
to confirm".

---

# Part 1: the first-run journey (target output for mashctl 0.5.6), DRAFT10

Design document. Nothing here was run. BEFORE blocks are excerpts of
`docs/plans/2026-11-19-cli-output-catalog-mashctl.md` (v0.5.5), trimmed to the
lines that matter, each marked with the point it breaks (P1..P9, defined in
the catalog). AFTER blocks are the target, exactly as the terminal shows them,
every line 80 columns or fewer except a command a person is told to run (checked
by `make_packet.py`, `check.sh` and `check3.sh` in the scratch directory).

## Conventions for every output (Parts 1 and 2)

These facts are written once, here. Part 2 refers to this block and does not
repeat them.

- Exit codes. One table for every command in both parts. It is the CLI
  standard's rule 14, plus the two codes the contract adds. Rule 14 reads:
  "Exit codes mean: 0 success; 1 failure; 2 wrong usage (unknown command, bad
  flag or argument); 3 cancelled; 4 not authorized; any other code is listed on
  the command's reference page." The contract (Exit codes) adds 5 and 6 and says
  the same condition gets the same code in every command.

  | Code | Meaning | Where this document uses it |
  |---|---|---|
  | 0 | success | done, planned (`--dry-run`), nothing to change, nothing wrong |
  | 1 | failure | a refusal, or a check that found a problem; a plain URL refused for TLS |
  | 2 | wrong usage | unknown command or flag, missing or bad argument, a removed command |
  | 3 | cancelled | the person typed the wrong confirmation |
  | 4 | not authorized | no person at a terminal for a confirmation, or no administrator credentials |
  | 5 | partial | part of a bulk command was done |
  | 6 | the server | could not be reached, or did not confirm (an incomplete check) |

  No command has a code of its own. `mashctl endpoint suspended` is gone (its
  answer is a line of `endpoint show`), so no command uses an exit code to
  answer a question.
- Streams. stdout carries results, tables and `NEXT:` lines. stderr carries
  `error:` lines, `fix:` lines, a `See` line that follows them, and warnings.
- Labels. Every error line starts `error:`. A warning starts `warning:`.
  Hints are labelled: `fix:` after a failure, `NEXT:` after a success, `See`
  for a document; a second choice after either starts `Or`. A `NEXT:` is set
  apart by a blank line; a `fix:` follows its `error:` line directly. A fix
  with a command is one sentence ending in a colon and the command alone on the
  next line, indented two spaces. A verdict line (`OK`, `PROBLEM`,
  `NOT REACHABLE`) is a result, not an error line: a health command that finds
  a fault prints its `error:` and `fix:` lines after the rows.
- The server is not in the output. The server and the path of the config file
  appear in no success output. A normal run prints nothing about them. They
  appear on failure, under `--detail`, and in `mashctl status`. One safety
  line remains. A `mashctl` run inside a mash session inherits MASH_URL. When
  the server comes from MASH_URL and the config file names another server, and
  only then, the command prints this on stderr before it acts:

  ```text
  warning: using the server from MASH_URL in this shell (the config file names
           another server)
  ```
- Files. A command that writes a file prints one line for it, indented two
  spaces, with a lowercase verb (`wrote`, `kept`, `copied`, `renamed`) and the
  file. It shows the path only when the person will need it: the credentials,
  the token, the CA file. `config.json` is named only in the file lines of
  `credential install` and `adopt`.
- Typed confirmation (contract INV-004; CLI standard rule 15). Rule 15 says a
  command prompts only when standard input is a terminal and, without one, it
  fails. INV-004 applies it to every change a person must own, without the
  `--yes` flag that rule 15 allows: no flag and no environment variable stands
  in for the person. The change of trust is a new server or a new certificate
  authority. The destruction is `endpoint delete`, any single `endpoint
  reissue`, a bulk reissue, and the replacement of installed credentials. Before
  it acts the command says what will be lost. A line that ends in a colon and
  asks for typing is a prompt; the mockups show what the person types after the
  colon. The person types the address (or, for a bulk reissue, the count; for a
  trust change, the fingerprint). A wrong answer exits 3. With no terminal the
  command exits 4 with `error:` and `fix:`. `--dry-run` prints the same text
  of what will be lost and asks nothing. A real fingerprint is `sha256:` and 64
  digits, so the mockups shorten it to `sha256:3f9a...c41e`.
- Plain-text warning (contract REQ-010). While `MASH_INSECURE_PLAINTEXT` is
  set, every command that connects prints this first line on stderr, before
  any result (shown in full once, in E2, on `endpoint list`):

  ```text
  warning: plain-text connection to myserver (MASH_INSECURE_PLAINTEXT is set):
           anyone on the network can read mail and obtain signatures
  ```
- Colour. On a terminal the words `NEXT:`, `error:` and `warning:` and the
  verdict words (`OK`, `PROBLEM`, `NOT REACHABLE`) are coloured, in colours
  that read on a dark and on a light background. There is no colour when
  `NO_COLOR` is set or when the output is not a terminal; the words stay, and
  tables do not depend on colour.
- `--json` is unchanged in every command. Nothing here redesigns any JSON.
- Paths. Every path printed is the resolved path: an absolute
  `MASH_CONFIG_DIR`, else an absolute `XDG_CONFIG_HOME/mash`, else
  `~/.config/mash`. Mockups show the default.
  A file a command writes in the current directory (the token, or a path given
  to `--creds-out` or `--token-out`) is shown as `./<name>` (or as the person
  typed it), because the default is the current directory; the hand-off
  instructions use the same path as the line that reports the write.
- Versions. A mockup never prints a version number; it prints `<version>`.

Conventions in the AFTER blocks:

- `NEXT:` (after a success) and `fix:` (after a failure) are the two labels for
  "what to type now". A blank line always comes before `NEXT:`.
- A command a person must run stands alone on its own line, indented two
  spaces, complete, never wrapped, with no punctuation after it. The sentence
  before it ends with a colon and fits one line. A command is shown with the
  values mash knows (the token file, the address, the CA file). Anything mash
  cannot know is a `<placeholder>`: `<address>` for an endpoint address and
  `<file>` for a file. A document is not a NEXT: it is written `See <path>`.
- A fix that sets configuration names the file with its resolved path first
  (mockups: `~/.config/mash/config.json`). An environment variable is offered
  only as the test for one shell.
- The walkthrough is one terminal that you scroll back through. Nothing clears
  the screen between steps.
- File names. A key file takes the endpoint's name (`bob.key`, `bob.pub`).
  A token file and a credentials file take the whole address
  (`acme.demo.bob.token`, `acme.demo.bob.creds`), because one folder can hold
  many endpoints' files.
- Flags the rules rename are used in their new form: `--token-out`,
  `--system-credentials`, `--admin-credentials`, `--make-admin`,
  `mash credential install --key --token` (or `--creds <file>`),
  `mash key create --out bob.key`.
  Tags are `--tag k=v` and capabilities `--capability`.
  Suspend and resume are `mashctl endpoint suspend` and `mashctl endpoint
  resume`. `mashctl list` is removed (use `mashctl endpoint list`). Flags the
  rules do not name keep today's spelling unless listed under "Choices to
  confirm".
- Names: endpoint (the thing), name (`bob`), address (the dotted path
  `acme.demo.bob`, header `ADDRESS` in tables; the name's header is
  `ENDPOINT`), group, owner. The address is typed and printed in the dotted
  form; mashctl takes `acme.demo.bob` in every command. Part 2 uses the same
  headers: `OWNER GROUP ENDPOINT ADDRESS`.
- Machines in the walkthrough: `myserver` runs the NATS server and mashctl,
  and alice runs there; `bobs-laptop` runs bob. The server is written
  `tls://myserver:4222`, the one form in every mockup. A tool does not know the
  name of another machine, so a tool's own text says "bob's machine"; the
  headings of the walkthrough say `bobs-laptop`.

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
We are going to walk you through setting up mash between two endpoints on two
machines.

  Machine      Runs                        Endpoint
  myserver     the NATS server, mashctl    alice
  bobs-laptop  mash                        bob

  Owner  acme
  Group  demo

  +------------- myserver --------------+       +--- bobs-laptop ---+
  |                                     |       |                   |
  |  +-------------+   +-----------+    |       |    +---------+    |
  |  | NATS server |<--|  mashctl  |    |       |    |   bob   |    |
  |  +-------------+   +-----------+    |       |    +---------+    |
  |    ^         ^                      |       |         |         |
  |    |    +---------+                 |       +---------|---------+
  |    |    |  alice  |                 |                 |
  |    |    +---------+                 |                 |
  +----|--------------------------------+                 |
       +----------------------------------------------------+

An endpoint is an agent or tool on the mail mesh. Its address is written with
dots: owner.group.name. In acme.demo.bob, the owner is acme, the group is
demo and the name is bob. mash send and mashctl both take this address.

mashctl is a program you download from the release page: <release page URL>
```

**What changed and why**

- DRAFT10 (critic A.0, walk): the setup block tells the two-machine story the
  steps follow. `myserver` runs the NATS server and mashctl, and alice runs
  there. `bobs-laptop` runs bob, whose key never leaves it. The earlier text
  said "two endpoints on one machine" above steps that copy files between
  machines, and never named bob's machine.
- P6 (maintainer point 6): machines, group, endpoints and the diagram come
  before the first command, so every later "this machine" has a name.
- Point 2 and rule 3: alice and bob, owner acme, group demo.
- Point 8 (last paragraph asked for the `x/y/z` path explained): one
  paragraph explains the dotted address, owner.group.name, with
  acme.demo.bob as the example. It is the only form shown or typed (R3;
  see Choices to confirm, item 2).
- Point 4: wrapped by hand at 80 columns or fewer.
- R-names: "Agents" became "Endpoints"; the path is called the address, written
  with dots.
- DRAFT8 (T4): the last line says how to get mashctl. It ships as a release
  binary, so a newcomer does not need Go, and no AFTER block says `go install`.
  The exact release-page wording is a fact to confirm.

---

## 1. Connect mashctl to the server

mashctl does not install or start NATS (rule 14; maintainer point 1). mashctl
itself is a downloaded program from the release page (DRAFT8), not something
built with Go. The operator who runs the NATS server gives you four things: the server address,
the operator key file, the account key file and the system credentials file,
and the file of the server's certificate authority if the certificate is
private.

### 1a. If there is no server yet: `mashctl init --local`

This section proposes removing `mashctl init --local`. Removing it reverses the
local TLS quick start that ADR 0146 and 0.5.5 shipped. The contract that
ratifies this document carries the ADR (mash order 17: mashctl does not
install, configure or start NATS). This document is a proposal until then.

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

See the skill that sets up a server:
https://github.com/bogworks/mash/blob/main/docs/skills/mash-nats-local/SKILL.md
See the script it runs:
https://github.com/bogworks/mash/blob/main/scripts/nats-local-setup.sh
```

Exit code 2 (usage: the flag no longer exists) [confirm: exit 2 or 1]. `--start`, `--dir` and `--port`
get the same message. The skill and the script replace
`docs/guides/nats-local.md` as the setup instructions; the message names
both and no longer names the guide. DRAFT9: the message gives URLs on the
project's repository, because an installed mashctl cannot see repository
paths such as `docs/skills/...`. [confirm: the final URLs, or the release
page, once the files are published; Choice 38.]

### 1a, second screen, AFTER: `mashctl list` is removed

`mashctl list` is removed in the same way (it showed what `mashctl endpoint
list` shows):

```text
$ mashctl list
error: mashctl list was removed.
fix: list endpoints with the new command:
  mashctl endpoint list
```

**What changed and why**

- Maintainer point 1 and rule 14: mashctl no longer makes a server, a
  certificate, keys under `/demo`, or a process. The message is the refusal
  form (rule 10) and names the skill and the script as `See <path>` (R-doc),
  with the command to run next on its own line (R-paste).
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
$ mashctl adopt --server tls://myserver:4222 --owner acme --account MASH \
    --ca ca.pem --make-admin --operator-key operator.nk \
    --account-key account.nk --system-credentials system.creds

mashctl will trust the certificate authority in ca.pem for this server. Whoever
holds its key can pose as that server. Check the fingerprint with your
operator.

  server       tls://myserver:4222
  fingerprint  sha256:3f9a...c41e

Type the fingerprint to trust it: sha256:3f9a...c41e

Checking the server as owner acme. Nothing is written until all the checks
pass.

  1 files     ok  The operator key, account key and system credentials files
                  are there, and only you can read them.
  2 server    ok  The server turns away a connection with no credentials and
                  accepts the system credentials. Only accounts made by your
                  operator can use it.
  3 account   ok  Account MASH exists on the server and has not expired.
  4 storage   ok  The account may store messages (2 GiB). mash needs this to
                  keep mail until it is read.
  5 system    ok  The system credentials can list the server and look up
                  accounts.
  6 operator  ok  The operator key signed this account, so you hold the
                  authority to manage it.
  7 admin     ok  The admin credentials that mashctl will use connect, and
                  the server confirms storage is on for the account.

Adopted the server as owner acme.

  copied  mashadmin.creds to ~/.config/mash/server/mashadmin.creds  (mode 600)
  wrote   ~/.config/mash/config.json  (server, owner, certificate file)

  The keys are saved in the same folder. The settings hold the certificate
  file as a full path, so later commands find it from any folder.
  On the server, the only change is a record of the new admin credentials.
  mashctl keeps its own copies, so you can remove <export folder> now.

NEXT: on this machine, prepare the server for mash:
  mashctl init

Or, to remove the folder you exported the keys and credentials to:
  rm -r <export folder>
```

When a check fails, the run stops at that line:

```text
  3 account   FAILED  Account MASH is not on the server.

error: account MASH was not found on tls://myserver:4222.
fix: ask your operator for the account name and pass it as --account <name>
```

When a person who is not at a terminal, or a script, runs adopt with a
certificate authority file, or types a fingerprint that does not match:

```text
$ mashctl adopt <the same options>   (no terminal)
[exit 4]
error: this makes mash trust a certificate authority, and a person must
       confirm it at a terminal.
fix: run the same command in a terminal and type the fingerprint when asked:
  mashctl adopt <the same options>

$ mashctl adopt <the same options>
...
Type the fingerprint to trust it: sha256:0000...0000
[exit 3]
error: the fingerprint you typed does not match the file. Nothing was changed.
fix: get the fingerprint from your operator, then run the command again:
  mashctl adopt <the same options>
```

When the address is `nats://` (plain text) and it came from `--server`:

```text
$ mashctl adopt --server nats://myserver:4222 --owner acme ...
error: nats://myserver:4222 is not encrypted, and mash only connects over TLS.
fix: give --server a tls:// address. Your operator can give you the
     certificate file, which you pass as --ca <file>.

Or, for a test server only: put MASH_INSECURE_PLAINTEXT=1 before the command:
  MASH_INSECURE_PLAINTEXT=1 mashctl adopt <the same options>

With it, credentials and mail cross the connection unencrypted.
```

If adopt is run without `--server` and the plain address comes from MASH_URL,
the error names MASH_URL and the fix is the one shown in E2 (Part 2): `unset
MASH_URL`.

`--detail` adds the key ids and the file paths of each check; `--dry-run`
prints "Would ..." lines and ends "Nothing was changed." (rule 11).

adopt reports its checks one line at a time as it runs, so its verdict is the
last line (`Adopted ...`, or `error:` after a `FAILED` line), not the first.
The verdict-first rule (R-verdict) holds for `mashctl status`, which has all
its answers before it prints.

**What changed and why**

- DRAFT10 (V1, V6, critic A.1b): the result line is `Adopted the server as
  owner acme.`; no address and no `(from --server)`. The server shows only in
  the trust prompt, where the person must see it to confirm it. The first line
  of the checks is `Checking the server as owner acme.` The certificate file
  is a flag, `--ca <file>`, like `credential install --ca`; the
  `MASH_TLS_CA=` form and its two-line explanation are gone. Check 2 says
  "accounts made by your operator", not "operator-signed accounts". The NEXT
  says `on this machine`: `init` runs wherever mashctl runs, not necessarily on
  the NATS host. A wrong fingerprint exits 3 (cancelled, rule 14); no terminal
  exits 4 (not authorized).
- DRAFT9 (U2, INV-004): adopt with a certificate authority file makes mash
  trust that authority, so it asks the person to type the fingerprint before
  anything is checked or written. No flag skips it. With no terminal it
  refuses with `error:` and `fix:`; a wrong fingerprint refuses and changes
  nothing. [confirm: how adopt computes the fingerprint of the file it was
  given.]
- DRAFT9 (U6): the plain-URL refusal names where the address came from. From
  `--server` the fix names `--server`; from MASH_URL it is the E2 fix.
- The plain-URL refusal also names the override, which is the environment
  variable `MASH_INSECURE_PLAINTEXT=1` for that one run (DRAFT8, REQ-010).
  The config key `insecure_plaintext` is gone: no file can turn plain text on,
  and no mash command writes the override. The refusal says plainly that
  credentials and mail then cross the connection unencrypted, and that it is
  for a test server only.
- DRAFT8 (T2, INV-003): adopt copies the admin credentials into mashctl's own
  folder (mode 600), in the `copied` form of `mash credential install`, so
  the folder they were exported to can be removed. The closing text says so
  as a plain sentence and offers the removal as an `Or` hint with the command
  on its own line (`rm -r <export folder>`; mash cannot know the folder, so
  it is a placeholder). The `wrote` line names `config.json`.
- P5 (maintainer point 4): the command is wrapped with `\`, every output line
  is 80 columns or fewer, no line carries a path longer than `~/.config/mash`.
- P6 (point 8, point 16): each check is one line of meaning in plain words. No
  JWT, seed, resolver, JetStream, bucket, lease, issue record.
- P8 and P9: the three 56-character key ids, the file modes and the resource
  counts (streams, consumers) are gone from the default output; they are in
  `--detail`.
- Certificate path (review finding 1): the typed command shows `--ca
  ca.pem`, and adopt saves the CA's absolute path in config.json. Today adopt saves the value of MASH_TLS_CA as typed
  (cmd/mashctl/adopt.go, `settings["tls_ca"] = cfg.TLSCA`), and later runs
  resolve a relative `tls_ca` under the config folder
  (internal/config/config.go, `inDir`), so `ca.pem` would point to
  `~/.config/mash/ca.pem` and the next `mashctl init` could fail TLS
  verification. The target requires adopt to make the path absolute before it
  saves it. The other files adopt takes are not at risk: the operator key,
  account key and system credentials are copied into the config folder, and
  the admin credentials path is already saved as an absolute path.
- P7: the closing paragraph "What adopt does not do" is replaced by the one
  true sentence for this run: the server change is the admin record.
- P4 (point 11): the `Next:` list of two items, with the `provision`
  placeholder, becomes one `NEXT:` with the command on its own line. The
  command is the next step in the guide and runs on this machine. `provision`
  is not next in the guide.
- P1 (point 14): adopt printed its own header already; it now says only what
  it is checking and why nothing is written yet.
- Wrong-server trap (DRAFT5, replaced in DRAFT10): the lines no longer say
  where the address came from. adopt takes it from the command you typed.
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

Prepared the server for mash.

  mash now has room on the server for:
    - the list of endpoints (what `mashctl endpoint list` shows)
    - the record of every token mash signs
    - the sessions that are running

NEXT: on this machine, create an endpoint:
  mashctl endpoint create acme.<group>.<name> --make-key

Or, for an endpoint on another machine, start there:
  mash key create --out <name>.key
```

Run again on a server that is ready:

```text
$ mashctl init
Already prepared. Nothing was changed.
```

[confirm: init is safe to run twice and says nothing changed; the catalog shows
"ready" both times].

**What changed and why**

- P1 (point 14): the three-part preamble is gone; the result line names no
  server (DRAFT10).
- DRAFT10 (critic A.1c): the NEXT offers both paths, as the empty `endpoint
  list` does: `--make-key` for an endpoint on this machine, and `mash key
  create` for another machine. The owner is in config, so the command shows
  `acme.`.
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
problem, `error:`, `fix:` and the command. Exit code 0 on `OK`, 1 on `PROBLEM`
and 6 on `NOT REACHABLE` (the server did not answer). On a terminal the verdict word is coloured (green, red,
yellow); the capitals carry the meaning without it.

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
signed in:  as admin
prepared:   yes
config:     ~/.config/mash/config.json
```

The server was adopted and `mashctl init` was not run yet:

```text
$ mashctl status
PROBLEM: init has not been run on this server.

server:     tls://myserver:4222 (reachable, encrypted)
prepared:   NO

error: the server is not prepared for mash.
fix: prepare the server, on this machine:
  mashctl init
```

The server cannot be reached; the command says so and names the address:

```text
$ mashctl status
NOT REACHABLE: tls://myserver:4222 refused the connection.

error: mashctl cannot check anything else while the server is down.
fix: start the NATS server or ask whoever runs it, then check again:
  mashctl status
```

**AFTER, `--detail`**

```text
$ mashctl status --detail
OK

version:             mashctl <version>
server:              tls://myserver:4222 (reachable, encrypted)
certificate:         checked against ~/.config/mash/ca.pem
owner:               acme
account:             MASH
signed in:           as admin
prepared:            yes

config:              ~/.config/mash/config.json
key directory:       ~/.config/mash/keys
admin credentials:   ~/.config/mash/server/mashadmin.creds
system credentials:  ~/.config/mash/server/sys.creds

keys (the operator key made the account; the account key signs each token):
operator key:        OAQCFPM5BOL22AGI6O76QGKIQHRLYN7DZZEATDRWD6QTEC5462SVCBJU
account key:         AD7BK6VI6EGIDZ2XXCLTKUBGR5NOIJYVZ77DTRETXNW2XXPNQTK2UGO6
```

**What changed and why**

- P1 (point 14): the preamble becomes a command you run when you want it. The
  facts are the same: server (from `url`), owner, the credentials in use, the
  config location. The credentials file path moves to `--detail`; the short
  form says "signed in: as admin for owner acme".
- DRAFT10 (V1, V3, critic A.1d): `status` is the one command where the server
  and the config path are the answer, so they stay. `signed in: as admin`
  does not repeat the owner. `NOT REACHABLE` exits 6 (the server), not 1. The
  fix for a down server says "or ask whoever runs it". `--detail` says in one
  line what the operator key and the account key are for.
- R-verdict: line 1 is `OK`, `PROBLEM: ...` or `NOT REACHABLE: ...`. A problem
  prints only the rows that are wrong, then `error:` and `fix:` with the
  command on its own line (DRAFT7: the verdict is a result, the `error:` line
  is the error). The version moved to `--detail` (a version is not a check);
  the mockup prints `mashctl <version>`, not a number (DRAFT7).
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
once in short form (what a plain run prints). Bob runs on `bobs-laptop`: Steps 1
and 3 run there and Step 2 runs on `myserver`, where mashctl is. Alice runs on
`myserver`, so one command does everything for her. A tool does not know the
name of another machine, so its text says "bob's machine"; the headings below
name it.

Short vocabulary used by all three steps (rule 7): private key, public key,
token (the statement your mash server signs: "this public key is endpoint
acme.demo.bob"), credentials (the one file mash runs with: private key plus
token).

mash and mashctl print `<placeholder>` where they do not know the value. After
`mash key create` the address is unknown to mash, so the NEXT carries
`<address>`.

### Step 1 of 3 (long form), on bobs-laptop: `mash key create --out bob.key --explain`

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
key stays on this machine; only bob.pub leaves it. If someone else runs your
mash server, send them bob.pub; if mashctl is on another machine, copy bob.pub
there.

$ mash key create --out bob.key

Created a key pair.

  private key  bob.key  (keep it secret; only you can read it)
  public key   bob.pub  (safe to share)

bob.pub goes to whoever runs mashctl. bob.key never leaves this machine.

NEXT: copy bob.pub to the machine that runs mashctl, and run there:
  mashctl endpoint create <address> --public-key bob.pub
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
  on its own line. `<address>` is a placeholder, because `mash key create`
  was not told the owner or group.
- R-names: "Created a key pair" (the verb for things created; no owner is
  claimed from a file name).
- DRAFT10 (critic A.S1, walk finding 1): one line says why the two files
  differ: bob.pub is handed on, bob.key stays. The NEXT uses `<address>` (one
  placeholder form). A second key (`bob2.key`, made for a reissue) prints the
  same NEXT, which names `endpoint create`, and a reissue refuses that verb.
  mash cannot tell a first key from a replacement, so the reissue step (B2)
  names the right command. [confirm: Facts to confirm, walk finding 1.]

### Step 2 of 3 (long form), on myserver: `mashctl endpoint create ... --explain`

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
is the group bob mails within, and bob is the name.

mashctl signs bob's public key. The result is bob's token: the statement your
mash server signs that "this public key is endpoint acme.demo.bob". The
server accepts bob only with a key and a token that go together. The token
also holds the server's address, so bob's machine needs no other setting.

$ mashctl endpoint create acme.demo.bob --public-key bob.pub

Created endpoint acme.demo.bob.

  token  ./acme.demo.bob.token  (proves bob.pub is acme.demo.bob; not secret)

Copy these two files to bob's machine:

  ./acme.demo.bob.token
  ~/.config/mash/ca.pem

Give bob's machine this fingerprint of ca.pem separately from the files. The
install asks for it, so that machine can check that it got the right ca.pem.

  fingerprint  sha256:3f9a...c41e

NEXT: on bob's machine, with bob.key and both files in one folder, run:
  mash credential install --key bob.key --token acme.demo.bob.token
```

**What changed and why**

- P1 (point 14): the preamble is gone, and since DRAFT10 the result line names
  no server and no config file (V1): bob's machine gets the server from the
  token.
- P2 and P8 (points 13, 16): the endpoint is printed once, as
  `acme.demo.bob`, the form that was typed.
- P6 (points 3, 8): `jwt` becomes `token`, with its one-sentence meaning in
  the why paragraph and in the file line (`proves bob.pub is acme.demo.bob`).
  The flag is `--token-out` (default `acme.demo.bob.token` in the current
  directory).
- DRAFT10 (V4, critic A.S2, walk findings 2 and 16): the hand-off names the
  two files to copy with their full paths on this machine (the token and the
  CA file; walk: the CA was never mentioned and bob's machine knew nothing of
  the server), and the fingerprint, which goes separately (DRAFT9, contract
  REQ-003). The NEXT command carries the real values mashctl knows: the token
  file and `bob.key` (the name the `.pub` file came from). It has no
  placeholder: the fingerprint is not in the command but typed at the
  prompt of the install (Part 1, choice 36).
- DRAFT10: the token holds the server address, and `credential install` takes
  the server from the token (contract REQ-003: the flags, then the token's
  contents, then existing config) and `ca.pem` from beside the token, so
  `--server` and `--ca` leave the command. [confirm with core: Choice 36 and
  Facts to confirm.]
- Point 8 (the request to explain `randy/demo/beta`): the first paragraph
  does it, in one sentence of meaning and one of use.
- R-names: "Created the token" (was "Wrote").

### Step 3 of 3 (long form), on bobs-laptop: `mash credential install ... --explain`

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
writes acme.demo.bob.creds to mash's config directory. It also saves the
server's address (from the token) and a copy of ca.pem there, which mash needs
to connect. The private key does not leave this machine. When only one
endpoint is installed on a machine, mash uses it, and the setting
MASH_ENDPOINT can be left out.

mash runs with the credentials file alone. bob.key and the token file were
only inputs: delete them, or keep them as a backup and keep bob.key private.

$ mash credential install --key bob.key --token acme.demo.bob.token

mash will trust the certificate authority in ca.pem for this server. Whoever
holds its key can pose as that server. Check the fingerprint with whoever
runs mashctl.

  server       tls://myserver:4222
  fingerprint  sha256:3f9a...c41e

Type the fingerprint to trust it: sha256:3f9a...c41e

Installed credentials for acme.demo.bob.
Checked: ca.pem has the fingerprint you typed.

  wrote   ~/.config/mash/acme.demo.bob.creds  (mode 600)
  wrote   ~/.config/mash/config.json          (server, owner, CA)
  copied  ca.pem to ~/.config/mash/ca.pem     (mode 644)

NEXT: on this machine, check that bob can sign in. No settings are needed:
  mash status

Or add another endpoint, starting on its machine:
  mash key create --out <name>.key
```

**What changed and why**

- DRAFT9 (U1), changed in DRAFT10: a first install makes mash trust a
  certificate authority, so it shows the server and the fingerprint and the
  person types the fingerprint (INV-004), as `adopt` does. The fingerprint
  comes from the hand-off of Step 2. A file that does not match is refused and
  nothing is written. DRAFT9 put `--ca-fingerprint <fingerprint>` in the
  command, which was 98 columns and a placeholder for a value mashctl knew.
  [confirm with core: the wording of the `Checked:` line, and Choice 36.]
- DRAFT10 (V4, critic A.S3, walk findings 3, 4, 5, 6): install writes
  `config.json` (server, owner, CA copy) as well as the credentials, because
  on a fresh machine nothing else tells mash the server, the CA or the group
  (walk: `mash status` said the server was missing, then `send` failed with an
  unknown authority and with a missing group). The address in the token gives
  the owner, group and name, so the NEXT is `mash status` with nothing set.
  The NEXT sentence says no settings are needed.
- DRAFT10 (critic A.S3, walk answer c): what bob keeps is said once, in this
  long form. mash runs with the credentials file alone (walk: moving the key
  and the token aside left `send` working). The earlier line "Keep bob.key:
  mash signs in with the key and token" contradicted that and is gone from
  every block.
- P6 and the question in point 8 ("one credential a key and the other a
  credential"): `mash credential create --seed --jwt` becomes
  `mash credential install --key --token`. The why paragraph says what the
  file is made of. "Install" says the file goes into the config directory.
- P4 (point 11): `next: mash status` is kept as `mash status`, now with
  nothing set because exactly one endpoint is installed on this machine
  (contract REQ-009). The second item is the other thing a person may do
  next. Rule 6 allows a short list when there is a real choice.
- P8 and P9: no public key, no repeated path.
- DRAFT6 (core's block): the output is `Installed credentials for
  acme.demo.bob.` and three file lines with lowercase verbs and aligned
  columns: `wrote` the credentials, `wrote` `config.json`, `copied` the
  certificate file. When `config.json` already points at the same server the
  second line says `kept` (shown below). Flags of `mash credential install`
  (core): `--key --token [--server] [--ca] [--out <dir>]
  [--dry-run] [--detail] [--json]`, and `--creds` in place of `--key --token`.
  There is no `--force`: replacing credentials is a typed confirmation
  (below).
- Point 10 ("why don't I need a beta.seed"): see alice below, where one
  command does all three steps.

### The three steps in short form (what a plain run prints)

Each command prints this when run without `--explain`.

**AFTER**

```text
$ mash key create --out bob.key

Created a key pair.

  private key  bob.key  (keep it secret; only you can read it)
  public key   bob.pub  (safe to share)

bob.pub goes to whoever runs mashctl. bob.key never leaves this machine.

NEXT: copy bob.pub to the machine that runs mashctl, and run there:
  mashctl endpoint create <address> --public-key bob.pub
```

```text
$ mashctl endpoint create acme.demo.bob --public-key bob.pub

Created endpoint acme.demo.bob.

  token  ./acme.demo.bob.token  (proves bob.pub is acme.demo.bob; not secret)

Copy these two files to bob's machine:

  ./acme.demo.bob.token
  ~/.config/mash/ca.pem

Give bob's machine this fingerprint of ca.pem separately from the files. The
install asks for it, so that machine can check that it got the right ca.pem.

  fingerprint  sha256:3f9a...c41e

NEXT: on bob's machine, with bob.key and both files in one folder, run:
  mash credential install --key bob.key --token acme.demo.bob.token
```

```text
$ mash credential install --key bob.key --token acme.demo.bob.token

mash will trust the certificate authority in ca.pem for this server. Whoever
holds its key can pose as that server. Check the fingerprint with whoever
runs mashctl.

  server       tls://myserver:4222
  fingerprint  sha256:3f9a...c41e

Type the fingerprint to trust it: sha256:3f9a...c41e

Installed credentials for acme.demo.bob.
Checked: ca.pem has the fingerprint you typed.

  wrote   ~/.config/mash/acme.demo.bob.creds  (mode 600)
  wrote   ~/.config/mash/config.json          (server, owner, CA)
  copied  ca.pem to ~/.config/mash/ca.pem     (mode 644)

NEXT: on this machine, check that bob can sign in. No settings are needed:
  mash status

Or add another endpoint, starting on its machine:
  mash key create --out <name>.key
```

On a machine that already has this server's settings (for example the machine
that runs mashctl), the middle file line says `kept`:

```text
$ mash credential install --key bob.key --token acme.demo.bob.token

Installed credentials for acme.demo.bob.

  wrote   ~/.config/mash/acme.demo.bob.creds  (mode 600)
  kept    ~/.config/mash/config.json          (same server and CA)
  kept    ~/.config/mash/ca.pem               (same CA)

NEXT: on this machine, check that bob can sign in:
  MASH_ENDPOINT=acme.demo.bob mash status

Or add another endpoint, starting on its machine:
  mash key create --out <name>.key
```

After a reissue the credentials for the same address are already installed. The
install says what is replaced and asks the person to type the address; there is
no `--force`:

```text
$ mash credential install --key bob2.key --token acme.demo.bob.2.token

The current credentials for acme.demo.bob use an old key that the server now
refuses. Installing replaces them.

Type the address to replace them: acme.demo.bob

Installed credentials for acme.demo.bob.

  wrote   ~/.config/mash/acme.demo.bob.creds  (replaced the old credentials)
  kept    ~/.config/mash/config.json          (same server and CA)
  kept    ~/.config/mash/ca.pem               (same CA)

NEXT: on this machine, check that bob can sign in:
  mash status
```

With no terminal, or with a wrong answer:

```text
$ mash credential install --key bob2.key --token acme.demo.bob.2.token
[exit 4]
error: replacing the installed credentials needs a person at a terminal.
fix: run the same command in a terminal and type the address when asked:
  mash credential install --key bob2.key --token acme.demo.bob.2.token

$ mash credential install --key bob2.key --token acme.demo.bob.2.token
...
Type the address to replace them: acme.demo.alice
[exit 3]
error: what you typed is not acme.demo.bob. Nothing was changed.
fix: run the command again and type the address of the credentials to replace:
  mash credential install --key bob2.key --token acme.demo.bob.2.token
```

To copy one credentials file to another machine instead, use `mash
credential install --creds <file>`; it makes the same writes as `--key
--token`. That moves a private key across machines; the three steps keep
the key on the agent's machine.

**What changed and why**

- Rule 9: the short form keeps the what-happened lines and the NEXT; it drops
  the step header and the why paragraph, which `--explain` brings back.
- Rule 8: result, then up to three lines.
- DRAFT10 (V4, V2, walk finding 10): installing over credentials for the same
  address (the second install after a reissue) used to need `--force`, and the
  refusal did not say the old file held a revoked key. Now the install says
  what is replaced and the person types the address; it exits 3 on a wrong
  answer and 4 without a terminal (Choice 39). [confirm: how install learns
  that the server refuses the old key; Facts to confirm.]

### Alice on the same machine: `mashctl endpoint create acme.demo.alice --make-key`

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
$ mashctl endpoint create acme.demo.alice --make-key

Created endpoint acme.demo.alice.

  1. Created a private key for alice on this machine.
  2. Created her token, signed by your server.
  3. Installed credentials for alice.

  wrote   ~/.config/mash/acme.demo.alice.creds  (mode 600)

NEXT: on this machine, check that alice can sign in:
  mash status

Or add another endpoint on this machine:
  mashctl endpoint create acme.<group>.<name> --make-key
```

### Credentials for another machine: `endpoint create --make-key --creds-out`

**AFTER, short form, credentials for another machine (`--creds-out`)**

This is not the same-machine path: carol runs on another machine, so the
command writes one file to carry there and installs nothing here.

```text
$ mashctl endpoint create acme.demo.carol --make-key --creds-out carol.creds

Created endpoint acme.demo.carol.

  1. Created a private key for carol.
  2. Created her token, signed by your server.
  3. Wrote the credentials for carol to one file.

  wrote   ./carol.creds  (new file, mode 600)

warning: copying carol.creds moves carol's private key to another machine.
         To keep the key on carol's machine, make it there with mash key
         create and use mashctl endpoint create with --public-key.

Copy these two files to carol's machine:

  ./carol.creds
  ~/.config/mash/ca.pem

Give carol's machine this fingerprint of ca.pem separately from the files. The
install asks for it, so that machine can check that it got the right ca.pem.

  fingerprint  sha256:3f9a...c41e

NEXT: on carol's machine, with both files in one folder, run:
  mash credential install --creds carol.creds
```

### A MASH_URL that names another server: `endpoint create`

**AFTER, run in a mash session (MASH_URL names another server than the
config file)**

mashctl run inside a mash session inherits MASH_URL. The administrator
credentials belong to the server the config file names, so credentials made
for any other server would not sign in. The command refuses and writes
nothing:

```text
$ MASH_URL=tls://testserver:4222 mashctl endpoint create acme.demo.alice \
    --make-key
[exit 1]
error: MASH_URL is tls://testserver:4222, but your administrator credentials
       are for tls://myserver:4222. Nothing was changed.
fix: run outside the mash session, or unset MASH_URL, then run it again:
  unset MASH_URL
```

### Alice on the same machine, long form: `endpoint create --make-key --explain`

**AFTER, long form (`--explain`)**

```text
This does all three steps in one command, on one machine. Use it when the
endpoint runs on the same machine as mashctl. alice's private key is made here
and never written anywhere except her credentials file.

Step 1 of 3: create a key for your agent (on this machine)

  This makes a private key (NATS calls it a seed) for alice. Its public half
  is signed in Step 2.
  Done: created a private key for alice.

Step 2 of 3: create the endpoint (on this machine)

  This creates the endpoint acme.demo.alice. mashctl signs alice's public
  key, which makes her token: the statement your mash server signs that
  "this public key is endpoint acme.demo.alice".
  Done: created the token; endpoint acme.demo.alice exists.

Step 3 of 3: install the credentials (on this machine)

  This joins the private key and the token into credentials: the one file
  mash runs with.
  Done: installed credentials for alice.

  wrote   ~/.config/mash/acme.demo.alice.creds  (mode 600)

NEXT: on this machine, check that alice can sign in:
  mash status

Or add another endpoint on this machine:
  mashctl endpoint create acme.<group>.<name> --make-key
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
- DRAFT10 (V1, critic A.alice): no server and no `kept` lines for files the
  command did not touch. The default output has one file line, the
  credentials. With one endpoint installed on this machine, the check is
  `mash status` with nothing set.
- DRAFT10 (V5, critic A.alice, F.3): `--creds-out` says `Wrote the credentials
  ... to one file`, never "Installed"; it installs nothing. It lists the file
  to carry, the CA file and the fingerprint, and the NEXT names the real
  values. The example is carol, who runs on another machine, so the heading no
  longer sits under "same machine". The `--explain` step headings say "on this
  machine" (they said the agent's machine and the machine that runs mashctl
  for a one-machine command).
- DRAFT10 (V5, critic F.3): a MASH_URL that names another server is a
  refusal, exit 1, naming both servers. DRAFT9 saved credentials for the other
  server after a `--replace` and a typed fingerprint; that path is gone from
  `endpoint create` (Choice 35), because credentials made for another server
  would not sign in with these administrator credentials. A server that
  replaces another on a machine is `credential install` (a trust change with
  its own prompt).
- DRAFT8 (T1, INV-003): `--creds-out FILE` creates a new file. The line says
  `new file, mode 600`: the file did not exist, only its owner can read it,
  and it was private from the moment it was made. An existing different file,
  a path that is a link, and a folder the person does not own are refused
  (E3). The default path in the config folder keeps `(mode 600)`.
- DRAFT6 (one command, one file): `--creds-out FILE` writes the credentials
  at FILE (mode 600), so the person copies one file to the machine where
  carol will run. The other machine installs it with `mash credential
  install --creds FILE` (core accepted it; same writes as `--key --token`).

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

OWNER  GROUP  ENDPOINT  ADDRESS          CREATED (UTC)
acme   demo   alice     acme.demo.alice  2026-11-19 00:21
              bob       acme.demo.bob    2026-11-19 00:22

2 endpoints
```

```text
$ mashctl endpoint list --wide

OWNER  GROUP  ENDPOINT  ADDRESS          TYPE   HOST  CREATED (UTC)     NOTE
acme   demo   alice     acme.demo.alice  agent  -     2026-11-19 00:21  -
              bob       acme.demo.bob    agent  -     2026-11-19 00:22  -

2 endpoints
```

When another owner or group is present, the owner and the group are written on
their first row only, as kubectl does:

```text
$ mashctl endpoint list

OWNER  GROUP  ENDPOINT  ADDRESS           CREATED (UTC)
acme   demo   alice     acme.demo.alice   2026-11-19 00:21
              bob       acme.demo.bob     2026-11-19 00:22
       prod   deploy    acme.prod.deploy  2026-11-19 00:30

3 endpoints
```

With no endpoints:

```text
$ mashctl endpoint list

No endpoints yet.

NEXT: create an endpoint on this machine:
  mashctl endpoint create acme.<group>.<name> --make-key

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
- R-names: the table has the columns `OWNER GROUP ENDPOINT ADDRESS`.
  `ENDPOINT` is the endpoint's name (`bob`) and `ADDRESS` is the dotted
  address (`acme.demo.bob`), the one `mash send` takes. DRAFT10 (V6, critic
  A.list): the column was `PATH`, a word used nowhere else; every other block
  says "address". Part 2's grouped tables use the same `ENDPOINT` header.
- `--wide` width: the header and rows with `HOST` and `NOTE` are 77 columns
  for two rows; a longer note is cut to the screen width with `...`.
- DRAFT10 (critic A.list): `HOST` shows `-` for bob, as `endpoint show
  --detail` does: the host is empty until `endpoint set --host` sets it.
  DRAFT9 printed `myserver` in the list and `-` in the show.

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
online:       no
unread mail:  3
credentials:  not on this machine
```

Bob runs on bobs-laptop, so the credentials line says `not on this machine`.
For alice, whose credentials are here, it names the file. A note, host,
description or tags (`tags:  env=demo`) appear as one more line only when
set. A suspended endpoint adds `status:  SUSPENDED since <time>`, then the
`suspension id` and `last message` lines (set by `mashctl endpoint suspend`,
undone by `mashctl endpoint resume`; see C3).

**AFTER, `--detail`**

```text
$ mashctl endpoint show acme.demo.bob --detail

Endpoint acme.demo.bob

owner:         acme
group:         demo
name:          bob
storage name:  acme_demo_bob
mail subject:  agents.mail.acme.demo.bob
runs with:     claude-code
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
online:        no
unread mail:   3
credentials:   not on this machine
```

**What changed and why**

- P1 (point 14): no preamble.
- P2 and P8 (points 13, 16): the title says the endpoint once; owner, group
  and name are not repeated on the default screen. In `--detail` the
  storage name appears under its own label, so it is not an unexplained
  second form. The slash form is not shown anywhere.
- DRAFT10 (critic A.show): the default has the three things people ask
  about: `online`, `unread mail` and, when set, `status: SUSPENDED`. `type`
  is the only word for agent or tool; the program an agent runs is `runs
  with` in `--detail` (it was `agent:`, a second name for type). Bob's
  credentials are on bobs-laptop, so the example says `not on this machine`.
- P9 (point 16, "most of this detail could be shoved into --detail"): the
  default form has six lines: created, type, token, online, unread mail,
  credentials. Empty
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
Run a session as bob (on bobs-laptop)

Step 1. Open a terminal on bobs-laptop, in the folder bob works in.

Step 2. Install the mash plugin in Claude Code, once on this machine:
  claude plugin marketplace add bogworks/mash
  claude plugin install mash@mash

Step 3. Start Claude Code with mash's channel on:
  claude --dangerously-load-development-channels plugin:mash@mash

Claude Code asks you to confirm this each time it starts. Answer yes. The
flag is there because mash's channel is not on Claude Code's approved list.
Without it, mail still arrives at your next prompt; the channel is what wakes
an idle session.

mash signs in with bob's credentials file, the one Step 3 saved. Only bob is
installed on bobs-laptop, so no setting is needed. alice runs the same way on
myserver.
```

If two endpoints are installed on one machine and none is chosen:

```text
$ mash status
error: no endpoint is set, and 2 are installed here.
fix: pick one for this shell:
  export MASH_ENDPOINT=acme.demo.alice
  export MASH_ENDPOINT=acme.demo.bob

Or, to keep the choice, set "endpoint" in ~/.config/mash/config.json to the
endpoint's address.
```

**What changed and why**

- Point 11: the step that follows Step 3 is this one, in the order a user
  does it. DRAFT10 (V4, critic A.session): bob runs on bobs-laptop with only
  his own credentials installed, so no `export` is needed and the launch is
  one command. DRAFT9 asked for two exports and a long paragraph about the
  config file; both are gone.
- One setting (DRAFT7): `MASH_ENDPOINT` takes the endpoint's full address and
  replaces MASH_GROUP and MASH_NAME. With exactly one endpoint installed
  neither it nor `endpoint` in the config file is needed (contract REQ-009).
- No endpoint set: `error: no endpoint is set, and 2 are installed here.`
  mash knows which credentials are installed, so the fix lists them
  (critic A.session). The config file is named for a lasting choice
  (R-true-fix: an export lives in one shell).
- Start command (review finding 2): the DRAFT3 command, `claude --channels
  plugin:mash@mash`, is refused ("not on the approved channels allowlist")
  without a managed channel allowlist (docs/install.md section 3). For one
  person section 3 gives the development form, `claude
  --dangerously-load-development-channels plugin:mash@mash`, which needs no
  administrator and asks for a confirmation at each launch. DRAFT10 says
  once why the flag is there. Section 3 is optional for one person: without a
  channel, mail still arrives at the next prompt or tool call.
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
   Step 2's why paragraph, and explains the dotted address, owner.group.name
   (acme.demo.bob: owner acme, group demo, name bob). Decided (R3): the
   dotted address is the only form printed and typed; mashctl takes
   `acme.demo.bob` in every command. The slash form may still work but is
   not shown. Every `<owner>.<group>.bob` of DRAFT5 is now
   `<owner>.<group>.bob`, and `--help` and its examples use the dotted form.
3. Flag renames beyond the rules: `--sys-creds` becomes
   `--system-credentials`, `--admin-creds` becomes `--admin-credentials`
   (not shown), `--mint-admin` becomes `--make-admin`. These are decided in
   the sibling document and adopted here. Labels are tags (`--tag k=v`) and
   capabilities `--capability`; suspend and resume are `mashctl endpoint
   suspend|resume`; `mashctl list` is removed. Accepted for the review.
   `endpoint reissue` keeps its name (decided).
4. The walkthrough is two machines (DRAFT10): `myserver` runs the NATS
   server and mashctl, and alice runs there; `bobs-laptop` runs bob. The
   setup block draws both. A tool does not know the name of another machine,
   so a tool's own text says "bob's machine" and the walkthrough headings
   name `bobs-laptop`. Reasoning: the critic's A.0 and C.1 (the setup block said
   one machine above steps that copy files between machines).
5. After Step 3 and after alice the NEXT is one command (check the endpoint
   signs in) and a second, "Or add another endpoint", separated by a blank
   line. Reasoning: the walkthrough makes alice before a session starts, so a
   single NEXT to the session would be untrue on the screen after bob
   (point 11). With one endpoint installed on a machine the check needs no
   setting; with several it carries `MASH_ENDPOINT=<address>`.
6. `--explain` on `mashctl endpoint create ... --make-key` prints one opening
   paragraph and three step blocks, each ending `Done: ...`, with no `$`
   echo per step. Reasoning: there is one command, so one `$`; the rules
   describe the three-command case only.
7. In the grouped list, a long address is not truncated; every row is padded
   to the longest address (third example). Reasoning: a cut address cannot be
   used in `mash send`.
8. `mashctl endpoint list` counts on stdout (`2 endpoints`), after a blank
   line. Reasoning: the catalog puts it on stderr, which a pipe loses.
9. The default `show` prints six lines (DRAFT10: created, type, token, online,
   unread mail, credentials) and `--detail` prints the rest;
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
18. The plain-URL refusal exits 1 in `adopt` and in every command (decided,
    R2; written once in the conventions block). REQ-014 is being updated.
19. The NEXT after `mash key create` carries `<address>`, because mash does
    not know the owner or group on a new machine. The NEXT after `endpoint
    create --public-key` carries the real token file and `bob.key`; it has no
    placeholder: the fingerprint is typed at the prompt of the install (36).
20. The word `seed` stays once, in Step 1 ("NATS calls it a seed"). Review 2
    asked to move it to `--detail`; rule 7 decided it stays in the key step.
21. Table headers (DRAFT10): `ENDPOINT` is the name (`bob`) and `ADDRESS` the
    dotted address (`acme.demo.bob`), in every grouped table (columns `OWNER
    GROUP ENDPOINT ADDRESS`). `PATH` and the old `NAME` header are retired.
    `ENDPOINT` is 8 columns wide, so the `--wide` list header fills 77
    columns; if the NOTE column ever grows its header, drop `HOST` first.
22. Removing `mashctl init --local` reverses the local TLS quick start that
    ADR 0146 and 0.5.5 shipped. The contract that ratifies this document
    carries the ADR (mash order 17: mashctl does not install, configure or
    start NATS). Until then this is a proposal.
23. The plain-URL refusal shows the override, because ADR 0146 requires it, in
    one form only (DRAFT8, REQ-010): `MASH_INSECURE_PLAINTEXT=1` before the
    command, for that run. The config key `insecure_plaintext` is removed and
    no mash command writes the override. The refusal says that credentials and
    mail then cross the connection unencrypted, for a test server only.
24. adopt saves the CA's absolute path. Reasoning: adopt saves `MASH_TLS_CA`
    as typed today, and a relative value is later read under the config
    folder, so the next `mashctl init` can fail TLS verification. The target
    makes adopt resolve the path before saving it; this is a behaviour change
    in adopt, to confirm with the contract.
25. The walkthrough starts Claude Code with the development channel form, the
    solo-user command in docs/install.md section 3. Reasoning: `--channels
    plugin:mash@mash` is refused without a managed channel allowlist; the
    development form needs none and asks for a confirmation at each launch.
26. `--creds-out FILE` is new on `mashctl endpoint create ... --make-key`.
    It writes the credentials at FILE in one command (mode 0600); without
    it they go to the config folder, as today. Reasoning: a person who runs
    the agent on another machine copies one file, not two.
27. No success output names the server or the config file (DRAFT10, V1;
    contract REQ-001). They appear on failure, under `--detail` and in
    `mashctl status`. One line remains, for the trap of a `mashctl` run inside
    a mash session, which inherits MASH_URL: when the server comes from
    MASH_URL and the config file names another server, and only then, the
    command prints `warning: using the server from MASH_URL in this shell (the
    config file names another server)` on stderr before it acts. A normal run
    prints nothing. The result lines that carried `(from ~/.config/mash/
    config.json)` and `(set by MASH_URL ...)` (36 places in DRAFT9) are
    shorter. `make_packet.py` fails any success block that contains `tls://` or
    a config path, except the file lines of `credential install` and `adopt`,
    the trust prompts and the typed commands (the exceptions are listed in the
    script).
28. Both ways to get credentials print file lines in core's shape: two
    spaces, a lowercase verb (`wrote`, `kept`, `copied`), the file, a
    parenthesis (`(mode 600)`, `(server, owner, CA)`). `mash credential
    install` prints `wrote` for the credentials and `config.json` and
    `copied` for the certificate file, or `kept` for `config.json` when it
    already holds the same server and CA; `endpoint create --make-key` prints
    only `wrote` for the credentials (DRAFT10: the `kept` lines for files it
    did not touch are gone). Core confirmed the install block.
29. `mash credential install --creds FILE` (core accepted it) installs the
    file that `--creds-out` wrote, with the same writes as `--key --token`.
    The `--creds-out` output says that copying the file moves a private key
    across machines, and that the three-step path keeps the key on the
    agent's machine.
30. The launch of Claude Code is one command (DRAFT10): bob is alone on
    bobs-laptop, so no `export MASH_ENDPOINT=...` is needed (REQ-009). Where
    two endpoints are installed on one machine, the `no endpoint is set`
    error lists them.
31. No printed NEXT or fix command sets MASH_OWNER (R4): the owner is in
    `config.json`. A session needs its endpoint named in full: `endpoint` in
    `config.json`, or MASH_ENDPOINT in the terminal that launches it.
32. One setting names the endpoint by its full address:
    `MASH_ENDPOINT=acme.demo.bob` (config key `endpoint`). mash uses it
    automatically when exactly one endpoint is installed on the machine.
    MASH_GROUP and MASH_NAME are gone from every command, status mockup,
    walkthrough step, help text, choice and fact. This reverses the earlier
    ruling that the session name comes from MASH_NAME (ADR 0012). The contract
    that ratifies this document must carry the decision (an ADR that departs
    from ADR 0012 on the session name); until then it is a proposal.
33. The refusal for a missing endpoint is `error: no endpoint is set.` with a
    labelled fix that names `endpoint` in the config file and MASH_ENDPOINT.
    It replaces any refusal that asked for MASH_GROUP or MASH_NAME.
34. Conventions block (top of Part 1): exit codes, stdout and stderr, the
    colour rule, `--json`, resolved paths and the label rules are written
    once there. Every error line starts `error:`; hints are `fix:`, `NEXT:`,
    `Or` or `See`. Mockups print `<version>`, never a version number.
35. Audit of confirmations: every command that changes trust, exposure or
    destroys something (contract INV-004). The contract's words: "A change of
    trust (another server or CA for an endpoint) or of exposure (listening on
    more than loopback, firewall rules, a startup unit) needs the person's own
    confirmation: an interactive prompt on a terminal that shows the exact
    address, CA fingerprint, interface or scope and asks the person to type
    it back. No flag (`--yes` or similar) and no environment variable stands
    in for it." And: "INV-004 also covers destruction: `mashctl endpoint
    delete`, and a reissue that replaces a key mash did not make on this
    machine, state what will be lost before acting ... and need the same typed
    confirmation; the output afterwards proves the old credential is refused,
    or gives the one command that proves it." DRAFT10 decides that every
    single reissue of an endpoint qualifies, because every one revokes a key.
    No command in this document offers a flag that skips a confirmation.

    | Command | Kind of change | Confirmation (what the person types) |
    |---|---|---|
    | `adopt` with a CA file | trust | the fingerprint |
    | `credential install`, first install | trust | the fingerprint, from the hand-off |
    | `credential install` replacing the saved server or CA | trust | `--replace` and the new fingerprint |
    | `credential install` over credentials for the same address | replaces a key | the address |
    | `MASH_INSECURE_PLAINTEXT` (any command) | plain text | the variable for one run, a warning on every command; see below |
    | `endpoint delete` | destroys the inbox, retires the address, revokes the key | the address |
    | `endpoint reissue` (one endpoint) | revokes the old key | the address |
    | `endpoint reissue --all`, `--changed` | revokes the old keys | the count (`Type 3 to reissue 3 endpoints`) |
    | `deprovision` | destroys the inbox, revokes the key | the address (D8) |
    | `set`, `suspend`, `resume`, `provision` | none | none |
    | `init`, `limits`, `permissions`, `audit`, `issued`, `inbox inspect` | none | none |
    | `init --local`, `--start` | exposure | removed, so nothing to confirm |

    CLI standard rule 15 says a command prompts only when standard input is a
    terminal, that every prompt has a flag that answers it (`--yes`), and that
    without a terminal it fails with exit 2 naming that flag. INV-004 departs
    from rule 15 on the flag (there is none), and this document departs on the
    code: a command with no terminal exits 4 (not authorized, rule 14), because
    no flag can fix it; a wrong answer exits 3 (cancelled, rule 14). [confirm:
    the contract text cites "the CLI standard's confirmation rule" with no
    number; this is rule 15 of standard 1.1.1, and the contract cites 1.2.0.]
    Without a terminal, a confirmation refuses with `error:` and `fix:` (adopt,
    delete and install show both). [confirm: the plain-text override is a
    variable, as REQ-010 says, while INV-004 says no environment variable
    stands in for a confirmation; this document follows REQ-010 and warns on
    every command, and the contract should say which rule wins.]
36. The hand-off (DRAFT9 U1, DRAFT10 V4): the two files to copy with their
    paths on this machine, one sentence on why the fingerprint goes
    separately, the fingerprint on a line of its own, and a NEXT that carries
    the real token file and key file. The install then asks the person to type
    the fingerprint, as `adopt` does. DRAFT9 put `--ca-fingerprint <fingerprint>`
    in the command (98 columns, with a placeholder for a value mashctl knew; a
    real fingerprint would make it 150). [confirm with core: contract REQ-003
    says `credential install` requires `--ca-fingerprint`, and INV-004 says no
    flag stands in for a typed confirmation. This document follows INV-004. If
    the flag stays for scripts, it is listed in `--help`, and the person's
    command is the short one.] [confirm with core: the token carries the server
    address, and `ca.pem` beside the token is used when `--ca` is not given;
    without both the NEXT needs `--server` and `--ca`.]
37. The MASH_ENDPOINT proposal departs from ADR 0012 on the session name, and
    the removal of `init --local` with its exit code departs from ADR 0146.
    Both are proposals in this document; the ratifying contract carries the
    ADRs (choices 32 and 22).
38. The skill and the script that the `init --local` message names ship from
    another change. Until it is merged and the files are published at the
    URLs the message gives, the message points at nothing; the URLs and the
    release page are to confirm.
39. Replacing installed credentials (DRAFT10, V4): `mash credential install`
    over credentials for the same address needs no `--force`. It says what is
    replaced ("The current credentials for acme.demo.bob use an old key that
    the server now refuses.") and the person types the address. Wrong answer:
    exit 3. No terminal: exit 4. Reasoning: the walk showed the second install
    after a reissue failing with `file_exists` until `--force`.
40. Reissue writes a numbered token (DRAFT10, V4): the first token for an
    address is `acme.demo.bob.token`, the next `acme.demo.bob.2.token`, then
    `.3`. The output says so and keeps the earlier file. Alternative: refuse
    and name `--token-out`. Reasoning: the walk (finding 8) and the critic (E4)
    found that a reissue from the folder where `create` wrote the token failed
    with `file_exists`, so the documented second step failed on first use.
41. The NEXT after a reissue names the new key file the person made
    (`bob2.key`, derived from `bob2.pub` by the rule `mash key create` uses),
    never the old key (walk finding 9: the old line named `bob.seed` and
    failed with `key_mismatch`).
42. `endpoint create` with MASH_URL set refuses with `error:` when the
    administrator credentials in use belong to another server than MASH_URL
    names (DRAFT10, V5), naming both servers. DRAFT9's `--replace` path for
    `endpoint create --make-key` is removed.
43. Exit-code changes in Part 1 (DRAFT10, V3), one per command whose code
    changed: `mashctl status` NOT REACHABLE 1 to 6; `adopt` wrong fingerprint
    1 to 3 and no terminal 1 to 4; `endpoint suspended` is gone (it exited 3
    for "not suspended", the standard's "cancelled").
44. A delete ends with the command that lists what remains and one sentence
    that a new endpoint needs a new name (DRAFT10, V5). The earlier rule that
    every delete ends `NEXT: create an endpoint with a new name` is dropped:
    it offered a step that was not the next one for a person who deleted by
    mistake, and nothing can restore the address.

## Facts to confirm

- [confirm: exit code of `mashctl init --local` and `mashctl list` when they
  print the removal message (2 as written, or 1)]
- [confirm: `mashctl init` is safe to run twice and may say nothing changed]
- [confirm: what the lease storage holds, written as "the sessions that are
  running"]
- [confirm: `mashctl status` connects to the server to test it and to see
  whether init ran; the catalog shows only values read from `config.json`]
- [confirm: `mashctl status` exits 1 on `PROBLEM` and 6 on `NOT REACHABLE`]
- [confirm: `mash key create --out bob.key` writes `bob.pub` (the catalog
  says a trailing `.seed` becomes `.pub`, and any other name gets `.pub`
  appended, so `bob.key` would give `bob.key.pub` today; the rules decide
  `bob.pub`)]
- [confirm: the default config directory is `~/.config/mash` on this
  machine; the order is `MASH_CONFIG_DIR`, then an absolute
  `XDG_CONFIG_HOME/mash`, then `~/.config/mash`, and every printed path shows
  the resolved directory]
- [confirm: `config.json` holds `owner` and may hold `endpoint`; `MASH_ENDPOINT`
  overrides it; with neither set mash uses the only endpoint whose
  credentials file is in the config folder, and refuses with `error: no
  endpoint is set` when there are none or several (replaces MASH_GROUP and
  MASH_NAME of docs/install.md and ADR 0012)]
- [confirm: `--detail` of `show` prints capabilities and tags when set, under
  the words `capabilities` and `tags`]
- [confirm: `endpoint show` can tell a suspended endpoint, and prints it as a
  `status:` line when it is]
- [confirm: the plugin install commands (docs/install.md section 2) and the
  development channel command (section 3) are the start for one person; the
  section also says Claude Code asks for confirmation at each launch]
- [confirm: `mashctl adopt` accepts the server certificate file as
  `MASH_TLS_CA` on the command line, as the catalog's capture does, and
  saves its absolute path in config.json (a behaviour the target requires;
  today it saves the value as typed)]
- [confirm: adopt's only server change with `--make-admin` is recording the
  admin credentials in the record of signed tokens (catalog, "What adopt does
  not do")]
- [confirm: the address `tls://myserver:4222` is an example; the TLS
  certificate must name myserver]
- [walk, answer c: mash runs with the credentials file alone; moving the key
  and the token aside left `mash send` working. Step 3's long form says so.]
- [walk, findings 2, 3, 5, 6, 16: on a fresh machine, v0.5.5's `credential
  create` wrote only the credentials file, so the server address, the CA and
  the group came from nowhere. The target is that `credential install` writes
  `config.json` (server, owner, CA copy) and takes the address from the
  token. Confirm with core that the token holds the server address; the walk
  says today's token holds none.]
- [walk, finding 4: `endpoint create` and `reissue` print the token path
  relative (`./acme.demo.bob.jwt`) and do not say the file is mode 0600. The
  target prints the full path. Confirm the token's mode: the contract says a
  token is not secret, but the walk found it 0600, so the copy across users
  failed until `chmod`.]
- [walk, finding 11: mail that bob sent before a reissue shows as
  `[unknown-signer]` in his own inbox afterwards, while new mail is
  `[verified]`. Nothing says so. Confirm whether the reissue output should
  warn, and what the mash targets show for it.]
- [walk, finding 12: `endpoint show` printed a creds file path although no
  file exists on that machine. The target prints `not on this machine`.]
- [walk, finding 13: a public-key reissue left `acme.demo.bob.creds.lock` on
  the machine with no credentials for bob. Confirm that a reissue with
  `--public-key` takes no lock file where nothing is written.]
- [walk, finding 17: `init --local` listened on 127.0.0.1 only, so a second
  machine cannot reach it. The two-machine flow assumes a server a second
  machine can reach; the setup skill owns that.]
- [confirm: `credential install` can tell that the installed credentials are
  for the same address and that the server refuses their key; it may need one
  connection. If it cannot connect, the sentence should say "may use an old
  key".]
- [confirm: mashctl records which server the administrator credentials were
  made for (adopt wrote both), so a MASH_URL that names another server is
  refused before any connection (the walk saw exit 6 `Authorization
  Violation` and nothing written).]
- [confirm: the CLI standard's rules 14 and 15 are quoted from standard 1.1.1;
  the contract cites 1.2.0 for rules 24 to 33 and gives its "confirmation
  rule" no number.]
- [core confirmed: `mash credential install --key K --token T [--server
  <url>] [--ca <ca.pem>] [--out <dir>] [--force] [--dry-run] [--detail]
  [--json]` prints the block in section 2 (`wrote`, `wrote`, `copied`; or
  `kept` for `config.json`), and `--creds FILE` makes the same writes from
  a credentials file made by `mashctl endpoint create --make-key
  --creds-out FILE`]
- [confirm: `--creds-out FILE` is created exclusively as a new regular file in
  a folder the person owns, without following links, mode 600 from creation,
  completed atomically (INV-003); an existing different file, a link and a
  folder the person does not own are refused before the endpoint is made
  (E3), and the folder is not created]
- [confirm: `mashctl adopt` copies the admin credentials into mashctl's own
  folder (mode 600); the destination `~/.config/mash/server/mashadmin.creds`
  and the `copied` line follow the 0.5.5 layout]
- [confirm: the exact release-page wording in the setup block (`mashctl is a
  program you download from the release page`) and the page address]
- [confirm: a repeat `adopt` keeps the files and prints `kept` lines (H3);
  `endpoint delete` of an address with no server record but a local
  credentials file refuses and names `--force` (H7)]
- [confirm: mashctl can tell that the server came from MASH_URL rather than
  the config file, and the other variables in a session's environment
  (MASH_OWNER, MASH_ENDPOINT) do not change which server is used]

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

# mashctl 0.5.6 target output, part 2: every command except the first-run journey, DRAFT10

Status: design proposal, no code. Written against the catalog
`docs/plans/2026-11-19-cli-output-catalog-mashctl.md` (v0.5.5) and the rules in
`rules.md`. The first-run journey (adopt, init, status, endpoint create / list /
show, key and credentials steps) is in part 1 and is not repeated here.

How to read the mockups.
- Exit codes, stdout and stderr, colour, `--json`, resolved paths, labels and
  versions follow the conventions block at the top of Part 1. They are not
  repeated here.
- `NEXT:` follows a success and `fix:` follows a failure. Both are one sentence
  ending in a colon; a command a person must run is on its own line beneath it,
  indented two spaces, complete, not wrapped, with no full stop or comma after
  it, and `<placeholder>` for any value mash cannot know. A document is not a
  NEXT: it is written `See <path>`.
- A command that reports a state (`audit`, `issued check`)
  prints the verdict first, in words that read without colour (`OK`, `PROBLEM`,
  `INCOMPLETE`, `SUSPENDED`), then only what is wrong or asked for.
- No success output names the server or the config file (conventions block,
  DRAFT10). A failure names the server it could not use. The old `mashctl:
  target server ...` line is gone from every command (P1).
- `$ command` and `[exit N]` lines are annotations for the reader. The program
  prints only the lines under them.
- Names: owner `acme`, group `demo`, endpoints `alice` and `bob`, machine
  `myserver`. One word per thing: endpoint (the thing), name (`bob`), address
  (`acme.demo.bob`), group, owner. People see the dotted address in results
  and type it in commands: mashctl takes `acme.demo.bob`. The slash form may
  still work but is not shown. In a grouped
  table the column that holds the name is headed `ENDPOINT`; the dotted address
  column, where a table has one, is headed `ADDRESS` (part 1's endpoint list
  has both: `OWNER GROUP ENDPOINT ADDRESS`); a column that holds an address in
  a message table is headed `ADDRESS` or `FROM`.
- Points P1..P9 are the nine points of the catalog (P1 preamble, P2 address three
  ways, P3 unaligned tables, P4 NEXT lines, P5 over 80 columns, P6 undefined
  words, P7 agent output shown to a person, P8 repeated fields, P9 unasked
  detail).
- Dry-runs end `Nothing was changed.` and print no NEXT, except a bulk reissue
  dry run, which names the command to run (see Choices). A dry run of a
  command that asks for typed confirmation prints the same "will lose" text
  and asks nothing.
- Exit codes in every `[exit N]` below are the one table in the conventions
  block (rule 14 of the CLI standard, plus 5 and 6 from the contract).

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
               endpoint suspend | resume
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
- `endpoint suspend` and `endpoint resume` replace the top-level `suspend` and
  `resume`. `suspended` is gone (DRAFT10): it existed only to answer a question
  with an exit code (3 for "not suspended"), and 3 is "cancelled" in the one
  exit-code table. `endpoint show` says whether an endpoint is suspended.
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
  endpoint list       list endpoints
  endpoint show       show one endpoint
  endpoint set        change an endpoint's role, host, description, tags
  endpoint reissue    give an endpoint a new key (the old key stops working)
  endpoint delete     delete an endpoint and its inbox for good
  endpoint suspend    stop delivering mail to an endpoint
  endpoint resume     deliver mail to an endpoint again

Inspect
  audit               what the server holds, and what is left over
  issued              list the keys mash issued; check who is connected
  inbox inspect       show the mail stored for one endpoint
  permissions         show what an endpoint is allowed to do

Advanced
  limits              raise inbox size limits to the configured values
  provision           register an endpoint whose NATS user someone else made
  deprovision         remove such an endpoint

To set up a NATS server first, see the setup skill and the script it runs:
https://github.com/bogworks/mash/blob/main/docs/skills/mash-nats-local/SKILL.md
https://github.com/bogworks/mash/blob/main/scripts/nats-local-setup.sh

An address is owner.group.name, for example acme.demo.bob. Add --json for
scripts, --help for a command.
```

What changed and why:

- P5: every line is 80 columns or less; today 20 lines are over, up to 330.
- P6, P7: the text names no lease, bucket, cap, creds, kind or JWT. The words
  that remain (endpoint, tags, key) are explained where they appear.
- P3: one aligned list, grouped by task (connect, endpoints, inspect, advanced),
  instead of two layouts in one table.
- Feedback 14 and rule 1: nothing about the server is printed here; `status`
  shows it on request.
- Rule 14 (feedback 1): `init --local` is not listed; its error (A5) says why.
- Rule 12: one-line summary, usage, grouped list; the example is in each
  command's own `--help`.
- DRAFT10 (critic A.A3): `endpoint list` and `endpoint show` are one line each
  (the column broke when two shared a line); `endpoint resume` is listed; the
  "Moved or removed" block is gone. The removed commands say what replaced
  them in their own error (A5) and in the release notes, and the old block
  lacked `add`, `reissue` and `retire`.

### A4. AFTER: `mashctl endpoint --help`

```text
$ mashctl endpoint --help
[exit 0]
mashctl endpoint - make, change and remove endpoints

Usage: mashctl endpoint <command> [options]

  create      make an endpoint and its credentials
  list        list endpoints (--wide for more columns)
  show        show one endpoint, and whether it is suspended
  set         change an endpoint's role, host, description or tags
  reissue     give an endpoint a new key; the old key stops working
  delete      delete an endpoint, its inbox and its address for good
  suspend     stop delivering mail to an endpoint
  resume      deliver mail to an endpoint again

Options (each command's --help says which it takes)
  --all       every endpoint whose credentials are on this machine (reissue)
  --changed   only the endpoints that need a new key (reissue)
  --dry-run   print what would be done and change nothing
  --detail    print everything that was done
  --wide      add columns to a list

delete and reissue say what will be lost and ask you to type the address.

Example:
  mashctl endpoint create acme.demo.bob --make-key
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

### A4a. AFTER: `mashctl endpoint create --help`

```text
$ mashctl endpoint create --help
[exit 0]
mashctl endpoint create - make an endpoint on the server

Usage:
  mashctl endpoint create <address> --make-key [--creds-out <file>]
  mashctl endpoint create <address> --public-key <file>

Use --make-key when the endpoint runs on this machine: one command makes the
private key, signs it and writes the credentials. Use --public-key when the
key was made on another machine with mash key create.

Flags:
  --make-key           make the private key here and write the credentials
  --creds-out <file>   write the credentials to <file>: a new file, private
                       to you (mode 0600) from the moment it is made, in a
                       folder you own. A file that already exists is refused.
                       Without it they go to
                       ~/.config/mash/<address>.creds
  --public-key <file>  sign a public key made on another machine; writes the
                       token and prints what to give that machine: the token
                       file, the CA file and the CA fingerprint
  --token-out <file>   write the token to <file>. Default: <address>.token in
                       this folder
  --tag K=V            add a tag to the endpoint (repeat for more)
  --tool               mark the endpoint as a tool, not an agent
  --note TEXT          a note kept with the endpoint
  --describe TEXT      one sentence about the endpoint
  --capability WORD    add a capability (a word for something it can do)
  --dry-run            print what would be done and change nothing
  --explain            print each step and why it is done
  --detail             print everything that was done

The address is one argument, owner.group.name. There are no --owner or
--group flags.

Example:
  mashctl endpoint create acme.demo.alice --make-key
```

What changed and why:

- The help is new (DRAFT5): it did not exist in this document. It shows the
  one-command same-machine path, `--make-key --creds-out FILE`, and the
  other-machine path, `--public-key FILE`, with the file each one writes.
- It prints no NEXT: the NEXT of this command depends on the flag used, and
  the command prints it after a run.
- DRAFT9 (U1, U2): `--public-key` says it prints the hand-off block.
- DRAFT10 (V5, V6, critic A.A4): `--replace` is gone from `endpoint create`
  (a MASH_URL that names another server is a refusal; Part 1, choice 42). The
  help documents exactly the flags that work: the address is one dotted
  argument, so `--owner` and `--group` are not flags (E1 no longer shows
  them). Placeholders are `<address>` and `<file>`, one form each. The hand-off
  no longer lists the server: the token holds it (Part 1, choice 36).
- DRAFT8: `--creds-out` is described as a new private file (T1); the flags
  `--tool`, `--note`, `--describe`, `--capability` and `--dry-run`, which the
  v0.5.5 help had, are listed (T5).

### A5. AFTER: removed commands and flags

All exit 2 (wrong usage: the command or flag no longer exists), nothing is
changed. Two lines, `error:` then `fix:` (rule 10). The fix uses the person's
own address and files when they typed them.

```text
$ mashctl add acme.demo.bob --public-key bob.pub
[exit 2]
error: mashctl add was removed.
fix: create an endpoint with the new command:
  mashctl endpoint create acme.demo.bob --public-key bob.pub

$ mashctl reissue acme.demo.bob
[exit 2]
error: mashctl reissue was removed.
fix: reissue an endpoint, with a new key made on the machine where it runs:
  mashctl endpoint reissue acme.demo.bob --public-key <file>

Or, for an endpoint that runs on this machine:
  mashctl endpoint reissue acme.demo.bob --make-key

$ mashctl retire acme.demo.bob
[exit 2]
error: mashctl retire was removed.
fix: delete an endpoint with the new command:
  mashctl endpoint delete acme.demo.bob

$ mashctl list
[exit 2]
error: mashctl list was removed.
fix: list endpoints with the new command:
  mashctl endpoint list

$ mashctl label acme.demo.alice env=demo
[exit 2]
error: mashctl label was replaced by mashctl endpoint set.
fix: use the new command:
  mashctl endpoint set acme.demo.alice --tag env=demo

$ mashctl unlabel acme.demo.alice env
[exit 2]
error: mashctl unlabel was replaced by mashctl endpoint set.
fix: use the new command:
  mashctl endpoint set acme.demo.alice --untag env

$ mashctl set-meta acme.demo.alice --role designer
[exit 2]
error: mashctl set-meta was replaced by mashctl endpoint set.
fix: use the new command:
  mashctl endpoint set acme.demo.alice --role designer

$ mashctl suspend acme.demo.bob
[exit 2]
error: mashctl suspend was replaced by mashctl endpoint suspend.
fix: use the new command:
  mashctl endpoint suspend acme.demo.bob

$ mashctl suspended acme.demo.bob
[exit 2]
error: mashctl suspended was replaced by mashctl endpoint show.
fix: use the new command; its status line says whether bob is suspended:
  mashctl endpoint show acme.demo.bob
```

`resume` gets the same two lines with its own name.

```text
$ mashctl init --local
[exit 2]
error: mashctl no longer installs or starts a NATS server.
fix: set one up first, then connect mashctl to it:
  mashctl adopt --help

See the skill that sets up a server:
https://github.com/bogworks/mash/blob/main/docs/skills/mash-nats-local/SKILL.md
See the script it runs:
https://github.com/bogworks/mash/blob/main/scripts/nats-local-setup.sh

$ mashctl init --start
[exit 2]
error: mashctl no longer installs or starts a NATS server.
fix: set one up first, then connect mashctl to it:
  mashctl adopt --help

See the skill that sets up a server:
https://github.com/bogworks/mash/blob/main/docs/skills/mash-nats-local/SKILL.md
See the script it runs:
https://github.com/bogworks/mash/blob/main/scripts/nats-local-setup.sh

$ mashctl endpoint create acme.demo.bob --public-key b.pub --jwt-out b.jwt
[exit 2]
error: --jwt-out was renamed --token-out.
fix: run it again with the new flag name:
  mashctl endpoint create acme.demo.bob --public-key b.pub --token-out b.jwt

$ mash credential create --seed bob.seed --jwt acme.demo.bob.jwt
[exit 2]
error: mash credential create was replaced by mash credential install.
fix: use the new command, with the same files:
  mash credential install --key bob.seed --token acme.demo.bob.jwt

$ mash credential install --seed bob.seed --jwt acme.demo.bob.jwt
[exit 2]
error: --seed was renamed --key, and --jwt was renamed --token.
fix: run it again with the new flag names, with the same files:
  mash credential install --key bob.seed --token acme.demo.bob.jwt
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
- Feedback 1 and 14 (rule 14): the `init --local` message names the skill and
  the script, which replace `docs/guides/nats-local.md`; getting a server is
  done outside mashctl. DRAFT9: it names them by URL, each alone on a line
  (the URL is 79 columns, so it cannot be indented); a repository path is not
  visible to a mashctl that was downloaded as a program.
- The old `--seed` and `--jwt` names appear only in these messages, so a person
  who has them in a script learns the new names at once. They are not in help.
- DRAFT10 (V5, critic A.A5, F.3): the fixes echo the person's own files and
  values. DRAFT9 told a person with `bob.seed` and `acme.demo.bob.jwt` to run
  `--key bob.key --token acme.demo.bob.token`, files that do not exist. `mashctl
  reissue` no longer suggests `--make-key` first: that moves a remote
  endpoint's key to this machine. It names `--public-key <file>` first (works
  wherever the endpoint runs) and `--make-key` second, for this machine only.
  `mashctl suspended` points at `endpoint show`.
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
$ mashctl endpoint reissue acme.demo.alice --make-key

Reissuing acme.demo.alice will:
  - make a new key here and refuse alice's old key from now
  - sign out the session that runs as alice, until it is restarted
  - keep alice's inbox and its 2 unread messages

Type the address to reissue it: acme.demo.alice

Reissued acme.demo.alice.
  Checked: the server now refuses alice's old key.
  Unread mail in alice's inbox is kept.

  wrote   ~/.config/mash/acme.demo.alice.creds  (mode 600; the new key)

NEXT: on this machine, restart the session that runs as alice, so it uses
the new credentials. Until then alice is signed out.
```

AFTER, with `--detail`

```text
$ mashctl endpoint reissue acme.demo.alice --make-key --detail
(the text of what will change and the prompt, as above)

Reissued acme.demo.alice.
  Checked: the server now refuses alice's old key.
  Unread mail in alice's inbox is kept.

  wrote   ~/.config/mash/acme.demo.alice.creds  (mode 600; the new key)

Detail
  storage name      acme_demo_alice
  old public key    UAO35ZE32HBKAMOGS4CYOJCJWGWGGEGGDEWOLR5Q2L47QFBJMQJRU3CC
  new public key    UA2VGOPQ6HQ2U4DAHV5XFS6NHSQCYBKB5RMLXN4Y52OWXWSXTU3FJTQQ
  old key revoked   2026-11-19 00:30 UTC
  new token issued  2026-11-19 00:30 UTC
  servers           1 of 1 answered
  inbox             kept (2 messages)

NEXT: on this machine, restart the session that runs as alice, so it uses
the new credentials. Until then alice is signed out.
```

AFTER, `--dry-run`

```text
$ mashctl endpoint reissue acme.demo.alice --make-key --dry-run
[exit 0]
Would reissue acme.demo.alice:
  - make a new key here and refuse alice's old key from now
  - sign out the session that runs as alice, until it is restarted
  - replace alice's credentials file here with the new credentials
  - keep alice's inbox and its 2 unread messages
Nothing was changed.
```

What changed and why:

- P1: no preamble. DRAFT10 (V1): the result line names no server.
- P2, P8: the address is `acme.demo.alice` once. The old output had it as
  `acme.team.alice` and `acme.team.alice` on the next line, plus the creds path.
- P6: "creds", "user key" and "account update" are gone. The two "servers
  acknowledged" lines are one `Checked:` line: the server now refuses the old
  key. This is what the old lines meant: mashctl sends the change to every
  server and waits for each one to confirm before it reports success. For one
  server it says "the server"; "N of N servers" appears only when N is more
  than one (critic D.7).
- P4: NEXT names the action, who does it and where.
- P9: public keys, the storage name and times are behind `--detail`.
- DRAFT10 (V2, critic D.1, D.4, INV-004): every single reissue states what it
  changes and asks the person to type the address, because it revokes a key
  and signs a running session out. No flag skips it. The `Checked:` line
  after it is the proof that the old key is refused (mashctl asks the server
  to confirm). The refusal without a terminal, and the wrong answer, are in E4.
  `--dry-run` prints the same text as "Would ..." and asks nothing (rule 11).
- DRAFT10 (V6): the example is alice, who runs on myserver, so her credentials
  are here. A reissue of bob, whose credentials are on bobs-laptop, is B2.

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
$ mashctl endpoint reissue acme.demo.bob --public-key bob2.pub

Reissuing acme.demo.bob will:
  - refuse bob's old key from now, on every machine: bob is signed out until
    the new credentials are installed on bob's machine
  - keep bob's inbox and its 3 unread messages

Type the address to reissue it: acme.demo.bob

Reissued acme.demo.bob.
  Checked: the server now refuses bob's old key, so bob is signed out until
  the new credentials are installed.
  Unread mail in bob's inbox is kept.

  token  ./acme.demo.bob.2.token  (a new file; the first token is kept)

NEXT: copy acme.demo.bob.2.token to bob's machine, and run there:
  mash credential install --key bob2.key --token acme.demo.bob.2.token
```

AFTER, `--dry-run`

```text
$ mashctl endpoint reissue acme.demo.bob --public-key bob2.pub --dry-run
[exit 0]
Would reissue acme.demo.bob:
  - sign a new token for the key in bob2.pub
  - write it to ./acme.demo.bob.2.token (acme.demo.bob.token already exists)
  - refuse bob's old key from now, on every machine
  - keep bob's inbox and its 3 unread messages
Nothing was changed.
```

What changed and why:

- P6: "jwt" and "seed" become token and key (rule 7). The command is the decided
  `mash credential install`.
- DRAFT10 (V4, critic C.3, walk findings 8 and 9): the walk found two faults.
  A reissue wrote `acme.demo.bob.jwt` again, which `create` had already
  written, so it failed with `file_exists`. And its NEXT named the old key
  (`bob.seed`), so following it failed with `key_mismatch`. Now the reissue
  writes a numbered token, `acme.demo.bob.2.token` (then `.3`), says so and
  keeps the first one (Part 1, choice 40). The NEXT names the new key file the
  person made: `bob2.key`, the name `bob2.pub` came from. The old key is not
  named anywhere.
- DRAFT10 (V2): the same typed confirmation as B1, and the `Checked:` line.
  The old key is refused at once, so bob is signed out until the install; the
  "will lose" text says so before the person types.
- The install on bob's machine needs no `--force` and no CA flags: it
  replaces the credentials after a typed confirmation (Part 1, step 3).
- The flag `--jwt-out` is now `--token-out`; `--token-out <file>` overrides
  the numbered name.

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

AFTER, `--dry-run`

```text
$ mashctl endpoint reissue --all --dry-run
[exit 0]
Would give new keys to 2 endpoints. The old keys would stop working.

OWNER  GROUP  ENDPOINT  UNREAD
acme   demo   alice     2
              zoe       0

Skipped, no credentials file on this machine (2):
  acme.demo.bob
  acme.demo.carol

Nothing was changed.

NEXT: to give alice and zoe new keys, run:
  mashctl endpoint reissue --all

Or reissue bob, starting on bob's machine:
  mash key create --out bob2.key
```

AFTER, `--all`, all done

```text
$ mashctl endpoint reissue --all

Reissuing 2 endpoints makes a new key for each and refuses the old keys from
now. The sessions that run as alice and zoe are signed out until they are
restarted. Unread mail is kept.

OWNER  GROUP  ENDPOINT  UNREAD
acme   demo   alice     2
              zoe       0

Type 2 to reissue 2 endpoints: 2

Gave new keys to 2 endpoints.

OWNER  GROUP  ENDPOINT  RESULT
acme   demo   alice     done
              zoe       done

Checked: the server now refuses the old keys.

NEXT: on this machine, restart the sessions that run as alice and zoe, so
they use the new credentials.
```

AFTER, `--all`, a wrong count, and no terminal

```text
$ mashctl endpoint reissue --all
(the text of what will change, and the prompt, as above)
Type 2 to reissue 2 endpoints: 3
[exit 3]
error: what you typed is not 2. Nothing was changed.
fix: run the command again and type the number of endpoints it lists:
  mashctl endpoint reissue --all

$ mashctl endpoint reissue --all
[exit 4]
error: a bulk reissue needs a person at a terminal.
fix: run the same command in a terminal and type the count when asked:
  mashctl endpoint reissue --all

Or, to see what it would change without changing anything:
  mashctl endpoint reissue --all --dry-run
```

AFTER, `--all`, partial outcome (exit 5)

```text
$ mashctl endpoint reissue --all
(the text of what will change, and the prompt, as above)
[exit 5]
Gave a new key to 1 of 2 endpoints. 1 was not done.

OWNER  GROUP  ENDPOINT  RESULT
acme   demo   alice     done
              zoe       not done: its credentials file here is not the one
                        the server expects (nothing was changed for zoe)

Checked: the server now refuses alice's old key.
The key of zoe is unchanged.
warning: do not run --all again: it would give alice a second new key. The
         sessions of alice and zoe need a restart once zoe is done.

error: zoe was not given a new key.
fix: reissue zoe alone, with a key made on the machine where zoe runs:
  mashctl endpoint reissue acme.demo.zoe --public-key <file>

Or, if zoe runs here, move its credentials file aside and reissue it:
  mv ~/.config/mash/acme.demo.zoe.creds ~/.config/mash/acme.demo.zoe.old
  mashctl endpoint reissue acme.demo.zoe --make-key
```

AFTER, `--changed`, nothing to do

```text
$ mashctl endpoint reissue --changed
[exit 0]
Nothing to reissue: no endpoint on this machine needs a new key.
```

What changed and why:

- P3, P2: a grouped table (owner and group once), the dotted address only.
- P6: no `reissued 0, skipped 0, refused 0, error 0` counter line. The result is
  a sentence plus a RESULT column; the reason is in plain words.
- P4: the old partial text mixed an instruction, a warning and a long command in
  one 190 column line. Now: what happened, a `warning:` for what not to do,
  then `error:` and one `fix:`. A partial result is a failure, so it prints
  `fix:`, not `NEXT:` (DRAFT7).
- DRAFT10 (V2, critic D.1): the bulk commands have no confirmation flag. A bulk
  reissue prints what it will change and asks the person to type the count
  (`Type 2 to reissue 2 endpoints:`); the typed number is the guard that a
  person read the list. A wrong count exits 3, no terminal exits 4 (the one
  table). The dry run ends with `NEXT:` and `mashctl endpoint reissue --all`,
  and an `Or` for the skipped endpoint. [confirm: that `--changed` takes the
  same prompt.]
- DRAFT10 (critic C.3, B.3): the partial-failure fix was `mv` the file aside,
  then `--make-key`: that moves the identity of an endpoint that runs on another
  machine to this one. The fix now gives `--public-key <file>` first (true
  wherever the endpoint runs) and the `mv` form second, for an endpoint that
  runs here.
- DRAFT10 (V6): the examples are alice and zoe, whose credentials are on this
  machine; bob and carol run elsewhere and are skipped.
- The old advice ("rerun with --changed") is not used for zoe: once the file is
  moved aside, zoe has no credentials file here, so `--changed` would skip it.
  [confirm: that `--changed` skips an endpoint with no local file]

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
$ mashctl endpoint delete acme.demo.bob --dry-run
[exit 0]
Would delete acme.demo.bob. It would lose:
  - its inbox, with 3 unread messages
  - its address: it would be retired for good and could not be used again
  - its key: the server would refuse bob's credentials from now, on every
    machine
Nothing was changed.
```

AFTER, real (bob's credentials are on another machine)

```text
$ mashctl endpoint delete acme.demo.bob

Deleting acme.demo.bob will lose:
  - its inbox, with 3 unread messages
  - its address: it is retired for good and cannot be used again
  - its key: the server will refuse bob's credentials from now, on every
    machine

Type the address to delete it: acme.demo.bob

Deleted acme.demo.bob. Its address cannot be used again.
  Checked: the server now refuses bob's old credentials.
  Its inbox is deleted, with 3 unread messages.

Nothing can restore a deleted address. A new endpoint needs a new name.

NEXT: see what is left:
  mashctl endpoint list

Or, to see the refusal from bob's machine, run there:
  MASH_ENDPOINT=acme.demo.bob mash status
```

What that last command prints on bob's machine (the other author's `mash`
targets decide the exact lines):

```text
$ MASH_ENDPOINT=acme.demo.bob mash status
[exit 4]
NOT READY: the server refuses bob's credentials.

version:      <version>
server:       tls://myserver:4222 (reachable; REFUSED the credentials)
config:       ~/.config/mash/config.json
credentials:  ~/.config/mash/acme.demo.bob.creds (REFUSED by the server)

error: the credentials for acme.demo.bob are no longer accepted.
fix: ask whoever runs mashctl whether bob was deleted or given a new key.
```

AFTER, a credentials file is here (alice): the file is renamed

```text
$ mashctl endpoint delete acme.demo.alice
(the text of what will be lost, with one more line, and the prompt)
  - its credentials file here: renamed, or removed with --delete-creds

Type the address to delete it: acme.demo.alice

Deleted acme.demo.alice. Its address cannot be used again.
  Checked: the server now refuses alice's old credentials.
  Its inbox is deleted, with 2 unread messages.

  renamed  ~/.config/mash/acme.demo.alice.creds to
           acme.demo.alice.creds.retired-20261119-0024

Nothing can restore a deleted address. A new endpoint needs a new name.

NEXT: see what is left:
  mashctl endpoint list
```

AFTER, `--detail`

```text
$ mashctl endpoint delete acme.demo.bob --detail
(the text of what will be lost, and the prompt, as above)

Deleted acme.demo.bob. Its address cannot be used again.
  Checked: the server now refuses bob's old credentials.
  Its inbox is deleted, with 3 unread messages.

Detail
  storage name    acme_demo_bob
  public key      UAO35ZE32HBKAMOGS4CYOJCJWGWGGEGGDEWOLR5Q2L47QFBJMQJRU3CC
  key revoked     2026-11-19 00:24 UTC
  servers         1 of 1 answered
  mail lost       3 unread messages

Nothing can restore a deleted address. A new endpoint needs a new name.

NEXT: see what is left:
  mashctl endpoint list
```

AFTER, a wrong answer, and no terminal

```text
$ mashctl endpoint delete acme.demo.bob
(the text of what will be lost, and the prompt, as above)
Type the address to delete it: acme.demo.alice
[exit 3]
error: what you typed is not acme.demo.bob. Nothing was changed.
fix: run the command again and type the address you want to delete:
  mashctl endpoint delete acme.demo.bob

$ mashctl endpoint delete acme.demo.bob
[exit 4]
error: deleting an endpoint needs a person at a terminal.
fix: run the same command in a terminal and type the address when asked:
  mashctl endpoint delete acme.demo.bob

Or, to see what it would lose without deleting anything:
  mashctl endpoint delete acme.demo.bob --dry-run
```

What changed and why:

- P2: the address appears once, dotted. The old output printed it as
  `acme.team.dave (acme.team.dave)` on the result line.
- P8, P9: "revoked" was said three times (twice with the key, once as a count).
  It is one `Checked:` line now. The key text and the count are in `--detail`.
- P6: "directory record", "burn the address", "creds", "account JWT" are gone;
  "retired for good and cannot be used again" says what burning does.
- DRAFT10 (V2, critic D.1, D.2, INV-004; CLI standard rule 15): delete is
  permanent, so it says what will be lost before it acts (the unread count,
  the address retired for good, the key refused from now) and the person types
  the address. The count that was behind `--detail` is in the default. There is
  no flag that skips it. A wrong answer exits 3; no terminal exits 4 and offers
  `--dry-run`. The hint to use `--delete-creds` was after the file was
  renamed, too late to use; it is in the prompt text now, where it can be
  used. `--dry-run` prints the same "would lose" text and asks nothing.
- DRAFT10 (V2): the proof that the old credential is refused is two things.
  mashctl asks the server to confirm and prints `Checked: ...`. And the one
  command that shows it from bob's machine, `MASH_ENDPOINT=acme.demo.bob mash
  status`, is the `Or` line, with what it prints. [confirm: the exact lines
  belong to the `mash` targets; the verdict and exit 4 are this document's
  proposal.]
- DRAFT10 (V1, V5, critic B.5, F.3): no server line. The NEXT is no longer
  "create an endpoint with a new name" (a step the person may not want, and
  it never offered the retired address). It is the command that lists what
  remains, with one sentence that a deleted address cannot be restored and a
  new endpoint needs a new name (Part 1, choice 44).
- The `renamed` file line shows the path because the person may want the file
  (a `.retired-` copy of credentials that no longer work).

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
$ mashctl endpoint delete acme.demo.bob
[exit 0]
acme.demo.bob was already deleted. Its address cannot be used again.
Nothing was changed.

NEXT: see what is left:
  mashctl endpoint list
```

A repeat where a credentials file is still here and the server has no record
refuses and names `--force` (H7); it does not rename the file by itself. The
plain command never touches a file it cannot tie to a record.

AFTER, `--force` when the server has no record (finishes a half-done delete)

```text
$ mashctl endpoint delete acme.demo.bob --force
[exit 0]
Finished deleting acme.demo.bob. Its address cannot be used again.
  The server has no record of it. Only the credentials file here was
  handled.
  No key was revoked, because mash does not know which key it was.

  renamed  ~/.config/mash/acme.demo.bob.creds to
           acme.demo.bob.creds.retired-20261119-0031

NEXT: see what is left:
  mashctl endpoint list
```

AFTER, `--force --dry-run`

```text
$ mashctl endpoint delete acme.demo.bob --force --dry-run
[exit 0]
Would handle acme.demo.bob:
  - the server has no record of it, so no key would be revoked
  - the address stays retired: it cannot be used again
  - its credentials file here would be renamed
Nothing was changed.
```

AFTER, `--no-revoke` (the warning is printed once, after the results)

```text
$ mashctl endpoint delete acme.demo.bob --no-revoke

Deleting acme.demo.bob will lose:
  - its inbox, with 3 unread messages
  - its address: it is retired for good and cannot be used again
Its key will NOT be revoked: bob's credentials keep working until whoever
runs the server revokes the key.

Type the address to delete it: acme.demo.bob

Deleted acme.demo.bob. Its address cannot be used again. KEY NOT REVOKED.
  Its inbox is deleted, with 3 unread messages.
  Its key was not revoked.

warning: bob's key still works. Until it is revoked, a copy of bob's
credentials can still sign in, send and read mail. Ask whoever runs the
server to revoke it with their tools (in NATS, add it to the account's
revocation list). bob's public key is:
  UAO35ZE32HBKAMOGS4CYOJCJWGWGGEGGDEWOLR5Q2L47QFBJMQJRU3CC

NEXT: see what is left:
  mashctl endpoint list
```

What changed and why:

- P1, P2, P8: no preamble, one address form, and the "already retired" case is
  one sentence. The old output said `deleted ...` when nothing was deleted.
- A repeat delete says only what the command can know: the endpoint is already
  deleted and mash holds no record or key for it. It does not say the key was
  revoked, because the first delete may have used `--no-revoke`.
- P5: the longest old line (250 columns) is wrapped under 80.
- `--no-revoke`: the old output printed the warning twice (both on stderr) plus
  a third long line on stdout. It now prints the warning once, after the
  results, in plain words, with what to do. The first line says `KEY NOT
  REVOKED` so the state reads before anything else. DRAFT10: it asks for the
  typed address like every delete, and its "will lose" text says the key will
  not be revoked.
- DRAFT10 (V2, critic D.3): `--force` and the plain repeat no longer give the
  same result. The plain command refuses when it finds a credentials file with
  no record on the server (H7), and `--force` is the way to say "finish it".
  `--force` loses nothing that exists on the server, so it asks nothing.
- Every delete that finishes ends with `mashctl endpoint list` and the
  sentence that a new endpoint needs a new name; the dry run prints no NEXT
  (rule 11).
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

Usage: mashctl endpoint set <address> [options]

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
  mashctl endpoint set acme.demo.alice --role designer --tag team=ui
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
$ mashctl endpoint set acme.demo.alice --role designer --host myserver \
>     --describe "makes designs" --tag team=ui --capability read
[exit 0]
Updated endpoint acme.demo.alice.
  role              designer
  host              myserver
  description       makes designs
  tag added         team=ui
  capability added  read
```

AFTER, clearing

```text
$ mashctl endpoint set acme.demo.alice --role - --untag team
[exit 0]
Updated endpoint acme.demo.alice.
  role         cleared
  tag removed  team
```

AFTER, nothing differs

```text
$ mashctl endpoint set acme.demo.alice --host myserver
[exit 0]
Endpoint acme.demo.alice already has these values. Nothing was changed.
```

AFTER, removing a tag that is not set (warns, still exit 0)

```text
$ mashctl endpoint set acme.demo.alice --untag color
[exit 0]
warning: acme.demo.alice has no tag "color". Nothing was changed.
```

AFTER, `--dry-run`

```text
$ mashctl endpoint set acme.demo.alice --role designer --tag team=ui --dry-run
[exit 0]
Would update endpoint acme.demo.alice:
  - set role to designer
  - add tag team=ui
Nothing was changed.
```

What changed and why:

- P9: the old line printed all five fields every time, so clearing one printed
  four unchanged values. Only changed fields are shown.
- P6: role, host, description, tag and capability are defined in `--help`.
- DRAFT10 (critic A.C2): the result words have one order, `tag added` and
  `capability added`; the examples use US spelling (`color`).
- The old dry-run said "would have labeled ... (nothing written)" without saying
  what; the new one lists the changes.
- No NEXT: nothing is required afterwards, and the key does not change.

### C3. suspend, resume, suspended

Behaviour kept (docs/mashctl.md): `suspend` refuses all inbound mail to the
endpoint's inbox and keeps stored mail readable; the endpoint's key still
works; suspending twice returns the same suspension. `deprovision` needs the
suspension id and the last message number from here. DRAFT10: `suspended` is
gone; whether an endpoint is suspended is a line of `endpoint show`, which
also prints the id and the last message number.

BEFORE (catalog, suspend, suspended_yes, suspended_not), marks P1 P5 P6 P9:

```text
suspended acme.team.bob: id 63afd15649fb5dee9904fbba687fbe7c, last_seq 0, msgs 0 (new mail parked; the endpoint's credentials are still valid)
suspended acme.team.bob since 2026-11-19T00:22:17Z: id 63afd15649fb5dee9904fbba687fbe7c, last_seq 0, msgs 0
acme.team.bob is not suspended
```

AFTER

```text
$ mashctl endpoint suspend acme.demo.bob
[exit 0]
Suspended mail to endpoint acme.demo.bob.
  New mail to bob is refused. The 3 messages already stored stay readable.
  The key of bob still works: it can sign in and read what is stored.
  suspension id  63afd15649fb5dee9904fbba687fbe7c
  last message   number 3
```

AFTER, resume

```text
$ mashctl endpoint resume acme.demo.bob
[exit 0]
Resumed mail to endpoint acme.demo.bob.
  New mail to bob is delivered again.
```

AFTER, asking (`endpoint show` of a suspended endpoint)

```text
$ mashctl endpoint show acme.demo.bob
[exit 0]

Endpoint acme.demo.bob

created:        2026-11-19 00:22 UTC
type:           agent
token:          valid, not revoked
status:         SUSPENDED since 2026-11-19 00:22 UTC
suspension id:  63afd15649fb5dee9904fbba687fbe7c
last message:   number 3 (3 messages stored)
online:         no
unread mail:    3
credentials:    not on this machine
```

AFTER, `--dry-run`

```text
$ mashctl endpoint suspend acme.demo.bob --dry-run
[exit 0]
Would suspend mail to endpoint acme.demo.bob:
  - refuse new mail to bob; the 3 stored messages stay readable
  - leave bob's key working
Nothing was changed.
```

What changed and why:

- P1, P5, P6: no preamble; no `id`, `last_seq`, `msgs`, `parked`. "suspension
  id" and "last message" stay because `deprovision` needs both. DRAFT10
  (critic A.C3): the labels are lowercase like every other label block. They
  stay in the default because the person needs them for the drain command;
  [confirm: or print them only in a NEXT that shows the drain command with the
  id filled in].
- DRAFT10 (V3, critic A.A4): `endpoint suspended` is removed. It answered with
  exit 3 for "not suspended", and 3 is "cancelled" in the one exit-code table.
  `endpoint show` prints `status: SUSPENDED since <time>` with the id and the
  last message number; an endpoint that is not suspended prints no status line.
  Exit codes: suspend 0 (also the second time), resume 0, errors 1, usage 2.
- No NEXT on suspend or resume: the next screen is whatever the person
  decides, not a step of a guide.
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
moved since an earlier `--json` report; exit 6 when the server could not be
reached. The sentence "changes nothing; shows what the server holds, not who
used it" is in `mashctl audit --help`, not printed on every run (critic A.D1).

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

Audit of owner acme, 2026-11-19 00:22 UTC

OWNER  GROUP  ENDPOINT  ONLINE  MAIL STORED  UNREAD  DELIVERY
acme   demo   alice     no      0            0       on
              bob       no      3            3       on
```

AFTER, `--wide` (adds columns), `--detail` (adds the rest)

```text
$ mashctl audit --wide
[exit 0]
OK: nothing is wrong and nothing is left over.

Audit of owner acme, 2026-11-19 00:22 UTC

OWNER  GROUP  ENDPOINT  ONLINE  MAIL STORED  UNREAD  DELIVERY  FIRST  LAST
acme   demo   alice     no      0            0       on        0      0
              bob       no      3            3       on        1      3

FIRST and LAST are the numbers of the first and last message stored.
```

```text
$ mashctl audit --detail
[exit 0]
OK: nothing is wrong and nothing is left over.

Audit of owner acme, 2026-11-19 00:22 UTC

OWNER  GROUP  ENDPOINT  ONLINE  MAIL STORED  UNREAD  DELIVERY
acme   demo   alice     no      0            0       on
              bob       no      3            3       on

Detail
  Credentials folders searched: ~/.config/mash
  acme.demo.bob
    storage name    acme_demo_bob
    public key      UAO35ZE32HBKAMOGS4CYOJCJWGWGGEGGDEWOLR5Q2L47QFBJMQJRU3CC
    inbox           inbox-acme_demo_bob, messages 1 to 3
    read position   0 confirmed, 0 delivered, 3 waiting, 0 redelivered
    mail subject    agents.mail.acme.demo.bob
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

Audit of owner acme, 2026-11-19 00:24 UTC

OWNER  GROUP  ENDPOINT  ONLINE  MAIL STORED  UNREAD  DELIVERY
acme   demo   alice     no      0            0       on
              bob       no      3            3       on

error: the audit found 1 problem.
fix: if you do not need that file, finish the delete that left it:
  mashctl endpoint delete acme.demo.dave --force

Or, if the file may belong to an endpoint that exists, list the endpoints:
  mashctl endpoint list
```

Without `--strict` the same finding prints the same rows and verdict and
exits 0; only `--strict` turns a finding into exit 1:

```text
$ mashctl audit
[exit 0]
PROBLEM: 1 credentials file belongs to no endpoint.
  ~/.config/mash/acme.demo.dave.creds
  It may be left over from a delete or a reissue that was cut short.

Audit of owner acme, 2026-11-19 00:24 UTC

OWNER  GROUP  ENDPOINT  ONLINE  MAIL STORED  UNREAD  DELIVERY
acme   demo   alice     no      0            0       on
              bob       no      3            3       on
```

AFTER, `--compare`

```text
$ mashctl audit --compare audit-earlier.json
[exit 0]
OK: nothing is wrong and nothing is left over.

Audit of owner acme, 2026-11-19 00:25 UTC

OWNER  GROUP  ENDPOINT  ONLINE  MAIL STORED  UNREAD  DELIVERY
acme   demo   alice     no      0            0       on
              bob       no      3            3       on

Compared with the audit of 2026-11-19 00:22 UTC: 2 inboxes checked, 0
changed, 0 gone, 0 new.
```

What changed and why:

- P7, P9: the default is one line per endpoint. The 330 column line (public
  key, stream name, six consumer fields, subject) is behind `--detail`. The
  eight lines of retired markers and tombstones, which only describe deleted
  endpoints, are behind `--detail` too.
- DRAFT10 (V1, critic A.D1): the `Audit of owner acme` header names no server,
  and the parenthetical that printed on every run is in `--help`. `--strict`'s
  fix names the command that finishes the delete that left the file (B5);
  `FIRST` and `LAST` get a footer; `--compare` says what was compared.
- P2, P8: one address form; the stream name and storage name are shown only in
  `--detail`. The old line had `acme_team_alice`, `acme.team.alice` and
  `inbox-acme_team_alice` in one row.
- P3: a table with a header, replacing tab separated `name value` pairs. The
  column of names is headed ENDPOINT, as in part 1.
- P6: lease, bucket, orphan, fenced and creds are replaced by "leftovers",
  "problem" and "credentials file". Online means a session is signed in now.
- The verdict is the first line: `OK` or `PROBLEM`, then what is wrong. The old
  330 character `--strict` line becomes `error:` and `fix:`; exit codes are
  0 clean, 1 finding under `--strict`, 6 the server could not be reached
  (DRAFT10: it was 3).
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
Keys that mash has issued

OWNER  GROUP  ENDPOINT   PUBLIC KEY   ISSUED (UTC)      STATUS
acme   demo   alice      UAGT4ZQX...  2026-11-19 00:21  in use
              bob        UAO35ZE3...  2026-11-19 00:21  in use
-      -      mashadmin  UBFOC5KR...  2026-11-19 00:21  in use

3 keys issued, none revoked. mashadmin is the key mashctl signs in with.
```

AFTER, `--wide`

```text
$ mashctl issued list --wide
[exit 0]
Keys that mash has issued

OWNER  GROUP  ENDPOINT   PUBLIC KEY                 ISSUED (UTC)      STATUS
acme   demo   alice      UAGT4ZQXALANXPHIAET7WXOZQ  2026-11-19 00:21  in use
              bob        UAO35ZE32HBKAMOGS4CYOJCJW  2026-11-19 00:21  in use
-      -      mashadmin  UBFOC5KRY5JRFS5QWS3K5GHKT  2026-11-19 00:21  in use

3 keys issued, none revoked. mashadmin is the key mashctl signs in with.
```

What changed and why:

- P3: header row and aligned columns. The administrator key (the one mashctl
  connects with) belongs to no owner and group, so it shows `-` in both.
- P2, P6, P9: `generation 0`, `tags` and `purpose issue` are not shown. The
  purpose (issue, rotation) and the full public key appear in `--detail`
  [confirm: the exact `--detail` fields are decided with the key store].
- STATUS is "in use" or "revoked 2026-11-19 00:30" (UTC). "revoked-at" is gone.
- DRAFT10 (critic A.D2): the unheaded seventh column is gone; the footer says
  what `mashadmin` is. `--wide` has the same column order as the default. It
  still cuts the public key (to 25 characters, with no `...`): the contract's
  80-column rule has no `--wide` exemption (declined; `--json` and `--detail`
  carry the whole key). [confirm: whether to show the administrator key at
  all without `--detail`]

### D3. issued check

What it does: asks every server which keys are connected now and compares them
with the keys mash issued. It reports a connected key mash did not issue (key,
server, connection name, who signed it), a revoked key still connected, and
keys that are issued but not connected (normal). Exit 0 clean and complete, 1 a
finding, 6 nothing found but a server did not answer (the check is incomplete).

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

Checked the server. 1 key is connected.
```

AFTER, a finding (exit 1)

```text
$ mashctl issued check
[exit 1]
PROBLEM: 1 of the 2 connected keys was not issued by mash.

Checked the server.

Connected, not issued by mash
  public key       UBH6YD4HP7LRXHHW7ADO5AKELNC3Z7JKW24V6DRNDKW7QVZVSAIDSKFT
  server           tls://myserver:4222
  connection name  build-agent
  signed by        a key that can sign for your account

error: a key that mash did not issue can sign in to your mash account.
fix: find who made it. If it is not yours, revoke it with the tools of
     whoever runs the server, and check the account's signing keys.
```

AFTER, a revoked key still connected (exit 1)

```text
$ mashctl issued check
[exit 1]
PROBLEM: a revoked key is still connected.

Checked the server. 2 keys are connected.

Revoked, still connected
  endpoint         acme.demo.bob
  public key       UAO35ZE32HBKAMOGS4CYOJCJWGWGGEGGDEWOLR5Q2L47QFBJMQJRU3CC
  server           tls://myserver:4222

error: the revocation of bob's key did not take effect on this server.
fix: check that every server has the latest account update, then check again:
  mashctl issued check
```

AFTER, incomplete (exit 6)

```text
$ mashctl issued check
[exit 6]
INCOMPLETE: 1 of 2 servers did not answer.

Checked 1 of 2 servers. Nothing wrong was found on the server that answered.
mashctl expects 2 servers (set with --servers).

error: 1 of 2 servers did not answer, so the check cannot be called clean.
fix: bring the server back, then run the check again:
  mashctl issued check
```

What changed and why:

- P3, P7, P9: the list of issued-but-not-connected keys (a 56 character key per
  row, no header) is one sentence with a count. The keys are in `--detail`.
- P6: "unrecorded", "recorded", "revoked but connected" are gone; "issued by
  mash" and "revoked" are used instead.
- DRAFT10 (V3, critic A.D3): an incomplete check exits 6 (a server did not
  answer), not 2, which is the usage code; an unreachable server is 6 here as
  everywhere. The server is named in the finding blocks (a failure) and not in
  the clean result; "mash-local" is `tls://myserver:4222`; "the account's
  signing key" is said in plain words.
- The result is the first line (`OK`, `PROBLEM` or
  `INCOMPLETE`), and `error:` and `fix:` lines follow a finding. The keys that
  are issued but not connected are not printed by default; they are normal.
- No NEXT: the next step depends on who made the key, which mash cannot tell.

### D4. inbox inspect

What it does: shows one endpoint's stored mail and where its delivery stands.
It reads only. Exit 0, 1 refusal (no such endpoint, no inbox), 2 usage, 6 the
server could not be reached.

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
$ mashctl inbox inspect acme.demo.bob
[exit 0]
Inbox of acme.demo.bob: 3 messages stored, 3 waiting to be delivered.

NO.  FROM             TRUST     STORED (UTC)      SIZE   TYPE      STATE
1    acme.demo.alice  verified  2026-11-19 00:22  11 B   task      waiting
2    acme.demo.alice  verified  2026-11-19 00:22  10 B   question  waiting
3    acme.demo.alice  verified  2026-11-19 00:22  2 B    task      waiting

STATE comes from the inbox's delivery position, not from each message.
```

AFTER, a part of the inbox (`--from 2 --limit 1`)

```text
$ mashctl inbox inspect acme.demo.bob --from 2 --limit 1
[exit 0]
Inbox of acme.demo.bob: 3 messages stored, 3 waiting to be delivered.
Showing message 2 only (--from 2 --limit 1).

NO.  FROM             TRUST     STORED (UTC)      SIZE   TYPE      STATE
2    acme.demo.alice  verified  2026-11-19 00:22  10 B   question  waiting
```

AFTER, `--detail`

```text
$ mashctl inbox inspect acme.demo.bob --detail
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
- DRAFT10 (critic A.D4): a footer says STATE comes from the inbox's delivery
  position, so the column does not claim a per-message fact the server does
  not keep (`--detail` has the full explanation).
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

  inbox size            16 MiB per endpoint (the oldest mail is dropped first)
  inbox age             30 days  (older mail is dropped)
  directory size        16 MiB
  online-status size    4 MiB
  readers per inbox     no limit

The limits are set in the config file; mashctl status says where it is.

NEXT: after you change the limits in the config file, run:
  mashctl limits
```

AFTER, raising

```text
$ mashctl limits
[exit 0]
Raised the inbox size limit of 2 endpoints.

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
$ mashctl limits acme.demo.bob
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
- `limits --show` ends with a labelled `NEXT:` (DRAFT7): the unlabelled
  sentence was a hint. The command raises the inboxes after the person edits
  the file.
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
$ mashctl permissions acme.demo.alice
[exit 0]
What acme.demo.alice is allowed to do

It may:
  - send mail, prompts and status messages to any endpoint of owner acme
  - receive prompts and status messages sent to acme.demo.alice
  - read its own inbox, and no other inbox
  - show that it is online, and see which acme endpoints are online
  - read the directory of endpoints

It may not send to, or read from, another owner's endpoints.

These are the same for every endpoint of owner acme.
```

AFTER, `--detail` (first lines)

```text
$ mashctl permissions acme.demo.alice --detail
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
- DRAFT10 (critic A.D6): the closing hint is gone; `mashctl permissions --help`
  lists `--detail` and `--nsc`.

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

Usage: mashctl provision <address> --user KEY --account KEY [options]

Use this on a server where mashctl cannot make NATS users. For a server
mashctl adopted and can sign users for, use endpoint create instead.

Options
  --user KEY       the endpoint's NATS user public key (starts with U)
  --account KEY    the account's public key (starts with A)
  --role R         what the endpoint does
  --host H         the machine it runs on
  --describe TEXT  one sentence about it
  --tag K=V        add a tag (repeat for more)
  --note TEXT      a note kept with the endpoint

Example:
  mashctl provision acme.demo.alice --user UD7CJ5RS... --account AD7BK6VI...
```

AFTER, real

```text
$ mashctl provision acme.demo.alice --user UD7CJ5RS... --account AD7BK6VI...
[exit 0]
Registered endpoint acme.demo.alice.
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
`endpoint suspend`) it removes only if the inbox is still suspended with that
id and no message arrived after message N. It does not check that the messages
were read. `--no-revoke` skips the revocation.

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

Usage: mashctl deprovision <address> [options]

Revokes the endpoint's NATS user (it stops working), then removes the
endpoint and its inbox. Unread mail is lost unless you drain it first. It
says what will be lost and asks you to type the address.

Options
  --suspension ID      the suspension id printed by endpoint suspend
  --drained-to N       the last message number you read (needs --suspension)
  --user KEY           revoke this NATS user instead of the one on record
  --no-revoke          remove the endpoint but do not revoke its key
  --servers N          how many servers must confirm (else MASH_SERVERS)
  --sys-creds <file>   system account credentials (else MASH_SYS_CREDS)

Example, without losing mail:
  mashctl endpoint suspend acme.demo.alice
  mashctl inbox inspect acme.demo.alice
  mashctl deprovision acme.demo.alice --suspension ID --drained-to 41
```

AFTER, plain

```text
$ mashctl deprovision acme.demo.alice

Removing acme.demo.alice will lose:
  - its inbox, with 2 unread messages
  - its key: the server will refuse alice's NATS user from now

Type the address to remove it: acme.demo.alice

Removed endpoint acme.demo.alice.
  Checked: the server now refuses its NATS user.
  Its inbox is deleted, with 2 unread messages.

NEXT: delete any copy of alice's credentials file on its machine.
```

AFTER, after a drain

```text
$ mashctl deprovision acme.demo.alice --suspension 63afd156... --drained-to 41
(the text of what will be lost, and the prompt, as above)

Removed endpoint acme.demo.alice.
  Checked before removing: the inbox was suspended with id 63afd156..., and
  no message had arrived after message 41.
  mash does not check that messages 1 to 41 were read. They are deleted.
  Checked: the server now refuses its NATS user.

NEXT: delete any copy of alice's credentials file on its machine.
```

AFTER, `--no-revoke` (the warning once)

```text
$ mashctl deprovision acme.demo.alice --no-revoke
(the text of what will be lost, without the key line, and the prompt)

Removed endpoint acme.demo.alice. KEY NOT REVOKED.
  Its inbox is deleted, with 2 unread messages. Its key was not revoked.

warning: alice's key still works. Until it is revoked, a copy of alice's
credentials can still sign in, send and read mail. Revoke it with the
tools of whoever runs the server.

NEXT: ask whoever runs the server to revoke alice's key, whose public key is:
  UD7CJ5RSKIOORP6QZS6RB5I3JD2LX4IOHRXLC6FBZW6MVYGYU62MBPNS
```

What changed and why:

- P1, P5, P6, P8: one "no longer works" line instead of two; JWT, issue record
  and "revoked in the account JWT" removed; the warning is printed once.
- DRAFT10 (V2, critic A.D8): `deprovision` destroys an inbox and revokes a
  key, as `endpoint delete` does, so it says what will be lost and asks the
  person to type the address (Part 1, choice 35). The warning "mash has no
  record of issuing this key, so it cannot check that no credential for it is
  dated in the future" is gone from the default: the person can do nothing
  with it; it is in `--detail`. The help example names the command that reads
  the stored mail.
- P4: NEXT is true: deprovision of an endpoint made by `endpoint create` leaves
  its credentials file in the config folder (catalog, depro_hal).
- Drained delete (review finding 3): the line "none was lost" is removed. The
  guard (`--suspension ID --drained-to N`) proves two things: the inbox was
  still suspended with that id, and its last stored message number is N (no
  message arrived after N). It does not prove that any message was read or
  acknowledged. The output states those two checks and says plainly that
  reading is not checked. Before removing unread mail a person reads the
  stored messages (`mash inbox`, `mash read`) or copies them, then runs the
  command with the id and number that `endpoint suspend` printed.
- `deprovision` has no `--dry-run` today (see Facts to confirm).

---

## E. Refusals and errors

Form (rule 10): the first line is `error:` and says what is wrong; the second
line is `fix:` and says what to do, as one sentence ending in a colon with the
command on its own indented line beneath it (a fix that needs no command is one
sentence ending in a full stop); nothing else by default. Every line is 80
columns or less, and every command line is 78 or less. The machine code
(`file_exists`, `key_unchanged`, ...) is printed only with `--json` and
`--detail`. Exit codes follow the one table in the conventions block at the top
of Part 1. A key, a token or a tag value that failed a check is named but never
printed back, so a secret pasted into the wrong place is not echoed; an
address is not secret and is printed back. A fix that sets configuration names the
file first, with its resolved path. A variable set with `export` is offered
only as a test in the current shell, because it does not reach a session
started anywhere else. A second choice after a fix starts `Or`; a step that
follows the fix is part of the fix sentence.

With `--detail`, the machine code is added as a third line, for example:

```text
$ mashctl endpoint reissue acme.demo.bob --public-key bob.pub --detail
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
$ mashctl endpoint reissue acme.demo.bob
[exit 2]
error: a reissue needs a new key: give --public-key <file> or --make-key.
fix: give the key made on the machine where bob runs:
  mashctl endpoint reissue acme.demo.bob --public-key <file>

Or, if bob runs on this machine, make the new key here:
  mashctl endpoint reissue acme.demo.bob --make-key
usage: mashctl endpoint reissue <address> (--make-key | --public-key <file>)

$ mashctl endpoint reissue
[exit 2]
error: no endpoint address given.
fix: name the endpoint to reissue:
  mashctl endpoint reissue <address> --public-key <file>
usage: mashctl endpoint reissue <address> (--make-key | --public-key <file>)

$ mashctl endpoint set acme.demo.alice
[exit 2]
error: nothing to change: give at least one of --role, --host, --describe,
       --tag, --untag, --capability.
fix: for example, set a role:
  mashctl endpoint set acme.demo.alice --role <role>
usage: mashctl endpoint set <address> [options]

$ mashctl endpoint set acme.demo.alice --policy x
[exit 2]
error: unknown option --policy.
fix: see the options:
  mashctl endpoint set --help
usage: mashctl endpoint set <address> [options]

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
  mashctl inbox inspect <address> --limit 5
usage: mashctl inbox inspect <address> [--from N] [--to N] [--limit N]

$ mashctl endpoint delete bob
[exit 2]
error: "bob" is not an endpoint address.
fix: give the whole address, owner, group and name:
  mashctl endpoint delete acme.<group>.bob
usage: mashctl endpoint delete <address> [--force] [--no-revoke] [--dry-run]
```

Note (DRAFT10, critic A.E): an address is not secret, so every address error
prints the value the person typed, as `delete bob` does and as `limits nobody`
now does. Keys, tokens and tag values are never printed back.

Other usage errors, same form:

```text
$ mashctl endpoint reissue acme.demo.bob --make-key --public-key bob.pub
[exit 2]
error: give --make-key or --public-key, not both.
fix: choose one; to use the key made on bob's machine:
  mashctl endpoint reissue acme.demo.bob --public-key bob.pub
usage: mashctl endpoint reissue <address> (--make-key | --public-key <file>)

$ mashctl endpoint reissue --all --public-key bob.pub
[exit 2]
error: --all and --changed make the new keys on this machine, so they cannot
       take --public-key.
fix: leave out --public-key; to see what would change:
  mashctl endpoint reissue --all --dry-run
usage: mashctl endpoint reissue (--all | --changed) [--dry-run]

$ mashctl endpoint reissue --all --yes
[exit 2]
error: unknown option --yes. A bulk reissue asks you to type the count.
fix: run it without the flag, in a terminal:
  mashctl endpoint reissue --all
usage: mashctl endpoint reissue (--all | --changed) [--dry-run]

$ mashctl endpoint create bob
[exit 2]
error: an endpoint address has three parts: owner, group and name.
fix: give all three:
  mashctl endpoint create acme.<group>.bob --make-key
usage: mashctl endpoint create <address> (--make-key | --public-key <file>)

$ mashctl endpoint create demo.bob --make-key
[exit 2]
error: "demo.bob" has two parts; an address has three, owner.group.name.
fix: add the owner:
  mashctl endpoint create acme.demo.bob --make-key
usage: mashctl endpoint create <address> (--make-key | --public-key <file>)

$ mashctl endpoint create bob --owner acme --make-key
[exit 2]
error: unknown option --owner.
fix: the address is one argument, owner.group.name:
  mashctl endpoint create acme.<group>.bob --make-key
usage: mashctl endpoint create <address> (--make-key | --public-key <file>)

$ mashctl endpoint create acme.demo.bob --group ops --make-key
[exit 2]
error: unknown option --group.
fix: give the group inside the address:
  mashctl endpoint create acme.ops.bob --make-key
usage: mashctl endpoint create <address> (--make-key | --public-key <file>)

$ mashctl endpoint create acme.demo --make-key
[exit 2]
error: no endpoint name given.
fix: add the name:
  mashctl endpoint create acme.demo.<name> --make-key
usage: mashctl endpoint create <address> (--make-key | --public-key <file>)

$ mashctl endpoint create acme.demo.bob --make-key --tag "bad key=x"
[exit 2]
error: a tag name must start with a letter, then up to 31 letters, digits,
       or _ . -  (the value is not shown).
fix: write a tag like this:
  mashctl endpoint create acme.demo.bob --make-key --tag team=ui
usage: mashctl endpoint create <address> (--make-key | --public-key <file>)

$ mashctl endpoint create acme.demo.bob --public-key bob.pub --creds-out b.creds
[exit 2]
error: --creds-out goes with --make-key; --public-key writes a token.
fix: name the token file with --token-out:
  mashctl endpoint create acme.demo.bob --public-key b.pub --token-out b.token
usage: mashctl endpoint create <address> (--make-key | --public-key <file>)

$ mashctl issued check --servers 0
[exit 2]
error: --servers must be a whole number, 1 or more.
fix: give the number of servers:
  mashctl issued check --servers <number>
usage: mashctl issued check [--sys-creds <file>] [--servers N]

$ mashctl deprovision acme.demo.alice --drained-to 3
[exit 2]
error: --drained-to and --suspension go together.
fix: add the suspension id that endpoint suspend printed:
  mashctl deprovision acme.demo.alice --suspension <id> --drained-to 3
usage: mashctl deprovision <address> [--suspension ID --drained-to N]

$ mashctl deprovision acme.demo.alice --user U... --drained-to 3 --suspension x
[exit 2]
error: --user cannot be combined with --drained-to.
fix: leave out --user:
  mashctl deprovision acme.demo.alice --suspension x --drained-to 3
usage: mashctl deprovision <address> [--suspension ID --drained-to N]

$ mashctl provision acme.demo.alice
[exit 2]
error: --user is required.
fix: add the user's and the account's public keys:
  mashctl provision acme.demo.alice --user <user-key> --account <account-key>
usage: mashctl provision <address> --user KEY --account KEY

$ mashctl provision acme.demo.alice --user notakey --account A...
[exit 2]
error: --user is not a NATS user public key (it starts with U; the value
       is not shown).
fix: give the user's public key:
  mashctl provision acme.demo.alice --user <user-key> --account <account-key>
usage: mashctl provision <address> --user KEY --account KEY

$ mashctl limits nobody
[exit 2]
error: "nobody" is not an endpoint address.
fix: give the whole address, owner, group and name:
  mashctl limits acme.<group>.<name>
usage: mashctl limits [<address>...] | mashctl limits --show
```

What changed and why:

- P5, P6: the error is one line under 80 columns, no code in parentheses (it
  was `error (key_missing)`), and the usage fits 80 columns.
- P9: no full command list under an unknown command, as in 0.5.5.
- P4: the `fix:` command replaces the long advice that was inside the error
  line; it is complete and pasteable, with placeholders where mash cannot know.
- DRAFT10 (V5, V6, critic A.E): the owner is in the config, so a fix shows
  `acme.<group>.bob`, as the empty list does. `--owner` and `--group` are not
  flags (A4a), so the two cases that used them are one `unknown option`; the
  address is one dotted argument. A reissue with no key flag names
  `--public-key <file>` first, because `--make-key` moves a remote endpoint's
  identity to this machine. One placeholder form per role: `<address>`,
  `<file>`. A confirmation flag is an unknown option.
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
$ MASH_URL=nats://myserver:4222 mashctl endpoint list
[exit 1]
error: MASH_URL is nats://myserver:4222, which is not encrypted.
fix: use your configured server instead, by removing MASH_URL:
  unset MASH_URL

If your profile exports MASH_URL, remove it from the file that sets it, then
start a new shell. To use another address, set MASH_URL to a tls:// address.

Or, for a test server only: put MASH_INSECURE_PLAINTEXT=1 before the command:
  MASH_INSECURE_PLAINTEXT=1 mashctl endpoint list

With it, credentials and mail cross the connection unencrypted.

$ mashctl endpoint list
[exit 1]
error: refusing to connect to myserver without encryption (the address
       starts with nats://, and ~/.config/mash/config.json set it).
fix: change "url" in ~/.config/mash/config.json to a tls:// address. For a
     server with its own certificate authority, also set "tls_ca" there to
     the full path of its file.

Or, for a test server only: put MASH_INSECURE_PLAINTEXT=1 before the command:
  MASH_INSECURE_PLAINTEXT=1 mashctl endpoint list

With it, credentials and mail cross the connection unencrypted.

$ mashctl endpoint list
[exit 1]
error: mashctl is not connected to a server yet.
fix: connect mashctl to a server first; for how, run:
  mashctl adopt --help

$ mashctl endpoint create acme.demo.bob --make-key
[exit 4]
error: mashctl does not have this server's signing key, so it cannot make
       endpoints.
fix: connect mashctl to the server first; for how, run:
  mashctl adopt --help

$ mashctl endpoint list
[exit 4]
error: mashctl has no administrator credentials for this server.
fix: connect mashctl to the server; for how, run:
  mashctl adopt --help

$ mashctl endpoint list
[exit 4]
error: the administrator credentials file is missing: it cannot be opened.
fix: restore the file, or connect again; for how, run:
  mashctl adopt --help

$ mashctl endpoint list
[exit 1]
error: the administrator credentials file can be read by other users
       (mode 0644).
fix: make the file private to you:
  chmod 600 ~/.config/mash/server/mashadmin.creds

$ mashctl endpoint list
[exit 6]
error: cannot reach the server tls://myserver:4222.
fix: check that the server is running, then see where mashctl stands:
  mashctl status

$ mashctl endpoint create acme.demo.bob --make-key
[exit 6]
error: cannot reach the server tls://myserver:4222. Nothing was changed.
fix: start the server, then run the same command again.

$ mashctl audit
[exit 6]
error: cannot reach the server tls://myserver:4222.
fix: check that the server is running, then see where mashctl stands:
  mashctl status

$ mashctl inbox inspect acme.demo.bob
[exit 6]
error: cannot reach the server tls://myserver:4222.
fix: check that the server is running, then see where mashctl stands:
  mashctl status

$ mashctl issued check
[exit 6]
error: cannot reach the server tls://myserver:4222.
fix: check that the server is running, then see where mashctl stands:
  mashctl status

$ mashctl endpoint delete acme.demo.bob
[exit 6]
error: cannot reach the server tls://myserver:4222. Nothing was changed.
fix: start the server, then run the same command again.
```

Warnings (not refusals; the command goes on). They replace the two long
`warning:` lines of 0.5.5 and are printed only when they matter.

```text
$ MASH_INSECURE_PLAINTEXT=1 mashctl endpoint list
[exit 0]
warning: plain-text connection to myserver (MASH_INSECURE_PLAINTEXT is set):
         anyone on the network can read mail and obtain signatures

OWNER  GROUP  ENDPOINT  ADDRESS          CREATED (UTC)
acme   demo   alice     acme.demo.alice  2026-11-19 00:21
              bob       acme.demo.bob    2026-11-19 00:22

2 endpoints

$ MASH_OWNER=other mashctl endpoint list
warning: MASH_OWNER=other overrides the owner in your configuration (acme).
         mashctl is acting as owner other. Unset MASH_OWNER to use acme.

$ MASH_URL=tls://testserver:4222 MASH_ADMIN_CREDS=<file> mashctl endpoint list
warning: using the server from MASH_URL in this shell (the config file names
         another server)

(the list prints as usual)
```

What changed and why:

- P1: the preamble is gone. A server problem names the server in the error (a failure may name it).
- P5, P6: the TLS refusal (310 columns, with `insecure_plaintext` and `tls_ca`
  explained nowhere) is a short fix. The test-server override is the
  environment variable `MASH_INSECURE_PLAINTEXT=1` for one run, named in an
  `Or` block (DRAFT8, REQ-010); the config key `insecure_plaintext` is not
  offered. The text says that credentials and mail then cross the connection
  unencrypted.
- DRAFT9 (U6): the plain-URL refusal says where the address came from. From
  MASH_URL the fix is `unset MASH_URL` alone on its line (a test for the
  shell), then one sentence for a profile that exports it: remove it from the
  file that sets it. From the configuration file the fix names its `url`.
  Both end with the same `Or` block for a test server. [confirm: mashctl can
  tell the two sources apart; Part 1 Facts to confirm 20.]
- DRAFT9 (U3, REQ-010): while the override is set, every connecting command
  prints one `warning:` line first on stderr, before any result, in core's
  words wrapped to 80. It is shown once here, on `endpoint list`, and not in
  the other mockups. The conventions block states the rule.
- The wrapper text `mashctl: endpoint list: mashctl: mash:` (the same program
  named three times) is gone.
- "not initialised" now means "not connected to a server yet": the fix points
  to `mashctl adopt --help`, because `init --local` is removed.
- DRAFT10 (V3): every unreachable server is exit 6 (`audit` and `inbox inspect`
  were 3, `issued check` was 2, the usage code). No administrator credentials,
  a missing credentials file and a missing signing key are 4 (not authorized;
  they were 6 and 1). A credentials file other users can read is 1 (a
  refusal; it was 6). The TLS refusal is 1 (conventions block).
- DRAFT10 (V1, V5, critic A.E, F.3): the MASH_URL refusal says `MASH_URL is
  nats://...`, naming the variable that is wrong; the fix removes it. The
  session trap has one line, the warning of the conventions block, printed
  when MASH_URL (with its own administrator credentials) overrides the config
  file's server. The `127.0.0.1` in the second refusal is `myserver`.
- `status` (part 1) shows the warnings above on request in plain words.

### E3. endpoint create (refusals)

BEFORE (docs/mashctl.md table, catalog cases 20 to 34), marks P5 P6 P7:

```text
mashctl: endpoint create: error (group_missing): an endpoint needs a group; write --group dev or alice/dev/design
mashctl: endpoint create: refused (public_key_issued): the public key UASKC43D... was already issued to another address; each key is used at one address: make a new key for this endpoint with: mash key create --out alice2.seed
```

AFTER

```text
$ mashctl endpoint create acme.demo.bob --public-key bob.pub
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
  mashctl endpoint reissue acme.demo.bob --public-key <file>

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

# when: the token file already exists
error: ./acme.demo.bob.token already exists and is never overwritten.
fix: name another file with --token-out, or delete this one.

# when: a server did not answer the revocation check (exit 6)
error: mashctl could not check whether this key was revoked: a server did
       not answer. Nothing was changed.
fix: bring the server back, then run the same command again.

$ mashctl endpoint create acme.demo.bob --make-key
[exit 1]
# when: the endpoint already exists
error: the endpoint acme.demo.bob already exists.
fix: to give it a new key, reissue it with a key made where bob runs:
  mashctl endpoint reissue acme.demo.bob --public-key <file>

# when: the key kept from an unfinished run was revoked
error: the key waiting from an earlier, unfinished run was revoked.
fix: delete the key left by the unfinished run, then run the command again:
  rm ~/.config/mash/acme.demo.bob.creds.pending

# when: another key's credentials file is in the way
error: a credentials file for a different key is already at
       ~/.config/mash/acme.demo.bob.creds.
fix: move that file aside, then run the command again:
  mv ~/.config/mash/acme.demo.bob.creds ~/.config/mash/acme.demo.bob.old

# when: no config folder is set
error: mashctl has no folder to write bob's credentials to.
fix: in this shell, point mashctl at a folder you own, then run it again:
  export MASH_CONFIG_DIR=<folder>

# when: the config folder is writable by other users
error: mashctl cannot write to ~/.config/mash: other users can write there.
       Nothing was changed.
fix: make the folder private to you:
  chmod 700 ~/.config/mash
```

When the credentials file named by `--creds-out` cannot be created safely
(DRAFT8, T1, INV-003). Nothing is written, and the endpoint is not made:

```text
$ mashctl endpoint create acme.demo.alice --make-key --creds-out alice.creds
[exit 1]
# when: the file already exists and is a different file
error: alice.creds already exists and is a different file. Nothing was
       changed.
fix: choose a new path, or remove it yourself if it is yours:
  rm alice.creds

# when: the path is a link to another file
error: alice.creds is a link to another file, and mashctl does not write
       through a link. Nothing was changed.
fix: choose a path that is not a link:
  mashctl endpoint create acme.demo.alice --make-key --creds-out <new path>

# when: the folder belongs to someone else
error: the folder for alice.creds is not yours, so mashctl will not put a
       private file in it. Nothing was changed.
fix: choose a path in a folder you own:
  mashctl endpoint create acme.demo.alice --make-key --creds-out <new path>
```

Exit codes in this block: invalid name 1, naming rule 1, exists 1, deleted
address 1, unreadable key file 1, not a public key 1, key already used 1,
revoked 1, waiting key revoked 1, token file exists 1, credentials file
exists 1, no folder 1, folder not safe 1 (it was 6), could not check
revocation 6; for `--creds-out`: file exists 1, link 1, folder not yours 1. The
`error:` text does not repeat the exit code.

DRAFT10 (critic A.E): the cases are split by the command that causes them. The
first block is `--public-key`; the second is `--make-key`, which alone writes
a credentials file and can meet a file in its way. The `.pending` file is the
key `--make-key` keeps until the server answers; no other block shows it
[confirm: Facts to confirm 21]. The "already exists" fix names
`--public-key <file>` first.

Codes covered: `name_invalid`, `name_rule`, `address_taken`, `key_retired`,
`public_key_unreadable`, `public_key_invalid`, `public_key_issued`,
`key_revoked`, `staged_key_revoked`, `file_exists`, `creds_exists`,
`no_config_dir`, `write`, `revocation_unknown`, `no_account_key` (E2).

### E4. endpoint reissue (refusals)

```text
$ mashctl endpoint reissue acme.demo.zed --make-key
[exit 1]
error: there is no endpoint acme.demo.zed.
fix: see which endpoints exist:
  mashctl endpoint list

$ mashctl endpoint reissue acme.demo.bob --public-key bob.pub
[exit 1]
error: bob.pub is already bob's public key, so nothing would change.
fix: make a new key on bob's machine:
  mash key create --out bob2.key

$ mashctl endpoint reissue acme.demo.bob --public-key b2.pub --token-out t.token
[exit 1]
error: t.token already exists and is never overwritten.
fix: name another file with --token-out, or delete this one.

$ mashctl endpoint reissue acme.demo.bob --public-key bob2.pub
(the text of what will change, and the prompt, as in B2)
Type the address to reissue it: acme.demo.alice
[exit 3]
error: what you typed is not acme.demo.bob. Nothing was changed.
fix: run the command again and type the address you want to reissue:
  mashctl endpoint reissue acme.demo.bob --public-key bob2.pub

$ mashctl endpoint reissue acme.demo.bob --public-key bob2.pub   (no terminal)
[exit 4]
error: a reissue needs a person at a terminal.
fix: run the same command in a terminal and type the address when asked:
  mashctl endpoint reissue acme.demo.bob --public-key bob2.pub

Or, to see what it would change without changing anything:
  mashctl endpoint reissue acme.demo.bob --public-key bob2.pub --dry-run

$ mashctl endpoint reissue acme.demo.bob --make-key
[exit 1]
error: bob's credentials are not on this machine, so --make-key would move
       bob's identity here. Nothing was changed.
fix: give a key made on the machine where bob runs:
  mashctl endpoint reissue acme.demo.bob --public-key <file>

Or make that key first, on bob's machine:
  mash key create --out bob2.key

$ mashctl endpoint reissue acme.demo.alice --public-key bob2.pub
[exit 1]
error: this public key already belongs to another endpoint.
fix: make a new key on alice's machine:
  mash key create --out alice2.key

$ mashctl endpoint reissue acme.demo.bob --make-key
[exit 1]
error: the credentials file ~/.config/mash/acme.demo.bob.creds is not
       bob's current one. Nothing was changed.
fix: move that file aside, then run again:
  mv ~/.config/mash/acme.demo.bob.creds ~/.config/mash/acme.demo.bob.old

Or give a key made on bob's machine, starting there:
  mash key create --out bob2.key

$ mashctl endpoint reissue acme.demo.bob --make-key
[exit 1]
error: bob's credentials file cannot be read or understood. Nothing was
       changed.
fix: restore the file, or move it aside and run again.

$ mashctl endpoint reissue acme.demo.bob --make-key
[exit 1]
error: bob's old key has a token dated in the future, so revoking the key
       would not cover it. Nothing was changed.
fix: check this machine's clock, then run the command again.

$ mashctl endpoint reissue acme.demo.bob --make-key
[exit 1]
error: another reissue of acme.demo.bob is running (process 85044 on myserver).
fix: wait for it to finish, then run again. A killed run leaves nothing to
     clean up.

$ mashctl endpoint reissue acme.demo.bob --make-key
[exit 1]
error: an old lock folder blocks the reissue of acme.demo.bob:
       ~/.config/mash/acme.demo.bob.creds.lock
fix: if no reissue is running, delete that folder once, then run again:
  rm -r ~/.config/mash/acme.demo.bob.creds.lock

$ mashctl endpoint reissue acme.demo.bob --make-key
[exit 1]
error: another reissue changed bob's key while this one ran, so the file
       this run wrote was removed. The old key stays revoked.
fix: run the same command again.

$ mashctl endpoint reissue acme.demo.bob --make-key
[exit 6]
error: only 1 of 2 servers answered. Nothing was revoked.
fix: bring the missing server back, or pass --servers N if the count is
     wrong. Then run the same command again.

$ mashctl endpoint reissue acme.demo.bob --make-key
[exit 6]
error: the server did not answer, or answered with an error. Nothing was
       changed.
fix: see where mashctl stands, then run the command again:
  mashctl status
```

In a bulk run (`--all`, `--changed`) a refusal is shown in the RESULT column
(see B3) with the same words as the `error:` line above, and a skipped endpoint
that has no credentials file here is shown under "Skipped" (exit 0). The
unreadable file is exit 1 for one endpoint; in a bulk run it is a `not done` row and
the run exits 5 (partial).

Codes covered: `endpoint_missing`, `key_unchanged`, `file_exists`,
`public_key_issued`, `creds_mismatch`, `creds_unreadable`, `future_issued`,
`reissue_busy` (both forms), `superseded`, `servers`, `server`,
`no_local_creds`, plus (from E3) `key_revoked`, `staged_key_revoked`,
`key_retired`, `no_account_key`, `no_config_dir`, `write`,
`revocation_unknown`, `key_both`, `key_missing`, `bulk_needs_make_key`.

### E5. endpoint delete (refusals)

```text
$ mashctl endpoint delete acme.demo.zed
[exit 1]
error: there is no endpoint acme.demo.zed.
fix: see which endpoints exist:
  mashctl endpoint list

$ mashctl endpoint delete acme.demo.bob
[exit 6]
error: only 1 of 2 servers answered, so the key cannot be shown to be
       revoked. Nothing was changed.
fix: bring the missing server back and run the command again. If the
     count is wrong, pass --servers N.

$ mashctl endpoint delete acme.demo.bob
[exit 1]
error: bob's key has a token dated in the future, so revoking the key would
       not cover it. Nothing was changed.
fix: check this machine's clock, then run the command again.

$ mashctl endpoint delete acme.demo.bob
[exit 6]
error: the server could not confirm that bob's key was revoked. Nothing was
       removed.
fix: run the same command again; revoking twice is safe.

$ mashctl endpoint delete acme.demo.bob
[exit 1]
error: the record for acme.demo.bob belongs to a different key than the one
       mash issued. Nothing was changed.
fix: list what the server holds, then remove it by the key you mean:
  mashctl audit
  mashctl deprovision acme.demo.bob --user <public-key>

$ mashctl endpoint delete acme.demo.bob
[exit 1]
error: mash cannot tell which key it issued for bob, so it will not delete
       bob. Nothing was changed.
fix: revoke the key with the tools of whoever runs the server, then:
  mashctl endpoint delete acme.demo.bob --no-revoke
```

DRAFT10 (critic A.E, V3): "never created" and "removed only in part" were
two claims in one error; the error says only that no endpoint exists. A delete
left half done is finished with `--force` (B5, H7), not `deprovision`. The
"too many times" sentence is in plain words. Too few servers and an
unconfirmed revocation are exit 6 (they were 1).

Codes covered: `not_found`, too few servers, future issue time, revocation not
confirmed, record belongs to another key, history not complete; plus
`servers_invalid` and `address_invalid` (E1).

### E6. Other commands

```text
$ mashctl endpoint show acme.demo.zed
[exit 1]
error: there is no endpoint acme.demo.zed.
fix: see which endpoints exist:
  mashctl endpoint list

$ mashctl endpoint set acme.demo.zed --role x
[exit 1]
error: there is no endpoint acme.demo.zed.
fix: see which endpoints exist:
  mashctl endpoint list

$ mashctl endpoint suspend acme.demo.zed
[exit 1]
error: acme.demo.zed has no inbox, so there is nothing to suspend.
fix: see which endpoints exist:
  mashctl endpoint list

$ mashctl inbox inspect acme.demo.zed
[exit 1]
error: there is no endpoint acme.demo.zed.
fix: see which endpoints exist:
  mashctl endpoint list

$ mashctl audit --compare nope.json
[exit 1]
error: the file nope.json does not exist.
fix: save an earlier audit first:
  mashctl audit --json > audit-earlier.json

$ mashctl provision acme.demo.alice --user UD7CJ5RS... --account AD7B...
[exit 1]
error: acme.demo.alice is already registered.
fix: to change how it is described, use set:
  mashctl endpoint set acme.demo.alice --role <role>

$ mashctl deprovision acme.demo.zed
[exit 1]
error: there is no endpoint acme.demo.zed.
fix: see which endpoints exist:
  mashctl endpoint list

$ mashctl deprovision acme.demo.bob --suspension deadbeef --drained-to 0
[exit 1]
error: the inbox is not suspended with that id, or a message arrived after
       message 0. Nothing was revoked.
fix: read the current suspension:
  mashctl endpoint show acme.demo.bob

$ mashctl deprovision acme.demo.bob
[exit 1]
error: a credential for bob's key is dated after the revocation time, so a
       revocation would not cover it. Nothing was revoked.
fix: check this machine's clock, then run the command again.

$ mashctl deprovision acme.demo.bob
[exit 1]
error: mash cannot read its record of bob's key, so it cannot check it.
       Nothing was revoked.
fix: check the server's records, then run the command again:
  mashctl audit

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
Would <verb> <thing>:
  - <one step per line, plain words; what would be lost, when anything>
Nothing was changed.
```

Rules for the form: a dry-run connects when the real command would, so it
reports the real refusals; it prints `Would` sentences in the same order as the
real output; it prints no NEXT (a bulk reissue dry run prints one, B3); it
exits 0 when the real command would run. A command that asks for a typed
confirmation prints the same "will lose" lines as "Would ... It would lose:"
and asks nothing. The form names no server.

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
Not for a server mashctl adopted and can sign users for. Use this when the
NATS users are made by someone else, such as a hosted NATS service. mash does
not make a key or credentials here, and mashctl cannot revoke the user unless
it holds the server's operator key. Then use deprovision --no-revoke and ask
the server's owner to revoke the key.

$ mashctl deprovision --help
...
It revokes the user when mashctl holds the operator key. When it does not, use
--no-revoke and ask the server's owner to revoke the key.
```

What changed and why:

- Feedback 14 (P9): the old text said "revoke it with your operator tooling".
  The new note says who can revoke, and when.
- Nothing is promised for Synadia beyond the commands above; no unreleased
  integration is named.

---

## H. Help, usage and dry-run screens for adopt, init and endpoint

DRAFT8 (T5). The v0.5.5 screens for these commands ran 100 to 450 columns
wide. The screens below are the ones the earlier sections did not show: the
`--help` of each command, the usage errors, the dry runs and the repeat run.
Each follows the rules above: 80 columns, plain words, one fix with its
command on its own line.

### H1. AFTER: `mashctl adopt --help`

```text
$ mashctl adopt --help
[exit 0]
mashctl adopt - connect mashctl to a NATS server you already run

Usage:
  mashctl adopt --server <url> --owner NAME --operator-key <file>
                --account-key <file> --system-credentials <file>
                [--account NAME] [--ca <file>]
                [--make-admin | --admin-credentials <file>] [--dry-run]

adopt checks that you hold the authority over the server, then copies the
keys and credentials into ~/.config/mash and writes the settings. On the
server it changes nothing except, with --make-admin, a record of the new
admin credentials. Your operator gives you the address and the files.

Flags:
  --server <url>                the server address; it must start with tls://
  --owner NAME                  the owner name for your endpoints, such as acme
  --operator-key <file>         the operator key file
  --account-key <file>          the account key file
  --system-credentials <file>   the system credentials file
  --account NAME                the account mash uses on the server
  --ca <file>                   the certificate file of a server with its own
                                certificate authority. It asks you to type the
                                fingerprint; no flag skips it
  --make-admin                  make the admin credentials mashctl will use
  --admin-credentials <file>    use admin credentials you already have
  --dry-run                     run the checks and print what would be saved
  --detail                      print the key ids and the file of each check

Example:
  mashctl adopt --server tls://myserver:4222 --owner acme --ca ca.pem \
    --make-admin --operator-key operator.nk --account-key account.nk \
    --system-credentials system.creds
```

### H2. AFTER: `mashctl adopt` usage and file errors

```text
$ mashctl adopt
[exit 2]
error: adopt needs the server address, the owner and the three key files.
fix: see what each one is:
  mashctl adopt --help
usage: mashctl adopt --server <url> --owner NAME [options]

$ mashctl adopt --server tls://myserver:4222 --owner acme ...
[exit 1]
Checking the server as owner acme. Nothing is written until all the checks
pass.

  1 files     FAILED  operator.nk cannot be read.

error: operator.nk cannot be read.
fix: check the file name and that you can read it.

$ mashctl adopt ... --make-admin --admin-credentials admin.creds
[exit 2]
error: give --make-admin or --admin-credentials, not both.
fix: leave out one of them and run the command again.
usage: mashctl adopt --server <url> --owner NAME [options]
```

### H3. AFTER: `mashctl adopt` dry run and repeat run

```text
$ mashctl adopt --server tls://myserver:4222 --owner acme ... --dry-run
[exit 0]
Would adopt the server as owner acme:
  - check the operator key, account key and system credentials
  - make admin credentials and record them on the server
  - copy the admin credentials into mashctl's folder
  - copy the keys into mashctl's folder
  - write the settings
Nothing was changed.

$ mashctl adopt ... --admin-credentials admin.creds --dry-run
[exit 0]
Would adopt the server as owner acme:
  - check the operator key, account key and system credentials
  - copy admin.creds into mashctl's folder
  - copy the keys into mashctl's folder
  - write the settings
Nothing was changed.

$ mashctl adopt --server tls://myserver:4222 --owner acme ...
[exit 0]
(the checks print as in the first run)

Adopted the server as owner acme. Nothing new was copied.

  kept    ~/.config/mash/server/mashadmin.creds  (it connected)
  kept    ~/.config/mash/config.json             (already has these settings)

NEXT: on this machine, prepare the server for mash:
  mashctl init
```

### H4. AFTER: `mashctl init --help` and usage errors

```text
$ mashctl init --help
[exit 0]
mashctl init - prepare the server for mash (once, after adopt)

Usage:
  mashctl init

init makes room on the server for the list of endpoints, the record of every
token mash signs and the sessions that are running. It changes nothing that
is already there, so running it twice is safe.

Flags:
  --detail    print every step
  --json      print the result for a script

$ mashctl init now
[exit 2]
error: init takes no arguments.
fix: run it by itself:
  mashctl init
usage: mashctl init

$ mashctl init --port 20001
[exit 2]
error: unknown option --port.
fix: see the options:
  mashctl init --help
usage: mashctl init
```

### H5. AFTER: `--help` of endpoint list, show, delete and reissue

```text
$ mashctl endpoint list --help
[exit 0]
mashctl endpoint list - list the endpoints on the server

Usage:
  mashctl endpoint list [--wide] [--detail]

Flags:
  --wide      add the type, host and note columns
  --detail    print everything known about each endpoint

Example:
  mashctl endpoint list

$ mashctl endpoint show --help
[exit 0]
mashctl endpoint show - show one endpoint

Usage:
  mashctl endpoint show <address> [--detail]

Flags:
  --detail    add the keys and the server's record

Example:
  mashctl endpoint show acme.demo.bob

$ mashctl endpoint delete --help
[exit 0]
mashctl endpoint delete - delete an endpoint and its inbox for good

Usage:
  mashctl endpoint delete <address> [--dry-run] [--delete-creds] [--force]
                          [--no-revoke]

It says what will be lost (the unread mail, the address, the key) and asks
you to type the address. No flag skips it.

Flags:
  --dry-run        print what would be lost and change nothing
  --delete-creds   remove the credentials file here; the default renames it
  --force          finish a delete that was left half done
  --no-revoke      delete without stopping the key; use only when the server
                   owner stops it
  --detail         print everything that was done

Example:
  mashctl endpoint delete acme.demo.bob --dry-run

$ mashctl endpoint reissue --help
[exit 0]
mashctl endpoint reissue - give an endpoint a new key

Usage:
  mashctl endpoint reissue <address> --public-key <file>
  mashctl endpoint reissue <address> --make-key
  mashctl endpoint reissue (--all | --changed)

The old key stops working. Use --public-key when the new key was made on the
machine where the endpoint runs, and --make-key only when it runs on this
machine. It says what will change and asks you to type the address (for --all
and --changed, the count). No flag skips it.

Flags:
  --public-key <file> sign a new public key; writes a numbered token
  --make-key          make the new private key here and write the credentials
  --token-out <file>  write the token to <file>
  --all               reissue every endpoint whose credentials are here
  --changed           reissue only those that need it
  --dry-run           print what would change and change nothing
  --detail            print everything that was done

Example:
  mashctl endpoint reissue acme.demo.bob --public-key bob2.pub
```

### H6. AFTER: endpoint create without a key choice, and dry runs

```text
$ mashctl endpoint create
[exit 2]
error: no endpoint address given.
fix: name the endpoint to create:
  mashctl endpoint create acme.<group>.<name> --make-key
usage: mashctl endpoint create <address> (--make-key | --public-key <file>)

$ mashctl endpoint create acme.demo.bob
[exit 2]
error: an endpoint needs a key: give --make-key or --public-key <file>.
fix: make the key here:
  mashctl endpoint create acme.demo.bob --make-key
usage: mashctl endpoint create <address> (--make-key | --public-key <file>)

$ mashctl endpoint create acme.demo.bob --make-key --color
[exit 2]
error: unknown option --color.
fix: see the options:
  mashctl endpoint create --help
usage: mashctl endpoint create <address> (--make-key | --public-key <file>)

$ mashctl endpoint create acme.demo.bob acme.demo.alice --make-key
[exit 2]
error: give one endpoint address, not two.
fix: create them one at a time:
  mashctl endpoint create acme.demo.bob --make-key
usage: mashctl endpoint create <address> (--make-key | --public-key <file>)

$ mashctl endpoint create acme.demo.bob --make-key --dry-run
[exit 0]
Would create endpoint acme.demo.bob:
  - make a private key for bob here
  - sign bob's public key, which makes the token
  - write bob's credentials file here
Nothing was changed.

$ mashctl endpoint create acme.demo.bob --public-key bob.pub --dry-run
[exit 0]
Would create endpoint acme.demo.bob:
  - sign bob's public key, which makes the token
  - write the token to ./acme.demo.bob.token
  - print the CA file and its fingerprint to give bob's machine
Nothing was changed.
```

### H7. AFTER: show, delete and list, the remaining usage errors and cases

```text
$ mashctl endpoint show
[exit 2]
error: no endpoint address given.
fix: name the endpoint to show:
  mashctl endpoint show <address>
usage: mashctl endpoint show <address> [--detail]

$ mashctl endpoint show bob
[exit 2]
error: "bob" is not an endpoint address.
fix: give the whole address, owner, group and name:
  mashctl endpoint show acme.<group>.bob
usage: mashctl endpoint show <address> [--detail]

$ mashctl endpoint show acme.demo.bob
[exit 0]

Endpoint acme.demo.bob

created:      2026-11-19 00:22 UTC
type:         agent
token:        valid, not revoked
online:       no
unread mail:  3
credentials:  not on this machine

$ mashctl endpoint show acme.demo.dave
[exit 1]
error: the address acme.demo.dave was deleted, and a deleted address is
       never used again.
fix: see which endpoints exist:
  mashctl endpoint list

$ mashctl endpoint delete
[exit 2]
error: no endpoint address given.
fix: name the endpoint to delete:
  mashctl endpoint delete <address>
usage: mashctl endpoint delete <address> [--force] [--no-revoke] [--dry-run]

$ mashctl endpoint delete acme.demo.alice --delete-creds
(the text of what will be lost, and the prompt)
Type the address to delete it: acme.demo.alice
[exit 0]
Deleted acme.demo.alice. Its address cannot be used again.
  Checked: the server now refuses alice's old credentials.
  Its inbox is deleted, with 2 unread messages.
  Its credentials file here was removed.

Nothing can restore a deleted address. A new endpoint needs a new name.

NEXT: see what is left:
  mashctl endpoint list

$ mashctl endpoint delete acme.demo.hal
[exit 1]
error: the server has no record of acme.demo.hal, but its credentials file
       is here. Nothing was changed.
fix: remove what is left on this machine:
  mashctl endpoint delete acme.demo.hal --force

$ mashctl endpoint list now
[exit 2]
error: endpoint list takes no arguments.
fix: run it by itself:
  mashctl endpoint list
usage: mashctl endpoint list [--wide] [--detail]

$ mashctl endpoint reissue acme.demo.bob --make-key
[exit 1]
error: the credentials file here is not bob's current key, so it was not
       replaced. Nothing was changed.
fix: move that file aside, then run the command again:
  mv ~/.config/mash/acme.demo.bob.creds ~/.config/mash/acme.demo.bob.old
```

---

## Wide lines fixed

DRAFT8 (T5). The v0.5.5 catalog has 348 lines over 100 columns in the default
text output of `adopt`, `init` and `endpoint` (up to 456 columns; notes under
a case excluded). The table is built by a script over
`docs/plans/2026-11-19-cli-output-catalog-mashctl.md`: each row is one catalog
case, with the count of its lines over 100 columns, the widest, the catalog
line number of the first, and the section of this document that replaces it.
Every AFTER line in this document is 80 columns or fewer; a typed command a
person is told to run is the only exception, and those are kept short
(checked by script over the reviewer packet). `--json` output keeps its form.

| Catalog section and case | Lines over 100 | Widest | First line | Replaced by |
|---|---|---|---|---|
| adopt: help_adopt | 1 | 298 | 320 | section H1 |
| adopt: ad_noargs | 3 | 456 | 333 | section H2 |
| adopt: ad_missingfile | 3 | 198 | 340 | section H2 |
| adopt: ad_plain | 4 | 323 | 357 | section 1b (plain-text refusal) |
| adopt: ad_dry | 14 | 397 | 371 | section H3 |
| adopt: ad_dry_noadmin | 15 | 397 | 405 | section H3 |
| adopt: ad_both | 2 | 257 | 438 | section H2 |
| adopt: ad_real | 13 | 397 | 451 | section 1b |
| adopt: ad_again | 14 | 397 | 486 | section H3 |
| endpoint: help_endpoint | 4 | 324 | 980 | section A4 |
| endpoint: bare_endpoint_nosub | 1 | 120 | 1011 | section E1 |
| endpoint create: help_endpoint_create | 9 | 119 | 1034 | section A4a |
| endpoint create: bare_endpoint_create | 1 | 113 | 1061 | section H6 |
| endpoint create: ec_dry | 1 | 197 | 1077 | section H6 |
| endpoint create: ec_make | 1 | 197 | 1093 | section 2 (alice, same machine) |
| endpoint create: ec_make_json | 1 | 197 | 1132 | none: `--json` is unchanged |
| endpoint create: ec_pub | 2 | 197 | 1140 | section 2 (Step 2, bob) |
| endpoint create: ec_pub_dry | 2 | 224 | 1160 | section H6 |
| endpoint create: ec_tool | 1 | 197 | 1177 | section 2 (Step 2, bob) |
| endpoint create: ec_tool_dry | 1 | 197 | 1193 | section H6 |
| endpoint create: ecx_default_jwt | 4 | 197 | 1198 | section H6 |
| endpoint create: kc_ned | 1 | 134 | 1224 | section 2 (Step 1, bob) |
| endpoint create: ec_ned | 2 | 197 | 1233 | section 2 (Step 2, bob) |
| endpoint create: ecx_flags | 1 | 197 | 1257 | section H6 |
| endpoint create: ecx_oneword | 1 | 197 | 1273 | section E1 |
| endpoint create: ecx_exists | 1 | 169 | 1285 | section E3 |
| endpoint create: ecx_taken | 3 | 197 | 1293 | section E3 |
| endpoint create: ecx_two | 1 | 123 | 1310 | section E1 |
| endpoint create: ecx_nogroup | 1 | 113 | 1322 | section E1 |
| endpoint create: ecx_nokey | 1 | 187 | 1346 | section H6 |
| endpoint create: ecx_pubmissing | 1 | 146 | 1370 | section E3 |
| endpoint create: ecx_pubinvalid | 1 | 192 | 1382 | section E3 |
| endpoint create: ecx_flag | 9 | 119 | 1395 | section H6 |
| endpoint create: ecx_extra | 9 | 119 | 1422 | section H6 |
| endpoint create: ecx_disagree | 1 | 113 | 1448 | section E1 |
| endpoint create: ecx_label_bad | 9 | 119 | 1461 | section E1 |
| endpoint create: ecx_json_refusal | 2 | 169 | 1515 | none: `--json` is unchanged |
| endpoint create: ecx_fileexists | 2 | 159 | 1527 | section E3 |
| endpoint create: ecx_revoked | 3 | 197 | 1539 | section E3 |
| endpoint create: ecx_revoked2 | 3 | 197 | 1552 | section E3 |
| endpoint create: ec_retired | 2 | 197 | 1569 | section E3 |
| endpoint create: ec_retired_pub | 3 | 197 | 1578 | section E3 |
| endpoint delete: help_endpoint_delete | 1 | 115 | 1624 | section H5 |
| endpoint delete: bare_endpoint_delete | 1 | 139 | 1647 | section H7 |
| endpoint delete: ed_dry | 2 | 197 | 1661 | section B4 |
| endpoint delete: ed_dry_json | 1 | 197 | 1690 | none: `--json` is unchanged |
| endpoint delete: ed | 3 | 197 | 1702 | section B4 |
| endpoint delete: ed_json | 1 | 197 | 1735 | none: `--json` is unchanged |
| endpoint delete: ed_again | 2 | 197 | 1749 | section B5 |
| endpoint delete: ed_force | 2 | 197 | 1764 | section B5 |
| endpoint delete: ed_norevoke | 5 | 325 | 1777 | section B5 |
| endpoint delete: ed_delcreds | 2 | 197 | 1796 | section H7 |
| endpoint delete: ed_nrec_nocreds_refuse | 3 | 197 | 1811 | section H7 |
| endpoint delete: ed_force_dry | 1 | 197 | 1831 | section B5 |
| endpoint delete: ed_force_creds | 2 | 197 | 1845 | section B5 |
| endpoint delete: ed_bad | 1 | 132 | 1858 | section E1 |
| endpoint delete: ed_noarg | 1 | 139 | 1870 | section H7 |
| endpoint delete: ed_missing | 2 | 250 | 1882 | section E5 |
| endpoint delete: ed_json_missing | 3 | 250 | 1911 | none: `--json` is unchanged |
| endpoint list: help_endpoint_list | 1 | 120 | 1963 | section H5 |
| endpoint list: el_empty | 1 | 197 | 1982 | section 3 (endpoint list) |
| endpoint list: el_empty_json | 1 | 197 | 1998 | none: `--json` is unchanged |
| endpoint list: el_three | 2 | 197 | 2012 | section 3 (endpoint list) |
| endpoint list: el_three_json | 1 | 197 | 2068 | none: `--json` is unchanged |
| endpoint list: el_after | 3 | 197 | 2079 | section 3 (endpoint list) |
| endpoint list: el_extra | 1 | 127 | 2110 | section H7 |
| endpoint reissue: help_endpoint_reissue | 23 | 124 | 2142 | section H5 |
| endpoint reissue: er_nokey | 1 | 194 | 2204 | section E1 |
| endpoint reissue: er_unchanged | 3 | 197 | 2224 | section E4 |
| endpoint reissue: er_missing | 2 | 197 | 2241 | section E4 |
| endpoint reissue: er_bad | 1 | 158 | 2254 | section E1 |
| endpoint reissue: er_dry | 1 | 197 | 2270 | section B1 |
| endpoint reissue: er_dry_json | 1 | 197 | 2308 | none: `--json` is unchanged |
| endpoint reissue: er_make | 1 | 197 | 2325 | section B1 |
| endpoint reissue: er_make_json | 1 | 197 | 2366 | none: `--json` is unchanged |
| endpoint reissue: er_pub_dry | 2 | 197 | 2375 | section B2 |
| endpoint reissue: er_pub | 2 | 197 | 2391 | section B2 |
| endpoint reissue: er_pub_exists | 2 | 158 | 2409 | section E4 |
| endpoint reissue: er_json_unchanged | 4 | 227 | 2421 | none: `--json` is unchanged |
| endpoint reissue: er_credsmismatch | 2 | 348 | 2467 | section H7 |
| endpoint reissue: er_busy | 2 | 197 | 2480 | section E4 |
| endpoint reissue: er_all_dry | 1 | 197 | 2499 | section B3 |
| endpoint reissue: er_all_dry2 | 2 | 197 | 2514 | section B3 |
| endpoint reissue: er_all_json | 1 | 197 | 2571 | none: `--json` is unchanged |
| endpoint reissue: er_all_noyes | 1 | 197 | 2589 | section B3 |
| endpoint reissue: er_all_yes | 3 | 352 | 2608 | section B3 |
| endpoint reissue: er_busy_all | 4 | 352 | 2633 | section B3 |
| endpoint reissue: er_changed | 1 | 197 | 2654 | section B3 |
| endpoint reissue: er_changed_dry_json | 1 | 197 | 2680 | none: `--json` is unchanged |
| endpoint reissue: er_changed_yes | 1 | 197 | 2694 | section B3 |
| endpoint reissue: er_all_nomake | 24 | 175 | 2706 | section B3 |
| endpoint reissue: er_all_pub | 24 | 175 | 2758 | section B3 |
| endpoint show: help_endpoint_show | 1 | 116 | 2838 | section H5 |
| endpoint show: bare_endpoint_show | 1 | 135 | 2856 | section H7 |
| endpoint show: es_found | 1 | 197 | 2885 | section 3 (endpoint show) |
| endpoint show: es_after_meta | 1 | 197 | 2914 | section 3 (endpoint show) |
| endpoint show: es_tool | 1 | 197 | 2943 | section 3 (endpoint show) |
| endpoint show: es_json | 1 | 197 | 2988 | none: `--json` is unchanged |
| endpoint show: es_nocreds | 1 | 197 | 3017 | section H7 |
| endpoint show: es_nocreds_json | 1 | 197 | 3061 | none: `--json` is unchanged |
| endpoint show: es_notfound | 2 | 197 | 3073 | section E6 |
| endpoint show: es_json_missing | 2 | 197 | 3111 | none: `--json` is unchanged |
| endpoint show: es_deleted | 2 | 197 | 3127 | section H7 |
| endpoint show: es_bad | 1 | 130 | 3140 | section H7 |
| init: help_init | 2 | 251 | 3386 | section H4 |
| init: init_local_start | 5 | 248 | 3393 | section 1a, A5 |
| init: init_after | 1 | 197 | 3425 | section 1c |
| init: init_extra | 1 | 197 | 3437 | section H4 |
| init: init_flag | 1 | 197 | 3450 | section H4 |
| init --local: il_nostart | 4 | 268 | 3482 | section 1a, A5 |
| init --local: il_dir | 3 | 268 | 3507 | section 1a, A5 |
| init --local: il_portinuse | 4 | 155 | 3532 | section 1a, A5 |
| init --local: il_portinuse_again | 1 | 254 | 3554 | section 1a, A5 |
| init --local: init_again_local | 1 | 277 | 3567 | section 1a, A5 |
| init --local: il_portword | 1 | 185 | 3592 | section 1a, A5 |
| init --local: ilx_flag | 1 | 185 | 3605 | section 1a, A5 |

Sections 1a, 1b, 1c, 2, 3, A, B and E are earlier in this document; H is the
screens that had no replacement before DRAFT8 (the `--help` of adopt, init,
list, show, delete and reissue; usage errors; dry runs; a repeat adopt; four
refusals).

---

## Choices to confirm

Decisions the rules left open. Each is one line.

1. `endpoint set` flags are `--tag`, `--untag`, `--capability` and
   `--remove-capability`; reason: "label" and "cap" are banned in default output
   and "tag" is the word people use. If the old names stay, the two one-line
   explanations in C1 notes are needed in `--help`. Accepted for the review.
2. `--tag` replaces `--label` on `endpoint create` and `provision` too, for the
   same reason (part 1 must agree). Accepted for the review.
3. `suspend` and `resume` move under `endpoint`; reason: one place for
   everything about an endpoint. They keep exit codes 0, 1, 2. `suspended` is
   folded into `endpoint show` (DRAFT10, choice 41). Accepted for the review.
4. `mashctl list` is removed, not aliased; reason: hard cut (no old-state
   tolerance), and `endpoint list` shows everything it showed. Accepted for
   the review.
5. `provision` and `deprovision` keep their names and move to "Advanced";
   reason: a rename adds nothing and the list stays short.
6. Dry-runs print no NEXT, except the bulk reissue dry run, which ends with
   `NEXT:` and the command to run; reason: rule 11 says they end `Nothing was
   changed.`, the next screen is not a step of a guide, and the bulk dry run
   has no other way to say what to type (DRAFT7).
7. Bulk reissue (`--all`, `--changed`) no longer needs `--make-key`; reason:
   `--make-key` was the only form it accepted, so the flag carried no choice.
8. The partial-reissue `fix:` names a single-endpoint reissue, not `--changed`;
   reason: after the file is moved aside, `--changed` skips that endpoint.
9. `permissions` default prints words; `--json` prints what it printed before;
   `--nsc` is unchanged; reason: rule 15 (the default is for a person).
10. `mashctl list`, `label`, `unlabel`, `set-meta`, `suspend`, `resume` get
    removed-command messages (exit 2), not silent aliases; reason: same as 4.
11. `audit` and `issued check` print the verdict first (`OK`, `PROBLEM`,
    `INCOMPLETE`); reason: the exit code alone is not visible to a person, and a
    person reads the first line first. A `--no-revoke` delete or deprovision
    ends its first line with `KEY NOT REVOKED` for the same reason.
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
    path; the actual folder is the one mash resolved (`MASH_CONFIG_DIR`, then
    `XDG_CONFIG_HOME/mash`, then `~/.config/mash`).
16. The administrator key is listed in `issued list` with `-` for owner and
    group; reason: it is a key mash issued, and hiding it would hide a key that
    can sign in.
17. A refused plain URL exits 1 in every command (decided, R2; written once
    in the conventions block of Part 1). The `error:` text names a refusal,
    not a usage mistake.
18. `mashctl status` is the new command that part 1 defines (the server mashctl
    uses and whether it works, in plain words, replacing the old preamble). Part
    2 only points to it, in the fix for a server that cannot be reached.
19. In a grouped table the name column is headed `ENDPOINT` (`bob`) and the
    dotted address, where shown, `ADDRESS` (`acme.demo.bob`) (DRAFT10; it was
    `PATH`). Part 1's endpoint list uses the same headers. No table here gained
    an `ADDRESS` column: each already shows owner and group, so the name alone
    identifies the row. [confirm: contract REQ-005 still says `PATH`.]
20. A usage error's `fix:` command uses the person's own values when they typed
    them and `<owner>.<group>.<name>`-style placeholders when they did not.
21. A fix that needs a file removed or moved prints the exact `rm`, `rmdir`
    or `mv` command on its own line, with the full path. [confirm: that a person
    should be handed these commands; the alternative is a sentence only]
22. `Removed`/`Registered`/`Reissued`/`Deleted`/`Suspended` are kept as the verbs
    of those actions; `Created` is used for new keys and tokens made inside
    them.
23. The bulk reissue dry run ends with a labelled `NEXT:` and the command on
    its own line (DRAFT7, `--yes` removed in DRAFT10): `mashctl endpoint
    reissue --all`, which then asks for the count.
24. The plain-URL refusal (E2) shows the override in one form only (DRAFT8,
    REQ-010): `MASH_INSECURE_PLAINTEXT=1` before the command, for that run.
    The config key `insecure_plaintext` is removed. The text says that
    credentials and mail then cross the connection unencrypted.
25. Removing `mashctl init --local` (A5) reverses the local TLS quick start
    that ADR 0146 and 0.5.5 shipped. The contract that ratifies this document
    carries the ADR (mash order 17); until then this is a proposal.
26. No result line names the server or says where it came from (DRAFT10, Part
    1 choice 27). The one warning for a MASH_URL that overrides the config
    file is in the conventions block. `mashctl status` shows the server and the
    config path.
27. `--creds-out FILE` (DRAFT8: a new private file, never an existing one) belongs to `endpoint create --make-key` only; with
    `--public-key` it is a usage error (E1). `endpoint reissue --make-key`
    still writes to the config folder; give it `--creds-out` too if the
    same one-file copy is wanted after a reissue.
28. The dotted address is the only form printed or typed (R3): every command
    and every `--help` example takes `acme.demo.bob`. The slash form may
    still work but is not shown. Usage lines read `<address>` and `<file>`
    (DRAFT10: one placeholder form per role).
29. A delete that finishes ends with `NEXT: see what is left:` and `mashctl
    endpoint list`, after one sentence that a deleted address cannot be
    restored and a new endpoint needs a new name (DRAFT10, V5; Part 1, choice
    44). The result line is `Deleted acme.demo.bob. Its address cannot be used
    again.`; it names no server.
30. `endpoint reissue` keeps its name (decided).
31. Removing `init --local` names the two files that replace
    `docs/guides/nats-local.md`: `docs/skills/mash-nats-local/SKILL.md` and
    `scripts/nats-local-setup.sh`.
32. Labels (DRAFT7): every error line starts `error:`; a hint is `fix:` after
    a failure, `NEXT:` after a success, `Or` for a second choice, `See` for a
    document. A step that follows a fix is part of the fix sentence ("..., then
    run the command again:"), not a bare line after the command. A partial
    bulk result (exit 5) prints `fix:`, not `NEXT:`.
33. The exit codes, the stdout and stderr split, the colour rule, `--json`,
    resolved paths and `<version>` are written once, in the conventions block
    at the top of Part 1, and are not repeated in this part (DRAFT7).
34. No command has a flag that skips a confirmation (DRAFT10, V2). `endpoint
    delete`, every single `endpoint reissue`, `deprovision` and `credential
    install` over credentials for the same address ask for the address;
    `endpoint reissue --all` and `--changed` ask for the count. DRAFT9 kept
    `--yes` on the bulk commands because a reissue is neither a trust nor an
    exposure change; the contract's INV-004 now covers destruction, and every
    reissue revokes a key. The audit table is Part 1, choice 35.
35. The plain-text warning is one `warning:` line printed first on stderr by
    every connecting command (REQ-010). It is shown once in E2; per-command
    mockups do not repeat it.
36. The `init --local` message names two URLs on the project's repository
    (A3, A5), each alone on its line, because an installed mashctl cannot see
    repository paths. The files ship from another change (Part 1, choice 38).
37. The ADR points for MASH_ENDPOINT (ADR 0012) and `init --local` with its
    exit code (ADR 0146) are proposals here; the ratifying contract carries
    the ADRs (Part 1, choice 37).
38. Exit codes (DRAFT10, V3): one table for every command, the CLI standard's
    rule 14 plus 5 and 6 from the contract (conventions block). Choices 39 to
    48 are one line per command whose code changed.
39. `mashctl audit`: server unreachable 3 to 6. `audit --compare` with a file
    that does not exist: 3 to 1 (a refusal).
40. `mashctl inbox inspect`: server unreachable 3 to 6.
41. `mashctl issued check`: server unreachable 2 to 6; an incomplete check
    (`INCOMPLETE`) 2 to 6. `mashctl endpoint suspended` is removed because it
    answered a question with exit 3 ("not suspended"), and 3 is "cancelled";
    `endpoint show` prints the status instead.
42. mashctl with no administrator credentials, a missing credentials file, or
    no signing key: 6 or 1 to 4 (not authorized). A credentials file other
    users can read: 6 to 1 (a refusal).
43. `mashctl endpoint delete`: "only 1 of 2 servers answered" and "the server
    could not confirm the revocation": 1 to 6. A wrong typed address: 3 (new).
    No terminal: 4 (new).
44. `mashctl endpoint reissue` (every form): a wrong typed address or count 3,
    no terminal 4 (new). "Another reissue changed bob's key": 6 to 1. An
    unreadable credentials file in a bulk run is a `not done` row, exit 5.
45. `mashctl endpoint create`: a config folder other users can write to: 6 to
    1. `--make-key` with a MASH_URL that names another server: 1 (new).
46. `mashctl deprovision`: the typed address, 3 and 4, as for delete (new).
47. `mash credential install` over credentials for the same address: wrong
    address 3, no terminal 4 (new). `mash status` after a delete or a
    reissue: the server refuses the credentials, exit 4 [confirm with the
    `mash` targets].
48. `mashctl adopt` (Part 1): a wrong fingerprint 1 to 3; no terminal 1 to 4.
    `mashctl status` NOT REACHABLE: 1 to 6.
49. Rule 15 of the CLI standard says a missing terminal fails with exit 2
    naming the flag that answers the prompt. There is no such flag here
    (INV-004), so the code is 4 (not authorized). Alternative: 2, with a fix
    that names a terminal. Reasoning: 2 means a typo; a script that runs
    `endpoint delete` without a terminal did not mistype anything.
50. Typed confirmation on `endpoint reissue --make-key` for an endpoint whose
    credentials are here is kept (DRAFT10): the person types the address even
    though the key is made on this machine, because the running session is
    signed out and the old key is revoked.
51. The reissue token name is numbered (`acme.demo.bob.2.token`), not dated;
    alternative: `--token-out` required. Part 1, choice 40.
52. The packet lints (`make_packet.py`): V1 fails any success block with
    `tls://` or a config path, except typed commands, the file lines of
    `credential install`, `endpoint create`, `adopt`, reissue and delete, the
    adopt trust rows and the CA path to copy; V3 fails an `[exit N]` outside
    the table or a condition with the wrong code; V2 fails `--yes`; V6 fails a
    `PATH` column and any server other than `tls://myserver:4222` (and
    `tls://testserver:4222`, the second server of the MASH_URL examples).

## Facts to confirm

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
15. [confirm: mashctl's configuration file is `config.json` in the resolved
    config folder (`MASH_CONFIG_DIR`, then `XDG_CONFIG_HOME/mash`, then
    `~/.config/mash`), and holds `url`, `tls_ca`, `owner`, `limits` and may
    hold `endpoint` (E2, D5); part 1 shows the same path.]
16. [confirm: `mashctl adopt --help` is the pointer to print when mashctl has no
    server (a full `adopt` command needs the operator's files, which mash cannot
    name).]
17. [confirm: `mash who --detail` shows tags (C1 help text).]
18. [confirm: `rm -r` of the lock folder named in E4 is safe when no reissue is
    running.]
19. [core confirmed: `mash credential install --creds FILE`, which A4a and
    part 1 name as the way to install the file made by `--creds-out`.]
20. [confirm: mashctl can tell a server chosen by MASH_URL from one read from
    the config file, for the warning and for the source of a plain-URL
    refusal.]
21. [confirm: the `.pending` file, `acme.demo.bob.creds.pending`, is the key
    `--make-key` keeps until the server answers (E3). No other block shows it;
    the critic found it hidden state. Say what it is where it is first met,
    or make it a name a person recognises.]
22. [confirm: `mash status` after the key was refused prints a verdict and
    exits 4 (B4); the exact lines belong to the `mash` targets.]
23. [confirm: `Checked: the server now refuses the old key` is something
    mashctl can check after a revoke: it asks every server to confirm the
    revocation; it does not connect with the old credentials.]
24. [confirm: `endpoint show` can print `online` and `unread mail`; `audit`
    already has both columns.]
25. [confirm: what `--capability` grants. If it is a note, `show` and `set
    --help` should say so; if it grants something, `permissions` must show it
    (critic A.show).]
26. [confirm: `endpoint reissue --public-key` writes a numbered token and
    keeps the earlier one; the number comes from the files in the folder.]
27. [confirm: `deprovision` asks for the typed address, as delete does (D8);
    it deletes an inbox and revokes a key.]

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

## Changes in DRAFT3

1. Config paths. A rule at the top of Part 1 and Part 2 says every path in a
   message is the one mash resolved (MASH_CONFIG_DIR, then XDG_CONFIG_HOME/mash,
   then ~/.config/mash) and mockups show the default. Statements that the
   config is only under ~/.config/mash or MASH_CONFIG_DIR now list all three
   places (Part 1 conventions, Part 2 section E intro, choice 15, fact 15).
2. Repeat delete. The claim that the key was revoked when the endpoint was
   deleted is removed; the output says only that the endpoint is already
   deleted and mash holds no record or key for it. "What changed and why"
   says why.
3. Session name. config.json holds the owner only; the name comes from
   MASH_NAME at launch (ADR 0012) and the group from MASH_GROUP at launch
   (docs/install.md). Step 2 of the walkthrough now sets MASH_GROUP and
   MASH_NAME in the starting terminal, and Step 3 uses the start command from
   docs/install.md. The claim that config.json can hold the name or group is
   removed everywhere. [Replaced in DRAFT7: one setting, MASH_ENDPOINT.]
4. Removing `mashctl init --local`. Section 1a of Part 1, choice 22 and
   choice 25 of Part 2 say that removal reverses the local TLS quick start
   that ADR 0146 and 0.5.5 shipped, that the contract that ratifies this
   document carries the ADR (mash order 17), and that this document is a
   proposal until then. No ADR file is added.
5. Plain-URL refusal. The refusals in Part 1 adopt and Part 2 E2 name the
   override `insecure_plaintext` (`MASH_INSECURE_PLAINTEXT=1`) and tell a
   person with a throwaway test server what to do, with the command on its own
   line. "What changed and why" is updated; choices 23 (Part 1) and 24
   (Part 2) record that ADR 0146 requires the refusal to show it.
   [Replaced in DRAFT8: the override is the environment variable alone, for
   one run; the config key is removed.]

## Changes in DRAFT4

1. Certificate path in `mashctl adopt` (Part 1, 1b). The typed command uses
   `MASH_TLS_CA=<path to ca.pem>`, a full path, in place of `ca.pem`. adopt
   saves the value as typed today, and a relative `tls_ca` is read under the
   config folder, so the next `mashctl init` could fail TLS verification. The
   target requires adopt to save the CA's absolute path; the output says so,
   "What changed and why" says why, and choice 24 and a fact record it. The
   other files adopt takes (operator key, account key, system credentials) are
   copied into the config folder, and the admin credentials path is saved as
   an absolute path, so no other relative path is saved. The refusal in E2 now
   says to set `tls_ca` to the full path of the file.
2. Start command (Part 1, section 4). `claude --channels plugin:mash@mash` is
   replaced by `claude --dangerously-load-development-channels
   plugin:mash@mash`, the solo form in docs/install.md section 3, with a
   plugin install step (section 2) before it and a note that Claude Code asks
   for a confirmation at each launch. "What changed and why", choice 25 and
   the fact list are updated.
3. Drained deprovision (Part 2, D8). "none was lost" is removed. The output
   states what the guard proves (suspended with that id, no message after
   message N) and that reading is not checked. The description and "What
   changed and why" say what a person checks before removing unread mail. The
   mismatch refusal in E no longer says "the suspension you drained".

The fourth finding (an ADR for removing `init --local`) is answered in the pull
request; nothing changed for it.

## Changes in DRAFT5

1. Same-machine path in one command (Part 1, section 2; Part 2, A4a, E1).
   `mashctl endpoint create <owner>/<group>/<name> --make-key --creds-out
   <path>` makes the private key, signs the endpoint and writes the
   credentials file at <path> (mode 0600) in one command. Its output names
   each file it wrote, one line each: `Wrote <file>`, with `(private; only you
   can read it)` on the secret one. Without `--creds-out` the credentials go to
   the config folder. A person who runs the agent on another machine copies
   one file. Alice's short and long forms use the new lines; a short form for
   `--creds-out` is added. `mashctl endpoint create --help` is added (A4a,
   with the flags table); `--creds-out` without `--make-key` is a usage error
   (E1). Choices 26 and 28 record it.
2. One story for both paths. `mash credential install --key --token --server
   --ca` (core's command) writes the credentials, the certificate file and
   `config.json`, one `Wrote` line each; the same-machine command writes only
   the credentials because the other two already exist where mashctl runs.
   Both end with the same files in the same folder. Where the wording or file
   layout depends on core, the text says `[match mash targets]` and the facts
   list it.
3. The wrong-server trap (all commands that change something). The result or
   confirmation line says which server and why, in the same line: `(from
   ~/.config/mash/config.json)` or `(set by MASH_URL in this shell)`, and
   `(from --server)` for adopt. Added to create, reissue (one, `--all`),
   delete, set, suspend, resume, limits, provision, deprovision, init and
   adopt, and to every `Would ...` line, with the line wrapped at 80 columns.
   A rule is added to both Parts' conventions, a What-changed bullet on create,
   and choices 27 (Part 1) and 26 (Part 2).
4. NEXT and fix: sentences. Four NEXT lines had a parenthesis in the sentence
   (`(copy bob.pub there first)`, `(copy acme.demo.bob.token there first)`):
   the copy is now part of the sentence, which ends in a colon, and the
   command stands alone. Two `fix:` lines with `(See docs/...)` and three
   `fix:` blocks that ran several sentences before a command are split: the
   fix is one sentence, and a throwaway-test note is a separate sentence
   before its command. The install command in the Step 2 NEXT carries
   `--server` and `--ca` with placeholders so it fits 78 columns.

## Changes in DRAFT6

Maintainer rulings, applied as decided (not reopened).

1. R1, `NEXT:` form. Kept: capitals, coloured on a terminal, after a blank
   line, the command alone on the next line.
2. R2, plain-URL refusal exits 1, not 2 (Part 1 adopt; Part 2 E intro, E2
   example and notes; Part 1 choice 18; Part 2 choice 17; the R-exit rule in
   `rules.md`). REQ-014 is being updated to 1; the implemented v0.5.5
   behaviour (exit 2) changes.
3. R3, dotted address everywhere. Every typed command, `NEXT:`, `fix:`,
   `--help` line and example in an AFTER block uses `acme.demo.bob`, or
   `<owner>.<group>.bob` where the owner and group are not known (about 120
   lines, Parts 1 and 2). Usage lines read `OWNER.GROUP.NAME`. The setup block
   explains the dotted address (owner.group.name); the Step 2 and alice text
   no longer say "you typed it as ...". The "slash form accepted as input"
   wording is gone from the setup block, the Part 2 conventions, the
   `mashctl --help` footer and Part 1 choice 2. `show --detail` no longer has a
   `slash form` row. BEFORE blocks keep v0.5.5 text as it was.
4. R4, no MASH_OWNER in a printed command. The check line after
   `credential install` and after alice's create is `MASH_GROUP=demo
   MASH_NAME=bob mash status` (alice likewise). The owner is in `config.json`.
   [Replaced in DRAFT7: `MASH_ENDPOINT=acme.demo.bob mash status`.]
   The one warning that names MASH_OWNER (E2) describes a variable the person
   set; it is not a fix command.
5. R5, delete. Every delete output (single, `--detail`, repeat, repeat with a
   credentials file, `--force`, `--no-revoke`) starts with `Deleted
   acme.demo.bob. Its address cannot be used again.` (or the matching
   "already deleted" and "Finished deleting" form) and ends with `NEXT:
   create an endpoint with a new name:` and `mashctl endpoint create
   acme.demo.<new-name> --make-key --creds-out <path>`. The dry runs say "it
   cannot be used again" and print no NEXT. The old NEXT about stopping the
   session and deleting copies of the credentials is a plain line above the
   new NEXT; the `--no-revoke` key moved into the warning. The server and its
   source are on the first indented line (Part 2 choice 29).
6. R6, Claude Code launch. Section 4 of Part 1 is one NEXT with two lines, in
   this order: `export MASH_GROUP=demo MASH_NAME=bob`, then `claude
   --dangerously-load-development-channels plugin:mash@mash`. The plugin
   install is Step 2.
7. R7 and R8. `reissue` keeps its name. Flag renames, `mashctl list` removed
   and suspend and resume under `endpoint` are marked "accepted for the
   review" in Part 1 choice 3 and Part 2 choices 1 to 4.
8. R9. The `init --local` and `--start` message (Part 1 section 1a; Part 2 A3
   footer and A5) names `docs/skills/mash-nats-local/SKILL.md` and
   `scripts/nats-local-setup.sh`, and no longer names
   `docs/guides/nats-local.md`. The two facts that asked for the names are
   dropped.
9. R10. `mash credential install` prints core's block unchanged: `Installed
   credentials for acme.demo.bob.`, the line to keep `bob.key`, and three file
   lines (`wrote`, `wrote`, `copied`) with aligned columns; a second example
   shows `kept` for `config.json`. `mashctl endpoint create --make-key` and
   `--creds-out` print `wrote` for the credentials and `kept` for
   `config.json` and the certificate file. The `--creds-out` output carries the
   caveat that copying the file moves a private key across machines, and that
   the three-step path keeps the key on the agent's machine. `mash credential
   install --creds FILE` is accepted (the "not in core's design" marker is
   gone); flags of `credential install` are listed in Part 1 section 2.

## Changes in DRAFT7

Maintainer rulings, applied as decided (not reopened). The change logs of
DRAFT3 and DRAFT6 above record those drafts and keep their old wording.

1. S1, one setting for the endpoint. `MASH_ENDPOINT=acme.demo.bob` (config key
   `endpoint`) names the endpoint by its full address; mash uses it
   automatically when exactly one endpoint is installed on the machine.
   MASH_GROUP and MASH_NAME are removed from every printed command: the check
   after install and after alice's create (`MASH_ENDPOINT=acme.demo.bob mash
   status`, six places), the launch (`export MASH_ENDPOINT=acme.demo.bob`,
   then the claude command, in one `NEXT: in the terminal the session starts
   from, run these two lines in order:`), the Step 3 text of section 4, the
   facts and choices 30, 31, 32 and 33 of Part 1. The text that says
   `MASH_ENDPOINT` can be left out with one endpoint installed is written once
   (Step 3, long form). The R-true-fix text names the config file first, with
   its resolved path, and the export as the one-shell test. Statements that
   config.json holds the owner only now say it holds the owner and may hold
   the endpoint. The new refusal is `error: no endpoint is set.` with a
   labelled fix (section 4). Choice 32 records that this reverses the earlier
   ruling that the session name comes from MASH_NAME (ADR 0012) and that the
   contract must carry the decision.
2. S2, errors. Audit of every AFTER block: `mashctl status` prints `error:`
   and `fix:` after a problem verdict (it printed `fix:` alone); the adopt and
   E2 plain-text overrides start `Or`; the `--creds-out` caveat is a
   `warning:`; the partial bulk reissue prints `warning:`, `error:` and
   `fix:` in place of `NEXT:`; the bulk reissue dry run ends with `NEXT:` and
   `Or` in place of two bare sentences; `limits --show` ends with `NEXT:`;
   `permissions` ends with `See`; the delete advice to stop the session and
   delete copies is now a statement of fact; the `--servers` hint of `issued
   check` is a statement of fact; 8 fixes in E3 to E6 that ran a bare "Then
   run ..." line after the command now put the step in the fix sentence or in
   an `Or` block. No refusal asks for MASH_GROUP or MASH_NAME.
3. S3, conventions block. A block at the top of Part 1 states once: the exit
   codes (0, 1, 2, 5, 6; the plain-URL refusal is 1; `suspended` keeps 0, 3,
   1; `audit` and `inbox inspect` keep 3; `issued check` keeps 2), stdout and
   stderr, the labels, the colour rule, `--json` and resolved paths. The
   repeats are deleted: Part 1 colour bullet, resolved-path bullet and adopt's
   exit-code sentence; Part 2 colour bullet, path bullet, `--json` bullet and
   the exit-code sentences of the E intro, E2 notes and choice 17; Part 1
   choice 18.
4. S4, no version numbers in a mockup. `mashctl status --detail` prints
   `version:   mashctl <version>` (it printed `0.5.6`). The AFTER blocks and
   the reviewer packet contain no `0.5.5` or `0.5.6`; BEFORE blocks keep
   v0.5.5 text. The packet header no longer names a version.

## Changes in DRAFT8

Maintainer rulings and security requirements (contract rev 2: INV-003,
REQ-002, REQ-003, REQ-007, REQ-010), applied as decided.

1. T1, `--creds-out`. It creates a new regular file in a folder the person
   owns, without following links, mode 600 from creation, completed
   atomically. Part 2 section E3 shows three refusals (a different file in
   the way, a path that is a link, a folder the person does not own), each
   with a `fix:` and its command alone on its line. The credentials line
   reads `wrote   <path>  (new file, mode 600)`; the `endpoint create --help`
   flag text says the same (A4a).
2. T2, adopt copies the admin credentials into mashctl's own folder (mode
   600). The adopt block has a `copied` line, the closing text says "you can
   remove <export folder> now" with the removal as an `Or` hint, and the
   what-changed and choices say so.
3. T3, the plain-text override is the environment variable
   `MASH_INSECURE_PLAINTEXT=1` for one run. The config key `insecure_plaintext`
   is removed from every mockup, note and choice (the DRAFT3 and DRAFT6 logs
   keep their old wording). The adopt and E2 refusals show one command line
   with the variable in front, then say that credentials and mail cross the
   connection unencrypted. The conventions block never named the key.
4. T4, mashctl is a downloaded program from the release page. The setup block
   has one line for it, section 1 says it, and no AFTER block names `go
   install`. The wording is a fact to confirm.
5. T5, no output line over 80 columns. A script over the reviewer packet
   finds no AFTER line over 80 columns outside typed commands. The new
   section "Wide lines fixed" lists the catalog's lines over 100 columns for
   adopt, init and endpoint, case by case, with the section that replaces
   each. Section H adds the screens that had no replacement: adopt, init and
   endpoint `--help`, usage errors, dry runs, a repeat adopt, a repeat of the
   show, delete and reissue refusals; A4a lists the create flags that were
   missing.

## Changes in DRAFT9

Contract rev 3 (INV-004, REQ-003, REQ-010) and two review rounds, applied as
decided.

1. U1, the hand-off. `endpoint create --public-key` prints a block of what
   bob's machine needs (token file, server, CA file, fingerprint), one
   sentence on why the fingerprint exists, and a NEXT with `mash credential
   install ... --ca-fingerprint`. Step 2 (long and short), the step 3 typed
   commands (three), the `--creds-out` output, the A4a help, the H6 dry run
   and the choices are updated. Install prints `Checked ca.pem against the
   fingerprint: it matches.`
2. U2, INV-004. Adopt with a CA file asks the person to type the fingerprint
   and has no flag for it; a refusal without a terminal and a wrong
   fingerprint are shown. The conventions block states the rule. Choice 35
   audits every command that offers `--yes` or changes trust: only
   `endpoint reissue --all` and `--changed` offer `--yes`, and they stay,
   because a reissue is not a trust or exposure change.
3. U3, the plain-text warning. The conventions block shows the line; E2
   shows it once on `endpoint list`. No other mockup has it.
4. U4, the `init --local` message gives two URLs on the project's repository
   (A3, A5 and the Part 1 block). The files ship from another change.
5. U5, the alice example. A server that differs from `config.json` needs
   `--replace` and the typed fingerprint, saves the credentials, `config.json`
   and `ca.pem`, prints what it saved, and only then says `Created endpoint`.
6. U6, the plain-URL refusal names its source. From MASH_URL: `unset
   MASH_URL`, and a sentence for a profile. From the configuration file: its
   `url`. From `--server` (adopt): `--server`.
7. U7, ADR points. Choice 37 says the MASH_ENDPOINT and `init --local` ADRs
   are proposals here and the ratifying contract carries them.

## Changes in DRAFT10

The critic's round 2, a real two-machine walk of v0.5.5 in a container, and the
six directions given for this draft (V1 to V6), applied as decided. Line numbers refer to
this file after the change.

1. V1, no server or config path in a success result. The 36 result lines that
   read `... on tls://... (from ~/.config/mash/config.json)`, the `Server:`
   body line of delete, `(from --server)` of adopt and `(set by MASH_URL ...)`
   are gone; the server is in `status`, `--detail` and failures. The one
   warning for a MASH_URL that overrides the config file is in the conventions
   block; it replaces the source phrase. A lint in `make_packet.py` fails a
   success block that holds `tls://` or a config path, except the file lines
   of the credential commands, the adopt trust rows, the CA path to copy and
   typed commands. Dry-run lines name no path (they said "replace
   ~/.config/mash/...").
2. V2, typed confirmation for destruction. `endpoint delete`, every single
   `endpoint reissue`, `deprovision` and `credential install` over credentials
   for the same address print what will be lost first and ask the person to
   type the address; a bulk reissue asks for the count. `--yes` is removed.
   The result carries `Checked: the server now refuses ...`; delete adds the
   one command that proves it from bob's machine and what it prints. No
   terminal exits 4, a wrong answer exits 3. Cited: INV-004 and CLI standard
   rule 15 (Part 1, choice 35).
3. V3, one exit-code table in the conventions block (CLI standard rule 14 plus
   the contract's 5 and 6), with one Choices line per command whose code changed
   (Part 2, choices 38 to 52). `endpoint suspended` is removed. A lint fails an
   `[exit N]` outside the table and an error with the wrong code.
4. V4, real values and the two-machine flow. The setup block is two machines
   (myserver, bobs-laptop). The hand-off names the files to copy and their
   paths; `credential install` writes `config.json` and says so; a reissue
   writes `acme.demo.bob.2.token`; its NEXT names `bob2.key`, never the old
   key; installing over the same address is a typed confirmation, no
   `--force`; what bob keeps is said once; walk finding 11 is under Facts.
5. V5, false fixes. The MASH_URL error names MASH_URL; the removed-command
   messages echo the person's files; `reissue` names `--public-key` first;
   delete's NEXT lists what remains; `endpoint create` refuses a MASH_URL for
   another server; `--creds-out` says `wrote`; the one-machine `--explain`
   headings say "on this machine".
6. V6, names. `PATH` is `ADDRESS`; `Adopted`; one server form
   (`tls://myserver:4222`, plus `tls://testserver:4222` for the two-server
   MASH_URL examples); `endpoint --help` lists `--all`, `--changed`,
   `--dry-run`; `create --help` lists only flags that work; `<address>` and
   `<file>`.
7. The conventions block is written for a reader (no internal labels) and is
   copied into the reviewer packet, which had only three header lines.
8. Dispositions of every finding of the critic's round 2 are at the end.

## Dispositions of the critic's round 2

Numbers follow the critic's headings: A (every command's output, with the
measured facts as A.m and each command by name), B (the owner's complaints, B.1
to B.7), C (flows, C.1 to C.5), D (destructive commands, D.1 to D.7), E
(consistency, E.1 to E.9), F (the five serious problems, F.1 to F.5) and G (the
verdict). "Fixed" names the section; "contract" means accepted and left to the
contract or the build; "declined" gives the reason. Sections are "P1 s.N" for
Part 1 section N and "P2 X" for Part 2 section X.

| FINDING | DISPOSITION | WHERE |
|---|---|---|
| A.m1 lines at 77 to 80 columns, the wide header and the dry-run template at 80 | fixed: the dry-run form names no server, so it is short; lints check width | P2 F, make_packet.py |
| A.m2 36 lines name the config file or the server's source | fixed (V1) | P1 conventions; every result line |
| A.m3 seven server forms | fixed (V6); the one second server, `testserver`, is the MASH_URL example | whole file |
| A.m4 exit codes shown with no table; first-run blocks show no code | fixed: one table; first-run blocks without `[exit N]` exit 0 (stated in conventions); their refusals show codes | P1 conventions |
| A.0 setup block says one machine over a two-machine flow | fixed | P1 s.0 |
| A.1a.1 repo-relative SKILL.md exposes plumbing | declined: the URLs name the files the contract (REQ-002) names; an installed binary can open a URL | P1 s.1a, P2 A5 |
| A.1a.2 `1a` duplicated; two texts for removed `list` | fixed: one text, the A5 text | P1 s.1a |
| A.1b.1 `(from --server)` | fixed (V1) | P1 s.1b |
| A.1b.2 check prose behind `--detail` | declined: the maintainer asked each check to say what it means (Part 1, choice 15) | P1 s.1b |
| A.1b.3 CA by environment variable | fixed: `--ca <file>`; the wart paragraph is gone | P1 s.1b, P2 H1 |
| A.1b.4 undefined words (`--make-admin`, operator-signed, system credentials) | fixed: check 2 and the help say it in plain words; `system credentials` stays as the name of the file | P1 s.1b, P2 H1 |
| A.1b.5 NEXT says "on myserver" | fixed: "on this machine" | P1 s.1b |
| A.1c NEXT offers only the other-machine path | fixed: both paths | P1 s.1c |
| A.1d.1 `signed in` repeats the owner | fixed | P1 s.1d |
| A.1d.2 failure forms print two verdicts | declined: verdict first is R-verdict and REQ-004; the verdict is a result and `error:` is the explanation (conventions) | P1 s.1d |
| A.1d.3 fix assumes the reader runs the server | fixed | P1 s.1d |
| A.1d.4 `--detail` key words unglossed | fixed: one line says what each key is | P1 s.1d |
| A.S1 Step 1 does not say why | fixed: one line | P1 s.2 Step 1 |
| A.S2.1 server and config path in the first line | fixed (V1) | P1 s.2 Step 2 |
| A.S2.2 token never explained in the short form | fixed: the file line says what it proves | P1 s.2 Step 2 |
| A.S2.3 NEXT has placeholders for known values and omits the CA | fixed: no placeholder; the fingerprint is typed at the install's prompt (Choice 36) | P1 s.2 Step 2 |
| A.S3 "Keep bob.key" contradicts "the one file mash runs with" | fixed: said once, in the long form | P1 s.2 Step 3 |
| A.alice.1 `--creds-out`: false "Installed", `kept` noise, placeholders, heading | fixed | P1 s.2 alice |
| A.alice.2 MASH_URL variant writes credentials for the wrong server | fixed: a refusal naming both servers | P1 s.2 alice |
| A.alice.3 `--explain` headings claim two machines | fixed: "on this machine" | P1 s.2 alice |
| A.list.1 column `PATH` | fixed (V6) | P1 s.3, P2 |
| A.list.2 `--wide` host contradicts `show --detail` | fixed: both `-` | P1 s.3 |
| A.show.1 `type` / `agent` / `TYPE` | fixed: `type`; the program is `runs with` | P1 s.3 |
| A.show.2 credentials line for a remote endpoint | fixed: `not on this machine` | P1 s.3, P2 H7 |
| A.show.3 no suspended, unread, online | fixed | P1 s.3, P2 C3 |
| A.show.4 `--detail` fields unexplained, `capability` never explained | contract: Facts 25 asks what `--capability` grants; help text follows the answer | P2 Facts 25 |
| A.show.5 `storage name` / `directory key`, `mail subject` / `address for mail` | fixed: one label each | P2 B1, D1 |
| A.session.1 step 3 is config theory | fixed | P1 s.4 |
| A.session.2 `mash status` error ignores installed endpoints | fixed: lists them | P1 s.4 |
| A.session.3 why the launch flag | fixed: one sentence | P1 s.4 |
| A.A3.1 two commands on one help line | fixed | P2 A3 |
| A.A3.2 `endpoint resume` missing | fixed | P2 A3 |
| A.A3.3 removed-commands block in top-level help | fixed: removed; the A5 errors say it | P2 A3 |
| A.A4.1 `endpoint --help` omits `--all`, `--changed`, `--dry-run` | fixed (V6) | P2 A4 |
| A.A4.2 `create --help` omits `--owner`/`--group` that E1 shows | fixed: E1 no longer shows them; the help says there are none | P2 A4a, E1 |
| A.A4.3 placeholder forms | fixed for address and file; `KEY`, `ID`, `N` stay | P2 whole |
| A.A4.4 `suspended` exists to set an exit code | fixed: folded into `show` | P2 A1, C3 |
| A.A5.1 renames print files that do not exist | fixed (V5) | P2 A5 |
| A.A5.2 `reissue` fix moves a remote key here | fixed (V5) | P2 A5 |
| A.B reissue and delete | see D.1 to D.7 | P2 B |
| A.C2 word order, `colour` | fixed | P2 C2, H6 |
| A.C3 title-case labels, internal values | fixed: lowercase; the id and the number stay in the default because `deprovision` needs them (Part 2 C3 asks to confirm) | P2 C3 |
| A.D1.1 parenthetical on every run | fixed: in `--help` | P2 D1 |
| A.D1.2 second header after the verdict | declined: kept without the server; an audit has a time | P2 D1 |
| A.D1.3 `FIRST LAST` unexplained | fixed: footer | P2 D1 |
| A.D1.4 `--strict` fix; exit under PROBLEM | fixed: the delete command; a block shows exit 0 without `--strict` | P2 D1 |
| A.D1.5 `--compare` footer | fixed | P2 D1 |
| A.D2.1 unheaded seventh column | fixed: footer | P2 D2 |
| A.D2.2 `--wide` truncates the key; order changes | fixed: order; declined: truncation, because REQ-001 has no `--wide` exemption (`--json` has the whole key) | P2 D2 |
| A.D3 `mash-local`, unglossed key words, INCOMPLETE exit 2 | fixed | P2 D3 |
| A.D4 STATE implies a per-message fact | fixed: footer | P2 D4 |
| A.D5 title-case labels in `limits --show` | fixed | P2 D5 |
| A.D6 closing hint names a NATS tool | fixed | P2 D6 |
| A.D7 "set up with adopt" | fixed: "adopted" | P2 D7, G |
| A.D8.1 section G heading shows the wrong help | fixed: both helps | P2 G |
| A.D8.2 future-dated warning | fixed: out of the default | P2 D8 |
| A.D8.3 help example with no command | fixed | P2 D8 |
| A.E.1 E2 MASH_URL fix names the wrong thing | fixed (V5) | P2 E2 |
| A.E.2 E1 prints `<owner>` though the owner is known | fixed | P2 E1 |
| A.E.3 `limits nobody` does not echo | fixed: addresses are echoed | P2 E1 |
| A.E.4 E3 shows `--make-key` refusals under `--public-key` | fixed: split by command | P2 E3 |
| A.E.5 `.creds.pending` is hidden state | contract: Facts 21 | P2 E3, Facts 21 |
| A.E.6 `bf2` | fixed | P2 E4 |
| A.E.7 E5 "never created" and "removed in part" | fixed | P2 E5 |
| A.E.8 E5 "recorded too many times" | fixed | P2 E5 |
| A.E.9 `fix:` layout inconsistent | fixed: conventions say a `fix:` follows its `error:` and a `NEXT:` is set apart; the two multi-line layouts stay, written down | P1 conventions |
| A.Color colour off | fixed: off with `NO_COLOR` and when not a terminal; what is coloured is listed | P1 conventions |
| B.1 trash on every command | fixed (V1) | whole file |
| B.2 aligned list | answered; `PATH` to `ADDRESS` | P1 s.3 |
| B.3 `show` repeats names | fixed: unread, online, status added; host agrees | P1 s.3 |
| B.4 key, endpoint, credential not understandable | fixed | P1 s.2 |
| B.5 a `next:` that was not the next step | fixed: delete, init, adopt | P1, P2 B4 |
| B.6 list sketch | fixed: `ADDRESS` | P1 s.3 |
| B.7 mashctl must not install the server | fixed: "adopted" | P2 D7, G |
| C.1 bob flow fails as printed, CA hand-off missing | fixed | P1 s.2 |
| C.2 same machine, MASH_URL and `--creds-out` | fixed | P1 s.2 alice |
| C.3 reissue: token collision, wrong fix | fixed (V4, V5) | P2 B2, B3 |
| C.4 delete: nothing proves the old credential is refused | fixed: `Checked:` and the `mash status` block | P2 B4 |
| C.5 adopt: CA by environment variable | fixed | P1 s.1b |
| D.1 delete acts with no prompt; the count is hidden | fixed (V2) | P2 B4 |
| D.2 `--delete-creds` hint comes too late | fixed: in the prompt text | P2 B4 |
| D.3 repeat and `--force` give the same result | fixed: the plain command refuses, `--force` finishes | P2 B5, H7 |
| D.4 single reissue with no local credentials | fixed: refusal, and the typed prompt | P2 B1, E4 |
| D.5 `--no-revoke` | no change asked | P2 B5 |
| D.6 dry-runs | no change asked; they print the same "would lose" text | P2 B |
| D.7 "all 1 servers confirmed it" | fixed: "the server" | P2 B |
| E.1 address forms in file names | fixed: one line in conventions | P1 conventions |
| E.2 label drift (`PATH`, `type`, storage name, mail subject, creds) | fixed except `.creds` and `--creds`, which are core's names (Part 1, choice 13) | whole file |
| E.3 server forms | fixed (V6) | whole file |
| E.4 capitalization | fixed for labels and for sentences that began with a name; `Detail` headings stay as section titles (declined) | P2 B, C3, D |
| E.5 time forms | no change asked | none |
| E.6 exit codes | fixed (V3) | P1 conventions |
| E.7 stdout and stderr | fixed: the conventions block, now in the packet | P1 conventions |
| E.8 conventions block versus examples | fixed: block rewritten and linted | P1 conventions |
| E.9 mock hygiene | fixed | P1 s.1a, P2 G |
| F.1 provenance in the first sentence | fixed (V1) | whole file |
| F.2 cross-machine flow fails when followed | fixed (V4) | P1 s.2, P2 B2 |
| F.3 false fixes and NEXT lines | fixed (V5) | P2 A5, B4, E2; P1 s.2 |
| F.4 destructive commands act without confirmation | fixed (V2) | P2 B |
| F.5 no conventions; collisions; doubled verdicts | fixed except doubled verdicts (A.1d.2, declined) | P1 conventions |
| G.1 strip provenance | fixed | whole file |
| G.2 known values in NEXT, carry the CA | fixed | P1 s.2 |
| G.3 false fixes | fixed | P2 |
| G.4 delete and single reissue confirm | fixed | P2 B |
| G.5 token collision, duplicate and mislabeled blocks | fixed | P2 B2, G |
| G.6 one exit-code table | fixed | P1 conventions |
| Direction V1 no server or path in success output; one warning | fixed; lint added | P1 conventions; make_packet.py |
| Direction V2 destruction typed, no `--yes`, count for bulk | fixed | P2 B |
| Direction V3 one exit-code table, a line per changed command, a lint | fixed | P1 conventions; P2 choices 38 to 52 |
| Direction V4 real values, two machines, numbered token, install without `--force` | fixed | P1 s.0, s.2; P2 B2 |
| Direction V5 false fixes | fixed | P2 A5, E1, E2 |
| Direction V6 names | fixed | whole file |
| Contract rev 4 REQ-001: server in no success output | fixed; but REQ-001 and JRN-006 also say a write names its server "as part of a write's confirmation": V1 supersedes that, so the contract needs the same edit | P1 conventions |
| Contract rev 4 INV-004 destruction | fixed; the contract cites "the CLI standard's confirmation rule" with no number: rule 15 of standard 1.1.1 is quoted | P1 choice 35 |
| Contract rev 4 exit codes | fixed: 5 and 6 added to rule 14; `3 cancelled` used for a wrong answer | P1 conventions |
| Contract rev 3 REQ-003 install writes `config.json`, `--ca-fingerprint` | fixed, with one departure: the install asks the person to type the fingerprint (INV-004), so the NEXT stays short; the flag is for the contract to settle. The token carrying the server and `ca.pem` beside it are assumed and listed | P1 s.2, choice 36 |
| Contract rev 3 REQ-009 one endpoint setting | fixed: no setting where one endpoint is installed | P1 s.4 |
| Contract REQ-005 table header `PATH` | contract: it must say `ADDRESS` | P2 choice 19 |
