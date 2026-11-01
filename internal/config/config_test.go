package config

import (
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
	c, err := Load(envOf(map[string]string{"MASH_NAME": "a"}), home)
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
	c, err := Load(envOf(map[string]string{
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
	c, err := Load(envOf(map[string]string{"MASH_NAME": "a"}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.SettleMS != 5 || c.BatchMsgs != 2 || c.BatchBytes != 64 {
		t.Fatalf("got %+v", c)
	}
}

func TestInertWithoutName(t *testing.T) {
	home := t.TempDir()
	c, err := Load(envOf(nil), home)
	if err != nil {
		t.Fatalf("inert load must not fail: %v", err)
	}
	if !c.Inert() {
		t.Fatal("expected inert")
	}
}

func TestMissingURLIsErrorUnlessInert(t *testing.T) {
	home := t.TempDir()
	_, err := Load(envOf(map[string]string{"MASH_NAME": "a", "MASH_OWNER": "o"}), home)
	if err == nil || !strings.Contains(err.Error(), "url") {
		t.Fatalf("want url error, got %v", err)
	}
	_, err = Load(envOf(map[string]string{"MASH_NAME": "a", "MASH_URL": "tls://x:1"}), home)
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
	if _, err := Load(envOf(map[string]string{"MASH_NAME": "Bad", "MASH_URL": "u", "MASH_OWNER": "o"}), home); err == nil {
		t.Fatal("bad MASH_NAME must be rejected")
	}
	if _, err := Load(envOf(map[string]string{"MASH_NAME": "a", "MASH_URL": "u", "MASH_OWNER": "O"}), home); err == nil {
		t.Fatal("bad owner must be rejected")
	}
}

func TestBadNumbersRejected(t *testing.T) {
	home := t.TempDir()
	_, err := Load(envOf(map[string]string{"MASH_NAME": "a", "MASH_URL": "u", "MASH_OWNER": "o", "MASH_SETTLE_MS": "x"}), home)
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
		return Load(func(k string) string { return env[k] }, t.TempDir())
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
		return Load(envOf(env), home)
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

func TestTimeoutSettings(t *testing.T) {
	load := func(file string, env map[string]string) (Config, error) {
		home := t.TempDir()
		writeFile(t, home, `{"url":"tls://f:4222","owner":"o"`+file+`}`)
		env["MASH_NAME"] = "a"
		return Load(envOf(env), home)
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

func TestLoadSpoolFromStart(t *testing.T) {
	load := func(t *testing.T, file string, env map[string]string) (Config, error) {
		t.Helper()
		home := t.TempDir()
		writeFile(t, home, file)
		env["MASH_NAME"] = "a"
		return Load(envOf(env), home)
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
	c, err := Load(envOf(nil), home)
	if err != nil || c.Limits != store.DefaultLimits() {
		t.Fatalf("no file: %+v %v", c.Limits, err)
	}
	writeFile(t, home, `{"limits":{"inbox_max_bytes":"64MiB","inbox_max_age":"7d","directory_max_bytes":2097152,"lease_max_bytes":"1GiB","inbox_max_consumers":4}}`)
	c, err = Load(envOf(nil), home)
	want := store.Limits{InboxMaxBytes: 64 << 20, InboxMaxAge: 7 * 24 * time.Hour, DirectoryMaxBytes: 2 << 20, LeaseMaxBytes: 1 << 30, InboxMaxConsumers: 4}
	if err != nil || c.Limits != want {
		t.Fatalf("custom: %+v %v", c.Limits, err)
	}
	writeFile(t, home, `{"limits":{"inbox_max_age":"36h"}}`)
	if c, err = Load(envOf(nil), home); err != nil || c.Limits.InboxMaxAge != 36*time.Hour || c.Limits.InboxMaxBytes != 16<<20 {
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
		if _, err := Load(envOf(nil), home); err == nil || !strings.Contains(err.Error(), bad.key) {
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
	c, err := Load(envOf(map[string]string{"MASH_NAME": "a"}), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.TLSCA != "/f/ca.pem" || c.TLSCert != "/f/c.pem" || c.TLSKey != "/f/k.pem" {
		t.Fatalf("file: got %q %q %q", c.TLSCA, c.TLSCert, c.TLSKey)
	}
	c, err = Load(envOf(map[string]string{"MASH_NAME": "a", "MASH_TLS_CA": "/e/ca.pem", "MASH_TLS_CERT": "/e/c.pem", "MASH_TLS_KEY": "/e/k.pem"}), home)
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
	_, err := Load(envOf(map[string]string{"MASH_NAME": "a"}), home)
	if err == nil || !strings.Contains(err.Error(), "tls_cert") || !strings.Contains(err.Error(), "tls_key") {
		t.Fatalf("file cert only: err = %v", err)
	}
	// The environment can complete the pair, and can break one.
	if _, err := Load(envOf(map[string]string{"MASH_NAME": "a", "MASH_TLS_KEY": "/e/k.pem"}), home); err != nil {
		t.Fatalf("pair completed by env: %v", err)
	}
	writeFile(t, home, `{"url":"tls://x:4222","owner":"o"}`)
	if _, err := Load(envOf(map[string]string{"MASH_NAME": "a", "MASH_TLS_KEY": "/e/k.pem"}), home); err == nil {
		t.Fatal("key only: want an error")
	}
	// The files are read at connect time, not at load.
	if _, err := Load(envOf(map[string]string{"MASH_NAME": "a", "MASH_TLS_CA": "/missing"}), home); err != nil {
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
