package endpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// fetchConcurrency bounds the requests a client has in flight at once when it reads many records or messages. Over a
// slow link the requests of one batch cost one round trip together, not one each.
const fetchConcurrency = 64

// PresenceQuiet is how long the presence window stays open after the last answer once at least one has come: a
// one-shot who does not wait out PresenceCap for an answer that is not coming. An endpoint that answers later than
// this shows offline until its next heartbeat.
const PresenceQuiet = 300 * time.Millisecond

// PresenceCap is the longest the presence window stays open, and the whole wait when nothing answers.
const PresenceCap = 2 * time.Second

// leasePrefetchFresh is how old a lease read at open may be when AdoptLaunchInstance uses it. A send that waits for
// its body on stdin reads the lease again afterwards, so it never signs for a serve that has since gone.
const leasePrefetchFresh = 2 * time.Second

// ClientOptions say what a one-shot client needs. Everything asked for is requested together with the identity
// check, in the first wave of round trips after connecting.
type ClientOptions struct {
	// FullDirectory reads every directory record, which is what classifying mail from any sender and who both want.
	// Without it only the endpoint's own record and Records are read, and a command that shows mail reads the records
	// of the senders it shows (loadSenders).
	FullDirectory bool
	// Records are other directory records to read with the endpoint's own (the recipient of a send).
	Records []string
	// Inbox reads the endpoint's inbox stream along with the rest, for a command that reads its inbox.
	Inbox bool
	// Lease reads the endpoint's own lease, for AdoptLaunchInstance or OwnLease.
	Lease bool
	// Presence asks who is online at once, for Refresh to wait on.
	Presence bool

	// holding: Open will take the lease next, so read the lease bucket and the key in the same wave (holdProbe).
	holding bool
}

// prefetched is what the first wave read for the command that follows. Each part is used once.
type prefetched struct {
	mu       sync.Mutex
	inbox    jetstream.Stream
	inboxErr error
	inboxSet bool
	lease    []byte
	leaseErr error
	leaseAt  time.Time
	leaseSet bool
	probe    *presenceProbe
}

func (p *prefetched) takeInbox() (jetstream.Stream, error, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.inboxSet {
		return nil, nil, false
	}
	s, err := p.inbox, p.inboxErr
	p.inbox, p.inboxErr, p.inboxSet = nil, nil, false
	return s, err, true
}

func (p *prefetched) takeLease() ([]byte, error, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.leaseSet || time.Since(p.leaseAt) > leasePrefetchFresh {
		p.leaseSet = false
		return nil, nil, false
	}
	v, err := p.lease, p.leaseErr
	p.lease, p.leaseErr, p.leaseSet = nil, nil, false
	return v, err, true
}

func (e *Endpoint) takeProbe() *presenceProbe {
	e.pre.mu.Lock()
	defer e.pre.mu.Unlock()
	p := e.pre.probe
	e.pre.probe = nil
	return p
}

// parallel runs fn(0..n-1), at most limit at a time, and waits for all of them.
func parallel(n, limit int, fn func(i int)) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, limit)
	for i := range n {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			fn(i)
		}()
	}
	wg.Wait()
}

// apiRequest is a request-reply to the JetStream API; a context without a deadline gets the API's default five seconds.
func (e *Endpoint) apiRequest(ctx context.Context, subj string, body []byte) (*nats.Msg, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	return e.nc.RequestWithContext(ctx, subj, body)
}

// directGetKV reads one key of a KV bucket with a direct get: one request, no bucket bind before it. It fails like a
// KV Get does for a key that is absent (jetstream.ErrKeyNotFound) or deleted (jetstream.ErrKeyDeleted).
func (e *Endpoint) directGetKV(ctx context.Context, bucket, key string) ([]byte, error) {
	v, _, err := e.directGetKVAt(ctx, bucket, key)
	return v, err
}

// directGetKVRev is directGetKV for a caller that wants only the key's state: its error, and the revision it was read
// at (the Nats-Sequence of the entry, or of the delete marker).
func (e *Endpoint) directGetKVRev(ctx context.Context, bucket, key string) (uint64, error) {
	_, rev, err := e.directGetKVAt(ctx, bucket, key)
	return rev, err
}

func (e *Endpoint) directGetKVAt(ctx context.Context, bucket, key string) ([]byte, uint64, error) {
	m, err := e.apiRequest(ctx, "$JS.API.DIRECT.GET.KV_"+bucket+".$KV."+bucket+"."+key, nil)
	if err != nil {
		return nil, 0, err
	}
	if st := m.Header.Get("Status"); st != "" {
		if st == "404" {
			return nil, 0, jetstream.ErrKeyNotFound
		}
		return nil, 0, fmt.Errorf("direct get %s: status %s %s", key, st, m.Header.Get("Description"))
	}
	rev, _ := strconv.ParseUint(m.Header.Get("Nats-Sequence"), 10, 64)
	switch m.Header.Get("KV-Operation") {
	case "DEL", "PURGE":
		return nil, rev, jetstream.ErrKeyDeleted
	}
	return m.Data, rev, nil
}

// recordResult is one directory record as read: the record, or why there is none. gone is a key that is absent,
// deleted or not a usable record (as opposed to a failed read).
type recordResult struct {
	rec  store.Record
	err  error
	gone bool
}

// fetchRecordHook, set only by tests, fails the read of a key before it is made.
var fetchRecordHook atomic.Pointer[func(key string) error]

func (e *Endpoint) fetchRecord(ctx context.Context, key string) recordResult {
	if hp := fetchRecordHook.Load(); hp != nil {
		if err := (*hp)(key); err != nil {
			return recordResult{err: fmt.Errorf("directory %s: %w", key, err),
				gone: errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted)}
		}
	}
	v, err := e.directGetKV(ctx, store.DirectoryBucket, key)
	if err != nil {
		return recordResult{err: fmt.Errorf("directory %s: %w", key, err),
			gone: errors.Is(err, jetstream.ErrKeyNotFound) || errors.Is(err, jetstream.ErrKeyDeleted)}
	}
	rec, err := store.ParseRecord(key, v)
	return recordResult{rec: rec, err: err, gone: err != nil}
}

// recordKey reports whether key is safe to put in a subject: a directory key is an endpoint name, but the keys of a
// stream's subjects are whatever was written.
func recordKey(key string) bool {
	return key != "" && !strings.ContainsAny(key, " \t\r\n*>")
}

// fetchRecords reads the records for keys, many at a time.
func (e *Endpoint) fetchRecords(ctx context.Context, keys []string) map[string]recordResult {
	out := make([]recordResult, len(keys))
	parallel(len(keys), fetchConcurrency, func(i int) {
		if !recordKey(keys[i]) {
			out[i] = recordResult{err: errors.New("not a directory key"), gone: true}
			return
		}
		out[i] = e.fetchRecord(ctx, keys[i])
	})
	got := make(map[string]recordResult, len(keys))
	for i, k := range keys {
		got[k] = out[i]
	}
	return got
}

// directoryKeys lists the directory's keys: the subjects of a filtered stream info (a consumer or a multi-subject
// get would be denied to an endpoint).
func (e *Endpoint) directoryKeys(ctx context.Context) ([]string, error) {
	prefix := "$KV." + store.DirectoryBucket + "."
	subj := "$JS.API.STREAM.INFO.KV_" + store.DirectoryBucket
	var keys []string
	for {
		body, _ := json.Marshal(map[string]any{"offset": len(keys), "subjects_filter": prefix + ">"})
		m, err := e.apiRequest(ctx, subj, body)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Error *struct {
				Description string `json:"description"`
			} `json:"error"`
			Total int `json:"total"`
			State struct {
				Subjects map[string]uint64 `json:"subjects"`
			} `json:"state"`
		}
		if err := json.Unmarshal(m.Data, &resp); err != nil {
			return nil, err
		}
		if resp.Error != nil {
			return nil, errors.New(resp.Error.Description)
		}
		for s := range resp.State.Subjects {
			keys = append(keys, strings.TrimPrefix(s, prefix))
		}
		if len(resp.State.Subjects) == 0 || len(keys) >= resp.Total {
			return keys, nil
		}
	}
}

// directoryState reads the directory stream's state with an unfiltered stream info: a few hundred bytes whatever the
// directory's size, and the same permission the key list needs.
func (e *Endpoint) directoryState(ctx context.Context) (DirState, error) {
	m, err := e.apiRequest(ctx, "$JS.API.STREAM.INFO.KV_"+store.DirectoryBucket, []byte("{}"))
	if err != nil {
		return DirState{}, fmt.Errorf("mash: reading the directory state: %w", err)
	}
	st, err := ParseDirState(m.Data)
	if err != nil {
		return DirState{}, fmt.Errorf("mash: reading the directory state: %w", err)
	}
	return st, nil
}

// init binds the directory, checks the identity and reads what the command that follows asked for, all of it in the
// first wave: the requests that do not depend on one another go out together. Reading the whole directory takes a
// second wave, for the values of the keys the first one listed.
func (e *Endpoint) init(ctx context.Context, opts ClientOptions) error {
	js, err := jetstream.New(e.nc)
	if err != nil {
		return err
	}
	e.js = js

	want := []string{e.key}
	for _, n := range opts.Records {
		n = sap.KeyOf(n) // a recipient may be written canonical (ADR 0081)
		// A key the name rule refuses is not read: lookup of it reports the rule, not the record.
		if n != e.key && validDirectoryKey(n) && e.checkKey(pointLookup, n) == nil {
			want = append(want, n)
		}
	}
	var (
		kvErr, keysErr error
		keys           []string
		got            map[string]recordResult
	)
	tasks := []func(){
		func() { e.dirKV, kvErr = js.KeyValue(ctx, store.DirectoryBucket) },
		func() { got = e.fetchRecords(ctx, want) },
	}
	if opts.FullDirectory {
		tasks = append(tasks, func() { keys, keysErr = e.directoryKeys(ctx) })
	}
	if opts.Inbox {
		tasks = append(tasks, func() {
			e.pre.inbox, e.pre.inboxErr = js.Stream(ctx, sap.InboxStream(e.key))
			e.pre.inboxSet = true
		})
	}
	if opts.holding {
		tasks = append(tasks, func() {
			if kv, err := js.KeyValue(ctx, store.LeaseBucket); err == nil {
				e.hold.kv = kv
			}
		}, func() {
			e.hold.rev, e.hold.err = e.directGetKVRev(ctx, store.LeaseBucket, e.key)
		})
	}
	if opts.Lease {
		tasks = append(tasks, func() {
			e.pre.lease, e.pre.leaseErr = e.directGetKV(ctx, store.LeaseBucket, e.key)
			e.pre.leaseAt, e.pre.leaseSet = time.Now(), true
		})
	}
	parallel(len(tasks), len(tasks), func(i int) { tasks[i]() })

	if kvErr != nil {
		return fmt.Errorf("mash: directory: %w", kvErr)
	}
	selfRes := got[e.key]
	if selfRes.err != nil {
		return fmt.Errorf("mash: endpoint %s is not provisioned: %w", e.Display(), selfRes.err)
	}
	self := selfRes.rec
	if self.User != e.id.User {
		return fmt.Errorf("mash: the credentials in %s (user %s) do not match endpoint %s (user %s)",
			e.cfg.Creds, e.id.User, e.Display(), self.User)
	}
	e.self = self
	// The compatibility alias is an identity the record answers to: the rule applies to it too. The alias is data of
	// the endpoint's own record, not an identity the caller requested (that one is checked before the directory is
	// read), so it can be checked only once the record is read: an absent record has no alias and reports "not
	// provisioned" above. Nothing else is read for the caller past this point (the rest of the directory, the
	// consumers), so a refused alias stops the start before any of it.
	if alias, ok, err := self.AliasIdentity(); err == nil && ok {
		if err := checkRule(e.cfg.NameRule, pointAlias, alias); err != nil {
			return err
		}
	}
	// The endpoint's own mail subject is the one its record says: a zoned endpoint advertises its zoned subject.
	e.subj.Mail = self.MailSubject
	if opts.FullDirectory {
		if keysErr != nil {
			return fmt.Errorf("mash: reading the directory: %w", keysErr)
		}
		// The first wave read the endpoint's own record and the ones asked for; the rest are read now.
		var rest []string
		for _, k := range keys {
			if _, ok := got[k]; !ok {
				rest = append(rest, k)
			}
		}
		for k, r := range e.fetchRecords(ctx, rest) {
			got[k] = r
		}
		for k := range got {
			if !contains(keys, k) {
				delete(got, k) // read in the first wave, but no longer (or never) in the directory
			}
		}
		e.installDirectory(got)
		return nil
	}
	e.learn(got)
	return nil
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// learn adds the records read to the in-memory directory, without replacing what is there.
func (e *Endpoint) learn(got map[string]recordResult) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for name, r := range got {
		if r.err == nil {
			e.dir[name] = r.rec
			e.byUser[r.rec.User] = name
		}
	}
}

// ownLease is the raw value of this endpoint's own lease: the one read at open when it is fresh, else a direct get now.
func (e *Endpoint) ownLease(ctx context.Context) ([]byte, error) {
	if v, err, ok := e.pre.takeLease(); ok {
		return v, err
	}
	return e.directGetKV(ctx, store.LeaseBucket, e.key)
}

// loadSenders reads the directory records the senders of raw need before the mail is classified: a client that did
// not load the whole directory knows only its own record. A signed message names its sender, and the record that
// name has in the directory (or the lack of one) is all classify asks of it. A signed message with no name is told
// from the key alone, and for that the whole directory is read, as it always was.
func (e *Endpoint) loadSenders(ctx context.Context, raws []*jetstream.RawStreamMsg) error {
	e.mu.RLock()
	full := e.fullDir
	e.mu.RUnlock()
	if full {
		return nil
	}
	need := map[string]bool{}
	for _, raw := range raws {
		h, trust := sap.VerifyStored(raw.Header.Get(sap.HeaderName), raw.Subject, raw.Data)
		switch trust {
		case sap.TrustForged, sap.TrustMalformed, sap.TrustAbsent, sap.TrustClaimed:
			continue // never looked up
		}
		switch {
		case h.Name == "":
			return e.loadSenderDirectory(ctx)
		case validDirectoryKey(h.Name):
			need[h.Name] = true
		}
	}
	e.mu.RLock()
	var keys []string
	for n := range need {
		if _, ok := e.dir[n]; !ok {
			keys = append(keys, n)
		}
	}
	e.mu.RUnlock()
	got := e.fetchRecords(ctx, keys)
	for _, r := range got {
		if r.err != nil && !r.gone {
			return fmt.Errorf("mash: reading the directory: %w", r.err)
		}
	}
	e.learn(got)
	return nil
}

// loadSenderDirectory loads the whole directory for a classify that needs it. Records that could not be read were
// never an error here: the load's partial result is used, as the poll loop retries the rest.
func (e *Endpoint) loadSenderDirectory(ctx context.Context) error {
	err := e.loadDirectory(ctx)
	var pe *partialLoadError
	if errors.As(err, &pe) {
		e.log.Print(err)
		return nil
	}
	return err
}

// presenceProbe is one $SRV.INFO request and the answers to it so far.
type presenceProbe struct {
	sub   *nats.Subscription
	start time.Time
	kick  chan struct{}

	err     error // the request could not be sent
	counted int   // registrations awaitPresence counted (verified, the directory's key)

	mu   sync.Mutex
	msgs []*nats.Msg
	last time.Time
}

// startPresence asks every SAP agent for its registration. The request goes out with whatever else the caller sends
// next, and the answers are held until awaitPresence.
func (e *Endpoint) startPresence() *presenceProbe {
	p := &presenceProbe{start: time.Now(), kick: make(chan struct{}, 1)}
	inbox := e.nc.NewInbox()
	sub, err := e.nc.Subscribe(inbox, func(m *nats.Msg) {
		p.mu.Lock()
		p.msgs = append(p.msgs, m)
		p.last = time.Now()
		p.mu.Unlock()
		select {
		case p.kick <- struct{}{}:
		default:
		}
	})
	if err != nil {
		p.err = err
		return p
	}
	p.sub = sub
	if err := e.nc.PublishRequest("$SRV.INFO."+sap.ServiceName, inbox, nil); err != nil {
		p.err = err
	}
	return p
}

// awaitPresence waits for the window to close, then records the answers. The window closes PresenceQuiet after the
// last answer once there is one, or PresenceCap after the request, whichever is first. A completed probe's start time
// becomes the line below which an endpoint is not online. It returns an error when the request could not be sent or
// ctx ended first; nothing is recorded then.
func (e *Endpoint) awaitPresence(ctx context.Context, p *presenceProbe) error {
	if p.sub == nil || p.err != nil {
		if p.sub != nil {
			_ = p.sub.Unsubscribe()
		}
		return fmt.Errorf("mash: presence probe: %w", p.err)
	}
	defer func() { _ = p.sub.Unsubscribe() }()
	deadline := p.start.Add(PresenceCap)
	for {
		p.mu.Lock()
		n, last := len(p.msgs), p.last
		p.mu.Unlock()
		wait := time.Until(deadline)
		if n > 0 {
			wait = min(wait, PresenceQuiet-time.Since(last))
		}
		if wait <= 0 {
			break
		}
		t := time.NewTimer(wait)
		select {
		case <-p.kick:
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		}
		t.Stop()
	}
	p.mu.Lock()
	msgs := p.msgs
	p.mu.Unlock()
	for _, m := range msgs {
		p.counted += e.applyPresence(m)
	}
	e.mu.Lock()
	if p.start.After(e.probeAt) {
		e.probeAt = p.start
	}
	e.mu.Unlock()
	return nil
}

// validDirectoryKey reports whether name can be a directory key: a legacy endpoint
// name, or the flat form (agent_owner_name) of a new-form identity. The canonical agent.owner.name is an input form
// only: callers turn it into the flat key first (sap.KeyOf).
func validDirectoryKey(name string) bool {
	if config.ValidName(name) {
		return true
	}
	_, err := sap.FromFlat(name)
	return err == nil
}
