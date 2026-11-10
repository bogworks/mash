package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/sap"
)

const auditUsage = "usage: mashctl audit [--json] [--strict] [--creds-dir DIR]\n"

// auditVersion is the version field of the JSON report. It changes only when a field is removed or changes meaning.
const auditVersion = 1

// trustLabelNA is the trust_label of every renamed identity: the server holds no trust label (the receiving endpoint
// evaluates one from the signed sender header when it reads a message).
const (
	trustLabelNA   = "not-available"
	trustLabelNote = "the server holds no trust label: the receiving endpoint evaluates it when it reads a message, so compare the message counts and last sequences here and check labels at the endpoint"
)

// auditNow is the clock for the report's generated field; a test replaces it.
var auditNow = time.Now

type auditIdentity struct {
	Agent string `json:"agent"`
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

type auditAlias struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

type auditDeliver struct {
	Present    bool   `json:"present"`
	Pending    uint64 `json:"pending"`
	AckPending int    `json:"ack_pending"`

	DeliverPolicy  string   `json:"deliver_policy,omitempty"`
	StartSeq       uint64   `json:"start_seq"`
	StartTime      string   `json:"start_time,omitempty"`
	FilterSubjects []string `json:"filter_subjects"`
	AckFloor       uint64   `json:"ack_floor"`
	Delivered      uint64   `json:"delivered"`
}

type auditInbox struct {
	Stream    string       `json:"stream"`
	Present   bool         `json:"present"`
	Msgs      uint64       `json:"msgs"`
	FirstSeq  uint64       `json:"first_seq"`
	LastSeq   uint64       `json:"last_seq"`
	Subjects  []string     `json:"subjects"`
	Suspended bool         `json:"suspended"`
	Deliver   auditDeliver `json:"deliver"`
}

type auditRecord struct {
	Key      string        `json:"key"`
	Identity auditIdentity `json:"identity"`
	Flat     string        `json:"flat"`
	Form     string        `json:"form"` // legacy or new
	User     string        `json:"user"`
	Zone     string        `json:"zone,omitempty"`
	Alias    *auditAlias   `json:"alias,omitempty"`
	Running  bool          `json:"running"`
	Inbox    auditInbox    `json:"inbox"`
}

type auditRename struct {
	Identity       string     `json:"identity"` // agent.owner.name
	Flat           string     `json:"flat"`
	Alias          auditAlias `json:"alias"`
	LegacyInbox    auditInbox `json:"legacy_inbox"`
	NewInbox       auditInbox `json:"new_inbox"`
	Drained        bool       `json:"drained"`
	TrustLabel     string     `json:"trust_label"`
	TrustLabelNote string     `json:"trust_label_note"`
}

type auditOrphanStream struct {
	Stream    string   `json:"stream"`
	Msgs      uint64   `json:"msgs"`
	FirstSeq  uint64   `json:"first_seq"`
	LastSeq   uint64   `json:"last_seq"`
	Subjects  []string `json:"subjects"`
	Suspended bool     `json:"suspended"`
}

type auditOrphanLease struct {
	Key string `json:"key"`
}

type auditOrphanCreds struct {
	File string `json:"file"`
	User string `json:"user"`
}

type auditOrphans struct {
	Streams []auditOrphanStream `json:"streams"`
	Leases  []auditOrphanLease  `json:"leases"`
	Creds   []auditOrphanCreds  `json:"creds"`
}

type auditProblem struct {
	What  string `json:"what"` // record or creds
	Item  string `json:"item"` // the directory key or the file
	Error string `json:"error"`
}

type auditSummary struct {
	Records       int `json:"records"`
	Legacy        int `json:"legacy"`
	New           int `json:"new"`
	Running       int `json:"running"`
	Renamed       int `json:"renamed"`
	Undrained     int `json:"undrained"`
	OrphanStreams int `json:"orphan_streams"`
	OrphanLeases  int `json:"orphan_leases"`
	OrphanCreds   int `json:"orphan_creds"`
	Problems      int `json:"problems"`
}

// auditReport is what `mashctl audit --json` prints.
type auditReport struct {
	Version     int            `json:"version"`
	Generated   string         `json:"generated"`
	Owner       string         `json:"owner"`
	CredsDirs   []string       `json:"creds_dirs"`
	Records     []auditRecord  `json:"records"`
	Renames     []auditRename  `json:"renames"`
	Orphans     auditOrphans   `json:"orphans"`
	FencedLease []string       `json:"fenced_leases"` // lease keys holding a deprovision tombstone and no record: expected, they expire
	Problems    []auditProblem `json:"problems"`
	Summary     auditSummary   `json:"summary"`
}

// findings is how many things --strict counts: every orphan, every undrained alias and every item the audit could not
// read (it cannot call what it did not read clean).
func (r auditReport) findings() int {
	s := r.Summary
	return s.OrphanStreams + s.OrphanLeases + s.OrphanCreds + s.Undrained + s.Problems
}

func toAuditInbox(name string, st *admin.AuditStream) auditInbox {
	in := auditInbox{Stream: sap.InboxStream(name), Subjects: []string{}}
	if st == nil {
		return in
	}
	in.Present, in.Msgs, in.FirstSeq, in.LastSeq, in.Suspended = true, st.Msgs, st.FirstSeq, st.LastSeq, st.Suspended
	in.Subjects = append(in.Subjects, st.Subjects...)
	if st.Deliver != nil {
		d := st.Deliver
		in.Deliver = auditDeliver{Present: true, Pending: d.Pending, AckPending: d.AckPending, DeliverPolicy: d.DeliverPolicy,
			StartSeq: d.StartSeq, StartTime: d.StartTime, FilterSubjects: append([]string{}, d.FilterSubjects...),
			AckFloor: d.AckFloor, Delivered: d.Delivered}
	}
	return in
}

// drained reports whether a legacy inbox can be cut off. An inbox that is gone, or one that holds no message, is
// drained. Otherwise its `deliver` consumer must have nothing pending and nothing unacknowledged, and must be
// configured to have delivered every stored message: deliver policy all (or a start sequence at or below the first
// stored sequence), no filter narrower than the stream's subjects, and an ack floor that reaches the last stored
// sequence. The counts alone are not enough: a consumer recreated to skip stored messages shows zero for both.
func (in auditInbox) drained() bool {
	if !in.Present || in.Msgs == 0 {
		return true
	}
	d := in.Deliver
	if !d.Present || d.Pending != 0 || d.AckPending != 0 {
		return false
	}
	switch d.DeliverPolicy {
	case "all":
	case "by_start_sequence":
		if d.StartSeq > in.FirstSeq {
			return false
		}
	default: // new, last, last_per_subject, by_start_time (a time cannot be compared with the stored messages), unknown
		return false
	}
	if len(d.FilterSubjects) > 0 && !slices.Equal(d.FilterSubjects, in.Subjects) {
		return false
	}
	return d.AckFloor >= in.LastSeq
}

// buildAudit combines the server snapshot with the creds files' public user keys. credsUsers maps a creds file to its
// user public key (or to an error); skipUsers are keys that are not endpoints' (the admin's).
func buildAudit(owner string, snap admin.AuditSnapshot, dirs []string, creds []credsFile, skipUsers map[string]bool) auditReport {
	rep := auditReport{Version: auditVersion, Owner: owner, CredsDirs: dirs,
		Records: []auditRecord{}, Renames: []auditRename{}, FencedLease: []string{}, Problems: []auditProblem{},
		Orphans: auditOrphans{Streams: []auditOrphanStream{}, Leases: []auditOrphanLease{}, Creds: []auditOrphanCreds{}}}
	streams := map[string]*admin.AuditStream{}
	for i := range snap.Streams {
		streams[snap.Streams[i].Name] = &snap.Streams[i]
	}
	live := map[string]bool{}
	for _, l := range snap.Leases {
		if !l.Tombstone {
			live[l.Key] = true
		}
	}
	claimedStream, claimedLease, users := map[string]bool{}, map[string]bool{}, map[string]bool{}

	for _, b := range snap.BadRecords {
		rep.Problems = append(rep.Problems, auditProblem{What: "record", Item: b.Key, Error: b.Error})
		// the entry still exists: what it names is not an orphan, though the entry itself is not trusted
		claimedStream[sap.InboxStream(b.Key)], claimedLease[b.Key] = true, true
		if b.User != "" {
			users[b.User] = true
		}
	}
	for _, r := range snap.Records {
		id, err := r.Identity()
		if err != nil {
			rep.Problems = append(rep.Problems, auditProblem{What: "record", Item: r.KeyOrName(), Error: err.Error()})
			k := r.KeyOrName()
			claimedStream[sap.InboxStream(k)], claimedLease[k] = true, true
			if r.User != "" {
				users[r.User] = true
			}
			continue
		}
		key := id.Flat()
		claimedStream[sap.InboxStream(key)], claimedLease[key], users[r.User] = true, true, true
		form := "new"
		if id.IsLegacy() {
			form = "legacy"
		}
		ar := auditRecord{Key: key, Identity: auditIdentity{Agent: id.Agent, Owner: id.Owner, Name: id.Name}, Flat: key,
			Form: form, User: r.User, Zone: r.Zone, Running: live[key], Inbox: toAuditInbox(key, streams[sap.InboxStream(key)])}
		if r.Alias != nil {
			ar.Alias = &auditAlias{Owner: r.Alias.Owner, Name: r.Alias.Name}
			if aid, ok, err := r.AliasIdentity(); err == nil && ok {
				legacy := aid.Flat()
				claimedStream[sap.InboxStream(legacy)] = true
				rn := auditRename{Identity: id.String(), Flat: key, Alias: *ar.Alias,
					LegacyInbox: toAuditInbox(legacy, streams[sap.InboxStream(legacy)]), NewInbox: ar.Inbox,
					TrustLabel: trustLabelNA, TrustLabelNote: trustLabelNote}
				rn.Drained = rn.LegacyInbox.drained()
				rep.Renames = append(rep.Renames, rn)
			}
		}
		rep.Records = append(rep.Records, ar)
	}
	slices.SortFunc(rep.Records, func(a, b auditRecord) int { return strings.Compare(a.Key, b.Key) })
	slices.SortFunc(rep.Renames, func(a, b auditRename) int { return strings.Compare(a.Flat, b.Flat) })

	for _, st := range snap.Streams {
		if !claimedStream[st.Name] {
			rep.Orphans.Streams = append(rep.Orphans.Streams, auditOrphanStream{Stream: st.Name, Msgs: st.Msgs, FirstSeq: st.FirstSeq,
				LastSeq: st.LastSeq, Subjects: append([]string{}, st.Subjects...), Suspended: st.Suspended})
		}
	}
	for _, l := range snap.Leases {
		switch {
		case claimedLease[l.Key]:
		case l.Tombstone:
			rep.FencedLease = append(rep.FencedLease, l.Key)
		default:
			rep.Orphans.Leases = append(rep.Orphans.Leases, auditOrphanLease{Key: l.Key})
		}
	}
	for _, c := range creds {
		switch {
		case c.err != nil:
			rep.Problems = append(rep.Problems, auditProblem{What: "creds", Item: c.path, Error: c.err.Error()})
		case users[c.user] || skipUsers[c.user]:
		default:
			rep.Orphans.Creds = append(rep.Orphans.Creds, auditOrphanCreds{File: c.path, User: c.user})
		}
	}
	slices.SortFunc(rep.Orphans.Creds, func(a, b auditOrphanCreds) int { return strings.Compare(a.File, b.File) })
	slices.SortFunc(rep.Problems, func(a, b auditProblem) int {
		return strings.Compare(a.What+"\x00"+a.Item, b.What+"\x00"+b.Item)
	})

	s := &rep.Summary
	s.Records, s.Renamed = len(rep.Records), len(rep.Renames)
	for _, r := range rep.Records {
		if r.Form == "legacy" {
			s.Legacy++
		} else {
			s.New++
		}
		if r.Running {
			s.Running++
		}
	}
	for _, r := range rep.Renames {
		if !r.Drained {
			s.Undrained++
		}
	}
	s.OrphanStreams, s.OrphanLeases, s.OrphanCreds, s.Problems = len(rep.Orphans.Streams), len(rep.Orphans.Leases), len(rep.Orphans.Creds), len(rep.Problems)
	return rep
}

// credsFile is one creds file found in a managed directory: its public user key, or why it could not be read.
type credsFile struct {
	path string
	user string
	err  error
}

// scanCreds reads the user public key of every regular *.creds file in dirs (not the staged .next files, not
// symbolic links). Only the JWT's subject is read: the seed and the token are never kept or shown. A directory that
// does not exist is skipped when optional, else an error.
func scanCreds(dirs []string, optional bool) ([]credsFile, error) {
	var out []credsFile
	for _, d := range dirs {
		ents, err := os.ReadDir(d)
		if errors.Is(err, fs.ErrNotExist) && optional {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("mashctl: audit: creds directory: %w", err)
		}
		for _, e := range ents {
			if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".creds") {
				continue
			}
			p := filepath.Join(d, e.Name())
			u, err := credsUserKey(p)
			if err != nil {
				// the error may name a path and a parse failure, never the file's content
				out = append(out, credsFile{path: p, err: errors.New(config.Clean(strings.TrimPrefix(err.Error(), p+": ")))})
				continue
			}
			out = append(out, credsFile{path: p, user: u})
		}
	}
	return out, nil
}

type auditOpts struct {
	json, strict bool
	credsDir     string
}

func parseAuditArgs(args []string) (auditOpts, error) {
	var o auditOpts
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, inline, hasInline := strings.Cut(a, "=")
		switch name {
		case "--json", "--strict":
			if hasInline {
				return o, fmt.Errorf("unexpected %s", config.RejectedArg(a))
			}
			if name == "--json" {
				o.json = true
			} else {
				o.strict = true
			}
		case "--creds-dir":
			switch {
			case hasInline && inline != "":
				o.credsDir = inline
			case !hasInline && i+1 < len(args) && args[i+1] != "":
				i++
				o.credsDir = args[i]
			default:
				return o, errors.New("--creds-dir needs a value")
			}
		default:
			return o, fmt.Errorf("unexpected %s", config.RejectedArg(a))
		}
	}
	return o, nil
}

// runAudit is `mashctl audit`: a read-only listing of mash's side of the population. It never writes to the server or
// to disk.
func runAudit(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	o, err := parseAuditArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl: audit: %v\n%s", err, auditUsage)
		return 2
	}
	// audit changes nothing and acts as no endpoint, so a set agent_token (which makes the writing verbs refuse) is no
	// reason to refuse it.
	cfg, err := loadConfig(env, lookup)
	if err != nil && !errors.Is(err, errAgentTokenSet) {
		fmt.Fprintln(stderr, err)
		return 1
	}
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer closeAdmin()
	snap, err := adm.Audit(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	dirs, optional := []string{}, true
	if o.credsDir != "" {
		dirs, optional = []string{filepath.Clean(o.credsDir)}, false
	} else {
		for _, d := range []string{cfg.Dir, cfg.Legacy} {
			if d != "" && !slices.Contains(dirs, filepath.Clean(d)) {
				dirs = append(dirs, filepath.Clean(d))
			}
		}
	}
	creds, err := scanCreds(dirs, optional)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	skip := map[string]bool{} // the admin's and the system account's creds are in the same directory but are no endpoint's
	for _, p := range []string{cfg.AdminCreds, env("MASH_SYS_CREDS")} {
		if p == "" {
			continue
		}
		if u, err := credsUserKey(p); err == nil {
			skip[u] = true
		}
	}
	rep := buildAudit(cfg.Owner, snap, dirs, creds, skip)
	rep.Generated = auditNow().UTC().Format(time.RFC3339)

	if o.json {
		b, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "%s\n", b)
	} else {
		writeAuditText(stdout, rep)
	}
	if o.strict && rep.findings() > 0 {
		s := rep.Summary
		fmt.Fprintf(stderr, "mashctl: audit --strict: %d finding(s): %d orphan stream(s), %d orphan lease(s), %d orphan creds file(s), %d alias inbox(es) not drained, %d item(s) not readable\n",
			rep.findings(), s.OrphanStreams, s.OrphanLeases, s.OrphanCreds, s.Undrained, s.Problems)
		return 1
	}
	return 0
}

func inboxText(in auditInbox) string {
	if !in.Present {
		return fmt.Sprintf("stream %s\tabsent", config.Clean(in.Stream))
	}
	d := in.Deliver
	consumer := "deliver false"
	if d.Present {
		filter := "none"
		if len(d.FilterSubjects) > 0 {
			filter = strings.Join(d.FilterSubjects, ",")
		}
		consumer = fmt.Sprintf("deliver true\tpolicy %s\tstart_seq %d\tfilter %s\tack_floor %d", config.Clean(d.DeliverPolicy), d.StartSeq, config.Clean(filter), d.AckFloor)
		if d.StartTime != "" {
			consumer += "\tstart_time " + config.Clean(d.StartTime)
		}
	}
	return fmt.Sprintf("stream %s\tmsgs %d\tfirst %d\tlast %d\tsuspended %t\tpending %d\tack_pending %d\t%s\tsubjects %s", config.Clean(in.Stream), in.Msgs, in.FirstSeq, in.LastSeq,
		in.Suspended, d.Pending, d.AckPending, consumer, config.Clean(strings.Join(in.Subjects, ",")))
}

// writeAuditText is the human form: the same facts as the JSON, one tab-separated line each.
func writeAuditText(w io.Writer, r auditReport) {
	c := config.Clean
	fmt.Fprintf(w, "mashctl audit version %d generated %s owner %s\n", r.Version, r.Generated, c(r.Owner))
	fmt.Fprintf(w, "creds directories: %s\n", c(strings.Join(r.CredsDirs, ", ")))
	fmt.Fprintf(w, "records %d (legacy %d, new %d), running %d\n", r.Summary.Records, r.Summary.Legacy, r.Summary.New, r.Summary.Running)
	for _, x := range r.Records {
		state := "stopped"
		if x.Running {
			state = "running"
		}
		alias := ""
		if x.Alias != nil {
			alias = "\talias " + c(x.Alias.Owner) + "/" + c(x.Alias.Name)
		}
		fmt.Fprintf(w, "record %s\t%s\t%s\tidentity %s.%s.%s\tuser %s%s\t%s\n", c(x.Key), x.Form, state, c(x.Identity.Agent), c(x.Identity.Owner), c(x.Identity.Name), c(x.User), alias, inboxText(x.Inbox))
	}
	fmt.Fprintf(w, "renamed identities %d, not drained %d\n", r.Summary.Renamed, r.Summary.Undrained)
	for _, x := range r.Renames {
		drained := "drained"
		if !x.Drained {
			drained = "NOT drained"
		}
		fmt.Fprintf(w, "rename %s\t%s\ttrust_label %s\n", c(x.Identity), drained, x.TrustLabel)
		fmt.Fprintf(w, "  before (legacy %s/%s)\t%s\n", c(x.Alias.Owner), c(x.Alias.Name), inboxText(x.LegacyInbox))
		fmt.Fprintf(w, "  after\t%s\n", inboxText(x.NewInbox))
	}
	if r.Summary.Renamed > 0 {
		fmt.Fprintf(w, "trust_label: %s\n", trustLabelNote)
	}
	fmt.Fprintf(w, "orphans: %d stream(s), %d lease(s), %d creds file(s)\n", r.Summary.OrphanStreams, r.Summary.OrphanLeases, r.Summary.OrphanCreds)
	for _, x := range r.Orphans.Streams {
		fmt.Fprintf(w, "orphan stream %s\tmsgs %d\tfirst %d\tlast %d\tsuspended %t\tsubjects %s\n", c(x.Stream), x.Msgs, x.FirstSeq, x.LastSeq, x.Suspended, c(strings.Join(x.Subjects, ",")))
	}
	for _, x := range r.Orphans.Leases {
		fmt.Fprintf(w, "orphan lease %s\n", c(x.Key))
	}
	for _, x := range r.Orphans.Creds {
		fmt.Fprintf(w, "orphan creds %s\tuser %s\n", c(x.File), c(x.User))
	}
	for _, k := range r.FencedLease {
		fmt.Fprintf(w, "fenced lease %s (a deprovision tombstone; it expires)\n", c(k))
	}
	for _, p := range r.Problems {
		fmt.Fprintf(w, "not readable %s %s: %s\n", p.What, c(p.Item), c(p.Error))
	}
}
