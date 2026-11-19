# mash status views, DRAFT1

Date: 2026-11-19. Status: DRAFT1, paper design only (nothing is built, no
product code, the targets document is not edited). It answers the maintainer's
comment on the noon page: "I see no reason to put the mash server details in
the mash status. There's only one server in your mash and if you don't know
what it is, it's a problem." He proposes `mash status` (my local agent
endpoint), `mash server status` (my mash server) and `mash endpoint status`
(all endpoints by my owner, taking an endpoint path and simple patterns), and
asks: "Other?", "can you see heartbeat status?" and "What does mash report via
SAP status, and should it be in mash status?".

No clock: quality first. The fixed-rows idea of the status screen stays (R11 of
`docs/plans/2026-11-19-cli-targets-mash.md`, DRAFT6c); the rows change.

Sources. The v0.5.5 source at tag v0.5.5 (cited `file:line`, paths relative to
the repository root), run for the permission facts (section 1: an in-process
test server with the production permission set, a throwaway test that was
deleted afterwards). The targets (`docs/plans/2026-11-19-cli-targets-mash.md`,
cited "targets"), the catalog of today's output
(`docs/plans/2026-11-19-cli-output-catalog-mash.md`), the mashctl lane's
DRAFT15 (cited "ctl D15", its `mashctl status`, `endpoint list`, `--wide` and
`issued` parts), the CLI standard 1.2.6 (`~/ops/standards/cli/STANDARD.md`,
cited "standard") and ADR 0066 (`docs/decisions/0066-...`).

Every fenced block that starts with `$` is a TARGET (what a person should see),
in the style of the targets: owner `acme`, group `demo`, endpoints
`acme.demo.alice`, `acme.demo.bob` (the person at the keyboard) and
`acme.lab.carol`, machine `myserver`, server `tls://myserver:4222`. Section 9
measures every fenced block.

## Contents

0. In short
1. Tone: what a status screen is for
2. What an endpoint may read (the facts every view stands on)
3. (b) What mash reports through SAP status today, field by field
4. (a) The three views: `mash status`, `mash server status`,
   `mash endpoint status`
5. (c) "Other?": the questions none of the three answer, and the names
6. (d) Fit with the CLI standard; Agree with ctl
7. Open questions for the maintainer
8. What this changes in the targets doc
9. What I could not verify, and checks run

## 0. In short

The recommended view set (one name per thing):

- `mash status`: this endpoint only. Verdict, then six fixed rows: version,
  server (reachable or not, no address), config, credentials, inbox (how much
  mail is new), session (is a Claude Code session serving it).
- `mash server status`: the server. Address, reachable, encrypted and its
  certificate, clock, storage, message limit, endpoints registered. Only what
  an ordinary endpoint's credentials may read; the rest is `mashctl`.
- `mash endpoint status [<address or pattern>]`: the endpoints of your owner,
  live: online or offline and last seen. It REPLACES `mash who` (hard cut, no
  alias). A pattern is three dotted parts, owner literal, `*` inside a part.
- `mashctl endpoint list` stays the administrator's registry (created, type,
  note, never-seen endpoints included); `mashctl status` becomes `mashctl
  server status`, the same noun as the person's `mash server status`.
- Heartbeat and SAP status stay internal. A person sees conclusions
  (online, session serving), never the payload.

His two direct questions, plainly (evidence in section 3):

Can you see heartbeat status? There is a heartbeat: each running session
publishes a signed one every 30 seconds (`internal/endpoint/service.go:67,
201-219, 274-287`), but since ADR 0066 nothing in mash listens to it. What
replaced it is a probe made when someone asks (`client.go:474-500`): "online"
means "answered the probe just now", about 0.3 seconds when anyone answers and
2 seconds when nobody does (`client.go:26-32`). No heartbeat time or age is
stored anywhere, so you can see online now; you cannot see "heartbeat status".

What does mash report via SAP status, and should it be in `mash status`? The
`status` verb (`agents.status.<owner>.<group>.<name>`) answers with the same
eight-field payload as the heartbeat, which has no health data at all: no host,
no version, no mail count (`service.go:179-199, 260-266`); and no mash command
asks it. It should NOT be in `mash status`: it says only "I answered", which
`mash status` shows as the session row and `mash endpoint status` as `online`.

What the targets get wrong today, found while reading (details in sections 3
and 7): the `LAST SEEN 3h ago` of `mash who` cannot be produced by 0.5.5 (the
time is kept in the memory of one process and is set only for an endpoint that
answered a probe, `directory.go:25,79,94`); the claim that `mash who` shows only
your owner's endpoints (N13) is wrong (`directory.go:89-103` filters nothing,
presence is the part that is owner-only, `directory.go:64`); and `runs on` in
`who --detail` is the directory's `host` field, where an administrator says the
endpoint is meant to run, not a live fact for any endpoint but your own
(`cmd/mash/serve.go:914-922`).

## 1. Tone: what a status screen is for

The maintainer's comment is the whole rule: show a person only what they cannot
be expected to know, and say it in the words of their own work. Eight rules for
the three views, each one a question the screen must pass:

1. The first line is the answer, in a sentence, about the thing asked.
   `acme.demo.bob: mail works.` A person who reads one line knows.
2. Ask about one thing, answer about that thing. `mash status` is about this
   endpoint; it does not report the server's address, the group, or anyone
   else's state. A neighbour's facts have their own command.
3. Rows are fixed. The same rows in the same order in every state, so a
   person can compare two screens. A row that cannot be checked says `not
   checked`, with the reason in brackets when the reason is not obvious.
4. A row holds a conclusion, not a measurement. `reachable`, not `answered in
   41ms` (that is behind `--detail` or on `mash server status`).
5. Capitals only for a problem. `NOT REACHABLE`, `REFUSED`. Offline is not a
   problem: an endpoint that nobody is running is normal, and shown in lower
   case, with exit 0.
6. Nothing a person is expected to know is printed. The server address, the
   endpoint's own address (named once, in the verdict), the version of the
   protocol under the mail: absent. The address appears in an error or a fix
   only when it is what the person must check.
7. One thing, one name, one form: the dotted address, `online`, `offline`,
   `new`. No second word for the same thing (`unread`, `alive`, `lease`).
8. A fix is a true next step and a command stands alone (R4, R9, R10). A
   screen that worked ends without a hint: the answer was the point.

## 2. What an endpoint may read (the facts every view stands on)

The maintainer asked for the server's JetStream, streams, limits and storage
"if a person can read them without admin credentials". The permission set of an
ordinary endpoint is `Permissions` (`internal/admin/permissions.go:29-50`) and
`fixedPub` (`permissions.go:56-77`); anything not listed is denied (comment,
`permissions.go:15-18`). I ran the production set against an in-process
server (nats-server 2.15.1, a test fixture of the repository; one throwaway
test file in a scratch clone, deleted) connected as Bob, with a live session
registered as Alice. Result of each request (a denied request is not an error
to the caller: it is dropped and the request times out, so a view must never
ASK for a denied subject: each such ask costs its whole timeout):

| what a person asks | subject | granted by | live result |
|---|---|---|---|
| the JetStream account's usage and limits | `$JS.API.INFO` | `permissions.go:65` | answered: storage 1066B used, 131072000B reserved; 7 streams of 50, 2 consumers of 100; memory limit 64MB, storage limit 256MB |
| who runs a service (SAP agents) | `$SRV.PING`, `$SRV.INFO`, `$SRV.STATS` | `permissions.go:44,64` | answered with Alice's registration (name, version, metadata, endpoints); STATS adds request counts |
| one endpoint's live answer | `agents.status.<owner>.<g>.<n>` | `permissions.go:36-38` | Alice answered Bob with the heartbeat payload (section 3) |
| the directory's state | `$JS.API.STREAM.INFO.KV_mash-directory` | `permissions.go:72` | answered: 2 records, 780B, cap 16MB, file storage, 1 replica |
| every directory record | `$JS.API.DIRECT.GET.KV_mash-directory.>` | `permissions.go:73` | allowed for ALL keys, every owner's (`client.go:234-258,269`) |
| the lease bucket's state | `$JS.API.STREAM.INFO.KV_mash-leases` | `permissions.go:74` | answered: ends after 90s (`max_age`) |
| its OWN lease, nobody else's | `...DIRECT.GET.KV_mash-leases.$KV.mash-leases.<own key>` | `permissions.go:75-76` | own only (`directory.go:136-148`, `serve.go:914-922`) |
| its OWN inbox stream and consumer | `$JS.API.STREAM.INFO.inbox-<own>`, `$JS.API.CONSUMER.INFO.inbox-<own>.deliver` | `permissions.go:66-68` | answered: cap 16MB, kept 30 days, discard oldest; messages, ack and pending counts |
| ANOTHER endpoint's inbox | `$JS.API.STREAM.INFO.inbox-<other>` | not granted | denied (timed out) |
| the list of streams | `$JS.API.STREAM.LIST`, `.NAMES` | not granted | denied (timed out) |
| the server itself | `$SYS.REQ.SERVER.PING`, `...PING.VARZ` | not granted | denied (timed out) |

Without any subject: the server's own greeting on connect gives the version
(2.15.1), the server name, `max_payload` (1048576B: the largest message), the
connected address, and, on a tls address, the certificate chain (Go's
`tls.ConnectionState`: subject, issuer, `NotAfter`). mash uses `MaxPayload`
(`internal/endpoint/mail.go:715,798`); it reads no certificate field today
(searched: no use of `TLSConnectionState` or `NotAfter` outside the test
helpers). Stream and account answers carry the server's clock (`ts`), from
which the offset used by `ClockSkew` (`endpoint.go:44-47, 855-868`) comes.

What this decides:

- A person CAN see, with their own credentials: whether the server answers,
  whether it is encrypted and its certificate (and how long it is valid), its
  clock against theirs, the account's storage used and its limits (shared by
  every owner in the account), the stream count against its limit, the largest
  message, the number of registered endpoints, their own inbox's fill, kept
  time and counts, and who is online in their OWN owner.
- A person CANNOT see: the server's connections, uptime, software of other
  servers, load or logs (`$SYS`); any other inbox; the list of streams; who is
  online in ANOTHER owner. For the last: an endpoint publishes replies only
  into `_INBOX.<its owner>.>` (`permissions.go:19-24,63`), so another owner's
  agents cannot answer it, and `applyPresence` drops any answer whose
  metadata owner is not the caller's (`directory.go:64`). That is a fact, not
  a rule mash chose: "an address pattern never crosses owners" (section 4) is
  the only honest rule, because beyond the owner nothing is knowable.
- All of that beyond a person's rights is `mashctl` (it signs in as the
  administrator; ctl D15 `status`, `audit`, `issued`, `limits`, `permissions`).

## 3. (b) What mash reports through SAP status today, field by field

Three different messages carry "status" and are easy to confuse:

- The SAP `status` verb: a request to `agents.status.<owner>.<group>.<name>`.
  Every serving endpoint registers a responder (`service.go:260-266`) and
  answers with the heartbeat payload. Nothing in mash sends it (searched: the
  subject appears only in `address.go:53-58`, the permission lists and the
  responder; discovery uses `$SRV.INFO`, `service.go:425-446`).
- The heartbeat: the same payload, signed, published to
  `agents.hb.<owner>.<group>.<name>` every `HeartbeatInterval` = 30s
  (`service.go:67, 201-219, 274-287`). Only an endpoint's own owner may
  subscribe (`permissions.go:45`); mash itself no longer does (ADR 0066).
- The `$SRV.INFO` registration of the service named `agents` (`service.go:
  229-238`): the thing the presence probe reads.

The payload (`heartbeat()`, `service.go:179-199`; the reply to the status
verb is the same bytes, unsigned: `service.go:264`) and the other record
families, each with what a person should see and where:

| field | where it comes from | what a person should see | where |
|---|---|---|---|
| `agent` = `claude-code` | `service.go:183-199`, `sap/subjects.go:8` | the program an endpoint runs | endpoint status `--detail` (`program`) |
| `owner` | `service.go:183-199` | nothing: it is in the address | nowhere |
| `session` (the storage name `acme_demo_bob`) | `service.go:183-199` (`e.key`) | nothing (R3: storage name only behind `--detail`) | nowhere; `--detail` of the owner's views at most |
| `instance_id` (the service's random id) | `service.go:183-199`, `Instance()` `:166-172` | nothing: it changes with every serve | nowhere |
| `ts` (the second it was made) | `service.go:183-199` | nothing: it is "now" | nowhere (internal) |
| `interval_s` = 30 | `service.go:67,189` | nothing | nowhere (internal) |
| `protocol_version` = 0.3 | `sap/subjects.go:9` | nothing a newcomer can act on | nowhere (internal) |
| `endpoints.prompt.subject`, `endpoints.mail.subject` | `service.go:183-199` | nothing (`mail subject` is ctl `show --detail`) | nowhere |
| `endpoints.prompt.metadata` (`max_payload` 1MB, `attachments_ok` false, `min_sender_trust` signed) | `service.go:174-176` | nothing; a live prompt that is too long is refused with its own error | nowhere |
| `endpoints.mail.metadata` (`durable` true) | `service.go:183-199` | nothing | nowhere |
| signature header on the heartbeat | `service.go:208-216` | nothing | nowhere |
| `$SRV.INFO` `version` (the answering mash) | `service.go:230-231`, `CurrentVersion()` | the version a peer runs, for an online peer | endpoint status (`--wide` `VERSION`, one-address view) |
| `$SRV.INFO` `description`, `name` | `service.go:230,232` | nothing | nowhere |
| `$SRV.INFO` metadata `agent`, `owner`, `group`, `instance` | `service.go:233-238` | the address | the address column |
| `$SRV.INFO` metadata `user_nkey`, `account`, `id_sig`, `mail_subject` | `service.go:233-238` | nothing (the key is checked against the directory, then dropped) | nowhere (internal; `directory.go:54-85`) |
| online (the probe) | `client.go:474-500`, `directory.go:54-103` | online or offline | endpoint status `STATE`; `session` row of `mash status` (own) |
| last seen | `directory.go:25,79,94` (memory of one process) | when it last answered | endpoint status `LAST SEEN`, needs a new record (section 4, question 4) |
| directory record: owner, group, instance | `store/store.go:223-228` | the address | address column |
| directory record: `agent` | `store.go:227` | the program | `--detail` |
| directory record: account, user, mail_subject | `store.go:228-230` | nothing | nowhere (internal) |
| directory record: created | `store.go:231` | when it was registered | endpoint status `--detail` (`registered`) |
| directory record: note, role, host, description, caps, labels | `store.go:232-242` | what it is for; where it is meant to run (`host`) | `--wide` / `--detail`; `--role`, `--label`, `--cap` filters |
| own lease: `host`, `since`, `version`, `commit`, `pid`, `machine` | `endpoint.go:147-168` | whether a session serves this endpoint, since when, on which machine | `mash status` row `session`; `--detail` for host |
| own lease: `mcp_seen`, `launch_token`, `ppid`, `session` | `endpoint.go:155-171` | nothing | nowhere (internal, takeover logic) |
| own lease: `waking_pending` + `_at` | `endpoint.go:172-178`, `waking.go` | nothing: it counts mail that should WAKE, not new mail | nowhere (the inbox row uses the inbox's own count) |
| own inbox: counts, fill, evictions | `eviction.go:22-40` | new mail; a warning when mail was lost or the inbox is nearly full | `mash status` row `inbox` and a `warning:` |
| clock offset | `endpoint.go:855-868`, `status.go:325-345` | only when it is wrong (beyond 10s) | `mash server status` row `clock` |
| server `max_payload` | `mail.go:715,798` | the largest message | `mash server status` row `message limit` |

What `mash who` uses today (`cmd/mash/cli.go:166-179`, `clientNeeds`
`:211-213`):
the whole directory (`FullDirectory`, every owner), a presence probe
(`Refresh`), and with `--long` the caller's own lease only (`liveLeases`,
`serve.go:914-922`). Its JSON carries `last_seen` only when the probe was
answered (`whofilter.go:130-132`).

### Heartbeat: the answers

Is there a heartbeat today? Yes, and it is the same message as the status
reply: 30 seconds apart, signed, from every serving session
(`service.go:276-286`). If publishing fails it is written to the endpoint's own
log and shown nowhere (`service.go:210,217`).

What replaced it? ADR 0066 (accepted 2026-11-02): `mash serve` dropped the
standing subscription to everyone's heartbeats (N squared deliveries: 33,333 a
second at 1,000 endpoints) and asks "who is online" when someone asks. The
probe is one request to `$SRV.INFO.agents` (`client.go:474-500`, subject at
`:492`); every serving agent answers; an answer counts only when its `id_sig`
verifies and its key is the directory's key for that name (`directory.go:
54-85`).

What does "online" mean, and how late is it? Online means "answered the latest
probe" (`directory.go:89-103`; the 90-second `OnlineWindow` of the old
heartbeat rule survives only for a process that has not probed yet,
`directory.go:17-19`). The window closes 300ms after the last answer, or at 2s
when nobody answers (`PresenceQuiet`, `PresenceCap`, `client.go:26-32`); ADR
0066 measured about 312ms. Inside a session the result is cached 30 seconds
(`presence.go:11`); `mash who` from a shell probes every time. The cost of the
cap: an endpoint that answers after the quiet window shows offline for that
ask (ADR 0066, Consequences).

Is the time of the last heartbeat or answer kept? No. `lastSeen` lives in the
process's memory (`endpoint.go:100`, set at `directory.go:79`), so a one-shot
`mash who` knows a last-seen time only for the endpoints that answered it, and
that time is "now". An endpoint that was online yesterday shows no last-seen at
all today. The target `LAST SEEN 3h ago` (targets, `who`, lines 2663-2684) has
no source in 0.5.5. Section 4, question 4 says what it needs.

Should the SAP status payload be in `mash status`? No. Every field is either
internal (instance id, timestamp, interval, subjects, protocol) or a limit of
the live-prompt path nobody asked about. What a person asks, "is it serving,
is it reachable", is a conclusion, and the screens show it as one.

## 4. (a) The three views

### 4.1 `mash status`: this endpoint only

The question: "can this endpoint send and receive mail, and is anything
waiting?" It names no other endpoint and no server address.

The six fixed rows, in this order, in every state, each with its reason:

| row | why it is there, in one line |
|---|---|
| `version` | which mash this is: the first thing anyone asks, and the plugin-drift warning hangs on it |
| `server` | can this machine reach the server, encrypted and trusted; the one outside fact mail needs; NO address |
| `config` | is there a configuration whose trust files agree (R21) |
| `credentials` | is this endpoint's own file there, private, and accepted by the server |
| `inbox` | is mail waiting, and how much: the first thing a person wants to know once it works |
| `session` | does a Claude Code session serve this endpoint: why mail did, or did not, arrive on its own |

Not rows: the endpoint (it is in the verdict and in `--detail`), how it was
chosen (`--detail`), the server's address (`mash server status`; in an error
or fix only when it is the thing to check; `--detail` keeps a `server` fact
row, question 3), the group and owner counts (the verdict line of `mash
endpoint status`), heartbeat, hooks, clock, name rule, relay trace (`--detail`
or internal).

Row values, the complete lists (anything else is a defect):

- `version`: the number.
- `server`: `reachable`; `reachable, NOT ENCRYPTED` (the override allows a
  plain address); `reachable, certificate NOT TRUSTED`; `NOT REACHABLE`;
  `NOT ENCRYPTED (refused; not connected)`; `not checked` with a reason when
  mash did not ask. No time and no address: a row that says `reachable` was
  checked just now, so the DRAFT4 defect (`alive` with a past time) cannot
  occur, and the `last reached` of N10 is no longer needed here.
- `config`, `credentials`: as the targets (`ok`, `MISSING`, `REFUSED`,
  `UNSAFE`, `not checked`).
- `inbox`: `no new mail`; `2 new (1 urgent)`; `not checked`. The count is the
  one `mash inbox` says in its header ("57 messages ..., 3 new, 1 urgent";
  targets, `inbox`); it comes from the endpoint's own inbox and consumer,
  which the credentials may read (section 2). When mail was lost or the inbox
  is nearly full, a `warning:` follows (`eviction.go:22-60`).
- `session`: `serving since 09:12`; `not running`; `not checked`. From the
  endpoint's own lease (section 2), and fresh within 90 seconds
  (`store.LeaseTTL`, `store.go:22`; the lease is renewed every 30s,
  `endpoint.go:30`).

Verdict forms are the targets': `<endpoint>: mail works.`, `... (1 warning).`,
`... mail does NOT work: <why>.`, `mail does NOT work: <why>.` when no endpoint
is known. `session: not running` does not change the verdict: mail waits.

Exit codes as the contract (section 2 of the targets): 0 mail works, 1 it does
not, 4 refused, 6 unreachable, 2 usage.

Healthy:

<!-- packet: status-healthy -->
```
$ mash status
acme.demo.bob: mail works.

  version      0.5.6
  server       reachable
  config       ok
  credentials  ok
  inbox        2 new (1 urgent)
  session      serving since 09:12
```

Healthy, `--detail`. The seven rows of REQ-012 are unchanged and come after
the six. The group counts leave (they are the verdict of `mash endpoint
status`), the clock and the answer time leave (they are `mash server status`);
the configured server address is one fact row (question 3):

<!-- packet: status-detail -->
```
$ mash status --detail
acme.demo.bob: mail works.

  version      0.5.6
  server       reachable
  config       ok
  credentials  ok
  inbox        2 new (1 urgent)
  session      serving since 09:12

  config file       /home/bob/.config/mash/config.json
  ca                /home/bob/.config/mash/ca.pem
  credentials file  /home/bob/.config/mash/acme.demo.bob.creds
  trust file        /home/bob/.config/mash/trust.json
  trust             matches config (generation 1; the account acme)
  endpoint          acme.demo.bob
  chosen by         the only one installed here

  server        tls://myserver:4222
  commit        e389eaf
  token expires never
  storage name  acme_demo_bob
  name rule     none set (no list limits which names are accepted)
  session       on myserver since 2026-11-19 09:12 UTC
  claude code   plugin hooks installed; mash may wake an idle session
  relays seen   0 messages relayed through another endpoint
  settings      MASH_* none set; the server and CA come from config.json
```

Claude Code is not set up to wake an idle session (warning, exit 0; the
warning is the targets' and is unchanged):

<!-- packet: status-claude -->
```
$ mash status
acme.demo.bob: mail works (1 warning).

  version      0.5.6
  server       reachable
  config       ok
  credentials  ok
  inbox        1 new
  session      serving since 09:12

warning: an idle Claude Code session is not woken by new mail; the mail
         waits for its next prompt.
fix: start Claude Code with the mash channel, from the terminal you want it in:
  claude --dangerously-load-development-channels plugin:mash@mash
```

Server not reachable (exit 6). The address appears in the fix only: it is the
thing to check, and `mash server status` shows it with the cause when the
server is back:

<!-- packet: status-unreachable -->
```
$ mash status
acme.demo.bob: mail does NOT work: the server did not answer within 2s.

  version      0.5.6
  server       NOT REACHABLE
  config       ok
  credentials  not checked (the server was not reached)
  inbox        not checked
  session      not checked

fix: check that the server at tls://myserver:4222 is running, and that
     "server" in ~/.config/mash/config.json is right.
```

Credentials refused (exit 4):

<!-- packet: status-refused -->
```
$ mash status
acme.demo.bob: mail does NOT work: the server refused its credentials.

  version      0.5.6
  server       reachable
  config       ok
  credentials  REFUSED (deleted, or its key was reissued)
  inbox        not checked
  session      not checked

fix: ask whoever runs your mash server which of the two it was. If the key
     was reissued, move the old credentials aside, then install the new key
     and token:
  mv ~/.config/mash/acme.demo.bob.creds ~/.config/mash/acme.demo.bob.creds.old
  mash credential install --key <key file> --token <token file>
```

The endpoint was deleted (exit 4; needs the server to say "deleted", N11):

<!-- packet: status-deleted -->
```
$ mash status
acme.demo.bob: mail does NOT work: this endpoint was DELETED on the server.

  version      0.5.6
  server       reachable
  config       ok
  credentials  REFUSED (endpoint DELETED)
  inbox        not checked
  session      not checked

fix: its address cannot be used again. For a new endpoint, make a new key,
     then give its .pub file to whoever runs your mash server:
  mash key create --out <new-name>.key
```

No configuration at all (exit 1; nothing is asked, so every row after
`version` is `not checked`):

<!-- packet: status-noconfig -->
```
$ mash status
mail does NOT work: mash has no configuration on this machine.

  version      0.5.6
  server       not checked (no server is set)
  config       MISSING (~/.config/mash/config.json)
  credentials  not checked
  inbox        not checked
  session      not checked

fix: install the credentials your server made. mash asks for the server
     address and the CA fingerprint that whoever runs your mash server gave
     you. Add --ca <ca file> for a server with its own CA:
  mash credential install --key <key file> --token <token file>
```

Several endpoints installed and none chosen (exit 1). The server needs no
credentials, so it is still checked; the candidates move from the credentials
row to the fix, where they are acted on:

<!-- packet: status-choose -->
```
$ mash status
mail does NOT work: 2 endpoints are installed here and none is chosen.

  version      0.5.6
  server       reachable
  config       ok
  credentials  not checked (no endpoint is chosen)
  inbox        not checked
  session      not checked

fix: mash never guesses between acme.demo.alice and acme.demo.bob. To use
     acme.demo.alice, add this line to ~/.config/mash/config.json:
    "endpoint": "acme.demo.alice"
     For this shell only, and the Claude Code you start from it:
  export MASH_ENDPOINT=acme.demo.alice
```

Plain text with the override (warning, exit 0). Both addresses of the override
are in the warning, because the person must see what is overriding what:

<!-- packet: status-plain-warning -->
```
$ export MASH_SERVER=nats://myserver:4222
$ export MASH_INSECURE_PLAINTEXT=1
$ mash status
acme.demo.bob: mail works (1 warning).

  version      0.5.6
  server       reachable, NOT ENCRYPTED
  config       ok
  credentials  ok
  inbox        2 new (1 urgent)
  session      serving since 09:12

warning: anyone on the network can read your credentials and your mail.
         MASH_SERVER overrides the server in config.json for this run:
         nats://myserver:4222 instead of tls://myserver:4222. It is allowed
         only because MASH_INSECURE_PLAINTEXT is set (test servers only).
fix: stop overriding it in this shell:
  unset MASH_SERVER MASH_INSECURE_PLAINTEXT
```

The environment overrides the server (not ready, exit 1; mash does not ask
that server). A `MASH_CA` that differs reads the same with `MASH_CA`:

<!-- packet: status-env-override -->
```
$ export MASH_SERVER=tls://other:4222
$ mash status
acme.demo.bob: mail does NOT work: MASH_SERVER overrides config.json.

  version      0.5.6
  server       not checked (MASH_SERVER overrides config.json)
  config       ok
  credentials  not checked (the server was not asked)
  inbox        not checked
  session      not checked

fix: MASH_SERVER is overriding the server in config.json for this run
     (tls://other:4222 instead of tls://myserver:4222). Stop overriding
     it in this shell:
  unset MASH_SERVER
```

The other states keep the targets' text with the two rows changed the same
way: a plain address without the override (`server  NOT ENCRYPTED (refused;
not connected)`, exit 1), the override set with a tls address (a warning;
`server  reachable`), a credentials file that is not private (`credentials
UNSAFE ...`, exit 1) or missing, and the trust files that disagree (`config
REFUSED (disagrees with the trust record)`, `server  not checked (the trust
files disagree)`). In a running session the MCP `status` tool returns the
same six facts (section 8).

### 4.2 `mash server status`: the server

The question: "is my server up, is it safe to talk to, and has it room?" The
rows are what section 2 shows an endpoint may read, nothing from `$SYS` and
nothing that asks a denied subject. It needs the credentials only for the
storage rows (JetStream answers only a signed-in endpoint); reachability and
encryption need none, so they are shown even when the credentials are missing
or refused, and the storage rows say `not checked (<why>)`.

Fixed rows, in this order, in every state:

| row | why, in one line | source |
|---|---|---|
| `address` | the one place the address is shown, as configured, with its scheme | config |
| `state` | reachable, and how fast it answered (the connection and one request) | connect time |
| `encryption` | encrypted or not; the certificate trusted, and until when; a problem says which | the TLS handshake and chain |
| `clock` | the server's clock against this computer's: a skew beyond 10s makes peers look offline and mail `[stale]` (`status.go:321-345`) | `ts` of a stream or account answer |
| `mail storage` | space used of the limit (the account is shared by every owner) | `$JS.API.INFO` |
| `message limit` | the largest message the server accepts | the server's greeting |
| `endpoints` | how many are registered | directory stream info |

`--detail` adds, same screen plus rows: `server software` (NATS 2.15.1),
`certificate` (checked against which CA file), `certificate ends` (the date and
the days left), `streams` (7 of 50 in use; a stream holds an inbox or mash's
own records), `inbox rules` (each inbox keeps 16MB or 30 days of mail:
`permissions` allow the endpoint's own stream info, `eviction.go:31-37`) and
`storage set aside` (the server's `reserved_storage`, 125MB here, which counts
toward the limit; read as a number only, see section 9). A certificate within
30 days of its end is a warning (question 11).

Healthy:

<!-- packet: server-healthy -->
```
$ mash server status
The server is reachable and encrypted.

  address        tls://myserver:4222
  state          reachable (answered in 41ms)
  encryption     encrypted; certificate trusted until 2027-02-01
  clock          1s ahead of this computer
  mail storage   1KB used of 256MB
  message limit  1MB
  endpoints      3 registered
```

Healthy, `--detail`:

<!-- packet: server-detail -->
```
$ mash server status --detail
The server is reachable and encrypted.

  address        tls://myserver:4222
  state          reachable (answered in 41ms)
  encryption     encrypted; certificate trusted until 2027-02-01
  clock          1s ahead of this computer
  mail storage   1KB used of 256MB
  message limit  1MB
  endpoints      3 registered

  server software   NATS 2.15.1
  certificate       checked against /home/bob/.config/mash/ca.pem
  certificate ends  2027-02-01 (in 74 days)
  streams           7 of 50 in use (an inbox each, and mash's own records)
  inbox rules       each inbox keeps up to 16MB and 30 days of mail
  storage set aside 125MB of the 256MB, counted against the limit
```

Server not reachable (exit 6; every row after `state` is `not checked`):

<!-- packet: server-unreachable -->
```
$ mash server status
The server is NOT REACHABLE.

  address        tls://myserver:4222
  state          NOT REACHABLE (no answer within 2s)
  encryption     not checked
  clock          not checked
  mail storage   not checked
  message limit  not checked
  endpoints      not checked

fix: check that the server is running and that "server" in
     ~/.config/mash/config.json is right.
```

Plain text with the override (a warning, exit 0; the connection is allowed, so
the rest is read):

<!-- packet: server-plaintext -->
```
$ export MASH_SERVER=nats://myserver:4222
$ export MASH_INSECURE_PLAINTEXT=1
$ mash server status
The server is reachable, but NOT ENCRYPTED (1 warning).

  address        nats://myserver:4222
  state          reachable (answered in 3ms)
  encryption     NOT ENCRYPTED (allowed by MASH_INSECURE_PLAINTEXT)
  clock          1s ahead of this computer
  mail storage   1KB used of 256MB
  message limit  1MB
  endpoints      3 registered

warning: anyone on the network can read your credentials and your mail.
         MASH_SERVER overrides the server in config.json for this run.
fix: stop overriding it in this shell:
  unset MASH_SERVER MASH_INSECURE_PLAINTEXT
```

The certificate is not trusted (exit 1). The server answered, but mash does
not talk further to a server it does not trust, so the rest is `not checked`.
The cause is named in the row: not signed by this machine's CA, expired, or
for another host name:

<!-- packet: server-untrusted -->
```
$ mash server status
The server's certificate is NOT TRUSTED.

  address        tls://myserver:4222
  state          reachable (answered in 41ms)
  encryption     certificate NOT TRUSTED (not signed by the CA in
                 ~/.config/mash/ca.pem)
  clock          not checked (mash does not trust this server)
  mail storage   not checked (mash does not trust this server)
  message limit  not checked (mash does not trust this server)
  endpoints      not checked (mash does not trust this server)

fix: do not go on if you did not expect this. Ask whoever runs your mash
     server whether it changed its certificate. If it did, at a terminal:
  mash trust reset
     then install again with your credentials:
  mash credential install --key <key file> --token <token file>
```

For an expired certificate or a wrong host name the same screen says `expired
2026-11-01` or `made for another host name`, and the fix is to ask whoever
runs the server to renew it or to give the right address: `mash trust reset`
would not help, so it is not named. With credentials the server refuses the
screen is the same as the healthy one down to `encryption`, with `mail
storage`, `clock`, `message limit` and `endpoints` as `not checked
(credentials refused)`, exit 4. With no credentials installed, the same with
`not checked (no credentials here)`, exit 1. `mash server status` never asks a
`$SYS` subject: it would be dropped and cost the whole timeout.

What needs `mashctl`, said in `mash help server status` and nowhere on the
screen: who is connected now, the server's uptime and load, every inbox's size
(`mashctl audit`), the keys issued (`mashctl issued list`), and changing a
limit (`mashctl limits`).

### 4.3 `mash endpoint status [<address or pattern>]`: your owner's endpoints

The question: "which of our endpoints exist, and which are online?" It replaces
`mash who` (section 5). It lists only the endpoints of the caller's owner,
because beyond the owner nothing can be known (section 2); this also corrects
the targets, where N13 assumed `who` already did so.

The pattern syntax, chosen small enough to say in one sentence: an address is
`owner.group.name`; a pattern is the same three parts, where a part may hold
`*`, which stands for any run of characters (including none) inside that part
and never crosses a dot. The owner part must be written out and must be your
owner: a pattern never crosses owners.

| written | matches |
|---|---|
| (nothing) | every endpoint of your owner, the same as `acme.*.*` |
| `acme.demo.alice` | that one endpoint (the one-address view) |
| `acme.demo.*` | every endpoint in the group `demo` |
| `acme.*.bob` | every endpoint named `bob`, in any group |
| `acme.demo.b*` | `bob`, `bart`, ... in the group `demo` |
| `acme.release-*.deploy-*` | `acme.release-pipeline.deploy-coordinator` |
| `*.demo.*` or `zed.demo.alice` | refused: the owner is not `acme` (exit 2) |
| `acme.demo` | refused: it needs three parts (exit 2) |

A shell expands an unquoted `*` against the files in the folder, so the help
and every example quote the pattern: `mash endpoint status 'acme.demo.*'`.
(No `?`, no `[...]`, no regular expressions: one wildcard, so one sentence.)
The filters of `who` that are not a name stay as flags: `--online`,
`--offline`, `--role SUB`, `--label K[=V]`, `--cap C`, `--match TEXT`. `--owner`
and `--name` are removed: the pattern says both.

Columns: `ENDPOINT`, `STATE` (`online` or `offline`), `LAST SEEN`. `*` marks the
caller. The verdict line names the set and the counts, which says which
endpoints the default acted on (standard rule 8). Sorted by address, because
a person pastes the address and `grep` finds it. No `NEXT:` under a listing.
Exit 0 whenever the question was asked and answered, even if every endpoint is
offline: offline is a state, not an error. `--json` and the other exits are in
the end of this section.

All endpoints of your owner:

<!-- packet: endpoint-status -->
```
$ mash endpoint status
3 endpoints of acme: 1 online, 2 offline.

  ENDPOINT         STATE    LAST SEEN
  acme.demo.alice  online   now
* acme.demo.bob    offline  3h ago
  acme.lab.carol   offline  never

* you
online: a Claude Code session is serving that endpoint now
last seen: the last time this computer saw it answer; never: not seen yet
```

A pattern with matches:

<!-- packet: endpoint-status-pattern -->
```
$ mash endpoint status 'acme.demo.*'
2 endpoints match acme.demo.*: 1 online, 1 offline.

  ENDPOINT         STATE    LAST SEEN
  acme.demo.alice  online   now
* acme.demo.bob    offline  3h ago

* you
online: a Claude Code session is serving that endpoint now
last seen: the last time this computer saw it answer; never: not seen yet
```

A pattern with no match (exit 1; stderr). The fix is true: it is the command
that lists what exists:

<!-- packet: endpoint-status-nomatch -->
```
$ mash endpoint status 'acme.demo.z*'
error: no endpoint matches acme.demo.z*.
fix: see the endpoints of acme:
  mash endpoint status
```

An owner that is not yours, or a pattern that is not three parts (exit 2, a bad
argument; the fix names the corrected pattern, which mash can derive):

<!-- packet: endpoint-status-owner -->
```
$ mash endpoint status '*.demo.*'
error: the owner of a pattern must be acme, your owner: mash shows only
       the endpoints of acme.
fix: write the owner out:
  mash endpoint status 'acme.demo.*'
```

<!-- packet: endpoint-status-parts -->
```
$ mash endpoint status acme.demo
error: acme.demo is not an address or a pattern: it needs three parts.
fix: write owner.group.name, with * for a part you do not know:
  mash endpoint status 'acme.demo.*'
```

An address that is not registered (exit 1; stderr). It is a different case from
a pattern with no match: a pattern may legitimately match nothing, an address
names one thing that is not there:

<!-- packet: endpoint-status-unknown -->
```
$ mash endpoint status acme.demo.zed
error: acme.demo.zed is not an endpoint of acme.
fix: see the endpoints of its group:
  mash endpoint status 'acme.demo.*'
```

One address: a block. The facts are the ones a person can read about another
endpoint (section 2): online from the probe, its version from the answer, the
rest from the directory record. `host` is the machine an administrator says
the endpoint is meant to run on, not a live fact (question 5):

<!-- packet: endpoint-status-one -->
```
$ mash endpoint status acme.demo.alice
acme.demo.alice: online.

  endpoint   acme.demo.alice
  state      online
  last seen  now
  version    0.5.6
  program    claude-code
  host       myserver
```

An endpoint that never reported (registered, no answer ever seen from this
computer). Nothing is made up: unknown is `-`, and the verdict says what is
true:

<!-- packet: endpoint-status-never -->
```
$ mash endpoint status acme.lab.carol
acme.lab.carol: offline, never seen.

  endpoint   acme.lab.carol
  state      offline
  last seen  never
  version    -
  program    claude-code
  host       -
```

One address, `--detail` (more rows, never a different screen; the storage name
is shown only here, R3):

<!-- packet: endpoint-status-detail -->
```
$ mash endpoint status acme.demo.alice --detail
acme.demo.alice: online.

  endpoint   acme.demo.alice
  state      online
  last seen  now
  version    0.5.6
  program    claude-code
  host       myserver

  registered     2026-11-19 00:21 UTC
  role           reviewer
  description    reviews the release plan
  capabilities   review
  labels         team=demo
  storage name   acme_demo_alice
```

A list with `--wide`: more columns on the same row, which may pass 80 columns
(standard rule 28), never wrap:

<!-- packet: endpoint-status-wide -->
```
$ mash endpoint status --wide
3 endpoints of acme: 1 online, 2 offline.

  ENDPOINT         STATE    LAST SEEN  VERSION  PROGRAM      HOST      ROLE
  acme.demo.alice  online   now        0.5.6    claude-code  myserver  reviewer
* acme.demo.bob    offline  3h ago     -        claude-code  myserver  -
  acme.lab.carol   offline  never      -        claude-code  -         -

* you
online: a Claude Code session is serving that endpoint now
last seen: the last time this computer saw it answer; never: not seen yet
```

Many endpoints. Past 40 rows the table stops and says so; the verdict counts
all of them; `--all` shows every row, a pattern or `--online` narrows it (the
four rows shown stand for the first 40; the `...` stands for the rest, which
the real output prints):

<!-- packet: endpoint-status-many -->
```
$ mash endpoint status
112 endpoints of acme: 7 online, 105 offline. Showing the first 40.

  ENDPOINT                   STATE    LAST SEEN
  acme.build.runner-001      online   now
  acme.build.runner-002      offline  2d ago
  acme.build.runner-003      offline  never
  acme.build.runner-004      online   now
  ...

online: a Claude Code session is serving that endpoint now
last seen: the last time this computer saw it answer; never: not seen yet
To see more, narrow it with a pattern or --online, or use --all.
```

The real-name case (address kept whole, widest 54 columns):

<!-- packet: endpoint-status-long -->
```
$ mash endpoint status
2 endpoints of acme: 1 online, 1 offline.

  ENDPOINT                                  STATE    LAST SEEN
  acme.release-pipeline.build-watcher-prod  online   now
* acme.release-pipeline.deploy-coordinator  offline  3h ago

* you
online: a Claude Code session is serving that endpoint now
last seen: the last time this computer saw it answer; never: not seen yet
```

The server cannot be reached (exit 6, stderr: the whole answer depends on it).
The fix names the one command that shows the address and the cause:

<!-- packet: endpoint-status-unreachable -->
```
$ mash endpoint status
error: the server did not answer within 20s.
fix: check that the server is running and that "server" in
     ~/.config/mash/config.json is right. To see the address mash tried:
  mash server status
```

`--json` (one document, standard rule 10; the fields keep the names of the MCP
`who` result of the targets, section 6, plus the pattern):

```
{ "owner": "acme", "pattern": "acme.demo.*",
  "endpoints": [
    { "endpoint": "acme.demo.alice", "state": "online",
      "last_seen": "2026-11-19T09:42:11Z", "version": "0.5.6",
      "program": "claude-code", "host": "myserver", "role": "reviewer" },
    { "endpoint": "acme.demo.bob", "state": "offline",
      "last_seen": "2026-11-19T06:41:02Z", "version": null,
      "program": "claude-code", "host": "myserver", "role": null } ] }
```

Exit codes: 0 asked and answered; 1 an address or pattern with no endpoint
(its own words), a refused connection, trust files that disagree; 2 a
pattern that is not three parts, an owner that is not yours, an unknown flag;
4 the credentials were refused; 6 the server did not answer.

## 5. (c) "Other?": the questions none of the three answer, and the names

Every question a person plausibly has, the view that answers it, and whether
`mash who` and `mashctl endpoint list` already cover it or collide with it
(who = `mash who` of the targets, list = `mashctl endpoint list` of ctl D15):

| question | answered by | who | list |
|---|---|---|---|
| Does mail work from this machine? | `mash status` | no | no |
| Which endpoint am I? | `mash status` verdict; `*` row of `endpoint status` | `*` row | no |
| Is mail waiting, how much? | `mash status` row `inbox`; the table of `mash inbox` | no | `show` has `unread mail` for ONE endpoint (admin) |
| Is a Claude Code session serving me? | `mash status` row `session` | `online` of the `*` row only | `show` has `online` (admin) |
| Is my plugin installed, are its hooks set? | `mash status` warning, `--detail` row `claude code` | no | no |
| Is this machine's trust record fine? | `mash status` row `config`, `--detail` rows `trust` | no | no |
| Is the server up and encrypted? | `mash server status`; the `server` row of `mash status` (reachable only) | no | `mashctl status` shows it (admin) |
| Is the server full, how long is my mail kept? | `mash server status --detail` | no | `mashctl limits` (admin) |
| Who is online in my group? | `mash endpoint status 'acme.demo.*'` | YES: collides | no |
| Is Carol online, when last? | `mash endpoint status acme.lab.carol` | YES: collides | no |
| Which endpoints exist in my owner? | `mash endpoint status` | YES: collides | YES: overlaps (admin sees more) |
| Who can do a thing (role, label, capability)? | `mash endpoint status --role ... --cap ...` | YES (the same flags) | `show` only |
| Which version do my peers run? | `mash endpoint status --wide` | no | no |
| Who may I mail? | everyone registered in your owner; `mash send` refuses the rest. The token holds the exact list, no view reads it | no | `mashctl permissions` (admin) |
| Who is connected to the server, every inbox, the keys? | `mashctl` only (section 2) | no | `audit`, `issued` |

Three of the answers collide with `mash who`. One name per thing means `mash
who` goes. The alternatives:

| option | the set | for | against |
|---|---|---|---|
| A (recommended) | `mash who` is removed; `mash endpoint status` is the one live view of your owner's endpoints; `mashctl endpoint list` stays the administrator's registry (created, type, note, never-seen endpoints, every owner of the account) | one name per thing, noun-verb as the standard says, liveness is `status` and an inventory is `list`, as the standard's verb table defines them; the maintainer's own proposal | `who` is the word people and agents know; a hard cut, but the targets already hard-cut `create`, `--long`, `--names` |
| B | keep `mash who` as the peer list and add `endpoint status` for ONE address only | no rename; `who` stays short | two names for one set; `who` breaks standard rule 1 and 6; the maintainer's "taking a pattern" is lost |
| C | `mash endpoint list` (directory, no probe, fast) and `mash endpoint status` (probes); `who` removed | cheap inventory without a 0.3s probe; the standard's `list` and `status` verbs both used | a person rarely wants the list without liveness; two views of one set differ only by a probe; a third name for the same endpoints next to ctl's list |
| D | keep `mash who` only; no `endpoint status`; add `server status` | smallest change | the maintainer asked for endpoint status; `who` stays outside the noun-verb shape the standard fixes for the major revision; patterns need a new flag family |

Recommendation: A. What it makes of the three neighbours:

- `mash who` becomes an unknown command with an error that names its
  replacement (the targets already do this for removed verbs, section 5 "Unknown
  command", for `credential create`): `error: unknown command who.` `fix: the
  endpoints of acme, and who is online:` then `mash endpoint status`. No alias
  (standard rule 6). Its flags: `--online`, `--offline`, `--role`, `--label`,
  `--cap`, `--match` move over unchanged; `--name` and `--owner` become the
  pattern; `--long` is `--detail` for one address and `--wide` for the list
  (the targets' `--by-group` is dropped: a pattern `acme.demo.*` per group says
  it, and the table is one address per row).
- `mashctl endpoint list` is the registry: every endpoint the administrator
  made, with the date it was made, including those that never ran. It does not
  say who is online (that is `mash endpoint status`; ctl's `endpoint show`
  carries `online` and `unread mail` for one endpoint, which only an
  administrator can read: questions for ctl, section 6).
- The agent's MCP tool `who` is the same view. One name per thing says rename
  it `endpoint_status` in the same release (the result already renames its
  fields, targets section 6); question 2.

The set, as a table of things and names (the whole interface in six lines):

| the thing | the person's command | the administrator's |
|---|---|---|
| this endpoint, can it mail | `mash status` | (none: an endpoint is not the administrator's concern) |
| the server | `mash server status` | `mashctl server status` |
| the endpoints of my owner, live | `mash endpoint status [pattern]` | `mashctl endpoint show`, `list` (registry) |
| one endpoint, in full | `mash endpoint status <address>` | `mashctl endpoint show <address>` |
| mail | `mash send`, `inbox`, `read` | `mashctl inbox inspect` |
| the machine's trust | `mash trust reset` | `mashctl adopt` |

## 6. (d) Fit with the CLI standard; Agree with ctl

The standard was found (`~/ops/standards/cli/STANDARD.md`, version 1.2.6, read
in full; the targets cite its numbered rules as "cli rule N"). It is not in the
repository: there is no copy under `docs/`, and the targets quote it by rule
number. What I applied, and where I had to assume:

- Rule 1 and 2 (`<tool> <noun> <verb>`, one noun, singular): `mash server
  status` and `mash endpoint status` conform. `mash send`, `inbox`, `read`,
  `prompt`, `hook` do not, and the standard's own table says they move under
  nouns at mash's major revision (`mash message send`); the targets keep them
  for 0.5.6, and I assumed so too. The new commands are noun-verb now so the
  major revision renames nothing of this document.
- Rule 3 (top-level verbs): `mash status` is allowed only as "a verb that acts
  on that thing itself in a tool named for a thing" (the `range status`
  precedent: the range's own status). I ASSUMED mash is named for the thing it
  acts as, the endpoint. That is an assumption: if the planner reads rule 3
  more narrowly, the name for the readiness check is `mash doctor` (the table's
  `doctor`: "check this machine and say what is missing, with the command that
  fixes each"), which is almost the screen above. The maintainer, the README
  and the quick start say `mash status`; I keep it and ask (question 12).
- Rule 4 (the verb table): `status` is "one object's live state, or a summary
  of all of them with no name given". `mash endpoint status` with no name is
  exactly that summary; with an address it is the one object. `list` is "the
  objects of this noun, one per line": `mashctl endpoint list`.
- Rule 6 (no aliases): `mash who` is removed, not kept beside `endpoint
  status`.
- Rule 7 and 8 (the object's name is the positional; an optional name acts on
  what the context gives and says which): the pattern is the one positional;
  the verdict line says `3 endpoints of acme`.
- Rule 10 (`--json` on every read command): each view has it; the fields are
  the ones of the MCP tools (section 8, "Section 6 MCP").
- Rules 24 to 28: default answers only what was asked (six rows); `--detail`
  is more about the same thing, `--wide` is more columns of a list; no
  preamble; tables aligned with a header; 80 columns, with `--wide` exempt.
- Rule 14 (exit codes): 0, 1, 2, 3 cancelled, 4 not authorized, and the
  contract's 5 and 6; I use 0, 1, 2, 4, 6 as the output contract does.

### Agree with ctl

Not agreed with the mashctl lane; these are the questions I would put to them,
each with a proposal. (ctl D15 as cited: `status` 1458-1580, `endpoint list`
and `show` 2452-2656, `issued list` 4918-4975.)

1. Name. `mashctl status` is the administrator's view of the SERVER (what
   `config.json` and the admin credentials say about it; its own text says it
   "knows no endpoint"). Under rule 1 a top-level `status` needs "a tool named
   for a thing"; `mashctl` is not. Proposal: `mashctl server status`, the same
   noun as `mash server status`. Is that agreed?
2. One screen, two tools. Both show reachable, encrypted, certificate. Proposal:
   the row labels and values for those facts are written once (`address`,
   `state`, `encryption`) and used by both; ctl adds the administrator's rows
   (`signed in`, `prepared`, owner, the keys). Their first line is `OK` or
   `PROBLEM: ...` where mine is `The server is reachable and encrypted.`:
   proposal, both use a sentence for the verdict. And rows: ctl prints
   `label:` with a colon, mash prints the label alone (13 screens of the
   targets); proposal, one style in both tools, without the colon. Agreed?
3. `endpoint list` against `mash endpoint status`. Does `list` include
   endpoints that never ran (it reads the registry, so it should), and does it
   show every owner of the account or only the configured owner? My proposal:
   registry, every endpoint, never-seen included, no liveness column (the one
   live view is `mash endpoint status`).
4. The same patterns. Proposal: `mashctl endpoint list [<pattern>]` takes the
   same three-part pattern, the same one wildcard. The administrator may write
   any owner (the account holds several), so the owner part may be a literal
   other owner, or `*`: that is the one place the two tools differ, and the
   help says so. Agreed?
5. `HOST`. In `list --wide` it is the directory's `host` (the machine the
   endpoint is meant to run on, set by `mashctl endpoint set --host`): ctl D15
   shows `-` for bob until it is set. In `mash endpoint status --wide` it is
   the same field. Proposal: both tools call it `host`, and neither says `runs
   on` (the targets' `who --detail` says `runs on`, which is wrong for any
   endpoint but your own). Agreed?
6. `unread mail` (ctl `show`) against `new` (the targets' `inbox`, status
   `inbox` row). Proposal: `new` in both. And where does ctl read `online:` for
   an endpoint (an administrator may ping `$SYS`, an endpoint may not): by the
   same `$SRV.INFO` probe? If so say `online`/`offline` in both tools, the
   same two words.
7. Flags. ctl uses `--detail` on `status` and `show` and `--wide` on lists
   (standard rule 24). The targets' `who` used `--detail` blocks for a list.
   Proposal: lists take `--wide` (more columns, one row each, may pass 80),
   single-object views take `--detail`; that is what `mash endpoint status`
   does above.
8. Exit codes for `server status`: ctl says 0 OK, 1 PROBLEM, 6 NOT REACHABLE.
   Mine are the same, plus 4 when the credentials are refused (the storage
   rows then say why). Agreed?

## 7. Open questions for the maintainer

1. The set of names. Recommendation: A of section 5: `mash status`, `mash
   server status`, `mash endpoint status [pattern]`, `mash who` removed with no
   alias, `mashctl endpoint list` kept as the registry. The "Other?" of the
   comment is answered by the table in section 5: the nearest thing not covered
   is "who may I mail", which has no view and needs none in 0.5.6.
2. The agent's `who` tool. Recommendation: rename it `endpoint_status` in 0.5.6
   (one name per thing, the JSON already changes; the plugin's instructions
   are regenerated). The alternative is to keep `who` for agents only, which
   leaves two names for one view for the life of the tool.
3. The server's address in `mash status --detail`. Recommendation: yes, one
   fact row (`server  tls://myserver:4222`), because `--detail` already says
   where everything is (the config file, the CA, the credentials file). The
   default screen has none, as you said. If you want it nowhere in `mash
   status`, `mash server status` is the only place and the `--detail` row goes.
4. `LAST SEEN`. It has no source in 0.5.5 (section 3): the time is in one
   process's memory. Options: (a) a record in this machine's state directory,
   written by every `mash endpoint status`, `serve` and `send` that sees an
   answer: no server change, no new permission, honest label "the last time
   THIS computer saw it answer" (as N10's `last reached`); (b) a record the
   endpoint writes for its owner to read: it needs a new KV bucket and new
   permissions, which is a protocol and permission change that D229 rules out
   without Randy; (c) drop the column and show `online`/`offline` only.
   Recommendation: (a) now, and say so in the footer; (c) if you want nothing
   that can be wrong. Not (b) in 0.5.6.
5. `host`. It is the directory's field, set by an administrator, the machine an
   endpoint is MEANT to run on (`store.go:233-235`), not where it runs. Only
   your own endpoint's live host is readable (`serve.go:914-922`).
   Recommendation:
   show it as `host` in `--wide` and one-address views, never `runs on`, and
   show `session ... on myserver` (live) only for your own endpoint in `mash
   status --detail`.
6. Patterns. Recommendation: the owner must be literal and yours; `*` inside a
   group or name part; no `?`; the pattern is quoted in every example. The
   alternative is a `--group demo` flag (standard rule 7: scope as a flag),
   which is simpler to type and to say but cannot say `acme.*.bob`.
7. The table limit. Recommendation: 40 rows by default, sorted by address,
   with `--all`; the verdict line counts everything. Online-first sorting
   is the alternative, with the cost that the order changes between runs.
8. `mash server status` shows the ACCOUNT's storage and stream totals, which
   every owner in the account shares (ADR 0006). Recommendation: show them, as
   `mail storage` and `streams` (`--detail`), because a full account is the
   one server problem a person can hit; the label says nothing about owners.
9. Offline exits 0. `mash endpoint status acme.lab.carol` for an offline
   endpoint is exit 0 (the question was answered), as `status` of a healthy
   screen with a warning is 0. A script that wants a gate uses `--json`.
   Recommendation: keep 0. The alternative (1 when the one asked-for endpoint
   is offline) turns an answer into a failure.
10. The `inbox` and `session` rows add two lookups to `mash status` (own
    inbox consumer, own lease) on the connection that already signs in. They
    are `not checked` whenever the sign-in did not happen. The `new` count is
    only as good as `mash inbox`'s own definition of `new` (reading a message
    does not ack it, `waking.go:16-30`): the same count must be used, so the
    inbox contract decides it. Recommendation: add both; the maintainer asked
    "is mail waiting" and "can you see heartbeat" in one breath.
11. The certificate's end date. Recommendation: show it in `mash server
    status` and warn within 30 days; mash reads no certificate field today
    (section 2), so it is new code but no new permission.
12. Rule 3 of the standard and `mash status` (section 6). Recommendation: keep
    `mash status`, record the reading of rule 3 in the contract; if the planner
    refuses it the name is `mash doctor`, and `status` would then mean only
    `mash endpoint status <own address>`.
13. The server address in errors. Recommendation: a fix may name the address
    (`check that the server at tls://myserver:4222 is running`), because it is
    the thing to check; no other line of `mash status` does.

## 8. What this changes in the targets doc

Not edited. The sections and blocks that would change:

- Rule R11 (lines 158-210): the default screen is the verdict and SIX rows
  (`version`, `server`, `config`, `credentials`, `inbox`, `session`); the
  server row is one word (`reachable`, `reachable, NOT ENCRYPTED`, ...), no
  address, no time; the two and three line forms, the `checked 09:42` and
  `last reached` texts and the env override lines in the row go; the override
  moves into the `warning:`/`fix:` text. `--detail`: the seven rows of REQ-012
  stay, the `group` fact leaves, a `server` (address) and a `session` fact
  are added.
- R14 (lines 234-244) and R3 (94-105): add `endpoint status` columns and the
  pattern; "the address is the dotted path" gains the pattern form (the one
  wildcard).
- R20 (317-329): "one place keeps naming the server ... the rows of `mash
  status`" (N19) is now `mash server status`.
- Section 5 `mash status` (1508-2186): the lead paragraph; every block
  (healthy, detail, Claude, no configuration, none chosen, none installed,
  credentials missing, unsafe, unreachable, refused, deleted, differs, plain
  with the override, override with a tls address, plain refused, environment
  overrides, unknown flag) takes the six rows; the `--detail` blocks add the
  `server` and `session` fact rows; "Changes (status)" (2170-2186) is
  rewritten.
- Section 5 `mash who` (2649-2767): replaced by `mash endpoint status` (the
  blocks of section 4.3 here), with `mash who` in "Unknown command" (3394-)
  naming it; N3 (`--by-group`, `mash who <address>`) is closed by the pattern;
  N13 is answered (the directory holds every owner; presence is owner-only).
- New sections 5 `mash server status` and `mash endpoint status` (sections 4.2
  and 4.3 here).
- Help (3234-3300): `who` leaves "Mail"; a `Server` and `Endpoints` group, or
  `endpoint status`/`server status` lines, appear; `status` stays under "Set
  up".
- Section 4 (key and credential flow) and the READMEs (`docs/plans/
  2026-11-19-readme-quickstart-*.md`, `...-day2-plain-mash.md`): every healthy
  `mash status` they quote ("this is how a healthy `mash status` looks") takes
  the six rows.
- Section 6 MCP: `who` becomes `endpoint_status` (question 2) with the pattern
  argument, `last_seen` documented as local; `status` takes the six rows
  (`server.address`, `server.checked_at`, `server.last_reached` leave the
  default JSON; `inbox: {new, urgent}` and `session: {serving, since}` are
  new); new tool `server_status`; the initialize `instructions` text (4316-)
  names the new tools.
- Section 7: N10 (`last reached`) closes for `mash status` and moves to the
  LAST SEEN record (question 4); N13 closes; new N for the pattern and the
  limit. Section 8 (behaviour changes): `who` removed, `status` reads the
  inbox and the lease, two new commands, a local last-seen record. The
  contract's REQ-012 (the seven detail rows) needs a note that two fact rows
  are added.
- Section 2 (exit codes): the table gains the exit-code row of `server status`
  and `endpoint status` (section 4.3 of this document).
- Section 9 (checks) and the dispositions file: rows for D1 (the server row
  forms) and L5.

## 9. What I could not verify, and checks run

What I could not verify:

- Another owner's agents cannot answer an endpoint's probe. It follows from
  the permission list (`permissions.go:19-24,63`) and the comment on it; I
  tested only one owner, so the claim for two owners is a reading, not a run.
  That `mash who` lists other owners' records is a reading of `directory.go:
  89-103` and `client.go:234-258`, also not run.
- `reserved_storage` (125MB in the run): I show it as a number the server
  reports and did not establish what counts toward it; the detail row's
  wording may need to change.
- The certificate fields (end date, issuer, the cause of a refusal): my run
  used the plain test server (`tlsreq: false`), so no certificate was read. Go's
  API gives them; mash reads none today.
- The `new` count and the `session` row. The inbox consumer and the own lease
  are readable (the consumer info answered live; the lease read is
  `OwnLease`, `directory.go:138-148`). I did not run `mash inbox`'s own
  `new` definition (it is a target, not in 0.5.5), and I did not check that
  a one-shot CLI can read the lease fresh enough: `ClientOptions.Lease`
  (`client.go:49-50`) exists for the launch path.
- A multi-server cluster: ADR 0066 measured three servers, the maintainer says
  one. `mash server status` talks to the server it connects to; a cluster would
  need a sentence about replicas (stream info has `cluster.leader`).
- Whether a `listset` policy (`internal/listset/compile.go`) narrows who an
  endpoint may mail beyond its owner; the permission set alone says "any
  endpoint of the same owner".
- How ctl reads `online` and `unread mail` for another endpoint: ctl D15 does
  not say.
- The in-session MCP status: not run; the six facts are the CLI's.
- The test server's numbers (7 streams of 50, 256MB, 64MB memory) are the
  repository's test fixture, not a production server's.
- The cast: every output in this document is a target, not output of a binary.

Checks run: the facts of section 2 were obtained by a throwaway test against
the in-process test server in a scratch clone of v0.5.5 (deleted; the clone
unchanged: `git status` clean); the denied subjects were observed as timeouts,
the granted ones as replies. The fenced blocks were measured with a script
(Python 3, `-I`, characters per line of every fenced block, `$`-blocks and the
JSON block included):

29 fenced blocks (the `$` blocks and the one JSON block), none over 80
columns; the widest is 79 (the `--wide` table of section 4.3 and the Claude
Code fix line of `status-claude`). Prose lines outside tables: none over
80. Tables in this file are not measured (a markdown table row cannot wrap).
Not measured, by the same rule as the targets: nothing exempt is used here.

## Appendix: the CLI team's answers, quoted unchanged

Received after the sections above were written. The "Agree with ctl" section
above lists my questions; these are ctl's answers (mail of 2026-11-19, 09:03 and
09:04Z). Where they differ from my proposal, the page above is not yet updated.

> An endpoint's own credentials may (a) read the WHOLE directory bucket (stream
> info + direct get on KV_mash-directory: every record, all owners), (b)
> subscribe to heartbeats of its OWN owner, (c) read its own inbox and its own
> lease key only. They may NOT read other endpoints' inboxes or lease keys, the
> issue/retired/adoption buckets, account/server stats, or the system account.
> `mash endpoint status [path|pattern]` CAN run on an endpoint's own
> credentials. It CANNOT show mail stored / unread / delivery state of others:
> that needs inbox-stream info, admin only.
>
> NAMES: `mash status` = my endpoint's health. `mash server status` = the server
> as my credentials see it (reachable, encrypted, which server, certificate
> checked; no counts). `mash endpoint status [path|pattern]` = the owner's
> endpoints with heartbeat. `mashctl status` stays = how mashctl itself is set
> up (it is about the operator's tool, never about endpoints, so no overlap).
> `mashctl endpoint list` = the directory as the operator sees it (OWNER GROUP
> ENDPOINT ADDRESS CREATED(UTC); `--wide` adds TYPE, HOST, NOTE); it does not
> show online or mail state. `mash endpoint status` shows the same four leading
> columns. Operator-only mail state stays in `mashctl audit`; 'online' is
> dropped from audit because `mash endpoint status` owns it. `mash who` already
> lists endpoints and presence: fold it into `mash endpoint status` (hard cut:
> remove it; ask the maintainer).
>
> (1) `mashctl status` rows: default: verdict, server (reachable, encrypted),
> owner, signed in (as admin). `--detail` adds certificate (CA file path),
> account, prepared, config file, key directory, admin credentials file, system
> credentials file, operator and account key ids. (2) `mash server status` =
> what ordinary endpoint credentials can read about the server: reachable,
> encrypted, certificate checked against the CA, server address, round-trip
> time, JetStream available, whether the directory is readable. `mashctl
> status` = how the operator's tool is set up and whether it can administer;
> never repeats server facts except the one reachable/encrypted line needed to
> explain a failure. (3) `mashctl endpoint list` STAYS as the operator's
> directory list, not renamed to `endpoint status` (no liveness). (4) Patterns:
> `acme.demo.*` and `*.*.bob`; `*` matches exactly one dotted part; a pattern
> with other than three parts is an error; `mashctl` accepts patterns in
> `endpoint list` and `endpoint show` only for one address; `mash endpoint
> status` never crosses owners (a different owner in the pattern is refused:
> `error: you can only see endpoints of owner acme`).

Difference to resolve with ctl: they keep the name `mashctl status`; this page
proposes `mashctl server status`. And rev 15 REQ-015 adds a MAILBOX row to
`mash status` (`<n> waiting`, or `OVER THE LIMIT OF 15`): the `inbox` row of
section 1 is the same row, to be renamed `mailbox` and given the limit wording
in the next draft.
