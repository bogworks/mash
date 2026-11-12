package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// capInbox sets the byte cap of name's inbox stream, below what a provisioned one allows, so a few messages fill it.
func (f *auditFixture) capInbox(t *testing.T, name string, maxBytes int64) {
	t.Helper()
	s, err := f.js.Stream(context.Background(), sap.InboxStream(name))
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg := info.Config
	cfg.MaxBytes = maxBytes
	if _, err := f.js.UpdateStream(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
}

func (f *auditFixture) publishBig(t *testing.T, subject string, n int) {
	t.Helper()
	body := []byte(strings.Repeat("x", 500))
	for i := 0; i < n; i++ {
		if _, err := f.js.Publish(context.Background(), subject, body); err != nil {
			t.Fatal(err)
		}
	}
}

func inboxProblemsOf(r retiredOut) []string {
	var out []string
	for _, p := range r.Problems {
		if p.What == "inbox" {
			out = append(out, p.Item+": "+p.Error)
		}
	}
	return out
}

func (f *auditFixture) inboxOf(t *testing.T, key string) auditInbox {
	t.Helper()
	out, _, _ := f.run(t, "--json")
	var rep auditReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	for _, r := range rep.Records {
		if r.Key == key {
			return r.Inbox
		}
	}
	t.Fatalf("no record %s", key)
	return auditInbox{}
}

func TestAuditReportsInboxMailEvictedUnread(t *testing.T) {
	f := newAuditEnv(t)
	f.legacy(t, "evic")
	f.capInbox(t, "evic", 4096)
	subj := sap.For(f.e.Owner, "evic").Mail

	f.publishBig(t, subj, 3)
	c, err := f.js.Consumer(context.Background(), sap.InboxStream("evic"), store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Fetch(2, jetstream.FetchMaxWait(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for m := range b.Messages() { // two read and acknowledged
		_ = m.DoubleAck(context.Background())
	}
	f.publishBig(t, subj, 20) // fills the inbox past its cap: the oldest, unread mail goes

	in := f.inboxOf(t, "evic")
	if in.Evicted != in.FirstSeq-1 || in.Evicted < 10 {
		t.Fatalf("evicted %d with first seq %d", in.Evicted, in.FirstSeq)
	}
	if want := in.Evicted - 2; in.EvictedUnread != want { // messages 1 and 2 were read
		t.Fatalf("evicted unread %d, want %d", in.EvictedUnread, want)
	}
	r, _, code := f.retiredAudit(t, "--strict")
	p := inboxProblemsOf(r)
	if code != 1 || len(p) == 0 || !strings.Contains(p[0], "unread message(s) were evicted from this inbox") ||
		!strings.Contains(p[0], "docs/operations.md") {
		t.Fatalf("exit %d problems %v", code, p)
	}
	if txt, _, _ := f.run(t); !strings.Contains(txt, "inbox evic: ") || strings.Contains(txt, "not readable inbox") {
		t.Errorf("text form:\n%s", txt)
	}

	// Reading again moves the ack floor past the gap; the loss is still reported.
	f.ackAll(t, "evic")
	if in := f.inboxOf(t, "evic"); in.EvictedUnread < in.Evicted-2 || in.EvictedUnread == 0 {
		t.Fatalf("after the consumer caught up: unread %d of %d", in.EvictedUnread, in.Evicted)
	}
}

func TestAuditNearFullInboxIsANoteNotAProblem(t *testing.T) {
	f := newAuditEnv(t)
	f.legacy(t, "full")
	f.capInbox(t, "full", 4096)
	f.publishBig(t, sap.For(f.e.Owner, "full").Mail, 7) // about 3.8 KiB of 4 KiB, nothing dropped
	r, _, code := f.retiredAudit(t, "--strict")
	if code != 0 || len(inboxProblemsOf(r)) != 0 {
		t.Fatalf("exit %d problems %v: a near-full inbox must not fail --strict", code, r.Problems)
	}
	out, _, _ := f.run(t, "--json")
	var rep auditReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.Problems) != 0 || rep.Summary.Problems != 0 {
		t.Fatalf("problems %v", rep.Problems)
	}
	var found bool
	for _, n := range rep.Notes {
		if strings.Contains(n, "inbox full is ") && strings.Contains(n, "% full") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no near-full note in %v", rep.Notes)
	}
	if txt, _, _ := f.run(t); !strings.Contains(txt, "note: inbox full is ") {
		t.Errorf("text form lacks the note:\n%s", txt)
	}
}

func TestAuditUnreadLossInNearFullInboxIsAProblem(t *testing.T) {
	f := newAuditEnv(t)
	f.legacy(t, "both")
	f.capInbox(t, "both", 4096)
	f.publishBig(t, sap.For(f.e.Owner, "both").Mail, 20) // nothing read, the cap dropped the oldest
	r, _, code := f.retiredAudit(t, "--strict")
	p := inboxProblemsOf(r)
	if code != 1 || len(p) != 1 || !strings.Contains(p[0], "unread message(s) were evicted") || !strings.Contains(p[0], "% full") {
		t.Fatalf("exit %d problems %v", code, p)
	}
}

func TestAuditCleanInboxReportsNothing(t *testing.T) {
	f := newAuditEnv(t)
	f.legacy(t, "calm")
	f.publish(t, sap.For(f.e.Owner, "calm").Mail, 3)
	r, _, code := f.retiredAudit(t, "--strict")
	if code != 0 || len(inboxProblemsOf(r)) != 0 {
		t.Fatalf("exit %d %v", code, r.Problems)
	}
	if in := f.inboxOf(t, "calm"); in.Evicted != 0 || in.EvictedUnread != 0 {
		t.Fatalf("clean inbox: %+v", in)
	}
}

func TestAuditEvictedButReadInboxIsNotAProblem(t *testing.T) {
	f := newAuditEnv(t)
	f.legacy(t, "tidy")
	f.capInbox(t, "tidy", 4096)
	subj := sap.For(f.e.Owner, "tidy").Mail
	for range 4 { // the endpoint keeps up: every message is read before the next ones push it out
		f.publishBig(t, subj, 5)
		f.ackAll(t, "tidy")
	}
	in := f.inboxOf(t, "tidy")
	if in.Evicted == 0 || in.EvictedUnread != 0 {
		t.Fatalf("want turnover with nothing lost unread: %+v", in)
	}
	f.publishBig(t, subj, 1)
	f.ackAll(t, "tidy")
	r, _, code := f.retiredAudit(t, "--strict")
	for _, p := range inboxProblemsOf(r) {
		if strings.Contains(p, "evicted from") {
			t.Fatalf("exit %d: read mail that aged out is not a problem: %s", code, p)
		}
	}
}
