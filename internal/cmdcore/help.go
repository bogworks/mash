package cmdcore

import (
	"reflect"
	"slices"
	"strings"

	"github.com/bogworks/mash/internal/cmdcore/render"
)

// helpWidth is the widest line --help prints.
const helpWidth = 80

// HelpOption is one global option as --help lists it.
type HelpOption struct {
	Flag  string // "--endpoint"
	Short string // "-v", or ""
	Value string // placeholder for the value, "address", or "" for a switch
	Desc  string // in the tool's own help
	// CmdDesc is the text in a command's own help when that differs from Desc (the tool's help says which commands need
	// the option, a command's help says only what it does); "" means Desc.
	CmdDesc string
}

// The global options that apply to a command, named by flag, for Command.Globals and StaticCommand.Globals. A command's
// help lists them under Options, after its own; the tool's own help is the only one with a Global options block.
var (
	// GlobalsAll are the connection options, --detail, --json, --no-color and --help: a command that reads the
	// configuration, talks to the server and prints an answer.
	GlobalsAll = []string{"--endpoint", "--creds", "--server", "--ca", "--config-dir", "--detail", "--json", "--no-color", "--help"}
	// GlobalsConnect are the connection options and --help: a command that has no --json form and nothing fuller to say
	// (prompt, serve, hook).
	GlobalsConnect = []string{"--endpoint", "--creds", "--server", "--ca", "--config-dir", "--help"}
	// GlobalsLocal are --config-dir, --detail, --json, --no-color and --help: a command that works on the configuration
	// folder and takes no connection option (join).
	GlobalsLocal = []string{"--config-dir", "--detail", "--json", "--no-color", "--help"}
	// GlobalsJSON are --json and --help: a command that reads no configuration (version, key create).
	GlobalsJSON = []string{"--json", "--help"}
)

// PickOptions are the rows (label, description) of the global options of fr named by flags, in fr's order, as a
// command's own help words them.
func PickOptions(fr Frame, flags []string) [][2]string {
	var rows [][2]string
	for _, g := range fr.Globals {
		if !slices.Contains(flags, g.Flag) {
			continue
		}
		d := g.CmdDesc
		if d == "" {
			d = g.Desc
		}
		rows = append(rows, [2]string{optionLabel(g.Short, g.Flag, g.Value), d})
	}
	return rows
}

// Frame is what --help prints around a command: the tool's name and its global options.
type Frame struct {
	Tool    string
	Globals []HelpOption
}

// wantsHelp reports whether args ask for help: -h or --help before a "--", not as the value of another option.
func wantsHelp(fields []field, args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return false
		case a == "-h" || a == "--help":
			return true
		case a == "-" || !strings.HasPrefix(a, "-") || strings.Contains(a, "="):
		default:
			for _, f := range fields {
				if f.flag != "" && (f.flag == a || f.short == a) && f.typ.Kind() != reflect.Bool {
					i++ // its value
					break
				}
			}
		}
	}
	return false
}

// helpText is the generated --help of c, in the one layout: the description, Usage, Examples, Arguments and Options (its
// own, then the global options that apply to it), each section after a blank line, no line wider than 80 columns. A
// section with nothing in it is left out.
func helpText[P any, R Result](c Command[P, R], fields []field, fr Frame) string {
	tool := fr.Tool
	if tool == "" {
		tool = "mash"
	}
	desc := c.Description
	if desc == "" {
		desc = c.Summary
	}
	var args, opts []field
	for _, f := range fields {
		switch {
		case f.positional():
			args = append(args, f)
		case f.flag != "":
			opts = append(opts, f)
		}
	}
	use := synopsis(tool, c.Name, args, len(opts) > 0)
	sections := []string{strings.Join(wrap(desc, helpWidth), "\n"), "Usage:\n  " + use}
	if len(c.Examples) > 0 {
		sections = append(sections, "Examples:\n  "+strings.Join(c.Examples, "\n  "))
	}
	if len(args) > 0 {
		var rows [][2]string
		for _, a := range args {
			rows = append(rows, [2]string{a.arg, a.desc})
		}
		sections = append(sections, "Arguments:\n"+table(rows))
	}
	var rows [][2]string
	for _, o := range opts {
		v := o.value
		if v == "" && o.typ.Kind() != reflect.Bool {
			v = strings.TrimLeft(o.flag, "-")
		}
		rows = append(rows, [2]string{optionLabel(o.short, o.flag, v), o.desc})
	}
	for _, r := range PickOptions(fr, c.Globals) {
		for flag, d := range c.GlobalDescs {
			if strings.Contains(r[0], flag) {
				r[1] = d
			}
		}
		rows = append(rows, r)
	}
	if len(rows) > 0 {
		sections = append(sections, "Options:\n"+table(rows))
	}
	return strings.Join(sections, "\n\n") + "\n"
}

// synopsis is the Usage line: the tool, the command, its positionals and [options] when it has any.
func synopsis(tool, name string, args []field, hasOpts bool) string {
	use := tool + " " + name
	for _, a := range args {
		if a.optional {
			use += " [<" + a.arg + ">]"
		} else {
			use += " <" + a.arg + ">"
		}
	}
	if hasOpts {
		use += " [options]"
	}
	return use
}

func optionLabel(short, flag, value string) string {
	s := flag
	if short != "" {
		s = short + ", " + flag
	}
	if value != "" {
		s += " <" + value + ">"
	}
	return s
}

// table lays out rows of (label, description) indented two spaces, the descriptions aligned and wrapped to fit.
func table(rows [][2]string) string {
	w := 0
	for _, r := range rows {
		if l := render.DisplayWidth(r[0]); l > w {
			w = l
		}
	}
	col := 2 + w + 2
	var out []string
	for _, r := range rows {
		lines := wrap(r[1], helpWidth-col)
		if len(lines) == 0 {
			lines = []string{""}
		}
		for i, l := range lines {
			label := ""
			if i == 0 {
				label = r[0]
			}
			line := "  " + label + strings.Repeat(" ", w-render.DisplayWidth(label)+2) + l
			out = append(out, strings.TrimRight(line, " "))
		}
	}
	return strings.Join(out, "\n")
}

// wrap breaks s into lines of at most width display columns at spaces. A word wider than a line is left whole.
func wrap(s string, width int) []string {
	var lines []string
	cur := ""
	for _, w := range strings.Fields(s) {
		switch {
		case cur == "":
			cur = w
		case render.DisplayWidth(cur)+1+render.DisplayWidth(w) <= width:
			cur += " " + w
		default:
			lines = append(lines, cur)
			cur = w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// ParentHelp is the --help of a command that only holds others (mash key): its description and Usage with one line per
// command under it, in the same layout and width as a command's own. The options are the subcommands'.
func ParentHelp(description string, usages []string) string {
	sections := []string{strings.Join(wrap(description, helpWidth), "\n"), "Usage:\n  " + strings.Join(usages, "\n  ")}
	return strings.Join(sections, "\n\n") + "\n"
}

// StaticCommand is a command that is not on the table (its parameters are not a struct) but whose --help is the same
// layout: the texts are the specification's. Arguments and Options are (label, description) rows, the label as printed
// ("address", "--wake-count <n>").
type StaticCommand struct {
	Name        string
	Description string
	Usage       string // the synopsis, "mash serve [options]"
	Examples    []string
	Arguments   [][2]string
	Options     [][2]string
	// Globals are the global options that apply (flag names); they are listed under Options after its own, worded as
	// PickOptions words them, except any row of Options already given for the same flag.
	Globals []string
	// Override replaces the text of a global option's row, by flag ("--endpoint").
	Override map[string]string
}

// StaticHelp is the --help of c in the layout of helpText: description, Usage, Examples, Arguments and Options (its own,
// then the global options that apply), each after a blank line, no line wider than 80 columns.
func StaticHelp(fr Frame, c StaticCommand) string {
	sections := []string{strings.Join(wrap(c.Description, helpWidth), "\n"), "Usage:\n  " + c.Usage}
	if len(c.Examples) > 0 {
		sections = append(sections, "Examples:\n  "+strings.Join(c.Examples, "\n  "))
	}
	if len(c.Arguments) > 0 {
		sections = append(sections, "Arguments:\n"+table(c.Arguments))
	}
	rows := slices.Clone(c.Options)
	for _, r := range PickOptions(fr, c.Globals) {
		for flag, d := range c.Override {
			if strings.Contains(r[0], flag) {
				r[1] = d
			}
		}
		rows = append(rows, r)
	}
	if len(rows) > 0 {
		sections = append(sections, "Options:\n"+table(rows))
	}
	return strings.Join(sections, "\n\n") + "\n"
}

// Glossary is the one paragraph of the tool's own help, in mash and in mashctl and nowhere else, that says what the
// words a newcomer meets first are. Already wrapped to 80 columns.
const Glossary = "An endpoint is one agent's mailbox, addressed owner.group.name; the owner, the\n" +
	"first part, is the account that holds it. SAP is the Synadia Agent Protocol,\n" +
	"NATS is the message server mash talks to, creds are the file that signs an\n" +
	"endpoint in to it, and a trust label ([verified] and the others) says whether a\n" +
	"message's sender is proven."

// GlobalOptions is the "Global options:" section of fr: the heading and the table (no trailing newline), "" when fr has none.
func GlobalOptions(fr Frame) string { return GlobalOptionsUnder(fr, "Global options:") }

// GlobalOptionsUnder is GlobalOptions with another heading line, "Global options (before or after the verb):".
func GlobalOptionsUnder(fr Frame, heading string) string {
	var rows [][2]string
	for _, g := range fr.Globals {
		rows = append(rows, [2]string{optionLabel(g.Short, g.Flag, g.Value), g.Desc})
	}
	if len(rows) == 0 {
		return ""
	}
	return heading + "\n" + table(rows)
}
