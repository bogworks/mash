package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAT005FlagParsing(t *testing.T) {
	ok := [][]string{
		{"--hosts", "podman", "--run-by", "manual", "--host-label", "dev-box.1"},
		{"--hosts", "podman", "--run-by", "ci", "--host-label", "A", "--record-out", "/x/y"},
		{"--hosts", "local", "--dry-setup"},
		{},
	}
	for _, a := range ok {
		if _, err := parseAt005(a); err != nil {
			t.Errorf("%v refused: %v", a, err)
		}
	}
	bad := [][]string{
		{"--hosts", "podman"},
		{"--hosts", "podman", "--run-by", "manual"},
		{"--hosts", "podman", "--host-label", "x"},
		{"--hosts", "podman", "--run-by", "robot", "--host-label", "x"},
		{"--hosts", "podman", "--run-by", "", "--host-label", "x"},
		{"--hosts", "podman", "--run-by", "ci", "--host-label", "has space"},
		{"--hosts", "podman", "--run-by", "ci", "--host-label", "under_score"},
		{"--hosts", "podman", "--run-by", "ci", "--host-label", strings.Repeat("a", 41)},
		{"--hosts", "podman", "--run-by", "ci", "--host-label", ""},
		{"--hosts", "local", "--run-by", "manual"},
		{"--hosts", "ssh"},
		{"extra"},
	}
	for _, a := range bad {
		if _, err := parseAt005(a); err == nil {
			t.Errorf("%v accepted", a)
		}
	}
	if _, err := parseAt005([]string{"--hosts", "podman", "--run-by", "ci", "--host-label", strings.Repeat("a", 40)}); err != nil {
		t.Errorf("40 characters refused: %v", err)
	}
	var out, errb strings.Builder
	if c := at005Main([]string{"--hosts", "podman"}, &out, &errb); c != 2 {
		t.Errorf("exit %d, want 2", c)
	}
}

func TestAT005RecordOutRefusesExisting(t *testing.T) {
	f := filepath.Join(t.TempDir(), "rec.md")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	if c := at005Main([]string{"--record-out", f}, &out, &errb); c != 2 || !strings.Contains(errb.String(), "refusing to overwrite") {
		t.Errorf("exit %d: %s", c, errb.String())
	}
	if b, _ := os.ReadFile(f); string(b) != "x" {
		t.Error("existing file changed")
	}
}

func TestAT005RunLinePodman(t *testing.T) {
	f := podmanFake(func(c Cmd) (string, string, int, bool) {
		if strings.HasPrefix(strings.Join(c.Argv, " "), "podman version --format {{.Client.Version}}") {
			return "5.3.1\n", "", 0, true
		}
		return "", "", 0, false
	})
	got := runLine(&Podman{Runner: f}, "manual", "dev-box")
	if got != "run: manual, host dev-box, substrate podman 5.3.1" {
		t.Errorf("got %q", got)
	}
	if !f.has("podman version --format {{.Client.Version}}") {
		t.Error("version not asked")
	}
	fail := podmanFake(func(Cmd) (string, string, int, bool) { return "", "boom", 1, true })
	if got := runLine(&Podman{Runner: fail}, "ci", "m"); got != "run: ci, host m, substrate podman unknown" {
		t.Errorf("got %q", got)
	}
}

func TestAT005RecordLocalAndNoHostname(t *testing.T) {
	line := runLine(Local{}, "", "")
	if line != "run: local loopback (dry setup or developer check; not a record for the contract)" {
		t.Errorf("got %q", line)
	}
	mk := func(local bool, env string) string {
		return Record{RunID: "r", Revision: "v", AT: "at005", Scenario: "s", Env: []string{env},
			Observed: []Assertion{{Name: "a", Pass: true}}, LocalSubstrate: local}.Markdown()
	}
	l := mk(true, line)
	if !strings.Contains(l, "local substrate: not AT-005 evidence") || !strings.Contains(l, line) {
		t.Errorf("local record:\n%s", l)
	}
	p := mk(false, runLine(&Podman{Runner: podmanFake(nil)}, "manual", "dev-box"))
	if strings.Contains(p, "not AT-005 evidence") || !strings.Contains(p, "## Result\n\nPASS\n") {
		t.Errorf("podman record:\n%s", p)
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		for _, rec := range []string{l, p} {
			if strings.Contains(rec, h) {
				t.Errorf("record leaks the hostname %q", h)
			}
		}
	}
}
