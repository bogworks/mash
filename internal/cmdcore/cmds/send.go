// Package cmds holds the commands defined on cmdcore: each is a params struct, a result struct and a Run, served through
// both the command line and the MCP tool.
package cmds

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore"
)

// Sentinels a Sender returns, wrapped or bare, for the two failures Send words itself.
var (
	// ErrNoSuchEndpoint: the recipient address is not an endpoint.
	ErrNoSuchEndpoint = errors.New("no such endpoint")
	// ErrRecipientSuspended: the recipient is suspended, so its inbox takes no mail.
	ErrRecipientSuspended = errors.New("recipient suspended")
	// ErrNotSignedIn: the server refused the sender's credentials.
	ErrNotSignedIn = errors.New("not signed in")
)

// SignInError is an ErrNotSignedIn that names the endpoint that could not sign in (the sender).
type SignInError struct{ Addr string }

func (e *SignInError) Error() string { return e.Addr + " cannot sign in" }

// Is makes errors.Is(err, ErrNotSignedIn) true.
func (e *SignInError) Is(target error) bool { return target == ErrNotSignedIn }

// SendOptions are the optional parts of a send.
type SendOptions struct {
	ReplyTo, Relay int // message numbers, 1 or more; 0 is none
	Urgent         bool
	Kind, ID       string
}

// SendReceipt is what the service stored. To and Kind are set when the service changed what was asked: the address in
// its canonical dotted form, and the kind a short reply was tagged with. Retagged says the kind was changed to fyi
// because the reply asked nothing.
type SendReceipt struct {
	Number    int
	Duplicate bool
	Retagged  bool
	To, Kind  string
}

// Sender is the service Send needs from Env.Svc.
type Sender interface {
	Send(ctx context.Context, to, text string, o SendOptions) (SendReceipt, error)
}

// SendParams are the arguments of mash send and of the MCP tool send.
type SendParams struct {
	Address string `json:"address" arg:"address" required:"true" desc:"Who to send to, as owner.group.name." mcpdesc:"The recipient: owner.group.name, as the who tool lists it."`
	Message string `json:"message" arg:"message,optional" required:"true" desc:"The text. Leave it out to read the text from --file or from standard input." mcpdesc:"The text of the mail."`
	Kind    string `json:"kind,omitempty" flag:"--kind" value:"kind" enum:"blocker,question,task,result,fyi" desc:"What sort of message this is. blocker, question and task wake an idle recipient that has mash's wake channel on; otherwise, and for result and fyi, the message arrives with its next turn. Default: task. One exception: a reply (--reply-to) marked question whose text has no question mark and is at most 280 characters asks nothing, so it is sent as fyi and the output says so." mcpdesc:"What sort of message this is. blocker, question and task wake an idle recipient that has mash's wake channel on; otherwise, and for result and fyi, the message arrives with its next turn. Default: task. One exception: a reply (reply_to set) marked question whose text has no question mark and is at most 280 characters asks nothing, so it is sent as fyi and the output says so."`
	Urgent  bool   `json:"urgent,omitempty" flag:"--urgent" desc:"Deliver at once, into a running turn where the agent host allows it."`
	ReplyTo *int   `json:"reply_to,omitempty" flag:"--reply-to" value:"number" min:"1" desc:"The number of the message in your inbox that this answers. Signed, so the recipient can trust the link."`
	Relay   *int   `json:"relay,omitempty" flag:"--relay" value:"number" min:"1" desc:"The number of a message in your inbox that this passes on. Signed, so the recipient can trust the link."`
	ID      string `json:"id,omitempty" flag:"--id" value:"key" desc:"A key of your own. A second send with the same key to the same address within two minutes stores nothing new, so a retry cannot deliver twice."`
	File    string `json:"-" flag:"--file" value:"path" desc:"Read the message text from a file. Use - for standard input; if nothing arrives there within 15 minutes the command gives up."`
}

// SendResult is the receipt of a send.
type SendResult struct {
	To        string `json:"to"`
	Number    int    `json:"number"`
	Kind      string `json:"kind"`
	Urgent    bool   `json:"urgent"`
	Duplicate bool   `json:"duplicate"`
	// Retagged is true when a reply marked question asked nothing and was sent as fyi.
	Retagged bool `json:"retagged"`
}

// Text is "Mail sent (number 42 in acme.demo.alice's inbox)" with the words Urgent and the kind put in front when they are not the defaults ("Urgent
// question mail sent (number 43 in acme.demo.alice's inbox)"), "Mail sent as fyi (number 44): a reply marked question that asks nothing" when the
// kind was changed (with "Urgent" in front for an urgent one), or "Mail already sent; nothing new stored" for a repeated
// --id. The number is printed because the sender can get it nowhere else. With Detail it adds the to, kind and urgent
// lines, aligned and labelled.
func (r SendResult) Text(o cmdcore.Opts) string {
	var s string
	if r.Duplicate {
		s = "Mail already sent; nothing new stored"
	} else if r.Retagged {
		s = fmt.Sprintf("Mail sent as fyi (number %d): a reply marked question that asks nothing", r.Number)
		if r.Urgent {
			s = "Urgent m" + s[1:]
		}
	} else {
		var words []string
		if r.Urgent {
			words = append(words, "Urgent")
		}
		if r.Kind != "" && r.Kind != "task" {
			words = append(words, r.Kind)
		}
		number := fmt.Sprintf("number %d", r.Number)
		if r.To != "" {
			number += fmt.Sprintf(" in %s's inbox", r.To)
		}
		words = append(words, "mail sent ("+number+")")
		s = strings.Join(words, " ")
		s = strings.ToUpper(s[:1]) + s[1:]
	}
	if !o.Detail {
		return s
	}
	if !r.Duplicate && !r.Retagged {
		s = strings.TrimSuffix(s, ".") // #1046, for the --detail form only until the other lines follow
	}
	yn := map[bool]string{true: "yes", false: "no"}
	return s + "\n" + cliframe.Block(cliframe.Row{Label: "to", Value: r.To}, cliframe.Row{Label: "kind", Value: r.Kind},
		cliframe.Row{Label: "urgent", Value: yn[r.Urgent]})
}

// Send is the send command.
var Send = cmdcore.Command[SendParams, SendResult]{
	Name:           "send",
	Group:          "Everyday",
	Summary:        "Send a message to an endpoint.",
	Description:    "Send a message to an endpoint.",
	Globals:        cmdcore.GlobalsAll,
	MCPDescription: "Send mail to another endpoint. Two arguments are required: address (who to send to, owner.group.name) and message (the text). Choose the kind for what you need from the recipient: blocker (you cannot continue without them), question (you need an answer) and task (you are asking them to do something; the default) wake an idle recipient that has mash's wake channel on; otherwise, and for result (the outcome of something they asked for) and fyi (information, no answer needed), the message arrives with the recipient's next turn. Use urgent only for what cannot wait: it interrupts. When you answer a message, set reply_to to its number so the recipient sees which one. When you pass a message on, set relay to its number. Do not send a reply that only acknowledges. A reply marked question that asks nothing is sent as fyi. Use id when you retry after a timeout, so the message is not delivered twice.",
	Examples: []string{
		`mash send acme.demo.alice "lunch at noon?"`,
		`mash send acme.demo.alice --urgent --kind question "ready?"`,
		`mash send acme.demo.alice --reply-to 2 "yes"`,
		`git log -1 | mash send acme.demo.alice --kind fyi --file -`,
	},
	Run: runSend,
}

func usageNeedsText() error {
	return cliframe.Usagef("mash send needs the message text").WithHint("mash send --help")
}

func runSend(ctx context.Context, env *cmdcore.Env, p SendParams) (SendResult, error) {
	if env == nil {
		return SendResult{}, cliframe.Failf("internal: mash send has no environment to run in")
	}
	svc, ok := env.Svc.(Sender)
	if !ok || svc == nil {
		return SendResult{}, cliframe.Failf("internal: mash send has no service to send with")
	}
	to, err := cmdcore.ParseAddress(p.Address, "mash send --help")
	if err != nil {
		return SendResult{}, err
	}
	p.Address = to
	text, err := messageText(env, p)
	if err != nil {
		return SendResult{}, err
	}
	kind := p.Kind
	if kind == "" {
		kind = "task"
	}
	o := SendOptions{Urgent: p.Urgent, Kind: kind, ID: p.ID}
	if p.ReplyTo != nil {
		o.ReplyTo = *p.ReplyTo
	}
	if p.Relay != nil {
		o.Relay = *p.Relay
	}
	rc, err := svc.Send(ctx, p.Address, text, o)
	if err != nil {
		var se *SignInError
		switch {
		case errors.Is(err, ErrNoSuchEndpoint):
			return SendResult{}, cmdcore.NoSuchEndpoint(p.Address)
		case errors.Is(err, ErrRecipientSuspended):
			return SendResult{}, cmdcore.RecipientSuspended(p.Address)
		case errors.Is(err, ErrNotSignedIn):
			addr := "this endpoint"
			if errors.As(err, &se) {
				addr = se.Addr
			}
			return SendResult{}, cmdcore.NotSignedIn(addr)
		}
		return SendResult{}, err
	}
	if rc.To != "" {
		to = rc.To
	}
	if rc.Kind != "" {
		kind = rc.Kind
	}
	return SendResult{To: to, Number: rc.Number, Kind: kind, Urgent: p.Urgent, Duplicate: rc.Duplicate, Retagged: rc.Retagged}, nil
}

// messageText picks the text: the argument, or --file, or standard input when it is piped.
func messageText(env *cmdcore.Env, p SendParams) (string, error) {
	if p.Message == "-" && env.Stdin != nil { // a bare "-" is standard input, as it is for --file
		p.Message = ""
		if p.File == "" {
			p.File = "-"
		} else {
			p.Message = "-" // both given: refused below
		}
	}
	switch {
	case p.Message != "" && p.File != "":
		return "", cliframe.Usagef("mash send takes the message text or --file, not both").WithHint("mash send --help")
	case p.Message != "":
		if strings.TrimSpace(p.Message) == "" {
			return "", usageNeedsText()
		}
		return p.Message, nil
	case p.File == "" && !(env.Stdin != nil && env.StdinPiped):
		return "", usageNeedsText()
	}
	var b []byte
	var err error
	switch {
	case p.File == "" || p.File == "-":
		if env.Stdin == nil {
			return "", usageNeedsText()
		}
		b, err = readStdin(env)
		if errors.Is(err, errStdinTimeout) {
			// A failure, exit 1: the command was well formed and nothing arrived in time.
			return "", cliframe.Failf("no message text arrived on standard input")
		}
	case env.ReadFile != nil:
		b, err = env.ReadFile(p.File)
	default:
		b, err = os.ReadFile(p.File)
	}
	if err != nil {
		return "", cliframe.Failf("cannot read the message: %v", err)
	}
	text := string(b)
	if t, ok := strings.CutSuffix(text, "\n"); ok { // one trailing newline goes, as $(...) drops it; CRLF is one newline
		text = strings.TrimSuffix(t, "\r")
	}
	if strings.TrimSpace(text) == "" {
		return "", usageNeedsText()
	}
	return text, nil
}

// errStdinTimeout says the standard input gave no end of text within the limit the caller set (env.StdinTimeout).
var errStdinTimeout = errors.New("standard input timed out")

// readStdin reads all of env.Stdin, giving up after env.StdinTimeout (when set). The read runs in a goroutine: one
// blocked on a pipe nobody writes to is abandoned, and the process ends before it matters.
func readStdin(env *cmdcore.Env) ([]byte, error) {
	if env.StdinTimeout <= 0 {
		return io.ReadAll(env.Stdin)
	}
	type result struct {
		b   []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		b, err := io.ReadAll(env.Stdin)
		ch <- result{b, err}
	}()
	t := time.NewTimer(env.StdinTimeout)
	defer t.Stop()
	select {
	case r := <-ch:
		return r.b, r.err
	case <-t.C:
		return nil, errStdinTimeout
	}
}
