package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
)

// The goldens live beside the code that builds the line (internal/endpoint, which tests the real service's refusals).
// This runs the real CLI (run, as main does) with `mash prompt` against a target that is discovered like a real one
// and refuses with each code; the actual stdout and stderr are compared with the goldens: stdout is empty and stderr
// is the golden in style A (CHG-17, TO-10, TO-11): the "mash: " prefix of the endpoint's text becomes "error: ", the
// words stay, and no hint follows a refusal by the peer (the plain-text warning line, which is not part of the golden,
// is taken out first).
func TestPromptRefusalReachesStderrInStyleA(t *testing.T) {
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
			svc, err := micro.AddService(target, micro.Config{
				Name: sap.ServiceName, Version: "0.0.1",
				Metadata: map[string]string{"owner": w.e.Owner},
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
			if got, wantA := withoutPlainWarning(stderr.String()), "error: "+strings.TrimPrefix(string(want), "mash: "); got != wantA {
				t.Errorf("stderr %q, want %q", got, wantA)
			}
		})
	}
}

// CHG-17: every error line of mash prompt, with its hint and exit code.
func TestPromptErrorLinesAreStyleA(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"malformed", "mash: prompt refused: 400 malformed Agent-Sender header", "error: prompt refused: 400 malformed Agent-Sender header\n"},
		{"cap", "mash: prompt refused: 401 too many recent prompts: try again shortly", "error: prompt refused: 401 too many recent prompts: try again shortly\n"},
		{"too large", "mash: prompt refused: 413 prompt too large (this one is 9)", "error: prompt refused: 413 prompt too large (this one is 9)\n"},
		{"not online", "mash: o.dev.zed is not online (no SAP registration answered)", "error: o.dev.zed is not online (no SAP registration answered)\nhint: mash who\n"},
		{"connect", "mash: connect nats://h:4222: refused", "error: connect nats://h:4222: refused\nhint: mash doctor\n"},
		{"credentials", "mash: credentials: no such file", "error: credentials: no such file\nhint: mash doctor\n"},
		{"not sent", "not sent: could not sign: your name is too long", "error: not sent: could not sign: your name is too long\n"},
	} {
		var stdout, stderr bytes.Buffer
		if c := cliframe.Report(&stdout, &stderr, promptFailure(errors.New(tc.in)), false); c != 1 {
			t.Errorf("%s: exit %d, want 1", tc.name, c)
		}
		if stderr.String() != tc.want || stdout.Len() != 0 {
			t.Errorf("%s: stderr %q stdout %q, want stderr %q", tc.name, stderr.String(), stdout.String(), tc.want)
		}
		if strings.HasPrefix(stderr.String(), "mash: ") || strings.HasPrefix(stderr.String(), "usage:") {
			t.Errorf("%s: old style: %q", tc.name, stderr.String())
		}
	}
}

// The usage line is style A, exit 2, and comes before anything is opened (no endpoint is configured here).
func TestPromptUsageIsStyleAExit2(t *testing.T) {
	for _, args := range [][]string{{"prompt"}, {"prompt", "o.dev.zed"}} {
		var stdout, stderr bytes.Buffer
		code := cli(config.Config{}, args, strings.NewReader(""), &stdout, &stderr)
		want := "error: mash prompt needs <to> and the message text\nhint: mash prompt --help\n"
		if code != 2 || stderr.String() != want || stdout.Len() != 0 {
			t.Errorf("%v: exit %d, stderr %q, stdout %q", args, code, stderr.String(), stdout.String())
		}
	}
}
