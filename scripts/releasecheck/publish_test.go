package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fakeWorld records every effect, and serves a scripted state.
type fakeWorld struct {
	tagAt      string // "" none
	release    string // relNone, relDraft, relPublished
	draftAfter int    // Release polls that say none before the draft appears (0: as scripted)
	wfFailed   bool
	preErr     error
	assetsErr  error
	publishErr error
	orderErr   error // what Order says once the assets gate has run (another version published meanwhile)
	assetsRan  bool
	tagErr     error
	now        time.Time
	calls      []string
	polls      int
	cutover    string // the cutover version Keyless reports ("": none)
	keyless    bool   // the tag is at or above the cutover: its tag is pushed by the minting service
}

func newFake() *fakeWorld { return &fakeWorld{release: relNone, now: time.Unix(1000, 0)} }

func (f *fakeWorld) RemoteTag(tag string) (bool, string, error) {
	f.calls = append(f.calls, "remotetag")
	return f.tagAt != "", f.tagAt, nil
}
func (f *fakeWorld) Release(tag string) (string, error) {
	f.calls = append(f.calls, "release")
	if f.release == relNone && f.draftAfter > 0 && f.polls >= f.draftAfter {
		f.release = relDraft
	}
	f.polls++
	return f.release, nil
}
func (f *fakeWorld) WorkflowFailed(tag string) (bool, error) {
	f.calls = append(f.calls, "workflow")
	return f.wfFailed, nil
}
func (f *fakeWorld) PreTag() error { f.calls = append(f.calls, "pretag"); return f.preErr }
func (f *fakeWorld) CreateTag(tag, x string) error {
	f.calls = append(f.calls, "createtag")
	if f.tagErr == nil {
		f.tagAt = x
	}
	return f.tagErr
}
func (f *fakeWorld) Keyless(tag, x string) (string, bool, error) {
	f.calls = append(f.calls, "keyless")
	return f.cutover, f.keyless, nil
}
func (f *fakeWorld) Assets() error {
	f.calls = append(f.calls, "assets")
	f.assetsRan = true
	return f.assetsErr
}
func (f *fakeWorld) Order(x string) error {
	f.calls = append(f.calls, "order")
	if f.assetsRan {
		return f.orderErr
	}
	return nil
}
func (f *fakeWorld) Publish(tag string) error {
	f.calls = append(f.calls, "publish")
	if f.publishErr == nil {
		f.release = relPublished
	}
	return f.publishErr
}
func (f *fakeWorld) Now() time.Time        { return f.now }
func (f *fakeWorld) Sleep(d time.Duration) { f.calls = append(f.calls, "sleep"); f.now = f.now.Add(d) }

// effects is the calls that change or run something, without the lookups.
func (f *fakeWorld) effects() []string {
	var out []string
	for _, c := range f.calls {
		switch c {
		case "pretag", "createtag", "assets", "publish":
			out = append(out, c)
		}
	}
	return out
}

func TestPublish_orderRecheckedRightBeforePublishing(t *testing.T) {
	t.Run("another version published during the assets gate: refused, nothing published", func(t *testing.T) {
		f := newFake()
		f.tagAt, f.release = "abc", relDraft
		f.orderErr = errors.New("v1.2.3 is not above the latest published release v1.3.0")
		err := run(f)
		if err == nil || !strings.Contains(err.Error(), "not above") {
			t.Fatalf("err = %v, want the ordering refusal", err)
		}
		var g gateError
		if errors.As(err, &g) {
			t.Errorf("a refusal (exit 3) was reported as a failure (exit 4): %v", err)
		}
		for _, c := range f.calls {
			if c == "publish" {
				t.Fatalf("published despite the ordering refusal: %v", f.calls)
			}
		}
	})
	t.Run("unchanged: publishes after the recheck", func(t *testing.T) {
		f := newFake()
		f.tagAt, f.release = "abc", relDraft
		if err := run(f); err != nil {
			t.Fatal(err)
		}
		var order, pub = -1, -1
		for i, c := range f.calls {
			if c == "order" && order < 0 {
				order = i
			}
			if c == "publish" {
				pub = i
			}
		}
		if order < 0 || pub < 0 || order > pub {
			t.Errorf("calls %v: want order before publish", f.calls)
		}
	})
}

func run(f *fakeWorld) error {
	return runPublish(Options{Version: "1.2.3", Wait: time.Minute}, f, "abc")
}

func TestPublish_freshRunInOrder(t *testing.T) {
	f := newFake()
	f.draftAfter = 2
	if err := run(f); err != nil {
		t.Fatal(err)
	}
	if got, want := f.effects(), []string{"pretag", "createtag", "assets", "publish", "assets"}; !reflect.DeepEqual(got, want) {
		t.Errorf("effects %v, want %v", got, want)
	}
	if f.tagAt != "abc" || f.release != relPublished {
		t.Errorf("end state: tag %q release %q", f.tagAt, f.release)
	}
}

func TestPublish_resumeFromTagWithoutRelease(t *testing.T) {
	f := newFake()
	f.tagAt, f.draftAfter = "abc", 1
	if err := run(f); err != nil {
		t.Fatal(err)
	}
	if got, want := f.effects(), []string{"pretag", "assets", "publish", "assets"}; !reflect.DeepEqual(got, want) {
		t.Errorf("effects %v, want %v (gate before publishing, no new tag)", got, want)
	}
}

func TestPublish_resumeFromDraft(t *testing.T) {
	f := newFake()
	f.tagAt, f.release = "abc", relDraft
	if err := run(f); err != nil {
		t.Fatal(err)
	}
	if got, want := f.effects(), []string{"pretag", "assets", "publish", "assets"}; !reflect.DeepEqual(got, want) {
		t.Errorf("effects %v, want %v", got, want)
	}
}

func TestPublish_resumeFromPublishedOnlyVerifies(t *testing.T) {
	f := newFake()
	f.tagAt, f.release = "abc", relPublished
	if err := run(f); err != nil {
		t.Fatal(err)
	}
	if got, want := f.effects(), []string{"assets"}; !reflect.DeepEqual(got, want) {
		t.Errorf("effects %v, want only the read-only verify", got)
	}
}

func TestPublish_publishedButBadAssetsIsRefusedNotRepaired(t *testing.T) {
	f := newFake()
	f.tagAt, f.release, f.assetsErr = "abc", relPublished, errors.New("refused: asset differs")
	err := run(f)
	if err == nil || exitCode(err) != 3 {
		t.Fatalf("got %v (exit %d), want a refusal", err, exitCode(err))
	}
	if got := f.effects(); !reflect.DeepEqual(got, []string{"assets"}) {
		t.Errorf("effects %v", got)
	}
}

func TestPublish_tagAtWrongCommitIsRefusedWithNoEffects(t *testing.T) {
	for _, st := range []string{relNone, relDraft, relPublished} {
		f := newFake()
		f.tagAt, f.release = "other", st
		err := run(f)
		if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "not the candidate") {
			t.Errorf("%s: %v (exit %d)", st, err, exitCode(err))
		}
		if len(f.effects()) != 0 {
			t.Errorf("%s: effects %v", st, f.effects())
		}
	}
}

func TestPublish_gateRefusalStopsBeforeTagging(t *testing.T) {
	f := newFake()
	f.preErr = errors.New("refused abc: earlier release is a draft")
	err := run(f)
	if err == nil || exitCode(err) != 3 {
		t.Fatalf("got %v exit %d", err, exitCode(err))
	}
	if got := f.effects(); !reflect.DeepEqual(got, []string{"pretag"}) || f.tagAt != "" {
		t.Errorf("effects %v tag %q", got, f.tagAt)
	}
}

func TestPublish_gateCouldNotDecideIsExit4(t *testing.T) {
	f := newFake()
	f.preErr = gateError{errors.New("could not decide")}
	if err := run(f); exitCode(err) != 4 {
		t.Fatalf("exit %d: %v", exitCode(err), err)
	}
}

func TestPublish_releaseWithoutTagIsRefused(t *testing.T) {
	f := newFake()
	f.release = relDraft
	err := run(f)
	if exitCode(err) != 3 || len(f.effects()) != 0 {
		t.Errorf("exit %d effects %v: %v", exitCode(err), f.effects(), err)
	}
}

func TestPublish_tagPushFailureIsExit4AndResumable(t *testing.T) {
	f := newFake()
	f.tagErr = gateError{errors.New("push failed")} // realWorld.CreateTag marks its git failures so
	if err := run(f); exitCode(err) != 4 {
		t.Fatalf("exit %d: %v", exitCode(err), err)
	}
	if f.tagAt != "" {
		t.Error("tag recorded despite the failure")
	}
}

func TestPublish_timeoutIsExit4(t *testing.T) {
	f := newFake()
	f.tagAt = "abc"
	err := run(f)
	if exitCode(err) != 4 || !strings.Contains(err.Error(), "no draft") {
		t.Fatalf("exit %d: %v", exitCode(err), err)
	}
	if got := f.effects(); len(got) != 0 {
		t.Errorf("effects %v", got)
	}
	if f.now.Sub(time.Unix(1000, 0)) < time.Minute {
		t.Errorf("gave up after %s, before --wait", f.now.Sub(time.Unix(1000, 0)))
	}
}

func TestPublish_failedWorkflowStopsEarly(t *testing.T) {
	f := newFake()
	f.tagAt, f.wfFailed = "abc", true
	err := run(f)
	if exitCode(err) != 4 || !strings.Contains(err.Error(), "release.yml failed") {
		t.Fatalf("exit %d: %v", exitCode(err), err)
	}
	if f.now != time.Unix(1000, 0) {
		t.Error("slept although the workflow had already failed")
	}
}

func TestPublish_assetsRefusalLeavesDraft(t *testing.T) {
	f := newFake()
	f.tagAt, f.release, f.assetsErr = "abc", relDraft, errors.New("refused: assets differ")
	err := run(f)
	if exitCode(err) != 3 || f.release != relDraft {
		t.Fatalf("exit %d release %s: %v", exitCode(err), f.release, err)
	}
	for _, e := range f.effects() {
		if e == "publish" {
			t.Error("published despite the refusal")
		}
	}
}

// A refusal by Publish itself (no verifying SHA256SUMS.sig) is exit 3, not a failure to retry.
func TestPublish_aRefusalToPublishIsExit3(t *testing.T) {
	f := newFake()
	f.tagAt, f.release, f.publishErr = "abc", relDraft, errors.New("refused: SHA256SUMS.sig is not attached")
	if err := run(f); exitCode(err) != 3 {
		t.Fatalf("exit %d: %v", exitCode(err), err)
	}
}

func TestPublish_publishFailureIsExit4(t *testing.T) {
	f := newFake()
	f.tagAt, f.release, f.publishErr = "abc", relDraft, op(errors.New("gh down")) // realWorld.Publish marks a gh failure as the gate's
	if err := run(f); exitCode(err) != 4 {
		t.Fatalf("exit %d: %v", exitCode(err), err)
	}
}

func TestOrderAbove(t *testing.T) {
	rel := func(kv ...any) []releaseRow {
		var r []releaseRow
		for i := 0; i < len(kv); i += 2 {
			r = append(r, releaseRow{TagName: kv[i].(string), IsDraft: kv[i+1].(bool)})
		}
		return r
	}
	cases := []struct {
		name     string
		version  string
		releases []releaseRow
		bad      bool
	}{
		{"no releases", "0.0.1", nil, false},
		{"above", "0.2.11", rel("v0.2.10", false), false},
		{"equal", "0.2.10", rel("v0.2.10", false), true},
		{"below", "0.2.9", rel("v0.2.10", false), true},
		{"numeric not lexical", "0.2.9", rel("v0.2.10", false, "v0.2.2", false), true},
		{"a draft above does not count", "0.2.11", rel("v0.2.10", false, "v0.3.0", true), false},
		{"non-semver ignored", "0.0.2", rel("latest", false, "v0.0.1-rc1", false), false},
		{"the highest published rules", "0.2.5", rel("v0.2.1", false, "v0.2.10", false), true},
	}
	for _, c := range cases {
		err := orderAbove(c.version, c.releases)
		if (err != nil) != c.bad {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	if err := orderAbove("x", nil); err == nil {
		t.Error("a bad version passed")
	}
}

func TestRunFailed(t *testing.T) {
	cases := map[string]bool{
		`[]`: false,
		`[{"status":"in_progress","conclusion":""}]`:                                                    false,
		`[{"status":"completed","conclusion":"success"}]`:                                               false,
		`[{"status":"completed","conclusion":"failure"}]`:                                               true,
		`[{"status":"completed","conclusion":"success"},{"status":"completed","conclusion":"failure"}]`: false, // newest first
		`[{"status":"completed","conclusion":"cancelled"}]`:                                             true,
	}
	for in, want := range cases {
		if got, err := runFailed([]byte(in)); err != nil || got != want {
			t.Errorf("%s: %v %v", in, got, err)
		}
	}
}

func TestParseArgs_publish(t *testing.T) {
	o, assets, err := parseArgs([]string{"publish", "1.2.3", "abc"})
	if err != nil || assets || !o.Publish || o.Version != "1.2.3" || o.X != "abc" || o.Wait != defaultWait {
		t.Fatalf("%+v %v %v", o, assets, err)
	}
	for _, args := range [][]string{
		{"publish", "--wait", "5m", "1.2.3", "abc"},
		{"publish", "1.2.3", "--wait=5m", "abc"},
	} {
		o, _, err := parseArgs(args)
		if err != nil || o.Wait != 5*time.Minute || o.Version != "1.2.3" || o.X != "abc" {
			t.Errorf("%v: %+v %v", args, o, err)
		}
	}
	for _, args := range [][]string{
		{"publish"}, {"publish", "1.2.3"}, {"publish", "1.2.3", "abc", "d"},
		{"publish", "--assets", "1.2.3", "abc"}, {"publish", "--consumer", "co", "1.2.3", "abc"},
		{"publish", "--wait"}, {"publish", "--wait", "soon", "1.2.3", "abc"}, {"publish", "--wait", "-1m", "1.2.3", "abc"},
		{"publish", "--bogus", "1.2.3", "abc"},
		{"1.2.3", "abc", "publish"}, // publish is only the first argument
	} {
		if _, _, err := parseArgs(args); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}

func TestResolveSigning(t *testing.T) {
	has := map[string]bool{}
	exists := func(p string) bool { return has[p] }
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	if _, _, err := resolveSigning(env(nil), "/h", exists, "/o"); err == nil || !strings.Contains(err.Error(), "/h/.ssh/id_ed25519.pub") {
		t.Fatalf("missing default key: %v", err)
	}
	has["/h/.ssh/id_ed25519.pub"] = true
	k, s, err := resolveSigning(env(nil), "/h", exists, "/o")
	if err != nil || k != "/h/.ssh/id_ed25519.pub" || s != "/o" {
		t.Fatalf("defaults: %q %q %v", k, s, err)
	}
	has["/k.pub"] = true
	k, s, err = resolveSigning(env(map[string]string{"RELEASE_SIGNING_KEY": "/k.pub", "RELEASE_SIGNERS": "/s"}), "/h", exists, "/o")
	if err != nil || k != "/k.pub" || s != "/s" {
		t.Fatalf("env: %q %q %v", k, s, err)
	}
	if _, _, err = resolveSigning(env(map[string]string{"RELEASE_SIGNING_KEY": "/nope.pub"}), "/h", exists, "/o"); err == nil {
		t.Fatal("missing env key accepted")
	}
}

func createTagWorld(t *testing.T, localTag bool, verifyErr error) (realWorld, *[]string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	key := filepath.Join(home, "k.pub")
	if err := os.WriteFile(key, []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RELEASE_SIGNING_KEY", key)
	t.Setenv("RELEASE_SIGNERS", "/signers")
	var calls []string
	r := realWorld{o: Options{Version: "1.2.3", Remote: "origin"}}
	r.gitFn = func(dir string, args ...string) (string, error) {
		j := strings.Join(args, " ")
		calls = append(calls, j)
		switch {
		case args[0] == "rev-parse":
			if localTag {
				return "abc", nil
			}
			return "", errors.New("none")
		case strings.Contains(j, "verify-tag"):
			return "", verifyErr
		case args[0] == "push":
			return "", errors.New("stop here")
		}
		return "", nil
	}
	return r, &calls
}

func TestCreateTag_explicitSigningFlags(t *testing.T) {
	r, calls := createTagWorld(t, false, nil)
	err := r.CreateTag("v1.2.3", "abc")
	if err == nil || !strings.Contains(err.Error(), "stop here") {
		t.Fatalf("want to reach push: %v", err)
	}
	c := *calls
	if len(c) != 4 || !strings.HasPrefix(c[1], "-c gpg.format=ssh -c user.signingkey=") || !strings.HasSuffix(c[1], "k.pub tag -s v1.2.3 -m mash 1.2.3 abc") ||
		c[2] != "-c gpg.format=ssh -c gpg.ssh.allowedSignersFile=/signers verify-tag v1.2.3" || !strings.HasPrefix(c[3], "push ") {
		t.Fatalf("calls: %q", c)
	}
}

func TestCreateTag_verifyFailureDoesNotPush(t *testing.T) {
	r, calls := createTagWorld(t, false, errors.New("bad signature"))
	err := r.CreateTag("v1.2.3", "abc")
	var g gateError
	if err == nil || errors.As(err, &g) || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("want a refusal: %v", err)
	}
	c := strings.Join(*calls, "\n")
	if strings.Contains(c, "push") || !strings.Contains(c, "tag -d v1.2.3") {
		t.Fatalf("calls: %s", c)
	}
}

func TestCreateTag_reusedLocalTagFailingVerifyIsKept(t *testing.T) {
	r, calls := createTagWorld(t, true, errors.New("bad signature"))
	if err := r.CreateTag("v1.2.3", "abc"); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("want a refusal: %v", err)
	}
	c := strings.Join(*calls, "\n")
	if strings.Contains(c, "push") || strings.Contains(c, "tag -d") || strings.Contains(c, "tag -s") {
		t.Fatalf("calls: %s", c)
	}
}

func TestCreateTag_missingKeyBeforeAnyGit(t *testing.T) {
	r, calls := createTagWorld(t, false, nil)
	t.Setenv("RELEASE_SIGNING_KEY", "/nonexistent.pub")
	err := r.CreateTag("v1.2.3", "abc")
	var g gateError
	if !errors.As(err, &g) || len(*calls) != 0 {
		t.Fatalf("want exit-4 failure with no git calls: %v %q", err, *calls)
	}
}

func TestPublish_resumeFromDraftRunsPreTagFirst(t *testing.T) {
	f := newFake()
	f.tagAt, f.release = "abc", relDraft
	if err := run(f); err != nil {
		t.Fatal(err)
	}
	if got, want := f.effects(), []string{"pretag", "assets", "publish", "assets"}; !reflect.DeepEqual(got, want) {
		t.Errorf("effects %v, want %v", got, want)
	}
}

func TestPublish_resumeFromTagThenDraftRunsPreTagOnce(t *testing.T) {
	f := newFake()
	f.tagAt, f.draftAfter = "abc", 1
	if err := run(f); err != nil {
		t.Fatal(err)
	}
	if got, want := f.effects(), []string{"pretag", "assets", "publish", "assets"}; !reflect.DeepEqual(got, want) {
		t.Errorf("effects %v, want %v", got, want)
	}
}

func TestPublish_existingDraftFailingPreTagIsRefusedAndNotPublished(t *testing.T) {
	f := newFake()
	f.tagAt, f.release = "abc", relDraft
	f.preErr = errors.New("refused: CI failed")
	if err := run(f); err == nil {
		t.Fatal("want a refusal")
	}
	if got, want := f.effects(), []string{"pretag"}; !reflect.DeepEqual(got, want) {
		t.Errorf("effects %v, want %v (no assets gate, nothing published)", got, want)
	}
	if f.release != relDraft {
		t.Errorf("release %q, want it left a draft", f.release)
	}
}

func TestPublish_publishedDoesNotRunPreTag(t *testing.T) {
	f := newFake()
	f.tagAt, f.release = "abc", relPublished
	f.preErr = errors.New("would refuse")
	if err := run(f); err != nil {
		t.Fatal(err)
	}
	if got, want := f.effects(), []string{"assets"}; !reflect.DeepEqual(got, want) {
		t.Errorf("effects %v, want %v", got, want)
	}
}

// The gate must not count this version's own draft or tag against it (resume path).
func TestOrderAbove_ownDraftDoesNotCount(t *testing.T) {
	rel := []releaseRow{{TagName: "v1.2.2"}, {TagName: "v1.2.3", IsDraft: true}}
	if err := orderAbove("1.2.3", rel); err != nil {
		t.Errorf("own draft counted against itself: %v", err)
	}
}
