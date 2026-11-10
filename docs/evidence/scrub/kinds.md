# Kind inventory

Date: 2026-11-10. Tree: branch `pr/core/scrub-kinds`, off `main` at `c40e650`. Plan: `docs/plans/2026-11-10-scrub-remainder.md`,
step 2 (REQ-001, AT-006).

REQ-001: each kind is defined by delivery behaviour only, and no kind assumes a role of the sender or recipient beyond
"an endpoint". A kept verdict is valid only when the assumption column reads "endpoint to endpoint". This is judged by
what the kind does, not by the words it uses; the scrub list is not evidence for it.

## Where kinds are defined and acted on

- Definition and parsing: `internal/kind/kind.go`. The kinds are `blocker`, `question`, `task`, `result`, `fyi`. `Parse`
  rejects any other name when sending (`cmd/mash/cli.go:84`, `cmd/mash/serve.go:514`, `internal/endpoint/mail.go:574`).
  `Of` reads a received header value: a missing or unknown one is `task` (`internal/endpoint/mail.go:336`, `:763`,
  `internal/endpoint/hook.go:500`).
- The kind travels as the unsigned `Mash-Kind` header (`internal/endpoint/mail.go`, `KindHeader`). It is the sender's
  claim and never a trust input (`internal/kind/kind.go` package comment; threat model row "Sender changes wake behaviour
  by kind").
- It decides one thing: `Kind.Wakes()` (`internal/kind/kind.go`). The wake rules are in `internal/endpoint/hook.go`
  (idle wake after 45 seconds, backoff, the held-backlog exception `SpoolWakeCount` and `SpoolWakeBytes`) and
  `internal/endpoint/waking.go`, which counts waking mail (`WakingPending`). A verified `--urgent` mark wakes at once,
  whatever the kind.
- Wake detail (all in `internal/endpoint/hook.go` unless noted): with hooks, a waking kind counts toward the idle wake;
  under `SpoolFromStart`, before any hook has run, waking kinds ring at once (`:286`, `:293`); a verified urgent mark
  rings at once for any kind; the one notice of the proven hookless state announces held mail of any kind (`:300`); and
  non-waking mail still wakes the session when the held backlog (all held mail, or the consumer or byte limit being
  full) crowds the consumer (`:288`), unless a drain is in progress (`:322`). Without hooks, delivery is by channel for
  every kind and a non-waking kind is not held for the next turn (`internal/endpoint/mail.go:413`).
- Other behaviours that read the kind:
  - The auto-tag rule `kind.Tag` runs only in the MCP `send` tool (`cmd/mash/serve.go:518`). `SendDetailed` and the CLI
    parse and carry the kind they are given (`internal/endpoint/mail.go:574`, `cmd/mash/cli.go:84`) and never retag. The
    sender is told of a retag (`kind.TaggedNote`) except when the send is a duplicate by ID, which returns first
    (`cmd/mash/serve.go:524`).
  - Rendering: the kind appears on full messages, inbox lines and digest rows (`internal/render/render.go:97`,
    `:130`, `:182`). A summary lists the kinds present, and omits the list when every message is `fyi`
    (`:198`, used by batches and by the wake summary, `internal/endpoint/hook.go:361`).
  - Model-facing text: the MCP `send` tool's `kind` parameter states the enum, the default and which kinds wake
    (`cmd/mash/serve.go:462-468`), and the CLI usage does the same (`cmd/mash/main.go:26`, `:120`). Both describe
    wake behaviour only and assume no role (emission-path map, section J).

Nothing in mash requires, tracks or enforces a reply to any kind, gates a kind by who the sender or recipient is, or
changes trust or permissions by kind.

## Inventory

| Kind | Delivery behaviour | Reply expected by mash? | Assumes of the sender | Assumes of the recipient | Verdict |
|---|---|---|---|---|---|
| `task` (the default) | Wakes an idle recipient after the idle wake (45 s by default; at once if also urgent and verified). Sent when no kind is given; also what a missing or unknown received kind is read as, so old senders still wake. | No. | An endpoint. | An endpoint. | Kept: endpoint to endpoint. |
| `question` | Wakes like `task`. Auto-tag, in the MCP `send` tool only: a reply (`in_reply_to` set) of this kind whose body has no `?` and is at most 280 characters is sent as `fyi` instead, and the sender is told (except on a duplicate send). | No. The auto-tag looks only at the body of a reply. | An endpoint. | An endpoint. | Kept: endpoint to endpoint. |
| `blocker` | Wakes like `task`. No other effect. | No. | An endpoint. | An endpoint. | Kept: endpoint to endpoint. |
| `result` | Does not wake. Waits for the recipient's next hook run or turn. Wakes the session only in the crowding and hookless-notice cases above, never because of its own kind. Opt-in. | No. | An endpoint. | An endpoint. | Kept: endpoint to endpoint. |
| `fyi` | Same as `result`. Also the target of the auto-tag downgrade. A digest of only `fyi` mail shows no kind list. Opt-in. | No. | An endpoint. | An endpoint. | Kept: endpoint to endpoint. |

Reading the assumption columns: the comments in the code say what the sender wants of the recipient ("a task is an
instruction", "an instruction and needs no '?'"). That describes the content of a message, not a standing relation:
any endpoint may send any kind to any endpoint it is allowed to mail, mash checks nothing about their relative rank,
and the same endpoint may send `task` in one message and receive `task` in the next. The words `task`, `blocker` and
`result` read naturally between peers (a request, a statement that the sender is stuck, a report), and none names a
party that owns the work or a party that carries it out.

## Review against the mutation

AT-006's mutation: a kind named `dispatch-to-supervisor` (no scrub-list word) must be rejected by this review. Two
different things reject it, and only the second is the review the contract asks for.

1. Today's code rejects it incidentally: `kind.Parse` accepts only the five names, so sending it fails with `unknown
   kind "dispatch-to-supervisor" (one of blocker|question|task|result|fyi)`. That is a whitelist, not a judgement, and
   it would equally reject a harmless new kind.
2. The review rejects it by behaviour, by the test the inventory applies to every row: what would mash do differently
   for that kind, and what would it assume of the sender and recipient? A kind that exists to route work to a
   supervisor would need mash to know which endpoint is a supervisor (to deliver, gate or rank by it), or would only
   work if recipients were that role. Either is an assumption beyond "endpoint to endpoint", so the assumption
   columns would not read that, and the verdict would not be "kept". A new kind passes only if its row can say what it
   changes in delivery (wake or wait, rendering) and that sender and recipient are any endpoint. The five kinds do.

## Verdict

All five kinds are kept. No kind needs a rename and none changes delivery or wake semantics, so DEC-006 (whether a
renamed kind is still accepted on receive for one release) is not triggered, and nothing goes to Randy as an
amendment.

Two observations for the lead, not changes:
- Model-facing text and docs describe kinds by wake behaviour (`docs/operations.md`, `docs/architecture.md`,
  `README.md`, `cmd/mash/main.go` usage). They assume no role. The map of emission paths
  (`docs/evidence/scrub/emission-paths.md`, J3, I6) defers the kind words to this inventory, which clears them.
- A reviewer other than the implementer is required (AT-006). The implementer of this document is the authoring
  session; the reviewers are the adversarial review on a model of another family, recorded on the pull request, and the
  reviewing session in charge.
