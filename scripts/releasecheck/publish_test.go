package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fakeWorld records every effect, and serves a scripted state.
type fakeWorld struct {
	tagAt           string // "" none
	release         string // relNone, relDraft, relPublished
	draftAfter      int    // Release polls that say none before the draft appears (0: as scripted)
	wfFailed        bool
	preErr          error
	assetsErr       error
	publishErr      error
	orderErr        error // what Order says once the assets gate has run (another version published meanwhile)
	assetsRan       bool
	tagErr          error
	now             time.Time
	calls           []string
	polls           int
	cutover         string // the cutover version Keyless reports ("": none)
	latestErr       error  // what VerifyNotLatest says
	notMarkedLatest error  // what VerifyLatest says
	keyless         bool   // the tag is at or above the cutover: its tag is pushed by the minting service
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
func (f *fakeWorld) VerifyNotLatest(tag string) error {
	f.calls = append(f.calls, "notlatest")
	return f.latestErr
}
func (f *fakeWorld) VerifyLatest(tag string) error {
	f.calls = append(f.calls, "latest")
	return f.notMarkedLatest
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

func TestPublish_rerunVerifiesNotLatest(t *testing.T) {
	has := func(f *fakeWorld) bool {
		for _, c := range f.calls {
			if c == "notlatest" {
				return true
			}
		}
		return false
	}
	o := Options{Version: "1.2.3", Wait: time.Minute, NotLatest: true}
	t.Run("published and marked latest: exit 4, not a refusal", func(t *testing.T) {
		f := newFake()
		f.tagAt, f.release = "abc", relPublished
		f.latestErr = errors.New("v1.2.3 was published with --not-latest but is marked latest")
		err := runPublish(o, f, "abc")
		if err == nil || !strings.Contains(err.Error(), "marked latest") {
			t.Fatalf("err = %v, want the refusal", err)
		}
	})
	t.Run("published and not latest: ok", func(t *testing.T) {
		f := newFake()
		f.tagAt, f.release = "abc", relPublished
		if err := runPublish(o, f, "abc"); err != nil || !has(f) {
			t.Fatalf("err = %v, calls %v", err, f.calls)
		}
	})
	t.Run("without the flag: the latest check, not the not-latest one", func(t *testing.T) {
		f := newFake()
		f.tagAt, f.release = "abc", relPublished
		if err := run(f); err != nil || has(f) {
			t.Fatalf("err = %v, calls %v", err, f.calls)
		}
		found := false
		for _, c := range f.calls {
			found = found || c == "latest"
		}
		if !found {
			t.Fatalf("no latest check: %v", f.calls)
		}
	})
	t.Run("without the flag, published but not marked Latest: exit 4, not a refusal", func(t *testing.T) {
		f := newFake()
		f.tagAt, f.release = "abc", relPublished
		f.notMarkedLatest = errors.New("v1.2.3 was published but is not marked Latest")
		err := run(f)
		if err == nil || !strings.Contains(err.Error(), "not marked Latest") {
			t.Fatalf("err = %v, want the refusal", err)
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
		{"publish", "--bogus", "1.2.3", "abc"}, {"publish", "--not-latest=false", "1.2.3", "abc"},
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

func TestParseArgs_notLatest(t *testing.T) {
	o, _, err := parseArgs([]string{"publish", "--not-latest", "1.2.3", "abc"})
	if err != nil || !o.NotLatest {
		t.Fatalf("%+v %v", o, err)
	}
	if o, _, err = parseArgs([]string{"publish", "1.2.3", "abc"}); err != nil || o.NotLatest {
		t.Fatalf("%+v %v", o, err)
	}
}

// ghLog writes a gh that logs its arguments and answers `release list` with list (the recorded output shape).
func ghLog(t *testing.T, list string) (Options, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\n" +
		"case \"$2\" in list) echo '" + list + "' ;; esac\n"
	gh := filepath.Join(dir, "gh")
	if err := os.WriteFile(gh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return Options{GH: gh, GHRepo: "bogworks/mash"}, log
}

const (
	listNotLatest = `[{"isLatest":true,"tagName":"v0.5.0"},{"isLatest":false,"tagName":"v0.4.0"}]`
	listLatest    = `[{"isLatest":true,"tagName":"v0.4.0"},{"isLatest":false,"tagName":"v0.3.0"}]`
)

func noSleep(time.Duration) {}

// ghSeq is a fake gh whose n-th release list answers lists[n] (the last one repeats).
func ghSeq(t *testing.T, lists ...string) (Options, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\n" + "case \"$2\" in list)\n" +
		"n=$(grep -c '^release list' " + log + ")\ncase $n in\n"
	for i, l := range lists {
		pat := fmt.Sprint(i + 1)
		if i == len(lists)-1 {
			pat = "*"
		}
		script += pat + ") echo '" + l + "' ;;\n"
	}
	script += "esac ;; esac\n"
	gh := filepath.Join(dir, "gh")
	if err := os.WriteFile(gh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return Options{GH: gh, GHRepo: "bogworks/mash"}, log
}

func TestVerifyLatest_retriesUntilLatest(t *testing.T) {
	o, log := ghSeq(t, listNotLatest, listLatest)
	var slept []time.Duration
	if err := verifyLatest(o, "v0.4.0", func(d time.Duration) { slept = append(slept, d) }); err != nil {
		t.Fatal(err)
	}
	if len(slept) != 1 || slept[0] != 2*time.Second {
		t.Errorf("slept %v", slept)
	}
	b, _ := os.ReadFile(log)
	if n := strings.Count(string(b), "release list"); n != 2 {
		t.Errorf("%d reads", n)
	}
}

func TestVerifyLatest_givesUpAfterFiveReads(t *testing.T) {
	o, log := ghSeq(t, listNotLatest)
	sleeps := 0
	err := verifyLatest(o, "v0.4.0", func(time.Duration) { sleeps++ })
	if err == nil || !strings.Contains(err.Error(), "not marked Latest") {
		t.Fatalf("got %v", err)
	}
	b, _ := os.ReadFile(log)
	if n := strings.Count(string(b), "release list"); n != 5 || sleeps != 4 {
		t.Errorf("%d reads, %d sleeps", n, sleeps)
	}
}

func TestVerifyNotLatest_retriesUntilNotLatest(t *testing.T) {
	o, _ := ghSeq(t, listLatest, listNotLatest)
	if err := verifyNotLatest(o, "v0.4.0", noSleep); err != nil {
		t.Fatal(err)
	}
}

// An older release that is already published is not Latest: verifying it without --not-latest fails.
func TestVerifyLatest_olderPublishedTagFails(t *testing.T) {
	o, _ := ghLog(t, listLatest)
	err := verifyLatest(o, "v0.3.0", noSleep)
	if err == nil || !strings.Contains(err.Error(), "not marked Latest") {
		t.Fatalf("got %v", err)
	}
}

func TestPublish_failedCheckAfterPublishIsExit4(t *testing.T) {
	for _, notLatest := range []bool{false, true} {
		f := newFake()
		f.tagAt, f.release = "abc", relPublished
		fix := "--latest"
		if notLatest {
			f.latestErr = errors.New("marked latest")
			fix = "--latest=false"
		} else {
			f.notMarkedLatest = errors.New("not marked Latest")
		}
		err := runPublish(Options{Version: "1.2.3", Wait: time.Minute, NotLatest: notLatest}, f, "abc")
		want := "v1.2.3 is published; rerun to re-verify, or gh release edit v1.2.3 " + fix
		if !notLatest {
			want = "v1.2.3 is published; rerun to re-verify, or if this is an older release, rerun with --not-latest"
		}
		if err == nil || !strings.Contains(err.Error(), want) || exitCode(err) != 4 {
			t.Errorf("notLatest=%v: got %v, exit %d", notLatest, err, exitCode(err))
		}
	}
}

func TestPublish_latestHintDependsOnWhoPublished(t *testing.T) {
	t.Run("published by this run: suggests --latest", func(t *testing.T) {
		f := newFake()
		f.tagAt, f.release = "abc", relDraft
		f.notMarkedLatest = errors.New("not marked Latest")
		err := run(f)
		if err == nil || exitCode(err) != 4 || !strings.Contains(err.Error(), "gh release edit v1.2.3 --latest") || strings.Contains(err.Error(), "--not-latest") {
			t.Fatalf("got %v, exit %d", err, exitCode(err))
		}
	})
	t.Run("already published: no --latest, suggests --not-latest", func(t *testing.T) {
		f := newFake()
		f.tagAt, f.release = "abc", relPublished
		f.notMarkedLatest = errors.New("not marked Latest")
		err := run(f)
		if err == nil || exitCode(err) != 4 || strings.Contains(err.Error(), "--latest") || !strings.Contains(err.Error(), "older release, rerun with --not-latest") {
			t.Fatalf("got %v, exit %d", err, exitCode(err))
		}
	})
}

func TestPublishRelease_notLatest(t *testing.T) {
	o, log := ghLog(t, listNotLatest)
	o.NotLatest = true
	if err := publishRelease(o, "v0.4.0", noSleep); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	want := "release edit v0.4.0 --repo bogworks/mash --draft=false --latest=false\nrelease list --repo bogworks/mash --json tagName,isLatest --limit 1000 --exclude-drafts\n"
	if string(b) != want {
		t.Errorf("gh calls:\n%s", b)
	}
}

func TestPublishRelease_notLatestButLatest(t *testing.T) {
	o, _ := ghLog(t, listLatest)
	o.NotLatest = true
	err := publishRelease(o, "v0.4.0", noSleep)
	if err == nil || !strings.Contains(err.Error(), "marked latest") {
		t.Fatalf("got %v", err)
	}
}

func TestPublishRelease_notLatestTagAbsent(t *testing.T) {
	o, _ := ghLog(t, listNotLatest)
	o.NotLatest = true
	err := publishRelease(o, "v9.9.9", noSleep)
	if err == nil || !strings.Contains(err.Error(), "not found in the release list") {
		t.Fatalf("got %v", err)
	}
}

func TestPublishRelease_notLatestMalformedList(t *testing.T) {
	o, _ := ghLog(t, `not json`)
	o.NotLatest = true
	err := publishRelease(o, "v0.4.0", noSleep)
	var oe opError
	if err == nil || !errors.As(err, &oe) {
		t.Fatalf("got %v (%T), want an opError", err, err)
	}
}

func TestPublishRelease_latestPassesFlagAndVerifies(t *testing.T) {
	o, log := ghLog(t, listLatest)
	if err := publishRelease(o, "v0.4.0", noSleep); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	want := "release edit v0.4.0 --repo bogworks/mash --draft=false --latest\nrelease list --repo bogworks/mash --json tagName,isLatest --limit 1000 --exclude-drafts\n"
	if string(b) != want {
		t.Errorf("gh calls:\n%s", b)
	}
}

func TestPublishRelease_latestButAnotherIsLatest(t *testing.T) {
	o, _ := ghLog(t, listNotLatest)
	err := stepErr(publishRelease(o, "v0.4.0", noSleep)) // as runPublish wraps it
	if err == nil || !strings.Contains(err.Error(), "not marked Latest") || !strings.Contains(err.Error(), "is published; rerun") || exitCode(err) != 4 {
		t.Fatalf("got %v, exit %d", err, exitCode(err))
	}
}

func TestVerifyLatest_readOnly(t *testing.T) {
	o, log := ghLog(t, listLatest)
	if err := verifyLatest(o, "v0.4.0", noSleep); err != nil {
		t.Fatal(err)
	}
	if err := verifyLatest(o, "v0.3.0", noSleep); err == nil {
		t.Fatal("v0.3.0 is not latest: want an error")
	}
	b, _ := os.ReadFile(log)
	if strings.Contains(string(b), "edit") {
		t.Errorf("verification wrote: %s", b)
	}
}
