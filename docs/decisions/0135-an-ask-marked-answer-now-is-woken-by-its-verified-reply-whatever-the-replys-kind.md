---
status: proposed
date: 2026-11-15
decision-makers: the mash coordinator (mash-lead), from the peer-status design (U2); no ruling needed (it changes nothing on the wire)
---

# An ask marked answer-now is woken by its verified reply, whatever the reply's kind

Amends: ADR-0027

## Context and Problem Statement

[ADR 0027](0027-mail-kinds-decide-wake-or-wait.md) lets a mail's kind decide whether it wakes its recipient:
* `blocker`, `question` and `task` wake;
* `result` and `fyi` wait;
* a short `question` with no `?` is downgraded to `fyi`.

That works for the recipient. It fails the asker. A sender that needs an answer now cannot make the **reply** wake it. The replier chooses the reply's kind, and a reply sent as `fyi`, or downgraded to it, waits until the asker's session next takes a turn by itself. The asker learns of an answer late, or not at all, while it waits.

## Decision Drivers

* Nothing changes on the wire, and nothing changes for the recipient.
* Only signed facts may cause the wake, so a third party cannot trigger it.
* A kind schedules delivery and never grants trust (ADR 0027). The same must hold here.
* Bounded: an asker cannot make every later message urgent.

## Considered Options

* The asker marks the ask `answer: now`, and its own mash raises the matching verified reply to a wake (chosen).
* The replier's mash sends replies to waking asks as urgent.
* Leave it to the replier's choice of kind.

## Decision Outcome

Chosen option: "the asker marks the ask, and its own mash raises the matching verified reply".

**Sending an ask that expects an answer now.**
* The MCP `send` tool takes `answer: "now"` and `answer_within`. `answer_within` is a duration: the default is 10 minutes, the maximum 24 hours, and it is refused without `answer: "now"`.
* They are the asker's local instruction to its own mash. Nothing is sent on the wire, and the recipient sees an ordinary mail.
* `mash serve` keeps the ask in a small table in the state dir (`asks/<owner>/<name>.json`, written atomically), which survives a restart. Each entry holds:
  * the recipient's user key from its directory record;
  * the receipt sequence in the recipient's inbox;
  * the time sent;
  * the deadline.
* At most 20 asks are open. A 21st is refused before anything is sent.
* An entry is dropped after 24 hours.
* The receipt says `will wake you on the reply`.
* The CLI `mash send` is unchanged (ADR 0034).

**The wake.** One incoming message closes the ask and rings like an urgent mail, whatever its `Mash-Kind`. It must meet all three conditions:
* its trust is `verified`;
* its signed `Mash-In-Reply-To` verifies (ADR 0063) and equals the ask's receipt sequence;
* its sender's user key is the ask's recipient's.

Only the first such reply closes the ask. Later replies follow their own kind.

Nothing about the message changes:
* its trust label stays as it was, and so does its text;
* `urgent` is not set, so the message does not claim an urgency its sender did not sign;
* a separate local flag, `answer_wake`, marks it for the delivery schedule only.

A message that fails any of the three conditions, including an unverifiable or forged in-reply-to, follows its own kind as before.

**What this amends.** ADR 0027 says the kind decides wake or wait. For the one reply that answers an `answer: now` ask, the asker's own instruction decides instead. A kind still never grants trust, and neither does this.

**The deadline wake.** `mash serve` scans the open asks every 5 s. When an ask's deadline passes and it is still open, mash first looks at the asker's inbox.
* If a verified message from the recipient was stored since the ask, the notice says so: `<recipient> sent mail at <time> (not marked as an answer to #<seq>)`.
* Otherwise mash asks the recipient's status and wakes the session with a notice such as `No answer to #<seq> to <recipient> after 10m. It reports: blocked (question).` Each other outcome has its own sentence: working, not reporting, no answer, and not available.
* The ask is then closed. A reply that closes the ask first means no notice.

The notice is mash's own:
* It goes through serve's existing local-notice path, and its lines start `mash:`.
* It has no peer sender, no inbox sequence of its own and no trust label.
* It carries only state words and mash's own numbers, never text the peer sent.

Asks that expired while serve was not running are reported once, in one combined notice at the next start.

The send receipt says `will wake you on the reply, or at <HH:MMZ> with its state if none came`.

Limitation: like serve's other local notices, it is a channel event. On a host that takes no channel delivery, it may not reach the session.

### Consequences

* Good, because an answer reaches the asker at once, even when the replier sent it as `fyi`.
* Good, because every fact that causes the wake is signed. A third party cannot forge the in-reply-to or the sender.
* Bad, because a replier whose mash does not set `in_reply_to` is not recognised. Its reply follows its own kind. The deadline wake then reports, at the deadline, that the recipient sent mail.
* Bad, because a reply that arrives in the few milliseconds between the send's acknowledgement and the ask being recorded is missed. Such a reply follows its own kind.
* Neutral, because a held answer-wake mail can ring again on the normal idle backoff, as a held urgent mail does.

### Confirmation

* `internal/endpoint/askwake_test.go` covers:
  * the options' parsing and limits;
  * the 21st ask refused;
  * persistence and reload;
  * the first match closing the ask;
  * the 24-hour expiry.
* The reply-wake tests show:
  * a verified `fyi` reply to the ask wakes the asker, with `answer_wake` set and its trust, kind and in-reply-to unchanged;
  * a second reply does not get the flag;
  * no wake from a reply without in-reply-to, from another sender with the same number, with a different number, or with a forged in-reply-to.
* `cmd/mash/send_answer_now_test.go` covers the receipt line, the refusals, and that the CLI is unchanged.

## Pros and Cons of the Options

### The asker marks the ask, and its own mash raises the reply (chosen)

* Good, because it works with any replier that sets `in_reply_to`, old or new, and needs nothing from the replier.
* Bad, because the asker must ask for it per send.

### The replier's mash sends replies to waking asks as urgent

* Good, because the asker does nothing.
* Bad, because it depends on the replier's software, and the asker cannot make an older replier comply.

### Leave it to the replier's choice of kind

* Good, because it needs no code.
* Bad, because it is the failure this record exists to fix.

## More Information

* The design is the peer-status design, section 6 ("Answer needed now").
