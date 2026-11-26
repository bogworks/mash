package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"

	"github.com/bogworks/mash/internal/sap"
)

// The goldens live beside the code that builds the line (internal/endpoint, which tests the real service's refusals).
// This runs the real CLI (run, as main does) with `mash prompt` against a target that is discovered like a real one
// and refuses with each code; the actual stdout and stderr are compared with the goldens: stdout is empty and stderr
// is exactly the golden (the plain-text warning line, which is not part of the golden, is taken out first).
func TestPromptRefusalReachesStderrUnchanged(t *testing.T) {
	for _, code := range []string{"400", "401", "403"} {
		t.Run(code, func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join("..", "..", "internal", "endpoint", "testdata", "prompt_refused_"+code+".golden"))
			if err != nil {
				t.Fatal(err)
			}
			text := strings.TrimSuffix(strings.TrimPrefix(string(want), "mash: prompt refused: "+code+" "), "\n")
			w := newWorld(t, "alpha", "beta")
			target := w.e.ConnAs(t, w.env["beta"]["MASH_CREDS"], "beta")
			subj := mustSubj(tAddr("beta")).Prompt
			// Discovery verifies a registration (REQ-016), so the target registers as a real endpoint does: its own
			// user, account, agent and owner, and an id_sig over them and the prompt subject.
			credsData, err := os.ReadFile(w.env["beta"]["MASH_CREDS"])
			if err != nil {
				t.Fatal(err)
			}
			id, err := sap.IdentityFromCreds(credsData)
			if err != nil {
				t.Fatal(err)
			}
			idSig, err := id.IDSig(sap.AgentName, w.e.Owner, subj)
			if err != nil {
				t.Fatal(err)
			}
			svc, err := micro.AddService(target, micro.Config{
				Name: sap.ServiceName, Version: "0.0.1",
				Metadata: map[string]string{"owner": w.e.Owner, "agent": sap.AgentName, "user_nkey": id.User,
					"account": id.Account, "id_sig": idSig},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = svc.Stop() })
			if err := svc.AddEndpoint("prompt", micro.HandlerFunc(func(r micro.Request) {
				em := nats.NewMsg(r.Reply())
				em.Header.Set("Nats-Service-Error-Code", code)
				em.Header.Set("Nats-Service-Error", text)
				_ = target.PublishMsg(em)
				_ = target.Publish(r.Reply(), nil)
			}), micro.WithEndpointSubject(subj), micro.WithEndpointQueueGroup(sap.QueueGroup)); err != nil {
				t.Fatal(err)
			}
			if err := target.Flush(); err != nil {
				t.Fatal(err)
			}

			var stdout, stderr bytes.Buffer
			exit := run([]string{"prompt", tTo("beta"), "hello"}, envFrom(w.env["alpha"]), strings.NewReader(""), &stdout, &stderr)
			if exit == 0 {
				t.Fatalf("exit 0; stdout %q stderr %q", stdout.String(), stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout %q", stdout.String())
			}
			if got := withoutPlainWarning(stderr.String()); got != string(want) {
				t.Errorf("stderr %q, want %q", got, want)
			}
		})
	}
}
