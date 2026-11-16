package policydirect

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/policydoc"
	"github.com/bogworks/mash/internal/policyintake"
	"github.com/bogworks/mash/internal/policyserve"
)

// The outcomes of Apply. Each belongs to one exit-code group (Result.ExitCode).
const (
	// OutcomeApplied: the gate passed on every server (exit 0).
	OutcomeApplied = "applied"
	// OutcomePlanned: a dry run that found nothing wrong; nothing was changed (exit 0).
	OutcomePlanned = "planned"
	// OutcomeUnchanged: the content is the content in force; nothing was changed (exit 0).
	OutcomeUnchanged = "unchanged"
	// OutcomeInvalid: the document is malformed or breaks a rule (exit 1).
	OutcomeInvalid = "invalid"
	// OutcomeUnsupported: the engine cannot do it in this release, such as narrowing an existing sender (exit 1).
	OutcomeUnsupported = "unsupported"
	// OutcomeStale: the base generation is not the one in force, or another generation is pending (exit 1).
	OutcomeStale = "stale"
	// OutcomeBlocked: a precondition fails: an adopted account, an added address with no inbox, a size over a server's
	// limit (exit 1).
	OutcomeBlocked = "blocked"
	// OutcomeSignatureFailed: a signature did not verify (exit 4). A direct caller submits no signature, so Apply never
	// gives it; it is here because the outcome words are one set for every way of applying a policy.
	OutcomeSignatureFailed = "signature_failed"
	// OutcomePartial: pushed, but the gate has not passed or the servers differ; run it again or act on the servers
	// named in the message (exit 5).
	OutcomePartial = "partial"
	// OutcomeError: a connection, the system-account read or an internal failure (exit 6).
	OutcomeError = "error"
)

// StageApply is the stage of a failure that no check names (the intake's own stage for the same).
const StageApply = policyintake.StageApply

// The names and results of the checks.
const (
	CheckDocument       = "document"
	CheckBaseGeneration = "base_generation"
	CheckSize           = "size"
	CheckServers        = "servers"
	CheckSignature      = "signature"

	ResultPass          = "pass"
	ResultFail          = "fail"
	ResultNotApplicable = "not_applicable"
	// ResultNotRun: the checks before it refused, or the run did not get that far.
	ResultNotRun = "not_run"
)

// Check is one check of the run and what it found.
type Check struct {
	Name   string `json:"name"`
	Result string `json:"result"`
	Detail string `json:"detail,omitempty"`
}

// Result is what Apply reports. It is plain data and encodes as JSON as it is.
type Result struct {
	// Outcome is one of the Outcome constants.
	Outcome string `json:"outcome"`
	// Stage is the engine's stage name for a refusal or failure, "" when there is none.
	Stage string `json:"stage,omitempty"`
	// Refused is true when nothing was pushed because a rule or a precondition refused the document (invalid,
	// unsupported, stale, blocked, signature_failed).
	Refused bool `json:"refused"`
	// Message says what happened in plain words.
	Message string `json:"message"`
	// Generation is the generation the document states; for unchanged, the generation in force.
	Generation uint64  `json:"generation"`
	DryRun     bool    `json:"dry_run"`
	Checks     []Check `json:"checks"`
	// Status is the engine's applied-policy report, when the run wrote one.
	Status *policyapply.Status `json:"status,omitempty"`
	// Plan is the dry run's plan, when there was one.
	Plan *listset.Plan `json:"plan,omitempty"`
	// NextSteps is what each sender the policy touches needs done next, from the plan: a dry run's, and a real run's
	// that applied or partly pushed. It is plain data; the steps are not taken here.
	NextSteps []NextStep `json:"next_steps"`
}

// The steps of NextStep, named as the plan names the lists they come from.
const (
	StepFirstCredential = "first_credential"
	StepNewCredential   = "new_credential"
	StepRotateAndRevoke = "rotate_and_revoke"
	StepRevoke          = "revoke"
)

// The actions of NextStep.
const (
	ActionIssue              = "issue"
	ActionIssueThenRevokeOld = "issue_then_revoke_old"
	ActionRevoke             = "revoke"
)

// NextStep is one thing a sender needs once the policy is in force. Purpose is "issue" or "rotation", as a credential
// request for it would carry (empty for a revoke); Address is the sender's address, owner.name.
type NextStep struct {
	Sender  string `json:"sender"`
	Step    string `json:"step"`
	Action  string `json:"action"`
	Address string `json:"address"`
	Purpose string `json:"purpose,omitempty"`
	Detail  string `json:"detail"`
}

// nextSteps is the steps the plan's four lists make, in the order first credential, new credential, rotate and revoke,
// revoke; each list is sorted already.
func nextSteps(p *listset.Plan) []NextStep {
	out := []NextStep{}
	for _, s := range p.FirstCredential {
		out = append(out, NextStep{Sender: s, Step: StepFirstCredential, Action: ActionIssue, Address: s, Purpose: "issue",
			Detail: "issue a credential for a public key the endpoint made; mashctl never needs its private key"})
	}
	for _, s := range p.NewCredential {
		out = append(out, NextStep{Sender: s, Step: StepNewCredential, Action: ActionIssue, Address: s, Purpose: "issue",
			Detail: "issue a new credential for this sender's public key, so its changed tags take effect; nothing is revoked"})
	}
	for _, s := range p.RotateAndRevoke {
		out = append(out, NextStep{Sender: s, Step: StepRotateAndRevoke, Action: ActionIssueThenRevokeOld, Address: s, Purpose: "rotation",
			Detail: "issue a credential for a new public key the endpoint made, then revoke the old key, whose credential keeps reach the policy removed"})
	}
	for _, s := range p.Revoke {
		out = append(out, NextStep{Sender: s, Step: StepRevoke, Action: ActionRevoke, Address: s,
			Detail: "revoke every key issued for this sender, which the policy no longer lists"})
	}
	return out
}

// ExitCode is the exit-code group of the outcome: 0 applied, planned and unchanged; 1 invalid, unsupported, stale and
// blocked; 4 signature_failed; 5 partial; 6 error and anything unknown.
func (r Result) ExitCode() int {
	switch r.Outcome {
	case OutcomeApplied, OutcomePlanned, OutcomeUnchanged:
		return 0
	case OutcomeInvalid, OutcomeUnsupported, OutcomeStale, OutcomeBlocked:
		return 1
	case OutcomeSignatureFailed:
		return 4
	case OutcomePartial:
		return 5
	}
	return 6
}

// stageOutcomes is the one table that maps every stage the engine can name to an outcome.
// TestEveryStageMapsToOneOutcome reads the Stage constants of the engine's packages and fails when one is missing here,
// so a new stage forces a decision.
var stageOutcomes = map[string]string{
	// The list set does not validate or its compiled output does not check: the document breaks a rule. A malformed
	// document and one that names another account are the intake's stages for the same.
	listset.StageValidate:      OutcomeInvalid,
	listset.StageCheck:         OutcomeInvalid,
	policyintake.StageDocument: OutcomeInvalid,
	policyintake.StageAccount:  OutcomeInvalid,
	// The list set in force does not compile: nothing sound to compare with, an internal failure.
	listset.StageInForce: OutcomeError,
	// A limit of the servers, a missing or suspended inbox, an adopted account: a precondition that fails.
	policyserve.StageSize:      OutcomeBlocked,
	policydoc.StageInbox:       OutcomeBlocked,
	policyintake.StageAdoption: OutcomeBlocked,
	// The base is not the generation in force, or another generation is pending.
	policydoc.StageBase:    OutcomeStale,
	policydoc.StagePending: OutcomeStale,
	// A narrowing of an existing sender needs a new credential, which this release does not deliver.
	policydoc.StageNarrowing: OutcomeUnsupported,
	// The content is the content in force.
	policyapply.StageUnchanged: OutcomeUnchanged,
	// A signature that does not verify.
	policyintake.StageSignature: OutcomeSignatureFailed,
	// A failure no check names.
	policyintake.StageApply: OutcomeError,
}

// stateOutcomes maps the states of a status that is not a refusal; a refused status goes through stageOutcomes by its
// first refusal.
var stateOutcomes = map[policyapply.State]string{
	policyapply.StateApplied:  OutcomeApplied,
	policyapply.StatePartial:  OutcomePartial,
	policyapply.StateApplying: OutcomePartial, // a run that stopped after it began to push
}

// outcomeOfStage is the outcome of a stage; an unknown stage is an error, never a guess.
func outcomeOfStage(stage string) string {
	if o, ok := stageOutcomes[stage]; ok {
		return o
	}
	return OutcomeError
}

// refusedOutcome is true for the outcomes that mean nothing was pushed because the document or a precondition refused.
func refusedOutcome(o string) bool {
	switch o {
	case OutcomeInvalid, OutcomeUnsupported, OutcomeStale, OutcomeBlocked, OutcomeSignatureFailed:
		return true
	}
	return false
}

// outcomeOfStatus maps a status the engine wrote to an outcome, a stage and a message.
func outcomeOfStatus(st policyapply.Status) (outcome, stage, msg string) {
	if st.State == policyapply.StateRefused {
		if len(st.Refusals) == 0 {
			return OutcomeError, StageApply, fmt.Sprintf("generation %d was refused with no reason on record", st.Generation)
		}
		r := st.Refusals[0]
		msg = r.Reason
		if len(st.Refusals) > 1 {
			msg = fmt.Sprintf("%s (and %d more refusals in the status)", msg, len(st.Refusals)-1)
		}
		return outcomeOfStage(r.Stage), r.Stage, msg
	}
	o, ok := stateOutcomes[st.State]
	if !ok {
		return OutcomeError, StageApply, fmt.Sprintf("generation %d is in the unknown state %q", st.Generation, st.State)
	}
	if o == OutcomePartial {
		return o, "", partialMessage(st)
	}
	return o, "", fmt.Sprintf("generation %d is applied on every server", st.Generation)
}

// partialMessage names the servers that fail or do not hold the generation, and the failures.
func partialMessage(st policyapply.Status) string {
	bad := map[string]bool{}
	for _, f := range st.Failing {
		if f.Server != "" {
			bad[f.Server] = true
		}
	}
	for _, s := range st.Servers {
		if s.Generation == nil || *s.Generation != st.Generation {
			bad[s.Server] = true
		}
	}
	names := make([]string, 0, len(bad))
	for n := range bad {
		names = append(names, n)
	}
	sort.Strings(names)
	msg := fmt.Sprintf("generation %d is not applied on every server (state %s)", st.Generation, st.State)
	if len(names) > 0 {
		msg += "; servers not at the generation or failing: " + strings.Join(names, ", ")
	}
	var parts []string
	for _, f := range st.Failing {
		p := f.Check
		if f.Server != "" {
			p += " on " + f.Server
		}
		if f.Sender != "" {
			p += " for " + f.Sender
		}
		if f.Detail != "" {
			p += ": " + f.Detail
		}
		parts = append(parts, p)
	}
	if len(st.NotConnected) > 0 {
		parts = append(parts, "not connected: "+strings.Join(st.NotConnected, ", "))
	}
	if len(parts) > 0 {
		msg += "; " + strings.Join(slices.Compact(parts), "; ")
	}
	return msg + "; run it again once the servers are reachable, or act on the servers named"
}
