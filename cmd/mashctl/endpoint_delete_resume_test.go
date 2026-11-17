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
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// killedDelete leaves what a delete killed after the directory purge leaves: no record, a retirement marker in the
// given state, and the inbox stream still there.
func (h *fleetEnv) killedDelete(a sap.Address, state string) {
	h.t.Helper()
	rec, ok := h.record(a)
	if !ok {
		h.t.Fatal("no record")
	}
	js, err := jetstream.New(h.e.Conn(h.t, h.e.AdminCreds))
	if err != nil {
		h.t.Fatal(err)
	}
	ctx := context.Background()
	rk, err := js.KeyValue(ctx, store.RetiredBucket)
	if err != nil {
		h.t.Fatal(err)
	}
	m := store.Retired{Owner: a.Owner, Group: a.Group, Instance: a.Instance, User: rec.User,
		RetiredAt: time.Now().UTC().Format(time.RFC3339), State: state}
	v, _ := json.Marshal(m)
	if _, err := rk.Put(ctx, a.Flat(), v); err != nil {
		h.t.Fatal(err)
	}
	dk, err := js.KeyValue(ctx, store.DirectoryBucket)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := dk.Purge(ctx, a.Flat()); err != nil {
		h.t.Fatal(err)
	}
}

func (h *fleetEnv) assertCleaned(a sap.Address) {
	h.t.Helper()
	adm := h.adminOf()
	if left, err := adm.Exists(context.Background(), a); err != nil || left {
		h.t.Errorf("inbox or lease left: %v %v", left, err)
	}
	if st, err := adm.RetireState(context.Background(), a); err != nil || st != "final" {
		h.t.Errorf("marker %q %v", st, err)
	}
}

func TestEndpointDeleteFinishesAfterKilledPurge(t *testing.T) {
	for _, withCreds := range []bool{true, false} {
		for _, state := range []string{store.RetiredPending, store.RetiredFinal} {
			name := state
			if !withCreds {
				name += "-no-creds"
			}
			t.Run(name, func(t *testing.T) {
				h := newFleetEnv(t)
				fakeRevoke(t, nil)
				a := ad("kd")
				h.seed(a)
				h.killedDelete(a, state)
				if !withCreds {
					if err := os.Remove(h.creds(a)); err != nil {
						t.Fatal(err)
					}
				}
				so, se, code := h.run("endpoint", "delete", slashForm(a))
				if code != 0 {
					t.Fatalf("%d %q %q", code, so, se)
				}
				h.assertCleaned(a)
				m, _ := filepath.Glob(h.creds(a) + ".retired-*")
				if withCreds && len(m) != 1 {
					t.Errorf("creds not renamed: %v", m)
				}
				if !strings.Contains(so, "finished the retirement") {
					t.Errorf("text: %q", so)
				}
			})
		}
	}
}

func TestEndpointDeleteFinalMarkerNothingLeftIsUnchanged(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	a := ad("un")
	h.seed(a)
	if _, se, code := h.run("endpoint", "delete", slashForm(a)); code != 0 {
		t.Fatalf("first: %d %q", code, se)
	}
	if err := os.Remove(h.creds(a)); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	so, se, code := h.run("endpoint", "delete", slashForm(a), "--json")
	if code != 0 || !strings.Contains(so, `"outcome": "unchanged"`) {
		t.Fatalf("%d %q %q", code, so, se)
	}
}

func TestEndpointDeleteNeverExistedIsNotFound(t *testing.T) {
	h := newFleetEnv(t)
	so, se, code := h.run("endpoint", "delete", "o/g/ghost", "--json")
	if code != 1 || !strings.Contains(so, "not_found") {
		t.Fatalf("%d %q %q", code, so, se)
	}
}

func TestEndpointDeleteCleanupFailureIsServerError(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	a := ad("cf")
	h.seed(a)
	h.killedDelete(a, store.RetiredPending)
	// a marker the admin cannot read makes the cleanup fail; the creds file must be left alone
	js, _ := jetstream.New(h.e.Conn(t, h.e.AdminCreds))
	rk, _ := js.KeyValue(context.Background(), store.RetiredBucket)
	if _, err := rk.Put(context.Background(), a.Flat(), []byte("not json")); err != nil {
		t.Fatal(err)
	}
	so, se, code := h.run("endpoint", "delete", slashForm(a), "--json")
	if code != 6 || !strings.Contains(so, `"server"`) {
		t.Fatalf("%d %q %q", code, so, se)
	}
	if _, err := os.Lstat(h.creds(a)); err != nil {
		t.Error("creds file touched after a failed cleanup")
	}
}

func TestEndpointReissueFutureIssuedCheckedUnderTheLock(t *testing.T) {
	h := newFleetEnv(t)
	calls := fakeRevoke(t, nil)
	a := ad("hk")
	h.seed(a)
	rec, _ := h.record(a)
	epBeforeRevokeCheck = func() { h.recordIssuedAt(rec.User, time.Now().Add(time.Hour)) }
	t.Cleanup(func() { epBeforeRevokeCheck = nil })
	so, se, code := h.run("endpoint", "reissue", a.String(), "--make-key", "--json")
	d := epDocOf(t, so)
	if code != 1 || d.Refusal == nil || d.Refusal.Code != "future_issued" {
		t.Fatalf("%d %q %q", code, so, se)
	}
	if len(*calls) != 0 {
		t.Errorf("the key was revoked: %+v", *calls)
	}
	if r, _ := h.record(a); r.User != rec.User {
		t.Error("directory key changed")
	}
}

func TestEndpointPublishStagedSyncsBeforeRemovingPending(t *testing.T) {
	acct, _ := nkeys.CreateAccount()
	user, _ := nkeys.CreateUser()
	upub, _ := user.PublicKey()
	useed, _ := user.Seed()
	tok, err := jwt.NewUserClaims(upub).Encode(acct)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	creds := filepath.Join(dir, "x.creds")
	pending := creds + ".pending"
	if err := os.WriteFile(creds, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pending, []byte("seed"), 0o600); err != nil {
		t.Fatal(err)
	}
	var pendingAtSync bool
	epSyncDir = func(string) error {
		_, err := os.Lstat(pending)
		pendingAtSync = err == nil
		return nil
	}
	t.Cleanup(func() { epSyncDir = syncDir })
	if err := epPublishStaged(tok, useed, creds, pending, true); err != nil {
		t.Fatal(err)
	}
	if !pendingAtSync {
		t.Error("the pending file was gone before the directory sync")
	}
	if _, err := os.Lstat(pending); !os.IsNotExist(err) {
		t.Error("pending file left")
	}
	// a sync error is an error and keeps the pending file
	if err := os.WriteFile(pending, []byte("seed"), 0o600); err != nil {
		t.Fatal(err)
	}
	epSyncDir = func(string) error { return os.ErrInvalid }
	if err := epPublishStaged(tok, useed, creds, pending, true); err == nil {
		t.Fatal("sync error ignored")
	}
	if _, err := os.Lstat(pending); err != nil {
		t.Error("pending file removed after a failed sync")
	}
}
