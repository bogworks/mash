package cmdcore_test

import (
	"testing"

	"github.com/bogworks/mash/internal/cmdcore"
)

func TestDoctorHintIsTheFirstProblemsCommandOrNone(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fixes []string
		want  string
	}{
		{"no problem", nil, ""},
		{"a command", []string{"mash join <file>"}, "mash join <file>"},
		{"the first problem only", []string{"mash join <file>", "mash x"}, "mash join <file>"},
		{"a first problem with no command gives none", []string{"", "mash join <file>"}, ""},
		{"a check is never a hint", []string{"mash doctor"}, ""},
		{"status is a check", []string{"mash status --endpoint a.b.c"}, ""},
		{"mashctl doctor is a check", []string{"mashctl doctor --audit-users"}, ""},
	} {
		if got := cmdcore.DoctorHint(tc.fixes); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestDoctorExitCode(t *testing.T) {
	for _, tc := range []struct {
		p, w   int
		strict bool
		want   int
	}{{0, 0, false, 0}, {1, 0, false, 1}, {2, 3, false, 1}, {0, 1, false, 0}, {0, 1, true, 1}, {0, 0, true, 0}} {
		if got := cmdcore.DoctorExitCode(tc.p, tc.w, tc.strict); got != tc.want {
			t.Errorf("%+v: %d", tc, got)
		}
	}
}

func TestCheckOnly(t *testing.T) {
	for hint, want := range map[string]bool{"mash doctor": true, "mash status": true, "mash who": true, "mash join <file>": false,
		"claude plugin install mash@mash": false, "mashctl endpoint reissue a.b.c": false, "": false} {
		if got := cmdcore.CheckOnly(hint); got != want {
			t.Errorf("%q: %v", hint, got)
		}
	}
}
