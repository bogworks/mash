package cmdcore

import (
	"slices"
	"strings"

	"github.com/bogworks/mash/internal/cliframe"
)

// NoSuchEndpoint is the failure for an address nothing answers to (exit 1).
func NoSuchEndpoint(addr string) *cliframe.Error {
	return cliframe.Failf("no endpoint has the address %s", addr).WithHint("mash who")
}

// RefusedMessage is the failure for the number of a message serve refused because it was not verified (exit 1).
func RefusedMessage(n int) *cliframe.Error {
	return cliframe.Failf("message %d was refused: it was not verified", n).WithHint("mash doctor")
}

// NotSignedIn is the failure for an endpoint whose credentials the server refused (exit 1).
func NotSignedIn(addr string) *cliframe.Error {
	return cliframe.Failf("%s cannot sign in: the server refused its credentials", addr).WithHint("mash doctor")
}

// ServerNotAnswering is the failure for a server that could not be reached or did not answer (exit 1).
func ServerNotAnswering(url string) *cliframe.Error {
	return cliframe.Failf("the server did not answer: %s", url).WithHint("mash doctor")
}

// errNoEnv is the failure for a command run without an environment: a programming error, never a panic.
func errNoEnv(name string) *cliframe.Error {
	return cliframe.Failf("internal: mash %s has no environment to run in", name)
}

// NoEndpointKind says where having no endpoint was found out, which decides its words (rules.json: "Having no endpoint
// reads the same in every mash command").
type NoEndpointKind int

const (
	// NoneInstalled is a shell with no endpoint chosen and none installed (exit 2).
	NoneInstalled NoEndpointKind = iota
	// SeveralInstalled is a shell with no endpoint chosen and several installed (exit 2).
	SeveralInstalled
	// NoneForSession is an MCP tool in a session that names no endpoint (exit 1).
	NoneForSession
)

// SessionNoEndpointHint is the hint of NoneForSession.
const SessionNoEndpointHint = "start the session with MASH_ENDPOINT=<address>"

// NoEndpoint is the one place the three wordings of having no endpoint are made. command is the command being run (the
// hint of SeveralInstalled names it: "mash send --endpoint acme.demo.alice"); addresses are the installed endpoints,
// for SeveralInstalled only.
func NoEndpoint(kind NoEndpointKind, command string, addresses []string) *cliframe.Error {
	switch kind {
	case SeveralInstalled:
		sorted := slices.Sorted(slices.Values(addresses))
		hint := "mash " + command + " --endpoint"
		if len(sorted) > 0 {
			hint += " " + sorted[0]
		} else {
			hint += " <address>"
		}
		return cliframe.Usagef("more than one endpoint is installed here: %s", strings.Join(sorted, ", ")).WithHint(hint)
	case NoneForSession:
		return cliframe.Failf("No endpoint is chosen for this session.").WithHint(SessionNoEndpointHint)
	}
	return cliframe.Usagef("no endpoint is installed here").WithHint("mash join <file>")
}
