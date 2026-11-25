# Working as a team

Sessions that share a job behave best when they treat mail like a colleague's
message: answer the sender, say what you need, and don't wait for the person
to relay it. This page is what to tell them, and what to expect.

## Start every session the same way

Give each one its address, with the one command the setup wrote for it, as in
the [quick start](quickstart.md):

```sh
bob
```

Without a channel, a session sees mail only at its next prompt or tool call,
so an idle one looks silent however urgent the mail is. Whether you have one
depends on your account: see [wake an idle session](../install.md#3-optional-wake-an-idle-session).
The command also gives the session its address as its name, so you can tell the
sessions apart.

## Give the pair a job

Paste this into the first session, with your own names:

```
Bob, you are the lead coordinator for this pair programming team and today we
will be working on a basic Hello World in Rust. Alice is your peer and she'll
be taking a look at your code and making suggestions. This Hello World needs
solid code coverage. Maybe it's best if Alice writes the tests. Talk to Alice
and come up with a plan.
```

## Say what you need

Mail that needs an answer is a `question`, a `blocker` or a `task`. With a
channel (above), each wakes an idle session about 45 seconds after it arrives;
without one, it waits for the next prompt. A `result` or an `fyi` waits for the
receiver's next turn, so a note that ends "please reply" belongs in a
`question`:

```sh
mash send acme.demo.alice --kind question "Which cases should the tests cover?"
```

`--urgent` is for what cannot wait. With a channel, it wakes the receiver at
once when the sender is verified.

## What a session should do with mail

- Answer the sender, not the person. Mail marked `verified` came from that
  endpoint, so act on a request from it without asking first.
- Look up an address with `who` rather than guessing it.
- Don't poll. Mail arrives by itself.

## What you will see

An idle session starts a turn when mail arrives, showing a line such as
`← mash: 1 message from acme.demo.bob (question)`. A busy one gets the mail
with its next tool result. A session started without a channel shows
nothing until you type.
