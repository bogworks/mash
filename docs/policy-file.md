# The policy file

The policy file says which endpoints may mail which others. Today a rule covers mail; prompt and status probes stay open (ADR 0054; they join the policy when the maintainer decides). It is compiled by `mashctl` into NATS publish permissions (one scoped signing key per zone, plus tags on each endpoint's user), so the server enforces it and a client cannot talk around it. `mashctl` is not shipped yet: this page describes the file and the `internal/policy` package that parses, checks, tests, explains and compiles it. The file does nothing until `mashctl` applies it, and mash today still issues every endpoint the wildcard mail line.

## Schema

YAML, one mapping with five keys, all optional. Unknown fields and repeated keys (at any depth) are errors, with line numbers.

```yaml
default: allow | deny      # what applies where no rule matches; allow when absent
subject_label: zone        # the label whose value is the zone, carried in the mail subject; zone when absent
endpoints:                 # every endpoint the file knows
  NAME:
    labels: {KEY: VALUE}   # at most 8
rules:
  - name: UNIQUE-NAME
    action: allow | deny
    from: {KEY: VALUE}     # required; {} is every endpoint
    to: {KEY: VALUE}       # required
    log: true              # optional; carried, no effect on the compiled permissions
tests:
  - {from: NAME, to: NAME, expect: allow | deny}
```

- An endpoint name is lower-case letters, digits and single hyphens, starting with a letter, 1 to 48 characters (the same rule as an owner or endpoint name everywhere else in mash).
- Label keys and values follow the directory record: a key is a lowercase letter then up to 31 of `a-z`, `0-9`, `_`, `.`, `-`; a value is non-empty printable text of up to 64 characters with no tab or newline. Quote values YAML would read as booleans if you mean the string (`internet: "yes"`); unquoted `yes` and `1` are read as the strings `yes` and `1`.
- `subject_label` names a label key. An endpoint's zone is the value of that label, or `default` when it has none. A zone is one subject token, so the value must be a valid name (lower-case letters, digits and single hyphens, starting with a letter, 1 to 48 characters); `check` rejects any other value of that label. Lower case matters twice: NATS lower-cases tags.
- A rule name is non-empty and unique; it is what `explain` and test failures quote.
- A test names declared endpoints on both sides.

## Selectors and rules

A selector is a map of `key: value` pairs, all of which must match an endpoint's labels. `{}` matches every endpoint. An endpoint without the label does not match. An endpoint without the subject label is in zone `default`, and selectors match it as such (`{zone: default}` selects it, as it does an endpoint labeled `zone: default`). A rule that leaves out `from` or `to` is an error, so forgetting one never widens a rule to everyone.

Rules are a union and their order means nothing. For a sender and a recipient: if any matching rule denies, the pair is denied; otherwise if any matching rule allows, it is allowed; otherwise `default` decides. Today a rule covers mail; prompt and status probes stay open (ADR 0054; they join the policy when the maintainer decides). `Compiled.Endpoints[].Recipients`, the explain output, the matrix and the JSON form all mean mail recipients. Self-mail follows the same rules as any other pair: an endpoint is its own recipient exactly when a rule or the default says so (`from: {}, to: {}` includes it).

## What check, test and compile say

- `check` reports errors (a bad action, a repeated rule name, a bad name, a zone value that is not one subject token, a test naming an undeclared endpoint) and warnings. Warnings: a rule whose `from` or `to` selects no endpoint (a misspelled label matches nothing, and the pair falls to the default; the message adds when the selector names a zone with no endpoints), an endpoint whose user would need more than 150 tags (see below), and, under `default: deny`, an endpoint no allow rule lets send or receive. Labels no rule uses are not warnings.
- `test` evaluates the `tests` section against the rules.
- `explain FROM TO` names the deciding rule and the selectors that selected it, or says no rule matched and the default applied. When several rules match, a deny decides over any allow, and the text lists the others.

## Zones, wildcards and tag exceptions

Writing every recipient out in every sender's permissions is quadratic in endpoints (measured: the account JWT passes its 1 MiB cap at 87 endpoints), so the compile uses the structure of the subject instead. The zone is in the mail subject only: mail to an endpoint goes to `agents.mail.<kind>.<owner>.<zone>.<name>` (ADR 0054). Prompt, status and the heartbeat keep SAP's five tokens (`agents.prompt.cc.<owner>.<name>`), and the zone key grants prompt and status as two open fixed lines, `agents.prompt.*.<owner>.*` and `agents.status.*.<owner>.*`, with the endpoint's own five-token prompt and status subscriptions as `{{name()}}` lines. `mash admin permissions` still prints the per-endpoint layout. The package builds its mail subjects with its own `MailSubjectPattern`.

The compile is one scoped signing key per zone:

- The key's permission template holds the fixed per-endpoint lines (own heartbeat, inbox, lease and so on) as `{{name()}}` templates, so each user gets its own, and one mail publish line for every recipient zone that every endpoint of the zone may address: `agents.mail.*.<owner>.<zone>.*`.
- Anything finer than a zone pair is an exception carried as tags on the user's JWT: `allow:<zone>.<name>` (or `allow:<zone>.*`) and `deny:<zone>.<name>` (or `deny:<zone>.*`), expanded by `agents.mail.*.<owner>.{{tag(allow)}}` in the key's publish allow list and `agents.mail.*.<owner>.{{tag(deny)}}` in its publish deny list. NATS lets a deny beat an allow.
- For each pair of zones (sender zone, recipient zone) the compile looks at every pair of endpoints: all allowed gives the wildcard and no tags; none allowed gives nothing; a mix gives the cheaper of two encodings, counted in tags over all senders of the sender zone: allow tags for the allowed pairs and no wildcard, or the wildcard and deny tags for the denied pairs. The wildcard belongs to the key, so it applies to every sender of the zone; a tie goes to the allow tags. A sender allowed nothing in the zone gets no tag (or one deny tag for the whole zone under the wildcard encoding).
- A sender allowed nobody still has a non-empty permission block: the fixed lines stay.

Limits of the encoding, each measured against nats-server 2.15:

- `{{tag(deny)}}` expands in the publish deny list, but nats-server refuses to authenticate a user whose scoped key has such a template and who has no `deny:` tag at all (the allow template just drops its lines). So every user of a zone whose key has deny templates carries at least the placeholder tag `deny:0`, which addresses nothing (no name starts with a digit). Adding the first deny exception to a zone therefore changes the key and re-issues all of that zone's users.
- Tags are lower-cased and their values become subjects as written: a tag is a permission. Whoever signs users with a zone's key is that zone's policy authority and can grant any exception.
- **A zone wildcard covers every name in its zone, declared or not.** `agents.mail.*.<owner>.<zoneB>.*` lets a sender publish to an endpoint of zone B that the file does not list. The compile's recipient lists and tests cover declared endpoints only. The invariant that closes this is that the file is the inventory: every endpoint is minted through the file (one authority, whoever holds the account key, as above), so an endpoint of a zone that the file does not declare can exist only if someone with the key mints a user under that zone key by hand. `mashctl drift` must report directory records and users that the file does not declare.
- A user's JWT travels in the CONNECT line, which the default server caps at 4096 bytes together with the signature and options. A 3,522-byte user JWT connects and a 4,056-byte one does not, so the budget limit is 4,000 bytes, about 150 tags. A rule on a label other than the zone, aimed at many endpoints, spends tags; keep such rules to a few endpoints or give the group its own zone.

`Compiled.Effective(name)` expands a zone key for one endpoint exactly as nats-server does, and a property test checks over random files that the subjects it lets an endpoint publish to are its recipients, no more and no fewer. A live test starts nats-server in operator mode with the compiled keys and publishes every sender and recipient of the enterprise example and two further files, checking each against the compile: mail on its six-token subject is allowed exactly as compiled, while a five-token prompt and status probe to any peer is always allowed.

## Sizes

`Budget` measures two things by building the real JWTs with throwaway keys: the account JWT with one scoped signing key per zone, against the 1 MiB the server accepts for an account (the jwt library's decoder cap; no server setting lifts it), and the largest user JWT (its name, its tags, signed by its zone's key), against 4,000 bytes.

The account JWT grows with zones and the zone pairs granted, not with endpoints. Measured:

| File | Account JWT | Largest user JWT |
| --- | --- | --- |
| 1,000 endpoints in 20 zones, each zone may mail the next three | 29,185 bytes | 606 bytes |
| 60 endpoints, one zone, everyone may mail everyone | 2,077 bytes | 609 bytes |
| 60 endpoints in 60 zones of their own, everyone may mail everyone | 227,772 bytes | 609 bytes |

The last row is the worst shape (the number of wildcards is quadratic in zones, so all-to-all passes 1 MiB at about 140 zones). A user's tags are the only thing that grows with a sparse set of exceptions, and adding an endpoint never touches the account JWT.

## Examples

A solo developer's file: three endpoints, open by default, one restriction.

```yaml
# A solo developer's policy: three endpoints, everything open, one restriction.
default: allow

endpoints:
  coder:
    labels: {role: agent}
  reviewer:
    labels: {role: agent}
  deploy-bot:
    labels: {risk: high, kind: tool}

rules:
  # Agents may not reach the tools that can ship to production.
  - name: agents-do-not-reach-high-risk-tools
    action: deny
    from: {role: agent}
    to: {risk: high}
    log: true

tests:
  - {from: coder, to: reviewer, expect: allow}
  - {from: coder, to: deploy-bot, expect: deny}
  - {from: deploy-bot, to: coder, expect: allow}
```

An IT department's file: deny by default, zones by label, tests.

```yaml
# An IT department's policy: deny by default, zones by label, a tests section that gates every apply.
default: deny

endpoints:
  web-intake:
    labels: {zone: dmz, internet: "yes", team: web}
  support-bot:
    labels: {zone: internal, team: support}
  billing-agent:
    labels: {zone: internal, team: finance}
  ledger-agent:
    labels: {zone: internal, team: finance}
  hr-agent:
    labels: {zone: internal, team: hr}
  build-bot:
    labels: {zone: internal, team: platform}
  deploy-tool:
    labels: {zone: internal, team: platform, kind: tool}

rules:
  - name: internal-agents-talk
    action: allow
    from: {zone: internal}
    to: {zone: internal}
  - name: dmz-reaches-support
    action: allow
    from: {zone: dmz}
    to: {team: support}
  - name: support-answers-dmz
    action: allow
    from: {team: support}
    to: {zone: dmz}
  - name: nothing-internet-facing-reaches-finance
    action: deny
    from: {internet: "yes"}
    to: {team: finance}
    log: true
  - name: hr-is-private
    action: deny
    from: {}
    to: {team: hr}
  # This allow never takes effect: hr-is-private denies the same pair, and a deny beats any allow.
  - name: hr-agent-may-mail-itself
    action: allow
    from: {team: hr}
    to: {team: hr}

tests:
  - {from: web-intake, to: support-bot, expect: allow}
  - {from: web-intake, to: billing-agent, expect: deny}
  - {from: support-bot, to: web-intake, expect: allow}
  - {from: billing-agent, to: ledger-agent, expect: allow}
  - {from: build-bot, to: hr-agent, expect: deny}
  - {from: hr-agent, to: build-bot, expect: allow}
```

## Using it

`mashctl policy` works on the file alone: it needs no server and no admin credentials. Every verb finds the file the same way: `--policy FILE`, else the `MASH_POLICY` environment variable, else `policy.yaml` in the config directory (`MASH_CONFIG_DIR`, else `$XDG_CONFIG_HOME/mash`, else `~/.config/mash`).

| Command | What it does | Exit code |
| --- | --- | --- |
| `mashctl policy check` | Parses and validates the file. Prints `check: OK - N endpoints, M rules` (after any `warning:` lines), or one `error: <where>: <message>` line per problem; parse errors carry their line number. | 1 on any error, 0 otherwise (warnings do not fail) |
| `mashctl policy test` | Runs the `tests` section: `PASS from->to expect allow`, or `FAIL from->to expect allow got deny: <why>`. A file with no tests prints `test: no tests in the file`. | 1 if any test fails |
| `mashctl policy explain FROM TO` | Says whether FROM may mail TO and which rule (or the default) decided, then the line `note: rules cover mail only; prompt and status probes are not covered by rules yet (open)`. Both must be declared endpoints. | 0 whether allowed or denied; 2 for an unknown endpoint or bad usage |
| `mashctl policy show [--json] [--budget]` | Prints the who-may-mail-whom matrix (or the JSON form), then a summary of zones, wildcard zones and tags per endpoint, then the same `note:` line (rules cover mail only; prompt and status probes are open). With `--json` there is no note; the document carries top-level `"covers": ["mail"]` and `"open": ["prompt", "status"]` instead. `--budget` also prints the account and worst user JWT sizes against their limits (to stderr with `--json`). Needs the owner (`MASH_OWNER` or `config.json`). | 1 on an invalid file, a missing owner, or a budget over its limit |

`mashctl policy` with no verb, or an unknown one, prints the usage to stderr and exits 2.

An edit refuses, naming the line, to change a flow-style (`{...}`) `endpoints:` map or a flow-style entry or `labels:` inside one, so write those in block style (a flow-style entry or `labels:` on its own line under a block-style `endpoints:` is edited in place).

A command that edits the file saves it atomically and refuses, leaving the file as it is, if the file changed on disk since the command loaded it.

## What the file is not

It is not a place for content rules, rate limits or anything about what a message says: it decides reachability between endpoints and nothing else. A rule that lets a sender mail a recipient lets it ask for anything. It does not stop one endpoint asking another to forward a message (relay by asking), and it does not replace the receiving agent's judgement.
