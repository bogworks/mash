package policy

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
)

// liveServer is an operator-mode nats-server (in process) with one account whose signing keys are the compiled zone
// keys, as scoped signers. Users are minted the way mashctl will: signed by their zone's key, carrying their tags.
type liveServer struct {
	t     *testing.T
	c     *Compiled
	url   string
	acct  string // account public key
	keys  map[string]nkeys.KeyPair
	admin *liveConn
}

// liveConn is one connected user with the permission violations it was told about and, for the admin, every
// message it saw.
type liveConn struct {
	name string
	nc   *nats.Conn
	mu   sync.Mutex
	errs []string
	seen map[string]bool // "<sender>|<subject>" delivered to the admin's catch-all subscription
}

func (l *liveConn) violations() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.errs)
}

func startLive(t *testing.T, c *Compiled) *liveServer {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	opKP, err := nkeys.CreateOperator()
	must(err)
	opPub, _ := opKP.PublicKey()
	oc := jwt.NewOperatorClaims(opPub)
	oc.Name = "policy-live"
	opJWT, err := oc.Encode(opKP)
	must(err)
	op, err := jwt.DecodeOperatorClaims(opJWT)
	must(err)

	resolver := &server.MemAccResolver{}
	sysKP, _ := nkeys.CreateAccount()
	sysPub, _ := sysKP.PublicKey()
	sc := jwt.NewAccountClaims(sysPub)
	sc.Name = "SYS"
	sysJWT, err := sc.Encode(opKP)
	must(err)
	must(resolver.Store(sysPub, sysJWT))

	acctKP, _ := nkeys.CreateAccount()
	acctPub, _ := acctKP.PublicKey()
	ac := jwt.NewAccountClaims(acctPub)
	ac.Name = "MASH"
	keys := map[string]nkeys.KeyPair{}
	for _, z := range c.Zones {
		kp, _ := nkeys.CreateAccount()
		pub, _ := kp.PublicKey()
		keys[z.Name] = kp
		scope := jwt.NewUserScope()
		scope.Key = pub
		scope.Role = z.Name
		scope.Template.Permissions = z.Perms
		ac.SigningKeys.AddScopedSigner(scope)
	}
	acctJWT, err := ac.Encode(opKP)
	must(err)
	must(resolver.Store(acctPub, acctJWT))

	s, err := server.NewServer(&server.Options{
		Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true,
		TrustedOperators: []*jwt.OperatorClaims{op}, AccountResolver: resolver, SystemAccount: sysPub,
	})
	must(err)
	go s.Start()
	if !s.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats-server did not start")
	}
	t.Cleanup(func() { s.Shutdown(); s.WaitForShutdown() })

	h := &liveServer{t: t, c: c, url: s.ClientURL(), acct: acctPub, keys: keys}
	// The admin is signed by the account's own key, unrestricted, and watches everything.
	uk, _ := nkeys.CreateUser()
	upub, _ := uk.PublicKey()
	uc := jwt.NewUserClaims(upub)
	uc.Name = "admin"
	tok, err := uc.Encode(acctKP)
	must(err)
	h.admin, err = h.connect("admin", tok, uk)
	must(err)
	h.admin.seen = map[string]bool{}
	_, err = h.admin.nc.Subscribe(">", func(m *nats.Msg) {
		h.admin.mu.Lock()
		h.admin.seen[string(m.Data)+"|"+m.Subject] = true
		h.admin.mu.Unlock()
	})
	must(err)
	must(h.admin.nc.Flush())
	return h
}

func (h *liveServer) connect(name, userJWT string, kp nkeys.KeyPair) (*liveConn, error) {
	l := &liveConn{name: name}
	nc, err := nats.Connect(h.url, nats.Name(name), nats.MaxReconnects(0), nats.Timeout(5*time.Second),
		nats.UserJWT(func() (string, error) { return userJWT, nil }, func(n []byte) ([]byte, error) { return kp.Sign(n) }),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			l.mu.Lock()
			l.errs = append(l.errs, err.Error())
			l.mu.Unlock()
		}))
	if err != nil {
		return nil, err
	}
	h.t.Cleanup(nc.Close)
	l.nc = nc
	return l, nil
}

// user mints and connects the user of compiled endpoint name with the tags given (its compiled tags unless
// overridden).
func (h *liveServer) user(name string, tags []string) (*liveConn, error) {
	e, ok := h.c.endpoint(name)
	if !ok {
		h.t.Fatalf("no endpoint %s", name)
	}
	uk, _ := nkeys.CreateUser()
	upub, _ := uk.PublicKey()
	uc := jwt.NewUserClaims(upub)
	uc.Name = name
	uc.IssuerAccount = h.acct
	uc.UserPermissionLimits = jwt.UserPermissionLimits{} // a scoped key's users carry no permissions or limits of their own
	uc.Tags.Add(tags...)
	tok, err := uc.Encode(h.keys[e.Zone])
	if err != nil {
		h.t.Fatal(err)
	}
	// What is on the wire carries no permission or limit of its own: the zone key's template is all there is.
	if dec, err := jwt.DecodeUserClaims(tok); err != nil {
		h.t.Fatal(err)
	} else if !reflect.DeepEqual(dec.UserPermissionLimits, jwt.UserPermissionLimits{}) {
		h.t.Fatalf("%s: the decoded JWT has permissions or limits of its own: %+v", name, dec.UserPermissionLimits)
	}
	return h.connect(name, tok, uk)
}

// probe publishes a core message to every subject as u and returns which were delivered (the admin saw them) and
// which the server refused (u was told of a permissions violation). Each publish ends in exactly one of the two.
func (h *liveServer) probe(u *liveConn, subjects []string) (allowed, denied []string) {
	h.t.Helper()
	before := len(u.violations())
	for _, s := range subjects {
		if err := u.nc.Publish(s, []byte(u.name)); err != nil {
			h.t.Fatal(err)
		}
	}
	if err := u.nc.Flush(); err != nil {
		h.t.Fatal(err)
	}
	signals := func() (a, d []string) {
		h.admin.mu.Lock()
		for _, s := range subjects {
			if h.admin.seen[u.name+"|"+s] {
				a = append(a, s)
			}
		}
		h.admin.mu.Unlock()
		for _, v := range u.violations()[before:] {
			for _, s := range subjects {
				if strings.Contains(v, "Publish to \""+s+"\"") {
					d = append(d, s)
				}
			}
		}
		return
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		a, d := signals()
		if len(a)+len(d) >= len(subjects) || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // a late extra signal would show here
	allowed, denied = signals()
	slices.Sort(allowed)
	slices.Sort(denied)
	if len(allowed)+len(denied) != len(subjects) {
		h.t.Fatalf("%s: %d subjects, %d delivered, %d refused (violations: %v)", u.name, len(subjects), len(allowed), len(denied), u.violations()[before:])
	}
	return allowed, denied
}

// liveMatrix connects a user per endpoint with its compiled tags, publishes every ordered pair and verb, and fails
// unless the server allowed exactly the compiled recipients. It returns the connections by endpoint name.
func (h *liveServer) liveMatrix(t *testing.T) map[string]*liveConn {
	t.Helper()
	c := h.c
	conns := map[string]*liveConn{}
	for _, e := range c.Endpoints {
		u, err := h.user(e.Name, e.Tags)
		if err != nil {
			t.Fatalf("connect %s (tags %v): %v", e.Name, e.Tags, err)
		}
		conns[e.Name] = u
	}

	pairs, allowedPairs := 0, 0
	for _, from := range c.Endpoints {
		var subjects, want []string
		for _, to := range c.Endpoints {
			// Mail is policed and carries the zone token (six tokens): allowed exactly for the compiled recipients.
			mail := strings.Replace(MailSubjectPattern(c.Owner, to.Zone, to.Name), "*", "cc", 1)
			subjects = append(subjects, mail)
			if slices.Contains(from.Recipients, to.Name) {
				want = append(want, mail)
			}
			// Prompt and status keep the five-token SAP subjects and are open: every peer, whatever the rules say.
			for _, verb := range []string{"prompt", "status"} {
				open := "agents." + verb + ".cc." + c.Owner + "." + to.Name
				subjects = append(subjects, open)
				want = append(want, open)
			}
		}
		slices.Sort(want)
		allowed, denied := h.probe(conns[from.Name], subjects)
		pairs += len(subjects)
		allowedPairs += len(allowed)
		if !slices.Equal(allowed, want) {
			t.Errorf("%s (zone %s, tags %v): the server allowed\n  %v\nCompile says\n  %v\n(refused %v)", from.Name, from.Zone, from.Tags, allowed, want, denied)
		}
	}
	t.Logf("%d sender-recipient-subject publishes (mail policed, prompt and status open) over %d endpoints: %d allowed, %d refused, all as compiled", pairs, len(c.Endpoints), allowedPairs, pairs-allowedPairs)
	return conns
}

// TestLiveEnterprise enforces the compiled enterprise file on a real nats-server: one user per endpoint, signed by its
// zone's scoped key with its tags, publishes to every ordered pair and verb, and the server allows exactly the
// pairs Compile says. It covers a wildcard zone line (internal to internal), an allow exception by name
// (web-intake to support-bot), an allow exception for a whole zone (support-bot to dmz), a deny exception on top of a
// wildcard (nobody in internal reaches hr-agent) and the fixed per-endpoint lines through {{name()}}.
func TestLiveEnterprise(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a nats-server")
	}
	data, err := os.ReadFile("testdata/enterprise.yaml")
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Compile(f, goldenOwner)
	if err != nil {
		t.Fatal(err)
	}
	// The encodings this test is about are really in the compile.
	var sawAllowName, sawAllowZone, sawDeny bool
	for _, e := range c.Endpoints {
		for _, tag := range e.Tags {
			sawAllowName = sawAllowName || tag == "allow:internal.support-bot"
			sawAllowZone = sawAllowZone || tag == "allow:dmz.web-intake"
			sawDeny = sawDeny || tag == "deny:internal.hr-agent"
		}
	}
	if !sawAllowName || !sawAllowZone || !sawDeny {
		t.Fatalf("enterprise compile lacks an encoding: name %v zone %v deny %v", sawAllowName, sawAllowZone, sawDeny)
	}
	h := startLive(t, c)

	conns := h.liveMatrix(t)

	// The fixed lines are per user: {{name()}} is the name in the user's own JWT.
	for _, name := range []string{"web-intake", "hr-agent"} {
		u := conns[name]
		other := "billing-agent"
		own := []string{
			"agents.hb.cc.acme." + name,
			"$KV.mash-leases." + name,
			"$JS.API.CONSUMER.MSG.NEXT.inbox-" + name + ".deliver",
			"$JS.API.STREAM.INFO.inbox-" + name,
			"$JS.ACK.inbox-" + name + ".deliver.1.2.3",
		}
		others := []string{
			"agents.hb.cc.acme." + other,
			"$KV.mash-leases." + other,
			"$JS.API.CONSUMER.MSG.NEXT.inbox-" + other + ".deliver",
			"$JS.API.STREAM.INFO.inbox-" + other,
			"$JS.ACK.inbox-" + other + ".deliver.1.2.3",
			"agents.hb.cc.acme.not-an-endpoint",
			"agents.mail.cc.otherowner.internal.billing-agent", // another owner
		}
		if a, d := h.probe(u, own); len(a) != len(own) {
			t.Errorf("%s: own lines refused: %v", name, d)
		}
		if a, _ := h.probe(u, others); len(a) != 0 {
			t.Errorf("%s: another endpoint's lines allowed: %v", name, a)
		}
	}

	// Subscriptions: the own five-token prompt subject is allowed, a peer's is not, and neither is a zoned one.
	u := conns["billing-agent"]
	subOK, subBad := "agents.prompt.cc.acme.billing-agent", "agents.prompt.cc.acme.ledger-agent"
	before := len(u.violations())
	for _, s := range []string{subOK, subBad, "agents.prompt.cc.acme.internal.billing-agent"} {
		if _, err := u.nc.Subscribe(s, func(*nats.Msg) {}); err != nil {
			t.Fatal(err)
		}
	}
	if err := u.nc.Flush(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	got := strings.Join(u.violations()[before:], "\n")
	if strings.Contains(got, subOK) || !strings.Contains(got, subBad) || !strings.Contains(got, "acme.internal.billing-agent") {
		t.Errorf("subscription violations:\n%s", got)
	}

	// The deny tag is what refuses hr-agent: the same user with only the placeholder tag may mail it.
	if u, err := h.user("billing-agent", []string{admin.NoDenyTag}); err != nil {
		t.Fatal(err)
	} else if a, _ := h.probe(u, []string{"agents.mail.cc.acme.internal.hr-agent"}); len(a) != 1 {
		t.Error("billing-agent without its deny tag is still refused hr-agent")
	}

	// nats-server refuses a user of a zone whose key has deny templates when it carries no deny tag: that is why every
	// such user carries admin.NoDenyTag. A user with no allow tag (billing-agent above) connects fine.
	if _, err := h.user("billing-agent", nil); err == nil {
		t.Error("a user without a deny tag connected on a zone key with {{tag(deny)}} templates")
	} else {
		t.Logf("tagless user of a deny zone: %v", err)
	}
}

// TestLiveFirstDenyExceptionLeavesZoneKeys: a policy with no deny exception already puts both tag templates on every
// zone key and the deny:_none placeholder on every user, so the users connect and behave as compiled. Adding a deny
// exception for one endpoint then leaves every zone key template identical (the same account claims serve both
// compiles) and changes the tags of only the endpoint it names. A user with no deny tag at all is refused.
func TestLiveFirstDenyExceptionLeavesZoneKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a nats-server")
	}
	const head = `
default: deny
endpoints:
  a1: {labels: {zone: red, team: x}}
  a2: {labels: {zone: red}}
  a3: {labels: {zone: red}}
  b1: {labels: {zone: blue}}
  b2: {labels: {zone: blue, tier: secret}}
rules:
  - {name: r, action: allow, from: {zone: red}, to: {zone: blue}}
  - {name: red-red, action: allow, from: {zone: red}, to: {zone: red}}
  - {name: blue-red, action: allow, from: {zone: blue}, to: {zone: red}}
`
	c1, err := Compile(mustParse(t, head), "o")
	if err != nil {
		t.Fatal(err)
	}
	c2, err := Compile(mustParse(t, head+"  - {name: d, action: deny, from: {team: x}, to: {tier: secret}}\n"), "o")
	if err != nil {
		t.Fatal(err)
	}
	// Before: both templates on every key, only the placeholder on every user.
	for _, z := range c1.Zones {
		if !slices.Contains(z.Perms.Pub.Allow, "agents.mail.*.o.{{tag(allow)}}") || !slices.Equal(z.Perms.Pub.Deny, []string{"agents.mail.*.o.{{tag(deny)}}"}) {
			t.Errorf("zone %s lacks a template: allow %v deny %v", z.Name, z.Perms.Pub.Allow, z.Perms.Pub.Deny)
		}
	}
	for _, e := range c1.Endpoints {
		if !slices.Equal(e.Tags, []string{admin.NoDenyTag}) {
			t.Errorf("%s before the deny exception: tags %v", e.Name, e.Tags)
		}
	}
	// After: identical keys, one endpoint's tags changed.
	for i := range c1.Zones {
		if !reflect.DeepEqual(c1.Zones[i].Perms, c2.Zones[i].Perms) {
			t.Errorf("zone %s: key template changed with the first deny exception:\n%+v\n%+v", c1.Zones[i].Name, c1.Zones[i].Perms, c2.Zones[i].Perms)
		}
	}
	for i, e := range c1.Endpoints {
		changed := !slices.Equal(e.Tags, c2.Endpoints[i].Tags)
		if changed != (e.Name == "a1") {
			t.Errorf("%s: tags %v -> %v (only a1 may change)", e.Name, e.Tags, c2.Endpoints[i].Tags)
		}
	}
	if got := c2.Endpoints[0].Tags; !slices.Equal(got, []string{admin.NoDenyTag, "deny:blue.b2"}) {
		t.Errorf("a1 tags after: %v", got)
	}

	h := startLive(t, c1)
	h.liveMatrix(t)
	// The same account claims (same zone keys) serve the second compile: new users only, one JWT different.
	h.c = c2
	h.admin.mu.Lock()
	h.admin.seen = map[string]bool{} // forget what the first compile's users delivered
	h.admin.mu.Unlock()
	conns := h.liveMatrix(t)
	// deny:_none and the real deny tag both expand: a1 is refused b2 and keeps b1.
	if _, d := h.probe(conns["a1"], []string{"agents.mail.cc.o.blue.b1", "agents.mail.cc.o.blue.b2"}); len(d) != 1 || d[0] != "agents.mail.cc.o.blue.b2" {
		t.Errorf("a1 refused %v, want only b2", d)
	}
	if eff, err := c2.Effective("a1"); err != nil || !slices.Contains(eff.Pub.Deny, "agents.mail.*.o._none") || !slices.Contains(eff.Pub.Deny, "agents.mail.*.o.blue.b2") {
		t.Errorf("Effective deny lines %v %v", eff.Pub.Deny, err)
	}
	// nats-server refuses a user with no deny tag at all under these keys: why every user carries the placeholder.
	if _, err := h.user("a2", nil); err == nil {
		t.Error("a user without a deny tag connected on a zone key with the {{tag(deny)}} template")
	}
}

// TestLiveZoneTagsAndWildcards enforces a file with every encoding in zones of several endpoints: a wildcard zone,
// allow tags by name and for a whole zone (allow:<zone>.*), deny tags by name and for a whole zone, and zones that
// share nothing.
func TestLiveZoneTagsAndWildcards(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a nats-server")
	}
	f := mustParse(t, `
default: deny
endpoints:
  r1: {labels: {zone: red, team: x}}
  r2: {labels: {zone: red, team: y}}
  r3: {labels: {zone: red, team: y}}
  b1: {labels: {zone: blue, tier: low}}
  b2: {labels: {zone: blue, tier: secret}}
  b3: {labels: {zone: blue, tier: low}}
  g1: {labels: {zone: green, who: boss}}
  g2: {labels: {zone: green}}
  n1: {}
rules:
  - {name: red-red, action: allow, from: {zone: red}, to: {zone: red}}
  - {name: red-blue, action: allow, from: {zone: red}, to: {zone: blue}}
  - {name: x-not-secret, action: deny, from: {team: x}, to: {tier: secret}}
  - {name: x-to-green, action: allow, from: {team: x}, to: {zone: green}}
  - {name: y-to-g1, action: allow, from: {team: y}, to: {zone: green}}
  - {name: boss-to-red, action: allow, from: {who: boss}, to: {zone: red}}
  - {name: blue-to-n, action: allow, from: {zone: blue}, to: {zone: default}}
  - {name: green-to-r1, action: allow, from: {zone: green}, to: {team: x}}
`)
	c, err := Compile(f, "o")
	if err != nil {
		t.Fatal(err)
	}
	var star, deny, allowName bool
	for _, e := range c.Endpoints {
		for _, tag := range e.Tags {
			star = star || strings.HasPrefix(tag, "allow:") && strings.HasSuffix(tag, ".*")
			deny = deny || strings.HasPrefix(tag, "deny:") && tag != admin.NoDenyTag
			allowName = allowName || strings.HasPrefix(tag, "allow:") && !strings.HasSuffix(tag, ".*")
		}
	}
	if !star || !deny || !allowName {
		t.Fatalf("file lacks an encoding: star %v deny %v allow-by-name %v\n%v", star, deny, allowName, c.Endpoints)
	}
	newLiveServer := startLive(t, c)
	newLiveServer.liveMatrix(t)
}
