package endpoint

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/sap"
)

const refusedText = "refused by policy: you may not mail "

// restrictedSender provisions sender "s" whose permissions are admin.Permissions with the owner-wide mail line
// replaced by the mail subjects of the allowed recipients only, and returns its client. (admin.PermissionsTo will do
// this once it lands.)
func (f *fixture) restrictedSender(t *testing.T, allowed ...string) *Endpoint {
	t.Helper()
	p := admin.Permissions(f.e.Owner, "s")
	wide := "agents.mail.*." + f.e.Owner + ".*"
	var pub []string
	for _, s := range p.Pub.Allow {
		if s == wide {
			for _, n := range allowed {
				pub = append(pub, sap.For(f.e.Owner, n).Mail)
			}
			continue
		}
		pub = append(pub, s)
	}
	if len(pub) != len(p.Pub.Allow)-1+len(allowed) {
		t.Fatalf("permissions template has no %q line: %v", wide, p.Pub.Allow)
	}
	p.Pub.Allow = pub
	c, u := f.e.MintRaw(t, "s", p)
	if _, err := f.adm.Provision(context.Background(), "s", u, f.e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	f.creds["s"], f.users["s"] = c, u
	return f.client(t, "s")
}

func wantRefused(t *testing.T, err error, to string, took time.Duration) {
	t.Helper()
	var re *RefusedError
	if !errors.As(err, &re) {
		t.Fatalf("send to %s: %v (after %v), want *RefusedError", to, err, took)
	}
	if re.To != to || err.Error() != refusedText+to {
		t.Fatalf("refusal %+v text %q", re, err.Error())
	}
	if !errors.Is(err, &RefusedError{}) || !errors.Is(err, &RefusedError{To: to}) || errors.Is(err, &RefusedError{To: "zz"}) {
		t.Fatalf("errors.Is on %v", err)
	}
	var te *SendTimeoutError
	if errors.As(err, &te) {
		t.Fatalf("a refusal is not a timeout")
	}
	if took > 500*time.Millisecond {
		t.Fatalf("refusal took %v, want well under the 5 s timeout", took)
	}
}

func timedSend(ep *Endpoint, to string) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, err := ep.Send(ctx, to, "hello "+to, 0)
	return time.Since(start), err
}

func TestSendToADeniedRecipientFailsFast(t *testing.T) {
	f := newFixture(t, "ok", "ok2", "denied", "denied2")
	s := f.restrictedSender(t, "ok", "ok2")

	if _, err := timedSend(s, "ok"); err != nil {
		t.Fatalf("allowed send before: %v", err)
	}
	took, err := timedSend(s, "denied")
	wantRefused(t, err, "denied", took)
	var re *RefusedError
	errors.As(err, &re)
	if re.Subject != sap.For(f.e.Owner, "denied").Mail {
		t.Fatalf("subject %q", re.Subject)
	}
	if _, err := timedSend(s, "ok"); err != nil {
		t.Fatalf("allowed send after: %v", err)
	}
	if n := inboxMsgs(t, f, "denied"); n != 0 {
		t.Fatalf("denied inbox holds %d messages", n)
	}
	if n := inboxMsgs(t, f, "ok"); n != 2 {
		t.Fatalf("allowed inbox holds %d messages, want 2", n)
	}
	if n := s.refusals.pending(); n != 0 {
		t.Fatalf("%d waiters left registered", n)
	}
}

func TestConcurrentSendsToADeniedRecipientBothFailFast(t *testing.T) {
	f := newFixture(t, "ok", "denied")
	s := f.restrictedSender(t, "ok")
	for round := 0; round < 3; round++ {
		var wg sync.WaitGroup
		took := make([]time.Duration, 2)
		errs := make([]error, 2)
		for i := range errs {
			wg.Add(1)
			go func() { defer wg.Done(); took[i], errs[i] = timedSend(s, "denied") }()
		}
		wg.Wait()
		for i := range errs {
			wantRefused(t, errs[i], "denied", took[i])
		}
	}
	if n := s.refusals.pending(); n != 0 {
		t.Fatalf("%d waiters left registered", n)
	}
}

func TestDeniedAndAllowedConcurrentSendsDoNotInterfere(t *testing.T) {
	f := newFixture(t, "ok", "ok2", "denied", "denied2")
	s := f.restrictedSender(t, "ok", "ok2")
	var wg sync.WaitGroup
	type out struct {
		took time.Duration
		err  error
	}
	res := map[string]*out{}
	for _, n := range []string{"ok", "ok2", "denied", "denied2"} {
		for i := 0; i < 3; i++ {
			res[fmt.Sprintf("%s/%d", n, i)] = &out{}
		}
	}
	for _, n := range []string{"ok", "ok2", "denied", "denied2"} {
		for i := 0; i < 3; i++ {
			key := fmt.Sprintf("%s/%d", n, i)
			o := res[key]
			wg.Add(1)
			go func() { defer wg.Done(); o.took, o.err = timedSend(s, n) }()
		}
	}
	wg.Wait()
	for key, o := range res {
		to := key[:len(key)-2]
		if to == "denied" || to == "denied2" {
			wantRefused(t, o.err, to, o.took)
		} else if o.err != nil {
			t.Fatalf("allowed send %s: %v", key, o.err)
		}
	}
	if inboxMsgs(t, f, "ok") != 3 || inboxMsgs(t, f, "ok2") != 3 || inboxMsgs(t, f, "denied") != 0 {
		t.Fatalf("inbox counts wrong")
	}
	if n := s.refusals.pending(); n != 0 {
		t.Fatalf("%d waiters left registered", n)
	}
}

// A violation nobody waits on is dropped, and leaves no state that changes a later send.
func TestViolationWithNoWaiterIsDropped(t *testing.T) {
	f := newFixture(t, "ok", "denied")
	s := f.restrictedSender(t)
	subj := sap.For(f.e.Owner, "denied").Mail
	s.refusals.note(fmt.Errorf("%w: Permissions Violation for Publish to %q", nats.ErrPermissionViolation, subj))
	if n := s.refusals.pending(); n != 0 {
		t.Fatalf("%d waiters", n)
	}
	took, err := timedSend(s, "denied")
	wantRefused(t, err, "denied", took)
	if n := s.refusals.pending(); n != 0 {
		t.Fatalf("%d waiters left registered", n)
	}
}

// A stale violation for a subject, delivered while a send to a different subject waits, does not refuse that send.
func TestViolationForAnotherSubjectDoesNotRefuse(t *testing.T) {
	f := newFixture(t, "ok", "denied")
	s := f.restrictedSender(t, "ok")
	ch, release := s.refusals.register(sap.For(f.e.Owner, "ok").Mail)
	defer release()
	s.refusals.note(fmt.Errorf("%w: Permissions Violation for Publish to %q", nats.ErrPermissionViolation, "agents.mail.cc.o.other"))
	s.refusals.note(fmt.Errorf("%w: Permissions Violation for Subscription to %q", nats.ErrPermissionViolation, sap.For(f.e.Owner, "ok").Mail))
	select {
	case <-ch:
		t.Fatal("waiter signalled by an unrelated violation")
	default:
	}
}

// When the text does not parse, a denied send behaves as it did before: it waits out its deadline and reports a timeout.
func TestUnparseableViolationFallsBackToTimeout(t *testing.T) {
	f := newFixture(t, "ok", "denied")
	s := f.restrictedSender(t, "ok")
	s.refusals.parse = func(error) (string, bool) { return "", false }

	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	_, err := s.Send(ctx, "denied", "x", 0)
	var te *SendTimeoutError
	if !errors.As(err, &te) {
		t.Fatalf("got %v, want *SendTimeoutError", err)
	}
	var re *RefusedError
	if errors.As(err, &re) {
		t.Fatal("reported a refusal for text that did not parse")
	}

	// The real parser rejects a violation text without the quoted subject.
	for _, bad := range []string{"nats: permissions violation: something new", `Permissions Violation for Publish to ""`, `Publish to "unterminated`} {
		if sub, ok := parseViolation(fmt.Errorf("%w: %s", nats.ErrPermissionViolation, bad)); ok {
			t.Fatalf("%q parsed as %q", bad, sub)
		}
	}
	// A synthetic error that is not a permission violation never signals.
	ch, release := s.refusals.register("x.y")
	defer release()
	s.refusals.note(errors.New(`Permissions Violation for Publish to "x.y"`))
	select {
	case <-ch:
		t.Fatal("signalled by a non-violation error")
	default:
	}
	if n := s.refusals.pending(); n != 1 {
		t.Fatalf("pending %d", n)
	}
}
