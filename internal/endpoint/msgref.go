package endpoint

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/spool"
)

// MsgRef identifies one message of an endpoint's mail: the stream and the sequence in it. An endpoint reads one inbox
// stream, so Stream is always "" and the message is shown and typed as a bare number, #5. It is the same type the spool
// keys by.
type MsgRef = spool.Ref

// refOf is the identity of a delivered message.
func refOf(m render.Mail) MsgRef { return MsgRef{Stream: m.Stream, Seq: m.Seq} }

// FormatMsgRef is the text of a message number: "5". A ref of any other stream has no short form and is written
// "<stream>:<seq>", for a log line.
func FormatMsgRef(r MsgRef) string { return render.Num(r.Stream, r.Seq) }

// ParseMsgRef reads a message number as the model types it: "5" or "#5".
func ParseMsgRef(s string) (MsgRef, error) {
	t := strings.TrimPrefix(strings.TrimSpace(s), "#")
	if t == "" || strings.Trim(t, "0123456789") != "" {
		return MsgRef{}, fmt.Errorf("mash: %q is not a message number (a number like 5)", s)
	}
	seq, err := strconv.ParseUint(t, 10, 64)
	if err != nil || seq == 0 {
		return MsgRef{}, fmt.Errorf("mash: %q is not a message number (a number like 5)", s)
	}
	return MsgRef{Seq: seq}, nil
}

// ResolveRef is ParseMsgRef for this endpoint.
func (e *Endpoint) ResolveRef(s string) (MsgRef, error) { return ParseMsgRef(s) }
