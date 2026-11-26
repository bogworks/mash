package policydoc

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
)

// Reader is what the read path needs of the ledger: the list set in force and the latest status, read together from one
// revision of the ledger value, without taking the lease or writing anything. policyapply.KVLedger implements the read;
// Account is the account the ledger is for (the ledger's key is derived from it).
type Reader interface {
	// Account is the account public key the ledger is for.
	Account() string
	// InForceViewAndStatus returns the record and the list set in force, the pending record and the latest status, all
	// from one revision; ok is false when no list set is in force, hasPending when a generation is pending and
	// statusOK when no status has been written. A committed record with no stored list set is an error.
	InForceViewAndStatus(ctx context.Context) (rec listset.Record, ls listset.ListSet, ok bool, pending listset.Record, hasPending bool, st policyapply.Status, statusOK bool, err error)
}

// NoPolicyNotice is the notice when no account-wide policy is in force.
const NoPolicyNotice = "no account-wide policy in force"

// LatestAttempt is the latest status: the last attempt to apply a generation, which may be about a generation other
// than the one in force.
type LatestAttempt struct {
	// Generation is the generation the attempt is about; 0 when it is unknown (a refusal before the generation was read).
	Generation uint64 `json:"generation"`
	// State is the status's state.
	State string `json:"state"`
	// Applied is the status's in_force: true only when the generation was applied on every server.
	Applied bool `json:"applied"`
}

// StateHeader is what the ledger says about the list set, shown above every answer. It is plain data with stable
// JSON: snake_case, slices never null and sorted.
type StateHeader struct {
	// InForceGeneration is the generation of the committed list set in force; 0 when none is committed.
	InForceGeneration uint64 `json:"in_force_generation"`
	// Latest is the latest attempt; nil when no status has been written.
	Latest *LatestAttempt `json:"latest"`
	// Servers is the generation each server holds in the latest status, sorted by server.
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

// header builds the header from the committed in-force generation (0 when none) and the latest status. What is in
// force comes from the committed record; the servers and the partial notice come from the status.
func header(inForceGen uint64, st *policyapply.Status) StateHeader {
	h := StateHeader{InForceGeneration: inForceGen, Servers: []policyapply.ServerGeneration{}}
	if st != nil {
		h.Latest = &LatestAttempt{Generation: st.Generation, State: string(st.State), Applied: st.InForce}
		h.Servers = append(h.Servers, st.Servers...)
		sort.SliceStable(h.Servers, func(i, j int) bool { return h.Servers[i].Server < h.Servers[j].Server })
		seen := map[string]bool{}
		for _, s := range h.Servers {
			seen[genWord(s.Generation)] = true
		}
		h.Partial = len(seen) > 1
	}
	switch {
	case h.Partial && inForceGen != 0:
		parts := make([]string, 0, len(h.Servers))
		for _, s := range h.Servers {
			parts = append(parts, fmt.Sprintf("%s: %s", s.Server, genWord(s.Generation)))
		}
		h.Notice = fmt.Sprintf("partial application: servers hold different generations (%s)", strings.Join(parts, ", "))
	case h.Partial:
		parts := make([]string, 0, len(h.Servers))
		for _, s := range h.Servers {
			parts = append(parts, fmt.Sprintf("%s: %s", s.Server, genWord(s.Generation)))
		}
		h.Notice = fmt.Sprintf("partial application: servers hold different generations (%s); %s", strings.Join(parts, ", "), NoPolicyNotice)
	case inForceGen == 0 && st != nil && st.Generation != 0:
		h.Notice = fmt.Sprintf("generation %d is %s; %s", st.Generation, st.State, NoPolicyNotice)
	case inForceGen == 0 && st != nil:
		h.Notice = fmt.Sprintf("the latest attempt is %s; %s", st.State, NoPolicyNotice)
	case inForceGen == 0:
		h.Notice = NoPolicyNotice
	}
	return h
}

func genWord(g *uint64) string {
	if g == nil {
		return "unknown"
	}
	return fmt.Sprintf("%d", *g)
}

// loaded is the list set in force with the record it was committed as.
type loaded struct {
	ls  listset.ListSet
	gen uint64
	// committed is false when no generation is committed; ls and gen are then empty.
	committed bool
	// pending is the pending record; hasPending is false when no generation is pending.
	pending    listset.Record
	hasPending bool
	// rec is the committed record (its Signed digest is what the unchanged check compares with).
	rec listset.Record
}

// load reads the list set in force, the pending record and the latest status in one read.
func load(ctx context.Context, r Reader) (*loaded, StateHeader, error) {
	rec, ls, ok, pending, hasPending, st, haveStatus, err := r.InForceViewAndStatus(ctx)
	if err != nil {
		return nil, StateHeader{}, fmt.Errorf("the list set in force is unreadable: %w", err)
	}
	var sp *policyapply.Status
	if haveStatus {
		sp = &st
	}
	if !ok || rec.Generation < 1 {
		return &loaded{pending: pending, hasPending: hasPending}, header(0, sp), nil
	}
	return &loaded{ls: ls, gen: rec.Generation, rec: rec, committed: true, pending: pending, hasPending: hasPending}, header(rec.Generation, sp), nil
}

// Show describes the list set in force (REQ-010) with the state of its application (REQ-011). Nothing in force is
// an answer, not an error.
func Show(ctx context.Context, r Reader) (ShowResult, error) {
	cur, h, err := load(ctx, r)
	if err != nil || !cur.committed {
		return ShowResult{Header: h}, err
	}
	c, err := listset.Compile(cur.ls)
	if err != nil {
		return ShowResult{}, err
	}
	v, err := listset.Show(cur.ls, c, cur.gen)
	if err != nil {
		return ShowResult{}, err
	}
	return ShowResult{ListSetInForce: true, Header: h, View: &v}, nil
}

// Explain says whether sender may reach receiver, per verb, under the list set in force.
func Explain(ctx context.Context, r Reader, sender, receiver string) (ExplainResult, error) {
	cur, h, err := load(ctx, r)
	if err != nil || !cur.committed {
		return ExplainResult{Header: h}, err
	}
	c, err := listset.Compile(cur.ls)
	if err != nil {
		return ExplainResult{}, err
	}
	e, err := listset.Explain(cur.ls, c, sender, receiver)
	if err != nil {
		return ExplainResult{}, err
	}
	return ExplainResult{ListSetInForce: true, Header: h, Explanation: &e}, nil
}

// StageBase is the stage of the refusal Diff reports for a document whose base_generation is not the generation in
// force.
const StageBase = "base"

// StageOwner is the stage of the refusal Diff reports for a document whose owner is not the selected owner.
const StageOwner = "owner"

// StagePending is the stage of the refusal Diff reports for a document whose content is not that of the pending
// generation: until the pending generation is applied or rolled back, only the same list set can be submitted.
const StagePending = "pending"

// SizeLimitsDefault is the size_limits of a plan: sizes were checked against the default server limits.
const SizeLimitsDefault = "default"

// SizeLimitsLine is the text line that says the same.
const SizeLimitsLine = "size checked against the default server limits; the apply checks each server's own"

// DiffPlan is a plan with the limits its size check used. It marshals as the plan with one more field.
type DiffPlan struct {
	listset.Plan
	// SizeLimits says which limits the size check used: "default" (the apply checks each server's own).
	SizeLimits string `json:"size_limits"`
	// inboxUnchecked is true when the caller passed no FailClosedFunc, so the inbox and narrowing checks were not run.
	inboxUnchecked bool
}

// ErrOwnerNeeded is what Diff returns when no list set is in force and the caller gave no owner: a first document's
// owner cannot be checked without one.
var ErrOwnerNeeded = errors.New("--owner is needed to check a first list set's owner when nothing is in force")

// StageInbox is the stage of the refusal Diff reports for a change that adds an address whose inbox is missing or
// suspended; the apply refuses it before pushing anything.
const StageInbox = "inbox"

// StageNarrowing is the stage of the refusal Diff reports for a change that narrows an existing sender by tags, which
// the apply refuses before pushing anything (it needs a new credential for that sender).
const StageNarrowing = "narrowing"

// FailClosedFunc is the apply's own pre-push checks (policyserve.FailClosed, bound to the inbox source): the refusals
// the Applier would give for moving from prev (nil when nothing is in force) to target, with its stages and texts.
// policydoc cannot import policyserve, so the caller passes it in.
type FailClosedFunc func(ctx context.Context, prev, target *listset.Compiled) ([]listset.Refusal, error)

// InboxNotCheckedLine says the apply's inbox and narrowing checks were not run, when Diff got no FailClosedFunc.
const InboxNotCheckedLine = "inbox not checked: the apply's inbox and narrowing checks were not run, so it may still refuse"

// Diff decodes file (raw JSON) and returns what applying it would do. A document for another account is an error. A
// document for an owner other than owner (when owner is empty, the owner of the list set in force; with none in force
// it is an error, ErrOwnerNeeded) is reported in the plan as a refusal with stage "owner", one whose base_generation
// is not the generation in force as a refusal with stage "base", and one whose content is not that of the pending
// generation as a refusal with stage "pending": a submission of any would be refused. A document whose content is that
// in force is refused as the apply refuses it, with stage "unchanged".
func Diff(ctx context.Context, r Reader, file []byte, owner string, servers []listset.ServerLimits, failClosed FailClosedFunc) (DiffPlan, error) {
	next, err := Decode(file, DecodeOptions{})
	if err != nil {
		return DiffPlan{}, err
	}
	if next.Account != r.Account() {
		return DiffPlan{}, fmt.Errorf("the document is for account %s, not %s", next.Account, r.Account())
	}
	cur, _, err := load(ctx, r)
	if err != nil {
		return DiffPlan{}, err
	}
	var inForce *listset.ListSet
	var gen uint64
	if cur.committed {
		inForce, gen = &cur.ls, cur.gen
		if owner == "" {
			owner = cur.ls.Owner
		}
	} else if owner == "" {
		return DiffPlan{}, ErrOwnerNeeded
	}
	p := listset.DryRun(inForce, next.ListSet, servers)
	var pre []listset.Refusal
	if owner != "" && next.Owner != owner {
		pre = append(pre, listset.Refusal{Stage: StageOwner, Entry: "owner",
			Reason: fmt.Sprintf("the document's `owner` field is %s and the selected owner is %s", next.Owner, owner)})
	}
	if next.BaseGeneration != gen {
		pre = append(pre, listset.Refusal{Stage: StageBase, Entry: "base_generation",
			Reason: fmt.Sprintf("the document is based on generation %d and generation %d is in force: a submission would be refused as stale", next.BaseGeneration, gen)})
	}
	// The apply refuses other content while the generation after the one in force is pending (policyapply.Run); a
	// pending record left from an older generation is cleared by the apply and does not count.
	if cur.hasPending && cur.pending.Generation == gen+1 && cur.pending.Content != listset.DigestListSet(next.ListSet) {
		pre = append(pre, listset.Refusal{Stage: StagePending, Entry: "pending",
			Reason: fmt.Sprintf("generation %d is pending: only the same list set can be submitted until it is applied or rolled back", cur.pending.Generation)})
	}
	dp := DiffPlan{Plan: p, SizeLimits: SizeLimitsDefault}
	dp.inboxUnchecked = failClosed == nil
	// The apply refuses a list set whose compiled content is that in force (ADR 0119).
	if cur.committed {
		if _, _, err := listset.Accept(cur.rec, next.ListSet, file); errors.Is(err, listset.ErrUnchanged) {
			pre = append(pre, listset.Refusal{Stage: policyapply.StageUnchanged, Entry: "list_set",
				Reason: fmt.Sprintf("same content as generation %d in force; nothing to apply", gen)})
		}
	}
	// The apply refuses, before pushing, an added address with no usable inbox and a change that narrows a sender by tags.
	// A document that does not compile is refused by DryRun already.
	if failClosed != nil {
		if target, cerr := listset.Compile(next.ListSet); cerr == nil {
			var prev *listset.Compiled
			if inForce != nil {
				prev, cerr = listset.Compile(*inForce)
			}
			if cerr == nil {
				rs, err := failClosed(ctx, prev, target)
				if err != nil {
					rs = []listset.Refusal{{Stage: StageInbox, Entry: "inbox", Reason: err.Error()}}
				}
				pre = append(pre, rs...)
			}
		}
	}
	p = dp.Plan
	if len(pre) > 0 {
		p.Refusals = append(pre, p.Refusals...)
		p.Refused = true
		p.Receivers, p.Templates, p.Tags = []listset.ReceiverChange{}, []listset.TemplateChange{}, []listset.TagChange{}
		p.RotateAndRevoke, p.NewCredential, p.Revoke, p.FirstCredential = []string{}, []string{}, []string{}, []string{}
	}
	dp.Plan = p
	return dp, nil
}
