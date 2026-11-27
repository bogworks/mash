# Roadmap

This is a proposed sorting of mash's coming work into Now, Next and Later. It
is a proposal and it may change. Now is what has shipped. Nothing under Next
or Later is a promise: an item can move, shrink or drop out, and none of them
has a date.

mash is a Claude Code plugin. It gives a session an inbox and registers it as
an endpoint on a NATS mesh. This page says where that is going. What mash does
today is in the [guide](docs/guide/) and the
[reference](docs/reference/README.md).

The generated reference lists what is planned, as it stands today. See
[planned commands](docs/reference/planned-not-yet-released.md). It is not a promise
of any syntax.

## Now

Now is the work after 0.6.1, the current release. What 0.6.0 and 0.6.1
shipped is below; nothing in Next has been started on this page's say-so.

Shipped in 0.6.0:

- mash's internals moved onto the SAP Go SDK. Nothing you do with mash
  changes, and nothing you have set up has to change.
- Sender headers are read strictly, as the SDK reads them. A few malformed
  headers that older mash accepted are refused instead, and refused mail is
  listed, never hidden. See the
  [0.6.0 release notes](docs/release-notes/0.6.0.md).
- The one-time 0.5.5 migration script and the refusal of a 0.5.5
  configuration folder are gone.

Shipped in 0.6.1:

- `mash doctor` run at a plain shell warns when mash cannot create its state
  folder ([#1907](https://github.com/bogworks/mash/issues/1907)).
- Release tags are signed. See the
  [0.6.1 release notes](docs/release-notes/0.6.1.md).

Security policy and `mash sap` are not in 0.6.0 or 0.6.1.

## Next

Next is planned and may change. These are the items we expect to work on
after Now. None has a version or a date.

- Policy: you say who may send to whom. mash ships open, and narrowing the
  mesh is a choice you make.
- Prompting an agent through the SAP protocol, as a first-class action beside
  mail.
- Sharing one server between owners. Today a deployment has one owner (ADR 0006); a change to that is a decision still to be made.
- Asking to join, by administrator approval: an endpoint asks to join the mesh
  and an administrator approves it.

## Later

Later is a short, unordered list of ideas. Each one is named somewhere in
these docs as not yet scheduled. None of them has a design, a date or a
promise.

- Administering a hosted NATS service from mash
  ([#876](https://github.com/bogworks/mash/issues/876)), and guides for a
  local server and for Synadia Cloud
  ([#1037](https://github.com/bogworks/mash/issues/1037)). See
  [Synadia](docs/synadia.md).
- A study of the MCP Events extension as the way to wake a session. Channels
  stay until then.
- Scoping the directory and the NATS service subjects to the owner, so a
  credential reads only its own owner's entries. See
  [comparison](docs/comparison.md).
- Approving a request to join by policy, as well as by an administrator.
