package host

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/spool"
)

func TestPiSelectAndKnown(t *testing.T) {
	a, err := Select("pi", t.TempDir())
	if err != nil || a.Name() != "pi" || !a.SpoolsFromStart() {
		t.Fatalf("Select(pi) = %v, %v", a, err)
	}
	if !slices.Contains(Known(), "pi") {
		t.Fatalf("Known() = %v", Known())
	}
}

func TestPiTrigger(t *testing.T) {
	for in, want := range map[string]bool{
		`{"event":"prompt"}`:                true,
		`{"event":"tool"}`:                  true,
		`{"event":"wake"}`:                  true,
		`{"event":"init"}`:                  true,
		`{"event":"Stop"}`:                  false,
		`{"event":""}`:                      false,
		`{}`:                                false,
		`{"hook_event_name":"PostToolUse"}`: false,
		`not json`:                          false,
		``:                                  false,
	} {
		tr, ok := Pi{}.Trigger([]byte(in), nil)
		if ok != want || (ok && tr.Event == "") {
			t.Errorf("Trigger(%s) = %+v, %v; want ok %v", in, tr, ok, want)
		}
	}
}

func piEntries(n int) []spool.Entry {
	var es []spool.Entry
	for i := 1; i <= n; i++ {
		es = append(es, spool.Entry{Seq: uint64(i), From: "b", Trust: "verified", At: time.Now(), Body: []byte("m" + string(rune('0'+i)))})
	}
	return es
}

func decodePi(t *testing.T, out []byte) (o struct {
	Text   string   `json:"text"`
	Seqs   []uint64 `json:"seqs"`
	Urgent bool     `json:"urgent"`
}) {
	t.Helper()
	if len(out) == 0 || out[len(out)-1] != '\n' || strings.Count(string(out), "\n") != 1 {
		t.Fatalf("not one JSON line: %q", out)
	}
	if err := json.Unmarshal(out, &o); err != nil {
		t.Fatal(err)
	}
	return o
}

func TestPiPresentShape(t *testing.T) {
	out, shown := Pi{}.Present(Presentation{Name: "a", Owner: "o", Event: "tool", BatchMsgs: 10, BatchBytes: 4096}, piEntries(2))
	o := decodePi(t, out)
	if len(shown) != 2 || !slices.Equal(o.Seqs, []uint64{1, 2}) || o.Urgent {
		t.Fatalf("shown %d, %+v", len(shown), o)
	}
	for _, want := range []string{"mash mail for a, delivered by mash's hook", "2 messages from b", "> m1", "> m2"} {
		if !strings.Contains(o.Text, want) {
			t.Errorf("text lacks %q:\n%s", want, o.Text)
		}
	}
	if strings.Contains(string(out), "hookSpecificOutput") {
		t.Error("Claude's envelope in Pi output")
	}
}

// The text is the framed batch Claude renders, so what the model sees is the same.
func TestPiTextMatchesClaudes(t *testing.T) {
	p := Presentation{Name: "a", Owner: "o", Event: "tool", BatchMsgs: 10, BatchBytes: 4096}
	cOut, _ := Claude{}.Present(p, piEntries(3))
	var c struct {
		H struct {
			Ctx string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(cOut, &c); err != nil {
		t.Fatal(err)
	}
	pOut, _ := Pi{}.Present(p, piEntries(3))
	if got := decodePi(t, pOut).Text; got != c.H.Ctx {
		t.Fatalf("pi text differs from claude's:\n%s\n---\n%s", got, c.H.Ctx)
	}
}

func TestPiPresentUrgentFirst(t *testing.T) {
	es := piEntries(3)
	es[2].Urgent = true
	out, shown := Pi{}.Present(Presentation{Name: "a", Owner: "o", BatchMsgs: 10, BatchBytes: 4096}, es)
	o := decodePi(t, out)
	if !o.Urgent || !slices.Equal(o.Seqs, []uint64{3, 1, 2}) || shown[0].Seq != 3 {
		t.Fatalf("%+v", o)
	}
	if strings.Index(o.Text, "> m3") > strings.Index(o.Text, "> m1") {
		t.Fatalf("urgent not first:\n%s", o.Text)
	}
}

func TestPiPresentBatchMsgsLimit(t *testing.T) {
	es := piEntries(5)
	es[4].Urgent = true
	out, shown := Pi{}.Present(Presentation{Name: "a", Owner: "o", BatchMsgs: 3, BatchBytes: 4096}, es)
	o := decodePi(t, out)
	if len(shown) != 3 || !slices.Equal(o.Seqs, []uint64{5, 1, 2}) || strings.Contains(o.Text, "> m3") {
		t.Fatalf("%+v", o)
	}
	// BatchMsgs 0 still drains (as for Claude).
	_, shown = Pi{}.Present(Presentation{Name: "a", Owner: "o", BatchMsgs: 0, BatchBytes: 4096}, piEntries(4))
	if len(shown) != 4 {
		t.Fatalf("BatchMsgs 0 showed %d", len(shown))
	}
}

func TestPiPresentDigestPastBatchBytes(t *testing.T) {
	es := piEntries(2)
	es[0].Body = []byte(strings.Repeat("x", 500))
	out, shown := Pi{}.Present(Presentation{Name: "a", Owner: "o", BatchMsgs: 10, BatchBytes: 100}, es)
	o := decodePi(t, out)
	if len(shown) != 2 || !slices.Equal(o.Seqs, []uint64{1, 2}) || strings.Contains(o.Text, strings.Repeat("x", 500)) {
		t.Fatalf("not a digest: %+v", o)
	}
}

// Pi has no 10,000-character cap: a long text is shown whole, and every entry in it is drained.
func TestPiPresentIsNotTruncatedToClaudesCap(t *testing.T) {
	es := piEntries(3)
	for i := range es {
		es[i].Body = []byte(strings.Repeat("y", 6000))
	}
	p := Presentation{Name: "a", Owner: "o", BatchMsgs: 10, BatchBytes: 1 << 20}
	out, shown := Pi{}.Present(p, es)
	o := decodePi(t, out)
	if len(shown) != 3 || len(o.Seqs) != 3 || Utf16Len(o.Text) <= 10000 {
		t.Fatalf("shown %d, text %d units", len(shown), Utf16Len(o.Text))
	}
	if n := strings.Count(o.Text, strings.Repeat("y", 6000)); n != 3 {
		t.Fatalf("%d bodies whole, want 3", n)
	}
}

// A re-delivered entry carries the same note in Pi's text as in Claude's.
func TestPiPresentFlagsRedelivered(t *testing.T) {
	es := piEntries(2)
	es[1].Redelivered = true
	out, _ := Pi{}.Present(Presentation{Name: "a", Owner: "o", BatchMsgs: 10, BatchBytes: 4096}, es)
	text := decodePi(t, out).Text
	if !strings.Contains(text, "(#2 re-delivered after an interrupted hook") || strings.Contains(text, "(#1 re-delivered") {
		t.Fatalf("flag:\n%s", text)
	}
}

func wakeNames(t *testing.T, dir string) []string {
	t.Helper()
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range des {
		out = append(out, d.Name())
	}
	return out
}

func TestPiWakerWritesOneFilePerWake(t *testing.T) {
	var notified bool
	p := Pi{SpoolDir: filepath.Join(t.TempDir(), "spool")}
	wake := p.Waker(func(string, map[string]string) error { notified = true; return nil })
	for _, text := range []string{"one", "two", "three"} {
		if err := wake(text, map[string]string{"kind": "prompt", "request_id": text}); err != nil {
			t.Fatal(err)
		}
	}
	if notified {
		t.Fatal("the MCP notification was used")
	}
	dir := filepath.Join(p.SpoolDir, "wakes")
	names := wakeNames(t, dir)
	if len(names) != 3 || !slices.IsSorted(names) {
		t.Fatalf("names %v", names)
	}
	for i, text := range []string{"one", "two", "three"} {
		if !strings.HasSuffix(names[i], ".json") || strings.HasPrefix(names[i], ".") {
			t.Errorf("name %q", names[i])
		}
		raw, err := os.ReadFile(filepath.Join(dir, names[i]))
		if err != nil {
			t.Fatal(err)
		}
		var w struct {
			At   string            `json:"at"`
			Text string            `json:"text"`
			Meta map[string]string `json:"meta"`
		}
		if err := json.Unmarshal(raw, &w); err != nil {
			t.Fatal(err)
		}
		if _, err := time.Parse(time.RFC3339Nano, w.At); err != nil || w.Text != text || w.Meta["request_id"] != text || w.Meta["kind"] != "prompt" {
			t.Errorf("file %d: %s", i, raw)
		}
	}
	if err := wake("no meta", nil); err != nil {
		t.Fatal(err)
	}
	last := wakeNames(t, dir)[3]
	raw, _ := os.ReadFile(filepath.Join(dir, last))
	if !strings.Contains(string(raw), `"meta":null`) {
		t.Errorf("nil meta: %s", raw)
	}
}

func TestPiWakerCapsTheDirectory(t *testing.T) {
	p := Pi{SpoolDir: t.TempDir()}
	wake := p.Waker(nil)
	for i := 0; i < wakeCap+10; i++ {
		if err := wake(strings.Repeat("n", i%3)+string(rune('A'+i%26)), nil); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(p.SpoolDir, "wakes")
	names := wakeNames(t, dir)
	if len(names) != wakeCap {
		t.Fatalf("%d files, want %d", len(names), wakeCap)
	}
	// The oldest ten are gone: the survivors are the last wakeCap writes, whose first is write #10.
	raw, _ := os.ReadFile(filepath.Join(dir, names[0]))
	var w struct{ Text string }
	_ = json.Unmarshal(raw, &w)
	want := strings.Repeat("n", 10%3) + string(rune('A'+10%26))
	if w.Text != want {
		t.Fatalf("oldest survivor %q, want %q", w.Text, want)
	}
}

func TestPiWakerReturnsErrorsWithoutPanicking(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// The spool dir is a file, so the wakes directory cannot be made.
	if err := (Pi{SpoolDir: file}).Waker(nil)("x", nil); err == nil {
		t.Fatal("no error")
	}
}

func TestPiInit(t *testing.T) {
	dir := t.TempDir()
	out, ok := Pi{SpoolDir: dir}.Init(Trigger{Event: "init"})
	if !ok || string(out) != `{"wake_dir":"`+filepath.Join(dir, "wakes")+`"}`+"\n" {
		t.Fatalf("%q %v", out, ok)
	}
	if _, ok := (Pi{SpoolDir: dir}).Init(Trigger{Event: "tool"}); ok {
		t.Fatal("tool handled as init")
	}
	if _, err := os.Stat(filepath.Join(dir, "wakes")); err == nil {
		t.Fatal("init created the directory")
	}
}

// Pi owns exactly as Claude does: the parent's token, and any ancestor matching a pid: owner.
func TestPiOwnershipMatchesClaude(t *testing.T) {
	none := func(string) string { return "" }
	if (Pi{}).OwnerToken(none) != (Claude{}).OwnerToken(none) {
		t.Fatal("owner tokens differ")
	}
	stubParents(t, map[int]int{})
	old := Ancestors
	Ancestors = slices.Values([]int{111, 222})
	t.Cleanup(func() { Ancestors = old })
	for _, owner := range []string{"pid:111", "pid:222", "pid:999", "pid:x", "", "session-id", `{"pid":222}`, `{"pid":999}`} {
		if p, c := (Pi{}).Owns(owner, HookIdentity{}), (Claude{}).Owns(owner, HookIdentity{}); p != c {
			t.Errorf("Owns(%q): pi %v, claude %v", owner, p, c)
		}
	}
	if !(Pi{}).Owns("pid:222", HookIdentity{}) || (Pi{}).Owns("pid:999", HookIdentity{}) {
		t.Error("ancestry not enforced")
	}
}
