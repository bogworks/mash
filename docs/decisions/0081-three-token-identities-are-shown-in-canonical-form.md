---
status: accepted
date: 2026-11-10
decision-makers: Randy Bias (mash maintainer), by the maintainer's ruling relayed 2026-11-10
---

# Three-token identities are shown in canonical form; scripts keep the flat key

## Context and Problem Statement

A three-token endpoint (agent, owner and name tokens, [ADR 0074](0074-an-endpoint-is-three-sap-tokens-and-keys-its-storage-on-their-flat-form.md)) is stored and signed under its flat key, `agent_owner_name`. Every surface of mash that names an endpoint prints that key, or only the name token, so a person reads `bogworks_x_sap-go-sdk` in one place and `sap-go-sdk` in another. The naming contract (REQ-011) orders the tokens `agent.owner.name`, and `sap.EndpointIdentity.String()` already prints that form in `mash status`. Which form do people read, which form do scripts keep, and which forms does input accept?

## Decision Drivers

* People should read one name for an endpoint, on every surface, in the order the naming contract gives.
* The flat key is a storage and wire form; scripts and stored data depend on it and must not move.
* A name a person reads must be one they can type back: if `who` shows a form, `send` must accept it.
* A legacy endpoint must be shown byte for byte as before ([ADR 0049](0049-who-filters-and-its-json-form-are-a-stable-shared-contract.md), the render contract).

## Considered Options

* Human views show the canonical form, script contracts stay flat, input accepts both (chosen).
* Show the canonical form everywhere, including `who --names` and `--json`.
* Keep showing the flat key and teach people to read it.

## Decision Outcome

Every human-facing surface of `mash` and `mashctl` shows a three-token endpoint as `agent.owner.name`: `who` text, `inbox`, `read`, the message header and trust line, the wake, digest and sender summaries, `relayed from`, `send` receipts, `status`, hook frames, notices and instructions, error messages that name an endpoint, and `mashctl` output such as drift. The form is `sap.EndpointIdentity.String()`; `sap.Display(key)` gives it for a directory key, and `config.Config.DisplayName()` and `Endpoint.Display()` for an endpoint's own name. A legacy name, and anything that is not a valid three-token flat key, is shown unchanged, so a legacy endpoint prints exactly what it did.

The flat key stays the script contract and the stored form: `who --names`, the first column of `mashctl list`, the `to` and `from` fields of channel and hook event meta, the spool, relay tracking, streams, subjects and every SAP wire field.

`who --json` gains an optional `identity` field holding the canonical form, beside the unchanged `name`. It is present only for a three-token endpoint and omitted otherwise, as `zone` ([ADR 0054](0054-mail-subjects-carry-a-zone-token-legacy-subjects-stay-valid.md)) and `alias` ([ADR 0076](0076-a-three-token-record-may-carry-the-legacy-identity-it-answers-to-as-an-alias.md)) are, which ADR 0049 allows: a field is added, never renamed or removed.

`mashctl drift --json` is a machine output too: a finding's `subject` stays the flat directory key, and a finding gains the same optional `identity` field (the canonical form, present only for a three-token subject). Only the text output of drift prints the canonical form; the free-text `detail` names the endpoint canonically, as prose.

`who --name` and `who --match` accept what a person copies from `who`: when the argument is a three-token identity, written `agent.owner.name` or as the flat key, it matches that endpoint's full identity (exactly, case-insensitively); any other argument filters on the name token as before, so a legacy filter behaves as it always did.

Input accepts the canonical form as well as the flat key and legacy names, wherever a recipient or directory key is accepted: `send`, `prompt` and the directory lookup. `sap.ParseCanonical` parses exactly three dot-separated tokens through `sap.New`, so the token rules and the refusal of the legacy agent token still apply, and `sap.KeyOf` turns it into the flat key before anything else uses it. A legacy name never contains a dot (letters, digits and hyphens only), so the forms cannot be confused. An argument that is none of the three is refused with an error that names the canonical form and says the flat form is accepted too.

A cross-owner sender is shown as `agent.owner.name` with no `owner/` prefix, since the owner is already a token.

### Consequences

* Good, because a person reads and types one name for an endpoint.
* Good, because scripts, stored mail and the wire do not change, and legacy output is byte-identical.
* Good, because the `who --json` change is additive.
* Bad, because the first column of `mash who` changes for a three-token endpoint, and a script that parsed it must read `--names` or `--json` instead.
* Bad, because two spellings now name one endpoint on input.
* Bad, because `who --name` and `--match` still match the name token only, not the dotted form; that is left for a follow-on.

### Confirmation

Table tests for `sap.Display`, `sap.ParseCanonical` and `sap.KeyOf`, and for `Config.DisplayName`; render tests for the header, line, digest, summary, sender and `relayed from` of a three-token sender; `cmd/mash` tests for `who` text, `--names` and `--json`, `status`, and a live CLI test that sends by the canonical and the flat form and reads the canonical header; `cmd/mashctl` tests for drift. `contract.txt` and the legacy golden tests are unchanged.

## Pros and Cons of the Options

### Human views canonical, script contracts flat, input accepts both (chosen)

* Good, because people and scripts each get the form that suits them.
* Bad, because the flat form stays visible in script output.

### Canonical everywhere

* Good, because there is one spelling to document.
* Bad, because `who --names` and `--json` `name` are a stable contract that scripts feed to `send`, and a dotted key would break the stored forms that share them.

### Keep the flat key

* Good, because nothing changes.
* Bad, because people read and mistype an underscore form that the naming contract does not use.

## More Information

* Code: `internal/sap/display.go`, `internal/config/display.go`, `internal/render/render.go`, `cmd/mash/whofilter.go`.
* Related: [ADR 0049](0049-who-filters-and-its-json-form-are-a-stable-shared-contract.md), [ADR 0074](0074-an-endpoint-is-three-sap-tokens-and-keys-its-storage-on-their-flat-form.md), [ADR 0076](0076-a-three-token-record-may-carry-the-legacy-identity-it-answers-to-as-an-alias.md).
