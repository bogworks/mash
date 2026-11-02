// Command dirbench is a MEASUREMENT TOOL, not product code (bogworks/mash#43, "directory polling grows with the
// square of the endpoint count"). It simulates N endpoints against a NATS cluster that is already running: each one
// has its own connection, polls the directory the way internal/endpoint does (a filtered stream info for the keys,
// then one direct get per key, 64 at a time, five second timeout), publishes a heartbeat on agents.hb.cc.<owner>.<name>
// and counts what its agents.hb.*.<owner>.* subscription delivers. It does not sign or verify anything and does not
// import the Endpoint. It creates the directory bucket from store.DirectoryConfig (replicas settable by flag), writes
// N valid records, runs a warm-up and then a measured window, and prints key=value lines: client counters, and server
// side numbers from /varz deltas and /proc (CPU, RSS) of the server pids it is given.
//
// Usage: go run ./scripts/dirbench -urls a,b,c -mon http://..,.. -pids 1,2,3 -n 100 [-sync] [-replicas 1]
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/store"
)

const (
	defPoll    = 15 * time.Second
	defHB      = 30 * time.Second
	defWarm    = 10 * time.Second
	defMeasure = 60 * time.Second
	fetchConc  = 64 // internal/endpoint fetchConcurrency
	reqTimeout = 5 * time.Second
	owner      = "o"
	clkTck     = 100 // getconf CLK_TCK on Linux
)

type ep struct {
	name string
	nc   *nats.Conn
}

var (
	cStream, cGet, cFail, cTimeout, cLoads, cLoadFail atomic.Int64
	cHBPub, cHBDel                                    atomic.Int64
	measuring                                         atomic.Bool
	durMu                                             sync.Mutex
	durs                                              []float64
)

func main() {
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
	flag.Parse()
	u := strings.Split(*urls, ",")
	m := strings.Split(*mons, ",")
	pl := strings.Split(*pids, ",")
	if *urls == "" || len(m) != len(pl) {
		fmt.Fprintln(os.Stderr, "need -urls, and -mon and -pids of equal length")
		os.Exit(2)
	}
	ctx := context.Background()
	if err := setup(ctx, u[0], *n, *replicas); err != nil {
		fmt.Fprintln(os.Stderr, "setup:", err)
		os.Exit(1)
	}

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
			_, _ = nc.Subscribe("agents.hb.*."+owner+".*", func(*nats.Msg) { cHBDel.Add(1) })
			eps[i] = &ep{name, nc}
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
		go loop(stop, offP, *poll, func() { loadDirectory(e.nc) })
		go loop(stop, offH, *hb, func() { _ = e.nc.Publish("agents.hb.cc."+owner+"."+e.name, make([]byte, 300)); cHBPub.Add(1) })
	}
	time.Sleep(*warm)

	// measured window
	measuring.Store(true)
	var s0, g0, f0, t0, l0, lf0, hp0, hd0 = cStream.Load(), cGet.Load(), cFail.Load(), cTimeout.Load(), cLoads.Load(), cLoadFail.Load(), cHBPub.Load(), cHBDel.Load()
	v0 := varz(m)
	c0 := cpu(pl)
	self0 := cpu([]string{"self"})
	w0 := time.Now()
	time.Sleep(*measure)
	el := time.Since(w0).Seconds()
	v1 := varz(m)
	c1 := cpu(pl)
	self1 := cpu([]string{"self"})
	measuring.Store(false)
	close(stop)

	d := func(a, b int64) float64 { return float64(a-b) / el }
	fmt.Printf("n=%d sync=%v replicas=%d window_s=%.1f\n", *n, *syncStart, *replicas, el)
	fmt.Printf("loads_per_s=%.3f streaminfo_per_s=%.3f directget_per_s=%.3f requests_per_s=%.3f\n", d(cLoads.Load(), l0),
		d(cStream.Load(), s0), d(cGet.Load(), g0), d(cStream.Load()+cGet.Load(), s0+g0))
	fmt.Printf("request_failures=%d timeouts=%d load_failures=%d\n", cFail.Load()-f0, cTimeout.Load()-t0, cLoadFail.Load()-lf0)
	durMu.Lock()
	sort.Float64s(durs)
	fmt.Printf("load_duration_ms p50=%.1f p99=%.1f max=%.1f samples=%d\n", pct(durs, .5), pct(durs, .99), pct(durs, 1), len(durs))
	durMu.Unlock()
	fmt.Printf("hb_published_per_s=%.3f hb_delivered_per_s=%.3f\n", d(cHBPub.Load(), hp0), d(cHBDel.Load(), hd0))
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

func setup(ctx context.Context, url string, n, replicas int) error {
	nc, err := nats.Connect(url)
	if err != nil {
		return err
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		return err
	}
	_ = js.DeleteKeyValue(ctx, store.DirectoryBucket)
	cfg := store.DirectoryConfig()
	cfg.Replicas = replicas
	kv, err := js.CreateKeyValue(ctx, cfg)
	if err != nil {
		return err
	}
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
func loadDirectory(nc *nats.Conn) {
	t0 := time.Now()
	prefix := "$KV." + store.DirectoryBucket + "."
	var keys []string
	for {
		body, _ := json.Marshal(map[string]any{"offset": len(keys), "subjects_filter": prefix + ">"})
		cStream.Add(1)
		m, err := req(nc, "$JS.API.STREAM.INFO.KV_"+store.DirectoryBucket, body)
		if err != nil {
			cLoadFail.Add(1)
			return
		}
		var resp struct {
			Total int `json:"total"`
			State struct {
				Subjects map[string]uint64 `json:"subjects"`
			} `json:"state"`
		}
		if json.Unmarshal(m.Data, &resp) != nil {
			cLoadFail.Add(1)
			return
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
			m, err := req(nc, "$JS.API.DIRECT.GET.KV_"+store.DirectoryBucket+".$KV."+store.DirectoryBucket+"."+k, nil)
			if err != nil || m.Header.Get("Status") != "" {
				bad.Store(true)
			}
		}()
	}
	wg.Wait()
	if bad.Load() {
		cLoadFail.Add(1)
		return
	}
	cLoads.Add(1)
	if measuring.Load() {
		durMu.Lock()
		durs = append(durs, float64(time.Since(t0).Microseconds())/1000)
		durMu.Unlock()
	}
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
