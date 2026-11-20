package policyserve

import (
	"strings"
	"testing"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/policyapply"
)

func TestResolveURLs(t *testing.T) {
	wild := func(n string) ServerInfo { return ServerInfo{Name: n, Host: "", Port: 4222} }
	conc := func(n, h string) ServerInfo { return ServerInfo{Name: n, Host: h, Port: 4223} }
	tests := map[string]struct {
		servers  []ServerInfo
		cfg      string
		declared int
		want     []string
		refuse   string
	}{
		"concrete host, scheme of the configured URL": {[]ServerInfo{conc("a", "10.0.0.5")}, "tls://10.0.0.5:4222", 1, []string{"tls://10.0.0.5:4223"}, ""},
		"wildcard, one URL, one server":               {[]ServerInfo{wild("a")}, "tls://nats.example:4222", 1, []string{"tls://nats.example:4222"}, ""},
		"wildcard, list in order":                     {[]ServerInfo{wild("a"), wild("b")}, "tls://h1:4222, tls://h2:4222", 2, []string{"tls://h1:4222", "tls://h2:4222"}, ""},
		"wildcard, list of the wrong length":          {[]ServerInfo{wild("a"), wild("b")}, "tls://h1:4222", 2, nil, "list one URL per server"},
		"wildcard, fewer answered than declared":      {[]ServerInfo{wild("a")}, "tls://h1:4222,tls://h2:4222", 2, nil, "list one URL per server"},
		"wildcard, no loopback invented":              {[]ServerInfo{wild("a")}, "", 1, nil, "list one URL per server"},
	}
	for name, tc := range tests {
		got, err := ResolveURLs(tc.servers, tc.cfg, tc.declared)
		if tc.refuse != "" {
			if err == nil || !strings.Contains(err.Error(), tc.refuse) {
				t.Errorf("%s: err = %v", name, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		for i, w := range tc.want {
			if got[i].URL != w {
				t.Errorf("%s: server %d url %q, want %q", name, i, got[i].URL, w)
			}
		}
	}
}

func TestParseVarzHostWildcard(t *testing.T) {
	for _, h := range []string{"", "0.0.0.0", "::", "[::]"} {
		raw := `{"server":{"name":"a"},"data":{"host":"` + h + `","port":4222,"max_payload":1,"max_control_line":1}}`
		info, ok, err := parseVarz([]byte(raw))
		if err != nil || !ok || info.Host != "" {
			t.Errorf("host %q: %+v %v %v", h, info, ok, err)
		}
	}
}

func TestReadServersNeedsADeclaredCount(t *testing.T) {
	if _, err := ReadServers(t.Context(), nil, 0); err == nil || !strings.Contains(err.Error(), "init --config") {
		t.Errorf("err = %v", err)
	}
}

// The applier hands its connection options to the probes and the witnesses.
func TestApplierTransportsCarryTheOptions(t *testing.T) {
	opt := func(*nats.Options) error { return nil }
	a := &Applier{Options: []nats.Option{opt, opt}}
	p, w := a.transports([]ServerInfo{{Name: "a", URL: "tls://h:1"}}, &policyapply.ProbeMinter{})
	if len(p.Options) != 2 || len(w.Options) != 2 || p.URLs["a"] != "tls://h:1" {
		t.Errorf("prober %d, witnesses %d options", len(p.Options), len(w.Options))
	}
}
