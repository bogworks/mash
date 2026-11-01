package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// consumerScript is the entry point the test manifest names; testManifest is the consumer's committed manifest.
const consumerScript = "ci/check.sh"

const testManifest = `# the consumer's check of mash
command='ci/check.sh'
bin_env=MASH_CONSUMER_BIN
plugin_env=MASH_CONSUMER_PLUGIN
set_env=MASH_CONSUMER_SET
set="mash"
results_env=MASH_CONSUMER_RESULTS
marketplace_env=MASH_CONSUMER_MARKETPLACE
drop_env=MASH_CONSUMER_DROP, OTHER_DROP
parts=PartA,PartB,PartC,PartD
`

var testParts = []string{"PartA", "PartB", "PartC", "PartD"}

const consumerStepName = "the consumer's check passes against the candidate"

func TestParseArgs(t *testing.T) {
	cases := []struct {
		args       []string
		ok         bool
		assets     bool
		consumer   string
		x, version string
	}{
		{args: []string{"X", "0.1.0"}, ok: true, x: "X", version: "0.1.0"},
		{args: []string{"--assets", "0.1.0", "X"}, ok: true, assets: true, x: "X", version: "0.1.0"},
		{args: []string{"--consumer", "/rh", "X", "0.1.0"}, ok: true, consumer: "/rh", x: "X", version: "0.1.0"},
		{args: []string{}},
		{args: []string{"--consumer"}},
		{args: []string{"--consumer", "/rh"}},
		{args: []string{"--consumer", "/rh", "X"}},
		{args: []string{"--consumer", "/rh", "X", "0.1.0", "extra"}},
		{args: []string{"--assets", "0.1.0"}},
		{args: []string{"--assets", "--consumer", "/rh", "X"}},
		{args: []string{"--consumer", "/rh", "--assets", "X"}},
		{args: []string{"--consumer", "--assets", "X", "0.1.0"}},
		{args: []string{"--assets", "0.1.0", "--consumer"}},
		{args: []string{"X", "--consumer", "/rh", "0.1.0"}}, // the flag is only the first argument: three arguments, not two
	}
	for _, c := range cases {
		o, assets, err := parseArgs(c.args)
		if (err == nil) != c.ok {
			t.Errorf("%v: err %v, want ok=%v", c.args, err, c.ok)
			continue
		}
		if !c.ok {
			continue
		}
		if assets != c.assets || o.Consumer != c.consumer || o.X != c.x || o.Version != c.version {
			t.Errorf("%v: assets %v consumer %q x %q version %q", c.args, assets, o.Consumer, o.X, o.Version)
		}
	}
	var out bytes.Buffer
	for _, args := range [][]string{{"--consumer", "/rh", "X"}, {"--consumer", "/rh", "X", "0.1"}, {"--assets", "--consumer", "/rh", "X"}} {
		if got := mainRun(args, &out); got != 2 {
			t.Errorf("%v: exit %d, want 2", args, got)
		}
	}
}

// consumerCheckout is a consumer checkout: v0.3.0 and v0.3.1 tagged, then a commit after the newest tag, and a tag on a
// side branch that HEAD does not reach.
func consumerCheckout(t *testing.T) (dir, head string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "consumer")
	gitT(t, filepath.Dir(dir), "init", "-q", "-b", "main", dir)
	commit := func(name string) {
		write(t, dir, name, name)
		gitT(t, dir, "add", name)
		gitT(t, dir, "commit", "-q", "-m", name)
	}
	commit("a")
	gitT(t, dir, "tag", "v0.3.0")
	commit("b")
	gitT(t, dir, "tag", "v0.3.1")
	gitT(t, dir, "tag", "not-a-release")
	gitT(t, dir, "checkout", "-q", "-b", "side")
	commit("side")
	gitT(t, dir, "tag", "v9.9.9")
	gitT(t, dir, "checkout", "-q", "main")
	commit("c")
	write(t, dir, ".mash-consumer-check", testManifest)
	write(t, dir, consumerScript, "exit 0\n")
	gitT(t, dir, "add", ".mash-consumer-check", consumerScript)
	gitT(t, dir, "commit", "-q", "-m", "manifest")
	return dir, gitT(t, dir, "rev-parse", "HEAD")
}

func TestConsumerInputs(t *testing.T) {
	dir, head := consumerCheckout(t)
	got, rx, v, err := consumerInputs(dir)
	if err != nil || got != dir || rx != head || v != "0.3.1" {
		t.Fatalf("got %q %q %q %v, want %q %q 0.3.1", got, rx, v, err, dir, head)
	}
	// A relative path is made absolute.
	wd, _ := os.Getwd()
	if rel, err := filepath.Rel(wd, dir); err == nil {
		if got, _, _, err := consumerInputs(rel); err != nil || got != dir {
			t.Errorf("relative %q: got %q %v, want %q", rel, got, err, dir)
		}
	}
	// A checkout at the tag itself, and one at a detached older commit.
	gitT(t, dir, "checkout", "-q", "--detach", "v0.3.0")
	if _, _, v, err := consumerInputs(dir); err != nil || v != "0.3.0" {
		t.Errorf("at v0.3.0: %q %v", v, err)
	}
	// No release tag reachable, a missing checkout and a directory that is not a repository are the gate's failures.
	bare := filepath.Join(t.TempDir(), "r")
	gitT(t, filepath.Dir(bare), "init", "-q", "-b", "main", bare)
	write(t, bare, "f", "f")
	gitT(t, bare, "add", "f")
	gitT(t, bare, "commit", "-q", "-m", "f")
	gitT(t, bare, "tag", "not-a-release")
	for name, d := range map[string]string{"no v tag": bare, "missing": filepath.Join(t.TempDir(), "nope"), "not a repository": t.TempDir()} {
		_, _, _, err := consumerInputs(d)
		var oe opError
		if !errors.As(err, &oe) {
			t.Errorf("%s: err %v, want an op error", name, err)
		}
	}
}

func TestConsumerInputs_highestReachableTagNotTheNearest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "consumer")
	gitT(t, filepath.Dir(dir), "init", "-q", "-b", "main", dir)
	commit := func(name string) {
		write(t, dir, name, name)
		gitT(t, dir, "add", name)
		gitT(t, dir, "commit", "-q", "-m", name)
	}
	commit("base")
	gitT(t, dir, "checkout", "-q", "-b", "high")
	commit("high")
	gitT(t, dir, "tag", "v1.10.0")
	gitT(t, dir, "checkout", "-q", "main")
	commit("low")
	gitT(t, dir, "tag", "v1.9.0")
	gitT(t, dir, "tag", "v2.0.0-rc1") // not a release
	gitT(t, dir, "merge", "-q", "--no-ff", "-m", "merge", "high")
	commit("after")
	if _, _, v, err := consumerInputs(dir); err != nil || v != "1.10.0" {
		t.Fatalf("got %q %v, want 1.10.0", v, err)
	}
}

// consumerFixture is a check fixture with a consumer checkout, the runner faked: results is what it writes to
// MASH_CONSUMER_RESULTS (nil: no file), code and err what it returns.
type consumerFixture struct {
	*fixture
	dir, head string
	calls     int
	gotDir    string
	gotScript string
	gotArgs   []string
	gotEnv    []string
	gotBin    []byte // MASH_CONSUMER_BIN's content, read while the runner runs
	gotHead   string // the commit checked out in the runner's directory, and
	gotFile   string // the content of the checkout's file "a" there, both read while the runner runs
	results   *string
	code      int
	err       error
}

// mashSetPass is a start and a pass line for each part of the mash set, the minimum a passing run leaves.
func mashSetPass(cand string) string {
	ids := map[string]string{"PartA": "V3", "PartB": "V10", "PartC": "V11", "PartD": "V23"}
	var b strings.Builder
	for _, p := range testParts {
		for _, r := range []string{"start", "pass"} {
			b.WriteString(`{"id":"` + ids[p] + `","tier":"c","part":"` + p + `","result":"` + r + `","candidate_mash":"` + cand + `"}` + "\n")
		}
	}
	return b.String()
}

func newConsumerFixture(t *testing.T) *consumerFixture {
	t.Helper()
	h := &consumerFixture{fixture: newFixture(t), code: 0}
	h.dir, h.head = consumerCheckout(t)
	h.o.Consumer = h.dir
	h.results = strp(mashSetPass("@BIN@"))
	h.o.consumer = func(_ context.Context, dir, script string, args, env []string, _ io.Writer) (int, error) {
		h.calls++
		h.gotDir, h.gotScript, h.gotArgs, h.gotEnv = dir, script, args, env
		t.Cleanup(func() { os.RemoveAll(filepath.Dir(h.env("MASH_CONSUMER_RESULTS"))) }) // the step keeps it for the operator
		h.gotBin, _ = os.ReadFile(h.env("MASH_CONSUMER_BIN"))
		h.gotHead, _ = git(dir, "rev-parse", "HEAD")
		b, _ := os.ReadFile(filepath.Join(dir, "a"))
		h.gotFile = string(b)
		if h.results != nil { // "@BIN@" is the stamp a faithful consumer writes: the binary it was given
			res := strings.ReplaceAll(*h.results, "@BIN@", h.env("MASH_CONSUMER_BIN"))
			if err := os.WriteFile(h.env("MASH_CONSUMER_RESULTS"), []byte(res), 0o644); err != nil {
				t.Error(err)
			}
		}
		return h.code, h.err
	}
	return h
}

// env is the last value of the variable the runner was given.
func (h *consumerFixture) env(k string) string {
	v := ""
	for _, kv := range h.gotEnv {
		if strings.HasPrefix(kv, k+"=") {
			v = strings.TrimPrefix(kv, k+"=")
		}
	}
	return v
}

func (h *consumerFixture) out() string { return h.o.Out.(*bytes.Buffer).String() }

func TestCheck_consumerVerdict(t *testing.T) {
	line := func(id, part, result, cand string) string {
		return `{"id":"` + id + `","tier":"c","part":"` + part + `","result":"` + result + `","candidate_mash":"` + cand + `"}` + "\n"
	}
	cases := []struct {
		name    string
		code    int
		results *string // nil: no file
		runErr  error
		want    int    // the gate's exit code: 0 passed, 3 refused, 4 could not decide
		says    string // what the failure must say
	}{
		{name: "0 with passing lines", code: 0, results: strp(mashSetPass("@BIN@")), want: 0},
		{name: "0 with one part of the set missing", code: 0, results: strp(strings.ReplaceAll(mashSetPass("@BIN@"), "PartC", "TestV11_Other")), want: 3, says: "PartC did not run"},
		{name: "0 with only V3", code: 0, results: strp(line("V3", "PartA", "pass", "@BIN@")), want: 3, says: "PartD did not run"},
		{name: "0 with an empty file", code: 0, results: strp(""), want: 3, says: "empty"},
		{name: "0 with blank lines only", code: 0, results: strp("\n\n"), want: 3, says: "empty"},
		{name: "0 with no file", code: 0, results: nil, want: 3, says: "no results file"},
		{name: "0 with a fail line", code: 0, results: strp(mashSetPass("@BIN@") + line("V3", "P1", "pass", "@BIN@") + line("V10", "P2", "fail", "@BIN@")), want: 3, says: "V10/P2 ended"},
		{name: "0 with a skip line", code: 0, results: strp(mashSetPass("@BIN@") + line("V3", "P1", "skip", "@BIN@")), want: 3, says: "V3/P1 ended"},
		{name: "0 with a part that only started", code: 0, results: strp(mashSetPass("@BIN@") + line("V3", "P1", "start", "@BIN@")), want: 3, says: "V3/P1 never passed"},
		{name: "0 with a line that names no candidate", code: 0, results: strp(mashSetPass("@BIN@") + line("V3", "P1", "pass", "@BIN@") + line("V10", "P2", "pass", "")), want: 3, says: "V10/P2 has no candidate_mash"},
		{name: "0 with an unreadable line", code: 0, results: strp("not json\n"), want: 3, says: "unreadable"},
		{name: "3 names the failing parts and the results", code: 3, results: strp(line("V3", "P1", "pass", "@BIN@") + line("V11", "PartC", "fail", "@BIN@")), want: 3, says: "V11/PartC fail"},
		{name: "3 without a failing part is a setup failure: could not decide", code: 3, results: nil, want: 4, says: "no failing part could be read"},
		{name: "2 is usage: could not decide", code: 2, results: nil, want: 4, says: "exited 2"},
		{name: "7 is a failure of the run", code: 7, results: strp(line("V3", "P1", "fail", "@BIN@")), want: 4, says: "exited 7"},
		{name: "failure to start", code: 0, results: nil, runErr: errors.New("starting the consumer runner: no sh"), want: 4, says: "no sh"},
		{name: "a run that does not finish", code: 0, results: nil, runErr: context.DeadlineExceeded, want: 4, says: "deadline exceeded"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newConsumerFixture(t)
			h.code, h.results, h.err = c.code, c.results, c.runErr
			err := h.check()
			if got := exitCode(err); got != c.want {
				t.Fatalf("exit %d, want %d: %v\n%s", got, c.want, err, h.out())
			}
			if c.want == 0 {
				if !strings.Contains(h.out(), "ok    "+consumerStepName) {
					t.Fatalf("no ok line for the step:\n%s", h.out())
				}
				return
			}
			if !strings.Contains(err.Error(), c.says) || !strings.Contains(err.Error(), consumerStepName) {
				t.Fatalf("the failure does not say %q under the step:\n%v", c.says, err)
			}
			if c.code == 3 && !strings.Contains(err.Error(), h.env("MASH_CONSUMER_RESULTS")) {
				t.Fatalf("a refusal must name the results path %s:\n%v", h.env("MASH_CONSUMER_RESULTS"), err)
			}
			if !strings.Contains(h.out(), h.env("MASH_CONSUMER_RESULTS")) { // printed whatever the outcome
				t.Fatalf("the results path was not printed:\n%s", h.out())
			}
		})
	}
}

func TestCheck_consumerIsPassedTheCandidateNotTheRunningCheckout(t *testing.T) {
	h := newConsumerFixture(t)
	if err := h.check(); err != nil {
		t.Fatal(err)
	}
	if h.calls != 1 {
		t.Fatalf("the runner ran %d times", h.calls)
	}
	if h.gotDir == h.dir || h.gotHead != h.head || strings.Join(h.gotArgs, " ") != h.head+" 0.3.1" {
		t.Errorf("ran in %q at %q with %q, want a clone of %q at %q with %q", h.gotDir, h.gotHead, h.gotArgs, h.dir, h.head, h.head+" 0.3.1")
	}
	if _, err := os.Stat(h.gotDir); !os.IsNotExist(err) {
		t.Errorf("the runner's clone survived the run: %v", err)
	}
	for _, want := range []string{"consumer checkout " + h.dir, h.head, "v0.3.1", "fresh clone " + h.gotDir + " at commit " + h.head} {
		if !strings.Contains(h.out(), want) {
			t.Errorf("the output does not name %q:\n%s", want, h.out())
		}
	}
	bin, plugin, mk, res := h.env("MASH_CONSUMER_BIN"), h.env("MASH_CONSUMER_PLUGIN"), h.env("MASH_CONSUMER_MARKETPLACE"), h.env("MASH_CONSUMER_RESULTS")
	if h.env("MASH_CONSUMER_SET") != "mash" {
		t.Errorf("MASH_CONSUMER_SET=%q", h.env("MASH_CONSUMER_SET"))
	}
	for k, v := range map[string]string{"MASH_CONSUMER_BIN": bin, "MASH_CONSUMER_PLUGIN": plugin, "MASH_CONSUMER_MARKETPLACE": mk, "MASH_CONSUMER_RESULTS": res} {
		if !filepath.IsAbs(v) {
			t.Errorf("%s=%q is not absolute", k, v)
		}
	}
	// On a linux host the candidate's own host build is the container's binary: one build, not two.
	if runtime.GOOS == "linux" && string(h.gotBin) != goodMash {
		t.Errorf("MASH_CONSUMER_BIN held %q, want the candidate's build", h.gotBin)
	}
	// The plugin and marketplace are a fresh clone of X, never the checkout running the gate.
	if plugin != filepath.Join(mk, "plugin") || strings.HasPrefix(mk, h.repo) || mk == h.repo {
		t.Errorf("plugin %q, marketplace %q: want plugin/ of a clone other than %s", plugin, mk, h.repo)
	}
	// Both are gone with the run; the results are not.
	if _, err := os.Stat(mk); !os.IsNotExist(err) {
		t.Errorf("the clone survived the run: %v", err)
	}
	if _, err := os.Stat(res); err != nil {
		t.Errorf("the results did not survive the run: %v", err)
	}
	for _, kv := range h.gotEnv {
		if strings.HasPrefix(kv, "MASH_CONSUMER_DROP=") || strings.Contains(kv, "RECEIPT") {
			t.Errorf("the runner was given %q", kv)
		}
	}
}

func TestCheck_consumerEnvironmentKeepsOursAndDropsInheritedCopiesOfTheFive(t *testing.T) {
	t.Setenv("MASH_CONSUMER_BIN", "/inherited")
	t.Setenv("MASH_CONSUMER_SET", "other")
	t.Setenv("KEEP_ME", "yes")
	t.Setenv("MASH_CONSUMER_DROP", "PartA")
	h := newConsumerFixture(t)
	if err := h.check(); err != nil {
		t.Fatal(err)
	}
	n := map[string]int{}
	for _, kv := range h.gotEnv {
		k, _, _ := strings.Cut(kv, "=")
		n[k]++
	}
	for _, k := range []string{"MASH_CONSUMER_BIN", "MASH_CONSUMER_PLUGIN", "MASH_CONSUMER_SET", "MASH_CONSUMER_RESULTS", "MASH_CONSUMER_MARKETPLACE"} {
		if n[k] != 1 {
			t.Errorf("%s is given %d times, want once", k, n[k])
		}
	}
	if got := consumerEnv([]string{"MASH_CONSUMER_DROP=x", "KEEP_ME=yes"}, mustManifest(t), "b", "p", "m", "r"); slices.Contains(got, "MASH_CONSUMER_DROP=x") || !slices.Contains(got, "KEEP_ME=yes") {
		t.Errorf("an inherited MASH_CONSUMER_DROP must be dropped: %v", got)
	}
	if h.env("MASH_CONSUMER_DROP") != "" {
		t.Errorf("MASH_CONSUMER_DROP reached the runner: %q", h.env("MASH_CONSUMER_DROP"))
	}
	if h.env("MASH_CONSUMER_BIN") == "/inherited" || h.env("MASH_CONSUMER_SET") != "mash" || h.env("KEEP_ME") != "yes" {
		t.Errorf("env: bin %q set %q keep %q", h.env("MASH_CONSUMER_BIN"), h.env("MASH_CONSUMER_SET"), h.env("KEEP_ME"))
	}
}

func TestCheck_withoutTheFlagThereIsNoConsumerStep(t *testing.T) {
	h := newConsumerFixture(t)
	h.o.Consumer = ""
	if err := h.check(); err != nil {
		t.Fatal(err)
	}
	if h.calls != 0 || strings.Contains(h.out(), "consumer") {
		t.Fatalf("the runner ran %d times without the flag:\n%s", h.calls, h.out())
	}
	if !strings.Contains(h.out(), "ok    fresh-session mail arrives no slower") {
		t.Fatalf("the other steps did not run:\n%s", h.out())
	}
}

// The consumer step is the last one, after the latency step.
func TestCheck_theConsumerStepComesLast(t *testing.T) {
	h := newConsumerFixture(t)
	if err := h.check(); err != nil {
		t.Fatal(err)
	}
	out := h.out()
	if a, b := strings.Index(out, "ok    fresh-session mail"), strings.Index(out, "ok    "+consumerStepName); a < 0 || b < a {
		t.Fatalf("the steps are out of order:\n%s", out)
	}
}

func TestCheck_consumerOnANonLinuxHostBuildsTheCandidateForLinux(t *testing.T) {
	h := newConsumerFixture(t)
	h.o.hostGOOS = "darwin"
	var targets []string
	h.o.build = func(_ context.Context, _, out, goos, goarch, version, commit string) (string, error) {
		targets = append(targets, goos+"/"+goarch)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return "", err
		}
		body := "#!/bin/sh\necho 'mash " + version + " (commit " + commit + ")'\n" // the host build must run: reproducible() runs it
		if goos == "linux" {
			body = "linux build " + goos + "/" + goarch
		}
		return "a", os.WriteFile(out, []byte(body), 0o755)
	}
	builds := 0
	cand := h.o.candBinary
	h.o.candBinary = func(ctx context.Context, dir string) (string, error) { builds++; return cand(ctx, dir) }
	if err := h.check(); err != nil {
		t.Fatal(err)
	}
	// reproducible() builds the host twice; this step builds linux once more, on the host's architecture.
	if want := runtime.GOARCH; targets[len(targets)-1] != "linux/"+want {
		t.Fatalf("builds %v, want the last for linux/%s", targets, want)
	}
	if string(h.gotBin) != "linux build linux/"+runtime.GOARCH {
		t.Fatalf("MASH_CONSUMER_BIN held %q, want the linux build", h.gotBin)
	}
	if builds != 1 {
		t.Fatalf("the host candidate was built %d times", builds)
	}
}

func TestCheck_aLinuxBuildThatFailsIsNotRunAndNamesTheStep(t *testing.T) {
	h := newConsumerFixture(t)
	h.o.hostGOOS = "darwin"
	h.o.build = func(_ context.Context, _, out, goos, _, _, _ string) (string, error) {
		if goos == "linux" {
			return "", errors.New("linux build broke")
		}
		return "a", os.WriteFile(out, []byte(goodMash), 0o755)
	}
	err := h.check()
	if err == nil || !strings.Contains(err.Error(), "linux build broke") || h.calls != 0 {
		t.Fatalf("err %v, runner called %d times", err, h.calls)
	}
}

func TestCheck_consumerIsNotRunWithoutACandidateBuild(t *testing.T) {
	h := newConsumerFixture(t)
	h.o.candBinary = func(context.Context, string) (string, error) { return "", errors.New("build failed") }
	err := h.check()
	if err == nil || h.calls != 0 || !strings.Contains(err.Error(), "not run") {
		t.Fatalf("err %v, runner called %d times", err, h.calls)
	}
}

func TestCheck_aConsumerCheckoutWithoutATagIsFourAndNotRun(t *testing.T) {
	h := newConsumerFixture(t)
	bare := filepath.Join(t.TempDir(), "r")
	gitT(t, filepath.Dir(bare), "init", "-q", "-b", "main", bare)
	write(t, bare, "f", "f")
	gitT(t, bare, "add", "f")
	gitT(t, bare, "commit", "-q", "-m", "f")
	h.o.Consumer = bare
	err := h.check()
	if got := exitCode(err); got != 4 || h.calls != 0 {
		t.Fatalf("exit %d (%v), runner called %d times", got, err, h.calls)
	}
}

func TestCheck_aConsumerRunThatOutlastsItsDeadlineIsFour(t *testing.T) {
	h := newConsumerFixture(t)
	h.o.consumerTimeout = 50 * time.Millisecond
	var ctxErr error
	h.o.consumer = func(ctx context.Context, _, _ string, _, env []string, _ io.Writer) (int, error) {
		<-ctx.Done()
		ctxErr = ctx.Err()
		return 0, ctx.Err()
	}
	err := h.check()
	if exitCode(err) != 4 || !errors.Is(ctxErr, context.DeadlineExceeded) {
		t.Fatalf("exit %d (%v), ctx %v", exitCode(err), err, ctxErr)
	}
}

// The default runner: `sh <script> <RX> <RVersion>` in the checkout with exactly the given environment, output
// streamed to the writer, the exit code returned as it is.
func TestRunConsumer_runsTheScriptInTheCheckoutAndStreamsItsOutput(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, consumerScript, "echo \"args=$*\"; echo \"pwd=$(pwd)\"; echo \"v=$FOO\"; echo err >&2; exit 3\n")
	var out bytes.Buffer
	code, err := runConsumer(context.Background(), dir, consumerScript, []string{"abc", "0.3.1"}, []string{"PATH=" + os.Getenv("PATH"), "FOO=bar"}, &out)
	if err != nil || code != 3 {
		t.Fatalf("code %d err %v", code, err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	for _, want := range []string{"args=abc 0.3.1", "pwd=" + real, "v=bar", "err"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
	if code, err := runConsumer(context.Background(), filepath.Join(dir, "nope"), consumerScript, nil, nil, &out); err == nil {
		t.Errorf("a missing directory started: code %d", code)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	write(t, dir, consumerScript, "sleep 30\n")
	if _, err := runConsumer(ctx, dir, consumerScript, nil, []string{"PATH=" + os.Getenv("PATH")}, &out); err == nil {
		t.Errorf("a run past its deadline reported no error")
	}
}

// Uncommitted changes in the checkout (the runner builds from the tree it runs in) must not reach the run.
func TestCheck_consumerRunsFromACleanCloneNotTheDirtyCheckout(t *testing.T) {
	h := newConsumerFixture(t)
	write(t, h.dir, "a", "edited, uncommitted")
	write(t, h.dir, consumerScript, "echo untracked script")
	if err := h.check(); err != nil {
		t.Fatal(err)
	}
	if h.gotFile != "a" || h.gotHead != h.head {
		t.Errorf("the run saw file a = %q at %q, want the committed %q at %q", h.gotFile, h.gotHead, "a", h.head)
	}
	if _, err := os.Stat(filepath.Join(h.gotDir, consumerScript)); err == nil {
		t.Errorf("the run dir has the checkout's untracked script")
	}
	if b, _ := os.ReadFile(filepath.Join(h.dir, "a")); string(b) != "edited, uncommitted" {
		t.Errorf("the checkout was touched: %q", b)
	}
}

func TestCheck_consumerRefusesResultsStampedWithAnotherMash(t *testing.T) {
	h := newConsumerFixture(t)
	h.results = strp(strings.Replace(mashSetPass("@BIN@"), `"candidate_mash":"@BIN@"`, `"candidate_mash":"/usr/bin/mash"`, 1) + mashSetPass("/also/other"))
	err := h.check()
	if exitCode(err) != 3 || !strings.Contains(err.Error(), `"/usr/bin/mash", not the candidate "`+h.env("MASH_CONSUMER_BIN")+`"`) {
		t.Fatalf("exit %d: %v", exitCode(err), err)
	}
	// A relative stamp is not the absolute path either.
	h = newConsumerFixture(t)
	h.results = strp(mashSetPass("mash"))
	if err := h.check(); exitCode(err) != 3 {
		t.Fatalf("exit %d: %v", exitCode(err), err)
	}
	if !filepath.IsAbs(h.env("MASH_CONSUMER_BIN")) || filepath.Clean(h.env("MASH_CONSUMER_BIN")) != h.env("MASH_CONSUMER_BIN") {
		t.Errorf("MASH_CONSUMER_BIN %q is not absolute and clean", h.env("MASH_CONSUMER_BIN"))
	}
}

func mustManifest(t *testing.T) consumerManifest {
	t.Helper()
	m, err := parseConsumerManifest(testManifest)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestParseConsumerManifest(t *testing.T) {
	m := mustManifest(t)
	if m.Command != "ci/check.sh" || m.BinEnv != "MASH_CONSUMER_BIN" || m.PluginEnv != "MASH_CONSUMER_PLUGIN" || m.SetEnv != "MASH_CONSUMER_SET" ||
		m.Set != "mash" || m.ResultsEnv != "MASH_CONSUMER_RESULTS" || m.MarketplaceEnv != "MASH_CONSUMER_MARKETPLACE" ||
		!slices.Equal(m.DropEnv, []string{"MASH_CONSUMER_DROP", "OTHER_DROP"}) || !slices.Equal(m.Parts, testParts) {
		t.Errorf("parsed %+v", m)
	}
	base := strings.ReplaceAll(testManifest, "marketplace_env=MASH_CONSUMER_MARKETPLACE\n", "")
	if m, err := parseConsumerManifest(base); err != nil || m.MarketplaceEnv != "" {
		t.Errorf("marketplace_env is optional: %+v %v", m, err)
	}
	for name, c := range map[string]struct{ from, to, says string }{
		"no command":       {"command='ci/check.sh'\n", "", "command is required"},
		"no bin_env":       {"bin_env=MASH_CONSUMER_BIN\n", "", "bin_env is required"},
		"no parts":         {"parts=PartA,PartB,PartC,PartD\n", "", "parts is required"},
		"unknown key":      {"set=", "sett=", "unknown key"},
		"not key=value":    {"parts=", "parts\nparts=", "want key=value"},
		"duplicate":        {"set=\"mash\"\n", "set=a\nset=b\n", "given twice"},
		"absolute command": {"'ci/check.sh'", "/bin/sh", "inside the checkout"},
		"escaping command": {"'ci/check.sh'", "../x.sh", "inside the checkout"},
		"bad env name":     {"bin_env=MASH_CONSUMER_BIN", "bin_env=A-B", "not an environment variable name"},
		"shared env":       {"plugin_env=MASH_CONSUMER_PLUGIN", "plugin_env=MASH_CONSUMER_BIN", "named for two values"},
	} {
		if _, err := parseConsumerManifest(strings.Replace(testManifest, c.from, c.to, 1)); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: err %v, want %q", name, err, c.says)
		}
	}
}

func TestCheck_consumerManifestIsHandedOver(t *testing.T) {
	h := newConsumerFixture(t)
	if err := h.check(); err != nil {
		t.Fatal(err)
	}
	if h.gotScript != consumerScript || h.env("MASH_CONSUMER_SET") != "mash" || len(h.gotArgs) != 2 || h.gotArgs[0] != h.head || h.gotArgs[1] != "0.3.1" {
		t.Errorf("script %q set %q args %v", h.gotScript, h.env("MASH_CONSUMER_SET"), h.gotArgs)
	}
}

func TestCheck_aConsumerCheckoutWithoutAManifestIsFourAndNotRun(t *testing.T) {
	h := newConsumerFixture(t)
	gitT(t, h.dir, "rm", "-q", ".mash-consumer-check")
	gitT(t, h.dir, "commit", "-q", "-m", "drop manifest")
	err := h.check()
	if got := exitCode(err); got != 4 || h.calls != 0 || !strings.Contains(err.Error(), ".mash-consumer-check") {
		t.Fatalf("exit %d (%v), runner called %d times", got, err, h.calls)
	}
}

func TestCheck_aManifestNamingNoScriptIsFourAndNotRun(t *testing.T) {
	h := newConsumerFixture(t)
	gitT(t, h.dir, "rm", "-q", consumerScript)
	gitT(t, h.dir, "commit", "-q", "-m", "drop script")
	err := h.check()
	if got := exitCode(err); got != 4 || h.calls != 0 || !strings.Contains(err.Error(), "not a file") {
		t.Fatalf("exit %d (%v), runner called %d times", got, err, h.calls)
	}
}

func TestCheck_aManifestOnlyInTheDirtyCheckoutIsNotUsed(t *testing.T) {
	h := newConsumerFixture(t)
	gitT(t, h.dir, "rm", "-q", ".mash-consumer-check")
	gitT(t, h.dir, "commit", "-q", "-m", "drop manifest")
	write(t, h.dir, ".mash-consumer-check", testManifest) // untracked: the run is from a clean clone
	if err := h.check(); exitCode(err) != 4 || h.calls != 0 {
		t.Fatalf("err %v, runner called %d times", err, h.calls)
	}
}
