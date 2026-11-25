package endpoint

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/spool"
)

// SetSpoolContext waits for the spool lock on its context: a hook stalled while holding the lock cannot keep serve
// from exiting (#1651).
func TestSetSpoolContextLockWaitFollowsContext(t *testing.T) {
	s := spool.Spool{Dir: filepath.Join(t.TempDir(), "spool")}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	unlock, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	e := &Endpoint{}
	start := time.Now()
	if err := e.SetSpoolContext(ctx, s, "owner"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("SetSpoolContext = %v, want deadline exceeded", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("SetSpoolContext took %v to give up", d)
	}
}
