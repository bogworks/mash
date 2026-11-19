#!/usr/bin/env bash
# Test for docs/skills/mash-nats-local/SKILL.md (the guide).  Run with:
#
#     bash scripts/mash-nats-local-guide_test.sh [--update-closing]
#
# It extracts every fenced block whose info string is `sh` or `sh person`,
# in order, and runs each one in ONE fresh rootless podman container (debian
# bookworm-slim) as user alice: each `sh` block as its own `bash -c`, with
# HOME=/home/alice and PATH=/usr/local/bin:/usr/bin:/bin only; each `sh
# person` block on a pseudo-terminal with the typed confirmation fed in.
# Nothing runs on the host except the Go build (in a scratch copy of the
# repo) and podman.  See the SUBSTITUTIONS section of the report for what
# differs from the guide.  The container and image are removed by exact name.
#
# --update-closing writes the helper's closing output (from the create block)
# into SKILL.md between the closing-begin/closing-end markers; without it the
# fresh output is compared with what is embedded.
# GUIDETEST_REPORT=FILE sets where the report goes (default: none kept);
# GUIDETEST_OUT=DIR (must not exist) keeps every block's raw output.
# Needs: podman (rootless), go, python3, network (apt, GitHub).
set -u

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P) || exit 2
REPO=$(dirname "$HERE")
SKILL=$REPO/docs/skills/mash-nats-local/SKILL.md
[ -f "$SKILL" ] || { echo "no $SKILL" >&2; exit 2; }
for c in podman go python3 awk; do
    command -v "$c" >/dev/null || { echo "$c is required" >&2; exit 2; }
done
UPDATE=0
case "${1:-}" in
--update-closing) UPDATE=1 ;;
"") ;;
*) echo "usage: $0 [--update-closing]" >&2; exit 2 ;;
esac

T=$(mktemp -d "${TMPDIR:-/tmp}/guidetest.XXXXXX") || exit 2
RUNID=$(printf '%s' "${T##*.}" | tr 'A-Z' 'a-z')
C=mash-guidetest-$RUNID
IMG=localhost/mash-guidetest-$RUNID
BUILT_IMG=0
CIP=/home/alice

cleanup() {
    if [ -n "${GUIDETEST_REPORT:-}" ] && [ -f "$T/report.md" ]; then
        cp "$T/report.md" "$GUIDETEST_REPORT"
    fi
    if [ -n "${GUIDETEST_OUT:-}" ] && [ -d "$T/out" ] &&
        [ ! -e "$GUIDETEST_OUT" ]; then
        cp -r "$T/out" "$GUIDETEST_OUT"
    fi
    podman rm -f "$C" >/dev/null 2>&1
    [ "$BUILT_IMG" = 1 ] && podman rmi -f "$IMG" >/dev/null 2>&1
    case $T in
    */guidetest.*) rm -rf -- "$T" ;;
    esac
}
trap cleanup EXIT

say() { printf '== %s\n' "$*"; }
mkdir -p "$T/out" "$T/blk" "$T/bin" "$T/gocache" "$T/ctx" "$T/assets"

# ------------------------------------------------- the guide itself
say "reading the guide"
awk -v d="$T/blk" '
/^```/ {
    if (!infence) {
        infence = 1
        info = substr($0, 4)
        cap = (info == "sh" || info == "sh person")
        if (cap) {
            n++
            f = sprintf("%s/%02d.sh", d, n)
            printf "%s\n", info > sprintf("%s/%02d.info", d, n)
            close(sprintf("%s/%02d.info", d, n))
            printf "" > f
        }
    } else {
        infence = 0
        if (cap) close(f)
        cap = 0
    }
    next
}
infence { if (cap) print > f; next }
length > 80 { printf "%d: %s\n", NR, $0 > (d "/wide.txt") }
' "$SKILL"
NBLK=$(find "$T/blk" -name '*.sh' | grep -c .)
WIDE=0
[ -f "$T/blk/wide.txt" ] && WIDE=$(grep -c . "$T/blk/wide.txt")
LONGEST=$(cat "$T"/blk/*.sh | awk '{ if (length > m) m = length } END { print m + 0 }')
LONGEST_ASCII=$(grep -c '[^ -~]' "$SKILL" || true)

# ------------------------------------------------- build
say "building mash and mashctl in a scratch copy of the repo"
cp -a "$REPO" "$T/src" || exit 2
python3 -I - "$T/src/cmd/mashctl/adopt.go" <<'__PATCH__' || exit 2
import sys
p = sys.argv[1]
s = open(p).read()
old = '''		adminPath = st.adminCredsAbs
		step("%srecord the admin creds path %s (the file is not copied)", verb, adminPath)
'''
new = '''		adminPath = st.adminDst
		if o.dryRun {
			step("would copy the admin creds to %s (0600)", st.adminDst)
			break
		}
		if _, err := ctlkeys.InstallFile(st.adminDst, st.adminRaw); err != nil {
			return fail("%v", err)
		}
		step("admin creds copied to %s", st.adminDst)
'''
if s.count(old) != 1:
    sys.exit("adopt.go patch: the expected text is not there exactly once")
open(p, "w").write(s.replace(old, new))
__PATCH__
for b in mashctl mash; do
    (cd "$T/src" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
        GOCACHE="$T/gocache" go build -o "$T/assets/$b-linux-amd64" \
        "./cmd/$b") || { echo "FAIL  go build $b" >&2; exit 2; }
done
printf '{}\n' >"$T/assets/mash.intoto.jsonl"

cat >"$T/assets/curl" <<'__CURL__'
#!/bin/bash
# test shim: the v0.5.6 release assets of bogworks/mash come from local files;
# every other URL goes to the real curl untouched.
args=()
for a in "$@"; do
    case $a in
    https://github.com/bogworks/mash/releases/download/v0.5.6/*)
        f=${a##*/}
        case $f in
        mash-linux-amd64 | mashctl-linux-amd64 | mash.intoto.jsonl)
            a=file:///opt/guide-assets/$f ;;
        esac ;;
    esac
    args+=("$a")
done
exec /usr/bin/curl "${args[@]}"
__CURL__
cat >"$T/assets/gh-shim" <<'__GH__'
#!/bin/bash
# test shim for `gh attestation verify`: logs argv, checks the flags the guide
# promises, exits 0.  (The real v0.5.6 assets do not exist yet.)
printf '%s\n' "$*" >>/home/alice/gh-shim.log
all=" $* "
for w in "attestation verify" " -R bogworks/mash " \
    " --bundle mash.intoto.jsonl " \
    " --signer-workflow bogworks/mash/.github/workflows/release.yml " \
    " --source-ref refs/tags/v0.5.6 "; do
    case $all in
    *"$w"*) ;;
    *) echo "gh shim: missing '$w'" >&2; exit 1 ;;
    esac
done
exit 0
__GH__

cat >"$T/ctx/Containerfile" <<'__CF__'
FROM docker.io/library/debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends sudo
RUN useradd -m -u 1000 -s /bin/bash alice && echo 'alice ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/alice && chmod 440 /etc/sudoers.d/alice
__CF__
say "building the image $IMG"
podman build -q -t "$IMG" "$T/ctx" >/dev/null || exit 2
BUILT_IMG=1
podman run -d --name "$C" --user alice "$IMG" sleep 7200 >/dev/null || exit 2
podman exec --user root "$C" mkdir -p /opt/guide-assets
for f in mash-linux-amd64 mashctl-linux-amd64 mash.intoto.jsonl; do
    podman cp "$T/assets/$f" "$C:/opt/guide-assets/$f"
done
podman exec --user root "$C" chmod -R a+rX /opt/guide-assets

# ------------------------------------------------- runners
PASSN=0
FAILN=0
BPASS=0
BFAIL=0
BWHY=""
OUT=""
RC=0
cexec() {
    podman exec --user alice -w "$CIP" "$C" env -i HOME="$CIP" \
        PATH=/usr/local/bin:/usr/bin:/bin "$@"
}
cin() { OUT=$(cexec bash -c "$1" 2>&1 </dev/null); RC=$?; }
has() { printf '%s\n' "$OUT" | grep -qF -- "$1"; }
ck() { # DESCRIPTION CMD...: one assertion
    local d=$1
    shift
    if "$@"; then PASSN=$((PASSN + 1)); else
        FAILN=$((FAILN + 1))
        BWHY="$BWHY; $d"
    fi
}
nlines() { [ "$(printf '%s\n' "$OUT" | grep -cF -- "$1")" -eq "$2" ]; }
firstline() { [ "$(printf '%s\n' "$OUT" | head -n1 | cut -c1-3)" = "$1" ]; }
sig() { grep -qF -- "$2" "$T/blk/$(printf %02d "$1").sh"; }
rc0() { [ "$RC" -eq 0 ]; }
nothas() { ! has "$1"; }
row() { # N METHOD RC
    local st=PASS
    [ -z "$BWHY" ] || st="FAIL$BWHY"
    case $st in PASS) BPASS=$((BPASS + 1)) ;; *) BFAIL=$((BFAIL + 1)) ;; esac
    local first
    first=$(head -c 60 "$T/blk/$(printf %02d "$1").sh" | tr '\n' ' ')
    printf '| %s | `%s` | %s | %s | %s |\n' "$1" \
        "$(printf '%s' "$first" | sed 's/|/\\|/g')" "$2" "$3" "$st" \
        >>"$T/rows.md"
    printf '%s  block %s (%s rc=%s)%s\n' "${st%%;*}" "$1" "$2" "$3" \
        "${BWHY:+  $BWHY}"
    BWHY=""
}
save() { # NAME: keep the current output, secrets scrubbed
    printf '%s\n' "$OUT" | tr -d '\r' |
        sed -E 's/S[OANUCX][A-Z2-7]{40,}/<SEED>/g;s/eyJ[A-Za-z0-9_.-]{20,}/<JWT>/g' \
            >"$T/out/$1.txt"
}
runblk() { # N: as written, its own bash -c
    local f
    f=$T/blk/$(printf %02d "$1").sh
    cin "$(cat "$f")"
    save "blk$(printf %02d "$1")"
}
runpty() { # N TYPED: on a pty (script -qec), the confirmation fed in
    local nn f
    nn=$(printf %02d "$1")
    f=$T/blk/$nn.sh
    podman cp "$f" "$C:$CIP/blk$nn.sh"
    podman exec --user root "$C" chown alice:alice "$CIP/blk$nn.sh"
    OUT=$({ sleep 5; printf '%s\n' "$2"; } |
        podman exec -i --user alice -w "$CIP" "$C" env -i HOME="$CIP" \
            PATH=/usr/local/bin:/usr/bin:/bin \
            script -qec "bash -c \"\$(cat $CIP/blk$nn.sh)\"" /dev/null 2>&1)
    RC=$?
    OUT=$(printf '%s\n' "$OUT" | tr -d '\r')
    save "blk$nn"
}
extra() { # NAME: an extra row (not a guide block)
    local st=PASS
    [ -z "$BWHY" ] || st="FAIL$BWHY"
    case $st in PASS) BPASS=$((BPASS + 1)) ;; *) BFAIL=$((BFAIL + 1)) ;; esac
    printf '| %s | (test step) | bash -c | %s | %s |\n' "$1" "$RC" "$st" \
        >>"$T/rows.md"
    printf '%s  step: %s%s\n' "${st%%;*}" "$1" "${BWHY:+  $BWHY}"
    BWHY=""
}

# ------------------------------------------------- the guide's own checks
say "checking the guide itself"
BWHY=""
RC=0
ck "block count is 30 (got $NBLK)" test "$NBLK" -eq 30
ck "$WIDE prose lines over 80 columns" test "$WIDE" -eq 0
ck "non-ASCII characters in SKILL.md" test "$LONGEST_ASCII" -eq 0
extra "guide: 30 sh blocks, prose <= 80 columns, ASCII"

# expected signature per block (so a reordered guide fails loudly)
declare -A SIG=(
    [1]='command -v "$c"' [2]='sudo apt-get install' [3]='exists: $p'
    [4]='sha256sum -c -' [5]='--owner acme --start' [6]='--check'
    [7]='" version;' [8]='gh_2.105.0_linux_amd64.tar.gz' [9]='releases/download'
    [10]='attestation verify' [11]='install -m 755 "$f-linux-amd64"'
    [12]='--dry-run' [13]='--admin-creds' [14]='mashctl" init'
    [15]="stat -c" [16]='create acme/demo/alice' [17]='create acme/demo/bob'
    [18]='endpoint list' [19]='" send acme.demo.bob' [20]='" inbox'
    [21]='echo "same $p"' [22]='--retire-keys' [23]='--check' [24]='--stop'
    [25]='--start' [26]='--backup' [27]='--upgrade' [28]='--enable-boot'
    [29]='--disable-boot' [30]='--remove'
)
declare -A KIND=([22]=person [28]=person [29]=person [30]=person)
for n in $(seq 1 30); do
    want="sh"
    [ -n "${KIND[$n]:-}" ] && want="sh person"
    got=$(cat "$T/blk/$(printf %02d "$n").info" 2>/dev/null)
    BWHY=""
    ck "info string is '$got', expected '$want'" test "$got" = "$want"
    ck "block $n does not contain '${SIG[$n]}'" sig "$n" "${SIG[$n]}"
    if [ -n "$BWHY" ]; then
        extra "block $n identity"
    fi
done
BWHY=""

# ------------------------------------------------- run the blocks
D=$CIP/mash-nats
CA=$D/ca.pem
MC="MASH_TLS_CA=$CA"
say "running the blocks"

# 1: missing check, before apt
runblk 1
ck "rc 0" rc0
for m in curl unzip openssl ss; do ck "missing: $m is reported" has "missing: $m"; done
ck "tar is not reported missing" nothas "missing: tar"
ck "sha256sum is not reported missing" nothas "missing: sha256sum"
ck "prints checked" has checked
row 1 "bash -c" "$RC"

# 2: apt
runblk 2
ck "rc 0" rc0
row 2 "bash -c" "$RC"
podman cp "$T/assets/curl" "$C:/usr/local/bin/curl"
podman exec --user root "$C" chmod 755 /usr/local/bin/curl
RC=0
extra "install curl shim at /usr/local/bin/curl (after apt)"
runblk 1
save blk01-after-apt
ck "rc 0" rc0
ck "nothing missing after apt" nothas "missing:"
ck "prints checked" has checked
extra "block 1 again, after apt: nothing missing"
cin 'command -v curl; head -c 30 /usr/local/bin/curl | head -n1'
RC=0

# 3: nothing exists
runblk 3
ck "rc 0" rc0
ck "no 'exists:' line" nothas "exists:"
ck "no port line" nothas "LISTEN"
ck "prints only checked" test "$OUT" = checked
row 3 "bash -c" "$RC"

# 4: fetch the script
runblk 4
ck "rc 0" rc0
ck "sha256sum says OK" has ": OK"
row 4 "bash -c" "$RC"

# 5: create --start
runblk 5
ck "rc 0" rc0
ck "prints Done." has "Done."
row 5 "bash -c" "$RC"
awk '/^Done\./ { p = 1 } p' "$T/out/blk05.txt" >"$T/out/closing.txt"

# 6: check
runblk 6
ck "rc 0" rc0
ck "no FAIL line" nothas "FAIL"
row 6 "bash -c" "$RC"

# 7: mash/mashctl are absent
runblk 7
ck "rc 0" rc0
ck "No such file" has "No such file"
row 7 "bash -c" "$RC"

# 8: gh install (real download, real sha256)
runblk 8
ck "rc 0" rc0
ck "gh version 2.105.0" has "gh version 2.105.0"
ck "sha256 OK" has ": OK"
row 8 "bash -c" "$RC"

# 8b: the REAL gh against the real v0.5.5 assets
cin 'mkdir -p "$HOME/v055" && cd "$HOME/v055" && for f in mash-linux-amd64 mash.intoto.jsonl; do curl -fsSLO "https://github.com/bogworks/mash/releases/download/v0.5.5/$f" || exit 90; done && "$HOME/.local/bin/gh" attestation verify mash-linux-amd64 -R bogworks/mash --bundle mash.intoto.jsonl --signer-workflow bogworks/mash/.github/workflows/release.yml --source-ref refs/tags/v0.5.5; echo "gh exit=$?"'
save real-gh-v055
ck "real gh verify of v0.5.5 exits 0" has "gh exit=0"
extra "REAL gh attestation verify on real v0.5.5 mash-linux-amd64"

# swap gh for the shim (after the real one is installed and used)
podman cp "$T/assets/gh-shim" "$C:$CIP/gh-shim"
podman exec --user root "$C" chown alice:alice "$CIP/gh-shim"
cin 'install -m 755 "$HOME/gh-shim" "$HOME/.local/bin/gh" && rm "$HOME/gh-shim"'
ck "shim installed" rc0
extra "swap \$HOME/.local/bin/gh for the argv-checking shim (never restored)"

# 9: downloads (shim curl)
runblk 9
ck "rc 0" rc0
for f in mash-linux-amd64 mashctl-linux-amd64 mash.intoto.jsonl; do
    ck "ls shows $f" has "$f"
done
row 9 "bash -c" "$RC"

# 10: verify
runblk 10
ck "rc 0" rc0
ck "prints verified" has verified
cin 'cat "$HOME/gh-shim.log"'
ck "shim saw two verify calls" test "$(printf '%s\n' "$OUT" | grep -c 'attestation verify')" -eq 2
row 10 "bash -c" "$RC"

# 11: install
runblk 11
ck "rc 0" rc0
ck "mashctl version printed" has mashctl
row 11 "bash -c" "$RC"

# 12: dry run
runblk 12
ck "rc 0" rc0
ck "All checks passed" has "All checks passed"
ck "dry-run says nothing is written" has "nothing is written"
ck "no FAIL or error line" nothas "FAIL"
row 12 "bash -c" "$RC"

# 13: adopt
runblk 13
ck "rc 0" rc0
ck "All checks passed" has "All checks passed"
ck "mashadmin.creds copied, not recorded" nothas "not copied"
row 13 "bash -c" "$RC"

# 14: init
runblk 14
ck "rc 0" rc0
row 14 "bash -c" "$RC"

# 15: stat
runblk 15
ck "rc 0" rc0
ck "first line begins 700" firstline 700
row 15 "bash -c" "$RC"

# 16-18: endpoints
runblk 16
ck "rc 0" rc0
row 16 "bash -c" "$RC"
runblk 17
ck "rc 0" rc0
row 17 "bash -c" "$RC"
runblk 18
ck "rc 0" rc0
ck "alice listed" has alice
ck "bob listed" has bob
row 18 "bash -c" "$RC"

# 19-20: alice -> bob
runblk 19
ck "rc 0" rc0
row 19 "bash -c" "$RC"
runblk 20
ck "rc 0" rc0
ck "[verified]" has "[verified]"
ck "Hello Bob" has "Hello Bob"
ck "from alice" has alice
row 20 "bash -c" "$RC"

# 21: cmp
runblk 21
ck "rc 0" rc0
ck "same four times" nlines "same " 4
ck "no differ" nothas differ
row 21 "bash -c" "$RC"
# diagnostic (no secret printed): same content once whitespace is dropped?
cin 'for p in keys/operator.nk keys/account.nk; do f="$HOME/mash-nats/export/$(basename "$p")"; g="$HOME/.config/mash/$p"; a=$(tr -d " \n" <"$f" | sha256sum); b=$(tr -d " \n" <"$g" | sha256sum); [ "$a" = "$b" ] && s=equal-without-whitespace || s=DIFFERENT-CONTENT; echo "$p: $s; sizes $(stat -c %s "$f") vs $(stat -c %s "$g")"; done; ls -l "$HOME/.config/mash/keys"'
save diag-cmp
cin 'for p in keys/operator.nk keys/account.nk server/sys.creds server/mashadmin.creds; do f="$HOME/mash-nats/export/$(basename "$p")"; [ -s "$f" ] && [ "$(cat "$f")" = "$(cat "$HOME/.config/mash/$p")" ] && echo "same $p" || echo "differ $p"; done'
save proposed-cmp
ck "proposed block 21 prints same four times" nlines "same " 4
ck "proposed block 21 prints no differ" nothas differ
extra "PROPOSED replacement for block 21 (not the guide's text)"

# 22: retire-keys on a pty
runpty 22 "$D"
ck "rc 0" rc0
row 22 "pty (script -qec)" "$RC"
cin 'for p in export ca.key nsc; do [ -e "$HOME/mash-nats/$p" ] && echo "STILL: $p"; done; stat -c "%a %n" "$HOME/mash-nats-keys.tar.gz"; tar tzf "$HOME/mash-nats-keys.tar.gz" >"$HOME/keys.lst"; echo "tar rc=$?"; head -n 8 "$HOME/keys.lst"'
save retire-probe
ck "export/, ca.key, nsc/ are gone" nothas "STILL:"
ck "archive mode is 600" has "600 /home/alice/mash-nats-keys.tar.gz"
ck "archive lists back" has "tar rc=0"
extra "after retire-keys: files gone, archive 600 and lists back"

# 23: check after
runblk 23
ck "rc 0" rc0
ck "no FAIL line" nothas "FAIL"
row 23 "bash -c" "$RC"

answers() { # the server answers through mashctl
    cin "$MC \$HOME/.local/bin/mashctl endpoint list"
    ck "mashctl endpoint list rc 0" rc0
    ck "alice still listed" has alice
}

# 24-27: lifecycle
runblk 24
ck "rc 0" rc0
ck "stopped pid" has "stopped pid"
row 24 "bash -c" "$RC"
cin 'ss -ltn | grep ":4222 "; true'
ck "port 4222 no longer listening" test -z "$OUT"
extra "after stop: nothing listens on 4222"

runblk 25
ck "rc 0" rc0
row 25 "bash -c" "$RC"
BWHY=""
answers
extra "after start: the server answers (mashctl endpoint list)"

runblk 26
ck "rc 0" rc0
row 26 "bash -c" "$RC"
cin 'stat -c "%a %n" "$HOME/mash-nats-backup.tar.gz"; tar tzf "$HOME/mash-nats-backup.tar.gz" | head -n 5'
save backup-probe
ck "backup archive mode 600" has "600 /home/alice/mash-nats-backup.tar.gz"
answers
extra "after backup: archive 600, the server answers again"

runblk 27
ck "rc 0" rc0
row 27 "bash -c" "$RC"
answers
extra "after upgrade: the server answers"

# 28, 29: boot service needs systemd; not run here
BWHY=""
for n in 28 29; do
    printf '| %s | `%s` | skipped | - | NOT RUN (no systemd here; covered by nats-local-setup_test.sh boot group) |\n' \
        "$n" "$(head -c 60 "$T/blk/$n.sh" | sed 's/|/\\|/g')" >>"$T/rows.md"
done

# 30: remove, last
runpty 30 "$D"
ck "rc 0" rc0
row 30 "pty (script -qec)" "$RC"
cin '[ ! -e "$HOME/mash-nats" ] && echo gone; [ -d "$HOME/.config/mash" ] && echo "mashctl folder kept"'
ck "server directory gone" has gone
ck "mashctl folder kept" has "mashctl folder kept"
extra "after remove: server dir gone, mashctl folder kept"
cin 'ss -ltn | grep ":4222 "; true'
ck "port 4222 free after remove" test -z "$OUT"
extra "after remove: nothing listens on 4222"

# ------------------------------------------------- the closing output
say "closing output"
CL=$T/out/closing.txt
BWHY=""
RC=0
ck "closing text captured" test -s "$CL"
ck "closing text <= 80 columns" test "$(awk 'length > 80' "$CL" | grep -c .)" -eq 0
ck "closing text has no \$HOME" test "$(grep -cF '$HOME' "$CL")" -eq 0
ck "closing text uses /home/alice paths" grep -q '/home/alice/' "$CL"
ck "closing text has no pid or key id" test \
    "$(grep -Eic 'pid [0-9]+|[A-Z2-7]{50,}' "$CL")" -eq 0
extra "closing text: captured, <= 80 columns, real paths, no pid or key id"

if [ "$UPDATE" = 1 ] && [ -s "$CL" ]; then
    awk -v f="$CL" '
    /<!-- closing-begin -->/ {
        print; print "```"
        while ((getline l < f) > 0) print l
        print "```"; skip = 1; next
    }
    /<!-- closing-end -->/ { skip = 0 }
    !skip { print }' "$SKILL" >"$T/SKILL.new" && cp "$T/SKILL.new" "$SKILL"
    say "SKILL.md closing text updated"
fi
awk '/<!-- closing-begin -->/ { on = 1; next }
    /<!-- closing-end -->/ { on = 0 }
    on && $0 != "```"' "$SKILL" >"$T/out/embedded.txt"
BWHY=""
ck "embedded closing text is not empty" test -s "$T/out/embedded.txt"
ck "embedded closing text equals the fresh output" \
    diff -q "$T/out/embedded.txt" "$CL"
extra "embedded closing text equals the fresh create output"

# ------------------------------------------------- report
say "writing the report"
{
    printf '# nats-local guide test report\n\n'
    printf 'RESULT: %s blocks/steps passed, %s failed; %s assertions passed, %s failed.\n\n' \
        "$BPASS" "$BFAIL" "$PASSN" "$FAILN"
    printf 'Guide checks: %s sh blocks; %s prose lines over 80 columns; longest sh block line %s columns (not a failure).\n\n' \
        "$NBLK" "$WIDE" "$LONGEST"
    printf '## Per-block results\n\n| n | first 60 chars | method | rc | result |\n|---|---|---|---|---|\n'
    cat "$T/rows.md"
    cat <<'__SUBS__'

## SUBSTITUTIONS

(a) /usr/local/bin/curl in the container is a shim, installed after the apt
    block (so the "missing" check sees no curl first). It maps
    https://github.com/bogworks/mash/releases/download/v0.5.6/<f> to local
    files for mash-linux-amd64 and mashctl-linux-amd64 (built from this repo
    tree) and mash.intoto.jsonl (placeholder `{}`); every other URL goes to
    the real /usr/bin/curl.
(b) The gh install block ran for real (real download, real sha256, gh
    v2.105.0). Before the verify block, $HOME/.local/bin/gh was replaced by a
    shim that logs argv, checks it holds `attestation verify`, `-R
    bogworks/mash`, `--bundle mash.intoto.jsonl`, `--signer-workflow
    bogworks/mash/.github/workflows/release.yml`, `--source-ref
    refs/tags/v0.5.6`, and exits 0. It was not restored afterwards. Earlier,
    the real gh was run on the real v0.5.5 mash-linux-amd64 and
    mash.intoto.jsonl (same flags, v0.5.5 ref).
(c) mash and mashctl were built (CGO_ENABLED=0, linux/amd64) from a scratch
    COPY of the repo. In the copy, cmd/mashctl/adopt.go was patched so that
    `adopt --admin-creds FILE` copies the file to <config>/server/
    mashadmin.creds (0600, like sys.creds) instead of recording its path and
    printing "(the file is not copied)". This stands in for the 0.5.6
    behaviour lead described; it is not committed anywhere.
(d) Other changes: the image is debian:bookworm-slim plus sudo only (apt lists
    kept, because the guide runs apt-get install without apt-get update); user
    alice was made with useradd (uid 1000) with NOPASSWD sudo; each block ran
    as `podman exec --user alice ... env -i HOME=/home/alice
    PATH=/usr/local/bin:/usr/bin:/bin bash -c '<block>'`; `sh person` blocks
    ran under `script -qec` (a pty) with the directory typed after a 5 second
    wait; enable-boot and disable-boot were not run (no systemd in this
    container).
__SUBS__
    printf '\n## NOT VERIFIED\n\n'
    cat <<'__NV__'
- The real attestation verify on v0.5.6: the v0.5.6 assets do not exist yet
  and the release workflow does not yet publish mashctl, so verify on 0.5.6
  is NOT verified. Only the v0.5.5 mash binary was verified for real.
- --enable-boot and --disable-boot (not run here), and a real reboot.
- Any distribution other than Debian 12 (bookworm), and Ubuntu.
- mashctl adopt copying the admin creds: tested only with the patched,
  uncommitted binary.
__NV__
    printf '\n## Real output (trimmed)\n'
    sec() { # TITLE FILE [LINES]
        printf '\n### %s\n\n```\n' "$1"
        head -n "${3:-40}" "$2"
        printf '```\n'
    }
    sec "missing-check (block 1, before apt)" "$T/out/blk01.txt"
    sec "missing-check again (after apt)" "$T/out/blk01-after-apt.txt"
    sec "create output, from Done. to the end" "$CL" 60
    sec "adopt --dry-run (block 12)" "$T/out/blk12.txt" 40
    sec "adopt (block 13)" "$T/out/blk13.txt" 40
    sec "stat (block 15)" "$T/out/blk15.txt"
    sec "cmp (block 21)" "$T/out/blk21.txt"
    sec "cmp diagnostic" "$T/out/diag-cmp.txt"
    sec "cmp, PROPOSED replacement" "$T/out/proposed-cmp.txt"
    sec "alice send (block 19)" "$T/out/blk19.txt"
    sec "bob inbox (block 20)" "$T/out/blk20.txt"
    sec "retire-keys (block 22, pty)" "$T/out/blk22.txt" 40
    sec "after retire-keys probe" "$T/out/retire-probe.txt"
    sec "check before retire (block 6)" "$T/out/blk06.txt" 40
    sec "check after retire (block 23)" "$T/out/blk23.txt" 40
    sec "stop (block 24)" "$T/out/blk24.txt"
    sec "start (block 25)" "$T/out/blk25.txt"
    sec "backup (block 26)" "$T/out/blk26.txt"
    sec "backup probe" "$T/out/backup-probe.txt"
    sec "upgrade (block 27)" "$T/out/blk27.txt"
    sec "remove (block 30, pty)" "$T/out/blk30.txt"
    sec "real gh on v0.5.5" "$T/out/real-gh-v055.txt"
    sec "verify (block 10)" "$T/out/blk10.txt"
} >"$T/report.md"

printf '\nRESULT: %s blocks/steps passed, %s failed; %s assertions passed, %s failed\n' \
    "$BPASS" "$BFAIL" "$PASSN" "$FAILN"
[ "$BFAIL" -eq 0 ]
