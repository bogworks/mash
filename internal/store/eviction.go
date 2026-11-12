package store

// InboxNearFullPercent is the share of an inbox's byte cap at which mashctl audit and mash status call it near full.
const InboxNearFullPercent = 90

// InboxState is what the server shows of one inbox stream and its consumer, the inputs of Eviction.
type InboxState struct {
	FirstSeq, LastSeq uint64
	Bytes             uint64
	MaxBytes          int64 // the stream's byte cap; 0 or less when it has none

	HaveConsumer      bool   // the stream has the durable consumer, and it delivers from the start (policy all)
	DeliveredStream   uint64 // the highest stream sequence the consumer delivered
	DeliveredConsumer uint64 // the consumer sequence of its last delivery
}

// InboxEviction is how much mail an inbox has dropped and how full it is.
type InboxEviction struct {
	// Dropped is the number of messages whose sequence is below the stream's first: every message the stream took and
	// no longer holds. Deleting and purging are denied on an inbox, so what is gone was dropped by a limit: the byte cap
	// (the oldest mail goes first) or the age limit. A stream that is deprovisioned and provisioned again starts at 0.
	Dropped uint64
	// Unread is how many of those were never delivered to the endpoint: at least this many were lost unread. It is
	// exact unless a message was delivered twice (a redelivery counts as a delivery, so Unread can read low).
	Unread uint64
	// Fill is the stored bytes as a percentage of the byte cap; 0 when there is no cap.
	Fill float64
}

// Eviction derives an inbox's drops from stream state alone. The server keeps no counter, but it does keep the first
// sequence, and sequences are never reused. A consumer that delivers from the start numbers its deliveries from 1,
// and the ack floor stalls below a gap only until the next delivery jumps over it, so the ack floor cannot say what
// was lost; the delivered stream sequence against the delivered consumer sequence can: every stream sequence at or below
// the first delivered one that the consumer skipped was dropped before it got there, and every message below
// FirstSeq that is past the last delivery was dropped waiting.
func Eviction(s InboxState) InboxEviction {
	var e InboxEviction
	if s.FirstSeq > 0 {
		e.Dropped = s.FirstSeq - 1
	}
	if s.MaxBytes > 0 {
		e.Fill = float64(s.Bytes) * 100 / float64(s.MaxBytes)
	}
	if e.Dropped == 0 {
		return e
	}
	if !s.HaveConsumer {
		e.Unread = e.Dropped // nothing was ever delivered
		return e
	}
	if s.DeliveredStream > s.DeliveredConsumer {
		e.Unread = s.DeliveredStream - s.DeliveredConsumer
	}
	if e.Dropped > s.DeliveredStream {
		e.Unread += e.Dropped - s.DeliveredStream
	}
	e.Unread = min(e.Unread, e.Dropped)
	return e
}

// NearFull reports whether the inbox is at or over InboxNearFullPercent of its byte cap.
func (e InboxEviction) NearFull() bool { return e.Fill >= InboxNearFullPercent }
