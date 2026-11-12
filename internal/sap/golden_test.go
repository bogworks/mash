// Characterisation tests (plan 2b, Phase 0): these pin today's identifiers and validity for legacy identities; a change here is a change to a legacy identifier and needs its own decision.

package sap

import "testing"

// goldenN48 is a 48-character name or owner.
const (
	goldenN48 = "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijkl"
)

func TestGoldenVectorLengths(t *testing.T) {
	if len(goldenN48) != 48 {
		t.Fatalf("vector length %d", len(goldenN48))
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
		if o, n, ok := SplitMail(c.mail); !ok || o != c.owner || n != c.name {
			t.Errorf("SplitMail(%q) = %q %q %v", c.mail, o, n, ok)
		}
	}
}

func TestGoldenLegacyZoneParsing(t *testing.T) {
	// Legacy parsing for unzone and the upgrade guards: a six-token filter or subject of an older release is read, never built.
	for _, c := range []struct{ owner, zone, name string }{
		{"o", "z", "a"},
		{"acme-corp", "default", "build-1"},
		{"o", "9-z-", "a"},
		{goldenN48, "z0123456789abcdefghijklmnopqrstu", goldenN48},
	} {
		filter := "agents.mail.*." + c.owner + "." + c.zone + "." + c.name
		if z, ok := LegacyZoneOfMailFilter(filter, c.owner, c.name); !ok || z != c.zone {
			t.Errorf("LegacyZoneOfMailFilter(%q) = %q %v", filter, z, ok)
		}
		subj := "agents.mail.cc." + c.owner + "." + c.zone + "." + c.name
		if z, ok := LegacyZoneOfMailSubject(subj, c.owner, c.name); !ok || z != c.zone {
			t.Errorf("LegacyZoneOfMailSubject(%q) = %q %v", subj, z, ok)
		}
		if _, ok := LegacyZoneOfMailFilter(MailFilter(c.owner, c.name), c.owner, c.name); ok {
			t.Errorf("five-token filter parsed as zoned")
		}
		if _, ok := LegacyZoneOfMailSubject(For(c.owner, c.name).Mail, c.owner, c.name); ok {
			t.Errorf("five-token subject parsed as zoned")
		}
	}
}

func TestGoldenSplitMailRefusals(t *testing.T) {
	for _, s := range []string{
		"", "agents.mail.cc.o", "agents.mail.cc.o.z.a", "agents.mail.cc.o.z.a.x", "agents.prompt.cc.o.a", "agent.mail.cc.o.a",
		"agents.mail.cc..a", "agents.mail.cc.o.", "agents.mail.cc.o.z.",
	} {
		if _, _, ok := SplitMail(s); ok {
			t.Errorf("SplitMail(%q) accepted", s)
		}
	}
	if _, ok := LegacyZoneOfMailFilter("agents.mail.*.o.Z.a", "o", "a"); ok {
		t.Error("LegacyZoneOfMailFilter accepted an invalid zone")
	}
	if _, ok := LegacyZoneOfMailFilter("agents.mail.*.o.z.b", "o", "a"); ok {
		t.Error("LegacyZoneOfMailFilter accepted another name")
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
		ProtocolVersion != "0.3" {
		t.Error("a protocol constant changed")
	}
}
