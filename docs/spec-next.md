# Planned changes in the specification (spec-next)

The specification in `spec/` describes the released behavior, and the
conformance tests and `scripts/refgen` hold the code to it. A new `x-output`
case on an existing command would break conformance before the code lands, so
planned changes for the next release are written in separate places that the
tests ignore.

## Per command: `x-output-next`

A command in `spec/mash.json` or `spec/mashctl.json` may carry one more
metadata entry:

```json
{"name": "x-output-next", "value": [
  {"case": "...", "text": "...", "stream": "stderr", "exit": 1,
   "issue": "#1055", "replaces": "existing x-output case name",
   "note": "..."}
]}
```

`replaces` (optional) names a current `x-output` case of the same command;
`note` is optional.

## Everything else: `spec/next.json`

```json
{"info": {"version": "0.5.9-spec.0"},
 "items": [{"id": "...", "where": "install.sh|policy document|rules",
            "case": "...", "text": "...", "stream": "...", "exit": 0,
            "issue": "#...", "replaces": "...", "note": "..."}],
 "rules_next": [{"rule": "...", "detail": "...",
                 "replaces": "existing rule title"}]}
```

For changes not tied to one command: installer messages, the policy document
field, rules. A `rules_next` entry's `replaces` is a rule title in
`spec/rules.json`.

## Rendering the Gate 1 demo

```
go run ./scripts/gate1demo [-spec spec] [-out demo.md]
```

Prints Markdown: for each case, the command (or item id) and issue, the
"Today" text when `replaces` matches, the new text (headed with the release number, here 0.5.9), stream and exit. The
output is deterministic. A new-text line over 69 columns is flagged with a
width line; over 80 columns is an error (case and item texts; an amended rule's prose is not width-checked). The renderer refuses, writing
nothing, if any text looks like a secret (an nkey seed, a PEM header, a JWT,
`MASH_SETUP_TOKEN=`).

When the code lands, move the case into `x-output` and bump `x-release` in the
same change, and delete it from the next-places.
Cases with no `x-output` home (installer, setup script) stay in
`spec/next.json` until the spec is closed for their release, so the Gate 1
demo keeps showing them.
