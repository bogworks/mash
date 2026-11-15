// Package reconcile is the first-list-set reconcile of an adopted account (AT-015, ADR 0111): it plans, and with a
// signer executes, the removal of what the account held before its first list set that the list set does not own.
//
// Half (a), signing keys: a signing key of the account JWT that is not a scoped key the key store holds for an owner
// group (of this range or any other, see KeptSigners) is a non-mash key. The gate of the first applied list set
// refuses extra signers, so the apply removes them; the plan reports each one still present in the JWT in force as a
// finding, and with a list set from a file, before the first apply, each one the apply would remove. It never pushes for a signing key.
//
// Half (b), revocations: a recorded issued user whose sender is not in the list set is revoked, and so is a connected
// user that is neither recorded, exempt nor issued under the list set, each only when the issue record shows the
// evidence it needs (see Build). Everything the plan cannot name or prove is a finding and is left alone.
package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// Source says which list set the plan was computed against.
type Source string

const (
	// SourceInForce is the list set in force in the ledger (generation 1 or later). Only such a plan can be executed.
	SourceInForce Source = "in_force"
	// SourceListSetFile is a list set read from a file before it is applied: a preview, never executed.
	SourceListSetFile Source = "listset_file"
)

// The states of a signing key line.
const (
	StateStillPresent   = "still_present"
	StateRemovedByApply = "removed_by_first_apply"
)

// The reasons of a revocation.
const (
	ReasonRecorded  = "recorded, sender not in the list set"
	ReasonUntracked = "connected, untracked"
)

// IssueSource is the issue record as the planner reads it. *admin.Admin is one.
type IssueSource interface {
	Issued(ctx context.Context) ([]store.Issued, bool, error)
	IssuedHistoryByKey(ctx context.Context, publicKey string) (admin.IssuedTimes, bool, error)
	IssuedBucketProof(ctx context.Context) (ok bool, why string, err error)
}

// Resolver resolves an address to the directory record that serves it. *admin.Admin is one (ServingIdentity).
type Resolver interface {
	ServingIdentity(ctx context.Context, id sap.EndpointIdentity) (target sap.EndpointIdentity, legacy bool, found bool, err error)
}

// Input is everything the planner needs. It does no I/O of its own beyond reading Issued.
type Input struct {
	Account string
	// Now is the plan's clock; a revocation is at max(Now, the connected JWT's iat).
	Now    time.Time
	Source Source
	// Generation is the generation in force (0 for a list set from a file).
	Generation uint64
	ListSet    listset.ListSet
	// Claims is the account JWT whose signing keys are examined: the one in force, or for a file the one the servers hold.
	Claims *jwt.AccountClaims
	// Signers maps each template group of the list set to its scoped signing key (see ExpectedSigners).
	Signers map[string]string
	// Held is the key store's GroupPublics: every owner group of every range to its public key. A scoped signing key
	// held there is mash's, whatever its range, and is kept (see KeptSigners).
	Held map[string]string
	// Exempt is the adoption marker's exempt users: never in any action.
	Exempt []string
	// Servers is what every server's CONNZ said for the account; ScanIncomplete is non-empty when that is not the whole
	// picture (a server did not answer or its list was not read whole).
	Servers        []apply.ConnzServer
	ScanIncomplete string
	Issued         IssueSource
	// Resolver maps each sender of the list set to the directory record that serves it, so that an issued record made
	// under a legacy directory name (`mashctl add`: Address "l1", Owner "h1") is the sender lab.h1.l1's. Nil: a record
	// is the sender's only when its Address is the sender's address as the list set spells it.
	Resolver Resolver
	// AdminUser and AdminIssuer are the public key and the `iss` of the admin credentials' user JWT.
	AdminUser, AdminIssuer string
}

// Conn is one connection of a user, as CONNZ showed it.
type Conn struct {
	Server string `json:"server"`
	Host   string `json:"host"`
	// Name is the name claim of the user's JWT; Client is the connection name the client sent.
	Name   string `json:"name"`
	Client string `json:"client"`
	CID    uint64 `json:"cid"`
}

// SignedUser is a connected user a signing key signed.
type SignedUser struct {
	Key  string `json:"key"`
	Conn Conn   `json:"connection"`
}

// SigningKey is a non-mash signing key of the account JWT.
type SigningKey struct {
	Key    string       `json:"key"`
	State  string       `json:"state"`
	Signed []SignedUser `json:"signed"`
}

// Revocation is one user key the reconcile revokes.
type Revocation struct {
	Key string `json:"key"`
	// Address is the sender address of the issue record ("" for an unrecorded key).
	Address string `json:"address"`
	Reason  string `json:"reason"`
	// At is the revocation time, RFC 3339 UTC: max(now, the connected JWT's iat when known).
	At          string `json:"at"`
	Connections []Conn `json:"connections"`
}

// Finding is something the plan cannot act on and leaves alone. Key is "" when the finding is not about one key.
type Finding struct {
	Key    string `json:"key"`
	Detail string `json:"detail"`
}

// Plan is the reconcile's plan. Its JSON is stable: snake_case, sorted, slices never null.
type Plan struct {
	Account        string                `json:"account"`
	Source         Source                `json:"source"`
	Generation     uint64                `json:"generation"`
	SigningKeys    []SigningKey          `json:"signing_keys"`
	Revocations    []Revocation          `json:"revocations"`
	Findings       []Finding             `json:"findings"`
	IssuerProblems []admin.IssuerProblem `json:"issuer_problems"`
	// Actions is the number of revocations; Unknown the findings plus the signing keys still present.
	Actions int `json:"actions"`
	Unknown int `json:"unknown"`
	// Status is the last line of the text form: reconciled: nothing to do, N actions, or partial (N unknown).
	Status string `json:"status"`
	// PlanDigest is Digest() of a plan against the list set in force ("" for a preview, which is never executed). It is
	// what `--yes --plan` names.
	PlanDigest string `json:"plan_digest,omitempty"`
}

// Digest is "sha256:" and the hex SHA-256 of the plan's canonical JSON, which `--yes --plan` must name. The canonical
// form is the plan's decision and nothing that moves between two runs of the same state: the account, source and
// generation; each non-mash signing key with its state; each revocation's key, sender and reason; the findings; the
// issuer problems; and the status. It leaves out every time (a revocation's `at` is the later of now and a JWT's iat, so
// it differs on every run) and the connection details (connection ids, hosts, which users a key signed): a client that
// reconnects does not change what is revoked. The slices are already sorted, so the same state gives the same digest.
func (p Plan) Digest() string {
	type revocation struct {
		Key     string `json:"key"`
		Address string `json:"address"`
		Reason  string `json:"reason"`
	}
	type signingKey struct {
		Key   string `json:"key"`
		State string `json:"state"`
	}
	c := struct {
		Account        string                `json:"account"`
		Source         Source                `json:"source"`
		Generation     uint64                `json:"generation"`
		SigningKeys    []signingKey          `json:"signing_keys"`
		Revocations    []revocation          `json:"revocations"`
		Findings       []Finding             `json:"findings"`
		IssuerProblems []admin.IssuerProblem `json:"issuer_problems"`
		Status         string                `json:"status"`
	}{p.Account, p.Source, p.Generation, []signingKey{}, []revocation{}, p.Findings, p.IssuerProblems, p.Status}
	for _, k := range p.SigningKeys {
		c.SigningKeys = append(c.SigningKeys, signingKey{k.Key, k.State})
	}
	for _, r := range p.Revocations {
		c.Revocations = append(c.Revocations, revocation{r.Key, r.Address, r.Reason})
	}
	if c.Findings == nil {
		c.Findings = []Finding{}
	}
	if c.IssuerProblems == nil {
		c.IssuerProblems = []admin.IssuerProblem{}
	}
	b, _ := json.Marshal(c) // fixed struct of strings and numbers: cannot fail
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// IssuerErr is nil when the admin user and every connected exempt user keep their issuer (admin.CheckIssuersKept).
func (p Plan) IssuerErr() error {
	if len(p.IssuerProblems) == 0 {
		return nil
	}
	return &admin.IssuersNotKeptError{Problems: p.IssuerProblems}
}

// ExpectedSigners maps each group to the scoped signing key that is mash's for it: the key the key store holds for the
// group (held, from GroupPublics). The key store is the authority, as in the policy signer: the account JWT is never
// asked which key is mash's, because a key that merely carries the group's role is not mash's unless the key store
// holds it. A group with no held key is an error, and nothing is concluded.
func ExpectedSigners(groups []string, held map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for _, g := range groups {
		k := held[g]
		if k == "" {
			return nil, fmt.Errorf("the key store holds no signing key for group %s: run the reconcile where the range's owner group keys are (the account JWT is not used to guess which key is mash's)", g)
		}
		out[g] = k
	}
	return out, nil
}

// OtherRangesKept is the scoped signing keys of the account JWT that stay beside the target's: every scoped key
// (*jwt.UserScope) the key store holds (held, from GroupPublics) for an owner group of ANOTHER range (a group that is not
// rng's: "rng.<name>"), group to public key. It is how policyserve.Issuer keeps them. A key of this range's is
// rebuilt from the list set, so one the list set no longer names is not in it. rng is the list set's range.
func OtherRangesKept(claims *jwt.AccountClaims, held map[string]string, rng string) (map[string]string, error) {
	if rng == "" {
		return nil, errors.New("no range: cannot tell this range's signing keys from another range's (the list set names none)")
	}
	out := map[string]string{}
	if claims == nil {
		return out, nil
	}
	for g, pub := range held {
		if strings.HasPrefix(g, rng+".") {
			continue
		}
		if sc, _ := claims.SigningKeys.GetScope(pub); sc != nil {
			if us, ok := sc.(*jwt.UserScope); ok && us != nil {
				out[g] = pub
			}
		}
	}
	return out, nil
}

// KeptSigners is the account JWT's signing keys that are mash's and stay once the list set is applied, as
// policyserve.Issuer.Sign keeps them: the list set's expected signers (the target's group keys) and the scoped keys the
// key store holds for another range's owner groups (OtherRangesKept). A key of this range's that the list set no longer
// names goes, so it is not kept. Every other signing key, a scoped key the key store does not hold included, is non-mash.
func KeptSigners(claims *jwt.AccountClaims, held, signers map[string]string, rng string) (map[string]bool, error) {
	other, err := OtherRangesKept(claims, held, rng)
	if err != nil {
		return nil, err
	}
	kept := map[string]bool{}
	for _, k := range signers {
		kept[k] = true
	}
	for _, k := range other {
		kept[k] = true
	}
	return kept, nil
}

// Groups is the owner groups of the list set's compiled templates, sorted.
func Groups(ls listset.ListSet) ([]string, error) {
	c, err := listset.Compile(ls)
	if err != nil {
		return nil, err
	}
	var gs []string
	for _, t := range c.Templates {
		gs = append(gs, t.Group)
	}
	slices.Sort(gs)
	return gs, nil
}

// conn is a connection with the claims the planner needs.
type conn struct {
	Conn
	issuer string
	iat    int64
}

// Build computes the plan. A read of the issue record that fails is an error; everything else the record cannot show
// is a finding.
//
// The safety rules, each a unit test:
//   - a recorded key is revoked only when its issue-record history is shown complete (admin.IssuedHistoryByKey, ADR
//     0115: the bucket's history marker, no revision at the cap) and no revision is future-dated; otherwise it is a
//     finding and the key is untouched;
//   - a connected unrecorded key whose JWT issuer is one of the list set's scoped keys is never revoked as untracked: it
//     is a finding, a list-set credential without its record;
//   - an unrecorded key is untracked only when the issue bucket proves no record was lost (admin.IssuedBucketProof: it
//     denies deletes and purges and carries the history marker full or raised:N:S); otherwise every unrecorded
//     connected key is a finding;
//   - an exempt key is in no action and no finding;
//   - the revocation time is max(now, the connected JWT's iat when known).
func Build(ctx context.Context, in Input) (Plan, error) {
	c, err := listset.Compile(in.ListSet)
	if err != nil {
		return Plan{}, fmt.Errorf("the list set: %w", err)
	}
	senders := map[string]bool{}
	for _, u := range c.Users {
		senders[u.Sender] = true
	}
	sm := resolveSenders(ctx, in.Resolver, c.Users)
	scoped := map[string]bool{}
	for _, k := range in.Signers {
		scoped[k] = true
	}
	kept, err := KeptSigners(in.Claims, in.Held, in.Signers, in.ListSet.Range)
	if err != nil {
		return Plan{}, err
	}
	adminUnrecorded := false
	exempt := map[string]bool{}
	for _, k := range in.Exempt {
		exempt[k] = true
	}
	// The admin user key in use is exempt whether or not the marker records it (the credentials may have been rotated
	// after adoption): it is never in any action. A marker that lacks it is a finding.
	if in.AdminUser != "" && !exempt[in.AdminUser] {
		exempt[in.AdminUser] = true
		adminUnrecorded = true
	}
	p := Plan{Account: in.Account, Source: in.Source, Generation: in.Generation,
		SigningKeys: []SigningKey{}, Revocations: []Revocation{}, Findings: []Finding{}, IssuerProblems: []admin.IssuerProblem{}}

	// What is connected, by user key.
	byKey := map[string][]conn{}
	for _, s := range in.Servers {
		for _, cc := range s.Conns {
			if cc.Key == "" {
				continue
			}
			byKey[cc.Key] = append(byKey[cc.Key], conn{
				Conn:   Conn{Server: s.Name, Host: cc.IP, Name: cc.UserName, Client: cc.Name, CID: cc.CID},
				issuer: cc.IssuerKey, iat: cc.IssuedAt})
		}
	}
	for _, cs := range byKey {
		slices.SortFunc(cs, func(a, b conn) int {
			if x := strings.Compare(a.Server, b.Server); x != 0 {
				return x
			}
			return int(a.CID) - int(b.CID)
		})
	}

	// Half (a): the signing keys that are not the list set's.
	if in.Claims != nil {
		state := StateStillPresent
		if in.Source == SourceListSetFile {
			state = StateRemovedByApply
		}
		for _, k := range slices.Sorted(maps.Keys(in.Claims.SigningKeys)) {
			if kept[k] {
				continue
			}
			sk := SigningKey{Key: k, State: state, Signed: []SignedUser{}}
			for _, u := range slices.Sorted(maps.Keys(byKey)) {
				for _, cn := range byKey[u] {
					if cn.issuer == k {
						sk.Signed = append(sk.Signed, SignedUser{Key: u, Conn: cn.Conn})
					}
				}
			}
			p.SigningKeys = append(p.SigningKeys, sk)
		}
	}

	// The issue record.
	recs, _, err := in.Issued.Issued(ctx)
	if err != nil {
		return Plan{}, err
	}
	recorded := map[string]store.Issued{}
	for _, r := range recs {
		recorded[r.PublicKey] = r
	}
	proven, why, err := in.Issued.IssuedBucketProof(ctx)
	if err != nil {
		return Plan{}, err
	}

	revokeAt := func(cs []conn) time.Time {
		at := in.Now.UTC()
		for _, cn := range cs {
			if t := time.Unix(cn.iat, 0).UTC(); cn.iat > 0 && t.After(at) {
				at = t
			}
		}
		return at.Truncate(time.Second)
	}
	connsOf := func(cs []conn) []Conn {
		out := []Conn{}
		for _, cn := range cs {
			out = append(out, cn.Conn)
		}
		return out
	}
	finding := func(key, format string, a ...any) {
		p.Findings = append(p.Findings, Finding{Key: key, Detail: fmt.Sprintf(format, a...)})
	}

	// Half (b), rule 1: recorded users whose sender is not in the list set.
	for _, r := range recs {
		if exempt[r.PublicKey] || r.RevokedAt != "" || senders[r.Address] {
			continue
		}
		cs := byKey[r.PublicKey]
		if r.Kind == store.IssuedAdmin {
			finding(r.PublicKey, "recorded admin user %q is not in the exempt set: not revoked; exempt it by running adopt again, or revoke it by hand", r.Address)
			continue
		}
		// A record is a sender's when it is the directory record that serves the sender's address: a legacy record
		// (Address the bare name) is matched on name and owner together, because legacy names repeat across owners.
		// Doubt is a finding, never a revoke (ADR 0121).
		if by := sm.match[recKey{r.Address, r.Owner}]; len(by) > 0 {
			if len(by) > 1 {
				finding(r.PublicKey, "recorded as %q owner %q, the directory record that serves %s: not revoked; one record serving several senders is ambiguous", r.Address, r.Owner, strings.Join(by, ", "))
			}
			continue
		}
		// A sender the directory no longer serves is still named by the list set: its recorded issuance is not
		// outside the list set, and revoking it is irreversible. Removing the sender from the list set revokes it.
		if by := sm.absent[recKey{r.Address, r.Owner}]; len(by) > 0 {
			finding(r.PublicKey, "recorded as %q owner %q, the list set names %s but no directory record serves it: not revoked; remove the sender from the list set to revoke it", r.Address, r.Owner, strings.Join(by, ", "))
			continue
		}
		if why := sm.doubt(r); why != "" {
			finding(r.PublicKey, "recorded as %q owner %q, which may be a sender of the list set, but %s: not revoked", r.Address, r.Owner, why)
			continue
		}
		t, found, herr := in.Issued.IssuedHistoryByKey(ctx, r.PublicKey)
		var bad *admin.MalformedIssuedAtError
		var incomplete *admin.IncompleteHistoryError
		switch {
		case herr != nil && (errors.As(herr, &bad) || errors.As(herr, &incomplete)):
			finding(r.PublicKey, "recorded, sender %s not in the list set, but its issue history cannot be shown complete (%s): not revoked", r.Address, herr)
		case herr != nil:
			return Plan{}, herr
		case !found:
			finding(r.PublicKey, "recorded, sender %s not in the list set, but its issue history could not be read: not revoked", r.Address)
		case t.Max.After(in.Now):
			finding(r.PublicKey, "recorded, sender %s not in the list set, but a revision is dated in the future (%s): a revocation now would not cover it; not revoked", r.Address, t.Max.UTC().Format(time.RFC3339))
		case t.Truncated:
			finding(r.PublicKey, "recorded, sender %s not in the list set, but its history is at the bucket's cap, so an older future-dated revision cannot be ruled out: not revoked", r.Address)
		default:
			p.Revocations = append(p.Revocations, Revocation{Key: r.PublicKey, Address: r.Address, Reason: ReasonRecorded,
				At: revokeAt(cs).Format(time.RFC3339), Connections: connsOf(cs)})
		}
	}

	// Rule 2: connected keys that are not recorded.
	for _, k := range slices.Sorted(maps.Keys(byKey)) {
		if exempt[k] {
			continue
		}
		cs := byKey[k]
		if r, ok := recorded[k]; ok {
			if r.RevokedAt != "" {
				finding(k, "recorded as revoked at %s but still connected: the revocation did not take effect", r.RevokedAt)
			}
			continue
		}
		switch issuers := issuersOf(cs); {
		case anyIn(issuers, scoped):
			finding(k, "a list-set credential without its record")
		case !proven:
			finding(k, "connected, not in the issue record, and the record cannot prove no record was lost (%s): not revoked", why)
		case badIssuer(cs, in.Account, in.Claims) != "":
			finding(k, "connection with no reported issuer: cannot tell whether a list-set key signed it (%s): not revoked", badIssuer(cs, in.Account, in.Claims))
		case in.ScanIncomplete != "":
			// A server that did not answer could hold a connection using this key under a list-set signer.
			finding(k, "connected, untracked: not revoked: the connection scan is incomplete")
		default:
			p.Revocations = append(p.Revocations, Revocation{Key: k, Reason: ReasonUntracked,
				At: revokeAt(cs).Format(time.RFC3339), Connections: connsOf(cs)})
		}
	}
	if adminUnrecorded {
		finding(in.AdminUser, "the admin user KEY in use is not in the adoption marker's exempt set: run `mashctl policy adopt` again to record it")
	}
	if in.ScanIncomplete != "" {
		finding("", "the connection scan is incomplete: %s", in.ScanIncomplete)
	}

	// The issuer check: the users that must keep working must not be signed by a key that leaves.
	exIssuers := map[string][]string{}
	for k := range exempt {
		for _, cn := range byKey[k] {
			exIssuers[k] = append(exIssuers[k], cn.issuer)
		}
	}
	var nk *admin.IssuersNotKeptError
	if e := admin.CheckIssuersKept(in.Account, in.AdminUser, in.AdminIssuer, exIssuers, kept); e != nil && errors.As(e, &nk) {
		p.IssuerProblems = nk.Problems
	}

	slices.SortFunc(p.Revocations, func(a, b Revocation) int { return strings.Compare(a.Key, b.Key) })
	slices.SortFunc(p.Findings, func(a, b Finding) int {
		if x := strings.Compare(a.Key, b.Key); x != 0 {
			return x
		}
		return strings.Compare(a.Detail, b.Detail)
	})
	p.Actions = len(p.Revocations)
	p.Unknown = len(p.Findings)
	for _, sk := range p.SigningKeys {
		if sk.State == StateStillPresent {
			p.Unknown++
		}
	}
	switch {
	case p.Unknown > 0:
		p.Status = fmt.Sprintf("reconciled: partial (%d unknown)", p.Unknown)
	case p.Actions > 0:
		p.Status = fmt.Sprintf("reconciled: %d actions", p.Actions)
	default:
		p.Status = "reconciled: nothing to do"
	}
	if p.Source == SourceInForce {
		p.PlanDigest = p.Digest()
	}
	return p, nil
}

func issuersOf(cs []conn) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.issuer)
	}
	return out
}

// badIssuer says why a connection's issuer cannot be trusted to rule out a list-set key: it is empty, or it is neither
// the account key nor a signing key of the account JWT. "" when every connection's issuer is consistent.
func badIssuer(cs []conn, account string, claims *jwt.AccountClaims) string {
	for _, c := range cs {
		switch {
		case c.issuer == "":
			return "issuer empty"
		case c.issuer == account:
		case claims != nil && claims.SigningKeys.Contains(c.issuer):
		default:
			return "issuer " + c.issuer + " is not the account key or a signing key of the account JWT"
		}
	}
	return ""
}

func anyIn(keys []string, set map[string]bool) bool {
	for _, k := range keys {
		if k != "" && set[k] {
			return true
		}
	}
	return false
}

// recKey is how an issued record names the endpoint: its directory name and its owner.
type recKey struct{ name, owner string }

// senderMap is what the senders of the list set resolve to.
type senderMap struct {
	// match maps the (directory name, owner) of the record serving each sender to those senders.
	match map[recKey][]string
	// names maps a directory name a sender can be recorded under (its bare name, its flat key) to the senders; errs
	// holds the senders whose resolution failed, by sender.
	names map[string][]string
	errs  map[string]error
	// absent maps the (directory name, owner) a sender would be recorded under, as its bare name or its flat key, to
	// the senders for which no directory record was found.
	absent map[recKey][]string
}

// resolveSenders resolves every sender of the list set to the record that serves it. A nil resolver resolves none.
func resolveSenders(ctx context.Context, rs Resolver, users []listset.User) senderMap {
	m := senderMap{match: map[recKey][]string{}, names: map[string][]string{}, errs: map[string]error{}, absent: map[recKey][]string{}}
	if rs == nil {
		return m
	}
	for _, u := range users {
		parts := strings.Split(u.Sender, ".")
		var id sap.EndpointIdentity
		var err error
		if len(parts) == 3 {
			id, err = sap.New(parts[0], parts[1], parts[2])
		} else {
			err = fmt.Errorf("sender %q is not range.owner.name", u.Sender)
		}
		if err != nil {
			m.names[parts[len(parts)-1]] = append(m.names[parts[len(parts)-1]], u.Sender)
			m.errs[u.Sender] = err
			continue
		}
		m.names[id.Name] = append(m.names[id.Name], u.Sender)
		m.names[id.Flat()] = append(m.names[id.Flat()], u.Sender)
		target, _, found, err := rs.ServingIdentity(ctx, id)
		if err != nil {
			m.errs[u.Sender] = err
			continue
		}
		if found {
			k := recKey{target.Flat(), target.Owner}
			m.match[k] = append(m.match[k], u.Sender)
		} else {
			for _, n := range []string{id.Name, id.Flat()} {
				k := recKey{n, id.Owner}
				m.absent[k] = append(m.absent[k], u.Sender)
			}
		}
	}
	return m
}

// doubt is why a record that no sender is known to own may still be one's, or "" when it cannot be: its owner is
// missing, or the resolution of a sender it could belong to failed.
func (m senderMap) doubt(r store.Issued) string {
	senders := m.names[r.Address]
	if len(senders) == 0 {
		return ""
	}
	if r.Owner == "" {
		return "has no owner, so it cannot be matched to " + strings.Join(senders, ", ")
	}
	for _, s := range senders {
		if err := m.errs[s]; err != nil {
			return fmt.Sprintf("resolving %s failed (%v)", s, err)
		}
	}
	return ""
}
