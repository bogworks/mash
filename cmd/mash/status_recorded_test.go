package main

import (
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/config"
)

func loadCfg(env map[string]string) (config.Config, error) {
	e := envFrom(env)
	return config.Load(config.LookupFromFunc(e), e("HOME"))
}

// A future error must not be able to carry a credential into an error text, a log or a report: a JWT and an nkey
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
