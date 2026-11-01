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
