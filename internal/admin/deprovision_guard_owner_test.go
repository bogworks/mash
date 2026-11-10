package admin_test

import (
	"context"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

// otherOwnerAdmin is an admin on the same directory configured with an owner that is not the legacy record's own.
func otherOwnerAdmin(t *testing.T, e *testenv.Env) *admin.Admin {
	t.Helper()
	b, err := admin.New(e.Conn(t, e.AdminCreds), "p")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The alias guard matches the legacy record's own owner, not the removing admin's configured owner.
func TestLegacyDeprovisionGuardUsesRecordOwnerNotAdminOwner(t *testing.T) {
	type rm struct {
		name string
		run  func(b *admin.Admin, user, sid string, seq uint64) error
	}
	rms := []rm{
		{"Deprovision", func(b *admin.Admin, _, _ string, _ uint64) error { return b.Deprovision(context.Background(), "ghost") }},
		{"DeprovisionIfUser", func(b *admin.Admin, u, _ string, _ uint64) error {
			return b.DeprovisionIfUser(context.Background(), "ghost", u)
		}},
		{"DeprovisionDrained", func(b *admin.Admin, _, sid string, seq uint64) error {
			return b.DeprovisionDrained(context.Background(), "ghost", sid, seq)
		}},
	}
	for _, r := range rms {
		t.Run(r.name, func(t *testing.T) {
			e, a, js := setup(t)
			ctx := context.Background()
			_, legUser := e.Mint(t, "ghost")
			if _, err := a.Provision(ctx, "ghost", legUser, e.Account, "", admin.Meta{}); err != nil {
				t.Fatal(err)
			}
			id := newID(t, "acme", "team1", "ops1")
			_, user := e.Mint(t, "ops1")
			if _, err := a.ProvisionIdentity(ctx, id, &store.Alias{Owner: e.Owner, Name: "ghost"}, user, e.Account, "", admin.Meta{}); err != nil {
				t.Fatal(err)
			}
			sid, seq := suspendIDSeq(t, a, "ghost")
			b := otherOwnerAdmin(t, e)
			keys, streams := dirKeys(t, js), streamNames(t, js)
			err := r.run(b, legUser, sid, seq)
			if err == nil || !strings.Contains(err.Error(), id.Flat()) {
				t.Fatalf("want a refusal naming %s, got %v", id.Flat(), err)
			}
			untouched(t, js, keys, streams)
			if err := a.Deprovision(ctx, id.Flat()); err != nil {
				t.Fatal(err)
			}
			if err := r.run(b, legUser, sid, seq); err != nil {
				t.Fatalf("after the alias is gone: %v", err)
			}
		})
	}
}

// A legacy record whose owner cannot be established blocks any alias naming its name, whatever the alias owner.
func TestLegacyDeprovisionUnreadableRecordBlocksOnNameAlone(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	_, legUser := e.Mint(t, "ghost")
	if _, err := a.Provision(ctx, "ghost", legUser, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	id := newID(t, "acme", "team1", "ops1")
	_, user := e.Mint(t, "ops1")
	if _, err := a.ProvisionIdentity(ctx, id, &store.Alias{Owner: e.Owner, Name: "ghost"}, user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	kv, _ := js.KeyValue(ctx, store.DirectoryBucket)
	if _, err := kv.Put(ctx, "ghost", []byte("not json")); err != nil {
		t.Fatal(err)
	}
	b := otherOwnerAdmin(t, e)
	if err := b.Deprovision(ctx, "ghost"); err == nil {
		t.Fatalf("want a refusal while %s aliases the unreadable record", id.Flat())
	}
	if _, err := kv.Get(ctx, "ghost"); err != nil {
		t.Fatalf("nothing may be removed: %v", err)
	}
}
