package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/bogworks/mash/internal/testenv"
)

// severalInstalled installs the creds of each named endpoint of w in one config directory and returns the environment
// of a shell there: no endpoint is chosen.
func severalInstalled(t *testing.T, w *world, names ...string) (map[string]string, string) {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "mash")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		b, err := os.ReadFile(w.env[n]["MASH_CREDS"])
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, tTo(n)+".creds"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return map[string]string{"HOME": home, "MASH_SERVER": w.env[names[0]]["MASH_SERVER"], "MASH_CA": w.e.CAFile}, dir
}

func TestDoctorSeveralAllClean(t *testing.T) {
	w := tlsWorld(t, "a", "b")
	env, _ := severalInstalled(t, w, "a", "b")
	r := runMash(env, "doctor")
	w1 := max(len("ENDPOINT"), len(tTo("a"))) + 2
	row := func(a, p, wn, f string) string {
		return strings.TrimRight(a+strings.Repeat(" ", w1-len(a))+p+strings.Repeat(" ", 10-len(p))+wn+strings.Repeat(" ", 10-len(wn))+f, " ") + "\n"
	}
	want := "Endpoints installed here: 2.\n\n" + row("ENDPOINT", "PROBLEMS", "WARNINGS", "FIRST ISSUE") +
		row(tTo("a"), "0", "0", "") + row(tTo("b"), "0", "0", "") + "\nNo problems.\n"
	if r.code != 0 || r.out != want || r.errs != "" {
		t.Fatalf("doctor:\n%+v\nwant %q", r, want)
	}
	s := runMash(env, "doctor", "--strict")
	if s.code != 0 || s.out != want {
		t.Fatalf("--strict: %+v", s)
	}
	j := runMash(env, "doctor", "--json")
	var o struct {
		Endpoints []struct {
			Endpoint string
			Problems int
			Checks   []struct{ Name string }
		}
		Problems, Warnings int
		Hint               *string
	}
	if err := json.Unmarshal([]byte(j.out), &o); err != nil || j.code != 0 || j.errs != "" {
		t.Fatalf("--json: %+v %v", j, err)
	}
	if len(o.Endpoints) != 2 || o.Endpoints[0].Endpoint != tTo("a") || o.Endpoints[1].Endpoint != tTo("b") ||
		len(o.Endpoints[0].Checks) != 7 || o.Problems != 0 || o.Warnings != 0 || o.Hint != nil {
		t.Fatalf("object %+v", o)
	}
	if !strings.HasSuffix(j.out, `],"problems":0,"warnings":0,"hint":null}`+"\n") {
		t.Fatalf("shape: %s", j.out)
	}
}

func TestDoctorSeveralOneProblem(t *testing.T) {
	w := tlsWorld(t, "a", "b")
	env, dir := severalInstalled(t, w, "a", "b")
	other := testenv.Start(t)
	creds, _ := other.Mint(t, tAddr("b"))
	b, err := os.ReadFile(creds)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, tTo("b")+".creds"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	r := runMash(env, "doctor")
	lines := strings.Split(r.out, "\n")
	if r.code != 1 || len(lines) < 6 || lines[0] != "Endpoints installed here: 2." ||
		!strings.HasSuffix(strings.TrimRight(lines[4], " "), "1         0         sign-in: the server refused these credentials") ||
		lines[len(lines)-2] != "1 problem. See it: mash doctor --endpoint "+tTo("b") ||
		r.errs != "" {
		t.Fatalf("doctor: %+v", r)
	}
	for _, l := range lines {
		if utf8.RuneCountInString(l) > 79 {
			t.Errorf("line over 79 columns: %q", l)
		}
	}
	// each endpoint is checked as --endpoint would: the same rows
	single := runMash(env, "doctor", "--endpoint", tTo("b"), "--json")
	j := runMash(env, "doctor", "--json")
	var o struct {
		Endpoints []json.RawMessage
		Problems  int
		Hint      *string
	}
	if err := json.Unmarshal([]byte(j.out), &o); err != nil || j.code != 1 || o.Problems != 1 || o.Hint != nil {
		t.Fatalf("--json: %+v %v", j, err)
	}
	if string(o.Endpoints[1])+"\n" != single.out {
		t.Fatalf("endpoint b object differs from --endpoint:\n%s\n%s", o.Endpoints[1], single.out)
	}
}

func TestDoctorSeveralOneWarning(t *testing.T) {
	w := tlsWorld(t, "a", "b")
	env, _ := severalInstalled(t, w, "a", "b")
	// The launcher row is machine-wide, so it warns in every endpoint's result: the totals count it once for each.
	old := launcherGetenv
	launcherGetenv = func(k string) string {
		if k == launchedFromEnv {
			return "path"
		}
		return ""
	}
	t.Cleanup(func() { launcherGetenv = old })
	r := runMash(env, "doctor")
	row := func(n string) string {
		return tTo(n) + strings.Repeat(" ", max(len("ENDPOINT"), len(tTo(n)))+2-len(tTo(n))) +
			"0         1         launcher: mash from PATH, not the pinned one\n"
	}
	if r.code != 0 || !strings.Contains(r.out, row("a")+row("b")) ||
		!strings.HasSuffix(r.out, "\n\n2 warnings. See it: mash doctor --endpoint "+tTo("a")+"\n") ||
		r.errs != "" {
		t.Fatalf("doctor: %+v", r)
	}
	// a warning alone is a problem only with --strict
	if s := runMash(env, "doctor", "--strict"); s.code != 1 || s.out != r.out {
		t.Fatalf("--strict: %+v", s)
	}
	j := runMash(env, "doctor", "--json")
	if j.code != 0 || !strings.HasSuffix(j.out, `"problems":0,"warnings":2,"hint":null}`+"\n") {
		t.Fatalf("--json: %+v", j)
	}
}

// One endpoint installed, or one chosen, is unchanged; status and the other commands still stop at several.
func TestDoctorSeveralLeavesTheRestAlone(t *testing.T) {
	w := tlsWorld(t, "a", "b")
	env, _ := severalInstalled(t, w, "a", "b")
	chosen := runMash(env, "doctor", "--endpoint", tTo("a"))
	if chosen.code != 0 || strings.Contains(chosen.out, "Endpoints installed") || !strings.HasPrefix(chosen.out, "config    ok\n") {
		t.Fatalf("--endpoint: %+v", chosen)
	}
	viaEnv := map[string]string{"MASH_ENDPOINT": tTo("a")}
	for k, v := range env {
		viaEnv[k] = v
	}
	if r := runMash(viaEnv, "doctor"); r.out != chosen.out {
		t.Fatalf("MASH_ENDPOINT: %+v", r)
	}
	solo, _ := severalInstalled(t, w, "a")
	if r := runMash(solo, "doctor", "--json"); r.code != 0 || !strings.HasPrefix(r.out, `{"endpoint":"`+tTo("a")+`","problems":0`) {
		t.Fatalf("one installed: %+v", r)
	}
	if r := runMash(env, "status"); r.code != 2 || r.out != "" ||
		r.errs != "error: more than one endpoint is installed here: "+tTo("a")+", "+tTo("b")+"\nhint: mash status --endpoint "+tTo("a")+"\n" {
		t.Fatalf("status: %+v", r)
	}
}
