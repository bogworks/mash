package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/store"
)

func TestParseWhoFlags(t *testing.T) {
	f, o, err := ParseWhoFlags([]string{"--long", "--match", "nats", "--name=al", "--owner", "o", "--role=r", "--label", "a", "--label=b=c", "--online"})
	if err != nil {
		t.Fatal(err)
	}
	want := endpoint.WhoFilter{Match: "nats", Name: "al", Owner: "o", Role: "r", Labels: []string{"a", "b=c"}, Online: true}
	if !reflect.DeepEqual(f, want) || !o.Long || o.JSON || o.Names {
		t.Fatalf("got %+v %+v", f, o)
	}
	if _, o, err := ParseWhoFlags([]string{"--json"}); err != nil || !o.JSON {
		t.Fatalf("json: %+v %v", o, err)
	}
	for _, bad := range [][]string{{"--match"}, {"--label"}, {"--bogus"}, {"extra"}, {"--online", "--offline"}, {"--json", "--names"},
		{"--label", "=x"}, {"--label=k="}, {"--long=1"}} {
		if _, _, err := ParseWhoFlags(bad); err == nil || !strings.Contains(err.Error(), "usage: mash who") {
			t.Errorf("%v: err %v", bad, err)
		}
	}
}

func whoFixture() []endpoint.Presence {
	return []endpoint.Presence{
		{Online: true, LastSeen: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), Record: store.Record{Name: "a", Owner: "o", Role: "chief", Host: "h", Note: "n",
			Labels: map[string]string{"z": "1", "a": "2"}}},
		{Record: store.Record{Name: "b", Owner: "o"}},
	}
}

func TestWhoRenderers(t *testing.T) {
	ps := whoFixture()
	if got := whoNames(ps); got != "a\nb" {
		t.Fatalf("names %q", got)
	}
	const golden = `[
  {
    "name": "a",
    "owner": "o",
    "online": true,
    "last_seen": "2026-11-01T12:00:00Z",
    "role": "chief",
    "host": "h",
    "note": "n",
    "labels": {
      "a": "2",
      "z": "1"
    }
  },
  {
    "name": "b",
    "owner": "o",
    "online": false
  }
]`
	if got := whoJSON(ps, false, nil); got != golden {
		t.Fatalf("json:\n%s", got)
	}
	live := map[string]endpoint.LeaseInfo{"a": {Host: "lh", Version: "1.0"}}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(whoJSON(ps, true, live)), &arr); err != nil || arr[0]["lease"].(map[string]any)["host"] != "lh" || arr[1]["lease"] != nil {
		t.Fatalf("long json: %v %v", arr, err)
	}
	if strings.Contains(whoJSON(ps, false, live), "lease") {
		t.Fatal("lease without --long")
	}
	if got := whoJSON(nil, false, nil); got != "[]" {
		t.Fatalf("empty json %q", got)
	}
	if got := whoRender(nil, whoOutput{}, true, nil); got != "" {
		t.Fatalf("filtered empty text %q", got)
	}
	if got := whoRender(nil, whoOutput{}, false, nil); got != "no endpoints" {
		t.Fatalf("empty directory %q", got)
	}
	if got := whoRender(nil, whoOutput{Names: true}, false, nil); got != "" {
		t.Fatalf("empty names %q", got)
	}
}
