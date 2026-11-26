package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/config"
)

const endpointShowUsage = `Usage: mashctl endpoint show <address> [--json]
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
	Address      string            `json:"address"`
	Online       bool              `json:"online"`
	Suspended    bool              `json:"suspended"`
	Created      string            `json:"created"`
	Waiting      uint64            `json:"waiting"`
	Role         *string           `json:"role"`
	Host         *string           `json:"host"`
	Description  *string           `json:"description"`
	Capabilities []string          `json:"capabilities"`
	Note         *string           `json:"note"`
	Labels       map[string]string `json:"labels"`
	PublicKey    string            `json:"public_key"`
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
			caps := rec.Caps
			if caps == nil {
				caps = []string{}
			}
			writeJSON(stdout, showJSON{Address: rt.in.addr.String(), Online: st.Online, Suspended: st.Suspended, Created: utcRFC3339(rec.Created), Waiting: st.Waiting,
				Role: nullIfEmpty(rec.Role), Host: nullIfEmpty(rec.Host), Description: nullIfEmpty(rec.Description), Capabilities: caps, Note: nullIfEmpty(rec.Note),
				Labels: labels, PublicKey: rec.User})
			return nil
		}
		c := config.Clean
		rows := []cliframe.Row{{Label: "address", Value: rt.in.addr.String()}, {Label: "state", Value: stateWord(st)}}
		created := "-"
		if rec.Created != "" {
			created = utcMinute(rec.Created) + " UTC"
		}
		rows = append(rows, cliframe.Row{Label: "created", Value: created}, cliframe.Row{Label: "waiting", Value: messages(st.Waiting)})
		for _, f := range []struct{ name, value string }{{"role", rec.Role}, {"host", rec.Host}, {"description", rec.Description},
			{"capability", strings.Join(rec.Caps, ",")}, {"note", rec.Note}} {
			if f.value != "" {
				rows = append(rows, cliframe.Row{Label: f.name, Value: c(f.value)})
			}
		}
		rows = append(rows, cliframe.Row{Label: "labels", Value: labelPairs(rec.Labels)}, cliframe.Row{Label: "public key", Value: c(rec.User)})
		fmt.Fprintln(stdout, cliframe.Block(rows...))
		return nil
	})
}

// nullIfEmpty is the JSON value of an optional text: null when unset, never "".
func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
