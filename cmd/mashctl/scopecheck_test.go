package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctlkeys"
)

// Every test in the package runs against a verifier that accepts: the real one needs system account credentials.
func init() {
	verifyZoneScope = func(context.Context, config.Config, func(string) string, *ctlkeys.Authority, string, string) error {
		return nil
	}
}

// stubScope replaces verifyZoneScope for the test, counts its calls and restores it.
func stubScope(t *testing.T, err error) *int {
	t.Helper()
	old := verifyZoneScope
	calls := new(int)
	verifyZoneScope = func(context.Context, config.Config, func(string) string, *ctlkeys.Authority, string, string) error {
		*calls++
		return err
	}
	t.Cleanup(func() { verifyZoneScope = old })
	return calls
}

func TestAddExistingZoneRefusedWhenScopeUnverified(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seed("a")
	calls := stubScope(t, errors.New("the signing key of zone red is not published in the account JWT as a scoped signer: run `mashctl policy apply`"))
	before, _ := os.ReadFile(h.policy)
	recs := h.records()
	out, errb, code := h.run("add", "n", "--label", "zone=red")
	if code != 1 || out != "" || !strings.Contains(errb, "zone red") || !strings.Contains(errb, "mashctl policy apply") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
	if *calls != 1 {
		t.Errorf("verifier called %d times", *calls)
	}
	if _, err := os.Stat(h.creds("n")); !os.IsNotExist(err) {
		t.Error("creds written")
	}
	if !slices.Equal(recs, h.records()) {
		t.Errorf("directory changed: %v -> %v", recs, h.records())
	}
	if after, _ := os.ReadFile(h.policy); !bytes.Equal(before, after) {
		t.Error("policy changed")
	}
}

func TestAddExistingZoneVerifiedSucceeds(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seed("a")
	calls := stubScope(t, nil)
	out, errb, code := h.run("add", "n", "--label", "zone=red")
	if code != 0 || !strings.HasPrefix(out, "added n") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if *calls != 1 {
		t.Errorf("verifier called %d times", *calls)
	}
}

func TestAddNewZoneSkipsScopeCheck(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	calls := stubScope(t, errors.New("must not be called"))
	out, errb, code := h.run("add", "g", "--label", "zone=green")
	if code != 0 {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if *calls != 0 {
		t.Errorf("verifier called %d times for a new zone", *calls)
	}
}

func TestAddNewZoneWithLeftoverKeyRefusedWhenScopeUnverified(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	if _, err := h.keys().ZoneKey("green", true); err != nil { // left by an earlier failed add
		t.Fatal(err)
	}
	calls := stubScope(t, errors.New("the signing key of zone green is not published in the account JWT as a scoped signer: run `mashctl policy apply`"))
	recs := h.records()
	out, errb, code := h.run("add", "g", "--label", "zone=green")
	if code != 1 || out != "" || !strings.Contains(errb, "zone green") || !strings.Contains(errb, "mashctl policy apply") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
	if *calls != 1 {
		t.Errorf("verifier called %d times", *calls)
	}
	if _, err := os.Stat(h.creds("g")); !os.IsNotExist(err) {
		t.Error("creds written")
	}
	if !slices.Equal(recs, h.records()) {
		t.Errorf("directory changed: %v -> %v", recs, h.records())
	}
}

func TestLiveVerifierNeedsSysCreds(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	err := verifyZoneScopeLive(context.Background(), config.Config{}, func(string) string { return "" }, h.keys(), "red", "A")
	if err == nil || !strings.Contains(err.Error(), "MASH_SYS_CREDS") || !strings.Contains(err.Error(), "verify the zone key's scope") {
		t.Fatalf("%v", err)
	}
}

func TestCheckZoneScope(t *testing.T) {
	op, _ := nkeys.CreateOperator()
	opPub, _ := op.PublicKey()
	acct, _ := nkeys.CreateAccount()
	acctPub, _ := acct.PublicKey()
	scoped, _ := nkeys.CreateAccount()
	scopedPub, _ := scoped.PublicKey()
	plain, _ := nkeys.CreateAccount()
	plainPub, _ := plain.PublicKey()
	missing, _ := nkeys.CreateAccount()
	missingPub, _ := missing.PublicKey()

	ac := jwt.NewAccountClaims(acctPub)
	us := jwt.NewUserScope()
	us.Key = scopedPub
	us.Role = "red"
	ac.SigningKeys.AddScopedSigner(us)
	ac.SigningKeys.Add(plainPub)
	tok, err := ac.Encode(op)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := apply.NewTrust(opPub, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkZoneScope(tok, trust, acctPub, "red", scopedPub); err != nil {
		t.Errorf("scoped signer refused: %v", err)
	}
	for name, pub := range map[string]string{"plain": plainPub, "absent": missingPub} {
		err := checkZoneScope(tok, trust, acctPub, "red", pub)
		if err == nil || !strings.Contains(err.Error(), "zone red") || !strings.Contains(err.Error(), "mashctl policy apply") {
			t.Errorf("%s key: %v", name, err)
		}
	}
	if err := checkZoneScope(tok, trust, missingPub, "red", scopedPub); err == nil {
		t.Error("a JWT for another account was accepted")
	}
}

func TestCheckZoneScopeAll(t *testing.T) {
	op, _ := nkeys.CreateOperator()
	opPub, _ := op.PublicKey()
	acct, _ := nkeys.CreateAccount()
	acctPub, _ := acct.PublicKey()
	zone, _ := nkeys.CreateAccount()
	zonePub, _ := zone.PublicKey()
	enc := func(scoped bool) string {
		ac := jwt.NewAccountClaims(acctPub)
		if scoped {
			us := jwt.NewUserScope()
			us.Key = zonePub
			us.Role = "red"
			ac.SigningKeys.AddScopedSigner(us)
		} else {
			ac.SigningKeys.Add(zonePub)
		}
		tok, err := ac.Encode(op)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	trust, err := apply.NewTrust(opPub, "")
	if err != nil {
		t.Fatal(err)
	}
	s, p := enc(true), enc(false)
	if err := checkZoneScopeAll([]string{s, s, s}, trust, acctPub, "red", zonePub); err != nil {
		t.Errorf("all scoped refused: %v", err)
	}
	err = checkZoneScopeAll([]string{s, p, s}, trust, acctPub, "red", zonePub)
	if err == nil || !strings.Contains(err.Error(), "reply 2 of 3") {
		t.Errorf("one plain among scoped: %v", err)
	}
	if err := checkZoneScopeAll(nil, trust, acctPub, "red", zonePub); err == nil {
		t.Error("empty list accepted")
	}
}
