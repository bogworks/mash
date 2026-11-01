---
status: accepted
date: 2026-10-21
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# Subjects follow SAP's hierarchy, `agents.<verb>.cc.<owner>.<name>`

## Context and Problem Statement

Every NATS message travels on a subject, a dot-separated name that permissions and subscriptions match against. SAP
fixes a five-token shape for agent subjects, `agents.{verb}.{agent}.{owner}.{name}`, and reserves four verbs. mash
needed subjects for its sessions' live prompts, status, heartbeats and mail, and a subject filter for each inbox
stream.

## Decision Drivers

* Pure SAP: SAP tools must find mash sessions where SAP says agents live (spec §1.1 goal 5).
* Permissions and stream filters must be expressible as NATS subject wildcards.
* One deployment has one owner (ADR-0006).

## Considered Options (reconstructed 2026-10-31; not recorded at the time)

* SAP's five-token hierarchy, with agent token `cc` and the added verb `mail`
* A separate mash subject space (for example `mash.<owner>.<name>`) for mail, alongside SAP subjects

## Decision Outcome

Chosen option: "SAP's five-token hierarchy", because it follows from the decision that mash is pure SAP (ADR-0003)
(reconstructed).

For endpoint `<name>` under owner `<owner>`:

* prompt `agents.prompt.cc.<owner>.<name>`, status `agents.status.cc.<owner>.<name>`, heartbeat
  `agents.hb.cc.<owner>.<name>`, mail `agents.mail.cc.<owner>.<name>`.
* `cc` is SAP's agent token for Claude Code (SAP Appendix C); the service metadata sets `agent: claude-code`.
* The owner is SAP's fourth token, "the operator, e.g. `acme`. One per deployment, set in the config" (spec §2).
* The inbox stream's filter wildcards the agent token, `agents.mail.*.<owner>.<name>`. No reason was recorded; the
  wildcard lets an agent with another agent token mail the inbox (reconstructed).

### Consequences

* Good, because SAP discovery, heartbeats and status probes work on mash sessions unchanged.
* Good, because permissions are a short list of subject patterns (ADR-0007).
* Bad, because every mash endpoint is `cc`/`claude-code`, whatever agent actually runs behind it.
* Neutral, because the owner is a subject token, not a NATS account; ADR-0006 records how owners and accounts relate.

### Confirmation

* `internal/sap/subjects_test.go` `TestFor`: the four subjects, the inbox filter `agents.mail.*.<owner>.<name>` and the
  reply prefix.
* `internal/sap/subjects_test.go` `TestNameFromSubject`: only five-token `agents.*` subjects yield a name.
* `internal/admin/admin_test.go` `TestProvisionCreatesInboxConsumerRecord`: the provisioned stream filters
  `agents.mail.*.<owner>.<name>`.

## Pros and Cons of the Options

### SAP's five-token hierarchy

* Good, because it is what SAP tools expect; no translation layer is needed.
* Good, because the owner and name tokens make per-endpoint and per-owner permissions simple wildcards.
* Bad, because the agent token hard-codes the agent kind into every subject.

### A separate mash subject space for mail

* Good, because mail would be independent of SAP's naming.
* Bad, because SAP tools would not associate the mailbox with the agent.
* Bad, because permissions would have to cover two subject spaces.

## More Information

* Design spec §2 (terms) and §3 (subjects).
* Code: `internal/sap/subjects.go` (`AgentToken = "cc"`, `AgentName = "claude-code"`, `For`, `MailFilter`).
* Related: ADR-0003, ADR-0004, ADR-0006, ADR-0007.
