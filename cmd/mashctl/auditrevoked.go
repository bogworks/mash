package main

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
)

// auditRevocationPending is the What of the finding of a revocation that has not reached every server (ADR 0142).
const auditRevocationPending = "revocation_pending"

// auditPendingRevocations asks the servers which of keys they hold as revoked; a test replaces it.
var auditPendingRevocations = pendingRevocationsFromServers

// pendingRevocationsFromServers opens the key store, connects with the system account credentials and compares the
// account JWT of every server with keys (apply.RevocationsPending). Any failure to ask is an error.
func pendingRevocationsFromServers(ctx context.Context, cfg config.Config, env func(string) string, servers int, keys []string) (apply.RevocationReport, error) {
	ks, err := openKeys(cfg, env)
	if err != nil {
		return apply.RevocationReport{}, err
	}
	nc, err := connectSys(cfg, "", env)
	if err != nil {
		return apply.RevocationReport{}, err
	}
	defer nc.Close()
	ctx, cancel := context.WithTimeout(ctx, applyDeadline)
	defer cancel()
	servers = withDeclared(cfg, servers)
	return apply.RevocationsPending(ctx, apply.NATS{Conn: nc, Inventory: servers}, ks, servers, keys)
}

// revokedUserKeys is every user key the issue record (revoked_at) or the retired-name bucket marks as revoked, sorted.
func revokedUserKeys(issued []string, retired []auditRetiredMarker) []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range issued {
		if k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for _, m := range retired {
		if m.User != "" && !seen[m.User] {
			seen[m.User] = true
			out = append(out, m.User)
		}
	}
	sort.Strings(out)
	return out
}

// revocationProblems turns what the servers hold into findings: one per revoked key an answering server does not list,
// and one for the servers that did not answer (a silent server cannot be shown to hold any revocation). A server cannot
// be named from here: the account lookup does not say which server replied, so the count of servers is given. Nothing
// is reported once every server holds every revocation.
func revocationProblems(rep apply.RevocationReport) []auditProblem {
	var probs []auditProblem
	for _, p := range rep.Pending {
		probs = append(probs, auditProblem{What: auditRevocationPending, Item: ctl.ShortKey(p.Key),
			Error: fmt.Sprintf("the revocation of user key %s... has not reached every server: %d of %d answering servers do not list it as revoked, so a credential of that key is still accepted there until they take the account JWT from the account resolver (a server that rejoins gets it on its next sync)", ctl.ShortKey(p.Key), p.Lacking, rep.Answered)})
	}
	if rep.Silent > 0 && rep.Keys > 0 {
		probs = append(probs, auditProblem{What: auditRevocationPending, Item: "servers",
			Error: fmt.Sprintf("%d of %d servers did not answer the account JWT lookup, so they cannot be shown to hold the %d recorded revocation(s): a server that is down gets them from the account resolver when it rejoins; bring it back and run the audit again", rep.Silent, rep.Declared, rep.Keys)})
	}
	return probs
}

// auditRevocations is the revocation_pending findings of a report: the keys the issue record and the retired-name
// bucket mark revoked, against what the servers hold. It needs the key store and the system account credentials; with
// no revoked key to check, or without those credentials, it checks nothing and says nothing. A check that is set up but
// cannot be made is a note (it is not a finding: the audit could not tell).
func auditRevocations(ctx context.Context, adm *admin.Admin, cfg config.Config, env func(string) string, rep *auditReport) {
	issued, _, err := adm.Issued(ctx)
	if err != nil {
		rep.Notes = append(rep.Notes, "revocations were not compared with the servers: the issue record could not be read: "+config.Clean(err.Error()))
		return
	}
	var revoked []string
	for _, r := range issued {
		if r.RevokedAt != "" {
			revoked = append(revoked, r.PublicKey)
		}
	}
	var markers []auditRetiredMarker
	if rep.Retired != nil {
		markers = rep.Retired.Markers
	}
	keys := revokedUserKeys(revoked, markers)
	if len(keys) == 0 {
		return
	}
	if _, serr := sysCredsPath("", env, cfg); serr != nil {
		return
	}
	servers, _ := parseServers(env)
	res, err := auditPendingRevocations(ctx, cfg, env, servers, keys)
	if err != nil {
		rep.Notes = append(rep.Notes, "revocations were not compared with the servers: "+config.Clean(err.Error()))
		return
	}
	rep.Problems = append(rep.Problems, revocationProblems(res)...)
	slices.SortFunc(rep.Problems, func(a, b auditProblem) int {
		return strings.Compare(a.What+"\x00"+a.Item+"\x00"+a.Error, b.What+"\x00"+b.Item+"\x00"+b.Error)
	})
	rep.Summary.Problems = len(rep.Problems)
}
