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
	writeFile(t, home, `{"url":"tls://x:4222","owner":"o"}`)
	c, err := load(envOf(map[string]string{"MASH_NAME": "a"}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.URL != "tls://x:4222" || c.Owner != "o" || c.Name != "a" {
		t.Fatalf("got %+v", c)
	}
	if c.Creds != filepath.Join(home, ".config", "mash", "a.creds") {
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
	writeFile(t, home, `{"url":"tls://file:4222","owner":"o","settle_ms":5}`)
	c, err := load(envOf(map[string]string{
		"MASH_NAME": "a", "MASH_URL": "tls://env:4222", "MASH_OWNER": "p",
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
	writeFile(t, home, `{"url":"tls://f:4222","owner":"o","settle_ms":5,"batch_msgs":2,"batch_bytes":64}`)
	c, err := load(envOf(map[string]string{"MASH_NAME": "a"}), home)
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
	_, err := load(envOf(map[string]string{"MASH_NAME": "a", "MASH_OWNER": "o"}), home)
	if err == nil || !strings.Contains(err.Error(), "url") {
		t.Fatalf("want url error, got %v", err)
	}
	_, err = load(envOf(map[string]string{"MASH_NAME": "a", "MASH_URL": "tls://x:1"}), home)
	if err == nil || !strings.Contains(err.Error(), "owner") {
		t.Fatalf("want owner error, got %v", err)
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
	if _, err := load(envOf(map[string]string{"MASH_NAME": "Bad", "MASH_URL": "u", "MASH_OWNER": "o"}), home); err == nil {
		t.Fatal("bad MASH_NAME must be rejected")
	}
	if _, err := load(envOf(map[string]string{"MASH_NAME": "a", "MASH_URL": "u", "MASH_OWNER": "O"}), home); err == nil {
		t.Fatal("bad owner must be rejected")
	}
}

func TestBadNumbersRejected(t *testing.T) {
	home := t.TempDir()
	_, err := load(envOf(map[string]string{"MASH_NAME": "a", "MASH_URL": "u", "MASH_OWNER": "o", "MASH_SETTLE_MS": "x"}), home)
	if err == nil {
		t.Fatal("non-numeric MASH_SETTLE_MS must be rejected")
	}
}

func TestLaunchTokenFromTheEnvironment(t *testing.T) {
	base := map[string]string{"MASH_URL": "nats://x", "MASH_OWNER": "o", "MASH_NAME": "n"}
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
		writeFile(t, home, `{"url":"tls://f:4222","owner":"o"`+file+`}`)
		env["MASH_NAME"] = "a"
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
		writeFile(t, home, `{"url":"tls://f:4222","owner":"o"`+file+`}`)
		env["MASH_NAME"] = "a"
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
		writeFile(t, home, `{"url":"tls://f:4222","owner":"o"`+file+`}`)
		env["MASH_NAME"] = "a"
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
		writeFile(t, home, `{"url":"tls://f:4222","owner":"o"`+file+`}`)
		env["MASH_NAME"] = "a"
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
		env["MASH_NAME"] = "a"
		return load(envOf(env), home)
	}
	base := `"url":"tls://x:4222","owner":"o"`
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
	writeFile(t, home, `{"url":"tls://x:4222","owner":"o","tls_ca":"/f/ca.pem","tls_cert":"/f/c.pem","tls_key":"/f/k.pem"}`)
	c, err := load(envOf(map[string]string{"MASH_NAME": "a"}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.TLSCA != "/f/ca.pem" || c.TLSCert != "/f/c.pem" || c.TLSKey != "/f/k.pem" {
		t.Fatalf("file: got %q %q %q", c.TLSCA, c.TLSCert, c.TLSKey)
	}
	c, err = load(envOf(map[string]string{"MASH_NAME": "a", "MASH_TLS_CA": "/e/ca.pem", "MASH_TLS_CERT": "/e/c.pem", "MASH_TLS_KEY": "/e/k.pem"}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.TLSCA != "/e/ca.pem" || c.TLSCert != "/e/c.pem" || c.TLSKey != "/e/k.pem" {
		t.Fatalf("env: got %q %q %q", c.TLSCA, c.TLSCert, c.TLSKey)
	}
}

func TestTLSCertAndKeyMustBeSetTogether(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"url":"tls://x:4222","owner":"o","tls_cert":"/f/c.pem"}`)
	_, err := load(envOf(map[string]string{"MASH_NAME": "a"}), home)
	if err == nil || !strings.Contains(err.Error(), "tls_cert") || !strings.Contains(err.Error(), "tls_key") {
		t.Fatalf("file cert only: err = %v", err)
	}
	// The environment can complete the pair, and can break one.
	if _, err := load(envOf(map[string]string{"MASH_NAME": "a", "MASH_TLS_KEY": "/e/k.pem"}), home); err != nil {
		t.Fatalf("pair completed by env: %v", err)
	}
	writeFile(t, home, `{"url":"tls://x:4222","owner":"o"}`)
	if _, err := load(envOf(map[string]string{"MASH_NAME": "a", "MASH_TLS_KEY": "/e/k.pem"}), home); err == nil {
		t.Fatal("key only: want an error")
	}
	// The files are read at connect time, not at load.
	if _, err := load(envOf(map[string]string{"MASH_NAME": "a", "MASH_TLS_CA": "/missing"}), home); err != nil {
		t.Fatalf("missing CA file at load: %v", err)
	}
}

func TestPlainTextWarning(t *testing.T) {
	for url, warn := range map[string]bool{
		"nats://10.0.0.5":                       true,
		"nats://example.com:4222":               true,
		"ws://example.com":                      true,
		"nats://user:pw@10.0.0.5:4222":          true,
		"nats://localhost":                      false,
		"nats://127.0.0.1:4222":                 false,
		"nats://127.1.2.3":                      false,
		"nats://[::1]:4222":                     false,
		"nats://LOCALHOST:4222":                 false,
		"tls://example.com:4222":                false,
		"wss://example.com":                     false,
		"":                                      false,
		"tls://a:4222,nats://10.0.0.5:4222":     true,
		"nats://127.0.0.1:4222, nats://b:4222":  true,
		"nats://127.0.0.1:4222,nats://[::1]:42": false,
	} {
		if got := PlainTextWarning(url) != ""; got != warn {
			t.Errorf("PlainTextWarning(%q) warns = %v, want %v", url, got, warn)
		}
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
	writeFile(t, home, `{"url":"tls://old:1","owner":"old"}`)
	writeIn(t, filepath.Join(xdg, "mash"), `{"url":"tls://new:1","owner":"new"}`)
	c, err := load(envOf(map[string]string{"MASH_NAME": "a", "XDG_CONFIG_HOME": xdg}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.URL != "tls://new:1" || c.Dir != filepath.Join(xdg, "mash") || c.Creds != filepath.Join(xdg, "mash", "a.creds") {
		t.Fatalf("got %+v", c)
	}
}

func TestOldConfigStillReadWhenXDGHasNone(t *testing.T) {
	home, xdg := t.TempDir(), t.TempDir()
	writeFile(t, home, `{"url":"tls://old:1","owner":"old"}`)
	c, err := load(envOf(map[string]string{"MASH_NAME": "a", "XDG_CONFIG_HOME": xdg}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.URL != "tls://old:1" || c.Dir != filepath.Join(home, ".config", "mash") {
		t.Fatalf("got %+v", c)
	}
}

func TestUnreadableXDGDirFailsLoudlyNotLegacy(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	home, xdg := t.TempDir(), t.TempDir()
	writeFile(t, home, `{"url":"tls://old:1","owner":"old"}`)
	mashDir := filepath.Join(xdg, "mash")
	writeIn(t, mashDir, `{"url":"tls://new:1","owner":"new"}`)
	if err := os.Chmod(mashDir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(mashDir, 0o700) })
	if _, err := os.Stat(filepath.Join(mashDir, "config.json")); err == nil {
		t.Skip("the OS ignores the directory mode")
	}
	c, err := load(envOf(map[string]string{"MASH_NAME": "a", "XDG_CONFIG_HOME": xdg}), home)
	if err == nil {
		t.Fatalf("Load succeeded with %+v, want an error", c)
	}
	if !strings.Contains(err.Error(), mashDir) {
		t.Fatalf("error %q does not mention %s", err, mashDir)
	}
	if c.URL == "tls://old:1" {
		t.Fatalf("used the legacy config: %+v", c)
	}
}

func TestMissingXDGConfigFallsBackToLegacyEvenWithXDGDir(t *testing.T) {
	home, xdg := t.TempDir(), t.TempDir()
	writeFile(t, home, `{"url":"tls://old:1","owner":"old"}`)
	if err := os.MkdirAll(filepath.Join(xdg, "mash"), 0o700); err != nil {
		t.Fatal(err)
	}
	c, err := load(envOf(map[string]string{"MASH_NAME": "a", "XDG_CONFIG_HOME": xdg}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.URL != "tls://old:1" {
		t.Fatalf("got %+v", c)
	}
}

func TestRelativeXDGConfigHomeIgnored(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"url":"tls://old:1","owner":"old"}`)
	t.Chdir(t.TempDir())
	writeIn(t, filepath.Join("rel", "mash"), `{"url":"tls://rel:1","owner":"rel"}`)
	c, err := load(envOf(map[string]string{"MASH_NAME": "a", "XDG_CONFIG_HOME": "rel"}), home)
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
	_, err := load(envOf(map[string]string{"MASH_NAME": "a", "MASH_URL": "u", "MASH_OWNER": "o"}), "")
	if !errors.Is(err, ErrNoConfigDir) {
		t.Fatalf("err %v", err)
	}
	// So do url and owner, which would have come from the file.
	_, err = load(envOf(map[string]string{"MASH_NAME": "a"}), "")
	if !errors.Is(err, ErrNoConfigDir) || !strings.Contains(err.Error(), "set HOME or XDG_CONFIG_HOME") {
		t.Fatalf("err %v", err)
	}
	// A relative XDG_CONFIG_HOME does not stand in for the home.
	_, err = load(envOf(map[string]string{"MASH_NAME": "a", "MASH_URL": "u", "MASH_OWNER": "o", "XDG_CONFIG_HOME": "rel"}), "")
	if !errors.Is(err, ErrNoConfigDir) {
		t.Fatalf("err %v", err)
	}
}

func TestNoHomeWithEverythingFromTheEnvironmentLoads(t *testing.T) {
	t.Chdir(t.TempDir())
	c, err := load(envOf(map[string]string{"MASH_NAME": "a", "MASH_URL": "tls://x:1", "MASH_OWNER": "o", "MASH_CREDS": "/etc/a.creds"}), "")
	if err != nil {
		t.Fatal(err)
	}
	if c.URL != "tls://x:1" || c.Creds != "/etc/a.creds" || c.Dir != "" {
		t.Fatalf("got %+v", c)
	}
	// An absolute XDG_CONFIG_HOME is enough of a home for the default creds path.
	xdg := t.TempDir()
	c, err = load(envOf(map[string]string{"MASH_NAME": "a", "MASH_URL": "tls://x:1", "MASH_OWNER": "o", "XDG_CONFIG_HOME": xdg}), "")
	if err != nil || c.Creds != filepath.Join(xdg, "mash", "a.creds") {
		t.Fatalf("got %+v, %v", c, err)
	}
}

func TestTildeExpandedInCreds(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"url":"u","owner":"o","creds":"~/x.creds","admin_creds":"~"}`)
	c, err := load(envOf(map[string]string{"MASH_NAME": "a"}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.Creds != filepath.Join(home, "x.creds") || c.AdminCreds != home {
		t.Fatalf("got %q, %q", c.Creds, c.AdminCreds)
	}
	c, err = load(envOf(map[string]string{"MASH_NAME": "a", "MASH_CREDS": "~/e.creds", "MASH_ADMIN_CREDS": "~/adm/a.creds"}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.Creds != filepath.Join(home, "e.creds") || c.AdminCreds != filepath.Join(home, "adm", "a.creds") {
		t.Fatalf("got %q, %q", c.Creds, c.AdminCreds)
	}
}

func TestTildeUserNotExpanded(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"url":"u","owner":"o","creds":"~bob/x","admin_creds":"~bob"}`)
	c, err := load(envOf(map[string]string{"MASH_NAME": "a"}), home)
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

func TestFindFileFallsBackToLegacy(t *testing.T) {
	home, xdg := t.TempDir(), t.TempDir()
	env := envOf(map[string]string{"XDG_CONFIG_HOME": xdg})
	xdgDir, legacy := filepath.Join(xdg, "mash"), filepath.Join(home, ".config", "mash")
	writeXDG(t, xdg, `{}`) // XDG config.json exists, so ConfigDir is the XDG directory
	if got := ConfigDir(env, home); got != xdgDir {
		t.Fatalf("ConfigDir %q", got)
	}
	if got, want := FindFile(env, home, "policy.md"), filepath.Join(xdgDir, "policy.md"); got != want {
		t.Fatalf("neither exists: %q, want %q", got, want)
	}
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	lp := filepath.Join(legacy, "policy.md")
	if err := os.WriteFile(lp, []byte("L"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := FindFile(env, home, "policy.md"); got != lp {
		t.Fatalf("legacy only: %q, want %q", got, lp)
	}
	xp := filepath.Join(xdgDir, "policy.md")
	if err := os.WriteFile(xp, []byte("X"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := FindFile(env, home, "policy.md"); got != xp {
		t.Fatalf("both: %q, want %q", got, xp)
	}
}

func TestLoadDefaultCredsFallBackToLegacy(t *testing.T) {
	home, xdg := t.TempDir(), t.TempDir()
	writeXDG(t, xdg, `{"url":"tls://x:4222","owner":"o"}`)
	legacy := filepath.Join(home, ".config", "mash")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	lp := filepath.Join(legacy, "a.creds")
	if err := os.WriteFile(lp, []byte("c"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := load(envOf(map[string]string{"MASH_NAME": "a", "XDG_CONFIG_HOME": xdg}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.Creds != lp {
		t.Fatalf("creds %q, want %q", c.Creds, lp)
	}
}

func TestFindDoesNotFallBackWhenXDGFileIsPresentButUnusable(t *testing.T) {
	home, xdg := t.TempDir(), t.TempDir()
	env := envOf(map[string]string{"XDG_CONFIG_HOME": xdg})
	xdgDir, legacy := filepath.Join(xdg, "mash"), filepath.Join(home, ".config", "mash")
	writeXDG(t, xdg, `{}`)
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "policy.md"), []byte("L"), 0o644); err != nil {
		t.Fatal(err)
	}
	xp := filepath.Join(xdgDir, "policy.md")
	check := func(label string) {
		t.Helper()
		if got := FindFile(env, home, "policy.md"); got != xp {
			t.Fatalf("%s: FindFile %q, want %q", label, got, xp)
		}
		c := Config{Dir: xdgDir, Legacy: legacy}
		if got := c.Find("policy.md"); got != xp {
			t.Fatalf("%s: Find %q, want %q", label, got, xp)
		}
	}
	if err := os.Symlink(filepath.Join(xdg, "nowhere"), xp); err != nil {
		t.Skip("symlinks unavailable")
	}
	check("dangling symlink")
	if err := os.Remove(xp); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		return
	}
	locked := filepath.Join(xdg, "locked")
	if err := os.MkdirAll(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(locked, "policy.md")
	if err := os.WriteFile(target, []byte("X"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	if _, err := os.Stat(target); err == nil {
		t.Skip("the OS ignores the directory mode")
	}
	if err := os.Symlink(target, xp); err != nil {
		t.Fatal(err)
	}
	check("symlink to inaccessible target")
}

func TestMashDirVariables(t *testing.T) {
	home, xdg := t.TempDir(), t.TempDir()
	writeXDG(t, xdg, `{"url":"u-xdg","owner":"o"}`) // an XDG config exists, and so does a legacy one
	legacy := filepath.Join(home, ".config", "mash")
	writeFile(t, home, `{"url":"u-legacy","owner":"o"}`)
	mdir := t.TempDir()
	cases := []struct {
		name       string
		env        map[string]string
		dir, state string
		legacy     bool
		err        string
	}{
		{"defaults", nil, legacy, filepath.Join(home, ".local", "state", "mash"), true, ""},
		{"xdg state", map[string]string{"XDG_STATE_HOME": "/x/s"}, legacy, "/x/s/mash", true, ""},
		{"xdg state relative ignored", map[string]string{"XDG_STATE_HOME": "rel"}, legacy, filepath.Join(home, ".local", "state", "mash"), true, ""},
		{"config dir beats xdg and legacy", map[string]string{"MASH_CONFIG_DIR": mdir, "XDG_CONFIG_HOME": xdg}, mdir, filepath.Join(home, ".local", "state", "mash"), false, ""},
		{"state dir beats xdg", map[string]string{"MASH_STATE_DIR": "/m/s", "XDG_STATE_HOME": "/x/s"}, legacy, "/m/s", true, ""},
		{"relative config dir", map[string]string{"MASH_CONFIG_DIR": "rel/c"}, "", "", false, "MASH_CONFIG_DIR"},
		{"relative state dir", map[string]string{"MASH_STATE_DIR": "rel/s"}, "", "", false, "MASH_STATE_DIR"},
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
			if c.Dir != tc.dir || c.StateDir != tc.state || (c.Legacy != "") != tc.legacy {
				t.Fatalf("dir %q state %q legacy %q, want %q %q (legacy %v)", c.Dir, c.StateDir, c.Legacy, tc.dir, tc.state, tc.legacy)
			}
		})
	}
}

// MASH_CONFIG_DIR is used for config.json, policy.md and default creds with no fallback to XDG or legacy.
func TestMashConfigDirHasNoFallback(t *testing.T) {
	home, xdg, mdir := t.TempDir(), t.TempDir(), t.TempDir()
	writeXDG(t, xdg, `{"url":"u-xdg","owner":"o"}`)
	legacy := filepath.Join(home, ".config", "mash")
	writeFile(t, home, `{"url":"u-legacy","owner":"o"}`)
	if err := os.WriteFile(filepath.Join(legacy, "policy.md"), []byte("p"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := envOf(map[string]string{"MASH_CONFIG_DIR": mdir, "XDG_CONFIG_HOME": xdg, "MASH_NAME": "a"})
	if got, want := FindFile(env, home, "policy.md"), filepath.Join(mdir, "policy.md"); got != want {
		t.Fatalf("FindFile %q, want %q", got, want)
	}
	c, err := load(env, home)
	if !errors.Is(err, ErrNoURL) { // nothing in MASH_CONFIG_DIR yet: neither the XDG nor the legacy url is read
		t.Fatalf("err %v, want ErrNoURL", err)
	}
	if got, want := c.Find("policy.md"), filepath.Join(mdir, "policy.md"); got != want {
		t.Fatalf("Find %q, want %q", got, want)
	}
	if c.URL != "" {
		t.Fatalf("read another directory: url %q", c.URL)
	}
	if err := os.WriteFile(filepath.Join(mdir, "config.json"), []byte(`{"url":"tls://x:4222","owner":"o"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err = load(env, home); err != nil || c.URL != "tls://x:4222" || c.Creds != filepath.Join(mdir, "a.creds") {
		t.Fatalf("url %q creds %q err %v", c.URL, c.Creds, err)
	}
}

func TestLoadInsecureLoopback(t *testing.T) {
	load := func(file string, env map[string]string) (Config, error) {
		home := t.TempDir()
		writeFile(t, home, file)
		env["MASH_NAME"] = "a"
		return load(envOf(env), home)
	}
	base := `"url":"nats://127.0.0.1:4222","owner":"o"`
	for _, tc := range []struct {
		name, file string
		env        map[string]string
		want       bool
	}{
		{"default off", `{` + base + `}`, map[string]string{}, false},
		{"file key", `{` + base + `,"insecure_loopback":true}`, map[string]string{}, true},
		{"env 1", `{` + base + `}`, map[string]string{"MASH_INSECURE_LOOPBACK": "1"}, true},
		{"env 0 beats the file", `{` + base + `,"insecure_loopback":true}`, map[string]string{"MASH_INSECURE_LOOPBACK": "0"}, false},
	} {
		if c, err := load(tc.file, tc.env); err != nil || c.InsecureLoopback != tc.want {
			t.Fatalf("%s: %v, %v; want %v", tc.name, c.InsecureLoopback, err, tc.want)
		}
	}
	if _, err := load(`{`+base+`}`, map[string]string{"MASH_INSECURE_LOOPBACK": "yes"}); err == nil || !strings.Contains(err.Error(), "MASH_INSECURE_LOOPBACK") {
		t.Fatalf("a bad value: %v", err)
	}
}

func TestSourcesRecordWhereEachSettingCameFrom(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"url":"tls://file:4222","owner":"fileowner","admin_creds":"~/a.creds","tls_ca":"/file/ca.pem"}`)
	c, err := load(envOf(map[string]string{"MASH_URL": "tls://env:4222", "MASH_TLS_CERT": "/c.pem", "MASH_TLS_KEY": "/k.pem"}), home)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"url": "MASH_URL", "owner": "config.json", "admin_creds": "config.json", "tls_ca": "config.json",
		"tls_cert": "MASH_TLS_CERT", "tls_key": "MASH_TLS_KEY", "insecure_loopback": "default"}
	for k, v := range want {
		if c.Source[k] != v {
			t.Errorf("Source[%s] = %q, want %q", k, c.Source[k], v)
		}
	}
	if c.File != filepath.Join(home, ".config", "mash", "config.json") {
		t.Errorf("File %q", c.File)
	}
	// The file's own values, to compare with the effective ones: the env overrode url, so they differ.
	if c.URL != "tls://env:4222" || c.FileValues["url"] != "tls://file:4222" {
		t.Errorf("url %q, file said %q", c.URL, c.FileValues["url"])
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
	c, err := load(envOf(map[string]string{"MASH_URL": "tls://e:1", "MASH_OWNER": "o", "MASH_INSECURE_LOOPBACK": "1"}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.File != "" || len(c.FileValues) != 0 {
		t.Errorf("File %q FileValues %v", c.File, c.FileValues)
	}
	for k, v := range map[string]string{"url": "MASH_URL", "owner": "MASH_OWNER", "admin_creds": "unset", "insecure_loopback": "MASH_INSECURE_LOOPBACK"} {
		if c.Source[k] != v {
			t.Errorf("Source[%s] = %q, want %q", k, c.Source[k], v)
		}
	}
}

func TestSourcesAgreeingEnvStillNamesEnv(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, `{"url":"tls://same:4222","insecure_loopback":false}`)
	c, err := load(envOf(map[string]string{"MASH_URL": "tls://same:4222"}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.Source["url"] != "MASH_URL" || c.FileValues["url"] != c.URL {
		t.Errorf("%v %v", c.Source, c.FileValues)
	}
	if c.FileValues["insecure_loopback"] != "false" || c.Source["insecure_loopback"] != "config.json" {
		t.Errorf("%v %v", c.Source, c.FileValues)
	}
}

// load is Load over a plain env func: a variable is present iff its value is not empty.
func load(env func(string) string, home string) (Config, error) {
	return Load(LookupFromFunc(env), home)
}
