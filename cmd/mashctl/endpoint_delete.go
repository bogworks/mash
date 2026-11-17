package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
)

const endpointDeleteUsage = `usage: mashctl endpoint delete ADDRESS [--delete-creds] [--force] [--no-revoke] [--dry-run] [--json]
  ADDRESS is owner/group/instance (for example alice/dev/design) or owner.group.instance.
  Revokes the endpoint's user key, removes its directory record and inbox, burns the address, and renames its creds
  file (if one is in the config directories) to NAME.creds.retired-<time>.
  --delete-creds   remove the creds file instead of renaming it
  --force          also handle the creds file when the server has no record
  --no-revoke      do not revoke the user key
  --dry-run        say what would happen and change nothing
  --json           print the result as JSON (schema mashctl-endpoint@1)
  --servers N and --sys-creds FILE override MASH_SERVERS and MASH_SYS_CREDS
  exit: 0 deleted or unchanged, 1 refused, 2 usage, 5 partial, 6 error
Example:
  mashctl endpoint delete alice/dev/design --dry-run
`

// endpointObject holds the names of an endpoint; the delete and show JSON carry them at the top level.
type endpointObject struct {
	Owner    string `json:"owner"`
	Group    string `json:"group"`
	Instance string `json:"instance"`
	Address  string `json:"address"`
	Key      string `json:"key"`
}

type deleteCredsJSON struct {
	Path   string `json:"path"`
	Action string `json:"action"`
}

type refusalJSON struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

type deleteJSON struct {
	Schema  string `json:"schema"`
	Outcome string `json:"outcome"`
	endpointObject
	Creds deleteCredsJSON `json:"creds"`
	// ServersAcknowledged is how many servers took the revocation of the user key; always present when the outcome is deleted, 0 when this run revoked none.
	ServersAcknowledged *int         `json:"servers_acknowledged,omitempty"`
	NextSteps           []string     `json:"next_steps"`
	Refusal             *refusalJSON `json:"refusal"`
}

// slashForm is owner/group/instance.
func slashForm(a sap.Address) string { return a.Owner + "/" + a.Group + "/" + a.Instance }

// writeJSON prints v as indented JSON and a newline.
func writeJSON(w io.Writer, v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Fprintf(w, "%s\n", b)
}

// runEndpointDelete is `mashctl endpoint delete`: retire's removal behind the endpoint noun.
func runEndpointDelete(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprint(stdout, endpointDeleteUsage)
		return 0
	}
	var arg string
	var opts retireOpts
	asJSON := jsonRequested(args)
	var names endpointObject
	// usageErr answers a usage error: the sentence on stderr, and with --json the document on stdout.
	usageErr := func(code, msg string) int {
		fmt.Fprintf(stderr, "mashctl: endpoint delete: %s\n", msg)
		if asJSON {
			writeJSON(stdout, deleteJSON{Schema: endpointSchema, Outcome: "error", endpointObject: names,
				Creds: deleteCredsJSON{Action: "none"}, NextSteps: []string{}, Refusal: &refusalJSON{Code: code, Detail: msg}})
		}
		return 2
	}
	for _, a := range args {
		switch {
		case a == "--delete-creds":
			opts.deleteCreds = true
		case a == "--force":
			opts.force = true
		case a == "--no-revoke":
			opts.noRevoke = true
		case a == "--dry-run":
			opts.dryRun = true
		case a == "--json":
		case a == "--help" || a == "-h":
			fmt.Fprint(stdout, endpointDeleteUsage)
			return 0
		case strings.HasPrefix(a, "-"):
			return usageErr("unknown_flag", fmt.Sprintf("unknown %s; try: mashctl endpoint delete --help", config.RejectedArg(a)))
		case arg != "":
			return usageErr("usage", "one endpoint address only; try: mashctl endpoint delete --help")
		default:
			arg = a
		}
	}
	if arg == "" {
		return usageErr("address_missing", "no endpoint address: give owner/group/instance, for example alice/dev/design; try: mashctl endpoint delete --help")
	}
	addr, err := parseEndpointRef(arg)
	if err != nil {
		return usageErr("address_invalid", err.Error())
	}
	names = endpointObject{Owner: addr.Owner, Group: addr.Group, Instance: addr.Instance, Address: addr.String(), Key: addr.Flat()}
	short := slashForm(addr)
	opts.verb = "endpoint delete"
	opts.label = short
	opts.full = short + " (" + addr.String() + ")"
	opts.again = "mashctl endpoint delete " + short
	opts.done = "deleted"
	opts.usage = endpointDeleteUsage
	opts.listHint = "mashctl endpoint list shows the endpoints that exist"
	out := stdout
	if asJSON {
		out = io.Discard
	}
	res := retireAddress(addr, opts, env, lookup, out, stderr)
	if res.pendingUsage {
		if asJSON {
			writeJSON(stdout, deleteJSON{Schema: endpointSchema, Outcome: "error", endpointObject: names,
				Creds: deleteCredsJSON{Action: "none"}, NextSteps: []string{}, Refusal: &refusalJSON{Code: "servers_invalid", Detail: res.detail}})
		}
		return 2
	}
	if res.kind == "deleted" {
		fmt.Fprintf(out, "servers acknowledged the revocation: %d\n", res.acked)
	}
	if asJSON {
		j := deleteJSON{
			Schema: endpointSchema, Outcome: res.kind, endpointObject: names,
			Creds:     deleteCredsJSON{Path: res.credsPath, Action: res.credsAction},
			NextSteps: res.nextSteps,
		}
		if j.Creds.Action == "" {
			j.Creds.Action = "none"
		}
		if j.NextSteps == nil {
			j.NextSteps = []string{}
		}
		if res.kind == "deleted" {
			j.ServersAcknowledged = &res.acked
		}
		if res.kind == "refused" || (res.kind == "error" && res.refusalCode != "") {
			j.Refusal = &refusalJSON{Code: res.refusalCode, Detail: res.detail}
		}
		writeJSON(stdout, j)
	}
	switch res.kind {
	case "deleted", "unchanged", "planned":
		return 0
	case "refused":
		return 1
	case "partial":
		return 5
	}
	return 6
}
