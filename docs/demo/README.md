# Demo: two Claude Code sessions exchange mail

`two-sessions.cast` is a real recording (nothing scripted or edited except trimming the end and replacing the
working-directory path in the banner): two Claude Code sessions, `alice` (right) and `bob` (left), on a free
[Synadia Cloud](https://cloud.synadia.com) account set up with [`scripts/quickstart-synadia.sh`](../../scripts/quickstart-synadia.sh).

Play it in a terminal (the recording has long waits; `-i 2` caps any pause at two seconds):

```sh
asciinema play -i 2 docs/demo/two-sessions.cast
```

## What happens

1. Both sessions start as `MASH_NAME=<name> claude --channels plugin:mash@mash`. `bob` sits idle: nothing is typed in it.
2. In `alice` the only prompt is: *Mail bob a task: run uname -sr and mail me the output. Then wait.*
   `alice` calls the `send` tool and says the task went out.
3. `bob`, idle, **is woken by the mail**: a `← mash: 1 message from alice (task)` line starts a turn on its own. `bob`
   runs `uname -sr` and mails the answer back.
4. Asked "any reply from bob yet?", `alice` reports bob's reply, shown as verified (every message is signed with the
   sender's own NATS key).

The screen as it ended (abridged, from the final frames of the recording):

```
bob                                                         alice
← mash: 1 message from alice (task)                         ❯ Mail bob a task: run uname -sr and mail me the output. Then wait.
  Called plugin:mash:mash, ran 1 shell command              ● I mailed bob the task (message #2) ... waiting for his reply.
● I ran uname -sr and mailed the output to alice            ❯ any reply from bob yet?
  as a reply to their task message (#2). The output         ● Yes, bob replied (verified): uname -sr printed
  was Linux 6.1.0-54-cloud-arm64.                             Linux 6.1.0-54-cloud-arm64.
```

## How it was made

- mash 0.3.0 built from this repository; the plugin installed from `mash plugin export`.
- The endpoints were created with `scripts/quickstart-synadia.sh --owner demo --endpoint alice --endpoint bob`.
- `MASH_IDLE_WAKE=5s` was set for both sessions so the wake arrives within seconds. The default is 45 seconds
  (a blocker, question or task wakes an idle session after that; see [operations](../operations.md)).
- The model's wording differs from run to run; the mechanism (wake by mail, signed delivery, the reply) does not.
- The endpoints, their NATS users and the temporary admin user were deleted afterwards.
