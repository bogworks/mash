package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeSeed and fakeJWT have the shape of a secret and are not one.
var (
	fakeSeed = "SU" + strings.Repeat("B", 54)
	fakeJWT  = "eyJ" + strings.Repeat("c", 12) + ".payload12345.sig_-"
)

// tool resolves a program on PATH, failing the test when it is missing.
func tool(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("required tool %q is not on PATH: %v", name, err)
	}
	return p
}

func TestScanBytes(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"nothing here", nil},
		{"a seed " + fakeSeed + " in text", []string{"seed"}},
		{"a jwt " + fakeJWT, []string{"jwt"}},
		{fakeSeed + "\n" + fakeJWT, []string{"seed", "jwt"}},
		{"SU" + strings.Repeat("B", 20), nil}, // too short to be a seed
		{"SAMPLE SECTION SUCCESS SOCIAL", nil},
		{"U" + strings.Repeat("B", 55), nil}, // public keys are not seeds
		{"A" + strings.Repeat("B", 55), nil},
		{"eyJabc.def.ghi", nil}, // too short to be a JWT
	} {
		got := scanBytes([]byte(c.in))
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("scanBytes(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestScanDirNamesFileNotMatch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "01-a.out"), []byte("clean"), 0o600)
	os.WriteFile(filepath.Join(dir, "02-b.err"), []byte("x "+fakeSeed), 0o600)
	os.WriteFile(filepath.Join(dir, "03-c.out"), []byte(fakeJWT), 0o600)
	leaks, err := scanDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(leaks) != 2 || leaks[0].File != "02-b.err" || leaks[0].Kind != "seed" || leaks[1].File != "03-c.out" || leaks[1].Kind != "jwt" {
		t.Fatalf("leaks = %+v", leaks)
	}
	for _, l := range leaks {
		if strings.Contains(l.File+l.Kind, fakeSeed) || strings.Contains(l.File+l.Kind, fakeJWT) {
			t.Fatal("a leak holds the match")
		}
	}
}

func TestScanFor(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a"), []byte("one SECRETTEXT two"), 0o600)
	os.WriteFile(filepath.Join(dir, "b"), []byte("none"), 0o600)
	got, _ := scanFor(dir, map[string][]byte{"s": []byte("SECRETTEXT"), "empty": nil})
	if len(got) != 1 || got[0].File != "a" || got[0].Kind != "s" {
		t.Fatalf("got %+v", got)
	}
}

func TestRunnerSavesEveryCommandAndFlagsALeak(t *testing.T) {
	out := t.TempDir()
	var log strings.Builder
	r := &Runner{Out: out, Cwd: out, Log: &log}
	h, _ := NewHome(filepath.Join(t.TempDir(), "h"))
	env := BuildEnv(h, nil)
	a := r.Run("Say Hi", []string{tool(t, "echo"), "hello"}, env, "")
	b := r.Run("fail", []string{tool(t, "sh"), "-c", "echo oops >&2; exit 3"}, env, "")
	c := r.Run("leaky", []string{tool(t, "echo"), fakeSeed}, env, "")
	d := r.Run("stdin", []string{tool(t, "cat")}, env, "from stdin")
	if a.Code != 0 || a.Stdout != "hello\n" || b.Code != 3 || b.Stderr != "oops\n" || c.Code != 0 || d.Stdout != "from stdin" {
		t.Fatalf("results: %+v %+v %+v %+v", a, b, c, d)
	}
	for _, f := range []string{"01-say-hi.out", "01-say-hi.err", "02-fail.err", "03-leaky.out", "04-stdin.out"} {
		st, err := os.Stat(filepath.Join(out, f))
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v", f, st, err)
		}
	}
	leaks := r.Leaks()
	if len(leaks) != 1 || leaks[0].File != "03-leaky.out" || leaks[0].Kind != "seed" {
		t.Fatalf("leaks %+v", leaks)
	}
	if !strings.Contains(log.String(), "02-fail: sh -c") || !strings.Contains(log.String(), "exit=3") {
		t.Errorf("log lacks the command line and exit code:\n%s", log.String())
	}
	if strings.Contains(log.String(), fakeSeed) {
		t.Error("the log holds the leaked text")
	}
}

func TestRunnerTimeout(t *testing.T) {
	out := t.TempDir()
	r := &Runner{Out: out, Cwd: out, Timeout: 300 * time.Millisecond}
	h, _ := NewHome(filepath.Join(t.TempDir(), "h"))
	res := r.Run("sleepy", []string{tool(t, "sleep"), "5"}, BuildEnv(h, nil), "")
	if res.Code != -1 || !strings.Contains(res.Stderr, "killed after") {
		t.Fatalf("%+v", res)
	}
}

func TestBuildEnvInheritsNothing(t *testing.T) {
	t.Setenv("MASH_SERVER", "nats://caller.invalid:1")
	t.Setenv("MASH_ENDPOINT", "caller.g.n")
	t.Setenv("NATS_URL", "nats://caller.invalid:2")
	t.Setenv("HOME", "/caller/home")
	t.Setenv("XDG_CONFIG_HOME", "/caller/xdg")
	t.Setenv("PATH", "/opt/tools/bin:/usr/bin")
	h, err := NewHome(filepath.Join(t.TempDir(), "h"))
	if err != nil {
		t.Fatal(err)
	}
	env := BuildEnv(h, []string{"/opt/nats"}, "MASH_SERVER=nats://127.0.0.1:1", "MASH_ENDPOINT=alice.g.n", "MASH_ENDPOINT=bob.g.n")
	got := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if _, dup := got[k]; dup {
			t.Errorf("%s set twice", k)
		}
		got[k] = v
	}
	if got["HOME"] != h.Dir || got["MASH_CONFIG_DIR"] != h.Config || got["MASH_KEYS_DIR"] != h.Keys {
		t.Errorf("scratch values missing: %v", got)
	}
	if got["MASH_SERVER"] != "nats://127.0.0.1:1" || got["MASH_ENDPOINT"] != "bob.g.n" {
		t.Errorf("extra does not win, last extra wins: %v", got)
	}
	for k := range got {
		if strings.HasPrefix(k, "NATS_") || k == "XDG_CONFIG_HOME" {
			t.Errorf("inherited or unexpected variable %s", k)
		}
	}
	if got["PATH"] != "/opt/tools/bin:/usr/bin:/opt/nats" { // the caller's PATH names where the tools are, plus the extras
		t.Errorf("PATH %q", got["PATH"])
	}
	for _, v := range got {
		if strings.Contains(v, "caller") {
			t.Errorf("a caller value leaked: %q", v)
		}
	}
}

func TestChecks(t *testing.T) {
	var c Checks
	if !c.Check("holds", true, "exit 0") || c.Check("breaks", false, "exit 2, refusal group_missing") {
		t.Fatal("Check must return its condition")
	}
	if c.Failed() != 1 || len(c.All()) != 2 {
		t.Fatalf("failed %d of %d", c.Failed(), len(c.All()))
	}
	tab := c.Table()
	if !strings.Contains(tab, "PASS  holds  [exit 0]") || !strings.Contains(tab, "FAIL  breaks  [exit 2, refusal group_missing]") {
		t.Fatalf("table:\n%s", tab)
	}
}

func TestRecordHasNoSecretsAndShowsEverything(t *testing.T) {
	rec := Record{
		RunID: "20261117T000000Z-abcd1234", Revision: "deadbeef (clean)", AT: "at001", Scenario: "the scenario",
		Env: []string{"linux/amd64", "go: go1.27.4", "nats-server: nats-server: v2.15.1"},
		Observed: []Assertion{
			{Name: "a holds", Pass: true, Detail: "exit 0"},
			{Name: "b refuses", Pass: false, Detail: "exit 2, refusal group_missing, saw " + fakeSeed + " and " + fakeJWT},
		},
		Leaks: []Leak{{File: "03-x.out", Kind: "seed"}},
	}
	md := rec.Markdown()
	if strings.Contains(md, fakeSeed) || strings.Contains(md, fakeJWT) || len(scanBytes([]byte(md))) != 0 {
		t.Fatalf("the record holds a secret:\n%s", md)
	}
	for _, want := range []string{"20261117T000000Z-abcd1234", "deadbeef (clean)", "linux/amd64", "the scenario", "- a holds", "- pass: a holds (exit 0)", "- FAIL: b refuses", "refusal group_missing", "03-x.out", "## Result\n\nFAIL"} {
		if !strings.Contains(md, want) {
			t.Errorf("record lacks %q:\n%s", want, md)
		}
	}
	rec.Leaks, rec.Observed = nil, rec.Observed[:1]
	if !rec.Passed() || !strings.Contains(rec.Markdown(), "## Result\n\nPASS") {
		t.Error("a clean record of passing assertions must pass")
	}
	if (Record{}).Passed() {
		t.Error("a record with no assertions must not pass")
	}
	path := filepath.Join(t.TempDir(), "record-at001.md")
	if err := WriteRecord(path, rec); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
	if WriteRecord(path, rec) == nil {
		t.Error("an existing record must not be overwritten")
	}
}

func TestRunID(t *testing.T) {
	a, b := newRunID(time.Date(2026, 11, 17, 1, 2, 3, 0, time.UTC)), newRunID(time.Date(2026, 11, 17, 1, 2, 3, 0, time.UTC))
	if !strings.HasPrefix(a, "20261117T010203Z-") || len(a) != len("20261117T010203Z-")+8 || a == b {
		t.Errorf("%q %q", a, b)
	}
}

func TestRefusalCode(t *testing.T) {
	for in, want := range map[string]string{
		`{"refusal":"group_missing"}`:          "group_missing",
		`{"refusal":{"code":"key_missing"}}`:   "key_missing",
		`{"outcome":"created","refusal":null}`: "",
		`not json`:                             "",
	} {
		if got := refusalCode(in); got != want {
			t.Errorf("refusalCode(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestTreeHash(t *testing.T) {
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "a"), []byte("1"), 0o600)
	os.Mkdir(filepath.Join(d, "state"), 0o700)
	h1, _ := treeHash(d, "state")
	os.WriteFile(filepath.Join(d, "state", "x"), []byte("noise"), 0o600)
	if h2, _ := treeHash(d, "state"); h1 != h2 {
		t.Error("a skipped directory changed the digest")
	}
	os.WriteFile(filepath.Join(d, "a"), []byte("2"), 0o600)
	if h3, _ := treeHash(d, "state"); h1 == h3 {
		t.Error("a changed byte did not change the digest")
	}
}

func TestStopPIDRefusesAForeignCommandLine(t *testing.T) {
	r := &Run{Out: t.TempDir()}
	_ = r
	out := t.TempDir()
	run := &Runner{Out: out, Cwd: out}
	h, _ := NewHome(filepath.Join(t.TempDir(), "h"))
	p, err := run.Start("cat", []string{tool(t, "cat")}, BuildEnv(h, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	stopPID(p.PID, "no-such-marker", func(string, ...any) {})
	if !alive(p.PID) {
		t.Fatal("stopPID signalled a process whose command line does not hold the marker")
	}
	pid := p.PID
	p.Stop()
	if alive(pid) {
		t.Fatal("Stop left the process")
	}
}

func TestBuildEnvDefaultPathWhenTheCallerHasNone(t *testing.T) {
	t.Setenv("PATH", "")
	got := map[string]string{}
	for _, kv := range BuildEnv(Home{Dir: "/h", Config: "/h/c", Keys: "/h/k", State: "/h/s", Tmp: "/h/t"}, nil) {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	if got["PATH"] != "/usr/bin:/bin" {
		t.Errorf("PATH %q", got["PATH"])
	}
}

func TestEverySeedPrefixIsFlagged(t *testing.T) {
	for _, p := range []string{"O", "A", "N", "C", "U", "P", "X"} {
		tok := "S" + p + strings.Repeat("A", 54)
		if got := scanBytes([]byte("x " + tok + " y")); len(got) != 1 || got[0] != "seed" {
			t.Errorf("prefix %s: got %v", p, got)
		}
		if strings.Contains(redact(tok), tok) {
			t.Errorf("prefix %s: not redacted", p)
		}
	}
	if got := scanBytes([]byte("S" + "Z" + strings.Repeat("A", 54))); got != nil {
		t.Errorf("unknown prefix flagged: %v", got)
	}
}

func TestCheckPermissions(t *testing.T) {
	pub, sub := expectedPermissions("alice", "dev", "design")
	if f, o := checkPermissions("alice", "dev", "design", pub, sub, nil); len(f)+len(o) != 0 {
		t.Fatalf("exact set failed: %v %v", f, o)
	}
	f, o := checkPermissions("alice", "dev", "design", append(slices.Clone(pub), "agents.>"), sub, nil)
	if len(f) != 1 || !strings.HasPrefix(f[0], "unexpected:") || len(o) != 1 {
		t.Errorf("extra agents.>: %v %v", f, o)
	}
	f, _ = checkPermissions("alice", "dev", "design", pub[1:], sub, nil)
	if len(f) != 1 || !strings.HasPrefix(f[0], "missing:") {
		t.Errorf("missing line: %v", f)
	}
	f, o = checkPermissions("alice", "dev", "design", append(slices.Clone(pub), "agents.mail.bob.*.*"), sub, nil)
	if len(f) != 1 || len(o) != 1 {
		t.Errorf("foreign owner: %v %v", f, o)
	}
	f, _ = checkPermissions("alice", "dev", "design", pub, sub, []string{"publish x"})
	if len(f) != 1 || !strings.HasPrefix(f[0], "deny entries:") {
		t.Errorf("deny: %v", f)
	}
}

func finishFor(t *testing.T, out string) (*Runner, []Assertion) {
	t.Helper()
	dir := t.TempDir()
	r := &Runner{Out: out, Cwd: dir}
	a := &AT{R: &Run{Dir: dir, Out: out, Runner: r, pids: map[int]string{}}, ID: "x", Dir: dir, C: &Checks{}, secrets: map[string][]byte{}}
	h, _ := NewHome(filepath.Join(dir, "h"))
	a.R.Runner.Run("echo", []string{tool(t, "echo"), "hi"}, BuildEnv(h, nil), "")
	a.Finish(Scenario{ID: "x"})
	return r, a.C.All()
}

func captureCheck(as []Assertion) *Assertion {
	for i := range as {
		if strings.Contains(as[i].Name, "captured completely") {
			return &as[i]
		}
	}
	return nil
}

func TestCleanRunRecordsNoCaptureError(t *testing.T) {
	r, as := finishFor(t, t.TempDir())
	c := captureCheck(as)
	if len(r.CaptureErrors()) != 0 || c == nil || !c.Pass {
		t.Fatalf("errors %v check %+v", r.CaptureErrors(), c)
	}
}

func TestCaptureErrorFailsTheScenario(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-dir")
	r, as := finishFor(t, missing)
	errs := r.CaptureErrors()
	if len(errs) == 0 || !strings.Contains(errs[0], "01-echo.out") {
		t.Fatalf("errors %v", errs)
	}
	c := captureCheck(as)
	if c == nil || c.Pass || !strings.Contains(c.Detail, "01-echo.out") {
		t.Fatalf("check %+v", c)
	}
	res := r.Run("again", []string{tool(t, "echo"), "hi"}, os.Environ(), "")
	if res.CaptureErr == "" || res.Code != 0 {
		t.Fatalf("result %+v", res)
	}
}

func TestProcReadBackErrorIsRecorded(t *testing.T) {
	out := t.TempDir()
	r := &Runner{Out: out, Cwd: out}
	h, _ := NewHome(filepath.Join(t.TempDir(), "h"))
	p, err := r.Start("cat", []string{tool(t, "cat")}, BuildEnv(h, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(out, p.base+".out")); err != nil {
		t.Fatal(err)
	}
	p.Stop()
	errs := r.CaptureErrors()
	if len(errs) != 1 || !strings.Contains(errs[0], "reading back") {
		t.Fatalf("errors %v", errs)
	}
}

// fakeJWT builds header.payload.signature at run time with a fake signature.
func fakeIssuedJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	b, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	e := base64.RawURLEncoding
	return e.EncodeToString([]byte(`{"typ":"JWT","alg":"x"}`)) + "." + e.EncodeToString(b) + "." + e.EncodeToString([]byte("sig"))
}

func TestDecodeIssuedAgainstContract(t *testing.T) {
	wp, ws := expectedPermissions("alice", "dev", "design")
	claims := func(name string, extraPub string, deny []string) map[string]any {
		pub := slices.Clone(wp)
		if extraPub != "" {
			pub = append(pub, extraPub)
		}
		return map[string]any{"name": name, "nats": map[string]any{
			"pub": map[string]any{"allow": pub, "deny": deny}, "sub": map[string]any{"allow": ws}}}
	}
	cases := []struct {
		label string
		c     map[string]any
		ok    bool
	}{
		{"exact", claims("alice.dev.design", "", nil), true},
		{"extra grant", claims("alice.dev.design", "other.>", nil), false},
		{"deny", claims("alice.dev.design", "", []string{"x.>"}), false},
		{"wrong name", claims("alice.dev.review", "", nil), false},
	}
	for _, c := range cases {
		pub, sub, deny, name, err := decodeIssued("-----BEGIN NATS USER JWT-----\n" + fakeIssuedJWT(t, c.c) + "\n------END NATS USER JWT------\n")
		if err != nil {
			t.Fatalf("%s: %v", c.label, err)
		}
		fails, _ := checkPermissions("alice", "dev", "design", pub, sub, deny)
		got := len(fails) == 0 && name == "alice.dev.design"
		if got != c.ok {
			t.Errorf("%s: pass=%v want %v (%v)", c.label, got, c.ok, fails)
		}
	}
	if _, _, _, _, err := decodeIssued("nothing here"); err == nil {
		t.Error("a file with no JWT must error")
	}
}

// Two audits of an unchanged server that fall in different seconds hash alike: the "generated" stamp is not hashed.
func TestWithoutGeneratedIgnoresTheTimestamp(t *testing.T) {
	a := `{"version":2,"generated":"2026-11-17T10:00:00Z","records":[{"key":"a"}]}`
	b := `{"records":[{"key":"a"}],"generated":"2026-11-17T10:00:01Z","version":2}`
	if withoutGenerated(a) != withoutGenerated(b) {
		t.Errorf("audits that differ only in generated and key order differ: %q vs %q", withoutGenerated(a), withoutGenerated(b))
	}
	c := `{"version":2,"generated":"2026-11-17T10:00:00Z","records":[{"key":"b"}]}`
	if withoutGenerated(a) == withoutGenerated(c) {
		t.Error("audits with different records are equal")
	}
	if got := withoutGenerated("not json"); got != "not json" {
		t.Errorf("non-JSON changed: %q", got)
	}
}

// A check that does not apply is recorded as n/a: not a pass, not a failure.
func TestNotApplicableIsNeitherPassNorFailure(t *testing.T) {
	var c Checks
	c.Check("a real check", true, "")
	c.NotApplicable("a check that does not apply", "no account update")
	if c.Failed() != 0 {
		t.Errorf("Failed() = %d", c.Failed())
	}
	rec := Record{RunID: "x", Revision: "y", AT: "at005", Scenario: "s", Observed: c.All()}.Markdown()
	if !strings.Contains(rec, "- pass: a real check") || !strings.Contains(rec, "- n/a: a check that does not apply (no account update)") {
		t.Errorf("record lines wrong:\n%s", rec)
	}
	if strings.Contains(rec, "- pass: a check that does not apply") {
		t.Error("a not-applicable check was recorded as a pass")
	}
	if !strings.Contains(c.Table(), "N/A") {
		t.Errorf("table: %q", c.Table())
	}
}
