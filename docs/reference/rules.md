Generated from spec/*.json by scripts/refgen. Do not edit.

# Rules

The rules every mash command and tool follows.

1. A command reports what it did, in a line or a few. Like gh, git and aws.
   Explanation lives in the guide, the walkthrough, --help and the glossary,
   never in output.

2. An action prints one line. Past tense, naming the thing: 'Created endpoint
   acme.demo.bob', 'Mail sent (number 42).' It does not print what another
   command already shows; it does print an identifier the person can get
   nowhere else.

3. --verbose adds detail, --wide adds columns. Neither is the default.
   --verbose (-v) follows an action's line with 'label: value' lines. --wide
   adds columns to a table and may pass 80 columns. Every table's --wide form
   is written out in this specification.

4. A list is a plain table. A header row in capitals, two spaces between
   columns, no borders. A repeated leading value may be left blank. --wide
   adds columns. An empty list is one sentence.

5. One thing is a list of 'label: value' lines. The labels are the JSON field
   names.

6. Status is one line. '<what>: <state>'. Everything behind it is in doctor.

7. An error is one line on stderr, and at most one hint. 'error: <what
   failed>', then 'hint: <a command to run>' when there is one. No hint on
   success, and no 'next step' lines.

8. --json prints one object. Always an object, never a bare array: a list is
   {"endpoints": [...]}. It is the same object the matching MCP tool returns
   as its structured result. Field names are the glossary's words. Both are
   produced by one implementation.

9. A wrong command line gets one error line in a fixed form, and one hint. A
   missing argument: 'error: mash send needs <address>'. A bad value: 'error:
   --kind must be one of blocker, question, task, result, fyi' or 'error:
   --reply-to must be a whole number'. Two things that cannot go together:
   'error: mash send takes the message text or --file, not both'. An option
   that does not exist: 'error: unknown option --x'. The hint is always the
   command's own help: 'hint: mash send --help'. Exit 2, and nothing is
   attempted.

10. --help has one layout, generated from this specification. For a command,
   in this order: the one-line description; 'Usage:' and the synopsis;
   'Examples:'; 'Arguments:'; 'Options:'; 'Global options:'. Nothing else, 80
   columns. For the tool itself: its one-line description, then one heading
   per group in the specification's order ('Everyday:', 'Setup:',
   'Integration:', 'Raw SAP:' for mash; 'Mesh:', 'Endpoints:', 'Owners:' for
   mashctl) with each command and its one-line description indented beneath,
   then 'Global options:'. Help is never written by hand beside the
   specification.

11. Times read the way a person says them. '10s ago', '2m ago', '1h 12m'.
   Anything under a second, or in the future because two clocks disagree, is
   'just now'. --json carries the exact time.

12. Text read from a file or standard input loses one trailing newline. As the
   shell's own $(...) does. Nothing else in the text is changed.

13. Three exit codes. 0 it worked, 1 it failed, 2 the command line was wrong.
   Today the same failure exits 2, 3 or 6 depending on the command. A command
   that did part of its work exits 1, says what is left, and is safe to run
   again.

14. Both doctors work the same way. mash doctor and mashctl doctor print every
   row every time, each marked ok, warn, FAIL or skip; a row that cannot run
   because an earlier one failed is skip, with what it needs. 'checks' in
   --json always lists every row, whatever failed. After the rows come one
   blank line and a count ('No problems.', '1 problem, 1 warning.'). One hint
   follows when the first problem, or with no problem the first warning, has a
   command that fixes it; where no command does, there is no hint and the
   row's own text says what is wrong. Exit 1 when there is a problem; warnings
   alone exit 0, or 1 with --strict. A server row shows the address as
   configured.

15. status and doctor report, they do not fail. Their lines go to stdout
   whatever they find, and they exit 1 when they found a problem. Any hint
   still goes to stderr.

16. The server not answering reads the same everywhere. 'error: the server did
   not answer: tls://host:4222' and 'hint: mash doctor' (or mashctl doctor),
   exit 1. Two more server failures have fixed words: 'the server's
   certificate is not trusted: <address>' and 'the server did not offer TLS:
   <address>'.

17. Having no endpoint reads the same in every mash command. Three wordings,
   by where it is asked. At a shell with none installed: 'error: no endpoint
   is installed here' and 'hint: mash join <file>', exit 2. At a shell with
   several installed and none chosen: 'error: more than one endpoint is
   installed here: <addresses>' and 'hint: mash <command> --endpoint <the
   first, alphabetically>', exit 2. As an MCP tool in a session with none
   named: 'No endpoint is chosen for this session.' and 'hint: start the
   session with MASH_ENDPOINT=<address>'.

18. --json when a command fails. Prints {"error": ..., "hint": ...} on stdout
   and nothing on stderr, with the same exit code.

19. Every command that changes the mesh or this machine's setup takes
   --dry-run. It prints the usual line with 'Would' in front and changes
   nothing; with --json the object is the one the real run would print, plus
   "dry_run": true. That is mashctl's changing commands and mash join. Sending
   a message is not such a change and has no --dry-run.

20. help is also a command. 'mash help' and 'mash help send' print exactly
   what 'mash --help' and 'mash send --help' print, as git, kubectl and gh do.
   The same for mashctl.

21. Every setting three ways. A flag, then an environment variable, then
   config.json. The flag wins.

22. One spelling. An address is owner.group.name everywhere. NATS's and SAP's
   things keep NATS's and SAP's names. The same thing has the same column
   heading and the same field name in every command.

23. A prefix selects, the way a domain name does. Where a command takes a
   prefix, acme means everything under acme, acme.demo everything under
   acme.demo, and a full address that one endpoint. acme.* is accepted and
   means acme. There is no other pattern matching.

24. Options go anywhere after the tool's name. Every option, global or the
   command's own, is accepted before or after the command and its arguments,
   as with kubectl and gh. Help and documentation always write them last: mash
   status --endpoint acme.demo.alice.

25. A sender's words are always marked as the sender's. Wherever mash prints a
   message's text in full, every line starts with '> ' and control characters
   are escaped, at a shell and in an MCP tool alike. It reads as quoted mail
   does to a person, and it shows an agent where data from someone else begins
   and ends. A table's MESSAGE column holds the sender's words too.

26. Only a destructive command asks. It asks you to type the address, and
   --yes skips the question.

27. Eighty columns, and color only on a terminal. --wide may be wider.
   NO_COLOR is honored.
