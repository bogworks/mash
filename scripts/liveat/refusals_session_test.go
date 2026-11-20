package main

import "testing"

func TestDegradedToolSet(t *testing.T) {
	for _, c := range []struct {
		names []string
		want  bool
	}{
		{[]string{"version", "status", "doctor"}, true},
		{[]string{"doctor", "status", "version"}, true},
		{nil, false},
		{[]string{"doctor", "status"}, false},
		{[]string{"doctor", "status", "version", "send"}, false},
		{[]string{"doctor", "status", "send"}, false},
	} {
		if got := degradedToolSet(c.names); got != c.want {
			t.Errorf("degradedToolSet(%v) = %v, want %v", c.names, got, c.want)
		}
	}
}

func TestToolNamesIn(t *testing.T) {
	out := `{"jsonrpc":"2.0","id":1,"result":{}}
{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"status"},{"name":"doctor"}]}}
`
	got := toolNamesIn(out, 2)
	if len(got) != 2 || got[0] != "doctor" || got[1] != "status" {
		t.Errorf("toolNamesIn = %v", got)
	}
	if toolNamesIn(out, 3) != nil {
		t.Error("an absent reply must give nil")
	}
}

func TestNamesBadAddress(t *testing.T) {
	good := `mash: not ready: MASH_ENDPOINT: sap: address "alice.ghost" has 2 dot-separated parts: want exactly three (owner.group.instance)`
	if !namesBadAddress(good, "alice.ghost") {
		t.Error("a reason with the address and the rule must pass")
	}
	for _, bad := range []string{"", "mash: not ready", "No endpoint is chosen for this session.", `address "alice.ghost" is bad`, "has 2 dot-separated parts"} {
		if namesBadAddress(bad, "alice.ghost") {
			t.Errorf("%q must fail", bad)
		}
	}
}
