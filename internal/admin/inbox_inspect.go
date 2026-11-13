package admin

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// InspectSchema is the schema string of the JSON `mashctl inbox inspect` prints. It is a contract with the readers of
// that output and changes only when a field is removed or changes meaning.
const InspectSchema = "mash-inbox-inspect@1"

// DefaultInspectLimit is how many messages an inspection returns when no limit is given.
const DefaultInspectLimit = 20

// Delivery states of a stored message, derived only from the consumer's cursors.
const (
	StateAcked       = "acked"                                   // at or below the consumer's ack floor
	StateUndelivered = "undelivered"                             // above the consumer's last delivered stream sequence
	StateAckUnsure   = "delivered-unacked-or-acked-out-of-order" // delivered, above the ack floor: NATS does not say which
)

// InspectLimits are the things an inspection cannot say, in the words the output carries.
var InspectLimits = []string{
	"per-message delivery count and ack time are not kept by JetStream or mash; the consumer's num_redelivered is the aggregate",
	"a message above the consumer's ack floor and at or below its last delivered sequence is delivered and either not acknowledged yet or acknowledged out of order: JetStream does not report acknowledgements above the ack floor",
}

// inspectMaxSignedAge is the age past which a signature made before the message was stored is called stale. It is the
// endpoint's MaxSignedAge (which this package cannot import).
const inspectMaxSignedAge = 5 * time.Minute

// RefusedError is a refusal of an inspection that is the caller's to fix (an unknown endpoint, no stream), as against
// a failure to read the server.
type RefusedError struct{ Msg string }

func (e *RefusedError) Error() string { return e.Msg }

// InboxTarget is the inbox an address names.
type InboxTarget struct {
	Address string // as the caller gave it
	Flat    string // the flat key of the inbox stream that is read
	Stream  string // inbox-<flat>
	Legacy  bool   // the stream is the alias's legacy one
}

// InspectOptions bound an inspection. From and To are an inclusive stream-sequence range (0 is unset) and Limit caps
// the messages (0 is DefaultInspectLimit). With From set the first Limit messages from it are returned; otherwise the
// last Limit messages up to To (or the last sequence). Either way the result is oldest first.
type InspectOptions struct {
	From, To uint64
	Limit    int
}

// InspectedStream is the stream's own counters.
type InspectedStream struct {
	Name     string
	FirstSeq uint64
	LastSeq  uint64
	Messages uint64
	Bytes    uint64
}

// Cursor is a stream sequence and the consumer sequence that goes with it.
type Cursor struct{ Stream, Consumer uint64 }

// InspectedConsumer is what CONSUMER.INFO says of the `deliver` consumer.
type InspectedConsumer struct {
	Name           string
	AckFloor       Cursor
	Delivered      Cursor
	NumPending     uint64
	NumRedelivered int
	NumAckPending  int
}

// InspectedMessage is one stored message and its delivery state.
type InspectedMessage struct {
	Seq      uint64
	MsgID    string
	Sender   string
	Trust    string
	StoredAt time.Time
	Size     int
	Kind     string
	State    string
}

// InboxInspection is what InspectInbox read. Consumer is nil when the stream has no `deliver` consumer.
type InboxInspection struct {
	Stream   InspectedStream
	Consumer *InspectedConsumer
	Messages []InspectedMessage
}

// DeliveryState is the state of the message at stream sequence seq given the consumer (nil when there is none). It is
// derived from the cursors alone.
func DeliveryState(seq uint64, c *InspectedConsumer) string {
	switch {
	case c == nil:
		return StateUndelivered
	case seq <= c.AckFloor.Stream:
		return StateAcked
	case seq > c.Delivered.Stream:
		return StateUndelivered
	}
	return StateAckUnsure
}

// ClampRange is the inclusive range [lo, hi] a request for from..to (0 unset) covers in a stream holding first..last.
// ok is false when nothing of it is in the stream.
func ClampRange(from, to, first, last uint64) (lo, hi uint64, ok bool) {
	if last == 0 || last < first {
		return 0, 0, false
	}
	lo, hi = first, last
	if from > lo {
		lo = from
	}
	if to != 0 && to < hi {
		hi = to
	}
	return lo, hi, lo <= hi
}

// inspectSeqs is the sequences to read, in the order to read them, for a clamped range, a limit and whether the
// range starts at From (read upward) or ends at the top (read downward).
func inspectSeqs(lo, hi uint64, limit int, upward bool) []uint64 {
	n := hi - lo + 1
	if n > uint64(limit) {
		n = uint64(limit)
	}
	out := make([]uint64, 0, n)
	for i := uint64(0); i < n; i++ {
		if upward {
			out = append(out, lo+i)
		} else {
			out = append(out, hi-i)
		}
	}
	return out
}

// parseInboxAddress is the directory key an address names: the endpoint name (legacy), the flat key agent_owner_name,
// or the dotted agent.owner.name. A dotted address with the legacy agent token names the legacy endpoint owner.name.
// owner is the owner a dotted address names ("" for the other forms), which the record must carry.
func parseInboxAddress(address string) (key, owner string, err error) {
	switch {
	case strings.Contains(address, "."):
		p := strings.Split(address, ".")
		if len(p) != 3 {
			return "", "", fmt.Errorf("address is not agent.owner.name, agent_owner_name or an endpoint name")
		}
		if p[0] == sap.AgentToken {
			id, err := sap.Legacy(p[1], p[2])
			if err != nil {
				return "", "", err
			}
			return id.Flat(), id.Owner, nil
		}
		id, err := sap.New(p[0], p[1], p[2])
		if err != nil {
			return "", "", err
		}
		return id.Flat(), id.Owner, nil
	case strings.Contains(address, "_"):
		id, err := sap.FromFlat(address)
		if err != nil {
			return "", "", err
		}
		return id.Flat(), "", nil
	}
	if !config.ValidName(address) {
		return "", "", fmt.Errorf("address is not agent.owner.name, agent_owner_name or an endpoint name")
	}
	return address, "", nil
}

// ResolveInbox finds the directory record address names and the inbox stream to read. It reads the one directory key
// and nothing else: it lists no keys and creates no consumer. With legacy it names the alias's legacy stream; a record
// without an alias is refused.
func (a *Admin) ResolveInbox(ctx context.Context, address string, legacy bool) (InboxTarget, error) {
	key, owner, err := parseInboxAddress(address)
	if err != nil {
		return InboxTarget{}, &RefusedError{Msg: fmt.Sprintf("mash: inbox %s: %v", config.Clean(address), err)}
	}
	kv, err := a.dir(ctx)
	if err != nil {
		return InboxTarget{}, err
	}
	e, err := kv.Get(ctx, key)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return InboxTarget{}, &RefusedError{Msg: fmt.Sprintf("mash: no endpoint %s in the directory", config.Clean(address))}
	}
	if err != nil {
		return InboxTarget{}, fmt.Errorf("mash: directory %s: %w", config.Clean(key), err)
	}
	rec, err := store.ParseRecord(key, e.Value())
	if err != nil {
		return InboxTarget{}, &RefusedError{Msg: fmt.Sprintf("mash: directory record %s is not readable: %v", config.Clean(key), err)}
	}
	id, err := rec.Identity()
	if err != nil {
		return InboxTarget{}, &RefusedError{Msg: err.Error()}
	}
	if id.Flat() != key || (owner != "" && owner != id.Owner) {
		return InboxTarget{}, &RefusedError{Msg: fmt.Sprintf("mash: no endpoint %s in the directory", config.Clean(address))}
	}
	t := InboxTarget{Address: address, Flat: key}
	if legacy {
		aid, ok, err := rec.AliasIdentity()
		switch {
		case err != nil:
			return InboxTarget{}, &RefusedError{Msg: fmt.Sprintf("mash: %s has an alias that names no legacy identity: %v", config.Clean(address), err)}
		case !ok:
			return InboxTarget{}, &RefusedError{Msg: fmt.Sprintf("mash: %s has no alias, so it has no legacy inbox", config.Clean(address))}
		}
		t.Flat, t.Legacy = aid.Flat(), true
	}
	t.Stream = sap.InboxStream(t.Flat)
	return t, nil
}

// InspectInbox reads the stored messages of inbox-<flat> and the delivery state of its `deliver` consumer. It uses
// only STREAM.INFO, a direct get by sequence and CONSUMER.INFO of the existing consumer (and directory key reads to
// name senders), so it changes nothing: it never creates, binds or fetches from a consumer and never acknowledges.
func (a *Admin) InspectInbox(ctx context.Context, flat string, opts InspectOptions) (*InboxInspection, error) {
	if opts.Limit < 0 {
		return nil, fmt.Errorf("mash: inspect limit %d: must be 1 or more", opts.Limit)
	}
	if opts.Limit == 0 {
		opts.Limit = DefaultInspectLimit
	}
	if opts.From != 0 && opts.To != 0 && opts.From > opts.To {
		return nil, &RefusedError{Msg: fmt.Sprintf("mash: inspect: --from %d is after --to %d", opts.From, opts.To)}
	}
	name := sap.InboxStream(flat)
	s, err := a.js.Stream(ctx, name)
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		return nil, &RefusedError{Msg: fmt.Sprintf("mash: no inbox stream %s", config.Clean(name))}
	}
	if err != nil {
		return nil, fmt.Errorf("mash: stream %s: %w", config.Clean(name), err)
	}
	st := s.CachedInfo().State
	out := &InboxInspection{Stream: InspectedStream{Name: name, FirstSeq: st.FirstSeq, LastSeq: st.LastSeq, Messages: st.Msgs, Bytes: st.Bytes}}

	c, err := a.js.Consumer(ctx, name, store.Consumer)
	switch {
	case errors.Is(err, jetstream.ErrConsumerNotFound):
	case err != nil:
		return nil, fmt.Errorf("mash: stream %s consumer: %w", config.Clean(name), err)
	default:
		ci := c.CachedInfo()
		out.Consumer = &InspectedConsumer{Name: ci.Name,
			AckFloor:   Cursor{Stream: ci.AckFloor.Stream, Consumer: ci.AckFloor.Consumer},
			Delivered:  Cursor{Stream: ci.Delivered.Stream, Consumer: ci.Delivered.Consumer},
			NumPending: ci.NumPending, NumRedelivered: ci.NumRedelivered, NumAckPending: ci.NumAckPending}
	}

	lo, hi, ok := ClampRange(opts.From, opts.To, st.FirstSeq, st.LastSeq)
	if !ok || st.Msgs == 0 {
		out.Messages = []InspectedMessage{}
		return out, nil
	}
	senders := &senderCache{users: map[string]string{}}
	out.Messages = []InspectedMessage{}
	for _, seq := range inspectSeqs(lo, hi, opts.Limit, opts.From != 0) {
		raw, err := s.GetMsg(ctx, seq)
		if errors.Is(err, jetstream.ErrMsgNotFound) {
			continue // gone: expired or dropped by a limit
		}
		if err != nil {
			return nil, fmt.Errorf("mash: stream %s message %d: %w", config.Clean(name), seq, err)
		}
		m := InspectedMessage{Seq: raw.Sequence, StoredAt: raw.Time.UTC(), Size: len(raw.Data),
			MsgID: config.Clean(raw.Header.Get(jetstream.MsgIDHeader)), Kind: config.Clean(raw.Header.Get("Mash-Kind"))}
		if m.Seq == 0 {
			m.Seq = seq
		}
		m.Sender, m.Trust = a.inspectSender(ctx, senders, raw)
		m.State = DeliveryState(m.Seq, out.Consumer)
		out.Messages = append(out.Messages, m)
	}
	if opts.From == 0 {
		slices.Reverse(out.Messages)
	}
	return out, nil
}

// senderCache holds the directory handle and the user key on record for each sender name already looked up.
type senderCache struct {
	kv     jetstream.KeyValue
	failed bool
	users  map[string]string // directory key -> the user key on record ("" when none)
}

// inspectSender names a message's sender and its trust label the way the receiving endpoint does, with the directory
// asked by the signer's own name (one key read) instead of listing it. A signed name that is not a directory key, or
// whose record holds another user key, is unknown-signer.
func (a *Admin) inspectSender(ctx context.Context, cache *senderCache, raw *jetstream.RawStreamMsg) (string, string) {
	h, trust := sap.VerifyStored(raw.Header.Get(sap.HeaderName), raw.Subject, raw.Data)
	shown := config.Clean(h.Name)
	switch trust {
	case sap.TrustForged, sap.TrustMalformed, sap.TrustClaimed:
		if shown == "" {
			shown = "?"
		}
		return shown, string(trust)
	case sap.TrustAbsent:
		return "?", string(trust)
	}
	user, seen := cache.users[h.Name]
	if !seen {
		if h.Name != "" {
			if cache.kv == nil && !cache.failed {
				if kv, err := a.dir(ctx); err == nil {
					cache.kv = kv
				} else {
					cache.failed = true
				}
			}
			if cache.kv != nil {
				if e, err := cache.kv.Get(ctx, h.Name); err == nil {
					if r, err := store.ParseRecord(h.Name, e.Value()); err == nil {
						user = r.User
					}
				}
			}
		}
		cache.users[h.Name] = user
	}
	if h.Name == "" || user == "" || user != h.User {
		if shown == "" {
			shown = "?"
		}
		return shown, string(sap.TrustUnknownSigner)
	}
	if signed, err := time.Parse(time.RFC3339Nano, h.TS); err == nil {
		if age := raw.Time.Sub(signed); age > inspectMaxSignedAge || age < -sap.LiveSkew {
			return shown, string(sap.TrustStale)
		}
	}
	return shown, string(sap.TrustVerified)
}
