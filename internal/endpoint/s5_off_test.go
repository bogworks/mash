package endpoint

// The 0.5.x regression of S5: with the switch off (s5Switch nil, as in every 0.5.x command), discovery is main's own.
// Presence uses the client's NewInbox for its reply subject, the recipient discovery of prompt takes the first answer
// that names the prompt subject (with no directory record read), and nothing is counted.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/sap"
)

func TestS5Off_DiscoveryIsMains(t *testing.T) {
	if s5Hook() != nil {
		t.Fatal("the S5 switch is set at the start of a 0.5.x test")
	}
	z := newS5Fix(t)
	forged := z.forgeries(t, z.genuineRegistration(t, z.bob))
	tp := z.tap(t, "$SRV.INFO."+sap.ServiceName)
	alice, err := OpenClientWith(context.Background(), z.cfgAt(z.alice), ClientOptions{FullDirectory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(alice.Close)

	// presence: the reply subject is the one NewInbox gives (prefix and one nuid token)
	if err := alice.awaitPresence(context.Background(), alice.startPresence()); err != nil {
		t.Fatal(err)
	}
	var reply string
	s5Wait(t, "the presence request", 5*time.Second, func() bool {
		for _, m := range tp.all() {
			reply = m.Reply
		}
		return reply != ""
	})
	if want := len(alice.nc.NewInbox()); len(reply) != want {
		t.Errorf("the presence reply subject %q is %d long, want %d (the length NewInbox gives)", reply, len(reply), want)
	}
	if tok := reply[strings.LastIndex(reply, ".")+1:]; len(tok) != 22 {
		t.Errorf("the presence reply token %q is %d characters, want the 22 of a nuid", tok, len(tok))
	}

	// a forged answer is judged as main judges it, and nothing is counted
	for _, f := range forged {
		alice.applyPresence(&nats.Msg{Data: f.Data})
	}
	if n := alice.AnswersNotVerified(); n != 0 {
		t.Errorf("the count is %d after forged answers, want 0 while the switch is off", n)
	}
}

func TestS5Off_PromptDiscoveryWithoutARecord(t *testing.T) {
	if s5Hook() != nil {
		t.Fatal("the S5 switch is set at the start of a 0.5.x test")
	}
	z := newS5Fix(t)
	bob := z.connect(t, z.bob)
	subj := mustSubj(z.bob).Prompt
	body, _ := json.Marshal(map[string]any{"endpoints": []map[string]string{{"name": "prompt", "subject": subj}}})
	if _, err := bob.nc.Subscribe("$SRV.INFO."+sap.ServiceName, func(m *nats.Msg) { _ = m.Respond(body) }); err != nil {
		t.Fatal(err)
	}
	if err := bob.nc.Flush(); err != nil {
		t.Fatal(err)
	}
	alice, err := OpenClientWith(context.Background(), z.cfgAt(z.alice), ClientOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(alice.Close)
	if _, ok := alice.dir[z.bob.Flat()]; ok {
		t.Fatal("the client holds bob's record: the test needs a recipient with no directory record")
	}
	got, err := alice.DiscoverRecipient(context.Background(), z.bob.Flat())
	if err != nil || got != subj {
		t.Fatalf("discovery of an unsigned answer with no record: %q, %v, want %q (main's subject match)", got, err, subj)
	}
	if _, ok := alice.dir[z.bob.Flat()]; ok {
		t.Error("the discovery read bob's record while the switch is off")
	}
	if n := alice.AnswersNotVerified(); n != 0 {
		t.Errorf("the count is %d, want 0", n)
	}
}
