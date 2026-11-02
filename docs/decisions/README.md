# Decisions

mash's architecture decision records (ADRs), in [MADR 4.0](https://adr.github.io/madr/) format. [0002](0002-record-architecture-decisions-as-madr-records.md)
says how they work: one decision per file, never rewritten once accepted, superseded by a new ADR instead. A pull
request that makes or reverses a decision carries its ADR.

0003 to 0037 were backfilled on 2026-10-31 from the design spec, pull requests, issues and maintainer discussions.
Each gives the date the decision was made and its sources. Where the options weighed were not written down at the
time, the section says "reconstructed"; where no reason was recorded, the ADR says so. "Not yet true" marks where the
code or docs do not match the decision yet.

The **Layer** column says where a decision sits: NATS (the message bus), SAP (the Synadia Agent Protocol mash builds
on), or mash's own choice on top of them.

| ADR | Title | Status | Layer | Date |
|---|---|---|---|---|
| [0001](0001-publishing-is-one-resumable-command.md) | Publishing a release is one resumable command, and a draft exists only while it runs | accepted | project | 2026-10-31 |
| [0002](0002-record-architecture-decisions-as-madr-records.md) | Record architecture decisions as MADR records | accepted | project | 2026-10-31 |
| [0003](0003-sap-plus-mail-verb-and-jetstream-durability.md) | mash is SAP 0.3 plus one verb and JetStream durability; it invents no protocol | accepted | SAP + NATS + mash | 2026-10-21 |
| [0004](0004-mail-is-a-declared-sap-verb.md) | `mail` is a declared SAP verb, not a registered micro-service endpoint | accepted | SAP / mash | 2026-10-21 |
| [0005](0005-subjects-follow-sap-hierarchy.md) | Subjects follow SAP's hierarchy, `agents.<verb>.cc.<owner>.<name>` | accepted | SAP | 2026-10-21 |
| [0006](0006-one-shared-nats-account-one-owner-per-deployment.md) | All endpoints share one NATS account; one owner per deployment; names are unique in the account | accepted | NATS / mash | 2026-10-21 |
| [0007](0007-one-nats-user-per-endpoint-least-privilege.md) | One NATS user per endpoint, and a fixed least-privilege permission set is the whole security model | accepted | NATS / mash | 2026-10-21 |
| [0008](0008-mash-never-mints-users-or-holds-seeds.md) | mash never mints NATS users or holds operator or account seeds; credentials stay 0600 on the endpoint's host | superseded by [0052](0052-mashctl-mints-nats-users-and-holds-the-account-signing-key.md) | NATS / mash | 2026-10-21 |
| [0009](0009-one-inbox-stream-and-consumer-per-endpoint.md) | One inbox stream and one durable pull consumer per endpoint | accepted | NATS / mash | 2026-10-21 |
| [0010](0010-inbox-limits-and-append-only-history.md) | Inboxes keep 30 days or 16 MiB, drop the oldest, and deny delete and purge | accepted | NATS / mash | 2026-10-21 |
| [0011](0011-directory-is-the-root-of-identity-and-addressing.md) | The directory KV bucket is the root of identity and addressing | accepted | mash (bends SAP §2) | 2026-10-21 |
| [0012](0012-endpoint-chosen-per-launch-by-mash-name.md) | An endpoint is chosen per launch by `MASH_NAME`; without it the server is inert | accepted | mash | 2026-10-21 |
| [0013](0013-one-live-session-per-name-by-lease.md) | A lease KV entry allows one live session per name (TTL 90 s, refresh 30 s) | accepted | NATS / mash | 2026-10-21 |
| [0014](0014-held-name-waits-dead-holder-taken-over.md) | A session that finds its name held waits and retries; a dead holder on the same machine is taken over | accepted | mash | 2026-10-23 (extended 2026-10-28, 2026-10-29) |
| [0015](0015-deprovision-fences-the-name-with-a-lease-tombstone.md) | Deprovisioning fences the name with a lease tombstone before removing anything | accepted | mash | 2026-10-27 |
| [0016](0016-the-lease-is-a-zero-turn-health-record.md) | The lease doubles as a zero-turn health record, and claims nothing about delivery | accepted | mash | 2026-10-28 |
| [0017](0017-every-message-is-signed-per-sap-13.md) | Every message is signed per SAP §13 with the endpoint's own NATS key | accepted | SAP (inherited) | 2026-10-21 |
| [0018](0018-mash-signed-marks-are-separate-headers.md) | mash's own signed marks (urgent, sender instance) are separate headers; kind and in-reply-to are unsigned | accepted (in-reply-to amended by [0063](0063-in-reply-to-is-signed-and-a-sender-can-declare-a-relay-with-a-signed-mash-via-both-are-shown-only-when-they-verify.md)) | mash (on SAP §13) | 2026-10-25 |
| [0019](0019-five-trust-labels-forged-never-shown-stale-after-five-minutes.md) | Five trust labels; forged mail is never shown; a signature over 5 minutes old is `stale` | accepted | SAP + mash | 2026-10-21 |
| [0020](0020-verified-mail-is-a-delegated-request.md) | Verified mail is a request delegated by the operator; a policy file adds the exceptions | accepted | mash | 2026-10-21 |
| [0021](0021-message-bodies-are-shown-quoted.md) | Message bodies are shown quoted, as the sender's data | accepted | mash | 2026-10-21 |
| [0022](0022-one-endpoint-manager-per-session.md) | One long-lived endpoint manager per session holds the connection; the session acts through its tools | accepted | mash | 2026-10-21 |
| [0023](0023-own-minimal-mcp-server-with-pinned-revisions.md) | mash implements its own minimal MCP server, no SDK, and negotiates only revisions it supports | accepted | mash | 2026-10-21 |
| [0024](0024-mail-is-delivered-by-hook-the-channel-only-wakes.md) | Mail is delivered by hook, not by channel; the channel only wakes an idle session | accepted (amended by [0041](0041-sessions-whose-hooks-never-run-stop-waking-deliver-through-read.md)) | mash (Claude Code host) | 2026-10-27 |
| [0025](0025-a-local-spool-hands-mail-to-the-hook.md) | A local spool hands mail from serve to the hook; ack only once shown; at-least-once | accepted (ownership clause superseded by [0040](0040-hook-ownership-session-id-launch-token-then-process-chain.md)) | mash | 2026-10-28 |
| [0026](0026-batching-settle-then-full-or-digest.md) | Batching: one batch per 100 ms of quiet; full up to 10 messages and 16 KiB, else a digest | accepted | mash | 2026-10-21 |
| [0027](0027-mail-kinds-decide-wake-or-wait.md) | Mail kinds decide wake or wait; the default kind is `task`; the kind is the sender's unsigned claim | accepted | mash | 2026-10-29 |
| [0028](0028-wake-timing-idle-wake-backoff-and-backlog.md) | Wake timing: a 45 s idle wake with backoff, at once before the first hook, and a backlog wake | accepted (amended by [0041](0041-sessions-whose-hooks-never-run-stop-waking-deliver-through-read.md)) | mash | 2026-10-28 |
| [0029](0029-urgent-is-a-signed-mark-honoured-only-from-verified-senders.md) | Urgent is a separate signed mark, honoured only from verified senders | accepted | mash | 2026-10-28 |
| [0030](0030-sessions-are-full-sap-agents.md) | Sessions are full SAP agents: heartbeat presence, status and live prompts; only the CLI starts a prompt | accepted | SAP inherited + mash | 2026-10-21 |
| [0031](0031-send-at-most-once-with-an-idempotency-key.md) | A send can be made at-most-once with a key, within the stream's duplicate window | accepted | NATS + mash | 2026-10-30 |
| [0032](0032-host-adapter-seam.md) | A host adapter seam isolates the agent host; Claude Code is the only host | accepted | mash | 2026-10-28 |
| [0033](0033-claude-code-plugin-is-the-distribution-unit.md) | The Claude Code plugin, in the repo's own marketplace, is the distribution unit | accepted | mash (Claude Code packaging) | 2026-10-21 |
| [0034](0034-text-output-and-plugin-commands-are-a-compatibility-contract.md) | Text output and the plugin's commands are a compatibility contract | accepted | mash | 2026-10-29 |
| [0035](0035-init-synadia-provisions-a-hosted-account.md) | `mash init synadia` provisions a hosted account from the core binary | accepted | mash | 2026-10-30 |
| [0036](0036-names-are-lower-case-hyphenated-words-up-to-48-characters.md) | Names and owners are lower-case words joined by single hyphens, 1-48 characters | accepted | mash | 2026-10-21 |
| [0037](0037-mail-carries-text-files-travel-by-reference.md) | Mail carries text only; files travel by reference to an object store | accepted | mash | 2026-10-21 |
| [0038](0038-synadia-setup-is-a-script-outside-the-binary.md) | Synadia setup is a script outside the binary; it supersedes 0035 | accepted | mash | 2026-10-31 |
| [0039](0039-mash-reads-claude-codes-managed-settings-read-only-for-diagnosis.md) | mash reads Claude Code's managed settings, read-only, for diagnosis only | accepted | mash (Claude Code host) | 2026-11-01 |
| [0040](0040-hook-ownership-session-id-launch-token-then-process-chain.md) | Hook ownership: session id, launch token, then process chain | accepted (amended by [0043](0043-second-launch-of-a-running-session-is-refused-and-told-once.md)) | mash | 2026-11-01 |
| [0041](0041-sessions-whose-hooks-never-run-stop-waking-deliver-through-read.md) | Sessions whose hooks never run stop waking and deliver through the read tool | accepted | mash | 2026-11-01 |
| [0042](0042-sign-sha256sums-with-sshsig.md) | A release's SHA256SUMS is signed with SSHSIG, namespace mash-release, after a maintainer-side rebuild | accepted | project | 2026-10-31 |
| [0043](0043-second-launch-of-a-running-session-is-refused-and-told-once.md) | A second launch of a running session is refused and told once in-session (amends 0040) | accepted | mash | 2026-11-01 |
| [0044](0044-config-and-state-directories-are-relocatable.md) | Config and state directories are relocatable by MASH_CONFIG_DIR and MASH_STATE_DIR | accepted | mash | 2026-11-01 |
| [0045](0045-product-code-carries-no-personal-convention-a-ratchet-lint-enforces-it.md) | Product code carries no personal convention; a ratchet lint enforces it | accepted | project | 2026-11-01 |
| [0046](0046-a-digest-lists-mail-it-does-not-ack-it.md) | A digest lists mail and does not ack it; the read tool or a full showing does (amends 0026 and 0024; their files are unchanged) | accepted | mash | 2026-11-01 |
| [0047](0047-guarded-deprovision-deletes-the-record-first-on-its-revision.md) | A guarded deprovision deletes the record first, against its revision, and fences the lease after (amends 0015 for `--user`) | accepted | mash | 2026-11-01 |
| [0048](0048-limit-changes-take-a-per-stream-kv-lock-with-expiry.md) | Limit changes take a per-stream KV lock with expiry | accepted | NATS / mash | 2026-11-01 |
| [0049](0049-who-filters-and-its-json-form-are-a-stable-shared-contract.md) | `who` filters and its JSON form are a stable, shared contract | accepted | mash | 2026-11-01 |
| [0050](0050-the-directory-record-says-what-an-endpoint-does-with-a-description-and-capabilities.md) | The directory record says what an endpoint does with a description and capabilities | accepted | mash | 2026-11-01 |
| [0051](0051-releases-carry-an-sbom-per-binary-and-slsa-provenance.md) | Releases carry an SBOM per binary and SLSA provenance from an isolated generator | proposed | project | 2026-11-01 |
| [0052](0052-mashctl-mints-nats-users-and-holds-the-account-signing-key.md) | `mashctl` mints NATS users under one scoped signing key per zone and holds the account signing key; it supersedes 0008 | accepted | NATS / mash | 2026-11-02 |
| [0053](0053-who-may-mail-whom-is-a-policy-file-compiled-by-mashctl-into-nats-permissions.md) | Who may mail whom is a policy file; one label, the zone, lives in the mail subject, and `mashctl` compiles the rules into each zone's NATS permissions | accepted | NATS / mash | 2026-11-02 |
| [0054](0054-mail-subjects-carry-a-zone-token-legacy-subjects-stay-valid.md) | Mail subjects carry a zone token and the legacy subjects stay valid (amends 0005 for the mail verb) | accepted | mash | 2026-11-02 |
| [0055](0055-until-the-maintainer-rules-only-mail-is-policed-prompt-and-status-stay-open.md) | Until the maintainer rules on prompt and status, only mail is policed; they stay open on their five-token subjects (amends 0053; its file is unchanged) | accepted | mash | 2026-11-02 |
| [0056](0056-every-zone-key-carries-both-tag-templates-and-every-user-the-deny-none-placeholder.md) | Every zone key carries both tag templates and every user the `deny:_none` placeholder (amends 0053) | accepted | NATS / mash | 2026-11-02 |
| [0057](0057-an-admin-can-suspend-an-endpoints-inbound-mail-by-parking-its-inbox-stream-subjects.md) | An admin can suspend an endpoint's inbound mail by parking its inbox stream's subjects, and read whether it is suspended | accepted | NATS / mash | 2026-11-02 |
| [0058](0058-an-operation-under-the-name-lock-stops-before-the-lock-expires-and-rechecks-it-before-writes.md) | An operation under the name lock stops before the lock expires and re-checks it before a write (amends 0048) | accepted | mash | 2026-11-02 |
| [0059](0059-mashctl-checks-who-issued-the-account-jwt-carries-only-its-own-signing-keys-and-requires-tls-for-admin-connections.md) | mashctl checks who issued the account JWT, carries only its own signing keys, and requires TLS for admin and system connections | accepted | NATS / mash | 2026-11-02 |
| [0061](0061-a-publish-violation-is-a-definite-refusal-only-when-one-send-is-waiting-on-the-subject.md) | A publish violation is a definite refusal only when one send is waiting on the subject | accepted | mash | 2026-11-02 |
| [0062](0062-log-is-rejected-until-implemented-check-runs-the-budget-case-variant-labels-warn-usage-errors-exit-2.md) | `log:` is rejected until implemented, `policy check` runs the budget, case-variant labels warn, usage errors exit 2 | accepted | mash | 2026-11-02 |
| [0060](0060-a-deny-rule-that-selects-nobody-is-an-error-unless-the-rule-says-allow-empty-add-and-reissue-gate-on-the-files-tests.md) | A deny rule that selects nobody is an error unless the rule says allow_empty; add and reissue gate on the file's tests | accepted | mash | 2026-11-02 |
| [0063](0063-in-reply-to-is-signed-and-a-sender-can-declare-a-relay-with-a-signed-mash-via-both-are-shown-only-when-they-verify.md) | In-reply-to is signed, and a sender can declare a relay with a signed `Mash-Via`; both are shown only when they verify (amends 0018) | accepted | mash (on SAP §13) | 2026-11-02 |
| [0064](0064-relay-by-asking-is-traced-not-blocked-content-free-trace-lines-and-counters.md) | Relay by asking is traced, not blocked: content-free trace lines and counters | accepted | mash | 2026-11-02 |
| [0065](0065-a-directory-poll-reloads-only-when-the-directory-stream-moved-and-is-jittered.md) | A directory poll reloads only when the directory stream moved, and is jittered | proposed | mash | 2026-11-02 |
