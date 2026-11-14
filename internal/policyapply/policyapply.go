// Package policyapply is the core of the applied-policy gate (contract mash-messaging-policy REQ-011, draft-3).
//
// A list set generation counts as applied only when all of these hold:
//
//  1. every server has taken the push: each holds the target generation's account JWT;
//  2. every revoked key is refused on every server: it is in each server's revocation list;
//  3. a live send to a removed receiver is refused on every server, both with each old credential (a probe) and from
//     a connection that was open before the change (a witness).
//
// A template push closes every connection under the changed scoped key (AT-011, ADR 0097), so a witness counts as
// refused by being closed; a sender that reconnects is bound by the new template. Until all hold the gate reports
// "not applied", naming the failing servers and senders. When servers hold different generations it reports partial
// application, each server's generation, and no account-wide policy in force. Converge brings every server to one
// generation, by finishing the push or pushing the previous generation again, before any policy is reported in force.
//
// Probe credentials. mash never holds a sender's seed, so it cannot replay the sender's own credential. A probe is
// minted by mash under the same owner-group scoped key with the sender's OLD name and OLD tags; under the template
// that is exactly the old credential's permissions. ProbeCred carries those three facts and the transport mints the
// user JWT.
//
// Tag-only narrowing. When a change only narrows a sender's tags, the target template expanded with the old tags still
// grants the reach, so a probe with the old credential cannot be refused: only revoking the old key refuses it (REQ-014's
// narrowing rotation, a later change, makes sure the old key is revoked). For those reaches (Expect.CredentialOnly) the
// gate probes the sender's TARGET credential, which must be refused, and requires each witness closed.
//
// Scope. The gate is pure logic over the interfaces Cluster, Prober, Witnesses and Ledger, tested with in-memory fakes.
// The live NATS transport implements them: NATSCluster (system-account requests; the server's own account JWT is read
// with $SYS.REQ.SERVER.PING.ACCOUNTZ, so a server that has not loaded the account yet reports none), LiveProber and
// LiveWitnesses (each connects to one named server only, never reconnects), and ProbeMinter, which writes every probe
// to an IssueRecord before handing it out. The caller revokes the probes when the gate ends, pass or fail, with
// RevokeMinted. AT-010 (at010_live_test.go) runs the gate on a live two-server cluster, including a server held back
// from the push.
//
// Ordering (REQ-015, AT-014; order.go). A new receiver is reachable only after the senders that must refuse it. NewOrder
// derives, from the previous and target generations and Expect, each new address with the senders that must refuse it
// and each existing address newly denied to a sender. RunOrdered runs Run (the gate is inside it) and only then, for
// each new address whose senders have all passed (SenderPassed), calls the Provisioner to create its inbox stream and
// consumer and to issue its credential; any other address is held. A change that newly denies an existing address to
// a sender is reported applied only after that sender's gate passes (Applied). Cost: each push that finishes a
// sender's gate disconnects every sender under that owner group's key (ADR 0097). AT-014 (at014_live_test.go) runs
// this on the live cluster.
//
// Ledger caveat. MemLedger is process-local. The Ledger's Lock is mandatory and a persistent implementation MUST make it
// a cross-process lock (a lease in the shared store); MemLedger's lock is exclusive within one process only, so until
// the persistent compare-and-set store (the status.<account> key in KV) comes, two processes can still push over each
// other.
package policyapply

import (
	"context"

	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/listset"
)

// Cluster is the system-account view of the deployment.
type Cluster interface {
	// Servers pings the system account and returns the servers that replied.
	Servers(ctx context.Context) (apply.ServerSet, error)
	// AccountJWTs returns, per server name, the account JWT that server holds for account ("" if none).
	AccountJWTs(ctx context.Context, account string) (map[string]string, error)
	// Push sends the signed account JWT to every server and returns the answer of each server that replied. Pushing
	// a JWT a server already holds is harmless.
	Push(ctx context.Context, accountJWT string, expect []string) ([]apply.ServerAck, error)
}

// Op is the kind of probe: a publish for mail, prompt and status, a subscribe for heartbeat.
type Op string

// The two probe operations.
const (
	OpPub Op = "pub"
	OpSub Op = "sub"
)

// OpOf returns the operation a sender uses for verb v.
func OpOf(v listset.Verb) Op {
	if v == listset.Heartbeat {
		return OpSub
	}
	return OpPub
}

// ProbeCred is the old credential of a sender: its address, name claim and tags. The transport mints a user JWT with
// them under the sender's owner-group scoped key.
type ProbeCred struct {
	Sender string   `json:"sender"`
	Name   string   `json:"name"`
	Tags   []string `json:"tags"`
}

// Prober sends one probe on one named server with a probe credential and reports whether the server refused it.
type Prober interface {
	Refused(ctx context.Context, server string, probe ProbeCred, op Op, subject string) (bool, error)
}

// WitnessKey names the witness connection of one sender on one server.
type WitnessKey struct {
	Server string
	Sender string
}

// Witnesses are connections opened per sender per server before the push.
type Witnesses interface {
	// Closed reports, per server and sender, whether the witness opened before the push has been closed by the server.
	Closed(ctx context.Context) (map[WitnessKey]bool, error)
}
