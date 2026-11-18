package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
)

// forgePayloads are the two answers a second owner's endpoint publishes into the reply subject of another owner's
// discovery request: a forged service-discovery answer that claims an offline endpoint of owner is up, and a second
// answer shaped like a replayed registration (carrying identity fields of an earlier registration). Both name owner.
func forgePayloads(owner, group, instance string) (forged, replay []byte) {
	subject := "agents.prompt." + owner + "." + group + "." + instance
	mk := func(meta map[string]string) []byte {
		b, _ := json.Marshal(map[string]any{
			"name": "agents", "type": "io.nats.micro.v1.info_response", "metadata": meta,
			"endpoints": []map[string]string{{"name": "prompt", "subject": subject}},
		})
		return b
	}
	base := map[string]string{"owner": owner, "group": group, "instance": instance, "agent": "claude-code"}
	forged = mk(base)
	rm := map[string]string{"user_nkey": "UREPLAYED", "account": "AREPLAYED", "id_sig": "replayed"}
	for k, v := range base {
		rm[k] = v
	}
	replay = mk(rm)
	return forged, replay
}

// forgeResult is the one line `nodeinit forge` prints: it carries counts only.
type forgeResult struct {
	Seen       int `json:"seen_requests"`
	Violations int `json:"violations"`
}

// isForgeViolation reports whether an error is the server refusing a publish into one of alice's reply subjects.
func isForgeViolation(err error) bool {
	if err == nil {
		return false
	}
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "permissions violation for publish") && strings.Contains(m, "_inbox.alice.")
}

// cmdForge is `nodeinit forge --url URL --ca FILE --creds FILE --seconds N`: connect with the given credentials over
// TLS, subscribe to $SRV.>, and for each $SRV.INFO request whose reply is in _INBOX.alice. publish both forged
// payloads into it. It counts the server's permissions violations and prints {"seen_requests":N,"violations":M}.
// It prints "forge: subscribed" on errw once the subscription is in place. No credential is ever printed.
func cmdForge(a []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("forge", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	url := fs.String("url", "", "")
	ca := fs.String("ca", "", "")
	creds := fs.String("creds", "", "")
	secs := fs.Int("seconds", 0, "")
	if err := fs.Parse(a); err != nil || fs.NArg() != 0 || *url == "" || *ca == "" || *creds == "" || *secs <= 0 {
		fmt.Fprintln(errw, "usage: nodeinit forge --url tls://ADDR:PORT --ca FILE --creds FILE --seconds N")
		return 2
	}
	var violations atomic.Int64
	nc, err := nats.Connect(*url, nats.UserCredentials(*creds), nats.RootCAs(*ca), nats.CustomInboxPrefix("_INBOX.bob.forge"),
		nats.MaxReconnects(0), nats.Timeout(5*time.Second),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, e error) {
			if isForgeViolation(e) {
				violations.Add(1)
			}
		}))
	if err != nil {
		fmt.Fprintln(errw, "nodeinit: forge: connect failed:", oneLine(err))
		return 1
	}
	defer nc.Close()
	spy, err := nc.SubscribeSync("$SRV.>")
	if err == nil {
		err = nc.Flush()
	}
	if err != nil {
		fmt.Fprintln(errw, "nodeinit: forge: subscribe failed:", oneLine(err))
		return 1
	}
	forged, replay := forgePayloads("alice", "dev", "ghost")
	fmt.Fprintln(errw, "forge: subscribed")
	seen := 0
	end := time.Now().Add(time.Duration(*secs) * time.Second)
	var settle time.Time
	for time.Now().Before(end) {
		if !settle.IsZero() && time.Now().After(settle) {
			break
		}
		m, err := spy.NextMsg(50 * time.Millisecond)
		if err == nil && strings.HasPrefix(m.Subject, "$SRV.INFO") && strings.HasPrefix(m.Reply, "_INBOX.alice.") {
			seen++
			_ = nc.Publish(m.Reply, forged)
			_ = nc.Publish(m.Reply, replay)
			_ = nc.Flush()
		}
		if settle.IsZero() && violations.Load() >= 2 {
			settle = time.Now().Add(500 * time.Millisecond)
		}
	}
	_ = nc.Flush()
	b, _ := json.Marshal(forgeResult{Seen: seen, Violations: int(violations.Load())})
	fmt.Fprintln(out, string(b))
	return 0
}

func oneLine(err error) string { return strings.ReplaceAll(err.Error(), "\n", " ") }
