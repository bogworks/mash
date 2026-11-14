package ctlkeys

import (
	"os"
	"testing"
)

// HeldKeys lists the operator, system and account public keys with what each is; a store without an account key
// is an error (fail closed), one without an operator or system key is not.
func TestHeldKeys(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	a, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.HeldKeys(); err == nil {
		t.Fatal("a store with no account key must be an error")
	}
	acct, _, err := a.InitAccount()
	if err != nil {
		t.Fatal(err)
	}
	if got, held, err := a.HeldKeys(); err != nil || got != acct || len(held) != 1 {
		t.Fatalf("account only: %q %v %v", got, held, err)
	}
	op, _, _ := a.InitOperator()
	sys, _, _ := a.InitSystem()
	_, held, err := a.HeldKeys()
	if err != nil || len(held) != 3 {
		t.Fatalf("%v %v", held, err)
	}
	for _, k := range []string{op, sys, acct} {
		if held[k] == "" {
			t.Errorf("a held key is missing: %q", k[:8])
		}
	}
}
