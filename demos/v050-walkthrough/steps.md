# Walk-through content (authoritative text; the page uses it verbatim or near-verbatim)

Source of real output: v050-rc-report.md (captured on RC 625d1bc, throwaway server, nothing live).
Each step lists: title, what you see, why it matters, iptables nearest equivalent, and which report section(s) and commands to show.
Keep every step to the commands named; skip the rest of that section. If a named command is missing from the report, skip it and say nothing.

## Intro (before step 1)
mash v0.5.0 does two things. It removes zones: mail always uses one address form, `agents.mail.<agent>.<owner>.<name>`. And it adds an outside-owned messaging policy: once an account is adopted, who may mail whom changes only through list sets signed by one registered key, and the NATS server itself enforces them through each endpoint's credential.
The mental model: a list set is a firewall ruleset for mail. Senders are sources, addresses are destinations, the NATS server is the packet filter, and the credential is the compiled rule.
Everything below is real output from release candidate 625d1bc against a throwaway local server. Nothing live was touched.

## Step 1. The old commands are gone  (report §1: show `mashctl policy check --policy x.yaml`, `mashctl add foo --zone z`, `mashctl set-zone foo z`)
What you see: the zone commands and the old policy-file verbs now refuse, and say what replaced them.
Why: zones are removed (ADR 0091). A script that still calls them fails loudly instead of doing something different.
iptables: a distribution dropping `iptables-legacy`. The old commands error with a pointer to the new tool.

## Step 2. Adopt the account  (report §2: `mashctl policy adopt --show` (before), `mashctl policy adopt --submitter <KEY>` (first one), `mashctl policy adopt --show` (after))
What you see: before adoption nothing owns the policy; after it, one submitter public key is registered, and the admin and system users are recorded as exempt.
Why: from here on, only list sets signed by that key change who may mail whom. mash never holds the private half.
iptables: turning on firewalld with lockdown. A manager now owns the ruleset, and only it may change rules.

## Step 3. Hand edits are refused on an adopted account  (report §3: `mashctl add zed`, `mashctl reissue l1`, `mashctl label l1 k=v`, `mashctl retire l1`)
What you see: the commands that used to change an endpoint's reach directly now refuse on an adopted account.
Why: two owners of one ruleset is how a firewall drifts. One account is run one way only (REQ-016).
iptables: firewalld lockdown refusing a direct `iptables -A`.

## Step 4. A lane that may mail only its lead  (report §5: `mashctl policy diff gen1.json --range lab`, `mashctl policy explain lab.h1.l1 lab.h1.lead --range lab`, `mashctl policy explain lab.h1.l1 lab.h1.l2 --range lab`)
What you see: `diff` shows exactly what the signed list set would change, and applies nothing. `explain` answers one question: may this sender mail that receiver, and which line says so.
Why: you review a change before it exists, and you ask about one pair instead of reading the whole ruleset.
iptables: `diff` is `iptables-restore --test` with a diff of the result. `explain` is `iptables -C` (does this rule match?) combined with a packet trace that names the rule that decided.

## Step 5. Show the policy in force  (report §6: `mashctl policy show --range lab`)
What you see: every sender, and whom it may mail, as it is in force now.
Why: the read-back is from the policy store, not from the file you hoped you applied.
iptables: `iptables -S` (or `-L -n -v`): the rules as the kernel holds them.

## Step 6. A send-only service  (report §7: both `explain` commands, lead→queue-alarm and queue-alarm→lead)
What you see: the service may mail the lead; nobody may mail the service.
Why: alarms and bots should talk, not be talked to.
iptables: `OUTPUT ACCEPT` for the service with `INPUT DROP` to it. There is no conntrack-style return path in mail; each direction is its own rule.

## Step 7. One user blocks one colleague  (report §8: the three `explain` commands)
What you see: the ops group shares mail, so its members may mail each other. alice has a deny entry for bob, so alice may not mail bob, but she may mail carol, and bob may still mail alice.
Why: a deny is a narrow exception inside an open group, and it is one-directional.
iptables: one `-A OUTPUT -s alice -d bob -j DROP` ahead of a group-wide `ACCEPT`, with no matching rule for bob to alice.

## Step 8. Zones in a document are refused  (report §9: `mashctl policy diff z1-zones-field.json --range lab`, `mashctl unzone stray`)
What you see: a list set that mentions a zone is refused; `unzone` converts an old zoned record.
Why: no half-zoned state can sneak back in.
iptables: a rule that jumps to a chain that no longer exists: `No chain/target/match by that name`, and nothing is applied.

## Step 9. Only a signed document can change the policy  (report §10: both `diff gen3.json` commands, plus the "apply of gen3 by mashctl policy serve" line that follows)
What you see: `diff` on the unsigned document previews the change (it adds the queue-alarm service to l1's reach). Naming the submitter key makes `diff` check the signature, and the unsigned document fails. The signed copy, handed to `policy serve`, is applied.
Why: the preview is free for anyone; the change itself happens only with the registered submitter's signature.
iptables: no direct equivalent. iptables trusts whoever is root; mash trusts only the registered submitter's signature.

## Step 10. A stale base and a repeat are refused  (report §11: `diff gen3.json`, `diff gen4.json`, `show`)
What you see: gen3 is now refused because it was written against generation 1 while generation 2 is in force. gen4 has the same content as what is in force, so there is nothing to apply. `show` reports generation 2 in force.
Why: two people editing at once cannot silently overwrite each other, and a resend changes nothing.
iptables: nftables' ruleset generation check (a commit against a stale generation fails), plus `iptables-restore --test`.

## Step 11. A bad file changes nothing  (report §12: `diff v02-truncated.json`, `diff v09-duplicate-key.json`, `diff v11-wildcard.json`, `diff gen1-otherkey.jws --submitter …`)
What you see: truncated JSON, a duplicate key, a wildcard and a document signed by the wrong key are each refused with the reason.
Why: a list set is applied whole or not at all.
iptables: `iptables-restore` rejecting a malformed file with a line number, leaving the running ruleset as it was (atomic commit).

## Step 12. Taking reach away is refused in this release  (report §13: `diff gen2.json`, the "apply of gen2" line, `show`)
What you see: gen2 removes a permission from an existing sender. It is refused: narrowing a sender needs a new credential for that sender, and v0.5.0 does not replace credentials yet. `show` reports generation 1 still in force and the attempt refused. This is caveat 1 on screen.
Why: removing a rule without also cutting the sender's existing credential would leave the old reach working. mash refuses the change rather than half-apply it.
iptables: `iptables -D` without `conntrack -D` leaves established flows open. mash refuses until it can do both (planned with the issue service).

## Step 13. What the sender sees  (report §14: the two `mash send` lines, `mash policy show` and `mash policy explain lab.h1.l2` as l1)
What you see: an allowed send goes; a refused send says it is refused by policy and why; the endpoint can ask what it may reach without admin rights.
Why: a refused mail is never silently lost, and an agent can check before it tries.
iptables: `REJECT --reject-with icmp-admin-prohibited` rather than `DROP`: the sender is told.

## Step 14. The adoption reconcile, one way  (report §15: `reconcile --range lab --servers 1` (plan), the first `reconcile --yes --plan sha256:…`, the second identical `--yes --plan` (refused), `reconcile` again, `issued list`)
What you see: the dry run plans one revocation (the legacy endpoint "stray", not in the list set), warns that `--yes` is one-way, and prints a plan digest. `--yes --plan <digest>` revokes exactly that. Re-running the same plan is refused because the world changed. A fresh dry run finds nothing to do, and `issued list` shows stray's key revoked.
Why: it removes credentials the list set does not cover. A revoked credential cannot be reissued on an adopted account in this release, and there is no un-adopt.
iptables: `firewall-cmd --complete-reload`: rules nobody declared are flushed and their connections dropped. The difference is that mash makes you read the plan, then pin it by digest.

## Close
Three calls for you (the one-page brief has the detail):
1. Keys: run live `add`, `reissue` and `unzone` only on the key-store machine for now, and move them to the issue service later.
2. Exception limits: publish the measured table (for example 83 exceptions per sender at 14-character names), not "about 100".
3. Reconcile: ship it one-way. A real adoption needs its own go and a rehearsal on a throwaway account.
Then: go or no-go on tagging v0.5.0 at 625d1bc.

## The four caveats (pinned, always visible)
1. A list set that narrows an existing sender is refused in v0.5.0.
2. A brand-new address is refused until the issue-service path exists (test accounts only).
3. Exception limits are the measured ones (83 at 14-character names), not the contract's 100.
4. The help text is final only at the release commit.
