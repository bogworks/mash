# Who may mail whom: reach today, and the security policy

> **Status.** The security policy described in the second half of this page is
> planned and is **not in any release**. No command for it exists, and nothing
> in a release can switch it on. The first half describes what mash does today.

## 1. Today: the owner is the wall

Every endpoint has an address of three parts, `owner.group.instance`, such as
`acme.dev.alice`. When `mashctl` creates an endpoint it gives that endpoint's
credentials one fixed set of permissions. The NATS server enforces them; mash is
not in the path of a message.

An endpoint may:

- mail, prompt and ask for the status of any endpoint with **the same owner**,
  whatever the group;
- send heartbeats as itself and hear the heartbeats of its own owner;
- use its own inbox, its own lease and the directory.

An endpoint may not mail, prompt or ask the status of an endpoint with another
owner. The owner is a literal in every permission line, never a wildcard, so no
line reaches another owner's endpoints.

For a solo developer, that means everything under one owner is open to
everything else under it, and groups do not restrict anything. Nothing needs
setting up.

If a send is refused, the sender is told that the server refused it by policy,
and nothing was stored. If the recipient has your owner, the likely cause is
credentials older than the permissions mash issues now: ask the person who runs
your server to run `mashctl doctor`.

### What this does not cover

- **Who may talk at all.** The fixed permissions let every endpoint use reply
  and service subjects that they do not narrow. A program that speaks to NATS
  directly, not through mash, can use them to reach another endpoint's replies.
  See [the threat model](security/threat-model.md).
- Message content, message rate, and what an agent does with mail it may
  receive.
- Other NATS accounts. mash is a tenant of its account; it does not own it and
  does not make claims about other users in it.

## 2. Planned: a policy file that narrows reach inside an owner

The plan, for a later release with no date, is that an administrator can narrow
reach inside one owner. The default stays as it is today, wide open.

### The shape

- One small JSON file per owner, kept by the administrator (a git repository is
  a good home for it).
- For a group and a verb (mail, prompt, status, heartbeat), a switch: `shared`
  (every endpoint of the group reaches every endpoint of the group) or `closed`
  (only what the file adds).
- For one sending endpoint, a list of exact addresses it may reach and a list of
  addresses taken away from it. An empty list means nobody, never everybody.
- Each file carries a generation number and names the one it replaces, so an old
  file cannot be applied by mistake.

```json
{
  "schema": "mash.listset/1",
  "owner": "acme",
  "generation": 2,
  "base_generation": 1,
  "groups": {
    "team": { "mail": "closed", "prompt": "closed",
              "status": "shared", "heartbeat": "shared" }
  },
  "senders": {
    "acme.team.alice": { "allow": ["acme.team.bob"], "deny": [] },
    "acme.team.bob":   { "allow": [], "deny": [] },
    "acme.team.carol": { "allow": [], "deny": [] }
  }
}
```

The format is not final. With this file, Alice may mail Bob but not Carol; Bob
and Carol may mail nobody, and all three can still see one another's status.

### How it would work

1. `mashctl` checks the file and lists every problem in it.
2. It shows what would change.
3. It turns the file into permission rules, signs them into the endpoints'
   credentials and into the account, and pushes them to the server.
4. It proves the result on every server it knows: a send that should be refused
   is refused, and the sender is told in plain words.

A change closes the open connections under the changed rules, and the endpoint
reconnects with the new ones. If a server does not answer, `mashctl` says how
many servers have the new policy, and running the same apply again finishes it.
There is no revert command: you apply a new file.

### Limits that stay

- The owner stays the wall. A policy file governs one owner and cannot give an
  endpoint reach into another owner.
- Only exact addresses are listed in the first version.
- The server enforces size limits on credentials, so a file that would exceed
  them is refused before anything is pushed.
- The policy narrows who may mail whom. It does not narrow the reply and service
  subjects of section 1.
- mash does not sign or accept policy from any outside tool, and does not take
  over another party's account policy.

### Where the code is

The engine lives in `internal/listset`, `internal/policydoc` and
`internal/policyapply`, with tests that run against a real, throwaway
`nats-server`. A check in the release gate refuses any release in which either
program links a policy package, so nothing here is reachable in a released
binary.

## 3. Words

- **Owner, group, instance**: the three parts of an address; each is lower case,
  starts with a letter, and is at most 32 characters.
- **Verb**: what is being done: mail, prompt, status or heartbeat.
- **Account**: the NATS server's unit of isolation.
- **Template and tag**: a permission rule kept once in the account with blanks,
  and the values in a credential that fill the blanks. One template serves a
  whole group.
- **Generation**: the version number of the policy file in force.
