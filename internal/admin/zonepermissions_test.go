package admin

import (
	"slices"
	"strings"
	"testing"
)

func TestZonePermissions(t *testing.T) {
	p := ZonePermissions("acme", "red", []string{"blue", "green"})
	for _, w := range []string{
		"agents.mail.*.acme.blue.*", "agents.mail.*.acme.green.*", "agents.mail.*.acme.{{tag(allow)}}",
		// Prompt and status are open and five-token (ADR 0054): one fixed line each, no zone, no tag.
		"agents.prompt.*.acme.*", "agents.status.*.acme.*",
		"agents.hb.cc.acme.{{name()}}", "$KV.mash-leases.{{name()}}", "$JS.ACK.inbox-{{name()}}.deliver.>",
	} {
		if !slices.Contains(p.Pub.Allow, w) {
			t.Errorf("pub allow lacks %s: %v", w, p.Pub.Allow)
		}
	}
	for _, w := range []string{"_INBOX.{{name()}}.>", "agents.prompt.cc.acme.{{name()}}", "agents.status.cc.acme.{{name()}}", "agents.hb.*.acme.*"} {
		if !slices.Contains(p.Sub.Allow, w) {
			t.Errorf("sub allow lacks %s: %v", w, p.Sub.Allow)
		}
	}
	if len(p.Pub.Deny) != 0 {
		t.Errorf("deny lines without ZonePermissionsWithDeny: %v", p.Pub.Deny)
	}
	for _, s := range p.Pub.Allow {
		if (strings.HasPrefix(s, "agents.prompt.") || strings.HasPrefix(s, "agents.status.")) && s != "agents."+strings.Split(s, ".")[1]+".*.acme.*" {
			t.Errorf("a prompt or status line that is not the open five-token line: %s", s)
		}
		if strings.HasPrefix(s, "agents.mail.*.acme.") && !strings.Contains(s, "{{") && !strings.HasSuffix(s, ".*") {
			t.Errorf("a literal recipient in a zone key: %s", s)
		}
	}
	d := ZonePermissionsWithDeny("acme", "red", nil)
	if !slices.Equal(d.Pub.Deny, []string{"agents.mail.*.acme.{{tag(deny)}}"}) {
		t.Errorf("deny: %v", d.Pub.Deny)
	}
	if len(ZonePermissions("acme", "red", nil).Pub.Allow) == 0 {
		t.Error("empty block")
	}
	if NoDenyTag != "deny:0" {
		t.Error(NoDenyTag)
	}
}
