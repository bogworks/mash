An `answer: "now"` ask that has no reply by `answer_within` wakes you with a notice from mash. The notice gives the recipient's
reported state, for example `No answer to #7 to peer after 10m. It reports: blocked (question).`, or says that the recipient sent
mail not marked as an answer. Asks that expired while `mash serve` was not running are reported once, at the next start
([ADR 0135](docs/decisions/0135-an-ask-marked-answer-now-is-woken-by-its-verified-reply-whatever-the-replys-kind.md)).
