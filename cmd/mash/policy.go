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
const noResponderText = "open: owner line only, no list set in force"

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
		return localOpen(stdout, asJSON, ep.Display())
	}
	// Only a list-set credential may publish to the policy subject; any other has no grant there, and a publish
	// would be a permissions violation, not "no responders". Decide from the client's own credential.
	if !listSetCreds(cfg.Creds) {
		return localOpen(stdout, asJSON, ep.Display())
	}
	subject := policyview.Subject(ident.Agent)
	payload, err := json.Marshal(req)
	if err != nil {
		return fail(err)
	}
	h, err := ep.Identity().Sign(subject, payload, ep.Key(), time.Now(), "")
	if err != nil {
		return fail(err)
	}
	m := nats.NewMsg(subject)
	m.Data = payload
	m.Header.Set(sap.HeaderName, h.Encode())
	rctx, rcancel := context.WithTimeout(ctx, cfg.CallTimeout)
	defer rcancel()
	// A permissions violation on this subject (and only this one) means no list set reaches this account: treat it
	// as no responders. The handler replaces the endpoint's, which only logs.
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
	if wasDenied || errors.Is(err, nats.ErrNoResponders) {
		return localOpen(stdout, asJSON, ep.Display())
	}
	if err != nil {
		return fail(fmt.Errorf("mash policy: no answer from the policy responder: %w", err))
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

// localOpen prints the answer of an account with no responder: no lists, so the owner line only.
func localOpen(stdout io.Writer, asJSON bool, sender string) int {
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
	if r.Notice != "" {
		fmt.Fprintf(&b, "note: %s\n", r.Notice)
	}
	for _, v := range r.Verbs {
		word := "refused"
		if v.Allowed {
			word = "allowed"
		}
		fmt.Fprintf(&b, "  %s: %s (%s): %s\n", v.Verb, word, v.Reason, v.Detail)
	}
	fmt.Fprintf(&b, "open subjects, publish: %s\n", joinOr(r.OpenSubjects.Publish, "none"))
	fmt.Fprintf(&b, "open subjects, subscribe: %s\n", joinOr(r.OpenSubjects.Subscribe, "none"))
	fmt.Fprintf(&b, "%s\n", r.RawClientLimit)
	return b.String()
}
