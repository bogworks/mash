package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
)

// At a shell, mash status --detail and mash doctor show the skipped stored-mail scan when the scan is not done and the
// state directory is not writable, with no record to read (it would be kept in that directory). The access check is
// replaced: a chmod does not block a root test.
func TestStatusDoctorShowASkippedStoredMailScan(t *testing.T) {
	w := newWorld(t, "a", "b")
	sp := spoolFor(w, "a")
	if err := os.MkdirAll(sp.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := endpoint.DirAccessible
	t.Cleanup(func() { endpoint.DirAccessible = old })
	writable := true
	endpoint.DirAccessible = func(string) bool { return writable }
	dir := config.Tilde(sp.Dir)
	text := "stored mail:    not checked: " + dir + " is not writable"
	row := "stored mail  skip  not checked: " + dir + " is not writable"
	storedMail := func() json.RawMessage {
		out, _ := w.cli(t, "a", "status", "--detail", "--json")
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(out), &m); err != nil {
			t.Fatalf("%q: %v", out, err)
		}
		got, ok := m["stored_mail"]
		if !ok {
			t.Fatalf("no stored_mail field: %s", out)
		}
		return got
	}
	doctorJSON := func() (detail string, found bool) {
		out, _ := w.cli(t, "a", "doctor", "--json")
		var r struct {
			Checks []struct{ Name, State, Detail string }
		}
		if err := json.Unmarshal([]byte(out), &r); err != nil {
			t.Fatalf("%q: %v", out, err)
		}
		for _, c := range r.Checks {
			if c.Name == "stored mail" {
				return c.State + " " + c.Detail, true
			}
		}
		return "", false
	}
	// Writable and not scanned: no skip row.
	if got := string(storedMail()); got != "null" {
		t.Fatalf("writable: %s", got)
	}
	if out, _ := w.cli(t, "a", "status", "--detail"); strings.Contains(out, "not checked") {
		t.Errorf("writable, status --detail:\n%s", out)
	}
	if out, _ := w.cli(t, "a", "doctor"); strings.Contains(out, "not checked") {
		t.Errorf("writable, doctor:\n%s", out)
	}
	if d, ok := doctorJSON(); ok && strings.Contains(d, "not checked") {
		t.Errorf("writable, doctor json: %s", d)
	}
	// Unwritable and not scanned: the skip row everywhere.
	writable = false
	var sm struct {
		Checked bool
		Dir     string
		Reason  string
	}
	if err := json.Unmarshal(storedMail(), &sm); err != nil || sm.Checked || sm.Dir != dir || sm.Reason != "state directory not writable" {
		t.Errorf("skipped: %+v %v", sm, err)
	}
	if out, _ := w.cli(t, "a", "status", "--detail"); !strings.Contains(out, text) {
		t.Errorf("status --detail:\n%s", out)
	}
	if out, _ := w.cli(t, "a", "doctor"); !strings.Contains(out, row) {
		t.Errorf("doctor:\n%s", out)
	}
	if d, ok := doctorJSON(); !ok || d != "skip not checked: "+dir+" is not writable" {
		t.Errorf("doctor json: %q %v", d, ok)
	}
	// Scan done: no row even though the directory is not writable.
	if err := os.WriteFile(filepath.Join(sp.Dir, "refused-malformed.seqs"), []byte("stream x\nscanned 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := string(storedMail()); got != "null" {
		t.Errorf("scan done: %s", got)
	}
	if out, _ := w.cli(t, "a", "doctor"); strings.Contains(out, "stored mail") {
		t.Errorf("doctor after the scan:\n%s", out)
	}
}
