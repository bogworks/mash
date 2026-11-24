//go:build unix

package ctlkeys

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// With config.json's signing_key_pub given to Open, Signing returns the key only when signing.nk and signing.pub both
// hold exactly it; every other state is an ErrSigningKeyLost, never ErrNoSigningKey (the identity key must not stand in).
func TestSigningWithAnExpectedKey(t *testing.T) {
	a, dir := newStore(t)
	src := t.TempDir()
	p1, pub1 := signingSeed(t, src, "s1")
	_, pub2 := signingSeed(t, src, "s2")
	if _, _, err := a.ImportSigning(p1); err != nil {
		t.Fatal(err)
	}
	open := func(exp string) *Authority {
		t.Helper()
		ks, err := Open(dir, exp)
		if err != nil {
			t.Fatal(err)
		}
		return ks
	}
	lost := func(label string, err error, want ...string) {
		t.Helper()
		if !errors.Is(err, ErrSigningKeyLost) || errors.Is(err, ErrNoSigningKey) {
			t.Fatalf("%s: err = %v, want ErrSigningKeyLost and not ErrNoSigningKey", label, err)
		}
		for _, w := range want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%s: %q lacks %q", label, err, w)
			}
		}
	}

	// the expected key, both files: fine; no expectation: as before
	if kp, pub, err := open(pub1).Signing(); err != nil || pub != pub1 {
		t.Fatalf("expected key, both files: %v %v", pub, err)
	} else {
		kp.Wipe()
	}
	// the record differs from the expectation
	_, _, err := open(pub2).Signing()
	lost("record differs", err, pub1, pub2)

	// signing.pub gone, signing.nk present
	rec := filepath.Join(dir, "signing.pub")
	if err := os.Rename(rec, rec+".away"); err != nil {
		t.Fatal(err)
	}
	if kp, pub, err := open("").Signing(); err != nil || pub != pub1 {
		t.Fatalf("no expectation, nk only: %v %v", pub, err)
	} else {
		kp.Wipe()
	}
	_, _, err = open(pub1).Signing()
	lost("nk only", err, "signing.pub is missing", "mashctl init --config <file>")
	_, _, err = open(pub2).Signing()
	lost("nk only, other expectation", err, pub1, pub2)

	// signing.nk differs from the expectation (record restored)
	if err := os.Rename(rec+".away", rec); err != nil {
		t.Fatal(err)
	}
	p3, pub3 := signingSeed(t, src, "s3")
	if err := os.Remove(filepath.Join(dir, "signing.nk")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p3)
	if err := os.WriteFile(filepath.Join(dir, "signing.nk"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = open(pub1).Signing()
	lost("stored nk differs", err, pub3, pub1)

	// both gone
	for _, f := range []string{"signing.nk", "signing.pub"} {
		if err := os.Remove(filepath.Join(dir, f)); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := open("").Signing(); !errors.Is(err, ErrNoSigningKey) {
		t.Fatalf("both gone, no expectation: %v", err)
	}
	_, _, err = open(pub1).Signing()
	lost("both gone", err, pub1, "both missing", "mashctl init --config <file>")
	var lb *SigningLostBothError
	if !errors.As(err, &lb) || lb.Pub != pub1 {
		t.Fatalf("not a SigningLostBothError for %s: %v", pub1, err)
	}
}

// An expected key that is not an account public key is refused at Open.
func TestOpenRefusesAMalformedExpectedSigningKey(t *testing.T) {
	for _, bad := range []string{"nonsense", "SAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		if _, err := Open(t.TempDir(), bad); err == nil || !strings.Contains(err.Error(), "signing_key_pub") {
			t.Errorf("Open(%q) = %v", bad, err)
		}
	}
}

// Each refusal has its own type; each is an ErrSigningKeyLost, names the key folder and gives the fix as a hint line.
func TestSigningRefusalTypes(t *testing.T) {
	src := t.TempDir()
	p1, pub1 := signingSeed(t, src, "s1")
	p2, pub2 := signingSeed(t, src, "s2")
	cases := []struct {
		name  string
		setup func(t *testing.T, dir string) (expect string)
		check func(t *testing.T, err error)
	}{
		{"both missing", func(t *testing.T, dir string) string {
			rm(t, dir, "signing.nk", "signing.pub")
			return pub1
		}, func(t *testing.T, err error) {
			var e *SigningLostBothError
			if !errors.As(err, &e) || e.Pub != pub1 {
				t.Fatalf("type: %T", err)
			}
		}},
		{"signing.pub missing", func(t *testing.T, dir string) string {
			rm(t, dir, "signing.pub")
			return pub1
		}, func(t *testing.T, err error) {
			var e *SigningPubMissingError
			if !errors.As(err, &e) || e.Pub != pub1 {
				t.Fatalf("type: %T", err)
			}
		}},
		{"record differs from config.json", func(t *testing.T, dir string) string { return pub2 }, func(t *testing.T, err error) {
			var e *SigningRecordMismatchError
			if !errors.As(err, &e) || e.Expected != pub2 || e.Recorded != pub1 {
				t.Fatalf("type: %T", err)
			}
		}},
		{"signing.nk differs from the record", func(t *testing.T, dir string) string {
			b, _ := os.ReadFile(p2)
			if err := os.WriteFile(filepath.Join(dir, "signing.nk"), b, 0o600); err != nil {
				t.Fatal(err)
			}
			return pub1
		}, func(t *testing.T, err error) {
			var e *SigningKeyMismatchError
			if !errors.As(err, &e) || e.Held != pub2 || e.Want != pub1 {
				t.Fatalf("type: %T", err)
			}
		}},
		{"signing.nk differs from config.json, no record", func(t *testing.T, dir string) string {
			rm(t, dir, "signing.pub")
			return pub2
		}, func(t *testing.T, err error) {
			var e *SigningKeyMismatchError
			if !errors.As(err, &e) || e.Held != pub1 || e.Want != pub2 {
				t.Fatalf("type: %T", err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, dir := newStore(t)
			if _, _, err := a.ImportSigning(p1); err != nil {
				t.Fatal(err)
			}
			exp := c.setup(t, dir)
			ks, err := Open(dir, exp)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = ks.Signing()
			if err == nil || !errors.Is(err, ErrSigningKeyLost) || errors.Is(err, ErrNoSigningKey) {
				t.Fatalf("err = %v", err)
			}
			c.check(t, err)
			msg := err.Error()
			lines := strings.Split(msg, "\n")
			if !strings.Contains(msg, dir) || len(lines) != 2 || !strings.HasPrefix(lines[1], "  hint: ") {
				t.Errorf("text lacks the folder or the hint line: %q", msg)
			}
			t.Log(strings.ReplaceAll(msg, dir, "<dir>"))
		})
	}
}

func rm(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.Remove(filepath.Join(dir, n)); err != nil {
			t.Fatal(err)
		}
	}
}
