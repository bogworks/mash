package main

import (
	"context"
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
	if fileMode(t, marker) != 0o600 {
		t.Error("marker mode")
	}
	if _, err := os.Lstat(jwtOut); !os.IsNotExist(err) {
		t.Fatal("a join file exists after the kill")
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
		t.Errorf("join file not written: %v", err)
	}
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Error("marker left")
	}
	if len(*calls) != 1 {
		t.Errorf("the old key was revoked %d times, want 1: %+v", len(*calls), *calls)
	}
	// without a marker the same key is refused as before
	_, se, code := h.run("endpoint", "reissue", a.String(), "--out", filepath.Join(out, "other.join"), "--public-key", file1)
	if code != 1 || !strings.Contains(se, "already this endpoint's key") {
		t.Fatalf("no marker: %d %q", code, se)
	}
	// a marker for another key or address does not count
	if err := os.WriteFile(filepath.Join(out, "other.join.pending"), []byte("x\ny\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, se, code := h.run("endpoint", "reissue", a.String(), "--out", filepath.Join(out, "other.join"), "--public-key", file1); code != 1 || !strings.Contains(se, "already this endpoint's key") {
		t.Fatalf("foreign marker: %d %q", code, se)
	}
}

func TestEndpointReissueRefusesFutureIssued(t *testing.T) {
	for _, mode := range []string{"public-key", "installed-here", "out"} {
		t.Run(mode, func(t *testing.T) {
			h := newFleetEnv(t)
			calls := fakeRevoke(t, nil)
			a := ad("fu")
			h.seed(a)
			rec, _ := h.record(a)
			h.recordIssuedAt(rec.User, time.Now().Add(time.Hour))
			out := t.TempDir()
			_, file, _ := epKey(t, out, "n")
			args := []string{"endpoint", "reissue", a.String()}
			switch mode {
			case "public-key":
				args = append(args, "--out", filepath.Join(out, "n.join"), "--public-key", file)
			case "out":
				args = append(args, "--out", filepath.Join(out, "n.join"))
			}
			before := epSnapshot(t, h, h.dir)
			so, se, code := h.run(args...)
			if code != 1 || so != "" || !strings.HasPrefix(se, "error: ") || !strings.Contains(se, "issued in the future") {
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

func TestEndpointReissueMakeKeyChecksCreds(t *testing.T) {
	h := newFleetEnv(t)
	calls := fakeRevoke(t, nil)
	a, b, c, d := ad("a"), ad("b"), ad("c"), ad("d")
	for _, x := range []string{"a", "b", "c", "d"} {
		h.seed(ad(x))
	}
	// matching file passes
	if so, se, code := h.run("endpoint", "reissue", a.String()); code != 0 {
		t.Fatalf("matching: %d %q %q", code, so, se)
	}
	n := len(*calls)
	// a file for another endpoint is refused and kept
	other, _ := os.ReadFile(h.creds(b))
	if err := os.WriteFile(h.creds(a), other, 0o600); err != nil {
		t.Fatal(err)
	}
	so, se, code := h.run("endpoint", "reissue", a.String())
	if code != 1 || so != "" || !strings.Contains(se, "is not this endpoint's current creds") || !strings.Contains(se, "mv ") {
		t.Fatalf("other endpoint: %d %q", code, se)
	}
	if got, _ := os.ReadFile(h.creds(a)); string(got) != string(other) || len(*calls) != n {
		t.Error("the file was overwritten or a key revoked")
	}
	// a file whose key differs from the directory's (the directory moved on) is refused
	oldC, _ := os.ReadFile(h.creds(c))
	if _, se, code := h.run("endpoint", "reissue", c.String()); code != 0 {
		t.Fatalf("reissue c: %q", se)
	}
	if err := os.WriteFile(h.creds(c), oldC, 0o600); err != nil {
		t.Fatal(err)
	}
	n = len(*calls)
	so, se, code = h.run("endpoint", "reissue", c.String())
	if code != 1 || !strings.Contains(se, "is not this endpoint's current creds") || len(*calls) != n {
		t.Fatalf("stale key: %d %q", code, se)
	}
	// an unparsable file
	if err := os.WriteFile(h.creds(d), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	so, se, code = h.run("endpoint", "reissue", d.String())
	if code != 1 || so != "" || !strings.Contains(se, "the creds file "+h.creds(d)+" exists but") {
		t.Fatalf("junk: %d %q", code, se)
	}
}

// A reissue whose key was replaced by another run before the check removes its output and reports superseded.
func TestEndpointReissueSupersededRemovesOutput(t *testing.T) {
	for _, mode := range []string{"public-key", "installed-here", "out"} {
		t.Run(mode, func(t *testing.T) {
			h := newFleetEnv(t)
			fakeRevoke(t, nil)
			a := ad("sup")
			h.seed(a)
			out := t.TempDir()
			target := filepath.Join(out, "new.join")
			args := []string{"endpoint", "reissue", a.String()}
			switch mode {
			case "installed-here":
				target = h.creds(a)
			case "out":
				args = append(args, "--out", target)
			default:
				_, file1, _ := epKey(t, out, "k1")
				args = append(args, "--out", target, "--public-key", file1)
			}
			epBeforeVerify = func() {
				rec, _ := h.record(a)
				_, other := h.e.Mint(t, ad("sup2"))
				if _, err := h.adminOf().SetUser(context.Background(), a, h.e.Account, rec.User, other); err != nil {
					t.Error(err)
				}
			}
			t.Cleanup(func() { epBeforeVerify = nil })
			_, se, code := h.run(args...)
			if code != 1 || !strings.Contains(se, "another reissue changed") {
				t.Fatalf("%d %q", code, se)
			}
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Errorf("the output file was kept: %v", err)
			}
			if _, err := os.Lstat(target + ".pending"); !os.IsNotExist(err) {
				t.Errorf("a pending file was left: %v", err)
			}
		})
	}
}

// With nothing changing the record, the check passes and the output stays.
func TestEndpointReissueVerifyPassesOnNormalRun(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	a := ad("nrm")
	h.seed(a)
	called := false
	epBeforeVerify = func() { called = true }
	t.Cleanup(func() { epBeforeVerify = nil })
	_, se, code := h.run("endpoint", "reissue", a.String())
	if code != 0 || !called {
		t.Fatalf("%d %v %q", code, called, se)
	}
	if _, err := os.Stat(h.creds(a)); err != nil {
		t.Error(err)
	}
}
