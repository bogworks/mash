# name-rule@1

The format of mash's name-acceptance rule file, version 1. mash owns this schema and its compatibility; whoever runs a
deployment owns the content of the file. mash ships no default rule: with no file named, no rule applies. The decision
is [ADR 0077](decisions/0077-the-name-rule-file-is-versioned-and-mash-reads-two-versions-for-one-window.md).

A rule restricts which addresses mash will accept. An address is the three tokens `owner`, `group` and `instance`
(`owner.group.instance`). mash applies the rules as given and gives no token a meaning of its own. A rule can
only refuse: it cannot grant what mash's own syntax forbids, and it is not a grant of any permission.

## Where the file comes from

- The path is the environment variable `MASH_NAME_RULE_FILE`, or the key `name_rule_file` in `config.json`. The
  environment wins over the file key.
- `mash` and `mashctl` read it through the same configuration that gives them their owner, so every invocation gets the
  same rule.
- The file is read once, at start. A change takes a restart. mash does not read a rule from the directory.
- No path named: no rule. A path that is set but empty is invalid, not absent.
- `MASH_NAME_RULE_REQUIRED` (or `name_rule_required` in `config.json`; the environment wins) is a boolean, `true` or
  `false`. When it is `true`, mash refuses to start unless a rule file was loaded, so a later restart without the file
  cannot reopen what the rule closed. Any other value is invalid.

## Shape

```json
{
  "version": 1,
  "rules": [
    {
      "id": "no-scratch",
      "match": { "group": { "is": "scratch" } },
      "message": "the group scratch is not for real work"
    }
  ]
}
```

- `version`: the integer `1`. Required.
- `rules`: an array. It may be empty. Required.
- A rule has exactly the keys `id`, `match` and `message`. `id` is a short non-empty name, unique in the file.
  `message` is the non-empty refusal text.
- `match` is an object over any of `owner`, `group` and `instance`. The keys `agent` and `name` of the earlier format
  are refused, and a file that uses them does not load. Each key present takes one object: `{"is": RE}` or
  `{"is_not": RE}`, where `RE` is a Go RE2 regular expression. `is` holds when the token matches; `is_not` holds when it
  does not.
- A rule matches an address when every key in its `match` holds. An address that matches any rule is refused. There
  are no accept rules and no ordering.
- mash wraps every pattern as a whole-token match, `^(?:RE)$`, so an alternation cannot match part of a token: `a|b`
  matches the token `a` or the token `b`, never `ab`.
- Unknown keys are errors at every level.

## Where it applies

The rule applies at every entry point, with no per-rule list: provision, start, send, reply, prompt and lookup. For `send` and `prompt` both the addressed address and the
sender's own address are checked; `reply` checks the address being answered; `lookup` of a refused address fails with
the named-rule error and never omits it silently. The requested address is checked before the directory is read, so a
refused address whose record is gone still gets the named-rule error, not "not found".

A refusal names the rule (`id`) and carries its `message`. The sender of a `send`, `reply` or `prompt` is told; nothing
is dropped silently.

## What refuses start

mash refuses to start, naming the file and the problem, when any of these holds:

- the path is set but empty, or the file is unreadable;
- the file is not valid JSON, or has trailing content after the JSON value;
- a key is unknown at the top level, in a rule, in `match` (this includes the old `agent` and `name`), or in a token
  object;
- `version` is missing, is not an integer, or is not a version mash reads (see Compatibility);
- `rules` is missing or is not an array;
- a rule has a missing or empty `id` or `message`, or two rules have the same `id`;
- `match` is missing, is empty, or has an empty object under a key;
- one key has both `is` and `is_not`, or a token object has neither;
- a pattern is not valid RE2;
- `name_rule_required` is `true` and no file was loaded, or its value is not a boolean.

## Compatibility

`version` is an integer. mash reads version n and n-1 of the rule file for one compatibility window. The window is the
release that introduces version n plus the next minor release; after it, version n-1 refuses start with an error that
names the version and the file. Any other version refuses start. Only version 1 exists today, so only `1` is accepted.

A change that cannot be read by a mash that reads only n-1 raises the version. Adding a key to a rule, a match key or a
token object is such a change, because unknown keys are errors.

## Examples

These show the format. They are content, not part of it; the deployment decides what its file holds.

Refuse a group that is reserved, and any instance name that starts with `tmp-`:

```json
{
  "version": 1,
  "rules": [
    { "id": "no-mash-group", "match": { "group": { "is": "mash" } }, "message": "the group mash is reserved" },
    { "id": "no-tmp", "match": { "instance": { "is": "tmp-.*" } }, "message": "tmp- instances are not allowed" }
  ]
}
```

Allow only one owner's addresses, by refusing every other owner (here `acme`):

```json
{
  "version": 1,
  "rules": [
    { "id": "owner-acme-only", "match": { "owner": { "is_not": "acme" } }, "message": "only the owner acme is allowed" }
  ]
}
```
