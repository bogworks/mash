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
