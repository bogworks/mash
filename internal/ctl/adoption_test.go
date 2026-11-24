package ctl

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctlkeys"
)

// HeldPublicKeys includes the account signing key, so a planned rotation's next submitter cannot be it.
func TestHeldPublicKeysIncludesTheSigningKey(t *testing.T) {
	dir := t.TempDir()
	ks, err := ctlkeys.Open(filepath.Join(dir, "keys"), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ks.InitAccount(); err != nil {
		t.Fatal(err)
	}
	sk, _ := nkeys.CreateAccount()
	seed, _ := sk.Seed()
	sp := filepath.Join(dir, "signing.seed")
	if err := os.WriteFile(sp, append(seed, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	spub, _, err := ks.ImportSigning(sp)
	if err != nil {
		t.Fatal(err)
	}
	ukp, _ := nkeys.CreateUser()
	upub, _ := ukp.PublicKey()
	tok, err := jwt.NewUserClaims(upub).Encode(sk)
	if err != nil {
		t.Fatal(err)
	}
	useed, _ := ukp.Seed()
	creds, err := jwt.FormatUserConfig(tok, useed)
	if err != nil {
		t.Fatal(err)
	}
	cp := filepath.Join(dir, "admin.creds")
	if err := os.WriteFile(cp, creds, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := HeldPublicKeys(config.Config{AdminCreds: cp}, ks)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, spub) || !slices.Contains(got, upub) {
		t.Errorf("held keys %v lack the signing key or the admin user", got)
	}
}
