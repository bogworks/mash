package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/store"
)

const endpointListUsage = `usage: mashctl endpoint list [--json]
  Prints the endpoints in the directory, one per line: owner/group/instance, owner.group.instance and what is on record.
  --json   print the endpoints as JSON (schema mashctl-endpoint-list@1)
  exit: 0 listed (also when there are none), 2 usage, 6 error
Example:
  mashctl endpoint list
`

// endpointRecordJSON is one directory record as the list prints it.
type endpointRecordJSON struct {
	Owner         string            `json:"owner"`
	Group         string            `json:"group"`
	Instance      string            `json:"instance"`
	Address       string            `json:"address"`
	Key           string            `json:"key"`
	UserPublicKey string            `json:"user_public_key"`
	Created       string            `json:"created"`
	Note          string            `json:"note"`
	Labels        map[string]string `json:"labels"`
	Caps          []string          `json:"caps"`
	Description   string            `json:"description"`
	Role          string            `json:"role"`
	Host          string            `json:"host"`
}

func recordJSON(r store.Record) endpointRecordJSON {
	labels := r.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	caps := r.Caps
	if caps == nil {
		caps = []string{}
	}
	addr := r.Owner + "." + r.Group + "." + r.Instance
	if a, err := r.Address(); err == nil {
		addr = a.String()
	}
	return endpointRecordJSON{
		Owner: r.Owner, Group: r.Group, Instance: r.Instance, Address: addr, Key: strings.ReplaceAll(addr, ".", "_"), UserPublicKey: r.User,
		Created: r.Created, Note: r.Note, Labels: labels, Caps: caps, Description: r.Description, Role: r.Role, Host: r.Host,
	}
}

// recordSuffix is what is on record for the endpoint beyond its address and key, empty when nothing is.
func recordSuffix(r store.Record) string {
	var parts []string
	if r.Created != "" {
		parts = append(parts, "created "+config.Clean(r.Created))
	}
	if r.Note != "" {
		parts = append(parts, fmt.Sprintf("note %q", config.Clean(r.Note)))
	}
	if r.Role != "" {
		parts = append(parts, fmt.Sprintf("role %q", config.Clean(r.Role)))
	}
	if r.Host != "" {
		parts = append(parts, fmt.Sprintf("host %q", config.Clean(r.Host)))
	}
	if len(r.Labels) > 0 {
		keys := make([]string, 0, len(r.Labels))
		for k := range r.Labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			keys[i] = config.Clean(k) + "=" + config.Clean(r.Labels[k])
		}
		parts = append(parts, "labels "+strings.Join(keys, ","))
	}
	if len(r.Caps) > 0 {
		parts = append(parts, "caps "+config.Clean(strings.Join(r.Caps, ",")))
	}
	if r.Description != "" {
		parts = append(parts, fmt.Sprintf("description %q", config.Clean(r.Description)))
	}
	return strings.Join(parts, ", ")
}

// runEndpointList is `mashctl endpoint list`.
func runEndpointList(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	asJSON := jsonRequested(args)
	// usageErr answers a usage error: the sentence on stderr, and with --json the document on stdout.
	usageErr := func(code, msg string) int {
		fmt.Fprintf(stderr, "mashctl: endpoint list: %s\n", msg)
		if asJSON {
			writeJSON(stdout, struct {
				Schema    string               `json:"schema"`
				Outcome   string               `json:"outcome"`
				Endpoints []endpointRecordJSON `json:"endpoints"`
				Refusal   *refusalJSON         `json:"refusal"`
			}{"mashctl-endpoint-list@1", "error", []endpointRecordJSON{}, &refusalJSON{Code: code, Detail: msg}})
		}
		return 2
	}
	for _, a := range args {
		switch {
		case a == "--help" || a == "-h":
			fmt.Fprint(stdout, endpointListUsage)
			return 0
		case a == "--json":
		case strings.HasPrefix(a, "-"):
			return usageErr("unknown_flag", fmt.Sprintf("unknown %s; try: mashctl endpoint list --help", config.RejectedArg(a)))
		default:
			return usageErr("usage", "takes no argument; try: mashctl endpoint show ADDRESS for one endpoint, or mashctl endpoint list --help")
		}
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "mashctl: endpoint list: %v\n", err)
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
	items := make([]endpointRecordJSON, 0, len(recs))
	suffix := map[string]string{}
	for _, r := range recs {
		j := recordJSON(r)
		items = append(items, j)
		suffix[j.Address] = recordSuffix(r)
	}
	sort.Slice(items, func(a, b int) bool { return items[a].Address < items[b].Address })
	if asJSON {
		writeJSON(stdout, struct {
			Schema    string               `json:"schema"`
			Endpoints []endpointRecordJSON `json:"endpoints"`
		}{"mashctl-endpoint-list@1", items})
		return 0
	}
	if len(items) == 0 {
		fmt.Fprintln(stdout, "no endpoints")
		fmt.Fprintln(stdout, "make one with: mashctl endpoint create --help")
		return 0
	}
	for _, j := range items {
		line := fmt.Sprintf("%s/%s/%s  %s  %s", config.Clean(j.Owner), config.Clean(j.Group), config.Clean(j.Instance), config.Clean(j.Address), config.Clean(j.Key))
		if s := suffix[j.Address]; s != "" {
			line += "  " + s
		}
		fmt.Fprintln(stdout, line)
	}
	noun := "endpoints"
	if len(items) == 1 {
		noun = "endpoint"
	}
	fmt.Fprintf(stderr, "%d %s\n", len(items), noun)
	return 0
}
