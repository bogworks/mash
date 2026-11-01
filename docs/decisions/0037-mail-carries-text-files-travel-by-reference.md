---
status: accepted
date: 2026-10-21
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# Mail carries text only; files travel by reference to an object store

## Context and Problem Statement

Agents often need to hand each other files: a patch, a log, a report. Mail is stored in a JetStream stream, NATS's
persistence layer, with a per-message limit and a per-inbox byte limit (ADR-0010). SAP's prompt envelope can carry
attachments, and NATS offers an object store, but every byte in mail is also text a model session may read. mash had
to decide whether mail carries files.

## Decision Drivers

* Keep each inbox small and bounded, so it holds its 30 days of history.
* A message is shown to a model session; large binary content does not belong in its context.
* Design goal 6: "Minimal new code" (spec §1.1).

## Considered Options (reconstructed 2026-10-31; not recorded at the time)

* Attachments inside mail, up to the message size limit
* Files stored by mash in a NATS object store, referenced from mail
* Text only; a file goes by reference to an object store the sender chooses

## Decision Outcome

Chosen option: "text only; files by reference", recorded as a non-goal for v0.1: "File transfer. Mail carries text;
large files go by reference to an object store." (spec §1.2). The reasoning is not recorded beyond that line; it is
consistent with the inbox limits, 1 MiB per message and 16 MiB per inbox (reconstructed).

As built (mash layer, declared through SAP metadata):

* A mail message has a body and mash's headers, and no attachment field. A send larger than 1 MiB with its headers, or
  than the server's `max_payload` when that is smaller, is refused; in the second case the error advises putting the
  content in a file and sending the path (`internal/endpoint/mail.go`).
* The SAP `prompt` endpoint declares `attachments_ok: false`, and a prompt carrying attachments is refused with `400`
  (`internal/endpoint/service.go`).
* What "by reference" means is left to the sender: a path, a URL, an object-store key. mash does not fetch or check
  it.

### Consequences

* Good, because inboxes stay small and every message is readable text.
* Good, because mash carries no file storage, transfer or cleanup code.
* Bad, because the sender and recipient must share some store the reference points to; mash gives no help with
  access or lifetime.
* Neutral, because a reference is not covered by the message signature beyond its text: the signed body proves who
  sent the pointer, not what the pointed-to file contains.

### Confirmation

* `internal/endpoint/maxpayload_test.go`: `TestSendKeepsTheOneMiBCapOnTheDefaultServer`,
  `TestSendFollowsTheServersMaxPayload`; `cmd/mash/maxpayload_test.go`: `TestCLISendOverTheServersMaxPayloadSaysSo`.
* `internal/endpoint/service_test.go`: `TestRegistrationMetadata` (`attachments_ok` is `false`),
  `TestPromptBadEnvelope400` (a prompt with attachments is refused).

## Pros and Cons of the Options

### Attachments inside mail

* Good, because one send carries everything.
* Bad, because files would eat the inbox's byte limit and push older mail out, and land in a model's context.

### mash-managed object store

* Good, because references would always resolve.
* Bad, because mash would own storage, permissions and expiry for files: much more code.

### Text only, files by reference

* Good, because it keeps mail small and mash simple.
* Bad, because file handling is left to each deployment.

## More Information

* Design spec §1.2 (non-goals for v0.1) and §7.4 (prompt metadata).
* A consumer adopted the same rule for its own messages.
* Related: ADR-0003 (scope of mash), ADR-0010 (inbox and message limits), ADR-0030 (the SAP prompt endpoint).
