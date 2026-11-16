package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/activity"
)

func TestProgramStatusMapping(t *testing.T) {
	cases := []struct {
		name    string
		a       activity.Activity
		present bool
		want    string // the program_status JSON, "" for the key absent
	}{
		{"absent", activity.Activity{State: activity.StateIdle}, false, ""},
		{"empty state", activity.Activity{Source: "hooks"}, true, ""},
		{"hooks working", activity.Activity{State: activity.StateWorking, Source: "hooks"}, true, `{"state":"working"}`},
		{"hooks done (Stop or PostCompact) is idle", activity.Activity{State: activity.StateDone, Source: "hooks"}, true, `{"state":"idle"}`},
		{"hooks blocked permission is working", activity.Activity{State: activity.StateBlocked, Kind: activity.KindPermission, Source: "hooks"}, true, `{"state":"working"}`},
		{"hooks blocked question is working", activity.Activity{State: activity.StateBlocked, Kind: activity.KindQuestion, Source: "hooks"}, true, `{"state":"working"}`},
		{"herdr blocked permission", activity.Activity{State: activity.StateBlocked, Kind: activity.KindPermission, Source: "both"}, true, `{"kind":"permission","state":"blocked"}`},
		{"herdr blocked no kind", activity.Activity{State: activity.StateBlocked, Source: "runtime"}, true, `{"state":"blocked"}`},
		{"herdr blocked odd kind dropped", activity.Activity{State: activity.StateBlocked, Kind: "weird", Source: "both"}, true, `{"state":"blocked"}`},
		{"herdr idle", activity.Activity{State: activity.StateIdle, Source: "runtime"}, true, `{"state":"idle"}`},
		{"herdr working", activity.Activity{State: activity.StateWorking, Source: "runtime"}, true, `{"state":"working"}`},
		{"herdr done is kept", activity.Activity{State: activity.StateDone, Source: "runtime"}, true, `{"state":"done"}`},
		{"both done is the runtime's done", activity.Activity{State: activity.StateDone, Source: "both"}, true, `{"state":"done"}`},
		{"kind only with blocked", activity.Activity{State: activity.StateWorking, Kind: activity.KindQuestion, Source: "both"}, true, `{"state":"working"}`},
		{"unknown state", activity.Activity{State: "sleeping", Source: "runtime"}, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := programStatus(func() (activity.Activity, bool) { return c.a, c.present })()
			if c.want == "" {
				if len(got) != 0 {
					t.Fatalf("want no key, got %v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("keys %v", got)
			}
			b, _ := json.Marshal(got["program_status"])
			if string(b) != c.want {
				t.Fatalf("program_status %s, want %s", b, c.want)
			}
		})
	}
}

func TestProgramDetail(t *testing.T) {
	cur := func(a activity.Activity, ok bool) func() (activity.Activity, bool) {
		return func() (activity.Activity, bool) { return a, ok }
	}
	get := func(display string, a activity.Activity, ok bool) map[string]string {
		m := programDetail(display, cur(a, ok))()
		if m == nil {
			return nil
		}
		return m["program_detail"].(map[string]string)
	}
	d := get("mash-sdk", activity.Activity{State: activity.StateBlocked, Kind: activity.KindPermission, Source: "both"}, true)
	if d["title"] != "mash-sdk (Claude Code)" || d["msg"] != "waiting for permission" {
		t.Fatalf("detail %v", d)
	}
	if d := get("mash-sdk", activity.Activity{State: activity.StateBlocked, Kind: activity.KindQuestion, Source: "both"}, true); d["msg"] != "waiting for an answer to a question" {
		t.Fatalf("question msg %v", d)
	}
	if d := get("mash-sdk", activity.Activity{State: activity.StateBlocked, Kind: activity.KindPermission, Source: "hooks"}, true); d["msg"] != "working" {
		t.Fatalf("hooks-only blocked says working, got %v", d)
	}
	if d := get("mash-sdk", activity.Activity{}, false); d["msg"] != "" || d["title"] == "" {
		t.Fatalf("absent state: title only, got %v", d)
	}
	if d := get("", activity.Activity{}, false); d != nil {
		t.Fatalf("both empty: key omitted, got %v", d)
	}
	long := strings.Repeat("é", 200) // 2 bytes each
	d = get(long, activity.Activity{State: activity.StateWorking, Source: "hooks"}, true)
	if len(d["title"]) > programTitleMax || !strings.HasSuffix(d["title"], "é") && len(d["title"]) != programTitleMax {
		t.Fatalf("title %d bytes", len(d["title"]))
	}
	if cutRunes(strings.Repeat("é", 200), 119) != strings.Repeat("é", 59) {
		t.Fatal("cut at an odd byte count must stop at a rune boundary")
	}
}
