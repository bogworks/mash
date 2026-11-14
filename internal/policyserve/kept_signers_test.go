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

// The kept signers are the other ranges' group keys that the account JWT carries; a group whose name merely starts
// with the range's name, without the dot, is another range's. A key store key of another range that the JWT does not
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
	iss := &Issuer{Ctx: context.Background(), Keys: keys, Resolver: oneJWT{tok}, Account: apub, Trust: trust, Servers: 1, Range: "foo",
		Now: func() time.Time { return now }}
	got, err := iss.Kept(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"bar.x": pubOf(keys.groups["bar.x"]), "foobar.y": pubOf(keys.groups["foobar.y"])}
	if !maps.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
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
