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
	const acct, scoped, plain, plain2 = "AACCT", "ASCOPED", "APLAIN", "APLAIN2"
	kept := map[string]bool{scoped: true}

	// the account key and a kept scoped key are fine, for the admin user and for an exempt user
	if err := admin.CheckIssuersKept(acct, "UADMIN", acct, map[string][]string{"USVC": {scoped}, "USVC2": {acct}}, kept); err != nil {
		t.Errorf("kept issuers refused: %v", err)
	}
	// nothing connected is exempt: only the admin is checked
	if err := admin.CheckIssuersKept(acct, "UADMIN", scoped, nil, kept); err != nil {
		t.Errorf("admin under a kept key refused: %v", err)
	}

	// the admin user and an exempt user under keys that do not stay are refused, each named with its key
	err := admin.CheckIssuersKept(acct, "UADMIN", plain, map[string][]string{"USVC": {plain2}, "UOK": {acct}}, kept)
	var nk *admin.IssuersNotKeptError
	if !errors.As(err, &nk) {
		t.Fatalf("err = %v", err)
	}
	if len(nk.Problems) != 2 || nk.Problems[0] != (admin.IssuerProblem{Role: "admin", User: "UADMIN", Issuer: plain}) ||
		nk.Problems[1] != (admin.IssuerProblem{Role: "exempt", User: "USVC", Issuer: plain2}) {
		t.Errorf("problems = %+v", nk.Problems)
	}
	for _, want := range []string{"UADMIN", plain, "USVC", plain2, "reissue them under the account key first"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q is not in %q", want, err)
		}
	}
	if strings.Contains(err.Error(), "UOK") {
		t.Errorf("a user under the account key is named: %v", err)
	}

	// two connections of one exempt user, one issuer kept and one not: refused, naming the one that leaves
	err = admin.CheckIssuersKept(acct, "UADMIN", acct, map[string][]string{"USVC": {scoped, plain}}, kept)
	if !errors.As(err, &nk) || len(nk.Problems) != 1 || nk.Problems[0] != (admin.IssuerProblem{Role: "exempt", User: "USVC", Issuer: plain}) {
		t.Errorf("a user with one kept and one removed issuer: %v", err)
	}

	// an issuer that is not known is not known to stay
	if err := admin.CheckIssuersKept(acct, "UADMIN", "", nil, kept); err == nil {
		t.Error("an empty admin issuer passed")
	}
	if err := admin.CheckIssuersKept(acct, "UADMIN", acct, map[string][]string{"USVC": {""}}, kept); err == nil {
		t.Error("an empty exempt issuer passed")
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
