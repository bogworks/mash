# Who may mail whom: reach today, and the security policy

> **Status.** The security policy mentioned in section 2 is planned and is **not
> in any release**. No command for it exists. Section 1 describes what mash does
> today.

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

## 2. Planned: narrowing reach inside an owner

A later release, with no date, is planned to let an administrator narrow reach
inside one owner. The default stays as it is today, wide open. The NATS server
enforces the narrowing, not mash, as it does the fixed permissions of section 1.
The details come when it is designed. Until then nothing in a release can turn
it on.

## 3. Words

- **Owner, group, instance**: the three parts of an address; each is lower case,
  starts with a letter, and is at most 32 characters.
- **Account**: the NATS server's unit of isolation.
