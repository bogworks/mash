package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/sap"
)

// The self-tests of AT-4: each check is shown to fail on input that lacks the protection and to pass on input that has
// it. None starts a server or podman.

var (
	testSeed = "SU" + strings.Repeat("A", 54) // the right shape only: not a key
	testJWT  = "eyJ" + strings.Repeat("a", 12) + ".payload12345.sig"
)

func TestScanArgvsCatchesAPlantedSecret(t *testing.T) {
	secrets := map[string][]byte{"design creds": []byte("a-registered-secret-text-that-is-long")}
	clean := []Argv{{"01-a", []string{"/bin/mash", "send", "alice.dev.review", "hello"}}, {"02-b", []string{"/bin/mashctl", "endpoint", "list", "--json"}}}
	if l := scanArgvs(clean, secrets); len(l) != 0 {
		t.Fatalf("a clean run was flagged: %v", l)
	}
	for name, arg := range map[string]string{
		"a seed-shaped string":       testSeed,
		"a JWT-shaped string":        testJWT,
		"a registered secret":        "--creds=a-registered-secret-text-that-is-long",
		"a seed inside a longer arg": "--seed=" + testSeed,
	} {
		planted := append(append([]Argv(nil), clean...), Argv{"03-bad", []string{"/bin/mash", "credential", "create", arg}})
		l := scanArgvs(planted, secrets)
		if len(l) == 0 || !strings.HasPrefix(l[0].File, "03-bad") {
			t.Errorf("%s in argv was not caught: %v", name, l)
		}
		for _, x := range l {
			if strings.Contains(x.File+x.Kind, testSeed) || strings.Contains(leakList(l), "a-registered-secret-text") {
				t.Errorf("%s: the report holds the match: %v", name, l)
			}
		}
	}
}

func TestScanResultsCatchesAPlantedSecretInOutput(t *testing.T) {
	secrets := map[string][]byte{"review seed": []byte(testSeed)}
	rs := []labelled{{"ok", Result{Stdout: "created\n", Stderr: "hint: mashctl endpoint list\n"}}}
	if l := scanResults(rs, nil); len(l) != 0 {
		t.Fatalf("a clean run was flagged: %v", l)
	}
	if l := scanResults(rs, secrets); len(l) != 0 {
		t.Fatalf("a clean run was flagged with secrets registered: %v", l)
	}
	for name, r := range map[string]Result{
		"seed on stdout":         {Stdout: "key " + testSeed + "\n"},
		"seed on stderr":         {Stderr: "error: cannot parse " + testSeed},
		"jwt on stderr":          {Stderr: "bad token " + testJWT},
		"registered, not shaped": {Stdout: "x"},
	} {
		secs := secrets
		if name == "registered, not shaped" {
			secs = map[string][]byte{"odd": []byte("x-odd-secret-text-not-shaped-like-a-key")}
			r.Stdout = "echo x-odd-secret-text-not-shaped-like-a-key"
		}
		l := scanResults(append(append([]labelled(nil), rs...), labelled{"bad", r}), secs)
		if len(l) == 0 || !strings.HasPrefix(l[0].File, "bad") {
			t.Errorf("%s was not caught: %v", name, l)
		}
	}
}

func TestScanProcCmdlines(t *testing.T) {
	root := t.TempDir()
	mk := func(pid, cmdline string) {
		d := filepath.Join(root, pid)
		os.Mkdir(d, 0o755)
		os.WriteFile(filepath.Join(d, "cmdline"), []byte(strings.ReplaceAll(cmdline, " ", "\x00")+"\x00"), 0o644)
	}
	mk("12", "nats-server -c /srv/server.conf")
	os.Mkdir(filepath.Join(root, "self"), 0o755) // not a pid
	l, n, err := scanProcCmdlines(root, nil)
	if err != nil || n != 1 || len(l) != 0 {
		t.Fatalf("clean: %v %d %v", l, n, err)
	}
	mk("77", "mash send alice.dev.x --seed "+testSeed)
	l, n, err = scanProcCmdlines(root, nil)
	if err != nil || n != 2 || len(l) != 1 || l[0].File != "process 77 (cmdline)" || l[0].Kind != "seed" {
		t.Fatalf("planted: %v %d %v", l, n, err)
	}
}

func TestRefusalHoldsFailsWithoutTheProtection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.join")
	tgtDir := filepath.Join(dir, "t")
	os.Mkdir(tgtDir, 0o700)
	secrets := map[string][]byte{"x": []byte("the-registered-secret-in-the-test")}
	refusal := Result{Code: 1, Stderr: "error: " + path + " already exists\n"}
	for _, st := range []pathState{psFile, psSymlink, psDangling} {
		_ = os.Remove(path)
		g, err := plant(st, path, tgtDir)
		if err != nil {
			t.Fatal(st, err)
		}
		var before []fileGuard
		for _, p := range g {
			before = append(before, takeGuard(p))
		}
		after := func() []fileGuard {
			var a []fileGuard
			for _, b := range before {
				a = append(a, takeGuard(b.Path))
			}
			return a
		}
		if ok, d := refusalHolds(refusal, before, after(), refusedWordsRE, secrets); !ok {
			t.Errorf("%s: a proper refusal failed: %s", st, d)
		}
		// exit 0: the protection is gone
		if ok, _ := refusalHolds(Result{Code: 0, Stdout: "created\n"}, before, after(), refusedWordsRE, secrets); ok {
			t.Errorf("%s: exit 0 passed", st)
		}
		// killed, panic
		if ok, _ := refusalHolds(Result{Code: -1, Stderr: "already exists"}, before, after(), refusedWordsRE, secrets); ok {
			t.Errorf("%s: a killed command passed", st)
		}
		if ok, _ := refusalHolds(Result{Code: 2, Stderr: "panic: already exists\ngoroutine 1"}, before, after(), refusedWordsRE, secrets); ok {
			t.Errorf("%s: a panic passed", st)
		}
		// no message, or a message that does not say why
		if ok, _ := refusalHolds(Result{Code: 1}, before, after(), refusedWordsRE, secrets); ok {
			t.Errorf("%s: a silent refusal passed", st)
		}
		if ok, _ := refusalHolds(Result{Code: 1, Stderr: "something went wrong\n"}, before, after(), refusedWordsRE, secrets); ok {
			t.Errorf("%s: an unexplained refusal passed", st)
		}
		// a secret in the output
		if ok, _ := refusalHolds(Result{Code: 1, Stderr: "already exists; saw " + testSeed}, before, after(), refusedWordsRE, secrets); ok {
			t.Errorf("%s: a seed in the output passed", st)
		}
		if ok, _ := refusalHolds(Result{Code: 1, Stderr: "already exists the-registered-secret-in-the-test"}, before, after(), refusedWordsRE, secrets); ok {
			t.Errorf("%s: a registered secret in the output passed", st)
		}
		// the file was changed, the symlink was replaced, the target was changed
		switch st {
		case psFile:
			os.WriteFile(path, []byte("overwritten\n"), 0o600)
			if ok, _ := refusalHolds(refusal, before, after(), refusedWordsRE, secrets); ok {
				t.Errorf("%s: an overwritten file passed", st)
			}
		case psSymlink:
			os.WriteFile(g[1], []byte("written through the link\n"), 0o600) // the target is changed
			if ok, _ := refusalHolds(refusal, before, after(), refusedWordsRE, secrets); ok {
				t.Errorf("%s: a changed target passed", st)
			}
			os.WriteFile(g[1], []byte(plantedText), 0o600)
			os.Remove(path)
			os.WriteFile(path, []byte(plantedText), 0o600) // the link replaced by a file with the same bytes
			if ok, _ := refusalHolds(refusal, before, after(), refusedWordsRE, secrets); ok {
				t.Errorf("%s: a replaced link passed", st)
			}
		case psDangling:
			os.WriteFile(g[1], []byte("created through the dangling link\n"), 0o600)
			if ok, _ := refusalHolds(refusal, before, after(), refusedWordsRE, secrets); ok {
				t.Errorf("%s: a file created at the link's target passed", st)
			}
		}
	}
	// a mode change is a change
	_ = os.Remove(path)
	g, _ := plant(psFile, path, tgtDir)
	b := takeGuard(g[0])
	os.Chmod(path, 0o644)
	if b.Diff(takeGuard(path)) == "" {
		t.Error("a mode change was not seen")
	}
	// a path that appears where there was none (the staged key, the seed written before the refusal)
	gone := filepath.Join(dir, "absent.seed")
	b = takeGuard(gone)
	os.WriteFile(gone, []byte("x"), 0o600)
	if ok, _ := refusalHolds(refusal, []fileGuard{b}, []fileGuard{takeGuard(gone)}, refusedWordsRE, nil); ok {
		t.Error("a file that appeared passed")
	}
}

func TestCraftMailMakesEachCase(t *testing.T) {
	acct, _ := nkeys.CreateAccount()
	apub, _ := acct.PublicKey()
	mk := func() sap.Identity {
		kp, _ := nkeys.CreateUser()
		seed, _ := kp.Seed()
		id, err := sap.NewIdentity(seed, apub)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	legit, stranger := mk(), mk()
	if legit.User == stranger.User {
		t.Fatal("same key")
	}
	subject := "agents.mail.alice.dev.review"
	payload := []byte("probe")
	now := time.Now()
	hdr := func(kind string) string {
		h, err := craftMail(kind, legit, stranger, "alice_dev_design", subject, payload, now)
		if err != nil {
			t.Fatal(kind, err)
		}
		return h
	}
	if h := hdr("absent"); h != "" {
		t.Errorf("absent has a header: %q", h)
	}
	if _, tr := sap.VerifyStored(hdr("absent"), subject, payload); tr != sap.TrustAbsent {
		t.Errorf("absent is %s", tr)
	}
	if h, tr := sap.VerifyStored(hdr("claimed"), subject, payload); tr != sap.TrustClaimed || h.User != legit.User {
		t.Errorf("claimed is %s", tr)
	}
	// unknown-signer: a valid signature by a key that is not the sender's (the directory, which knows only legit, decides)
	if h, tr := sap.VerifyStored(hdr("unknown-signer"), subject, payload); tr != sap.TrustVerified || h.User != stranger.User || h.Name != "alice_dev_design" {
		t.Errorf("unknown-signer: %s user %s name %s", tr, h.User, h.Name)
	}
	// stale: signed by the sender's own key an hour before now
	h, tr := sap.VerifyStored(hdr("stale"), subject, payload)
	ts, _ := time.Parse(time.RFC3339Nano, h.TS)
	if tr != sap.TrustVerified || h.User != legit.User || now.Sub(ts) < 59*time.Minute {
		t.Errorf("stale: %s user %s age %s", tr, h.User, now.Sub(ts))
	}
	// the control is signed now by the sender
	h, tr = sap.VerifyStored(hdr("control"), subject, payload)
	ts, _ = time.Parse(time.RFC3339Nano, h.TS)
	if tr != sap.TrustVerified || h.User != legit.User || now.Sub(ts) > time.Minute {
		t.Errorf("control: %s", tr)
	}
	// a signature is for one payload and one subject
	if _, tr := sap.VerifyStored(hdr("control"), subject, []byte("other")); tr != sap.TrustForged {
		t.Errorf("a changed payload is %s", tr)
	}
	if _, err := craftMail("nonsense", legit, stranger, "x", subject, payload, now); err == nil {
		t.Error("an unknown case was made")
	}
}

func TestStoredTrustAndShownVerified(t *testing.T) {
	js := `{"address":"alice.dev.review","messages":[{"number":4,"from":"?","trust":"absent"},{"number":7,"from":"alice.dev.design","trust":"verified"}]}`
	if tr, ok := storedTrust(js, 4); !ok || tr != "absent" {
		t.Error(tr, ok)
	}
	if tr, ok := storedTrust(js, 7); !ok || tr != "verified" {
		t.Error(tr, ok)
	}
	if _, ok := storedTrust(js, 9); ok {
		t.Error("an absent message was found")
	}
	if _, ok := storedTrust("not json", 4); ok {
		t.Error("not json was read")
	}
	digest := "{\"content\":\"3 messages from a, ? [absent], b [verified]\\n\\n--- #3 from ? [absent] at T\\nkind: task\\n\\u003e probe at4-x\\n\\n--- #4 from alice.dev.design [verified] at T\\n\\u003e probe at4-y\\n\"}"
	if l := shownLabels(digest, "at4-x"); len(l) != 1 || l[0] != "absent" {
		t.Errorf("digest: %v", l)
	}
	if l := shownLabels(digest, "at4-y"); len(l) != 1 || l[0] != "verified" {
		t.Errorf("digest: %v", l)
	}
	if l := shownLabels(digest, "at4-none"); len(l) != 0 {
		t.Errorf("a message that is not shown has labels: %v", l)
	}
	inbox := "#3\tfrom ? [absent]\tT\t41 B\tprobe at4-x\tkind task\n#4\tfrom alice.dev.design [verified]\tT\t41 B\tprobe at4-y\n"
	if l := shownLabels(inbox, "at4-y"); len(l) != 1 || l[0] != "verified" {
		t.Errorf("inbox: %v", l)
	}
	// the protection fails when the message is presented as verified, or under another label than the stored one
	texts := func(s string) map[string]string { return map[string]string{"x": s} }
	if d := mislabelled(texts(digest), "at4-x", "absent"); d != "" {
		t.Errorf("a right label failed: %s", d)
	}
	if d := mislabelled(texts("--- #3 from alice.dev.design [verified] at T\n> probe at4-x\n"), "at4-x", "absent"); d == "" {
		t.Error("a message presented as verified passed")
	}
	if d := mislabelled(texts("--- #3 from alice.dev.design at T\n> probe at4-x\n"), "at4-x", "stale"); d == "" {
		t.Error("a message shown with no label passed")
	}
	if d := mislabelled(texts("nothing here"), "at4-x", "absent"); d != "" {
		t.Errorf("a message that is not shown failed: %s", d)
	}
}

func TestPlainChecksFailWithoutTheProtection(t *testing.T) {
	const ov = "MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION"
	good := Result{Code: 2, Stderr: "mash: a plain nats:// URL needs " + ov + "=1; use a tls:// URL\n"}
	if ok, d := plainRefused(good, 20*time.Millisecond, 3*time.Second, ov); !ok {
		t.Fatal(d)
	}
	for name, c := range map[string]struct {
		r    Result
		took time.Duration
	}{
		"it connected and succeeded": {Result{Code: 0, Stdout: "ok\n"}, 20 * time.Millisecond},
		"it waited for a connection": {good, 6 * time.Second},
		"a connection error":         {Result{Code: 1, Stderr: "error: nats: no servers available for connection; use tls:// and " + ov}, 20 * time.Millisecond},
		"no mention of tls":          {Result{Code: 2, Stderr: "refused " + ov}, 20 * time.Millisecond},
		"no mention of the override": {Result{Code: 2, Stderr: "refused: use tls://"}, 20 * time.Millisecond},
		"killed":                     {Result{Code: -1, Stderr: "tls:// " + ov}, 20 * time.Millisecond},
	} {
		if ok, _ := plainRefused(c.r, c.took, 3*time.Second, ov); ok {
			t.Errorf("%s passed", name)
		}
	}
	// the override's behaviour (pending today)
	if ok, d := dangerousWarned(Result{Code: 0, Stderr: at4EncryptionWarning + "\n"}, 1); !ok {
		t.Error(d)
	}
	for name, c := range map[string]struct {
		r Result
		n int64
	}{
		"no connection":       {Result{Stderr: at4EncryptionWarning}, 0},
		"no warning":          {Result{Stderr: "plain"}, 1},
		"warning on stdout":   {Result{Stdout: at4EncryptionWarning}, 1},
		"a different warning": {Result{Stderr: "warning: plain-text connection"}, 1},
	} {
		if ok, _ := dangerousWarned(c.r, c.n); ok {
			t.Errorf("%s passed", name)
		}
	}
	if ok, d := oldNameRefused(Result{Code: 2, Stderr: "MASH_INSECURE_PLAINTEXT is refused: use MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION"}, 0); !ok {
		t.Error(d)
	}
	if ok, _ := oldNameRefused(Result{Code: 0, Stderr: "connected"}, 1); ok {
		t.Error("the old name still working passed")
	}
	if ok, _ := oldNameRefused(Result{Code: 2, Stderr: "MASH_INSECURE_PLAINTEXT is refused"}, 0); ok {
		t.Error("a refusal that does not name the new variable passed")
	}
}

func TestPendingIsNotAPassAndTheTableOnlyShrinks(t *testing.T) {
	if at4Judge(false, true) != at4Pass || at4Judge(false, false) != at4Fail {
		t.Fatal("unlisted checks must pass or fail on their condition")
	}
	if at4Judge(true, false) != at4Waiting {
		t.Fatal("a listed check that does not hold is pending")
	}
	if at4Judge(true, true) != at4Fail {
		t.Fatal("a listed check that holds must fail: its entry is stale")
	}
	a := &AT{C: &Checks{}}
	a.pend("plain-text-override", "pending one", false, "observed")
	a.pend("plain-text-override", "stale one", true, "observed")
	a.pend("no-such-key", "unlisted failing", false, "observed")
	a.pend("no-such-key", "unlisted passing", true, "observed")
	all := a.C.All()
	if !all[0].Pending || !all[0].Pass || !strings.Contains(all[0].Detail, "R1-2") {
		t.Errorf("pending: %+v", all[0])
	}
	if all[1].Pass || !strings.Contains(all[1].Detail, "remove") {
		t.Errorf("stale: %+v", all[1])
	}
	if all[2].Pass || all[2].Pending {
		t.Errorf("unlisted failing: %+v", all[2])
	}
	if !all[3].Pass || all[3].Pending {
		t.Errorf("unlisted passing: %+v", all[3])
	}
	if a.C.PendingCount() != 1 || a.C.Failed() != 2 {
		t.Errorf("pending %d failed %d", a.C.PendingCount(), a.C.Failed())
	}
	tab := a.C.Table()
	if !strings.Contains(tab, "PEND  pending one") || strings.Contains(tab, "PASS  pending one") {
		t.Errorf("table:\n%s", tab)
	}
	// in the record a pending check is "pending", never "pass", and the result says so
	c := &Checks{}
	c.Check("held", true, "")
	c.Pending("later", "waits")
	rec := Record{RunID: "x", Revision: "y", AT: "at004", Scenario: "s", Observed: c.All()}
	md := rec.Markdown()
	if !strings.Contains(md, "- pending: later (waits)") || strings.Contains(md, "- pass: later") || !strings.Contains(md, "PASS, with 1 check(s) pending") {
		t.Errorf("record:\n%s", md)
	}
	if !rec.Passed() {
		t.Error("a pending check must not fail the run")
	}
	// every entry of the table is named by a check in at004.go (no orphan entry), and has a reason
	src, err := os.ReadFile("at004.go")
	if err != nil {
		t.Fatal(err)
	}
	for k, reason := range at4Pending {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("%s has no reason", k)
		}
		if !regexp.MustCompile(`pend\("` + k + `"|remove ` + k + ` from`).Match(src) {
			t.Errorf("at4Pending entry %q is used by no check", k)
		}
	}
}

func TestRegisterSecretsAndPlant(t *testing.T) {
	m := map[string][]byte{}
	registerSecrets(m, "creds", []byte("-----BEGIN NATS USER JWT-----\n"+testJWT+"\n------END NATS USER JWT------\n\n-----BEGIN USER NKEY SEED-----\n"+testSeed+"\n------END USER NKEY SEED------\n"))
	var whole, seed, jwt bool
	for k := range m {
		whole = whole || strings.HasSuffix(k, "(whole file)")
		seed = seed || strings.Contains(k, " seed 1")
		jwt = jwt || strings.Contains(k, " jwt 1")
	}
	if !whole || !seed || !jwt {
		t.Errorf("registered: %v", m)
	}
	if _, err := plant("nonsense", filepath.Join(t.TempDir(), "x"), t.TempDir()); err == nil {
		t.Error("an unknown state was planted")
	}
}

func TestAT004IsListed(t *testing.T) {
	var so, se strings.Builder
	if code := realMain([]string{"at004", "--port", "4222", "--inside"}, &so, &se); code != 2 || !strings.Contains(se.String(), "--inside is refused outside a container") {
		t.Fatalf("at004 is not a verb of the harness: %d %q", code, se.String())
	}
	if AT004.ID != "at004" || !strings.Contains(AT004.Scenario, "AT-005") {
		t.Error("the scenario text names no cross-reference to AT-005")
	}
	var usage strings.Builder
	realMain(nil, &so, &usage)
	if !strings.Contains(usage.String(), "at004") {
		t.Error("usage does not list at004")
	}
}
