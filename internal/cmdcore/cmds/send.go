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

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore"
)

// Sentinels a Sender returns, wrapped or bare, for the two failures Send words itself.
var (
	// ErrNoSuchEndpoint: the recipient address is not an endpoint.
	ErrNoSuchEndpoint = errors.New("no such endpoint")
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
	Address string `json:"address" arg:"address" required:"true" desc:"Who to send to, as owner.group.name."`
	Message string `json:"message" arg:"message,optional" required:"true" desc:"The text. Leave it out to read the text from --file or from standard input." mcpdesc:"The text."`
	Kind    string `json:"kind,omitempty" flag:"--kind" value:"kind" enum:"blocker,question,task,result,fyi" desc:"What sort of message this is. blocker, question and task wake an idle recipient; result and fyi wait for its next turn. Default: task. One exception: a reply (--reply-to) marked question whose text has no question mark and is at most 280 characters asks nothing, so it is sent as fyi and the output says so."`
	Urgent  bool   `json:"urgent,omitempty" flag:"--urgent" desc:"Deliver at once, into a running turn where the agent host allows it."`
	ReplyTo *int   `json:"reply_to,omitempty" flag:"--reply-to" value:"number" min:"1" desc:"The number of the message in your inbox that this answers. Signed, so the recipient can trust the link."`
	Relay   *int   `json:"relay,omitempty" flag:"--relay" value:"number" min:"1" desc:"The number of a message in your inbox that this passes on. Signed, so the recipient can trust the link."`
	ID      string `json:"id,omitempty" flag:"--id" value:"key" desc:"A key of your own. A second send with the same key to the same address within two minutes stores nothing new, so a retry cannot deliver twice."`
	File    string `json:"-" flag:"--file" value:"path" desc:"Read the message text from a file. Use - for standard input."`
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

// Text is "Mail sent." with the words Urgent and the kind put in front when they are not the defaults ("Urgent question
// mail sent."), "Mail sent as fyi: a reply marked question that asks nothing." when the kind was changed (with "Urgent"
// in front for an urgent one), or "Mail already sent; nothing new stored." for a repeated --id. With Verbose it adds the
// receipt's fields as aligned labelled lines.
func (r SendResult) Text(o cmdcore.Opts) string {
	var s string
	if r.Duplicate {
		s = "Mail already sent; nothing new stored."
	} else if r.Retagged {
		s = "Mail sent as fyi: a reply marked question that asks nothing."
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
		words = append(words, "mail sent.")
		s = strings.Join(words, " ")
		s = strings.ToUpper(s[:1]) + s[1:]
	}
	if !o.Verbose {
		return s
	}
	yn := map[bool]string{true: "yes", false: "no"}
	return s + fmt.Sprintf("\n%-9s%s\n%-9s%d\n%-9s%s\n%-9s%s", "to:", r.To, "number:", r.Number, "kind:", r.Kind, "urgent:", yn[r.Urgent])
}

// Send is the send command.
var Send = cmdcore.Command[SendParams, SendResult]{
	Name:           "send",
	Summary:        "Send a message to an endpoint.",
	Description:    "Send a message to an endpoint.",
	MCPDescription: "Send mail to another endpoint by its address, owner.group.name. Choose the kind for what you need from the recipient: blocker (you cannot continue without them), question (you need an answer) and task (you are asking them to do something; the default) wake an idle recipient; result (the outcome of something they asked for) and fyi (information, no answer needed) wait for their next turn. Use urgent only for what cannot wait: it interrupts. When you answer a message, set reply_to to its number so the recipient sees which one. When you pass a message on, set relay to its number. Do not send a reply that only acknowledges. A reply marked question that asks nothing is sent as fyi. Use id when you retry after a timeout, so the message is not delivered twice.",
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
		b, err = io.ReadAll(env.Stdin)
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
