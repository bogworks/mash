package host

import "strings"

// How a host was chosen, as `mash status` says it.
const (
	howNamed   = "MASH_HOST"
	howAssumed = "assumed"
)

// Environment variables that mark a host. They are read in the order Detect lists them. Claude Code's is the one
// variable mash already reads for its session id; Codex sets none that reaches serve (its MCP environment is
// filtered), so it is recognised by the MCP client's name or by MASH_HOST.
const (
	envPi     = "PI_CODING_AGENT"
	envClaude = "CLAUDE_CODE_SESSION_ID"
)

// MCP clientInfo names, as each host sends them in initialize.
const (
	clientCodex  = "codex-mcp-client"
	clientPi     = "pi"
	clientClaude = "claude" // a prefix: Claude Code sends "claude-code"
)

// Detection is the host mash chose and how. There is no silent default: when nothing names the host, Assumed is true,
// the adapter is Claude Code's, and serve and status say so.
type Detection struct {
	Adapter Adapter
	// How is "MASH_HOST", "environment <VAR>", "MCP client <name>" or "assumed".
	How string
	// Client is the MCP client name the detection saw, for the assumed message; "" before one is seen.
	Client string
}

// Named is true when MASH_HOST chose the host: nothing else may change it.
func (d Detection) Named() bool { return d.How == howNamed }

// Assumed is true when nothing identified the host and Claude Code is only the fallback.
func (d Detection) Assumed() bool { return d.How == howAssumed }

// Pending is true while the MCP client's name could still decide the host: nothing named it and no environment
// variable marked it. Serve resolves it with WithClient after initialize.
func (d Detection) Pending() bool { return d.Assumed() }

// Detect chooses the host, in this order: (a) name, which is MASH_HOST; (b) the environment markers, PI_CODING_AGENT
// then CLAUDE_CODE_SESSION_ID; (d) Claude Code, flagged as assumed. Step (c), the MCP client's name, needs the
// initialize request and so only serve has it: WithClient. An unknown name is an error, never read as Claude.
func Detect(name string, env func(string) string) (Detection, error) {
	if name != "" {
		a, err := Select(name)
		return Detection{Adapter: a, How: howNamed}, err
	}
	get := func(k string) string {
		if env == nil {
			return ""
		}
		return env(k)
	}
	switch {
	case get(envPi) != "":
		return Detection{Adapter: Pi{}, How: "environment " + envPi}, nil
	case get(envClaude) != "":
		return Detection{Adapter: Claude{}, How: "environment " + envClaude}, nil
	}
	return Detection{Adapter: Claude{}, How: howAssumed}, nil
}

// WithClient settles a pending detection with the MCP client's name from initialize: "codex-mcp-client" is Codex, a
// name starting with "claude" is Claude Code, "pi" is Pi. Anything else leaves it assumed, remembering the name for the
// message. A detection that is not pending is returned as it is.
func (d Detection) WithClient(name string) Detection {
	if !d.Pending() {
		return d
	}
	d.Client = name
	var a Adapter
	switch {
	case name == clientCodex:
		a = Codex{}
	case name == clientPi:
		a = Pi{}
	case strings.HasPrefix(name, clientClaude):
		a = Claude{}
	default:
		return d
	}
	return Detection{Adapter: a, How: "MCP client " + name, Client: name}
}

// Line is the `host:` line of status: the host and how it was chosen.
func (d Detection) Line() string {
	return "host: " + d.Adapter.Name() + " (" + d.how() + ")"
}

func (d Detection) how() string {
	if !d.Assumed() {
		return d.How
	}
	return "assumed: " + d.why()
}

// why says what was missing and what to do.
func (d Detection) why() string {
	client := "no MCP client name seen"
	if d.Client != "" {
		client = "MCP client " + quote(d.Client) + " is not a known host"
	}
	return "MASH_HOST is unset, no host environment variable (" + envPi + ", " + envClaude + ") is set and " + client +
		"; set MASH_HOST to one of " + strings.Join(Known(), ", ") + " to choose"
}

// AssumedMessage is the one line serve writes to stderr when the host is still assumed once the MCP client is known.
func (d Detection) AssumedMessage() string {
	return "mash: host assumed to be " + d.Adapter.Name() + ": " + d.why()
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `'`) + `"` }
