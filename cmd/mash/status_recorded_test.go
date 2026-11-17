package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
)

// connRecordPath is where the session of address a records its connection state.
func connRecordPath(t *testing.T, w *world, a string) string {
	t.Helper()
	cfg, _ := loadCfg(w.env[a])
	d, err := endpoint.SpoolDir(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(d, endpoint.ConnectionFile)
}

// A session whose credentials are refused records the failure; `mash status` from the shell says why.
func TestStatusCLIShowsRecordedAuthorizationFailure(t *testing.T) {
	w := newWorld(t, "a")
	old := openEndpoint
	openEndpoint = func(context.Context, config.Config) (*endpoint.Endpoint, error) {
		return nil, errors.New("mash: connect nats://user:hunter2@host:4222: nats: Authorization Violation")
	}
	t.Cleanup(func() { openEndpoint = old })
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.await(t, 5*time.Second, isChannel)

	path := connRecordPath(t, w, "a")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", fi.Mode().Perm())
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "hunter2") || !strings.Contains(string(b), "REDACTED@") {
		t.Errorf("record not redacted: %s", b)
	}
	out, code := runStatusCLI(t, w.env["a"])
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	l := statusLine(t, out, "connection: ")
	wantAll(t, "cli failed line", l, "connection: FAILED - ", "Authorization Violation", "recorded by the session at ",
		"Next: ", "mashctl endpoint show <address>")
	if strings.Contains(l, "hunter2") {
		t.Errorf("line leaks: %s", l)
	}
}

// With no record the CLI text is the old one.
func TestStatusCLINoRecordKeepsOldText(t *testing.T) {
	w := newWorld(t, "a")
	out, _ := runStatusCLI(t, w.env["a"])
	if l := statusLine(t, out, "connection: "); l != "connection: "+cliLiveNote {
		t.Fatalf("got %q", l)
	}
}

// A later successful connect rewrites the record as ok, and status shows the old text again.
func TestStatusCLIRecordClearedByConnect(t *testing.T) {
	w := newWorld(t, "a")
	cfg, _ := loadCfg(w.env["a"])
	if err := endpoint.RecordConnection(cfg, "failed", "nats: Authorization Violation", time.Now()); err != nil {
		t.Fatal(err)
	}
	out, _ := runStatusCLI(t, w.env["a"])
	if l := statusLine(t, out, "connection: "); !strings.HasPrefix(l, "connection: FAILED - ") {
		t.Fatalf("want the recorded failure, got %q", l)
	}
	s := startServe(t, w.env["a"])
	s.initialize(t)
	eventually(t, 5*time.Second, "record says ok", func() bool {
		c, ok := endpoint.ReadConnection(cfg)
		return ok && c.State == "ok"
	})
	out, _ = runStatusCLI(t, w.env["a"])
	if l := statusLine(t, out, "connection: "); l != "connection: "+cliLiveNote {
		t.Fatalf("got %q", l)
	}
}

func loadCfg(env map[string]string) (config.Config, error) {
	e := envFrom(env)
	return config.Load(config.LookupFromFunc(e), e("HOME"))
}

// A future error must not be able to carry a credential into connection.json or the status output: a JWT and an nkey
// seed are masked as well as URL userinfo.
func TestRedactTextMasksJWTAndSeed(t *testing.T) {
	jwtLike := "eyJ0eXAiOiJKV1QiLCJhbGciOiJlZDI1NTE5LW5rZXkifQ.eyJqdGkiOiJBQkNERUZHSElKS0xNTk9QIiwiaWF0IjoxNzAwMDAwMDAwfQ.c2lnbmF0dXJlLWJ5dGVzLWhlcmU"
	seedLike := "SUAEXAMPLEEXAMPLEEXAMPLEEXAMPLEEXAMPLEEXAMPLEEXAMPLEEXAMPLEEXAMPLE"[:58]
	for _, in := range []string{
		"nats: bad creds " + jwtLike + " at server",
		"nats: bad seed " + seedLike + " at server",
		"connect nats://u:s3cret@host:4222: bad " + jwtLike + " and " + seedLike,
	} {
		got := redactText(in)
		for _, secret := range []string{jwtLike, seedLike, "s3cret"} {
			if strings.Contains(got, secret) {
				t.Errorf("redactText(%q) = %q still holds %q", in, got, secret)
			}
		}
		if !strings.Contains(got, "REDACTED") {
			t.Errorf("redactText(%q) = %q masks nothing", in, got)
		}
	}
	if got := redactText("nats: Authorization Violation"); got != "nats: Authorization Violation" {
		t.Errorf("plain text changed: %q", got)
	}
}
