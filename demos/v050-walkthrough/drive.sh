#!/usr/bin/env bash
# drive.sh: run mash's v0.5.0 policy-model examples against a THROWAWAY local server and write one
# markdown report (command, list set, real output, exit code per example).
#
# usage: drive.sh [--shell|--narrate] --port N [--repo DIR] [--ref REF] [--out FILE] [--keep]
#   --shell    interactive walkthrough mode (for a human, in a herdr tab; no tmux): build, start the throwaway server,
#              add lead/l1/l2, adopt a generated submitter key, then open a shell whose mash/mashctl are the built ones,
#              with no live MASH_*/NATS_* variables. Server, temp dir and shell are torn down when the shell exits.
#              The report is not written in this mode.
#   --narrate  report mode PLUS live narration on the terminal (for the VHS walk-through): quiet setup, READY, then one
#              numbered video step per case with typed commands and their real output, then WALK-DONE.
#   --port N   REQUIRED (or env PORT): a free local port reserved with portwarden; never 4222. portwarden is an MCP
#              server only (no reserve/renew/release CLI), so the CALLER reserves the port with the portwarden tool,
#              renews it while the shell is open, and releases it afterwards (the script prints that reminder at exit).
#   --repo DIR git repository to take the source from (env REPO; default: the repository this script is in).
#   --ref REF  what to build (env REF; default HEAD). Only `git archive` reads the repo; nothing is written there.
#   --out FILE the report (env OUT; default out/v050-examples-report.md next to this script; out/ is not committed).
#   --keep     keep the temp dir (and print its path) instead of removing it at exit.
#
# Safety: every mash/mashctl/nats call goes through `iso`, which unsets EVERY MASH_* variable and points HOME and the
# config and key dirs inside the temp dir. `guard` re-checks the target is 127.0.0.1:$PORT before each network step.
# Capabilities of the build (policy diff/show/explain, policy serve, mash policy) are probed; an example that needs
# one that is missing prints `SKIPPED: needs <X>` and the script carries on, so it reruns unchanged on a later build.

set -u
exec 9>&2   # the original stderr (narrate mode sends stderr to a log; die still reaches the terminal)

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO=${REPO:-$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel 2>/dev/null)}
REF=${REF:-HEAD}
OUT=${OUT:-$SCRIPT_DIR/out/v050-examples-report.md}
PORT=${PORT:-}
KEEP=0
MODE=report
NARRATE=0

while [ $# -gt 0 ]; do
  case "$1" in
    --port) PORT=${2:-}; shift 2 ;;
    --port=*) PORT=${1#--port=}; shift ;;
    --repo) REPO=${2:-}; shift 2 ;;
    --repo=*) REPO=${1#--repo=}; shift ;;
    --ref) REF=${2:-}; shift 2 ;;
    --ref=*) REF=${1#--ref=}; shift ;;
    --out) OUT=${2:-}; shift 2 ;;
    --out=*) OUT=${1#--out=}; shift ;;
    --keep) KEEP=1; shift ;;
    --shell) MODE=shell; shift ;;
    --narrate) NARRATE=1; shift ;;
    -h|--help) sed -n '2,/^$/p' "$0"; exit 0 ;;
    *) echo "drive.sh: unknown argument: $1" >&2; exit 2 ;;
  esac
done
case "$OUT" in /*) ;; *) OUT=$PWD/$OUT ;; esac
mkdir -p "$(dirname "$OUT")" || { echo "drive.sh: cannot create the report directory" >&2; exit 2; }
[ "$NARRATE" = 1 ] && [ "$MODE" = shell ] && { echo "drive.sh: --narrate and --shell do not combine" >&2; exit 2; }

die() {
  echo "drive.sh: $*" >&9
  [ "$NARRATE" = 1 ] && [ -f "${T:-/nonexistent}/stderr.log" ] && tail -n 15 "$T/stderr.log" >&9
  exit 1
}

# ---- the port rules (before anything is created) ----
[ -n "$PORT" ] || die "refusing: no port. Pass --port N (or env PORT): a port reserved with portwarden, never a live server's port"
case "$PORT" in *[!0-9]*|'') die "refusing: --port must be a number" ;; esac
# Ports that must never be used: the live server's. 4222 (NATS) is always refused; MASH_DEMO_PROTECTED_PORTS="5222 6222" adds more.
for _p in 4222 ${MASH_DEMO_PROTECTED_PORTS:-}; do   # 4222 is always refused; the variable adds more
  [ "$PORT" -ne "$_p" ] || die "refusing: port $PORT is on the protected list (a live server's port)"
done
[ "$PORT" -ge 1024 ] && [ "$PORT" -le 65535 ] || die "refusing: port $PORT is outside 1024..65535"
if (exec 3<>"/dev/tcp/127.0.0.1/$PORT") 2>/dev/null; then
  die "refusing: something is already listening on 127.0.0.1:$PORT (not a free reserved port)"
fi
for t in git go jq python3 nats tar; do command -v "$t" >/dev/null 2>&1 || die "missing tool: $t"; done
git -C "$REPO" rev-parse --verify -q "$REF^{commit}" >/dev/null || die "ref $REF does not resolve in $REPO"
COMMIT=$(git -C "$REPO" rev-parse "$REF^{commit}")

# ---- temp dir and teardown ----
T=$(mktemp -d "${TMPDIR:-/tmp}/mash-policy.XXXXXX") || die "mktemp failed"   # TMPDIR chooses the scratch location
chmod 700 "$T"
if [ "$NARRATE" = 1 ]; then
  exec 2>"$T/stderr.log"   # setup chatter goes to a log; the terminal shows one line
  echo "Setting up a throwaway server from ${COMMIT:0:7} ..."
fi
SERVER_STARTED=0
SERVE_PID=""
declare -A APPLY_STATE

stop_pid() { # stop_pid PID WHY: only a process whose command line is under $T (so one this run started)
  local pid=$1 i
  [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null || return 0
  if ! ps -ww -o command= -p "$pid" 2>/dev/null | grep -qF "$T"; then   # ps, not /proc: the same on Linux and macOS
    echo "teardown: pid $pid is not a process started under $T; leaving it alone" >&2
    return 0
  fi
  kill "$pid" 2>/dev/null
  for i in 1 2 3 4 5 6 7 8 9 10; do kill -0 "$pid" 2>/dev/null || return 0; sleep 0.5; done
  kill -9 "$pid" 2>/dev/null
}

cleanup() {
  trap - EXIT INT TERM
  [ -n "$SERVE_PID" ] && stop_pid "$SERVE_PID"
  # runbook step 26: the pid init --local --start printed is in $T/server/server.pid
  if [ -s "$T/server/server.pid" ]; then stop_pid "$(tr -dc '0-9' <"$T/server/server.pid")"; fi
  if [ "$KEEP" = 1 ]; then echo "kept: $T" >&2; else rm -rf "$T"; fi
  if [ "$MODE" = shell ]; then
    echo "portwarden: this script cannot release the lease (portwarden is MCP-only). When you are done: release_port port=$PORT" >&2
    echo "            (and while the shell was open it needed renew_port port=$PORT before the lease expired)." >&2
  fi
}
trap cleanup EXIT
trap 'exit 130' INT TERM

mkdir -p "$T/home" "$T/state" "$T/cfg" "$T/keys" "$T/docs" "$T/ex" "$T/src" "$T/b" "$T/sign"
chmod 700 "$T/keys"

# ---- isolation ----
# Unset EVERY MASH_* variable that exists (and nats' own), then set only what the throwaway needs.
iso() {
  local x=""
  [ "${1:-}" = --exec ] && { shift; x=exec; }   # --exec: replace the (background) subshell so $! is the real process
  # shellcheck disable=SC2046
  $x env $(env | grep -o '^\(MASH\|NATS\)_[A-Za-z0-9_]*' | sed 's/^/-u /') \
      -u NATS_URL -u NATS_CREDS -u NATS_CONTEXT -u NATS_USER -u NATS_PASSWORD -u NATS_NKEY -u NATS_CA -u NATS_CERT -u NATS_KEY \
      HOME="$T/home" XDG_CONFIG_HOME="$T/home/.config" XDG_STATE_HOME="$T/home/.local/state" MASH_CONFIG_DIR="$T/cfg" MASH_KEYS_DIR="$T/keys" \
      MASH_POLICY_BUCKET='homerange_{range}_policy' "$@"
}
mashctl() { iso "$T/mashctl" "$@"; }
mash() { iso "$T/mash" "$@"; }
mash_as() { local n=$1 c=$2; shift 2; iso MASH_NAME="$n" MASH_CREDS="$c" "$T/mash" "$@"; }
natsq() { iso nats --server "nats://127.0.0.1:$PORT" --creds "$T/server/mashadmin.creds" "$@"; }

guard() {
  local u bad
  u=$(jq -r '.url // empty' "$T/cfg/config.json" 2>/dev/null)
  [ "$u" = "nats://127.0.0.1:$PORT" ] || die "refusing: not the throwaway server (configured url is '${u:-none}', expected nats://127.0.0.1:$PORT)"
  bad=$(iso env | grep '^MASH_' | grep -v -E '^MASH_(CONFIG_DIR|KEYS_DIR|POLICY_BUCKET)=')
  [ -z "$bad" ] || die "refusing: not the throwaway server (a MASH_ variable leaked into the isolated environment)"
}

# ---- build from a git archive; the repo working tree is never touched ----
echo "== source: $REF ($COMMIT)" >&2
git -C "$REPO" archive "$COMMIT" | tar -x -C "$T/src" || die "git archive failed"
(cd "$T/src" && go build -o "$T/mashctl" ./cmd/mashctl && go build -o "$T/mash" ./cmd/mash) || die "go build failed"

# ---- signing helper: always built inside the extracted source copy (usercreds needs internal/admin and internal/sap) ----
mkdir -p "$T/src/cmd/signdoc"
cat >"$T/src/cmd/signdoc/main.go" <<'EOF'
package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"MASHMOD/internal/admin"
	"MASHMOD/internal/sap"
)

// usercreds ACCOUNT_SEEDFILE USER_SEEDFILE agent.owner.name: a .creds file on stdout for a new-form endpoint, signed
// by the account key itself (as the repo's own user minting does: no IssuerAccount, no signing key), with the
// permissions admin.NewFormPermissions gives that identity.
func usercreds(acctFile, userFile, name string) error {
	ab, err := os.ReadFile(acctFile)
	if err != nil {
		return err
	}
	akp, err := nkeys.FromSeed([]byte(strings.TrimSpace(string(ab))))
	if err != nil {
		return err
	}
	ub, err := os.ReadFile(userFile)
	if err != nil {
		return err
	}
	seed := []byte(strings.TrimSpace(string(ub)))
	ukp, err := nkeys.FromSeed(seed)
	if err != nil {
		return err
	}
	upub, err := ukp.PublicKey()
	if err != nil {
		return err
	}
	id, err := sap.ParseCanonical(name)
	if err != nil {
		return err
	}
	perms, err := admin.NewFormPermissions(id, nil)
	if err != nil {
		return err
	}
	uc := jwt.NewUserClaims(upub)
	uc.Name = name
	uc.Permissions = perms
	tok, err := uc.Encode(akp)
	if err != nil {
		return err
	}
	creds, err := jwt.FormatUserConfig(tok, seed)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(creds)
	return err
}

// listsetcreds GROUPSEEDFILE ACCOUNT_PUB USERSEEDFILE address tag,tag...: a .creds file on stdout shaped as the issue path
// makes it (internal/issue/issuer.go): signed by the owner group's scoped signing key, IssuerAccount set to the account, no
// permissions of its own (the scoped key's template, pushed by the apply, grants the subjects), carrying the tags. Made
// by this demo helper from the key store's group key; it is NOT recorded anywhere and no wrangler countersigned it.
func listsetcreds(groupFile, acct, userFile, name, tags string) error {
	gb, err := os.ReadFile(groupFile)
	if err != nil {
		return err
	}
	gkp, err := nkeys.FromSeed([]byte(strings.TrimSpace(string(gb))))
	if err != nil {
		return err
	}
	ub, err := os.ReadFile(userFile)
	if err != nil {
		return err
	}
	seed := []byte(strings.TrimSpace(string(ub)))
	ukp, err := nkeys.FromSeed(seed)
	if err != nil {
		return err
	}
	upub, err := ukp.PublicKey()
	if err != nil {
		return err
	}
	uc := jwt.NewUserClaims(upub)
	uc.Name = name
	uc.IssuerAccount = acct
	uc.UserPermissionLimits = jwt.UserPermissionLimits{}
	for _, t := range strings.Split(tags, ",") {
		if t != "" {
			uc.Tags.Add(t)
		}
	}
	tok, err := uc.Encode(gkp)
	if err != nil {
		return err
	}
	creds, err := jwt.FormatUserConfig(tok, seed)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(creds)
	return err
}

// genuser FILE: as gen, but a user-type key.
// gen FILE: write a new account-type nkey seed to FILE (mode 600), print only the public key.
// sign SEEDFILE PAYLOADFILE: print the compact JWS (EdDSA) of the payload, as policydoc's sign() does.
func main() {
	if len(os.Args) == 5 && os.Args[1] == "usercreds" {
		if err := usercreds(os.Args[2], os.Args[3], os.Args[4]); err != nil {
			fmt.Fprintln(os.Stderr, "signdoc usercreds:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 7 && os.Args[1] == "listsetcreds" {
		if err := listsetcreds(os.Args[2], os.Args[3], os.Args[4], os.Args[5], os.Args[6]); err != nil {
			fmt.Fprintln(os.Stderr, "signdoc listsetcreds:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && (os.Args[1] == "gen" || os.Args[1] == "genuser") {
		var kp nkeys.KeyPair
		var err error
		if os.Args[1] == "genuser" {
			kp, err = nkeys.CreateUser()
		} else {
			kp, err = nkeys.CreateAccount()
		}
		if err != nil {
			panic(err)
		}
		seed, _ := kp.Seed()
		pub, _ := kp.PublicKey()
		if err := os.WriteFile(os.Args[2], append(seed, '\n'), 0o600); err != nil {
			panic(err)
		}
		fmt.Print(pub)
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "pub" { // pub SEEDFILE: print the public key of a seed file
		seed, err := os.ReadFile(os.Args[2])
		if err != nil {
			panic(err)
		}
		kp, err := nkeys.FromSeed([]byte(strings.TrimSpace(string(seed))))
		if err != nil {
			panic(err)
		}
		pub, _ := kp.PublicKey()
		fmt.Print(pub)
		return
	}
	seed, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	payload, err := os.ReadFile(os.Args[2])
	if err != nil {
		panic(err)
	}
	kp, err := nkeys.FromSeed([]byte(strings.TrimSpace(string(seed))))
	if err != nil {
		panic(err)
	}
	e := base64.RawURLEncoding
	in := e.EncodeToString([]byte(`{"alg":"EdDSA","typ":"JWT"}`)) + "." + e.EncodeToString(payload)
	sig, _ := kp.Sign([]byte(in))
	fmt.Print(in + "." + e.EncodeToString(sig))
}
EOF
# the helper imports the selected repository's own packages: take the module path from its go.mod
MASHMOD=$(awk '$1=="module"{print $2; exit}' "$T/src/go.mod")
[ -n "$MASHMOD" ] || die "no module line in $T/src/go.mod"
sed "s|MASHMOD|$MASHMOD|g" "$T/src/cmd/signdoc/main.go" >"$T/src/cmd/signdoc/main.go.tmp" && mv "$T/src/cmd/signdoc/main.go.tmp" "$T/src/cmd/signdoc/main.go"
(cd "$T/src" && go build -o "$T/signdoc" ./cmd/signdoc) || die "could not build the signing helper"
SIGN_HOW="built inside the extracted source copy (it needs the repo's internal packages for usercreds; the repo itself untouched)"
echo "== signer: $SIGN_HOW" >&2

# ---- stand up the throwaway server ----
echo "== init --local --start on 127.0.0.1:$PORT" >&2
mashctl init --local --dir "$T/server" --owner lab --port "$PORT" --start >"$T/init.log" 2>&1
INIT_RC=$?
[ -s "$T/server/server.pid" ] && SERVER_STARTED=1
[ "$INIT_RC" -eq 0 ] || { sed 's/^/  init: /' "$T/init.log" >&2; die "init --local --start failed (exit $INIT_RC)"; }
# verify: the configured URL is the throwaway, and the target mashctl reports for a real call is too
guard
TARGET=$(mashctl list 2>&1 >/dev/null | grep -o 'target server [^ ]*' | head -1)
[ "$TARGET" = "target server nats://127.0.0.1:$PORT" ] || die "refusing: not the throwaway server (mashctl targets '${TARGET:-nothing}')"
echo "== verified target: nats://127.0.0.1:$PORT" >&2

natsq kv add homerange_lab_policy --history 8 >"$T/bucket.log" 2>&1 || die "could not create the policy bucket (see $T/bucket.log)"
ls "$T/server" >"$T/server-files.txt"

# ---- setup the examples need before adoption ----
# Every sender the documents name must already have its inbox when the list set is applied (`mashctl policy serve` checks
# it and makes nothing: ADR 0122; internal/policyapply, cmd/mashctl/policy_serve.go policyServeProvisioner), and after
# adoption `mashctl add` and `provision` are refused. So, BEFORE adoption, each address range.owner.name is provisioned:
#   lab.h1.{l1,l2,lead}  lab.ops.{alice,bob}  lab.svc.{queue-alarm,swamp-update}   by `mashctl provision NAME --agent lab --owner OWNER
#       --user KEY --account ACCT` (MASH_AGENT_TOKEN=lab for that call only), the three-token form shell mode uses for new1
#       (a directory record and inbox for range.owner.name; the user key is a generated, never-used key; no creds file)
#   (a legacy `mashctl add` with owner h1/ops/svc is refused once a new-form endpoint of that owner exists, and the other
#   way round: "owner equals a new-form owner ... this legacy wildcard filter would reach it", so legacy add is not an option)
#   stray (owner lab)                                                 by `mashctl add stray`, named by no document: what the
#       reconcile plans to revoke
: >"$T/setup.log"
PROVISIONED=""
ACCT0=$("$T/signdoc" pub "$T/keys/account.nk" 2>/dev/null)
[ -n "$ACCT0" ] || die "could not read the account key"
for a in h1:l1 h1:l2 h1:lead ops:alice ops:bob svc:queue-alarm svc:swamp-update; do
  o=${a%%:*}; n=${a##*:}
  U=$("$T/signdoc" genuser "$T/prov-$n.seed"); chmod 600 "$T/prov-$n.seed"
  iso MASH_AGENT_TOKEN=lab "$T/mashctl" provision "$n" --agent lab --owner "$o" --user "$U" --account "$ACCT0" >>"$T/setup.log" 2>&1 \
    || die "mashctl provision $n --agent lab --owner $o failed (see $T/setup.log)"
  PROVISIONED="$PROVISIONED lab.$o.$n"
done
mashctl add stray >>"$T/setup.log" 2>&1 || die "mashctl add stray failed (see $T/setup.log)"
if [ "$MODE" = shell ]; then
  # One endpoint in the new three-token form (acme.team1.new1): legacy `add` records have no address, so the client answers
  # `mash policy show` for them locally; this one reaches the policy responder. agent_token is set for this call only.
  NEWU=$("$T/signdoc" genuser "$T/new1.seed"); chmod 600 "$T/new1.seed"
  ACCT0=$("$T/signdoc" pub "$T/keys/account.nk" 2>/dev/null)
  iso MASH_AGENT_TOKEN=acme "$T/mashctl" provision new1 --agent acme --owner team1 --user "$NEWU" --account "$ACCT0" >>"$T/setup.log" 2>&1 \
    || die "mashctl provision new1 --agent acme failed (see $T/setup.log)"
  # mashctl has no verb that writes a creds file for a three-token endpoint: mint one with the demo helper, before adoption.
  ( umask 077; "$T/signdoc" usercreds "$T/keys/account.nk" "$T/new1.seed" acme.team1.new1 >"$T/cfg/new1.creds" ) \
    || die "signdoc usercreds failed"
  chmod 600 "$T/cfg/new1.creds"
fi

# A new-form (three-token) credential for lab.h1.l1, minted BEFORE adoption by the demo helper: the account key signs a user
# JWT for the user key lab.h1.l1 was provisioned with (prov-l1.seed, mode 600, never printed), with
# admin.NewFormPermissions for lab.h1.l1. mashctl has no verb that writes it, and the list-set apply issues no creds file
# (it records issued keys: internal/policyapply/issue.go), so this is the only way to get an endpoint that reaches the
# policy responder as lab.h1.l1. It is NOT what the list-set issue path would mint (no list-set tags).
( umask 077; "$T/signdoc" usercreds "$T/keys/account.nk" "$T/prov-l1.seed" lab.h1.l1 >"$T/cfg/l1-newform.creds" ) || die "could not mint the new-form credential for lab.h1.l1"
chmod 600 "$T/cfg/l1-newform.creds"
mash_l1() { iso MASH_NAME=l1 MASH_AGENT_TOKEN=lab MASH_OWNER=h1 MASH_CREDS="$T/cfg/l1-newform.creds" "$T/mash" "$@"; }

SUB_PUB=$("$T/signdoc" gen "$T/sub.seed")           # the submitter's key; the seed stays in a mode-600 file
OTHER_PUB=$("$T/signdoc" gen "$T/other.seed")       # a second key, for "signed by another key"
chmod 600 "$T/sub.seed" "$T/other.seed"

# ---- capability probes ----
POLICY_USAGE=$(mashctl policy 2>&1)
HAVE_diff=0; HAVE_serve=0; HAVE_mashp=0
if grep -q 'policy diff' <<<"$POLICY_USAGE" && grep -q 'policy show' <<<"$POLICY_USAGE" && grep -q 'policy explain' <<<"$POLICY_USAGE"; then HAVE_diff=1; fi
if grep -q 'policy serve' <<<"$POLICY_USAGE"; then HAVE_serve=1; fi
mash policy </dev/null 2>&1 | grep -q 'policy show' && HAVE_mashp=1

why() { case "$1" in diff) echo '#526' ;; serve) echo 'policy serve' ;; mashp) echo '#536' ;; esac; }
have() { local v="HAVE_$1"; [ "${!v}" = 1 ]; }

# ---- the account key and the documents ----
ACCT=$(mashctl policy adopt --show 2>&1 | awk '/^  account /{print $2}')   # empty before adoption: filled after step 2
cd "$T/docs" || die "no docs dir"
SRC_DOCS=$SCRIPT_DIR/docs
for g in gen1 gen2 gen3 gen4; do
  [ -f "$SRC_DOCS/$g.json" ] || die "missing source document $SRC_DOCS/$g.json"
  cp "$SRC_DOCS/$g.json" "$T/docs/$g.template"
done

write_variants() {
python3 -I - "$T/docs" <<'PY'
import json, sys, copy, os
d = sys.argv[1]
g1 = json.load(open(os.path.join(d, "gen1.json")))
g2 = json.load(open(os.path.join(d, "gen2.json")))
man = []
def W(name, desc, obj=None, text=None):
    p = os.path.join(d, name)
    with open(p, "w") as f:
        f.write(text if text is not None else json.dumps(obj, indent=2, sort_keys=True) + "\n")
    man.append((name, desc))
def v(base=g1):
    return copy.deepcopy(base)
acct = g1["account"]
W("v01-yaml.yaml", "the document as YAML text", text="schema: mash.listset/1\naccount: %s\nrange: lab\ngeneration: 1\nbase_generation: 0\nissued_at: 2026-11-14T09:00:00Z\ngroups:\n  h1: {mail: closed, prompt: closed, status: closed, heartbeat: closed}\nsenders:\n  lab.h1.l1: {allow: [lab.h1.lead], deny: []}\n" % acct)
W("v02-truncated.json", "gen1 cut off after 300 bytes", text=(json.dumps(g1, indent=2, sort_keys=True))[:300])
W("v03-empty.json", "an empty file", text="")
o = v(); del o["senders"]
W("v04-missing-field.json", 'required field "senders" removed', o)
o = v(); del o["groups"]["h1"]["heartbeat"]
W("v05-missing-verb.json", 'verb "heartbeat" removed from group h1', o)
o = v(); o["groups"]["h1"]["mail"] = "open"
W("v06-bad-switch.json", 'group h1 mail set to "open"', o)
o = v(); o["schema"] = "mash.listset/2"
W("v07-wrong-schema.json", 'schema set to "mash.listset/2"', o)
o = v(g2); o["generation"] = 5
W("v08-generation.json", "gen2 with generation 5 (base_generation stays 1)", o)
s = json.dumps(g1, indent=2, sort_keys=True)
W("v09-duplicate-key.json", 'the key "schema" written twice', text='{\n  "schema": "mash.listset/1",' + s[1:] + "\n")
o = v(); o["endpoints"] = []
W("v10-unknown-field.json", 'unknown field "endpoints" added', o)
o = v(); o["senders"]["lab.h1.l1"]["allow"] = ["lab.h1.*"]
W("v11-wildcard.json", 'lab.h1.l1 allows "lab.h1.*"', o)
o = v(); o["senders"]["lab.h1.l1"]["allow"] = ["other.h1.lead"]
W("v12-other-range.json", 'lab.h1.l1 allows "other.h1.lead" (another range)', o)
o = v(); o["senders"]["lab.h1.Lead"] = o["senders"].pop("lab.h1.lead")
W("v13-upper-case.json", 'sender "lab.h1.lead" renamed "lab.h1.Lead"', o)
o = v(); o["senders"]["lab.ops.alice"]["deny"] = ["lab.h1.lead"]
W("v14-deny-outside.json", 'lab.ops.alice denies "lab.h1.lead" (outside its owner group)', o)
o = v(); o["senders"]["lab.ops.alice"]["allow"] = ["lab.ops.bob"]
W("v15-allow-and-deny.json", 'lab.ops.alice both allows and denies "lab.ops.bob"', o)
o = v(); o["senders"]["lab.h1.l1"]["deny"] = ["lab.h1.l2"]
W("v16-deny-no-shared.json", 'lab.h1.l1 denies "lab.h1.l2" in a group with no shared verb', o)
# case 5
o = v(); o["zones"] = {"z1": ["lab.h1.l1", "lab.h1.l2"]}
W("z1-zones-field.json", 'gen1 with a "zones" field', o)
o = v(); o["groups"]["lab.h1.l1"] = {"mail": "closed", "prompt": "closed", "status": "closed", "heartbeat": "closed"}
W("z2-switch-for-sender.json", 'a switch set for one sender ("lab.h1.l1") as if it were a group', o)
with open(os.path.join(d, "variants.tsv"), "w") as f:
    for n, ds in man:
        f.write("%s\t%s\n" % (n, ds))
PY
}

# ======================================================================================================
# --shell: an interactive throwaway walkthrough instead of the report. Nothing below this block runs in it.
# ======================================================================================================
if [ "$MODE" = shell ]; then
  mashctl policy adopt --submitter "$SUB_PUB" >"$T/adopt.log" 2>&1 || die "policy adopt failed (see $T/adopt.log)"
  ACCT=$(mashctl policy adopt --show 2>&1 | awk '/^  account /{print $2}')
  [ -n "$ACCT" ] || ACCT=$("$T/signdoc" pub "$T/keys/account.nk" 2>/dev/null)
  [ -n "$ACCT" ] || die "could not determine the account key"
  for g in gen1 gen2 gen3 gen4; do sed "s/ACXTEST/$ACCT/g" "$T/docs/$g.template" >"$T/docs/$g.json"; done
  "$T/signdoc" "$T/sub.seed" "$T/docs/gen1.json" >"$T/docs/gen1.jws"
  "$T/signdoc" "$T/other.seed" "$T/docs/gen1.json" >"$T/docs/gen1-otherkey.jws"
  write_variants
  rm -f "$T"/docs/*.template
  printf '%s\n' "$SUB_PUB" >"$T/submitter.pub"
  mkdir -p "$T/bin"
  ln -s "$T/mash" "$T/bin/mash"; ln -s "$T/mashctl" "$T/bin/mashctl"; ln -s "$T/signdoc" "$T/bin/signdoc"
  guard
  SHORT=${COMMIT:0:6}

  printf '%s\n' "NOT LIVE: throwaway local server. Caveats: (1) a list set that narrows an existing sender is refused in v0.5.0; (2) a brand-new address is refused until the issue-service path exists; (3) exception limits are the measured ones (83 at 14-character names), not the contract's 100; (4) $REF@$SHORT — help text may change until the release commit. (5) new1's credential was minted by a demo helper (account key, admin.NewFormPermissions); in v0.5.0 a three-token credential comes from the issue service, test accounts only. mashctl has no verb for it." >"$T/banner.txt"
  cat >"$T/cheatsheet.txt" <<EOF
Server      nats://127.0.0.1:$PORT   range/owner: lab   endpoints: lab.h1.{lead,l1,l2} lab.ops.{alice,bob} lab.svc.queue-alarm (mashctl provision --agent), stray (legacy add) and acme.team1.new1 (three-token via mashctl provision --agent, creds $T/cfg/new1.creds), all before adoption
Config      $T/cfg   keys: $T/keys   admin creds: $T/server/mashadmin.creds
Submitter   public key:  cat $T/submitter.pub     (seed: $T/sub.seed, mode 600, never print it)
            sign a document:  signdoc $T/sub.seed FILE.json > FILE.jws
Account key mashctl policy adopt --show     (the 'account' line)
Documents   $T/docs   gen1..gen4.json (account key filled in), gen1.jws (signed), v01..v16 and z1/z2 variants, variants.tsv
Try         mashctl policy adopt --show ; mashctl policy diff $T/docs/gen1.json --range lab ; mash version
new1        new1's credential was minted by a demo helper (account key, admin.NewFormPermissions); in v0.5.0 a three-token credential comes from the issue service, test accounts only. mashctl has no verb for it.
            as new1:  mash_new1 policy show ;  mash_new1 policy explain l1 ;  mash_new1 who
            (= MASH_NAME=new1 MASH_AGENT_TOKEN=acme MASH_OWNER=team1 MASH_CREDS=$T/cfg/new1.creds mash ...)
Again       type  banner  or  cheatsheet.   Exit the shell to tear everything down (server stopped, temp dir removed).
EOF
  cat >"$T/rc.sh" <<EOF
export PATH="$T/bin:\$PATH"
export HOME="$T/home" XDG_CONFIG_HOME="$T/home/.config" XDG_STATE_HOME="$T/home/.local/state"
export MASH_CONFIG_DIR="$T/cfg" MASH_KEYS_DIR="$T/keys" MASH_POLICY_BUCKET='homerange_{range}_policy'
export MASH_URL="nats://127.0.0.1:$PORT" MASH_OWNER=lab MASH_ADMIN_CREDS="$T/server/mashadmin.creds"
export HISTFILE="$T/home/.shell_history"
mash_new1() { MASH_NAME=new1 MASH_AGENT_TOKEN=acme MASH_OWNER=team1 MASH_CREDS="$T/cfg/new1.creds" mash "\$@"; }
banner() { cat "$T/banner.txt"; }
cheatsheet() { cat "$T/cheatsheet.txt"; }
cd "$T/docs"
EOF
  WANT=bash
  case "$(basename "${SHELL:-bash}")" in zsh) command -v zsh >/dev/null 2>&1 && WANT=zsh ;; esac
  if [ "$WANT" = zsh ]; then
    mkdir -p "$T/zdot"
    { cat "$T/rc.sh"; echo "PS1='[THROWAWAY :$PORT] %~ %# '"; echo 'banner; echo; cheatsheet'; } >"$T/zdot/.zshrc"
    SHELL_CMD=(zsh -i)
    SHELL_ENV=(ZDOTDIR="$T/zdot")
  else
    { echo "PS1='[THROWAWAY :$PORT] \\w \\\$ '"; echo 'banner; echo; cheatsheet'; } >>"$T/rc.sh"
    SHELL_CMD=(bash --rcfile "$T/rc.sh" -i)
    SHELL_ENV=()
  fi

  echo "== $WANT shell; the server and temp dir go away when it exits (port $PORT stays reserved until YOU release it)" >&2
  # Ctrl-C belongs to the shell: the parent only runs ':' (and waits); TERM/HUP tear everything down via the EXIT trap.
  trap ':' INT
  trap 'exit 143' TERM HUP
  iso PATH="$T/bin:$PATH" "${SHELL_ENV[@]}" "${SHELL_CMD[@]}"
  exit 0
fi


# ======================================================================================================
# --narrate: live narration to the terminal. Everything above and below still runs exactly as in report mode;
# these functions only DISPLAY (the display text is the same sanitized text the report records).
# ======================================================================================================
NARR_STEP=""; NPI=0; NPATS=()
declare -A NARR_MAP=([1]=1 [2]=2 [3]=3 [12]=4 [9]=5 [5]=6 [6]=7 [7]=8 [8]=9 [13]=10 [10]=11 [11]=12 [14]=13 [15]=14)
C_DIM=$'\e[2m'; C_BOLD=$'\e[1m'; C_CYAN=$'\e[36m'; C_GRN=$'\e[1;32m'; C_OFF=$'\e[0m'

narr_pats() { # narr_pats K: the commands steps.md names for video step K, in order (globs; * matches a key or digest)
  NPI=0
  case $1 in
    1) NPATS=("mashctl policy check --policy x.yaml" "mashctl add foo --zone z" "mashctl set-zone foo z") ;;
    2) NPATS=("mashctl policy adopt --show" "mashctl policy adopt --submitter *" "mashctl policy adopt --show") ;;
    3) NPATS=("mashctl add zed" "mashctl reissue l1" "mashctl label l1 k=v" "mashctl retire l1") ;;
    4) NPATS=("mashctl policy diff v02-truncated.json --range lab" "mashctl policy diff v09-duplicate-key.json --range lab" "mashctl policy diff v11-wildcard.json --range lab" "mashctl policy diff gen1-otherkey.jws --submitter * --range lab") ;;
    5) NPATS=("mashctl policy diff z1-zones-field.json --range lab" "mashctl unzone stray") ;;
    6) NPATS=("mashctl policy diff gen1.json --range lab" "mashctl policy explain lab.h1.l1 lab.h1.lead --range lab" "mashctl policy explain lab.h1.l1 lab.h1.l2 --range lab") ;;
    7) NPATS=("mashctl policy show --range lab") ;;
    8) NPATS=("mashctl policy explain lab.h1.lead lab.svc.queue-alarm --range lab" "mashctl policy explain lab.svc.queue-alarm lab.h1.lead --range lab") ;;
    9) NPATS=("mashctl policy explain lab.ops.alice lab.ops.bob --range lab" "mashctl policy explain lab.ops.alice lab.ops.carol --range lab" "mashctl policy explain lab.ops.bob lab.ops.alice --range lab") ;;
    10) NPATS=("mashctl policy diff gen2.json --range lab" "mashctl policy show --range lab") ;;
    11) NPATS=("mashctl policy diff gen3.json --range lab" "mashctl policy diff gen3.json --range lab --submitter *") ;;
    12) NPATS=("mashctl policy diff gen3.json --range lab" "mashctl policy diff gen4.json --range lab" "mashctl policy show --range lab") ;;
    13) NPATS=("*l1-listset.creds mash send lab.h1.lead ping" "*l1-listset.creds mash send lab.h1.l2 ping" "*l1-listset.creds mash policy show" "*l1-listset.creds mash policy explain lab.h1.l2") ;;
    14) NPATS=("mashctl policy reconcile --range lab --servers 1" "mashctl policy reconcile --yes --plan sha256:* --range lab --servers 1" "mashctl policy reconcile --yes --plan sha256:* --range lab --servers 1" "mashctl policy reconcile --range lab --servers 1" "mashctl issued list") ;;
    *) NPATS=() ;;
  esac
}

narr_match() { # does this command line match the next command the step names? (consumes it when it does)
  [ -n "$NARR_STEP" ] && [ "$NPI" -lt "${#NPATS[@]}" ] || return 1
  # shellcheck disable=SC2053
  if [[ $1 == ${NPATS[$NPI]} ]]; then NPI=$((NPI + 1)); return 0; fi
  return 1
}

narr_clean() { sed -e "s#$T#\$T#g" -e 's/\bS[ACUNOX][A-Z2-7]\{54\}\b/<seed-redacted>/g'; }
narr_wrap() { fold -s -w 100 | sed -e '2,$s/^/    /' -e '1s/^\([A-Za-z ]*:\)/'"$C_CYAN"'\1'"$C_OFF"'/'; }
narr_rule() { local r="" i; for i in $(seq 1 100); do r="$r─"; done; printf '%s%s%s\n' "$C_DIM" "$r" "$C_OFF"; }

narr_begin() { # narr_begin EXN: called from ex_begin; clears the screen and prints the step header (no step for case 4)
  NARR_STEP=${NARR_MAP[$1]:-}
  NPI=0; NPATS=()
  [ -n "$NARR_STEP" ] || return 0
  narr_pats "$NARR_STEP"
  narr_text "$NARR_STEP"
  printf '\e[H\e[2J\e[3J'
  printf '%sStep %s of 14 · %s%s\n\n' "$C_BOLD" "$NARR_STEP" "$N_TITLE" "$C_OFF"
  printf 'What you see: %s\n' "$N_SEE" | narr_clean | narr_wrap
  printf 'Why: %s\n' "$N_WHY" | narr_clean | narr_wrap
  printf 'iptables: %s\n' "$N_IPT" | narr_clean | narr_wrap
  narr_rule
  printf '%sCaveats: 1 narrowing refused · 2 new address refused until issue service · 3 measured exception limits · 4 help text final at release%s\n' "$C_DIM" "$C_OFF"
  echo
  sleep 6
}

narr_type() { # prompt, then the command typed one character at a time (~18 ms), then a pause
  local s i
  s=$(printf '%s' "$1" | narr_clean)
  printf '%s$ %s' "$C_GRN" "$C_OFF"
  for ((i = 0; i < ${#s}; i++)); do printf '%s' "${s:i:1}"; sleep 0.018; done
  sleep 0.5
  echo
}

narr_out() { # narr_out RC: the sanitized output of the command just run ($T/b/tmp), at most 25 lines, and the exit code
  local total
  total=$(wc -l <"$T/b/tmp")
  narr_clean <"$T/b/tmp" | head -n 25
  [ "$total" -gt 25 ] && printf '%s(... %d more lines)%s\n' "$C_DIM" $((total - 25)) "$C_OFF"
  printf '%s(exit %s)%s\n' "$C_DIM" "$1" "$C_OFF"
  sleep 3
}

narr_skipped() { printf 'SKIPPED: needs %s\n%s(exit 0)%s\n' "$1" "$C_DIM" "$C_OFF"; sleep 3; }

narr_apply_pre() { [ "$NARRATE" = 1 ] || return 0; printf '%s· applying %s through policy serve (signed by the submitter) ...%s\n' "$C_DIM" "$1" "$C_OFF"; }
narr_apply_post() { [ "$NARRATE" = 1 ] || return 0; printf '%s· %s: state %s%s\n' "$C_DIM" "$1" "${APPLY_STATE[$1]:-not attempted}" "$C_OFF"; sleep 2; }
narr_apply_line() { # the "apply of GEN" line (steps 9 and 12 name it), with the refusal reason when there is one
  [ "$NARRATE" = 1 ] || return 0
  case "$NARR_STEP" in 10|11) ;; *) return 0 ;; esac
  {
    printf -- '--- apply of %s by mashctl policy serve: state %s\n' "$1" "${APPLY_STATE[$1]:-not attempted}"
    if [ "${APPLY_STATE[$1]:-}" != applied ]; then grep 'refused apply revision' "$T/serve.log" 2>/dev/null | tail -n 1; fi
  } | narr_clean | sed -e 's/^/'"$C_DIM"'/' -e 's/$/'"$C_OFF"'/'
  sleep 3
}

narr_finish() { # the Close section and WALK-DONE
  printf '\e[H\e[2J\e[3J'
  printf '%sClose%s\n\n' "$C_BOLD" "$C_OFF"
  narr_close | fold -s -w 100
  echo
  echo WALK-DONE
  sleep 8
}

narr_text() { # narr_text K: sets N_TITLE N_SEE N_WHY N_IPT (text from walk/steps.md)
  case $1 in
  1)
    IFS= read -r -d '' N_TITLE <<'NARR_EOF' || true
The old commands are gone
NARR_EOF
    IFS= read -r -d '' N_SEE <<'NARR_EOF' || true
the zone commands and the old policy-file verbs now refuse, and say what replaced them.
NARR_EOF
    IFS= read -r -d '' N_WHY <<'NARR_EOF' || true
zones are removed (ADR 0091). A script that still calls them fails loudly instead of doing something different.
NARR_EOF
    IFS= read -r -d '' N_IPT <<'NARR_EOF' || true
a distribution dropping `iptables-legacy`. The old commands error with a pointer to the new tool.
NARR_EOF
    ;;
  2)
    IFS= read -r -d '' N_TITLE <<'NARR_EOF' || true
Adopt the account
NARR_EOF
    IFS= read -r -d '' N_SEE <<'NARR_EOF' || true
before adoption nothing owns the policy; after it, one submitter public key is registered, and the admin and system users are recorded as exempt.
NARR_EOF
    IFS= read -r -d '' N_WHY <<'NARR_EOF' || true
from here on, only list sets signed by that key change who may mail whom. mash never holds the private half.
NARR_EOF
    IFS= read -r -d '' N_IPT <<'NARR_EOF' || true
turning on firewalld with lockdown. A manager now owns the ruleset, and only it may change rules.
NARR_EOF
    ;;
  3)
    IFS= read -r -d '' N_TITLE <<'NARR_EOF' || true
Hand edits are refused on an adopted account
NARR_EOF
    IFS= read -r -d '' N_SEE <<'NARR_EOF' || true
the commands that used to change an endpoint's reach directly now refuse on an adopted account.
NARR_EOF
    IFS= read -r -d '' N_WHY <<'NARR_EOF' || true
two owners of one ruleset is how a firewall drifts. One account is run one way only (REQ-016).
NARR_EOF
    IFS= read -r -d '' N_IPT <<'NARR_EOF' || true
firewalld lockdown refusing a direct `iptables -A`.
NARR_EOF
    ;;
  4)
    IFS= read -r -d '' N_TITLE <<'NARR_EOF' || true
A bad file changes nothing
NARR_EOF
    IFS= read -r -d '' N_SEE <<'NARR_EOF' || true
truncated JSON, a duplicate key, a wildcard and a document signed by the wrong key are each refused with the reason.
NARR_EOF
    IFS= read -r -d '' N_WHY <<'NARR_EOF' || true
a list set is applied whole or not at all.
NARR_EOF
    IFS= read -r -d '' N_IPT <<'NARR_EOF' || true
`iptables-restore` rejecting a malformed file with a line number, leaving the running ruleset as it was (atomic commit).
NARR_EOF
    ;;
  5)
    IFS= read -r -d '' N_TITLE <<'NARR_EOF' || true
Zones in a document are refused
NARR_EOF
    IFS= read -r -d '' N_SEE <<'NARR_EOF' || true
a list set that mentions a zone is refused; `unzone` converts an old zoned record.
NARR_EOF
    IFS= read -r -d '' N_WHY <<'NARR_EOF' || true
no half-zoned state can sneak back in.
NARR_EOF
    IFS= read -r -d '' N_IPT <<'NARR_EOF' || true
a rule that jumps to a chain that no longer exists: `No chain/target/match by that name`, and nothing is applied.
NARR_EOF
    ;;
  6)
    IFS= read -r -d '' N_TITLE <<'NARR_EOF' || true
A lane that may mail only its lead
NARR_EOF
    IFS= read -r -d '' N_SEE <<'NARR_EOF' || true
`diff` shows exactly what the signed list set would change, and applies nothing. `explain` answers one question: may this sender mail that receiver, and which line says so.
NARR_EOF
    IFS= read -r -d '' N_WHY <<'NARR_EOF' || true
you review a change before it exists, and you ask about one pair instead of reading the whole ruleset.
NARR_EOF
    IFS= read -r -d '' N_IPT <<'NARR_EOF' || true
`diff` is `iptables-restore --test` with a diff of the result. `explain` is `iptables -C` (does this rule match?) combined with a packet trace that names the rule that decided.
NARR_EOF
    ;;
  7)
    IFS= read -r -d '' N_TITLE <<'NARR_EOF' || true
Show the policy in force
NARR_EOF
    IFS= read -r -d '' N_SEE <<'NARR_EOF' || true
every sender, and whom it may mail, as it is in force now.
NARR_EOF
    IFS= read -r -d '' N_WHY <<'NARR_EOF' || true
the read-back is from the policy store, not from the file you hoped you applied.
NARR_EOF
    IFS= read -r -d '' N_IPT <<'NARR_EOF' || true
`iptables -S` (or `-L -n -v`): the rules as the kernel holds them.
NARR_EOF
    ;;
  8)
    IFS= read -r -d '' N_TITLE <<'NARR_EOF' || true
A send-only service
NARR_EOF
    IFS= read -r -d '' N_SEE <<'NARR_EOF' || true
the service may mail the lead; nobody may mail the service.
NARR_EOF
    IFS= read -r -d '' N_WHY <<'NARR_EOF' || true
alarms and bots should talk, not be talked to.
NARR_EOF
    IFS= read -r -d '' N_IPT <<'NARR_EOF' || true
`OUTPUT ACCEPT` for the service with `INPUT DROP` to it. There is no conntrack-style return path in mail; each direction is its own rule.
NARR_EOF
    ;;
  9)
    IFS= read -r -d '' N_TITLE <<'NARR_EOF' || true
One user blocks one colleague
NARR_EOF
    IFS= read -r -d '' N_SEE <<'NARR_EOF' || true
the ops group shares mail, so its members may mail each other. alice has a deny entry for bob, so alice may not mail bob, but she may mail carol, and bob may still mail alice.
NARR_EOF
    IFS= read -r -d '' N_WHY <<'NARR_EOF' || true
a deny is a narrow exception inside an open group, and it is one-directional.
NARR_EOF
    IFS= read -r -d '' N_IPT <<'NARR_EOF' || true
one `-A OUTPUT -s alice -d bob -j DROP` ahead of a group-wide `ACCEPT`, with no matching rule for bob to alice.
NARR_EOF
    ;;
  10)
    IFS= read -r -d '' N_TITLE <<'NARR_EOF' || true
Taking reach away is refused in this release
NARR_EOF
    IFS= read -r -d '' N_SEE <<'NARR_EOF' || true
gen2 removes a permission from an existing sender. It is refused: narrowing a sender needs a new credential for that sender, and v0.5.0 does not replace credentials yet. `show` reports generation 1 still in force and the attempt refused. This is caveat 1 on screen.
NARR_EOF
    IFS= read -r -d '' N_WHY <<'NARR_EOF' || true
removing a rule without also cutting the sender's existing credential would leave the old reach working. mash refuses the change rather than half-apply it.
NARR_EOF
    IFS= read -r -d '' N_IPT <<'NARR_EOF' || true
`iptables -D` without `conntrack -D` leaves established flows open. mash refuses until it can do both (planned with the issue service).
NARR_EOF
    ;;
  11)
    IFS= read -r -d '' N_TITLE <<'NARR_EOF' || true
Only a signed document can change the policy
NARR_EOF
    IFS= read -r -d '' N_SEE <<'NARR_EOF' || true
`diff` on the unsigned document previews the change (it adds the queue-alarm service to l1's reach). Naming the submitter key makes `diff` check the signature, and the unsigned document fails. The signed copy, handed to `policy serve`, is applied.
NARR_EOF
    IFS= read -r -d '' N_WHY <<'NARR_EOF' || true
the preview is free for anyone; the change itself happens only with the registered submitter's signature.
NARR_EOF
    IFS= read -r -d '' N_IPT <<'NARR_EOF' || true
no direct equivalent. iptables trusts whoever is root; mash trusts only the registered submitter's signature.
NARR_EOF
    ;;
  12)
    IFS= read -r -d '' N_TITLE <<'NARR_EOF' || true
A stale base and a repeat are refused
NARR_EOF
    IFS= read -r -d '' N_SEE <<'NARR_EOF' || true
gen3 is now refused because it was written against generation 1 while generation 2 is in force. gen4 has the same content as what is in force, so there is nothing to apply. `show` reports generation 2 in force.
NARR_EOF
    IFS= read -r -d '' N_WHY <<'NARR_EOF' || true
two people editing at once cannot silently overwrite each other, and a resend changes nothing.
NARR_EOF
    IFS= read -r -d '' N_IPT <<'NARR_EOF' || true
nftables' ruleset generation check (a commit against a stale generation fails), plus `iptables-restore --test`.
NARR_EOF
    ;;
  13)
    IFS= read -r -d '' N_TITLE <<'NARR_EOF' || true
What the sender sees
NARR_EOF
    IFS= read -r -d '' N_SEE <<'NARR_EOF' || true
an allowed send goes; a refused send says it is refused by policy and why; the endpoint can ask what it may reach without admin rights.
NARR_EOF
    IFS= read -r -d '' N_WHY <<'NARR_EOF' || true
a refused mail is never silently lost, and an agent can check before it tries.
NARR_EOF
    IFS= read -r -d '' N_IPT <<'NARR_EOF' || true
`REJECT --reject-with icmp-admin-prohibited` rather than `DROP`: the sender is told.
NARR_EOF
    ;;
  14)
    IFS= read -r -d '' N_TITLE <<'NARR_EOF' || true
The adoption reconcile, one way
NARR_EOF
    IFS= read -r -d '' N_SEE <<'NARR_EOF' || true
the dry run plans one revocation (the legacy endpoint "stray", not in the list set), warns that `--yes` is one-way, and prints a plan digest. `--yes --plan <digest>` revokes exactly that. Re-running the same plan is refused because the world changed. A fresh dry run finds nothing to do, and `issued list` shows stray's key revoked.
NARR_EOF
    IFS= read -r -d '' N_WHY <<'NARR_EOF' || true
it removes credentials the list set does not cover. A revoked credential cannot be reissued on an adopted account in this release, and there is no un-adopt.
NARR_EOF
    IFS= read -r -d '' N_IPT <<'NARR_EOF' || true
`firewall-cmd --complete-reload`: rules nobody declared are flushed and their connections dropped. The difference is that mash makes you read the plan, then pin it by digest.
NARR_EOF
    ;;
  esac
  N_TITLE=${N_TITLE%$'\n'}; N_SEE=${N_SEE%$'\n'}; N_WHY=${N_WHY%$'\n'}; N_IPT=${N_IPT%$'\n'}
}
narr_close() {
  cat <<'NARR_EOF'
Three calls for you (the one-page brief has the detail):
1. Keys: run live `add`, `reissue` and `unzone` only on the key-store machine for now, and move them to the issue service later.
2. Exception limits: publish the measured table (for example 83 exceptions per sender at 14-character names), not "about 100".
3. Reconcile: ship it one-way. A real adoption needs its own go and a rehearsal on a throwaway account.
Then: go or no-go on tagging v0.5.0 at 625d1bc.
NARR_EOF
}

# ---- report buffers ----
EXN=00; EXT=""; EXC=""; SKIPPED_ANY=0
ex_begin() { EXN=$(printf '%02d' "$1"); EXT=$2; EXC=$3; EXI=$1; : >"$T/b/sh"; : >"$T/b/json"; : >"$T/b/out"; [ "$NARRATE" = 1 ] && narr_begin "$1"; }
ex_json() { # ex_json FILE [LABEL]: a list set, pretty-printed, under its own block
  { echo; echo "List set \`${2:-$(basename "$1")}\`:"; echo; echo '```json'; jq . "$1" 2>/dev/null || cat "$1"; echo '```'; } >>"$T/b/json"
}
ex_note() { { echo; echo "$*"; } >>"$T/b/json"; }
ex_say() { echo "$*" >>"$T/b/out"; }
ex_end() {
  { echo "### $EXI. $EXT"; echo; echo "Case: $EXC"; echo; echo '```sh'; cat "$T/b/sh"; echo '```'
    cat "$T/b/json"; echo; echo '```text'; cat "$T/b/out"; echo '```'; echo; } >"$T/ex/$EXN.md"
}
sanitize() { sed -e "s#$T#\$T#g" -e 's/\bS[ACUNOX][A-Z2-7]\{54\}\b/<seed-redacted>/g' | head -n 40 | awk '{print} END{ if (NR>=40) print "(... output truncated at 40 lines)"}'; }

# R [needs...] -- cmd args : records the command, runs it (or says SKIPPED), records output and exit code.
# DISP overrides the displayed command. SKIPPED lines name what is missing.
R() {
  local needs=() n miss="" rc line show=0
  while [ "$1" != -- ]; do needs+=("$1"); shift; done; shift
  line=${DISP:-$*}
  echo "$line" >>"$T/b/sh"
  echo "\$ $line" >>"$T/b/out"
  for n in "${needs[@]}"; do have "$n" || { miss=$(why "$n"); break; }; done
  [ "$NARRATE" = 1 ] && narr_match "$line" && show=1
  [ "$show" = 1 ] && narr_type "$line"
  if [ -n "$miss" ]; then
    echo "SKIPPED: needs $miss" >>"$T/b/out"; echo >>"$T/b/out"; SKIPPED_ANY=1
    [ "$show" = 1 ] && narr_skipped "$miss"
    return 0
  fi
  guard
  "$@" >"$T/b/tmp" 2>&1 </dev/null; rc=$?
  sanitize <"$T/b/tmp" >>"$T/b/out"
  echo "(exit $rc)" >>"$T/b/out"; echo >>"$T/b/out"
  [ "$show" = 1 ] && narr_out "$rc"
  return 0
}

# ---- apply path (best effort; only runs when `mashctl policy serve` exists) ----
ensure_serve() {
  [ -n "$SERVE_PID" ] && return 0
  # main requires --range and --servers; the system-account credentials default to <server dir>/sys.creds, found next to
  # the admin credentials (cmd/mashctl/syscreds.go sysCredsPath), so no --sys-creds is needed for an init --local server.
  iso --exec "$T/mashctl" policy serve --range lab --servers 1 >"$T/serve.log" 2>&1 </dev/null &
  SERVE_PID=$!
  local i
  for i in $(seq 1 20); do
    grep -q 'policy service listening' "$T/serve.log" 2>/dev/null && return 0
    kill -0 "$SERVE_PID" 2>/dev/null || break
    sleep 0.5
  done
  echo "serve did not report listening; log: $(sanitize <"$T/serve.log" | tr '\n' ' ')" >&2
}
# The intake watches the key listset.<ACCT> (internal/policyintake/intake.go KeyPrefix) and takes a compact JWS as the value.
# The status is at status.<ACCT> (internal/policyapply/seam.go StatusKey); its value is a statusValue (kvledger.go) with the
# Status nested under .status: .status.generation and .status.state (status.go).
apply_gen_real() { # apply_gen GEN: sign GEN.json, put it at listset.<ACCT>, wait for .status.generation==GEN and a final state
  have serve || return 0
  guard
  ensure_serve
  local g i st old
  g=$(jq -r .generation "$T/docs/$1.json")
  # the status may still hold an earlier attempt with the same generation number: wait for updated_at to change
  old=$(natsq kv get homerange_lab_policy "status.$ACCT" --raw 2>/dev/null | jq -r '.status.updated_at // ""' 2>/dev/null)
  "$T/signdoc" "$T/sub.seed" "$T/docs/$1.json" >"$T/docs/$1.jws"
  natsq kv put homerange_lab_policy "listset.$ACCT" "$(cat "$T/docs/$1.jws")" >"$T/put-$1.log" 2>&1 || echo "kv put failed: $(cat "$T/put-$1.log")" >&2
  for i in $(seq 1 40); do
    st=$(natsq kv get homerange_lab_policy "status.$ACCT" --raw 2>/dev/null | jq -r --argjson g "$g" --arg o "$old" 'select(.status.generation==$g and .status.updated_at != $o) | .status.state' 2>/dev/null)
    case "$st" in applied|refused|partial) APPLY_STATE[$1]=$st; return 0 ;; esac
    sleep 1
  done
  APPLY_STATE[$1]=timeout
}

apply_gen() { narr_apply_pre "$1"; apply_gen_real "$1"; narr_apply_post "$1"; }

apply_report_real() { # apply_report GEN: what the apply did, and the serve log tail (all of it that matters when it did not apply)
  local n=4
  [ "${APPLY_STATE[$1]:-}" = applied ] || n=8
  ex_say "--- apply of $1 by mashctl policy serve: state ${APPLY_STATE[$1]:-not attempted}; serve.log, last $n lines:"
  tail -n "$n" "$T/serve.log" 2>/dev/null | sanitize >>"$T/b/out"
  ex_say ""
}

apply_report() { apply_report_real "$1"; narr_apply_line "$1"; }

# ======================================================================================================
# Execution order differs from report order: examples that need nothing in force run first, then the
# state is advanced gen1 -> gen2 -> gen3 for the examples that need it. The report is sorted by number.
# ======================================================================================================

[ "$NARRATE" = 1 ] && { echo READY; sleep 3; }

# ---- 1. removed verbs (main) ----
ex_begin 1 "Removed verbs are refused" "the policy file, the zone verbs and \`--policy\` no longer exist (ADR 0091); the old surface answers with usage or an unknown-command error"
R -- mashctl policy check --policy x.yaml
R -- mashctl policy apply
R -- mashctl policy show
R -- mashctl policy diff
R -- mashctl add foo --policy x.yaml
R -- mashctl add foo --zone z
R -- mashctl set-zone foo z
ex_note 'Note: on a build that has the read verbs (#526), `policy show` and `policy diff` exist; their output below is then real output of those verbs, not a refusal.'
ex_end

# ---- 2. adoption (main) ----
ex_begin 2 "Adoption" "adopt registers the one submitter key allowed to hand in list sets; adopting again with the same key changes nothing"
R -- mashctl policy adopt --show
R -- mashctl policy adopt --submitter "$SUB_PUB"
R -- mashctl policy adopt --submitter "$SUB_PUB"
R -- mashctl policy adopt --show
R -- mashctl policy adopt --show --json
ex_note "The submitter public key above was generated for this run; its seed is in a mode-600 file inside the temp dir and is not in this report. The account key is the one 'policy adopt --show' prints."
ex_end
ACCT=$(mashctl policy adopt --show 2>&1 | awk '/^  account /{print $2}')
# a build without adoption has no marker: take the account key from the key store's account seed instead
[ -n "$ACCT" ] || ACCT=$("$T/signdoc" pub "$T/keys/account.nk" 2>/dev/null)
[ -n "$ACCT" ] || die "could not determine the account key"

# substitute the account key into the documents
for g in gen1 gen2 gen3 gen4; do sed "s/ACXTEST/$ACCT/g" "$T/docs/$g.template" >"$T/docs/$g.json"; done
"$T/signdoc" "$T/sub.seed" "$T/docs/gen1.json" >"$T/docs/gen1.jws"
"$T/signdoc" "$T/other.seed" "$T/docs/gen1.json" >"$T/docs/gen1-otherkey.jws"

# ---- 3. adoption guards (main) ----
ex_begin 3 "Adoption guards" "once adopted, the direct verbs that change an account are refused: list sets come only through the list-set path"
R -- mashctl add zed
R -- mashctl reissue l1
R -- mashctl label l1 k=v
R -- mashctl retire l1
ex_note "The endpoint l1 (lab.h1.l1) was provisioned (\`mashctl provision l1 --agent lab --owner h1 ...\`) before adoption, so the verbs have a name to refuse on."
ex_end

# ---- 4. bad submitter and usage errors (main) ----
ex_begin 4 "A bad submitter key, and usage errors" "the submitter must be an nkey public key that mash does not hold; the value is never echoed"
R -- mashctl policy adopt --submitter notakey
R -- mashctl policy adopt --submitter "$ACCT"
R -- mashctl policy adopt
R -- mashctl policy adopt --json
R -- mashctl policy adopt --submitter
R -- mashctl policy adopt --show --bogus
R -- mashctl policy adopt --show --exempt "$SUB_PUB"
ex_note 'Second command: the value is the account public key itself, a key mash holds, so it is refused as a submitter.'
ex_end

# ---- variants for case 9 and 5 ----
write_variants   # variants for cases 9, 5 and 12 (also used by --shell)

# ---- 12. bad files (nothing in force yet: runs before any apply) ----
ex_begin 12 "A bad file" "every defect is refused with a precise reason and exit 1; the plan prints every refusal, not only the first"
ex_json "$T/docs/gen1.json" "gen1.json (the base of every variant below; gen2 for the generation variant)"
while IFS=$'\t' read -r f desc; do
  case "$f" in v*) ;; *) continue ;; esac
  ex_say "--- variant: $f: $desc"
  if [ "$f" != v01-yaml.yaml ] && [ "$f" != v02-truncated.json ] && [ "$f" != v03-empty.json ] && [ "$f" != v09-duplicate-key.json ] && [ "$f" != v08-generation.json ]; then
    ex_say "$(diff gen1.json "$f" | grep '^[<>]' | head -n 6 | sed 's/^</  gen1:/; s/^>/  here:/')"
  fi
  R diff -- mashctl policy diff "$f" --range lab
done <"$T/docs/variants.tsv"
ex_say "--- variant: gen1.json given to --submitter with no signature"
R diff -- mashctl policy diff gen1.json --submitter "$SUB_PUB" --range lab
ex_say "--- variant: gen1 signed by another key, checked against the adopted submitter"
R diff -- mashctl policy diff gen1-otherkey.jws --submitter "$SUB_PUB" --range lab
ex_say "--- control: gen1 signed by the submitter"
R diff -- mashctl policy diff gen1.jws --submitter "$SUB_PUB" --range lab
ex_end

# ---- 9. zone construct (case 5 of the cases) ----
ex_begin 9 "A zone construct in a document, and the old CLI" "no zone exists in v0.5.0: a list set that carries one is refused as an unknown field; the nearest real refusals follow"
ex_json "$T/docs/z1-zones-field.json" "z1-zones-field.json (gen1 plus a zones field)"
R diff -- mashctl policy diff z1-zones-field.json --range lab
R diff -- mashctl policy diff z2-switch-for-sender.json --range lab
R -- mashctl add foo --zone z
R -- mashctl unzone stray
ex_note 'The `unzone` line is the upgrade helper for a directory record that still carries a zone; on a record that has none it has nothing to do (the real output is shown), and it may refuse on an adopted account. An applied old zone policy in the account JWT is refused by the zone guard; that needs system credentials and an old release, so it is not produced here.'
ex_end

# ---- 5, 6, 7, 8: gen1 ----
ex_begin 5 "A lane that may mail only its lead" "gen1: the h1 group keeps every verb closed and l1/l2 allow only the lead"
ex_json "$T/docs/gen1.json" "gen1.json"
R diff -- mashctl policy diff gen1.json --range lab
apply_gen gen1
apply_report gen1
R diff serve -- mashctl policy explain lab.h1.l1 lab.h1.lead --range lab
R diff serve -- mashctl policy explain lab.h1.l1 lab.h1.l2 --range lab
ex_note 'The first command (a diff with nothing in force) is the first-list-set plan. The explain commands describe the list set IN FORCE, so they need `mashctl policy serve` to have applied gen1.'
ex_end

ex_begin 6 "A lead that may mail its herd" "gen1: the lead lists l1 and l2 on its allow list; the lanes list only the lead"
ex_json "$T/docs/gen1.json" "gen1.json"
R diff serve -- mashctl policy show --range lab
ex_end

ex_begin 7 "A send-only service identity" "gen1: queue-alarm sits in its own closed group and lists only the lead; nobody lists it, so nobody may mail it"
ex_json "$T/docs/gen1.json" "gen1.json"
R diff serve -- mashctl policy explain lab.h1.lead lab.svc.queue-alarm --range lab
R diff serve -- mashctl policy explain lab.svc.queue-alarm lab.h1.lead --range lab
ex_end

ex_begin 8 "A narrow user" "gen1: ops mail is shared but alice denies bob; a deny is one-way"
ex_json "$T/docs/gen1.json" "gen1.json"
R diff serve -- mashctl policy explain lab.ops.alice lab.ops.bob --range lab
R diff serve -- mashctl policy explain lab.ops.alice lab.ops.carol --range lab
R diff serve -- mashctl policy explain lab.ops.bob lab.ops.alice --range lab
ex_end

# ---- 13. removing a permission: gen2 against gen1 in force ----
ex_begin 13 "Removing a permission" "gen2 drops l2's allow of the lead; the plan says the old credential must be rotated and revoked"
ex_json "$T/docs/gen2.json" "gen2.json (gen1 is in force)"
R diff serve -- mashctl policy diff gen2.json --range lab
apply_gen gen2
apply_report gen2
R diff serve -- mashctl policy show --range lab
ex_end
G2STATE=${APPLY_STATE[gen2]:-none}
BASE3="gen2 is in force"
if [ "$G2STATE" != applied ]; then
  # Main's apply refuses a list set that narrows an existing sender by tags (gen2 drops l2's allow of the lead): see
  # "latest attempt" in case 11 and $T/serve.log in the report. gen3 and gen4 were written on top of gen2, so they would
  # only ever be "stale". To still exercise a widening apply and a same-content generation for real, derive gen3 and gen4
  # from what IS in force (gen1): gen3 = gen1 + l1 also allows lab.svc.queue-alarm (generation 2, base 1), gen4 = the same
  # content as generation 3 (base 2). The original gen3/gen4 are kept as gen3-orig.json / gen4-orig.json.
  cp "$T/docs/gen3.json" "$T/docs/gen3-orig.json"; cp "$T/docs/gen4.json" "$T/docs/gen4-orig.json"
  python3 -I - "$T/docs" <<'PYD'
import json, sys, os
d = sys.argv[1]
g = json.load(open(os.path.join(d, "gen1.json")))
g["generation"] = 2; g["base_generation"] = 1
g["senders"]["lab.h1.l1"]["allow"] = ["lab.h1.lead", "lab.svc.queue-alarm"]
json.dump(g, open(os.path.join(d, "gen3.json"), "w"), indent=2, sort_keys=True)
g["generation"] = 3; g["base_generation"] = 2
json.dump(g, open(os.path.join(d, "gen4.json"), "w"), indent=2, sort_keys=True)
PYD
  BASE3="gen2 was refused by the apply (see case 13), so gen1 is still in force and gen3 here is DERIVED from gen1: generation 2, base_generation 1, l1 also allows lab.svc.queue-alarm; gen4 is the same content as generation 3, generation 3, base_generation 2"
fi

# ---- 10. widening: gen3 against gen2 in force ----
ex_begin 10 "An apply that would widen a user" "gen3 gives l1 an allow of lab.svc.queue-alarm (against gen2 if gen2 applied, else against gen1: see the note); there is no widen gate, the diff shows it"
ex_json "$T/docs/gen3.json" "gen3.json"
ex_note "gen3: $BASE3."
R diff serve -- mashctl policy diff gen3.json --range lab
R diff -- mashctl policy diff gen3.json --range lab --submitter "$SUB_PUB"
ex_note 'The second command is not state-dependent: gen3 is unsigned, so with --submitter it is refused. The apply of gen3 follows the diff.'
apply_gen gen3
apply_report gen3
ex_end

# ---- 11. dry run, diff, stale base, same-content ----
ex_begin 11 "A dry run, a diff, a stale base, a same-content generation" "diff changes nothing; gen3 again after gen3 is in force is stale; gen4 has the same content as gen3"
ex_json "$T/docs/gen3.json" "gen3.json (in force)"
ex_json "$T/docs/gen4.json" "gen4.json (same content as gen3, one generation later)"
R diff serve -- mashctl policy diff gen3.json --range lab
R diff serve -- mashctl policy diff gen4.json --range lab
R diff serve -- mashctl policy show --range lab
ex_end

# ---- 14. refused send and who ----
ex_begin 14 "The refused-send text and who after policy" "with the list set in force, l1's credential may mail the lead but not l2; who has no policy column"
ex_json "$T/docs/gen1.json" "gen1.json (the list set in force)"
# No credential file comes out of the apply path on main: the apply records each issued key in the issue record (internal/
# policyapply/issue.go, `mashctl issued list`), and the issue service (`mashctl issue serve`, test accounts only) hands a
# credential to a wrangler-countersigned key. So two demo-minted credentials for lab.h1.l1 are used (see the note):
#   (the JWT name is the bare endpoint name l1: the scoped template's subjects are inbox-lab_h1_{{name()}} etc.; internal/policyserve/signer.go:238-243)
#   l1-listset.creds  signed by the group lab.h1's scoped signing key, IssuerAccount set, no permissions, with the tags the
#                     list set in force gives lab.h1.l1: the SHAPE the issue path makes (internal/issue/issuer.go:236-243),
#                     so the scoped key's template (pushed by the apply) is what enforces; the client treats it as a
#                     list-set credential and asks the policy responder (cmd/mash/policy.go listSetCreds)
#   l1-newform.creds  minted in setup, signed by the account key with admin.NewFormPermissions: open owner-line permissions
INFORCE=gen1; [ "${APPLY_STATE[gen3]:-}" = applied ] && INFORCE=gen3
L1TAGS=$(jq -r '.senders["lab.h1.l1"] | ((.allow // []) | map("allow:" + sub("^lab\\."; ""))) + ((.deny // []) | map("deny:" + sub("^lab\\."; ""))) | join(",")' "$T/docs/$INFORCE.json")
( umask 077; "$T/signdoc" listsetcreds "$T/keys/groups/lab.h1.nk" "$ACCT" "$T/prov-l1.seed" l1 "$L1TAGS" >"$T/cfg/l1-listset.creds" ) || die "could not mint l1-listset.creds"
chmod 600 "$T/cfg/l1-listset.creds"
mash_l1ls() { iso MASH_NAME=l1 MASH_AGENT_TOKEN=lab MASH_OWNER=h1 MASH_CREDS="$T/cfg/l1-listset.creds" "$T/mash" "$@"; }
ex_say "(l1-listset.creds carries the tags: $L1TAGS, from $INFORCE.json, the list set in force)"
ex_say ""
LS='MASH_NAME=l1 MASH_AGENT_TOKEN=lab MASH_OWNER=h1 MASH_CREDS=l1-listset.creds'
DISP='mashctl issued list' R -- mashctl issued list
DISP="$LS mash send lab.h1.lead ping"  R -- mash_l1ls send lab.h1.lead ping
DISP="$LS mash send lab.h1.l2 ping"    R -- mash_l1ls send lab.h1.l2 ping
DISP="$LS mash who"                    R -- mash_l1ls who
DISP="$LS mash policy show"            R mashp -- mash_l1ls policy show
DISP="$LS mash policy explain lab.h1.l2" R mashp -- mash_l1ls policy explain lab.h1.l2
DISP="$LS mash policy explain lab.h1.lead" R mashp -- mash_l1ls policy explain lab.h1.lead
NF='MASH_NAME=l1 MASH_AGENT_TOKEN=lab MASH_OWNER=h1 MASH_CREDS=l1-newform.creds'
DISP="$NF mash send lab.h1.l2 ping   (account-key demo credential)" R -- mash_l1 send lab.h1.l2 ping
DISP="$NF mash policy show   (account-key demo credential)" R mashp -- mash_l1 policy show
DISP='MASH_NAME=stray MASH_CREDS=stray.creds mash policy show   (a legacy credential, `mashctl add stray` wrote it)' R mashp -- mash_as stray "$T/cfg/stray.creds" policy show
ex_note 'HONEST NOTE on the credentials: BOTH were minted by a demo helper (signdoc), NOT by the list-set issue path (no wrangler countersign, no issue-record entry, no `mashctl issue serve`). l1-listset.creds is signed by the lab.h1 scoped group key from the key store with the tags the list set in force gives l1 and no permissions of its own, so the scoped key template pushed by the apply enforces, as for an issued credential, but it was not issued by the issue path. l1-newform.creds (minted in setup, before adoption) is signed by the account key with admin.NewFormPermissions for lab.h1.l1: open permissions, no list-set tags, so its send results do not reflect list-set templates, and the client answers `mash policy` for it locally (it is not a list-set credential). The list-set verdicts are the `mash policy explain` lines and `mashctl policy explain` in cases 5-8, which describe the list set in force. The apply path issues no creds file; `mashctl issued list` above shows what it recorded.'
ex_note 'The refusal text above is real output of the build (internal/endpoint/refusal.go).'
ex_end

# ---- 15. the adoption reconcile (after the first list set is applied) ----
ex_begin 15 "The adoption reconcile" "after gen1 is applied: the dry run plans what the account held before the list set and the list set does not own (the recorded legacy user stray, which no document names); --yes --plan DIGEST revokes exactly that plan; the same digest again is refused if the state changed"
if [ "${APPLY_STATE[gen1]:-}" = applied ]; then
  R -- mashctl policy reconcile --range lab --servers 1
  PLAN=$(grep -o 'sha256:[0-9a-f]\{64\}' "$T/b/tmp" | tail -1)
  R -- mashctl policy reconcile --range lab --servers 1 --json
  PLAN_J=$(sed -n '/^{/,$p' "$T/b/tmp" | jq -r '.plan_digest // empty' 2>/dev/null)
  ex_say "(digest from the text plan: ${PLAN:-none}; from the JSON .plan_digest: ${PLAN_J:-not found}; they $( [ "$PLAN" = "$PLAN_J" ] && echo match || echo DIFFER))"
  ex_say ""
  if [ -n "$PLAN" ]; then
    R -- mashctl policy reconcile --yes --plan "$PLAN" --range lab --servers 1
    R -- mashctl policy reconcile --yes --plan "$PLAN" --range lab --servers 1
    R -- mashctl policy reconcile --range lab --servers 1
    R -- mashctl issued list
  else
    ex_say "no plan digest found in the dry run; --yes not run"
  fi
else
  ex_say "gen1 was not applied (state ${APPLY_STATE[gen1]:-not attempted}), so the reconcile does not apply"
fi
ex_note 'The throwaway server is disposable, so --yes was run once for real. The second --yes uses the same digest after the first changed the account: the real answer is shown.'
ex_end

# ======================================================================================================
# the report
# ======================================================================================================
MASHCTL_VER=$(mashctl version 2>&1 | head -1)
MASH_VER=$(mash version 2>&1 | head -1)
{
  echo "# mash v0.5.0 policy-model examples: real output"
  echo
  echo "- Date: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "- REF: \`$REF\`"
  echo "- Source commit (\`git rev-parse $REF\`): \`$COMMIT\`"
  echo "- mashctl: \`$MASHCTL_VER\`"
  echo "- mash: \`$MASH_VER\`"
  echo "- Server: a throwaway local server from \`mashctl init --local --owner lab --port $PORT --start\`, 127.0.0.1:$PORT, torn down at the end. Range \`lab\` (owner for init is \`lab\`, so the documents' range \`lab\` matches; no change was needed)."
  echo "- Policy bucket: \`homerange_lab_policy\` created with \`nats kv add homerange_lab_policy --history 8\`; \`MASH_POLICY_BUCKET='homerange_{range}_policy'\`."
  echo "- Account key (from \`policy adopt --show\`): \`$ACCT\`, substituted for ACXTEST in every document. Submitter public key: \`$SUB_PUB\` (seed kept in a mode-600 file, not shown)."
  echo "- Isolation: every command ran with all MASH_* variables unset and HOME/config/keys inside the temp dir; the live server was never contacted."
  echo "- Signer: $SIGN_HOW."
  echo "- Capabilities of this build: \`policy diff|show|explain\` $( [ $HAVE_diff = 1 ] && echo present || echo 'absent (needs #526)' ); \`policy serve\` $( [ $HAVE_serve = 1 ] && echo present || echo 'absent (needs policy serve)' ); \`mash policy show\` $( [ $HAVE_mashp = 1 ] && echo present || echo 'absent (needs #536)' )."
  echo "- Provisioned before adoption (setup, so every address a document names already has its inbox: \`policy serve\` checks it and makes nothing, ADR 0122; after adoption \`mashctl add\` is refused): $PROVISIONED, with \`MASH_AGENT_TOKEN=lab mashctl provision NAME --agent lab --owner OWNER --user KEY --account ACCT\` (three-token address range.owner.name, a generated user key, no creds file). A legacy \`mashctl add\` of owner h1, ops or svc is refused beside them (owner equals a new-form owner), so every document sender is three-token. Also \`mashctl add stray\` (legacy, owner lab), named by no document: a target for the case 15 reconcile."
  echo "- Credentials: The three-token endpoints have no creds file (provisioned with a generated user key). \`stray\` is a legacy \`mashctl add\` endpoint with a creds file. \`l1-newform.creds\` (lab.h1.l1) was minted by a demo helper (account key plus admin.NewFormPermissions) for lab.h1.l1's provisioned user key before adoption, NOT by the list-set issue path; the apply path issues no creds file on main. See case 14."
  echo "- Apply path: \`mashctl policy serve --range lab --servers 1\` (system creds found at the default location next to the admin creds), started by the script, stopped at the end. Each list set is put at \`listset.<account>\` as a compact JWS signed by the submitter; the status is read from \`status.<account>\` at \`.status.generation\` / \`.status.state\`."
  echo "- Output longer than 40 lines is truncated and says so. Paths under the temp dir are shown as \`\$T\`."
  echo "- Examples run in a different order from the numbers (state is advanced gen1, gen2, gen3 for the ones that need it); the numbering is the report order."
  echo
  cat "$T"/ex/*.md
} >"$OUT"

# last line of defence: no seed or credential material in the report
if grep -qE '\bS[ACUNOX][A-Z2-7]{54}\b|BEGIN NATS|-----BEGIN' "$OUT"; then
  sed -i -E 's/\bS[ACUNOX][A-Z2-7]{54}\b/<seed-redacted>/g' "$OUT"
  echo "drive.sh: warning: key-shaped material was redacted from the report" >&2
fi
echo "report: $OUT" >&2
[ "$NARRATE" = 1 ] && narr_finish
exit 0
