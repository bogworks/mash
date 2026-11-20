package cmds

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore"
	"github.com/bogworks/mash/internal/config"
)

// The states of a check.
const (
	CheckOK   = "ok"
	CheckWarn = "warn"
	CheckFail = "fail"
	// CheckSkip is a row that could not be run because an earlier one failed. It is neither a problem nor a warning.
	CheckSkip = "skip"
)

// ReasonNoSessionEndpoint is the config row's detail, and status's reason, in a session that names no endpoint. It is
// the one case in which status and doctor answer in words of their own instead of the row table or the one line.
const ReasonNoSessionEndpoint = "no endpoint is chosen for this session"

// SessionEndpointFix is what to do about a session that names no endpoint.
const SessionEndpointFix = cmdcore.SessionNoEndpointHint

// ReasonMailHeld is status's reason, and the start of the delivery row's detail, when the session holds mail because
// the folder it requires for arriving mail cannot be written.
const ReasonMailHeld = "mail is held: its folder cannot be written"

// The ways a session can fail to hold its endpoint (HealthReport.Lease), and the words for them. Asked inside a session,
// status and doctor report on that session itself: "ready, online" means this session holds the endpoint.
const (
	LeaseOther  = "other"  // another session holds the endpoint
	LeaseLost   = "lost"   // a newer session took it from this one
	LeaseFailed = "failed" // this session could not take it; HealthReport.LeaseCause says why

	ReasonAnotherSession = "another session holds this endpoint"
	ReasonLostToNewer    = "this session lost this endpoint to a newer one"
	ReasonCouldNotTake   = "this session could not take this endpoint"
)

// leaseReason is the words for a session that does not hold its endpoint ("" when it does): the cause of one that
// could not take it follows after ": ".
func leaseReason(lease, cause string) string {
	switch lease {
	case LeaseOther:
		return ReasonAnotherSession
	case LeaseLost:
		return ReasonLostToNewer
	case LeaseFailed:
		if cause == "" {
			return ReasonCouldNotTake
		}
		return ReasonCouldNotTake + ": " + cause
	}
	return ""
}

// ErrNoEndpointInstalled is what Health returns at a shell where no endpoint is chosen and none is installed. The
// command turns it into the usage error "no endpoint is installed here" (hint "mash join <file>"), exit 2.
var ErrNoEndpointInstalled = errors.New(cmdcore.NoEndpoint(cmdcore.NoneInstalled, "", nil).Msg)

// The checks of doctor, in the order it reports them.
const (
	CheckConfig = "config"
	CheckCreds  = "creds"
	CheckServer = "server"
	CheckSignIn = "sign-in"
	// CheckDelivery is whether mail that arrives can be stored on this machine. It is checked only inside a session.
	CheckDelivery = "delivery"
	CheckRefused  = "refused"
)

// HealthCheck is one row the service found. Detail is what the row says after its state ("" says nothing). Fix is the
// command or step that mends a warn or fail row ("" when none is known). Reason is how status words a failing row when
// that differs from Detail ("" means Detail).
type HealthCheck struct {
	Name   string
	State  string
	Detail string
	Fix    string
	Reason string
}

// HealthReport is what status and doctor are both made from: the endpoint (empty when none is chosen), whether an
// agent session holds it open now, and the rows that could be checked, in order. A row that cannot be checked because
// an earlier one failed is left out.
type HealthReport struct {
	Endpoint string
	Online   bool
	Checks   []HealthCheck
	// MailHeld is true in a session that holds mail because the folder it requires (MASH_SPOOL_REQUIRED) cannot be
	// written: the rows are fine and mail does not work. Status says so; doctor shows it as the delivery row.
	MailHeld bool
	// Lease is "" at a shell and in a session that holds its endpoint; otherwise LeaseOther, LeaseLost or LeaseFailed
	// (LeaseCause says why, for the last). A session that does not hold its endpoint is not online and not ready.
	Lease      string
	LeaseCause string
	// Session is what a session says about itself for status's detail; nil at a shell.
	Session *SessionState
}

// SessionState is what the session says about itself: whether it holds its endpoint, whether the agent host's hooks
// have delivered mail in it (the Hooks words), and whether its mail folder works (Storage).
type SessionState struct {
	HoldsEndpoint bool   `json:"holds_endpoint"`
	Hooks         string `json:"hooks"`
	Storage       string `json:"storage"`
}

// The words of SessionState.Hooks and SessionState.Storage, and of status's reason_code.
const (
	HooksRan       = "ran"
	HooksNotRun    = "not_run"
	HooksSuspected = "suspected"
	HooksDegraded  = "degraded"
	HooksRefused   = "refused"

	StorageOK      = "ok"
	StorageFailing = "failing"
)

// Health is the service status and doctor need from Env.Svc. It returns config.SeveralEndpointsError (wrapped or not)
// when no endpoint is chosen and several are installed, and ErrNoEndpointInstalled when none is chosen and none is
// installed (a session that names no endpoint is a report instead, see ReasonNoSessionEndpoint).
type Health interface {
	Health(ctx context.Context) (HealthReport, error)
}

func healthOf(ctx context.Context, env *cmdcore.Env, name string) (HealthReport, error) {
	if env == nil {
		return HealthReport{}, cliframe.Failf("internal: mash %s has no environment to run in", name)
	}
	svc, ok := env.Svc.(Health)
	if !ok || svc == nil {
		return HealthReport{}, cliframe.Failf("internal: mash %s has no way to check the endpoint", name)
	}
	r, err := svc.Health(ctx)
	var se *config.SeveralEndpointsError
	if errors.As(err, &se) && len(se.Addresses) > 0 {
		return HealthReport{}, cmdcore.NoEndpoint(cmdcore.SeveralInstalled, name, se.Addresses)
	}
	if errors.Is(err, ErrNoEndpointInstalled) {
		return HealthReport{}, cmdcore.NoEndpoint(cmdcore.NoneInstalled, name, nil)
	}
	return r, err
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// StatusParams has no arguments.
type StatusParams struct{}

// StatusResult is the one line: whether mail works for the endpoint and whether it is online. Endpoint is null when no
// endpoint is chosen; Reason is null when ready.
type StatusResult struct {
	Endpoint *string `json:"endpoint"`
	Ready    bool    `json:"ready"`
	Online   bool    `json:"online"`
	Reason   *string `json:"reason"`

	// The fuller answer (DetailValue): not part of the usual object.
	reasonCode string
	session    *SessionState
}

// StatusDetail is status's fuller object: the usual four things, then reason_code and session. reason_code is a fixed
// word beside the free-text reason, null when ready. session is null at a shell.
type StatusDetail struct {
	Endpoint   *string       `json:"endpoint"`
	Ready      bool          `json:"ready"`
	Online     bool          `json:"online"`
	Reason     *string       `json:"reason"`
	ReasonCode *string       `json:"reason_code"`
	Session    *SessionState `json:"session"`
}

// DetailValue is the fuller object, which contains the usual one.
func (r StatusResult) DetailValue() any {
	return StatusDetail{r.Endpoint, r.Ready, r.Online, r.Reason, strPtr(r.reasonCode), r.session}
}

// DetailText is the session's state as 'label: value' lines, or "session: none" at a shell.
func (r StatusResult) DetailText(cmdcore.Opts) string {
	if r.session == nil {
		return "session:        none"
	}
	yn := map[bool]string{true: "yes", false: "no"}
	return fmt.Sprintf("holds endpoint: %s\nhooks:          %s\nstorage:        %s", yn[r.session.HoldsEndpoint], r.session.Hooks, r.session.Storage)
}

// reasonCodeOf is the fixed word for the check (or session state) that made status say not ready.
func reasonCodeOf(check, reason string) string {
	switch check {
	case CheckConfig:
		switch {
		case reason == ReasonNoSessionEndpoint || strings.Contains(reason, "no endpoint"):
			return "no_endpoint"
		case strings.Contains(reason, "more than one endpoint"):
			return "endpoints_ambiguous"
		}
		return "config_unreadable"
	case CheckCreds:
		return "creds_missing"
	case CheckServer:
		switch {
		case strings.Contains(reason, "not trusted"):
			return "server_untrusted"
		case strings.Contains(reason, "did not offer TLS"):
			return "server_no_tls"
		}
		return "server_unreachable"
	case CheckSignIn:
		switch {
		case strings.Contains(reason, "refused"):
			return "creds_refused"
		case strings.Contains(reason, "does not exist") || strings.Contains(reason, "deleted"):
			return "endpoint_deleted"
		}
		return "server_unreachable"
	}
	return ""
}

// noSessionEndpoint is the answer of a session that names no endpoint.
func (r StatusResult) noSessionEndpoint() bool {
	return !r.Ready && r.Endpoint == nil && r.Reason != nil && *r.Reason == ReasonNoSessionEndpoint
}

// Text is "<endpoint>: ready, offline", "<endpoint>: ready, online" or "<endpoint>: not ready: <reason>"; with no
// endpoint chosen the name is "mash". A session that names no endpoint says "No endpoint is chosen for this session."
func (r StatusResult) Text(cmdcore.Opts) string {
	if r.noSessionEndpoint() {
		return cmdcore.NoEndpoint(cmdcore.NoneForSession, "", nil).Msg
	}
	name := "mash"
	if r.Endpoint != nil {
		name = config.Clean(*r.Endpoint)
	}
	if !r.Ready {
		reason := ""
		if r.Reason != nil {
			reason = config.Clean(*r.Reason)
		}
		// the cause of a session that could not take its endpoint goes on the next line, indented
		if cause, ok := strings.CutPrefix(reason, ReasonCouldNotTake+": "); ok {
			reason = ReasonCouldNotTake + ":\n  " + cause
		}
		return name + ": not ready: " + reason
	}
	if r.Online {
		return name + ": ready, online"
	}
	return name + ": ready, offline"
}

// ExitCode is 1 when the endpoint is not ready.
func (r StatusResult) ExitCode() int {
	if r.Ready {
		return 0
	}
	return 1
}

// Hint is "mash doctor" when the endpoint is not ready; for a session that names no endpoint it is how to name one.
func (r StatusResult) Hint() string {
	if r.Ready {
		return ""
	}
	if r.noSessionEndpoint() {
		return SessionEndpointFix
	}
	return "mash doctor"
}

// statusChecks are the rows that decide whether mail works: the refused row is advice, not readiness.
func statusChecks(name string) bool {
	switch name {
	case CheckConfig, CheckCreds, CheckServer, CheckSignIn:
		return true
	}
	return false
}

// Status is the status command.
var Status = cmdcore.Command[StatusParams, StatusResult]{
	Name:        "status",
	Group:       "Everyday",
	Summary:     "Say in one line whether mail works for this endpoint, and whether it is online.",
	Description: "Say in one line whether mail works for this endpoint, and whether it is online.",
	Examples:    []string{"mash status", "mash status --endpoint acme.demo.alice"},
	Run: func(ctx context.Context, env *cmdcore.Env, _ StatusParams) (StatusResult, error) {
		rep, err := healthOf(ctx, env, "status")
		if err != nil {
			return StatusResult{}, err
		}
		r := StatusResult{Endpoint: strPtr(rep.Endpoint), Ready: true, session: rep.Session}
		for _, c := range rep.Checks {
			if c.State == CheckFail && statusChecks(c.Name) {
				reason := c.Reason
				if reason == "" {
					reason = c.Detail
				}
				r.Ready, r.Reason = false, &reason
				r.reasonCode = reasonCodeOf(c.Name, reason)
				break
			}
		}
		// a row that fails is the first thing to mend and is what both doors say; the session's own state comes next
		if reason := leaseReason(rep.Lease, rep.LeaseCause); r.Ready && reason != "" {
			r.Ready, r.Reason = false, &reason
			r.reasonCode = map[string]string{LeaseOther: "endpoint_held_elsewhere", LeaseLost: "endpoint_lost", LeaseFailed: "endpoint_not_taken"}[rep.Lease]
		}
		if r.Ready && rep.MailHeld {
			reason := ReasonMailHeld
			r.Ready, r.Reason, r.reasonCode = false, &reason, "storage_unwritable"
		}
		r.Online = r.Ready && rep.Online
		return r, nil
	},
}

// DoctorParams: Strict makes a warning exit 1 too (on the MCP tool, mark the result an error); it changes nothing else.
type DoctorParams struct {
	Strict bool `json:"strict,omitempty" flag:"--strict" desc:"Exit 1 on a warning too."`
}

// DoctorCheck is one row of the report. Detail and Fix are null when there is none.
type DoctorCheck struct {
	Name   string  `json:"name"`
	State  string  `json:"state"`
	Detail *string `json:"detail"`
	Fix    *string `json:"fix"`
}

// DoctorResult is every check, in order, with a state of ok, warn, fail or skip. Problems counts the rows in state fail,
// Warnings those in state warn; a skipped row is neither.
type DoctorResult struct {
	Endpoint *string       `json:"endpoint"`
	Problems int           `json:"problems"`
	Warnings int           `json:"warnings"`
	Checks   []DoctorCheck `json:"checks"`

	strict bool
}

// Text is a row per check (the name in 10 columns, the state in 6, then the detail), a blank line, and the count:
// "No problems.", or the problems and the warnings that there are ("1 problem, 1 warning.", "2 problems.", "1 warning.").
// A session that names no endpoint says "No endpoint is chosen for this session." instead.
func (r DoctorResult) Text(cmdcore.Opts) string {
	if r.Endpoint == nil && len(r.Checks) > 0 && r.Checks[0].Name == CheckConfig && r.Checks[0].State == CheckFail &&
		r.Checks[0].Detail != nil && *r.Checks[0].Detail == ReasonNoSessionEndpoint {
		return cmdcore.NoEndpoint(cmdcore.NoneForSession, "", nil).Msg
	}
	var b strings.Builder
	for _, c := range r.Checks {
		state := c.State
		if state == CheckFail {
			state = "FAIL"
		}
		line := fmt.Sprintf("%-10s%-6s", c.Name, state)
		extra := ""
		if c.Detail != nil {
			detail := config.Clean(*c.Detail)
			// a held folder's path goes on the next line, under the detail column
			if c.Name == CheckDelivery && c.State == CheckFail {
				for _, head := range []string{ReasonMailHeld, ReasonCouldNotTake} {
					if path, ok := strings.CutPrefix(detail, head+": "); ok {
						detail, extra = head+":", "\n"+strings.Repeat(" ", 16)+path
					}
				}
			}
			line += detail
		}
		b.WriteString(strings.TrimRight(line, " ") + extra + "\n")
	}
	b.WriteString("\n")
	var parts []string
	if r.Problems > 0 {
		parts = append(parts, plural(r.Problems, "problem"))
	}
	if r.Warnings > 0 {
		parts = append(parts, plural(r.Warnings, "warning"))
	}
	if len(parts) == 0 {
		b.WriteString("No problems.")
	} else {
		b.WriteString(strings.Join(parts, ", ") + ".")
	}
	return b.String()
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// ExitCode is 1 when a check failed. A warning alone is 0, or 1 with --strict.
func (r DoctorResult) ExitCode() int {
	if r.Problems > 0 || (r.strict && r.Warnings > 0) {
		return 1
	}
	return 0
}

// Hint is the fix of the first problem or, with no problem, of the first warning; none when that row has no fix. A mashctl
// command is for whoever runs the mesh: "ask whoever runs your mesh to run: mashctl ...". Any other fix is given as it is.
func (r DoctorResult) Hint() string {
	var first *DoctorCheck
	for _, state := range []string{CheckFail, CheckWarn} {
		for i := range r.Checks {
			if r.Checks[i].State == state {
				first = &r.Checks[i]
				break
			}
		}
		if first != nil {
			break
		}
	}
	if first == nil || first.Fix == nil {
		return ""
	}
	if strings.HasPrefix(*first.Fix, "mashctl ") {
		return "ask whoever runs your mesh to run: " + config.Clean(*first.Fix)
	}
	return config.Clean(*first.Fix)
}

// Doctor is the doctor command.
var Doctor = cmdcore.Command[DoctorParams, DoctorResult]{
	Name:        "doctor",
	Group:       "Everyday",
	Summary:     "Check everything mail depends on for this endpoint, and say how to fix what is wrong. Changes nothing.",
	Description: "Check everything mail depends on for this endpoint, and say how to fix what is wrong. Changes nothing.",
	Examples:    []string{"mash doctor", "mash doctor --strict"},
	Run: func(ctx context.Context, env *cmdcore.Env, p DoctorParams) (DoctorResult, error) {
		rep, err := healthOf(ctx, env, "doctor")
		if err != nil {
			return DoctorResult{}, err
		}
		r := DoctorResult{Endpoint: strPtr(rep.Endpoint), Checks: []DoctorCheck{}, strict: p.Strict}
		for _, c := range rep.Checks {
			switch c.State {
			case CheckFail:
				r.Problems++
			case CheckWarn:
				r.Warnings++
			}
			r.Checks = append(r.Checks, DoctorCheck{Name: c.Name, State: c.State, Detail: strPtr(c.Detail), Fix: strPtr(c.Fix)})
		}
		return r, nil
	},
}
