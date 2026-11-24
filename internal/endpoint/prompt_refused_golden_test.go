package endpoint

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/sap"
)

// These goldens pin the line mash prompt prints when the target refuses, as main printed it at the S2 capture
// (2f9a36f8): promptSend builds "mash: prompt refused: <code> <text>" and cmd/mash/cli.go prints it with Fprintln.
// The target here answers as (*Endpoint).end does: a message with the error headers, then the empty terminator. The
// texts are those of service.go. (The capture's encryption warning line is not part of the golden.)
// NEEDS A NATS SERVER (testenv.Start, in process): written, not run on the host that wrote it.
func TestPromptRefusedGoldens(t *testing.T) {
	for _, tc := range []struct{ code, text string }{
		{"400", "malformed Agent-Sender header"},
		{"401", "a signed Agent-Sender header is required (min_sender_trust=signed)"},
		{"403", "the sender is not a mash endpoint"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			f := newFixture(t, "a", "b")
			b := f.client(t, "b")
			ta := f.e.ConnAs(t, f.creds["a"], tkey("a"))
			subj := taddr("a").Subjects().Prompt
			if _, err := ta.QueueSubscribe(subj, sap.QueueGroup, func(m *nats.Msg) {
				em := nats.NewMsg(m.Reply)
				em.Header.Set("Nats-Service-Error-Code", tc.code)
				em.Header.Set("Nats-Service-Error", tc.text)
				_ = ta.PublishMsg(em)
				_ = ta.Publish(m.Reply, nil)
			}); err != nil {
				t.Fatal(err)
			}
			if err := ta.Flush(); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			err := promptSend(context.Background(), b.Conn(), b.Identity(), tkey("b"), subj, "hello", &out)
			if err == nil {
				t.Fatal("no refusal")
			}
			want, rerr := os.ReadFile(filepath.Join("testdata", "prompt_refused_"+tc.code+".golden"))
			if rerr != nil {
				t.Fatal(rerr)
			}
			// cmd/mash/cli.go: fmt.Fprintln(stderr, err)
			if got := err.Error() + "\n"; got != string(want) {
				t.Errorf("got %q, want %q", got, want)
			}
			if out.Len() != 0 {
				t.Errorf("stdout %q", out.String())
			}
		})
	}
}
