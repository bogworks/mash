package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore"
	"github.com/bogworks/mash/internal/cmdcore/cmds"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/testenv"
)

// doctorOf runs mash doctor against a healthSvc built by a test (a session's own state cannot be had from a shell): the
// exit code, stdout, stderr, and with --json too.
func doctorOf(h *healthSvc, asJSON bool) (code int, out, errs string) {
	var o, e bytes.Buffer
	env := func(context.Context) (*cmdcore.Env, error) { return &cmdcore.Env{Svc: h}, nil }
	code = cmdcore.CLI(cmds.Doctor, env)(nil, strings.NewReader(""), &o, &e, cliframe.Global{JSON: asJSON})
	return code, o.String(), e.String()
}

// Every problem `mash doctor` can show ends with at most one hint, a command of ours that fixes it; where none does, the
// row's own words say what is wrong and there is no hint (#1099, REQ-006). The report always lists every row, the
// exit code is 1, and --json carries the same hint in one "hint" field (null when none).
func TestDoctorOneHintACommandThatFixesIt(t *testing.T) {
	w := tlsWorld(t, "a")
	plain := newWorld(t, "a")
	plainURL := "tls://" + strings.TrimPrefix(plain.e.URL, "nats://")
	other := testenv.Start(t)
	otherCreds, _ := other.Mint(t, tAddr("a"))
	untrusted := strictEnv(w, "a", nil)
	delete(untrusted, "MASH_CA")
	missing := filepath.Join(t.TempDir(), "none.creds")
	junk := filepath.Join(t.TempDir(), "junk.creds")
	if err := os.WriteFile(junk, []byte(credsSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	ghostCreds, _ := w.e.Mint(t, tAddr("ghost"))
	loose := filepath.Join(t.TempDir(), "loose.creds")
	if b, err := os.ReadFile(w.env["a"]["MASH_CREDS"]); err != nil || os.WriteFile(loose, b, 0o644) != nil {
		t.Fatal("cannot copy creds", err)
	}
	if err := os.Chmod(loose, 0o644); err != nil {
		t.Fatal(err)
	}
	otherBytes, err := os.ReadFile(otherCreds)
	if err != nil {
		t.Fatal(err)
	}
	// the creds path join writes: no MASH_CREDS, <config dir>/<address>.creds
	atDefault := func(content []byte) (map[string]string, string) {
		dir := t.TempDir()
		env := strictEnv(w, "a", map[string]string{"MASH_CONFIG_DIR": dir})
		delete(env, "MASH_CREDS")
		p := filepath.Join(dir, env["MASH_ENDPOINT"]+".creds")
		if content != nil {
			if err := os.WriteFile(p, content, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return env, p
	}
	dMissingEnv, dMissing := atDefault(nil)
	dJunkEnv, dJunk := atDefault([]byte(credsSecret))
	dRefusedEnv, _ := atDefault(otherBytes)
	shell := func(env map[string]string) func(args ...string) ran {
		return func(args ...string) ran { return runMash(env, append([]string{"doctor"}, args...)...) }
	}
	session := func(h *healthSvc) func(args ...string) ran {
		h.session = true
		return func(args ...string) ran { c, o, e := doctorOf(h, len(args) > 0); return ran{c, o, e} }
	}
	lease := func(state string) *healthSvc {
		return &healthSvc{cfg: w.cfgOf(t, "a"), lease: func() (string, string) { return state, "" }}
	}
	held := &healthSvc{cfg: w.cfgOf(t, "a"), mailHeld: func() bool { return true }}
	heldDir, err := endpoint.SpoolDir(held.cfg)
	if err != nil {
		t.Fatal(err)
	}
	heldRow := "delivery  FAIL  cannot write " + config.Tilde(heldDir)
	if len(heldRow) > 80 { // a folder too long for the line goes under the detail column
		heldRow = "delivery  FAIL  cannot write\n" + strings.Repeat(" ", 16) + config.Tilde(heldDir)
	}
	for _, tc := range []struct {
		name, row, hint string
		run             func(args ...string) ran
		long            bool // the row carries a temp path: its width is not ours to bound
	}{
		{"creds file missing", "creds     FAIL  " + config.Tilde(missing) + " is missing", "",
			shell(strictEnv(w, "a", map[string]string{"MASH_CREDS": missing})), true},
		{"creds file not a creds file", "creds     FAIL  " + config.Tilde(junk) + " is not a creds file", "",
			shell(strictEnv(w, "a", map[string]string{"MASH_CREDS": junk})), true},
		{"default creds file missing", "creds     FAIL  " + config.Tilde(dMissing) + " is missing", cmds.JoinFix, shell(dMissingEnv), true},
		{"default creds file not a creds file", "creds     FAIL  " + config.Tilde(dJunk) + " is not a creds file", cmds.JoinReplaceFix, shell(dJunkEnv), true},
		{"default creds refused", "sign-in   FAIL  the server refused these credentials", cmds.JoinReplaceFix,
			shell(dRefusedEnv), false},
		{"creds file readable by others", "creds     FAIL  " + config.Tilde(loose) + " can be read by others", "",
			shell(strictEnv(w, "a", map[string]string{"MASH_CREDS": loose})), true},
		{"server not answering", "server    FAIL  the server did not answer: tls://127.0.0.1:1", "",
			shell(strictEnv(w, "a", map[string]string{"MASH_SERVER": "tls://127.0.0.1:1", "MASH_OPEN_TIMEOUT": "2s"})), false},
		{"certificate not trusted", "server    FAIL  the server's certificate is not trusted: " + w.e.URL, "",
			shell(untrusted), true},
		{"server does not offer TLS", "server    FAIL  the server did not offer TLS: " + plainURL, "",
			shell(strictEnv(w, "a", map[string]string{"MASH_SERVER": plainURL, "MASH_OPEN_TIMEOUT": "5s"})), true},
		{"credentials refused", "sign-in   FAIL  the server refused these credentials", "",
			shell(strictEnv(w, "a", map[string]string{"MASH_CREDS": otherCreds})), false},
		{"endpoint deleted", "sign-in   FAIL  " + cmds.ReasonEndpointDeleted, "mash join <file>",
			shell(strictEnv(w, "a", map[string]string{"MASH_ENDPOINT": tTo("ghost"), "MASH_CREDS": ghostCreds})), false},
		{"another session holds the endpoint", "delivery  FAIL  another session holds this endpoint", "", session(lease(cmds.LeaseOther)), false},
		{"a newer session replaced this one", "delivery  FAIL  this session lost this endpoint to a newer one", "", session(lease(cmds.LeaseLost)), false},
		{"arriving mail cannot be stored", heldRow, "", session(held), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.run()
			if r.code != 1 {
				t.Errorf("exit %d, want 1:\n%s", r.code, r.out)
			}
			if !strings.Contains(r.out, tc.row+"\n") {
				t.Errorf("no row %q in:\n%s", tc.row, r.out)
			}
			if !strings.HasSuffix(r.out, "\n\n1 problem.\n") {
				t.Errorf("count: %q", r.out)
			}
			want := ""
			if tc.hint != "" {
				want = "hint: " + tc.hint + "\n"
				if cmdcore.CheckOnly(tc.hint) {
					t.Errorf("the hint %q only checks", tc.hint)
				}
			}
			if r.errs != want {
				t.Errorf("stderr %q, want %q", r.errs, want)
			}
			if !tc.long {
				for _, l := range strings.Split(r.out, "\n") {
					if len(l) > 80 {
						t.Errorf("%d columns: %q", len(l), l)
					}
				}
				if len(r.errs) > 81 {
					t.Errorf("hint is %d columns", len(r.errs)-1)
				}
			}
			j := tc.run("--json")
			if j.code != 1 || j.errs != "" {
				t.Fatalf("--json: %d %q", j.code, j.errs)
			}
			var got struct {
				Problems int
				Checks   []struct{ Name, State string }
				Hint     *string
			}
			if err := json.Unmarshal([]byte(j.out), &got); err != nil {
				t.Fatal(err)
			}
			names := []string{}
			for _, c := range got.Checks {
				if c.Name != "claude" { // the claude row is the command's: a session test's report here has none
					names = append(names, c.Name)
				}
			}
			if strings.Join(names, ",") != "config,creds,server,sign-in,delivery,refused" || got.Problems != 1 {
				t.Errorf("checks %v problems %d", names, got.Problems)
			}
			if !strings.Contains(j.out, `"hint":`) {
				t.Errorf("no hint field in %s", j.out)
			}
			switch {
			case tc.hint == "" && got.Hint != nil:
				t.Errorf("hint %q, want null", *got.Hint)
			case tc.hint != "" && (got.Hint == nil || *got.Hint != tc.hint):
				t.Errorf("hint %v, want %q", got.Hint, tc.hint)
			}
		})
	}
}

// No problem: exit 0, six rows, no hint, hint null. A warning alone with no command that fixes it does not make a hint, and exits 0 (1 with --strict).
func TestDoctorNoProblemNoHint(t *testing.T) {
	w := tlsWorld(t, "a")
	env := strictEnv(w, "a", nil)
	r := runMash(env, "doctor")
	if r.code != 0 || r.errs != "" || !strings.HasSuffix(r.out, "\n\nNo problems.\n") {
		t.Fatalf("%+v", r)
	}
	if j := runMash(env, "doctor", "--json"); j.code != 0 || !strings.HasSuffix(j.out, `],"hint":null}`+"\n") {
		t.Fatalf("%+v", j)
	}
	writeRefused(t, env, 2)
	if r := runMash(env, "doctor"); r.code != 0 || r.errs != "" {
		t.Fatalf("warning: %+v", r)
	}
	if r := runMash(env, "doctor", "--strict"); r.code != 1 || r.errs != "" {
		t.Fatalf("warning, strict: %+v", r)
	}
}

// Standing order 17: other users in the account never make a mash doctor row fail. mash doctor reads nothing about the
// other users (its rows are config, creds, server, sign-in, delivery and refused: its own); with a second endpoint
// minted on the same server it is still clean.
func TestDoctorIsNotAboutOtherUsers(t *testing.T) {
	w := tlsWorld(t, "a", "b", "c")
	r := runMash(strictEnv(w, "a", nil), "doctor")
	if r.code != 0 || !strings.HasSuffix(r.out, "\n\nNo problems.\n") || r.errs != "" {
		t.Fatalf("%+v", r)
	}
}

// No hint any doctor row can carry is a command that only checks: the commands this package names as hints are listed
// here, and none of them is one of the check-only names.
func TestNoDoctorHintOnlyChecks(t *testing.T) {
	for _, hint := range []string{cmds.JoinFix, cmds.JoinReplaceFix, cmds.EndpointDeletedFix, cmds.HooksFailingFix} {
		if cmdcore.CheckOnly(hint) {
			t.Errorf("%q only checks", hint)
		}
		if f := strings.Fields(hint); len(f) < 2 || (f[0] != "mash" && f[0] != "claude") {
			t.Errorf("%q is not a command", hint)
		}
	}
	for _, bad := range []string{"mash doctor", "mash status", "mashctl doctor", "mash who"} {
		if !cmdcore.CheckOnly(bad) {
			t.Errorf("%q should be rejected", bad)
		}
	}
}
