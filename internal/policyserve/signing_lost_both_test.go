package policyserve

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/ctlkeys"
)

// The policy applier's view of the signing key refuses, rather than reporting none, when config.json records a signing
// key and the key store holds neither signing.nk nor signing.pub.
func TestSigningPubRefusesWhenBothSigningFilesAreGone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")
	sk, _ := nkeys.CreateAccount()
	pub, _ := sk.PublicKey()
	none, err := ctlkeys.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := SigningPub(none); err != nil || got != "" {
		t.Fatalf("no expectation: %q %v", got, err)
	}
	ks, err := ctlkeys.Open(dir, pub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SigningPub(ks); !errors.Is(err, ctlkeys.ErrSigningKeyLost) {
		t.Fatalf("err = %v, want ErrSigningKeyLost", err)
	}
}
