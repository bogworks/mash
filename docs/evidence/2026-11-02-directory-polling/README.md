# Directory polling load vs endpoint count (bogworks/mash#43)

Measurement only; no product code changed. Harness: `scripts/dirbench/main.go`. Cluster driver: `run.sh` (starts 3 nats-servers in a temp dir, runs the matrix, kills them). Raw output: `results.txt` (main matrix, bucket R1 as the product configures it) and `results-r3.txt` (one extra run with an R3 bucket).

## Method

- 3-server NATS cluster (v2.15.1), JetStream with file storage in temp dirs, no auth, no TLS, loopback, a monitor port on each. Servers run under `nice -n 10`.
- Directory bucket created with `store.DirectoryConfig()` (the product's config: history 1, file storage, **replicas 1**, i.e. R1; the product never sets replicas) under a benchmark-owned unique name `dirbench-<8 random hex>` (the name does not affect the numbers; the request subjects below use that name in place of `mash-directory`). The bench only creates and deletes that one bucket, and refuses non-loopback servers unless given `-i-know-this-is-a-scratch-server`: it is for scratch servers only, never a deployment. Runs recorded in `results*.txt` were made with an earlier revision of the harness that used the name `mash-directory` on the fresh scratch cluster; the request pattern is identical. N valid `store.Record` JSON records (~330 bytes each) put under the endpoint name with plain KV puts.
- N simulated endpoints in one bench process, each with its own NATS connection spread round-robin over the 3 servers. Each runs:
  - a directory poll every 15 s: filtered stream info on `KV_<bucket>` (`subjects_filter`, paged like `directoryKeys`), then one direct get (`$JS.API.DIRECT.GET.KV_<bucket>.$KV.<bucket>.<key>`) per key, 64 in flight at a time, 5 s timeout, as in `internal/endpoint/client.go`;
  - a heartbeat every 30 s, 300-byte payload, on `agents.hb.cc.o.<name>`;
  - a subscription on `agents.hb.*.o.*` that only counts deliveries (no signature verification).
- Random start offset in [0,15 s) for polls and [0,30 s) for heartbeats; the `-sync` variant starts every endpoint's poll and heartbeat at the same instant.
- Ramp up all connections, wait 10 s, measure 60 s. Server numbers are deltas of `/varz` in_msgs/out_msgs/in_bytes/out_bytes summed over the 3 servers, CPU is utime+stime from `/proc/<pid>/stat` (CLK_TCK 100) over the window divided by wall time, RSS from `/proc/<pid>/status`. The bench's own CPU comes from `/proc/self/stat`.

## Environment

16 cores (nproc), Linux 6.1.0-54-cloud-arm64 (arm64), nats-server v2.15.1, go1.27.3, `ulimit -n` 1048576. Shared host (other work was running on it). 2026-11-02.

## Results (R1 bucket, random offsets unless noted)

| N | loads/s | stream info/s | direct gets/s | requests/s | failures/timeouts | load p50 / p99 (ms) | hb pub/s | hb delivered/s | srv in msgs/s | srv out msgs/s | srv out MB/s | srv CPU cores (sum / max) | srv RSS MB | bench cores |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 10 | 0.67 | 0.67 | 6.7 | 7.3 | 0/0 | 1.3 / 1.6 | 0.33 | 3.3 | 28 | 31 | 0.009 | 0.007 / 0.002 | 19/18/20 | 0.001 |
| 100 | 6.67 | 6.67 | 667 | 673 | 0/0 | 2.6 / 3.8 | 3.3 | 333 | 1,594 | 1,924 | 0.61 | 0.050 / 0.024 | 25/26/27 | 0.03 |
| 500 | 33.3 | 33.3 | 16,653 | 16,686 | 0/0 | 8.4 / 13.4 | 16.7 | 8,333 | 39,001 | 47,317 | 15.0 | 0.77 / 0.41 | 77/76/76 | 0.58 |
| 1000 | 66.7 | 66.7 | 66,666 | 66,733 | 0/0 | 21.8 / 83.2 | 33.3 | 33,333 | 155,825 | 189,124 | 59.8 | 3.20 / 1.65 | 145/134/136 | 2.32 |
| 100 sync | 6.66 | 6.66 | 666 | 673 | 0/0 | 30.5 / 51.3 | 3.3 | 333 | 1,589 | 1,919 | 0.61 | 0.029 / 0.015 | 46/42/42 | 0.02 |
| 500 sync | 33.3 | 33.3 | 16,664 | 16,697 | 0/0 | 599 / 732 | 16.7 | 8,332 | 39,041 | 47,357 | 15.0 | 0.42 / 0.23 | 106/80/79 | 0.42 |
| 500, R3 bucket | 33.3 | 33.3 | 16,667 | 16,700 | 0/0 | 8.6 / 14.0 | 16.7 | 8,333 | 39,051 | 47,368 | 15.0 | 1.22 / 0.42 | 83/83/83 | 0.65 |

N=1000 sustained: all 1000 connections made in 0.2 s, no failures, no timeouts.

## Measured vs predicted

Predictions: loads/s = N/15, requests/s = N(N+1)/15, heartbeat deliveries/s = N^2/30.

| N | loads/s meas / pred | requests/s meas / pred | hb delivered/s meas / pred |
|---|---|---|---|
| 10 | 0.67 / 0.67 | 7.3 / 7.3 | 3.3 / 3.3 |
| 100 | 6.67 / 6.67 | 673 / 673 | 333 / 333 |
| 500 | 33.3 / 33.3 | 16,686 / 16,700 | 8,333 / 8,333 |
| 1000 | 66.7 / 66.7 | 66,733 / 66,733 | 33,333 / 33,333 |

The model matches to within a fraction of a percent. Request rate grows 100x for a 10x increase in N (673 -> 66,733 per second between N=100 and N=1000).

## Observations

- Not a problem for a NATS server at N<=500: 0.77 cores summed over three servers at 500 (16.7k requests/s). At 1000 it is 3.2 cores summed (1.65 on the busiest server), 160-190k server msgs/s and about 60 MB/s out, with load p99 83 ms. Superlinear growth in server CPU is visible (0.77 -> 3.2 cores for 2x N).
- Heartbeat fan-out is the same order as the polling in message count but far smaller in bytes (hb deliveries 33k/s at N=1000 vs 67k direct gets/s), and heartbeat delivery is N^2/30 so it also grows quadratically.
- Synchronised starts do not change the throughput but make each load slow and bursty: N=500 sync p50 599 ms vs 8 ms staggered (a wave of 250k requests lands at once). No timeouts at 5 s here.
- An R3 bucket at N=500 spreads the load but costs more total CPU (1.22 vs 0.77 cores); latency unchanged.
- Direct-get request rate (not server CPU) is what a per-request metered hosted service would see: ~670/s at 100 endpoints, ~16.7k/s at 500, ~66.7k/s at 1000, i.e. per day (x 86,400): 58 M at N=100, 1.44 G at N=500, 5.8 G at N=1000.

## Honest limits

- Simulated endpoints in one process, not the real Endpoint: no signing/verification (the real endpoint verifies each heartbeat, extra client CPU, no server load), no hooks, spool, leases, mail, inbox streams, or `bootstrapPresence` traffic.
- No auth or TLS, loopback network, so per-connection auth and TLS costs and real network latency are absent. Real latency would raise load durations, not the request counts.
- Client and servers share a 16-core shared host. The bench process used 0.58 cores at N=500 and 2.3 cores at N=1000 (one Go process, 16 cores available, not saturated, but it competes with the servers for CPU and cache, so server CPU may be slightly overstated or latency inflated). Measured request rates match the model exactly, so the client kept up.
- `/varz` in_msgs/out_msgs also count route-forwarded and reply traffic between cluster members, which is why they exceed the client request counts (about 2.3x requests/s in); they are cluster-wide totals, not client-visible counts. The R1 stream lives on one server, so requests to the other two cross a route.
- One 60 s window per case, no repeats; CPU numbers at N=10 and 100 are tiny and noisy (the 100 sync run reads lower than the staggered one).
- Records are written once; no churn, so no change traffic. Directory size equals N; a real directory may hold more or fewer records than endpoints running.
- What this says about a hosted account's request metering: only the request counts (stream info + direct gets, quadratic in N) apply if the service counts JetStream API requests; whether it does, how direct gets are metered, and any rate limits or per-account message quotas of a hosted service were not measured here.

## After: ADR 0065 poll (`MODE=new`)

`results-new.txt`: the same host, cluster and harness, `-mode new` (each simulated endpoint's poll runs through the real `endpoint.DirPoller`: one unfiltered stream-info request per poll, a full reload only when the stream's state moved, jittered waits), 40 s warm-up so that every endpoint has made its first (start-up) full load before the 60 s measured window. Random offsets unless noted. Nothing failed: 0 timeouts, 0 failed loads.

| N | reloads/s | state info/s | filtered stream info/s | direct gets/s | requests/s | srv CPU cores (sum / max) | srv out MB/s |
|---|---|---|---|---|---|---|---|
| 10 | 0 | 0.63 | 0 | 0 | 0.63 | 0.007 / 0.002 | 0.004 |
| 100 | 0 | 6.58 | 0 | 0 | 6.58 | 0.022 / 0.008 | 0.118 |
| 500 | 0 | 34.0 | 0 | 0 | 34.0 | 0.163 / 0.058 | 2.58 |
| 1000 | 0 | 66.6 | 0 | 0 | 66.6 | 0.522 / 0.180 | 10.2 |
| 100 sync | 0 | 6.62 | 0 | 0 | 6.62 | 0.014 / 0.005 | 0.118 |
| 500 sync | 0 | 33.3 | 0 | 0 | 33.3 | 0.059 / 0.023 | 2.58 |

Before and after, steady state:

| N | requests/s before | after | srv CPU cores (sum) before | after | srv out MB/s before | after |
|---|---|---|---|---|---|---|
| 100 | 673 | 6.6 | 0.050 | 0.022 | 0.61 | 0.12 |
| 500 | 16,686 | 34.0 | 0.77 | 0.163 | 15.0 | 2.58 |
| 1000 | 66,733 | 66.6 | 3.20 | 0.522 | 59.8 | 10.2 |

- The idle request count is now N/15 per second, as designed (66.6 at 1,000, against 66,733): about 1,000 times fewer at 1,000 endpoints, 100 times fewer at 100. Direct gets and filtered stream infos are zero in steady state, and the synchronised-start variants no longer produce a wave (there is nothing to synchronise: each poll is one small request, and the jitter spreads them).
- What is left on the servers is the heartbeat fan-out (33,333 deliveries per second at 1,000 endpoints, 10 MB/s out), which this change does not touch; it is now the whole of the server's load.
- A first run with the default 10 s warm-up (not kept in the results) mixed start-up reloads into the window: the first poll of each endpoint after start is a full reload at a random offset inside the first 15 s, so a window that opens earlier than that measures them as well. The numbers above use the longer warm-up. A directory change makes every endpoint reload once within an interval (about N(N+1) requests spread over the 15 s), the same cost as one old poll; this window contains no change.
- The forced reload (every 20 minutes, jittered) is outside a 60 s window by construction; its cost is one old-style poll per endpoint per 20 minutes.
