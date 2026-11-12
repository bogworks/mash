package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	err := judgePolicy(&out, fakeEntries(t), streams, tests)
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
}

// scripts/ci-nats-version-changed decides, from go.mod at the base and at HEAD, whether a pull request runs the policy
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
	if got := decide("pull_request", base); got != "run=false" {
		t.Errorf("another module bumped: %s", got)
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
