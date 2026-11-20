package cmdcore

import (
	"reflect"
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
	Desc  string
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

// helpText is the generated --help of c, in the one layout: the description, Usage, Examples, Arguments, Options and
// Global options, each section after a blank line, no line wider than 80 columns. A section with nothing in it is left
// out.
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
	if len(opts) > 0 {
		var rows [][2]string
		for _, o := range opts {
			v := o.value
			if v == "" && o.typ.Kind() != reflect.Bool {
				v = strings.TrimLeft(o.flag, "-")
			}
			rows = append(rows, [2]string{optionLabel(o.short, o.flag, v), o.desc})
		}
		sections = append(sections, "Options:\n"+table(rows))
	}
	if len(fr.Globals) > 0 {
		var rows [][2]string
		for _, g := range fr.Globals {
			rows = append(rows, [2]string{optionLabel(g.Short, g.Flag, g.Value), g.Desc})
		}
		sections = append(sections, "Global options:\n"+table(rows))
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

// ParentHelp is the --help of a command that only holds others (mash key): its description, Usage with one line per
// command under it, and the Global options, in the same layout and width as a command's own.
func ParentHelp(fr Frame, description string, usages []string) string {
	sections := []string{strings.Join(wrap(description, helpWidth), "\n"), "Usage:\n  " + strings.Join(usages, "\n  ")}
	var rows [][2]string
	for _, g := range fr.Globals {
		rows = append(rows, [2]string{optionLabel(g.Short, g.Flag, g.Value), g.Desc})
	}
	if len(rows) > 0 {
		sections = append(sections, "Global options:\n"+table(rows))
	}
	return strings.Join(sections, "\n\n") + "\n"
}
