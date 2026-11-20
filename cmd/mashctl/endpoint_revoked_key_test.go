package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
)

// A key the account JWT revokes is never signed by create or reissue --public-key, even with no issue record.
func TestEndpointCreatePublicKeyRefusesRevokedKey(t *testing.T) {
	h := newFleetEnv(t)
	out := t.TempDir()
	pub, file, _ := epKey(t, out, "k")
	a := ad("rk")
	jf := filepath.Join(out, "rk.join")
	before := epSnapshot(t, h, out)
	for _, dry := range []bool{false, true} {
		fakeKeyRevoked(t, func(p string) (bool, error) { return p == pub, nil })
		args := []string{"endpoint", "create", a.String(), "--out", jf, "--public-key", file}
		if dry {
			args = append(args, "--dry-run")
		}
		so, se, code := h.run(args...)
		if code != 1 || so != "" || !strings.Contains(se, "was revoked") || !strings.HasSuffix(se, "\nhint: mash key create --out NAME.seed\n") {
			t.Fatalf("dry=%v revoked: %d %q %q", dry, code, so, se)
		}
		fakeKeyRevoked(t, func(string) (bool, error) { return false, errors.New("no answer") })
		so, se, code = h.run(args...)
		if code != 1 || !strings.Contains(se, "cannot tell whether the public key") {
			t.Fatalf("dry=%v unknown: %d %q %q", dry, code, so, se)
		}
	}
	if _, err := os.Lstat(jf); !os.IsNotExist(err) {
		t.Error("a join file was written")
	}
	if _, ok := h.record(a); ok {
		t.Error("a directory record was written")
	}
	if got := epSnapshot(t, h, out); got != before {
		t.Errorf("state changed:\n%s\nwas:\n%s", got, before)
	}
}

func TestEndpointReissuePublicKeyRefusesRevokedKey(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	out := t.TempDir()
	a := ad("rr")
	_, file0, _ := epKey(t, out, "k0")
	if _, se, code := h.run("endpoint", "create", a.String(), "--out", filepath.Join(out, "k0.join"), "--public-key", file0); code != 0 {
		t.Fatalf("create: %q", se)
	}
	pub1, file1, _ := epKey(t, out, "k1")
	jf := filepath.Join(out, "new.join")
	before := epSnapshot(t, h, out)
	for _, dry := range []bool{false, true} {
		fakeKeyRevoked(t, func(p string) (bool, error) { return p == pub1, nil })
		args := []string{"endpoint", "reissue", a.String(), "--out", jf, "--public-key", file1}
		if dry {
			args = append(args, "--dry-run")
		}
		so, se, code := h.run(args...)
		if code != 1 || !strings.Contains(se, "was revoked") {
			t.Fatalf("dry=%v revoked: %d %q %q", dry, code, so, se)
		}
		fakeKeyRevoked(t, func(string) (bool, error) { return false, errors.New("no answer") })
		so, se, code = h.run(args...)
		if code != 1 || !strings.Contains(se, "cannot tell whether the public key") {
			t.Fatalf("dry=%v unknown: %d %q %q", dry, code, so, se)
		}
	}
	if got := epSnapshot(t, h, out); got != before {
		t.Errorf("state changed:\n%s\nwas:\n%s", got, before)
	}
}

// A resume (marker names the directory's key) and a restore (no issue record) never re-sign a revoked current key.
func TestEndpointReissueNeverResignsRevokedCurrentKey(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	out := t.TempDir()
	a := ad("rv")
	_, file0, _ := epKey(t, out, "k0")
	if _, se, code := h.run("endpoint", "create", a.String(), "--out", filepath.Join(out, "k0.join"), "--public-key", file0); code != 0 {
		t.Fatalf("create: %q", se)
	}
	pub1, file1, _ := epKey(t, out, "k1")
	jwtOut := filepath.Join(out, "new.join")
	args := []string{"endpoint", "reissue", a.String(), "--out", jwtOut, "--public-key", file1}
	if code := killAt(h, &epAfterEnroll, args...); code != -1 {
		t.Fatalf("not stopped: %d", code)
	}
	marker := jwtOut + ".pending"
	before, _ := os.ReadFile(marker)
	fakeKeyRevoked(t, func(p string) (bool, error) { return p == pub1, nil })
	so, se, code := h.run(args...)
	if code != 1 || !strings.Contains(se, "was revoked") {
		t.Fatalf("resume revoked: %d %q %q", code, so, se)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != string(before) {
		t.Errorf("marker not kept: %v", err)
	}
	if _, err := os.Lstat(jwtOut); !os.IsNotExist(err) {
		t.Error("a join file was written")
	}
	fakeKeyRevoked(t, func(string) (bool, error) { return false, errors.New("no answer") })
	so, se, code = h.run(args...)
	if code != 1 || !strings.Contains(se, "cannot tell whether the public key") {
		t.Fatalf("resume unknown: %d %q %q", code, so, se)
	}
	if _, err := os.Lstat(marker); err != nil {
		t.Error("marker removed")
	}

	// restore: a directory record whose key has no issue record
	b := ad("rs2")
	cur, _, _ := epKey(t, out, "cur")
	if _, err := h.adminOf().Provision(context.Background(), b, cur, h.accountPub(), "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := h.adminOf().IssuedByKey(context.Background(), cur); found {
		t.Fatal("the provisioned endpoint has an issue record")
	}
	curFile := filepath.Join(out, "cur.pub")
	if err := os.WriteFile(curFile, []byte(cur+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rj := filepath.Join(out, "r.join")
	rargs := []string{"endpoint", "reissue", b.String(), "--out", rj, "--public-key", curFile}
	fakeKeyRevoked(t, func(p string) (bool, error) { return p == cur, nil })
	so, se, code = h.run(rargs...)
	if code != 1 || !strings.Contains(se, "was revoked") {
		t.Fatalf("restore revoked: %d %q %q", code, so, se)
	}
	fakeKeyRevoked(t, func(string) (bool, error) { return false, errors.New("no answer") })
	so, se, code = h.run(rargs...)
	if code != 1 || !strings.Contains(se, "cannot tell whether the public key") {
		t.Fatalf("restore unknown: %d %q %q", code, so, se)
	}
	if _, err := os.Lstat(rj); !os.IsNotExist(err) {
		t.Error("a join file was written")
	}
}
