package endpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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

// applyPresence records the agents a $SRV.INFO answer vouches for as seen now, when its id_sig verifies and its key
// is the directory's key for the address in the prompt subject. The address comes from the subject alone; metadata
// owner is not a filter (an SDK host registers under another token) and never names the address.
func (e *Endpoint) applyPresence(m *nats.Msg) (counted int) {
	if h := s5Hook(); h != nil {
		return e.applyPresenceS5(h, m)
	}
	var info struct {
		Metadata  map[string]string `json:"metadata"`
		Endpoints []struct {
			Name    string `json:"name"`
			Subject string `json:"subject"`
		} `json:"endpoints"`
	}
	if json.Unmarshal(m.Data, &info) != nil {
		return 0
	}
	md := info.Metadata
	for _, ep := range info.Endpoints {
		if ep.Name != "prompt" {
			continue
		}
		a, _, ok := sap.AddressFromSubject(ep.Subject)
		name := a.Flat()
		if !ok || !sap.VerifyIDSig(md["user_nkey"], md["account"], md["agent"], md["owner"], ep.Subject, md["id_sig"]) {
			continue
		}
		e.mu.Lock()
		if e.byUser[md["user_nkey"]] == name {
			e.lastSeen[name] = time.Now()
			counted++
		}
		e.mu.Unlock()
	}
	return counted
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
