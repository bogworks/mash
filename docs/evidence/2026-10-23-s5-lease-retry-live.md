# S5 live check: a crash-resumed session recovers its lease unattended

- **Date:** 2026-10-23, host rlb. **Build under test:** mash `dd2f033095a3fa8201a03fec269dd87c7c7a13a7`
  (`mash version` prints `mash 0.1.0 (commit dd2f033095a3fa8201a03fec269dd87c7c7a13a7)`, built with
  `-ldflags "-X main.commit=<sha>"`). Claude Code 2.1.309, nats-server v2.15.1 (operator mode, JetStream, local
  scratch, no real credentials, no herdr, no theherd).
- Method as in rangehand `docs/spikes/s5-s6.md`, variant B: two real claude sessions `alpha` and `beta`.

## Variant B (claude and `mash serve` killed together, then resumed at once)

```
21:54:38  kill -9 <claude pid> <mash serve pid>   (alpha)
21:54:46  claude --continue relaunched as alpha (dev-channel dialog confirmed)
21:54:46 .. 21:55:58  mash who: alpha offline        (lease held by the dead pid, 90 s TTL)
21:56:10  mash who: alpha online                      (first poll after expiry; 92 s after the kill)
pane:  ← mash: endpoint alpha is already in use by another session (...); mail is off ... retrying every 10s.
       ← mash: endpoint alpha is online; mail is on for this session.
```

No restart of Claude was needed (S5 before the fix: offline for 3+ minutes until a manual restart).
Mail sent after recovery (`#3`, `#4` from beta) was delivered to the session as channel events.

## Holder alive, second session with the same name

A second `alpha` session was started while the first was online and renewing. For the 75 s observed it stayed
out: `mash who` showed one `alpha`, its pane showed only the held notice, and mail `#4` went to the holder.

## Caveats

- The scratch Claude login (a copy of the OAuth token) expired during the run ("Login expired"), so after
  recovery the sessions could receive mail but not answer it. Delivery, not a model reply, is what this check
  shows after recovery; replies were shown in S6 and in the earlier S5 run (`PINEAPPLE`, `MANGO`).
- The 90 s lease expiry itself was observed live (92 s); the unit tests simulate expiry by deleting the key.
