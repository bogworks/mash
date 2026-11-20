package cmdcore

import (
	"io"
	"sort"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/mcp"
)

// Entry is one registered command, usable through both doors without its type parameters.
type Entry interface {
	CLIName() string
	Summary() string
	Tool(env EnvFunc) Tool
	RunCLI(env EnvFunc, args []string, stdin io.Reader, stdout, stderr io.Writer, g cliframe.Global) int
}

type entry[P any, R Result] struct{ c Command[P, R] }

// Register wraps c as an Entry.
func Register[P any, R Result](c Command[P, R]) Entry { return entry[P, R]{c} }

func (e entry[P, R]) CLIName() string { return e.c.Name }
func (e entry[P, R]) Summary() string { return e.c.Summary }
func (e entry[P, R]) Tool(env EnvFunc) Tool {
	return MCPTool(e.c, env)
}
func (e entry[P, R]) RunCLI(env EnvFunc, args []string, stdin io.Reader, stdout, stderr io.Writer, g cliframe.Global) int {
	return CLI(e.c, env)(args, stdin, stdout, stderr, g)
}

// Registry is one table of commands that both doors range over.
type Registry struct {
	Entries []Entry
	Env     EnvFunc
}

// NewRegistry returns a Registry of entries that make their Env with env.
func NewRegistry(env EnvFunc, entries ...Entry) *Registry {
	return &Registry{Entries: entries, Env: env}
}

// Lookup returns the entry for a CLI verb.
func (r *Registry) Lookup(name string) (Entry, bool) {
	for _, e := range r.Entries {
		if e.CLIName() == name {
			return e, true
		}
	}
	return nil, false
}

// Names returns the CLI verbs in sorted order.
func (r *Registry) Names() []string {
	var n []string
	for _, e := range r.Entries {
		n = append(n, e.CLIName())
	}
	sort.Strings(n)
	return n
}

// Tools returns every command as an MCP tool, in registration order.
func (r *Registry) Tools() []Tool {
	var t []Tool
	for _, e := range r.Entries {
		t = append(t, e.Tool(r.Env))
	}
	return t
}

// MCPTools returns Tools converted for the MCP server.
func (r *Registry) MCPTools() []mcp.Tool {
	var t []mcp.Tool
	for _, x := range r.Tools() {
		t = append(t, x.MCP())
	}
	return t
}

// RunCLI runs the command named name. The caller has checked Lookup.
func (r *Registry) RunCLI(name string, args []string, stdin io.Reader, stdout, stderr io.Writer, g cliframe.Global) int {
	e, ok := r.Lookup(name)
	if !ok {
		return cliframe.Report(stdout, stderr, cliframe.Usagef("unknown command %s", name).WithHint("mash --help"), g.JSON)
	}
	return e.RunCLI(r.Env, args, stdin, stdout, stderr, g)
}
