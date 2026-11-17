package main

import (
	"bytes"
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
)

// unmarkIssuedStream removes the history marker from the issue record's stream, so the bucket reads as one from before
// the marker (an older deployment).
func (h *fleetEnv) unmarkIssuedStream() {
	h.t.Helper()
	ctx := context.Background()
	js, err := jetstream.New(h.e.Conn(h.t, h.e.AdminCreds))
	if err != nil {
		h.t.Fatal(err)
	}
	s, err := js.Stream(ctx, store.IssuedStreamName)
	if err != nil {
		h.t.Fatal(err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	sc := info.Config
	md := map[string]string{}
	for k, v := range sc.Metadata {
		if k != store.IssuedMarkerKey {
			md[k] = v
		}
	}
	sc.Metadata = md
	if _, err := js.UpdateStream(ctx, sc); err != nil {
		h.t.Fatal(err)
	}
}

// recordHistory leaves pub's issue record with exactly total revisions in a bucket that has no marker.
func (h *fleetEnv) recordHistory(pub, name string, total int) {
	h.t.Helper()
	adm, err := admin.New(h.e.Conn(h.t, h.e.AdminCreds), h.e.Owner)
	if err != nil {
		h.t.Fatal(err)
	}
	rec := store.Issued{PublicKey: pub, Address: name, Kind: store.IssuedEndpoint}
	if h.issuedHistory0(pub) == 0 {
		if err := adm.RecordIssued(context.Background(), rec); err != nil {
			h.t.Fatal(err)
		}
	}
	h.unmarkIssuedStream()
	for h.issuedHistory0(pub) < total {
		if err := adm.RecordIssued(context.Background(), rec); err != nil {
			h.t.Fatal(err)
		}
	}
}

// issuedHistory0 is issuedHistory, but 0 for a key or bucket that does not exist yet.
func (h *fleetEnv) issuedHistory0(pub string) int {
	h.t.Helper()
	js, err := jetstream.New(h.e.Conn(h.t, h.e.AdminCreds))
	if err != nil {
		h.t.Fatal(err)
	}
	kv, err := js.KeyValue(context.Background(), store.IssuedBucket)
	if err != nil {
		return 0
	}
	hist, err := kv.History(context.Background(), pub)
	if err != nil {
		return 0
	}
	return len(hist)
}

const unprovableText = "its issued history cannot be shown to be complete"

// retire refuses a key whose issue history cannot be shown complete, with nothing changed; a provable one retires.
func TestRetireRefusesAnUnprovableIssueHistory(t *testing.T) {
	h := newFleetEnv(t)
	h.seedMeta(ad("c"), admin.Meta{Labels: map[string]string{"env": "red"}}, nil)
	_, pub := h.credsClaims(ad("c"))
	h.recordHistory(pub, ad("c").Flat(), store.IssuedMinHistory) // no marker, 8 revisions
	before, _ := os.ReadFile(h.creds(ad("c")))
	recs := h.records()
	calls := fakeRevoke(t, nil)

	out, errb, code := h.run("retire", "o.g.c")
	if code != 1 || out != "" || !strings.Contains(errb, unprovableText) || !strings.Contains(errb, "by hand") || !strings.Contains(errb, "nothing was changed") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
	if after, _ := os.ReadFile(h.creds(ad("c"))); len(*calls) != 0 || !bytes.Equal(before, after) || !slices.Equal(recs, h.records()) || h.issued()[pub].RevokedAt != "" {
		t.Fatalf("something changed: calls %+v records %v", *calls, h.records())
	}

	// Control: the same bucket with 7 revisions of the key is provable.
	h2 := newFleetEnv(t)
	h2.seedMeta(ad("c"), admin.Meta{Labels: map[string]string{"env": "red"}}, nil)
	_, pub2 := h2.credsClaims(ad("c"))
	h2.recordHistory(pub2, ad("c").Flat(), store.IssuedMinHistory-1)
	calls2 := fakeRevoke(t, nil)
	if out, errb, code := h2.run("retire", "o.g.c"); code != 0 || !strings.Contains(out, "revoked in the account JWT") || len(*calls2) != 1 {
		t.Fatalf("%d %q %q calls %+v", code, out, errb, *calls2)
	}
}
