package admin_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
)

func TestSetUser(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, u1 := e.Mint(t, "alpha")
	_, u2 := e.Mint(t, "alpha2")
	meta := admin.Meta{Role: "r", Zone: "z", Labels: map[string]string{"team": "x"}}
	if _, err := a.Provision(ctx, "alpha", u1, e.Account, "n", meta); err != nil {
		t.Fatal(err)
	}
	rec, err := a.SetUser(ctx, "alpha", e.Account, u1, u2)
	if err != nil || rec.User != u2 || rec.Role != "r" || rec.Labels["team"] != "x" || rec.Note != "n" || rec.Zone != "z" {
		t.Fatalf("%+v %v", rec, err)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	before, _ := kv.Get(ctx, "alpha")
	// idempotent: the same user again changes nothing, not even the revision
	if rec, err := a.SetUser(ctx, "alpha", e.Account, u1, u2); err != nil || rec.User != u2 {
		t.Fatalf("%+v %v", rec, err)
	}
	if after, _ := kv.Get(ctx, "alpha"); after.Revision() != before.Revision() {
		t.Error("an idempotent SetUser wrote the record")
	}
	// compare-and-set: a record whose user is neither old nor new is refused and left alone
	_, u3 := e.Mint(t, "alpha3")
	if _, err := a.SetUser(ctx, "alpha", e.Account, u1, u3); err == nil || !strings.Contains(err.Error(), "left in place") {
		t.Errorf("wrong old user: %v", err)
	}
	if got, _ := store.GetRecord(ctx, kv, "alpha"); got.User != u2 {
		t.Errorf("a refused CAS changed the record: %+v", got)
	}
	if user, acct, err := a.UserOf(ctx, "alpha"); err != nil || user != u2 || acct != e.Account {
		t.Errorf("UserOf: %q %q %v", user, acct, err)
	}
	if _, _, err := a.UserOf(ctx, "nobody"); !errors.Is(err, admin.ErrNoRecord) {
		t.Errorf("UserOf no record: %v", err)
	}
	// another account is refused, and so is no record, a bad key and a bad name
	if _, err := a.SetUser(ctx, "alpha", "AABC", u2, u1); err == nil || !strings.Contains(err.Error(), "belongs to account") {
		t.Errorf("account mismatch: %v", err)
	}
	if got, _ := store.GetRecord(ctx, kv, "alpha"); got.User != u2 {
		t.Errorf("a refused call changed the record: %+v", got)
	}
	if _, err := a.SetUser(ctx, "nobody", e.Account, u2, u1); err == nil || !strings.Contains(err.Error(), "not provisioned") {
		t.Errorf("no record: %v", err)
	}
	if _, err := a.SetUser(ctx, "alpha", e.Account, u2, "not-a-key"); err == nil {
		t.Error("bad key accepted")
	}
	if _, err := a.SetUser(ctx, "Bad Name", e.Account, u2, u1); err == nil {
		t.Error("bad name accepted")
	}
}
