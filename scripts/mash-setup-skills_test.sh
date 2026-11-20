#!/usr/bin/env bash
# Static checker for the skills-only plugin mash-setup/ and the marketplace.
# No network, no containers, nothing installed; scratch files live in a mktemp
# directory that this script removes itself. Prints ok:/FAIL: per check and a
# total; exits 1 on any FAIL.
#
# MASH_NATS_SETUP overrides the helper script whose --help is compared with
# the options the skills use (default scripts/mash-local-nats-setup.sh).
set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2
command -v jq >/dev/null 2>&1 || { echo "FAIL: jq is required"; exit 1; }

T="$(mktemp -d)" || exit 2
trap 'rm -rf "$T"' EXIT

PASS=0
NFAIL=0
ok() { PASS=$((PASS + 1)); echo "ok: $*"; }
fail() { NFAIL=$((NFAIL + 1)); echo "FAIL: $*"; }
note() { echo "note: $*"; }

MKT=.claude-plugin/marketplace.json
SETUP=mash-setup/skills/setup/SKILL.md
DAY2=mash-setup/skills/day-2/SKILL.md
SKILLS="setup day-2"
skillfile() { case "$1" in setup) echo "$SETUP" ;; *) echo "$DAY2" ;; esac; }

# ---------------------------------------------------------------- check 1
echo "== 1 marketplace and plugin layout"
if jq -e . "$MKT" >/dev/null 2>&1; then
  ok "marketplace.json is valid JSON"
else
  fail "marketplace.json is not valid JSON"
fi
names="$(jq -r '[.plugins[].name] | sort | join(",")' "$MKT" 2>/dev/null)"
if [ "$names" = "mash,mash-setup" ]; then
  ok "marketplace plugins are exactly mash and mash-setup"
else
  fail "marketplace plugins are '$names', want mash,mash-setup"
fi
for pair in mash:./plugin mash-setup:./mash-setup; do
  n="${pair%%:*}"; want="${pair#*:}"
  got="$(jq -r --arg n "$n" '.plugins[]|select(.name==$n)|.source' "$MKT" 2>/dev/null)"
  if [ "$got" = "$want" ]; then
    ok "marketplace source of $n is $want"
  else
    fail "marketplace source of $n is '$got', want $want"
  fi
  pj="$want/.claude-plugin/plugin.json"
  if [ ! -f "$pj" ]; then
    fail "$pj is missing"
    continue
  fi
  if ! jq -e . "$pj" >/dev/null 2>&1; then
    fail "$pj is not valid JSON"
    continue
  fi
  pn="$(jq -r '.name // empty' "$pj")"
  if [ "$pn" = "$n" ]; then
    ok "$pj name equals the marketplace entry name ($n)"
  else
    fail "$pj name is '$pn', want $n"
  fi
  for k in name version description; do
    if [ -n "$(jq -r --arg k "$k" '.[$k] // empty' "$pj")" ]; then
      ok "$pj has $k"
    else
      fail "$pj lacks $k"
    fi
  done
done
pj=mash-setup/.claude-plugin/plugin.json
extra="$(jq -r '[keys[] | select(test("^(hooks|mcpServers|monitors|agents|commands|settings|lspServers|outputStyles)$"))] | join(",")' "$pj" 2>/dev/null)"
if [ -z "$extra" ]; then
  ok "mash-setup plugin.json declares no components"
else
  fail "mash-setup plugin.json declares: $extra"
fi
top="$(find mash-setup -mindepth 1 -maxdepth 1 -printf '%f\n' | sort | tr '\n' ' ')"
if [ "$top" = ".claude-plugin skills " ]; then
  ok "mash-setup holds only .claude-plugin and skills"
else
  fail "mash-setup top level is: $top"
fi
bad="$(find mash-setup \( -name hooks -o -name hooks.json -o -name '.mcp.json' -o -name bin -o -name monitors -o -name agents -o -name commands -o -name 'settings*' \) | tr '\n' ' ')"
if [ -z "$bad" ]; then
  ok "mash-setup has no hooks, .mcp.json, bin, monitors, agents, commands or settings"
else
  fail "mash-setup has non-skill parts: $bad"
fi
sk="$(find mash-setup/skills -mindepth 1 -maxdepth 1 -printf '%f\n' | sort | tr '\n' ' ')"
if [ "$sk" = "day-2 setup " ]; then
  ok "skills are exactly day-2 and setup"
else
  fail "skills dir holds: $sk"
fi
others="$(find mash-setup/skills -type f ! -name SKILL.md | tr '\n' ' ')"
if [ -z "$others" ]; then
  ok "skills hold only SKILL.md files"
else
  fail "skills hold other files: $others"
fi

# ----------------------------------------------------- parse the skill files
# Per skill: $T/<s>/prose.txt (N:line, outside fences), $T/<s>/b<k> (first
# line "startline<TAB>info", then the block body) and $T/<s>/spans.txt
# (startline<TAB>inline-code text).
for s in $SKILLS; do
  f="$(skillfile "$s")"
  mkdir -p "$T/$s"
  [ -f "$f" ] || continue
  awk -v d="$T/$s" '
    BEGIN { inb = 0; k = 0; inspan = 0 }
    {
      if (inb) {
        if ($0 ~ /^```[[:space:]]*$/) { inb = 0; next }
        print $0 >> out; next
      }
      if ($0 ~ /^```/) {
        inb = 1; k++; out = d "/b" k
        info = $0; sub(/^```/, "", info)
        printf "%d\t%s\n", NR, info > out
        next
      }
      print NR ":" $0 >> (d "/prose.txt")
      n = split($0, parts, "`")
      for (i = 1; i <= n; i++) {
        if (i > 1) {
          inspan = !inspan
          if (!inspan) { printf "%d\t%s\n", sl, buf >> (d "/spans.txt"); buf = "" }
          else sl = NR
        }
        if (inspan) buf = buf parts[i]
      }
      if (inspan) buf = buf " "
    }
  ' "$f"
  : >>"$T/$s/prose.txt"; : >>"$T/$s/spans.txt"
done
blockinfo() { head -n1 "$1" | cut -f2; }
blockline() { head -n1 "$1" | cut -f1; }
blockbody() { tail -n +2 "$1"; }

# ---------------------------------------------------------------- check 2
echo "== 2 front matter"
for s in $SKILLS; do
  f="$(skillfile "$s")"
  if [ "$(head -n1 "$f")" != "---" ]; then fail "$f: no YAML front matter"; continue; fi
  fm="$(awk 'NR>1 && /^---[[:space:]]*$/ {exit} NR>1 {print}' "$f")"
  keys="$(printf '%s\n' "$fm" | grep -E '^[A-Za-z_-]+:' | cut -d: -f1 | sort | tr '\n' ' ')"
  if [ "$keys" = "description name " ]; then
    ok "$s: front matter has only name and description"
  else
    fail "$s: front matter keys are: $keys"
  fi
  nm="$(printf '%s\n' "$fm" | sed -n 's/^name:[[:space:]]*//p')"
  if [ "$nm" = "$s" ] && [ "$(basename "$(dirname "$f")")" = "$s" ]; then
    ok "$s: name equals its directory name"
  else
    fail "$s: name '$nm' vs directory '$(basename "$(dirname "$f")")'"
  fi
  desc="$(printf '%s\n' "$fm" | awk '/^description:/ {on=1; sub(/^description:[[:space:]]*(>-?|\|-?)?[[:space:]]*/, ""); print; next} on && /^[[:space:]]/ {print; next} on {on=0}' | tr '\n' ' ' | tr -s ' ' | tr -d '`')"
  printf '%s\n' "$desc" >"$T/$s/desc"
  if [ -n "${desc// /}" ]; then ok "$s: description is not empty"; else fail "$s: description is empty"; fi
  if printf '%s' "$desc" | grep -qi 'use when'; then
    ok "$s: description says when to use it"
  else
    fail "$s: description does not say 'Use when'"
  fi
done
sd="$(cat "$T/setup/desc")"; dd="$(cat "$T/day-2/desc")"
for p in 'not installed' 'no server' 'day-2'; do
  if printf '%s' "$sd" | grep -q "$p"; then ok "setup description says '$p'"; else fail "setup description lacks '$p'"; fi
done
for p in 'mash version works' 'setup skill'; do
  if printf '%s' "$dd" | grep -q "$p"; then ok "day-2 description says '$p'"; else fail "day-2 description lacks '$p'"; fi
done
# The trigger is the text before the pointer sentence ("If ...").
strig="${sd%% If *}"; dtrig="${dd%% If *}"
if printf '%s' "$strig" | grep -q 'mash version works'; then fail "setup trigger overlaps day-2 ('mash version works')"; else ok "setup trigger does not claim 'mash version works'"; fi
if printf '%s' "$dtrig" | grep -Eq 'not installed|no server'; then fail "day-2 trigger overlaps setup ('not installed'/'no server')"; else ok "day-2 trigger does not claim 'not installed'/'no server'"; fi

# ---------------------------------------------------------------- check 3
echo "== 3 style and safety"
for s in $SKILLS; do
  f="$(skillfile "$s")"
  before=$NFAIL
  while IFS= read -r l; do
    n="${l%%:*}"; t="${l#*:}"
    case "$t" in '|'*) continue ;; esac
    case "$t" in *http://* | *https://*) continue ;; esac
    if [ "${#t}" -gt 80 ]; then
      fail "$f:$n: prose line is ${#t} columns: $t"
    fi
  done <"$T/$s/prose.txt"
  [ "$NFAIL" = "$before" ] && ok "$s: prose lines are 80 columns or fewer"
  before=$NFAIL
  for b in "$T/$s"/b*; do
    [ -e "$b" ] || continue
    info="$(blockinfo "$b")"; ln="$(blockline "$b")"; body="$(blockbody "$b")"
    case "$info" in
      sh | "sh person" | "") ;;
      *) fail "$f:$ln: fence is \`\`\`$info (want sh, sh person or plain)" ;;
    esac
    case "$info" in sh | "sh person") isc=1 ;; *) isc=0 ;; esac
    if [ "$isc" = 1 ] && printf '%s\n' "$body" | grep -Eq '(^|[;&|[:space:]])exit([[:space:];&|]|$)'; then
      fail "$f:$ln: an sh block uses exit"
    fi
    if [ "$info" = sh ] && printf '%s\n' "$body" | grep -Eq '(^|[;&|[:space:]])sudo([[:space:]]|$)'; then
      fail "$f:$ln: sudo in a plain sh block"
    fi
    if printf '%s\n' "$body" | grep -Eq -e '--remove|endpoint delete|claude --dangerously'; then
      [ "$info" = "sh person" ] || fail "$f:$ln: destructive or dangerous block is not 'sh person'"
    fi
    if printf '%s\n' "$body" | grep -q -e '--yes'; then fail "$f:$ln: --yes in a block"; fi
    if [ "$isc" = 1 ] && printf '%s\n' "$body" | grep -Eq '(^|[;&|[:space:]])(pkill|killall)([[:space:]]|$)|pgrep[[:space:]]+-[a-zA-Z]*f'; then
      fail "$f:$ln: pkill/killall/pgrep -f in a command block"
    fi
  done
  [ "$NFAIL" = "$before" ] && ok "$s: fences, exit, sudo, person-only, --yes and pkill rules hold"
  if grep -nE '\|[[:space:]]*(bash|sh|sudo)([[:space:]]|$|`)|curl[^&;|]*\|' "$f" >"$T/pipe.txt"; then
    while IFS= read -r l; do fail "$f:$l (pipe into a shell or sudo)"; done <"$T/pipe.txt"
  else
    ok "$s: no pipe-to-shell"
  fi
done

# ---------------------------------------------------------------- check 4
echo "== 4 commands and options exist"
declare -A SPEC=([mash]=spec/mash.json [mashctl]=spec/mashctl.json)
nodeopts() { jq -r '.options[]?.name' <<<"$1"; }
check_cmd() { # where text
  local where="$1" text="$2" seg w i tool root node allowed resolved stopped tok sub o
  text="${text//&&/;}"; text="${text//||/;}"; text="${text//|/;}"
  while IFS= read -r seg; do
    # shellcheck disable=SC2206
    w=($seg)
    i=0
    while [ "$i" -lt "${#w[@]}" ] && [[ "${w[$i]}" =~ ^[A-Za-z_][A-Za-z0-9_]*= ]]; do i=$((i + 1)); done
    [ "$i" -lt "${#w[@]}" ] || continue
    tool="${w[$i]}"
    case "$tool" in mash | mashctl) ;; *) continue ;; esac
    root="$(jq -c '.command' "${SPEC[$tool]}")"
    node="$root"
    allowed="$(nodeopts "$root")"
    i=$((i + 1))
    resolved="$tool"; stopped=0
    while [ "$i" -lt "${#w[@]}" ]; do
      tok="${w[$i]}"
      case "$tok" in -*) break ;; esac
      i=$((i + 1))
      [ "$stopped" = 1 ] && continue
      sub="$(jq -c --arg n "$tok" '.commands[]? | select(.name==$n)' <<<"$node" | head -n1)"
      if [ -z "$sub" ]; then
        if [ "$(jq '(.commands // []) | length' <<<"$node")" != 0 ]; then
          fail "$where: '$resolved $tok' is not a command"
          continue 2
        fi
        stopped=1
        continue
      fi
      node="$sub"; resolved="$resolved $tok"
      allowed="$allowed"$'\n'"$(nodeopts "$node")"
    done
    for tok in "${w[@]}"; do
      case "$tok" in
        --*)
          o="${tok%%=*}"
          o="${o%%[\"\'\`,.)]*}"
          if ! grep -qxF -- "$o" <<<"$allowed"; then
            fail "$where: '$o' is not an option of '$resolved' (or of $tool)"
            continue 2
          fi ;;
      esac
    done
    ok "$where: '$resolved' exists with its options"
  done < <(tr ';' '\n' <<<"$text")
}
for s in $SKILLS; do
  f="$(skillfile "$s")"
  for b in "$T/$s"/b*; do
    [ -e "$b" ] || continue
    case "$(blockinfo "$b")" in sh | "sh person") ;; *) continue ;; esac
    ln="$(blockline "$b")"
    body="$(blockbody "$b" | sed -e ':a' -e '/\\$/N; s/\\\n//; ta')"
    while IFS= read -r line; do
      case "$line" in *mash-local-nats-setup.sh*) continue ;; esac
      check_cmd "$f:$ln" "$line"
    done <<<"$body"
  done
  while IFS=$'\t' read -r ln span; do
    case "$span" in *mash-local-nats-setup.sh*) continue ;; esac
    check_cmd "$f:$ln" "$span"
  done <"$T/$s/spans.txt"
done

# helper-script options
HELPER="${MASH_NATS_SETUP:-scripts/mash-local-nats-setup.sh}"
used="$(cat "$SETUP" "$DAY2" | grep -E 'mash-local-nats-setup\.sh' | grep -oE -e '--[a-z][a-z-]*' | sort -u | tr '\n' ' ')"
used="$(printf '%s\n' $used --dir --owner --start --stop --remove | sort -u | tr '\n' ' ')"
if [ -f "$HELPER" ]; then
  if bash "$HELPER" --help >"$T/helper.help" 2>&1; then
    for o in $used; do
      if grep -qE -e "$o([^a-z-]|\$)" "$T/helper.help"; then
        ok "helper script --help documents $o"
      else
        fail "helper script --help does not mention $o"
      fi
    done
  else
    fail "'bash $HELPER --help' failed: $(head -n2 "$T/helper.help" | tr '\n' ' ')"
  fi
else
  note "$HELPER is not in this tree (it is a release asset); not checked: $used (set MASH_NATS_SETUP to a copy)"
fi

# ---------------------------------------------------------------- check 5
echo "== 5 release asset names"
WANT_BASE=https://github.com/bogworks/mash/releases/latest/download
bases="$(cat "$SETUP" "$DAY2" | grep -oE 'https://github\.com/bogworks/mash/releases[^ "$]*' | sort -u | tr '\n' ' ')"
if [ "$bases" = "$WANT_BASE " ]; then
  ok "only the release base $WANT_BASE is referenced"
else
  fail "release URLs referenced: $bases"
fi
assets="$(cat "$SETUP" "$DAY2" | grep -oE '\$U/[A-Za-z0-9._-]+' | sed 's|^\$U/||' | sort -u | tr '\n' ' ')"
if [ "$assets" = "SHA256SUMS install.sh mash-local-nats-setup.sh " ]; then
  ok "downloaded assets are exactly SHA256SUMS, install.sh, mash-local-nats-setup.sh"
else
  fail "downloaded assets are: $assets"
fi
ubase="$(grep -oE 'U=[^ ;&]+' "$SETUP" | sort -u | tr '\n' ' ')"
if [ "$ubase" = "U=$WANT_BASE " ]; then ok "U= points at the latest-download base"; else fail "U= is: $ubase"; fi
gnames="$(grep -oE "grep -E '[^']*'" "$SETUP" | head -n1 | sed -E 's/.*\((.*)\).*/\1/' | tr '|' '\n' | sed 's/\\//g' | sort | tr '\n' ' ')"
if [ "$gnames" = "install.sh mash-local-nats-setup.sh " ]; then
  ok "the checksum grep names install.sh and mash-local-nats-setup.sh"
else
  fail "the checksum grep names: $gnames"
fi
if grep -Eq 'releases/download/|bogworks/mash[^ ]*\.(tar\.gz|tgz|zip|deb|rpm)' "$SETUP" "$DAY2"; then
  fail "a skill names a release archive or a versioned download"
else
  ok "no archive or versioned release download is referenced"
fi

# ---------------------------------------------------------------- check 6
echo "== 6 required sections and plugin facts"
for s in $SKILLS; do
  if grep -qE '^## Do not[[:space:]]*$' "$(skillfile "$s")"; then ok "$s: has a 'Do not' list"; else fail "$s: no '## Do not' heading"; fi
done
if grep -qE '^## Stop and ask' "$SETUP"; then ok "setup has 'Stop and ask'"; else fail "setup lacks '## Stop and ask'"; fi
hooks="$(jq -r '.hooks | keys[]' plugin/hooks/hooks.json | sort)"
for h in $hooks; do
  if grep -q "$h" "$SETUP"; then ok "setup names hook $h"; else fail "setup does not name hook $h"; fi
done
for h in PreToolUse PostToolUseFailure SubagentStop SessionEnd Notification PreCompact UserPromptExpansion; do
  if printf '%s\n' "$hooks" | grep -qx "$h"; then continue; fi
  if grep -qw "$h" "$SETUP"; then fail "setup names hook $h, which plugin/hooks/hooks.json does not declare"; fi
done
nh="$(printf '%s\n' "$hooks" | wc -l | tr -d ' ')"
if [ "$nh" = 3 ] && grep -qi 'three hooks' "$SETUP"; then
  ok "setup says 'three hooks' and hooks.json declares $nh"
else
  fail "hooks.json declares $nh hooks; setup must say 'three hooks'"
fi
ns="$(jq '.mcpServers | length' plugin/.mcp.json)"
if [ "$ns" = 1 ] && grep -q 'one MCP server' "$SETUP"; then
  ok "plugin/.mcp.json declares one server and setup says 'one MCP server'"
else
  fail ".mcp.json declares $ns servers; setup must say 'one MCP server'"
fi

# ---------------------------------------------------------------- check 7
echo "== 7 scripted-person dry check (informational)"
{
  for b in "$T/setup"/b*; do
    [ -e "$b" ] || continue
    if [ "$(blockinfo "$b")" = "sh person" ]; then
      printf '%05d\tPERSON-RUNS\t%s\n' "$(blockline "$b")" "$(blockbody "$b" | head -n1)"
    fi
  done
  grep -nE 'Needs a yes|Say yes' "$SETUP" | while IFS= read -r l; do
    printf '%05d\tYES-GATE\t%s\n' "${l%%:*}" "${l#*:}"
  done
} | sort -n | while IFS=$'\t' read -r n kind txt; do
  printf '  line %d  %-12s %s\n' "$((10#$n))" "$kind" "$txt"
done

# ---------------------------------------------------------------- check 8
echo "== 8 expected output quoted in the skills appears in the spec"
# spec_texts <tool> <command words...>: the output texts (x-output) of that spec command.
spec_texts() {
  local tool="$1"; shift
  jq -r --arg path "$*" '
    reduce ($path | split(" ")[]) as $w (.command;
      (.commands // [] | map(select(.name == $w)) | .[0]) // {})
    | ((.metadata // []) | map(select(.name == "x-output")) | .[0].value // [])[]
    | .text' "${SPEC[$tool]}"
}
# skill_pattern <text>: an ERE matching the quoted text, with the placeholders
# <n>, <N>, a lone N or M or any digits (a number), <server> (anything) and acme.demo.<name>.
skill_pattern() {
  # shellcheck disable=SC2016
  printf '%s' "$1" | sed -E -e 's/[0-9]+/@D@/g' -e 's/[][\.*^$(){}?+|\\/]/\\&/g' \
    -e 's/acme\\\.demo\\\.[a-z]+/acme\\.demo\\.[a-z]+/g' \
    -e 's/<[nN]>/[0-9]+/g' -e 's/<server>/.+/g' \
    -e 's/(^|[^A-Za-z])[NM]([^A-Za-z]|$)/\1[0-9]+\2/g' \
    -e 's/(^|[^A-Za-z])[NM]([^A-Za-z]|$)/\1[0-9]+\2/g' -e 's/@D@/[0-9]+/g'
}
# expect <skill> <line-key> <span-number> <tool> <command words...>
expect() {
  local skill="$1" key="$2" nth="$3" tool="$4" line text pat texts
  shift 4
  local f; f="$(skillfile "$skill")"
  line="$(grep -nF -e "$key" "$f" | head -n1)"
  if [ -z "$line" ]; then
    fail "$f: no line with '$key' (needed for the spec check of '$tool $*')"
    return
  fi
  text="$(printf '%s\n' "${line#*:}" | awk -F'`' -v n="$nth" '{print $(2*n)}')"
  texts="$(spec_texts "$tool" "$@")"
  if [ -z "$texts" ]; then
    fail "spec has no x-output for '$tool $*'"
    return
  fi
  if [ -z "$text" ]; then
    fail "$f:${line%%:*}: no quoted text (span $nth) on the line for '$tool $*'"
    return
  fi
  pat="$(skill_pattern "$text")"
  if printf '%s\n' "$texts" | grep -Eq -e "$pat"; then
    ok "$f:${line%%:*}: '$text' appears in the spec output of '$tool $*'"
  else
    fail "$f:${line%%:*}: '$text' is not in the spec output of '$tool $*' (spec says: $(printf '%s\n' "$texts" | head -n1))"
  fi
}
expect setup 'Expected: `Created endpoint' 1 mashctl endpoint create
expect setup 'Expected: `acme.demo.bob: ready' 1 mash status
expect setup 'Expected: `Mail sent' 1 mash send
expect day-2 'Expected: `Created endpoint' 1 mashctl endpoint create
# shellcheck disable=SC2016
expect day-2 '`mashctl status` is one line' 2 mashctl status

echo
echo "total: $PASS ok, $NFAIL FAIL"
[ "$NFAIL" = 0 ]
