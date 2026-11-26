package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/release"
)

// writeReceipts writes each receipt as its own JSON file in a fresh directory.
func writeReceipts(t *testing.T, rs []Receipt) string {
	t.Helper()
	dir := t.TempDir()
	for i, r := range rs {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("r%02d.json", i)), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func injectExpected(exp Expected) func(context.Context, Options, string) (Expected, error) {
	return func(context.Context, Options, string) (Expected, error) { return exp, nil }
}

// runStep runs the receipts step alone, the way check() does, and returns the report text and the result.
func runStep(o Options) (string, error) {
	var out bytes.Buffer
	r := &report{out: &out}
	receiptsStep(context.Background(), o, "x", r)
	return out.String(), r.result("x")
}

func gateOptions(version, dir string, exp Expected) Options {
	return Options{Version: version, Receipts: dir, expect: injectExpected(exp)}
}

func TestReceiptsGate_belowTheSwitchOnWithoutReceiptsIsUnchanged(t *testing.T) {
	for _, v := range []string{"0.5.9", "0.5.10"} {
		out, err := runStep(Options{Version: v, expect: func(context.Context, Options, string) (Expected, error) {
			t.Error("expect ran with no --receipts below the switch-on")
			return Expected{}, nil
		}})
		if err != nil || out != "" {
			t.Errorf("%s: err %v, output %q; want neither", v, err, out)
		}
	}
}

func TestReceiptsGate_0510WithReceipts(t *testing.T) {
	exp := receiptExpected()
	out, err := runStep(gateOptions("0.5.10", writeReceipts(t, validReceipts(exp, lineAT011)), exp))
	if err != nil || !strings.Contains(out, "ok    "+receiptsStepName) {
		t.Fatalf("complete AT-011 set: err %v, output %q", err, out)
	}

	// AT-012 is not asked for below 0.6.0: a set without it passes, and one with it passes too.
	if _, err := runStep(gateOptions("0.5.10", writeReceipts(t, validReceipts(exp, lineSwitchOn)), exp)); err != nil {
		t.Fatalf("0.5.10 with extra AT-012 receipts: %v", err)
	}

	rs := validReceipts(exp, lineAT011)
	rs[0].InstallSHA256 = rsha("another install.sh")
	out, err = runStep(gateOptions("0.5.10", writeReceipts(t, rs), exp))
	if err == nil || exitCode(err) != 3 || !strings.Contains(out, "FAIL  "+receiptsStepName) || !strings.Contains(err.Error(), "AT-011") {
		t.Fatalf("mismatched receipt: exit %d, err %v, output %q; want a refusal naming AT-011", exitCode(err), err, out)
	}
}

func TestReceiptsGate_0510EveryProblemIsReported(t *testing.T) {
	exp := receiptExpected()
	rs := validReceipts(exp, lineAT011)[:2] // two platforms of four
	_, err := runStep(gateOptions("0.5.10", writeReceipts(t, rs), exp))
	if err == nil || exitCode(err) != 3 {
		t.Fatalf("exit %d, err %v; want a refusal", exitCode(err), err)
	}
	if n := strings.Count(err.Error(), "AT-011 "); n != 4 { // mash and mashctl on each of the two missing platforms
		t.Fatalf("%d AT-011 problems, want 4:\n%v", n, err)
	}
}

func TestReceiptsGate_0600WithoutReceiptsIsRefused(t *testing.T) {
	for _, v := range []string{"0.6.0", "0.6.1", "1.0.0", "0.10.0"} {
		out, err := runStep(Options{Version: v, expect: func(context.Context, Options, string) (Expected, error) {
			t.Error("expect ran with no receipts given")
			return Expected{}, nil
		}})
		if err == nil || exitCode(err) != 3 {
			t.Fatalf("%s: exit %d, err %v; want refusal", v, exitCode(err), err)
		}
		const want = "receipts: none given: the proofs of AT-011 and AT-012 are required from 0.6.0"
		lines := strings.Split(err.Error(), "\n")
		if len(lines) != 2 || strings.TrimSpace(lines[1]) != want {
			t.Fatalf("%s: problems %q, want the one line %q", v, lines[1:], want)
		}
		if !strings.Contains(out, "FAIL  ") {
			t.Fatalf("%s: no FAIL line:\n%s", v, out)
		}
	}
}

func TestReceiptsGate_0600WithTheCompleteSetPasses(t *testing.T) {
	exp := receiptExpected()
	out, err := runStep(gateOptions("0.6.0", writeReceipts(t, validReceipts(exp, lineSwitchOn)), exp))
	if err != nil || !strings.Contains(out, "ok    "+receiptsStepName) {
		t.Fatalf("complete AT-011 and AT-012 set: err %v, output %q", err, out)
	}
}

func TestReceiptsGate_0600RefusesWhatIsShort(t *testing.T) {
	exp := receiptExpected()
	full := validReceipts(exp, lineSwitchOn)

	// AT-012 short one platform.
	var short []Receipt
	for _, r := range full {
		if r.Kind == kindAT012 && r.GOOS == "darwin" && r.GOARCH == "arm64" {
			continue
		}
		short = append(short, r)
	}
	_, err := runStep(gateOptions("0.6.0", writeReceipts(t, short), exp))
	if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "AT-012 darwin/arm64") {
		t.Fatalf("AT-012 short: exit %d, err %v; want a refusal naming AT-012 darwin/arm64", exitCode(err), err)
	}

	// AT-011 only: the line needs AT-012 as well.
	_, err = runStep(gateOptions("0.6.0", writeReceipts(t, validReceipts(exp, lineAT011)), exp))
	if err == nil || exitCode(err) != 3 || strings.Count(err.Error(), "AT-012 ") != 4 {
		t.Fatalf("no AT-012: exit %d, err %v; want a refusal naming all four platforms", exitCode(err), err)
	}

	// AT-012 that ran other binaries than the gate's.
	bad := validReceipts(exp, lineSwitchOn)
	for i := range bad {
		if bad[i].Kind == kindAT012 {
			bad[i].NewerMashSHA256 = rsha("other build")
			break
		}
	}
	_, err = runStep(gateOptions("0.6.0", writeReceipts(t, bad), exp))
	if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "newer mash sha256 differs") {
		t.Fatalf("AT-012 other binary: exit %d, err %v", exitCode(err), err)
	}
}

func TestReceiptsGate_unreadableReceiptsFailClosed(t *testing.T) {
	exp := receiptExpected()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{dir, filepath.Join(dir, "missing")} {
		for _, v := range []string{"0.5.10", "0.6.0"} {
			_, err := runStep(gateOptions(v, d, exp))
			if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "receipts:") {
				t.Errorf("%s %s: exit %d, err %v; want a refusal", v, d, exitCode(err), err)
			}
		}
	}
	// An empty directory holds no receipt: refused, never a pass.
	if _, err := runStep(gateOptions("0.6.0", t.TempDir(), exp)); err == nil || exitCode(err) != 3 {
		t.Fatalf("empty directory: exit %d, err %v", exitCode(err), err)
	}
}

func TestReceiptsGate_expectedFailingIsFour(t *testing.T) {
	exp := receiptExpected()
	for _, v := range []string{"0.5.10", "0.6.0"} {
		o := gateOptions(v, writeReceipts(t, validReceipts(exp, lineSwitchOn)), exp)
		o.expect = func(context.Context, Options, string) (Expected, error) { return Expected{}, errors.New("build broke") }
		out, err := runStep(o)
		if err == nil || exitCode(err) != 4 || !strings.Contains(err.Error(), "build broke") || !strings.Contains(out, "FAIL  ") {
			t.Fatalf("%s: exit %d, err %v; want exit 4", v, exitCode(err), err)
		}
	}
}

func TestReceiptsGate_anExpectedThatIsEmptyFailsClosed(t *testing.T) {
	exp := receiptExpected()
	exp.UpgradePage = "" // no upgrade page at the candidate is a problem, not a pass
	_, err := runStep(gateOptions("0.6.0", writeReceipts(t, validReceipts(receiptExpected(), lineSwitchOn)), exp))
	if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "upgrade page blob id") {
		t.Fatalf("exit %d, err %v", exitCode(err), err)
	}
}

// The step is part of check(): a full gate run with --receipts, at the fixture's version.
func TestReceiptsGate_checkRunsTheStep(t *testing.T) {
	exp := receiptExpected()
	f := newFixture(t)
	f.o.Receipts = writeReceipts(t, validReceipts(exp, lineAT011))
	f.o.expect = injectExpected(exp)
	if err := f.check(); err != nil {
		t.Fatal(err)
	}
	if out := f.o.Out.(*bytes.Buffer).String(); !strings.Contains(out, "ok    "+receiptsStepName) {
		t.Fatalf("no receipts step in the output:\n%s", out)
	}

	f = newFixture(t)
	rs := validReceipts(exp, lineAT011)
	rs[1].UpgradePage = "blob-other"
	f.o.Receipts = writeReceipts(t, rs)
	f.o.expect = injectExpected(exp)
	err := f.check()
	if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "upgrade page revision differs") {
		t.Fatalf("mismatch: exit %d, err %v", exitCode(err), err)
	}

	f = newFixture(t)
	f.o.Receipts = writeReceipts(t, validReceipts(exp, lineAT011))
	f.o.expect = func(context.Context, Options, string) (Expected, error) { return Expected{}, errors.New("no builds") }
	if err := f.check(); err == nil || exitCode(err) != 4 {
		t.Fatalf("expect failure: exit %d, err %v; want 4", exitCode(err), err)
	}
}

func TestReceiptsGate_checkWithoutReceiptsAddsNothing(t *testing.T) {
	f := newFixture(t)
	f.o.expect = func(context.Context, Options, string) (Expected, error) {
		t.Error("expect ran with no --receipts below the switch-on")
		return Expected{}, nil
	}
	if err := f.check(); err != nil {
		t.Fatal(err)
	}
	if out := f.o.Out.(*bytes.Buffer).String(); strings.Contains(out, "REQ-019") || strings.Contains(out, "receipt") {
		t.Fatalf("the output names the receipts:\n%s", out)
	}
}

// At the switch-on version a gate run with no --receipts is refused with the one line, through check().
func TestReceiptsGate_checkAtTheSwitchOnRefusesWithoutReceipts(t *testing.T) {
	f := newFixture(t)
	f.o.Version = release.SwitchOnVersion
	err := f.check()
	if err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "\n  receipts: none given: the proofs of AT-011 and AT-012 are required from 0.6.0") {
		t.Fatalf("exit %d, err %v", exitCode(err), err)
	}
}

func TestReceiptsGate_flags(t *testing.T) {
	o, assets, err := parseArgs([]string{"--receipts", "/r", "abc", "0.5.10"})
	if err != nil || assets || o.Receipts != "/r" || o.X != "abc" || o.Version != "0.5.10" {
		t.Fatalf("plain form: %+v %v %v", o, assets, err)
	}
	o, _, err = parseArgs([]string{"abc", "0.5.10", "--receipts=/r"})
	if err != nil || o.Receipts != "/r" || o.X != "abc" {
		t.Fatalf("= form after the positionals: %+v %v", o, err)
	}
	o, _, err = parseArgs([]string{"--consumer", "/c", "--receipts", "/r", "abc", "0.6.0"})
	if err != nil || o.Receipts != "/r" || o.Consumer != "/c" || o.X != "abc" || o.Version != "0.6.0" {
		t.Fatalf("--consumer form: %+v %v", o, err)
	}
	if o, _, err = parseArgs([]string{"abc", "0.5.10"}); err != nil || o.Receipts != "" {
		t.Fatalf("no flag: %+v %v", o, err)
	}
	var out bytes.Buffer
	for _, args := range [][]string{
		{"abc", "0.5.10", "--receipts"},
		{"--receipts", "abc", "0.5.10"}, // takes abc as the directory: one positional short
		{"--receipts=", "abc", "0.5.10"},
		{"--receipts", "--consumer", "abc", "0.5.10"},
		{"--assets", "0.5.10", "abc", "--receipts", "/r"},
		{"--assets", "--receipts=/r", "0.5.10", "abc"},
		{"publish", "0.5.10", "abc", "--receipts"},
		{"publish", "--receipts=", "0.5.10", "abc"},
		{"publish", "--receipts", "--not-latest", "0.5.10", "abc"},
	} {
		if got := mainRun(args, &out); got != 2 {
			t.Errorf("%v: exit %d, want 2", args, got)
		}
	}
}

// realExpected computes everything from the gate's own builds, the previous release and the repository at X.
func TestReceiptsGate_realExpected(t *testing.T) {
	f := newFixture(t)
	gitT(t, f.repo, "tag", "v0.0.9", f.x)
	gitT(t, f.repo, "push", "-q", "origin", "v0.0.9")
	// M1: a higher tag that is a draft, and one recorded as skipped (a tag with no release), are passed over.
	withEarlierTags(t, f, "v0.0.10", "v0.0.11")
	f.setReleases(map[string]bool{"v0.0.9": false, "v0.0.10": true})
	var assets []map[string]string
	for _, b := range release.Binaries() {
		assets = append(assets, map[string]string{"name": b.AssetName(), "digest": "sha256:" + rsha("old "+b.AssetName())})
	}
	ab, _ := json.Marshal(map[string]any{"assets": assets})
	write(t, filepath.Dir(f.o.GH), "assets.json", string(ab))

	// The upgrade page is committed after the fixture's candidate, so the candidate is the new commit.
	write(t, f.repo, upgradePagePath, "# Upgrading\n")
	gitT(t, f.repo, "add", "-A")
	gitT(t, f.repo, "commit", "-q", "-m", "upgrade page")
	x := gitT(t, f.repo, "rev-parse", "HEAD")
	oldX := f.x
	f.o.X, f.x = x, x
	ours := f.ourBuilds()
	if err := f.o.defaults(); err != nil {
		t.Fatal(err)
	}
	exp, err := realExpected(context.Background(), f.o, x)
	if err != nil {
		t.Fatal(err)
	}
	if ps := exp.validate(lineSwitchOn); len(ps) > 0 {
		t.Fatalf("the computed Expected is not complete: %v", ps)
	}
	if exp.Commit != x || len(exp.Platforms) != 4 || len(exp.Executables) != 8 || len(exp.Prior) != 8 {
		t.Fatalf("commit %s platforms %d executables %d prior %d", exp.Commit, len(exp.Platforms), len(exp.Executables), len(exp.Prior))
	}
	if exp.PrevVersion != "0.0.9" || exp.OlderVersion != "0.0.9" || exp.NewerVersion != f.o.Version {
		t.Fatalf("versions: %q %q %q", exp.PrevVersion, exp.OlderVersion, exp.NewerVersion)
	}
	if want := gitT(t, f.repo, "rev-parse", "HEAD:"+upgradePagePath); exp.UpgradePage != want {
		t.Fatalf("upgrade page %q, want %q", exp.UpgradePage, want)
	}
	if want := sha256hex([]byte(scriptBody("install.sh"))); exp.InstallSHA != want {
		t.Fatalf("install.sh sha %q, want %q", exp.InstallSHA, want)
	}
	if exp.CorpusSHA != compatCorpusDigest() {
		t.Fatalf("corpus sha %q", exp.CorpusSHA)
	}
	for _, b := range release.Binaries() {
		got, ok := findExec(exp.Executables, b.Program, Platform{b.GOOS, b.GOARCH})
		if !ok || got.SHA256 != ours[b] || got.Build.Toolchain != release.Toolchain {
			t.Fatalf("%s: %+v", b.AssetName(), got)
		}
		if p, _ := findPrior(exp.Prior, b.Program, Platform{b.GOOS, b.GOARCH}); p.SHA256 != rsha("old "+b.AssetName()) {
			t.Fatalf("prior %s: %+v", b.AssetName(), p)
		}
	}
	// A candidate with no upgrade page at X leaves the blob id empty, which the check refuses.
	exp2, err := realExpected(context.Background(), f.o, oldX)
	if err != nil {
		t.Fatal(err)
	}
	if exp2.UpgradePage != "" {
		t.Fatalf("upgrade page %q at a commit without it", exp2.UpgradePage)
	}
}

// B1: publish takes --receipts (both spellings, anywhere after publish) and its pre-tag gate is held to it.
func TestReceiptsGate_publishTakesReceipts(t *testing.T) {
	for _, args := range [][]string{
		{"publish", "--receipts", "/r", "0.6.0", "abc"},
		{"publish", "0.6.0", "--receipts=/r", "abc", "--not-latest"},
	} {
		o, _, err := parseArgs(args)
		if err != nil || !o.Publish || o.Receipts != "/r" || o.Version != "0.6.0" || o.X != "abc" {
			t.Errorf("%v: %+v %v", args, o, err)
		}
	}
	if o, _, err := parseArgs([]string{"publish", "0.6.0", "abc"}); err != nil || o.Receipts != "" {
		t.Errorf("no flag: %+v %v", o, err)
	}
}

// The pre-tag gate publish runs is check with the options publish was given: at the switch-on release it refuses
// without the receipts, and with a --receipts directory it reads them instead of reporting none given.
func TestReceiptsGate_publishPreTagGetsTheReceipts(t *testing.T) {
	f := newFixture(t)
	f.o.Version = release.SwitchOnVersion
	err := realWorld{ctx: context.Background(), o: f.o}.PreTag()
	if err == nil || !strings.Contains(err.Error(), receiptsNoneGiven) {
		t.Fatalf("without --receipts: %v", err)
	}
	f.o.Receipts = filepath.Join(t.TempDir(), "absent")
	err = realWorld{ctx: context.Background(), o: f.o}.PreTag()
	if err == nil || strings.Contains(err.Error(), receiptsNoneGiven) || !strings.Contains(err.Error(), "receipts") {
		t.Fatalf("with --receipts: %v", err)
	}
}
