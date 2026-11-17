package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/store"
)

const endpointShowUsage = `usage: mashctl endpoint show ADDRESS [--json]
  ADDRESS is owner/group/instance (for example alice/dev/design) or owner.group.instance.
  Prints the endpoint's whole directory record, the creds file for it in the config directories (its path only), and
  its issue record, if there is one. Changes nothing.
  --json   print the result as JSON (schema mashctl-endpoint@1)
  exit: 0 shown, 1 refused (no such endpoint), 2 usage, 6 error
Example:
  mashctl endpoint show alice/dev/design
`

// endpointShowRecordJSON is the whole directory record.
type endpointShowRecordJSON struct {
	endpointRecordJSON
	Agent       string `json:"agent"`
	Account     string `json:"account"`
	MailSubject string `json:"mail_subject"`
}

type showCredsJSON struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

type showIssuedJSON struct {
	Purpose  string `json:"purpose"`
	IssuedAt string `json:"issued_at"`
	Revoked  bool   `json:"revoked"`
}

type showJSON struct {
	Schema   string                  `json:"schema"`
	Outcome  string                  `json:"outcome"`
	Endpoint *endpointShowRecordJSON `json:"endpoint"`
	Creds    showCredsJSON           `json:"creds"`
	Issued   *showIssuedJSON         `json:"issued"`
	Refusal  *refusalJSON            `json:"refusal"`
}

// runEndpointShow is `mashctl endpoint show`.
func runEndpointShow(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	var arg string
	asJSON := false
	for _, a := range args {
		switch {
		case a == "--help" || a == "-h":
			fmt.Fprint(stdout, endpointShowUsage)
			return 0
		case a == "--json":
			asJSON = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "mashctl: endpoint show: unknown %s; try: mashctl endpoint show --help\n", config.RejectedArg(a))
			return 2
		case arg != "":
			fmt.Fprintf(stderr, "mashctl: endpoint show: one endpoint address only; try: mashctl endpoint show --help\n")
			return 2
		default:
			arg = a
		}
	}
	if arg == "" {
		fmt.Fprintf(stderr, "mashctl: endpoint show: no endpoint address: give owner/group/instance, for example alice/dev/design; try: mashctl endpoint show --help\n")
		return 2
	}
	addr, err := parseEndpointRef(arg)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl: endpoint show: %v\n", err)
		return 2
	}
	short := slashForm(addr)
	fail := func(err error) int {
		fmt.Fprintf(stderr, "mashctl: endpoint show %s: %v\n", short, err)
		return 6
	}
	cfg, err := loadCtlConfig(env, lookup, true)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 6
	}
	announceTarget(cfg, env, stderr, nil)
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		return fail(err)
	}
	defer closeAdmin()
	recs, bad, err := adm.List(ctx)
	if err != nil {
		return fail(err)
	}
	for _, b := range bad {
		b.Warn(stderr, "mashctl")
	}
	credsPath := cfg.Find(addr.String() + ".creds")
	rec, found := findRecord(recs, addr)
	if !found {
		detail := "no directory record for " + short + "; mashctl endpoint list shows the endpoints that exist"
		if asJSON {
			writeJSON(stdout, showJSON{Schema: "mashctl-endpoint@1", Outcome: "refused",
				Creds:   showCredsJSON{Path: credsPath, Exists: credsPath != ""},
				Refusal: &refusalJSON{Code: "not_found", Detail: detail}})
		} else {
			fmt.Fprintf(stderr, "mashctl: endpoint show %s: %s\n", short, detail)
		}
		return 1
	}
	var issued *store.Issued
	if rec.User != "" {
		is, ok, err := adm.IssuedByKey(ctx, rec.User)
		if err != nil {
			return fail(err)
		}
		if ok {
			issued = &is
		}
	}
	if asJSON {
		j := showJSON{
			Schema: "mashctl-endpoint@1", Outcome: "shown",
			Endpoint: &endpointShowRecordJSON{endpointRecordJSON: recordJSON(rec), Agent: rec.Agent, Account: rec.Account, MailSubject: rec.MailSubject},
			Creds:    showCredsJSON{Path: credsPath, Exists: credsPath != ""},
		}
		if issued != nil {
			j.Issued = &showIssuedJSON{Purpose: issued.EffectivePurpose(), IssuedAt: issued.IssuedAt, Revoked: issued.RevokedAt != ""}
		}
		writeJSON(stdout, j)
		return 0
	}
	c := config.Clean
	fmt.Fprintf(stdout, "%s  %s\n", short, addr.String())
	fmt.Fprintf(stdout, "  owner:        %s\n", c(rec.Owner))
	fmt.Fprintf(stdout, "  group:        %s\n", c(rec.Group))
	fmt.Fprintf(stdout, "  instance:     %s\n", c(rec.Instance))
	fmt.Fprintf(stdout, "  agent:        %s\n", c(rec.Agent))
	fmt.Fprintf(stdout, "  account:      %s\n", c(rec.Account))
	fmt.Fprintf(stdout, "  user key:     %s\n", c(rec.User))
	fmt.Fprintf(stdout, "  mail subject: %s\n", c(rec.MailSubject))
	fmt.Fprintf(stdout, "  created:      %s\n", c(rec.Created))
	fmt.Fprintf(stdout, "  note:         %s\n", c(rec.Note))
	fmt.Fprintf(stdout, "  role:         %s\n", c(rec.Role))
	fmt.Fprintf(stdout, "  host:         %s\n", c(rec.Host))
	fmt.Fprintf(stdout, "  description:  %s\n", c(rec.Description))
	fmt.Fprintf(stdout, "  caps:         %s\n", c(strings.Join(rec.Caps, ",")))
	labels := recordSuffix(store.Record{Labels: rec.Labels})
	fmt.Fprintf(stdout, "  labels:       %s\n", strings.TrimPrefix(labels, "labels "))
	if credsPath != "" {
		fmt.Fprintf(stdout, "  creds file:   %s\n", c(credsPath))
	} else {
		fmt.Fprintln(stdout, "  creds file:   none in the config directories")
	}
	if issued == nil {
		fmt.Fprintln(stdout, "  issue record: none for this key")
	} else {
		state := "not revoked"
		if issued.RevokedAt != "" {
			state = "revoked " + c(issued.RevokedAt)
		}
		fmt.Fprintf(stdout, "  issue record: purpose %s, issued %s, %s\n", c(issued.EffectivePurpose()), c(issued.IssuedAt), state)
	}
	return 0
}
