// Package policyview is the responder behind `mash policy show` and `mash policy explain` (REQ-010).
//
// A sender sees only its own reach. The responder answers a signed request on agents.policy.<range>, finds who the
// caller is from the verified signature alone, and answers with that caller's own entry of the list set in force. It
// never returns another sender's lists or reach; the full lists are the operator's view (mashctl).
package policyview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"

	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/sap"
)

// Request ops.
const (
	OpShow    = "show"
	OpExplain = "explain"
)

// ErrSignatureRequired is the error text of every request that is not signed by a verified sender.
const ErrSignatureRequired = "signature required"

// NoListSetNotice is the notice of an answer when no list set is in force.
const NoListSetNotice = "no list set in force: owner line only"

// Store is the read side of the policy ledger.
type Store interface {
	// InForceView returns the record and the list set in force, read together so they are one generation; ok is
	// false when no list set is in force.
	InForceView() (rec listset.Record, ls listset.ListSet, ok bool, err error)
	// LatestStatus returns the latest applied-policy status; ok is false when none was written.
	LatestStatus(ctx context.Context) (policyapply.Status, bool, error)
}

// Options configures Mount.
type Options struct {
	// Range is the account's range. Required.
	Range string
	// Store is the ledger to read. Required.
	Store Store
	// Now gives the time requests are judged by; nil means time.Now.
	Now func() time.Time
	// Nonces is the replay set of signed requests; nil means a fresh in-memory set.
	Nonces sap.NonceSet
	// ResolveAddress maps a verified user public key to the endpoint's three-part address. ok is false when the key
	// names no endpoint. Required.
	ResolveAddress func(ctx context.Context, userPub string) (address string, ok bool, err error)
}

// Subject is the subject of the responder of range rng.
func Subject(rng string) string { return listset.PolicySubject(rng) }

// Request is the body of a request.
type Request struct {
	Op       string `json:"op"`
	Receiver string `json:"receiver,omitempty"`
}

// ErrorReply is the body of a refusal.
type ErrorReply struct {
	Error string `json:"error"`
}

// Latest is the latest attempt to apply a generation, reported when it is not the generation in force.
type Latest struct {
	Generation uint64 `json:"generation"`
	State      string `json:"state"`
}

// ShowReply is the answer of show: the caller's own entry and the status header. Every slice is non-nil.
// Generation is the generation in force, whose reach this is; Latest is the latest attempt when it differs.
type ShowReply struct {
	Describes  string  `json:"describes"`
	Generation uint64  `json:"generation"`
	State      string  `json:"state"`
	InForce    bool    `json:"in_force"`
	Latest     *Latest `json:"latest,omitempty"`
	// Notice is empty when the list set is in force and applied; otherwise one plain sentence.
	Notice         string               `json:"notice,omitempty"`
	Sender         string               `json:"sender"`
	Reach          []listset.VerbReach  `json:"reach"`
	OpenSubjects   listset.OpenSubjects `json:"open_subjects"`
	RawClientLimit string               `json:"raw_client_limit"`
}

// ExplainReply is the answer of explain: the explanation and the status header.
type ExplainReply struct {
	Describes      string                    `json:"describes"`
	Generation     uint64                    `json:"generation"`
	State          string                    `json:"state"`
	InForce        bool                      `json:"in_force"`
	Latest         *Latest                   `json:"latest,omitempty"`
	Notice         string                    `json:"notice,omitempty"`
	Sender         string                    `json:"sender"`
	Receiver       string                    `json:"receiver"`
	Verbs          []listset.VerbExplanation `json:"verbs"`
	OpenSubjects   listset.OpenSubjects      `json:"open_subjects"`
	RawClientLimit string                    `json:"raw_client_limit"`
}

type responder struct {
	opts Options

	mu       sync.Mutex
	cacheKey string
	compiled *listset.Compiled
	views    map[uint64]listset.View
}

// Mount serves the responder on nc: a micro service with one endpoint on Subject(opts.Range), in a queue group so
// several responders share the load. stop drains the service.
func Mount(nc *nats.Conn, opts Options) (stop func() error, err error) {
	if opts.Range == "" {
		return nil, errors.New("policyview: no range")
	}
	if opts.Store == nil {
		return nil, errors.New("policyview: no store")
	}
	if opts.ResolveAddress == nil {
		return nil, errors.New("policyview: no address resolver")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Nonces == nil {
		opts.Nonces = sap.NewNonceSet(4 * sap.LiveSkew)
	}
	r := &responder{opts: opts}
	svc, err := micro.AddService(nc, micro.Config{
		Name:    "mash-policy",
		Version: "0.1.0",
	})
	if err != nil {
		return nil, fmt.Errorf("policyview: %w", err)
	}
	if err := svc.AddEndpoint("policy", micro.HandlerFunc(r.handle),
		micro.WithEndpointSubject(Subject(opts.Range)), micro.WithEndpointQueueGroup("mash-policy")); err != nil {
		_ = svc.Stop()
		return nil, fmt.Errorf("policyview: %w", err)
	}
	return svc.Stop, nil
}

func reply(req micro.Request, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte(`{"error":"internal error"}`)
	}
	_ = req.Respond(b)
}

func refuse(req micro.Request, msg string) { reply(req, ErrorReply{Error: msg}) }

func (r *responder) handle(req micro.Request) {
	now := r.opts.Now()
	h, trust := sap.VerifyLive(req.Headers().Get(sap.HeaderName), req.Subject(), req.Data(), now, r.opts.Nonces)
	if trust != sap.TrustVerified {
		refuse(req, ErrSignatureRequired)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	caller, ok, err := r.opts.ResolveAddress(ctx, h.User)
	if err != nil {
		refuse(req, "cannot resolve the sender: "+err.Error())
		return
	}
	if !ok || caller == "" {
		refuse(req, "the signing key names no endpoint")
		return
	}
	if parts := strings.Split(caller, "."); len(parts) != 3 || parts[0] != r.opts.Range {
		refuse(req, fmt.Sprintf("the sender is not an endpoint of range %q", r.opts.Range))
		return
	}
	var q Request
	if err := json.Unmarshal(req.Data(), &q); err != nil {
		refuse(req, "the request is not JSON")
		return
	}
	switch q.Op {
	case OpShow:
		v, err := r.show(ctx, caller)
		if err != nil {
			refuse(req, err.Error())
			return
		}
		reply(req, v)
	case OpExplain:
		v, err := r.explain(ctx, caller, q.Receiver)
		if err != nil {
			refuse(req, err.Error())
			return
		}
		reply(req, v)
	default:
		refuse(req, fmt.Sprintf("unknown op %q: want show or explain", q.Op))
	}
}

// header is the status part of every answer. generation is the one in force; latest is set when the latest attempt
// is another generation.
type header struct {
	generation uint64
	state      string
	inForce    bool
	notice     string
	latest     *Latest
}

// header labels the in-force record rec with the latest status. The reach is always that of rec's generation: a
// later attempt that was refused or is partial is reported beside it, never in its place.
func (r *responder) header(ctx context.Context, rec listset.Record) (header, error) {
	st, ok, err := r.opts.Store.LatestStatus(ctx)
	if err != nil {
		return header{}, fmt.Errorf("cannot read the policy status: %w", err)
	}
	if !ok {
		return header{generation: rec.Generation, state: "unknown", inForce: true, notice: "no status is recorded for the list set in force"}, nil
	}
	if st.Generation != rec.Generation {
		// The status of the generation in force is not the latest; it was applied when it was committed.
		return header{
			generation: rec.Generation, state: string(policyapply.StateApplied), inForce: true,
			latest: &Latest{Generation: st.Generation, State: string(st.State)},
			notice: fmt.Sprintf("the latest attempt, generation %d, is %s; the reach shown is generation %d's, which is in force", st.Generation, st.State, rec.Generation),
		}, nil
	}
	h := header{generation: rec.Generation, state: string(st.State), inForce: true}
	switch st.State {
	case policyapply.StateApplied:
	case policyapply.StatePartial:
		h.notice = fmt.Sprintf("generation %d is partial: not every server holds it, so what is shown may not hold everywhere", st.Generation)
	default:
		h.notice = fmt.Sprintf("generation %d is %s", st.Generation, st.State)
	}
	return h, nil
}

// snapshot compiles the list set in force (cached by its digest) and shows it.
func (r *responder) snapshot(ls listset.ListSet, generation uint64) (*listset.Compiled, listset.View, error) {
	key := listset.DigestListSet(ls).String()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cacheKey != key || r.compiled == nil {
		c, err := listset.Compile(ls)
		if err != nil {
			return nil, listset.View{}, fmt.Errorf("the list set in force does not compile: %w", err)
		}
		r.cacheKey, r.compiled, r.views = key, c, map[uint64]listset.View{}
	}
	if v, ok := r.views[generation]; ok {
		return r.compiled, v, nil
	}
	v, err := listset.Show(ls, r.compiled, generation)
	if err != nil {
		return nil, listset.View{}, err
	}
	r.views[generation] = v
	return r.compiled, v, nil
}

func emptyOpen() listset.OpenSubjects {
	return listset.OpenSubjects{Publish: []string{}, Subscribe: []string{}}
}

func (r *responder) show(ctx context.Context, caller string) (ShowReply, error) {
	rec, ls, inForce, err := r.opts.Store.InForceView()
	if err != nil {
		return ShowReply{}, fmt.Errorf("cannot read the list set in force: %w", err)
	}
	out := ShowReply{
		Describes:      listset.DescribesListSet,
		Sender:         caller,
		Reach:          []listset.VerbReach{},
		OpenSubjects:   emptyOpen(),
		RawClientLimit: listset.RawClientLimit,
	}
	if !inForce {
		out.State = "none"
		out.Notice = NoListSetNotice
		return out, nil
	}
	h, err := r.header(ctx, rec)
	if err != nil {
		return ShowReply{}, err
	}
	out.Generation, out.State, out.InForce, out.Notice, out.Latest = h.generation, h.state, h.inForce, h.notice, h.latest
	_, view, err := r.snapshot(ls, h.generation)
	if err != nil {
		return ShowReply{}, err
	}
	for _, s := range view.Senders {
		if s.Sender == caller {
			out.Reach, out.OpenSubjects = s.Reach, s.OpenSubjects
			return out, nil
		}
	}
	if out.Notice == "" {
		out.Notice = "the list set in force has no entry for this sender"
	}
	return out, nil
}

func (r *responder) explain(ctx context.Context, caller, receiver string) (ExplainReply, error) {
	rec, ls, inForce, err := r.opts.Store.InForceView()
	if err != nil {
		return ExplainReply{}, fmt.Errorf("cannot read the list set in force: %w", err)
	}
	if !inForce {
		return ExplainReply{}, errors.New(NoListSetNotice)
	}
	h, err := r.header(ctx, rec)
	if err != nil {
		return ExplainReply{}, err
	}
	c, _, err := r.snapshot(ls, h.generation)
	if err != nil {
		return ExplainReply{}, err
	}
	e, err := listset.Explain(ls, c, caller, receiver)
	if err != nil {
		return ExplainReply{}, err
	}
	return ExplainReply{
		Describes: e.Describes, Generation: h.generation, State: h.state, InForce: h.inForce, Latest: h.latest, Notice: h.notice,
		Sender: e.Sender, Receiver: e.Receiver, Verbs: e.Verbs, OpenSubjects: e.OpenSubjects, RawClientLimit: e.RawClientLimit,
	}, nil
}
