package policyserve

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
)

const settledGroup = "c"

// settledJWT is the account JWT of a server that holds the scoped templates of c, with the keys revoked at the times
// given.
func settledJWT(t *testing.T, acct nkeys.KeyPair, groupPub string, c *listset.Compiled, revoked map[string]int64) string {
	t.Helper()
	apub, err := acct.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	ac := jwt.NewAccountClaims(apub)
	for _, tpl := range c.Templates {
		sc := jwt.NewUserScope()
		sc.Key, sc.Role = groupPub, tpl.Group
		sc.Template.Permissions = tpl.Permissions
		ac.SigningKeys.AddScopedSigner(sc)
	}
	for k, at := range revoked {
		ac.RevokeAt(k, time.Unix(at, 0))
	}
	tok, err := ac.Encode(acct)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// Whether the servers already hold the change decides which senders keep a witness: a sender is settled only when every
// server of the declared count was read and holds the target templates and, for a removed sender, the revocation of every
// key the record has for it.
func TestSettledSenders(t *testing.T) {
	lead := listset.SenderLists{Sender: "c.lead"}
	sdk := listset.SenderLists{Sender: "c.sdk"}
	core := listset.SenderLists{Sender: "c.core", Allow: []string{"c.lead"}}
	prev := witnessSetWith(t, []listset.Verb{listset.Status}, core, lead, sdk)
	target := witnessSet(t, core, lead)
	ex, err := policyapply.NewExpect(prev, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	acct, _ := nkeys.CreateAccount()
	apub, _ := acct.PublicKey()
	grp, _ := nkeys.CreateAccount()
	gpub, _ := grp.PublicKey()
	signers := map[string]string{settledGroup: gpub}
	issued := time.Now().Add(-time.Hour).Truncate(time.Second)
	keyA, keyB := "UAKEYA", "UAKEYB"
	record := map[string][]policyapply.IssueEntry{"c.sdk": {
		{Address: "c.sdk", PublicKey: keyA, Purpose: policyapply.PurposeIssue, IssuedAt: issued},
		{Address: "c.sdk", PublicKey: keyB, Purpose: policyapply.PurposeRotation, IssuedAt: issued},
		{Address: "c.sdk", PublicKey: "UAPROBE", Purpose: policyapply.PurposeProbe, IssuedAt: issued},
	}}
	entries := func(a string) ([]policyapply.IssueEntry, error) { return record[a], nil }
	after := issued.Add(time.Minute).Unix()
	both := map[string]int64{keyA: after, keyB: after}
	onTarget := settledJWT(t, acct, gpub, target, both)
	onPrev := settledJWT(t, acct, gpub, prev, both)

	all := []string{"c.core", "c.lead", "c.sdk"}
	cases := []struct {
		name      string
		jwts      map[string]string
		inventory int
		record    func(string) ([]policyapply.IssueEntry, error)
		want      []string
	}{
		{"every server on the target, the key revoked", map[string]string{"a": onTarget, "b": onTarget}, 2, entries, nil},
		{"one server lags on the old templates", map[string]string{"a": onTarget, "b": onPrev}, 2, entries, all},
		{"a server did not answer", map[string]string{"a": onTarget}, 2, entries, all},
		{"a server holds no account JWT", map[string]string{"a": onTarget, "b": ""}, 2, entries, all},
		{"a server's JWT does not decode", map[string]string{"a": onTarget, "b": "garbage"}, 2, entries, all},
		{"no server answered", map[string]string{}, 1, entries, all},
		{"the record cannot be read", map[string]string{"a": onTarget}, 1,
			func(string) ([]policyapply.IssueEntry, error) { return nil, errors.New("no record") }, []string{"c.sdk"}},
		{"a revocation is earlier than the credential", map[string]string{"a": onTarget, "b": settledJWT(t, acct, gpub, target,
			map[string]int64{keyA: after, keyB: issued.Unix() - 5})}, 2, entries, []string{"c.sdk"}},
		{"a key is not revoked on one server", map[string]string{"a": onTarget, "b": settledJWT(t, acct, gpub, target,
			map[string]int64{keyA: after})}, 2, entries, []string{"c.sdk"}},
		{"the removed sender has no key in the record", map[string]string{"a": onTarget}, 1,
			func(string) ([]policyapply.IssueEntry, error) { return nil, nil }, []string{"c.sdk"}},
		{"a key with no issue time", map[string]string{"a": onTarget}, 1,
			func(string) ([]policyapply.IssueEntry, error) {
				return []policyapply.IssueEntry{{PublicKey: keyA, Purpose: policyapply.PurposeIssue}}, nil
			}, []string{"c.sdk"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := settledSenders(c.jwts, c.inventory, apub, signers, prev, target, ex, c.record)
			got := sendersOf(witnessSenders(prev, target, ex, st))
			slices.Sort(got)
			if !slices.Equal(got, c.want) {
				t.Errorf("witnesses for %v, want %v", got, c.want)
			}
		})
	}
}

// With nothing read the zero state settles nothing and every affected sender keeps its witness, as on a first run.
func TestSettledSendersWithoutServerStateSettlesNothing(t *testing.T) {
	core := listset.SenderLists{Sender: "c.core"}
	sdk := listset.SenderLists{Sender: "c.sdk"}
	prev, target := witnessSetWith(t, []listset.Verb{listset.Mail}, core, sdk), witnessSet(t, core)
	ex, _ := policyapply.NewExpect(prev, target, nil)
	got := sendersOf(witnessSenders(prev, target, ex, settledState{}))
	slices.Sort(got)
	if want := []string{"c.core", "c.sdk"}; !slices.Equal(got, want) {
		t.Errorf("witnesses for %v, want %v", got, want)
	}
}
