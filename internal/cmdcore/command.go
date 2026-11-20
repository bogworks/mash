// Package cmdcore defines a command once and serves it through both doors: the command line and an MCP tool. A command
// is a params struct P, a result struct R and a Run function. Tags on P give the flag, positional, JSON name,
// description and enum; R's JSON tags are the --json object and the MCP structured content; R's Text method is the human
// output and the MCP text.
//
// Param tags: json:"name" (MCP), flag:"--name" and optional short:"-n" (CLI option), arg:"name" or arg:"name,optional"
// (CLI positional, in field order), desc:"...", mcpdesc:"..." (replaces desc in the MCP schema), enum:"a,b",
// required:"true" (required in the MCP schema), repeat:"true" (a []string given by repeating the flag), value:"number"
// (the placeholder for a flag's value in --help), min:"1" (the smallest whole number an integer field takes). Field types
// are string, bool, int, *int and []string.
package cmdcore

import (
	"context"
	"io"
	"time"

	"github.com/bogworks/mash/internal/cmdcore/render"
)

// Opts are the output options Text receives.
type Opts = render.Opts

// Result is what a command returns: a struct whose JSON form is the structured result, and the text for people.
type Result interface {
	Text(o Opts) string
}

// Env is what a command runs against. Svc is the command package's own service, asserted to the interface it needs, so
// tests can fake it.
type Env struct {
	Svc  any
	Now  func() time.Time
	Opts Opts
	// Stdin is the standard input of a CLI run, nil for an MCP call. StdinPiped says it is a pipe or file rather than a
	// terminal.
	Stdin      io.Reader
	StdinPiped bool
	// StdinTimeout bounds a read of Stdin: a pipe that never writes would otherwise block the command for ever. Zero
	// means no bound.
	StdinTimeout time.Duration
	// ReadFile reads a file for --file style options; nil means os.ReadFile.
	ReadFile func(path string) ([]byte, error)
}

// EnvFunc makes the Env for one run.
type EnvFunc func(ctx context.Context) (*Env, error)

// Command is one command: its CLI name (the verb, also the MCP tool name), a one-line summary, the one-line description
// and the examples that --help prints, and its Run. MCPDescription is the MCP tool's description, written for the model;
// when empty the tool's description is the Summary. Group is the heading the tool's own help lists the command under.
type Command[P any, R Result] struct {
	Name string
	// Group is the heading the tool's own help lists the command under (the specification's x-group): Everyday, Setup,
	// Integration or Raw SAP.
	Group          string
	Summary        string
	MCPDescription string
	Description    string
	Examples       []string
	Run            func(ctx context.Context, env *Env, p P) (R, error)
}
