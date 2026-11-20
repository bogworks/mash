package cmdcore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore/render"
)

// CLIFunc runs one command from its arguments (the global options already removed by cliframe.Extract) and returns the
// process exit code.
type CLIFunc func(args []string, stdin io.Reader, stdout, stderr io.Writer, g cliframe.Global) int

// CLI adapts c to the command line.
//
// Arguments are parsed from the params tags: positionals in field order, --flag value, --flag=value, short flags, bool
// flags, repeated flags for []string, and "--" to end the options. A bare "-" is a positional (standard input for
// --file, and for send's message). An unknown option, a missing value, a bad enum value, number or boolean, an empty required positional, a
// single-value option given twice and surplus positionals are usage errors (exit 2). On success the result's Text, or with --json its compact JSON, goes to stdout; a result that is an ExitCoder sets the exit code and one that is a Hinter adds a hint line on stderr.
func CLI[P any, R Result](c Command[P, R], env EnvFunc) CLIFunc {
	return CLIWith(c, env, Frame{Tool: "mash"})
}

// CLIWith is CLI with the frame that --help prints around the command. --help or -h anywhere before a "--" prints the
// help on stdout and exits 0, and nothing else is read or run.
func CLIWith[P any, R Result](c Command[P, R], env EnvFunc, frame Frame) CLIFunc {
	fields := paramFields(reflect.TypeOf((*P)(nil)).Elem())
	return func(args []string, stdin io.Reader, stdout, stderr io.Writer, g cliframe.Global) int {
		if wantsHelp(fields, args) {
			fmt.Fprint(stdout, helpText(c, fields, frame))
			return 0
		}
		var p P
		if err := parseArgs(c.Name, fields, args, reflect.ValueOf(&p).Elem()); err != nil {
			return cliframe.Report(stdout, stderr, err, g.JSON)
		}
		ctx := context.Background()
		if env == nil {
			return cliframe.Report(stdout, stderr, errNoEnv(c.Name), g.JSON)
		}
		e, err := env(ctx)
		if err != nil {
			return cliframe.Report(stdout, stderr, err, g.JSON)
		}
		if e == nil {
			return cliframe.Report(stdout, stderr, errNoEnv(c.Name), g.JSON)
		}
		tty := isTTY(stdout)
		e.Opts = Opts{Detail: g.Detail, NoColor: g.NoColor || os.Getenv("NO_COLOR") != "", TTY: tty,
			Width: render.Width(os.Getenv, tty)}
		e.Stdin, e.StdinPiped = stdin, piped(stdin)
		r, err := c.Run(ctx, e, p)
		if err != nil {
			return cliframe.Report(stdout, stderr, err, g.JSON)
		}
		code := 0
		if x, ok := any(r).(ExitCoder); ok {
			code = x.ExitCode()
		}
		var detail Detailer
		if g.Detail {
			detail, _ = any(r).(Detailer)
		}
		if g.JSON {
			var v any = r
			if detail != nil {
				v = detail.DetailValue()
			}
			b, err := marshal(v)
			if err != nil {
				return cliframe.Report(stdout, stderr, err, g.JSON)
			}
			fmt.Fprintf(stdout, "%s\n", b)
			return code
		}
		out := r.Text(e.Opts)
		if detail != nil {
			if more := detail.DetailText(e.Opts); more != "" {
				out += "\n" + more
			}
		}
		fmt.Fprintf(stdout, "%s\n", out)
		if h, ok := any(r).(Hinter); ok && h.Hint() != "" {
			fmt.Fprintf(stderr, "hint: %s\n", h.Hint())
		}
		return code
	}
}

func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// piped reports whether r is something to read the text from when none was given: a pipe or a regular file. A terminal,
// /dev/null and a socket are not. Any reader that is not an *os.File counts (a test's strings.Reader).
func piped(r io.Reader) bool {
	if r == nil {
		return false
	}
	if f, ok := r.(*os.File); ok {
		fi, err := f.Stat()
		return err == nil && (fi.Mode()&os.ModeNamedPipe != 0 || fi.Mode().IsRegular())
	}
	return true
}

// marshal is compact JSON without HTML escaping.
func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

func parseArgs(name string, fields []field, args []string, pv reflect.Value) error {
	var pos []field
	for _, f := range fields {
		if f.positional() {
			pos = append(pos, f)
		}
	}
	find := func(a string) *field {
		for i := range fields {
			if fields[i].flag != "" && fields[i].flag == a || fields[i].short != "" && fields[i].short == a {
				return &fields[i]
			}
		}
		return nil
	}
	unknown := func(a string) error {
		return cliframe.Usagef("unknown option %s", a).WithHint("mash " + name + " --help")
	}
	usage := func(format string, a ...any) error {
		return cliframe.Usagef(format, a...).WithHint("mash " + name + " --help")
	}
	npos := 0
	onlyPos := false
	given := map[int]bool{} // single-value flags seen
	for i := 0; i < len(args); i++ {
		a := args[i]
		if onlyPos || a == "-" || !strings.HasPrefix(a, "-") {
			if npos >= len(pos) {
				return usage("unexpected argument %q for mash %s", a, name)
			}
			if !pos[npos].optional && strings.TrimSpace(a) == "" {
				return usage("mash %s needs <%s>", name, pos[npos].arg)
			}
			if err := set(pv.Field(pos[npos].index), pos[npos], a); err != nil {
				return usage("%s %s", pos[npos].arg, err.Error())
			}
			npos++
			continue
		}
		if a == "--" {
			onlyPos = true
			continue
		}
		key, val, hasVal := a, "", false
		if strings.HasPrefix(a, "--") {
			key, val, hasVal = strings.Cut(a, "=")
		}
		f := find(key)
		if f == nil || f.positional() {
			return unknown(key)
		}
		if !f.repeat {
			if given[f.index] {
				return usage("%s was given twice", f.flag)
			}
			given[f.index] = true
		}
		fv := pv.Field(f.index)
		if f.typ.Kind() == reflect.Bool {
			b := true
			if hasVal {
				switch strings.ToLower(val) {
				case "true", "yes":
				case "false", "no":
					b = false
				default:
					return usage("%s must be true or false", key)
				}
			}
			fv.SetBool(b)
			continue
		}
		if !hasVal {
			if i+1 >= len(args) {
				return usage("%s needs a value", key)
			}
			i++
			val = args[i]
		}
		if val == "" {
			return usage("%s needs a value", key)
		}
		if err := set(fv, *f, val); err != nil {
			return usage("%s %s", key, err.Error())
		}
	}
	for i := npos; i < len(pos); i++ {
		if !pos[i].optional {
			return usage("mash %s needs <%s>", name, pos[i].arg)
		}
	}
	return nil
}

// set stores the command-line text s into v, checking the enum and the number.
func set(v reflect.Value, f field, s string) error {
	if len(f.enum) > 0 {
		c, ok := enumValue(f.enum, s)
		if !ok {
			return fmt.Errorf("must be one of %s", strings.Join(f.enum, ", "))
		}
		s = c
	}
	t := v.Type()
	if t.Kind() == reflect.Pointer {
		n := reflect.New(t.Elem())
		if err := set(n.Elem(), f, s); err != nil {
			return err
		}
		v.Set(n)
		return nil
	}
	switch t.Kind() {
	case reflect.String:
		v.SetString(s)
	case reflect.Int:
		n, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("must be a whole number")
		}
		if err := f.rangeError(int64(n)); err != nil {
			return err
		}
		v.SetInt(int64(n))
	case reflect.Slice:
		v.Set(reflect.Append(v, reflect.ValueOf(s)))
	}
	return nil
}

// enumValue finds s among the allowed values, ignoring case and surrounding spaces, and returns the allowed spelling.
func enumValue(enum []string, s string) (string, bool) {
	s = strings.TrimSpace(s)
	for _, e := range enum {
		if strings.EqualFold(e, s) {
			return e, true
		}
	}
	return "", false
}
