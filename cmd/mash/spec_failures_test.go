package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/admin"
)

// The failure cases of send, inbox, read, who and doctor that the specification words: each is run against a real
// server and compared with spec/mash.json word for word. The specification's example addresses (acme.demo.alice,
// acme.demo.bob) stand for the test world's endpoints b and a.

// specText is the case's text with the specification's addresses replaced by the world's.
func specText(c specCase) string {
	s := strings.NewReplacer("acme.demo.alice", tTo("b"), "acme.demo.bob", tTo("a")).Replace(c.Text)
	return s
}

func adminOf(t *testing.T, w *world) *admin.Admin {
	t.Helper()
	adm, err := admin.New(w.e.Conn(t, w.e.AdminCreds), w.e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	return adm
}

// split is the spec text as stdout and stderr (the case's stream says which one it is).
func (c specCase) streams(text string) (out, errs string) {
	if c.Stream == "stdout" {
		return text + "\n", ""
	}
	return "", text + "\n"
}

func checkCase(t *testing.T, c specCase, text string, code int, out, errs string) {
	t.Helper()
	wantOut, wantErr := c.streams(text)
	if code != c.Exit || out != wantOut || errs != wantErr {
		t.Errorf("%s: want exit %d, stdout %q, stderr %q; got exit %d, stdout %q, stderr %q", c.Case, c.Exit, wantOut, wantErr, code, out, errs)
	}
}

// A send to a suspended endpoint says so, in the specification's words, from the command line and from the tool.
func TestSendToSuspendedMatchesSpec(t *testing.T) {
	c, ok := specCases(t, "send")["The recipient is suspended or was deleted"]
	if !ok {
		t.Fatal("spec/mash.json no longer has the send case \"The recipient is suspended\"")
	}
	w := tlsWorld(t, "a", "b")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := adminOf(t, w).Suspend(ctx, tAddr("b")); err != nil {
		t.Fatal(err)
	}
	text := specText(c)
	r := runMash(strictEnv(w, "a", nil), "send", tTo("b"), "hello")
	checkCase(t, c, text, r.code, r.out, r.errs)
	for _, bad := range []string{"nats", "responders", "stream"} {
		if strings.Contains(strings.ToLower(r.errs), bad) {
			t.Errorf("the NATS wording %q is shown: %q", bad, r.errs)
		}
	}

	s := startServe(t, w.env["a"])
	s.initialize(t)
	res := s.call(t, 71, "send", `{"address":"`+tTo("b")+`","message":"hello"}`)
	sc, _ := res["structuredContent"].(map[string]any)
	errLine, hint, _ := strings.Cut(text, "\nhint: ")
	if res["isError"] != true || sc["error"] != strings.TrimPrefix(errLine, "error: ") || sc["hint"] != hint || resultText(res) != text {
		t.Errorf("send tool: want error %q hint %q text %q, got %v", errLine, hint, text, res)
	}
}

// A creds file that is missing is said in the specification's words by every command that signs in.
func TestMissingCredsMatchesSpec(t *testing.T) {
	c, ok := specCases(t, "send")["No credentials here"]
	if !ok {
		t.Fatal("spec/mash.json no longer has the send case \"No credentials here\"")
	}
	w := tlsWorld(t, "a", "b")
	env := strictEnv(w, "a", nil)
	delete(env, "MASH_CREDS") // the default: <config folder>/<address>.creds, which is not there
	text := specText(c)
	if !strings.Contains(text, "~/.config/mash/"+tTo("a")+".creds is missing") {
		t.Fatalf("the spec text has changed shape: %q", text)
	}
	for _, args := range [][]string{{"send", tTo("b"), "hello"}, {"inbox"}, {"read", "1"}, {"who"}} {
		t.Run(args[0], func(t *testing.T) {
			r := runMash(env, args...)
			checkCase(t, c, text, r.code, r.out, r.errs)
		})
	}
	// the same with the creds named by MASH_CREDS and by --creds
	env["MASH_CREDS"] = filepath.Join(env["HOME"], "elsewhere", "x.creds")
	want := strings.Replace(text, "~/.config/mash/"+tTo("a")+".creds", "~/elsewhere/x.creds", 1)
	if r := runMash(env, "inbox"); r.code != 1 || r.out != "" || r.errs != want+"\n" {
		t.Errorf("MASH_CREDS: %d %q %q, want %q", r.code, r.out, r.errs, want)
	}
	// A session whose creds are missing offers only version, status and doctor (tools/degraded in the conformance test): the
	// send, inbox, read and who tools do not exist there, so they cannot answer with this failure.
}

// doctor after the endpoint was deleted says so in the specification's words, with the fix for a deleted endpoint.
func TestDoctorDeletedEndpointMatchesSpec(t *testing.T) {
	c, ok := specCases(t, "doctor")["The endpoint was deleted"]
	if !ok {
		t.Fatal("spec/mash.json no longer has the doctor case \"The endpoint was deleted\"")
	}
	w := tlsWorld(t, "a")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := adminOf(t, w).Deprovision(ctx, tAddr("a")); err != nil {
		t.Fatal(err)
	}
	// the spec's example shows the server as tls://myserver:4222: here it is the test server's address
	text := strings.Replace(specText(c), "tls://myserver:4222", w.e.URL, 1)
	stdout, hint, _ := strings.Cut(text, "\nhint: ")
	r := runMash(strictEnv(w, "a", nil), "doctor")
	if r.code != c.Exit || r.out != stdout+"\n" || r.errs != "hint: "+hint+"\n" {
		t.Errorf("doctor: want exit %d, stdout %q, stderr %q; got exit %d, stdout %q, stderr %q", c.Exit, stdout+"\n", "hint: "+hint+"\n", r.code, r.out, r.errs)
	}
	// the tool says the same
	s := startServe(t, w.env["a"])
	s.initialize(t)
	if got := reportText(t, s, 90, "doctor"); !strings.Contains(got, strings.SplitN(stdout, "\n", 5)[3]) || !strings.HasSuffix(got, "hint: "+hint) {
		t.Errorf("doctor tool: %q", got)
	}
}

// The inbox counts only the messages it lists: mail whose sender was deleted after sending is not shown, so it is not
// in the total or the new count, and an inbox with nothing to show is the specification's empty one.
func TestInboxIgnoresMailFromADeletedSender(t *testing.T) {
	c, ok := specCases(t, "inbox")["Empty"]
	if !ok {
		t.Fatal("spec/mash.json no longer has the inbox case \"Empty\"")
	}
	w := tlsWorld(t, "a", "b")
	if r := runMash(strictEnv(w, "b", nil), "send", tTo("a"), "hello"); r.code != 0 {
		t.Fatalf("send: %+v", r)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := adminOf(t, w).Deprovision(ctx, tAddr("b")); err != nil {
		t.Fatal(err)
	}
	text := strings.NewReplacer("acme.demo.alice", tTo("a")).Replace(c.Text)
	r := runMash(strictEnv(w, "a", nil), "inbox")
	checkCase(t, c, text, r.code, r.out, r.errs)

	r = runMash(strictEnv(w, "a", nil), "inbox", "--json")
	if r.code != 0 || !strings.Contains(r.out, `"total":0`) || !strings.Contains(r.out, `"new":0`) || !strings.Contains(r.out, `"messages":[]`) {
		t.Errorf("inbox --json: exit %d, stdout %q, stderr %q", r.code, r.out, r.errs)
	}
}

// status on the machine that deleted the endpoint: mashctl set the creds file aside as <creds>.deleted, so the creds
// row says the endpoint was deleted, in the specification's words. The endpoint is named, as it is no longer installed.
func TestStatusDeletedEndpointMatchesSpec(t *testing.T) {
	const name = "After the endpoint was deleted, on the machine that deleted it"
	c, ok := specCases(t, "status")[name]
	if !ok {
		t.Fatalf("spec/mash.json no longer has the status case %q", name)
	}
	w := tlsWorld(t, "a")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := adminOf(t, w).Deprovision(ctx, tAddr("a")); err != nil {
		t.Fatal(err)
	}
	creds := w.env["a"]["MASH_CREDS"]
	if err := os.Rename(creds, creds+".deleted"); err != nil {
		t.Fatal(err)
	}
	text := specText(c)
	r := runMash(strictEnv(w, "a", nil), "status", "--endpoint", tTo("a"))
	stdout, hint, _ := strings.Cut(text, "\nhint: ")
	if r.code != c.Exit || r.out != stdout+"\n" || r.errs != "hint: "+hint+"\n" {
		t.Errorf("status: want exit %d, stdout %q, stderr %q; got exit %d, stdout %q, stderr %q", c.Exit, stdout+"\n", "hint: "+hint+"\n", r.code, r.out, r.errs)
	}
	_, reason, _ := strings.Cut(stdout, "not ready: ")
	r = runMash(strictEnv(w, "a", nil), "status", "--json")
	want := `{"endpoint":"` + tTo("a") + `","ready":false,"online":false,"reason":"` + reason + `"}` + "\n"
	if r.code != 1 || r.out != want {
		t.Errorf("status --json: want exit 1, stdout %q; got exit %d, stdout %q, stderr %q", want, r.code, r.out, r.errs)
	}
	r = runMash(strictEnv(w, "a", nil), "status", "--json", "--detail")
	if r.code != 1 || !strings.Contains(r.out, `"reason_code":"endpoint_deleted"`) {
		t.Errorf("status --json --detail: exit %d, stdout %q, stderr %q", r.code, r.out, r.errs)
	}

	// the tool says the same
	s := startServe(t, w.env["a"])
	s.initialize(t)
	if got := reportText(t, s, 91, "status"); got != text {
		t.Errorf("status tool: %q, want %q", got, text)
	}
}
