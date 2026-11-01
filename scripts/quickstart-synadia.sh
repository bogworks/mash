#!/usr/bin/env bash
# quickstart-synadia.sh: set mash up on Synadia Cloud (free tier) from one API token.
#
# A demo/quickstart, not part of the mash binary (ADR 0038). mash itself never creates
# NATS users (ADR 0008); this script does that through Synadia's control-plane REST API
# (https://cloud.synadia.com/api/core/beta, undocumented and unstable: if a response is not
# shaped as expected, the script stops). Needs bash, curl, jq and a mash binary.
#
# What it does:
#   1. finds your team, system and account, and checks the free-tier stream limit
#      (2 shared streams + 1 per endpoint must fit); after `mash admin init` it also counts the
#      endpoints the mash directory already holds and stops if the account cannot take the plan;
#   2. creates ONE temporary admin NATS user, writes <config dir>/config.json (url, owner) and
#      runs `mash admin init` with the admin creds, then (if the plan fits) creates, for each
#      endpoint, a NATS user with the permissions `mash admin permissions <name>` prints (creds go
#      to <config dir>/<name>.creds, 0600) and runs `mash admin provision`;
#   4. sends a test message between the first two endpoints (if there are two);
#   5. deletes the temporary admin user when it ends, however it ends (EXIT trap).
#
# The API token comes from SYNADIA_API_TOKEN, one line on stdin (--token-stdin), or a prompt
# on a terminal. It is never taken from argv, never stored and never printed; curl gets it
# through a config file descriptor, so it does not appear in `ps`. No `set -x`.
#
# Guards of the old `mash init synadia` this script DROPS: the config.json file lock (do not
# run two at once), the check of stream usage by other tools (only the plan, plus the endpoints
# already in the mash directory, is checked against the limit), reuse of an existing same-name
# user or directory record (it refuses instead), the check that a reused user's permissions are exactly mash's, and cleanup of
# orphaned admin users from crashed runs (they are named mash-quickstart-admin-*; delete them
# in the Synadia console, see docs/synadia.md).
#
# Usage: quickstart-synadia.sh [--team T] [--account A] [--owner O] [--endpoint N]...
#          [--dry-run] [--mash PATH] [--config-dir DIR] [--force] [--token-stdin] [--no-test]
#   --config-dir must end in .config/mash (mash reads $HOME/.config/mash); default ~/.config/mash.
#   SYNADIA_API_URL overrides the API base URL (for tests). The bearer token goes to that URL, so only
#   https://HOST[:port][/path], http://127.0.0.1[:port][/path] and http://localhost[:port][/path] are accepted.
set -euo pipefail
umask 077

die() { echo "quickstart-synadia: $*" >&2; exit 1; }
API="${SYNADIA_API_URL:-https://cloud.synadia.com/api/core/beta}"
API="${API%/}"
# The token is sent to $API: refuse anything but https or a loopback http server (before the token is read).
case "$API" in
  https://*) [[ "$API" =~ ^https://[^/@?#[:space:]]+([/].*)?$ ]] || die "SYNADIA_API_URL '$API' is not a valid https URL" ;;
  http://*) [[ "$API" =~ ^http://(127\.0\.0\.1|localhost)(:[0-9]+)?([/].*)?$ ]] \
    || die "SYNADIA_API_URL '$API' refused: plain http is only allowed to 127.0.0.1 or localhost" ;;
  *) die "SYNADIA_API_URL '$API' refused: it must be an https URL (or http://127.0.0.1 / http://localhost)" ;;
esac
TEAM="" ACCOUNT="" OWNER="" MASH="mash" CONFIG_DIR="" DRY=0 FORCE=0 TOKEN_STDIN=0 NO_TEST=0
ENDPOINTS=()
ADMIN_ID="" TMP=""
CREATED_IDS=() CREATED_NAMES=() CREATED_CREDS=() PROVISIONED=() # endpoint users this run created (id, name, whether it wrote their creds file), endpoints provisioned
CFG_BACKUP="" CFG_CREATED=0 CFG_PENDING=0 # config.json: its backup, whether this run created it, whether a failure must undo the write

say() { echo "$*"; }

usage() { sed -n '2,/^set -euo/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'; }

while [ $# -gt 0 ]; do
  case "$1" in
    --team) TEAM="${2:?--team needs a value}"; shift 2 ;;
    --account) ACCOUNT="${2:?--account needs a value}"; shift 2 ;;
    --owner) OWNER="${2:?--owner needs a value}"; shift 2 ;;
    --endpoint) ENDPOINTS+=("${2:?--endpoint needs a value}"); shift 2 ;;
    --mash) MASH="${2:?--mash needs a value}"; shift 2 ;;
    --config-dir) CONFIG_DIR="${2:?--config-dir needs a value}"; shift 2 ;;
    --dry-run) DRY=1; shift ;;
    --force) FORCE=1; shift ;;
    --token-stdin) TOKEN_STDIN=1; shift ;;
    --no-test) NO_TEST=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument: $1 (try --help)" ;;
  esac
done

for tool in curl jq; do command -v "$tool" >/dev/null || die "$tool is required"; done
command -v "$MASH" >/dev/null || die "mash binary not found ($MASH); install it or pass --mash PATH"
MASH=$(command -v "$MASH") # an absolute path: the mash() function below must never resolve to itself

# ---- names and directories ------------------------------------------------------------------
sanitize() { # lower-case, [a-z0-9-], single hyphens, starts with a letter, at most 48 chars
  local s
  s=$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9' '-' | tr -s '-' | sed 's/^-*//; s/-*$//')
  case "$s" in [a-z]*) ;; *) s="x$s" ;; esac
  printf '%s' "${s:0:48}" | sed 's/-*$//'
}
valid_name() { [ "${#1}" -le 48 ] && [[ "$1" =~ ^[a-z][a-z0-9]*(-[a-z0-9]+)*$ ]]; }

USER_NAME="${USER:-$(id -un)}"
[ -n "$OWNER" ] || OWNER=$(sanitize "$USER_NAME")
if [ "${#ENDPOINTS[@]}" -eq 0 ]; then ENDPOINTS=("$(sanitize "$USER_NAME-$(hostname -s 2>/dev/null || echo host)")"); fi
valid_name "$OWNER" || die "owner '$OWNER' is not valid (1-48 lower-case letters, digits and single hyphens, starting with a letter); use --owner"
for n in "${ENDPOINTS[@]}"; do
  valid_name "$n" || die "endpoint name '$n' is not valid (1-48 lower-case letters, digits and single hyphens, starting with a letter)"
  case "$n" in mash-quickstart-admin*) die "endpoint name '$n' is reserved" ;; esac
done
[ "$(printf '%s\n' "${ENDPOINTS[@]}" | sort -u | wc -l)" -eq "${#ENDPOINTS[@]}" ] || die "an --endpoint is given twice"

REAL_HOME="${HOME:?HOME is not set}"
[ -n "$CONFIG_DIR" ] || CONFIG_DIR="$REAL_HOME/.config/mash"
case "$CONFIG_DIR" in
  */.config/mash) MASH_HOME="${CONFIG_DIR%/.config/mash}"; [ -n "$MASH_HOME" ] || MASH_HOME=/ ;;
  *) die "--config-dir must end in .config/mash (mash reads \$HOME/.config/mash)" ;;
esac
CONFIG="$CONFIG_DIR/config.json"

# mash reads these in every later session and they beat config.json: do not let them skew this run.
for v in MASH_URL MASH_OWNER MASH_CREDS MASH_NAME MASH_ADMIN_CREDS MASH_LAUNCH_TOKEN; do
  if [ -n "${!v:-}" ]; then say "note: ignoring $v from the environment for this run (set, it also overrides config.json in later sessions)"; fi
  unset "$v"
done
mash() { HOME="$MASH_HOME" "$MASH" "$@"; }

# ---- token (never argv, never stored, never echoed) -------------------------------------------
TOKEN="${SYNADIA_API_TOKEN:-}"
unset SYNADIA_API_TOKEN
if [ "$TOKEN_STDIN" -eq 1 ]; then
  IFS= read -r TOKEN || true
elif [ -z "$TOKEN" ] && [ -t 0 ]; then
  IFS= read -rsp "Synadia API token (not shown): " TOKEN </dev/tty || true; echo >&2
fi
[ -n "$TOKEN" ] || die "no Synadia API token: set SYNADIA_API_TOKEN, use --token-stdin, or run on a terminal"

# api METHOD PATH [JSON_BODY] prints the response body. On a non-2xx status it stops, naming the
# status and path only (a response body can hold secrets). The token goes in through a descriptor.
api() {
  local method="$1" path="$2" body="${3:-}" out status
  local args=(-sS --max-time 30 -X "$method" -H "Accept: application/json, text/plain, */*" -w $'\n%{http_code}')
  [ -z "$body" ] || args+=(-H "Content-Type: application/json" --data-binary "$body")
  out=$(curl "${args[@]}" -K <(printf 'header = "Authorization: Bearer %s"\n' "$TOKEN") "$API$path") \
    || die "$method $path: request failed"
  status="${out##*$'\n'}"
  case "$status" in
    2??) printf '%s' "${out%$'\n'*}" ;;
    401|403) die "$method $path: HTTP $status; the API token is wrong, expired or revoked" ;;
    *) die "$method $path: HTTP $status" ;;
  esac
}

# ---- cleanup: always delete the temporary admin user -------------------------------------------
cleanup() {
  local rc=$?
  trap - EXIT
  if [ -n "$ADMIN_ID" ]; then
    if (api DELETE "/nats-users/$ADMIN_ID" >/dev/null) 2>/dev/null; then
      say "Deleted the temporary admin user."
    else
      echo "quickstart-synadia: COULD NOT delete the temporary admin user (id $ADMIN_ID, name mash-quickstart-admin-*): delete it in the Synadia console." >&2
      [ "$rc" -ne 0 ] || rc=1
    fi
  fi
  if [ "$rc" -ne 0 ] && [ "${#CREATED_IDS[@]}" -gt 0 ]; then
    # A failed run deletes the endpoint users it created but did not provision (a retry would refuse their names);
    # users and creds of provisioned endpoints stay, they work.
    local i removed=() kept=() failed=()
    for i in "${!CREATED_IDS[@]}"; do
      if printf '%s\n' "${PROVISIONED[@]}" | grep -qxF "${CREATED_NAMES[$i]}"; then kept+=("${CREATED_NAMES[$i]}"); continue; fi
      if (api DELETE "/nats-users/${CREATED_IDS[$i]}" >/dev/null) 2>/dev/null; then
        removed+=("${CREATED_NAMES[$i]}")
        [ "${CREATED_CREDS[$i]}" -ne 1 ] || rm -f "$CONFIG_DIR/${CREATED_NAMES[$i]}.creds"
      else failed+=("${CREATED_NAMES[$i]} (id ${CREATED_IDS[$i]})"); fi
    done
    [ "${#removed[@]}" -eq 0 ] || say "Setup failed: removed the unprovisioned NATS user(s): ${removed[*]}."
    [ "${#kept[@]}" -eq 0 ] || say "Remaining, provisioned and working: ${kept[*]} (users, creds files and $CONFIG kept)."
    [ "${#failed[@]}" -eq 0 ] || echo "quickstart-synadia: COULD NOT delete the NATS user(s) ${failed[*]}: delete them in the Synadia console." >&2
  fi
  if [ "$rc" -ne 0 ] && [ "$CFG_PENDING" -eq 1 ]; then # setup failed before any endpoint was provisioned: undo the config.json write
    if [ -n "$CFG_BACKUP" ] && [ -f "$CFG_BACKUP" ]; then
      if mv -f "$CFG_BACKUP" "$CONFIG"; then say "Setup failed: restored the previous $CONFIG."
      else echo "quickstart-synadia: COULD NOT restore the previous $CONFIG; a copy is at $CFG_BACKUP" >&2; TMP=""; fi
    elif [ "$CFG_CREATED" -eq 1 ]; then rm -f "$CONFIG" && say "Setup failed: removed the $CONFIG this run created."; fi
  fi
  [ -z "$TMP" ] || rm -rf "$TMP"
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# ---- discover -----------------------------------------------------------------------------------
# pick NOUN SELECTOR JSON_ARRAY(id,name): the only item, or the one SELECTOR names (name or id).
pick() {
  local noun="$1" sel="$2" items="$3" n
  if [ -n "$sel" ]; then
    items=$(jq -c --arg s "$sel" '[.[] | select(.id == $s or .name == $s)]' <<<"$items")
  fi
  n=$(jq 'length' <<<"$items")
  if [ "$n" -eq 1 ]; then jq -c '.[0]' <<<"$items"; return; fi
  if [ "$n" -eq 0 ]; then die "no $noun matches ${sel:-}"; fi
  die "several ${noun}s are visible; name one with --$noun: $(jq -r '[.[] | "\(.name) (\(.id))"] | join(", ")' <<<"$items")"
}

teams=$(api GET /teams | jq -c '.items // error("no items")')
team=$(pick team "$TEAM" "$teams")
team_id=$(jq -r .id <<<"$team"); team_name=$(jq -r .name <<<"$team")

# Candidate accounts: every account of every system of the team that has R1 JetStream limits.
cands='[]'
sys_ids=$(api GET "/teams/$team_id/systems" | jq -r '.items[].id')
while IFS= read -r sys_id; do
  accs=$(api GET "/systems/$sys_id/accounts" | jq -c --arg sel "$ACCOUNT" '
    [.items[] | {id, name, public_key: .account_public_key, system: .system.name,
      url: .system.server_urls, streams: (.claims.nats.limits.tiered_limits.R1.streams // null)}
     | select($sel != "" or .streams != null)]')
  cands=$(jq -c --argjson a "$accs" '. + $a' <<<"$cands")
done <<<"$sys_ids"
[ "$(jq length <<<"$cands")" -gt 0 ] || die "no account with JetStream is visible to this token; mash needs one"
acct=$(pick account "$ACCOUNT" "$cands")
acct_id=$(jq -r .id <<<"$acct"); acct_name=$(jq -r .name <<<"$acct")
acct_key=$(jq -r .public_key <<<"$acct"); NATS_URL=$(jq -r .url <<<"$acct")
stream_limit=$(jq -r '.streams // "null"' <<<"$acct")
[ "$stream_limit" != "null" ] || die "account '$acct_name' has no JetStream (R1) limits; mash needs a JetStream-enabled account"
case "$acct_key" in A*) ;; *) die "account '$acct_name' reports no account public key" ;; esac
case "$NATS_URL" in ""|null) die "account '$acct_name' reports no server URL" ;; esac

# A signing-key group that is not scoped, not disabled and has its seed: users are created under it.
skg_id=$(api GET "/accounts/$acct_id/account-sk-groups" | jq -r '[.items[] | select(.is_scoped|not) | select(.disabled|not) | select(.missing_seed|not)][0].id // empty')
[ -n "$skg_id" ] || die "account '$acct_name' has no usable signing-key group (not scoped, not disabled, with its seed); users cannot be created"

say "Synadia: team '$team_name', system '$(jq -r .system <<<"$acct")', account '$acct_name'"
say "NATS URL: $NATS_URL"

# Capacity: 2 shared streams (directory and lease KV buckets) + 1 inbox per endpoint. Streams the account
# already holds are not counted (the old init asked the server; this script has no NATS client).
need=$((2 + ${#ENDPOINTS[@]}))
say "Plan: owner '$OWNER', ${#ENDPOINTS[@]} endpoint(s) (${ENDPOINTS[*]})"
say "Streams: 2 shared + ${#ENDPOINTS[@]} x 1 per endpoint = $need (the account allows $stream_limit)"
if [ "$stream_limit" -ge 0 ] && [ "$need" -gt "$stream_limit" ]; then
  die "this account allows $stream_limit streams and the plan needs $need; room for $((stream_limit > 2 ? stream_limit - 2 : 0)) endpoint(s). Nothing was created"
fi

# Config conflict, before anything is created.
if [ -e "$CONFIG" ]; then
  jq -e 'type == "object"' "$CONFIG" >/dev/null 2>&1 || die "$CONFIG is not a JSON object; fix it first"
  [ "$(jq -r '.creds // "" | tostring' "$CONFIG")" = "" ] || die "$CONFIG has a \"creds\" key, which would beat the new <name>.creds files; remove it"
  if [ "$FORCE" -ne 1 ]; then
    for kv in "url:$NATS_URL" "owner:$OWNER"; do
      cur=$(jq -r --arg k "${kv%%:*}" '.[$k] // "" | tostring' "$CONFIG")
      [ -z "$cur" ] || [ "$cur" = "${kv#*:}" ] || die "$CONFIG already has ${kv%%:*} '$cur', not '${kv#*:}'; use --force to overwrite it"
    done
  fi
fi

# An existing <name>.creds may be the only local copy of another account's (or a removed user's) creds.
for n in "${ENDPOINTS[@]}"; do
  if [ -e "$CONFIG_DIR/$n.creds" ] || [ -L "$CONFIG_DIR/$n.creds" ]; then
    die "$CONFIG_DIR/$n.creds already exists and would be overwritten; move it away or pick another --endpoint. Nothing was created"
  fi
done

if [ "$DRY" -eq 1 ]; then
  say ""
  say "Dry run: nothing is created. A real run would:"
  say "  - create a temporary admin user, and delete it at the end"
  for n in "${ENDPOINTS[@]}"; do say "  - create NATS user '$n' (creds in $CONFIG_DIR/$n.creds), then provision it"; done
  say "  - write $CONFIG (url $NATS_URL, owner $OWNER) and run 'mash admin init'"
  [ "${#ENDPOINTS[@]}" -lt 2 ] || [ "$NO_TEST" -eq 1 ] || say "  - send a test message from ${ENDPOINTS[0]} to ${ENDPOINTS[1]}"
  exit 0
fi

# Refuse same-name users instead of reusing them (a dropped guard).
existing=$(api GET "/accounts/$acct_id/nats-users" | jq -r '.items[].name')
for n in "${ENDPOINTS[@]}"; do
  if grep -qxF "$n" <<<"$existing"; then die "a NATS user named '$n' already exists in account '$acct_name'; delete it in the Synadia console or pick another --endpoint"; fi
done

# create_user NAME PUB_JSON SUB_JSON -> prints the user's JSON (id, user_public_key).
create_user() {
  local body
  body=$(jq -nc --arg n "$1" --arg g "$skg_id" --argjson pub "$2" --argjson sub "$3" \
    '{name: $n, sk_group_id: $g, jwt_settings: {pub: {allow: $pub}, sub: {allow: $sub}, subs: -1, data: -1, payload: -1}}')
  api POST "/accounts/$acct_id/nats-users" "$body"
}
# creds_user_key CREDS_TEXT prints the user public key (the "sub" claim) of the JWT in a .creds file.
creds_user_key() {
  local jwt pl pad
  jwt=$(printf '%s\n' "$1" | sed -n '/-----BEGIN NATS USER JWT-----/{n;p;q;}')
  pl=$(printf '%s' "$jwt" | cut -d. -f2 | tr '_-' '/+')
  case $(( ${#pl} % 4 )) in 2) pad="==" ;; 3) pad="=" ;; *) pad="" ;; esac
  printf '%s%s' "$pl" "$pad" | base64 -d 2>/dev/null | jq -r '.sub // empty' 2>/dev/null || true
}
# fetch_creds USER_ID DEST [EXPECT_KEY]: with EXPECT_KEY, refuses creds whose user public key is another one. Writes the creds file (umask 077, so mode 0600). Never replaces an existing DEST:
# it is written to a temp file beside it and linked into place, which fails if DEST appeared meanwhile.
fetch_creds() {
  local c t
  c=$(api POST "/nats-users/$1/creds" '{}')
  case "$c" in *"-----BEGIN NATS USER JWT-----"*) ;; *) die "the creds response for user $1 does not look like a .creds file" ;; esac
  if [ -n "${3:-}" ] && [ "$(creds_user_key "$c")" != "$3" ]; then
    die "the creds downloaded for user $1 belong to a different user public key than the one created ($3); refusing them"
  fi
  t=$(mktemp "$2.XXXXXX") || die "cannot create a temporary file beside $2"
  printf '%s\n' "$c" >"$t"
  chmod 600 "$t"
  if ! ln "$t" "$2" 2>/dev/null; then rm -f "$t"; die "$2 already exists (it appeared while the script ran); the creds for user $1 were not saved there"; fi
  rm -f "$t"
}

# ---- temporary admin user -------------------------------------------------------------------------
TMP=$(mktemp -d)
admin_name="mash-quickstart-admin-$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"
# shellcheck disable=SC2016 # the $ in NATS subjects is literal
admin=$(create_user "$admin_name" '["$JS.API.>","$KV.>","_INBOX.>"]' '["_INBOX.>","$JS.EVENT.>"]')
ADMIN_ID=$(jq -r '.id // empty' <<<"$admin")
[ -n "$ADMIN_ID" ] || die "creating the temporary admin user: the response has no id"
ADMIN_CREDS="$TMP/admin.creds"
fetch_creds "$ADMIN_ID" "$ADMIN_CREDS"
say "Created the temporary admin user $admin_name (deleted before this script ends)."

mkdir -p "$CONFIG_DIR"
chmod 700 "$CONFIG_DIR" 2>/dev/null || true
# ---- config.json (before the admin commands: they read url and owner from it) -------------------
tmpcfg=$(mktemp "$CONFIG_DIR/.config.json.XXXXXX")
if [ -e "$CONFIG" ]; then
  CFG_BACKUP="$TMP/config.json.bak"; cp -p "$CONFIG" "$CFG_BACKUP" || die "cannot back up $CONFIG"
else CFG_CREATED=1; fi
CFG_PENDING=1
if [ -e "$CONFIG" ]; then jq --arg u "$NATS_URL" --arg o "$OWNER" '. + {url: $u, owner: $o}' "$CONFIG" >"$tmpcfg"
else jq -n --arg u "$NATS_URL" --arg o "$OWNER" '{url: $u, owner: $o}' >"$tmpcfg"; fi
mv "$tmpcfg" "$CONFIG"
say "Wrote $CONFIG."

# ---- mash admin init and provision -------------------------------------------------------------------
export MASH_ADMIN_CREDS="$ADMIN_CREDS"
EXISTING=()

# streams_hint: the plan does not fit; say why and what to do. Exits non-zero (the trap deletes the admin user).
streams_hint() {
  local held=${#EXISTING[@]}
  {
    echo "quickstart-synadia: this account allows $stream_limit streams; it already holds the 2 shared buckets plus $held endpoint inbox(es)${EXISTING[*]:+ (${EXISTING[*]})}, and the plan needs ${#ENDPOINTS[@]} more."
    echo "Remove an endpoint you no longer use (docs/synadia.md, \"Removing an endpoint\"), or pass fewer --endpoint."
  } >&2
  exit 1
}
# mash_admin ARGS...: run `mash admin ARGS`, show its output, and turn a "maximum number of streams" failure into the hint.
mash_admin() {
  local out
  if out=$(mash admin "$@" 2>&1); then [ -z "$out" ] || printf '%s\n' "$out"; return 0; fi
  printf '%s\n' "$out" >&2
  case "$out" in *"maximum number of streams"*) streams_hint ;; esac
  die "mash admin $1 failed"
}

mash_admin init
listing=$(mash admin list)
# A requested name that already has a directory record is stale or taken: stop before any endpoint user exists.
for n in "${ENDPOINTS[@]}"; do
  while IFS=$'\t' read -r en _; do
    [ "$en" != "$n" ] || die "the mash directory already holds a record for '$n' (stale or taken): run 'mash admin deprovision $n' (docs/synadia.md, \"Removing an endpoint\") or pick another --endpoint. No endpoint user was created"
  done <<<"$listing"
done
# Endpoints already in the directory (not in this run's list) each hold an inbox stream.
while IFS=$'\t' read -r en _; do
  [ -n "$en" ] || continue
  printf '%s\n' "${ENDPOINTS[@]}" | grep -qxF "$en" || EXISTING+=("$en")
done <<<"$listing"
if [ "$stream_limit" -ge 0 ] && [ $(( ${#EXISTING[@]} + ${#ENDPOINTS[@]} + 2 )) -gt "$stream_limit" ]; then
  streams_hint
fi
# ---- endpoint users (after the capacity check, so a refused plan leaves none behind) ------------
for n in "${ENDPOINTS[@]}"; do
  perms=$(MASH_OWNER="$OWNER" mash admin permissions "$n" | jq -c '{pub: .pub.allow, sub: .sub.allow}') \
    || die "could not read the permissions from 'mash admin permissions $n'"
  u=$(create_user "$n" "$(jq -c .pub <<<"$perms")" "$(jq -c .sub <<<"$perms")")
  uid=$(jq -r '.id // empty' <<<"$u"); ukey=$(jq -r '.user_public_key // empty' <<<"$u")
  if [ -n "$uid" ]; then CREATED_IDS+=("$uid"); CREATED_NAMES+=("$n"); CREATED_CREDS+=(0); fi # tracked at once: a failure below deletes it
  [ -n "$uid" ] && [ -n "$ukey" ] || die "creating NATS user $n: the response has no id or user_public_key"
  fetch_creds "$uid" "$CONFIG_DIR/$n.creds" "$ukey"
  CREATED_CREDS[${#CREATED_CREDS[@]}-1]=1
  say "Endpoint $n: user created, creds in $CONFIG_DIR/$n.creds"
  mash_admin provision "$n" --user "$ukey" --account "$acct_key" --note "created by quickstart-synadia.sh"
  PROVISIONED+=("$n")
  CFG_PENDING=0 # an endpoint exists under this config: a later failure keeps it
done
unset MASH_ADMIN_CREDS

# The admin user is no longer needed.
if api DELETE "/nats-users/$ADMIN_ID" >/dev/null; then ADMIN_ID=""; say "Deleted the temporary admin user. No standing admin credential exists."; fi

# ---- test message -------------------------------------------------------------------------------------
if [ "$NO_TEST" -eq 1 ]; then say "Test mail: skipped (--no-test)."
elif [ "${#ENDPOINTS[@]}" -lt 2 ]; then say "Test mail: skipped, it needs two endpoints; add a second --endpoint."
else
  a="${ENDPOINTS[0]}" b="${ENDPOINTS[1]}"
  MASH_NAME="$a" mash send "$b" "quickstart-synadia.sh: test message, setup works." >/dev/null || die "everything was created, but sending the test message failed"
  inbox=$(MASH_NAME="$b" mash inbox) || die "everything was created, but reading $b's inbox failed"
  grep -q "verified" <<<"$inbox" || die "the test message did not arrive as [verified] in $b's inbox"
  say "Test mail: $a sent a message to $b, which read it as [verified]."
fi

say ""
say "Done. Start a session as an endpoint, for example:"
say "  MASH_NAME=${ENDPOINTS[0]} claude --channels plugin:mash@mash"
