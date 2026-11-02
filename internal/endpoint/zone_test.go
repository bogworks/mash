package endpoint

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/sap"
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

// Legacy and zoned endpoints mail each other through the directory: a sender follows the recipient's record.
func TestLegacyAndZonedMailEachOther(t *testing.T) {
	f := newFixture(t, "old")
	f.addZoned(t, "new", "default")
	old, oldEv := f.run(t, "old")
	nw, newEv := f.run(t, "new")
	if old.subj.Mail != "agents.mail.cc.o.old" || nw.subj.Mail != "agents.mail.cc.o.default.new" {
		t.Fatalf("own subjects %q %q", old.subj.Mail, nw.subj.Mail)
	}
	if nw.subj.Prompt != sap.For("o", "new").Prompt || nw.subj.HB != sap.For("o", "new").HB || nw.subj.Status != sap.For("o", "new").Status {
		t.Fatalf("prompt, status and heartbeat subjects changed: %+v", nw.subj)
	}
	ctx := context.Background()
	if _, err := old.Send(ctx, "new", "legacy to zoned", 0); err != nil {
		t.Fatal(err)
	}
	if got := newEv.waitFor(t, 1, 5*time.Second); !strings.Contains(got[0], "from old [verified]") || !strings.Contains(got[0], "legacy to zoned") {
		t.Fatalf("zoned side: %v", got)
	}
	if _, err := nw.Send(ctx, "old", "zoned to legacy", 0); err != nil {
		t.Fatal(err)
	}
	if got := oldEv.waitFor(t, 1, 5*time.Second); !strings.Contains(got[0], "from new [verified]") || !strings.Contains(got[0], "zoned to legacy") {
		t.Fatalf("legacy side: %v", got)
	}
	// The advertised mail subject in the service registration is the record's.
	if err := nw.StartService(ctx, func(PromptEvent) {}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(nw.heartbeat()), `"agents.mail.cc.o.default.new"`) {
		t.Fatalf("heartbeat: %s", nw.heartbeat())
	}
}

// A moved endpoint gets mail from a sender that has not learned of the move and publishes to the old subject, and from
// one that follows the new record.
func TestMovedEndpointStillGetsOldSubjectMail(t *testing.T) {
	f := newFixture(t, "m", "s")
	ctx := context.Background()
	m, ev := f.run(t, "m")
	if _, err := f.adm.SetZone(ctx, "m", "z1"); err != nil {
		t.Fatal(err)
	}
	// A sender that has not been updated: signs and publishes to the legacy subject.
	oldSubj := sap.For(f.e.Owner, "m").Mail
	nc := f.e.ConnAs(t, f.creds["s"], "s")
	id := identity(t, f.creds["s"])
	h, err := id.Sign(oldSubj, []byte("to the old subject"), "s", time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	publishRaw(t, nc, oldSubj, h.Encode(), "to the old subject")
	// A fresh client reads the record and publishes to the new one.
	if _, err := f.client(t, "s").Send(ctx, "m", "to the new subject", 0); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(ev.waitFor(t, 1, 5*time.Second), "\n")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !(strings.Contains(got, "to the old subject") && strings.Contains(got, "to the new subject")) {
		time.Sleep(20 * time.Millisecond)
		got = strings.Join(ev.all(), "\n")
	}
	if !strings.Contains(got, "to the old subject") || !strings.Contains(got, "to the new subject") {
		t.Fatalf("events:\n%s", got)
	}
	if strings.Contains(got, "[claimed]") || strings.Contains(got, "[absent]") {
		t.Fatalf("a message was not verified:\n%s", got)
	}
	_ = m
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
