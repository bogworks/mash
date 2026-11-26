package endpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// OnlineWindow is how recently an answer must have been seen to count as online when no presence probe has been
// made yet (3 x 30 s, SAP §8.2). Once a probe has run, online means answering that probe or a later one.
var OnlineWindow = 90 * time.Second

// Presence is a directory record and whether its endpoint is online.
type Presence struct {
	Record store.Record
	Online bool
	// LastSeen is the time of the last answer to a presence probe ($SRV.INFO); it is no longer the last heartbeat
	// (ADR 0066). Zero when it has not answered.
	LastSeen time.Time
}

// DirectoryPoll is how often a running endpoint re-reads the directory.
// Polling needs only stream info and direct gets: endpoints may not create
// consumers on the directory (one could exhaust or delete another's).
var DirectoryPoll = 15 * time.Second

// pollDirectory polls the directory until ctx ends: one small unfiltered stream-info request per poll, and a full
// reload only when the stream moved or DirectoryFullReload is due (ADR 0065). Polls are jittered.
func (e *Endpoint) pollDirectory(ctx context.Context, every time.Duration) {
	p := NewDirPoller(nil, nil)
	t := time.NewTimer(p.FirstWait(every))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := p.Tick(ctx, e.directoryState, e.loadDirectory); err != nil && ctx.Err() == nil {
				e.log.Print(err)
			}
			t.Reset(p.Wait(every))
		}
	}
}

// registration is one prompt endpoint of a $SRV.INFO answer, with the metadata that vouches for it.
type registration struct {
	Subject  string
	Metadata map[string]string
}

// parseAnswer is the prompt registrations a $SRV.INFO answer carries. An answer that is not JSON, or names no prompt
// endpoint, carries none: it is not a registration to verify, and is not counted (TO-12).
func parseAnswer(data []byte) []registration {
	var info struct {
		Metadata  map[string]string `json:"metadata"`
		Endpoints []struct {
			Name    string `json:"name"`
			Subject string `json:"subject"`
		} `json:"endpoints"`
	}
	if json.Unmarshal(data, &info) != nil {
		return nil
	}
	var regs []registration
	for _, ep := range info.Endpoints {
		if ep.Name == "prompt" {
			regs = append(regs, registration{Subject: ep.Subject, Metadata: info.Metadata})
		}
	}
	return regs
}

// dirView is what the directory says about one registration: the endpoint that holds its user_nkey, and the record of
// the address its prompt subject names.
type dirView struct {
	holder string
	rec    store.Record
	hasRec bool
}

// viewOfLocked reads the directory for reg. The caller holds e.mu.
func (e *Endpoint) viewOfLocked(reg registration) *dirView {
	d := &dirView{holder: e.byUser[reg.Metadata["user_nkey"]]}
	if a, _, ok := sap.AddressFromSubject(reg.Subject); ok {
		d.rec, d.hasRec = e.dir[a.Flat()]
	}
	return d
}

// verifyRegistration is mash's one verify step for a discovery answer (REQ-016); both discoveries (presence and the
// recipient discovery of prompt) call it, through verifyStep, so that the mutation test of AT-010 can take the step out
// (replace it with one that accepts) and show AT-015's negative cases fail. verifyStep is atomic because a discovery
// that has returned still reads the rest of its window in the background. It returns the address name the registration vouches for and whether it
// is verified: the id_sig verifies over the user_nkey, account, agent, owner and prompt subject, and, when d is not
// nil, the user_nkey is the directory's key for the address in the prompt subject and the account is the record's.
// A nil d is a caller with no directory: only the id_sig is checked.
//
// metadata.owner is not compared with the record's owner: an SDK host registers under another token, and main has
// always accepted that (TestApplyPresenceIgnoresMetadataOwner). REQ-016 asks for the comparison; it waits for a ruling.
func verifyRegistration(reg registration, d *dirView) (name string, ok bool) {
	a, _, ok := sap.AddressFromSubject(reg.Subject)
	if !ok {
		return "", false
	}
	name = a.Flat()
	md := reg.Metadata
	if !sap.VerifyIDSig(md["user_nkey"], md["account"], md["agent"], md["owner"], reg.Subject, md["id_sig"]) {
		return name, false
	}
	if d == nil {
		return name, true
	}
	if !d.hasRec || d.holder != name {
		return name, false
	}
	if d.rec.Account != "" && md["account"] != d.rec.Account {
		return name, false
	}
	return name, true
}

// verifyFunc is the type of the verify step.
type verifyFunc func(reg registration, d *dirView) (name string, ok bool)

// verifyStep holds the verify step in use: verifyRegistration, except in the mutation test of AT-010.
var verifyStep = func() *atomic.Pointer[verifyFunc] {
	var p atomic.Pointer[verifyFunc]
	f := verifyFunc(verifyRegistration)
	p.Store(&f)
	return &p
}()

// verify runs the verify step in use.
func verify(reg registration, d *dirView) (string, bool) { return (*verifyStep.Load())(reg, d) }

// AnswersNotVerified is how many discovery answers failed verification over this endpoint's life, each counted once
// per discovery (TO-12, REQ-016). It is kept and not printed: no view shows it until W.
func (e *Endpoint) AnswersNotVerified() int { return int(e.notVerified.Load()) }

// applyPresence records the agents a $SRV.INFO answer vouches for as seen now, when the verify step accepts them.
// The address comes from the subject alone; metadata owner is not a filter and never names the address. It returns
// how many registrations it counted, and 1 when the answer carried a registration that failed verification: that is
// counted once, in AnswersNotVerified, and attributed to no endpoint. An invalid answer changes nothing else.
func (e *Endpoint) applyPresence(m *nats.Msg) (counted, rejected int) {
	failed := false
	for _, reg := range parseAnswer(m.Data) {
		e.mu.Lock()
		name, ok := verify(reg, e.viewOfLocked(reg))
		if ok {
			e.lastSeen[name] = time.Now()
			counted++
		}
		e.mu.Unlock()
		if !ok {
			failed = true
		}
	}
	if failed {
		rejected = 1
		e.notVerified.Add(1)
	}
	return counted, rejected
}

// judgeAnswer is the recipient discovery's reading of one answer: it reports whether the answer carries a verified
// registration at the prompt subject want, and counts once an answer that carries a registration that fails
// verification. An invalid answer never ends the discovery. A nil e is the package-level Discover, which has no
// directory and, as it always has, matches the prompt subject alone (TestDiscoverMatchesSubjectNotMetadata): nothing in
// production calls it, `mash prompt` goes through Endpoint.DiscoverRecipient.
func (e *Endpoint) judgeAnswer(data []byte, want string) (vouched bool) {
	failed := false
	for _, reg := range parseAnswer(data) {
		if e != nil {
			e.mu.RLock()
			d := e.viewOfLocked(reg)
			e.mu.RUnlock()
			if _, ok := verify(reg, d); !ok {
				failed = true
				continue
			}
		}
		// Any peer can answer the broadcast: accept only the subject SAP derives from the target's name, so a
		// forged answer can neither redirect the prompt nor name a subject its sender could read.
		if reg.Subject == want {
			vouched = true
		}
	}
	if failed {
		e.notVerified.Add(1)
	}
	return vouched
}

// flatOf is a record's flat directory key, owner_group_instance.
func flatOf(r store.Record) string { return r.Owner + "_" + r.Group + "_" + r.Instance }

// Who returns the directory with presence, sorted by directory key. After a presence probe has completed an endpoint is online
// when it answered that probe or a later one; before any probe, when it was seen within OnlineWindow.
func (e *Endpoint) Who() []Presence {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Presence, 0, len(e.dir))
	for name, r := range e.dir {
		seen := e.lastSeen[name]
		online := !seen.IsZero() && time.Since(seen) < OnlineWindow
		if !e.probeAt.IsZero() {
			online = !seen.IsZero() && !seen.Before(e.probeAt)
		}
		out = append(out, Presence{Record: r, LastSeen: seen, Online: online})
	}
	sort.Slice(out, func(i, j int) bool { return flatOf(out[i].Record) < flatOf(out[j].Record) })
	return out
}

// WhoFresh refreshes presence when it is due, then returns Who(). It is for a running endpoint (the who tool in
// mash serve): it asks every agent for its registration at most once per PresenceCacheTTL, one probe at a time, and
// not again within PresenceRetryGap of a failed probe (ADR 0066). On a probe error it still returns the directory,
// whose presence is then that of the last probe that completed. A probe that no verified registration answered (not
// even this endpoint's, which may not have registered its service yet) is not kept for the cache time: it is retried after PresenceRetryGap, and
// is not an error to the caller (everyone is then offline).
func (e *Endpoint) WhoFresh(ctx context.Context) ([]Presence, error) {
	_, err := e.presence.Ensure(ctx, func(ctx context.Context) error {
		p := e.takeProbe()
		if p == nil {
			p = e.startPresence()
		}
		if err := e.awaitPresence(ctx, p); err != nil {
			return err
		}
		if p.counted == 0 {
			return errNoAnswers
		}
		return nil
	})
	if errors.Is(err, errNoAnswers) {
		err = nil
	}
	return e.Who(), err
}

var errNoAnswers = errors.New("mash: no registration answered the presence probe")

// LeaseInfo is the live host and mash version a session wrote to its lease.
type LeaseInfo struct{ Host, Version string }

// OwnLease reads this endpoint's own lease. It is the only lease an endpoint's permissions let it read, so other
// endpoints' live host and version are not available here. ok is false when there is no readable live lease.
func (e *Endpoint) OwnLease(ctx context.Context) (LeaseInfo, bool) {
	val, err := e.ownLease(ctx)
	if err != nil || fenced(val) {
		return LeaseInfo{}, false
	}
	var lv leaseValue
	if json.Unmarshal(val, &lv) != nil || lv.Host == "" && lv.Version == "" {
		return LeaseInfo{}, false
	}
	// #1327: a crashed serve's lease key stays live until it expires. A holder on this machine that is gone is not up. Our own pid is not exempt: a crashed holder's
	// pid can be reused by this process, and the recorded start time (holderGone) tells that lease from our own.
	if lv.Machine != "" && lv.Machine == ourMachine() && holderGone(lv.PID, lv.Start) {
		return LeaseInfo{}, false
	}
	return LeaseInfo{Host: lv.Host, Version: lv.Version}, true
}

// Refresh waits for the answers to who is online, for one-shot callers (the CLI's who) that don't Run. The directory
// was read when the client opened (ClientOptions.FullDirectory), so it is not read again. The wait ends once answers
// have come and gone quiet (PresenceQuiet), or after PresenceCap when nothing answers.
func (e *Endpoint) Refresh(ctx context.Context) error {
	p := e.takeProbe()
	if p == nil {
		p = e.startPresence()
	}
	return e.awaitPresence(ctx, p)
}

// loadDirectory reads every directory record and replaces the in-memory copy: the keys come from stream info (subjects
// filter), the values by direct gets, many at a time. A key that is gone is dropped (the new state reflects it); a key
// whose read failed keeps its previous record and makes the load return an error after installing what was read, so
// the poll loop does not record the stream state and reloads at its next poll.
func (e *Endpoint) loadDirectory(ctx context.Context) error {
	keys, err := e.directoryKeys(ctx)
	if err != nil {
		return fmt.Errorf("mash: reading the directory: %w", err)
	}
	if failed := e.installDirectory(e.fetchRecords(ctx, keys)); failed > 0 {
		return &partialLoadError{failed: failed, total: len(keys)}
	}
	return nil
}

// partialLoadError: some records could not be read (transiently) in a directory load that otherwise worked.
type partialLoadError struct{ failed, total int }

func (p *partialLoadError) Error() string {
	return fmt.Sprintf("mash: directory: %d of %d records could not be read; retrying at the next poll", p.failed, p.total)
}

// installDirectory replaces the in-memory directory with the records read. A key that is gone (absent, deleted, not a
// usable record) is left out; a key whose read failed keeps its previous record, if any. It returns how many reads failed.
func (e *Endpoint) installDirectory(got map[string]recordResult) (failed int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	dir := make(map[string]store.Record, len(got))
	for key, r := range got {
		e.warnBad(key, r)
		switch {
		case r.err == nil:
			dir[key] = r.rec
		case !r.gone:
			failed++
			if old, ok := e.dir[key]; ok {
				dir[key] = old
			}
		}
	}
	for n, r := range dir {
		if old, ok := e.dir[n]; ok && old.User != r.User {
			delete(e.lastSeen, n) // an answer the old key signed is not presence for the new key
		}
	}
	e.dir = dir
	e.byUser = make(map[string]string, len(dir))
	for n, r := range dir {
		e.byUser[r.User] = n
	}
	e.fullDir = true
	return failed
}

// maxFailedLookups bounds the remembered failed sender lookups.
const maxFailedLookups = 1024

// reloadSender reads the directory record of the endpoint a message's sender header names, bypassing the cached
// directory, and installs it when it is the record of the header's key. It reports whether the cache changed.
func (e *Endpoint) reloadSender(h sap.SenderHeader) bool {
	if h.Name == "" || h.User == "" || !recordKey(h.Name) {
		return false
	}
	// A lookup that found nothing for this (name, key) is not repeated for one directory poll interval, and the memory
	// of them is bounded: a full table evicts its oldest entry, never refusing a lookup.
	fail := h.Name + "\x00" + h.User
	e.mu.Lock()
	if until, ok := e.failedLookups[fail]; ok && time.Now().Before(until) {
		e.mu.Unlock()
		return false
	}
	if len(e.failedLookups) >= maxFailedLookups {
		// Full: expired entries go, and if none had expired the oldest (the earliest to expire) makes room, so a
		// flood of fake keys never stops the lookup of a real new sender.
		now := time.Now()
		oldestK, oldest := "", time.Time{}
		for k, until := range e.failedLookups {
			if !now.Before(until) {
				delete(e.failedLookups, k)
			} else if oldestK == "" || until.Before(oldest) {
				oldestK, oldest = k, until
			}
		}
		if len(e.failedLookups) >= maxFailedLookups && oldestK != "" {
			delete(e.failedLookups, oldestK)
		}
	}
	e.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r := e.fetchRecord(ctx, h.Name)
	if r.err != nil || r.rec.User != h.User {
		e.mu.Lock()
		if e.failedLookups == nil {
			e.failedLookups = map[string]time.Time{}
		}
		e.failedLookups[fail] = time.Now().Add(DirectoryPoll)
		e.mu.Unlock()
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dir == nil {
		e.dir = map[string]store.Record{}
	}
	if old, ok := e.dir[h.Name]; ok && old.User == r.rec.User {
		return false
	}
	if old, ok := e.dir[h.Name]; ok && e.byUser[old.User] == h.Name {
		delete(e.byUser, old.User)
	}
	if old, ok := e.dir[h.Name]; ok && old.User != r.rec.User {
		delete(e.lastSeen, h.Name) // an answer the old key signed is not presence for the new key
	}
	e.dir[h.Name] = r.rec
	if e.byUser == nil {
		e.byUser = map[string]string{}
	}
	e.byUser[r.rec.User] = h.Name
	return true
}
