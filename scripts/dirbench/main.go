// Command dirbench is a MEASUREMENT TOOL, not product code (bogworks/mash#43, "directory polling grows with the
// square of the endpoint count"). It simulates N endpoints against a NATS cluster that is already running: each one
// has its own connection, polls the directory the way internal/endpoint does (a filtered stream info for the keys,
// then one direct get per key, 64 at a time, five second timeout), publishes a heartbeat on agents.hb.cc.<owner>.<name>
// and counts what its agents.hb.*.<owner>.* subscription delivers. It does not sign or verify anything and does not
// import the Endpoint. It creates a bucket of its OWN, named dirbench-<8 random hex>, with the product's directory
// config (store.DirectoryConfig: history, storage; replicas settable by flag), writes N valid records, runs a warm-up
// and then a measured window, and prints key=value lines: client counters, and server side numbers from /varz deltas
// and /proc (CPU, RSS) of the server pids it is given.
//
// SCRATCH SERVERS ONLY. The tool refuses to start unless every -urls host is loopback (127.0.0.0/8, ::1, localhost),
// or -i-know-this-is-a-scratch-server is passed. It only ever creates its own uniquely named bucket (after checking
// that neither the bucket nor its stream exists), never touches any other bucket, stream or key, and deletes only that
// bucket at the end, on error and on SIGINT/SIGTERM. It still loads the server with N connections and polls: do not
// point it at a deployment.
//
// -mode old (default) is the behaviour measured in docs/evidence/2026-11-02-directory-polling: every poll is a full
// load. -mode new runs each endpoint's poll through endpoint.DirPoller, the decision logic of ADR 0065 (one unfiltered
// stream info per poll, a full load only when the state moved or the forced reload is due, jittered waits).
//
// -hb-mode (docs/evidence/2026-11-02-heartbeat-fanout) varies the heartbeat side: all (default, today) = every endpoint
// publishes a heartbeat and holds the standing agents.hb.*.<owner>.* subscription; none = it publishes but holds no
// heartbeat subscription; probe = none plus each endpoint answers $SRV.INFO.agents with a -who-payload byte registration-like
// reply and runs a who probe every -who-every (random first offset; 0 = none) or all at once once (-who-burst): subscribe a
// fresh inbox, publish the request, wait endpoint.PresenceQuiet after the last reply or endpoint.PresenceCap. Each
// endpoint's who calls go through the real endpoint.PresenceCache (ADR 0066): a call within -who-gap (default 30s, the
// product's endpoint.PresenceCacheTTL) of its last successful probe is a cache hit and sends nothing; -who-gap 0
// makes every call probe, as the numbers in docs/evidence/2026-11-02-heartbeat-fanout/results.txt were taken.
//
// Usage: go run ./scripts/dirbench -urls a,b,c -mon http://..,.. -pids 1,2,3 -n 100 [-sync] [-replicas 1] [-mode old|new]
//
//	[-hb-mode all|none|probe] [-who-every 5m] [-who-burst] [-who-payload 600] [-who-gap 30s]
package main

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/store"
)

const (
	defPoll    = 15 * time.Second
	defHB      = 30 * time.Second
	defWarm    = 10 * time.Second
	defMeasure = 60 * time.Second
	defWho     = 5 * time.Minute
	fetchConc  = 64 // internal/endpoint fetchConcurrency
	reqTimeout = 5 * time.Second
	owner      = "o"
	clkTck     = 100 // getconf CLK_TCK on Linux
)

type ep struct {
	name  string
	nc    *nats.Conn
	cache endpoint.PresenceCache
}

var (
	cStream, cGet, cFail, cTimeout, cLoads, cLoadFail atomic.Int64
	cState, cReloads                                  atomic.Int64 // mode new: unfiltered state requests; polls that reloaded
	cHBPub, cHBDel                                    atomic.Int64
	cWho, cWhoHit, cReplies, cIncomplete              atomic.Int64 // cache hits that sent nothing; completed probes in the window, replies they received, probes with fewer than n replies
	whoN                                              int
	whoGap                                            time.Duration // -who-gap; 0 = no cache
	whoDurs, whoLast                                  []float64     // probe duration until quiet / until the last reply, ms
	measuring                                         atomic.Bool
	durMu                                             sync.Mutex
	durs                                              []float64
	bucket                                            string // the benchmark-owned bucket; set once in run before any endpoint starts
)

// newBucketName is dirbench-<8 random hex>: a name no deployment uses.
func newBucketName() (string, error) {
	var b [4]byte
	if _, err := crand.Read(b[:]); err != nil {
		return "", err
	}
	return "dirbench-" + hex.EncodeToString(b[:]), nil
}

// checkLoopback reports an error unless every URL's host is localhost or a loopback address.
func checkLoopback(urls []string) error {
	for _, raw := range urls {
		r := strings.TrimSpace(raw)
		if !strings.Contains(r, "://") {
			r = "nats://" + r
		}
		u, err := url.Parse(r)
		if err != nil || u.Hostname() == "" {
			return fmt.Errorf("cannot parse server URL %q", raw)
		}
		h := u.Hostname()
		if strings.EqualFold(h, "localhost") {
			continue
		}
		if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
			continue
		}
		return fmt.Errorf("server %q is not a loopback host: dirbench is for scratch servers only; pass -i-know-this-is-a-scratch-server to override", raw)
	}
	return nil
}

func main() { os.Exit(run()) }

func run() int {
	urls := flag.String("urls", "", "comma separated client URLs of the cluster")
	mons := flag.String("mon", "", "comma separated monitor base URLs (for /varz), same order as -pids")
	pids := flag.String("pids", "", "comma separated nats-server pids")
	n := flag.Int("n", 100, "simulated endpoints")
	syncStart := flag.Bool("sync", false, "every endpoint starts its poll and heartbeat at the same instant")
	replicas := flag.Int("replicas", 1, "replicas of the directory bucket (the product uses 1)")
	poll := flag.Duration("poll", defPoll, "directory poll interval")
	hb := flag.Duration("hb", defHB, "heartbeat interval")
	warm := flag.Duration("warm", defWarm, "wait after ramp-up before measuring")
	measure := flag.Duration("measure", defMeasure, "measured window")
	mode := flag.String("mode", "old", "old: every poll is a full load (as measured); new: poll through endpoint.DirPoller (ADR 0065)")
	hbMode := flag.String("hb-mode", "all", "all: publish heartbeats and hold the standing subscription (today); none: publish, no subscription; probe: none plus who probes")
	whoEvery := flag.Duration("who-every", defWho, "hb-mode probe: each endpoint probes this often (random first offset); 0 = no periodic probes")
	whoBurst := flag.Bool("who-burst", false, "hb-mode probe: every endpoint probes once in the same instant, 5 s into the measured window")
	whoGapF := flag.Duration("who-gap", 30*time.Second, "hb-mode probe: a who within this long of the endpoint's last successful probe is served from the cache (endpoint.PresenceCache; the product's PresenceCacheTTL is 30s); 0 = every who probes")
	whoPayload := flag.Int("who-payload", 600, "hb-mode probe: bytes of each simulated registration reply")
	scratch := flag.Bool("i-know-this-is-a-scratch-server", false, "allow non-loopback -urls (the tool creates and deletes its own dirbench-* bucket only)")
	flag.Parse()
	u := strings.Split(*urls, ",")
	m := strings.Split(*mons, ",")
	pl := strings.Split(*pids, ",")
	if *urls == "" || len(m) != len(pl) {
		fmt.Fprintln(os.Stderr, "need -urls, and -mon and -pids of equal length")
		return 2
	}
	if *mode != "old" && *mode != "new" {
		fmt.Fprintln(os.Stderr, "-mode must be old or new")
		return 2
	}
	if *hbMode != "all" && *hbMode != "none" && *hbMode != "probe" {
		fmt.Fprintln(os.Stderr, "-hb-mode must be all, none or probe")
		return 2
	}
	whoN = *n
	whoGap = *whoGapF
	if whoGap > 0 {
		endpoint.PresenceCacheTTL = whoGap
	}
	if !*scratch {
		if err := checkLoopback(u); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
	}
	name, err := newBucketName()
	if err != nil {
		fmt.Fprintln(os.Stderr, "bucket name:", err)
		return 1
	}
	bucket = name
	ctx := context.Background()
	var created atomic.Bool
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			if created.Load() {
				if err := dropBucket(ctx, u[0], bucket); err != nil {
					fmt.Fprintln(os.Stderr, "cleanup: could not delete bucket", bucket+":", err)
				}
			}
		})
	}
	defer cleanup()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; cleanup(); os.Exit(130) }()
	if err := setup(ctx, u[0], bucket, *n, *replicas, &created); err != nil {
		fmt.Fprintln(os.Stderr, "setup:", err)
		return 1
	}
	fmt.Printf("bucket=%s\n", bucket)

	eps := make([]*ep, *n)
	ramp := time.Now()
	var wg sync.WaitGroup
	sem := make(chan struct{}, 32)
	var connFail atomic.Int64
	for i := range *n {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			name := fmt.Sprintf("e%04d", i)
			nc, err := nats.Connect(u[i%len(u)], nats.Name(name), nats.MaxReconnects(-1))
			if err != nil {
				connFail.Add(1)
				return
			}
			if *hbMode == "all" {
				_, _ = nc.Subscribe("agents.hb.*."+owner+".*", func(*nats.Msg) { cHBDel.Add(1) })
			}
			if *hbMode == "probe" {
				reply := make([]byte, *whoPayload)
				_, _ = nc.Subscribe("$SRV.INFO.agents", func(m *nats.Msg) { _ = m.Respond(reply) })
			}
			eps[i] = &ep{name: name, nc: nc}
		}()
	}
	wg.Wait()
	fmt.Printf("ramp_seconds=%.1f connected=%d connect_failures=%d\n", time.Since(ramp).Seconds(), *n-int(connFail.Load()), connFail.Load())
	stop := make(chan struct{})
	for _, e := range eps {
		if e == nil {
			continue
		}
		var offP, offH time.Duration
		if !*syncStart {
			offP, offH = rand.N(*poll), rand.N(*hb)
		}
		if *mode == "new" {
			go pollNew(stop, e.nc, *poll)
		} else {
			go loop(stop, offP, *poll, func() { _ = loadDirectory(e.nc) })
		}
		go loop(stop, offH, *hb, func() { _ = e.nc.Publish("agents.hb.cc."+owner+"."+e.name, make([]byte, 300)); cHBPub.Add(1) })
	}
	burst := make(chan struct{})
	if *hbMode == "probe" {
		for _, e := range eps {
			if e == nil {
				continue
			}
			if *whoEvery > 0 {
				go loop(stop, rand.N(*whoEvery), *whoEvery, func() { whoCall(e) })
			}
			if *whoBurst {
				go func() {
					select {
					case <-stop:
					case <-burst:
						whoCall(e)
					}
				}()
			}
		}
	}
	time.Sleep(*warm)

	// measured window
	measuring.Store(true)
	cWho.Store(0)
	cWhoHit.Store(0)
	cReplies.Store(0)
	cIncomplete.Store(0)
	var st0, rl0, s0, g0, f0, t0, l0, lf0, hp0, hd0 = cState.Load(), cReloads.Load(), cStream.Load(), cGet.Load(), cFail.Load(), cTimeout.Load(), cLoads.Load(), cLoadFail.Load(), cHBPub.Load(), cHBDel.Load()
	v0 := varz(m)
	c0 := cpu(pl)
	self0 := cpu([]string{"self"})
	w0 := time.Now()
	if *whoBurst {
		time.Sleep(5 * time.Second)
		close(burst)
		time.Sleep(*measure - 5*time.Second)
	} else {
		time.Sleep(*measure)
	}
	el := time.Since(w0).Seconds()
	v1 := varz(m)
	c1 := cpu(pl)
	self1 := cpu([]string{"self"})
	measuring.Store(false)
	close(stop)

	d := func(a, b int64) float64 { return float64(a-b) / el }
	fmt.Printf("n=%d sync=%v replicas=%d mode=%s window_s=%.1f\n", *n, *syncStart, *replicas, *mode, el)
	fmt.Printf("loads_per_s=%.3f streaminfo_per_s=%.3f directget_per_s=%.3f requests_per_s=%.3f\n", d(cLoads.Load(), l0),
		d(cStream.Load(), s0), d(cGet.Load(), g0), d(cStream.Load()+cState.Load()+cGet.Load(), s0+st0+g0))
	fmt.Printf("streaminfo_state_per_s=%.3f reloads_per_s=%.3f\n", d(cState.Load(), st0), d(cReloads.Load(), rl0))
	fmt.Printf("request_failures=%d timeouts=%d load_failures=%d\n", cFail.Load()-f0, cTimeout.Load()-t0, cLoadFail.Load()-lf0)
	durMu.Lock()
	sort.Float64s(durs)
	fmt.Printf("load_duration_ms p50=%.1f p99=%.1f max=%.1f samples=%d\n", pct(durs, .5), pct(durs, .99), pct(durs, 1), len(durs))
	durMu.Unlock()
	fmt.Printf("hb_published_per_s=%.3f hb_delivered_per_s=%.3f\n", d(cHBPub.Load(), hp0), d(cHBDel.Load(), hd0))
	fmt.Printf("hb_mode=%s who_every=%s who_burst=%v who_payload=%d who_gap=%s\n", *hbMode, *whoEvery, *whoBurst, *whoPayload, *whoGapF)
	durMu.Lock()
	sort.Float64s(whoDurs)
	sort.Float64s(whoLast)
	fmt.Printf("who_probes=%d who_probes_per_s=%.3f who_cache_hits=%d who_cache_hits_per_s=%.3f who_replies_per_s=%.1f replies_per_probe=%.1f incomplete_probes=%d\n", cWho.Load(), d(cWho.Load(), 0), cWhoHit.Load(), d(cWhoHit.Load(), 0), d(cReplies.Load(), 0),
		float64(cReplies.Load())/max(1, float64(cWho.Load())), cIncomplete.Load())
	fmt.Printf("probe_ms p50=%.0f p99=%.0f max=%.0f last_reply_ms p50=%.0f p99=%.0f max=%.0f\n", pct(whoDurs, .5), pct(whoDurs, .99), pct(whoDurs, 1),
		pct(whoLast, .5), pct(whoLast, .99), pct(whoLast, 1))
	durMu.Unlock()
	fmt.Printf("server_in_msgs_per_s=%.1f out_msgs_per_s=%.1f in_bytes_per_s=%.0f out_bytes_per_s=%.0f\n",
		(v1[0]-v0[0])/el, (v1[1]-v0[1])/el, (v1[2]-v0[2])/el, (v1[3]-v0[3])/el)
	var sum, mx float64
	for i := range c1 {
		c := (c1[i] - c0[i]) / clkTck / el
		sum += c
		mx = max(mx, c)
	}
	fmt.Printf("server_cpu_cores_sum=%.3f server_cpu_cores_max=%.3f server_rss_mb=%s\n", sum, mx, rss(pl))
	fmt.Printf("bench_cpu_cores=%.3f\n", (self1[0]-self0[0])/clkTck/el)
	for _, e := range eps {
		if e != nil {
			e.nc.Close()
		}
	}
	return 0
}

func loop(stop chan struct{}, off, every time.Duration, f func()) {
	select {
	case <-stop:
		return
	case <-time.After(off):
	}
	f()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			f()
		}
	}
}

// pollNew is one endpoint's poll in -mode new: the real DirPoller decides, with its jittered first wait and waits.
func pollNew(stop chan struct{}, nc *nats.Conn, every time.Duration) {
	p := endpoint.NewDirPoller(nil, nil)
	state := func(context.Context) (endpoint.DirState, error) {
		cState.Add(1)
		m, err := req(nc, "$JS.API.STREAM.INFO.KV_"+bucket, []byte("{}"))
		if err != nil {
			return endpoint.DirState{}, err
		}
		return endpoint.ParseDirState(m.Data)
	}
	t := time.NewTimer(p.FirstWait(every))
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if reloaded, _ := p.Tick(context.Background(), state, func(context.Context) error { return loadDirectory(nc) }); reloaded {
				cReloads.Add(1)
			}
			t.Reset(p.Wait(every))
		}
	}
}

// dropBucket deletes the named bucket; it is only ever called with the bucket this process created.
func dropBucket(ctx context.Context, url, name string) error {
	nc, err := nats.Connect(url)
	if err != nil {
		return err
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		return err
	}
	return js.DeleteKeyValue(ctx, name)
}

// setup creates the benchmark's own bucket (refusing if the name already exists) and fills it with n records.
// created is set as soon as CreateKeyValue succeeds, so the caller deletes exactly what was created.
func setup(ctx context.Context, url, name string, n, replicas int, created *atomic.Bool) error {
	nc, err := nats.Connect(url)
	if err != nil {
		return err
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		return err
	}
	if _, err := js.KeyValue(ctx, name); err == nil {
		return fmt.Errorf("bucket %q already exists on the server; refusing to touch it", name)
	} else if !errors.Is(err, jetstream.ErrBucketNotFound) {
		return fmt.Errorf("checking that bucket %q does not exist: %w", name, err)
	}
	if _, err := js.Stream(ctx, "KV_"+name); err == nil {
		return fmt.Errorf("stream KV_%s already exists on the server; refusing to touch it", name)
	} else if !errors.Is(err, jetstream.ErrStreamNotFound) {
		return fmt.Errorf("checking that stream KV_%s does not exist: %w", name, err)
	}
	cfg := store.DirectoryConfig()
	cfg.Bucket = name
	cfg.Description = "dirbench scratch bucket"
	cfg.Replicas = replicas
	kv, err := js.CreateKeyValue(ctx, cfg)
	if err != nil {
		return err
	}
	created.Store(true)
	for i := range n {
		name := fmt.Sprintf("e%04d", i)
		rec := store.Record{Name: name, Owner: owner, Agent: "A" + name, AgentToken: "tok-" + name + "-0123456789abcdef0123456789abcdef",
			Account: "ACCT0123456789", User: "U" + name + "0123456789", MailSubject: store.MailSubjectFor(owner, "", name),
			Created: "2026-11-02T00:00:00Z", Role: "bench", Host: "bench-host"}
		b, _ := json.Marshal(rec)
		if _, err := kv.Put(ctx, name, b); err != nil {
			return err
		}
	}
	return nil
}

// loadDirectory is internal/endpoint's loadDirectory: keys by filtered stream info, then one direct get per key.
func loadDirectory(nc *nats.Conn) error {
	t0 := time.Now()
	prefix := "$KV." + bucket + "."
	var keys []string
	for {
		body, _ := json.Marshal(map[string]any{"offset": len(keys), "subjects_filter": prefix + ">"})
		cStream.Add(1)
		m, err := req(nc, "$JS.API.STREAM.INFO.KV_"+bucket, body)
		if err != nil {
			cLoadFail.Add(1)
			return errors.New("load failed")
		}
		var resp struct {
			Total int `json:"total"`
			State struct {
				Subjects map[string]uint64 `json:"subjects"`
			} `json:"state"`
		}
		if json.Unmarshal(m.Data, &resp) != nil {
			cLoadFail.Add(1)
			return errors.New("load failed")
		}
		for s := range resp.State.Subjects {
			keys = append(keys, strings.TrimPrefix(s, prefix))
		}
		if len(resp.State.Subjects) == 0 || len(keys) >= resp.Total {
			break
		}
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, fetchConc)
	var bad atomic.Bool
	for _, k := range keys {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			cGet.Add(1)
			m, err := req(nc, "$JS.API.DIRECT.GET.KV_"+bucket+".$KV."+bucket+"."+k, nil)
			if err != nil || m.Header.Get("Status") != "" {
				bad.Store(true)
			}
		}()
	}
	wg.Wait()
	if bad.Load() {
		cLoadFail.Add(1)
		return errors.New("load failed")
	}
	cLoads.Add(1)
	if measuring.Load() {
		durMu.Lock()
		durs = append(durs, float64(time.Since(t0).Microseconds())/1000)
		durMu.Unlock()
	}
	return nil
}

func req(nc *nats.Conn, subj string, body []byte) (*nats.Msg, error) {
	ctx, cancel := context.WithTimeout(context.Background(), reqTimeout)
	defer cancel()
	m, err := nc.RequestWithContext(ctx, subj, body)
	if err != nil {
		cFail.Add(1)
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, nats.ErrTimeout) {
			cTimeout.Add(1)
		}
	}
	return m, err
}

func pct(s []float64, p float64) float64 {
	if len(s) == 0 {
		return 0
	}
	return s[min(len(s)-1, int(p*float64(len(s)-1)+.5))]
}

// varz sums in_msgs, out_msgs, in_bytes, out_bytes over the servers.
func varz(mons []string) [4]float64 {
	var t [4]float64
	for _, m := range mons {
		r, err := http.Get(m + "/varz")
		if err != nil {
			continue
		}
		var v struct {
			A float64 `json:"in_msgs"`
			B float64 `json:"out_msgs"`
			C float64 `json:"in_bytes"`
			D float64 `json:"out_bytes"`
		}
		_ = json.NewDecoder(r.Body).Decode(&v)
		r.Body.Close()
		t[0], t[1], t[2], t[3] = t[0]+v.A, t[1]+v.B, t[2]+v.C, t[3]+v.D
	}
	return t
}

// cpu returns utime+stime clock ticks per pid ("self" is this process).
func cpu(pids []string) []float64 {
	out := make([]float64, len(pids))
	for i, p := range pids {
		b, err := os.ReadFile("/proc/" + p + "/stat")
		if err != nil {
			continue
		}
		f := strings.Fields(string(b[strings.LastIndexByte(string(b), ')')+2:]))
		ut, _ := strconv.ParseFloat(f[11], 64)
		st, _ := strconv.ParseFloat(f[12], 64)
		out[i] = ut + st
	}
	return out
}

func rss(pids []string) string {
	var out []string
	for _, p := range pids {
		b, _ := os.ReadFile("/proc/" + p + "/status")
		for _, l := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(l, "VmRSS:") {
				kb, _ := strconv.Atoi(strings.Fields(l)[1])
				out = append(out, strconv.Itoa(kb/1024))
			}
		}
	}
	return strings.Join(out, "/")
}

// whoCall is one who call by an endpoint: through its PresenceCache (a hit sends nothing) unless -who-gap is 0.
func whoCall(e *ep) {
	if whoGap <= 0 {
		_ = whoProbe(e.nc)
		return
	}
	probed, _ := e.cache.Ensure(context.Background(), func(context.Context) error { return whoProbe(e.nc) })
	if !probed && measuring.Load() {
		cWhoHit.Add(1)
	}
}

// whoProbe is one `who` probe as internal/endpoint's startPresence/awaitPresence do it: a fresh inbox, one request on
// $SRV.INFO.agents, then wait endpoint.PresenceQuiet after the last reply (once there is one) or endpoint.PresenceCap.
// A probe that completes inside the measured window counts; replies are counted per probe.
func whoProbe(nc *nats.Conn) error {
	inbox := nc.NewRespInbox()
	var replies atomic.Int64
	var lastNS atomic.Int64
	start := time.Now()
	sub, err := nc.Subscribe(inbox, func(*nats.Msg) { replies.Add(1); lastNS.Store(int64(time.Since(start))) })
	if err != nil {
		return err
	}
	defer func() { _ = sub.Unsubscribe() }()
	if err := nc.PublishRequest("$SRV.INFO.agents", inbox, nil); err != nil {
		return err
	}
	for {
		time.Sleep(10 * time.Millisecond)
		el := time.Since(start)
		if el >= endpoint.PresenceCap || (replies.Load() > 0 && el-time.Duration(lastNS.Load()) >= endpoint.PresenceQuiet) {
			break
		}
	}
	if !measuring.Load() {
		return nil
	}
	r := replies.Load()
	cWho.Add(1)
	cReplies.Add(r)
	if r < int64(whoN) {
		cIncomplete.Add(1)
	}
	durMu.Lock()
	whoDurs = append(whoDurs, float64(time.Since(start).Milliseconds()))
	whoLast = append(whoLast, float64(lastNS.Load())/1e6)
	durMu.Unlock()
	return nil
}
