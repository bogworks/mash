//go:build unix

package ctlkeys

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A FIFO with no writer must be refused, not block the open forever.
func TestReadOwnedFileRefusesFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := ReadOwnedFile(path, "system_creds")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("err = %v, want one containing %q", err, "not a regular file")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ReadOwnedFile blocked on a FIFO")
	}
}
