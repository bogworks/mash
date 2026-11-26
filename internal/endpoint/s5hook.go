package endpoint

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// The S5 checks of discovery (REQ-016, TO-12) sit behind a switch. s5Switch is nil until something sets it, and while
// it is nil every discovery path is main's own: presence uses the client's NewInbox and counts an answer whose id_sig
// verifies and whose user_nkey is the directory's for the subject's address; the recipient discovery of prompt returns
// on the first answer that names the prompt subject; nothing is counted. The shipped mash binary sets the switch once at
// startup, through EnableVerifiedDiscovery (cmd/mash/s5wire.go); the tests set it themselves. The verify step itself
// lives in internal/sapsdk/discovery, which this package never imports, so the hook receives it as a function.
//
// The hook is an atomic pointer: a poller left over from one test may read it while the next test stores it. Every read
// loads it once through s5Hook and keeps the value. A discovery that returns early keeps reading its window in the
// background; it holds the hook it started with.

// s5Hooks is the S5 switch-on.
type s5Hooks struct {
	// verify is the one verify step: it returns the address name the registration vouches for and whether it is
	// verified against the directory's view.
	verify func(reg registration, d *dirView) (name string, ok bool)
	// secretPresence makes the presence probe use secretInbox (128 random bits) for its reply subject.
	secretPresence bool
	// promptDirectory makes the recipient discovery of prompt verify the answers against the directory record, which
	// it reads on demand, and count those that fail.
	promptDirectory bool
}

// s5Switch is the switch: nil until EnableVerifiedDiscovery (or a test) sets it. Read it only through s5Hook.
var s5Switch atomic.Pointer[s5Hooks]

// EnableVerifiedDiscovery turns the S5 checks on for the life of the process: the verify step, the secret presence
// reply subject, and the directory check in the recipient discovery of prompt. verify is the step of
// internal/sapsdk/discovery, handed in by the command (this package does not import it): the registration's prompt
// subject and metadata, and what the directory says about it (whether a record exists, and the record's user key, owner
// and account). It returns the address name the registration vouches for and whether it is verified.
func EnableVerifiedDiscovery(verify func(subject string, metadata map[string]string, hasRecord bool, user, owner, account string) (name string, ok bool)) {
	s5Switch.Store(&s5Hooks{
		verify: func(reg registration, d *dirView) (string, bool) {
			return verify(reg.Subject, reg.Metadata, d.hasRec, d.rec.User, d.rec.Owner, d.rec.Account)
		},
		secretPresence:  true,
		promptDirectory: true,
	})
}

// DisableVerifiedDiscoveryForTest clears the switch, so a test that set it through EnableVerifiedDiscovery leaves the
// process as it found it. Nothing in the shipped binaries calls it.
func DisableVerifiedDiscoveryForTest() { s5Switch.Store(nil) }

// VerifiedDiscoveryEnabled reports whether the S5 checks are on.
func VerifiedDiscoveryEnabled() bool { return s5Hook() != nil }

// s5Hook loads the switch once; the caller keeps the value it got.
func s5Hook() *s5Hooks { return s5Switch.Load() }

// registration is one prompt endpoint of a $SRV.INFO answer, with the metadata that vouches for it.
type registration struct {
	Subject  string
	Metadata map[string]string
}

// dirView is what the directory says about one registration: the record of the address its prompt subject names. The
// record's own User field is the key that address holds now; the reverse index is not consulted.
type dirView struct {
	rec    store.Record
	hasRec bool
}

// AnswersNotVerified is how many discovery answers failed verification over this endpoint's life, each counted once
// per discovery (TO-12, REQ-016). `mash who` and the MCP who tool show it (the warning line and
// answers_not_verified). It stays 0 while the S5 switch is off; the shipped binaries turn it on at startup.
func (e *Endpoint) AnswersNotVerified() int { return int(e.notVerified.Load()) }

// presenceInbox is the reply subject of a presence probe.
func (e *Endpoint) presenceInbox() string {
	if h := s5Hook(); h != nil && h.secretPresence {
		return secretInbox(e.nc)
	}
	return e.nc.NewInbox()
}

// parseAnswer is the prompt registrations a $SRV.INFO answer carries. An answer that is not JSON, or names no prompt
// endpoint, carries none: it is not a registration to verify, and is not counted.
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

// viewOfLocked reads the directory for reg. The caller holds e.mu.
func (e *Endpoint) viewOfLocked(reg registration) *dirView {
	d := &dirView{}
	if a, _, ok := sap.AddressFromSubject(reg.Subject); ok {
		d.rec, d.hasRec = e.dir[a.Flat()]
	}
	return d
}

// applyPresenceS5 is applyPresence with the S5 verify step: it records the agents the step accepts as seen now,
// and counts once, in AnswersNotVerified, an answer that carried a registration the step refused. It returns how many
// registrations it recorded. The step runs outside e.mu, on a copy of the directory view taken under the read lock;
// the write lock is taken only to record a verified registration, and then the record is read again: the user key,
// owner and account the step saw must be unchanged, so if the name's record was replaced, or removed, or its owner or
// account changed while the step ran, the registration is not recorded and counts as not verified.
func (e *Endpoint) applyPresenceS5(h *s5Hooks, m *nats.Msg) (counted int) {
	regs := parseAnswer(m.Data)
	views := make([]*dirView, len(regs))
	e.mu.RLock()
	for i, reg := range regs {
		views[i] = e.viewOfLocked(reg)
	}
	e.mu.RUnlock()
	failed := false
	for i, reg := range regs {
		name, ok := h.verify(reg, views[i])
		if ok {
			e.mu.Lock()
			cur, has := e.dir[name]
			if has && views[i].hasRec && sameVerifiedFields(cur, views[i].rec) {
				e.lastSeen[name] = time.Now()
				counted++
			} else {
				ok = false
			}
			e.mu.Unlock()
		}
		if !ok {
			failed = true
		}
	}
	if failed {
		e.notVerified.Add(1)
	}
	return counted
}

// sameVerifiedFields reports whether two records agree on the fields verification reads: the user key, the owner and
// the account. (Record holds a map, so it cannot be compared with ==.)
func sameVerifiedFields(a, b store.Record) bool {
	return a.User == b.User && a.Owner == b.Owner && a.Account == b.Account
}

// judgeAnswerS5 reports whether an answer carries a verified registration at the prompt subject want, and counts once
// an answer whose registration at want fails verification. A registration at any other subject is another endpoint's
// and not this discovery's business: it is neither verified nor counted (a one-shot client holds only its own record
// and the target's). An accepted answer's record is read again under the lock, as in applyPresenceS5. An invalid answer never ends the discovery.
func (e *Endpoint) judgeAnswerS5(h *s5Hooks, data []byte, want string) (vouched bool) {
	failed := false
	for _, reg := range parseAnswer(data) {
		if reg.Subject != want {
			continue
		}
		e.mu.RLock()
		d := e.viewOfLocked(reg)
		e.mu.RUnlock()
		name, ok := h.verify(reg, d)
		if ok {
			e.mu.RLock()
			cur, has := e.dir[name]
			ok = has && d.hasRec && sameVerifiedFields(cur, d.rec)
			e.mu.RUnlock()
		}
		if !ok {
			failed = true
			continue
		}
		vouched = true
	}
	if failed {
		e.notVerified.Add(1)
	}
	return vouched
}

// discoverRecipientS5 is the recipient discovery of prompt with the S5 checks: the recipient's directory record is
// read when this endpoint does not hold it, and an answer is accepted only when the verify step accepts it.
func (e *Endpoint) discoverRecipientS5(ctx context.Context, h *s5Hooks, arg string) (string, error) {
	a, err := parseRecipient(arg)
	if err != nil {
		return "", err
	}
	s, err := a.Subjects()
	if err != nil {
		return "", err
	}
	e.ensureRecord(ctx, a.Flat())
	return e.discoverSubjectS5(ctx, h, s.Prompt, a.Flat())
}

// ensureRecord reads the directory record of key when this endpoint does not hold it yet (a one-shot client reads only
// the records it was asked for). A record that cannot be read is left out: no answer then verifies.
func (e *Endpoint) ensureRecord(ctx context.Context, key string) {
	e.mu.RLock()
	_, ok := e.dir[key]
	e.mu.RUnlock()
	if !ok {
		e.learn(e.fetchRecords(ctx, []string{key}))
	}
}

// discoverSubjectS5 sends one discovery request and returns want when an answer carries a verified registration at
// that prompt subject. On a valid answer the caller is not kept waiting: the rest of the window is read in the
// background, so a late invalid answer is still counted.
func (e *Endpoint) discoverSubjectS5(ctx context.Context, h *s5Hooks, want, name string) (string, error) {
	inbox := secretInbox(e.nc)
	sub, err := e.nc.SubscribeSync(inbox)
	if err != nil {
		return "", err
	}
	if err := e.nc.PublishRequest("$SRV.INFO."+sap.ServiceName, inbox, nil); err != nil {
		_ = sub.Unsubscribe()
		return "", err
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		m, err := sub.NextMsg(time.Until(deadline))
		if err != nil {
			break
		}
		if !e.judgeAnswerS5(h, m.Data, want) {
			continue
		}
		go func() {
			defer sub.Unsubscribe()
			for time.Now().Before(deadline) {
				m, err := sub.NextMsg(time.Until(deadline))
				if err != nil {
					return
				}
				e.judgeAnswerS5(h, m.Data, want)
			}
		}()
		return want, nil
	}
	_ = sub.Unsubscribe()
	return "", fmt.Errorf("mash: %s is not online (no SAP registration answered)", sap.Display(name))
}
