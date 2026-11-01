# Lease handoff (#44) and serve startup round trips (#121): live proof, 2026-10-31 21:17-21:34Z

Binary: `mash 0.0.0-dev (commit 7ebf0ab4a05dbe47116d3fd2bd9452ff47059)`, built from main (7ebf0ab, includes #44 as f4001c0 and #121 as 6b6e335). Baselines
built from `git archive` of each merge's first parent: pre-#44 = e76a299, pre-#121 = 8dec1c0 (no git state touched). Host test-host, Linux arm64, Go 1.27.3.

Real processes only: `mash admin init`, `mash admin provision` (and `deprovision`), `mash send`, `mash who`, `mash inbox`, `mash serve`. The NATS server is
nats-server v2.15.1 embedded through `internal/testenv` (operator mode, account MASH with JetStream, users issued with mash's permission template) by
a throwaway helper (`harness/natsenv-main.go.txt`; the second run used a copy with a bigger disk limit). The standalone nats-server binary
was not used; testenv was the shorter path to the same server configuration. Each endpoint ran under its own temp HOME and XDG_STATE_HOME, so
nothing touched `~/.config/mash`. `mash serve` is an MCP stdio server, so a small client (`harness/drv.py`) does the MCP handshake and logs channel
notifications with timestamps. The delay proxy is `harness/proxy.py` (python3 asyncio TCP proxy, fixed delay each way, logs the PUB subjects it forwards).
No credential file contents appear anywhere here. Full output: `timeline.log`; scripts: `harness/`. The harness scripts take their work directory from `LRL_DIR` (holding `lib.sh` and `run/`) and the binary under test from `BIN`; the original run used a scratch directory, shown as `<scratch>` in this receipt.

## Verdict

| case | result |
|---|---|
| A  lease handoff after `kill -9` (#44) | PASS |
| B1 open_timeout=2s over a >3 s proxy fails within about 2 s | PARTIAL: fails in 2.01-2.03 s, but the message does not name open_timeout (see below) |
| B2 env overrides and invalid values | PASS for MASH_OPEN_TIMEOUT and for rejection messages; MASH_CALL_TIMEOUT shows its effect on `send` only, not on `who`, `inbox` or `read` (observation below) |
| B3 startup is about 7 x RTT plus a fixed cost, flat in directory size | PASS (7 waves, 1.375 s at RTT 100 ms, identical at 10 and 40 endpoints) |

## Case A: lease handoff (PR #44, G270)

Procedure (`harness/caseA.sh`, per run on a fresh endpoint): serve #1 for X takes the lease; serve #2 for X starts while #1 is alive; mail is sent
to X while both run; #2 is stopped; `kill -9` serve #1; serve #3 for X starts 17-18 ms after the kill; `mash send` to X; time to the first mail notification serve #3 emits.

| binary | kill -9 to mail delivered by the new serve | serve #3 start to delivery |
|---|---|---|
| main (#44), endpoint x | 1.16 s | 1.14 s |
| main, endpoint e02 | 0.90 s | 0.89 s |
| main, endpoint e03 | 0.84 s | 0.82 s |
| pre-#44 (e76a299), endpoint e01 | 81.59 s | 81.57 s |

The 0.7-1.1 s on main is serve's own startup floor (B3 below measures 0.71 s from spawn to first consumer pull with no link delay), not a lease wait.
The pre-#44 number is inside the 90 s lease TTL, as predicted (the dead holder's name stayed fenced for about 80 s).

Negatives, on main:

1. Live holder is kept. Serve #2 started while #1 renewed for 35 s (a 30 s renewal period inside it) got one notice, `mash: endpoint e02 is already in use by another
   session (host test-host, pid 811079, since 2026-10-31T21:25:50Z); mail is off for this session. mash is retrying every 10s.` Mail sent while both ran (`m-alive`)
   arrived at #1 only (`1 message from sender (task)`); #2 never reported mail. At the end both were alive and `who --long` still showed the lease held (same for x and e03).
2. A dead holder's slot is not taken for a live one. After `kill -9`, serve #3 never printed "already in use" (0 occurrences in all three main runs) and took the lease at once,
   while the killed pid's lease key was still inside its TTL. The pre-#44 control shows the failure being fixed: serve #3 reported `endpoint e01 is already in use by another session
   (... pid 749945 ...)` for the dead pid 749945, and came online 80.1 s after its start (`mash: endpoint e01 is online; mail is on for this session.`). Not exercised live: pid reuse by an unrelated live
   process (covered by `TestLeaseReusedPIDTakenOver` in the unit tests).

## Case B: serve startup and the open/call timeouts (PR #121)

### B1: open_timeout=2s against a proxy that adds more than 3 s

`config.json` carried `"open_timeout":"2s"`; `MASH_URL` was the proxy's address from the same file.

| proxy delay each way | command | elapsed | output |
|---|---|---|---|
| 3.5 s (RTT 7 s) | `mash who` | 2.03 s, exit 1 | `mash: connect nats://127.0.0.1:45699: context deadline exceeded` |
| 3.5 s | `mash serve` (MCP handshake answered) | stays up; initialize answered at once | stderr: `mash: connect nats://127.0.0.1:45699: context deadline exceeded; retrying (a transient error does not end the wait)` |
| 0.8 s (RTT 1.6 s) | `mash who` | 2.01 s, exit 1 | `mash: connect nats://127.0.0.1:36701: context deadline exceeded` |
| 0.8 s | `mash serve` | as above | same stderr line |

The failure arrives inside the 2 s bound. The message says only "context deadline exceeded": it does not name open_timeout, which the brief asked for. The repo's own test
(`openTimedOut` in `cmd/mash/timeouts_test.go`) accepts "deadline exceeded" or "i/o timeout", so this matches what #121 shipped; it is a usability gap, not a regression. Serve does not exit on it: it keeps
retrying, and its MCP session answers (the tool call reports "retrying").
Ad hoc, not in timeline.log: with the default 20 s open_timeout and the 3.5 s proxy, `mash who` failed after 5.5 s with `nats: Authentication Timeout`, the server giving up on the late CONNECT before the client's timeout.

### B2: environment overrides and invalid values

| check | result |
|---|---|
| `MASH_OPEN_TIMEOUT=2s`, no value in file, RTT 7 s | fails in 2.01 s |
| file `open_timeout` 60 s, `MASH_OPEN_TIMEOUT=2s` | fails in 2.01 s: the environment wins |
| `MASH_CALL_TIMEOUT` over RTT 1.6 s, `mash send e07` | `1s`: `send to e07 timed out ... (context deadline exceeded)` after 5.02 s; `3s`: sent after 5.62 s; default 15 s: sent after 5.62 s |
| `MASH_OPEN_TIMEOUT` of `0`, `5`, `abc`, `-1s`, `0s`; `MASH_CALL_TIMEOUT` of `0`, `5`, `-1s` | each exits at once: `mash: MASH_OPEN_TIMEOUT "5" is not a duration above zero (for example "20s" or "1m")` (the CALL one names MASH_CALL_TIMEOUT) |
| `config.json` `open_timeout`/`call_timeout` of `"0"`, `"5"`, `"0s"` | `mash: config.json "open_timeout" "5" is not a duration above zero ...` |
| `config.json` `"open_timeout":0` (a JSON number) | `<HOME>/.config/mash/config.json: json: cannot unmarshal number into Go struct field file.open_timeout of type string` (names the field, not the "above zero" rule) |
| `MASH_OPEN_TIMEOUT=45s MASH_CALL_TIMEOUT=30s`, direct | works, 0.31 s |

Observation: `MASH_CALL_TIMEOUT=500ms` did not fail `mash inbox` (4.02 s) or `mash read 1` (4.52 s), and `mash who` with 1 s passed (5.62 s) over RTT 1.6 s. Those commands' requests
completed despite a call timeout shorter than one round trip, so the call timeout is visible live only on `send` here. Not investigated further (no source changes made).

### B3: serve startup against link delay and directory size

Measured from process spawn to the inbox consumer's first pull (`$JS.API.CONSUMER.MSG.NEXT.inbox-e05...`) seen at the proxy, the last thing startup does. Three runs after a warm-up each,
endpoint e05, spread under 0.01 s except where noted. "Waves" are clusters of dependent client-to-server requests after the TCP connect, read from the proxy log.

| binary | directory | proxy RTT | first pull after spawn | waves after connect |
|---|---|---|---|---|
| main | 10 endpoints | 0 | 0.71 s | 2 |
| main | 40 endpoints | 0 | 0.71 s | 2 |
| main | 10 endpoints | 100 ms | 1.375 s | 7 |
| main | 40 endpoints | 100 ms | 1.375 s | 7 |
| pre-#121 | 10 endpoints | 100 ms | 1.78 s (one run 1.90 s) | 11 |
| pre-#121 | 40 endpoints | 100 ms | 1.78 s (one run 1.80 s) | 11 |

Main: 0.71 s floor plus 7 x 100 ms = 1.41 s, measured 1.375 s. At RTT 100 ms the proxy log shows the sequence at +0.15, +0.26, +0.36, +0.46, +1.06, +1.27 and +1.37 s after the TCP accept
(lease read and stream infos, a batched wave of directory gets, the lease write, one more directory get, the inbox stream info, heartbeat plus consumer info, then the pull). A 0.6 s pause between the 4th and 5th wave is not a
round trip: it matches the fixed cost seen at RTT 0 (0.71 s total), whose cause was not investigated. The wave count and time are identical with 10 and 40 registered endpoints (the directory reads ride in one batched wave).
Before #121, the same link cost 11 waves (1.78 s), 4 more than the 7 in the budget `TestServeStartupRoundTrips` pins. Honest limit: the pre-#121 binary is also flat from 10 to 40 endpoints, so the 10-vs-40 comparison does
not discriminate the two builds; the wave count (11 to 7) does.

## What failed or did not go as planned

- First scale attempt: provisioning e08..e37 on testenv's default account limits stopped at 14 endpoints, each further `mash admin provision` printing exactly
  `mash: inbox stream: nats: API error: code=500 err_code=10047 description=insufficient storage resources available` (256 MiB of disk, 16 MiB reserved per inbox stream). A throwaway copy of testenv
  with an 8 GiB limit was used for the final 10-vs-40 runs. The first pass at N=14 gave the same 1.373 s and 7 waves.
- My first A script counted serve's "already in use" notice as delivered mail, which made one pre-#44 run look instant (0.73 s). That run is discarded and omitted from timeline.log; the table's pre-#44 row is the corrected rerun.
- A shared `<scratch>/live` directory (the brief's suggested location) was deleted by another process mid-run, so work moved to `<scratch>/lrl`. No data was lost.

## Cleanup

Every process started (NATS helper, proxies, serve clients) was stopped; `scratch-live/` was removed; the temp dirs under `<scratch>/lrl` hold the NATS store and per-endpoint HOME dirs, which contain minted
creds for a throwaway operator and are not part of the evidence. No source file, git ref or commit was changed.
