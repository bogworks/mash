package endpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
	"github.com/bogworks/mash/internal/store"
)

// Tests for naming plan 1.4b: a three-token endpoint whose own directory record carries an alias reads its own stream
// and the legacy inbox stream through the legacy stream's existing durable consumer.
//
// The endpoints here run with the credentials a provisioned user really has: admin.NewFormPermissions, and for an
// endpoint with an alias also admin.WindowBridge for the legacy identity (cc, aliasOwner, aliasName), merged by union
// of the publish and subscribe allow lists (aliasPerms). A permission the bridge lacks shows up as a failing test.

// aliasOpts says what addNewFormAlias breaks.
type aliasOpts struct {
	rawRecord []byte // when set, the directory record's value as is (a record the endpoint cannot parse)
}

// addNewFormAlias provisions a three-token endpoint like addNewForm, with an alias to the legacy endpoint
// aliasOwner/aliasName in its directory record and the provisioned user (NewFormPermissions, plus WindowBridge when
// aliasName is set).
func (f *fixture) addNewFormAlias(t *testing.T, agent, owner, name, aliasOwner, aliasName string, o aliasOpts) sap.EndpointIdentity {
	t.Helper()
	id, err := sap.New(agent, owner, name)
	if err != nil {
		t.Fatal(err)
	}
	creds, user := f.e.MintRaw(t, id.Flat(), aliasPerms(t, id, aliasOwner, aliasName))
	ctx := context.Background()
	js, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	s, err := js.CreateStream(ctx, store.InboxConfigIdentity(id, store.DefaultLimits()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateOrUpdateConsumer(ctx, store.ConsumerConfig()); err != nil {
		t.Fatal(err)
	}
	kv, err := js.KeyValue(ctx, store.DirectoryBucket)
	if err != nil {
		t.Fatal(err)
	}
	rec := store.Record{Name: id.Name, Owner: id.Owner, Agent: sap.AgentName, AgentToken: id.Agent,
		Account: f.e.Account, User: user, MailSubject: id.Subjects().Mail, Created: time.Now().UTC().Format(time.RFC3339)}
	if aliasName != "" {
		rec.Alias = &store.Alias{Owner: aliasOwner, Name: aliasName}
	}
	val, _ := json.Marshal(rec)
	if o.rawRecord != nil {
		val = o.rawRecord
	}
	if _, err := kv.Create(ctx, id.Flat(), val); err != nil {
		t.Fatal(err)
	}
	f.creds[id.Flat()], f.users[id.Flat()] = creds, user
	return id
}

// aliasPerms is the permission set of a provisioned user for id: NewFormPermissions, united with WindowBridge for the
// legacy alias aliasOwner/aliasName when aliasName is set.
func aliasPerms(t *testing.T, id sap.EndpointIdentity, aliasOwner, aliasName string) jwt.Permissions {
	t.Helper()
	perms, err := admin.NewFormPermissions(id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if aliasName == "" {
		return perms
	}
	alias, err := sap.Legacy(aliasOwner, aliasName)
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := admin.WindowBridge(id, alias)
	if err != nil {
		t.Fatal(err)
	}
	perms.Pub.Allow.Add(bridge.Pub.Allow...)
	perms.Sub.Allow.Add(bridge.Sub.Allow...)
	return perms
}

// setRecord overwrites the directory record of id with raw (admin credentials).
func (f *fixture) setRecord(t *testing.T, id sap.EndpointIdentity, raw []byte) {
	t.Helper()
	js, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	kv, err := js.KeyValue(context.Background(), store.DirectoryBucket)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Put(context.Background(), id.Flat(), raw); err != nil {
		t.Fatal(err)
	}
}

// runNF is serveNF with a stop the test controls (a restart), without the service.
func (f *fixture) runNF(t *testing.T, id sap.EndpointIdentity) (*Endpoint, *events, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, f.cfgNF(id))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	ev := &events{}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, ev.deliver); close(done) }()
	var once bool
	stop := func() {
		if once {
			return
		}
		once = true
		cancel()
		<-done
		ep.Close()
	}
	t.Cleanup(stop)
	return ep, ev, stop
}

func joined(ev *events) string { return strings.Join(ev.all(), "\n") }

// waitText waits until the events' text contains every want.
func waitText(t *testing.T, ev *events, wants ...string) string {
	t.Helper()
	var got string
	until(t, fmt.Sprintf("events containing %q", wants), func() bool {
		got = joined(ev)
		for _, w := range wants {
			if !strings.Contains(got, w) {
				return false
			}
		}
		return true
	})
	return got
}

func (f *fixture) sendTo(t *testing.T, from *Endpoint, to string, bodies ...string) {
	t.Helper()
	for _, b := range bodies {
		if _, err := from.Send(context.Background(), to, b, 0); err != nil {
			t.Fatal(err)
		}
	}
}

func legacyConsumerInfo(t *testing.T, f *fixture, stream string) *jetstream.ConsumerInfo {
	t.Helper()
	js, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	c, err := js.Consumer(context.Background(), stream, store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	i, err := c.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return i
}

// (a) Legacy mail queued before the endpoint starts is delivered exactly once, shown as #L<n>, and a restart does not
// show it again once acknowledged. The legacy stream is never altered.
func TestLegacyQueuedMailIsShownOnceAsL(t *testing.T) {
	f := newFixture(t, "old", "snd")
	snd := f.client(t, "snd")
	f.sendTo(t, snd, "old", "legacy one", "legacy two")
	id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	peer := f.addNewForm(t, "ag", "team", "peer", newFormOpts{})
	ep, ev, stop := f.runNF(t, id)
	if ep.alias == nil || ep.legacyKey != "old" || !ep.hasLegacyStream() {
		t.Fatalf("alias %v key %q", ep.alias, ep.legacyKey)
	}
	got := waitText(t, ev, "--- #L1 from snd [verified]", "--- #L2 from snd [verified]", "> legacy one", "> legacy two")
	if n := strings.Count(got, "legacy one"); n != 1 {
		t.Fatalf("legacy one shown %d times:\n%s", n, got)
	}
	if strings.Contains(got, "--- #1 ") || strings.Contains(got, "--- #2 ") {
		t.Fatalf("a legacy message was shown with a bare number:\n%s", got)
	}
	ackFloor(t, f, "inbox-old", 2)
	if i := legacyConsumerInfo(t, f, "inbox-old"); i.NumPending != 0 || i.NumAckPending != 0 {
		t.Fatalf("legacy consumer %+v", i)
	}
	// Nothing was copied or republished: the legacy stream holds its two messages, the new stream none.
	js, _ := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	for stream, want := range map[string]uint64{"inbox-old": 2, "inbox-ag_team_new": 0} {
		s, err := js.Stream(context.Background(), stream)
		if err != nil {
			t.Fatal(err)
		}
		if st := s.CachedInfo().State; st.Msgs != want {
			t.Fatalf("%s holds %d, want %d", stream, st.Msgs, want)
		}
	}
	stop()

	// A restart shows only what is new.
	pe := f.clientNF(t, peer)
	_, ev2, _ := f.runNF(t, id)
	f.sendTo(t, pe, id.Flat(), "own after restart")
	got = waitText(t, ev2, "--- #1 from ag_team_peer [verified]", "> own after restart")
	time.Sleep(400 * time.Millisecond)
	got = joined(ev2)
	if strings.Contains(got, "legacy") || strings.Contains(got, "#L") {
		t.Fatalf("a restart showed acknowledged legacy mail again:\n%s", got)
	}
}

// (b) Both streams interleaved: both are delivered, the numbers are #n and #Ln, distinct and correct, and read and inbox
// follow them.
func TestTwoStreamsInterleaved(t *testing.T) {
	f := newFixture(t, "old", "snd")
	snd := f.client(t, "snd")
	id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	peer := f.addNewForm(t, "ag", "team", "peer", newFormOpts{})
	pe := f.clientNF(t, peer)
	ep, ev, _ := f.runNF(t, id)
	for i := 1; i <= 3; i++ {
		f.sendTo(t, snd, "old", fmt.Sprintf("legacy-%d", i))
		time.Sleep(120 * time.Millisecond)
		f.sendTo(t, pe, id.Flat(), fmt.Sprintf("own-%d", i))
		time.Sleep(120 * time.Millisecond)
	}
	var wants []string
	for i := 1; i <= 3; i++ {
		wants = append(wants, fmt.Sprintf("--- #L%d from snd [verified]", i), fmt.Sprintf("--- #%d from ag_team_peer [verified]", i),
			fmt.Sprintf("> legacy-%d", i), fmt.Sprintf("> own-%d", i))
	}
	got := waitText(t, ev, wants...)
	for i := 1; i <= 3; i++ {
		if n := strings.Count(got, fmt.Sprintf("> legacy-%d", i)); n != 1 {
			t.Fatalf("legacy-%d shown %d times", i, n)
		}
	}
	ctx := context.Background()
	ms, err := ep.Inbox(ctx, 20)
	if err != nil || len(ms) != 6 {
		t.Fatalf("inbox %d %v", len(ms), err)
	}
	num := map[string]string{}
	for i, m := range ms {
		num[string(m.Body)] = m.Num()
		if i > 0 && ms[i].At.Before(ms[i-1].At) {
			t.Fatalf("inbox is not ordered by time: %v", ms)
		}
	}
	for i := 1; i <= 3; i++ {
		if num[fmt.Sprintf("legacy-%d", i)] != fmt.Sprintf("L%d", i) || num[fmt.Sprintf("own-%d", i)] != fmt.Sprintf("%d", i) {
			t.Fatalf("numbers %v", num)
		}
	}
	// Each shows its qualified number in a listing line.
	if line := render.Line(ms[0]); !strings.HasPrefix(line, "#L1\t") {
		t.Fatalf("first line %q", line)
	}
	// Read by either form.
	for ref, want := range map[string]string{"1": "own-1", "#2": "own-2", "L3": "legacy-3", "#L2": "legacy-2"} {
		r, err := ep.ResolveRef(ref)
		if err != nil {
			t.Fatal(err)
		}
		m, err := ep.ReadRef(ctx, r)
		if err != nil || string(m.Body) != want {
			t.Fatalf("read %s: %q %v, want %s", ref, m.Body, err, want)
		}
	}
	if m, err := ep.Read(ctx, 3); err != nil || string(m.Body) != "own-3" || m.Stream != "" {
		t.Fatalf("Read(3): %+v %v", m, err)
	}
	if _, err := ep.ReadRef(ctx, MsgRef{Stream: LegacyStream, Seq: 99}); err == nil || !strings.Contains(err.Error(), "#L99") {
		t.Fatalf("missing legacy message: %v", err)
	}
	// Paging forward is per stream.
	after, err := ep.InboxAfterRef(ctx, MsgRef{Stream: LegacyStream, Seq: 1}, 10)
	if err != nil || len(after) != 2 || after[0].Num() != "L2" || after[1].Num() != "L3" {
		t.Fatalf("after L1: %+v %v", after, err)
	}
	after, err = ep.InboxAfter(ctx, 2, 10)
	if err != nil || len(after) != 1 || after[0].Num() != "3" {
		t.Fatalf("after 2: %+v %v", after, err)
	}
	ackFloor(t, f, "inbox-old", 3)
	ackFloor(t, f, "inbox-ag_team_new", 3)
	if ms, _ := ep.Inbox(ctx, 4); len(ms) != 4 {
		t.Fatalf("limit 4 gave %d", len(ms))
	}
}

// (c) Mail acknowledged by an earlier legacy-form session is not shown again, including after acknowledgements out of
// order that left a gap. The ack state is read from the legacy durable consumer itself.
func TestLegacyAckedMailIsNotShownAgain(t *testing.T) {
	t.Run("legacy session", func(t *testing.T) {
		f := newFixture(t, "old", "snd")
		snd := f.client(t, "snd")
		f.sendTo(t, snd, "old", "seen by the legacy session")
		lctx, lcancel := context.WithCancel(context.Background())
		lep, err := Open(lctx, f.cfg("old")) // the endpoint as it was before the rename: legacy form, bare numbers
		if err != nil {
			t.Fatal(err)
		}
		lev := &events{}
		ldone := make(chan struct{})
		go func() { _ = lep.Run(lctx, lev.deliver); close(ldone) }()
		lev.waitFor(t, 1, 5*time.Second)
		ackFloor(t, f, "inbox-old", 1)
		// The legacy session ends. Mail then arrives for the old name.
		lcancel()
		<-ldone
		lep.Close()
		f.sendTo(t, snd, "old", "arrived between sessions")
		id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
		_, ev, _ := f.runNF(t, id)
		got := waitText(t, ev, "--- #L2 from snd [verified]", "> arrived between sessions")
		time.Sleep(300 * time.Millisecond)
		got = joined(ev)
		if strings.Contains(got, "seen by the legacy session") || strings.Contains(got, "#L1") {
			t.Fatalf("acknowledged mail was shown again:\n%s", got)
		}
		ackFloor(t, f, "inbox-old", 2)
	})
	for _, tc := range []struct {
		name   string
		acks   []int // message numbers acknowledged, in this order; the rest are released (nak) for redelivery
		unseen []string
		shown  []string
	}{
		{"ack 3, 1 leaving 2", []int{3, 1}, []string{"m1", "m3"}, []string{"m2"}},
		{"ack 3, 1, 2", []int{3, 1, 2}, []string{"m1", "m2", "m3"}, nil},
		{"ack 2 only", []int{2}, []string{"m2"}, []string{"m1", "m3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "old", "snd")
			snd := f.client(t, "snd")
			f.sendTo(t, snd, "old", "m1", "m2", "m3")
			js, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
			if err != nil {
				t.Fatal(err)
			}
			cons, err := js.Consumer(context.Background(), "inbox-old", store.Consumer)
			if err != nil {
				t.Fatal(err)
			}
			batch, err := cons.Fetch(3, jetstream.FetchMaxWait(5*time.Second))
			if err != nil {
				t.Fatal(err)
			}
			var msgs []jetstream.Msg
			for m := range batch.Messages() {
				msgs = append(msgs, m)
			}
			if len(msgs) != 3 {
				t.Fatalf("fetched %d", len(msgs))
			}
			acked := map[int]bool{}
			for _, n := range tc.acks {
				if err := msgs[n-1].DoubleAck(context.Background()); err != nil {
					t.Fatal(err)
				}
				acked[n] = true
			}
			for i, m := range msgs {
				if !acked[i+1] {
					if err := m.Nak(); err != nil {
						t.Fatal(err)
					}
				}
			}
			// The consumer's own state: the floor stops at the gap, and the acked message above it is recorded.
			wantFloor := uint64(0)
			for acked[int(wantFloor)+1] {
				wantFloor++
			}
			until(t, "consumer state", func() bool {
				i := legacyConsumerInfo(t, f, "inbox-old")
				return i.AckFloor.Stream == wantFloor
			})
			id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
			_, ev, _ := f.runNF(t, id)
			if len(tc.shown) > 0 {
				var wants []string
				for _, s := range tc.shown {
					wants = append(wants, "> "+s)
				}
				waitText(t, ev, wants...)
			}
			until(t, "everything acknowledged", func() bool {
				i := legacyConsumerInfo(t, f, "inbox-old")
				return i.AckFloor.Stream == 3 && i.NumAckPending == 0 && i.NumPending == 0
			})
			time.Sleep(300 * time.Millisecond)
			got := joined(ev)
			for _, s := range tc.unseen {
				if strings.Contains(got, "> "+s) {
					t.Fatalf("%s was acknowledged before the switch and was shown again:\n%s", s, got)
				}
			}
			for _, s := range tc.shown {
				if n := strings.Count(got, "> "+s); n != 1 {
					t.Fatalf("%s shown %d times:\n%s", s, n, got)
				}
			}
		})
	}
}

// (d) Open fails closed when the endpoint's own record is absent, unreadable or carries a malformed alias, and an alias
// that changes in the directory after open is not adopted.
func TestOpenFailsClosedOnTheOwnRecord(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		f := newFixture(t, "old")
		id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
		afterLeaseHook = func() {
			js, _ := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
			kv, _ := js.KeyValue(context.Background(), store.DirectoryBucket)
			_ = kv.Purge(context.Background(), id.Flat())
		}
		t.Cleanup(func() { afterLeaseHook = nil })
		if _, err := Open(context.Background(), f.cfgNF(id)); !errors.Is(err, ErrDeprovisioned) {
			t.Fatalf("Open: %v", err)
		}
	})
	t.Run("never provisioned", func(t *testing.T) {
		f := newFixture(t, "old")
		id := f.addNewForm(t, "ag", "team", "new", newFormOpts{noRecord: true})
		if ep, err := Open(context.Background(), f.cfgNF(id)); err == nil {
			ep.Close()
			t.Fatal("Open succeeded without a record")
		}
	})
	t.Run("unreadable", func(t *testing.T) {
		f := newFixture(t, "old")
		id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
		afterLeaseHook = func() { f.setRecord(t, id, []byte("this is not json")) }
		t.Cleanup(func() { afterLeaseHook = nil })
		ep, err := Open(context.Background(), f.cfgNF(id))
		if err == nil {
			ep.Close()
			t.Fatal("Open succeeded with an unreadable record")
		}
	})
	t.Run("malformed alias", func(t *testing.T) {
		f := newFixture(t, "old")
		id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
		// The record as the endpoint first read it was fine; at the read under the lease its alias is not a legacy identity.
		bad := f.recordJSON(t, id, &store.Alias{Owner: "Not A Token!", Name: "old"})
		afterLeaseHook = func() { f.setRecord(t, id, bad) }
		t.Cleanup(func() { afterLeaseHook = nil })
		ep, err := Open(context.Background(), f.cfgNF(id))
		if err == nil {
			ep.Close()
			t.Fatal("Open succeeded with a malformed alias")
		}
		if !strings.Contains(err.Error(), "alias") {
			t.Fatalf("error does not name the alias: %v", err)
		}
	})
	t.Run("malformed alias from the start", func(t *testing.T) {
		f := newFixture(t, "old")
		id := f.addNewFormAlias(t, "ag", "team", "new", "", "", aliasOpts{})
		f.setRecord(t, id, f.recordJSON(t, id, &store.Alias{Owner: id.Owner, Name: "old"})) // alias owner equals the record's owner
		if ep, err := Open(context.Background(), f.cfgNF(id)); err == nil {
			ep.Close()
			t.Fatal("Open succeeded with a malformed alias")
		}
	})
	t.Run("changed alias is not adopted", func(t *testing.T) {
		f := newFixture(t, "old", "other", "snd")
		snd := f.client(t, "snd")
		id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
		ep, ev, _ := f.runNF(t, id)
		f.setRecord(t, id, f.recordJSON(t, id, &store.Alias{Owner: f.e.Owner, Name: "other"}))
		if err := ep.Refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
		f.sendTo(t, snd, "other", "for the other alias")
		f.sendTo(t, snd, "old", "for the first alias")
		waitText(t, ev, "--- #L1 from snd [verified]", "> for the first alias")
		time.Sleep(400 * time.Millisecond)
		if got := joined(ev); strings.Contains(got, "for the other alias") {
			t.Fatalf("a changed alias was adopted:\n%s", got)
		}
		if ep.alias == nil || ep.legacyKey != "old" {
			t.Fatalf("alias %v key %q", ep.alias, ep.legacyKey)
		}
	})
}

// recordJSON is id's record as addNewFormAlias wrote it, with alias.
func (f *fixture) recordJSON(t *testing.T, id sap.EndpointIdentity, alias *store.Alias) []byte {
	t.Helper()
	rec := store.Record{Name: id.Name, Owner: id.Owner, Agent: sap.AgentName, AgentToken: id.Agent, Account: f.e.Account,
		User: f.users[id.Flat()], MailSubject: id.Subjects().Mail, Created: time.Now().UTC().Format(time.RFC3339), Alias: alias}
	b, _ := json.Marshal(rec)
	return b
}

// A three-token endpoint with no alias reads its own stream only, and a legacy endpoint is as it was.
func TestNoAliasNoLegacyStream(t *testing.T) {
	f := newFixture(t, "old", "snd")
	id := f.addNewFormAlias(t, "ag", "team", "new", "", "", aliasOpts{})
	ep, _, _ := f.runNF(t, id)
	if ep.alias != nil || ep.hasLegacyStream() || len(ep.sources()) != 1 {
		t.Fatalf("alias %v", ep.alias)
	}
	if _, err := ep.ResolveRef("L1"); !errors.Is(err, ErrNoLegacyStream) {
		t.Fatalf("%v", err)
	}
	if _, err := ep.ReadRef(context.Background(), MsgRef{Stream: LegacyStream, Seq: 1}); !errors.Is(err, ErrNoLegacyStream) {
		t.Fatalf("%v", err)
	}
	lg, _ := f.run(t, "old")
	if lg.alias != nil || lg.hasLegacyStream() {
		t.Fatal("a legacy endpoint has no alias")
	}
}

// (e) An L reference is accepted only with an alias.
func TestResolveRefNeedsAnAlias(t *testing.T) {
	old, _ := sap.Legacy("o", "old")
	with, without := &Endpoint{alias: &old, legacyKey: "old"}, &Endpoint{}
	for _, s := range []string{"L5", "#L5"} {
		if r, err := with.ResolveRef(s); err != nil || r != (MsgRef{Stream: LegacyStream, Seq: 5}) {
			t.Errorf("with alias %q: %+v %v", s, r, err)
		}
		if _, err := without.ResolveRef(s); !errors.Is(err, ErrNoLegacyStream) {
			t.Errorf("without alias %q: %v", s, err)
		}
	}
	for _, s := range []string{"5", "#5"} {
		for _, e := range []*Endpoint{with, without} {
			if r, err := e.ResolveRef(s); err != nil || r != (MsgRef{Seq: 5}) {
				t.Errorf("%q: %+v %v", s, r, err)
			}
		}
	}
}

// (7) Legacy mail is spooled with its stream (legacy@<n>.json), a hook's drained mark by that ref acks it, and the own
// stream's mail of the same number is untouched.
func TestLegacyMailSpoolsAndDrainsByRef(t *testing.T) {
	f := newFixture(t, "old", "snd")
	snd := f.client(t, "snd")
	f.sendTo(t, snd, "old", "legacy body")
	id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	peer := f.addNewForm(t, "ag", "team", "peer", newFormOpts{})
	pe := f.clientNF(t, peer)
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, f.cfgNF(id))
	if err != nil {
		t.Fatal(err)
	}
	ep.NoChannel()
	ep.SpoolFromStart()
	sp := spool.Spool{Dir: t.TempDir() + "/spool"}
	if err := ep.SetSpool(sp, ""); err != nil {
		t.Fatal(err)
	}
	ev := &events{}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, ev.deliver); close(done) }()
	t.Cleanup(func() { cancel(); <-done; ep.Close() })
	f.sendTo(t, pe, id.Flat(), "own body")
	until(t, "both spooled", func() bool { return len(waiting(t, sp)) == 2 })
	byBody := map[string]spool.Entry{}
	for _, e := range waiting(t, sp) {
		byBody[string(e.Body)] = e
	}
	if e := byBody["legacy body"]; e.Stream != LegacyStream || e.Seq != 1 {
		t.Fatalf("legacy entry %+v", e)
	}
	if e := byBody["own body"]; e.Stream != "" || e.Seq != 1 {
		t.Fatalf("own entry %+v", e)
	}
	if got := FormatMsgRef(byBody["legacy body"].Ref()); got != "L1" {
		t.Fatalf("ref text %q", got)
	}
	// Only the legacy message is drained: only it is acknowledged.
	if err := sp.Drained(byBody["legacy body"].Ref()); err != nil {
		t.Fatal(err)
	}
	until(t, "legacy message acked", func() bool {
		i := legacyConsumerInfo(t, f, "inbox-old")
		return i.AckFloor.Stream == 1 && i.NumAckPending == 0
	})
	if i := legacyConsumerInfo(t, f, "inbox-ag_team_new"); i.AckFloor.Stream != 0 || i.NumAckPending != 1 {
		t.Fatalf("the own stream's message was acked by the legacy drain: %+v", i)
	}
	if left := waiting(t, sp); len(left) != 1 || string(left[0].Body) != "own body" {
		t.Fatalf("waiting after the drain %+v", left)
	}
}

// (f) A legacy-only endpoint (no agent_token, no alias) shows exactly the text it always did: the full message, the line,
// the digest, the redelivered note, the channel event's seqs and the wake summary's seqs.
func TestGoldenLegacyOnlyText(t *testing.T) {
	ms := []render.Mail{gMail(3, "one"), gMail(12, "two")}
	if got, want := render.One(ms[1]), "--- #12 from b [verified] at 1970-01-01T00:16:52Z\nkind: task\n> two"; got != want {
		t.Errorf("one:\n%q\nwant\n%q", got, want)
	}
	if got, want := render.Line(ms[0]), "#3\tfrom b [verified]\t1970-01-01T00:16:43Z\t3 B\tone\tkind task"; got != want {
		t.Errorf("line:\n%q\nwant\n%q", got, want)
	}
	wantBatch := "2 messages from b (task)\n" +
		"\n--- #3 from b [verified] at 1970-01-01T00:16:43Z\nkind: task\n> one\n" +
		"\n--- #12 from b [verified] at 1970-01-01T00:16:52Z\nkind: task\n> two\n"
	if got := render.Batch("a", "o", ms, 10, 10000); got != wantBatch {
		t.Errorf("batch:\n%q\nwant\n%q", got, wantBatch)
	}
	wantDigest := "2 messages, digest, from b (task)\nShown as a digest (6 B). Read the ones you need with the read tool (by #seq).\n\n" +
		"#3\tfrom b [verified]\t3 B\tone\tkind task\n#12\tfrom b [verified]\t3 B\ttwo\tkind task\n"
	if got := render.Batch("a", "o", ms, 1, 10000); got != wantDigest {
		t.Errorf("digest:\n%q\nwant\n%q", got, wantDigest)
	}
	re := gMail(4, "x")
	re.Redelivered = true
	wantRe := "--- #4 from b [verified] at 1970-01-01T00:16:44Z\nkind: task\nnote: (#4 re-delivered after an interrupted hook: it may already have been shown; do not act on it twice)\n> x"
	if got := render.One(re); got != wantRe {
		t.Errorf("redelivered:\n%q\nwant\n%q", got, wantRe)
	}
	if got, want := render.Line(re), "#4\tfrom b [verified]\t1970-01-01T00:16:44Z\t1 B\tx\tkind task\t(#4 re-delivered after an interrupted hook: it may already have been shown; do not act on it twice)"; got != want {
		t.Errorf("redelivered line:\n%q\nwant\n%q", got, want)
	}

	// The channel event's metadata of a batch shown on the channel.
	e, _ := gEndpoint(t)
	e.cfg.BatchMsgs, e.cfg.BatchBytes = 10, 10000
	var text string
	var meta map[string]string
	deliver := func(tx string, m map[string]string) error { text, meta = tx, m; return nil }
	a, b := &dblAck{}, &dblAck{}
	e.deliverBatch(deliver, []pending{{mail: gMail(3, "one"), msg: a}, {mail: gMail(12, "two"), msg: b}}, newSeenSet(0))
	if text != wantBatch || meta["seqs"] != "3,12" || meta["count"] != "2" || meta["to"] != "a" || len(meta) != 3 {
		t.Errorf("channel event %q %v", text, meta)
	}
	if a.acks != 1 || b.acks != 1 {
		t.Errorf("acks %d %d", a.acks, b.acks)
	}

	// The wake summary of held urgent mail.
	text, meta = "", nil
	h := gHeld()
	for _, seq := range []uint64{3, 12} {
		m := gMail(seq, "x")
		m.Urgent = true
		h.msgs[spool.Ref{Seq: seq}] = pending{mail: m, msg: &dblAck{}}
		h.at[spool.Ref{Seq: seq}] = time.Now()
	}
	e.wakeIfIdle(deliver, h, HooklessNone)
	if text != "urgent, 2 messages from b (task)" || meta["seqs"] != "3,12" || meta["wake"] != "true" || meta["count"] != "2" {
		t.Errorf("wake %q %v", text, meta)
	}
}

// With an alias the same paths print the qualified number: the wake summary, the channel event's seqs and the logs.
func TestLegacyNumbersInWakeEventAndLogs(t *testing.T) {
	e, _ := gEndpoint(t)
	e.cfg.BatchMsgs, e.cfg.BatchBytes = 10, 10000
	var text string
	var meta map[string]string
	deliver := func(tx string, m map[string]string) error { text, meta = tx, m; return nil }
	e.deliverBatch(deliver, []pending{{mail: gMail(5, "own"), msg: &dblAck{}}, {mail: legacyMail(5), msg: &dblAck{}}}, newSeenSet(0))
	if meta["seqs"] != "5,L5" || !strings.Contains(text, "--- #5 from b") || !strings.Contains(text, "--- #L5 from b") {
		t.Errorf("event %q %v", text, meta)
	}
	h := gHeld()
	for _, m := range []render.Mail{gMail(5, "x"), legacyMail(5)} {
		m.Urgent = true
		h.msgs[refOf(m)] = pending{mail: m, msg: &dblAck{}}
		h.at[refOf(m)] = time.Now()
	}
	e.wakeIfIdle(deliver, h, HooklessNone)
	if meta["seqs"] != "5,L5" || meta["wake"] != "true" {
		t.Errorf("wake %v", meta)
	}
	if got := render.One(func() render.Mail { m := legacyMail(5); m.Redelivered = true; return m }()); !strings.Contains(got, "note: (#L5 re-delivered") {
		t.Errorf("redelivered %q", got)
	}
	if got := render.Batch("a", "o", func() []render.Mail {
		var out []render.Mail
		for i := 0; i < 3; i++ {
			out = append(out, legacyMail(uint64(i+1)))
		}
		return out
	}(), 1, 100); !strings.Contains(got, "#L2\tfrom b [verified]") {
		t.Errorf("digest %q", got)
	}
}

// (g) The seen set prunes each stream by its own ack floor; the (user, nonce) de-duplication is global.
func TestSeenSetPrunesPerStream(t *testing.T) {
	s := newSeenSet(time.Minute)
	s.pruneAt, s.pruneEvery = 1, 0
	floors := map[string]uint64{"": 0, LegacyStream: 0}
	known := map[string]bool{"": true, LegacyStream: true}
	s.ackFloors = map[string]func() (uint64, bool){}
	for stream := range floors {
		s.ackFloors[stream] = func() (uint64, bool) { return floors[stream], known[stream] }
	}
	s.mark(pending{key: "u\x00a", mail: gMail(3, "")})
	s.mark(pending{key: "u\x00b", mail: legacyMail(3)})
	s.mark(pending{key: "u\x00c", mail: legacyMail(1)})
	floors[""], floors[LegacyStream] = 10, 2 // the consumers acknowledged more; the next mark reads the floors
	s.mark(pending{key: "u\x00z", mail: gMail(11, "")})
	has := func(r spool.Ref) bool { _, ok := s.seqs[r]; return ok }
	if has(spool.Ref{Seq: 3}) {
		t.Error("the own #3 is under its floor 10 and should be pruned")
	}
	if has(spool.Ref{Stream: LegacyStream, Seq: 1}) {
		t.Error("the legacy #1 is under its floor 2 and should be pruned")
	}
	if !has(spool.Ref{Stream: LegacyStream, Seq: 3}) {
		t.Error("the legacy #3 is above its floor 2 and was pruned by the other stream's floor")
	}
	// A floor that cannot be read prunes nothing in its stream, and does not stop the other stream.
	known[LegacyStream] = false
	floors[LegacyStream] = 100
	floors[""] = 20
	s.mark(pending{key: "u\x00d", mail: gMail(15, "")})
	s.mark(pending{key: "u\x00e", mail: legacyMail(15)})
	if has(spool.Ref{Seq: 15}) || !has(spool.Ref{Stream: LegacyStream, Seq: 15}) || !has(spool.Ref{Stream: LegacyStream, Seq: 3}) {
		t.Errorf("seqs %v", s.seqs)
	}
	// One signed message copied to the other stream is the same (user, nonce): a replay, whichever stream it arrives on.
	d := newSeenSet(time.Minute)
	d.mark(pending{key: "u\x00n", mail: gMail(1, "")})
	if !d.dup(pending{key: "u\x00n", mail: legacyMail(7)}) {
		t.Error("the same (user, nonce) on the other stream inside the window is a replay")
	}
	if d.dup(pending{key: "u\x00other", mail: legacyMail(7)}) {
		t.Error("another nonce is not a replay")
	}
}

// (g) The waking count is known only when every stream's backlog is accounted for; the first-sequence eviction is per
// stream.
func TestWakingReadinessPerStream(t *testing.T) {
	w := newWakingTracker()
	w.startStreams(map[string]streamStart{
		"":           {Backlog: 1, Last: 5},
		LegacyStream: {Backlog: 2, Last: 10},
	}, true, nil)
	if _, ok := w.snapshot(); ok {
		t.Fatal("known before any backlog arrived")
	}
	w.received(spool.Ref{Seq: 5})
	if _, ok := w.snapshot(); ok {
		t.Fatal("the own stream alone made the count known")
	}
	w.received(spool.Ref{Stream: LegacyStream, Seq: 3})
	w.received(spool.Ref{Stream: LegacyStream, Seq: 3}) // a redelivery stands in for nothing
	if _, ok := w.snapshot(); ok {
		t.Fatal("a legacy redelivery stood in for a message still outstanding")
	}
	w.received(spool.Ref{Stream: LegacyStream, Seq: 11}) // after start
	if _, ok := w.snapshot(); ok {
		t.Fatal("a legacy message after start counted")
	}
	w.received(spool.Ref{Stream: LegacyStream, Seq: 4})
	if _, ok := w.snapshot(); !ok {
		t.Fatal("every stream's backlog arrived, the count should be known")
	}

	// The same through the server's rule at a lease refresh, per stream.
	var own, old probeState
	probe := func(p *probeState) wakingProbe {
		return func(context.Context) (uint64, int, uint64, error) {
			if p.fail {
				return 0, 0, 0, errors.New("no")
			}
			return p.pending, p.ackPending, p.first, nil
		}
	}
	w = newWakingTracker()
	w.startStreams(map[string]streamStart{"": {Backlog: 3, Last: 9}, LegacyStream: {Backlog: 3, Last: 9}}, true,
		map[string]wakingProbe{"": probe(&own), LegacyStream: probe(&old)})
	own.first, old.first = 1, 1
	own.ackPending, old.ackPending = 0, 1
	w.reconcile(context.Background())
	if _, ok := w.snapshot(); ok {
		t.Fatal("the legacy stream still has an unacknowledged message the handler does not hold")
	}
	w.add(spool.Ref{Stream: LegacyStream, Seq: 2}, true)
	old.fail = true
	w.reconcile(context.Background())
	if _, ok := w.snapshot(); ok {
		t.Fatal("a failed probe made the stream ready")
	}
	old.fail = false
	w.reconcile(context.Background())
	if n, ok := w.snapshot(); !ok || n != 1 {
		t.Fatalf("count %d ok=%v, want 1 known", n, ok)
	}

	// A stream's first sequence evicts that stream's entries only.
	w = newWakingTracker()
	w.startStreams(map[string]streamStart{"": {}, LegacyStream: {}}, true, map[string]wakingProbe{"": probe(&own), LegacyStream: probe(&old)})
	w.add(spool.Ref{Seq: 2}, true)
	w.add(spool.Ref{Stream: LegacyStream, Seq: 2}, true)
	own.pending, own.ackPending, own.first = 0, 1, 5
	old.pending, old.ackPending, old.first = 0, 1, 1
	w.reconcile(context.Background())
	if n, _ := w.snapshot(); n != 1 {
		t.Fatalf("count %d, want only the legacy #L2 left", n)
	}
	if _, ok := w.unread[spool.Ref{Stream: LegacyStream, Seq: 2}]; !ok {
		t.Fatal("the own stream's first sequence evicted a legacy entry")
	}
	old.first = 3
	w.reconcile(context.Background())
	if n, _ := w.snapshot(); n != 0 {
		t.Fatalf("count %d, want 0 once the legacy first passed #L2", n)
	}
}

// probeState is what a fake stream probe answers.
type probeState struct {
	pending    uint64
	ackPending int
	first      uint64
	fail       bool
}
