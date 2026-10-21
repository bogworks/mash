package admin

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPermissionsExact(t *testing.T) {
	p := Permissions("o", "a")
	wantPub := []string{
		"$JS.ACK.inbox-a.deliver.>",
		"$JS.API.CONSUMER.CREATE.KV_mash-directory.>",
		"$JS.API.CONSUMER.INFO.inbox-a.deliver",
		"$JS.API.CONSUMER.MSG.NEXT.inbox-a.deliver",
		"$JS.API.DIRECT.GET.KV_mash-directory.>",
		"$JS.API.DIRECT.GET.inbox-a",
		"$JS.API.DIRECT.GET.inbox-a.>",
		"$JS.API.INFO",
		"$JS.API.STREAM.INFO.KV_mash-directory",
		"$JS.API.STREAM.INFO.KV_mash-leases",
		"$JS.API.STREAM.INFO.inbox-a",
		"$JS.FC.KV_mash-directory.>",
		"$KV.mash-leases.a",
		"$SRV.>",
		"agents.hb.cc.o.a",
		"agents.mail.*.o.*",
		"agents.prompt.*.o.*",
		"agents.status.*.o.*",
	}
	wantSub := []string{
		"$SRV.>",
		"_INBOX.>",
		"agents.hb.*.o.*",
		"agents.prompt.cc.o.a",
		"agents.status.cc.o.a",
	}
	gotPub := slices.Sorted(slices.Values([]string(p.Pub.Allow)))
	gotSub := slices.Sorted(slices.Values([]string(p.Sub.Allow)))
	if !slices.Equal(gotPub, wantPub) {
		t.Errorf("pub\n got %q\nwant %q", gotPub, wantPub)
	}
	if !slices.Equal(gotSub, wantSub) {
		t.Errorf("sub\n got %q\nwant %q", gotSub, wantSub)
	}
	if len(p.Pub.Deny) != 0 || len(p.Sub.Deny) != 0 {
		t.Errorf("unexpected deny lists")
	}
	if p.Resp == nil || p.Resp.MaxMsgs != 1000 || p.Resp.Expires != 15*time.Minute {
		t.Errorf("resp %+v; want 1000 messages for 15m (SAP prompt streams chunks)", p.Resp)
	}
}

func TestPermissionsNeverGrantOthers(t *testing.T) {
	p := Permissions("o", "a")
	for _, s := range append([]string(p.Pub.Allow), p.Sub.Allow...) {
		if s == ">" || s == "$JS.API.>" || strings.HasPrefix(s, "$JS.API.STREAM.DELETE") ||
			strings.HasPrefix(s, "$JS.API.STREAM.PURGE") || strings.HasPrefix(s, "$JS.API.STREAM.MSG.DELETE") {
			t.Errorf("over-broad grant %q", s)
		}
		if strings.Contains(s, "inbox-") && !strings.Contains(s, "inbox-a") {
			t.Errorf("grant on another inbox: %q", s)
		}
		if strings.Contains(s, "$KV.mash-directory") {
			t.Errorf("directory must be read-only: %q", s)
		}
	}
}

func TestNSCFlags(t *testing.T) {
	f := strings.Join(NSCFlags("o", "a"), " ")
	for _, want := range []string{
		"--allow-pub agents.mail.*.o.*",
		"--allow-sub agents.prompt.cc.o.a",
		"--allow-pub-response=1000",
		"--response-ttl 15m",
	} {
		if !strings.Contains(f, want) {
			t.Errorf("flags missing %q:\n%s", want, f)
		}
	}
}
