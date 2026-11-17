package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A --public-key rerun after the directory switched to the key but the JWT was not written finishes the job.
func TestEndpointReissuePublicKeyResumesAfterKill(t *testing.T) {
	h := newFleetEnv(t)
	calls := fakeRevoke(t, nil)
	out := t.TempDir()
	a := ad("rs")
	_, file0, _ := epKey(t, out, "k0")
	if _, se, code := h.run("endpoint", "create", a.String(), "--public-key", file0, "--jwt-out", filepath.Join(out, "k0.jwt")); code != 0 {
		t.Fatalf("create: %q", se)
	}
	pub1, file1, _ := epKey(t, out, "k1")
	jwtOut := filepath.Join(out, "new.jwt")
	args := []string{"endpoint", "reissue", a.String(), "--public-key", file1, "--jwt-out", jwtOut}
	if code := killAt(h, &epAfterEnroll, args...); code != -1 {
		t.Fatalf("not stopped: %d", code)
	}
	marker := jwtOut + ".pending"
	if fileMode(t, marker) != 0o600 {
		t.Error("marker mode")
	}
	if _, err := os.Lstat(jwtOut); !os.IsNotExist(err) {
		t.Fatal("a JWT exists after the kill")
	}
	if rec, _ := h.record(a); rec.User != pub1 {
		t.Fatal("the directory did not move to the new key")
	}
	if so, se, code := h.run(append(args, "--dry-run")...); code != 0 {
		t.Fatalf("dry-run resume: %d %q %q", code, so, se)
	}
	if so, se, code := h.run(args...); code != 0 {
		t.Fatalf("rerun: %d %q %q", code, so, se)
	}
	if _, err := os.Stat(jwtOut); err != nil {
		t.Errorf("JWT not written: %v", err)
	}
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Error("marker left")
	}
	if len(*calls) != 1 {
		t.Errorf("the old key was revoked %d times, want 1: %+v", len(*calls), *calls)
	}
	// without a marker the same key is refused as before
	_, se, code := h.run("endpoint", "reissue", a.String(), "--public-key", file1, "--jwt-out", filepath.Join(out, "other.jwt"), "--json")
	if code != 1 || !strings.Contains(se, "key_unchanged") {
		t.Fatalf("no marker: %d %q", code, se)
	}
	// a marker for another key or address does not count
	if err := os.WriteFile(filepath.Join(out, "other.jwt.pending"), []byte("x\ny\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, se, code := h.run("endpoint", "reissue", a.String(), "--public-key", file1, "--jwt-out", filepath.Join(out, "other.jwt")); code != 1 || !strings.Contains(se, "key_unchanged") {
		t.Fatalf("foreign marker: %d %q", code, se)
	}
}

func TestEndpointReissueRefusesFutureIssued(t *testing.T) {
	for _, mode := range []string{"public-key", "make-key"} {
		t.Run(mode, func(t *testing.T) {
			h := newFleetEnv(t)
			calls := fakeRevoke(t, nil)
			a := ad("fu")
			h.seed(a)
			rec, _ := h.record(a)
			h.recordIssuedAt(rec.User, time.Now().Add(time.Hour))
			out := t.TempDir()
			_, file, _ := epKey(t, out, "n")
			args := []string{"endpoint", "reissue", a.String(), "--json"}
			if mode == "make-key" {
				args = append(args, "--make-key")
			} else {
				args = append(args, "--public-key", file, "--jwt-out", filepath.Join(out, "n.jwt"))
			}
			before := epSnapshot(t, h, h.dir)
			so, se, code := h.run(args...)
			d := epDocOf(t, so)
			if code != 1 || d.Refusal == nil || d.Refusal.Code != "future_issued" || !strings.Contains(d.Refusal.Detail, "issued in the future") {
				t.Fatalf("%d %q %q", code, so, se)
			}
			if len(*calls) != 0 || epSnapshot(t, h, h.dir) != before {
				t.Errorf("something changed: %+v", *calls)
			}
			if r, _ := h.record(a); r.User != rec.User {
				t.Error("directory key changed")
			}
			noPending(t, h)
			if m, _ := filepath.Glob(filepath.Join(out, "*.pending")); len(m) != 0 {
				t.Errorf("marker left: %v", m)
			}
		})
	}
}

func TestEndpointBulkRefusesFutureIssued(t *testing.T) {
	h := newFleetEnv(t)
	calls := fakeRevoke(t, nil)
	a, b := ad("a"), ad("b")
	h.seed(a)
	h.seed(b)
	rec, _ := h.record(a)
	h.recordIssuedAt(rec.User, time.Now().Add(time.Hour))
	so, _, code := h.run("endpoint", "reissue", "--all", "--make-key", "--yes", "--json")
	d := bulkDoc(t, so)
	if code != 5 || d.Counts.Reissued != 1 || d.Counts.Refused != 1 || d.Endpoints[0].Reason == nil || *d.Endpoints[0].Reason != "future_issued" {
		t.Fatalf("%d %q", code, so)
	}
	if len(*calls) != 1 {
		t.Errorf("revoke calls %+v", *calls)
	}
}

func TestEndpointBulkChangedSelectsPendingAndRevoked(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	a, b, c := ad("a"), ad("b"), ad("c")
	h.seed(a)
	h.seed(b)
	h.seed(c)
	if err := os.WriteFile(h.creds(a)+".pending", []byte("not a seed"), 0o600); err != nil {
		t.Fatal(err)
	}
	recB, _ := h.record(b)
	fakeKeyRevoked(t, func(pub string) (bool, error) { return pub == recB.User, nil })
	oldC := credsPub(t, h, c)
	so, se, code := h.run("endpoint", "reissue", "--changed", "--make-key", "--json")
	d := bulkDoc(t, so)
	if code != 0 || d.Counts.Planned != 2 || len(d.Endpoints) != 2 {
		t.Fatalf("plan: %d %q %q", code, so, se)
	}
	if r := d.Endpoints[0].Reason; r == nil || *r != "rotation_pending" {
		t.Errorf("a: %+v", d.Endpoints[0])
	}
	if r := d.Endpoints[1].Reason; r == nil || *r != "key_revoked" || d.Endpoints[1].Address != b.String() {
		t.Errorf("b: %+v", d.Endpoints[1])
	}
	so, _, _ = h.run("endpoint", "reissue", "--changed", "--make-key", "--yes", "--json")
	d = bulkDoc(t, so)
	if len(d.Endpoints) != 2 {
		t.Fatalf("%q", so)
	}
	if rec, _ := h.record(b); rec.User == recB.User {
		t.Error("the endpoint with the revoked key was not reissued")
	}
	if credsPub(t, h, c) != oldC {
		t.Error("an unselected endpoint was reissued")
	}
}

func TestEndpointBulkChangedRevocationUnknown(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	h.seed(ad("a"))
	fakeKeyRevoked(t, func(string) (bool, error) { return false, errors.New("no sys creds") })
	so, se, code := h.run("endpoint", "reissue", "--changed", "--make-key", "--yes", "--json")
	d := bulkDoc(t, so)
	if code == 0 || d.Outcome == "unchanged" || d.Counts.Error != 1 || d.Endpoints[0].Reason == nil || *d.Endpoints[0].Reason != "revocation_unknown" {
		t.Fatalf("%d %q %q", code, so, se)
	}
}

func TestEndpointReissueMakeKeyChecksCreds(t *testing.T) {
	h := newFleetEnv(t)
	calls := fakeRevoke(t, nil)
	a, b, c, d := ad("a"), ad("b"), ad("c"), ad("d")
	for _, x := range []string{"a", "b", "c", "d"} {
		h.seed(ad(x))
	}
	// matching file passes
	if so, se, code := h.run("endpoint", "reissue", a.String(), "--make-key"); code != 0 {
		t.Fatalf("matching: %d %q %q", code, so, se)
	}
	n := len(*calls)
	// a file for another endpoint is refused and kept
	other, _ := os.ReadFile(h.creds(b))
	if err := os.WriteFile(h.creds(a), other, 0o600); err != nil {
		t.Fatal(err)
	}
	so, _, code := h.run("endpoint", "reissue", a.String(), "--make-key", "--json")
	doc := epDocOf(t, so)
	if code != 1 || doc.Refusal == nil || doc.Refusal.Code != "creds_mismatch" || !strings.Contains(doc.Refusal.Detail, "mv ") || !strings.Contains(doc.Refusal.Detail, "--public-key") {
		t.Fatalf("other endpoint: %d %q", code, so)
	}
	if got, _ := os.ReadFile(h.creds(a)); string(got) != string(other) || len(*calls) != n {
		t.Error("the file was overwritten or a key revoked")
	}
	// a file whose key differs from the directory's (the directory moved on) is refused
	oldC, _ := os.ReadFile(h.creds(c))
	if _, se, code := h.run("endpoint", "reissue", c.String(), "--make-key"); code != 0 {
		t.Fatalf("reissue c: %q", se)
	}
	if err := os.WriteFile(h.creds(c), oldC, 0o600); err != nil {
		t.Fatal(err)
	}
	n = len(*calls)
	so, _, code = h.run("endpoint", "reissue", c.String(), "--make-key", "--json")
	doc = epDocOf(t, so)
	if code != 1 || doc.Refusal == nil || doc.Refusal.Code != "creds_mismatch" || len(*calls) != n {
		t.Fatalf("stale key: %d %q", code, so)
	}
	// an unparsable file
	dRaw, _ := os.ReadFile(h.creds(d))
	if err := os.WriteFile(h.creds(d), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	so, _, code = h.run("endpoint", "reissue", d.String(), "--make-key", "--json")
	doc = epDocOf(t, so)
	if code != 1 || doc.Refusal == nil || doc.Refusal.Code != "creds_unreadable" {
		t.Fatalf("junk: %d %q", code, so)
	}
	// bulk: a mismatch refuses that endpoint only
	if err := os.WriteFile(h.creds(d), dRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	bo, _, code := h.run("endpoint", "reissue", "--all", "--make-key", "--yes", "--json")
	bd := bulkDoc(t, bo)
	if code != 5 || bd.Counts.Refused != 2 || bd.Counts.Reissued != 2 {
		t.Fatalf("bulk: %d %q", code, bo)
	}
}
