package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// fakeDrive answers each direction (keyed "writer>reader") from a table; a direction not in it passes.
func fakeDrive(bad map[string]func([]compatObs) []compatObs) func(context.Context, compatBin, compatBin, []compatMsg) ([]compatObs, error) {
	return func(_ context.Context, w, r compatBin, corpus []compatMsg) ([]compatObs, error) {
		obs := make([]compatObs, len(corpus))
		for i, m := range corpus {
			obs[i] = compatObs{Number: m.Number, Sent: true, Seq: m.Number + 1, Copies: 1, Notified: 1}
		}
		if f := bad[w.Version+">"+r.Version]; f != nil {
			obs = f(obs)
		}
		return obs, nil
	}
}

func fakeDownload(fail string) func(context.Context, string, string) (string, error) {
	return func(_ context.Context, tag, dir string) (string, error) {
		if tag == "v"+fail {
			return "", errors.New("no such release")
		}
		p := filepath.Join(dir, "mash")
		return p, os.WriteFile(p, []byte(fakeBinary(tag)), 0o755)
	}
}

// fakeBinary is what the fake download of tag contains.
func fakeBinary(tag string) string { return "fake mash " + tag }

func hexSum(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// fakeSums is the SHA256SUMS of the fake release tag: the right digest of fakeBinary for this host.
func fakeSums(_ context.Context, tag string) ([]byte, error) {
	return []byte(hexSum(fakeBinary(tag)) + "  mash-" + runtime.GOOS + "-" + runtime.GOARCH + "\n" + strings.Repeat("0", 64) + "  mash-plan9-386\n"), nil
}

func runCompat(args []string, o CompatOptions) (stdout, stderr string, code int) {
	var so, se bytes.Buffer
	o.Out, o.ErrOut = &so, &se
	if o.Sums == nil {
		o.Sums = fakeSums
	}
	code = compatMain(args, o)
	return so.String(), se.String(), code
}

// TestTO13Golden compares the four outputs of TO-13 with testdata/to13.golden byte for byte.
func TestTO13Golden(t *testing.T) {
	raw, err := os.ReadFile("testdata/to13.golden")
	if err != nil {
		t.Fatal(err)
	}
	type gcase struct {
		stream string
		exit   int
		text   string
	}
	golden := map[string]gcase{}
	hdr := regexp.MustCompile(`^=== (\S+) (\S+) exit (\d+)$`)
	var id string
	for _, line := range strings.SplitAfter(string(raw), "\n") {
		if m := hdr.FindStringSubmatch(strings.TrimSuffix(line, "\n")); m != nil {
			id = m[1]
			var n int
			fmt.Sscan(m[3], &n)
			golden[id] = gcase{stream: m[2], exit: n}
			continue
		}
		g := golden[id]
		g.text += line
		golden[id] = g
	}
	if len(golden) != 4 {
		t.Fatalf("golden has %d cases, want 4", len(golden))
	}
	utc := map[string]func([]compatObs) []compatObs{"0.6.0>0.5.10": func(o []compatObs) []compatObs {
		o[6].Refused = "time not in UTC"
		o[6].Copies = 0
		return o
	}}
	cases := map[string]struct {
		args []string
		o    CompatOptions
	}{
		"release-check-compat-passed":    {[]string{"0.5.10", "0.6.0"}, CompatOptions{Download: fakeDownload(""), Drive: fakeDrive(nil)}},
		"release-check-compat-failed":    {[]string{"0.5.10", "0.6.0"}, CompatOptions{Download: fakeDownload(""), Drive: fakeDrive(utc)}},
		"release-check-compat-usage":     {[]string{"0.5.10"}, CompatOptions{Download: fakeDownload(""), Drive: fakeDrive(nil)}},
		"release-check-compat-undecided": {[]string{"0.5.10", "0.6.0"}, CompatOptions{Download: fakeDownload("0.6.0"), Drive: fakeDrive(nil)}},
	}
	for id, c := range cases {
		t.Run(id, func(t *testing.T) {
			g, ok := golden[id]
			if !ok {
				t.Fatalf("no golden for %s", id)
			}
			stdout, stderr, code := runCompat(c.args, c.o)
			got, other := stdout, stderr
			if g.stream == "stderr" {
				got, other = stderr, stdout
			}
			if got != g.text || other != "" || code != g.exit {
				t.Errorf("%s: %s %q, other stream %q, exit %d; golden %s %q, exit %d", id, g.stream, got, other, code, g.stream, g.text, g.exit)
			}
		})
	}
}

func TestCompatUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"0.5.10"}, {"1", "2"}, {"0.5.10", "0.5.10"}, {"0.6.0", "0.5.10"}, {"0.5.10", "v0.6.0"}, {"a", "b", "c"}} {
		stdout, stderr, code := runCompat(args, CompatOptions{Download: fakeDownload(""), Drive: fakeDrive(nil)})
		if code != 2 || stdout != "" || !strings.HasPrefix(stderr, "error: --compat takes ") || strings.Count(stderr, "\n") != 1 {
			t.Errorf("%v: exit %d stdout %q stderr %q", args, code, stdout, stderr)
		}
	}
}

func TestCompatReasons(t *testing.T) {
	cases := map[string]struct {
		mod  func(o []compatObs) []compatObs
		want string
	}{
		"missing":                   {func(o []compatObs) []compatObs { o[2].Copies = 0; return o }, "message 3: missing"},
		"twice":                     {func(o []compatObs) []compatObs { o[1].Copies = 2; return o }, "message 2: delivered twice"},
		"not delivered":             {func(o []compatObs) []compatObs { o[0].Sent = false; return o }, "message 1: not delivered"},
		"refused":                   {func(o []compatObs) []compatObs { o[4].Refused = "version missing"; return o }, "message 5: refused: version missing"},
		"stored but never notified": {func(o []compatObs) []compatObs { o[3].Notified = 0; return o }, "message 4: not delivered"},
		"notified twice":            {func(o []compatObs) []compatObs { o[1].Notified = 2; return o }, "message 2: delivered twice"},
		"missing from the inbox":    {func(o []compatObs) []compatObs { o[2].Copies = 0; o[2].Notified = 1; return o }, "message 3: missing"},
		"first of several":          {func(o []compatObs) []compatObs { o[5].Copies = 0; o[3].Copies = 2; return o }, "message 4: delivered twice"},
		"absent":                    {func(o []compatObs) []compatObs { return o[:2] }, "message 3: not delivered"},
	}
	for name, c := range cases {
		stdout, _, code := runCompat([]string{"0.5.10", "0.6.0"}, CompatOptions{Download: fakeDownload(""), Drive: fakeDrive(map[string]func([]compatObs) []compatObs{"0.5.10>0.6.0": c.mod})})
		want := "compat: 0.6.0 reads mail written by 0.5.10: FAILED\n  " + c.want + "\n"
		// the first direction passes, so its line comes first
		want = "compat: 0.5.10 reads mail written by 0.6.0: passed\n" + want
		if code != 3 || stdout != want {
			t.Errorf("%s: exit %d, stdout %q, want %q", name, code, stdout, want)
		}
	}
}

func TestCompatDownloadOlderUndecided(t *testing.T) {
	stdout, stderr, code := runCompat([]string{"0.5.10", "0.6.0"}, CompatOptions{Download: fakeDownload("0.5.10"), Drive: fakeDrive(nil)})
	if code != 4 || stdout != "" || stderr != "compat: could not decide: 0.5.10 could not be downloaded\n" {
		t.Errorf("exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestCompatDriveErrorExitsOne(t *testing.T) {
	o := CompatOptions{Download: fakeDownload(""), Drive: func(context.Context, compatBin, compatBin, []compatMsg) ([]compatObs, error) {
		return nil, errors.New("the lab broke")
	}}
	stdout, stderr, code := runCompat([]string{"0.5.10", "0.6.0"}, o)
	if code != 1 || stdout != "" || stderr != "error: the lab broke\n" {
		t.Errorf("exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestCompatPanicExitsOne(t *testing.T) {
	stdout, stderr, code := runCompat([]string{"0.5.10", "0.6.0"}, CompatOptions{Download: fakeDownload(""), Drive: fakeDrive(nil), Panic: true})
	if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "error: ") {
		t.Errorf("exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestCompatMainRunDispatch(t *testing.T) {
	var out bytes.Buffer
	if code := mainRun([]string{"--compat", "0.5.10"}, &out); code != 2 || out.Len() != 0 {
		t.Errorf("exit %d stdout %q", code, out.String())
	}
}

func TestCompatCorpus(t *testing.T) {
	d := compatCorpusDigest()
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(d) || d != compatCorpusDigest() {
		t.Errorf("digest %q is not a stable sha256", d)
	}
	var kinds = map[string]bool{}
	for i, m := range compatCorpus {
		if m.Number != i+1 {
			t.Errorf("message %d is numbered %d", i+1, m.Number)
		}
		kinds[m.What] = true
	}
	for _, w := range []string{"plain", "fyi", "urgent", "reply", "long body", "non-ASCII body"} {
		if !kinds[w] {
			t.Errorf("the corpus lacks %q", w)
		}
	}
}

// F2: a downloaded binary is checked against SHA256SUMS before it runs; a mismatch, a missing line or a missing
// SHA256SUMS is exit 4 and nothing is driven.
func TestCompatChecksumGate(t *testing.T) {
	good := string(must(fakeSums(context.Background(), "v0.5.10")))
	asset := "mash-" + runtime.GOOS + "-" + runtime.GOARCH
	for name, c := range map[string]struct {
		sums func(context.Context, string) ([]byte, error)
		ver  string
	}{
		"digest differs": {func(_ context.Context, tag string) ([]byte, error) {
			return []byte(strings.Repeat("a", 64) + "  " + asset + "\n"), nil
		}, "0.6.0"},
		"no line for the asset": {func(context.Context, string) ([]byte, error) {
			return []byte(strings.Repeat("a", 64) + "  mash-plan9-386\n"), nil
		}, "0.6.0"},
		"no SHA256SUMS": {func(context.Context, string) ([]byte, error) { return nil, errors.New("no such asset") }, "0.6.0"},
		"empty":         {func(context.Context, string) ([]byte, error) { return nil, nil }, "0.6.0"},
		"only the older is wrong": {func(_ context.Context, tag string) ([]byte, error) {
			if tag == "v0.5.10" {
				return []byte(strings.Repeat("a", 64) + "  " + asset + "\n"), nil
			}
			return fakeSums(context.Background(), tag)
		}, "0.5.10"},
	} {
		driven := false
		drive := func(context.Context, compatBin, compatBin, []compatMsg) ([]compatObs, error) {
			driven = true
			return nil, nil
		}
		stdout, stderr, code := runCompat([]string{"0.5.10", "0.6.0"}, CompatOptions{Download: fakeDownload(""), Sums: c.sums, Drive: drive})
		if code != 4 || stdout != "" || stderr != "compat: could not decide: "+c.ver+" failed its checksum\n" || driven {
			t.Errorf("%s: exit %d stdout %q stderr %q driven %v", name, code, stdout, stderr, driven)
		}
	}
	if !strings.Contains(good, asset) {
		t.Fatal("the fake sums lack this host")
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// H3: --candidate runs the given binary as the newer side without downloading it; --evidence records what was run.
func TestCompatCandidateAndEvidence(t *testing.T) {
	dir := t.TempDir()
	cand := filepath.Join(dir, "cand-mash")
	if err := os.WriteFile(cand, []byte("the candidate"), 0o755); err != nil {
		t.Fatal(err)
	}
	ev := filepath.Join(dir, "evidence.json")
	var downloaded []string
	dl := func(ctx context.Context, tag, d string) (string, error) {
		downloaded = append(downloaded, tag)
		return fakeDownload("")(ctx, tag, d)
	}
	var ran [][2]string
	drive := func(_ context.Context, w, r compatBin, _ []compatMsg) ([]compatObs, error) {
		ran = append(ran, [2]string{w.Version + "=" + filepath.Base(w.Path), r.Version + "=" + filepath.Base(r.Path)})
		return fakeDrive(nil)(nil, w, r, compatCorpus)
	}
	for _, args := range [][]string{
		{"--candidate", cand, "--evidence", ev, "0.5.10", "0.6.0"},
		{"0.5.10", "0.6.0", "--candidate=" + cand, "--evidence=" + ev},
	} {
		downloaded, ran = nil, nil
		stdout, stderr, code := runCompat(args, CompatOptions{Download: dl, Drive: drive})
		want := "compat: 0.5.10 reads mail written by 0.6.0: passed\ncompat: 0.6.0 reads mail written by 0.5.10: passed\n"
		if code != 0 || stdout != want || stderr != "" {
			t.Fatalf("%v: exit %d stdout %q stderr %q", args, code, stdout, stderr)
		}
		if len(downloaded) != 1 || downloaded[0] != "v0.5.10" {
			t.Errorf("downloaded %v: only the older is downloaded", downloaded)
		}
		if len(ran) != 2 || ran[0][0] != "0.6.0=cand-mash" || ran[1][1] != "0.6.0=cand-mash" || ran[0][1] != "0.5.10=mash" {
			t.Errorf("ran %v", ran)
		}
		b, err := os.ReadFile(ev)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		plat, _ := got["platform"].(map[string]any)
		if got["older"] != "0.5.10" || got["newer"] != "0.6.0" || got["older_mash_sha256"] != hexSum(fakeBinary("v0.5.10")) ||
			got["newer_mash_sha256"] != hexSum("the candidate") || got["corpus_sha256"] != compatCorpusDigest() ||
			plat["goos"] != runtime.GOOS || plat["goarch"] != runtime.GOARCH || got["result"] != "passed" || got["exit_status"] != float64(0) || len(got) != 8 {
			t.Errorf("evidence %s", b)
		}
	}
	// a refusal is recorded too
	bad := fakeDrive(map[string]func([]compatObs) []compatObs{"0.6.0>0.5.10": func(o []compatObs) []compatObs { o[0].Copies = 0; return o }})
	if _, _, code := runCompat([]string{"--candidate", cand, "--evidence", ev, "0.5.10", "0.6.0"}, CompatOptions{Download: dl, Drive: bad}); code != 3 {
		t.Fatalf("exit %d", code)
	}
	b, _ := os.ReadFile(ev)
	if !strings.Contains(string(b), `"result": "refused"`) || !strings.Contains(string(b), `"exit_status": 3`) {
		t.Errorf("evidence %s", b)
	}
	// a checksum failure of the older is recorded as could not decide
	badSums := func(context.Context, string) ([]byte, error) { return nil, errors.New("gone") }
	if _, _, code := runCompat([]string{"--candidate", cand, "--evidence", ev, "0.5.10", "0.6.0"}, CompatOptions{Download: dl, Sums: badSums, Drive: drive}); code != 4 {
		t.Fatalf("exit %d", code)
	}
	b, _ = os.ReadFile(ev)
	if !strings.Contains(string(b), `"result": "could not decide"`) || !strings.Contains(string(b), `"exit_status": 4`) {
		t.Errorf("evidence %s", b)
	}
}

func TestCompatFlagUsage(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	for _, args := range [][]string{
		{"0.5.10", "0.6.0", "--candidate"},
		{"--candidate=", "0.5.10", "0.6.0"},
		{"0.5.10", "0.6.0", "--evidence"},
		{"--candidate", missing, "0.5.10", "0.6.0"},
		{"--candidate", t.TempDir(), "0.5.10", "0.6.0"}, // a directory is not a binary
	} {
		stdout, stderr, code := runCompat(args, CompatOptions{Download: fakeDownload(""), Drive: fakeDrive(nil)})
		if code != 2 || stdout != "" || !strings.HasPrefix(stderr, "error: --c") && !strings.HasPrefix(stderr, "error: --e") || strings.Count(stderr, "\n") != 1 {
			t.Errorf("%v: exit %d stdout %q stderr %q", args, code, stdout, stderr)
		}
	}
}

// H5: the repository of the downloads is derived as the main gate does: $RELEASE_CHECK_REPO, else origin on github.com,
// else bogworks/mash.
func TestCompatDefaultRepo(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "gh.log")
	gh := filepath.Join(dir, "gh")
	if err := os.WriteFile(gh, []byte("#!/bin/sh\necho \"$*\" >> "+log+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	repoOf := func() string {
		os.Remove(log)
		if _, _, code := runCompat([]string{"0.5.10", "0.6.0"}, CompatOptions{GH: gh, Sums: fakeSums, Drive: fakeDrive(nil)}); code != 4 {
			t.Fatalf("exit %d", code)
		}
		b, _ := os.ReadFile(log)
		m := regexp.MustCompile(`--repo (\S+)`).FindStringSubmatch(string(b))
		if m == nil {
			t.Fatalf("gh was called as %q", b)
		}
		return m[1]
	}
	checkout := t.TempDir()
	gitT(t, checkout, "init", "-q")
	t.Chdir(checkout)
	t.Setenv("RELEASE_CHECK_REPO", "")
	if got := repoOf(); got != "bogworks/mash" {
		t.Errorf("no origin: %s", got)
	}
	gitT(t, checkout, "remote", "add", "origin", "https://github.com/acme/fork.git")
	if got := repoOf(); got != "acme/fork" {
		t.Errorf("origin: %s", got)
	}
	t.Setenv("RELEASE_CHECK_REPO", "env/repo")
	if got := repoOf(); got != "env/repo" {
		t.Errorf("env: %s", got)
	}
}

// H2: the delivery path is observed through the channel notifications serve sends: their meta names the receiver and
// the messages it delivered.
func TestCompatSeqsOf(t *testing.T) {
	for _, c := range []struct {
		meta string
		want []string
	}{
		{`{"to":"reader","count":"2","seqs":"3,4"}`, []string{"3", "4"}},
		{`{"to":"reader","seqs":"7","wake":"true"}`, []string{"7"}},
		{`{"to":"other","seqs":"7"}`, nil},
		{`{"to":"reader"}`, nil},
		{``, nil},
		{`not json`, nil},
	} {
		if got := seqsOf(c.meta, "reader"); strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%q: %v, want %v", c.meta, got, c.want)
		}
	}
	p := &serveProc{anyEv: make(chan chanEvent, 8)}
	for _, m := range []string{`{"to":"reader","seqs":"3,4"}`, `{"to":"reader","seqs":"3"}`, `{"level":"warn"}`} {
		p.anyEv <- chanEvent{Meta: m}
	}
	counts := map[string]int{}
	p.collectSeqs("reader", counts)
	if counts["3"] != 2 || counts["4"] != 1 || len(counts) != 2 {
		t.Errorf("counts %v", counts)
	}
}

// H4: the wrapper changes to the repository root before it runs the gate, so it makes the caller's relative paths
// absolute first. The rewrite is run on its own, from a directory of the caller's.
func TestWrapper_relativePathsBecomeAbsolute(t *testing.T) {
	b, err := os.ReadFile("../release-check")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	i, j := strings.Index(text, "# BEGIN absolute-paths\n"), strings.Index(text, "# END absolute-paths\n")
	if i < 0 || j < i {
		t.Fatal("the wrapper has no absolute-paths block")
	}
	script := "set -eu\n" + text[i:j] + "printf '%s\\n' \"$@\"\n"
	caller, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"rec", "co", "out dir"} {
		if err := os.Mkdir(filepath.Join(caller, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(caller, "cand"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	abs := func(p string) string { return filepath.Join(caller, p) }
	for _, c := range []struct{ in, want []string }{
		{[]string{"--receipts", "rec", "abc", "0.6.0"}, []string{"--receipts", abs("rec"), "abc", "0.6.0"}},
		{[]string{"abc", "0.6.0", "--receipts=rec"}, []string{"abc", "0.6.0", "--receipts=" + abs("rec")}},
		{[]string{"--consumer", "co", "--receipts", "./rec", "abc", "0.6.0"}, []string{"--consumer", abs("co"), "--receipts", abs("rec"), "abc", "0.6.0"}},
		{[]string{"publish", "--receipts", "../" + filepath.Base(caller) + "/rec", "0.6.0", "abc"}, []string{"publish", "--receipts", abs("rec"), "0.6.0", "abc"}},
		{[]string{"publish", "0.6.0", "abc", "--receipts=rec"}, []string{"publish", "0.6.0", "abc", "--receipts=" + abs("rec")}},
		{[]string{"--receipts", "/already/abs", "abc", "0.6.0"}, []string{"--receipts", "/already/abs", "abc", "0.6.0"}},
		// a directory that does not exist is left for the gate to report
		{[]string{"--receipts", "nope", "abc", "0.6.0"}, []string{"--receipts", "nope", "abc", "0.6.0"}},
		{[]string{"--receipts=nope", "abc", "0.6.0"}, []string{"--receipts=nope", "abc", "0.6.0"}},
		{[]string{"abc", "0.6.0", "--receipts"}, []string{"abc", "0.6.0", "--receipts"}},
		{[]string{"--compat", "--candidate", "cand", "--evidence", "out dir/e.json", "0.5.10", "0.6.0"},
			[]string{"--compat", "--candidate", abs("cand"), "--evidence", abs("out dir/e.json"), "0.5.10", "0.6.0"}},
		{[]string{"--compat", "--candidate=cand", "--evidence=nodir/e.json", "0.5.10", "0.6.0"},
			[]string{"--compat", "--candidate=" + abs("cand"), "--evidence=nodir/e.json", "0.5.10", "0.6.0"}},
		{[]string{"--assets", "0.6.0", "abc"}, []string{"--assets", "0.6.0", "abc"}},
	} {
		cmd := exec.Command("sh", append([]string{"-c", script, "wrapper"}, c.in...)...)
		cmd.Dir = caller
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", c.in, err, out)
		}
		if got := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n"); strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
			t.Errorf("%v\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}
