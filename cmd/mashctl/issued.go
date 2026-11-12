package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/mint"
	"github.com/bogworks/mash/internal/store"
)

const issuedUsage = `usage: mashctl issued list [--json]
       mashctl issued check [--json] [--sys-creds FILE] [--servers N]
`

// The exit codes of `mashctl issued check`: 0 no unrecorded key and no revoked key is connected; 1 at least one is;
// 2 the check could not be made (usage, a connection, a server that did not answer).
const (
	issuedClean    = 0
	issuedFinding  = 1
	issuedNoAnswer = 2
)

// issuedOf is the issue record's entry for a user mint just made: public information only. The issue time is the
// JWT's own iat.
func issuedOf(m *mint.Minted, owner, purpose string) store.Issued {
	r := store.Issued{PublicKey: m.UserPub, Address: m.Name, Owner: owner, Zone: m.Zone, Kind: store.IssuedEndpoint,
		Tags: append([]string{}, m.Tags...), Purpose: purpose}
	if uc, err := jwt.DecodeUserClaims(m.JWT); err == nil && uc.IssuedAt > 0 {
		r.IssuedAt = time.Unix(uc.IssuedAt, 0).UTC().Format(time.RFC3339)
	}
	return r
}

type issuedOpts struct {
	json     bool
	sysCreds string
	servers  string
}

func parseIssuedArgs(verb string, args []string) (issuedOpts, error) {
	var o issuedOpts
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, inline, hasInline := strings.Cut(a, "=")
		switch {
		case name == "--json":
			if hasInline {
				return o, fmt.Errorf("unexpected %s", config.RejectedArg(a))
			}
			o.json = true
		case verb == "check" && (name == "--sys-creds" || name == "--servers"):
			var v string
			switch {
			case hasInline && inline != "":
				v = inline
			case !hasInline && i+1 < len(args) && args[i+1] != "":
				i++
				v = args[i]
			default:
				return o, fmt.Errorf("%s needs a value", name)
			}
			if name == "--sys-creds" {
				o.sysCreds = v
			} else {
				o.servers = v
			}
		default:
			return o, fmt.Errorf("unexpected %s", config.RejectedArg(a))
		}
	}
	return o, nil
}

// issuedList is what `mashctl issued list --json` prints.
type issuedList struct {
	Present bool           `json:"present"`
	Count   int            `json:"count"`
	Issued  []store.Issued `json:"issued"`
}

// runIssued is `mashctl issued`: list reads the issue record, check compares it with what is connected.
func runIssued(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "list" && args[0] != "check") {
		fmt.Fprint(stderr, "mashctl: issued: list or check\n"+issuedUsage)
		return 2
	}
	verb := args[0]
	o, err := parseIssuedArgs(verb, args[1:])
	if err != nil {
		fmt.Fprintf(stderr, "mashctl: issued %s: %v\n%s", verb, err, issuedUsage)
		return 2
	}
	servers := 0
	if verb == "check" {
		sv, src := o.servers, "--servers"
		if sv == "" {
			sv, src = env("MASH_SERVERS"), "MASH_SERVERS"
		}
		if sv != "" {
			if servers, err = strconv.Atoi(strings.TrimSpace(sv)); err != nil || servers < 1 {
				fmt.Fprintf(stderr, "mashctl: issued check: %s must be a whole number of servers, 1 or more, got %q\n%s", src, sv, issuedUsage)
				return 2
			}
		}
	}
	cfg, err := loadCtlConfig(env, lookup, true)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "mashctl: issued %s: %s\n", verb, fmt.Sprintf(format, a...))
		return 2
	}
	var sys *sysTarget
	if verb == "check" {
		sys = &sysTarget{o.sysCreds}
	}
	announceTarget(cfg, env, stderr, sys)
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		return fail("%v", err)
	}
	defer closeAdmin()
	recs, present, err := adm.Issued(ctx)
	if err != nil {
		return fail("%v", err)
	}
	if verb == "list" {
		return printIssuedList(recs, present, o.json, stdout)
	}
	return runIssuedCheck(ctx, cfg, env, o, servers, recs, present, stdout, stderr)
}

func printIssuedList(recs []store.Issued, present, asJSON bool, stdout io.Writer) int {
	if recs == nil {
		recs = []store.Issued{}
	}
	recs = slices.Clone(recs)
	for i := range recs {
		recs[i].Purpose = recs[i].EffectivePurpose() // an old record with no purpose is an ordinary issue
	}
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(issuedList{Present: present, Count: len(recs), Issued: recs})
		return 0
	}
	if !present {
		fmt.Fprintf(stdout, "no issue record yet (bucket %s): run `mashctl init`; nothing has been recorded\n", store.IssuedBucket)
		return 0
	}
	for _, r := range recs {
		line := fmt.Sprintf("%s\t%s\t%s\t%s\tgeneration %d\ttags %s\tpurpose %s", r.PublicKey, config.Clean(r.Kind), config.Clean(r.Address), config.Clean(r.IssuedAt), r.Generation, config.Clean(strings.Join(r.Tags, ",")), config.Clean(r.EffectivePurpose()))
		if r.RevokedAt != "" {
			line += "\trevoked " + config.Clean(r.RevokedAt)
		}
		fmt.Fprintln(stdout, line)
	}
	fmt.Fprintf(stdout, "%d issued\n", len(recs))
	return 0
}

// issuedKnown is a user mash itself uses, taken as recorded though the record may not hold it: the admin user the
// command connected with and the system account user it queries with, read from their creds files' JWTs.
type issuedKnown struct {
	PublicKey string `json:"public_key"`
	Source    string `json:"source"`
}

// issuedFinding is a connected key the record does not hold.
type issuedFindingOut struct {
	Server    string   `json:"server"`
	Account   string   `json:"account"`
	PublicKey string   `json:"public_key"`
	Name      string   `json:"name,omitempty"`
	IssuerKey string   `json:"issuer_key,omitempty"`
	Tags      []string `json:"tags,omitempty"`
}

// issuedRevokedConn is a connected key the record holds as revoked: the revocation did not take effect.
type issuedRevokedConn struct {
	Server    string `json:"server"`
	Account   string `json:"account"`
	PublicKey string `json:"public_key"`
	Address   string `json:"address"`
	Kind      string `json:"kind"`
	Purpose   string `json:"purpose"`
	RevokedAt string `json:"revoked_at"`
}

type issuedNotConnected struct {
	PublicKey string `json:"public_key"`
	Address   string `json:"address"`
	Kind      string `json:"kind"`
}

type issuedServers struct {
	Answered []string `json:"answered"`
	Missing  []string `json:"missing"`
	Expected int      `json:"expected"`
}

type issuedSummary struct {
	Connected      int `json:"connected"`
	Recorded       int `json:"recorded"`
	Unrecorded     int `json:"unrecorded"`
	RevokedConn    int `json:"revoked_connected"`
	NotConnected   int `json:"recorded_not_connected"`
	KnownMashUsers int `json:"known_mash_users"`
}

// issuedCheck is what `mashctl issued check --json` prints. The slices are never nil, so the JSON is stable.
type issuedCheck struct {
	// Clean is true when no unrecorded key and no revoked key is connected and every server answered.
	Clean         bool                 `json:"clean"`
	Complete      bool                 `json:"complete"`
	Incomplete    string               `json:"incomplete,omitempty"`
	Account       string               `json:"account"`
	RecordPresent bool                 `json:"record_present"`
	Servers       issuedServers        `json:"servers"`
	Unrecorded    []issuedFindingOut   `json:"unrecorded"`
	RevokedConn   []issuedRevokedConn  `json:"revoked_connected"`
	NotConnected  []issuedNotConnected `json:"recorded_not_connected"`
	Known         []issuedKnown        `json:"known_mash_users"`
	Summary       issuedSummary        `json:"summary"`
}

// issuedCredsKey is the public key of the user in the creds file at path, read from its JWT; the seed is never read.
func issuedCredsKey(path string) (string, error) {
	raw, err := config.ReadCredsFile(path)
	if err != nil {
		return "", err
	}
	defer clear(raw)
	tok, err := jwt.ParseDecoratedJWT(raw)
	if err != nil {
		return "", errors.New("no readable JWT")
	}
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil {
		return "", errors.New("no readable user JWT")
	}
	return uc.Subject, nil
}

func runIssuedCheck(ctx context.Context, cfg config.Config, env func(string) string, o issuedOpts, servers int, recs []store.Issued, present bool, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "mashctl: issued check: %s\n", fmt.Sprintf(format, a...))
		return issuedNoAnswer
	}
	keys, err := openKeys(cfg, env)
	if err != nil {
		return fail("%v", err)
	}
	akp, account, err := keys.Account()
	if err != nil {
		return fail("%v", err)
	}
	akp.Wipe()
	nc, err := connectSys(cfg, o.sysCreds, env)
	if err != nil {
		return fail("%v", err)
	}
	defer nc.Close()
	ctx, cancel := context.WithTimeout(ctx, applyDeadline)
	defer cancel()
	res := apply.NATS{Conn: nc, Inventory: servers}
	set, err := res.Servers(ctx)
	if err != nil {
		return fail("%v", err)
	}
	conns, cerr := res.Connz(ctx, account, set.Names, 0)
	if cerr != nil {
		return fail("%v", cerr)
	}

	rep := issuedCheck{Account: account, RecordPresent: present, Unrecorded: []issuedFindingOut{}, RevokedConn: []issuedRevokedConn{}, NotConnected: []issuedNotConnected{}, Known: []issuedKnown{}}
	rep.Servers = issuedServers{Answered: []string{}, Missing: []string{}, Expected: max(set.Expected, servers, len(set.Names))}
	answered := map[string]bool{}
	for _, s := range conns {
		answered[s.Name] = true
		rep.Servers.Answered = append(rep.Servers.Answered, s.Name)
	}
	for _, n := range set.Names {
		if !answered[n] {
			rep.Servers.Missing = append(rep.Servers.Missing, n)
		}
	}
	switch {
	case !set.Complete(servers):
		rep.Incomplete = set.Why(servers)
	case len(rep.Servers.Missing) > 0:
		rep.Incomplete = fmt.Sprintf("%d server(s) answered the ping but not the connection list: %s", len(rep.Servers.Missing), config.Clean(strings.Join(rep.Servers.Missing, ", ")))
	}
	rep.Complete = rep.Incomplete == ""

	recorded := map[string]store.Issued{}
	for _, r := range recs {
		recorded[r.PublicKey] = r
	}
	known := map[string]bool{}
	addKnown := func(path, source string) {
		if path == "" {
			return
		}
		if k, err := issuedCredsKey(path); err == nil && k != "" {
			if !known[k] {
				known[k] = true
				rep.Known = append(rep.Known, issuedKnown{PublicKey: k, Source: source})
			}
		}
	}
	addKnown(cfg.AdminCreds, "admin creds of this command")
	if p, err := sysCredsPath(o.sysCreds, env, cfg); err == nil {
		addKnown(p, "system account creds of this command")
	}

	connected := map[string]bool{}
	for _, s := range conns {
		for _, c := range s.Conns {
			if c.Key == "" {
				continue
			}
			connected[c.Key] = true
			rep.Summary.Connected++
			if r, ok := recorded[c.Key]; ok {
				if r.RevokedAt != "" {
					rep.RevokedConn = append(rep.RevokedConn, issuedRevokedConn{Server: s.Name, Account: c.Account, PublicKey: c.Key,
						Address: r.Address, Kind: r.Kind, Purpose: r.EffectivePurpose(), RevokedAt: r.RevokedAt})
				}
				continue
			}
			if known[c.Key] {
				continue
			}
			rep.Unrecorded = append(rep.Unrecorded, issuedFindingOut{Server: s.Name, Account: c.Account, PublicKey: c.Key,
				Name: c.Name, IssuerKey: c.IssuerKey, Tags: c.Tags})
		}
	}
	for _, r := range recs {
		if !connected[r.PublicKey] {
			rep.NotConnected = append(rep.NotConnected, issuedNotConnected{PublicKey: r.PublicKey, Address: r.Address, Kind: r.Kind})
		}
	}
	slices.SortFunc(rep.Unrecorded, func(a, b issuedFindingOut) int {
		if c := strings.Compare(a.Server, b.Server); c != 0 {
			return c
		}
		return strings.Compare(a.PublicKey, b.PublicKey)
	})
	slices.SortFunc(rep.RevokedConn, func(a, b issuedRevokedConn) int {
		if c := strings.Compare(a.Server, b.Server); c != 0 {
			return c
		}
		return strings.Compare(a.PublicKey, b.PublicKey)
	})
	rep.Summary.RevokedConn = len(rep.RevokedConn)
	rep.Summary.Recorded = len(recs)
	rep.Summary.Unrecorded = len(rep.Unrecorded)
	rep.Summary.NotConnected = len(rep.NotConnected)
	rep.Summary.KnownMashUsers = len(rep.Known)
	rep.Clean = len(rep.Unrecorded) == 0 && len(rep.RevokedConn) == 0 && rep.Complete

	code := issuedClean
	switch {
	case len(rep.Unrecorded) > 0 || len(rep.RevokedConn) > 0:
		code = issuedFinding
	case !rep.Complete:
		code = issuedNoAnswer
	}
	if o.json {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
		return code
	}
	printIssuedCheck(rep, stdout)
	return code
}

func printIssuedCheck(rep issuedCheck, w io.Writer) {
	if !rep.RecordPresent {
		fmt.Fprintf(w, "note: there is no issue record yet (bucket %s): run `mashctl init`; every connected key is reported\n", store.IssuedBucket)
	}
	for _, f := range rep.Unrecorded {
		line := fmt.Sprintf("UNRECORDED key %s: connected to server %s in account %s", f.PublicKey, config.Clean(f.Server), config.Clean(f.Account))
		if f.Name != "" {
			line += fmt.Sprintf(", client name %q", config.Clean(f.Name))
		}
		if f.IssuerKey != "" {
			line += ", signed by " + config.Clean(f.IssuerKey)
		}
		if len(f.Tags) > 0 {
			line += ", tags " + config.Clean(strings.Join(f.Tags, ","))
		}
		fmt.Fprintln(w, line+": mash did not issue it")
	}
	for _, f := range rep.RevokedConn {
		fmt.Fprintf(w, "REVOKED key %s still connected: %s %s (purpose %s) is connected to server %s in account %s, revoked at %s: the revocation did not take effect\n",
			f.PublicKey, config.Clean(f.Kind), config.Clean(f.Address), config.Clean(f.Purpose), config.Clean(f.Server), config.Clean(f.Account), config.Clean(f.RevokedAt))
	}
	if len(rep.NotConnected) > 0 {
		fmt.Fprintf(w, "recorded but not connected (informational, %d):\n", len(rep.NotConnected))
		for _, n := range rep.NotConnected {
			fmt.Fprintf(w, "  %s\t%s\t%s\n", n.PublicKey, config.Clean(n.Kind), config.Clean(n.Address))
		}
	}
	if !rep.Complete {
		fmt.Fprintf(w, "INCOMPLETE: %s; keys connected to a server that did not answer were not checked\n", rep.Incomplete)
	}
	fmt.Fprintf(w, "summary: %d connected, %d unrecorded, %d revoked but connected, %d recorded (%d not connected), %d known mash users, %d of %d servers answered\n",
		rep.Summary.Connected, rep.Summary.Unrecorded, rep.Summary.RevokedConn, rep.Summary.Recorded, rep.Summary.NotConnected, rep.Summary.KnownMashUsers,
		len(rep.Servers.Answered), rep.Servers.Expected)
	if rep.Clean {
		fmt.Fprintln(w, "ok: every connected key was issued by mash and none is revoked")
	}
}
