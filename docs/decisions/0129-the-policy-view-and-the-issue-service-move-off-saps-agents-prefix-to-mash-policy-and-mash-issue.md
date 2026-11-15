---
status: proposed
date: 2026-11-15
decision-makers: Randy Bias (mash maintainer), Decision 229 (no change to SAP, no divergence in the SAP Go SDK); the mash coordinator (mash-lead), for the subjects, the window and the order of the upgrade
---

# The policy view and the issue service move off SAP's `agents` prefix, to `mash.policy` and `mash.issue`

Amends: ADR-0110, ADR-0104

## Context and Problem Statement

SAP reserves the subject prefix `agents` for its own protocol (SAP 0.3 §2). Two v0.5.0 services sit under it, and both are
mash's own:

* the policy view, on `agents.policy.<range>`
  ([ADR 0110](0110-a-sender-sees-only-its-own-reach-through-a-signed-policy-view-request.md));
* the issue service, on `agents.issue.<range>.<owner>`
  ([ADR 0104](0104-an-issue-service-signs-a-credential-for-a-public-key-a-wrangler-countersigned.md)).

Neither is recorded as a departure from SAP. ADR 0104's comment on the issue subject argues it is safe because `issue` is
none of the verbs (`internal/issue/service.go:19-22`). That is true, but it does not answer the reservation.

Randy's Decision 229 (2026-11-15) rules that there is to be no change to SAP and no divergence in the SAP Go SDK. Where
mash needs something, it takes an approach that SAP as published allows. A mash service that is not SAP belongs outside
SAP's prefix. v0.6.0 rebuilds mash on the SAP Go SDK, so this is also the point where mash stops asking any SAP library
to tolerate a mash subject under `agents`.

The policy-view subject is part of every list-set sender's permission template: it is the one fixed publish line
(`internal/listset/compile.go:150`, `:166-169`). The template sits in the account JWT. By
[ADR 0097](0097-a-template-change-on-a-scoped-signing-key-closes-open-connections-and-binds-the-reconnect.md), pushing a
changed template closes the connection of every sender under that key. Messages in flight at the close can be lost.
Moving the subject is therefore a live, reconnect-causing step on every adopted account. The question is how to make
that move once, safely, and without making an applied policy look open to anyone in between.

The issue subject is not in any template. A wrangler's permission to publish on it is set by hand in the account
(docs/operations.md).

`agents.mail` is outside this record. It is the remaining mash use of the prefix, under its own decision
([ADR 0004](0004-mail-is-a-declared-sap-verb.md)). Moving it would change every inbox stream and every endpoint's
permissions, and would break mail between v0.5.0 and v0.6.0 endpoints. It is not decided here.

## Decision Drivers

* No change to SAP and no SDK divergence (Decision 229). mash's own services live outside `agents`.
* One template push per adopted account, not two, because each push closes every sender's connection (ADR 0097).
* No point in the upgrade at which `mash policy show|explain` gives the open answer for an account with a list set in
  force. ADR 0110's rule, that a refusal is an error and never "open", holds throughout.
* A v0.5.0 endpoint keeps working until its operator upgrades it, and the order of the upgrade is written down.
* The push stays a stop point: it runs on a live account only on an explicit go.

## Considered Options

* Move both subjects to `mash.policy.<range>` and `mash.issue.<range>.<owner>`. v0.6.0 serves both old and new
  subjects for one release, and the template is pushed once, with the new line only (chosen).
* Move both subjects with no window. One push changes the template, and the services answer only the new subject.
* Move both subjects with a window carried in the template: one push adds the new line beside the old one, and a
  second push in the next release removes the old one.
* Keep both subjects under `agents`, and record them as departures from SAP.

## Decision Outcome

Chosen option: "Move both, serve both for one release, push the template once".

**The subjects.**
* The policy view moves from `agents.policy.<range>` to `mash.policy.<range>`. `listset.PolicySubject` stays its single
  source.
* The issue service moves from `agents.issue.<range>.<owner>` to `mash.issue.<range>.<owner>`. `issue.SubjectPrefix`
  stays its single source.
* `mash.` is a prefix for mash-internal control subjects. One subject already uses it: `mash.suspended.<name>`, the
  subject a suspended inbox is rewritten to (`internal/admin/suspend.go`). It has a different second token and shape,
  so it does not overlap either new subject.
* No account permission may use `mash.>`. Each user is granted its exact lines only.
* The other `mash.` strings in the code (`mash.listset.v1`, `mash.compiled.v1`) are hash domain tags, not subjects.

**The window: v0.6.0 serves both subjects.**
* `mashctl policy serve` mounts the policy view on both `mash.policy.<range>` and `agents.policy.<range>`.
* `mashctl issue serve` answers on both `mash.issue.<range>.*` and `agents.issue.<range>.*`.
* The v0.6.0 `mash policy show|explain` asks `mash.policy.<range>` first. On a permission violation or no responder there,
  it asks `agents.policy.<range>` once.
* Only when both attempts fail does it report ADR 0110's error ("policy view refused" or "policy view unavailable"). It
  never reports the open answer for a list-set credential.
* A v0.5.0 client asks `agents.policy.<range>`, which a v0.6.0 serve still answers.

**The template: one push per adopted account.**
* From v0.6.0, `listset.Compile` emits only `mash.policy.<range>` as the fixed publish line.
* For the window release, `Check` accepts a template carrying either line, the old or the new. A list set compiled by
  v0.5.0 is therefore not refused, and an account can stay on its old template until its push.
* **Comparisons with what is already signed or published accept the old form for the window release.**
  * Until its push, an account's in-force generation was signed, and its scoped templates published, with the old line.
  * Every check that compares a fresh compile with them would fail on that one line alone. So, for the window release,
    `listset.Compile` can produce either form of the fixed line, and each such check accepts the old form as well:
    * The apply's check that the previous generation's compiled output digests to what that generation signed
      (`internal/policyapply/run.go:585`). The list set in force is compiled with the new line first. When that does
      not match the signed digest, it is compiled again with the old line, and that compile is the previous. Any other
      mismatch is refused, as now.
    * The issue service's check that an owner group's published scoped signer carries the list set's template
      (`internal/issue/scope.go`, `template_mismatch`). It accepts a published template equal to the compile in either
      form, so `issue serve` keeps issuing on an account that has not had its push.
    * Any other place that compares a compile with a signed or published template, which the implementing pull
      request lists. It gets the same either-form rule, and nothing else.
  * The old form is accepted only where it is compared with something already signed or published. Nothing new is ever
    compiled, signed or pushed with it.
* **The push is the next apply on that account under v0.6.0.** That apply changes every group's template.
  * Its compiled output differs from the generation in force by the fixed line. A resubmission of the list set in force
    is therefore not refused as unchanged (`listset.Accept`), and is applied as the next generation.
  * So the submitter can make the push without changing any list.
* By ADR 0097, the apply closes the connection of every sender in the range once. No credential is reissued: a user JWT
  issued under a scoped key takes the template at connect.
* It is a stop point. It runs on an adopted live account only on an explicit go, as for any reconnect-causing apply.
* Credentials made with `mashctl add` carry explicit permissions and no list-set template. They answer `mash policy
  show` locally (ADR 0110), so they are unaffected.

**The order of the upgrade**, which goes in the v0.6.0 CHANGELOG's Upgrading section:

1. Upgrade `mashctl` and restart `policy serve` and `issue serve`. They now answer both subjects.
2. Upgrade the endpoints. A v0.6.0 client works on an old template through the fallback.
3. On a go, run the next apply on each adopted account. That pushes the new template, once.
4. Change each wrangler's account permission from `agents.issue.<range>.<owner>` to `mash.issue.<range>.<owner>`, at any
   time in the window. It is not in a template, so it closes no connection.

An endpoint left on v0.5.0 after step 3 cannot publish `agents.policy.<range>` any more. Its `mash policy show` fails
with ADR 0110's error. It does not fail open.

**The window closes in the next release.**
* Serve and issue drop the `agents.` subjects.
* The client drops its fallback.
* `Check` stops accepting the old line. An account that never had its push is refused at apply time, with a message that
  names the push.

### What this amends

* **ADR 0110:** its subject. "`agents.policy.<range>`" becomes "`mash.policy.<range>`", with the one-release window
  above. Everything else in ADR 0110 stands:
  * the signed request;
  * the directory mapping;
  * the reply on the sender's own `_INBOX` prefix;
  * the local answer for an account without list sets;
  * the error, never the open answer.
* **ADR 0104:** its subject. "`agents.issue.<range>.*`" and "`agents.issue.<range>.<owner>`" become `mash.issue.…`,
  with the same window. Everything else in ADR 0104 stands. The rule that no template mash compiles can reach the issue
  subject still holds. Its test now checks a prefix that no `agents.` line covers.

### Consequences

* Good, because mash's own services leave SAP's reserved prefix. Under `agents`, only `prompt`, `status` and `hb` remain
  (SAP's), plus `mail` (ADR 0004).
* Good, because each adopted account takes one template push, not two.
* Good, because no step makes an applied policy look open. The fallback only widens where the client asks, and the
  answer still comes from the signed responder.
* Bad, because every adopted account must take one reconnect-causing push, on a go. Each account that adopts v0.5.0
  before v0.6.0 adds one such push, so this lands early in v0.6.0.
* Bad, because for one release `Check` accepts two forms of the fixed line, and serve and issue listen on two subjects.
  That is more code, and more to remove in the following release.
* Neutral, because a sender's reply subject does not change: it is still its own `_INBOX` prefix.

### Confirmation

* The `Check` rule that every non-fixed line is an `agents.` subject (`internal/listset/check.go:336`) is rewritten,
  together with `isPolicySubject` (`check.go:234`) and the open-subject listing (`internal/listset/explain.go:265`).
  Their tests cover:
  * a template with the old line;
  * a template with the new line;
  * a template with neither, refused;
  * a `mash.>` line, refused.
* The policy-view tests run `mash policy show|explain` four ways:
  * a v0.6.0 client against a v0.6.0 serve, on both an old and a new template;
  * a v0.5.0 client's subject against a v0.6.0 serve;
  * a v0.6.0 client against an old serve.

  None gives the open answer for a list-set credential.
* The issue-service test that no template reaches the issue subject checks `mash.issue.<range>.<owner>`.
* Two tests prove the window's old-form acceptance:
  * **An apply on top of a generation signed with the old line succeeds:** the previous is compiled in the old form and
    digests to what was signed, and the push carries only the new line.
  * **`issue serve` issues against a published template in the old form, and in the new form.** It refuses any other
    difference with `template_mismatch`.
* A resubmission of the list set in force, made under v0.6.0 on an account still on the old line, is accepted as the
  next generation.
* The two-server and real-send tests run again on the new subjects.

## Pros and Cons of the Options

### Move both, serve both for one release, push the template once (chosen)

* Good, because there is one push per account, and nothing fails open.
* Bad, because the window adds code for one release.

### Move both with no window

* Good, because it has the least code.
* Bad, because after the push a v0.5.0 client gets an error, and before it a v0.6.0 client gets one. The upgrade order
  cannot avoid both, so `mash policy show` fails for some senders until the last endpoint is upgraded.

### Move both with the window in the template (two pushes)

* Good, because clients need no fallback.
* Bad, because every adopted account takes two reconnect-causing pushes, one per release.

### Keep both under `agents` and record the departure

* Good, because nothing changes on a live account.
* Bad, because mash keeps two of its own services under the prefix SAP reserves, against Decision 229. The cost of moving
  later grows with every account that adopts.

## More Information

* The sizing of this change, taken on main 15021a3:
  * every place each subject is built or matched;
  * 6 tests and about 11 goldens;
  * the docs;
  * about 8 to 11 hours for one pull request.
* The v0.6.0 work plan does this first, because it does not depend on the SDK adoption.
* This record is proposed. It changes no code. The implementing pull request follows the v0.5.0 tag and sets it accepted.
