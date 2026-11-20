package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// credsPub is the user public key of the creds file of a.
func credsPub(t *testing.T, h *fleetEnv, a sap.Address) string {
	t.Helper()
	pub, err := epCredsUser(h.creds(a), a)
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

// deleteIssued removes the issue record of pub, as the admin API of the bucket allows.
func (h *fleetEnv) deleteIssued(pub string) {
	h.t.Helper()
	js, err := jetstream.New(h.e.Conn(h.t, h.e.AdminCreds))
	if err != nil {
		h.t.Fatal(err)
	}
	kv, err := js.KeyValue(context.Background(), store.IssuedBucket)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := kv.Delete(context.Background(), pub); err != nil {
		h.t.Fatal(err)
	}
}

func TestEndpointReissueSameKey(t *testing.T) {
	h := newFleetEnv(t)
	calls := fakeRevoke(t, nil)
	out := t.TempDir()
	pub, file, _ := epKey(t, out, "k")
	a := ad("same")
	if _, se, code := h.run("endpoint", "create", a.String(), "--out", filepath.Join(out, "k.join"), "--public-key", file); code != 0 {
		t.Fatalf("create: %q", se)
	}
	for _, extra := range [][]string{{"--dry-run"}, nil} {
		args := append([]string{"endpoint", "reissue", a.String(), "--out", filepath.Join(out, "n.join"), "--public-key", file}, extra...)
		so, se, code := h.run(args...)
		if code != 1 || so != "" || !strings.HasPrefix(se, "error: the public key is already this endpoint's key, so nothing would be rotated") {
			t.Fatalf("%v: %d %q %q", extra, code, so, se)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("revoke calls %+v", *calls)
	}
	if !gone(t, filepath.Join(out, "n.join")) {
		t.Error("a join file was written")
	}
	// the restore case: the issue record is gone, the same key is recorded again
	h.deleteIssued(pub)
	if _, se, code := h.run("endpoint", "reissue", a.String(), "--out", filepath.Join(out, "n.join"), "--public-key", file, "--dry-run"); code != 0 {
		t.Fatalf("dry-run restore: %d %q", code, se)
	}
	if _, ok := h.issued()[pub]; ok {
		t.Fatal("the dry-run recorded the key")
	}
	if so, se, code := h.run("endpoint", "reissue", a.String(), "--out", filepath.Join(out, "n.join"), "--public-key", file); code != 0 {
		t.Fatalf("restore: %d %q %q", code, so, se)
	}
	if rec, ok := h.issued()[pub]; !ok || rec.Address != a.Flat() {
		t.Errorf("the issue record is not back: %+v", h.issued())
	}
}

// killAt runs the command with the hook set to panic, as a kill at that point would stop it, and returns the exit code
// the run would have had: -1 when it was stopped.
func killAt(h *fleetEnv, hook *func(), args ...string) (code int) {
	*hook = func() { panic("killed") }
	defer func() { *hook = nil }()
	defer func() {
		if recover() != nil {
			code = -1
		}
	}()
	_, _, code = h.run(args...)
	return code
}

// issuedFor is how many issue records name a.
func issuedFor(h *fleetEnv, a sap.Address) int {
	n := 0
	for _, r := range h.issued() {
		if r.Address == a.Flat() {
			n++
		}
	}
	return n
}

func noPending(t *testing.T, h *fleetEnv) {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(h.dir, "*.pending"))
	if len(m) != 0 {
		t.Errorf("pending files left: %v", m)
	}
}

func TestEndpointCreateKilledAfterEnroll(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	a := ad("k1")
	args := []string{"endpoint", "create", a.String()}
	if code := killAt(h, &epAfterEnroll, args...); code != -1 {
		t.Fatalf("not stopped: %d", code)
	}
	pending := h.creds(a) + ".pending"
	if fileMode(t, pending) != 0o600 {
		t.Error("pending mode")
	}
	if _, err := os.Lstat(h.creds(a)); !os.IsNotExist(err) {
		t.Fatal("creds file exists after the kill")
	}
	rec, ok := h.record(a)
	if !ok {
		t.Fatal("no directory record after the kill")
	}
	so, se, code := h.run(args...)
	if code != 0 || so != "Created endpoint "+a.String()+"\n" {
		t.Fatalf("rerun %d %q %q", code, so, se)
	}
	noSecrets(t, so, se)
	if n := len(h.records()); n != 1 {
		t.Errorf("%d directory records", n)
	}
	if n := issuedFor(h, a); n != 1 {
		t.Errorf("%d issued keys", n)
	}
	if got := credsPub(t, h, a); got != rec.User {
		t.Errorf("creds key %s, record %s", got, rec.User)
	}
	noPending(t, h)
}

// The same kill with --out: the staged key sits beside the join file path, and the rerun writes the join file with it.
func TestEndpointCreateOutKilledAfterEnroll(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	a := ad("k1o")
	jf := filepath.Join(t.TempDir(), "k1o.join")
	args := []string{"endpoint", "create", a.String(), "--out", jf}
	if code := killAt(h, &epAfterEnroll, args...); code != -1 {
		t.Fatalf("not stopped: %d", code)
	}
	if fileMode(t, jf+".pending") != 0o600 || !gone(t, jf) || !gone(t, h.creds(a)) {
		t.Fatal("the files after the kill are wrong")
	}
	rec, ok := h.record(a)
	if !ok {
		t.Fatal("no directory record after the kill")
	}
	so, se, code := h.run(args...)
	if code != 0 || so != "Created endpoint "+a.String()+"\nJoin file: "+jf+"\n" {
		t.Fatalf("rerun %d %q %q", code, so, se)
	}
	j, _ := readJoin(t, jf)
	if got, err := epCredsUserOf([]byte(j.Creds), jf, a); err != nil || got != rec.User {
		t.Errorf("join creds key %s (%v), record %s", got, err, rec.User)
	}
	if !gone(t, jf+".pending") || issuedFor(h, a) != 1 || len(h.records()) != 1 {
		t.Error("state after the rerun")
	}
	if !gone(t, h.creds(a)) {
		t.Error("a creds file was installed here")
	}
}

func TestEndpointCreateKilledAfterStage(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	a := ad("k2")
	args := []string{"endpoint", "create", a.String()}
	if code := killAt(h, &epAfterStage, args...); code != -1 {
		t.Fatalf("not stopped: %d", code)
	}
	if _, ok := h.record(a); ok {
		t.Fatal("a record before enroll")
	}
	if _, se, code := h.run(args...); code != 0 {
		t.Fatalf("rerun %d %q", code, se)
	}
	rec, ok := h.record(a)
	if !ok || credsPub(t, h, a) != rec.User || issuedFor(h, a) != 1 || len(h.records()) != 1 {
		t.Errorf("state: %+v %v", rec, ok)
	}
	noPending(t, h)
}

func TestEndpointCreateCredsExist(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	a := ad("k3")
	args := []string{"endpoint", "create", a.String()}
	if _, se, code := h.run(args...); code != 0 {
		t.Fatalf("create %q", se)
	}
	pending := h.creds(a) + ".pending"
	// a pending file for the same key as the creds file: the rerun removes it and succeeds
	raw, _ := os.ReadFile(h.creds(a))
	kp, err := jwt.ParseDecoratedUserNKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := kp.Seed()
	if err := os.WriteFile(pending, seed, 0o600); err != nil {
		t.Fatal(err)
	}
	so, se, code := h.run(args...)
	if code != 0 || so != "Created endpoint "+a.String()+"\n" {
		t.Fatalf("same key: %d %q %q", code, so, se)
	}
	noPending(t, h)
	// a pending file for another key: refused, nothing changed
	_, _, other := epKey(t, t.TempDir(), "o")
	if err := os.WriteFile(pending, other, 0o600); err != nil {
		t.Fatal(err)
	}
	before := epSnapshot(t, h, h.dir)
	so, se, code = h.run(args...)
	if code != 1 || so != "" || se != "error: "+h.creds(a)+" already exists\n" {
		t.Fatalf("other key: %d %q %q", code, so, se)
	}
	if epSnapshot(t, h, h.dir) != before {
		t.Error("a refusal changed state")
	}
}

func TestEndpointCreateDryRunStagesNothing(t *testing.T) {
	h := newFleetEnv(t)
	before := epSnapshot(t, h, h.dir)
	if _, se, code := h.run("endpoint", "create", ad("k4").String(), "--dry-run"); code != 0 {
		t.Fatalf("%d %q", code, se)
	}
	if epSnapshot(t, h, h.dir) != before {
		t.Error("dry-run changed state")
	}
	noPending(t, h)
}

func TestEndpointReissueKilledAfterEnroll(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	a := ad("k5")
	if _, se, code := h.run("endpoint", "create", a.String()); code != 0 {
		t.Fatalf("create %q", se)
	}
	old := credsPub(t, h, a)
	args := []string{"endpoint", "reissue", a.String()}
	if code := killAt(h, &epAfterEnroll, args...); code != -1 {
		t.Fatalf("not stopped: %d", code)
	}
	if credsPub(t, h, a) != old {
		t.Fatal("creds changed before the kill point")
	}
	if _, se, code := h.run(args...); code != 0 {
		t.Fatalf("rerun %d %q", code, se)
	}
	rec, _ := h.record(a)
	if got := credsPub(t, h, a); got != rec.User || got == old {
		t.Errorf("creds %s record %s old %s", got, rec.User, old)
	}
	noPending(t, h)
}

func TestEndpointDeleteFinishesAfterKill(t *testing.T) {
	h := newFleetEnv(t)
	a := ad("d1")
	h.seed(a)
	retireNow = func() time.Time { return time.Date(2026, 11, 2, 3, 4, 5, 0, time.UTC) }
	t.Cleanup(func() { retireNow = time.Now })
	creds, _ := os.ReadFile(h.creds(a))
	if _, se, code := h.run("endpoint", "delete", a.String(), "--yes"); code != 0 {
		t.Fatalf("delete %q", se)
	}
	// the state of a kill before the creds step: the record is gone, the address burned, the creds file in place
	retired := h.creds(a) + ".retired-20261102T030405Z"
	if err := os.Rename(retired, h.creds(a)); err != nil {
		t.Fatal(err)
	}
	so, se, code := h.run("endpoint", "delete", a.String(), "--yes", "--json")
	if code != 0 {
		t.Fatalf("rerun %d %q %q", code, so, se)
	}
	var d deleteJSON
	if err := json.Unmarshal([]byte(so), &d); err != nil || !d.Deleted || d.Address != a.String() {
		t.Fatalf("doc %q %v", so, err)
	}
	if got, err := os.ReadFile(retired); err != nil || string(got) != string(creds) {
		t.Errorf("retired file: %v", err)
	}
	if _, err := os.Lstat(h.creds(a)); !os.IsNotExist(err) {
		t.Error("creds file still in place")
	}
}

func TestEndpointDeleteNoRecordNotBurnedStillRefuses(t *testing.T) {
	h := newFleetEnv(t)
	a := ad("d2")
	if err := os.WriteFile(h.creds(a), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	so, _, code := h.run("endpoint", "delete", a.String(), "--yes", "--json")
	if code != 1 || !strings.Contains(so, `"error": "`) && !strings.Contains(so, `"error":"`) || !strings.Contains(so, a.String()+" does not exist") {
		t.Fatalf("%d %q", code, so)
	}
	if _, err := os.Lstat(h.creds(a)); err != nil {
		t.Error("the creds file was touched")
	}
}

func TestEndpointDeleteTakesOnlyTheDottedForm(t *testing.T) {
	h := newFleetEnv(t)
	a := ad("d3")
	h.seed(a)
	for _, in := range []string{a.Owner + "/" + a.Group + "/" + a.Instance, "d3", a.Group + "." + a.Instance} {
		so, se, code := h.run("endpoint", "delete", in, "--yes")
		wantUsage(t, so, se, code, "delete", "not an endpoint address")
	}
	if _, ok := h.record(a); !ok {
		t.Error("a refused delete removed the record")
	}
}
