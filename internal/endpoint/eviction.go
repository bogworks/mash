package endpoint

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// evictionInfoTimeout bounds the two server reads behind the inbox line of status: status must not wait on a slow
// server for a line that is only advice.
const evictionInfoTimeout = 2 * time.Second

// InboxEvictionLine is the inbox line of status: it is set when this endpoint's own inbox has dropped mail that was
// never delivered to it, or is near its byte cap. ok is false when there is nothing to say, and also when the server
// does not allow or cannot answer the stream and consumer info reads: the line is advice and never an error. The
// count is a lower bound (see store.InboxEviction), so no line does not prove that nothing was lost.
func (e *Endpoint) InboxEvictionLine(ctx context.Context) (line string, ok bool) {
	return e.evictionLine(ctx, "inbox", e.key)
}

// LegacyInboxEvictionLine is the same for the legacy inbox of an endpoint whose record carries an alias, the stream
// this endpoint also reads while it holds the legacy identity's lease. ok is false for an endpoint with no alias.
func (e *Endpoint) LegacyInboxEvictionLine(ctx context.Context) (line string, ok bool) {
	if !e.hasLegacyStream() {
		return "", false
	}
	return e.evictionLine(ctx, "legacy inbox", e.legacyKey)
}

func (e *Endpoint) evictionLine(ctx context.Context, label, key string) (line string, ok bool) {
	ctx, cancel := context.WithTimeout(ctx, evictionInfoTimeout)
	defer cancel()
	stream := sap.InboxStream(key)
	s, err := e.js.Stream(ctx, stream)
	if err != nil {
		return "", false
	}
	info := s.CachedInfo()
	in := store.InboxState{FirstSeq: info.State.FirstSeq, LastSeq: info.State.LastSeq, Bytes: info.State.Bytes, MaxBytes: info.Config.MaxBytes}
	if c, err := s.Consumer(ctx, store.Consumer); err == nil {
		ci, err := c.Info(ctx)
		if err != nil {
			return "", false
		}
		in.HaveConsumer = ci.Config.DeliverPolicy == jetstream.DeliverAllPolicy
		in.DeliveredStream, in.DeliveredConsumer = ci.Delivered.Stream, ci.Delivered.Consumer
	}
	return evictionLine(label, store.Eviction(in))
}

// EvictionLine words the inbox line for ev; ok is false when ev needs none. The unread count is a lower bound.
func EvictionLine(ev store.InboxEviction) (line string, ok bool) { return evictionLine("inbox", ev) }

func evictionLine(label string, ev store.InboxEviction) (line string, ok bool) {
	const next = "Next: read mail sooner; an operator can run `mashctl audit` to see inboxes that fill (docs/operations.md)"
	switch {
	case ev.Unread > 0 && ev.NearFull():
		return fmt.Sprintf("%s: WARN - at least %d unread message(s) lost to eviction (%d evicted in all) and the inbox is %.0f%% full. %s", label, ev.Unread, ev.Dropped, ev.Fill, next), true
	case ev.Unread > 0:
		return fmt.Sprintf("%s: WARN - at least %d unread message(s) lost to eviction (%d evicted in all) because the inbox was full or its mail aged out. %s", label, ev.Unread, ev.Dropped, next), true
	case ev.NearFull():
		return fmt.Sprintf("%s: WARN - %.0f%% full: the oldest mail is evicted at 100%%. %s", label, ev.Fill, next), true
	}
	return "", false
}
