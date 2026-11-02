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

func TestImportOperatorAndAccount(t *testing.T) {
	src := t.TempDir()
	write := func(name string, kp nkeys.KeyPair, mode os.FileMode) (string, string) {
		seed, _ := kp.Seed()
		p := filepath.Join(src, name)
		if err := os.WriteFile(p, append(seed, '\n'), mode); err != nil {
			t.Fatal(err)
		}
		pub, _ := kp.PublicKey()
		return p, pub
	}
	opKP, _ := nkeys.CreateOperator()
	acKP, _ := nkeys.CreateAccount()
	opPath, opPub := write("op", opKP, 0o600)
	acPath, acPub := write("ac", acKP, 0o600)

	dir := filepath.Join(t.TempDir(), "keys")
	a, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if pub, created, err := a.ImportOperator(opPath); err != nil || !created || pub != opPub {
		t.Fatalf("ImportOperator: %q %v %v", pub, created, err)
	}
	if pub, created, err := a.ImportAccount(acPath); err != nil || !created || pub != acPub {
		t.Fatalf("ImportAccount: %q %v %v", pub, created, err)
	}
	for _, n := range []string{"operator.nk", "account.nk"} {
		if fi, err := os.Stat(filepath.Join(dir, n)); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", n, fi, err)
		}
	}
	if _, pub, err := a.Operator(); err != nil || pub != opPub {
		t.Fatalf("Operator after import: %q %v", pub, err)
	}
	// The same key again is fine; a different one is refused and the stored key stays.
	if _, created, err := a.ImportOperator(opPath); err != nil || created {
		t.Fatalf("second import: %v %v", created, err)
	}
	otherKP, _ := nkeys.CreateOperator()
	otherPath, _ := write("op2", otherKP, 0o600)
	if _, _, err := a.ImportOperator(otherPath); !errors.Is(err, ErrDifferentKey) {
		t.Fatalf("different key: %v", err)
	}
	if _, pub, _ := a.Operator(); pub != opPub {
		t.Fatal("the stored operator key changed")
	}
	// The wrong prefix, and a seed file other users can read, are refused.
	if _, _, err := a.ImportOperator(acPath); err == nil {
		t.Fatal("an account seed was imported as the operator key")
	}
	if _, _, err := a.ImportAccount(opPath); err == nil {
		t.Fatal("an operator seed was imported as the account key")
	}
	openPath, _ := write("open", acKP, 0o644)
	if _, _, err := a.ImportAccount(openPath); err == nil {
		t.Fatal("a world-readable seed file was accepted")
	}
}

func TestInstallFileNoClobber(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if created, err := InstallFile(p, []byte("one")); err != nil || !created {
		t.Fatalf("%v %v", created, err)
	}
	if created, err := InstallFile(p, []byte("two")); err != nil || created {
		t.Fatalf("%v %v", created, err)
	}
	if b, _ := os.ReadFile(p); string(b) != "one" {
		t.Fatalf("clobbered: %q", b)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if ents, _ := os.ReadDir(filepath.Dir(p)); len(ents) != 1 {
		t.Fatalf("temp files left: %v", ents)
	}
}
