package store

import "testing"

// KeyOrName is the directory key for a record ParseRecord accepted and the name for a bare fixture.
func TestKeyOrName(t *testing.T) {
	l := legacyRec("acme", "", "ops1")
	n := newRec("acme", "team1", "ops1")
	if l.KeyOrName() != "ops1" || l.KeyOrName() != l.Key() {
		t.Fatalf("legacy: %q", l.KeyOrName())
	}
	if n.KeyOrName() != n.Key() || n.KeyOrName() == "ops1" || n.KeyOrName() == "" {
		t.Fatalf("new form: %q", n.KeyOrName())
	}
	if got := (Record{Name: "bare"}).KeyOrName(); got != "bare" {
		t.Fatalf("incomplete record: %q", got)
	}
}
