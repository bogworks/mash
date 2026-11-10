# Scrub audit inventory

Every hit of `go run ./scripts/scrubcheck` on main at 6dff55f (2026-11-09), one row per hit, as the tool printed it
(`file:line:word`). The verdict column is one of `open`, `removed`, `renamed` or `excepted` (with the commit that did
it). The scrub itself comes in later pull requests; this one only starts the inventory, so every verdict is `open`. The
scrub is done when no row is `open`. This file is inside the tool's own directory, which the tool skips.

Tracked files only: the release archives and the plugin package are scanned through `-archive` once a build exists, and
any hit from there is added here. The last 8 rows are hits that the scrub plan
(`docs/plans/2026-11-10-scrub-remainder.md`) itself adds by naming the words. Regenerate the raw list with `go run ./scripts/scrubcheck`.

| Hit | Line text | Verdict |
|---|---|---|
| `CHANGELOG.md:146:herd` | `- The '--herd' directory field and the legacy herd read. Use '--label key=value' instead.` | open |
| `CHANGELOG.md:146:herd` | `- The '--herd' directory field and the legacy herd read. Use '--label key=value' instead.` | open |
| `cmd/mash/standalone_serve_test.go:17:herdr` | `// environment of standalone_test.go (empty PATH and HOME, no RANGE_/HERDR_/CLAUDE variables) against the embe` | renamed (comment reworded) |
| `cmd/mash/standalone_test.go:19:herdr` | `// Mash must be fully usable with only NATS, its SAP and its runtime dependencies: no Claude, herdr or other a` | renamed (comment reworded) |
| `cmd/mash/standalone_test.go:22:herdr` | `var foreignTools = []string{"range", "herdr", "claude"}` | excepted (exceptions.tsv) |
| `cmd/mash/standalone_test.go:63:herdr` | `for _, bad := range []string{"RANGE_", "HERDR_", "CLAUDE"} {` | excepted (exceptions.tsv) |
| `cmd/mash/standalone_test.go:161:herd` | `// TestStandaloneEnvOnlyConfig runs with no config.json and no herd config: the environment alone configures m` | renamed (comment reworded) |
| `cmd/mash/standalone_test.go:196:herdr` | `// imports herdr or claude.` | renamed (comment reworded) |
| `cmd/mash/standalone_test.go:269:herdr` | `return strings.Contains(s, "herdr")` | excepted (exceptions.tsv) |
| `docs/evidence/2026-10-31-init-synadia-live/RECEIPT.md:13:lane` | `\| 4 capacity \| read from the plan line of step 2 \| Room: 4 of 5 streams planned, "Room for 1 more endpoint(` | open |
| `docs/evidence/2026-10-31-init-synadia-live/RECEIPT.md:30:lane` | `plan, not real usage. The real run's capacity check passed with 4 of 5, so the account held none of the earlie` | open |
| `docs/evidence/2026-11-02-heartbeat-fanout/README.md:97:lanes` | `- Client and servers share a 16-core shared host (other lanes' nats-servers were running and were not touched)` | open |
| `docs/plans/2026-11-03-next-tranche.md:18:coordinator` | `Order is the order to take them. Owners are suggestions; the coordinator assigns.` | open |
| `docs/plans/2026-11-03-next-tranche.md:48:coordinator` | `* A release dry run on 'main' with a real Fulcio certificate exercises the generator-commit check (#163): the ` | open |
| `docs/security/threat-model.md:229:coordinator` | `6. Should one owner support trust tiers (for example a "coordinator" endpoint whose mail delegates and "worker` | renamed (trusted/ordinary) |
| `internal/endpoint/waking_test.go:20:lane` | `// wakingFixture is a hooked lane "a" (mail is held in its spool until drained) with a sender "b" and a reader` | renamed (endpoint) |
| `internal/portable/portable_test.go:36:homerange` | `personalRE = regexp.MustCompile('(?i)randyb\|randybias\|rlb\|homerange\|rangehand\|plethorax\|dev1\|thao` | open |
| `internal/portable/portable_test.go:36:rangehand` | `personalRE = regexp.MustCompile('(?i)randyb\|randybias\|rlb\|homerange\|rangehand\|plethorax\|dev1\|thao` | open |
| `internal/portable/portable_test.go:482:homerange` | `for _, s := range []string{"randyb", "RandyBias", "my rlb box", "homerange", "rangehand-x", "plethorax", "dev1` | open |
| `internal/portable/portable_test.go:482:rangehand` | `for _, s := range []string{"randyb", "RandyBias", "my rlb box", "homerange", "rangehand-x", "plethorax", "dev1` | open |
| `internal/store/store_test.go:46:herd` | `r, err := ParseRecord("a", []byte('{'+baseRecord+',"herd":"acme","role":"r"}'))` | renamed (grouping) |
| `scripts/adrlint/main.go:87:herds` | `vocabRE     = regexp.MustCompile('(?i)(herds?\|herdr\|lanes?\|lead\|orchestrator)')` | open |
| `scripts/adrlint/main.go:87:herdr` | `vocabRE     = regexp.MustCompile('(?i)(herds?\|herdr\|lanes?\|lead\|orchestrator)')` | open |
| `scripts/adrlint/main.go:87:lanes` | `vocabRE     = regexp.MustCompile('(?i)(herds?\|herdr\|lanes?\|lead\|orchestrator)')` | open |
| `scripts/adrlint/main_test.go:158:lane` | `{"d: vocabulary", with(good(), adr, edit(goodADR, "A driver.", "A Lane and the lead, herd, herds, herdr, orche` | open |
| `scripts/adrlint/main_test.go:158:herd` | `{"d: vocabulary", with(good(), adr, edit(goodADR, "A driver.", "A Lane and the lead, herd, herds, herdr, orche` | open |
| `scripts/adrlint/main_test.go:158:herds` | `{"d: vocabulary", with(good(), adr, edit(goodADR, "A driver.", "A Lane and the lead, herd, herds, herdr, orche` | open |
| `scripts/adrlint/main_test.go:158:herdr` | `{"d: vocabulary", with(good(), adr, edit(goodADR, "A driver.", "A Lane and the lead, herd, herds, herdr, orche` | open |
| `scripts/adrlint/main_test.go:158:lane` | `{"d: vocabulary", with(good(), adr, edit(goodADR, "A driver.", "A Lane and the lead, herd, herds, herdr, orche` | open |
| `scripts/adrlint/main_test.go:158:herds` | `{"d: vocabulary", with(good(), adr, edit(goodADR, "A driver.", "A Lane and the lead, herd, herds, herdr, orche` | open |
| `scripts/adrlint/main_test.go:158:herdr` | `{"d: vocabulary", with(good(), adr, edit(goodADR, "A driver.", "A Lane and the lead, herd, herds, herdr, orche` | open |
| `scripts/adrlint/main_test.go:159:lane` | `{"d: escape on the same line", with(good(), adr, edit(goodADR, "A driver.", "A lane. <!-- adrlint:allow vocabu` | open |
| `scripts/adrlint/main_test.go:160:lane` | `{"d: escape on another line does not count", with(good(), adr, edit(goodADR, "A driver.", "<!-- adrlint:allow ` | open |
| `scripts/adrlint/main_test.go:160:lane` | `{"d: escape on another line does not count", with(good(), adr, edit(goodADR, "A driver.", "<!-- adrlint:allow ` | open |
| `scripts/adrlint/main_test.go:161:lanes` | `{"d: whole word only", with(good(), adr, edit(goodADR, "A driver.", "leading planes lanes_x")), nil},` | open |
| `scripts/adrlint/main_test.go:186:lane` | `dir := tree(t, with(good(), "0001-use-one-way.md", edit(edit(goodADR, "status: accepted", "status: nope"), "A ` | open |
| `scripts/adrlint/main_test.go:469:lane` | `{"built-in rule still applies", "zebra", "A lane.", true},` | open |
| `docs/plans/2026-11-10-scrub-remainder.md:3:herd` | `Date: 2026-11-10. Scope: the work contract 'mash-shed-herd-idioms' (draft-4, ratified 2026-11-09) after its fi` | open (this plan names the words to plan their removal; see step 6) |
| `docs/plans/2026-11-10-scrub-remainder.md:23:coordinator` | `\| G1 shipped prose \| 'docs/security/threat-model.md' \| 1 \| the word "coordinator" in an open question; shi` | open (this plan names the words to plan their removal; see step 6) |
| `docs/plans/2026-11-10-scrub-remainder.md:29:rangehand` | `The scan finds no 'rangehand' or 'compat.json' in release scripts, their tests, workflows or 'docs/release.md'` | open (this plan names the words to plan their removal; see step 6) |
| `docs/plans/2026-11-10-scrub-remainder.md:56:herd` | `"owner" where it says a herd appears in mash as a zone, and REQ-009 covers translating an outside identifier i` | open (this plan names the words to plan their removal; see step 6) |
| `docs/plans/2026-11-10-scrub-remainder.md:75:homerange` | `- 'internal/portable/portable_test.go': its personal-name regexp must name 'homerange' and 'rangehand' to dete` | open (this plan names the words to plan their removal; see step 6) |
| `docs/plans/2026-11-10-scrub-remainder.md:75:rangehand` | `- 'internal/portable/portable_test.go': its personal-name regexp must name 'homerange' and 'rangehand' to dete` | open (this plan names the words to plan their removal; see step 6) |
| `docs/plans/2026-11-10-scrub-remainder.md:92:rangehand` | `'rangehand' and 'compat.json' over the release scripts, their tests, workflows and 'docs/release.md', and a re` | open (this plan names the words to plan their removal; see step 6) |
| `docs/plans/2026-11-10-scrub-remainder.md:95:herd` | `herd; the requirement-to-evidence map; the review records (pre-merge review and adversarial review on another ` | open (this plan names the words to plan their removal; see step 6) |
