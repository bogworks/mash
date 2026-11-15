package policyserve

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
)

// storeKeys is a Keys with an operator key and any number of owner group keys.
type storeKeys struct {
	op     nkeys.KeyPair
	groups map[string]nkeys.KeyPair
}

func (s storeKeys) Operator() (nkeys.KeyPair, string, error) {
	kp, err := nkeys.FromSeed(mustSeed(s.op))
	pub, _ := kp.PublicKey()
	return kp, pub, err
}

func (s storeKeys) GroupKey(g string, _ bool) (nkeys.KeyPair, error) {
	kp, ok := s.groups[g]
	if !ok {
		return nil, errors.New("no key")
	}
	return nkeys.FromSeed(mustSeed(kp))
}

func (s storeKeys) GroupPublics() (map[string]string, error) {
	out := map[string]string{}
	for g, kp := range s.groups {
		out[g], _ = kp.PublicKey()
	}
	return out, nil
}

// oneJWT is a Resolver whose one server holds tok.
type oneJWT struct{ tok string }

func (r oneJWT) Lookup(context.Context, string) (string, error) { return r.tok, nil }
func (r oneJWT) LookupAll(context.Context, string, int) ([]string, error) {
	return []string{r.tok}, nil
}
func (oneJWT) Servers(context.Context) (apply.ServerSet, error) {
	return apply.ServerSet{Names: []string{"s1"}, Expected: 1}, nil
}
func (oneJWT) Push(context.Context, string, []string) ([]apply.ServerAck, error) { return nil, nil }

func newGroupKey() nkeys.KeyPair { kp, _ := nkeys.CreateAccount(); return kp }

func scopeOf(t *testing.T, ac *jwt.AccountClaims, pub string) *jwt.UserScope {
	t.Helper()
	sc, ok := ac.SigningKeys.GetScope(pub)
	if !ok {
		t.Fatalf("signing key %.8s is not in the account JWT", pub)
	}
	us, ok := sc.(*jwt.UserScope)
	if !ok {
		t.Fatalf("signing key %.8s is not scoped", pub)
	}
	return us
}

func pubOf(kp nkeys.KeyPair) string { p, _ := kp.PublicKey(); return p }

func TestSignKeepsOtherRangesGroupKeys(t *testing.T) {
	op, _ := nkeys.CreateOperator()
	acct, _ := nkeys.CreateAccount()
	apub := pubOf(acct)
	keys := storeKeys{op: op, groups: map[string]nkeys.KeyPair{
		"foo.c": newGroupKey(), "foo.gone": newGroupKey(), "bar.d": newGroupKey(),
	}}
	ac := jwt.NewAccountClaims(apub)
	for _, g := range []string{"foo.c", "foo.gone", "bar.d"} {
		sc := jwt.NewUserScope()
		sc.Key, sc.Role = pubOf(keys.groups[g]), g
		sc.Template.Pub.Allow.Add("keep." + g)
		ac.SigningKeys.AddScopedSigner(sc)
	}
	tok, err := ac.Encode(op)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := apply.NewTrust(pubOf(op), "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := listset.Compile(listset.ListSet{
		Range: "foo",
		Switches: []listset.SwitchEntry{{Group: "foo.c", Verb: listset.Mail, Switch: listset.Closed}, {Group: "foo.c", Verb: listset.Prompt, Switch: listset.Closed},
			{Group: "foo.c", Verb: listset.Status, Switch: listset.Closed}, {Group: "foo.c", Verb: listset.Heartbeat, Switch: listset.Closed}},
		Senders: []listset.SenderLists{{Sender: "foo.c.x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(5 * time.Second)
	iss := &Issuer{Ctx: context.Background(), Keys: keys, Resolver: oneJWT{tok}, Account: apub, Trust: trust, Servers: 1, Range: "foo",
		Now: func() time.Time { return now }}
	out, err := iss.Sign(listset.Record{}, c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := jwt.DecodeAccountClaims(out)
	if err != nil {
		t.Fatal(err)
	}
	// The other range's signer stays, with its own scope.
	us := scopeOf(t, got, pubOf(keys.groups["bar.d"]))
	if us.Role != "bar.d" || !slices.Contains([]string(us.Template.Pub.Allow), "keep.bar.d") {
		t.Errorf("the other range's scope changed: %+v", us)
	}
	// This range's group of the list set is there; a this-range group no longer in the list set is gone.
	scopeOf(t, got, pubOf(keys.groups["foo.c"]))
	if _, ok := got.SigningKeys.GetScope(pubOf(keys.groups["foo.gone"])); ok {
		t.Error("a group of this range that left the list set still has a valid signer")
	}
}

func TestSignPreviousRefusesAnotherAccountsJWT(t *testing.T) {
	op, _ := nkeys.CreateOperator()
	mine, _ := nkeys.CreateAccount()
	other, _ := nkeys.CreateAccount()
	stranger, _ := nkeys.CreateOperator()
	now := time.Now()
	iss := &Issuer{Ctx: context.Background(), Keys: memKeys{op}, Account: pubOf(mine), Now: func() time.Time { return now.Add(2 * time.Second) }}

	otherTok, _ := jwt.NewAccountClaims(pubOf(other)).Encode(op)
	if _, err := iss.SignPrevious(policyapply.PreviousClaims{JWT: otherTok}, nil); err == nil {
		t.Error("the JWT of another account was re-signed")
	}
	foreignTok, _ := jwt.NewAccountClaims(pubOf(mine)).Encode(stranger)
	if _, err := iss.SignPrevious(policyapply.PreviousClaims{JWT: foreignTok}, nil); err == nil {
		t.Error("a JWT issued by a key that is not the operator was re-signed")
	}
}

func TestSignPreviousStampsThePlannedRevocationTime(t *testing.T) {
	op, _ := nkeys.CreateOperator()
	acct, _ := nkeys.CreateAccount()
	apub, _ := acct.PublicKey()
	prevTok, err := jwt.NewAccountClaims(apub).Encode(op)
	if err != nil {
		t.Fatal(err)
	}
	u1, _ := nkeys.CreateUser()
	u2, _ := nkeys.CreateUser()
	k1, _ := u1.PublicKey()
	k2, _ := u2.PublicKey()
	future := time.Now().Add(time.Hour).Truncate(time.Second)
	iss := &Issuer{Ctx: context.Background(), Keys: memKeys{op}, Account: apub, Now: func() time.Time { return time.Now().Add(2 * time.Second) }}
	before := time.Now().Unix()
	tok, err := iss.SignPrevious(policyapply.PreviousClaims{JWT: prevTok, RevokeAt: map[string]time.Time{k1: future}}, []string{k1, k2})
	if err != nil {
		t.Fatal(err)
	}
	c, err := jwt.DecodeAccountClaims(tok)
	if err != nil {
		t.Fatal(err)
	}
	if c.Revocations[k1] != future.Unix() {
		t.Errorf("planned key stamped %d, want %d", c.Revocations[k1], future.Unix())
	}
	if got := c.Revocations[k2]; got < before || got > before+5 {
		t.Errorf("unplanned key stamped %d, want about now (%d)", got, before)
	}
}
