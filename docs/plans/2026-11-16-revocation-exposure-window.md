# Can the revocation exposure window close? (note)

Lane core, 2026-11-16. Question (lead #586): after a quorum revocation, a server that was down accepts the revoked credential until it syncs the account resolver. Can it sync before it accepts clients, or can mash push again when the server returns?

## Short answer

Not to zero with nats-server as it is. It can be cut from a minute to the resolver interval (measured: about 5 s at `interval: 5s`) with one resolver setting, and mash can make the remainder visible and repairable. Otherwise the documented window stands.

## What nats-server does (read in v2.15.1, the version in go.mod: `server/accounts.go`, `server/server.go`)

- The full resolver (`resolver: {type: full}`) syncs only from a ticker: `DirAccResolver.Start` starts a goroutine that, every `syncInterval`, publishes a pack request carrying its store hash; other servers answer with what differs. There is no sync at startup: the first one comes one interval after the server starts (`NewDirAccResolver` defaults the interval to one minute; the config key is `interval`).
- Nothing gates clients on the resolver: `readyForConnections` waits for the listeners only. An account is looked up in the local store first (`DirAccResolver.Fetch` returns the local JWT when it has one), so a restarted server authenticates users against the account JWT it had when it went down, revocation list included or not.
- `resolver_preload` only fills an account the store lacks; it does not refresh a stale one.
- So no setting makes a rejoining server sync first or refuse clients until it has.

## What can be done

1. **Shorten the interval (a setting, cheap).** `resolver: { type: full, dir: ..., interval: 5s }` on every node. A tick sends one hash (tens of bytes) and a mismatch causes a merge, so a few seconds costs nothing. The window becomes about the interval (measured below: the interval plus under a tenth of a second). `mashctl init --local` is one server and needs nothing. For a cluster the operator's own server config is where it goes, so the operations guide's cluster block (in the cluster-setup change) should carry `interval: 5s`; `adopt` cannot read a remote server's resolver interval, so it cannot check it.
2. **Push again when the server returns (mash side).** A revocation push is idempotent (`running the revoke again is safe` is already the message). `audit` already finds a revocation a server does not hold (`revocation_pending`). A repair action, "push the pending revocations again", is a small addition: it re-sends the claims update for the keys `audit` named. It is still operator-triggered: mash has no resident process on a solo setup (standing order), so nothing sees the server come back by itself. It beats waiting for the interval only if the operator runs it right after starting the node, and it is not needed once item 1 is in place. Not worth building before item 1 is seen to be insufficient.
3. **Operator ordering (documentation).** Start the returning node with its client port closed to clients until `mashctl audit` is clean (a firewall rule or listening on a private address first). That closes the window to zero for an operator who wants it, at the cost of a manual step.

## Recommendation

Do item 1 as documentation in the cluster-setup change and state the window as "the resolver interval" there, with item 3 for those who need zero. Leave item 2 until a deployment shows the interval is not enough. No code change in this PR.

## Measured

Method: a three-node test cluster with the resolver `interval` at 5 s on every node (`TestLiveRevocationWindowRestartedNode`, `cmd/mashctl/revocation_window_cluster_test.go`). For each run it adds an endpoint, shows its credential works on all three nodes, stops node 3, retires the endpoint with a write quorum (2 of 3), checks the other two nodes refuse the credential at once, then starts node 3 again and, every 100 ms from the start call, connects to node 3 alone with the retired credential (no reconnect). `t_accept` is the first attempt the port answers (connected or refused), `t_first_refusal` the first authorization refusal that is followed only by refusals (4 polls). Window is `t_first_refusal - t_accept`. Runs 1 to 3 poll from the start call; run 4 starts polling after the node serves clients and shows routes. The same test was run twice (the first run's numbers, then the second's):

| Run | Interval | Restart to accept | Window | Restart to refusal |
|---|---|---|---|---|
| 1 | 5 s | 0.107 s / 0.109 s | 4.92 s / 4.96 s | 5.03 s / 5.07 s |
| 2 | 5 s | 0.109 s / 0.107 s | 4.95 s / 4.96 s | 5.06 s / 5.07 s |
| 3 | 5 s | 0.108 s / 0.109 s | 4.93 s / 4.94 s | 5.04 s / 5.05 s |
| 4 (polling after ready) | 5 s | 0.057 s / 0.058 s | 5.05 s / 5.05 s | 5.11 s / 5.11 s |

Conclusion: in this one-machine test the restarted node accepted the revoked credential for the resolver interval (about 5 s, 47 of 52 polls) and refused it from the first tick, within about 0.1 s of the interval. A restart is always a full interval from its first tick, so there is no mid-interval case to measure; routes formed within the first tenth of a second here, and over a real network their formation time adds to it, which this test does not measure.
