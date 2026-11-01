package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

// maxCLIRoundTrips is what a one-shot command may cost over a slow link, the connection's own handshake included. A
// session 150 ms from NATS paid 10 to 22 seconds per call when every command read the whole directory one key at a time.
const maxCLIRoundTrips = 8

// rttWorld is a directory of n endpoints (a few provisioned, the rest directory-only records, which cost no stream),
// 25 messages in me's inbox, and a live lease for me carrying a launch token.
type rttWorld struct {
	*world
	px *testenv.RTTProxy
}

const rttToken = "tok-rtt"

func newRTTWorld(t *testing.T, n int) *rttWorld {
	t.Helper()
	ctx := context.Background()
	names := []string{"me", "sender", "peer1", "peer2", "peer3"}
	w := newWorld(t, names...)
	conn := w.e.Conn(t, w.e.AdminCreds)
	js, err := jetstream.New(conn)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := js.KeyValue(ctx, store.DirectoryBucket)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(names) + 1; i <= n; i++ {
		name := fmt.Sprintf("dir-%d", i)
		_, user, err := w.e.MintEndpoint(name)
		if err != nil {
			t.Fatal(err)
		}
		rec := store.Record{Name: name, Owner: w.e.Owner, Agent: sap.AgentName, AgentToken: sap.AgentToken, Account: w.e.Account,
			User: user, MailSubject: sap.For(w.e.Owner, name).Mail, Created: time.Now().UTC().Format(time.RFC3339)}
		v, _ := json.Marshal(rec)
		if _, err := dir.Create(ctx, name, v); err != nil {
			t.Fatal(err)
		}
	}
	cfgOf := func(name, token string) config.Config {
		env := map[string]string{}
		for k, v := range w.env[name] {
			env[k] = v
		}
		if token != "" {
			env["MASH_LAUNCH_TOKEN"] = token
		}
		cfg, err := config.Load(envFrom(env), env["HOME"])
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	sender, err := endpoint.OpenClient(ctx, cfgOf("sender", ""))
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 25; i++ {
		if _, err := sender.Send(ctx, "me", fmt.Sprintf("message %d from sender", i), 0); err != nil {
			t.Fatal(err)
		}
	}
	sender.Close()
	for _, p := range []string{"peer2", "peer3"} { // online, so presence has answers to wait for
		ep, err := endpoint.Open(ctx, cfgOf(p, ""))
		if err != nil {
			t.Fatal(err)
		}
		if err := ep.StartService(ctx, func(endpoint.PromptEvent) {}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(ep.Close)
	}
	me, err := endpoint.Open(ctx, cfgOf("me", rttToken)) // a session's serve: the lease its shell's send adopts
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(me.Close)
	// 20 ms each way: far above the scheduling jitter of a burst of pipelined requests, so the count is exact, and
	// far below a test's patience.
	return &rttWorld{world: w, px: testenv.NewRTTProxy(t, w.e.URL, 20*time.Millisecond)}
}

// measure runs one CLI command as me through the proxy and returns its output and the round trips it took.
func (r *rttWorld) measure(t *testing.T, token string, args ...string) (string, float64) {
	t.Helper()
	env := map[string]string{}
	for k, v := range r.env["me"] {
		env[k] = v
	}
	env["MASH_URL"] = r.px.URL()
	if token != "" {
		env["MASH_LAUNCH_TOKEN"] = token
	}
	r.px.Reset()
	var out, errb bytes.Buffer
	if code := run(args, envFrom(env), strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("mash %v: exit %d: %s%s", args, code, out.String(), errb.String())
	}
	time.Sleep(60 * time.Millisecond) // let the last frames cross the proxy
	return out.String() + errb.String(), r.px.RTT()
}

// The CLI's cost in round trips is bounded and does not grow with the directory: the same command against 10 and 40
// directory records takes the same number of waves.
func TestCLIRoundTrips(t *testing.T) {
	cmds := []struct {
		name  string
		token string
		args  []string
		check func(t *testing.T, out string)
	}{
		{"inbox-limit-1", "", []string{"inbox", "--limit", "1"}, func(t *testing.T, out string) {
			if !strings.HasPrefix(out, "#25\t") || strings.Count(out, "\n") != 1 {
				t.Fatalf("inbox --limit 1: %q", out)
			}
		}},
		{"inbox-limit-20", "", []string{"inbox", "--limit", "20"}, func(t *testing.T, out string) {
			if !strings.HasPrefix(out, "#6\t") || strings.Count(out, "\n") != 20 || !strings.Contains(out, "verified") {
				t.Fatalf("inbox --limit 20: %q", out)
			}
		}},
		{"read", "", []string{"read", "#7"}, func(t *testing.T, out string) {
			if !strings.Contains(out, "message 7 from sender") || !strings.Contains(out, "verified") {
				t.Fatalf("read: %q", out)
			}
		}},
		{"send", "", []string{"send", "peer1", "hello", "there"}, func(t *testing.T, out string) {
			if !strings.HasPrefix(out, "sent #") || !strings.Contains(out, "from me to peer1") {
				t.Fatalf("send: %q", out)
			}
		}},
		{"send-launch-token", rttToken, []string{"send", "peer1", "hello", "there"}, func(t *testing.T, out string) {
			if !strings.HasPrefix(out, "sent #") || strings.Contains(out, "MASH_LAUNCH_TOKEN") {
				t.Fatalf("send with the launch token: %q", out)
			}
		}},
		{"who", "", []string{"who"}, func(t *testing.T, out string) {
			if strings.Count(out, "\n") < 4 || !strings.Contains(out, "peer2") || !strings.Contains(out, "online") {
				t.Fatalf("who: %q", out)
			}
		}},
		{"who-long", "", []string{"who", "--long"}, func(t *testing.T, out string) {
			if strings.Count(out, "\n") < 4 || !strings.Contains(out, "peer2") {
				t.Fatalf("who --long: %q", out)
			}
		}},
	}
	rtts := map[string]map[int]float64{}
	for _, n := range []int{10, 40} {
		t.Run(fmt.Sprintf("N=%d", n), func(t *testing.T) {
			r := newRTTWorld(t, n)
			for _, c := range cmds {
				out, rtt := r.measure(t, c.token, c.args...)
				c.check(t, out)
				if rtts[c.name] == nil {
					rtts[c.name] = map[int]float64{}
				}
				rtts[c.name][n] = rtt
				t.Logf("%-16s %4.1f RTT\n%s", c.name, rtt, r.px.Describe())
				if rtt > maxCLIRoundTrips {
					t.Errorf("mash %s over %d directory records: %.1f round trips, want at most %d\n%s",
						strings.Join(c.args, " "), n, rtt, maxCLIRoundTrips, r.px.Describe())
				}
			}
		})
	}
	for _, c := range cmds {
		small, big := rtts[c.name][10], rtts[c.name][40]
		if big > small+1 {
			t.Errorf("%s: %.1f round trips at 10 directory records but %.1f at 40: the cost grows with the directory", c.name, small, big)
		}
	}
}

// whoText's default lines are parsed by other tools; the round-trip work must leave them as they were.
func TestCLIWhoDefaultFormat(t *testing.T) {
	r := newRTTWorld(t, 12)
	out, _ := r.measure(t, "", "who")
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 2 || (f[1] != "online" && f[1] != "offline") {
			t.Fatalf("who line %q is not <name>\\t<online|offline>...", line)
		}
	}
}
