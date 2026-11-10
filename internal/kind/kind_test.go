package kind

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	for in, want := range map[string]Kind{"blocker": Blocker, "Question": Question, " TASK ": Task, "result": Result, "fyi": FYI, "": Task} {
		if got, err := Parse(in); err != nil || got != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	_, err := Parse("urgent")
	if err == nil || !strings.Contains(err.Error(), "blocker|question|task|result|fyi") {
		t.Fatalf("unknown kind: %v", err)
	}
	if Of("") != Task || Of("junk") != Task || Of("task") != Task || Of("fyi") != FYI || Of("result") != Result {
		t.Fatal("Of")
	}
}

func TestWakes(t *testing.T) {
	for k, want := range map[Kind]bool{Blocker: true, Question: true, Task: true, Result: false, FYI: false, "": true} {
		if k.Wakes() != want {
			t.Errorf("%q.Wakes() = %v", k, !want)
		}
	}
	if Kind("").String() != "task" {
		t.Fatal("the zero kind is task")
	}
}

// TestKindInventoryIsTrue pins the behaviour docs/evidence/scrub/kinds.md describes: the five kinds, which wake, and
// that nothing outside the inventory is accepted on send or read as anything but task on receipt.
func TestKindInventoryIsTrue(t *testing.T) {
	wantAll := []Kind{Blocker, Question, Task, Result, FYI}
	if len(All) != len(wantAll) {
		t.Fatalf("All = %v; want %v", All, wantAll)
	}
	for i, k := range wantAll {
		if All[i] != k {
			t.Fatalf("All[%d] = %q; want %q", i, All[i], k)
		}
	}

	inventory := map[Kind]bool{Blocker: true, Question: true, Task: true, Result: false, FYI: false}
	if len(inventory) != len(All) {
		t.Fatalf("inventory has %d rows, All has %d kinds: a kind needs a row", len(inventory), len(All))
	}
	for _, k := range All {
		want, ok := inventory[k]
		if !ok {
			t.Errorf("kind %q has no inventory row", k)
			continue
		}
		if k.Wakes() != want {
			t.Errorf("%q.Wakes() = %v; inventory says %v", k, k.Wakes(), want)
		}
	}

	for _, name := range []string{"dispatch-to-supervisor", "urgent", "supervisor", "worker"} {
		_, err := Parse(name)
		if err == nil || !strings.Contains(err.Error(), "blocker|question|task|result|fyi") {
			t.Errorf("Parse(%q) error = %v; want a refusal listing the five kinds", name, err)
		}
		if got := Of(name); got != Task {
			t.Errorf("Of(%q) = %q; want task", name, got)
		}
	}

	for _, k := range All {
		for _, in := range []string{string(k), strings.ToUpper(string(k)), "  " + strings.ToUpper(string(k)[:1]) + string(k)[1:] + "\t"} {
			if got, err := Parse(in); err != nil || got != k {
				t.Errorf("Parse(%q) = %q, %v; want %q", in, got, err, k)
			}
		}
	}
}

func TestTag(t *testing.T) {
	long := strings.Repeat("x", 281)
	edge := strings.Repeat("é", 280) // 280 characters, 560 bytes
	for _, c := range []struct {
		name   string
		k      Kind
		reply  uint64
		body   string
		want   Kind
		tagged bool
	}{
		{"short reply question", Question, 3, "ok, done", FYI, true},
		{"short reply task stays", Task, 3, "Please deploy the fix", Task, false},
		{"has a question mark", Question, 3, "done?", Question, false},
		{"long", Question, 3, long, Question, false},
		{"280 characters, not bytes", Question, 3, edge, FYI, true},
		{"not a reply", Question, 0, "ok, done", Question, false},
		{"blocker stays", Blocker, 3, "ok, done", Blocker, false},
		{"fyi stays", FYI, 3, "ok", FYI, false},
		{"result stays", Result, 3, "ok", Result, false},
	} {
		got, tagged := Tag(c.k, c.reply, c.body)
		if got != c.want || tagged != c.tagged {
			t.Errorf("%s: Tag = %q, %v; want %q, %v", c.name, got, tagged, c.want, c.tagged)
		}
	}
}

// An unkinded reply is task and is never downgraded; a question reply with no '?' still becomes fyi.
func TestTagLeavesAnUnkindedReplyAlone(t *testing.T) {
	k, _ := Parse("")
	if got, tagged := Tag(k, 3, "ok, done"); got != Task || tagged {
		t.Fatalf("unkinded reply: %q %v", got, tagged)
	}
	if got, tagged := Tag(Question, 3, "ok, done"); got != FYI || !tagged {
		t.Fatalf("question reply: %q %v", got, tagged)
	}
}
