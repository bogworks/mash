package host

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bogworks/mash/internal/spool"
)

// wakeCap is how many wake files serve leaves in the spool's wakes directory: with no extension consuming them
// (Pi not running) they must not grow without bound.
const wakeCap = 64

// Pi is the adapter for Pi (@earendil-works/pi-coding-agent): an extension runs `mash hook` as a child process and
// shows the JSON answer. Pi drops MCP channel notifications, so the wake is a file the extension watches.
type Pi struct {
	// SpoolDir is the endpoint's spool directory; wake files go in its wakes subdirectory.
	SpoolDir string
}

func (Pi) Name() string { return "pi" }

// Trigger reads the extension's one-word event. Anything else is not ours.
func (Pi) Trigger(stdin []byte, _ func(string) string) (Trigger, bool) {
	var in struct {
		Event string `json:"event"`
	}
	if json.Unmarshal(stdin, &in) != nil {
		return Trigger{}, false
	}
	switch in.Event {
	case "prompt", "tool", "wake", "init":
		return Trigger{Event: in.Event}, true
	}
	return Trigger{}, false
}

// OwnerToken is the JSON owner record of the serve that holds the lease: its parent (the Pi process) and its launch
// token; Pi has no session id for serve to record.
func (Pi) OwnerToken(env func(string) string) string { return ownerToken(env, "") }

// Owns: the hook is spawned by the Pi extension, so it is a descendant of the Pi process (or carries the launch
// token), as under Claude.
func (p Pi) Owns(owner string, h HookIdentity) bool {
	ok, _ := p.Match(owner, h)
	return ok
}

// Match: see matchOwner.
func (Pi) Match(owner string, h HookIdentity) (bool, string) { return matchOwner(owner, h) }

// SpoolsFromStart: Pi never sees the channel notification.
func (Pi) SpoolsFromStart() bool { return true }

func (p Pi) wakeDir() string { return filepath.Join(p.SpoolDir, "wakes") }

// Init answers the extension's init event with the wake directory it should watch. It drains nothing.
func (p Pi) Init(t Trigger) ([]byte, bool) {
	if t.Event != "init" {
		return nil, false
	}
	dir, err := filepath.Abs(p.wakeDir())
	if err != nil {
		dir = p.wakeDir()
	}
	out, _ := json.Marshal(map[string]string{"wake_dir": dir})
	return append(out, '\n'), true
}

// Present renders the batch as one JSON object. The text is Claude's framed batch without its 10,000-character
// cap, which Pi does not have; the byte budget still bounds it.
func (Pi) Present(p Presentation, waiting []spool.Entry) ([]byte, []spool.Entry) {
	shown := batchEntries(p, waiting)
	seqs := make([]uint64, len(shown))
	urgent := false
	for i, e := range shown {
		seqs[i] = e.Seq
		urgent = urgent || e.Urgent
	}
	out, _ := json.Marshal(struct {
		Text   string   `json:"text"`
		Seqs   []uint64 `json:"seqs"`
		Urgent bool     `json:"urgent"`
	}{frameBatch(p, shown, p.BatchBytes), seqs, urgent})
	return append(out, '\n'), shown
}

var (
	wakeMu  sync.Mutex // one writer at a time, so the cap's listing and deletes do not race
	wakeSeq atomic.Uint64
)

// Waker ignores the channel notification and writes each wake as its own file in the wakes directory: a file per
// wake, so a watcher never merges two, written to a temp name and renamed so it never reads a partial one. The
// extension deletes what it reads. A write failure is returned; the mail stays spooled and serve re-wakes.
func (p Pi) Waker(func(string, map[string]string) error) func(string, map[string]string) error {
	return func(text string, meta map[string]string) error {
		body, err := json.Marshal(map[string]any{"at": time.Now().Format(time.RFC3339Nano), "text": text, "meta": meta})
		if err != nil {
			return err
		}
		wakeMu.Lock()
		defer wakeMu.Unlock()
		dir := p.wakeDir()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		name := fmt.Sprintf("%020d-%010d.json", time.Now().UnixNano(), wakeSeq.Add(1))
		tmp := filepath.Join(dir, "."+name+".tmp")
		if err := os.WriteFile(tmp, append(body, '\n'), 0o600); err != nil {
			return err
		}
		if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
			os.Remove(tmp)
			return err
		}
		return pruneWakes(dir)
	}
}

// pruneWakes deletes the oldest wake files beyond wakeCap (names sort in write order).
func pruneWakes(dir string) error {
	des, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, d := range des {
		if strings.HasSuffix(d.Name(), ".json") && !strings.HasPrefix(d.Name(), ".") {
			names = append(names, d.Name())
		}
	}
	slices.Sort(names)
	var first error
	for len(names) > wakeCap {
		if err := os.Remove(filepath.Join(dir, names[0])); err != nil && !os.IsNotExist(err) && first == nil {
			first = err
		}
		names = names[1:]
	}
	return first
}
