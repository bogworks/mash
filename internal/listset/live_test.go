package listset

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

// The live cross-check (REQ-020's pattern, after core's verification tests): an in-process nats-server in operator
// mode, one account whose scoped signing keys carry the compiled templates, users minted with only tags. For every
// sender and every address of the list set the server's actual outcome must equal Check's simulation, and equal what
// the lists give.

type liveEnv struct {
	t      *testing.T
	url    string
	acc    nkeys.KeyPair
	accPub string
	scoped map[string]nkeys.KeyPair // owner group -> signing key
}

func newLiveEnv(t *testing.T, c *Compiled) *liveEnv {
	t.Helper()
	op, _ := nkeys.CreateOperator()
	opPub, _ := op.PublicKey()
	acc, _ := nkeys.CreateAccount()
	accPub, _ := acc.PublicKey()
	ac := jwt.NewAccountClaims(accPub)
	ac.Name = c.Range
	e := &liveEnv{t: t, acc: acc, accPub: accPub, scoped: map[string]nkeys.KeyPair{}}
	for _, tpl := range c.Templates {
		sk, _ := nkeys.CreateAccount()
		pub, _ := sk.PublicKey()
		us := jwt.NewUserScope()
		us.Key = pub
		us.Template.Permissions = tpl.Permissions
		ac.SigningKeys.AddScopedSigner(us)
		e.scoped[tpl.Group] = sk
	}
	ajwt, err := ac.Encode(op)
	if err != nil {
		t.Fatal(err)
	}
	// MaxControlLine is raised so the CONNECT line can carry a user JWT with over a thousand tags.
	o := &server.Options{Port: -1, NoLog: true, NoSigs: true, TrustedKeys: []string{opPub}, MaxControlLine: 1 << 20}
	mr := &server.MemAccResolver{}
	if err := mr.Store(accPub, ajwt); err != nil {
		t.Fatal(err)
	}
	o.AccountResolver = mr
	s, err := server.NewServer(o)
	if err != nil {
		t.Fatal(err)
	}
	go s.Start()
	if !s.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats-server not ready")
	}
	t.Cleanup(s.Shutdown)
	e.url = s.ClientURL()
	return e
}

// conn is a connected user with the permission violations the server reported.
type conn struct {
	nc   *nats.Conn
	errs chan string
}

func (e *liveEnv) connect(signer nkeys.KeyPair, name string, tags []string, scoped bool) (*conn, error) {
	u, _ := nkeys.CreateUser()
	pub, _ := u.PublicKey()
	uc := jwt.NewUserClaims(pub)
	uc.Name = name
	uc.Tags.Add(tags...)
	if scoped {
		uc.IssuerAccount = e.accPub
		uc.UserPermissionLimits = jwt.UserPermissionLimits{}
	}
	j, err := uc.Encode(signer)
	if err != nil {
		return nil, err
	}
	c := &conn{errs: make(chan string, 256)}
	c.nc, err = nats.Connect(e.url,
		nats.UserJWT(func() (string, error) { return j, nil }, func(n []byte) ([]byte, error) { return u.Sign(n) }),
		nats.NoReconnect(),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			select {
			case c.errs <- err.Error():
			default:
			}
		}))
	return c, err
}

// violation waits for a permissions violation of kind ("Publish" or "Subscription") that mentions subject; false on
// timeout.
func (c *conn) violation(kind, subject string, d time.Duration) bool {
	timeout := time.After(d)
	for {
		select {
		case m := <-c.errs:
			if strings.Contains(m, "Permissions Violation for "+kind) && strings.Contains(m, `"`+subject+`"`) {
				return true
			}
		case <-timeout:
			return false
		}
	}
}

// canPublish publishes subject as c and reports whether the observer received it. A refusal must be confirmed by the
// server's own violation error, so a lost message is never mistaken for a denial.
func canPublish(t *testing.T, c *conn, obs *nats.Subscription, subject string) bool {
	t.Helper()
	if err := c.nc.Publish(subject, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := c.nc.Flush(); err != nil {
		t.Fatal(err)
	}
	for {
		m, err := obs.NextMsg(100 * time.Millisecond)
		if err != nil {
			if !c.violation("Publish", subject, 2*time.Second) {
				t.Errorf("publish to %s: neither delivered nor refused", subject)
			}
			return false
		}
		if m.Subject == subject {
			return true
		}
	}
}

// canSubscribe subscribes to subject as c, then has the observer publish to it: delivered means allowed; otherwise
// the server's violation error must confirm the refusal.
func canSubscribe(t *testing.T, c *conn, obsConn *nats.Conn, subject string) bool {
	t.Helper()
	sub, err := c.nc.SubscribeSync(subject)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Unsubscribe() }()
	if err := c.nc.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := obsConn.Publish(subject, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := obsConn.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := sub.NextMsg(100 * time.Millisecond); err == nil {
		return true
	}
	if !c.violation("Subscription", subject, 2*time.Second) {
		t.Errorf("subscribe to %s: neither delivered nor refused", subject)
	}
	return false
}

func liveCases() map[string]ListSet {
	return map[string]ListSet{
		"AT-001 shape":         at001(),
		"deny key (AT-003)":    at003Deny(),
		"cross-group, one way": corpus()[3].ls,
	}
}

func TestLiveMatchesCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("live nats-server cross-check")
	}
	for name, ls := range liveCases() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c, err := Compile(ls)
			if err != nil {
				t.Fatal(err)
			}
			env := newLiveEnv(t, c)
			m, _ := validate(ls)
			universe := universeNamed(m)
			universe = append(universe, ls.Range+".probe-owner.probe-name", ls.Range+"."+m.senders[0].addr.owner+".probe-name")

			admin, err := env.connect(env.acc, "admin", nil, false)
			if err != nil {
				t.Fatal(err)
			}
			defer admin.nc.Close()
			obs, err := admin.nc.SubscribeSync(">")
			if err != nil {
				t.Fatal(err)
			}
			if err := admin.nc.Flush(); err != nil {
				t.Fatal(err)
			}

			for _, u := range c.Users {
				a, _ := parseAddress(u.Sender)
				// The user JWT carries only tags: the connection below is signed with no permissions of its own.
				sc, err := env.connect(env.scoped[a.group()], u.Name, u.Tags, true)
				if err != nil {
					t.Fatalf("%s cannot connect with tags %v: %v", u.Sender, u.Tags, err)
				}
				want, err := effective(ls, c, u.Sender)
				if err != nil {
					t.Fatal(err)
				}
				for _, addr := range universe {
					for _, v := range []Verb{Mail, Prompt, Status} {
						got := canPublish(t, sc, obs, "agents."+v.subject()+"."+addr)
						if w := slices.Contains(want[v], addr); got != w && slices.Contains(universeNamed(m), addr) {
							t.Errorf("%s publish %s to %s: server %v, Check simulates %v", u.Sender, v, addr, got, w)
						} else if !slices.Contains(universeNamed(m), addr) && got {
							// a probe is outside every list: only a shared owner line may reach it
							if !m.shared(a.group(), v) || !strings.HasPrefix(addr, a.group()+".") {
								t.Errorf("%s publish %s reaches probe %s", u.Sender, v, addr)
							}
						}
					}
					got := canSubscribe(t, sc, admin.nc, "agents.hb."+addr)
					if w := slices.Contains(want[Heartbeat], addr); got != w && slices.Contains(universeNamed(m), addr) {
						t.Errorf("%s watch heartbeat of %s: server %v, Check simulates %v", u.Sender, addr, got, w)
					}
				}
				// The fixed lines, which need {{name()}}: own heartbeat out, own prompt and status in; no other's.
				self := u.Sender
				if !canPublish(t, sc, obs, "agents.hb."+self) {
					t.Errorf("%s cannot publish its own heartbeat", self)
				}
				for _, addr := range universe {
					if addr != self && canPublish(t, sc, obs, "agents.hb."+addr) {
						t.Errorf("%s published the heartbeat of %s", self, addr)
					}
					for _, v := range []Verb{Prompt, Status} {
						in := canSubscribe(t, sc, admin.nc, "agents."+v.subject()+"."+addr)
						if in != (addr == self) {
							t.Errorf("%s subscribe to %s of %s: %v", self, v, addr, in)
						}
					}
					if canSubscribe(t, sc, admin.nc, "agents.mail."+addr) {
						t.Errorf("%s subscribed to mail of %s", self, addr)
					}
				}
				// Its own inbox stream and consumer API subjects, and no other sender's.
				flat := strings.ReplaceAll(self, ".", "_")
				if !canPublish(t, sc, obs, "$JS.API.STREAM.INFO.inbox-"+flat) {
					t.Errorf("%s cannot reach its own inbox stream info", self)
				}
				for _, o := range m.senders {
					if o.addr.String() == self {
						continue
					}
					other := strings.ReplaceAll(o.addr.String(), ".", "_")
					if canPublish(t, sc, obs, "$JS.API.CONSUMER.MSG.NEXT.inbox-"+other+".deliver") {
						t.Errorf("%s fetches from the inbox of %s", self, o.addr)
					}
				}
				sc.nc.Close()
			}
		})
	}
}

// The server's template expansion limit (maxPermTemplateSubjectExpansions) at its boundary: for AT-001's key the
// publish allow list is 3N+1 subjects, so 1365 allow tags connect and 1366 are refused, as Check simulates.
func TestLiveExpansionLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("live nats-server cross-check")
	}
	ls := at001()
	c, err := Compile(ls)
	if err != nil {
		t.Fatal(err)
	}
	env := newLiveEnv(t, c)
	var tpl jwt.Permissions
	for _, x := range c.Templates {
		if x.Group == "foo.c" {
			tpl = x.Permissions
		}
	}
	for _, n := range []int{1365, 1366} {
		_, simErr := expandPermissions(tpl, "core", allowTags(n))
		sc, err := env.connect(env.scoped["foo.c"], "core", allowTags(n), true)
		if sc != nil && sc.nc != nil {
			defer sc.nc.Close()
		}
		if (simErr != nil) != (err != nil) {
			t.Errorf("%d tags: server connect error %v, Check simulates %v", n, err, simErr)
		}
		if n == 1365 && err != nil {
			t.Errorf("%d tags: server refused: %v", n, err)
		}
		if n == 1366 && err == nil {
			t.Errorf("%d tags: server accepted", n)
		}
	}
}
