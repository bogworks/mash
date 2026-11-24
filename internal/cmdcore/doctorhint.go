package cmdcore

import "strings"

// CheckOnlyCommands are the commands of ours that only look at things. A doctor's hint is a command that fixes the
// problem it found, so none of these is ever one (REQ-006, #1099).
var CheckOnlyCommands = []string{"doctor", "status", "who", "version", "inbox", "read", "list", "show", "check"}

// CheckOnly reports whether hint is a command that only checks: "mash doctor", "mashctl doctor --audit-users", "mash
// status --endpoint x". The command's name is the word after the program (mash or mashctl).
func CheckOnly(hint string) bool {
	f := strings.Fields(hint)
	if len(f) < 2 || (f[0] != "mash" && f[0] != "mashctl") {
		return false
	}
	for _, c := range CheckOnlyCommands {
		if f[1] == c {
			return true
		}
	}
	return false
}

// DoctorHint is the one hint a doctor ends with. problemFixes and warnFixes hold the command that fixes each problem row
// and each warning row, each in the order the rows are reported, with "" for a row no command of ours fixes.
//
// With a problem the hint is the first problem's command: that is the thing to mend first, and a later row's command (or
// a warning's) is never offered in its place. It is "" when that row has no command or when the command only checks
// (CheckOnly). With no problem the hint is the first warning that has a command that fixes it; a warning with no
// command, or one whose command only checks, is passed over. A hint is a command that fixes the problem, or there is
// none, and the row's own words say what is wrong.
func DoctorHint(problemFixes, warnFixes []string) string {
	if len(problemFixes) > 0 {
		if problemFixes[0] == "" || CheckOnly(problemFixes[0]) {
			return ""
		}
		return problemFixes[0]
	}
	for _, f := range warnFixes {
		if f != "" && !CheckOnly(f) {
			return f
		}
	}
	return ""
}

// DoctorExitCode is the exit code of a doctor: 1 when it found a problem, 0 otherwise. A warning alone is 0, or 1 when
// strict is set.
func DoctorExitCode(problems, warnings int, strict bool) int {
	if problems > 0 || (strict && warnings > 0) {
		return 1
	}
	return 0
}
