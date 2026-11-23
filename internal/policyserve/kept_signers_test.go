package policyserve

import (
	"context"
	"maps"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/listset"
)

// The kept signers are the other prefixes' group keys that the account JWT carries; a group whose name merely starts
// with the prefix's name, without the dot, is another prefix's. A key store key of another prefix that the JWT does not
// carry is neither kept (the gate would require it) nor retained by Sign (the target would lack it).
func TestKeptSignersAreOtherRangesGroupsInTheJWT(t *testing.T) {
	op, _ := nkeys.CreateOperator()
	acct, _ := nkeys.CreateAccount()
	apub := pubOf(acct)
	keys := storeKeys{op: op, groups: map[string]nkeys.KeyPair{
		"foo.c": newGroupKey(), "bar.x": newGroupKey(), "foobar.y": newGroupKey(), "baz.absent": newGroupKey(),
	}}
	ac := jwt.NewAccountClaims(apub)
	for _, g := range []string{"foo.c", "bar.x", "foobar.y"} { // baz.absent is in the key store only
		sc := jwt.NewUserScope()
		sc.Key, sc.Role = pubOf(keys.groups[g]), g
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
	now := time.Now().Add(5 * time.Second)
	iss := &Issuer{Ctx: context.Background(), Keys: keys, Resolver: oneJWT{tok}, Account: apub, Trust: trust, Servers: 1, Prefix: "foo",
		Now: func() time.Time { return now }}
	got, err := iss.Kept(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"bar.x": pubOf(keys.groups["bar.x"]), "foobar.y": pubOf(keys.groups["foobar.y"])}
	if !maps.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	c, err := listset.Compile(listset.ListSet{
		Prefix: "foo",
		Switches: []listset.SwitchEntry{{Group: "foo.c", Verb: listset.Mail, Switch: listset.Closed}, {Group: "foo.c", Verb: listset.Prompt, Switch: listset.Closed},
			{Group: "foo.c", Verb: listset.Status, Switch: listset.Closed}, {Group: "foo.c", Verb: listset.Heartbeat, Switch: listset.Closed}},
		Senders: []listset.SenderLists{{Sender: "foo.c.x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := iss.Sign(listset.Record{}, c)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := jwt.DecodeAccountClaims(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, pub := range got {
		scopeOf(t, signed, pub) // every kept key is retained
	}
	if _, ok := signed.SigningKeys.GetScope(pubOf(keys.groups["baz.absent"])); ok {
		t.Error("a key store key that the account JWT did not carry was added")
	}
}

// The configured account signing key is a plain signing key of the account JWT that is mash's own: it is not refused
// as foreign (a later generation does not drop non-mash keys), Kept does not list it, and Sign leaves it in place. A
// foreign plain key beside it is still refused.
func TestSignKeepsTheConfiguredAccountSigningKey(t *testing.T) {
	op, _ := nkeys.CreateOperator()
	acct, _ := nkeys.CreateAccount()
	apub := pubOf(acct)
	signing := newGroupKey()
	keys := storeKeys{op: op, groups: map[string]nkeys.KeyPair{"foo.c": newGroupKey()}, signing: signing}
	build := func(extraPlain string) *Issuer {
		ac := jwt.NewAccountClaims(apub)
		ac.SigningKeys.Add(pubOf(signing))
		if extraPlain != "" {
			addForeignScoped(ac, extraPlain)
		}
		tok, err := ac.Encode(op)
		if err != nil {
			t.Fatal(err)
		}
		trust, err := apply.NewTrust(pubOf(op), "")
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().Add(5 * time.Second)
		return &Issuer{Ctx: context.Background(), Keys: keys, Resolver: oneJWT{tok}, Account: apub, Trust: trust, Servers: 1, Prefix: "foo",
			Now: func() time.Time { return now }}
	}
	c, err := listset.Compile(listset.ListSet{
		Prefix: "foo",
		Switches: []listset.SwitchEntry{{Group: "foo.c", Verb: listset.Mail, Switch: listset.Closed}, {Group: "foo.c", Verb: listset.Prompt, Switch: listset.Closed},
			{Group: "foo.c", Verb: listset.Status, Switch: listset.Closed}, {Group: "foo.c", Verb: listset.Heartbeat, Switch: listset.Closed}},
		Senders: []listset.SenderLists{{Sender: "foo.c.x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	iss := build("")
	if k, err := iss.Kept(context.Background(), 2); err != nil || len(k) != 0 {
		t.Fatalf("Kept: %v %v", k, err)
	}
	out, err := iss.Sign(listset.Record{Generation: 2}, c)
	if err != nil {
		t.Fatalf("Sign refused the account signing key: %v", err)
	}
	signed, err := jwt.DecodeAccountClaims(out)
	if err != nil {
		t.Fatal(err)
	}
	if !signed.SigningKeys.Contains(pubOf(signing)) {
		t.Error("the account signing key was dropped")
	}
	if sc, _ := signed.SigningKeys.GetScope(pubOf(signing)); sc != nil {
		t.Error("the account signing key became scoped")
	}
	scopeOf(t, signed, pubOf(keys.groups["foo.c"]))
	// a foreign scoped key beside it is still refused
	if _, err := build(pubOf(newGroupKey())).Sign(listset.Record{Generation: 2}, c); err == nil {
		t.Error("a foreign scoped signing key was accepted")
	}
}
