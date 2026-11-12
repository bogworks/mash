package apply

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"

	"github.com/bogworks/mash/internal/policy"
)

// A list set that pushes one sender past the smallest of several servers' limits is refused whole, naming the sender,
// its size, the limit and that server; nothing is created, signed or pushed (AT-006).
func TestSmallestServerRefusesTheWholeSet(t *testing.T) {
	ks, fk, c := setup(t)
	fk.servers = ServerSet{Names: []string{"nats-1", "nats-2", "nats-3"}, Expected: 3}
	fk.limits = map[string]int{"nats-1": 4096, "nats-2": 500, "nats-3": 8192}
	var out bytes.Buffer
	code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk, Servers: 3}, &out)
	s := out.String()
	if code != 1 {
		t.Fatalf("exit %d\n%s", code, s)
	}
	for _, want := range []string{`sender "x"`, `server "nats-2"`, "allows 500 (max_control_line)", "connect line would be", "bytes with", "tags", "nothing was changed, signed or pushed"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	for _, not := range []string{`server "nats-1"`, `server "nats-3"`} {
		if strings.Contains(s, not) {
			t.Errorf("names a server that takes it: %s\n%s", not, s)
		}
	}
	if fk.pushes != 0 {
		t.Error("pushed")
	}
	if z, _ := ks.Zones(); len(z) != 0 {
		t.Errorf("created keys %v", z)
	}
	if fk.lastPush != "" {
		t.Error("a token was sent")
	}
}

// An account JWT over one server's limit is refused naming the account, its size, the limit and the server.
func TestAccountJWTOverAServersLimit(t *testing.T) {
	ks, fk, c := setup(t)
	fk.servers = ServerSet{Names: []string{"nats-1", "nats-2"}, Expected: 2}
	fk.maxPayload = 400 // every server's max_payload: the account JWT cannot be pushed to it
	fk.limits = map[string]int{"nats-1": 4096, "nats-2": 4096}
	var out bytes.Buffer
	code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk, Servers: 2}, &out)
	s := out.String()
	if code != 1 || !strings.Contains(s, `account "account"`) || !strings.Contains(s, `server "nats-1"`) || !strings.Contains(s, `server "nats-2"`) ||
		!strings.Contains(s, "allows 400 (max_payload)") || !strings.Contains(s, "its JWT would be") {
		t.Fatalf("exit %d\n%s", code, s)
	}
	if fk.pushes != 0 {
		t.Error("pushed")
	}
}

// The account JWT apply builds from the real claims is checked against every server's limit too, naming the account
// by the name it has and each server over.
func TestBuiltAccountJWTChecksEveryServer(t *testing.T) {
	ks, fk, c := setup(t)
	fk.servers = ServerSet{Names: []string{"nats-1", "nats-2"}, Expected: 2}
	fk.maxPayload = 400
	fk.limits = map[string]int{"nats-1": 4096, "nats-2": 4096}
	var out bytes.Buffer
	ok := func(*policy.Compiled, []policy.ServerLimits) (policy.BudgetReport, error) {
		return policy.BudgetReport{OK: true}, nil
	}
	code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk, Servers: 2, Budget: ok}, &out)
	s := out.String()
	if code != 1 || !strings.Contains(s, `server "nats-1"`) || !strings.Contains(s, `server "nats-2"`) || !strings.Contains(s, "allows 400 (max_payload)") {
		t.Fatalf("exit %d\n%s", code, s)
	}
	if fk.pushes != 0 {
		t.Error("pushed")
	}
}

// A server that gives no limit is not assumed small or large: apply refuses and says so, unless the operator gives
// the limit, which then stands for every server that did not report.
func TestUnknownServerLimit(t *testing.T) {
	ks, fk, c := setup(t)
	fk.servers = ServerSet{Names: []string{"nats-1", "nats-2"}, Expected: 2}
	fk.limits = map[string]int{"nats-1": 4096, "nats-2": 0} // nats-2 does not answer the limits request
	var out bytes.Buffer
	code := Run(context.Background(), Params{Mode: Apply, Compiled: c, Keys: ks, Resolver: fk, Servers: 2}, &out)
	s := out.String()
	if code != 1 || !strings.Contains(s, `"nats-2"`) || !strings.Contains(s, "is not known") || !strings.Contains(s, "--max-control-line") || strings.Contains(s, `"nats-1"`) {
		t.Fatalf("exit %d\n%s", code, s)
	}
	if fk.pushes != 0 {
		t.Error("pushed")
	}
	out.Reset()
	code = Run(context.Background(), Params{Mode: DryRun, Compiled: c, Keys: ks, Resolver: fk, Servers: 2, MaxControlLine: 4096}, &out)
	if code != 0 {
		t.Fatalf("with the operator's limit: exit %d\n%s", code, out.String())
	}
	out.Reset()
	code = Run(context.Background(), Params{Mode: DryRun, Compiled: c, Keys: ks, Resolver: fk, Servers: 2, MaxControlLine: 500}, &out)
	if code != 1 || !strings.Contains(out.String(), `server "nats-2"`) || !strings.Contains(out.String(), "allows 500") {
		t.Fatalf("with a small operator limit: exit %d\n%s", code, out.String())
	}
}

func TestLearnLimits(t *testing.T) {
	reps := []ServerReport{{Server: "a", MaxControlLine: 4096, MaxPayload: 1 << 20}, {Server: "b", MaxControlLine: 1024, MaxPayload: 200_000}, {Server: "c"}}
	got, err := LearnLimits([]string{"a", "b"}, reps, 0)
	if err != nil || len(got) != 2 || got[0].Server != "a" || got[0].MaxControlLine != 4096 || got[0].AccountJWT != policy.AccountJWTLimit ||
		got[1].Server != "b" || got[1].MaxControlLine != 1024 || got[1].AccountJWT != 200_000 {
		t.Fatalf("%+v %v", got, err)
	}
	// a server of the set that did not report is unknown; the override fills it and caps a reporting server
	_, err = LearnLimits([]string{"a", "b", "c"}, reps, 0)
	if !errors.Is(err, ErrLimitUnknown) || !strings.Contains(err.Error(), `"c"`) {
		t.Fatalf("%v", err)
	}
	got, err = LearnLimits([]string{"a", "b", "c"}, reps, 2000)
	if err != nil || len(got) != 3 || got[0].MaxControlLine != 2000 || got[1].MaxControlLine != 1024 || got[2].MaxControlLine != 2000 {
		t.Fatalf("%+v %v", got, err)
	}
	// nobody answered at all
	if _, err = LearnLimits(nil, nil, 0); !errors.Is(err, ErrLimitUnknown) {
		t.Fatalf("%v", err)
	}
	if got, err = LearnLimits(nil, nil, 4096); err != nil || len(got) != 1 || got[0].MaxControlLine != 4096 {
		t.Fatalf("%+v %v", got, err)
	}
}

// Over the real transport: every server answers the VARZ request with its own max_control_line, and apply refuses a
// sender that does not fit it, naming the server, before anything is pushed.
func TestLimitsLive(t *testing.T) {
	const small = 900
	env := startLive(t, func(o *server.Options) { o.MaxControlLine = small })
	n := NATS{Conn: env.sysNC, Inventory: 1}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reps, err := n.ServerLimits(ctx, 1)
	if err != nil || len(reps) != 1 || reps[0].Server != "s1" || reps[0].MaxControlLine != small || reps[0].MaxPayload != 1<<20 {
		t.Fatalf("%+v %v", reps, err)
	}
	set, err := n.Servers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lim, err := Limits(ctx, n, set, 0)
	if err != nil || len(lim) != 1 || lim[0].Server != "s1" || lim[0].MaxControlLine != small || lim[0].AccountJWT != policy.AccountJWTLimit {
		t.Fatalf("%+v %v", lim, err)
	}

	// Every sender here has a connect line of a bit over a kilobyte: refused at the server's 1,024, before a push.
	_, _, c := setup(t)
	before, err := n.Lookup(ctx, env.acctPub)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := Run(ctx, Params{Mode: Apply, Compiled: c, Keys: env.ks, Resolver: n, Servers: 1}, &out); code != 1 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	for _, want := range []string{`sender "x"`, `server "s1"`, "allows 900 (max_control_line)", "nothing was changed, signed or pushed"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in\n%s", want, out.String())
		}
	}
	after, err := n.Lookup(ctx, env.acctPub)
	if err != nil || after != before {
		t.Fatalf("the account JWT changed on a refusal: %v", err)
	}
	if z, _ := env.ks.Zones(); len(z) != 0 {
		t.Errorf("created keys %v", z)
	}
}
