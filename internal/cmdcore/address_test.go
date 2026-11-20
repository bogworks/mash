package cmdcore_test

import (
	"errors"
	"testing"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore"
)

func TestParseAddress(t *testing.T) {
	for _, tc := range []struct{ in, ok, want string }{
		{"acme.demo.alice", "acme.demo.alice", ""},
		{"a1.b-2.c3", "a1.b-2.c3", ""},
		{"acme_demo_alice", "", "acme_demo_alice is not an address; write it as acme.demo.alice"},
		{"acme/demo/alice", "", "acme/demo/alice is not an address; write it as acme.demo.alice"},
		{"Acme.Demo.Alice", "", "Acme.Demo.Alice is not an address; write it as acme.demo.alice"},
		{"alice", "", "alice is not an address; write it as owner.group.name"},
		{"demo.alice", "", "demo.alice is not an address; write it as owner.group.name"},
		{"a.b.c.d", "", "a.b.c.d is not an address; write it as owner.group.name"},
		{"a..c", "", "a..c is not an address; write it as owner.group.name"},
		{"", "", " is not an address; write it as owner.group.name"},
		{"acme.demo.*", "", "acme.demo.* is not an address; write it as owner.group.name"},
	} {
		got, err := cmdcore.ParseAddress(tc.in, "mash send --help")
		if tc.want == "" {
			if err != nil || got != tc.ok {
				t.Errorf("%q: %q, %v", tc.in, got, err)
			}
			continue
		}
		var ce *cliframe.Error
		if !errors.As(err, &ce) || ce.Msg != tc.want || ce.Hint != "mash send --help" || ce.Code != 2 || got != "" {
			t.Errorf("%q: %q, %#v; want usage error %q", tc.in, got, err, tc.want)
		}
	}
	if _, err := cmdcore.ParseAddress("x", ""); err == nil || err.(*cliframe.Error).Hint != "" {
		t.Errorf("no hint wanted: %v", err)
	}
}
