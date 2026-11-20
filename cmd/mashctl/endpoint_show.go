package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/config"
)

const endpointShowUsage = `usage: mashctl endpoint show <address> [--json]
  address  the endpoint, as owner.group.name
  --json   print the endpoint as JSON
Example:
  mashctl endpoint show acme.demo.bob
`

var endpointShowSpec = epSpec{
	name: "show", usage: endpointShowUsage, min: 1, max: 1, need: "the endpoint address (owner.group.name)", check: checkAddress,
}

// showJSON is the JSON of one endpoint.
type showJSON struct {
	Address     string            `json:"address"`
	State       string            `json:"state"`
	Created     string            `json:"created"`
	Waiting     uint64            `json:"waiting"`
	Role        string            `json:"role,omitempty"`
	Host        string            `json:"host,omitempty"`
	Description string            `json:"description,omitempty"`
	Capability  []string          `json:"capability,omitempty"`
	Note        string            `json:"note,omitempty"`
	Labels      map[string]string `json:"labels"`
	PublicKey   string            `json:"public_key"`
}

// runEndpointShow is `mashctl endpoint show`.
func runEndpointShow(args []string, g cliframe.Global, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	return runEp(endpointShowSpec, g, args, env, lookup, stdout, stderr, func(rt *epRuntime) error {
		rec, err := rt.loadRecord(rt.in.addr)
		if err != nil {
			return err
		}
		st, err := rt.adm.EndpointState(rt.ctx, rt.in.addr, true)
		if err != nil {
			return failErr(err)
		}
		if rt.in.json {
			labels := rec.Labels
			if labels == nil {
				labels = map[string]string{}
			}
			writeJSON(stdout, showJSON{Address: rt.in.addr.String(), State: stateWord(st), Created: utcRFC3339(rec.Created), Waiting: st.Waiting,
				Role: rec.Role, Host: rec.Host, Description: rec.Description, Capability: rec.Caps, Note: rec.Note, Labels: labels, PublicKey: rec.User})
			return nil
		}
		c := config.Clean
		line := func(label, value string) { fmt.Fprintf(stdout, "%-11s %s\n", label+":", value) }
		line("address", rt.in.addr.String())
		line("state", stateWord(st))
		created := "-"
		if rec.Created != "" {
			created = utcMinute(rec.Created) + " UTC"
		}
		line("created", created)
		line("waiting", messages(st.Waiting))
		for _, f := range []struct{ name, value string }{{"role", rec.Role}, {"host", rec.Host}, {"description", rec.Description},
			{"capability", strings.Join(rec.Caps, ",")}, {"note", rec.Note}} {
			if f.value != "" {
				line(f.name, c(f.value))
			}
		}
		line("labels", labelPairs(rec.Labels))
		line("public key", c(rec.User))
		return nil
	})
}
