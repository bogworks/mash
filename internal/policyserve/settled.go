package policyserve

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
)

// settledState is what the servers already hold of a change, read before the witnesses are opened. The zero value
// settles nothing: every sender keeps its witness, which is the behaviour of a first run.
//
// A scoped-template change closes every connection under the scoped key at the push (ADR 0097). When every server
// already holds the target's templates, an earlier run pushed the change, so a connection opened now was opened under
// the target and no witness opened now could be closed by the push: it would stay open for ever. A key revoked on every
// server cannot connect again, and the connection it had was closed by the revocation. ADR 0125 gives the argument.
type settledState struct {
	// Servers are the names of the servers that answered, set only when every server of the declared count did.
	Servers []string
	// Templates is true when every server's account JWT holds the target's scoped template for every owner group of
	// the target and holds none for an owner group of the previous generation the target does not have.
	Templates bool
	// Revoked are the senders every key of which, in the issue record, is revoked on every server at or after the time
	// the credential was issued.
	Revoked map[string]bool
}

// settledSenders reads what the servers' account JWTs say about the change. jwts is the account JWT each server holds
// (policyapply.Cluster.AccountJWTs), inventory the declared number of servers, signers the public key of each owner
// group's scoped key in the target, and entries the issue record's entries for a sender's address. It settles
// nothing when fewer servers answered than the declared count, when a server's JWT is empty or does not decode or is
// for another account, or when the issue record cannot be read: a silent or lagging server never relaxes a witness.
func settledSenders(jwts map[string]string, inventory int, account string, signers map[string]string,
	prev, target *listset.Compiled, ex policyapply.Expect,
	entries func(address string) ([]policyapply.IssueEntry, error)) settledState {
	var none settledState
	if prev == nil || target == nil || len(jwts) == 0 || len(jwts) < inventory {
		return none
	}
	var claims []*jwt.AccountClaims
	names := make([]string, 0, len(jwts))
	for name, tok := range jwts {
		if tok == "" {
			return none
		}
		ac, err := jwt.DecodeAccountClaims(tok)
		if err != nil || ac.Subject != account {
			return none
		}
		claims = append(claims, ac)
		names = append(names, name)
	}
	slices.Sort(names)
	st := settledState{Servers: names, Templates: true, Revoked: map[string]bool{}}
	for _, ac := range claims {
		if !holdsTemplates(ac, prev, target, signers) {
			st.Templates = false
			break
		}
	}
	// A key revoked is a sender settled: for the senders the change removes and for those whose reach it removes.
	candidates := map[string]bool{}
	for _, s := range ex.RemovedSenders {
		candidates[s] = true
	}
	for _, r := range slices.Concat(ex.Removed, ex.CredentialOnly) {
		candidates[r.Sender] = true
	}
	for s := range candidates {
		held, err := entries(s)
		if err != nil {
			continue
		}
		if allKeysRevoked(held, claims) {
			st.Revoked[s] = true
		}
	}
	return st
}

// holdsTemplates reports whether the account JWT holds exactly the target's scoped templates for the owner groups of
// either generation: each target group has its scoped signer under the key the target is signed with, with that group
// as its role and the same permissions, and no group of the previous generation that the target dropped has one.
// The issue time is not compared.
func holdsTemplates(ac *jwt.AccountClaims, prev, target *listset.Compiled, signers map[string]string) bool {
	want := map[string]bool{}
	for _, t := range target.Templates {
		want[t.Group] = true
		pub, ok := signers[t.Group]
		if !ok {
			return false
		}
		sc, ok := ac.SigningKeys.GetScope(pub)
		us, isUser := sc.(*jwt.UserScope)
		if !ok || !isUser || us.Role != t.Group || !samePermissions(us.Template.Permissions, t.Permissions) {
			return false
		}
	}
	for _, p := range prev.Templates {
		if want[p.Group] {
			continue
		}
		for _, k := range ac.SigningKeys.Keys() {
			if sc, ok := ac.SigningKeys.GetScope(k); ok {
				if us, isUser := sc.(*jwt.UserScope); isUser && us.Role == p.Group {
					return false
				}
			}
		}
	}
	return true
}

func samePermissions(a, b jwt.Permissions) bool {
	x, errA := json.Marshal(a)
	y, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(x, y)
}

// allKeysRevoked reports whether the sender has at least one revocable key in its entries and every one is revoked, on
// every account JWT, at or after the time its credential was issued (which revokes every credential made with it). An
// entry with no issue time cannot be judged, so it is not revoked.
func allKeysRevoked(held []policyapply.IssueEntry, claims []*jwt.AccountClaims) bool {
	n := 0
	for _, e := range held {
		if e.Purpose != policyapply.PurposeIssue && e.Purpose != policyapply.PurposeRotation {
			continue
		}
		n++
		if e.IssuedAt.IsZero() {
			return false
		}
		for _, ac := range claims {
			at, ok := ac.Revocations[e.PublicKey]
			if !ok || at <= 0 || at < e.IssuedAt.Unix() {
				return false
			}
		}
	}
	return n > 0
}

// groupOf is the owner group of a sender address: range.owner of range.owner.name.
func groupOf(sender string) string {
	if i := strings.LastIndex(sender, "."); i >= 0 {
		return sender[:i]
	}
	return sender
}

// groupChanges reports whether the push changes the scoped template of the owner group: it differs between prev and
// target, or the group appears or disappears.
func groupChanges(prev, target *listset.Compiled, group string) bool {
	find := func(c *listset.Compiled) (listset.Template, bool) {
		for _, t := range c.Templates {
			if t.Group == group {
				return t, true
			}
		}
		return listset.Template{}, false
	}
	p, pok := find(prev)
	t, tok := find(target)
	return pok != tok || (pok && !samePermissions(p.Permissions, t.Permissions))
}

// settledWitnesses is the Witnesses of a run whose gate also asks for senders that have no witness because the servers
// already hold the change: each is reported closed on every server that was read. The senders were settled by
// settledSenders, on every server of the declared count.
type settledWitnesses struct {
	inner   policyapply.Witnesses
	servers []string
	senders []string
}

func (w settledWitnesses) Closed(ctx context.Context) (map[policyapply.WitnessKey]bool, error) {
	out, err := w.inner.Closed(ctx)
	if err != nil {
		return nil, err
	}
	merged := make(map[policyapply.WitnessKey]bool, len(out)+len(w.servers)*len(w.senders))
	for k, v := range out {
		merged[k] = v
	}
	for _, s := range w.senders {
		for _, n := range w.servers {
			merged[policyapply.WitnessKey{Server: n, Sender: s}] = true
		}
	}
	return merged, nil
}
