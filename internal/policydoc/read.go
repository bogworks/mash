package policydoc

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/store"
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

// StageRange is the stage of the refusal Diff reports for a document whose range is not the selected range.
const StageRange = "range"

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
	// NextSubmitter is the document's next_submitter (a planned submitter rotation, ADR 0112); empty when it has none.
	NextSubmitter string `json:"next_submitter"`
	// Rotation is what the document does to the submitter rotation: nil when nothing (and for a refused plan). A plan
	// with a Rotation is a change.
	Rotation *RotationAction `json:"rotation"`
	// rotation is the text of the rotation line, after "rotation: ".
	rotation string
	// unsigned is true when no --submitter was given, so the document's signature was not checked.
	unsigned bool
	// adoptionUnchecked is true when the caller passed no marker source, so the adoption was not checked.
	adoptionUnchecked bool
}

// Rotation actions of a RotationAction.
const (
	// RotationStarts: the document's next_submitter would be recorded as a planned rotation.
	RotationStarts = "starts"
	// RotationCompletes: the document, signed by the pending next submitter, would complete the pending rotation.
	RotationCompletes = "completes"
	// RotationPending: a rotation is pending and the document does not complete it, or (Unknown) whether it does
	// cannot be told.
	RotationPending = "pending"
)

// RotationAction is the part of a plan that concerns the submitter rotation (ADR 0112).
type RotationAction struct {
	// Action is "starts", "completes" or "pending".
	Action string `json:"action"`
	// NextSubmitter is the key the rotation is to: the document's next_submitter for "starts", the marker's pending
	// next submitter for "completes" and "pending".
	NextSubmitter string `json:"next_submitter"`
	// Unknown is true when the signer could not be told (not a JWS the marker's keys verify) and no --submitter was
	// given, so whether the document completes the pending rotation cannot be checked.
	Unknown bool `json:"unknown,omitempty"`
}

func (p DiffPlan) completes() bool {
	return p.Rotation != nil && p.Rotation.Action == RotationCompletes
}

// RotationContext is what the intake would check a document's next_submitter against: the adoption marker and the keys
// mash holds, read by the caller, and the key that signed the document when it is known (the --submitter).
type RotationContext struct {
	// Adopted is whether the account has an adoption marker; Marker is it.
	Adopted bool
	Marker  store.Marker
	// Held is the public keys mash holds.
	Held []string
	// Signer is the key that signed the document; "" when unknown.
	Signer string
	// Err is why the marker or the held keys could not be read; the plan is then refused at stage document.
	Err error
	// MarkerErr is why the adoption marker could not be read (Err is set too); with a Signer, the plan is then refused at
	// stage signature.
	MarkerErr error
}

// ErrRangeNeeded is what Diff returns when no list set is in force and the caller gave no range: a first document's
// range cannot be checked without one.
var ErrRangeNeeded = errors.New("--range is needed to check a first list set's range when nothing is in force")

// StageSignature is the stage of the refusal Diff reports for a signer the adoption marker does not accept.
const StageSignature = "signature"

// signerShortLen is how much of a public key a refusal shows, as the intake's.
const signerShortLen = 8

func shortKey(k string) string {
	if len(k) > signerShortLen {
		return k[:signerShortLen] + "..."
	}
	return k
}

// checkAdoption is the intake's first check: an account that is not adopted has every list set ignored, so no
// submission to it is accepted. A marker that cannot be read is a refusal too. A nil rc means the caller read no marker:
// nothing is checked and the plan says so.
func checkAdoption(rc *RotationContext, account string) []listset.Refusal {
	if rc == nil {
		return nil
	}
	refuse := func(reason string) []listset.Refusal {
		return []listset.Refusal{{Stage: StageAdoption, Entry: "adoption", Reason: reason}}
	}
	if rc.MarkerErr != nil {
		return refuse("the adoption marker cannot be read: " + rc.MarkerErr.Error())
	}
	if !rc.Adopted {
		return refuse("this account is not adopted: list sets are ignored")
	}
	// The marker is read over the admin connection, whose adoption bucket holds one marker and cannot be addressed by
	// account: a marker for another account is not this account's adoption (the intake refuses it too).
	if rc.Marker.Account != "" && rc.Marker.Account != account {
		return refuse("the adoption marker names another account: list sets are ignored")
	}
	return nil
}

// checkSigner is the intake's rule on who may sign: the adoption marker's next submitter when a rotation is pending,
// else its submitter. It applies only when the caller named the signer and the account is adopted (checkAdoption).
func checkSigner(rc *RotationContext) []listset.Refusal {
	if rc == nil || rc.Signer == "" || rc.MarkerErr != nil || !rc.Adopted {
		return nil
	}
	expected, pending := rc.Marker.Submitter, rc.Marker.NextSubmitter != ""
	if pending {
		expected = rc.Marker.NextSubmitter
	}
	if rc.Signer == expected {
		return nil
	}
	reason := "the adoption marker expects " + shortKey(expected)
	if pending {
		reason += " (a rotation is pending: only the next submitter's key is accepted)"
	}
	return []listset.Refusal{{Stage: StageSignature, Entry: "signature", Reason: reason}}
}

// StageDocument is the stage of the refusal Diff reports for a next_submitter the intake would refuse.
const StageDocument = "document"

// StageAdoption is the stage of the refusal Diff reports for an account that is not adopted or whose adoption marker
// cannot be read.
const StageAdoption = "adoption"

// rotate adds what the document's next_submitter would do to p, the way policyintake checks it before an apply. A nil
// rc means the caller read neither the marker nor the keys: the rotation is shown, and said not to be validated.
func rotate(p *DiffPlan, next Document, rc *RotationContext) []listset.Refusal {
	p.NextSubmitter = next.NextSubmitter
	if rc == nil {
		if next.NextSubmitter != "" {
			p.rotation = "next submitter " + next.NextSubmitter + " would be recorded (not validated: the adoption marker and the keys mash holds were not read)"
			p.Rotation = &RotationAction{Action: RotationStarts, NextSubmitter: next.NextSubmitter}
		}
		return nil
	}
	refuse := func(stage, reason string) []listset.Refusal {
		return []listset.Refusal{{Stage: stage, Entry: "next_submitter", Reason: reason}}
	}
	if rc.MarkerErr != nil || !rc.Adopted && rc.Err == nil {
		return nil // checkAdoption refuses the plan
	}
	if rc.Err != nil {
		if next.NextSubmitter == "" {
			return nil
		}
		return refuse(StageDocument, "next_submitter cannot be checked: "+rc.Err.Error())
	}
	pending := rc.Adopted && rc.Marker.NextSubmitter != ""
	if pending && rc.Signer != "" && rc.Signer == rc.Marker.NextSubmitter {
		p.Rotation = &RotationAction{Action: RotationCompletes, NextSubmitter: rc.Marker.NextSubmitter}
		p.rotation = "generation would complete the pending rotation to " + rc.Marker.NextSubmitter
		return nil
	}
	if next.NextSubmitter == "" {
		return nil
	}
	if pending {
		p.rotation = "a rotation to " + rc.Marker.NextSubmitter + " is pending"
		p.Rotation = &RotationAction{Action: RotationPending, NextSubmitter: rc.Marker.NextSubmitter}
		return nil
	}
	if err := admin.ValidateNextSubmitter(next.Account, rc.Marker, next.NextSubmitter, rc.Held); err != nil {
		detail := "next_submitter is not acceptable: " + err.Error()
		if errors.Is(err, admin.ErrNextIsMashKey) {
			detail = "next_submitter is a key mash holds"
		}
		return refuse(StageDocument, detail)
	}
	p.rotation = "next submitter " + next.NextSubmitter + " would be recorded"
	p.Rotation = &RotationAction{Action: RotationStarts, NextSubmitter: next.NextSubmitter}
	return nil
}

// Diff decodes file (raw JSON or a JWS, whose signature is not checked) and returns what applying it would do. A
// document for another account is an error. A document for a range other than rng (when rng is empty, the range of
// the list set in force; with none in force it is an error, ErrRangeNeeded) is reported in the plan as a refusal with stage "range", and
// one whose base_generation is not the generation in force as a refusal with stage "base", and one whose content is not
// that of the pending generation as a refusal with stage "pending", and one whose next_submitter the intake would
// refuse as a refusal with stage "document" or "adoption" (checked against rc; see RotationContext): a submission of
// any would be refused. A document whose content is that in force is refused as the apply refuses it, with stage "unchanged", unless it starts or
// completes a submitter rotation; one with no adoption marker (rc nil: not checked) or an account that is not adopted or
// whose marker cannot be read is refused with stage "adoption".
func Diff(ctx context.Context, r Reader, file []byte, rng string, servers []listset.ServerLimits, rc *RotationContext) (DiffPlan, error) {
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
		if rng == "" {
			rng = cur.ls.Range
		}
	} else if rng == "" {
		return DiffPlan{}, ErrRangeNeeded
	}
	p := listset.DryRun(inForce, next.ListSet, servers)
	var pre []listset.Refusal
	if rng != "" && next.Range != rng {
		pre = append(pre, listset.Refusal{Stage: StageRange, Entry: "range",
			Reason: fmt.Sprintf("the document is for range %s and the selected range is %s", next.Range, rng)})
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
	dp.unsigned = rc == nil || rc.Signer == ""
	dp.adoptionUnchecked = rc == nil
	pre = append(pre, checkAdoption(rc, r.Account())...)
	pre = append(pre, checkSigner(rc)...)
	// The signer decides whether the document completes a pending rotation. Without --submitter it is told from the
	// JWS: the key of the marker (the pending next submitter, or the current one) that verifies the signature.
	rotRC, signerKnown := rc, rc != nil && rc.Signer != ""
	if rc != nil && !signerKnown && rc.Adopted && rc.MarkerErr == nil && rc.Err == nil {
		if k := signerFromJWS(file, rc.Marker); k != "" {
			c := *rc
			c.Signer, rotRC, signerKnown = k, &c, true
		}
	}
	pre = append(pre, rotate(&dp, next, rotRC)...)
	// The apply refuses a list set whose compiled content is that in force (ADR 0119), unless the submission starts or
	// completes a submitter rotation: it asks listset.Accept, or AcceptRotationStep for a rotation step.
	if cur.committed {
		pendingRotation := rc != nil && rc.Adopted && rc.Marker.NextSubmitter != ""
		accept := listset.Accept
		if dp.completes() || next.NextSubmitter != "" && !pendingRotation {
			accept = listset.AcceptRotationStep
		}
		if _, _, err := accept(cur.rec, next.ListSet, file); errors.Is(err, listset.ErrUnchanged) && pendingRotation && !signerKnown && rc.Err == nil && rc.MarkerErr == nil {
			// The intake completes the pending rotation when the pending key signed this document, which cannot be
			// told: not a refusal.
			note := "whether this document completes the pending rotation cannot be checked without --submitter"
			if dp.rotation == "" {
				dp.rotation = "a rotation to " + rc.Marker.NextSubmitter + " is pending; " + note
			} else {
				dp.rotation += "; " + note
			}
			dp.Rotation = &RotationAction{Action: RotationPending, NextSubmitter: rc.Marker.NextSubmitter, Unknown: true}
		} else if errors.Is(err, listset.ErrUnchanged) {
			pre = append(pre, listset.Refusal{Stage: policyapply.StageUnchanged, Entry: "list_set",
				Reason: fmt.Sprintf("same content as generation %d in force; nothing to apply", gen)})
		}
	}
	p = dp.Plan
	if len(pre) > 0 {
		dp.Rotation = nil // nothing is applied, so no rotation step is taken
		p.Refusals = append(pre, p.Refusals...)
		p.Refused = true
		p.Receivers, p.Templates, p.Tags = []listset.ReceiverChange{}, []listset.TemplateChange{}, []listset.TagChange{}
		p.RotateAndRevoke, p.NewCredential, p.Revoke, p.FirstCredential = []string{}, []string{}, []string{}, []string{}
	}
	dp.Plan = p
	return dp, nil
}

// signerFromJWS returns the key of m that signed file (a JWS): the pending next submitter, else the current one;
// "" when the file is not a JWS or neither verifies.
func signerFromJWS(file []byte, m store.Marker) string {
	for _, k := range []string{m.NextSubmitter, m.Submitter} {
		if k == "" {
			continue
		}
		if d, err := Decode(file, DecodeOptions{Submitter: k}); err == nil && d.Verified {
			return k
		}
	}
	return ""
}
