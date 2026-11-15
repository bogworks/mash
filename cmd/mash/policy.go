package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyview"
	"github.com/bogworks/mash/internal/sap"
)

const policyUsage = "usage: mash policy show [--json] | mash policy explain RECEIVER [--json]"

// noResponderText is what an account with no list set answers locally: with no responder there are no lists.
const noResponderText = "this credential is not covered by a list set: it has the owner line only (a list set may be in force for the account's other senders; only a list-set credential can ask the policy responder)"

// policyCmd runs `mash policy show` and `mash policy explain`: it asks the range's policy responder what this
// endpoint may reach, as a signed request, and prints the answer.
func policyCmd(cfg config.Config, args []string, stdout, stderr io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(stderr, err); return 1 }
	usage := func(msg string) int {
		if msg != "" {
			fmt.Fprintf(stderr, "mash policy: %s\n", msg)
		}
		fmt.Fprintln(stderr, policyUsage)
		return 2
	}
	if len(args) == 0 {
		return usage("")
	}
	for _, a := range args[1:] {
		if a == "--help" || a == "-h" { // the usage, and the verb does not run
			fmt.Fprintln(stdout, policyUsage)
			return 0
		}
	}
	req := policyview.Request{Op: args[0]}
	asJSON := false
	var rest []string
	for _, a := range args[1:] {
		if a == "--json" {
			asJSON = true
		} else {
			rest = append(rest, a)
		}
	}
	switch args[0] {
	case policyview.OpShow:
		if len(rest) != 0 {
			return usage("unexpected " + config.RejectedArg(rest[0]))
		}
	case policyview.OpExplain:
		if len(rest) != 1 || strings.HasPrefix(rest[0], "-") {
			return usage("explain takes one RECEIVER")
		}
		req.Receiver = rest[0]
	default:
		return usage(fmt.Sprintf("unknown subcommand %q", args[0]))
	}
	if cfg.Inert() {
		return fail(errors.New("mash: set MASH_NAME to the endpoint you are acting as"))
	}

	ctx, cancel := context.WithTimeout(context.Background(), cliDeadline(cfg))
	defer cancel()
	openCtx, openCancel := context.WithTimeout(ctx, cfg.OpenTimeout)
	ep, err := endpoint.OpenClientWith(openCtx, cfg, endpoint.ClientOptions{})
	openCancel()
	if err != nil {
		return fail(err)
	}
	defer ep.Close()

	ident := ep.EndpointIdentity()
	if ident.IsLegacy() { // a legacy name has no range, so no list set can name it
		return localOpen(stdout, asJSON, ep.Display(), req)
	}
	// Only a list-set credential may publish to the policy subject; any other has no grant there, and a publish
	// would be a permissions violation, not "no responders". Decide from the client's own credential.
	if !isListSetCreds(cfg.Creds) {
		return localOpen(stdout, asJSON, ep.Display(), req)
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return fail(err)
	}
	// ADR 0129: the window serves both subjects. Ask the new one first; only a permissions violation on it (an old
	// template) or no responder on it (an old serve) falls back, once, to the old one. Any other failure of the first
	// attempt is reported as it is.
	subjects := policyview.Subjects(ident.Agent)
	var resp *nats.Msg
	for i, subject := range subjects {
		r, outcome, err := askPolicy(ctx, ep, cfg.CallTimeout, subject, payload, stderr)
		if err != nil {
			return fail(err)
		}
		if outcome == askAnswered {
			resp = r
			break
		}
		last := i == len(subjects)-1
		if !last {
			continue // denied or no responder on a subject with a fallback left
		}
		// This credential holds a list set, so silence is not "open": a stopped responder or an older template must
		// not make an applied policy look absent. The error is the last attempt's, naming the first subject tried.
		tried := ""
		if i > 0 {
			tried = " (after " + subjects[0] + ")"
		}
		if outcome == askDenied {
			return fail(errors.New("policy view refused: this credential may not ask the policy responder" + tried))
		}
		return fail(fmt.Errorf("policy view unavailable: no responder on %s%s", subject, tried))
	}
	var e policyview.ErrorReply
	if json.Unmarshal(resp.Data, &e) == nil && e.Error != "" {
		return fail(fmt.Errorf("mash policy: %s", e.Error))
	}
	if asJSON {
		fmt.Fprintln(stdout, strings.TrimSpace(string(resp.Data)))
		return 0
	}
	var text string
	if req.Op == policyview.OpShow {
		var r policyview.ShowReply
		if err := json.Unmarshal(resp.Data, &r); err != nil {
			return fail(fmt.Errorf("mash policy: unreadable answer: %w", err))
		}
		text = showText(r)
	} else {
		var r policyview.ExplainReply
		if err := json.Unmarshal(resp.Data, &r); err != nil {
			return fail(fmt.Errorf("mash policy: unreadable answer: %w", err))
		}
		text = explainText(r)
	}
	fmt.Fprint(stdout, text)
	return 0
}

// isListSetCreds is listSetCreds; a test seam.
var isListSetCreds = listSetCreds

// listSetCreds reports whether the user JWT in the creds file is a list-set credential: signed by a scoped signing
// key (an issuer account is set and the issuer is not that account) and carrying no permission list of its own,
// since the scope's template grants the subjects. An unreadable file is not one.
func listSetCreds(path string) bool {
	raw, err := config.ReadCredsFile(path)
	if err != nil {
		return false
	}
	tok, err := nkeys.ParseDecoratedJWT(raw)
	if err != nil {
		return false
	}
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil {
		return false
	}
	p := uc.Permissions
	noPerms := len(p.Pub.Allow) == 0 && len(p.Pub.Deny) == 0 && len(p.Sub.Allow) == 0 && len(p.Sub.Deny) == 0 && p.Resp == nil
	return uc.IssuerAccount != "" && uc.Issuer != uc.IssuerAccount && noPerms
}

// noListSetReason marks a verb whose answer is not knowable locally: no list set was consulted.
const noListSetReason listset.Reason = "no_list_set"

// localOpen prints the answer of an account with no list-set credential: no lists, so the owner line only. It
// answers in the shape of the op asked: show gets a ShowReply, explain an ExplainReply whose every verb says the
// answer needs a list set (what the owner line allows for that receiver is not knowable here).
func localOpen(stdout io.Writer, asJSON bool, sender string, req policyview.Request) int {
	if req.Op == policyview.OpExplain {
		r := policyview.ExplainReply{
			Describes: listset.DescribesListSet, State: "none", Notice: noResponderText, Sender: sender, Receiver: req.Receiver,
			Verbs:        []listset.VerbExplanation{},
			OpenSubjects: listset.OpenSubjects{Publish: []string{}, Subscribe: []string{}}, RawClientLimit: listset.RawClientLimit,
		}
		for _, v := range listset.Verbs {
			r.Verbs = append(r.Verbs, listset.VerbExplanation{Verb: v, Reason: noListSetReason, Detail: noResponderText})
		}
		if asJSON {
			b, _ := json.Marshal(r)
			fmt.Fprintln(stdout, string(b))
			return 0
		}
		fmt.Fprint(stdout, explainText(r))
		return 0
	}
	if asJSON {
		b, _ := json.Marshal(policyview.ShowReply{
			Describes: listset.DescribesListSet, State: "none", Notice: noResponderText, Sender: sender,
			Reach: []listset.VerbReach{}, OpenSubjects: listset.OpenSubjects{Publish: []string{}, Subscribe: []string{}},
			RawClientLimit: listset.RawClientLimit,
		})
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprintln(stdout, noResponderText)
	return 0
}

func policyStatusLine(generation uint64, state string, inForce bool) string {
	if state == "none" || state == "unknown" {
		return fmt.Sprintf("list set: state %s", state)
	}
	f := "not in force"
	if inForce {
		f = "in force"
	}
	return fmt.Sprintf("list set: generation %d, %s, %s", generation, state, f)
}

func latestLine(l *policyview.Latest) string {
	if l == nil {
		return ""
	}
	return fmt.Sprintf("latest attempt: generation %d, %s\n", l.Generation, l.State)
}

func joinOr(items []string, none string) string {
	if len(items) == 0 {
		return none
	}
	return strings.Join(items, ", ")
}

func showText(r policyview.ShowReply) string {
	var b strings.Builder
	fmt.Fprintf(&b, "sender: %s\n", r.Sender)
	if r.State == "none" {
		fmt.Fprintf(&b, "%s\n", r.Notice)
		return b.String()
	}
	fmt.Fprintf(&b, "%s\n", policyStatusLine(r.Generation, r.State, r.InForce))
	b.WriteString(latestLine(r.Latest))
	if r.Notice != "" {
		fmt.Fprintf(&b, "note: %s\n", r.Notice)
	}
	b.WriteString("reach:\n")
	for _, v := range r.Reach {
		fmt.Fprintf(&b, "  %s: %s\n", v.Verb, joinOr(v.Receivers, "none"))
	}
	fmt.Fprintf(&b, "open subjects, publish: %s\n", joinOr(r.OpenSubjects.Publish, "none"))
	fmt.Fprintf(&b, "open subjects, subscribe: %s\n", joinOr(r.OpenSubjects.Subscribe, "none"))
	fmt.Fprintf(&b, "%s\n", r.RawClientLimit)
	return b.String()
}

func explainText(r policyview.ExplainReply) string {
	var b strings.Builder
	fmt.Fprintf(&b, "sender: %s\nreceiver: %s\n", r.Sender, r.Receiver)
	fmt.Fprintf(&b, "%s\n", policyStatusLine(r.Generation, r.State, r.InForce))
	b.WriteString(latestLine(r.Latest))
	if r.Notice != "" {
		fmt.Fprintf(&b, "note: %s\n", r.Notice)
	}
	for _, v := range r.Verbs {
		word := "refused"
		if v.Allowed {
			word = "allowed"
		} else if v.Reason == noListSetReason {
			word = "unknown"
		}
		fmt.Fprintf(&b, "  %s: %s (%s): %s\n", v.Verb, word, v.Reason, v.Detail)
	}
	fmt.Fprintf(&b, "open subjects, publish: %s\n", joinOr(r.OpenSubjects.Publish, "none"))
	fmt.Fprintf(&b, "open subjects, subscribe: %s\n", joinOr(r.OpenSubjects.Subscribe, "none"))
	fmt.Fprintf(&b, "%s\n", r.RawClientLimit)
	return b.String()
}

type askOutcome int

const (
	askAnswered    askOutcome = iota
	askDenied                 // a permissions violation on this attempt's subject
	askNoResponder            // no responder on this attempt's subject
)

// askPolicy sends one signed request to subject and waits up to timeout. A permissions violation on this subject (and
// only this one) is askDenied and no responder is askNoResponder, neither an error: the caller decides whether to
// fall back. Any other failure is an error. The error handler it installs replaces the endpoint's, which only logs;
// it logs everything but this subject's violation to stderr, as before.
func askPolicy(ctx context.Context, ep *endpoint.Endpoint, timeout time.Duration, subject string, payload []byte, stderr io.Writer) (*nats.Msg, askOutcome, error) {
	h, err := ep.Identity().Sign(subject, payload, ep.Key(), time.Now(), "")
	if err != nil {
		return nil, 0, err
	}
	m := nats.NewMsg(subject)
	m.Data = payload
	m.Header.Set(sap.HeaderName, h.Encode())
	rctx, rcancel := context.WithTimeout(ctx, timeout)
	defer rcancel()
	var mu sync.Mutex
	denied := false
	ep.Conn().SetErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
		if err != nil && strings.Contains(err.Error(), "ermissions Violation for Publish to "+strconv.Quote(subject)) {
			mu.Lock()
			denied = true
			mu.Unlock()
			rcancel()
			return
		}
		fmt.Fprintf(stderr, "mash: nats: %v\n", err)
	})
	resp, err := ep.Conn().RequestMsgWithContext(rctx, m)
	mu.Lock()
	wasDenied := denied
	mu.Unlock()
	if wasDenied {
		return nil, askDenied, nil
	}
	if errors.Is(err, nats.ErrNoResponders) {
		return nil, askNoResponder, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("mash policy: no answer from the policy responder: %w", err)
	}
	return resp, askAnswered, nil
}
