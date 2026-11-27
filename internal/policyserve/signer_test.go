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
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
)

// storeKeys is a Keys with an operator key and any number of owner group keys.
type storeKeys struct {
	op      nkeys.KeyPair
	groups  map[string]nkeys.KeyPair
	signing nkeys.KeyPair // the account signing key (signing.nk); nil when none
}

func (s storeKeys) Signing() (nkeys.KeyPair, string, error) {
	if s.signing == nil {
		return nil, "", ctlkeys.ErrNoSigningKey
	}
	kp, err := nkeys.FromSeed(mustSeed(s.signing))
	pub, _ := kp.PublicKey()
	return kp, pub, err
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
		Owner: "foo",
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

// dropWorld is an account JWT with a plain (non-mash) signing key and another range's held group key, and an Issuer
// for range foo over it.
type dropWorld struct {
	keys        storeKeys
	acct        string
	plain       string
	otherRange  string
	iss         *Issuer
	compiled    *listset.Compiled
	targetGroup string
}

func newDropWorld(t *testing.T) *dropWorld {
	t.Helper()
	op, _ := nkeys.CreateOperator()
	acct, _ := nkeys.CreateAccount()
	w := &dropWorld{acct: pubOf(acct), plain: pubOf(newGroupKey()), targetGroup: "foo.c"}
	w.keys = storeKeys{op: op, groups: map[string]nkeys.KeyPair{"foo.c": newGroupKey(), "bar.d": newGroupKey()}}
	w.otherRange = pubOf(w.keys.groups["bar.d"])
	ac := jwt.NewAccountClaims(w.acct)
	addForeignScoped(ac, w.plain)
	sc := jwt.NewUserScope()
	sc.Key, sc.Role = w.otherRange, "bar.d"
	sc.Template.Pub.Allow.Add("keep.bar.d")
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
		Owner: "foo",
		Switches: []listset.SwitchEntry{{Group: "foo.c", Verb: listset.Mail, Switch: listset.Closed}, {Group: "foo.c", Verb: listset.Prompt, Switch: listset.Closed},
			{Group: "foo.c", Verb: listset.Status, Switch: listset.Closed}, {Group: "foo.c", Verb: listset.Heartbeat, Switch: listset.Closed}},
		Senders: []listset.SenderLists{{Sender: "foo.c.x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(5 * time.Second)
	w.iss = &Issuer{Ctx: context.Background(), Keys: w.keys, Resolver: oneJWT{tok}, Account: w.acct, Trust: trust, Servers: 1, Range: "foo",
		Now: func() time.Time { return now }}
	return w
}

// enable sets the drop fields: the admin user signed by adminIssuer.
func (w *dropWorld) enable(adminIssuer string) {
	w.iss.AdminUser, w.iss.AdminIssuer = "UADMIN", adminIssuer
}

func TestSignRefusesANonMashKeyWithoutTheDropFields(t *testing.T) {
	w := newDropWorld(t)
	for name, set := range map[string]func(){
		"none":            func() {},
		"no admin issuer": func() { w.iss.AdminUser, w.iss.AdminIssuer = "UADMIN", "" },
	} {
		w.iss.AdminUser, w.iss.AdminIssuer = "", ""
		set()
		_, err := w.iss.Sign(listset.Record{Generation: 1}, w.compiled)
		if err == nil || !strings.Contains(err.Error(), "not an owner group key of the key store") || !strings.Contains(err.Error(), w.plain[:8]) {
			t.Errorf("%s: err %v", name, err)
		}
		if _, err := w.iss.Kept(context.Background(), 1); err == nil {
			t.Errorf("%s: Kept accepted a non-mash key", name)
		}
	}
}

func TestSignDropsANonMashKeyAfterTheIssuerCheckAndKeepsOtherRanges(t *testing.T) {
	w := newDropWorld(t)
	other := newGroupKey()
	w.enable(pubOf(other)) // the admin user is under a key that is neither kept nor in the JWT
	if _, err := w.iss.Sign(listset.Record{Generation: 1}, w.compiled); err == nil {
		t.Fatal("an admin user under an unknown key passed the check")
	}
	w.enable(w.otherRange) // under another range's key the JWT carries: kept
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
	if us := scopeOf(t, got, w.otherRange); us.Role != "bar.d" {
		t.Errorf("the other range's key changed: %+v", us)
	}
	scopeOf(t, got, pubOf(w.keys.groups["foo.c"]))
	if len(got.SigningKeys) != 2 {
		t.Errorf("signing keys: %d", len(got.SigningKeys))
	}
	if k, err := w.iss.Kept(context.Background(), 1); err != nil || k["bar.d"] != w.otherRange || len(k) != 1 {
		t.Errorf("Kept: %v %v", k, err)
	}
}

func TestSignRefusesWhenTheAdminIssuerIsANonMashKey(t *testing.T) {
	w := newDropWorld(t)
	w.enable(w.plain)
	_, err := w.iss.Sign(listset.Record{Generation: 1}, w.compiled)
	var nk *admin.IssuersNotKeptError
	if !errors.As(err, &nk) || len(nk.Problems) != 1 || nk.Problems[0].Role != "admin" || nk.Problems[0].Issuer != w.plain {
		t.Fatalf("err %v", err)
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
		addForeignScoped(ac, w.plain)
	}
	sc := jwt.NewUserScope()
	sc.Key, sc.Role = w.otherRange, "bar.d"
	ac.SigningKeys.AddScopedSigner(sc)
	tok, err := ac.Encode(w.keys.op)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestSignDropsANonMashKeyOnlyOnTheFirstGeneration(t *testing.T) {
	w := newDropWorld(t)
	w.enable(w.acct)
	if _, err := w.iss.Sign(listset.Record{Generation: 1}, w.compiled); err != nil {
		t.Fatalf("generation 1: %v", err)
	}
	if _, err := w.iss.Kept(context.Background(), 1); err != nil {
		t.Fatalf("Kept generation 1: %v", err)
	}
	for _, g := range []uint64{0, 2, 7} {
		_, err := w.iss.Sign(listset.Record{Generation: g}, w.compiled)
		if err == nil || !strings.Contains(err.Error(), "remove it before a list set can be applied") || !strings.Contains(err.Error(), w.plain[:8]) {
			t.Errorf("generation %d: err %v", g, err)
		}
		if _, err := w.iss.Kept(context.Background(), g); err == nil {
			t.Errorf("generation %d: Kept accepted a non-mash key", g)
		}
	}
}

// addForeignScoped adds a scoped signing key that is not an owner group key of any key store (another issuer's).
func addForeignScoped(ac *jwt.AccountClaims, key string) {
	sc := jwt.NewUserScope()
	sc.Key, sc.Role = key, "other.role"
	sc.Template.Pub.Allow.Add("other.>")
	ac.SigningKeys.AddScopedSigner(sc)
}

// Foreign plain signing keys (the admin user's other issuers) are carried: never non-mash, never dropped,
// and a first-generation Sign with no drop fields does not refuse them.
func TestSignCarriesEveryPlainSigningKey(t *testing.T) {
	op, _ := nkeys.CreateOperator()
	acct, _ := nkeys.CreateAccount()
	apub := pubOf(acct)
	plain1, plain2 := pubOf(newGroupKey()), pubOf(newGroupKey())
	keys := storeKeys{op: op, groups: map[string]nkeys.KeyPair{"foo.c": newGroupKey()}}
	ac := jwt.NewAccountClaims(apub)
	ac.SigningKeys.Add(plain1)
	ac.SigningKeys.Add(plain2)
	tok, err := ac.Encode(op)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := apply.NewTrust(pubOf(op), "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(5 * time.Second)
	iss := &Issuer{Ctx: context.Background(), Keys: keys, Resolver: oneJWT{tok}, Account: apub, Trust: trust, Servers: 1, Range: "foo",
		Now: func() time.Time { return now }}
	c, err := listset.Compile(listset.ListSet{
		Owner: "foo",
		Switches: []listset.SwitchEntry{{Group: "foo.c", Verb: listset.Mail, Switch: listset.Closed}, {Group: "foo.c", Verb: listset.Prompt, Switch: listset.Closed},
			{Group: "foo.c", Verb: listset.Status, Switch: listset.Closed}, {Group: "foo.c", Verb: listset.Heartbeat, Switch: listset.Closed}},
		Senders: []listset.SenderLists{{Sender: "foo.c.x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := iss.Plain(context.Background())
	if err != nil || len(plain) != 2 {
		t.Fatalf("Plain = %v, %v", plain, err)
	}
	for _, g := range []uint64{1, 2} {
		out, err := iss.Sign(listset.Record{Generation: g}, c)
		if err != nil {
			t.Fatalf("generation %d: %v", g, err)
		}
		got, err := jwt.DecodeAccountClaims(out)
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{plain1, plain2} {
			if !got.SigningKeys.Contains(k) {
				t.Errorf("generation %d: plain key dropped", g)
			}
		}
		if _, err := iss.Kept(context.Background(), g); err != nil {
			t.Errorf("Kept %d: %v", g, err)
		}
		if gen, ok := policyapply.GenerationOf(out, []policyapply.Candidate{{Generation: g, Compiled: c, Signers: map[string]string{"foo.c": pubOf(keys.groups["foo.c"])}, Plain: plain}}); !ok || gen != g {
			t.Errorf("generation %d: the generation read fails with the plain keys carried", g)
		}
	}
}

// foreignScopedWorld is the drop world with the configured plain account signing key (signing.nk) and a foreign scoped
// key (addForeignScoped) in the account JWT, plus another range's held group key.
func foreignScopedWorld(t *testing.T) (*dropWorld, string) {
	t.Helper()
	w := newDropWorld(t)
	signing := newGroupKey()
	w.keys.signing = signing
	w.iss.Keys = w.keys
	ac := jwt.NewAccountClaims(w.acct)
	ac.SigningKeys.Add(pubOf(signing))
	addForeignScoped(ac, w.plain)
	tok, err := ac.Encode(w.keys.op)
	if err != nil {
		t.Fatal(err)
	}
	w.iss.Resolver = oneJWT{tok}
	w.enable(w.acct)
	return w, pubOf(signing)
}

// ADR 0124 / ADR 0158: the first generation, with the drop fields set, drops a foreign scoped signing key and keeps the
// configured plain account signing key.
func TestSigningKeySet_FirstGenerationDropsAForeignScopedKeyAndKeepsTheConfiguredKey(t *testing.T) {
	w, cfg := foreignScopedWorld(t)
	out, err := w.iss.Sign(listset.Record{Generation: 1}, w.compiled)
	if err != nil {
		t.Fatal(err)
	}
	got, err := jwt.DecodeAccountClaims(out)
	if err != nil {
		t.Fatal(err)
	}
	if got.SigningKeys.Contains(w.plain) {
		t.Error("the foreign scoped key stays")
	}
	if !got.SigningKeys.Contains(cfg) {
		t.Fatal("the configured plain signing key was dropped")
	}
	if sc, _ := got.SigningKeys.GetScope(cfg); sc != nil {
		t.Error("the configured signing key became scoped")
	}
}

// ADR 0124 / ADR 0158: a later generation does not drop; it refuses the foreign scoped signing key, and the configured
// plain account signing key is still in the account JWT.
func TestSigningKeySet_LaterGenerationRefusesAForeignScopedKey(t *testing.T) {
	w, cfg := foreignScopedWorld(t)
	for _, g := range []uint64{2, 7} {
		out, err := w.iss.Sign(listset.Record{Generation: g}, w.compiled)
		if err == nil || out != "" {
			t.Fatalf("generation %d: the foreign scoped key was accepted (err %v)", g, err)
		}
		if !strings.Contains(err.Error(), "remove it before a list set can be applied") || !strings.Contains(err.Error(), w.plain[:8]) {
			t.Errorf("generation %d: the refusal does not name the foreign key: %v", g, err)
		}
		if strings.Contains(err.Error(), cfg[:8]) {
			t.Errorf("generation %d: the configured key is blamed: %v", g, err)
		}
	}
	tok, err := w.iss.Resolver.Lookup(context.Background(), w.acct)
	if err != nil {
		t.Fatal(err)
	}
	cl, err := jwt.DecodeAccountClaims(tok)
	if err != nil {
		t.Fatal(err)
	}
	if !cl.SigningKeys.Contains(cfg) || !cl.SigningKeys.Contains(w.plain) {
		t.Error("the account JWT changed")
	}
}
