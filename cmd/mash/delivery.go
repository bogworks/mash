package main

import (
	"sync/atomic"

	"github.com/bogworks/mash/internal/endpoint"
)

// deliveryState is what serve knows about how mail reaches the session, from the code in serve that binds the spool.
type deliveryState struct {
	// BindFailed: the spool could not be opened or bound at start (endpoint.SpoolDir or SetSpool failed).
	BindFailed bool
	// FailClosed: MASH_SPOOL_REQUIRED. The channel is never used; what the spool cannot take is held unacked.
	FailClosed bool
	// FaultActive: under FailClosed, the spool is failing now (a start failure not yet recovered by retrySpool, or a
	// write failure noted at run time). Cleared when the spool binds or works again.
	FaultActive bool
	// WriteFault and WriteHeld: a spool write failed at run time in any mode (endpoint.SpoolWriteFault) and no write
	// has worked since. WriteHeld: the message is held unacked rather than left to the channel (no-channel host,
	// spool-from-start before a hook, a wake not landing, or fail-closed).
	WriteFault, WriteHeld bool
	// NoChannel: the host takes no channel delivery (adapter.SpoolsFromStart).
	NoChannel bool
	// FromStartHeld: MASH_SPOOL_FROM_START on a host with a channel, spool failed: mail is held unacked (#16).
	FromStartHeld bool
}

// deliveryHealth maps serve's spool and channel state to what the status reply reports. Only the word and the class
// leave the process, never an error string.
//
//	ok        the spool is bound and nothing is failing.
//	holding   mail is held unacked in the inbox and does not reach the session until it reads by tool:
//	          - FailClosed and the spool failing        -> channel_hidden (the channel is not used; spool required)
//	          - NoChannel host with no spool            -> channel_hidden (the host never shows channel events)
//	          - FromStartHeld (spool failed, held #16)  -> held_unacked
//	runtime write failure (spool bound, a write failed, none has worked since):
//	          - held unacked on a NoChannel host        -> holding/channel_hidden
//	          - held unacked with a channel host        -> holding/held_unacked (spool-from-start before a hook)
//	          - the message falls back to the channel   -> degraded/spool_failing
//	degraded  the spool failed but the channel still delivers (the "mail stays on the channel" path of serve)
//	          -> spool_failing: mail arrives, by the fallback, with the early-mail guarantee of the spool lost.
func deliveryHealth(s deliveryState) endpoint.Delivery {
	switch {
	case s.FailClosed && s.FaultActive:
		return endpoint.Delivery{Health: endpoint.DeliveryHolding, Reason: endpoint.DeliveryReasonChannelHidden}
	case s.WriteFault && s.WriteHeld && s.NoChannel:
		return endpoint.Delivery{Health: endpoint.DeliveryHolding, Reason: endpoint.DeliveryReasonChannelHidden}
	case s.WriteFault && s.WriteHeld:
		return endpoint.Delivery{Health: endpoint.DeliveryHolding, Reason: endpoint.DeliveryReasonHeldUnacked}
	case s.WriteFault:
		return endpoint.Delivery{Health: endpoint.DeliveryDegraded, Reason: endpoint.DeliveryReasonSpoolFailing}
	case s.BindFailed && !s.FailClosed && s.NoChannel:
		return endpoint.Delivery{Health: endpoint.DeliveryHolding, Reason: endpoint.DeliveryReasonChannelHidden}
	case s.BindFailed && s.FromStartHeld:
		return endpoint.Delivery{Health: endpoint.DeliveryHolding, Reason: endpoint.DeliveryReasonHeldUnacked}
	case s.BindFailed && !s.FailClosed:
		return endpoint.Delivery{Health: endpoint.DeliveryDegraded, Reason: endpoint.DeliveryReasonSpoolFailing}
	}
	return endpoint.Delivery{Health: endpoint.DeliveryOK}
}

// deliveryExtra is the status-extras part for delivery: the one reply-only key `delivery`, {"health", "reason"}, with
// reason only when health is not ok, or nothing when the reader is unset, reports absent, or gives anything but the
// known words.
func deliveryExtra(read func() (endpoint.Delivery, bool)) map[string]any {
	if read == nil {
		return nil
	}
	d, ok := read()
	if !ok || !endpoint.KnownDelivery(d) {
		return nil
	}
	m := map[string]string{"health": d.Health}
	if d.Health != endpoint.DeliveryOK && d.Reason != "" {
		m["reason"] = d.Reason
	}
	return map[string]any{"delivery": m}
}

// statusExtras is the one status-extras provider serve registers (endpoint.SetStatusExtras): program_detail and
// delivery, both reply only. delivery reads the reader the pointer holds when asked, because serve knows its spool and
// channel state only after it binds the spool.
func statusExtras(detail func() map[string]any, delivery *atomic.Pointer[func() (endpoint.Delivery, bool)]) func() map[string]any {
	return func() map[string]any {
		out := map[string]any{}
		if detail != nil {
			for k, v := range detail() {
				out[k] = v
			}
		}
		if p := delivery.Load(); p != nil {
			for k, v := range deliveryExtra(*p) {
				out[k] = v
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
}
