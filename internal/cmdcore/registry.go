package cmdcore

import (
	"io"
	"reflect"
	"sort"
	"strings"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/mcp"
)

// Entry is one registered command, usable through both doors without its type parameters.
type Entry interface {
	CLIName() string
	Summary() string
	// Group is the heading the tool's own help lists the command under.
	Group() string
	// HasMCP is whether the command is also an MCP tool.
	HasMCP() bool
	Tool(env EnvFunc) Tool
	RunCLI(env EnvFunc, fr Frame, args []string, stdin io.Reader, stdout, stderr io.Writer, g cliframe.Global) int
	// HelpFor is the --help text when args ask for it, and whether they do: it reads nothing and runs nothing.
	HelpFor(fr Frame, args []string) (string, bool)
	// Usage is the synopsis, "mash send <address> [<message>] [options]", and the one-line description.
	Usage(tool string) (synopsis, description string)
}

type entry[P any, R Result] struct{ c Command[P, R] }

// Register wraps c as an Entry.
// It panics when the tags of the params type P are inconsistent (see paramFields).
func Register[P any, R Result](c Command[P, R]) Entry {
	paramFields(reflect.TypeOf((*P)(nil)).Elem())
	return entry[P, R]{c}
}

func (e entry[P, R]) CLIName() string { return e.c.Name }
func (e entry[P, R]) Summary() string { return e.c.Summary }
func (e entry[P, R]) Group() string   { return e.c.Group }
func (e entry[P, R]) HasMCP() bool    { return !e.c.NoMCP }
func (e entry[P, R]) Tool(env EnvFunc) Tool {
	return MCPTool(e.c, env)
}
func (e entry[P, R]) RunCLI(env EnvFunc, fr Frame, args []string, stdin io.Reader, stdout, stderr io.Writer, g cliframe.Global) int {
	return CLIWith(e.c, env, fr)(args, stdin, stdout, stderr, g)
}
func (e entry[P, R]) Usage(tool string) (string, string) {
	var args []field
	hasOpts := false
	for _, f := range paramFields(reflect.TypeOf((*P)(nil)).Elem()) {
		switch {
		case f.positional():
			args = append(args, f)
		case f.flag != "":
			hasOpts = true
		}
	}
	if tool == "" {
		tool = "mash"
	}
	d := e.c.Description
	if d == "" {
		d = e.c.Summary
	}
	return synopsis(tool, e.c.Name, args, hasOpts), d
}
func (e entry[P, R]) HelpFor(fr Frame, args []string) (string, bool) {
	fields := paramFields(reflect.TypeOf((*P)(nil)).Elem())
	if !wantsHelp(fields, args) {
		return "", false
	}
	return helpText(e.c, fields, fr), true
}

// Registry is one table of commands that both doors range over.
type Registry struct {
	Entries []Entry
	Env     EnvFunc
	Frame   Frame // what --help prints around each command
}

// NewRegistry returns a Registry of entries that make their Env with env.
func NewRegistry(env EnvFunc, entries ...Entry) *Registry {
	return &Registry{Entries: entries, Env: env}
}

// WithFrame sets what --help prints around each command, and returns r.
func (r *Registry) WithFrame(fr Frame) *Registry {
	r.Frame = fr
	return r
}

// HelpFor is the --help text of the command named name when args ask for it.
func (r *Registry) HelpFor(name string, args []string) (string, bool) {
	e, ok := r.Lookup(name)
	if !ok {
		return "", false
	}
	return e.HelpFor(r.Frame, args)
}

// ListLine is the line the tool's own help gives the command named name: two spaces, its synopsis, and its one-line
// description starting at column col (on a line of its own, indented to col, when the synopsis is too long), wrapped to
// 80 columns. It is "" when there is no such command.
func (r *Registry) ListLine(name string, col int) string {
	e, ok := r.Lookup(name)
	if !ok {
		return ""
	}
	use, desc := e.Usage(r.Frame.Tool)
	return ListLine(use, desc, col)
}

// ListLine is one entry of a tool's command list: two spaces, the synopsis, and the description from column col, wrapped
// to 80 columns with its continuation lines indented to col. When the synopsis reaches col the description starts on a
// line of its own.
func ListLine(synopsis, desc string, col int) string {
	line := "  " + synopsis
	lines := wrap(desc, helpWidth-col)
	if len(lines) == 0 {
		return line
	}
	pad := strings.Repeat(" ", col)
	if len(line)+1 > col {
		return line + "\n" + pad + strings.Join(lines, "\n"+pad)
	}
	return line + strings.Repeat(" ", col-len(line)) + strings.Join(lines, "\n"+pad)
}

// Group returns the heading of the command named name ("" when there is no such command).
func (r *Registry) Group(name string) string {
	if e, ok := r.Lookup(name); ok {
		return e.Group()
	}
	return ""
}

// Groups returns the names of the table's commands under heading group, in table order.
func (r *Registry) Groups(group string) []string {
	var n []string
	for _, e := range r.Entries {
		if e.Group() == group {
			n = append(n, e.CLIName())
		}
	}
	return n
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

// Tools returns every command that has one as an MCP tool, in registration order.
func (r *Registry) Tools() []Tool {
	var t []Tool
	for _, e := range r.Entries {
		if !e.HasMCP() {
			continue
		}
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
		return cliframe.Report(stdout, stderr, cliframe.Usagef("unknown command %q", name), g.JSON)
	}
	return e.RunCLI(r.Env, r.Frame, args, stdin, stdout, stderr, g)
}
