package endpoint

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/sap"
)

// These goldens pin the line mash prompt prints when the target refuses, as main printed it at the S2 capture
// (2f9a36f8): promptSend builds "mash: prompt refused: <code> <text>" and cmd/mash/cli.go prints it with Fprintln.
// The target is a real served endpoint (handlePrompt), so the refusal text is the one service.go produces.
// 403 goes through promptSend itself (a sender whose signed name is not its own). A client cannot make promptSend send
// a malformed or an absent header, so 400 and 401 are raw requests to the same service and the line is built from the
// reply headers exactly as promptSend does. (The capture's encryption warning line is not part of the golden.)
// NEEDS A NATS SERVER (testenv.Start, in process).
func TestPromptRefusedGoldens(t *testing.T) {
	for _, tc := range []struct{ code, hdr string }{
		{"400", "not json"},
		{"401", ""},
		{"403", "name"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			f := newFixture(t, "a", "b")
			f.serve(t, "a", func(*Endpoint, PromptEvent) { t.Error("a refused prompt reached the session") })
			b := f.client(t, "b")
			subj := mustSubj(taddr("a")).Prompt
			var got string
			var out bytes.Buffer
			if tc.code == "403" {
				err := promptSend(context.Background(), b.Conn(), b.Identity(), tkey("c"), subj, "hello", &out)
				if err == nil {
					t.Fatal("no refusal")
				}
				got = err.Error()
			} else {
				got = rawRefusal(t, b.Conn(), subj, tc.hdr)
			}
			want, rerr := os.ReadFile(filepath.Join("testdata", "prompt_refused_"+tc.code+".golden"))
			if rerr != nil {
				t.Fatal(rerr)
			}
			// cmd/mash/cli.go: fmt.Fprintln(stderr, err)
			if got+"\n" != string(want) {
				t.Errorf("got %q, want %q", got+"\n", want)
			}
			if out.Len() != 0 {
				t.Errorf("stdout %q", out.String())
			}
		})
	}
}

// rawRefusal sends "hello" to subj with the given Agent-Sender header (none when empty) and returns the refusal as
// promptSend words it.
func rawRefusal(t *testing.T, nc *nats.Conn, subj, hdr string) string {
	t.Helper()
	inbox := nc.NewInbox()
	sub, err := nc.SubscribeSync(inbox)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	m := nats.NewMsg(subj)
	m.Reply = inbox
	m.Data = []byte("hello")
	if hdr != "" {
		m.Header.Set(sap.HeaderName, hdr)
	}
	if err := nc.PublishMsg(m); err != nil {
		t.Fatal(err)
	}
	r, err := sub.NextMsg(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	code := r.Header.Get("Nats-Service-Error-Code")
	if code == "" {
		t.Fatalf("no refusal: %q", r.Data)
	}
	return fmt.Sprintf("mash: prompt refused: %s %s", code, r.Header.Get("Nats-Service-Error"))
}
