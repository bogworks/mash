package endpoint

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/sap"
)

// A message whose sender header is refused (malformed or forged) is not shown, is acked, is counted by reason class in
// Refused(), is logged with the class only, and stays in the inbox stream.
func TestRefusedMailIsCountedLoggedAndKept(t *testing.T) {
	f := newFixture(t, "a", "b")
	subj := sap.For(f.e.Owner, "a").Mail
	nc := f.e.ConnAs(t, f.creds["b"], "b")
	id := identity(t, f.creds["b"])
	prefix := fmt.Sprintf(`"v":1,"account":%q,"user":%q`, id.Account, id.User)
	zeroSig := base64.RawURLEncoding.EncodeToString(make([]byte, 64))

	signed, err := id.Sign(subj, []byte("original"), "b", time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	named := sap.SenderHeader{V: 1, Account: id.Account, User: id.User, Name: "a\x01b-SECRETNAME"}
	const secretBody = "SECRETBODY"
	for _, m := range []struct{ hdr, body string }{
		{"{" + prefix + `,"pad":"` + strings.Repeat("p", 2100) + `"}`, secretBody}, // size
		{"{" + prefix + `,"user":` + fmt.Sprintf("%q", id.User) + "}", secretBody}, // duplicate-member
		{named.Encode(), secretBody}, // name
		{"{" + prefix + fmt.Sprintf(`,"sub":%q,"ts":"yesterday","nonce":"n1","sig":%q}`, subj, zeroSig), secretBody}, // timestamp
		{signed.Encode(), secretBody}, // forged: signed for another payload
	} {
		publishRaw(t, nc, subj, m.hdr, m.body)
	}
	good, err := id.Sign(subj, []byte("good"), "b", time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	publishRaw(t, nc, subj, good.Encode(), "good")

	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	var logs lockedBuf
	ep.log = log.New(&logs, "", 0)
	ev := &events{}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, ev.deliver); close(done) }()
	t.Cleanup(func() { cancel(); <-done; ep.Close() })

	shown := strings.Join(ev.waitFor(t, 1, 5*time.Second), "\n")
	until(t, "five refusals counted", func() bool { return ep.dropped.Load() == 5 })
	until(t, "every message acked", func() bool { return unread(t, ep) == 0 })
	if strings.Contains(shown, secretBody) || !strings.Contains(shown, "good") {
		t.Fatalf("shown:\n%s", shown)
	}
	if n := len(ev.all()); n != 1 {
		t.Fatalf("%d messages shown, want 1", n)
	}
	total, by := ep.Refused()
	want := map[string]int64{"size": 1, "duplicate-member": 1, "name": 1, "timestamp": 1, "signature": 1}
	if total != 5 || !reflect.DeepEqual(by, want) || ep.Dropped() != 5 {
		t.Fatalf("Refused() = %d %v, Dropped() = %d; want 5 %v", total, by, ep.Dropped(), want)
	}
	by["size"] = 99
	if _, again := ep.Refused(); again["size"] != 1 {
		t.Fatal("Refused() returned the live map")
	}

	out := logs.String()
	for _, w := range []string{"reason size, subject " + subj, "reason duplicate-member", "reason name", "reason timestamp",
		"forged sender header, reason signature", "malformed sender header, reason size"} {
		if !strings.Contains(out, w) {
			t.Errorf("log lacks %q:\n%s", w, out)
		}
	}
	for _, leak := range []string{secretBody, "SECRETNAME", "ppppp", zeroSig} {
		if strings.Contains(out, leak) {
			t.Errorf("log leaks %q:\n%s", leak, out)
		}
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "malformed sender header") && strings.Contains(l, id.User) {
			t.Errorf("a malformed line names the claimed user: %s", l)
		}
		if strings.Contains(l, "forged sender header") && !strings.Contains(l, "claimed user "+id.User) {
			t.Errorf("a forged line lacks the claimed user: %s", l)
		}
	}

	// the inbox stream keeps all six
	js, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	s, err := js.Stream(context.Background(), sap.InboxStream("a"))
	if err != nil {
		t.Fatal(err)
	}
	if n := s.CachedInfo().State.Msgs; n != 6 {
		t.Fatalf("stream holds %d messages, want 6", n)
	}
	for seq := uint64(1); seq <= 5; seq++ {
		raw, err := s.GetMsg(context.Background(), seq)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw.Data) != secretBody {
			t.Errorf("#%d: body %q", seq, raw.Data)
		}
	}
}
