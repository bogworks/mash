//go:build unix

package ctlkeys

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nats-io/nkeys"
)

func TestOperatorAndSystemKeys(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")
	a, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Operator(); !errors.Is(err, ErrNoOperatorKey) {
		t.Fatalf("Operator before init: %v", err)
	}
	if _, _, err := a.System(); !errors.Is(err, ErrNoSystemKey) {
		t.Fatalf("System before init: %v", err)
	}
	opPub, created, err := a.InitOperator()
	if err != nil || !created || !nkeys.IsValidPublicOperatorKey(opPub) {
		t.Fatalf("InitOperator: %q %v %v", opPub, created, err)
	}
	sysPub, created, err := a.InitSystem()
	if err != nil || !created || !nkeys.IsValidPublicAccountKey(sysPub) {
		t.Fatalf("InitSystem: %q %v %v", sysPub, created, err)
	}
	for _, n := range []string{"operator.nk", "system.nk"} {
		if fi, err := os.Stat(filepath.Join(dir, n)); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", n, fi, err)
		}
	}
	// No clobber: a second init reports the same key and creates nothing.
	if pub, created, err := a.InitOperator(); err != nil || created || pub != opPub {
		t.Fatalf("second InitOperator: %q %v %v", pub, created, err)
	}
	kp, pub, err := a.Operator()
	if err != nil || pub != opPub {
		t.Fatalf("Operator: %v", err)
	}
	kp.Wipe()
	// The file holds a seed of the other kind: refused, not misread.
	b, _ := os.ReadFile(filepath.Join(dir, "system.nk"))
	if err := os.Remove(filepath.Join(dir, "operator.nk")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "operator.nk"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Operator(); err == nil {
		t.Fatal("an account seed was accepted as the operator key")
	}
}
