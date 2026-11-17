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
		{Online: true, LastSeen: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), Record: store.Record{Owner: "o", Group: "g", Instance: "a", Role: "chief", Host: "h", Note: "n",
			Labels: map[string]string{"z": "1", "a": "2"}}},
		{Record: store.Record{Owner: "o", Group: "g", Instance: "b"}},
	}
}

func TestWhoRenderers(t *testing.T) {
	ps := whoFixture()
	if got := whoNames(ps); got != "o_g_a\no_g_b" {
		t.Fatalf("names %q", got)
	}
	const golden = `[
  {
    "name": "o_g_a",
    "identity": "o.g.a",
    "owner": "o",
    "group": "g",
    "instance": "a",
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
    "name": "o_g_b",
    "identity": "o.g.b",
    "owner": "o",
    "group": "g",
    "instance": "b",
    "online": false
  }
]`
	if got := whoJSON(ps, false, nil); got != golden {
		t.Fatalf("json:\n%s", got)
	}
	live := map[string]endpoint.LeaseInfo{"o_g_a": {Host: "lh", Version: "1.0"}}
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

func TestWhoCapsFlagsAndRenderers(t *testing.T) {
	f, _, err := ParseWhoFlags([]string{"--cap", "a", "--cap=b"})
	if err != nil || !reflect.DeepEqual(f.Caps, []string{"a", "b"}) {
		t.Fatalf("%+v %v", f, err)
	}
	for _, bad := range [][]string{{"--cap"}, {"--cap", "Bad"}, {"--cap="}} {
		if _, _, err := ParseWhoFlags(bad); err == nil || !strings.Contains(err.Error(), "usage: mash who") {
			t.Errorf("%v: %v", bad, err)
		}
	}
	ps := []endpoint.Presence{
		{Record: store.Record{Owner: "o", Group: "g", Instance: "a", Description: "does things", Caps: []string{"x", "y"}}},
		{Record: store.Record{Owner: "o", Group: "g", Instance: "b"}},
	}
	long := whoText(ps, true, nil)
	if !strings.Contains(long, "\tdesc does things\tcaps x,y") {
		t.Fatalf("long: %q", long)
	}
	if short := whoText(ps, false, nil); strings.Contains(short, "desc") || strings.Contains(short, "caps") {
		t.Fatalf("short: %q", short)
	}
	if l2 := strings.Split(long, "\n")[1]; strings.Contains(l2, "desc") || strings.Contains(l2, "caps") {
		t.Fatalf("unset shown: %q", long)
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(whoJSON(ps, false, nil)), &arr); err != nil || arr[0]["description"] != "does things" ||
		len(arr[0]["caps"].([]any)) != 2 || arr[1]["description"] != nil || arr[1]["caps"] != nil {
		t.Fatalf("json: %v %v", arr, err)
	}
}

func TestWhoShowsAddressAndHarness(t *testing.T) {
	r := store.Record{Owner: "acme", Group: "team1", Instance: "ops1", Agent: "claude-code"}
	ps := []endpoint.Presence{{Record: r}}
	long := whoText(ps, true, nil)
	if !strings.HasPrefix(long, "acme.team1.ops1\toffline") || !strings.Contains(long, "\tagent claude-code") {
		t.Fatalf("--long text: %q", long)
	}
	if short := whoText(ps, false, nil); strings.Contains(short, "agent") || short != "acme.team1.ops1\toffline" {
		t.Fatalf("short text: %q", short)
	}
	if got := whoNames(ps); got != "acme_team1_ops1" {
		t.Fatalf("--names stays the flat key: %q", got)
	}
	var out []map[string]any
	if err := json.Unmarshal([]byte(whoJSON(ps, false, nil)), &out); err != nil {
		t.Fatal(err)
	}
	e := out[0]
	if e["name"] != "acme_team1_ops1" || e["identity"] != "acme.team1.ops1" || e["owner"] != "acme" || e["group"] != "team1" ||
		e["instance"] != "ops1" || e["agent"] != "claude-code" {
		t.Fatalf("json: %v", e)
	}
	if _, has := e["alias"]; has {
		t.Fatalf("an alias key: %v", e)
	}
}
