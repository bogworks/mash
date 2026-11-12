package endpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/bootstrap"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/mint"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/policy"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

const zkOwner = "alice"

// zkPolicyV1 lets zone a mail zone b; zkPolicyV2 changes the scoped signing key of BOTH zones (zone b may now mail a,
// zone a may now mail c), which is what makes the server close every user signed by either key.
const (
	zkPolicyV1 = `default: deny
endpoints:
  rx: {labels: {zone: b}}
  tx: {labels: {zone: a}}
rules:
  - {name: a-to-b, action: allow, from: {zone: a}, to: {zone: b}}
`
	zkPolicyV2 = `default: deny
endpoints:
  rx: {labels: {zone: b}}
  tx: {labels: {zone: a}}
  other: {labels: {zone: c}}
rules:
  - {name: a-to-b, action: allow, from: {zone: a}, to: {zone: b}}
  - {name: b-to-a, action: allow, from: {zone: b}, to: {zone: a}}
  - {name: a-to-c, action: allow, from: {zone: a}, to: {zone: c}}
`
)

func zkCompile(t *testing.T, src string) *policy.Compiled {
	t.Helper()
	f, err := policy.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	c, err := policy.Compile(f, zkOwner)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A zone key's template changes while its endpoint is consuming mail, and its sender is publishing, on the server
// `mashctl init --local` stands up (operator mode, a full resolver, `apply` pushing the scoped keys), with users MINTED
// under the zone keys (internal/mint), not the testenv's plain users. This is the mashctl plan's gate (codex H3 on the
// plan): the spike used a simpler fetch loop and never interrupted a publish awaiting its ack, an ack after delivery or
// a lease refresh.
//
// What the server does: apply replaces the scoped signer of zones a and b; every user signed by a replaced key is
// closed ("Authentication Failure", client sees EOF) within milliseconds and reconnects after the client's own wait
// (endpoint.go: 2 s plus up to 5 s of jitter), and the new template governs the new connection. Users signed by an
// unchanged key are not touched.
//
// What the code guarantees, and this test asserts, for a stream of messages with unique --id keys (SendNonce and
// Nats-Msg-Id derive from the id):
//   - Receiver: every message stored is presented exactly once. A delivery whose ack was lost is redelivered by the
//     consumer after its 30 s AckWait with the same stream sequence, and the seen set drops it (acking it) instead of
//     presenting it again; messages delivered but not yet acked when the connection closed stay unacked and are
//     redelivered the same way. No message is lost: JetStream stores it whether or not the receiver is connected.
//   - Lease: the lease is a KV key refreshed by a compare-and-set against the revision held. A refresh that cannot
//     reach the server is a transport error and is retried on the next tick; the entry only lapses if the outage
//     outlasts store.LeaseTTL (90 s), far longer than a reconnect. So the lease stays held by the same instance with
//     no takeover, and Run does not return (a lost lease would end it). The test refreshes every 500 ms to put
//     refreshes into the outage; it asserts that the key's history holds only this instance's puts.
//   - Sender: a publish awaiting its JetStream ack when the connection closes either completes (nats.go keeps the
//     request, resends it after the reconnect and the ack arrives on the new connection) or ends in a
//     *SendTimeoutError naming the key; SendDetailed with the same ID then stores the message at most once (the
//     stream drops the second copy inside its duplicate window and the ack says Duplicate). The test retries that way
//     and asserts the stream holds exactly one message per id.
//   - Acks after the reconnect: they are buffered by the client and sent on the new connection, or lost and the
//     message is redelivered and dropped as a duplicate (above); either way the consumer ends with nothing pending and
//     nothing unacked, which the test waits for (within AckWait).
//
// In five runs under -race one send timed out across the outage and its retry with the same id was acknowledged as a
// Duplicate, with exactly one stored copy; in the others the publishes spanning the outage completed on their own.
//
// It polls with deadlines, never fixed sleeps, and every duration is under the portable rule's 30 s.
func TestZoneKeyChangeDuringConsumeAndSend(t *testing.T) {
	old := LeaseRefresh
	LeaseRefresh = 500 * time.Millisecond
	t.Cleanup(func() { LeaseRefresh = old })

	root := t.TempDir()
	ks, err := ctlkeys.Open(filepath.Join(root, "keys"))
	if err != nil {
		t.Fatal(err)
	}
	pl, err := bootstrap.Build(bootstrap.Params{Dir: filepath.Join(root, "server"), Owner: zkOwner, Port: 4222}, ks)
	if err != nil {
		t.Fatal(err)
	}
	if err := pl.Write(); err != nil {
		t.Fatal(err)
	}
	opts, err := server.ProcessConfigFile(pl.ConfPath())
	if err != nil {
		t.Fatal(err)
	}
	opts.Port = -1
	opts.NoLog, opts.NoSigs = true, true
	srv, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("server did not start")
	}
	t.Cleanup(func() { srv.Shutdown(); srv.WaitForShutdown() })
	url := srv.ClientURL()

	// apply policy v1 through the system account, as `mashctl policy apply` does.
	sys, err := nats.Connect(url, natsauth.UserCredentials(filepath.Join(pl.Dir, bootstrap.SysCreds)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sys.Close)
	doApply := func(c *policy.Compiled) {
		t.Helper()
		var out bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if code := apply.Run(ctx, apply.Params{Mode: apply.Apply, Compiled: c, Keys: ks, Resolver: apply.NATS{Conn: sys}}, &out); code != 0 {
			t.Fatalf("apply: exit %d\n%s", code, out.String())
		}
	}
	c1 := zkCompile(t, zkPolicyV1)
	doApply(c1)

	// the directory, and two users minted under their zones' keys.
	anc, err := nats.Connect(url, natsauth.UserCredentials(pl.Creds))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(anc.Close)
	adm, err := admin.New(anc, zkOwner)
	if err != nil {
		t.Fatal(err)
	}
	actx, cancelAdm := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancelAdm)
	if err := adm.Init(actx); err != nil {
		t.Fatal(err)
	}
	creds := map[string]string{}
	for _, ep := range c1.Endpoints {
		m, err := mint.Mint(ks, c1, ep.Name, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		creds[ep.Name] = filepath.Join(root, ep.Name+".creds")
		if err := os.WriteFile(creds[ep.Name], m.Creds, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := adm.Provision(actx, ep.Name, m.UserPub, m.AccountPub, "", admin.Meta{Labels: ep.Labels, Zone: ep.Zone}); err != nil {
			t.Fatal(err)
		}
	}
	cfg := func(name string) config.Config {
		return config.Config{URL: url, Owner: zkOwner, Name: name, Creds: creds[name], SettleMS: 50, BatchMsgs: 10, BatchBytes: 16384}
	}

	// the receiver: a real endpoint with its lease, consuming; the sender: a client of the other zone.
	rctx, cancelRx := context.WithCancel(context.Background())
	rx, err := Open(rctx, cfg("rx"))
	if err != nil {
		t.Fatal(err)
	}
	shown := map[uint64]int{} // stream sequence -> times presented
	var shownMu sync.Mutex
	nShown := func() int { shownMu.Lock(); defer shownMu.Unlock(); return len(shown) }
	runDone := make(chan error, 1)
	go func() {
		runDone <- rx.Run(rctx, func(text string, meta map[string]string) error {
			shownMu.Lock()
			defer shownMu.Unlock()
			for _, s := range strings.Split(meta["seqs"], ",") {
				n, err := strconv.ParseUint(s, 10, 64)
				if err != nil {
					return fmt.Errorf("bad seqs %q", meta["seqs"])
				}
				shown[n]++
			}
			return nil
		})
	}()
	t.Cleanup(func() { cancelRx(); <-runDone; rx.Close() })
	tx, err := OpenClient(context.Background(), cfg("tx"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tx.Close)
	rxInstance := rx.instance

	// Watch the lease key from here on: the bucket keeps one revision, so a history read would show only the last.
	// Every update seen must be a put by this instance; a delete, purge or other holder means the lease lapsed.
	ljs, err := jetstream.New(anc)
	if err != nil {
		t.Fatal(err)
	}
	lkv, err := ljs.KeyValue(actx, store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	watch, err := lkv.Watch(actx, "rx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = watch.Stop() })
	var (
		leaseMu   sync.Mutex
		leasePuts int
		leaseBad  []string
	)
	go func() {
		for e := range watch.Updates() {
			if e == nil {
				continue // the end of the initial values
			}
			leaseMu.Lock()
			if e.Operation() != jetstream.KeyValuePut {
				leaseBad = append(leaseBad, fmt.Sprintf("revision %d is a %v", e.Revision(), e.Operation()))
			} else {
				var lv leaseValue
				if err := json.Unmarshal(e.Value(), &lv); err != nil || lv.Instance != rxInstance {
					leaseBad = append(leaseBad, fmt.Sprintf("revision %d is held by %q", e.Revision(), lv.Instance))
				}
				leasePuts++
			}
			leaseMu.Unlock()
		}
	}()

	// Before the change, rx may not mail tx (the permission lines of zone b lack zone a): refused fast.
	rctxShort, cancelShort := context.WithTimeout(context.Background(), 5*time.Second)
	_, err = rx.Send(rctxShort, "tx", "before", 0)
	cancelShort()
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("rx -> tx before the change: %v, want a refusal", err)
	}

	// The sender: workers send unique ids, each retried with the same id until it is acknowledged.
	var (
		next       atomic.Int64
		stopSend   = make(chan struct{})
		wg         sync.WaitGroup
		sentMu     sync.Mutex
		sentSeq    = map[string]uint64{} // id -> stream sequence of the ack
		attempts   atomic.Int64
		timeouts   atomic.Int64
		dupAcks    atomic.Int64
		spanned    atomic.Int64 // sends whose ack took over a second: they spanned the outage
		sendFailed atomic.Value
	)
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopSend:
					return
				default:
				}
				id := fmt.Sprintf("m-%05d", next.Add(1))
				deadline := time.Now().Add(25 * time.Second)
				for {
					attempts.Add(1)
					ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
					began := time.Now()
					res, err := tx.SendDetailed(ctx, "rx", "body "+id, SendOptions{ID: id})
					cancel()
					if time.Since(began) > time.Second {
						spanned.Add(1)
					}
					if err == nil {
						if res.Duplicate {
							dupAcks.Add(1)
						}
						sentMu.Lock()
						sentSeq[id] = res.Seq
						sentMu.Unlock()
						break
					}
					var te *SendTimeoutError
					if errors.As(err, &te) {
						timeouts.Add(1)
						if te.Key != id {
							sendFailed.Store(fmt.Sprintf("timeout key %q != id %q", te.Key, id))
							return
						}
					}
					if time.Now().After(deadline) {
						sendFailed.Store(fmt.Sprintf("%s: %v", id, err))
						return
					}
				}
				time.Sleep(5 * time.Millisecond)
			}
		}()
	}
	nSent := func() int { sentMu.Lock(); defer sentMu.Unlock(); return len(sentSeq) }
	waitUntil := func(what string, d time.Duration, cond func() bool) {
		t.Helper()
		end := time.Now().Add(d)
		for !cond() {
			if v := sendFailed.Load(); v != nil {
				t.Fatalf("a send failed for good: %v", v)
			}
			select {
			case err := <-runDone:
				t.Fatalf("the receiver's Run returned while waiting for %s: %v", what, err)
			default:
			}
			if time.Now().After(end) {
				t.Fatalf("timed out waiting for %s (sent %d, shown %d)", what, nSent(), nShown())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitUntil("the stream to flow before the change", 20*time.Second, func() bool { return nShown() >= 30 })
	reconnects := func(e *Endpoint) uint64 { return e.nc.Stats().Reconnects }
	rx0, tx0 := reconnects(rx), reconnects(tx)

	// MID-STREAM: apply policy v2. Both zones' scoped keys change; the server closes both users.
	doApply(zkCompile(t, zkPolicyV2))
	sentAtApply := nSent()

	waitUntil("both endpoints to reconnect", 25*time.Second, func() bool {
		return reconnects(rx) > rx0 && reconnects(tx) > tx0 && rx.nc.IsConnected() && tx.nc.IsConnected()
	})
	// ... and the stream to flow on the new connections.
	waitUntil("the stream to flow after the reconnect", 25*time.Second, func() bool { return nSent() >= sentAtApply+30 })
	close(stopSend)
	wg.Wait()
	if v := sendFailed.Load(); v != nil {
		t.Fatalf("a send failed for good: %v", v)
	}

	// The new template governs the new connection: rx may now mail tx.
	sctx, cancelS := context.WithTimeout(context.Background(), 10*time.Second)
	if _, err := rx.Send(sctx, "tx", "after", 0); err != nil {
		t.Fatalf("rx -> tx after the change: %v", err)
	}
	cancelS()

	// Receiver: every stored message is presented, each exactly once.
	sentMu.Lock()
	want := map[uint64]string{}
	for id, seq := range sentSeq {
		if prev, dup := want[seq]; dup {
			t.Fatalf("ids %s and %s were acknowledged with the same stream sequence %d", prev, id, seq)
		}
		want[seq] = id
	}
	sentMu.Unlock()
	waitUntil("every sent message to be presented", 25*time.Second, func() bool {
		shownMu.Lock()
		defer shownMu.Unlock()
		for seq := range want {
			if shown[seq] == 0 {
				return false
			}
		}
		return true
	})
	// The stream holds exactly one message per id: a retried publish never stored a second copy.
	js, err := jetstream.New(anc)
	if err != nil {
		t.Fatal(err)
	}
	st, err := js.Stream(actx, sap.InboxStream("rx"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := st.Info(actx)
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Msgs != uint64(len(want)) {
		t.Fatalf("the inbox stream holds %d messages for %d distinct ids: a send was stored twice or an ack lied", info.State.Msgs, len(want))
	}
	// The consumer ends with nothing pending and nothing unacked: acks after the reconnect got through, or the message
	// was redelivered (after AckWait, 30 s) and dropped as a duplicate without a second presentation.
	cons, err := js.Consumer(actx, sap.InboxStream("rx"), store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	waitUntil("the consumer to have nothing pending or unacked", 28*time.Second, func() bool {
		ci, err := cons.Info(actx)
		return err == nil && ci.NumAckPending == 0 && ci.NumPending == 0 && ci.AckFloor.Stream == info.State.LastSeq
	})
	shownMu.Lock()
	for seq, n := range shown {
		if n != 1 {
			t.Errorf("stream sequence %d was presented %d times", seq, n)
		}
		if _, sent := want[seq]; !sent {
			t.Errorf("stream sequence %d was presented but is not one of the sent messages", seq)
		}
	}
	shownMu.Unlock()

	// Lease: still held by the instance that took it, never lapsed or taken over, and Run is still going.
	select {
	case err := <-runDone:
		t.Fatalf("Run returned: %v", err)
	default:
	}
	leaseMu.Lock()
	if len(leaseBad) > 0 {
		t.Errorf("the lease lapsed or changed hands: %v", leaseBad)
	}
	puts0 := leasePuts
	leaseMu.Unlock()
	if puts0 < 1 {
		t.Errorf("no lease write seen: the lease was never taken")
	}
	// The refresh loop is still writing: the watch sees one more write after this point. A count of writes over the
	// whole test would depend on how long it ran; this depends only on the loop being alive.
	refreshWait := 10 * LeaseRefresh
	refreshEnd := time.Now().Add(refreshWait)
	puts := puts0
	for {
		leaseMu.Lock()
		puts = leasePuts
		leaseMu.Unlock()
		if puts > puts0 {
			break
		}
		if time.Now().After(refreshEnd) {
			t.Errorf("the lease was not refreshed for %v (%d writes seen)", refreshWait, puts)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	leaseMu.Lock()
	if len(leaseBad) > 0 {
		t.Errorf("the lease lapsed or changed hands: %v", leaseBad)
	}
	leaseMu.Unlock()
	cur, err := lkv.Get(actx, "rx")
	if err != nil {
		t.Fatalf("the lease is gone: %v", err)
	}
	var lv leaseValue
	if err := json.Unmarshal(cur.Value(), &lv); err != nil || lv.Instance != rxInstance {
		t.Errorf("the lease is now held by %q (revision %d), not by this instance", lv.Instance, cur.Revision())
	}
	t.Logf("sent %d ids in %d attempts (%d send timeouts retried with the same id, %d acks said Duplicate, %d sends spanned the outage); rx reconnected %d time(s), tx %d; %d lease writes, all this instance's",
		len(want), attempts.Load(), timeouts.Load(), dupAcks.Load(), spanned.Load(), reconnects(rx)-rx0, reconnects(tx)-tx0, puts)
}
