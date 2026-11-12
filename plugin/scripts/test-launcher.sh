#!/bin/sh
# Tests for plugin/scripts/mash and pin-plugin. Plain sh; needs only coreutils-ish tools.
HERE=$(cd "$(dirname "$0")" && pwd)
T=$(mktemp -d "${TMPDIR:-/tmp}/mash-launcher-test.XXXXXX") || exit 1
trap 'rm -rf "$T"' EXIT
PASS=0
FAIL=0

ok()   { PASS=$((PASS + 1)); echo "ok   - $1"; }
fail() { FAIL=$((FAIL + 1)); echo "FAIL - $1"; }
check() { # desc, then a test command
  _d=$1; shift
  if "$@"; then ok "$_d"; else fail "$_d"; fi
}
contains() { case "$1" in *"$2"*) return 0 ;; esac; return 1; }
absent() { [ ! -e "$1" ]; }
empty_file() { [ ! -s "$1" ]; }
eq() { [ "$1" = "$2" ]; }

sha() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi; }

VER=1.2.3
ASSETS="mash-linux-amd64 mash-linux-arm64 mash-darwin-amd64 mash-darwin-arm64"

# Fake release dir: $1/v$2/<asset> for every asset, plus $1/SHA256SUMS.
mkrelease() { # dir version
  mkdir -p "$1/v$2"
  : > "$1/SHA256SUMS"
  for a in $ASSETS; do
    cat > "$1/v$2/$a" <<BIN
#!/bin/sh
if [ "\$1" = version ]; then echo "mash $2 (commit x)"; exit 0; fi
echo "fake-mash ran: \$*"
BIN
    printf '%s  %s\n' "$(sha "$1/v$2/$a")" "$a" >> "$1/SHA256SUMS"
  done
}

# A fresh copy of the plugin tree, so pinning never touches the repo.
newplugin() { # dir
  mkdir -p "$1/.claude-plugin" "$1/scripts"
  cp "$HERE/mash" "$HERE/pin-plugin" "$1/scripts/"
  cp "$HERE/../.claude-plugin/plugin.json" "$1/.claude-plugin/"
  chmod +x "$1/scripts/mash" "$1/scripts/pin-plugin"
}

REL="$T/release"
mkrelease "$REL" "$VER"
export MASH_RELEASE_BASE="file://$REL"
unset MASH_BIN
NOMASH="$T/nomash"; mkdir -p "$NOMASH"
# PATH without any directory that already holds a real `mash`, so the host's install cannot leak in.
CLEANPATH=""
_oifs=$IFS; IFS=:
for _d in $PATH; do
  [ -x "$_d/mash" ] && continue
  CLEANPATH="$CLEANPATH:$_d"
done
IFS=$_oifs
BASEPATH="$NOMASH$CLEANPATH"

# --- pin-plugin
P="$T/plugin"; newplugin "$P"
cp "$P/scripts/mash" "$T/mash.orig"
cp "$P/.claude-plugin/plugin.json" "$T/plugin.json.orig"
absent "$P/scripts/mash.pins" && ok "fresh plugin tree has no mash.pins"
"$P/scripts/pin-plugin" "$VER" "$REL/SHA256SUMS" --unverified >/dev/null 2>&1
check "pin-plugin writes mash.pins" test -f "$P/scripts/mash.pins"
check "mash.pins has the version line" grep -q "^version=$VER\$" "$P/scripts/mash.pins"
check "mash.pins has four sums" eq "$(grep -c '^[0-9a-f]\{64\}  mash-' "$P/scripts/mash.pins")" 4
check "pin-plugin leaves scripts/mash byte-identical" cmp -s "$T/mash.orig" "$P/scripts/mash"
check "pin-plugin bumps plugin.json to <version>-pin.1" grep -q "\"version\": \"$VER-pin.1\"" "$P/.claude-plugin/plugin.json"
check "pin-plugin changes only the version line" eq "$(diff "$T/plugin.json.orig" "$P/.claude-plugin/plugin.json" | grep -c '^[<>]')" 2
sed "s/\"version\": \"[^\"]*\"/\"version\": \"X\"/" "$T/plugin.json.orig" > "$T/pj.a"
sed "s/\"version\": \"[^\"]*\"/\"version\": \"X\"/" "$P/.claude-plugin/plugin.json" > "$T/pj.b"
check "pin-plugin leaves the rest of plugin.json byte-identical" cmp -s "$T/pj.a" "$T/pj.b"
cp "$P/.claude-plugin/plugin.json" "$T/pj.pinned"
cp "$P/scripts/mash.pins" "$T/pins1"
"$P/scripts/pin-plugin" "v$VER" "$REL/SHA256SUMS" --unverified >/dev/null 2>&1
check "pin-plugin is idempotent" cmp -s "$T/pins1" "$P/scripts/mash.pins"
check "pin-plugin rerun leaves plugin.json alone" cmp -s "$T/pj.pinned" "$P/.claude-plugin/plugin.json"
sed "s/-pin\.1\"/-pin.4\"/" "$T/pj.pinned" > "$P/.claude-plugin/plugin.json"
"$P/scripts/pin-plugin" "$VER" "$REL/SHA256SUMS" --unverified >/dev/null 2>&1
check "pin-plugin keeps an existing <version>-pin.N" grep -q "\"version\": \"$VER-pin.4\"" "$P/.claude-plugin/plugin.json"
cp "$T/pj.pinned" "$P/.claude-plugin/plugin.json"
head -n 2 "$REL/SHA256SUMS" > "$T/short.sums"
if "$P/scripts/pin-plugin" 9.9.9 "$T/short.sums" --unverified >/dev/null 2>&1; then fail "pin-plugin rejects missing asset"; else ok "pin-plugin rejects missing asset"; fi
check "pin-plugin left mash.pins alone on failure" cmp -s "$T/pins1" "$P/scripts/mash.pins"
sed 's/^[0-9a-f]/Z/' "$REL/SHA256SUMS" > "$T/bad.sums"
if "$P/scripts/pin-plugin" 9.9.9 "$T/bad.sums" --unverified >/dev/null 2>&1; then fail "pin-plugin rejects non-hex sum"; else ok "pin-plugin rejects non-hex sum"; fi
check "mash.pins unchanged after bad sum" cmp -s "$T/pins1" "$P/scripts/mash.pins"

run() { # data-dir, args...   (stdout -> $T/out, stderr -> $T/err, status -> $RC)
  _data=$1; shift
  CLAUDE_PLUGIN_DATA="$_data" PATH="$BASEPATH" sh "$P/scripts/mash" "$@" > "$T/out" 2> "$T/err"
  RC=$?
}

# --- success: download, verify, exec
D1="$T/data1"
run "$D1" hello world
check "download+exec exits 0" eq "$RC" 0
check "exec'd binary got the args" eq "$(cat "$T/out")" "fake-mash ran: hello world"
check "binary installed at its versioned path" test -x "$D1/bin/mash-$VER"
check "no version marker file" absent "$D1/bin/mash.version"
check "no staging files left" eq "$(find "$D1/bin" -type f | wc -l | tr -d ' ')" 1

# --- cache hit: break the release; a cached copy must not need it
MASH_RELEASE_BASE="file://$T/does-not-exist" run "$D1" again
check "cache hit runs without download" eq "$(cat "$T/out")" "fake-mash ran: again"
check "cache hit did not try to download" empty_file "$T/err"

# --- a stale binary of another version at a different path is never run; the pinned one is downloaded
printf '#!/bin/sh\necho "STALE ran"\n' > "$D1/bin/mash-0.0.1"; chmod +x "$D1/bin/mash-0.0.1"
printf '#!/bin/sh\necho "STALE ran"\n' > "$D1/bin/mash"; chmod +x "$D1/bin/mash"
rm -f "$D1/bin/mash-$VER"
run "$D1" redo
check "other-version binaries never run" eq "$(cat "$T/out")" "fake-mash ran: redo"
check "pinned version re-downloaded" test -x "$D1/bin/mash-$VER"
check "other versions left in place" test -x "$D1/bin/mash-0.0.1"

# --- two pinned versions share one CLAUDE_PLUGIN_DATA and coexist
VER2=2.0.0
REL3="$T/release3"; mkrelease "$REL3" "$VER2"
PV2="$T/plugin-v2"; newplugin "$PV2"
"$PV2/scripts/pin-plugin" "$VER2" "$REL3/SHA256SUMS" --unverified >/dev/null 2>&1
DS="$T/shared"
run "$DS" one
check "shared data: v1 launcher runs v1" eq "$(cat "$T/out")" "fake-mash ran: one"
MASH_RELEASE_BASE="file://$REL3" CLAUDE_PLUGIN_DATA="$DS" PATH="$BASEPATH" sh "$PV2/scripts/mash" two > "$T/out" 2> "$T/err"
check "shared data: v2 launcher runs" eq "$(cat "$T/out")" "fake-mash ran: two"
check "shared data: both versions installed" test -x "$DS/bin/mash-$VER" -a -x "$DS/bin/mash-$VER2"
check "shared data: v1 binary reports v1" contains "$("$DS/bin/mash-$VER" version)" "mash $VER "
check "shared data: v2 binary reports v2" contains "$("$DS/bin/mash-$VER2" version)" "mash $VER2 "
MASH_RELEASE_BASE="file://$T/nope" run "$DS" one-again
check "shared data: v1 still its own binary, no download" eq "$(cat "$T/out")" "fake-mash ran: one-again"
MASH_RELEASE_BASE="file://$T/nope" CLAUDE_PLUGIN_DATA="$DS" PATH="$BASEPATH" sh "$PV2/scripts/mash" version > "$T/out" 2> "$T/err"
check "shared data: v2 launcher executes v2" contains "$(cat "$T/out")" "mash $VER2 "

# --- sha mismatch refused, nothing installed
REL2="$T/release2"; mkrelease "$REL2" "$VER"
for a in $ASSETS; do echo tampered >> "$REL2/v$VER/$a"; done
D2="$T/data2"
MASH_RELEASE_BASE="file://$REL2" run "$D2" x
check "sha mismatch refused (exit 127)" eq "$RC" 127
check "sha mismatch logged" contains "$(cat "$T/err")" "sha256 mismatch"
check "sha mismatch installed no binary" absent "$D2/bin/mash-$VER"
check "sha mismatch left no staging file" eq "$(find "$D2/bin" -type f 2>/dev/null | wc -l | tr -d ' ')" 0
check "sha mismatch wrote nothing to stdout" empty_file "$T/out"

# --- missing sum line refused
P2="$T/plugin2"; newplugin "$P2"
"$P2/scripts/pin-plugin" "$VER" "$REL/SHA256SUMS" --unverified >/dev/null 2>&1
sed '/^[0-9a-f]\{64\}  mash-/d' "$P2/scripts/mash.pins" > "$T/stripped"
cat "$T/stripped" > "$P2/scripts/mash.pins"
D3="$T/data3"
CLAUDE_PLUGIN_DATA="$D3" PATH="$BASEPATH" sh "$P2/scripts/mash" x > "$T/out" 2> "$T/err"; RC=$?
check "missing sum line refused (exit 127)" eq "$RC" 127
check "missing sum line installed nothing" absent "$D3/bin/mash-$VER"
check "missing sum line logged" contains "$(cat "$T/err")" "no pinned sha256"

P3="$T/plugin3"; newplugin "$P3"
# --- pinned: a reachable download beats an older mash on PATH
PB="$T/pathbin"; mkdir -p "$PB"
printf '#!/bin/sh\necho "path-mash: $*"\n' > "$PB/mash"; chmod +x "$PB/mash"
D4="$T/data4"
CLAUDE_PLUGIN_DATA="$D4" PATH="$PB:$BASEPATH" sh "$P/scripts/mash" serve > "$T/out" 2> "$T/err"
check "pinned: download beats PATH mash" eq "$(cat "$T/out")" "fake-mash ran: serve"
check "pinned: download installed the pinned binary" test -x "$D4/bin/mash-$VER"
# --- pinned, download fails: fall back to PATH, logged
D4b="$T/data4b"
MASH_RELEASE_BASE="file://$T/nope" CLAUDE_PLUGIN_DATA="$D4b" PATH="$PB:$BASEPATH" sh "$P/scripts/mash" serve > "$T/out" 2> "$T/err"
check "pinned, download failed: PATH fallback used" eq "$(cat "$T/out")" "path-mash: serve"
check "pinned, download failed: fallback logged" contains "$(cat "$T/err")" "falling back"
check "pinned, download failed: nothing installed" absent "$D4b/bin/mash-$VER"
# --- security stance: strict refuses the PATH fallback, relaxed (the default) falls back with a warning
stance_run() { # data-dir, release-base, stance ("-" = unset), args...
  _sd=$1; _sb=$2; _ss=$3; shift 3
  if [ "$_ss" = - ]; then
    env -u MASH_SECURITY_STANCE MASH_RELEASE_BASE="$_sb" CLAUDE_PLUGIN_DATA="$_sd" PATH="$PB:$BASEPATH" sh "$P/scripts/mash" "$@" > "$T/out" 2> "$T/err"
  else
    MASH_SECURITY_STANCE="$_ss" MASH_RELEASE_BASE="$_sb" CLAUDE_PLUGIN_DATA="$_sd" PATH="$PB:$BASEPATH" sh "$P/scripts/mash" "$@" > "$T/out" 2> "$T/err"
  fi
  RC=$?
}
NOREL="file://$T/nope"
stance_run "$T/sd1" "$NOREL" relaxed serve
check "relaxed, download fails: PATH mash runs" eq "$(cat "$T/out")" "path-mash: serve"
check "relaxed, download fails: warning names the failure" contains "$(cat "$T/err")" "warning: pinned mash $VER unavailable (download failed)"
check "relaxed, download fails: warning says relaxed" contains "$(cat "$T/err")" "relaxed fallback"
stance_run "$T/sd2" "file://$REL2" relaxed serve
check "relaxed, sha mismatch: PATH mash runs" eq "$(cat "$T/out")" "path-mash: serve"
check "relaxed, sha mismatch: warning names sha256 mismatch" contains "$(cat "$T/err")" "warning: pinned mash $VER unavailable (sha256 mismatch for"
check "relaxed, sha mismatch: nothing installed" absent "$T/sd2/bin/mash-$VER"
stance_run "$T/sd3" "$MASH_RELEASE_BASE" relaxed serve
check "relaxed, success: pinned binary runs" eq "$(cat "$T/out")" "fake-mash ran: serve"
check "relaxed, success: no warning" eq "$(grep -c 'warning' "$T/err")" 0
stance_run "$T/sd4" "$NOREL" strict serve
check "strict, download fails: exit 127" eq "$RC" 127
check "strict, download fails: PATH mash not run, stdout empty" empty_file "$T/out"
check "strict, download fails: error names strict" contains "$(cat "$T/err")" "security stance is strict"
check "strict, download fails: error names the failure" contains "$(cat "$T/err")" "(download failed)"
check "strict, download fails: error says how to relax" contains "$(cat "$T/err")" "MASH_SECURITY_STANCE=relaxed"
check "strict, download fails: no install hint" eq "$(grep -c 'go install' "$T/err")" 0
check "strict, download fails: nothing installed" absent "$T/sd4/bin/mash-$VER"
stance_run "$T/sd5" "file://$REL2" strict serve
check "strict, sha mismatch: exit 127" eq "$RC" 127
check "strict, sha mismatch: nothing on stdout" empty_file "$T/out"
check "strict, sha mismatch: error names sha256 mismatch" contains "$(cat "$T/err")" "sha256 mismatch for"
check "strict, sha mismatch: nothing installed" absent "$T/sd5/bin/mash-$VER"
stance_run "$T/sd6" "$MASH_RELEASE_BASE" strict serve
check "strict, success: pinned binary runs" eq "$(cat "$T/out")" "fake-mash ran: serve"
check "strict, success: exit 0, no warning" eq "$RC$(grep -c 'warning' "$T/err")" 00
stance_run "$T/sd7" "$NOREL" - serve
check "default stance (unset) is relaxed" eq "$(cat "$T/out")" "path-mash: serve"
stance_run "$T/sd8" "$NOREL" bogus serve
check "invalid stance behaves as strict (exit 127)" eq "$RC" 127
check "invalid stance does not run PATH mash" empty_file "$T/out"
check "invalid stance note names the value" contains "$(cat "$T/err")" "unrecognised security stance 'bogus'"
stance_run "$T/sd13" "$NOREL" "" serve
check "empty stance behaves as strict (exit 127)" eq "$RC" 127
check "empty stance does not run PATH mash" empty_file "$T/out"
stance_run "$T/sd9" "$NOREL" strict hook
check "strict, hook: refuses too" eq "$RC" 127
stance_run "$T/sd9" "$NOREL" strict hook
check "strict, hook skipping a recent failure: refuses" contains "$(cat "$T/err")" "failed recently"
check "strict, hook skipping a recent failure: exit 127" eq "$RC" 127
stance_run "$T/sd10" "$NOREL" strict __prefetch
check "strict does not change prefetch (exit 0, silent)" eq "$RC$(wc -c < "$T/out" | tr -d ' ')" 00
printf '#!/bin/sh\necho "override: $*"\n' > "$T/ovr0"; chmod +x "$T/ovr0"
MASH_SECURITY_STANCE=strict MASH_BIN="$T/ovr0" CLAUDE_PLUGIN_DATA="$T/sd11" PATH="$PB:$BASEPATH" sh "$P/scripts/mash" a > "$T/out" 2>/dev/null
check "strict: MASH_BIN override unchanged" eq "$(cat "$T/out")" "override: a"
MASH_SECURITY_STANCE=strict CLAUDE_PLUGIN_DATA="$T/sd3" PATH="$PB:$BASEPATH" MASH_RELEASE_BASE="$NOREL" sh "$P/scripts/mash" cached > "$T/out" 2>/dev/null
check "strict: managed-copy hit unchanged" eq "$(cat "$T/out")" "fake-mash ran: cached"

# --- strict never runs an unverified binary: unpinned plugin
MASH_SECURITY_STANCE=strict CLAUDE_PLUGIN_DATA="$T/sd12" PATH="$PB:$BASEPATH" sh "$P3/scripts/mash" serve > "$T/out" 2> "$T/err"; RC=$?
check "strict, unpinned (no pins file): exit 127" eq "$RC" 127
check "strict, unpinned: PATH mash not run" empty_file "$T/out"
check "strict, unpinned: error names the pins file" contains "$(cat "$T/err")" "$P3/scripts/mash.pins"
check "strict, unpinned: error says unpinned and strict" contains "$(cat "$T/err")" "unpinned"
check "strict, unpinned: error says strict and how to relax" contains "$(cat "$T/err")" "MASH_SECURITY_STANCE=relaxed"
check "strict, unpinned: says there is no pins file" contains "$(cat "$T/err")" "there is no"
P5="$T/plugin5"; newplugin "$P5"
printf '# nothing useful\nversion=\n' > "$P5/scripts/mash.pins"
MASH_SECURITY_STANCE=strict CLAUDE_PLUGIN_DATA="$T/sd14" PATH="$PB:$BASEPATH" sh "$P5/scripts/mash" serve > "$T/out" 2> "$T/err"; RC=$?
check "strict, malformed pins (no version line): exit 127" eq "$RC" 127
check "strict, malformed pins: PATH mash not run" empty_file "$T/out"
check "strict, malformed pins: error names the file and says no valid version" contains "$(cat "$T/err")" "$P5/scripts/mash.pins has no valid version= line"
MASH_SECURITY_STANCE=relaxed CLAUDE_PLUGIN_DATA="$T/sd15" PATH="$PB:$BASEPATH" sh "$P3/scripts/mash" serve > "$T/out" 2> "$T/err"; RC=$?
check "relaxed, unpinned: PATH mash runs" eq "$(cat "$T/out")" "path-mash: serve"
check "relaxed, unpinned: warns naming the pins file" contains "$(cat "$T/err")" "warning: this plugin is unpinned (there is no $P3/scripts/mash.pins)"
MASH_SECURITY_STANCE=strict MASH_BIN="$T/ovr0" CLAUDE_PLUGIN_DATA="$T/sd16" PATH="$PB:$BASEPATH" sh "$P3/scripts/mash" a > "$T/out" 2>/dev/null
check "strict, unpinned: MASH_BIN override still wins" eq "$(cat "$T/out")" "override: a"

# --- strict never runs an unverified binary: the cached copy is re-verified on every run
MASH_SECURITY_STANCE=strict CLAUDE_PLUGIN_DATA="$T/sd3" PATH="$PB:$BASEPATH" MASH_RELEASE_BASE="$NOREL" sh "$P/scripts/mash" cached > "$T/out" 2> "$T/err"; RC=$?
check "strict, cached binary with matching sha: runs it" eq "$(cat "$T/out")" "fake-mash ran: cached"
check "strict, cached binary with matching sha: silent" eq "$RC$(wc -c < "$T/err" | tr -d ' ')" 00
tamper() { # data-dir: copy the verified cache and change its content, keeping it executable
  rm -rf "$1"; mkdir -p "$1"; cp -R "$T/sd3/bin" "$1/bin"
  printf '#!/bin/sh\necho "TAMPERED ran"\n' > "$1/bin/mash-$VER"; chmod +x "$1/bin/mash-$VER"
}
tamper "$T/sd17"
MASH_SECURITY_STANCE=strict CLAUDE_PLUGIN_DATA="$T/sd17" PATH="$PB:$BASEPATH" MASH_RELEASE_BASE="$MASH_RELEASE_BASE" sh "$P/scripts/mash" x > "$T/out" 2> "$T/err"; RC=$?
check "strict, tampered cache: exit 127" eq "$RC" 127
check "strict, tampered cache: nothing run" empty_file "$T/out"
check "strict, tampered cache: error names the cached path" contains "$(cat "$T/err")" "$T/sd17/bin/mash-$VER"
check "strict, tampered cache: error says sha256 does not match mash.pins" contains "$(cat "$T/err")" "sha256 does not match mash.pins"
check "strict, tampered cache: error says how to relax" contains "$(cat "$T/err")" "MASH_SECURITY_STANCE=relaxed"
check "strict, tampered cache: not replaced" contains "$(cat "$T/sd17/bin/mash-$VER")" "TAMPERED"
tamper "$T/sd18"
MASH_SECURITY_STANCE=strict CLAUDE_PLUGIN_DATA="$T/sd18" PATH="$PB:$BASEPATH" MASH_RELEASE_BASE="$MASH_RELEASE_BASE" sh "$P/scripts/mash" hook > "$T/out" 2> "$T/err"; RC=$?
check "strict, tampered cache, hook: refuses too" eq "$RC" 127
MASH_SECURITY_STANCE=strict MASH_BIN="$T/ovr0" CLAUDE_PLUGIN_DATA="$T/sd17" PATH="$PB:$BASEPATH" sh "$P/scripts/mash" a > "$T/out" 2>/dev/null
check "strict, tampered cache: MASH_BIN override still wins" eq "$(cat "$T/out")" "override: a"
tamper "$T/sd19"
MASH_SECURITY_STANCE=relaxed CLAUDE_PLUGIN_DATA="$T/sd19" PATH="$PB:$BASEPATH" MASH_RELEASE_BASE="$MASH_RELEASE_BASE" sh "$P/scripts/mash" x > "$T/out" 2> "$T/err"; RC=$?
check "relaxed, tampered cache: re-fetches and runs the verified binary" eq "$(cat "$T/out")" "fake-mash ran: x"
check "relaxed, tampered cache: warns on stderr" contains "$(cat "$T/err")" "failed verification"
check "relaxed, tampered cache: cache replaced by the verified binary" eq "$(sha "$T/sd19/bin/mash-$VER")" "$(sha "$T/sd3/bin/mash-$VER")"
tamper "$T/sd20"
MASH_SECURITY_STANCE=relaxed CLAUDE_PLUGIN_DATA="$T/sd20" PATH="$PB:$BASEPATH" MASH_RELEASE_BASE="$NOREL" sh "$P/scripts/mash" x > "$T/out" 2> "$T/err"; RC=$?
check "relaxed, tampered cache, release unreachable: PATH mash runs" eq "$(cat "$T/out")" "path-mash: x"
check "relaxed, tampered cache, release unreachable: warns about the cache" contains "$(cat "$T/err")" "failed verification"
check "relaxed, tampered cache, release unreachable: warns about the fallback" contains "$(cat "$T/err")" "relaxed fallback"
check "relaxed, tampered cache, release unreachable: tampered file left in place" contains "$(cat "$T/sd20/bin/mash-$VER")" "TAMPERED"

# --- unpinned: PATH only, never downloads
CLAUDE_PLUGIN_DATA="$T/data4c" PATH="$PB:$BASEPATH" sh "$P3/scripts/mash" serve > "$T/out" 2> "$T/err"
check "unpinned uses PATH mash" eq "$(cat "$T/out")" "path-mash: serve"
check "unpinned PATH use never downloads" absent "$T/data4c"

# --- hook mode: the download has a hard budget, then PATH; a failure is remembered for 300 s
FC="$T/fakecurl"; mkdir -p "$FC"
cat > "$FC/curl" <<'CURL'
#!/bin/sh
echo "$*" >> "$FAKE_CURL_LOG"
sleep "${FAKE_CURL_SLEEP:-0}"
exit 28
CURL
chmod +x "$FC/curl"
DH="$T/datah"; : > "$T/curllog"
_t0=$(date +%s)
FAKE_CURL_LOG="$T/curllog" FAKE_CURL_SLEEP=30 CLAUDE_PLUGIN_DATA="$DH" PATH="$FC:$PB:$BASEPATH" sh "$P/scripts/mash" hook > "$T/out" 2> "$T/err"
_el=$(( $(date +%s) - _t0 ))
check "hook, stalled download: PATH mash ran" eq "$(cat "$T/out")" "path-mash: hook"
check "hook, stalled download: reached PATH in under 6 s" test "$_el" -lt 6
check "hook, stalled download: curl capped (4 s)" contains "$(cat "$T/curllog")" "--max-time 4"
check "hook, stalled download: no retry flag" eq "$(grep -c -e '--retry' "$T/curllog")" 0
check "hook, failed download leaves the marker" test -s "$DH/bin/.dl-failed-$VER"
: > "$T/curllog"
FAKE_CURL_LOG="$T/curllog" FAKE_CURL_SLEEP=30 CLAUDE_PLUGIN_DATA="$DH" PATH="$FC:$PB:$BASEPATH" sh "$P/scripts/mash" hook > "$T/out" 2> "$T/err"
check "hook within 300 s of a failure: PATH mash ran" eq "$(cat "$T/out")" "path-mash: hook"
check "hook within 300 s of a failure: curl not called" empty_file "$T/curllog"
echo 1 > "$DH/bin/.dl-failed-$VER"
FAKE_CURL_LOG="$T/curllog" CLAUDE_PLUGIN_DATA="$DH" PATH="$FC:$PB:$BASEPATH" sh "$P/scripts/mash" hook > "$T/out" 2> "$T/err"
check "hook with an old marker tries the download again" test -s "$T/curllog"
# serve is never capped and ignores the marker
: > "$T/curllog"; date +%s > "$DH/bin/.dl-failed-$VER"
FAKE_CURL_LOG="$T/curllog" CLAUDE_PLUGIN_DATA="$DH" PATH="$FC:$PB:$BASEPATH" sh "$P/scripts/mash" serve > "$T/out" 2> "$T/err"
check "serve ignores the marker and uses the 60 s max-time" contains "$(cat "$T/curllog")" "--max-time 60"
check "serve keeps its retries" contains "$(cat "$T/curllog")" "--retry 2"
# a successful install removes the marker (real curl)
date +%s > "$DH/bin/.dl-failed-$VER"
CLAUDE_PLUGIN_DATA="$DH" PATH="$PB:$BASEPATH" sh "$P/scripts/mash" serve > "$T/out" 2> "$T/err"
check "serve success installs despite marker" test -x "$DH/bin/mash-$VER"
check "successful install removes the marker" absent "$DH/bin/.dl-failed-$VER"
DH2="$T/datah2"; mkdir -p "$DH2/bin"; date +%s > "$DH2/bin/.dl-failed-$VER"
CLAUDE_PLUGIN_DATA="$DH2" PATH="$PB:$BASEPATH" sh "$P/scripts/mash" __prefetch > "$T/out" 2> "$T/err"
check "prefetch ignores the marker and installs" test -x "$DH2/bin/mash-$VER"
check "prefetch success removes the marker" absent "$DH2/bin/.dl-failed-$VER"

# --- the launcher itself on PATH is skipped (no recursion)
CLAUDE_PLUGIN_DATA="$T/data5" PATH="$P/scripts:$BASEPATH" sh "$P/scripts/mash" serve > "$T/out" 2> "$T/err"
check "self on PATH is skipped" eq "$(cat "$T/out")" "fake-mash ran: serve"

# --- MASH_BIN override wins
printf '#!/bin/sh\necho "override: $*"\n' > "$T/ovr"; chmod +x "$T/ovr"
MASH_BIN="$T/ovr" run "$T/data6" a b
check "MASH_BIN override" eq "$(cat "$T/out")" "override: a b"
check "MASH_BIN did not download" absent "$T/data6/bin/mash-$VER"

# --- unpinned launcher (no mash.pins, or one without a version line) never downloads
P4="$T/plugin4"; newplugin "$P4"; : > "$P4/scripts/mash.pins"
CLAUDE_PLUGIN_DATA="$T/data7b" PATH="$BASEPATH" sh "$P4/scripts/mash" serve > "$T/out" 2> "$T/err"; RC=$?
check "empty mash.pins exits 127" eq "$RC" 127
check "empty mash.pins never downloads" absent "$T/data7b"
D7="$T/data7"
CLAUDE_PLUGIN_DATA="$D7" PATH="$BASEPATH" sh "$P3/scripts/mash" serve > "$T/out" 2> "$T/err"; RC=$?
check "unpinned with nothing resolvable exits 127" eq "$RC" 127
check "unpinned never downloads" absent "$D7"
check "unpinned prints install hint on stderr" contains "$(cat "$T/err")" "go install github.com/bogworks/mash/cmd/mash@latest"
check "unpinned wrote nothing to stdout" empty_file "$T/out"

# --- prefetch
D8="$T/data8"
run "$D8" __prefetch
check "prefetch success exits 0" eq "$RC" 0
check "prefetch success silent on stdout" empty_file "$T/out"
check "prefetch installed binary" test -x "$D8/bin/mash-$VER"
MASH_RELEASE_BASE="file://$REL2" run "$T/data9" __prefetch
check "prefetch on bad sha exits 0" eq "$RC" 0
check "prefetch on bad sha silent on stdout" empty_file "$T/out"
check "prefetch on bad sha installed nothing" absent "$T/data9/bin/mash-$VER"
MASH_RELEASE_BASE="file://$T/nope" run "$T/data10" __prefetch
check "prefetch offline exits 0" eq "$RC" 0
check "prefetch offline silent on stdout" empty_file "$T/out"
CLAUDE_PLUGIN_DATA="$T/data11" PATH="$BASEPATH" sh "$P3/scripts/mash" __prefetch > "$T/out" 2> "$T/err"; RC=$?
check "prefetch unpinned exits 0" eq "$RC" 0
check "prefetch unpinned silent on stdout" empty_file "$T/out"
check "prefetch unpinned never downloads" absent "$T/data11"

# --- bare $0 (no slash) falls back to the current directory for mash.pins
(cd "$P/scripts" && PATH="$BASEPATH" CLAUDE_PLUGIN_DATA="$T/data13" sh mash serve > "$T/out" 2> "$T/err")
check "bare \$0 finds mash.pins in ." eq "$(cat "$T/out")" "fake-mash ran: serve"

# --- no exec bit (embed export), relative path, self on PATH as a relative dir: still skipped
chmod -x "$P/scripts/mash"
(cd "$P" && CLAUDE_PLUGIN_DATA="$T/data12" PATH="scripts:$BASEPATH" sh scripts/mash serve > "$T/out" 2> "$T/err")
check "no exec bit, relative self on PATH skipped" eq "$(cat "$T/out")" "fake-mash ran: serve"

# --- pin-plugin fails closed: signature / attestation / explicit opt-out
if command -v ssh-keygen >/dev/null 2>&1; then
  K="$T/keys"; mkdir -p "$K"
  ssh-keygen -q -t ed25519 -N '' -C test -f "$K/k" >/dev/null 2>&1
  ssh-keygen -q -t ed25519 -N '' -C other -f "$K/other" >/dev/null 2>&1
  printf 'release namespaces="mash-release" %s\n' "$(cut -d' ' -f1,2 "$K/k.pub")" > "$K/signers"
  printf 'release namespaces="mash-release" %s\n' "$(cut -d' ' -f1,2 "$K/other.pub")" > "$K/other-signers"
  printf '# placeholder\n\n' > "$K/empty-signers"
  printf 'release sk-ssh-ed25519@openssh.com AAAAGnNrLXNzaC1lZDI1NTE5QG9wZW5zc2guY29t\n' > "$K/sk-signers"
  PP="$T/plugin-sig"; newplugin "$PP"; PS="$PP/scripts/pin-plugin"; PINS="$PP/scripts/mash.pins"
  cp "$REL/SHA256SUMS" "$K/SHA256SUMS"
  ssh-keygen -Y sign -q -f "$K/k" -n mash-release "$K/SHA256SUMS" >/dev/null 2>&1   # writes SHA256SUMS.sig
  cp "$K/SHA256SUMS" "$K/wrongns.sums"
  ssh-keygen -Y sign -q -f "$K/k" -n git "$K/wrongns.sums" >/dev/null 2>&1
  cp "$REL/SHA256SUMS" "$K/tampered"; printf '%s  mash-linux-amd64\n' "$(printf x | sha256sum | cut -d' ' -f1)" >> "$K/tampered"
  sed 's/^[0-9a-f]/0/' "$K/SHA256SUMS" > "$K/tampered2"

  "$PS" "$VER" "$K/SHA256SUMS" --sig "$K/SHA256SUMS.sig" --signers "$K/signers" >/dev/null 2>&1; RC=$?
  check "pin-plugin --sig good signature exits 0" eq "$RC" 0
  check "pin-plugin --sig good signature pins" cmp -s "$PINS" "$T/pins1"
  check "pin-plugin --sig good signature bumps plugin.json" grep -q "\"version\": \"$VER-pin.1\"" "$PP/.claude-plugin/plugin.json"
  rm -f "$PINS"; cp "$T/plugin.json.orig" "$PP/.claude-plugin/plugin.json"
  refused() { # desc, then pin-plugin args; must exit 1 and write nothing
    _d=$1; shift
    "$PS" "$@" >/dev/null 2>"$T/err"; _rc=$?
    check "$_d exits 1" eq "$_rc" 1
    check "$_d writes nothing" absent "$PINS"
    check "$_d leaves plugin.json alone" cmp -s "$T/plugin.json.orig" "$PP/.claude-plugin/plugin.json"
  }
  refused "pin-plugin --sig tampered sums" "$VER" "$K/tampered2" --sig "$K/SHA256SUMS.sig" --signers "$K/signers"
  refused "pin-plugin --sig appended sums" "$VER" "$K/tampered" --sig "$K/SHA256SUMS.sig" --signers "$K/signers"
  refused "pin-plugin --sig wrong namespace" "$VER" "$K/wrongns.sums" --sig "$K/wrongns.sums.sig" --signers "$K/signers"
  refused "pin-plugin --sig missing sig file" "$VER" "$K/SHA256SUMS" --sig "$K/nope.sig" --signers "$K/signers"
  refused "pin-plugin --sig untrusted signer" "$VER" "$K/SHA256SUMS" --sig "$K/SHA256SUMS.sig" --signers "$K/other-signers"
  refused "pin-plugin --sig sk- key in signers" "$VER" "$K/SHA256SUMS" --sig "$K/SHA256SUMS.sig" --signers "$K/sk-signers"
  refused "pin-plugin --sig signers without keys" "$VER" "$K/SHA256SUMS" --sig "$K/SHA256SUMS.sig" --signers "$K/empty-signers"
  check "pin-plugin names the empty signers file" contains "$(cat "$T/err")" "no key lines"
  refused "pin-plugin --sig default placeholder signers" "$VER" "$K/SHA256SUMS" --sig "$K/SHA256SUMS.sig"
  NOSSH="$T/nossh"; mkdir -p "$NOSSH"
  for _t in sh awk grep cp cat mktemp rm mv head sed dirname cut sha256sum; do
    _p=$(command -v "$_t") && ln -sf "$_p" "$NOSSH/$_t"
  done
  PATH="$NOSSH" "$PS" "$VER" "$K/SHA256SUMS" --sig "$K/SHA256SUMS.sig" --signers "$K/signers" >/dev/null 2>&1; RC=$?
  check "pin-plugin --sig without ssh-keygen exits 1" eq "$RC" 1
  check "pin-plugin --sig without ssh-keygen writes nothing" absent "$PINS"
else
  echo "skip - ssh-keygen not installed: signature tests"
  PP="$T/plugin-sig"; newplugin "$PP"; PS="$PP/scripts/pin-plugin"; PINS="$PP/scripts/mash.pins"
fi

# a manifest without a single version line: exit 1, neither file written
for _bad in 'no version' 'two versions'; do
  PM="$T/plugin-m"; rm -rf "$PM"; newplugin "$PM"
  if [ "$_bad" = "no version" ]; then grep -v '"version"' "$T/plugin.json.orig" > "$PM/.claude-plugin/plugin.json"
  else { cat "$T/plugin.json.orig"; echo '"version": "1"'; } > "$PM/.claude-plugin/plugin.json"; fi
  cp "$PM/.claude-plugin/plugin.json" "$T/pj.m"
  "$PM/scripts/pin-plugin" "$VER" "$REL/SHA256SUMS" --unverified >/dev/null 2>&1; RC=$?
  check "pin-plugin manifest with $_bad exits 1" eq "$RC" 1
  check "pin-plugin manifest with $_bad writes no pins" absent "$PM/scripts/mash.pins"
  check "pin-plugin manifest with $_bad leaves the manifest" cmp -s "$T/pj.m" "$PM/.claude-plugin/plugin.json"
done

"$PS" >/dev/null 2>&1; RC=$?;                                     check "pin-plugin no args exits 2" eq "$RC" 2
"$PS" "$VER" "$REL/SHA256SUMS" >/dev/null 2>&1; RC=$?;           check "pin-plugin no mode exits 2" eq "$RC" 2
check "pin-plugin no mode writes nothing" absent "$PINS"
"$PS" "$VER" "$REL/SHA256SUMS" --unverified --sig x >/dev/null 2>&1; RC=$?; check "pin-plugin two modes exits 2" eq "$RC" 2
"$PS" "$VER" --attested "$T" --unverified >/dev/null 2>&1; RC=$?;          check "pin-plugin attested+unverified exits 2" eq "$RC" 2
check "pin-plugin usage errors write nothing" absent "$PINS"

"$PS" "$VER" "$REL/SHA256SUMS" --unverified >/dev/null 2>"$T/err"; RC=$?
check "pin-plugin --unverified exits 0" eq "$RC" 0
check "pin-plugin --unverified warns" contains "$(cat "$T/err")" "WARNING"
check "pin-plugin --unverified pins" test -f "$PINS"
rm -f "$PINS"

# attested mode with a fake gh on PATH
ATT="$T/att"; mkdir -p "$ATT"
for a in $ASSETS; do cp "$REL/v$VER/$a" "$ATT/$a"; done
FG="$T/fakegh"; mkdir -p "$FG"
cat > "$FG/gh" <<'GH'
#!/bin/sh
# fake gh: gh attestation verify <file> -R <repo>; fails for FAKE_GH_FAIL (a basename) and records the repo
[ "$1 $2" = "attestation verify" ] || exit 64
echo "$5" > "${FAKE_GH_LOG:-/dev/null}"
[ "$(basename "$3")" = "${FAKE_GH_FAIL:-}" ] && { echo "verification failed" >&2; exit 1; }
exit 0
GH
chmod +x "$FG/gh"
FAKE_GH_LOG="$T/ghrepo" PATH="$FG:$PATH" "$PS" "$VER" --attested "$ATT" --repo o/n >/dev/null 2>&1; RC=$?
check "pin-plugin --attested success exits 0" eq "$RC" 0
check "pin-plugin --attested pins computed sums" cmp -s "$PINS" "$T/pins1"
check "pin-plugin --attested passes the repo" eq "$(cat "$T/ghrepo")" "o/n"
rm -f "$PINS"
FAKE_GH_FAIL=mash-darwin-arm64 PATH="$FG:$PATH" "$PS" "$VER" --attested "$ATT" --repo o/n >/dev/null 2>&1; RC=$?
check "pin-plugin --attested one failure exits 1" eq "$RC" 1
check "pin-plugin --attested one failure writes nothing" absent "$PINS"
rm "$ATT/mash-linux-arm64"
PATH="$FG:$PATH" "$PS" "$VER" --attested "$ATT" --repo o/n >/dev/null 2>&1; RC=$?
check "pin-plugin --attested missing binary exits 1" eq "$RC" 1
check "pin-plugin --attested missing binary writes nothing" absent "$PINS"
cp "$REL/v$VER/mash-linux-arm64" "$ATT/"
NOGH="$T/nogh"; mkdir -p "$NOGH"
for _t in sh awk cp cat mktemp rm mv head sed dirname git sha256sum; do _p=$(command -v "$_t") && ln -sf "$_p" "$NOGH/$_t"; done
PATH="$NOGH" "$PS" "$VER" --attested "$ATT" --repo o/n >/dev/null 2>&1; RC=$?
check "pin-plugin --attested without gh exits 1" eq "$RC" 1
check "pin-plugin --attested without gh writes nothing" absent "$PINS"

echo
echo "$PASS passed, $FAIL failed"
[ "$FAIL" = 0 ]
