#!/usr/bin/env bash
# scripts/gate --review (ADR 0078) against a throwaway origin and clone, a stub ocr on PATH and the real reviewgate.
# Go and git are real; nothing here calls a model or the network.
#   bash scripts/gate_review_test.sh
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/.." && pwd)
T=$(mktemp -d "${TMPDIR:-/tmp}/gate-review-test.XXXXXX"); trap 'rm -rf "$T"' EXIT
export GATE_REVIEWGATE=$T/reviewgate GATE_REVIEW_STORE=$T/store GATE_SLOT_DIR=$T/slots GATE_FINDINGS_LOG=$T/findings.ndjson
export XDG_STATE_HOME=$T/xdg-state XDG_DATA_HOME=$T/xdg-data TMPDIR=$T/tmp
# Go keeps its caches under HOME: point them at the real ones before HOME moves, so the build stays offline and fast.
export GOCACHE=${GOCACHE:-$(go env GOCACHE)} GOMODCACHE=${GOMODCACHE:-$(go env GOMODCACHE)}
export HOME=$T/home
mkdir -p "$HOME" "$TMPDIR" "$T/bin"
(cd "$repo" && go build -o "$GATE_REVIEWGATE" ./scripts/reviewgate)

# The stub ocr: the canned result is chosen by the model name; every call is logged.
cat >"$T/bin/ocr" <<'STUB'
#!/usr/bin/env bash
prov= model= o= repo= from= to= preview=0
while [ $# -gt 0 ]; do
	case $1 in
	--repo) repo=$2; shift 2 ;;
	--from) from=$2; shift 2 ;;
	--to) to=$2; shift 2 ;;
	--preview) preview=1; shift ;;
	--provider) prov=$2; shift 2 ;;
	--model) model=$2; shift 2 ;;
	--rule) echo "$2" >>"$STUB_RULES"; shift 2 ;;
	-b) echo "$2" >>"$STUB_BG"; shift 2 ;;
	-o) o=$2; shift 2 ;;
	*) shift ;;
	esac
done
if [ "$preview" = 1 ]; then # like ocr: Markdown and *_test.go files are excluded from review
	echo preview >>"$STUB_PREVIEWS"
	[ -z "${STUB_PREVIEW_FAIL:-}" ] || { echo "ocr: simulated preview failure" >&2; exit 1; }
	[ -z "${STUB_PREVIEW_JUNK:-}" ] || { echo "no header here"; exit 0; }
	files=$(git -C "$repo" diff --name-only "$from" "$to")
	will=$(grep -vE '\.md$|_test\.go$' <<<"$files" || true)
	echo "Preview: $(wc -l <<<"$files" | tr -d ' ') file(s) changed  |  +9  -1"
	echo
	echo "Will review ($(grep -c . <<<"$will" || true)):"
	[ -z "${STUB_PREVIEW_NONAMES:-}" ] || exit 0
	[ -z "$will" ] || sed 's/^/  [A]  /; s/$/    +9    -1   /' <<<"$will" # the real format carries the line stats after the path
	exit 0
fi
echo "ocr provider=$prov model=$model" >>"$STUB_CALLS"
c=
case ${model:-clean} in
broken) echo "ocr: simulated failure" >&2; exit 1 ;;
bad) c='{"path":"main.go","content":"a real defect","start_line":3,"end_line":3,"category":"bug","severity":"high"}' ;;
blocker) c='{"path":"main.go","content":"a blocking defect","start_line":3,"end_line":3,"category":"bug","severity":"blocker"}' ;;
esac
printf '{"status":"complete","llm":{"provider":"%s","model":"%s"},"manifest":{"input":{"exact_range":"%s..%s","source_artifact_sha256":"feed"}},"comments":[%s]}\n' "${prov:-stub}" "${model:-clean}" "$from" "$to" "$c" >"$o"
STUB
chmod +x "$T/bin/ocr"
export PATH=$T/bin:$PATH STUB_CALLS=$T/calls STUB_PREVIEWS=$T/previews STUB_RULES=$T/rules STUB_BG=$T/bg

git init -q --bare "$T/origin.git"
git clone -q "$T/origin.git" "$T/clone" 2>/dev/null
cd "$T/clone"
git config user.email t@example.com; git config user.name t
git checkout -q -b main
goversion=$(go env GOVERSION | sed 's/^go//; s/^\([0-9]*\.[0-9]*\).*/\1/')
printf 'module example.com/t\n\ngo %s\n' "$goversion" >go.mod
printf 'package main\n\nfunc main() {\n\tprintln("hi")\n}\n' >main.go
git add -A; git commit -qm base; git push -q origin main
mkdir -p docs/review && cp "$repo/docs/review/policy.json" docs/review/policy.json # untracked: the author's checkout policy
printf 'docs/review/\n' >.git/info/exclude

fail=0
ok() { echo "ok   $1"; }
bad() { echo "FAIL $1"; fail=1; }
expect() { if grep -q -- "$2" <<<"$3"; then ok "$1"; else bad "$1: want /$2/ in: $3"; fi; }
refute() { if grep -q -- "$2" <<<"$3"; then bad "$1: unexpected /$2/ in: $3"; else ok "$1"; fi; }
eq() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1: want '$2' got '$3'"; fi; }
gate() { # args...; sets rc and out
	rc=0; out=$(bash "$here/gate" "$@" 2>&1) || rc=$?
}
branch() { # name, file, content: a fresh branch off main adding one file
	git checkout -q main; git checkout -q -B "$1"
	mkdir -p "$(dirname "$2")"; printf '%s\n' "$3" >"$2"; git add -A; git commit -qm "add $2"
}
amend() { printf '%s\n' "$2" >"$1"; git add -A; git commit -q --amend -m "amended $1"; }
roundsfile() { find "$GATE_ROUNDS_DIR" -name "$1.rounds" 2>/dev/null | head -1; }
calls() { if [ -f "$STUB_CALLS" ]; then wc -l <"$STUB_CALLS" | tr -d ' '; else echo 0; fi; }
newrun() { rm -rf "$T/rounds" "$T/store" "$T/findings.ndjson" "$STUB_CALLS" "$STUB_PREVIEWS" "$STUB_RULES" "$STUB_BG"; export GATE_ROUNDS_DIR=$T/rounds; }
pidof() { git diff main "$1" | git patch-id --verbatim | cut -d' ' -f1; }
record() { find "$T/store" -type f -name "$1" | head -1; }

# 1. a clean model passes and records the patch-id
newrun; branch b1 extra.go $'package main\n\nfunc extra() {}'
gate b1 --review --model stub:clean
eq "1 clean: exit 0" 0 "$rc"; expect "1 clean: REVIEW PASSED" "REVIEW PASSED: .* patch-id" "$out"
pid=$(pidof b1)
if [ -f "$(record "$pid")" ]; then ok "1 clean: record file for the patch-id"; else bad "1 record file for $pid"; fi
if grep -q '^verdict: pass' "$(record "$pid")"; then ok "1 record says pass"; else bad "1 record verdict"; fi

# 2. clean + bad blocks, names the finding and the models count; no record
newrun; branch b2 extra.go $'package main\n\nfunc extra() {}'
gate b2 --review --model stub:clean --model stub:bad
eq "2 blocked: exit 1" 1 "$rc"; expect "2 lists H1" "^H1 high main.go:3" "$out"; expect "2 models count" "(1 of 2 models: stub/bad)" "$out"
pid=$(pidof b2)
if grep -q "^$pid " "$(roundsfile b2)"; then ok "2 rounds file has the patch-id"; else bad "2 rounds file"; fi
if [ -z "$(find "$T/store" -type f 2>/dev/null)" ]; then ok "2 no record"; else bad "2 unexpected record"; fi

# 3. the same head again keeps its round; an amended head advances it
gate b2 --review --model stub:bad
eq "3 same head: exit 1" 1 "$rc"; expect "3 same head: still round 1" "round 1 of 4" "$out"
amend extra.go $'package main\n\nfunc extra() { _ = 1 }'
gate b2 --review --model stub:bad
eq "3 amended head: exit 1" 1 "$rc"; expect "3 amended head: round 2" "round 2 of 4" "$out"

# 4. the cap (standard tier cap 2 here, so the second head hits it): exit 3 and a cap record; a blocker at the cap is exit 1
newrun; sed 's/"round_cap": 4/"round_cap": 2/' "$repo/docs/review/policy.json" >docs/review/policy.json
branch b4 extra.go $'package main\n\nfunc extra() {}'
gate b4 --review --model stub:bad; eq "4 round 1: exit 1" 1 "$rc"
amend extra.go $'package main\n\nfunc extra() { _ = 2 }'
gate b4 --review --model stub:bad; eq "4 round 2 at the cap: exit 3" 3 "$rc"; expect "4 ROUND CAP printed" "ROUND CAP" "$out"
pid=$(pidof b4)
if grep -q '^verdict: cap' "$(record "$pid")"; then ok "4 cap record"; else bad "4 cap record"; fi
amend extra.go $'package main\n\nfunc extra() { _ = 3 }'
gate b4 --review --model stub:blocker; eq "4 blocker at the cap: exit 1" 1 "$rc"
cp "$repo/docs/review/policy.json" docs/review/policy.json

# 5. a failed run among clean ones passes naming it; all failed is exit 2
newrun; branch b5 extra.go $'package main\n\nfunc extra() {}'
gate b5 --review --model stub:broken --model stub:clean
eq "5 one broken: exit 0" 0 "$rc"; expect "5 names the failed run" "models: ocr-0.json FAILED" "$out"
gate b5 --review --model stub:broken --model stub:broken
eq "5 all broken: exit 2" 2 "$rc"; expect "5 fails closed" "failing closed" "$out"

# 6. gofmt-bad Go file: conformance fails before any ocr run
newrun; branch b6 ugly.go $'package main\nfunc   ugly( ) {}'
gate b6 --review --model stub:clean
eq "6 gofmt: exit 1" 1 "$rc"; expect "6 FAIL gofmt" "FAIL gofmt" "$out"; eq "6 no ocr call" 0 "$(calls)"

# 7. a test file plus a non-excluded docs file: Go build/vet still run (a .go file changed), tests-and-docs tier, a high finding passes
newrun; branch b7 docs/data.json '{"a":1}'
printf 'package main\n\nfunc helperX() {}\n' >x_test.go; git add -A; git commit -qm "add test"
gate b7 --review --model stub:bad
eq "7 tests-and-docs: exit 0" 0 "$rc"; expect "7 tests-and-docs tier" "tier: tests-and-docs" "$out"
expect "7 covered 1 of 2" "covered 1 of 2 files" "$out"; expect "7 server covers the rest" "the server-side review covers the other 1 files" "$out"
pid=$(pidof b7); rec=$(record "$pid")
if grep -q '^covered: 1$' "$rec" && grep -q '^total: 2$' "$rec"; then ok "7 record has covered and total"; else bad "7 record coverage: $(cat "$rec")"; fi
if [ "$(grep -c '^covered-file: ' "$rec")" = 1 ] && ! grep -q 'covered-file: .*_test.go' "$rec"; then ok "7 record names the one covered file, not the test"; else bad "7 record covered-file lines: $(cat "$rec")"; fi

# 7b. a docs-only Go-free diff: Markdown is excluded from review, so nothing is covered
newrun; branch b7b docs/note.md $'# note'
gate b7b --review --model stub:bad
eq "7b docs only: exit 0" 0 "$rc"; expect "7b REVIEW SKIPPED" "REVIEW SKIPPED: OCR covers none of the 1 changed files" "$out"
refute "7b no pass line" "REVIEW PASSED" "$out"
eq "7b no model run" 0 "$(calls)"; eq "7b preview asked once" 1 "$(wc -l <"$STUB_PREVIEWS" | tr -d ' ')"
if [ -z "$(find "$T/store" -type f 2>/dev/null)" ]; then ok "7b no record"; else bad "7b unexpected record"; fi

# 7c. a test-only diff is skipped too
newrun; branch b7c y_test.go $'package main\n\nfunc helperY() {}'
gate b7c --review --model stub:clean
eq "7c test only: exit 0" 0 "$rc"; expect "7c REVIEW SKIPPED" "REVIEW SKIPPED" "$out"; eq "7c no model run" 0 "$(calls)"

# 10. a normal diff: the record carries coverage and its evidence, the evidence is a copy of each run
newrun; branch b10 extra.go $'package main\n\nfunc extra() {}'
gate b10 --review --model stub:clean --model stub:broken --model stub:clean2
eq "10 exit 0" 0 "$rc"; expect "10 covered 1 of 1" "covered 1 of 1 files" "$out"; refute "10 nothing left to the server" "covers the other" "$out"
pid=$(pidof b10); rec=$(record "$pid")
for l in '^covered: 1$' '^total: 1$' '^covered-file: extra.go$' '^evidence: 2$' '^run-1: [0-9a-f]\{64\} .*\.\..* feed$' '^run-2: [0-9a-f]\{64\} '; do
	if grep -q "$l" "$rec"; then ok "10 record has $l"; else bad "10 record lacks $l: $(cat "$rec")"; fi
done
if [ -f "$rec.evidence-1.json" ] && [ -f "$rec.evidence-2.json" ] && [ ! -e "$rec.evidence-3.json" ]; then ok "10 two evidence files (the failed run has none)"; else bad "10 evidence files"; fi

# 11. an unreadable preview fails closed, before any model runs
newrun; branch b11 extra.go $'package main\n\nfunc extra() {}'
STUB_PREVIEW_FAIL=1 gate b11 --review --model stub:clean
eq "11 preview fails: exit 2" 2 "$rc"; expect "11 says why" "failing closed" "$out"; eq "11 no model run" 0 "$(calls)"
STUB_PREVIEW_JUNK=1 gate b11 --review --model stub:clean
eq "11 headerless preview: exit 2" 2 "$rc"; expect "11 says why" "no 'Preview" "$out"
if [ -z "$(find "$T/store" -type f 2>/dev/null)" ]; then ok "11 no record"; else bad "11 unexpected record"; fi

# 11b. a preview that counts files but lists no names fails closed too
newrun; branch b11b extra.go $'package main\n\nfunc extra() {}'
STUB_PREVIEW_NONAMES=1 gate b11b --review --model stub:clean
eq "11b names missing: exit 2" 2 "$rc"; expect "11b says why" "lists 0 file name" "$out"; eq "11b no model run" 0 "$(calls)"

# 8. --model without --review is a usage error
gate b1 --model stub:clean; eq "8 --model alone: exit 2" 2 "$rc"; expect "8 says why" "only valid with --review" "$out"
# 9. the old flag parser still rejects unknown flags
gate b1 --bogus; eq "9 unknown flag: exit 2" 2 "$rc"; expect "9 unknown flag text" "unknown flag --bogus" "$out"
# 10. a base policy with questions: the question rides in ocr's background (never --rule, which would drop its built-in
# rules); none: no question
newrun; git checkout -q main; git checkout -q -B qbase
mkdir -p docs/review; cp "$repo/docs/review/policy.json" docs/review/policy.json
git add -f docs/review/policy.json; git commit -qm "policy with questions"
git checkout -q -B b10; printf 'package main\n\nfunc extra() {}\n' >extra.go; git add extra.go; git commit -qm extra
gate b10 --review --base qbase --model stub:clean --model stub:clean
eq "10 with questions: exit 0" 0 "$rc"
eq "10 ocr is never given --rule" 0 "$([ -f "$STUB_RULES" ] && wc -l <"$STUB_RULES" | tr -d ' ' || echo 0)"
eq "10 the question is in the background of each of the 2 runs" 2 "$(grep -c 'Also answer: Does this change hard-code an environment assumption.* A yes is a high finding' "$STUB_BG" || true)"
newrun; gate b1 --review --model stub:clean # the base (origin/main) has no policy.json
eq "10 no base policy: exit 0" 0 "$rc"; eq "10 no base policy: no question in the background" 0 "$(grep -c "Also answer" "$STUB_BG" 2>/dev/null || true)"
# 12. the findings log: one line per comment of each complete run, model recorded, confirmed null; a failed write never fails the gate
newrun; branch b12 extra.go $'package main\n\nfunc extra() {}'
mkdir -p docs/review && cp "$repo/docs/review/policy.json" docs/review/policy.json # the author's checkout policy (earlier cases moved branches)
gate b12 --review --model stub:bad --model stub:clean --model stub:broken
eq "12 blocked: exit 1" 1 "$rc"
eq "12 one line per comment (clean and broken add none)" 1 "$(wc -l <"$GATE_FINDINGS_LOG" | tr -d ' ')"
eq "12 model recorded" "stub:bad" "$(jq -r .model "$GATE_FINDINGS_LOG")"
eq "12 confirmed is null" null "$(jq -c .confirmed "$GATE_FINDINGS_LOG")"
eq "12 fields" "main.go 3 high bug a real defect inloop 1 standard" "$(jq -r '[.file,.line,.severity,.category,.title,.source,.round,.tier]|join(" ")' "$GATE_FINDINGS_LOG")"
eq "12 ref and patch-id" "b12 $(pidof b12)" "$(jq -r '[.ref,.patchid]|join(" ")' "$GATE_FINDINGS_LOG")"
newrun; gate b12 --review --model stub:bad --model stub:blocker
eq "12 two runs: two lines" 2 "$(wc -l <"$GATE_FINDINGS_LOG" | tr -d ' ')"
newrun; gate b1 --review --model stub:clean
eq "12 clean run: exit 0, no lines" 0 "$rc"; eq "12 clean: log empty or absent" 0 "$([ -f "$GATE_FINDINGS_LOG" ] && wc -l <"$GATE_FINDINGS_LOG" | tr -d ' ' || echo 0)"
newrun; mkdir -p "$T/ro"; : >"$T/ro/file"
GATE_FINDINGS_LOG=$T/ro/file/sub/findings.ndjson gate b12 --review --model stub:bad
eq "12 unwritable log: gate still exits 1" 1 "$rc"; expect "12 unwritable log: verdict still printed" "^H1 high main.go:3" "$out"
newrun; GATE_FINDINGS_LOG=$T/ro/file/sub/findings.ndjson gate b1 --review --model stub:clean
eq "12 unwritable log: pass still exits 0" 0 "$rc"

if [ "$fail" = 0 ]; then echo "all passed"; else echo "FAILURES"; exit 1; fi
