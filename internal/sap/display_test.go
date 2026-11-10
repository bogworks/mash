package sap_test

import (
	"testing"

	"github.com/bogworks/mash/internal/sap"
)

func TestDisplay(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"bogworks_x_sap-go-sdk", "bogworks.x.sap-go-sdk"},
		{"a_b_c", "a.b.c"},
		{"team-a", "team-a"},   // a legacy name is shown as it is
		{"a_b", "a_b"},         // not three tokens
		{"a_b_c_d", "a_b_c_d"}, // not three tokens
		{"cc_o_n", "cc_o_n"},   // the legacy agent token is not a new-form identity
		{"A_b_c", "A_b_c"},     // an invalid token
		{"", ""},
		{"bogworks.x.sap-go-sdk", "bogworks.x.sap-go-sdk"}, // already canonical
	} {
		if got := sap.Display(c.in); got != c.want {
			t.Errorf("Display(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseCanonical(t *testing.T) {
	id, err := sap.ParseCanonical("bogworks.x.sap-go-sdk")
	if err != nil || id.Flat() != "bogworks_x_sap-go-sdk" || id.String() != "bogworks.x.sap-go-sdk" {
		t.Fatalf("%v %v", id, err)
	}
	for _, bad := range []string{"", "a", "a.b", "a.b.c.d", "a..c", "cc.o.n", "A.b.c", "a_b_c", "a.b_c.d", "team-a"} {
		if _, err := sap.ParseCanonical(bad); err == nil {
			t.Errorf("ParseCanonical(%q) succeeded", bad)
		}
	}
}

func TestKeyOf(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"bogworks.x.sap-go-sdk", "bogworks_x_sap-go-sdk"}, // canonical -> flat
		{"bogworks_x_sap-go-sdk", "bogworks_x_sap-go-sdk"}, // flat stays
		{"team-a", "team-a"}, // legacy stays
		{"a.b", "a.b"},       // anything else passes through for the caller's refusal
		{"cc.o.n", "cc.o.n"},
		{"", ""},
	} {
		if got := sap.KeyOf(c.in); got != c.want {
			t.Errorf("KeyOf(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A legacy name never contains a dot, so a dot cannot make a canonical form ambiguous with a legacy name.
func TestLegacyNamesHaveNoDot(t *testing.T) {
	if _, err := sap.Legacy("o", "a.b"); err == nil {
		t.Fatal("a legacy name with a dot was accepted")
	}
}
