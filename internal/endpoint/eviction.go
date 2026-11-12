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
// does not allow or cannot answer the stream and consumer info reads: the line is advice and never an error.
func (e *Endpoint) InboxEvictionLine(ctx context.Context) (line string, ok bool) {
	ctx, cancel := context.WithTimeout(ctx, evictionInfoTimeout)
	defer cancel()
	stream := sap.InboxStream(e.key)
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
	return EvictionLine(store.Eviction(in))
}

// EvictionLine words the inbox line for ev; ok is false when ev needs none.
func EvictionLine(ev store.InboxEviction) (line string, ok bool) {
	const next = "Next: read mail sooner; an operator can run `mashctl audit` to see inboxes that fill (docs/operations.md)"
	switch {
	case ev.Unread > 0 && ev.NearFull():
		return fmt.Sprintf("inbox: WARN - %d unread message(s) were evicted (%d in all) and the inbox is %.0f%% full. %s", ev.Unread, ev.Dropped, ev.Fill, next), true
	case ev.Unread > 0:
		return fmt.Sprintf("inbox: WARN - %d unread message(s) were evicted (%d in all) because the inbox was full or its mail aged out. %s", ev.Unread, ev.Dropped, next), true
	case ev.NearFull():
		return fmt.Sprintf("inbox: WARN - %.0f%% full: the oldest mail is evicted at 100%%. %s", ev.Fill, next), true
	}
	return "", false
}
