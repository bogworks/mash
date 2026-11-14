# The policy file

The policy file says which endpoints may mail which others, as labels and rules. Today a rule covers mail; prompt and status probes stay open ([ADR 0055](decisions/0055-until-the-maintainer-rules-only-mail-is-policed-prompt-and-status-stay-open.md)). There are no zones: the address is the fence and no server enforces a rule of this file ([ADR 0091](decisions/0091-there-are-no-zones-mail-keeps-the-five-token-subject-and-the-address-is-the-fence.md)). `mashctl` parses, checks and tests the file, and `mashctl add` declares endpoints in it; each endpoint's user is minted with its own permission set, whatever the rules say. Until the label policy model is ruled the file is a statement of intent that the tools check. A file that still carries a zone (`subject_label`, a `zone` or `zones` field, or a label or selector key `zone`) is refused, naming the line of each and what to remove.

## Schema

YAML, one mapping with four keys, all optional. Unknown fields and repeated keys (at any depth) are errors, with line numbers.

```yaml
default: allow | deny      # what applies where no rule matches; allow when absent
endpoints:                 # every endpoint the file knows
  NAME:
    labels: {KEY: VALUE}   # at most 64
rules:
  - name: UNIQUE-NAME
    action: allow | deny
    from: {KEY: VALUE}     # required; {} is every endpoint
    to: {KEY: VALUE}       # required
    # log: true            # not implemented: Check refuses it (ADR 0062)
    allow_empty: true      # optional; lets a selector of this rule match no endpoint (see check below); no effect on permissions
tests:
  - {from: NAME, to: NAME, expect: allow | deny}
```

- An endpoint name is lower-case letters, digits and single hyphens, starting with a letter, 1 to 48 characters (the same rule as an owner or endpoint name everywhere else in mash).
- Map keys must be plain strings: quote a name like `"null"`; an unquoted `null`, `~`, number or boolean as a key is refused with its line number.
- Label keys and values follow the directory record: a key is a lowercase letter then up to 31 of `a-z`, `0-9`, `_`, `.`, `-`; a value is non-empty printable text of up to 64 characters with no tab or newline. Quote values YAML would read as booleans if you mean the string (`internet: "yes"`); unquoted `yes` and `1` are read as the strings `yes` and `1`.
- A rule name is non-empty and unique; it is what test failures quote.
- A test names declared endpoints on both sides.

## Selectors and rules

A selector is a map of `key: value` pairs, all of which must match an endpoint's labels. `{}` matches every endpoint. An endpoint without the label does not match. A rule that leaves out `from` or `to` is an error, so forgetting one never widens a rule to everyone.

Rules are a union and their order means nothing. For a sender and a recipient: if any matching rule denies, the pair is denied; otherwise if any matching rule allows, it is allowed; otherwise `default` decides. Today a rule covers mail; prompt and status probes stay open (ADR 0055; they join the policy when the maintainer decides). `Compiled.Endpoints[].Recipients` means mail recipients. Self-mail follows the same rules as any other pair: an endpoint is its own recipient exactly when a rule or the default says so (`from: {}, to: {}` includes it).

## What check, test and compile say

- `check` reports errors (a null list item, a bad action, a repeated rule name, a bad name, a test naming an undeclared endpoint, `log: true` on a rule: it is not implemented yet, and a deny rule whose `from` or `to` selects no endpoint) and warnings. A deny that selects nobody denies nothing, and under `default: allow` a misspelled label fails open, so it is an error that names the rule, the selector and the line; set `allow_empty: true` on the rule if the group is intentionally empty (a group with no endpoints yet), and it is a warning again (ADR 0060). Warnings: label values for one key that differ only in case across endpoints (`Finance` and `finance` are different values to a selector); an allow rule, or a deny rule with `allow_empty`, whose `from` or `to` selects no endpoint (a misspelled label matches nothing, and the pair falls to the default), and, under `default: deny`, an endpoint no allow rule lets send or receive. Labels no rule uses are not warnings.
- `test` evaluates the `tests` section against the rules.
- `mashctl policy apply`, `mashctl add` and `mashctl reissue` all parse the file (so a file that does not check is refused) and run its `tests` before anything is created, minted or pushed. If any test fails they print the `FAIL` lines and `refusing: N of M policy tests in FILE fail (...)`, and exit 1; `add` creates no key, creds file or record and `reissue` rewrites no creds file (ADR 0060).

## What the compile gives

`Compile` resolves the file against its declared endpoints into, per endpoint, the recipients it may mail: the declared endpoints that some allow rule grants and no deny rule forbids, or what the default says where no rule matches. That is what `tests` assert. It produces no signing key, no permission template and no tag. Mail keeps SAP's five-token subject, `agents.mail.<agent>.<owner>.<name>`, and a user's publish permission is its own agent and owner tokens ([ADR 0091](decisions/0091-there-are-no-zones-mail-keeps-the-five-token-subject-and-the-address-is-the-fence.md)); reaching another owner is an explicit extra grant. A within-owner deny has no mechanism until the label policy model is ruled, which is the planner's to write.

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

tests:
  - {from: coder, to: reviewer, expect: allow}
  - {from: coder, to: deploy-bot, expect: deny}
  - {from: deploy-bot, to: coder, expect: allow}
```

An IT department's file: deny by default, networks by label, tests.

```yaml
# An IT department's policy: deny by default, networks by label, a tests section that gates every add and reissue.
default: deny

endpoints:
  web-intake:
    labels: {net: dmz, internet: "yes", team: web}
  support-bot:
    labels: {net: internal, team: support}
  billing-agent:
    labels: {net: internal, team: finance}
  ledger-agent:
    labels: {net: internal, team: finance}
  hr-agent:
    labels: {net: internal, team: hr}
  build-bot:
    labels: {net: internal, team: platform}
  deploy-tool:
    labels: {net: internal, team: platform, kind: tool}

rules:
  - name: internal-agents-talk
    action: allow
    from: {net: internal}
    to: {net: internal}
  - name: dmz-reaches-support
    action: allow
    from: {net: dmz}
    to: {team: support}
  - name: support-answers-dmz
    action: allow
    from: {team: support}
    to: {net: dmz}
  - name: nothing-internet-facing-reaches-finance
    action: deny
    from: {internet: "yes"}
    to: {team: finance}
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
| `mashctl policy check` | Parses, validates and compiles the file. Prints `check: OK - N endpoints, M rules` (after any `warning:` lines), or one `error: <where>: <message>` line per problem; parse errors carry their line number. | 1 on any error, 0 otherwise (warnings do not fail) |
| `mashctl policy test` | Runs the `tests` section: `PASS from->to expect allow`, or `FAIL from->to expect allow got deny: <why>`. A file with no tests prints `test: no tests in the file`. | 1 if any test fails |

`mashctl policy` with no verb, or an unknown one, prints the usage to stderr and exits 2. `policy show`, `policy explain` and `policy diff` were removed with the zone model and are unknown verbs. `mashctl policy apply` refuses (exit 1, nothing changed) until the label policy model ships: the zone policy stack it served is gone ([ADR 0091](decisions/0091-there-are-no-zones-mail-keeps-the-five-token-subject-and-the-address-is-the-fence.md)).

An edit refuses, naming the line, to change a flow-style (`{...}`) `endpoints:` map or a flow-style entry or `labels:` inside one, so write those in block style (a flow-style entry or `labels:` on its own line under a block-style `endpoints:` is edited in place).

A command that edits the file saves it atomically and refuses, leaving the file as it is, if the file changed on disk since the command loaded it.

The enforcement behind a policy (compiled templates, tags and the live server) is tested on every release and on every change of the NATS server version: see [Policy tests on every release](release.md#policy-tests-on-every-release).

## What the file is not

It is not a place for content rules, rate limits or anything about what a message says: it decides reachability between endpoints and nothing else. A rule that lets a sender mail a recipient lets it ask for anything. It does not stop one endpoint asking another to forward a message (relay by asking), and it does not replace the receiving agent's judgement.
