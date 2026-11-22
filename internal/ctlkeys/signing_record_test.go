//go:build unix

package ctlkeys

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nats-io/nkeys"
)

func signingSeed(t *testing.T, dir, name string) (string, string) {
	t.Helper()
	kp, _ := nkeys.CreateAccount()
	seed, _ := kp.Seed()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, append(seed, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	pub, _ := kp.PublicKey()
	return p, pub
}

func TestImportSigningRecordsAndLossIsDistinct(t *testing.T) {
	a, dir := newStore(t)
	if _, _, err := a.Signing(); !errors.Is(err, ErrNoSigningKey) {
		t.Fatalf("no record, no seed: %v", err)
	}
	src := t.TempDir()
	p1, pub1 := signingSeed(t, src, "s1")
	p2, _ := signingSeed(t, src, "s2")
	if pub, created, err := a.ImportSigning(p1); err != nil || !created || pub != pub1 {
		t.Fatalf("import: %v %v %v", pub, created, err)
	}
	rec := filepath.Join(dir, "signing.pub")
	if m := mode(t, rec); m != 0o600 {
		t.Fatalf("signing.pub mode %o", m)
	}
	if pub, created, err := a.ImportSigning(p1); err != nil || created || pub != pub1 {
		t.Fatalf("rerun: %v %v %v", pub, created, err)
	}
	if _, _, err := a.ImportSigning(p2); !errors.Is(err, ErrDifferentKey) {
		t.Fatalf("another key: %v", err)
	}
	if kp, pub, err := a.Signing(); err != nil || pub != pub1 {
		t.Fatalf("Signing: %v %v", pub, err)
	} else {
		kp.Wipe()
	}
	if err := os.Remove(filepath.Join(dir, "signing.nk")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Signing(); !errors.Is(err, ErrSigningKeyLost) || errors.Is(err, ErrNoSigningKey) {
		t.Fatalf("lost: %v", err)
	}
	if err := os.Rename(p2, filepath.Join(dir, "signing.nk")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Signing(); !errors.Is(err, ErrSigningKeyLost) {
		t.Fatalf("mismatch: %v", err)
	}
	os.Remove(filepath.Join(dir, "signing.nk"))
	p3, _ := signingSeed(t, src, "s3")
	if _, _, err := a.ImportSigning(p3); !errors.Is(err, ErrDifferentKey) {
		t.Fatalf("other key after loss: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "signing.nk")); err == nil {
		t.Fatal("signing.nk was written despite the refusal")
	}
	if _, created, err := a.ImportSigning(p1); err != nil || !created {
		t.Fatalf("restore: %v %v", created, err)
	}
	if _, _, err := a.Signing(); err != nil {
		t.Fatal(err)
	}
}

func TestSigningSeedWithoutRecordStillWorks(t *testing.T) {
	a, dir := newStore(t)
	p, pub := signingSeed(t, t.TempDir(), "s")
	if _, _, err := a.ImportSigning(p); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(dir, "signing.pub"))
	if kp, got, err := a.Signing(); err != nil || got != pub {
		t.Fatalf("%v %v", got, err)
	} else {
		kp.Wipe()
	}
}
