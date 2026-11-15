package listset

import (
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// TestAT019LivePromptGetsItsDirectAnswer runs the one-way cross-group list set (corpus case 3: a.lead may prompt
// b.cve, nobody allows the reverse) on a live nats-server. TestLiveMatchesCheck covers who may send; this covers
// the answer: a live prompt from a.lead reaches b.cve, whose answer comes straight back on the reply subject
// in a.lead's own inbox space, while b.cve still cannot start a prompt of its own to a.lead.
func TestAT019LivePromptGetsItsDirectAnswer(t *testing.T) {
	if testing.Short() {
		t.Skip("live nats-server test")
	}
	const sender, recipient = "a.lead", "b.cve"
	ls := corpus()[3].ls
	c, err := Compile(ls)
	if err != nil {
		t.Fatal(err)
	}
	env := newLiveEnv(t, c)
	lead, _, err := env.user(c, sender)
	if err != nil {
		t.Fatal(err)
	}
	cve, _, err := env.user(c, recipient)
	if err != nil {
		t.Fatal(err)
	}

	// The recipient listens on its own prompt subject; the sender listens on a reply subject in its inbox space.
	prompts := make(chan *nats.Msg, 4)
	if _, err := cve.nc.ChanSubscribe(subj(recipient, Prompt), prompts); err != nil {
		t.Fatal(err)
	}
	reply := "_INBOX." + flat(sender) + ".AT019"
	answers, err := lead.nc.SubscribeSync(reply)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range []*conn{lead, cve} {
		if err := x.nc.Flush(); err != nil {
			t.Fatal(err)
		}
	}

	if err := lead.nc.PublishRequest(subj(recipient, Prompt), reply, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	if err := lead.nc.Flush(); err != nil {
		t.Fatal(err)
	}
	var got *nats.Msg
	select {
	case got = <-prompts:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not receive the prompt from %s", recipient, sender)
	}
	if got.Reply != reply || string(got.Data) != "ping" {
		t.Fatalf("prompt arrived as reply=%q data=%q", got.Reply, got.Data)
	}

	// The recipient answers on the reply subject; the sender receives it directly.
	if err := cve.nc.Publish(got.Reply, []byte("pong")); err != nil {
		t.Fatal(err)
	}
	if err := cve.nc.Flush(); err != nil {
		t.Fatal(err)
	}
	ans, err := answers.NextMsg(5 * time.Second)
	if err != nil {
		t.Fatalf("%s got no answer from %s (recipient violation on reply: %v): %v", sender, recipient, cve.violation("Publish", reply, time.Millisecond), err)
	}
	if string(ans.Data) != "pong" || ans.Subject != reply {
		t.Fatalf("answer arrived as subject=%q data=%q", ans.Subject, ans.Data)
	}

	// One way: the recipient cannot start a new prompt to the sender.
	back := subj(sender, Prompt)
	if err := cve.nc.PublishRequest(back, "_INBOX."+flat(recipient)+".AT019", []byte("ping")); err != nil {
		t.Fatal(err)
	}
	if err := cve.nc.Flush(); err != nil {
		t.Fatal(err)
	}
	if !cve.violation("Publish", back, 2*time.Second) {
		t.Errorf("%s publishing a new prompt to %s: no permissions violation", recipient, sender)
	}
}
