package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/store"
)

func writeFile(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "mash")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"servers":"tls://x:4222"}`)
	c, err := load(envOf(map[string]string{"MASH_ENDPOINT": "o.g.a"}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.URL != "tls://x:4222" || c.Owner != "o" || c.Name != "a" {
		t.Fatalf("got %+v", c)
	}
	if c.Creds != filepath.Join(home, ".config", "mash", "o.g.a.creds") {
		t.Fatalf("creds %q", c.Creds)
	}
	if c.SettleMS != 100 || c.BatchMsgs != 10 || c.BatchBytes != 16384 || c.IdleWake != 45*time.Second || c.OpenTimeout != 20*time.Second || c.CallTimeout != 15*time.Second {
		t.Fatalf("delivery defaults %+v", c)
	}
	if c.Dir != filepath.Join(home, ".config", "mash") {
		t.Fatalf("dir %q", c.Dir)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"servers":"tls://file:4222","settle_ms":5}`)
	c, err := load(envOf(map[string]string{
		"MASH_ENDPOINT": "p.g.a", "MASH_SERVER": "tls://env:4222",
		"MASH_CREDS": "/x/y.creds", "MASH_SETTLE_MS": "7", "MASH_BATCH_MSGS": "3",
		"MASH_BATCH_BYTES": "99", "MASH_ADMIN_CREDS": "/a.creds",
	}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.URL != "tls://env:4222" || c.Owner != "p" || c.Creds != "/x/y.creds" ||
		c.SettleMS != 7 || c.BatchMsgs != 3 || c.BatchBytes != 99 || c.AdminCreds != "/a.creds" {
		t.Fatalf("got %+v", c)
	}
}

func TestFileSettingsUsedWithoutEnv(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"servers":"tls://f:4222","settle_ms":5,"batch_msgs":2,"batch_bytes":64}`)
	c, err := load(envOf(map[string]string{"MASH_ENDPOINT": "o.g.a"}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.SettleMS != 5 || c.BatchMsgs != 2 || c.BatchBytes != 64 {
		t.Fatalf("got %+v", c)
	}
}

func TestInertWithoutName(t *testing.T) {
	home := t.TempDir()
	c, err := load(envOf(nil), home)
	if err != nil {
		t.Fatalf("inert load must not fail: %v", err)
	}
	if !c.Inert() {
		t.Fatal("expected inert")
	}
}

func TestMissingURLIsErrorUnlessInert(t *testing.T) {
	home := t.TempDir()
	_, err := load(envOf(map[string]string{"MASH_ENDPOINT": "o.g.a"}), home)
	if !errors.Is(err, ErrNoURL) || !strings.Contains(err.Error(), "MASH_SERVER") {
		t.Fatalf("want server error, got %v", err)
	}
	c, err := load(envOf(map[string]string{"MASH_SERVER": "tls://x:1"}), home)
	if err != nil || !c.Inert() {
		t.Fatalf("a server and no endpoint is inert: %+v, %v", c, err)
	}
}

func TestRejectsBadNames(t *testing.T) {
	bad := []string{"", "A", "a--b", "-a", "a-", "1a", "a_b", "a.b", strings.Repeat("a", 49)}
	for _, n := range bad {
		if ValidName(n) {
			t.Errorf("%q should be invalid", n)
		}
	}
	good := []string{"a", "p-ops2", "mash-alpha", strings.Repeat("a", 48)}
	for _, n := range good {
		if !ValidName(n) {
			t.Errorf("%q should be valid", n)
		}
	}
	home := t.TempDir()
	if _, err := load(envOf(map[string]string{"MASH_ENDPOINT": "o.g.Bad", "MASH_SERVER": "u"}), home); err == nil {
		t.Fatal("a bad endpoint name must be rejected")
	}
	if _, err := load(envOf(map[string]string{"MASH_ENDPOINT": "O.g.a", "MASH_SERVER": "u"}), home); err == nil {
		t.Fatal("bad owner must be rejected")
	}
}

func TestBadNumbersRejected(t *testing.T) {
	home := t.TempDir()
	_, err := load(envOf(map[string]string{"MASH_ENDPOINT": "o.g.a", "MASH_SERVER": "u", "MASH_SETTLE_MS": "x"}), home)
	if err == nil {
		t.Fatal("non-numeric MASH_SETTLE_MS must be rejected")
	}
}

func TestLaunchTokenFromTheEnvironment(t *testing.T) {
	base := map[string]string{"MASH_SERVER": "nats://x", "MASH_ENDPOINT": "o.g.n"}
	load := func(tok string) (Config, error) {
		env := map[string]string{"MASH_LAUNCH_TOKEN": tok}
		for k, v := range base {
			env[k] = v
		}
		return load(func(k string) string { return env[k] }, t.TempDir())
	}
	if c, err := load("tok-1.A_b"); err != nil || c.LaunchToken != "tok-1.A_b" {
		t.Fatalf("%+v %v", c, err)
	}
	if c, err := load(""); err != nil || c.LaunchToken != "" {
		t.Fatalf("unset: %+v %v", c, err)
	}
	for _, bad := range []string{"has space", "new\nline", strings.Repeat("a", 129), "semi;colon"} {
		if _, err := load(bad); err == nil {
			t.Errorf("token %q accepted", bad)
		}
	}
}

func TestIdleWakeSettings(t *testing.T) {
	load := func(file string, env map[string]string) (Config, error) {
		home := t.TempDir()
		writeFile(t, home, `{"servers":"tls://f:4222"`+file+`}`)
		env["MASH_ENDPOINT"] = "o.g.a"
		return load(envOf(env), home)
	}
	if c, err := load(`,"idle_wake":"2m"`, map[string]string{}); err != nil || c.IdleWake != 2*time.Minute {
		t.Fatalf("file: %v %v", c.IdleWake, err)
	}
	if c, err := load("", map[string]string{"MASH_IDLE_WAKE": "10s"}); err != nil || c.IdleWake != 10*time.Second {
		t.Fatalf("env: %v %v", c.IdleWake, err)
	}
	if c, err := load(`,"idle_wake":"2m"`, map[string]string{"MASH_IDLE_WAKE": "30m"}); err != nil || c.IdleWake != 30*time.Minute {
		t.Fatalf("env beats file: %v %v", c.IdleWake, err)
	}
	if c, err := load(`,"idle_wake":"5s"`, map[string]string{}); err != nil || c.IdleWake != 5*time.Second {
		t.Fatalf("lower bound: %v %v", c.IdleWake, err)
	}
	for _, bad := range []string{"soon", "4s", "31m", "0s", "-1m", "45"} {
		if _, err := load("", map[string]string{"MASH_IDLE_WAKE": bad}); err == nil || !strings.Contains(err.Error(), "MASH_IDLE_WAKE") {
			t.Errorf("env %q: err %v", bad, err)
		}
		if _, err := load(`,"idle_wake":"`+bad+`"`, map[string]string{}); err == nil || !strings.Contains(err.Error(), "idle_wake") {
			t.Errorf("file %q: err %v", bad, err)
		}
	}
}

func TestRelayWindowSettings(t *testing.T) {
	load := func(file string, env map[string]string) (Config, error) {
		home := t.TempDir()
		writeFile(t, home, `{"servers":"tls://f:4222"`+file+`}`)
		env["MASH_ENDPOINT"] = "o.g.a"
		return load(envOf(env), home)
	}
	if c, err := load("", map[string]string{}); err != nil || c.RelayWindow != 60*time.Second {
		t.Fatalf("default: %v %v", c.RelayWindow, err)
	}
	if c, err := load(`,"relay_window":"5m"`, map[string]string{}); err != nil || c.RelayWindow != 5*time.Minute {
		t.Fatalf("file: %v %v", c.RelayWindow, err)
	}
	if c, err := load(`,"relay_window":"5m"`, map[string]string{"MASH_RELAY_WINDOW": "1h"}); err != nil || c.RelayWindow != time.Hour {
		t.Fatalf("env beats file: %v %v", c.RelayWindow, err)
	}
	if c, err := load(`,"relay_window":"1s"`, map[string]string{}); err != nil || c.RelayWindow != time.Second {
		t.Fatalf("lower bound: %v %v", c.RelayWindow, err)
	}
	for _, bad := range []string{"soon", "999ms", "61m", "0s", "-1m", "60"} {
		if _, err := load("", map[string]string{"MASH_RELAY_WINDOW": bad}); err == nil || !strings.Contains(err.Error(), "MASH_RELAY_WINDOW") {
			t.Errorf("env %q: err %v", bad, err)
		}
		_, err := load(`,"relay_window":"`+bad+`"`, map[string]string{})
		if err == nil || !strings.Contains(err.Error(), "relay_window") {
			t.Errorf("file %q: err %v", bad, err)
		}
		if err := ValidateFile([]byte(`{"relay_window":"` + bad + `"}`)); err == nil || !strings.Contains(err.Error(), "relay_window") {
			t.Errorf("validate %q: err %v", bad, err)
		}
	}
}

func TestTimeoutSettings(t *testing.T) {
	load := func(file string, env map[string]string) (Config, error) {
		home := t.TempDir()
		writeFile(t, home, `{"servers":"tls://f:4222"`+file+`}`)
		env["MASH_ENDPOINT"] = "o.g.a"
		return load(envOf(env), home)
	}
	if c, err := load("", map[string]string{}); err != nil || c.OpenTimeout != 20*time.Second || c.CallTimeout != 15*time.Second {
		t.Fatalf("defaults: %v %v %v", c.OpenTimeout, c.CallTimeout, err)
	}
	if c, err := load(`,"open_timeout":"1m","call_timeout":"45s"`, map[string]string{}); err != nil || c.OpenTimeout != time.Minute || c.CallTimeout != 45*time.Second {
		t.Fatalf("file: %v %v %v", c.OpenTimeout, c.CallTimeout, err)
	}
	if c, err := load("", map[string]string{"MASH_OPEN_TIMEOUT": "90s", "MASH_CALL_TIMEOUT": "2m"}); err != nil || c.OpenTimeout != 90*time.Second || c.CallTimeout != 2*time.Minute {
		t.Fatalf("env: %v %v %v", c.OpenTimeout, c.CallTimeout, err)
	}
	if c, err := load(`,"open_timeout":"1m","call_timeout":"45s"`, map[string]string{"MASH_OPEN_TIMEOUT": "3m"}); err != nil || c.OpenTimeout != 3*time.Minute || c.CallTimeout != 45*time.Second {
		t.Fatalf("env beats file, each key on its own: %v %v %v", c.OpenTimeout, c.CallTimeout, err)
	}
	// An invalid file value under a valid env override loads: the effective value is chosen before it is validated.
	if c, err := load(`,"open_timeout":"soon","call_timeout":"0s"`, map[string]string{"MASH_OPEN_TIMEOUT": "90s", "MASH_CALL_TIMEOUT": "2m"}); err != nil || c.OpenTimeout != 90*time.Second || c.CallTimeout != 2*time.Minute {
		t.Fatalf("invalid file values under valid env overrides: %v %v %v", c.OpenTimeout, c.CallTimeout, err)
	}
	// Each on its own: one overridden, the other's invalid file value is still the effective one and still errors.
	if _, err := load(`,"open_timeout":"soon","call_timeout":"0s"`, map[string]string{"MASH_OPEN_TIMEOUT": "90s"}); err == nil || !strings.Contains(err.Error(), "call_timeout") {
		t.Fatalf("invalid effective call_timeout: %v", err)
	}
	if _, err := load(`,"open_timeout":"soon","call_timeout":"1m"`, map[string]string{"MASH_CALL_TIMEOUT": "2m"}); err == nil || !strings.Contains(err.Error(), "open_timeout") {
		t.Fatalf("invalid effective open_timeout: %v", err)
	}
	for _, k := range []struct{ key, env string }{{"open_timeout", "MASH_OPEN_TIMEOUT"}, {"call_timeout", "MASH_CALL_TIMEOUT"}} {
		for _, bad := range []string{"soon", "0s", "0", "-5s", "45", "1x"} {
			if _, err := load("", map[string]string{k.env: bad}); err == nil || !strings.Contains(err.Error(), k.env) {
				t.Errorf("env %s=%q: err %v", k.env, bad, err)
			}
			if _, err := load(`,"`+k.key+`":"`+bad+`"`, map[string]string{}); err == nil || !strings.Contains(err.Error(), k.key) {
				t.Errorf("file %s=%q: err %v", k.key, bad, err)
			}
		}
	}
}

func TestMoreTimeoutSettings(t *testing.T) {
	load := func(file string, env map[string]string) (Config, error) {
		home := t.TempDir()
		writeFile(t, home, `{"servers":"tls://f:4222"`+file+`}`)
		env["MASH_ENDPOINT"] = "o.g.a"
		return load(envOf(env), home)
	}
	keys := []struct {
		key, env string
		get      func(Config) time.Duration
	}{
		{"lease_retry", "MASH_LEASE_RETRY", func(c Config) time.Duration { return c.LeaseRetry }},
		{"transient_budget", "MASH_TRANSIENT_BUDGET", func(c Config) time.Duration { return c.TransientBudget }},
		{"transient_max", "MASH_TRANSIENT_MAX", func(c Config) time.Duration { return c.TransientMax }},
		{"cli_timeout", "MASH_CLI_TIMEOUT", func(c Config) time.Duration { return c.CLITimeout }},
		{"admin_timeout", "MASH_ADMIN_TIMEOUT", func(c Config) time.Duration { return c.AdminTimeout }},
	}
	for _, k := range keys {
		if c, err := load("", map[string]string{}); err != nil || k.get(c) != 0 {
			t.Errorf("%s unset: %v %v", k.key, k.get(c), err)
		}
		if c, err := load(`,"`+k.key+`":"7s"`, map[string]string{}); err != nil || k.get(c) != 7*time.Second {
			t.Errorf("%s file: %v %v", k.key, k.get(c), err)
		}
		if c, err := load("", map[string]string{k.env: "9s"}); err != nil || k.get(c) != 9*time.Second {
			t.Errorf("%s env: %v %v", k.env, k.get(c), err)
		}
		if c, err := load(`,"`+k.key+`":"7s"`, map[string]string{k.env: "9s"}); err != nil || k.get(c) != 9*time.Second {
			t.Errorf("%s env beats file: %v %v", k.env, k.get(c), err)
		}
		if c, err := load(`,"`+k.key+`":"soon"`, map[string]string{k.env: "9s"}); err != nil || k.get(c) != 9*time.Second {
			t.Errorf("%s invalid file under valid env: %v %v", k.key, k.get(c), err)
		}
		for _, bad := range []string{"soon", "0s", "0", "-5s", "45"} {
			if _, err := load("", map[string]string{k.env: bad}); err == nil || !strings.Contains(err.Error(), k.env) {
				t.Errorf("env %s=%q: err %v", k.env, bad, err)
			}
			if _, err := load(`,"`+k.key+`":"`+bad+`"`, map[string]string{}); err == nil || !strings.Contains(err.Error(), k.key) {
				t.Errorf("file %s=%q: err %v", k.key, bad, err)
			}
		}
	}
}

func TestLoadSpoolFromStart(t *testing.T) {
	load := func(t *testing.T, file string, env map[string]string) (Config, error) {
		t.Helper()
		home := t.TempDir()
		writeFile(t, home, file)
		env["MASH_ENDPOINT"] = "o.g.a"
		return load(envOf(env), home)
	}
	base := `"servers":"tls://x:4222"`
	for _, tc := range []struct {
		name, file string
		env        map[string]string
		want       bool
	}{
		{"default off", `{` + base + `}`, map[string]string{}, false},
		{"env 1", `{` + base + `}`, map[string]string{"MASH_SPOOL_FROM_START": "1"}, true},
		{"env true", `{` + base + `}`, map[string]string{"MASH_SPOOL_FROM_START": "true"}, true},
		{"file key", `{` + base + `,"spool_from_start":true}`, map[string]string{}, true},
		{"env 0 beats the file", `{` + base + `,"spool_from_start":true}`, map[string]string{"MASH_SPOOL_FROM_START": "0"}, false},
		{"env false beats the file", `{` + base + `,"spool_from_start":true}`, map[string]string{"MASH_SPOOL_FROM_START": "false"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := load(t, tc.file, tc.env)
			if err != nil || c.SpoolFromStart != tc.want {
				t.Fatalf("SpoolFromStart = %v, %v; want %v", c.SpoolFromStart, err, tc.want)
			}
		})
	}
	for _, v := range []string{"yes", "on", "TRUE", "2", " 1"} {
		if _, err := load(t, `{`+base+`}`, map[string]string{"MASH_SPOOL_FROM_START": v}); err == nil || !strings.Contains(err.Error(), "MASH_SPOOL_FROM_START") {
			t.Fatalf("%q: err %v, want a MASH_SPOOL_FROM_START error", v, err)
		}
	}
}

func TestLoadSpoolRequired(t *testing.T) {
	load := func(t *testing.T, file string, env map[string]string) (Config, error) {
		t.Helper()
		home := t.TempDir()
		writeFile(t, home, file)
		env["MASH_ENDPOINT"] = "o.g.a"
		return load(envOf(env), home)
	}
	base := `"servers":"tls://x:4222"`
	for _, tc := range []struct {
		name, file string
		env        map[string]string
		want       bool
	}{
		{"default off", `{` + base + `}`, map[string]string{}, false},
		{"env 1", `{` + base + `}`, map[string]string{"MASH_SPOOL_REQUIRED": "1"}, true},
		{"env true", `{` + base + `}`, map[string]string{"MASH_SPOOL_REQUIRED": "true"}, true},
		{"file key", `{` + base + `,"spool_required":true}`, map[string]string{}, true},
		{"env 0 beats the file", `{` + base + `,"spool_required":true}`, map[string]string{"MASH_SPOOL_REQUIRED": "0"}, false},
		{"env false beats the file", `{` + base + `,"spool_required":true}`, map[string]string{"MASH_SPOOL_REQUIRED": "false"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := load(t, tc.file, tc.env)
			if err != nil || c.SpoolRequired != tc.want {
				t.Fatalf("SpoolRequired = %v, %v; want %v", c.SpoolRequired, err, tc.want)
			}
		})
	}
	for _, v := range []string{"yes", "on", "TRUE", "2", " 1"} {
		if _, err := load(t, `{`+base+`}`, map[string]string{"MASH_SPOOL_REQUIRED": v}); err == nil || !strings.Contains(err.Error(), "MASH_SPOOL_REQUIRED") {
			t.Fatalf("%q: err %v, want a MASH_SPOOL_REQUIRED error", v, err)
		}
	}
}

func TestLoadAcceptUnverified(t *testing.T) {
	load := func(t *testing.T, file string, env map[string]string) (Config, error) {
		t.Helper()
		home := t.TempDir()
		writeFile(t, home, file)
		env["MASH_ENDPOINT"] = "o.g.a"
		return load(envOf(env), home)
	}
	base := `"servers":"tls://x:4222"`
	for _, tc := range []struct {
		name, file string
		env        map[string]string
		want       bool
	}{
		{"default off", `{` + base + `}`, map[string]string{}, false},
		{"env 1", `{` + base + `}`, map[string]string{"MASH_ACCEPT_UNVERIFIED": "1"}, true},
		{"env true", `{` + base + `}`, map[string]string{"MASH_ACCEPT_UNVERIFIED": "true"}, true},
		{"file key", `{` + base + `,"accept_unverified":true}`, map[string]string{}, true},
		{"env 0 beats the file", `{` + base + `,"accept_unverified":true}`, map[string]string{"MASH_ACCEPT_UNVERIFIED": "0"}, false},
		{"env false beats the file", `{` + base + `,"accept_unverified":true}`, map[string]string{"MASH_ACCEPT_UNVERIFIED": "false"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := load(t, tc.file, tc.env)
			if err != nil || c.AcceptUnverified != tc.want {
				t.Fatalf("AcceptUnverified = %v, %v; want %v", c.AcceptUnverified, err, tc.want)
			}
		})
	}
	for _, v := range []string{"yes", "on", "TRUE", "2", " 1"} {
		if _, err := load(t, `{`+base+`}`, map[string]string{"MASH_ACCEPT_UNVERIFIED": v}); err == nil || !strings.Contains(err.Error(), "MASH_ACCEPT_UNVERIFIED") {
			t.Fatalf("%q: err %v, want a MASH_ACCEPT_UNVERIFIED error", v, err)
		}
	}
	if _, err := load(t, `{`+base+`,"accept_unverified":"yes"}`, map[string]string{}); err == nil || !strings.Contains(err.Error(), "accept_unverified") {
		t.Fatalf("a non-boolean key: %v, want an error naming accept_unverified", err)
	}
}

func TestLimitsConfig(t *testing.T) {
	home := t.TempDir()
	c, err := load(envOf(nil), home)
	if err != nil || c.Limits != store.DefaultLimits() {
		t.Fatalf("no file: %+v %v", c.Limits, err)
	}
	writeFile(t, home, `{"limits":{"inbox_max_bytes":"64MiB","inbox_max_age":"7d","directory_max_bytes":2097152,"lease_max_bytes":"1GiB","inbox_max_consumers":4}}`)
	c, err = load(envOf(nil), home)
	want := store.Limits{InboxMaxBytes: 64 << 20, InboxMaxAge: 7 * 24 * time.Hour, DirectoryMaxBytes: 2 << 20, LeaseMaxBytes: 1 << 30, InboxMaxConsumers: 4}
	if err != nil || c.Limits != want {
		t.Fatalf("custom: %+v %v", c.Limits, err)
	}
	writeFile(t, home, `{"limits":{"inbox_max_age":"36h"}}`)
	if c, err = load(envOf(nil), home); err != nil || c.Limits.InboxMaxAge != 36*time.Hour || c.Limits.InboxMaxBytes != 16<<20 {
		t.Fatalf("partial: %+v %v", c.Limits, err)
	}
	for _, bad := range []struct{ body, key string }{
		{`{"limits":{"inbox_max_bytez":"1MiB"}}`, "inbox_max_bytez"},
		{`{"limits":{"inbox_max_bytes":"lots"}}`, "inbox_max_bytes"},
		{`{"limits":{"inbox_max_bytes":"16MB"}}`, "inbox_max_bytes"},
		{`{"limits":{"inbox_max_bytes":-5}}`, "inbox_max_bytes"},
		{`{"limits":{"inbox_max_bytes":"1MiB"}}`, "inbox_max_bytes"}, // below the minimum
		{`{"limits":{"inbox_max_age":"soon"}}`, "inbox_max_age"},
		{`{"limits":{"inbox_max_age":"0d"}}`, "inbox_max_age"},
		{`{"limits":{"inbox_max_age":"30m"}}`, "inbox_max_age"},
		{`{"limits":{"inbox_max_age":30}}`, "inbox_max_age"},
		{`{"limits":{"directory_max_bytes":"1KiB"}}`, "directory_max_bytes"},
		{`{"limits":{"lease_max_bytes":true}}`, "lease_max_bytes"},
		{`{"limits":{"inbox_max_consumers":0}}`, "inbox_max_consumers"},
		{`{"limits":{"inbox_max_consumers":"2"}}`, "inbox_max_consumers"},
		{`{"limits":[1]}`, "limits"},
	} {
		writeFile(t, home, bad.body)
		if _, err := load(envOf(nil), home); err == nil || !strings.Contains(err.Error(), bad.key) {
			t.Errorf("%s: %v", bad.body, err)
		}
		if err := ValidateFile([]byte(bad.body)); err == nil {
			t.Errorf("ValidateFile accepted %s", bad.body)
		}
	}
}

func TestParseSizeAndAge(t *testing.T) {
	for in, want := range map[string]int64{"1024": 1024, "10B": 10, "4KiB": 4096, "16MiB": 16 << 20, "2GiB": 2 << 30, " 3 MiB ": 3 << 20} {
		if got, err := ParseSize(in); err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "MiB", "-1MiB", "0", "1.5MiB", "1MB", "9999999999GiB"} {
		if _, err := ParseSize(in); err == nil {
			t.Errorf("ParseSize(%q) accepted", in)
		}
	}
	for in, want := range map[string]time.Duration{"30d": 720 * time.Hour, "1d": 24 * time.Hour, "90m": 90 * time.Minute, "720h": 720 * time.Hour} {
		if got, err := ParseAge(in); err != nil || got != want {
			t.Errorf("ParseAge(%q) = %v, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "d", "-1d", "1.5d", "0", "x"} {
		if _, err := ParseAge(in); err == nil {
			t.Errorf("ParseAge(%q) accepted", in)
		}
	}
}

func TestTLSSettingsFileAndEnv(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"servers":"tls://x:4222","ca":"/f/ca.pem","tls_cert":"/f/c.pem","tls_key":"/f/k.pem"}`)
	c, err := load(envOf(map[string]string{"MASH_ENDPOINT": "o.g.a"}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.TLSCA != "/f/ca.pem" || c.TLSCert != "/f/c.pem" || c.TLSKey != "/f/k.pem" {
		t.Fatalf("file: got %q %q %q", c.TLSCA, c.TLSCert, c.TLSKey)
	}
	c, err = load(envOf(map[string]string{"MASH_ENDPOINT": "o.g.a", "MASH_CA": "/e/ca.pem", "MASH_TLS_CERT": "/e/c.pem", "MASH_TLS_KEY": "/e/k.pem"}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.TLSCA != "/e/ca.pem" || c.TLSCert != "/e/c.pem" || c.TLSKey != "/e/k.pem" {
		t.Fatalf("env: got %q %q %q", c.TLSCA, c.TLSCert, c.TLSKey)
	}
}

func TestTLSCertAndKeyMustBeSetTogether(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"servers":"tls://x:4222","tls_cert":"/f/c.pem"}`)
	_, err := load(envOf(map[string]string{"MASH_ENDPOINT": "o.g.a"}), home)
	if err == nil || !strings.Contains(err.Error(), "tls_cert") || !strings.Contains(err.Error(), "tls_key") {
		t.Fatalf("file cert only: err = %v", err)
	}
	// The environment can complete the pair, and can break one.
	if _, err := load(envOf(map[string]string{"MASH_ENDPOINT": "o.g.a", "MASH_TLS_KEY": "/e/k.pem"}), home); err != nil {
		t.Fatalf("pair completed by env: %v", err)
	}
	writeFile(t, home, `{"servers":"tls://x:4222"}`)
	if _, err := load(envOf(map[string]string{"MASH_ENDPOINT": "o.g.a", "MASH_TLS_KEY": "/e/k.pem"}), home); err == nil {
		t.Fatal("key only: want an error")
	}
	// The files are read at connect time, not at load.
	if _, err := load(envOf(map[string]string{"MASH_ENDPOINT": "o.g.a", "MASH_CA": "/missing"}), home); err != nil {
		t.Fatalf("missing CA file at load: %v", err)
	}
}

func writeIn(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestXDGConfigHomeUsedWhenItHasConfig(t *testing.T) {
	home, xdg := t.TempDir(), t.TempDir()
	writeFile(t, home, `{"servers":"tls://old:1","endpoint":"old.g.a"}`)
	writeIn(t, filepath.Join(xdg, "mash"), `{"servers":"tls://new:1","endpoint":"new.g.a"}`)
	c, err := load(envOf(map[string]string{"XDG_CONFIG_HOME": xdg}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.URL != "tls://new:1" || c.Dir != filepath.Join(xdg, "mash") || c.Creds != filepath.Join(xdg, "mash", "new.g.a.creds") {
		t.Fatalf("got %+v", c)
	}
}

func TestRelativeXDGConfigHomeIgnored(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"servers":"tls://old:1","endpoint":"old.g.a"}`)
	t.Chdir(t.TempDir())
	writeIn(t, filepath.Join("rel", "mash"), `{"servers":"tls://rel:1","endpoint":"rel.g.a"}`)
	c, err := load(envOf(map[string]string{"XDG_CONFIG_HOME": "rel"}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.URL != "tls://old:1" || c.Dir != filepath.Join(home, ".config", "mash") {
		t.Fatalf("got %+v", c)
	}
}

func TestNoHomeNeedingAPathIsAnError(t *testing.T) {
	t.Chdir(t.TempDir())
	// The default creds path needs a directory.
	_, err := load(envOf(map[string]string{"MASH_ENDPOINT": "o.g.a", "MASH_SERVER": "u"}), "")
	if !errors.Is(err, ErrNoConfigDir) {
		t.Fatalf("err %v", err)
	}
	// So do url and owner, which would have come from the file.
	_, err = load(envOf(map[string]string{"MASH_ENDPOINT": "o.g.a"}), "")
	if !errors.Is(err, ErrNoConfigDir) || !strings.Contains(err.Error(), "set HOME or XDG_CONFIG_HOME") {
		t.Fatalf("err %v", err)
	}
	// A relative XDG_CONFIG_HOME does not stand in for the home.
	_, err = load(envOf(map[string]string{"MASH_ENDPOINT": "o.g.a", "MASH_SERVER": "u", "XDG_CONFIG_HOME": "rel"}), "")
	if !errors.Is(err, ErrNoConfigDir) {
		t.Fatalf("err %v", err)
	}
}

func TestNoHomeWithEverythingFromTheEnvironmentLoads(t *testing.T) {
	t.Chdir(t.TempDir())
	c, err := load(envOf(map[string]string{"MASH_ENDPOINT": "o.g.a", "MASH_SERVER": "tls://x:1", "MASH_CREDS": "/etc/a.creds"}), "")
	if err != nil {
		t.Fatal(err)
	}
	if c.URL != "tls://x:1" || c.Creds != "/etc/a.creds" || c.Dir != "" {
		t.Fatalf("got %+v", c)
	}
	// An absolute XDG_CONFIG_HOME is enough of a home for the default creds path.
	xdg := t.TempDir()
	c, err = load(envOf(map[string]string{"MASH_ENDPOINT": "o.g.a", "MASH_SERVER": "tls://x:1", "XDG_CONFIG_HOME": xdg}), "")
	if err != nil || c.Creds != filepath.Join(xdg, "mash", "o.g.a.creds") {
		t.Fatalf("got %+v, %v", c, err)
	}
}

func TestTildeExpandedInCreds(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"servers":"u","creds":"~/x.creds","admin_creds":"~"}`)
	c, err := load(envOf(map[string]string{"MASH_ENDPOINT": "o.g.a"}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.Creds != filepath.Join(home, "x.creds") || c.AdminCreds != home {
		t.Fatalf("got %q, %q", c.Creds, c.AdminCreds)
	}
	c, err = load(envOf(map[string]string{"MASH_ENDPOINT": "o.g.a", "MASH_CREDS": "~/e.creds", "MASH_ADMIN_CREDS": "~/adm/a.creds"}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.Creds != filepath.Join(home, "e.creds") || c.AdminCreds != filepath.Join(home, "adm", "a.creds") {
		t.Fatalf("got %q, %q", c.Creds, c.AdminCreds)
	}
}

func TestTildeUserNotExpanded(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"servers":"u","creds":"~bob/x","admin_creds":"~bob"}`)
	c, err := load(envOf(map[string]string{"MASH_ENDPOINT": "o.g.a"}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.Creds != "~bob/x" || c.AdminCreds != "~bob" {
		t.Fatalf("got %q, %q", c.Creds, c.AdminCreds)
	}
}

func writeXDG(t *testing.T, xdg, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(xdg, "mash"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xdg, "mash", "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMashDirVariables(t *testing.T) {
	home, xdg := t.TempDir(), t.TempDir()
	writeXDG(t, xdg, `{"servers":"u-xdg"}`)
	homeDir := filepath.Join(home, ".config", "mash")
	writeFile(t, home, `{"servers":"u-home"}`)
	mdir := t.TempDir()
	cases := []struct {
		name       string
		env        map[string]string
		dir, state string
		err        string
	}{
		{"defaults", nil, homeDir, filepath.Join(home, ".local", "state", "mash"), ""},
		{"xdg state", map[string]string{"XDG_STATE_HOME": "/x/s"}, homeDir, "/x/s/mash", ""},
		{"xdg state relative ignored", map[string]string{"XDG_STATE_HOME": "rel"}, homeDir, filepath.Join(home, ".local", "state", "mash"), ""},
		{"config dir beats xdg and home", map[string]string{"MASH_CONFIG_DIR": mdir, "XDG_CONFIG_HOME": xdg}, mdir, filepath.Join(home, ".local", "state", "mash"), ""},
		{"state dir beats xdg", map[string]string{"MASH_STATE_DIR": "/m/s", "XDG_STATE_HOME": "/x/s"}, homeDir, "/m/s", ""},
		{"relative config dir", map[string]string{"MASH_CONFIG_DIR": "rel/c"}, "", "", "MASH_CONFIG_DIR"},
		{"relative state dir", map[string]string{"MASH_STATE_DIR": "rel/s"}, "", "", "MASH_STATE_DIR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := tc.env
			if env == nil {
				env = map[string]string{}
			}
			if _, ok := env["XDG_CONFIG_HOME"]; !ok {
				env["XDG_CONFIG_HOME"] = ""
			}
			c, err := load(envOf(env), home)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err %v, want one naming %s", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.Dir != tc.dir || c.StateDir != tc.state {
				t.Fatalf("dir %q state %q, want %q %q", c.Dir, c.StateDir, tc.dir, tc.state)
			}
		})
	}
}

// MASH_CONFIG_DIR is used for config.json, policy.md and default creds with no fallback to XDG or the home directory.
func TestMashConfigDirHasNoFallback(t *testing.T) {
	home, xdg, mdir := t.TempDir(), t.TempDir(), t.TempDir()
	writeXDG(t, xdg, `{"servers":"u-xdg"}`)
	homeDir := filepath.Join(home, ".config", "mash")
	writeFile(t, home, `{"servers":"u-home"}`)
	if err := os.WriteFile(filepath.Join(homeDir, "policy.md"), []byte("p"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := envOf(map[string]string{"MASH_CONFIG_DIR": mdir, "XDG_CONFIG_HOME": xdg, "MASH_ENDPOINT": "o.g.a"})
	c, err := load(env, home)
	if !errors.Is(err, ErrNoURL) { // nothing in MASH_CONFIG_DIR yet: neither the XDG nor the home url is read
		t.Fatalf("err %v, want ErrNoURL", err)
	}
	if got, want := c.Find("policy.md"), filepath.Join(mdir, "policy.md"); got != want {
		t.Fatalf("Find %q, want %q", got, want)
	}
	if c.URL != "" {
		t.Fatalf("read another directory: url %q", c.URL)
	}
	if err := os.WriteFile(filepath.Join(mdir, "config.json"), []byte(`{"servers":"tls://x:4222"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err = load(env, home); err != nil || c.URL != "tls://x:4222" || c.Creds != filepath.Join(mdir, "o.g.a.creds") {
		t.Fatalf("url %q creds %q err %v", c.URL, c.Creds, err)
	}
}

func TestLoadSkipEncryption(t *testing.T) {
	const name = "MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION"
	load := func(file string, env map[string]string) (Config, error) {
		home := t.TempDir()
		writeFile(t, home, file)
		env["MASH_ENDPOINT"] = "o.g.a"
		return load(envOf(env), home)
	}
	base := `"servers":"tls://127.0.0.1:4222"`
	for _, tc := range []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"default off", map[string]string{}, false},
		{"empty is off", map[string]string{name: ""}, false},
		{"0 is off", map[string]string{name: "0"}, false},
		{"1 is on", map[string]string{name: "1"}, true},
	} {
		c, err := load(`{`+base+`}`, tc.env)
		if err != nil || c.SkipEncryption != tc.want {
			t.Fatalf("%s: %v, %v; want %v", tc.name, c.SkipEncryption, err, tc.want)
		}
	}
	// Only 1 turns it on: true, yes, on and anything else are errors naming the variable.
	for _, v := range []string{"yes", "on", "TRUE", "true", "false", "2"} {
		_, err := load(`{`+base+`}`, map[string]string{name: v})
		var se *SkipEncryptionError
		if err == nil || !errors.As(err, &se) || !strings.Contains(err.Error(), name) {
			t.Fatalf("a bad value %q: %v", v, err)
		}
	}
	// The old names are refused, not aliased: the error names the new variable.
	_, err := load(`{`+base+`}`, map[string]string{"MASH_INSECURE_PLAINTEXT": "1"})
	var se *SkipEncryptionError
	if err == nil || !errors.As(err, &se) || !strings.Contains(err.Error(), "MASH_INSECURE_PLAINTEXT is no longer read; set "+name+"=1 instead") {
		t.Fatalf("the old variable: %v", err)
	}
	for _, v := range []string{`true`, `false`, `null`} {
		_, err := load(`{`+base+`,"insecure_plaintext":`+v+`}`, map[string]string{})
		if err == nil || !errors.As(err, &se) || !strings.Contains(err.Error(), "insecure_plaintext in config.json is no longer read; set "+name+"=1 instead") {
			t.Fatalf("the old key %s: %v", v, err)
		}
		if err := ValidateFile([]byte(`{` + base + `,"insecure_plaintext":` + v + `}`)); err == nil {
			t.Fatalf("ValidateFile accepted the old key %s", v)
		}
	}
}

func TestOldTLSKeysAreUnknownKeys(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"servers":"tls://127.0.0.1:4222","insecure_loopback":true,"require_tls":true}`)
	c, err := load(envOf(map[string]string{"MASH_ENDPOINT": "o.g.a", "MASH_INSECURE_LOOPBACK": "1", "MASH_REQUIRE_TLS": "1"}), home)
	if err != nil || c.SkipEncryption {
		t.Fatalf("old names must be ignored: %v, %v", c.SkipEncryption, err)
	}
	if _, ok := c.Source["insecure_loopback"]; ok {
		t.Fatal("old key has a source")
	}
}

func TestSourcesRecordWhereEachSettingCameFrom(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"servers":"tls://file:4222","endpoint":"fileowner.g.a","admin_creds":"~/a.creds","ca":"/file/ca.pem"}`)
	c, err := load(envOf(map[string]string{"MASH_SERVER": "tls://env:4222", "MASH_TLS_CERT": "/c.pem", "MASH_TLS_KEY": "/k.pem"}), home)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"servers": "MASH_SERVER", "endpoint": "config.json", "admin_creds": "config.json", "ca": "config.json",
		"tls_cert": "MASH_TLS_CERT", "tls_key": "MASH_TLS_KEY"}
	for k, v := range want {
		if c.Source[k] != v {
			t.Errorf("Source[%s] = %q, want %q", k, c.Source[k], v)
		}
	}
	if c.File != filepath.Join(home, ".config", "mash", "config.json") {
		t.Errorf("File %q", c.File)
	}
	// The file's own values, to compare with the effective ones: the env overrode url, so they differ.
	if c.URL != "tls://env:4222" || c.FileValues["servers"] != "tls://file:4222" {
		t.Errorf("servers %q, file said %q", c.URL, c.FileValues["servers"])
	}
	if c.FileValues["admin_creds"] != filepath.Join(home, "a.creds") || c.AdminCreds != c.FileValues["admin_creds"] {
		t.Errorf("admin creds %q, file said %q", c.AdminCreds, c.FileValues["admin_creds"])
	}
	if _, ok := c.FileValues["tls_cert"]; ok {
		t.Errorf("FileValues has a key the file did not set: %v", c.FileValues)
	}
}

func TestSourcesWithoutFileAreEnvOrUnset(t *testing.T) {
	home := t.TempDir()
	c, err := load(envOf(map[string]string{"MASH_SERVER": "tls://e:1", "MASH_ENDPOINT": "o.dev.x", "MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION": "1"}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.File != "" || len(c.FileValues) != 0 {
		t.Errorf("File %q FileValues %v", c.File, c.FileValues)
	}
	for k, v := range map[string]string{"servers": "MASH_SERVER", "endpoint": "MASH_ENDPOINT", "admin_creds": "unset"} {
		if c.Source[k] != v {
			t.Errorf("Source[%s] = %q, want %q", k, c.Source[k], v)
		}
	}
}

func TestSourcesAgreeingEnvStillNamesEnv(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"servers":"tls://same:4222"}`)
	c, err := load(envOf(map[string]string{"MASH_SERVER": "tls://same:4222"}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.Source["servers"] != "MASH_SERVER" || c.FileValues["servers"] != c.URL {
		t.Errorf("%v %v", c.Source, c.FileValues)
	}
}

// load is Load over a plain env func: a variable is present iff its value is not empty.
func load(env func(string) string, home string) (Config, error) {
	return Load(LookupFromFunc(env), home)
}

func TestConfigDirChoice(t *testing.T) {
	mdir := t.TempDir()
	cases := []struct {
		name     string
		xdg      string // "" unset, "abs" a fresh absolute dir, "rel" a relative one
		noHome   bool
		homeDir  bool   // $HOME/.config/mash exists
		xdgCfg   bool   // $XDG_CONFIG_HOME/mash/config.json exists
		explicit bool   // MASH_CONFIG_DIR set
		want     string // "xdg", "home", "explicit" or ""
	}{
		{name: "fresh machine, xdg set", xdg: "abs", want: "xdg"},
		{name: "home dir exists, xdg set: xdg only", xdg: "abs", homeDir: true, want: "xdg"},
		{name: "xdg config.json exists", xdg: "abs", homeDir: true, xdgCfg: true, want: "xdg"},
		{name: "explicit wins on a fresh machine", xdg: "abs", explicit: true, want: "explicit"},
		{name: "explicit wins over both", xdg: "abs", homeDir: true, xdgCfg: true, explicit: true, want: "explicit"},
		{name: "relative xdg ignored, fresh machine", xdg: "rel", want: "home"},
		{name: "relative xdg ignored, home dir exists", xdg: "rel", homeDir: true, want: "home"},
		{name: "no xdg, fresh machine", want: "home"},
		{name: "no home, xdg set", xdg: "abs", noHome: true, want: "xdg"},
		{name: "no home, relative xdg", xdg: "rel", noHome: true, want: ""},
		{name: "no home, no xdg", noHome: true, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, xdgBase := t.TempDir(), t.TempDir()
			homeDir, xdgDir := filepath.Join(home, ".config", "mash"), filepath.Join(xdgBase, "mash")
			if tc.homeDir {
				if err := os.MkdirAll(homeDir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if tc.xdgCfg {
				writeIn(t, xdgDir, `{}`)
			}
			env := map[string]string{}
			switch tc.xdg {
			case "abs":
				env["XDG_CONFIG_HOME"] = xdgBase
			case "rel":
				env["XDG_CONFIG_HOME"] = "rel"
			}
			if tc.explicit {
				env["MASH_CONFIG_DIR"] = mdir
			}
			h := home
			if tc.noHome {
				h = ""
			}
			want := map[string]string{"xdg": xdgDir, "home": homeDir, "explicit": mdir, "": ""}[tc.want]
			if got := ConfigDir(envOf(env), h); got != want {
				t.Fatalf("ConfigDir %q, want %q (%s)", got, want, tc.want)
			}
		})
	}
}

func TestWakeSettings(t *testing.T) {
	base := map[string]string{"MASH_ENDPOINT": "o.g.a"}
	loadWith := func(file string, env map[string]string) (Config, error) {
		home := t.TempDir()
		writeFile(t, home, `{"servers":"tls://x:4222"`+file+`}`)
		e := map[string]string{}
		for k, v := range base {
			e[k] = v
		}
		for k, v := range env {
			e[k] = v
		}
		return load(envOf(e), home)
	}
	c, err := loadWith("", nil)
	if err != nil || c.WakeCount != 15 || c.WakeAge != time.Hour || c.WakeAgeText != "1h" {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	for _, tc := range []struct {
		name, file string
		env        map[string]string
		count      int
		age        time.Duration
		text       string
	}{
		{"file", `,"wake_count":3,"wake_age":"2d"`, nil, 3, 48 * time.Hour, "2d"},
		{"env beats file", `,"wake_count":3,"wake_age":"2d"`, map[string]string{"MASH_WAKE_COUNT": "40", "MASH_WAKE_AGE": "30m"}, 40, 30 * time.Minute, "30m"},
		{"off in env", "", map[string]string{"MASH_WAKE_COUNT": "0", "MASH_WAKE_AGE": "0"}, 0, 0, "0"},
		{"off in file", `,"wake_count":0,"wake_age":0`, nil, 0, 0, "0"},
		{"large", "", map[string]string{"MASH_WAKE_COUNT": "5000", "MASH_WAKE_AGE": "90d"}, 5000, 90 * 24 * time.Hour, "90d"},
		{"go duration", "", map[string]string{"MASH_WAKE_AGE": "90s"}, 15, 90 * time.Second, "90s"},
		{"one minute", "", map[string]string{"MASH_WAKE_AGE": "1m"}, 15, time.Minute, "1m"},
		{"bad file value under a good env", `,"wake_count":-1,"wake_age":"5s"`, map[string]string{"MASH_WAKE_COUNT": "2", "MASH_WAKE_AGE": "1h"}, 2, time.Hour, "1h"},
	} {
		c, err := loadWith(tc.file, tc.env)
		if err != nil || c.WakeCount != tc.count || c.WakeAge != tc.age || c.WakeAgeText != tc.text {
			t.Errorf("%s: %+v %v", tc.name, c, err)
		}
	}
	for _, tc := range []struct {
		name, file string
		env        map[string]string
		want       string
	}{
		{"count negative", "", map[string]string{"MASH_WAKE_COUNT": "-1"}, `MASH_WAKE_COUNT "-1" is not a whole number of messages`},
		{"count text", "", map[string]string{"MASH_WAKE_COUNT": "many"}, `MASH_WAKE_COUNT "many"`},
		{"count file string", `,"wake_count":"15"`, nil, `config.json "wake_count" "15" is not a whole number`},
		{"count file fraction", `,"wake_count":1.5`, nil, `config.json "wake_count" "1.5"`},
		{"age text", "", map[string]string{"MASH_WAKE_AGE": "bogus"}, `MASH_WAKE_AGE "bogus" is not a duration`},
		{"age negative", "", map[string]string{"MASH_WAKE_AGE": "-5m"}, `MASH_WAKE_AGE "-5m"`},
		{"age fraction", "", map[string]string{"MASH_WAKE_AGE": "1.5d"}, `MASH_WAKE_AGE "1.5d"`},
		{"age bare number", "", map[string]string{"MASH_WAKE_AGE": "60"}, `MASH_WAKE_AGE "60"`},
		{"age file number", `,"wake_age":5`, nil, `config.json "wake_age" 5`},
		{"age file bad", `,"wake_age":"0m"`, nil, `config.json "wake_age" "0m"`},
	} {
		c, lerr := loadWith(tc.file, tc.env)
		if lerr != nil {
			t.Errorf("%s: a bad wake setting must not fail the load: %v", tc.name, lerr)
		}
		err := c.WakeErr
		if c.WakeCount != 15 || c.WakeAge != time.Hour {
			t.Errorf("%s: a bad setting leaves the defaults, got %d %v", tc.name, c.WakeCount, c.WakeAge)
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want error containing %q, got %v", tc.name, tc.want, err)
		}
	}
}

// A bad wake_count does not stop a valid wake_age from being read: the bad one is reported, the good one kept.
func TestBadWakeCountKeepsAValidWakeAge(t *testing.T) {
	c, err := loadWith(t, idFile+`,"wake_count":"x","wake_age":"30m"}`, map[string]string{"MASH_ENDPOINT": "o.g.a"})
	if err != nil {
		t.Fatal(err)
	}
	if c.WakeErr == nil || !strings.Contains(c.WakeErr.Error(), `config.json "wake_count"`) {
		t.Errorf("WakeErr %v", c.WakeErr)
	}
	if c.WakeAge != 30*time.Minute || c.WakeAgeText != "30m" || c.WakeCount != 15 {
		t.Errorf("age %v %q count %d", c.WakeAge, c.WakeAgeText, c.WakeCount)
	}
}
