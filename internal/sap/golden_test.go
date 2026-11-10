// Characterisation tests (plan 2b, Phase 0): these pin today's identifiers and validity for legacy identities; a change here is a change to a legacy identifier and needs its own decision.

package sap

import (
	"strings"
	"testing"
)

// goldenN48 is a 48-character name or owner; goldenZ32 is a 32-character zone.
const (
	goldenN48 = "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijkl"
	goldenZ32 = "z0123456789abcdefghijklmnopqrstu"
)

func TestGoldenVectorLengths(t *testing.T) {
	if len(goldenN48) != 48 || len(goldenZ32) != 32 {
		t.Fatalf("vector lengths %d %d", len(goldenN48), len(goldenZ32))
	}
}

func TestGoldenSubjectsLegacy(t *testing.T) {
	for _, c := range []struct{ owner, name, prompt, status, hb, mail, filter string }{
		{"o", "a",
			"agents.prompt.cc.o.a", "agents.status.cc.o.a", "agents.hb.cc.o.a", "agents.mail.cc.o.a", "agents.mail.*.o.a"},
		{"acme-corp", "build-1",
			"agents.prompt.cc.acme-corp.build-1", "agents.status.cc.acme-corp.build-1", "agents.hb.cc.acme-corp.build-1",
			"agents.mail.cc.acme-corp.build-1", "agents.mail.*.acme-corp.build-1"},
		{goldenN48, goldenN48,
			"agents.prompt.cc." + goldenN48 + "." + goldenN48, "agents.status.cc." + goldenN48 + "." + goldenN48,
			"agents.hb.cc." + goldenN48 + "." + goldenN48, "agents.mail.cc." + goldenN48 + "." + goldenN48,
			"agents.mail.*." + goldenN48 + "." + goldenN48},
	} {
		s := For(c.owner, c.name)
		if s.Prompt != c.prompt || s.Status != c.status || s.HB != c.hb || s.Mail != c.mail {
			t.Errorf("For(%q,%q) = %+v", c.owner, c.name, s)
		}
		if got := MailFilter(c.owner, c.name); got != c.filter {
			t.Errorf("MailFilter(%q,%q) = %q", c.owner, c.name, got)
		}
		if o, z, n, ok := SplitMail(c.mail); !ok || o != c.owner || z != "" || n != c.name {
			t.Errorf("SplitMail(%q) = %q %q %q %v", c.mail, o, z, n, ok)
		}
	}
}

func TestGoldenSubjectsZoned(t *testing.T) {
	for _, c := range []struct{ owner, zone, name, mail, filter string }{
		{"o", "z", "a", "agents.mail.cc.o.z.a", "agents.mail.*.o.z.a"},
		{"acme-corp", "default", "build-1", "agents.mail.cc.acme-corp.default.build-1", "agents.mail.*.acme-corp.default.build-1"},
		{"o", "9-z-", "a", "agents.mail.cc.o.9-z-.a", "agents.mail.*.o.9-z-.a"},
		{goldenN48, goldenZ32, goldenN48,
			"agents.mail.cc." + goldenN48 + "." + goldenZ32 + "." + goldenN48,
			"agents.mail.*." + goldenN48 + "." + goldenZ32 + "." + goldenN48},
	} {
		s := ForZone(c.owner, c.zone, c.name)
		legacy := For(c.owner, c.name)
		if s.Mail != c.mail {
			t.Errorf("ForZone mail = %q, want %q", s.Mail, c.mail)
		}
		// only mail carries the zone token
		if s.Prompt != legacy.Prompt || s.Status != legacy.Status || s.HB != legacy.HB {
			t.Errorf("ForZone changed a non-mail subject: %+v", s)
		}
		if got := MailFilterZone(c.owner, c.zone, c.name); got != c.filter {
			t.Errorf("MailFilterZone = %q, want %q", got, c.filter)
		}
		if o, z, n, ok := SplitMail(c.mail); !ok || o != c.owner || z != c.zone || n != c.name {
			t.Errorf("SplitMail(%q) = %q %q %q %v", c.mail, o, z, n, ok)
		}
		if z, ok := ZoneOfMailFilter(c.filter, c.owner, c.name); !ok || z != c.zone {
			t.Errorf("ZoneOfMailFilter(%q) = %q %v", c.filter, z, ok)
		}
		// the legacy filter is not an exact-zone filter
		if _, ok := ZoneOfMailFilter(MailFilter(c.owner, c.name), c.owner, c.name); ok {
			t.Errorf("legacy filter parsed as zoned")
		}
	}
}

func TestGoldenSplitMailRefusals(t *testing.T) {
	for _, s := range []string{
		"", "agents.mail.cc.o", "agents.mail.cc.o.z.a.x", "agents.prompt.cc.o.a", "agent.mail.cc.o.a",
		"agents.mail.cc..a", "agents.mail.cc.o.", "agents.mail.cc.o.z.",
	} {
		if _, _, _, ok := SplitMail(s); ok {
			t.Errorf("SplitMail(%q) accepted", s)
		}
	}
	if _, ok := ZoneOfMailFilter("agents.mail.*.o.Z.a", "o", "a"); ok {
		t.Error("ZoneOfMailFilter accepted an invalid zone")
	}
	if _, ok := ZoneOfMailFilter("agents.mail.*.o.z.b", "o", "a"); ok {
		t.Error("ZoneOfMailFilter accepted another name")
	}
}

func TestGoldenInboxNames(t *testing.T) {
	for _, c := range []struct{ name, stream, prefix string }{
		{"a", "inbox-a", "_INBOX.a"},
		{"build-1", "inbox-build-1", "_INBOX.build-1"},
		{goldenN48, "inbox-" + goldenN48, "_INBOX." + goldenN48},
	} {
		if got := InboxStream(c.name); got != c.stream {
			t.Errorf("InboxStream(%q) = %q", c.name, got)
		}
		if got := InboxPrefix(c.name); got != c.prefix {
			t.Errorf("InboxPrefix(%q) = %q", c.name, got)
		}
	}
}

func TestGoldenProtocolConstants(t *testing.T) {
	if AgentToken != "cc" || AgentName != "claude-code" || ServiceName != "agents" || QueueGroup != "agents" ||
		ProtocolVersion != "0.3" || DefaultZone != "default" || MaxZoneLen != 32 {
		t.Error("a protocol constant changed")
	}
}

func TestGoldenValidZone(t *testing.T) {
	for _, z := range []string{"a", "z", "default", "0", "9abc", "-", "-a", "a-", "a--b", goldenZ32, strings.Repeat("0", 32), strings.Repeat("-", 32)} {
		if !ValidZone(z) {
			t.Errorf("ValidZone(%q) = false, today it accepts it", z)
		}
	}
	for _, z := range []string{
		"", goldenZ32 + "a", strings.Repeat("a", 33), "a.b", ".", "*", ">", "a*", "a>", " ", "a b", "a\tb", "a\nb", "A", "aB", "a_b", "é",
	} {
		if ValidZone(z) {
			t.Errorf("ValidZone(%q) = true, today it refuses it", z)
		}
	}
}
