package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
)

// Every test runs without a real server-set check: the real one needs system account credentials.
func init() {
	checkServerSet = func(context.Context, config.Config, func(string) string, int) error { return nil }
}

func TestExtractServerFlags(t *testing.T) {
	base := map[string]string{"MASH_SERVERS": "2", "MASH_SYS_CREDS": "/env/sys.creds", "OTHER": "x"}
	envf := func(k string) string { return base[k] }
	cases := []struct {
		name    string
		args    []string
		rest    []string
		servers string
		creds   string
		errHas  string
	}{
		{"none", []string{"a", "--force"}, []string{"a", "--force"}, "2", "/env/sys.creds", ""},
		{"space forms", []string{"--servers", "3", "a", "--sys-creds", "/f/s.creds"}, []string{"a"}, "3", "/f/s.creds", ""},
		{"equals forms", []string{"a", "--servers=4", "--sys-creds=/g"}, []string{"a"}, "4", "/g", ""},
		{"servers only", []string{"a", "--servers", "5"}, []string{"a"}, "5", "/env/sys.creds", ""},
		{"not a number", []string{"a", "--servers", "abc"}, nil, "", "", "must be a whole number of servers, 1 or more"},
		{"zero", []string{"a", "--servers=0"}, nil, "", "", "must be a whole number of servers, 1 or more"},
		{"negative", []string{"a", "--servers=-1"}, nil, "", "", "must be a whole number of servers, 1 or more"},
		{"servers without value", []string{"a", "--servers"}, nil, "", "", "--servers needs a value"},
		{"sys-creds without value", []string{"a", "--sys-creds"}, nil, "", "", "--sys-creds needs a value"},
		{"empty equals", []string{"a", "--sys-creds="}, nil, "", "", "--sys-creds needs a value"},
	}
	for _, c := range cases {
		rest, env2, err := extractServerFlags(c.args, envf)
		if c.errHas != "" {
			if err == nil || !strings.Contains(err.Error(), c.errHas) {
				t.Errorf("%s: err %v, want %q", c.name, err, c.errHas)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !reflect.DeepEqual(rest, c.rest) {
			t.Errorf("%s: rest %v, want %v", c.name, rest, c.rest)
		}
		if env2("MASH_SERVERS") != c.servers || env2("MASH_SYS_CREDS") != c.creds || env2("OTHER") != "x" {
			t.Errorf("%s: env2 servers %q creds %q other %q", c.name, env2("MASH_SERVERS"), env2("MASH_SYS_CREDS"), env2("OTHER"))
		}
	}
}

func TestVerbsAcceptServersAndSysCredsFlags(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	s := stubRevoke(t)
	h.env["MASH_SERVERS"], h.env["MASH_SYS_CREDS"] = "2", "/env/sys.creds"
	out, errb, code := h.run("endpoint", "delete", "o.g.a", "--yes", "--servers", "3", "--sys-creds=/flag/sys.creds")
	if code != 0 {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if len(s.srv) != 1 || s.srv[0] != 3 || s.creds[0] != "/flag/sys.creds" {
		t.Fatalf("flag did not beat env: servers %v creds %v", s.srv, s.creds)
	}
}

func TestBadServersFlagIsRefusedOnEveryVerb(t *testing.T) {
	h := newFleetEnv(t)
	for _, args := range [][]string{
		{"endpoint", "delete", "o.g.a", "--yes", "--servers", "x"},
	} {
		_, errb, code := h.run(args...)
		if code != 2 || !strings.Contains(errb, "must be a whole number of servers, 1 or more") {
			t.Errorf("%v: %d %q", args, code, errb)
		}
	}
}

func TestUsageListsTheFlags(t *testing.T) {
	for _, u := range []string{endpointDeleteUsage} {
		if !strings.Contains(u, "--servers N") || !strings.Contains(u, "--sys-creds FILE") {
			t.Errorf("usage lacks the flags: %q", u)
		}
	}
}

func TestNoSysCredsTextNamesTheFlag(t *testing.T) {
	if !strings.Contains(noSysCredsMsg, "pass --sys-creds FILE or set MASH_SYS_CREDS") {
		t.Errorf("%q", noSysCredsMsg)
	}
}

func TestRetireShortServerSetChangesNothing(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	s := stubRevoke(t)
	old := checkServerSet
	checkServerSet = func(context.Context, config.Config, func(string) string, int) error {
		return errors.New("only 2 of 3 servers answered the account lookup")
	}
	t.Cleanup(func() { checkServerSet = old })
	out, errb, code := h.run("endpoint", "delete", "o.g.a", "--yes")
	if code != 1 || out != "" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if !strings.Contains(errb, "nothing was revoked; the key is still valid") || !strings.Contains(errb, "only 2 of 3") {
		t.Errorf("stderr %q", errb)
	}
	if strings.Contains(errb, "deleted o.g.a") || len(s.calls) != 0 || len(h.records()) != 1 {
		t.Errorf("changed something: %q calls %v records %v", errb, s.calls, h.records())
	}
}

func TestEndpointDeleteShortServerSetChangesNothing(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	s := stubRevoke(t)
	old := checkServerSet
	checkServerSet = func(context.Context, config.Config, func(string) string, int) error {
		return errors.New("only 2 of 3 servers answered the account lookup")
	}
	t.Cleanup(func() { checkServerSet = old })
	_, errb, code := h.run("endpoint", "delete", "o.g.a", "--yes")
	if code != 1 || !strings.Contains(errb, "nothing was revoked; the key is still valid") {
		t.Fatalf("%d %q", code, errb)
	}
	if len(s.calls) != 0 || len(h.records()) != 1 {
		t.Errorf("changed something: calls %v records %v", s.calls, h.records())
	}
}

func TestRetirePartialListsWhatWasRevokedAndWhatIsPending(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("b")
	stubRevoke(t)
	failAfterPurge(t)
	out, errb, code := h.run("endpoint", "delete", "o.g.b", "--yes")
	if code != 1 {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if out != "" || !strings.Contains(errb, "error: deleted o.g.b, but ") || !strings.HasSuffix(errb, "hint: mashctl endpoint delete o.g.b\n") {
		t.Errorf("stderr: %q", errb)
	}
}

func TestRetireFailureBeforeAnyRevokeSaysKeyStillValid(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("b")
	deprovisionIfUser = func(*admin.Admin, context.Context, sap.Address, string) error {
		return errors.New("the server holding bucket KV_lease is down")
	}
	t.Cleanup(func() { deprovisionIfUser = (*admin.Admin).DeprovisionIfUser })
	_, errb, code := h.run("endpoint", "delete", "o.g.b", "--yes")
	if code != 1 || strings.Contains(errb, "deleted o.g.b") || !strings.Contains(errb, "nothing was revoked; the key is still valid") {
		t.Fatalf("%d %q", code, errb)
	}
}
