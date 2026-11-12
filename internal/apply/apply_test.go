package apply

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/ctlkeys"
)

// fake is an in-memory resolver.
type fake struct {
	mu         sync.Mutex
	noServers  bool      // the ping finds nobody
	servers    ServerSet // the system account ping's answer
	token      string
	acks       []ServerAck
	pushErr    error
	applyPush  bool // a push replaces token
	pushes     int
	lastPush   string // the token of the latest push
	lookups    int
	serversErr error    // Servers returns it with servers
	all        []string // LookupAll's answer; nil: every expected server holds token
}

func (f *fake) Lookup(context.Context, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.token, nil
}
func (f *fake) LookupAll(_ context.Context, _ string, expect int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.all != nil {
		return f.all, nil
	}
	var out []string
	for i := 0; i < max(expect, 1); i++ {
		out = append(out, f.token)
	}
	return out, nil
}
func (f *fake) Servers(context.Context) (ServerSet, error) {
	if f.serversErr != nil {
		return f.servers, f.serversErr
	}
	if f.servers.Names == nil && !f.servers.Gateways && f.servers.Expected == 0 && !f.noServers {
		return ServerSet{Names: []string{"s1"}, Expected: 1}, nil // the default: one healthy server
	}
	return f.servers, nil
}
func (f *fake) Push(_ context.Context, tok string, _ []string) ([]ServerAck, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushes++
	f.lastPush = tok
	if f.applyPush {
		f.token = tok
	}
	return f.acks, f.pushErr
}

func setup(t *testing.T) (*ctlkeys.Authority, *fake) {
	t.Helper()
	ks, err := ctlkeys.Open(filepath.Join(t.TempDir(), "keys"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []func() (string, bool, error){ks.InitOperator, ks.InitAccount} {
		if _, _, err := f(); err != nil {
			t.Fatal(err)
		}
	}
	op, _, _ := ks.Operator()
	_, apub, _ := ks.Account()
	tok, err := jwt.NewAccountClaims(apub).Encode(op)
	if err != nil {
		t.Fatal(err)
	}
	return ks, &fake{token: tok}
}

// The exported names other packages import keep their shape: the system account transport, the server set and its
// verdicts, and the acknowledgement.
var (
	_ Resolver = NATS{}
	_          = ServerSet{}.Complete
	_          = ServerSet{}.Why
	_          = ServerAck{}.OK
	_          = NATS{Conn: nil, Inventory: 0}.Servers
	_          = NATS{}.Push
)

func TestServerSetNeedsInventoryOrStandalone(t *testing.T) {
	cases := []struct {
		name      string
		servers   ServerSet
		inventory int
		complete  bool
		why       string
	}{
		{"standalone", ServerSet{Names: []string{"s1"}, Expected: 1}, 0, true, ""},
		{"three of three", ServerSet{Names: []string{"s1", "s2", "s3"}, Expected: 3}, 3, true, ""},
		{"two of three replied", ServerSet{Names: []string{"s1", "s2"}, Expected: 3}, 0, false, "2 server(s) replied but the cluster reports 3"},
		{"gateways", ServerSet{Names: []string{"s1"}, Expected: 1, Gateways: true}, 0, false, "gateways"},
		{"ping found nobody", ServerSet{}, 0, false, "answered by no server"},
		{"cluster named, no inventory", ServerSet{Names: []string{"s1"}, Expected: 1, Cluster: "C"}, 0, false, "this server is part of cluster C and the number of servers cannot be verified from here; pass --servers N"},
		{"one with a cluster name", ServerSet{Names: []string{"s1"}, Expected: 1, Cluster: "C"}, 1, true, ""},
		{"two of three by inventory", ServerSet{Names: []string{"s1", "s2"}, Expected: 2, Cluster: "C"}, 3, false, "2 server(s) replied but --servers says 3"},
		{"routes say more than the inventory", ServerSet{Names: []string{"s1"}, Expected: 3, Cluster: "C"}, 1, false, "the cluster reports 3"},
		{"inventory with a standalone reply", ServerSet{Names: []string{"s1"}, Expected: 1}, 2, false, "--servers says 2"},
		{"clusters conflict", ServerSet{Names: []string{"s1", "s2"}, Expected: 2, Cluster: "C", ClusterConflict: true}, 2, false, "different clusters"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.servers.Complete(tc.inventory); got != tc.complete {
				t.Fatalf("Complete(%d) = %v", tc.inventory, got)
			}
			if !tc.complete && !strings.Contains(tc.servers.Why(tc.inventory), tc.why) {
				t.Fatalf("Why = %q, want %q", tc.servers.Why(tc.inventory), tc.why)
			}
		})
	}
}

func TestServerAckOK(t *testing.T) {
	if !(ServerAck{Code: 200}).OK() || (ServerAck{Code: 503}).OK() {
		t.Fatal("OK is not code 200")
	}
}

// pushAndCheck is the acknowledgement rule of every push: each known server must acknowledge with 200.
func TestPushAndCheck(t *testing.T) {
	ok := func(names ...string) []ServerAck {
		var a []ServerAck
		for _, n := range names {
			a = append(a, ServerAck{Server: n, Code: 200})
		}
		return a
	}
	cases := []struct {
		name  string
		known []string
		acks  []ServerAck
		want  string // "" means acknowledged by all
	}{
		{"all ack", []string{"s1", "s2", "s3"}, ok("s1", "s2", "s3"), ""},
		{"one silent", []string{"s1", "s2"}, ok("s1"), "no acknowledgement from s2"},
		{"non-200", []string{"s1", "s2"}, []ServerAck{{Server: "s1", Code: 200}, {Server: "s2", Code: 503, Message: "busy"}}, "refused by s2: code 503 busy"},
		{"nobody", []string{"s1"}, nil, "no server answered"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, fk := setup(t)
			fk.acks = tc.acks
			_, why := pushAndCheck(context.Background(), fk, "tok", tc.known)
			if (tc.want == "") != (why == "") || !strings.Contains(why, tc.want) {
				t.Fatalf("why = %q, want %q", why, tc.want)
			}
		})
	}
}

// An account JWT with scoped signing keys is a zone policy from an older release: AccountFindings reports it, and
// AppliedZonePolicy (the Z1 guard's detector) says so.
func TestAccountFindingsAndTheZonePolicyDetector(t *testing.T) {
	ks, fk := setup(t)
	op, _, _ := ks.Operator()
	fs, err := AccountFindings(context.Background(), fk, ks, 0)
	if err != nil || len(fs) != 0 {
		t.Fatalf("a plain account: %+v %v", fs, err)
	}
	if applied, err := AppliedZonePolicy(context.Background(), fk, ks, 0); err != nil || applied {
		t.Fatalf("a plain account reads as a zone policy: %v %v", applied, err)
	}
	resolverToken(t, fk, ks, op, func(cl *jwt.AccountClaims) {
		sk, _ := nkeys.CreateAccount()
		sc := jwt.NewUserScope()
		sc.Key, sc.Role = pubOf(t, sk), "red"
		cl.SigningKeys.AddScopedSigner(sc)
	})
	fs, err = AccountFindings(context.Background(), fk, ks, 0)
	if err != nil || len(fs) != 1 || fs[0].Kind != KindZonePolicyApplied || !strings.Contains(fs[0].Detail, "ADR 0091") {
		t.Fatalf("a scoped signer: %+v %v", fs, err)
	}
	if applied, err := AppliedZonePolicy(context.Background(), fk, ks, 0); err != nil || !applied {
		t.Fatalf("a scoped signer is not a zone policy: %v %v", applied, err)
	}
	if err := RefuseZonePolicy(context.Background(), fk, ks, 0, errZoneApplied); err != errZoneApplied {
		t.Fatalf("RefuseZonePolicy: %v", err)
	}
}

var errZoneApplied = errors.New("zone policy applied")

// A plain signing key is not a zone policy but is still not mashctl's own.
func TestAccountFindingsReportsAPlainSigningKey(t *testing.T) {
	ks, fk := setup(t)
	op, _, _ := ks.Operator()
	pk, _ := nkeys.CreateAccount()
	resolverToken(t, fk, ks, op, func(cl *jwt.AccountClaims) { cl.SigningKeys.Add(pubOf(t, pk)) })
	fs, err := AccountFindings(context.Background(), fk, ks, 0)
	if err != nil || len(fs) != 1 || fs[0].Kind != KindUnknownKey || !strings.Contains(fs[0].Detail, "will not carry signing keys it did not create") {
		t.Fatalf("%+v %v", fs, err)
	}
	if applied, _ := AppliedZonePolicy(context.Background(), fk, ks, 0); applied {
		t.Fatal("a plain signing key reads as a zone policy")
	}
}

func TestAccountFindingsReportsServersThatDisagree(t *testing.T) {
	ks, fk := setup(t)
	op, _, _ := ks.Operator()
	_, acct, _ := ks.Account()
	a := tokenAt(t, op, jwt.NewAccountClaims(acct), 1000)
	b := tokenAt(t, op, jwt.NewAccountClaims(acct), 2000)
	fk.servers = ServerSet{Names: []string{"s1", "s2"}, Expected: 2}
	m := &multi{fake: fk, copies: []string{a, b}}
	fs, err := AccountFindings(context.Background(), m, ks, 2)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range fs {
		found = found || f.Kind == KindServersDiffer
	}
	if !found {
		t.Fatalf("no servers-differ finding: %+v", fs)
	}
}
