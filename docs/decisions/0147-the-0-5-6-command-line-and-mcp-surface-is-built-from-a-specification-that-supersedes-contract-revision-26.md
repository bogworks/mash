---
status: accepted
date: 2026-11-20
decision-makers: Randy Bias (mash maintainer)
---

# The 0.5.6 command-line and MCP surface is built from a specification that supersedes contract revision 26

## Context and Problem Statement

The 0.5.6 command-line work was designed on paper as target outputs, status views and README pages, and revised against
many reviews up to contract revision 26. The maintainer rejected the output shape (put a pin in the paper work and get 0.5.6 built) and ruled that 0.5.6 is built from a specification: OpenCLI files for `mash` and `mashctl`, and
one for the MCP tools, with the exact output of each case, one implementation behind each command and its MCP tool, and
conformance proved by a test that walks the command tree against those files. What does the specification replace, and
where does it live?

## Decision Drivers

* One source of truth for every command's words, options, output and exit codes.
* A command and its MCP tool must not drift: one implementation, the same object.
* Conformance is a test, not a paper review.

## Considered Options

* Keep the target-output pages and contract revision 26 as the source.
* Build from the specification files, which are the single source (chosen).

## Decision Outcome

Chosen: build from the specification. The files `spec/mash.json`, `spec/mashctl.json` and `spec/mash-mcp.json` are the
single source and are kept in the repository unchanged from the planner's copy; the planner is their only editor and
nothing else goes in `spec/`. Where a file is wrong or silent the builder reports the command and the problem and does not
improvise.

It supersedes, from contract revision 26 and the target-output pages:

* the executing install script;
* an agent installing the plugin;
* the three-value confirmation of `mash credential install`;
* `mashctl endpoint delete` asking for a confirmation at a terminal (it takes `--yes`);
* pipe-to-shell installation;
* `mash endpoint status` and `mash server status`, and the six-row `mash status` (it is one line; `mash doctor` owns the checks);
* the exit codes 4 and 6 in `mash` (the codes are 0, 1 and 2).

Decisions already made that stay: TLS everywhere ([ADR 0146](0146-every-mash-and-mashctl-connection-requires-tls-loopback-included-and-one-override-insecure-plaintext-allows-plain-text.md))
and the owner, group, name address ([ADR 0145](0145-an-endpoint-address-is-owner-group-instance-and-0-5-5-is-a-hard-cut-with-no-migration-in-mash.md)).

### Consequences

* Good: each command, its flags, its output and its MCP tool are written once.
* Good: a test can walk the command tree and compare it with the files.
* Bad: the 0.5.6 paper work (targets, status views, README drafts) is withdrawn.

### Confirmation

A conformance test reads `spec/*.json` and walks the command tree of `mash` and `mashctl` and the tool list of the MCP
server (planner's acceptance test C1, started with slice S2).

## Pros and Cons of the Options

### Keep the target-output pages and contract revision 26

* Good: the work is done and reviewed many times.
* Bad: the maintainer rejected the output shape, and a paper review cannot prove the code matches.

### Build from the specification files

* Good: one source for words, options, output and exit codes; a test can check it.
* Bad: the paper drafts are withdrawn.

## More Information

The specification's own source and viewer live with the planner. The three files here are byte-identical to the planner's
copies, whose SHA-256 sums each commit message names.
