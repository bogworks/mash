package endpoint

// Delivery is the health of this endpoint's mail delivery to its session, as the status reply reports it (mash's own
// reply-only field `delivery`, an object `{"health": ..., "reason": ...}`; not part of program_status). It is a word and a class from fixed
// sets and nothing else: no subject, name, count or error text ever goes on the wire.
type Delivery struct {
	Health string // DeliveryOK, DeliveryDegraded or DeliveryHolding
	Reason string // one of the DeliveryReason* classes; empty with DeliveryOK
}

const (
	// DeliveryOK: normal delivery.
	DeliveryOK = "ok"
	// DeliveryDegraded: mail still reaches the session, but by a fallback (the spool failed and the channel is used).
	DeliveryDegraded = "degraded"
	// DeliveryHolding: mail is being held unacked in the inbox and is not reaching the session.
	DeliveryHolding = "holding"
)

const (
	// DeliveryReasonSpoolFailing: the spool cannot be opened or written.
	DeliveryReasonSpoolFailing = "spool_failing"
	// DeliveryReasonChannelHidden: mail cannot be shown by the channel (the host takes no channel delivery, or the spool
	// is required and failing, so the channel is not used).
	DeliveryReasonChannelHidden = "channel_hidden"
	// DeliveryReasonHeldUnacked: mail is being held unacked in the inbox until the session reads it by tool.
	DeliveryReasonHeldUnacked = "held_unacked"
)

var (
	deliveryHealths = map[string]bool{DeliveryOK: true, DeliveryDegraded: true, DeliveryHolding: true}
	deliveryReasons = map[string]bool{DeliveryReasonSpoolFailing: true, DeliveryReasonChannelHidden: true, DeliveryReasonHeldUnacked: true}
)

// KnownDelivery reports whether d is made only of the known words: the health, and for a health other than ok a known
// reason class (a reason with ok is not known).
func KnownDelivery(d Delivery) bool {
	if !deliveryHealths[d.Health] {
		return false
	}
	if d.Health == DeliveryOK {
		return d.Reason == ""
	}
	return d.Reason == "" || deliveryReasons[d.Reason]
}
