package endpoint

import (
	"context"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
)

// addZoned provisions name into zone from the start.
func (f *fixture) addZoned(t *testing.T, name, zone string) {
	t.Helper()
	c, u := f.e.Mint(t, name)
	if _, err := f.adm.Provision(context.Background(), name, u, f.e.Account, "", admin.Meta{Zone: zone}); err != nil {
		t.Fatal(err)
	}
	f.creds[name], f.users[name] = c, u
}

// Who carries the zone of a zoned endpoint and none for a legacy one.
func TestWhoShowsZone(t *testing.T) {
	f := newFixture(t, "old", "viewer")
	f.addZoned(t, "new", "z1")
	v, err := OpenClientWith(context.Background(), f.cfg("viewer"), ClientOptions{FullDirectory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Close)
	zones := map[string]string{}
	for _, p := range v.Who() {
		zones[p.Record.Name] = p.Record.Zone
	}
	if zones["new"] != "z1" || zones["old"] != "" || len(zones) != 3 {
		t.Fatalf("zones %v", zones)
	}
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
