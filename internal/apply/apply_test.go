package apply

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/policy"
)

// fake is an in-memory resolver.
type fake struct {
	mu        sync.Mutex
	noServers bool      // the ping finds nobody
	servers   ServerSet // the system account ping's answer
	token     string
	acks      []ServerAck
	pushErr   error
	applyPush bool // a push replaces token
	pushes    int
	lastPush  string // the token of the latest push
	lookups   int
	all       []string // LookupAll's answer; nil: every expected server holds token
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

func setup(t *testing.T) (*ctlkeys.Authority, *fake, *policy.Compiled) {
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
	f, err := policy.Parse([]byte("default: deny\nendpoints:\n  x: {labels: {zone: a}}\n  y: {labels: {zone: b}}\nrules:\n  - {name: r, action: allow, from: {zone: a}, to: {zone: b}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := policy.Compile(f, "alice")
	if err != nil {
		t.Fatal(err)
	}
	return ks, &fake{token: tok}, c
}

func TestBudgetRefusalChangesNothing(t *testing.T) {
	ks, fk, c := setup(t)
	var out bytes.Buffer
	code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk,
		Budget: func(*policy.Compiled) (policy.BudgetReport, error) {
			return policy.BudgetReport{}, errors.New("policy: the account JWT would be too big")
		}}, &out)
	if code != 1 || !strings.Contains(out.String(), "refusing") || !strings.Contains(out.String(), "too big") {
		t.Fatalf("%d %s", code, out.String())
	}
	if fk.pushes != 0 {
		t.Fatal("pushed")
	}
	if z, _ := ks.Zones(); len(z) != 0 {
		t.Fatalf("created keys %v", z)
	}
}

func TestPartialWhenNotAcknowledgedOrNotVerified(t *testing.T) {
	cases := []struct {
		name string
		fk   func(*fake)
		want string
	}{
		{"no ack", func(f *fake) { f.pushErr = errors.New("no server answered the claims update in time") }, "not acknowledged"},
		{"a server refuses", func(f *fake) {
			f.acks = []ServerAck{{Server: "s1", Code: 200}, {Server: "s2", Code: 500, Message: "boom"}}
		}, "refused by s2: code 500 boom"},
		{"acked but the resolver shows the old claims", func(f *fake) { f.acks = []ServerAck{{Server: "s1", Code: 200}} }, "missing signing keys"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ks, fk, c := setup(t)
			tc.fk(fk)
			var out bytes.Buffer
			code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk, VerifyTries: 2, VerifyWait: 1}, &out)
			s := out.String()
			if code != 1 || !strings.Contains(s, "PARTIAL") || !strings.Contains(s, tc.want) || !strings.Contains(s, "mashctl policy apply") || !strings.Contains(s, "zone keys created locally: a, b") {
				t.Fatalf("%d\n%s", code, s)
			}
		})
	}
}

func TestAckedAndVerifiedThenNoOp(t *testing.T) {
	ks, fk, c := setup(t)
	fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
	var out bytes.Buffer
	if code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk}, &out); code != 0 || fk.pushes != 1 {
		t.Fatalf("%d %d\n%s", code, fk.pushes, out.String())
	}
	out.Reset()
	if code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk}, &out); code != 0 || fk.pushes != 2 || !strings.Contains(out.String(), "account claims already up to date") {
		t.Fatalf("%d %d\n%s", code, fk.pushes, out.String())
	}
	if fk.lastPush != fk.token {
		t.Fatal("the no-change run re-signed instead of sending the JWT the resolver has")
	}
}

// With no change the unchanged JWT is still sent to every server; "already up to date" needs a complete set and an
// acknowledgement from each, and anything else is PARTIAL or UNCONFIRMED.
func TestNoChangeStillConfirmsEveryServer(t *testing.T) {
	ok := func(names ...string) []ServerAck {
		var a []ServerAck
		for _, n := range names {
			a = append(a, ServerAck{Server: n, Code: 200})
		}
		return a
	}
	cases := []struct {
		name    string
		servers ServerSet
		acks    []ServerAck
		code    int
		want    string
		not     string
	}{
		{"all match", ServerSet{Names: []string{"s1", "s2"}, Expected: 2}, ok("s1", "s2"), 0, "already up to date", "PARTIAL"},
		{"one silent (stale)", ServerSet{Names: []string{"s1", "s2"}, Expected: 2}, ok("s1"), 1, "PARTIAL: the re-send of the unchanged claims was not acknowledged: no acknowledgement from s2", "already up to date"},
		{"one refuses", ServerSet{Names: []string{"s1", "s2"}, Expected: 2}, []ServerAck{{Server: "s1", Code: 200}, {Server: "s2", Code: 500, Message: "boom"}}, 1, "refused by s2: code 500 boom", "already up to date"},
		{"incomplete set", ServerSet{Names: []string{"s1"}, Expected: 2}, ok("s1"), 1, "UNCONFIRMED", "already up to date"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ks, fk, c := setup(t)
			// First apply brings every server up to date; then the case's servers answer the second.
			fk.servers, fk.acks, fk.applyPush = ServerSet{Names: []string{"s1", "s2"}, Expected: 2}, ok("s1", "s2"), true
			var out bytes.Buffer
			if code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk}, &out); code != 0 {
				t.Fatalf("setup apply: %d\n%s", code, out.String())
			}
			fk.pushes = 0
			fk.servers, fk.acks = tc.servers, tc.acks
			out.Reset()
			code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk, VerifyTries: 1, VerifyWait: 1}, &out)
			if code != tc.code || !strings.Contains(out.String(), tc.want) || strings.Contains(out.String(), tc.not) {
				t.Fatalf("%d\n%s", code, out.String())
			}
			if fk.pushes != 1 || fk.lastPush != fk.token {
				t.Fatalf("pushes %d, re-signed %v", fk.pushes, fk.lastPush != fk.token)
			}
		})
	}
}

func TestDryRunCreatesNoKeysAndPreservesOtherSigners(t *testing.T) {
	ks, fk, c := setup(t)
	// the account already has a plain signing key and an unrelated scoped one
	op, _, _ := ks.Operator()
	_, apub, _ := ks.Account()
	cl, _ := jwt.DecodeAccountClaims(fk.token)
	cl.SigningKeys.Add(apub)                 // a plain signing key that is mashctl's own (the account identity key)
	other, err := ks.ZoneKey("legacy", true) // a zone key of the key store whose zone the file lacks
	if err != nil {
		t.Fatal(err)
	}
	op2, _ := other.PublicKey()
	sc := jwt.NewUserScope()
	sc.Key, sc.Role = op2, "legacy"
	cl.SigningKeys.AddScopedSigner(sc)
	tok, err := cl.Encode(op)
	if err != nil {
		t.Fatal(err)
	}
	fk.token = tok
	var out bytes.Buffer
	if code := Run(context.Background(), Params{Mode: DryRun, Compiled: c, Keys: ks, Resolver: fk}, &out); code != 1 || !strings.Contains(out.String(), "REFUSED unless --remove-zone legacy or --keep-zone legacy") {
		t.Fatal(out.String())
	}
	out.Reset()
	if code := Run(context.Background(), Params{Mode: DryRun, Compiled: c, Keys: ks, Resolver: fk, KeepZones: []string{"legacy"}}, &out); code != 0 {
		t.Fatal(out.String())
	}
	s := out.String()
	if !strings.Contains(s, "zone legacy is outside the policy and its key stays active") || !strings.Contains(s, "would create its signing key") {
		t.Fatalf("%s", s)
	}
	if z, _ := ks.Zones(); len(z) != 1 || fk.pushes != 0 { // only the legacy key the test made
		t.Fatal("dry run changed something")
	}
}

func compile(t *testing.T, y string) *policy.Compiled {
	t.Helper()
	f, err := policy.Parse([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	c, err := policy.Compile(f, "alice")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestServerAcknowledgements(t *testing.T) {
	ok := func(names ...string) []ServerAck {
		var a []ServerAck
		for _, n := range names {
			a = append(a, ServerAck{Server: n, Code: 200})
		}
		return a
	}
	cases := []struct {
		name    string
		servers ServerSet
		acks    []ServerAck
		code    int
		want    []string
	}{
		{"complete three all ack", ServerSet{Names: []string{"s1", "s2", "s3"}, Expected: 3}, ok("s1", "s2", "s3"), 0, []string{"pushed: 3 server(s)"}},
		{"two of three replied", ServerSet{Names: []string{"s1", "s2"}, Expected: 3}, ok("s1", "s2"), 1, []string{"UNCONFIRMED", "2 server(s) replied but the cluster reports 3", "may still hold the old claims", "2 server(s) that replied acknowledged"}},
		{"gateways", ServerSet{Names: []string{"s1"}, Expected: 1, Gateways: true}, ok("s1"), 1, []string{"UNCONFIRMED", "gateways"}},
		{"single server", ServerSet{Names: []string{"s1"}, Expected: 1}, ok("s1"), 0, []string{"pushed: 1 server(s)"}},
		{"one silent", ServerSet{Names: []string{"s1", "s2"}, Expected: 2}, ok("s1"), 1, []string{"PARTIAL", "no acknowledgement from s2"}},
		{"non-200", ServerSet{Names: []string{"s1", "s2"}, Expected: 2}, []ServerAck{{Server: "s1", Code: 200}, {Server: "s2", Code: 503, Message: "busy"}}, 1, []string{"PARTIAL", "refused by s2: code 503 busy"}},
		{"ping found nobody", ServerSet{}, ok("s1"), 1, []string{"UNCONFIRMED", "answered by no server"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ks, fk, c := setup(t)
			fk.servers, fk.acks, fk.applyPush = tc.servers, tc.acks, true
			fk.noServers = len(tc.servers.Names) == 0 && tc.servers.Expected == 0 && !tc.servers.Gateways
			var out bytes.Buffer
			code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk, VerifyTries: 1, VerifyWait: 1}, &out)
			if code != tc.code {
				t.Fatalf("%d\n%s", code, out.String())
			}
			for _, w := range tc.want {
				if !strings.Contains(out.String(), w) {
					t.Fatalf("missing %q:\n%s", w, out.String())
				}
			}
		})
	}
}

// lostKey applies once, then replaces the zone key file of zone a, as if the file were lost and apply ran again.
func lostKey(t *testing.T) (*ctlkeys.Authority, *fake, *policy.Compiled, string, string) {
	t.Helper()
	ks, fk, c := setup(t)
	fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
	var out bytes.Buffer
	if code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk}, &out); code != 0 {
		t.Fatal(out.String())
	}
	oldPub, err := ks.ZonePublic("a")
	if err != nil {
		t.Fatal(err)
	}
	zdir := filepath.Join(ks.Dir(), "zones")
	entries, _ := os.ReadDir(zdir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "a.") {
			if err := os.Remove(filepath.Join(zdir, e.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := ks.ZonePublic("a"); err == nil {
		t.Fatal("zone key a still present")
	}
	return ks, fk, c, oldPub, fk.token
}

func TestLostZoneKeyIsRefusedUnlessReplaced(t *testing.T) {
	for _, mode := range []Mode{Apply, DryRun, Diff} {
		ks, fk, c, oldPub, before := lostKey(t)
		pushes := fk.pushes
		var out bytes.Buffer
		code := Run(context.Background(), Params{Mode: mode, Compiled: c, Keys: ks, Resolver: fk}, &out)
		want := 1
		if mode == Diff {
			want = 0
		}
		s := out.String()
		if code != want || !strings.Contains(s, "REFUSED") || !strings.Contains(s, "zone a") || !strings.Contains(s, oldPub) ||
			!strings.Contains(s, "--replace-zone-key") || !strings.Contains(s, "restore the original zone key file") || !strings.Contains(s, "stop working") {
			t.Fatalf("%s: %d\n%s", mode, code, s)
		}
		if fk.pushes != pushes || fk.token != before {
			t.Fatalf("%s pushed", mode)
		}
		if _, err := ks.ZonePublic("a"); err == nil {
			t.Fatalf("%s created a zone key before refusing", mode)
		}
	}
}

func TestReplaceZoneKeyReplacesTheOldSigner(t *testing.T) {
	ks, fk, c, oldPub, _ := lostKey(t)
	var out bytes.Buffer
	// a dry run with the flag reports the replacement and changes nothing
	if code := Run(context.Background(), Params{Mode: DryRun, Compiled: c, Keys: ks, Resolver: fk, ReplaceZoneKeys: []string{"a"}}, &out); code != 0 ||
		!strings.Contains(out.String(), "replaces the signing key "+oldPub+" (role a)") || !strings.Contains(out.String(), "by any other tool, stop working") {
		t.Fatalf("%d\n%s", code, out.String())
	}
	out.Reset()
	if code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk, ReplaceZoneKeys: []string{"a"}}, &out); code != 0 {
		t.Fatalf("%d\n%s", code, out.String())
	}
	newPub, err := ks.ZonePublic("a")
	if err != nil || newPub == oldPub {
		t.Fatalf("%v %s", err, newPub)
	}
	cl, _ := jwt.DecodeAccountClaims(fk.token)
	if _, ok := cl.SigningKeys.GetScope(oldPub); ok {
		t.Fatal("the old signer is still in the account JWT")
	}
	if _, ok := cl.SigningKeys.GetScope(newPub); !ok || len(cl.SigningKeys) != 2 {
		t.Fatalf("signers: %v", cl.SigningKeys.Keys())
	}
	// a flag for a zone the file lacks is an error
	out.Reset()
	if code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk, ReplaceZoneKeys: []string{"nope"}}, &out); code != 1 || !strings.Contains(out.String(), "no zone of that name") {
		t.Fatalf("%d %s", code, out.String())
	}
}

func TestConcurrentAppliesNeverLoseAZone(t *testing.T) {
	ks, fk, _ := setup(t)
	fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
	y1 := "default: deny\nendpoints:\n  x: {labels: {zone: a}}\n  y: {labels: {zone: b}}\nrules:\n  - {name: r, action: allow, from: {zone: a}, to: {zone: b}}\n"
	y2 := "default: deny\nendpoints:\n  x: {labels: {zone: c}}\n  y: {labels: {zone: d}}\nrules:\n  - {name: r, action: allow, from: {zone: c}, to: {zone: d}}\n"
	var wg sync.WaitGroup
	var mu sync.Mutex
	codes := map[int]int{}
	for _, y := range []string{y1, y2} {
		c := compile(t, y)
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out bytes.Buffer
			code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk, LockWait: 10 * time.Second}, &out)
			mu.Lock()
			codes[code]++
			mu.Unlock()
			if code != 0 && !strings.Contains(out.String(), "lock") && !strings.Contains(out.String(), "changed twice") && !strings.Contains(out.String(), "REFUSED unless") {
				t.Errorf("unclean error: %s", out.String())
			}
		}()
	}
	wg.Wait()
	cl, _ := jwt.DecodeAccountClaims(fk.token)
	have := map[string]bool{}
	for _, k := range cl.SigningKeys.Keys() {
		sc, _ := cl.SigningKeys.GetScope(k)
		if us, ok := sc.(*jwt.UserScope); ok {
			have[us.Role] = true
		}
	}
	if codes[0] == 2 && (!have["a"] || !have["b"] || !have["c"] || !have["d"]) {
		t.Fatalf("a zone was lost: %v", have)
	}
	if codes[0] == 0 {
		t.Fatalf("neither apply succeeded: %v", codes)
	}
}

func TestLockExcludesAndWaitsOut(t *testing.T) {
	ks, _, _ := setup(t)
	un, err := ks.Lock(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ks.Lock(50 * time.Millisecond); !errors.Is(err, ctlkeys.ErrLocked) {
		t.Fatalf("second lock: %v", err)
	}
	un()
	un2, err := ks.Lock(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	un2()
}

func TestOutsidePolicyZones(t *testing.T) {
	ks, fk, c := setup(t)
	fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
	var out bytes.Buffer
	if code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk}, &out); code != 0 {
		t.Fatal(out.String())
	}
	// an unrelated scoped signer (not created by mash) appears on the server
	op, _, _ := ks.Operator()
	cl, _ := jwt.DecodeAccountClaims(fk.token)
	other, err := ks.ZoneKey("legacy", true) // a zone key of the key store whose zone the file lacks
	if err != nil {
		t.Fatal(err)
	}
	opub, _ := other.PublicKey()
	sc := jwt.NewUserScope()
	sc.Key, sc.Role = opub, "legacy"
	cl.SigningKeys.AddScopedSigner(sc)
	tok, err := cl.Encode(op)
	if err != nil {
		t.Fatal(err)
	}
	fk.token = tok
	pushes := fk.pushes
	for _, mode := range []Mode{Apply, DryRun, Diff} {
		out.Reset()
		code := Run(context.Background(), Params{Mode: mode, Compiled: c, Keys: ks, Resolver: fk}, &out)
		want := 1
		if mode == Diff {
			want = 0
		}
		if code != want || !strings.Contains(out.String(), "REFUSED unless --remove-zone legacy or --keep-zone legacy") || strings.Contains(out.String(), "account claims already up to date") || fk.pushes != pushes || fk.token != tok {
			t.Fatalf("%s: %d\n%s", mode, code, out.String())
		}
	}
	// bad flag names
	for _, p := range []Params{{RemoveZones: []string{"nope"}}, {KeepZones: []string{"a"}}, {KeepZones: []string{"legacy"}, RemoveZones: []string{"legacy"}}} {
		p.Mode, p.Compiled, p.Keys, p.Resolver = Apply, c, ks, fk
		out.Reset()
		if code := Run(context.Background(), p, &out); code != 1 || fk.pushes != pushes {
			t.Fatalf("%+v: %d\n%s", p, code, out.String())
		}
	}
	// keep: proceeds with the warning, nothing pushed to the account but the unchanged JWT
	out.Reset()
	if code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk, KeepZones: []string{"legacy"}}, &out); code != 0 ||
		!strings.Contains(out.String(), "zone legacy is outside the policy and its key stays active") || !strings.Contains(out.String(), "account claims already up to date") {
		t.Fatalf("%d\n%s", code, out.String())
	}
	// remove: the signer is gone from the JWT, the other zones stay
	out.Reset()
	if code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk, RemoveZones: []string{"legacy"}}, &out); code != 0 || !strings.Contains(out.String(), "stop working") {
		t.Fatalf("%d\n%s", code, out.String())
	}
	after, _ := jwt.DecodeAccountClaims(fk.token)
	if _, ok := after.SigningKeys.GetScope(opub); ok || len(after.SigningKeys) != 2 {
		t.Fatalf("signers after remove: %v", after.SigningKeys.Keys())
	}
}

// A clustered server that sees no peers is not proof of a complete set: it needs the operator's --servers or it is
// UNCONFIRMED; a server naming no cluster is standalone.
func TestServerSetNeedsInventoryOrStandalone(t *testing.T) {
	ok := func(names ...string) []ServerAck {
		var a []ServerAck
		for _, n := range names {
			a = append(a, ServerAck{Server: n, Code: 200})
		}
		return a
	}
	cases := []struct {
		name      string
		servers   ServerSet
		inventory int
		acks      []ServerAck
		code      int
		want      string
	}{
		{"standalone", ServerSet{Names: []string{"s1"}, Expected: 1}, 0, ok("s1"), 0, "pushed: 1 server(s)"},
		{"cluster named, no inventory", ServerSet{Names: []string{"s1"}, Expected: 1, Cluster: "C"}, 0, ok("s1"), 1, "UNCONFIRMED: the server set could not be verified (this server is part of cluster C and the number of servers cannot be verified from here; pass --servers N)"},
		{"three of three", ServerSet{Names: []string{"s1", "s2", "s3"}, Expected: 3, Cluster: "C"}, 3, ok("s1", "s2", "s3"), 0, "pushed: 3 server(s)"},
		{"two of three", ServerSet{Names: []string{"s1", "s2"}, Expected: 2, Cluster: "C"}, 3, ok("s1", "s2"), 1, "UNCONFIRMED: the server set could not be verified (2 server(s) replied but --servers says 3)"},
		{"one with a cluster name", ServerSet{Names: []string{"s1"}, Expected: 1, Cluster: "C"}, 1, ok("s1"), 0, "pushed: 1 server(s)"},
		{"routes say more than the inventory", ServerSet{Names: []string{"s1"}, Expected: 3, Cluster: "C"}, 1, ok("s1"), 1, "the cluster reports 3"},
		{"inventory with a standalone reply", ServerSet{Names: []string{"s1"}, Expected: 1}, 2, ok("s1"), 1, "--servers says 2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ks, fk, c := setup(t)
			fk.servers, fk.acks, fk.applyPush = tc.servers, tc.acks, true
			var out bytes.Buffer
			code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk, Servers: tc.inventory, VerifyTries: 1, VerifyWait: 1}, &out)
			if code != tc.code || !strings.Contains(out.String(), tc.want) {
				t.Fatalf("%d\n%s", code, out.String())
			}
		})
	}
	ks, fk, c := setup(t)
	if code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk, Servers: -1}, &bytes.Buffer{}); code != 1 || fk.pushes != 0 {
		t.Fatalf("negative servers: %d", code)
	}
}

// A successful apply, a diff and a dry run print the "Not covered by apply" block, naming the endpoints that carry
// exception tags, and the up-to-date text says it is about the account claims.
func TestNotCoveredBlockOnSuccess(t *testing.T) {
	ks, fk, c := setup(t)
	c.Endpoints[0].Tags = []string{"allow:b.y"}
	fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
	for i, mode := range []Mode{Diff, Apply, Apply} {
		var out bytes.Buffer
		if code := Run(context.Background(), Params{Mode: mode, Compiled: c, Keys: ks, Resolver: fk}, &out); code != 0 {
			t.Fatalf("%d: %d\n%s", i, code, out.String())
		}
		s := out.String()
		for _, w := range []string{"Not covered by apply:", "1 endpoint(s) carry exception tags: x", "mashctl reissue", "nsc", "mashctl policy drift"} {
			if !strings.Contains(s, w) {
				t.Fatalf("%d: missing %q\n%s", i, w, s)
			}
		}
		if i == 2 && !strings.Contains(s, "account claims already up to date") {
			t.Fatalf("%s", s)
		}
	}
}

// --replace-zone-key drops the named zone's own signer only: a plain signing key, a scoped key of a role that is not a
// zone of the file, and one whose role differs from a zone's in case are still refused.
func TestReplaceZoneKeyDoesNotCoverForeignKeys(t *testing.T) {
	ks, fk, c, scoped, plain, _ := unknownSetup(t)
	op, _, _ := ks.Operator()
	ck, _ := nkeys.CreateAccount()
	caseKey := pubOf(t, ck)
	resolverToken(t, fk, ks, op, func(cl *jwt.AccountClaims) {
		cl.SigningKeys.Add(plain)
		for key, role := range map[string]string{scoped: "legacy", caseKey: "A"} {
			sc := jwt.NewUserScope()
			sc.Key, sc.Role = key, role
			cl.SigningKeys.AddScopedSigner(sc)
		}
	})
	before := fk.token
	fk.acks, fk.applyPush = []ServerAck{{Server: "s1", Code: 200}}, true
	for _, mode := range []Mode{Apply, DryRun} {
		var out bytes.Buffer
		p := c
		p.Mode, p.Keys, p.Resolver, p.ReplaceZoneKeys = mode, ks, fk, []string{"a"}
		code := Run(context.Background(), p, &out)
		s := out.String()
		for _, w := range []string{"REFUSED", scoped, plain, caseKey, "role A"} {
			if !strings.Contains(s, w) {
				t.Fatalf("%s: missing %q:\n%s", mode, w, s)
			}
		}
		if code != 1 || fk.pushes != 0 || fk.token != before {
			t.Fatalf("%s: exit %d, pushes %d", mode, code, fk.pushes)
		}
	}
	if z, _ := ks.Zones(); len(z) != 0 {
		t.Fatalf("created zone keys %v", z)
	}
}
