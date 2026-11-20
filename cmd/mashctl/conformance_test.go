package main

// The conformance test of mashctl. It proves the build matches the single source, spec/mashctl.json (the commands) and
// spec/rules.json (the rules), by walking the specification and asking the build. It is the twin of the test of the same
// name in cmd/mash. Only help and usage-error paths run: nothing here needs a server.
//
// Every check is a subtest named by what it checks. A check yields a list of problems, each saying what the specification
// expects and what the build gives. What a check may fail on is decided in one place per kind:
//
//   - knownGaps: things the specification has and the build does not have yet. Each has a one-line reason. The test fails
//     if a listed gap is closed (the entry must go, so the list only shrinks) and fails for any difference not listed.
//   - knownDrift: a difference in something that is built, held here only so the suite is green while a fix is in flight.
//     The same rules apply; the entry says what closes it.
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

// confGaps: key is "<check> <name>" as the subtest names it. The value is why it is not built yet.
var confGaps = map[string]string{
	"commands doctor":        "doctor does not exist: it takes over audit and issued check (spec x-today); the pull request that adds the command closes these",
	"toplevel doctor":        "doctor does not exist: it takes over audit and issued check (spec x-today); the pull request that adds the command closes these",
	"bad-option doctor":      "doctor does not exist (see commands doctor)",
	"bad-option-json doctor": "doctor does not exist (see commands doctor)",
}

// confDrift: key is "<check> <name>"; the value says what differs and what closes it.
var confDrift = map[string]string{
	"commands init":            "init --help names the option's argument FILE; spec/mashctl.json names it path (internal/ctl Usage or the spec; one of them changes)",
	"toplevel-unlisted audit":  "audit is still a verb and in mashctl --help; the specification removed it (doctor takes it over); closes with doctor",
	"toplevel-unlisted issued": "issued is still a verb and in mashctl --help; the specification removed it (doctor takes it over, endpoint show has the record); closes with doctor",
	"removed audit":            "audit still runs (read-only listing, exit 1 with no owner); the specification removed it; closes with doctor",
	"removed issued":           "issued still runs and prints its own usage; the specification removed it; closes with doctor",
	"width mashctl":            confWide + ": mashctl --help puts the verb's description on the same line (internal/ctl Usage), up to 402 columns",
	"width init":               confWide,
	"width status":             confWide,
	"width limits":             confWide,
	"width limits apply":       confWide,
	"width endpoint create":    confWide + ": usage line, --out, --public-key and the notes run past 80",
	"width endpoint delete":    confWide + ": the note under the options runs past 80",
	"width endpoint reissue":   confWide + ": usage line, --out and the notes run past 80",
	"bad-option endpoint":      confParentOption,
	"bad-option-json endpoint": confParentOption,
}

const confWide = "a line of the --help output is longer than 80 columns (rules.json: eighty columns); the help is hand-written text, not wrapped"

// confParentOption: 'mashctl endpoint --bogus' reads the option as a sub-command name (cmd/mashctl/endpoint_cmd.go); the rule is 'unknown option'.
const confParentOption = "a parent command takes an unknown option as an unknown sub-command: 'error: unknown command \"--bogus\"' (cmd/mashctl/endpoint_cmd.go), rules.json says 'error: unknown option --bogus'"

// confCheck applies the rule of the two tables to the problems of one check.
func confCheck(t *testing.T, key string, problems []string) {
	t.Helper()
	table, tableName := confGaps, "confGaps"
	if _, ok := confDrift[key]; ok {
		table, tableName = confDrift, "confDrift"
	}
	reason, listed := table[key]
	switch {
	case listed && len(problems) == 0:
		t.Errorf("%s: listed in %s (%s) but it now conforms; remove it from the table", key, tableName, reason)
	case listed:
		t.Logf("%s: %s: %s", tableName, key, reason)
	default:
		for _, p := range problems {
			t.Errorf("%s: %s", key, p)
		}
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

func confOptionRE(name string) *regexp.Regexp {
	return regexp.MustCompile(`(^|[\s\[,])` + regexp.QuoteMeta(name) + `($|[\s\],=])`)
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
	type specCommand struct {
		path string
		c    confCmd
	}
	var tracked []specCommand
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
				lines := strings.Split(r.out, "\n")
				if !strings.Contains(r.out, "mashctl "+sc.path) {
					p = append(p, fmt.Sprintf("usage: want it to name %q", "mashctl "+sc.path))
				}
				for _, o := range sc.c.Options {
					re := confOptionRE(o.Name)
					var line string
					for _, l := range lines {
						if re.MatchString(l) && (line == "" || !strings.HasPrefix(strings.TrimSpace(line), o.Name)) {
							line = l
						}
					}
					switch {
					case line == "":
						p = append(p, fmt.Sprintf("option %s: want it listed, not found", o.Name))
					default:
						for _, a := range o.Arguments {
							after := line[strings.Index(line, o.Name)+len(o.Name):]
							if !strings.Contains(strings.ToLower(after), strings.ToLower(a.Name)) {
								p = append(p, fmt.Sprintf("option %s: want its argument named %q, line is %q", o.Name, a.Name, strings.TrimSpace(line)))
							}
						}
					}
				}
				for _, a := range sc.c.Arguments {
					if !strings.Contains(strings.ToLower(r.out), strings.ToLower(a.Name)) {
						p = append(p, fmt.Sprintf("argument %s: want it named, not found", a.Name))
					}
				}
				confCheck(t, "commands "+sc.path, p)
			})
		}
	})

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
				confCheck(t, "toplevel "+sc.path, p)
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
					confCheck(t, "toplevel-unlisted "+v, p)
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
				confCheck(t, "removed "+v, p)
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
				confCheck(t, "removed "+name, p)
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
				confCheck(t, "width "+name, p)
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
				confCheck(t, "bad-option "+sc.path, p)
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
				confCheck(t, "bad-option-json "+sc.path, p)
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
				confCheck(t, "usage-exit "+sc.path, p)
			})
		}
	})
}
