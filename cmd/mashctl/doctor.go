package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// doctorUsage is the text of `mashctl doctor --help`.
const doctorUsage = `usage: mashctl doctor [--strict] [--audit-users] [--json]

Check the server, mash's storage on it and every endpoint's records,
and say how to fix what is wrong.
It changes nothing.

options:
  --strict       exit 1 on a warning too
  --audit-users  fail if a user connected to the mash account was not
                 issued by mashctl; use it to check who is connected
                 now (--strict does not do this)
  --json         print the result as JSON

exit codes:
  0  no problem (and, with --strict, no warning)
  1  a problem was found
  2  the command line was wrong; nothing was attempted

Example: mashctl doctor
`

var doctorSpec = epSpec{name: "doctor", verb: "doctor", flags: []string{"--strict", "--audit-users"}, min: 0, max: 0, offline: true}

// The states of a check.
const (
	stateOK   = "ok"
	stateWarn = "warn"
	stateFail = "fail"
	stateSkip = "skip" // a row that could not run because an earlier one failed
)

// doctorCheck is one line of the report: the text form and one object of the JSON.
type doctorCheck struct {
	Name   string  `json:"name"`
	State  string  `json:"state"`
	Detail string  `json:"detail"`
	Fix    *string `json:"fix"`
}

// doctorReport is what `mashctl doctor --json` prints.
type doctorReport struct {
	Problems int           `json:"problems"`
	Warnings int           `json:"warnings"`
	Checks   []doctorCheck `json:"checks"`

	plainRefused bool // the server row failed on natsauth's refusal of a plain URL: exit 2
}

const initFix = "mashctl init --config <file>"

func (r *doctorReport) add(name, state, detail, fix string) {
	c := doctorCheck{Name: name, State: state, Detail: detail}
	if fix != "" {
		c.Fix = &fix
	}
	r.Checks = append(r.Checks, c)
	switch state {
	case stateFail:
		r.Problems++
	case stateWarn:
		r.Warnings++
	}
}

// doctorRows are the rows in the order they are printed, and what each needs when it is skipped.
var doctorRows = []struct{ name, needs string }{
	{"config", ""}, {"server", "needs the config"}, {"sign-in", "needs the server"},
	{"storage", "needs sign-in"}, {"endpoints", "needs the storage"}, {"keys", "needs sign-in"},
}

// complete makes every row appear, in order: a row that did not run is skip, with what it needs.
func (r *doctorReport) complete() {
	have := map[string]doctorCheck{}
	for _, c := range r.Checks {
		have[c.Name] = c
	}
	r.Checks = r.Checks[:0:0]
	for _, row := range doctorRows {
		if c, ok := have[row.name]; ok {
			r.Checks = append(r.Checks, c)
			continue
		}
		r.Checks = append(r.Checks, doctorCheck{Name: row.name, State: stateSkip, Detail: row.needs})
	}
}

// doctorWord is how the text form says a state.
func doctorWord(s string) string {
	switch s {
	case stateFail:
		return "FAIL"
	case stateWarn:
		return "warn"
	case stateSkip:
		return "skip"
	}
	return "ok"
}

// summary is the line under the checks: "No problems.", "1 problem, 2 warnings.", "No problems, 1 warning.".
func (r doctorReport) summary() string {
	p := "No problems"
	if r.Problems > 0 {
		p = count(r.Problems, "problem")
	}
	if r.Warnings > 0 {
		return p + ", " + count(r.Warnings, "warning") + "."
	}
	return p + "."
}

// hint is the fix of the first problem, or with no problem the first warning ("" when that one names none).
func (r doctorReport) hint() string {
	st := stateWarn
	if r.Problems > 0 {
		st = stateFail
	}
	for _, c := range r.Checks {
		if c.State == st {
			if c.Fix != nil {
				return *c.Fix
			}
			return "" // the first problem (or warning) has no command that fixes it: no hint
		}
	}
	return ""
}

// writeText prints every row, then a blank line and the count, on w; the one hint goes to hintW (stderr at a shell:
// every hint line does, whatever stream the rest uses).
func (r doctorReport) writeText(w, hintW io.Writer) {
	for _, c := range r.Checks {
		// a detail of several lines continues under the detail column (11 + 6)
		detail := strings.ReplaceAll(c.Detail, "\n", "\n"+strings.Repeat(" ", 17))
		fmt.Fprintln(w, strings.TrimRight(fmt.Sprintf("%-11s%-6s%s", c.Name, doctorWord(c.State), detail), " "))
	}
	fmt.Fprintf(w, "\n%s\n", r.summary())
	if h := r.hint(); h != "" {
		fmt.Fprintf(hintW, "hint: %s\n", h)
	}
}

// runDoctor is `mashctl doctor`: it runs the checks in order, a failed check stopping the ones that depend on it, and
// changes nothing.
func runDoctor(args []string, g cliframe.Global, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	in, err := parseEpInput(doctorSpec, args)
	jsonOut := in.json || g.JSON
	if err != nil {
		return cliframe.Report(stdout, stderr, err, jsonRequested(args))
	}
	rep := doctorReport{Checks: []doctorCheck{}}
	doctorChecks(&rep, env, lookup, in.flags["--audit-users"])
	rep.complete()
	if jsonOut {
		writeJSON(stdout, rep)
	} else {
		rep.writeText(stdout, stderr)
	}
	if rep.plainRefused { // a plain URL without the override: a configuration error, whatever else failed
		return 2
	}
	if rep.Problems > 0 || (in.flags["--strict"] && rep.Warnings > 0) {
		return 1
	}
	return 0
}

// oneLine is a message as one cleaned line without mash's own prefixes.
func oneLine(s string) string {
	s = strings.SplitN(s, "\n", 2)[0]
	for _, p := range []string{"mashctl: ", "mash: "} {
		s = strings.TrimPrefix(s, p)
	}
	return config.Clean(s)
}

// doctorChecks runs the six checks and adds each result to rep.
func doctorChecks(rep *doctorReport, env func(string) string, lookup config.Lookup, auditUsers bool) {
	// 1. config
	cfg, err := loadCtlConfig(env, lookup, true)
	why := ""
	switch {
	case err != nil:
		why = oneLine(err.Error())
	case cfg.URL == "":
		why = "no servers in the configuration"
	case cfg.AdminCreds == "":
		why = "no admin_creds in the configuration"
	case cfg.Owner == "":
		why = "no owner in the configuration"
	}
	if why != "" {
		rep.add("config", stateFail, why, initFix)
		return
	}
	rep.add("config", stateOK, "", "")

	// 2. server: the first one listed answers over TLS
	var servers []string
	for _, s := range strings.Split(cfg.URL, ",") {
		if s = strings.TrimSpace(s); s != "" {
			servers = append(servers, s)
		}
	}
	if why := probeServer(cfg, servers[0]); why != "" {
		rep.plainRefused = anyPlainRefused(servers, cfg.SkipEncryption)
		rep.add("server", stateFail, why, "mashctl status")
		return
	}
	rep.add("server", stateOK, maskURL(servers[0]), "")

	// 3. sign-in: the admin credentials connect
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		rep.plainRefused = rep.plainRefused || anyPlainRefused(servers, cfg.SkipEncryption) // a later server in the list is a plain URL: also a configuration error
		msg := oneLine(err.Error())
		if strings.Contains(msg, "uthorization violation") {
			msg = "the server refused the admin credentials"
		}
		rep.add("sign-in", stateFail, msg, initFix)
		return
	}
	defer closeAdmin()
	rep.add("sign-in", stateOK, "", "")

	// 4. storage, then 5. endpoints (which read what storage found)
	if snap, ok := doctorStorage(rep, ctx, adm, cfg); ok {
		doctorEndpoints(rep, snap)
	}

	// 6. keys
	doctorKeys(rep, ctx, adm, cfg, env, auditUsers)
}

// probeServer connects to one server, with the TLS settings of the configuration and no credentials. A server that
// answers, or that refuses the connection for want of credentials, is up; the reason a server is not is returned.
func probeServer(cfg config.Config, server string) string {
	tlsReq, _, err := natsauth.RequireTLS(server, cfg.SkipEncryption)
	if err != nil {
		return oneLine(err.Error())
	}
	if why := probe(cfg, server, tlsReq); why != "" {
		return why
	}
	return ""
}

func probe(cfg config.Config, server string, tlsReq []nats.Option) string {
	opts := append(append(natsauth.TLSOptions(cfg.TLSCA, cfg.TLSCert, cfg.TLSKey), tlsReq...),
		nats.Name("mashctl-doctor"), nats.NoReconnect(), nats.IgnoreDiscoveredServers(), nats.Timeout(statusDeadline))
	nc, err := nats.Connect(server, opts...)
	if err == nil {
		nc.Close()
		return ""
	}
	if errors.Is(err, nats.ErrAuthorization) || errors.Is(err, nats.ErrAuthExpired) ||
		strings.Contains(strings.ToLower(err.Error()), "authorization violation") {
		return ""
	}
	if isUnreachable(err) || strings.Contains(err.Error(), "no servers available") {
		return serverSilent
	}
	return oneLine(config.RedactErr(natsauth.NoServersHint(natsauth.TLSHint(err)), server).Error())
}

// bucketStatus is the byte cap and use of one bucket.
func bucketStatus(ctx context.Context, js jetstream.JetStream, name string) (cap int64, used uint64, err error) {
	kv, err := js.KeyValue(ctx, name)
	if err != nil {
		return 0, 0, err
	}
	st, err := kv.Status(ctx)
	if err != nil {
		return 0, 0, err
	}
	if bs, ok := st.(*jetstream.KeyValueBucketStatus); ok {
		cap = bs.StreamInfo().Config.MaxBytes
	}
	return cap, st.Bytes(), nil
}

// doctorStorage is the storage check: the buckets exist, the directory and lease buckets are not capped below what the
// configuration says and are not nearly full, and the lease bucket is hardened. It returns the snapshot the endpoints
// check reads, and false when there is none.
func doctorStorage(rep *doctorReport, ctx context.Context, adm *admin.Admin, cfg config.Config) (admin.AuditSnapshot, bool) {
	fail := func(detail string) (admin.AuditSnapshot, bool) {
		rep.add("storage", stateFail, detail, initFix)
		return admin.AuditSnapshot{}, false
	}
	var fails, warns []string
	missing, err := adm.MissingBuckets(ctx)
	if err != nil {
		return fail(oneLine(admin.RewordUnavailable(err.Error())))
	}
	core := false
	for _, b := range missing {
		if b == store.DirectoryBucket || b == store.LeaseBucket {
			core = true
			fails = append(fails, "the "+config.Clean(b)+" bucket is missing")
		} else {
			warns = append(warns, "the "+config.Clean(b)+" bucket is missing")
		}
	}
	if core {
		return fail(strings.Join(fails, "; "))
	}
	snap, err := adm.Audit(ctx)
	if err != nil {
		return fail(oneLine(admin.RewordUnavailable(err.Error())))
	}
	switch {
	case snap.LeaseRollup:
		fails = append(fails, "the "+store.LeaseBucket+" bucket allows rollup writes")
	case snap.LeaseError != "":
		fails = append(fails, "the "+store.LeaseBucket+" bucket's configuration could not be read: "+oneLine(snap.LeaseError))
	}
	js, err := jetstream.New(adm.Conn())
	if err != nil {
		return fail(oneLine(err.Error()))
	}
	for _, b := range []struct {
		name       string
		configured int64
	}{{store.DirectoryBucket, cfg.Limits.DirectoryMaxBytes}, {store.LeaseBucket, cfg.Limits.LeaseMaxBytes}} {
		capBytes, used, err := bucketStatus(ctx, js, b.name)
		if err != nil {
			return fail(oneLine(admin.RewordUnavailable(err.Error())))
		}
		switch {
		case capBytes > 0 && b.configured > 0 && capBytes < b.configured:
			warns = append(warns, fmt.Sprintf("the %s bucket's cap is %s, below the configured %s", b.name, sizeText(capBytes), sizeText(b.configured)))
		case capBytes > 0 && used*100 >= uint64(capBytes)*store.InboxNearFullPercent:
			warns = append(warns, fmt.Sprintf("the %s bucket is %d%% full (%s of %s)", b.name, used*100/uint64(capBytes), sizeText(int64(used)), sizeText(capBytes)))
		}
	}
	switch {
	case len(fails) > 0:
		rep.add("storage", stateFail, strings.Join(append(fails, warns...), "; "), initFix)
	case len(warns) > 0:
		rep.add("storage", stateWarn, strings.Join(warns, "; "), initFix)
	default:
		rep.add("storage", stateOK, "", "")
	}
	return snap, true
}

// finding is one thing the endpoints or keys check found: a sentence and the command that fixes it.
type finding struct {
	text, fix string
}

// dotted is the address a flat key names, else the key cleaned.
func dotted(flat string) string {
	if a, err := sap.AddressFromFlat(flat); err == nil {
		return a.String()
	}
	return config.Clean(flat)
}

// names lists up to three of names, then "and N more".
func names(list []string) string {
	if len(list) > 3 {
		return strings.Join(list[:3], ", ") + fmt.Sprintf(" and %d more", len(list)-3)
	}
	return strings.Join(list, ", ")
}

// keyLines lists, each whole on a line of its own after a newline, the one line for the keyless connections (when
// there are any) and then the keys, three lines at most, then "and N more". N counts the remaining users, not lines:
// every key not shown is one user (the keyless line comes first, so it is always shown).
func keyLines(keys []string, keyless int) string {
	var lines []string
	if keyless > 0 {
		lines = append(lines, keylessLine(keyless))
	}
	room := 3 - len(lines)
	lines = append(lines, keys[:min(room, len(keys))]...)
	out := "\n" + strings.Join(lines, "\n")
	if len(keys) > room {
		out += fmt.Sprintf("\nand %d more", len(keys)-room)
	}
	return out
}

// plural picks the verb form for n things: "1 inbox has", "2 inboxes have".
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// addFindings adds one check from its findings: fail when any of fails, else warn when any of warns, else ok with
// okDetail. The detail is the sentences joined, failures first; the fix is the first one's.
func addFindings(rep *doctorReport, name, okDetail string, fails, warns []finding) {
	all := append(slices.Clone(fails), warns...)
	if len(all) == 0 {
		rep.add(name, stateOK, okDetail, "")
		return
	}
	var parts []string
	fix := ""
	for _, f := range all {
		parts = append(parts, f.text)
		if fix == "" {
			fix = f.fix
		}
	}
	state := stateWarn
	if len(fails) > 0 {
		state = stateFail
	}
	rep.add(name, state, strings.Join(parts, "; "), fix)
}

// doctorEndpoints is the endpoints check: every directory record has an inbox and every inbox a record, no inbox lost
// mail unread, no inbox is nearly full, and the retired-name bucket has room and no unfinished retirement.
func doctorEndpoints(rep *doctorReport, snap admin.AuditSnapshot) {
	a := buildAudit(snap)
	var fails, warns []finding

	var bad, orphan, noInbox, lost, pending, otherRetired []string
	var otherRetiredErr string
	for _, p := range a.Problems {
		switch {
		case p.What == "record":
			bad = append(bad, dotted(p.Item))
		case p.What == "inbox":
			lost = append(lost, dotted(p.Item))
		case p.What == "retired" && strings.HasPrefix(p.Error, "pending retired marker"):
			pending = append(pending, dotted(p.Item))
		case p.What == "retired":
			otherRetired = append(otherRetired, dotted(p.Item))
			if otherRetiredErr == "" {
				otherRetiredErr = oneLine(p.Error)
			}
		}
	}
	for _, s := range a.Orphans.Streams {
		orphan = append(orphan, dotted(strings.TrimPrefix(s.Stream, sap.InboxStream(""))))
	}
	for _, r := range a.Records {
		if !r.Inbox.Present {
			noInbox = append(noInbox, r.Address)
		}
	}
	show := func(first string) string { return "mashctl endpoint show " + first }
	if n := len(bad); n > 0 {
		fails = append(fails, finding{fmt.Sprintf("%s unreadable: %s", count(n, "endpoint record")+" "+plural(n, "is", "are"), names(bad)), "mashctl endpoint list"})
	}
	if n := len(orphan); n > 0 {
		fails = append(fails, finding{fmt.Sprintf("%s no endpoint record: %s", count(n, "inbox")+" "+plural(n, "has", "have"), names(orphan)), show(orphan[0])})
	}
	if n := len(noInbox); n > 0 {
		fails = append(fails, finding{fmt.Sprintf("%s no inbox: %s", count(n, "endpoint record")+" "+plural(n, "has", "have"), names(noInbox)), show(noInbox[0])})
	}
	if n := len(lost); n > 0 {
		fails = append(fails, finding{fmt.Sprintf("%s lost mail unread: %s", count(n, "inbox")+" "+plural(n, "has", "have"), names(lost)), "mashctl endpoint inbox " + lost[0]})
	}
	if n := len(pending); n > 0 {
		fails = append(fails, finding{fmt.Sprintf("%s not finished: %s", count(n, "retirement")+" "+plural(n, "is", "are"), names(pending)), "mashctl endpoint delete " + pending[0]})
	}
	if n := len(otherRetired); n > 0 {
		fails = append(fails, finding{fmt.Sprintf("retired-name bucket: %s", otherRetiredErr), ""})
	}
	var near []string
	for _, r := range a.Records {
		if inboxNote(r.Key, r.Inbox) != "" {
			near = append(near, r.Address)
		}
	}
	if n := len(near); n > 0 {
		warns = append(warns, finding{fmt.Sprintf("%s %d%% full or more: %s", count(n, "inbox")+" "+plural(n, "is", "are"), int(store.InboxNearFullPercent), names(near)), "mashctl limits apply " + near[0]})
	}
	if rt := a.Retired; rt != nil && rt.Alarm {
		warns = append(warns, finding{fmt.Sprintf("the retired-name bucket is %.0f%% full", rt.PercentUsed), ""})
	}
	if n := len(a.Orphans.Leases); n > 0 {
		var keys []string
		for _, l := range a.Orphans.Leases {
			keys = append(keys, dotted(l.Key))
		}
		warns = append(warns, finding{fmt.Sprintf("%s no endpoint record: %s", count(n, "lease")+" "+plural(n, "has", "have"), names(keys)), show(keys[0])})
	}
	addFindings(rep, "endpoints", fmt.Sprintf("%s, %s", count(len(a.Records), "record"), count(len(snap.Streams), "inbox")), fails, warns)
}

// connScan is what every server's CONNZ said about the account's connections, and how much of the picture it is.
type connScan struct {
	Conns []apply.ConnzServer
	// Incomplete says why the scan is not the whole picture ("" when it is): the server set is incomplete, a server did
	// not answer the connection list, or a list was not read whole.
	Incomplete string
	// Unsettled is true when the only reason for Incomplete is that some server's list kept changing while it was read:
	// the server set is complete and every server answered. Another read may settle; it is not a missing server.
	Unsettled bool
}

// churnTries is how many times the keys check reads the connections when the lists keep changing; churnPause is the
// short wait between two reads (tests set it to 0).
const churnTries = 3

var churnPause = 300 * time.Millisecond

// scanConns reads the connections; a variable so tests can script a sequence of results.
var scanConns = scanConnections

// scanConnections reads the connections of account from every server over nc (a system account connection). An error is
// a failure to ask at all; a partial answer is reported in Incomplete, never passed over.
func scanConnections(ctx context.Context, nc *nats.Conn, account string, servers int) (connScan, error) {
	res := apply.NATS{Conn: nc, Inventory: servers}
	set, err := res.Servers(ctx)
	if err != nil {
		return connScan{}, err
	}
	conns, err := res.Connz(ctx, account, set.Names, 0)
	if err != nil {
		return connScan{}, err
	}
	sc := connScan{Conns: conns}
	answered := map[string]bool{}
	var unreconciled, missing []string
	for _, s := range conns {
		answered[s.Name] = true
		if s.Incomplete != "" {
			unreconciled = append(unreconciled, s.Incomplete)
		}
	}
	for _, n := range set.Names {
		if !answered[n] {
			missing = append(missing, n)
		}
	}
	switch {
	case !set.Complete(servers):
		sc.Incomplete = set.Why(servers)
	case len(missing) > 0:
		sc.Incomplete = fmt.Sprintf("%d server(s) answered the ping but not the connection list: %s", len(missing), config.Clean(strings.Join(missing, ", ")))
	}
	if len(unreconciled) > 0 {
		sc.Unsettled = sc.Incomplete == ""
		why := strings.Join(unreconciled, "; ")
		if sc.Incomplete != "" {
			why = sc.Incomplete + "; " + why
		}
		sc.Incomplete = why
	}
	return sc, nil
}

// keylessLine stands, on one line, for the connections whose entry carries no user key.
func keylessLine(n int) string {
	if n == 1 {
		return "(no key reported)"
	}
	return fmt.Sprintf("(no key reported, %d connections)", n)
}

// connectedFindings returns the distinct unrecorded keys and, apart, the number of connections that report no key (each
// is a user, so the users not issued by mashctl number len(unrecorded)+keyless), and the revoked keys still connected.
// It sorts the connected keys against the issue record: the keys no record holds, and the keys the
// record holds as revoked. No key is exempt: the admin user is in the account and init records it, and the system
// account's users are never in the mash account's connection list. A connection with no key counts as unrecorded.
func connectedFindings(conns []apply.ConnzServer, recs []store.Issued) (unrecorded []string, keyless int, revoked []string) {
	recorded := map[string]store.Issued{}
	for _, r := range recs {
		recorded[r.PublicKey] = r
	}
	seenU, seenR := map[string]bool{}, map[string]bool{}

	for _, s := range conns {
		for _, c := range s.Conns {
			if c.Key == "" { // a user that cannot be identified was not issued by mashctl as far as anyone can tell
				keyless++
				continue
			}
			r, ok := recorded[c.Key]
			switch {
			case !ok && !seenU[c.Key]:
				seenU[c.Key] = true
				unrecorded = append(unrecorded, c.Key)
			case ok && r.RevokedAt != "" && !seenR[c.Key]:
				seenR[c.Key] = true
				revoked = append(revoked, c.Key)
			}
		}
	}
	slices.Sort(unrecorded)
	slices.Sort(revoked)
	return unrecorded, keyless, revoked
}

// doctorKeys is the keys check: no key the issue record holds as revoked is still connected; with auditUsers, every
// connected user was also issued by mashctl. It fails when the issue record is missing, in every mode, because the
// record is mashctl's own state. It needs the system account credentials; without them it warns and checks nothing,
// or fails under auditUsers.
func doctorKeys(rep *doctorReport, ctx context.Context, adm *admin.Admin, cfg config.Config, env func(string) string, auditUsers bool) {
	if cfg.SysCreds == "" && auditUsers {
		rep.add("keys", stateFail, "no system_creds: connected users cannot be audited", initFix)
		return
	}
	if cfg.SysCreds == "" {
		rep.add("keys", stateWarn, "no system_creds in the configuration; connected keys were not checked", initFix)
		return
	}
	failKeys := func(detail string) {
		rep.add("keys", stateFail, oneLine(admin.RewordUnavailable(detail)), initFix)
	}
	account, err := credsAccountKey(cfg.AdminCreds)
	if err != nil {
		failKeys(err.Error())
		return
	}
	recs, present, err := adm.Issued(ctx)
	if err != nil {
		failKeys(err.Error())
		return
	}
	if !present { // without the record every connected user would look foreign
		rep.add("keys", stateFail, "no issue record; mashctl cannot tell its users from others", initFix)
		return
	}
	nc, err := connectSys(cfg, "", env)
	if err != nil {
		failKeys(err.Error())
		return
	}
	defer nc.Close()
	ctx, cancel := context.WithTimeout(ctx, applyDeadline)
	defer cancel()
	var attempts []connScan
	stopped := false // the deadline or a cancel ended the reads after at least one complete one
	for i := 0; i < churnTries && !stopped; i++ {
		if i > 0 && churnPause > 0 {
			t := time.NewTimer(churnPause)
			select {
			case <-ctx.Done():
				t.Stop()
				stopped = true
			case <-t.C:
			}
			if stopped {
				break
			}
		}
		sc, err := scanConns(ctx, nc, account, serverCount(cfg))
		if err != nil {
			if len(attempts) > 0 && (ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) {
				stopped = true
				break
			}
			failKeys(err.Error())
			return
		}
		attempts = append(attempts, sc)
		if !sc.Unsettled {
			break
		}
	}
	scan := attempts[len(attempts)-1]
	var warns []finding
	churn := false
	if scan.Unsettled {
		// Every read so far was unsettled (the loop ends early only on a settled one): after the last try, or when the
		// deadline stopped the reads.
		reads := "read " + plural(len(attempts), "once", fmt.Sprintf("%d times", len(attempts)))
		if !auditUsers {
			// Churn among users mashctl did not issue is not a finding; churn of one of mashctl's users is.
			if churnIsMashs(attempts, recs) {
				warns = append(warns, finding{"mashctl's users kept connecting and leaving; " + reads, "wait a minute, then run mashctl doctor again"})
			}
			scan.Incomplete = ""
		} else {
			churn = true
			scan.Incomplete = fmt.Sprintf("%s; %s", scan.Incomplete, reads)
		}
	}
	unrecorded, keyless, revoked := connectedFindings(scan.Conns, recs)
	// A key recorded as revoked that was connected in ANY read counts, even when the last read no longer saw it.
	for _, a := range attempts[:len(attempts)-1] {
		_, _, r := connectedFindings(a.Conns, recs)
		for _, k := range r {
			if !slices.Contains(revoked, k) {
				revoked = append(revoked, k)
			}
		}
	}
	slices.Sort(revoked)
	var fails []finding
	const fix = "mashctl endpoint list"
	auditFix := "revoke it with the tool that issued it (mashctl revokes only its own)"
	if len(unrecorded)+keyless > 1 {
		auditFix = "revoke them with the tool that issued them (mashctl revokes only its own)"
	}
	if n := len(revoked); n > 0 {
		fails = append(fails, finding{fmt.Sprintf("%s still connected", count(n, "revoked key")+" "+plural(n, "is", "are")), fix})
	}
	if scan.Incomplete != "" {
		f := finding{oneLine(scan.Incomplete), initFix}
		if churn && len(fails) == 0 && len(unrecorded)+keyless == 0 {
			f.fix = "wait a minute, then run mashctl doctor --audit-users again"
		}
		fails = append(fails, f)
	}
	auditText := ""
	audited := auditUsers && len(unrecorded)+keyless > 0
	if audited { // last: its keys are lines of their own, so no "; ..." may follow
		n := len(unrecorded) + keyless
		auditText = fmt.Sprintf("%s not issued by mashctl:%s", count(n, "connected user")+" "+plural(n, "was", "were"), keyLines(unrecorded, keyless))
		fails = append(fails, finding{auditText, auditFix})
	}
	okDetail := "every connected user was issued by mashctl"
	other := ""
	if n := len(unrecorded) + keyless; n > 0 && !audited {
		other = fmt.Sprintf("%s, not issued by mashctl", count(n, "other user"))
		okDetail = "mashctl's users ok; " + other
	}
	addFindings(rep, "keys", okDetail, fails, warns)
	if c := &rep.Checks[len(rep.Checks)-1]; audited && len(fails) > 1 { // the audit sentence starts a line of its own
		c.Detail = strings.Replace(c.Detail, "; "+auditText, "\n"+auditText, 1)
	}
	if c := &rep.Checks[len(rep.Checks)-1]; other != "" && (c.State == stateFail || c.State == stateWarn) { // the count stays on a line of its own
		c.Detail += "\n" + other
	}
}

// churnIsMashs reports whether mashctl's users came or went between the reads: some key the issue record holds has a
// different set of connection ids in two reads (a recorded key appearing or disappearing counts). Foreign keys,
// keyless connections and a foreign key reconnecting are not mashctl's. With one read there is nothing to compare, and
// mashctl's users cannot be ruled out (the deadline stopped the reads), so it reports true. With several reads that
// differ in nothing, it reports false.
func churnIsMashs(attempts []connScan, recs []store.Issued) bool {
	if len(attempts) < 2 {
		return true
	}
	recorded := map[string]bool{}
	for _, r := range recs {
		recorded[r.PublicKey] = true
	}
	sets := func(a connScan) map[string]map[string]bool {
		m := map[string]map[string]bool{}
		for _, s := range a.Conns {
			for _, c := range s.Conns {
				if !recorded[c.Key] {
					continue
				}
				if m[c.Key] == nil {
					m[c.Key] = map[string]bool{}
				}
				m[c.Key][fmt.Sprintf("%s/%d", s.Name, c.CID)] = true
			}
		}
		return m
	}
	first := sets(attempts[0])
	for _, a := range attempts[1:] {
		next := sets(a)
		if len(next) != len(first) {
			return true
		}
		for k, ids := range first {
			if !maps.Equal(ids, next[k]) {
				return true
			}
		}
	}
	return false
}

// anyPlainRefused reports whether any server in the list is a plain URL the encryption rule refuses: a configuration
// error, whichever server answers.
func anyPlainRefused(servers []string, skip bool) bool {
	for _, s := range servers {
		var p *natsauth.PlainRefusalError
		if _, _, err := natsauth.RequireTLS(s, skip); errors.As(err, &p) {
			return true
		}
	}
	return false
}
