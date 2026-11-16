package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/credential"
	"github.com/bogworks/mash/internal/issue"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/policydirect"
	"github.com/bogworks/mash/internal/sap"
)

// lockedBuffer is a log sink that is safe to share between goroutines.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// credEndpoint is one endpoint of the test: the key it made for itself, its address and its connection.
type credEndpoint struct {
	name   string
	seed   []byte
	pub    string
	addr   string
	flat   string
	nc     *nats.Conn
	closed chan struct{} // closed when the server drops or closes the connection
	once   sync.Once

	mu         sync.Mutex
	violations []string
}

func (c *credEndpoint) denied(subject string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, v := range c.violations {
		if strings.Contains(v, `Publish to "`+subject+`"`) {
			return true
		}
	}
	return false
}

// credConnect connects creds on the server at url, recording permission violations and the close.
func (c *credEndpoint) connect(t *testing.T, url, credsPath string) {
	t.Helper()
	c.closed = make(chan struct{})
	nc, err := nats.Connect(url, natsauth.UserCredentials(credsPath), nats.Timeout(serveWait), nats.CustomInboxPrefix("_INBOX."+c.flat),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			c.mu.Lock()
			c.violations = append(c.violations, err.Error())
			c.mu.Unlock()
		}),
		nats.NoReconnect(),
		nats.DisconnectErrHandler(func(*nats.Conn, error) { c.once.Do(func() { close(c.closed) }) }),
		nats.ClosedHandler(func(*nats.Conn) { c.once.Do(func() { close(c.closed) }) }))
	if err != nil {
		t.Fatalf("%s: connect with the assembled credential: %v", c.name, err)
	}
	t.Cleanup(nc.Close)
	c.nc = nc
}

// A credential for a public key the endpoint made, with no service running and no requester: the endpoint makes its
// key, the holder of the keys signs for the public half only, the endpoint assembles its creds file and connects to the
// OTHER server than the one the policy was applied through. It reaches who the list set says and no one else; a seed is
// in no output; the record finds it by public key; a revoked key is refused; a key bound to another address is refused.
func TestCredentialForAPublicKeyOnTwoRoutedServers(t *testing.T) {
	e, second := newServeEnvCluster(t, false)
	owner := e.env["MASH_OWNER"]
	adm := e.adminOnly()
	sys := e.connect(natsauth.UserCredentials(e.env["MASH_SYS_CREDS"]))
	cfg := e.directConfig(adm, sys, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*serveWait)
	defer cancel()

	logs := &lockedBuffer{}
	var outputs []string // every Result, Report and log the test sees: no seed may be in any
	record := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		outputs = append(outputs, string(b))
	}

	// The endpoints make their keys and are provisioned (directory record, inbox) for the public half only.
	names := []string{"x", "y", "z"}
	eps := map[string]*credEndpoint{}
	for _, n := range names {
		seed, pub, err := credential.NewKey()
		if err != nil {
			t.Fatal(err)
		}
		ident, err := sap.New(sap.AgentName, owner, n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := adm.ProvisionIdentity(ctx, ident, nil, pub, e.acctPub, "", admin.Meta{}); err != nil {
			t.Fatalf("provision %s: %v", n, err)
		}
		eps[n] = &credEndpoint{name: n, seed: seed, pub: pub, addr: owner + "." + n, flat: ident.Flat()}
	}
	req := func(n string) credential.Request {
		return credential.Request{Address: eps[n].addr, Endpoint: sap.AgentName + "." + eps[n].addr, PublicKey: eps[n].pub}
	}

	ccfg, err := credential.FromDirect(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ccfg.Log = slog.New(slog.NewTextHandler(logs, nil))

	// No list set in force: refused, and nothing is recorded.
	res, err := credential.Issue(ctx, ccfg, req("x"))
	record(res)
	if err != nil || res.Outcome != credential.OutcomeRefused || res.Refusal == nil || res.Refusal.Code != issue.CodeNoListSet || res.ExitCode() != 1 {
		t.Fatalf("before any policy: %+v, %v", res, err)
	}
	if _, found, err := adm.IssuedByKey(ctx, eps["x"].pub); err != nil || found {
		t.Fatalf("a refused issue left a record: %v %v", found, err)
	}

	// The list set: x may reach y, z may not. Every verb closed for the group, so reach is the allow lists only.
	doc := e.serveDoc(1, 0, func(m map[string]any) {
		m["groups"] = map[string]any{owner: map[string]any{"mail": "closed", "prompt": "closed", "status": "closed", "heartbeat": "closed"}}
		m["senders"] = map[string]any{
			eps["x"].addr: map[string]any{"allow": []string{eps["y"].addr}, "deny": []string{}},
			eps["y"].addr: map[string]any{"allow": []string{}, "deny": []string{}},
			eps["z"].addr: map[string]any{"allow": []string{}, "deny": []string{}},
		}
	})
	dry := directApply(t, cfg, doc, true)
	record(dry)
	if dry.Outcome != policydirect.OutcomePlanned || dry.Plan == nil {
		t.Fatalf("dry run: %+v", dry)
	}
	checkFirst := func(what string, r policydirect.Result) {
		t.Helper()
		got := map[string]policydirect.NextStep{}
		for _, s := range r.NextSteps {
			got[s.Sender] = s
		}
		for _, n := range names {
			s, ok := got[eps[n].addr]
			if !ok || s.Step != "first_credential" || s.Action != "issue" || s.Purpose != "issue" || s.Address != eps[n].addr || s.Detail == "" {
				t.Errorf("%s: no first_credential step for %s: %+v", what, n, r.NextSteps)
			}
		}
		if len(r.NextSteps) != len(names) {
			t.Errorf("%s: %d next steps, want %d: %+v", what, len(r.NextSteps), len(names), r.NextSteps)
		}
	}
	checkFirst("dry run", dry)

	// The apply: the real run lists the same first credentials.
	applied := directApply(t, cfg, doc, false)
	record(applied)
	if applied.Outcome != policydirect.OutcomeApplied {
		t.Fatalf("apply: %+v", applied)
	}
	checkFirst("real run", applied)

	// Issue for each, assemble, and connect on the other server (s2): the policy was applied through s1.
	other := second.ClientURL()
	if other == e.srv.ClientURL() {
		t.Fatal("the two servers share a URL")
	}
	for _, n := range names {
		ep := eps[n]
		out, err := credential.Issue(ctx, ccfg, req(n))
		record(out)
		if err != nil || out.Outcome != credential.OutcomeIssued || out.JWT == "" || out.ExitCode() != 0 || out.Generation != 1 {
			t.Fatalf("issue %s: %+v, %v", n, out, err)
		}
		creds, err := credential.Assemble(out.JWT, ep.seed)
		if err != nil {
			t.Fatalf("assemble %s: %v", n, err)
		}
		path := filepath.Join(t.TempDir(), n+".creds")
		if err := os.WriteFile(path, creds, 0o600); err != nil {
			t.Fatal(err)
		}
		clear(creds)
		ep.connect(t, other, path)
	}

	// An address that is not a sender of the list set is refused as unlisted.
	_, strayPub, _ := credential.NewKey()
	res, err = credential.Issue(ctx, ccfg, credential.Request{Address: owner + ".w", Endpoint: sap.AgentName + "." + owner + ".w", PublicKey: strayPub})
	record(res)
	if err != nil || res.Refusal == nil || res.Refusal.Code != issue.CodeUnlisted {
		t.Fatalf("an unlisted address: %+v, %v", res, err)
	}
	// An endpoint at another address than the request's is refused as a mismatch.
	bad := req("x")
	bad.Endpoint = sap.AgentName + "." + eps["y"].addr
	if res, _ = credential.Issue(ctx, ccfg, bad); res.Refusal == nil || res.Refusal.Code != issue.CodeEndpoint {
		t.Fatalf("an endpoint at another address: %+v", res)
	}

	// Reach: the observer is on s1 and listens on y's mail and prompt subjects; x and z publish from s2.
	mailY := "agents.mail." + sap.AgentName + "." + eps["y"].addr
	promptY := "agents.prompt." + sap.AgentName + "." + eps["y"].addr
	got := make(chan string, 256)
	for _, subj := range []string{mailY, promptY} {
		if _, err := e.nc.Subscribe(subj, func(m *nats.Msg) { got <- m.Subject + "|" + string(m.Data) }); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.nc.Flush(); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	drain := func() {
		for {
			select {
			case s := <-got:
				seen[s] = true
			default:
				return
			}
		}
	}
	// x is allowed: publish until each subject has delivered (the route's interest may take a moment).
	deadline := time.Now().Add(serveWait)
	for !(seen[mailY+"|x"] && seen[promptY+"|x"]) && time.Now().Before(deadline) {
		for _, s := range []string{mailY, promptY} {
			_ = eps["x"].nc.Publish(s, []byte("x"))
		}
		_ = eps["x"].nc.Flush()
		time.Sleep(100 * time.Millisecond)
		drain()
	}
	if !seen[mailY+"|x"] || !seen[promptY+"|x"] {
		t.Fatalf("x did not reach y's subjects: %v (violations %v)", seen, eps["x"].violations)
	}
	if eps["x"].denied(mailY) || eps["x"].denied(promptY) {
		t.Errorf("x was refused a subject it may reach: %v", eps["x"].violations)
	}
	// z is not allowed: the server refuses each publish and nothing is delivered.
	for _, s := range []string{mailY, promptY} {
		_ = eps["z"].nc.Publish(s, []byte("z"))
	}
	_ = eps["z"].nc.Flush()
	for deadline = time.Now().Add(serveWait); !(eps["z"].denied(mailY) && eps["z"].denied(promptY)) && time.Now().Before(deadline); {
		time.Sleep(servePoll)
	}
	if !eps["z"].denied(mailY) || !eps["z"].denied(promptY) {
		t.Fatalf("z was not refused y's subjects: %v", eps["z"].violations)
	}
	time.Sleep(500 * time.Millisecond)
	drain()
	if seen[mailY+"|z"] || seen[promptY+"|z"] {
		t.Fatalf("z's message reached y: %v", seen)
	}
	// z still reaches its own inbox, and not y's.
	jsz, err := jetstream.New(eps["z"].nc)
	if err != nil {
		t.Fatal(err)
	}
	sctx, scancel := context.WithTimeout(ctx, 20*time.Second)
	if _, err := jsz.Stream(sctx, sap.InboxStream(eps["z"].flat)); err != nil {
		t.Errorf("z cannot reach its own inbox: %v", err)
	}
	scancel()
	sctx, scancel = context.WithTimeout(ctx, 3*time.Second)
	if _, err := jsz.Stream(sctx, sap.InboxStream(eps["y"].flat)); err == nil {
		t.Error("z reached y's inbox stream")
	}
	scancel()

	// Show finds the key by its public key alone.
	rep, err := credential.Show(ctx, ccfg, eps["x"].pub)
	record(rep)
	if err != nil || !rep.Recorded || rep.Record == nil || rep.Record.Address != eps["x"].addr || rep.History == nil || rep.History.Revisions < 1 ||
		rep.AccountRevokes != credential.StateNo || rep.Connection.State != credential.StateYes || !rep.Complete || !rep.Clean {
		t.Fatalf("show: %+v, %v", rep, err)
	}
	if rep.Record.Generation != 1 || len(rep.Record.Tags) == 0 || rep.Record.Purpose != "issue" {
		t.Errorf("record %+v", rep.Record)
	}

	// A key bound to another address is refused: y's key asked for at x's address.
	bound := req("x")
	bound.PublicKey = eps["y"].pub
	res, err = credential.Issue(ctx, ccfg, bound)
	record(res)
	if err != nil || res.Refusal == nil || res.Refusal.Code != issue.CodeBound || res.JWT != "" {
		t.Fatalf("a key bound elsewhere: %+v, %v", res, err)
	}

	// Revoke x with the existing revoke path: the account JWT revokes it, the record says so, and the connection closes.
	env := func(k string) string { return e.env[k] }
	mcfg, err := config.Load(config.LookupFromFunc(env), e.env["HOME"])
	if err != nil {
		t.Fatal(err)
	}
	var revOut bytes.Buffer
	if err := revokeUserKeys(ctx, mcfg, env, 2, []string{eps["x"].pub}, &revOut, nil); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := adm.MarkRevoked(ctx, eps["x"].pub, time.Now()); err != nil {
		t.Fatalf("mark revoked: %v", err)
	}
	select {
	case <-eps["x"].closed:
	case <-time.After(serveWait):
		t.Fatalf("the revoked key's connection stayed open (status %v, last error %v, revoke output %q)", eps["x"].nc.Status(), eps["x"].nc.LastError(), revOut.String())
	}
	if !eps["y"].nc.IsConnected() || !eps["z"].nc.IsConnected() {
		t.Error("a revocation of x closed another endpoint")
	}
	res, err = credential.Issue(ctx, ccfg, req("x"))
	record(res)
	if err != nil || res.Outcome != credential.OutcomeRefused || res.Refusal == nil || res.Refusal.Code != issue.CodeRevoked || res.JWT != "" {
		t.Fatalf("a revoked key: %+v, %v", res, err)
	}
	rep, err = credential.Show(ctx, ccfg, eps["x"].pub)
	record(rep)
	if err != nil || rep.AccountRevokes != credential.StateYes || rep.Record == nil || rep.Record.RevokedAt == "" || rep.Connection.State != credential.StateNo || !rep.Clean {
		t.Fatalf("show after revoke: %+v, %v", rep, err)
	}

	// No seed is in any result, report, log or output the test saw, nor in a key-store file's text.
	outputs = append(outputs, logs.String(), revOut.String())
	for _, n := range names {
		for _, o := range outputs {
			if strings.Contains(o, string(eps[n].seed)) {
				t.Fatalf("the seed of %s is in an output:\n%.300s", n, o)
			}
		}
	}
	for _, o := range outputs {
		e.noSeeds(o)
	}
	if !strings.Contains(logs.String(), "credential issued") || !strings.Contains(logs.String(), "credential refused") {
		t.Errorf("the log says nothing of the issues and refusals:\n%s", logs.String())
	}
}
