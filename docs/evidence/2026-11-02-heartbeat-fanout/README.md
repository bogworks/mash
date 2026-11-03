# Heartbeat fan-out: standing subscription vs on-demand `who` probe (follow-up of bogworks/mash#43)

Measurement only; no product code changed (`internal/` untouched). Harness: `scripts/dirbench/main.go`, extended with `-hb-mode all|none|probe`, `-who-every`, `-who-burst`, `-who-payload`. Cluster driver: `run.sh` (3 scratch nats-servers in a temp dir, ports 26817.. / 27517.. / 28617.., runs the matrix, kills them). Raw output: `results.txt`.

## What is compared

Every `mash serve` endpoint publishes a heartbeat every 30 s and, today, holds the wildcard subscription `agents.hb.*.<owner>.*` that records lastSeen for `Endpoint.Who()`. Proposal: keep publishing, drop the standing subscription, and have `who` inside serve probe on demand like the CLI (`$SRV.INFO.agents` request, N replies) with a short cache.

- `all` (today): publish heartbeat every 30 s + standing subscription.
- `none`: publish, no heartbeat subscription (the proposal when nobody runs `who`).
- `probe`: `none` + every endpoint answers `$SRV.INFO.agents` with a 600-byte reply, and runs a `who` probe every `-who-every` at a random first offset. A probe is what `startPresence`/`awaitPresence` do: subscribe a fresh inbox, publish the request with that reply subject, collect replies until `endpoint.PresenceQuiet` (300 ms) after the last one or `endpoint.PresenceCap` (2 s). Each probe is answered by all N endpoints (the asker's own included), so it costs N replies.
- Burst (`-who-burst`, `-who-every 0`): all N endpoints probe at the same instant (5 s into the measured window), once.

## Method

As in `docs/evidence/2026-11-02-directory-polling`: 3-server NATS v2.15.1 cluster, JetStream file storage, no auth, no TLS, loopback, `nice -n 10`, own bucket `dirbench-<hex>`, N simulated endpoints in one bench process each with its own connection spread round-robin over the servers, 300-byte heartbeats at random offsets. Directory poll is `-mode new` (ADR 0065, `endpoint.DirPoller`), so the directory cost is already low (about N/15 state requests/s). **40 s warm-up, 60 s measured window**, one run per case. Server numbers are `/varz` deltas summed over the three servers and `/proc` CPU (cores = ticks/100/window). 16 cores, Linux 6.1.0-54-cloud-arm64, go1.27.3, 2026-11-02, shared host. Nothing failed anywhere: 0 request failures, 0 timeouts, 0 failed loads, 0 incomplete probes (every probe collected all N replies).

## Results

"hb deliv/s" = heartbeats delivered to subscribers per second (client counter). Server msgs and MB are cluster totals from `/varz`, so they include route forwarding and the directory state requests (about N/15 per second, in `-mode new`).

### Mode `all` (today)

| N | hb pub/s | hb deliv/s | srv in msgs/s | srv out msgs/s | srv out MB/s | srv CPU cores (sum / max) | srv RSS MB | bench cores |
|---|---|---|---|---|---|---|---|---|
| 100 | 3.3 | 333 | 47 | 377 | 0.12 | 0.022 / 0.008 | 25/23/23 | 0.010 |
| 500 | 16.7 | 8,333 | 188 | 8,505 | 2.58 | 0.160 / 0.056 | 49/49/48 | 0.121 |
| 1000 | 33.3 | 33,337 | 358 | 33,657 | 10.2 | 0.521 / 0.181 | 83/76/74 | 0.427 |

### Mode `none`

| N | hb pub/s | hb deliv/s | srv in msgs/s | srv out msgs/s | srv out MB/s | srv CPU cores (sum / max) | srv RSS MB | bench cores |
|---|---|---|---|---|---|---|---|---|
| 100 | 3.3 | 0 | 41 | 37 | 0.016 | 0.012 / 0.005 | 34/61/60 | 0.003 |
| 500 | 16.7 | 0 | 154 | 138 | 0.074 | 0.036 / 0.015 | 47/81/79 | 0.012 |
| 1000 | 33.3 | 0 | 292 | 258 | 0.144 | 0.061 / 0.027 | 70/110/108 | 0.020 |

(Residual server out traffic is the directory state requests and their replies, about N/15 per second. RSS differs between servers because the bucket's stream lives on one of them.)

### Mode `probe`, who every 5 min per endpoint

| N | probes/s | replies/s | srv in msgs/s | srv out msgs/s | srv out MB/s | srv CPU cores (sum / max) | probe ms to last reply p50 / p99 | probe ms until quiet p50 / p99 | bench cores |
|---|---|---|---|---|---|---|---|---|---|
| 100 | 0.28 | 28 | 88 | 141 | 0.061 | 0.015 / 0.006 | 1 / 2 | 306 / 308 | 0.004 |
| 500 | 1.67 | 833 | 1,548 | 3,196 | 1.41 | 0.071 / 0.027 | 3 / 4 | 310 / 313 | 0.036 |
| 1000 | 3.28 | 3,283 | 5,776 | 12,306 | 5.40 | 0.175 / 0.064 | 5 / 8 | 312 / 315 | 0.103 |

### Mode `probe`, who every 1 min per endpoint

| N | probes/s | replies/s | srv in msgs/s | srv out msgs/s | srv out MB/s | srv CPU cores (sum / max) | probe ms to last reply p50 / p99 | probe ms until quiet p50 / p99 | bench cores |
|---|---|---|---|---|---|---|---|---|---|
| 500 | 8.28 | 4,142 | 7,039 | 15,247 | 6.66 | 0.202 / 0.071 | 3 / 5 | 311 / 313 | 0.121 |
| 1000 | 16.6 | 16,617 | 27,982 | 61,099 | 26.7 | 0.648 / 0.222 | 5 / 13 | 313 / 316 | 0.392 |

`probes/s` x N = `replies/s`: a probe's cost is N replies, so the probe load grows as N^2 / `who-every`, the same shape as the heartbeat fan-out (N^2/30) but with a period of the operator's choosing and only while someone asks. Probe duration "until quiet" is dominated by the 300 ms quiet window, the time to the last reply is milliseconds.

### Worst case: all N endpoints probe in the same second

| N | probes | replies delivered (total) | probe ms to last reply p50 / p99 / max | probe ms until quiet p50 / p99 / max | incomplete | srv CPU cores over the 60 s window (sum / max) |
|---|---|---|---|---|---|---|---|
| 100 | 100 | 10,000 | 21 / 22 / 22 | 329 / 330 / 330 | 0 | 0.015 / 0.006 |
| 500 | 500 | 250,000 | 344 / 375 / 375 | 647 / 680 / 680 | 0 | 0.090 / 0.033 |

N=500 burst: 250,000 replies (about 160 MB of payload) land in well under a second; no probe lost a reply (the 2 s cap was never reached) and the p99 probe took 680 ms. The CPU column is averaged over the whole 60 s window, so it understates the instantaneous peak (the burst itself lasts well under a second).

## Before / after

Server side, steady state, same cluster, `-mode new` directory poll:

| N | hb deliveries/s: all / none / probe 5m / probe 1m | server out msgs/s: all / none / probe 5m / probe 1m | server out MB/s | server CPU cores (sum) |
|---|---|---|---|---|
| 100 | 333 / 0 / 0 / n.m. | 377 / 37 / 141 / n.m. | 0.12 / 0.016 / 0.061 / n.m. | 0.022 / 0.012 / 0.015 / n.m. |
| 500 | 8,333 / 0 / 0 / 0 | 8,505 / 138 / 3,196 / 15,247 | 2.58 / 0.074 / 1.41 / 6.66 | 0.160 / 0.036 / 0.071 / 0.202 |
| 1000 | 33,337 / 0 / 0 / 0 | 33,657 / 258 / 12,306 / 61,099 | 10.2 / 0.144 / 5.40 / 26.7 | 0.521 / 0.061 / 0.175 / 0.648 |

(n.m. = not measured: the 1-minute probe was only specified for N=500 and 1000.)

Observations:

- Dropping the standing subscription takes the heartbeat deliveries from N^2/30 to zero: at N=1000 server out msgs fall 33,657 -> 258 per second (130x), output 10.2 -> 0.14 MB/s, CPU 0.52 -> 0.06 cores. The heartbeat publish itself (N/30 per second in) is negligible.
- A probe every 5 minutes per endpoint is a fraction of today's fan-out: at N=1000, 12.3k msgs/s out (2.7x less than `all`), 5.4 MB/s (1.9x less), 0.175 cores (3x less). It is not free: each probe replies N times, and at N=500 and 1000 it is already about 37 percent of the heartbeat fan-out's server message rate at both N=500 and N=1000. The reply payload (600 B) is about 2x the heartbeat (300 B), which is why bytes fall less than messages.
- The break-even is the probe rate. With every endpoint probing every `W` seconds, replies/s = N^2/W against the heartbeat fan-out N^2/30, so a probe every 30 s per endpoint equals today's message rate (with twice the bytes), and anything less frequent is cheaper. At 1-minute probes, N=1000 costs 61k msgs/s out, 26.7 MB/s, 0.65 cores: more than today (33.7k, 10.2 MB/s, 0.52 cores) in messages and bytes. An unconditional "every serve probes every minute" is worse than the standing subscription; the saving only exists if probes happen on demand (an agent actually calls `who`) and are cached for longer than a few seconds.
- The measured probes model every endpoint probing continuously, which is not what the proposal does: most endpoints never call `who`. The realistic cost is `none` plus a handful of probes, i.e. close to the `none` rows, and the 5-minute rows are an upper-ish bound for an active fleet.
- A burst of N=500 simultaneous probes is survivable (all replies in 375 ms at the slowest, 0 lost). It needs `PresenceCap` (2 s) to be longer than the burst clears; at larger N the burst grows as N^2 and would need re-measuring. Caching of the result and jitter or single-flight (one probe at a time per endpoint) bound it.
- Reply latency to a single probe: 1-13 ms to the last reply up to N=1000 when probes are spread, so the 300 ms quiet window is the cost of a `who`, not the NATS fan-out.

## Client-side cost today (computed, not measured)

The real endpoint verifies every heartbeat it receives, so each endpoint must verify N/30 signatures per second: 3.3/s at N=100, 16.7/s at N=500, 33.3/s at N=1000 (and 333/s at N=10,000). Fleet-wide that is N^2/30 verifications per second (33,333/s at N=1000), every one of them for a value (lastSeen) that is read only by `who` inside serve. `-hb-mode none` removes all of it; `probe` replaces it with each endpoint answering every probe anyone makes (probes/s, e.g. 3.3/s at N=1000 with 5-minute probes from everyone) plus the asker processing N replies per `who`. What one verification costs was not measured here.

## Honest limits

- Simulated endpoints in one bench process, not the real Endpoint. No signature creation or verification of heartbeats (the real endpoint verifies them: client CPU only, no server load; `bench cores` here understates real client CPU in `all` mode). No hooks, spool, leases, mail or the real micro service handler.
- The reply payload is an estimate: `-who-payload 600` bytes of zeros as a stand-in for a registration (the real `$SRV.INFO` reply is JSON with the name, owner, host, role and endpoint metadata; its true size was not measured). The probe request carries no payload. The asker here only counts replies: it does not parse JSON or build a table, which a real `who` must do for N replies (about N x 600 bytes: 0.6 MB at N=1000, per call).
- Probes in `probe` mode are driven by a timer in every endpoint (plus the burst), not by real demand; the 5 min and 1 min rows are loads, not predictions of how often `who` is called. They do not include the proposed cache.
- No auth, no TLS, loopback: no network latency, no per-connection costs. All numbers are one 60 s window per case, no repeats; the tiny CPU figures (N=100, `none`) are noisy, and RSS depends on which server hosts the bucket's stream.
- Client and servers share a 16-core shared host (other lanes' nats-servers were running and were not touched); in `all` mode the bench process used up to 0.43 cores, so server CPU may be slightly overstated or latency inflated.
- Random first offsets spread probes evenly, so the 5- and 1-minute rows are steady-state means; the burst row is the only synchronised case. Directory polling is `-mode new` throughout; no directory change occurs in a window, so reloads are not included.
- N is capped at 1000 here; the quadratic shapes (both the heartbeat and the probe loads) mean larger fleets would differ much more than linearly.

## After: the implemented change (ADR 0066)

`results-after.txt`: the real `endpoint.PresenceCache` (30 s cache, single-flight, 5 s retry gap) drives each simulated endpoint's `who` probes (`-hb-mode probe`, default `-who-gap 30s`); no standing heartbeat subscription (`none` and `probe`); directory poll in `-mode new`; 40 s warm-up, 60 s window, same host and cluster. 0 failures, 0 timeouts, 0 incomplete probes in every case.

| N | variant | hb deliveries/s | server out msgs/s | server out MB/s | server CPU cores (sum / max) | probes/s (cache hits/s) | probe until quiet (ms p50 / p99) |
|---|---|---|---|---|---|---|---|
| 100 | today (`all`) | 333 | 377 | 0.12 | 0.022 / 0.008 | none | none |
| 100 | nobody asks | 0 | 37 | 0.016 | 0.012 / 0.005 | 0 | none |
| 100 | each asks every 5 min | 0 | 129 | 0.056 | 0.014 / 0.006 | 0.25 (0) | 305 / 309 |
| 500 | today (`all`) | 8,333 | 8,505 | 2.58 | 0.160 / 0.058 | none | none |
| 500 | nobody asks | 0 | 137 | 0.074 | 0.035 / 0.014 | 0 | none |
| 500 | each asks every 5 min | 0 | 3,316 | 1.46 | 0.069 / 0.026 | 1.73 (0) | 310 / 313 |
| 500 | each asks every 10 s (capped by the 30 s cache) | 0 | 18,888 | 8.25 | 0.241 / 0.084 | 10.4 (39.8) | 306 / 314 |
| 1000 | today (`all`) | 33,337 | 33,657 | 10.2 | 0.521 / 0.180 | none | none |
| 1000 | nobody asks | 0 | 259 | 0.145 | 0.059 / 0.026 | 0 | none |
| 1000 | each asks every 5 min | 0 | 12,977 | 5.69 | 0.175 / 0.064 | 3.45 (0) | 312 / 316 |
| 1000 | each asks every 10 s (capped by the 30 s cache) | 0 | 76,417 | 33.4 | 0.743 / 0.253 | 21.3 (79.3) | 313 / 320 |

- Nobody asking (what almost every session does): 130 times fewer server messages and 7 to 9 times less server CPU at 1,000 endpoints (0.52 to 0.06 cores), no heartbeat delivered to anyone, no signature to verify on the client.
- The cache works as designed: at "every 10 s" each endpoint made one probe per 30 s and answered the other two asks from memory (21.3 probes/s against 79.3 hits/s at 1,000), so the probe rate is capped at N/30 (33/s at 1,000) however often `who` is called.
- That cap is not free at the extreme: with every endpoint asking all the time at 1,000 endpoints the servers carry 76k messages per second (cluster totals include route forwarding), 33 MB/s and 0.74 cores, against 10 MB/s and 0.52 cores today (about 1.4 times the CPU and three times the bytes, because a reply is larger than a heartbeat). No session does that; the case shows the bound, not the expectation.
- A probe takes about 312 ms until quiet at 1,000 endpoints (300 ms of it the quiet window); the last reply arrives within 11 ms.
- Honest limits as above (simulated endpoints, no signature verification, loopback, shared host, one 60 s run per case, the 600 B reply size is an estimate).
