package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/sap"
)

// endpointBulkSchema is the schema field of the JSON of the bulk reissue.
const endpointBulkSchema = "mashctl-endpoint-bulk@1"

type epBulkEntry struct {
	Owner     string  `json:"owner"`
	Group     string  `json:"group"`
	Instance  string  `json:"instance"`
	Address   string  `json:"address"`
	Key       string  `json:"key"`
	Outcome   string  `json:"outcome"` // reissued, planned, skipped, refused or error
	Reason    *string `json:"reason"`
	CredsPath string  `json:"creds_path"`
	// ServersAcknowledged is how many servers took the account update; present on a reissued endpoint only.
	ServersAcknowledged *int `json:"servers_acknowledged,omitempty"`
}

type epBulkCounts struct {
	Reissued int `json:"reissued"`
	Planned  int `json:"planned"`
	Skipped  int `json:"skipped"`
	Refused  int `json:"refused"`
	Error    int `json:"error"`
}

type epBulkDoc struct {
	Schema    string        `json:"schema"`
	Outcome   string        `json:"outcome"`
	Counts    epBulkCounts  `json:"counts"`
	Endpoints []epBulkEntry `json:"endpoints"`
	NextSteps []epStep      `json:"next_steps"`
	Refusal   *epRefusal    `json:"refusal"`
}

// bulkArgs is what runEndpointReissue parsed when --all, --changed or --yes was given.
type bulkArgs struct {
	all, changed, yes bool
	positional        string
	c                 epCommon
}

// samePermissions compares two permission sets as sets: the order of a list means nothing.
func samePermissions(a, b jwt.Permissions) bool {
	same := func(x, y jwt.StringList) bool {
		xs, ys := slices.Clone([]string(x)), slices.Clone([]string(y))
		slices.Sort(xs)
		slices.Sort(ys)
		return slices.Equal(slices.Compact(xs), slices.Compact(ys))
	}
	resp := (a.Resp == nil) == (b.Resp == nil) && (a.Resp == nil || *a.Resp == *b.Resp)
	return same(a.Pub.Allow, b.Pub.Allow) && same(a.Pub.Deny, b.Pub.Deny) &&
		same(a.Sub.Allow, b.Sub.Allow) && same(a.Sub.Deny, b.Sub.Deny) && resp
}

// epCredsStale reads the creds file at path and reports whether its permission set differs from the current one for
// addr.
func epCredsStale(path string, addr sap.Address) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	defer clear(raw)
	tok, err := jwt.ParseDecoratedJWT(raw)
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	want := admin.Permissions(addr)
	if want == nil {
		return false, fmt.Errorf("%s: not a valid endpoint address", addr)
	}
	return !samePermissions(uc.Permissions, *want), nil
}

// runEndpointBulk is `mashctl endpoint reissue --all` and `--changed`: the single-address reissue of each endpoint in
// address order. Both skip an endpoint with no creds file on this host (no_local_creds); --changed also skips one
// whose creds are current and whose key is not revoked, and takes one with a staged rotation (rotation_pending) or a
// revoked key (key_revoked); a revocation that cannot be checked is the error revocation_unknown. A refusal or error on one endpoint does not stop the others.
func runEndpointBulk(a bulkArgs, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	c := a.c
	doc := epBulkDoc{Schema: endpointBulkSchema, Outcome: "refused", Endpoints: []epBulkEntry{}, NextSteps: []epStep{}}
	report := func(code int, text func(io.Writer)) int {
		if doc.Refusal != nil {
			fmt.Fprintf(stderr, "mashctl: endpoint reissue: %s (%s): %s\n", doc.Outcome, doc.Refusal.Code, doc.Refusal.Detail)
		}
		if c.jsonOut {
			writeJSON(stdout, doc)
		} else if text != nil {
			text(stdout)
		}
		return code
	}
	usage := func(code, msg string) int {
		fmt.Fprintf(stderr, "mashctl: endpoint reissue: %s\n%s", msg, endpointReissueUsage)
		if c.jsonOut {
			doc.Outcome = "error"
			doc.Refusal = &epRefusal{code, msg}
			writeJSON(stdout, doc)
		}
		return 2
	}
	switch {
	case a.all && a.changed:
		return usage("usage", "give one of --all and --changed, not both")
	case !a.all && !a.changed:
		return usage("usage", "--yes is for the bulk forms: write --all or --changed")
	case a.positional != "":
		return usage("usage", "an endpoint address cannot be given with --all or --changed")
	case c.jwtOut != "":
		return usage("usage", "--jwt-out is for one endpoint; the bulk forms write creds files")
	case c.publicKey != "" || !c.makeKey:
		return usage("bulk_needs_make_key", "the bulk forms take --make-key only: many public keys cannot arrive in one command; run endpoint reissue ADDRESS --public-key FILE once per endpoint")
	}
	fail := func(code int, outcome, rc, detail string) int {
		doc.Outcome = outcome
		doc.Refusal = &epRefusal{rc, detail}
		return report(code, nil)
	}
	servers, err := parseServers(env)
	if err != nil {
		return usage("servers_invalid", err.Error())
	}
	cfg, err := loadCtlConfig(env, lookup, true)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return fail(6, "error", "config", "the configuration did not load; fix it and run the same command again")
	}
	keys, acct, err := epAccountOf(cfg, env)
	if err != nil {
		return fail(1, "refused", "no_account_key", err.Error())
	}
	announceTarget(cfg, env, stderr, nil)
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		return fail(6, "error", "server", admin.RewordUnavailable(err.Error())+"; nothing was changed")
	}
	defer closeAdmin()
	recs, err := adm.List(ctx)
	if err != nil {
		return fail(6, "error", "server", admin.RewordUnavailable(err.Error()))
	}
	var addrs []sap.Address
	for _, r := range recs {
		ad, err := r.Address()
		if err != nil {
			continue
		}
		addrs = append(addrs, ad)
	}
	slices.SortFunc(addrs, func(x, y sap.Address) int { return strings.Compare(x.String(), y.String()) })

	run := epRun{ctx: ctx, adm: adm, cfg: cfg, env: env, servers: servers, keys: keys, acct: acct, stderr: stderr}
	doit := a.yes && !c.dryRun
	if doit && len(addrs) > 0 {
		if err := checkServerSet(ctx, cfg, env, servers); err != nil {
			return fail(6, "error", "servers", fmt.Sprintf("%v; %s", err, ctl.NotRevoked))
		}
	}
	var firstFail *epRefusal
	add := func(ad sap.Address, path, outcome, reason string, acked *int) {
		e := epBulkEntry{Owner: ad.Owner, Group: ad.Group, Instance: ad.Instance, Address: ad.String(), Key: ad.Flat(), Outcome: outcome, CredsPath: path, ServersAcknowledged: acked}
		if reason != "" {
			e.Reason = &reason
		}
		doc.Endpoints = append(doc.Endpoints, e)
		n := &doc.Counts
		switch outcome {
		case "reissued":
			n.Reissued++
		case "planned":
			n.Planned++
		case "skipped":
			n.Skipped++
		case "refused":
			n.Refused++
		case "error":
			n.Error++
		}
	}
	for _, ad := range addrs {
		path, perr := credsFileFor(cfg, ad)
		if perr != nil {
			add(ad, "", "error", "no_config_dir", nil)
			firstFail = firstOr(firstFail, &epRefusal{"no_config_dir", "no config directory to write the creds file in; set MASH_CONFIG_DIR"})
			continue
		}
		// An endpoint whose creds are not on this host is never rotated here: that would revoke the key it works with
		// and write the replacement creds where the endpoint cannot read them.
		if config.Absent(path) {
			add(ad, path, "skipped", "no_local_creds", nil)
			continue
		}
		reason := ""
		if a.changed {
			// Selected: a staged rotation is waiting, the permission set differs, or the directory's key is revoked.
			if _, serr := os.Lstat(path + ".pending"); serr == nil {
				reason = "rotation_pending"
			} else {
				stale, serr := epCredsStale(path, ad)
				if serr != nil {
					add(ad, path, "error", "creds_unreadable", nil)
					firstFail = firstOr(firstFail, &epRefusal{"creds_unreadable", serr.Error()})
					fmt.Fprintf(stderr, "mashctl: endpoint reissue %s: creds_unreadable: %v\n", epShortcut(ad), serr)
					continue
				}
				if !stale {
					revoked, rerr := epKeyIsRevoked(ctx, adm, cfg, env, servers, ad)
					if rerr != nil {
						detail := fmt.Sprintf("cannot tell whether the current key of %s is revoked: %v", ad, rerr)
						add(ad, path, "error", "revocation_unknown", nil)
						firstFail = firstOr(firstFail, &epRefusal{"revocation_unknown", detail})
						fmt.Fprintf(stderr, "mashctl: endpoint reissue %s: revocation_unknown: %s\n", epShortcut(ad), detail)
						continue
					}
					if !revoked {
						continue
					}
					reason = "key_revoked"
				}
			}
		}
		if !doit {
			add(ad, path, "planned", reason, nil)
			continue
		}
		if f := run.preflight(ad); f != nil {
			add(ad, path, failOutcome(f), f.code, nil)
			firstFail = firstOr(firstFail, &epRefusal{f.code, f.detail})
			fmt.Fprintf(stderr, "mashctl: endpoint reissue %s: %s: %s\n", epShortcut(ad), f.code, f.detail)
			continue
		}
		_, acked, f := run.reissue(ad, true, "", path, false)
		if f != nil {
			add(ad, path, failOutcome(f), f.code, nil)
			firstFail = firstOr(firstFail, &epRefusal{f.code, f.detail})
			fmt.Fprintf(stderr, "mashctl: endpoint reissue %s: %s: %s\n", epShortcut(ad), f.code, f.detail)
			continue
		}
		add(ad, path, "reissued", reason, &acked)
	}

	n := doc.Counts
	bad := n.Refused + n.Error
	code := 0
	switch {
	case n.Reissued+n.Planned+bad == 0:
		doc.Outcome = "unchanged"
	case n.Planned > 0 && bad == 0:
		doc.Outcome = "planned"
	case n.Reissued > 0 && bad == 0:
		doc.Outcome = "reissued"
	case n.Reissued == 0 && n.Planned == 0:
		doc.Outcome, code = "refused", 1
		if n.Error > 0 {
			doc.Outcome, code = "error", 6
		}
		doc.Refusal = firstFail
	default:
		doc.Outcome, code = "partial", 5
	}
	if n.Reissued > 0 {
		doc.NextSteps = append(doc.NextSteps, epStep{Sender: "endpoint", Step: "session",
			Action: "restart the sessions of the reissued endpoints so they read the new creds", Purpose: "use"})
	}
	if n.Planned > 0 {
		doc.NextSteps = append(doc.NextSteps, epStep{Sender: "operator", Step: "confirm",
			Action: "reissue these endpoints", Purpose: "use", Detail: bulkAgain(a)})
	}
	return report(code, func(w io.Writer) {
		for _, e := range doc.Endpoints {
			s := epShortcut(sap.Address{Owner: e.Owner, Group: e.Group, Instance: e.Instance})
			reason := ""
			if e.Reason != nil {
				reason = " (" + *e.Reason + ")"
			}
			switch e.Outcome {
			case "reissued":
				fmt.Fprintf(w, "reissued %s\n", s)
				if e.ServersAcknowledged != nil {
					fmt.Fprintf(w, "  servers acknowledged the account update: %d\n", *e.ServersAcknowledged)
				}
			case "planned":
				fmt.Fprintf(w, "would reissue %s\n", s)
			case "skipped":
				fmt.Fprintf(w, "skipped %s%s", s, reason)
				if e.Reason != nil && *e.Reason == "no_local_creds" {
					fmt.Fprintf(w, ": its creds are not on this host; reissue it on its own machine with: mash key create, then mashctl endpoint reissue %s --public-key FILE", s)
				}
				fmt.Fprintln(w)
			default:
				fmt.Fprintf(w, "%s %s%s\n", e.Outcome, s, reason)
			}
		}
		fmt.Fprintf(w, "reissued %d, skipped %d, refused %d, error %d\n", n.Reissued, n.Skipped, n.Refused, n.Error)
		switch {
		case n.Planned > 0:
			fmt.Fprintf(w, "would reissue %d; nothing was changed; to do it run: %s\n", n.Planned, bulkAgain(a))
		case n.Reissued+bad == 0:
			fmt.Fprintln(w, "nothing to reissue")
		case n.Reissued > 0:
			fmt.Fprintln(w, "next: restart the sessions of the reissued endpoints so they read the new creds")
		}
	})
}

// epKeyIsRevoked reports whether the key the directory names for ad is revoked in the account JWT.
func epKeyIsRevoked(ctx context.Context, adm *admin.Admin, cfg config.Config, env func(string) string, servers int, ad sap.Address) (bool, error) {
	cur, _, err := adm.UserOf(ctx, ad)
	if err != nil {
		return false, err
	}
	return keyRevoked(ctx, cfg, env, servers, cur)
}

func failOutcome(f *epFail) string {
	if f.refused {
		return "refused"
	}
	return "error"
}

func firstOr(have, next *epRefusal) *epRefusal {
	if have != nil {
		return have
	}
	return next
}

func bulkAgain(a bulkArgs) string {
	mode := "--all"
	if a.changed {
		mode = "--changed"
	}
	return "mashctl endpoint reissue " + mode + " --make-key --yes"
}
