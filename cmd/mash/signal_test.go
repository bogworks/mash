package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/store"
)

// A killed session (Claude Code exiting, a closed terminal) must release its
// lease at once, not leave the name blocked until the lease expires.
func TestServeReleasesLeaseOnSignal(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "mash")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			w := newWorld(t, "a")
			cmd := exec.Command(bin, "serve")
			for k, v := range w.env["a"] {
				cmd.Env = append(cmd.Env, k+"="+v)
			}
			stdin, _ := cmd.StdinPipe()
			cmd.Stdout = io.Discard
			cmd.Stderr = os.Stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			_, _ = io.WriteString(stdin, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`+"\n")

			js, _ := jetstream.New(w.e.Conn(t, w.e.AdminCreds))
			kv, err := js.KeyValue(context.Background(), store.LeaseBucket)
			if err != nil {
				t.Fatal(err)
			}
			held := func() bool {
				_, err := kv.Get(context.Background(), "a")
				return err == nil
			}
			deadline := time.Now().Add(10 * time.Second)
			for !held() {
				if time.Now().After(deadline) {
					t.Fatal("serve never took the lease")
				}
				time.Sleep(50 * time.Millisecond)
			}
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				_ = cmd.Process.Kill()
				t.Fatal("serve did not exit on signal")
			}
			if held() {
				t.Fatal("lease still held after a clean signal exit")
			}
			_, err = kv.Get(context.Background(), "a")
			if !errors.Is(err, jetstream.ErrKeyNotFound) && !errors.Is(err, jetstream.ErrKeyDeleted) {
				t.Fatalf("lease lookup: %v", err)
			}
		})
	}
}
