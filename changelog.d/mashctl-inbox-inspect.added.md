`mashctl inbox inspect <address>` shows one endpoint's stored messages and where its `deliver` consumer stands, for the test lane that
needs transport evidence: for each message the stream sequence, `Nats-Msg-Id`, the verified sender and trust label, store time, size,
kind and delivery state, then the consumer's totals. It is read only. It reads stream info, messages by sequence and the existing
consumer's info, and never creates, binds, fetches from or acknowledges a consumer or writes anything, so it is safe to run against a
live inbox. `--json` prints one object (`mash-inbox-inspect@1`, documented in `docs/mashctl.md`); `--from`, `--to` and `--limit`
choose the range; `--legacy` reads an alias's legacy inbox.
The state is derived from the consumer's cursors alone: `acked`, `undelivered`, or `delivered-unacked-or-acked-out-of-order`
(shown as `delivered, ack not confirmed`), because NATS does not report acknowledgements above the ack floor. Per-message delivery
count and ack time are not kept by JetStream or mash, so they are reported as null and the consumer's `num_redelivered` is the aggregate.
