package sap

import (
	"strings"
	"testing"
)

// REQ-009 / AT-007: an owner, a zone and a name given to the subject builders come out as themselves. The subject
// functions join the tokens they are given with dots; they do not translate, alias, split or infer one from another.
// These are characterization tests of the existing code.

var passthroughCases = []struct{ label, owner, zone, name string }{
	{"plain", "acme", "prod", "ops"},
	{"dashes", "a-b-c", "x-y-z", "p-ops-2"},
	{"digits", "o1", "z2", "n3"},
	{"zone of 32 characters", "acme", strings.Repeat("z", MaxZoneLen), "ops"},
	{"owner equals zone", "same", "same", "ops"},
	{"owner equals zone and name", "same", "same", "same"},
	{"owner with parts looks like zone-name", "prod-ops", "prod", "ops"},
	{"zone with parts looks like owner-name", "acme", "acme-ops", "ops"},
	{"zone default is not special", "acme", DefaultZone, "ops"},
	{"zone that looks like an agent token", "acme", AgentToken, "ops"},
	{"owner of 48 characters", strings.Repeat("o", 48), "prod", strings.Repeat("n", 48)},
}

func TestZonePassthroughSubjects(t *testing.T) {
	for _, c := range passthroughCases {
		t.Run(c.label, func(t *testing.T) {
			if !ValidZone(c.zone) {
				t.Fatalf("fixture zone %q is not a valid zone", c.zone)
			}
			z := ForZone(c.owner, c.zone, c.name)
			wantMail := "agents.mail." + AgentToken + "." + c.owner + "." + c.zone + "." + c.name
			if z.Mail != wantMail {
				t.Fatalf("ForZone mail %q, want %q", z.Mail, wantMail)
			}
			// The zone does not reach any other subject of the endpoint: they equal For's, which has no zone.
			plain := For(c.owner, c.name)
			if z.Prompt != plain.Prompt || z.Status != plain.Status || z.HB != plain.HB {
				t.Fatalf("zone leaked into a non-mail subject: %+v vs %+v", z, plain)
			}
			wantPlain := "agents.mail." + AgentToken + "." + c.owner + "." + c.name
			if plain.Mail != wantPlain {
				t.Fatalf("For mail %q, want %q", plain.Mail, wantPlain)
			}
			if got := MailFilter(c.owner, c.name); got != "agents.mail.*."+c.owner+"."+c.name {
				t.Fatalf("MailFilter %q", got)
			}
			f := MailFilterZone(c.owner, c.zone, c.name)
			if f != "agents.mail.*."+c.owner+"."+c.zone+"."+c.name {
				t.Fatalf("MailFilterZone %q", f)
			}

			// SplitMail returns the tokens it was built from, byte for byte.
			o, zn, n, ok := SplitMail(z.Mail)
			if !ok || o != c.owner || zn != c.zone || n != c.name {
				t.Fatalf("SplitMail(%q) = %q %q %q %v", z.Mail, o, zn, n, ok)
			}
			// The legacy subject has no zone: it is not filled in with DefaultZone or anything else.
			o, zn, n, ok = SplitMail(plain.Mail)
			if !ok || o != c.owner || zn != "" || n != c.name {
				t.Fatalf("SplitMail(%q) = %q %q %q %v", plain.Mail, o, zn, n, ok)
			}

			// ZoneOfMailFilter returns the zone of the exact-zone filter unchanged, and only for the same owner and name.
			gz, ok := ZoneOfMailFilter(f, c.owner, c.name)
			if !ok || gz != c.zone {
				t.Fatalf("ZoneOfMailFilter(%q) = %q %v", f, gz, ok)
			}
			if _, ok := ZoneOfMailFilter(f, c.owner+"x", c.name); ok {
				t.Fatal("ZoneOfMailFilter accepted another owner")
			}
			if _, ok := ZoneOfMailFilter(f, c.owner, c.name+"x"); ok {
				t.Fatal("ZoneOfMailFilter accepted another name")
			}
			if _, ok := ZoneOfMailFilter(MailFilter(c.owner, c.name), c.owner, c.name); ok {
				t.Fatal("ZoneOfMailFilter invented a zone for the legacy filter")
			}
		})
	}
}

// A dashed owner is one token: SplitMail does not cut it into parts, and does not guess which part is a zone.
func TestSplitMailDoesNotSplitTokens(t *testing.T) {
	o, z, n, ok := SplitMail("agents.mail.cc.a-b-c.d-e")
	if !ok || o != "a-b-c" || z != "" || n != "d-e" {
		t.Fatalf("got %q %q %q %v", o, z, n, ok)
	}
	o, z, n, ok = SplitMail("agents.mail.cc.a-b-c.d-e.f-g")
	if !ok || o != "a-b-c" || z != "d-e" || n != "f-g" {
		t.Fatalf("got %q %q %q %v", o, z, n, ok)
	}
	// The agent token is not part of the result, whatever it is.
	o, z, n, ok = SplitMail("agents.mail.xx.acme.prod.ops")
	if !ok || o != "acme" || z != "prod" || n != "ops" {
		t.Fatalf("got %q %q %q %v", o, z, n, ok)
	}
}

// ValidZone only validates: a value it accepts is unchanged, a value it refuses is refused rather than repaired.
func TestValidZoneValidatesOnly(t *testing.T) {
	for _, z := range []string{"a", "a-b", "0", "default", strings.Repeat("z", MaxZoneLen)} {
		if !ValidZone(z) {
			t.Errorf("%q refused", z)
		}
	}
	for _, z := range []string{"", "A", "a.b", "a b", "*", ">", "a_b", strings.Repeat("z", MaxZoneLen+1)} {
		if ValidZone(z) {
			t.Errorf("%q accepted", z)
		}
	}
}
