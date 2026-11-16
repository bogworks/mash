# How an `add` endpoint comes under a policy (design note)

Lane core, 2026-11-16. Question from the lead: a solo developer's `mashctl add` endpoint is legacy, and the credential primitive (ADR 0136) refuses a legacy identity, so it cannot be governed. Either move the endpoint to the new form, or make `add` produce new-form endpoints.

## Facts (read in the code)

- A legacy identity is `cc.<owner>.<name>`; its flat key is `<name>` (`sap.Legacy`). Its inbox stream is `inbox-<name>`, its reply prefix `_INBOX.<name>`, its lease key `<name>` (`admin.PermissionsTo`, `sap.InboxPrefix`). A new-form flat key is `<agent>_<owner>_<name>`.
- The only reason a legacy endpoint cannot be governed is one refusal in `listset.EndpointTag` ("its flat key has no owner, so no template line could be exact for it"). The template already wildcards the agent token (`agents.<verb>.*.<owner>.<name>`, ADR 0132) and takes the inbox, lease and reply lines from `{{tag(ep)}}`. The legacy shape is the same shape with a shorter flat key: `ep:<name>` makes the same lines exact for a legacy sender.
- A legacy flat key can never equal a new-form one (legacy names are hyphenated words; a new-form flat key has underscores).
- A legacy flat key has no owner, so the name alone must not be enough: the directory is keyed on the name, so `<name>` belongs to exactly one owner. A credential tagged `ep:<name>` for another owner's address would reach that owner's inbox.
- `add` and `reissue` sign with the account key and mint the owner-wide set, untagged (`mint.Mint`). Under an applied list set that credential is not governed: spike 8 applied a policy and the senders reached each other as before. `zoneGuard` refuses only a zone policy of an older release.
- Moving an endpoint to the new form exists in the engine (`ProvisionIdentity` with an alias, ADR 0076) but costs a new identity, a new inbox stream, an alias window with two streams, a changed `agent_token` on the host and a new credential. `init --local` writes no agent token, so a solo developer is legacy today.

## Options

1. **Govern the legacy endpoint in place (recommended).** `EndpointTag` accepts a legacy identity and returns `ep:<name>`, and the issuer adds one rule: the legacy tag is issued only when the directory holds a legacy record named `<name>` under the address's owner. Nothing moves: no new inbox, no alias window, no new agent token, no stored mail moves. A solo developer runs `add`, then gets a governed credential through the primitive for the same name.
2. **`add` makes new-form endpoints by default from 0.5.5.** Needs a default agent token (a naming choice that is the maintainer's), a new `agent_token` in what `init --local` writes, and every existing legacy endpoint still needs the alias migration. It is a larger change that the first option does not need, and it changes what `add` produces for everyone.
3. **Migrate legacy endpoints to the new form first.** The migration of option 2 for every user before any policy: the longest path.

Option 1 does not decide option 2. A default agent token can follow later without changing anything below.

## What changes under option 1

- `listset.EndpointTag`: a legacy identity gives `ep:<name>`; the owner/name-at-address check stays.
- The issuer's directory check for a legacy tag (one read of the record named `<name>`: legacy, same owner). No record, another owner's record or a new-form record at that name is refused `endpoint_mismatch`. This is a seam in `issue.Authority` (a `Directory` lookup), supplied by the admin connection on the direct path; the issue service supplies the same.
- `credential.Issue` and the live test cover a legacy endpoint: x, y and z made by `add`, governed by the apply plus a credential, z cannot reach y.
- `add` and `reissue` refuse while a list set is in force, as `zoneGuard` does for a zone policy: the owner-wide untagged set they mint bypasses the list set. The error says to get the credential from the primitive. Without this guard a policy and `add` can contradict each other silently, which is the spike 8 failure. The CLI face of the governed path (what the verb is called, whether `add` calls the primitive itself) is ctl's and the maintainer's answer on #681; this change only adds the refusal and the engine.
- Existing legacy credentials (untagged, owner-wide) are not governed until reissued through the primitive; the apply's plan already lists them (`first_credential`, `new_credential`) and `issued check` shows them.

## Risks

- The legacy tag rests on one directory read: a stale or forged record cannot be forged by an endpoint (it cannot write the directory), and the issuer holds the admin connection. The check fails closed on a read error.
- A legacy endpoint renamed later (alias window) keeps its old tag until reissued; reissue gives it the new flat key.

## Not decided here

Whether `add` itself calls the primitive, the verb names, the first-policy default and a default agent token. All stay with the maintainer.
