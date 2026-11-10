package endpoint

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/spool"
)

// MsgRef identifies one message of an endpoint's mail: the stream and the sequence in it. Two streams of one endpoint
// (the legacy inbox stream and a new one, while an endpoint reads both) number from 1 each, so the bare sequence is
// not an identity. It is the same type the spool keys by.
//
// What the model sees: the endpoint's own (primary) stream, Stream == "", is shown and typed as a bare number, #5.
// A message of the legacy stream is shown and typed with an L, #L5 (display is naming plan 1.4b; until then nothing
// produces it).
type MsgRef = spool.Ref

// LegacyStream is the Stream of a message in the legacy inbox stream, which an endpoint reads during the transition
// to a new identity.
const LegacyStream = "legacy"

// legacyPrefix is the letter that qualifies a legacy-stream number in text: #L5.
const legacyPrefix = "L"

// ErrNoLegacyStream is what a reference to a legacy-stream message resolves to on an endpoint with no such stream.
var ErrNoLegacyStream = errors.New("no legacy stream on this endpoint")

// refOf is the identity of a delivered message.
func refOf(m render.Mail) MsgRef { return MsgRef{Stream: m.Stream, Seq: m.Seq} }

// FormatMsgRef is the text of a message number: "5" for the primary stream, "L5" for the legacy stream. A ref of any
// other stream has no short form and is written "<stream>:<seq>", for a log line.
func FormatMsgRef(r MsgRef) string {
	n := strconv.FormatUint(r.Seq, 10)
	switch r.Stream {
	case "":
		return n
	case LegacyStream:
		return legacyPrefix + n
	}
	return r.Stream + ":" + n
}

// ParseMsgRef reads a message number as the model types it: "5" or "#5" (the primary stream), "L5" or "#L5" (the
// legacy stream). It does not know which streams an endpoint has: Endpoint.ResolveRef does.
func ParseMsgRef(s string) (MsgRef, error) {
	t := strings.TrimPrefix(strings.TrimSpace(s), "#")
	stream := ""
	if rest, ok := strings.CutPrefix(t, legacyPrefix); ok {
		stream, t = LegacyStream, rest
	}
	if t == "" || strings.Trim(t, "0123456789") != "" {
		return MsgRef{}, fmt.Errorf("mash: %q is not a message number (a number like 5, or L5 for a legacy message)", s)
	}
	seq, err := strconv.ParseUint(t, 10, 64)
	if err != nil || seq == 0 {
		return MsgRef{}, fmt.Errorf("mash: %q is not a message number (a number like 5, or L5 for a legacy message)", s)
	}
	return MsgRef{Stream: stream, Seq: seq}, nil
}

// hasLegacyStream reports whether this endpoint reads a legacy stream beside its own. None does yet (naming plan 1.4b
// adds it).
func (e *Endpoint) hasLegacyStream() bool { return false }

// ResolveRef is ParseMsgRef for this endpoint: a legacy-stream number is an error unless the endpoint has that stream.
func (e *Endpoint) ResolveRef(s string) (MsgRef, error) {
	r, err := ParseMsgRef(s)
	if err != nil {
		return MsgRef{}, err
	}
	if r.Stream == LegacyStream && !e.hasLegacyStream() {
		return MsgRef{}, fmt.Errorf("mash: %s: %w", s, ErrNoLegacyStream)
	}
	return r, nil
}
