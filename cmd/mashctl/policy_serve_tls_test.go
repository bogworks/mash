package main

import (
	"bytes"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/policyapply"
)

// Connection names the service and its probes give their connections.
const (
	probeConnName = "mash-policyapply"
	serveConnName = "mashctl-policy-serve"
	sysConnName   = "mashctl-sys"
	// plainControlName names the test's own plain connection.
	plainControlName = "tls-test-plain-control"
)

// newTLSServeEnv is a serve environment whose server offers TLS with a certificate of a throwaway CA (127.0.0.1). The
// server also accepts plain connections (AllowNonTLS), which gives the test a plain control connection; every
// connection `policy serve` makes carries nats.Secure and the CA, so it is TLS or it fails. The environment is
// switched to the tls:// URL and tls_ca (MASH_TLS_CA), with no insecure_loopback, after the setup commands ran. It
// returns the CA's path and the tls:// URL.
func newTLSServeEnv(t *testing.T) (e *serveEnv, caPath, url string) {
	t.Helper()
	caPath, certPath, keyPath := writeTestPKI(t, t.TempDir())
	e = newServeEnvFull(t, true, nil, func(o *server.Options) {
		cfg, err := server.GenTLSConfig(&server.TLSConfigOpts{CertFile: certPath, KeyFile: keyPath})
		if err != nil {
			t.Fatal(err)
		}
		o.TLSConfig, o.TLS, o.TLSTimeout, o.AllowNonTLS = cfg, true, 5, true
	}, func(a *applyEnv) {
		// The setup commands and the helpers' direct connections reach the server over its tls:// URL.
		a.tlsCA = caPath
		a.env["MASH_TLS_CA"] = caPath
	})
	url = fmt.Sprintf("tls://127.0.0.1:%d", e.srv.Addr().(*net.TCPAddr).Port)
	e.env["MASH_URL"] = url
	e.env["MASH_TLS_CA"] = caPath
	delete(e.env, "MASH_INSECURE_LOOPBACK")
	return e, caPath, url
}

// connsNamed returns the TLS version of every connection the server has seen (open and closed) with the name.
func connsNamed(t *testing.T, srv *server.Server, name string) (versions []string) {
	t.Helper()
	cz, err := srv.Connz(&server.ConnzOptions{State: server.ConnAll, Limit: 10000})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cz.Conns {
		if c.Name == name {
			versions = append(versions, c.TLSVersion)
		}
	}
	return versions
}

// requireNoServiceConn fails when the server saw any connection of the service or its probes: a client that is
// refused before it sends CONNECT has no name.
func requireNoServiceConn(t *testing.T, srv *server.Server) {
	t.Helper()
	for _, name := range []string{sysConnName, serveConnName, probeConnName} {
		if n := len(connsNamed(t, srv, name)); n != 0 {
			t.Errorf("the server saw %d connections named %q, want none", n, name)
		}
	}
}

// requireAllTLS fails unless the server saw at least min connections with the name and every one used TLS.
func requireAllTLS(t *testing.T, srv *server.Server, name string, min int) int {
	t.Helper()
	vs := connsNamed(t, srv, name)
	if len(vs) < min {
		t.Fatalf("the server saw %d connections named %q, want at least %d", len(vs), name, min)
	}
	for i, v := range vs {
		if v == "" {
			t.Errorf("connection %d named %q did not use TLS", i, name)
		}
	}
	return len(vs)
}

// A service against a TLS server with a private CA applies a template change and a pure allow-add, and every
// connection it and its probes and witnesses make is TLS.
func TestPolicyServeTLSAppliesGenerations(t *testing.T) {
	withProvisioner(t, &recordingProv{})
	e, _, _ := newTLSServeEnv(t)
	e.start()

	// A plain connection of the test's own is the control: it shows the check tells TLS from plain.
	ctl, err := nats.Connect(fmt.Sprintf("nats://127.0.0.1:%d", e.srv.Addr().(*net.TCPAddr).Port),
		natsauth.UserCredentials(e.env["MASH_ADMIN_CREDS"]), nats.Name(plainControlName), nats.Timeout(serveWait))
	if err != nil {
		t.Fatal(err)
	}
	defer ctl.Close()
	if plain := connsNamed(t, e.srv, plainControlName); len(plain) != 1 || plain[0] != "" {
		t.Fatalf("control: the plain connection's TLS version %q", plain)
	}

	// Generation 1: the scoped key and its template reach the server; the gate's probes connect over TLS.
	e.put(signed(t, e.sub, e.serveDoc(1, 0, nil)))
	st := e.waitStatus("generation 1 applied", func(s *policyapply.Status) bool { return s.Generation == 1 && s.State == policyapply.StateApplied })
	if !st.InForce || len(st.Failing) != 0 || len(st.Refusals) != 0 {
		t.Fatalf("status %+v", st)
	}
	if e.inForce() != 1 {
		t.Fatalf("generation in force %d", e.inForce())
	}
	first := e.shape()
	afterFirst := requireAllTLS(t, e.srv, probeConnName, 0)

	// Generation 2: the status verb closes (a template change, so witnesses open and must close) and core may mail
	// sdk (an added allow).
	e.put(signed(t, e.sub, e.serveDoc(2, 1, func(m map[string]any) {
		m["groups"].(map[string]any)["c"].(map[string]any)["status"] = "closed"
		m["senders"].(map[string]any)["foo.c.core"] = map[string]any{"allow": []string{"foo.c.lead", "foo.c.sdk"}, "deny": []string{}}
	})))
	st = e.waitStatus("generation 2 applied", func(s *policyapply.Status) bool { return s.Generation == 2 && s.State == policyapply.StateApplied })
	if !st.InForce || len(st.Failing) != 0 || len(st.Refusals) != 0 {
		t.Fatalf("status %+v", st)
	}
	if e.inForce() != 2 || e.shape() == first {
		t.Fatalf("generation in force %d, signing keys changed %v", e.inForce(), e.shape() != first)
	}
	if n := requireAllTLS(t, e.srv, probeConnName, afterFirst+1); n <= afterFirst {
		t.Errorf("generation 2 made no probe or witness connection (%d before, %d after)", afterFirst, n)
	}

	// The service's own connections (the system and the admin ones) were TLS too.
	requireAllTLS(t, e.srv, sysConnName, 1)
	requireAllTLS(t, e.srv, serveConnName, 0)
	e.noSeeds(e.logs.String())
	if code := e.shutdown(); code != 0 {
		t.Fatalf("exit %d: %s", code, e.logs.String())
	}
	if strings.Contains(e.logs.String(), "could not revoke") {
		t.Errorf("the probe credentials were not revoked: %s", e.logs.String())
	}
}

// runServeRefused runs `policy serve` with the environment and returns its exit code and stderr. It must exit by
// itself: the service never reaches the point where it is watching.
func runServeRefused(t *testing.T, e *serveEnv) (int, string) {
	t.Helper()
	oldReady := policyServeReady
	policyServeReady = func() { t.Error("the service started watching") }
	t.Cleanup(func() { policyServeReady = oldReady })
	var out, errb bytes.Buffer
	code := runEnv([]string{"policy", "serve", "--range", serveRange, "--servers", "1"}, func(k string) string { return e.env[k] }, &out, &errb)
	return code, errb.String()
}

// A tls_ca that does not sign the server's certificate: the service exits non-zero naming the CA setting, and the
// server sees no connection from it.
func TestPolicyServeTLSWrongCARefused(t *testing.T) {
	e, _, _ := newTLSServeEnv(t)
	otherCA, _, _ := writeTestPKI(t, t.TempDir())
	e.env["MASH_TLS_CA"] = otherCA
	code, errs := runServeRefused(t, e)
	if code != 1 || !strings.Contains(errs, "not trusted") || !strings.Contains(errs, "tls_ca") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	requireNoServiceConn(t, e.srv)
	if st, found := e.status(); found && st.Status != nil {
		t.Errorf("a status was written: %+v", st.Status)
	}
}

// TLS is required for the admin and system connections: a plain URL to a remote host, and insecure_loopback with a
// remote host, are refused before any connection; a server with no TLS is refused by nats.Secure (the loopback
// address alone is not enough without insecure_loopback).
func TestPolicyServeTLSRequired(t *testing.T) {
	cases := []struct {
		name string
		set  func(e *serveEnv)
		want []string
	}{
		{"plain nats to a remote host", func(e *serveEnv) { e.env["MASH_URL"] = "nats://example.invalid:4222" },
			[]string{"refusing a plain nats://", "tls_ca"}},
		{"insecure_loopback with a remote host", func(e *serveEnv) {
			e.env["MASH_URL"] = "nats://example.invalid:4222"
			e.env["MASH_INSECURE_LOOPBACK"] = "1"
		}, []string{"insecure_loopback", "not one", "tls://"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, _, _ := newTLSServeEnv(t)
			c.set(e)
			code, errs := runServeRefused(t, e)
			if code != 1 {
				t.Fatalf("exit %d: %s", code, errs)
			}
			for _, w := range c.want {
				if !strings.Contains(errs, w) {
					t.Errorf("stderr lacks %q: %s", w, errs)
				}
			}
			requireNoServiceConn(t, e.srv)
		})
	}

	t.Run("a server with no TLS", func(t *testing.T) {
		e := newServeEnvNoService(t, true) // a plain server
		delete(e.env, "MASH_INSECURE_LOOPBACK")
		code, errs := runServeRefused(t, e)
		if code != 1 || !strings.Contains(errs, "connect") {
			t.Fatalf("exit %d: %s", code, errs)
		}
		requireNoServiceConn(t, e.srv)
	})
}
