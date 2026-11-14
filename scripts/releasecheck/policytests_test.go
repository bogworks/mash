package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// stream builds a fake `go test -json` stream: each name gets the actions given.
func stream(events ...[2]string) []byte {
	var b strings.Builder
	for _, e := range events {
		fmt.Fprintf(&b, `{"Action":%q,"Package":"p","Test":%q}`+"\n", e[0], e[1])
	}
	return []byte(b.String())
}

func passing(test string) []byte { return stream([2]string{"run", test}, [2]string{"pass", test}) }

const fakeList = `# comment
AT-004  ./internal/a  TestOne  required
AT-001  ./internal/a  TestLive/AT-001_shape  required
AT-010  -  -  pending  no harness on main
`

func fakeEntries(t *testing.T) []policyEntry {
	t.Helper()
	es, err := parsePolicyList(strings.NewReader(fakeList))
	if err != nil {
		t.Fatal(err)
	}
	return es
}

func judge(t *testing.T, streams map[int][]byte, tests func(string) []string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	if tests == nil {
		tests = func(string) []string { return nil }
	}
	err := judgePolicy(&out, fakeEntries(t), streams, nil, tests)
	return out.String(), err
}

func TestPolicyAllPassIsOkAndPendingIsANotice(t *testing.T) {
	out, err := judge(t, map[int][]byte{0: passing("TestOne"), 1: passing("TestLive/AT-001_shape")}, nil)
	if err != nil {
		t.Fatalf("all pass: %v\n%s", err, out)
	}
	for _, want := range []string{"policy pass    AT-004", "policy pass    AT-001", "policy pending AT-010  pending: no harness on main"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary lacks %q:\n%s", want, out)
		}
	}
}

func TestPolicyOneFailureRefuses(t *testing.T) {
	bad := stream([2]string{"run", "TestOne"}, [2]string{"fail", "TestOne"})
	out, err := judge(t, map[int][]byte{0: bad, 1: passing("TestLive/AT-001_shape")}, nil)
	if err == nil || !strings.Contains(err.Error(), "AT-004") || !strings.Contains(out, "policy FAIL    AT-004") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if exitCode(err) != 3 {
		t.Fatalf("exit code %d, want 3", exitCode(err))
	}
}

func TestPolicyMissingTestRefuses(t *testing.T) {
	// The stream has events for another test only: TestOne was renamed or deleted.
	_, err := judge(t, map[int][]byte{0: passing("TestOther"), 1: passing("TestLive/AT-001_shape")}, nil)
	if err == nil || !strings.Contains(err.Error(), "not run") {
		t.Fatalf("err = %v, want a not-run refusal", err)
	}
	// An empty stream (the package did not build) is not a pass either.
	if _, err := judge(t, map[int][]byte{0: nil, 1: passing("TestLive/AT-001_shape")}, nil); err == nil {
		t.Fatal("an empty stream passed")
	}
}

func TestPolicyOnlyStartedEventIsNotAPass(t *testing.T) {
	_, err := judge(t, map[int][]byte{0: stream([2]string{"run", "TestOne"}), 1: passing("TestLive/AT-001_shape")}, nil)
	if err == nil {
		t.Fatal("a test that never passed was accepted")
	}
}

func TestPolicySkippedRequiredTestRefuses(t *testing.T) {
	skip := stream([2]string{"run", "TestLive/AT-001_shape"}, [2]string{"skip", "TestLive/AT-001_shape"})
	_, err := judge(t, map[int][]byte{0: passing("TestOne"), 1: skip}, nil)
	if err == nil || !strings.Contains(err.Error(), "skipped") {
		t.Fatalf("err = %v, want a skipped refusal", err)
	}
}

func TestPolicyPackageFailureRefuses(t *testing.T) {
	s := append(passing("TestOne"), []byte(`{"Action":"fail","Package":"p"}`+"\n")...)
	if _, err := judge(t, map[int][]byte{0: s, 1: passing("TestLive/AT-001_shape")}, nil); err == nil {
		t.Fatal("a failed package passed")
	}
}

func TestPolicyPendingEntryWhoseTestExistsRefuses(t *testing.T) {
	out, err := judge(t, map[int][]byte{0: passing("TestOne"), 1: passing("TestLive/AT-001_shape")},
		func(at string) []string {
			if at == "AT-010" {
				return []string{"internal/x/x_test.go:TestAT010Applied"}
			}
			return nil
		})
	if err == nil || !strings.Contains(err.Error(), "promote to required") || !strings.Contains(out, "exists: promote to required") {
		t.Fatalf("err = %v\n%s", err, out)
	}
}

func TestPolicyListParse(t *testing.T) {
	for _, bad := range []string{"", "AT-004 ./a TestOne\n", "AT-4 ./a TestOne required\n", "AT-004 ./a TestOne maybe\n", "AT-004 - - required\n"} {
		if _, err := parsePolicyList(strings.NewReader(bad)); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestRunPattern(t *testing.T) {
	if got, want := runPattern("TestLiveMatchesCheck/deny_key_(AT-003)"), `^TestLiveMatchesCheck$/^deny_key_\(AT-003\)$`; got != want {
		t.Fatalf("%s, want %s", got, want)
	}
}

func TestTestsForATFindsOnlyThatAT(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a/a_test.go", "package a\nfunc TestAT010_Applied(t *testing.T) {}\nfunc TestAT0100(t *testing.T) {}\nfunc TestAT011X(t *testing.T) {}\n")
	got := testsForAT(dir, "AT-010")
	if len(got) != 1 || !strings.HasSuffix(got[0], ":TestAT010_Applied") {
		t.Fatalf("got %v", got)
	}
}

// The policy-tests step through the runner seam: a failing run refuses, a clean one passes.
func TestPolicyTestsStepWithFakeRunner(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, filepath.FromSlash(policyListFile), fakeList)
	var out bytes.Buffer
	o := Options{Out: &out, policyRun: func(_ context.Context, _ string, e policyEntry) ([]byte, error) { return passing(e.Test), nil }}
	if err := policyTests(context.Background(), o, dir); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	o.policyRun = func(context.Context, string, policyEntry) ([]byte, error) { return nil, nil }
	if err := policyTests(context.Background(), o, dir); err == nil || exitCode(err) != 3 {
		t.Fatalf("err = %v", err)
	}
}

// A nonzero exit of go test refuses even when every required test passed in the stream.
func TestPolicyNonzeroGoTestExitRefuses(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, filepath.FromSlash(policyListFile), fakeList)
	runWith := func(exit error, trunc bool) error {
		var out bytes.Buffer
		o := Options{Out: &out, policyRun: func(_ context.Context, _ string, e policyEntry) ([]byte, error) {
			if trunc {
				return []byte(`{"Action":"run","Package":"p","Test":"` + e.Test + `"}` + "\npanic: boom\n"), exit
			}
			return passing(e.Test), exit
		}}
		r := &report{out: &out}
		r.step("the policy tests", policyTests(context.Background(), o, dir))
		return r.result("the policy tests") // the exit code is the gate's: 3 refused, 4 could not decide
	}
	crash := &goTestExit{Pkg: "./internal/a", Status: "exit status 2", Tail: "WARNING: DATA RACE"}
	if err := runWith(crash, false); err == nil || exitCode(err) != 3 || !strings.Contains(err.Error(), "./internal/a") || !strings.Contains(err.Error(), "exit status 2") || !strings.Contains(err.Error(), "DATA RACE") {
		t.Fatalf("all pass but exit 2: err = %v", err)
	}
	if err := runWith(nil, false); err != nil {
		t.Fatalf("all pass and clean exit: %v", err)
	}
	if err := runWith(&goTestExit{Pkg: "./internal/a", Status: "signal: killed"}, true); err == nil || exitCode(err) != 3 {
		t.Fatalf("truncated stream: err = %v", err)
	}
	if err := runWith(op(errors.New("go not found")), false); err == nil || exitCode(err) != 4 {
		t.Fatalf("go not runnable: err = %v", err)
	}
}

func TestStderrTailIsBounded(t *testing.T) {
	long := strings.Repeat("x", 5000)
	if got := stderrTail([]byte("a\nb\nc\nd\ne\nf\ng\n")); got != "c | d | e | f | g" {
		t.Errorf("tail = %q", got)
	}
	if got := stderrTail([]byte(long)); len(got) > 610 {
		t.Errorf("tail is %d bytes", len(got))
	}
}

// The real list names tests that exist in the tree, so a rename breaks CI, not the release day; and no pending row
// has a test (the gate would refuse), and every required AT of REQ-020 is listed.
func TestRealPolicyListNamesTestsThatExist(t *testing.T) {
	root := filepath.Join("..", "..")
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(policyListFile)))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	entries, err := parsePolicyList(f)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, e := range entries {
		listed[e.AT] = true
		if !e.Required {
			if found := testsForAT(root, e.AT); len(found) > 0 {
				t.Errorf("%s is pending but %v exist: promote to required", e.AT, found)
			}
			continue
		}
		dir := filepath.Join(root, filepath.FromSlash(e.Pkg))
		var src strings.Builder
		ents, err := os.ReadDir(dir)
		if err != nil {
			t.Errorf("%s: %v", e.Pkg, err)
			continue
		}
		for _, d := range ents {
			if strings.HasSuffix(d.Name(), "_test.go") {
				b, _ := os.ReadFile(filepath.Join(dir, d.Name()))
				src.Write(b)
			}
		}
		parts := strings.Split(e.Test, "/")
		if !regexp.MustCompile(`(?m)^func ` + regexp.QuoteMeta(parts[0]) + `\(t \*testing\.T\)`).MatchString(src.String()) {
			t.Errorf("%s: no func %s in %s", e.AT, parts[0], e.Pkg)
		}
		for _, sub := range parts[1:] {
			if !strings.Contains(src.String(), `"`+strings.ReplaceAll(sub, "_", " ")+`"`) {
				t.Errorf("%s: no subtest %q in %s", e.AT, sub, e.Pkg)
			}
		}
	}
	for _, at := range []string{"AT-001", "AT-003", "AT-004", "AT-005", "AT-010", "AT-011", "AT-020"} {
		if !listed[at] {
			t.Errorf("%s (REQ-020) is not in %s", at, policyListFile)
		}
	}
	// The statuses and tests the gap closing set: every row is required, and AT-010 runs the live two-server test.
	want := map[string][]string{
		"AT-003": {"TestLiveMatchesCheck/deny_key_(AT-003)", "TestAT003OwnerLineDroppedAndDenyHolds"},
		"AT-005": {"TestAT005DenyAllSenderIsRefused"},
		"AT-020": {"TestLiveMatchesCheck", "TestAT020OpenReplyAndPlumbingSubjectsArePinned"},
		"AT-010": {"TestAT010Live"},
	}
	got := map[string][]string{}
	for _, e := range entries {
		if e.Required {
			got[e.AT] = append(got[e.AT], e.Test)
		} else {
			t.Errorf("%s is pending: every REQ-020 AT is required", e.AT)
		}
	}
	for at, tests := range want {
		if !reflect.DeepEqual(got[at], tests) {
			t.Errorf("%s required tests = %v, want %v", at, got[at], tests)
		}
	}
}

// scripts/ci-nats-version-changed decides, from the files a pull request changes, whether a pull request runs the policy
// tests.
func TestCINatsVersionChanged(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("..", "ci-nats-version-changed"))
	if err != nil {
		t.Fatal(err)
	}
	gomod := func(v string) string {
		return "module m\n\ngo 1.27\n\nrequire (\n\tgithub.com/nats-io/jwt/v2 v2.8.0\n\tgithub.com/nats-io/nats-server/v2 " + v + "\n)\n"
	}
	repo := filepath.Join(t.TempDir(), "r")
	gitT(t, filepath.Dir(repo), "init", "-q", "-b", "main", repo)
	write(t, repo, "go.mod", gomod("v2.15.1"))
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "base")
	base := gitT(t, repo, "rev-parse", "HEAD")
	decide := func(event, b string) string {
		cmd := exec.Command("bash", script)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "EVENT="+event, "BASE="+b)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commit := func(msg string, files map[string]string) {
		for n, b := range files {
			write(t, repo, n, b)
		}
		gitT(t, repo, "add", "-A")
		gitT(t, repo, "commit", "-q", "-m", msg)
	}
	if got := decide("push", ""); got != "run=true" {
		t.Errorf("push: %s", got)
	}
	if got := decide("pull_request", base); got != "run=false" {
		t.Errorf("unchanged: %s", got)
	}
	commit("other dependency", map[string]string{"go.mod": strings.Replace(gomod("v2.15.1"), "v2.8.0", "v2.9.0", 1)})
	if got := decide("pull_request", base); got != "run=true" {
		t.Errorf("another module bumped (any go.mod change runs the tests): %s", got)
	}
	commit("docs", map[string]string{"x.txt": "x"})
	commit("bump nats-server", map[string]string{"go.mod": gomod("v2.15.2")})
	if got := decide("pull_request", base); got != "run=true" {
		t.Errorf("version bump: %s", got)
	}
	if got := decide("pull_request", ""); got != "run=true" {
		t.Errorf("no base: %s", got)
	}
	mid := gitT(t, repo, "rev-parse", "HEAD")
	commit("list", map[string]string{"scripts/releasecheck/policytests.txt": "x"})
	if got := decide("pull_request", mid); got != "run=true" {
		t.Errorf("list changed: %s", got)
	}
}

// The decision must not depend on how a pipe closes: a large diff, a failed diff and a push all run the tests, and an
// unrelated change does not.
func TestCINatsVersionChangedEdgeCases(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("..", "ci-nats-version-changed"))
	if err != nil {
		t.Fatal(err)
	}
	gomod := func(v string) string {
		return "module m\n\ngo 1.27\n\nrequire github.com/nats-io/nats-server/v2 " + v + "\n"
	}
	repo := filepath.Join(t.TempDir(), "r")
	gitT(t, filepath.Dir(repo), "init", "-q", "-b", "main", repo)
	write(t, repo, "go.mod", gomod("v2.15.1"))
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "base")
	base := gitT(t, repo, "rev-parse", "HEAD")
	decide := func(event, b string) string {
		cmd := exec.Command("bash", script)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "EVENT="+event, "BASE="+b)
		out, err := cmd.Output() // stdout only: git's complaint about a bad base goes to stderr
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commit := func(msg string, n int, prefix string) {
		for i := 0; i < n; i++ {
			write(t, repo, fmt.Sprintf("%s/f%04d_%s.txt", prefix, i, strings.Repeat("x", 200)), "x")
		}
		gitT(t, repo, "add", "-A")
		gitT(t, repo, "commit", "-q", "-m", msg)
	}

	commit("unrelated", 3, "docs")
	if got := decide("pull_request", base); got != "run=false" {
		t.Errorf("unrelated files only: %s", got)
	}
	if got := decide("push", base); got != "run=true" {
		t.Errorf("push: %s", got)
	}
	if got := decide("pull_request", "0123456789abcdef0123456789abcdef01234567"); got != "run=true" {
		t.Errorf("bad base: %s", got)
	}

	// The policy list sorts before hundreds of unrelated names, so grep -q matches and exits while git diff still has
	// far more than a pipe holds to write: the old pipeline then failed with SIGPIPE and read as "no match".
	commit("many unrelated", 600, "zbulk")
	write(t, repo, "scripts/releasecheck/policytests.txt", "x")
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "list")
	if got := decide("pull_request", base); got != "run=true" {
		t.Errorf("large diff including the list: %s", got)
	}
	// A large diff with no match still reads run=false.
	mid := gitT(t, repo, "rev-parse", "HEAD")
	commit("more unrelated", 600, "zbulk2")
	if got := decide("pull_request", mid); got != "run=false" {
		t.Errorf("large unrelated diff: %s", got)
	}

	write(t, repo, "go.mod", gomod("v2.15.2"))
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "bump")
	if got := decide("pull_request", mid); got != "run=true" {
		t.Errorf("version line changed: %s", got)
	}

	// Anything that can change module resolution runs the tests, not only the requirement line.
	for _, c := range []struct{ name, file, body string }{
		{"replace directive", "go.mod", gomod("v2.15.2") + "replace github.com/nats-io/nats-server/v2 => ../fork\n"},
		{"go.sum only", "go.sum", "github.com/nats-io/nats-server/v2 v2.15.2 h1:x\n"},
		{"go.work added", "go.work", "go 1.27\nuse .\n"},
		{"go.work.sum", "go.work.sum", "x\n"},
		{"vendor", "vendor/modules.txt", "# x\n"},
		{"exclude directive", "go.mod", gomod("v2.15.2") + "exclude github.com/nats-io/nats-server/v2 v2.15.3\n"},
	} {
		before := gitT(t, repo, "rev-parse", "HEAD")
		write(t, repo, c.file, c.body)
		gitT(t, repo, "add", "-A")
		gitT(t, repo, "commit", "-q", "-m", c.name)
		if got := decide("pull_request", before); got != "run=true" {
			t.Errorf("%s: %s", c.name, got)
		}
	}
	after := gitT(t, repo, "rev-parse", "HEAD")
	commit("unrelated again", 2, "docs2")
	if got := decide("pull_request", after); got != "run=false" {
		t.Errorf("unrelated after resolution files: %s", got)
	}
}
