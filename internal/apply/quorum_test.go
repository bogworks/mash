package apply

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
)

func TestServerSetQuorate(t *testing.T) {
	names := func(n int) []string {
		var out []string
		for i := 0; i < n; i++ {
			out = append(out, string(rune('a'+i)))
		}
		return out
	}
	for _, c := range []struct {
		name              string
		set               ServerSet
		inventory, quorum int
		want              bool
		why               string
	}{
		{"majority of three answered", ServerSet{Names: names(2), Expected: 2, Cluster: "c"}, 3, 2, true, ""},
		{"one of three answered", ServerSet{Names: names(1), Expected: 1, Cluster: "c"}, 3, 2, false, "1 of 3 servers answered and the write quorum is 2: a server may be down: bring it back, or set write_quorum"},
		{"all three answered", ServerSet{Names: names(3), Expected: 3, Cluster: "c"}, 3, 2, true, ""},
		{"nobody answered", ServerSet{}, 3, 2, false, "answered by no server"},
		{"gateways are never a quorum", ServerSet{Names: names(3), Expected: 3, Gateways: true}, 3, 2, false, "gateways"},
		{"two clusters are never a quorum", ServerSet{Names: names(3), Expected: 3, ClusterConflict: true}, 3, 2, false, "different clusters"},
		{"a cluster bigger than declared", ServerSet{Names: names(2), Expected: 5, Cluster: "c"}, 3, 2, false, "the cluster reports 5 servers but 3 are declared"},
		{"declared count unknown", ServerSet{Names: names(2), Expected: 2}, 0, 2, false, "not declared"},
		{"two of two", ServerSet{Names: names(2), Expected: 2}, 2, 2, true, ""},
		{"one of two is below the all-servers quorum", ServerSet{Names: names(1), Expected: 1}, 2, 2, false, "1 of 2 servers answered and the write quorum is 2"},
		{"zero quorum is the all-servers rule", ServerSet{Names: names(2), Expected: 2, Cluster: "c"}, 3, 0, false, "replied but"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.set.Quorate(c.inventory, c.quorum); got != c.want {
				t.Fatalf("Quorate = %v, want %v", got, c.want)
			}
			if !c.want {
				if why := c.set.QuorumWhy(c.inventory, c.quorum); !strings.Contains(why, c.why) {
					t.Fatalf("why %q lacks %q", why, c.why)
				}
			}
		})
	}
}

func TestChooseBaseQuorumTakesTheNewestAmongTheAnswers(t *testing.T) {
	ks, op, acct := baseEnv(t)
	trust, _ := LoadTrust(ks)
	cl := jwt.NewAccountClaims(acct)
	old, newest := tokenAt(t, op, cl, 1000), tokenAt(t, op, cl, 3000)
	// Two of three servers answered, one with the older copy: the newest of the answers is the base and the quorum is met.
	b, err := chooseBaseQuorum([]string{old, newest}, acct, trust, 3, 2)
	if err != nil || b.token != newest || b.short != "" || b.answered != 2 {
		t.Fatalf("%v %+v", err, b)
	}
	// One answer is below the quorum: short says how many answered of how many and the quorum.
	b, err = chooseBaseQuorum([]string{newest}, acct, trust, 3, 2)
	if err != nil || b.short != "1 of 3 servers answered and the write quorum is 2: a server may be down: bring it back, or set write_quorum" {
		t.Fatalf("%v %q", err, b.short)
	}
	// Without a quorum the same two answers are short (every server is needed).
	b, _ = chooseBaseQuorum([]string{old, newest}, acct, trust, 3, 0)
	if !strings.Contains(b.short, "only 2 of 3 servers answered") {
		t.Fatalf("%q", b.short)
	}
	// An unverifiable copy is refused even when a quorum answered.
	_, err = chooseBaseQuorum([]string{newest, "not-a-jwt"}, acct, trust, 3, 2)
	if err == nil {
		t.Fatal("an unverifiable copy was accepted")
	}
}

func TestPushAndCheckQuorumCountsAcknowledgements(t *testing.T) {
	known := []string{"s1", "s2"}
	ok := func(names ...string) []ServerAck {
		var a []ServerAck
		for _, n := range names {
			a = append(a, ServerAck{Server: n, Code: 200})
		}
		return a
	}
	for _, c := range []struct {
		name       string
		acks       []ServerAck
		pending    []string
		whyHas     string
		declared   int
		quorum     int
		knownNames []string
	}{
		{"quorum of three acknowledged", ok("s1", "s2"), []string{unnamedPending}, "", 3, 2, known},
		{"all acknowledged", ok("s1", "s2", "s3"), nil, "", 3, 2, []string{"s1", "s2", "s3"}},
		{"a known server is silent", ok("s1"), []string{"s2", unnamedPending}, "1 of 3 servers answered and the write quorum is 2", 3, 2, known},
		{"a duplicate ack counts once", ok("s1", "s1"), []string{"s2", unnamedPending}, "1 of 3 servers answered", 3, 2, known},
		{"an ack from an unknown name does not count", ok("s1", "zz"), []string{"s2", unnamedPending}, "1 of 3 servers answered", 3, 2, known},
		{"a refusal fails the push even with a quorum", append(ok("s1", "s2"), ServerAck{Server: "s3", Code: 500, Message: "no"}), []string{unnamedPending}, "refused by s3: code 500 no", 3, 2, known},
		{"two of two", ok("s1", "s2"), nil, "", 2, 2, known},
		{"one of two", ok("s1"), []string{"s2"}, "1 of 2 servers answered and the write quorum is 2", 2, 2, known},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, pending, why := pushAndCheckQuorum(context.Background(), &fakeAcks{c.acks}, "t", c.knownNames, c.declared, c.quorum)
			if (c.whyHas == "") != (why == "") || !strings.Contains(why, c.whyHas) {
				t.Fatalf("why %q, want it to have %q", why, c.whyHas)
			}
			if strings.Join(pending, "|") != strings.Join(c.pending, "|") {
				t.Fatalf("pending %v, want %v", pending, c.pending)
			}
		})
	}
}

// Three servers declared, one down, write quorum 2: a revocation is pushed, acknowledged by the two that answer, read
// back from them, and the third is reported pending. The result is not an error.
func TestRevokeUsersWithAQuorumSucceedsWithOneServerDown(t *testing.T) {
	ks, fk := setup(t)
	op, _, _ := ks.Operator()
	_, acct, _ := ks.Account()
	u := userKey(t)
	base := tokenAt(t, op, jwt.NewAccountClaims(acct), 1000)
	fk.token = base
	fk.servers = ServerSet{Names: []string{"s1", "s2"}, Expected: 2, Cluster: "c"}
	fk.acks = []ServerAck{{Server: "s1", Code: 200}, {Server: "s2", Code: 200}}
	m := &multi{fake: fk, copies: []string{base, base}, healed: map[int]bool{0: true, 1: true}}
	res, err := RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: m, Servers: 3, Quorum: 2, VerifyTries: 1, Now: func() time.Time { return revokeAt }}, []string{u})
	if err != nil {
		t.Fatal(err)
	}
	if res.Acked != 2 || res.Complete || res.Quorum != 2 || res.Declared != 3 || len(res.Pending) != 1 || res.Pending[0] != unnamedPending || fk.pushes != 1 {
		t.Fatalf("%+v pushes %d", res, fk.pushes)
	}
	if !revokedIn(t, fk.lastPush, u) {
		t.Fatal("the pushed JWT does not revoke the key")
	}

	// With every server answering nothing is pending and the result is complete.
	fk2 := &fake{token: base, servers: ServerSet{Names: []string{"s1", "s2", "s3"}, Expected: 3, Cluster: "c"},
		acks: []ServerAck{{Server: "s1", Code: 200}, {Server: "s2", Code: 200}, {Server: "s3", Code: 200}}}
	m2 := &multi{fake: fk2, copies: []string{base, base, base}, healed: map[int]bool{0: true, 1: true, 2: true}}
	res, err = RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: m2, Servers: 3, Quorum: 2, VerifyTries: 1, Now: func() time.Time { return revokeAt }}, []string{u})
	if err != nil || !res.Complete || len(res.Pending) != 0 {
		t.Fatalf("%v %+v", err, res)
	}
}

// Below the quorum nothing is pushed and the refusal says how many answered of how many and what the quorum is.
func TestRevokeUsersBelowTheQuorumChangesNothing(t *testing.T) {
	ks, fk := setup(t)
	u := userKey(t)
	fk.servers = ServerSet{Names: []string{"s1"}, Expected: 1, Cluster: "c"}
	fk.acks = []ServerAck{{Server: "s1", Code: 200}}
	_, err := RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: fk, Servers: 3, Quorum: 2, VerifyTries: 1}, []string{u})
	want := "1 of 3 servers answered and the write quorum is 2: a server may be down: bring it back, or set write_quorum (nothing was revoked)"
	if err == nil || !strings.Contains(err.Error(), want) || fk.pushes != 0 {
		t.Fatalf("%v pushes %d", err, fk.pushes)
	}

	// The ping saw two servers but only one answered the lookup: the same refusal, still before any push.
	fk.servers = ServerSet{Names: []string{"s1", "s2"}, Expected: 2, Cluster: "c"}
	fk.all = []string{fk.token}
	_, err = RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: fk, Servers: 3, Quorum: 2, VerifyTries: 1}, []string{u})
	if err == nil || !strings.Contains(err.Error(), "1 of 3 servers answered and the write quorum is 2") || fk.pushes != 0 {
		t.Fatalf("%v pushes %d", err, fk.pushes)
	}

	// The push reaches one server only: not confirmed, which a repeat of the command settles.
	fk.all = nil
	fk.servers = ServerSet{Names: []string{"s1", "s2"}, Expected: 2, Cluster: "c"}
	fk.acks = []ServerAck{{Server: "s1", Code: 200}}
	_, err = RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: fk, Servers: 3, Quorum: 2, VerifyTries: 1}, []string{u})
	if err == nil || !strings.Contains(err.Error(), "the revocation was pushed but not confirmed") || !strings.Contains(err.Error(), "1 of 3 servers answered and the write quorum is 2") {
		t.Fatalf("%v", err)
	}
}

// A quorum read is built from the newest copy among the answers: a stale answering server does not undo a revocation
// the other holds.
func TestRevokeUsersWithAQuorumBuildsFromTheNewestAnswer(t *testing.T) {
	ks, fk := setup(t)
	op, _, _ := ks.Operator()
	_, acct, _ := ks.Account()
	u1, u2 := userKey(t), userKey(t)
	newCl := jwt.NewAccountClaims(acct)
	newCl.Revocations = jwt.RevocationList{}
	newCl.Revocations.Revoke(u1, revokeAt)
	newest := tokenAt(t, op, newCl, 2000)
	stale := tokenAt(t, op, jwt.NewAccountClaims(acct), 1000)
	fk.token = newest
	fk.servers = ServerSet{Names: []string{"s1", "s2"}, Expected: 2, Cluster: "c"}
	fk.acks = []ServerAck{{Server: "s1", Code: 200}, {Server: "s2", Code: 200}}
	m := &multi{fake: fk, copies: []string{stale, newest}, healed: map[int]bool{0: true, 1: true}}
	if _, err := RevokeUsers(context.Background(), RevokeParams{Keys: ks, Resolver: m, Servers: 3, Quorum: 2, VerifyTries: 1, Now: func() time.Time { return revokeAt.Add(time.Hour) }}, []string{u2}); err != nil {
		t.Fatal(err)
	}
	if !revokedIn(t, fk.lastPush, u1) || !revokedIn(t, fk.lastPush, u2) {
		t.Fatal("the push lost the earlier revocation or the new one")
	}
}

func TestKeyRevokedWithAQuorum(t *testing.T) {
	ks, fk := setup(t)
	op, _, _ := ks.Operator()
	_, acct, _ := ks.Account()
	u := userKey(t)
	cl := jwt.NewAccountClaims(acct)
	cl.Revocations = jwt.RevocationList{}
	cl.Revocations.Revoke(u, revokeAt)
	held := tokenAt(t, op, cl, 2000)
	none := tokenAt(t, op, jwt.NewAccountClaims(acct), 1000)
	fk.servers = ServerSet{Names: []string{"s1", "s2"}, Expected: 2, Cluster: "c"}
	m := &multi{fake: fk, copies: []string{none, held}}
	got, err := KeyRevoked(context.Background(), RevokeParams{Keys: ks, Resolver: m, Servers: 3, Quorum: 2}, u)
	if err != nil || !got {
		t.Fatalf("%v %v", err, got)
	}
	// Without a quorum the same read is refused: the set is not complete.
	if _, err := KeyRevoked(context.Background(), RevokeParams{Keys: ks, Resolver: m, Servers: 3}, u); err == nil {
		t.Fatal("two of three servers were accepted without a quorum")
	}
	// One answer is below the quorum.
	m1 := &multi{fake: &fake{token: none, servers: ServerSet{Names: []string{"s1"}, Expected: 1, Cluster: "c"}}, copies: []string{none}}
	if _, err := KeyRevoked(context.Background(), RevokeParams{Keys: ks, Resolver: m1, Servers: 3, Quorum: 2}, u); err == nil || !strings.Contains(err.Error(), "1 of 3 servers answered and the write quorum is 2") {
		t.Fatalf("%v", err)
	}
}

func TestRevocationsPending(t *testing.T) {
	ks, fk := setup(t)
	op, _, _ := ks.Operator()
	_, acct, _ := ks.Account()
	held, never, lagging := userKey(t), userKey(t), userKey(t)
	withAll := jwt.NewAccountClaims(acct)
	withAll.Revocations = jwt.RevocationList{}
	withAll.Revocations.Revoke(held, revokeAt)
	withAll.Revocations.Revoke(lagging, revokeAt)
	full := tokenAt(t, op, withAll, 3000)
	partial := jwt.NewAccountClaims(acct)
	partial.Revocations = jwt.RevocationList{}
	partial.Revocations.Revoke(held, revokeAt)
	behind := tokenAt(t, op, partial, 2000)
	fk.servers = ServerSet{Names: []string{"s1", "s2", "s3"}, Expected: 3, Cluster: "c"}
	keys := []string{held, never, lagging}

	// One server lags: the key it lacks is pending, the one every server holds and the one nobody holds are not.
	rep, err := RevocationsPending(context.Background(), &multi{fake: fk, copies: []string{full, full, behind}}, ks, 3, keys)
	if err != nil || rep.Converged() || len(rep.Pending) != 1 || rep.Pending[0].Key != lagging || rep.Pending[0].Lacking != 1 || rep.Answered != 3 || rep.Silent != 0 || rep.Keys != 2 {
		t.Fatalf("%v %+v", err, rep)
	}
	// All hold it: converged, nothing pending.
	rep, err = RevocationsPending(context.Background(), &multi{fake: fk, copies: []string{full, full, full}}, ks, 3, keys)
	if err != nil || !rep.Converged() || len(rep.Pending) != 0 {
		t.Fatalf("%v %+v", err, rep)
	}
	// One server silent: nothing lacks it among the answers, but the silent one cannot be shown to hold it.
	rep, err = RevocationsPending(context.Background(), &multi{fake: fk, copies: []string{full, full}}, ks, 3, keys)
	if err != nil || rep.Converged() || rep.Silent != 1 || rep.Declared != 3 || len(rep.Pending) != 0 {
		t.Fatalf("%v %+v", err, rep)
	}
	// A server holding no JWT at all lacks every revocation.
	rep, err = RevocationsPending(context.Background(), &multi{fake: fk, copies: []string{full, full, ""}}, ks, 3, keys)
	if err != nil || len(rep.Pending) != 2 {
		t.Fatalf("%v %+v", err, rep)
	}
	// No revoked key at all: converged even with a silent server.
	rep, err = RevocationsPending(context.Background(), &multi{fake: fk, copies: []string{full}}, ks, 3, []string{never})
	if err != nil || !rep.Converged() {
		t.Fatalf("%v %+v", err, rep)
	}
}
