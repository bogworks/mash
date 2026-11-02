package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/ctl"
)

func TestAdminZoneCLI(t *testing.T) {
	w := newWorld(t, "a", "plain")
	_, u := w.e.Mint(t, "zed")
	if out, code := w.admin(t, "provision", "zed", "--user", u, "--account", w.e.Account, "--note", "n", "--zone", "z1"); code != 0 ||
		!strings.Contains(out, "mail agents.mail.cc.o.z1.zed") || !strings.Contains(out, "zone z1") {
		t.Fatalf("provision --zone: %d %s", code, out)
	}
	// A legacy provision is exactly as before.
	_, u2 := w.e.Mint(t, "leg")
	if out, code := w.admin(t, "provision", "leg", "--user", u2, "--account", w.e.Account); code != 0 ||
		!strings.Contains(out, "mail agents.mail.cc.o.leg") || strings.Contains(out, "zone") {
		t.Fatalf("legacy provision: %d %s", code, out)
	}
	if out, code := w.admin(t, "provision", "bad", "--user", u, "--account", w.e.Account, "--zone", "A.b"); code == 0 || !strings.Contains(out, "zone") {
		t.Fatalf("bad zone: %d %s", code, out)
	}

	// list: a zone= suffix for a zoned endpoint only.
	lout, _ := w.admin(t, "list")
	if got := whoLine(t, lout, "zed"); !strings.HasSuffix(got, "\tzone=z1") {
		t.Fatalf("list line %q", got)
	}
	if got := whoLine(t, lout, "leg"); strings.Contains(got, "zone") || len(strings.Split(got, "\t")) != 4 {
		t.Fatalf("legacy list line %q", got)
	}

	// who: default line unchanged, --long shows the zone, --json has the key only when set.
	out, _ := w.cli(t, "a", "who")
	if got := whoLine(t, out, "zed"); got != "zed\toffline\tn" {
		t.Fatalf("default who line changed: %q", got)
	}
	out, _ = w.cli(t, "a", "who", "--long")
	if got := whoLine(t, out, "zed"); got != "zed\toffline\tn\tzone z1" {
		t.Fatalf("who --long: %q", got)
	}
	if got := whoLine(t, out, "leg"); strings.Contains(got, "zone") {
		t.Fatalf("who --long legacy: %q", got)
	}
	out, _ = w.cli(t, "a", "who", "--json")
	var entries []map[string]any
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for _, e := range entries {
		z, has := e["zone"]
		if e["name"] == "zed" && (!has || z != "z1") || e["name"] != "zed" && has {
			t.Fatalf("zone key in %v", e)
		}
	}

	// set-zone moves a legacy endpoint; mail follows the record.
	if out, code := w.admin(t, "set-zone", "leg", "z2"); code != 0 || !strings.Contains(out, "zone z2") || !strings.Contains(out, "agents.mail.cc.o.z2.leg") {
		t.Fatalf("set-zone: %d %s", code, out)
	}
	if out, code := w.admin(t, "set-zone", "leg", "z2"); code != 0 {
		t.Fatalf("set-zone again: %d %s", code, out)
	}
	if out, _ := w.admin(t, "list"); !strings.HasSuffix(whoLine(t, out, "leg"), "\tzone=z2") {
		t.Fatalf("list after move: %q", out)
	}
	if out, code := w.cli(t, "a", "send", "leg", "hello moved"); code != 0 {
		t.Fatalf("send to a moved endpoint: %d %s", code, out)
	}
	for _, bad := range [][]string{{"set-zone"}, {"set-zone", "leg"}, {"set-zone", "leg", "z", "extra"}, {"set-zone", "leg", "-"},
		{"set-zone", "leg", "Bad"}, {"set-zone", "leg", "a.b"}, {"set-zone", "ghost", "z"}, {"set-zone", "leg", "other"}, {"set-zone", "Bad Name", "z"}} {
		if out, code := w.admin(t, bad...); code == 0 {
			t.Errorf("%v succeeded: %s", bad, out)
		}
	}
	if out, _ := w.admin(t, "set-zone", "leg", "-"); !strings.Contains(out, "cannot return to the legacy subject") {
		t.Errorf("set-zone -: %s", out)
	}
}

func TestZoneUsage(t *testing.T) {
	text := ctl.Usage("mash admin")
	for _, want := range []string{"mash admin provision <name> --user U --account A", "[--zone Z]", "mash admin set-zone <name> <zone>"} {
		if !strings.Contains(text, want) {
			t.Errorf("usage lacks %q:\n%s", want, text)
		}
	}
}
