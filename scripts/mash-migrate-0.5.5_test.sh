#!/usr/bin/env bash
# Container test for scripts/mash-migrate-0.5.5.sh.  Run it with:
#
#     bash scripts/mash-migrate-0.5.5_test.sh
#
# Venue rules: rootless podman on this host only; official debian:stable only
# (its digest is in the receipt); one container per case, named after the run
# id, no ports published, network none, a non-root user (tester); nothing is
# installed on the host.  On exit (also on failure) it removes only this run's
# containers, by name, this run's images, by recorded id, and its build
# directory.
#
# Environment:
#   MMIG_CASES        space-separated case numbers (default: 1 ... 10, 12 and 13, and 11 when MMIG_SERVER_TEST=1)
#   MMIG_REPORT       receipt file (markdown; default: mmig-receipt-<run id>.md under ${TMPDIR:-/tmp})
#   MMIG_SCRATCH      scratch directory (default: a new mktemp -d, removed at the end when
#                     the test made it and nothing failed)
#   MMIG_SCRIPT       test another copy of the script (default: scripts/mash-migrate-0.5.5.sh)
#   MMIG_MASH         a LINUX amd64 static `mash` binary (0.5.6 or newer); case 10 runs it on the
#                     migrated and the unmigrated file.  Default unset: the test builds one from
#                     this checkout on the host before the containers start (needs go):
#                       CGO_ENABLED=0 go build -trimpath -o <run dir>/mash ./cmd/mash
#                     and removes it, by that exact path, at the end.  A failed build fails the run.
#   MMIG_SERVER_TEST  1: also run case 11, a live local NATS server made by
#                     scripts/mash-local-nats-setup.sh inside the container; needs MMIG_MASHCTL (a linux amd64 static mashctl with `init --config`), the network
#                     for the image build (apt, natscli download) and takes a few minutes
#   MMIG_MASHCTL      the mashctl binary for case 11
#   MMIG_NATSCLI      natscli version for case 11 (default 0.5.2; others need MMIG_NATSCLI_SHA256)
#
# shellcheck disable=SC2016,SC2329  # bash -c bodies are single-quoted on purpose; helpers are called through chk

# On macOS (no podman) run the cases on the host with Apple bash 3.2 and BSD tools; each case then needs its own
# HOME (and TMPDIR), because the cases share the folder $HOME/.config/mash and leave files in it.

# Every assertion prints "ok: ..." or "FAIL: ..."; the totals come last and the
# exit status is non-zero when any assertion failed.

# ======================================================================
# Part 1: runs INSIDE the container (as `bash test.sh N` with MMIG_INSIDE=1)
# ======================================================================
inside_main() {
set -u
SCRIPT=/t/mash-migrate-0.5.5.sh
CASE=$1
H=${HOME:?}
W=$(mktemp -d "${TMPDIR:-/tmp}/w.XXXXXX")
RC=0
CFG=$H/.config/mash

ok() { printf 'ok: c%s: %s\n' "$CASE" "$1"; }
dump() {
    printf '  --- rc=%s, stdout then stderr of the last run\n' "$RC"
    { head -n 12 "$W/out"; echo '  ---'; head -n 12 "$W/err"; } 2>/dev/null | sed 's/^/  | /'
}
bad() { printf 'FAIL: c%s: %s\n' "$CASE" "$1"; dump; }
chk() {
    local d=$1
    shift
    if "$@" >"$W/chk" 2>&1; then ok "$d"; else bad "$d"; sed 's/^/  > /' "$W/chk" | head -n 8; fi
}
rc_is() { [ "$RC" = "$1" ]; }
has() { grep -Fq -- "$2" "$1"; }
lacks() { ! grep -Fq -- "$2" "$1"; }
mode_of() { stat -c %a "$1"; }
mode_is() { [ "$(mode_of "$1")" = "$2" ]; }
lines_of() { [ "$(wc -l <"$1" | tr -d ' ')" = "$2" ]; }
absent() { [ ! -e "$1" ] && [ ! -L "$1" ]; }
same() { cmp -s "$1" "$2"; }
empty() { [ ! -s "$1" ]; }
# refused: exit 1, stdout empty, stderr is exactly an error: line and a hint: line
refused() {
    [ "$RC" = 1 ] && [ ! -s "$W/out" ] && [ "$(wc -l <"$W/err" | tr -d ' ')" = 2 ] &&
        [ "$(sed -n 1p "$W/err" | cut -c1-7)" = 'error: ' ] && [ "$(sed -n 2p "$W/err" | cut -c1-6)" = 'hint: ' ]
}

# mig [VAR=val ...]: the script as a file, with a clean folder environment plus the given variables
mig() {
    env -u MASH_CONFIG_DIR -u XDG_CONFIG_HOME "$@" bash "$SCRIPT" >"$W/out" 2>"$W/err" </dev/null
    RC=$?
}
# migpipe [VAR=val ...]: the published way, `cat script | bash`
migpipe() {
    cat "$SCRIPT" | env -u MASH_CONFIG_DIR -u XDG_CONFIG_HOME "$@" bash >"$W/out" 2>"$W/err"
    RC=$?
}
# put FILE: stdin to FILE, mode 600
put() { mkdir -p "$(dirname "$1")"; cat >"$1"; chmod 600 "$1"; }
# snap DIR: a listing with mode, size and a checksum of every file under DIR
snap() { (cd "$1" && find . -type f -exec sh -c 'for f; do printf "%s %s %s\n" "$f" "$(stat -c "%a %s" "$f")" "$(cksum <"$f")"; done' _ {} + | sort); }

REAL055='{
  "url": "tls://127.0.0.1:4222",
  "owner": "acme",
  "admin_creds": "/node/.config/mash/server/mashadmin.creds",
  "tls_ca": "/node/.config/mash/server/ca.pem"
}'
EXP056='{
  "servers": ["tls://127.0.0.1:4222"],
  "owner": "acme",
  "admin_creds": "/node/.config/mash/server/mashadmin.creds",
  "ca": "/node/.config/mash/server/ca.pem"
}'

case1() { # a real 0.5.5 file
    printf '%s\n' "$REAL055" | put "$CFG/config.json"
    cp -p "$CFG/config.json" "$W/orig"
    printf '%s\n' "$EXP056" >"$W/exp"
    mig HOME="$H"
    chk "exit 0" rc_is 0
    chk "migrated file is the expected text, every other byte unchanged" same "$CFG/config.json" "$W/exp"
    chk "migrated file has mode 600" mode_is "$CFG/config.json" 600
    chk "backup is config.json.0.5.5" test -f "$CFG/config.json.0.5.5"
    chk "backup equals the original byte for byte" same "$CFG/config.json.0.5.5" "$W/orig"
    chk "backup keeps the original's mode (600)" mode_is "$CFG/config.json.0.5.5" 600
    chk "one line on stdout" lines_of "$W/out" 1
    chk "stderr is empty" empty "$W/err"
    chk "the line says what changed" has "$W/out" "Migrated $CFG/config.json: url -> servers, tls_ca -> ca (old file kept as $CFG/config.json.0.5.5)"
    chk "no temp file is left: only config.json and the backup remain" bash -c '[ "$(ls -A "$1")" = "config.json
config.json.0.5.5" ]' _ "$CFG"
    chk "the script has no predictable pid-based name" bash -c '! grep -Fq "\$\$" "$1"' _ "$SCRIPT"
}

case2() { # commas
    printf '{\n  "url": "tls://a:4222, tls://b:4222"\n}\n' | put "$CFG/config.json"
    mig HOME="$H"
    chk "exit 0" rc_is 0
    printf '{\n  "servers": ["tls://a:4222", "tls://b:4222"]\n}\n' >"$W/exp"
    chk "two addresses become a 2-element list" same "$CFG/config.json" "$W/exp"
    chk "url alone: the line names only url" has "$W/out" ": url -> servers (old file"
    printf '{"url": " tls://a:1 ,, tls://b:2 ,tls://c:3,"}\n' | put "$CFG/config.json"
    rm -f "$CFG/config.json.0.5.5"
    mig HOME="$H"
    printf '{"servers": ["tls://a:1", "tls://b:2", "tls://c:3"]}\n' >"$W/exp"
    chk "spaces trimmed, empties dropped, three addresses" same "$CFG/config.json" "$W/exp"
}

case3() { # one old key only
    printf '{"url": "tls://a:1", "owner": "o"}\n' | put "$CFG/config.json"
    mig HOME="$H"
    chk "only url: exit 0" rc_is 0
    chk "only url: servers, owner untouched" same "$CFG/config.json" <(printf '{"servers": ["tls://a:1"], "owner": "o"}\n')
    chk "only url: the line names url only" has "$W/out" ": url -> servers (old file"
    chk "only url: the line does not say tls_ca" lacks "$W/out" tls_ca
    rm -f "$CFG/config.json.0.5.5"
    printf '{"owner": "o", "tls_ca": "/c.pem"}\n' | put "$CFG/config.json"
    mig HOME="$H"
    chk "only tls_ca: exit 0" rc_is 0
    chk "only tls_ca: ca, owner untouched" same "$CFG/config.json" <(printf '{"owner": "o", "ca": "/c.pem"}\n')
    chk "only tls_ca: the line names tls_ca only" has "$W/out" ": tls_ca -> ca (old file"
    chk "only tls_ca: the line does not say url" lacks "$W/out" 'url ->'
}

case4() { # folder resolution
    local m=$H/m x=$H/x h=$H/hh
    for d in "$m" "$x/mash" "$h/.config/mash"; do
        printf '{"url": "tls://a:1"}\n' | put "$d/config.json"
    done
    hit() { # which of the three folders now has a backup
        local r='' d
        for d in "$m" "$x/mash" "$h/.config/mash"; do [ -e "$d/config.json.0.5.5" ] && r="$r${r:+ }$d"; done
        printf '%s\n' "$r"
        for d in "$m" "$x/mash" "$h/.config/mash"; do rm -f "$d/config.json.0.5.5"; printf '{"url": "tls://a:1"}\n' | put "$d/config.json"; done
    }
    mig HOME="$h" XDG_CONFIG_HOME="$x" MASH_CONFIG_DIR="$m"
    chk "MASH_CONFIG_DIR beats XDG and HOME" [ "$(hit)" = "$m" ]
    mig HOME="$h" XDG_CONFIG_HOME="$x"
    chk "XDG_CONFIG_HOME/mash beats HOME" [ "$(hit)" = "$x/mash" ]
    mig HOME="$h"
    chk "HOME/.config/mash by default" [ "$(hit)" = "$h/.config/mash" ]
    mig HOME="$h" XDG_CONFIG_HOME="$x" MASH_CONFIG_DIR=rel/m
    chk "a relative MASH_CONFIG_DIR is refused (nothing in any folder changed)" bash -c '[ "$1" = 1 ] && [ -z "$2" ]' _ "$RC" "$(hit)"
    chk "a relative MASH_CONFIG_DIR: an error line that names the variable, and not its value" bash -c 'grep -qx "error: MASH_CONFIG_DIR is not an absolute path" "$1" && ! grep -q "rel/m" "$1"' _ "$W/err"
    chk "a relative MASH_CONFIG_DIR: an error line and a hint line" refused
    mig HOME="$h" XDG_CONFIG_HOME=rel/x
    chk "a relative XDG_CONFIG_HOME is ignored (HOME wins)" [ "$(hit)" = "$h/.config/mash" ]
    mig HOME="$h" XDG_CONFIG_HOME=rel/x MASH_CONFIG_DIR=rel/m
    chk "both relative: MASH_CONFIG_DIR is refused, nothing changed" bash -c '[ "$1" = 1 ] && [ -z "$2" ] && grep -q "^error: MASH_CONFIG_DIR is not" "$3"' _ "$RC" "$(hit)" "$W/err"
    mig HOME="$h" MASH_CONFIG_DIR="$m/"
    chk "a trailing slash on MASH_CONFIG_DIR is fine" [ "$(hit)" = "$m" ]
    env -u HOME -u MASH_CONFIG_DIR -u XDG_CONFIG_HOME bash "$SCRIPT" >"$W/out" 2>"$W/err" </dev/null
    RC=$?
    chk "no HOME and nothing else: a refusal that names HOME" bash -c '[ "$1" = 1 ] && grep -q "^error: .*HOME" "$2"' _ "$RC" "$W/err"
    env -u HOME XDG_CONFIG_HOME="$x" bash "$SCRIPT" >"$W/out" 2>"$W/err" </dev/null
    RC=$?
    chk "no HOME but an absolute XDG_CONFIG_HOME: works" rc_is 0
}

case5() { # refusals
    local before
    # no file
    rm -rf "$CFG"; mkdir -p "$CFG"
    mig HOME="$H"
    chk "no config.json: refused" refused
    chk "no config.json: nothing created" bash -c '[ -z "$(ls -A "$1")" ]' _ "$CFG"
    rm -rf "$CFG"
    mig HOME="$H"
    chk "no folder: refused" refused
    chk "no folder: not created" absent "$CFG"
    # no old keys
    printf '{"servers": ["tls://a:1"], "ca": "/c", "limits": {"url": "x"}}\n' | put "$CFG/config.json"
    before=$(snap "$CFG")
    mig HOME="$H"
    chk "no old keys (a nested url does not count): refused" refused
    chk "no old keys: nothing changed" [ "$(snap "$CFG")" = "$before" ]
    # both
    for body in '{"url": "tls://a:1", "servers": ["tls://b:1"]}' '{"tls_ca": "/c", "ca": "/d"}' '{"url": "tls://a:1", "ca": "/d"}' '{"tls_ca": "/c", "servers": []}'; do
        printf '%s\n' "$body" | put "$CFG/config.json"
        before=$(snap "$CFG")
        mig HOME="$H"
        chk "old and new keys together ($body): refused" refused
        chk "old and new keys together: nothing changed" [ "$(snap "$CFG")" = "$before" ]
    done
    # backup exists
    printf '{"url": "tls://a:1"}\n' | put "$CFG/config.json"
    printf 'precious\n' | put "$CFG/config.json.0.5.5"
    before=$(snap "$CFG")
    mig HOME="$H"
    chk "backup exists: refused" refused
    chk "backup exists: file and backup both unchanged" [ "$(snap "$CFG")" = "$before" ]
    rm -f "$CFG/config.json.0.5.5"
    ln -s /nonexistent "$CFG/config.json.0.5.5"
    mig HOME="$H"
    chk "backup is a dangling symlink: refused" refused
    chk "backup symlink untouched" [ "$(readlink "$CFG/config.json.0.5.5")" = /nonexistent ]
    rm -f "$CFG/config.json.0.5.5"
    # url not a string
    for body in '{"url": ["tls://a:1"]}' '{"url": null}' '{"url": 5}' '{"url": {"a": 1}}'; do
        printf '%s\n' "$body" | put "$CFG/config.json"
        before=$(snap "$CFG")
        mig HOME="$H"
        chk "url is not a string ($body): refused" refused
        chk "url is not a string: nothing changed" [ "$(snap "$CFG")" = "$before" ]
    done
    # broken JSON
    printf '{"url": "tls://a:1"' | put "$CFG/config.json"
    before=$(snap "$CFG")
    mig HOME="$H"
    chk "truncated file: refused" refused
    chk "truncated file: nothing changed" [ "$(snap "$CFG")" = "$before" ]
    # usage
    bash "$SCRIPT" --bogus >"$W/out" 2>"$W/err" </dev/null; RC=$?
    chk "an unknown argument: exit 2" rc_is 2
    bash "$SCRIPT" a b >"$W/out" 2>"$W/err" </dev/null; RC=$?
    chk "two arguments: exit 2" rc_is 2
    bash "$SCRIPT" --help >"$W/out" 2>"$W/err" </dev/null; RC=$?
    chk "--help: exit 0 and usage on stdout" bash -c '[ "$1" = 0 ] && grep -q "^usage:" "$2"' _ "$RC" "$W/out"
    bash "$SCRIPT" -h >"$W/out" 2>"$W/err" </dev/null; RC=$?
    chk "-h: exit 0" rc_is 0
}

case6() { # twice
    printf '%s\n' "$REAL055" | put "$CFG/config.json"
    mig HOME="$H"
    chk "first run: exit 0" rc_is 0
    local before
    before=$(snap "$CFG")
    mig HOME="$H"
    chk "second run: refused" refused
    chk "second run: says nothing to migrate" has "$W/err" 'nothing to migrate'
    chk "second run: nothing changed" [ "$(snap "$CFG")" = "$before" ]
}

case7() { # piped
    printf '%s\n' "$REAL055" | put "$CFG/config.json"
    printf '%s\n' "$EXP056" >"$W/exp"
    migpipe HOME="$H"
    chk "cat script | bash: exit 0" rc_is 0
    chk "cat script | bash: migrated" same "$CFG/config.json" "$W/exp"
    chk "cat script | bash: one line" lines_of "$W/out" 1
    printf '%s\n' "$REAL055" | put "$CFG/config.json"
    rm -f "$CFG/config.json.0.5.5"
    cat "$SCRIPT" | env -u MASH_CONFIG_DIR -u XDG_CONFIG_HOME HOME="$H" bash -s -- >"$W/out" 2>"$W/err"
    RC=$?
    chk "cat script | bash -s --: exit 0" rc_is 0
}

case8() { # escapes, unicode, nested keys, layout
    cat >"$W/in" <<'EOF'
{
	"owner": "a\"b\\céé€ , \"url\": 1, ]}",
  "url"  :   "tls://x:4222",
  "limits": {"url": "keep", "tls_ca": "keep2", "inbox": 5},
  "list": ["url", {"url": 1}, ["tls_ca"]],
  "note": "tls_ca\"url\"",
  "flag": true, "n": -1.5e3, "z": null
}
EOF
    sed 's/"url"  :   "tls:\/\/x:4222"/"servers"  :   ["tls:\/\/x:4222"]/' "$W/in" >"$W/exp"
    put "$CFG/config.json" <"$W/in"
    mig HOME="$H"
    chk "escapes, unicode, nested url/tls_ca keys: exit 0" rc_is 0
    chk "only the top-level url token changed, the rest byte for byte" same "$CFG/config.json" "$W/exp"
    chk "backup is the original" same "$CFG/config.json.0.5.5" "$W/in"
    # escapes inside the url value stay as written
    rm -f "$CFG/config.json.0.5.5"
    printf '{"url": "tls://a:1,tls://b\\u00e9:2"}\n' | put "$CFG/config.json"
    mig HOME="$H"
    chk "an escape inside the url value is kept as written" same "$CFG/config.json" <(printf '{"servers": ["tls://a:1", "tls://b\\u00e9:2"]}\n')
    # no trailing newline, compact, CRLF
    rm -f "$CFG/config.json.0.5.5"
    printf '{"url":"tls://a:1","tls_ca":"/c"}' | put "$CFG/config.json"
    mig HOME="$H"
    chk "compact file, no trailing newline: kept so" same "$CFG/config.json" <(printf '{"servers":["tls://a:1"],"ca":"/c"}')
    rm -f "$CFG/config.json.0.5.5"
    printf '{\r\n  "url": "tls://a:1"\r\n}\r\n' | put "$CFG/config.json"
    mig HOME="$H"
    chk "CRLF line ends kept" same "$CFG/config.json" <(printf '{\r\n  "servers": ["tls://a:1"]\r\n}\r\n')
}

case9() { # no network, no writes outside the folder
    printf '%s\n' "$REAL055" | put "$CFG/config.json"
    if [ -d /sys/class/net ]; then
        chk "the container has only the loopback interface" bash -c '[ "$(ls /sys/class/net)" = lo ]'
    else
        ok "skipped: no /sys/class/net (the loopback-only check needs Linux)"
    fi
    find "$H" /tmp -mindepth 1 -path "$W" -prune -o -print 2>/dev/null | sort >"$W/before"
    snap "$H" >"$W/snap0"
    mig HOME="$H"
    find "$H" /tmp -mindepth 1 -path "$W" -prune -o -print 2>/dev/null | sort >"$W/after"
    chk "exit 0" rc_is 0
    chk "the only new path under HOME and /tmp is the backup" bash -c '[ "$(comm -13 "$1" "$2")" = "$3" ] && [ -z "$(comm -23 "$1" "$2")" ]' _ "$W/before" "$W/after" "$CFG/config.json.0.5.5"
    chk "the script names no network tool" bash -c '! grep -Ev "^[[:space:]]*#" "$1" | grep -Eq "(^|[^a-z])(curl|wget|nc|ssh|nats|telnet)[[:space:]]"' _ "$SCRIPT"
    # read-only folder: the script must refuse and change nothing
    printf '%s\n' "$REAL055" | put "$CFG/config.json"
    rm -f "$CFG/config.json.0.5.5"
    local before
    before=$(snap "$CFG")
    chmod 500 "$CFG"
    mig HOME="$H"
    chmod 700 "$CFG"
    chk "a folder we cannot write in: refused" refused
    chk "a folder we cannot write in: nothing changed" [ "$(snap "$CFG")" = "$before" ]
    chk "a folder we cannot write in: no stray files" bash -c '[ "$(ls -A "$1" | wc -l | tr -d " ")" = 1 ]' _ "$CFG"
}

case10() { # the real mash accepts the migrated file
    if [ ! -x /t/mash ]; then
        ok "skipped: MMIG_MASH not set (no check with the real mash)"
        return
    fi
    local e='no NATS url'
    printf '%s\n' "$REAL055" | put "$CFG/config.json"
    cp -p "$CFG/config.json" "$W/orig"
    # the unmigrated file
    env MASH_ENDPOINT=acme.dev.alice MASH_CONFIG_DIR="$CFG" HOME="$H" timeout 60 /t/mash status >"$W/old.out" 2>"$W/old.err" </dev/null
    # the refusal is required: internal/cliframe/legacy055.go prints
    #   error: <folder> holds a mash 0.5.5 configuration
    #   hint: <release base>/mash-migrate-0.5.5.sh
    # the folder is the configuration folder, with the home directory written as ~
    cat "$W/old.out" "$W/old.err" >"$W/old.all"
    chk "unmigrated 0.5.5 file: error: <folder> holds a mash 0.5.5 configuration" bash -c 'grep -Eq "^error: (~|/)[^ ]* holds a mash 0.5.5 configuration\$" "$1"' _ "$W/old.all"
    chk "unmigrated 0.5.5 file: a hint line names mash-migrate-0.5.5.sh" bash -c 'grep -Eq "^hint: .*mash-migrate-0.5.5\.sh\$" "$1"' _ "$W/old.all"
    mig HOME="$H"
    chk "migration: exit 0" rc_is 0
    env MASH_ENDPOINT=acme.dev.alice MASH_CONFIG_DIR="$CFG" HOME="$H" timeout 60 /t/mash status >"$W/new.out" 2>"$W/new.err" </dev/null
    cat "$W/new.out" "$W/new.err" >"$W/new.all"
    chk "migrated file: mash does not say it holds a 0.5.5 configuration" lacks "$W/new.all" 'mash 0.5.5 configuration'
    chk "migrated file: mash does not say there is no NATS url" lacks "$W/new.all" "$e"
    chk "migrated file: mash does not say no server is set" lacks "$W/new.all" 'no server is set'
    chk "unmigrated file, for contrast: mash does not read a server from it" bash -c 'cat "$1" "$2" | grep -Eq "no server is set|holds a mash 0.5.5 configuration"' _ "$W/old.out" "$W/old.err"
    {
        echo '  --- mash status on the migrated file (first lines)'
        head -n 8 "$W/new.all" | sed 's/^/  | /'
    } >"$W/info"
    cat "$W/info"
}

# case 11: a live server made by the setup script, mashctl init, a 0.5.5-shaped file, the migration, mash status
case11() {
    local d=$H/srv cfg=$H/live ep=acme.dev.migrator out
    if [ ! -x /t/mash ] || [ ! -x /t/mashctl ]; then
        bad "case 11 needs the mash and mashctl binaries"
        return
    fi
    cat /t/mash-local-nats-setup.sh >"$W/setup.sh"
    bash "$W/setup.sh" --owner acme --dir "$d" >"$W/out" 2>"$W/err" </dev/null
    RC=$?
    chk "the setup script made a local server" rc_is 0
    local who
    who=$(tail -n 1 "$W/out")
    chk "its last line is the mashctl init command" [ "$who" = "mashctl init --config $d/mash-setup.yaml" ]
    mkdir -p "$W/bin"
    ln -sf /t/mashctl "$W/bin/mashctl"
    ln -sf /t/mash "$W/bin/mash"
    export PATH="$W/bin:$PATH"
    mkdir -p "$cfg"
    chmod 700 "$cfg"
    MASH_CONFIG_DIR="$cfg" HOME="$H" mashctl init --config "$d/mash-setup.yaml" >"$W/out" 2>"$W/err" </dev/null
    RC=$?
    chk "mashctl init --config: exit 0" rc_is 0
    MASH_CONFIG_DIR="$cfg" HOME="$H" mashctl endpoint create "acme.dev.migrator" >"$W/out" 2>"$W/err" </dev/null
    RC=$?
    chk "mashctl endpoint create: exit 0" rc_is 0
    chk "init wrote servers and ca" bash -c 'grep -q "\"servers\"" "$1" && grep -q "\"ca\"" "$1"' _ "$cfg/config.json"
    # the 0.5.5 shape: url as one string, tls_ca
    python3 - "$cfg/config.json" <<'PY'
import json, sys
p = sys.argv[1]
c = json.load(open(p))
old = {}
for k, v in c.items():
    if k == "servers":
        old["url"] = ",".join(v)
    elif k == "ca":
        old["tls_ca"] = v
    else:
        old[k] = v
with open(p, "w") as f:
    json.dump(old, f, indent=2)
    f.write("\n")
PY
    chmod 600 "$cfg/config.json"
    chk "the 0.5.5-shaped file has url and tls_ca" bash -c 'grep -q "\"url\"" "$1" && grep -q "\"tls_ca\"" "$1"' _ "$cfg/config.json"
    cp -p "$cfg/config.json" "$W/orig55"
    mig MASH_CONFIG_DIR="$cfg" HOME="$H"
    chk "migration: exit 0" rc_is 0
    chk "migration: one line" lines_of "$W/out" 1
    chk "backup is the 0.5.5 file" same "$cfg/config.json.0.5.5" "$W/orig55"
    chk "migrated file has servers and ca, no url and no tls_ca" bash -c 'grep -q "\"servers\"" "$1" && grep -q "\"ca\"" "$1" && ! grep -q "\"url\"" "$1" && ! grep -q "tls_ca" "$1"' _ "$cfg/config.json"
    MASH_CONFIG_DIR="$cfg" HOME="$H" MASH_ENDPOINT=$ep timeout 90 /t/mash status >"$W/out" 2>"$W/err" </dev/null
    RC=$?
    cat "$W/out" "$W/err" >"$W/st.all"
    chk "mash status after the migration: exit 0" rc_is 0
    chk "mash status after the migration says ready" has "$W/st.all" ready
    out=$(head -n 12 "$W/st.all" | sed 's/^/  | /')
    printf '  --- mash status after the migration\n%s\n' "$out"
}

# refuse_case LABEL BODY [ERRTEXT HINTTEXT]: BODY goes through printf %b into config.json
refuse_case() {
    rm -f "$CFG/config.json.0.5.5"
    printf '%b' "$2" | put "$CFG/config.json"
    cp -p "$CFG/config.json" "$W/orig"
    mig HOME="$H"
    chk "$1: refused (exit 1, one error: and one hint: line)" refused
    chk "$1: config.json byte-identical" same "$CFG/config.json" "$W/orig"
    chk "$1: no backup and no temp file left" bash -c '[ "$(ls -A "$1")" = config.json ]' _ "$CFG"
    [ -z "${3:-}" ] || chk "$1: error text" has "$W/err" "error: $3"
    [ -z "${4:-}" ] || chk "$1: hint text" has "$W/err" "hint: $4"
}

case12() { # invalid JSON and empty url are refusals, not invalid output; temp names are not followed
    mkdir -p "$CFG"
    local je="$CFG/config.json is not valid JSON" jh="fix the file by hand, then run this again"
    refuse_case "trailing comma before }" '{"url":"a",}\n'
    refuse_case "mismatched closer ]" '{"url":"a"]\n'
    refuse_case "[}" '[}\n'
    refuse_case "}" '}\n'
    refuse_case "nested mismatch" '{"a":[1},"url":"x"}\n'
    refuse_case "trailing comma before ]" '{"a":[1,],"url":"x"}\n'
    refuse_case "raw tab inside the url value" '{"url":"a\tb"}\n'
    refuse_case "raw tab inside another string" '{"owner":"a\tb","url":"x"}\n'
    refuse_case "raw newline inside a string" '{"url":"a\nb"}\n'
    refuse_case "raw carriage return inside a string" '{"url":"a\rb"}\n'
    refuse_case "raw tab after a backslash inside a string" '{"url":"a\\\tb"}\n'
    refuse_case "bad escape \\q" '{"url":"a\\qb"}\n' "$je" "$jh"
    refuse_case "bad escape \\q in another string" '{"owner":"a\\qb","url":"x"}\n' "$je" "$jh"
    refuse_case "short unicode escape" '{"url":"a\\u12"}\n' "$je" "$jh"
    refuse_case "non-hex unicode escape" '{"url":"a\\uZZZZ"}\n' "$je" "$jh"
    # shellcheck disable=SC1003  # the backslash is the test input
    refuse_case "a lone backslash at the end" '{"url":"a\\' "$je" "$jh"
    refuse_case "unterminated object" '{"url":"a"\n'
    refuse_case "unterminated array" '{"url":"a","l":[1,2\n'
    refuse_case "unterminated string" '{"url":"a\n'
    refuse_case "empty url" '{"url":""}\n' "url is empty: nothing to migrate" "set servers by hand"
    refuse_case "blank url" '{"url":"   "}\n' "url is empty: nothing to migrate" "set servers by hand"
    refuse_case "commas-only url" '{"url":" , ,"}\n' "url is empty: nothing to migrate" "set servers by hand"
    refuse_case "empty url next to tls_ca" '{"url":"","tls_ca":"/c"}\n' "url is empty: nothing to migrate" "set servers by hand"
    # tabs, newlines and CRs OUTSIDE strings are whitespace
    rm -f "$CFG/config.json.0.5.5"
    printf '{\t"url":\t"tls://a:1"\r\n,\t"n":[1,\n2]\n}\n' | put "$CFG/config.json"
    mig HOME="$H"
    chk "tabs and newlines outside strings: exit 0" rc_is 0
    chk "tabs and newlines outside strings: kept as written" same "$CFG/config.json" <(printf '{\t"servers":\t["tls://a:1"]\r\n,\t"n":[1,\n2]\n}\n')
    # valid escapes and a raw UTF-8 letter stay accepted
    rm -f "$CFG/config.json.0.5.5"
    printf '{"note":"\\"q\\" \\\\ \\/ \\b\\f\\n\\r\\t \\u00e9 \\uD83D\\uDE00 \xc3\xa9","url":"tls://a:1"}\n' | put "$CFG/config.json"
    mig HOME="$H"
    chk "valid escapes (\\\" \\\\ \\/ \\b \\f \\n \\r \\t \\u00e9) and a raw UTF-8 letter: exit 0" rc_is 0
    chk "valid escapes: kept as written" same "$CFG/config.json" <(printf '{"note":"\\"q\\" \\\\ \\/ \\b\\f\\n\\r\\t \\u00e9 \\uD83D\\uDE00 \xc3\xa9","servers":["tls://a:1"]}\n')
    # grammar: a missing comma or colon, extra values, trailing text, bad literals
    refuse_case "missing comma between members" '{"url":"a" "owner":"b"}\n' "$je" "$jh"
    refuse_case "missing colon" '{"owner" "b","url":"a"}\n' "$je" "$jh"
    refuse_case "missing colon after url" '{"url" "a"}\n'
    refuse_case "two top-level values" '{"url":"a"}{"url":"b"}\n' "$je" "$jh"
    refuse_case "a second top-level value after the object" '{"url":"a"} 5\n' "$je" "$jh"
    refuse_case "trailing garbage after the object" '{"url":"a"} x\n' "$je" "$jh"
    refuse_case "bad literal tru" '{"url":"a","f":tru}\n' "$je" "$jh"
    refuse_case "bad literal nulll" '{"url":"a","f":nulll}\n' "$je" "$jh"
    refuse_case "bad number 1." '{"url":"a","n":1.}\n' "$je" "$jh"
    refuse_case "bad number -" '{"url":"a","n":-}\n' "$je" "$jh"
    refuse_case "missing value" '{"url":"a","n":}\n' "$je" "$jh"
    refuse_case "missing comma in an array" '{"url":"a","l":[1 2]}\n' "$je" "$jh"
    refuse_case "a key that is not a string" '{"url":"a",1:2}\n' "$je" "$jh"
    refuse_case "a colon in an array" '{"url":"a","l":[1:2]}\n' "$je" "$jh"
    refuse_case "empty file" '' "$je" "$jh"
    refuse_case "a bare word at top level" 'url\n' "$je" "$jh"
    # an escaped top-level key evades a raw-text comparison: refused
    local ke="a key in $CFG/config.json uses an escape: not a file this script wrote" kh="edit by hand: rename url to servers (a list) and tls_ca to ca"
    refuse_case "escaped top-level key servers beside url" '{"\\u0073ervers":["x"],"url":"a"}\n' "$ke" "$kh"
    refuse_case "escaped top-level key url" '{"ur\\u006c":"a","tls_ca":"/c"}\n' "$ke" "$kh"
    # nested keys and escapes in values stay allowed
    rm -f "$CFG/config.json.0.5.5"
    printf '{"limits":{"a\\u0062":1},"note":"x\\ny","url":"tls://a:1"}\n' | put "$CFG/config.json"
    mig HOME="$H"
    chk "an escaped nested key and an escaped value: exit 0" rc_is 0
    chk "an escaped nested key and an escaped value: kept as written" same "$CFG/config.json" <(printf '{"limits":{"a\\u0062":1},"note":"x\\ny","servers":["tls://a:1"]}\n')
}

case13() { # folder mode, unpredictable temp names, atomic backup, planted old names
    local m before pid
    mkdir -p "$CFG"
    for m in 777 775 757 770 702; do
        rm -f "$CFG/config.json.0.5.5"
        chmod 700 "$CFG"
        printf '%s\n' "$REAL055" | put "$CFG/config.json"
        cp -p "$CFG/config.json" "$W/orig"
        chmod "$m" "$CFG"
        mig HOME="$H"
        chmod 700 "$CFG"
        chk "folder mode $m: refused" refused
        chk "folder mode $m: error text" has "$W/err" "error: $CFG is writable by other users"
        chk "folder mode $m: hint text" has "$W/err" "hint: run chmod go-w on it, then run this again"
        chk "folder mode $m: config.json byte-identical, nothing else created" bash -c '[ "$(ls -A "$1")" = config.json ] && cmp -s "$1/config.json" "$2"' _ "$CFG" "$W/orig"
    done
    for m in 755 700 750 1755 1777; do
        rm -f "$CFG/config.json.0.5.5"
        chmod 700 "$CFG"
        printf '%s\n' "$REAL055" | put "$CFG/config.json"
        cp -p "$CFG/config.json" "$W/orig"
        printf '%s\n' "$EXP056" >"$W/exp"
        chmod "$m" "$CFG"
        mig HOME="$H"
        chk "folder mode $m: exit 0" rc_is 0
        chk "folder mode $m: migrated" same "$CFG/config.json" "$W/exp"
        chk "folder mode $m: only config.json and the backup remain" bash -c '[ "$(ls -A "$1")" = "config.json
config.json.0.5.5" ]' _ "$CFG"
        chk "folder mode $m: backup equals the original" same "$CFG/config.json.0.5.5" "$W/orig"
    done
    chmod 700 "$CFG"
    # a complete backup of a larger file
    rm -f "$CFG/config.json.0.5.5"
    { printf '{"url":"tls://a:1","pad":"'; head -c 300000 /dev/zero | tr '\0' 'x'; printf '","tls_ca":"/c"}\n'; } | put "$CFG/config.json"
    cp -p "$CFG/config.json" "$W/orig"
    mig HOME="$H"
    chk "large file: exit 0" rc_is 0
    chk "large file: backup equals the original byte for byte" same "$CFG/config.json.0.5.5" "$W/orig"
    chk "large file: backup mode 600" mode_is "$CFG/config.json.0.5.5" 600
    # the old predictable names (pid-based) are not used: plant symlinks under them, the migration ignores them
    rm -f "$CFG/config.json.0.5.5"
    printf '{"url":"tls://a:1"}\n' | put "$CFG/config.json"
    printf 'victim\n' >"$W/victim"
    cp -p "$W/victim" "$W/victim.orig"
    env -u MASH_CONFIG_DIR -u XDG_CONFIG_HOME HOME="$H" bash -c \
        'echo "$$" >"$1"; ln -s "$2" "$3/.config.json.migrate.$$"; ln -s "$2" "$3/.config.json.migrate.$$.st"; ln -s "$2" "$3/.config.json.$$.tmp"; ln -s "$2" "$3/.config.json.$$.st"; exec bash "$4"' \
        _ "$W/pid" "$W/victim" "$CFG" "$SCRIPT" >"$W/out" 2>"$W/err" </dev/null
    RC=$?
    pid=$(cat "$W/pid")
    chk "planted old names: exit 0" rc_is 0
    chk "planted old names: the file they pointed at is untouched" same "$W/victim" "$W/victim.orig"
    chk "planted old names: migrated" same "$CFG/config.json" <(printf '{"servers":["tls://a:1"]}\n')
    chk "planted old names: still four links, config.json and the backup, nothing else" bash -c '[ "$(ls -A "$1" | wc -l | tr -d " ")" = 6 ]' _ "$CFG"
    rm -f -- "$CFG/.config.json.migrate.$pid" "$CFG/.config.json.migrate.$pid.st" "$CFG/.config.json.$pid.tmp" "$CFG/.config.json.$pid.st"
}

run_case() {
    case $CASE in
    1) case1 ;; 2) case2 ;; 3) case3 ;; 4) case4 ;; 5) case5 ;; 6) case6 ;; 7) case7 ;;
    8) case8 ;; 9) case9 ;; 10) case10 ;; 11) case11 ;; 12) case12 ;; 13) case13 ;;
    *) echo "FAIL: c$CASE: no such case" ;;
    esac
}
run_case
printf 'done: c%s\n' "$CASE"
}

if [ "${MMIG_INSIDE:-}" = 1 ]; then
    inside_main "$@"
    exit 0
fi

# ======================================================================
# Part 2: the host side (podman only)
# ======================================================================
set -u
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P) || exit 2
SELF_TEST=$HERE/$(basename "${BASH_SOURCE[0]}")
SCRIPT_SRC=${MMIG_SCRIPT:-$HERE/mash-migrate-0.5.5.sh}
SETUP_SRC=$HERE/mash-local-nats-setup.sh
[ -f "$SCRIPT_SRC" ] || { echo "no $SCRIPT_SRC" >&2; exit 2; }
for t in podman sha256sum awk timeout; do
    command -v "$t" >/dev/null 2>&1 || { echo "needs $t" >&2; exit 2; }
done
[ "$(id -u)" -ne 0 ] || { echo "run as an ordinary user (rootless podman)" >&2; exit 2; }

SERVER=${MMIG_SERVER_TEST:-0}
if [ "$SERVER" = 1 ]; then
    command -v curl >/dev/null 2>&1 || { echo "needs curl" >&2; exit 2; }
    [ -n "${MMIG_MASHCTL:-}" ] && [ -f "$MMIG_MASHCTL" ] || { echo "MMIG_SERVER_TEST=1 needs MMIG_MASHCTL (a linux amd64 static binary)" >&2; exit 2; }
    [ -f "$SETUP_SRC" ] || { echo "no $SETUP_SRC" >&2; exit 2; }
fi
if [ -n "${MMIG_MASH:-}" ] && [ ! -f "$MMIG_MASH" ]; then echo "no $MMIG_MASH (MMIG_MASH)" >&2; exit 2; fi
CASES=${MMIG_CASES:-"1 2 3 4 5 6 7 8 9 10 12 13"}
[ -n "${MMIG_CASES:-}" ] || [ "$SERVER" != 1 ] || CASES="$CASES 11"
NCV=${MMIG_NATSCLI:-0.5.2}
# sha256 of the natscli v0.5.2 linux-amd64 zip, the same pin as mash-local-nats-setup_test.sh
NATPIN=9dddf92072bba154c11a08165718fb6ee42af1f25ce4f1bb284d2fc141abcff2
[ "$NCV" = 0.5.2 ] || NATPIN=${MMIG_NATSCLI_SHA256:-}
[ "$SERVER" != 1 ] || [ -n "$NATPIN" ] || { echo "natscli v$NCV has no pinned sha256; set MMIG_NATSCLI_SHA256" >&2; exit 2; }

MADE_SCRATCH=0
if [ -n "${MMIG_SCRATCH:-}" ]; then
    SCRATCH=$MMIG_SCRATCH
    mkdir -p "$SCRATCH" || exit 2
else
    SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/mmig.XXXXXX") || exit 2
    MADE_SCRATCH=1
fi
RUN=$(date +%m%d%H%M%S)-$$
REPORT=${MMIG_REPORT:-${TMPDIR:-/tmp}/mmig-receipt-$RUN.md}
CTX=$SCRATCH/build-$RUN
LOGS=$SCRATCH/logs-$RUN
MY_CONTAINERS=()
MY_IMAGE_IDS=()
CLEANED='not run'
BUILT_NOTE=''
SCRATCH_NOTE=''
PASS=0; FAIL=0
RESULTS=''
IMGLINES=''
NATSHA=''
START=$(date +%s)
MASHSHA=''
MASH_BIN=${MMIG_MASH:-}   # the binary mounted at /t/mash
MASH_BUILT=''             # recorded path of the binary this run built, if any
BIN_DIR=''                # recorded path of the directory it was built in
SCRIPTSHA=$(sha256sum "$SCRIPT_SRC" | awk '{print $1}')

write_receipt() {
    {
        echo "# mash-migrate-0.5.5.sh container test receipt"
        echo
        echo "- run id: $RUN"
        echo "- date (UTC): $(date -u +%Y-%m-%dT%H:%M:%SZ)"
        echo "- test: scripts/mash-migrate-0.5.5_test.sh, sha256 $(sha256sum "$SELF_TEST" | awk '{print $1}')"
        echo "- script under test: scripts/mash-migrate-0.5.5.sh, sha256 $SCRIPTSHA"
        echo "- cases: $CASES; MMIG_SERVER_TEST=$SERVER; mash binary: ${MASHSHA:-none}"
        [ "$SERVER" != 1 ] || echo "- natscli v$NCV linux-amd64 zip, sha256 $NATSHA (pinned: $NATPIN)"
        echo "- podman: $(podman --version 2>/dev/null)"
        echo "- awk in the image: mawk (Debian's default; POSIX awk). Not shown by this test: BSD awk, bash 3.2 and BSD"
        echo "  stat/cp/mv on a real macOS (the script uses awk, tail, tr, cp -p, mv, chmod only)."
        echo
        echo "## Images"
        echo
        printf '%s\n' "$IMGLINES"
        echo
        echo "## Totals"
        echo
        echo "- ok: $PASS"
        echo "- FAIL: $FAIL"
        echo "- wall time: $(($(date +%s) - START)) s"
        echo "- cleanup: $CLEANED"
        echo "- scratch: $SCRATCH_NOTE"
        echo
        echo "## Cases"
        echo
        printf '%s\n' "$RESULTS"
    } >"$REPORT" 2>/dev/null
}

cleanup() {
    local n i left=0
    trap - EXIT INT TERM
    for n in "${MY_CONTAINERS[@]+"${MY_CONTAINERS[@]}"}"; do podman rm -f "$n" >/dev/null 2>&1; done
    for i in "${MY_IMAGE_IDS[@]+"${MY_IMAGE_IDS[@]}"}"; do podman rmi -f "$i" >/dev/null 2>&1; done
    case $CTX in "$SCRATCH"/build-*) rm -rf -- "$CTX" ;; esac
    [ -z "$MASH_BUILT" ] || rm -f -- "$MASH_BUILT"
    [ -z "$BIN_DIR" ] || rmdir -- "$BIN_DIR" 2>/dev/null
    BUILT_NOTE=''
    [ -z "$MASH_BUILT" ] || { [ ! -e "$MASH_BUILT" ] && BUILT_NOTE="; built mash $MASH_BUILT removed" || BUILT_NOTE="; built mash $MASH_BUILT NOT removed"; }
    SCRATCH_NOTE='scratch dir given by MMIG_SCRATCH: kept'
    if [ "$MADE_SCRATCH" = 1 ]; then
        if [ "$FAIL" = 0 ] && [ "$PASS" -gt 0 ]; then
            rm -rf -- "$LOGS"
            rmdir -- "$SCRATCH" 2>/dev/null && SCRATCH_NOTE="made by this run ($SCRATCH): removed" ||
                SCRATCH_NOTE="made by this run ($SCRATCH): NOT empty, kept"
        else
            SCRATCH_NOTE="made by this run ($SCRATCH): kept for inspection (logs in $LOGS)"
        fi
    fi
    for n in "${MY_CONTAINERS[@]+"${MY_CONTAINERS[@]}"}"; do
        ! podman container exists "$n" 2>/dev/null || left=$((left + 1))
    done
    for i in "${MY_IMAGE_IDS[@]+"${MY_IMAGE_IDS[@]}"}"; do
        ! podman image exists "$i" 2>/dev/null || left=$((left + 1))
    done
    if [ "$left" = 0 ]; then
        CLEANED="done: ${#MY_CONTAINERS[@]} containers removed by name, ${#MY_IMAGE_IDS[@]} images by id, build dir removed${BUILT_NOTE}; none left"
    else
        CLEANED="INCOMPLETE: $left of this run's objects still exist"
    fi
    write_receipt
    echo "cleanup: $CLEANED"
    echo "receipt: $REPORT"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

mkdir -p "$CTX" "$LOGS" || exit 2
# cases 10 and 11 need a 0.5.6-era mash: unless MMIG_MASH is given, build one from this checkout
case " $CASES " in
*" 10 "* | *" 11 "*)
    if [ -z "$MASH_BIN" ]; then
        command -v go >/dev/null 2>&1 || { echo "FAIL: case 10 builds mash and needs go (or set MMIG_MASH)"; FAIL=$((FAIL + 1)); exit 1; }
        BIN_DIR=$SCRATCH/bin-$RUN
        mkdir -p "$BIN_DIR" || exit 2
        MASH_BUILT=$BIN_DIR/mash
        echo "== building mash from this checkout: $MASH_BUILT"
        (cd "$HERE/.." && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$MASH_BUILT" ./cmd/mash) \
            >"$LOGS/build-mash.log" 2>&1 || {
            echo "FAIL: go build of ./cmd/mash failed; see $LOGS/build-mash.log"; sed 's/^/  > /' "$LOGS/build-mash.log" | head -n 12
            FAIL=$((FAIL + 1)); exit 1; }
        MASH_BIN=$MASH_BUILT
    fi
    ;;
esac
[ -z "$MASH_BIN" ] || MASHSHA="sha256 $(sha256sum "$MASH_BIN" | awk '{print $1}')"
echo "== run $RUN: cases: $CASES"
TAG=debian:stable
cat >"$CTX/Containerfile" <<'CF'
ARG BASE
FROM ${BASE}
RUN useradd -m -s /bin/bash tester && mkdir -p /t && chmod 755 /t
CF
BASEIMG=localhost/mmig-$RUN-base
SRVIMG=localhost/mmig-$RUN-srv
echo "== building $BASEIMG from $TAG"
podman build -q --pull=missing --build-arg "BASE=$TAG" -t "$BASEIMG" -f "$CTX/Containerfile" "$CTX" \
    >"$LOGS/build-base.log" 2>&1 || { echo "FAIL: base image build; see $LOGS/build-base.log"; FAIL=$((FAIL + 1)); exit 1; }
MY_IMAGE_IDS+=("$(podman image inspect --format '{{.Id}}' "$BASEIMG")")
bdig=$(podman image inspect --format '{{.Digest}}' "$TAG" 2>/dev/null)
brd=$(podman image inspect --format '{{index .RepoDigests 0}}' "$TAG" 2>/dev/null)
IMGLINES="- $TAG: digest ${bdig:-?} (${brd:-no repo digest}); built image $BASEIMG id ${MY_IMAGE_IDS[0]}"
if [ "$SERVER" = 1 ]; then
    echo "== downloading natscli v$NCV"
    curl -fsSL -o "$CTX/nats.zip" \
        "https://github.com/nats-io/natscli/releases/download/v$NCV/nats-$NCV-linux-amd64.zip" || {
        echo "cannot download natscli v$NCV" >&2; FAIL=$((FAIL + 1)); exit 2; }
    NATSHA=$(sha256sum "$CTX/nats.zip" | awk '{print $1}')
    if [ "$NATSHA" != "$NATPIN" ]; then
        echo "FAIL: natscli v$NCV zip sha256 is $NATSHA, pinned $NATPIN" >&2
        FAIL=$((FAIL + 1)); exit 2
    fi
    echo "natscli sha256 matches the pin"
    cat >"$CTX/Containerfile.srv" <<'CF'
ARG BASE
FROM ${BASE}
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    nats-server openssl iproute2 procps curl unzip ca-certificates util-linux python3 \
    && rm -rf /var/lib/apt/lists/*
COPY nats.zip /tmp/nats.zip
RUN unzip -j -o /tmp/nats.zip '*/nats' -d /usr/local/bin && chmod 755 /usr/local/bin/nats && rm /tmp/nats.zip
CF
    echo "== building $SRVIMG"
    podman build -q --pull=never --build-arg "BASE=$BASEIMG" -t "$SRVIMG" -f "$CTX/Containerfile.srv" "$CTX" \
        >"$LOGS/build-srv.log" 2>&1 || { echo "FAIL: server image build; see $LOGS/build-srv.log"; FAIL=$((FAIL + 1)); exit 1; }
    sid=$(podman image inspect --format '{{.Id}}' "$SRVIMG")
    MY_IMAGE_IDS+=("$sid")
    IMGLINES="$IMGLINES
- server image $SRVIMG (adds nats-server from apt, natscli v$NCV, openssl, python3) id $sid"
fi

SETUPMNT=()
[ "$SERVER" != 1 ] || SETUPMNT=(-v "$SETUP_SRC:/t/mash-local-nats-setup.sh:ro")
for n in $CASES; do
    name=mmig-$RUN-c$n
    lg=$LOGS/c$n.log
    img=$BASEIMG
    [ "$n" != 11 ] || img=$SRVIMG
    MY_CONTAINERS+=("$name")
    echo "== case $n"
    timeout 600 podman run --rm --name "$name" --user tester --network none \
        --security-opt no-new-privileges --cap-drop=all --pids-limit 1024 \
        -e MMIG_INSIDE=1 \
        -v "$SELF_TEST:/t/test.sh:ro" -v "$SCRIPT_SRC:/t/mash-migrate-0.5.5.sh:ro" \
        ${MASH_BIN:+-v "$MASH_BIN:/t/mash:ro"} \
        ${MMIG_MASHCTL:+-v "$MMIG_MASHCTL:/t/mashctl:ro"} \
        ${SETUPMNT[@]+"${SETUPMNT[@]}"} \
        "$img" bash /t/test.sh "$n" >"$lg" 2>&1
    rc=$?
    podman rm -f "$name" >/dev/null 2>&1
    po=$(grep -c '^ok: ' "$lg")
    pf=$(grep -c '^FAIL: ' "$lg")
    if ! grep -q "^done: c$n\$" "$lg"; then
        pf=$((pf + 1))
        echo "FAIL: c$n did not finish (podman rc $rc)"
        RESULTS="$RESULTS
- FAIL c$n did not finish (podman rc $rc); log $lg"
    fi
    PASS=$((PASS + po)); FAIL=$((FAIL + pf))
    grep -E '^(FAIL|  )|^ok: .*skipped' "$lg"
    RESULTS="$RESULTS
$(grep -E '^(ok|FAIL): ' "$lg" | sed 's/^/- /')"
done

echo "== totals: ok=$PASS FAIL=$FAIL"
[ "$FAIL" = 0 ] && [ "$PASS" -gt 0 ]
