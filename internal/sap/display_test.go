package sap_test

import (
	"testing"

	"github.com/bogworks/mash/internal/sap"
)

func TestDisplay(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"bogworks_x_sap-go-sdk", "bogworks.x.sap-go-sdk"},
		{"a_b_c", "a.b.c"},
		{"team-a", "team-a"},   // not three tokens
		{"a_b", "a_b"},         // not three tokens
		{"a_b_c_d", "a_b_c_d"}, // not three tokens
		{"A_b_c", "A_b_c"},     // an invalid token
		{"", ""},
		{"bogworks.x.sap-go-sdk", "bogworks.x.sap-go-sdk"}, // already canonical
	} {
		if got := sap.Display(c.in); got != c.want {
			t.Errorf("Display(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestKeyOf(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"bogworks.x.sap-go-sdk", "bogworks_x_sap-go-sdk"}, // canonical -> flat
		{"bogworks_x_sap-go-sdk", "bogworks_x_sap-go-sdk"}, // flat stays
		{"team-a", "team-a"}, // a bare name stays
		{"a.b", "a.b"},       // anything else passes through for the caller's refusal
		{"A.b.c", "A.b.c"},
		{"", ""},
	} {
		if got := sap.KeyOf(c.in); got != c.want {
			t.Errorf("KeyOf(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
