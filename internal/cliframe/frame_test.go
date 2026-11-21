package cliframe

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

var mashSpec = Spec{Tool: "mash", CredsEnv: "MASH_CREDS", CredsKey: "creds"}

var ctlSpec = Spec{
	Tool: "mashctl", CredsEnv: "MASH_ADMIN_CREDS", CredsKey: "admin_creds",
	Extra: []Setting{{Flag: "--system-creds", Env: "MASH_SYSTEM_CREDS", Key: "system_creds"}},
}

func TestExtractValueFlags(t *testing.T) {
	get := map[string]func(Global) string{
		"--endpoint":   func(g Global) string { return g.Endpoint },
		"--creds":      func(g Global) string { return g.Creds },
		"--ca":         func(g Global) string { return g.CA },
		"--config-dir": func(g Global) string { return g.ConfigDir },
	}
	for flag, f := range get {
		for _, tc := range []struct {
			name string
			args []string
			rest []string
		}{
			{"space before verb", []string{flag, "V", "send", "x"}, []string{"send", "x"}},
			{"equals before verb", []string{flag + "=V", "send", "x"}, []string{"send", "x"}},
			{"space after verb", []string{"send", flag, "V", "x"}, []string{"send", "x"}},
			{"equals at end", []string{"send", "x", flag + "=V"}, []string{"send", "x"}},
			{"space at end", []string{"send", "x", flag, "V"}, []string{"send", "x"}},
			{"bare dash value", []string{flag, "-"}, []string{}},
		} {
			t.Run(flag+"/"+tc.name, func(t *testing.T) {
				g, rest, err := Extract(mashSpec, tc.args)
				if err != nil {
					t.Fatal(err)
				}
				want := "V"
				if tc.name == "bare dash value" {
					want = "-"
				}
				if f(g) != want {
					t.Errorf("value = %q, want %q", f(g), want)
				}
				if !g.Set[flag] {
					t.Errorf("Set[%s] not true", flag)
				}
				if !reflect.DeepEqual(rest, tc.rest) {
					t.Errorf("rest = %q, want %q", rest, tc.rest)
				}
			})
		}
	}
}

func TestExtractBooleans(t *testing.T) {
	for _, tc := range []struct {
		flag string
		set  string
		get  func(Global) bool
	}{
		{DetailFlag, DetailFlag, func(g Global) bool { return g.Detail }},
		{"--json", "--json", func(g Global) bool { return g.JSON }},
		{"--no-color", "--no-color", func(g Global) bool { return g.NoColor }},
	} {
		for pos, args := range map[string][]string{
			"before": {tc.flag, "who", "alice"},
			"middle": {"who", tc.flag, "alice"},
			"after":  {"who", "alice", tc.flag},
		} {
			t.Run(tc.flag+"/"+pos, func(t *testing.T) {
				g, rest, err := Extract(mashSpec, args)
				if err != nil {
					t.Fatal(err)
				}
				if !tc.get(g) || !g.Set[tc.set] {
					t.Errorf("flag not recorded: %+v", g)
				}
				if !reflect.DeepEqual(rest, []string{"who", "alice"}) {
					t.Errorf("rest = %q", rest)
				}
			})
		}
	}
}

func TestExtractNoFlags(t *testing.T) {
	g, rest, err := Extract(mashSpec, []string{"send", "-x", "--other=1", "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Set) != 0 || g.Endpoint != "" || g.Detail {
		t.Errorf("unexpected globals: %+v", g)
	}
	if !reflect.DeepEqual(rest, []string{"send", "-x", "--other=1", "hi"}) {
		t.Errorf("rest = %q", rest)
	}
	_, rest, _ = Extract(mashSpec, nil)
	if len(rest) != 0 {
		t.Errorf("rest = %q", rest)
	}
}

func TestExtractLastWins(t *testing.T) {
	g, _, err := Extract(mashSpec, []string{"--endpoint", "a", "x", "--endpoint=b"})
	if err != nil {
		t.Fatal(err)
	}
	if g.Endpoint != "b" {
		t.Errorf("Endpoint = %q, want b", g.Endpoint)
	}
}

func TestExtractServers(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"one", []string{"--server", "nats://a"}, []string{"nats://a"}},
		{"repeat", []string{"--server", "a", "x", "--server=b"}, []string{"a", "b"}},
		{"comma", []string{"--server=a, b ,,c"}, []string{"a", "b", "c"}},
		{"comma and repeat", []string{"--server", "a,b", "--server", "c"}, []string{"a", "b", "c"}},
		{"only commas", []string{"--server", ","}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, _, err := Extract(mashSpec, tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(g.Servers, tc.want) {
				t.Errorf("Servers = %q, want %q", g.Servers, tc.want)
			}
			if !g.Set["--server"] {
				t.Error("Set[--server] not true")
			}
		})
	}
}

func TestExtractDoubleDash(t *testing.T) {
	g, rest, err := Extract(mashSpec, []string{"--json", "send", "--", "--endpoint", "x", "-v", "--creds=y"})
	if err != nil {
		t.Fatal(err)
	}
	if !g.JSON || g.Endpoint != "" || g.Detail || g.Creds != "" {
		t.Errorf("globals = %+v", g)
	}
	want := []string{"send", "--", "--endpoint", "x", "-v", "--creds=y"}
	if !reflect.DeepEqual(rest, want) {
		t.Errorf("rest = %q, want %q", rest, want)
	}
	// A value after a flag may not be "--" (it looks like a flag).
	if _, _, err := Extract(mashSpec, []string{"--endpoint", "--"}); err == nil {
		t.Error("want error for --endpoint --")
	}
}

func TestExtractValueErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
		not  string
	}{
		{"missing at end", []string{"send", "--endpoint"}, "--endpoint needs a value", ""},
		{"empty inline", []string{"--ca="}, "--ca needs a value", ""},
		{"empty next", []string{"--creds", ""}, "--creds needs a value", ""},
		{"next is flag", []string{"--endpoint", "--json"},
			`--endpoint needs a value; "--json" looks like a flag (write --endpoint=--json to use it as the value)`, ""},
		{"next is flag with equals", []string{"--creds", "--token=sekrit"},
			`--creds needs a value; "--token" looks like a flag (write --creds=VALUE to use it as the value)`, "sekrit"},
		{"next is short flag", []string{"--server", "-v"}, `"-v" looks like a flag`, ""},
		{"long flag text truncated", []string{"--ca", "--" + strings.Repeat("x", 60)}, `looks like a flag (write --ca=VALUE`, strings.Repeat("x", 40)},
		{"extra flag", []string{"--system-creds"}, "--system-creds needs a value", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Extract(ctlSpec, tc.args)
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("err = %v, want *Error", err)
			}
			if e.Code != 2 {
				t.Errorf("Code = %d, want 2", e.Code)
			}
			if !strings.Contains(e.Msg, tc.want) {
				t.Errorf("Msg = %q, want it to contain %q", e.Msg, tc.want)
			}
			if tc.not != "" && strings.Contains(e.Msg, tc.not) {
				t.Errorf("Msg = %q must not contain %q", e.Msg, tc.not)
			}
		})
	}
}

func TestExtractExtras(t *testing.T) {
	g, rest, err := Extract(ctlSpec, []string{"--system-creds", "/s", "issue", "--system-creds=/t", "bob"})
	if err != nil {
		t.Fatal(err)
	}
	if g.Extra["--system-creds"] != "/t" || !g.Set["--system-creds"] {
		t.Errorf("extra = %v set = %v", g.Extra, g.Set)
	}
	if !reflect.DeepEqual(rest, []string{"issue", "bob"}) {
		t.Errorf("rest = %q", rest)
	}
	// The mash spec does not know the extra flag: it stays in the rest.
	_, rest, err = Extract(mashSpec, []string{"--system-creds", "/s"})
	if err != nil || !reflect.DeepEqual(rest, []string{"--system-creds", "/s"}) {
		t.Errorf("rest = %q err = %v", rest, err)
	}
}

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolvePrecedence(t *testing.T) {
	type setting struct {
		flag, env, key string
		spec           Spec
		args           func(v string) []string
		get            func(Resolved) string
	}
	settings := []setting{
		{"--endpoint", "MASH_ENDPOINT", "endpoint", mashSpec,
			func(v string) []string { return []string{"--endpoint", v} }, func(r Resolved) string { return r.Endpoint }},
		{"--creds", "MASH_CREDS", "creds", mashSpec,
			func(v string) []string { return []string{"--creds", v} }, func(r Resolved) string { return r.Creds }},
		{"--creds", "MASH_ADMIN_CREDS", "admin_creds", ctlSpec,
			func(v string) []string { return []string{"--creds", v} }, func(r Resolved) string { return r.Creds }},
		{"--ca", "MASH_CA", "ca", mashSpec,
			func(v string) []string { return []string{"--ca", v} }, func(r Resolved) string { return r.CA }},
		{"--system-creds", "MASH_SYSTEM_CREDS", "system_creds", ctlSpec,
			func(v string) []string { return []string{"--system-creds", v} }, func(r Resolved) string { return r.Extra["--system-creds"] }},
	}
	for _, s := range settings {
		for _, tc := range []struct {
			name         string
			flag, env, f string // values; empty means not given
			want, source string
		}{
			{"all three", "F", "E", "C", "F", "flag"},
			{"flag and file", "F", "", "C", "F", "flag"},
			{"flag and env", "F", "E", "", "F", "flag"},
			{"env and file", "", "E", "C", "E", s.env},
			{"env only", "", "E", "", "E", s.env},
			{"file only", "", "", "C", "C", "config.json"},
			{"empty env falls to file", "", "", "C", "C", "config.json"},
			{"none", "", "", "", "", "unset"},
		} {
			t.Run(s.spec.Tool+"/"+s.flag+"/"+tc.name, func(t *testing.T) {
				var args []string
				if tc.flag != "" {
					args = s.args(tc.flag)
				}
				g, _, err := Extract(s.spec, args)
				if err != nil {
					t.Fatal(err)
				}
				env := map[string]string{}
				if tc.env != "" {
					env[s.env] = tc.env
				} else {
					env[s.env] = "" // an empty value counts as unset
				}
				var file map[string]any
				if tc.f != "" {
					file = map[string]any{s.key: tc.f}
				}
				r := g.Resolve(s.spec, envOf(env), file)
				if got := s.get(r); got != tc.want {
					t.Errorf("value = %q, want %q", got, tc.want)
				}
				if r.Source[s.flag] != tc.source {
					t.Errorf("Source = %q, want %q", r.Source[s.flag], tc.source)
				}
			})
		}
	}
}

func TestResolveConfigDir(t *testing.T) {
	g, _, _ := Extract(mashSpec, []string{"--config-dir", "/f"})
	r := g.Resolve(mashSpec, envOf(map[string]string{"MASH_CONFIG_DIR": "/e"}), nil)
	if r.ConfigDir != "/f" || r.Source["--config-dir"] != "flag" {
		t.Errorf("flag: %q %q", r.ConfigDir, r.Source["--config-dir"])
	}
	g, _, _ = Extract(mashSpec, nil)
	r = g.Resolve(mashSpec, envOf(map[string]string{"MASH_CONFIG_DIR": "/e"}), map[string]any{"config_dir": "/c"})
	if r.ConfigDir != "/e" || r.Source["--config-dir"] != "MASH_CONFIG_DIR" {
		t.Errorf("env: %q %q", r.ConfigDir, r.Source["--config-dir"])
	}
	r = g.Resolve(mashSpec, envOf(nil), nil)
	if r.ConfigDir != "" || r.Source["--config-dir"] != "unset" {
		t.Errorf("unset: %q %q", r.ConfigDir, r.Source["--config-dir"])
	}
}

func TestResolveServers(t *testing.T) {
	flagG, _, _ := Extract(mashSpec, []string{"--server", "f1,f2"})
	noneG, _, _ := Extract(mashSpec, nil)
	for _, tc := range []struct {
		name   string
		g      Global
		env    string
		file   any
		want   []string
		source string
	}{
		{"flag wins", flagG, "e1", []any{"c1"}, []string{"f1", "f2"}, "flag"},
		{"env wins over file", noneG, "e1, e2", []any{"c1"}, []string{"e1", "e2"}, "MASH_SERVER"},
		{"file array", noneG, "", []any{"c1", "c2"}, []string{"c1", "c2"}, "config.json"},
		{"file string array type", noneG, "", []string{"c1"}, []string{"c1"}, "config.json"},
		{"file single string", noneG, "", "c1", []string{"c1"}, "config.json"},
		{"file comma string", noneG, "", "c1,c2", []string{"c1", "c2"}, "config.json"},
		{"file array skips non-strings", noneG, "", []any{"c1", 7, ""}, []string{"c1"}, "config.json"},
		{"file wrong type", noneG, "", 5, nil, "unset"},
		{"empty env empty file", noneG, "", []any{}, nil, "unset"},
		{"nothing", noneG, "", nil, nil, "unset"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := map[string]any{}
			if tc.file != nil {
				file["servers"] = tc.file
			}
			r := tc.g.Resolve(mashSpec, envOf(map[string]string{"MASH_SERVER": tc.env}), file)
			if !reflect.DeepEqual(r.Servers, tc.want) {
				t.Errorf("Servers = %q, want %q", r.Servers, tc.want)
			}
			if r.Source["--server"] != tc.source {
				t.Errorf("Source = %q, want %q", r.Source["--server"], tc.source)
			}
		})
	}
}

func TestResolveNilEnvAndFile(t *testing.T) {
	r := Global{}.Resolve(mashSpec, nil, nil)
	if r.Endpoint != "" || r.NoColor || r.Source["--endpoint"] != "unset" {
		t.Errorf("r = %+v", r)
	}
}

func TestResolveNoColor(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		env  string
		want bool
	}{
		{"neither", nil, "", false},
		{"flag", []string{"--no-color"}, "", true},
		{"NO_COLOR set", nil, "1", true},
		{"both", []string{"--no-color"}, "1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, _, _ := Extract(mashSpec, tc.args)
			r := g.Resolve(mashSpec, envOf(map[string]string{"NO_COLOR": tc.env}), nil)
			if r.NoColor != tc.want {
				t.Errorf("NoColor = %v, want %v", r.NoColor, tc.want)
			}
		})
	}
}

func TestResolveBooleansPassThrough(t *testing.T) {
	g, _, _ := Extract(mashSpec, []string{DetailFlag, "--json"})
	r := g.Resolve(mashSpec, nil, nil)
	if !r.Detail || !r.JSON {
		t.Errorf("r = %+v", r)
	}
}

// The old names of the option are not options any more: they stay in the arguments for the command to refuse.
func TestOldVerboseIsNotAGlobalOption(t *testing.T) {
	for _, old := range []string{"--verbose", "-v"} {
		g, rest, err := Extract(mashSpec, []string{"who", old})
		if err != nil || g.Detail || g.Set[DetailFlag] || !reflect.DeepEqual(rest, []string{"who", old}) {
			t.Errorf("%s: %+v %q %v", old, g, rest, err)
		}
	}
	if DetailParam != "detail" || DetailFlag != "--"+DetailParam {
		t.Errorf("DetailFlag %q, DetailParam %q", DetailFlag, DetailParam)
	}
}
