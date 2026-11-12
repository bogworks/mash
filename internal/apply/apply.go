// Package apply is mashctl's view of the account resolver through the system account (ADR 0052): it looks the account
// JWT up on the servers, pushes a revision of it signed with the operator key, and reads it back to verify. What it
// pushes is user revocations (RevokeUsers). It no longer publishes per-zone scoped signing keys: zones are removed
// (ADR 0091), and what remains of them here is the detector of an account JWT that still carries scoped signers
// (AppliedZonePolicy), which the zone-removal guards read. Seed text is never put in output or errors.
package apply

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctlkeys"
)

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
	// Names are the distinct servers that replied, as the servers sent them (raw: they are the identity acknowledgements
	// match on; print one through config.Clean).
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
		return fmt.Sprintf("%d server(s) replied and they name different clusters (first seen: %s): membership cannot be verified from here", len(s.Names), config.Clean(s.Cluster))
	case s.Gateways:
		return fmt.Sprintf("%d server(s) replied, and a server reports gateways: a super-cluster's membership cannot be verified from here", len(s.Names))
	case inventory <= 0 && s.Cluster != "" && len(s.Names) >= s.Expected:
		return fmt.Sprintf("this server is part of cluster %s and the number of servers cannot be verified from here; pass --servers N", config.Clean(s.Cluster))
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

const (
	defaultVerifyTries = 5
	defaultVerifyWait  = 200 * time.Millisecond
	defaultLockWait    = 5 * time.Second
)

// unknownKey is a signing key of the fetched account JWT that is not one of mashctl's own keys. role is the scoped
// signer's role, "" for a plain signing key.
type unknownKey struct{ key, role string }

func (u unknownKey) describe() string {
	if u.role == "" {
		return "plain signing key " + config.Clean(u.key)
	}
	return fmt.Sprintf("scoped signing key %s (role %s)", config.Clean(u.key), config.Clean(u.role))
}

// knownSigningKeys is the set of signing keys mashctl may find in the account JWT: the account identity key. There are
// no per-zone signing keys any more (ADR 0091), so a scoped signer in the account JWT is never one of mashctl's own.
func knownSigningKeys(ks *ctlkeys.Authority, acctPub string) (map[string]bool, error) {
	return map[string]bool{acctPub: true}, nil
}

// unknownKeyNote is the one explanation printed with every unknown signing key.
const unknownKeyNote = "mashctl signs the account JWT with the operator key, so it will not carry signing keys it did not create. A scoped signer is a zone key of a zone policy from an older release: ADR 0091 removed zones, so stay on the last zoned release until the label policy model ships. Any other key is removed with the tool that added it."

// pushAndCheck pushes token to the resolver and checks the answers against known (the servers the ping found): every
// known server must have acknowledged with code 200 and at least one must have answered. why is "" when all did.
func pushAndCheck(ctx context.Context, r Resolver, token string, known []string) (acks []ServerAck, why string) {
	acks, perr := r.Push(ctx, token, known)
	acked := map[string]bool{}
	var bad []string
	for _, a := range acks {
		acked[a.Server] = true
		if !a.OK() {
			bad = append(bad, fmt.Sprintf("%s: code %d %s", serverName(a), a.Code, config.Clean(a.Message)))
		}
	}
	var silent []string
	for _, k := range known {
		if !acked[k] {
			silent = append(silent, config.Clean(k))
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

func serverName(a ServerAck) string {
	if a.Server == "" {
		return "a server"
	}
	return config.Clean(a.Server) // a.Server is raw, the identity; clean it only to print
}

func sortedKeys(sk jwt.SigningKeys) []string {
	k := sk.Keys()
	sort.Strings(k)
	return k
}
