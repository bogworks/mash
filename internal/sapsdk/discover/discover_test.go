package discover

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/sapsdk"
)

type peer struct {
	addr sap.Address
	id   sapsdk.Identity
	seed []byte
}

func mkPeer(t *testing.T, account, owner, group, instance string) peer {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := kp.Seed()
	id, err := sapsdk.NewIdentity(seed, account)
	if err != nil {
		t.Fatal(err)
	}
	a, err := sap.NewAddress(owner, group, instance)
	if err != nil {
		t.Fatal(err)
	}
	return peer{addr: a, id: id, seed: seed}
}

func (p peer) subject(t *testing.T) string {
	t.Helper()
	s, err := p.addr.Subjects()
	if err != nil {
		t.Fatal(err)
	}
	return s.Prompt
}

// registration is the answer p's service gives: signed by signer (p when nil), with edit applied to the metadata and
// the prompt subject before signing.
func registration(t *testing.T, p peer, signer *peer, edit func(md map[string]string, subject *string)) []byte {
	t.Helper()
	s := signer
	if s == nil {
		s = &p
	}
	subject := p.subject(t)
	md := map[string]string{"agent": sap.AgentName, "owner": p.addr.Owner, "user_nkey": s.id.User, "account": s.id.Account}
	if edit != nil {
		edit(md, &subject)
	}
	sig, err := s.id.IDSig(md["agent"], md["owner"], subject)
	if err != nil {
		t.Fatal(err)
	}
	md["id_sig"] = sig
	b, err := json.Marshal(map[string]any{
		"metadata":  md,
		"endpoints": []map[string]string{{"name": "prompt", "subject": subject}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type world struct {
	acct               string
	alice, bob, carol  peer
	dir                Directory
	forgeries          map[string][]byte
	genuineBob, aliceR []byte
}

func mkWorld(t *testing.T) world {
	t.Helper()
	ak, _ := nkeys.CreateAccount()
	acct, _ := ak.PublicKey()
	w := world{acct: acct}
	w.alice = mkPeer(t, acct, "acme", "ops", "alice")
	w.bob = mkPeer(t, acct, "acme", "ops", "bob")
	w.carol = mkPeer(t, acct, "acme", "ops", "carol")
	w.dir = Directory{}
	for _, p := range []peer{w.alice, w.bob, w.carol} {
		w.dir[p.addr.Flat()] = Record{Address: p.addr, User: p.id.User, Account: acct}
	}
	w.genuineBob = registration(t, w.bob, nil, nil)
	w.aliceR = registration(t, w.alice, nil, nil)
	ok2, _ := nkeys.CreateAccount()
	other, _ := ok2.PublicKey()
	bobOther, err := sapsdk.NewIdentity(w.bob.seed, other)
	if err != nil {
		t.Fatal(err)
	}
	w.forgeries = map[string][]byte{
		"unsigned": mutateSig(t, w.genuineBob, func(md map[string]string) { delete(md, "id_sig") }),
		"forged":   mutateSig(t, w.genuineBob, func(md map[string]string) { md["id_sig"] = "AAAA" }),
		"wrongkey": registration(t, w.bob, &w.carol, nil),
		"subject":  registration(t, w.bob, &w.bob, func(_ map[string]string, s *string) { *s = w.carol.subject(t) }),
		"owner":    registration(t, w.bob, nil, func(md map[string]string, _ *string) { md["owner"] = "zeta" }),
		"account":  registration(t, w.bob, &peer{addr: w.bob.addr, id: bobOther}, nil),
	}
	return w
}

func mutateSig(t *testing.T, data []byte, f func(md map[string]string)) []byte {
	t.Helper()
	var info map[string]any
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatal(err)
	}
	md := map[string]string{}
	for k, v := range info["metadata"].(map[string]any) {
		md[k], _ = v.(string)
	}
	f(md)
	info["metadata"] = md
	b, _ := json.Marshal(info)
	return b
}

func TestJudgeVerdicts(t *testing.T) {
	w := mkWorld(t)
	if v, regs := Judge(w.dir, w.genuineBob); v != Verified || len(regs) != 1 || regs[0].Address != w.bob.addr || regs[0].UserNKey != w.bob.id.User {
		t.Fatalf("genuine: %v %+v", v, regs)
	}
	for name, data := range w.forgeries {
		if v, regs := Judge(w.dir, data); v != NotVerified || regs != nil {
			t.Errorf("%s: verdict %v %+v, want NotVerified", name, v, regs)
		}
	}
	for name, data := range map[string][]byte{"empty": nil, "not json": []byte("pong"), "no endpoints": []byte(`{"metadata":{}}`),
		"not a SAP subject": []byte(`{"metadata":{},"endpoints":[{"name":"prompt","subject":"x.y"}]}`)} {
		if v, _ := Judge(w.dir, data); v != NotARegistration {
			t.Errorf("%s: verdict %v, want NotARegistration", name, v)
		}
	}
	stranger := mkPeer(t, w.acct, "acme", "ops", "dave") // validly signed, not in the directory
	if v, _ := Judge(w.dir, registration(t, stranger, nil, nil)); v != NotVerified {
		t.Errorf("an address the directory does not hold: %v, want NotVerified", v)
	}
}

func TestCollectorCountsEachInvalidAnswerOnce(t *testing.T) {
	w := mkWorld(t)
	var invalid [][]byte
	for _, d := range w.forgeries {
		invalid = append(invalid, d)
	}
	n := len(invalid)
	cases := map[string]func(c *Collector){
		"invalid-only": func(c *Collector) {
			for _, d := range invalid {
				c.Add(d)
			}
		},
		"invalid-before-valid": func(c *Collector) {
			for _, d := range invalid {
				c.Add(d)
			}
			c.Add(w.genuineBob)
		},
		"invalid-after-valid": func(c *Collector) {
			c.Add(w.genuineBob)
			for _, d := range invalid {
				c.Add(d)
			}
		},
		"between": func(c *Collector) {
			for i, d := range invalid {
				c.Add(d)
				if i == n/2 {
					c.Add(w.genuineBob)
				}
			}
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			c := NewCollector(w.dir, w.bob.addr)
			run(c)
			if c.NotVerified != n {
				t.Fatalf("counted %d, want %d", c.NotVerified, n)
			}
			want := 1
			if name == "invalid-only" {
				want = 0
			}
			if len(c.Accepted) != want {
				t.Fatalf("accepted %+v, want %d", c.Accepted, want)
			}
		})
	}
}

func TestCollectorDoesNotCountOrAcceptOtherValidAddresses(t *testing.T) {
	w := mkWorld(t)
	c := NewCollector(w.dir, w.bob.addr)
	c.Add(w.aliceR)          // alice's own valid registration: not bob's, not counted
	c.Add([]byte("garbage")) // not a registration: not counted
	c.Add(w.genuineBob)      //
	c.Add(w.genuineBob)      // the same registration twice is one
	if c.NotVerified != 0 || len(c.Accepted) != 1 {
		t.Fatalf("counted %d, accepted %+v", c.NotVerified, c.Accepted)
	}
}

func TestWarningWording(t *testing.T) {
	for n, want := range map[int]string{
		0: "",
		1: "warning: 1 discovery answer could not be verified",
		2: "warning: 2 discovery answers could not be verified",
		7: "warning: 7 discovery answers could not be verified",
	} {
		if got := Warning(n); got != want {
			t.Errorf("Warning(%d) = %q, want %q", n, got, want)
		}
	}
	if AnswersNotVerifiedMember != "answers_not_verified" {
		t.Errorf("JSON member %q", AnswersNotVerifiedMember)
	}
}

// An oversized prompt is refused before any use of the connection (the Caller here has none to publish on).
func TestPromptOverTheLimitIsRefusedBeforeAnythingIsPublished(t *testing.T) {
	c := &Caller{}
	_, err := c.Prompt(context.Background(), sap.Address{}, strings.Repeat("x", MaxPromptBytes+1))
	if err == nil || !strings.Contains(err.Error(), PromptTooLargeCode) || !strings.Contains(err.Error(), "prompt_too_large") {
		t.Fatalf("got %v", err)
	}
}

func TestRandTokensAreBase32OfTheRightWidth(t *testing.T) {
	a, b := randToken(routeLen), randToken(routeLen)
	if len(a) != routeLen || a == b || strings.Trim(a, "abcdefghijklmnopqrstuvwxyz234567") != "" {
		t.Fatalf("tokens %q %q", a, b)
	}
}
