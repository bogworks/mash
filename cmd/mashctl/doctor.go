package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

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
const doctorUsage = `usage: mashctl doctor [--strict] [--json]

Check the server, mash's storage on it and every endpoint's records, and say how to fix what is wrong.
It changes nothing.

options:
  --strict   exit 1 on a warning too
  --json     print the result as JSON

exit codes:
  0  no problem (and, with --strict, no warning)
  1  a problem was found
  2  the command line was wrong; nothing was attempted

Example: mashctl doctor
`

var doctorSpec = epSpec{name: "doctor", verb: "doctor", flags: []string{"--strict"}, min: 0, max: 0, offline: true}

// The states of a check.
const (
	stateOK   = "ok"
	stateWarn = "warn"
	stateFail = "fail"
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

// doctorWord is how the text form says a state.
func doctorWord(s string) string {
	switch s {
	case stateFail:
		return "FAIL"
	case stateWarn:
		return "warn"
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

// hint is the fix of the first check that failed, else of the first that warned ("" when none names one).
func (r doctorReport) hint() string {
	for _, st := range []string{stateFail, stateWarn} {
		for _, c := range r.Checks {
			if c.State == st && c.Fix != nil {
				return *c.Fix
			}
		}
	}
	return ""
}

// writeText prints the report: every check when all is well, else only the checks that are not ok, then the summary
// and the hint.
func (r doctorReport) writeText(w io.Writer) {
	all := r.Problems == 0 && r.Warnings == 0
	for _, c := range r.Checks {
		if !all && c.State == stateOK {
			continue
		}
		fmt.Fprintln(w, strings.TrimRight(fmt.Sprintf("%-11s%-6s%s", c.Name, doctorWord(c.State), c.Detail), " "))
	}
	fmt.Fprintf(w, "\n%s\n", r.summary())
	if h := r.hint(); h != "" && !all {
		fmt.Fprintf(w, "hint: %s\n", h)
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
	doctorChecks(&rep, env, lookup)
	if jsonOut {
		writeJSON(stdout, rep)
	} else {
		rep.writeText(stdout)
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
func doctorChecks(rep *doctorReport, env func(string) string, lookup config.Lookup) {
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
		rep.add("server", stateFail, why, "mashctl status")
		return
	}
	rep.add("server", stateOK, maskURL(servers[0]), "")

	// 3. sign-in: the admin credentials connect
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
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
	doctorKeys(rep, ctx, adm, cfg, env)
}

// probeServer connects to one server, with the TLS settings of the configuration and no credentials. A server that
// answers, or that refuses the connection for want of credentials, is up; the reason a server is not is returned.
func probeServer(cfg config.Config, server string) string {
	tlsReq, warning, err := natsauth.RequireTLS(server, cfg.InsecurePlaintext)
	if err != nil {
		return oneLine(err.Error())
	}
	if why := probe(cfg, server, tlsReq); why != "" {
		if warning != "" { // the sign-in step says it when the server answers
			fmt.Fprintln(os.Stderr, warning)
		}
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
}

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
		why := strings.Join(unreconciled, "; ")
		if sc.Incomplete != "" {
			why = sc.Incomplete + "; " + why
		}
		sc.Incomplete = why
	}
	return sc, nil
}

// connectedFindings sorts the connected keys against the issue record: the keys no record holds, and the keys the
// record holds as revoked. No key is exempt: the admin user is in the account and init records it, and the system
// account's users are never in the mash account's connection list.
func connectedFindings(conns []apply.ConnzServer, recs []store.Issued) (unrecorded, revoked []string) {
	recorded := map[string]store.Issued{}
	for _, r := range recs {
		recorded[r.PublicKey] = r
	}
	seenU, seenR := map[string]bool{}, map[string]bool{}
	for _, s := range conns {
		for _, c := range s.Conns {
			if c.Key == "" {
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
	return unrecorded, revoked
}

// doctorKeys is the keys check: every key connected to the account was issued here and none that was revoked is still
// connected. It needs the system account credentials; without them it warns and checks nothing.
func doctorKeys(rep *doctorReport, ctx context.Context, adm *admin.Admin, cfg config.Config, env func(string) string) {
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
	recs, _, err := adm.Issued(ctx)
	if err != nil {
		failKeys(err.Error())
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
	scan, err := scanConnections(ctx, nc, account, serverCount(cfg))
	if err != nil {
		failKeys(err.Error())
		return
	}
	unrecorded, revoked := connectedFindings(scan.Conns, recs)
	var fails []finding
	const fix = "mashctl endpoint list"
	if n := len(unrecorded); n > 0 {
		fails = append(fails, finding{fmt.Sprintf("%s not issued here", count(n, "connected key")+" "+plural(n, "was", "were")), fix})
	}
	if n := len(revoked); n > 0 {
		fails = append(fails, finding{fmt.Sprintf("%s still connected", count(n, "revoked key")+" "+plural(n, "is", "are")), fix})
	}
	if scan.Incomplete != "" {
		fails = append(fails, finding{oneLine(scan.Incomplete), initFix})
	}
	addFindings(rep, "keys", "every connected key was issued here", fails, nil)
}
