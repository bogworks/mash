package admin_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
)

func TestCheckIssuersKept(t *testing.T) {
	const acct, scoped, plain = "AACCT", "ASCOPED", "APLAIN"
	kept := map[string]bool{scoped: true}

	// the account key and a kept scoped key are fine for the admin user
	for _, iss := range []string{acct, scoped} {
		if err := admin.CheckIssuersKept(acct, "UADMIN", iss, kept); err != nil {
			t.Errorf("admin under a kept key %s refused: %v", iss, err)
		}
	}

	// the admin user under a key that does not stay is refused, named with its key
	err := admin.CheckIssuersKept(acct, "UADMIN", plain, kept)
	var nk *admin.IssuersNotKeptError
	if !errors.As(err, &nk) {
		t.Fatalf("err = %v", err)
	}
	if len(nk.Problems) != 1 || nk.Problems[0] != (admin.IssuerProblem{Role: "admin", User: "UADMIN", Issuer: plain}) {
		t.Errorf("problems = %+v", nk.Problems)
	}
	for _, want := range []string{"UADMIN", plain, "reissue it under the account key first"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q is not in %q", want, err)
		}
	}

	// an issuer that is not known is not known to stay
	if err := admin.CheckIssuersKept(acct, "UADMIN", "", kept); err == nil {
		t.Error("an empty admin issuer passed")
	}
}

// IssuedBucketProof is true for the bucket as mash makes it and false when the history marker is missing or the bucket is
// not hardened.
func TestIssuedBucketProof(t *testing.T) {
	ctx := context.Background()
	_, a, js := setup(t)
	if ok, why, err := a.IssuedBucketProof(ctx); err != nil || !ok {
		t.Fatalf("a bucket as mash makes it: %v %q %v", ok, why, err)
	}
	setIssuedStream(t, js, store.IssuedHistory, "raised:8:5")
	if ok, _, err := a.IssuedBucketProof(ctx); err != nil || !ok {
		t.Errorf("raised marker: %v %v", ok, err)
	}
	setIssuedStream(t, js, store.IssuedHistory, "")
	if ok, why, err := a.IssuedBucketProof(ctx); err != nil || ok || !strings.Contains(why, store.IssuedMarkerKey) {
		t.Errorf("no marker: %v %q %v", ok, why, err)
	}
	setIssuedStream(t, js, store.IssuedHistory, "bogus")
	if ok, why, err := a.IssuedBucketProof(ctx); err != nil || ok || why == "" {
		t.Errorf("unreadable marker: %v %q %v", ok, why, err)
	}
}

func TestIssuedBucketProofWithNoBucket(t *testing.T) {
	ctx := context.Background()
	_, a, js := setup(t)
	if err := js.DeleteStream(ctx, store.IssuedStreamName); err != nil {
		t.Fatal(err)
	}
	if ok, why, err := a.IssuedBucketProof(ctx); err != nil || ok || !strings.Contains(why, "no issue record") {
		t.Errorf("no bucket: %v %q %v", ok, why, err)
	}
	// a bucket that allows deletes or purges proves nothing, whatever its marker says
	for name, mod := range map[string]func(*jetstream.StreamConfig){
		"deleting": func(c *jetstream.StreamConfig) { c.DenyDelete = false },
		"purging":  func(c *jetstream.StreamConfig) { c.DenyPurge = false },
	} {
		sc := jetstream.StreamConfig{Name: store.IssuedStreamName, Subjects: []string{"$KV." + store.IssuedBucket + ".>"},
			MaxMsgsPerSubject: store.IssuedHistory, Storage: jetstream.FileStorage, DenyDelete: true, DenyPurge: true,
			Discard: jetstream.DiscardNew, MaxBytes: store.IssuedMaxBytes, AllowDirect: true,
			Metadata: map[string]string{store.IssuedMarkerKey: store.IssuedMarkerFull}}
		mod(&sc)
		if _, err := js.CreateStream(ctx, sc); err != nil {
			t.Fatal(err)
		}
		if ok, why, err := a.IssuedBucketProof(ctx); err != nil || ok || !strings.Contains(why, name) {
			t.Errorf("%s allowed: %v %q %v", name, ok, why, err)
		}
		if err := js.DeleteStream(ctx, store.IssuedStreamName); err != nil {
			t.Fatal(err)
		}
	}
}
