package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// fakeDrive answers each direction (keyed "writer>reader") from a table; a direction not in it passes.
func fakeDrive(bad map[string]func([]compatObs) []compatObs) func(context.Context, compatBin, compatBin, []compatMsg) ([]compatObs, error) {
	return func(_ context.Context, w, r compatBin, corpus []compatMsg) ([]compatObs, error) {
		obs := make([]compatObs, len(corpus))
		for i, m := range corpus {
			obs[i] = compatObs{Number: m.Number, Sent: true, Seq: m.Number + 1, Copies: 1}
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
		return dir + "/mash", nil
	}
}

func runCompat(args []string, o CompatOptions) (stdout, stderr string, code int) {
	var so, se bytes.Buffer
	o.Out, o.ErrOut = &so, &se
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
		"missing":          {func(o []compatObs) []compatObs { o[2].Copies = 0; return o }, "message 3: missing"},
		"twice":            {func(o []compatObs) []compatObs { o[1].Copies = 2; return o }, "message 2: delivered twice"},
		"not delivered":    {func(o []compatObs) []compatObs { o[0].Sent = false; return o }, "message 1: not delivered"},
		"refused":          {func(o []compatObs) []compatObs { o[4].Refused = "version missing"; return o }, "message 5: refused: version missing"},
		"first of several": {func(o []compatObs) []compatObs { o[5].Copies = 0; o[3].Copies = 2; return o }, "message 4: delivered twice"},
		"absent":           {func(o []compatObs) []compatObs { return o[:2] }, "message 3: not delivered"},
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
