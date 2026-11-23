package main

// The conformance test of mashctl. It proves the build matches the single source, spec/mashctl.json (the commands) and
// spec/rules.json (the rules), by walking the specification and asking the build. It is the twin of the test of the same
// name in cmd/mash. Only help and usage-error paths run: nothing here needs a server.
//
// Every check is a subtest named by what it checks. A check yields a list of problems, each saying what the
// specification expects and what the build gives. There are no waivers: no table lists a gap or a drift, and any
// mismatch fails.
//
// What it checks:
//
//	commands/<path>      mashctl <path> --help exits 0 and names the command path, each option (with its argument name)
//	                     and each argument the specification lists for it
//	toplevel/<verb>      every command of the specification is a verb of the build's mashctl --help
//	toplevel/unlisted    every verb of the build's mashctl --help is a command of the specification
//	removed/<verb>       the verbs and options the specification removed are unknown to the build
//	width/<path>         every --help output is at most 80 columns per line
//	bad-option/<path>    an unknown option exits 2: 'error: unknown option --bogus', then 'hint: mashctl <path> --help'
//	bad-option-json/...  the same with --json: {error, hint} on stdout, nothing on stderr
//	usage-exit/<path>    a missing required argument exits 2, and no usage error gives a code other than 2
//	extra-option/...     an option a --help lists that the specification lacks for the command (its own and the global ones)
//	extra-command/...    a subcommand a --help lists that the specification lacks for the command, and the other way round
//	outputs/<path> <case>  every x-output case of the specification (text, stream, exit code) run against the build and
//	                     compared exactly (conformance_outputs_test.go); a case with no runner fails

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// confFail reports every problem of one check as a failure. There are no waivers: a mismatch fails.
func confFail(t *testing.T, key string, problems []string) {
	t.Helper()
	for _, p := range problems {
		t.Errorf("%s: %s", key, p)
	}
}

type confArg struct {
	Name     string
	Required bool
}

type confOpt struct {
	Name      string
	Aliases   []string
	Arguments []confArg
	Recursive bool
}

type confExit struct{ Code int }

type confCmd struct {
	Name      string
	Arguments []confArg
	Options   []confOpt
	Commands  []confCmd
	ExitCodes []confExit
	Metadata  []struct {
		Name  string
		Value json.RawMessage
	}
}

// meta is the string value of the metadata entry name ("" when there is none).
func (c confCmd) meta(name string) string {
	for _, m := range c.Metadata {
		if m.Name == name {
			var s string
			if json.Unmarshal(m.Value, &s) == nil {
				return s
			}
		}
	}
	return ""
}

func loadConfSpec(t *testing.T) confCmd {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "spec", "mashctl.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m struct{ Command confCmd }
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("spec/mashctl.json: %v", err)
	}
	return m.Command
}

// confWalk calls f with every command under root and its path ("endpoint create"), parents before children.
func confWalk(root confCmd, f func(path string, c confCmd)) {
	var rec func(prefix string, c confCmd)
	rec = func(prefix string, c confCmd) {
		path := strings.TrimSpace(prefix + " " + c.Name)
		f(path, c)
		for _, s := range c.Commands {
			rec(path, s)
		}
	}
	for _, c := range root.Commands {
		rec("", c)
	}
}

// confEffectiveOptions is, for each command path, the options that apply to it: its own and the recursive ones of every
// command above it (the root's are the global options).
func confEffectiveOptions(root confCmd) map[string][]confOpt {
	out := map[string][]confOpt{}
	var rec func(prefix string, c confCmd, inherited []confOpt)
	rec = func(prefix string, c confCmd, inherited []confOpt) {
		path := strings.TrimSpace(prefix + " " + c.Name)
		all := append(append([]confOpt{}, inherited...), c.Options...)
		out[path] = all
		var pass []confOpt
		for _, o := range all {
			if o.Recursive {
				pass = append(pass, o)
			}
		}
		for _, s := range c.Commands {
			rec(path, s, pass)
		}
	}
	var global []confOpt
	for _, o := range root.Options {
		if o.Recursive {
			global = append(global, o)
		}
	}
	for _, c := range root.Commands {
		rec("", c, global)
	}
	return out
}

// specCommand is a command of the specification and its path.
type specCommand struct {
	path string
	c    confCmd
}

// confTrack names the release the build is held to.
func confTrack(c confCmd) bool { return c.meta("x-release") == "0.5.6" }

type confRan struct {
	out, errs string
	code      int
}

// confRun runs mashctl in this process with no configuration and nothing of the test process's own environment.
func confRun(t *testing.T, args ...string) confRan {
	t.Helper()
	d := t.TempDir()
	env := map[string]string{"HOME": d, "MASH_CONFIG_DIR": d}
	var out, errb bytes.Buffer
	code := runEnv(args, func(k string) string { return env[k] }, &out, &errb)
	return confRan{out.String(), errb.String(), code}
}

// confPositional is a value for each argument the command takes, enough to get past the argument count.
func confPositional(c confCmd) []string {
	var p []string
	for _, a := range c.Arguments {
		switch a.Name {
		case "key=value":
			p = append(p, "k=v")
		case "key":
			p = append(p, "k")
		default:
			p = append(p, "a.b.c")
		}
	}
	return p
}

// confHelp is what a command's --help lists: each option with the argument text of every place it is listed, and each
// subcommand.
type confHelp struct {
	opts map[string][]string
	subs map[string]bool
}

var confWordRE = regexp.MustCompile(`^[a-z][a-z-]*$`)

// confScanOptions adds the options of one stretch of help text (a synopsis line or the head of an option line):
// a token that starts with - names an option, and the token after it is its argument unless that is another option or
// a bracketed group.
func confScanOptions(h confHelp, text string) {
	toks := strings.Fields(text)
	for i, tok := range toks {
		c := strings.Trim(tok, "[],")
		if len(c) < 2 || c[0] != '-' || c == "--" {
			continue
		}
		name, _, _ := strings.Cut(c, "=")
		arg := ""
		if i+1 < len(toks) {
			n := toks[i+1]
			if !strings.HasPrefix(n, "[") && !strings.HasPrefix(strings.Trim(n, "[],"), "-") {
				arg = strings.Trim(n, "[],")
			}
		}
		h.opts[name] = append(h.opts[name], arg)
	}
}

// confParseHelp reads the --help of the command at path. An option is listed where a line of the help starts with it
// (after its indentation), as '  --out PATH  write ...' does, or inside a synopsis line ('mashctl endpoint create
// <address> [--out PATH]' and its wrapped continuation lines): never where prose mentions it. Of a command that has
// subcommands, the synopsis lines of the subcommands are not its own. A subcommand is a lowercase word after
// 'mashctl <path> ' in a synopsis line, or the first word of a line under 'commands:'.
func confParseHelp(path, help string) confHelp {
	h := confHelp{opts: map[string][]string{}, subs: map[string]bool{}}
	lines := strings.Split(help, "\n")
	inSyn, skip, inCommands := false, false, false
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			inSyn, inCommands = false, false
			continue
		}
		if t == "commands:" {
			inCommands, inSyn = true, false
			continue
		}
		if inCommands {
			if line[0] != ' ' {
				inCommands = false
			} else if w := strings.Fields(t)[0]; confWordRE.MatchString(w) {
				h.subs[w] = true
				continue
			}
		}
		syn := strings.HasPrefix(t, "usage: mashctl") || strings.HasPrefix(t, "mashctl ") || strings.HasPrefix(t, "Example: mashctl")
		if syn {
			inSyn, skip = true, false
			if rest, ok := strings.CutPrefix(t[strings.Index(t, "mashctl"):], "mashctl "+path+" "); ok {
				if w := strings.Fields(rest); len(w) > 0 && confWordRE.MatchString(w[0]) {
					h.subs[w[0]] = true
					skip = true // the subcommand's own synopsis, not this command's
				}
			}
			if !skip && !strings.HasPrefix(t, "Example:") {
				confScanOptions(h, t)
			}
			continue
		}
		switch {
		case inSyn && strings.HasPrefix(t, "["): // the wrapped rest of a synopsis line
			if !skip {
				confScanOptions(h, t)
			}
		case t[0] == '-':
			inSyn = false
			head, _, _ := strings.Cut(t, "  ")
			confScanOptions(h, head)
		default:
			inSyn = false
		}
	}
	return h
}

// confArgListed reports whether some listing of the option names the argument.
func confArgListed(listings []string, arg string) bool {
	for _, l := range listings {
		if strings.Contains(strings.ToLower(l), strings.ToLower(arg)) {
			return true
		}
	}
	return false
}

// confBuildVerbs are the first words after "mashctl" of the lines of mashctl --help.
func confBuildVerbs(help string) []string {
	seen := map[string]bool{}
	var verbs []string
	for _, line := range strings.Split(help, "\n") {
		rest, ok := strings.CutPrefix(line, "  mashctl ")
		if !ok {
			continue
		}
		if v := strings.Fields(rest)[0]; !seen[v] {
			seen[v] = true
			verbs = append(verbs, v)
		}
	}
	return verbs
}

func TestConformance(t *testing.T) {
	root := loadConfSpec(t)
	var tracked []specCommand
	confEffective := confEffectiveOptions(root)
	specTop := map[string]confCmd{}
	removedVerbs := map[string]bool{}
	confWalk(root, func(path string, c confCmd) {
		if !strings.Contains(path, " ") {
			specTop[c.Name] = c
		}
		if confTrack(c) {
			tracked = append(tracked, specCommand{path, c})
		}
	})
	if len(tracked) == 0 {
		t.Fatal("no command of release 0.5.6 in spec/mashctl.json")
	}
	help := confRun(t, "--help")
	if help.code != 0 {
		t.Fatalf("mashctl --help: exit %d: %s", help.code, help.errs)
	}

	t.Run("commands", func(t *testing.T) {
		for _, sc := range tracked {
			sc := sc
			t.Run(sc.path, func(t *testing.T) {
				r := confRun(t, append(strings.Fields(sc.path), "--help")...)
				var p []string
				if r.code != 0 {
					p = append(p, fmt.Sprintf("exit: want 0, got %d (stderr %q)", r.code, r.errs))
				}
				if !strings.Contains(r.out, "mashctl "+sc.path) {
					p = append(p, fmt.Sprintf("usage: want it to name %q", "mashctl "+sc.path))
				}
				listed := confParseHelp(sc.path, r.out).opts
				for _, o := range sc.c.Options {
					args, found := listed[o.Name]
					switch {
					case !found:
						p = append(p, fmt.Sprintf("option %s: want a help line that starts with it, or a synopsis that has it; not found", o.Name))
					default:
						for _, a := range o.Arguments {
							if !confArgListed(args, a.Name) {
								p = append(p, fmt.Sprintf("option %s: want its argument named %q, listed as %q", o.Name, a.Name, args))
							}
						}
					}
				}
				for _, a := range sc.c.Arguments {
					if !strings.Contains(strings.ToLower(r.out), strings.ToLower(a.Name)) {
						p = append(p, fmt.Sprintf("argument %s: want it named, not found", a.Name))
					}
				}
				confFail(t, "commands "+sc.path, p)
			})
		}
	})

	// both ways: what a --help lists must be in the specification for that command (its own options, the recursive ones
	// of the commands above it and the global ones), and its subcommands must be the specification's
	t.Run("extras", func(t *testing.T) {
		for _, sc := range tracked {
			sc := sc
			r := confRun(t, append(strings.Fields(sc.path), "--help")...)
			if r.code != 0 {
				continue // the commands check says so
			}
			h := confParseHelp(sc.path, r.out)
			known := map[string]bool{}
			for _, o := range confEffective[sc.path] {
				known[o.Name] = true
				for _, al := range o.Aliases {
					known[al] = true
				}
			}
			var opts []string
			for o := range h.opts {
				opts = append(opts, o)
			}
			sort.Strings(opts)
			for _, o := range opts {
				o := o
				t.Run(sc.path+" "+o, func(t *testing.T) {
					var p []string
					if !known[o] {
						p = append(p, fmt.Sprintf("mashctl %s --help lists option %s, which spec/mashctl.json does not have for the command", sc.path, o))
					}
					confFail(t, "extra-option "+sc.path+" "+o, p)
				})
			}
			specSubs := map[string]bool{}
			for _, c := range sc.c.Commands {
				specSubs[c.Name] = true
				c := c
				t.Run(sc.path+" "+c.Name+" listed", func(t *testing.T) {
					var p []string
					if !h.subs[c.Name] {
						p = append(p, fmt.Sprintf("mashctl %s --help: want subcommand %s listed, not found", sc.path, c.Name))
					}
					confFail(t, "missing-command "+sc.path+" "+c.Name, p)
				})
			}
			var subs []string
			for c := range h.subs {
				subs = append(subs, c)
			}
			sort.Strings(subs)
			for _, c := range subs {
				c := c
				t.Run(sc.path+" "+c, func(t *testing.T) {
					var p []string
					if !specSubs[c] {
						p = append(p, fmt.Sprintf("mashctl %s --help lists subcommand %s, which spec/mashctl.json does not have for the command", sc.path, c))
					}
					confFail(t, "extra-command "+sc.path+" "+c, p)
				})
			}
		}
	})

	t.Run("outputs", func(t *testing.T) { confOutputs(t, tracked) })

	t.Run("toplevel", func(t *testing.T) {
		listed := map[string]bool{}
		for _, v := range confBuildVerbs(help.out) {
			listed[v] = true
		}
		for _, sc := range tracked {
			if strings.Contains(sc.path, " ") {
				continue
			}
			sc := sc
			t.Run(sc.path, func(t *testing.T) {
				var p []string
				if !listed[sc.path] {
					p = append(p, fmt.Sprintf("listed: want %q in mashctl --help, not listed", sc.path))
				}
				confFail(t, "toplevel "+sc.path, p)
			})
		}
		t.Run("unlisted", func(t *testing.T) {
			var verbs []string
			for v := range listed {
				verbs = append(verbs, v)
			}
			sort.Strings(verbs)
			for _, v := range verbs {
				v := v
				t.Run(v, func(t *testing.T) {
					var p []string
					if c, ok := specTop[v]; !ok {
						p = append(p, fmt.Sprintf("mashctl --help lists %q, which spec/mashctl.json does not have", v))
					} else if !confTrack(c) {
						p = append(p, fmt.Sprintf("mashctl --help lists %q, which the specification gives to release %s", v, c.meta("x-release")))
					}
					confFail(t, "toplevel-unlisted "+v, p)
				})
			}
		})
	})

	// the verbs and options the specification removed (its x-today notes say which): nothing runs, exit 2, the
	// unknown-command or unknown-option line with the hint to the tool's or the command's help
	t.Run("removed", func(t *testing.T) {
		for _, v := range []string{"adopt", "provision", "deprovision", "audit", "issued", "label", "unlabel", "set-meta", "suspended", "list", "inbox", "suspend", "resume", "permissions"} {
			removedVerbs[v] = true
			v := v
			t.Run(v, func(t *testing.T) {
				r := confRun(t, v)
				var p []string
				want := fmt.Sprintf("error: unknown command %q\nhint: mashctl --help\n", v)
				if r.code != 2 || r.out != "" || r.errs != want {
					p = append(p, fmt.Sprintf("want exit 2, no stdout, stderr %q; got exit %d, stdout %q, stderr %q", want, r.code, r.out, r.errs))
				}
				confFail(t, "removed "+v, p)
			})
		}
		for _, o := range []struct {
			args []string
			hint string
		}{
			{[]string{"init", "--local"}, "init"},
			{[]string{"endpoint", "create", "a.b.c", "--make-key"}, "endpoint create"},
			{[]string{"limits", "--show"}, "limits"},
			{[]string{"endpoint", "inbox", "a.b.c", "--from", "x"}, "endpoint inbox"},
			{[]string{"endpoint", "inbox", "a.b.c", "--to", "x"}, "endpoint inbox"},
			{[]string{"status", "--verbose"}, "status"},
			{[]string{"status", "-v"}, "status"},
			{[]string{"endpoint", "reissue", "a.b.c", "--verbose"}, "endpoint reissue"},
			{[]string{"endpoint", "delete", "a.b.c", "-v"}, "endpoint delete"},
		} {
			o := o
			name := strings.Join(o.args, " ")
			t.Run(name, func(t *testing.T) {
				r := confRun(t, o.args...)
				opt := o.args[len(o.args)-2]
				if !strings.HasPrefix(opt, "--") {
					opt = o.args[len(o.args)-1]
				}
				want := fmt.Sprintf("error: unknown option %s\nhint: mashctl %s --help\n", opt, o.hint)
				var p []string
				if r.code != 2 || r.out != "" || r.errs != want {
					p = append(p, fmt.Sprintf("want exit 2, no stdout, stderr %q; got exit %d, stdout %q, stderr %q", want, r.code, r.out, r.errs))
				}
				confFail(t, "removed "+name, p)
			})
		}
	})

	t.Run("width", func(t *testing.T) {
		check := func(name string, args ...string) {
			t.Run(name, func(t *testing.T) {
				r := confRun(t, args...)
				var p []string
				for i, line := range strings.Split(r.out+r.errs, "\n") {
					if n := len([]rune(line)); n > 80 {
						p = append(p, fmt.Sprintf("line %d is %d columns, want at most 80: %q", i+1, n, line))
					}
				}
				confFail(t, "width "+name, p)
			})
		}
		check("mashctl", "--help")
		for _, sc := range tracked {
			check(sc.path, append(strings.Fields(sc.path), "--help")...)
		}
	})

	t.Run("bad-option", func(t *testing.T) {
		for _, sc := range tracked {
			sc := sc
			pos := confPositional(sc.c)
			base := append(strings.Fields(sc.path), pos...)
			t.Run(sc.path, func(t *testing.T) {
				r := confRun(t, append(append([]string{}, base...), "--bogus")...)
				var p []string
				lines := strings.Split(strings.TrimSuffix(r.errs, "\n"), "\n")
				if r.code != 2 {
					p = append(p, fmt.Sprintf("exit: want 2, got %d", r.code))
				}
				if r.out != "" {
					p = append(p, fmt.Sprintf("stdout: want nothing, got %q", r.out))
				}
				if len(lines) != 2 || lines[0] != "error: unknown option --bogus" || lines[1] != "hint: mashctl "+sc.path+" --help" {
					p = append(p, fmt.Sprintf("stderr: want %q, got %q", "error: unknown option --bogus\nhint: mashctl "+sc.path+" --help\n", r.errs))
				}
				confFail(t, "bad-option "+sc.path, p)
			})
			t.Run(sc.path+" as json", func(t *testing.T) {
				r := confRun(t, append(append([]string{}, base...), "--bogus", "--json")...)
				var p []string
				var obj map[string]any
				if err := json.Unmarshal([]byte(r.out), &obj); err != nil {
					p = append(p, fmt.Sprintf("stdout: want one JSON object, got %q", r.out))
				} else if len(obj) != 2 || obj["error"] != "unknown option --bogus" || obj["hint"] != "mashctl "+sc.path+" --help" {
					p = append(p, fmt.Sprintf("stdout: want {error:%q, hint:%q}, got %q", "unknown option --bogus", "mashctl "+sc.path+" --help", r.out))
				}
				if r.errs != "" {
					p = append(p, fmt.Sprintf("stderr: want nothing with --json, got %q", r.errs))
				}
				if r.code != 2 {
					p = append(p, fmt.Sprintf("exit: want 2, got %d", r.code))
				}
				confFail(t, "bad-option-json "+sc.path, p)
			})
		}
	})

	// the exit codes of the specification are 0, 1 and 2; a command line that is wrong gives 2 and nothing else
	t.Run("usage-exit", func(t *testing.T) {
		for _, sc := range tracked {
			sc := sc
			t.Run(sc.path, func(t *testing.T) {
				var p []string
				for _, e := range sc.c.ExitCodes {
					if e.Code < 0 || e.Code > 2 {
						p = append(p, fmt.Sprintf("the specification lists exit code %d, want 0, 1 or 2 only", e.Code))
					}
				}
				required := false
				for _, a := range sc.c.Arguments {
					required = required || a.Required
				}
				if len(sc.c.Commands) == 0 && (required || sc.path == "init") {
					r := confRun(t, strings.Fields(sc.path)...)
					if r.code != 2 || r.out != "" {
						p = append(p, fmt.Sprintf("with a required argument missing: want exit 2 and no stdout, got exit %d, stdout %q, stderr %q", r.code, r.out, r.errs))
					}
				}
				confFail(t, "usage-exit "+sc.path, p)
			})
		}
	})
}
