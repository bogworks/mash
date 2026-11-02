// Package apply pushes a compiled policy to the MASH account (ADR 0052, 0053): one scoped signing key per zone in
// the account JWT. It looks the current account JWT up on the resolver through the system account, replaces or adds
// only the scoped signing keys of the policy's zones, signs the result with the operator key, pushes it, and reads it
// back to verify. It removes or replaces a zone key only when asked (--replace-zone-key, --remove-zone), refuses a zone the file lacks unless told to remove or keep it, and it is not a transaction across NATS claims, JetStream resources and
// local files; a partial result says what was and was not done. Seed text is never put in output or errors.
package apply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/policy"
)

// NotATransaction is the one sentence every run ends with.
const NotATransaction = "apply is not a transaction across NATS claims, JetStream resources and local files: if a step fails it says what was done and what was not, and running `mashctl policy apply` again is safe."

// Resolver is the system-account view of the account resolver.
type Resolver interface {
	// Lookup returns the account's current JWT, or "" when the resolver has none.
	Lookup(ctx context.Context, account string) (string, error)
	// LookupAll returns the account JWT of every server that answered the claims lookup, one entry per reply ("" is a
	// server that has none). expect is how many servers are known (0: unknown, so only a quiet period ends the wait);
	// a transport stops early once that many answered. Fewer entries than expect means a server did not answer.
	LookupAll(ctx context.Context, account string, expect int) ([]string, error)
	// Servers returns what a system-account ping learned: the servers that answered and how many the cluster says
	// it has. The set that must acknowledge a push is ServerSet.Names; only a Complete set is authoritative.
	Servers(ctx context.Context) (ServerSet, error)
	// Push sends the account JWT as a claims update and returns the answer of every server that replied; expect is
	// the known server set (nil when unknown), which a transport may wait for.
	Push(ctx context.Context, token string, expect []string) ([]ServerAck, error)
}

// ServerSet is the answer to the system-account ping.
type ServerSet struct {
	// Names are the distinct servers that replied.
	Names []string
	// Expected is the largest 1 + route peers any reply reported: how many servers the cluster has.
	Expected int
	// Gateways is set when any reply reports gateways: super-cluster membership cannot be verified from here.
	Gateways bool
	// Cluster is the cluster name any reply gave ("" when no reply names a cluster: a standalone server).
	Cluster string
	// ClusterConflict is set when replies name different clusters: the set spans clusters and is not authoritative.
	ClusterConflict bool
}

// need is how many distinct replies make the set complete: what the routes say, or the operator's inventory
// (--servers) when that is larger.
func (s ServerSet) need(inventory int) int { return max(s.Expected, inventory) }

// Complete says the set is authoritative. It needs at least one reply, no gateways, and as many replies as the
// routes report and as the operator's inventory says (--servers; 0 is none given). Without an inventory, a reply
// that names a cluster is not enough, even with zero routes: an isolated cluster member sees no peers, so the
// number of servers cannot be verified from here. A reply naming no cluster is a standalone server.
func (s ServerSet) Complete(inventory int) bool {
	if s.Gateways || s.ClusterConflict || len(s.Names) == 0 {
		return false
	}
	if inventory <= 0 && s.Cluster != "" {
		return false
	}
	return len(s.Names) >= s.need(inventory)
}

// Why says, for an incomplete set, why it cannot be trusted.
func (s ServerSet) Why(inventory int) string {
	switch {
	case len(s.Names) == 0:
		return "the system account ping was answered by no server"
	case s.ClusterConflict:
		return fmt.Sprintf("%d server(s) replied and they name different clusters (first seen: %s): membership cannot be verified from here", len(s.Names), s.Cluster)
	case s.Gateways:
		return fmt.Sprintf("%d server(s) replied, and a server reports gateways: a super-cluster's membership cannot be verified from here", len(s.Names))
	case inventory <= 0 && s.Cluster != "" && len(s.Names) >= s.Expected:
		return fmt.Sprintf("this server is part of cluster %s and the number of servers cannot be verified from here; pass --servers N", s.Cluster)
	case s.Expected >= inventory:
		return fmt.Sprintf("%d server(s) replied but the cluster reports %d", len(s.Names), s.Expected)
	}
	return fmt.Sprintf("%d server(s) replied but --servers says %d", len(s.Names), inventory)
}

// ServerAck is one server's answer to a claims update.
type ServerAck struct {
	Server  string
	Code    int
	Message string
}

// OK says the server accepted the update.
func (a ServerAck) OK() bool { return a.Code == 200 }

// Mode is what a run does.
type Mode int

const (
	// Apply pushes, creating missing zone keys first.
	Apply Mode = iota
	// DryRun reports what Apply would do and changes nothing.
	DryRun
	// Diff is DryRun under another name (`mashctl policy diff`).
	Diff
)

func (m Mode) String() string {
	switch m {
	case DryRun:
		return "policy apply --dry-run"
	case Diff:
		return "policy diff"
	}
	return "policy apply"
}

// Params is one run.
type Params struct {
	Mode     Mode
	Compiled *policy.Compiled
	Keys     *ctlkeys.Authority
	Resolver Resolver
	// Budget measures the compile; nil means policy.Budget. A seam for tests.
	Budget func(*policy.Compiled) (policy.BudgetReport, error)
	// VerifyTries and VerifyWait bound the read-back after a push (defaults 5 and 200ms): a clustered resolver
	// may take a moment to answer with the new claims everywhere.
	VerifyTries int
	VerifyWait  time.Duration
	// ReplaceZoneKeys names zones whose scoped signer on the server may be replaced by the key apply holds
	// (--replace-zone-key): credentials signed by the old key stop working.
	ReplaceZoneKeys []string
	// RemoveZones and KeepZones say what to do with a scoped signer on the server whose role is a zone the policy
	// file lacks (--remove-zone, --keep-zone). Without one of them apply refuses.
	RemoveZones, KeepZones []string
	// Servers is the number of servers in the deployment (--servers, MASH_SERVERS); 0 means not given.
	Servers int
	// LockWait is how long an apply waits for the key directory lock (default 5s).
	LockWait time.Duration

	// collectAll (drift only, with Mode Diff) makes build go on past a refusal and describe every zone, so a drift
	// report lists everything at once; the zones with a conflict are skipped, the conflict being their finding.
	collectAll bool
	// trust is the set of accepted issuers of the account JWT, loaded from the key store by Run and AccountFindings.
	trust Trust
}

const (
	defaultVerifyTries = 5
	defaultVerifyWait  = 200 * time.Millisecond
	defaultLockWait    = 5 * time.Second
)

// zoneState is how one file zone compares with the server.
type zoneState int

const (
	zoneUnchanged zoneState = iota
	zoneNew
	zoneChanged
)

type zonePlan struct {
	name      string
	state     zoneState
	pub       string
	keyIsNew  bool // no key file yet: apply creates it, a dry run would
	scope     *jwt.UserScope
	changes   []string // "pub allow  + line" for a changed or new zone
	otherDiff bool     // changed in the role or limits only
	replaced  []string // old scoped signers under this role that this run removes (--replace-zone-key)
}

// built is one computation of the desired account JWT from the JWT the resolver had.
type built struct {
	cur       string
	desired   *jwt.AccountClaims
	plans     []*zonePlan
	kept      []string // outside-policy zones acknowledged with --keep-zone
	removed   []outside
	unacked   []outside    // outside-policy zones nobody said what to do with
	unknown   []unknownKey // signing keys of the account JWT that mashctl did not create and that nobody pinned
	changed   bool
	token     string
	conflicts []conflict
}

// outside is a scoped signer on the server whose role is not a zone of the policy file.
type outside struct{ zone, key string }

// unknownKey is a signing key of the fetched account JWT that is not one of mashctl's own keys. role is the scoped
// signer's role, "" for a plain signing key.
type unknownKey struct{ key, role string }

func (u unknownKey) describe() string {
	if u.role == "" {
		return "plain signing key " + u.key
	}
	return fmt.Sprintf("scoped signing key %s (role %s)", u.key, u.role)
}

// knownSigningKeys is the set of signing keys mashctl may find in the account JWT: the account identity key and the
// public key of every zone key in the key store.
func knownSigningKeys(ks *ctlkeys.Authority, acctPub string) (map[string]bool, error) {
	known := map[string]bool{acctPub: true}
	zones, err := ks.Zones()
	if err != nil {
		return nil, err
	}
	for _, z := range zones {
		pub, err := ks.ZonePublic(z)
		if err != nil {
			return nil, err
		}
		known[pub] = true
	}
	return known, nil
}

// unknownKeyNote is the one explanation printed with every unknown signing key.
const unknownKeyNote = "mashctl signs the account JWT with the operator key, so it will not carry signing keys it did not create. Remove it with the tool that added it before apply can run. Only a scoped signer whose role is exactly a zone of the policy file can be dropped, by --replace-zone-key naming that zone (or --remove-zone for a zone the file lacks)."

func (b *built) refused() bool {
	return len(b.conflicts) > 0 || len(b.unacked) > 0 || len(b.unknown) > 0
}

// conflict is a scoped signer on the server under a file zone's role whose key is not the zone key apply holds.
type conflict struct {
	zone, serverKey, localKey string // localKey "" means the zone key file is missing
}

// Run executes p and writes its report to out; the result is the process exit code (0 ok, 1 refused or partial; `diff` included: a refusal is exit 1, a report of differences is 0).
func Run(ctx context.Context, p Params, out io.Writer) int {
	pf := func(format string, a ...any) { fmt.Fprintf(out, format, a...) }
	fail := func(err error) int { fmt.Fprintf(out, "mashctl: %s: %v\n", p.Mode, err); return 1 }
	c := p.Compiled

	budget := p.Budget
	if budget == nil {
		budget = policy.Budget
	}
	if rep, err := budget(c); err != nil || !rep.OK {
		if err == nil {
			err = errors.New("over budget")
		}
		return fail(fmt.Errorf("refusing: the policy does not fit: %w (nothing was changed)", err))
	}

	replace := map[string]bool{}
	for _, r := range p.ReplaceZoneKeys {
		found := false
		for _, z := range c.Zones {
			found = found || z.Name == r
		}
		if !found {
			return fail(fmt.Errorf("--replace-zone-key %s: no zone of that name in the policy file (nothing was changed)", r))
		}
		replace[r] = true
	}

	if p.Servers < 0 {
		return fail(fmt.Errorf("--servers must be a positive number, got %d (nothing was changed)", p.Servers))
	}
	opKP, _, err := p.Keys.Operator()
	if err != nil {
		return fail(err)
	}
	defer opKP.Wipe()
	acctKP, acctPub, err := p.Keys.Account()
	if err != nil {
		return fail(err)
	}
	acctKP.Wipe()

	trust, err := LoadTrust(p.Keys)
	if err != nil {
		return fail(fmt.Errorf("refusing: %w (nothing was changed)", err))
	}
	p.trust = trust

	// Applies that share this key directory are serialized, from before the first read until after the read-back.
	if p.Mode == Apply {
		wait := p.LockWait
		if wait <= 0 {
			wait = defaultLockWait
		}
		unlock, lerr := p.Keys.Lock(wait)
		if lerr != nil {
			return fail(fmt.Errorf("%w (nothing was changed)", lerr))
		}
		defer unlock()
	}

	// The servers asked for the base, the push and the read-back: the ping and the inventory say how many there are.
	set, kerr := p.Resolver.Servers(ctx)
	expect := serverExpect(set, p.Servers)

	created := map[string]bool{}
	var b *built
	var warn string
	for attempt := 0; ; attempt++ {
		base, err := lookupBase(ctx, p.Resolver, acctPub, p.trust, expect, "look up the account JWT")
		if err != nil {
			return fail(fmt.Errorf("%w (nothing was changed)", err))
		}
		warn = base.warn
		b, err = build(p, opKP, acctPub, base.token, replace, created)
		if err != nil {
			return fail(err)
		}
		if b.refused() || p.Mode != Apply || !b.changed {
			break
		}
		// Someone else may have pushed since the read: compute again from the newest copy now, once.
		again, err := lookupBase(ctx, p.Resolver, acctPub, p.trust, expect, "look up the account JWT before the push")
		if err != nil {
			return fail(fmt.Errorf("%w (nothing was pushed)", err))
		}
		if again.token == b.cur {
			break
		}
		if attempt == 1 {
			return fail(errors.New("the account JWT changed twice while apply was computing its update: something else is changing it; run the command again (nothing was pushed)"))
		}
	}
	if warn != "" {
		pf("warning: %s\n", warn)
	}
	var createdList []string
	for _, z := range c.Zones {
		if created[z.Name] {
			createdList = append(createdList, z.Name)
		}
	}
	plans, desired, changed, token, cur := b.plans, b.desired, b.changed, b.token, b.cur

	if b.refused() {
		refuse(out, p, b)
		return 1
	}

	report(out, p, plans, b, len(cur), len(token), changed)

	if p.Mode != Apply {
		if changed {
			pf("\n%s: dry run, nothing was changed on the server and no key was created.\n", p.Mode)
		} else {
			pf("\n%s: the account claims on the resolver's copy already match the file; no server was asked (apply confirms every server).\n", p.Mode)
		}
		notCovered(out, p)
		pf("%s\n", NotATransaction)
		return 0
	}
	for _, z := range createdList {
		pf("created the signing key of zone %s (a local file in the key store)\n", z)
	}

	// The set of servers that must acknowledge, then push, then read back. With no change the claims are not
	// re-signed: the JWT the resolver already has (same token, same iat) is sent again, so that every server
	// acknowledges it or is named. One resolver answering with the new claims does not show that the others hold
	// them, and a retry after a partial push is how a lagging server is repaired.
	what := "the claims update"
	if !changed {
		what = "the re-send of the unchanged claims"
	}
	acks, why := pushAndCheck(ctx, p.Resolver, token, set.Names)
	if why != "" {
		return partial(out, p, createdList, fmt.Sprintf("%s was not acknowledged: %s", what, why), verifyState(ctx, p, acctPub, token, desired, expect))
	}
	if msg := verifyState(ctx, p, acctPub, token, desired, expect); msg != "" {
		return partial(out, p, createdList, fmt.Sprintf("%d server(s) acknowledged the claims update", len(acks)), msg)
	}
	if !set.Complete(p.Servers) {
		why := set.Why(p.Servers)
		if kerr != nil {
			why = kerr.Error()
		}
		return unconfirmed(out, p, createdList, len(acks), why, changed)
	}
	if !changed {
		// "already up to date" is a claim about every server: each acknowledged the re-send (above), and each now
		// answers the lookup with this very JWT.
		if msg := verifyAll(ctx, p, acctPub, set.need(p.Servers), token); msg != "" {
			return partial(out, p, createdList, "every server acknowledged the re-send of the unchanged claims, but they do not all hold it", msg)
		}
		pf("\n%s: account claims already up to date: the claims are unchanged, and all %d server(s) acknowledged the unchanged JWT sent again and the resolver answers with it. This covers the account claims only, not user credentials already issued.\n", p.Mode, len(acks))
		notCovered(out, p)
		pf("%s\n", NotATransaction)
		return 0
	}
	pf("\npushed: %d server(s) acknowledged the claims update and the resolver now answers with the new signing keys (%d zones).\n", len(acks), len(plans))
	notCovered(out, p)
	pf("%s\n", NotATransaction)
	return 0
}

// pushAndCheck pushes token to the resolver and checks the answers against known (the servers the ping found): every
// known server must have acknowledged with code 200 and at least one must have answered. why is "" when all did.
func pushAndCheck(ctx context.Context, r Resolver, token string, known []string) (acks []ServerAck, why string) {
	acks, perr := r.Push(ctx, token, known)
	acked := map[string]bool{}
	var bad []string
	for _, a := range acks {
		acked[a.Server] = true
		if !a.OK() {
			bad = append(bad, fmt.Sprintf("%s: code %d %s", serverName(a), a.Code, a.Message))
		}
	}
	var silent []string
	for _, k := range known {
		if !acked[k] {
			silent = append(silent, k)
		}
	}
	if perr == nil && len(bad) == 0 && len(acks) > 0 && len(silent) == 0 {
		return acks, ""
	}
	var parts []string
	switch {
	case perr != nil:
		parts = append(parts, perr.Error())
	case len(acks) == 0:
		parts = append(parts, "no server answered")
	}
	if len(bad) > 0 {
		parts = append(parts, "refused by "+strings.Join(bad, "; "))
	}
	if len(silent) > 0 {
		parts = append(parts, "no acknowledgement from "+strings.Join(silent, ", ")+" (known from the system account ping)")
	}
	return acks, strings.Join(parts, "; ")
}

// build computes the desired account JWT from cur. It creates missing zone keys only when nothing conflicts and the
// mode is Apply; created collects their zone names.
func build(p Params, opKP nkeys.KeyPair, acctPub, cur string, replace, created map[string]bool) (*built, error) {
	c := p.Compiled
	if cur == "" {
		return nil, fmt.Errorf("the resolver has no JWT for the MASH account %s: is the account key in the key store the account of this server? (nothing was changed)", acctPub)
	}
	current, err := VerifyAccountJWT(cur, acctPub, p.trust)
	if err != nil {
		return nil, fmt.Errorf("refusing the resolver's account JWT: %w (nothing was changed)", err)
	}
	desired, err := jwt.DecodeAccountClaims(cur)
	if err != nil {
		return nil, err
	}
	if desired.SigningKeys == nil {
		desired.SigningKeys = jwt.SigningKeys{}
	}
	b := &built{cur: cur, desired: desired}

	// A scoped signer on the server under a file zone's role but with another key than the zone key apply holds
	// would keep its old permissions beside the new policy: refuse, or replace it when asked. Nothing is created yet.
	localPub := map[string]string{}
	isFileZone := map[string]bool{}
	for _, z := range c.Zones {
		isFileZone[z.Name] = true
		kp, kerr := p.Keys.ZoneKey(z.Name, false)
		switch {
		case kerr == nil:
			localPub[z.Name], kerr = kp.PublicKey()
			kp.Wipe()
			if kerr != nil {
				return nil, kerr
			}
		case errors.Is(kerr, ctlkeys.ErrNoZoneKey):
		default:
			return nil, kerr
		}
	}
	// Known keys: the account identity key and every zone key in the key store (the zone keys apply creates are not
	// on the server yet). Any other signing key of the JWT is unknown and refused.
	known, kerr2 := knownSigningKeys(p.Keys, acctPub)
	if kerr2 != nil {
		return nil, kerr2
	}
	oldKeys := map[string][]string{}
	remove, keep := map[string]bool{}, map[string]bool{}
	for _, z := range p.RemoveZones {
		remove[z] = true
	}
	for _, z := range p.KeepZones {
		if remove[z] {
			return nil, fmt.Errorf("zone %s is named by both --remove-zone and --keep-zone (nothing was changed)", z)
		}
		keep[z] = true
	}
	onServer := map[string]bool{}
	for _, k := range sortedKeys(current.SigningKeys) {
		sc, _ := current.SigningKeys.GetScope(k)
		us, _ := sc.(*jwt.UserScope)
		switch {
		case us != nil && isFileZone[us.Role]:
			// a zone of the file: handled below (the zone key, a replacement, or a conflict)
		case !known[k]:
			u := unknownKey{key: k}
			if us != nil {
				u.role = us.Role
				onServer[us.Role] = true
			}
			b.unknown = append(b.unknown, u)
			continue
		case us == nil:
			continue // a plain signing key that is one of mashctl's own
		}
		if !isFileZone[us.Role] {
			// A zone the file lacks (or a scoped signer mash did not create): its key and old permissions would stay
			// active outside the policy, so the operator must say what to do with it.
			onServer[us.Role] = true
			switch {
			case remove[us.Role]:
				b.removed = append(b.removed, outside{us.Role, k})
			case keep[us.Role]:
				if len(b.kept) == 0 || b.kept[len(b.kept)-1] != us.Role {
					b.kept = append(b.kept, us.Role)
				}
			default:
				b.unacked = append(b.unacked, outside{us.Role, k})
			}
			continue
		}
		if k == localPub[us.Role] {
			continue
		}
		if replace[us.Role] {
			oldKeys[us.Role] = append(oldKeys[us.Role], k)
			continue
		}
		b.conflicts = append(b.conflicts, conflict{zone: us.Role, serverKey: k, localKey: localPub[us.Role]})
	}
	for _, flag := range []struct {
		name  string
		zones []string
	}{{"--remove-zone", p.RemoveZones}, {"--keep-zone", p.KeepZones}} {
		for _, z := range flag.zones {
			switch {
			case isFileZone[z]:
				return nil, fmt.Errorf("%s %s: zone %s is in the policy file, so it is not outside the policy (nothing was changed)", flag.name, z, z)
			case !onServer[z]:
				return nil, fmt.Errorf("%s %s: no scoped signing key with that role is on the server (nothing was changed)", flag.name, z)
			}
		}
	}
	if b.refused() && !p.collectAll {
		return b, nil
	}
	conflictZone := map[string]bool{}
	for _, cf := range b.conflicts {
		conflictZone[cf.zone] = true
	}

	// Zone keys: apply creates the missing ones, a dry run reports them and uses a throwaway public key for sizing.
	for _, z := range c.Zones {
		if conflictZone[z.Name] {
			continue
		}
		zp := &zonePlan{name: z.Name}
		kp, kerr := p.Keys.ZoneKey(z.Name, false)
		switch {
		case kerr == nil:
		case errors.Is(kerr, ctlkeys.ErrNoZoneKey):
			zp.keyIsNew = true
			if p.Mode == Apply {
				kp, kerr = p.Keys.ZoneKey(z.Name, true)
				if kerr != nil {
					return nil, kerr
				}
				created[z.Name] = true
			} else if kp, kerr = nkeys.CreateAccount(); kerr != nil {
				return nil, kerr
			}
		default:
			return nil, kerr
		}
		zp.pub, err = kp.PublicKey()
		kp.Wipe()
		if err != nil {
			return nil, err
		}
		scope := jwt.NewUserScope()
		scope.Key = zp.pub
		scope.Role = z.Name
		scope.Template.Permissions = z.Perms
		zp.scope = scope

		have, _ := current.SigningKeys.GetScope(zp.pub)
		old, _ := have.(*jwt.UserScope)
		switch {
		case len(oldKeys[z.Name]) > 0:
			zp.replaced = oldKeys[z.Name]
			zp.state = zoneChanged
			hv, _ := current.SigningKeys.GetScope(zp.replaced[0])
			ov, _ := hv.(*jwt.UserScope)
			zp.changes = permDiff(ov, scope)
		case old == nil:
			zp.state = zoneNew
			zp.changes = permDiff(nil, scope)
		case scopeJSON(old) == scopeJSON(scope):
			zp.state = zoneUnchanged
		default:
			zp.state = zoneChanged
			zp.changes = permDiff(old, scope)
			zp.otherDiff = len(zp.changes) == 0
		}
		for _, o := range zp.replaced {
			delete(desired.SigningKeys, o)
		}
		desired.SigningKeys[zp.pub] = scope
		b.plans = append(b.plans, zp)
	}

	for _, o := range b.removed {
		delete(desired.SigningKeys, o.key)
	}
	b.changed = len(b.removed) > 0
	for _, zp := range b.plans {
		b.changed = b.changed || zp.state != zoneUnchanged
	}
	b.token = cur
	if b.changed {
		b.token, err = desired.Encode(opKP)
		if err != nil {
			return nil, fmt.Errorf("encode the account JWT: %w (nothing was changed)", err)
		}
		if len(b.token) > policy.AccountJWTLimit {
			return nil, fmt.Errorf("the account JWT would be %d bytes, over the %d byte limit: fewer zones, or fewer zone pairs granted (nothing was changed)", len(b.token), policy.AccountJWTLimit)
		}
	}
	return b, nil
}

// refuse prints the refusals (public keys only) and the ways out: zone key conflicts, and zones on the server that the
// policy file lacks.
func refuse(out io.Writer, p Params, b *built) {
	cs, outs := b.conflicts, b.unacked
	if len(b.unknown) > 0 {
		fmt.Fprintf(out, "%s: REFUSED: the account JWT holds signing keys that mashctl did not create (nothing was changed, no key was created, nothing was pushed)\n", p.Mode)
		for _, u := range b.unknown {
			fmt.Fprintf(out, "  %s\n", u.describe())
		}
		fmt.Fprintln(out, unknownKeyNote)
	}
	if len(cs) > 0 {
		fmt.Fprintf(out, "%s: REFUSED: the server has a signing key for a zone that is not the zone key in the key store (nothing was changed, no key was created)\n", p.Mode)
		for _, c := range cs {
			local := c.localKey
			if local == "" {
				local = "none (the zone key file is missing: apply would create a new key)"
			}
			fmt.Fprintf(out, "  zone %s: the server's signing key is %s, the key store's is %s\n", c.zone, c.serverKey, local)
		}
		fmt.Fprintln(out, "applying anyway would leave the server's old key in place: credentials signed by it keep their old permissions and bypass the policy.")
		fmt.Fprintln(out, "either restore the original zone key file, or run apply with --replace-zone-key ZONE: it removes the old key and installs the new one in the same account JWT, and credentials signed by the old key stop working.")
	}
	if len(outs) > 0 {
		fmt.Fprintf(out, "%s: REFUSED unless each zone below is named by --remove-zone or --keep-zone: the server has a signing key for a zone that is not in the policy file (nothing was changed, no key was created)\n", p.Mode)
		seen := map[string]bool{}
		for _, o := range outs {
			fmt.Fprintf(out, "  zone %s: signing key %s stays active with its old permissions, outside the policy\n", o.zone, o.key)
			if !seen[o.zone] {
				seen[o.zone] = true
				fmt.Fprintf(out, "      REFUSED unless --remove-zone %s or --keep-zone %s\n", o.zone, o.zone)
			}
		}
		fmt.Fprintln(out, "--remove-zone ZONE removes that signing key from the account JWT in this apply: users signed by that key stop working (the local zone key file is not deleted).")
		fmt.Fprintln(out, "--keep-zone ZONE says the key is deliberately kept active outside the policy: apply proceeds and warns.")
	}
}

// unconfirmed prints that the update was acknowledged by the servers that were heard from but that the full server set
// could not be established, and returns exit code 1.
func unconfirmed(out io.Writer, p Params, created []string, acked int, why string, changed bool) int {
	fmt.Fprintf(out, "\nUNCONFIRMED: the server set could not be verified (%s)\n", why)
	fmt.Fprintln(out, "done:")
	if changed {
		fmt.Fprintf(out, "  the claims update was pushed; %d server(s) that replied acknowledged it and the resolver answers with the new signing keys\n", acked)
	} else {
		fmt.Fprintf(out, "  the claims were already current on the resolver; the unchanged JWT was sent again and %d server(s) that replied acknowledged it\n", acked)
	}
	if len(created) > 0 {
		fmt.Fprintf(out, "  zone keys created locally: %s\n", strings.Join(created, ", "))
	}
	fmt.Fprintln(out, "not confirmed:")
	fmt.Fprintln(out, "  servers that did not reply may still hold the old claims")
	fmt.Fprintln(out, "retry: mashctl policy apply   (idempotent: running it again is safe; check the other servers if there are any)")
	fmt.Fprintf(out, "%s\n", NotATransaction)
	return 1
}

// partial prints what was and was not done and returns exit code 1. observed is what the read-back found ("" is a
// match).
func partial(out io.Writer, p Params, created []string, step, observed string) int {
	fmt.Fprintf(out, "\nPARTIAL: %s\n", step)
	fmt.Fprintln(out, "done:")
	if len(created) > 0 {
		fmt.Fprintf(out, "  zone keys created locally: %s\n", strings.Join(created, ", "))
	} else {
		fmt.Fprintln(out, "  nothing that persists (no zone key was created)")
	}
	fmt.Fprintln(out, "not done or not confirmed:")
	if observed == "" {
		fmt.Fprintln(out, "  the resolver answers with the new claims, but the update was not acknowledged by every server as required")
	} else {
		fmt.Fprintf(out, "  the account JWT on the resolver: %s\n", observed)
	}
	fmt.Fprintln(out, "  user JWTs were not touched")
	fmt.Fprintln(out, "retry: mashctl policy apply   (idempotent: it sends the claims again to every server, so a server that missed the update is repaired; mashctl policy diff shows what differs)")
	fmt.Fprintf(out, "%s\n", NotATransaction)
	return 1
}

// verifyState reads the account JWT back from every server until each answering one holds token (the pushed JWT); ""
// is a match, else the description of what was found. A server that still holds another copy, or none, is not a match.
func verifyState(ctx context.Context, p Params, acctPub, token string, desired *jwt.AccountClaims, need int) string {
	return readBack(ctx, p.Resolver, acctPub, p.trust, need, p.VerifyTries, p.VerifyWait, token, func(got *jwt.AccountClaims) string {
		return signersDiff(desired, got)
	})
}

// signersDiff compares the signing keys (scoped ones with their role and template) of want and got.
func signersDiff(want, got *jwt.AccountClaims) string {
	var miss, extra, differ []string
	for _, k := range sortedKeys(want.SigningKeys) {
		ws, _ := want.SigningKeys.GetScope(k)
		gs, ok := got.SigningKeys.GetScope(k)
		if !ok {
			miss = append(miss, label(k, ws))
			continue
		}
		wu, _ := ws.(*jwt.UserScope)
		gu, _ := gs.(*jwt.UserScope)
		if (wu == nil) != (gu == nil) || (wu != nil && scopeJSON(wu) != scopeJSON(gu)) {
			differ = append(differ, label(k, ws))
		}
	}
	for _, k := range sortedKeys(got.SigningKeys) {
		if _, ok := want.SigningKeys.GetScope(k); !ok {
			gs, _ := got.SigningKeys.GetScope(k)
			extra = append(extra, label(k, gs))
		}
	}
	var parts []string
	if len(miss) > 0 {
		parts = append(parts, "missing signing keys: "+strings.Join(miss, ", "))
	}
	if len(differ) > 0 {
		parts = append(parts, "templates differ for: "+strings.Join(differ, ", "))
	}
	if len(extra) > 0 {
		parts = append(parts, "unexpected signing keys: "+strings.Join(extra, ", "))
	}
	return strings.Join(parts, "; ")
}

func label(key string, s jwt.Scope) string {
	if us, ok := s.(*jwt.UserScope); ok && us != nil {
		return "zone " + us.Role
	}
	return key
}

func serverName(a ServerAck) string {
	if a.Server == "" {
		return "a server"
	}
	return a.Server
}

func sortedKeys(sk jwt.SigningKeys) []string {
	k := sk.Keys()
	sort.Strings(k)
	return k
}

// scopeJSON is the JSON of a copy of s with its permission lists sorted, so equal sets compare equal.
func scopeJSON(s *jwt.UserScope) string {
	cp := *s
	sortList := func(l jwt.StringList) jwt.StringList {
		out := append(jwt.StringList(nil), l...)
		sort.Strings(out)
		return out
	}
	cp.Template.Pub.Allow, cp.Template.Pub.Deny = sortList(s.Template.Pub.Allow), sortList(s.Template.Pub.Deny)
	cp.Template.Sub.Allow, cp.Template.Sub.Deny = sortList(s.Template.Sub.Allow), sortList(s.Template.Sub.Deny)
	b, err := json.Marshal(cp)
	if err != nil {
		return fmt.Sprintf("%#v", cp)
	}
	return string(b)
}

// permDiff lists the added ("+") and removed ("-") permission lines going from old (nil: none) to now.
func permDiff(old, now *jwt.UserScope) []string {
	lists := func(s *jwt.UserScope) map[string][]string {
		m := map[string][]string{}
		if s == nil {
			return m
		}
		m["pub allow"] = s.Template.Pub.Allow
		m["pub deny"] = s.Template.Pub.Deny
		m["sub allow"] = s.Template.Sub.Allow
		m["sub deny"] = s.Template.Sub.Deny
		return m
	}
	o, n := lists(old), lists(now)
	var out []string
	for _, k := range []string{"pub allow", "pub deny", "sub allow", "sub deny"} {
		oset, nset := toSet(o[k]), toSet(n[k])
		for _, l := range sortedSet(nset) {
			if !oset[l] {
				out = append(out, fmt.Sprintf("%-9s + %s", k, l))
			}
		}
		for _, l := range sortedSet(oset) {
			if !nset[l] {
				out = append(out, fmt.Sprintf("%-9s - %s", k, l))
			}
		}
	}
	return out
}

func toSet(l []string) map[string]bool {
	m := make(map[string]bool, len(l))
	for _, s := range l {
		m[s] = true
	}
	return m
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for s := range m {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// report prints the diff shared by apply, --dry-run and diff.
func report(out io.Writer, p Params, plans []*zonePlan, b *built, before, after int, changed bool) {
	fmt.Fprintf(out, "%s: %d zone(s) in the file\n", p.Mode, len(plans))
	for _, zp := range plans {
		switch zp.state {
		case zoneUnchanged:
			fmt.Fprintf(out, "  zone %s: unchanged\n", zp.name)
		case zoneNew:
			note := ""
			if zp.keyIsNew {
				if p.Mode == Apply {
					note = " (its signing key is created now)"
				} else {
					note = " (would create its signing key)"
				}
			}
			fmt.Fprintf(out, "  zone %s: new%s\n", zp.name, note)
		case zoneChanged:
			fmt.Fprintf(out, "  zone %s: changed\n", zp.name)
		}
		for _, o := range zp.replaced {
			fmt.Fprintf(out, "      replaces the signing key %s (role %s) on the explicit --replace-zone-key %s: it is dropped from the account JWT, and credentials signed by it, by mashctl or by any other tool, stop working\n", o, zp.name, zp.name)
		}
		for _, l := range zp.changes {
			fmt.Fprintf(out, "      %s\n", l)
		}
		if zp.otherDiff {
			fmt.Fprintln(out, "      the permission lines are the same; the role or limits of the key differ")
		}
	}
	for _, o := range b.removed {
		fmt.Fprintf(out, "  zone %s is not in the file: removes its signing key %s (--remove-zone): users signed by it stop working (the local zone key file is not deleted)\n", o.zone, o.key)
	}
	for _, z := range b.kept {
		fmt.Fprintf(out, "  warning: zone %s is outside the policy and its key stays active (--keep-zone)\n", z)
	}
	fmt.Fprintf(out, "account JWT: %d bytes now, %d after, limit %d\n", before, after, policy.AccountJWTLimit)
	tagged := 0
	for _, e := range p.Compiled.Endpoints {
		if hasExceptionTags(e) {
			tagged++
		}
	}
	fmt.Fprintf(out, "endpoints: %d in the file, %d carry exception tags\n", len(p.Compiled.Endpoints), tagged)
	if changed && tagged > 0 {
		for _, zp := range plans {
			if zp.state != zoneUnchanged {
				fmt.Fprintln(out, "user JWTs carrying changed exception tags must be re-issued with mashctl reissue")
				break
			}
		}
	}
}

// hasExceptionTags reports whether the endpoint carries a real exception tag: every user carries the placeholder
// admin.NoDenyTag (ADR 0056), which is not an exception.
func hasExceptionTags(e policy.CompiledEndpoint) bool {
	for _, t := range e.Tags {
		if t != admin.NoDenyTag {
			return true
		}
	}
	return false
}

// notCovered prints the fixed block of what apply does not do, when the policy has endpoints. It is printed only on
// success, diff and dry-run, never on a refusal or an error.
func notCovered(out io.Writer, p Params) {
	eps := p.Compiled.Endpoints
	if len(eps) == 0 {
		return
	}
	var tagged []string
	for _, e := range eps {
		if hasExceptionTags(e) {
			tagged = append(tagged, e.Name)
		}
	}
	sort.Strings(tagged)
	a := "user credentials issued before this apply keep the permissions of the key they were signed with and the exception tags they carry: an allow/deny tag change takes effect only when that endpoint's user JWT is re-issued (mashctl reissue), and apply cannot see issued tags"
	if len(tagged) > 0 {
		names := tagged
		more := ""
		if len(names) > 5 {
			names, more = names[:5], ", ..."
		}
		a += fmt.Sprintf(" (%d endpoint(s) carry exception tags: %s%s)", len(tagged), strings.Join(names, ", "), more)
	}
	fmt.Fprintln(out, "Not covered by apply:")
	fmt.Fprintln(out, "  - "+a)
	fmt.Fprintln(out, "  - credentials minted by other tools (for example nsc) under the account key keep their own permissions until revoked: no rule is enforced on them")
	fmt.Fprintln(out, "  - the directory records, the key store and the creds files are compared with the file by mashctl policy drift (it cannot see credentials minted by other tools either)")
}
