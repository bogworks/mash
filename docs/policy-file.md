# The policy file

The policy file says which endpoints may mail, prompt and probe which others. It is compiled by `mashctl` into each endpoint's NATS publish permissions, so the server enforces it and a client cannot talk around it. `mashctl` is not shipped yet: this page describes the file and the `internal/policy` package that parses, checks, tests, explains and compiles it. The file does nothing until `mashctl` applies it, and mash today still issues every endpoint the wildcard mail line.

## Schema

YAML, one mapping with four keys, all optional. Unknown fields and repeated keys (at any depth) are errors, with line numbers.

```yaml
default: allow | deny      # what applies where no rule matches; allow when absent
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
- A rule name is non-empty and unique; it is what `explain` and test failures quote.
- A test names declared endpoints on both sides.

## Selectors and rules

A selector is a map of `key: value` pairs, all of which must match an endpoint's labels. `{}` matches every endpoint. An endpoint without the label does not match. A rule that leaves out `from` or `to` is an error, so forgetting one never widens a rule to everyone.

Rules are a union and their order means nothing. For a sender and a recipient: if any matching rule denies, the pair is denied; otherwise if any matching rule allows, it is allowed; otherwise `default` decides. One rule covers every verb an endpoint addresses to a peer: mail, live prompt and status probe. Self-mail follows the same rules as any other pair: an endpoint is its own recipient exactly when a rule or the default says so (`from: {}, to: {}` includes it).

## What check, test and compile say

- `check` reports errors (a bad action, a repeated rule name, a bad name, a test naming an undeclared endpoint) and warnings. Warnings: a rule whose `from` or `to` selects no endpoint (a misspelled label matches nothing, and the pair falls to the default), and, under `default: deny`, an endpoint no allow rule lets send or receive. Labels no rule uses are not warnings.
- `test` evaluates the `tests` section against the rules.
- `explain FROM TO` names the deciding rule and the selectors that selected it, or says no rule matched and the default applied. When several rules match, a deny decides over any allow, and the text lists the others.
- Compile gives each sender one publish line per allowed recipient and verb, `agents.mail.*.<owner>.<recipient>` and the prompt and status forms, in place of the three wildcard lines, plus the fixed lines every endpoint has. A sender allowed no recipients keeps the fixed lines and has no mail, prompt or status line. A rule that cannot be expressed is an error, never a silent widening.
- The compile is measured as the account JWT it needs (one scoped signing key per endpoint) against the 1 MiB the server accepts for an account push. A 60-endpoint file where everyone may mail everyone is about 520 KB (521,452 bytes); the budget grows with endpoints times recipients.

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

## What the file is not

It is not a place for content rules, rate limits or anything about what a message says: it decides reachability between endpoints and nothing else. A rule that lets a sender mail a recipient lets it ask for anything. It does not stop one endpoint asking another to forward a message (relay by asking), and it does not replace the receiving agent's judgement.
