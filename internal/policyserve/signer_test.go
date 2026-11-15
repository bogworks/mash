package policyserve

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
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

func TestSignReplacesTheOwnerGroupKeys(t *testing.T) {
	op, _ := nkeys.CreateOperator()
	acct, _ := nkeys.CreateAccount()
	apub := pubOf(acct)
	keys := storeKeys{op: op, groups: map[string]nkeys.KeyPair{
		"c": newGroupKey(), "gone": newGroupKey(), "d": newGroupKey(),
	}}
	ac := jwt.NewAccountClaims(apub)
	for _, g := range []string{"c", "gone", "d"} {
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
		Switches: []listset.SwitchEntry{{Group: "c", Verb: listset.Mail, Switch: listset.Closed}, {Group: "c", Verb: listset.Prompt, Switch: listset.Closed},
			{Group: "c", Verb: listset.Status, Switch: listset.Closed}, {Group: "c", Verb: listset.Heartbeat, Switch: listset.Closed}},
		Senders: []listset.SenderLists{{Sender: "c.x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(5 * time.Second)
	iss := &Issuer{Ctx: context.Background(), Keys: keys, Resolver: oneJWT{tok}, Account: apub, Trust: trust, Servers: 1,
		Now: func() time.Time { return now }}
	out, err := iss.Sign(listset.Record{}, c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := jwt.DecodeAccountClaims(out)
	if err != nil {
		t.Fatal(err)
	}
	// Every owner group key of the account JWT is rebuilt from the list set: the group of the list set is there with
	// the template compiled from it, and a group that left the list set (held in the key store or not) is gone.
	us := scopeOf(t, got, pubOf(keys.groups["c"]))
	if us.Role != "c" || slices.Contains([]string(us.Template.Pub.Allow), "keep.c") {
		t.Errorf("the group's scope was not rebuilt: %+v", us)
	}
	for _, g := range []string{"gone", "d"} {
		if _, ok := got.SigningKeys.GetScope(pubOf(keys.groups[g])); ok {
			t.Errorf("owner group %s left the list set and still has a valid signer", g)
		}
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

// dropWorld is an account JWT with a plain (non-mash) signing key and the held key of owner group d, which the list
// set does not name, and an Issuer over it.
type dropWorld struct {
	keys        storeKeys
	acct        string
	plain       string
	leaving     string
	iss         *Issuer
	compiled    *listset.Compiled
	targetGroup string
}

func newDropWorld(t *testing.T) *dropWorld {
	t.Helper()
	op, _ := nkeys.CreateOperator()
	acct, _ := nkeys.CreateAccount()
	w := &dropWorld{acct: pubOf(acct), plain: pubOf(newGroupKey()), targetGroup: "c"}
	w.keys = storeKeys{op: op, groups: map[string]nkeys.KeyPair{"c": newGroupKey(), "d": newGroupKey()}}
	w.leaving = pubOf(w.keys.groups["d"])
	ac := jwt.NewAccountClaims(w.acct)
	ac.SigningKeys.Add(w.plain)
	sc := jwt.NewUserScope()
	sc.Key, sc.Role = w.leaving, "d"
	sc.Template.Pub.Allow.Add("keep.d")
	ac.SigningKeys.AddScopedSigner(sc)
	tok, err := ac.Encode(op)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := apply.NewTrust(pubOf(op), "")
	if err != nil {
		t.Fatal(err)
	}
	w.compiled, err = listset.Compile(listset.ListSet{
		Switches: []listset.SwitchEntry{{Group: "c", Verb: listset.Mail, Switch: listset.Closed}, {Group: "c", Verb: listset.Prompt, Switch: listset.Closed},
			{Group: "c", Verb: listset.Status, Switch: listset.Closed}, {Group: "c", Verb: listset.Heartbeat, Switch: listset.Closed}},
		Senders: []listset.SenderLists{{Sender: "c.x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(5 * time.Second)
	w.iss = &Issuer{Ctx: context.Background(), Keys: w.keys, Resolver: oneJWT{tok}, Account: w.acct, Trust: trust, Servers: 1,
		Now: func() time.Time { return now }}
	return w
}

// enable sets the drop fields: the admin user signed by adminIssuer, and the connected exempt users' issuers.
func (w *dropWorld) enable(adminIssuer string, exempt map[string][]string) {
	w.iss.AdminUser, w.iss.AdminIssuer = "UADMIN", adminIssuer
	w.iss.ExemptIssuers = func(context.Context) (map[string][]string, error) { return exempt, nil }
}

func TestSignRefusesANonMashKeyWithoutTheDropFields(t *testing.T) {
	w := newDropWorld(t)
	for name, set := range map[string]func(){
		"none":             func() {},
		"no exempt source": func() { w.iss.AdminUser, w.iss.AdminIssuer = "UADMIN", w.acct },
		"no admin issuer": func() {
			w.iss.AdminIssuer = ""
			w.iss.ExemptIssuers = func(context.Context) (map[string][]string, error) { return nil, nil }
		},
	} {
		w.iss.AdminUser, w.iss.AdminIssuer, w.iss.ExemptIssuers = "", "", nil
		set()
		_, err := w.iss.Sign(listset.Record{Generation: 1}, w.compiled)
		if err == nil || !strings.Contains(err.Error(), "not an owner group key of the key store") || !strings.Contains(err.Error(), w.plain[:8]) {
			t.Errorf("%s: err %v", name, err)
		}
	}
}

func TestSignDropsANonMashKeyAfterTheIssuerCheck(t *testing.T) {
	w := newDropWorld(t)
	other := newGroupKey()
	stay := pubOf(w.keys.groups["c"])                                                        // the key of the list set's group
	w.enable(w.acct, map[string][]string{"UEXEMPT": {w.acct, stay}, "USYS": {pubOf(other)}}) // USYS is under a key that is not in the JWT
	if _, err := w.iss.Sign(listset.Record{Generation: 1}, w.compiled); err == nil {
		t.Fatal("an exempt user under an unknown key passed the check")
	}
	// A user under the key of a group the list set no longer names does not stay either: that key is dropped.
	w.enable(w.acct, map[string][]string{"UEXEMPT": {w.acct, w.leaving}})
	var nk *admin.IssuersNotKeptError
	if _, err := w.iss.Sign(listset.Record{Generation: 1}, w.compiled); !errors.As(err, &nk) {
		t.Fatalf("an exempt user under a dropped group key passed the check: %v", err)
	}
	w.enable(w.acct, map[string][]string{"UEXEMPT": {w.acct, stay}})
	out, err := w.iss.Sign(listset.Record{Generation: 1}, w.compiled)
	if err != nil {
		t.Fatal(err)
	}
	got, err := jwt.DecodeAccountClaims(out)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.SigningKeys.GetScope(w.plain); ok {
		t.Error("the non-mash key stays")
	}
	if _, ok := got.SigningKeys.GetScope(w.leaving); ok {
		t.Error("the key of a group that left the list set stays")
	}
	scopeOf(t, got, stay)
	if len(got.SigningKeys) != 1 {
		t.Errorf("signing keys: %d", len(got.SigningKeys))
	}
}

func TestSignRefusesWhenTheAdminIssuerIsANonMashKey(t *testing.T) {
	w := newDropWorld(t)
	w.enable(w.plain, nil)
	_, err := w.iss.Sign(listset.Record{Generation: 1}, w.compiled)
	var nk *admin.IssuersNotKeptError
	if !errors.As(err, &nk) || len(nk.Problems) != 1 || nk.Problems[0].Role != "admin" || nk.Problems[0].Issuer != w.plain {
		t.Fatalf("err %v", err)
	}
}

func TestSignRefusesWhenAConnectedExemptUserIsUnderANonMashKey(t *testing.T) {
	w := newDropWorld(t)
	w.enable(w.acct, map[string][]string{"UEXEMPT": {w.acct, w.plain}})
	_, err := w.iss.Sign(listset.Record{Generation: 1}, w.compiled)
	var nk *admin.IssuersNotKeptError
	if !errors.As(err, &nk) || len(nk.Problems) != 1 || nk.Problems[0].Role != "exempt" || nk.Problems[0].User != "UEXEMPT" {
		t.Fatalf("err %v", err)
	}
	// an unreadable scan refuses too
	w.iss.ExemptIssuers = func(context.Context) (map[string][]string, error) { return nil, errors.New("scan incomplete") }
	if _, err := w.iss.Sign(listset.Record{Generation: 1}, w.compiled); err == nil || !strings.Contains(err.Error(), "scan incomplete") {
		t.Errorf("err %v", err)
	}
}

func TestSignWithOnlyMashKeysNeedsNoDropFields(t *testing.T) {
	w := newDropWorld(t)
	w.iss.Resolver = oneJWT{tokOf(t, w, false)}
	if _, err := w.iss.Sign(listset.Record{Generation: 1}, w.compiled); err != nil {
		t.Fatal(err)
	}
}

// tokOf is the world's account JWT without the plain key.
func tokOf(t *testing.T, w *dropWorld, plain bool) string {
	t.Helper()
	ac := jwt.NewAccountClaims(w.acct)
	if plain {
		ac.SigningKeys.Add(w.plain)
	}
	sc := jwt.NewUserScope()
	sc.Key, sc.Role = w.leaving, "d"
	ac.SigningKeys.AddScopedSigner(sc)
	tok, err := ac.Encode(w.keys.op)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestSignDropsANonMashKeyOnlyOnTheFirstGeneration(t *testing.T) {
	w := newDropWorld(t)
	w.enable(w.acct, map[string][]string{"UEXEMPT": {w.acct, pubOf(w.keys.groups["c"])}})
	if _, err := w.iss.Sign(listset.Record{Generation: 1}, w.compiled); err != nil {
		t.Fatalf("generation 1: %v", err)
	}
	for _, g := range []uint64{0, 2, 7} {
		_, err := w.iss.Sign(listset.Record{Generation: g}, w.compiled)
		if err == nil || !strings.Contains(err.Error(), "remove it before a list set can be applied") || !strings.Contains(err.Error(), w.plain[:8]) {
			t.Errorf("generation %d: err %v", g, err)
		}
	}
}
