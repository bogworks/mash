package main

import (
	"context"
	"encoding/json"
	"flag"
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

var updateEndpointBulkGolden = flag.Bool("update-endpoint-bulk", false, "rewrite cmd/mashctl/testdata/endpoint_bulk.golden.json")

func bulkDoc(t *testing.T, so string) epBulkDoc {
	t.Helper()
	var d epBulkDoc
	if err := json.Unmarshal([]byte(so), &d); err != nil {
		t.Fatalf("json %q: %v", so, err)
	}
	return d
}

// credsPub is the user public key of the creds file of a.
func credsPub(t *testing.T, h *fleetEnv, a sap.Address) string {
	t.Helper()
	pub, err := epCredsUser(h.creds(a), a)
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func TestEndpointBulkPlannedChangesNothing(t *testing.T) {
	h := newFleetEnv(t)
	calls := fakeRevoke(t, nil)
	h.seed(ad("a"))
	h.seed(ad("b"))
	before := epSnapshot(t, h, h.dir)
	for _, extra := range [][]string{nil, {"--yes", "--dry-run"}} {
		args := append([]string{"endpoint", "reissue", "--all", "--make-key", "--json"}, extra...)
		so, se, code := h.run(args...)
		if code != 0 {
			t.Fatalf("%d %q %q", code, so, se)
		}
		d := bulkDoc(t, so)
		if d.Outcome != "planned" || d.Counts.Planned != 2 || len(d.Endpoints) != 2 || d.Endpoints[0].Outcome != "planned" {
			t.Fatalf("doc %q", so)
		}
		if epSnapshot(t, h, h.dir) != before || len(*calls) != 0 {
			t.Fatal("the plan changed state")
		}
	}
	so, _, code := h.run("endpoint", "reissue", "--all", "--make-key")
	if code != 0 || !strings.Contains(so, "would reissue o/g/a\n") || !strings.Contains(so, "nothing was changed") {
		t.Fatalf("text %d %q", code, so)
	}
}

func TestEndpointBulkAll(t *testing.T) {
	h := newFleetEnv(t)
	calls := fakeRevoke(t, nil)
	setFakeAcked(t, 2)
	a, b := ad("a"), ad("b")
	h.seed(a)
	h.seed(b)
	oldA, oldB := credsPub(t, h, a), credsPub(t, h, b)
	so, se, code := h.run("endpoint", "reissue", "--all", "--make-key", "--yes", "--json")
	if code != 0 {
		t.Fatalf("%d %q %q", code, so, se)
	}
	d := bulkDoc(t, so)
	if d.Outcome != "reissued" || d.Counts.Reissued != 2 || len(d.Endpoints) != 2 || d.Endpoints[0].Address != a.String() {
		t.Fatalf("doc %q", so)
	}
	noSecrets(t, so, se)
	for _, e := range d.Endpoints {
		if e.ServersAcknowledged == nil || *e.ServersAcknowledged != 2 {
			t.Errorf("%s: servers_acknowledged in %q", e.Address, so)
		}
	}
	for _, x := range []struct {
		a   sap.Address
		old string
	}{{a, oldA}, {b, oldB}} {
		rec, _ := h.record(x.a)
		now := credsPub(t, h, x.a)
		if now == x.old || rec.User != now {
			t.Errorf("%s: record %s creds %s old %s", x.a, rec.User, now, x.old)
		}
		if fileMode(t, h.creds(x.a)) != 0o600 {
			t.Error("mode")
		}
	}
	if len(*calls) != 2 {
		t.Errorf("revoke calls %d", len(*calls))
	}
	if tx, _, c := h.run("endpoint", "reissue", "--all", "--make-key", "--yes"); c != 0 || strings.Count(tx, "servers acknowledged the account update: 2\n") != 2 {
		t.Errorf("text %d %q", c, tx)
	}
}

func TestEndpointBulkAllSkipsNoLocalCreds(t *testing.T) {
	h := newFleetEnv(t)
	calls := fakeRevoke(t, nil)
	a, b := ad("a"), ad("b")
	h.seed(a)
	h.seed(b)
	if err := os.Remove(h.creds(b)); err != nil {
		t.Fatal(err)
	}
	oldA := credsPub(t, h, a)
	recB, _ := h.record(b)
	so, se, code := h.run("endpoint", "reissue", "--all", "--make-key", "--yes", "--json")
	if code != 0 {
		t.Fatalf("%d %q %q", code, so, se)
	}
	d := bulkDoc(t, so)
	if d.Counts.Reissued != 1 || d.Counts.Skipped != 1 || len(d.Endpoints) != 2 {
		t.Fatalf("doc %q", so)
	}
	if e := d.Endpoints[1]; e.Address != b.String() || e.Outcome != "skipped" || e.Reason == nil || *e.Reason != "no_local_creds" {
		t.Errorf("skipped entry %+v", e)
	}
	if credsPub(t, h, a) == oldA {
		t.Error("the endpoint with creds was not reissued")
	}
	if len(*calls) != 1 {
		t.Errorf("revoke calls %+v", *calls)
	}
	for _, c := range *calls {
		if c.users[0] == recB.User {
			t.Error("the endpoint with no creds had its key revoked")
		}
	}
	if rec, _ := h.record(b); rec.User != recB.User {
		t.Errorf("record key changed: %s to %s", recB.User, rec.User)
	}
	if _, err := os.Lstat(h.creds(b)); !os.IsNotExist(err) {
		t.Error("a creds file was made for the endpoint with none")
	}
	tx, _, code := h.run("endpoint", "reissue", "--all", "--make-key")
	if want := "skipped o/g/b (no_local_creds): its creds are not on this host; reissue it on its own machine with: mash key create, then mashctl endpoint reissue o/g/b --public-key FILE\n"; code != 0 || !strings.Contains(tx, want) {
		t.Errorf("text %d %q", code, tx)
	}
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
	if _, se, code := h.run("endpoint", "create", a.String(), "--public-key", file, "--jwt-out", filepath.Join(out, "k.jwt")); code != 0 {
		t.Fatalf("create: %q", se)
	}
	for _, extra := range [][]string{{"--dry-run"}, nil} {
		args := append([]string{"endpoint", "reissue", a.String(), "--public-key", file, "--jwt-out", filepath.Join(out, "n.jwt"), "--json"}, extra...)
		so, se, code := h.run(args...)
		var d epDoc
		if err := json.Unmarshal([]byte(so), &d); err != nil || code != 1 || d.Refusal == nil || d.Refusal.Code != "key_unchanged" || len(d.NextSteps) != 1 || !strings.Contains(d.NextSteps[0].Detail, "mash key create") {
			t.Fatalf("%v: %d %q %q", extra, code, so, se)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("revoke calls %+v", *calls)
	}
	if _, err := os.Lstat(filepath.Join(out, "n.jwt")); !os.IsNotExist(err) {
		t.Error("a JWT was written")
	}
	// the restore case: the issue record is gone, the same key is recorded again
	h.deleteIssued(pub)
	if _, se, code := h.run("endpoint", "reissue", a.String(), "--public-key", file, "--jwt-out", filepath.Join(out, "n.jwt"), "--dry-run"); code != 0 {
		t.Fatalf("dry-run restore: %d %q", code, se)
	}
	if _, ok := h.issued()[pub]; ok {
		t.Fatal("the dry-run recorded the key")
	}
	if so, se, code := h.run("endpoint", "reissue", a.String(), "--public-key", file, "--jwt-out", filepath.Join(out, "n.jwt")); code != 0 {
		t.Fatalf("restore: %d %q %q", code, so, se)
	}
	if rec, ok := h.issued()[pub]; !ok || rec.Address != a.Flat() {
		t.Errorf("the issue record is not back: %+v", h.issued())
	}
}

func TestEndpointBulkChanged(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	a, b, c := ad("a"), ad("b"), ad("c")
	h.seed(a)
	h.seedWith(b, func(uc *jwt.UserClaims) { uc.Permissions.Pub.Allow.Add("extra.subject") })
	h.seed(c)
	if err := os.Remove(h.creds(c)); err != nil {
		t.Fatal(err)
	}
	credsA, _ := os.ReadFile(h.creds(a))
	oldB := credsPub(t, h, b)
	so, se, code := h.run("endpoint", "reissue", "--changed", "--make-key", "--yes", "--json")
	if code != 0 {
		t.Fatalf("%d %q %q", code, so, se)
	}
	d := bulkDoc(t, so)
	if d.Outcome != "reissued" || d.Counts.Reissued != 1 || d.Counts.Skipped != 1 || len(d.Endpoints) != 2 {
		t.Fatalf("doc %q", so)
	}
	if e := d.Endpoints[1]; e.Address != c.String() || e.Outcome != "skipped" || e.Reason == nil || *e.Reason != "no_local_creds" {
		t.Errorf("skipped entry %+v", e)
	}
	if got, _ := os.ReadFile(h.creds(a)); string(got) != string(credsA) {
		t.Error("the endpoint with current creds was reissued")
	}
	if credsPub(t, h, b) == oldB {
		t.Error("the stale endpoint was not reissued")
	}
	if _, err := os.Lstat(h.creds(c)); !os.IsNotExist(err) {
		t.Error("a creds file was made for the endpoint with none")
	}
	// the second run has nothing to do
	so, _, code = h.run("endpoint", "reissue", "--changed", "--make-key", "--yes", "--json")
	d = bulkDoc(t, so)
	if code != 0 || d.Outcome != "unchanged" || d.Counts.Reissued != 0 {
		t.Fatalf("second run %d %q", code, so)
	}
}

func TestEndpointBulkRefusalDoesNotStopOthers(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	a, b, c := ad("a"), ad("b"), ad("c")
	h.seed(a)
	h.seed(b)
	h.seed(c)
	oldB := credsPub(t, h, b)
	// a lock left on b: its reissue is refused
	lock := h.creds(b) + ".lock"
	holdLock(t, lock)
	prev := epLockWait
	epLockWait = 20 * time.Millisecond
	t.Cleanup(func() { epLockWait = prev })
	so, _, code := h.run("endpoint", "reissue", "--all", "--make-key", "--yes", "--json")
	if code != 5 {
		t.Fatalf("code %d: %q", code, so)
	}
	d := bulkDoc(t, so)
	if d.Outcome != "partial" || d.Counts.Reissued != 2 || d.Counts.Refused != 1 {
		t.Fatalf("doc %q", so)
	}
	if e := d.Endpoints[1]; e.Outcome != "refused" || e.Reason == nil || *e.Reason != "reissue_busy" {
		t.Errorf("entry %+v", e)
	}
	if credsPub(t, h, b) != oldB {
		t.Error("the refused endpoint changed")
	}
	// every endpoint refused: exit 1
	for _, x := range []sap.Address{a, c} {
		holdLock(t, h.creds(x)+".lock")
	}
	so, _, code = h.run("endpoint", "reissue", "--all", "--make-key", "--yes", "--json")
	if d := bulkDoc(t, so); code != 1 || d.Outcome != "refused" || d.Counts.Refused != 3 || d.Refusal == nil {
		t.Fatalf("all refused: %d %q", code, so)
	}
}

func TestEndpointBulkUsage(t *testing.T) {
	h := newFleetEnv(t)
	out := t.TempDir()
	_, pubFile, _ := epKey(t, out, "k")
	cases := []struct {
		name string
		args []string
		code string
	}{
		{"public key", []string{"--all", "--public-key", pubFile, "--yes"}, "bulk_needs_make_key"},
		{"no key flag", []string{"--changed", "--yes"}, "bulk_needs_make_key"},
		{"address", []string{ad("a").String(), "--all", "--make-key", "--yes"}, "usage"},
		{"both", []string{"--all", "--changed", "--make-key", "--yes"}, "usage"},
	}
	for _, c := range cases {
		so, _, code := h.run(append([]string{"endpoint", "reissue", "--json"}, c.args...)...)
		d := bulkDoc(t, so)
		if code != 2 || d.Refusal == nil || d.Refusal.Code != c.code {
			t.Errorf("%s: %d %q", c.name, code, so)
		}
	}
}

func TestEndpointBulkGolden(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	h.seed(ad("a"))
	h.seedWith(ad("b"), func(uc *jwt.UserClaims) { uc.Permissions.Sub.Allow.Add("extra.subject") })
	h.seed(ad("c"))
	if err := os.Remove(h.creds(ad("c"))); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	step := func(label string, args ...string) {
		so, _, code := h.run(args...)
		b.WriteString("# " + label + " exit " + string(rune('0'+code)) + "\n" + h.scrub(so))
	}
	step("planned", "endpoint", "reissue", "--changed", "--make-key", "--json")
	step("reissued", "endpoint", "reissue", "--changed", "--make-key", "--yes", "--json")
	step("unchanged", "endpoint", "reissue", "--changed", "--make-key", "--yes", "--json")
	step("all_planned", "endpoint", "reissue", "--all", "--make-key", "--json")
	step("bulk_needs_make_key", "endpoint", "reissue", "--all", "--public-key", "k.pub", "--yes", "--json")
	golden := filepath.Join("testdata", "endpoint_bulk.golden.json")
	if *updateEndpointBulkGolden {
		if err := os.WriteFile(golden, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("golden file: %v", err)
	}
	if b.String() != string(want) {
		t.Fatalf("differs from %s (run with -update-endpoint-bulk after checking the diff)\n got: %s\nwant: %s", golden, b.String(), want)
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

func TestEndpointCreateMakeKeyKilledAfterEnroll(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	a := ad("k1")
	args := []string{"endpoint", "create", a.String(), "--make-key", "--json"}
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
	if code != 0 {
		t.Fatalf("rerun %d %q %q", code, so, se)
	}
	noSecrets(t, so, se)
	if d := epDocOf(t, so); d.Outcome != "created" {
		t.Errorf("outcome %q", d.Outcome)
	}
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

func TestEndpointCreateMakeKeyKilledAfterStage(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	a := ad("k2")
	args := []string{"endpoint", "create", a.String(), "--make-key"}
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

func TestEndpointCreateMakeKeyCredsExist(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	a := ad("k3")
	args := []string{"endpoint", "create", a.String(), "--make-key", "--json"}
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
	if code != 0 || epDocOf(t, so).Outcome != "created" {
		t.Fatalf("same key: %d %q %q", code, so, se)
	}
	noPending(t, h)
	// a pending file for another key: refused, nothing changed
	_, _, other := epKey(t, t.TempDir(), "o")
	if err := os.WriteFile(pending, other, 0o600); err != nil {
		t.Fatal(err)
	}
	before := epSnapshot(t, h, h.dir)
	so, _, code = h.run(args...)
	if d := epDocOf(t, so); code != 1 || d.Refusal == nil || d.Refusal.Code != "creds_exists" {
		t.Fatalf("other key: %d %q", code, so)
	}
	if epSnapshot(t, h, h.dir) != before {
		t.Error("a refusal changed state")
	}
}

func TestEndpointCreateMakeKeyDryRunStagesNothing(t *testing.T) {
	h := newFleetEnv(t)
	before := epSnapshot(t, h, h.dir)
	if _, se, code := h.run("endpoint", "create", ad("k4").String(), "--make-key", "--dry-run"); code != 0 {
		t.Fatalf("%d %q", code, se)
	}
	if epSnapshot(t, h, h.dir) != before {
		t.Error("dry-run changed state")
	}
	noPending(t, h)
}

func TestEndpointReissueMakeKeyKilledAfterEnroll(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	a := ad("k5")
	if _, se, code := h.run("endpoint", "create", a.String(), "--make-key"); code != 0 {
		t.Fatalf("create %q", se)
	}
	old := credsPub(t, h, a)
	args := []string{"endpoint", "reissue", a.String(), "--make-key"}
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

func epDocOf(t *testing.T, so string) epDoc {
	t.Helper()
	var d epDoc
	if err := json.Unmarshal([]byte(so), &d); err != nil {
		t.Fatalf("json %q: %v", so, err)
	}
	return d
}

func TestEndpointDeleteFinishesAfterKill(t *testing.T) {
	h := newFleetEnv(t)
	a := ad("d1")
	h.seed(a)
	retireNow = func() time.Time { return time.Date(2026, 11, 2, 3, 4, 5, 0, time.UTC) }
	t.Cleanup(func() { retireNow = time.Now })
	creds, _ := os.ReadFile(h.creds(a))
	if _, se, code := h.run("endpoint", "delete", "o/g/d1"); code != 0 {
		t.Fatalf("delete %q", se)
	}
	// the state of a kill before the creds step: the record is gone, the address burned, the creds file in place
	retired := h.creds(a) + ".retired-20261102T030405Z"
	if err := os.Rename(retired, h.creds(a)); err != nil {
		t.Fatal(err)
	}
	so, se, code := h.run("endpoint", "delete", "o/g/d1", "--json")
	if code != 0 {
		t.Fatalf("rerun %d %q %q", code, so, se)
	}
	var d deleteJSON
	if err := json.Unmarshal([]byte(so), &d); err != nil || d.Outcome != "deleted" || d.Creds.Action != "renamed" {
		t.Fatalf("doc %q %v", so, err)
	}
	if got, err := os.ReadFile(retired); err != nil || string(got) != string(creds) {
		t.Errorf("retired file: %v", err)
	}
	if _, err := os.Lstat(h.creds(a)); !os.IsNotExist(err) {
		t.Error("creds file still in place")
	}
	// --delete-creds finishes it as well
	if err := os.WriteFile(h.creds(a), creds, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, se, code := h.run("endpoint", "delete", "o/g/d1", "--delete-creds"); code != 0 {
		t.Fatalf("delete-creds %d %q", code, se)
	}
	if _, err := os.Lstat(h.creds(a)); !os.IsNotExist(err) {
		t.Error("creds file not removed")
	}
}

func TestEndpointDeleteNoRecordNotBurnedStillRefuses(t *testing.T) {
	h := newFleetEnv(t)
	a := ad("d2")
	if err := os.WriteFile(h.creds(a), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	so, _, code := h.run("endpoint", "delete", "o/g/d2", "--json")
	var d deleteJSON
	if err := json.Unmarshal([]byte(so), &d); err != nil || code != 1 || d.Refusal == nil || d.Refusal.Code != "not_found" {
		t.Fatalf("%d %q", code, so)
	}
	if _, err := os.Lstat(h.creds(a)); err != nil {
		t.Error("the creds file was touched")
	}
}
