---
status: proposed
date: 2026-11-15
decision-makers: Randy Bias (mash maintainer)
---

# `mash prompt` always sends its arguments as text, and a raw envelope needs `--envelope`

## Context and Problem Statement

`mash prompt <to> <text...>` sent the joined arguments as the payload of the live prompt. A receiver applies the SAP
§5.3 rule to a payload: if its first non-space byte is `{` it is a JSON envelope that must carry a non-empty string
`prompt`; anything else is the prompt text. So `mash prompt peer '{"ask":"state"}'`, an argument that is meant as
text and happens to be a JSON object, was refused with 400 ("the envelope has no prompt"). The workaround was to write
the envelope by hand: `{"prompt":"{\"ask\":\"state\"}"}`. Nothing in the CLI help or the docs said that an argument
beginning with `{` was read as an envelope. Reported as homestead-names #1002. What should the
CLI do with an argument that parses as a JSON object?

## Decision Drivers

* A plain argument is prompt text. The same words must not mean two things depending on their first character.
* The wire stays SAP §5.3: the receiver is not changed, and a non-mash SAP agent is served the same way.
* A caller who wants to send a raw envelope (for example with fields mash does not set) can still do it.
* Plain text that does not start with `{` goes on the wire exactly as before.

## Considered Options

* The CLI wraps text that starts with `{` in an envelope, and a new `--envelope` flag sends a raw one (chosen).
* Keep today's rule and document it.
* Make the receiver accept an envelope without `prompt` as text.
* Always send every prompt as an envelope.

## Decision Outcome

Chosen option: "wrap text that starts with `{`, and add `--envelope`".

* `mash prompt <to> <text...>` joins its arguments with spaces and treats the result as text. When the text, after
  leading white space, starts with `{`, the CLI sends `{"prompt": <text>}`, so the receiver gets the text unchanged.
  Any other text is sent as it was.
* `mash prompt <to> --envelope <json>` sends the one JSON argument as the payload, as is. `--envelope` is read only as
  the first argument after `<to>`.
* The 32 KiB limit on a prompt (`endpoint.MaxPromptBytes`) is checked on the text before it is wrapped, and on the
  payload for `--envelope`.
* The receiver's rule (`promptText`) is unchanged.

### Consequences

* Good, because `mash prompt peer '{"ask":"state"}'` delivers that text.
* Good, because no receiver changes and no wire format is added.
* Bad, because a script that relied on a JSON object argument being read as a raw envelope must add `--envelope`.
  Nothing in this repository does: the tests and the docs use plain text.
* Neutral, because mash refuses attachments (`attachments_ok=false`), so `--envelope` is for a hand-built `prompt`
  field only.

### Confirmation

Tests named in the pull request: a unit test of the payload rule, a CLI test that `{"ask":"state"}` reaches the
receiver as that text, and one that `--envelope '{"prompt":"hello"}'` reaches it as `hello`.

## Pros and Cons of the Options

### Wrap text that starts with `{`, add `--envelope`

* Good, because plain text is unchanged on the wire and the raw form is explicit.
* Bad, because the CLI changes for a script that sent an envelope without the flag.

### Keep today's rule and document it

* Good, because nothing changes.
* Bad, because a JSON-looking message stays an error that points at the wrong cause.

### Make the receiver accept an envelope without `prompt` as text

* Good, because the CLI is unchanged.
* Bad, because it departs from SAP §5.3 and hides a malformed envelope from a caller that meant one.

### Always send every prompt as an envelope

* Good, because the rule is uniform.
* Bad, because it changes every prompt on the wire for a case that affects one.

## More Information

* Code: `cmd/mash/cli.go` (`prompt`), `internal/endpoint/service.go` (`Prompt`, `promptText`).
* Related: [ADR 0003](0003-sap-plus-mail-verb-and-jetstream-durability.md).
