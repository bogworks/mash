package cmds

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSeveralTextCutsFirstIssueToTheLine(t *testing.T) {
	long := strings.Repeat("x", 120)
	r := DoctorResult{
		Problems: 1,
		hint:     "mash doctor --endpoint acme.dev.bob",
		addrs:    []string{"acme.dev.alice", "acme.dev.bob"},
		several: []DoctorResult{
			{},
			{Problems: 1, Checks: []DoctorCheck{{Name: "config", State: CheckOK}, {Name: "delivery", State: CheckFail, Detail: &long}}},
		},
	}
	out := r.severalText()
	lines := strings.Split(out, "\n")
	if lines[0] != "Endpoints installed here: 2." || !strings.HasSuffix(out, "\n\n1 problem. See it: mash doctor --endpoint acme.dev.bob") {
		t.Fatalf("text:\n%s", out)
	}
	for _, l := range lines {
		if utf8.RuneCountInString(l) > 79 {
			t.Errorf("over 79 columns: %q", l)
		}
	}
	if !strings.Contains(lines[4], "delivery: xxx") || !strings.HasSuffix(lines[4], "…") || strings.HasSuffix(lines[3], " ") {
		t.Errorf("rows: %q %q", lines[3], lines[4])
	}
}

func TestSeveralFirstIssueFallsBackToFirstWarning(t *testing.T) {
	d1, d2 := "late", "bad"
	e := DoctorResult{Warnings: 1, Checks: []DoctorCheck{{Name: "refused", State: CheckWarn, Detail: &d1}}}
	if got := e.firstIssue(); got != "refused: late" {
		t.Errorf("warning only: %q", got)
	}
	e.Checks = append(e.Checks, DoctorCheck{Name: "sign-in", State: CheckFail, Detail: &d2})
	if got := e.firstIssue(); got != "sign-in: bad" {
		t.Errorf("failure wins: %q", got)
	}
}

func TestSeveralHintIsInTheTextOnly(t *testing.T) {
	r := DoctorResult{hint: "mash doctor --endpoint a", several: []DoctorResult{{}}}
	if r.Hint() != "" {
		t.Errorf("several: Hint() = %q", r.Hint())
	}
	if (DoctorResult{hint: "x"}).Hint() != "x" {
		t.Error("single endpoint keeps its hint")
	}
}

// A 64-character address leaves no room for the issue on its row: the address is never cut, and the issue goes on the
// next line, 4 spaces in (the ISSUE column is too far right to hold it).
func TestSeveralTextLongAddressKeepsItAndMovesTheIssue(t *testing.T) {
	addr := strings.Repeat("a", 20) + "." + strings.Repeat("b", 20) + "." + strings.Repeat("c", 22) // 64 characters
	if len(addr) != 64 {
		t.Fatal(len(addr))
	}
	detail := "the server refused these credentials"
	r := DoctorResult{
		Problems: 1,
		hint:     "mash doctor --endpoint " + addr,
		addrs:    []string{addr, "acme.dev.bob"},
		several: []DoctorResult{
			{Problems: 1, Checks: []DoctorCheck{{Name: "sign-in", State: CheckFail, Detail: &detail}}},
			{Problems: 1, Checks: []DoctorCheck{{Name: "sign-in", State: CheckFail, Detail: &detail}}},
		},
	}
	lines := strings.Split(r.severalText(), "\n")
	// lines: title, blank, header, its FIRST ISSUE, row 1, issue 1, row 2, issue 2, ...
	if !strings.HasPrefix(lines[4], addr+"  ") || strings.Contains(lines[4], "sign-in") {
		t.Errorf("row with the long address: %q", lines[4])
	}
	if lines[5] != "    sign-in: the server refused these credentials" {
		t.Errorf("issue line: %q", lines[5])
	}
	if !strings.HasPrefix(lines[6], "acme.dev.bob") || lines[7] != lines[5] {
		t.Errorf("the short address row shares the table's width, so its issue moves too: %q / %q", lines[6], lines[7])
	}
}
