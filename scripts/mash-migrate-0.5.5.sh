#!/usr/bin/env bash
# mash-migrate-0.5.5.sh: move a mash 0.5.5 config.json to the 0.5.6 key names.
#
#     curl -fsSL <release base>/mash-migrate-0.5.5.sh | bash
#
# Run it once.  It renames two top-level keys in config.json, in place:
# "url" becomes "servers" (a list; 0.5.5's url may hold several addresses
# separated by commas) and "tls_ca" becomes "ca".  It keeps the old file as
# config.json.0.5.5 beside it, prints one line saying what it changed, and
# touches nothing on the server.  Every other byte of the file stays as it was.
#
# It finds the folder the way mash does: an absolute $MASH_CONFIG_DIR; else an
# absolute $XDG_CONFIG_HOME/mash; else $HOME/.config/mash.
#
# It refuses (exit 1, "error:" and "hint:" lines on stderr, nothing changed)
# when there is nothing to migrate, when the file already has "servers" or "ca"
# next to an old key, when "url" is not a string or holds no address, when the file is not valid JSON
# (a mismatched or trailing bracket, a comma before a closer, a control character
# inside a string, an unterminated structure), or when the backup exists.
# Exit codes: 0 done, 1 refused, 2 usage.
#
# Plain bash, awk and the usual tools only (bash 3.2 and BSD awk are enough).

usage() {
    cat <<'EOF'
usage: mash-migrate-0.5.5.sh [-h|--help]

Rewrites the config.json that mash 0.5.5 left behind: url -> servers (a list),
tls_ca -> ca.  The old file is kept as config.json.0.5.5 beside it.  Nothing on
the server is touched.  The folder is $MASH_CONFIG_DIR, else
$XDG_CONFIG_HOME/mash, else $HOME/.config/mash (absolute paths only).
EOF
}

refuse() { # MESSAGE HINT
    printf 'error: %s\n' "$1" >&2
    printf 'hint: %s\n' "$2" >&2
    exit 1
}

TMP=''
STAT=''
BACKUP_MADE=''
cleanup() {
    [ -z "$TMP" ] || rm -f -- "$TMP"
    [ -z "$STAT" ] || rm -f -- "$STAT"
    [ -z "$BACKUP_MADE" ] || rm -f -- "$BACKUP_MADE"
}

# The scanner.  It walks the file one character at a time, knowing strings and
# their escapes, and rewrites only a key token "url" or "tls_ca" that sits
# directly in the top-level object.  Facts and errors go to the file named by st.
# shellcheck disable=SC2016
AWK_PROG='
function fail(m) { print "err " m > st; close(st); bad = 1; exit 3 }
function emit_list(raw,   n, parts, i, p, s, o) {
    n = split(raw, parts, ",")
    o = ""
    for (i = 1; i <= n; i++) {
        p = parts[i]
        gsub(/^[ \t]+/, "", p)
        gsub(/[ \t]+$/, "", p)
        if (p == "") continue
        o = o (o == "" ? "" : ", ") "\"" p "\""
    }
    if (o == "") fail("url is empty")
    return "[" o "]"
}
BEGIN { depth = 0; instr = 0; esc = 0; last = ""; mode = 0; top = ""; nurl = 0; nca = 0 }
{
    line = $0 "\n"
    n = length(line)
    for (i = 1; i <= n; i++) {
        c = substr(line, i, 1)
        if (instr) {
            if (c < " ") fail("a control character inside a string")
            if (esc) { esc = 0; tok = tok c; continue }
            if (c == "\\") { esc = 1; tok = tok c; continue }
            if (c != "\"") { tok = tok c; continue }
            # the string ends here
            instr = 0
            if (isval) {
                out = out emit_list(tok)
                isval = 0; mode = 0; last = "\""
                continue
            }
            if (depth == 1 && iskey) {
                if (tok == "url") {
                    if (nurl++) fail("url appears twice")
                    out = out "\"servers\""; mode = 1
                    print "url" > st
                } else if (tok == "tls_ca") {
                    if (nca++) fail("tls_ca appears twice")
                    out = out "\"ca\""; mode = 0
                    print "tls_ca" > st
                } else {
                    out = out "\"" tok "\""
                    if (tok == "servers") print "has_servers" > st
                    if (tok == "ca") print "has_ca" > st
                }
            } else {
                out = out "\"" tok "\""
            }
            iskey = 0; last = "\""
            continue
        }
        if (mode == 1 || mode == 2) {
            # between the url key and its value: only space, then a colon, then a string
            if (c == " " || c == "\t" || c == "\n" || c == "\r") { out = out c; continue }
            if (mode == 1) {
                if (c != ":") fail("not a key and a value after url")
                out = out c; mode = 2; continue
            }
            if (c != "\"") fail("url is not a string")
            instr = 1; isval = 1; tok = ""; mode = 0
            continue
        }
        if (c == "\"") {
            instr = 1; tok = ""
            iskey = (depth == 1 && top == "{" && (last == "{" || last == ","))
            continue
        }
        if (c == " " || c == "\t" || c == "\n" || c == "\r") { out = out c; continue }
        if (top == "") top = c
        if (c == "{" || c == "[") {
            depth++
            stk[depth] = c
        } else if (c == "}" || c == "]") {
            want = (c == "}") ? "{" : "["
            if (depth < 1 || stk[depth] != want) fail("a closing " c " does not match what it closes")
            if (last == ",") fail("a comma directly before " c)
            depth--
        }
        out = out c
        last = c
    }
}
END {
    if (bad) exit 3
    if (instr || depth != 0 || mode != 0) fail("config.json is not valid JSON")
    printf "%s", (nonl ? substr(out, 1, length(out) - 1) : out)
    close(st)
}
'

main() {
    case $# in
    0) ;;
    1)
        case $1 in
        -h | --help) usage; return 0 ;;
        esac
        usage >&2; return 2 ;;
    *) usage >&2; return 2 ;;
    esac

    local dir
    if [ -n "${MASH_CONFIG_DIR:-}" ] && [ "${MASH_CONFIG_DIR#/}" != "$MASH_CONFIG_DIR" ]; then
        dir=$MASH_CONFIG_DIR
    elif [ -n "${XDG_CONFIG_HOME:-}" ] && [ "${XDG_CONFIG_HOME#/}" != "$XDG_CONFIG_HOME" ]; then
        dir=${XDG_CONFIG_HOME%/}/mash
    elif [ -n "${HOME:-}" ]; then
        dir=${HOME%/}/.config/mash
    else
        refuse "cannot find the mash folder: HOME is not set" \
            "set MASH_CONFIG_DIR to the folder that holds config.json"
    fi
    while [ "${#dir}" -gt 1 ] && [ "${dir%/}" != "$dir" ]; do dir=${dir%/}; done

    local file=$dir/config.json backup=$dir/config.json.0.5.5
    [ ! -L "$file" ] || refuse "$file is a symbolic link; not touching it" \
        "migrate the file it points to by hand"
    [ -f "$file" ] || refuse "nothing to migrate: $file does not exist" \
        "this script is for a machine that ran mash 0.5.5"
    [ -r "$file" ] || refuse "cannot read $file" "check its permissions"

    trap cleanup EXIT
    TMP=$dir/.config.json.migrate.$$
    STAT=$dir/.config.json.migrate.$$.st
    local nonl=0
    if [ -s "$file" ] && [ -n "$(tail -c 1 "$file" | tr -d '\n')" ]; then nonl=1; fi

    umask 077
    local rc=0
    (set -C; : >"$TMP") 2>/dev/null || { TMP=''; refuse "cannot write in $dir" "check the folder's permissions"; }
    (set -C; : >"$STAT") 2>/dev/null || { STAT=''; refuse "cannot write in $dir" "check the folder's permissions"; }
    LC_ALL=C awk -v st="$STAT" -v nonl="$nonl" "$AWK_PROG" "$file" >"$TMP" || rc=$?
    if [ "$rc" != 0 ]; then
        local why
        why=$(sed -n 's/^err //p' "$STAT" 2>/dev/null | head -n 1)
        [ "$why" != "url is empty" ] || refuse "url is empty: nothing to migrate" "set servers by hand"
        refuse "cannot migrate $file: ${why:-the scanner failed}" \
            "fix config.json by hand: rename url to servers (a list) and tls_ca to ca"
    fi

    local has_url=0 has_ca_old=0 has_servers=0 has_ca=0 k
    while IFS= read -r k; do
        case $k in
        url) has_url=1 ;;
        tls_ca) has_ca_old=1 ;;
        has_servers) has_servers=1 ;;
        has_ca) has_ca=1 ;;
        esac
    done <"$STAT"
    if [ "$has_url$has_ca_old" = 00 ]; then
        refuse "nothing to migrate: $file has neither url nor tls_ca" \
            "it is already in the new form, or it is not from mash 0.5.5"
    fi
    if [ "$has_servers$has_ca" != 00 ]; then
        refuse "$file has servers or ca next to url or tls_ca; not guessing" \
            "remove the old key or the new key by hand, then run again"
    fi

    chmod 600 "$TMP" || refuse "cannot set the mode of the new file" "check the folder's permissions"
    # the backup: created exclusively (never over anything), then a copy that keeps the mode
    (set -C; : >"$backup") 2>/dev/null ||
        refuse "$backup already exists; not overwriting it" \
            "move it aside, or remove it if you no longer need it, and run again"
    BACKUP_MADE=$backup
    cp -p "$file" "$backup" || refuse "cannot write the backup $backup" "check the folder's permissions"
    # From here the new file and the backup are no longer cleanup's to remove: an
    # interrupt after the replace must not delete the backup.  If the replace
    # itself fails, remove both by their recorded paths.
    local newfile=$TMP
    TMP=''
    BACKUP_MADE=''
    if ! mv -f "$newfile" "$file"; then
        rm -f -- "$newfile" "$backup"
        refuse "cannot replace $file" "check the folder's permissions"
    fi

    local what=''
    [ "$has_url" = 0 ] || what='url -> servers'
    [ "$has_ca_old" = 0 ] || what="${what:+$what, }tls_ca -> ca"
    printf 'Migrated %s: %s (old file kept as %s)\n' "$file" "$what" "$backup"
}

main "$@"
