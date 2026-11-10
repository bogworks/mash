# Scrub audit inventory

Every hit of `go run ./scripts/scrubcheck` on main at 6dff55f (2026-11-09), one row per hit, as the tool found it. A row
is `file:line:Wn`: the file, the line, and Wn, the 1-based position in `words.txt` of the scrub-list word that hit (a
plural counts as its base word). Neither the words nor the lines they sit on are quoted here, so this file holds no
scrub-list word itself and the tool scans it like any other tracked file. The verdict column is one of `open`,
`removed`, `renamed` or `excepted` (with the commit that did it). The scrub is done when no row is `open`.

Tracked files only: the release archives and the plugin package are scanned through `-archive` once a build exists, and
any hit from there is added here. The rows for `docs/plans/` are hits that the scrub plan
(`docs/plans/2026-11-10-scrub-remainder.md`) itself adds by naming the words. Regenerate the raw list with
`go run ./scripts/scrubcheck`.

| Hit | Verdict |
|---|---|
| CHANGELOG.md:146:W1 | open |
| CHANGELOG.md:146:W1 | open |
| cmd/mash/standalone_test.go:22:W6 | excepted (exceptions.tsv) |
| cmd/mash/standalone_test.go:63:W6 | excepted (exceptions.tsv) |
| cmd/mash/standalone_test.go:269:W6 | excepted (exceptions.tsv) |
| docs/evidence/2026-10-31-init-synadia-live/RECEIPT.md:13:W2 | open |
| docs/evidence/2026-10-31-init-synadia-live/RECEIPT.md:30:W2 | open |
| docs/evidence/2026-11-02-heartbeat-fanout/README.md:97:W2 | open |
| docs/plans/2026-11-03-next-tranche.md:18:W3 | open |
| docs/plans/2026-11-03-next-tranche.md:48:W3 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:5:W5 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:5:W2 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:18:W1 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:33:W2 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:34:W5 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:93:W2 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:93:W1 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:100:W2 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:100:W1 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:117:W1 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:117:W2 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:120:W5 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:121:W1 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:128:W5 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:168:W5 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:171:W5 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:172:W5 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:181:W5 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:203:W1 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:204:W2 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:211:W5 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:212:W1 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:212:W2 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:215:W1 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:215:W1 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:216:W1 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:216:W2 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:217:W5 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:224:W5 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:225:W1 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:252:W1 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:253:W2 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:263:W2 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:284:W2 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:295:W1 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:295:W1 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:296:W5 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:297:W2 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:297:W1 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:298:W2 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:298:W1 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:298:W1 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:305:W5 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:306:W5 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:320:W1 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:320:W2 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:322:W2 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:322:W1 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:333:W2 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:340:W5 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:341:W5 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:342:W5 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:354:W1 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:386:W1 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:386:W2 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:392:W2 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:392:W1 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:393:W5 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:407:W1 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:421:W2 | open |
| docs/plans/2026-11-10-naming-grammar-in-mash.md:422:W2 | open |
| docs/plans/2026-11-10-scrub-remainder.md:3:W1 | open (this plan names the words to plan their removal; see step 6) |
| docs/plans/2026-11-10-scrub-remainder.md:64:W3 | open |
| docs/plans/2026-11-10-scrub-remainder.md:70:W4 | open |
| docs/plans/2026-11-10-scrub-remainder.md:97:W1 | open |
| docs/plans/2026-11-10-scrub-remainder.md:116:W5 | open |
| docs/plans/2026-11-10-scrub-remainder.md:116:W4 | open |
| docs/plans/2026-11-10-scrub-remainder.md:133:W4 | open |
| docs/plans/2026-11-10-scrub-remainder.md:136:W1 | open |
| internal/portable/portable_test.go:36:W5 | open |
| internal/portable/portable_test.go:36:W4 | open |
| internal/portable/portable_test.go:482:W5 | open |
| internal/portable/portable_test.go:482:W4 | open |
| internal/store/store_test.go:46:W1 | excepted (exceptions.tsv) |
| scripts/adrlint/main.go:87:W1 | open |
| scripts/adrlint/main.go:87:W6 | open |
| scripts/adrlint/main.go:87:W2 | open |
| scripts/adrlint/main_test.go:158:W2 | open |
| scripts/adrlint/main_test.go:158:W1 | open |
| scripts/adrlint/main_test.go:158:W1 | open |
| scripts/adrlint/main_test.go:158:W6 | open |
| scripts/adrlint/main_test.go:158:W2 | open |
| scripts/adrlint/main_test.go:158:W1 | open |
| scripts/adrlint/main_test.go:158:W6 | open |
| scripts/adrlint/main_test.go:159:W2 | open |
| scripts/adrlint/main_test.go:160:W2 | open |
| scripts/adrlint/main_test.go:160:W2 | open |
| scripts/adrlint/main_test.go:161:W2 | open |
| scripts/adrlint/main_test.go:186:W2 | open |
| scripts/adrlint/main_test.go:469:W2 | open |
