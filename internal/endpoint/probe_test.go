package endpoint

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/config"
)

func refusedCfg(t *testing.T) (config.Config, string) {
	t.Helper()
	cfg := config.Config{Owner: "acme", Group: "demo", Name: "bob", StateDir: t.TempDir()}
	d, err := SpoolDir(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, d
}

// The count of refused unverified messages is what the file holds; anything else, or no file, is none.
func TestRefusedCount(t *testing.T) {
	cfg, dir := refusedCfg(t)
	if n := RefusedCount(cfg); n != 0 {
		t.Fatalf("no file: %d", n)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for body, want := range map[string]int{"3\n": 3, "12": 12, "0": 0, "": 0, "many": 0, "-2": 0, "1.5": 0} {
		if err := os.WriteFile(filepath.Join(dir, RefusedFile), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if n := RefusedCount(cfg); n != want {
			t.Errorf("%q: %d, want %d", body, n, want)
		}
	}
	if n := RefusedCount(config.Config{}); n != 0 {
		t.Errorf("no endpoint: %d", n)
	}
}

// CheckServer says a server answered when something speaks NATS and asks for credentials (or accepts none), and says
// the server did not answer when nothing listens; a plain URL is refused before anything is dialled.
func TestCheckServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens there now
	cfg := config.Config{URL: "tls://" + addr, Name: "bob", Owner: "acme", Group: "demo"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := CheckServer(ctx, cfg); err == nil {
		t.Fatal("a closed port answered")
	}
	cfg.URL = "nats://" + addr
	if err := CheckServer(ctx, cfg); err == nil || !strings.Contains(err.Error(), "insecure_plaintext") {
		t.Fatalf("a plain URL with no override: %v", err)
	}
}
