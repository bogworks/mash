# mash status views, DRAFT5c

Date: 2026-11-19. Status: DRAFT5c, paper design only (nothing is built, no
product code; the targets document and the READMEs are edited by others). It
answers Randy's comment on the noon page: "I see no reason to put the mash
server details in the mash status. There's only one server in your mash and if
you don't know what it is, it's a problem." He proposes `mash status` (my local
agent endpoint), `mash server status` (my mash server) and `mash endpoint
status` (all endpoints by my owner, taking an endpoint path and simple
regexes), and asks: "Other?", "can you see heartbeat status?" and "What does
mash report via SAP status, and should it be in mash status?".

Who is who in this page: Randy is the maintainer (both words mean him). The
lead is the lead of the mash team, who rules on the contract. The planner is
the master planner, who checks pages like this one. "ctl" is the team that
builds `mashctl`, the administrator's tool. The critic and the junior are two
independent readers of earlier drafts (section 10).

Two things for Randy, first, because they change what he asked for:

1. HEARTBEAT, LAST SEEN. A heartbeat is the short "I am alive" signal a running
   session sends every 30 seconds. Nothing stores it, so mash cannot say when
   another endpoint was last alive. You asked for a more detailed endpoint
   list; what mash can truthfully show about another endpoint today is whether
   it answers. Version, role and where it is meant to run are under --wide
   (the group is in the address). More than that (last alive, mail waiting)
   needs heartbeats stored or decision 249. Do you want that built (new), or
   is "answers now / does not answer" enough for 0.5.6? This page shows no
   "last seen" anywhere by default; `--detail` of one endpoint says plainly
   that nothing keeps the history (section 3, section 4.3).
2. PATTERNS, RULED (the planner's ruling; ctl mirrors it). You asked for
   simple regexes; the grammar is one wildcard, `*`, because it is one thing
   to learn, it needs no escaping, and it is safe in a shell and in the
   directory's own subjects (where `.` and `*` already mean something and a
   regex's `.` would not). The common case needs no wildcard at all:
   `acme.demo` means "everything in demo". Wherever a pattern holds `*` the
   help and every example quote it, because zsh (your shell) stops with "no
   matches found" before mash runs. The grammar is written once, in section
   4.3.

The view set (the lead accepted it as the proposal; Randy has not ruled on it
yet): `mash status` (this endpoint), `mash server status` (the server) and
`mash endpoint status [<address or pattern>]`, which REPLACES `mash who` with a
hard cut and no alias.

Decision 249 is the lead's open decision about the SENDER's side of mail: did
my message arrive, did the session wake. This page does not answer it; section
5 says what a sender can see today, and what only decision 249 can add.

DRAFT5c (the age trigger; Randy said yes at 11:30Z, "configurable but defaults
to like 1 hour", and the contract, rev 26, REQ-015 now has it). The mailbox
wakes the session, and shows a warning, when the OLDEST waiting message is
older than `mailbox_age` (default 1 hour, 0 = off) as well as when more than
`mailbox_limit` wait: one new screen, `status-mailbox-age-over`; a second line
under `mailbox limit` in `--detail`; the age predicate and its build plan in
4.4; the MCP `mailbox` object is `{waiting, limit, oldest_age, age_limit,
over_limit, over_age_limit, woke_at}`. Nothing else changes.

DRAFT5b (the lead's four rulings on DRAFT5, same day). (1) `mash read` does
NOT acknowledge directly in 0.5.6: it keeps the tombstone, so with no session
running the count drops when the next session starts; every screen and sentence
says so (4.4 item 2 is now "not in 0.5.6"). (2) Problem values stay in capitals,
the contract's `OVER THE LIMIT OF 15` included; rule 5 is reversed. (3) The
one-address view of your own endpoint shows the mailbox row: the contract says
so now, and section 8 no longer flags it. (4) What could not be determined is
a list of build-time checks, "Verify at build", in the targets' decisions file;
section 9 points to it.

What DRAFT5 changes, in order (the lead's items a to f and rulings 1 to 8, the
planner's check of DRAFT4 and the junior's read; each is a row of section 10):

- One source for the mailbox count: the consumer's own number, stated once in
  4.4. `mash read` marks the message read here (the drained tombstone); the
  count drops within 250 ms of a running session's serve, or when the next
  session starts. A direct acknowledgement is not in 0.5.6 (4.4, item 2).
- REQ-015 is stated once, in 4.3 (the contract, rev 26, already says it);
  section 8's "open difference" is deleted.
- The over-the-limit screens carry a concrete action (read the mail yourself;
  look at the session's terminal), for a running and for an absent session; the
  mailbox row carries the time of the wake; "limit" is explained on every screen
  that shows it as the number that triggers a wake; "digest" is gone from the
  screens ("a one-line summary of the message").
- Words are explained where they appear: wake, heartbeat, CA, token, account
  key, channel, `nats://`.
- `host` is `meant to run on`, with one explanatory line; a sender's screen
  says what mash cannot know.
- The endpoint status help explains every flag and has `--detail`; `online` and
  `offline` are defined once, and `mash status --detail` points to it.
- `mash status` with the server unreachable prints no address, and points to
  `mash server status`.
- The env-override verdict is `not checked` and its fix says the setting is
  IGNORED and how to remove it.
- The pattern grammar is ruled and stated once; a long list ends with a labelled
  `next:`; the printed blocks are all at most 80 columns; the broken tables,
  stray indents and the contradictions found by the planner and the junior are
  mended.

Earlier drafts, in one paragraph: DRAFT4 (the lead's contract rev 21 ruling)
made the mailbox count unacknowledged mail with delivery acknowledging; DRAFT3
made one word and one source for "is a session there" (`online`, from the
presence probe), dropped every remembered "last seen", the `MAILBOX` column of
the list and the legend under a list, set one 2 s timeout and one rule for
`MASH_SERVER`, and used the plain-text override
`MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION` (REQ-010).

The fixed-rows idea of the status screen stays (R11 of
`docs/plans/2026-11-19-cli-targets-mash.md`, DRAFT11); the rows change.

Sources. The v0.5.5 source at tag v0.5.5 (cited `file:line`, paths relative to
the repository root), run for the permission facts (section 2: an in-process
test server with the production permission set, a throwaway test that was
deleted afterwards). The contract, rev 26, the final one (cited "contract",
REQ-004, REQ-010, REQ-012, REQ-015), the targets (cited "targets", DRAFT11),
the catalog of today's output
(`docs/plans/2026-11-19-cli-output-catalog-mash.md`), the mashctl lane's
DRAFT15 (cited "ctl D15"), the CLI standard 1.2.6
(`~/ops/standards/cli/STANDARD.md`, cited "standard"), ADR 0041, 0046, 0066 and
0069 (`docs/decisions/`), and the independent reads of earlier drafts (section
10). Codes: R-numbers and N-numbers are the targets' rules and notes, REQ-nnn
and AT-nnn the contract's requirements and acceptance tests, D229 decision 229
(stance belongs in mash: no SAP protocol change, no new permission without
Randy), and "decision 249" the lead's open decision about mail that waits in an
idle mailbox. References inside this page: "top item N" is one of the two
things above; "7.N" is item N of section 7.

Every fenced block that starts with `$` is a TARGET (what a person should see),
in the style of the targets: owner `acme`, group `demo`, endpoints
`acme.demo.alice`, `acme.demo.bob` (the person at the keyboard) and
`acme.lab.carol`. One-machine blocks use `tls://localhost:4222` and host
`localhost`, as the targets do; a reader with the server on another machine
reads that machine's name. In every block alice and bob have a Claude Code
session running and carol has none, except `status-offline` and
`status-mailbox-over-offline`, where bob has none. Section 9 measures every
fenced block.

## Contents

0. In short
1. Tone: what a status screen is for
2. What an endpoint may read (the facts every view stands on)
3. What mash reports through SAP status today, field by field
4. The three views: 4.1 `mash status`; 4.2 `mash server status`; 4.3 `mash
   endpoint status`; 4.4 The mailbox count: one source, and delivery
   acknowledges
5. "Other?": the questions none of the three answer, and the names
6. Fit with the CLI standard; Agree with ctl
7. Open questions for Randy
8. What this changes in the targets doc and the contract
9. What I could not verify, and checks run
10. Disposition of the findings (the critic's, the junior's, the planner's, the
    lead's)

## 0. In short

The recommended view set (one name per thing):

- `mash status`: this endpoint only. Verdict, then six fixed rows: version,
  server (reachable or not, no address), config, credentials, mailbox (how many
  messages are waiting: not yet acknowledged), session (`online` or
  `offline`).
- `mash server status`: the server. Verdict, then four fixed rows: address,
  state, encryption, mail storage; `--detail` adds the rest. Only what an
  ordinary endpoint's credentials may read; the rest is `mashctl`.
- `mash endpoint status [<address or pattern>]`: the endpoints of your owner:
  `online` or `offline` now, and nothing remembered. `--wide` adds version,
  where it is meant to run, and role; one address gives a block. It REPLACES
  `mash who` (hard cut, no alias).
- `mashctl endpoint list` stays the administrator's registry (created, type,
  note, never-seen endpoints included). `mashctl status` stays (the operator's
  tool: admin sign-in, shared storage, key directory, config paths).
- Heartbeat and SAP status stay internal. A person sees conclusions (`online`,
  `offline`), never the payload, and no history.

His two direct questions, plainly (evidence in section 3):

Can you see heartbeat status? There is a heartbeat: each running session
publishes a signed one every 30 seconds (`internal/endpoint/service.go:67,
201-219, 274-287`), but since ADR 0066 nothing in mash listens to it, and
nothing stores it. What replaced it is a probe made when someone asks
(`client.go:474-500`): "online" means "answered the probe just now". So you can
see online now; you cannot see "heartbeat status", and you cannot see when an
endpoint was last alive. That is the first question at the top.

What does mash report via SAP status, and should it be in `mash status`? The
`status` verb (`agents.status.<owner>.<group>.<name>`) answers with the same
eight-field payload as the heartbeat, which has no health data at all: no host,
no version, no mail count (`service.go:179-199, 260-266`); and no mash command
asks it. It should NOT be in `mash status`: it says only "I answered", which
`mash status` shows as the `session` row and `mash endpoint status` as
`online`.

What the targets got wrong, found while reading (details in sections 3 and 7):
the `LAST SEEN 3h ago` of `mash who` cannot be produced by 0.5.5 (the time is
kept in the memory of one process and is set only for an endpoint that answered
a probe, `directory.go:25,79,94`); the claim that `mash who` shows only your
owner's endpoints (N13) is wrong (`directory.go:89-103` filters nothing,
presence is the part that is owner-only, `directory.go:64`); `runs on` in `who
--detail` is the directory's `host` field, where an administrator says the
endpoint is meant to run, not a live fact for any endpoint but your own
(`cmd/mash/serve.go:914-922`); and `mash read` does not mark a message read
today (`cmd/mash/cli.go:134-148`), which the targets' `state` row assumes
(section 4.4).

## 1. Tone: what a status screen is for

The maintainer's comment is the whole rule: show a person only what they cannot
be expected to know, and say it in the words of their own work. Rules for the
three views, each one a question the screen must pass:

1. The first line is the answer, in a sentence, about the thing asked, and it
   is true as written. `acme.demo.bob: mail works.` If mash did not check, it
   says `not checked`, never `does NOT work`.
2. Ask about one thing, answer about that thing. `mash status` is about this
   endpoint; it does not report the server's address, the group, or anyone
   else's state. A neighbour's facts have their own command.
3. Rows are fixed. The same rows in the same order in every state, so a
   person can compare two screens. A row that cannot be checked says `not
   checked`, with the reason in brackets when the reason is not obvious.
4. A row holds a conclusion, not a measurement. `reachable`, not `answered in
   41ms` (that is behind `--detail` or on `mash server status`).
5. A problem value is in capitals (REQ-004), and only a problem. `NOT
   REACHABLE`, `REFUSED`, `NOT ENCRYPTED`, `NOT TRUSTED`, `OVER THE LIMIT OF
   15`. Offline is not a problem: an endpoint that nobody is running is
   normal, and shown in lower case, with exit 0. A mailbox over its limit is a
   problem value in the row, a warning (exit 0), explained in a `warning:`
   block.
6. Nothing a person is expected to know is printed. The server address, the
   endpoint's own address (named once, in the verdict). `mash status` prints no
   server address: `mash server status` is the one place that shows it, and
   every failure points there. The two override screens name the value that
   overrides, because that is the thing to check.
7. One thing, one name, one form, one source: the dotted address; `online` and
   `offline` (a session answered the probe just now, or not); `waiting` (mail
   not yet acknowledged). No second word for the same thing (`serving`,
   `unread`, `alive`, `lease`).
8. A fix is a true next step and a command stands alone (R4, R9, R10). `next:`
   labels a step that is not a fix (a list cut short, a step no command can
   do); `note:` labels a plain fact that stops a screen from misleading. A
   screen that worked ends at its rows, and a list has no legend under it:
   definitions live in `mash help <command>` and in the one-address view.
9. One alignment per screen: every value starts in the same column, at least
   two spaces after the longest label of that screen.

Two numbers every view shares, stated once:

- THE TIMEOUT. Every status view waits at most 2 s for the server to answer a
  connection, and says `the server did not answer within 2s` (the same words,
  in all three). The contract bounds the new `mash status` connect at 2 s
  (contract, section 5 "Security, reliability, compatibility, performance").
  0.5.5's own wait is 20 s (`DefaultOpenTimeout`,
  `internal/config/config.go:26`), which is too long for a screen whose job is
  a quick answer; the status views need a shorter bound of their own. The 2 s
  of `client.go:26-32` (`PresenceCap`) is a different thing: how long the
  "who is online" probe waits for answers; it is never printed.
- TIMES. A time on a default screen is in the computer's own time zone, as
  `09:40` for today and `yesterday 23:10` or `2026-11-17 14:02` before that;
  `--detail` prints the full stamp with its offset, `2026-11-19 09:40 +00:00`.

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
  message, the number of registered endpoints, their own inbox's counts, and
  who answers now in their OWN owner.
- A person CANNOT see: the server's connections, uptime, load or logs
  (`$SYS`); any other inbox; the list of streams; who is online in ANOTHER
  owner. For the last: an endpoint publishes replies only into
  `_INBOX.<its owner>.>` (`permissions.go:19-24,63`), so another owner's agents
  cannot answer it, and `applyPresence` drops any answer whose metadata owner
  is not the caller's (`directory.go:64`). That is a fact, not a rule mash
  chose: "an address pattern never crosses owners" (section 4.3) is the only
  honest rule.
- All of that beyond a person's rights is `mashctl` (it signs in as the
  administrator; ctl D15 `status`, `audit`, `issued`, `limits`, `permissions`).

## 3. What mash reports through SAP status today, field by field

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
| `agent` = `claude-code` | `service.go:183-199`, `sap/subjects.go:8` | nothing: it is the same on every row | `--json` only |
| `owner` | `service.go:183-199` | nothing: it is in the address | nowhere |
| `session` (the storage name `acme_demo_bob`) | `service.go:183-199` (`e.key`) | nothing (R3: storage name only behind `--detail`) | one-address `--detail` |
| `instance_id` (the service's random id) | `service.go:183-199`, `Instance()` `:166-172` | nothing: it changes with every serve | nowhere |
| `ts` (the second it was made) | `service.go:183-199` | nothing: it is "now" | nowhere (internal) |
| `interval_s` = 30 | `service.go:67,189` | nothing | nowhere (internal) |
| `protocol_version` = 0.3 | `sap/subjects.go:9` | nothing a newcomer can act on | nowhere (internal) |
| `endpoints.*` (subjects, `max_payload`, `attachments_ok`, `min_sender_trust`, `durable`) | `service.go:174-199` | nothing; a live prompt that is too long is refused with its own error | nowhere |
| signature header on the heartbeat | `service.go:208-216` | nothing | nowhere |
| `$SRV.INFO` `version` (the answering mash) | `service.go:230-231`, `CurrentVersion()` | the version a peer runs, for an endpoint that answers | `--wide` `VERSION`, one-address view |
| `$SRV.INFO` `description`, `name` | `service.go:230,232` | nothing | nowhere |
| `$SRV.INFO` metadata `agent`, `owner`, `group`, `instance` | `service.go:233-238` | the address | the address column |
| `$SRV.INFO` metadata `user_nkey`, `account`, `id_sig`, `mail_subject` | `service.go:233-238` | nothing (the key is checked against the directory, then dropped) | nowhere (internal; `directory.go:54-85`) |
| online (the probe) | `client.go:474-500`, `directory.go:54-103` | `online` or `offline` | `STATE` of the list, the one-address block; the `session` row of `mash status` |
| last seen | `directory.go:25,79,94` (memory of one process) | NOTHING: no source in 0.5.5 and none proposed (first question at the top) | one-address `--detail` says "not recorded" |
| directory record: owner, group, instance | `store/store.go:223-228` | the address | address column |
| directory record: `agent` | `store.go:227` | nothing | `--json` |
| directory record: account, user, mail_subject | `store.go:228-230` | nothing | nowhere (internal) |
| directory record: created | `store.go:231` | when it was registered | one-address `--detail` (`registered`) |
| directory record: note, role, host, description, caps, labels | `store.go:232-242` | what it is for; where it is meant to run (`host`) | `--wide` / `--detail`; `--role`, `--label`, `--cap`, `--match` filters |
| own lease: `host`, `since`, `version`, `commit`, `pid`, `machine` | `endpoint.go:147-168` | when and where this session started | `mash status --detail` row `session started` |
| own lease: `mcp_seen`, `launch_token`, `ppid`, `session` | `endpoint.go:155-171` | nothing | nowhere (internal, takeover logic) |
| own lease: `waking_pending` + `_at` | `endpoint.go:172-178`, `waking.go` | nothing: it counts mail that should WAKE, not the backlog | nowhere; section 4.4 adds the wake time `backlog_woke_at` beside it |
| own inbox: counts, fill, evictions | `eviction.go:22-40` | a warning when mail was lost or the inbox is nearly full | `warning:` of `mash status` |
| clock offset | `endpoint.go:855-868`, `status.go:325-345` | only when it is wrong (beyond 10s) | a `warning:` of `mash server status` |
| server `max_payload` | `mail.go:715,798` | the largest message | `mash server status --detail` |

What `mash who` uses today (`cmd/mash/cli.go:166-179`, `clientNeeds`
`:211-213`): the whole directory (`FullDirectory`, every owner), a presence
probe (`Refresh`), and with `--long` the caller's own lease only (`liveLeases`,
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
`directory.go:17-19`). The probe window closes 300ms after the last answer, or
at 2s when nobody answers (`PresenceQuiet`, `PresenceCap`, `client.go:26-32`);
ADR 0066 measured about 312ms. The cost of the cap: an endpoint that answers
after the quiet window shows offline for that ask (ADR 0066, Consequences).
`mash status` and `mash endpoint status` therefore cost about 0.3 s more when
anyone answers and up to 2 s more when nobody does. If a request addressed to
the one endpoint (`agents.status.<owner>.<group>.<name>`, which a serving
endpoint answers, `service.go:260-266`) fails at once with "no responders" when
nobody serves, a one-address check would cost milliseconds instead of 2 s; I did
not run that (section 9).

Is the time of the last heartbeat or answer kept? No. `lastSeen` lives in the
process's memory (`endpoint.go:100`, set at `directory.go:79`), so a one-shot
command knows a last-seen time only for the endpoints that answered it, and
that time is "now". The target `LAST SEEN 3h ago` (targets, `who`) has no
source in 0.5.5, and a record kept on this machine would measure how long
since you last asked, not when the endpoint went away, so none is proposed.
Building a real history is the first question at the top.

Should the SAP status payload be in `mash status`? No. Every field is either
internal (instance id, timestamp, interval, subjects, protocol) or a limit of
the live-prompt path nobody asked about. What a person asks, "is a session
there, is it reachable", is a conclusion, and the screens show it as one.

## 4. The three views

### 4.1 `mash status`: this endpoint only

The question: "can this endpoint send and receive mail, and is anything
waiting?" It names no other endpoint and no server address. The rows are the
fixed rows of REQ-004: verdict, version, server (no address), config,
credentials and the mailbox (REQ-015); `session` is the one row added here.

The six fixed rows, in this order, in every state, each with its reason:

| row | why it is there, in one line |
|---|---|
| `version` | which mash this is: the first thing anyone asks |
| `server` | can this machine reach the server, encrypted and trusted; the one outside fact mail needs; NO address |
| `config` | is there a configuration mash can use |
| `credentials` | is this endpoint's own file there, private, and accepted by the server |
| `mailbox` | how many messages are waiting (section 4.4), against the limit of REQ-015 |
| `session` | is a Claude Code session there: it says why mail, or the backlog wake of REQ-015, did or did not reach a session on its own |

Not rows: the endpoint (it is in the verdict and in `--detail`), how it was
chosen (`--detail`), the server's address and any other server fact (`mash
server status`), the group and owner counts, heartbeat, hooks, clock, relay
trace (`--detail` or internal). The group and owner counts are about other
endpoints: they are the verdict line of `mash endpoint status`, and Randy's own
condition, "if that takes too long, nevermind", is why they are not on the
screen that must be quick (counting them reads the whole directory and probes
everyone). The `session` row probes too, with the same presence probe, but only
to answer whether this endpoint has a session; it is a conclusion about this
endpoint, not a count of others.

Row values, the complete lists (anything else is a defect):

- `version`: the number.
- `server`: `reachable, encrypted`; `reachable, NOT ENCRYPTED` (the test-server
  override allows a plain address); `reachable, certificate NOT TRUSTED`; `NOT
  REACHABLE`; `NOT ENCRYPTED (refused; not connected)`; `not checked` with a
  reason when mash did not ask. No time and no address: a row that says
  `reachable` was checked just now.
- `config`, `credentials`: as the targets (`ok`, `MISSING`, `REFUSED`,
  `UNSAFE`, `not checked`).
- `mailbox` (REQ-015, section 4.4): `0 waiting`; `2 waiting`; `17 waiting,
  OVER THE LIMIT OF 15 (woke the session at 09:40)` (a warning: the verdict
  says `mail works (1 warning)`, exit 0); `3 waiting, no limit set` (the config
  key `mailbox_limit` is 0; mash never wakes the session for a backlog; the
  count is still shown); `not checked`. In order: how many are waiting; whether
  that is OVER THE LIMIT; when it is, whether and when mash woke the session
  (`woke the session at 09:40`, `no wake yet`, or `no session to wake`). When
  the OLDEST message is past `mailbox_age` the row is `3 waiting, oldest 1h
  12m, OVER THE AGE LIMIT OF 1h`, with the wake on its second line; when both
  limits are passed, `22 waiting, OVER THE LIMIT OF 15 AND THE AGE LIMIT OF
  1h`, and the second line is `oldest 1h 12m (woke the session at 09:40)`. The
  `warning:` and the `fix:` under the rows say what to do.
- "Limit" means one thing, and every screen that shows it says so: the limit
  is the number that triggers a wake, and the age limit is the age that
  triggers a wake. It is not a capacity: the inbox's
  capacity is 16MB or 30 days, a separate warning (below).
- A "wake" is a note that mash sends an idle session to say mail is waiting;
  the screen that shows a wake says so in the same words.
- `session`: `online`; `offline (mail waits for the next session)`; `not
  checked`. `online` means a Claude Code session answered the presence probe
  for this endpoint just now, the same words and the same source as `STATE` in
  `mash endpoint status` (section 4.3), and it is defined once, in `mash help
  endpoint status` (4.3), where `mash status --detail` points. The probe, not
  the lease, is the one source: the lease says what a session wrote up to 90 s
  ago (`store.LeaseTTL`, `store.go:22`), the probe says who answers now, and it
  is the only thing that can be asked about another endpoint at all, so the
  same question has the same answer wherever it is asked. The lease still
  supplies facts that are not a state: when and where this session started
  (`--detail`) and when mash last woke it (section 4.4). Cost: about 0.3 s more
  when a session answers, up to 2 s more when none does.

When mail was lost or the inbox is nearly full, a second `warning:` follows
(`eviction.go:22-60`).

Verdict forms are the targets', plus one: `<endpoint>: mail works.`, `...
(1 warning).`, `... mail does NOT work: <why>.`, `mail does NOT work: <why>.`
when no endpoint is known, and `<endpoint>: not checked: <why>.` when mash did
not ask (the environment rule, below). `session: offline` does not change the
verdict: mail waits.

THE ENVIRONMENT RULE, one rule for `MASH_SERVER` and `MASH_CA`. A value that
differs from what `config.json` holds is IGNORED: nothing vouches for it, so
mash asks nothing and the verdict is `not checked` (exit 1, not ready). The fix
says so in the same words ("mash IGNORES it") and how to remove it (`unset
MASH_SERVER`). The one exception is the test-server override: a `nats://`
address in `MASH_SERVER` together with `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1`
(REQ-010) is used, with a warning, and the verdict stays `mail works (1
warning)`. A value equal to `config.json` is not an override and is not
reported. So a tls address in `MASH_SERVER` is never used, and a plain one is
used only with the variable.

Labels under the rows (one rule for the page, also rule 8 of section 1): `fix:`
is for a problem, and its command stands alone on its own line; `next:` is for a
step that is not a fix (a list cut short; a step no command can do); `note:` is
a plain fact that stops the screen from misleading; `warning:` says what is
wrong and why it matters. A screen that worked and has nothing to add ends at
its rows.

Exit codes as the contract (section 2 of the targets): 0 mail works, 1 it does
not or was not checked, 4 refused, 6 unreachable, 2 usage.

Healthy:

<!-- packet: status-healthy -->
```
$ mash status
acme.demo.bob: mail works.

  version      0.5.6
  server       reachable, encrypted
  config       ok
  credentials  ok
  mailbox      2 waiting
  session      online
```

Healthy, `--detail`. The six rows come first, then the facts; one value column
for the whole screen. REQ-012's rows stay (config file, CA file, credentials
file, selected endpoint, how it was chosen); the group counts, the clock and
every server fact are gone (they are `mash endpoint status` and `mash server
status`), and so are the trust rows (the contract has no trust record). Added:
what `waiting` means, the mailbox limit, mail shown only as a one-line summary
(still waiting), when and where the session started. The CA, the token and the
commit are explained where they appear:

<!-- packet: status-detail -->
```
$ mash status --detail
acme.demo.bob: mail works.

  version           0.5.6
  server            reachable, encrypted
  config            ok
  credentials       ok
  mailbox           2 waiting
  session           online (what that means: mash help endpoint status)

  config file       /home/bob/.config/mash/config.json
  ca file           /home/bob/.config/mash/ca.pem (how mash trusts the server)
  credentials file  /home/bob/.config/mash/acme.demo.bob.creds
  endpoint          acme.demo.bob
  chosen by         the only one installed here
  waiting means     not yet shown to the session in full, and not read
  mailbox limit     15 (the number that triggers a wake; key mailbox_limit)
                    age 1h (the age that triggers a wake; key mailbox_age)
  summary only      0 (shown as a one-line summary, not read; still waiting)
  session started   2026-11-19 09:12 +00:00 on localhost
  claude code       set up to tell an idle session about new mail
  token expires     never (the token: what the server issued with your key)
  commit            e389eaf (the source this mash was built from)
  settings          no MASH_* variable is set
  server details    run: mash server status
```

The session is not running (nothing to wake; mail waits):

<!-- packet: status-offline -->
```
$ mash status
acme.demo.bob: mail works.

  version      0.5.6
  server       reachable, encrypted
  config       ok
  credentials  ok
  mailbox      2 waiting
  session      offline (mail waits for the next session)
```

Claude Code is not set up to wake an idle session (warning, exit 0; the
warning is the targets', with the channel explained and one thing added: a
session that is already running must be closed first, because the channel is
chosen when Claude Code starts):

<!-- packet: status-claude -->
```
$ mash status
acme.demo.bob: mail works (1 warning).

  version      0.5.6
  server       reachable, encrypted
  config       ok
  credentials  ok
  mailbox      1 waiting
  session      online

warning: an idle Claude Code session is not woken by new mail; the mail
         waits for its next prompt.
fix: close the session, then start Claude Code with the mash channel (the
     link that lets mash wake an idle session), from the terminal you want
     it in:
  claude --dangerously-load-development-channels plugin:mash@mash
```

The mailbox is over its limit and a session is there (warning, exit 0; AT-008).
This is the screen Randy asked for ("more than X messages stack up ...
undelivered"). The row says how many, that it is over, and when mash woke the
session; the warning says what the limit is, what a wake is, and what stops a
message waiting; the fix is a concrete thing to look at and a way to lower the
count yourself:

<!-- packet: status-mailbox-over -->
```
$ mash status
acme.demo.bob: mail works (1 warning).

  version      0.5.6
  server       reachable, encrypted
  config       ok
  credentials  ok
  mailbox      17 waiting, OVER THE LIMIT OF 15 (woke the session at 09:40)
  session      online

warning: 17 messages are waiting. The limit, 15, is the number that triggers
         a wake: a note mash sends an idle session to say mail is waiting.
         It sends one at most every 10 minutes while more than 15 wait. A
         message stops waiting when the session is shown it in full or reads
         it. A one-line summary of a message does not count as shown.
fix: if the count stays over the limit, the session is not taking mail. Look
     at the terminal it runs in: it may be stuck or waiting for you. Or read
     the mail yourself; the session is running, so a message you read leaves
     the count within a second:
  mash inbox
  mash read <number>
```

Before the first wake the row says `(no wake yet)` instead of the time (the 10
minutes count from the start of `mash serve`, so a restart never wakes sooner).
`mash inbox` lists the mail, the unread first, with each message's number;
`mash read` marks what it reads as read here: a running session's serve
acknowledges it within 250 ms, and with no session the count drops when the
next one starts (4.4).

The oldest waiting message is older than the age limit (warning, exit 0;
AT-008), however few wait. The row says how many, how old the oldest is, that
the AGE limit is passed, and when mash woke the session (its second line):

<!-- packet: status-mailbox-age-over -->
```
$ mash status
acme.demo.bob: mail works (1 warning).

  version      0.5.6
  server       reachable, encrypted
  config       ok
  credentials  ok
  mailbox      3 waiting, oldest 1h 12m, OVER THE AGE LIMIT OF 1h
               (woke the session at 09:40)
  session      online

warning: 3 messages are waiting, and the oldest has waited 1h 12m. The age
         limit, 1h, is the age that triggers a wake: a note mash sends an
         idle session to say mail is waiting. It sends one at most every 10
         minutes while the oldest message is older than that.
fix: have the session read its mail: the session is running, so a message
     you read leaves the count within a second:
  mash inbox
  mash read <number>
To change the age limit, add this line to ~/.config/mash/config.json (0
turns the age check off):
    "mailbox_age": "2h"
```

With `mailbox_age` 0, the `--detail` line under `mailbox limit` reads `no age
limit set` and the age never wakes the session.

The mailbox is over its limit and no session is there (warning, exit 0). There
is no one to wake, so the action is to start one or to read the mail:

<!-- packet: status-mailbox-over-offline -->
```
$ mash status
acme.demo.bob: mail works (1 warning).

  version      0.5.6
  server       reachable, encrypted
  config       ok
  credentials  ok
  mailbox      17 waiting, OVER THE LIMIT OF 15 (no session to wake)
  session      offline (mail waits for the next session)

warning: 17 messages are waiting. The limit, 15, is the number that triggers
         a wake (a note mash sends an idle session). No session is running,
         so there is no one to wake: the mail waits for the next session.
fix: start Claude Code in the terminal you want it in: it is shown the
     waiting mail. Or read the mail yourself; no session is running, so the
     count drops when the next one starts, not when you read:
  mash inbox
  mash read <number>
```

The limit is turned off (`mailbox_limit` 0; exit 0, no warning). The `note:`
explains the word, because this person never sees the over-the-limit screen:

<!-- packet: status-mailbox-off -->
```
$ mash status
acme.demo.bob: mail works.

  version      0.5.6
  server       reachable, encrypted
  config       ok
  credentials  ok
  mailbox      3 waiting, no limit set
  session      online

note: the limit is the number of waiting messages that triggers a wake. None
      is set, so mash never wakes the session for a backlog. To set one, put
      "mailbox_limit" (a whole number; the default is 15) in
      ~/.config/mash/config.json.
```

Server not reachable (exit 6). No address on this screen: the server's address
is on `mash server status`, which is the one place that shows it, with the
cause:

<!-- packet: status-unreachable -->
```
$ mash status
acme.demo.bob: mail does NOT work: the server did not answer within 2s.

  version      0.5.6
  server       NOT REACHABLE
  config       ok
  credentials  not checked (the server was not reached)
  mailbox      not checked
  session      not checked

fix: the server did not answer; details, with the address mash tried:
  mash server status
```

Credentials refused (exit 4):

<!-- packet: status-refused -->
```
$ mash status
acme.demo.bob: mail does NOT work: the server refused its credentials.

  version      0.5.6
  server       reachable, encrypted
  config       ok
  credentials  REFUSED (deleted, or its key was reissued)
  mailbox      not checked
  session      not checked

fix: ask whoever runs your mash server which of the two it was. If the key
     was reissued, move the old credentials aside, then install the new key
     and token (the token is the server's signed approval of your key):
  mv ~/.config/mash/acme.demo.bob.creds ~/.config/mash/acme.demo.bob.creds.old
  mash credential install --key <key file> --token <token file>
```

The endpoint was deleted (exit 4; needs the server to say "deleted", N11). Once
the administrator has issued a new key and token, the same `credential install`
as above installs them:

<!-- packet: status-deleted -->
```
$ mash status
acme.demo.bob: mail does NOT work: this endpoint was DELETED on the server.

  version      0.5.6
  server       reachable, encrypted
  config       ok
  credentials  REFUSED (endpoint DELETED)
  mailbox      not checked
  session      not checked

fix: its address cannot be used again. For a new endpoint, make a new key,
     then give its .pub file to whoever runs your mash server:
  mash key create --out <new-name>.key
```

No configuration at all (exit 1; nothing is asked, so every row after
`version` is `not checked`). The words a newcomer meets here are explained in
the fix, once; the command is continued over two lines with a backslash, so it
stays pasteable and within 80 columns:

<!-- packet: status-noconfig -->
```
$ mash status
mail does NOT work: mash has no configuration on this machine.

  version      0.5.6
  server       not checked (no server is set)
  config       MISSING (~/.config/mash/config.json)
  credentials  not checked
  mailbox      not checked
  session      not checked

fix: ask whoever runs your mash server for five things: a key file (your
     endpoint's private key), a token file (the server's signed approval of
     your key), the server address, the CA file (the certificate that lets
     mash trust the server) and the account key (it names the server's
     account). mash shows the address, the CA fingerprint (a short code that
     identifies the CA file) and the account, and asks you to confirm all
     three before it writes anything:
  mash credential install --key <key file> --token <token file> \
    --server <address> --ca <ca file> --account <account key>
```

Several endpoints installed and none chosen (exit 1). The server needs no
credentials, so it is still checked; the candidates move from the credentials
row to the fix, where they are acted on. The first fix sets it for good (a
command that writes the key does not exist in 0.5.6, so the person edits the
file), the second for one shell:

<!-- packet: status-choose -->
```
$ mash status
mail does NOT work: 2 endpoints are installed here and none is chosen.

  version      0.5.6
  server       reachable, encrypted
  config       ok
  credentials  not checked (no endpoint is chosen)
  mailbox      not checked
  session      not checked

fix: mash never guesses between acme.demo.alice and acme.demo.bob. To use
     acme.demo.bob, set "endpoint" to "acme.demo.bob" in
     ~/.config/mash/config.json. For this shell only, and the Claude Code
     you start from it:
  export MASH_ENDPOINT=acme.demo.bob
```

Plain text with the test-server override (warning, exit 0). Both addresses of
the override are in the warning, because the person must see what is
overriding what, and `nats://` is explained where it first appears:

<!-- packet: status-plain-warning -->
```
$ export MASH_SERVER=nats://localhost:4222
$ export MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1
$ mash status
acme.demo.bob: mail works (1 warning).

  version      0.5.6
  server       reachable, NOT ENCRYPTED
  config       ok
  credentials  ok
  mailbox      2 waiting
  session      online

warning: anyone on the network can read your credentials and your mail.
         MASH_SERVER is set to nats://localhost:4222, a plain-text address
         (tls:// ones are encrypted), instead of tls://localhost:4222 from
         config.json. It is allowed only because
         MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION is set (test servers only).
fix: stop overriding it in this shell:
  unset MASH_SERVER MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION
```

The environment names a server that `config.json` does not have (not checked,
exit 1; mash does not ask that server). A `MASH_CA` that differs reads the same
with `MASH_CA`. The fix says the setting is IGNORED, which agrees with the
verdict (mash never used it), and how to remove it:

<!-- packet: status-env-override -->
```
$ export MASH_SERVER=tls://other:4222
$ mash status
acme.demo.bob: not checked: MASH_SERVER is not the server in config.json.

  version      0.5.6
  server       not checked (MASH_SERVER is not the server in config.json)
  config       ok
  credentials  not checked (the server was not asked)
  mailbox      not checked
  session      not checked

fix: MASH_SERVER is set to tls://other:4222, which is not the server in
     config.json, so mash IGNORES it and asked no server. Remove it from
     this shell, then run mash status again:
  unset MASH_SERVER
```

The other states keep the targets' text with the rows changed the same way: a
plain address without the override (`server  NOT ENCRYPTED (refused; not
connected)`, exit 1), the override set with a tls address (a warning;
`server  reachable, encrypted`, and the warning says the variable is set and
not used), and a credentials file that is not private (`credentials  UNSAFE
...`, exit 1) or missing. In a running session the MCP `status` tool returns
the same six facts (section 8). The plugin-drift warning that hangs on the
`version` row is the targets' and is not redrawn here (I could not determine
its fix command: `plugin install` is out of 0.5.6, section 9).


### 4.2 `mash server status`: the server

The question: "is my server up, is it safe to talk to, and has it room?" The
rows are what section 2 shows an endpoint may read, nothing from `$SYS` and
nothing that asks a denied subject. Reachability and encryption need no
credentials; only the storage row does (JetStream answers only a signed-in
endpoint). A credentials problem is a fact about this endpoint, not about the
server, so it never turns a healthy server into a failed screen (below).

Four fixed rows, in this order, in every state:

| row | why, in one line | source |
|---|---|---|
| `address` | the one place the address is shown, as configured, with its scheme | config |
| `state` | reachable, and how fast it answered (the connection and one request) | connect time |
| `encryption` | encrypted or not; the certificate trusted; a problem says which | the TLS handshake and chain |
| `mail storage` | what the server holds for all owners, in use now; `NEARLY FULL` past 90% of its limit | `$JS.API.INFO` |

`--detail` adds, in the same screen: `server software` (NATS 2.15.1),
`certificate` (checked against which CA file), `certificate ends` (the date and
the days left; within 30 days is a warning), `clock` (the server's against this
computer's; beyond 10 s it is a `warning:` too, because a skew that large makes
peers look offline and mail `[stale]`, `status.go:321-345`), `storage limit`,
`mail stores` (a store holds one inbox, or mash's own records), `inbox rules`
(each inbox keeps 16MB or 30 days of mail, `eviction.go:31-37`), `message
limit` (the largest message) and `endpoints` (how many are registered, ALL
owners: the directory holds every owner's, so this number can be larger than
the "endpoints of acme" of `mash endpoint status`). The reserved storage the
server reports (125MB in my run) is not shown: what counts toward it is
unverified (section 9), and a row that answers "has it room" must not stand on
it. Each fact appears once on a screen: the certificate's date is in
`certificate ends` only.

Healthy:

<!-- packet: server-healthy -->
```
$ mash server status
The server is reachable and encrypted.

  address       tls://localhost:4222
  state         reachable (answered in 41ms)
  encryption    encrypted; certificate trusted
  mail storage  1KB in use
```

Healthy, `--detail`:

<!-- packet: server-detail -->
```
$ mash server status --detail
The server is reachable and encrypted.

  address           tls://localhost:4222
  state             reachable (answered in 41ms)
  encryption        encrypted; certificate trusted
  mail storage      1KB in use

  server software   NATS 2.15.1
  certificate       checked against /home/bob/.config/mash/ca.pem
  certificate ends  2027-02-01 (in 74 days)
  clock             1s ahead of this computer (only 10s or more matters)
  storage limit     256MB
  mail stores       7 of 50 in use (3 inboxes, the rest are mash's own)
  inbox rules       each inbox keeps up to 16MB and 30 days of mail
  message limit     1MB for one message
  endpoints         3 registered, all owners
```

Server not reachable (exit 6; every row after `state` is `not checked`). The
same sentence as `mash status` and `mash endpoint status`:

<!-- packet: server-unreachable -->
```
$ mash server status
The server did not answer within 2s.

  address       tls://localhost:4222
  state         NOT REACHABLE
  encryption    not checked
  mail storage  not checked

fix: check that the server is running and that "server" in
     ~/.config/mash/config.json is right.
```

Plain text with the test-server override (a warning, exit 0; the connection is
allowed, so the rest is read):

<!-- packet: server-plaintext -->
```
$ export MASH_SERVER=nats://localhost:4222
$ export MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1
$ mash server status
The server is reachable, but NOT ENCRYPTED (1 warning).

  address       nats://localhost:4222
  state         reachable (answered in 3ms)
  encryption    NOT ENCRYPTED (allowed by the override)
  mail storage  1KB in use

warning: anyone on the network can read your credentials and your mail.
         MASH_SERVER overrides the server in config.json for this run.
fix: stop overriding it in this shell:
  unset MASH_SERVER MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION
```

The certificate is not trusted (exit 1). The server answered, but mash does not
talk further to a server it does not trust, so the rest is `not checked`. The
cause is named in the row: not signed by this machine's CA, expired, or made
for another host name. There is no trust record and no `mash trust reset` in
0.5.6 (contract), so the way out is the rule of `credential install`:
it never changes an existing CA file, it says which file to move aside:

<!-- packet: server-untrusted -->
```
$ mash server status
The server's certificate is NOT TRUSTED.

  address       tls://localhost:4222
  state         reachable (answered in 41ms)
  encryption    certificate NOT TRUSTED (not signed by the CA in
                ~/.config/mash/ca.pem)
  mail storage  not checked (mash does not trust this server)

fix: do not go on if you did not expect this. Ask whoever runs your mash
     server whether it changed its certificate. If it did, they give you the
     new CA file: move the old one aside, then install again with it:
  mv ~/.config/mash/ca.pem ~/.config/mash/ca.pem.old
  mash credential install --key <key file> --token <token file> --ca <ca file>
```

For an expired certificate or a wrong host name the same screen says `expired
2026-11-01` or `made for another host name`, and the fix is to ask whoever runs
the server to renew it or to give the right address.

This endpoint's credentials are refused or missing (exit 0: the server is fine;
the storage row says why it is blank and where to look):

<!-- packet: server-no-credentials -->
```
$ mash server status
The server is reachable and encrypted.

  address       tls://localhost:4222
  state         reachable (answered in 41ms)
  encryption    encrypted; certificate trusted
  mail storage  not checked (needs this endpoint's credentials: mash status)
```

Exit codes: 0 the server is fine (a credentials problem does not change it), 1 a
problem with the server itself (not trusted, expired, wrong host name), 6 not
reachable. `mash server status` never asks a `$SYS` subject: it would be
dropped and cost the whole timeout.

What needs `mashctl`, said in `mash help server status` and nowhere on the
screen: who is connected now, the server's uptime and load, every inbox's size
(`mashctl audit`), the keys issued (`mashctl issued list`), and changing a
limit (`mashctl limits`).

### 4.3 `mash endpoint status [<address or pattern>]`: your owner's endpoints

The question: "which of our endpoints exist, and which answer now?" It replaces
`mash who` (section 5). It lists only the endpoints of the caller's owner,
because beyond the owner nothing can be known (section 2); this also corrects
the targets, where N13 assumed `who` already did so.

THE PATTERN GRAMMAR, RULED (the planner's ruling; ctl mirrors it), stated once:
one grammar for `mash endpoint status` and for `mashctl endpoint list` and
`show`. An address is `owner.group.name`. A pattern is an address, or an
address cut short after a dot, in which a part may hold `*`: any run of
characters (including none) inside that part, never across a dot; a part that
is only `*` is any one part. A cut-short pattern means the missing parts are
`*`: `acme.demo` is `acme.demo.*`, "everything in demo". Otherwise a pattern
has three dotted parts. In mash the owner part is written out and must be your
owner: a pattern never crosses owners. No `?`, no `[...]`, no regular
expressions. The two additions to ctl's own answer (appendix), `*` inside a part
(`acme.demo.b*`) and the cut-short form, are ruled in. `mashctl`'s only
difference is scope, not grammar: an administrator may name another owner, or
`*` for any.

The patterns, and what each matches:

| written | matches |
|---|---|
| (nothing) | every endpoint of your owner, the same as `acme.*.*` |
| `acme.demo.alice` | that one endpoint (the one-address view) |
| `acme.demo` | every endpoint in the group `demo`, the same as `'acme.demo.*'` |
| `'acme.demo.*'` | every endpoint in the group `demo` |
| `'acme.*.bob'` | every endpoint named `bob`, in any group |
| `'acme.demo.b*'` | `bob`, `bart`, ... in the group `demo` |
| `'acme.release-*.deploy-*'` | `acme.release-pipeline.deploy-coordinator` |
| `'*.demo.*'` or `zed.demo.alice` | refused: the owner is not `acme` (exit 2) |

Why the examples are quoted: unquoted, zsh stops with "no matches found" before
mash runs (and bash may expand the pattern against the files in the folder);
quoted, the shell passes it through. `acme.demo` holds no `*`, needs no quote,
and is accepted. One command takes one address or pattern; for two groups run it
twice.

The filters of `who` that are not a name stay as flags: `--online`,
`--offline`, `--role SUB`, `--label K[=V]`, `--cap C`, `--match TEXT`. `--match`
is a case-insensitive search of the description and the capabilities
(`internal/endpoint/whofilter.go:14,122`); it no longer searches the address,
which the pattern says (one way to say it). `--owner` and `--name` are removed.

Columns: `ENDPOINT` and `STATE` (`online` or `offline`), nothing else by
default; `--wide` adds `VERSION`, `MEANT TO RUN ON` and `ROLE`. The caller's own
row ends `(you)`. Nothing is remembered: `online` means a Claude Code session
answered the presence probe just now (section 4.1), `offline` means none did,
and that is normal. There is no `LAST SEEN` column, because a last-seen time
has no source (section 3), and no `MAILBOX` column. REQ-015 (contract, rev 26)
says it, and this is the one place the page states it: `mash endpoint status`
shows no mailbox column, because an endpoint's credentials can read only its
own inbox (section 2), so the count is shown by that endpoint's own `mash
status`, and a column would be `unknown` on every row but yours. This page adds
one thing the contract does not say: the one-address view of your own endpoint
shows the same row (section 8). The list is thinner than "more detailed"
sounds; what is true about another endpoint is its address, whether it answers,
its version, where it is meant to run, and the directory's description.

`meant to run on` is the directory's `host` field, where an administrator says
the endpoint is meant to run; it is not a live fact for any endpoint but your
own, and every block that shows it says so in one line.

The verdict line names the set and the counts, which says which endpoints the
default acted on (standard rule 8). Sorted by address. No legend under a list:
the definitions are in `mash help endpoint status`, and that is the one place
`online` and `offline` are defined (`mash status --detail` points to it):

<!-- packet: endpoint-status-help -->
```
$ mash help endpoint status
mash endpoint status [<address or pattern>]

Which endpoints of your owner exist, and which answer now.

  mash endpoint status                  every endpoint of your owner
  mash endpoint status acme.demo        everything in the group demo
  mash endpoint status 'acme.demo.b*'   names starting with b, in demo
  mash endpoint status acme.demo.alice  one endpoint, in full

Quote a pattern that holds *: zsh stops with "no matches found" before mash
runs. A part may hold * for any run of characters; the owner must be yours.

online   a Claude Code session answered for that endpoint just now
offline  none did. That is normal: its mail waits for the next session

Flags:
  --online       only the endpoints that are online
  --offline      only the endpoints that are offline
  --role TEXT    only roles that contain TEXT (what an endpoint is for)
  --label K[=V]  only endpoints with that label, K or K=V
  --cap NAME     only endpoints with that capability
  --match TEXT   only endpoints whose description or capabilities hold TEXT
  --wide         a list with more columns: version, where each is meant to
                 run, role
  --detail       one address, with more rows: when it was registered,
                 program, description, what mash does not keep
  --all          a list: every row, not only the first 40
  --json         the same answer as one JSON document
```

Exit 0 whenever the question was asked and answered, even if every endpoint is
offline or a pattern matches nothing: offline and empty are answers. `--json`
and the other exits are at the end of this section.

All endpoints of your owner:

<!-- packet: endpoint-status -->
```
$ mash endpoint status
3 endpoints of acme: 2 online, 1 offline.

  ENDPOINT         STATE
  acme.demo.alice  online
  acme.demo.bob    online (you)
  acme.lab.carol   offline
```

A pattern with matches, and the group form that needs no quote:

<!-- packet: endpoint-status-pattern -->
```
$ mash endpoint status 'acme.demo.*'
2 endpoints match acme.demo.*: 2 online.

  ENDPOINT         STATE
  acme.demo.alice  online
  acme.demo.bob    online (you)
```

<!-- packet: endpoint-status-group -->
```
$ mash endpoint status acme.demo
2 endpoints match acme.demo.*: 2 online.

  ENDPOINT         STATE
  acme.demo.alice  online
  acme.demo.bob    online (you)
```

A pattern with no match is an answer (exit 0, stdout), as `gh` and `kubectl`
treat an empty filtered list:

<!-- packet: endpoint-status-nomatch -->
```
$ mash endpoint status 'acme.demo.z*'
0 endpoints match acme.demo.z*.
```

An owner that is not yours (exit 2, a bad argument; the fix names the corrected
pattern, which mash can derive):

<!-- packet: endpoint-status-owner -->
```
$ mash endpoint status '*.demo.*'
error: the owner of a pattern must be acme, your owner: mash shows only
       the endpoints of acme.
fix: write the owner out:
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
  mash endpoint status acme.demo
```

One address: a block. The facts are the ones a person can read about another
endpoint (section 2): `state` from the probe, its version from the answer, the
rest from the directory record. Below it, one line says what `meant to run on`
is, and one line tells a sender what mash cannot know, which is whether the
recipient has read the mail:

<!-- packet: endpoint-status-one -->
```
$ mash endpoint status acme.demo.alice
acme.demo.alice: online.

  endpoint         acme.demo.alice
  state            online
  version          0.5.6
  meant to run on  localhost

note: meant to run on is what an administrator recorded, not where it runs.
note: mash cannot tell whether acme.demo.alice has read your mail; only a
      reply shows that.
```

Your own endpoint adds the one row only you can read, the mailbox, in the words
of `mash status`, and has no sender line (you are the reader):

<!-- packet: endpoint-status-own -->
```
$ mash endpoint status acme.demo.bob
acme.demo.bob: online (you).

  endpoint         acme.demo.bob
  state            online
  mailbox          2 waiting
  version          0.5.6
  meant to run on  localhost

note: meant to run on is what an administrator recorded, not where it runs.
```

An endpoint nobody is running (registered; nothing is made up: unknown is `-`):

<!-- packet: endpoint-status-offline -->
```
$ mash endpoint status acme.lab.carol
acme.lab.carol: offline.

  endpoint         acme.lab.carol
  state            offline
  version          -
  meant to run on  -

note: mail to acme.lab.carol waits for its next session. mash cannot tell
      whether it has been read; only a reply shows that.
```

One address, `--detail` (more rows, never a different screen). The last row is
the honest answer to "heartbeat status": there is none to show (top item 1):

<!-- packet: endpoint-status-detail -->
```
$ mash endpoint status acme.demo.alice --detail
acme.demo.alice: online.

  endpoint         acme.demo.alice
  state            online
  version          0.5.6
  meant to run on  localhost

  registered       2026-11-19 00:21 +00:00
  role             reviewer
  description      reviews the release plan
  capabilities     review
  labels           team=demo
  program          claude-code
  storage name     acme_demo_alice (its mail store on the server)
  last alive       not recorded: mash only asks whether a session answers now

note: meant to run on is what an administrator recorded, not where it runs.
note: mash cannot tell whether acme.demo.alice has read your mail; only a
      reply shows that.
```

A list with `--wide`: more columns on the same row, which may pass 80 columns
(standard rule 28), never wrap:

<!-- packet: endpoint-status-wide -->
```
$ mash endpoint status --wide
3 endpoints of acme: 2 online, 1 offline.

  ENDPOINT         STATE    VERSION  MEANT TO RUN ON  ROLE
  acme.demo.alice  online   0.5.6    localhost        reviewer
  acme.demo.bob    online   0.5.6    localhost        -         (you)
  acme.lab.carol   offline  -        -                -
```

Many endpoints. Past 40 rows the table stops and says so; the verdict counts
all of them; `--all` shows every row, a pattern or `--online` narrows it. The
caller's own row is always printed, last, when it is not among the 40, so a
person can find themselves (the four rows shown stand for the first 40; the
`...` stands for the rest, which the real output prints). A list cut short ends
with one labelled `next:` line and the command on its own line:

<!-- packet: endpoint-status-many -->
```
$ mash endpoint status
112 endpoints of acme: 7 online, 105 offline. Showing the first 40.

  ENDPOINT               STATE
  acme.build.runner-001  online
  acme.build.runner-002  offline
  acme.build.runner-003  offline
  acme.build.runner-004  online
  ...
  acme.demo.bob          online (you)

next: to see the rest, narrow the list with a pattern or --online, or show
      every row:
  mash endpoint status --all
```

The real-name case (address kept whole, widest 56 columns):

<!-- packet: endpoint-status-long -->
```
$ mash endpoint status
2 endpoints of acme: 2 online.

  ENDPOINT                                  STATE
  acme.release-pipeline.build-watcher-prod  online
  acme.release-pipeline.deploy-coordinator  online (you)
```

The server cannot be reached (exit 6, stderr: the whole answer depends on it).
The fix names the one command that shows the address and the cause:

<!-- packet: endpoint-status-unreachable -->
```
$ mash endpoint status
error: the server did not answer within 2s.
fix: check that the server is running and that "server" in
     ~/.config/mash/config.json is right. To see the address mash tried:
  mash server status
```

`--json` (one document, standard rule 10; the fields keep the names of the MCP
`who` result of the targets, section 6, plus the pattern; `host` is the `meant
to run on` field; `you` and `mailbox` appear on the caller's own entry only;
there is no `last_seen`):

```
{ "owner": "acme", "pattern": "acme.demo.*",
  "endpoints": [
    { "endpoint": "acme.demo.alice", "state": "online",
      "version": "0.5.6", "program": "claude-code", "host": "localhost",
      "role": "reviewer" },
    { "endpoint": "acme.demo.bob", "state": "online", "you": true,
      "mailbox": { "waiting": 2, "limit": 15, "oldest_age": "9m",
                   "age_limit": "1h", "over_limit": false,
                   "over_age_limit": false, "woke_at": null },
      "version": "0.5.6", "program": "claude-code", "host": "localhost",
      "role": null } ] }
```

Exit codes: 0 asked and answered (including no match and every endpoint
offline); 1 an address that is not registered, trust or configuration problems
(`mash status` says which); 2 a pattern that is not an address or cut-short
address, an owner that is not yours, an unknown flag; 4 the credentials were
refused; 6 the server did not answer.

### 4.4 The mailbox count: one source, and delivery acknowledges

THE COUNT, ONE SOURCE, stated once. The number on every screen is the
consumer's own: the messages in this endpoint's inbox that its consumer has not
acknowledged, pending plus ack-pending (`NumPending + NumAckPending`), as
REQ-015 defines it. Whoever asks reads that number from the server: `mash
status` from a shell (a consumer info request on its own consumer, which an
endpoint's credentials may make, `internal/admin/permissions.go:67`), `mashctl
audit`, and the MCP `status` tool. There is no second source: DRAFT4's lease
field `backlog` is gone, and a shell never reads a number `mash serve` wrote.
`mash serve`'s wake trigger counts the same set in memory (the waking tracker's
unread set, `internal/endpoint/waking.go:42-49`: every message received and not
yet acked), so the trigger and the screen differ only by an ack that is in
flight (up to 250 ms). One thing is not a count and does live in the lease: the
time of the last backlog wake, `backlog_woke_at`, beside `waking_pending`
(`internal/endpoint/endpoint.go:176-181`), because REQ-015 keeps the cooldown in
memory (it does not survive a restart) and the row must say `woke the session
at 09:40` from somewhere it can read. The lease is the endpoint's own record,
readable by its own credentials; it is not a SAP field, so BND-002 holds.

THE RULING (the lead, contract rev 21; REQ-015 of rev 26 words it), which
replaced DRAFT3's section 4.4. What changes is what ACKNOWLEDGES. DELIVERY
ACKNOWLEDGES: a message shown in full in the session's hook context or read
with the `read` tool is acknowledged and no longer counts. `mash read` from a
shell marks the message read here, and it stops counting within 250 ms if a
session's serve is running, otherwise when the next session starts (item 2). A
digest does not acknowledge. A digest is the
hook's one-line summary of a message (sender, size, first line), which it
prints instead of the whole text when many messages wait; on a screen this page
says "a one-line summary of the message". A message the hook only summarised
stays unacknowledged and COUNTS. DRAFT3 had recommended the opposite; that is
dropped.

What v0.5.5 does with acknowledgement today, path by path (the last column is
what 0.5.6 does). The table cells are long; each row is one line of the file:

| path | what happens | acks today? | in 0.5.6 | where |
|---|---|---|---|---|
| the consumer | durable `deliver`, pull, explicit ack, ack wait 30 s, at most 1000 unacked, redelivered until acked (MaxDeliver -1), from the start of the stream | - | same | `internal/store/store.go:130-137` |
| mail arrives | `mash serve` receives it, classifies it, queues it, and HOLDS it unacked | no | same | `internal/endpoint/mail.go:339-387` |
| a hook shows it in full | the hook prints up to 10 messages (and 16 KiB) as hook context, then marks each drained in the local spool; serve sees the mark within 250 ms and acks (double ack, 2 s) | YES | same | `cmd/mash/hook.go:140-176`, `internal/host/claude.go:189-249`, `internal/endpoint/hook.go:39,256-258,478-492` |
| a hook shows a digest | the hook prints one line per message and marks each LISTED, not drained; the message stays unacked, never wakes, and waits to be read | no | same: no ack, and it COUNTS | `cmd/mash/hook.go:160-170`, `internal/spool/spool.go:395-435`, ADR 0046 |
| the `read` tool (MCP) | after the result is written to the model it drains a message that was LISTED, or one waiting in a session proven hookless; any other waiting message is left for a hook | only those two | ANY waiting message | `cmd/mash/serve.go:708-722`, `internal/endpoint/hookless.go:144-185` |
| `mash read` (shell) | reads the stored message straight from the stream; no consumer, no spool | no | the drained tombstone: a running serve acks within 250 ms, else the next serve start (item 2); no direct ack in 0.5.6 | `cmd/mash/cli.go:134-148`, `internal/endpoint/mail.go:870-895` |
| `mash inbox`, the MCP `inbox` tool | list stored messages straight from the stream | no | no (listing is not reading) | `internal/endpoint/mail.go:899-943`, `hookless.go:148`, ADR 0046 |
| before any hook ran (no spool-from-start) | the channel write is the delivery; serve acks it after the write | YES | same | `internal/endpoint/mail.go:1007-1033` |
| a hold, a nak, a lost lease | the message stays unacked and is redelivered after a delay; while held, serve extends its ack deadline every 10 s | no | same | `hook.go:127,171,178,286-291`, `mail.go:356` |
| serve restarts | the consumer redelivers every unacked message after the 30 s ack wait; a message a hook had drained before the crash has a tombstone and is acked then, not shown again | on redelivery | same | `waking.go:25-34`, `mail.go:462-467`, `hook.go:162` |

So a full hook showing already lowers the count today, and one hook run lowers
it by at most 10 (`BatchMsgs`, `claude.go:193,208-210`); with the default limit
of 15, a backlog of 17 clears in two runs of an active session. What does not
lower it today, and does in 0.5.6: the `read` tool on a message no summary
listed, and `mash read` (through the tombstone, so with a session). What
still does not: a one-line summary, and `mash inbox`.

WHICH CODE CHANGES THAT, and anything else the ack change touches:

1. THE `read` TOOL DRAINS ANY WAITING MESSAGE: `MarkRead`
   (`internal/endpoint/hookless.go:144-185`, the condition at `:157-170`) is
   changed so a message read while it is still waiting for a hook is drained,
   and serve acks it. The hook then does not show it again (it shows only what
   is waiting in the spool). A model that reads a message by `#seq` before the
   next hook run has been shown it.
2. `mash read` KEEPS THE TOMBSTONE (the lead, 0.5.6: a direct acknowledgement,
   `Endpoint.AckRead`, is NOT IN 0.5.6; the contract, rev 26, records it).
   `cmd/mash/cli.go:134-148` writes the drained tombstone
   (`spool.Drained`, `internal/spool/spool.go:441-460`) after the message is
   printed: a running `mash serve` acks within 250 ms
   (`internal/endpoint/hook.go:256-258`), and with no session running the next
   serve start acks it when the consumer redelivers it. The truth on every
   screen: with no session running, reading marks the message read HERE, and
   the count drops when the next session starts. Why no direct ack: a shell
   cannot ack by number, it has to receive the message, so it would receive and
   nak up to 1000 earlier messages, a new mechanism with side effects
   (delivery counts, redelivery), and the freeze holds. The build-time checks it
   would need are kept in "Verify at build" (targets decisions file).
3. `mash serve` WRITES THE WAKE TIME: `backlog_woke_at` in the endpoint's own
   lease (the count itself is not written, above). The 10-minute cooldown stays
   in memory as the contract has it, and starts when serve starts.
4. THE TRIGGER USES THE SAME SET: the wake fires when the unread set passes the
   limit, so a session that has been shown or has read its mail is not woken. A
   backlog already over the limit when serve starts wakes once, at 10 minutes
   after the start, if still over (REQ-015). Everything else of REQ-015
   stands: the line text, the cooldown, `mailbox_limit` 0 turns it off.
5. THE AGE TRIGGER (the contract, rev 26, REQ-015; Randy: "configurable but
   defaults to like 1 hour"). WHAT IT COUNTS: the age of the OLDEST waiting
   message, by the time the SERVER stored it (not the time serve first saw it),
   against `mailbox_age` (default 1 hour, 0 turns it off). When it is older, the
   same wake fires, under the same cooldown (per endpoint, 10 minutes from the
   last wake, not kept over a restart) and the same start hold (a backlog
   already over at the start wakes once, at 10 minutes), however few messages
   wait. The wake line and the row say which limit was passed (`oldest 1h 12m,
   over the age limit of 1h`; `status-mailbox-age-over`). BUILD PLAN. In
   `mash serve`: a third predicate in `wakeIfIdle`
   (`internal/endpoint/hook.go:305-317`) beside the count, and the waking
   tracker (`internal/endpoint/waking.go:48`, `:144`) keeps the stream's
   stored time of each unread message (the mail's `At`, which survives a
   restart because redelivery keeps it), with a `min()` for the oldest. In
   `mash status` from a SHELL (no session running): ONE extra direct get on its
   own inbox stream; the consumer's ack floor says which sequence is the oldest
   unacked, and its stored time is the age. The endpoint's own credentials
   already allow a direct get on its own inbox
   (`internal/admin/permissions.go:70-71`; read, not run: "Verify at build"
   (j)). No new SAP field (BND-002), and no new mechanism for a security read:
   it reads its own inbox only. The count itself is unchanged.

This amends ADR 0041 (the `read` tool acks only in a hookless session, and `mash
read` never acks); the pull request that builds items 1 to 3 adds an ADR
(AGENTS.md).

What else the ack change touches (a short list; nothing else changes):

1. `mash inbox` STATE. The `new` and `read` words of `mash inbox` (the targets,
   0.5.6) have no source in 0.5.5 (no per-message state: `internal/render/
   render.go:20-47`). `new` means not acknowledged, so a message shown only as a
   one-line summary is `new` until it is read. From a shell the consumer
   reports counts and an ack floor, not which message above the floor was
   acked, so the number is exact and a per-message `new` is exact only at or
   below the floor (section 9).
2. `mashctl audit` (`internal/admin/audit.go:171-173`, `cmd/mashctl/audit.go:
   295-310`) reads the same consumer fields (pending, ack-pending, ack floor),
   so it and `mash status` read one source and agree, except for an ack in
   flight or a read at a different instant.
3. Redelivery and AckWait (30 s, MaxDeliver -1) matter only for what is held
   unacked; while a session is there, serve keeps held messages alive every 10 s
   (`hook.go:286-291`). Nothing about the values changes.
4. The hook's 10 messages per run (`claude.go:193,208-210`) is unchanged: with
   the limit at 15, a backlog of 22 takes three runs of an active session, and
   reading by `#seq` with the `read` tool lowers the count faster.
5. ADR 0069 (its 500-held and byte lines decide when a crowded spool wakes a
   session that no hook is draining) and ADR 0046 (a digest lists and does not
   ack; `inbox` never acks) are unchanged; REQ-015's limit is a separate, lower
   trigger on the same set.

A message acked by being shown whose session never really saw it. The ack comes
only after the hook has written the context to its output (`hook.go:155-170` of
`cmd/mash`; a failed write leaves it spooled), and Claude Code's
10,000-character cut is avoided by fitting the text first (`claude.go:211-233`).
What mash cannot know is whether the model used it: that is the existing rule,
"an ack means the session has been shown it" (ADR 0024), and the count is of
mail mash has handed over, not of mail the model absorbed. Such a message is not
lost: it is acked, so the consumer never redelivers it, but it stays in the
inbox stream for 30 days or 16MB (section 2), so `mash read <seq>` and `mash
inbox` still show it. A crash between printing and acking leaves a tombstone;
the message is acked, never shown again, at the next serve (`mail.go:462-467`).

NOT changed: what a hook does with a batch of 10 or fewer, 16 KiB or less (shown
in full, acked); the digest still lists and does not ack (ADR 0046); `mash
inbox` and the MCP `inbox` tool still never ack; the channel write before any
hook still acks; the `[stale]` trust label (it compares the signature's age with
the stored time, `render` and `classify`, and has nothing to do with acks); and
the inbox stream's own limits (a message past 30 days or 16MB is gone, read or
not).

## 5. "Other?": the questions none of the three answer, and the names

Every question a person plausibly has, and the view that answers it (who = `mash
who` of the targets, list = `mashctl endpoint list` of ctl D15):

| question | answered by | who | list |
|---|---|---|---|
| Does mail work from this machine? | `mash status` | no | no |
| Which endpoint am I? | `mash status` verdict; `(you)` in `endpoint status` | the `*` row | no |
| Is mail waiting, how much? | `mash status` row `mailbox`; `mash inbox` | no | `show` has `unread mail` for ONE endpoint (admin) |
| Is a Claude Code session there? | `mash status` row `session` | `online` of your own row | `show` has `online` (admin) |
| Is my plugin installed, are its hooks set? | `mash status` warning, `--detail` row `claude code` | no | no |
| Is the server up and encrypted? | `mash server status`; the `server` row of `mash status` | no | `mashctl status` shows it (admin) |
| Is the server full, how long is my mail kept? | `mash server status`, `--detail` | no | `mashctl limits` (admin) |
| Who is online in my group? | `mash endpoint status acme.demo` | YES: collides | no |
| Is Carol there now? | `mash endpoint status acme.lab.carol` | YES: collides | no |
| Which endpoints exist in my owner? | `mash endpoint status` | YES: collides | YES: overlaps (admin sees more) |
| Who can do a thing (role, label, capability)? | `mash endpoint status --role ... --cap ...` | YES (the same flags) | `show` only |
| Which version do my peers run? | `mash endpoint status --wide` | no | no |
| Which endpoints are installed on this machine? | `mash status --detail` row `endpoint` names the one in use; with several installed and none chosen, `mash status` lists them in its fix (`status-choose`); once one is chosen no view lists the others | no | no |
| Is my plugin older than mash? | the `version` row's warning, as the targets word it; not redrawn here | no | no |
| Is my token about to expire? | `mash status --detail` row `token expires`; no warning for a near date is proposed (I could not determine that tokens expire at all in 0.5.6) | no | `issued list` (admin) |
| Did my message to carol arrive? | DECISION 249's question; what a sender sees today is below | no | no |
| Why didn't my session wake, and when was it last woken? | the `mailbox` row of `mash status` says when mash last woke it (4.1); the rest is DECISION 249's | no | no |
| Who may I mail? | everyone registered in your owner; `mash send` refuses the rest | no | `mashctl permissions` (admin) |
| Who is connected to the server, every inbox, the keys? | `mashctl` only (section 2) | no | `audit`, `issued` |

What a sender can see TODAY about mail they sent, from the code, short and
honest:

- `mash send` prints `sent #7 from acme.demo.bob to acme.demo.alice`
  (`cmd/mash/cli.go:133`). That means the server stored the message in alice's
  inbox and gave it that number. It does not say that alice's session received,
  was shown or read it.
- `mash endpoint status acme.demo.alice` says whether a session answers for
  alice now: `online`, or `offline` (her mail waits for the next session). That
  is the whole of it, and it is the answer to "is my mail to alice sitting?":
  if she is `offline` it is sitting, by design; if she is `online` mash cannot
  tell the sender (bob) whether the mail has been delivered, how many messages
  wait in her inbox (the credentials may not read another inbox, section 2), or
  whether her session was woken. The one-address screens say the part that
  matters in a `note:`: mash cannot tell whether she has read it.
- The only proof that a person or a session saw the mail is a reply.
- Everything beyond this (delivery state of sent mail, why a session did not
  wake, when it was last woken, from the sender's side) is decision 249. This
  page does not add it, and `mash endpoint status` shows none of it.

Three of the answers collide with `mash who`. One name per thing means `mash
who` goes. The alternatives:

| option | the set | for | against |
|---|---|---|---|
| A (recommended) | `mash who` is removed; `mash endpoint status` is the one live view of your owner's endpoints; `mashctl endpoint list` stays the administrator's registry | one name per thing, noun-verb as the standard says; liveness is `status` and an inventory is `list`; the maintainer's own proposal | `who` is the word people and agents know; a hard cut, but the targets already hard-cut `create`, `--long`, `--names` |
| B | keep `mash who` as the peer list and add `endpoint status` for ONE address only | no rename | two names for one set; breaks standard rules 1 and 6 |
| C | `mash endpoint list` (no probe) and `mash endpoint status` (probes); `who` removed | a cheap inventory | two views of one set differ only by a probe; a third name next to ctl's list |
| D | keep `mash who` only; add `server status` | smallest change | the maintainer asked for endpoint status; patterns need a new flag family |

Recommendation: A. What it makes of the neighbours:

- `mash who` becomes an unknown command with an error that names its
  replacement (the targets do this for removed verbs, section 5 "Unknown
  command"): `error: unknown command who.` `fix: the endpoints of acme, and who
  is online:` then `mash endpoint status`. No alias (standard rule 6). Its
  flags `--online`, `--offline`, `--role`, `--label`, `--cap`, `--match` move
  over; `--name` and `--owner` become the pattern; `--long` is `--detail` for
  one address and `--wide` for the list; `--by-group` is dropped (`acme.demo`
  says it).
- `mashctl endpoint list` is the registry: every endpoint the administrator
  made, with the date it was made, including those that never ran. It does not
  say who is online (that is `mash endpoint status`).
- `mashctl status` stays, as ctl confirmed (appendix): how the operator's tool
  is set up. Nothing about it is redrawn on this page; it is ctl's screen. For
  the person who runs the server, the server is `mash server status` (when they
  hold an endpoint's credentials) and `mashctl status`, `mashctl audit` and
  `mashctl issued` for everything an endpoint may not read.
- The agent's MCP tool `who` is the same view. One name per thing says rename it
  `endpoint_status` in the same release (7.2).

The set, as a table of things and names (the whole interface in six lines):

| the thing | the person's command | the administrator's |
|---|---|---|
| this endpoint, can it mail | `mash status` | (none) |
| the server | `mash server status` | `mashctl status` (the operator's tool; no second `server status`) |
| the endpoints of my owner, live | `mash endpoint status [pattern]` | `mashctl endpoint show`, `list` (registry) |
| one endpoint, in full | `mash endpoint status <address>` | `mashctl endpoint show <address>` |
| mail | `mash send`, `inbox`, `read` | `mashctl inbox inspect` |
| the machine's trust | (none in 0.5.6: no trust record) | `mashctl adopt` |

## 6. Fit with the CLI standard; Agree with ctl

The standard was found (`~/ops/standards/cli/STANDARD.md`, version 1.2.6, read
in full; the targets cite its numbered rules as "cli rule N"). It is not in the
repository, and the targets quote it by rule number. What I applied:

- Rules 1 and 2 (`<tool> <noun> <verb>`, one noun, singular): `mash server
  status` and `mash endpoint status` conform. `mash send`, `inbox`, `read`,
  `prompt`, `hook` do not, and the standard's own table says they move under
  nouns at mash's major revision; the targets keep them for 0.5.6, and I assumed
  so too.
- Rule 3 (top-level verbs): `mash status` is allowed as "a verb that acts on
  that thing itself in a tool named for a thing" (the `range status`
  precedent). Closed, not asked: the maintainer, the README and the quick start
  say `mash status`, and the rule's own exception covers it.
- Rule 4 (the verb table): `status` is "one object's live state, or a summary
  of all of them with no name given"; `list` is "the objects of this noun, one
  per line": `mashctl endpoint list`.
- Rule 6 (no aliases): `mash who` is removed, not kept beside `endpoint status`.
- Rules 7 and 8 (the object's name is the positional; an optional name acts on
  what the context gives and says which): the pattern is the one positional;
  the verdict line says `3 endpoints of acme`.
- Rule 10 (`--json` on every read command): each view has it.
- Rules 24 to 28: default answers only what was asked; `--detail` is more about
  the same thing, `--wide` is more columns of a list; no preamble, no legend;
  aligned tables; 80 columns, with `--wide` exempt.
- Rule 14 (exit codes): 0, 1, 2, 3 cancelled, 4 not authorized, and the
  contract's 5 and 6; I use 0, 1, 2, 4, 6.

### Agree with ctl

Not agreed with the mashctl lane except where ctl's answer (appendix) says so.
The questions I would put to them, each with a proposal. (ctl D15 as cited:
`status` 1458-1580, `endpoint list` and `show` 2452-2656, `issued list`
4918-4975.)

1. One screen, two tools. Both show reachable, encrypted, certificate.
   Proposal: the row labels and values for those facts are written once
   (`address`, `state`, `encryption`) and used by both; ctl adds the
   administrator's rows. Their first line is `OK` or `PROBLEM: ...` where
   mine is `The server is reachable and encrypted.`: both use a sentence. ctl
   prints `label:` with a colon, mash prints the label alone: one style, without
   the colon. Agreed?
2. `endpoint list` against `mash endpoint status`. ctl says the two share four
   leading columns (OWNER GROUP ENDPOINT ADDRESS CREATED). This page's list has
   one address column, `ENDPOINT`, and `STATE`; the owner and group are in the
   address. Proposal: both use `ENDPOINT` (the dotted address) and neither
   repeats it as separate OWNER, GROUP and ADDRESS columns, since a person
   pastes the address. Does `list` include endpoints that never ran (it reads
   the registry, so it should), and every owner of the account or only the
   configured one? My proposal: registry, every endpoint, never-seen included,
   no liveness column.
3. SETTLED with the lead, confirmed by ctl in its answer: ONE name,
   `mashctl status` stays (how the operator's tool is set up; it never repeats a
   server fact except the one reachable/encrypted line that explains a failure),
   next to `mash server status`. `mashctl server status` is not proposed.
4. The grammar. RULED (the planner), and ctl mirrors it: one grammar for both
   tools, stated once in section 4.3, with `*` inside a part (`acme.demo.b*`)
   and the cut-short form (`acme.demo`) added to ctl's answer. The one
   difference to keep is scope, not grammar: `mashctl endpoint list` may take
   another owner, or `*` for any, since an administrator sees the whole
   account.
5. `HOST`. In `list --wide` it is the directory's `host` (the machine the
   endpoint is meant to run on, set by `mashctl endpoint set --host`): ctl D15
   shows `-` for bob until it is set. In `mash endpoint status --wide` it is the
   same field. Proposal: both tools label it `meant to run on` (the column is
   `MEANT TO RUN ON`; the JSON key stays `host`), and neither says `runs on` or
   a bare `host`: a bare `host` reads as where it runs now.
6. Words. ctl `show` says `unread mail`; this page says `waiting` (section 4.4
   defines it: not yet acknowledged, and delivery acknowledges). And where does
   ctl read `online:` for an endpoint (an administrator may ping `$SYS`, an
   endpoint may not): by the same `$SRV.INFO` probe? If so say
   `online`/`offline` in both tools, the same two words, one source.
7. Flags. ctl uses `--detail` on `status` and `show` and `--wide` on lists
   (standard rule 24); `mash endpoint status` does the same.
8. Exit codes for `server status`: ctl says 0 OK, 1 PROBLEM, 6 NOT REACHABLE.
   Mine are the same; a credentials problem does not change the server's exit
   (section 4.2).
9. Counts on `mash server status`. ctl's answer says "no counts"; this page
   shows `mail storage` (default) and, with `--detail`, `mail stores` and
   `endpoints` (counts), because the maintainer asked for storage and limits "if
   a person can read them without admin credentials" and section 2 shows they
   can. Is that agreed, or does ctl want them only in `mashctl`?

## 7. Open questions for Randy

Every item carries one mark. SETTLED: the lead or the contract has ruled.
RULED: the planner has ruled. CLOSED: answered on this page, nothing left to
decide. OPEN: Randy or ctl has not ruled; the recommendation is given.

1. SETTLED as the proposal (the lead accepted it; it goes to Randy as "hard cut
   of `mash who`", his ruling is not yet in). The set of names: option A of
   section 5. The "Other?" of the comment is answered by the table in section 5.
2. OPEN. The agent's `who` tool. Recommendation: rename it `endpoint_status` in
   0.5.6 (one name per thing, the JSON already changes; the plugin's
   instructions are regenerated). The alternative is to keep `who` for agents
   only, which leaves two names for one view for the life of the tool.
3. SETTLED (the planner's ruling): no server detail in `mash status`, not even
   in `--detail`; the one line `server details  run: mash server status` points
   to it.
4. OPEN, top item 1: heartbeat history. Is "answers now / does not answer"
   enough for 0.5.6, or should mash store a last-alive time (new: it needs
   either every endpoint to write one to the server, a protocol and permission
   change that D229 rules out without Randy, or every listener to subscribe to
   heartbeats again, which ADR 0066 removed)? Recommendation: enough for 0.5.6;
   nothing in this page needs history.
5. OPEN. `meant to run on`. It is the directory's `host` field, set by an
   administrator, the machine an endpoint is MEANT to run on
   (`store.go:233-235`), not where it runs. Only your own endpoint's live host
   is readable (`serve.go:914-922`). Recommendation: show it as `meant to run
   on` (the JSON key stays `host`) in `--wide` and one-address views, never
   `runs on`, with the one-line note under the block, and show `session started
   ... on localhost` (live) only for your own endpoint in `mash status
   --detail`.
6. RULED (the planner's ruling; ctl mirrors it), top item 2: the pattern
   grammar, one for both tools: `*` inside one part, the short group form
   `acme.demo`, three dotted parts otherwise, never across owners in mash,
   quoted in examples (4.3). The alternative that was rejected is a `--group
   demo` flag (standard rule 7: scope as a flag), which cannot say
   `acme.*.bob`.
7. OPEN. The table limit. Recommendation: 40 rows by default, sorted by
   address, with `--all`; the verdict line counts everything and your own row
   is always printed.
8. OPEN. `mash server status` shows the ACCOUNT's storage, which every owner in
   the account shares (ADR 0006). Recommendation: show it, because a full
   account is the one server problem a person can hit; the label says nothing
   about owners.
9. OPEN. Offline exits 0, and so does a pattern that matches nothing. A script
   that wants a gate uses `--json`. Recommendation: keep 0.
10. SETTLED, the lead (rev 21, contract rev 26): the mailbox count is
    unacknowledged mail, and delivery acknowledges (4.4). The code changes (the
    `read` tool drains any waiting message; `mash read` keeps the tombstone;
    serve writes `backlog_woke_at` into the endpoint's own lease) amend ADR 0041
    and need an ADR. A one-line summary does not acknowledge and still counts.
    Nothing is open: the lead ruled that a direct acknowledgement from the
    shell is not in 0.5.6 (4.4, item 2).
11. OPEN. The certificate's end date. Recommendation: show it in `mash server
    status --detail` and warn within 30 days; mash reads no certificate field
    today (section 2), so it is new code but no new permission.
12. CLOSED: rule 3 of the standard and `mash status`; the exception covers it
    (section 6).
13. CLOSED: the server address. `mash status` prints no server address except
    in the two override screens, where the value that overrides is the thing
    to check; for every failure it points to `mash server status`, the one
    place that shows the address.
14. CLOSED: the `MAILBOX` of other endpoints. No column (stated once, in 4.3).
15. CLOSED by the same ruling: with `mailbox_limit` 0 the row says `3 waiting,
    no limit set`, and a `note:` says what the limit is.
16. OPEN, small. Time format (section 1): local time on default screens, with
    the offset behind `--detail`. Recommendation: as written.
17. OPEN, small. `mash status` now probes presence for the `session` row: about
    0.3 s more when a session answers, up to 2 s more when none does (section
    3). Recommendation: accept; test the one-address "no responders" shortcut,
    which would make it milliseconds.

## 8. What this changes in the targets doc and the contract

Not edited by this page. The sections and blocks that would change:

- Rule R11 (the default screen): the verdict and SIX rows (`version`, `server`,
  `config`, `credentials`, `mailbox`, `session`); the server row is one phrase
  (`reachable, encrypted`, `reachable, NOT ENCRYPTED`, ...), no address, no
  time; the two and three line forms, the `checked 09:42` and `last reached`
  texts and the environment lines in the row go; the override moves into the
  `warning:`/`fix:` text. `--detail`: REQ-012's rows stay without the trust
  rows and the `group` fact; added: `waiting means`, `mailbox limit`, `summary
  only`, `session started`, `server details`. The `mailbox` row is `<n>
  waiting`, `..., OVER THE LIMIT OF <limit> (<wake>)`, `..., no limit set`,
  `not checked`, in every block, with the over-the-limit blocks, their `fix:`
  and the `no limit set` note added.
- The environment rule (section 4.1): the targets' DRAFT11 says `mail does NOT
  work: MASH_SERVER overrides config.json` for a differing value; this page says
  `not checked`, because mash did not check (critic, defect 7), and the fix says
  the value is IGNORED. One word to change in the targets' verdict forms.
- R14 and R3: `endpoint status` columns (`ENDPOINT`, `STATE`; `--wide`:
  `VERSION`, `MEANT TO RUN ON`, `ROLE`) and the pattern; "the address is the
  dotted path" gains the pattern form. The label `host` becomes `meant to run
  on` wherever the targets print the directory's host.
- R20: "one place keeps naming the server ... the rows of `mash status`" (N19)
  is now `mash server status`; `mash status` prints no address (the
  unreachable fix points to `mash server status`).
- Section 5 `mash status`: the lead paragraph; every block takes the six rows;
  the `--detail` blocks take the new fact rows; "Changes (status)" is rewritten.
- Section 5 `mash who`: replaced by `mash endpoint status` (the blocks of 4.3
  here); `mash who` in "Unknown command" names it; N3 closes by the pattern; N13
  is answered (the directory holds every owner; presence is owner-only).
- New sections `mash server status` and `mash endpoint status` (4.2 and 4.3).
- Help: `who` leaves "Mail"; `endpoint status` and `server status` lines appear;
  `status` stays under "Set up".
- The mailbox section of the targets (DRAFT11): O1 (a one-line summary counts
  and does not acknowledge) stands, O2 is reversed by the lead's rev 21
  (reading acknowledges and lowers the count; `mash read` through the
  tombstone), and "What each command does with
  it" gains the lease's `backlog_woke_at` (no `backlog` field: the count has one
  source, 4.4); `mash read --detail` `state` is true only after change 2 of 4.4
  (the tombstone).
- The contract, rev 26 (the final one). REQ-015 already has the definition of
  4.4 (unacknowledged; shown in full or read is acknowledged; a one-line
  summary is not), the `mash endpoint status` rule (stated once, in 4.3) and
  `no limit set`. This page adds to it: (1) the row wording, `<n> waiting,
  OVER THE LIMIT OF <limit> (woke the session at 09:40)`, the contract's
  capitals with the wake time added; (2) the recorded wake time,
  `backlog_woke_at`. The contract already has the one-address view of your own
  endpoint showing the mailbox row, and `mash read` marking the message read
  locally (the count drops when the next session starts). REQ-004's `mash
  server status: address, alive, encrypted, last seen` and `mash endpoint
  status: ... heartbeat or last seen` become the rows and the `online`/`offline`
  of this page (no last seen). REQ-012: its list is unchanged; the fact rows of
  4.1 are added. REQ-010's variable name is used throughout
  (`MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION`); there is no trust record.
- Section 6 MCP: `who` becomes `endpoint_status` (7.2) with the pattern
  argument and `mailbox` for the caller's own entry; `status` takes the six rows
  (`mailbox: {waiting, limit, oldest_age, age_limit, over_limit,
  over_age_limit, woke_at}`, `session: online|offline`); new tool
  `server_status`; the initialize `instructions` text names the new tools.
- Section 7 and 8 of the targets: N10 (`last reached`) closes for `mash status`
  and is dropped for the list (no last seen); N13 closes; the behaviour changes
  list gains: `who` removed, `status` reads the mailbox number and probes, two
  new commands, `mash read` and the `read` tool deliver, the lease gains one
  field.
- The READMEs: every healthy `mash status` they quote takes the six rows.
- Section 9 (checks) and the dispositions file: rows for these findings.

## 9. What I could not verify, and checks run

What I could not verify:

- Another owner's agents cannot answer an endpoint's probe. It follows from the
  permission list (`permissions.go:19-24,63`) and its comment; I tested only one
  owner, so the claim for two owners is a reading, not a run. That `mash who`
  lists other owners' records is a reading of `directory.go:89-103` and
  `client.go:234-258`, also not run.
- A directed request to `agents.status.<owner>.<group>.<name>` failing at once
  with "no responders" when nothing serves (it would make the one-address
  `online` check cost milliseconds, section 3): not run.
- `reserved_storage` (125MB in the run): not shown on any screen until it is
  known what counts toward it.
- The certificate fields (end date, issuer, the cause of a refusal): my run used
  the plain test server, so no certificate was read. Go's API gives them; mash
  reads none today.
- Build-time checks. Everything this page could not determine is one table,
  "Verify at build", in `docs/plans/2026-11-19-cli-targets-mash-decisions.md`
  (item, why it matters, who checks, how). The list below is what I did not
  run, with the reason.
- Section 4.4. I read the code paths of every ack, and did not run the changes.
  Not run (a direct acknowledgement is not in 0.5.6; its checks are kept for
  later in "Verify at build"): that a shell can receive a message with the pull
  request and ack it, that the naks of the others redeliver at once, how long it
  takes to reach a message deep in a backlog, and whether a server accepts the
  ack of a message received by a client that exits at once. Not run, for the
  tombstone, which 0.5.6 keeps: that a tombstone written by `mash read` is acked
  by a running serve within 250 ms, and by a starting serve on redelivery; that
  the spool directory exists for an endpoint whose session has run on this
  machine and not for one whose session never has; that a tombstone for a
  message the stream has since expired is pruned (I did not find where, so it
  may leak a few empty files); the lease's new field and its freshness (the
  throttle on lease writes is `WakingWriteMin`, 5 s, `waking.go:36-38`).
- Per-message `new`/`read` from a shell: the consumer reports counts and an ack
  floor, not which message above the floor was acked. The number is exact; the
  per-message state is exact only at or below the floor.
- Whether Claude Code reliably puts a hook's context in front of the model: not
  testable from the code (the 10,000-character cut is handled,
  `claude.go:211-233`).
- The plugin-drift warning's fix command (`mash plugin install` is out of 0.5.6)
  and whether a token can expire at all in 0.5.6: not determined.
- The exact command after an untrusted certificate (move the old CA aside and
  install again) follows REQ-003's rule as I read it; I did not run install.
- Whether a `listset` policy (`internal/listset/compile.go`) narrows who an
  endpoint may mail beyond its owner; the permission set alone says "any
  endpoint of the same owner".
- How ctl reads `online` and `unread mail` for another endpoint: ctl D15 does
  not say.
- A multi-server cluster: ADR 0066 measured three servers, the maintainer says
  one; a cluster would need a sentence about replicas (stream info has
  `cluster.leader`).
- The in-session MCP status: not run; the six facts are the CLI's.
- The test server's numbers (7 streams of 50, 256MB, 64MB memory) are the
  repository's test fixture, not a production server's.
- `mail storage  NEARLY FULL` past 90% of its limit rests on the same
  unverified accounting as the reserved storage.
- `token expires  never`: whether a token can expire at all in 0.5.6 is not
  determined; the row is a target and its wording follows the contract's token.
- The sentence "close the session, then start Claude Code with the mash
  channel" (`status-claude`): the channel is chosen when Claude Code starts, so
  a running session cannot gain it; I did not run it.
- `7 of 50 in use (3 inboxes, the rest are mash's own)`: the 3 is the example's
  endpoints; what the other stores are in a production server was not listed.
- Every output in this document is a target, not output of a binary.

Checks run: the facts of section 2 were obtained by a throwaway test against the
in-process test server in a scratch clone of v0.5.5 (deleted; the clone
unchanged); the denied subjects were observed as timeouts, the granted ones as
replies. For section 4.4 I read, without running: the consumer config, the
receive path, the hook, the one-line summary (digest), the `read` tool, `mash
read`, `mash inbox`, the channel path, the lease record, the audit's consumer
fields, the permission list (`permissions.go:67-69` for the pull and the ack),
and ADR 0041, 0046, 0066 and 0069.

DRAFT5c adds one block, `status-mailbox-age-over`, and a line to `status-detail`
(37 fenced blocks in all, 36 before; the widest printed line is
still 78: re-measured, none is over 80).

DRAFT5b, measured (a Python 3 script, `-I`, characters per line of every fenced
block, the `$` blocks and the JSON block included; a second pass checks that
every key/value block has one value column; a third lists prose lines over 80
outside tables): 36 fenced blocks, the same 36 as DRAFT4. NO printed line is
over 80 columns. The widest is 78 (`status-detail`, `status-refused` and
`server-untrusted`). `status-noconfig`, whose command was 121 columns in
DRAFT4, is now 76: the command is continued over two lines with a backslash and
no line of it is over 80. The new `mash status` blocks: `status-mailbox-over`
76, `status-mailbox-over-offline` 76, `status-mailbox-off` 76. `mash endpoint
status`: the default tables are 41 columns or fewer, the `--wide` table 69, the
real-name table 56 (DRAFT4 said both 54 and 56), the help block 75, the
one-address blocks 73 to 77, the JSON block 72. Every key/value block has one
value column, status-detail's at column 20. Prose lines outside tables and
quoted text: none over 80. Tables in this file are not measured (a markdown
table row cannot wrap; each is one line of the file).

## 10. Disposition of the findings

DRAFT5 and DRAFT5b answer the planner's check of DRAFT4, the junior's read of
DRAFT4, and the lead's items a to f and rulings 1 to 8 (rows below, after the
older ones). One row per finding. FIXED: the page now says what the finding
asked. CHANGED: done differently, with the reason. NOT TAKEN: not done, with the
reason. Defects are the critic's numbers (D, 1 to 18); "critic A to E" are its
sections; "junior" is its part 3 unless named.

| finding | status | where, and what |
|---|---|---|
| critic D1: one fact, two names, two sources | FIXED | `online`/`offline`, from the presence probe, in `mash status` (section 4.1) and `mash endpoint status` (4.3); rule 7. The lease is not the state source: it can be 90 s old, and cannot be read for another endpoint |
| critic D2: the count cannot be lowered | FIXED | 4.4: delivery acknowledges (shown in full, the `read` tool, `mash read`), so reading lowers the count, with or without a session; the screens carry a `fix:` with `mash inbox` and `mash read` |
| critic D3: the wake is not shown | FIXED | the `mailbox` row itself says `woke the session at 09:40` (lead ruling 4); the time is read from the lease (4.4) |
| critic D4: `off` and `LIMIT` mislead | FIXED | `no limit set`, with a `note:` on that screen; "limit" is explained on every screen that shows it as the number that triggers a wake, and is not a capacity |
| critic D5: no-match is both legitimate and an error | FIXED | `endpoint-status-nomatch`: exit 0, stdout; an unregistered full address stays exit 1 |
| critic D6: two timeouts | FIXED | one value, 2 s, and one sentence in all three views; the source of the number is stated once (section 1) |
| critic D7: a verdict not true as written | FIXED | `not checked: MASH_SERVER is not the server in config.json`; one environment rule in 4.1; the fix now says the value is IGNORED and how to remove it; the targets need the same change (section 8) |
| critic D8: `mashctl status` contradicted | FIXED | the superseded item and the appendix's closing paragraph are gone; settled in section 6, item 3 |
| critic D9: two pattern dialects | FIXED | one grammar for both tools, ruled by the planner (ruling 7): stated once in 4.3, ctl mirrors it; the one difference is scope (mashctl may name another owner) |
| critic D10: `--json` drops "here" | FIXED | by removal: no `last_seen` anywhere |
| critic D11: "last seen here" claims more than it knows | FIXED | by removal (planner's ruling): no last-seen by default and no local record; the question to Randy is at the top; `--detail` says `heartbeats  not recorded` |
| critic D12: the same fact twice on one screen | FIXED | `status-detail` has no server or session fact rows (one pointer line); the certificate date appears once |
| critic D13: a legend under every list | FIXED | no legend; `endpoint-status-help` holds the definitions; the caller is marked `(you)` |
| critic D14: the `MAILBOX` column is `unknown` | FIXED | the column is gone; REQ-015 (rev 26) says so and 4.3 states it once; the contract differs from this page only in the one-address view of your own endpoint (section 8) |
| critic D15: `server status` fails on a credentials problem | FIXED | `server-no-credentials`: the server's verdict, exit 0, storage row `not checked (...)` |
| critic D16: the `status-choose` fix is not pasteable | FIXED | prose for the file edit, the command for the shell; indents 5 and 2 |
| critic D17: two alignments on one screen | FIXED | one value column per screen (rule 9) in every block, including `status-detail` (the `listed, not read` row is now `summary only`, at the common column) |
| critic D18: `mail storage` may overstate the room | FIXED | `1KB in use`; the reserved storage is not shown until verified |
| critic A: "encrypted" in his sketch | FIXED | the healthy row is `reachable, encrypted` |
| critic A: tie the dropped group counts to his "if that takes too long" | FIXED | 4.1, "Not rows" |
| critic A: `--match` unexplained | FIXED | 4.3: a search of description and capabilities, no longer the address |
| critic B: ctl's "same four leading columns" unreconciled | CHANGED | section 6, item 2: one `ENDPOINT` column; asked of ctl |
| critic B: `*` is both wildcard and "you" | FIXED | the marker is `(you)` |
| critic B: capitals on a warning | CHANGED (DRAFT5b) | the lead ruled problem values stay in capitals (REQ-004): the row is `OVER THE LIMIT OF 15`; rule 5 reversed |
| critic C: jargon in `--detail` | FIXED | trust rows, `relays seen` and `name rule` are gone; `storage name` is explained; `account` and `generation` are gone with the trust record |
| critic C: default `server status` rows | FIXED | four rows; clock, limits and counts behind `--detail`; a wrong clock is a warning |
| critic C: `endpoints 3 registered` vs "3 of acme" | FIXED | `3 registered, all owners` |
| critic C: `warning:` runs into `fix:` | NOT TAKEN | decided once: they are one block, as in the targets, and a blank line separates it from the rows |
| critic C: "your server made" credentials | FIXED | `status-noconfig` |
| critic C: `serving since` has no zone | FIXED | the times rule (section 1) |
| critic E: did my message arrive; why no wake; which endpoints here | FIXED | section 5, with what a sender sees today and what is decision 249's |
| critic E: plugin drift, token expiry have no screen | CHANGED | answered in section 5; no new block (the fix command and token expiry could not be determined, section 9) |
| critic E: `PROGRAM`, the legend, `relays seen`, `name rule` | FIXED | all removed from the default views |
| critic E: close question 12 | FIXED | closed (7.12) |
| critic polish: say "you said regexes" in the open | FIXED | top item 2 |
| critic polish: accept `acme.demo` | FIXED | 4.3, `acme.demo` is `acme.demo.*` |
| critic polish: zsh and unquoted `*` | FIXED | top, 4.3, help |
| critic polish: orphan "decision 249" line | CHANGED | a plain sentence at the top says what decision 249 owns |
| critic polish: stale appendix note | FIXED | removed |
| critic polish: bare codes | FIXED | R-numbers and N-numbers are the targets' rules and notes, REQ-nnn the contract's, D229 decision 229 (no SAP protocol change); stated in "Sources" |
| junior: 2 s against 20 s | FIXED | section 1: 2 s for the status views, 20 s is 0.5.5's default and the reason for the shorter bound |
| junior: `MASH_SERVER` has two verdicts | FIXED | one rule, 4.1 |
| junior: the leftover `mashctl server status` paragraph | FIXED | deleted |
| junior: what `*` means | FIXED | the grammar, once; `(you)` is the marker |
| junior: bob serving and offline | FIXED | one word, one source; bob is online in every block except `status-offline` and `status-mailbox-over-offline`, which say so |
| junior: what the mailbox number counts, the limit, the one thing to do (Q5) | FIXED | `status-mailbox-over` and 4.4 |
| junior: Q7, "is my mail to bob sitting?" | FIXED | section 5: what mash can and cannot tell a sender today; the rest is decision 249 |
| junior: Q8, two patterns at once | FIXED | one address or pattern per command; run it twice |
| junior: Q10, the person who runs the server | CHANGED | section 5 and the set table name `mash server status`, `mashctl status`, `audit`, `issued`; no `mashctl` screen is drawn here, it is ctl's |
| junior: `session not running` has no block | FIXED | `status-offline` |
| junior: `serving` unexplained; 09:12 without a zone | FIXED | `online`; the times rule |
| junior: the `--dangerously-load-development-channels` flag | FIXED | the flag stays (Claude Code requires it); the fix says what the channel is and to close a running session first |
| junior: "token", "CA fingerprint" | FIXED | explained where they appear, in the `status-noconfig` fix and the `--detail` rows (lead item d) |
| junior: the unreachable fix prints the address in one view and not the other | FIXED | planner ruling 5: `mash status` prints no address and points to `mash server status` (the one place for the address); `mash endpoint status` does the same |
| junior: my mail works but someone does not answer: nothing points on | FIXED | the one-address screens carry a `note:` telling a sender what mash cannot know (lead item c) |
| junior: counting detail rows, section letters, broken line, "at a terminal" | FIXED | the sections are rewritten |
| junior: the cast shares one host name | CHANGED | one-machine blocks read `localhost`, as the targets do |
| planner check, defect 7 (the env-override fix said "is overriding") | FIXED | `status-env-override`: the fix says mash IGNORES the value and asked no server, and how to remove it (`unset MASH_SERVER`); ruling 1 |
| planner check, defect 14 (REQ-015 amended and not) | FIXED | stated once, in 4.3, as the contract (rev 26) says it; section 8's "open difference" and "the contract stands" are deleted; 7.14 points to 4.3 (lead item a, ruling 2) |
| planner check, defect 17 (status-detail alignment) | FIXED | every row of `status-detail` is at column 20 |
| planner check, R1 (`no limit set`, "limit" undefined) | FIXED | the `note:` under `status-mailbox-off` and the `mailbox limit` row of `--detail` say what the limit is (ruling 4) |
| planner check, R4 (grammar not one) | FIXED | ruled and stated once in 4.3, "ruled; ctl mirrors it" (ruling 7) |
| planner check, R6 (unreachable fix prints the address) | FIXED | no address in `mash status`; `fix: ... details, with the address mash tried: mash server status` (ruling 5) |
| planner check, R7 (installed endpoints row; cast mixup) | FIXED | section 5: the row says what is true (listed only when none is chosen); the sender bullet is alice's, from bob's side |
| planner check, R8 and step 5 item 1 (two sources for the count) | FIXED | one source, the consumer's number, stated once in 4.4; the lease's `backlog` field is gone (ruling 3) |
| planner check, step 5 item 2 (`mash read` offline does not lower the count) | CHANGED (DRAFT5b) | with no session running reading marks the message read here and the count drops when the next session starts; the screens say so (4.4, item 2) |
| planner check, step 5 item 5 ("the session row probes for one endpoint") | FIXED | 4.1 "Not rows": the `session` row uses the same probe; group counts are left out because they are about other endpoints |
| planner check, step 5 item 7 ("when the server is back") | FIXED | the sentence is deleted; the unreachable screen does not print the address |
| planner check, step 5 item 9 (help omits `--detail`) | FIXED | `endpoint-status-help` explains every flag and has `--detail` (lead item f) |
| planner check, step 5 item 10 ("bob online in every block") | FIXED | the cast paragraph and the junior row name the two blocks where bob has no session |
| planner check, step 4 (unlabeled hint under the long list) | FIXED | `endpoint-status-many` ends with a labelled `next:` and the command on its own line; rule 8 defines `next:` and `note:` |
| planner check, step 4 (prose leftovers) | FIXED | "no no", the dangling "woke the session;", the stray indents and the broken table rows are mended; the 125-column line of section 8 is gone; the 121-column command is continued with a backslash |
| planner check, step 3 (group counts, reason weak) | FIXED | the reason is now that the counts are about other endpoints (4.1 "Not rows") |
| lead (a): section 8 stale | FIXED | section 8 agrees with 4.3 and the contract, rev 26 |
| lead (b): over-limit screens need a concrete action | FIXED | `status-mailbox-over` and `status-mailbox-over-offline` have a `fix:` (look at the session's terminal; start a session; read the mail with `mash inbox` and `mash read <number>`) |
| lead (c): tell a sender what mash cannot know | FIXED | a `note:` on every one-address screen of another endpoint: mash cannot tell whether it has read your mail; only a reply shows that |
| lead (d): explain digest, wake, heartbeats, CA, token, account key where they appear | FIXED | "a one-line summary of the message" in every screen; wake in both warnings; heartbeat at the top and in `last alive`; CA, token and account key in `status-noconfig`; token, CA and commit in `--detail`; channel and `nats://` where they appear |
| lead (e): the `host` label | FIXED | `meant to run on` (column `MEANT TO RUN ON`; JSON key `host`) with a `note:` line; asked of ctl (6.5) |
| lead (f): the endpoint status help | FIXED | each flag explained, `--detail` included; `online`/`offline` defined once there, and `mash status --detail` points to it |
| lead ruling 1: env override | FIXED | see planner check, defect 7 |
| lead ruling 2: REQ-015 once | FIXED | see planner check, defect 14 |
| lead ruling 3: one source; `mash read` acks directly | CHANGED (DRAFT5b) | 4.4; the lead ruled no direct ack in 0.5.6: `mash read` keeps the tombstone (item 2) |
| lead ruling 4: limit explained once per screen; no "digest"; wake time in the row | FIXED | `status-mailbox-over` and the other screens; the row reads `(woke the session at 09:40)` |
| lead ruling 5: no address when unreachable | FIXED | `status-unreachable` |
| lead ruling 6: say honestly what the endpoint list can show | FIXED | top item 1, next to the heartbeat sentence; the list is not padded. One change from the ruling's words: "group" is not a `--wide` column, it is in the address |
| lead ruling 7: one pattern grammar | FIXED | 4.3, once, marked ruled |
| lead ruling 8: the rest of the planner's check | FIXED | see the planner rows above; every printed line is at most 80 columns (section 9) |
| junior: `2 waiting` means what, on the healthy screen | FIXED | `--detail` has `waiting means`; the warnings define it |
| junior: `session online` defined only in another command's help | FIXED | `mash status --detail` says where: `mash help endpoint status` |
| junior: `commit`, `nats://`, `the mash channel`, `token expires` | FIXED | each explained in its screen |
| junior: `server details` looked like a fact, not a command | FIXED | `run: mash server status` |
| junior: `mail storage` has no limit on the default screen | NOT TAKEN | one place per fact: the limit is the `storage limit` row of `--detail` |
| junior: `mail stores 7` beside `3 registered` | FIXED | `7 of 50 in use (3 inboxes, the rest are mash's own)` |
| junior: `clock 1s ahead`: is that fine | FIXED | `(only 10s or more matters)` |
| junior: heartbeats row under an internal word | FIXED | the row is `last alive`; the heartbeat is explained at the top |
| junior: rule 8 against the pointer rows; rule 5 against NOT ENCRYPTED | FIXED | rule 8 now says what `next:` and `note:` are and that a screen ends at its rows; rule 5 allows capitals for a security risk |
| junior: `token expires never` while section 9 says unknown | NOT TAKEN | the row is a target; section 9 still says it is not determined |
| junior: `ca file` against `CA` | NOT TAKEN | labels are lower case, prose uses the word CA |
| junior: two screens for "deleted" | NOT TAKEN | which appears depends on what the server says (N11); both are targets |
| junior: whether the running session closes first (status-claude) | FIXED | the fix says close it first (not run, section 9) |
| junior: "Question 2" meant two things; marks missing; rev and DRAFT numbers; who is who; 54 against 56; two DRAFT paragraphs; "Proposed to Randy" not a sentence; broken rows | FIXED | "top item N" and "7.N"; every item of section 7 has one mark; contract rev 26 and targets DRAFT11 throughout; a who-is-who paragraph; 56; one measurement paragraph; sentences mended; rows joined |
| DRAFT5b, lead 1: `mash read` direct ack | CHANGED | not in 0.5.6; tombstone kept; every sentence and both over-the-limit screens say the count drops when the next session starts |
| DRAFT5b, lead 2: capitals | FIXED | `OVER THE LIMIT OF 15` in every block, row list, rule 5 and section 8 |
| DRAFT5b, lead 3: own-endpoint one-address view | FIXED | the flag is removed from section 8 (the contract says it now) |
| DRAFT5b, lead 4: open items | FIXED | "Verify at build", a table in the targets decisions file; one pointer in section 9 |
| DRAFT5c, the age trigger (Randy, 11:30Z; contract rev 26 REQ-015) | FIXED | `status-mailbox-age-over`, the second line of `mailbox limit` in `--detail`, 4.4 item 5 (the predicate and the build plan), the MCP object; the shell's one direct get is "Verify at build" (j) |
| junior: sections 2, 3, 4.4 need the code to read | CHANGED | they are for a reviewer; the words a person needs are in the screens |

## Appendix: the CLI team's answers, quoted unchanged

Received after DRAFT1 was written. The "Agree with ctl" section above lists my
questions; these are ctl's answers (mail of 2026-11-19, 09:03 and 09:04Z). Where
my proposal goes beyond them, section 6 says where.

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
