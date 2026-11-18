# Old-form search, 0.5.5

Run 2026-11-18 on the code commit `88e2a86b74c4ad22986a69e6479faa013bfa3300` (main; HEAD of this branch has the same tree apart from docs/evidence, checked with `git diff 88e2a86 --stat -- . ':!docs/evidence'`: empty).

The search of the source, tests and docs for the old form, as the 0.5.5 contract asks. Searches 1 to 3 exclude the history: decisions, evidence, plans, specs, the changelog, the contracts, the release notes and the changelog fragments. Search 4 (the names removed by REQ-014) excludes nothing, so every hit anywhere in the tracked tree is listed and classified.

Counts: search 1 = 26 hits, search 2 = 33 hits, search 3 = 0 hits, search 4 = 38 hits.

No hit of any search is in shipped code (a non-test Go file, a flag, a field, an error or a message) apart from the two REQ-009 message surfaces and the live-run harness that asserts it, listed below; search 4 has no hit in any non-test source file.

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
receipts/0.5.5-endpoint-model/final-gate-a191f08b7cdb-20261118T061551Z.md:117:- **Blocking — required verification evidence is absent.** The [completion contract requires the forbidden-word test, SDK compatibility gate, and old-form source search with output](pages/contract/contracts__0.5.5-endpoint-model.md:519). The handed records show acceptance scenarios and an AT-004 Go suite, but no named run and output for those three checks. The old-form search needs particular care: [tests still contain `agent_token` and the old `cc` subject](internal/store/record_test.go:61), whereas the [success measure allows old-form references only in release notes and REQ-009 messages](pages/contract/contracts__0.5.5-endpoint-model.md:77). Consequence: the required boundary and removal checks cannot be verified against this revision. Resolve by supplying the runs and search output, and reconciling the test fixtures with the ratified success measure. **Severity: blocking. Certainty: high for missing evidence and the cited source conflict.**
scripts/liveat/refusals.go:181:			{Name: "mashctl add", Label: "removed-add", Home: ha, Args: []string{"add", "alice/dev/old"}, Exit: 2, NoJSON: true, Hint: [][]string{{"endpoint create"}}},
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
receipts/0.5.5-endpoint-model/final-gate-a191f08b7cdb-20261118T061551Z.md:26:- decisions: pages/decisions/docs__decisions__0076-a-three-token-record-may-carry-the-legacy-identity-it-answers-to-as-an-alias.md = bogworks/mash@a191f08b7cdb:docs/decisions/0076-a-three-token-record-may-carry-the-legacy-identity-it-answers-to-as-an-alias.md sha256 6f9b0203c3a22244088ff90e501ba40951b46dff9f0b4bbc77edeb993be21f9d
receipts/0.5.5-endpoint-model/final-gate-d25cdcaf4041-20261118T095140Z.md:30:- decisions: pages/decisions/docs__decisions__0076-a-three-token-record-may-carry-the-legacy-identity-it-answers-to-as-an-alias.md = bogworks/mash@d25cdcaf4041:docs/decisions/0076-a-three-token-record-may-carry-the-legacy-identity-it-answers-to-as-an-alias.md sha256 6f9b0203c3a22244088ff90e501ba40951b46dff9f0b4bbc77edeb993be21f9d
receipts/0.5.5-endpoint-model/final-gate-d25cdcaf4041-20261118T095140Z.md:169:- **The hard-cut documentation and search classification disagree. Severity: medium. Certainty: confirmed.** The old-form search classifies `docs/operations.md:474` as an unrelated PID marker even though that line still says the lease rule applies to a legacy identity with an alias. The release draft also promises a replacement hint for `unzone`, while the CLI test expects `unknown command` and the removal table gives the hint to `retire`. An operator following either description gets incorrect cutover guidance. Remove the stale lease sentence, correct the removed-command list, and refresh the search evidence against the release tree. [Contract REQ-009](pages/contract/contracts__0.5.5-endpoint-model.md#L250), [standing orders lines 50–56](pages/orders/standing-orders__mash.md#L50), [old-form search line 122](pages/evidence/docs__evidence__2026-11-17-0-5-5-live-runs__old-form-search.md#L122), [operations guide line 474](work/docs/operations.md#L474), [release draft lines 41–43](pages/evidence/docs__release-notes__0.5.5-draft.md#L41), [removed-command table](work/cmd/mashctl/main.go#L149), [CLI test line 114](work/cmd/mashctl/main_test.go#L114)
scripts/scrubcheck/exceptions.tsv:16:internal/store/store_test.go	"\x68erd":"acme"	a live directory record may still carry the removed legacy field; the test proves it is ignored
scripts/scrubcheck/exceptions.tsv:32:CHANGELOG.md	legacy \x68erd read	dated release note: names the removed read as it was called when shipped (D186)
```

## 3. The legacy parsers

```
$ git grep -n -i -E 'sap\.Legacy|LegacyZone|KeyFromSubject|NameFromSubject|SplitMail|EndpointIdentity' -- . ':!docs/decisions' ':!docs/evidence' ':!docs/plans' ':!docs/specs' ':!CHANGELOG.md' ':!contracts' ':!docs/release-notes' ':!changelog.d'
(no output)
```

## 4. The names REQ-014 removed (whole tree, no exclusions)

```
$ git grep -n -I -E 'insecure_loopback|MASH_INSECURE_LOOPBACK|require_tls|MASH_REQUIRE_TLS'
CHANGELOG.md:23:  - refuse unencrypted connections to remote servers (`MASH_REQUIRE_TLS`);
CHANGELOG.md:70:- **`MASH_REQUIRE_TLS`.** Refuses plain-text connections to any host that isn't your own machine. `mash status` now warns about a plain-text remote address in every form ([ADR 0108](https://github.com/bogworks/mash/blob/v0.5.0/docs/decisions/0108-require-tls-makes-endpoint-connections-refuse-plain-text-to-a-remote-host.md)).
CHANGELOG.md:229:- `mashctl policy apply`, `diff`, `repair`, `drift` and `adopt` verify the account JWT's issuer: its signature must be valid, its subject the account key, and its issuer the operator key or a key declared by the operator JWT (`<key dir>/operator.jwt`, which `init --local` records). `adopt` now refuses where it warned. Apply only adds or removes mash's own zone keys: any other signing key in the account JWT is refused (named by public key and role; `diff` and `drift` report it): mashctl will not carry signing keys it did not create, so they must be removed with the tool that added them. Admin and system connections require TLS: a plain `nats://` URL to a non-loopback host is refused, and a local server made by `init --local` opts in with `insecure_loopback` in `config.json` (or `MASH_INSECURE_LOOPBACK=1`); `init --local --start` connects only to the loopback server it just started and needs no opt-in. See [ADR 0059](docs/decisions/0059-mashctl-checks-who-issued-the-account-jwt-carries-only-its-own-signing-keys-and-requires-tls-for-admin-connections.md).
changelog.d/tls-everywhere.changed.md:1:**Every `mash` and `mashctl` connection now requires TLS, loopback included.** A plain `nats://` URL (or one with no scheme, or `ws://`) is refused before any connection is made, unless `insecure_plaintext` (`MASH_INSECURE_PLAINTEXT=1`) is set; with it set, mash connects, prints a warning on standard error and shows it in `mash status`. Use a `tls://` URL, and `tls_ca` for a private certificate authority. `mashctl init --local` makes its own certificate authority and runs its server with TLS only, so the quick start needs no override. `insecure_loopback` and `require_tls` (`MASH_INSECURE_LOOPBACK`, `MASH_REQUIRE_TLS`) are removed and are not read ([ADR 0146](docs/decisions/0146-every-mash-and-mashctl-connection-requires-tls-loopback-included-and-one-override-insecure-plaintext-allows-plain-text.md)).
changelog.d/tls-required.changed.md:1:**Every mash connection now requires TLS, including to localhost.** `mashctl init --local` makes a certificate authority and a server certificate and writes a `tls://` URL and `tls_ca` into `config.json`, so the quick start runs over TLS with no extra setting. A plain `nats://` or `ws://` URL is refused before mash connects, with exit code 2, naming the fix: use a `tls://` URL with the server's certificate (`tls_ca` for a private CA). The one override is `insecure_plaintext` (`MASH_INSECURE_PLAINTEXT=1`), for a throwaway server in a test; each command then prints a warning, because plain text lets anyone on the network read mail and obtain signatures. The settings `insecure_loopback` and `require_tls` (`MASH_INSECURE_LOOPBACK`, `MASH_REQUIRE_TLS`) are gone.
contracts/0.5.5-endpoint-model.md:315:  `insecure_loopback` and `require_tls`. With the override set, the
contracts/0.5.5-endpoint-model.md:319:  with TLS and no override. This supersedes the `require_tls` default of ADR 0108 and the loopback allowance of ADR
docs/decisions/0059-mashctl-checks-who-issued-the-account-jwt-carries-only-its-own-signing-keys-and-requires-tls-for-admin-connections.md:37:Transport. Every connection mashctl makes with admin or system credentials (`natsauth.RequireTLS`) requires TLS: the connection fails when the server does not offer it, and a plain `nats://` or `ws://` URL to a host that is not `localhost` or a loopback IP literal is refused before any connection is made. The one opt-in for plain text is the setting `insecure_loopback` (`MASH_INSECURE_LOOPBACK=1`), a setting and not a command-line flag, honoured only when every host of the URL is loopback and refused otherwise; it may be renamed when the command-line settings are redesigned. `init --local` writes it into the `config.json` it creates. `init --local --start` connects only to the loopback server that same run generated and started, which has no TLS, and needs no opt-in; `bootstrap.Start` takes that as an internal parameter and refuses a URL that is not loopback. The client ignores server addresses the server advertises (`nats.IgnoreDiscoveredServers`) in both modes, and with `insecure_loopback` it dials through a dialer that refuses any address that is not `localhost` or a loopback IP, at connect and at every reconnect. Endpoint connections are unchanged.
docs/decisions/0108-require-tls-makes-endpoint-connections-refuse-plain-text-to-a-remote-host.md:2:status: superseded by ADR-0146; only its require_tls default and its loopback allowance
docs/decisions/0108-require-tls-makes-endpoint-connections-refuse-plain-text-to-a-remote-host.md:7:# MASH_REQUIRE_TLS makes endpoint connections refuse plain text to a remote host
docs/decisions/0108-require-tls-makes-endpoint-connections-refuse-plain-text-to-a-remote-host.md:28:* A concrete setting, `require_tls` (`MASH_REQUIRE_TLS`), default off, loopback always allowed (chosen).
docs/decisions/0108-require-tls-makes-endpoint-connections-refuse-plain-text-to-a-remote-host.md:34:Chosen: a concrete boolean setting, `require_tls` in `config.json` or `MASH_REQUIRE_TLS` in the environment, read like
docs/decisions/0108-require-tls-makes-endpoint-connections-refuse-plain-text-to-a-remote-host.md:44:a loopback host is always allowed, with no `insecure_loopback`, and a plain loopback connection is pinned to
docs/decisions/0108-require-tls-makes-endpoint-connections-refuse-plain-text-to-a-remote-host.md:56:`MASH_REQUIRE_TLS=1` to make it a refusal). With the setting on it prints `tls: OK (required)` when the URL is
docs/decisions/0129-the-policy-commands-are-not-part-of-v0-5-0-and-the-engine-stays-internal-until-it-works-standalone.md:54:* Zone removal, `mashctl unzone`, and every hardening change: the issue record, revocation checks, `MASH_REQUIRE_TLS`, `MASH_SPOOL_REQUIRED`, `MASH_PLUGIN_REQUIRE_VERIFIED` and `mashctl inbox inspect`.
docs/decisions/0146-every-mash-and-mashctl-connection-requires-tls-loopback-included-and-one-override-insecure-plaintext-allows-plain-text.md:13:with `insecure_loopback` to allow other plain hosts for mashctl). Endpoint connections allowed it everywhere unless
docs/decisions/0146-every-mash-and-mashctl-connection-requires-tls-loopback-included-and-one-override-insecure-plaintext-allows-plain-text.md:14:`require_tls` was set, and then still allowed it to loopback
docs/decisions/0146-every-mash-and-mashctl-connection-requires-tls-loopback-included-and-one-override-insecure-plaintext-allows-plain-text.md:32:* Keep ADR 0108: `require_tls` off by default, loopback always allowed, `insecure_loopback` for mashctl.
docs/decisions/0146-every-mash-and-mashctl-connection-requires-tls-loopback-included-and-one-override-insecure-plaintext-allows-plain-text.md:53:* **Removed.** `insecure_loopback`, `require_tls` and `MASH_REQUIRE_TLS` (all are no longer read), and the
docs/decisions/0146-every-mash-and-mashctl-connection-requires-tls-loopback-included-and-one-override-insecure-plaintext-allows-plain-text.md:56:This supersedes the `require_tls` default and the loopback allowance of ADR 0108, and the loopback allowance of ADR 0059. It also supersedes the line of ADR 0059 that has `mashctl init --local` write `insecure_loopback: true` into the configuration it makes: `init --local` now makes a local certificate authority and a server certificate and writes a `tls://` URL and `tls_ca`.
docs/decisions/INDEX.md:106:| [0108](0108-require-tls-makes-endpoint-connections-refuse-plain-text-to-a-remote-host.md) | MASH_REQUIRE_TLS makes endpoint connections refuse plain text to a remote host | superseded by ADR-0146 | 2026-11-12 |
docs/evidence/2026-11-17-0-5-5-live-runs/record-at005-bc96b27.md:110:- ADR 0108: with MASH_REQUIRE_TLS=1 an endpoint refuses to connect to a plain nats:// URL on a non-loopback address (refused before any connection; no plain server started)
docs/evidence/2026-11-17-0-5-5-live-runs/record-at005-bc96b27.md:209:- pass: ADR 0108: with MASH_REQUIRE_TLS=1 an endpoint refuses to connect to a plain nats:// URL on a non-loopback address (refused before any connection; no plain server started) (exit 1: mash: MASH_REQUIRE_TLS (require_tls) is set: refusing a plain nats:// connection to "10.89.119.11": use tls:// with the server's certificate (tls_ca for a private CA))
docs/evidence/2026-11-17-0-5-5-live-runs/record-at005-bc96b27.md:210:- pass: ADR 0108: mash status says tls: FAILED for it (tls: FAILED - mash: MASH_REQUIRE_TLS (require_tls) is set: refusing a plain nats:// connection to "10.89.119.11": use tls:// with the server's certificate (tls_ca for a p...)
docs/evidence/2026-11-17-0-5-5-live-runs/record-at005.md:95:- ADR 0108: with MASH_REQUIRE_TLS=1 an endpoint refuses to connect to a plain nats:// URL on a non-loopback address (refused before any connection; no plain server started)
docs/evidence/2026-11-17-0-5-5-live-runs/record-at005.md:179:- pass: ADR 0108: with MASH_REQUIRE_TLS=1 an endpoint refuses to connect to a plain nats:// URL on a non-loopback address (refused before any connection; no plain server started) (exit 1: mash: MASH_REQUIRE_TLS (require_tls) is set: refusing a plain nats:// connection to "10.89.195.11": use tls:// with the server's certificate (tls_ca for a private CA))
docs/evidence/2026-11-17-0-5-5-live-runs/record-at005.md:180:- pass: ADR 0108: mash status says tls: FAILED for it (tls: FAILED - mash: MASH_REQUIRE_TLS (require_tls) is set: refusing a plain nats:// connection to "10.89.195.11": use tls:// with the server's certificate (tls_ca for a p...)
docs/evidence/scrub/emission-paths.md:864:internal/ctl/local.go:116:		fmt.Fprintf(stdout, "    \"url\": %q, \"owner\": %q, \"admin_creds\": %q, \"insecure_loopback\": true\n", pl.URL, owner, p
docs/evidence/scrub/emission-paths.md:870:internal/ctl/local.go:147:		fmt.Fprintf(stdout, "    \"url\": %q, \"owner\": %q, \"admin_creds\": %q, \"insecure_loopback\": true\n", pl.URL, owner, p
docs/release-notes/0.5.5-draft.md:63:10. **Remove `insecure_loopback` and `require_tls` from your settings.** `insecure_loopback` (`MASH_INSECURE_LOOPBACK`) and
docs/release-notes/0.5.5-draft.md:64:    `require_tls` (`MASH_REQUIRE_TLS`) are gone and are not read: `insecure_plaintext` replaces both
docs/release-notes/0.5.5-draft.md:164:  `~/.config/mash` described above, and `insecure_loopback` and `require_tls` (`MASH_INSECURE_LOOPBACK`,
docs/release-notes/0.5.5-draft.md:165:  `MASH_REQUIRE_TLS`), which `insecure_plaintext` replaces.
internal/bootstrap/bootstrap_test.go:241:	if err != nil || !strings.Contains(string(cfg), `"tls://127.0.0.1:4222"`) || !strings.Contains(string(cfg), `"owner": "alice"`) || !strings.Contains(string(cfg), s.pl.Creds) || !strings.Contains(string(cfg), `"tls_ca": "`+filepath.Join(s.serverDir, "ca.pem")+`"`) || strings.Contains(string(cfg), "insecure_loopback") {
internal/config/config_test.go:709:	writeFile(t, home, `{"url":"tls://127.0.0.1:4222","owner":"o","insecure_loopback":true,"require_tls":true}`)
internal/config/config_test.go:710:	c, err := load(envOf(map[string]string{"MASH_NAME": "a", "MASH_GROUP": "g", "MASH_INSECURE_LOOPBACK": "1", "MASH_REQUIRE_TLS": "1"}), home)
internal/config/config_test.go:714:	if _, ok := c.Source["insecure_loopback"]; ok {
receipts/0.5.5-endpoint-model/final-gate-d25cdcaf4041-20261118T095140Z.md:97:**Concern to investigate — remote endpoint transport.** Endpoint TLS enforcement is off by default: [operations.md](docs/operations.md:19) says a remote plain NATS connection is allowed unless `require_tls` is set. The AT-005 refusal exercised the setting enabled. A deployment that omits it could expose endpoint credentials over the connection. This is an existing, documented limit, and [ADR 0059](pages/decisions/docs__decisions__0059-mashctl-checks-who-issued-the-account-jwt-carries-only-its-own-signing-keys-and-requires-tls-for-admin-connections.md) explicitly leaves endpoint connections unchanged. **Severity:** potentially high. **Certainty:** high about the default; deployment exposure depends on configuration. Confirm that remote setup instructions reliably enable TLS enforcement.
```

## Every remaining hit, searches 1 to 3

A hit that is not an asserted refusal, a REQ-009 message, an unrelated word, a plain valid token or the enforcement list would be removed; there is none. The two enforcement rows are not search hits: they are the forbidden-name list and its sample strings, listed as before.

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
| receipts/0.5.5-endpoint-model/final-gate-a191f08b7cdb-20261118T061551Z.md:117 | past record (gate review receipt) | a dated record of an earlier review of this work, kept as written; it is not code, a test or a doc a user reads |
| scripts/liveat/refusals.go:181 | asserted refusal (live acceptance run) | the harness runs `mashctl add` and asserts exit 2 with the hint `endpoint create` (REQ-009 message) |
| cmd/mash/retry_test.go:317 | asserted refusal in a test | a policy.md in the old ~/.config/mash holds the sentinel; the test asserts mash does NOT read it (the instructions never contain it) and reports the unreadable XDG file |
| cmd/mash/retry_test.go:344 | asserted refusal in a test | a policy.md in the old ~/.config/mash holds the sentinel; the test asserts mash does NOT read it (the instructions never contain it) and reports the unreadable XDG file |
| cmd/mash/retry_test.go:349 | asserted refusal in a test | a policy.md in the old ~/.config/mash holds the sentinel; the test asserts mash does NOT read it (the instructions never contain it) and reports the unreadable XDG file |
| cmd/mash/retry_test.go:365 | asserted refusal in a test | a policy.md in the old ~/.config/mash holds the sentinel; the test asserts mash does NOT read it (the instructions never contain it) and reports the unreadable XDG file |
| cmd/mashctl/audit_test.go:197 | unrelated word | comment on a JetStream consumer of the server state; no address involved |
| cmd/mashctl/inbox_inspect_test.go:43 | asserted refusal in a test | `--legacy` is an unknown option: parseInboxInspectArgs must refuse it |
| docs/audit.md:200 | unrelated word | the earlier-report fields table is about inboxes of older audit reports |
| docs/operations.md:249 | unrelated word | JetStream history rule: a bucket without the marker keeps the 8-revision limit |
| docs/operations.md:470 | unrelated word | `<spool>/.session` text `pid:N` kept so a hook of an older mash enforces the same rule; not an address or a command |
| internal/admin/issued.go:453 | unrelated word | JetStream history: the 8-revision rule of a bucket with no marker |
| internal/admin/issued.go:459 | unrelated word | JetStream history: the 8-revision rule of a bucket with no marker |
| internal/admin/issued.go:464 | unrelated word | JetStream history: the 8-revision rule of a bucket with no marker |
| internal/admin/issued.go:477 | unrelated word | JetStream history: the 8-revision rule of a bucket with no marker |
| internal/admin/issued_marker_test.go:192 | unrelated word | JetStream history: the 8-revision rule of a bucket with no marker |
| internal/apply/escape_test.go:12 | unrelated word | the string `legacy` is test data (a hostile role name; a role label), not an old form |
| internal/apply/escape_test.go:38 | unrelated word | the string `legacy` is test data (a hostile role name; a role label), not an old form |
| internal/apply/trust_test.go:175 | unrelated word | the string `legacy` is test data (a hostile role name; a role label), not an old form |
| internal/apply/trust_test.go:184 | unrelated word | the string `legacy` is test data (a hostile role name; a role label), not an old form |
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
| receipts/0.5.5-endpoint-model/final-gate-a191f08b7cdb-20261118T061551Z.md:26 | past record (gate review receipt) | a dated record of an earlier review of this work, kept as written; it is not code, a test or a doc a user reads |
| receipts/0.5.5-endpoint-model/final-gate-d25cdcaf4041-20261118T095140Z.md:30 | past record (gate review receipt) | a dated record of an earlier review of this work, kept as written; it is not code, a test or a doc a user reads |
| receipts/0.5.5-endpoint-model/final-gate-d25cdcaf4041-20261118T095140Z.md:169 | past record (gate review receipt) | a dated record of an earlier review of this work, kept as written; it is not code, a test or a doc a user reads |
| scripts/scrubcheck/exceptions.tsv:16 | unrelated word (the scrub list) | a scrub exception row; the word describes a removed directory field or a dated release note, not an address form |
| scripts/scrubcheck/exceptions.tsv:32 | unrelated word (the scrub list) | a scrub exception row; the word describes a removed directory field or a dated release note, not an address form |
| internal/portable/portable_test.go:36 | enforcement (the forbidden-name list) | `personalRE` lists the names of people, machines and private projects (among them `homerange` and `rangehand`, the layer above) that the portability scan refuses anywhere in source, docs and tests; it is how the contract's reject rule "any layer above mash is named in code, flags, subjects, keys, fields, errors or tests" is enforced. It is not the old form; it names words in order to refuse them |
| internal/portable/portable_test.go:482 | enforcement (its own sample strings) | the test strings that prove `personalRE` matches each listed name and does not match `bogworks` or ordinary words |

Since the earlier revision: `docs/operations.md:474` no longer carries the word (it is gone from search 2); `scripts/liveat/refusals.go` moved from line 180 to 181; the four `receipts/` rows are new (the gate review records).

## Every hit, search 4 (REQ-014 removed names)

Hits in tests that assert the old keys are ignored, in ADRs as history, in the changelog and release notes as removed-names text and in docs/evidence as past records are not defects; each has its reason. There is no hit in a non-test source file, a flag, a default, a template or a script.

| file:line | class | reason it is not live |
|---|---|---|
| CHANGELOG.md:23 | changelog (removed-names text) | dated release note of the release that introduced the name, or of its removal; history, as written |
| CHANGELOG.md:70 | changelog (removed-names text) | dated release note of the release that introduced the name, or of its removal; history, as written |
| CHANGELOG.md:229 | changelog (removed-names text) | dated release note of the release that introduced the name, or of its removal; history, as written |
| changelog.d/tls-everywhere.changed.md:1 | changelog fragment (removed-names text) | the entry says these settings are gone and not read |
| changelog.d/tls-required.changed.md:1 | changelog fragment (removed-names text) | the entry says these settings are gone and not read |
| contracts/0.5.5-endpoint-model.md:315 | contract text | the 0.5.5 contract names the removed settings in the requirement that removes them (REQ-014) |
| contracts/0.5.5-endpoint-model.md:319 | contract text | the 0.5.5 contract names the removed settings in the requirement that removes them (REQ-014) |
| docs/decisions/0059-mashctl-checks-who-issued-the-account-jwt-carries-only-its-own-signing-keys-and-requires-tls-for-admin-connections.md:37 | ADR history | a decision record, kept as written; 0108 is marked superseded by 0146, which records the removal |
| docs/decisions/0108-require-tls-makes-endpoint-connections-refuse-plain-text-to-a-remote-host.md:2 | ADR history | a decision record, kept as written; 0108 is marked superseded by 0146, which records the removal |
| docs/decisions/0108-require-tls-makes-endpoint-connections-refuse-plain-text-to-a-remote-host.md:7 | ADR history | a decision record, kept as written; 0108 is marked superseded by 0146, which records the removal |
| docs/decisions/0108-require-tls-makes-endpoint-connections-refuse-plain-text-to-a-remote-host.md:28 | ADR history | a decision record, kept as written; 0108 is marked superseded by 0146, which records the removal |
| docs/decisions/0108-require-tls-makes-endpoint-connections-refuse-plain-text-to-a-remote-host.md:34 | ADR history | a decision record, kept as written; 0108 is marked superseded by 0146, which records the removal |
| docs/decisions/0108-require-tls-makes-endpoint-connections-refuse-plain-text-to-a-remote-host.md:44 | ADR history | a decision record, kept as written; 0108 is marked superseded by 0146, which records the removal |
| docs/decisions/0108-require-tls-makes-endpoint-connections-refuse-plain-text-to-a-remote-host.md:56 | ADR history | a decision record, kept as written; 0108 is marked superseded by 0146, which records the removal |
| docs/decisions/0129-the-policy-commands-are-not-part-of-v0-5-0-and-the-engine-stays-internal-until-it-works-standalone.md:54 | ADR history | a decision record, kept as written; 0108 is marked superseded by 0146, which records the removal |
| docs/decisions/0146-every-mash-and-mashctl-connection-requires-tls-loopback-included-and-one-override-insecure-plaintext-allows-plain-text.md:13 | ADR history | a decision record, kept as written; 0108 is marked superseded by 0146, which records the removal |
| docs/decisions/0146-every-mash-and-mashctl-connection-requires-tls-loopback-included-and-one-override-insecure-plaintext-allows-plain-text.md:14 | ADR history | a decision record, kept as written; 0108 is marked superseded by 0146, which records the removal |
| docs/decisions/0146-every-mash-and-mashctl-connection-requires-tls-loopback-included-and-one-override-insecure-plaintext-allows-plain-text.md:32 | ADR history | a decision record, kept as written; 0108 is marked superseded by 0146, which records the removal |
| docs/decisions/0146-every-mash-and-mashctl-connection-requires-tls-loopback-included-and-one-override-insecure-plaintext-allows-plain-text.md:53 | ADR history | a decision record, kept as written; 0108 is marked superseded by 0146, which records the removal |
| docs/decisions/0146-every-mash-and-mashctl-connection-requires-tls-loopback-included-and-one-override-insecure-plaintext-allows-plain-text.md:56 | ADR history | a decision record, kept as written; 0108 is marked superseded by 0146, which records the removal |
| docs/decisions/INDEX.md:106 | ADR history | title of ADR 0108, now superseded by ADR 0146 |
| docs/evidence/2026-11-17-0-5-5-live-runs/record-at005-bc96b27.md:110 | past record | an earlier run record or scrub listing, kept as written for the revision it names; not current behavior |
| docs/evidence/2026-11-17-0-5-5-live-runs/record-at005-bc96b27.md:209 | past record | an earlier run record or scrub listing, kept as written for the revision it names; not current behavior |
| docs/evidence/2026-11-17-0-5-5-live-runs/record-at005-bc96b27.md:210 | past record | an earlier run record or scrub listing, kept as written for the revision it names; not current behavior |
| docs/evidence/2026-11-17-0-5-5-live-runs/record-at005.md:95 | past record | an earlier run record or scrub listing, kept as written for the revision it names; not current behavior |
| docs/evidence/2026-11-17-0-5-5-live-runs/record-at005.md:179 | past record | an earlier run record or scrub listing, kept as written for the revision it names; not current behavior |
| docs/evidence/2026-11-17-0-5-5-live-runs/record-at005.md:180 | past record | an earlier run record or scrub listing, kept as written for the revision it names; not current behavior |
| docs/evidence/scrub/emission-paths.md:864 | past record | an earlier run record or scrub listing, kept as written for the revision it names; not current behavior |
| docs/evidence/scrub/emission-paths.md:870 | past record | an earlier run record or scrub listing, kept as written for the revision it names; not current behavior |
| docs/release-notes/0.5.5-draft.md:63 | release notes (removed-names text) | step 10 and the changed list tell the reader to remove these settings; they are not read |
| docs/release-notes/0.5.5-draft.md:64 | release notes (removed-names text) | step 10 and the changed list tell the reader to remove these settings; they are not read |
| docs/release-notes/0.5.5-draft.md:164 | release notes (removed-names text) | step 10 and the changed list tell the reader to remove these settings; they are not read |
| docs/release-notes/0.5.5-draft.md:165 | release notes (removed-names text) | step 10 and the changed list tell the reader to remove these settings; they are not read |
| internal/bootstrap/bootstrap_test.go:241 | test asserting the old key is absent | asserts the config that init --local writes has no insecure_loopback key (REQ-014) |
| internal/config/config_test.go:709 | test asserting the old keys are ignored | the test writes both keys and both variables and asserts the loaded config does not carry or report them as a source (ignored) |
| internal/config/config_test.go:710 | test asserting the old keys are ignored | the test writes both keys and both variables and asserts the loaded config does not carry or report them as a source (ignored) |
| internal/config/config_test.go:714 | test asserting the old keys are ignored | the test writes both keys and both variables and asserts the loaded config does not carry or report them as a source (ignored) |
| receipts/0.5.5-endpoint-model/final-gate-d25cdcaf4041-20261118T095140Z.md:97 | past record (gate review receipt) | a dated record of an earlier review of this work, kept as written; it is not code, a test or a doc a user reads |
