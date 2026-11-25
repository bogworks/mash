package endpoint

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/spool"
)

// deliveredRecord returns the seqs serve has recorded as delivered under cfg.
func deliveredRecord(cfg config.Config) []uint64 {
	dir, _ := SpoolDir(cfg)
	_, seqs := readRefusedSeqs(filepath.Join(dir, deliveredSeqsFile))
	return seqs
}

// failFirst is a Deliverer that fails its first call, noting what was recorded as delivered at that moment.
type failFirst struct {
	cfg      config.Config
	ev       *events
	calls    atomic.Int32
	atFailed atomic.Int32 // the number of seqs recorded when the first write failed
}

func (d *failFirst) deliver(text string, meta map[string]string) error {
	if d.calls.Add(1) == 1 {
		d.atFailed.Store(int32(len(deliveredRecord(d.cfg))))
		return os.ErrClosed
	}
	return d.ev.deliver(text, meta)
}

func waitRecorded(t *testing.T, cfg config.Config) {
	t.Helper()
	until(t, "recorded as delivered", func() bool { return len(deliveredRecord(cfg)) == 1 })
}

// A failed channel write is not a delivery: the message is not recorded as delivered, and is once its redelivery is
// written (issue #1507).
func TestDeliveredRecordedOnlyAfterChannelWrite(t *testing.T) {
	f := newFixture(t, "a", "b")
	cfg := f.cfgState("a", t.TempDir(), true)
	f.unknownSignerMsg(t, "a")
	d := &failFirst{cfg: cfg, ev: &events{}}
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, d.deliver); close(done) }()
	t.Cleanup(func() { cancel(); <-done; ep.Close() })
	d.ev.waitFor(t, 1, 10*time.Second)
	if n := d.atFailed.Load(); n != 0 {
		t.Fatalf("%d messages recorded as delivered when the write failed", n)
	}
	waitRecorded(t, cfg)
}

// A message whose spool write failed is not recorded: only the channel fallback's successful write records it.
func TestDeliveredNotRecordedWhenSpoolWriteFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through a 0500 directory")
	}
	f := newFixture(t, "a", "b")
	cfg := f.cfgState("a", t.TempDir(), true)
	f.unknownSignerMsg(t, "a")
	d := &failFirst{cfg: cfg, ev: &events{}}
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	sp := spool.Spool{Dir: t.TempDir() + "/spool"}
	if err := ep.SetSpool(sp, ""); err != nil {
		t.Fatal(err)
	}
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sp.Dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(sp.Dir, 0o700) })
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, d.deliver); close(done) }()
	t.Cleanup(func() { cancel(); <-done; ep.Close() })
	d.ev.waitFor(t, 1, 15*time.Second)
	if n := d.atFailed.Load(); n != 0 {
		t.Fatalf("%d messages recorded as delivered with the spool and the channel both failing", n)
	}
	waitRecorded(t, cfg)
}
