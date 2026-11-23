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
# It finds the folder the way mash does: an absolute $MASH_CONFIG_DIR (a relative
# one is refused, as mash refuses it); else an absolute $XDG_CONFIG_HOME/mash (a
# relative XDG_CONFIG_HOME is ignored); else $HOME/.config/mash.
#
# It refuses (exit 1, "error:" and "hint:" lines on stderr, nothing changed)
# when the folder is writable by group or other (unless sticky), when there is nothing to migrate, when the file already has "servers" or "ca"
# next to an old key, when "url" is not a string or holds no address, when the file is not valid JSON
# (any break in the JSON grammar: a missing comma or colon, a mismatched bracket,
# a comma before a closer, a control character or a bad escape inside a string,
# trailing text, an unterminated structure), when a top-level key uses an escape, or when the backup
# exists.
# Exit codes: 0 done, 1 refused, 2 usage.
#
# Plain bash, awk and the usual tools only (bash 3.2 and BSD awk are enough).

usage() {
    cat <<'EOF'
usage: mash-migrate-0.5.5.sh [-h|--help]

Rewrites the config.json that mash 0.5.5 left behind: url -> servers (a list),
tls_ca -> ca.  The old file is kept as config.json.0.5.5 beside it.  Nothing on
the server is touched.  The folder is $MASH_CONFIG_DIR, else
$XDG_CONFIG_HOME/mash, else $HOME/.config/mash.  A relative MASH_CONFIG_DIR is
refused; a relative XDG_CONFIG_HOME is ignored, as in mash.
EOF
}

refuse() { # MESSAGE HINT
    printf 'error: %s\n' "$1" >&2
    printf 'hint: %s\n' "$2" >&2
    exit 1
}

TMP=''
STAT=''
BTMP=''
BACKUP_MADE=''
cleanup() {
    [ -z "$TMP" ] || rm -f -- "$TMP"
    [ -z "$BTMP" ] || rm -f -- "$BTMP"
    [ -z "$STAT" ] || rm -f -- "$STAT"
    [ -z "$BACKUP_MADE" ] || rm -f -- "$BACKUP_MADE"
}

# The scanner.  It walks the file one character at a time, knowing strings and
# their escapes, and rewrites only a key token "url" or "tls_ca" that sits
# directly in the top-level object.  Facts and errors go to the file named by st.
# shellcheck disable=SC2016
AWK_PROG='
function fail(m) { print "err " m > st; close(st); bad = 1; exit 3 }
function litok(t) {
    if (t == "true" || t == "false" || t == "null") return 1
    return (t ~ /^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][-+]?[0-9]+)?$/)
}
function closer(c,   want) {
    want = (c == "}") ? "{" : "["
    if (depth < 1 || stk[depth] != want) fail("a closing " c " does not match what it closes")
    if (state == "V" || state == "K") {
        if (last == ",") fail("a comma directly before " c)
        fail("invalid")
    }
    depth--
    state = (depth == 0) ? "E" : "A"
}
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
BEGIN { depth = 0; instr = 0; esc = 0; last = ""; mode = 0; nurl = 0; nca = 0; state = "V"; intok = 0; uh = 0 }
{
    line = $0 "\n"
    n = length(line)
    for (i = 1; i <= n; i++) {
        c = substr(line, i, 1)
        if (instr) {
            if (c < " ") fail((esc || uh > 0) ? "invalid" : "a control character inside a string")
            if (uh > 0) {
                if (index("0123456789abcdefABCDEF", c) == 0) fail("invalid")
                uh--; tok = tok c; continue
            }
            if (esc) {
                if (index("\"\\/bfnrtu", c) == 0) fail("invalid")
                if (c == "u") uh = 4
                esc = 0; tok = tok c; continue
            }
            if (c == "\\") { esc = 1; tok = tok c; continue }
            if (c != "\"") { tok = tok c; continue }
            # the string ends here
            instr = 0
            if (isval) {
                out = out emit_list(tok)
                isval = 0; mode = 0; last = "\""; state = "A"
                continue
            }
            if (iskey) {
                if (depth == 1) {
                    if (index(tok, "\\")) fail("key escape")
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
                iskey = 0; state = "C"; last = "\""
                continue
            }
            out = out "\"" tok "\""
            last = "\""; state = (depth == 0) ? "E" : "A"
            continue
        }
        if (intok) {
            if (c ~ /[-+.0-9A-Za-z]/) { tokv = tokv c; out = out c; continue }
            if (!litok(tokv)) fail("invalid")
            intok = 0; state = (depth == 0) ? "E" : "A"
            i--; continue
        }
        if (mode == 1 || mode == 2) {
            # between the url key and its value: only space, then a colon, then a string
            if (c == " " || c == "\t" || c == "\n" || c == "\r") { out = out c; continue }
            if (mode == 1) {
                if (c != ":") fail("not a key and a value after url")
                out = out c; mode = 2; state = "V"; last = ":"; continue
            }
            if (c != "\"") fail("url is not a string")
            instr = 1; isval = 1; iskey = 0; tok = ""; mode = 0
            continue
        }
        if (c == " " || c == "\t" || c == "\n" || c == "\r") { out = out c; continue }
        if (state == "E") fail("invalid")
        if (state == "C") {
            if (c != ":") fail("invalid")
            state = "V"; out = out c; last = c
            continue
        }
        if (state == "A") {
            if (c == ",") { state = (stk[depth] == "{") ? "K" : "V"; out = out c; last = c; continue }
            if (c == "}" || c == "]") { closer(c); out = out c; last = c; continue }
            fail("invalid")
        }
        if (c == "}" || c == "]") { closer(c); out = out c; last = c; continue }
        if (state == "K" || state == "J") {
            if (c != "\"") fail("invalid")
            instr = 1; tok = ""; iskey = 1
            continue
        }
        # state V or W: a value
        if (c == "\"") { instr = 1; tok = ""; iskey = 0; continue }
        if (c == "{") { depth++; stk[depth] = "{"; state = "J"; out = out c; last = c; continue }
        if (c == "[") { depth++; stk[depth] = "["; state = "W"; out = out c; last = c; continue }
        if (c ~ /[-0-9tfn]/) { intok = 1; tokv = c; out = out c; last = "x"; continue }
        fail("invalid")
    }
}
END {
    if (bad) exit 3
    if (intok && !litok(tokv)) fail("invalid")
    if (instr || mode != 0 || state != "E") fail("invalid")
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
    if [ -n "${MASH_CONFIG_DIR:-}" ] && [ "${MASH_CONFIG_DIR#/}" = "$MASH_CONFIG_DIR" ]; then
        refuse "MASH_CONFIG_DIR is not an absolute path" \
            "set MASH_CONFIG_DIR to an absolute folder, or unset it"
    elif [ -n "${MASH_CONFIG_DIR:-}" ]; then
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

    # $file and $backup are the names messages show; every check, read and write uses the
    # names relative to the working directory, so a swapped parent path cannot redirect them.
    local file=$dir/config.json backup=$dir/config.json.0.5.5
    # Enter the folder once, physically (symlinks resolved).  From here the process's
    # working directory is the folder's own inode.  A folder that cannot be entered is,
    # as before, a file that is not there.
    # shellcheck disable=SC1007 # an empty CDPATH for this one cd, so it cannot search or print
    CDPATH= cd -P -- "$dir" 2>/dev/null || refuse "nothing to migrate: $file does not exist" \
        "this script is for a machine that ran mash 0.5.5"
    local rfile=./config.json rbackup=./config.json.0.5.5
    [ ! -L "$rfile" ] || refuse "$file is a symbolic link; not touching it" \
        "migrate the file it points to by hand"
    [ -f "$rfile" ] || refuse "nothing to migrate: $file does not exist" \
        "this script is for a machine that ran mash 0.5.5"
    [ -r "$rfile" ] || refuse "cannot read $file" "check its permissions"

    # a folder other users can write in is not one to put files in (a sticky one is fine)
    local perms
    # shellcheck disable=SC2012 # ls -ld is the portable way to read a mode
    perms=$(ls -ld -- ./. | awk '{print $1}')
    case "${perms:5:1}${perms:8:1}" in
    *w*)
        case "${perms:9:1}" in
        t | T) ;;
        *) refuse "$dir is writable by other users" "run chmod go-w on it, then run this again" ;;
        esac
        ;;
    esac

    trap cleanup EXIT
    local nonl=0
    if [ -s "$rfile" ] && [ -n "$(tail -c 1 "$rfile" | tr -d '\n')" ]; then nonl=1; fi

    umask 077
    local rc=0
    # unpredictable names, made by mktemp (GNU and BSD alike), each recorded for cleanup
    TMP=$(mktemp ./.mash-migrate.XXXXXX 2>/dev/null) || { TMP=''; refuse "cannot write in $dir" "check the folder's permissions"; }
    STAT=$(mktemp ./.mash-migrate.XXXXXX 2>/dev/null) || { STAT=''; refuse "cannot write in $dir" "check the folder's permissions"; }
    LC_ALL=C awk -v st="$STAT" -v nonl="$nonl" "$AWK_PROG" "$rfile" >"$TMP" || rc=$?
    if [ "$rc" != 0 ]; then
        local why
        why=$(sed -n 's/^err //p' "$STAT" 2>/dev/null | head -n 1)
        [ "$why" != "url is empty" ] || refuse "url is empty: nothing to migrate" "set servers by hand"
        case $why in
        invalid) refuse "$file is not valid JSON" "fix the file by hand, then run this again" ;;
        "key escape") refuse "a key in $file uses an escape: not a file this script wrote" \
            "edit by hand: rename url to servers (a list) and tls_ca to ca" ;;
        esac
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
    { [ ! -e "$rbackup" ] && [ ! -L "$rbackup" ]; } || refuse "$backup already exists; not overwriting it" \
        "move it aside, or remove it if you no longer need it, and run again"
    # The backup: a complete copy (keeping the mode) under a private name, then hard-linked
    # to the backup name.  ln does not replace anything, so a backup that appeared meanwhile
    # is refused, and the backup name never shows a partial file.
    BTMP=$(mktemp ./.mash-migrate.XXXXXX 2>/dev/null) || { BTMP=''; refuse "cannot write in $dir" "check the folder's permissions"; }
    cp -p "$rfile" "$BTMP" || refuse "cannot write the backup $backup" "check the folder's permissions"
    ln -- "$BTMP" "$rbackup" 2>/dev/null ||
        refuse "cannot make the backup $backup (it exists, or links are not allowed here)" \
            "move an existing backup aside and run again"
    BACKUP_MADE=$rbackup
    rm -f -- "$BTMP" || refuse "cannot remove the temporary copy $dir/${BTMP#./}" "check the folder's permissions"
    BTMP=''
    # From here the new file and the backup are no longer cleanup's to remove: an
    # interrupt after the replace must not delete the backup.  If the replace
    # itself fails, remove both by their recorded paths.
    local newfile=$TMP
    TMP=''
    BACKUP_MADE=''
    if ! mv -f "$newfile" "$rfile"; then
        rm -f -- "$newfile" "$rbackup"
        refuse "cannot replace $file" "check the folder's permissions"
    fi

    local what=''
    [ "$has_url" = 0 ] || what='url -> servers'
    [ "$has_ca_old" = 0 ] || what="${what:+$what, }tls_ca -> ca"
    printf 'Migrated %s: %s (old file kept as %s)\n' "$file" "$what" "$backup"
}

main "$@"
