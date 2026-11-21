package main

// C1: the conformance test. It proves the build matches the single source, spec/mash.json (the commands),
// spec/mash-mcp.json (the MCP tools) and spec/rules.json (the rules), by walking the specification and asking the build.
//
// Every check is a subtest named by what it checks. A check yields a list of problems, each saying the check, what the
// specification expects and what the build gives. What a check may fail on is decided in one place per kind:
//
//   - knownGaps: things the specification has and the build does not have yet (or has in a form that is not yet the
//     generated one). Each has a one-line reason. The test fails if a listed gap is closed (the entry must go, so the
//     list only shrinks) and fails for any difference that is not listed.
//   - knownDrift: a difference in something that is built, held here only so the suite is green while a fix is in
//     flight in another pull request. The same rules apply: a closed one fails until removed. A difference of a built
//     command's options or descriptions is never a known gap; a drift entry names the pull request that closes it.
//
// What it checks:
//
//	help/<command>      mash <command> --help: the generated layout, word for word, in 80 columns
//	toplevel/<command>  the command is listed under its x-group heading in mash --help
//	toplevel/layout     mash --help itself: description, each command's one-line description, Global options
//	toplevel/unlisted   no command in the build's help is missing from the specification (but a short allowlist)
//	tools/<tool>        the MCP tool: description, parameter names, types, required, enums, descriptions
//	tools/unexpected    no tool in the build is missing from the specification
//	tools/degraded      with no endpoint chosen the server offers only version, status and doctor
//	outputs/<command>   a spec-comparison test exists for each command of the table (a meta check)
//	rules/*             the machine-checkable rules of spec/rules.json, for every command of the table

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// knownGaps: key is "<check> <name>" as the subtest names it. The value is why it is not built yet.
var knownGaps = map[string]string{
	"help serve":      "serve is hand-written: its --help prints the top-level line, not the generated layout",
	"help hook":       "hook is hand-written: its --help prints the top-level line, not the generated layout",
	"help prompt":     "prompt is hand-written: one-line usage, not the generated layout",
	"tools reply":     "reply is the old hand-written tool: description and parameter texts differ from the specification",
	"toplevel layout": "the tool's own help is still hand-written: other description, own wording for each command, no Global options heading",
}

// knownDrift: key is "<rule> <command>"; the value says what differs and what closes it.
var knownDrift = map[string]string{
	"help-spelling -h version":     confHelpFirst,
	"help-spelling --help version": confHelpFirst,
}

// confHelpFirst: rules.json says options go before or after the command, and help is also a command; the build takes a
// help option before the command as the tool's own help (cmd/mash/main.go, cli.go).
const confHelpFirst = "a help option before the command prints the tool's own help; rules.json: 'mash -h version' is 'mash version --help'"

// confCheck applies the rule of the two tables to the problems of one check.
func confCheck(t *testing.T, table map[string]string, tableName, key string, problems []string) {
	t.Helper()
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

// confCheckAny looks the key up in knownGaps, then in knownDrift.
func confCheckAny(t *testing.T, key string, problems []string) {
	t.Helper()
	if _, ok := knownDrift[key]; ok {
		confCheck(t, knownDrift, "knownDrift", key, problems)
		return
	}
	confCheck(t, knownGaps, "knownGaps", key, problems)
}

func TestConformance(t *testing.T) {
	spec := loadConfSpec(t)
	env := confEnv(t)

	type specCommand struct {
		path string
		c    confCmd
	}
	var tracked []specCommand
	specTop := map[string]confCmd{}
	confWalk(spec.Root, func(path string, c confCmd) {
		if !strings.Contains(path, " ") {
			specTop[c.Name] = c
		}
		if confTrack(c) {
			tracked = append(tracked, specCommand{path, c})
		}
	})
	if len(tracked) == 0 {
		t.Fatal("no command of release 0.5.6 in spec/mash.json")
	}

	help := runMash(env, "--help")
	if help.code != 0 {
		t.Fatalf("mash --help: exit %d: %s", help.code, help.errs)
	}
	order, groups := confTopLevel(help.out)

	t.Run("help", func(t *testing.T) {
		for _, sc := range tracked {
			sc := sc
			t.Run(sc.path, func(t *testing.T) {
				confCheckAny(t, "help "+sc.path, confHelpProblems(spec, env, sc.path, sc.c))
			})
		}
	})

	t.Run("toplevel", func(t *testing.T) {
		for _, sc := range tracked {
			if strings.Contains(sc.path, " ") {
				continue
			}
			sc := sc
			t.Run(sc.path, func(t *testing.T) {
				var p []string
				group := sc.c.meta("x-group")
				found := false
				for g, names := range groups {
					for _, n := range names {
						if n != sc.path {
							continue
						}
						found = true
						if g != group {
							p = append(p, fmt.Sprintf("group: want %q, listed under %q", group, g))
						}
					}
				}
				if !found {
					p = append(p, fmt.Sprintf("listed: want %q under %q in mash --help, not listed", sc.path, group))
				}
				confCheckAny(t, "toplevel "+sc.path, p)
			})
		}
		t.Run("layout", func(t *testing.T) {
			var p []string
			var want []string
			for _, sc := range tracked {
				if g := sc.c.meta("x-group"); !strings.Contains(sc.path, " ") && !confContains(want, g) {
					want = append(want, g)
				}
			}
			if strings.Join(order, ",") != strings.Join(want, ",") {
				p = append(p, fmt.Sprintf("headings: want %v in the specification's order, got %v", want, order))
			}
			first := strings.SplitN(help.out, "\n", 2)[0]
			if first != spec.Root.Description {
				p = append(p, fmt.Sprintf("description: want %q first, got %q", spec.Root.Description, first))
			}
			flat := confFlat(help.out)
			for _, sc := range tracked {
				if !strings.Contains(sc.path, " ") && !strings.Contains(flat, confFlat(sc.c.Description)) {
					p = append(p, fmt.Sprintf("%s: want its description %q beside it, not found", sc.path, sc.c.Description))
				}
			}
			if !strings.Contains(help.out, "\nGlobal options:\n") {
				p = append(p, "Global options: want the heading, not found")
			}
			for i, line := range strings.Split(help.out, "\n") {
				if n := len([]rune(line)); n > 80 {
					p = append(p, fmt.Sprintf("width: line %d is %d columns, want at most 80", i+1, n))
				}
			}
			confCheck(t, knownGaps, "knownGaps", "toplevel layout", p)
		})
		t.Run("unlisted", func(t *testing.T) {
			listed := map[string]bool{}
			for _, names := range groups {
				for _, n := range names {
					listed[n] = true
				}
			}
			var names []string
			for n := range listed {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				if _, inSpec := specTop[n]; inSpec {
					continue
				}
				t.Errorf("mash --help lists %q, which spec/mash.json does not have", n)
			}
		})
	})

	t.Run("tools", func(t *testing.T) {
		w := newWorld(t, "a")
		s := startServe(t, w.env["a"])
		s.initialize(t)
		got := map[string]map[string]any{}
		for _, tl := range s.tools(t) {
			m := tl.(map[string]any)
			got[m["name"].(string)] = m
		}
		inSpec := map[string]bool{}
		for _, want := range spec.Tools {
			if want.Release != "0.5.6" {
				continue
			}
			want := want
			inSpec[want.Name] = true
			t.Run(want.Name, func(t *testing.T) {
				var p []string
				if g, ok := got[want.Name]; !ok {
					p = append(p, fmt.Sprintf("tool: want it offered; offered %v", confKeysOf(got)))
				} else {
					p = confToolProblems(want, g)
				}
				confCheckAny(t, "tools "+want.Name, p)
			})
		}
		t.Run("unexpected", func(t *testing.T) {
			for _, n := range confKeysOf(got) {
				if !inSpec[n] {
					t.Errorf("tools/list offers %q, which spec/mash-mcp.json does not have", n)
				}
			}
		})
		t.Run("degraded", func(t *testing.T) {
			s := startServe(t, map[string]string{"HOME": t.TempDir()})
			s.initialize(t)
			names := toolNames(t, s)
			if len(names) != 3 || !names["version"] || !names["status"] || !names["doctor"] {
				t.Errorf("with no endpoint chosen: want tools [doctor status version], got %v", names)
			}
			for _, tl := range s.tools(t) {
				m := tl.(map[string]any)
				for _, want := range spec.Tools {
					if want.Name == m["name"] {
						for _, p := range confToolProblems(want, m) {
							t.Errorf("degraded %s: %s", want.Name, p)
						}
					}
				}
			}
		})
	})

	// The comparison of each command's output cases with the specification's x-output is the work of the tests of the
	// command (TestMatchesSpec, TestStatusMatchesSpec, TestDoctorMatchesSpec, TestWhoMatchesSpec); the conformance test
	// does not repeat them. It checks that one exists for every command on the table.
	t.Run("outputs", func(t *testing.T) {
		matches := confSpecTests(t)
		for _, e := range commandEntries {
			name := e.CLIName()
			t.Run(name, func(t *testing.T) {
				for _, m := range matches {
					if strings.Contains(strings.ToLower(m.name), name) || strings.Contains(m.body, `"`+name+`"`) {
						return
					}
				}
				t.Errorf("no Test*MatchesSpec in internal/cmdcore/cmds or cmd/mash names %q: its x-output cases are not compared with the specification", name)
			})
		}
	})

	t.Run("rules", func(t *testing.T) { confRules(t, spec) })
}

func confContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func confKeysOf(m map[string]map[string]any) []string {
	var k []string
	for n := range m {
		k = append(k, n)
	}
	sort.Strings(k)
	return k
}

type confTestFunc struct{ name, body string }

var confFuncRE = regexp.MustCompile(`(?m)^func (Test\w*MatchesSpec)\(`)

// confSpecTests are the Test*MatchesSpec functions of the tests of the commands.
func confSpecTests(t *testing.T) []confTestFunc {
	t.Helper()
	var out []confTestFunc
	for _, dir := range []string{".", filepath.Join("..", "..", "internal", "cmdcore", "cmds")} {
		files, _ := filepath.Glob(filepath.Join(dir, "*_test.go"))
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			src := string(b)
			for _, loc := range confFuncRE.FindAllStringSubmatchIndex(src, -1) {
				body := src[loc[0]:]
				if i := strings.Index(body[1:], "\nfunc "); i >= 0 {
					body = body[:i+1]
				}
				out = append(out, confTestFunc{src[loc[2]:loc[3]], body})
			}
		}
	}
	return out
}

// noEndpoint is what rules.json says a shell with no endpoint installed gets, from any command that needs one.
const confNoEndpoint = "error: no endpoint is installed here\nhint: mash join <file>\n"

func confRules(t *testing.T, spec confSpec) {
	rules := map[string]bool{}
	for _, r := range spec.Rules {
		rules[r.Rule] = true
	}
	for _, need := range []string{
		"A wrong command line gets one error line in a fixed form, and one hint",
		"--json when a command fails",
		"Options go anywhere after the tool's name",
		"Having no endpoint reads the same in every mash command",
		"Eighty columns, and color only on a terminal",
	} {
		if !rules[need] {
			t.Fatalf("spec/rules.json no longer has the rule %q: this test checks it, update it", need)
		}
	}
	// the arguments a command needs to get past its own usage checks
	positional := map[string][]string{"send": {"a.b.c", "hi"}, "who": nil, "version": nil, "status": nil, "doctor": nil, "inbox": nil, "read": {"1"}, "join": {"x.join"}}
	// setup commands make the configuration or a key: they need no endpoint, so the rule on having none does not apply
	setup := map[string]bool{"join": true, "key create": true}
	for _, e := range commandEntries {
		name := e.CLIName()
		words := strings.Fields(name) // "key create" is two words on the command line
		pos := positional[name]
		t.Run(name, func(t *testing.T) {
			env := confEnv(t)
			if name == "key create" { // a seed path that cannot be made (its parent is a file), so a run writes nothing
				f := filepath.Join(t.TempDir(), "file")
				if err := os.WriteFile(f, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				pos = []string{"--out", filepath.Join(f, "x.seed")}
			}

			t.Run("width", func(t *testing.T) { // (a) 80 columns
				r := runMash(env, append(append([]string{}, words...), "--help")...)
				var p []string
				for i, line := range strings.Split(r.out, "\n") {
					if n := len([]rune(line)); n > 80 {
						p = append(p, fmt.Sprintf("line %d is %d columns, want at most 80: %q", i+1, n, line))
					}
				}
				confCheckAny(t, "width "+name, p)
			})

			t.Run("bad option", func(t *testing.T) { // (b) and (d): exit 2, an error line, the command's own help as the hint
				r := runMash(env, append(append([]string{}, words...), append(pos, "--x")...)...)
				var p []string
				lines := strings.Split(strings.TrimSuffix(r.errs, "\n"), "\n")
				if r.code != 2 {
					p = append(p, fmt.Sprintf("exit: want 2, got %d", r.code))
				}
				if r.out != "" {
					p = append(p, fmt.Sprintf("stdout: want nothing (nothing is attempted), got %q", r.out))
				}
				if len(lines) != 2 {
					p = append(p, fmt.Sprintf("stderr: want one error line and one hint line, got %q", r.errs))
				} else {
					if want := "error: unknown option --x"; lines[0] != want {
						p = append(p, fmt.Sprintf("error line: want %q, got %q", want, lines[0]))
					}
					if want := "hint: mash " + name + " --help"; lines[1] != want {
						p = append(p, fmt.Sprintf("hint line: want %q, got %q", want, lines[1]))
					}
				}
				confCheckAny(t, "bad-option "+name, p)
			})

			t.Run("bad option as json", func(t *testing.T) { // (c): {error, hint} on stdout, nothing on stderr, the same exit code
				r := runMash(env, append(append([]string{}, words...), append(pos, "--x", "--json")...)...)
				var p []string
				var obj map[string]any
				if err := json.Unmarshal([]byte(r.out), &obj); err != nil {
					p = append(p, fmt.Sprintf("stdout: want one JSON object, got %q", r.out))
				} else if len(obj) != 2 || obj["error"] == nil || obj["hint"] != "mash "+name+" --help" || obj["error"] != "unknown option --x" {
					p = append(p, fmt.Sprintf("stdout: want {error:%q, hint:%q}, got %q", "unknown option --x", "mash "+name+" --help", r.out))
				}
				if r.errs != "" {
					p = append(p, fmt.Sprintf("stderr: want nothing with --json, got %q", r.errs))
				}
				if r.code != 2 {
					p = append(p, fmt.Sprintf("exit: want 2, got %d", r.code))
				}
				confCheckAny(t, "bad-option-json "+name, p)
			})

			if name != "version" && !setup[name] { // (d) and the rule on having no endpoint: the same three-wording sentence everywhere
				t.Run("no endpoint", func(t *testing.T) {
					r := runMash(env, append(append([]string{}, words...), pos...)...)
					var p []string
					if r.code != 2 || r.out != "" || r.errs != confNoEndpoint {
						p = append(p, fmt.Sprintf("with no endpoint installed: want exit 2, no stdout, stderr %q; got exit %d, stdout %q, stderr %q", confNoEndpoint, r.code, r.out, r.errs))
					}
					confCheckAny(t, "no-endpoint "+name, p)
				})
				t.Run("no endpoint as json", func(t *testing.T) {
					r := runMash(env, append(append([]string{}, words...), append(pos, "--json")...)...)
					want := `{"error":"no endpoint is installed here","hint":"mash join <file>"}` + "\n"
					var p []string
					if r.code != 2 || r.out != want || r.errs != "" {
						p = append(p, fmt.Sprintf("with no endpoint installed and --json: want exit 2, stdout %q, no stderr; got exit %d, stdout %q, stderr %q", want, r.code, r.out, r.errs))
					}
					confCheckAny(t, "no-endpoint-json "+name, p)
				})
			}

			t.Run("global options anywhere", func(t *testing.T) { // (e)
				d := t.TempDir()
				globals := []string{"--no-color", "--config-dir", d, "--json"}
				after := runMash(env, append(append(append([]string{}, words...), pos...), globals...)...)
				before := runMash(env, append(append(append([]string{}, globals...), words...), pos...)...)
				var p []string
				if after != before {
					p = append(p, fmt.Sprintf("%v after the command and before it differ: after %+v, before %+v", globals, after, before))
				}
				confCheckAny(t, "globals "+name, p)
			})
		})
	}

	// all nine global options, taken before and after the command, on the command that needs no server
	t.Run("global options, all", func(t *testing.T) {
		env := confEnv(t)
		d := t.TempDir()
		globals := []string{"--endpoint", "a.b.c", "--creds", filepath.Join(d, "c"), "--server", "nats://127.0.0.1:1", "--ca", filepath.Join(d, "ca"),
			"--config-dir", d, "--detail", "--json", "--no-color"}
		after := runMash(env, append([]string{"version"}, globals...)...)
		before := runMash(env, append(append([]string{}, globals...), "version")...)
		if after.code != 0 || after != before {
			t.Errorf("mash version with every global option: want exit 0 and the same output before and after the command, got after %+v, before %+v", after, before)
		}
		short := runMash(env, "version", "--detail")
		if short.code != 0 {
			t.Errorf("mash version -v: want exit 0, got %+v", short)
		}
		for _, h := range [][]string{{"version", "-h"}, {"-h", "version"}, {"--help", "version"}, {"help", "version"}} {
			h := h
			t.Run("help "+strings.Join(h, " "), func(t *testing.T) {
				r := runMash(env, h...)
				want := runMash(env, "version", "--help")
				var p []string
				if r != want {
					p = append(p, fmt.Sprintf("mash %v: want exactly what mash version --help prints (%d lines), got exit %d and %d lines starting %q",
						h, strings.Count(want.out, "\n"), r.code, strings.Count(r.out, "\n"), strings.SplitN(r.out, "\n", 2)[0]))
				}
				confCheckAny(t, "help-spelling "+strings.Join(h, " "), p)
			})
		}
	})
}
