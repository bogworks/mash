package cliframe

import (
	"reflect"
	"testing"

	"github.com/bogworks/mash/internal/config"
)

func TestShadowFlagsBeatEnvironment(t *testing.T) {
	spec := Spec{Tool: "mash", CredsEnv: "MASH_CREDS", CredsKey: "creds"}
	g, rest, err := Extract(spec, []string{"--endpoint", "a.b.c", "who", "--server=tls://x:1", "--server", "tls://y:2", "--creds", "/c", "--ca", "/ca", "--config-dir", "/d"})
	if err != nil || !reflect.DeepEqual(rest, []string{"who"}) {
		t.Fatalf("%v %v", rest, err)
	}
	base := map[string]string{"MASH_ENDPOINT": "e.e.e", "MASH_SERVER": "tls://env:1", "MASH_CREDS": "/envc", "MASH_CA": "/envca", "MASH_CONFIG_DIR": "/envd", "HOME": "/h"}
	env, lookup := g.Shadow(spec, func(k string) string { return base[k] }, func(k string) (string, bool) { v, ok := base[k]; return v, ok })
	for k, want := range map[string]string{"MASH_ENDPOINT": "a.b.c", "MASH_SERVER": "tls://x:1,tls://y:2", "MASH_CREDS": "/c", "MASH_CA": "/ca", "MASH_CONFIG_DIR": "/d", "HOME": "/h"} {
		if env(k) != want {
			t.Errorf("env(%s) = %q, want %q", k, env(k), want)
		}
		if v, ok := lookup(k); !ok || v != want {
			t.Errorf("lookup(%s) = %q, %v, want %q", k, v, ok, want)
		}
	}
	if v, ok := lookup(config.FlagSourcePrefix + "MASH_SERVER"); !ok || v != "--server" {
		t.Errorf("flag source of MASH_SERVER = %q, %v", v, ok)
	}
	if _, ok := lookup(config.FlagSourcePrefix + "MASH_NAME"); ok {
		t.Error("a variable no flag supplied has a flag source")
	}
}

func TestShadowWithoutFlagsChangesNothing(t *testing.T) {
	spec := Spec{Tool: "mashctl", CredsEnv: "MASH_ADMIN_CREDS", CredsKey: "admin_creds"}
	g, _, _ := Extract(spec, []string{"x", "--json"})
	env, lookup := g.Shadow(spec, func(k string) string { return "e-" + k }, func(k string) (string, bool) { return "l-" + k, true })
	if env("A") != "e-A" {
		t.Error(env("A"))
	}
	if v, _ := lookup("A"); v != "l-A" {
		t.Error(v)
	}
}

func TestShadowCredsEnvIsTheToolsOwn(t *testing.T) {
	spec := Spec{Tool: "mashctl", CredsEnv: "MASH_ADMIN_CREDS", CredsKey: "admin_creds"}
	g, _, _ := Extract(spec, []string{"--creds", "/admin"})
	env, _ := g.Shadow(spec, func(string) string { return "" }, func(string) (string, bool) { return "", false })
	if env("MASH_ADMIN_CREDS") != "/admin" || env("MASH_CREDS") != "" {
		t.Errorf("%q %q", env("MASH_ADMIN_CREDS"), env("MASH_CREDS"))
	}
}

func TestExtractOwningKeepsItsFlagsForItsVerbs(t *testing.T) {
	spec := Spec{Tool: "mash", CredsEnv: "MASH_CREDS", CredsKey: "creds"}
	own := map[string][]string{"who": {"--json"}, "adopt": {"--server"}}
	g, rest, err := ExtractOwning(spec, []string{"--endpoint", "a.b.c", "who", "--note", "--json", "--", "--json"}, own)
	if err != nil || g.JSON || g.Endpoint != "a.b.c" || !reflect.DeepEqual(rest, []string{"who", "--note", "--json", "--", "--json"}) {
		t.Errorf("%+v %v %v", g, rest, err)
	}
	g, rest, err = ExtractOwning(spec, []string{"send", "--json", "x"}, own)
	if err != nil || !g.JSON || !reflect.DeepEqual(rest, []string{"send", "x"}) {
		t.Errorf("%+v %v %v", g, rest, err)
	}
	g, rest, err = ExtractOwning(spec, []string{"adopt", "--server", "u1", "--ca", "/ca", "--server=u2", "--json"}, own)
	if err != nil || len(g.Servers) != 0 || g.CA != "/ca" || !g.JSON || !reflect.DeepEqual(rest, []string{"adopt", "--server", "u1", "--server=u2"}) {
		t.Errorf("%+v %v %v", g, rest, err)
	}
	if _, _, err = ExtractOwning(spec, []string{"who", "--creds"}, own); err == nil {
		t.Error("a global flag without its value is still an error")
	}
}

func TestDetailFlagIsGlobalAnywhere(t *testing.T) {
	spec := Spec{Tool: "mash", CredsEnv: "MASH_CREDS", CredsKey: "creds"}
	own := map[string][]string{"who": {"--json"}}
	for _, args := range [][]string{{DetailFlag, "who", "a"}, {"who", DetailFlag, "a"}, {"who", "a", DetailFlag}} {
		g, rest, err := ExtractOwning(spec, args, own)
		if err != nil || !g.Detail || !reflect.DeepEqual(rest, []string{"who", "a"}) {
			t.Errorf("%q: %+v %q %v", args, g, rest, err)
		}
	}
}
