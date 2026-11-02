package endpoint

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/sap"
)

const refusedText = "refused by policy: you may not mail "

// refusedSuffix is the second sentence of every refusal: the pair may be allowed by the policy file while the
// credentials predate the rule, or the recipient may not be listed at all.
const refusedSuffix = ". If the policy file allows this pair, or lists the recipient at all (mashctl policy explain), " +
	"your credentials may be older than the rule: ask the admin to run mashctl policy drift, then mashctl reissue."

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
	if re.To != to || err.Error() != refusedText+to+refusedSuffix {
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

func violation(subject string) error {
	return fmt.Errorf("%w: Permissions Violation for Publish to %q", nats.ErrPermissionViolation, subject)
}

func signalled(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// One waiter and a violation is a definite refusal, with the new text.
func TestSoleWaiterViolationRefuses(t *testing.T) {
	r := newRefusals()
	ch, release := r.register("a.b", false)
	defer release()
	r.note(violation("a.b"))
	if !signalled(ch) {
		t.Fatal("sole waiter not signalled")
	}
	err := error(&RefusedError{To: "x", Subject: "a.b"})
	if err.Error() != refusedText+"x"+refusedSuffix {
		t.Fatalf("text %q", err.Error())
	}
}

// Changed from "concurrent sends are both signalled and fail fast": a violation cannot be attributed to one of
// several sends, so none is signalled and each stays pending until it is acked, times out or is released.
func TestSeveralWaitersViolationSignalsNone(t *testing.T) {
	r := newRefusals()
	c1, rel1 := r.register("a.b", false)
	c2, rel2 := r.register("a.b", false)
	r.note(violation("a.b"))
	if signalled(c1) || signalled(c2) {
		t.Fatal("an unattributable violation signalled a waiter")
	}
	if n := r.pending(); n != 2 {
		t.Fatalf("pending %d, want 2", n)
	}
	if !r.sawAmbiguous("a.b") {
		t.Fatal("ambiguity not recorded")
	}
	rel1()
	if !r.sawAmbiguous("a.b") || r.pending() != 1 {
		t.Fatal("flag must survive until the set empties")
	}
	rel2()
	if r.pending() != 0 || r.sawAmbiguous("a.b") {
		t.Fatal("flag leaked past the empty set")
	}
}

// A send after an overlap is a definite refusal again: the flag does not leak across sends.
func TestAmbiguityDoesNotLeakToALaterSend(t *testing.T) {
	r := newRefusals()
	_, rel1 := r.register("a.b", false)
	_, rel2 := r.register("a.b", false)
	r.note(violation("a.b"))
	rel1()
	rel2()
	ch, rel := r.register("a.b", false)
	defer rel()
	if r.sawAmbiguous("a.b") {
		t.Fatal("flag leaked")
	}
	r.note(violation("a.b"))
	if !signalled(ch) {
		t.Fatal("later sole send not refused")
	}
}

// A flagged send that times out says the violation could not be attributed; an unflagged one does not.
func TestAmbiguousTimeoutCarriesClause(t *testing.T) {
	const clause = "; a permission violation on this subject was seen while several sends were in flight, so it could not be attributed (mashctl policy explain shows whether the pair is allowed)"
	f := newFixture(t, "ok")
	s := f.restrictedSender(t, "ok")
	subj := sap.For(f.e.Owner, "ok").Mail
	expired := func() error {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // the send ends at its deadline, whatever the server would have said
		_, err := s.Send(ctx, "ok", "x", 0)
		return err
	}
	var te *SendTimeoutError
	if err := expired(); !errors.As(err, &te) || te.Ambiguous || strings.Contains(err.Error(), clause) {
		t.Fatalf("unflagged send: %v", err)
	}
	// Two other sends are in flight on the subject when a violation arrives.
	_, rel1 := s.refusals.register(subj, false)
	defer rel1()
	_, rel2 := s.refusals.register(subj, false)
	defer rel2()
	s.refusals.note(violation(subj))
	err := expired()
	if !errors.As(err, &te) || !te.Ambiguous || !strings.HasSuffix(err.Error(), clause) || !errors.Is(err, context.Canceled) {
		t.Fatalf("flagged send: %v", err)
	}
}

// An allowed send acked after a stray violation on its subject still succeeds.
func TestAllowedSendAckedAfterStrayViolationSucceeds(t *testing.T) {
	f := newFixture(t, "ok")
	s := f.restrictedSender(t, "ok")
	subj := sap.For(f.e.Owner, "ok").Mail
	s.refusals.note(violation(subj)) // no waiter: dropped
	if _, err := timedSend(s, "ok"); err != nil {
		t.Fatalf("allowed send: %v", err)
	}
	if n := s.refusals.pending(); n != 0 {
		t.Fatalf("%d waiters left", n)
	}
}

func TestDeniedAndAllowedConcurrentSendsDoNotInterfere(t *testing.T) {
	f := newFixture(t, "ok", "ok2", "denied", "denied2")
	s := f.restrictedSender(t, "ok", "ok2")
	var wg sync.WaitGroup
	// Denied recipients get one send each: several concurrent sends to one denied subject are not attributable and
	// would time out (see TestSeveralWaitersViolationSignalsNone).
	copies := func(n string) int {
		if n == "denied" || n == "denied2" {
			return 1
		}
		return 3
	}
	type out struct {
		took time.Duration
		err  error
	}
	res := map[string]*out{}
	for _, n := range []string{"ok", "ok2", "denied", "denied2"} {
		for i := 0; i < copies(n); i++ {
			res[fmt.Sprintf("%s/%d", n, i)] = &out{}
		}
	}
	for _, n := range []string{"ok", "ok2", "denied", "denied2"} {
		for i := 0; i < copies(n); i++ {
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
	ch, release := s.refusals.register(sap.For(f.e.Owner, "ok").Mail, false)
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
	ch, release := s.refusals.register("x.y", false)
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

// countingFlush replaces the barrier's flush with one that counts calls, runs during (if non-nil) and returns err.
func countingFlush(s *Endpoint, during func(), err error) *int {
	n := new(int)
	s.refusals.flush = func(context.Context) error {
		*n++
		if during != nil {
			during()
		}
		return err
	}
	return n
}

func cancelledSend(s *Endpoint, to string) error {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.Send(ctx, to, "x", 0)
	return err
}

// A violation delayed from a send that timed out arrives after the next send starts: the barrier delivers it before
// that send registers, so it is dropped and the next send is not refused.
func TestDelayedViolationFromTimedOutSendDoesNotRefuseNext(t *testing.T) {
	f := newFixture(t, "ok")
	s := f.restrictedSender(t, "ok")
	subj := sap.For(f.e.Owner, "ok").Mail
	var te *SendTimeoutError
	if err := cancelledSend(s, "ok"); !errors.As(err, &te) {
		t.Fatalf("send 1: %v", err)
	}
	n := countingFlush(s, func() { s.refusals.note(violation(subj)) }, nil)
	if _, err := timedSend(s, "ok"); err != nil {
		t.Fatalf("send 2 was refused or failed: %v", err)
	}
	if *n != 1 {
		t.Fatalf("flush called %d times, want 1", *n)
	}
	if _, err := timedSend(s, "ok"); err != nil || *n != 1 {
		t.Fatalf("send 3: %v, flush calls %d (barrier must run once per dirty mark)", err, *n)
	}
	if s.refusals.pending() != 0 {
		t.Fatal("waiters left")
	}
}

// When the barrier fails, the next send is unattributable: a real violation during it is not a definite refusal.
func TestFailedBarrierNeverReportsDefiniteRefusal(t *testing.T) {
	f := newFixture(t, "ok", "denied")
	s := f.restrictedSender(t, "ok")
	var te *SendTimeoutError
	if err := cancelledSend(s, "denied"); !errors.As(err, &te) {
		t.Fatalf("send 1: %v", err)
	}
	n := countingFlush(s, nil, errors.New("flush failed"))
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	_, err := s.Send(ctx, "denied", "x", 0)
	var re *RefusedError
	if errors.As(err, &re) || !errors.As(err, &te) || !te.Ambiguous {
		t.Fatalf("got %v, want an ambiguous *SendTimeoutError", err)
	}
	if *n != 1 || s.refusals.pending() != 0 {
		t.Fatalf("flush calls %d, pending %d", *n, s.refusals.pending())
	}
}

// A send that ended by ack, or by a definite refusal, leaves nothing in flight: no barrier follows.
func TestSettledSendsDoNotMarkDirty(t *testing.T) {
	f := newFixture(t, "ok", "denied")
	s := f.restrictedSender(t, "ok")
	n := countingFlush(s, nil, nil)
	if _, err := timedSend(s, "ok"); err != nil {
		t.Fatal(err)
	}
	if _, err := timedSend(s, "ok"); err != nil || *n != 0 {
		t.Fatalf("after an ack: %v, flush calls %d", err, *n)
	}
	took, err := timedSend(s, "denied")
	wantRefused(t, err, "denied", took)
	took, err = timedSend(s, "denied")
	wantRefused(t, err, "denied", took)
	if *n != 0 {
		t.Fatalf("flush called %d times after settled sends", *n)
	}
}
