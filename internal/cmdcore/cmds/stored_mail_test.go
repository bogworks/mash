package cmds_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore/cmds"
)

const skipDir = "~/.local/state/mash/acme.demo.bob"

func withSkip(f *fakeHealth, dir string) *fakeHealth {
	f.rep.StoredSkipDir = dir
	return f
}

func TestStoredMailCheckRow(t *testing.T) {
	c := cmds.StoredMailCheck(skipDir)
	if c.Name != "stored mail" || c.State != cmds.CheckSkip || c.Detail != "not checked: "+skipDir+" is not writable" ||
		c.Fix != "make that folder writable, then restart the session" {
		t.Fatalf("row: %+v", c)
	}
}

func TestStatusDetailStoredMail(t *testing.T) {
	_, out, _ := runStatus(withSkip(allWell(), skipDir), cliframe.Global{Detail: true})
	line := "stored mail:    not checked: " + skipDir + " is not writable"
	if !strings.Contains(out, line) || len(line) > 80 {
		t.Errorf("no stored mail line in:\n%s", out)
	}
	_, out, _ = runStatus(allWell(), cliframe.Global{Detail: true})
	if strings.Contains(out, "stored mail") {
		t.Errorf("a stored mail line without a skip:\n%s", out)
	}
	code, out, _ := runStatus(withSkip(allWell(), skipDir), cliframe.Global{})
	if code != 0 || strings.Contains(out, "stored mail") {
		t.Errorf("the usual answer changed: %d %q", code, out)
	}
}

func TestStatusDetailJSONStoredMail(t *testing.T) {
	fields := func(f *fakeHealth) map[string]json.RawMessage {
		_, out, _ := runStatus(f, cliframe.Global{Detail: true, JSON: true})
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(out), &m); err != nil {
			t.Fatalf("%q: %v", out, err)
		}
		return m
	}
	if got, ok := fields(allWell())["stored_mail"]; !ok || string(got) != "null" {
		t.Errorf("absent skip: %q present %v", got, ok)
	}
	got := fields(withSkip(allWell(), skipDir))["stored_mail"]
	var sm map[string]any
	if err := json.Unmarshal(got, &sm); err != nil {
		t.Fatal(err)
	}
	if len(sm) != 3 || sm["checked"] != false || sm["dir"] != skipDir || sm["reason"] != "state directory not writable" {
		t.Errorf("stored_mail: %s", got)
	}
	// without --detail the object has no such field
	_, out, _ := runStatus(withSkip(allWell(), skipDir), cliframe.Global{JSON: true})
	if strings.Contains(out, "stored_mail") {
		t.Errorf("stored_mail without --detail: %s", out)
	}
}

func TestDoctorStoredMailRow(t *testing.T) {
	code, out, errs := runDoctor(withSkip(allWell(), skipDir), cliframe.Global{})
	if code != 0 || errs != "" {
		t.Errorf("a skip changed the verdict: %d %q", code, errs)
	}
	// padded to the longest name and two: 13, then the state in 6
	want := "stored mail  skip  not checked: " + skipDir + " is not writable"
	if !strings.Contains(out, want) || strings.Contains(out, "hint:") || strings.Contains(out, "make that folder") {
		t.Errorf("doctor text:\n%s", out)
	}
	if !strings.Contains(out, "config       ok\n") || !strings.Contains(out, "No problems.") {
		t.Errorf("rows are not padded alike:\n%s", out)
	}
	_, out, _ = runDoctor(allWell(), cliframe.Global{})
	if strings.Contains(out, "stored mail") || !strings.Contains(out, "config    ok\n") {
		t.Errorf("a row without a skip, or main's padding changed:\n%s", out)
	}
}

func TestDoctorJSONStoredMailRow(t *testing.T) {
	_, out, _ := runDoctor(withSkip(allWell(), skipDir), cliframe.Global{JSON: true})
	var r struct {
		Problems, Warnings int
		Checks             []map[string]any
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	for _, c := range r.Checks {
		if c["name"] == "stored mail" {
			row = c
		}
	}
	if row == nil || len(row) != 4 || row["state"] != "skip" || row["detail"] != "not checked: "+skipDir+" is not writable" ||
		row["fix"] != "make that folder writable, then restart the session" || r.Problems != 0 || r.Warnings != 0 {
		t.Errorf("row %v in %s", row, out)
	}
	_, out, _ = runDoctor(allWell(), cliframe.Global{JSON: true})
	if strings.Contains(out, "stored mail") {
		t.Errorf("a row without a skip: %s", out)
	}
}
