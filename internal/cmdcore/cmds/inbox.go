package cmds

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore"
	"github.com/bogworks/mash/internal/cmdcore/render"
)

// InboxLimitDefault and InboxLimitMax are the default and the most of --limit and the tool's limit, the same in both
// doors.
const (
	InboxLimitDefault = 20
	InboxLimitMax     = 100
)

// MailItem is one message as the service reads it. Relay and ReplyTo are empty and zero when the message carries none.
type MailItem struct {
	Number  int
	From    string // the sender's full address, owner.group.name
	Sent    time.Time
	New     bool // the session has not been told of it
	Urgent  bool
	Kind    string
	Trust   string
	Size    int // bytes of the body
	ReplyTo int
	Relay   string
	Body    string
}

// InboxQuery is what mash inbox asks the service for. Limit is 1 to InboxLimitMax. After is the number to start after
// (0 = from the start). OnlyNew keeps the messages the session has not been told of.
type InboxQuery struct {
	OnlyNew bool
	Limit   int
	After   int
}

// InboxView is what the service reads for mash inbox: the messages the query selects (oldest first), and the counts
// of the whole inbox, which the query does not change.
type InboxView struct {
	Endpoint string // the address of the endpoint whose inbox it is
	Total    int    // every message the inbox holds
	Waiting  int    // the messages the session has not been told of, from the server's consumer
	// NewUrgent is how many of the waiting messages are urgent: counted over all of them, whatever the query selects.
	NewUrgent int
	// OldestWaiting is when the oldest waiting message was stored; zero when none is waiting.
	OldestWaiting time.Time
	Messages      []MailItem
	WakeCount     int
	WakeAge       time.Duration
	WakeAgeText   string // wake_age as it was written: "1h", "0"
}

// ReadView is what the service reads for mash read: the endpoint, and the message, nil when the inbox has no such one.
type ReadView struct {
	Endpoint string
	Message  *MailItem
}

// Mailbox is the service Inbox and Read need from Env.Svc.
type Mailbox interface {
	Inbox(ctx context.Context, q InboxQuery) (InboxView, error)
	Message(ctx context.Context, number int) (ReadView, error)
}

func mailbox(env *cmdcore.Env, verb string) (Mailbox, error) {
	if env == nil {
		return nil, cliframe.Failf("internal: mash %s has no environment to run in", verb)
	}
	svc, ok := env.Svc.(Mailbox)
	if !ok || svc == nil {
		return nil, cliframe.Failf("internal: mash %s has no inbox to read", verb)
	}
	return svc, nil
}

func now(env *cmdcore.Env) time.Time {
	if env != nil && env.Now != nil {
		return env.Now()
	}
	return time.Now()
}

// InboxParams are the arguments of mash inbox and of the MCP tool inbox.
type InboxParams struct {
	New   bool `json:"new,omitempty" flag:"--new" desc:"Only new messages: those that have not yet reached a session."`
	Limit int  `json:"limit,omitempty" flag:"--limit" value:"n" min:"1" max:"100" desc:"How many to show, newest last. Default 20, at most 100."`
	After int  `json:"after,omitempty" flag:"--after" value:"number" min:"0" desc:"Only messages after this number."`
	Wide  bool `json:"wide,omitempty" flag:"--wide" desc:"More columns: the full sender address, trust and size. May pass 80 columns."`
}

// InboxMessage is one message of the listing, always with every field, whatever --wide or the tool's wide says (that
// changes only the text). From is the full address; Preview is the first line of the text cut to fit an 80-column table.
type InboxMessage struct {
	Number  int    `json:"number"`
	From    string `json:"from"`
	Sent    string `json:"sent"` // RFC3339, UTC
	State   string `json:"state"`
	Urgent  bool   `json:"urgent"`
	Kind    string `json:"kind"`
	Trust   string `json:"trust"`
	Preview string `json:"preview"`
}

// InboxResult is the listing and the counts. The JSON form is always this object. wake_count and wake_age are the
// settings as they are (0 = off); oldest_waiting is how long the oldest waiting message has waited, null when none is.
type InboxResult struct {
	Endpoint      string         `json:"endpoint"`
	Total         int            `json:"total"`
	New           int            `json:"new"`
	Urgent        int            `json:"urgent"`
	WakeCount     int            `json:"wake_count"`
	OverWakeCount bool           `json:"over_wake_count"`
	WakeAge       string         `json:"wake_age"`
	OldestWaiting *string        `json:"oldest_waiting"`
	OverWakeAge   bool           `json:"over_wake_age"`
	Messages      []InboxMessage `json:"messages"`

	// what the text needs and the object does not carry
	wide  bool
	rows  []inboxRow
	count int // wake_count, for the line that names it
	age   string
}

type inboxRow struct {
	number, from, when, state, urgent, kind, trust, size, line string
}

// previewCols is the width the preview is cut for: a table of 80 columns whose lines stop at 79, so that no line fills
// the last column of the terminal.
const previewCols = 79

// inboxPrefixWidth is the display width of the columns before MESSAGE of the table without --wide, for the rows.
func inboxPrefixWidth(endpoint string, rows []inboxRow) int {
	w := [6]int{len("NUMBER"), len("FROM"), len("WHEN"), len("STATE"), len("URGENT"), len("KIND")}
	for _, r := range rows {
		for i, c := range []string{r.number, shortFrom(endpoint, r.from), r.when, r.state, r.urgent, r.kind} {
			if l := render.DisplayWidth(c); l > w[i] {
				w[i] = l
			}
		}
	}
	n := 0
	for _, x := range w {
		n += x + 2
	}
	return n
}

// shortFrom is the sender as the table shows it: the name alone when it is in the caller's own owner and group, else
// the full address.
func shortFrom(endpoint, from string) string {
	own := strings.Split(endpoint, ".")
	got := strings.Split(from, ".")
	if len(own) == 3 && len(got) == 3 && own[0] == got[0] && own[1] == got[1] {
		return got[2]
	}
	return from
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// Text is the summary line, the wake lines when they apply, a blank line and the table; or "No messages for ADDRESS."
// when the inbox holds none. A listing with no message in it (--new with none new, --after past the last) has the
// summary and no table.
func (r InboxResult) Text(o cmdcore.Opts) string {
	if r.Total == 0 && len(r.rows) == 0 {
		return "No messages for " + r.Endpoint + "."
	}
	var b strings.Builder
	b.WriteString(render.Plural(r.Total, "message") + " for " + r.Endpoint + ", " + strconv.Itoa(r.New) + " new, " + strconv.Itoa(r.Urgent) + " urgent.")
	if r.OverWakeCount {
		b.WriteString("\nMore than " + strconv.Itoa(r.count) + " are waiting.")
	}
	if r.OverWakeAge && r.OldestWaiting != nil {
		b.WriteString("\nThe oldest has waited " + *r.OldestWaiting + ".")
	}
	if len(r.rows) == 0 {
		return b.String()
	}
	cols := []string{"number", "from", "when", "state", "urgent", "kind"}
	if r.wide {
		cols = append(cols, "trust", "size")
	}
	cols = append(cols, "message")
	rows := make([][]string, len(r.rows))
	for i, x := range r.rows {
		from := shortFrom(r.Endpoint, x.from)
		if r.wide {
			from = x.from
		}
		row := []string{x.number, from, x.when, x.state, x.urgent, x.kind}
		if r.wide {
			row = append(row, x.trust, x.size)
		}
		rows[i] = append(row, x.line)
	}
	width := o.Width - 1
	if o.Width <= 0 {
		width = previewCols
	}
	if r.wide {
		width = 0 // wide may pass the width
	}
	b.WriteString("\n\n" + render.Table(cols, rows, width, true))
	return b.String()
}

// Inbox is the inbox command.
var Inbox = cmdcore.Command[InboxParams, InboxResult]{
	Name:        "inbox",
	Group:       "Everyday",
	Summary:     "List the messages this endpoint has received.",
	Description: "List the messages this endpoint has received.",
	Examples:    []string{"mash inbox", "mash inbox --new"},
	Run: func(ctx context.Context, env *cmdcore.Env, p InboxParams) (InboxResult, error) {
		limit := p.Limit
		if limit == 0 {
			limit = InboxLimitDefault
		}
		if limit < 1 || limit > InboxLimitMax { // the options check this; a caller that built the params itself gets it too
			return InboxResult{}, cliframe.Usagef("--limit must be a whole number from 1 to %d", InboxLimitMax).WithHint("mash inbox --help")
		}
		svc, err := mailbox(env, "inbox")
		if err != nil {
			return InboxResult{}, err
		}
		v, err := svc.Inbox(ctx, InboxQuery{OnlyNew: p.New, Limit: limit, After: p.After})
		if err != nil {
			return InboxResult{}, err
		}
		at := now(env)
		r := InboxResult{Endpoint: v.Endpoint, Total: v.Total, New: v.Waiting, Urgent: v.NewUrgent, WakeCount: v.WakeCount, WakeAge: v.WakeAgeText,
			Messages: []InboxMessage{}, wide: p.Wide, count: v.WakeCount}
		if r.WakeAge == "" {
			r.WakeAge = "0"
		}
		r.OverWakeCount = v.WakeCount > 0 && v.Waiting > v.WakeCount
		if v.Waiting > 0 && !v.OldestWaiting.IsZero() {
			age := at.Sub(v.OldestWaiting)
			s := render.Age(age)
			r.OldestWaiting = &s
			r.OverWakeAge = v.WakeAge > 0 && age > v.WakeAge
		}
		for _, m := range v.Messages {
			state := "read"
			if m.New {
				state = "new"
			}
			r.rows = append(r.rows, inboxRow{number: strconv.Itoa(m.Number), from: m.From, when: render.RelTime(at, m.Sent), state: state,
				urgent: yesNo(m.Urgent), kind: m.Kind, trust: m.Trust, size: strconv.Itoa(m.Size) + " B", line: firstLine(m.Body)})
		}
		cut := previewCols - inboxPrefixWidth(r.Endpoint, r.rows)
		for i, m := range v.Messages {
			x := r.rows[i]
			r.Messages = append(r.Messages, InboxMessage{Number: m.Number, From: m.From, Sent: m.Sent.UTC().Format(time.RFC3339), State: x.state,
				Urgent: m.Urgent, Kind: m.Kind, Trust: m.Trust, Preview: render.Cut(x.line, cut)})
		}
		return r, nil
	},
}

// firstLine is the first line of body that is not blank, trimmed; the service hands the body over already safe to print.
func firstLine(body string) string {
	for _, l := range strings.Split(body, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// ReadParams are the arguments of mash read and of the MCP tool read.
type ReadParams struct {
	Number int `json:"number" arg:"number" required:"true" min:"1" desc:"The message's number, from mash inbox."`
}

// ReadResult is one message in full. reply_to and relay are null when the message carries none; relay is the origin and
// number the sender declared, as "acme.demo.bob 12".
type ReadResult struct {
	Number  int     `json:"number"`
	From    string  `json:"from"`
	Sent    string  `json:"sent"` // RFC3339, UTC
	Trust   string  `json:"trust"`
	Kind    string  `json:"kind"`
	Urgent  bool    `json:"urgent"`
	ReplyTo *int    `json:"reply_to"`
	Relay   *string `json:"relay"`
	Body    string  `json:"body"`

	when string
}

// Text is the labelled lines (number, from, sent, trust, kind, urgent, and reply to and relayed from when the message
// carries them), a blank line and the whole text with "> " before every line (an empty line is "> "). The body is
// already escaped; the object (--json, the tool's structured result) carries it unquoted.
func (r ReadResult) Text(cmdcore.Opts) string {
	lines := []string{
		"number:   " + strconv.Itoa(r.Number),
		"from:     " + r.From,
		"sent:     " + r.when,
		"trust:    " + r.Trust,
		"kind:     " + r.Kind,
		"urgent:   " + yesNo(r.Urgent),
	}
	if r.ReplyTo != nil {
		lines = append(lines, "reply to: "+strconv.Itoa(*r.ReplyTo))
	}
	if r.Relay != nil {
		lines = append(lines, "relay:    "+*r.Relay)
	}
	body := strings.Split(strings.TrimRight(r.Body, "\n"), "\n")
	for i, l := range body {
		body[i] = "> " + l
	}
	return strings.Join(lines, "\n") + "\n\n" + strings.Join(body, "\n")
}

// Read is the read command.
var Read = cmdcore.Command[ReadParams, ReadResult]{
	Name:        "read",
	Group:       "Everyday",
	Summary:     "Show one message in full.",
	Description: "Show one message in full.",
	Examples:    []string{"mash read 2"},
	Run: func(ctx context.Context, env *cmdcore.Env, p ReadParams) (ReadResult, error) {
		if p.Number < 1 {
			return ReadResult{}, cliframe.Usagef("number must be a whole number of 1 or more").WithHint("mash read --help")
		}
		svc, err := mailbox(env, "read")
		if err != nil {
			return ReadResult{}, err
		}
		v, err := svc.Message(ctx, p.Number)
		if err != nil {
			return ReadResult{}, err
		}
		if v.Message == nil {
			return ReadResult{}, cliframe.Failf("%s has no message %d", v.Endpoint, p.Number).WithHint("mash inbox")
		}
		m := v.Message
		r := ReadResult{Number: m.Number, From: m.From, Sent: m.Sent.UTC().Format(time.RFC3339), Trust: m.Trust, Kind: m.Kind,
			Urgent: m.Urgent, Body: m.Body, when: render.RelTime(now(env), m.Sent)}
		if m.ReplyTo > 0 {
			n := m.ReplyTo
			r.ReplyTo = &n
		}
		if m.Relay != "" {
			s := m.Relay
			r.Relay = &s
		}
		return r, nil
	},
}
