//go:build unix

package endpoint

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestOpenRefusesCredsReadableByOthers(t *testing.T) {
	f := newFixture(t, "a")
	cfg := f.cfg("a")
	if err := os.Chmod(cfg.Creds, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := OpenClientWith(context.Background(), cfg, ClientOptions{})
	if err == nil || !strings.Contains(err.Error(), "chmod 600 "+cfg.Creds) || !strings.Contains(err.Error(), "0644") {
		t.Fatalf("want a refusal with the chmod hint, got %v", err)
	}
}

func TestOpenNamesOperatorModeJWTCredentials(t *testing.T) {
	f := newFixture(t, "a")
	for name, body := range map[string]string{
		"password line": "alice:s3cretpassw0rd\n",
		"empty":         "",
	} {
		cfg := f.cfg("a")
		if err := os.WriteFile(cfg.Creds, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := OpenClientWith(context.Background(), cfg, ClientOptions{})
		if err == nil || !strings.Contains(err.Error(), "operator-mode JWT credentials") || !strings.Contains(err.Error(), cfg.Creds) || strings.Contains(err.Error(), "s3cretpassw0rd") {
			t.Fatalf("%s: got %v", name, err)
		}
	}
}
