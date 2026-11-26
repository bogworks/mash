package sap_test

import (
	"testing"

	"github.com/bogworks/mash/internal/sap"
)

func TestDisplay(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"acme_dev_build-bot", "acme.dev.build-bot"},
		{"a_b_c", "a.b.c"},
		{"team-a", "team-a"},   // not three tokens
		{"a_b", "a_b"},         // not three tokens
		{"a_b_c_d", "a_b_c_d"}, // not three tokens
		{"A_b_c", "A_b_c"},     // an invalid token
		{"", ""},
		{"acme.dev.build-bot", "acme.dev.build-bot"}, // already canonical
	} {
		if got := sap.Display(c.in); got != c.want {
			t.Errorf("Display(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestKeyOf(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"acme.dev.build-bot", "acme_dev_build-bot"}, // canonical -> flat
		{"acme_dev_build-bot", "acme_dev_build-bot"}, // flat stays
		{"team-a", "team-a"},                         // a bare name stays
		{"a.b", "a.b"},                               // anything else passes through for the caller's refusal
		{"A.b.c", "A.b.c"},
		{"", ""},
	} {
		if got := sap.KeyOf(c.in); got != c.want {
			t.Errorf("KeyOf(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
