package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

const endpointListUsage = `usage: mashctl endpoint list [prefix] [--wide] [--json]
  prefix   only endpoints under this owner (acme) or owner.group (acme.demo)
  --wide   add the columns CREATED, WAITING and LABELS
  --json   print the endpoints as JSON
Example:
  mashctl endpoint list acme.demo
`

var endpointListSpec = epSpec{
	name: "list", usage: endpointListUsage, flags: []string{"--wide"}, min: 0, max: 1, need: "prefix",
	check: func(in *epInput) error {
		if len(in.pos) == 0 {
			return nil
		}
		parts := strings.Split(in.pos[0], ".")
		if len(parts) > 2 {
			return cliframe.Usagef("not an owner or owner.group prefix (the value is not shown): write acme or acme.demo").WithHint("mashctl endpoint list --help")
		}
		for i, p := range parts {
			var err error
			if i == 0 {
				_, err = sap.NewAddress(p, "g", "n")
			} else {
				_, err = sap.NewAddress(parts[0], p, "n")
			}
			if err != nil {
				return cliframe.Usagef("not an owner or owner.group prefix (the value is not shown): write acme or acme.demo").WithHint("mashctl endpoint list --help")
			}
		}
		in.prefix = parts
		return nil
	},
}

// listRow is one endpoint of the list.
type listRow struct {
	addr    sap.Address
	state   string
	created string
	waiting uint64
	labels  map[string]string
}

// listEntryJSON is one endpoint of the JSON list.
type listEntryJSON struct {
	Address string `json:"address"`
	Owner   string `json:"owner"`
	Group   string `json:"group"`
	Name    string `json:"name"`
	State   string `json:"state"`
}

// recordAddress is the address of a directory record; a record that does not name a valid one is shown by its parts.
func recordAddress(r store.Record) sap.Address {
	if a, err := r.Address(); err == nil {
		return a
	}
	return sap.Address{Owner: r.Owner, Group: r.Group, Instance: r.Instance}
}

// runEndpointList is `mashctl endpoint list`.
func runEndpointList(args []string, g cliframe.Global, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	return runEp(endpointListSpec, args, env, lookup, stdout, stderr, func(rt *epRuntime) error {
		recs, bad, err := rt.adm.List(rt.ctx)
		if err != nil {
			return failErr(err)
		}
		for _, b := range bad {
			b.Warn(stderr, "mashctl")
		}
		wide := rt.in.flags["--wide"]
		var rows []listRow
		for _, r := range recs {
			a := recordAddress(r)
			if p := rt.in.prefix; len(p) > 0 && (a.Owner != p[0] || (len(p) > 1 && a.Group != p[1])) {
				continue
			}
			st, err := rt.adm.EndpointState(rt.ctx, a, wide)
			if err != nil {
				return failErr(err)
			}
			rows = append(rows, listRow{addr: a, state: stateWord(st), created: r.Created, waiting: st.Waiting, labels: r.Labels})
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].addr.String() < rows[j].addr.String() })
		if rt.in.json {
			items := make([]listEntryJSON, 0, len(rows))
			for _, r := range rows {
				items = append(items, listEntryJSON{Address: r.addr.String(), Owner: r.addr.Owner, Group: r.addr.Group, Name: r.addr.Instance, State: r.state})
			}
			writeJSON(stdout, struct {
				Endpoints []listEntryJSON `json:"endpoints"`
			}{items})
			return nil
		}
		if len(rows) == 0 {
			fmt.Fprintln(stdout, "No endpoints.")
			return nil
		}
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		head := "OWNER\tGROUP\tNAME\tSTATE"
		if wide {
			head += "\tCREATED\tWAITING\tLABELS"
		}
		fmt.Fprintln(tw, head)
		var prev sap.Address
		for i, r := range rows {
			owner, group := config.Clean(r.addr.Owner), config.Clean(r.addr.Group)
			if i > 0 && r.addr.Owner == prev.Owner {
				owner = ""
				if r.addr.Group == prev.Group {
					group = ""
				}
			}
			prev = r.addr
			line := fmt.Sprintf("%s\t%s\t%s\t%s", owner, group, config.Clean(r.addr.Instance), r.state)
			if wide {
				created := "-"
				if r.created != "" {
					created = utcMinute(r.created)
				}
				line += fmt.Sprintf("\t%s\t%d\t%s", created, r.waiting, labelPairs(r.labels))
			}
			fmt.Fprintln(tw, line)
		}
		return failErr(tw.Flush())
	})
}
