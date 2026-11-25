package main

import (
	"fmt"
	"io"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/config"
)

const endpointSuspendUsage = `usage: mashctl endpoint suspend <address> [--json]
  address  the endpoint, as owner.group.name
  --json   print the result as JSON
  Refuses new mail to the endpoint. Its stored mail and its credentials stay.
Example:
  mashctl endpoint suspend acme.demo.bob
`

const endpointResumeUsage = `usage: mashctl endpoint resume <address> [--json]
  address  the endpoint, as owner.group.name
  --json   print the result as JSON
  Undoes suspend: the endpoint takes new mail again.
Example:
  mashctl endpoint resume acme.demo.bob
`

const endpointPermissionsUsage = `usage: mashctl endpoint permissions <address> [--json]
  address  the endpoint, as owner.group.name
  --json   accepted; the output is JSON either way
  Prints the NATS permissions the endpoint's user needs, as JSON, for someone
  who runs their own NATS. It reads no server.
Example:
  mashctl endpoint permissions acme.demo.bob
`

var endpointSuspendSpec = epSpec{
	name: "suspend", usage: endpointSuspendUsage, min: 1, max: 1,
	need: "the endpoint address (owner.group.name)", check: checkAddress,
}

var endpointResumeSpec = epSpec{
	name: "resume", usage: endpointResumeUsage, min: 1, max: 1,
	need: "the endpoint address (owner.group.name)", check: checkAddress,
}

var endpointPermissionsSpec = epSpec{
	name: "permissions", usage: endpointPermissionsUsage, min: 1, max: 1, offline: true,
	need: "the endpoint address (owner.group.name)", check: checkAddress,
}

// stateResultJSON is the JSON of suspend and resume.
type stateResultJSON struct {
	Address   string `json:"address"`
	Online    bool   `json:"online"`
	Suspended bool   `json:"suspended"`
}

// runEndpointSuspend is `mashctl endpoint suspend` (ADR 0057: the inbox stream's subjects are parked).
func runEndpointSuspend(args []string, g cliframe.Global, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	return runEp(endpointSuspendSpec, g, args, env, lookup, stdout, stderr, func(rt *epRuntime) error {
		return changeSuspension(rt, true)
	})
}

// runEndpointResume is `mashctl endpoint resume`.
func runEndpointResume(args []string, g cliframe.Global, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	return runEp(endpointResumeSpec, g, args, env, lookup, stdout, stderr, func(rt *epRuntime) error {
		return changeSuspension(rt, false)
	})
}

// changeSuspension is the body of suspend and resume.
func changeSuspension(rt *epRuntime, suspend bool) error {
	addr := rt.in.addr
	done := "Suspended"
	if !suspend {
		done = "Resumed"
	}
	if suspend {
		if _, err := rt.adm.Suspend(rt.ctx, addr); err != nil {
			return failErr(err)
		}
	} else if err := rt.adm.Resume(rt.ctx, addr); err != nil {
		return failErr(err)
	}
	st, err := rt.adm.EndpointState(rt.ctx, addr, false)
	if err != nil {
		return failErr(err)
	}
	if rt.in.json {
		writeJSON(rt.stdout, stateResultJSON{Address: addr.String(), Online: st.Online, Suspended: st.Suspended})
		return nil
	}
	fmt.Fprintf(rt.stdout, "%s endpoint %s\n", done, addr)
	return nil
}

// permLists is the allow and deny lists of one direction; both are always printed, an empty list as [].
type permLists struct {
	Allow []string `json:"allow"`
	Deny  []string `json:"deny"`
}

// permsJSON is the permissions object of a NATS user JWT, with NATS's own key names.
type permsJSON struct {
	Pub permLists `json:"pub"`
	Sub permLists `json:"sub"`
}

// permissionsJSON copies p into the printed shape, so that deny is never left out.
func permissionsJSON(p *jwt.Permissions) permsJSON {
	list := func(l []string) []string {
		if l == nil {
			return []string{}
		}
		return l
	}
	return permsJSON{
		Pub: permLists{Allow: list(p.Pub.Allow), Deny: list(p.Pub.Deny)},
		Sub: permLists{Allow: list(p.Sub.Allow), Deny: list(p.Sub.Deny)},
	}
}

// runEndpointPermissions is `mashctl endpoint permissions`: the permission set as JSON on stdout, nothing else.
func runEndpointPermissions(args []string, g cliframe.Global, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	return runEp(endpointPermissionsSpec, g, args, env, lookup, stdout, stderr, func(rt *epRuntime) error {
		if err := cliframe.WriteJSON(rt.stdout, permissionsJSON(admin.Permissions(rt.in.addr))); err != nil {
			return failErr(err)
		}
		return nil
	})
}
