package endpoint

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
)

// addZoned makes name's directory record as a zoned release left it (the zone and a six-token mail subject). Nothing in
// mash builds such a record any more (ADR 0091), so the test rewrites a provisioned one by hand.
func (f *fixture) addZoned(t *testing.T, name, zone string) {
	t.Helper()
	ctx := context.Background()
	c, u := f.e.Mint(t, name)
	if _, err := f.adm.Provision(ctx, name, u, f.e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	js, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	kv, err := js.KeyValue(ctx, store.DirectoryBucket)
	if err != nil {
		t.Fatal(err)
	}
	ent, err := kv.Get(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(ent.Value(), &obj); err != nil {
		t.Fatal(err)
	}
	obj["zone"] = json.RawMessage(`"` + zone + `"`)
	obj["mail_subject"] = json.RawMessage(`"agents.mail.cc.o.` + zone + `.` + name + `"`)
	raw, _ := json.Marshal(obj)
	if _, err := kv.Update(ctx, name, raw, ent.Revision()); err != nil {
		t.Fatal(err)
	}
	f.creds[name], f.users[name] = c, u
}

// A zoned record, this endpoint's or another's, stops the start (ADR 0091); without one Open is unchanged.
func TestOpenRefusesZonedRecord(t *testing.T) {
	f := newFixture(t, "a")
	ctx := context.Background()
	ep, err := Open(ctx, f.cfg("a"))
	if err != nil {
		t.Fatalf("no zoned record: %v", err)
	}
	ep.Close()
	f.addZoned(t, "other", "red")
	if _, err := Open(ctx, f.cfg("a")); err == nil || !strings.Contains(err.Error(), `record other carries zone "red": zones are removed (ADR 0091)`) {
		t.Fatalf("another endpoint's zoned record: %v", err)
	}
	if _, err := Open(ctx, f.cfg("other")); err == nil || !strings.Contains(err.Error(), "zones are removed (ADR 0091)") {
		t.Fatalf("own zoned record: %v", err)
	}
}
