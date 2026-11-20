package admin

import (
	"context"
	"errors"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// EndpointState is what the server holds about one endpoint besides its directory record.
type EndpointState struct {
	// Online is whether a live session holds the endpoint's lease. The lease expires after store.LeaseTTL, so an
	// endpoint whose session ended is offline within that time.
	Online bool
	// Suspended is whether the endpoint's inbox is parked (ADR 0057).
	Suspended bool
	// Waiting is how many stored messages the deliver consumer has not yet delivered; with no consumer, every stored
	// message. It is read only when asked for.
	Waiting uint64
}

// EndpointState reads addr's state. It reads the inbox stream (and its deliver consumer when withWaiting) and the lease
// key, and writes nothing. An address with no inbox stream is not suspended and has nothing waiting.
func (a *Admin) EndpointState(ctx context.Context, addr sap.Address, withWaiting bool) (EndpointState, error) {
	var st EndpointState
	if err := addr.Validate(); err != nil {
		return st, fmt.Errorf("mash: endpoint state: %w", err)
	}
	name := addr.Flat()
	s, err := a.js.Stream(ctx, sap.InboxStream(name))
	switch {
	case errors.Is(err, jetstream.ErrStreamNotFound):
	case err != nil:
		return st, fmt.Errorf("mash: inbox stream of %s: %w", name, err)
	default:
		ss, err := suspendState(ctx, name, s)
		if err != nil {
			return st, err
		}
		st.Suspended = ss.Suspended
		if withWaiting {
			st.Waiting = ss.Msgs
			c, err := s.Consumer(ctx, store.Consumer)
			switch {
			case errors.Is(err, jetstream.ErrConsumerNotFound):
			case err != nil:
				return st, fmt.Errorf("mash: inbox consumer of %s: %w", name, err)
			default:
				ci, err := c.Info(ctx)
				if err != nil {
					return st, fmt.Errorf("mash: inbox consumer of %s: %w", name, err)
				}
				st.Waiting = ci.NumPending
			}
		}
	}
	lease, err := a.js.KeyValue(ctx, store.LeaseBucket)
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return st, nil
	case err != nil:
		return st, fmt.Errorf("mash: lease bucket: %w", err)
	}
	e, err := lease.Get(ctx, name)
	switch {
	case errors.Is(err, jetstream.ErrKeyNotFound):
	case err != nil:
		return st, fmt.Errorf("mash: lease %s: %w", name, err)
	default:
		st.Online = !isTombstone(e.Value())
	}
	return st, nil
}

// InboxMessages is how many messages addr's inbox stream holds; 0 when it has no inbox stream. It writes nothing.
func (a *Admin) InboxMessages(ctx context.Context, addr sap.Address) (uint64, error) {
	if err := addr.Validate(); err != nil {
		return 0, fmt.Errorf("mash: inbox messages: %w", err)
	}
	name := addr.Flat()
	s, err := a.js.Stream(ctx, sap.InboxStream(name))
	switch {
	case errors.Is(err, jetstream.ErrStreamNotFound):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("mash: inbox stream of %s: %w", name, err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		return 0, fmt.Errorf("mash: inbox stream of %s: %w", name, err)
	}
	return info.State.Msgs, nil
}
