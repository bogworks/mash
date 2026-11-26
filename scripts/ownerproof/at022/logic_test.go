package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/sapsdk"
)

func TestParseSums(t *testing.T) {
	h := strings.Repeat("a", 64)
	m, err := ParseSums(h + "  mash-linux-amd64\n\n" + h + " *other\n")
	if err != nil || m["mash-linux-amd64"] != h || m["other"] != h {
		t.Fatalf("%v, %v", m, err)
	}
	if _, err := ParseSums("short  x\n"); err == nil {
		t.Fatal("a short digest was accepted")
	}
}

func TestSamePermissions(t *testing.T) {
	a, _ := sap.NewAddress("acme", "ops", "alice")
	want := admin.Permissions(a)
	got := admin.Permissions(a)
	// order does not matter
	got.Pub.Allow[0], got.Pub.Allow[1] = got.Pub.Allow[1], got.Pub.Allow[0]
	if p := SamePermissions("alice", got, want, nil); p != "" {
		t.Fatal(p)
	}
	got.Pub.Allow.Add("agents.mail.zeta.*.*")
	if p := SamePermissions("alice", got, want, nil); !strings.Contains(p, "publish allow") {
		t.Fatalf("an extra publish grant was not found: %q", p)
	}
	got = admin.Permissions(a)
	got.Sub.Deny.Add("x")
	if p := SamePermissions("alice", got, want, nil); !strings.Contains(p, "subscribe deny") {
		t.Fatalf("a deny entry was not found: %q", p)
	}
	if p := SamePermissions("alice", want, want, &jwt.ResponsePermission{}); !strings.Contains(p, "response permission") {
		t.Fatalf("a response permission was not found: %q", p)
	}
	if p := SamePermissions("alice", nil, want, nil); p == "" {
		t.Fatal("a missing set passed")
	}
}

func TestViolated(t *testing.T) {
	errs := []string{`nats: permissions violation for subscription to "_INBOX.acme.x.abc"`, `nats: Permissions Violation for Publish to "_INBOX.acme.x.abc"`}
	if !Violated(errs, "subscription", "_INBOX.acme.x.abc") || !Violated(errs, "publish", "_INBOX.acme.x.ABC") {
		t.Fatal("a violation that names the subject was missed")
	}
	if Violated(errs, "subscription", "_INBOX.acme.x.other") || Violated(errs[:1], "publish", "_INBOX.acme.x.abc") {
		t.Fatal("a violation for another subject or kind was accepted")
	}
	if len(Violations(append(errs, "nats: slow consumer"))) != 2 {
		t.Fatal("violations miscounted")
	}
}

func TestIncrementHex(t *testing.T) {
	for _, c := range []struct {
		in   string
		n    int
		want string
	}{{"00ff", 1, "0100"}, {"ffff", 1, "0000"}, {"0a", 5, "0f"}, {"0a", 6, "10"}} {
		if got := IncrementHex(c.in, c.n); got != c.want {
			t.Errorf("IncrementHex(%s, %d) = %s, want %s", c.in, c.n, got, c.want)
		}
	}
}

func TestNextNUIDs(t *testing.T) {
	tok := "ABCDEFGHIJKL" + "000000000z"
	got := NextNUIDs(tok, 1, 2)
	if len(got) != 2 || got[0] != "ABCDEFGHIJKL0000000010" || got[1] != "ABCDEFGHIJKL0000000011" {
		t.Fatalf("%v", got)
	}
	if NextNUIDs("short", 1, 2) != nil {
		t.Fatal("a token of the wrong length gave guesses")
	}
}

func TestGuesses(t *testing.T) {
	seen := "_INBOX.acme.acme_ops_alice." + strings.Repeat("0", 31) + "f"
	g := Guesses(seen)
	if len(g) != 1000 { // 700 NUID successors of the first 22 characters, 200 increments and 100 random tokens
		t.Fatalf("%d guesses", len(g))
	}
	pre := "_INBOX.acme.acme_ops_alice."
	next := pre + strings.Repeat("0", 30) + "10"
	found := false
	for _, s := range g {
		if !strings.HasPrefix(s, pre) || s == seen {
			t.Fatalf("bad guess %q", s)
		}
		found = found || s == next
	}
	if !found {
		t.Fatalf("the successor %q of the token is not among the guesses", next)
	}
	if Guesses("nodot") != nil {
		t.Fatal("a subject without a dot gave guesses")
	}
	if n := len(Guesses("_INBOX.acme.x.short")); n != 300 {
		t.Fatalf("a short token gave %d guesses, want 200 + 100", n)
	}
}

func TestParseWho(t *testing.T) {
	d, err := ParseWho([]byte(`{"endpoints":[{"address":"acme.ops.bob","online":true},{"address":"acme.ops.carol","online":false}],"answers_not_verified":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if l, o := d.Online("acme.ops.bob"); !l || !o {
		t.Fatal("bob")
	}
	if l, o := d.Online("acme.ops.carol"); !l || o {
		t.Fatal("carol")
	}
	if l, _ := d.Online("acme.ops.dave"); l {
		t.Fatal("dave listed")
	}
	if d.Count() != "2" {
		t.Fatal(d.Count())
	}
	d, _ = ParseWho([]byte(`{"endpoints":[]}`))
	if d.NotVerified != nil || d.Count() != "absent" {
		t.Fatal("a build that prints no count must read as absent, not as zero")
	}
	if _, err := ParseWho([]byte("not json")); err == nil {
		t.Fatal("bad JSON accepted")
	}
}

// genuineFor builds the registration bob's service would answer: his identity signs agent, owner and prompt subject.
func genuineFor(t *testing.T, bob sapsdk.Identity, subject string) []byte {
	t.Helper()
	sig, err := bob.IDSig("claude-code", "acme", subject)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{
		"name":      "agents",
		"endpoints": []map[string]string{{"name": "prompt", "subject": subject}},
		"metadata":  map[string]string{"agent": "claude-code", "owner": "acme", "user_nkey": bob.User, "account": bob.Account, "id_sig": sig},
	})
	return b
}

func identityOf(t *testing.T, account string) sapsdk.Identity {
	t.Helper()
	kp, _ := nkeys.CreateUser()
	seed, _ := kp.Seed()
	id, err := sapsdk.NewIdentity(seed, account)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestForgeries(t *testing.T) {
	acct, _ := nkeys.CreateAccount()
	apub, _ := acct.PublicKey()
	bob, carol := identityOf(t, apub), identityOf(t, apub)
	subject := "agents.prompt.claude-code.acme.ops.bob"
	genuine := genuineFor(t, bob, subject)
	check := func(data []byte) bool {
		var info struct {
			Metadata map[string]string `json:"metadata"`
		}
		if err := json.Unmarshal(data, &info); err != nil {
			t.Fatal(err)
		}
		md := info.Metadata
		return sapsdk.VerifyIDSig(md["user_nkey"], md["account"], md["agent"], md["owner"], subject, md["id_sig"])
	}
	if !check(genuine) {
		t.Fatal("the test's genuine registration does not verify")
	}
	forged, err := Forgeries(genuine, subject, carol)
	if err != nil {
		t.Fatal(err)
	}
	if len(forged) != 3 || forged[0].Name != "unsigned" || forged[1].Name != "forged-sig" || forged[2].Name != "wrong-key" {
		t.Fatalf("%+v", forged)
	}
	for _, f := range forged {
		want := f.Name == "wrong-key" // validly signed, by carol's key
		if got := check(f.Data); got != want {
			t.Errorf("%s verifies=%v, want %v", f.Name, got, want)
		}
		if s, err := PromptSubjectOf(f.Data); err != nil || s != subject {
			t.Errorf("%s names subject %q, %v: every forgery must name bob's prompt subject", f.Name, s, err)
		}
	}
	if strings.Contains(string(forged[0].Data), "id_sig") {
		t.Error("the unsigned forgery carries an id_sig")
	}
	var wk struct {
		Metadata map[string]string `json:"metadata"`
	}
	_ = json.Unmarshal(forged[2].Data, &wk)
	if wk.Metadata["user_nkey"] != carol.User || wk.Metadata["owner"] != "acme" {
		t.Errorf("wrong-key metadata %v", wk.Metadata)
	}
	if _, err := Forgeries([]byte(`{}`), subject, carol); err == nil {
		t.Error("a registration without metadata was accepted")
	}
}

func TestPromptSubjectOf(t *testing.T) {
	if _, err := PromptSubjectOf([]byte(`{"endpoints":[{"name":"status","subject":"x"}]}`)); err == nil {
		t.Fatal("no prompt endpoint, no error")
	}
}

func TestDiffItem4(t *testing.T) {
	a := Item4{Build: "candidate", Ran: true, PromptStdout: "CAROLgenuine", Subject: "s1"}
	b := Item4{Build: "oracle", Ran: true, PromptStdout: "CAROLgenuine", Subject: "s2"}
	if d := DiffItem4(a, b); len(d) != 0 {
		t.Fatalf("the random subject must not count: %v", d)
	}
	b.PromptStdout = "genuine"
	b.CarolViolations = 1
	if d := DiffItem4(a, b); len(d) != 2 {
		t.Fatalf("%v", d)
	}
	b.Ran = false
	if d := DiffItem4(a, b); len(d) != 3 {
		t.Fatalf("%v", d)
	}
}

func TestNormalStderr(t *testing.T) {
	if got := NormalStderr("warning: NATS is not encrypted\n\nerror: boom\n"); got != "error: boom" {
		t.Fatalf("%q", got)
	}
}

func TestErrorsIsSafe(t *testing.T) {
	var e Errors
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			e.Add("x")
		}
		close(done)
	}()
	for i := 0; i < 100; i++ {
		_ = e.All()
	}
	<-done
	if len(e.All()) != 100 {
		t.Fatal(len(e.All()))
	}
}
