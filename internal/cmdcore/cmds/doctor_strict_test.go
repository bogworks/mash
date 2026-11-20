package cmds

import "testing"

// --strict turns a warning into exit 1; without it a warning alone is 0.
func TestDoctorStrictExitCode(t *testing.T) {
	for _, tc := range []struct {
		r    DoctorResult
		want int
	}{
		{DoctorResult{}, 0},
		{DoctorResult{Warnings: 1}, 0},
		{DoctorResult{Warnings: 1, strict: true}, 1},
		{DoctorResult{Problems: 1}, 1},
		{DoctorResult{strict: true}, 0},
	} {
		if got := tc.r.ExitCode(); got != tc.want {
			t.Errorf("%+v: exit %d, want %d", tc.r, got, tc.want)
		}
	}
}
