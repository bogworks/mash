package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/config"
)

// TestMain sends the target lines to nowhere, so the tests that compare a verb's whole stderr need not know them; the
// tests of the lines themselves read them through showTarget.
func TestMain(m *testing.M) {
	announceSink = io.Discard
	os.Exit(m.Run())
}

// showTarget lets the target lines reach the stderr the test captures.
func showTarget(t *testing.T) {
	t.Helper()
	announceSink = nil
	t.Cleanup(func() { announceSink = io.Discard })
}

// loadCfg is config.Load over a config dir holding the config.json body (none when ""), with env on top.
func loadCfg(t *testing.T, fileBody string, env map[string]string) (config.Config, func(string) string) {
	t.Helper()
	dir := t.TempDir()
	if fileBody != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(fileBody), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	all := map[string]string{"HOME": t.TempDir(), "MASH_CONFIG_DIR": dir}
	for k, v := range env {
		all[k] = v
	}
	get := func(k string) string { return all[k] }
	cfg, err := loadConfig(get, config.LookupFromFunc(get))
	if err != nil {
		t.Fatal(err)
	}
	return cfg, get
}

func announced(cfg config.Config, env func(string) string, sys *sysTarget) string {
	var b bytes.Buffer
	announceSink = nil
	defer func() { announceSink = io.Discard }()
	announceTarget(cfg, env, &b, sys)
	return b.String()
}

func TestAnnounceNamesTargetAndSources(t *testing.T) {
	cfg, env := loadCfg(t, `{"endpoint":"fileowner.dev.x","admin_creds":"/f/admin.creds"}`,
		map[string]string{"MASH_SERVER": "tls://scratch:4222"})
	got := announced(cfg, env, nil)
	want := "mashctl: target server tls://scratch:4222 (from MASH_SERVER), owner fileowner (from " + cfg.File + "), admin creds /f/admin.creds (from " + cfg.File + ")\n"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestAnnounceEnvOverFileWarnsWithBothValues(t *testing.T) {
	cfg, env := loadCfg(t, `{"servers":"tls://file:4222","endpoint":"fo.dev.x","admin_creds":"/f/a.creds","ca":"/f/ca.pem"}`,
		map[string]string{"MASH_SERVER": "tls://env:4222", "MASH_ENDPOINT": "eo.dev.x", "MASH_ADMIN_CREDS": "/e/a.creds", "MASH_CA": "/e/ca.pem", "MASH_LAUNCH_TOKEN": "x"})
	got := announced(cfg, env, nil)
	for _, w := range []string{
		"warning: MASH_SERVER (tls://env:4222) overrides config.json's servers (tls://file:4222); mashctl is acting on tls://env:4222.",
		"warning: MASH_ENDPOINT (eo.dev.x) overrides config.json's endpoint (fo.dev.x)",
		"warning: MASH_ADMIN_CREDS (/e/a.creds) overrides config.json's admin_creds (/f/a.creds)",
		"warning: MASH_CA (/e/ca.pem) overrides config.json's ca (/f/ca.pem)",
		"unset them to use config.json.",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("lacks %q:\n%s", w, got)
		}
	}
}

// A central host sets no launch token and no MASH_ENDPOINT: its MASH_SERVER over config.json is meant, and the warning
// about the endpoint session does not apply.
func TestAnnounceEnvOverFileNoWarningWithoutALaunchToken(t *testing.T) {
	cfg, env := loadCfg(t, `{"servers":"tls://file:4222","endpoint":"fo.dev.x"}`,
		map[string]string{"MASH_SERVER": "tls://a:4222,tls://b:4222"})
	got := announced(cfg, env, nil)
	if strings.Contains(got, "endpoint session") {
		t.Errorf("warned on a central host:\n%s", got)
	}
	if !strings.Contains(got, "target server tls://a:4222,tls://b:4222 (from MASH_SERVER)") {
		t.Errorf("the target line lacks the url:\n%s", got)
	}
}

func TestAnnounceAgreeingOrNoFileNoWarning(t *testing.T) {
	cfg, env := loadCfg(t, `{"servers":"tls://same:4222","endpoint":"o.dev.x"}`, map[string]string{"MASH_SERVER": "tls://same:4222", "MASH_ENDPOINT": "o.dev.x"})
	if got := announced(cfg, env, nil); strings.Contains(got, "warning") || strings.Count(got, "\n") != 1 {
		t.Errorf("agreeing values warned:\n%s", got)
	}
	cfg, env = loadCfg(t, "", map[string]string{"MASH_SERVER": "tls://e:4222", "MASH_ENDPOINT": "o.dev.x"})
	if got := announced(cfg, env, nil); strings.Contains(got, "warning") || !strings.Contains(got, "admin creds unset") {
		t.Errorf("no file:\n%s", got)
	}
	// A value only in the file is not an override.
	cfg, env = loadCfg(t, `{"servers":"tls://file:4222","endpoint":"o.dev.x"}`, nil)
	if got := announced(cfg, env, nil); strings.Contains(got, "warning") || !strings.Contains(got, "tls://file:4222 (from "+cfg.File+")") {
		t.Errorf("file only:\n%s", got)
	}
}

func TestAnnounceSessionVariablesAddTheWarning(t *testing.T) {
	for _, v := range []string{"MASH_LAUNCH_TOKEN"} {
		cfg, env := loadCfg(t, "", map[string]string{"MASH_SERVER": "tls://session:4222", "MASH_ENDPOINT": "o.dev.x", v: "x"})
		got := announced(cfg, env, nil)
		if !strings.Contains(got, "inside a mash session, and tls://session:4222 (MASH_SERVER) is that session's own server") {
			t.Errorf("%s: no session warning:\n%s", v, got)
		}
	}
	// The session variables alone, with the url from config.json, add nothing.
	cfg, env := loadCfg(t, `{"servers":"tls://file:4222","endpoint":"o.dev.x"}`, map[string]string{"MASH_ENDPOINT": "o.dev.x"})
	if got := announced(cfg, env, nil); strings.Contains(got, "warning") {
		t.Errorf("url from the file:\n%s", got)
	}
}

// MASH_ENDPOINT (from the environment or the --endpoint flag) is not a sign of a mash session: only the launch token is.
// The session warning fires on a launch token, and when MASH_ENDPOINT comes from the environment while config.json
// names servers; it stays quiet for a central host (no token, no endpoint in the environment, or none in config.json).
func TestAnnounceSessionWarningKeysOnTheLaunchTokenOrAnEnvEndpointOverAFileWithServers(t *testing.T) {
	const file = `{"servers":"tls://file:4222","endpoint":"fo.dev.x"}`
	warned := func(got string) bool {
		return strings.Contains(got, "inside a mash session") || strings.Contains(got, "belong to the endpoint session")
	}
	for name, c := range map[string]struct {
		file string
		env  map[string]string
		want bool
	}{
		"endpoint env, servers in file":    {file, map[string]string{"MASH_ENDPOINT": "eo.dev.x"}, true},
		"endpoint env and server env":      {file, map[string]string{"MASH_ENDPOINT": "eo.dev.x", "MASH_SERVER": "tls://env:4222"}, true},
		"launch token":                     {file, map[string]string{"MASH_LAUNCH_TOKEN": "t", "MASH_SERVER": "tls://env:4222"}, true},
		"endpoint env, no servers in file": {`{"endpoint":"fo.dev.x"}`, map[string]string{"MASH_ENDPOINT": "eo.dev.x", "MASH_SERVER": "tls://env:4222"}, false},
		"central host: server env only":    {file, map[string]string{"MASH_SERVER": "tls://env:4222"}, false},
		"endpoint only in file, no env":    {file, map[string]string{}, false},
	} {
		cfg, getenv := loadCfg(t, c.file, c.env)
		if got := announced(cfg, getenv, nil); warned(got) != c.want {
			t.Errorf("%s: warned=%v, want %v:\n%s", name, warned(got), c.want, got)
		}
	}
}

func TestAnnounceMasksURLPasswordAndNeverPrintsCreds(t *testing.T) {
	dir := t.TempDir()
	creds := filepath.Join(dir, "admin.creds")
	secret := "SUAABCDEFSECRETSEEDVALUE"
	if err := os.WriteFile(creds, []byte("-----BEGIN NATS USER JWT-----\neyJhbGciOi\n------END NATS USER JWT-----\n"+secret), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, env := loadCfg(t, `{"servers":"tls://u:filepass@file:4222"}`, map[string]string{"MASH_SERVER": "tls://bob:hunter2@h:4222,tls://h2:4222", "MASH_ENDPOINT": "o.dev.n", "MASH_ADMIN_CREDS": creds, "MASH_LAUNCH_TOKEN": "t"})
	got := announced(cfg, env, nil)
	for _, bad := range []string{"hunter2", "filepass", secret, "eyJ", "BEGIN"} {
		if strings.Contains(got, bad) {
			t.Errorf("printed %q:\n%s", bad, got)
		}
	}
	if !strings.Contains(got, "tls://***@h:4222,tls://h2:4222") || !strings.Contains(got, "(tls://***@file:4222)") {
		t.Errorf("url not masked as expected:\n%s", got)
	}
}

func TestAnnounceSysCredsPathWhenKnown(t *testing.T) {
	cfg, env := loadCfg(t, "", map[string]string{"MASH_SERVER": "tls://h:1", "MASH_ENDPOINT": "o.dev.x", "MASH_SYS_CREDS": "/s/sys.creds"})
	if got := announced(cfg, env, &sysTarget{}); !strings.Contains(got, ", system creds /s/sys.creds (from MASH_SYS_CREDS)\n") {
		t.Errorf("env:\n%s", got)
	}
	if got := announced(cfg, env, &sysTarget{flagVal: "/x/s.creds"}); !strings.Contains(got, ", system creds /x/s.creds (from --sys-creds)\n") {
		t.Errorf("flag:\n%s", got)
	}
	if got := announced(cfg, env, nil); strings.Contains(got, "system creds") {
		t.Errorf("no sys target asked:\n%s", got)
	}
}

// The verbs, in process against a real server.

func TestTargetLineComesBeforeAWriteAndOfflineVerbsPrintNone(t *testing.T) {
	h := newFleetEnv(t)
	showTarget(t)
	h.env["MASH_LAUNCH_TOKEN"] = "sessionagent"

	out, errb, code := h.run("limits", "--show")
	if code != 0 || strings.Contains(errb, "target server") || !strings.Contains(out, "inbox_max_bytes") {
		t.Errorf("limits --show: %d %q", code, errb)
	}
	if _, errb, _ := h.run("permissions", "o.g.a", "--nsc"); strings.Contains(errb, "target server") {
		t.Errorf("permissions: %q", errb)
	}
	if _, errb, _ := h.run("policy"); strings.Contains(errb, "target server") {
		t.Errorf("policy: %q", errb)
	}
	if _, errb, _ := h.run("init", "--local", "--dir", filepath.Join(h.dir, "nope"), "--help"); strings.Contains(errb, "target server") {
		t.Errorf("init --local: %q", errb)
	}

	for _, args := range [][]string{{"list"}, {"init"}, {"limits"}, {"endpoint", "delete", "o.g.zz"}} {
		_, errb, _ := h.run(args...)
		want := "mashctl: target server " + h.e.URL + " (from MASH_SERVER), owner " + h.e.Owner + " (from MASH_ENDPOINT), admin creds " + h.e.AdminCreds + " (from MASH_ADMIN_CREDS)\n"
		if !strings.HasPrefix(errb, want) {
			t.Errorf("%v: stderr %q\nwant prefix %q", args, errb, want)
		}
		if !strings.Contains(errb, "inside a mash session") {
			t.Errorf("%v: no session warning in %q", args, errb)
		}
	}
}

func TestTargetWarnsWhenEnvOverridesConfigJSON(t *testing.T) {
	h := newFleetEnv(t)
	showTarget(t)
	h.env["MASH_LAUNCH_TOKEN"] = "sessionagent" // the warning is given inside an endpoint session only
	if err := os.WriteFile(filepath.Join(h.dir, "config.json"), []byte(`{"servers":"tls://scratch.example:4222"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, errb, code := h.run("list")
	if code != 0 {
		t.Fatalf("list: %d %q", code, errb)
	}
	want := "warning: MASH_SERVER (" + h.e.URL + ") overrides config.json's servers (tls://scratch.example:4222); mashctl is acting on " + h.e.URL + "."
	if !strings.Contains(errb, want) || strings.Contains(errb, "overrides config.json's endpoint") {
		t.Errorf("stderr:\n%s\nwant %q", errb, want)
	}
	// With the file and the environment agreeing there is no warning.
	delete(h.env, "MASH_LAUNCH_TOKEN")
	if err := os.WriteFile(filepath.Join(h.dir, "config.json"), []byte(`{"servers":"`+h.e.URL+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, errb, _ := h.run("list"); strings.Contains(errb, "warning") {
		t.Errorf("agreeing: %q", errb)
	}
}

// A password (or user name) with the letter s in it is masked like any other: the character classes of the pattern
// exclude whitespace, not the letter s (codex on #208).
func TestMaskURLHidesPasswordsWithTheLetterS(t *testing.T) {
	for in, want := range map[string]string{
		"tls://sam:secret@host":                "tls://***@host",
		"tls://sam:secret@host:4222":           "tls://***@host:4222",
		"nats://a:b@h1:4222,tls://sam:ssss@h2": "nats://***@h1:4222,tls://***@h2",
		"tls://h:4222":                         "tls://h:4222",
		"tls://user@h:4222":                    "tls://***@h:4222",
	} {
		if got := maskURL(in); got != want {
			t.Errorf("maskURL(%q) = %q, want %q", in, got, want)
		}
	}
}
