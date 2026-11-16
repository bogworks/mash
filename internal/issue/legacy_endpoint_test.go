package issue

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/store"
)

// fakeDirectory is the directory a legacy endpoint is checked against.
type fakeDirectory struct {
	owner         string
	legacy, found bool
	err           error
	reads         int
}

func (d *fakeDirectory) LegacyOwner(_ context.Context, _ string) (string, bool, bool, error) {
	d.reads++
	return d.owner, d.legacy, d.found, d.err
}

func (f *fixture) legacyReq(t *testing.T, address string) Request {
	t.Helper()
	r := f.req(t, address, time.Minute)
	r.Endpoint = "cc." + address
	var err error
	if r.Proof, err = SignProof(f.requester, r.Address, r.Endpoint, r.PublicKey, r.Host, r.Nonce, r.Expires); err != nil {
		t.Fatal(err)
	}
	return r
}

// A legacy endpoint is issued only for a legacy record of that name under the address's owner, and the tag is the name.
func TestIssuerIssuesALegacyTagForALegacyRecordOfTheOwner(t *testing.T) {
	f := newFixture(t)
	f.is.Directory = &fakeDirectory{owner: "c", legacy: true, found: true}
	out, err := f.is.Issue(context.Background(), f.legacyReq(t, "c.core"), store.PurposeIssue)
	if err != nil {
		t.Fatal(err)
	}
	uc, err := jwt.DecodeUserClaims(out.JWT)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"allow:c.lead", "ep:core"}; !slices.Equal([]string(uc.Tags), want) {
		t.Fatalf("tags %v, want %v", uc.Tags, want)
	}
}

// Every other directory answer, a nil directory included, is refused endpoint_mismatch and nothing is recorded, and the
// nonce is released.
func TestIssuerRefusesALegacyTagTheDirectoryDoesNotBack(t *testing.T) {
	cases := []struct {
		name string
		dir  Directory
	}{
		{"no record", &fakeDirectory{}},
		{"another owner's record", &fakeDirectory{owner: "d", legacy: true, found: true}},
		{"a new-form record", &fakeDirectory{owner: "c", legacy: false, found: true}},
		{"a read error", &fakeDirectory{err: errors.New("down"), owner: "c", legacy: true, found: true}},
		{"no directory", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.is.Directory = tc.dir
			out, err := f.is.Issue(context.Background(), f.legacyReq(t, "c.core"), store.PurposeIssue)
			if out != nil || code(err) != CodeEndpoint {
				t.Fatalf("%v %v", out, err)
			}
			if f.is.nonces.len() != 0 || len(f.rec.recs) != 0 {
				t.Fatal("a nonce or a record was kept")
			}
		})
	}
}

// The directory is read for a legacy endpoint only, and not before the nonce: a legacy request that fails its shape
// check costs no read, and a new-form request never reads.
func TestIssuerReadsTheDirectoryOnlyForALegacyEndpoint(t *testing.T) {
	f := newFixture(t)
	d := &fakeDirectory{owner: "c", legacy: true, found: true}
	f.is.Directory = d
	if _, err := f.is.Issue(context.Background(), f.req(t, "c.core", time.Minute), store.PurposeIssue); err != nil {
		t.Fatal(err)
	}
	if d.reads != 0 {
		t.Fatalf("a new-form endpoint read the directory %d times", d.reads)
	}
	// A legacy endpoint of another address is refused by the shape check before any read.
	r := f.legacyReq(t, "c.core")
	r.Endpoint = "cc.c.lead"
	var err error
	if r.Proof, err = SignProof(f.requester, r.Address, r.Endpoint, r.PublicKey, r.Host, r.Nonce, r.Expires); err != nil {
		t.Fatal(err)
	}
	if _, err := f.is.Issue(context.Background(), r, store.PurposeIssue); code(err) != CodeEndpoint || d.reads != 0 {
		t.Fatalf("%v, reads %d", err, d.reads)
	}
}
