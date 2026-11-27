package cmds

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore"
	"github.com/bogworks/mash/internal/cmdcore/render"
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

// ReasonStoredScan is status's reason, and the delivery row's detail, while the first start of the switched-on build checks
// the stored mail before it delivers any (TO-14). It happens once; there is nothing to do.
const ReasonStoredScan = "mail is waiting while stored mail is checked"

// StoredMailReason is why the scan was skipped, as status --detail --json words it.
const StoredMailReason = "state directory not writable"

// StoredMailFix is the stored mail row's fix in doctor --json.
const StoredMailFix = "make that folder writable, then restart the session"

// StoredMail is the "stored_mail" object of status --detail --json: present only while the first start's scan of the stored
// mail is skipped because the state directory is not writable.
type StoredMail struct {
	Checked bool   `json:"checked"`
	Dir     string `json:"dir"`
	Reason  string `json:"reason"`
}

// storedMailNotChecked is what status --detail and doctor say after "not checked: ".
func storedMailNotChecked(dir string) string { return "not checked: " + dir + " is not writable" }

// StoredMailCheck is doctor's stored mail row for a skipped scan in dir; it is a skip, so it changes neither the verdict nor
// the exit status.
func StoredMailCheck(dir string) HealthCheck {
	return HealthCheck{Name: CheckStoredMail, State: CheckSkip, Detail: storedMailNotChecked(dir), Fix: StoredMailFix}
}

// storedMailLine is the stored mail row of status --detail ("" when the scan is not skipped).
func storedMailLine(dir string) string {
	if dir == "" {
		return ""
	}
	return "\nstored mail:    " + storedMailNotChecked(dir)
}

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

// The words a session gives for why it could not take its endpoint (the cause after ReasonCouldNotTake). The code of a
// failed take follows the cause: a server that did not answer is server_unreachable, refused credentials creds_refused.
const (
	CauseServerSilent = "the server did not answer"
	CauseCredsRefused = "the server refused its credentials"
)

// leaseCode is the reason_code of a session that could not take its endpoint, from the cause it gave.
func leaseCode(cause string) string {
	switch {
	case strings.HasPrefix(cause, CauseServerSilent):
		return CodeServerUnreachable
	case cause == CauseCredsRefused:
		return CodeCredsRefused
	case cause == ReasonEndpointDeleted:
		return CodeEndpointDeleted
	}
	return CodeEndpointNotTaken
}

// ReasonEndpointDeleted is the sign-in row's detail (and status's reason) for an endpoint whose directory record is
// gone, and EndpointDeletedFix is its fix: a deleted address is never reused, so the way on is a new endpoint.
const (
	ReasonEndpointDeleted = cmdcore.ReasonEndpointDeleted
	EndpointDeletedFix    = JoinFix
)

// JoinFix mends a creds file that is missing, or a deleted endpoint: a join file for a new endpoint installs it.
// JoinReplaceFix is the same for creds that are installed but unusable or refused: join refuses an installed address
// without --replace. A certificate the server's CA did not sign has no hint: join cannot change a folder's CA.
// A hint only where the doctor found exactly one of those conditions.
const (
	JoinFix        = "mash join <file>"
	JoinReplaceFix = "mash join <file> --replace"
)

// CannotWrite begins the delivery row's detail for a folder that cannot be written; the folder follows.
const CannotWrite = "cannot write "

// ShellFolderWarnTail ends the delivery warning a shell gives for a state folder it cannot create, after a "; ".
const ShellFolderWarnTail = "plugin sessions may get no mail"

// The delivery row's words for a session whose hooks have not delivered mail yet (a warning) and for one whose hooks
// are suspected, degraded or refused (a failure).
const (
	ReasonHooksNotRun  = "hooks have not run in this session yet; they run with the first prompt or tool call"
	ReasonHooksFailing = "hooks are not delivering mail to this session; restart the session after reinstalling"
	// HooksFailingFix is the delivery row's fix for failing hooks. The not-yet-run warning has none: no command fixes it.
	HooksFailingFix = "claude plugin install mash@mash"
)

// hookRowWraps is where the text form breaks the delivery row's two hook details, as the specification's page shows them:
// the line, and the rest under the detail column.
var hookRowWraps = map[string][2]string{
	ReasonHooksNotRun:  {"hooks have not run in this session yet; they run with", "the first prompt or tool call"},
	ReasonHooksFailing: {"hooks are not delivering mail to this session;", "restart the session after reinstalling"},
}

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
// command turns it into the failure "no endpoint is installed here" with no hint, exit 1.
var ErrNoEndpointInstalled = errors.New(cmdcore.NoEndpoint(cmdcore.NoneInstalled, "", nil).Msg)

// The checks of doctor, in the order it reports them.
const (
	CheckConfig = "config"
	// CheckClaude is the Claude Code version against the tested range. It stands alone: no other row decides it.
	CheckClaude = "claude"
	// CheckLauncher is which mash the Claude Code plugin's launcher ran. It appears only when the launcher set
	// MASH_LAUNCHED_FROM, so nothing changes for anyone who does not use the plugin.
	CheckLauncher = "launcher"
	CheckCreds    = "creds"
	CheckServer   = "server"
	CheckSignIn   = "sign-in"
	// CheckDelivery is whether mail that arrives can be stored on this machine. It is checked only inside a session.
	CheckDelivery = "delivery"
	CheckRefused  = "refused"
	// CheckStoredMail is the row doctor shows only while the stored-mail scan is skipped for want of a writable state dir.
	CheckStoredMail = "stored mail"
)

// The reason codes of status --detail: a fixed word beside the free-text reason. Each names the failure that produces it.
const (
	CodeNoEndpoint            = "no_endpoint"             // a session names no endpoint
	CodeEndpointsAmbiguous    = "endpoints_ambiguous"     // no endpoint is named and several are installed
	CodeConfigUnreadable      = "config_unreadable"       // the configuration cannot be read or is incomplete
	CodeCredsMissing          = "creds_missing"           // the creds file is missing or unusable
	CodeCredsRefused          = "creds_refused"           // the server refused who we are: the creds or our client certificate, or the creds are another endpoint's
	CodeEndpointDeleted       = "endpoint_deleted"        // the endpoint's directory record is gone
	CodeServerUnreachable     = "server_unreachable"      // no server answered
	CodeServerUntrusted       = "server_untrusted"        // the server's certificate is not trusted
	CodeServerNoTLS           = "server_no_tls"           // a plain address, or a server that offers no TLS
	CodeEndpointHeldElsewhere = "endpoint_held_elsewhere" // another session holds the endpoint
	CodeEndpointLost          = "endpoint_lost"           // a newer session took it from this one
	CodeEndpointNotTaken      = "endpoint_not_taken"      // this session could not take it
	CodeStorageUnwritable     = "storage_unwritable"      // the mail folder cannot be written
	CodeUnknown               = "unknown"                 // a failure that fits none of the others; the reason says what was seen
	CodeCheckingStoredMail    = "checking_stored_mail"    // the first start's stored-mail check is running
)

// RefusedCounts is how many refused messages the endpoint's inbox still holds, in all and for each class (REQ-006). A
// message that was refused for its header and one that was not verified are both counted; one on both lists counts once.
type RefusedCounts struct {
	Total   int
	ByClass map[string]int
}

// classCount is one class and its count, for the lines that name them.
type classCount struct {
	class string
	n     int
}

// sortedClasses lists the classes largest count first, ties by class name.
func (r RefusedCounts) sortedClasses() []classCount {
	out := make([]classCount, 0, len(r.ByClass))
	for c, n := range r.ByClass {
		out = append(out, classCount{c, n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].n != out[j].n {
			return out[i].n > out[j].n
		}
		return out[i].class < out[j].class
	})
	return out
}

// byClass is the counts as the JSON carries them: an object, never null.
func (r RefusedCounts) byClass() map[string]int {
	if r.ByClass == nil {
		return map[string]int{}
	}
	return r.ByClass
}

// RefusedDetail is the refused row's detail (TO-5): the count in words, "no mail was refused" with none.
func RefusedDetail(n int) string {
	switch n {
	case 0:
		return "no mail was refused"
	case 1:
		return "1 message was refused"
	}
	return fmt.Sprintf("%d messages were refused", n)
}

// refusedStatusLines is the refused row of status --detail (TO-4): none; or the total and the one class on a line; or the
// total and then one line per class under the value column.
func (r RefusedCounts) statusLines() string {
	const label = "refused:        "
	cs := r.sortedClasses()
	switch {
	case r.Total == 0 || len(cs) == 0:
		return label + "none"
	case len(cs) == 1:
		return label + render.Plural(r.Total, "message") + ": " + cs[0].class
	}
	lines := []string{label + render.Plural(r.Total, "message")}
	for _, c := range cs {
		lines = append(lines, strings.Repeat(" ", len(label))+fmt.Sprintf("%d %s", c.n, c.class))
	}
	return strings.Join(lines, "\n")
}

// HealthCheck is one row the service found. Detail is what the row says after its state ("" says nothing). Fix is the
// command or step that mends a warn or fail row ("" when none is known). Reason is how status words a failing row when
// that differs from Detail ("" means Detail).
type HealthCheck struct {
	Name   string
	State  string
	Detail string
	Fix    string
	Reason string
	// Code is the reason_code status gives when this row fails (one of the Code constants), set where the failure is
	// classified; "" on a row that did not fail.
	Code string
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
	// StoredScan is true while serve checks the stored mail before it delivers any, once, at the first start of the
	// switched-on build: status says not ready with ReasonStoredScan.
	StoredScan bool
	// StoredSkipDir is the state directory that was not writable when the scan was due, so that it was skipped; "" when it
	// ran or was not needed. Status --detail and doctor show it (a skip, not a failure).
	StoredSkipDir string
	// Refused is the refused messages the inbox still holds, from the endpoint's two lists (the refused row, status --detail).
	Refused RefusedCounts
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

// ClaudeChecker is the optional service only doctor uses: the claude row, the installed Claude Code against the tested
// range. Status never asks for it, so status never looks for claude (ADR 0169).
type ClaudeChecker interface {
	ClaudeCheck(ctx context.Context) HealthCheck
}

// LauncherChecker is the optional service of the launcher row: it returns false when mash was not started by the plugin's
// launcher (the row is then left out).
type LauncherChecker interface {
	LauncherCheck() (HealthCheck, bool)
}

// rawHealth is healthOf without the translation of the two no-endpoint errors: doctor, at a shell with several
// endpoints installed, answers for all of them instead of failing.
func rawHealth(ctx context.Context, env *cmdcore.Env, name string) (HealthReport, error) {
	if env == nil {
		return HealthReport{}, cliframe.Failf("internal: mash %s has no environment to run in", name)
	}
	svc, ok := env.Svc.(Health)
	if !ok || svc == nil {
		return HealthReport{}, cliframe.Failf("internal: mash %s has no way to check the endpoint", name)
	}
	return svc.Health(ctx)
}

func healthOf(ctx context.Context, env *cmdcore.Env, name string) (HealthReport, error) {
	r, err := rawHealth(ctx, env, name)
	return r, noEndpointError(err, name)
}

// noEndpointError turns config.SeveralEndpointsError and ErrNoEndpointInstalled into the framed errors of
// cmdcore.NoEndpoint; any other error is returned as it is.
func noEndpointError(err error, name string) error {
	var se *config.SeveralEndpointsError
	if errors.As(err, &se) && len(se.Addresses) > 0 {
		return cmdcore.NoEndpoint(cmdcore.SeveralInstalled, name, se.Addresses)
	}
	if errors.Is(err, ErrNoEndpointInstalled) {
		return cmdcore.NoEndpoint(cmdcore.NoneInstalled, name, nil)
	}
	return err
}

// HealthEacher is the optional service doctor uses at a shell with several endpoints installed and none chosen: the
// report of each address, in the order given, each made exactly as a run with --endpoint <address> would make it.
type HealthEacher interface {
	HealthEach(ctx context.Context, addresses []string) ([]HealthReport, error)
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
	refused    RefusedCounts
	storedSkip string
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
	// Refused is how many refused messages the inbox holds (0 when none) and RefusedByClass the count for each class ({}
	// when none): always present with --detail (TO-4).
	Refused        int            `json:"refused"`
	RefusedByClass map[string]int `json:"refused_by_class"`
	// StoredMail is null unless the scan of the stored mail is skipped for want of a writable state directory.
	StoredMail *StoredMail `json:"stored_mail"`
}

// DetailValue is the fuller object, which contains the usual one.
func (r StatusResult) DetailValue() any {
	var sm *StoredMail
	if r.storedSkip != "" {
		sm = &StoredMail{Dir: r.storedSkip, Reason: StoredMailReason}
	}
	return StatusDetail{r.Endpoint, r.Ready, r.Online, r.Reason, strPtr(r.reasonCode), r.session, r.refused.Total, r.refused.byClass(), sm}
}

// DetailText is the session's state as 'label: value' lines, or "session: none" at a shell.
func (r StatusResult) DetailText(cmdcore.Opts) string {
	if r.session == nil {
		return "session:        none\n" + r.refused.statusLines() + storedMailLine(r.storedSkip)
	}
	yn := map[bool]string{true: "yes", false: "no"}
	return fmt.Sprintf("holds endpoint: %s\nhooks:          %s\nstorage:        %s\n%s", yn[r.session.HoldsEndpoint], r.session.Hooks, r.session.Storage, r.refused.statusLines()+storedMailLine(r.storedSkip))
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
	Globals:     cmdcore.GlobalsAll,
	Examples:    []string{"mash status", "mash status --endpoint acme.demo.alice"},
	Run: func(ctx context.Context, env *cmdcore.Env, _ StatusParams) (StatusResult, error) {
		rep, err := healthOf(ctx, env, "status")
		if err != nil {
			return StatusResult{}, err
		}
		r := StatusResult{Endpoint: strPtr(rep.Endpoint), Ready: true, session: rep.Session, refused: rep.Refused, storedSkip: rep.StoredSkipDir}
		for _, c := range rep.Checks {
			if c.State == CheckFail && statusChecks(c.Name) {
				reason := c.Reason
				if reason == "" {
					reason = c.Detail
				}
				r.Ready, r.Reason = false, &reason
				r.reasonCode = c.Code
				break
			}
		}
		// a row that fails is the first thing to mend and is what both doors say; the session's own state comes next
		if reason := leaseReason(rep.Lease, rep.LeaseCause); r.Ready && reason != "" {
			r.Ready, r.Reason = false, &reason
			r.reasonCode = map[string]string{LeaseOther: CodeEndpointHeldElsewhere, LeaseLost: CodeEndpointLost, LeaseFailed: leaseCode(rep.LeaseCause)}[rep.Lease]
		}
		if r.Ready && rep.MailHeld {
			reason := ReasonMailHeld
			r.Ready, r.Reason, r.reasonCode = false, &reason, CodeStorageUnwritable
		}
		if r.Ready && rep.StoredScan {
			reason := ReasonStoredScan
			r.Ready, r.Reason, r.reasonCode = false, &reason, CodeCheckingStoredMail
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
	// Refused and RefusedByClass are the same two facts as in status --detail (TO-5): always present, 0 and {} with none.
	Refused        int            `json:"refused"`
	RefusedByClass map[string]int `json:"refused_by_class"`
	// HintText is the one hint the doctor ends with, null when there is none: a command of ours that fixes the first
	// problem. Rows' own fix fields name each row's command, and only this one is offered.
	HintText *string `json:"hint"`

	strict  bool
	hint    string
	refused RefusedCounts
	// several is set for a shell with several endpoints installed and none chosen: one result per endpoint, and
	// Problems and Warnings are the totals. Its JSON form is severalDoctor.
	several []DoctorResult
	addrs   []string
}

// severalDoctor is the --json form of a doctor that answered for several endpoints: each endpoint's own object, the
// totals, and the one hint.
type severalDoctor struct {
	Endpoints []DoctorResult `json:"endpoints"`
	Problems  int            `json:"problems"`
	Warnings  int            `json:"warnings"`
	Hint      *string        `json:"hint"`
}

// MarshalJSON is the object of one endpoint, or severalDoctor for several. It does not escape <, > and &, as the rest of
// the command line's JSON does not.
func (r DoctorResult) MarshalJSON() ([]byte, error) {
	var v any
	type plain DoctorResult
	if r.several != nil {
		v = severalDoctor{r.several, r.Problems, r.Warnings, r.HintText}
	} else {
		v = plain(r)
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// Text is a row per check (the name in 10 columns, the state in 6, then the detail), a blank line, and the count:
// "No problems.", or the problems and the warnings that there are ("1 problem, 1 warning.", "2 problems.", "1 warning.").
// A session that names no endpoint says "No endpoint is chosen for this session." instead.
func (r DoctorResult) Text(cmdcore.Opts) string {
	if r.several != nil {
		return r.severalText()
	}
	if r.Endpoint == nil && len(r.Checks) > 0 && r.Checks[0].Name == CheckConfig && r.Checks[0].State == CheckFail &&
		r.Checks[0].Detail != nil && *r.Checks[0].Detail == ReasonNoSessionEndpoint {
		return cmdcore.NoEndpoint(cmdcore.NoneForSession, "", nil).Msg
	}
	var b strings.Builder
	nameW := 10 // the longest name and two; the usual rows are 8 and under
	for _, c := range r.Checks {
		nameW = max(nameW, len(c.Name)+2)
	}
	indent := strings.Repeat(" ", nameW+6) // the detail column
	for _, c := range r.Checks {
		state := c.State
		if state == CheckFail {
			state = "FAIL"
		}
		line := fmt.Sprintf("%-*s%-6s", nameW, c.Name, state)
		extra := ""
		if c.Detail != nil {
			detail := config.Clean(*c.Detail)
			// a held folder's path goes on the next line, under the detail column
			if c.Name == CheckDelivery && c.State == CheckFail {
				for _, head := range []string{ReasonMailHeld, ReasonCouldNotTake} {
					if path, ok := strings.CutPrefix(detail, head+": "); ok {
						detail, extra = head+":", "\n"+indent+path
					}
				}
			}
			// a folder too long for the line goes under the detail column
			if path, ok := strings.CutPrefix(detail, CannotWrite); ok && c.Name == CheckDelivery && len(line+detail) > 80 {
				detail, extra = strings.TrimSpace(CannotWrite), "\n"+indent+path
				if p, ok := strings.CutSuffix(path, "; "+ShellFolderWarnTail); ok { // a warning's reason goes on a line of its own
					extra = "\n" + indent + p + "\n" + indent + ShellFolderWarnTail
				}
			}
			if w, ok := hookRowWraps[detail]; ok && c.Name == CheckDelivery {
				detail, extra = w[0], "\n"+indent+w[1]
			}
			line += detail
		}
		if c.Name == CheckRefused && c.State == CheckOK { // one line under the row for each class, under the detail column
			for _, cc := range r.refused.sortedClasses() {
				extra += "\n" + indent + fmt.Sprintf("%d %s", cc.n, cc.class)
			}
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

// maxLine is the width the table of several endpoints fits.
const maxLine = 79

// firstIssue is "<check>: <detail>" of the first failing row, else of the first row in state warn ("" when none).
func (r DoctorResult) firstIssue() string {
	for _, state := range []string{CheckFail, CheckWarn} {
		for _, c := range r.Checks {
			if c.State != state {
				continue
			}
			if c.Detail == nil || *c.Detail == "" {
				return c.Name
			}
			return c.Name + ": " + config.Clean(*c.Detail)
		}
	}
	return ""
}

// severalText is the table of a doctor that answered for several endpoints: a row per endpoint with its counts and its
// first issue, then the totals and the command that shows the first endpoint with a problem (else a warning).
func (r DoctorResult) severalText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Endpoints installed here: %d.\n\n", len(r.several))
	w := utf8.RuneCountInString("ENDPOINT")
	for _, a := range r.addrs {
		w = max(w, utf8.RuneCountInString(config.Clean(a)))
	}
	w += 2
	const pw, ww = 10, 10 // "PROBLEMS  " and "WARNINGS  ": the headers and two spaces, as wide as any count
	const minIssue = 20   // a row that leaves less room than this for its issue prints the issue on the next line
	cut := func(t string, room int) string {
		if utf8.RuneCountInString(t) > room {
			return string([]rune(t)[:max(room-1, 0)]) + "…"
		}
		return t
	}
	line := func(a, p, wn, first string) {
		head := fmt.Sprintf("%-*s%-*s%-*s", w, a, pw, p, ww, wn)
		next := ""
		if first != "" {
			room := maxLine - utf8.RuneCountInString(head)
			if room < minIssue {
				// the address is never cut, it is what a person copies into --endpoint: the issue goes under the
				// ISSUE column, or 4 spaces in when that column is too far right to leave it room
				indent := w + pw + ww
				if maxLine-indent < minIssue {
					indent = 4
				}
				next = "\n" + strings.Repeat(" ", indent) + cut(first, maxLine-indent)
				first = ""
			} else {
				first = cut(first, room)
			}
		}
		b.WriteString(strings.TrimRight(head+first, " ") + next + "\n")
	}
	line("ENDPOINT", "PROBLEMS", "WARNINGS", "FIRST ISSUE")
	for i, e := range r.several {
		line(config.Clean(r.addrs[i]), fmt.Sprint(e.Problems), fmt.Sprint(e.Warnings), e.firstIssue())
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
		b.WriteString(strings.Join(parts, ", ") + ". See it: " + r.hint)
	}
	return b.String()
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// ExitCode is 1 when a check failed (cmdcore.DoctorExitCode). A warning alone is 0, or 1 with --strict.
func (r DoctorResult) ExitCode() int {
	return cmdcore.DoctorExitCode(r.Problems, r.Warnings, r.strict)
}

// Hint is the one command that fixes the first problem (cmdcore.DoctorHint): none when that row has no command of ours.
// With no problem it is the command of the first warning that has one that fixes it, or none.
// For several endpoints it is none: the text's last line names the command that shows the first endpoint with an
// issue, and the JSON hint is null (ADR 0168).
func (r DoctorResult) Hint() string {
	if r.several != nil {
		return ""
	}
	return r.hint
}

// Doctor is the doctor command.
var Doctor = cmdcore.Command[DoctorParams, DoctorResult]{
	Name:        "doctor",
	Group:       "Everyday",
	Summary:     "Check everything mail depends on for this endpoint, and say how to fix what is wrong. Changes nothing.",
	Description: "Check everything mail depends on for this endpoint, and say how to fix what is wrong. Changes nothing.",
	Globals:     cmdcore.GlobalsAll,
	Examples:    []string{"mash doctor", "mash doctor --strict", "mash doctor --endpoint acme.demo.alice"},
	Run: func(ctx context.Context, env *cmdcore.Env, p DoctorParams) (DoctorResult, error) {
		rep, err := rawHealth(ctx, env, "doctor")
		var se *config.SeveralEndpointsError
		if errors.As(err, &se) && len(se.Addresses) > 0 {
			if he, ok := env.Svc.(HealthEacher); ok && he != nil {
				return doctorSeveral(ctx, env, he, slices.Sorted(slices.Values(se.Addresses)), p.Strict)
			}
		}
		if err = noEndpointError(err, "doctor"); err != nil {
			return DoctorResult{}, err
		}
		var claude *HealthCheck
		if cc, ok := env.Svc.(ClaudeChecker); ok && cc != nil { // the claude row sits right after config and waits on nothing
			row := cc.ClaudeCheck(ctx)
			claude = &row
		}
		return doctorOf(rep, claude, launcherRow(env), p.Strict), nil
	},
}

// launcherRow is the launcher row, or nil when mash was not started by the plugin's launcher. It is machine-wide, like
// the claude row.
func launcherRow(env *cmdcore.Env) *HealthCheck {
	if lc, ok := env.Svc.(LauncherChecker); ok && lc != nil {
		if row, shown := lc.LauncherCheck(); shown {
			return &row
		}
	}
	return nil
}

// doctorOf is the result of one endpoint's report, with the claude row after config and the launcher row after that (a
// nil row is left out).
func doctorOf(rep HealthReport, claude, launcher *HealthCheck, strict bool) DoctorResult {
	r := DoctorResult{Endpoint: strPtr(rep.Endpoint), Checks: []DoctorCheck{}, strict: strict, Refused: rep.Refused.Total,
		RefusedByClass: rep.Refused.byClass(), refused: rep.Refused}
	checks := rep.Checks
	if claude != nil {
		at := 0
		if len(checks) > 0 && checks[0].Name == CheckConfig {
			at = 1
		}
		checks = append(append(append([]HealthCheck{}, checks[:at]...), *claude), checks[at:]...)
	}
	if launcher != nil { // the launcher row follows claude, or config
		at := 0
		for i, c := range checks {
			if c.Name == CheckConfig || c.Name == CheckClaude {
				at = i + 1
			}
		}
		checks = append(append(append([]HealthCheck{}, checks[:at]...), *launcher), checks[at:]...)
	}
	if rep.StoredSkipDir != "" { // the scan was skipped: a skip row after delivery, which changes no count
		at := len(checks)
		for i, c := range checks {
			if c.Name == CheckDelivery {
				at = i + 1
			}
		}
		checks = append(append(append([]HealthCheck{}, checks[:at]...), StoredMailCheck(rep.StoredSkipDir)), checks[at:]...)
	}
	var fixes, warnFixes []string
	for _, c := range checks {
		switch c.State {
		case CheckFail:
			r.Problems++
			fixes = append(fixes, c.Fix)
		case CheckWarn:
			r.Warnings++
			warnFixes = append(warnFixes, c.Fix)
		}
		r.Checks = append(r.Checks, DoctorCheck{Name: c.Name, State: c.State, Detail: strPtr(c.Detail), Fix: strPtr(c.Fix)})
	}
	r.hint = cmdcore.DoctorHint(fixes, warnFixes)
	r.HintText = strPtr(r.hint)
	return r
}

// doctorSeveral checks every installed endpoint as a run with --endpoint would, and totals them. The claude row is
// machine-wide: it is run once and the same row is in every endpoint's result, so a claude problem counts once for each.
func doctorSeveral(ctx context.Context, env *cmdcore.Env, he HealthEacher, addrs []string, strict bool) (DoctorResult, error) {
	reps, err := he.HealthEach(ctx, addrs)
	if err != nil {
		return DoctorResult{}, err
	}
	var claude *HealthCheck
	if cc, ok := env.Svc.(ClaudeChecker); ok && cc != nil {
		row := cc.ClaudeCheck(ctx)
		claude = &row
	}
	launcher := launcherRow(env)
	r := DoctorResult{Checks: []DoctorCheck{}, strict: strict, several: []DoctorResult{}, addrs: addrs}
	hintAt, warnAt := -1, -1
	for i, rep := range reps {
		e := doctorOf(rep, claude, launcher, strict)
		r.several = append(r.several, e)
		r.Problems += e.Problems
		r.Warnings += e.Warnings
		if e.Problems > 0 && hintAt < 0 {
			hintAt = i
		}
		if e.Warnings > 0 && warnAt < 0 {
			warnAt = i
		}
	}
	if hintAt < 0 {
		hintAt = warnAt
	}
	if hintAt >= 0 {
		// Only the text's See-it line names it: `--endpoint` shows a result, it fixes nothing, so by ADR 0168 the JSON
		// hint stays null.
		r.hint = "mash doctor --endpoint " + addrs[hintAt]
	}
	return r, nil
}
