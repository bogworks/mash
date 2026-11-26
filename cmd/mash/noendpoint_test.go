package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The three wordings of having no endpoint (rules.json) are the same in every mash command that needs one: at a shell
// with none installed, at a shell with several installed, and (status and doctor, the tools of a session that names
// none) in a serve session.
var noEndpointCommands = [][]string{
	{"send", "acme.demo.bob", "hi"},
	{"who"},
	{"inbox"},
	{"read", "1"},
	{"prompt", "acme.demo.bob", "hi"},
	{"status"},
	{"doctor"},
}

func TestEveryCommandWordsNoEndpointInstalledTheSame(t *testing.T) {
	for _, args := range noEndpointCommands {
		r := runMash(map[string]string{"HOME": t.TempDir()}, args...)
		if r.code != 1 || r.out != "" || r.errs != "error: no endpoint is installed here (set mash up with the setup skill: see the quick start)\n" {
			t.Errorf("%v: %+v", args, r)
		}
		if strings.Contains(r.errs, "MASH_ENDPOINT") {
			t.Errorf("%v: %q still names MASH_ENDPOINT", args, r.errs)
		}
	}
	for _, args := range [][]string{{"send", "acme.demo.bob", "hi"}, {"who"}, {"status"}, {"doctor"}} {
		j := runMash(map[string]string{"HOME": t.TempDir()}, append(args, "--json")...)
		if j.code != 1 || j.errs != "" || j.out != `{"error":"no endpoint is installed here (set mash up with the setup skill: see the quick start)"}`+"\n" {
			t.Errorf("%v --json: %+v", args, j)
		}
	}
}

func TestEveryCommandWordsSeveralEndpointsInstalledTheSame(t *testing.T) {
	w := tlsWorld(t, "a", "b")
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "mash")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a", "b"} {
		b, err := os.ReadFile(w.env[n]["MASH_CREDS"])
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, tTo(n)+".creds"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{"HOME": home, "MASH_SERVER": w.env["a"]["MASH_SERVER"], "MASH_CA": w.e.CAFile}
	for _, args := range noEndpointCommands {
		r := runMash(env, args...)
		want := "error: more than one endpoint is installed here: " + tTo("a") + ", " + tTo("b") + "\nhint: mash " + args[0] + " --endpoint " + tTo("a") + "\n"
		if r.code != 2 || r.out != "" || r.errs != want {
			t.Errorf("%v: %+v, want %q", args, r, want)
		}
	}
	for _, args := range [][]string{{"send", "acme.demo.bob", "hi"}, {"who"}, {"status"}, {"doctor"}} {
		j := runMash(env, append(args, "--json")...)
		want := `{"error":"more than one endpoint is installed here: ` + tTo("a") + `, ` + tTo("b") + `","hint":"mash ` + args[0] + ` --endpoint ` + tTo("a") + `"}` + "\n"
		if j.code != 2 || j.errs != "" || j.out != want {
			t.Errorf("%v --json: %+v, want %q", args, j, want)
		}
	}
}

// In a session with MASH_SPOOL_REQUIRED and a spool that cannot be written mail is held, so status does not say ready;
// a healthy session says ready. Doctor's delivery row fails with the folder.
func TestStatusToolSaysNotReadyWhileTheRequiredSpoolFails(t *testing.T) {
	w := newWorld(t, "a")
	w.env["a"]["MASH_SPOOL_REQUIRED"] = "1"
	state := filepath.Join(t.TempDir(), "state")
	w.env["a"]["MASH_STATE_DIR"] = state
	if err := os.WriteFile(state, nil, 0o600); err != nil { // a file where the state directory goes
		t.Fatal(err)
	}
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	want := tTo("a") + ": not ready: mail is held: its folder cannot be written\nhint: mash doctor"
	id := 10
	eventually(t, 15*time.Second, "status says mail is held", func() bool {
		id++
		return reportText(t, s, id, "status") == want
	})
	id++
	wantRow := "delivery     FAIL  cannot write\n                   " // padded for the stored mail row, which the unwritable state shows too
	if got := reportText(t, s, id, "doctor"); !strings.Contains(got, wantRow) || strings.Contains(strings.SplitN(got, "\n                   ", 2)[0], "spool") || !strings.Contains(got, "1 problem") {
		t.Errorf("doctor does not show the held folder: %q", got)
	}
	// the spool can be made again: the retry binds it and status is ready
	if err := os.Remove(state); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	eventually(t, 3*spoolRetryEvery+30*time.Second, "status says ready", func() bool {
		id++
		return strings.HasPrefix(reportText(t, s, id, "status"), tTo("a")+": ready")
	})
}

func TestStatusToolReadyWithAHealthySpool(t *testing.T) {
	w := newWorld(t, "a")
	w.env["a"]["MASH_SPOOL_REQUIRED"] = "1"
	w.env["a"]["MASH_STATE_DIR"] = filepath.Join(t.TempDir(), "state")
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	time.Sleep(time.Second)
	if got := toolText(t, s, 10, "status"); !strings.HasPrefix(got, tTo("a")+": ready") {
		t.Errorf("status: %q", got)
	}
}

// A name that is no command is refused before any endpoint is looked for: the same unknown-command text and exit 2
// whether none, one or several endpoints are installed.
func TestUnknownCommandIsRefusedBeforeTheEndpointLookup(t *testing.T) {
	w := tlsWorld(t, "a", "b")
	for _, n := range []int{0, 1, 2} {
		home := t.TempDir()
		dir := filepath.Join(home, ".config", "mash")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"a", "b"}[:n] {
			b, err := os.ReadFile(w.env[name]["MASH_CREDS"])
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, tTo(name)+".creds"), b, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		env := map[string]string{"HOME": home, "MASH_SERVER": w.env["a"]["MASH_SERVER"], "MASH_CA": w.e.CAFile}
		for _, bad := range []string{"key create"} {
			if r := runMash(env, bad); r.code != 2 || r.errs != "error: unknown command "+strconv.Quote(bad)+"\n" {
				t.Errorf("%d endpoints, %q: %+v", n, bad, r)
			}
		}
		r := runMash(env, "nonsense")
		if want := "error: unknown command \"nonsense\"\n"; r.code != 2 || r.out != "" || r.errs != want {
			t.Errorf("%d endpoints: %+v, want %q", n, r, want)
		}
		j := runMash(env, "nonsense", "--json")
		if want := `{"error":"unknown command \"nonsense\""}` + "\n"; j.code != 2 || j.errs != "" || j.out != want {
			t.Errorf("%d endpoints --json: %+v, want %q", n, j, want)
		}
	}
}
