# Old-form search, 0.5.5

The search of the source, tests and docs for the old form, as the 0.5.5 contract asks, run on the final tree of this branch. The excluded paths are the history: decisions, evidence, plans, specs, the changelog, the contracts, the release notes and the changelog fragments.

Counts: search 1 = 25 hits, search 2 = 31 hits, search 3 = 0 hits.

## 1. The old-form words and subjects

```
$ git grep -n -i -E 'agent_token|AGENT_TOKEN|mashctl add|agents\.[a-z*]+\.cc\b|"cc"' -- . ':!docs/decisions' ':!docs/evidence' ':!docs/plans' ':!docs/specs' ':!CHANGELOG.md' ':!contracts' ':!docs/release-notes' ':!changelog.d'
cmd/mash/namerule_status_test.go:17:const statusRuleBody = `{"version":1,"rules":[{"id":"no-cc","match":{"group":{"is":"cc"}},"message":"retired"},{"id":"no-x","match":{"instance":{"is":"x"}},"message":"x"}]}`
cmd/mashctl/audit_test.go:320:	for _, gone := range []string{`"identity"`, `"form"`, `"alias"`, `"renames"`, `"agent_token"`, `"name"`} {
cmd/mashctl/issued_test.go:142:	for _, gone := range []string{`"name"`, `"agent_token"`} {
cmd/mashctl/main_test.go:121:	for _, gone := range []string{"zone", "agent_token", "mashctl reissue", "mashctl retire", "mashctl add"} {
cmd/mashctl/testdata/removed_commands.golden.txt:1:$ mashctl add
docs/mashctl.md:255:| `mashctl add` | `mashctl endpoint create alice/dev/design --public-key design.pub` |
internal/admin/namerule_configa_test.go:26:		`{"id":"no-cc","match":{"group":{"is":"cc"}},"message":"the group cc is retired"},` +
internal/admin/namerule_configa_test.go:43:	earlyAddr := epIn(t, "o", "cc", "early")
internal/admin/namerule_configa_test.go:49:	_, user := e.Mint(t, epIn(t, "o", "cc", "fresh"))
internal/admin/namerule_configa_test.go:58:		{"group cc, no record", epIn(t, "o", "cc", "fresh"), user, "no-cc", true},
internal/admin/namerule_configa_test.go:61:		{"group cc before the directory is read (the user key is not even valid)", epIn(t, "o", "cc", "gone"), "not-a-key", "no-cc", true},
internal/admin/namerule_configa_test.go:97:	for _, g := range []string{"cc", "mash"} {
internal/config/golden_validity_test.go:51:	if _, err := load(envOf(map[string]string{"MASH_NAME": i28 + "cc", "MASH_GROUP": "g"}), home); err == nil {
internal/config/namerule_test.go:12:const goodRuleFile = `{"version":1,"rules":[{"id":"no-cc","match":{"owner":{"is":"cc"}},"message":"the owner cc is retired"}]}`
internal/config/namerule_test.go:54:	cc, _ := sap.NewAddress("cc", "g", "a")
internal/endpoint/directory_poll_test.go:325:		"old":             `{"name":"old","owner":"o","agent":"claude-code","agent_token":"cc","user":"U1","mail_subject":"agents.mail.cc.o.old"}`,
internal/endpoint/newform_key_test.go:130:	if n := e.applyPresence(presenceMsg(t, a, sap.AgentName, "agents.prompt.cc.acme.ops1")); n != 0 {
internal/endpoint/service_test.go:59:	if _, ok := md["agent_token"]; ok {
internal/endpoint/service_test.go:60:		t.Fatalf("service info carries an agent_token: %+v", md)
internal/namerule/namerule_test.go:168:	window := `{"id":"group-ci","match":{"owner":{"is_not":"cc"},"group":{"is":"ci"}},"message":"the group ci is reserved"}`
internal/namerule/namerule_test.go:188:		{"is_not on the owner slot", []string{window}, addr("cc", "ci", "peer"), "", ""},
internal/sap/address_test.go:16:		{"cc", "cc", "cc"},
internal/store/record_test.go:61:	// A record of an older release (name and agent_token, no group or instance) is refused.
internal/store/record_test.go:62:	old := `{"name":"a","owner":"o","agent":"cc","agent_token":"cc","account":"A","user":"U","mail_subject":"agents.mail.cc.o.a","created":"2026-01-01T00:00:00Z"}`
scripts/liveat/refusals.go:180:			{Name: "mashctl add", Label: "removed-add", Home: ha, Args: []string{"add", "alice/dev/old"}, Exit: 2, NoJSON: true, Hint: [][]string{{"endpoint create"}}},
```

## 2. The word legacy

```
$ git grep -n -i legacy -- . ':!docs/decisions' ':!docs/evidence' ':!docs/plans' ':!docs/specs' ':!CHANGELOG.md' ':!contracts' ':!docs/release-notes' ':!changelog.d'
cmd/mash/retry_test.go:317:// neither dropped nor replaced by the legacy copy. A missing policy.md is still an empty policy.
cmd/mash/retry_test.go:344:	if err := os.WriteFile(filepath.Join(dir, "policy.md"), []byte("LEGACY-POLICY-SENTINEL"), 0o644); err != nil {
cmd/mash/retry_test.go:349:	if strings.Contains(instr, "LEGACY-POLICY-SENTINEL") || !strings.Contains(instr, "reading "+pol) {
cmd/mash/retry_test.go:365:	if strings.Contains(instr, "LEGACY-POLICY-SENTINEL") || !strings.Contains(instr, "reading "+pol) {
cmd/mashctl/audit_test.go:197:// serverState is every stream (state, config, the legacy consumer's counts) and every KV entry with its revision.
cmd/mashctl/inbox_inspect_test.go:43:		{"a", "--limit", "-1"}, {"a", "--from", "5", "--to", "4"}, {"a", "--frob"}, {"a", "--json=1"}, {"a", "--legacy"}} {
docs/audit.md:200:- `retired_compared` is `false` and `retired_unverified` is `true` when the earlier report has no `retired` section (made before it existed, or `null`): it cannot show that no final marker was deleted since, so nothing is compared and the three lists are empty. Without `--strict` the comparison stays readable and the text form prints `WARNING retired comparison unverified: the earlier report has no retired section`. With `--strict` the file is refused like one that lacks a required field: exit 3, `retired comparison unverified: the earlier report has no retired section` on standard error and nothing on standard output; regenerate the earlier audit with this mashctl. This applies whatever the current report holds (an absent bucket now may be a deleted one). `retired_unverified` is `false` otherwise. The earlier-report fields table below is about legacy inboxes and does not change. A `retired` section that is present must be whole: `present`, a `markers` array and a `key` and `state` in each marker, or the file is refused with exit 3 naming the missing path (a stripped section would hide a deleted final marker). A marker whose `state` is not exactly `final` or `pending` (for example `Final` or `done`), or whose `key` repeats an earlier marker's, is refused the same way (exit 3, naming the path): the comparison knows only those two states, and with a duplicate the last entry would win and could hide a lost `final` marker.
docs/operations.md:249:- **Complete history.** The stream's metadata key `mash_issued_history` says whether a key's retained revisions can be trusted to be all of them: `full` for a bucket created at 64, `raised:N:S` written once by `mashctl init` when it raises an older bucket from N (S is the stream's last sequence, read after the limit is raised; if the marker write fails, `init` reports the error, the limit stays raised and the bucket has no marker, so the legacy rule applies until `init` is run again), none for a bucket from before the marker. `deprovision` and `endpoint delete` refuse to revoke a key whose history is not shown complete (8 or more revisions with no marker; N or more at or below S after a raise), and say to revoke it by hand in the account JWT only after checking where it was issued. Run `mashctl init` on an older deployment so a later raise is marked; a raise already done without a marker cannot be marked afterwards.
docs/operations.md:470:**Which hooks may drain the spool.** `mash serve` records its owner in `<spool>/.owner` as JSON: its parent pid, its `CLAUDE_CODE_SESSION_ID` and its `MASH_LAUNCH_TOKEN`. A serve that waits for the lease records itself in `<spool>/.waiting.<pid>` (its parent Claude pid and session id) and removes the record when it binds or exits. A hook that descends from such a waiting serve and not from the owner is refused as another launch, with no launch token needed (broken process layouts fall back to the rules below). Otherwise a hook drains the spool if any one of these holds: its session id (the `session_id` on its stdin, else `CLAUDE_CODE_SESSION_ID` from its environment) equals serve's (unless both sides have a non-empty `MASH_LAUNCH_TOKEN` and they differ: that is another launch of the same session, such as a `claude --resume` of a running one, [ADR-0043](decisions/0043-second-launch-of-a-running-session-is-refused-and-told-once.md)); both sides have the same non-empty `MASH_LAUNCH_TOKEN` (this survives `/clear`); or serve's parent pid is among the hook's ancestors. A hook that matches none is refused: it prints nothing, leaves the mail spooled, writes one line to `hook.log` (`mash hook: not this session's spool (...); mail left for its owner`) and records `<spool>/.refused` (at most once a minute). For the other-launch case the hook also prints a one-time notice into that session's context. This keeps a second session with the same `MASH_NAME` from draining this session's mail. `<spool>/.session` keeps the legacy text `pid:N`, so a hook from an older mash still enforces the process-ancestry rule (and nothing else) when serve is newer; a damaged `.owner` makes every hook refuse. Keep serve and hook on the same mash version for the session-id and launch-token rules. `MASH_HOOK_TRACE=1` makes every hook run log its pid chain, both session ids and the rule that matched.
docs/operations.md:474:When Claude Code restarts its MCP server inside one session (an account switch with `/login`, for one), the new `mash serve` can start while the old one is alive and still holds the endpoint lease. The lease records the serving process's parent pid and that parent's start time (`ppid`, `pstart`) and `CLAUDE_CODE_SESSION_ID` (`session`), each only when known. A new serve takes the lease over, by the same compare-and-set as for a dead holder, when the holder is on the same machine and has the same non-empty session id; when either side has no session id, the same parent pid with the same parent start time does instead. Two different non-empty session ids never match, whatever parent they share. The log line is `lease: took over from the same session's earlier serve <instance> (pid P)`. It refuses a lease of another session, another machine, one that lacks those fields, one started under another `MASH_LAUNCH_TOKEN`, and a taker whose own parent pid is 1 or less. What this does and does not protect: the session id and the parent identity are claims the holder writes into the lease. A process on the same machine that holds the endpoint's credentials is already that endpoint (it can publish and consume as it), so a same-session takeover gives it no authority it did not have; the refusals above keep one session from taking another's endpoint by accident or by a differing identity. The earlier serve reads its lease key every second, so it finds out within about a second (it no longer waits for its next refresh, up to 30 seconds): from that moment it neither delivers nor acknowledges any mail, nak's what it held (removing its waiting spool entries) so the new serve is redelivered it at once, stops consuming and refreshing, and becomes inactive. Its tools answer that it was superseded by a newer serve of the same session (naming the pid) and that mail is handled there, `mash status` shows `connection: SUPERSEDED` with the new holder, and it exits when its stdin closes or its parent changes. If the takeover loses its compare-and-set to a write made since, the new serve retries after 1 s, up to 5 times, instead of waiting the usual 10 s. The same rule applies to the legacy identity's lease of an endpoint with an alias.
internal/admin/issued.go:453:	// marker stays absent, which is the legacy 8-revision rule: the error is returned so init does not look clean.
internal/admin/issued.go:459:			return fmt.Errorf("mash: bucket %s: history raised to %d but the marker was not written (the legacy 8-revision rule applies; run init again after removing the cause): %w", store.IssuedBucket, store.IssuedHistory, err)
internal/admin/issued.go:464:		return fmt.Errorf("mash: bucket %s: history raised to %d but the marker was not written (the legacy 8-revision rule applies): %w", store.IssuedBucket, store.IssuedHistory, err)
internal/admin/issued.go:477:		return fmt.Errorf("mash: bucket %s: history raised to %d but the marker was not written (the legacy 8-revision rule applies): %w", store.IssuedBucket, store.IssuedHistory, err)
internal/admin/issued_marker_test.go:192:// A fault between the two updates leaves the limit raised and no marker; init reports it and the legacy 8-revision
internal/apply/escape_test.go:12:const hostileRole = "legacy\x1b[2K\r\nREFUSED: none"
internal/apply/escape_test.go:38:	if !strings.Contains(err.Error(), `legacy\x1b[2K\x0d\x0aREFUSED: none`) {
internal/apply/trust_test.go:175:		sc.Key, sc.Role = scoped, "legacy"
internal/apply/trust_test.go:184:	for _, w := range []string{scoped, "role legacy", plain, "plain signing key", "will not carry signing keys it did not create", "ADR 0091"} {
internal/reconcile/plan_test.go:179:	legacy := userKey(t)
internal/reconcile/plan_test.go:180:	w.connect(w.conn(legacy, w.acct, "legacy", w.now.Add(-time.Hour).Unix()))
internal/reconcile/plan_test.go:182:	r, ok := revocation(p, legacy)
internal/reconcile/plan_test.go:186:	if !strings.Contains(p.Text(), "revoke "+legacy+": connected, untracked") || !strings.Contains(p.Text(), "host 10.0.0.7, server s1") {
internal/reconcile/plan_test.go:283:	w.record(w.exempt, "svc.legacy", w.now.Add(-time.Hour))
internal/reconcile/plan_test.go:529:	legacy := userKey(t)
internal/reconcile/plan_test.go:530:	w.connect(w.conn(legacy, w.acct, "legacy", w.now.Add(-time.Hour).Unix()))
internal/reconcile/plan_test.go:537:	if _, ok := revocation(p, legacy); ok {
internal/reconcile/plan_test.go:540:	if f, ok := finding(p, legacy); !ok || !strings.Contains(f.Detail, "not revoked: the connection scan is incomplete") {
internal/store/issued.go:30:// at that moment. A bucket with neither is a legacy or unknown one.
scripts/scrubcheck/exceptions.tsv:16:internal/store/store_test.go	"\x68erd":"acme"	a live directory record may still carry the removed legacy field; the test proves it is ignored
scripts/scrubcheck/exceptions.tsv:32:CHANGELOG.md	legacy \x68erd read	dated release note: names the removed read as it was called when shipped (D186)
```

## 3. The legacy parsers

```
$ git grep -n -i -E 'sap\.Legacy|LegacyZone|KeyFromSubject|NameFromSubject|SplitMail|EndpointIdentity' -- . ':!docs/decisions' ':!docs/evidence' ':!docs/plans' ':!docs/specs' ':!CHANGELOG.md' ':!contracts' ':!docs/release-notes' ':!changelog.d'
(no output)
```

## Every remaining hit

A hit that is not an asserted refusal, a REQ-009 message, an unrelated word or a plain valid token would be removed; there is none.

| file:line | what it is | the assertion it serves, or why it is not the old form |
|---|---|---|
| cmd/mash/namerule_status_test.go:17 | plain valid token | `cc` is an ordinary owner, group or instance token now; the tests use it as a name that a name rule refuses or the address rule accepts |
| cmd/mashctl/audit_test.go:320 | asserted refusal in a test | the JSON output carries none of the listed removed keys (`agent_token` among them); in main_test.go:121 the help and error text mention none of the removed words or commands |
| cmd/mashctl/issued_test.go:142 | asserted refusal in a test | the JSON output carries none of the listed removed keys (`agent_token` among them); in main_test.go:121 the help and error text mention none of the removed words or commands |
| cmd/mashctl/main_test.go:121 | asserted refusal in a test | the JSON output carries none of the listed removed keys (`agent_token` among them); in main_test.go:121 the help and error text mention none of the removed words or commands |
| cmd/mashctl/testdata/removed_commands.golden.txt:1 | REQ-009 message | golden text of the message `mashctl add` prints: it was removed, use `mashctl endpoint create`; exit 2 |
| docs/mashctl.md:255 | REQ-009 message surface (removed-command table) | row of the table of removed commands mapping `mashctl add` to `mashctl endpoint create` |
| internal/admin/namerule_configa_test.go:26 | plain valid token | `cc` is an ordinary owner, group or instance token now; the tests use it as a name that a name rule refuses or the address rule accepts |
| internal/admin/namerule_configa_test.go:43 | plain valid token | `cc` is an ordinary owner, group or instance token now; the tests use it as a name that a name rule refuses or the address rule accepts |
| internal/admin/namerule_configa_test.go:49 | plain valid token | `cc` is an ordinary owner, group or instance token now; the tests use it as a name that a name rule refuses or the address rule accepts |
| internal/admin/namerule_configa_test.go:58 | plain valid token | `cc` is an ordinary owner, group or instance token now; the tests use it as a name that a name rule refuses or the address rule accepts |
| internal/admin/namerule_configa_test.go:61 | plain valid token | `cc` is an ordinary owner, group or instance token now; the tests use it as a name that a name rule refuses or the address rule accepts |
| internal/admin/namerule_configa_test.go:97 | plain valid token | `cc` is an ordinary owner, group or instance token now; the tests use it as a name that a name rule refuses or the address rule accepts |
| internal/config/golden_validity_test.go:51 | plain valid token | `cc` is an ordinary owner, group or instance token now; the tests use it as a name that a name rule refuses or the address rule accepts |
| internal/config/namerule_test.go:12 | plain valid token | `cc` is an ordinary owner, group or instance token now; the tests use it as a name that a name rule refuses or the address rule accepts |
| internal/config/namerule_test.go:54 | plain valid token | `cc` is an ordinary owner, group or instance token now; the tests use it as a name that a name rule refuses or the address rule accepts |
| internal/endpoint/directory_poll_test.go:325 | asserted refusal in a test | a directory record in the old form (agent_token, cc subject) is put in the KV bucket; the test asserts Who() lists only the new-form records tkey(a) and tkey(b) |
| internal/endpoint/newform_key_test.go:130 | asserted refusal in a test | presence on a subject of the removed form `agents.prompt.cc.acme.ops1` counts for nothing (n must be 0) |
| internal/endpoint/service_test.go:59 | asserted refusal in a test | service info metadata carries no `agent_token` key |
| internal/endpoint/service_test.go:60 | asserted refusal in a test | service info metadata carries no `agent_token` key |
| internal/namerule/namerule_test.go:168 | plain valid token | `cc` is an ordinary owner, group or instance token now; the tests use it as a name that a name rule refuses or the address rule accepts |
| internal/namerule/namerule_test.go:188 | plain valid token | `cc` is an ordinary owner, group or instance token now; the tests use it as a name that a name rule refuses or the address rule accepts |
| internal/sap/address_test.go:16 | plain valid token | `cc` is an ordinary owner, group or instance token now; the tests use it as a name that a name rule refuses or the address rule accepts |
| internal/store/record_test.go:61 | asserted refusal in a test (negative fixture) | a record of an older release (name and agent_token, no group or instance, cc subject) is refused by the record parser |
| internal/store/record_test.go:62 | asserted refusal in a test (negative fixture) | a record of an older release (name and agent_token, no group or instance, cc subject) is refused by the record parser |
| scripts/liveat/refusals.go:180 | asserted refusal (live acceptance run) | the harness runs `mashctl add` and asserts exit 2 with the hint `endpoint create` (REQ-009 message) |
| cmd/mash/retry_test.go:317 | asserted refusal in a test | a policy.md in the old ~/.config/mash holds the sentinel; the test asserts mash does NOT read it (the instructions never contain it) and reports the unreadable XDG file |
| cmd/mash/retry_test.go:344 | asserted refusal in a test | a policy.md in the old ~/.config/mash holds the sentinel; the test asserts mash does NOT read it (the instructions never contain it) and reports the unreadable XDG file |
| cmd/mash/retry_test.go:349 | asserted refusal in a test | a policy.md in the old ~/.config/mash holds the sentinel; the test asserts mash does NOT read it (the instructions never contain it) and reports the unreadable XDG file |
| cmd/mash/retry_test.go:365 | asserted refusal in a test | a policy.md in the old ~/.config/mash holds the sentinel; the test asserts mash does NOT read it (the instructions never contain it) and reports the unreadable XDG file |
| cmd/mashctl/audit_test.go:197 | unrelated word | comment on a JetStream consumer of the server state; no address involved |
| cmd/mashctl/inbox_inspect_test.go:43 | asserted refusal in a test | `--legacy` is an unknown option: parseInboxInspectArgs must refuse it |
| docs/audit.md:200 | unrelated word | the earlier-report fields table is about inboxes of older audit reports |
| docs/operations.md:249 | unrelated word | JetStream history rule: a bucket without the marker keeps the 8-revision limit |
| docs/operations.md:470 | unrelated word | `<spool>/.session` text `pid:N` kept so a hook of an older mash enforces the same rule; not an address or a command |
| docs/operations.md:474 | unrelated word | `<spool>/.session` text `pid:N` kept so a hook of an older mash enforces the same rule; not an address or a command |
| internal/admin/issued.go:453 | unrelated word | JetStream history: the 8-revision rule of a bucket with no marker |
| internal/admin/issued.go:459 | unrelated word | JetStream history: the 8-revision rule of a bucket with no marker |
| internal/admin/issued.go:464 | unrelated word | JetStream history: the 8-revision rule of a bucket with no marker |
| internal/admin/issued.go:477 | unrelated word | JetStream history: the 8-revision rule of a bucket with no marker |
| internal/admin/issued_marker_test.go:192 | unrelated word | JetStream history: the 8-revision rule of a bucket with no marker |
| internal/apply/escape_test.go:12 | unrelated word | the string `legacy` is test data (a hostile role name; a role label), not an old form |
| internal/apply/escape_test.go:38 | unrelated word | the string `legacy` is test data (a hostile role name; a role label), not an old form |
| internal/apply/trust_test.go:175 | unrelated word | the string `legacy` is test data (a hostile role name; a role label), not an old form |
| internal/apply/trust_test.go:184 | unrelated word | the string `legacy` is test data (a hostile role name; a role label), not an old form |
| internal/portable/portable_test.go:36 | enforcement (the forbidden-name list) | `personalRE` lists the names of people, machines and private projects (among them `homerange` and `rangehand`, the layer above) that the portability scan refuses anywhere in source, docs and tests; it is how the contract's reject rule "any layer above mash is named in code, flags, subjects, keys, fields, errors or tests" is enforced. It is not the old form; it names words in order to refuse them |
| internal/portable/portable_test.go:482 | enforcement (its own sample strings) | the test strings that prove `personalRE` matches each listed name and does not match `bogworks` or ordinary words |
| internal/reconcile/plan_test.go:179 | unrelated word | `legacy` is a label for a user connection or record name in a reconcile plan; not an old form |
| internal/reconcile/plan_test.go:180 | unrelated word | `legacy` is a label for a user connection or record name in a reconcile plan; not an old form |
| internal/reconcile/plan_test.go:182 | unrelated word | `legacy` is a label for a user connection or record name in a reconcile plan; not an old form |
| internal/reconcile/plan_test.go:186 | unrelated word | `legacy` is a label for a user connection or record name in a reconcile plan; not an old form |
| internal/reconcile/plan_test.go:283 | unrelated word | `legacy` is a label for a user connection or record name in a reconcile plan; not an old form |
| internal/reconcile/plan_test.go:529 | unrelated word | `legacy` is a label for a user connection or record name in a reconcile plan; not an old form |
| internal/reconcile/plan_test.go:530 | unrelated word | `legacy` is a label for a user connection or record name in a reconcile plan; not an old form |
| internal/reconcile/plan_test.go:537 | unrelated word | `legacy` is a label for a user connection or record name in a reconcile plan; not an old form |
| internal/reconcile/plan_test.go:540 | unrelated word | `legacy` is a label for a user connection or record name in a reconcile plan; not an old form |
| internal/store/issued.go:30 | unrelated word | JetStream history: the 8-revision rule of a bucket with no marker |
| scripts/scrubcheck/exceptions.tsv:16 | unrelated word (the scrub list) | a scrub exception row; the word describes a removed directory field or a dated release note, not an address form |
| scripts/scrubcheck/exceptions.tsv:32 | unrelated word (the scrub list) | a scrub exception row; the word describes a removed directory field or a dated release note, not an address form |

## Fixed in this branch

- internal/testenv/rttproxy_test.go: the sample heartbeat frame is now `agents.hb.o.dev.x`.
- cmd/mash/main_test.go: the two status requests use the address of the endpoint under test.
- docs/images: five SVG figures show `agents.<verb>.OWNER.GROUP.<instance>`.
- scripts/quickstart-synadia.sh: the config directory follows mash: MASH_CONFIG_DIR, an absolute XDG_CONFIG_HOME/mash, else $HOME/.config/mash.
- internal/spool, internal/endpoint golden_names headers, internal/spool streams tests, internal/config, internal/render and internal/release tests: the word and the stale allowlist entry removed.
- docs/install.md: the word `legacy` dropped from the MASH_CONFIG_DIR row.
