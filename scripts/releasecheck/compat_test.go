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
	"time"
)

// fakeDrive answers each direction (keyed "writer>reader") from a table; a direction not in it passes.
func fakeDrive(bad map[string]func([]compatObs) []compatObs) func(context.Context, compatBin, compatBin, []compatMsg) ([]compatObs, error) {
	return func(_ context.Context, w, r compatBin, corpus []compatMsg) ([]compatObs, error) {
		obs := make([]compatObs, len(corpus))
		for i, m := range corpus {
			obs[i] = compatObs{Number: m.Number, Sent: true, Seq: m.Number + 1, Copies: 1, Shown: 1}
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
		"missing":                {func(o []compatObs) []compatObs { o[2].Copies = 0; return o }, "message 3: missing"},
		"twice":                  {func(o []compatObs) []compatObs { o[1].Copies = 2; return o }, "message 2: delivered twice"},
		"not delivered":          {func(o []compatObs) []compatObs { o[0].Sent = false; return o }, "message 1: not delivered"},
		"refused":                {func(o []compatObs) []compatObs { o[4].Refused = "version missing"; return o }, "message 5: refused: version missing"},
		"stored but never shown": {func(o []compatObs) []compatObs { o[3].Shown = 0; return o }, "message 4: not delivered"},
		"shown twice":            {func(o []compatObs) []compatObs { o[1].Shown = 2; return o }, "message 2: delivered twice"},
		"missing from the inbox": {func(o []compatObs) []compatObs { o[2].Copies = 0; o[2].Shown = 1; return o }, "message 3: missing"},
		"first of several":       {func(o []compatObs) []compatObs { o[5].Copies = 0; o[3].Copies = 2; return o }, "message 4: delivered twice"},
		"absent":                 {func(o []compatObs) []compatObs { return o[:2] }, "message 3: not delivered"},
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

// H2: the delivery the session sees is what `mash hook` prints: shownCount counts the message blocks that carry a
// message's marker, in full-message form and in digest form, over the hook's JSON or raw text. A channel notification
// is not evidence of delivery, so a fyi message shown by the hook with no notification passes (Shown is all the judge
// reads), and a message the hook never showed, or showed twice (in one run or across two), fails.
func TestCompatShownCount(t *testing.T) {
	wrap := func(ctx string) string {
		b, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "UserPromptSubmit", "additionalContext": ctx}})
		return string(b)
	}
	full := "--- #5 from a.b.c\nkind: fyi\n> compat-1-ab\n> body\n--- #6 from a.b.c\nkind: task\n> compat-2-ab\n"
	digest := "#5\tfrom a.b.c\t\tkind fyi\tcompat-1-ab\n#6\tfrom a.b.c\t\tkind task\tcompat-2-ab\n"
	for _, c := range []struct {
		name, out, marker string
		want              int
	}{
		{"full, json", wrap(full), "compat-1-ab", 1},
		{"full, json, second", wrap(full), "compat-2-ab", 1},
		{"digest, json", wrap(digest), "compat-1-ab", 1},
		{"raw text", full, "compat-2-ab", 1},
		{"absent", wrap(full), "compat-9-ab", 0},
		{"nothing shown", "", "compat-1-ab", 0},
		{"shown twice in a run", wrap(full + full), "compat-1-ab", 2},
		{"two blocks mentioning it", wrap("--- #5 from a\n> compat-1-ab\n--- #6 from a\n> compat-1-ab\n"), "compat-1-ab", 2},
	} {
		if got := shownCount(c.out, c.marker); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
	// A message shown once by the first hook run and again by the second is shown twice.
	total := shownCount(wrap(full), "compat-1-ab") + shownCount(wrap(full), "compat-1-ab")
	obs := make([]compatObs, len(compatCorpus))
	for i, m := range compatCorpus {
		obs[i] = compatObs{Number: m.Number, Sent: true, Seq: m.Number + 1, Copies: 1, Shown: 1}
	}
	obs[0].Shown = total
	if n, reason := judgeCompat(compatCorpus, obs); n != compatCorpus[0].Number || reason != "delivered twice" {
		t.Errorf("shown on two hook runs: message %d %q", n, reason)
	}
	// fyi mail the hook showed (and no channel notification announced) is delivered.
	for i, m := range compatCorpus {
		if m.Kind == "fyi" {
			obs[0].Shown = 1
			obs[i].Shown = shownCount(wrap(full), "compat-1-ab")
		}
	}
	if n, reason := judgeCompat(compatCorpus, obs); n != 0 {
		t.Errorf("fyi shown once by the hook: message %d %q", n, reason)
	}
	// Shown zero times is not delivered.
	obs[1].Shown = shownCount("", "compat-2-ab")
	if n, reason := judgeCompat(compatCorpus, obs); n != compatCorpus[1].Number || reason != "not delivered" {
		t.Errorf("never shown: message %d %q", n, reason)
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

// M1: the reader's content is compared with the corpus body that was sent, not just the marker.
func TestCompatBodyIntact(t *testing.T) {
	wrap := func(ctx string) string {
		b, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "UserPromptSubmit", "additionalContext": ctx}})
		return string(b)
	}
	full := func(marker, body string) string {
		var q []string
		for _, l := range strings.Split(sentText(marker, body), "\n") {
			q = append(q, "> "+l)
		}
		return "--- #5 from a.b.c [verified] at 2026-01-01T00:00:00Z\nkind: task\n" + strings.Join(q, "\n") + "\n"
	}
	for _, m := range compatCorpus { // every corpus body, plain, multi-line, long and non-ASCII, passes intact
		mk := fmt.Sprintf("compat-%d-ab", m.Number)
		if !bodyIntact(wrap(full(mk, m.Body)), mk, m.Body) {
			t.Errorf("%s: intact body judged corrupted", m.What)
		}
	}
	long, uni := compatCorpus[5], compatCorpus[6]
	for _, c := range []struct {
		name, shown string
		m           compatMsg
	}{
		{"body changed", full("compat-1-ab", "Plain mail: tampered"), compatCorpus[0]},
		{"second line lost", full("compat-5-ab", "Is the second line kept?"), compatCorpus[4]},
		{"long truncated", full("compat-6-ab", long.Body[:len(long.Body)/2]), long},
		{"non-ASCII mangled", full("compat-7-ab", strings.ReplaceAll(uni.Body, "日本語", "????")), uni},
		{"non-ASCII replaced", full("compat-7-ab", strings.ReplaceAll(uni.Body, "é", "�")), uni},
		{"digest with wrong size", "#5\tfrom a.b.c [verified]\t9 B\tcompat-6-ab\tkind task\n", long},
	} {
		mk := fmt.Sprintf("compat-%d-ab", c.m.Number)
		if bodyIntact(wrap(c.shown), mk, c.m.Body) {
			t.Errorf("%s: corrupted body judged intact", c.name)
		}
	}
	// A digest (by design no body) with the right size passes; the long body is then compared in the stored inbox.
	n := len("compat-6-ab") + 1 + len(long.Body)
	if !bodyIntact(wrap(fmt.Sprintf("#5\tfrom a.b.c [verified]\t%d B\tcompat-6-ab\tkind task\n", n)), "compat-6-ab", long.Body) {
		t.Error("digest of the right size judged corrupted")
	}
	if bodyIntact("--- #5 from a\n> compat-6-ab\n> short\n", "compat-6-ab", long.Body) {
		t.Error("truncated stored copy judged intact")
	}
	obs := make([]compatObs, len(compatCorpus))
	for i, m := range compatCorpus {
		obs[i] = compatObs{Number: m.Number, Sent: true, Seq: m.Number + 1, Copies: 1, Shown: 1}
	}
	obs[6].Corrupted = true
	if n, reason := judgeCompat(compatCorpus, obs); n != 7 || reason != "corrupted" {
		t.Errorf("corrupted: message %d %q", n, reason)
	}
}

// M2: a hook that exits non-zero, on the delivery runs or on the required second run, is an error of the driver.
func TestCompatObserveHooksExit(t *testing.T) {
	mk := func(n int) string { return fmt.Sprintf("compat-%d-ab", n) }
	var all strings.Builder
	for _, m := range compatCorpus {
		var q []string
		for _, l := range strings.Split(sentText(mk(m.Number), m.Body), "\n") {
			q = append(q, "> "+l)
		}
		all.WriteString("--- #5 from a\nkind: task\n" + strings.Join(q, "\n") + "\n")
	}
	newObs := func() []compatObs {
		obs := make([]compatObs, len(compatCorpus))
		for i, m := range compatCorpus {
			obs[i] = compatObs{Number: m.Number, Sent: true, Seq: m.Number + 1, Copies: 1}
		}
		return obs
	}
	// The first run shows everything; the second shows nothing new.
	scripted := func(codes ...int) func() (string, int, error) {
		i := 0
		return func() (string, int, error) {
			c := codes[i]
			i++
			if i == 1 {
				return all.String(), c, nil
			}
			return "", c, nil
		}
	}
	obs := newObs()
	if err := observeHooks(context.Background(), scripted(0, 0), obs, compatCorpus, mk, time.Second, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if n, reason := judgeCompat(compatCorpus, obs); n != 0 {
		t.Errorf("clean runs: message %d %q", n, reason)
	}
	for _, c := range []struct {
		name  string
		codes []int
	}{{"first run", []int{3, 0}}, {"second run", []int{0, 1}}} {
		err := observeHooks(context.Background(), scripted(c.codes...), newObs(), compatCorpus, mk, time.Second, time.Millisecond)
		if err == nil || !strings.Contains(err.Error(), "exited") {
			t.Errorf("hook non-zero on the %s: err %v", c.name, err)
		}
	}
	// A corrupted body shown by the hook is recorded.
	bad := strings.Replace(all.String(), "kept?", "kept!", 1)
	obs = newObs()
	run := func() (string, int, error) { return bad, 0, nil }
	if err := observeHooks(context.Background(), run, obs, compatCorpus, mk, time.Second, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if !obs[4].Corrupted || obs[0].Corrupted {
		t.Errorf("corrupted flags: %+v", obs)
	}
}
