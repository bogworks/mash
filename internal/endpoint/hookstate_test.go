package endpoint

import (
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/spool"
)

func recordTo(t *testing.T, sp spool.Spool, rec HookRecord) {
	t.Helper()
	b, _ := json.Marshal(rec)
	if err := sp.NoteHookState(b); err != nil {
		t.Fatal(err)
	}
}

// A shell reads the session from the record only while the serve that wrote it exists, on this machine; a hook run and a
// refusal are read from the spool itself.
func TestSessionOnDisk(t *testing.T) {
	sp := spool.Spool{Dir: t.TempDir()}
	host, _ := os.Hostname()
	since := time.Now().Add(-time.Minute)
	if _, _, ok := SessionOnDisk(sp); ok {
		t.Fatal("a session with no record")
	}
	self := HookRecord{Hooks: HookSuspected, Storage: StorageOK, Since: since, Host: host, PID: strconv.Itoa(os.Getpid())}
	self.Start, _ = procStart(os.Getpid())
	recordTo(t, sp, self)
	if h, st, ok := SessionOnDisk(sp); !ok || h != HookSuspected || st != StorageOK {
		t.Errorf("suspected: %q %q %v", h, st, ok)
	}
	self.Hooks, self.Storage = "", StorageFailing
	recordTo(t, sp, self)
	if h, st, ok := SessionOnDisk(sp); !ok || h != HookNotRun || st != StorageFailing {
		t.Errorf("not run: %q %q %v", h, st, ok)
	}
	if err := sp.NoteRefused(spool.Refusal{Time: time.Now(), Why: "x"}); err != nil {
		t.Fatal(err)
	}
	if h, _, _ := SessionOnDisk(sp); h != HookRefused {
		t.Errorf("refused: %q", h)
	}
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	if h, _, _ := SessionOnDisk(sp); h != HookRan {
		t.Errorf("ran: %q", h)
	}
	other := self
	other.Host = host + "-elsewhere"
	recordTo(t, sp, other)
	if _, _, ok := SessionOnDisk(sp); ok {
		t.Error("a record of another machine")
	}
	// Own starts the next holder clean
	if err := sp.Own(`{"pid":1}`); err != nil {
		t.Fatal(err)
	}
	if _, found := sp.HookState(); found {
		t.Error("the record outlived Own")
	}
}
