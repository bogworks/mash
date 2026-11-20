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
// --file). An unknown option, a missing value, a bad enum value or number, a missing required positional and surplus
// positionals are usage errors (exit 2). On success the result's Text, or with --json its compact JSON, goes to stdout.
func CLI[P any, R Result](c Command[P, R], env EnvFunc) CLIFunc {
	fields := paramFields(reflect.TypeOf((*P)(nil)).Elem())
	return func(args []string, stdin io.Reader, stdout, stderr io.Writer, g cliframe.Global) int {
		var p P
		if err := parseArgs(c.Name, fields, args, reflect.ValueOf(&p).Elem()); err != nil {
			return cliframe.Report(stdout, stderr, err, g.JSON)
		}
		ctx := context.Background()
		e, err := env(ctx)
		if err != nil {
			return cliframe.Report(stdout, stderr, err, g.JSON)
		}
		tty := isTTY(stdout)
		e.Opts = Opts{Verbose: g.Verbose, NoColor: g.NoColor || os.Getenv("NO_COLOR") != "", TTY: tty,
			Width: render.Width(os.Getenv, tty)}
		e.Stdin, e.StdinPiped = stdin, piped(stdin)
		r, err := c.Run(ctx, e, p)
		if err != nil {
			return cliframe.Report(stdout, stderr, err, g.JSON)
		}
		if g.JSON {
			b, err := marshal(r)
			if err != nil {
				return cliframe.Report(stdout, stderr, err, g.JSON)
			}
			fmt.Fprintf(stdout, "%s\n", b)
			return 0
		}
		fmt.Fprintf(stdout, "%s\n", r.Text(e.Opts))
		return 0
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

// piped reports whether r is something to read from: a pipe, a file, or any reader that is not a terminal.
func piped(r io.Reader) bool {
	if r == nil {
		return false
	}
	if f, ok := r.(*os.File); ok {
		fi, err := f.Stat()
		return err == nil && fi.Mode()&os.ModeCharDevice == 0
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
		return cliframe.Usagef("unknown option %s for mash %s", a, name).WithHint("mash " + name + " --help")
	}
	usage := func(format string, a ...any) error {
		return cliframe.Usagef(format, a...).WithHint("mash " + name + " --help")
	}
	npos := 0
	onlyPos := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if onlyPos || a == "-" || !strings.HasPrefix(a, "-") {
			if npos >= len(pos) {
				return usage("unexpected argument %q for mash %s", a, name)
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
		fv := pv.Field(f.index)
		if f.typ.Kind() == reflect.Bool {
			b := true
			if hasVal {
				var err error
				if b, err = strconv.ParseBool(val); err != nil {
					return usage("%s takes yes or no, not %q", key, val)
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
		ok := false
		for _, e := range f.enum {
			ok = ok || e == s
		}
		if !ok {
			return fmt.Errorf("must be one of %s", strings.Join(f.enum, ", "))
		}
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
			return fmt.Errorf("must be a whole number, not %q", s)
		}
		v.SetInt(int64(n))
	case reflect.Slice:
		v.Set(reflect.Append(v, reflect.ValueOf(s)))
	}
	return nil
}
