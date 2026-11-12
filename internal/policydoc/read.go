package policydoc

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
)

// Reader is what the read path needs of the ledger. *policyapply.KVLedger implements it.
type Reader interface {
	// Account is the account public key the ledger is for.
	Account() string
	// InForceDocument returns the exact bytes of the list-set document in force, nil when none is stored.
	InForceDocument(ctx context.Context) ([]byte, error)
	// LatestStatus returns the latest status, nil when none has been written.
	LatestStatus(ctx context.Context) (*policyapply.Status, error)
}

var _ Reader = (*policyapply.KVLedger)(nil)

// NoPolicyNotice is the notice when no account-wide policy is in force.
const NoPolicyNotice = "no account-wide policy in force"

// StateHeader is what the latest status says about the list set, shown above every answer. It is plain data with
// stable JSON: snake_case, slices never null and sorted.
type StateHeader struct {
	// State is the latest status's state ("" when no status has been written).
	State string `json:"state"`
	// InForce is the status's in_force: true only when the generation was applied on every server.
	InForce bool `json:"in_force"`
	// StatusGeneration is the generation the status is about.
	StatusGeneration uint64 `json:"status_generation"`
	// Servers is the generation each server holds, sorted by server.
	Servers []policyapply.ServerGeneration `json:"servers"`
	// Partial is true when the servers hold different generations (an unknown generation counts as different).
	Partial bool `json:"partial"`
	// Notice is a plain sentence when the policy is not fully in force; empty otherwise.
	Notice string `json:"notice"`
}

// ShowResult is the answer of Show.
type ShowResult struct {
	// ListSetInForce is false when no list set is in force; then View is nil.
	ListSetInForce bool          `json:"list_set_in_force"`
	Header         StateHeader   `json:"header"`
	View           *listset.View `json:"view"`
}

// ExplainResult is the answer of Explain.
type ExplainResult struct {
	ListSetInForce bool                 `json:"list_set_in_force"`
	Header         StateHeader          `json:"header"`
	Explanation    *listset.Explanation `json:"explanation"`
}

func header(st *policyapply.Status) StateHeader {
	h := StateHeader{Servers: []policyapply.ServerGeneration{}}
	if st == nil {
		return h
	}
	h.State, h.InForce, h.StatusGeneration = string(st.State), st.InForce, st.Generation
	h.Servers = append(h.Servers, st.Servers...)
	sort.SliceStable(h.Servers, func(i, j int) bool { return h.Servers[i].Server < h.Servers[j].Server })
	seen := map[string]bool{}
	for _, s := range h.Servers {
		seen[genWord(s.Generation)] = true
	}
	h.Partial = len(seen) > 1
	switch {
	case h.Partial:
		parts := make([]string, 0, len(h.Servers))
		for _, s := range h.Servers {
			parts = append(parts, fmt.Sprintf("%s: %s", s.Server, genWord(s.Generation)))
		}
		h.Notice = fmt.Sprintf("partial application: servers hold different generations (%s); %s", strings.Join(parts, ", "), NoPolicyNotice)
	case !st.InForce:
		h.Notice = fmt.Sprintf("generation %d is %s; %s", st.Generation, st.State, NoPolicyNotice)
	}
	return h
}

func genWord(g *uint64) string {
	if g == nil {
		return "unknown"
	}
	return fmt.Sprintf("%d", *g)
}

// load reads the in-force document and the status. The document is decoded without a submitter: it was verified when
// it was submitted. doc is nil when nothing is in force.
func load(ctx context.Context, r Reader) (*Document, StateHeader, error) {
	raw, err := r.InForceDocument(ctx)
	if err != nil {
		return nil, StateHeader{}, err
	}
	st, err := r.LatestStatus(ctx)
	if err != nil {
		return nil, StateHeader{}, err
	}
	h := header(st)
	if len(raw) == 0 {
		return nil, h, nil
	}
	d, err := Decode(raw, DecodeOptions{})
	if err != nil {
		return nil, h, fmt.Errorf("the document in force is unreadable: %w", err)
	}
	if d.Account != r.Account() {
		return nil, h, fmt.Errorf("the document in force is for account %s, not %s", d.Account, r.Account())
	}
	return &d, h, nil
}

// Show describes the list set in force (REQ-010) with the state of its application (REQ-011). Nothing in force is
// an answer, not an error.
func Show(ctx context.Context, r Reader) (ShowResult, error) {
	d, h, err := load(ctx, r)
	if err != nil || d == nil {
		return ShowResult{Header: h}, err
	}
	c, err := listset.Compile(d.ListSet)
	if err != nil {
		return ShowResult{}, err
	}
	v, err := listset.Show(d.ListSet, c, d.Generation)
	if err != nil {
		return ShowResult{}, err
	}
	return ShowResult{ListSetInForce: true, Header: h, View: &v}, nil
}

// Explain says whether sender may reach receiver, per verb, under the list set in force.
func Explain(ctx context.Context, r Reader, sender, receiver string) (ExplainResult, error) {
	d, h, err := load(ctx, r)
	if err != nil || d == nil {
		return ExplainResult{Header: h}, err
	}
	c, err := listset.Compile(d.ListSet)
	if err != nil {
		return ExplainResult{}, err
	}
	e, err := listset.Explain(d.ListSet, c, sender, receiver)
	if err != nil {
		return ExplainResult{}, err
	}
	return ExplainResult{ListSetInForce: true, Header: h, Explanation: &e}, nil
}

// StageBase is the stage of the refusal Diff reports for a document whose base_generation is not the generation in
// force.
const StageBase = "base"

// Diff decodes file (raw JSON or a JWS, whose signature is not checked) and returns what applying it would do. A
// document for another account is an error. A document whose base_generation is not the generation in force is
// reported in the plan as a refusal with stage "base": a submission of it would be refused as stale.
func Diff(ctx context.Context, r Reader, file []byte, servers []listset.ServerLimits) (listset.Plan, error) {
	next, err := Decode(file, DecodeOptions{})
	if err != nil {
		return listset.Plan{}, err
	}
	if next.Account != r.Account() {
		return listset.Plan{}, fmt.Errorf("the document is for account %s, not %s", next.Account, r.Account())
	}
	cur, _, err := load(ctx, r)
	if err != nil {
		return listset.Plan{}, err
	}
	var inForce *listset.ListSet
	var gen uint64
	if cur != nil {
		inForce, gen = &cur.ListSet, cur.Generation
	}
	p := listset.DryRun(inForce, next.ListSet, servers)
	if next.BaseGeneration != gen {
		p.Refusals = append([]listset.Refusal{{Stage: StageBase, Entry: "base_generation",
			Reason: fmt.Sprintf("the document is based on generation %d and generation %d is in force: a submission would be refused as stale", next.BaseGeneration, gen)}}, p.Refusals...)
		p.Refused = true
		p.Receivers, p.Templates, p.Tags = []listset.ReceiverChange{}, []listset.TemplateChange{}, []listset.TagChange{}
		p.RotateAndRevoke, p.NewCredential, p.Revoke, p.FirstCredential = []string{}, []string{}, []string{}, []string{}
	}
	return p, nil
}
