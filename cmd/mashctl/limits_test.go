package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/policy"
)

func TestParseMaxControlLine(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if n, err := parseMaxControlLine("", env(nil)); n != 0 || err != nil {
		t.Errorf("none: %d %v", n, err)
	}
	if n, err := parseMaxControlLine("2048", env(map[string]string{maxControlLineEnv: "9"})); n != 2048 || err != nil {
		t.Errorf("flag wins: %d %v", n, err)
	}
	if n, err := parseMaxControlLine("", env(map[string]string{maxControlLineEnv: " 1024 "})); n != 1024 || err != nil {
		t.Errorf("env: %d %v", n, err)
	}
	for _, bad := range []string{"0", "-5", "big", "4k"} {
		if _, err := parseMaxControlLine(bad, env(nil)); err == nil || !strings.Contains(err.Error(), "--max-control-line") {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if _, err := parseMaxControlLine("", env(map[string]string{maxControlLineEnv: "x"})); err == nil || !strings.Contains(err.Error(), maxControlLineEnv) {
		t.Errorf("env bad: %v", err)
	}
}

func TestPolicyApplyAcceptsMaxControlLineFlag(t *testing.T) {
	o, err := parseApplyArgs("apply", []string{"--max-control-line", "2048", "--dry-run"})
	if err != nil || o.maxControlLine != "2048" || !o.dryRun {
		t.Fatalf("%+v %v", o, err)
	}
	if o, err = parseApplyArgs("diff", []string{"--max-control-line=1024"}); err != nil || o.maxControlLine != "1024" {
		t.Fatalf("%+v %v", o, err)
	}
	if _, err = parseApplyArgs("apply", []string{"--max-control-line"}); err == nil {
		t.Fatal("a flag with no value")
	}
}

// add and reissue measure against the limit the operator gives when the servers cannot be asked, name the server the
// limit stands for, and change nothing on a refusal.
func TestAddAndReissueMeasureAgainstTheOperatorsLimit(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.env[maxControlLineEnv] = "300"
	before, _ := os.ReadFile(h.policy)
	_, errb, code := h.run("add", "c", "--zone", "green")
	if code != 1 || !strings.Contains(errb, `sender "c"`) || !strings.Contains(errb, `server "--max-control-line"`) ||
		!strings.Contains(errb, "allows 300 (max_control_line)") || !strings.Contains(errb, "nothing was changed, signed or pushed") {
		t.Fatalf("add: %d %q", code, errb)
	}
	if after, _ := os.ReadFile(h.policy); !bytes.Equal(before, after) {
		t.Error("policy changed")
	}
	if _, err := os.Lstat(h.creds("c")); err == nil {
		t.Error("creds written")
	}
	if r := h.records(); len(r) != 0 {
		t.Errorf("records %v", r)
	}

	h.env[maxControlLineEnv] = "none"
	if _, errb, code := h.run("add", "c", "--zone", "green"); code != 1 || !strings.Contains(errb, maxControlLineEnv+" must be a whole number") {
		t.Fatalf("bad value: %d %q", code, errb)
	}

	// At 4,096 the same add is accepted, and no note says the limit was a default (the operator gave it).
	h.env[maxControlLineEnv] = "4096"
	_, errb, code = h.run("add", "c", "--zone", "green")
	if code != 0 || strings.Contains(errb, policy.DefaultServer) {
		t.Fatalf("add at 4096: %d %q", code, errb)
	}

	// reissue refuses the same way, before it touches the creds file
	h.env[maxControlLineEnv] = "300"
	h.seed("a")
	credsBefore, _ := os.ReadFile(h.creds("a"))
	_, errb, code = h.run("reissue", "a")
	if code != 1 || !strings.Contains(errb, `reissue a: refused: sender "a"`) || !strings.Contains(errb, "allows 300 (max_control_line)") {
		t.Fatalf("reissue: %d %q", code, errb)
	}
	if after, _ := os.ReadFile(h.creds("a")); !bytes.Equal(credsBefore, after) {
		t.Error("creds changed")
	}
}

// Without system credentials and without a limit from the operator there is nobody to ask: add measures against the
// default limit and says so.
func TestAddWithoutSystemCredsSaysItMeasuredTheDefault(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	_, errb, code := h.run("add", "c", "--zone", "green")
	if code != 0 || !strings.Contains(errb, "measured against the "+policy.DefaultServer+" of 4096 bytes") {
		t.Fatalf("%d %q", code, errb)
	}
}
