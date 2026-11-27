Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# Rules

The rules every mash command and tool follows.

1. A command reports what it did, in a line or a few. Like gh, git and aws.
   Explanation lives in the guide, the walkthrough, --help and the glossary,
   never in output.

2. An action prints one line. Past tense, naming the thing: 'Created endpoint
   acme.demo.bob', "Mail sent (number 42 in acme.demo.alice's inbox)". It does
   not print what another command already shows; it does print an identifier
   the person can get nowhere else.

3. Minimal by default, in text and in JSON alike; --detail gives the fuller
   answer in both. A command's usual answer is the least that answers the
   question, and --json prints that same least as an object. --detail asks for
   more: in text, 'label: value' lines after the usual line; with --json, a
   fuller object that contains the usual one. The fuller object's extra keys
   are the text's extra labels, in lower case with underscores ('holds
   endpoint' is holds_endpoint), so the two can be checked against each other.
   A program that needs the fuller object asks for it, as a person does. An
   MCP tool takes detail the same way. --wide is the same idea for a table:
   more columns, and it may pass 80 columns. --detail is one option with one
   meaning on every command of both tools, so nobody has to learn which
   commands have it: where a command has nothing more to say, --detail prints
   what it prints without it. Every --detail and --wide form that does add
   something is written out in this specification.

4. A list is a plain table. A header row in capitals, two spaces between
   columns, no borders. A repeated leading value may be left blank. --wide
   adds columns. An empty list is one sentence.

5. One thing is a list of 'label: value' lines. The labels are the JSON field
   names.

6. Status is one line. '&lt;what&gt;: &lt;state&gt;'. Everything behind it is
   in doctor.

7. An error is one line on stderr, and at most one hint. 'error: &lt;what
   failed&gt;', then 'hint: &lt;a command to run&gt;' when there is one. No
   hint on success, and no 'next step' lines.

8. --json prints one object. Always an object, never a bare array: a list is
   {"endpoints": [...]}. It is the same object the matching MCP tool returns
   as its structured result. Field names are the glossary's words. Both are
   produced by one implementation. One policy in both tools: it is indented
   when standard output is a terminal and one compact line when standard
   output is a pipe or a file, as gh and aws do. An error object follows the
   same policy. The specification shows the indented form.

9. A wrong command line gets one error line in a fixed form, and one hint. A
   missing argument: 'error: mash send needs &lt;address&gt;'. A bad value:
   'error: --kind must be one of blocker, question, task, result, fyi' or
   'error: --reply-to must be a whole number'. Two things that cannot go
   together: 'error: mash send takes the message text or --file, not both'. An
   option that does not exist: 'error: unknown flag --x', with no hint. Every
   other wrong command line has the command's own help as its hint: 'hint:
   mash send --help'. Exit 2, and nothing is attempted.

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

13. One exit rule, the same in both tools. 0 it worked, 1 it failed, 2 the
   command line was wrong and nothing was attempted. This holds for every
   command of mash and mashctl. A command that needs another code is named
   here as an exception; none is left implicit. There is one exception today:
   mash hook exits 0 whatever happens, so that a bad configuration or a failed
   check never fails the session that runs it. No command exits with a code
   above 2. status and doctor are not exceptions: they exit 1 when they found
   a problem (a warning alone is 0, or 1 with --strict), as a failure does. A
   command that did part of its work exits 1, says what is left, and is safe
   to run again.

14. Both doctors work the same way. mash doctor and mashctl doctor print every
   row every time, each marked ok, warn, FAIL or skip; a row that cannot run
   because an earlier one failed is skip, with what it needs. 'checks' in
   --json always lists every row, whatever failed. After the rows come one
   blank line and a count ('No problems.', '1 problem, 1 warning.'). A hint is
   a command of ours that fixes the problem, or there is no hint and the row's
   own words say what is wrong. A doctor prints at most one hint. It is never
   a command that only checks, and never one that replaces credentials, moves
   a folder or changes permissions unless the doctor found exactly the
   condition that command fixes. With no problem, the first warning that has a
   command that fixes it gets one hint. Exit 1 when there is a problem;
   warnings alone exit 0, or 1 with --strict. A server row shows the address
   as configured.

15. status and doctor report, they do not fail. Their lines go to stdout
   whatever they find, and they exit 1 when they found a problem. Any hint
   still goes to stderr.

16. Where a hint is printed. At a shell, every 'hint:' line goes to stderr,
   whichever stream the rest went to: after an error, after status, after
   doctor's count. The case pages show the hint beneath the text it follows,
   for reading; a case marked stdout means its lines other than the hint. In
   an MCP tool's text block the hint is the last line.

17. The server not answering reads the same everywhere. 'error: the server did
   not answer: tls://host:4222' and 'hint: mash doctor' (or mashctl doctor),
   exit 1. Two more server failures have fixed words: 'the server's
   certificate is not trusted: &lt;address&gt;' and 'the server did not offer
   TLS: &lt;address&gt;'.

18. Missing credentials read the same in every everyday command. When the
   chosen endpoint's creds file is not where the configuration says: 'error:
   acme.demo.bob has no credentials here: ~/.config/mash/acme.demo.bob.creds
   is missing' and 'hint: mash doctor', exit 1, from send, inbox, read and who
   alike; status says the same as its reason (reason_code creds_missing) and
   doctor in its creds row. Never the operating system's own wording. A
   recipient that takes no mail reads 'error: &lt;address&gt; is suspended or
   was deleted; nothing was sent', hint 'mash who', exit 1; never the server's
   wording. The sender cannot tell the two apart: all it sees is that no
   stream took the message, and a recipient deleted while the sender's
   directory cache still lists it looks the same; a marker in the directory
   record that tells them apart is planned. When the set-aside file
   &lt;address&gt;.creds.deleted is present, send, inbox, read and who say
   'error: &lt;address&gt;: this endpoint was deleted; its address cannot be
   used again' with 'hint: mash doctor', as mash status does.

19. A case written in brackets is a description. A case whose whole text
   stands in round brackets, such as '(the same JSON as --json ...)',
   describes the output in words; it is not a form to match byte for byte, and
   the conformance test skips it. The JSON example beside it shows the shape.
   A part of a case that varies from run to run (a key id, a cause as the
   server gave it) is named in that command's notes, with the words that stand
   before and after it; a test matches everything else byte for byte and
   nothing more loosely than the notes say.

20. Having no endpoint reads the same in every mash command. Three wordings,
   by where it is asked. At a shell with none installed: 'error: no endpoint
   is installed here (set mash up with the setup skill: see the quick start)'
   and no hint line, since no single command fixes a fresh machine (ADR 0168),
   exit 1; with --json {"error": ...} alone. At a shell with several installed
   and none chosen: 'error: more than one endpoint is installed here:
   &lt;addresses&gt;' and 'hint: mash &lt;command&gt; --endpoint &lt;the
   first, alphabetically&gt;', exit 2 (doctor is the exception: it checks
   every installed endpoint, ADR 0192). As an MCP tool in a session with none
   named: 'No endpoint is chosen for this session.' and 'hint: start the
   session with MASH_ENDPOINT=&lt;address&gt;'.

21. --json when a command fails. Prints {"error": ..., "hint": ...} on stdout
   and nothing on stderr, with the same exit code.

22. Every command that changes the mesh or this machine's setup takes
   --dry-run. It prints the usual line with 'Would' in front and changes
   nothing; with --json the object is the one the real run would print, with
   "dry_run": true. In the --json of every command that takes --dry-run
   (mashctl's changing commands, limits apply included, and mash join) the
   dry_run key is always present: false when the run is real (and mashctl init
   adds would_change, null unless it is a dry run). Sending a message is not
   such a change and has no --dry-run.

23. help is also a command. 'mash help' and 'mash help send' print exactly
   what 'mash --help' and 'mash send --help' print, as git, kubectl and gh do.
   The same for mashctl.

24. Every setting three ways. A flag, then an environment variable, then
   config.json. The flag wins.

25. One spelling. An address is owner.group.name everywhere. NATS's and SAP's
   things keep NATS's and SAP's names. The same thing has the same column
   heading and the same field name in every command.

26. A prefix selects, the way a domain name does. Where a command takes a
   prefix, acme means everything under acme, acme.demo everything under
   acme.demo, and a full address that one endpoint. acme.\* is accepted and
   means acme. There is no other pattern matching.

27. Options go anywhere after the tool's name. Every option, global or the
   command's own, is accepted before or after the command and its arguments,
   as with kubectl and gh. Help and documentation always write them last: mash
   status --endpoint acme.demo.alice.

28. A sender's words are always marked as the sender's. Wherever mash prints a
   message's text in full, every line starts with '&gt; ' and control
   characters are escaped, at a shell and in an MCP tool alike. It reads as
   quoted mail does to a person, and it shows an agent where data from someone
   else begins and ends. A table's MESSAGE column holds the sender's words
   too.

29. Encryption can be turned off in one way only, and it says so every time.
   Both tools talk to the server over TLS. The one way to turn that off is the
   environment variable MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1. There is no
   flag and no configuration-file key for it: this is the one setting that
   cannot be given three ways, so that it cannot be left on in a file and
   forgotten. While it is set, every command of both tools, errors included,
   first prints, on stderr: 'warning: no encryption to the server
   (MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION)'. The older names,
   MASH_INSECURE_PLAINTEXT and the key insecure_plaintext, are refused with an
   error naming the new one.

30. Only a destructive command asks. It asks you to type the address, and
   --yes skips the question.

31. Eighty columns, and color only on a terminal. --wide may be wider.
   NO_COLOR is honored.

32. In --json, one name for one fact. A yes-or-no fact has one name in every
   command of both tools: online is a boolean wherever it appears (mash who,
   mash status, mashctl endpoint list and show, mashctl status), never ready,
   ok or state for the same fact; an endpoint's suspended state is its own
   boolean, suspended. ready stays the name of "mail works for this endpoint"
   and "the server works" (mash status and mashctl status, which was ok). A
   count is named as a count: endpoint_count, online_count, wake_count. An
   unset field is null: never "" and never left out, so a command's keys are
   the same on every run. An empty list is [] and an empty map is {}. A list's
   key is plural: capabilities, not capability. The one exception is mash who
   --json, which follows ADR 0049 as amended by ADR 0184 (who JSON fields are
   added, never renamed or removed) and so keeps capability; it prints every
   field for every endpoint, an unset role, host or description as null and no
   capability as []. A doctor row's state (ok, warn, fail, skip) is a
   different fact and keeps its name.
