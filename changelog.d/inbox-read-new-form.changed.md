`mash inbox` and `mash read` have a new form, the same in the command line and in the MCP tools of the same names.
`mash inbox` prints a summary line (`2 messages for acme.demo.alice, 2 new, 1 urgent.`) and a table with the number,
sender, age, state (`new` until the session has been told of the message, then `read`), urgency, kind and the first line
of the text; `--new` lists only the new ones, `--after NUMBER` starts after a number and `--wide` adds the full sender
address, the trust label and the size. `--limit` is at most 100 in both doors (the command line took 1000 before; a
larger value is now a usage error). It says `More than 15 are waiting.` when more messages are waiting than `wake_count`
and `The oldest has waited 1h 12m.` when the oldest has waited longer than `wake_age`; both settings are new
(`MASH_WAKE_COUNT` or `wake_count`, default 15, 0 turns it off; `MASH_WAKE_AGE` or `wake_age`, default `1h`, such as `30m`
or `2d`, 0 turns it off). `mash read NUMBER` prints labelled lines (number, from, sent, trust, kind, urgent, and reply to
when the message answers one) and then the whole text, unquoted; a number the inbox does not hold is
`error: acme.demo.alice has no message 9`. Both take `--json`, which prints the same object as the MCP tool. The read
tool's argument is now `number` (it was `seq`) and only a whole number is accepted. The old one-line `#seq` listing and
the quoted `--- #5 from ...` form of `read` are gone; the hook still delivers mail in its own form.
