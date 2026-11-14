package policyapply

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/listset"
)

func switches(group string, mail, prompt, status, hb listset.Switch) []listset.SwitchEntry {
	return []listset.SwitchEntry{
		{Group: group, Verb: listset.Mail, Switch: mail},
		{Group: group, Verb: listset.Prompt, Switch: prompt},
		{Group: group, Verb: listset.Status, Switch: status},
		{Group: group, Verb: listset.Heartbeat, Switch: hb},
	}
}

// set1 is the first generation: mail is shared in the group and core also has docs on its allow list.
func set1() listset.ListSet {
	return listset.ListSet{
		Range:    "foo",
		Switches: switches("foo.c", listset.Shared, listset.Closed, listset.Closed, listset.Closed),
		Senders: []listset.SenderLists{
			{Sender: "foo.c.core", Allow: []string{"foo.c.docs"}},
			{Sender: "foo.c.docs"},
			{Sender: "foo.c.sdk"},
		},
	}
}

// set2 closes mail (a template change, so a probe with the old tags is refused) and takes core's allow of docs away
// (a tag change, so only the old credential's revocation refuses it).
func set2() listset.ListSet {
	ls := set1()
	ls.Base = 1
	ls.Switches = switches("foo.c", listset.Closed, listset.Closed, listset.Closed, listset.Closed)
	ls.Senders = []listset.SenderLists{{Sender: "foo.c.core"}, {Sender: "foo.c.docs"}, {Sender: "foo.c.sdk"}}
	return ls
}

type keys struct {
	acct    nkeys.KeyPair
	acctPub string
	signers map[string]string // group -> scoped key public
	revoked string            // a user public key
}

func newKeys(t *testing.T) keys {
	t.Helper()
	kp, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := kp.PublicKey()
	u, err := nkeys.FromRawSeed(nkeys.PrefixByteUser, make([]byte, 32)) // fixed, so the golden file is stable
	if err != nil {
		t.Fatal(err)
	}
	up, _ := u.PublicKey()
	k := keys{acct: kp, acctPub: pub, signers: map[string]string{}, revoked: up}
	sk, _ := nkeys.CreateAccount()
	k.signers["foo.c"], _ = sk.PublicKey()
	return k
}

// build makes an account JWT carrying c's templates as scoped keys (role = group) and the revoked keys.
func (k keys) build(t *testing.T, c *listset.Compiled, revoked ...string) string {
	t.Helper()
	return k.buildWith(t, c, k.signers, nil, revoked...)
}

// buildWith is build with the given scoped signer keys per group, and extra plain signing keys.
func (k keys) buildWith(t *testing.T, c *listset.Compiled, signers map[string]string, extra []string, revoked ...string) string {
	t.Helper()
	claims := jwt.NewAccountClaims(k.acctPub)
	for _, e := range extra {
		claims.SigningKeys.Add(e)
	}
	for _, tpl := range c.Templates {
		sc := jwt.NewUserScope()
		sc.Key = signers[tpl.Group]
		sc.Role = tpl.Group
		sc.Template.Permissions = tpl.Permissions
		claims.SigningKeys.AddScopedSigner(sc)
	}
	for _, r := range revoked {
		claims.Revoke(r)
	}
	tok, err := claims.Encode(k.acct)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func compile(t *testing.T, ls listset.ListSet) *listset.Compiled {
	t.Helper()
	c, err := listset.Compile(ls)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

type fakeCluster struct {
	mu       sync.Mutex
	names    []string
	expected int
	jwts     map[string]string
	dropN    map[string]int  // pushes to drop silently, per server
	failPush map[string]bool // servers that answer 500
	badAck   map[string]bool // servers that take the JWT but answer 500
	pushes   []string
}

func newCluster(tok string, names ...string) *fakeCluster {
	f := &fakeCluster{names: names, expected: len(names), jwts: map[string]string{}, dropN: map[string]int{}, failPush: map[string]bool{}, badAck: map[string]bool{}}
	for _, n := range names {
		f.jwts[n] = tok
	}
	return f
}

func (f *fakeCluster) Servers(context.Context) (apply.ServerSet, error) {
	return apply.ServerSet{Names: slices.Clone(f.names), Expected: f.expected}, nil
}

func (f *fakeCluster) AccountJWTs(context.Context, string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]string{}
	for k, v := range f.jwts {
		out[k] = v
	}
	return out, nil
}

func (f *fakeCluster) Push(_ context.Context, tok string, _ []string) ([]apply.ServerAck, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushes = append(f.pushes, tok)
	var acks []apply.ServerAck
	for _, n := range f.names {
		switch {
		case f.dropN[n] > 0:
			f.dropN[n]--
		case f.failPush[n]:
			acks = append(acks, apply.ServerAck{Server: n, Code: 500, Message: "refused"})
		case f.badAck[n]:
			f.jwts[n] = tok
			acks = append(acks, apply.ServerAck{Server: n, Code: 500, Message: "late"})
		default:
			f.jwts[n] = tok
			acks = append(acks, apply.ServerAck{Server: n, Code: 200})
		}
	}
	return acks, nil
}

func (f *fakeCluster) set(server, tok string) {
	f.mu.Lock()
	f.jwts[server] = tok
	f.mu.Unlock()
}

// fakeProber refuses a probe unless the server's held generation still reaches the receiver with the probe's tags.
type fakeProber struct {
	cluster *fakeCluster
	byGen   map[uint64]*listset.Compiled
	allow   map[string]bool // servers that wrongly accept everything
}

func (p fakeProber) Refused(_ context.Context, server string, probe ProbeCred, _ Op, subject string) (bool, error) {
	if p.allow[server] {
		return false, nil
	}
	var cands []Candidate
	for g, c := range p.byGen {
		cands = append(cands, Candidate{Generation: g, Compiled: c})
	}
	slices.SortFunc(cands, func(a, b Candidate) int { return int(b.Generation) - int(a.Generation) })
	jwts, _ := p.cluster.AccountJWTs(context.Background(), "")
	g, ok := GenerationOf(jwts[server], cands)
	if !ok {
		return false, errors.New("unknown generation")
	}
	c := &listset.Compiled{Range: p.byGen[g].Range, Templates: p.byGen[g].Templates}
	for _, u := range p.byGen[g].Users {
		if u.Sender == probe.Sender {
			u.Name, u.Tags = probe.Name, probe.Tags
		}
		c.Users = append(c.Users, u)
	}
	var universe []string
	for _, u := range c.Users {
		universe = append(universe, u.Sender)
	}
	reach, err := listset.ReachOf(c, probe.Sender, universe)
	if err != nil {
		return false, err
	}
	for _, v := range listset.Verbs {
		if recv, found := strings.CutPrefix(subject, listset.Subject(v, "")); found {
			return !slices.Contains(reach[v], recv), nil
		}
	}
	return false, errors.New("bad subject")
}

type fakeWitnesses struct {
	open map[WitnessKey]bool // witnesses still open; every other is closed
	all  []WitnessKey
}

func (w fakeWitnesses) Closed(context.Context) (map[WitnessKey]bool, error) {
	out := map[WitnessKey]bool{}
	for _, k := range w.all {
		out[k] = !w.open[k]
	}
	return out, nil
}

func witnessesFor(servers []string, senders ...string) fakeWitnesses {
	w := fakeWitnesses{open: map[WitnessKey]bool{}}
	for _, s := range servers {
		for _, d := range senders {
			w.all = append(w.all, WitnessKey{s, d})
		}
	}
	return w
}

func fixedNow() time.Time { return time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC) }
