package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/credential"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/issue"
	"github.com/bogworks/mash/internal/policydirect"
)

const credentialUsage = `usage: mashctl credential key --out FILE
       mashctl credential install --seed FILE --jwt FILE --out CREDS [--force]
       mashctl credential issue (--address owner.name --key PUBKEY [--endpoint agent.owner.name] [--purpose issue|rotation] | --request FILE|-) [--out FILE [--force]] [--json] [--servers N] [--sys-creds FILE]
       mashctl credential show PUBKEY|owner.name [--json] [--servers N] [--sys-creds FILE]
       mashctl credential revoke PUBKEY [--json] [--servers N] [--sys-creds FILE]
`

const (
	credIssueUsage  = "usage: mashctl credential issue (--address owner.name --key PUBKEY [--endpoint agent.owner.name] [--purpose issue|rotation] | --request FILE|-) [--out FILE [--force]] [--json] [--servers N] [--sys-creds FILE]"
	credShowUsage   = "usage: mashctl credential show PUBKEY|owner.name [--json] [--servers N] [--sys-creds FILE]"
	credRevokeUsage = "usage: mashctl credential revoke PUBKEY [--json] [--servers N] [--sys-creds FILE]"
)

// The exit codes of the credential verbs, as credential.Result.ExitCode and ADR 0134 give them.
const (
	credOK      = 0
	credRefused = 1
	credUsage   = 2
	credError   = 6
)

// credStdin is what `--request -` reads; a test replaces it.
var credStdin io.Reader = os.Stdin

// credBackend is what issue, show and revoke hold of the deployment: the admin connection, and (for issue and show) the
// credential.Config, which needs the key store and the system-account connection. A test replaces openCredBackend.
type credBackend struct {
	Ctx   context.Context
	Admin *admin.Admin
	// Cred makes the credential.Config; it is not called by revoke.
	Cred  func() (credential.Config, error)
	Close func()
}

var openCredBackend = realCredBackend

func realCredBackend(cfg config.Config, env func(string) string, servers int) (*credBackend, error) {
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		return nil, err
	}
	b := &credBackend{Ctx: ctx, Admin: adm}
	var sys *nats.Conn
	b.Cred = func() (credential.Config, error) {
		if servers < 1 {
			return credential.Config{}, fmt.Errorf("the number of servers is not known: pass --servers N or set MASH_SERVERS")
		}
		ks, err := openKeys(cfg, env)
		if err != nil {
			return credential.Config{}, err
		}
		_, acct, err := ks.Account()
		if err != nil {
			return credential.Config{}, err
		}
		trust, err := apply.LoadTrust(ks)
		if err != nil {
			return credential.Config{}, err
		}
		if sys, err = connectSys(cfg, "", env); err != nil {
			return credential.Config{}, err
		}
		return credential.FromDirect(policydirect.Config{Account: acct, Admin: adm, Sys: sys, Keys: ks, Trust: trust, Servers: servers, URL: cfg.URL})
	}
	b.Close = func() {
		if sys != nil {
			sys.Close()
		}
		closeAdmin()
	}
	return b, nil
}

// credEnv is env with MASH_SYS_CREDS set to sysCreds when the flag was given, so the system-account reads that look at
// the environment (sysCredsPath) use the flag.
func credEnv(env func(string) string, sysCreds string) func(string) string {
	if sysCreds == "" {
		return env
	}
	return func(k string) string {
		if k == "MASH_SYS_CREDS" {
			return sysCreds
		}
		return env(k)
	}
}

// credServers is --servers, else MASH_SERVERS; 0 when neither is given.
func credServers(flagVal string, env func(string) string) (int, error) {
	if flagVal == "" {
		return parseServers(env)
	}
	n, err := strconv.Atoi(strings.TrimSpace(flagVal))
	if err != nil || n < 1 {
		return 0, fmt.Errorf("--servers must be a whole number of servers, 1 or more")
	}
	return n, nil
}

// runCredential is `mashctl credential`.
func runCredential(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, credentialUsage)
		return credUsage
	}
	rest := args[1:]
	switch args[0] {
	case "key":
		return credential.KeyCmd("mashctl", rest, stdout, stderr)
	case "install":
		return credential.InstallCmd("mashctl", rest, stdout, stderr)
	case "issue":
		return credIssue(rest, env, lookup, stdout, stderr)
	case "show":
		return credShow(rest, env, lookup, stdout, stderr)
	case "revoke":
		return credRevoke(rest, env, lookup, stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, credentialUsage)
		return credOK
	}
	fmt.Fprintf(stderr, "mashctl credential: unknown verb\n%s", credentialUsage)
	return credUsage
}

// issueOpts is what the flags of issue say.
type issueOpts struct {
	req      credential.Request
	request  string // --request FILE or -, "" when not given
	out      string
	force    bool
	json     bool
	servers  string
	sysCreds string
	// endpointGiven is whether --endpoint was on the command line.
	endpointGiven bool
}

// parseIssueArgs reads the flags of issue. The error is a usage error; it never carries a flag's value.
func parseIssueArgs(args []string) (o issueOpts, help bool, err error) {
	vals, set, help, err := credential.ParseFlags(args, []string{"--address", "--key", "--endpoint", "--purpose", "--request", "--out", "--servers", "--sys-creds"}, []string{"--json", "--force"})
	if help || err != nil {
		return o, help, err
	}
	o = issueOpts{
		req:     credential.Request{Address: vals["--address"], Endpoint: vals["--endpoint"], PublicKey: vals["--key"], Purpose: vals["--purpose"]},
		request: vals["--request"], out: vals["--out"], json: set["--json"], force: set["--force"], servers: vals["--servers"], sysCreds: vals["--sys-creds"],
		endpointGiven: vals["--endpoint"] != "",
	}
	if o.request != "" {
		for _, n := range []string{"--address", "--key", "--endpoint", "--purpose"} {
			if _, ok := vals[n]; ok {
				return o, false, fmt.Errorf("--request conflicts with %s", n)
			}
		}
		return o, false, nil
	}
	switch {
	case o.req.Address == "":
		return o, false, fmt.Errorf("--address is required (or --request)")
	case o.req.PublicKey == "":
		return o, false, fmt.Errorf("--key is required (or --request)")
	}
	return o, false, nil
}

// readRequest reads --request FILE or - to at most credential.MaxRequestBytes+1 bytes, so Parse says "too large".
func readRequest(path string) ([]byte, error) {
	var r io.Reader = credStdin
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("cannot read %s", path)
		}
		defer f.Close()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, credential.MaxRequestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read the request")
	}
	return b, nil
}

func credIssue(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	o, help, err := parseIssueArgs(args)
	if help {
		fmt.Fprintln(stdout, credIssueUsage)
		return credOK
	}
	if err != nil {
		fmt.Fprintf(stderr, "mashctl credential issue: %v\n%s\n", err, credIssueUsage)
		return credUsage
	}
	if o.out != "" && !o.force {
		if _, err := os.Lstat(o.out); err == nil {
			fmt.Fprintf(stderr, "mashctl credential issue: %s exists; pass --force to overwrite\n", o.out)
			return credUsage
		}
	}
	// A seed in any input is refused before anything connects, and is never echoed.
	refuse := func(rf *credential.Refusal) int {
		return emitIssue(credential.Result{Outcome: credential.OutcomeRefused, Refusal: rf}, o, stdout, stderr)
	}
	var req credential.Request
	if o.request != "" {
		data, err := readRequest(o.request)
		if err != nil {
			fmt.Fprintf(stderr, "mashctl credential issue: %v\n", err)
			return credError
		}
		var rf *credential.Refusal
		if req, rf = credential.Parse(data); rf != nil {
			return refuse(rf)
		}
		clear(data)
	} else {
		req = o.req
		for _, f := range []string{req.Address, req.Endpoint, req.PublicKey, req.Purpose} {
			if credential.LooksLikeSeed(f) {
				return refuse(&credential.Refusal{Code: credential.CodeSeed, Detail: "a private key (seed) is in the request: send only the public key; the key you put there should be treated as exposed"})
			}
		}
		if _, _, err := req.AddressParts(); err != nil {
			return refuse(&credential.Refusal{Code: issue.CodeMalformed, Detail: "address is not owner.name"})
		}
	}
	servers, err := credServers(o.servers, env)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl credential issue: %v\n", err)
		return credUsage
	}
	cfg, err := loadCtlConfig(env, lookup, true)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return credError
	}
	if !o.json {
		announceTarget(cfg, env, stderr, &sysTarget{o.sysCreds})
	}
	env = credEnv(env, o.sysCreds)
	b, err := openCredBackend(cfg, env, servers)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl credential issue: %v\n", err)
		return credError
	}
	defer b.Close()
	if req.Endpoint == "" {
		eps, err := endpointsAt(b.Ctx, b.Admin, req.Address)
		if err != nil {
			fmt.Fprintf(stderr, "mashctl credential issue: the directory could not be read: %v\n", err)
			return credError
		}
		switch len(eps) {
		case 1:
			req.Endpoint = eps[0]
		case 0:
			fmt.Fprintf(stderr, "mashctl credential issue: the directory has no endpoint at %s: pass --endpoint agent.owner.name\n", req.Address)
			return credUsage
		default:
			fmt.Fprintf(stderr, "mashctl credential issue: the directory has %d endpoints at %s (%s): pass --endpoint with one of them\n", len(eps), req.Address, strings.Join(eps, ", "))
			return credUsage
		}
	}
	cc, err := b.Cred()
	if err != nil {
		fmt.Fprintf(stderr, "mashctl credential issue: %v\n", err)
		return credError
	}
	res, err := credential.Issue(b.Ctx, cc, req)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl credential issue: %v\n", err)
		res.Outcome = credential.OutcomeError
	}
	return emitIssue(res, o, stdout, stderr)
}

// endpointsAt is the endpoint of each directory record at address owner.name, in canonical form (the bare name for a
// legacy record).
func endpointsAt(ctx context.Context, adm *admin.Admin, address string) ([]string, error) {
	owner, name, err := credential.Request{Address: address}.AddressParts()
	if err != nil {
		return nil, err
	}
	recs, err := adm.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range recs {
		if r.Owner != owner || r.Name != name {
			continue
		}
		id, err := r.Identity()
		if err != nil {
			continue
		}
		out = append(out, id.String())
	}
	return out, nil
}

// emitIssue prints the result and returns its exit code. With --json the result is the one object on stdout and
// nothing else is there; the JWT is written to --out first when it is given, and left out of the text and the JSON.
func emitIssue(res credential.Result, o issueOpts, stdout, stderr io.Writer) int {
	code := res.ExitCode()
	wrote := false
	if res.Outcome == credential.OutcomeIssued && o.out != "" {
		if err := credential.WriteFile0600(o.out, []byte(res.JWT+"\n"), o.force); err != nil {
			fmt.Fprintf(stderr, "mashctl credential issue: issued %s for %s, but the JWT was not written: %v\n", res.Address, res.PublicKey, err)
			return credError
		}
		wrote = true
		res.JWT = ""
	}
	if o.json {
		b, err := json.Marshal(res)
		if err != nil {
			fmt.Fprintf(stderr, "mashctl credential issue: %v\n", err)
			return credError
		}
		fmt.Fprintln(stdout, string(b))
		return code
	}
	switch res.Outcome {
	case credential.OutcomeIssued:
		fmt.Fprintf(stdout, "issued: %s, key %s, generation %d\n", res.Address, res.PublicKey, res.Generation)
		if len(res.Tags) > 0 {
			fmt.Fprintf(stdout, "tags: %s\n", strings.Join(res.Tags, " "))
		}
		if wrote {
			fmt.Fprintf(stdout, "jwt written to %s\n", o.out)
			fmt.Fprintf(stdout, "next: copy %s to the endpoint's host, then run\n  mash credential install --seed ep.seed --jwt %s --out %s.creds\n", o.out, o.out, res.Address)
		} else {
			fmt.Fprintln(stdout, res.JWT)
		}
	case credential.OutcomeRefused:
		fmt.Fprintf(stderr, "refused: %s\n", res.Refusal)
	default:
		if res.Refusal != nil {
			fmt.Fprintf(stderr, "mashctl credential issue: %s\n", res.Refusal)
		}
	}
	return code
}

// splitTarget takes the positional target (first, not a flag) off args.
func splitTarget(args []string) (target string, rest []string, ok bool) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "", args, false
	}
	return args[0], args[1:], true
}

// credTarget is what show and revoke take: a flag-free parse of the target and the shared flags. A seed given as the
// target is refused without being echoed.
func credTarget(verb, usage string, args []string, bools []string, stdout, stderr io.Writer) (target string, vals map[string]string, set map[string]bool, rc int, done bool) {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(stdout, usage)
		return "", nil, nil, credOK, true
	}
	target, rest, ok := splitTarget(args)
	if !ok {
		fmt.Fprintf(stderr, "mashctl credential %s: a target is required\n%s\n", verb, usage)
		return "", nil, nil, credUsage, true
	}
	vals, set, help, err := credential.ParseFlags(rest, []string{"--servers", "--sys-creds"}, bools)
	if help {
		fmt.Fprintln(stdout, usage)
		return "", nil, nil, credOK, true
	}
	if err != nil {
		fmt.Fprintf(stderr, "mashctl credential %s: %v\n%s\n", verb, err, usage)
		return "", nil, nil, credUsage, true
	}
	if credential.LooksLikeSeed(target) {
		fmt.Fprintf(stderr, "mashctl credential %s: a private key (seed) was given: only a public key or an address is taken; the key you put there should be treated as exposed\n", verb)
		return "", nil, nil, credUsage, true
	}
	return target, vals, set, 0, false
}

// showKeys is `show --json` of an address: every key recorded at it.
type showKeys struct {
	Address string              `json:"address"`
	Keys    []credential.Report `json:"keys"`
}

// showNotFound is the JSON of a show that found no record.
type showNotFound struct {
	Outcome   string `json:"outcome"`
	Address   string `json:"address,omitempty"`
	PublicKey string `json:"public_key,omitempty"`
}

func credShow(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	target, vals, set, rc, done := credTarget("show", credShowUsage, args, []string{"--json"}, stdout, stderr)
	if done {
		return rc
	}
	isKey := nkeys.IsValidPublicUserKey(target)
	if !isKey {
		if _, _, err := (credential.Request{Address: target}).AddressParts(); err != nil {
			fmt.Fprintf(stderr, "mashctl credential show: the target is neither a user public key nor an address owner.name\n%s\n", credShowUsage)
			return credUsage
		}
	}
	servers, err := credServers(vals["--servers"], env)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl credential show: %v\n", err)
		return credUsage
	}
	cfg, err := loadCtlConfig(env, lookup, true)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return credError
	}
	if !set["--json"] {
		announceTarget(cfg, env, stderr, &sysTarget{vals["--sys-creds"]})
	}
	env = credEnv(env, vals["--sys-creds"])
	b, err := openCredBackend(cfg, env, servers)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl credential show: %v\n", err)
		return credError
	}
	defer b.Close()
	cc, err := b.Cred()
	if err != nil {
		fmt.Fprintf(stderr, "mashctl credential show: %v\n", err)
		return credError
	}
	keys := []string{target}
	if !isKey {
		recs, _, err := b.Admin.Issued(b.Ctx)
		if err != nil {
			fmt.Fprintf(stderr, "mashctl credential show: the issue record could not be read: %v\n", err)
			return credError
		}
		keys = keys[:0]
		for _, r := range recs {
			if r.Address == target {
				keys = append(keys, r.PublicKey)
			}
		}
	}
	var reps []credential.Report
	for _, k := range keys {
		rep, err := credential.Show(b.Ctx, cc, k)
		if err != nil {
			fmt.Fprintf(stderr, "mashctl credential show: %v\n", err)
			return credError
		}
		if rep.Recorded {
			reps = append(reps, rep)
		}
	}
	if len(reps) == 0 {
		nf := showNotFound{Outcome: "not_found"}
		if isKey {
			nf.PublicKey = target
		} else {
			nf.Address = target
		}
		if set["--json"] {
			printJSON(stdout, nf)
		} else {
			fmt.Fprintf(stderr, "not_found: no issue record for %s\n", target)
		}
		return credRefused
	}
	if set["--json"] {
		switch {
		case isKey:
			printJSON(stdout, reps[0])
		default:
			printJSON(stdout, showKeys{Address: target, Keys: reps})
		}
		return credOK
	}
	for _, rep := range reps {
		writeReport(stdout, rep)
	}
	return credOK
}

func printJSON(w io.Writer, v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintln(w, string(b))
}

// writeReport is the text of one key's report.
func writeReport(w io.Writer, rep credential.Report) {
	revoked := rep.AccountRevokes
	if rep.Record != nil && rep.Record.RevokedAt != "" {
		revoked = credential.StateYes
	}
	addr := ""
	if rep.Record != nil {
		addr = rep.Record.Address
	}
	fmt.Fprintf(w, "key: %s   address: %s   revoked: %s   connected: %s\n", rep.PublicKey, addr, revoked, rep.Connection.State)
	if rep.Record != nil {
		line := fmt.Sprintf("history: issued generation %d at %s (%s)", rep.Record.Generation, rep.Record.IssuedAt, rep.Record.EffectivePurpose())
		if rep.History != nil && rep.History.Revisions > 1 {
			line += fmt.Sprintf(", %d revisions", rep.History.Revisions)
		}
		fmt.Fprintln(w, line)
		if rep.Record.RevokedAt != "" {
			fmt.Fprintf(w, "revoked at %s\n", rep.Record.RevokedAt)
		}
	}
	for _, n := range rep.Notes {
		fmt.Fprintf(w, "note: %s\n", n)
	}
}

// revokeAckRE is the count revokeUsers prints for a key it revoked.
var revokeAckRE = regexp.MustCompile(`(\d+) server\(s\) acknowledged`)

// credRevoke is `mashctl credential revoke`: exit 0 revoked or unchanged, 1 refused, 2 usage, 5 partial, 6 error.
func credRevoke(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	target, vals, set, rc, done := credTarget("revoke", credRevokeUsage, args, []string{"--json"}, stdout, stderr)
	if done {
		return rc
	}
	if !nkeys.IsValidPublicUserKey(target) {
		fmt.Fprintf(stderr, "mashctl credential revoke: the target is not a NATS user public key\n%s\n", credRevokeUsage)
		return credUsage
	}
	servers, err := credServers(vals["--servers"], env)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl credential revoke: %v\n", err)
		return credUsage
	}
	asJSON := set["--json"]
	// finish reports the result: one JSON object on stdout, or (text) the refusal line on stderr.
	finish := func(res credential.RevokeResult, cause error) int {
		res.PublicKey = target
		if cause != nil {
			fmt.Fprintf(stderr, "mashctl credential revoke: %v\n", cause)
		}
		if asJSON {
			printJSON(stdout, res)
		} else if res.Refusal != nil {
			fmt.Fprintf(stderr, "refused: %s: %s\n", res.Refusal.Code, res.Refusal.Detail)
		}
		return res.ExitCode()
	}
	errRes := credential.RevokeResult{Outcome: credential.OutcomeError}
	cfg, err := loadCtlConfig(env, lookup, true)
	if err != nil {
		return finish(errRes, err)
	}
	if !asJSON {
		announceTarget(cfg, env, stderr, &sysTarget{vals["--sys-creds"]})
	}
	env = credEnv(env, vals["--sys-creds"])
	b, err := openCredBackend(cfg, env, servers)
	if err != nil {
		return finish(errRes, err)
	}
	defer b.Close()
	// One reading of the clock, as retire takes it: the check of the issue record and the revocation carry one time.
	at := ctl.RevokeNow()
	if _, found, err := b.Admin.IssuedByKey(b.Ctx, target); err == nil && !found {
		// No issue record: a key no server's account JWT revokes either is not one this deployment knows.
		revoked, err := keyRevoked(b.Ctx, cfg, env, servers, target)
		if err != nil {
			return finish(errRes, err)
		}
		if !revoked {
			return finish(credential.RevokeResult{Outcome: credential.OutcomeRefused, Refusal: &credential.Refusal{Code: credential.RefusalNotFound, Detail: "no issue record for " + target + " and no account JWT revokes it"}}, nil)
		}
	}
	if err := ctl.CheckNotFutureIssued(b.Ctx, b.Admin, []ctl.RevokeTarget{{Key: target}}, at, stderr); err != nil {
		return finish(errRes, err)
	}
	var out bytes.Buffer
	err = revokeUsers(b.Ctx, cfg, env, servers, []string{target}, &out, func() time.Time { return at })
	if asJSON {
		stderr.Write(out.Bytes()) // warnings; stdout holds the one JSON object
	} else {
		stdout.Write(out.Bytes())
	}
	if err != nil {
		cause := fmt.Errorf("user key %s...: %w", ctl.ShortKey(target), err)
		var ae *ackedError
		if errors.As(err, &ae) {
			return finish(credential.RevokeResult{Outcome: credential.OutcomePartial, ServersAcknowledged: ae.acked}, cause)
		}
		return finish(errRes, cause)
	}
	ctl.MarkRevoked(b.Ctx, b.Admin, target, at, stderr)
	res := credential.RevokeResult{Outcome: credential.OutcomeUnchanged}
	if m := revokeAckRE.FindStringSubmatch(out.String()); m != nil {
		res.Outcome = credential.OutcomeRevoked
		res.ServersAcknowledged, _ = strconv.Atoi(m[1])
		if servers > 0 && res.ServersAcknowledged < servers {
			res.Outcome = credential.OutcomePartial
		}
	}
	return finish(res, nil)
}
