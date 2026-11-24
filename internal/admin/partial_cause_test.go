package admin_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
)

// wantCause checks err is a partial removal that names cause (plain words, 28 characters or fewer), and that the
// cause carries none of the error's own words.
func wantCause(t *testing.T, err error, cause string) {
	t.Helper()
	got, ok := admin.PartialCause(err)
	if !ok || got != cause {
		t.Fatalf("PartialCause = %q, %v; want %q (error: %v)", got, ok, cause, err)
	}
	if len(got) > 28 || strings.ContainsAny(got, ".;:,") {
		t.Errorf("cause %q is over 28 characters or has punctuation", got)
	}
}

func sealStream(t *testing.T, js jetstream.JetStream, stream string) {
	t.Helper()
	s, err := js.Stream(context.Background(), stream)
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.CachedInfo().Config
	cfg.Sealed = true
	if _, err := js.UpdateStream(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
}

// each case: the key was revoked by a hook that also breaks the next step.
func partialWith(t *testing.T, step func(js jetstream.JetStream, user string), drained bool) (error, string) {
	t.Helper()
	e, a, js := setup(t)
	provisionFor(t, e, a, "w")
	user := recordUserOf(t, js, "w")
	hook := func(ctx context.Context, u string) error {
		step(js, user)
		return nil
	}
	ra := a.WithRevoker(hook)
	if drained {
		st, err := a.Suspend(context.Background(), ep(t, "w"))
		if err != nil {
			t.Fatal(err)
		}
		return ra.DeprovisionDrained(context.Background(), ep(t, "w"), st.ID, st.LastSeq), user
	}
	return ra.DeprovisionIfUser(context.Background(), ep(t, "w"), user), user
}

func TestPartialCauseLockLost(t *testing.T) {
	err, _ := partialWith(t, func(js jetstream.JetStream, _ string) { putLock(t, js, "o_dev_w", time.Now().Add(time.Minute)) }, false)
	wantCause(t, err, "its lock was lost")
}

func TestPartialCauseLockExpiring(t *testing.T) {
	t.Cleanup(admin.SetLockTiming(expiryTTL, longWait, fastPoll))
	t.Cleanup(admin.SetLockSafety(expirySafety))
	err, _ := partialWith(t, func(jetstream.JetStream, string) { time.Sleep(expiryStall) }, true)
	wantCause(t, err, "its lock was lost")
}

func TestPartialCauseRecordCouldNotBeRead(t *testing.T) {
	err, _ := partialWith(t, func(js jetstream.JetStream, _ string) { corruptRecord(t, js, "w") }, false)
	wantCause(t, err, "its record could not be read")
}

func TestPartialCauseRecordChanged(t *testing.T) {
	// the record is replaced by another user's: the guarded purge is refused, and the next read is not this key's
	e, a, js := setup(t)
	provisionFor(t, e, a, "w")
	user := recordUserOf(t, js, "w")
	_, other := e.Mint(t, ep(t, "other"))
	hook := func(context.Context, string) error { putUser(t, js, "w", other); return nil }
	err := a.WithRevoker(hook).DeprovisionIfUser(context.Background(), ep(t, "w"), user)
	wantCause(t, err, "its record changed meanwhile")

	// the record vanishes meanwhile
	e, a, js = setup(t)
	provisionFor(t, e, a, "w")
	user = recordUserOf(t, js, "w")
	hook = func(context.Context, string) error {
		kv, _ := js.KeyValue(context.Background(), store.DirectoryBucket)
		return kv.Delete(context.Background(), ep(t, "w").Flat())
	}
	err = a.WithRevoker(hook).DeprovisionIfUser(context.Background(), ep(t, "w"), user)
	wantCause(t, err, "its record changed meanwhile")

	// the same record, a new revision, found by DeprovisionDrained
	err, _ = partialWith(t, func(js jetstream.JetStream, user string) { putUser(t, js, "w", user) }, true)
	wantCause(t, err, "its record changed meanwhile")
}

func TestPartialCauseRecordRemovalNotConfirmed(t *testing.T) {
	for _, drained := range []bool{false, true} {
		prev := admin.SetDirectoryPurgeFault(func() error { return context.DeadlineExceeded })
		err, _ := partialWith(t, func(jetstream.JetStream, string) {}, drained)
		admin.SetDirectoryPurgeFault(prev)
		wantCause(t, err, "record removal not confirmed")
	}
}

func TestPartialCauseNameNotFreed(t *testing.T) {
	err, _ := partialWith(t, func(js jetstream.JetStream, _ string) { sealStream(t, js, "KV_"+store.RetiredBucket) }, false)
	wantCause(t, err, "its name was not freed")
}

func TestPartialCauseAddressNotClosed(t *testing.T) {
	err, _ := partialWith(t, func(js jetstream.JetStream, _ string) { sealStream(t, js, store.LeaseStreamName) }, false)
	wantCause(t, err, "its address was not closed")
}

func TestPartialCauseInboxStillThere(t *testing.T) {
	admin.InboxDeleteFault = func() error { return errors.New("stream delete failed") }
	t.Cleanup(func() { admin.InboxDeleteFault = nil })
	err, _ := partialWith(t, func(jetstream.JetStream, string) {}, false)
	wantCause(t, err, "its inbox is still there")
}

func TestPartialCauseEarlierRemovalStuck(t *testing.T) {
	// the record was replaced (a refusal that deletes the pending marker), but the marker bucket refuses the delete:
	// the marker stays pending and blocks the name
	e, a, js := setup(t)
	provisionFor(t, e, a, "w")
	user := recordUserOf(t, js, "w")
	_, other := e.Mint(t, ep(t, "other"))
	hook := func(context.Context, string) error {
		putUser(t, js, "w", other)
		sealStream(t, js, "KV_"+store.RetiredBucket)
		return nil
	}
	err := a.WithRevoker(hook).DeprovisionIfUser(context.Background(), ep(t, "w"), user)
	wantCause(t, err, "an earlier removal is stuck")
	if !admin.IsPartial(err) || admin.IsRefusal(err) {
		t.Errorf("IsPartial/IsRefusal changed: %v", err)
	}
}

func TestPartialCauseIsOnlyForPartial(t *testing.T) {
	if c, ok := admin.PartialCause(errors.New("x")); ok || c != "" {
		t.Errorf("a plain error has a cause: %q %v", c, ok)
	}
	if _, ok := admin.PartialCause(nil); ok {
		t.Error("nil has a cause")
	}
}
