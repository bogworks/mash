package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
)

const inboxUsage = "usage: mashctl inbox inspect <address> [--json] [--legacy] [--from SEQ] [--to SEQ] [--limit N]\n" +
	"  address: an endpoint name, a flat key agent_owner_name, or agent.owner.name\n"

// exitInspectIncomplete is the exit code of an inspection that could not complete (no server, no config); 1 is a
// refusal (an unknown endpoint, no inbox stream) and 2 is usage, as for audit.
const exitInspectIncomplete = 3

// inspectStateText is how the text form words a state; the JSON form carries the state itself.
var inspectStateText = map[string]string{admin.StateAckUnsure: "delivered, ack not confirmed"}

type inboxInspectOpts struct {
	address  string
	json     bool
	legacy   bool
	from, to uint64
	limit    int
}

func parseSeqFlag(name, v string) (uint64, error) {
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("%s needs a stream sequence of 1 or more", name)
	}
	return n, nil
}

func parseInboxInspectArgs(args []string) (inboxInspectOpts, error) {
	var o inboxInspectOpts
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, inline, hasInline := strings.Cut(a, "=")
		switch name {
		case "--json", "--legacy":
			if hasInline {
				return o, fmt.Errorf("unexpected %s", config.RejectedArg(a))
			}
			if name == "--json" {
				o.json = true
			} else {
				o.legacy = true
			}
		case "--from", "--to", "--limit":
			var v string
			switch {
			case hasInline && inline != "":
				v = inline
			case !hasInline && i+1 < len(args) && args[i+1] != "":
				i++
				v = args[i]
			default:
				return o, fmt.Errorf("%s needs a value", name)
			}
			if name == "--limit" {
				n, err := strconv.Atoi(v)
				if err != nil || n < 1 {
					return o, fmt.Errorf("--limit needs a whole number of 1 or more")
				}
				o.limit = n
				break
			}
			n, err := parseSeqFlag(name, v)
			if err != nil {
				return o, err
			}
			if name == "--from" {
				o.from = n
			} else {
				o.to = n
			}
		default:
			if strings.HasPrefix(a, "-") || o.address != "" {
				return o, fmt.Errorf("unexpected %s", config.RejectedArg(a))
			}
			o.address = a
		}
	}
	if o.address == "" {
		return o, errors.New("an address is needed")
	}
	if o.from != 0 && o.to != 0 && o.from > o.to {
		return o, errors.New("--from is after --to")
	}
	return o, nil
}

// runInbox is `mashctl inbox`: only the sub-verb inspect exists.
func runInbox(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "inspect" {
		fmt.Fprint(stderr, "mashctl: inbox: the sub-verb is inspect\n"+inboxUsage)
		return 2
	}
	return runInboxInspect(args[1:], env, lookup, stdout, stderr)
}

type inspectStreamJSON struct {
	Name     string `json:"name"`
	FirstSeq uint64 `json:"first_seq"`
	LastSeq  uint64 `json:"last_seq"`
	Messages uint64 `json:"messages"`
	Bytes    uint64 `json:"bytes"`
}

type inspectCursorJSON struct {
	StreamSeq   uint64 `json:"stream_seq"`
	ConsumerSeq uint64 `json:"consumer_seq"`
}

type inspectConsumerJSON struct {
	Name           string            `json:"name"`
	AckFloor       inspectCursorJSON `json:"ack_floor"`
	Delivered      inspectCursorJSON `json:"delivered"`
	NumPending     uint64            `json:"num_pending"`
	NumRedelivered int               `json:"num_redelivered"`
	NumAckPending  int               `json:"num_ack_pending"`
}

type inspectMessageJSON struct {
	Seq           uint64  `json:"seq"`
	MsgID         string  `json:"msg_id"`
	Sender        string  `json:"sender"`
	Trust         string  `json:"trust"`
	StoredAt      string  `json:"stored_at"` // the stream's store time, RFC 3339 UTC
	Size          int     `json:"size"`      // payload bytes
	Kind          string  `json:"kind"`
	State         string  `json:"state"`
	DeliveryCount *int    `json:"delivery_count"` // always null: not kept by JetStream or mash
	AckTime       *string `json:"ack_time"`       // always null: not kept by JetStream or mash
}

// inspectReport is what `mashctl inbox inspect --json` prints.
type inspectReport struct {
	Schema   string               `json:"schema"`
	Address  string               `json:"address"`
	Stream   inspectStreamJSON    `json:"stream"`
	Consumer *inspectConsumerJSON `json:"consumer"`
	Messages []inspectMessageJSON `json:"messages"`
	Limits   []string             `json:"limits"`
}

func buildInspectReport(address string, in *admin.InboxInspection) inspectReport {
	r := inspectReport{Schema: admin.InspectSchema, Address: address,
		Stream:   inspectStreamJSON{Name: in.Stream.Name, FirstSeq: in.Stream.FirstSeq, LastSeq: in.Stream.LastSeq, Messages: in.Stream.Messages, Bytes: in.Stream.Bytes},
		Messages: []inspectMessageJSON{}, Limits: append([]string{}, admin.InspectLimits...)}
	if c := in.Consumer; c != nil {
		r.Consumer = &inspectConsumerJSON{Name: c.Name,
			AckFloor:   inspectCursorJSON{StreamSeq: c.AckFloor.Stream, ConsumerSeq: c.AckFloor.Consumer},
			Delivered:  inspectCursorJSON{StreamSeq: c.Delivered.Stream, ConsumerSeq: c.Delivered.Consumer},
			NumPending: c.NumPending, NumRedelivered: c.NumRedelivered, NumAckPending: c.NumAckPending}
	}
	for _, m := range in.Messages {
		r.Messages = append(r.Messages, inspectMessageJSON{Seq: m.Seq, MsgID: m.MsgID, Sender: m.Sender, Trust: m.Trust,
			StoredAt: m.StoredAt.UTC().Format(time.RFC3339), Size: m.Size, Kind: m.Kind, State: m.State})
	}
	return r
}

func writeInspectText(w io.Writer, r inspectReport) {
	s := r.Stream
	fmt.Fprintf(w, "inbox %s: stream %s, first_seq %d, last_seq %d, messages %d, bytes %d\n", config.Clean(r.Address), s.Name, s.FirstSeq, s.LastSeq, s.Messages, s.Bytes)
	if c := r.Consumer; c != nil {
		fmt.Fprintf(w, "consumer %s: ack_floor stream %d consumer %d, delivered stream %d consumer %d, num_pending %d, num_redelivered %d, num_ack_pending %d\n",
			c.Name, c.AckFloor.StreamSeq, c.AckFloor.ConsumerSeq, c.Delivered.StreamSeq, c.Delivered.ConsumerSeq, c.NumPending, c.NumRedelivered, c.NumAckPending)
	} else {
		fmt.Fprintln(w, "consumer: none (the deliver consumer does not exist, so every message is undelivered)")
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SEQ\tMSG-ID\tSENDER\tTRUST\tSTORED\tSIZE\tKIND\tSTATE")
	for _, m := range r.Messages {
		state := m.State
		if t, ok := inspectStateText[state]; ok {
			state = t
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n", m.Seq, dash(m.MsgID), m.Sender, m.Trust, m.StoredAt, m.Size, dash(m.Kind), state)
	}
	tw.Flush()
	if len(r.Messages) == 0 {
		fmt.Fprintln(w, "(no stored messages in that range)")
	}
	for _, l := range r.Limits {
		fmt.Fprintf(w, "note: %s\n", l)
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// runInboxInspect is `mashctl inbox inspect`: a read-only view of one endpoint's stored messages and their delivery
// state. It reads stream info, messages by sequence and the existing consumer's info, and writes nothing.
func runInboxInspect(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	o, err := parseInboxInspectArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl: inbox inspect: %v\n%s", err, inboxUsage)
		return 2
	}
	// like audit: it changes nothing and acts as no endpoint, so a set agent_token is no reason to refuse it
	cfg, err := loadConfig(env, lookup)
	if err != nil && !errors.Is(err, errAgentTokenSet) {
		fmt.Fprintln(stderr, err)
		return exitInspectIncomplete
	}
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitInspectIncomplete
	}
	defer closeAdmin()
	fail := func(err error) int {
		fmt.Fprintln(stderr, err)
		var ref *admin.RefusedError
		if errors.As(err, &ref) {
			return 1
		}
		return exitInspectIncomplete
	}
	t, err := adm.ResolveInbox(ctx, o.address, o.legacy)
	if err != nil {
		return fail(err)
	}
	in, err := adm.InspectInbox(ctx, t.Flat, admin.InspectOptions{From: o.from, To: o.to, Limit: o.limit})
	if err != nil {
		return fail(err)
	}
	rep := buildInspectReport(o.address, in)
	if o.json {
		b, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitInspectIncomplete
		}
		fmt.Fprintf(stdout, "%s\n", b)
		return 0
	}
	writeInspectText(stdout, rep)
	return 0
}
